package tmuxctl_test

import (
	"bytes"
	"context"
	"errors"
	"math"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nctiggy/claude-remote-session-webhook/internal/tmuxctl"
)

// The fake is only useful if it is substitutable for the real thing.
var _ tmuxctl.Controller = tmuxctl.NewFake()

const (
	fakeName    = "crswd-9f2c4a1b8e6d3f7a0c5b2e9d4f1a7c3b"
	fakeWorkDir = "/home/u/code/repo"
)

// Every recorded argv, in order, for one pass over the whole interface. This is
// the contract in specs/001-crswd-daemon-core/contracts/tmuxctl.md written as an
// assertion: the exact-match targets, the payload on stdin rather than on the
// command line, capture without -e, and no shell anywhere.
func TestFakeRecordsExactArgv(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	f := tmuxctl.NewFake()

	if err := f.New(ctx, fakeName, fakeWorkDir); err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := f.SetOption(ctx, fakeName, "@crswd-managed", "1"); err != nil {
		t.Fatalf("SetOption: %v", err)
	}
	if err := f.SendKeys(ctx, fakeName, "Enter"); err != nil {
		t.Fatalf("SendKeys: %v", err)
	}
	if err := f.Paste(ctx, fakeName, []byte("hello")); err != nil {
		t.Fatalf("Paste: %v", err)
	}
	if _, err := f.CapturePane(ctx, fakeName); err != nil {
		t.Fatalf("CapturePane: %v", err)
	}
	if err := f.Resize(ctx, fakeName, 44, 24); err != nil {
		t.Fatalf("Resize: %v", err)
	}
	if err := f.Kill(ctx, fakeName); err != nil {
		t.Fatalf("Kill: %v", err)
	}
	if _, err := f.Has(ctx, fakeName); err != nil {
		t.Fatalf("Has: %v", err)
	}
	if _, err := f.List(ctx); err != nil {
		t.Fatalf("List: %v", err)
	}

	want := []tmuxctl.Call{
		{Op: tmuxctl.OpNew, Argv: []string{"tmux", "new-session", "-d", "-s", fakeName, "-c", fakeWorkDir}},
		{Op: tmuxctl.OpSetOption, Argv: []string{"tmux", "set-option", "-t", "=" + fakeName + ":", "@crswd-managed", "1"}},
		{Op: tmuxctl.OpSendKeys, Argv: []string{"tmux", "send-keys", "-t", "=" + fakeName + ":", "--", "Enter"}},
		{Op: tmuxctl.OpPaste, Argv: []string{"tmux", "load-buffer", "-b", fakeName, "-"}, Stdin: []byte("hello")},
		{Op: tmuxctl.OpPaste, Argv: []string{"tmux", "paste-buffer", "-d", "-b", fakeName, "-t", "=" + fakeName + ":"}},
		{Op: tmuxctl.OpCapturePane, Argv: []string{"tmux", "capture-pane", "-p", "-t", "=" + fakeName + ":"}},
		{Op: tmuxctl.OpResize, Argv: []string{"tmux", "resize-window", "-t", "=" + fakeName + ":", "-x", "44", "-y", "24"}},
		{Op: tmuxctl.OpKill, Argv: []string{"tmux", "kill-session", "-t", "=" + fakeName}},
		{Op: tmuxctl.OpHas, Argv: []string{"tmux", "has-session", "-t", "=" + fakeName}},
		{Op: tmuxctl.OpList, Argv: []string{"tmux", "list-sessions", "-F", "#{session_name}|#{session_created}|#{@crswd-managed}|#{@crswd-name}|#{@crswd-workdir}|#{@crswd-start}|#{@crswd-lifetime}|#{@crswd-width}|#{@crswd-conversation}|#{?#{@crswd-binary},#{==:#{pane_current_command},#{@crswd-binary}},?}"}},
	}

	got := f.Calls()
	if len(got) != len(want) {
		t.Fatalf("recorded %d calls, want %d: %v", len(got), len(want), got)
	}
	for i := range want {
		if got[i].Op != want[i].Op {
			t.Errorf("call %d op = %q, want %q", i, got[i].Op, want[i].Op)
		}
		if !slices.Equal(got[i].Argv, want[i].Argv) {
			t.Errorf("call %d argv =\n  %q\nwant\n  %q", i, got[i].Argv, want[i].Argv)
		}
		if !bytes.Equal(got[i].Stdin, want[i].Stdin) {
			t.Errorf("call %d stdin = %q, want %q", i, got[i].Stdin, want[i].Stdin)
		}
	}
}

