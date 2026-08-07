package httpapi

// outcome.go is what an action answers with, now that an action answers with a
// redirect (T014, #42, contracts/actions.md).
//
// Milestone 3 shipped the four action routes writing HTML fragments — a bare
// `<p>` or a bare `<article>`, meant to replace the card the operator clicked.
// Nothing replaced anything without a script: the forms are plain posts, so a
// browser with scripting off *navigated* to each fragment and the operator
// landed on a page with no <html> around it. The routes were correct and
// unusable, which is the whole of US3.
//
// The fix is POST-redirect-GET, which is the pattern a form post without script
// has: the handler answers 303 with a Location, the browser re-issues it as a
// GET, and the operator is back on a fleet that already shows what they did. The
// 303 is load-bearing rather than a spelling of 302 — it is the status that
// converts the POST into a GET on the redirect, so the reload an operator
// reaches for afterwards re-fetches the fleet instead of re-submitting the
// action. A second create is a second unsandboxed shell (research R7), which is
// why that matters here more than it does on an ordinary site.
//
// What survives the redirect is a code from the closed vocabulary below, and
// nothing else (FR-022). The code is chosen by the handler, never read from the
// request, so the only values that can reach a URL are the ones spelled in this
// file — and the fleet renders a banner only for a code it recognises. That is
// what keeps a query parameter from becoming a way to put text on this
// operator's own page: an unrecognised outcome renders nothing at all, so there
// is no path from `?outcome=<anything>` to the document.
//
// What deliberately does *not* redirect is a refusal (FR-025). The gate's
// uniform refusal and the uniform not-found are written where they were, with
// the status they had: sending an unauthorised caller somewhere is telling them
// their request was processed, and the two answers a caller probes this door
// with must stay the two answers milestone 3 made uniform.

import (
	"net/http"
	"net/url"
)

// outcome is one code from the closed vocabulary. It is a named type rather than
// a string so that a call site cannot pass a formatted error, a caller's field,
// or a sentence: the only values in existence are the constants below, and
// bannerFor is the only thing that turns one into words.
type outcome string

// The vocabulary. Every action route answers with exactly one of these.
//
// They are machine tokens rather than copy — an operator sees the sentence
// bannerFor gives, not the code — which is why `compacted` is spelled the way
// the audit action `dashboard.compact` is and does not violate FR-016a. What
// FR-016a governs is what this daemon *claims*, and the claim is the banner:
// outcomeCompacted's sentence says delivered, never compacted, for the reason
// bodyActionCompactDelivered said it before this change.
//
// The four failures are per-action rather than one shared `failed`, which is a
// deliberate departure from the vocabulary #42 suggests. Milestone 3 told an
// operator which thing could not be done — started, destroyed, renamed,
// delivered — and collapsing the four into one code would answer a failed
// compact with a sentence about "the action". A redirect is a change of
// mechanism and must not be a loss of what the operator is told (FR-031).
const (
	outcomeCreated   outcome = "created"
	outcomeDestroyed outcome = "destroyed"
	outcomeRenamed   outcome = "renamed"
	outcomeCompacted outcome = "compacted"

	// outcomeModeChanged is the toggle's success, and the fifth thing an action
	// on this door can answer (T020). It is its own code rather than a reuse of
	// outcomeRenamed's shape for the reason every code here is per-action: what
	// this one has to say is that a process was restarted, which none of the other
	// four sentences would tell an operator.
	outcomeModeChanged outcome = "mode-changed"

	outcomeBadName         outcome = "bad-name"
	outcomeBadWorkDir      outcome = "bad-work-dir"
	outcomeBadStartCommand outcome = "bad-start-command"
	outcomeBadMode         outcome = "bad-mode"

	outcomeLimited     outcome = "limited"
	outcomeUnconfirmed outcome = "unconfirmed"

	// outcomeModeUnconfirmed is the toggle's own unconfirmed, and not
	// outcomeUnconfirmed (T019). That code's sentence says nothing was torn
	// down, which is true of a destroy and meaningless about a mode change —
	// the per-action failures below are separate for the same reason.
	outcomeModeUnconfirmed outcome = "mode-unconfirmed"

	outcomeTeardownUnverified outcome = "teardown-unverified"

	outcomeCreateFailed  outcome = "create-failed"
	outcomeDestroyFailed outcome = "destroy-failed"
	outcomeRenameFailed  outcome = "rename-failed"
	outcomeCompactFailed outcome = "compact-failed"
	outcomeModeFailed    outcome = "mode-failed"
)

