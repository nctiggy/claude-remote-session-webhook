package claudeauth_test

import (
	"embed"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nctiggy/claude-remote-session-webhook/internal/claudeauth"
)

// sources is this package's own text, embedded so the false-positive guard reads
// the real files rather than a copy of the anchors that would drift from them.
//
//go:embed claudeauth.go claudeauth_test.go
var sources embed.FS

// golden reads a pane captured from a real Claude Code process.
//
// Every file under testdata came off a real terminal at 120 columns, against an
// isolated CLAUDE_CONFIG_DIR on a private tmux socket, so no credential on the
// capturing host was involved. They are stored with their wrapping intact
// because the wrapping is the thing most likely to break detection, and a
// hand-typed fixture is a fixture that agrees with the code by construction.
func golden(t *testing.T, name string) string {
	t.Helper()

	//nolint:gosec // G304: the path is this test's own literal, joined under testdata. There is no request, flag or environment value anywhere in it.
	body, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read golden pane: %v", err)
	}
	return string(body)
}

// TestDetectPrompt covers both sign-in screens and the panes that must not be
// mistaken for one.
//
// **Must fail when** a logged-out session reads as anything but a prompt. Today
// such a session renders `running`: the daemon reports a fleet that can do no
// work as healthy, which is the failure this package exists to end.
func TestDetectPrompt(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		pane     string
		wantKind claudeauth.Kind
		wantOK   bool
	}{
		{
			name:     "the device-code screen is named",
			pane:     golden(t, "device-code.pane"),
			wantKind: claudeauth.KindDeviceCode,
			wantOK:   true,
		},
		{
			name:     "the login menu is named",
			pane:     golden(t, "select-method.pane"),
			wantKind: claudeauth.KindSelectMethod,
			wantOK:   true,
		},
		{
			// A real TUI from the same binary that is emphatically not a login
			// screen. It is the first thing a fresh config dir shows, so it is
			// the pane most easily confused with one.
			name:   "the theme picker is not a login screen",
			pane:   golden(t, "theme-picker.pane"),
			wantOK: false,
		},
		{name: "an empty pane matches nothing", pane: "", wantOK: false},
		{
			name:   "an ordinary shell prompt matches nothing",
			pane:   "operator@host:~/code$ ",
			wantOK: false,
		},
		{
			// Half of the device-code screen's anchors. One phrase alone is
			// something Claude Code could print anywhere; this pins that it
			// takes both.
			name:   "a stray mention of pasting a code is not the screen",
			pane:   "Paste code here if prompted >",
			wantOK: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, ok := claudeauth.DetectPrompt(tc.pane)
			if ok != tc.wantOK {
				t.Fatalf("DetectPrompt matched=%v, want %v", ok, tc.wantOK)
			}
			if !tc.wantOK {
				if got != nil {
					t.Errorf("no match returned a prompt: %v", got)
				}
				return
			}
			if got.Kind != tc.wantKind {
				t.Errorf("kind is %q, want %q", got.Kind, tc.wantKind)
			}
		})
	}
}

// TestDetectPromptSurvivesTheWrapItArrivesWith is the failure this package was
// built around.
//
// **Must fail when** detection depends on where the pane happened to break a
// line. The pane width is the operator's terminal, not anything this daemon
// chooses, and a phrase that matches at 120 columns and not at 60 would report a
// logged-out session as healthy on exactly the narrow screen — a phone — this
// product exists to be used from.
func TestDetectPromptSurvivesTheWrapItArrivesWith(t *testing.T) {
	t.Parallel()

	flat := strings.Join(strings.Fields(golden(t, "device-code.pane")), " ")

	for _, width := range []int{40, 60, 80, 120} {
		t.Run(fmt.Sprintf("%d columns", width), func(t *testing.T) {
			t.Parallel()

			var rewrapped strings.Builder
			for i := 0; i < len(flat); i += width {
				end := min(i+width, len(flat))
				rewrapped.WriteString(flat[i:end])
				rewrapped.WriteString("\n")
			}

			got, ok := claudeauth.DetectPrompt(rewrapped.String())
			if !ok {
				t.Fatalf("the device-code screen went undetected at %d columns; a session showing it would render as running", width)
			}
			if got.Kind != claudeauth.KindDeviceCode {
				t.Errorf("kind is %q, want %q", got.Kind, claudeauth.KindDeviceCode)
			}
		})
	}
}

