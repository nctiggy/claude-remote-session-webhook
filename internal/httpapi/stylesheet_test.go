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
	"os"
	"regexp"
	"slices"
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
	"--tap":           "44px",
	"--fs-input":      "16px",
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

// componentsDocPath is docs/components.md, which AGENTS.md names as the file to
// load before touching a control and Principle VII makes binding.
const componentsDocPath = "../../docs/components.md"

// documentedComponentClass is the classes that document is answerable for *by
// name*: the working-directory picker, the switch, and the header the settings
// link sits in — the three controls milestone 5 built or changed. Matched by
// prefix on the class itself, so a rule added for any of them is swept without
// this expression being edited.
//
// It is a near-twin of comboSelector and deliberately not the same variable.
// That one is the set of rules T009 is answerable for the *styling* of, and
// widening it to carry the masthead would quietly widen four assertions about
// colour and focus that were written about a picker.
var documentedComponentClass = regexp.MustCompile(`\.(combo|switch|masthead)[\w-]*`)

// TestTheComponentsDocumentNamesThePickerTheSwitchAndTheHeader is the third
// direction of the sweep above, and it exists because the first two cannot see
// this one. A class can be rendered and styled and still be undocumented, which
// is how a second version of a control gets built: the next person writes the
// markup they need because the vocabulary they were told to reuse does not
// mention the one that is already there.
//
// Both directions, for the same reasons the markup sweep gives. A class the
// document omits is a control someone reimplements; a class it invents is a
// spelling someone copies into a template, where it renders unstyled.
//
// The picker is the case this was written for. It carries decisions no test can
// see — `display: grid` and `position: relative` on the wrapper, a pointer bound
// to mousedown rather than click, one accept shared by two triggers — and every
// one of them is a bug the next themed control over a native one would otherwise
// ship.
//
// **Must fail when** a `.combo*`, `.switch*` or `.masthead*` rule exists that
// docs/components.md never names, or the reverse.
func TestTheComponentsDocumentNamesThePickerTheSwitchAndTheHeader(t *testing.T) {
	t.Parallel()

	// Comments stripped first, unlike cssRules' own reading: a class named in a
	// rule's commentary is prose about the stylesheet, and requiring the document
	// to carry every spelling a comment mentions would hold it to a different
	// claim than the one it makes.
	styled := make(map[string]bool)
	for _, rule := range cssRules(cssComment.ReplaceAllString(stylesheet(t), "")) {
		for _, class := range documentedComponentClass.FindAllString(rule.selector, -1) {
			styled[class] = true
		}
	}
	if len(styled) == 0 {
		t.Fatal("crswd.css styles no picker, switch or masthead class at all, so this test is checking nothing")
	}

	raw, err := os.ReadFile(componentsDocPath)
	if err != nil {
		t.Fatalf("read %s: %v", componentsDocPath, err)
	}
	named := make(map[string]bool)
	for _, class := range documentedComponentClass.FindAllString(string(raw), -1) {
		named[class] = true
	}

	for class := range styled {
		if !named[class] {
			t.Errorf("crswd.css styles %s and %s never names it; the next person told to reuse the vocabulary is not told this exists", class, componentsDocPath)
		}
	}
	for class := range named {
		if !styled[class] {
			t.Errorf("%s names %s and crswd.css styles nothing by that name; a spelling copied out of the design system renders unstyled", componentsDocPath, class)
		}
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

	// Two, and they are different in kind — which is why this counts rather than
	// forbids.
	//
	// One is the fleet's fallback: a shape this script cannot compose, where
	// asking the daemon again is the honest answer. The other is the update
	// waiter, which reloads after the daemon has restarted into a new binary —
	// not an answer to a session appearing or leaving, but the moment the page it
	// is showing genuinely stopped being current.
	//
	// **Must fail when** a third appears. A reload is how this page loses a
	// half-typed working directory, the scroll position and the caret, so each
	// one has to be argued for.
	// One reload — the fleet's fallback, for a shape this script cannot compose.
	//
	// The update waiter used to be the second. It now names where to go instead,
	// because a form that posts without being intercepted leaves the browser at
	// /dashboard/update, and reloading that address repeats it as a GET this
	// daemon has no route for: the operator's successful update ended on a
	// not-found page.
	//
	// **Must fail when** a second appears. A reload is how this page loses a
	// half-typed working directory, the scroll position and the caret.
	if n := len(regexp.MustCompile(`location\.reload\(`).FindAllString(source, -1)); n != 1 {
		t.Errorf("crswd.js reloads the page in %d places; want exactly 1 — the fleet's fallback, never the answer to a session appearing or leaving", n)
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

// createFormMarkup is the create form's own source with its prose removed, for
// the two tests below that hold a joint between this tree and the script's.
func createFormMarkup(t *testing.T) string {
	t.Helper()

	form, err := fs.ReadFile(web.Templates, "templates/partials/create-form.html")
	if err != nil {
		t.Fatalf("read the embedded create form: %v", err)
	}
	return templateComment.ReplaceAllString(string(form), "")
}

// TestSubsetAnnounced is FR-045: the one picker requirement no browser answers
// on its own. A datalist narrows silently — the popup simply has fewer rows in
// it — and an operator who cannot see it is told nothing at all.
//
// The sentence moved with T010. It was a region of its own beside the field, and
// it is now the picker's own `.combo-status`, which is where the enhancement
// writes what the list is doing. That is not tidying: the wrapper T008 shipped
// brought a second `role="status"` to this one field, and a field with two live
// regions has one that will never speak — markup shaped like a feature, which is
// what FR-018a refuses about a value and docs/components.md's accessibility
// floor refuses about an announcement.
//
// **Must fail when** the count stops coming off the options the daemon rendered,
// when either half of the sentence goes unfilled, or when the field grows a
// second live region again.
//
// Go cannot execute this, so the claims are about the bytes a browser is handed
// — the same footing every other script assertion in this file stands on.
func TestSubsetAnnounced(t *testing.T) {
	t.Parallel()

	source := script(t)

	// Something calls it. A query naming the attribute the wrapper renders is as
	// close to that as a language Go cannot execute allows, and it is the
	// direction this whole task can be lost in silently — an announcement that is
	// written, correct, and attached to nothing.
	query := regexp.MustCompile(`querySelectorAll\(\s*['"][^'"]*data-combo[^'"]*['"]\s*\)`)
	if query.FindString(source) == "" {
		t.Error("crswd.js never queries the document for a wrapper carrying data-combo, so nothing is ever announced and FR-045 is markup nobody reads")
	}
	for want, why := range map[string]string{
		"dataset.workdirSubset": "the sentence is the template's; a script that authored its own prose would be a second place to look for it",
		".combo-status":         "the announcement goes into the region the template renders for it, which is the class the stylesheet sweep holds in both trees",
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

	// And the markup half. The sentence and the region it is written into are a
	// joint between two trees — the copy the page carries and the hook the script
	// reads back — so they are asserted together, because nothing else holds them.
	markup := createFormMarkup(t)

	region := regexp.MustCompile(`<p\b[^>]*\bclass="combo-status"[^>]*>\s*</p>`).FindString(markup)
	if region == "" {
		t.Fatalf("the create form renders no empty .combo-status; a region composed at announcement time is one a reader may never hear:\n%s", markup)
	}
	for want, why := range map[string]string{
		`data-workdir-subset="Showing {n} of {all} `: "the sentence is the page's own copy, with both halves left for the script to fill",
		`role="status"`:      "the region has to be a live one, or the one thing FR-045 asks be said is said to nobody who cannot see it",
		`aria-live="polite"`: "polite, because a count queues behind whatever is being read rather than cutting into it",
	} {
		if !strings.Contains(region, want) {
			t.Errorf("the create form's status region (%s) does not carry %q: %s", region, want, why)
		}
	}
	if strings.Contains(region, " hidden") {
		t.Errorf("the status region is rendered hidden (%s); it is empty markup that costs the field nothing, and hiding it keeps it out of the accessibility tree until the moment it has something to say", region)
	}

	// One live region on this field, and this is the assertion that says so. The
	// region the sentence used to live in outlived the move as markup nothing
	// writes into — an empty `role="status"` that can never speak, which reads as
	// finished in review and is a dead announcement in a browser.
	if strings.Contains(markup, "create-workdir-subset") {
		t.Error("the create form still renders the subset note's old region; the sentence is written into .combo-status now, and a field carrying two live regions is carrying one that never speaks")
	}
	if n := strings.Count(markup, `role="status"`); n != 1 {
		t.Errorf("the create form renders %d live regions; want exactly 1 — what this field has to say about a narrowed list is one sentence, said in one place", n)
	}
}

// TestTheThemedPickerEnhancesTheNativeOne is T010, and the rule the whole
// themed-combobox contract turns on: the native control works first, and the
// theme is an enhancement over something that already functions.
//
// The reason there is an enhancement at all is the one thing a stylesheet cannot
// reach. A `<datalist>` popup is drawn by the browser and styled by no CSS in
// any engine, so the picker was the single control on this dashboard wearing the
// browser's appearance instead of this interface's — the part milestone 4's R6
// missed while being right about everything it weighed.
//
// **Must fail when** the `<datalist>` is removed rather than reused, giving the
// script a second copy of the options that can disagree with the markup, or when
// the `list` attribute is cut before the element it names has been read — which
// leaves `field.list` null, the themed box permanently empty, and every test
// above still green because the sentence it announces is about zero of zero.
//
// The sweep this replaces forbade `setAttribute`, `createElement` and the
// datalist by name. It was right about a task that was markup-only and it
// forbids the exact four operations this one is: the property worth holding is
// not which calls appear, it is that the picker is still the daemon's with this
// file absent. So what is swept for now is a second list, an id spelled here
// rather than read off the element, and the ARIA landing anywhere but on the
// elements the template rendered.
//
// Go cannot execute this, so the claims are about the bytes a browser is handed
// — the same footing every other script assertion in this file stands on.
func TestTheThemedPickerEnhancesTheNativeOne(t *testing.T) {
	t.Parallel()

	source := script(t)

	// The order, which is the one part of this that fails silently. `field.list`
	// *is* the element the `list` attribute names, and removeAttribute is
	// precisely what makes it null: read first, then cut.
	read := regexp.MustCompile(`\.list\b`).FindStringIndex(source)
	cut := regexp.MustCompile(`removeAttribute\(\s*['"]list['"]\s*\)`).FindStringIndex(source)
	switch {
	case cut == nil:
		t.Error("crswd.js never removes the field's `list` attribute, so the browser draws its own unstyleable popup over the themed one — two lists open at once saying the same thing")
	case read == nil:
		t.Error("crswd.js never reads the field's own list, so whatever it offers is not what the daemon rendered")
	case read[0] > cut[0]:
		t.Errorf("crswd.js cuts the `list` attribute at %d and reads the list it names at %d; the read has to come first, or the enhancement holds null and the themed box is empty for good", cut[0], read[0])
	}

	// The ARIA the template deliberately does not carry, added by the code that
	// makes it true. Each is the contract's own literal, because a reader is told
	// about a combobox by the exact word or not at all.
	for _, added := range []struct {
		pattern *regexp.Regexp
		what    string
		why     string
	}{
		{regexp.MustCompile(`setAttribute\(\s*['"]role['"]\s*,\s*['"]combobox['"]`), `role="combobox"`, "the field is the control, and nothing else in the tree can be"},
		{regexp.MustCompile(`setAttribute\(\s*['"]role['"]\s*,\s*['"]listbox['"]`), `role="listbox"`, "the <ul> the template renders is what the field controls"},
		{regexp.MustCompile(`setAttribute\(\s*['"]role['"]\s*,\s*['"]option['"]`), `role="option"`, "a listbox whose children are plain list items offers a reader nothing to choose between"},
		{regexp.MustCompile(`setAttribute\(\s*['"]aria-expanded['"]`), "aria-expanded", "whether the list is open is said to a reader who cannot see that it is"},
		{regexp.MustCompile(`setAttribute\(\s*['"]aria-autocomplete['"]`), "aria-autocomplete", "the field completes from a list, which is what makes it a combobox rather than a text box beside one"},
		{regexp.MustCompile(`setAttribute\(\s*['"]aria-controls['"]\s*,\s*[A-Za-z_$][\w$]*\.id`), "aria-controls, read off the listbox", "the id is the template's to rename; a spelling copied into this file is the joint that drifts unnoticed"},
	} {
		if !added.pattern.MatchString(source) {
			t.Errorf("crswd.js never sets %s: %s", added.what, added.why)
		}
	}

	// The list stays the daemon's. Every one of these is this file acquiring a
	// second copy of the options, or a second spelling of where they live.
	for _, forbidden := range []struct {
		pattern *regexp.Regexp
		what    string
		why     string
	}{
		{regexp.MustCompile(`(?i)datalist`), "names the datalist element", "the list is reached through the field's own `list` attribute; a file that queries or builds one has a second handle on the options and a second place for them to disagree"},
		{regexp.MustCompile(`(?i)\bnew Option\(`), "builds an option", "an option built here is an offer the daemon never made, and one that is gone with scripting off"},
		{regexp.MustCompile(`workdir-listbox|create-work-dir|workdir-suggestions`), "spells an id the template owns", "every id this file needs is read off an element it was handed, so renaming one in the template renames it here"},
	} {
		if match := forbidden.pattern.FindString(source); match != "" {
			t.Errorf("crswd.js %s (%q): %s", forbidden.what, match, forbidden.why)
		}
	}

	// And the removal, which is the must-fail this task was given and the one the
	// sweep above cannot see: a file that reads the options into an array of its
	// own and then takes the element out of the document has exactly the second
	// copy the arrangement exists to avoid, and it reads as tidying up. The
	// element a variable holds is not spellable in a regular expression, so what
	// is counted is every removal in the file — there is one, the fleet's own
	// card, and a second is this.
	if n := strings.Count(source, ".remove()"); n != 1 {
		t.Errorf("crswd.js removes an element from the document in %d places; want exactly 1 — the fleet's departed card. The datalist is the enhancement's data source and stays where the daemon put it, or the options this file offers are a copy free to disagree with the markup", n)
	}

	// And the markup half, which is what all of the above is protecting: the
	// control an operator meets with this file absent is the one the daemon
	// rendered, joined to its own options, exactly as it was before any of this
	// existed (FR-015, FR-043).
	markup := createFormMarkup(t)
	for want, why := range map[string]string{
		`list="workdir-suggestions"`: "the field is joined to its list in the markup, so a browser running no script still filters",
		`<option value="{{ . }}">`:   "the suggestions are the template's, and they are the enhancement's one data source as well",
		`class="combo-list"`:         "the listbox is rendered rather than composed, or the stylesheet sweep reads its rules as dead",
	} {
		if !strings.Contains(markup, want) {
			t.Errorf("the create form does not carry %q: %s", want, why)
		}
	}
}

// TestComboKeyboardOperable is T011 and the last row of the themed-combobox
// contract's own table: the arrows move the active option, Enter accepts,
// Escape dismisses, Tab leaves, and `aria-activedescendant` follows.
//
// The requirement underneath all four is the one this control is likeliest to
// be improved past. **Must fail when** the listbox constrains what can be typed
// — any path must remain typeable in full (FR-008), and the listbox offers
// rather than restricts. A keyboard handler is exactly where that is lost: an
// inline completion that writes the nearest match into the field as the
// operator types, an Escape that reverts what they had, a blur that normalises
// it to something on the list. Every one of those looks like a better picker
// and every one of them makes a path that is not on the list unreachable, on a
// field whose whole point is that the allowlist is the daemon's and the list is
// a convenience. So the floor held below is the field's own value: it is
// written in exactly one place, it is written from an option the operator
// accepted, and nothing else in this file touches it. The markup half — no
// `pattern`, no `<select>`, still `type="text"` — is TestAnyPathStillTypeable's.
//
// The second requirement is T009's ring, which is keyed on
// `[aria-selected="true"]` because `:focus-visible` can never reach an option.
// Nothing wore it until this task, so the attribute and the rule are asserted
// together: a ring drawn for a selector no script sets is a keyboard operator
// moving a cursor nothing shows.
//
// Go cannot execute this, so the claims are about the bytes a browser is handed
// — the same footing every other script assertion in this file stands on.
func TestComboKeyboardOperable(t *testing.T) {
	t.Parallel()

	source := script(t)

	// Most of what follows is asserted about the picker's own block rather than
	// the whole file, because the words it turns on are ordinary ones: the toast
	// hides itself with `hidden = true` and both it and the card's selection fix
	// call preventDefault. The block runs from the picker's one constant to the
	// query that applies the enhancement, which is all of it and none of anything
	// else.
	from := strings.Index(source, "SETTLE_MS")
	to := strings.Index(source, "data-combo")
	if from < 0 || to <= from {
		t.Fatalf("crswd.js has no picker block between its settle constant (%d) and the wrapper query (%d); the enhancement is where the keyboard lives", from, to)
	}
	picker := source[from:to]

	if !regexp.MustCompile(`addEventListener\(\s*['"]keydown['"]`).MatchString(picker) {
		t.Fatal("the picker listens for no keydown, so the themed listbox is a box a keyboard operator can see and never reach — the native popup this replaces was operable, and an enhancement that costs behaviour is not one")
	}

	// The four keys, each by the name the platform gives it. A handler switching
	// on keyCode or on a spelling of its own answers a key nobody pressed.
	for key, why := range map[string]string{
		"'ArrowDown'": "down is the way into the list, and the key an operator reaches for first",
		"'ArrowUp'":   "a list that can only be moved down is one an operator has to close and reopen to correct a step",
		"'Enter'":     "the accept, and the only thing in this file permitted to write the field's value",
		"'Escape'":    "the dismissal, which leaves what was typed exactly where it is",
		"'Tab'":       "leaving the field closes the list, or it hangs over whatever now has focus",
	} {
		if !strings.Contains(picker, key) {
			t.Errorf("the picker answers no %s: %s", key, why)
		}
	}

	// Where the keyboard is, as the two attributes that say so — one for the
	// reader, one for the ring. Both are cleared when there is nothing active,
	// which is every redraw: an id left pointing at an option the last filter
	// removed is a reader told about an element that is gone.
	for _, wanted := range []struct {
		pattern *regexp.Regexp
		what    string
		why     string
	}{
		{regexp.MustCompile(`setAttribute\(\s*['"]aria-activedescendant['"]\s*,\s*[A-Za-z_$][\w$]*\.id`), "aria-activedescendant, read off the option", "focus never leaves the input, so this attribute is the whole of what tells a reader which option the arrows are on — and the id is the element's own, not a spelling this file keeps"},
		{regexp.MustCompile(`setAttribute\(\s*['"]aria-selected['"]\s*,\s*['"]true['"]`), `aria-selected="true"`, "that is the selector the active option's ring is keyed on, and nothing else can wear it"},
		{regexp.MustCompile("\\.id\\s*=\\s*`[^`]*\\$\\{[A-Za-z_$][\\w$]*\\.id\\}"), "an id on each option, built from the listbox's", "aria-activedescendant names one element by id and can point at nothing else, and an id spelled here is the joint that drifts when the template renames one"},
		{regexp.MustCompile(`\.hidden\s*=\s*true`), "a close path", "before this task the list shut only when nothing matched, so one opened by a keystroke stayed open over the rest of the form"},
	} {
		if !wanted.pattern.MatchString(picker) {
			t.Errorf("the picker never sets %s: %s", wanted.what, wanted.why)
		}
	}

	// And the redraw clears it, which is asserted where it happens rather than
	// anywhere in the block. Every keystroke replaces every option, and the ids
	// are positional — so an attribute that outlived its filter does not dangle,
	// it points at whatever path is in that position now, while the ring is on
	// nothing. That is a reader told one thing and an operator shown another, and
	// a clear on the close path alone reads as covering it.
	rebuild := strings.Index(picker, "replaceChildren")
	listener := strings.Index(picker, "addEventListener")
	if rebuild < 0 || listener <= rebuild {
		t.Fatalf("the picker rebuilds no listbox between %d and %d; the options are what the keyboard moves over", rebuild, listener)
	}
	if !strings.Contains(picker[rebuild:listener], "aria-activedescendant") {
		t.Error("the picker rebuilds its options without clearing aria-activedescendant; the ids are positional, so an attribute left over from the last filter names whichever path now sits in that position — announced as active while the ring is on nothing")
	}

	// The field's own value, which is the floor. One assignment, from an option's
	// own text — so there is no completion writing the nearest match into the
	// field as it is typed, no Escape putting back what was there before, and no
	// path by which the list narrows what may be submitted.
	assigns := regexp.MustCompile(`\.value\s*=[^=]`).FindAllStringIndex(source, -1)
	if len(assigns) != 1 {
		t.Fatalf("crswd.js writes a field's value in %d places; want exactly 1 — the accept the operator asked for. Any second one is this file deciding what the operator meant to type, on the field FR-008 says any path stays typeable in", len(assigns))
	}
	if !regexp.MustCompile(`\.value\s*=\s*[A-Za-z_$][\w$]*\.textContent`).MatchString(picker) {
		t.Error("the accepted path is not the option's own text; a value assembled here is one the daemon never offered, and the option is the only thing on this page that was")
	}
	if accept := strings.Index(source, "'Enter'"); accept < 0 || assigns[0][0] < accept {
		t.Error("the picker writes the field's value before it has answered Enter; the accept is the one keystroke that may change what is in this field, and a write on any other path is the list constraining what was typed")
	}

	// Tab is never refused. It is last in the contract's table and last in the
	// branch order below, so what follows it is the default that does nothing —
	// and a Tab this file swallowed is focus trapped in a text field, which trades
	// the accessibility floor for a tidy-looking close.
	//
	// The tail stops where the pointer starts (T017). What is below that refuses
	// something too, but it is the browser's own focus move on a press inside the
	// list and not a key at all, so a slice running to the end of the block would
	// answer this claim with the wrong branch. What is held is unchanged: nothing
	// between the Tab literal and the end of this handler refuses the key.
	tab := strings.Index(picker, "'Tab'")
	if tab < 0 {
		t.Fatal("the picker answers no Tab at all, so there is no branch to hold to leaving the key alone")
	}
	leaving := picker[tab:]
	if pointer := strings.Index(picker, "'mousedown'"); pointer > tab {
		leaving = picker[tab:pointer]
	}
	if strings.Contains(leaving, "preventDefault") {
		t.Error("the picker refuses the Tab that leaves the field; closing the list is all that is owed here, and swallowing the key traps a keyboard operator in the one control this milestone exists to make operable")
	}

	// The sweep: every shape of intercepting what is typed. Each looks like a
	// better picker and each makes an unlisted path unreachable — which is the
	// list becoming the allowlist, the mistake this whole control is arranged
	// against.
	for _, forbidden := range []struct {
		pattern *regexp.Regexp
		what    string
		why     string
	}{
		{regexp.MustCompile(`['"](keypress|beforeinput)['"]`), "listens for the characters themselves", "typing is never intercepted; the keys this file answers are the ones that mean something to a list, and a character is not one of them"},
		{regexp.MustCompile(`\bkey\.length\b`), "branches on single characters", "that test exists to catch typing, and catching typing is the one thing this handler may not do"},
		{regexp.MustCompile(`\breadOnly\b`), "makes the field read-only", "a picker that can only be picked from is the chooser FR-040 refuses — the allowlist is the daemon's and this control is a convenience"},
		{regexp.MustCompile(`setCustomValidity\(`), "refuses a value in the browser", "the refusal an operator gets is the daemon's, in words it wrote; a native bubble here is the containment rule copied into a browser and free to drift"},
	} {
		if match := forbidden.pattern.FindString(picker); match != "" {
			t.Errorf("the picker %s (%q): %s", forbidden.what, match, forbidden.why)
		}
	}

	// And the stylesheet half, which is the joint this task closes. T009 drew the
	// ring for an attribute nothing set; the assertion above says the script sets
	// it and this one says the rule is still there to be worn.
	if !strings.Contains(stylesheet(t), `[aria-selected="true"]`) {
		t.Error("crswd.css draws nothing for the active option; the attribute the arrows move is invisible without it, and a keyboard operator moves a cursor the page never shows")
	}
}

// pickerBlock is the enhancement's own source, sliced the way
// TestComboKeyboardOperable slices it: from the picker's one constant to the
// query that applies it, which is all of the control and none of anything else.
//
// T017's claims need it for the reason T011's did. The words they turn on are
// ordinary: the card's selection fix listens for a click and calls
// preventDefault, and the toast hides itself — so a claim about the pointer read
// against the whole file would be answered by a block that has nothing to do
// with this control. T011's own inline slicing is left exactly where it is: it
// asserts about the same bytes and it is green, and rewriting a passing test to
// route through something new is the churn AR-008 keeps out of a task's diff.
func pickerBlock(t *testing.T, source string) string {
	t.Helper()

	from := strings.Index(source, "SETTLE_MS")
	to := strings.Index(source, "data-combo")
	if from < 0 || to <= from {
		t.Fatalf("crswd.js has no picker block between its settle constant (%d) and the wrapper query (%d); the enhancement is where the pointer lives", from, to)
	}
	return source[from:to]
}

// TestComboOptionIsPointerSelectable is T017's first half, and it is the one
// place this enhancement cost an operator something rather than adding to it.
//
// The popup it replaced was selectable with a mouse. The themed list was not:
// T010 drew it, T011 made it operable from the keyboard, and neither task's text
// named a click — so from T008 onward `.combo-list li` has carried
// `cursor: pointer` over a control that did nothing, which is the first thing a
// pointer reaches for and the last thing it gets.
//
// **Must fail when** `click` is bound instead of `mousedown`. That is not a
// stylistic difference here: the blur close in the other half of this task shuts
// the list, and a blur lands between the press and the click that would have
// followed it, so a click handler fires on an option that has already been
// hidden. It works when it wins the race and does nothing when it does not,
// which reproduces intermittently and reads as flakiness rather than as a bug —
// the whole reason the pointer and the close are one change and not two.
//
// **Must also fail when** the affordance is removed rather than implemented.
// Deleting `cursor: pointer` would make the picker self-consistent and worse, so
// the rule is asserted here beside the handler that finally earns it — the same
// joint T011 closed between `[aria-selected="true"]` and the ring, on the half of
// the control a mouse reaches.
//
// Go cannot execute this, so the claims are about the bytes a browser is handed
// — the same footing every other script assertion in this file stands on.
func TestComboOptionIsPointerSelectable(t *testing.T) {
	t.Parallel()

	picker := pickerBlock(t, script(t))

	press := regexp.MustCompile(`addEventListener\(\s*['"]mousedown['"]`).FindStringIndex(picker)
	if press == nil {
		t.Fatal("the picker listens for no mousedown, so the themed list is display-only: an operator points at a path, presses it, and the field is unchanged — the control this replaced took that press and this one draws a cursor promising it does too")
	}

	// Every event below arrives after focus has already left the field, which is
	// after the close that leaving it runs. Each one makes the selection depend on
	// winning a race against a handler written to lose it.
	for _, forbidden := range []struct {
		pattern *regexp.Regexp
		what    string
		why     string
	}{
		{regexp.MustCompile(`['"]click['"]`), "selects on click", "a click is dispatched after the press, after the blur, and after the close the blur runs — so the option it lands on is one the list has already hidden"},
		{regexp.MustCompile(`['"]mouseup['"]`), "selects on mouseup", "same ordering, same lost race: the press is the last event that happens while the list is still open"},
		{regexp.MustCompile(`['"]pointerup['"]`), "selects on pointerup", "same ordering again, and naming the newer event does not change which side of the blur it falls on"},
	} {
		if match := forbidden.pattern.FindString(picker); match != "" {
			t.Errorf("the picker %s (%q): %s", forbidden.what, match, forbidden.why)
		}
	}

	// Found from the event rather than bound to each option, for the reason the
	// toast is delegated from the document: draw() replaces every option on every
	// keystroke, so a listener attached to one when this file ran is a listener on
	// an element that is no longer in the page.
	if !strings.Contains(picker[press[0]:], "closest(") {
		t.Error("the picker's pointer handler never finds the option from the event; the options are rebuilt on every keystroke, so anything bound to one of them individually is bound to markup the next filter threw away")
	}

	// The press itself is refused, which is the other half of what makes this pair
	// work. The default action of a press on something that cannot hold focus is to
	// take focus off the field — so without this the list shuts mid-drag when what
	// was pressed is the scroll bar of a bounded box, and an operator who picks a
	// path is left holding it with focus on the document body. It is also why
	// TestComboKeyboardOperable's "Tab is never refused" tail stops here: what is
	// refused below is a focus move and not a key.
	if !strings.Contains(picker[press[0]:], "preventDefault") {
		t.Error("the picker does not refuse the press inside its list; the browser then takes focus off the field, which closes the list under a pointer that was only scrolling it, and leaves an operator who has just picked a path focused on the document body")
	}

	// The two calls this trigger is made of, each found from the state it owns
	// rather than spelled here — so a pointer path that reached the field some
	// other way cannot answer either of them. This is what makes the pointer a
	// second trigger rather than a second behaviour: it takes the option the same
	// way the keyboard does, and the file still writes the field's value once.
	enter := strings.Index(picker, "'Enter'")
	if enter < 0 || enter > press[0] {
		t.Fatalf("the picker answers Enter at %d and the press at %d; the accept the keyboard runs has to exist, and to be above the pointer that shares it", enter, press[0])
	}
	for _, shared := range []struct {
		owns    *regexp.Regexp
		what    string
		regions map[string]string
		why     string
	}{
		{
			regexp.MustCompile(`\.value\s*=[^=]`),
			"the accept",
			map[string]string{"Enter": picker[enter:press[0]], "the pointer": picker[press[0]:]},
			"the field's value is written in exactly one place (T011 holds that), and both triggers reach it through the same helper — a pointer with an accept of its own is a second answer to what the operator chose, on the field FR-008 says any path stays typeable in",
		},
		{
			regexp.MustCompile(`setAttribute\(\s*['"]aria-selected['"]`),
			"the activation",
			map[string]string{"the pointer": picker[press[0]:]},
			"what was pointed at becomes the active option before it is accepted, so the ring, the reader and the value all name the one that was taken — a pointer that skipped it would accept an option the page never showed as chosen",
		},
	} {
		at := shared.owns.FindStringIndex(picker)
		if at == nil {
			t.Errorf("the picker never carries %s at all (%s)", shared.what, shared.owns)
			continue
		}
		// The helper that owns it: the nearest arrow declared above the line that
		// does the work. Naming it here instead would be this file keeping a
		// spelling of the script's, which is the joint every other assertion in
		// these two tests is arranged to avoid.
		declared := regexp.MustCompile(`const\s+([A-Za-z_$][\w$]*)\s*=\s*\(`).FindAllStringSubmatch(picker[:at[0]], -1)
		if len(declared) == 0 {
			t.Errorf("%s sits in no helper of its own, so the key and the pointer cannot be running the same one", shared.what)
			continue
		}
		call := declared[len(declared)-1][1] + "("
		for where, body := range shared.regions {
			if !strings.Contains(body, call) {
				t.Errorf("%s does not run %s (%s): %s", where, shared.what, call, shared.why)
			}
		}
	}

	// And the stylesheet half. The rule is the promise the page has been making
	// since T008, and this is the task that makes it true rather than the one that
	// withdraws it.
	if rule := blockFor(t, stylesheet(t), ".combo-list li"); !regexp.MustCompile(`cursor\s*:\s*pointer`).MatchString(rule) {
		t.Errorf("an option draws no pointer cursor (%q); taking the affordance away is the other way to make this control consistent, and it is the one that leaves an operator with a mouse nothing to aim at", rule)
	}
}

// TestComboClosesOnBlur is T017's other half, and it cannot be written apart from
// the first: the close is what makes a click handler lose its race, and the
// mousedown is what makes the close safe to add.
//
// Every way out of this control that the keyboard has shuts the list — Enter
// accepts and closes, Escape dismisses, Tab leaves. A pointer put anywhere else
// on the page shut nothing, so the list stayed drawn over the rest of the form
// for an operator who had moved on from the field, which on this page means it
// stayed over the roots hint and the submit button below it.
//
// **Must fail when** the close normalises what was typed. A blur is where a
// picker is most often improved into a chooser: writing the nearest option into
// the field on the way out looks tidy, and it makes every path that is not on the
// list unreachable (FR-008) on the one control whose entire arrangement says the
// list offers and the daemon's roots decide.
//
// Go cannot execute this, so the claims are about the bytes a browser is handed
// — the same footing every other script assertion in this file stands on.
func TestComboClosesOnBlur(t *testing.T) {
	t.Parallel()

	picker := pickerBlock(t, script(t))

	leave := regexp.MustCompile(`addEventListener\(\s*['"](blur|focusout)['"]`).FindStringIndex(picker)
	if leave == nil {
		t.Fatal("the picker never notices focus leaving the field, so a list opened by typing and abandoned with the pointer stays open over the rest of the form — the three keys that leave this control all close it, and the way an operator with a mouse leaves it did not")
	}

	// The same close the keyboard runs, found from the Tab branch rather than
	// spelled here. A second close is a second definition of what shutting this
	// list means, and the one this file has does three things beyond hiding a box:
	// it clears the active option, it cancels the settling timer, and it empties
	// the count — a sentence left standing under a closed list describes something
	// that is no longer on the screen.
	tab := strings.Index(picker, "'Tab'")
	if tab < 0 || tab > leave[0] {
		t.Fatalf("the picker answers Tab at %d and blur at %d; the close the keyboard already had is what leaving the field runs", tab, leave[0])
	}
	shut := regexp.MustCompile(`([A-Za-z_$][\w$]*)\(\s*\)\s*;`).FindStringSubmatch(picker[tab:leave[0]])
	if shut == nil {
		t.Fatal("the picker's Tab branch calls nothing, so there is no close for the blur to share")
	}
	if !strings.Contains(picker[leave[0]:], shut[1]+"(") {
		t.Errorf("leaving the field does not run %s(), the close Tab runs; a close written twice is one that forgets the settling timer or the count, and it forgets them on the path nobody is watching", shut[1])
	}

	// And it leaves the answer alone. The offer is what a blur withdraws; what was
	// typed is the operator's, and this file writes it in exactly one place, which
	// is the accept they asked for.
	if strings.Contains(picker[leave[0]:], ".value") {
		t.Error("the picker touches the field's value on its way out; a blur that normalised what was typed to something on the list is the list becoming the allowlist, decided in a browser rather than by the daemon that owns the roots")
	}
}

// TestSelectionDoesNotNavigate is FR-051, and it is as much about what this file
// is not allowed to become as about the one call that satisfies it.
//
// The card's readable half is one anchor (FR-046), so the identifier a card
// renders precisely so it can be copied is now inside a link. Dragging across it
// gives a selection rather than a link drag — draggable="false" buys that in the
// markup — but the release that ends the drag is still a click on a link, and
// the browser navigates away from the page the operator was reading.
//
// **Must fail when** this becomes a functional dependency rather than a papercut
// fix. That is the direction it is likeliest to be lost in by improvement rather
// than by mistake: a block that read the destination off the card and went there
// itself would look like the same feature, work identically in a browser running
// it, and leave a card that does nothing at all in one that is not — which is the
// floor US3 spent a milestone putting under every other control on this page. So
// the sweep is for every way of navigating rather than for a spelling, and the
// markup half below holds the destination where a browser can reach it with this
// file absent.
//
// Go cannot execute this, so the claims are about the bytes a browser is handed
// — the same footing every other script assertion in this file stands on.
func TestSelectionDoesNotNavigate(t *testing.T) {
	t.Parallel()

	source := script(t)

	// Something calls it, and on the document rather than on each anchor: the
	// fleet's live half replaces a card whenever the stream says that session
	// changed, so a listener attached at load is one no card on the page has
	// after its first update — the defect the toast was rewritten for.
	if !regexp.MustCompile(`document\.addEventListener\(\s*['"]click['"]`).MatchString(source) {
		t.Error("crswd.js listens for no click on the document, so either nothing implements FR-051 or it is bound per card and lost the moment the fleet replaces one")
	}
	if regexp.MustCompile(`closest\(\s*['"][^'"]*card-link[^'"]*['"]\s*\)`).FindString(source) == "" {
		t.Error("crswd.js never looks for the card's anchor from the clicked element, so a click on the identifier inside it is not recognised as a click on the link")
	}

	for want, why := range map[string]string{
		"getSelection": "what tells a drag that ended from a click is the platform's own selection, not a position remembered here",
		"isCollapsed":  "a plain click arrives with the selection collapsed, and that click has to navigate — the requirement is about the one that does not",
		"event.detail": "Enter on a focused link is a click with no pointer behind it, and swallowing it would make a card unreachable by keyboard for as long as a selection sat inside it",
	} {
		if !strings.Contains(source, want) {
			t.Errorf("crswd.js does not carry %q: %s", want, why)
		}
	}

	// The refusal itself, and it is asserted *after* the anchor lookup rather
	// than anywhere in the file. This is the second preventDefault here — the
	// toast calls one on every action form — so a bare Contains would be answered
	// by a block that has nothing to do with a card and would stay green with
	// this one deleted.
	if lookup := strings.Index(source, "card-link"); lookup >= 0 && !strings.Contains(source[lookup:], "preventDefault()") {
		t.Error("crswd.js finds the card's anchor and never refuses the click on it; the whole of the fix is declining this one navigation, and anything more is this file owning the link")
	}

	// The navigation sweep. Each of these is this file taking the destination
	// over rather than declining one click of it, and every one of them leaves a
	// card that opens nothing with scripting off.
	for _, forbidden := range []struct {
		pattern *regexp.Regexp
		what    string
		why     string
	}{
		{regexp.MustCompile(`location\.(href|replace)`), "navigates the page itself", "the anchor's href is what opens a session, and a script that went there instead is the one thing standing between a card and its page"},
		{regexp.MustCompile(`window\.open\(`), "opens a window", "same destination, same dependency, and a popup blocker away from doing nothing at all"},
		{regexp.MustCompile(`\.href\s*=[^=]`), "assigns an href", "a destination written here is a destination absent from the markup a browser running no script is handed"},
		{regexp.MustCompile(`\.click\(\)`), "clicks an element for the operator", "synthesising the activation this block just refused is the papercut fix reimplementing the browser"},
	} {
		if match := forbidden.pattern.FindString(source); match != "" {
			t.Errorf("crswd.js %s (%q): %s", forbidden.what, match, forbidden.why)
		}
	}

	// location.assign is allowed exactly once, and only for the update waiter.
	//
	// The sweep above exists so a card opens through its anchor's href rather
	// than through this file — a card that navigates by script opens nothing with
	// scripting off. The update waiter is not that: it runs after the daemon has
	// replaced itself, on a page the operator may have reached by a form post to
	// /dashboard/update, and naming where to go is the only way back from an
	// address that answers no GET.
	//
	// **Must fail when** a second appears, or when the one is not the waiter's.
	if n := len(regexp.MustCompile(`location\.assign\(`).FindAllString(source, -1)); n != 1 {
		t.Errorf("crswd.js assigns location in %d places; want exactly 1 — the update waiter's way back, never a card's destination", n)
	}
	if !regexp.MustCompile(`location\.assign\(UPDATES_PAGE\)`).MatchString(source) {
		t.Error("the one location.assign does not name UPDATES_PAGE, so it is a destination this file took over rather than the way back from a POST")
	}

	// And the markup half, which is what the sweep is protecting. The card is a
	// link with a real destination in it, and it carries no hook for this block:
	// the anchor is found by the class the stylesheet already gives it, so the
	// card a browser is handed is the same markup whether or not this file runs.
	// A card that had acquired a data- attribute for the selection fix would be a
	// card whose behaviour had moved out of the template.
	got := renderComponent(t, "session-card", actionableCard())

	anchors := cardAnchor.FindAllStringSubmatch(got, -1)
	if len(anchors) != 1 {
		t.Fatalf("the card renders %d links, so there is no one anchor a selection could be inside of:\n%s", len(anchors), got)
	}
	if target := `href="/sessions/`; !strings.Contains(anchors[0][1], target) {
		t.Errorf("the card's anchor carries no %s, so the session it opens is somewhere other than the markup and a browser running no script cannot reach it:\n%s", target, got)
	}
	if hooks := regexp.MustCompile(`data-[a-z-]+`).FindAllString(got, -1); !slices.Equal(hooks, []string{"data-session"}) {
		t.Errorf("the card renders %v; data-session is the one hook it has, and a second means the selection fix asked the template for something rather than reading what was already there:\n%s", hooks, got)
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

// cssRule is one selector and the declarations it carries.
type cssRule struct{ selector, body string }

// cssRules splits a stylesheet into its rules. Media preludes are stripped
// first, exactly as styledClasses does it, so a rule inside a media query is
// read as an ordinary chunk rather than being skipped along with the query —
// and a slice rather than a map, because .rain is declared twice and the second
// one is the reduced-motion answer to the first.
func cssRules(source string) []cssRule {
	var out []cssRule
	for _, chunk := range strings.Split(mediaOpen.ReplaceAllString(source, ""), "}") {
		selector, body, ok := strings.Cut(chunk, "{")
		if !ok {
			continue
		}
		out = append(out, cssRule{strings.TrimSpace(selector), body})
	}
	return out
}

// comboSelector is the picker and the switch. Matched by prefix on the class
// itself, so a rule added for either component is swept without this expression
// being edited — and anchored on the dot, so .field-switch, which is the row's
// layout and not the control's presentation, is not one of them.
var comboSelector = regexp.MustCompile(`\.(combo|switch)[\w-]*`)

// comboRules is every rule T009 is answerable for.
func comboRules(t *testing.T) []cssRule {
	t.Helper()

	var out []cssRule
	for _, rule := range cssRules(stylesheet(t)) {
		if comboSelector.MatchString(rule.selector) {
			out = append(out, rule)
		}
	}
	if len(out) == 0 {
		t.Fatal("crswd.css styles no picker and no switch at all, so every assertion about them below is vacuous")
	}
	return out
}

// comboColourProperties are the declarations that paint. A value here naming no
// token is a colour this file invented, whether or not it is spelled as a hex.
var comboColourProperties = map[string]bool{
	"color":            true,
	"background":       true,
	"background-color": true,
	"border":           true,
	"border-color":     true,
	"outline":          true,
	"outline-color":    true,
	"accent-color":     true,
	"box-shadow":       true,
	"text-shadow":      true,
}

// TestNoLiteralColourInComboRules is docs/design-system.md's first rule read at
// the component this task dressed. The whole-file sweep above already fails a
// hex anywhere, and this is deliberately not redundant with it: it also fails a
// paint declaration that names no token at all — a currentColor, a color-mix(),
// a keyword that sweep's list does not carry — which is how a palette drifts
// without a literal ever being written.
//
// **Must fail when** a hex value is introduced into any rule for the picker or
// the switch.
func TestNoLiteralColourInComboRules(t *testing.T) {
	t.Parallel()

	for _, rule := range comboRules(t) {
		for _, forbidden := range forbiddenInRules {
			if match := forbidden.pattern.FindString(rule.body); match != "" {
				t.Errorf("%s carries %s (%q); the picker is a component, and a component that introduced a value of its own is the drift Principle VII forbids", rule.selector, forbidden.what, match)
			}
		}
		for property, value := range declarations(rule.body) {
			if comboColourProperties[property] && !strings.Contains(value, "var(--") {
				t.Errorf("%s sets %s: %s, which references no token; every colour the picker spends comes out of the token block", rule.selector, property, value)
			}
		}
	}
}

// outlineRemoved is the half of the focus rule that is lost by improvement
// rather than by mistake: a ring somebody thought was ugly on a listbox.
var outlineRemoved = regexp.MustCompile(`(?i)outline\s*:\s*(none|0)\b`)

// TestComboFocusRingSurvives is docs/components.md's accessibility floor at the
// three places this control takes focus, and the third is the one a stylesheet
// has to answer on its own.
//
// The input and the switch are reached by the page's single :focus-visible
// rule, so what is held for them is that neither takes it away again — and that
// the rule is still unqualified, because a ring narrowed to a selector these two
// do not match is a ring removed for them just as surely.
//
// An option is different in kind. :focus-visible can never match one: focus
// stays on the input and aria-activedescendant is what points at the active
// option (T011), so nothing in the platform draws an indicator there and the
// only thing that can is a rule here.
//
// **Must fail when** `outline: none` appears with no replacement.
func TestComboFocusRingSurvives(t *testing.T) {
	t.Parallel()

	source := stylesheet(t)

	for _, rule := range comboRules(t) {
		if match := outlineRemoved.FindString(rule.body); match != "" {
			t.Errorf("%s carries %q; the design system permits removing the outline only by replacing it, and a picker is where a keyboard operator most needs to know where they are", rule.selector, match)
		}
	}

	var unqualified bool
	for _, rule := range cssRules(source) {
		unqualified = unqualified || rule.selector == ":focus-visible"
	}
	if !unqualified {
		t.Error("crswd.css has no unqualified :focus-visible rule, so the ring on the working-directory field and on the switch is whatever their own rules happen to draw")
	}

	active := blockFor(t, source, `.combo-list li[aria-selected="true"]`)
	ring := regexp.MustCompile(`(?i)outline\s*:\s*([^;}]+)`).FindStringSubmatch(active)
	if ring == nil {
		t.Fatalf("the active option draws no outline (%q); an option is never focused, so a keyboard operator moving through this list moves a cursor nothing shows", active)
	}
	for _, token := range []string{"var(--focus-width)", "var(--phosphor)"} {
		if !strings.Contains(ring[1], token) {
			t.Errorf("the active option's ring is %q and does not spend %s; the indicator is the design system's own ring, not a width and a colour invented for a listbox", strings.TrimSpace(ring[1]), token)
		}
	}
}

// animationDecl is the property the reduced-motion block does not name.
var animationDecl = regexp.MustCompile(`(?i)(?:^|[;{\s])animation(-[a-z]+)?\s*:\s*[^;}]+`)

// universalReset is the block's answer: transition removed for everything,
// rather than for the rules that happened to transition when it was written.
var universalReset = regexp.MustCompile(`(?s)\*[^{}]*\{[^}]*transition\s*:\s*none`)

// TestComboDoesNotAnimateUnderReducedMotion is FR-018 for the one component
// this milestone adds motion to, and the reason it is not covered by
// TestReducedMotionStopsEveryTransition is one property: that block resets
// `transition` and nothing else, so an @keyframes animation on the listbox runs
// on for an operator who asked for no motion and every existing test stays
// green.
//
// **Must fail when** a fade is added to the listbox that the preference does not
// reach.
func TestComboDoesNotAnimateUnderReducedMotion(t *testing.T) {
	t.Parallel()

	source := stylesheet(t)

	transitions := 0
	for _, rule := range comboRules(t) {
		if match := animationDecl.FindString(rule.body); match != "" {
			t.Errorf("%s declares %q; the preference is answered by a universal transition: none, which an animation does not obey", rule.selector, strings.TrimSpace(match))
		}
		transitions += len(transitionDecl.FindAllString(rule.body, -1))
	}
	if transitions == 0 {
		t.Fatal("no rule for the picker or the switch transitions anything, so the preference has nothing here to answer and this test asserts nothing")
	}

	reduced := blockFor(t, source, "@media (prefers-reduced-motion: reduce)")
	if !universalReset.MatchString(reduced) {
		t.Errorf("the reduced-motion block carries no universal transition: none (%q), so the picker's own transitions survive a preference for no motion", reduced)
	}
}

// appearanceNone is what takes the platform's own rendering away, in either
// spelling.
var appearanceNone = regexp.MustCompile(`(?i)(?:^|[;{\s-])appearance\s*:\s*none`)

// TestModeNotConveyedByColourAlone is FR-019 and the design system's fifth
// non-negotiable at the control that chooses how privileged a session is.
//
// The switch is a real checkbox and stays one: accent-color paints the
// platform's tick in this palette's green without removing the tick, so the
// state is a shape and the colour reinforces it. Take the appearance away and
// what is left is whatever fill this stylesheet draws — which is the state
// carried by hue, readable to nobody who cannot separate two greens.
//
// The words beside it are the other half, and they are what a state is actually
// carried by: a label a reader announces and a sighted operator reads, bound to
// the box by `for` rather than by sitting next to it.
//
// **Must fail when** the switch is styled with colour as its only cue.
func TestModeNotConveyedByColourAlone(t *testing.T) {
	t.Parallel()

	for _, rule := range comboRules(t) {
		if !strings.Contains(rule.selector, ".switch") {
			continue
		}
		if match := appearanceNone.FindString(rule.body); match != "" {
			t.Errorf("%s carries %q; the tick is the platform's own shape and it is what says the mode is on, so the two states then differ by fill alone", rule.selector, strings.TrimSpace(match))
		}
		if !strings.Contains(rule.selector, ":checked") {
			continue
		}
		var beyondColour bool
		for property := range declarations(rule.body) {
			beyondColour = beyondColour || !comboColourProperties[property]
		}
		if !beyondColour {
			t.Errorf("%s changes nothing but colour (%q), so being switched on is a hue; colour is reinforcement and never the state itself", rule.selector, strings.TrimSpace(rule.body))
		}
	}

	out := renderComponent(t, "create-form", createFormView{PageToken: testCardToken})

	box := regexp.MustCompile(`<input\b[^>]*\bclass="switch-input"[^>]*>`).FindString(out)
	if box == "" {
		t.Fatalf("the create form renders no .switch-input, so the rules above dress nothing:\n%s", out)
	}
	if kind, _ := attributeValue(t, box, "type"); kind != "checkbox" {
		t.Errorf("the switch is type=%q; the tick this test protects is a native checkbox's, and nothing else on the platform draws one", kind)
	}

	label := regexp.MustCompile(`(?s)<label\b[^>]*\bclass="switch-label"[^>]*>(.*?)</label>`).FindStringSubmatch(out)
	if label == nil || strings.TrimSpace(label[1]) == "" {
		t.Fatalf("the switch carries no .switch-label with words in it, so nothing but the box's own appearance says which mode is chosen:\n%s", out)
	}
	id, _ := attributeValue(t, box, "id")
	if named, _ := attributeValue(t, label[0], "for"); named != id {
		t.Errorf("the .switch-label names %q and the switch is id=%q; a label that is only beside a control is not that control's name", named, id)
	}
}

// comboClasses are the classes this component is made of: the picker's three,
// the switch's two, and the row the switch sits in.
var comboClasses = []string{"combo", "combo-list", "combo-status", "field-switch", "switch-input", "switch-label"}

// TestComboClassesAppearInRenderedMarkup names them one by one, which the
// stylesheet's own both-directions sweep deliberately does not: that test reads
// the template *source*, so a class inside a branch that is never taken counts
// as rendered there and would leave a rule styling markup no operator is ever
// handed.
//
// Both of the wrapper's states are checked, because the plain field — a daemon
// with nothing at all to suggest — is the one an enhancement can never help and
// the one a conditional is likeliest to have swallowed.
//
// **Must fail when** a class exists only in CSS, which the existing sweep reads
// as a dead rule and is how a second picker starts.
func TestComboClassesAppearInRenderedMarkup(t *testing.T) {
	t.Parallel()

	styled := styledClasses(t)

	for name, view := range comboViews() {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			out := renderComponent(t, "create-form", view)
			rendered := make(map[string]bool)
			for _, attr := range classAttr.FindAllStringSubmatch(out, -1) {
				for _, class := range strings.Fields(attr[1]) {
					rendered[class] = true
				}
			}

			for _, class := range comboClasses {
				if !rendered[class] {
					t.Errorf("the create form renders no %q; a rule for markup a browser is never handed is a component nobody built:\n%s", class, out)
				}
				if !styled[class] {
					t.Errorf("crswd.css has no rule selecting %q, so the browser gets it unstyled", class)
				}
			}
		})
	}
}

// TestEveryTokenReferencedExists closes the gap that let a broken declaration
// through.
//
// TestNoRuleCarriesAValueThatBelongsInAToken requires a rule to reference a
// token rather than a literal, and it is satisfied by referencing one that does
// not exist. `font-family: var(--font)` passed it and resolved to nothing, so the
// version beside the wordmark quietly inherited the brand's font — a rule that
// looked correct, passed the guard, and did nothing.
//
// **Must fail when** a rule names a token the block does not define. That is a
// declaration with no effect, and the failure is silent by construction: CSS
// drops what it cannot resolve and renders whatever was inherited.
func TestEveryTokenReferencedExists(t *testing.T) {
	t.Parallel()

	sheet := stylesheet(t)

	defined := map[string]bool{}
	for _, m := range regexp.MustCompile(`(?m)^\s*(--[a-z0-9-]+)\s*:`).FindAllStringSubmatch(sheet, -1) {
		defined[m[1]] = true
	}
	if len(defined) == 0 {
		t.Fatal("no tokens were found at all; this test is not checking anything")
	}

	for _, m := range regexp.MustCompile(`var\(\s*(--[a-z0-9-]+)`).FindAllStringSubmatch(sheet, -1) {
		if !defined[m[1]] {
			t.Errorf("a rule references %s and the token block does not define it; the declaration resolves to nothing and the property falls back to whatever was inherited", m[1])
		}
	}
}

// TestTheUpdateDoesNotBecomeAToast is the third attempt at this route's answer,
// pinned so there is not a fourth.
//
// It began as a redirect, which sent the browser to fetch a page from a daemon
// in the act of stopping — the operator watched their own update turn into a
// 404. It was then rendered in place, and the shared submit handler turned that
// page into a toast, which is a sentence where a spinner was wanted.
//
// **Must fail when** the update form falls through to the toast path. Every
// other form here does something the daemon finishes and reports on; an update
// has begun replacing the daemon and is about to stop answering, so the useful
// thing is the page staying put and waiting.
func TestTheUpdateDoesNotBecomeAToast(t *testing.T) {
	t.Parallel()

	source := script(t)

	if !strings.Contains(source, "form.matches('.update-form')") {
		t.Error("the submit handler does not single out the update form, so an update answers with a sentence where a spinner was wanted")
	}
	if !strings.Contains(source, "swapUpdatesSection") {
		t.Error("nothing puts the daemon's updating markup where the form was")
	}

	// The swap must come before the toast, or the toast wins and the special
	// case is unreachable.
	if at, toast := strings.Index(source, "swapUpdatesSection(said)"), strings.Index(source, "show(sentence(said)"); at < 0 || toast < 0 || at > toast {
		t.Error("the update's branch does not precede the toast, so it can never be taken")
	}
}

// TestThePaneDoesNotChainItsOverscroll holds the pane's horizontal scroll
// inside the pane.
//
// A session prints 80 columns into an element that is narrower than that on
// every phone and on plenty of desktops. When the reader pans to the right edge
// and keeps going, the gesture chains to the page, and the browser reads a
// horizontal swipe against a page that does not scroll sideways as its own
// back-navigation gesture: the reader is taken off the session they were
// reading, mid-read, by scrolling.
//
// It is asserted on the base rule because it is unconditional. Scroll chaining
// is not a phone behaviour — a trackpad does it — and even where the pane wraps,
// an unbroken run longer than the viewport still scrolls in this axis.
//
// **Must fail when** a pan at the scroll edge chains into the browser's
// navigation gesture and throws the reader off the page mid-session.
func TestThePaneDoesNotChainItsOverscroll(t *testing.T) {
	t.Parallel()

	pane := blockFor(t, stylesheet(t), ".pane")

	if !regexp.MustCompile(`(?i)overscroll-behavior-x\s*:\s*contain`).MatchString(pane) {
		t.Errorf("the pane does not contain its horizontal overscroll, so a pan past its right edge chains to the page and the browser navigates away from the session being read: %q", pane)
	}
}

// TestThePaneDoesNotTrapVerticalScrolling is the other half of the axis
// decision, and it exists because the symmetry is the trap.
//
// Containing one axis reads as half a job, and the obvious tidy-up — the bare
// property, or the matching `-y` — is a regression that no rendering test here
// would catch. The pane is `max-block-size: var(--pane-h)`, 30rem, against a
// phone viewport of roughly 660px: it is most of the screen. Contain that axis
// and a flick which started inside the pane stops at the pane's end instead of
// carrying on into the page, so the reader is sealed inside a box that fills
// their display with no way out but a gesture that begins somewhere else.
//
// The horizontal axis has no such cost, because the page does not scroll
// horizontally at all — there is nothing on that axis for the pane to be
// stealing.
//
// Every `.pane` rule is swept, not just the base one: the declaration would do
// the same damage from inside the breakpoint block, where the wrap lives.
//
// **Must fail when** the vertical axis is contained too, trapping the reader in
// a box that fills most of their screen.
func TestThePaneDoesNotTrapVerticalScrolling(t *testing.T) {
	t.Parallel()

	// Bare or `-y`. `-x` is what the rule above requires, so it is the one
	// spelling this expression must not match.
	trapped := regexp.MustCompile(`(?i)overscroll-behavior(-y)?\s*:`)

	var swept int
	for _, rule := range cssRules(stylesheet(t)) {
		selectors := strings.Split(rule.selector, ",")
		for i, one := range selectors {
			selectors[i] = strings.TrimSpace(one)
		}
		if !slices.Contains(selectors, ".pane") {
			continue
		}
		swept++
		if trapped.MatchString(rule.body) {
			t.Errorf("a .pane rule contains its vertical overscroll; the pane is 30rem of a phone screen, so a flick begun inside it would stop at its end rather than scrolling the page: %q", rule.body)
		}
	}
	if swept == 0 {
		t.Fatal("crswd.css has no .pane rule at all, so this test is checking nothing")
	}
}

// whiteSpaceDecl is a wrapping mode and the keyword it is set to. The value is
// captured rather than matched, because `pre` is a prefix of `pre-wrap` and the
// two rules below want opposite answers from the same property.
var whiteSpaceDecl = regexp.MustCompile(`(?i)white-space\s*:\s*([a-z-]+)`)

// TestThePaneWrapsOnlyOnNarrowViewports is the trade the milestone makes, held
// to the half of the stylesheet that is allowed to make it.
//
// A session prints 80 columns. A 390px phone shows about 44 of them, so reading
// one paragraph of prose is eleven pans right and back — the dominant phone task
// failing outright. Wrapping fixes that and costs the alignment of Claude Code's
// own box borders, dividers and tables, which wrap into a line plus a stub.
// research.md priced the alternative: shrink-to-fit needs a ~6.9px font.
//
// `overflow-wrap: anywhere` rather than `break-word` because the run this has to
// break is a path or a hash with no break opportunity in it, which is exactly
// what a real terminal breaks at its column edge.
//
// The assertion reads the rule inside the breakpoint block rather than
// `blockFor(".pane")`, which returns the base rule — the first `.pane` in the
// file — and would be unsatisfiable against a base rule that must say `pre`.
//
// **Must fail when** wrapping is added to the base rule, so a desktop loses
// column alignment to fix a phone.
func TestThePaneWrapsOnlyOnNarrowViewports(t *testing.T) {
	t.Parallel()

	narrow := blockFor(t, stylesheet(t), "@media (max-width: 780px)")
	pane := blockFor(t, narrow, ".pane")

	if got := whiteSpaceDecl.FindStringSubmatch(pane); got == nil || got[1] != "pre-wrap" {
		t.Errorf("the pane does not wrap below the breakpoint, so reading a paragraph on a phone is a horizontal pan per line: %q", pane)
	}
	if !regexp.MustCompile(`(?i)overflow-wrap\s*:\s*anywhere`).MatchString(pane) {
		t.Errorf("the pane wraps but an unbroken run — a path, a hash — still overflows the viewport with nothing to break it: %q", pane)
	}
}

// TestThePaneKeepsItsDesktopAlignment is the other side of that trade, and the
// side with no visible symptom.
//
// Overriding `white-space` inside the breakpoint and changing it in the base
// rule look identical on a phone. They differ on every desktop, where the pane
// is wide enough for 80 columns and alignment is the whole point of a terminal:
// a base-rule change would take diffs, tables and TUI chrome away from every
// reader who never had the problem, silently, to fix one who did.
//
// **Must fail when** the base declaration is changed rather than overridden, and
// every desktop reader loses alignment silently.
func TestThePaneKeepsItsDesktopAlignment(t *testing.T) {
	t.Parallel()

	pane := blockFor(t, stylesheet(t), ".pane")

	if got := whiteSpaceDecl.FindStringSubmatch(pane); got == nil || got[1] != "pre" {
		t.Errorf("the base pane rule no longer sets `white-space: pre`, so a desktop wide enough for 80 columns wraps them anyway and every alignment-dependent screen is misrepresented: %q", pane)
	}
}

// viewportMeta is the element that tells a phone how wide to pretend to be. It
// is counted rather than parsed: what this test forbids is a spelling, and the
// count is only here so a tree that stopped carrying viewports altogether fails
// loudly instead of passing vacuously.
var viewportMeta = regexp.MustCompile(`(?i)<meta[^>]*name="viewport"[^>]*>`)

// zoomClamp is the two ways a page takes pinch-zoom away. `maximum-scale` is
// forbidden at any value, not just 1: a ceiling is a ceiling, and the operator
// zooming into a wrapped diff is already past whatever number looked generous
// when it was written.
var zoomClamp = regexp.MustCompile(`(?i)maximum-scale|user-scalable\s*=\s*["']?\s*(no|0)`)

// TestNoPageClampsTheZoom keeps the escape hatch open for the trade the pane
// makes below the breakpoint.
//
// Wrapping the pane misrepresents everything alignment-dependent Claude Code
// prints — box borders, dividers, tables, diffs. The mitigation is that the
// reader can pinch into it, and that mitigation exists only for as long as no
// page clamps the scale. Disabling zoom is also the standard reflex reached for
// when a mobile layout misbehaves, so the moment this milestone introduced a
// reason to reach for it is the moment it needs a guard.
//
// The whole embedded tree is swept, not the four pages by name: a fifth page,
// or a partial that grew a meta of its own, would otherwise ship unguarded. The
// sweep is on the markup rather than inside the viewport meta, so a clamp in a
// second meta element is caught by the same expression.
//
// **Must fail when** someone "fixes" the layout by disabling zoom, removing the
// only mitigation for the trade the pane's wrap makes.
func TestNoPageClampsTheZoom(t *testing.T) {
	t.Parallel()

	viewports := 0
	err := fs.WalkDir(web.Templates, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		source, err := fs.ReadFile(web.Templates, p)
		if err != nil {
			return err
		}
		markup := string(templateComment.ReplaceAll(source, nil))

		viewports += len(viewportMeta.FindAllString(markup, -1))
		for _, clamp := range zoomClamp.FindAllString(markup, -1) {
			t.Errorf("web/%s clamps the visual viewport (%q); the pane wraps below the breakpoint and pinch-zoom is the only way left to read a diff or a box border on a phone", p, clamp)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk the embedded template tree: %v", err)
	}
	if viewports == 0 {
		t.Fatal("no template carries a viewport meta at all, so this sweep asserted nothing — and every page is being laid out at a desktop width and scaled down")
	}
}

// TestWideSettingsPanTheirOwnPanel puts the settings page's horizontal scroll
// on the content rather than on the index sitting beside it.
//
// `overflow-x: auto` was declared on `.settings`, which is the grid wrapper
// holding the section menu *and* the panel. A table wider than the viewport
// therefore panned both: reaching a Save button dragged the list of sections
// off the screen with it, so the operator lost their place in the page to touch
// the control they came for. On a phone every settings table is wider than the
// viewport, which makes this the ordinary case there rather than an edge one.
//
// Both halves are asserted together because either alone is satisfiable without
// changing anything a reader would see. Every rule whose selector is `.settings`
// is swept rather than the first one `blockFor` would return: the wrapper is
// declared twice at the top level and again inside the breakpoint, and the
// property does the same damage from any of them.
//
// **Must fail when** the panel gains the property without the wrapper losing
// it, which renders exactly as it does today.
func TestWideSettingsPanTheirOwnPanel(t *testing.T) {
	t.Parallel()

	source := stylesheet(t)
	pans := regexp.MustCompile(`(?i)overflow-x\s*:`)

	if panel := blockFor(t, source, ".settings-panel"); !pans.MatchString(panel) {
		t.Errorf("the settings panel is not its own scroll container, so a table wider than the viewport has nothing to scroll inside and moves the page instead: %q", panel)
	}

	var swept int
	for _, rule := range cssRules(source) {
		selectors := strings.Split(rule.selector, ",")
		for i, one := range selectors {
			selectors[i] = strings.TrimSpace(one)
		}
		if !slices.Contains(selectors, ".settings") {
			continue
		}
		swept++
		if pans.MatchString(rule.body) {
			t.Errorf("the .settings grid wrapper scrolls horizontally, so wide content pans the section menu along with the panel and the operator loses the index to reach a Save button: %q", rule.body)
		}
	}
	if swept == 0 {
		t.Fatal("crswd.css has no .settings wrapper rule at all, so the half of this test that matters asserted nothing")
	}
}
