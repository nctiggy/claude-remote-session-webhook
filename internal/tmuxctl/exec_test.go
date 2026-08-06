package tmuxctl

// Internal, unlike the rest of the package's tests: parseSessions and the
// stubbed Exec are the subjects here, and both are unexported. The real-tmux
// half of T005 lives in exec_tmux_test.go behind //go:build tmux; everything
// below runs in CI with no tmux binary anywhere, by putting a stub named tmux
// at the front of PATH. That keeps the argv, the exit status, and the stderr
// discrimination under test rather than under review.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

const (
	execName    = "crswd-9f2c4a1b8e6d3f7a0c5b2e9d4f1a7c3b"
	execWorkDir = "/home/u/code/repo"

	stubEnv       = "CRSWD_TEST_STUB"
	stubRecordEnv = "CRSWD_TEST_STUB_RECORD"
	stubStdoutEnv = "CRSWD_TEST_STUB_STDOUT"
	stubStderrEnv = "CRSWD_TEST_STUB_STDERR"
	stubCodeEnv   = "CRSWD_TEST_STUB_CODE"
)

var _ Controller = (*Exec)(nil)

// execSocket is the server name every stubbed Exec below drives. Production
// derives one from the listen address; these tests only need a fixed one that
// is not tmux's default.
const execSocket = "crswd-127-0-0-1-8765"

// newStubExec is the Exec the stub on PATH answers for.
func newStubExec(t *testing.T) *Exec {
	t.Helper()

	e, err := NewExec(execSocket)
	if err != nil {
		t.Fatalf("NewExec: %v", err)
	}
	return e
}

// TestMain doubles as the stub's entry point. The check has to happen before
// m.Run parses flags, because the child is handed tmux's arguments, not Go's.
func TestMain(m *testing.M) {
	if os.Getenv(stubEnv) != "" {
		os.Exit(runStub())
	}
	os.Exit(m.Run())
}

// runStub is this binary standing in for the tmux it replaced on PATH. It
// records the argv and stdin it was handed, then reproduces a chosen exit
// status, stdout, and stderr. It runs in a child process; nothing here is a test.
func runStub() int {
	if path := os.Getenv(stubRecordEnv); path != "" {
		stdin, err := io.ReadAll(os.Stdin)
		if err != nil {
			fmt.Fprintln(os.Stderr, "stub: read stdin:", err)
			return 125
		}
		row, err := json.Marshal(stubCall{Argv: os.Args, Stdin: stdin})
		if err != nil {
			fmt.Fprintln(os.Stderr, "stub: marshal:", err)
			return 125
		}
		f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600) //nolint:gosec // the parent test handed us its own t.TempDir
		if err != nil {
			fmt.Fprintln(os.Stderr, "stub: open record:", err)
			return 125
		}
		if _, err := f.Write(append(row, '\n')); err != nil {
			fmt.Fprintln(os.Stderr, "stub: write record:", err)
			return 125
		}
		if err := f.Close(); err != nil {
			fmt.Fprintln(os.Stderr, "stub: close record:", err)
			return 125
		}
	}

	if out := os.Getenv(stubStdoutEnv); out != "" {
		if _, err := fmt.Fprint(os.Stdout, out); err != nil {
			fmt.Fprintln(os.Stderr, "stub: write stdout:", err)
			return 125
		}
	}
	if msg := os.Getenv(stubStderrEnv); msg != "" {
		fmt.Fprintln(os.Stderr, msg)
	}

	code, err := strconv.Atoi(os.Getenv(stubCodeEnv))
	if err != nil {
		return 0
	}
	return code
}

// stubCall is one recorded invocation of the stub, mirroring what Fake.Call
// records so the two halves of the package describe a call the same way.
type stubCall struct {
	Argv  []string `json:"argv"`
	Stdin []byte   `json:"stdin"`
}

type stub struct {
	code   int
	stdout string
	stderr string
}

