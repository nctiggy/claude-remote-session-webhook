package httpapi

// dashboard.go is GET / — the fleet, and the first thing this daemon renders for
// a person rather than for a client.
//
// Everything on the page is derived per request and nothing is stored. The
// summary counts come from the cards below them, the display state comes from
// the reaper's own deadline, and the identity in the header comes from the
// assertion layer 1 validated — three values that would each be a second copy
// free to disagree with the thing it describes (data-model.md, "Derived, not
// stored").
//
// The read is owner-scoped and does not advance the idle clock. Both properties
// belong to internal/session rather than to this file: Manager.List takes the
// owner and cannot be called without one (FR-017, FR-037), and there is no Touch
// on that path (FR-034f). A dashboard that reached the store directly would be a
// fourth path to a record, free to forget either.

import (
	"errors"
	"fmt"
	"maps"
	"net/http"
	"slices"
	"strconv"
	"time"

	"github.com/nctiggy/claude-remote-session-webhook/internal/access"
	"github.com/nctiggy/claude-remote-session-webhook/internal/session"
)

// patternFleet is the fleet page's route, and the `{$}` in it is load-bearing.
//
// `GET /` is a subtree pattern in net/http's router: it would match every
// unrouted GET path as well and answer each one with the fleet page — a session
// list rendered under an address that does not exist. T016 moved those paths
// onto this same door (FR-013d), which is what makes the `{$}` load-bearing
// rather than tidy: the fleet and the not-found page are now neighbours, and
// this is the only thing that decides which of the two a path reaches.
const patternFleet = "GET /{$}"

// patternSessionView is the single-session page — the address every card on the
// fleet already links to (web/templates/partials/session-card.html).
//
// The wildcard is spelled through pathValueID, so the name in the pattern and
// the name read back out of the request cannot drift apart. It is the same
// wildcard the API's session-scoped routes carry and deliberately not a second
// spelling of it; what differs is everything behind it. This route is the
// browser door's, so what authorises it is the identity layer 1 verified plus
// the ownership check Manager.View makes — never the per-session credential,
// which a browser cannot hold and must not be given (FR-034a).
const patternSessionView = "GET /sessions/{" + pathValueID + "}/view"

// errDashboardNoOperator is the fail-closed reason for a page reached with no
// verified identity behind it.
//
// It is unreachable through newServer, which registers every dashboard route
// behind authenticateBrowser, and it is here for the reason errScopeNoCaller is
// on the other door: a wiring mistake that removed layer 1 deserves a reason of
// its own in the trail rather than an empty page that looks like an operator who
// owns nothing.
var errDashboardNoOperator = errors.New("a dashboard route was reached with no verified operator")

// The empty state's copy, supplied at the call site the way docs/components.md
// documents its parameters.
//
// The body used to end "this dashboard only watches — sessions are started
// through the API", which was true for exactly as long as the browser door
// served GET alone. T009 gave it a create and T010 gave that create a form, so
// the sentence became the one thing an empty state must never be: a page telling
// an operator it cannot do something it is offering to do two elements further
// down. It points at the form rather than describing the mechanism, because
// what an operator needs here is where to go next.
//
// The component's Action parameter stays absent all the same (FR-024a), and now
// for a design-system reason rather than a milestone one: the empty state is the
// one surface where the rain runs at full strength, and docs/design-system.md
// keeps rain off reading content — "not a pane, a card grid, a form, or a
// table". The form is a sibling of this section, not a parameter of it.
const (
	emptyFleetTitle = "No sessions running"
	emptyFleetBody  = "Nothing is executing on this host right now. The form below starts a Claude session in a tmux window on this host."
)

// fleetView is the whole of what the fleet page renders against.
//
// It is a page rather than a component, which is why it is here and not in
// view.go: components are docs/components.md's canonical inventory and their
// parameter lists are that file's subject, while this is the composition one
// page makes of them.
type fleetView struct {
	// Operator is the identity layer 1 verified, passed straight to the header
	// component. It is a pointer to the same value OperatorFrom returned rather
	// than a copied address, so there is no second place for the identity to be
	// wrong (FR-020, FR-036).
	Operator *access.VerifiedOperator

	// Summary is the state summary that renders before any detail, because a
	// dashboard is scanned rather than read (docs/design-system.md, FR-017).
	Summary []stateCount

	// Sessions is one entry per session the viewer owns, oldest first — the
	// order Store.List already imposes, kept rather than re-sorted so the page
	// and GET /sessions describe the fleet in the same order.
	Sessions []sessionView

	// Empty is what renders instead of the grid when the viewer owns nothing
	// (FR-021). It is built unconditionally because it costs two constants; the
	// page chooses between it and the grid.
	Empty emptyView

	// Create is the create form, which renders whichever of those two the page
	// chose (T010). An operator who owns nothing is exactly the operator most
	// in need of it, so it is a sibling of both rather than a companion of the
	// grid — and it is the fleet's control rather than a card's, because a
	// create names no session.
	Create createFormView
}

