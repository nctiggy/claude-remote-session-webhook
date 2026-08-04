// Internal test, matching the rest of the package. Every claim here is about the
// page a browser receives, so each one drives GET / through the real router, the
// real browser door, and a real *access.Validator over a locally generated key
// pair — the same arrangement browser_test.go uses, because a handler called
// directly would prove the markup and not the route.
//
// The US1 acceptance suite (T017) is at the foot of this file, under its own
// heading. It is the story's own claims rather than a handler's: what the page
// fetches, what it says without colour, what it withholds from a second owner,
// and where the identity in its header came from.
package httpapi

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/nctiggy/claude-remote-session-webhook/internal/audit"
	"github.com/nctiggy/claude-remote-session-webhook/internal/auth"
	"github.com/nctiggy/claude-remote-session-webhook/internal/session"
	"github.com/nctiggy/claude-remote-session-webhook/internal/tmuxctl"
)

// fleet is a server whose browser door admits a real assertion, together with the
// key server that mints one.
type fleet struct {
	*testServer
	keys *keyServer
}

func newFleet(t *testing.T) *fleet {
	t.Helper()
	return newFleetWith(t, newKeyServer(t))
}

// newFleetWith is newFleet with the edge chosen by the caller, which the
// fail-closed suite in browser_test.go needs and no page test here does: every
// claim in this file is about what a served page says, and a fleet whose signing
// keys cannot be obtained serves none.
func newFleetWith(t *testing.T, keys *keyServer) *fleet {
	t.Helper()
	return &fleet{testServer: newAuditedServerWith(t, keys.validator(t)), keys: keys}
}

// open asks for one path as the verified operator would and hands back whatever
// the daemon answered, status and all — some of the pages this suite reads are
// refusals, and a helper that insisted on 200 could not fetch them.
//
// The request carries no layer-2 signature and no bearer token, which is FR-012
// from the browser's side: this door refuses only by the check that applies to it,
// and a page that needed a signature would be a page no browser could open.
func (f *fleet) open(t *testing.T, target string) *httptest.ResponseRecorder {
	t.Helper()
	return f.openWith(t, target, f.keys.mint(t, f.keys.claims()))
}

// openWith is open with the credential chosen rather than minted, which every
// page test here has no use for — they all want the identity assertion open
// mints — and which the browser door's own tests need: theirs are claims about
// *which* assertion arrived. An assertion of absent sends no header at all, the
// same distinction door.request draws.
func (f *fleet) openWith(t *testing.T, target, assertion string) *httptest.ResponseRecorder {
	t.Helper()

	r := httptest.NewRequest(http.MethodGet, target, nil)
	if assertion != absent {
		r.Header.Set(headerAccessAssertion, assertion)
	}

	w := httptest.NewRecorder()
	f.ServeHTTP(w, r)
	return w
}

// view opens the fleet page, which every test here expects to be served.
func (f *fleet) view(t *testing.T) *httptest.ResponseRecorder {
	t.Helper()

	w := f.open(t, "/")
	if w.Code != http.StatusOK {
		t.Fatalf("GET / = %d (%s); want %d", w.Code, w.Body.String(), http.StatusOK)
	}
	return w
}

// idle is a record whose idle deadline has already passed at the fixture's
// instant, and running is one comfortably inside it. Both are expressed against
// session.IdleTimeout rather than against a duration written here, so a change to
// the bound moves the fixtures with it.
func idleAt(now time.Time) time.Time    { return now.Add(-session.IdleTimeout - time.Minute) }
func runningAt(now time.Time) time.Time { return now.Add(-time.Minute) }

// cardFor isolates the card naming one session, so an assertion about what a
// *card* says cannot be satisfied by markup elsewhere on the page.
//
// It exists because of a mutation this suite failed to catch: a hard-coded
// display state, with every card rendering "running", passed a page-wide search
// for the text "idle" — the summary row renders the same canonical status pill
// the cards do, which is the point of there being one pill, and it always names
// both states. A page-level search for a card's own text is not an assertion
// about a card.
//
// The opener names the element and not its class, so this works just as well on
// a page the colour has been stripped out of — which is what T017's
// without-colour assertion reads. The card is the only <article> the dashboard
// renders, and TestEveryCardIsLegibleWithoutColour holds that.
func cardFor(t *testing.T, page, id string) string {
	t.Helper()

	const opener = `<article`
	for _, card := range strings.Split(page, opener)[1:] {
		body, _, _ := strings.Cut(card, "</article>")
		if strings.Contains(body, id) {
			return body
		}
	}
	t.Fatalf("the page renders no card for session %s:\n%s", id, page)
	return ""
}

// TestTheFleetShowsEverySessionTheViewerOwnsAndNoOther is FR-017 and FR-018 at
// the route, and FR-020 with them: the cards are the viewer's own, the identity
// in the header came from the assertion, and one page load is the whole
// interaction (SC-003).
//
// The second owner is the half that matters. FR-037a makes every session the
// operator's in production, so a page built on an owner-blind read would look
// perfect — this plants a session nobody who can reach this door owns, and the
// card for it must not be there.
func TestTheFleetShowsEverySessionTheViewerOwnsAndNoOther(t *testing.T) {
	t.Parallel()

	const stranger auth.CallerID = "a-second-operator"

	f := newFleet(t)
	mine, _ := f.fixture.plant(t, session.Session{
		Name: "refactor the reaper", WorkDir: f.fixture.repo, CreatedAt: testTime.Add(-2 * time.Hour),
	})
	also, _ := f.fixture.plant(t, session.Session{Name: "read the trail", WorkDir: f.fixture.repo})
	theirs, _ := f.fixture.plant(t, session.Session{Owner: stranger, Name: "not yours", WorkDir: f.fixture.repo})

	page := f.view(t).Body.String()

	for _, owned := range []session.Session{mine, also} {
		if !strings.Contains(page, owned.ID) {
			t.Errorf("the fleet does not show session %s, which the viewer owns:\n%s", owned.ID, page)
		}
		if !strings.Contains(page, owned.Name) {
			t.Errorf("the fleet does not show the name of session %s:\n%s", owned.ID, page)
		}
	}
	// FR-018's four fields are name, state, working directory, and age. The other
	// three are asserted by their own tests; this is the one the page composes
	// itself, from a record planted two hours before the fixture's instant.
	if !strings.Contains(page, f.fixture.repo) {
		t.Errorf("the fleet does not show a working directory:\n%s", page)
	}
	if !strings.Contains(page, "2 hours") {
		t.Errorf("the fleet does not show the age of a session created two hours ago:\n%s", page)
	}
	if strings.Contains(page, theirs.ID) {
		t.Errorf("the fleet shows session %s, which belongs to %q:\n%s", theirs.ID, stranger, page)
	}
	if strings.Contains(page, theirs.Name) {
		t.Errorf("the fleet shows another owner's session name:\n%s", page)
	}
	if n := strings.Count(page, `<article class="card"`); n != 2 {
		t.Errorf("the fleet rendered %d cards; want one per owned session (2):\n%s", n, page)
	}

	// From the validated assertion, and there is nothing else in scope for it to
	// have come from: the header component executes against the VerifiedOperator.
	if !strings.Contains(page, testOperatorEmail) {
		t.Errorf("the fleet does not name the verified operator:\n%s", page)
	}

	rec := f.only(t)
	if got, want := rec["action"], string(audit.ActionDashboardView); got != want {
		t.Errorf("action = %v; want %v", got, want)
	}
	if got, want := rec["decision"], string(audit.Allow); got != want {
		t.Errorf("decision = %v; want %v", got, want)
	}
	if id, ok := rec["session_id"]; ok {
		t.Errorf("the record names session %v; the fleet is about no particular session", id)
	}
}