// queryOutcome is the query parameter the fleet reads its banner from, spelled
// once so the name a redirect writes and the name the page reads cannot drift.
const queryOutcome = "outcome"

// pathFleet is where every action returns the operator to. It is the fleet and
// not the page they came from, because the fleet is the one page that already
// reflects what the action did — a destroyed session is a card that is no longer
// there, and a created one is a card that is.
const pathFleet = "/"

// headerLocation is written by hand on the one kind of response that has one.
const headerLocation = "Location"

// alarmTeardownHeading is the heading the one prominent outcome carries.
//
// It is a heading rather than a longer sentence because prominence here has to
// survive an operator scanning the page: FR-023 requires that this outcome not
// be reduced to a one-line banner alongside "renamed", and the difference an
// operator actually sees is a titled block against a line of prose. Colour is
// not the difference — the design system's fifth non-negotiable rules that state
// is never encoded by colour alone, and a shade would be exactly that.
const alarmTeardownHeading = "Teardown could not be verified"

// banners is the code-to-copy map, and it is the whole of what a query parameter
// can put on the page.
//
// The sentences are milestone 3's own, moved here from the fragments they used
// to be written into rather than reworded: what an action says to an operator is
// not what US3 changed, and a rewrite in passing would be the churn Principle IV
// forbids. Two are edited, and only where the redirect made the old wording
// false — the rename refusal no longer has to say the session still holds its
// old name now that the card is not replaced, and the create's success has a
// sentence at all now that the answer is a page rather than the new card itself.
//
// It is unexported and never ranged over for rendering. A page renders the one
// entry its query named or none, so a code that is not here is not a missing
// banner, it is no banner.
var banners = map[outcome]outcomeView{
	outcomeCreated: {
		Message: "Session started. Its card is on the fleet below.",
	},
	outcomeDestroyed: {
		// Claims only what the host confirmed: Manager.Destroy returns nil after
		// confirmGone said so, never on the strength of the kill alone (FR-019).
		Message: "Session destroyed. The host confirmed its window is gone.",
	},
	outcomeRenamed: {
		Message: "Session renamed.",
	},
	outcomeCompacted: {
		// Delivered, never compacted (FR-016a). Nothing in this process can see
		// what the assistant is carrying, so a sentence claiming the compaction
		// happened would assert a fact no part of this daemon looked at.
		Message: "Compact delivered. The session decides what to do with it.",
	},
	outcomeModeChanged: {
		// Claims the restart and the resumption, which are the two things this
		// daemon did, and stops there. It does not say the session *is* now being
		// driven from anywhere — whether the assistant on the other end picks the
		// conversation up is not a fact this process observed, and outcomeCompacted's
		// sentence is careful in the same place and for the same reason (FR-016a).
		//
		// No apostrophe, like the other four: the template escapes one, so a
		// sentence carrying it reads correctly on the page and matches nothing a
		// test quotes from here.
		Message: "Mode changed. The process in the pane was restarted where it left off, and the session, its window and its scrollback are as they were.",
	},

	outcomeBadName: {
		// One sentence for the create and the rename alike, which the redirect
		// earns: milestone 3 needed a second one on the rename because its answer
		// *replaced the card*, leaving an operator looking at a slot where their
		// session used to be. Nothing is replaced now, so the card is still there
		// saying what it is still called.
		Message: "That is not a usable session name. Use letters, digits and hyphens, up to 64 characters. Nothing was changed.",
	},
	outcomeBadWorkDir: {
		// The single answer every working-directory refusal shares — traversal, an
		// absolute path outside the approved roots, a symlink that resolves out of
		// them, a path that is not a directory, and a path that is not there at all
		// (FR-012). ResolveWorkDir tells those apart and the trail keeps the
		// difference; a caller who could read it would hold a filesystem oracle.
		Message: "This daemon may not start a session in that working directory.",
	},
	outcomeBadStartCommand: {
		// Names the field rather than the value: the operator picked from a list
		// this page rendered, so a mismatch means the list and the daemon disagree,
		// which is worth saying plainly rather than blaming the choice. It arrived
		// on main after #42's branch was abandoned (#38, #39) and joins the
		// vocabulary rather than falling back to the generic failure — a create
		// refused for a nameable reason must keep saying which.
		Message: "That start command is not one this daemon is configured with.",
	},
	outcomeBadMode: {
		// Says the mode is not on offer and never what was asked for. The values
		// this refusal exists to turn away are the ones that name something to
		// run (FR-030), and a sentence that quoted one would print it on the
		// operator's own page — which is caller-authored text reaching the
		// document, the thing the closed vocabulary above exists to prevent.
		Message: "That is not a mode this daemon offers. Nothing was changed.",
	},
	outcomeLimited: {
		Message: "No session was started: this host is at its limit for now. Destroy one, or try again shortly.",
	},
	outcomeUnconfirmed: {
		Message: "This destroy was not confirmed, so nothing was torn down.",
	},
	outcomeModeUnconfirmed: {
		// The destroy's sentence with the fact it states replaced rather than
		// its shape: a mode change tears nothing down, and telling an operator
		// that nothing was torn down would be true and about something else.
		Message: "This mode change was not confirmed, so nothing was changed.",
	},

	outcomeTeardownUnverified: {
		// The one outcome that is not a line of prose, and the reason this view has
		// an Alarm field at all (FR-023). It means a live unsandboxed shell may have
		// survived a destroy the operator believes they completed, which is the one
		// thing Principle VI does not let this daemon say quietly — and the one
		// thing on this page that is worth interrupting a scan for.
		//
		// Being specific discloses nothing: the ownership check matched this
		// operator to this session before the teardown ran, so the fact it carries
		// is a fact about their own session.
		Heading: alarmTeardownHeading,
		Message: "The kill was issued and the host still reports the session's window. It may still be running, unsandboxed, on this host. The record has been kept so the reaper can try again; check the host directly before assuming the work is over.",
		Alarm:   true,
	},

	outcomeCreateFailed: {
		Message: "The session could not be started.",
	},
	outcomeDestroyFailed: {
		// Deliberately does not say the session may still be running — that is a
		// specific claim, and a failure nobody classified is not evidence for it.
		Message: "The session could not be destroyed.",
	},
	outcomeRenameFailed: {
		Message: "The session could not be renamed.",
	},
	outcomeCompactFailed: {
		// Never carries what was being delivered. The bytes are the daemon's own
		// and hold no secret, but a failure that quoted its payload is the shape of
		// the leak AR-007 closes.
		Message: "The compact could not be delivered.",
	},
	outcomeModeFailed: {
		// Says the session is as it was, because that is the fact an operator
		// needs from a failed toggle: the process they are watching is the one
		// they were already watching, and the mode on its card is still true.
		Message: "The session's mode could not be changed. It is running what it was running.",
	},
}

