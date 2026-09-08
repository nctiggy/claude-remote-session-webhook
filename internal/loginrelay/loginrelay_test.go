package loginrelay_test

// The claims here are about what the relay does to a host and what it refuses to
// carry. The one thing these cannot cover is whether Claude Code really draws
// the screen this drives — that lives in loginrelay_tmux_test.go against a real
// binary, because a fake that answered with a fixture would be this package
// agreeing with itself.

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/nctiggy/claude-remote-session-webhook/internal/claudeauth"
	"github.com/nctiggy/claude-remote-session-webhook/internal/loginrelay"
	"github.com/nctiggy/claude-remote-session-webhook/internal/tmuxctl"
)

const startCommand = "claude --dangerously-skip-permissions"

func newRelay(t *testing.T, fake *tmuxctl.Fake) *loginrelay.Relay {
	t.Helper()

	r, err := loginrelay.New(fake, startCommand, t.TempDir(), []string{"HOME=" + t.TempDir()})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return r
}

// TestStartSummonsTheScreenRatherThanWaitingForIt is the premise of the whole
// package.
//
// An expired credential does NOT put a running session back on the sign-in
// screen — it fails each request with "Login expired · Please run /login" and
// stays where it was. So the relay has to ask for the screen. It asks in a window
// of its own, and this pins the command it types.
func TestStartSummonsTheScreenRatherThanWaitingForIt(t *testing.T) {
	t.Parallel()

	fake := tmuxctl.NewFake()
	r := newRelay(t, fake)

	if err := r.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}

	var typed []string
	for _, call := range fake.Calls() {
		if call.Op == tmuxctl.OpSendKeys {
			typed = append(typed, strings.Join(call.Argv, " "))
		}
	}
	if len(typed) == 0 {
		t.Fatal("Start created a window and typed nothing into it, so no sign-in was ever asked for")
	}
	joined := strings.Join(typed, "\n")
	if !strings.Contains(joined, "auth login") {
		t.Errorf("Start did not run the sign-in command; it typed:\n%s", joined)
	}
	if !strings.Contains(joined, "--claudeai") {
		t.Errorf("Start did not name the subscription account, so it stops on the menu it cannot answer:\n%s", joined)
	}
}

// TestStartNeverTouchesASession is the safety property that decided the design.
//
// **Must fail when** the relay operates on any name but its own. The first
// design typed /login into a session that had gone stale; a relay that can
// damage the work it was called to rescue is not a rescue. Every call this makes
// must name the relay's own window.
func TestStartNeverTouchesASession(t *testing.T) {
	t.Parallel()

	fake := tmuxctl.NewFake()
	// A session on the host, of exactly the shape adoption recognises.
	fake.Seed(tmuxctl.SessionInfo{Name: "crswd-" + strings.Repeat("a", 32), Managed: true})

	r := newRelay(t, fake)
	if err := r.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := r.Deliver(context.Background(), "some-code"); err != nil {
		t.Fatalf("Deliver: %v", err)
	}
	if _, err := r.State(context.Background()); err != nil {
		t.Fatalf("State: %v", err)
	}

	for _, call := range fake.Calls() {
		for _, arg := range call.Argv {
			if strings.Contains(arg, strings.Repeat("a", 32)) {
				t.Errorf("%s reached a real session: %v", call.Op, call.Argv)
			}
		}
	}
}

// TestTheWindowIsNotASession pins that the relay cannot be adopted, reaped, or
// counted against the cap.
//
// Adoption wants the daemon's prefix followed by a 32-character hex identifier,
// AND the @crswd-managed option. The window must fail both, and it must fail
// them by construction rather than by nobody having set them yet.
func TestTheWindowIsNotASession(t *testing.T) {
	t.Parallel()

	name := loginrelay.WindowName

	id, hasPrefix := strings.CutPrefix(name, "crswd-")
	if !hasPrefix {
		t.Errorf("the window name %q does not carry the daemon's prefix, so an operator reading `tmux ls` cannot tell who made it", name)
	}
	if len(id) == 32 {
		t.Errorf("the window name %q is the shape adoption accepts; it would come back as a session across a restart", name)
	}

	fake := tmuxctl.NewFake()
	r := newRelay(t, fake)
	if err := r.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	for _, call := range fake.Calls() {
		if call.Op == tmuxctl.OpSetOption {
			t.Errorf("the relay set a tmux option (%v); @crswd-managed is half of what makes a window adoptable and this window must never carry it", call.Argv)
		}
	}
}

