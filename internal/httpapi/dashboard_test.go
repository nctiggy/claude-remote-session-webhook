// Internal test, matching the rest of the package. Every claim here is about the
// page a browser receives, so each one drives GET / through the real router, the
// real browser door, and a real *access.Validator over a locally generated key
// pair — the same arrangement browser_test.go uses, because a handler called
// directly would prove the markup and not the route.
//
// The US1 acceptance suite is T017's and lives in this file too when it arrives:
// zero external origins, every state distinguishable without colour, and the
// cross-owner refusal through GET /sessions/{id}/view, which no route serves yet.
package httpapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/nctiggy/claude-remote-session-webhook/internal/audit"
	"github.com/nctiggy/claude-remote-session-webhook/internal/auth"
	"github.com/nctiggy/claude-remote-session-webhook/internal/session"
)

// fleet is a server whose browser door admits a real assertion, together with the
// key server that mints one.
type fleet struct {
	*testServer
	keys *keyServer
}

func newFleet(t *testing.T) *fleet {
	t.Helper()

	keys := newKeyServer(t)
	return &fleet{testServer: newAuditedServerWith(t, keys.validator(t)), keys: keys}
}

// view opens the fleet page as the verified operator would.
//
// The request carries no layer-2 signature and no bearer token, which is FR-012
// from the browser's side: this door refuses only by the check that applies to it,
// and a page that needed a signature would be a page no browser could open.
func (f *fleet) view(t *testing.T) *httptest.ResponseRecorder {
	t.Helper()

	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.Header.Set(headerAccessAssertion, f.keys.mint(t, f.keys.claims()))

	w := httptest.NewRecorder()
	f.ServeHTTP(w, r)
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
func cardFor(t *testing.T, page, id string) string {
	t.Helper()

	const opener = `<article class="card"`
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
// answered with the fleet page, which is not this route's to decide. Moving
// unrouted paths to this door is FR-013d's and T016's, and until then they keep
// milestone 1's answer.
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
			f.renderPage(w, r, page(f), struct{}{})

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
