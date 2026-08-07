// Internal test, matching partials_test.go. That file sweeps the embedded
// template tree; this one covers the other embedded tree, because the design
// system's obligations divide cleanly between them: a template must carry no
// value at all, and the stylesheet must carry every value exactly once.
//
// Nothing here starts a server. The stylesheet is a static asset with no
// behaviour, so every claim below is about the bytes a browser is handed.
package httpapi

import (
	"io/fs"
	"regexp"
	"strings"
	"testing"

	"github.com/nctiggy/claude-remote-session-webhook/web"
)

// designTokens is docs/design-system.md's own CSS, transcribed here rather than
// read from the stylesheet it checks — the same reason securityHeaders in
// render_test.go writes the policy out by hand. A test that compared the file
// against its own spelling would still pass on a palette that had quietly
// drifted, which is precisely the drift Principle VII forbids.
//
// The typography and layout values are deliberately absent: the design system
// gives those in tables, without token names, so the stylesheet names them and
// only their absence from a rule is testable. What is here is every token that
// document declares as CSS.
var designTokens = map[string]string{
	"--ground":        "#050705",
	"--surface":       "#0a0f0a",
	"--surface-lift":  "#101710",
	"--edge":          "#17331f",
	"--edge-bright":   "#1f5c33",
	"--text":          "#c6f7d0",
	"--dim":           "#6f9c7c",
	"--phosphor":      "#00ff41",
	"--state-running": "#00ff41",
	"--state-idle":    "#3fa85c",
	"--state-auth":    "#ffb000",
	"--state-dead":    "#ff4d4d",
	"--mono":          `ui-monospace, "SF Mono", "JetBrains Mono", "Fira Mono", Menlo, Consolas, monospace`,
	"--sans":          `ui-sans-serif, system-ui, -apple-system, "Segoe UI", Roboto, sans-serif`,
	"--s1":            ".25rem",
	"--s2":            ".5rem",
	"--s3":            ".75rem",
	"--s4":            "1rem",
	"--s5":            "1.5rem",
	"--s6":            "2rem",
	"--s7":            "3rem",
	"--r":             "3px",
	"--edge-width":    "1px",
	"--transition":    ".12s ease",
}

// documentedStates is the design system's state table: the four display states
// and the token each one is coloured from. Two of them cannot occur in this
// milestone and are checked anyway — the pill has to keep working for a state
// the first time it happens, not the second.
var documentedStates = map[string]string{
	"running":    "--state-running",
	"idle":       "--state-idle",
	"needs-auth": "--state-auth",
	"dead":       "--state-dead",
}

// cssComment is what every sweep below reads past. A /* … */ comment is not a
// declaration: it cannot be a colour, a length, or an origin, and every rule in
// this stylesheet explains itself by naming the thing it must not contain.
var cssComment = regexp.MustCompile(`(?s)/\*.*?\*/`)

// mediaOpen is a media query's prelude and its opening brace. Stripping it
// leaves the rules inside readable by the same selector split as the rest of
// the file, and leaves the block's closing brace as a chunk with no selector.
var mediaOpen = regexp.MustCompile(`@media[^{]*\{`)

// cssVar is a reference to a token. Removing every one of them from a
// declaration leaves exactly the part that was hard-coded.
var cssVar = regexp.MustCompile(`var\(\s*--[a-z0-9-]+\s*(,[^()]*(\([^()]*\))?)?\)`)

func stylesheet(t *testing.T) string {
	t.Helper()

	css, err := web.Static.ReadFile("static/crswd.css")
	if err != nil {
		t.Fatalf("read the embedded stylesheet: %v", err)
	}
	return cssComment.ReplaceAllString(string(css), "")
}

// tokenBlockAndRules splits the stylesheet where the design system splits: the
// first rule declares every token, and everything after it may reference tokens
// and nothing else. The split is positional on purpose — a second token block
// further down the file is how "tokens only" becomes "tokens, mostly".
func tokenBlockAndRules(t *testing.T) (tokens, rules string) {
	t.Helper()

	source := stylesheet(t)
	open := strings.Index(source, "{")
	end := strings.Index(source, "}")
	if open < 0 || end < open {
		t.Fatal("the stylesheet has no first rule, so there are no tokens to check")
	}

	selector := strings.TrimSpace(source[:open])
	for _, theme := range []string{":root", `:root[data-theme="dark"]`, `:root[data-theme="light"]`} {
		if !strings.Contains(selector, theme) {
			t.Errorf("the token block does not match %s (%q); the design system pins one theme across every data-theme rather than leaving a toggle that half-applies them", theme, selector)
		}
	}
	return source[open+1 : end], source[end+1:]
}

// declarations reads `name: value` pairs out of one block.
func declarations(block string) map[string]string {
	out := make(map[string]string)
	for _, decl := range strings.Split(block, ";") {
		name, value, ok := strings.Cut(decl, ":")
		if !ok {
			continue
		}
		out[strings.TrimSpace(name)] = strings.TrimSpace(value)
	}
	return out
}

// TestTheTokenBlockIsTheDesignSystem is the transcription itself. Every value
// the design system declares as CSS must be declared here, spelled the same
// way: the tokens are the vocabulary, and a stylesheet missing one is a
// stylesheet whose rules had to invent something.
func TestTheTokenBlockIsTheDesignSystem(t *testing.T) {
	t.Parallel()

	tokens, _ := tokenBlockAndRules(t)
	declared := declarations(tokens)

	for name, want := range designTokens {
		got, ok := declared[name]
		if !ok {
			t.Errorf("the token block declares no %s; docs/design-system.md does", name)
			continue
		}
		if got != want {
			t.Errorf("%s is %q; docs/design-system.md says %q", name, got, want)
		}
	}
}

// forbiddenInRules is Principle VII below the token block: a value that stopped
// being a token. The colour keywords are here because a hex sweep alone reads
// `color: white` as clean, and a palette with one keyword in it is no longer
// the palette that was measured for contrast.
var forbiddenInRules = []struct {
	what    string
	pattern *regexp.Regexp
}{
	{"a hard-coded colour", regexp.MustCompile(`#[0-9a-fA-F]{3}`)},
	{"a colour function", regexp.MustCompile(`(?i)\b(rgba?|hsla?)\(`)},
	// Bounded by punctuation on both sides rather than by \b, so a keyword only
	// matches where a value can be: `white-space` is a property, not a colour.
	{"a colour keyword", regexp.MustCompile(`(?i)[:,(\s](white|black|red|green|blue|silver|gray|grey|yellow|orange|purple|magenta|cyan|lime|navy|teal|olive|maroon|aqua|fuchsia)[;,)\s}]`)},
	{"a hard-coded length", regexp.MustCompile(`\d+(\.\d+)?(px|rem|em|pt|ch|ex|vh|vw)\b`)},
	{"an external origin", regexp.MustCompile(`(?i)https?:|@import|url\(`)},
}

