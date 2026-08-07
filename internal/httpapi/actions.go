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

// The four things a destroy can answer are four outcome codes rather than four
// fragments (T014, outcome.go).
//
// Milestone 3 wrote each of them as markup here, because contracts/actions.md
// said the response would replace the card it acted on. Nothing did the
// replacing without a script — the form is a plain post — so an operator with
// scripting off navigated to a bare `<p>` with no page around it. The sentences
// are unchanged and now live in outcome.go's map, where the fleet renders them
// after the redirect lands, next to the fleet the destroy changed.
//
// The 409 the contract used to fix byte for byte is now outcomeTeardownUnverified,
// which is the one outcome that keeps a shape of its own: a teardown this daemon
// could not verify means a live unsandboxed shell may have survived, and FR-023
// is explicit that it must not be reduced to a line of prose beside "renamed".

// errDestroyUnconfirmed is what a destroy arriving without `confirm=yes` is
// recorded as.
//
// It is a refusal worth a name of its own rather than a shrug. The trail is
// where an operator would see a page submitting destroys without the confirming
// step — which is either a broken template or something that is not this
// daemon's page — and neither is visible from the redirect the caller gets.
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
		s.redirectOutcome(w, r, outcomeUnconfirmed)
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
	// before the teardown so the unverified outcome below names the session it
	// could not confirm. That is what makes an unverified teardown findable in the
	// trail rather than merely present in it.
	AuditFrom(r.Context()).SetSessionID(live.ID)

	if err := s.sessions.Destroy(r.Context(), live); err != nil {
		s.refuseBrowserDestroy(w, r, err)
		return
	}

	s.redirectOutcome(w, r, outcomeDestroyed)
}

// refuseBrowserDestroy maps a Destroy failure onto the answer contracts/actions.md
// gives it, and it is refuseDestroy's shape on the other door for the same
// reason: one case has an answer of its own because an operator has to be able
// to find it. That case is outcomeTeardownUnverified, which is the one outcome
// the fleet renders as more than a line of prose (FR-023).
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
		s.redirectOutcome(w, r, outcomeTeardownUnverified)
		return
	}
	AuditFrom(r.Context()).Deny(errDestroyRefused.Error())
	s.redirectOutcome(w, r, outcomeDestroyFailed)
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

	// fieldStartCommand names which of the operator's configured commands to
	// type into the new session's shell (#38). It carries a name; the command
	// line it maps to never leaves the daemon's configuration.
	fieldStartCommand = "start_command"
)