// TestTheFleetDerivesEachStateFromTheReapersOwnDeadline is FR-019a–c. The stored
// lifecycle field is deliberately the same on both records here — plant leaves
// them starting — so a page that rendered Session.State would label them
// identically and pass nothing below.
func TestTheFleetDerivesEachStateFromTheReapersOwnDeadline(t *testing.T) {
	t.Parallel()

	f := newFleet(t)
	stale, _ := f.fixture.plant(t, session.Session{
		Name: "left open", WorkDir: f.fixture.repo, LastActivity: idleAt(testTime),
	})
	live, _ := f.fixture.plant(t, session.Session{
		Name: "in use", WorkDir: f.fixture.repo, LastActivity: runningAt(testTime),
	})

	page := f.view(t).Body.String()
	for _, want := range []struct {
		id            string
		state, notThe session.DisplayState
	}{
		{stale.ID, session.DisplayIdle, session.DisplayRunning},
		{live.ID, session.DisplayRunning, session.DisplayIdle},
	} {
		// The label as text, which is FR-019 and the design system's fifth
		// non-negotiable: both states are green, so colour alone separates
		// nothing even for a reader who can see it.
		card := cardFor(t, page, want.id)
		if !strings.Contains(card, ">"+string(want.state)+"<") {
			t.Errorf("the card for session %s does not read %q as text:\n%s", want.id, want.state, card)
		}
		if strings.Contains(card, ">"+string(want.notThe)+"<") {
			t.Errorf("the card for session %s reads %q, and the reaper's own deadline says %q:\n%s", want.id, want.notThe, want.state, card)
		}
	}
	for _, unwanted := range []session.State{session.StateStarting, session.StateDead} {
		if strings.Contains(page, ">"+string(unwanted)+"<") {
			t.Errorf("a card renders the stored lifecycle state %q; the label is derived, never read (FR-019a):\n%s", unwanted, page)
		}
	}
}

// TestTheFleetSummaryCountsTheCardsBelowIt is the summary row: derived from the
// views it precedes, never a tracked counter (data-model.md).
//
// The zero is asserted as well as the counts. A row that only listed the states
// present would change shape between page loads, which is a row an operator has
// to read rather than scan — and the summary comes before any detail precisely so
// it can be scanned.
func TestTheFleetSummaryCountsTheCardsBelowIt(t *testing.T) {
	t.Parallel()

	f := newFleet(t)
	// A mixed fleet, not three of a kind: a summary built from the total rather
	// than from the states below it agrees with a uniform fleet and with nothing
	// else, so counting one state only would be a test that passes on the bug.
	for range 2 {
		f.fixture.plant(t, session.Session{Name: "busy", WorkDir: f.fixture.repo, LastActivity: runningAt(testTime)})
	}
	f.fixture.plant(t, session.Session{Name: "left open", WorkDir: f.fixture.repo, LastActivity: idleAt(testTime)})

	page := f.view(t).Body.String()
	if n := strings.Count(page, `<article class="card"`); n != 3 {
		t.Fatalf("the fleet rendered %d cards; want 3 before its summary is worth asserting:\n%s", n, page)
	}
	for _, want := range []struct {
		state session.DisplayState
		count string
	}{
		{session.DisplayRunning, "2"},
		{session.DisplayIdle, "1"},
	} {
		if !strings.Contains(page, `pill-`+string(want.state)) {
			t.Errorf("the summary carries no %s entry:\n%s", want.state, page)
		}
		if !strings.Contains(page, `<span class="summary-count">`+want.count+"</span>") {
			t.Errorf("the summary does not count %s as %s:\n%s", want.state, want.count, page)
		}
	}

	// A state none of the fleet is in still gets its entry. Without it the row
	// changes shape between two page loads, which is a row an operator has to read
	// rather than scan — and the summary comes before any detail precisely so that
	// it can be scanned.
	uniform := newFleet(t)
	uniform.fixture.plant(t, session.Session{Name: "busy", WorkDir: uniform.fixture.repo, LastActivity: runningAt(testTime)})

	if got := uniform.view(t).Body.String(); !strings.Contains(got, `<span class="summary-count">0</span>`) {
		t.Errorf("a fleet with nothing idle in it dropped the idle entry from its summary:\n%s", got)
	}
}

// TestSummariseCountsWhatItIsGiven holds the derivation itself, including the
// case no page can produce yet: a state outside the two this daemon derives is
// appended rather than dropped.
//
// That row is not speculation. needs-auth arrives with milestone 4's device-code
// relay, and a summary that silently omitted it would tell an operator the fleet
// is smaller than the grid underneath already shows.
func TestSummariseCountsWhatItIsGiven(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		views []sessionView
		want  []stateCount
	}{
		"no sessions at all": {
			want: []stateCount{{session.DisplayRunning, 0}, {session.DisplayIdle, 0}},
		},
		"a mixed fleet": {
			views: []sessionView{
				{DisplayState: session.DisplayIdle},
				{DisplayState: session.DisplayRunning},
				{DisplayState: session.DisplayIdle},
			},
			want: []stateCount{{session.DisplayRunning, 1}, {session.DisplayIdle, 2}},
		},
		"a state this milestone cannot produce": {
			views: []sessionView{
				{DisplayState: session.DisplayRunning},
				{DisplayState: "needs-auth"},
			},
			want: []stateCount{{session.DisplayRunning, 1}, {session.DisplayIdle, 0}, {"needs-auth", 1}},
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got := summarise(tc.views)
			if len(got) != len(tc.want) {
				t.Fatalf("summarise() = %v; want %v", got, tc.want)
			}
			for i, want := range tc.want {
				if got[i] != want {
					t.Errorf("entry %d = %v; want %v — the order is fixed so the row does not reshuffle between loads", i, got[i], want)
				}
			}
		})
	}
}