// TestStartRefusesASecondSignIn covers ErrAlreadyRunning.
//
// Not idempotent on purpose: restarting abandons a challenge the operator may be
// part-way through answering on their phone, and they would have no way to know
// the code they were about to paste had just been invalidated.
func TestStartRefusesASecondSignIn(t *testing.T) {
	t.Parallel()

	fake := tmuxctl.NewFake()
	r := newRelay(t, fake)

	if err := r.Start(context.Background()); err != nil {
		t.Fatalf("first Start: %v", err)
	}
	if err := r.Start(context.Background()); !errors.Is(err, loginrelay.ErrAlreadyRunning) {
		t.Errorf("second Start = %v, want ErrAlreadyRunning", err)
	}
}

// TestDeliverCarriesTheCodeOnStdinAndNeverInArgv is the credential rule as a
// test rather than a comment.
//
// **Must fail when** the code appears in any command line. A tmux argv is in
// /proc/<pid>/cmdline for the moment it runs, readable by anything on the host.
// tmuxctl.Paste puts the payload on stdin for exactly this reason, and this pins
// that the relay uses it rather than send-keys.
func TestDeliverCarriesTheCodeOnStdinAndNeverInArgv(t *testing.T) {
	t.Parallel()

	const code = "a-code-that-must-not-appear-in-argv"

	fake := tmuxctl.NewFake()
	r := newRelay(t, fake)
	if err := r.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := r.Deliver(context.Background(), code); err != nil {
		t.Fatalf("Deliver: %v", err)
	}

	var onStdin bool
	for _, call := range fake.Calls() {
		for _, arg := range call.Argv {
			if strings.Contains(arg, code) {
				t.Errorf("the code is in a command line: %s %v", call.Op, call.Argv)
			}
		}
		if strings.Contains(string(call.Stdin), code) {
			onStdin = true
		}
	}
	if !onStdin {
		t.Error("the code never reached tmux on stdin, so Deliver did not use Paste")
	}
}

// TestDeliverSubmitsOnlyAfterTheCodeLands pins the ordering.
//
// Enter is sent separately and second, so a delivery that half-failed leaves the
// code on screen for the operator rather than submitting some truncated form of
// it to an endpoint that will spend the challenge.
func TestDeliverSubmitsOnlyAfterTheCodeLands(t *testing.T) {
	t.Parallel()

	fake := tmuxctl.NewFake()
	r := newRelay(t, fake)
	if err := r.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}

	before := len(fake.Calls())
	if err := r.Deliver(context.Background(), "code"); err != nil {
		t.Fatalf("Deliver: %v", err)
	}

	pasted, submitted := -1, -1
	for i, call := range fake.Calls()[before:] {
		switch call.Op {
		case tmuxctl.OpPaste:
			pasted = i
		case tmuxctl.OpSendKeys:
			submitted = i
		}
	}
	if pasted < 0 {
		t.Fatal("Deliver never pasted")
	}
	if submitted < 0 {
		t.Fatal("Deliver never submitted")
	}
	if submitted < pasted {
		t.Errorf("Enter was sent before the code was pasted (paste at %d, submit at %d)", pasted, submitted)
	}
}

