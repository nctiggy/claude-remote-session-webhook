package httpapi

// restart.go is the seventh route on the browser door that changes something,
// and the second of the seven that changes *this daemon* rather than a session
// it manages (milestone 9).
//
// It is the update's last step and none of the six before it. No release is
// asked for, nothing is downloaded, nothing is verified, nothing is made
// executable and nothing is renamed: what comes back up is the binary that is
// already installed. That is the whole argument for a browser being allowed to
// do this at all — the operator argued that door open for the update, which
// installs code from the internet, and a restart is strictly less than an
// update rather than a different kind of thing.
//
// Sessions survive it, which is what makes this a control worth offering rather
// than a foot-gun. They are tmux windows on this host and this process is not
// their parent; a daemon that starts again adopts them, with their deadlines
// taken from the host's own session_created rather than from when it noticed
// them. That is #63's arrangement and this route only relies on it.
//
// The step this file owns outright is the exit, and it is the update's own: the
// answer is written, the record reaches the trail, and only then may the process
// end. See exitGrace in update.go for why the end is timed rather than taken.

import (
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/nctiggy/claude-remote-session-webhook/internal/buildinfo"
)

// patternDashboardRestart is the restart route.
//
// It is spelled the other six's way and for their reasons: under /dashboard/ so
// milestone 1's surface is untouched (FR-005) and a grep for the prefix finds
// every browser-initiated change, and the method inside the pattern so a GET
// here matches no pattern of this route's, falls to handleUnrouted's `/`, and is
// answered as a path nothing claims rather than as a 405 with an Allow header
// (FR-033).
//
// It carries no {id}, like the update and the create: a restart names no
// session. What it ends is the process every session on this host is managed by,
// which is why there is no ownership question behind it and no uniform not-found
// for it to give.
const patternDashboardRestart = "POST /dashboard/restart"

// The refusals this route authors. Each is a sentinel written here, so no record
// can carry a byte the caller chose (FR-042) — and this route reads exactly one
// field, so there is nothing else for one to carry.
var (
	// errRestartUnconfirmed is a restart without `confirm=yes`. It is its own
	// sentinel rather than the update's, for the reason every reason on this door
	// is: what tells one action's records from another's is this.
	errRestartUnconfirmed = errors.New("a browser restart arrived without the confirming step")

	// errRestartUnwired is the route reached on a server with no way to end this
	// process behind it. It should not happen in a daemon built by New, and it is
	// a refusal rather than a panic for the reason every other should-not-happen
	// branch on this door is one: fail-closed is only a property if it holds on
	// the paths that do not happen.
	errRestartUnwired = errors.New("the restart route was reached on a daemon with no way to end this process")
)