// TestTheFleetStatesWhatAnAdoptedSessionDoesNotKnow is FR-018a at the page. A
// session adopted after a restart carries neither a name nor a working directory,
// because milestone 1 records neither on purpose — so this is what the dashboard
// looks like after every restart, not an edge case.
//
// The assertion is that the absence is *stated*: an invented placeholder would
// be telling an operator something false about an unsandboxed shell, and a blank
// slot would be telling them nothing about one.
func TestTheFleetStatesWhatAnAdoptedSessionDoesNotKnow(t *testing.T) {
	t.Parallel()

	f := newFleet(t)
	adopted, _ := f.fixture.plant(t, session.Session{Adopted: true})

	page := f.view(t).Body.String()
	if !strings.Contains(page, adopted.ID) {
		t.Fatalf("the fleet does not show the adopted session at all:\n%s", page)
	}
	if n := strings.Count(page, `class="card-unknown"`); n != 2 {
		t.Errorf("the adopted card states %d unknown values; want 2 — the name and the working directory:\n%s", n, page)
	}
	for _, slot := range []string{`class="card-name"`, `class="card-path"`} {
		if strings.Contains(page, slot) {
			t.Errorf("the adopted card rendered %s, which is the slot a real value goes in:\n%s", slot, page)
		}
	}
}

// TestAnEmptyFleetExplainsItselfInsteadOfRenderingNothing is FR-021, and the
// second half is FR-024a: docs/components.md documents this component with a
// "Start a session" action, and it must not be here — a browser could not sign
// the create, so the button would be broken as well as out of scope.
func TestAnEmptyFleetExplainsItselfInsteadOfRenderingNothing(t *testing.T) {
	t.Parallel()

	page := newFleet(t).view(t).Body.String()

	if !strings.Contains(page, `class="empty"`) {
		t.Errorf("a viewer who owns nothing got no empty state:\n%s", page)
	}
	if !strings.Contains(page, emptyFleetBody) {
		t.Errorf("the empty state carries no explanation:\n%s", page)
	}
	if strings.Contains(page, `<article class="card"`) {
		t.Errorf("an empty fleet rendered a card:\n%s", page)
	}
	// No detail, so nothing for a summary to come before. A row of zeroes here
	// would be detail where FR-021 asks for an explanation.
	if strings.Contains(page, `class="summary"`) {
		t.Errorf("an empty fleet rendered a summary of nothing:\n%s", page)
	}
	for _, offer := range mutationMarkup {
		if strings.Contains(strings.ToLower(page), offer) {
			t.Errorf("the empty state offers %q; there is no route behind this door to take it, and no secret to sign it with:\n%s", offer, page)
		}
	}
	// The action row itself, which the sweep above cannot see: the component
	// renders it as an empty container, so an action passed here would appear as a
	// row holding nothing rather than as a control. FR-024a asks for the parameter
	// to be absent at this call site, and this is that call site.
	if strings.Contains(page, "empty-action") {
		t.Errorf("the empty state rendered its action row; this milestone passes no action (FR-024a):\n%s", page)
	}
	if strings.Contains(page, "Start") {
		t.Errorf("the empty state tells the operator to start a session, which this page cannot do:\n%s", page)
	}
}

// TestTheRenderedFleetOffersNothingToActWith is FR-024a at the call site rather
// than at the component, which is where the requirement actually lives: the
// components take an action row as a parameter and this milestone passes none,
// so a test that only asked the card whether it *can* render a row says nothing
// about whether the page asked it to.
//
// It was a surviving mutation before it was a test. Handing the card an action
// left every other test in this package green, including both of the component
// tests written for FR-024a in T013.
func TestTheRenderedFleetOffersNothingToActWith(t *testing.T) {
	t.Parallel()

	f := newFleet(t)
	f.fixture.plant(t, session.Session{Name: "refactor the reaper", WorkDir: f.fixture.repo})
	page := f.view(t).Body.String()

	if strings.Contains(page, "card-actions") {
		t.Errorf("a card rendered its action row; the dashboard is read-only in this milestone (FR-022, FR-024a):\n%s", page)
	}
	for _, offer := range mutationMarkup {
		if strings.Contains(strings.ToLower(page), offer) {
			t.Errorf("the fleet page offers %q; the browser door serves GET only, and no page holds a secret to sign a mutation with:\n%s", offer, page)
		}
	}
}

// TestOpeningTheFleetLeavesTheIdleClockWhereItWas is FR-034f at the first
// production caller of the non-touching read T012 added.
//
// A dashboard left open in a tab would otherwise postpone the idle deadline of
// every session on it for as long as the tab lived, which is one of the five
// bounds Principle VI calls non-negotiable — and the failure is silent: the
// sessions simply never get reaped.
//
// The record is read back out of the store rather than off the page, because
// every read in this package hands back a copy and a copy says nothing about
// what was written.
func TestOpeningTheFleetLeavesTheIdleClockWhereItWas(t *testing.T) {
	t.Parallel()

	f := newFleet(t)
	// Deliberately behind the fixture's clock. A record whose LastActivity is the
	// instant the manager stands at makes a Touch a no-op — Store.Touch only moves
	// forward — so the two behaviours would be indistinguishable.
	stale, _ := f.fixture.plant(t, session.Session{Name: "left open", LastActivity: idleAt(testTime)})

	before, err := f.fixture.store.Get(stale.ID, auth.CallerOperator)
	if err != nil {
		t.Fatalf("read the planted session back: %v", err)
	}
	f.view(t)

	after, err := f.fixture.store.Get(stale.ID, auth.CallerOperator)
	if err != nil {
		t.Fatalf("read the session back after the page was served: %v", err)
	}
	if !after.LastActivity.Equal(before.LastActivity) {
		t.Errorf("opening the fleet moved the idle clock of session %s from %v to %v; watching is not driving (FR-034f)",
			stale.ID, before.LastActivity, after.LastActivity)
	}
}