// FR-029: no shell string is ever constructed. Every command must be argv
// against the tmux binary itself, never an interpreter handed a command line.
func TestFakeArgvNeverInvokesAShell(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	f := tmuxctl.NewFake()
	if err := f.New(ctx, fakeName, fakeWorkDir); err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := f.Paste(ctx, fakeName, []byte("a; echo PWNED")); err != nil {
		t.Fatalf("Paste: %v", err)
	}

	shells := []string{"sh", "bash", "zsh", "/bin/sh", "/bin/bash"}
	for i, c := range f.Calls() {
		if len(c.Argv) == 0 {
			t.Fatalf("call %d recorded no argv", i)
		}
		if c.Argv[0] != "tmux" {
			t.Errorf("call %d argv[0] = %q, want %q", i, c.Argv[0], "tmux")
		}
		for _, shell := range shells {
			if slices.Contains(c.Argv, shell) {
				t.Errorf("call %d invokes a shell: %q", i, c.Argv)
			}
		}
	}
}

// capture-pane must never grow -e: tmux would reconstruct ANSI escapes from
// cell attributes and hand raw control bytes to the API, and eventually to a
// browser.
func TestFakeCapturePaneNeverAsksForEscapes(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	f := tmuxctl.NewFake()
	f.SetPane(fakeName, "some output")

	if _, err := f.CapturePane(ctx, fakeName); err != nil {
		t.Fatalf("CapturePane: %v", err)
	}
	calls := f.Calls()
	if len(calls) != 1 {
		t.Fatalf("recorded %d calls, want 1", len(calls))
	}
	if slices.Contains(calls[0].Argv, "-e") {
		t.Errorf("capture-pane asked for escapes: %q", calls[0].Argv)
	}
}

// The clamp is the point of Resize taking two integers at all. They are the only
// caller-influenced values that reach an argv in this package, and #120 requires
// a width the browser reported to be advisory — so a bad one is corrected here
// rather than refused, and what tmux is told is never a number it would reject.
//
// The bounds are tmux's own, measured against 3.4: 0 and negatives are "width
// too small", anything past 10000 is "width too large". A clamp that let either
// through would turn a viewport report into a failed reflow the operator reads
// on a phone.
func TestFakeResizeClampsWhatReachesTheArgv(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		cols, rows int
		wantX      string
		wantY      string
	}{
		"an ordinary phone width":  {cols: 44, rows: 24, wantX: "44", wantY: "24"},
		"the floor itself":         {cols: 1, rows: 1, wantX: "1", wantY: "1"},
		"the ceiling itself":       {cols: 10000, rows: 10000, wantX: "10000", wantY: "10000"},
		"zero is not a terminal":   {cols: 0, rows: 0, wantX: "1", wantY: "1"},
		"negative, past the floor": {cols: -1, rows: -80, wantX: "1", wantY: "1"},
		"one past the ceiling":     {cols: 10001, rows: 10001, wantX: "10000", wantY: "10000"},
		"nine million":             {cols: 9000000, rows: 9000000, wantX: "10000", wantY: "10000"},
		"the extremes of int":      {cols: math.MinInt, rows: math.MaxInt, wantX: "1", wantY: "10000"},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			ctx := context.Background()
			f := tmuxctl.NewFake()
			if err := f.New(ctx, fakeName, fakeWorkDir); err != nil {
				t.Fatalf("New: %v", err)
			}

			// Never an error, however absurd the numbers: advisory means clamped,
			// not rejected.
			if err := f.Resize(ctx, fakeName, tc.cols, tc.rows); err != nil {
				t.Fatalf("Resize(%d, %d): %v", tc.cols, tc.rows, err)
			}

			want := []string{"tmux", "resize-window", "-t", "=" + fakeName + ":", "-x", tc.wantX, "-y", tc.wantY}
			calls := f.Calls()
			got := calls[len(calls)-1]
			if got.Op != tmuxctl.OpResize {
				t.Fatalf("last call op = %q, want %q", got.Op, tmuxctl.OpResize)
			}
			if !slices.Equal(got.Argv, want) {
				t.Errorf("argv =\n  %q\nwant\n  %q", got.Argv, want)
			}

			// And the session is left at what tmux was told, not at what the
			// caller asked for — a fake that stored the unclamped value would let
			// a test claim a size the host never had.
			cols, rows, ok := f.Size(fakeName)
			if !ok {
				t.Fatal("Size reports no session after Resize")
			}
			if strconv.Itoa(cols) != tc.wantX || strconv.Itoa(rows) != tc.wantY {
				t.Errorf("size = %dx%d, want %sx%s", cols, rows, tc.wantX, tc.wantY)
			}
		})
	}
}

