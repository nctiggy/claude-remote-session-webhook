package httpapi

// actions.go is the browser door's mutating half — the four routes
// contracts/actions.md fixes, and the answers they share. The gate that admits a
// request to any of them is in browser.go, next to the layer-1 door it composes
// with; what lives here is what happens *after* a request has been admitted.
//
// The first of those answers is the one below, because it is the answer three of
// the four give more often than they give any other: an action against a session
// that is not this operator's to act on.

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"github.com/nctiggy/claude-remote-session-webhook/internal/session"
)

// bodyActionNotFound is the uniform not-found, byte for byte from
// contracts/actions.md.
//
// Three causes end here — an identifier no session ever had, one another
// operator owns, and one whose session is already gone — and none of them is
// distinguishable from outside (FR-017, SC-009). The difference between them is
// what enumeration is made of: an answer that separated "never existed" from
// "not yours" would let anyone who can reach this door count the sessions on
// this host and learn the identifiers of the ones they may not touch. Which of
// the three it really was is on the record the caller's handler emits, where the
// operator can read it.
//
// It is deliberately not bodyActionRefused. A refusal says the request was not
// accepted; this says the thing it named is not here, and an operator whose
// session was reaped between rendering a card and clicking it is owed the second
// rather than the first. The two are told apart by status as well — 403 against
// 404 — and both are uniform *within* themselves, which is where a caller
// probing one of them lives.
//
// Like every other body this door writes it references no stylesheet, no script
// and no external origin, so it renders the same under the CSP as without one.
var bodyActionNotFound = []byte(`<!doctype html><title>not found</title><p>No such session.</p>`)

// notFoundAction answers an action route whose {id} resolved to nothing this
// operator may act on (FR-017).
//
// It takes no reason argument, for the reason refuseBrowser and refuseAction
// take none: there is nothing a caller could pass that would be allowed to
// change a byte of what is written, so the parameter would only be an
// invitation — and it is the parameter, not the intent, that a later hand
// reaches for when one of the four routes wants to be helpful about which of the
// three causes applied. The record is where the cause goes, written by the
// handler that did the lookup and knows which sentinel came back.
//
// It writes the response and nothing else. The audit reason is the caller's,
// deliberately: resolveReason already turns a resolver error into the trail's
// existing vocabulary, and a not-found that emitted a record of its own would be
// a second record for one request (FR-041) — the fleet page and the session page
// audit the same failure this way today.
//
// The length is written rather than left to net/http, so that byte-identical is
// a property of this function rather than a property of how the response
// happened to be buffered. Everything else the response carries — nosniff among
// it — was written by setBrowserSecurityHeaders before layer 1 ran, so this
// leaves with the identical header set to a served page (FR-026).
func (s *Server) notFoundAction(w http.ResponseWriter) {
	w.Header().Set(headerContentType, contentTypeHTML)
	w.Header().Set(headerContentLength, strconv.Itoa(len(bodyActionNotFound)))
	w.WriteHeader(http.StatusNotFound)
	if _, err := w.Write(bodyActionNotFound); err != nil {
		s.report(fmt.Errorf("write the browser door's action not-found: %w", err))
	}
}

// --- Destroy ---------------------------------------------------------------
//
// The first route on this door that changes something, and the highest
// consequence write in the daemon: it ends an unsandboxed shell (US1).

// patternDashboardDestroy is the destroy route, from contracts/actions.md's table.
//
// It lives under /dashboard/ with the other three so milestone 1's surface is
// untouched (FR-005) and a grep for the prefix finds every browser-initiated
// change. The wildcard is spelled through pathValueID for the reason
// patternSessionView spells it that way: the name in the pattern and the name
// read back out of the request cannot then drift apart.
//
// The method is part of the pattern, which is what makes a GET here an unknown
// route rather than a 405. ServeMux answers 405 only when no other pattern
// matches, and handleUnrouted's method-less `/` matches everything — so a GET
// falls to the browser door's not-found page with no Allow header, which is what
// the contract requires (FR-033) and what T008 asserts.
const patternDashboardDestroy = "POST /dashboard/sessions/{" + pathValueID + "}/destroy"

