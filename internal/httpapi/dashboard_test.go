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
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/nctiggy/claude-remote-session-webhook/internal/audit"
	"github.com/nctiggy/claude-remote-session-webhook/internal/auth"
	"github.com/nctiggy/claude-remote-session-webhook/internal/config"
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

// sectionOf returns the contents of the first <section class="…"> on a rendered
// page, so a claim about one region cannot be satisfied by markup somewhere else
// on it. That distinction is the whole of the empty state's assertions now that
// the page around it really does carry a control.
func sectionOf(t *testing.T, page, class string) string {
	t.Helper()

	opener := `<section class="` + class + `">`
	_, after, ok := strings.Cut(page, opener)
	if !ok {
		t.Fatalf("the page renders no %s:\n%s", opener, page)
	}
	body, _, ok := strings.Cut(after, "</section>")
	if !ok {
		t.Fatalf("the page's %s is never closed:\n%s", opener, page)
	}
	return body
}

// openingTag is the opening tag of the first element of this kind carrying this
// class, so a test can assert about the attributes on it rather than about the
// whole page. `hidden` is the one that matters now: it is what decides which of
// the fleet page's two shapes an operator is looking at (issue #51), and it is
// invisible to a Contains check that only asks whether the markup is there.
func openingTag(t *testing.T, page, element, class string) string {
	t.Helper()

	tag := regexp.MustCompile(`<` + element + ` class="` + class + `"[^>]*>`).FindString(page)
	if tag == "" {
		t.Fatalf("the page renders no <%s class=%q>:\n%s", element, class, page)
	}
	return tag
}

// TestTheFleetPageComposesBothOfItsShapes is what makes updating in place
// possible without the live half becoming a second fleet page (issue #51).
//
// The page used to render one shape or the other, so a session appearing at an
// empty page and the last one leaving both had to be answered with a reload —
// the script cannot compose an empty state, a summary row or a count without
// becoming a second place any of them can be wrong. Both are rendered now and
// the one that does not apply is hidden, which turns those two events into a
// `hidden` attribute the script moves between markup the daemon authored.
//
// **Must fail when** either shape is left out of a render, because that is the
// state where the live half has nothing to reveal and the reload comes back.
func TestTheFleetPageComposesBothOfItsShapes(t *testing.T) {
	t.Parallel()

	running := newFleet(t)
	running.fixture.plant(t, session.Session{Name: "busy", WorkDir: running.fixture.repo, LastActivity: runningAt(testTime)})

	page := running.view(t).Body.String()
	for _, shown := range []struct {
		element string
		class   string
	}{{"ul", "summary"}, {"div", "grid"}} {
		if tag := openingTag(t, page, shown.element, shown.class); strings.Contains(tag, "hidden") {
			t.Errorf("a fleet with a session in it hides its %s (%q):\n%s", shown.class, tag, page)
		}
	}
	// The empty state is composed on this render too, and hidden. Without it the
	// last card leaving has nothing to reveal, and the page that had one session
	// is a page with a blank space where FR-021 asks for an explanation.
	if tag := openingTag(t, page, "section", "empty"); !strings.Contains(tag, "hidden") {
		t.Errorf("a fleet with a session in it shows the empty state as well (%q):\n%s", tag, page)
	}

	// And the other direction, which is the transition a naive implementation
	// misses: the first card arriving at a page that owns nothing.
	if tag := openingTag(t, newFleet(t).view(t).Body.String(), "section", "empty"); strings.Contains(tag, "hidden") {
		t.Errorf("a fleet with nothing in it hides the empty state (%q); the page explains nothing", tag)
	}
}