// A session nothing has resized is 80x24 — tmux's own default for a window no
// client ever attached to, which is every session this daemon starts. Answering
// 0x0 would be a size no terminal has, and a caller comparing a viewport against
// it would offer a reflow to every session on the page.
func TestFakeSizeIsTmuxsDefaultUntilSomethingResizes(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	f := tmuxctl.NewFake()
	if err := f.New(ctx, fakeName, fakeWorkDir); err != nil {
		t.Fatalf("New: %v", err)
	}

	cols, rows, ok := f.Size(fakeName)
	if !ok {
		t.Fatal("Size reports no session after New")
	}
	if cols != 80 || rows != 24 {
		t.Errorf("size = %dx%d, want 80x24", cols, rows)
	}

	if _, _, ok := f.Size("crswd-00000000000000000000000000000000"); ok {
		t.Error("Size reported a session that was never created")
	}
}

// The reason Paste exists. tmux's parser strips a trailing unescaped ';' from
// the final argument before -l applies, and -- does not prevent it, so caller
// text must never become part of a tmux command line at all. The fake records
// stdin separately precisely so a test can prove that.
func TestFakePastePayloadNeverEntersArgv(t *testing.T) {
	t.Parallel()

	payloads := []struct {
		name    string
		payload string
	}{
		{"bare semicolon", ";"},
		{"trailing semicolon", "foo;"},
		{"doubled trailing semicolon", "foo;;"},
		{"shell metacharacters", "a; echo PWNED; $(id)"},
		{"embedded newline", "line one\nline two"},
		{"empty", ""},
	}

	for _, tc := range payloads {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			ctx := context.Background()
			f := tmuxctl.NewFake()
			f.Seed(tmuxctl.SessionInfo{Name: fakeName, Managed: true})

			if err := f.Paste(ctx, fakeName, []byte(tc.payload)); err != nil {
				t.Fatalf("Paste: %v", err)
			}

			calls := f.Calls()
			if len(calls) != 2 {
				t.Fatalf("Paste recorded %d calls, want 2 (load-buffer then paste-buffer)", len(calls))
			}
			if got := string(calls[0].Stdin); got != tc.payload {
				t.Errorf("payload delivered as %q, want %q", got, tc.payload)
			}
			if calls[1].Stdin != nil {
				t.Errorf("paste-buffer carried stdin %q, want none", calls[1].Stdin)
			}
			if tc.payload == "" {
				return // an empty payload is a substring of everything
			}
			for i, c := range calls {
				for _, arg := range c.Argv {
					if strings.Contains(arg, tc.payload) {
						t.Errorf("call %d put caller text on the command line: %q", i, c.Argv)
					}
				}
			}
		})
	}
}

// The 409 path. tmux reports a successful kill and the session is still there;
// reporting teardown on Kill's exit code alone would leave a live unsandboxed
// shell with no owner.
func TestFakeKillCanLeaveTheSessionPresent(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	f := tmuxctl.NewFake()
	f.Seed(tmuxctl.SessionInfo{Name: fakeName, Managed: true})
	f.SurviveKill(fakeName)

	if err := f.Kill(ctx, fakeName); err != nil {
		t.Fatalf("Kill reported failure, want the survivor case to report success: %v", err)
	}
	present, err := f.Has(ctx, fakeName)
	if err != nil {
		t.Fatalf("Has: %v", err)
	}
	if !present {
		t.Fatal("session was removed; SurviveKill must keep it present so verified teardown has something to catch")
	}
}