// install puts the stub on PATH as "tmux" and returns a reader for what it
// recorded. A symlink to the test binary avoids writing an executable script,
// so no shell is involved in testing the package whose point is that no shell
// is ever involved.
//
// Every test using this mutates the environment and therefore cannot call
// t.Parallel — t.Setenv panics if it does.
func (s stub) install(t *testing.T) func(*testing.T) []stubCall {
	t.Helper()

	self, err := os.Executable()
	if err != nil {
		t.Fatalf("locate test binary: %v", err)
	}
	dir := t.TempDir()
	if err := os.Symlink(self, filepath.Join(dir, "tmux")); err != nil {
		t.Fatalf("link stub tmux: %v", err)
	}
	record := filepath.Join(dir, "calls.jsonl")

	t.Setenv("PATH", dir)
	t.Setenv(stubEnv, "1")
	t.Setenv(stubRecordEnv, record)
	t.Setenv(stubStdoutEnv, s.stdout)
	t.Setenv(stubStderrEnv, s.stderr)
	t.Setenv(stubCodeEnv, strconv.Itoa(s.code))

	return func(t *testing.T) []stubCall {
		t.Helper()
		raw, err := os.ReadFile(record) //nolint:gosec // path is this test's own t.TempDir
		if err != nil {
			t.Fatalf("read recorded calls: %v", err)
		}
		var calls []stubCall
		for _, line := range strings.Split(strings.TrimRight(string(raw), "\n"), "\n") {
			var c stubCall
			if err := json.Unmarshal([]byte(line), &c); err != nil {
				t.Fatalf("decode recorded call %q: %v", line, err)
			}
			calls = append(calls, c)
		}
		return calls
	}
}

// noTmux points PATH at an empty directory, so exec fails before a process
// exists. That is the case a controller must never mistake for an answer.
func noTmux(t *testing.T) {
	t.Helper()
	t.Setenv("PATH", t.TempDir())
}

// The argv the real controller hands to tmux, written out rather than compared
// against the builders, because a test that calls the builders would agree with
// them however wrong they both were. These literals are the same contract
// fake_test.go pins, which is what keeps the fake and this file from drifting.
func TestExecSendsTheContractArgv(t *testing.T) {
	ctx := context.Background()
	recorded := stub{}.install(t)
	e := newStubExec(t)

	if err := e.New(ctx, execName, execWorkDir); err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := e.SetOption(ctx, execName, "@crswd-managed", "1"); err != nil {
		t.Fatalf("SetOption: %v", err)
	}
	if err := e.SendKeys(ctx, execName, "Enter"); err != nil {
		t.Fatalf("SendKeys: %v", err)
	}
	if err := e.Paste(ctx, execName, []byte("hello")); err != nil {
		t.Fatalf("Paste: %v", err)
	}
	if _, err := e.CapturePane(ctx, execName); err != nil {
		t.Fatalf("CapturePane: %v", err)
	}
	if err := e.Kill(ctx, execName); err != nil {
		t.Fatalf("Kill: %v", err)
	}
	if _, err := e.Has(ctx, execName); err != nil {
		t.Fatalf("Has: %v", err)
	}
	if _, err := e.List(ctx); err != nil {
		t.Fatalf("List: %v", err)
	}

	want := []stubCall{
		{Argv: []string{"tmux", "-L", execSocket, "new-session", "-d", "-s", execName, "-c", execWorkDir}},
		{Argv: []string{"tmux", "-L", execSocket, "set-option", "-t", "=" + execName + ":", "@crswd-managed", "1"}},
		{Argv: []string{"tmux", "-L", execSocket, "send-keys", "-t", "=" + execName + ":", "--", "Enter"}},
		{Argv: []string{"tmux", "-L", execSocket, "load-buffer", "-b", execName, "-"}, Stdin: []byte("hello")},
		{Argv: []string{"tmux", "-L", execSocket, "paste-buffer", "-d", "-b", execName, "-t", "=" + execName + ":"}},
		{Argv: []string{"tmux", "-L", execSocket, "capture-pane", "-p", "-t", "=" + execName + ":"}},
		{Argv: []string{"tmux", "-L", execSocket, "kill-session", "-t", "=" + execName}},
		{Argv: []string{"tmux", "-L", execSocket, "has-session", "-t", "=" + execName}},
		{Argv: []string{"tmux", "-L", execSocket, "list-sessions", "-F", "#{session_name}|#{session_created}|#{@crswd-managed}|#{@crswd-name}|#{@crswd-workdir}"}},
	}

	got := recorded(t)
	if len(got) != len(want) {
		t.Fatalf("ran %d commands, want %d: %v", len(got), len(want), got)
	}
	for i := range want {
		if !slices.Equal(got[i].Argv, want[i].Argv) {
			t.Errorf("command %d argv =\n  %q\nwant\n  %q", i, got[i].Argv, want[i].Argv)
		}
		if !slices.Equal(got[i].Stdin, want[i].Stdin) {
			t.Errorf("command %d stdin = %q, want %q", i, got[i].Stdin, want[i].Stdin)
		}
	}
}

