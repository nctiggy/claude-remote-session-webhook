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
	"path"
	"regexp"
	"strings"
	"testing"

	"github.com/nctiggy/claude-remote-session-webhook/internal/access"
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
// renders nothing that could be submitted. The empty state is the other half:
// docs/components.md documents it with a "Start a session" action, and the
// create form that fills it is T010's rather than this task's.
//
// The positive direction — a card handed a token renders the control, outside
// its one anchor — is the two tests below. Both directions are needed: this one
// alone is satisfied by a component that renders no control at all.
func TestAComponentHandedNothingToActWithOffersNoAction(t *testing.T) {
	t.Parallel()

	rendered := map[string]string{
		"a card with no page token": renderComponent(t, "session-card", ownedCard()),
		"the empty state":           renderComponent(t, "empty", emptyView{Title: "No sessions running", Body: "Nothing is executing on this host right now."}),
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

// TestTheCardLinksTheNameAndNotOnlyTheIdentifier is issue #16.
//
// The accessibility floor was already met before this: the identifier was a real
// <a>, focus-ringed, in tab order, and every card was reachable. What was wrong
// was the affordance. The card reads as clickable end to end, the heading is the
// session's name, and the only thing that opened the session was the 32-character
// hex — so a mouse aimed at the obvious target hit nothing and a keyboard landed
// on the least human-readable string on the card.
//
// One link and not two. A card that linked the name and went on linking the hex
// would put two identical destinations next to each other in every link list,
// which reads worse than the arrangement being fixed rather than better.
func TestTheCardLinksTheNameAndNotOnlyTheIdentifier(t *testing.T) {
	t.Parallel()

	card := ownedCard()
	got := renderComponent(t, "session-card", card)

	anchors := cardAnchor.FindAllStringSubmatch(got, -1)
	if len(anchors) != 1 {
		t.Fatalf("the card renders %d links; one session is one destination:\n%s", len(anchors), got)
	}
	attributes, text := anchors[0][1], anchors[0][2]

	if target := "/sessions/" + card.ID + "/view"; !strings.Contains(attributes, `href="`+target+`"`) {
		t.Errorf("the card's link does not open %s:\n%s", target, got)
	}
	if !strings.Contains(text, card.Name) {
		t.Errorf("the card's link reads %q and the session is called %q; the name is what an operator reads and aims at:\n%s", text, card.Name, got)
	}
	if strings.Contains(text, card.ID) {
		t.Errorf("the identifier is the link's own text; it is the handle, not the label:\n%s", got)
	}

	heading := cardHeading.FindStringSubmatch(got)
	if heading == nil {
		t.Fatalf("the card renders no heading:\n%s", got)
	}
	if !strings.Contains(heading[1], "<a") {
		t.Errorf("the card's heading is not the link, so the name is still inert:\n%s", got)
	}

	// Still rendered, and now as text. It is the only handle a session with no
	// name has, so linking the name must not have cost the card the identifier.
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

// TestCardHasExactlyOneAnchor is FR-027 on the card that finally has a control
// to put somewhere.
//
// The count alone is not the requirement. A destroy button nested inside the
// card's link would leave the count at one and still be the defect: a link and a
// submit control occupying one target, where the control ends an unsandboxed
// shell and the link merely opens a page. So the anchor's own contents are read
// as well, and the control is confirmed to exist — a card rendering no control
// at all would satisfy both of the other assertions and prove nothing.
//
// **Must fail when** a control is added inside the anchor: the second assertion
// catches it where the first cannot.
func TestCardHasExactlyOneAnchor(t *testing.T) {
	t.Parallel()

	got := renderComponent(t, "session-card", actionableCard())

	anchors := cardAnchor.FindAllStringSubmatch(got, -1)
	if len(anchors) != 1 {
		t.Fatalf("a card carrying its action row renders %d links; the card carried exactly one before it had controls and FR-027 keeps it there:\n%s", len(anchors), got)
	}
	for _, control := range []string{"<form", "<button", "<input"} {
		if strings.Contains(strings.ToLower(anchors[0][2]), control) {
			t.Errorf("the card's link contains %q; a control nested in the anchor is one target holding two things to do, and one of them is irreversible:\n%s", control, got)
		}
	}

	// The row is really there, so the two assertions above are about a card with
	// something in it. Without this a component that dropped the control entirely
	// would read as passing.
	if !strings.Contains(got, `class="card-actions"`) {
		t.Errorf("the card renders no action row at all, so nothing above was asserted about a control:\n%s", got)
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
	if len(forms) != 1 {
		t.Fatalf("the card renders %d action forms; this milestone's card carries one, the destroy:\n%s", len(forms), got)
	}
	attributes, contents := forms[0][1], forms[0][2]

	target := strings.Replace(strings.TrimPrefix(patternDashboardDestroy, "POST "), "{"+pathValueID+"}", card.ID, 1)
	if !strings.Contains(attributes, `action="`+target+`"`) {
		t.Errorf("the destroy form posts to <form%s> and the daemon serves %q:\n%s", attributes, target, got)
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
		session.DisplayIdle,
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
// Every page composes the header, and the header renders a rain canvas
// (docs/components.md). A canvas nothing draws into is an empty rectangle: the
// markup is right, the stylesheet is right, every assertion in this package
// passes, and the effect the design system calls the product's signature is
// simply absent in a browser. This is the only place that shows up.
func TestEveryPageLoadsTheLoopThatDrivesItsRain(t *testing.T) {
	t.Parallel()

	pages := 0
	err := fs.WalkDir(web.Templates, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || path.Dir(p) != "templates" {
			return err
		}
		pages++
		source, err := fs.ReadFile(web.Templates, p)
		if err != nil {
			return err
		}
		if !strings.Contains(string(source), `src="/static/crswd.js"`) {
			t.Errorf("web/%s loads no script, and every page carries a header with a rain canvas in it", p)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk the embedded template tree: %v", err)
	}
	if pages == 0 {
		t.Fatal("the embedded tree holds no page at all, so this sweep asserted nothing")
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

// TestEveryCanonicalComponentIsAPartial is FR-024 held by the shape of the tree:
// pages compose components, components live in one place, and a second card is a
// defect visible by inspection. It is a test rather than a convention because
// the cheapest way to add a second card is to inline one in a page.
func TestEveryCanonicalComponentIsAPartial(t *testing.T) {
	t.Parallel()

	set := newTestServer(t, loopbackListen).templates
	for _, component := range []string{"header", "status-pill", "session-card", "empty", "rain", "pane"} {
		if set.Lookup(component) == nil {
			t.Errorf("the template set defines no %q component", component)
		}
	}

	err := fs.WalkDir(web.Templates, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		switch name := strings.TrimSuffix(path.Base(p), templateExt); name {
		case "header", "status-pill", "session-card", "empty", "rain", "pane":
			if dir := path.Dir(p); dir != "templates/partials" {
				t.Errorf("the %s component lives in %s; docs/components.md puts every one of them in templates/partials", name, dir)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk the embedded template tree: %v", err)
	}
}
