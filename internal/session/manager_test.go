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
	"sync"
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

// TestResolveIsEveryCheckAtOnce is the layer-3 table (FR-014, FR-032, FR-033).
// The four refusals answer with four sentinels so the trail can say which, and
// the caller behind them is answered identically by the handler — see
// internal/httpapi.
func TestResolveIsEveryCheckAtOnce(t *testing.T) {
	t.Parallel()

	// Named neutrally and spelled in words: a hex run of credential length is
	// what gitleaks refuses into the repository, and this is a value whose
	// rejection is the point.
	const neverIssued = "a-value-that-was-never-issued-for-any-session"
	const otherOwner auth.CallerID = "a-second-operator"

	f := newManagerFixture(t)
	s, tok := mustCreate(t, f, f.request())

	cases := map[string]struct {
		id        string
		owner     auth.CallerID
		presented string
		want      error
	}{
		"the owner with the credential issued": {s.ID, auth.CallerOperator, tok, nil},
		"an id that was never issued":          {"0123456789abcdef0123456789abcdef", auth.CallerOperator, tok, ErrSessionNotFound},
		"another owner holding the credential": {s.ID, otherOwner, tok, ErrSessionNotFound},
		// Unreachable behind authentication, and it must not become a skeleton
		// key if it ever is reached.
		"no owner at all":           {s.ID, "", tok, ErrSessionNotFound},
		"a credential never issued": {s.ID, auth.CallerOperator, neverIssued, ErrTokenMismatch},
		"no credential at all":      {s.ID, auth.CallerOperator, "", ErrTokenMismatch},
	}

	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got, err := f.mgr.Resolve(c.id, c.owner, c.presented)
			if !errors.Is(err, c.want) {
				t.Fatalf("Resolve() = _, %v; want %v", err, c.want)
			}
			if c.want != nil {
				if got.ID != "" {
					t.Errorf("Resolve() returned session %q alongside a refusal", got.ID)
				}
				return
			}
			if got.ID != s.ID || got.Owner != s.Owner {
				t.Errorf("Resolve() = %+v; want the record for %s owned by %s", got, s.ID, s.Owner)
			}
		})
	}
}

// TestResolveRefusesACredentialAtItsSessionsDeadline is FR-015's boundary, and
// it is stated against a second Manager over the same store so that what moved
// is the daemon's clock and not the record. A credential is good for the
// session's whole life and not one instant longer.
func TestResolveRefusesACredentialAtItsSessionsDeadline(t *testing.T) {
	t.Parallel()

	f := newManagerFixture(t)
	s, tok := mustCreate(t, f, f.request())

	cases := map[string]struct {
		at   time.Time
		want error
	}{
		"a second inside the lifetime": {f.now.Add(AbsoluteLifetime - time.Second), nil},
		"exactly at the deadline":      {f.now.Add(AbsoluteLifetime), ErrTokenExpired},
		"an hour past it":              {f.now.Add(AbsoluteLifetime + time.Hour), ErrTokenExpired},
	}

	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			mgr, err := NewManagerWithClock(f.tmux, f.store, f.roots(), stoppedClock{now: c.at})
			if err != nil {
				t.Fatalf("NewManagerWithClock() unexpected error: %v", err)
			}
			if _, err := mgr.Resolve(s.ID, auth.CallerOperator, tok); !errors.Is(err, c.want) {
				t.Fatalf("Resolve() at %v = _, %v; want %v", c.at, err, c.want)
			}
		})
	}
}

// TestResolveRefusesADeadSession keeps data-model.md's terminal state terminal.
// A record whose session is confirmed gone answers exactly as an unknown ID does
// (FR-033) — otherwise a destroyed session's endpoints would keep answering for
// a window that no longer exists.
func TestResolveRefusesADeadSession(t *testing.T) {
	t.Parallel()

	f := newManagerFixture(t)
	s, tok := mustCreate(t, f, f.request())

	if _, err := f.mgr.Resolve(s.ID, auth.CallerOperator, tok); err != nil {
		t.Fatalf("Resolve() before the session died = _, %v; want the record", err)
	}
	if err := f.store.SetState(s.ID, StateDead); err != nil {
		t.Fatalf("SetState(dead) unexpected error: %v", err)
	}
	if _, err := f.mgr.Resolve(s.ID, auth.CallerOperator, tok); !errors.Is(err, ErrSessionDead) {
		t.Fatalf("Resolve() on a dead session = _, %v; want %v", err, ErrSessionDead)
	}
}

