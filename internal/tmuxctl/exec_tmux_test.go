//go:build tmux

package tmuxctl

// The only tests in this repo that need a real tmux binary. They are excluded
// from `go test ./...` and run with:
//
//	go test -tags tmux ./internal/tmuxctl
//
// Everything runs on a private server socket, never tmux's default, so the
// kill-server in the cleanup can only ever reach sessions these tests made.
// None of them call t.Parallel.

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// tmuxPaneBound is deliberately far above any screen the tmux these tests start
// can render, because nothing here is about the bound: the stubbed suite in
// exec_test.go owns that, with a screen it composes itself. A bound at a real
// screen's own height would make every capture below a coin-flip on whatever
// `default-size` the host's tmux was built or configured with.
const tmuxPaneBound = 1000

// newTestExec gives each test its own tmux server. One shared server was enough
// for a test's kill-server to race the next test's new-session — the loser sees
// "server exited unexpectedly" — and it would also let one test's leftover
// session turn up in another's List.
func newTestExec(t *testing.T) *Exec {
	t.Helper()

	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skipf("tmux is not installed: %v", err)
	}

	// sessionEnv is set because run refuses without one. A struct literal here
	// is exactly the "one keystroke away" case ErrNoSessionEnv guards, and these
	// tests start real shells: a pane with no PATH would fail in ways that look
	// like tmux misbehaving rather than like a test building the wrong Exec.
	e := &Exec{socket: socketFor(t.Name()), paneBound: tmuxPaneBound, sessionEnv: realSessionEnv()}
	t.Cleanup(func() {
		// Constant argv, and -L keeps this off the operator's own server.
		out, err := exec.Command("tmux", "-L", e.socket, "kill-server").CombinedOutput() //nolint:gosec // socket is socketFor(t.Name())
		if err != nil && !noServer(err, string(out)) {
			t.Logf("cleanup kill-server: %v: %s", err, out)
		}
	})
	return e
}

// realSessionEnv is enough environment for a real shell to run in a real pane,
// and nothing more. It is what a composed session environment looks like.
func realSessionEnv() []string {
	env := []string{"TERM=xterm"}
	for _, name := range []string{"HOME", "PATH", "SHELL", "USER", "LOGNAME"} {
		if v := os.Getenv(name); v != "" {
			env = append(env, name+"="+v)
		}
	}
	return env
}

// socketFor makes a test name usable as the filename tmux turns -L into.
func socketFor(name string) string {
	return "crswd-test-" + strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			return r
		default:
			return '-'
		}
	}, name)
}

// waitFor polls rather than sleeps: these tests wait on a real shell starting
// and a real program reading its input, and a fixed sleep would be either flaky
// or slow.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()

	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func TestTmuxCreateHasKill(t *testing.T) {
	ctx := context.Background()
	e := newTestExec(t)
	const (
		name   = "crswd-11111111111111111111111111111111"
		anchor = "crswd-1a111111111111111111111111111111"
	)

	dir := t.TempDir()
	// tmux exits along with its last session, and a server that has exited
	// answers has-session with "no server running" — which this package refuses
	// to read as "gone". The anchor keeps the server up so this test is about
	// Kill and Has; the consequence of not having one is pinned in
	// TestTmuxKillingTheLastSessionStopsTheServer.
	if err := e.New(ctx, anchor, dir); err != nil {
		t.Fatalf("New anchor: %v", err)
	}
	if err := e.New(ctx, name, dir); err != nil {
		t.Fatalf("New: %v", err)
	}

	present, err := e.Has(ctx, name)
	if err != nil {
		t.Fatalf("Has after New: %v", err)
	}
	if !present {
		t.Fatal("Has = false right after New, want true")
	}

	if err := e.Kill(ctx, name); err != nil {
		t.Fatalf("Kill: %v", err)
	}

	present, err = e.Has(ctx, name)
	if err != nil {
		t.Fatalf("Has after Kill: %v", err)
	}
	if present {
		t.Error("Has = true after Kill, want false")
	}
}

// The exact-match target is the point: without the "=" prefix a tmux that
// prefix-matches would let the decoy answer for the real session.
func TestTmuxTargetsExactlyOneSession(t *testing.T) {
	ctx := context.Background()
	e := newTestExec(t)
	const (
		name  = "crswd-22222222222222222222222222222222"
		decoy = "crswd-22222222222222222222222222222222-decoy"
	)

	dir := t.TempDir()
	if err := e.New(ctx, name, dir); err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := e.New(ctx, decoy, dir); err != nil {
		t.Fatalf("New decoy: %v", err)
	}

	if err := e.Kill(ctx, name); err != nil {
		t.Fatalf("Kill: %v", err)
	}

	survived, err := e.Has(ctx, decoy)
	if err != nil {
		t.Fatalf("Has decoy: %v", err)
	}
	if !survived {
		t.Error("killing the session also killed the lookalike")
	}
}