// TestTheFleetIsServedByTheBrowserDoorAndNotTheAPIs is FR-013d's neighbour and
// the one deliberate change to a path milestone 1 answered: `GET /` is now the
// fleet, so it is refused by layer 1 rather than by a signature.
//
// The signed case is the half worth having. A caller holding the shared secret is
// not a caller this door serves, and a `GET /` that answered a signature would be
// the dashboard reachable by anyone who has the API's credential rather than by a
// verified person.
func TestTheFleetIsServedByTheBrowserDoorAndNotTheAPIs(t *testing.T) {
	t.Parallel()

	f := newFleet(t)
	cases := map[string]func(t *testing.T) *http.Request{
		"no credential at all": func(*testing.T) *http.Request {
			return httptest.NewRequest(http.MethodGet, "/", nil)
		},
		"the API's own signature": func(t *testing.T) *http.Request {
			r := httptest.NewRequest(http.MethodGet, "/", nil)
			signRequest(t, r, nil, testTime)
			return r
		},
	}

	for name, build := range cases {
		t.Run(name, func(t *testing.T) {
			w := httptest.NewRecorder()
			f.ServeHTTP(w, build(t))

			if w.Code != http.StatusUnauthorized {
				t.Fatalf("GET / with %s = %d; want %d", name, w.Code, http.StatusUnauthorized)
			}
			if body := w.Body.String(); body != string(bodyBrowserRefused) {
				t.Errorf("GET / with %s answered %q; want the browser door's one refusal — the API's JSON here would be an interface this caller never used", name, body)
			}
			if got := w.Header().Get(headerCSP); got != contentSecurityPolicy {
				t.Errorf("GET / with %s carries no policy (%q); a refusal is still a document a browser renders", name, got)
			}
		})
	}
}

// TestOnlyTheFleetPathIsTheFleetPage keeps the `{$}` in the route pattern.
//
// Without it `GET /` is a subtree pattern: every unrouted GET path would be
// answered with the fleet page — someone else's sessions rendered under the URL
// they mistyped. T016 moved those paths onto this same door, which makes the
// confinement matter more rather than less: both routes are now the browser's,
// and only the `{$}` decides which of the two a path reaches.
func TestOnlyTheFleetPathIsTheFleetPage(t *testing.T) {
	t.Parallel()

	f := newFleet(t)
	r := httptest.NewRequest(http.MethodGet, "/not-a-route", nil)
	r.Header.Set(headerAccessAssertion, f.keys.mint(t, f.keys.claims()))

	w := httptest.NewRecorder()
	f.ServeHTTP(w, r)

	if strings.Contains(w.Body.String(), emptyFleetTitle) {
		t.Errorf("GET /not-a-route was answered with the fleet page:\n%s", w.Body.String())
	}
}

// halfWritten is a template that emits markup and *then* fails. It is the shape
// the buffer in renderPage exists for, and the only shape that proves it: a page
// the set does not define fails before it writes a byte, so a renderer that
// executed straight into the response would pass a test built on one.
//
// That is not hypothetical — it is the mutation this suite first missed.
const halfWritten = `<p>everything before the failure</p>{{ .NoSuchField.AndNoSuchMethod }}`

// TestAPageThatCannotBeRenderedTellsTheCallerNothing is the failure path. It is
// nearly unreachable — the set is parsed at startup and the data is typed — and
// it is asserted because a template failure's own account of itself names a
// field, a value, or a session, and the caller must not receive either it or the
// half of the page that was written before it.
func TestAPageThatCannotBeRenderedTellsTheCallerNothing(t *testing.T) {
	t.Parallel()

	broken, err := parseTemplates(fstest.MapFS{"halts.html": &fstest.MapFile{Data: []byte(halfWritten)}})
	if err != nil {
		t.Fatalf("parse the failing fixture set: %v", err)
	}

	cases := map[string]func(f *fleet) string{
		"a page the set does not define": func(*fleet) string { return "not-a-page" },
		"a page that fails halfway through": func(f *fleet) string {
			f.Server.templates = broken
			return "halts"
		},
	}

	for name, page := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			f := newFleet(t)
			r := httptest.NewRequest(http.MethodGet, "/", nil)
			r.Header.Set(headerAccessAssertion, f.keys.mint(t, f.keys.claims()))

			w := httptest.NewRecorder()
			f.renderPage(w, r, http.StatusOK, page(f), struct{}{})

			if w.Code != http.StatusInternalServerError {
				t.Errorf("%s answered %d; want %d", name, w.Code, http.StatusInternalServerError)
			}
			if body := w.Body.String(); body != "" {
				t.Errorf("%s answered with %q; a response cannot be taken back once its first byte is written, so a page that failed halfway must never have started", name, body)
			}
			if len(f.failed) != 1 {
				t.Errorf("%s was reported %d times; want exactly 1 — an operator has to be able to find it", name, len(f.failed))
			}
		})
	}
}

// TestFormatAgeIsCoarseAndReadable pins the one string on the card the daemon
// composes itself rather than escaping. The negative case is reachable: an
// adopted session's CreatedAt is tmux's own reading of the host clock, so a clock
// that stepped backwards produces one, and "-3 minutes" on a card is worse than
// no age at all.
func TestFormatAgeIsCoarseAndReadable(t *testing.T) {
	t.Parallel()

	// A slice rather than a map keyed by duration, because the two bounds a card
	// really renders — the idle threshold and the absolute lifetime — are equal to
	// two of the plain durations above them, and a map would silently drop one of
	// each pair.
	cases := []struct {
		d    time.Duration
		want string
	}{
		{-time.Hour, "less than a minute"},
		{0, "less than a minute"},
		{30 * time.Second, "less than a minute"},
		{time.Minute, "1 minute"},
		{90 * time.Second, "1 minute"},
		{59 * time.Minute, "59 minutes"},
		{time.Hour, "1 hour"},
		{23 * time.Hour, "23 hours"},
		{24 * time.Hour, "1 day"},
		{50 * time.Hour, "2 days"},
		{session.IdleTimeout, "1 hour"},
		{session.AbsoluteLifetime, "1 day"},
	}

	for _, c := range cases {
		if got := formatAge(c.d); got != c.want {
			t.Errorf("formatAge(%v) = %q; want %q", c.d, got, c.want)
		}
	}
}

// viewOf opens one session's page as the verified operator would, and insists it
// was served — every claim below it is about what that page says.
func (f *fleet) viewOf(t *testing.T, id string) *httptest.ResponseRecorder {
	t.Helper()

	w := f.open(t, "/sessions/"+id+"/view")
	if w.Code != http.StatusOK {
		t.Fatalf("GET /sessions/%s/view = %d (%s); want %d", id, w.Code, w.Body.String(), http.StatusOK)
	}
	return w
}