// The confirming step, in the two halves it is read as (FR-029,
// contracts/actions.md).
//
// Exactly `yes`, compared rather than parsed: `on`, `true`, `1` and an empty
// value are all things a stray checkbox or a hand-built request produces, and a
// destroy is the one action on this door that cannot be taken back. The field
// costs a deliberate act to send, which is the whole of what FR-029 asks for.
const (
	fieldConfirm = "confirm"
	confirmYes   = "yes"
)

// The four things a destroy can answer, each a fragment because the caller is a
// card on a page rather than a client reading JSON.
//
// Only the 409 is quoted from contracts/actions.md byte for byte. The other
// three are authored here, the way milestone 2 authored the empty state's and
// the not-found page's copy at their call sites: the contract fixes what each
// one must *say* — a removal marker, a refusal that tore nothing down, a failure
// that states it failed — and does not fix the words. Every one of them is a
// text node and none carries colour, markup, or a control, which is FR-030 and
// FR-031 met at the point the answer is written rather than in the stylesheet
// that will style it (T007).
//
// They share one class with the 409 so the three outcomes are one component and
// not three. What tells them apart to an operator is the sentence, not the
// styling — a card that went quiet, or changed colour and said nothing, would be
// the silent revert FR-031 forbids.
var (
	// bodyActionDestroyed is the removal marker: the card is replaced by the
	// statement that the session it described is gone. It claims only what the
	// host confirmed — Manager.Destroy returns nil after confirmGone said so, and
	// never on the strength of the kill alone (FR-019).
	bodyActionDestroyed = []byte(`<p class="card-outcome">Session destroyed. The host confirmed its window is gone.</p>`)

	// bodyActionUnconfirmed answers a destroy that never carried the confirming
	// step. It says what did not happen, because that is the fact the operator
	// needs: a control that failed silently and left the card in place would be
	// indistinguishable from one that did nothing at all.
	bodyActionUnconfirmed = []byte(`<p class="card-outcome">This destroy was not confirmed, so nothing was torn down.</p>`)

	// bodyActionTeardownUnverified is contracts/actions.md's literal, and the one
	// non-uniform failure body on these routes.
	//
	// Being specific here discloses nothing: this operator has already been
	// matched to this session by the ownership check above, so the one fact it
	// carries is a fact about their own session. Being specific is also the point.
	// The alternative is telling an operator a session was torn down while a live
	// unsandboxed shell may have survived it, which is the one thing Principle VI
	// does not let this daemon say quietly.
	bodyActionTeardownUnverified = []byte(`<p class="card-outcome">Teardown could not be verified. This session may still be running on the host.</p>`)

	// bodyActionDestroyFailed is the fail-closed answer for a teardown that failed
	// for a reason no sentinel explains. It deliberately does not say the session
	// may still be running — that is a specific claim, and a failure nobody
	// classified is not evidence for it — and it deliberately does say something,
	// because an empty 500 renders as a card that reverted (FR-031).
	bodyActionDestroyFailed = []byte(`<p class="card-outcome">The session could not be destroyed.</p>`)
)

// errDestroyUnconfirmed is what a destroy arriving without `confirm=yes` is
// recorded as.
//
// It is a refusal worth a name of its own rather than a shrug. The trail is
// where an operator would see a page submitting destroys without the confirming
// step — which is either a broken template or something that is not this
// daemon's page — and neither is visible from the 400 the caller gets.
var errDestroyUnconfirmed = errors.New("a browser destroy arrived without the confirming step")

