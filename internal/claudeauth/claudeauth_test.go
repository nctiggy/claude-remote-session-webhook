package claudeauth_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nctiggy/claude-remote-session-webhook/internal/claudeauth"
)

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