// paneBody isolates what one session's pane element holds, so an assertion about
// the *screen* cannot be satisfied by markup elsewhere on the page — the same
// reason cardFor exists, for the element whose content came from the host.
func paneBody(t *testing.T, page, id string) string {
	t.Helper()

	opener := `<pre class="pane" id="pane-` + id + `"`
	_, after, ok := strings.Cut(page, opener)
	if !ok {
		t.Fatalf("the page renders no pane element for session %s:\n%s", id, page)
	}
	// Past the rest of the opening tag, which carries attributes this daemon
	// wrote, and up to the close.
	_, body, _ := strings.Cut(after, ">")
	body, _, _ = strings.Cut(body, "</pre>")
	return body
}

// TestTheSessionPageRendersTheCardAndTheScreen is the page itself: the address
// every card links to, serving the card again above the screen the session
// currently holds.
//
// The screen is the assertion that matters. It is host-authored bytes reaching a
// browser, so the payload here is the one docs/security.md is written about, and
// three things are asserted rather than one: the raw markup never appears, no
// element it could have opened is in the output, and the harmless text around it
// is still visible — rendered as text rather than dropped, which is the
// difference between escaping and sanitising.
func TestTheSessionPageRendersTheCardAndTheScreen(t *testing.T) {
	t.Parallel()

	const screen = "$ echo \"<script>alert(1)</script> <img src=x onerror=alert(2)>\"\nrunning tests…"

	f := newFleet(t)
	live, _ := f.fixture.plant(t, session.Session{Name: "refactor the reaper", WorkDir: f.fixture.repo})
	f.fixture.tmux.SetPane(live.TmuxName(), screen)

	page := f.viewOf(t, live.ID).Body.String()

	// The card, composed by the same component the fleet composes it with, so
	// the two pages cannot describe one session differently.
	card := cardFor(t, page, live.ID)
	for _, want := range []string{live.Name, f.fixture.repo, string(session.DisplayRunning)} {
		if !strings.Contains(card, want) {
			t.Errorf("the card on the session page does not carry %q:\n%s", want, card)
		}
	}

	// The screen, as text. The structural claim is the strong one: a text node
	// that went through html/template carries no angle bracket at all, so a pane
	// holding one is a pane that opened an element the host wrote (FR-028).
	pane := paneBody(t, page, live.ID)
	if strings.ContainsAny(pane, "<>") {
		t.Errorf("the pane carries an angle bracket, so something the session printed reached the page as markup:\n%s", pane)
	}
	// And the operator still sees what was really printed, rather than a
	// sanitised version of it — the difference escaping makes.
	for _, visible := range []string{"&lt;script&gt;alert(1)&lt;/script&gt;", "onerror=alert(2)", "running tests…"} {
		if !strings.Contains(pane, visible) {
			t.Errorf("the pane does not render %q; escaping shows what the session printed, it does not drop it:\n%s", visible, pane)
		}
	}

	// FR-032a at the call site: the page says what it is showing. An interface
	// that let an operator read a repainting screen as a transcript is one they
	// will trust wrongly.
	if !strings.Contains(page, "not scrollback") {
		t.Errorf("the page never says it shows the live screen rather than scrollback (FR-032a):\n%s", page)
	}

	// FR-024a and FR-022 on this page as well as on the fleet: the card takes an
	// action row as a parameter and this page passes none either.
	if strings.Contains(page, "card-actions") {
		t.Errorf("the session page rendered an action row; the dashboard is read-only in this milestone:\n%s", page)
	}
	for _, offer := range mutationMarkup {
		if strings.Contains(strings.ToLower(page), offer) {
			t.Errorf("the session page offers %q; the browser door serves GET only and no page holds a secret to sign with:\n%s", offer, page)
		}
	}
}

// TestTheSessionPageStatesAScreenItCouldNotRead is the failure this page can
// really meet: the record resolves, and the tmux exec behind the pane does not.
//
// A blank pane would be the wrong answer twice over — it claims the session
// printed nothing, and it hides a host that stopped answering. The card above is
// still true, so the page is still served; what changes is that the pane says so.
func TestTheSessionPageStatesAScreenItCouldNotRead(t *testing.T) {
	t.Parallel()

	f := newFleet(t)
	live, _ := f.fixture.plant(t, session.Session{Name: "still listed", WorkDir: f.fixture.repo})
	f.fixture.tmux.FailOp(tmuxctl.OpCapturePane, errors.New("no server running on /tmp/tmux-1000/default"))

	page := f.viewOf(t, live.ID).Body.String()

	if !strings.Contains(page, "could not be read") {
		t.Errorf("a screen the host could not be asked for renders as nothing at all:\n%s", page)
	}
	if strings.Contains(page, "<pre") {
		t.Errorf("the page rendered an empty pane rather than saying the screen is unknown; a blank <pre> claims the session printed nothing:\n%s", page)
	}
	// The card is the reason the page is still served, so it has to be on it.
	if !strings.Contains(page, live.Name) {
		t.Errorf("the page dropped the card as well as the screen:\n%s", page)
	}
	// The host's own account of the failure is for the operator, never for the
	// caller and never for the trail.
	if len(f.failed) != 1 {
		t.Errorf("an unreadable screen was reported %d times; want exactly 1 — an operator has to be able to find it", len(f.failed))
	}
	if trail := f.sink.String(); strings.Contains(trail, "tmux") {
		t.Errorf("the trail carries tmux's account of the failure:\n%s", trail)
	}
}

// TestOpeningASessionPageLeavesTheIdleClockWhereItWas is FR-034f at the second
// production caller of the non-touching read, and the one the requirement was
// written about: a browser tab left open on a session nobody is driving must not
// postpone its idle deadline, or a forgotten tab holds an unsandboxed shell open
// for as long as it lives.
//
// The record is read back out of the store rather than off the page, because
// every read in this package hands back a copy.
func TestOpeningASessionPageLeavesTheIdleClockWhereItWas(t *testing.T) {
	t.Parallel()

	f := newFleet(t)
	// Behind the fixture's clock deliberately: Store.Touch only moves forward, so
	// a record stamped at the manager's own instant would make a touch invisible.
	stale, _ := f.fixture.plant(t, session.Session{Name: "watched", LastActivity: idleAt(testTime)})

	before, err := f.fixture.store.Get(stale.ID, auth.CallerOperator)
	if err != nil {
		t.Fatalf("read the planted session back: %v", err)
	}
	f.viewOf(t, stale.ID)

	after, err := f.fixture.store.Get(stale.ID, auth.CallerOperator)
	if err != nil {
		t.Fatalf("read the session back after the page was served: %v", err)
	}
	if !after.LastActivity.Equal(before.LastActivity) {
		t.Errorf("opening the page of session %s moved its idle clock from %v to %v; watching is not driving (FR-034f)",
			stale.ID, before.LastActivity, after.LastActivity)
	}
}

