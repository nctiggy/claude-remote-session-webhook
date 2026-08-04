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
func ownedCard() sessionView {
	return sessionView{
		ID:           "3f6c1d8e4b2a0957e1c3d5f7a9b1c3d5",
		Name:         "refactor the reaper",
		WorkDir:      "/home/operator/code/crswd",
		DisplayState: session.DisplayRunning,
		Age:          "2 hours",
	}
}

// mutationMarkup is every way a rendered component could offer to change
// something on this host: the element docs/components.md's Button renders, the
// htmx attributes it renders with, the form that would submit without either,
// and the handler attribute the policy already forbids.
var mutationMarkup = []string{"<button", "hx-post", "hx-put", "hx-patch", "hx-delete", "<form", "onclick"}

// TestTheReadOnlyComponentsOfferNoAction is the third of the four independent
// layers contracts/dashboard.md builds the read-only guarantee from: no mutating
// route exists behind this door, no browser can sign one, the reads reach no
// mutating path, and the templates render no affordance. This is that fourth
// one, held at the component rather than at the page, because a card is composed
// once and rendered everywhere.
func TestTheReadOnlyComponentsOfferNoAction(t *testing.T) {
	t.Parallel()

	rendered := map[string]string{
		"the session card": renderComponent(t, "session-card", ownedCard()),
		"the empty state":  renderComponent(t, "empty", emptyView{Title: "No sessions running", Body: "Nothing is executing on this host right now."}),
	}

	for name, markup := range rendered {
		lowered := strings.ToLower(markup)
		for _, offer := range mutationMarkup {
			if strings.Contains(lowered, offer) {
				t.Errorf("%s rendered %q; the dashboard is read-only in this milestone and a browser could not sign the request anyway:\n%s", name, offer, markup)
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
			with:      func() sessionView { v := ownedCard(); v.Actions = []actionView{{}}; return v }(),
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
