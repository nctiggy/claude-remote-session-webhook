// Internal test, matching the rest of the package. Create's failure paths are
// what most of this file is about, and two of the properties they turn on — that
// a record is claimed before a shell exists, and that a half-started session is
// verified gone before its record is dropped — are only observable from inside.
package session

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/nctiggy/claude-remote-session-webhook/internal/auth"
	"github.com/nctiggy/claude-remote-session-webhook/internal/config"
	"github.com/nctiggy/claude-remote-session-webhook/internal/tmuxctl"
)

// hostileLabel is a name the alphabet admits (FR-027) chosen so that finding it
// anywhere in an argv is proof rather than coincidence: no path the fixture
// builds and no ID the daemon mints can contain it.
const hostileLabel = "zzz-not-a-target-zzz"

// insideLinkName is the last component of workDirFixture.insideLink(), which is
// a symlink to an approved directory. A create asked for the link must reach
// tmux as the resolved path, so this string appearing in an argv means the
// caller's spelling was used (FR-028).
const insideLinkName = "inside-link"

// errTmuxBroken stands in for tmux itself failing — a missing binary, a dead
// server, an exec error — as distinct from a session being absent.
var errTmuxBroken = errors.New("tmux is broken")

// stoppedClock stands still, so CreatedAt, LastActivity and every deadline
// derived from them are exact instants rather than approximately now.
type stoppedClock struct{ now time.Time }

func (c stoppedClock) Now() time.Time { return c.now }

// managerFixture puts a Manager on the real-filesystem work-dir fixture and the
// in-memory tmux fake. The filesystem half is not avoidable: ResolveWorkDir asks
// EvalSymlinks real questions, and Create is the first thing that puts its
// answer on a command line.
type managerFixture struct {
	workDirFixture

	tmux  *tmuxctl.Fake
	store *Store
	mgr   *Manager
	now   time.Time
}

func newManagerFixture(t *testing.T) managerFixture {
	t.Helper()

	wd := newWorkDirFixture(t)
	fake := tmuxctl.NewFake()
	store := NewStore()

	mgr, err := NewManagerWithClock(fake, store, wd.roots(), stoppedClock{now: contractCreatedAt})
	if err != nil {
		t.Fatalf("NewManagerWithClock() unexpected error: %v", err)
	}

	return managerFixture{workDirFixture: wd, tmux: fake, store: store, mgr: mgr, now: contractCreatedAt}
}

// repo is an ordinary working directory under the first approved root.
func (f managerFixture) repo() string { return filepath.Join(f.root, "repo") }

// request is the create every test starts from, varied per case on the copy.
func (f managerFixture) request() CreateRequest {
	return CreateRequest{Owner: auth.CallerOperator, Name: "refactor-auth", WorkDir: f.repo()}
}

func mustCreate(t *testing.T, f managerFixture, req CreateRequest) (*Session, string) {
	t.Helper()

	s, tok, err := f.mgr.Create(context.Background(), req)
	if err != nil {
		t.Fatalf("Create() unexpected error: %v", err)
	}
	if s == nil {
		t.Fatal("Create() returned no session alongside a nil error")
	}
	return s, tok
}

