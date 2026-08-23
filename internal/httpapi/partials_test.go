// Internal test, matching render_test.go. The components are executed through
// the server's own template set rather than a fixture tree, because every claim
// in this file is about the markup a browser receives — a partial that parses in
// a test and is missing from web/ would prove nothing.
//
// There is no page to drive them through yet: T014 registers GET / on the
// browser door. Until then a component is reached the way the page will reach
// it, by name, which is also the only way to assert what a *card* renders rather
// than what a page renders around it.
package httpapi

import (
	"io/fs"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/nctiggy/claude-remote-session-webhook/internal/access"
	"github.com/nctiggy/claude-remote-session-webhook/internal/auth"
	"github.com/nctiggy/claude-remote-session-webhook/internal/config"
	"github.com/nctiggy/claude-remote-session-webhook/internal/session"
	"github.com/nctiggy/claude-remote-session-webhook/web"
)

// renderComponent executes one component from the set the daemon serves from.
func renderComponent(t *testing.T, component string, data any) string {
	t.Helper()

	var out strings.Builder
	if err := newTestServer(t, loopbackListen).templates.ExecuteTemplate(&out, component, data); err != nil {
		t.Fatalf("execute the %s component: %v", component, err)
	}
	return out.String()
}

// ownedCard is a session with everything the daemon can know about it: created
// through the API, so it carries both of the fields adoption cannot.
//
// It carries no page token, which is deliberate and is what several of the
// assertions below rest on: a card handed nothing to authorise an action with
// offers none.
func ownedCard() sessionView {
	return sessionView{
		ID:           "3f6c1d8e4b2a0957e1c3d5f7a9b1c3d5",
		Name:         "refactor the reaper",
		WorkDir:      "/home/operator/code/crswd",
		DisplayState: session.DisplayRunning,
		Age:          "2 hours",
		// The deadline, already formatted here as the age is: what turns a record
		// into this string is cardOf's job and TestCardShowsItsDeadline's subject.
		AbsoluteDeadline: "in 22 hours",
	}
}

// testCardToken is a page token's shape and deliberately not a plausible value.
// The card is a template: it verifies nothing, so what it needs is a non-empty
// string. contracts/actions.md writes its own placeholders as `<...>` for the
// reason this one is spelled in words — a file full of high-entropy strings
// trains scanners and readers alike to skip past them.
const testCardToken = "expiry.not-a-real-mac" //nolint:gosec // G101 false positive, as on fieldPageToken in browser.go: the value is a placeholder chosen so that it cannot be mistaken for a credential, which is the opposite of the thing being reported.

// actionableCard is ownedCard as a page really renders it: carrying the token
// its action forms submit, which is the whole of what makes the card offer them.
func actionableCard() sessionView {
	card := ownedCard()
	card.PageToken = testCardToken
	return card
}

// createForm is the create form as a page really renders it. Its whole parameter
// list is the token, so this is the only thing that separates a form from
// nothing at all.
func createForm() createFormView {
	return createFormView{PageToken: testCardToken}
}

// mutationMarkup is every way a rendered component could offer to change
// something on this host: the element docs/components.md's Button renders, the
// htmx attributes it renders with, the form that would submit without either,
// and the handler attribute the policy already forbids.
var mutationMarkup = []string{"<button", "hx-post", "hx-put", "hx-patch", "hx-delete", "<form", "onclick"}

// scriptedMarkup is the subset of the above that stays forbidden now that the
// card really does carry a control (US1).
//
// A form and a submit button are what this milestone chose (research.md R4), so
// they are no longer evidence of anything wrong. An hx- attribute and a handler
// still are: this tree loads exactly one script, it draws rain and reads panes,
// and the policy the daemon sends has no unsafe-inline — so either of these is
// markup that either does nothing at all or is refused by the browser rather
// than by review.
var scriptedMarkup = []string{"hx-post", "hx-put", "hx-patch", "hx-delete", "onclick"}

// TestAComponentHandedNothingToActWithOffersNoAction is the discipline milestone
// 2 held for the whole dashboard, kept for the case it still applies to.
//
// A card offers its controls from the page token those controls submit
// (view.go), so a card built without one has nothing it could authorise and
// renders nothing that could be submitted. The create form is the same rule at
// the one component whose entire parameter list is that token: with none it is
// not a form with an empty field, it is nothing at all.
//
// The empty state is the third, and it stays here now that the page around it
// can act. docs/components.md documents this component with a "Start a session"
// action and T010 deliberately did not fill it: the empty state is the one
// surface where the rain runs at full strength, and docs/design-system.md keeps
// rain off reading content — "not a pane, a card grid, a form, or a table". The
// create form is a sibling of this section on the page, never a parameter of it.
//
// The positive direction — a component handed a token renders the control — is
// the tests below. Both directions are needed: this one alone is satisfied by a
// component that renders no control at all.
func TestAComponentHandedNothingToActWithOffersNoAction(t *testing.T) {
	t.Parallel()

	rendered := map[string]string{
		"a card with no page token":        renderComponent(t, "session-card", ownedCard()),
		"a create form with no page token": renderComponent(t, "create-form", createFormView{}),
		"the empty state":                  renderComponent(t, "empty", emptyView{Title: "No sessions running", Body: "Nothing is executing on this host right now."}),
	}

	for name, markup := range rendered {
		lowered := strings.ToLower(markup)
		for _, offer := range mutationMarkup {
			if strings.Contains(lowered, offer) {
				t.Errorf("%s rendered %q; a control with nothing to authorise it is one the gate is certain to refuse:\n%s", name, offer, markup)
			}
		}
	}
}

// TestTheActionRowIsAnAbsentParameterAndNotDeletedMarkup is FR-024a in both
// directions, and the second half is the half that matters.
//
// Asserting only that no action renders is satisfied by a component that has no
// action row at all — which is precisely the outcome FR-024a forbids, because
// milestone 3 would then be restoring markup rather than passing a parameter.
// Supplying a row and watching it appear is what tells those two apart.
//
// The card's parameter is now the page token rather than a placeholder slice:
// this milestone filled the row, and what a filled row needs is the value its
// forms submit. The claim the test makes is unchanged — the row is a parameter
// that can be absent, and it appears when one is supplied.
func TestTheActionRowIsAnAbsentParameterAndNotDeletedMarkup(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		component     string
		without, with any
		row           string
	}{
		"the session card": {
			component: "session-card",
			without:   ownedCard(),
			with:      actionableCard(),
			row:       "card-actions",
		},
		"the empty state": {
			component: "empty",
			without:   emptyView{Title: "No sessions running", Body: "Nothing is executing on this host right now."},
			with:      emptyView{Title: "No sessions running", Body: "Nothing is executing on this host right now.", Action: &actionView{}},
			row:       "empty-action",
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if got := renderComponent(t, tc.component, tc.without); strings.Contains(got, tc.row) {
				t.Errorf("%s rendered its action row from no parameter:\n%s", name, got)
			}
			if got := renderComponent(t, tc.component, tc.with); !strings.Contains(got, tc.row) {
				t.Errorf("%s given an action rendered no row:\n%s\nFR-024a asks for a parameter that is simply absent here, not for markup milestone 3 has to restore", name, got)
			}
		})
	}
}

// TestTheCardRendersCallerSuppliedTextAsText is FR-030 and FR-028 at the one
// place a caller's own bytes reach the page in this milestone: a session's name
// and its working directory, both chosen by whoever created it.
//
// Each row asserts three things, because two of them alone are passed by a bug:
// the raw bytes never appear (every payload here carries a character that must
// be escaped), no element they could have opened is in the output, and the
// harmless part of the payload is still visible — rendered as text rather than
// dropped, which is what "closed by construction" means as opposed to
// sanitising: the operator sees what the session is really called.
//
// Note what is deliberately not asserted: that the escaped attribute value holds
// no handler-looking text. `title="&#34; onmouseover=&#34;…"` is inert because
// the quote is escaped, and a test that refused those bytes would be asserting a
// sanitiser this project does not have and does not want.
func TestTheCardRendersCallerSuppliedTextAsText(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		hostile string
		visible string
	}{
		"a script element":       {hostile: `<script>alert(1)</script>`, visible: "alert(1)"},
		"an image error handler": {hostile: `<img src=x onerror=alert(1)>`, visible: "src=x"},
		"an attribute breakout":  {hostile: `" onmouseover="alert(1)`, visible: "alert(1)"},
		"an unclosed tag":        {hostile: `<div class="totally-fine`, visible: "totally-fine"},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			view := ownedCard()
			view.Name, view.WorkDir = tc.hostile, tc.hostile
			got := renderComponent(t, "session-card", view)

			if strings.Contains(got, tc.hostile) {
				t.Errorf("the card rendered %q verbatim:\n%s", tc.hostile, got)
			}
			// Elements the card never renders itself, so their presence can only
			// have come from the payload. Deliberately not <div>: the action row
			// milestone 3 fills is one, and a check that fails then would be
			// failing for a reason that has nothing to do with escaping.
			for _, opened := range []string{"<script", "<img", "<svg", "<iframe"} {
				if strings.Contains(strings.ToLower(got), opened) {
					t.Errorf("the card rendered %q, which %q opened:\n%s", opened, tc.hostile, got)
				}
			}
			if !strings.Contains(got, tc.visible) {
				t.Errorf("the card dropped %q instead of showing it as text; an operator would not see what the session is really called:\n%s", tc.visible, got)
			}
		})
	}
}

// TestTheCardStatesWhatTheDaemonDoesNotKnow is FR-018a. A session adopted after
// a restart carries neither a name nor a working directory, because milestone 1
// records neither — so this is what the fleet looks like after every restart,
// not an edge case.
//
// The assertion is structural rather than a reading of the copy: the unknown
// slots are marked, they are not empty, and the value slots are absent. A card
// that filled either with an invented placeholder would be telling an operator
// something false about an unsandboxed shell.
func TestTheCardStatesWhatTheDaemonDoesNotKnow(t *testing.T) {
	t.Parallel()

	adopted := ownedCard()
	adopted.Name, adopted.WorkDir = "", ""
	got := renderComponent(t, "session-card", adopted)

	if n := strings.Count(got, `class="card-unknown"`); n != 2 {
		t.Errorf("an adopted session rendered %d unknown values; want 2 — the name and the working directory:\n%s", n, got)
	}
	if strings.Contains(got, `class="card-unknown"></span>`) {
		t.Errorf("an unknown value rendered as an empty element rather than as a statement that it is unknown:\n%s", got)
	}
	for _, value := range []string{`class="card-name"`, `class="card-path"`} {
		if strings.Contains(got, value) {
			t.Errorf("an adopted session rendered %s, which is the slot a real value goes in:\n%s", value, got)
		}
	}

	if full := renderComponent(t, "session-card", ownedCard()); strings.Contains(full, "card-unknown") {
		t.Errorf("a session with both values rendered an unknown anyway:\n%s", full)
	}
}

// The four shapes the assertions below read out of a rendered card: any link,
// the heading that has to hold it, the paragraph the identifier is rendered into
// once it is no longer the link itself, and the link's reference to it.
var (
	cardAnchor      = regexp.MustCompile(`(?s)<a\b([^>]*)>(.*?)</a>`)
	cardHeading     = regexp.MustCompile(`(?s)<h2[^>]*\bclass="card-heading"[^>]*>(.*?)</h2>`)
	cardIdentifier  = regexp.MustCompile(`(?s)<p[^>]*\bclass="card-id"[^>]*>(.*?)</p>`)
	cardDescribedBy = regexp.MustCompile(`aria-describedby="([^"]*)"`)
)

// describedBy is the text of the element an aria-describedby names. Built per
// call because the id it matches is the session's own, which is the whole point:
// a description that resolved to some other card's element would read correctly
// and describe the wrong unsandboxed shell.
func describedBy(t *testing.T, markup, id string) (string, bool) {
	t.Helper()

	element := regexp.MustCompile(`<[a-z][a-z0-9]*[^>]*\bid="` + regexp.QuoteMeta(id) + `"[^>]*>([^<]*)<`)
	match := element.FindStringSubmatch(markup)
	if match == nil {
		return "", false
	}
	return strings.TrimSpace(match[1]), true
}

// TestAnchorCoversReadableBlock is issue #16 as issue #60 leaves it, and SC-010.
//
// The accessibility floor was met before either: the identifier was a real <a>,
// focus-ringed, in tab order, and every card was reachable. What was wrong was
// the affordance. First the only link was the 32-character hex, so a mouse aimed
// at the obvious target hit nothing and a keyboard landed on the least
// human-readable string on the card (#16). Then the link was the name and
// nothing else — a few words of target on a card that reads as clickable end to
// end, which is the same defect with a better label (#60).
//
// So the anchor is the whole readable half: the name, the pill, the identifier,
// the start command and the meta list, everything above the rule. One link
// still, and not two — a card that wrapped the block and went on linking the
// name inside it would put two identical destinations next to each other in
// every link list, which reads worse than the arrangement being fixed.
//
// The identifier is inside the link now and still rendered as text, which is
// what the last assertion is about: it is the only handle a session with no name
// has, and a card that lost it while gaining a bigger target would have traded
// one #16 for another.
//
// **Must fail when** the anchor wraps the name alone (FR-046, SC-010).
func TestAnchorCoversReadableBlock(t *testing.T) {
	t.Parallel()

	card := actionableCard()
	card.StartCommand = "claude-remote"
	card.Mode = session.ModeRemote
	got := renderComponent(t, "session-card", card)

	anchors := cardAnchor.FindAllStringSubmatch(got, -1)
	if len(anchors) != 1 {
		t.Fatalf("the card renders %d links; one session is one destination:\n%s", len(anchors), got)
	}
	attributes, text := anchors[0][1], anchors[0][2]

	if target := "/sessions/" + card.ID + "/view"; !strings.Contains(attributes, `href="`+target+`"`) {
		t.Errorf("the card's link does not open %s:\n%s", target, got)
	}

	// Everything the card says about what this session *is*, inside the one
	// anchor. The pill is here for the same reason the rest is: it is text, not a
	// control, and a state an operator can read but not aim at is this issue's
	// bigger target with a hole in it.
	for what, want := range map[string]string{
		"the name":              card.Name,
		"the state pill":        string(card.DisplayState),
		"the identifier":        card.ID,
		"the start command":     card.StartCommand,
		"the mode":              string(card.Mode),
		"the working directory": card.WorkDir,
		"the age":               card.Age,
	} {
		if !strings.Contains(text, want) {
			t.Errorf("the card's link does not cover %s (%q); the anchor is the readable half of the card and not a phrase inside it:\n%s", what, want, got)
		}
	}

	heading := cardHeading.FindStringSubmatch(got)
	if heading == nil {
		t.Fatalf("the card renders no heading:\n%s", got)
	}
	if !strings.Contains(text, heading[0]) {
		t.Errorf("the card's heading is outside the link, so the name an operator aims at is inert again:\n%s", got)
	}

	// Rendered as text, and as the only handle a session with no name has.
	identifier := cardIdentifier.FindStringSubmatch(got)
	if identifier == nil || strings.TrimSpace(identifier[1]) != card.ID {
		t.Errorf("the card does not render the identifier as text; a session with no name would have no handle left:\n%s", got)
	}
}

// TestTheLinkOnACardWithNoNameIsStillToldApartFromEveryOther is the half of the
// change above that loses silently.
//
// A session adopted after a restart has no name (FR-018a), so its heading — and
// therefore its link — reads "no name recorded". That is the whole fleet after
// every restart, and a link list of identical sentences pointing at different
// unsandboxed shells is a worse card than the one that linked the hex. The
// identifier the card already renders is what tells them apart, and it says so
// in markup rather than by being adjacent.
func TestTheLinkOnACardWithNoNameIsStillToldApartFromEveryOther(t *testing.T) {
	t.Parallel()

	adopted := ownedCard()
	adopted.Name = ""
	got := renderComponent(t, "session-card", adopted)

	anchors := cardAnchor.FindAllStringSubmatch(got, -1)
	if len(anchors) != 1 {
		t.Fatalf("a card with no name renders %d links; an adopted session is reachable like any other:\n%s", len(anchors), got)
	}

	describes := cardDescribedBy.FindStringSubmatch(anchors[0][1])
	if describes == nil {
		t.Fatalf("the link reads %q and carries nothing else; every adopted card in the fleet would announce that same sentence:\n%s", strings.TrimSpace(anchors[0][2]), got)
	}
	description, ok := describedBy(t, got, describes[1])
	if !ok {
		t.Fatalf("the link is described by %q and the card renders no element with that id:\n%s", describes[1], got)
	}
	if description != adopted.ID {
		t.Errorf("the link's description reads %q; the identifier is the only thing that separates two adopted cards:\n%s", description, got)
	}
}

// cardForm is one action form on a card, split into its attributes and its
// contents.
var cardForm = regexp.MustCompile(`(?s)<form\b([^>]*)>(.*?)</form>`)

// formPostingTo picks the one form on a card that submits to an address, and
// returns its attributes and its contents.
//
// The card carries more than one action now, so a test that read forms[0] would
// be asserting against whichever control happens to be first in the markup — and
// would go on passing, silently describing the wrong route, the moment the row is
// reordered. The address is the only thing that identifies a form here, because it
// is the only thing the daemon serves.
func formPostingTo(forms [][]string, target string) (attributes, contents string, ok bool) {
	for _, form := range forms {
		if strings.Contains(form[1], `action="`+target+`"`) {
			return form[1], form[2], true
		}
	}
	return "", "", false
}

// TestCardHasExactlyOneAnchor is FR-046 — FR-027 before this milestone restated
// it — on the card that finally has a control to put somewhere.
//
// One link per card is the recurring regression here, and it has gone wrong in
// both directions: the identifier was the only link and the name was inert
// (#16), then the name was linked and the block around it was not (#60). Each
// fix is one <a> away from adding a second, and two links to one destination is
// a link list that reads worse than either arrangement it replaced.
//
// The count is asserted on a card that really carries its controls, so it is
// asserted against the markup a browser is handed rather than a skeleton — and
// against the surface where a second link is likeliest, since every control
// below the rule is something a template author might reach for an <a> to do.
//
// **Must fail when** a second link is added.
func TestCardHasExactlyOneAnchor(t *testing.T) {
	t.Parallel()

	got := renderComponent(t, "session-card", actionableCard())

	if anchors := cardAnchor.FindAllStringSubmatch(got, -1); len(anchors) != 1 {
		t.Fatalf("a card carrying its action row renders %d links; the card carried exactly one before it had controls and FR-046 keeps it there:\n%s", len(anchors), got)
	}

	// The row is really there, so the count above is about a card with something
	// in it. Without this a component that dropped its controls entirely would
	// read as passing.
	if !strings.Contains(got, `class="card-actions"`) {
		t.Errorf("the card renders no action row at all, so the count above was taken on a card with nothing to put a second link in:\n%s", got)
	}
}

// TestNoControlInsideAnchor is FR-047, and it is the half of the rule above that
// the count cannot see.
//
// A destroy button nested inside the card's link leaves the count at one and is
// still the defect: a link and a submit control occupying one target, where the
// control ends an unsandboxed shell and the link merely opens a page. It is also
// invalid HTML, so what a browser does with it is a parser's guess rather than
// anything this template decided.
//
// It matters more now that the anchor is the whole readable half rather than the
// heading (#60). The old anchor was a few words and nesting a form in it would
// have been obviously wrong; this one wraps most of the card, so a control added
// to the readable half lands inside the link by default, and only the rule below
// it says where the anchor ends.
//
// <details> and <summary> are swept alongside the form controls because a
// disclosure is a control the way a button is — T027 moves the rename into one,
// and a <details> inside a link is a target that navigates when you try to open
// it.
//
// **Must fail when** any control is nested inside the anchor.
func TestNoControlInsideAnchor(t *testing.T) {
	t.Parallel()

	got := renderComponent(t, "session-card", actionableCard())

	anchors := cardAnchor.FindAllStringSubmatch(got, -1)
	if len(anchors) != 1 {
		t.Fatalf("a card carrying its action row renders %d links, so there is no one anchor to read the contents of:\n%s", len(anchors), got)
	}
	for _, control := range []string{"<form", "<button", "<input", "<details", "<summary", "<select", "<textarea"} {
		if strings.Contains(strings.ToLower(anchors[0][2]), control) {
			t.Errorf("the card's link contains %q; a control nested in the anchor is one target holding two things to do, and one of them is irreversible:\n%s", control, got)
		}
	}

	// The controls exist, outside it. Without this the sweep above is satisfied
	// by a card that renders no control anywhere.
	if !strings.Contains(got, "<button") {
		t.Errorf("the card renders no control at all, so nothing above was asserted about where one sits:\n%s", got)
	}
}

// TestTheCardsDestroyFormCarriesWhatTheRouteRequires is the linkage that loses
// silently: the markup, the route and the gate are three files that have to
// agree about one address and two field names, and when they do not, the card
// renders perfectly and every destroy is refused.
//
// The address is derived from the pattern the daemon registers rather than
// spelled again here. The field names are compared against the constants
// actions.go and browser.go read, because this template set is parsed with no
// function map — a template cannot reach a Go constant, so the second spelling
// is unavoidable and this is the only thing holding the two together.
func TestTheCardsDestroyFormCarriesWhatTheRouteRequires(t *testing.T) {
	t.Parallel()

	card := actionableCard()
	got := renderComponent(t, "session-card", card)

	forms := cardForm.FindAllStringSubmatch(got, -1)
	// Two, not three: the rename left the card for the session's own page (T027,
	// FR-049), and a count is the cheapest thing that notices it coming back — a
	// third form here is either that or a control nobody meant to ship on twenty
	// cards at once.
	if len(forms) != 2 {
		t.Fatalf("the card renders %d action forms; this milestone's card carries two, the destroy and the compact:\n%s", len(forms), got)
	}

	target := strings.Replace(strings.TrimPrefix(patternDashboardDestroy, "POST "), "{"+pathValueID+"}", card.ID, 1)
	attributes, contents, ok := formPostingTo(forms, target)
	if !ok {
		t.Fatalf("no form on the card posts to %q, which is where the daemon serves the destroy:\n%s", target, got)
	}
	// A GET on that path is an unknown route rather than a 405 (T008), so a form
	// that forgot its method would submit a query string to nothing at all — and
	// would put the token in a URL on the way.
	if !strings.Contains(strings.ToLower(attributes), `method="post"`) {
		t.Errorf("the destroy form declares no post method (<form%s>); a GET on that path is a route this daemon does not serve:\n%s", attributes, got)
	}

	token := hiddenTokenField.FindStringSubmatch(contents)
	if token == nil {
		t.Fatalf("the destroy form carries no hidden %s field, so the gate refuses every submission of it:\n%s", fieldPageToken, got)
	}
	if token[1] != card.PageToken {
		t.Errorf("the destroy form submits %q and the card was rendered with %q", token[1], card.PageToken)
	}

	// FR-029's confirming step, on the form rather than in the handler: a destroy
	// that arrives without it is answered 400 with nothing torn down, which is a
	// page of this daemon's silently failing to destroy anything.
	if want := `name="` + fieldConfirm + `" value="` + confirmYes + `"`; !strings.Contains(contents, want) {
		t.Errorf("the destroy form does not submit %s; the route refuses a destroy that never carried the confirming step:\n%s", want, got)
	}

	// The control itself is a submit button, which is what makes FR-028 a
	// property of the markup rather than work: keyboard operability and the focus
	// ring both come with the element.
	if !strings.Contains(contents, `type="submit"`) {
		t.Errorf("the destroy form holds no submit control, so nothing on the card operates it:\n%s", got)
	}
}