// bannerFor turns whatever arrived in the query string into the banner the fleet
// renders, or into nothing.
//
// The parameter is a plain string because that is what a URL carries, and the
// conversion to outcome happens here and only here: the map lookup is the
// membership test, so a value that is not one of the constants above cannot
// produce a view. That is the injection half of FR-022, and it is closed by
// construction rather than by escaping — html/template would escape a reflected
// value safely, but a page that reflected one at all would be a page whose text
// a link can choose.
//
// A pointer, so an absent or unrecognised outcome is a nil the template's
// `{{ with }}` renders as nothing. A zero-valued struct would render an empty
// banner, which is the affordance-shaped nothing FR-018a rules against.
func bannerFor(raw string) *outcomeView {
	view, ok := banners[outcome(raw)]
	if !ok {
		return nil
	}
	return &view
}

// redirectOutcome is how every action route answers (T014).
//
// One function, so that "an action answers with a 303 to the fleet" is a
// property of this file rather than of fourteen call sites agreeing. It writes
// no body: http.Redirect writes one for a GET alone, and a body nobody renders
// is a body that only ever appears when something else has already gone wrong.
//
// The URL is assembled through net/url rather than concatenated, so the query is
// encoded by the same code that will parse it. Nothing in the vocabulary above
// needs escaping today — that is what TestOutcomeCodesAreURLSafe pins — and the
// day one does, the encoding is already here rather than being remembered.
//
// The security headers are already on the response: authenticateBrowser writes
// them before layer 1 decides anything, so a redirect leaves with the identical
// set to a served page (FR-026), no-store among them. An action's answer is not
// a thing to keep.
func (s *Server) redirectOutcome(w http.ResponseWriter, r *http.Request, code outcome) {
	to := url.URL{Path: pathFleet, RawQuery: url.Values{queryOutcome: []string{string(code)}}.Encode()}
	http.Redirect(w, r, to.String(), http.StatusSeeOther)
}