// The payloads from research D4 that send-keys -l mangles. Here the concern is
// narrower and prior: whatever they contain, they must ride on stdin and never
// appear in an argument, because an argument is where tmux's own parser — and
// anything else that ever reads a command line — could get at them.
func TestExecPasteKeepsCallerTextOffTheCommandLine(t *testing.T) {
	payloads := []string{
		";",
		"foo;",
		"foo;;",
		"a; echo PWNED; $(id) `whoami`",
		"line one\nline two",
		"--dangerously-skip-permissions",
	}

	for _, payload := range payloads {
		t.Run(fmt.Sprintf("%q", payload), func(t *testing.T) {
			recorded := stub{}.install(t)

			if err := newStubExec(t).Paste(context.Background(), execName, []byte(payload)); err != nil {
				t.Fatalf("Paste: %v", err)
			}

			calls := recorded(t)
			if len(calls) != 2 {
				t.Fatalf("Paste ran %d commands, want 2: %v", len(calls), calls)
			}
			if got := string(calls[0].Stdin); got != payload {
				t.Errorf("payload on stdin = %q, want %q", got, payload)
			}
			for i, c := range calls {
				for j, arg := range c.Argv {
					if strings.Contains(arg, payload) {
						t.Errorf("payload reached command %d argv[%d] = %q", i, j, arg)
					}
				}
			}
		})
	}
}

func TestExecCapturePaneReturnsTmuxOutputVerbatim(t *testing.T) {
	const pane = "$ echo hi\nhi\n$ \n"
	stub{stdout: pane}.install(t)

	got, err := newStubExec(t).CapturePane(context.Background(), execName)
	if err != nil {
		t.Fatalf("CapturePane: %v", err)
	}
	if got != pane {
		t.Errorf("CapturePane = %q, want %q", got, pane)
	}
}

func TestExecHasReportsPresence(t *testing.T) {
	stub{code: 0}.install(t)

	got, err := newStubExec(t).Has(context.Background(), execName)
	if err != nil {
		t.Fatalf("Has: %v", err)
	}
	if !got {
		t.Error("Has = false for a session tmux exited 0 on, want true")
	}
}

func TestExecHasReportsAbsence(t *testing.T) {
	stub{code: 1, stderr: "can't find session: " + execName}.install(t)

	got, err := newStubExec(t).Has(context.Background(), execName)
	if err != nil {
		t.Fatalf("Has: %v", err)
	}
	if got {
		t.Error("Has = true for a session tmux could not find, want false")
	}
}

// The one that matters. Every case below is tmux failing to answer, and none of
// them may come back as (false, nil): teardown reports success on a false from
// Has, so a swallowed failure here is how an orphaned unsandboxed shell gets
// recorded as destroyed.
func TestExecHasRefusesToGuessWhenTmuxFails(t *testing.T) {
	tests := []struct {
		name  string
		setup func(*testing.T)
	}{
		{"no server", func(t *testing.T) {
			stub{code: 1, stderr: "no server running on /tmp/tmux-1000/default"}.install(t)
		}},
		{"exit 1 with nothing to say", func(t *testing.T) {
			stub{code: 1}.install(t)
		}},
		{"usage error", func(t *testing.T) {
			stub{code: 2, stderr: "usage: has-session [-t target-session]"}.install(t)
		}},
		{"tmux not installed", noTmux},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tc.setup(t)

			got, err := newStubExec(t).Has(context.Background(), execName)
			if err == nil {
				t.Fatalf("Has = (%v, nil), want an error — tmux never answered", got)
			}
			if got {
				t.Error("Has = true alongside an error, want false")
			}
		})
	}
}

// A host with no tmux server is the normal first boot, not a failure — and it
// has two shapes. The second was found by running the integration tests on a
// socket that had never existed: a machine that has never started tmux has no
// socket directory either, so startup adoption would have failed fatally on
// exactly the fresh host it is meant to come up on.
func TestExecListTreatsNoServerAsEmpty(t *testing.T) {
	tests := []struct {
		name   string
		stderr string
	}{
		{"socket exists, nothing listening", "no server running on /tmp/tmux-1000/default"},
		{"socket never existed", "error connecting to /tmp/tmux-1000/default (No such file or directory)"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			stub{code: 1, stderr: tc.stderr}.install(t)

			got, err := newStubExec(t).List(context.Background())
			if err != nil {
				t.Fatalf("List: %v", err)
			}
			if len(got) != 0 {
				t.Errorf("List = %v, want empty", got)
			}
		})
	}
}