// TestTheRenameFormCarriesWhatTheRouteRequires is the destroy form's linkage for
// the rename (T017): the markup, the route and the handler have to agree about
// one address and two field names, and when they do not, the form renders
// perfectly and every rename is refused — by the gate if the token field moved,
// and as bad input if the name field did.
//
// It reads the session's own page rather than the card, because T027 moved the
// control there and left the route where it was (FR-049, FR-050). Every
// assertion below is the one the card's version made: moving a control is not an
// invitation to re-decide what it submits.
//
// The address and both names are derived from what the daemon registers and reads
// rather than spelled again here. This template set is parsed with no function
// map, so a template cannot reach a Go constant and the second spelling is
// unavoidable; this is the only thing holding the two together.
//
// The label is asserted because docs/components.md requires one on every input
// and a placeholder is not a label — and its `for` is asserted to name *this*
// session's field, because the identifier is what qualifies every other id in
// this tree and a label pointing elsewhere reads correctly while operating the
// wrong session.
func TestTheRenameFormCarriesWhatTheRouteRequires(t *testing.T) {
	t.Parallel()

	card := actionableCard()
	got := renderedSessionPage(t, card)

	forms := cardForm.FindAllStringSubmatch(got, -1)
	target := strings.Replace(strings.TrimPrefix(patternDashboardRename, "POST "), "{"+pathValueID+"}", card.ID, 1)
	attributes, contents, ok := formPostingTo(forms, target)
	if !ok {
		t.Fatalf("no form on the session page posts to %q, which is where the daemon serves the rename:\n%s", target, got)
	}

	// A GET on that path is an unknown route rather than a 405, so a form that
	// forgot its method would submit a query string to nothing at all — and would
	// put the token in a URL on the way.
	if !strings.Contains(strings.ToLower(attributes), `method="post"`) {
		t.Errorf("the rename form declares no post method (<form%s>); a GET on that path is a route this daemon does not serve:\n%s", attributes, got)
	}

	token := hiddenTokenField.FindStringSubmatch(contents)
	if token == nil {
		t.Fatalf("the rename form carries no hidden %s field, so the gate refuses every submission of it:\n%s", fieldPageToken, got)
	}
	if token[1] != card.PageToken {
		t.Errorf("the rename form submits %q and the card was rendered with %q", token[1], card.PageToken)
	}

	name := regexp.MustCompile(`<input\b[^>]*\bname="` + regexp.QuoteMeta(fieldName) + `"[^>]*>`).FindString(contents)
	if name == "" {
		t.Fatalf("the rename form submits no %q field, and the handler reads the new label out of one:\n%s", fieldName, got)
	}
	if !strings.Contains(contents, `type="submit"`) {
		t.Errorf("the rename form holds no submit control, so nothing on the page operates it:\n%s", got)
	}

	id, ok := attributeValue(t, name, "id")
	if !ok {
		t.Fatalf("the %q input carries no id (%s), so no label can name it", fieldName, name)
	}
	if !strings.Contains(id, card.ID) {
		t.Errorf("the %q input is called %q, which does not name this session; every id in this tree is qualified by the identifier, and an unqualified one is a label waiting to name somebody else's field", fieldName, id)
	}
	labelled := false
	for _, label := range formLabel.FindAllStringSubmatch(contents, -1) {
		if label[1] == id && strings.TrimSpace(label[2]) != "" {
			labelled = true
		}
	}
	if !labelled {
		t.Errorf("the %q input is named by no label with words in it; a placeholder is not a label (docs/components.md):\n%s", fieldName, got)
	}

	// The field is what the daemon currently holds, so the operator edits a label
	// rather than recalling one — and an adopted session, which has none, renders
	// an empty field rather than an invented name (FR-018a).
	if value, ok := attributeValue(t, name, "value"); !ok || value != card.Name {
		t.Errorf("the %q input renders value %q (present: %t); want the session's current name %q", fieldName, value, ok, card.Name)
	}
	adopted := actionableCard()
	adopted.Name = ""
	nameless := regexp.MustCompile(`<input\b[^>]*\bname="` + regexp.QuoteMeta(fieldName) + `"[^>]*>`).
		FindString(renderedSessionPage(t, adopted))
	if value, _ := attributeValue(t, nameless, "value"); value != "" {
		t.Errorf("a session with no recorded name renders the rename field holding %q; an invented label is the page telling an operator something false about an unsandboxed shell", value)
	}

	// The client hints, pinned to the daemon's own rule rather than to a second
	// spelling of it — the create form's arrangement, and for its reason: a hint
	// that disagrees refuses in a native bubble this daemon never wrote, about a
	// rule it does not have, with nothing on the page to say why.
	if limit, ok := attributeValue(t, name, "maxlength"); !ok {
		t.Errorf("the %q input sets no maxlength (%s); the daemon's ceiling is %d characters", fieldName, name, session.MaxNameLen)
	} else if limit != strconv.Itoa(session.MaxNameLen) {
		t.Errorf("the %q input stops at %s characters and the daemon accepts %d", fieldName, limit, session.MaxNameLen)
	}
	hint, ok := attributeValue(t, name, "pattern")
	if !ok {
		t.Fatalf("the %q input carries no pattern (%s); the alphabet below has nothing to compare against", fieldName, name)
	}
	// Anchored, because an HTML pattern is: the browser matches the whole value.
	alphabet, err := regexp.Compile(`^(?:` + hint + `)$`)
	if err != nil {
		t.Fatalf("the %q input's pattern %q does not compile: %v", fieldName, hint, err)
	}
	for b := 0; b < 128; b++ {
		char := string(rune(b))
		hinted, accepted := alphabet.MatchString(char), session.ValidateName(char) == nil
		if hinted != accepted {
			t.Errorf("the browser hint %q %s %q and the daemon %s it; a hint that disagrees refuses in a bubble this daemon never wrote",
				hint, map[bool]string{true: "accepts", false: "refuses"}[hinted], char,
				map[bool]string{true: "accepts", false: "refuses"}[accepted])
		}
	}
	// The route refuses an empty name, so a form that submits without one is a
	// round trip whose only outcome is the refusal the fleet renders after the
	// redirect — on a page the operator has been taken off to read it.
	if !regexp.MustCompile(`\brequired\b`).MatchString(name) {
		t.Errorf("the %q input is not required (%s), and the route refuses an empty one", fieldName, name)
	}

	// And the page offers none of this without a token to submit, which is the
	// discipline every action on the card above it already follows: a control the
	// gate is certain to refuse is worse than no control, because an operator
	// cannot tell the two apart until they use it.
	if unauthorised := renderedSessionPage(t, ownedCard()); strings.Contains(unauthorised, target) {
		t.Errorf("a session page rendered with no page token still offers the rename:\n%s", unauthorised)
	}
}

// TestTheCardsCompactFormCarriesWhatTheRouteRequires is the destroy's and the
// rename's linkage for the second control on the card (T020), and it is the
// shortest of the three because the route reads no field of its own: what is
// delivered is a constant in the manager, so all this form has to carry is the
// evidence the gate demands.
//
// That makes the negative half the load-bearing one. A compact form that grew an
// input would be the general "type into the session" surface spec.md puts out of
// scope for this milestone, arriving as markup rather than as a decision — and
// nothing on the route would refuse it, because a handler that reads no field
// cannot notice one being sent.
//
// The address is derived from the pattern the daemon registers rather than
// spelled again here. This template set is parsed with no function map, so a
// template cannot reach a Go constant and the second spelling of the token field
// is unavoidable; this is the only thing holding the two together.
func TestTheCardsCompactFormCarriesWhatTheRouteRequires(t *testing.T) {
	t.Parallel()

	card := actionableCard()
	got := renderComponent(t, "session-card", card)

	forms := cardForm.FindAllStringSubmatch(got, -1)
	target := strings.Replace(strings.TrimPrefix(patternDashboardCompact, "POST "), "{"+pathValueID+"}", card.ID, 1)
	attributes, contents, ok := formPostingTo(forms, target)
	if !ok {
		t.Fatalf("no form on the card posts to %q, which is where the daemon serves the compact:\n%s", target, got)
	}

	// A GET on that path is an unknown route rather than a 405, so a form that
	// forgot its method would submit a query string to nothing at all — and would
	// put the token in a URL on the way.
	if !strings.Contains(strings.ToLower(attributes), `method="post"`) {
		t.Errorf("the compact form declares no post method (<form%s>); a GET on that path is a route this daemon does not serve:\n%s", attributes, got)
	}

	token := hiddenTokenField.FindStringSubmatch(contents)
	if token == nil {
		t.Fatalf("the compact form carries no hidden %s field, so the gate refuses every submission of it:\n%s", fieldPageToken, got)
	}
	if token[1] != card.PageToken {
		t.Errorf("the compact form submits %q and the card was rendered with %q", token[1], card.PageToken)
	}
	if !strings.Contains(contents, `type="submit"`) {
		t.Errorf("the compact form holds no submit control, so nothing on the card operates it:\n%s", got)
	}

	// The one field it may carry is the token, and the route reads nothing else.
	// A second input here would be a caller choosing what this daemon delivers
	// into a session it runs with --dangerously-skip-permissions.
	inputs := regexp.MustCompile(`<input\b[^>]*>`).FindAllString(contents, -1)
	if len(inputs) != 1 {
		t.Errorf("the compact form carries %d inputs (%v); it carries the page token and nothing else — what is delivered is a constant, not a caller's to choose:\n%s",
			len(inputs), inputs, got)
	}
	if strings.Contains(strings.ToLower(contents), "<textarea") || strings.Contains(strings.ToLower(contents), "<select") {
		t.Errorf("the compact form offers something to choose:\n%s", got)
	}
}

// attributeValue reads one attribute out of a rendered element's attribute list.
// Built per call rather than kept as a package regexp because the name is the
// thing under test in each case, and a helper that took a compiled pattern would
// put the spelling back at the call site it is meant to check.
func attributeValue(t *testing.T, attributes, name string) (string, bool) {
	t.Helper()

	match := regexp.MustCompile(`\b` + regexp.QuoteMeta(name) + `="([^"]*)"`).FindStringSubmatch(attributes)
	if match == nil {
		return "", false
	}
	return match[1], true
}

// The two shapes the create form's assertions read out of it: an input with its
// attributes, and a label with the input it names and the words it reads.
var (
	formInput = regexp.MustCompile(`<input\b([^>]*)>`)
	formLabel = regexp.MustCompile(`(?s)<label\b[^>]*\bfor="([^"]*)"[^>]*>(.*?)</label>`)
)

// TestCreateFormCarriesToken is T010's named test, and the linkage that loses
// silently: the form renders perfectly without the field, and every create it
// submits is refused by the gate with one uniform 403 that says nothing about
// which of the five causes applied.
//
// **Must fail when** the form renders without `crsw_page_token` — either because
// the token partial was dropped, or because the field was renamed away from the
// constant browser.go reads a submission out of.
//
// The second half is the direction a component gets wrong in the other
// direction: a form built with no token at all must render nothing, rather than
// a form carrying an empty field. An empty value looks like a token to
// everything reading the markup and verifies as none, so the operator would meet
// a control that fails only when they use it.
func TestCreateFormCarriesToken(t *testing.T) {
	t.Parallel()

	got := renderComponent(t, "create-form", createForm())

	forms := cardForm.FindAllStringSubmatch(got, -1)
	if len(forms) != 1 {
		t.Fatalf("the create form component renders %d forms; it is one form:\n%s", len(forms), got)
	}
	attributes, contents := forms[0][1], forms[0][2]

	token := hiddenTokenField.FindStringSubmatch(contents)
	if token == nil {
		t.Fatalf("the create form carries no hidden %s field, so the gate refuses every submission of it:\n%s", fieldPageToken, got)
	}
	if token[1] != testCardToken {
		t.Errorf("the create form submits %q and the component was rendered with %q", token[1], testCardToken)
	}

	// In the field and in nothing else. The form's own action is the one URL it
	// renders, and a token in a URL is a token in a referrer header, a browser
	// history and a proxy log — which is why the gate reads it out of PostForm.
	if strings.Contains(attributes, testCardToken) {
		t.Errorf("the create form carries the token in <form%s>; the hidden field is the only place it belongs", attributes)
	}

	if empty := renderComponent(t, "create-form", createFormView{}); strings.TrimSpace(empty) != "" {
		t.Errorf("a create form built with no token rendered %q; a control the gate is certain to refuse is worse than no control, because an operator cannot tell the two apart until they use it", empty)
	}
}

// TestTheCreateFormPostsWhatTheRouteReads is the destroy form's linkage for the
// route that has two caller-supplied fields instead of one fixed one: the
// markup, the route and the handler are three files that have to agree about an
// address and two field names, and when they do not, the form renders perfectly
// and every create is refused for a reason that reads like bad input.
//
// The address and both names are derived from what the daemon registers and
// reads rather than spelled again here. This template set is parsed with no
// function map, so a template cannot reach a Go constant and the second spelling
// is unavoidable — this is the only thing holding the two together.
func TestTheCreateFormPostsWhatTheRouteReads(t *testing.T) {
	t.Parallel()

	got := renderComponent(t, "create-form", createForm())

	forms := cardForm.FindAllStringSubmatch(got, -1)
	if len(forms) != 1 {
		t.Fatalf("the create form component renders %d forms; it is one form:\n%s", len(forms), got)
	}
	attributes, contents := forms[0][1], forms[0][2]

	target := strings.TrimPrefix(patternDashboardCreate, "POST ")
	if !strings.Contains(attributes, `action="`+target+`"`) {
		t.Errorf("the create form posts to <form%s> and the daemon serves %q:\n%s", attributes, target, got)
	}
	// A GET on that path is an unknown route rather than a 405, so a form that
	// forgot its method would submit a query string to nothing at all — and would
	// put the token in a URL on the way.
	if !strings.Contains(strings.ToLower(attributes), `method="post"`) {
		t.Errorf("the create form declares no post method (<form%s>); a GET on that path is a route this daemon does not serve:\n%s", attributes, got)
	}

	// The control itself is a submit button, which is what makes keyboard
	// operability a property of the markup rather than work: the focus ring the
	// design system sets applies to it untouched, and the form works with no
	// script running.
	if !strings.Contains(contents, `type="submit"`) {
		t.Errorf("the create form holds no submit control, so nothing on the page operates it:\n%s", got)
	}

	// Every input the route reads, each with a real label naming it.
	// docs/components.md's Form rules: a placeholder is not a label.
	labelled := make(map[string]string)
	for _, label := range formLabel.FindAllStringSubmatch(contents, -1) {
		labelled[label[1]] = strings.TrimSpace(label[2])
	}
	for _, field := range []string{fieldName, fieldWorkDir} {
		input := regexp.MustCompile(`<input\b[^>]*\bname="` + regexp.QuoteMeta(field) + `"[^>]*>`).FindString(contents)
		if input == "" {
			t.Errorf("the create form submits no %q field, and the handler reads the create's %s out of one:\n%s", field, field, got)
			continue
		}
		id, ok := attributeValue(t, input, "id")
		if !ok {
			t.Errorf("the %q input carries no id (%s), so no label can name it", field, input)
			continue
		}
		if labelled[id] == "" {
			t.Errorf("the %q input is named by no label; a placeholder is not a label (docs/components.md):\n%s", field, got)
		}
	}
}

// TestTheCreateFormsHintsAgreeWithTheDaemonsRules is the half of a convenience
// that stops being convenient the moment it drifts.
//
// docs/components.md permits client hints and makes server-side validation
// authoritative, which is exactly the arrangement here: the route calls
// ValidateName and answers a refusal in words. But a hint is not neutral when it
// disagrees — a browser refusing a name this daemon would have accepted shows a
// native bubble the daemon never wrote, about a rule it does not have, with
// nothing on the page to say why.
//
// The alphabet is compared against ValidateName itself rather than against a
// second spelling of the rule, so widening the daemon's own character class is
// what fails this rather than someone's memory of it.
func TestTheCreateFormsHintsAgreeWithTheDaemonsRules(t *testing.T) {
	t.Parallel()

	got := renderComponent(t, "create-form", createForm())

	name := regexp.MustCompile(`<input\b[^>]*\bname="` + regexp.QuoteMeta(fieldName) + `"[^>]*>`).FindString(got)
	if name == "" {
		t.Fatalf("the create form renders no %q input at all:\n%s", fieldName, got)
	}

	if limit, ok := attributeValue(t, name, "maxlength"); !ok {
		t.Errorf("the %q input sets no maxlength (%s); the daemon's ceiling is %d characters", fieldName, name, session.MaxNameLen)
	} else if limit != strconv.Itoa(session.MaxNameLen) {
		t.Errorf("the %q input stops at %s characters and the daemon accepts %d", fieldName, limit, session.MaxNameLen)
	}

	hint, ok := attributeValue(t, name, "pattern")
	if !ok {
		t.Fatalf("the %q input carries no pattern (%s); the alphabet below has nothing to compare against", fieldName, name)
	}
	// Anchored, because an HTML pattern is: the browser matches the whole value.
	alphabet, err := regexp.Compile(`^(?:` + hint + `)$`)
	if err != nil {
		t.Fatalf("the %q input's pattern %q does not compile: %v", fieldName, hint, err)
	}
	for b := 0; b < 128; b++ {
		char := string(rune(b))
		hinted, accepted := alphabet.MatchString(char), session.ValidateName(char) == nil
		if hinted != accepted {
			t.Errorf("the browser hint %q %s %q and the daemon %s it; a hint that disagrees refuses in a bubble this daemon never wrote",
				hint, map[bool]string{true: "accepts", false: "refuses"}[hinted], char,
				map[bool]string{true: "accepts", false: "refuses"}[accepted])
		}
	}

	// Both fields are required by the route — an empty name is ErrInvalidName and
	// an empty working directory is ErrInvalidWorkDir — so a form that submits
	// without them is a round trip whose only outcome is a refusal.
	for _, field := range []string{fieldName, fieldWorkDir} {
		input := regexp.MustCompile(`<input\b[^>]*\bname="` + regexp.QuoteMeta(field) + `"[^>]*>`).FindString(got)
		if !regexp.MustCompile(`\brequired\b`).MatchString(input) {
			t.Errorf("the %q input is not required (%s), and the route refuses an empty one", field, input)
		}
	}

	// And no hint whatever on the working directory. The approved roots are this
	// daemon's configuration; a pattern spelling them would put a map of the host
	// in markup that every browser, extension and proxy on the path can read.
	workDir := regexp.MustCompile(`<input\b[^>]*\bname="` + regexp.QuoteMeta(fieldWorkDir) + `"[^>]*>`).FindString(got)
	if _, ok := attributeValue(t, workDir, "pattern"); ok {
		t.Errorf("the %q input carries a pattern (%s); the approved roots are configuration, and a hint describing them is a map of this host in the markup", fieldWorkDir, workDir)
	}

	// Non-vacuity: the sweeps above are about inputs that really rendered.
	if n := len(formInput.FindAllString(got, -1)); n < 3 {
		t.Errorf("the create form renders %d inputs; want the two fields and the hidden token:\n%s", n, got)
	}
}

// TestTheCreateFormSaysWhenItIsInFlight is FR-031's in-progress state, and it is
// the pane's ended note in the other direction: there, a stream that stopped had
// to say so; here, a control that has been spent has to say why.
//
// The failure mode is the same one. A button that greys out silently reads as a
// page that has broken, and an operator who cannot tell a submission in flight
// from one that went nowhere clicks again — which is exactly the second
// unsandboxed shell this whole arrangement exists to prevent (research.md R7).
//
// The copy is in the template rather than in crswd.js for the reason every other
// sentence the interface says is: a script that authored its own prose would be
// a second place to look for it.
func TestTheCreateFormSaysWhenItIsInFlight(t *testing.T) {
	t.Parallel()

	got := renderComponent(t, "create-form", createForm())

	forms := cardForm.FindAllStringSubmatch(got, -1)
	if len(forms) != 1 {
		t.Fatalf("the create form component renders %d forms; it is one form:\n%s", len(forms), got)
	}
	hook, ok := attributeValue(t, forms[0][1], "data-submit-once")
	if !ok {
		t.Fatalf("the create form carries no data-submit-once hook (<form%s>), so nothing spends its submit and a double-click is two sessions:\n%s", forms[0][1], got)
	}

	note := regexp.MustCompile(`<p[^>]*id="` + regexp.QuoteMeta(hook) + `"[^>]*>([^<]*)</p>`).FindStringSubmatch(got)
	if note == nil {
		t.Fatalf("the create form points the script at %q and the render holds no such element:\n%s", hook, got)
	}
	if !strings.Contains(note[0], "hidden") {
		t.Errorf("the in-progress note renders visible (%q); a page that says it before a submission says it about nothing", note[0])
	}
	if strings.TrimSpace(note[1]) == "" {
		t.Error("the in-progress note carries no copy at all, so revealing it would say nothing")
	}
}