// TestAnEmptyFleetExplainsItselfInsteadOfRenderingNothing is FR-021, and the
// second half is FR-024a: docs/components.md documents this component with a
// "Start a session" action, and it must not be here.
//
// The reason it must not be here has changed, and the assertion is worth keeping
// precisely because of that. Through milestone 2 the answer was that no route
// existed to take it. T009 and T010 built that route and its form, so the answer
// is now the design system's: the empty state is the one surface where the rain
// runs at full strength, and rain never goes behind reading content — "not a
// pane, a card grid, a form, or a table". The create form is a sibling of this
// section on the page, which is what the sweeps below are scoped to the section
// to say.
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
	// No detail, so nothing for a summary to come before: a row of zeroes on
	// screen here would be detail where FR-021 asks for an explanation. It is
	// composed and hidden rather than left out, which is what lets the first card
	// to arrive be shown without the page being rebuilt around it (issue #51) —
	// what FR-021 forbids is showing it, and `hidden` is the browser's own way of
	// not showing something.
	if row := openingTag(t, page, "ul", "summary"); !strings.Contains(row, "hidden") {
		t.Errorf("an empty fleet shows a summary of nothing (%q):\n%s", row, page)
	}
	if fleet := openingTag(t, page, "div", "grid"); !strings.Contains(fleet, "hidden") {
		t.Errorf("an empty fleet shows an empty grid (%q):\n%s", fleet, page)
	}

	empty := sectionOf(t, page, "empty")
	for _, offer := range mutationMarkup {
		if strings.Contains(strings.ToLower(empty), offer) {
			t.Errorf("the empty state offers %q; the rain runs at full strength behind this section, and the design system keeps a control off the rain:\n%s", offer, empty)
		}
	}
	// The action row itself, which the sweep above cannot see: the component
	// renders it as an empty container, so an action passed here would appear as a
	// row holding nothing rather than as a control. FR-024a asks for the parameter
	// to be absent at this call site, and this is that call site.
	if strings.Contains(empty, "empty-action") {
		t.Errorf("the empty state rendered its action row; this page passes no action (FR-024a):\n%s", empty)
	}
}

// TestTheRenderedFleetOffersTheCreateForm is T010 at the call site rather than
// at the component, which is where it can be lost silently: the form is
// perfectly capable of rendering for a page that never asks it to, and this
// repository has shipped code nothing called three times.
//
// The empty-fleet row is the one that matters. An operator who owns nothing is
// exactly the operator who needs to start something, and a form rendered only
// beside the grid would be missing from the one page where the fleet is empty —
// a dashboard that can create only once it already has.
func TestTheRenderedFleetOffersTheCreateForm(t *testing.T) {
	t.Parallel()

	cases := map[string]bool{
		"an empty fleet":               false,
		"a fleet with a session in it": true,
	}

	for name, populated := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			f := newFleet(t)
			if populated {
				f.fixture.plant(t, session.Session{Name: "refactor the reaper", WorkDir: f.fixture.repo})
			}
			page := f.view(t).Body.String()

			create := sectionOf(t, page, "create")
			form := cardForm.FindStringSubmatch(create)
			if form == nil {
				t.Fatalf("the create section on the rendered fleet holds no form, so the control T010 built reaches no operator:\n%s", create)
			}

			target := strings.TrimPrefix(patternDashboardCreate, "POST ")
			if !strings.Contains(form[1], `action="`+target+`"`) {
				t.Errorf("the create form does not post to %q, which is the route this daemon serves for it:\n%s", target, create)
			}
			// The token the gate demands, on the form rather than merely on the
			// page: a hidden field outside every form is submitted by nothing.
			if !hiddenTokenField.MatchString(form[2]) {
				t.Errorf("the create form submits no %s, so the gate refuses it:\n%s", fieldPageToken, create)
			}

			// The roots this daemon is configured with, named where the operator
			// fills the field in (T014). It is asserted on the served page rather
			// than on the component because the component cannot lose it: what can
			// is the page forgetting to pass them, which is the shape of failure
			// this repository has shipped three times.
			for _, root := range f.cfg.Roots {
				if !strings.Contains(create, root.Path) {
					t.Errorf("the create form does not name the approved root %q, so the field is one an operator has to guess at:\n%s",
						root.Path, create)
				}
			}

			// Outside every card, which is the placement the task is about: a create
			// names no session, so a form drawn inside a card would act for whichever
			// card happened to hold it — and would be drawn once per session on a
			// page that needs it once.
			if strings.Contains(create, "<article") {
				t.Errorf("the create form is rendered inside a card:\n%s", create)
			}
			if strings.Contains(page, `action="`+target+`"`) && strings.Count(page, `action="`+target+`"`) != 1 {
				t.Errorf("the page posts to %q %d times; one page offers one create:\n%s", target, strings.Count(page, `action="`+target+`"`), page)
			}
		})
	}
}