// Pins the two stderr strings exec.go matches on against the tmux actually
// installed here. If a future tmux reworded either, the discrimination in Has
// and List would silently change meaning — and this is the test that notices.
func TestTmuxHasSeparatesAbsenceFromFailure(t *testing.T) {
	ctx := context.Background()
	e := newTestExec(t)
	const name = "crswd-33333333333333333333333333333333"

	if err := e.New(ctx, name, t.TempDir()); err != nil {
		t.Fatalf("New: %v", err)
	}

	present, err := e.Has(ctx, "crswd-nosuchsessionanywhere")
	if err != nil {
		t.Fatalf("Has on a live server for an unknown session: %v", err)
	}
	if present {
		t.Error("Has = true for a session that was never created")
	}

	if err := exec.Command("tmux", "-L", e.socket, "kill-server").Run(); err != nil { //nolint:gosec // socket is socketFor(t.Name())
		t.Fatalf("kill-server: %v", err)
	}

	if present, err := e.Has(ctx, name); err == nil {
		t.Errorf("Has with no server = (%v, nil), want an error — nothing answered", present)
	}
}

// Killing the last session takes the tmux server with it, so the Has that was
// supposed to confirm the teardown cannot answer at all. T028's verified
// destroy has to reckon with that, and this is what it looks like — pinned here
// so it is a known behaviour rather than a surprise in the destroy handler.
func TestTmuxKillingTheLastSessionStopsTheServer(t *testing.T) {
	ctx := context.Background()
	e := newTestExec(t)
	const name = "crswd-88888888888888888888888888888888"

	if err := e.New(ctx, name, t.TempDir()); err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := e.Kill(ctx, name); err != nil {
		t.Fatalf("Kill: %v", err)
	}

	present, err := e.Has(ctx, name)
	if err == nil {
		t.Fatalf("Has after killing the only session = (%v, nil): tmux now outlives its last session, so the destroy path is simpler than PROGRESS.md records", present)
	}
	if !strings.Contains(err.Error(), msgNoServer) {
		t.Errorf("Has error = %v, want it to name %q", err, msgNoServer)
	}

	// The way out for T028: no server is no sessions, which List reports as the
	// empty slice, so a teardown can still be confirmed rather than assumed.
	sessions, err := e.List(ctx)
	if err != nil {
		t.Fatalf("List once the server stopped: %v", err)
	}
	if len(sessions) != 0 {
		t.Errorf("List = %v, want empty", sessions)
	}
}

func TestTmuxListReportsProvenanceAndCreation(t *testing.T) {
	ctx := context.Background()
	e := newTestExec(t)
	const (
		ours   = "crswd-44444444444444444444444444444444"
		theirs = "notours"
	)

	empty, err := e.List(ctx)
	if err != nil {
		t.Fatalf("List with no server: %v", err)
	}
	if len(empty) != 0 {
		t.Fatalf("List with no server = %v, want empty", empty)
	}

	before := time.Now().Add(-2 * time.Second)
	dir := t.TempDir()
	if err := e.New(ctx, ours, dir); err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := e.SetOption(ctx, ours, "@crswd-managed", "1"); err != nil {
		t.Fatalf("SetOption: %v", err)
	}
	if err := e.New(ctx, theirs, dir); err != nil {
		t.Fatalf("New unmanaged: %v", err)
	}

	sessions, err := e.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(sessions) != 2 {
		t.Fatalf("List returned %d sessions, want 2: %v", len(sessions), sessions)
	}

	seen := make(map[string]SessionInfo, len(sessions))
	for _, s := range sessions {
		seen[s.Name] = s
	}
	if !seen[ours].Managed {
		t.Error("the session we marked came back unmanaged")
	}
	if seen[theirs].Managed {
		t.Error("a session we never marked came back managed")
	}
	// The absolute deadline of an adopted session derives from this, so a zero
	// or wildly wrong value would silently move every adopted session's expiry.
	if got := seen[ours].Created; got.Before(before) || got.After(time.Now().Add(time.Minute)) {
		t.Errorf("Created = %v, want a time around now (%v)", got, time.Now())
	}
	// The whole premise of measuring idle from the host: that #{session_activity}
	// is a format this tmux knows and renders as a Unix timestamp. A tmux that
	// did not would render the literal text, the parser would read it as
	// unreadable, and every session would quietly fall back to the old clock
	// with nothing failing to say so. This is what notices.
	if got := seen[ours].Activity; got.Before(before) || got.After(time.Now().Add(time.Minute)) {
		t.Errorf("Activity = %v, want a time around now (%v) — this tmux may not render #{session_activity}", got, time.Now())
	}
}