func TestCreateStartsASessionInTheValidatedDirectory(t *testing.T) {
	t.Parallel()

	f := newManagerFixture(t)
	req := f.request()
	s, tok := mustCreate(t, f, req)

	if !idShape.MatchString(s.ID) {
		t.Errorf("Create() id is %d characters and does not match %s", len(s.ID), idShape)
	}
	// Lengths, never the values: gitleaks reads a 64-character hex string as a
	// credential, and a failure message is a place a token would reach CI logs.
	if !tokenShape.MatchString(tok) {
		t.Errorf("Create() token is %d characters and does not match %s", len(tok), tokenShape)
	}
	if !s.TokenMatches(tok) {
		t.Error("Create() returned a token the record it belongs to does not accept")
	}

	if s.Owner != req.Owner {
		t.Errorf("Create() owner = %q, want %q", s.Owner, req.Owner)
	}
	if s.Name != req.Name {
		t.Errorf("Create() name = %q, want %q", s.Name, req.Name)
	}
	if s.WorkDir != f.repo() {
		t.Errorf("Create() work dir = %q, want the resolved %q", s.WorkDir, f.repo())
	}
	if s.State != StateStarting {
		t.Errorf("Create() state = %q, want %q — nothing has been confirmed yet", s.State, StateStarting)
	}
	if s.Adopted {
		t.Error("Create() marked a session it started as adopted")
	}

	// Both clocks come from the injected one, and the second is equal to the
	// first: a session that has never been used has been idle since it began.
	if !s.CreatedAt.Equal(f.now) {
		t.Errorf("Create() created at %s, want the injected %s", s.CreatedAt, f.now)
	}
	if !s.LastActivity.Equal(f.now) {
		t.Errorf("Create() last activity %s, want the creation instant %s", s.LastActivity, f.now)
	}
	if want := f.now.Add(AbsoluteLifetime); !s.TokenExpiry().Equal(want) {
		t.Errorf("Create() token expiry %s, want %s", s.TokenExpiry(), want)
	}

	stored, err := f.store.Get(s.ID, req.Owner)
	if err != nil {
		t.Fatalf("the created session is not in the store: %v", err)
	}
	if stored != *s {
		t.Error("the stored record differs from the one Create returned")
	}

	if dir, ok := f.tmux.WorkDir(s.TmuxName()); !ok || dir != f.repo() {
		t.Errorf("tmux session started in %q (present=%t), want %q", dir, ok, f.repo())
	}
	if v, ok := f.tmux.Option(s.TmuxName(), tmuxctl.OptionManaged); !ok || v != tmuxctl.OptionManagedValue {
		t.Errorf("%s = %q (present=%t), want %q", tmuxctl.OptionManaged, v, ok, tmuxctl.OptionManagedValue)
	}
	if v, ok := f.tmux.Option(s.TmuxName(), tmuxctl.OptionOwner); !ok || v != string(req.Owner) {
		t.Errorf("%s = %q (present=%t), want %q", tmuxctl.OptionOwner, v, ok, req.Owner)
	}

	// The round trip startup reconciliation depends on: provenance written by
	// Create must be readable back off the host as Managed (FR-021, FR-022).
	infos, err := f.tmux.List(context.Background())
	if err != nil {
		t.Fatalf("List() unexpected error: %v", err)
	}
	if len(infos) != 1 {
		t.Fatalf("List() returned %d sessions, want 1", len(infos))
	}
	if infos[0].Name != s.TmuxName() || !infos[0].Managed {
		t.Errorf("List() = {Name:%q Managed:%t}, want {%q true}", infos[0].Name, infos[0].Managed, s.TmuxName())
	}
}

// The order is the requirement, not an implementation detail: the session must
// exist before an option can be set on it, and it must be marked as ours before
// it runs anything — an unmarked session is one reconciliation will not adopt
// (FR-022) and so would never be torn down.
//
// The argv is spelled out here rather than built from tmuxctl's helpers on
// purpose. This asserts the command line tmux will receive; reusing the builders
// would assert only that Create called them.
func TestCreateSendsTheTmuxCommandsInOrder(t *testing.T) {
	t.Parallel()

	f := newManagerFixture(t)
	s, _ := mustCreate(t, f, f.request())

	name := "crswd-" + s.ID
	pane := "=" + name + ":"
	want := []tmuxctl.Call{
		{Op: tmuxctl.OpNew, Argv: []string{"tmux", "new-session", "-d", "-s", name, "-c", f.repo()}},
		{Op: tmuxctl.OpSetOption, Argv: []string{"tmux", "set-option", "-t", pane, "@crswd-managed", "1"}},
		{Op: tmuxctl.OpSetOption, Argv: []string{"tmux", "set-option", "-t", pane, "@crswd-owner", "operator"}},
		{Op: tmuxctl.OpSendKeys, Argv: []string{
			"tmux", "send-keys", "-t", pane, "--", "claude --dangerously-skip-permissions", "Enter",
		}},
	}

	got := f.tmux.Calls()
	if len(got) != len(want) {
		t.Fatalf("Create() ran %d tmux commands, want %d: %v", len(got), len(want), got)
	}
	for i := range want {
		if got[i].Op != want[i].Op {
			t.Errorf("command %d is %s, want %s", i, got[i].Op, want[i].Op)
			continue
		}
		if !slices.Equal(got[i].Argv, want[i].Argv) {
			t.Errorf("command %d argv:\n got %q\nwant %q", i, got[i].Argv, want[i].Argv)
		}
		if len(got[i].Stdin) != 0 {
			t.Errorf("command %d put %d bytes on stdin; create sends nothing that way", i, len(got[i].Stdin))
		}
	}
}