// TestNoRuleCarriesAValueThatBelongsInAToken is FR-023 for the one file where
// the values are allowed to exist at all. Everything after the token block is
// swept, with the media preludes removed first: a media query is evaluated
// before custom properties resolve, so the breakpoint's length cannot be a
// token and is held by its own test instead.
func TestNoRuleCarriesAValueThatBelongsInAToken(t *testing.T) {
	t.Parallel()

	_, rules := tokenBlockAndRules(t)
	swept := mediaOpen.ReplaceAllString(rules, "")

	for _, forbidden := range forbiddenInRules {
		if match := forbidden.pattern.Find([]byte(swept)); match != nil {
			t.Errorf("a rule in crswd.css carries %s (%q); it belongs in the token block, and if the value is not in docs/design-system.md it belongs there first", forbidden.what, match)
		}
	}
}

// fontDecl matches both spellings of a family: the property and the shorthand
// that also sets one.
var fontDecl = regexp.MustCompile(`(?i)(?:^|[;{\s])(font|font-family)\s*:\s*([^;}]+)`)

// TestNoRuleNamesAFontFace is the font half of the same rule, which cannot be a
// plain "no font-family below the token block" sweep: referencing --mono or
// --sans is the only correct way to set one, so what is forbidden is a family
// that is not a token. A literal face would also be a webfont this daemon's
// policy forbids serving, and would fall back silently in a browser rather than
// fail in review.
func TestNoRuleNamesAFontFace(t *testing.T) {
	t.Parallel()

	_, rules := tokenBlockAndRules(t)

	for _, decl := range fontDecl.FindAllStringSubmatch(rules, -1) {
		property, value := decl[1], strings.TrimSpace(decl[2])
		if !strings.Contains(value, "var(--") {
			t.Errorf("%s: %s references no token", property, value)
			continue
		}
		if literal := cssVar.ReplaceAllString(value, ""); strings.ContainsAny(literal, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ\"'") {
			t.Errorf("%s: %s carries %q outside a token", property, value, strings.TrimSpace(literal))
		}
	}
}

// TestTheFocusRingSurvives is FR-027 and SC-010 at the only place a stylesheet
// can hold them. The ring is the design system's rule verbatim, and the second
// half matters as much as the first: an outline removed without a replacement
// is how a keyboard-operable interface stops being one.
func TestTheFocusRingSurvives(t *testing.T) {
	t.Parallel()

	source := stylesheet(t)
	ring := blockFor(t, source, ":focus-visible")
	for _, want := range []string{"outline:", "var(--phosphor)", "outline-offset:"} {
		if !strings.Contains(ring, want) {
			t.Errorf(":focus-visible does not set %s: %q", want, ring)
		}
	}
	if match := regexp.MustCompile(`(?i)outline\s*:\s*(none|0)\b`).FindString(source); match != "" {
		t.Errorf("crswd.css carries %q; the design system permits removing the outline only by replacing it", match)
	}
}

// TestTheDashboardHasExactlyOneBreakpoint holds the number as well as the
// value. Two breakpoints is one more than one operator on a laptop and a phone
// needs, and each additional one is a layout nobody looks at until it is wrong.
func TestTheDashboardHasExactlyOneBreakpoint(t *testing.T) {
	t.Parallel()

	widths := regexp.MustCompile(`\((?:max|min)-width\s*:\s*([^)]+)\)`).FindAllStringSubmatch(stylesheet(t), -1)
	if len(widths) != 1 {
		t.Fatalf("crswd.css has %d width breakpoints; docs/design-system.md gives it one", len(widths))
	}
	if got := strings.TrimSpace(widths[0][1]); got != "780px" {
		t.Errorf("the breakpoint is at %s; docs/design-system.md puts it at 780px", got)
	}
}

// TestReducedMotionRemovesTheRain is SC-011. Removed entirely, not slowed and
// not faded: the canvas leaves the layout, so there is nothing for the shared
// animation loop to draw into and the message it sat behind is what remains.
func TestReducedMotionRemovesTheRain(t *testing.T) {
	t.Parallel()

	reduced := blockFor(t, stylesheet(t), "@media (prefers-reduced-motion: reduce)")
	rain := blockFor(t, reduced, ".rain")
	if !regexp.MustCompile(`(?i)display\s*:\s*none`).MatchString(rain) {
		t.Errorf("a reduced-motion preference leaves the rain canvas rendering: %q", rain)
	}
}

// transitionDecl is a declaration that animates a property change, and its
// value.
var transitionDecl = regexp.MustCompile(`(?i)transition\s*:\s*([^;}]+)`)

// TestReducedMotionStopsEveryTransition is the rest of SC-011. The rain is the
// page's only vestibular hazard and the test above already removes it; the two
// hover fades are 0.12s colour changes and not the motion WCAG 2.3.3 is about,
// so this is not the accessibility failure it would look like.
//
// What it holds is that the answer stops being a judgement call. A reset under
// the universal selector means "does the preference cover everything?" is read
// off the stylesheet rather than argued from the list of rules that happen to
// transition today — and the next hover fade someone writes is covered by the
// rule already being there rather than by their remembering this block exists.
func TestReducedMotionStopsEveryTransition(t *testing.T) {
	t.Parallel()

	source := stylesheet(t)
	if len(transitionDecl.FindAllString(source, -1)) == 0 {
		t.Fatal("crswd.css transitions nothing at all, so there is nothing here for the preference to answer")
	}

	reduced := blockFor(t, source, "@media (prefers-reduced-motion: reduce)")

	var universal string
	for _, chunk := range strings.Split(reduced, "}") {
		selector, body, ok := strings.Cut(chunk, "{")
		if !ok {
			continue
		}
		for _, part := range strings.Split(selector, ",") {
			if strings.TrimSpace(part) == "*" {
				universal = body
			}
		}
	}
	if universal == "" {
		t.Fatalf("the reduced-motion block carries no universal rule: %q", reduced)
	}
	if !regexp.MustCompile(`(?i)transition\s*:\s*none`).MatchString(universal) {
		t.Errorf("the reduced-motion block's universal rule does not set transition: none, so a rule elsewhere in the file still animates under the preference: %q", universal)
	}

	// And nothing inside the block puts a duration back. Shortening a transition
	// is the answer this rule exists instead of: a preference asking for no
	// motion is not asking for less of it.
	for _, decl := range transitionDecl.FindAllStringSubmatch(reduced, -1) {
		if value := strings.TrimSpace(decl[1]); !strings.EqualFold(value, "none") {
			t.Errorf("the reduced-motion block sets transition: %s; the preference is answered by removing the transition, not by shortening it", value)
		}
	}
}