// destroyFromBrowser is POST /dashboard/sessions/{id}/destroy (US1,
// contracts/actions.md).
//
// Everything that authorises it has already run: the gate in browser.go is
// wrapped around this handler by handleAction, so by the time it is called layer
// 1 has verified an identity, the browser has said the request came from this
// page, and the form has carried a token minted for that identity. What is left
// here is the ownership check, the confirming step, and the teardown itself.
//
// The order below is the same order the API's destroy runs in, for the same
// reasons, with one step in front of it: nothing is looked up until the request
// is one this daemon would act on at all. A request that was never going to be
// carried out costs no store read and no tmux command, and "refused" and
// "refused after acting" cannot become the same event.
//
// The teardown is Manager.Destroy and nothing else. It is the same verified path
// DELETE /sessions/{id} uses (FR-005 keeps that route unchanged), so a browser
// destroy and an API destroy cannot disagree about what "gone" means — and there
// is deliberately no second entry point that skips the verification, because
// AR-004 says a force path is not to exist.
func (s *Server) destroyFromBrowser(w http.ResponseWriter, r *http.Request) {
	operator, ok := OperatorFrom(r.Context())
	if !ok {
		// Fail closed on the path that should not happen, the way every other
		// handler on this door does: the gate in front puts the operator in the
		// context, so a false here is a route wired without one.
		AuditFrom(r.Context()).Deny(errDashboardNoOperator.Error())
		s.refuseBrowser(w)
		return
	}

	// The router matches any single segment, and the contract's `{id}` is 32
	// lowercase hex — so a path carrying anything else is not a route this daemon
	// serves, and is answered as one nothing claims. It discloses nothing that the
	// address of every card does not already: an identifier off that alphabet
	// cannot name a session on this host, so refusing it early is a shape check
	// and not an existence oracle. Sessions that could exist are all answered by
	// the uniform not-found below, which is where FR-017 lives.
	id := r.PathValue(pathValueID)
	if !routableID(id) {
		AuditFrom(r.Context()).Deny(errScopeNoRoute.Error())
		s.renderNotFound(w, r, operator)
		return
	}

	// Ahead of the lookup, so "nothing is torn down" is a property of the control
	// flow rather than of every later branch remembering it (FR-029). The field is
	// read from PostForm and never Form for the reason the gate reads the token
	// that way: a confirmation this daemon would accept from a query string is a
	// destroy that a link can carry.
	//
	// The form itself was parsed by the gate, under the configured body limit. A
	// handler that read r.Body here would find it drained.
	if r.PostForm.Get(fieldConfirm) != confirmYes {
		AuditFrom(r.Context()).Deny(errDestroyUnconfirmed.Error())
		s.writeFragment(w, http.StatusBadRequest, bodyActionUnconfirmed)
		return
	}

	// Manager.View, which is what a browser gets: it checks ownership without a
	// per-session credential, because a browser holds none and must not be given
	// one (FR-034a). An id that never existed, one another operator owns, and one
	// whose session is already gone are one answer to the caller (FR-017, SC-009)
	// and three sentinels on the record.
	live, err := s.sessions.View(id, operator.Owner)
	if err != nil {
		// resolveReason rather than a reason of this route's own, for the reason
		// sessionPage uses it: the trail already has a vocabulary for these, and an
		// operator should not need a second one because the request arrived through
		// a form. Never the wrapped error, which would carry the caller's spelling
		// of the id (FR-042).
		AuditFrom(r.Context()).Deny(resolveReason(err).Error())
		s.notFoundAction(w)
		return
	}
	// The id off the daemon's own record, never the bytes in the path, and stamped
	// before the teardown so the 409 below names the session it could not confirm.
	// That is what makes an unverified teardown findable in the trail rather than
	// merely present in it.
	AuditFrom(r.Context()).SetSessionID(live.ID)

	if err := s.sessions.Destroy(r.Context(), live); err != nil {
		s.refuseBrowserDestroy(w, r, err)
		return
	}

	s.writeFragment(w, http.StatusOK, bodyActionDestroyed)
}

// refuseBrowserDestroy maps a Destroy failure onto the answer contracts/actions.md
// gives it, and it is refuseDestroy's shape on the other door for the same
// reason: one case has a status of its own because an operator has to be able to
// find it.
//
// The record is retained on both branches, and that is Manager.Destroy's doing
// rather than this function's — it drops the record only on a confirmed teardown.
// A record is the only thing carrying an owner and two deadlines for a session
// that may still be running, and adoption runs at startup, so a record dropped
// here would be a live unsandboxed shell the running daemon has forgotten for
// good.
//
// The reasons are the API door's own, deliberately. The same fact deserves the
// same words in the journal whichever door reported it; what tells the two apart
// is the action the middleware already set — dashboard.destroy against
// session.destroy — which is what keeps an operator's count of one from being a
// count of the other.
func (s *Server) refuseBrowserDestroy(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, session.ErrOrphanedSession) {
		AuditFrom(r.Context()).Deny(errDestroyOrphaned.Error())
		s.writeFragment(w, http.StatusConflict, bodyActionTeardownUnverified)
		return
	}
	AuditFrom(r.Context()).Deny(errDestroyRefused.Error())
	s.writeFragment(w, http.StatusInternalServerError, bodyActionDestroyFailed)
}