// The leniency above must not extend one inch further, or a broken tmux looks
// like a host with nothing running on it — and startup adoption would then
// walk past every session it was supposed to reclaim.
func TestExecListDoesNotSwallowOtherFailures(t *testing.T) {
	tests := []struct {
		name  string
		setup func(*testing.T)
	}{
		{"exit 1 with another message", func(t *testing.T) {
			stub{code: 1, stderr: "server exited unexpectedly"}.install(t)
		}},
		{"exit 1 with nothing to say", func(t *testing.T) {
			stub{code: 1}.install(t)
		}},
		{
			// The near-miss of the missing-socket case above: same "error
			// connecting to", but the socket is there and a server with live
			// sessions may be behind it. Reporting an empty host here would
			// walk startup adoption straight past every one of them.
			"socket unreachable", func(t *testing.T) {
				stub{code: 1, stderr: "error connecting to /tmp/tmux-1000/default (Permission denied)"}.install(t)
			},
		},
		{"usage error", func(t *testing.T) {
			stub{code: 2, stderr: "usage: list-sessions [-F format]"}.install(t)
		}},
		{"tmux not installed", noTmux},
		{"unreadable creation time", func(t *testing.T) {
			stub{stdout: "crswd-abc123|not-a-timestamp|1\n||"}.install(t)
		}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tc.setup(t)

			got, err := newStubExec(t).List(context.Background())
			if err == nil {
				t.Fatalf("List = (%v, nil), want an error", got)
			}
			if got != nil {
				t.Errorf("List returned %v alongside an error, want nil", got)
			}
		})
	}
}

func TestExecListParsesTmuxOutput(t *testing.T) {
	stub{stdout: "crswd-abc123|1785706480|1||\ncrswd-abc123-decoy|1785706480|||\nnotours|1785706480|||\n"}.install(t)

	got, err := newStubExec(t).List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}

	want := []SessionInfo{
		{Name: "crswd-abc123", Created: time.Unix(1785706480, 0), Managed: true},
		{Name: "crswd-abc123-decoy", Created: time.Unix(1785706480, 0)},
		{Name: "notours", Created: time.Unix(1785706480, 0)},
	}
	if !slices.Equal(got, want) {
		t.Errorf("List =\n  %v\nwant\n  %v", got, want)
	}
}

func TestParseSessions(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		stdout  string
		want    []SessionInfo
		wantErr bool
	}{
		{
			name:   "no sessions",
			stdout: "",
			want:   []SessionInfo{},
		},
		{
			name:   "managed, lookalike, and unrelated",
			stdout: "crswd-abc123|1785706480|1||\ncrswd-abc123-decoy|1785706480|||\nnotours|1785706480|||\n",
			want: []SessionInfo{
				{Name: "crswd-abc123", Created: time.Unix(1785706480, 0), Managed: true},
				{Name: "crswd-abc123-decoy", Created: time.Unix(1785706480, 0)},
				{Name: "notours", Created: time.Unix(1785706480, 0)},
			},
		},
		{
			// tmux permits "|" in a session name, and the operator's own
			// sessions are in this list. Splitting left-to-right would read
			// this row's name as "weird" and its creation time as "name",
			// failing the whole call — and with it, adoption of every managed
			// session on the host.
			name:   "a pipe in someone else's session name",
			stdout: "weird|name|1785706480|||\n",
			want: []SessionInfo{
				{Name: "weird|name", Created: time.Unix(1785706480, 0)},
			},
		},
		{
			name:   "a pipe in a managed session name",
			stdout: "a|b|c|1785706480|1||\n",
			want: []SessionInfo{
				{Name: "a|b|c", Created: time.Unix(1785706480, 0), Managed: true},
			},
		},
		{
			// Provenance is the marker being set at all, never the name.
			name:   "any non-empty marker means ours",
			stdout: "crswd-abc123|1785706480|yes||\n",
			want: []SessionInfo{
				{Name: "crswd-abc123", Created: time.Unix(1785706480, 0), Managed: true},
			},
		},
		{
			name:   "no trailing newline",
			stdout: "crswd-abc123|1785706480|1||\n",
			want: []SessionInfo{
				{Name: "crswd-abc123", Created: time.Unix(1785706480, 0), Managed: true},
			},
		},
		{
			name:    "creation time is not a number",
			stdout:  "crswd-abc123|whenever|1\n||",
			wantErr: true,
		},
		{
			name:    "creation time missing entirely",
			stdout:  "crswd-abc123||1\n||",
			wantErr: true,
		},
		{
			name:    "only one separator",
			stdout:  "crswd-abc123|1785706480\n",
			wantErr: true,
		},
		{
			name:    "no separators at all",
			stdout:  "crswd-abc123\n",
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := parseSessions(tc.stdout)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("parseSessions(%q) = (%v, nil), want an error", tc.stdout, got)
				}
				if got != nil {
					t.Errorf("parseSessions returned %v alongside an error, want nil", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseSessions(%q): %v", tc.stdout, err)
			}
			if !slices.Equal(got, tc.want) {
				t.Errorf("parseSessions(%q) =\n  %v\nwant\n  %v", tc.stdout, got, tc.want)
			}
		})
	}
}