// What a create answers is an outcome code, exactly as a destroy's is (T014).
// The five refusals are outcomeBadName, outcomeBadWorkDir, outcomeBadStartCommand,
// outcomeLimited and outcomeCreateFailed, and the success is outcomeCreated;
// their sentences are milestone 3's own, moved into outcome.go's map.
//
// The one that changed shape is the success. It used to be the new card itself,
// rendered here and appended to a fleet by a swap only a running script
// performed — so an operator with scripting off who started a session was shown
// one card on a blank page. The fleet they are returned to now draws that card
// from the same projection (cardOf) alongside every other, which is the
// appending the plan described, done by the page that owns the grid.

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
		s.redirectOutcome(w, r, outcomeLimited)
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
		Owner:        operator.Owner,
		Name:         r.PostForm.Get(fieldName),
		WorkDir:      r.PostForm.Get(fieldWorkDir),
		StartCommand: r.PostForm.Get(fieldStartCommand),
	})
	if err != nil {
		s.refuseBrowserCreate(w, r, err)
		return
	}

	// The id off the daemon's own record, never a byte the form carried — the rule
	// SetSessionID exists to keep. It is what makes a create findable in the trail
	// rather than merely present in it.
	AuditFrom(r.Context()).SetSessionID(created.ID)

	// The fleet, which is where the new card is (T014).
	//
	// This used to render the card here and hand it back as a fragment, on the
	// plan's expectation that something would append it to the grid. Only a
	// running script did, so a create that worked put a scriptless operator on a
	// page holding one card and nothing else — no header, no summary, no fleet, no
	// create form. The redirect sends them to the page that draws the grid, and
	// that page renders this session from the same projection (cardOf) as every
	// other, so the first appearance of a card is a card in a fleet rather than a
	// card on its own.
	//
	// Nothing about the token needs carrying across. The page the operator lands
	// on mints its own for its own render, which is one fewer arrangement than the
	// fragment needed — that one had to write the submitted token back into the
	// new card so its siblings and it would expire together.
	s.redirectOutcome(w, r, outcomeCreated)
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
		s.redirectOutcome(w, r, outcomeBadName)
	case errors.Is(err, session.ErrInvalidWorkDir):
		AuditFrom(r.Context()).Deny(createReason(err).Error())
		s.redirectOutcome(w, r, outcomeBadWorkDir)
	case errors.Is(err, session.ErrUnknownStartCommand):
		// An outcome of its own rather than the generic failure: the operator named
		// something this daemon does not have, which is a request to fix rather than
		// a fault to report. The name is refused rather than falling back to the
		// default, because a caller who asked for remote control and silently got a
		// plain session has no way to discover that is what happened.
		AuditFrom(r.Context()).Deny(createReason(err).Error())
		s.redirectOutcome(w, r, outcomeBadStartCommand)
	case errors.Is(err, session.ErrTooManySessions):
		// A full fleet and a spent create budget are one outcome for the reason
		// they were one body: nothing the operator sent is wrong, and the only fix
		// is to wait or to destroy something. Which of the two it was is on the
		// record.
		AuditFrom(r.Context()).Deny(errCreateCapReached.Error())
		s.redirectOutcome(w, r, outcomeLimited)
	case errors.Is(err, session.ErrOrphanedSession):
		AuditFrom(r.Context()).Deny(errCreateOrphaned.Error())
		s.redirectOutcome(w, r, outcomeCreateFailed)
	default:
		AuditFrom(r.Context()).Deny(errCreateRefused.Error())
		s.redirectOutcome(w, r, outcomeCreateFailed)
	}
}

// --- Rename ----------------------------------------------------------------
//
// The third route on this door that changes something, and the only one of the
// four that changes nothing on the host at all: a rename is an edit to a record
// (US4).

// patternDashboardRename is the rename route, from contracts/actions.md's table.
//
// It is spelled the destroy's way and for the destroy's reasons: under
// /dashboard/ so milestone 1's surface is untouched (FR-005) and a grep for the
// prefix finds every browser-initiated change, the wildcard through pathValueID
// so the name in the pattern and the name read back cannot drift apart, and the
// method inside the pattern so a GET here falls to handleUnrouted's `/` and is
// answered as a path nothing claims rather than as a 405 with an Allow header
// (FR-033).
const patternDashboardRename = "POST /dashboard/sessions/{" + pathValueID + "}/rename"

// What a rename answers is an outcome code (T014): outcomeRenamed, outcomeBadName
// for a name ValidateName refused, and outcomeRenameFailed for a failure no
// sentinel explains.
//
// A refused name is the create's own outcome and not a second one, which the
// redirect earns. Milestone 3 needed two, because a rename's answer *replaced
// the card*: an operator told only that the name was bad was left looking at a
// slot where their session used to be, so that sentence had to add that the
// session was still called what it was. Nothing is replaced now — the card is on
// the fleet the operator lands on, saying what it is still called — so the two
// refusals are one sentence again. Neither carries the name that was refused;
// that is caller-supplied text on its way back out through a page (FR-042).

// errRenameRefused is the fail-closed reason for a rename that failed for a
// reason no sentinel explains. A refusal nobody classified is still a refusal,
// and errCreateRefused is not it: what tells one door's records from another's is
// the action, and what tells one action's from another's is this.
var errRenameRefused = errors.New("the session could not be renamed")