// TestResolveNamesNoCallerSuppliedTextInItsError is FR-042 at the one place this
// package handles bytes the caller chose: the id. An error built with %w around
// it would put a hostile string — newlines included — into the audit trail the
// moment a handler recorded it.
func TestResolveNamesNoCallerSuppliedTextInItsError(t *testing.T) {
	t.Parallel()

	f := newManagerFixture(t)
	_, err := f.mgr.Resolve(hostileLabel, auth.CallerOperator, "")
	if err == nil {
		t.Fatal("Resolve() accepted an id nothing was ever issued for")
	}
	if strings.Contains(err.Error(), hostileLabel) {
		t.Errorf("Resolve() error %q carries the caller's own text", err)
	}
}

// List is owner-scoped for the same reason Resolve is, and this is the test that
// says so at the seam rather than at the handler: a caller sees their own
// sessions and has no way, through this method, to learn that another owner's
// exists at all (FR-032). The empty identity is the case worth pinning — a
// lookup that answered it with everything would turn one missing context value
// into the whole fleet.
func TestListIsScopedToItsOwner(t *testing.T) {
	t.Parallel()

	const otherOwner auth.CallerID = "a-second-operator"

	f := newManagerFixture(t)
	mine, _ := mustCreate(t, f, f.request())

	theirs := f.request()
	theirs.Owner = otherOwner
	theirsCreated, _ := mustCreate(t, f, theirs)

	got := f.mgr.List(auth.CallerOperator)
	if len(got) != 1 || got[0].ID != mine.ID {
		t.Fatalf("List(operator) = %v; want exactly the operator's own session %s", got, mine.ID)
	}
	if other := f.mgr.List(otherOwner); len(other) != 1 || other[0].ID != theirsCreated.ID {
		t.Errorf("List(%s) = %v; want exactly that owner's own session %s", otherOwner, other, theirsCreated.ID)
	}
	if none := f.mgr.List(""); len(none) != 0 {
		t.Errorf("List(\"\") = %v; want nothing — an unauthenticated lookup must not be a fleet view", none)
	}
}

// Prompt's two commands, in the only order that delivers anything: the bytes
// reach tmux on stdin, and only then is Return pressed. The payload is one
// research D4 verified send-keys would mangle, and it must appear on no command
// line at all — which is the whole of FR-029 for this path.
func TestPromptPastesThenSubmits(t *testing.T) {
	t.Parallel()

	const payload = "run the tests; then summarise;"

	f := newManagerFixture(t)
	s, _ := mustCreate(t, f, f.request())
	before := len(f.tmux.Calls())

	if err := f.mgr.Prompt(context.Background(), *s, payload); err != nil {
		t.Fatalf("Prompt() unexpected error: %v", err)
	}

	name := "crswd-" + s.ID
	pane := "=" + name + ":"
	want := []tmuxctl.Call{
		{Op: tmuxctl.OpPaste, Argv: []string{"tmux", "load-buffer", "-b", name, "-"}, Stdin: []byte(payload)},
		{Op: tmuxctl.OpPaste, Argv: []string{"tmux", "paste-buffer", "-d", "-b", name, "-t", pane}},
		{Op: tmuxctl.OpSendKeys, Argv: []string{"tmux", "send-keys", "-t", pane, "--", "Enter"}},
	}

	got := f.tmux.Calls()[before:]
	if len(got) != len(want) {
		t.Fatalf("Prompt() ran %d tmux commands, want %d: %v", len(got), len(want), got)
	}
	for i := range want {
		if got[i].Op != want[i].Op || !slices.Equal(got[i].Argv, want[i].Argv) {
			t.Errorf("command %d is %s %q, want %s %q", i, got[i].Op, got[i].Argv, want[i].Op, want[i].Argv)
		}
		if !bytes.Equal(got[i].Stdin, want[i].Stdin) {
			t.Errorf("command %d stdin = %q, want %q", i, got[i].Stdin, want[i].Stdin)
		}
		if slices.ContainsFunc(got[i].Argv, func(arg string) bool { return strings.Contains(arg, payload) }) {
			t.Errorf("command %d put the prompt on the command line: %q", i, got[i].Argv)
		}
	}
}