// TestEveryDocumentedStateHasARule is the design system's state table held as
// code. needs-auth and dead render in no milestone this list reaches, and the
// pill is the component that must not need editing when they arrive.
func TestEveryDocumentedStateHasARule(t *testing.T) {
	t.Parallel()

	source := stylesheet(t)
	for state, token := range documentedStates {
		rule := blockFor(t, source, ".pill-"+state)
		if !strings.Contains(rule, "var("+token+")") {
			t.Errorf(".pill-%s does not colour itself from %s: %q", state, token, rule)
		}
	}
}

// classAttr is a rendered class list.
var classAttr = regexp.MustCompile(`class="([^"]*)"`)

// templateAction is a class composed at render time — the status pill's own is
// the only one in the tree. It collapses to a marker rather than being dropped,
// because `{{ . }}` holds spaces and dropping it would split one class name
// into several, and rather than the whole attribute being skipped, so the
// static class beside it is still checked.
var templateAction = regexp.MustCompile(`(?s)\{\{.*?\}\}`)

const composedClass = "\x00"

// actionFragments is the markup a route writes for itself rather than rendering.
//
// Every other byte a browser is handed comes out of web/templates, and the
// sweeps below walk that tree. What is left here is what no template can answer:
// the uniform not-found, which is written when there is no record to render a
// card from and must stay byte-identical whichever of its three causes applied.
//
// It held nine entries until T014. The other eight were the four routes' own
// answers, composed in Go because each one replaced the card it acted on — and
// they are now outcome codes the fleet renders through partials/outcome.html,
// which this walk already covers. Anything a route writes without a template
// still owes this map its answer, or the class it carries is styled by a rule
// "no template renders" in one direction and served unstyled in the other, and
// both failures look like the opposite mistake.
var actionFragments = map[string][]byte{
	"the action not-found": bodyActionNotFound,
}

