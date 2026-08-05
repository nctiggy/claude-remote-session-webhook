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

// actionFragments is the markup an action route writes for itself.
//
// Every other byte a browser is handed comes out of web/templates, and the
// sweeps below walk that tree — but an action's answer replaces the card it
// acted on, and by the time a destroy has an answer there is no record left to
// render a card from, so those four sentences are composed in Go (actions.go).
// They are markup all the same. Without them here, a class only a fragment
// carries is styled by a rule "no template renders" in one direction and served
// unstyled in the other, and both failures look like the opposite mistake.
// A create's own four join them for the same reason, from the other direction: a
// refused create has no record to render a card from, so what the operator is
// told is composed in Go as well. Every action route this milestone adds owes
// this map its answers, or the class they carry is invisible to both sweeps.
var actionFragments = map[string][]byte{
	"the destroyed marker":            bodyActionDestroyed,
	"the unconfirmed refusal":         bodyActionUnconfirmed,
	"the unverified teardown":         bodyActionTeardownUnverified,
	"the failed destroy":              bodyActionDestroyFailed,
	"the action not-found":            bodyActionNotFound,
	"the refused session name":        bodyActionCreateBadName,
	"the refused working directory":   bodyActionCreateBadWorkDir,
	"the create refused by the bound": bodyActionCreateLimited,
	"the failed create":               bodyActionCreateFailed,
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
	// rendering classes altogether still fails here rather than being covered by
	// the four sentences a destroy answers with.
	found := 0
	for what, fragment := range actionFragments {
		for _, attr := range classAttr.FindAllStringSubmatch(string(fragment), -1) {
			for _, name := range strings.Fields(attr[1]) {
				out[name], found = what, found+1
			}
		}
	}
	if found == 0 {
		t.Fatal("no action fragment carries a class, so the outcome an operator is shown is styled by nothing")
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