// The three records Prompt refuses before it executes anything. Only the empty
// text is reachable through the API — the resolver refuses a dead session and no
// handler can produce a record without an ID — so these are the fail-closed
// guards, checked here because nothing above this package can reach them.
func TestPromptRefusesWhatItCannotDeliver(t *testing.T) {
	t.Parallel()

	f := newManagerFixture(t)
	live, _ := mustCreate(t, f, f.request())

	dead := *live
	dead.State = StateDead

	cases := map[string]struct {
		session Session
		text    string
		want    error
	}{
		"an empty prompt": {*live, "", ErrEmptyPrompt},
		"a dead session":  {dead, "hello", ErrSessionDead},
		"a record with no id": {
			Session{Owner: auth.CallerOperator, State: StateStarting}, "hello", ErrSessionNotFound,
		},
	}

	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			// Counted rather than compared: the fixture's create already ran four
			// commands, and what this asserts is that the refusal added none.
			before := len(f.tmux.Calls())
			err := f.mgr.Prompt(context.Background(), c.session, c.text)
			if !errors.Is(err, c.want) {
				t.Fatalf("Prompt() = %v, want %v", err, c.want)
			}
			if after := len(f.tmux.Calls()); after != before {
				t.Errorf("the refused prompt ran %v; a refusal must cost no tmux command",
					f.tmux.Calls()[before:after])
			}
		})
	}
}

// FR-042 and docs/security.md §3 for the one place this package handles text the
// caller wrote: an error carrying the prompt back is an error a handler could
// record, and prompt text is secret.
func TestPromptNamesNoPromptTextInItsError(t *testing.T) {
	t.Parallel()

	f := newManagerFixture(t)
	s, _ := mustCreate(t, f, f.request())
	f.tmux.FailOp(tmuxctl.OpPaste, errTmuxBroken)

	err := f.mgr.Prompt(context.Background(), *s, hostileLabel)
	if err == nil {
		t.Fatal("Prompt() reported success while tmux was failing")
	}
	if strings.Contains(err.Error(), hostileLabel) {
		t.Errorf("Prompt() error %q carries the caller's own prompt text", err)
	}
}

// Output's one command and what it does to the answer. The pane holds a colour
// sequence, a title-setting OSC, and a raw control byte — none of which may
// survive into a value the API will hand a client (FR-031) — and the instant
// comes from the manager's own clock, so it is the one the reaper measures
// against rather than a second reading of time.
func TestOutputCapturesAndStrips(t *testing.T) {
	t.Parallel()

	const raw = "\x1b[31m$ go test ./...\x1b[0m\n\x1b]0;title\x07ok\tinternal/auth\x00\n"
	const want = "$ go test ./...\nok\tinternal/auth\n"

	f := newManagerFixture(t)
	s, _ := mustCreate(t, f, f.request())
	f.tmux.SetPane("crswd-"+s.ID, raw)
	before := len(f.tmux.Calls())

	got, err := f.mgr.Output(context.Background(), *s)
	if err != nil {
		t.Fatalf("Output() unexpected error: %v", err)
	}
	if got.Text != want {
		t.Errorf("Output() text = %q, want %q", got.Text, want)
	}
	if strings.ContainsRune(got.Text, 0x1B) {
		t.Errorf("Output() text carries an escape byte: %q", got.Text)
	}
	if !got.At.Equal(f.now) {
		t.Errorf("Output() captured at %v, want the manager's clock at %v", got.At, f.now)
	}

	calls := f.tmux.Calls()[before:]
	wantArgv := []string{"tmux", "capture-pane", "-p", "-t", "=crswd-" + s.ID + ":"}
	if len(calls) != 1 {
		t.Fatalf("Output() ran %d tmux commands, want exactly 1: %v", len(calls), calls)
	}
	if calls[0].Op != tmuxctl.OpCapturePane || !slices.Equal(calls[0].Argv, wantArgv) {
		t.Errorf("Output() ran %s %q, want %s %q",
			calls[0].Op, calls[0].Argv, tmuxctl.OpCapturePane, wantArgv)
	}
}