// renderedClasses is every class name the embedded templates put in the markup,
// plus every one the action routes write themselves.
func renderedClasses(t *testing.T) map[string]string {
	t.Helper()

	out := make(map[string]string)
	fromTemplates := 0

	err := fs.WalkDir(web.Templates, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		source, err := fs.ReadFile(web.Templates, p)
		if err != nil {
			return err
		}
		markup := templateAction.ReplaceAll(templateComment.ReplaceAll(source, nil), []byte(composedClass))
		for _, attr := range classAttr.FindAllStringSubmatch(string(markup), -1) {
			for _, name := range strings.Fields(attr[1]) {
				if strings.Contains(name, composedClass) {
					continue
				}
				out[name], fromTemplates = p, fromTemplates+1
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk the embedded template tree: %v", err)
	}
	if fromTemplates == 0 {
		t.Fatal("the templates render no class at all, so this comparison asserts nothing")
	}

	// Counted before the fragments are folded in, so a template tree that stopped
	// rendering classes altogether still fails above rather than being covered by
	// whatever a route composed in Go.
	//
	// There is no vacuity guard on the fold itself since T014, and its absence is
	// deliberate: the eight bodies that used to carry `card-outcome` are outcome
	// codes now, and the one body left is the uniform not-found, which carries no
	// class at all. A guard asserting that some Go-composed body is styled would be
	// asserting something this door is no longer meant to do. The fold stays so
	// that the next route to write markup without a template is swept the moment it
	// is added to the map above.
	for what, fragment := range actionFragments {
		for _, attr := range classAttr.FindAllStringSubmatch(string(fragment), -1) {
			for _, name := range strings.Fields(attr[1]) {
				out[name] = what
			}
		}
	}
	return out
}

// styledClasses is every class name the stylesheet selects on.
func styledClasses(t *testing.T) map[string]bool {
	t.Helper()

	out := make(map[string]bool)
	name := regexp.MustCompile(`\.(-?[a-zA-Z_][\w-]*)`)
	for _, chunk := range strings.Split(mediaOpen.ReplaceAllString(stylesheet(t), ""), "}") {
		selector, _, ok := strings.Cut(chunk, "{")
		if !ok {
			continue
		}
		for _, match := range name.FindAllStringSubmatch(selector, -1) {
			out[match[1]] = true
		}
	}
	return out
}

// TestTheStylesheetAndTheMarkupNameTheSameThings is what keeps a rendered
// dashboard styled and a stylesheet honest, in both directions.
//
// A class the templates render with no rule is the unstyled page this task
// exists to fix, and it is invisible in a Go test that only reads markup. A
// rule no template renders is worse: docs/components.md's whole premise is that
// there is one card and one pill, and the cheapest way to get a second is to
// style a class nobody renders and wait for someone to reach for it.
//
// The state pills are the documented exception in the second direction. Their
// class is composed at render time from the display state, so no template
// carries the literal, and two of the four cannot occur yet by design.
func TestTheStylesheetAndTheMarkupNameTheSameThings(t *testing.T) {
	t.Parallel()

	rendered, styled := renderedClasses(t), styledClasses(t)

	for name, file := range rendered {
		if !styled[name] {
			t.Errorf("%s renders class %q and crswd.css has no rule for it; the browser gets unstyled markup", file, name)
		}
	}
	for name := range styled {
		if rendered[name] != "" {
			continue
		}
		if state := strings.TrimPrefix(name, "pill-"); state != name && documentedStates[state] != "" {
			continue
		}
		t.Errorf("crswd.css styles %q and no template renders it; a rule for markup that does not exist is how a second component starts", name)
	}
}

// TestThePageLinksTheStylesheetThatIsEmbedded ties the one reference to the one
// asset. They are in different trees under web/, so a rename in either is
// silent — and the symptom is the unstyled dashboard rather than a failure.
func TestThePageLinksTheStylesheetThatIsEmbedded(t *testing.T) {
	t.Parallel()

	page, err := fs.ReadFile(web.Templates, "templates/dashboard.html")
	if err != nil {
		t.Fatalf("read the embedded fleet page: %v", err)
	}
	if !strings.Contains(string(page), `href="/static/crswd.css"`) {
		t.Error("the fleet page links no stylesheet at the path web/static/ embeds it under")
	}
}

// jsComment is what the sweeps below read past, for the reason cssComment
// exists: a comment is not code, and every rule this script follows is written
// beside it by naming the thing it must not do — so a sweep that read the prose
// would fail on the file explaining why it passes.
var jsComment = regexp.MustCompile(`(?s)/\*.*?\*/|//[^\n]*`)

// script is the other file in the static tree, with its prose removed. Nothing
// here executes it — Go cannot — so every claim below is about the bytes a
// browser is handed, which is the same footing the stylesheet's assertions stand
// on.
func script(t *testing.T) string {
	t.Helper()

	js, err := web.Static.ReadFile("static/crswd.js")
	if err != nil {
		t.Fatalf("read the embedded script: %v", err)
	}
	return jsComment.ReplaceAllString(string(js), "")
}

// TestTheRainLoopDrawsWithTokensAndNothingElse is Principle VII on the one
// surface a stylesheet cannot reach.
//
// A canvas is painted from strings in a script, so the design system's first
// non-negotiable — a value not in that document does not exist — has no CSS to be
// swept out of. The loop reads the tokens back out of the stylesheet at runtime
// instead, and this holds both halves of that: no literal value here, and every
// token it names really declared over there. A renamed token is the failure worth
// catching, because `getPropertyValue` answers a missing one with an empty
// string and an empty `fillStyle` is *ignored* — the rain would go on drawing in
// whatever colour it used last, which looks like a design decision.
func TestTheRainLoopDrawsWithTokensAndNothingElse(t *testing.T) {
	t.Parallel()

	source := script(t)
	for _, forbidden := range []struct {
		what    string
		pattern *regexp.Regexp
	}{
		{"a hard-coded colour", regexp.MustCompile(`#[0-9a-fA-F]{3}`)},
		{"a colour function", regexp.MustCompile(`(?i)\b(rgba?|hsla?)\(`)},
		{"an external origin", regexp.MustCompile(`(?i)https?://`)},
	} {
		if match := forbidden.pattern.FindString(source); match != "" {
			t.Errorf("crswd.js carries %s (%q); the value belongs in a token, and in docs/design-system.md before that", forbidden.what, match)
		}
	}

	declared := declarations(func() string { tokens, _ := tokenBlockAndRules(t); return tokens }())
	for _, name := range regexp.MustCompile(`--[a-z0-9-]+`).FindAllString(source, -1) {
		if _, ok := declared[name]; !ok {
			t.Errorf("crswd.js reads %s and crswd.css declares no such token; a missing one resolves to nothing and the canvas keeps its last colour", name)
		}
	}
}

// TestTheRainLoopIsTheEffectTheDesignSystemDescribes holds the rules that
// document states in prose, at the only place they exist as code.
//
// The forbidden half is the load-bearing one. `clearRect` would erase the trail
// that *is* the effect; `innerHTML` is the assignment docs/security.md forbids
// outright on this door's client half, and it holds for the whole file because
// everything a Claude session prints now arrives through it — the pane's own
// rules are below.
func TestTheRainLoopIsTheEffectTheDesignSystemDescribes(t *testing.T) {
	t.Parallel()

	source := script(t)
	required := map[string]string{
		"requestAnimationFrame":  "the design system asks for one shared loop across every .rain canvas",
		"canvas.rain":            "the loop draws into the canvases the rain partial renders, and nothing else",
		"prefers-reduced-motion": "the preference removes the rain entirely rather than slowing it",
		"--rain-wipe":            "the field is wiped with the translucent fill the design system names",
	}
	for want, why := range required {
		if !strings.Contains(source, want) {
			t.Errorf("crswd.js does not mention %q: %s", want, why)
		}
	}

	forbidden := map[string]string{
		"clearRect": "the trail behind each lead glyph is the effect; the field is wiped with a translucent fill",
		"innerHTML": "docs/security.md permits textContent only — everything a session prints reaches this file",
		"eval(":     "the policy this daemon sends carries no unsafe-eval, so it would be refused by the browser",
	}
	for what, why := range forbidden {
		if strings.Contains(source, what) {
			t.Errorf("crswd.js carries %q: %s", what, why)
		}
	}
}

// TestTheStreamClientReplacesTheScreenWithText is the browser half of the
// project's one XSS surface, held at the file the assignment lives in.
//
// Go cannot execute this, so the claims are about the bytes a browser is handed
// — the same footing every other assertion in this file stands on. What makes
// them worth making anyway is that the server half is already proved elsewhere:
// the wire carries a JSON string of the whole screen (stream_test.go), and this
// is the only place that says what is done with it once it arrives.
//
// The sinks sweep is the strong claim. A list of forbidden spellings is a list
// somebody has to keep up to date; asserting that *every* content assignment in
// the file is `textContent =` fails on the next sink somebody invents, and on
// the `+=` that would quietly turn a repainting screen into an appended
// transcript (FR-031a).
func TestTheStreamClientReplacesTheScreenWithText(t *testing.T) {
	t.Parallel()

	source := script(t)
	required := map[string]string{
		"EventSource":   "the live half attaches to the stream the pane's hook names (contracts/stream.md)",
		"JSON.parse":    "each event carries the whole screen as one JSON string, so parsing is the framing",
		"scrollLeft":    "FR-032 in the axis a screen wider than the pane scrolls in, as well as the obvious one",
		"textContent =": "the parsed value reaches the document as text and by no other route",
	}
	for want, why := range required {
		if !strings.Contains(source, want) {
			t.Errorf("crswd.js does not carry %q: %s", want, why)
		}
	}

	// The file has to *look for* the hook, and this is the direction the whole
	// task can be lost in silently: a stream client that is written, correct, and
	// never called is exactly the failure this plan warns about — the code exists
	// and nothing calls it. A query naming the attribute the pane renders is as
	// close to "something calls it" as a language Go cannot execute allows.
	// No backreference for the quote: RE2 has none, and a selector spelled with
	// one quote and closed with the other is not a thing this file can hold.
	query := regexp.MustCompile(`querySelectorAll\(\s*['"][^'"]*data-stream[^'"]*['"]\s*\)`)
	if query.FindString(source) == "" {
		t.Error("crswd.js never queries the document for a pane carrying data-stream, so no pane is ever attached to its stream")
	}

	sink := regexp.MustCompile(`\.\s*(innerHTML|outerHTML|innerText|textContent|nodeValue|srcdoc)\s*(\+?=)[^=]`)
	writes := sink.FindAllStringSubmatch(source, -1)
	if len(writes) == 0 {
		t.Fatal("crswd.js puts content into no element at all, so nothing here is updating a pane")
	}
	for _, write := range writes {
		if write[1] != "textContent" || write[2] != "=" {
			t.Errorf("crswd.js writes .%s %s …; textContent = is the one permitted spelling (docs/security.md, FR-031a)", write[1], write[2])
		}
	}

	// FR-032 is an order and not a mention. An offset read *after* the screen was
	// replaced is the offset the browser already clamped against an empty box, so
	// restoring it would put the operator back at the top of a screen they had
	// scrolled through — the yank the requirement is about, performed carefully.
	read := strings.Index(source, "= pane.scrollTop")
	replace := strings.Index(source, "pane.textContent =")
	restore := strings.Index(source, "pane.scrollTop =")
	switch {
	case read < 0 || replace < 0 || restore < 0:
		t.Fatalf("crswd.js does not read the scroll offset (%d), replace the screen (%d) and put the offset back (%d)", read, replace, restore)
	case read > replace || replace > restore:
		t.Errorf("crswd.js reads the scroll offset at %d, replaces the screen at %d and restores at %d; the read has to come first and the restore last, or the offset restored is the clamped one", read, replace, restore)
	}
}

// TestTheStreamClientClosesWhenTheSessionEnds is the client half of the
// lifecycle: the daemon's terminal event, and the two things that must happen
// when it arrives.
//
// The close is the load-bearing one, and it is not politeness. An EventSource
// left open reconnects for as long as the tab lives, and every reconnection
// after the session ended is a request the daemon answers with the uniform 404 —
// the dashboard turned into a scanner of its own daemon, by the operator's own
// browser, using the operator's own identity.
//
// The note is FR-033: a view that stopped updating in silence is one an operator
// reads as a session that has gone quiet.
func TestTheStreamClientClosesWhenTheSessionEnds(t *testing.T) {
	t.Parallel()

	source := script(t)

	// The named event, not `onmessage`. A screen is an unnamed event whatever it
	// contains, so listening by name is what stops a session ending its own stream
	// by printing the bytes of one.
	named := regexp.MustCompile(`addEventListener\(\s*['"]end['"]`)
	if named.FindString(source) == "" {
		t.Error("crswd.js listens for no `end` event, so a stream the daemon ended is a stream that simply stopped")
	}
	if !strings.Contains(source, ".close()") {
		t.Error("crswd.js never closes the EventSource; without it a browser reconnects into the uniform 404 for as long as the tab lives")
	}
	if !strings.Contains(source, "dataset.ended") {
		t.Error("crswd.js never reads the pane's end-note hook, so nothing on the page says the session ended (FR-033)")
	}

	// And the screen the session last printed is left where it is. One assignment
	// in the whole file is what says that: a second would be the end handler
	// replacing the last screen with a sentence, throwing away the most useful
	// thing on the page at the moment it became the only thing on it.
	if wrote := strings.Count(source, "pane.textContent ="); wrote != 1 {
		t.Errorf("crswd.js writes the pane's content %d times; want exactly 1 — the ending is said beside the screen, not over it", wrote)
	}
}

// TestTheScriptSpendsASubmitOnce is research.md R7's one genuine idempotence
// exposure, held at the file that closes it.
//
// Three of the four actions are idempotent by their own semantics: a second
// destroy finds no record, a second rename is the same end state, and a second
// compact is a second delivery the operator asked for. A second create is a
// second unsandboxed shell, and nothing about the response tells the operator
// afterwards that they made two.
//
// Go cannot execute this, so the claims are about the bytes a browser is handed
// — the same footing every other assertion in this file stands on. What makes
// them worth making is the direction the whole guard can be lost in silently: a
// handler that is written, correct, and attached to nothing. A query naming the
// attribute the form renders is as close to "something calls it" as a language
// Go cannot execute allows, and it is the same shape the pane's own hook is held
// by above.
func TestTheScriptSpendsASubmitOnce(t *testing.T) {
	t.Parallel()

	source := script(t)

	query := regexp.MustCompile(`querySelectorAll\(\s*['"][^'"]*data-submit-once[^'"]*['"]\s*\)`)
	if query.FindString(source) == "" {
		t.Error("crswd.js never queries the document for a form carrying data-submit-once, so no submit is ever spent and a double-click on the create is two sessions")
	}
	if !regexp.MustCompile(`addEventListener\(\s*['"]submit['"]`).MatchString(source) {
		t.Error("crswd.js listens for no submit event; a click handler would fire before the browser's own constraint validation and spend the control on a submission that never happened")
	}
	if !regexp.MustCompile(`\.disabled\s*=\s*true`).MatchString(source) {
		t.Error("crswd.js disables nothing on submission, which is what contracts/actions.md asks the create's control to do to itself")
	}

	// The in-progress state beside it (FR-031). A control that greys out in
	// silence reads as a page that has broken, which is the state an operator
	// answers by clicking again.
	if !strings.Contains(source, "dataset.submitOnce") {
		t.Error("crswd.js never reads the hook naming the in-progress note, so a spent control says nothing about why (FR-031)")
	}
}

// TestTheToastReadsTheBannerTheDaemonRenders is FR-024 held as code: the in-page
// behaviour survives T014's redirect rather than being replaced by it.
//
// The four actions answer 303 now, so a script that posts one gets the fleet the
// browser would have landed on. The sentence it shows has to come out of that
// page's banner — the same closed vocabulary, rendered by the same handler — or
// the scripted half and the scriptless half start telling an operator two
// different things about one action.
//
// **Must fail when** the script goes on looking for the fragments the routes used
// to write. That is the drift with no symptom in Go and an obvious one in a
// browser: every click answers "the host answered without a message", or worse,
// reads the whole fleet aloud into a corner of the page (#78). The class names
// are the joint, so they are asserted from both sides — here against the script,
// and against the template tree by the sweep at the top of this file.
//
// Go cannot execute this, so the claims are about the bytes a browser is handed,
// which is the footing every other script assertion in this file stands on.
func TestTheToastReadsTheBannerTheDaemonRenders(t *testing.T) {
	t.Parallel()

	source := script(t)

	for selector, why := range map[string]string{
		".outcome":         "an ordinary outcome is the line the fleet renders, and nothing else on that page is the answer to what the operator just did",
		".outcome-alarm":   "a teardown that could not be verified is a block rather than a line, and reading it as one would flatten the one outcome FR-023 keeps prominent",
		".outcome-heading": "the alarming outcome's heading is half of what it says; a toast carrying only the body drops the sentence an operator scans for",
		".outcome-body":    "the alarming outcome's body is the other half",
	} {
		if !regexp.MustCompile(`querySelector\(\s*['"]` + regexp.QuoteMeta(selector) + `['"]\s*\)`).MatchString(source) {
			t.Errorf("crswd.js never queries %q: %s", selector, why)
		}
	}

	// And not the fragments T014 deleted. A file still reaching for them is a
	// file whose toast has been silently broken since the routes started
	// redirecting.
	for _, gone := range []string{".card-outcome", "'Session started.'", `"Session started."`} {
		if strings.Contains(source, gone) {
			t.Errorf("crswd.js still reaches for %s, which no route writes since the actions began answering 303", gone)
		}
	}

	// textContent and never innerHTML, which is the rule that outlives the class
	// names: the banner is daemon-authored today, and the toast has to stay safe
	// after someone makes an outcome carry a name or a path.
	if strings.Contains(source, ".innerHTML") {
		t.Error("crswd.js assigns innerHTML; the answer is parsed into an inert document and read as text, which is what keeps a name a caller typed out of this page")
	}
}

// TestTheFleetClientSubscribesAndSaysWhenItStops is the script half of US3, and
// the half TestStreamLossIsVisible cannot see: that test proves the fleet page
// carries a prepared sentence about a stream that stopped, and markup nothing
// ever reveals is exactly as silent as no markup at all.
//
// Go cannot execute this, so the claims are about the bytes a browser is handed —
// the same footing every other assertion in this file stands on. What makes them
// worth making is the direction this whole task can be lost in silently: a live
// half that is written, correct, and attached to nothing. This repository has
// shipped that failure three times, and the symptom here is the one issue #15
// reported in the first place — an open dashboard that never changes.
//
// The names are the load-bearing part. The daemon says which of three things
// happened in the event's own name and puts one identifier in the data
// (contracts/fleet-stream.md), so a client reading onmessage would receive every
// change as the same undifferentiated thing and could not tell a session that
// arrived from one that is gone.
func TestTheFleetClientSubscribesAndSaysWhenItStops(t *testing.T) {
	t.Parallel()

	source := script(t)

	query := regexp.MustCompile(`querySelectorAll\(\s*['"][^'"]*data-fleet-stream[^'"]*['"]\s*\)`)
	if query.FindString(source) == "" {
		t.Error("crswd.js never queries the document for the fleet's stream hook, so nothing subscribes and an open dashboard is as static as it was in milestone 2 (issue #15)")
	}

	for kind, why := range map[string]string{
		"appeared": "a session that entered the fleet has no card on the page yet",
		"changed":  "a session whose state or name moved is a card that now describes it wrongly",
		"vanished": "a card for a session that is gone offers a control that ends nothing",
	} {
		if !regexp.MustCompile(`addEventListener\(\s*['"]` + kind + `['"]`).MatchString(source) {
			t.Errorf("crswd.js listens for no %q event: %s", kind, why)
		}
	}

	// FR-020. The hook is read here and the copy is over in the template, for the
	// reason every other sentence this interface says is: a script that authored
	// its own prose would be a second place to look for it.
	if !strings.Contains(source, "dataset.fleetStalled") {
		t.Error("crswd.js never reads the hook naming the fleet's stalled note, so a stream that stopped says nothing and the page presents a fleet it cannot vouch for (FR-020)")
	}
	// Two error handlers and not one: the pane's and this one. Every ending of the
	// fleet stream arrives that way and none of them arrives as an event
	// (contracts/fleet-stream.md), and the two streams answer it by different
	// rules — a pane leaves a browser that is retrying alone, and a fleet cannot,
	// because the changes that happened while it retried arrive as nothing at all.
	// So a file carrying one handler has left one of the two streams ending in
	// silence.
	if n := len(regexp.MustCompile(`onerror\s*=`).FindAllString(source, -1)); n != 2 {
		t.Errorf("crswd.js attaches %d EventSource error handlers; want 2 — the pane's and the fleet's", n)
	}

	// The card comes from the daemon and is parsed into an inert document before
	// one element of it is taken. These are the sinks that would put the same
	// bytes into the live document as markup instead, and neither is caught by
	// the assignment sweep above — they are calls rather than assignments.
	for _, sink := range []string{"insertAdjacentHTML", "document.write"} {
		if strings.Contains(source, sink) {
			t.Errorf("crswd.js carries %q; the answer to a card re-fetch is parsed and one element of it imported, which is what keeps a page a session printed into out of this document", sink)
		}
	}
}

// TestTheFleetUpdatesInPlaceRatherThanReloading is issue #51, and it is the
// script half of TestTheFleetPageComposesBothOfItsShapes.
//
// The page carries both of its shapes now, so a session appearing or vanishing
// is a `hidden` attribute moved between markup the daemon authored. What this
// holds is that the script actually does that instead of what it used to do:
// throw the page away and ask for it again. A reload loses everything that is
// not in the markup — a half-typed working directory, the scroll position, the
// caret, and any message the page was showing — which is why the action toast
// had to be given sessionStorage to survive the very reload the action caused
// (issue #42).
//
// One reload survives on purpose, and the count is the assertion. It is the
// fallback for what genuinely cannot be composed here: a card that could not be
// fetched, an answer that did not carry the card it named, and a state the
// summary has no row for. A second one is this file drifting back to reloading
// by default.
//
// **Must fail when** a shape change reaches for the page again rather than for
// the markup already on it, or when the announcement that replaces the reload's
// accidental re-reading of the page goes missing.
func TestTheFleetUpdatesInPlaceRatherThanReloading(t *testing.T) {
	t.Parallel()

	source := script(t)

	if n := len(regexp.MustCompile(`location\.reload\(`).FindAllString(source, -1)); n != 1 {
		t.Errorf("crswd.js reloads the page in %d places; want exactly 1 — the fallback, never the answer to a session appearing or vanishing (issue #51)", n)
	}

	// The two shapes it switches between, both of them the daemon's own markup.
	// A script that reached for neither is one that still has to ask the server
	// which shape the page should be in.
	for shape, why := range map[string]string{
		".empty":   "the grid becomes the empty state when the last card goes, and back when the first returns",
		".summary": "the row of counts shows and hides with the grid it describes",
	} {
		if !strings.Contains(source, `'`+shape+`'`) {
			t.Errorf("crswd.js never selects %q: %s", shape, why)
		}
	}
	// The `hidden` property is how it switches. An attribute the browser owns
	// needs no rule in the stylesheet and cannot be defeated by one, which is the
	// same reason the page's own notes hide with it.
	if !regexp.MustCompile(`\.hidden\s*=`).MatchString(source) {
		t.Error("crswd.js never sets hidden on anything, so the shape the page renders is the shape it keeps until it is reloaded")
	}

	// FR-020's neighbour: the hook is read here and the copy is over in the
	// template, exactly as the stalled note's is.
	if !strings.Contains(source, "dataset.fleetChanged") {
		t.Error("crswd.js never reads the hook naming the region that says the fleet's shape changed, so a page that rearranges itself announces nothing to an operator who cannot see it (issue #51)")
	}
	// The count in that sentence comes from the cards, not from a tally this file
	// keeps — the rule the summary row already follows. A number this script
	// carried would be the second source of truth issue #51 forbids.
	if !regexp.MustCompile(`replace\(\s*['"]\{n\}['"]`).MatchString(source) {
		t.Error("crswd.js never fills the {n} the page's sentence leaves for it, so the announcement is a fixed string a reader may not announce twice")
	}
}

// TestSubsetAnnounced is FR-045, and it is as much about what this file does
// *not* do to the working-directory picker as about the sentence it adds.
//
// The control is markup (contracts/directory-picker.md): `<input list>` and a
// `<datalist>` filter as the operator types, take a keyboard, announce their
// options and leave any path typeable in full, with nothing running. Five of the
// six picker requirements are the browser's own. The sixth — saying that the
// list has been narrowed — is the one a browser does not say out loud, and it is
// an addition to a control that already works.
//
// **Must fail when** the announcement becomes the thing that makes the control
// function. That is the direction this task is most likely to be lost in by
// improvement rather than by mistake: a file that builds the options, sets the
// field's value or attaches the `list` attribute has reimplemented the combobox
// the abandoned branch was rejected for — 225 lines that degrade to nothing with
// scripting off. So the middle section is a sweep for every operation that would
// make this file load-bearing, and the last one holds the picker's markup where
// it is, in the template, where an operator running no script still gets it.
//
// Go cannot execute this, so the claims are about the bytes a browser is handed
// — the same footing every other script assertion in this file stands on.
func TestSubsetAnnounced(t *testing.T) {
	t.Parallel()

	source := script(t)

	// Something calls it. A query naming the attribute the field renders is as
	// close to that as a language Go cannot execute allows, and it is the
	// direction this whole task can be lost in silently — an announcement that is
	// written, correct, and attached to nothing.
	query := regexp.MustCompile(`querySelectorAll\(\s*['"][^'"]*data-workdir-note[^'"]*['"]\s*\)`)
	if query.FindString(source) == "" {
		t.Error("crswd.js never queries the document for a field naming its subset note, so nothing is ever announced and FR-045 is markup nobody reads")
	}
	for want, why := range map[string]string{
		"dataset.workdirNote":   "the field names the region to write into, so the script does not carry an id the template could rename out from under it",
		"dataset.workdirSubset": "the sentence is the template's; a script that authored its own prose would be a second place to look for it",
		".options":              "the count comes off the options the daemon rendered rather than a tally this file keeps — the rule the summary row already follows",
	} {
		if !strings.Contains(source, want) {
			t.Errorf("crswd.js does not carry %q: %s", want, why)
		}
	}
	// Both halves of the sentence are filled. A count with no total is a number an
	// operator cannot tell "narrowed to three" from "all three" by, which is the
	// one distinction FR-045 exists to make.
	for _, placeholder := range []string{"{n}", "{all}"} {
		if !regexp.MustCompile(`replace\(\s*['"]` + regexp.QuoteMeta(placeholder) + `['"]`).MatchString(source) {
			t.Errorf("crswd.js never fills the %s the page's sentence leaves for it", placeholder)
		}
	}

	// The addition sweep. Each of these is this file taking ownership of the
	// control rather than commenting on it, and every one of them takes the
	// picker away from a browser running no script.
	for _, forbidden := range []struct {
		pattern *regexp.Regexp
		what    string
		why     string
	}{
		{regexp.MustCompile(`(?i)datalist`), "names the datalist element", "the list is composed by the template; a file that composes one has a picker that exists only while it runs"},
		{regexp.MustCompile(`(?i)\bnew Option\(|createElement\(`), "builds an element", "an option built here is an offer the daemon never made, and one that is gone with scripting off"},
		{regexp.MustCompile(`\.value\s*=[^=]`), "assigns a value", "choosing the operator's path for them is the combobox this control was chosen instead of (FR-040)"},
		{regexp.MustCompile(`\b(set|remove)Attribute\(`), "moves an attribute", "the field is joined to its list in the markup; an attribute added here is a picker that only exists when this file runs (FR-043)"},
	} {
		if match := forbidden.pattern.FindString(source); match != "" {
			t.Errorf("crswd.js %s (%q): %s", forbidden.what, match, forbidden.why)
		}
	}

	// And the markup half, which is what the sweep above is protecting. The
	// spellings are a joint between two trees — the hook the field renders and the
	// id the script looks up — so they are asserted together here, because nothing
	// else holds them.
	form, err := fs.ReadFile(web.Templates, "templates/partials/create-form.html")
	if err != nil {
		t.Fatalf("read the embedded create form: %v", err)
	}
	markup := templateComment.ReplaceAllString(string(form), "")

	for want, why := range map[string]string{
		`data-workdir-note="create-workdir-subset"`:  "the field names the region, and this is the spelling crswd.js reads back",
		`id="create-workdir-subset"`:                 "the region the field names has to be the region that is rendered",
		`<option value="{{ . }}">`:                   "the suggestions are the template's, so the list is there for an operator running no script (FR-043)",
		`list="workdir-suggestions"`:                 "the field is joined to its list in the markup, by the same rule",
		`data-workdir-subset="Showing {n} of {all} `: "the sentence is the page's own copy, with both halves left for the script to fill",
	} {
		if !strings.Contains(markup, want) {
			t.Errorf("the create form does not carry %q: %s", want, why)
		}
	}

	// Present and empty rather than hidden. docs/components.md's accessibility
	// floor is explicit about the difference: a live region has to be in the
	// accessibility tree before its text arrives for the announcement to happen at
	// all, and a region revealed and written in one go is one some readers never
	// announce.
	region := regexp.MustCompile(`<div\b[^>]*\bid="create-workdir-subset"[^>]*>\s*</div>`).FindString(markup)
	if region == "" {
		t.Fatalf("the create form renders no empty subset region; a region composed at announcement time is one a reader may never hear:\n%s", markup)
	}
	if strings.Contains(region, " hidden") {
		t.Errorf("the subset region is rendered hidden (%s); it is empty markup that costs the field nothing, and hiding it keeps it out of the accessibility tree until the moment it has something to say", region)
	}
	if !strings.Contains(region, `role="status"`) || !strings.Contains(region, `aria-live="polite"`) {
		t.Errorf("the subset region is not a polite live region (%s), so the one thing FR-045 asks be said is said to nobody who cannot see it", region)
	}
}

// TestBoundaryIsNotColourAlone is FR-048, and it is two claims because the
// requirement is two clauses.
//
// The rule is what makes the anchor safe to grow: everything above it is what
// the session is, everything below it does something, and the card carries
// exactly one link because the halves are told apart by a boundary rather than
// by which words happen to be underlined. A card that lost it would be a block
// link with a row of buttons floating in it.
//
// The first claim is the one a screenshot cannot make. A line says nothing to a
// screen reader, and a high-contrast theme is free to drop borders — so the
// halves are separate elements in the markup, and the rule is the visual
// expression of a split that already exists. That is asserted against a rendered
// card rather than against the stylesheet, because it is the markup that has to
// carry it.
//
// The second is that what the stylesheet draws is the design system's own edge
// and is accompanied by spacing from a token. Two cues, neither of which is
// colour: a browser that never drew the border still shows two halves.
//
// **Must fail when** the split is presentational only — one block with a line
// across it — or when the line becomes the only thing dividing the card.
func TestBoundaryIsNotColourAlone(t *testing.T) {
	t.Parallel()

	card := renderComponent(t, "session-card", actionableCard())

	readable := regexp.MustCompile(`(?s)<div[^>]*\bclass="card-read"[^>]*>(.*?)</div>`).FindStringSubmatch(card)
	if readable == nil {
		t.Fatalf("the card renders no readable half of its own; a boundary between two halves needs two elements, or it is a border drawn across one:\n%s", card)
	}
	if !strings.Contains(readable[1], "<a") {
		t.Errorf("the card's readable half does not hold the anchor, so the element and the link are not the same half:\n%s", card)
	}
	if strings.Contains(readable[1], `class="card-actions"`) {
		t.Errorf("the card's action row is inside its readable half; the halves are siblings or they are not halves:\n%s", card)
	}

	rule := blockFor(t, stylesheet(t), ".card-actions")

	edge := regexp.MustCompile(`(?i)border-block-start\s*:\s*([^;}]+)`).FindStringSubmatch(rule)
	if edge == nil {
		t.Fatalf("the action row has no top edge, so nothing visible separates what a session is from what can be done to it: %q", rule)
	}
	for _, token := range []string{"var(--edge-width)", "var(--edge)"} {
		if !strings.Contains(edge[1], token) {
			t.Errorf("the rule between the card's halves is drawn as %q and does not spend %s; a boundary is the design system's own edge, not a length and a colour invented here", strings.TrimSpace(edge[1]), token)
		}
	}

	space := regexp.MustCompile(`(?i)padding-block-start\s*:\s*(var\(--s[^;}]*)`).FindStringSubmatch(rule)
	if space == nil {
		t.Errorf("the action row sets no spacing above its controls from a spacing token (%q); the line must not be the only thing dividing the card, for the reason a state is never a colour alone", rule)
	}
}

// blockFor returns the body of the first block whose prelude contains marker,
// so a test can assert about one rule rather than about the whole file. Braces
// are counted, because a media query holds rules of its own.
func blockFor(t *testing.T, source, marker string) string {
	t.Helper()

	at := strings.Index(source, marker)
	if at < 0 {
		t.Fatalf("crswd.css has no %s rule at all", marker)
	}
	open := strings.Index(source[at:], "{")
	if open < 0 {
		t.Fatalf("%s is not followed by a block", marker)
	}
	depth, start := 0, at+open
	for i := start; i < len(source); i++ {
		switch source[i] {
		case '{':
			depth++
		case '}':
			if depth--; depth == 0 {
				return source[start+1 : i]
			}
		}
	}
	t.Fatalf("%s's block is never closed", marker)
	return ""
}

// TestHiddenAlwaysWins is #77, and it is the rule the stylesheet cannot be
// trusted to keep on its own.
//
// `[hidden] { display: none }` is a user-agent rule, so any class that sets
// `display` outranks it. Four of this page's components do — .empty, .grid,
// .summary and .empty-action — and all four are hidden by *attribute*, because
// the fleet renders both of its shapes and hides the inactive one (#51). The
// empty state was rendering underneath a fleet that had sessions in it.
//
// The failure is invisible in markup review: the template says `hidden`, the
// element is hidden in the DOM sense, and it is on the screen anyway.
func TestHiddenAlwaysWins(t *testing.T) {
	t.Parallel()

	source := stylesheet(t)

	rule := regexp.MustCompile(`\[hidden\]\s*\{[^}]*display:\s*none\s*!important`)
	if !rule.MatchString(source) {
		t.Fatal("crswd.css has no [hidden] rule that outranks a class-level display; any component that sets one renders while hidden")
	}

	// And it has to be last, because specificity ties are broken by order and a
	// later `display` on a plain class would beat an earlier [hidden] without
	// !important — belt and braces, since the rule above is what actually does
	// the work.
	hiddenAt := rule.FindStringIndex(source)
	for _, m := range regexp.MustCompile(`\n\.[a-z-]+\s*\{[^}]*display:`).FindAllStringIndex(source, -1) {
		if m[0] > hiddenAt[0] {
			t.Errorf("a class sets display after the [hidden] rule (offset %d > %d); move [hidden] to the end", m[0], hiddenAt[0])
		}
	}
}