// TestTheSessionPagesRecordNamesTheSessionAndNothingItRendered is the trail's
// half of this route: data-model.md puts session_id on this page's record and on
// no other dashboard route's, and nothing the page was made of may join it.
func TestTheSessionPagesRecordNamesTheSessionAndNothingItRendered(t *testing.T) {
	t.Parallel()

	const (
		chosenName = "a-name-a-caller-chose"
		chosenPath = "/srv/a-directory-a-caller-named"
		printed    = "a-line-the-session-printed"
	)

	f := newFleet(t)
	live, _ := f.fixture.plant(t, session.Session{Name: chosenName, WorkDir: chosenPath})
	f.fixture.tmux.SetPane(live.TmuxName(), printed)

	page := f.viewOf(t, live.ID).Body.String()
	for _, rendered := range []string{chosenName, chosenPath, printed, testOperatorEmail} {
		if !strings.Contains(page, rendered) {
			t.Fatalf("the page never rendered %q, so its absence from the trail proves nothing:\n%s", rendered, page)
		}
	}

	record := f.only(t)
	if got, want := record["action"], string(audit.ActionDashboardView); got != want {
		t.Errorf("the session page was recorded as %v; want %v", got, want)
	}
	if got := record["session_id"]; got != live.ID {
		t.Errorf("the record names session %v; want %v — the id comes off the daemon's own record", got, live.ID)
	}

	trail := f.sink.String()
	for _, secret := range []struct{ what, value string }{
		{"the session name a caller chose", chosenName},
		{"the working directory a caller chose", chosenPath},
		{"what the session printed", printed},
		{"the address the edge verified", testOperatorEmail},
	} {
		if strings.Contains(trail, secret.value) {
			t.Errorf("the trail carries %s; a record holds what the daemon derived and never what it rendered (FR-035):\n%s", secret.what, trail)
		}
	}
}

// ---------------------------------------------------------------------------
// The US1 acceptance suite (T017)
//
// Everything above is a claim about a handler. Everything below is a claim about
// the story — what an operator receives, asserted against the response and not
// against the code that composed it, which is why each one drives a real request
// through the real router and reads only what came back.
//
// The four are contracts/dashboard.md's own: zero external origins (SC-005),
// every state legible without colour (SC-009), a second owner's session withheld
// through the dashboard's own route (FR-037b, SC-016), and the identity in the
// header taken from the assertion and from nothing the request supplied (FR-020,
// FR-036). A fifth asserts what the trail keeps of all this (FR-035, SC-008).
// ---------------------------------------------------------------------------

// urlPosition matches the attributes a browser *fetches* from, which is the only
// place FR-025 is about.
//
// A session name or a working directory that reads like a URL is text, and stays
// text: it reaches the page inside a title attribute and between two tags, both
// escaped. So a sweep for "https://" anywhere in the markup would refuse a
// caller for naming a session after the repository it works on, while a page
// that really did pull a script off a CDN would be caught by neither that sweep
// nor the template sweeps in partials_test.go — those read the sources, which
// have no caller text in them at all. What SC-005 forbids is the page causing a
// request to somewhere else, and this is the list of attributes that causes one.
var urlPosition = regexp.MustCompile(`(?i)\s(href|src|srcset|action|formaction|poster|cite|background|data|manifest)\s*=\s*"([^"]*)"`)

// otherOrigin is a reference that leaves this origin: any scheme at all, and the
// protocol-relative form, which carries no scheme to spot and is the one a
// sweep for "https:" misses.
var otherOrigin = regexp.MustCompile(`(?i)^([a-z][a-z0-9+.\-]*:|//)`)

// styleFetch is the two CSS constructs that fetch. No rendered page may contain
// either, and the reason is structural rather than stylistic: the pages carry no
// CSS at all, inline or otherwise, so the only stylesheet is the one they link.
// The same sweep over the stylesheet's own rules is stylesheet_test.go's.
var styleFetch = regexp.MustCompile(`(?i)@import|url\(`)

// TestNoPageThisMilestoneServesFetchesFromAnotherOrigin is SC-005 and FR-025 over
// every page the browser door can render, with caller text on the page that would
// become an external origin the moment anything put it in a fetching position.
func TestNoPageThisMilestoneServesFetchesFromAnotherOrigin(t *testing.T) {
	t.Parallel()

	const (
		nameLikeAScript = "https://cdn.example.net/analytics.js"
		pathLikeAHost   = "//an-origin.example/not-a-directory"
	)

	f := newFleet(t)
	hostile, _ := f.fixture.plant(t, session.Session{Name: nameLikeAScript, WorkDir: pathLikeAHost})
	// The screen is the other half of the same sweep on the session's own page:
	// what a session prints is the one text on this dashboard that nobody chose
	// at all, and a page that put it in a fetching position would be fetching
	// from wherever the host said.
	f.fixture.tmux.SetPane(hostile.TmuxName(), `<link rel="stylesheet" href="https://cdn.example.net/theme.css">`)

	loaded := f.view(t).Body.String()
	// Without this the case is a page with no caller text on it, which every one
	// of the sweeps below would pass by rendering nothing.
	for _, text := range []string{nameLikeAScript, pathLikeAHost} {
		if !strings.Contains(loaded, text) {
			t.Fatalf("the fleet never rendered %q, so this case sweeps a page nothing hostile reached:\n%s", text, loaded)
		}
	}

	notFound := f.open(t, "/not-a-route")
	if notFound.Code != http.StatusNotFound {
		t.Fatalf("GET /not-a-route = %d; want %d — this case is about the page, so it has to be the page", notFound.Code, http.StatusNotFound)
	}

	pages := map[string]string{
		"the fleet, rendering caller text that reads like a reference": loaded,
		"an empty fleet":     newFleet(t).view(t).Body.String(),
		"the not-found page": notFound.Body.String(),
		"a session's page, rendering a screen that reads like a stylesheet": f.viewOf(t, hostile.ID).Body.String(),
	}

	for what, page := range pages {
		t.Run(what, func(t *testing.T) {
			t.Parallel()

			refs := urlPosition.FindAllStringSubmatch(page, -1)
			if len(refs) == 0 {
				t.Fatalf("%s references nothing at all, so the sweep below would pass on a blank response:\n%s", what, page)
			}

			linked := false
			for _, ref := range refs {
				if otherOrigin.MatchString(ref[2]) {
					t.Errorf("%s fetches %s=%q, which is another origin (FR-025, SC-005):\n%s", what, ref[1], ref[2], page)
				}
				linked = linked || ref[2] == "/static/crswd.css"
			}
			// The one reference every page really makes. A page that stopped
			// linking the stylesheet would satisfy "no external origin" by
			// referencing nothing, and this suite would have nothing to sweep.
			if !linked {
				t.Errorf("%s links no stylesheet, so it proves nothing about what a page fetches:\n%s", what, page)
			}
			if got := styleFetch.FindString(page); got != "" {
				t.Errorf("%s carries %q; a page holds no CSS, so the only stylesheet is the one it links:\n%s", what, got, page)
			}
		})
	}
}