// renameFromBrowser is POST /dashboard/sessions/{id}/rename (US4,
// contracts/actions.md).
//
// Everything that authorises it has already run: handleAction wrapped this
// handler in the gate, so layer 1 has verified an identity, the browser has said
// the request came from this page, and the form has carried a token minted for
// that identity. What is left is the ownership check and the record edit.
//
// The rename is Manager.Rename and nothing else, which is where FR-015 lives:
// that method takes no context and has no tmux name to change, so "a rename does
// not touch the host" is a property of the call rather than of this handler
// remembering not to. The validation is ValidateName's, reached by calling the
// same method rather than by restating the alphabet here — a name the create path
// refuses must not be reachable by renaming into it, and the only thing that keeps
// that true through a later widening is there being one check to widen.
//
// The record that is rendered back is the one Rename returned, not a second read
// of the store. It already carries the field this call just wrote, and re-reading
// would be a lookup free to disagree with the write it is describing.
func (s *Server) renameFromBrowser(w http.ResponseWriter, r *http.Request) {
	operator, ok := OperatorFrom(r.Context())
	if !ok {
		// Fail closed on the path that should not happen, the way every other
		// handler on this door does: the gate in front puts the operator in the
		// context, so a false here is a route wired without one.
		AuditFrom(r.Context()).Deny(errDashboardNoOperator.Error())
		s.refuseBrowser(w)
		return
	}

	// The shape check the destroy runs, ahead of the lookup and for its reason: an
	// identifier off the 32-lowercase-hex alphabet cannot name a session this
	// daemon minted, so it is a path nothing claims rather than a session that is
	// not there.
	id := r.PathValue(pathValueID)
	if !routableID(id) {
		AuditFrom(r.Context()).Deny(errScopeNoRoute.Error())
		s.renderNotFound(w, r, operator)
		return
	}

	// Manager.View, which is what a browser gets: it settles ownership without a
	// per-session credential and without advancing the idle clock (FR-034a,
	// FR-034f). Ahead of the name check deliberately — a session this operator may
	// not act on is answered the same whatever they asked to call it, so the two
	// refusals cannot be read against each other to learn which identifiers are
	// real.
	live, err := s.sessions.View(id, operator.Owner)
	if err != nil {
		// resolveReason rather than a reason of this route's own, for the reason
		// the destroy uses it: the trail already has a vocabulary for these, and
		// never the wrapped error, which would carry the caller's spelling of the id
		// (FR-042).
		AuditFrom(r.Context()).Deny(resolveReason(err).Error())
		s.notFoundAction(w)
		return
	}
	// The id off the daemon's own record, never the bytes in the path. It is
	// stamped before the edit, so a rename that failed is findable in the trail
	// under the session it failed against.
	AuditFrom(r.Context()).SetSessionID(live.ID)

	// Read from PostForm and never Form, for the reason the gate reads the token
	// that way: a rename this daemon would accept from a query string is a relabel
	// a link can carry. The form itself was parsed by the gate, under the
	// configured body limit.
	if _, err := s.sessions.Rename(live, r.PostForm.Get(fieldName)); err != nil {
		s.refuseBrowserRename(w, r, err)
		return
	}

	// The fleet, where the card now carries the new name (T014). It used to be the
	// re-rendered card, handed back as a fragment on the expectation that
	// something would put it where the old one was — and only a running script
	// did, so a rename that worked put a scriptless operator on a page holding one
	// card and no page around it. The name is the only thing about the card that
	// moved, and the fleet draws it from the same projection this route used to
	// call.
	s.redirectOutcome(w, r, outcomeRenamed)
}