// The two records Output refuses before it executes anything, which are the two
// Prompt refuses and for the same reasons: neither is reachable behind the
// resolver, and both would otherwise name a window the caller did not earn.
func TestOutputRefusesWhatItCannotRead(t *testing.T) {
	t.Parallel()

	f := newManagerFixture(t)
	live, _ := mustCreate(t, f, f.request())

	dead := *live
	dead.State = StateDead

	cases := map[string]struct {
		session Session
		want    error
	}{
		"a dead session":      {dead, ErrSessionDead},
		"a record with no id": {Session{Owner: auth.CallerOperator, State: StateStarting}, ErrSessionNotFound},
	}

	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			before := len(f.tmux.Calls())
			got, err := f.mgr.Output(context.Background(), c.session)
			if !errors.Is(err, c.want) {
				t.Fatalf("Output() = _, %v, want %v", err, c.want)
			}
			if got.Text != "" || !got.At.IsZero() {
				t.Errorf("Output() returned %+v alongside an error; want the zero Capture", got)
			}
			if after := len(f.tmux.Calls()); after != before {
				t.Errorf("the refused capture ran %v; a refusal must cost no tmux command",
					f.tmux.Calls()[before:after])
			}
		})
	}
}

// docs/security.md §3 for the one thing this method handles that is secret: a
// failed capture must not carry back what it managed to read. An error is the
// one value on this path a handler is free to record, so pane content in it
// would be pane content in the trail.
func TestOutputNamesNoPaneContentInItsError(t *testing.T) {
	t.Parallel()

	const paneMarker = "zzz-secret-pane-content-zzz"

	f := newManagerFixture(t)
	s, _ := mustCreate(t, f, f.request())
	f.tmux.SetPane("crswd-"+s.ID, "AWS_SECRET_ACCESS_KEY="+paneMarker)
	f.tmux.FailOp(tmuxctl.OpCapturePane, errTmuxBroken)

	got, err := f.mgr.Output(context.Background(), *s)
	if err == nil {
		t.Fatal("Output() reported success while tmux was failing")
	}
	if strings.Contains(err.Error(), paneMarker) {
		t.Errorf("Output() error %q carries pane content", err)
	}
	if strings.Contains(got.Text, paneMarker) {
		t.Errorf("Output() returned pane content alongside an error: %q", got.Text)
	}
}

// Destroy's two commands, in the only order that proves anything: the kill, and
// then the question about whether it worked (FR-019). What follows the answer is
// FR-020 — the record and the hash it carries are gone, and the credential that
// was good a moment ago now resolves exactly as an id nobody was ever issued.
func TestDestroyKillsThenVerifiesAndClearsTheRecord(t *testing.T) {
	t.Parallel()

	f := newManagerFixture(t)
	s, tok := mustCreate(t, f, f.request())
	before := len(f.tmux.Calls())

	if err := f.mgr.Destroy(context.Background(), *s); err != nil {
		t.Fatalf("Destroy() unexpected error: %v", err)
	}

	name := "crswd-" + s.ID
	want := []tmuxctl.Call{
		{Op: tmuxctl.OpKill, Argv: []string{"tmux", "kill-session", "-t", "=" + name}},
		{Op: tmuxctl.OpHas, Argv: []string{"tmux", "has-session", "-t", "=" + name}},
	}

	got := f.tmux.Calls()[before:]
	if len(got) != len(want) {
		t.Fatalf("Destroy() ran %d tmux commands, want %d: %v", len(got), len(want), got)
	}
	for i := range want {
		if got[i].Op != want[i].Op || !slices.Equal(got[i].Argv, want[i].Argv) {
			t.Errorf("command %d is %s %q, want %s %q", i, got[i].Op, got[i].Argv, want[i].Op, want[i].Argv)
		}
	}

	if _, ok := f.tmux.WorkDir(name); ok {
		t.Error("the tmux session survived a destroy that reported success")
	}
	if n := f.store.Len(); n != 0 {
		t.Errorf("the store holds %d records after a verified teardown, want 0", n)
	}
	if _, err := f.mgr.Resolve(s.ID, auth.CallerOperator, tok); !errors.Is(err, ErrSessionNotFound) {
		t.Errorf("Resolve() with the destroyed session's credential = _, %v; want %v", err, ErrSessionNotFound)
	}
}

