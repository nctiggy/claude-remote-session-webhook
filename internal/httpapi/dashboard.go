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
// The body is deliberately not that document's, which reads "Nothing is
// executing on this host right now. Start one to open a Claude session in a tmux
// window." There is no "start one" here: the dashboard is read-only (FR-022),
// the component's action parameter is absent (FR-024a), and a browser holds no
// shared secret to sign a create with even if it rendered a button. Copy that
// tells an operator to do something the page cannot do is a worse empty state
// than no copy at all.
const (
	emptyFleetTitle = "No sessions running"
	emptyFleetBody  = "Nothing is executing on this host right now. This dashboard only watches — sessions are started through the API."
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

	// No SetSessionID: this page is about the fleet and not about one session,
	// and data-model.md carries session_id on the single-session view alone.
	s.renderPage(w, r, http.StatusOK, "dashboard", s.fleet(operator))
}

// fleet reads the viewer's own sessions and projects them into the page.
//
// One clock reading serves the whole page, so every card's age and every card's
// display state are as of one instant. Two readings would let a fleet render
// with a session counted as running in the summary and drawn as idle in the grid
// — a disagreement between a page and itself, over the one fact that says a
// session is about to be reaped.
func (s *Server) fleet(operator *access.VerifiedOperator) fleetView {
	now := s.clock.Now()

	owned := s.sessions.List(operator.Owner)
	views := make([]sessionView, 0, len(owned))
	for _, live := range owned {
		views = append(views, sessionView{
			ID:      live.ID,
			Name:    live.Name,
			WorkDir: live.WorkDir,
			// The record's own method, which is the reaper's own deadline
			// (FR-019a–c). Nothing here reads live.State: both values it holds in
			// production display as running, and reading it is what FR-019a
			// forbids.
			DisplayState: live.DisplayState(now),
			Age:          formatAge(now.Sub(live.CreatedAt)),
			// Actions is left absent (FR-024a). See view.go: the parameter exists
			// so milestone 3 fills a row that is already there.
		})
	}

	return fleetView{
		Operator: operator,
		Summary:  summarise(views),
		Sessions: views,
		Empty:    emptyView{Title: emptyFleetTitle, Body: emptyFleetBody},
	}
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