// FR-034: there is no path from a caller-supplied string to a tmux target. The
// name is a label and the work dir reaches tmux only as ResolveWorkDir left it,
// so the caller's spelling of either must appear in no command line at all.
func TestCreateDerivesEveryTargetFromTheIDAlone(t *testing.T) {
	t.Parallel()

	f := newManagerFixture(t)
	req := f.request()
	req.Name = hostileLabel
	req.WorkDir = f.insideLink()

	s, _ := mustCreate(t, f, req)

	if s.WorkDir != f.repo() {
		t.Fatalf("Create() work dir = %q, want the symlink resolved to %q", s.WorkDir, f.repo())
	}

	name := "crswd-" + s.ID
	for i, c := range f.tmux.Calls() {
		for j, arg := range c.Argv {
			switch {
			case strings.Contains(arg, hostileLabel):
				t.Errorf("command %d (%s) argv[%d] carries the caller's name", i, c.Op, j)
			case strings.Contains(arg, insideLinkName):
				t.Errorf("command %d (%s) argv[%d] carries the caller's spelling of the work dir", i, c.Op, j)
			case arg == "-t" || arg == "-s":
				if j+1 == len(c.Argv) {
					t.Fatalf("command %d (%s) ends with %q and names no target", i, c.Op, arg)
				}
				switch target := c.Argv[j+1]; target {
				case name, "=" + name, "=" + name + ":":
				default:
					t.Errorf("command %d (%s) addresses %q, which is not derived from the id", i, c.Op, target)
				}
			}
		}
	}
}

// Nothing is executed for a request that fails validation. That is the property
// worth asserting rather than the error alone: a tmux command that ran before
// the check would mean the check was not the gate.
func TestCreateRefusesWhatValidationRefuses(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		vary func(managerFixture, *CreateRequest)
		want error
	}{
		{"no owner", func(_ managerFixture, r *CreateRequest) { r.Owner = "" }, ErrMissingOwner},
		{"an empty name", func(_ managerFixture, r *CreateRequest) { r.Name = "" }, ErrInvalidName},
		{"a name over the ceiling", func(_ managerFixture, r *CreateRequest) {
			r.Name = strings.Repeat("a", MaxNameLen+1)
		}, ErrInvalidName},
		{"a name that is a tmux window target", func(_ managerFixture, r *CreateRequest) {
			r.Name = "repo:1"
		}, ErrNameIsTmuxTarget},
		{"a name that is a tmux pane target", func(_ managerFixture, r *CreateRequest) {
			r.Name = "repo.1"
		}, ErrNameIsTmuxTarget},
		{"a relative work dir", func(_ managerFixture, r *CreateRequest) {
			r.WorkDir = filepath.Join("code", "repo")
		}, ErrWorkDirNotAbsolute},
		{"a work dir outside every root", func(f managerFixture, r *CreateRequest) {
			r.WorkDir = filepath.Join(f.outside, "repo")
		}, ErrWorkDirOutsideRoots},
		{"the string-prefix trap", func(f managerFixture, r *CreateRequest) {
			r.WorkDir = filepath.Join(f.evil, "repo")
		}, ErrWorkDirOutsideRoots},
		{"a symlink out of an approved root", func(f managerFixture, r *CreateRequest) {
			r.WorkDir = f.escapeLink()
		}, ErrWorkDirOutsideRoots},
		{"a file rather than a directory", func(f managerFixture, r *CreateRequest) {
			r.WorkDir = f.file()
		}, ErrWorkDirNotDirectory},
		{"a work dir that does not exist", func(f managerFixture, r *CreateRequest) {
			r.WorkDir = filepath.Join(f.root, "never-created")
		}, ErrWorkDirUnresolvable},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f := newManagerFixture(t)
			req := f.request()
			tc.vary(f, &req)

			s, tok, err := f.mgr.Create(context.Background(), req)
			if err == nil {
				t.Fatal("Create() accepted a request validation should have refused")
			}
			if !errors.Is(err, tc.want) {
				t.Errorf("Create() error = %v, want one wrapping %v", err, tc.want)
			}
			if s != nil || tok != "" {
				t.Errorf("Create() returned a session (%t) or a token (%d chars) alongside an error", s != nil, len(tok))
			}
			if n := f.store.Len(); n != 0 {
				t.Errorf("the store holds %d records after a refused create, want 0", n)
			}
			if calls := f.tmux.Calls(); len(calls) != 0 {
				t.Errorf("a refused create ran %d tmux commands, want 0: %v", len(calls), calls)
			}
		})
	}
}