// classAttribute is every attribute that carries colour on these pages. Nothing
// else can: docs/security.md's policy forbids the inline style attribute, the
// template sweep refuses it at build time, and FR-023 keeps every colour in a
// token the stylesheet reads. So removing these removes the colour, and what
// survives is what an operator who cannot see it receives.
var classAttribute = regexp.MustCompile(`\sclass="[^"]*"`)

// TestEveryCardIsLegibleWithoutColour is SC-009 and FR-019 as an acceptance
// claim rather than a component one.
//
// partials_test.go asserts that the pill *can* render its label; this asserts
// that what a browser is sent still distinguishes one state from another after
// every colour on it is gone. The two are not the same claim — a page could
// compose a labelled pill and still tell an operator apart by nothing but a
// class, which is what the summary row does if nobody looks.
func TestEveryCardIsLegibleWithoutColour(t *testing.T) {
	t.Parallel()

	f := newFleet(t)
	stale, _ := f.fixture.plant(t, session.Session{
		Name: "left open", WorkDir: f.fixture.repo, LastActivity: idleAt(testTime),
	})
	live, _ := f.fixture.plant(t, session.Session{
		Name: "in use", WorkDir: f.fixture.repo, LastActivity: runningAt(testTime),
	})
	other, _ := f.fixture.plant(t, session.Session{
		Name: "also in use", WorkDir: f.fixture.repo, LastActivity: runningAt(testTime),
	})

	page := f.view(t).Body.String()
	// The premise of stripping classes: nothing else on the page could be
	// carrying colour. Both are absences the CSP would also refuse at runtime,
	// asserted here because a proxy that stripped the header must not be the only
	// thing between a template and an inline colour.
	if strings.Contains(page, " style=") {
		t.Fatalf("the page carries an inline style attribute, so removing its classes does not remove its colour:\n%s", page)
	}
	if strings.Contains(strings.ToLower(page), "<style") {
		t.Fatalf("the page carries an inline stylesheet, so removing its classes does not remove its colour:\n%s", page)
	}

	colourless := classAttribute.ReplaceAllString(page, "")
	if n := strings.Count(colourless, "<article"); n != 3 {
		t.Fatalf("the colourless page holds %d cards; want 3 — the card is the only <article> the dashboard renders:\n%s", n, colourless)
	}

	for _, want := range []struct {
		id            string
		state, notThe session.DisplayState
	}{
		{stale.ID, session.DisplayIdle, session.DisplayRunning},
		{live.ID, session.DisplayRunning, session.DisplayIdle},
		{other.ID, session.DisplayRunning, session.DisplayIdle},
	} {
		card := cardFor(t, colourless, want.id)
		if !strings.Contains(card, ">"+string(want.state)+"<") {
			t.Errorf("with the colour gone, the card for session %s no longer says it is %q:\n%s", want.id, want.state, card)
		}
		if strings.Contains(card, ">"+string(want.notThe)+"<") {
			t.Errorf("the card for session %s reads %q as well as %q:\n%s", want.id, want.notThe, want.state, card)
		}
	}

	// The summary row is the half that would fail quietly. Its counts are
	// unambiguous, but the state each one counts is carried by the pill beside it
	// — and if that pill said nothing, the row would be three numbers whose
	// meaning is a colour.
	for _, want := range []struct {
		state session.DisplayState
		count string
	}{
		{session.DisplayRunning, "2"},
		{session.DisplayIdle, "1"},
	} {
		if !summaryReads(colourless, want.state, want.count) {
			t.Errorf("with the colour gone, no summary entry reads %q %s:\n%s", want.state, want.count, colourless)
		}
	}
}

// summaryReads reports whether one entry of the summary row names a state and
// its count as text, in that entry rather than anywhere on the page.
//
// Per entry, because the row is one <li> per state: a page-wide search for the
// state and a page-wide search for the number would both pass on a row that had
// paired them the wrong way round.
func summaryReads(page string, state session.DisplayState, count string) bool {
	for _, entry := range strings.Split(page, "<li")[1:] {
		body, _, _ := strings.Cut(entry, "</li>")
		if strings.Contains(body, ">"+string(state)+"<") && strings.Contains(body, ">"+count+"<") {
			return true
		}
	}
	return false
}

// TestASecondOwnersSessionIsInvisibleThroughTheDashboardsOwnRoute is FR-037b and
// SC-016, exercised where the requirement asks for it: through the dashboard,
// with a synthetic second owner. Milestone 1's API test asserts the same property
// on the other door, and pointing at it would be asserting nothing about this one.
//
// The claim is stronger than "the card is absent". Two responses are compared —
// one from a host holding a session this viewer does not own, one from a host
// holding nothing at all — and they must be identical byte for byte, because the
// difference between "not yours" and "does not exist" is what enumeration is made
// of. A page that leaked the difference by a count, a heading, or a stray space
// would pass an absence check and fail this one.
func TestASecondOwnersSessionIsInvisibleThroughTheDashboardsOwnRoute(t *testing.T) {
	t.Parallel()

	const stranger auth.CallerID = "a-second-operator"

	held := newFleet(t)
	theirs, _ := held.fixture.plant(t, session.Session{
		Owner: stranger, Name: "not yours", WorkDir: held.fixture.repo, LastActivity: idleAt(testTime),
	})
	empty := newFleet(t)

	withTheirs, withNothing := held.view(t), empty.view(t)
	if withTheirs.Body.String() != withNothing.Body.String() {
		t.Errorf("the fleet distinguishes a host holding another owner's session from a host holding none:\ngot:\n%s\nwant:\n%s",
			withTheirs.Body.String(), withNothing.Body.String())
	}

	// The page the card links to, which since T021b really serves a session the
	// viewer owns — so this is now FR-037b's own assertion on that route rather
	// than the guard it was while nothing answered there. Three requests, because
	// two of them alone are passed by a page that says "not yours" politely: a
	// session held by someone else, an id that never existed, and a path this
	// daemon has no route for at all must be one answer.
	unknown := strings.Repeat("c", session.IDLen)
	notMine, neverExisted := held.open(t, "/sessions/"+theirs.ID+"/view"), held.open(t, "/sessions/"+unknown+"/view")
	if notMine.Code != http.StatusNotFound {
		t.Errorf("another owner's session answers %d; want %d — it is not this viewer's to know about",
			notMine.Code, http.StatusNotFound)
	}
	if notMine.Code != neverExisted.Code {
		t.Errorf("another owner's session answers %d and one that never existed answers %d; the difference is what enumeration is made of",
			notMine.Code, neverExisted.Code)
	}
	if notMine.Body.String() != neverExisted.Body.String() {
		t.Errorf("another owner's session and one that never existed answer differently:\ngot:\n%s\nwant:\n%s",
			notMine.Body.String(), neverExisted.Body.String())
	}
	if noRoute := held.open(t, "/not-a-route"); notMine.Body.String() != noRoute.Body.String() {
		t.Errorf("a session this viewer may not see answers differently from a path nothing claims:\ngot:\n%s\nwant:\n%s",
			notMine.Body.String(), noRoute.Body.String())
	}
	if strings.Contains(notMine.Body.String(), theirs.Name) {
		t.Errorf("the answer for another owner's session names it:\n%s", notMine.Body.String())
	}

	// Non-vacuity: the same route, for a session this viewer does own, is a page.
	// Without it every comparison above is satisfied by a route that refuses
	// everyone.
	mine, _ := held.fixture.plant(t, session.Session{Name: "mine to watch", WorkDir: held.fixture.repo})
	if page := held.viewOf(t, mine.ID).Body.String(); !strings.Contains(page, mine.Name) {
		t.Errorf("the viewer's own session does not render on the route that withholds the stranger's:\n%s", page)
	}
}