// A session whose shell exited disappears with no Kill from us at all. Every
// operation against it must fail rather than silently succeed against nothing.
func TestFakeSessionCanVanishOnItsOwn(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	f := tmuxctl.NewFake()
	if err := f.New(ctx, fakeName, fakeWorkDir); err != nil {
		t.Fatalf("New: %v", err)
	}

	f.Vanish(fakeName)

	present, err := f.Has(ctx, fakeName)
	if err != nil {
		t.Fatalf("Has: %v", err)
	}
	if present {
		t.Fatal("vanished session still reported present")
	}
	if err := f.SendKeys(ctx, fakeName, "Enter"); err == nil {
		t.Error("SendKeys to a vanished session succeeded")
	}
	if _, err := f.CapturePane(ctx, fakeName); err == nil {
		t.Error("CapturePane on a vanished session succeeded")
	}
	for _, c := range f.Calls() {
		if c.Op == tmuxctl.OpKill {
			t.Error("Vanish recorded a Kill; it must model a session that died on its own")
		}
	}
}

// Adoption discrimination. The decoy shares the managed session's name as a
// prefix and is the reason targets carry a leading "=", but what decides
// adoption is provenance: an empty @crswd-managed means we did not create it.
func TestFakeListDiscriminatesManagedFromLookalike(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	base := time.Date(2026, 8, 3, 12, 0, 0, 0, time.UTC)
	f := tmuxctl.NewFake()

	f.Seed(tmuxctl.SessionInfo{Name: fakeName, Created: base, Managed: true})
	f.Seed(tmuxctl.SessionInfo{Name: fakeName + "-decoy", Created: base, Managed: false})
	f.Seed(tmuxctl.SessionInfo{Name: "notours", Created: base, Managed: false})
	f.Seed(tmuxctl.SessionInfo{Name: "crswd-expired", Created: base.Add(-25 * time.Hour), Managed: true})

	got, err := f.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}

	want := []tmuxctl.SessionInfo{
		{Name: fakeName, Created: base, Managed: true},
		{Name: fakeName + "-decoy", Created: base, Managed: false},
		{Name: "crswd-expired", Created: base.Add(-25 * time.Hour), Managed: true},
		{Name: "notours", Created: base, Managed: false},
	}
	if len(got) != len(want) {
		t.Fatalf("List returned %d sessions, want %d: %v", len(got), len(want), got)
	}
	for i := range want {
		if got[i].Name != want[i].Name || got[i].Managed != want[i].Managed || !got[i].Created.Equal(want[i].Created) {
			t.Errorf("session %d = %+v, want %+v", i, got[i], want[i])
		}
	}

	// The 25-hour-old survivor is destroyed rather than adopted, and that
	// decision is made from tmux's own creation time, not from when we noticed.
	i := slices.IndexFunc(got, func(s tmuxctl.SessionInfo) bool { return s.Name == "crswd-expired" })
	if i < 0 {
		t.Fatal("the expired survivor is missing from List")
	}
	if age := base.Sub(got[i].Created); age <= 24*time.Hour {
		t.Errorf("expired survivor is %v old, want more than 24h", age)
	}
}