// sessionPageView is the whole of what the single-session page renders against:
// the identity in the header, the canonical card, and the pane beneath it.
//
// It is a page rather than a component, which is why it is here beside fleetView
// and not in view.go. The card it holds is the fleet's own projection, not a
// second one — see cardOf.
type sessionPageView struct {
	// Operator is the identity layer 1 verified, passed straight to the header
	// component as the same pointer OperatorFrom returned (FR-020, FR-036).
	Operator *access.VerifiedOperator

	// Session is the one card this page is about. contracts/dashboard.md puts
	// the canonical card above the pane here for the reason docs/components.md
	// gives it: the card is the only place a session's summary is composed, so
	// the fleet and this page cannot describe the same session differently.
	Session sessionView

	// Pane is the screen, server-rendered so the page is useful before anything
	// live attaches to it (contracts/dashboard.md).
	Pane paneView
}

// stateCount is one entry in the summary row: a display state and how many of
// the cards below carry it.
type stateCount struct {
	State session.DisplayState
	Count int
}

// summarised is the order the summary row is rendered in, and it is fixed rather
// than taken from the counts so that a state does not appear and vanish between
// two page loads — a row that reshuffles is a row an operator has to read
// instead of scan.
//
// It holds the two states the daemon can derive (FR-019b). needs-auth and dead
// are deliberately absent: the design system keeps tokens for them and the pill
// renders them without an edit, but nothing in this daemon can produce either,
// and a tile reading "dead 0" is a claim about a state no record can hold.
var summarised = []session.DisplayState{session.DisplayRunning, session.DisplayIdle}

// dashboard serves GET / (FR-017, FR-018, contracts/dashboard.md).
func (s *Server) dashboard(w http.ResponseWriter, r *http.Request) {
	operator, ok := OperatorFrom(r.Context())
	if !ok {
		AuditFrom(r.Context()).Deny(errDashboardNoOperator.Error())
		s.refuseBrowser(w)
		return
	}

	token, minted := s.pageTokenFor(w, r, operator)
	if !minted {
		return
	}
	// No SetSessionID: this page is about the fleet and not about one session,
	// and data-model.md carries session_id on the single-session view alone.
	s.renderPage(w, r, http.StatusOK, "dashboard", s.fleet(operator, token))
}

// pageTokenFor mints the one page token a render's action forms carry, bound to
// the identity layer 1 verified for that request (FR-002b, FR-007).
//
// One mint per render rather than one per form. A page is rendered for one
// identity at one instant, so every form on it carries the same value and a
// second mint would only be a second expiry nothing is truer for. The identity is
// the operator layer 1 produced and never a value read out of the request, which
// is the whole of FR-007 at the minting end: the gate in browser.go recomputes
// the MAC against the identity of whoever submits, so a token minted for one
// operator's page cannot be spent by another.
//
// The instant is the server's own clock, the same one admitAction measures the
// expiry against. There is no arrangement where a page mints a token that is
// already expired by the reading of time that will check it.
//
// A failure serves no page. mint refuses an empty identity and a MAC it could not
// compute, neither of which is reachable behind this door — layer 1 refuses an
// assertion that names no person — but the honest answer to the unreachable one is
// still not a page: every action offered by a page whose token was not minted is
// refused by the gate, with nothing on the page to say why. It answers the way
// renderPage answers a template that failed, and for the same reason: 500 with no
// body, the reason on the record, the detail on the report channel where an
// operator is already reading. What went wrong is this daemon's, not the caller's.
func (s *Server) pageTokenFor(w http.ResponseWriter, r *http.Request, operator *access.VerifiedOperator) (string, bool) {
	token, err := s.pageKey.mint(operator.Email, s.clock.Now())
	if err != nil {
		// mint's reasons are pagetoken.go's own sentinels, authored there and
		// carrying no byte a caller wrote (FR-035, FR-042).
		AuditFrom(r.Context()).Deny(err.Error())
		// The identity is deliberately absent from the report as well as from the
		// record. It is the edge's word rather than the caller's, but nothing here
		// needs it to be diagnosed, and a page token's own failure is the last
		// place to start writing addresses down.
		s.report(fmt.Errorf("mint the page token for a dashboard render: %w", err))
		w.WriteHeader(http.StatusInternalServerError)
		return "", false
	}
	return token, true
}