// TestDetectPromptRebuildsTheWrappedURL covers the other half of the wrapping
// problem.
//
// **Must fail when** the URL is taken as the one line it appears to be. It is
// longer than any terminal is wide and always arrives split — four lines at 120
// columns — and half a sign-in URL is worse than none: it looks like a working
// link and goes nowhere.
func TestDetectPromptRebuildsTheWrappedURL(t *testing.T) {
	t.Parallel()

	got, ok := claudeauth.DetectPrompt(golden(t, "device-code.pane"))
	if !ok {
		t.Fatal("the device-code golden did not match")
	}
	if got.URL == "" {
		t.Fatal("no URL was recovered from a screen that shows one")
	}
	if strings.ContainsAny(got.URL, " \n\t") {
		t.Errorf("the rebuilt URL carries whitespace, so the lines were joined but not repaired: %q", got.URL)
	}
	// The last query parameter lives on the final wrapped line. Recovering it
	// is what proves every continuation was taken, not just the second.
	if !strings.Contains(got.URL, "state=not-a-real-state-fixture-value") {
		t.Errorf("the rebuilt URL stops before its last line; continuation lines are being dropped")
	}
	if strings.Contains(got.URL, "Paste code here") {
		t.Errorf("the rebuilt URL ran on into the prose below it: %q", got.URL)
	}
}

// TestAPromptNeverPrintsItsURL keeps a live credential out of the places a
// struct normally leaks into.
//
// **Must fail when** the URL is recoverable from the default rendering. It
// carries a one-shot PKCE challenge and the state paired with it, and
// docs/auth-and-sessions.md forbids logging it, auditing it, or returning it in
// an error. fmt reaches for String on both %v and %s, so making the safe
// rendering the default is what stops a debug line nobody reviewed from being
// the disclosure.
func TestAPromptNeverPrintsItsURL(t *testing.T) {
	t.Parallel()

	got, ok := claudeauth.DetectPrompt(golden(t, "device-code.pane"))
	if !ok {
		t.Fatal("the device-code golden did not match")
	}

	// Through an interface, so that the verbs are exercised at run time as they
	// would be in a log line — a direct fmt.Sprintf("%s", got) is rewritten by
	// staticcheck into the String() call this test is trying not to assume.
	var leaky any = got

	for _, rendered := range []string{
		fmt.Sprintf("%v", leaky),
		fmt.Sprintf("%s", leaky),
		fmt.Sprint(*got),
		fmt.Errorf("wrapped: %v", leaky).Error(),
	} {
		if strings.Contains(rendered, "claude.com") || strings.Contains(rendered, "code_challenge") {
			t.Errorf("a Prompt printed its sign-in URL: %s", rendered)
		}
	}
}

// TestDetectPromptDoesNotFireOnItsOwnSource is the regression guard for a
// false positive that shipped.
//
// **Must fail when** a pane merely carrying these phrases is reported as a
// sign-in screen. Every pane this package runs against is a Claude Code session,
// and those sessions read this repository. This file declares all four anchors
// as string literals, so before the quoting guard `cat` of it detected as
// KindDeviceCode — measured on the merged build, not supposed.
//
// The harm is the inverted twin of the one this package fixes, and worse: a
// logged-out session reading `running` is a stale card, while a working session
// reading `needs-auth` sends an operator to re-authenticate a host whose
// credential is fine, silently, because the mislabelled session keeps working.
//
// It reads the real files rather than a fixture on purpose. A fixture would be a
// copy of the anchors that stops being a copy the moment somebody edits the
// list, and the whole point is that this package's own text is the thing most
// likely to be on screen while somebody works on it.
func TestDetectPromptDoesNotFireOnItsOwnSource(t *testing.T) {
	t.Parallel()

	for _, name := range []string{"claudeauth.go", "claudeauth_test.go"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			src, err := sources.ReadFile(name)
			if err != nil {
				t.Fatalf("read %s: %v", name, err)
			}
			if got, found := claudeauth.DetectPrompt(string(src)); found {
				t.Errorf("%s displayed in a pane detects as %v; a session reading this package would be labelled unusable", name, got)
			}
		})
	}
}

// TestDetectPromptIgnoresAQuotedMention covers the shapes the guard is really
// for, spelled out rather than left implicit in the file read above.
func TestDetectPromptIgnoresAQuotedMention(t *testing.T) {
	t.Parallel()

	mentions := map[string]string{
		"a Go string literal": `var selectMethodPhrases = []string{
	"Select login method:",
	"Claude account with subscription",
}`,
		"prose quoting both anchors": `The two anchors are the line "Select login method:" and the
first option, "Claude account with subscription". Neither is
enough alone.`,
		"a diff of this package": `+	"Browser didn't open?",
+	"Paste code here if prompted",`,
		"JSON": `{"phrases": ["Select login method:", "Claude account with subscription"]}`,
	}

	for name, pane := range mentions {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if got, found := claudeauth.DetectPrompt(pane); found {
				t.Errorf("a pane that only mentions the anchors detects as %v", got)
			}
		})
	}
}

// TestDetectPromptStillFiresWhenAMentionIsAlsoOnScreen is the other half of the
// "at least once unquoted" rule.
//
// A session really parked on the sign-in may have a quoted mention scrolled
// above it — somebody was reading this package when their login expired, which
// is exactly how this was found. The screen is what matters.
func TestDetectPromptStillFiresWhenAMentionIsAlsoOnScreen(t *testing.T) {
	t.Parallel()

	pane := `  the anchors are "Select login method:" and "Claude account with subscription"
` + golden(t, "select-method.pane")

	got, found := claudeauth.DetectPrompt(pane)
	if !found {
		t.Fatal("a real sign-in screen went undetected because a quoted mention was scrolled above it")
	}
	if got.Kind != claudeauth.KindSelectMethod {
		t.Errorf("kind = %q, want %q", got.Kind, claudeauth.KindSelectMethod)
	}
}