// --- Create ----------------------------------------------------------------
//
// The second route on this door that changes something, and the one that starts
// an unsandboxed shell rather than ending one (US2).

// patternDashboardCreate is the create route, from contracts/actions.md's table.
//
// It carries no {id}, because a create names no session — the session is what it
// makes. So there is no shape check in front of it and no ownership question
// behind it: what this route makes is owned by the identity layer 1 verified, and
// nothing the form carries can name another owner (FR-012). A create is therefore
// the one action on this door with no uniform not-found to give.
//
// The method is part of the pattern for the reason the destroy's is: a GET here
// matches no pattern of this route's, falls to handleUnrouted's `/`, and is
// answered as a path nothing claims — never a 405 and never an Allow header
// (FR-033).
const patternDashboardCreate = "POST /dashboard/sessions"

// The two fields a create carries beside the token the gate reads
// (contracts/actions.md).
//
// Spelled once here so the field this handler reads has one spelling. The form
// that submits them is T010's, in a template set parsed with no function map, so
// the markup spells them a second time and a test is what holds the two together
// — the arrangement `confirm` already has on the card.
const (
	fieldName    = "name"
	fieldWorkDir = "work_dir"
)

// The four things a create can answer other than a card, each a fragment for the
// reason the destroy's are: the caller is a page, not a client reading JSON.
//
// None is quoted from contracts/actions.md, which fixes what each must *say* and
// not the words — a fragment naming the field for a bad name, one that speaks of
// permission and never of existence for a working directory, one that says the
// limit was reached, and one that says the session could not be started. Each is
// a text node carrying no colour, no markup and no control, which is FR-030 and
// FR-031 met where the answer is written; they share the class the destroy's
// outcomes carry, because what an operator is told after an action is one
// component and not five.
var (
	// bodyActionCreateBadName answers a name ValidateName refused. It names the
	// field and states the alphabet, and says nothing whatever about the
	// filesystem: the two fields are validated in one call, and an answer that
	// mentioned the directory while refusing the name would be a way to ask
	// questions about the host by sending a name that cannot pass.
	bodyActionCreateBadName = []byte(`<p class="card-outcome">That is not a usable session name. Use letters, digits and hyphens, up to 64 characters.</p>`)

	// bodyActionCreateBadWorkDir is the single answer every working-directory
	// refusal shares — traversal, an absolute path outside the approved roots, a
	// symlink that resolves out of them, a path that is not a directory, and a
	// path that is not there at all (FR-012).
	//
	// One message is the whole point. ResolveWorkDir tells those apart and the
	// trail keeps the difference, but a caller who could read it would hold a
	// filesystem oracle: ask for a path, learn from the wording whether it exists,
	// and map a host through a form. It speaks only of what this daemon permits,
	// which is a fact about the daemon's own configuration rather than about the
	// machine it runs on.
	bodyActionCreateBadWorkDir = []byte(`<p class="card-outcome">This daemon may not start a session in that working directory.</p>`)

	// bodyActionCreateLimited is the 429, and it is one body for the
	// concurrent-session cap and for the create rate alike — the arrangement
	// bodyTooManyRequests has on the API door and for the reason it has it: the
	// answer to either is to wait or to destroy something, so there is nothing a
	// caller could do with the difference. Which of the two it was is on the
	// record.
	//
	// It says nothing was started, because that is the fact the operator needs: a
	// refusal that only named a limit would leave them wondering whether a session
	// they cannot see is now running.
	bodyActionCreateLimited = []byte(`<p class="card-outcome">No session was started: this host is at its limit for now. Destroy one, or try again shortly.</p>`)

	// bodyActionCreateFailed is the fail-closed answer for a create that failed
	// for a reason no sentinel explains, and for the orphan case with it.
	//
	// It deliberately does not say a shell may have survived. The record Create
	// kept is what the reaper will collect, the caller holds no credential for it
	// — the token was discarded — and a create that ended here started nothing the
	// operator can drive, so the honest short answer is the one they can act on.
	// The orphan itself is the trail's business, under the same reason the API
	// door records for it, where an operator is already reading.
	bodyActionCreateFailed = []byte(`<p class="card-outcome">The session could not be started.</p>`)
)