// refuseBrowserRename maps a Rename failure onto the answer contracts/actions.md
// gives it, and it is refuseBrowserDestroy's and refuseBrowserCreate's shape for
// their reason: one function, so the branches are read together.
//
// Three arms over an error with two named causes. A name the shared check refused
// is the contract's refused name; a record that is no longer there, or is dead, is the
// uniform not-found the other routes give — the session was there when View
// answered and is not there now, which is exactly the "no longer exists" cause
// T005's one answer exists to cover, and telling a caller that a session
// disappeared between two reads of the store is the enumeration FR-017 closes.
//
// createReason is what turns a refused name into the sentinel an operator can
// act on. Only its two name arms are reachable from here — ValidateName is the
// only thing on this path that produces one — and they are the API door's own
// words on purpose: the same fact deserves the same words in the journal whichever
// door reported it, and what tells the records apart is the action the middleware
// already set.
func (s *Server) refuseBrowserRename(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, session.ErrInvalidName):
		AuditFrom(r.Context()).Deny(createReason(err).Error())
		s.redirectOutcome(w, r, outcomeBadName)
	case errors.Is(err, session.ErrSessionNotFound), errors.Is(err, session.ErrSessionDead):
		AuditFrom(r.Context()).Deny(resolveReason(err).Error())
		s.notFoundAction(w)
	default:
		AuditFrom(r.Context()).Deny(errRenameRefused.Error())
		s.redirectOutcome(w, r, outcomeRenameFailed)
	}
}

// --- Compact ---------------------------------------------------------------
//
// The fourth and last route on this door that changes something, and the only
// one of the four that answers with something it did not observe the end of: the
// bytes were handed to the session, and what the session makes of them is the
// session's own business (US5).

// patternDashboardCompact is the compact route, from contracts/actions.md's table.
//
// It is spelled the rename's way and for the rename's reasons: under /dashboard/
// so milestone 1's surface is untouched (FR-005) and a grep for the prefix finds
// every browser-initiated change, the wildcard through pathValueID so the name in
// the pattern and the name read back cannot drift apart, and the method inside
// the pattern so a GET here falls to handleUnrouted's `/` and is answered as a
// path nothing claims rather than as a 405 with an Allow header (FR-033).
const patternDashboardCompact = "POST /dashboard/sessions/{" + pathValueID + "}/compact"

// What a compact answers is an outcome code (T014): outcomeCompacted, and
// outcomeCompactFailed for a delivery that did not land.
//
// The code is a machine token and the sentence is the claim, which is where
// FR-016a lives after this change. contracts/actions.md fixed the delivered
// wording byte for byte because a sentence authored freely is exactly how
// "delivered" becomes "compacted" in a later edit that meant only to read
// better; that wording is now outcomeCompacted's entry in outcome.go's map,
// unchanged, and the code beside it is spelled the way the audit action
// dashboard.compact is. What an operator reads is the sentence.

// errCompactRefused is the fail-closed reason for a delivery that failed for a
// reason no sentinel explains. A refusal nobody classified is still a refusal,
// and errRenameRefused is not it: what tells one door's records from another's is
// the action, and what tells one action's from another's is this.
//
// It names the delivery and never the delivered text, exactly as Manager.Compact's
// own error does (FR-016b, AR-007).
var errCompactRefused = errors.New("the compact could not be delivered")