// TestDetectPromptReadsTheAuthLoginScreen covers the screen the relay itself
// drives, which is not the screen an interactive `claude` draws.
//
// **Must fail when** only the cold-start wording is known. Measured on 2.1.263:
// `claude auth login --claudeai` prints "If the browser didn't open, visit:"
// with the URL *inline on the same line*, where a `claude` that starts logged
// out prints "Browser didn't open? Use the url below to sign in (c to copy)"
// with the URL at column zero. They share one phrase and no URL layout.
//
// The relay runs `claude auth login` in a window of its own rather than typing
// into a working session, so this is the screen it has to read. A detector
// knowing only the other one reports the relay's own window as showing nothing,
// and hands the operator a page with no link on it — which is the entire
// feature, missing, with every other test green.
func TestDetectPromptReadsTheAuthLoginScreen(t *testing.T) {
	t.Parallel()

	got, found := claudeauth.DetectPrompt(golden(t, "auth-login.pane"))
	if !found {
		t.Fatal("the `claude auth login` screen went undetected; the relay would show no link")
	}
	if got.Kind != claudeauth.KindDeviceCode {
		t.Errorf("kind = %q, want %q — downstream this is the same question as the other device-code screen", got.Kind, claudeauth.KindDeviceCode)
	}

	// The inline URL has to come back whole, and without the prose in front of it.
	if strings.Contains(got.URL, "browser didn't open") || strings.Contains(got.URL, "visit:") {
		t.Errorf("the recovered URL carries the sentence it was printed inside: %q", got.URL)
	}
	if !strings.HasPrefix(got.URL, "https://claude.com/cai/oauth/authorize") {
		t.Errorf("the recovered URL does not start at the link: %q", got.URL)
	}
	for _, fragment := range []string{
		"code_challenge_method=S256",
		"state=EXAMPLEstateEXAMPLEstateEXAMPLEstateEXAMPLE",
	} {
		if !strings.Contains(got.URL, fragment) {
			t.Errorf("the recovered URL is missing %q; it was not rebuilt across the wrap", fragment)
		}
	}
	if strings.ContainsAny(got.URL, " \t\n") {
		t.Errorf("the recovered URL carries whitespace, so it is not one link: %q", got.URL)
	}
}

// TestSignInURLStopsAtThePastePrompt is the regression guard for a link that
// came back with the screen's next line welded onto it.
//
// **Must fail when** the recovered URL runs past the link. On the screen
// `claude auth login` draws there is no blank line between the URL's last
// continuation and "Paste code here if prompted >" — the prompt sits directly
// under it, at column zero. A rule that stopped at an indented line therefore
// did not stop at all, and the operator was handed a link with the prompt text
// on the end of it, which loads nothing.
//
// It was found by an acceptance test against a real terminal, not here: the
// golden that missed it had a blank line invented into that gap. A fixture is
// evidence only as far as it was really captured, which is why this asserts the
// no-blank-line shape explicitly rather than trusting the file to keep it.
func TestSignInURLStopsAtThePastePrompt(t *testing.T) {
	t.Parallel()

	pane := golden(t, "auth-login.pane")

	// The premise. If a blank line ever creeps back into the golden, this test
	// stops covering the thing it exists for and says so.
	lines := strings.Split(strings.TrimRight(pane, "\n"), "\n")
	last := lines[len(lines)-1]
	if !strings.HasPrefix(last, "Paste code here") {
		t.Fatalf("the golden's last line is %q, not the paste prompt; this test no longer covers the case it was written for", last)
	}
	if before := lines[len(lines)-2]; strings.TrimSpace(before) == "" {
		t.Fatal("the golden has a blank line before the paste prompt; the real screen has none, and with one this test proves nothing")
	}

	got, found := claudeauth.DetectPrompt(pane)
	if !found {
		t.Fatal("the screen went undetected")
	}
	for _, leaked := range []string{"Paste", "prompted", ">"} {
		if strings.Contains(got.URL, leaked) {
			t.Errorf("the recovered link carries %q from the line under it: it is not a link that will load", leaked)
		}
	}
	if strings.ContainsAny(got.URL, " \t") {
		t.Error("the recovered link carries whitespace")
	}
	if !strings.HasSuffix(got.URL, "EXAMPLE") {
		t.Errorf("the link does not end where the URL ends; it ends %q", tail(got.URL))
	}
}

// tail is the last few bytes of a URL, for a failure message that shows where it
// stopped without printing a link.
func tail(url string) string {
	const show = 20
	if len(url) <= show {
		return url
	}
	return "…" + url[len(url)-show:]
}