// createFromBrowser is POST /dashboard/sessions (US2, contracts/actions.md).
//
// Everything that authorises it has already run: handleAction wrapped this
// handler in the gate, so layer 1 has verified an identity, the browser has said
// the request came from this page, and the form has carried a token minted for
// that identity. What is left is the budget, the manager, and the card.
//
// The validation is Manager.Create's and nothing else. ValidateName and
// ResolveWorkDir are the rules milestone 1 wrote — cleaned, symlink-resolved,
// contained at a path-separator boundary — and this route reaches them by calling
// the same method POST /sessions calls (AR-008). A handler that re-checked here
// would be a second copy of the allowlist, free to disagree with the first about
// which directories on this host may hold an unsandboxed shell.
//
// The bearer token is discarded at the assignment below and never named (FR-013).
// A session created from the browser is drivable from the browser; driving it
// from the API would need a credential the operator was never handed, which is
// the accepted consequence of not handing credentials to a page — a page can keep
// one only in a URL or in something a script can read, and neither may hold the
// key to an unsandboxed shell.
func (s *Server) createFromBrowser(w http.ResponseWriter, r *http.Request) {
	operator, ok := OperatorFrom(r.Context())
	if !ok {
		// Fail closed on the path that should not happen, the way every other
		// handler on this door does: the gate in front puts the operator in the
		// context, so a false here is a route wired without one.
		AuditFrom(r.Context()).Deny(errDashboardNoOperator.Error())
		s.refuseBrowser(w)
		return
	}

	// The same per-caller budget POST /sessions spends, keyed by the same identity
	// (FR-037). Both doors resolve to one owner by construction (FR-037a), so the
	// operator has one create budget rather than one per door — a second door with
	// a budget of its own would be a way to spend twice as fast by alternating.
	//
	// Ahead of the manager, so a request over budget costs no path resolution and
	// no tmux command, which is where limitCreates sits on the other door and for
	// the same reason. It cannot be that middleware itself: that one spends the
	// layer-2 caller CallerFrom returns, and there is none on this door.
	if !s.creates.allow(operator.Owner) {
		AuditFrom(r.Context()).Deny(errCreateRateExceeded.Error())
		s.writeFragment(w, http.StatusTooManyRequests, bodyActionCreateLimited)
		return
	}

	// The form was parsed by the gate, under the configured body limit, and the
	// two fields are read from PostForm rather than Form for the reason the token
	// is: a create this daemon would accept from a query string is a session a
	// link can start.
	//
	// The owner is the identity layer 1 verified and never a field. A form that
	// could name its own owner would make every later ownership check a formality,
	// which is why there is no owner field to read and nothing here that would
	// read one.
	//
	// The token is discarded here, in the assignment: not stored, not logged, not
	// passed on, not given a name that a later edit could reach for. It is the
	// strongest form FR-013 has in this language.
	created, _, err := s.sessions.Create(r.Context(), session.CreateRequest{
		Owner:   operator.Owner,
		Name:    r.PostForm.Get(fieldName),
		WorkDir: r.PostForm.Get(fieldWorkDir),
	})
	if err != nil {
		s.refuseBrowserCreate(w, r, err)
		return
	}

	// The id off the daemon's own record, never a byte the form carried — the rule
	// SetSessionID exists to keep. It is what makes a create findable in the trail
	// rather than merely present in it.
	AuditFrom(r.Context()).SetSessionID(created.ID)

	// The card, rendered from the one projection the fleet and the session page
	// render from (cardOf), so a session's first appearance describes it exactly as
	// every later one will.
	//
	// The page token is the one the request carried, rendered back into the new
	// card's own form rather than a second one minted here. A page is rendered for
	// one identity at one instant and every form on it carries that render's token
	// (T004); this card joins exactly that page, so the token that belongs on it is
	// that page's own. Minting would give one card a later expiry than its
	// siblings, and would put a mint *after* a session exists — where the only
	// honest answer to a failure is a 500 for a create that succeeded. Nothing
	// arbitrary can reach this line: admitAction verified the value as a MAC over
	// this operator's identity before the handler ran, so what is written back is a
	// value this daemon minted for this browser and not caller-chosen text.
	//
	// renderPage rather than a fragment writer of its own. What it does is what a
	// fragment built from a template needs — built into a buffer first, so a
	// template that failed halfway cannot leave a browser holding half a card under
	// a 200, and answered with no body at all when it does. That answer is right
	// here for a reason it is not right anywhere else on this route: the session was
	// started, so a fragment saying it could not be would be a lie, and the fleet
	// the operator reloads will show the card this render could not.
	s.renderPage(w, r, http.StatusOK, "session-card", cardOf(*created, s.clock.Now(), r.PostForm.Get(fieldPageToken)))
}