// A create that fails after tmux has started a shell must not leave the shell
// running. The teardown is verified, so success here means Has confirmed the
// session is gone — and only then is the record dropped.
func TestCreateTearsDownWhatItStartedWhenTmuxFails(t *testing.T) {
	t.Parallel()

	for _, op := range []tmuxctl.Op{tmuxctl.OpNew, tmuxctl.OpSetOption, tmuxctl.OpSendKeys} {
		t.Run(string(op), func(t *testing.T) {
			t.Parallel()

			f := newManagerFixture(t)
			f.tmux.FailOp(op, errTmuxBroken)

			s, tok, err := f.mgr.Create(context.Background(), f.request())
			if err == nil {
				t.Fatalf("Create() succeeded with %s failing", op)
			}
			if !errors.Is(err, errTmuxBroken) {
				t.Errorf("Create() error = %v, want one wrapping %v", err, errTmuxBroken)
			}
			if errors.Is(err, ErrOrphanedSession) {
				t.Errorf("Create() reported an orphan for a teardown it could verify: %v", err)
			}
			if s != nil || tok != "" {
				t.Errorf("Create() returned a session (%t) or a token (%d chars) alongside an error", s != nil, len(tok))
			}
			if n := f.store.Len(); n != 0 {
				t.Errorf("the store holds %d records after a verified rollback, want 0", n)
			}

			calls := f.tmux.Calls()
			if len(calls) < 3 {
				t.Fatalf("Create() ran %d tmux commands, want the failure plus a kill and a check: %v", len(calls), calls)
			}
			if got, want := calls[len(calls)-2].Op, tmuxctl.OpKill; got != want {
				t.Errorf("second-to-last command is %s, want %s", got, want)
			}
			if got, want := calls[len(calls)-1].Op, tmuxctl.OpHas; got != want {
				t.Errorf("last command is %s, want %s — a kill is not evidence", got, want)
			}

			// new-session -d -s <name>: the name the rollback had to address,
			// read back off the command line rather than reconstructed.
			name := calls[0].Argv[4]
			if _, ok := f.tmux.WorkDir(name); ok {
				t.Error("the tmux session survived a create that reported failure")
			}
		})
	}
}

// The other half of the rollback rule, and the one Principle VI turns on: when
// the daemon cannot confirm the shell is gone, the record stays. A record is a
// session with an owner and two deadlines, which is the only thing that will
// ever collect it — adoption runs at startup, so a live session the running
// daemon has forgotten is forgotten for good.
func TestCreateKeepsTheRecordWhenTeardownCannotBeVerified(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		arrange func(*tmuxctl.Fake)
	}{
		{"tmux cannot be asked whether it is gone", func(fake *tmuxctl.Fake) {
			fake.FailOp(tmuxctl.OpSetOption, errTmuxBroken)
			fake.FailOp(tmuxctl.OpHas, errTmuxBroken)
		}},
		{"the session outlived the kill", func(fake *tmuxctl.Fake) {
			fake.FailOp(tmuxctl.OpSendKeys, errTmuxBroken)
			fake.FailOp(tmuxctl.OpKill, errTmuxBroken)
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f := newManagerFixture(t)
			tc.arrange(f.tmux)

			s, tok, err := f.mgr.Create(context.Background(), f.request())
			if err == nil {
				t.Fatal("Create() succeeded with an unverifiable teardown")
			}
			if !errors.Is(err, ErrOrphanedSession) {
				t.Errorf("Create() error = %v, want one wrapping %v", err, ErrOrphanedSession)
			}
			if !errors.Is(err, errTmuxBroken) {
				t.Errorf("Create() error = %v, want one wrapping the original failure %v", err, errTmuxBroken)
			}
			// No token was returned, so the retained session is drivable by
			// nobody and reapable by the daemon. That is the intended end state.
			if s != nil || tok != "" {
				t.Errorf("Create() returned a session (%t) or a token (%d chars) alongside an error", s != nil, len(tok))
			}

			held := f.store.List(auth.CallerOperator)
			if len(held) != 1 {
				t.Fatalf("the store holds %d records for the owner after an unverifiable teardown, want 1", len(held))
			}
			rec := held[0]
			if rec.Owner != auth.CallerOperator {
				t.Errorf("retained record owner = %q, want %q", rec.Owner, auth.CallerOperator)
			}
			if rec.State != StateStarting {
				t.Errorf("retained record state = %q, want %q — nothing was confirmed", rec.State, StateStarting)
			}
			if want := f.now.Add(IdleTimeout); !rec.IdleDeadline().Equal(want) {
				t.Errorf("retained record idle deadline %s, want %s", rec.IdleDeadline(), want)
			}
			if want := f.now.Add(AbsoluteLifetime); !rec.AbsoluteDeadline().Equal(want) {
				t.Errorf("retained record absolute deadline %s, want %s", rec.AbsoluteDeadline(), want)
			}
		})
	}
}