// restartFromBrowser is POST /dashboard/restart.
//
// Everything that authorises it has already run: handleAction wrapped this
// handler in the gate, so layer 1 has verified an identity, the browser has said
// the request came from this page, and the form has carried a token minted for
// that identity. What is left is the confirming step and the exit.
//
// The confirming step is read first, ahead of anything else, which is the
// destroy's own ordering rule (FR-029, FR-029a). It costs this host less than a
// destroy does — nothing is torn down and nothing is downloaded — but a request
// that was never going to be carried out must not end the process on its way to
// being refused, and every action on this door reads its confirmation before it
// acts.
func (s *Server) restartFromBrowser(w http.ResponseWriter, r *http.Request) {
	if _, ok := OperatorFrom(r.Context()); !ok {
		// Fail closed on the path that should not happen, the way every other
		// handler on this door does: the gate in front puts the operator in the
		// context, so a false here is a route wired without one.
		AuditFrom(r.Context()).Deny(errDashboardNoOperator.Error())
		s.refuseBrowser(w)
		return
	}

	// Read from PostForm and never Form, for the reason the gate reads the token
	// that way: a confirmation this daemon would accept from a query string is a
	// process a link can end. The form itself was parsed by the gate, under the
	// configured body limit.
	if r.PostForm.Get(fieldConfirm) != confirmYes {
		AuditFrom(r.Context()).Deny(errRestartUnconfirmed.Error())
		s.redirectOutcome(w, r, outcomeRestartUnconfirmed)
		return
	}

	if !s.updates.restartable() {
		AuditFrom(r.Context()).Deny(errRestartUnwired.Error())
		s.redirectOutcome(w, r, outcomeRestartRefused)
		return
	}

	// The order the two things that must survive the exit require. The answer is
	// written and pushed onto the connection, the record reaches the trail, and
	// only then is the end of this process scheduled — an exit before either
	// would leave the operator watching a request that never answered and a
	// restart with nothing in the journal to say it happened (FR-041).
	//
	// The record is emitted here rather than left to the middleware's deferred
	// emit, and the reason is the goroutine below rather than this handler not
	// returning: it does return, so the middleware would ordinarily get there
	// first. Ordinarily is the whole problem. Once that goroutine exists, the
	// record's write and this process's end are unordered, and the interleaving
	// that loses is a restart that happened with nothing in the trail to say who
	// asked for it. Emitting first makes the record a fact before there is
	// anything that could end the process. The emit is guarded against a second
	// one, so the deferred emit on the way out writes nothing more.
	//
	// No test can see the difference — both orderings leave one record long
	// before the grace period is up — which is why it is argued here.
	s.renderRestarting(w, r)
	s.emit(AuditFrom(r.Context()))
	if err := http.NewResponseController(w).Flush(); err != nil { //nolint:bodyclose // false positive: a ResponseController is not a response and has no body to close.
		// Reported and not acted on. A daemon that stayed up because it could not
		// flush a page would be one that quietly declined the only thing this
		// route does, and the operator would have no way to tell that from a
		// restart that worked.
		s.report(fmt.Errorf("flush the answer to a restart before exiting: %w", err))
	}
	// Exit after the handler returns, not inside it. The reasoning is written
	// above exitGrace, and it was paid for once: os.Exit inside the handler
	// severs the connection before net/http has finished the response, so what
	// reaches the proxy is a broken socket and what reaches the operator is a
	// Cloudflare 502 for a restart that was working.
	//
	// Nothing is waited on. The operator asked for this process to end, and the
	// end must not become conditional on a browser still being there to hear
	// about it.
	go func() {
		time.Sleep(exitGrace)
		s.updates.installer.ExitForRestart()
	}()
}

// restartable reports whether there is a way to end this process behind this
// route at all.
//
// It asks for the installer alone rather than for selfUpdate.wired(), because
// the installer is the only one of the three this route reaches: a restart that
// refused because a release feed was missing would be refusing over a
// collaborator it never calls. The shipping build wires all three together in
// liveSelfUpdate, so in production the two questions have the same answer — what
// the narrower one buys is that the reason a refusal gives is true.
//
// A server built by newServer has none of them, which is deliberate and is what
// keeps a test that reaches this route from ending the process running the
// suite.
func (u selfUpdate) restartable() bool { return u.installer != nil }

// renderRestarting answers an accepted restart with the page it was asked from.
//
// It is the update's answer and not a redirect, for the update's own reason: a
// 303 tells the browser to go and ask for a page from a daemon that is in the
// act of stopping, so the redirect is delivered, the browser follows it, and the
// operator watches their own restart turn into a connection error at the one
// moment they most need to be told it is working. The page stays where it is and
// waits instead. With no script it is still a page rather than an error.
//
// Becoming is the version this daemon is coming back as, which is the version it
// is already running: a restart installs nothing. It is set all the same,
// because it is what the waiting state is keyed on and what the script polls for
// — a daemon answering with this version is a daemon that is back — and
// Restarting is what keeps the sentence beside it from claiming an installation
// that is not happening.
//
// No update panel is composed. The waiting state renders instead of the update
// form rather than beside it, so a panel would be a section this answer does not
// show and a token minted for a form it does not draw.
func (s *Server) renderRestarting(w http.ResponseWriter, r *http.Request) {
	operator, ok := OperatorFrom(r.Context())
	if !ok {
		// Unreachable through the gate, which is why this is a refusal rather
		// than a nil dereference waiting to be found.
		AuditFrom(r.Context()).Deny(errDashboardNoOperator.Error())
		s.refuseAction(w)
		return
	}

	rows := settingsOf(s.cfg)
	s.renderPage(w, r, http.StatusOK, "settings", settingsView{
		Operator: operator,
		Settings: rows,
		// The same sentence GET /settings composes, though this answer shows the
		// Updates section and so renders none of it: one page, one account of which
		// door is live, whichever route composed it.
		Sections:   sectioned(rows, doorSentence(s.browser)),
		Shown:      sectionUpdates,
		ConfigFile: s.cfg.FilePath,
		Becoming:   buildinfo.Version,
		Restarting: true,
	})
}