// TestTheHeaderNamesTheAssertionAndNothingTheRequestSupplied is FR-020 and
// FR-036 at the page.
//
// The header component executes against the VerifiedOperator, so no
// request-supplied value is in scope for it — which is the point, and is exactly
// why this has to be asserted from outside: "the template is fed the right
// thing" is a claim about today's code, while this is a claim about the response.
//
// Byte-identity is the assertion rather than "the stranger's address is absent".
// A page that echoed a request field somewhere other than the header — a title, a
// hidden attribute, a comment — would pass an absence check for one spelling and
// still be reflecting a caller's text back at them.
func TestTheHeaderNamesTheAssertionAndNothingTheRequestSupplied(t *testing.T) {
	t.Parallel()

	f := newFleet(t)
	f.fixture.plant(t, session.Session{Name: "refactor the reaper", WorkDir: f.fixture.repo})

	plain := f.view(t).Body.String()
	if !strings.Contains(plain, testOperatorEmail) {
		t.Fatalf("the page does not name the verified operator at all:\n%s", plain)
	}

	// Everything a caller can put on a request that looks like an identity,
	// including the two the edge itself writes. The daemon reads exactly one
	// header and validates it; the rest are here because "it is not read" is a
	// property worth a test, and because the cookie in particular is the
	// browser's credential to the edge rather than the edge's product for the
	// daemon (browser.go).
	r := httptest.NewRequest(http.MethodGet, "/?email="+testStrangerEmail+"&operator="+testStrangerEmail, nil)
	r.Header.Set(headerAccessAssertion, f.keys.mint(t, f.keys.claims()))
	for _, field := range []string{
		"Cf-Access-Authenticated-User-Email",
		"X-Forwarded-Email",
		"X-Forwarded-User",
		"X-Remote-User",
		"From",
	} {
		r.Header.Set(field, testStrangerEmail)
	}
	r.Header.Set("Cookie", "CF_Authorization="+testStrangerEmail)
	// A second assertion, genuinely signed by the same key server, naming someone
	// the allowlist refuses. If the door read the last value rather than the
	// first, this request would be refused outright — so the status below is as
	// much of the assertion as the body is.
	r.Header.Add(headerAccessAssertion, f.keys.mint(t, claimsFor(f.keys, testStrangerEmail)))

	w := httptest.NewRecorder()
	f.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("GET / carrying identity-shaped request fields = %d (%s); want %d — the door reads the assertion and nothing else",
			w.Code, w.Body.String(), http.StatusOK)
	}
	if strings.Contains(w.Body.String(), testStrangerEmail) {
		t.Errorf("the page names an address the request supplied rather than the one the edge signed (FR-020, FR-036):\n%s", w.Body.String())
	}
	if w.Body.String() != plain {
		t.Errorf("a request carrying identity-shaped fields was answered with a different page:\ngot:\n%s\nwant:\n%s", w.Body.String(), plain)
	}
}

// claimsFor is the key server's own claim set with the address changed, so the
// second assertion above differs from a working one by the person it names and
// by nothing else.
func claimsFor(k *keyServer, email string) map[string]any {
	claims := k.claims()
	claims["email"] = email
	return claims
}

// TestTheFleetsRecordCarriesNothingThePageRendered is FR-035 and SC-008 at this
// route: one record for the request, and not a byte of what the response was
// made of.
//
// internal/audit's leak suite makes this claim across the whole daemon, but it
// drives the API door only — the browser door's own operations are not in it, so
// until they are, this is the assertion covering the values a *page* has in
// scope: a name and a working directory a caller chose, the address the edge
// verified, and the assertion itself.
func TestTheFleetsRecordCarriesNothingThePageRendered(t *testing.T) {
	t.Parallel()

	const (
		chosenName = "a-name-a-caller-chose"
		chosenPath = "/srv/a-directory-a-caller-named"
	)

	f := newFleet(t)
	f.fixture.plant(t, session.Session{Name: chosenName, WorkDir: chosenPath})

	assertion := f.keys.mint(t, f.keys.claims())
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.Header.Set(headerAccessAssertion, assertion)

	w := httptest.NewRecorder()
	f.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("GET / = %d (%s); want %d", w.Code, w.Body.String(), http.StatusOK)
	}

	page := w.Body.String()
	for _, rendered := range []string{chosenName, chosenPath, testOperatorEmail} {
		if !strings.Contains(page, rendered) {
			t.Fatalf("the page never rendered %q, so its absence from the trail proves nothing:\n%s", rendered, page)
		}
	}

	f.only(t)
	trail := f.sink.String()
	for _, secret := range []struct{ what, value string }{
		{"the session name a caller chose", chosenName},
		{"the working directory a caller chose", chosenPath},
		{"the address the edge verified", testOperatorEmail},
		{"the assertion itself", assertion},
	} {
		if strings.Contains(trail, secret.value) {
			t.Errorf("the trail carries %s; a record holds what the daemon derived and never what it rendered (FR-035):\n%s", secret.what, trail)
		}
	}
}