// TestTheStatusPillAlwaysCarriesItsLabelAsText is FR-019 and the design system's
// fifth non-negotiable: colour is reinforcement, never the only signal. Both
// states this milestone derives are green, so colour alone does not distinguish
// them even for a reader who can see it.
//
// The needs-auth row is not speculative. docs/design-system.md keeps tokens for
// needs-auth and dead and requires the status component to keep working for
// them; deriving the class from the value rather than spelling it here is what
// makes that true without an edit, and this row is what notices if someone
// replaces the derivation with a two-armed conditional.
func TestTheStatusPillAlwaysCarriesItsLabelAsText(t *testing.T) {
	t.Parallel()

	for _, state := range []session.DisplayState{
		session.DisplayRunning,
		// The second state the daemon really derives (spec 012): a session the
		// supervisor could not bring back. It reached the pill without an edit to
		// the component, which is the claim the comment above makes.
		session.DisplayFailed,
		// Two states the daemon does not derive. needs-auth arrives with the
		// device-code relay; idle was derived until milestone 15 and its token
		// stays in the design system. Both are here for the same reason: the
		// class is derived from the value, and a two-armed conditional would pass
		// every other assertion in this file.
		"idle",
		"needs-auth",
	} {
		t.Run(string(state), func(t *testing.T) {
			t.Parallel()

			got := renderComponent(t, "status-pill", state)
			if !strings.Contains(got, ">"+string(state)+"<") {
				t.Errorf("the %s pill carries no text label: %s", state, got)
			}
			if !strings.Contains(got, "pill-"+string(state)) {
				t.Errorf("the %s pill's class is not derived from its state: %s", state, got)
			}
		})
	}
}

// TestTheHeaderShowsTheIdentityLayerOneVerified is FR-020 and FR-036. The
// component executes against the VerifiedOperator, so the address it renders
// cannot have come from the request — there is nothing else in scope to render.
func TestTheHeaderShowsTheIdentityLayerOneVerified(t *testing.T) {
	t.Parallel()

	const address = "operator@example.com"
	got := renderComponent(t, "header", &access.VerifiedOperator{Email: address})

	if !strings.Contains(got, address) {
		t.Errorf("the header does not show the verified operator:\n%s", got)
	}
	if !strings.Contains(got, `class="rain"`) {
		t.Errorf("the header carries no rain canvas; the design system permits one behind it:\n%s", got)
	}

	// The edge wrote this address inside a signature, so escaping it is defence
	// in depth rather than the control — but it is the control for everything
	// else on the page, and a template with one exception is one someone copies.
	hostile := renderComponent(t, "header", &access.VerifiedOperator{Email: `<script>alert(1)</script>`})
	if strings.Contains(strings.ToLower(hostile), "<script") {
		t.Errorf("the header rendered an address as markup:\n%s", hostile)
	}
}

// mastheadElement is the header as a whole page renders it. The assertion below
// reads the anchor inside it and not the ones on the cards beside it: the card's
// exactly-one-link rule (FR-027, #16) is about the card, and a test that counted
// links per page would make the header and that rule contradict each other.
var mastheadElement = regexp.MustCompile(`(?s)<header class="masthead">(.*?)</header>`)

// TestTheHeaderIsTheRouteBackToTheFleet is issue #48.
//
// A session page reached from a card offered no way home: the wordmark was text,
// nothing else on the page pointed at the fleet, and the browser's back button
// was the whole of the navigation. Both pages are asserted, and the fleet is not
// the redundant half of that pair — it is the point. A link that is present on
// one page and absent on another is an affordance an operator has to learn the
// shape of; pointing at the page already open costs nothing next to that.
//
// The link's text is asserted too. A house glyph alone would satisfy an href and
// fail the reason FR-030 gives for never signalling by symbol alone.
func TestTheHeaderIsTheRouteBackToTheFleet(t *testing.T) {
	t.Parallel()

	card := actionableCard()
	pages := map[string]string{
		"the fleet page": renderedFleet(t),
		"the single-session page": renderComponent(t, "session", sessionPageView{
			Operator: &access.VerifiedOperator{Email: "operator@example.com"},
			Session:  card,
			Pane:     paneView{ID: card.ID, Text: "$ go test ./..."},
		}),
	}

	for name, page := range pages {
		masthead := mastheadElement.FindStringSubmatch(page)
		if masthead == nil {
			t.Fatalf("%s renders no masthead, so this asserted nothing about its header:\n%s", name, page)
		}

		var home []string
		for _, anchor := range cardAnchor.FindAllStringSubmatch(masthead[1], -1) {
			if strings.Contains(anchor[1], `href="/"`) {
				home = anchor
			}
		}
		if home == nil {
			t.Errorf("%s renders no header link to the fleet; the browser's back button is not a route home:\n%s", name, masthead[0])
			continue
		}
		if !strings.Contains(home[2], "crswd") {
			t.Errorf("%s renders the route home as %q; a mark with no word is a symbol on its own:\n%s", name, strings.TrimSpace(home[2]), masthead[0])
		}
	}
}

// TestTheRainCarriesNoInformationAndStaysOffReadingContent holds the design
// system's two rules for the effect: it is hidden from assistive technology
// because it says nothing, and it appears behind the header and the empty state
// and nowhere else. A card grid is reading content, and content sitting on the
// rain means the rain is in the wrong place.
func TestTheRainCarriesNoInformationAndStaysOffReadingContent(t *testing.T) {
	t.Parallel()

	canvas := renderComponent(t, "rain", nil)
	for _, want := range []string{"<canvas", `class="rain"`, `aria-hidden="true"`} {
		if !strings.Contains(canvas, want) {
			t.Errorf("the rain canvas is missing %s: %s", want, canvas)
		}
	}

	permitted := map[string]string{
		"the header":      renderComponent(t, "header", &access.VerifiedOperator{Email: "operator@example.com"}),
		"the empty state": renderComponent(t, "empty", emptyView{Title: "No sessions running", Body: "Nothing is executing on this host right now."}),
	}
	for name, markup := range permitted {
		if !strings.Contains(markup, `class="rain"`) {
			t.Errorf("%s renders no rain canvas, and the design system puts one there:\n%s", name, markup)
		}
	}
	forbidden := map[string]string{
		"the session card": renderComponent(t, "session-card", ownedCard()),
		// The clearest case of reading content there is: a screen an operator is
		// reading, in the one component whose whole content came from the host.
		"the pane viewer": renderComponent(t, "pane", paneView{ID: ownedCard().ID, Text: "$ go test ./..."}),
	}
	for name, markup := range forbidden {
		if strings.Contains(markup, `class="rain"`) {
			t.Errorf("%s renders rain behind reading content:\n%s", name, markup)
		}
	}
}

// renderedFleet is the fleet page as a browser receives it.
//
// It is a page rather than a component, and it has to be: the three hooks the
// live half reads belong to the composition — which stream to open, which
// address one card is at, and which note says the fleet has stopped being kept
// current — so a component executed on its own could not see any of them. It
// goes through the server's own template set all the same, which is what every
// assertion in this file stands on.
func renderedFleet(t *testing.T) string {
	t.Helper()

	return renderComponent(t, "dashboard", fleetView{
		Operator: &access.VerifiedOperator{Email: "operator@example.com"},
		Summary:  []stateCount{{State: session.DisplayRunning, Count: 1}},
		Sessions: []sessionView{actionableCard()},
		Empty:    emptyView{Title: "No sessions running", Body: "Nothing is executing on this host right now."},
		Create:   createForm(),
	})
}

// renderedSessionPage is one session's own page as a browser receives it, given
// the card that page is about.
//
// It takes the card because that is the parameter every assertion here varies:
// a card carrying a token and one carrying none are the two pages the rename has
// to behave differently on, and an adopted session's blank name is the third.
// The operator and the pane are fixtures, and neither is asserted through this.
func renderedSessionPage(t *testing.T, card sessionView) string {
	t.Helper()

	return renderComponent(t, "session", sessionPageView{
		Operator: &access.VerifiedOperator{Email: "operator@example.com"},
		Session:  card,
		Pane:     paneView{ID: card.ID, Text: "$ go test ./..."},
	})
}

// disclosure is a <details> element with its attributes and everything it holds.
var disclosure = regexp.MustCompile(`(?s)<details\b([^>]*)>(.*?)</details>`)

// TestRenameAbsentFromFleet is FR-049, and it is the half of T027 that is an
// absence — which is the half that comes back by accident.
//
// A rename on a card is a text entry on every card in the fleet: twenty fields
// between an operator and the thing a dashboard is scanned for, spent on the one
// action of the four that changes nothing on the host. The card is where it was
// and where a hand would put it back, so the claim is made against the page and
// against the component both. The component matters on its own because the
// fleet's live half re-fetches a *card* from its own route as sessions change —
// a rename restored there would reach a browser without this page ever being
// rendered again.
//
// The second half is the vacuity guard. "No form posts to the rename" is
// satisfied by a fleet that renders no controls at all, which is a much worse
// bug wearing this test's green, so the two controls that stayed are asserted
// present in the same pass.
//
// **Must fail when** rename returns to the card (FR-049).
func TestRenameAbsentFromFleet(t *testing.T) {
	t.Parallel()

	card := actionableCard()
	rename := strings.Replace(strings.TrimPrefix(patternDashboardRename, "POST "), "{"+pathValueID+"}", card.ID, 1)

	fleetless := map[string]string{
		"the fleet page":     renderedFleet(t),
		"the card component": renderComponent(t, "session-card", card),
		// The third is the one that would not be caught by reading either of the
		// two above. The fleet's live half re-fetches a session's *page* and lifts
		// the <article> out of it (crswd.js), so a rename rendered inside the card
		// on that page is a rename the fleet grows on its first state change —
		// correct on load, wrong the moment anything happens, which is the worst
		// shape a bug has. It is outside the card here, which is what makes that
		// impossible rather than stripped afterwards.
		"the card the fleet lifts from the session page": cardFor(t, renderedSessionPage(t, card), card.ID),
	}
	for name, markup := range fleetless {
		if _, _, found := formPostingTo(cardForm.FindAllStringSubmatch(markup, -1), rename); found {
			t.Errorf("%s renders a form posting to %q; the rename is on the session's own page and on no card (FR-049):\n%s", name, rename, markup)
		}
	}

	// The controls that stayed, asserted on the same three surfaces, so none of
	// the absences above can be satisfied by markup that offers nothing at all —
	// an empty string lifted out of a page included.
	for what, pattern := range map[string]string{
		"destroy": patternDashboardDestroy,
		"compact": patternDashboardCompact,
	} {
		target := strings.Replace(strings.TrimPrefix(pattern, "POST "), "{"+pathValueID+"}", card.ID, 1)
		for name, markup := range fleetless {
			if _, _, ok := formPostingTo(cardForm.FindAllStringSubmatch(markup, -1), target); !ok {
				t.Errorf("%s offers no %s either (%q); this test would then be passing on markup with no controls in it at all:\n%s", name, what, target, markup)
			}
		}
	}
}

// TestRenameOnSessionPageIsDisclosure is FR-050, and it is two claims: the
// control is on the session's own page, and it is revealed on request rather
// than resident.
//
// The second is what a <details> with no `open` attribute means, and it is
// asserted structurally rather than by class, because "closed until asked for"
// is a property of the element and not of a stylesheet — a page that styled a
// resident field to look collapsed would satisfy any assertion about appearance
// and none about what a screen reader is handed.
//
// The summary's words are asserted for the same reason the label's are: a
// disclosure whose control announces nothing is a control a non-sighted operator
// cannot find, and this is the only route to the field behind it.
//
// **Must fail when** it becomes a resident field (FR-050) — the form outside a
// disclosure, or a disclosure rendered open.
func TestRenameOnSessionPageIsDisclosure(t *testing.T) {
	t.Parallel()

	card := actionableCard()
	page := renderedSessionPage(t, card)
	rename := strings.Replace(strings.TrimPrefix(patternDashboardRename, "POST "), "{"+pathValueID+"}", card.ID, 1)

	if _, _, ok := formPostingTo(cardForm.FindAllStringSubmatch(page, -1), rename); !ok {
		t.Fatalf("the session page posts nothing to %q, so the rename is on no page at all (FR-050):\n%s", rename, page)
	}

	var attributes, contents string
	for _, details := range disclosure.FindAllStringSubmatch(page, -1) {
		if strings.Contains(details[2], `action="`+rename+`"`) {
			attributes, contents = details[1], details[2]
		}
	}
	if contents == "" {
		t.Fatalf("the rename form sits outside every <details> on the page; a field that is always there is not revealed on request (FR-050):\n%s", page)
	}

	// `open` is the whole of the difference between a disclosure and a field with
	// a heading over it, and it is one word away in either direction.
	if regexp.MustCompile(`(?i)\bopen\b`).MatchString(attributes) {
		t.Errorf("the rename disclosure is rendered open (<details%s>), so it is resident markup wearing a summary (FR-050)", attributes)
	}

	summary := regexp.MustCompile(`(?s)<summary\b[^>]*>(.*?)</summary>`).FindStringSubmatch(contents)
	if summary == nil {
		t.Fatalf("the rename disclosure carries no <summary>, so the browser labels the one control that opens it:\n%s", contents)
	}
	if strings.TrimSpace(summary[1]) == "" {
		t.Errorf("the rename disclosure's summary holds no words; the control that reveals the field announces nothing")
	}

	// And nothing script-shaped opens it. The element does that itself, which is
	// why it was chosen — the page loads one script, and it draws rain and reads
	// panes.
	for _, offer := range scriptedMarkup {
		if strings.Contains(strings.ToLower(contents), offer) {
			t.Errorf("the rename disclosure is wired with %q; a disclosure that needs script is one that does not open with script disabled:\n%s", offer, contents)
		}
	}
}

// TestStreamLossIsVisible is FR-020, and it is the pane's ended note made about
// a whole fleet: a page whose updates have stopped must say so rather than going
// on presenting a fleet it can no longer vouch for.
//
// Silence is the failure, and it is worse here than at a pane. A pane that stops
// updating looks like a session sitting quietly at a prompt; a *fleet* that stops
// updating looks like a host where nothing is happening — and this page now
// offers a control on every card, so a stale card is an operator destroying a
// session that has already gone or reading a fleet that has grown since.
//
// The ending arrives as no event at all. contracts/fleet-stream.md names three
// and not one of them is a farewell, so a severed stream, a restarted daemon and
// a subscriber this daemon dropped for falling behind are one thing to a browser:
// a response that ended. That is why the page needs a sentence prepared for it in
// advance, rather than something the daemon could send.
//
// **Must fail when** the page carries no disconnected state — the hook is gone,
// the note it names is missing, the note renders visible, or it says nothing. The
// other half of the claim is in stylesheet_test.go, where the script that reveals
// this note is: markup nothing reveals is as silent as no markup.
func TestStreamLossIsVisible(t *testing.T) {
	t.Parallel()

	page := renderedFleet(t)

	hook := regexp.MustCompile(`data-fleet-stalled="([^"]*)"`).FindStringSubmatch(page)
	if hook == nil {
		t.Fatalf("the fleet page names no note for a stream that stopped, so a page that has missed changes goes on presenting the fleet as current:\n%s", page)
	}

	note := regexp.MustCompile(`<p[^>]*id="` + regexp.QuoteMeta(hook[1]) + `"[^>]*>([^<]*)</p>`).FindStringSubmatch(page)
	if note == nil {
		t.Fatalf("the fleet page points the live half at %q and renders no such element:\n%s", hook[1], page)
	}
	if !strings.Contains(note[0], "hidden") {
		t.Errorf("the note renders visible (%q); a page that says its updates have stopped before they have says it about a stream that is working", note[0])
	}
	if strings.TrimSpace(note[1]) == "" {
		t.Error("the note carries no copy at all, so revealing it would say nothing")
	}
	// The page cannot recover what it missed while the stream was gone — the
	// reconnection brings no history with it — so the note has to name the one
	// thing that does. A sentence that only announced the loss would leave an
	// operator looking at a fleet they have been told not to trust with nothing
	// to do about it.
	if !strings.Contains(strings.ToLower(note[1]), "reload") {
		t.Errorf("the note reads %q and names nothing the operator can do about it; only a fresh render restores a fleet this page can vouch for", strings.TrimSpace(note[1]))
	}
}

// TestTheFleetSaysWhenItsShapeChanged is what the reload used to do by accident
// (issue #51).
//
// A page that reloads re-announces itself to a screen reader because it is a new
// page. A page that rearranges in place announces nothing at all, which is worse
// for an operator who cannot see it: cards they were told about are gone, cards
// they were not told about are there, and the interface said neither. So the two
// events that change what is on the page get one sentence each, in a live region
// the render composes.
//
// The copy is here and not in the script for the reason every other sentence
// this interface says is: a script that authored its own prose would be a second
// place to look for it. `{n}` is the substitution the live half already makes on
// the card's address, filled from the cards themselves rather than from a number
// this page could go stale about.
//
// **Must fail when** the region is missing, hidden, pre-filled, or carries no
// sentence for one of the two events — each of which is a fleet that rearranges
// silently for the operator least able to notice.
func TestTheFleetSaysWhenItsShapeChanged(t *testing.T) {
	t.Parallel()

	page := renderedFleet(t)

	hook := regexp.MustCompile(`data-fleet-changed="([^"]*)"`).FindStringSubmatch(page)
	if hook == nil {
		t.Fatalf("the fleet page names no region for a change to its shape, so a session appearing or vanishing is announced to nobody:\n%s", page)
	}

	note := regexp.MustCompile(`<p[^>]*id="` + regexp.QuoteMeta(hook[1]) + `"[^>]*>([^<]*)</p>`).FindStringSubmatch(page)
	if note == nil {
		t.Fatalf("the fleet page points the live half at %q and renders no such element:\n%s", hook[1], page)
	}
	// Present and empty rather than hidden: a live region has to be in the
	// accessibility tree before its text arrives for the announcement to happen,
	// and one revealed and written in the same breath is one some readers never
	// announce. Empty, the stylesheet's `:empty` rule takes it out of the layout.
	if strings.Contains(note[0], "hidden") {
		t.Errorf("the region renders hidden (%q); text arriving in a region that was not in the accessibility tree is text a reader may never announce", note[0])
	}
	if strings.TrimSpace(note[1]) != "" {
		t.Errorf("the region renders with %q already in it, so a page that has just loaded announces a change that did not happen", note[1])
	}
	if !strings.Contains(note[0], `role="status"`) && !strings.Contains(note[0], `aria-live=`) {
		t.Errorf("the region is not a live region at all (%q), so writing into it announces nothing", note[0])
	}

	// One sentence per event that changes what is on the page, and the count in
	// each. Nothing is announced for the third event: a card replaced in place is
	// docs/components.md's noise, and narrating it would make the grid a live
	// region.
	for _, event := range []string{"appeared", "vanished"} {
		sentence := regexp.MustCompile(`data-fleet-` + event + `="([^"]*)"`).FindStringSubmatch(note[0])
		if sentence == nil {
			t.Errorf("the region carries no sentence for a session that %s, so that event is announced as nothing:\n%s", event, note[0])
			continue
		}
		if !strings.Contains(sentence[1], "{n}") {
			t.Errorf("the sentence for %s reads %q and names no count; a live region that says the same words twice is a change some readers do not announce a second time", event, sentence[1])
		}
	}
}

// TestTheFleetNamesTheStreamAndTheCardItRefetches is the linkage that loses
// silently, and it loses three ways at once: the page, the routes and the script
// have to agree about two addresses and one identifier, and when they do not the
// fleet renders perfectly and simply never changes.
//
// Both addresses are derived from the patterns the daemon registers rather than
// spelled again here. A renamed route would otherwise leave every test in this
// package passing and every open dashboard silently stale — which is issue #15
// again, reintroduced by a rename nobody connected to it.
//
// The identifier is the third: the stream says only which session changed
// (contracts/fleet-stream.md), so a card that does not carry its own identifier
// in markup is a card the live half cannot find, replace, or remove.
func TestTheFleetNamesTheStreamAndTheCardItRefetches(t *testing.T) {
	t.Parallel()

	page := renderedFleet(t)

	stream := regexp.MustCompile(`data-fleet-stream="([^"]*)"`).FindStringSubmatch(page)
	if stream == nil {
		t.Fatalf("the fleet page names no stream, so nothing subscribes and the page is as static as it was in milestone 2:\n%s", page)
	}
	if want := strings.TrimPrefix(patternFleetStream, "GET "); stream[1] != want {
		t.Errorf("the fleet page subscribes to %q and the daemon serves %q", stream[1], want)
	}

	// The card's address with the daemon's own route parameter still in it. The
	// live half substitutes the identifier the stream named, so this is compared
	// against the registered pattern verbatim rather than against a rendering of
	// it — the two spellings are then one string.
	card := regexp.MustCompile(`data-fleet-card="([^"]*)"`).FindStringSubmatch(page)
	if card == nil {
		t.Fatalf("the fleet page names no address for a card, so an event naming a session leads nowhere:\n%s", page)
	}
	if want := strings.TrimPrefix(patternSessionView, "GET "); card[1] != want {
		t.Errorf("the fleet page re-fetches a card from %q and the daemon serves %q", card[1], want)
	}
	// FR-034 on this address for the reason the pane's stream hook carries it: a
	// credential in a URL is a credential in a referrer header, a browser history
	// and a proxy log, and what is on the other end of this one is an unsandboxed
	// shell.
	if strings.ContainsAny(card[1], "?#") {
		t.Errorf("the card's address carries more than the path to the session (%q)", card[1])
	}

	// And the identifier on the card itself, at the component as well as on the
	// page: the fleet's live half finds a card by it, and the create route's
	// fragment is the same component rendered on its own.
	view := actionableCard()
	for what, markup := range map[string]string{"the fleet page": page, "the card component": renderComponent(t, "session-card", view)} {
		if !strings.Contains(markup, `data-session="`+view.ID+`"`) {
			t.Errorf("%s renders no data-session on the card, so an event naming that session matches nothing on the page:\n%s", what, markup)
		}
	}

	// The single-session page carries no fleet hook, and that is load-bearing
	// rather than tidy. It renders one card and no grid, so a live half attached
	// to it would meet every change with the reload the fleet page's own shape
	// changes call for — on a page whose shape can never be what that reload is
	// for. A session that ends says so beside the pane there, on the stream that
	// page already opens.
	if session := renderComponent(t, "session", sessionPageView{
		Operator: &access.VerifiedOperator{Email: "operator@example.com"},
		Session:  view,
		Pane:     paneView{ID: view.ID, Text: "$ go test ./..."},
	}); strings.Contains(session, "data-fleet-stream") {
		t.Errorf("the single-session page subscribes to the fleet stream; it has no grid for a card to arrive in:\n%s", session)
	}
}