// TestTheRenderedFleetOffersWhatDiscoveryFound is T023 at the call site, which
// is the half a walk with three passing unit tests can still be missing: the
// picker's markup shipped one task before its only source, so a daemon that
// discovers directories no page asks it for renders an empty field forever. This
// repository has shipped code nothing called three times.
//
// The pair of cases is what makes it an assertion about the *configuration*
// rather than about the markup, and both halves are about the discovered
// *child*: a union wired to anything constant — a literal, the roots alone —
// fails the first case, and one with the gate dropped fails the second. From
// T006 the roots are a source of their own, so the second case asserts that they
// are offered rather than that nothing is.
func TestTheRenderedFleetOffersWhatDiscoveryFound(t *testing.T) {
	t.Parallel()

	for name, discover := range map[string]bool{
		"an operator who asked for discovery":  true,
		"an operator who did not, the default": false,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			f := newFleet(t)
			// The fixture's root is a real resolved directory with one
			// subdirectory in it, which is what a walk is a question about — the
			// server's own root is deliberately a path that resolves to nothing.
			// Both are set on this server's own Config before anything has
			// rendered, and only the fleet's projection reads either.
			f.cfg.Roots = []config.ApprovedRoot{{Path: f.fixture.root}}
			f.cfg.DiscoverRoots = discover

			create := sectionOf(t, f.view(t).Body.String(), "create")
			suggestion := `<option value="` + f.fixture.repo + `">`

			if discover {
				if !strings.Contains(create, suggestion) {
					t.Errorf("discovery is on and the create form offers no %s, so the datalist T022 rendered has nothing in it:\n%s", f.fixture.repo, create)
				}
				if !strings.Contains(create, `list="workdir-suggestions"`) {
					t.Errorf("the create form lists suggestions the field does not point at:\n%s", create)
				}
				return
			}
			if strings.Contains(create, suggestion) {
				t.Errorf("discovery is off and the create form names %s anyway; the host is read only when an operator asks:\n%s", f.fixture.repo, create)
			}
			// The list itself is still rendered, and from T006 that is the
			// requirement rather than a leak: the approved roots are a source of
			// their own and they are always offered, so "discovery is off" means
			// this daemon offers no path it had to read the host to learn — not
			// that it offers nothing. What the operator configured is above; what
			// is inside it is what the gate above holds back.
			if root := `<option value="` + f.fixture.root + `">`; !strings.Contains(create, root) {
				t.Errorf("discovery is off and the create form offers no %s either, so a default install meets a picker with nothing in it:\n%s", f.fixture.root, create)
			}
		})
	}
}

// TestTheRenderedFleetReadsNoConversationStore is US5 at the call site: the page
// a browser is handed no longer asks Claude Code's own store what it has
// recorded, because the form no longer carries the question.
//
// It replaces the assertion that the offer was wired to the store, and it is the
// same claim read the other way round — the shape milestone 2's empty-state test
// took when a component was made to stop offering something.
//
// **Must fail when** the projection keeps walking the store "so the data is there
// when the field comes back". That walk ran on the render path, for a fact
// nothing on the page could use, in the one place this daemon reads a directory
// it was never asked about.
//
// It is serial, alone in this file, because it describes a home directory: the
// store's location comes from the environment Claude Code itself reads.
func TestTheRenderedFleetReadsNoConversationStore(t *testing.T) {
	const conversation = "8f14e45f-ceea-467a-9b3d-0f2fc9de5b21"

	f := newFleet(t)
	f.cfg.Roots = []config.ApprovedRoot{{Path: f.fixture.root}}
	f.cfg.DiscoverRoots = true

	// A host that really has recorded a conversation, which is the only state in
	// which a surviving walk would show itself.
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := filepath.Join(home, ".claude", "projects", strings.ReplaceAll(f.fixture.repo, "/", "-"))
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatalf("create the conversation store: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, conversation+".jsonl"), []byte("{}\n"), 0o600); err != nil {
		t.Fatalf("record a conversation: %v", err)
	}

	create := sectionOf(t, f.view(t).Body.String(), "create")

	if strings.Contains(create, conversation) {
		t.Errorf("the create form names a conversation off this host, so the render still reads the store:\n%s", create)
	}
	for _, gone := range []string{`name="resume"`, "conversation-suggestions"} {
		if strings.Contains(create, gone) {
			t.Errorf("the rendered create form still carries %s:\n%s", gone, create)
		}
	}
}