// The 409 path (FR-019, spec.md US3 scenario 4). tmux reports the kill worked
// and the session is still there, which is the one outcome a daemon that
// believed its own kill would record as success. The record stays: it is the
// only thing carrying an owner and two deadlines for a shell that is still
// running, and the reaper is what will eventually collect it.
func TestDestroyKeepsTheRecordWhenTheSessionSurvives(t *testing.T) {
	t.Parallel()

	f := newManagerFixture(t)
	s, tok := mustCreate(t, f, f.request())
	name := "crswd-" + s.ID
	f.tmux.SurviveKill(name)

	err := f.mgr.Destroy(context.Background(), *s)
	if err == nil {
		t.Fatal("Destroy() reported success for a session that is still running")
	}
	if !errors.Is(err, ErrOrphanedSession) {
		t.Errorf("Destroy() error = %v, want one wrapping %v", err, ErrOrphanedSession)
	}
	if _, ok := f.tmux.WorkDir(name); !ok {
		t.Fatal("the fixture did not leave the session present; the test proves nothing")
	}

	// Still the owner's, and still drivable. A session that outlived its
	// teardown is a session someone may want to try again on.
	if _, err := f.mgr.Resolve(s.ID, auth.CallerOperator, tok); err != nil {
		t.Errorf("Resolve() after a refused teardown = _, %v; want the record", err)
	}
}

// The case iteration 6 pinned in exec_tmux_test.go: killing the last session
// takes the tmux server with it, so has-session answers "no server running" —
// an error, deliberately, because Has may not read a dead server as an absent
// session. Without a second question, every correct teardown of the last session
// on the host would be reported as an orphan. List is that question.
func TestDestroyConfirmsAbsenceThroughListWhenHasCannotAnswer(t *testing.T) {
	t.Parallel()

	f := newManagerFixture(t)
	s, _ := mustCreate(t, f, f.request())
	before := len(f.tmux.Calls())
	f.tmux.FailOp(tmuxctl.OpHas, errTmuxBroken)

	if err := f.mgr.Destroy(context.Background(), *s); err != nil {
		t.Fatalf("Destroy() reported failure for a session tmux no longer lists: %v", err)
	}

	got := f.tmux.Calls()[before:]
	wantOps := []tmuxctl.Op{tmuxctl.OpKill, tmuxctl.OpHas, tmuxctl.OpList}
	if len(got) != len(wantOps) {
		t.Fatalf("Destroy() ran %d tmux commands, want %d: %v", len(got), len(wantOps), got)
	}
	for i, op := range wantOps {
		if got[i].Op != op {
			t.Errorf("command %d is %s, want %s", i, got[i].Op, op)
		}
	}
	if n := f.store.Len(); n != 0 {
		t.Errorf("the store holds %d records after a teardown confirmed through List, want 0", n)
	}
}