// forbiddenInTemplates is the testable half of Principle VII, plus the two
// absences the policy in docs/security.md depends on.
//
// A hard-coded colour, size or font is a value that stopped being a token, and
// the design system's first non-negotiable is that a value not in that document
// does not exist. An inline style or an event-handler attribute is refused at
// runtime by the policy the daemon sends — this is the second, independent
// enforcement contracts/dashboard.md asks for, because a proxy stripping a header
// must not be the only thing between a template and execution. An external origin
// is FR-025.
//
// The script element is not swept here but in a test of its own below. The policy
// forbids an inline one and permits a file from 'self', and those are different
// strings in the same element: a pattern that refused both would forbid the one
// way the rain and the stream client can reach a page at all.
var forbiddenInTemplates = []struct {
	what    string
	pattern *regexp.Regexp
}{
	{"a hard-coded colour", regexp.MustCompile(`#[0-9a-fA-F]{3}\b`)},
	{"a hard-coded size", regexp.MustCompile(`\d+(px|rem|em|pt|vh|vw)\b`)},
	{"a hard-coded font", regexp.MustCompile(`(?i)font-family`)},
	{"an inline style", regexp.MustCompile(`(?i)style\s*=`)},
	{"an inline event handler", regexp.MustCompile(`(?i)\son[a-z]+\s*=\s*["']`)},
	{"an external origin", regexp.MustCompile(`(?i)https?:|//cdn|srcset`)},
}

// templateComment is what the sweep below reads past. A {{/* … */}} comment is
// dropped before a byte of the template is rendered, so it is not markup and
// cannot be an origin, a style, or a script — and every partial in this tree
// explains the rule it follows by naming the thing it must not contain.
var templateComment = regexp.MustCompile(`(?s)\{\{/\*.*?\*/\}\}`)