// TestTheRenderedFleetOffersTheDestroyControl is US1 at the call site rather
// than at the component, which is where it can be lost silently: the card is
// perfectly capable of rendering a control that no page ever asks it for, and
// this repository has shipped code nothing called three times.
//
// It is the inverse of the test that stood here through milestone 2, when the
// same assertion read the other way round — no page passed an action, and a card
// that rendered one anyway was the surviving mutation that test was written for.
// What has not changed is that the claim is made about the page a browser is
// handed, and about the card belonging to one session rather than about markup
// somewhere on it.
func TestTheRenderedFleetOffersTheDestroyControl(t *testing.T) {
	t.Parallel()

	f := newFleet(t)
	live, _ := f.fixture.plant(t, session.Session{Name: "refactor the reaper", WorkDir: f.fixture.repo})
	page := f.view(t).Body.String()
	card := cardFor(t, page, live.ID)

	if !strings.Contains(card, `class="card-actions"`) {
		t.Fatalf("the card on the rendered fleet carries no action row, so the control T007 built reaches no operator:\n%s", card)
	}
	target := strings.Replace(strings.TrimPrefix(patternDashboardDestroy, "POST "), "{"+pathValueID+"}", live.ID, 1)
	if !strings.Contains(card, `action="`+target+`"`) {
		t.Errorf("the card's control does not post to %q, which is the route this daemon serves for it:\n%s", target, card)
	}
	// The token the gate demands, on the form rather than merely on the page: a
	// hidden field outside every form is submitted by nothing.
	form := cardForm.FindStringSubmatch(card)
	if form == nil {
		t.Fatalf("the card renders an action row holding no form:\n%s", card)
	}
	if !hiddenTokenField.MatchString(form[2]) {
		t.Errorf("the card's destroy form submits no %s, so the gate refuses it:\n%s", fieldPageToken, card)
	}

	// The ways this tree does not wire a control, swept over the whole page. A
	// form and a submit button are the milestone's own choice (research.md R4);
	// an hx- attribute would do nothing at all here, and a handler attribute is
	// refused by the policy the daemon sends rather than by review.
	for _, offer := range scriptedMarkup {
		if strings.Contains(strings.ToLower(page), offer) {
			t.Errorf("the fleet page wires a control with %q; this door's actions are plain form posts:\n%s", offer, page)
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
			f.templates = broken
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

	// The action row on this page as well as on the fleet. It is one component,
	// so a session an operator opened is a session they can act on — a card that
	// offered its control on one page and not the other would be the two-cards
	// defect docs/components.md exists to prevent, wearing a permission's clothes.
	if !strings.Contains(card, `class="card-actions"`) {
		t.Errorf("the card on the session page carries no action row, and the fleet's does:\n%s", card)
	}
	for _, offer := range scriptedMarkup {
		if strings.Contains(strings.ToLower(page), offer) {
			t.Errorf("the session page wires a control with %q; this door's actions are plain form posts:\n%s", offer, page)
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

// TestTheSessionPageAnswersNotFoundOnceTheHostSaysTheSessionIsGone is issue #21:
// a session whose tmux session died out of band — a host reboot, a tmux server
// restart, an operator's own kill-session — kept a live card on the fleet, and
// its page answered with the note written for a host that could not be asked
// *just now*. The daemon had never asked.
//
// It asks now, and the answer is the whole difference. A session the host says
// is not there loses its record, so this page owes what an id that never existed
// is owed: the uniform 404, with the card and the pane note both gone, and the
// fleet stops drawing it on the very next load rather than at the reaper's
// convenience an hour later.
func TestTheSessionPageAnswersNotFoundOnceTheHostSaysTheSessionIsGone(t *testing.T) {
	t.Parallel()

	f := newFleet(t)
	live, _ := f.fixture.plant(t, session.Session{Name: "died on its own", WorkDir: f.fixture.repo})
	// No kill and no destroy: the session goes the way one whose host restarted
	// goes, leaving the daemon holding a record for a window nobody killed.
	f.fixture.tmux.Vanish(live.TmuxName())

	w := f.open(t, "/sessions/"+live.ID+"/view")
	if w.Code != http.StatusNotFound {
		t.Fatalf("GET the page of a session the host no longer has = %d; want %d", w.Code, http.StatusNotFound)
	}

	page := w.Body.String()
	if !strings.Contains(page, notFoundTitle) {
		t.Errorf("the page is not the uniform not-found page:\n%s", page)
	}
	// The pane note is the copy this issue is about: "just now" and "nothing was
	// changed by asking" are true of a host that hiccupped and false of a session
	// that is over, and it must not be what a permanent condition renders.
	for _, withheld := range []string{live.Name, "could not be read"} {
		if strings.Contains(page, withheld) {
			t.Errorf("the page still carries %q, describing a session this daemon no longer has:\n%s", withheld, page)
		}
	}

	// The record and the token hash it carried are gone, which is FR-020's
	// ending reached on FR-019's evidence.
	if _, err := f.fixture.store.Get(live.ID, auth.CallerOperator); !errors.Is(err, session.ErrSessionNotFound) {
		t.Errorf("the record outlived the session the host confirmed gone: Get() = _, %v, want %v", err, session.ErrSessionNotFound)
	}
	if fleet := f.view(t).Body.String(); strings.Contains(fleet, live.ID) {
		t.Errorf("the fleet still renders a card for a session that is not on the host:\n%s", fleet)
	}

	// Which of the "no such session" cases it really was is the operator's, in
	// the trail, and never the caller's (FR-042, SC-016).
	if trail := f.sink.String(); !strings.Contains(trail, session.ErrSessionDead.Error()) {
		t.Errorf("the trail does not record that the session was already gone:\n%s", trail)
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
	// The one byte string on these two pages that differs for a reason having
	// nothing to do with the fleet. A page token is minted per render from a key
	// drawn at startup, so two independently seeded daemons disagree about it the
	// way two processes disagree about any secret — and since T010 an empty fleet
	// carries one too, in the create form, where before this only a card did.
	//
	// Aligned rather than filtered out of the comparison, so the assertion stays
	// byte for byte. With one key, one identity and one clock reading, the two
	// mints are the same string, and a token that ever encoded something about
	// what the host holds would still make these pages differ.
	empty.pageKey = held.pageKey

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

// The three shapes a rendered value can take that decide whether it travels
// anywhere: the field a form submits, the URL a browser follows, and the
// attribute a script reads. Matching the attribute rather than searching the page
// for a substring is the point — the claim is not "the token appears once" but
// "every place that would carry it onwards is free of it".
//
// The field pattern is built from fieldPageToken, the constant the gate reads a
// submitted token out of, so a template that renamed the field it renders is
// caught here rather than at the first action that silently stops verifying.
var (
	hiddenTokenField = regexp.MustCompile(`<input type="hidden" name="` + fieldPageToken + `" value="([^"]*)">`)
	linkedURL        = regexp.MustCompile(`(?:href|src|action)="([^"]*)"`)
	dataAttribute    = regexp.MustCompile(`data-[a-zA-Z0-9-]+="([^"]*)"`)
)

// TestPageTokenNotInURLsOrLogs is T004's whole claim: a page carries the token
// its own action forms will submit, in a hidden field and in nothing else.
//
// **Must fail when** the token is placed in a link — the URL sweep reads every
// href, src and action on the page — or when it reaches a record, which the sweep
// of the render's own trail reads. It fails just as directly when the field is not
// rendered at all, and when the token is minted against anything other than the
// identity layer 1 verified: a page whose token does not verify for its own viewer
// is a page whose every action is refused with nothing to say why.
//
// The needle is the MAC rather than the whole token. The expiry is a timestamp and
// discloses nothing; the MAC is the half a forger needs, and searching for it
// catches a leak that carried the secret without its punctuation.
func TestPageTokenNotInURLsOrLogs(t *testing.T) {
	t.Parallel()

	// Quoted from contracts/actions.md rather than read from the constant. A test
	// that compared fieldPageToken with itself would go on passing through an edit
	// to the contract's own word, and this is the name every form on every page
	// and every submission the gate reads have to agree on.
	if fieldPageToken != "crsw_page_token" { //nolint:gosec // G101 false positive, as on the constant itself in browser.go: this is the field's *name*, which every rendered page carries in plain sight. The value it names is minted per render and appears in no source file.
		t.Fatalf("the token field is named %q; contracts/actions.md fixes it as %q", fieldPageToken, "crsw_page_token")
	}

	cases := []struct {
		page string
		open func(t *testing.T, f *fleet, id string) *httptest.ResponseRecorder
	}{
		{
			page: "the fleet",
			open: func(t *testing.T, f *fleet, _ string) *httptest.ResponseRecorder { return f.view(t) },
		},
		{
			// The other page that renders a card, and therefore the other page
			// whose card will carry action forms.
			page: "the single-session page",
			open: func(t *testing.T, f *fleet, id string) *httptest.ResponseRecorder { return f.viewOf(t, id) },
		},
	}

	for _, tc := range cases {
		t.Run(tc.page, func(t *testing.T) {
			t.Parallel()

			f := newFleet(t)
			live, _ := f.fixture.plant(t, session.Session{Name: "refactor the reaper", WorkDir: f.fixture.repo})

			w := tc.open(t, f, live.ID)
			page := w.Body.String()

			fields := hiddenTokenField.FindAllStringSubmatch(page, -1)
			if len(fields) == 0 {
				t.Fatalf("%s renders no hidden %s field, so nothing it offers could ever be submitted:\n%s",
					tc.page, fieldPageToken, page)
			}
			token := fields[0][1]
			for _, field := range fields[1:] {
				if field[1] != token {
					t.Errorf("%s rendered two different tokens (%q and %q); one page render mints one token, and every form on it carries that one",
						tc.page, token, field[1])
				}
			}

			// FR-007 at the minting end. What the gate recomputes on a submission
			// is this identity's MAC over this expiry, so a token bound to
			// anything else — a claim off the request, a constant, nobody — is a
			// page that cannot act. The clock is the fixture's own, the one the
			// mint measured the expiry on.
			if err := f.pageKey.verify(token, testOperatorEmail, testTime); err != nil {
				t.Errorf("%s rendered a token that does not verify for the identity the page was rendered for: %v",
					tc.page, err)
			}

			mac := token[strings.LastIndex(token, pageTokenSeparator)+1:]
			needles := []struct{ what, value string }{
				{"the page token", token},
				{"the MAC the page token is made of", mac},
			}

			urls := linkedURL.FindAllStringSubmatch(page, -1)
			if len(urls) == 0 {
				t.Fatalf("%s renders no href, src or action at all, so their freedom from the token asserts nothing:\n%s",
					tc.page, page)
			}
			for _, u := range urls {
				for _, needle := range needles {
					if strings.Contains(u[1], needle.value) {
						t.Errorf("%s carries %s in a URL (%s); a token in a link is a token in a referrer header, a browser history and a proxy log",
							tc.page, needle.what, u[0])
					}
				}
			}

			for _, attr := range dataAttribute.FindAllStringSubmatch(page, -1) {
				for _, needle := range needles {
					if strings.Contains(attr[1], needle.value) {
						t.Errorf("%s carries %s in a data- attribute (%s), which is a place scripts and extensions read from for no benefit here",
							tc.page, needle.what, attr[0])
					}
				}
			}

			// Every response header, which is where a Set-Cookie would be: the
			// token must never become ambient, because a credential that rides on
			// requests a hostile page triggers is the thing this one exists to
			// refuse.
			for name, values := range w.Header() {
				for _, value := range values {
					for _, needle := range needles {
						if strings.Contains(value, needle.value) {
							t.Errorf("%s carries %s in the %s response header (%q); the hidden field is the only place it belongs",
								tc.page, needle.what, name, value)
						}
					}
				}
			}

			// One record for the render, asserted before the trail is searched:
			// a page that recorded nothing would carry the token zero times for
			// entirely the wrong reason.
			if got, want := f.only(t)["decision"], string(audit.Allow); got != want {
				t.Errorf("%s was recorded as %v; want %v", tc.page, got, want)
			}
			trail := f.sink.String()
			for _, needle := range needles {
				if strings.Contains(trail, needle.value) {
					t.Errorf("%s put %s on the record; a trail holds what the daemon derived and never what it handed the browser (FR-035, AR-007):\n%s",
						tc.page, needle.what, trail)
				}
			}
		})
	}
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