// Unknown is treated as surviving, in both the shapes that produce it: a host
// nothing can be asked about, and a host that answers and still has the session.
// Principle VI does not have a third answer — "we could not find out" and "it is
// still there" cost the same thing if they are wrong.
func TestDestroyKeepsTheRecordWhenNothingCanConfirm(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		arrange func(*tmuxctl.Fake, string)
	}{
		{"neither question can be asked", func(fake *tmuxctl.Fake, _ string) {
			fake.FailOp(tmuxctl.OpHas, errTmuxBroken)
			fake.FailOp(tmuxctl.OpList, errTmuxBroken)
		}},
		{"the fallback finds the session still listed", func(fake *tmuxctl.Fake, name string) {
			fake.SurviveKill(name)
			fake.FailOp(tmuxctl.OpHas, errTmuxBroken)
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f := newManagerFixture(t)
			s, tok := mustCreate(t, f, f.request())
			tc.arrange(f.tmux, "crswd-"+s.ID)

			err := f.mgr.Destroy(context.Background(), *s)
			if err == nil {
				t.Fatal("Destroy() reported success for a teardown it could not confirm")
			}
			if !errors.Is(err, ErrOrphanedSession) {
				t.Errorf("Destroy() error = %v, want one wrapping %v", err, ErrOrphanedSession)
			}
			if _, err := f.mgr.Resolve(s.ID, auth.CallerOperator, tok); err != nil {
				t.Errorf("Resolve() after an unconfirmed teardown = _, %v; want the record", err)
			}
		})
	}
}

// A session whose shell exited on its own is already gone when the kill lands,
// so tmux refuses the kill and the verification agrees the session is absent.
// That is a completed teardown, not a failure — this is the path the reaper and
// shutdown will take for anything that died while nobody was looking.
func TestDestroySucceedsForASessionThatVanishedOnItsOwn(t *testing.T) {
	t.Parallel()

	f := newManagerFixture(t)
	s, _ := mustCreate(t, f, f.request())
	f.tmux.Vanish("crswd-" + s.ID)

	if err := f.mgr.Destroy(context.Background(), *s); err != nil {
		t.Fatalf("Destroy() reported failure for a session that was already gone: %v", err)
	}
	if n := f.store.Len(); n != 0 {
		t.Errorf("the store holds %d records for a session that is gone, want 0", n)
	}
}

// FR-034 on the teardown path. The record carries a caller-supplied name, and
// the only string that may reach a target is built from the id.
func TestDestroyDerivesItsTargetFromTheIDAlone(t *testing.T) {
	t.Parallel()

	f := newManagerFixture(t)
	req := f.request()
	req.Name = hostileLabel
	s, _ := mustCreate(t, f, req)
	before := len(f.tmux.Calls())

	if err := f.mgr.Destroy(context.Background(), *s); err != nil {
		t.Fatalf("Destroy() unexpected error: %v", err)
	}
	for i, c := range f.tmux.Calls()[before:] {
		if slices.ContainsFunc(c.Argv, func(arg string) bool { return strings.Contains(arg, hostileLabel) }) {
			t.Errorf("command %d (%s) put the caller's own label on the command line: %q", i, c.Op, c.Argv)
		}
	}
}

// The one guard Destroy keeps, and the reason it keeps it: an empty id builds
// the bare prefix as a target, which addresses no session the daemon owns and
// possibly one it does not. Unreachable behind the resolver, refused anyway.
func TestDestroyRefusesARecordWithNoID(t *testing.T) {
	t.Parallel()

	f := newManagerFixture(t)
	before := len(f.tmux.Calls())

	err := f.mgr.Destroy(context.Background(), Session{Owner: auth.CallerOperator, State: StateStarting})
	if !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("Destroy() = %v, want %v", err, ErrSessionNotFound)
	}
	if after := len(f.tmux.Calls()); after != before {
		t.Errorf("the refused destroy ran %v; a refusal must cost no tmux command", f.tmux.Calls()[before:after])
	}
}

// spec.md's concurrency edge case, in the shape this package can state it:
// whoever loses the race finds the session gone and the record already dropped,
// and that is success. A destroy that answered "not found" for a teardown that
// completed would send a caller looking for a session that no longer exists.
func TestDestroyRacingItselfReportsSuccessToEveryCaller(t *testing.T) {
	t.Parallel()

	const racers = 8

	f := newManagerFixture(t)
	s, _ := mustCreate(t, f, f.request())

	var wg sync.WaitGroup
	errs := make([]error, racers)
	for i := range errs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs[i] = f.mgr.Destroy(context.Background(), *s)
		}()
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Errorf("racer %d = %v, want success: the session is gone and so is the record", i, err)
		}
	}
	if n := f.store.Len(); n != 0 {
		t.Errorf("the store holds %d records after %d concurrent destroys, want 0", n, racers)
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