// TestNoTemplateCarriesAValueThatBelongsInATokenOrAnOrigin sweeps the whole
// embedded tree rather than the components this task added, so it keeps holding
// for the page T014 writes and the pane T026 writes without either having to
// remember it.
func TestNoTemplateCarriesAValueThatBelongsInATokenOrAnOrigin(t *testing.T) {
	t.Parallel()

	files := 0
	err := fs.WalkDir(web.Templates, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		files++
		source, err := fs.ReadFile(web.Templates, p)
		if err != nil {
			return err
		}
		markup := templateComment.ReplaceAll(source, nil)
		for _, forbidden := range forbiddenInTemplates {
			if match := forbidden.pattern.Find(markup); match != nil {
				t.Errorf("web/%s carries %s (%q)", p, forbidden.what, match)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk the embedded template tree: %v", err)
	}
	if files == 0 {
		t.Fatal("the embedded template tree is empty, so this sweep asserted nothing")
	}
}

// The three shapes a script can take in this tree: the whole element, the
// opening tag on its own — counted so an unclosed one cannot hide a body from the
// pair match — and the one spelling a page is allowed to load.
var (
	scriptElement = regexp.MustCompile(`(?is)<script([^>]*)>(.*?)</script>`)
	scriptOpener  = regexp.MustCompile(`(?i)<script`)
	scriptSource  = regexp.MustCompile(`^src="/static/([^"]+)"\s+defer$`)
)

// TestEveryScriptATemplateLoadsIsAnEmbeddedAssetAndNeverAnInlineOne is the half
// of the policy that forbidding the element outright would have overshot.
//
// docs/security.md's CSP is sent with `script-src 'self'` and no
// `unsafe-inline`, which is two rules and not one: a script *body* in a template
// would be refused by the browser, and a script *file* from this origin is how
// the rain and, later, the stream client reach a page at all. So the body must be
// empty, the reference must name a file web/static really embeds, and it must
// defer — a page whose script ran before the canvases were parsed would find
// nothing to draw into.
func TestEveryScriptATemplateLoadsIsAnEmbeddedAssetAndNeverAnInlineOne(t *testing.T) {
	t.Parallel()

	loaded := 0
	err := fs.WalkDir(web.Templates, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		source, err := fs.ReadFile(web.Templates, p)
		if err != nil {
			return err
		}
		markup := string(templateComment.ReplaceAll(source, nil))

		elements := scriptElement.FindAllStringSubmatch(markup, -1)
		if opened := len(scriptOpener.FindAllString(markup, -1)); opened != len(elements) {
			t.Errorf("web/%s opens %d script elements and closes %d; an unclosed one swallows whatever follows it", p, opened, len(elements))
		}

		for _, element := range elements {
			loaded++
			if body := strings.TrimSpace(element[2]); body != "" {
				t.Errorf("web/%s carries an inline script (%q); the policy this daemon sends has no unsafe-inline, so it would be refused by the browser rather than by review", p, body)
			}
			ref := scriptSource.FindStringSubmatch(strings.TrimSpace(element[1]))
			if ref == nil {
				t.Errorf(`web/%s loads a script as <script%s>; the one permitted spelling is src="/static/<file>.js" defer`, p, element[1])
				continue
			}
			if _, err := fs.Stat(web.Static, path.Join(staticRoot, ref[1])); err != nil {
				t.Errorf("web/%s loads %q and web/static embeds no such file; the symptom is a page that renders and does nothing", p, ref[1])
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk the embedded template tree: %v", err)
	}
	if loaded == 0 {
		t.Fatal("no template loads a script at all, so this sweep asserted nothing — and the rain has no loop")
	}
}

// TestEveryPageLoadsTheLoopThatDrivesItsRain is the linkage in the direction a
// test can lose silently.
//
// Every page that composes the header renders a rain canvas with it
// (docs/components.md). A canvas nothing draws into is an empty rectangle: the
// markup is right, the stylesheet is right, every assertion in this package
// passes, and the effect the design system calls the product's signature is
// simply absent in a browser. This is the only place that shows up.
//
// It reads the rendered pages rather than the template sources, and asks each
// one whether it drew a canvas at all. That is the derived form of a claim this
// test used to make by assuming — "every page carries a header" — which stopped
// being true when the sign-in page arrived with nobody to name in one
// (pagesWithNobodyToName). Deriving it is strictly stronger than listing an
// exception here: a page that renders a canvas of its own without composing the
// header is now caught too, and a page that quietly stops rendering one is
// TestEveryPageCarriesTheHeader's to catch, where the exception is argued once.
func TestEveryPageLoadsTheLoopThatDrivesItsRain(t *testing.T) {
	t.Parallel()

	drawn := 0
	for name, page := range renderedPages(t) {
		if !strings.Contains(page, "<canvas") {
			continue
		}
		drawn++
		if !strings.Contains(page, `src="/static/crswd.js"`) {
			t.Errorf("the %s page renders a rain canvas and loads no script; the canvas is an empty rectangle in a browser and nothing here would say so", name)
		}
	}
	if drawn == 0 {
		t.Fatal("no page renders a canvas at all, so this sweep asserted nothing — and the rain has nowhere to fall")
	}
}

// TestThePaneNamesTheStreamItsLiveHalfReads is the linkage above in the pane's
// direction, and it loses just as silently: the markup, the route and the script
// are three files that have to agree about one address, and when they do not the
// pane renders perfectly and simply never updates.
//
// The address is derived from the pattern the server registers rather than
// spelled again here. A renamed route would otherwise leave every test in this
// package passing and every open pane pointed at a 404 — which EventSource then
// reconnects to forever, turning the dashboard into a polite scanner of its own
// daemon.
func TestThePaneNamesTheStreamItsLiveHalfReads(t *testing.T) {
	t.Parallel()

	card := ownedCard()
	pane := renderComponent(t, "pane", paneView{ID: card.ID, Text: "$ go test ./..."})

	hook := regexp.MustCompile(`data-stream="([^"]*)"`).FindStringSubmatch(pane)
	if hook == nil {
		t.Fatalf("the pane renders no data-stream hook, so nothing tells the live half which stream to read:\n%s", pane)
	}
	if want := strings.Replace(strings.TrimPrefix(patternSessionStream, "GET "), "{"+pathValueID+"}", card.ID, 1); hook[1] != want {
		t.Errorf("the pane streams from %q and the daemon serves %q", hook[1], want)
	}

	// FR-034: the path to the session and nothing else. A credential here would
	// be the key to an unsandboxed shell, in an address the edge, the tunnel and
	// the browser's own history each write down.
	if strings.ContainsAny(hook[1], "?#") {
		t.Errorf("the pane's stream address carries more than the path to the session (%q)", hook[1])
	}

	// A screen nobody could read carries no hook, because the element the live
	// half attaches to is precisely the one that case does not render. Pinned
	// rather than left to be noticed: a pane that streamed into the note would be
	// a page arguing with itself, so this is where that has to be decided again
	// rather than acquired by accident.
	if unread := renderComponent(t, "pane", paneView{ID: card.ID, Unread: true}); strings.Contains(unread, "data-stream") {
		t.Errorf("a pane saying the screen could not be read opens a stream anyway:\n%s", unread)
	}
}

// TestThePaneSaysWhenTheWatchedSessionEnded is FR-033 in the markup: a stream
// that stops must say why.
//
// Silence is the failure. A pane whose updates simply cease looks exactly like a
// session that has gone quiet — which is the ordinary state of a session waiting
// at a prompt — so an operator would go on watching a screen that is never
// coming back, and would believe the session was still there.
//
// The copy is in the template rather than in crswd.js for the reason every other
// sentence on this page is: what the interface says to a person is the
// template's, and a script that authored its own prose would be a second place
// to look for it.
func TestThePaneSaysWhenTheWatchedSessionEnded(t *testing.T) {
	t.Parallel()

	card := ownedCard()
	pane := renderComponent(t, "pane", paneView{ID: card.ID, Text: "$ go test ./..."})

	hook := regexp.MustCompile(`data-ended="([^"]*)"`).FindStringSubmatch(pane)
	if hook == nil {
		t.Fatalf("the pane names no note for the end of the session, so a stream that stops says nothing:\n%s", pane)
	}

	note := regexp.MustCompile(`<p[^>]*id="` + regexp.QuoteMeta(hook[1]) + `"[^>]*>([^<]*)</p>`).FindStringSubmatch(pane)
	if note == nil {
		t.Fatalf("the pane points the live half at %q and the render holds no such element:\n%s", hook[1], pane)
	}
	if !strings.Contains(note[0], "hidden") {
		t.Errorf("the note renders visible (%q); a page that says so before it happens says it about a session that is running", note[0])
	}
	if strings.TrimSpace(note[1]) == "" {
		t.Error("the note carries no copy at all, so revealing it would say nothing")
	}

	// A pane that opens no stream needs no ending: nothing attaches to it, so
	// nothing could ever reveal the note. Held for the reason the hook's own
	// absence is — the two have to be the same decision.
	if unread := renderComponent(t, "pane", paneView{ID: card.ID, Unread: true}); strings.Contains(unread, "data-ended") {
		t.Errorf("a pane that opens no stream carries an end note anyway:\n%s", unread)
	}
}

// components is every component a page composes: docs/components.md's canonical
// inventory, plus the create form.
//
// The create form is in this list and not in that document, which is a gap that
// document owes rather than a second card: T010 put the form in partials/
// because a create is not a card's to offer, and docs/components.md has no Form
// partial for it to be — the same absence its Button and Modal entries already
// have. Keeping it here is what stops the next hand from inlining a second one
// into a page while the document catches up.
var components = []string{"header", "status-pill", "session-card", "create-form", "empty", "rain", "pane"}

// TestEveryCanonicalComponentIsAPartial is FR-024 held by the shape of the tree:
// pages compose components, components live in one place, and a second card is a
// defect visible by inspection. It is a test rather than a convention because
// the cheapest way to add a second card is to inline one in a page.
func TestEveryCanonicalComponentIsAPartial(t *testing.T) {
	t.Parallel()

	set := newTestServer(t, loopbackListen).templates
	for _, component := range components {
		if set.Lookup(component) == nil {
			t.Errorf("the template set defines no %q component", component)
		}
	}

	err := fs.WalkDir(web.Templates, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		if !slices.Contains(components, strings.TrimSuffix(path.Base(p), templateExt)) {
			return nil
		}
		if dir := path.Dir(p); dir != "templates/partials" {
			t.Errorf("the %s component lives in %s; docs/components.md puts every one of them in templates/partials", path.Base(p), dir)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk the embedded template tree: %v", err)
	}
}

// TestCreateFormHasNoStartCommandSelect is the milestone-4 miss read at the one
// layer that could have caught it. FR-026 said an operator chooses remote control
// as a *mode* rather than selecting a command by name; three tasks shipped green
// for it and this form kept its dropdown of names, because every assertion made
// was about a route or a record.
//
// So this reads markup, and it asserts an absence rather than a presence: a
// switch being there says nothing about the chooser still being there beside it.
//
// **Must fail when** the route accepts the right value but the form still renders
// the old control. That is the exact shape of what shipped.
//
// It is asserted twice, and the second time is the one with teeth. The chooser
// was conditional on the operator having configured more than one command, so a
// component rendered from a bare view never drew it and a test reading only that
// would pass with the `<select>` still in the template — the same near miss one
// layer down. The page case configures two commands, which is the state the
// control existed for.
//
// The field name is spelled here rather than taken from actions.go's constant on
// purpose. What must be gone is the control an operator sees, and that stays true
// whatever the handler goes on to read — a test bound to the constant would start
// passing for the wrong reason the moment the route stopped reading it.
func TestCreateFormHasNoStartCommandSelect(t *testing.T) {
	t.Parallel()

	// Not `<option`. The datalist beside the working-directory field renders
	// those legitimately, and it is about to render more of them (T006) — the
	// element the chooser is recognised by is the `<select>` around them.
	gone := []string{"<select", `name="start_command"`, `id="create-start-command"`}

	t.Run("the component", func(t *testing.T) {
		t.Parallel()

		out := renderComponent(t, "create-form", createForm())
		for _, chooser := range gone {
			if strings.Contains(out, chooser) {
				t.Errorf("the create form still renders %s, so it still asks an operator to pick a command by name:\n%s", chooser, out)
			}
		}
	})

	t.Run("a daemon with commands configured", func(t *testing.T) {
		t.Parallel()

		f := newFleet(t)
		f.cfg.StartCommands = config.NewStartCommands(map[string]string{
			"default": "claude --dangerously-skip-permissions",
			"rc":      "claude remote-control",
		})

		create := sectionOf(t, f.view(t).Body.String(), "create")
		for _, chooser := range gone {
			if strings.Contains(create, chooser) {
				t.Errorf("a daemon configuring two commands still renders %s on its create form, which is choosing a command by name:\n%s", chooser, create)
			}
		}
	})
}

// TestCreateFormRendersRemoteSwitch is the other half: what stands where the
// chooser stood is the two-state control research.md settled on, and it is the
// platform's own.
//
// Exactly one, and bound to a label. A second checkbox by the same name would
// post two values for one mode, and an unlabelled one is a control a screen
// reader announces as nothing — docs/components.md's Form rule, which is why the
// label is asserted through `for` rather than by proximity in the markup.
//
// **Must fail when** the control is a button, a link, an unlabelled input, or a
// checkbox posting anything but the one value the route will accept.
func TestCreateFormRendersRemoteSwitch(t *testing.T) {
	t.Parallel()

	out := renderComponent(t, "create-form", createForm())

	// By the name rather than by the type, which is what this counted until the
	// form grew its second switch (T002, the idle override). The count was always
	// a proxy for the thing the sentence below says — one two-state control per
	// mode — and a second checkbox posting a *different* field is not the defect
	// it was written about: what would be is two boxes both posting
	// remote_control, which is one mode answered twice in a single submission.
	// Counting every checkbox on the form made the create form's vocabulary
	// unable to grow without this test failing for a reason it does not hold.
	//
	// The literal, as this test has always spelled it rather than reaching for
	// the constant: what is being pinned is the name that goes over the wire, and
	// a rename that edited the constant and the template together would pass a
	// test written against the constant while every browser posted something the
	// deployed daemon does not read.
	var boxes []string
	for _, input := range formInput.FindAllStringSubmatch(out, -1) {
		kind, _ := attributeValue(t, input[1], "type")
		name, _ := attributeValue(t, input[1], "name")
		if kind == "checkbox" && name == "remote_control" {
			boxes = append(boxes, input[1])
		}
	}
	if len(boxes) != 1 {
		t.Fatalf("the create form renders %d remote_control checkboxes; the mode is one two-state control, and the route reads it out of that name:\n%s", len(boxes), out)
	}
	box := boxes[0]
	// An unticked checkbox posts nothing, so this one value is the whole of what
	// the ticked state says. A box with no value posts "on" by every browser's
	// convention, which is the same string — but by their convention rather than
	// by this daemon's, and the route spells what it accepts.
	if value, ok := attributeValue(t, box, "value"); !ok || value != "on" {
		t.Errorf("the switch submits %q and the route accepts on (<input%s>)", value, box)
	}
	if strings.Contains(box, "checked") {
		t.Errorf("the switch renders already on (<input%s>); absence means local, and a control defaulting to the more privileged mode makes the safe direction the deliberate one", box)
	}

	id, ok := attributeValue(t, box, "id")
	if !ok {
		t.Fatalf("the switch carries no id (<input%s>), so no label can name it", box)
	}
	var labelled bool
	for _, label := range formLabel.FindAllStringSubmatch(out, -1) {
		if label[1] != id {
			continue
		}
		labelled = true
		if strings.TrimSpace(label[2]) == "" {
			t.Errorf("the switch's label is empty; a control announced as nothing is an unlabelled control:\n%s", out)
		}
	}
	if !labelled {
		t.Errorf("no <label for=%q> names the switch; a placeholder is not a label and neither is proximity (docs/components.md):\n%s", id, out)
	}
}

// switchesSubmitting is every checkbox in a rendered form that submits one named
// field, read out of the markup a browser was handed.
//
// The field is passed as the constant the handler reads rather than as a literal,
// which is this suite's standing arrangement for a template set parsed with no
// function map: the markup spells the name a second time and a test is what holds
// the two spellings together.
func switchesSubmitting(t *testing.T, out, field string) []string {
	t.Helper()

	var boxes []string
	for _, input := range formInput.FindAllStringSubmatch(out, -1) {
		kind, _ := attributeValue(t, input[1], "type")
		name, _ := attributeValue(t, input[1], "name")
		if kind == "checkbox" && name == field {
			boxes = append(boxes, input[1])
		}
	}
	return boxes
}

// TestCreateFormLetsASessionOutliveItsAbsoluteDeadline is T005: the switch above
// asserted for the other clock, with the one difference that is the whole of this
// task — the control is offered only where the daemon's own ceiling is gone.
//
// resolveLifetimes grants a never-expiring session on exactly that condition, so
// a form rendering the box under a finite ceiling would offer a control certain
// to be refused on every submission an operator ever ticked it for. That is what
// an absent page token already keeps off a card, and the cost here is higher: a
// control that fails only when it is used teaches an operator that one of this
// daemon's switches is broken, on the one page they start unsandboxed shells from.
//
// The value goes through parseLifetimeOverrides — the route's own parser, not a
// second reading of what "never" ought to mean — and has to arrive as a negative
// lifetime, which is the record state TestACreateMayAskForNoAbsoluteDeadlineWhere
// TheOperatorAllowedIt then drives through the create route end to end. Between
// the two, what this markup posts and what the store holds are one claim.
//
// **Must fail when** the switch is dropped, renamed away from the field
// actions.go reads, posts a value that leaves the absolute deadline running,
// ships ticked, is unlabelled, loses the sentence saying what it switches off, or
// is rendered on a daemon whose ceiling still stands.
func TestCreateFormLetsASessionOutliveItsAbsoluteDeadline(t *testing.T) {
	t.Parallel()

	t.Run("offered where the operator removed the ceiling", func(t *testing.T) {
		t.Parallel()

		view := createForm()
		view.LifetimeCeilingRemoved = true
		out := renderComponent(t, "create-form", view)

		boxes := switchesSubmitting(t, out, fieldLifetime)
		if len(boxes) != 1 {
			t.Fatalf("the create form renders %d %q checkboxes; the operator's choice is one two-state control, and the handler reads the override out of that field:\n%s", len(boxes), fieldLifetime, out)
		}
		box := boxes[0]

		value, ok := attributeValue(t, box, "value")
		if !ok {
			t.Fatalf("the switch carries no value (<input%s>); a checkbox with none posts whatever its browser's convention is, and the parser reads a duration or one word", box)
		}
		lifetime, err := parseLifetimeOverride(value)
		if err != nil {
			t.Fatalf("the switch submits %s=%q and the route's own parser refuses it: %v", fieldLifetime, value, err)
		}
		if lifetime >= 0 {
			t.Errorf("the switch submits %s=%q, which the route parses to a lifetime of %s; a session whose lifetime override is not negative still ends at a deadline that is never renewed, so this control says it does something it does not (<input%s>)", fieldLifetime, value, lifetime, box)
		}

		// An unticked box posts nothing at all, which is the daemon's configured
		// default. Shipping it ticked would hand every operator who filled this
		// form in without reading it a session nothing reaps.
		if strings.Contains(box, "checked") {
			t.Errorf("the switch renders already on (<input%s>); removing the deadline that is never renewed is a choice rather than a state to arrive in", box)
		}

		id, ok := attributeValue(t, box, "id")
		if !ok {
			t.Fatalf("the switch carries no id (<input%s>), so no label can name it", box)
		}
		var labelled bool
		for _, label := range formLabel.FindAllStringSubmatch(out, -1) {
			if label[1] != id {
				continue
			}
			labelled = true
			if strings.TrimSpace(label[2]) == "" {
				t.Errorf("the switch's label is empty; a control announced as nothing is an unlabelled control:\n%s", out)
			}
		}
		if !labelled {
			t.Errorf("no <label for=%q> names the switch; a placeholder is not a label and neither is proximity (docs/components.md):\n%s", id, out)
		}

		// What it switches off, said where the operator is looking. With this box
		// and the one above it both ticked, no clock reaps the session at all —
		// which is the operator's decision to make on their own host, and exactly
		// the reason the interface has to state it rather than present it as a
		// convenience. The word rather than the sentence, so the prose stays the
		// template's to write.
		describes, ok := attributeValue(t, box, "aria-describedby")
		if !ok {
			t.Fatalf("the switch is described by nothing (<input%s>); it removes the one bound that is never renewed, and a control that does not say so understates what it does", box)
		}
		note := regexp.MustCompile(`(?s)<[^>]*\bid="` + regexp.QuoteMeta(describes) + `"[^>]*>(.*?)</div>`).FindStringSubmatch(out)
		if note == nil {
			t.Fatalf("the switch points at %q for its description and the render holds no such element:\n%s", describes, out)
		}
		if !strings.Contains(strings.ToLower(note[1]), "lifetime") {
			t.Errorf("the switch's description never names the bound it removes (%q)", strings.TrimSpace(note[1]))
		}
	})

	t.Run("withheld where the ceiling still stands", func(t *testing.T) {
		t.Parallel()

		out := renderComponent(t, "create-form", createForm())

		if boxes := switchesSubmitting(t, out, fieldLifetime); len(boxes) != 0 {
			t.Errorf("a daemon whose ceiling stands renders %d %q checkboxes; every create ticking one is refused by resolveLifetimes, and a control certain to be turned away is not offered (docs/components.md):\n%s", len(boxes), fieldLifetime, out)
		}
		// And the form still says where the remaining bound comes from. It is the
		// settings page on this daemon, because the form offers nothing that could
		// move it — the sentence and the control it names have to describe the same
		// daemon. The sentence lived on the idle switch's hint until milestone 15
		// withdrew that switch, and it was about this bound the whole time.
		if !strings.Contains(out, "settings") {
			t.Errorf("the form no longer says where the lifetime comes from; on a daemon with a ceiling that page is the only way to move it:\n%s", out)
		}
	})
}

// TestTheCreateFormOffersTheLifetimeSwitchOnlyWhereTheDaemonWouldGrantIt is the
// projection behind that branch, and it is a page test for
// TestCreateFormRendersNoCommandName's reason: what can still go wrong is not the
// template but the fact it is handed, and a view field nothing fills renders
// exactly like a daemon whose operator kept their ceiling.
//
// Both directions, because either alone passes on a form that had made its mind
// up. The second is the shipped daemon; the first is the operator who removed the
// ceiling, set through the same call server.go makes on their configuration.
//
// **Must fail when** the page stops asking the manager that will judge the create
// — a dashboard reading the ceiling for itself is the second reading of the rule
// session.Manager.LifetimeCeilingRemoved exists to prevent.
func TestTheCreateFormOffersTheLifetimeSwitchOnlyWhereTheDaemonWouldGrantIt(t *testing.T) {
	t.Parallel()

	t.Run("the operator removed the ceiling", func(t *testing.T) {
		t.Parallel()

		f := newFleet(t)
		// config.NeverLifetime is what an operator writes; a negative is what it
		// loads as, and this is the line server.go runs on it.
		f.fixture.mgr.SetLifetimes(session.AbsoluteLifetime, -time.Hour)

		create := sectionOf(t, f.view(t).Body.String(), "create")
		if len(switchesSubmitting(t, create, fieldLifetime)) != 1 {
			t.Errorf("a daemon that would grant a never-expiring session offers no switch for one; the record, the parser and the ceiling all agree and the one surface the operator uses cannot ask:\n%s", create)
		}
	})

	t.Run("the ceiling this daemon shipped with", func(t *testing.T) {
		t.Parallel()

		create := sectionOf(t, newFleet(t).view(t).Body.String(), "create")
		if n := len(switchesSubmitting(t, create, fieldLifetime)); n != 0 {
			t.Errorf("the shipped daemon offers %d lifetime switches; every create ticking one is refused, and this form would be teaching the operator that a control on it does not work:\n%s", n, create)
		}
	})
}

// TestCreateFormRendersNoCommandName is FR-002 at the only place it can be
// checked: against a daemon that really has commands configured.
//
// It is a page test rather than a component test, and it has to be. The view
// carries no command *names* at all, so a component rendered from one could not
// leak what it was never given — what can still go wrong is the projection
// putting them back, in a label, a value, a title or a data attribute, which is a
// fact about `fleet` and not about the template.
//
// The create section alone, deliberately. A card names the command its session is
// running (#38), which is a different disclosure with a different argument behind
// it: it describes a session that already exists rather than offering a choice of
// what to start.
//
// # The command line is now shown, and the name still is not
//
// This test asserted both absences until milestone 15. The preview added by that
// milestone renders the resolved command *line* on purpose (FR-014), so the
// preview element is excluded from the sweep below and its own rules are
// TestTheCreateFormPreviewIsAReadoutAndNotAChooser's.
//
// The distinction is not a loophole. A name in a control is a chooser — an
// operator picking "rc" out of a list is choosing a command by name, which is
// what FR-026 forbids and what this form used to do. A line in a `<pre>` is a
// readout of what the mode they picked will run. What crosses to the browser is
// wider than it was; what the browser can *select* is exactly what it was.
//
// **Must fail when** a configured name reaches any control on this form.
func TestCreateFormRendersNoCommandName(t *testing.T) {
	t.Parallel()

	f := newFleet(t)
	// Two names, because one would let a form that rendered "the default" pass
	// while still being a chooser, and the second is the worked example's `rc` —
	// the real configured name T004 must go on refusing as a submitted value.
	commands := map[string]string{"default": "claude --dangerously-skip-permissions", "rc": "claude remote-control"}
	f.cfg.StartCommands = config.NewStartCommands(commands)

	create := sectionOf(t, f.view(t).Body.String(), "create")
	controls := commandPreview.ReplaceAllString(create, "")

	for name := range commands {
		if n := strings.Count(controls, name); n != 0 {
			t.Errorf("the rendered create form names the configured command %q %d times outside its preview; a mode is asked for without the daemon's vocabulary for it:\n%s", name, n, controls)
		}
	}
	// Non-vacuity, in both directions: the section really did render and holds the
	// control that replaced the chooser, and the strip above really did remove a
	// preview rather than silently matching nothing.
	if !strings.Contains(controls, `name="remote_control"`) {
		t.Errorf("the rendered create form carries no remote_control switch, so the sweep above passed on markup that offers nothing:\n%s", controls)
	}
	if !commandPreview.MatchString(create) {
		t.Errorf("the create form rendered no command preview, so the exclusion above is hiding nothing and this test is weaker than it reads:\n%s", create)
	}
}

// commandPreview matches the readout block, so a sweep for command *names* can
// exclude the one element that renders a command *line* on purpose.
var commandPreview = regexp.MustCompile(`(?s)<pre class="command-preview".*?</pre>`)

// TestTheCreateFormPreviewIsAReadoutAndNotAChooser is what remains of
// TestViewCarriesNoStartCommands, and the change is worth reading.
//
// # Why the absolute guard went
//
// US1 removed the operator's configured command *names* from this view, because
// the form rendered them as a `<select name="start_command">` — an operator
// picking "rc" out of a list is choosing a command by name, which is the thing
// FR-026 said not to do. The guard here asserted no command-shaped field existed
// at all, which was the cheapest way to keep the chooser from coming back.
//
// Milestone 15 puts resolved command *lines* back on the view for the preview
// (FR-014). That is a different thing in the way that matters: what crosses to the
// browser is text to render, the switch still carries a mode, and the create still
// resolves that mode to a command server-side out of the operator's own set. So
// the rule to enforce is no longer "no commands on the view" but FR-016 — nothing
// the browser sends may select a command line except by mode.
//
// **Must fail when** the preview becomes a form field, or the form regains a
// control that names a command.
func TestTheCreateFormPreviewIsAReadoutAndNotAChooser(t *testing.T) {
	t.Parallel()

	view := createForm()
	view.Commands = map[bool]string{
		false: "claude --dangerously-skip-permissions",
		true:  `claude --dangerously-skip-permissions "/rc {name}"`,
	}
	out := renderComponent(t, "create-form", view)

	// The preview is rendered, so what follows is asserting about markup that
	// exists.
	if !strings.Contains(out, "claude --dangerously-skip-permissions") {
		t.Fatalf("the form renders no command preview, so this test asserts nothing:\n%s", out)
	}

	// Nothing that submits. A preview carrying a name attribute is a field, and a
	// field carrying a command line is the execution surface FR-016 closes.
	for _, forbidden := range []string{
		`name="start_command"`,
		`name="command"`,
		"contenteditable",
		"<textarea",
	} {
		if strings.Contains(out, forbidden) {
			t.Errorf("the create form carries %s; the preview is a readout, and the configured set stays the only source of commands:\n%s", forbidden, out)
		}
	}

	// And the control that decides which line runs is still a mode.
	if !strings.Contains(out, `name="remote_control"`) {
		t.Errorf("the create form carries no remote_control switch, so what selects the command is something else:\n%s", out)
	}
}

// TestTheCreateFormNamesTheConfiguredRoots is the other half of T014: the
// working-directory field says which directories this daemon will start a
// session under, so an operator can fill it in.
//
// **Must fail when** the hint drops a root, or renders when there is none. The
// first is the failure with consequences — an operator whose second root is
// missing reads the uniform refusal as one they have no way to explain, because
// that refusal deliberately will not tell them which rule applied.
//
// This is not the disclosure the uniform refusal prevents. That one answers
// "outside the roots", "not a directory" and "not there at all" identically so a
// caller cannot ask this form whether a path exists; naming the permitted set is
// not confirming what is inside it, and every card on the fleet already renders
// a working directory under one of them.
func TestTheCreateFormNamesTheConfiguredRoots(t *testing.T) {
	t.Parallel()

	t.Run("every configured root renders", func(t *testing.T) {
		t.Parallel()

		roots := []string{"/home/operator/code", "/srv/work"}
		out := renderComponent(t, "create-form", createFormView{PageToken: "t", Roots: roots})
		for _, root := range roots {
			if !strings.Contains(out, `<li class="field-hint-root">`+root+`</li>`) {
				t.Errorf("the hint omits the root %q:\n%s", root, out)
			}
		}
		// The field has to point at the hint, or a screen reader reaches the
		// control without the sentence that says what it will accept.
		if !strings.Contains(out, `aria-describedby="create-roots"`) || !strings.Contains(out, `id="create-roots"`) {
			t.Errorf("the working-directory field is not described by the hint:\n%s", out)
		}
	})

	// FR-040's half of the picker arrives in T022; what this pins now is that the
	// hint did not become a control. A field that only accepted what it listed
	// would be an allowlist rendered into the markup, free to drift from
	// ResolveWorkDir, and the refusal it produced would be a browser bubble this
	// daemon never wrote.
	t.Run("the field stays typeable", func(t *testing.T) {
		t.Parallel()

		out := renderComponent(t, "create-form", createFormView{PageToken: "t", Roots: []string{"/srv/work"}})
		if !strings.Contains(out, `<input class="field-input" id="create-work-dir" type="text" name="work_dir"`) {
			t.Errorf("the working directory is no longer a text field:\n%s", out)
		}
		if strings.Contains(out, `name="work_dir"`) && strings.Contains(out, "<select") &&
			!strings.Contains(out, `name="start_command"`) {
			t.Errorf("the working directory became a chooser:\n%s", out)
		}
	})

	// A daemon configured with no root cannot start — config.Load refuses it — so
	// this is the zero value's business rather than a state an operator reaches.
	// It renders no hint rather than an empty list, which is FR-018a's discipline
	// about absent values: state the absence, never render something that reads
	// like a value.
	//
	// Asserted against this hint's own id rather than against the field-hint
	// class, which stopped distinguishing when the conversation field grew a hint
	// of its own (T032). That one is unconditional and says what an empty field
	// does, so it is not the absent value this case is about.
	t.Run("no roots renders no hint", func(t *testing.T) {
		t.Parallel()

		out := renderComponent(t, "create-form", createFormView{PageToken: "t"})
		if strings.Contains(out, `id="create-roots"`) {
			t.Errorf("a form rendered without roots offers an empty hint:\n%s", out)
		}
	})
}

// The picker's fixture and the element it hangs off. Two paths, because a list
// of one cannot show that the whole list renders, and both under a plausible
// root because that is what either source produces — a configured list or
// T023's walk one level below an approved root.
var (
	workdirSuggestions = []string{"/home/operator/code/crswd", "/home/operator/code/notes"}
	workdirInput       = regexp.MustCompile(`<input\b[^>]*\bname="work_dir"[^>]*>`)
)

// TestPickerWorksWithoutScript is T022's named test and the reason the
// abandoned branch's combobox was not carried: this control is markup, and
// markup runs whether or not a script does.
//
// **Must fail when** the control becomes script-dependent (FR-043) — a field
// whose suggestions are assembled by crswd.js offers nothing at all with
// scripting off, which is the state this dashboard is required to work in.
//
// The assertion is deliberately made against the field's own `list` attribute
// rather than against the literal id: a datalist nothing points at is invisible
// to the browser and to a screen reader alike, and the two spellings drifting
// apart is exactly the failure that leaves a form looking correct in review.
func TestPickerWorksWithoutScript(t *testing.T) {
	t.Parallel()

	out := renderComponent(t, "create-form", createFormView{PageToken: "t", Suggestions: workdirSuggestions})

	input := workdirInput.FindString(out)
	if input == "" {
		t.Fatalf("the create form renders no work_dir field at all:\n%s", out)
	}
	list, ok := attributeValue(t, input, "list")
	if !ok {
		t.Fatalf("the working-directory field points at no list (%s), so the suggestions below it are markup the browser never reads:\n%s", input, out)
	}
	if !strings.Contains(out, `<datalist id="`+list+`">`) {
		t.Errorf("the field points at the datalist %q and no such element is rendered:\n%s", list, out)
	}
	for _, path := range workdirSuggestions {
		if !strings.Contains(out, `<option value="`+path+`">`) {
			t.Errorf("the datalist omits the suggestion %q:\n%s", path, out)
		}
	}

	// Nothing has to run for any of the above to be true. The form already
	// carries data-submit-once, which is an enhancement over a form that
	// submits without it; what must not appear is markup that only works
	// because a script interpreted it.
	for _, scripted := range scriptedMarkup {
		if strings.Contains(out, scripted) {
			t.Errorf("the create form carries %q; the picker is the platform's own control and needs none of it:\n%s", scripted, out)
		}
	}
	if strings.Contains(strings.ToLower(out), "<script") {
		t.Errorf("the create form loads a script of its own:\n%s", out)
	}

	// A suggestion is an attribute value, and from T023 it is a directory name
	// off a filesystem walk rather than a line the operator typed. A directory
	// named with a quote must close nothing.
	hostile := renderComponent(t, "create-form", createFormView{PageToken: "t", Suggestions: []string{`/srv/work/" onfocus="stealFocus`}})
	if strings.Contains(hostile, `" onfocus="stealFocus`) {
		t.Errorf("a suggestion escaped its attribute:\n%s", hostile)
	}
}

// TestAnyPathStillTypeable is FR-040, and the requirement a picker is most
// likely to be "improved" past: a list of directories is one short step from a
// chooser of directories, and the chooser is wrong.
//
// **Must fail when** a `<select>` replaces the input, or a `pattern` appears on
// it. Either turns the convenience into the control — an allowlist rendered
// into markup, free to drift from ResolveWorkDir, refusing in a native bubble
// this daemon never wrote and with nothing on the page to say why.
//
// The other half of the sentence — that a typed path *is* accepted when it is
// allowlisted — is the handler's, unchanged by this task and pinned by the
// create route's own tests. This control narrows nothing it hands over.
func TestAnyPathStillTypeable(t *testing.T) {
	t.Parallel()

	for name, view := range map[string]createFormView{
		"offering suggestions": {PageToken: "t", Suggestions: workdirSuggestions},
		"offering none":        {PageToken: "t"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			out := renderComponent(t, "create-form", view)

			input := workdirInput.FindString(out)
			if input == "" {
				t.Fatalf("the working directory is no longer a field the operator can type into:\n%s", out)
			}
			if kind, _ := attributeValue(t, input, "type"); kind != "text" {
				t.Errorf("the working-directory field is type %q; free text is what makes any path reachable (%s)", kind, input)
			}
			if pattern, ok := attributeValue(t, input, "pattern"); ok {
				t.Errorf("the working-directory field carries pattern=%q, which is the containment rule copied into the markup:\n%s", pattern, out)
			}
			if chooser := regexp.MustCompile(`<select\b[^>]*\bname="work_dir"`).FindString(out); chooser != "" {
				t.Errorf("the working directory became a chooser (%s):\n%s", chooser, out)
			}
		})
	}
}

// TestNoSuggestionsRendersPlainField is the absent-value half, and it is the
// state every render is in today: no task yet supplies a suggestion, so this is
// what an operator actually meets.
//
// **Must fail when** an empty `<datalist>` is emitted, or the field points at
// one that was not rendered. Both are the same defect in two shapes — markup
// that reads like an offer and makes none — and FR-018a's rule about absent
// values is that a component states the absence rather than rendering
// something shaped like a value.
func TestNoSuggestionsRendersPlainField(t *testing.T) {
	t.Parallel()

	out := renderComponent(t, "create-form", createForm())

	if strings.Contains(out, "<datalist") {
		t.Errorf("a form with nothing to suggest renders a datalist anyway:\n%s", out)
	}
	input := workdirInput.FindString(out)
	if input == "" {
		t.Fatalf("the create form renders no work_dir field at all:\n%s", out)
	}
	if list, ok := attributeValue(t, input, "list"); ok {
		t.Errorf("the field points at the datalist %q and none is rendered (%s); the field with nothing to suggest is the field that shipped before this existed", list, input)
	}
}

// TestDefaultInstallRendersOptions is T006 read at the layer this milestone
// exists for. Every assertion behind the picker was about a walk or a view, and
// the operator met a plain text field: discovery was the only source and it is
// off unless asked for, so a default install rendered a control with nothing in
// it. A union with three passing unit tests is the same shipped defect one layer
// down if the page never passes it on.
//
// It drives the server's own projection rather than handing the component a
// literal — a form fed a fixture list would render options on a daemon that
// offers none. The configuration is the plainest one that exists: one approved
// root, no explicit list, no discovery.
//
// **Must fail when** discovery is the only source again, and when the fix for
// emptiness is to turn discovery on — the child below is on the real filesystem
// so that a walk reaching past the gate would put it in the markup.
func TestDefaultInstallRendersOptions(t *testing.T) {
	t.Parallel()

	// Resolved, because that is what config.Load hands the daemon and what the
	// walk compares against. On a host whose temp directory is itself a symlink,
	// an unresolved root would leave the child assertion below passing for a
	// reason that has nothing to do with the gate it is about.
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("resolve the temp dir: %v", err)
	}
	child := filepath.Join(root, "repo")
	if err := os.Mkdir(child, 0o750); err != nil {
		t.Fatalf("create a discoverable directory: %v", err)
	}

	s := newTestServer(t, loopbackListen)
	s.cfg.Roots = []config.ApprovedRoot{{Path: root}}

	view := s.fleet(&access.VerifiedOperator{Email: testOperatorEmail, Owner: auth.CallerOperator}, testCardToken, nil)
	out := renderComponent(t, "create-form", view.Create)

	if !strings.Contains(out, `<option value="`) {
		t.Fatalf("a daemon configured with an approved root and nothing else rendered a picker with no options in it, which is the field an operator reported as missing:\n%s", out)
	}
	if !strings.Contains(out, `<option value="`+root+`">`) {
		t.Errorf("the create form offers no %q, and it is this daemon's one approved root:\n%s", root, out)
	}
	// The field has to point at the list for a browser or a screen reader to
	// reach it: options nothing points at are markup neither one reads.
	input := workdirInput.FindString(out)
	if input == "" {
		t.Fatalf("the create form renders no work_dir field at all:\n%s", out)
	}
	list, ok := attributeValue(t, input, "list")
	if !ok {
		t.Fatalf("the working-directory field points at no list (%s), so the options above are markup the browser never reads:\n%s", input, out)
	}
	if !strings.Contains(out, `<datalist id="`+list+`">`) {
		t.Errorf("the field points at the datalist %q and no such element is rendered:\n%s", list, out)
	}

	// The half that keeps the fix from becoming a disclosure. What is *inside* a
	// root is read from the host, and this operator did not ask for that.
	if strings.Contains(out, `<option value="`+child+`">`) {
		t.Errorf("the create form names %q with %s unset; the roots are offered because the operator configured them, and their children are the thing they have to ask for:\n%s",
			child, config.EnvDiscoverRoots, out)
	}
}

// The themed picker's three additions, and the shape each one has to keep
// (T008, contracts/themed-combobox.md). The wrapper is matched non-greedily to
// the first close: it holds an input, a datalist, a list and a status region and
// no nested div, so the first `</div>` after it is its own — and a wrapper that
// grew one would be a structure this task did not build.
var (
	comboWrapper = regexp.MustCompile(`(?s)<div\b[^>]*\bclass="combo"[^>]*>(.*?)</div>`)
	comboListbox = regexp.MustCompile(`<ul\b[^>]*\bclass="combo-list"[^>]*>\s*</ul>`)
	comboStatus  = regexp.MustCompile(`<p\b[^>]*\bclass="combo-status"[^>]*>\s*</p>`)
)

// comboViews is the two states the wrapper has to render in. The second is the
// one the enhancement can never help: a form with nothing to suggest still has
// to be the plain field that shipped before any of this existed.
func comboViews() map[string]createFormView {
	return map[string]createFormView{
		"offering suggestions": {PageToken: testCardToken, Suggestions: workdirSuggestions},
		"offering none":        {PageToken: testCardToken},
	}
}

// combo returns what the wrapper holds, failing if the form renders no wrapper
// at all.
func combo(t *testing.T, out string) string {
	t.Helper()

	match := comboWrapper.FindStringSubmatch(out)
	if match == nil {
		t.Fatalf("the create form renders no .combo wrapper around the working-directory field:\n%s", out)
	}
	return match[1]
}

// TestComboRendersWithoutAriaRoles is the rule the whole themed-combobox
// contract turns on, read at the only layer that can hold it. The native
// control works first and the theme is an enhancement over something that
// already functions, so the roles that describe the enhancement are added by
// the script that makes them true.
//
// **Must fail when** the roles are moved into the template. That is the shape
// this task is likeliest to be lost in by improvement rather than by mistake:
// markup carrying role="combobox" and aria-expanded="false" looks finished, and
// in a browser running no script it announces a control that does not exist and
// a popup that can never open. Markup that lies to a screen reader is worse
// than markup describing the plain field that is really there.
//
// role="status" and aria-live on the status region are not in the sweep and are
// required by the test below: a live region has to be in the accessibility tree
// before its text arrives, which is a fact about the region rather than a claim
// about a control.
func TestComboRendersWithoutAriaRoles(t *testing.T) {
	t.Parallel()

	for name, view := range comboViews() {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			out := renderComponent(t, "create-form", view)
			held := combo(t, out)
			if !strings.Contains(held, `name="work_dir"`) {
				t.Errorf("the .combo wrapper does not hold the working-directory field (%s):\n%s", held, out)
			}

			for _, aria := range []string{
				`role="combobox"`,
				`role="listbox"`,
				`role="option"`,
				"aria-expanded",
				"aria-controls",
				"aria-autocomplete",
				"aria-activedescendant",
			} {
				if strings.Contains(out, aria) {
					t.Errorf("the create form carries %s with no script to make it true; without one there is no combobox to expand and nothing to control, and a reader is told about a control that is not there:\n%s", aria, out)
				}
			}
		})
	}
}

// TestComboRendersListAndDatalist holds the two joints this structure adds, and
// both of them lose silently. The field is joined to its options by a `list`
// attribute naming a `<datalist>` id, and the enhancement will be joined to its
// listbox by an aria-controls naming a `<ul>` id — three spellings in two trees,
// none of which any compiler checks.
//
// **Must fail when** the ids drift apart. The symptom is the one that survives
// review: a picker that renders perfectly, in the right place, with the right
// styling, and offers nothing at all.
func TestComboRendersListAndDatalist(t *testing.T) {
	t.Parallel()

	out := renderComponent(t, "create-form", createFormView{PageToken: testCardToken, Suggestions: workdirSuggestions})
	held := combo(t, out)

	input := workdirInput.FindString(held)
	if input == "" {
		t.Fatalf("the .combo wrapper holds no work_dir field:\n%s", out)
	}
	list, ok := attributeValue(t, input, "list")
	if !ok {
		t.Fatalf("the working-directory field points at no list (%s):\n%s", input, out)
	}
	if !strings.Contains(held, `<datalist id="`+list+`">`) {
		t.Errorf("the field points at the datalist %q and the wrapper holds no such element; the options are markup the browser never reads:\n%s", list, out)
	}
	for _, path := range workdirSuggestions {
		if !strings.Contains(held, `<option value="`+path+`">`) {
			t.Errorf("the wrapper's datalist omits the suggestion %q, so the enhancement's one data source is missing it too:\n%s", path, out)
		}
	}

	// The listbox the script will name. It is empty markup today and its id is
	// the whole of what T010 has to point aria-controls at.
	listbox := comboListbox.FindString(held)
	if listbox == "" {
		t.Fatalf("the wrapper holds no empty .combo-list; a listbox composed at enhancement time is a class the stylesheet sweep reads as a dead rule:\n%s", out)
	}
	if id, ok := attributeValue(t, listbox, "id"); !ok || id != "workdir-listbox" {
		t.Errorf("the listbox is id=%q and the enhancement controls %q; the two spellings are a joint between two trees and nothing else holds them", id, "workdir-listbox")
	}
	if !strings.Contains(listbox, " hidden") {
		t.Errorf("the listbox renders visible (%s); with no script it can never be filled, and an empty box below the field is a control that reads as broken", listbox)
	}
}

// TestComboRendersPlainFieldWithNoSuggestions is FR-043 and FR-018a applied to
// the wrapper: a daemon with nothing to suggest renders the field exactly as it
// shipped before any picker existed, now inside a box that changes none of it.
//
// **Must fail when** an empty `<datalist>` is emitted, or the `list` attribute
// survives the element it names, or the listbox is filled with something. All
// three are markup that reads like an offer and makes none.
func TestComboRendersPlainFieldWithNoSuggestions(t *testing.T) {
	t.Parallel()

	out := renderComponent(t, "create-form", createForm())
	held := combo(t, out)

	if strings.Contains(held, "<datalist") {
		t.Errorf("a form with nothing to suggest renders a datalist anyway:\n%s", out)
	}
	input := workdirInput.FindString(held)
	if input == "" {
		t.Fatalf("the .combo wrapper holds no work_dir field:\n%s", out)
	}
	if list, ok := attributeValue(t, input, "list"); ok {
		t.Errorf("the field points at the datalist %q and none is rendered (%s)", list, input)
	}
	if comboListbox.FindString(held) == "" {
		t.Errorf("the listbox is missing or is not empty; with nothing to suggest there is nothing it could ever hold:\n%s", held)
	}
	if comboStatus.FindString(held) == "" {
		t.Errorf("the status region is missing or is not empty:\n%s", held)
	}
}

// TestComboStatusRegionIsInTheTemplate is docs/components.md's accessibility
// floor applied to the region the enhancement announces through: a live region
// has to be in the accessibility tree before its text arrives for the
// announcement to happen at all, which is the same rule the fleet's own notes
// and the subset note beside this field already follow.
//
// **Must fail when** it is created by script — the first announcement is then
// made into a region a reader has never seen, and the stylesheet sweep reads
// .combo-status as a rule no template renders, which is how a second component
// starts.
//
// Empty rather than hidden, for the same reason: a region revealed and written
// in one go is one some readers never announce.
func TestComboStatusRegionIsInTheTemplate(t *testing.T) {
	t.Parallel()

	for name, view := range comboViews() {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			held := combo(t, renderComponent(t, "create-form", view))
			region := comboStatus.FindString(held)
			if region == "" {
				t.Fatalf("the wrapper holds no empty .combo-status:\n%s", held)
			}
			if strings.Contains(region, " hidden") {
				t.Errorf("the status region is rendered hidden (%s); it is empty markup that costs the field nothing, and hiding it keeps it out of the accessibility tree until the moment it has something to say", region)
			}
			if !strings.Contains(region, `role="status"`) || !strings.Contains(region, `aria-live="polite"`) {
				t.Errorf("the status region is not a polite live region (%s), so what the enhancement says about a narrowed list is said to nobody who cannot see it", region)
			}
		})
	}
}