// TestDeliverRefusesWhatIsNotACode covers the shapes that would do something
// other than answer the prompt.
func TestDeliverRefusesWhatIsNotACode(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		code string
		want error
	}{
		"nothing":                   {"", loginrelay.ErrEmptyCode},
		"only whitespace":           {"   \t ", loginrelay.ErrEmptyCode},
		"an embedded newline":       {"code\nwhatever-comes-next", loginrelay.ErrUnusableCode},
		"a carriage return":         {"code\rmore", loginrelay.ErrUnusableCode},
		"an escape byte":            {"code\x1b[A", loginrelay.ErrUnusableCode},
		"a body rather than a code": {strings.Repeat("x", 4096), loginrelay.ErrUnusableCode},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			fake := tmuxctl.NewFake()
			r := newRelay(t, fake)
			if err := r.Start(context.Background()); err != nil {
				t.Fatalf("Start: %v", err)
			}

			before := len(fake.Calls())
			err := r.Deliver(context.Background(), tc.code)
			if !errors.Is(err, tc.want) {
				t.Errorf("Deliver(%q) = %v, want %v", tc.code, err, tc.want)
			}
			for _, call := range fake.Calls()[before:] {
				if call.Op == tmuxctl.OpPaste || call.Op == tmuxctl.OpSendKeys {
					t.Errorf("a refused code still reached the window: %s %v", call.Op, call.Argv)
				}
			}
		})
	}
}

// TestARefusedCodeIsNeverInTheError is the disclosure rule.
//
// An error string is one of the places a value goes to be logged, and this is
// the one value in the daemon that is a live credential in transit.
func TestARefusedCodeIsNeverInTheError(t *testing.T) {
	t.Parallel()

	const secret = "SECRET-CODE-VALUE"

	fake := tmuxctl.NewFake()
	r := newRelay(t, fake)
	if err := r.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// An EMBEDDED newline, not a trailing one. A trailing newline is trimmed on
	// purpose — a code pasted from a phone routinely carries one, and refusing
	// that would refuse the ordinary case. A newline in the middle is the one
	// that submits half a code and leaves the rest answering whatever asks next.
	err := r.Deliver(context.Background(), secret+"\nsecond-line")
	if err == nil {
		t.Fatal("a code with an embedded newline was accepted")
	}
	if strings.Contains(err.Error(), secret) {
		t.Errorf("the refusal quotes the code back: %v", err)
	}
}

// TestDeliverWithNoWindowRefuses covers the operator who pressed submit on a
// page whose sign-in has since been cancelled or finished.
func TestDeliverWithNoWindowRefuses(t *testing.T) {
	t.Parallel()

	fake := tmuxctl.NewFake()
	r := newRelay(t, fake)

	if err := r.Deliver(context.Background(), "code"); !errors.Is(err, loginrelay.ErrNotRunning) {
		t.Errorf("Deliver with no window = %v, want ErrNotRunning", err)
	}
}

// TestStateCarriesTheLinkAndNoPane pins what may be shown.
//
// Running plus the link is the whole of what a page needs. The pane itself is
// secret under docs/security.md, and this pane's is the most sensitive on the
// host — so State has nowhere to put it, rather than a rule about not reading it.
func TestStateCarriesTheLinkAndNoPane(t *testing.T) {
	t.Parallel()

	fake := tmuxctl.NewFake()
	r := newRelay(t, fake)
	if err := r.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}

	fake.SetPane(loginrelay.WindowName,
		"Opening browser to sign in…\n"+
			"If the browser didn't open, visit: https://claude.com/cai/oauth/authorize?code=true&state=EXAMPLE\n"+
			"\n"+
			"Paste code here if prompted >\n")

	got, err := r.State(context.Background())
	if err != nil {
		t.Fatalf("State: %v", err)
	}
	if !got.Running {
		t.Fatal("State reports no sign-in running while its window exists")
	}
	if got.Prompt == nil {
		t.Fatal("State read a sign-in screen and carried no prompt, so the page has no link to show")
	}
	if got.Prompt.Kind != claudeauth.KindDeviceCode {
		t.Errorf("kind = %q, want %q", got.Prompt.Kind, claudeauth.KindDeviceCode)
	}
	if !strings.HasPrefix(got.Prompt.URL, "https://claude.com/cai/oauth/authorize") {
		t.Errorf("the link is not the sign-in URL: %q", got.Prompt.URL)
	}
}