// An empty server is the normal first-boot case, not a failure.
func TestFakeListOnAnEmptyServer(t *testing.T) {
	t.Parallel()

	got, err := tmuxctl.NewFake().List(context.Background())
	if err != nil {
		t.Fatalf("List on an empty server: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("List returned %d sessions, want 0", len(got))
	}
}

// Collapsing "tmux itself failed" into "the session is gone" would report a
// teardown that never happened. The two must be distinguishable at the call
// site, and a failed Kill must not remove anything.
func TestFakeExecFailureIsDistinctFromAbsence(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	errExec := errors.New("exec: \"tmux\": executable file not found in $PATH")

	t.Run("absence is not an error", func(t *testing.T) {
		t.Parallel()

		present, err := tmuxctl.NewFake().Has(ctx, fakeName)
		if err != nil {
			t.Fatalf("Has on an absent session returned an error: %v", err)
		}
		if present {
			t.Error("Has reported an absent session as present")
		}
	})

	t.Run("exec failure is an error, not gone", func(t *testing.T) {
		t.Parallel()

		f := tmuxctl.NewFake()
		f.Seed(tmuxctl.SessionInfo{Name: fakeName, Managed: true})
		f.FailOp(tmuxctl.OpHas, errExec)

		present, err := f.Has(ctx, fakeName)
		if !errors.Is(err, errExec) {
			t.Fatalf("Has error = %v, want %v", err, errExec)
		}
		if present {
			t.Error("Has reported present alongside an exec failure; the answer is unknown")
		}
	})

	t.Run("an absent session does not report an exec failure", func(t *testing.T) {
		t.Parallel()

		f := tmuxctl.NewFake()
		err := f.SendKeys(ctx, fakeName, "Enter")
		if err == nil {
			t.Fatal("SendKeys to an absent session succeeded")
		}
		if errors.Is(err, errExec) {
			t.Error("absence was reported as an exec failure")
		}
	})

	t.Run("a failed kill removes nothing", func(t *testing.T) {
		t.Parallel()

		f := tmuxctl.NewFake()
		f.Seed(tmuxctl.SessionInfo{Name: fakeName, Managed: true})
		f.FailOp(tmuxctl.OpKill, errExec)

		if err := f.Kill(ctx, fakeName); !errors.Is(err, errExec) {
			t.Fatalf("Kill error = %v, want %v", err, errExec)
		}
		f.FailOp(tmuxctl.OpKill, nil)
		present, err := f.Has(ctx, fakeName)
		if err != nil {
			t.Fatalf("Has: %v", err)
		}
		if !present {
			t.Error("a kill that failed to exec still removed the session")
		}
	})

	t.Run("the attempted call is still recorded", func(t *testing.T) {
		t.Parallel()

		f := tmuxctl.NewFake()
		f.FailOp(tmuxctl.OpList, errExec)
		if _, err := f.List(ctx); !errors.Is(err, errExec) {
			t.Fatalf("List error = %v, want %v", err, errExec)
		}
		if len(f.Calls()) != 1 {
			t.Errorf("recorded %d calls, want the failed attempt to be recorded", len(f.Calls()))
		}
	})
}

// Created must come from a clock the test controls, so ageing a session never
// means waiting for one.
func TestFakeNewStampsCreatedFromTheInjectedClock(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	created := time.Date(2026, 8, 2, 11, 0, 0, 0, time.UTC)
	f := tmuxctl.NewFake()
	f.SetNow(func() time.Time { return created })

	if err := f.New(ctx, fakeName, fakeWorkDir); err != nil {
		t.Fatalf("New: %v", err)
	}
	got, err := f.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("List returned %d sessions, want 1", len(got))
	}
	if !got[0].Created.Equal(created) {
		t.Errorf("Created = %v, want %v", got[0].Created, created)
	}
	if got[0].Managed {
		t.Error("a session is managed only once @crswd-managed is set, not by New alone")
	}
}

// The lifetime has to survive the round trip through the fake, or nothing
// downstream of it can be tested. This is the exact shape of the milestone 15
// defect, one layer down: the daemon wrote a lifetime nobody stored and read one
// nobody returned, and every adoption test passed while a never-expiring session
// came back mortal. A fake that agrees with the daemon about a field neither of
// them carries proves nothing at all.
func TestFakeListReportsTheLifetime(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	created := time.Date(2026, 8, 2, 11, 0, 0, 0, time.UTC)
	const seeded = "crswd-1111111111111111111111111111abcd"

	f := tmuxctl.NewFake()
	f.SetNow(func() time.Time { return created })
	if err := f.New(ctx, fakeName, fakeWorkDir); err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := f.SetOption(ctx, fakeName, tmuxctl.OptionLifetime, "72h0m0s"); err != nil {
		t.Fatalf("SetOption: %v", err)
	}
	// A session that survived a restart brings its own lifetime with it, the way
	// a real one does — adoption reads it off the host rather than assuming the
	// daemon's default, which is the whole of the fix.
	f.Seed(tmuxctl.SessionInfo{Name: seeded, Created: created, Lifetime: "never", Managed: true})

	got, err := f.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("List returned %d sessions, want 2: %v", len(got), got)
	}
	byName := map[string]tmuxctl.SessionInfo{got[0].Name: got[0], got[1].Name: got[1]}

	if l := byName[fakeName].Lifetime; l != "72h0m0s" {
		t.Errorf("a created session's Lifetime = %q, want %q", l, "72h0m0s")
	}
	if l := byName[seeded].Lifetime; l != "never" {
		t.Errorf("a seeded session's Lifetime = %q, want %q", l, "never")
	}
}