// Byte-for-byte delivery of the payloads research D4 showed send-keys -l
// mangles. The session runs `cat > file`, so what lands is what the program in
// the pane actually read — not what a shell decided to do about it.
func TestTmuxPasteDeliversHostileTextByteForByte(t *testing.T) {
	tests := []struct {
		name    string
		session string
		payload string
	}{
		{"a lone semicolon", "crswd-5a000000000000000000000000000000", ";"},
		{"a trailing semicolon", "crswd-5b000000000000000000000000000000", "foo;"},
		{"two trailing semicolons", "crswd-5c000000000000000000000000000000", "foo;;"},
		{"a command injection attempt", "crswd-5d000000000000000000000000000000", "a; echo PWNED; $(id) `whoami`"},
		{"an embedded newline", "crswd-5e000000000000000000000000000000", "line one\nline two"},
	}

	ctx := context.Background()
	e := newTestExec(t)

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			sink := filepath.Join(dir, "landed.txt")

			if err := e.New(ctx, tc.session, dir); err != nil {
				t.Fatalf("New: %v", err)
			}
			// Daemon-authored, like the claude start command: SendKeys is only
			// ever handed constants the daemon wrote.
			if err := e.SendKeys(ctx, tc.session, "cat > "+sink); err != nil {
				t.Fatalf("SendKeys: %v", err)
			}
			if err := e.SendKeys(ctx, tc.session, "Enter"); err != nil {
				t.Fatalf("SendKeys Enter: %v", err)
			}
			waitFor(t, "the shell to start cat", func() bool {
				_, err := os.Stat(sink)
				return err == nil
			})

			if err := e.Paste(ctx, tc.session, []byte(tc.payload)); err != nil {
				t.Fatalf("Paste: %v", err)
			}
			if err := e.SendKeys(ctx, tc.session, "Enter"); err != nil {
				t.Fatalf("SendKeys Enter: %v", err)
			}

			want := []byte(tc.payload + "\n")
			var got []byte
			waitFor(t, "the payload to land", func() bool {
				b, err := os.ReadFile(sink) //nolint:gosec // sink is this test's own t.TempDir
				if err != nil {
					return false
				}
				got = b
				return len(b) >= len(want)
			})
			if !bytes.Equal(got, want) {
				t.Errorf("landed %q, want %q", got, want)
			}

			if err := e.Kill(ctx, tc.session); err != nil {
				t.Fatalf("Kill: %v", err)
			}
		})
	}
}

// Settles the open question from T004: argvSendKeys omits -l, because -l would
// send "Enter" as five characters instead of the Enter key. The daemon's start
// command must still arrive literally under that same shape.
func TestTmuxSendKeysDeliversTheStartCommandLiterally(t *testing.T) {
	ctx := context.Background()
	e := newTestExec(t)
	const (
		name  = "crswd-66666666666666666666666666666666"
		start = "claude --dangerously-skip-permissions"
	)

	if err := e.New(ctx, name, t.TempDir()); err != nil {
		t.Fatalf("New: %v", err)
	}
	// Typed onto the prompt and deliberately never submitted: this asserts how
	// the characters arrive, and nothing more should happen than that.
	if err := e.SendKeys(ctx, name, start); err != nil {
		t.Fatalf("SendKeys: %v", err)
	}

	var pane string
	waitFor(t, "the start command to appear on the prompt", func() bool {
		out, err := e.CapturePane(ctx, name)
		if err != nil {
			t.Fatalf("CapturePane: %v", err)
		}
		pane = out
		return strings.Contains(out, start)
	})
	if !strings.Contains(pane, start) {
		t.Errorf("pane = %q, want it to contain %q", pane, start)
	}
}

// capture-pane without -e returns the rendered screen, so colour written by the
// program in the pane comes back as text. With -e it would come back as real
// ESC bytes, headed for the API and eventually a browser.
func TestTmuxCapturePaneCarriesNoEscapeBytes(t *testing.T) {
	ctx := context.Background()
	e := newTestExec(t)
	const name = "crswd-77777777777777777777777777777777"

	if err := e.New(ctx, name, t.TempDir()); err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := e.SendKeys(ctx, name, `printf '\033[31mCRIMSON\033[0m\n'`); err != nil {
		t.Fatalf("SendKeys: %v", err)
	}
	if err := e.SendKeys(ctx, name, "Enter"); err != nil {
		t.Fatalf("SendKeys Enter: %v", err)
	}

	var pane string
	waitFor(t, "the coloured output to be drawn", func() bool {
		out, err := e.CapturePane(ctx, name)
		if err != nil {
			t.Fatalf("CapturePane: %v", err)
		}
		pane = out
		return strings.Contains(out, "CRIMSON")
	})

	if strings.ContainsRune(pane, 0x1b) {
		t.Errorf("captured pane carries an ESC byte: %q", pane)
	}
}