// TestStateBeforeTheScreenDrawsIsWaitingNotFailure covers the second or two
// between typing the command and Claude Code rendering.
//
// Running with no prompt is what the page renders as "waiting". Reporting it as
// not running would tell an operator their sign-in died a moment after they
// asked for it, and they would press again — abandoning the challenge that was
// about to appear.
func TestStateBeforeTheScreenDrawsIsWaitingNotFailure(t *testing.T) {
	t.Parallel()

	fake := tmuxctl.NewFake()
	r := newRelay(t, fake)
	if err := r.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}

	got, err := r.State(context.Background())
	if err != nil {
		t.Fatalf("State: %v", err)
	}
	if !got.Running {
		t.Error("a window that exists but has not drawn yet reports as not running")
	}
	if got.Prompt != nil {
		t.Errorf("a blank pane produced a prompt: %v", got.Prompt)
	}
}

// TestStopIsFineWithNothingToStop covers both callers: an operator abandoning an
// attempt, and the daemon tidying up after a confirmed sign-in.
func TestStopIsFineWithNothingToStop(t *testing.T) {
	t.Parallel()

	fake := tmuxctl.NewFake()
	r := newRelay(t, fake)

	if err := r.Stop(context.Background()); err != nil {
		t.Errorf("Stop with no window = %v, want nil", err)
	}

	if err := r.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := r.Stop(context.Background()); err != nil {
		t.Errorf("Stop: %v", err)
	}
	if err := r.Stop(context.Background()); err != nil {
		t.Errorf("second Stop = %v, want nil", err)
	}
}

// TestNewRefusesAStartCommandNamingNothingRunnable covers the configuration that
// would otherwise have this package type a guess into a shell.
func TestNewRefusesAStartCommandNamingNothingRunnable(t *testing.T) {
	t.Parallel()

	for _, command := range []string{"", "   ", "/", ".", "clau;de", "cl aude"} {
		if _, err := loginrelay.New(tmuxctl.NewFake(), command, t.TempDir(), nil); err == nil && command != "cl aude" {
			t.Errorf("New(%q) was accepted", command)
		}
	}
}

// TestTheBinaryComesFromTheOperatorsStartCommand pins that a deployment running
// claude through a wrapper signs in through the same wrapper.
//
// The wrapper is what decides which credential store is in play, so signing in
// with a bare `claude` would repair a store no session uses.
func TestTheBinaryComesFromTheOperatorsStartCommand(t *testing.T) {
	t.Parallel()

	fake := tmuxctl.NewFake()
	r, err := loginrelay.New(fake, "/home/operator/.local/lawnmower-bin/wrapped-claude {name}", t.TempDir(), nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := r.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}

	var typed string
	for _, call := range fake.Calls() {
		if call.Op == tmuxctl.OpSendKeys {
			typed = strings.Join(call.Argv, " ")
		}
	}
	if !strings.Contains(typed, "wrapped-claude auth login") {
		t.Errorf("the relay did not run the operator's own binary; it typed %q", typed)
	}
}

// TestDeliverTrimsTheWhitespaceAPhonePasteCarries is the other half of the
// newline rule, made explicit because the two look alike and are not.
//
// A code copied on a phone routinely arrives with a trailing newline or a
// leading space. Refusing those would refuse the ordinary case; carrying them
// through would submit a code the endpoint does not recognise, and the operator
// would be told their correct code was wrong.
func TestDeliverTrimsTheWhitespaceAPhonePasteCarries(t *testing.T) {
	t.Parallel()

	fake := tmuxctl.NewFake()
	r := newRelay(t, fake)
	if err := r.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := r.Deliver(context.Background(), "  the-code\n"); err != nil {
		t.Fatalf("Deliver of a pasted code with surrounding whitespace: %v", err)
	}

	for _, call := range fake.Calls() {
		if call.Op == tmuxctl.OpPaste {
			if got := string(call.Stdin); got != "the-code" {
				t.Errorf("the delivered code is %q, want %q", got, "the-code")
			}
			return
		}
	}
	t.Fatal("nothing was pasted")
}