// TestCreateFormHasNoResumeField is US5 in the markup: the form no longer asks
// for a conversation identifier, and asking is the whole of what was wrong with
// it. The offer beside the field could only ever list the conversations of a
// *directory*, while what an operator wants back is the one *this session* was
// having — the ambiguity FR-032 refuses to resolve by guessing.
//
// It reads the rendered markup rather than the view, which is this milestone's
// standing obligation: milestone 4 shipped three green tasks for a requirement
// about what an operator sees while the form kept the control it was meant to
// lose.
//
// **Must fail when** the field survives as a hidden input — the shape a deletion
// takes when someone means to keep the plumbing working — or when the datalist
// outlives the field that pointed at it. Both are the question still being asked,
// one of them without even the label that said what it was for.
//
// Both states are asserted, because a form with nothing to suggest is trivially
// free of a datalist and would pass this on its own.
func TestCreateFormHasNoResumeField(t *testing.T) {
	t.Parallel()

	for name, view := range map[string]createFormView{
		"a form with something to suggest": {PageToken: testCardToken, Suggestions: []string{"/home/operator/code/crswd"}},
		"a form with nothing to suggest":   createForm(),
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			out := renderComponent(t, "create-form", view)

			for _, gone := range []string{`name="resume"`, "conversation-suggestions", `id="create-resume"`, `id="create-resume-hint"`} {
				if strings.Contains(out, gone) {
					t.Errorf("the create form still carries %s, so it still asks for a conversation:\n%s", gone, out)
				}
			}
			// A hidden field is the deletion's near miss: nothing on the page
			// asks the question and every submission answers it anyway.
			if hidden := regexp.MustCompile(`<input\b[^>]*\btype="hidden"[^>]*\bname="resume"`).FindString(out); hidden != "" {
				t.Errorf("the conversation field went hidden rather than away (%s):\n%s", hidden, out)
			}
		})
	}
}

// TestViewCarriesNoConversations is the same removal one layer down, and it is
// the half that keeps the field from coming back: a view that still carried the
// data would be a template edit away from asking again, and the projection
// behind it reads the filesystem on the render path.
//
// It is written against the struct's own fields rather than against a call site,
// because "left in place for later" is exactly the state a call site cannot see —
// an unpopulated field looks like an empty offer.
//
// **Must fail when** the conversation projection grows a field carrying any part
// of what a conversation *said*.
//
// # This test replaced a stricter one, deliberately
//
// US5 removed conversations from this form outright, and the guard here asserted
// that no conversation-shaped field existed at all. That was the right rule for
// that milestone: the offer it removed listed every suggested directory's
// conversations to fill a free-text identifier field with, which asked an
// operator to resolve an ambiguity the daemon itself had refused to.
//
// Milestone 15 answers the question that milestone deferred — a picker, scoped to
// one working directory that has passed the create's own allowlist check — so the
// absolute guard is gone and this one takes its place. What is being protected is
// no longer "no conversations" but FR-025: enough to choose between them and no
// more. The transcript itself is the operator's work, and this daemon renders
// none of it.
func TestTheConversationProjectionCarriesNoContent(t *testing.T) {
	t.Parallel()

	// Everything a transcript holds, and the two things reading one would leak
	// besides: how big the work was, and where it sits on this host.
	forbidden := []string{"title", "message", "prompt", "summary", "text", "body", "content", "path", "file", "size", "bytes"}

	view := reflect.TypeOf(conversationView{})
	for i := range view.NumField() {
		field := view.Field(i)
		lower := strings.ToLower(field.Name)
		for _, word := range forbidden {
			if strings.Contains(lower, word) {
				t.Errorf("the conversation projection carries %s %s, which is part of what a conversation said rather than which one it is", field.Name, field.Type)
			}
		}
	}

	// Non-vacuity: the projection really does carry the two fields FR-025 allows,
	// so the sweep above is passing on a type with something in it.
	for _, want := range []string{"ID", "Modified"} {
		if _, ok := view.FieldByName(want); !ok {
			t.Errorf("the conversation projection has no %s field; the sweep above is asserting nothing", want)
		}
	}
}

// TestTheCardSaysWhatItIsRunning covers the other half: two sessions are
// otherwise identical on a fleet, and an operator needs to tell the
// remote-control one from the plain one.
//
// The empty case is not cosmetic. A default session and an adopted one both
// carry no name, and rendering "default" for either would be printing a guess:
// the daemon did not start an adopted session and does not know what did.
func TestTheCardSaysWhatItIsRunning(t *testing.T) {
	t.Parallel()

	withMode := renderComponent(t, "session-card", sessionView{ID: strings.Repeat("a", 32), Name: "x", StartCommand: "rc"})
	if !strings.Contains(withMode, "rc") {
		t.Errorf("a card for an rc session does not say so:\n%s", withMode)
	}

	without := renderComponent(t, "session-card", sessionView{ID: strings.Repeat("b", 32), Name: "x"})
	if strings.Contains(without, "card-mode") {
		t.Errorf("a card with no recorded start command labels one anyway:\n%s", without)
	}
}

// cardModeValue is the meta list's mode row as a reader receives it: the label,
// and whatever the value cell holds.
//
// It captures markup and strips it rather than matching text directly, so a
// value wrapped for styling still passes and a value that is *only* markup — the
// coloured dot FR-059 forbids — leaves nothing behind and fails. A test that
// demanded a bare text node would refuse a legitimate span; one that searched
// the whole card for the word would pass on a title attribute nobody can read.
var (
	cardModeRow = regexp.MustCompile(`(?s)<dt>mode</dt>\s*<dd>(.*?)</dd>`)
	markupTags  = regexp.MustCompile(`<[^>]*>`)
)

// TestCardShowsMode is FR-031 and FR-059 on the card: a session's mode is shown,
// and it is shown in words.
//
// It renders through cardOf rather than a hand-built view, because the claim is
// about what a page shows and the projection is half of that — a template that
// renders a field nothing fills is the shape of bug this plan warns about twice.
// The name that means remote is passed in the same way the daemon passes it, so
// this test also fails if the card ever starts deciding that for itself.
//
// The local case is not the trivial half. It is what every session on a daemon
// configuring no remote control is, and what an adopted session is, so a card
// that shows a mode only when it is the interesting one would leave the ordinary
// operator reading a card that says nothing about the thing the toggle changes.
func TestCardShowsMode(t *testing.T) {
	t.Parallel()

	const remoteCommand = "rc"
	now := time.Date(2026, time.August, 7, 12, 0, 0, 0, time.UTC)

	for _, tc := range []struct {
		start string
		want  session.Mode
	}{
		{start: remoteCommand, want: session.ModeRemote},
		{start: "review", want: session.ModeLocal},
	} {
		t.Run(string(tc.want), func(t *testing.T) {
			t.Parallel()

			card := cardOf(session.Session{
				ID:           strings.Repeat("c", 32),
				Name:         "a session",
				WorkDir:      "/home/operator/code/crswd",
				StartCommand: tc.start,
				CreatedAt:    now.Add(-time.Hour),
				LastActivity: now,
			}, now, testCardToken, remoteCommand)

			if card.Mode != tc.want {
				t.Fatalf("cardOf projected mode %q for a session running %q, want %q", card.Mode, tc.start, tc.want)
			}

			out := renderComponent(t, "session-card", card)
			row := cardModeRow.FindStringSubmatch(out)
			if row == nil {
				t.Fatalf("the card carries no labelled mode, so the fact the toggle changes is not on it:\n%s", out)
			}
			if got := strings.TrimSpace(markupTags.ReplaceAllString(row[1], "")); got != string(tc.want) {
				t.Errorf("the card's mode reads %q, want %q — state is never carried by markup or colour alone (FR-059):\n%s", got, tc.want, out)
			}
		})
	}
}

// The two deadline rows, read the way the mode row is: the label pins which
// clock the value belongs to, so a card that showed one duration twice — or
// swapped them — fails here rather than reading plausibly.
var cardLifetimeRow = regexp.MustCompile(`(?s)<dt>lifetime deadline</dt>\s*<dd>(.*?)</dd>`)

// TestCardShowsItsDeadline is T003: an operator can see when a session dies.
//
// It renders through cardOf for the reason TestCardShowsMode does — the claim is
// about what a page shows, and a template rendering a field nothing fills is the
// shape of bug this milestone exists to close for the fifth time.
//
// It asserted two deadlines until milestone 15, and the idle one went with its
// bound. The case that matters most is the disabled one: a switched-off deadline
// answers a century out, so a card that formatted it would read "in 36500 days"
// — a date nothing in this daemon believes — and it is now the *only* visible
// evidence that the create form's switch took effect.
//
// **Must fail when** the row renders that far-future instant, when it goes
// missing, or when the deadline is measured against a clock reading other than
// the render's.
func TestCardShowsItsDeadline(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.August, 10, 12, 0, 0, 0, time.UTC)

	for _, tc := range []struct {
		name         string
		lifetime     time.Duration
		wantLifetime string
	}{
		{
			// The daemon's own default: 24h of lifetime from a creation an hour
			// ago.
			name:         "the daemon's default",
			wantLifetime: "in 23 hours",
		},
		{
			// Milestone 13: the deadline that is never renewed is not there at
			// all, on a daemon whose operator removed the ceiling. Since
			// milestone 15 this is the whole of "nothing reaps this session",
			// where it used to be half of it.
			name:         "no absolute deadline",
			lifetime:     -1,
			wantLifetime: noLifetimeLimit,
		},
		{
			// A per-session lifetime shorter than the default, so the row cannot
			// be the default arriving by coincidence.
			name:         "a shorter lifetime than the default",
			lifetime:     3 * time.Hour,
			wantLifetime: "in 2 hours",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			card := cardOf(session.Session{
				ID:           strings.Repeat("d", 32),
				Name:         "a session",
				WorkDir:      "/home/operator/code/crswd",
				Lifetime:     tc.lifetime,
				CreatedAt:    now.Add(-time.Hour),
				LastActivity: now,
			}, now, testCardToken, "rc")

			out := renderComponent(t, "session-card", card)
			found := cardLifetimeRow.FindStringSubmatch(out)
			if found == nil {
				t.Fatalf("the card carries no labelled lifetime deadline, so an operator cannot tell when this session dies:\n%s", out)
			}
			if got := strings.TrimSpace(markupTags.ReplaceAllString(found[1], "")); got != tc.wantLifetime {
				t.Errorf("the card's lifetime deadline reads %q, want %q:\n%s", got, tc.wantLifetime, out)
			}
		})
	}
}

// TestEveryActionablePageCarriesTheLiveRegion is #77's real lesson.
//
// The toast script bails when the region is absent — so a page with action
// controls and no region has no interception at all, and every form on it
// navigates to a bare fragment. That is exactly what happened: the region was on
// the fleet, the single-session page had rename and destroy on it, and the fix
// looked broken rather than absent.
//
// Asserting it per page rather than trusting a shared layout, because there is
// no shared layout — each page template stands alone, which is the property that
// let one of them fall behind.
func TestEveryActionablePageCarriesTheLiveRegion(t *testing.T) {
	t.Parallel()

	for _, page := range []string{"dashboard.html", "session.html"} {
		source, err := fs.ReadFile(web.Templates, "templates/"+page)
		if err != nil {
			t.Fatalf("read %s: %v", page, err)
		}
		markup := string(source)

		// Only pages that can act need one — but every page that can act needs
		// one, and that is the direction the bug went.
		if !strings.Contains(markup, `method="post"`) && !strings.Contains(markup, "action-row") {
			continue
		}
		if !strings.Contains(markup, `id="action-toast"`) {
			t.Errorf("%s renders action controls but no live region, so crswd.js will not intercept its forms and every action navigates", page)
		}
	}
}

// The two shapes T012's assertions read out of a header: the anchor, which
// cardAnchor already gives, and the page's one first-level heading, which the
// settings link must not be inside.
var brandHeading = regexp.MustCompile(`(?s)<h1[^>]*\bclass="brand"[^>]*>(.*?)</h1>`)

// renderedHeader is the header component on its own, which is where every claim
// about what the header contains belongs: the pages below assert that they carry
// it, and this is the one place its contents are counted.
func renderedHeader(t *testing.T) string {
	t.Helper()

	return renderComponent(t, "header", &access.VerifiedOperator{Email: "operator@example.com"})
}

// mastheadOf is the header out of some rendered markup, and it fails rather than
// handing back an empty string: a page that rendered no header at all would
// otherwise satisfy every "the heading holds no settings link" assertion here by
// having no heading either.
func mastheadOf(t *testing.T, name, markup string) string {
	t.Helper()

	match := mastheadElement.FindStringSubmatch(markup)
	if match == nil {
		t.Fatalf("%s renders no masthead, so nothing below asserted anything about its header:\n%s", name, markup)
	}
	return match[1]
}

// anchorTo is the link in some markup that points at a target, with its
// attributes and the text it reads. The target is matched with its closing quote
// so that href="/" is not also every path that begins with a slash.
func anchorTo(markup, target string) (anchor []string, ok bool) {
	for _, candidate := range cardAnchor.FindAllStringSubmatch(markup, -1) {
		if strings.Contains(candidate[1], `href="`+target+`"`) {
			return candidate, true
		}
	}
	return nil, false
}

// TestHeaderLinksToSettings is FR-011 and the whole of US3. The page shipped in
// milestone 4 and every daemon since has been able to reach it only by typing
// the address — which is the shape of defect this milestone exists for: three
// tasks were green about a route, and nothing read the markup leading to it.
//
// The address is settingsPath, derived from the pattern the router registers, so
// a renamed route fails here rather than leaving a link to nothing that reads
// perfectly. The text is asserted for the reason the wordmark's is: FR-030
// forbids signalling by symbol alone, and a gear glyph satisfies an href.
//
// **Must fail when** the page ships unreachable again.
func TestHeaderLinksToSettings(t *testing.T) {
	t.Parallel()

	masthead := mastheadOf(t, "the header component", renderedHeader(t))

	link, ok := anchorTo(masthead, settingsPath)
	if !ok {
		t.Fatalf("the header links nowhere at %s, so the settings page is reachable only by typing its address:\n%s", settingsPath, masthead)
	}
	if text := strings.TrimSpace(markupTags.ReplaceAllString(link[2], "")); !strings.Contains(strings.ToLower(text), "settings") {
		t.Errorf("the settings link reads %q and does not name the page it leads to; a glyph alone satisfies an href and says nothing (FR-030):\n%s", text, masthead)
	}
	if !strings.Contains(link[1], `class="masthead-link"`) {
		t.Errorf("the settings link's attributes are %q and name no class crswd.css has a rule for; the browser gets an unstyled link in the bar:\n%s", strings.TrimSpace(link[1]), masthead)
	}
}

// TestSettingsLinkIsOutsideTheBrandHeading is FR-012.
//
// #46 made the wordmark the route home and it lives inside the page's one
// first-level heading. A second anchor in there would compete for that role, and
// a heading holding two links is a heading that has become a menu — a change to
// what the page says about its own structure, not a layout preference.
//
// Both directions are held: the settings link is not in the heading, and the
// heading still holds exactly the one link it is supposed to. The count is what
// catches a third arriving later by a route this assertion did not predict.
//
// **Must fail when** the anchor lands inside the <h1>.
func TestSettingsLinkIsOutsideTheBrandHeading(t *testing.T) {
	t.Parallel()

	masthead := mastheadOf(t, "the header component", renderedHeader(t))

	heading := brandHeading.FindStringSubmatch(masthead)
	if heading == nil {
		t.Fatalf("the header renders no brand heading, so this asserted nothing about what is inside it:\n%s", masthead)
	}
	if strings.Contains(heading[1], `href="`+settingsPath+`"`) {
		t.Errorf("the settings link is inside the page's one first-level heading, which makes that heading a menu:\n%s", heading[0])
	}
	if anchors := cardAnchor.FindAllStringSubmatch(heading[1], -1); len(anchors) != 1 {
		t.Errorf("the brand heading holds %d links; it holds the wordmark and nothing else:\n%s", len(anchors), heading[0])
	}
}