// fleet reads the viewer's own sessions and projects them into the page.
//
// One clock reading serves the whole page, so every card's age and every card's
// display state are as of one instant. Two readings would let a fleet render
// with a session counted as running in the summary and drawn as idle in the grid
// — a disagreement between a page and itself, over the one fact that says a
// session is about to be reaped.
//
// The token arrives as a parameter for the reason the clock reading is taken once
// above: it belongs to the render rather than to a card, so every card on the page
// carries the same value and the page cannot hand two of its own forms two
// different tokens.
func (s *Server) fleet(operator *access.VerifiedOperator, token string) fleetView {
	now := s.clock.Now()

	owned := s.sessions.List(operator.Owner)
	views := make([]sessionView, 0, len(owned))
	for _, live := range owned {
		views = append(views, cardOf(live, now, token))
	}

	return fleetView{
		Operator: operator,
		Summary:  summarise(views),
		Sessions: views,
		// Composed on every render and hidden when there is a grid instead. The
		// page carries both of its shapes so that the live half can switch
		// between them when a session appears or vanishes, rather than
		// composing an empty state of its own — a second one would be free to
		// disagree with this (issue #51).
		Empty: emptyView{Title: emptyFleetTitle, Body: emptyFleetBody, Hidden: len(views) > 0},
		// The render's own token, the same value every card above carries. The
		// form is one more thing on this page that acts for this identity at this
		// instant, so it is handed the page's token rather than a second mint —
		// the reason cardOf takes one as a parameter instead of issuing one.
		Create: createFormView{PageToken: token, StartCommands: s.cfg.StartCommands.Names()},
	}
}

// cardOf projects one record into the parameters the card renders from.
//
// One function because there is one card (docs/components.md, FR-024): the fleet
// draws a grid of them and the single-session page draws one, from this. A
// second projection would be a second card — the defect that document exists to
// prevent, spelled in Go instead of in markup, and free to disagree about the
// two fields it derives.
//
// The token is the render's, passed through rather than minted here, so the one
// function that projects a card cannot become a second place a token is issued.
func cardOf(live session.Session, now time.Time, token string) sessionView {
	return sessionView{
		ID:      live.ID,
		Name:    live.Name,
		WorkDir: live.WorkDir,
		// The record's own method, which is the reaper's own deadline
		// (FR-019a–c). Nothing here reads live.State: both values it holds in
		// production display as running, and reading it is what FR-019a forbids.
		DisplayState: live.DisplayState(now),
		StartCommand: live.StartCommand,
		Age:          formatAge(now.Sub(live.CreatedAt)),
		// The token is also what makes the card render its action row (view.go),
		// so every card either offers a control it can authorise or offers none.
		PageToken: token,
	}
}

// sessionPage serves GET /sessions/{id}/view (contracts/dashboard.md): one
// session's card above its current screen, and the page US2's stream will
// attach to.
//
// The read is Manager.View and not Manager.Resolve, which is the whole reason
// that method exists. It resolves ownership without advancing the idle clock
// (FR-034f): a browser tab left open on a session nobody is driving must not
// postpone its idle deadline, or a forgotten tab holds an unsandboxed shell
// alive for as long as it lives. Watching is not driving.
//
// It presents no per-session credential, and that is a decision rather than an
// omission (FR-034a). The token exists to tell apart callers who all
// authenticate as one shared secret; a verified person who owns the session is
// already told apart, and the only places a browser could keep such a token are
// the URL and a script the page can read — neither of which may hold the key to
// an unsandboxed shell.
//
// Every failure is the uniform not-found page. An id that never existed, one
// this viewer does not own, and one whose session is already gone are one answer
// (FR-037b, SC-016) — the difference between them is what enumeration is made
// of, and it is kept in the trail where the operator can read it.
func (s *Server) sessionPage(w http.ResponseWriter, r *http.Request) {
	operator, ok := OperatorFrom(r.Context())
	if !ok {
		AuditFrom(r.Context()).Deny(errDashboardNoOperator.Error())
		s.refuseBrowser(w)
		return
	}

	live, err := s.sessions.View(r.PathValue(pathValueID), operator.Owner)
	if err != nil {
		// resolveReason rather than a reason of this route's own. The trail
		// already has a vocabulary for "not yours or unknown" and "already gone",
		// and an operator grepping their journal should not need a second one
		// because the request arrived through the other door. Like every reason
		// in this repo it is a constant authored here, never the wrapped error,
		// which would carry the caller's own spelling of the id (FR-042).
		AuditFrom(r.Context()).Deny(resolveReason(err).Error())
		s.renderNotFound(w, r, operator)
		return
	}
	// The id off the daemon's own record, never the bytes in the path — the rule
	// SetSessionID exists to keep, and safe here for the reason it is safe in the
	// resolver: the record has just been matched to them. data-model.md carries
	// session_id on this page's record and on no other dashboard route's.
	AuditFrom(r.Context()).SetSessionID(live.ID)

	// The capture is the second read this page makes, and the only one that asks
	// the host rather than the store. A session that died on its own is
	// discovered here or not at all, and when it is, the page owes the same
	// uniform 404 the read above would have given it — the record it resolved
	// against has just been dropped, so serving the card would be a page
	// describing a session this daemon no longer has (#21).
	pane, err := s.screen(r, live)
	if err != nil {
		AuditFrom(r.Context()).Deny(resolveReason(err).Error())
		s.renderNotFound(w, r, operator)
		return
	}

	// Minted after both reads, so a request that ends in the uniform 404 mints
	// nothing: a token is a thing a page hands out, and this one is not serving a
	// page.
	token, minted := s.pageTokenFor(w, r, operator)
	if !minted {
		return
	}

	s.renderPage(w, r, http.StatusOK, "session", sessionPageView{
		Operator: operator,
		Session:  cardOf(live, s.clock.Now(), token),
		Pane:     pane,
	})
}