// A session whose lifetime was never set reads back empty, which is "unknown"
// and not "none". Every session created before the option existed is this case,
// and adoption must hand it the daemon's default rather than refuse it.
func TestFakeListReportsNoLifetimeWhenUnset(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	f := tmuxctl.NewFake()
	if err := f.New(ctx, fakeName, fakeWorkDir); err != nil {
		t.Fatalf("New: %v", err)
	}

	got, err := f.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("List returned %d sessions, want 1", len(got))
	}
	if got[0].Lifetime != "" {
		t.Errorf("Lifetime = %q, want empty for a session that never had one set", got[0].Lifetime)
	}
}

func TestFakeSessionState(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	f := tmuxctl.NewFake()

	if err := f.New(ctx, fakeName, fakeWorkDir); err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := f.New(ctx, fakeName, fakeWorkDir); err == nil {
		t.Error("creating a duplicate session succeeded")
	}
	if dir, ok := f.WorkDir(fakeName); !ok || dir != fakeWorkDir {
		t.Errorf("WorkDir = %q, %v, want %q, true", dir, ok, fakeWorkDir)
	}
	if err := f.SetOption(ctx, fakeName, "@crswd-owner", "operator"); err != nil {
		t.Fatalf("SetOption: %v", err)
	}
	if v, ok := f.Option(fakeName, "@crswd-owner"); !ok || v != "operator" {
		t.Errorf("Option(@crswd-owner) = %q, %v, want %q, true", v, ok, "operator")
	}
	if _, ok := f.Option(fakeName, "@crswd-managed"); ok {
		t.Error("@crswd-managed was set without anyone setting it")
	}

	f.SetPane(fakeName, "line one\nline two")
	pane, err := f.CapturePane(ctx, fakeName)
	if err != nil {
		t.Fatalf("CapturePane: %v", err)
	}
	if pane != "line one\nline two" {
		t.Errorf("CapturePane = %q, want the pane content that was set", pane)
	}
}

// Calls hands out copies: a test that mangles what it read must not be able to
// rewrite the record every other assertion depends on.
func TestFakeCallsReturnsCopies(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	f := tmuxctl.NewFake()
	f.Seed(tmuxctl.SessionInfo{Name: fakeName, Managed: true})
	if err := f.Paste(ctx, fakeName, []byte("hello")); err != nil {
		t.Fatalf("Paste: %v", err)
	}

	first := f.Calls()
	first[0].Argv[1] = "clobbered"
	first[0].Stdin[0] = 'X'

	second := f.Calls()
	if second[0].Argv[1] != "load-buffer" {
		t.Errorf("recorded argv was mutated through Calls: %q", second[0].Argv)
	}
	if string(second[0].Stdin) != "hello" {
		t.Errorf("recorded stdin was mutated through Calls: %q", second[0].Stdin)
	}
}

// Destroy races the reaper by design, and the session cap is decided under
// concurrent creates. The fake has to survive being used that way.
func TestFakeIsSafeForConcurrentUse(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	f := tmuxctl.NewFake()

	const workers = 16
	var wg sync.WaitGroup
	wg.Add(workers)
	for i := range workers {
		go func() {
			defer wg.Done()

			// Indexed rather than 'a'+i: the alphabet is visibly as long as
			// workers, so the name stays in [a-z] by construction rather than by
			// the reader checking the bound (gosec G115).
			name := fakeName + "-" + string("abcdefghijklmnop"[i])
			if err := f.New(ctx, name, fakeWorkDir); err != nil {
				t.Errorf("New(%s): %v", name, err)
				return
			}
			if _, err := f.Has(ctx, name); err != nil {
				t.Errorf("Has(%s): %v", name, err)
			}
			if _, err := f.List(ctx); err != nil {
				t.Errorf("List: %v", err)
			}
			if err := f.Kill(ctx, name); err != nil {
				t.Errorf("Kill(%s): %v", name, err)
			}
			f.Calls()
		}()
	}
	wg.Wait()

	if got, err := f.List(ctx); err != nil {
		t.Fatalf("List: %v", err)
	} else if len(got) != 0 {
		t.Errorf("%d sessions survived, want 0", len(got))
	}
}