// TestWordmarkIsStillTheFirstAnchor is the other half of FR-012: the new link
// must not displace the one that was already there.
//
// Order is the assertion and not presence, because presence is what
// TestTheHeaderIsTheRouteBackToTheFleet already holds. A settings link placed
// first is the first thing a keyboard operator reaches on every page of this
// dashboard and the first thing a screen reader lists — which is the primary
// anchor, whatever the markup calls it.
//
// **Must fail when** the settings link is placed before the wordmark.
func TestWordmarkIsStillTheFirstAnchor(t *testing.T) {
	t.Parallel()

	masthead := mastheadOf(t, "the header component", renderedHeader(t))

	anchors := cardAnchor.FindAllStringSubmatch(masthead, -1)
	if len(anchors) == 0 {
		t.Fatalf("the header renders no link at all, so this asserted nothing about which comes first:\n%s", masthead)
	}
	first := anchors[0][1]
	for _, want := range []string{`class="brand-link"`, `href="/"`} {
		if !strings.Contains(first, want) {
			t.Errorf("the header's first link is %q and carries no %s; the wordmark is the route home and it is reached first (#46):\n%s", strings.TrimSpace(first), want, masthead)
		}
	}
}

// TestHeaderHasExactlyTwoAnchors is the shape decision, written down where it
// can be broken: one link is not a navigation bar, and two is the whole of what
// this header is allowed to be.
//
// A third would arrive one obvious convenience at a time — a link to the logs, a
// link to a session — and each would be reasonable on its own. This count is
// what makes adding it a decision somebody takes deliberately rather than a diff
// nobody notices.
//
// **Must fail when** a third link is added without the shape being reconsidered.
func TestHeaderHasExactlyTwoAnchors(t *testing.T) {
	t.Parallel()

	masthead := mastheadOf(t, "the header component", renderedHeader(t))

	if anchors := cardAnchor.FindAllStringSubmatch(masthead, -1); len(anchors) != 2 {
		t.Errorf("the header renders %d links; it carries the wordmark and the settings link, and a third is a navigation bar this dashboard has not decided to have:\n%s", len(anchors), masthead)
	}
}

// renderedPages is every page this daemon serves, keyed by the template that
// renders it, each executed against the view its handler really builds.
//
// The map is checked against the template tree rather than trusted, because what
// the caller asserts is a universal claim and there is no shared layout here for
// a new page to inherit a header from — each page template stands alone, which
// is exactly how one of them falls behind.
func renderedPages(t *testing.T) map[string]string {
	t.Helper()

	operator := &access.VerifiedOperator{Email: "operator@example.com"}
	pages := map[string]string{
		"dashboard": renderedFleet(t),
		"session":   renderedSessionPage(t, actionableCard()),
		"settings": renderComponent(t, "settings", settingsView{
			Operator:   operator,
			Settings:   []settingRow{{Key: "listen", Value: loopbackListen, Source: "default"}},
			ConfigFile: "/home/operator/.config/crswd/crswd.conf",
		}),
		"not-found": renderComponent(t, "not-found", notFoundView{
			Operator: operator,
			Message:  emptyView{Title: notFoundTitle, Body: notFoundBody},
		}),
		// Against no data at all, which is what loginPage passes it: nothing on
		// that page is a fact about this daemon, and a view struct would be an
		// invitation to add one.
		"login": renderComponent(t, "login", nil),
	}

	unrendered := make(map[string]bool, len(pages))
	for name := range pages {
		unrendered[name] = true
	}
	err := fs.WalkDir(web.Templates, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || path.Dir(p) != "templates" {
			return err
		}
		name := strings.TrimSuffix(path.Base(p), templateExt)
		if !unrendered[name] {
			t.Errorf("web/%s is a page and nothing here renders it, so whatever its header carries is asserted by no test", p)
		}
		delete(unrendered, name)
		return nil
	})
	if err != nil {
		t.Fatalf("walk the embedded template tree: %v", err)
	}
	for name := range unrendered {
		t.Errorf("this file renders a page called %q and the template tree holds no such file", name)
	}
	return pages
}

// pagesWithNobodyToName is the named exception to the rule below, and it is a
// map to a reason rather than a set so that the exception has to be argued for
// in the same place it is taken.
//
// There is exactly one. The header exists so it is never ambiguous whose
// credentials are driving unsandboxed sessions on this host
// (docs/components.md), and the sign-in page is the one page in this tree served
// to somebody who has not been identified — that is its entire purpose. A
// masthead there would either invent an identity or draw an empty one, and the
// settings link inside it would point at a page this caller cannot open. The
// version goes with it, and that half is security rather than layout: every
// other page is behind layer 1, this one answers a stranger, and a version is
// exactly the fact GET /dashboard/version is behind the door to keep from a
// scanner.
//
// **A second entry here is a decision somebody takes deliberately**, which is
// the whole reason this is a named list and not a shape the loop skips.
var pagesWithNobodyToName = map[string]string{
	"login": "served before there is an operator to name, which is what it is for",
}

// TestEveryPageCarriesTheHeader is FR-011's word "every", and SC-003.
//
// A link on the fleet and nowhere else is an affordance an operator has to learn
// the shape of, which is the argument #48 already settled for the route home. It
// is asserted per page rather than on the partial, because carrying the partial
// is the thing a page can stop doing: there is no layout in this tree, so a page
// composing its own header is one edit away at all times.
//
// The not-found page is in here too and it is not padding. It is the page most
// likely to be written in a hurry and the one an operator meets after a mistyped
// address, which is precisely when a route to somewhere real is worth having.
//
// **Must fail when** a page composes its own header and loses the link.
func TestEveryPageCarriesTheHeader(t *testing.T) {
	t.Parallel()

	for name, page := range renderedPages(t) {
		if why := pagesWithNobodyToName[name]; why != "" {
			continue
		}
		masthead := mastheadOf(t, "the "+name+" page", page)
		if _, ok := anchorTo(masthead, settingsPath); !ok {
			t.Errorf("the %s page carries a header with no link to %s:\n%s", name, settingsPath, masthead)
		}
	}
}

// TestSettingsLinkHasVisibleFocusRing is docs/components.md's accessibility
// floor at the control this task adds.
//
// An anchor is focusable by the platform, so what has to hold is that the page's
// one :focus-visible rule still reaches it — that the rule is unqualified, and
// that nothing written for this link takes the outline away again. Both halves
// are lost by improvement rather than by mistake: a ring somebody thought was
// heavy on a small link in a bar.
//
// **Must fail when** `outline: none` appears on this control with no
// replacement, or when the ring is narrowed to selectors it does not match.
func TestSettingsLinkHasVisibleFocusRing(t *testing.T) {
	t.Parallel()

	var styled, unqualified bool
	for _, rule := range cssRules(stylesheet(t)) {
		unqualified = unqualified || rule.selector == ":focus-visible"
		if !strings.Contains(rule.selector, ".masthead-link") {
			continue
		}
		styled = true
		if match := outlineRemoved.FindString(rule.body); match != "" {
			t.Errorf("%s carries %q; the design system permits removing the outline only by replacing it", rule.selector, match)
		}
	}
	if !styled {
		t.Error("crswd.css has no .masthead-link rule at all, so this swept nothing and the link in the bar is unstyled")
	}
	if !unqualified {
		t.Error("crswd.css has no unqualified :focus-visible rule, so the ring on the settings link is whatever its own rules happen to draw")
	}
}

// TestSettingsStillHasNoMutatingVerb is FR-013, asked from the markup rather
// than from the route table.
//
// settings_test.go already holds that no mutating verb is registered on
// settingsPath, and this is not that assertion a second time. It starts at the
// link this task put on every page and follows where that link actually points:
// reachability is what changed today, and the failure worth catching is the one
// that reads as a natural next step — the page is one click away now, so a form
// on it begins to look like a convenience rather than the highest-consequence
// surface in the product. A route that does not exist cannot be exploited or
// mis-gated.
//
// The answer is compared against what a path nothing claims gives, rather than
// asserted a 404: a 405 is a route table handed to whoever asks for it, and the
// existing test settles that distinction the same way for the same reason.
//
// **Must fail when** reachability is mistaken for permission to add editing.
func TestSettingsStillHasNoMutatingVerb(t *testing.T) {
	t.Parallel()

	masthead := mastheadOf(t, "the header component", renderedHeader(t))

	var target string
	for _, anchor := range cardAnchor.FindAllStringSubmatch(masthead, -1) {
		if !strings.Contains(anchor[1], `class="masthead-link"`) {
			continue
		}
		href, ok := attributeValue(t, anchor[1], "href")
		if !ok {
			t.Fatalf("the header's settings link carries no href (%q), so there was nowhere to follow it to", strings.TrimSpace(anchor[1]))
		}
		target = href
	}
	if target == "" {
		t.Fatalf("the header renders no settings link, so this followed nothing:\n%s", masthead)
	}

	f := newFleet(t)
	for _, method := range []string{
		http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete,
	} {
		w := f.ask(t, method, target)
		nowhere := f.ask(t, method, target+"-nonesuch")

		if w.Code == http.StatusMethodNotAllowed {
			t.Errorf("%s %s — the page the header links to — was answered %d with %s: %q; which method a path serves is not a caller's to learn",
				method, target, w.Code, headerAllow, w.Header().Get(headerAllow))
			continue
		}
		if w.Code != nowhere.Code || w.Body.String() != nowhere.Body.String() {
			t.Errorf("%s %s answered %d (%s); at a path nothing claims it answered %d (%s) — something is registered there now, and this page is read-only (FR-013)",
				method, target, w.Code, w.Body.String(), nowhere.Code, nowhere.Body.String())
		}
	}
}

// The rain's own vocabulary, read out of the one file that declares it rather
// than transcribed here. A second copy in Go would be the very thing the four
// tests below exist to forbid — message text on the server — and it would agree
// with itself for as long as nobody changed the array.
var (
	messageArray  = regexp.MustCompile(`(?s)const MESSAGES = \[(.*?)\];`)
	messageString = regexp.MustCompile(`'([^']*)'`)
)

// rainSays is what crswd.js occasionally draws, and it fails the test rather
// than returning nothing: an empty list would make every sweep below vacuous.
func rainSays(t *testing.T) []string {
	t.Helper()

	block := messageArray.FindStringSubmatch(script(t))
	if block == nil {
		t.Fatal("crswd.js declares no MESSAGES array, so the rain says nothing and every assertion about what it says is vacuous")
	}

	var said []string
	for _, quoted := range messageString.FindAllStringSubmatch(block[1], -1) {
		if text := strings.TrimSpace(quoted[1]); text != "" {
			said = append(said, text)
		}
	}
	if len(said) == 0 {
		t.Fatal("crswd.js declares MESSAGES and puts nothing in it")
	}
	return said
}

// canvasTag is a canvas element's opening tag and everything on it.
var canvasTag = regexp.MustCompile(`(?i)<canvas([^>]*)>`)

// TestRainCanvasIsAriaHidden is FR-033's belt beside the braces.
//
// Canvas content is already outside the accessibility tree, so a drawn message
// is inaudible whatever this attribute says. The attribute is what covers the
// element itself — a canvas is still a node, and one with no accessible name
// sitting in the middle of a header is at best noise to move past.
//
// Asserted over every page rather than over the partial, and over *every*
// canvas on each: the partial is already checked in isolation elsewhere, and
// the failure this adds is a page that renders a second canvas of its own
// without the attribute the component carries.
//
// **Must fail when** a message becomes announceable.
func TestRainCanvasIsAriaHidden(t *testing.T) {
	t.Parallel()

	canvases := 0
	for name, page := range renderedPages(t) {
		for _, canvas := range canvasTag.FindAllStringSubmatch(page, -1) {
			canvases++
			if !strings.Contains(canvas[1], `aria-hidden="true"`) {
				t.Errorf(`the %s page renders <canvas%s>; the rain says nothing an operator needs, so it carries aria-hidden="true" (FR-033)`, name, canvas[1])
			}
		}
	}
	if canvases == 0 {
		t.Fatal("no page renders a canvas at all, so this asserted nothing — and the rain has nowhere to fall")
	}
}

// TestNoMessageInRenderedMarkup is the contract's "drawn, never inserted", made
// against the bytes a browser is handed.
//
// The messages are decoration drawn on a canvas, and a canvas leaves no trace in
// markup. So the whole claim is an absence: whatever crswd.js says, no render of
// any page ever contains it. That is what keeps the message out of the
// accessibility tree — a DOM node would be in it whatever the canvas beside it
// carried.
//
// Matched case-insensitively, because the failure is a message becoming content
// and a title-cased copy of one is exactly that.
//
// **Must fail when** they are inserted as DOM nodes.
func TestNoMessageInRenderedMarkup(t *testing.T) {
	t.Parallel()

	said := rainSays(t)
	for name, page := range renderedPages(t) {
		lowered := strings.ToLower(page)
		for _, message := range said {
			if strings.Contains(lowered, strings.ToLower(message)) {
				t.Errorf("the %s page renders %q; the rain's messages are drawn on the canvas and never put in the document, or they are in the accessibility tree (FR-033)", name, message)
			}
		}
	}
}

// The rain's half of crswd.js: everything from the glyph table down to the pane
// watcher, which is the first thing in the file that is not the rain. Both ends
// are asserted rather than assumed, so a rename that moved the boundary fails
// here instead of quietly shrinking what the sweep covers.
const (
	rainOpens  = "const GLYPHS ="
	rainCloses = "const watch = (pane) =>"
)

// reducedMotionGuard is start()'s early return under the preference — the one
// thing standing between a page that asked for stillness and the loop.
var reducedMotionGuard = regexp.MustCompile(`still\.matches\s*\)\s*\{\s*return;`)

// TestNothingRunsUnderReducedMotion is FR-032, and it is a structural claim
// because Go cannot execute this file — the same footing every other assertion
// about crswd.js stands on.
//
// FR-032 needs no code of its own: the rain already stops under the preference,
// and no rain means no messages. What it needs is for that to stay the only
// path. So the message must have no timer, no listener and no entry point of its
// own — it is reached from paint, paint is reached from the shared loop, and the
// loop is what start() declines to schedule when the preference is set.
//
// **Must fail when** a message path is added outside the rain's guard.
func TestNothingRunsUnderReducedMotion(t *testing.T) {
	t.Parallel()

	source := script(t)
	opens := strings.Index(source, rainOpens)
	closes := strings.Index(source, rainCloses)
	if opens < 0 || closes < opens {
		t.Fatalf("crswd.js does not open the rain at %q (%d) and close it at %q (%d), so there is no region to hold this to", rainOpens, opens, rainCloses, closes)
	}
	rain := source[opens:closes]

	if !reducedMotionGuard.MatchString(rain) {
		t.Error("the rain's loop is not guarded by a return on still.matches, so it runs on a page that asked for stillness and the messages run with it (FR-032)")
	}

	// Every name the message is made of, confined to the rain. A reference from
	// anywhere else in this file is a second caller, and a second caller is a
	// path the guard above does not stand in front of.
	for _, name := range []string{"MESSAGES", "SAYING_FRAMES", "SAYING_ODDS", "saying", "saidFor"} {
		spelling := regexp.MustCompile(`\b` + name + `\b`)
		found := spelling.FindAllStringIndex(source, -1)
		if len(found) == 0 {
			t.Errorf("crswd.js mentions %s nowhere; the rain has nothing to say and this sweep asserted nothing about where it says it", name)
			continue
		}
		for _, at := range found {
			if at[0] < opens || at[0] >= closes {
				t.Errorf("crswd.js reads %s at %d, outside the rain (%d..%d); the message may only be reached from the loop the preference stops (FR-032)", name, at[0], opens, closes)
			}
		}
	}

	// No clock of its own. The odds are spent once per painted frame, so a page
	// with no rain on it rolls them never.
	for _, timer := range []string{"setTimeout(", "setInterval(", "requestIdleCallback("} {
		if strings.Contains(rain, timer) {
			t.Errorf("the rain carries %s; a message on a timer would appear on a page whose loop never started (FR-032)", timer)
		}
	}

	// And it is drawn from inside paint, not beside it. The ordering is the
	// claim: a call between paint and tick is a call in paint's body, and paint
	// is only ever reached from the loop.
	paintAt := strings.Index(source, "const paint = (field) => {")
	callAt := strings.Index(source, "saying(field);")
	tickAt := strings.Index(source, "const tick = (now) => {")
	switch {
	case paintAt < 0 || callAt < 0 || tickAt < 0:
		t.Fatalf("crswd.js does not define paint (%d), call saying (%d) and define tick (%d)", paintAt, callAt, tickAt)
	case callAt < paintAt || callAt > tickAt:
		t.Errorf("crswd.js calls saying at %d, outside paint (%d..%d); drawn anywhere else it is no longer the loop that decides whether it runs (FR-032)", callAt, paintAt, tickAt)
	}
}

// TestMessagesAreNotServerSupplied is the contract's "beside the rain, not in a
// template", held in the direction it would be lost.
//
// The messages are decoration with no server involvement. Routing them through a
// handler or a template would make them content — something the daemon is
// saying, on the one surface an operator reads to find out what is running on
// their host — and it would put a second copy of the list somewhere it could
// disagree with the first. So the text exists in crswd.js and nowhere else in
// this repository's serving path: not in the template tree, not in the package
// that renders it.
//
// **Must fail when** they become content, and the daemon starts having opinions.
func TestMessagesAreNotServerSupplied(t *testing.T) {
	t.Parallel()

	said := rainSays(t)

	templates := 0
	err := fs.WalkDir(web.Templates, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		source, err := fs.ReadFile(web.Templates, p)
		if err != nil {
			return err
		}
		templates++
		lowered := strings.ToLower(string(source))
		for _, message := range said {
			if strings.Contains(lowered, strings.ToLower(message)) {
				t.Errorf("web/%s carries %q; the rain's messages live beside the rain, and a template that holds one has made it something the daemon says", p, message)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk the embedded template tree: %v", err)
	}
	if templates == 0 {
		t.Fatal("the embedded template tree is empty, so this sweep asserted nothing")
	}

	// The handlers too, because a route is the other way a message becomes
	// content: a view field carrying one reaches a template that never spelled it.
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read the package this renders from: %v", err)
	}
	handlers := 0
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		source, err := os.ReadFile(name) //nolint:gosec // G304: the name came from reading this package's own directory, which is the only way to sweep a file nobody remembered to list.
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		handlers++
		lowered := strings.ToLower(string(source))
		for _, message := range said {
			if strings.Contains(lowered, strings.ToLower(message)) {
				t.Errorf("internal/httpapi/%s carries %q; the daemon does not know what the rain says, which is what keeps it decoration", name, message)
			}
		}
	}
	if handlers == 0 {
		t.Fatal("this package has no non-test Go file, so the handler half of this sweep asserted nothing")
	}
}

// TestEveryPageShowsTheVersion is the operator's question — "what is actually
// running?" — answered without a shell on the host.
//
// **Must fail when** a page is added without the footer, or when the footer
// stops reading buildinfo.Version. The second is the one worth guarding: a
// footer that hardcoded a string would render perfectly and be wrong from the
// next release onward, which is indistinguishable from working until somebody
// deploys and the number does not move.
func TestEveryPageShowsTheVersion(t *testing.T) {
	t.Parallel()

	read := func(p string) string {
		t.Helper()
		source, err := fs.ReadFile(web.Templates, "templates/"+p)
		if err != nil {
			t.Fatalf("read %s: %v", p, err)
		}
		return string(source)
	}

	// It lives in the header now rather than a footer, which put the least
	// surprising fact on the page as far from the thing it describes as the
	// layout allows. Every page renders the header, so every page carries it.
	for _, page := range []string{"dashboard", "session", "settings", "not-found"} {
		if !strings.Contains(read(page+".html"), `{{ template "header"`) {
			t.Errorf("%s.html renders no header, so an operator on that page cannot tell what is running", page)
		}
	}

	if !strings.Contains(read("partials/header.html"), "{{ version }}") {
		t.Error("the header does not call the version function; a hardcoded string renders perfectly and is wrong from the next release onward")
	}
}

// settingsMenuElement is the settings page's section index, matched as the
// element rather than by its links, so what the assertions below read is the
// menu and never a control that happens to sit elsewhere on the page.
var settingsMenuElement = regexp.MustCompile(`(?s)<nav class="settings-menu"[^>]*>(.*?)</nav>`)

// TestTheSettingsMenuIsStillLinks is the mechanism the phone layout is not
// allowed to trade away.
//
// Below the breakpoint the menu is restyled into a scrolling row, and a row of
// choices is precisely the shape somebody reaches for a <select> or a <details>
// to build. Each of those was argued and priced in research.md R8 and rejected:
// a real link is a GET this daemon answers, so the menu works with no script at
// all, a section can be linked to and bookmarked, and the back button behaves.
// The reflow is CSS over the markup that already exists — a different shape, not
// a different mechanism.
//
// It lives here rather than beside the two stylesheet assertions this task also
// ships, because it reads rendered markup and that is the division this file and
// stylesheet_test.go are split on.
//
// **Must fail when** the menu is rebuilt as a control that needs JavaScript,
// which is what SC-009 is about and what no stylesheet assertion can see.
func TestTheSettingsMenuIsStillLinks(t *testing.T) {
	t.Parallel()

	page := renderComponent(t, "settings", settingsView{
		Operator: &access.VerifiedOperator{Email: "operator@example.com"},
		Shown:    "Limits",
		Sections: []settingSection{
			{Title: "Listening", Settings: []settingRow{{Key: "listen", Value: loopbackListen, Source: "default"}}},
			{Title: "Limits", Settings: []settingRow{{Key: "max_sessions", Value: "4", Source: "file"}}},
		},
	})

	match := settingsMenuElement.FindStringSubmatch(page)
	if match == nil {
		t.Fatalf("the settings page renders no section menu at all, so this asserted nothing about what the menu is made of:\n%s", page)
	}
	menu := match[1]

	// Updates is rendered outside the range over .Sections, so it is the entry a
	// menu rebuilt from that loop alone would quietly drop.
	for _, title := range []string{"Updates", "Listening", "Limits"} {
		if _, ok := anchorTo(menu, settingsPath+"?section="+title); !ok {
			t.Errorf("the menu offers no link to the %s section, so reaching it needs either a script or a typed address:\n%s", title, menu)
		}
	}

	// A button is in this list with the scripted controls: inside a menu it is
	// either a form's submit or something only a script can act on, and a section
	// entry is meant to be neither.
	for _, scripted := range []string{"<select", "<form", "<button", "onclick", "onchange"} {
		if strings.Contains(menu, scripted) {
			t.Errorf("the section menu renders %s, which needs a script or a POST where the page promises a bookmarkable GET per section that works with no JavaScript (SC-009):\n%s", scripted, menu)
		}
	}
}