// One record accepts exactly one token. The name is deliberately the same on
// every create: it is a label, and nothing is scoped to it.
func TestCreateIssuesADistinctIdentityEachTime(t *testing.T) {
	t.Parallel()

	f := newManagerFixture(t)

	const runs = 8
	seenIDs := make(map[string]bool, runs)
	seenTokens := make(map[string]bool, runs)
	sessions := make([]*Session, 0, runs)
	tokens := make([]string, 0, runs)

	for i := 0; i < runs; i++ {
		s, tok := mustCreate(t, f, f.request())
		if seenIDs[s.ID] {
			t.Fatalf("Create() repeated an id on run %d", i)
		}
		if seenTokens[tok] {
			t.Fatalf("Create() repeated a token on run %d", i)
		}
		if !idShape.MatchString(s.ID) {
			t.Fatalf("Create() id on run %d is %d characters and does not match %s", i, len(s.ID), idShape)
		}
		seenIDs[s.ID], seenTokens[tok] = true, true
		sessions = append(sessions, s)
		tokens = append(tokens, tok)
	}

	for i, s := range sessions {
		for j, tok := range tokens {
			if got := s.TokenMatches(tok); got != (i == j) {
				t.Errorf("record %d accepts token %d: got %t, want %t", i, j, got, i == j)
			}
		}
	}
	if n := f.store.Len(); n != runs {
		t.Errorf("the store holds %d records after %d creates", n, runs)
	}
}

// FR-013: the plaintext exists in the create response and nowhere else. tmux is
// the "nowhere else" this task adds — a command line and a stdin payload are
// both visible to anything that can read the process table or another tmux
// client.
func TestCreateKeepsTheTokenOffTheHost(t *testing.T) {
	t.Parallel()

	f := newManagerFixture(t)
	s, tok := mustCreate(t, f, f.request())

	for i, c := range f.tmux.Calls() {
		for j, arg := range c.Argv {
			if strings.Contains(arg, tok) {
				t.Errorf("command %d (%s) argv[%d] carries the bearer token", i, c.Op, j)
			}
		}
		if bytes.Contains(c.Stdin, []byte(tok)) {
			t.Errorf("command %d (%s) put the bearer token on stdin", i, c.Op)
		}
	}

	if s.TokenHash != hashToken(tok) {
		t.Error("the record's hash is not the hash of the token Create returned")
	}
}

func TestNewManagerFailsClosed(t *testing.T) {
	t.Parallel()

	wd := newWorkDirFixture(t)

	cases := []struct {
		name  string
		tmux  tmuxctl.Controller
		store *Store
		roots []config.ApprovedRoot
		clock Clock
	}{
		{"no controller", nil, NewStore(), wd.roots(), stoppedClock{}},
		{"no store", tmuxctl.NewFake(), nil, wd.roots(), stoppedClock{}},
		{"no clock", tmuxctl.NewFake(), NewStore(), wd.roots(), nil},
		{"no approved roots", tmuxctl.NewFake(), NewStore(), nil, stoppedClock{}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			m, err := NewManagerWithClock(tc.tmux, tc.store, tc.roots, tc.clock)
			if err == nil {
				t.Fatal("NewManagerWithClock() accepted a configuration it cannot enforce a session's bounds with")
			}
			if m != nil {
				t.Error("NewManagerWithClock() returned a Manager alongside an error")
			}
		})
	}
}

// The delegation, asserted the way auth_test asserts its own: every other test
// here injects a stopped clock, so nothing else would notice if NewManager
// reached for something other than the host clock.
func TestNewManagerUsesTheHostClock(t *testing.T) {
	t.Parallel()

	wd := newWorkDirFixture(t)

	m, err := NewManager(tmuxctl.NewFake(), NewStore(), wd.roots())
	if err != nil {
		t.Fatalf("NewManager() unexpected error: %v", err)
	}
	if _, ok := m.clock.(systemClock); !ok {
		t.Errorf("NewManager() clock is %T, want systemClock", m.clock)
	}
}