// screen captures the session's current pane for the initial render, and
// reports the one failure that is not about the pane at all.
//
// This is the one thing on the page that can fail for a reason that has nothing
// to do with the request: the capture is a tmux exec, and tmux may have stopped
// answering. That is not a refusal — the record is the viewer's own and the card
// above it is true — so the page is still served and the pane says the screen
// could not be read. An empty pane instead would be a claim that the session has
// printed nothing, which is the plausible-looking placeholder FR-018a forbids,
// made about output rather than about a name.
//
// The error is the other case, and it is the reason this returns one: the
// capture is also where the daemon learns that the window is not there — asked
// and answered, not merely unreachable — and that is a fact about the session
// rather than about the read. Manager.Output has dropped the record by the time
// it reaches here, so the caller renders the uniform 404 rather than a pane note
// about a screen that will never come back (#21). Only that answer travels; a
// host that could not be asked stays the pane's business.
//
// Nothing about the unreadable case reaches the caller or the trail. tmux's
// account of it is a fact about the host, so it goes to the report channel where
// an operator is already reading — the same place a page that could not be
// rendered goes.
func (s *Server) screen(r *http.Request, live session.Session) (paneView, error) {
	capture, err := s.sessions.Output(r.Context(), live)
	switch {
	case errors.Is(err, session.ErrSessionDead):
		// Unwrapped, so what the caller records is this package's own reason for
		// a session that is already gone rather than internal/session's wording
		// of it — the rule resolveReason exists to keep (FR-042).
		return paneView{}, session.ErrSessionDead
	case err != nil:
		s.report(fmt.Errorf("capture the screen for the view of session %s: %w", live.ID, err))
		return paneView{ID: live.ID, Unread: true}, nil
	}
	return paneView{ID: live.ID, Text: capture.Text}, nil
}

// summarise counts the display states of the views it is given.
//
// It counts the cards rather than tracking a counter, which is data-model.md's
// rule and the reason the summary cannot disagree with the grid: there is one
// pass over one slice, so a card that renders is a card that was counted.
//
// A state outside summarised is appended rather than dropped. Nothing produces
// one today, and that is exactly why: the day something does, the summary must
// under-report nothing — a row that silently omitted a card's state would be a
// summary that says the fleet is smaller than it is.
func summarise(views []sessionView) []stateCount {
	counted := make(map[session.DisplayState]int, len(summarised))
	for _, v := range views {
		counted[v.DisplayState]++
	}

	out := make([]stateCount, 0, len(counted)+len(summarised))
	for _, state := range summarised {
		out = append(out, stateCount{State: state, Count: counted[state]})
		delete(counted, state)
	}
	for _, state := range slices.Sorted(maps.Keys(counted)) {
		out = append(out, stateCount{State: state, Count: counted[state]})
	}
	return out
}

// formatAge is the coarse, human-readable age the card renders (data-model.md).
//
// It is computed server-side and formatted here rather than in the template,
// because a duration in a template is either a raw Go string or a function map
// this set deliberately does not have — and because there is no ticking clock in
// the browser for a formatted string to drift from.
//
// Coarse is the requirement, not a shortcut: an operator scanning a fleet is
// asking "is this old?", and a session whose absolute lifetime is 24 hours has
// nothing to say that a second's precision would add.
//
// A negative duration reads as the youngest age rather than as "-3 minutes". It
// is reachable: an adopted session's CreatedAt is tmux's own reading of the host
// clock, so a clock that stepped backwards between the two produces one.
func formatAge(d time.Duration) string {
	const day = 24 * time.Hour

	switch {
	case d < time.Minute:
		return "less than a minute"
	case d < time.Hour:
		return countOf(int(d/time.Minute), "minute")
	case d < day:
		return countOf(int(d/time.Hour), "hour")
	default:
		return countOf(int(d/day), "day")
	}
}

// countOf is the one place this package pluralises, so a card and a future page
// cannot spell the same duration two ways.
func countOf(n int, unit string) string {
	if n == 1 {
		return "1 " + unit
	}
	return strconv.Itoa(n) + " " + unit + "s"
}