// refuseBrowserCreate maps a Create failure onto the answer contracts/actions.md
// gives it, and it is refuseCreate's shape on the other door: the same four
// branches, over the same sentinels, differing only in what a browser is handed.
//
// The reasons are the API door's own, deliberately, exactly as refuseBrowserDestroy
// borrows that door's. The same fact deserves the same words in the journal
// whichever door reported it; what tells them apart is the action the middleware
// already set — dashboard.create against session.create.
//
// createReason is what turns the two general sentinels into the specific one an
// operator can act on — outside the roots, not a directory, unresolvable — and it
// reaches the record alone. The caller gets one body per field whichever of them
// applied, which is the half FR-012 is about.
func (s *Server) refuseBrowserCreate(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, session.ErrInvalidName):
		AuditFrom(r.Context()).Deny(createReason(err).Error())
		s.writeFragment(w, http.StatusBadRequest, bodyActionCreateBadName)
	case errors.Is(err, session.ErrInvalidWorkDir):
		AuditFrom(r.Context()).Deny(createReason(err).Error())
		s.writeFragment(w, http.StatusBadRequest, bodyActionCreateBadWorkDir)
	case errors.Is(err, session.ErrTooManySessions):
		// A full fleet is a 429 and not a 400, for the reason the API door gives:
		// nothing the operator sent is wrong, and the only fix is to wait or to
		// destroy something.
		AuditFrom(r.Context()).Deny(errCreateCapReached.Error())
		s.writeFragment(w, http.StatusTooManyRequests, bodyActionCreateLimited)
	case errors.Is(err, session.ErrOrphanedSession):
		AuditFrom(r.Context()).Deny(errCreateOrphaned.Error())
		s.writeFragment(w, http.StatusInternalServerError, bodyActionCreateFailed)
	default:
		AuditFrom(r.Context()).Deny(errCreateRefused.Error())
		s.writeFragment(w, http.StatusInternalServerError, bodyActionCreateFailed)
	}
}

// writeFragment writes one action's answer: the type, the length, the status and
// the bytes, in that order and in one place.
//
// The length is written by hand for the reason refuseAction and notFoundAction
// write theirs — so that what a response promises is a property of this function
// rather than of how the response happened to be buffered. Everything else it
// carries was written by setBrowserSecurityHeaders before layer 1 ran, so an
// action's answer leaves with the identical header set to a served page (FR-026).
//
// notFoundAction does not call it, and that is AR-008 rather than an oversight:
// that function is T005's, its body is a different body, and rewriting a
// neighbouring function is the churn this milestone's ground rules forbid.
func (s *Server) writeFragment(w http.ResponseWriter, status int, body []byte) {
	w.Header().Set(headerContentType, contentTypeHTML)
	w.Header().Set(headerContentLength, strconv.Itoa(len(body)))
	w.WriteHeader(status)
	if _, err := w.Write(body); err != nil {
		s.report(fmt.Errorf("write the browser door's %d action fragment: %w", status, err))
	}
}

// routableID reports whether the path value is the shape every identifier this
// daemon mints has: 32 lowercase hex characters (session.NewID).
//
// The length comes from internal/session rather than from a literal here, so an
// identifier that grew would not leave this check silently measuring the old one.
// The alphabet is spelled out because it is what hex.EncodeToString produces and
// what internal/session's own adoption check requires — an uppercase or non-hex
// character is not a session this daemon created, whatever else it might be.
func routableID(id string) bool {
	if len(id) != session.IDLen {
		return false
	}
	// Ranging over the string rather than its bytes refuses a multi-byte rune on
	// the same branch as an out-of-class byte.
	for _, c := range id {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}