// The four absences settings.html's header comment asserted, each paired with
// what the markup under that comment actually renders.
//
// The pairing is the whole test. "There is no form here" is a fine thing for a
// comment to say about a page that has no form, and this file was that page
// once; what makes it a defect is a denial standing over a file that does the
// opposite. So each claim is only read when its half of the page is present.
//
// The denials are the phrasings the false comment used, which is as far as a
// regex can honestly go — "carries no token" would evade this and mean the same
// thing. It catches the sentence coming back, not every way of writing it.
var settingsHeaderClaims = []struct {
	what    string
	renders *regexp.Regexp
	denies  *regexp.Regexp
}{
	{"a mutating form", regexp.MustCompile(`(?i)<form[^>]*method="post"`), regexp.MustCompile(`(?i)there is no form|\bno form (here|on this page|at all)\b`)},
	{"the page token", regexp.MustCompile(`name="` + fieldPageToken + `"`), regexp.MustCompile(`(?i)\bno page token\b`)},
	{"a control that submits one", regexp.MustCompile(`(?i)<button[^>]*type="submit"`), regexp.MustCompile(`(?i)\bno action row\b`)},
	{"a live region", regexp.MustCompile(`(?i)\baria-live=`), regexp.MustCompile(`(?i)\bno live region\b`)},
}

// TestTheSettingsCommentDescribesThePage is the one assertion in this milestone
// that is about a comment, and it is here because of what this pipeline is: a
// fresh context reads the file's own account of itself before it reads the file.
// This header stated that the page carries no form, no page token, no action row
// and no live region, for a milestone after all four landed — an executor that
// believed it would have "restored" the read-only page by deleting the operator's
// only way to edit a setting from a browser, and the denial it would have trusted
// was about whether a mutating form carries its token.
//
// Comments are stripped before a template renders and before every other sweep
// in this file reads one, so nothing else here can ever see this class of defect.
//
// **Must fail when** the header denies something the markup beneath it does.
func TestTheSettingsCommentDescribesThePage(t *testing.T) {
	t.Parallel()

	source, err := fs.ReadFile(web.Templates, "templates/settings.html")
	if err != nil {
		t.Fatalf("read the settings template: %v", err)
	}

	// The header comment is the one above the doctype. Splitting there rather
	// than sweeping every comment in the file keeps the per-row and per-section
	// notes out of it — several of them say "no token" truthfully, about the GET
	// that checks for a release.
	doctype := strings.Index(strings.ToLower(string(source)), "<!doctype")
	if doctype < 0 {
		t.Fatal("settings.html renders no doctype, so there was no line to split its header comment from its markup on")
	}
	header := templateComment.FindString(string(source)[:doctype])
	if header == "" {
		t.Fatal("settings.html carries no header comment above its doctype, so this read the page's own account of itself out of nothing")
	}
	// Unwrapped before anything is matched against it, because this comment is
	// wrapped prose: "carries no page / token" is one sentence to a reader and two
	// lines to a regex, and the claim would hide in the break. Proved by mutation
	// — the false paragraph restored verbatim evades the token denial without it.
	header = strings.Join(strings.Fields(header), " ")
	markup := string(source)[doctype:]

	present := 0
	for _, claim := range settingsHeaderClaims {
		if !claim.renders.MatchString(markup) {
			continue
		}
		present++
		if denial := claim.denies.FindString(header); denial != "" {
			t.Errorf("settings.html renders %s and its header comment says %q; the next fresh context reads that comment as the contract and acts on it", claim.what, denial)
		}
	}
	if present == 0 {
		t.Fatal("settings.html renders no form, no token, no submit control and no live region, so every denial above was read against nothing; if the page really did become read-only again, this test and that comment are rewritten together")
	}
}

// cardAgeRow is the meta list's age row, for the three-facts test below.
var cardAgeRow = regexp.MustCompile(`(?s)<dt>age</dt>\s*<dd>(.*?)</dd>`)

// TestACardSaysHowOldItIsWhatStartedItAndWhetherItDies is US5, and the reason it
// is one test rather than three is the reason the operator asked for it: the
// three facts are read together. A card that answers two of them is a card whose
// reader has to go somewhere else for the third.
//
// The adopted half is the half that used to be wrong, and it is why this test
// renders the same session twice. Before milestone 15 a restart rebuilt a record
// from what tmux knew, and tmux did not know a lifetime — so a never-expiring
// session came back saying it expired in 24 hours, which is a card stating a
// fact about an operator's session that nothing in the daemon believed. The name
// and the start command were restored by #72 and #58; this completes the set.
//
// **Must fail when** any of the three reads differently on an adopted session
// than on the one it was adopted from.
func TestACardSaysHowOldItIsWhatStartedItAndWhetherItDies(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.August, 14, 12, 0, 0, 0, time.UTC)
	started := now.Add(-3 * time.Hour)

	for _, tc := range []struct {
		name         string
		lifetime     time.Duration
		start        string
		wantMode     session.Mode
		wantLifetime string
	}{
		{
			name:         "remote control, and it never dies",
			lifetime:     -1,
			start:        "rc",
			wantMode:     session.ModeRemote,
			wantLifetime: noLifetimeLimit,
		},
		{
			name:         "plain, and it dies at the ceiling",
			start:        "",
			wantMode:     session.ModeLocal,
			wantLifetime: "in 21 hours",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			live := session.Session{
				ID:           strings.Repeat("e", 32),
				Name:         "a session",
				WorkDir:      "/home/operator/code/crswd",
				StartCommand: tc.start,
				Lifetime:     tc.lifetime,
				CreatedAt:    started,
				LastActivity: started,
			}
			// The same session as a restart hands it back: a fresh record built
			// from what the host held, which is what Adopt produces.
			adopted := live
			adopted.Adopted = true

			for what, s := range map[string]session.Session{"created": live, "adopted": adopted} {
				out := renderComponent(t, "session-card", cardOf(s, now, testCardToken, "rc"))

				for _, row := range []struct {
					fact  string
					match *regexp.Regexp
					want  string
				}{
					// How long it has been alive. Counted from CreatedAt, which
					// for an adopted session is tmux's own start time and not the
					// moment of adoption — so a restart does not make every
					// session on the fleet look newborn.
					{fact: "age", match: cardAgeRow, want: "3 hours"},
					// What started it, which is what remote-control provenance is
					// derived from (@crswd-start, #58).
					{fact: "mode", match: cardModeRow, want: string(tc.wantMode)},
					// Whether it can ever die (@crswd-lifetime, milestone 15).
					{fact: "lifetime deadline", match: cardLifetimeRow, want: tc.wantLifetime},
				} {
					found := row.match.FindStringSubmatch(out)
					if found == nil {
						t.Fatalf("the %s card carries no %s row:\n%s", what, row.fact, out)
					}
					if got := strings.TrimSpace(markupTags.ReplaceAllString(found[1], "")); got != row.want {
						t.Errorf("the %s card's %s reads %q, want %q:\n%s", what, row.fact, got, row.want, out)
					}
				}
			}
		})
	}
}

// fullCreateForm is the create view with every conditional control reachable:
// suggestions present, the daemon's lifetime ceiling removed, conversations
// recorded, and both command lines resolved.
//
// It exists because the bare createForm() fixture draws none of them, and a test
// reading only that would pass with four controls missing — the near miss
// TestCreateFormHasNoStartCommandSelect was written about, where three tasks
// shipped green for a requirement about what an operator sees because every
// assertion made was about a route or a record.
func fullCreateForm() createFormView {
	return createFormView{
		PageToken:              testCardToken,
		Roots:                  []string{"/home/operator/code", "/srv/work"},
		Suggestions:            []string{"/home/operator/code/crswd"},
		LifetimeCeilingRemoved: true,
		Commands:               map[bool]string{false: "local-command", true: "remote-command"},
	}
}

// outsideTheDialog is everything the create component draws that is not inside
// its <dialog> — what comes before it and what comes after it, joined.
//
// **Both sides, and the second one is the correction.** This read only what came
// before, and a cross-model review pointed out that a duplicate create form left
// *after* `</dialog>` is outside the dialog, is exactly the "left a copy behind"
// failure the assertions below claim to catch, and passed. A test that inspects
// one side of the thing it is checking is a test of that side.
func outsideTheDialog(t *testing.T, out string) string {
	t.Helper()

	ahead, rest, ok := strings.Cut(out, "<dialog")
	if !ok {
		t.Fatalf("the create component opens no <dialog>, so there is nothing behind the trigger:\n%s", out)
	}
	_, after, ok := strings.Cut(rest, "</dialog>")
	if !ok {
		t.Fatalf("the create component's <dialog> is never closed:\n%s", out)
	}
	return ahead + after
}

// insideTheDialog is the dialog's own markup, opening tag excluded.
func insideTheDialog(t *testing.T, out string) string {
	t.Helper()

	_, after, ok := strings.Cut(out, "<dialog")
	if !ok {
		t.Fatalf("the create component opens no <dialog>:\n%s", out)
	}
	body, _, ok := strings.Cut(after, "</dialog>")
	if !ok {
		t.Fatalf("the create component's <dialog> is never closed:\n%s", out)
	}
	return body
}

// TestTheDashboardOffersATriggerAndKeepsTheFormBehindIt is FR-001 and FR-004:
// the dashboard shows one control that leads to a create and nothing else about
// creating one.
//
// The form was not wrong and none of its parts were removed — what was wrong is
// that all of it was on screen all of the time, on the page an operator opens to
// answer a different question. So this asserts a *placement*, which is a claim no
// route test can make and the one this milestone is entirely about.
//
// **Must fail when** the form is moved into the dialog and a copy is left behind
// in the flow, which is the shape of the mistake a move like this makes: both
// render, both submit, and the page looks merely untidy rather than broken.
func TestTheDashboardOffersATriggerAndKeepsTheFormBehindIt(t *testing.T) {
	t.Parallel()

	t.Run("nothing but the trigger stands outside the dialog", func(t *testing.T) {
		t.Parallel()

		outside := outsideTheDialog(t, renderComponent(t, "create-form", fullCreateForm()))
		for _, control := range []string{"<input", "<select", "<label", "<datalist", "<pre", "field-hint", "command-preview", "create-form", "modal-outcome"} {
			if strings.Contains(outside, control) {
				t.Errorf("the fleet still draws %s outside the dialog; the form moved and left a copy behind:\n%s", control, outside)
			}
		}
		if !strings.Contains(outside, `command="show-modal"`) {
			t.Errorf("the fleet draws no trigger that opens the dialog:\n%s", outside)
		}
	})

	t.Run("the whole page, with a daemon that has something to offer", func(t *testing.T) {
		t.Parallel()

		f := newFleet(t)
		f.cfg.StartCommands = config.NewStartCommands(map[string]string{
			"default": "claude --dangerously-skip-permissions",
			"rc":      "claude remote-control",
		})

		create := sectionOf(t, f.view(t).Body.String(), "create")
		outside := outsideTheDialog(t, create)
		if strings.Contains(outside, "<input") || strings.Contains(outside, "<label") {
			t.Errorf("a rendered fleet carries create fields outside the dialog:\n%s", outside)
		}
	})

	t.Run("no page token, no trigger and no dialog", func(t *testing.T) {
		t.Parallel()

		// The rule the form already followed, extended to the control that opens
		// it: a button whose dialog's submission is certain to be refused by the
		// gate reads like a capability, and an operator cannot tell it from a
		// working one until they have used it.
		view := fullCreateForm()
		view.PageToken = ""

		out := renderComponent(t, "create-form", view)
		for _, offered := range []string{"<dialog", "<button", "<input", "show-modal"} {
			if strings.Contains(out, offered) {
				t.Errorf("a render that minted no token still offers %s:\n%s", offered, out)
			}
		}
	})
}

// TestTheCreateDialogOpensAndClosesFromMarkupAlone is FR-005, FR-007 and FR-008,
// read where they are actually decided: the bytes a browser is handed.
//
// The dialog opens with `command="show-modal"` — the Invoker Commands API, which
// reached Baseline in 2026 — and that is what keeps this tree's rule that every
// control works with scripting off. crswd.js carries a fallback for older
// browsers and is not what makes this work; a test satisfied by the script would
// pass on a page where the attribute had been dropped and the scriptless
// operator had lost the create form entirely.
//
// **Must fail when** the trigger points at a dialog this component never renders,
// when `show-modal` degrades to `show` — which is a non-modal dialog, and takes
// the focus trap, the inert page behind and Esc with it — or when the only way
// out is the backdrop, which is the one dismissal not every browser has.
func TestTheCreateDialogOpensAndClosesFromMarkupAlone(t *testing.T) {
	t.Parallel()

	out := renderComponent(t, "create-form", fullCreateForm())

	opening := regexp.MustCompile(`<dialog\b([^>]*)>`).FindStringSubmatch(out)
	if opening == nil {
		t.Fatalf("the create component opens no <dialog>:\n%s", out)
	}
	id, ok := attributeValue(t, opening[1], "id")
	if !ok {
		t.Fatalf("the dialog carries no id (<dialog%s>), so nothing can name it", opening[1])
	}
	if _, ok := attributeValue(t, opening[1], "closedby"); !ok {
		t.Errorf("the dialog does not offer light dismiss (<dialog%s>); it is an enhancement and costs nothing where the browser ignores it", opening[1])
	}
	if _, ok := attributeValue(t, opening[1], "aria-labelledby"); !ok {
		t.Errorf("the dialog is named by nothing (<dialog%s>); its title is a heading an operator can see, and one spelling of it is what keeps the two from disagreeing", opening[1])
	}

	var opens, closes int
	for _, button := range regexp.MustCompile(`<button\b([^>]*)>`).FindAllStringSubmatch(out, -1) {
		command, hasCommand := attributeValue(t, button[1], "command")
		target, hasTarget := attributeValue(t, button[1], "commandfor")
		if !hasCommand {
			continue
		}
		if !hasTarget || target != id {
			t.Errorf("a control commands %q at %q, and the dialog on this form is %q (<button%s>)", command, target, id, button[1])
			continue
		}
		switch command {
		case "show-modal":
			opens++
		case "close":
			closes++
		default:
			// `show` is the trap this names: it opens the dialog non-modally, so
			// the page behind stays interactive, Esc does nothing, and there is
			// no focus trap — three of this dialog's four dismissal properties
			// gone on one word.
			t.Errorf("a control issues %q, which is not one of the two commands this dialog answers (<button%s>)", command, button[1])
		}
		if kind, _ := attributeValue(t, button[1], "type"); kind != "button" {
			t.Errorf("a command control is type=%q; a <button> defaults to submit, and the one inside the form would submit the create it exists to abandon (<button%s>)", kind, button[1])
		}
	}
	if opens != 1 {
		t.Errorf("the component renders %d controls that open the dialog; one page needs exactly one", opens)
	}
	if closes < 1 {
		t.Errorf("the component renders no control that closes the dialog, so the only ways out are Esc and a backdrop not every browser dismisses on")
	}
}

// TestEveryCreateControlMovedIntoTheDialog is FR-002: the form was moved, not
// rewritten.
//
// Rendered from fullCreateForm, because the four conditional controls are
// exactly the ones a move loses silently — they are absent from the bare fixture
// whether or not the template still draws them.
//
// **Must fail when** a control is dropped in the move, or renders outside the
// dialog where the operator will never see it.
func TestEveryCreateControlMovedIntoTheDialog(t *testing.T) {
	t.Parallel()

	inside := insideTheDialog(t, renderComponent(t, "create-form", fullCreateForm()))

	// The field names are the route's own constants, so the markup's spelling and
	// the handler's stay one fact — this suite's standing arrangement for a
	// template set parsed with no function map.
	// fieldResume left this list in spec 013 with the control that posted it.
	for _, field := range []string{fieldName, fieldWorkDir, fieldRemoteControl, fieldLifetime} {
		if !regexp.MustCompile(`name="` + regexp.QuoteMeta(field) + `"`).MatchString(inside) {
			t.Errorf("the dialog posts no %q; a control that did not come across is a capability an operator has lost:\n%s", field, inside)
		}
	}
	// The token is named by the constant the gate reads rather than by a literal,
	// which is this suite's standing arrangement and here also keeps gosec's G101
	// off a field *name* that every rendered page carries in plain sight — the
	// same false positive browser.go annotates on the constant itself.
	for what, marker := range map[string]string{
		"the allowed-roots hint":     `id="create-roots"`,
		"the working-directory list": `<datalist`,
		"the command preview":        "data-command-preview",
		"the page token":             fieldPageToken,
		"the in-flight note":         `id="create-busy"`,
	} {
		if !strings.Contains(inside, marker) {
			t.Errorf("%s is not inside the dialog:\n%s", what, inside)
		}
	}
}

// hintText is every sentence this form says to a person: the field hints and the
// in-flight note. Not the labels, which name a control rather than explain one.
var hintText = regexp.MustCompile(`(?s)<p class="(?:field-hint-text|create-note)"[^>]*>(.*?)</p>`)

// TestNoHintOnTheCreateFormRunsPastALine is FR-013, and the numeric bound is a
// proxy stated as one.
//
// What the requirement asks for is that no control carries more than a line, and
// "a line" is not a property of a string — it depends on a width and a font. So
// this pins a character budget instead, and the budget is calibrated rather than
// chosen: every hint this milestone wrote is comfortably under it, and both of
// the two-sentence hints it removed are over. A test that passed on the copy that
// prompted the requirement would be a test of nothing.
//
// The roots list is exempt and is not matched here: it is a list rather than
// prose, and it is the one hint that is load-bearing — the working-directory
// refusal deliberately will not name the roots, so without it the field is one an
// operator has to guess at.
//
// **Must fail when** a hint grows back into an explanation of the daemon.
func TestNoHintOnTheCreateFormRunsPastALine(t *testing.T) {
	t.Parallel()

	const budget = 80

	out := renderComponent(t, "create-form", fullCreateForm())
	found := hintText.FindAllStringSubmatch(out, -1)
	if len(found) == 0 {
		t.Fatal("the create form says nothing to a person at all, so this sweep found nothing to check and would pass whatever the copy did")
	}
	for _, hint := range found {
		if words := strings.TrimSpace(hint[1]); len(words) > budget {
			t.Errorf("a hint runs to %d characters against a budget of %d, which is an explanation rather than a line: %q", len(words), budget, words)
		}
	}
}

// TestARefusedCreateHasSomewhereLegibleToBeSaid is
// contracts/outcome-where-the-operator-is.md, and it exists because this
// milestone got the answer wrong once before measuring it.
//
// A modal <dialog> makes everything outside it **inert** — not merely covered by
// its backdrop, but removed from the accessibility tree. The action toast is at
// the foot of <body>, so while the create dialog is open it can say nothing to
// anybody: a refused create would be invisible *and* unannounced, which is worse
// than what shipped before the dialog existed and is the one way this milestone
// could have made things worse.
//
// The first attempt promoted the toast into the top layer with `popover`, on the
// reasoning that top-layer stacking is insertion order. It is, and it does not
// help: a popover above a modal dialog is inert too. Measured in Chrome 149 — a
// control appended inside the promoted region could not take focus while the
// dialog was open, where the identical control was focusable with the dialog
// closed and focusable inside the dialog while it was open. The popover's
// user-agent rules had also moved the region to the middle of the viewport and
// shrunk it to its text.
//
// So the region is inside the dialog. That is not two places an outcome can be
// written, which is the objection worth answering rather than dismissing: the
// toast is unreachable by construction while the dialog is open, so exactly one
// region can speak at a time, and both take their words from outcome.go.
//
// **Must fail when** the dialog carries no region of its own — which is the
// state the popover attempt left it in, and which every sweep in this suite
// passed.
func TestARefusedCreateHasSomewhereLegibleToBeSaid(t *testing.T) {
	t.Parallel()

	page := newFleet(t).view(t).Body.String()

	if n := strings.Count(page, `id="action-toast"`); n != 1 {
		t.Errorf("the fleet carries %d toasts; the page outside the dialog has one place an answer is read", n)
	}

	inside := insideTheDialog(t, sectionOf(t, page, "create"))
	region := regexp.MustCompile(`<p class="modal-outcome"([^>]*)>`).FindStringSubmatch(inside)
	if region == nil {
		t.Fatalf("the create dialog carries no region for an answer, so a refusal reaches an inert toast and is neither seen nor announced:\n%s", inside)
	}
	if live, ok := attributeValue(t, region[1], "aria-live"); !ok || live != "polite" {
		t.Errorf("the dialog's answer region is aria-live=%q; a refusal an operator cannot see is one nobody hears either (<p%s>)", live, region[1])
	}
	if role, ok := attributeValue(t, region[1], "role"); !ok || role != "status" {
		t.Errorf("the dialog's answer region carries role=%q (<p%s>)", role, region[1])
	}
	// Present and empty, never hidden. A live region has to be in the
	// accessibility tree before its text arrives, and one revealed and written in
	// the same frame is one some readers never announce.
	if strings.Contains(region[1], "hidden") {
		t.Errorf("the dialog's answer region ships hidden (<p%s>); a live region revealed and written at once is one some screen readers never announce", region[1])
	}

	// And the toast is left alone. The promotion that was tried is not merely
	// unnecessary now — it actively hid the region behind the popover's own
	// user-agent positioning, so its absence is asserted rather than assumed.
	toast := openingTag(t, page, "output", "action-toast")
	if strings.Contains(toast, "popover") {
		t.Errorf("the toast is declared as a popover (<output%s>); a popover above a modal dialog is inert, and the attribute moves and resizes the region for nothing", toast)
	}
}