// compactFromBrowser is POST /dashboard/sessions/{id}/compact (US5,
// contracts/actions.md).
//
// Everything that authorises it has already run: handleAction wrapped this
// handler in the gate, so layer 1 has verified an identity, the browser has said
// the request came from this page, and the form has carried a token minted for
// that identity. What is left is the ownership check and the delivery.
//
// It reads no form field at all, and that is FR-016's shape rather than an
// omission. What is delivered is a constant in the manager, so there is nothing
// here for a caller to choose: a text parameter on this route would be the
// general "type into the session" surface spec.md puts out of scope for this
// milestone, arriving without anyone deciding to add it.
//
// The delivery is Manager.Compact and nothing else — the load-buffer path a
// prompt uses, never send-keys (T019) — and its nil is carried straight through
// to an outcome that says delivered rather than being upgraded on the way. That
// is the honest answer: the request was accepted for delivery, and the thing an
// operator asked for happens after this handler has returned, in a process this
// daemon cannot see into.
func (s *Server) compactFromBrowser(w http.ResponseWriter, r *http.Request) {
	operator, ok := OperatorFrom(r.Context())
	if !ok {
		// Fail closed on the path that should not happen, the way every other
		// handler on this door does: the gate in front puts the operator in the
		// context, so a false here is a route wired without one.
		AuditFrom(r.Context()).Deny(errDashboardNoOperator.Error())
		s.refuseBrowser(w)
		return
	}

	// The shape check the destroy and the rename run, ahead of the lookup and for
	// their reason: an identifier off the 32-lowercase-hex alphabet cannot name a
	// session this daemon minted, so it is a path nothing claims rather than a
	// session that is not there.
	id := r.PathValue(pathValueID)
	if !routableID(id) {
		AuditFrom(r.Context()).Deny(errScopeNoRoute.Error())
		s.renderNotFound(w, r, operator)
		return
	}

	// Manager.View, which is what a browser gets: it settles ownership without a
	// per-session credential, because a browser holds none and must not be given
	// one (FR-034a). It deliberately does not advance the idle clock (FR-034f) —
	// Compact does that itself, under the store's lock, because a compact is
	// activity and a lookup is not.
	live, err := s.sessions.View(id, operator.Owner)
	if err != nil {
		// resolveReason rather than a reason of this route's own, for the reason
		// the destroy and the rename use it: the trail already has a vocabulary for
		// these, and never the wrapped error, which would carry the caller's
		// spelling of the id (FR-042).
		AuditFrom(r.Context()).Deny(resolveReason(err).Error())
		s.notFoundAction(w)
		return
	}
	// The id off the daemon's own record, never the bytes in the path. It is
	// stamped before the delivery, so a compact that failed is findable in the
	// trail under the session it failed against.
	AuditFrom(r.Context()).SetSessionID(live.ID)

	if err := s.sessions.Compact(r.Context(), live); err != nil {
		s.refuseBrowserCompact(w, r, err)
		return
	}

	// A compact changes nothing a card draws — the name, the working directory and
	// the age are all as they were, and the idle clock it deferred is not on the
	// card at all — so the whole of what an operator learns here is the sentence
	// the banner carries. That was true when the answer was a fragment and it is
	// why this route never rendered a card.
	s.redirectOutcome(w, r, outcomeCompacted)
}

// refuseBrowserCompact maps a Compact failure onto the answer this route gives,
// and it is refuseBrowserRename's shape for its reason: one function, so the
// branches are read together.
//
// Three arms over an error with two named causes. A record that is no longer
// there, or is dead, is the uniform not-found the other routes give — the session
// was there when View answered and is not there now, which is exactly the "no
// longer exists" cause T005's one answer exists to cover, and telling a caller
// that a session disappeared between two reads of the store is the enumeration
// FR-017 closes. Compact reaches those two through the store's own Touch, taken
// under its lock, so a session the reaper collected mid-request lands here rather
// than having bytes delivered into whatever its window has become.
//
// Everything else is the failed delivery. A paste that failed is the only cause
// and it carries no sentinel, because there is nothing a caller could do differently:
// the delivery did not land, and whether tmux was busy, gone, or wrong is the
// operator's question to take to the journal.
func (s *Server) refuseBrowserCompact(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, session.ErrSessionNotFound), errors.Is(err, session.ErrSessionDead):
		AuditFrom(r.Context()).Deny(resolveReason(err).Error())
		s.notFoundAction(w)
	default:
		AuditFrom(r.Context()).Deny(errCompactRefused.Error())
		s.redirectOutcome(w, r, outcomeCompactFailed)
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