// The socket is the only global flag, and every command carries it: a daemon on
// tmux's shared default server cannot tell another daemon's sessions from its
// own, adopts them, and reaps them on shutdown (#22).
func TestExecSocketSelection(t *testing.T) {
	t.Parallel()

	e := &Exec{socket: "crswd-test"}
	want := []string{"-L", "crswd-test", "has-session"}
	if got := e.args([]string{"has-session"}); !slices.Equal(got, want) {
		t.Errorf("isolated server args = %q, want %q", got, want)
	}
}

// The refusal that makes the isolation structural rather than remembered. An
// Exec without a server name has nowhere to fall back to, because the only
// fallback tmux offers is the shared default server.
func TestExecRefusesTheDefaultServer(t *testing.T) {
	t.Parallel()

	if _, err := NewExec(""); !errors.Is(err, ErrNoSocket) {
		t.Errorf("NewExec(\"\") error = %v, want ErrNoSocket", err)
	}

	got, err := NewExec("crswd-127-0-0-1-8765")
	if err != nil {
		t.Fatalf("NewExec: %v", err)
	}
	if got.socket != "crswd-127-0-0-1-8765" {
		t.Errorf("socket = %q, want the name it was given", got.socket)
	}
}

// A struct literal is one keystroke away from NewExec, so the zero value must
// refuse too — otherwise the constructor is the only thing standing between a
// future caller and the operator's own tmux server.
func TestExecZeroValueRunsNothing(t *testing.T) {
	// Not parallel: install mutates the environment. If the guard ever fails
	// open, the stub records the call and this test says so.
	recorded := stub{}.install(t)

	var e Exec
	if err := e.Kill(context.Background(), execName); !errors.Is(err, ErrNoSocket) {
		t.Fatalf("Kill on a zero Exec = %v, want ErrNoSocket", err)
	}
	//nolint:gosec // same path as the read above: install(t) set it from this test's own t.TempDir
	if _, err := os.Stat(os.Getenv(stubRecordEnv)); !os.IsNotExist(err) {
		t.Errorf("the zero Exec executed tmux: %v", recorded(t))
	}
}

// The listen address is the daemon's identity — two daemons cannot share one —
// so the server name derived from it must differ whenever the address does, and
// must not change between restarts of the same daemon.
func TestSocketForIsPerAddressAndStable(t *testing.T) {
	t.Parallel()

	cases := map[string]string{
		"":               "",
		"127.0.0.1:8765": "crswd-127-0-0-1-8765",
		"127.0.0.1:8766": "crswd-127-0-0-1-8766",
		"127.0.0.2:8765": "crswd-127-0-0-2-8765",
		"[::1]:8765":     "crswd----1--8765",
	}
	for listen, want := range cases {
		if got := SocketFor(listen); got != want {
			t.Errorf("SocketFor(%q) = %q, want %q", listen, got, want)
		}
		if got := SocketFor(listen); got != want {
			t.Errorf("SocketFor(%q) is not stable: %q then %q", listen, want, got)
		}
	}

	// The name becomes a filename under tmux's socket directory, so anything
	// that is not a letter or a digit has to be gone.
	for _, r := range SocketFor("127.0.0.1:8765") {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-':
		default:
			t.Errorf("SocketFor produced %q, which is not usable as a filename", r)
		}
	}
}
