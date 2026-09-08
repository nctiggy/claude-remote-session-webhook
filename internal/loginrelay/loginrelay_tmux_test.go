//go:build tmux

package loginrelay_test

// The claims a fake cannot make: that Claude Code really draws the screen this
// package drives, that DetectPrompt really finds it in a real capture, and that
// the link really comes back whole from a real terminal's wrapping.
//
// # Why this is safe to run
//
// Everything here runs against an isolated HOME and CLAUDE_CONFIG_DIR, on a
// private tmux socket. So the credential store it touches is an empty one in a
// temp directory, never the operator's — measured before this was written:
// `claude auth status --json` under such a HOME reports loggedIn:false while the
// real one reports true, and `claude auth login` under it goes straight to the
// device-code screen with no onboarding in the way.
//
// Nothing here ever completes a sign-in. The relay is started, the screen is
// read, and the window is killed; the PKCE challenge that was printed is
// abandoned unspent, which is what happens to every challenge nobody answers.

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nctiggy/claude-remote-session-webhook/internal/claudeauth"
	"github.com/nctiggy/claude-remote-session-webhook/internal/loginrelay"
	"github.com/nctiggy/claude-remote-session-webhook/internal/tmuxctl"
)

// screenBudget is how long Claude Code gets to draw. It is generous on purpose:
// the command starts a Node process and reaches the network, and a flaky test
// that fails on a slow host teaches people to re-run rather than to read.
const screenBudget = 45 * time.Second

// isolatedRelay builds a relay whose every process runs against a temp HOME.
func isolatedRelay(t *testing.T) (*loginrelay.Relay, string) {
	t.Helper()

	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skipf("tmux is not installed: %v", err)
	}
	if _, err := exec.LookPath("claude"); err != nil {
		t.Skipf("claude is not installed: %v", err)
	}

	home := t.TempDir()
	env := []string{
		"HOME=" + home,
		// The credential store this run may read or write. Pointed at the temp
		// HOME so that nothing here can reach the operator's login even if the
		// flow were somehow completed.
		"CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"),
		"PATH=" + os.Getenv("PATH"),
		"TERM=xterm-256color",
	}

	// A socket named for the test, so a run cannot reach the operator's own tmux
	// server or another test's.
	socket := "crswd-relay-" + strings.ReplaceAll(t.Name(), "/", "-")
	tmux, err := tmuxctl.NewExec(socket, 200, env)
	if err != nil {
		t.Fatalf("NewExec: %v", err)
	}
	t.Cleanup(func() {
		out, err := exec.Command("tmux", "-L", socket, "kill-server").CombinedOutput() //nolint:gosec // socket is derived from t.Name()
		if err != nil && !strings.Contains(string(out), "no server running") {
			t.Logf("cleanup kill-server: %v: %s", err, out)
		}
	})

	relay, err := loginrelay.New(tmux, "claude --dangerously-skip-permissions", home, env)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return relay, home
}

// TestRelayDrivesTheRealSignInScreen is the end-to-end claim, minus the one step
// only a person can take.
//
// **Must fail when** the relay cannot produce a link an operator could open. It
// is the test that would have caught the thing measurement caught by hand: the
// screen `claude auth login` draws is NOT the screen an interactive `claude`
// draws when it starts logged out. They share one phrase and put the URL in
// different places, so a detector built only from the cold-start capture reports
// the relay's own window as showing nothing, and the operator is handed a page
// with no link on it — the whole feature, missing, with every unit test green.
func TestRelayDrivesTheRealSignInScreen(t *testing.T) {
	relay, _ := isolatedRelay(t)
	ctx := context.Background()

	if err := relay.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}

	var got loginrelay.State
	deadline := time.Now().Add(screenBudget)
	for time.Now().Before(deadline) {
		var err error
		if got, err = relay.State(ctx); err != nil {
			t.Fatalf("State: %v", err)
		}
		if got.Prompt != nil {
			break
		}
		time.Sleep(250 * time.Millisecond)
	}

	if !got.Running {
		t.Fatal("the sign-in window is not running")
	}
	if got.Prompt == nil {
		t.Fatalf("no sign-in screen was detected within %s; the relay would show an operator a page with no link", screenBudget)
	}
	if got.Prompt.Kind != claudeauth.KindDeviceCode {
		t.Errorf("kind = %q, want %q", got.Prompt.Kind, claudeauth.KindDeviceCode)
	}

	// The link is the entire product of this feature. Asserted on shape rather
	// than logged: it carries a one-shot PKCE challenge, and a test that printed
	// it would put a live credential in CI output.
	url := got.Prompt.URL
	if !strings.HasPrefix(url, "https://claude.com/") {
		t.Fatalf("the recovered link is not a sign-in URL (%d bytes, prefix %q)", len(url), safePrefix(url))
	}
	for _, required := range []string{"code_challenge=", "code_challenge_method=", "state="} {
		if !strings.Contains(url, required) {
			t.Errorf("the recovered link is missing %s, so it was truncated at the terminal's wrap", required)
		}
	}
	if strings.ContainsAny(url, " \t\n") {
		t.Error("the recovered link carries whitespace, so it was not rebuilt across the wrap")
	}
	// A real challenge is long. A fragment would look like a working link and go
	// somewhere useless, which is worse than none.
	if len(url) < 200 {
		t.Errorf("the recovered link is %d bytes, which is a fragment rather than a sign-in URL", len(url))
	}
}

// TestRelayStopEndsTheWindow pins that abandoning an attempt really ends the
// process, rather than leaving a Node process and a spent challenge on the host.
func TestRelayStopEndsTheWindow(t *testing.T) {
	relay, _ := isolatedRelay(t)
	ctx := context.Background()

	if err := relay.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if state, err := relay.State(ctx); err != nil || !state.Running {
		t.Fatalf("State after Start = %+v, %v; want running", state, err)
	}

	if err := relay.Stop(ctx); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	state, err := relay.State(ctx)
	if err != nil {
		t.Fatalf("State after Stop: %v", err)
	}
	if state.Running {
		t.Error("the sign-in window survived Stop")
	}
}

// TestSignedInReadsTheRealCLI is why the relay can tell an operator anything
// true about whether signing in worked.
//
// The screen cannot answer that: the wording Claude Code prints after a
// successful sign-in has never been captured here, and this project does not add
// a signature it has not seen. `auth status --json` is a documented answer, and
// this pins that the daemon reads it correctly — against a store that is
// genuinely empty, which is the answer this test can produce without anybody
// signing in to anything.
func TestSignedInReadsTheRealCLI(t *testing.T) {
	relay, home := isolatedRelay(t)

	signedIn, err := relay.SignedIn(context.Background())
	if err != nil {
		t.Fatalf("SignedIn: %v", err)
	}
	if signedIn {
		t.Errorf("SignedIn reported a login for an empty credential store at %s; "+
			"either the isolation leaked to the operator's real store or the JSON is being misread", home)
	}
}

// safePrefix is the most of a URL that may appear in a failure message: enough
// to see what kind of string came back, never enough to be a usable link.
func safePrefix(url string) string {
	const show = 24
	if len(url) <= show {
		return url
	}
	return url[:show] + "…"
}
