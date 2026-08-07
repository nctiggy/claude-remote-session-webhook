// Internal test, matching the rest of the package. Create's failure paths are
// what most of this file is about, and two of the properties they turn on — that
// a record is claimed before a shell exists, and that a half-started session is
// verified gone before its record is dropped — are only observable from inside.
package session

import (
	"bytes"
	"context"
	"encoding/base64"
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

// capNotUnderTest is the concurrent-session cap every fixture here carries: high
// enough that a test about something else cannot trip over it, since a create
// refused for want of room runs no tmux command and would fail such a test
// somewhere far from the reason. The cap's own tests build a manager with a cap
// they choose — see managerWithCap.
const capNotUnderTest = 64

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

	mgr, err := NewManagerWithClock(fake, store, wd.roots(), capNotUnderTest, stoppedClock{now: contractCreatedAt})
	if err != nil {
		t.Fatalf("NewManagerWithClock() unexpected error: %v", err)
	}

	return managerFixture{workDirFixture: wd, tmux: fake, store: store, mgr: mgr, now: contractCreatedAt}
}

// managerWithCap is a second Manager on the same host and the same store as the
// fixture's own, differing only in the cap it enforces. The store is shared on
// purpose: what the cap counts is records, whoever made them.
func (f managerFixture) managerWithCap(t *testing.T, limit int) *Manager {
	t.Helper()

	mgr, err := NewManagerWithClock(f.tmux, f.store, f.roots(), limit, stoppedClock{now: f.now})
	if err != nil {
		t.Fatalf("NewManagerWithClock() unexpected error: %v", err)
	}
	return mgr
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
		// The two facts adoption restores (#72). Base64 on the directory because
		// a path may contain the separator list-sessions puts between fields.
		{Op: tmuxctl.OpSetOption, Argv: []string{"tmux", "set-option", "-t", pane, "@crswd-name", f.request().Name}},
		{Op: tmuxctl.OpSetOption, Argv: []string{"tmux", "set-option", "-t", pane, "@crswd-workdir", base64.StdEncoding.EncodeToString([]byte(f.repo()))}},
		// The name mode is derived from, written even when it is the default's
		// empty string: an option set to nothing and one never set read back
		// identically, so the branch that skipped it would be untestable.
		{Op: tmuxctl.OpSetOption, Argv: []string{"tmux", "set-option", "-t", pane, "@crswd-start", ""}},
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
			// A caller-supplied name may now appear as an *option value* (#72),
			// which addresses nothing: it is its own argv element and the -t
			// check below is what FR-034 is actually about. What must never
			// happen is either string reaching a target.
			isOptionValue := c.Op == tmuxctl.OpSetOption && j == len(c.Argv)-1
			switch {
			case strings.Contains(arg, hostileLabel) && !isOptionValue:
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

// FR-036, and the half of it a caller sees: at the cap the create is refused,
// and refused before anything runs. A create that reached tmux and was then
// rolled back would satisfy the count and miss the point — the cap exists so the
// host is never asked to carry more than it was configured for.
func TestCreateRefusesPastTheConcurrentCap(t *testing.T) {
	t.Parallel()

	const limit = 3

	f := newManagerFixture(t)
	mgr := f.managerWithCap(t, limit)

	live := make([]*Session, 0, limit)
	for i := 0; i < limit; i++ {
		s, _, err := mgr.Create(context.Background(), f.request())
		if err != nil {
			t.Fatalf("Create() %d of %d unexpected error: %v", i+1, limit, err)
		}
		live = append(live, s)
	}

	before := len(f.tmux.Calls())
	refused, token, err := mgr.Create(context.Background(), f.request())
	if !errors.Is(err, ErrTooManySessions) {
		t.Fatalf("Create() past the cap = %v, want %v", err, ErrTooManySessions)
	}
	if refused != nil {
		t.Error("Create() returned a session alongside the refusal")
	}
	if token != "" {
		t.Error("Create() handed out a credential for a session it refused to start")
	}
	if extra := f.tmux.Calls()[before:]; len(extra) != 0 {
		t.Errorf("the refused create ran %v; a create past the cap must cost no tmux command", extra)
	}

	// The sessions already running are untouched: the cap refuses the newcomer,
	// it does not make room by degrading what is there.
	if n := f.store.Len(); n != limit {
		t.Errorf("the store holds %d records after a refused create, want %d", n, limit)
	}
	for i, s := range live {
		if _, err := f.store.Get(s.ID, s.Owner); err != nil {
			t.Errorf("session %d is no longer in the store after a refused create: %v", i, err)
		}
		present, err := f.tmux.Has(context.Background(), s.TmuxName())
		if err != nil {
			t.Fatalf("Has() unexpected error: %v", err)
		}
		if !present {
			t.Errorf("session %d is no longer on the host after a refused create", i)
		}
	}
}

// The boundary itself, which is the case a Len check in the manager would get
// wrong: every racer reads the count and inserts under one lock, so the cap is
// what the host ends up carrying rather than what it carries when nobody is in a
// hurry.
func TestConcurrentCreatesCannotOvershootTheCap(t *testing.T) {
	t.Parallel()

	const limit, racers = 4, 16

	f := newManagerFixture(t)
	mgr := f.managerWithCap(t, limit)

	var wg sync.WaitGroup
	errs := make([]error, racers)
	for i := range errs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _, errs[i] = mgr.Create(context.Background(), f.request())
		}()
	}
	wg.Wait()

	started := 0
	for i, err := range errs {
		switch {
		case err == nil:
			started++
		case errors.Is(err, ErrTooManySessions):
		default:
			t.Errorf("racer %d = %v, want either a session or the cap", i, err)
		}
	}
	if started != limit {
		t.Errorf("%d of %d racers started a session, want exactly the cap of %d", started, racers, limit)
	}
	if n := f.store.Len(); n != limit {
		t.Errorf("the store holds %d records, want the cap of %d", n, limit)
	}

	// The host is the claim that matters. A store that counted correctly while
	// tmux carried more sessions than the cap would be a cap in name only.
	infos, err := f.tmux.List(context.Background())
	if err != nil {
		t.Fatalf("List() unexpected error: %v", err)
	}
	if len(infos) != limit {
		t.Errorf("the host carries %d sessions, want the cap of %d", len(infos), limit)
	}
}

// The cap counts what is live, not what has ever been created: a destroyed
// session gives its place back. Stated as a test because the alternative — a
// high-water mark — is what a counter incremented on create and never decremented
// would silently become.
func TestTheCapCountsLiveSessionsAndNotCreates(t *testing.T) {
	t.Parallel()

	f := newManagerFixture(t)
	mgr := f.managerWithCap(t, 1)

	first, _, err := mgr.Create(context.Background(), f.request())
	if err != nil {
		t.Fatalf("Create() unexpected error: %v", err)
	}
	if _, _, err := mgr.Create(context.Background(), f.request()); !errors.Is(err, ErrTooManySessions) {
		t.Fatalf("Create() at the cap = %v, want %v", err, ErrTooManySessions)
	}

	if err := mgr.Destroy(context.Background(), *first); err != nil {
		t.Fatalf("Destroy() unexpected error: %v", err)
	}
	if _, _, err := mgr.Create(context.Background(), f.request()); err != nil {
		t.Fatalf("Create() after a teardown freed the cap = %v, want a session", err)
	}
}

// US4 against FR-036: a restart onto a host already carrying sessions adopts all
// of them — leaving one unowned would be worse than being over the cap — and
// then refuses to add to them until the reaper has brought the fleet back down.
func TestTheCapCountsSessionsAdoptedFromTheHost(t *testing.T) {
	t.Parallel()

	f := newManagerFixture(t)
	f.seedSurvivor(testID("a"), f.now.Add(-time.Hour))
	f.seedSurvivor(testID("b"), f.now.Add(-time.Hour))

	mgr := f.managerWithCap(t, 1)
	adopted, err := mgr.Adopt(context.Background())
	if err != nil {
		t.Fatalf("Adopt() unexpected error: %v", err)
	}
	if len(adopted) != 2 {
		t.Fatalf("Adopt() took %d sessions under management, want both: an unadopted survivor is an unowned shell", len(adopted))
	}

	if _, _, err := mgr.Create(context.Background(), f.request()); !errors.Is(err, ErrTooManySessions) {
		t.Errorf("Create() on a host already over the cap = %v, want %v", err, ErrTooManySessions)
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

			mgr, err := NewManagerWithClock(f.tmux, f.store, f.roots(), capNotUnderTest, stoppedClock{now: c.at})
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

// TestViewIsOwnerScopedWithNoCredentialToPresent is the whole of the watcher's
// authorisation at this seam: the record exists and it is the caller's, and
// there is no third argument because a browser holds no per-session token
// (FR-017). Every refusal is ErrSessionNotFound, so "no such session", "not
// yours", and "you skipped authentication" are one answer from one lookup —
// milestone 1's enumeration rule, unchanged by the second door.
func TestViewIsOwnerScopedWithNoCredentialToPresent(t *testing.T) {
	t.Parallel()

	const otherOwner auth.CallerID = "a-second-operator"

	f := newManagerFixture(t)
	s, _ := mustCreate(t, f, f.request())

	cases := map[string]struct {
		id    string
		owner auth.CallerID
		want  error
	}{
		"the owner of the session":    {s.ID, auth.CallerOperator, nil},
		"an id that was never issued": {"0123456789abcdef0123456789abcdef", auth.CallerOperator, ErrSessionNotFound},
		// FR-037b's synthetic second owner, at the seam the dashboard reads
		// through. The check runs even though production has one identity: a
		// check removed because it always passes is a check that will not be
		// there when a second one arrives.
		"another owner entirely": {s.ID, otherOwner, ErrSessionNotFound},
		// Unreachable behind the browser door, and refused rather than trusted:
		// an empty identity must not be a skeleton key if it ever gets here.
		"no owner at all": {s.ID, "", ErrSessionNotFound},
	}

	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got, err := f.mgr.View(c.id, c.owner)
			if !errors.Is(err, c.want) {
				t.Fatalf("View() = _, %v; want %v", err, c.want)
			}
			if c.want != nil {
				if got.ID != "" {
					t.Errorf("View() returned session %q alongside a refusal", got.ID)
				}
				return
			}
			if got.ID != s.ID || got.Owner != s.Owner {
				t.Errorf("View() = %+v; want the record for %s owned by %s", got, s.ID, s.Owner)
			}
		})
	}
}

// TestViewRefusesADeadSession keeps data-model.md's terminal state terminal on
// the browser's path too. A record whose session is confirmed gone answers
// exactly as an unknown id does (FR-033), so the dashboard cannot render a
// window that no longer exists.
func TestViewRefusesADeadSession(t *testing.T) {
	t.Parallel()

	f := newManagerFixture(t)
	s, _ := mustCreate(t, f, f.request())

	if _, err := f.mgr.View(s.ID, auth.CallerOperator); err != nil {
		t.Fatalf("View() before the session died = _, %v; want the record", err)
	}
	if err := f.store.SetState(s.ID, StateDead); err != nil {
		t.Fatalf("SetState(dead) unexpected error: %v", err)
	}
	if _, err := f.mgr.View(s.ID, auth.CallerOperator); !errors.Is(err, ErrSessionDead) {
		t.Fatalf("View() on a dead session = _, %v; want %v", err, ErrSessionDead)
	}
}

// TestViewLeavesTheIdleClockWhereResolveMovesIt is FR-034f stated as the
// difference between the two paths, because that is what makes it checkable: the
// same record, the same daemon clock, one read that drives and one that watches.
// Asserting only that View leaves the clock alone would also pass against a
// Touch that had stopped working for everyone.
func TestViewLeavesTheIdleClockWhereResolveMovesIt(t *testing.T) {
	t.Parallel()

	f := newManagerFixture(t)
	s, tok := mustCreate(t, f, f.request())

	// Later than creation on purpose. Store.Touch only ever moves the clock
	// forward, so a daemon whose clock stood still where the record was written
	// would make both paths look identical and prove nothing.
	later := f.now.Add(30 * time.Minute)
	mgr := f.managerAt(t, f.store, later)

	got, err := mgr.View(s.ID, auth.CallerOperator)
	if err != nil {
		t.Fatalf("View() unexpected error: %v", err)
	}
	if !got.LastActivity.Equal(f.now) {
		t.Errorf("View() returned LastActivity %v; want the record as written, %v", got.LastActivity, f.now)
	}
	if stored := mustStored(t, f, s.ID); !stored.LastActivity.Equal(f.now) {
		t.Errorf("View() moved the stored idle clock to %v; want it left at %v", stored.LastActivity, f.now)
	}

	if _, err := mgr.Resolve(s.ID, auth.CallerOperator, tok); err != nil {
		t.Fatalf("Resolve() unexpected error: %v", err)
	}
	if stored := mustStored(t, f, s.ID); !stored.LastActivity.Equal(later) {
		t.Errorf("Resolve() left the idle clock at %v; the driving path must still advance it to %v", stored.LastActivity, later)
	}
}

// TestASessionWatchedWithoutPauseIsStillReapedOnTime is US2 scenario 7 at the
// only seam that can enforce it, and it is the reason View exists at all: a
// browser tab that never stops watching must not keep an unsandboxed shell alive
// (Principle VI). The reaper's own expiredAt is the judge — a test that recomputed
// the deadline itself would agree with a View that had started touching the clock.
func TestASessionWatchedWithoutPauseIsStillReapedOnTime(t *testing.T) {
	t.Parallel()

	f := newManagerFixture(t)
	s, _ := mustCreate(t, f, f.request())

	// A tab open across the whole idle window, watching far more often than the
	// stream's one-second tick would.
	for at := f.now; at.Before(f.now.Add(IdleTimeout)); at = at.Add(time.Minute) {
		if _, err := f.managerAt(t, f.store, at).View(s.ID, auth.CallerOperator); err != nil {
			t.Fatalf("View() at %v unexpected error: %v", at, err)
		}
	}

	stored := mustStored(t, f, s.ID)
	deadline := f.now.Add(IdleTimeout)
	if !stored.IdleDeadline().Equal(deadline) {
		t.Fatalf("after an hour of watching the idle deadline is %v; want it unmoved at %v", stored.IdleDeadline(), deadline)
	}
	if got := expiredAt(stored, deadline); got != ExpiryIdle {
		t.Errorf("the reaper calls a continuously watched session %q at its idle deadline; want %q", got, ExpiryIdle)
	}
}

// TestViewNamesNoCallerSuppliedTextInItsError is FR-042 on the second path from
// a request to a record, for the reason it holds on the first: the id is bytes
// the caller chose, and an error built with %w around it puts a hostile string
// into the audit trail the moment a handler records it.
func TestViewNamesNoCallerSuppliedTextInItsError(t *testing.T) {
	t.Parallel()

	f := newManagerFixture(t)
	_, err := f.mgr.View(hostileLabel, auth.CallerOperator)
	if err == nil {
		t.Fatal("View() accepted an id nothing was ever issued for")
	}
	if strings.Contains(err.Error(), hostileLabel) {
		t.Errorf("View() error %q carries the caller's own text", err)
	}
}

// mustStored is the record as the store holds it, which is the only place the
// idle clock can be observed: every read hands back a copy, so asserting on a
// returned Session would say nothing about what was written.
func mustStored(t *testing.T, f managerFixture, id string) Session {
	t.Helper()

	s, err := f.store.Get(id, auth.CallerOperator)
	if err != nil {
		t.Fatalf("the store no longer holds session %s: %v", id, err)
	}
	return s
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

// hostSessions is every session name the host holds, sorted, so a test can say
// the host is exactly as it was rather than that some particular window survived.
func hostSessions(t *testing.T, f managerFixture) []string {
	t.Helper()

	infos, err := f.tmux.List(context.Background())
	if err != nil {
		t.Fatalf("the host could not be listed: %v", err)
	}
	names := make([]string, 0, len(infos))
	for _, info := range infos {
		names = append(names, info.Name)
	}
	slices.Sort(names)
	return names
}

// FR-015 and SC-012, pinned where the change happens. A rename is a record-only
// change: the tmux name derives from the identifier, so the window a session
// addresses before the rename is the window it addresses after.
//
// The way to hold that is not to compare two strings afterwards — an
// implementation that renamed the window would leave TmuxName agreeing with
// itself while the host no longer had that session. So the claim is made three
// ways: the rename runs no tmux command at all, the host's session names are
// unchanged, and the stored record differs in exactly one field.
//
// The new label is hostileLabel, which no path the fixture builds and no id the
// daemon mints can contain, so its absence from every argv is proof rather than
// coincidence.
func TestRenameLeavesTmuxNameAlone(t *testing.T) {
	t.Parallel()

	f := newManagerFixture(t)
	s, _ := mustCreate(t, f, f.request())

	before := mustStored(t, f, s.ID)
	hostBefore := hostSessions(t, f)
	calls := len(f.tmux.Calls())

	renamed, err := f.mgr.Rename(before, hostileLabel)
	if err != nil {
		t.Fatalf("Rename() unexpected error: %v", err)
	}

	// Read before the host is listed again, so that the listing itself is not
	// one of the calls being counted.
	if extra := f.tmux.Calls()[calls:]; len(extra) != 0 {
		t.Errorf("the rename ran %v; a record-only change costs no tmux command", extra)
	}
	if got := hostSessions(t, f); !slices.Equal(got, hostBefore) {
		t.Errorf("the host now holds %v, want %v unchanged", got, hostBefore)
	}

	after := mustStored(t, f, s.ID)
	if after.TmuxName() != before.TmuxName() {
		t.Errorf("the record now addresses %q, want %q: the tmux name derives from the id", after.TmuxName(), before.TmuxName())
	}

	// Every other field is compared as one value rather than named one at a
	// time, so a field added to Session later is held to this rule without the
	// test being revisited.
	want := before
	want.Name = hostileLabel
	if after != want {
		t.Errorf("the stored record is %+v, want %+v: a rename changes the name and nothing else", after, want)
	}
	if renamed != want {
		t.Errorf("Rename() returned %+v, want the record as stored %+v", renamed, want)
	}
}

// The same validation as create, because it is the same call. A name create
// refuses must not be reachable by renaming into it, and the existing name must
// survive the refusal — which is the contract's 400 (contracts/actions.md).
func TestRenameRefusesWhatCreateRefuses(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		to   string
		want error
	}{
		{"an empty name", "", ErrInvalidName},
		{"a name over the ceiling", strings.Repeat("a", MaxNameLen+1), ErrInvalidName},
		{"a name that is a tmux window target", "repo:1", ErrNameIsTmuxTarget},
		{"a name that is a tmux pane target", "repo.1", ErrNameIsTmuxTarget},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f := newManagerFixture(t)
			s, _ := mustCreate(t, f, f.request())
			before := mustStored(t, f, s.ID)
			calls := len(f.tmux.Calls())

			renamed, err := f.mgr.Rename(before, tc.to)
			if !errors.Is(err, tc.want) {
				t.Fatalf("Rename() = _, %v; want one wrapping %v", err, tc.want)
			}
			if renamed != (Session{}) {
				t.Errorf("Rename() returned %+v alongside an error, want the zero record", renamed)
			}
			// The rejected name is caller-supplied text, and an error travels to
			// the log the trail may not carry it in (FR-042). ValidateName says
			// what the rule is, never what was sent.
			if tc.to != "" && strings.Contains(err.Error(), tc.to) {
				t.Errorf("Rename() error %q repeats the name the caller sent", err)
			}
			if after := mustStored(t, f, s.ID); after != before {
				t.Errorf("the refused rename left %+v, want %+v unchanged", after, before)
			}
			if extra := f.tmux.Calls()[calls:]; len(extra) != 0 {
				t.Errorf("a refused rename ran %v, want no tmux command", extra)
			}
		})
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

// FR-016's delivery, asserted as the argv it becomes. The bytes reach tmux on
// stdin through the load-buffer path prompt text takes, and the newline is among
// them — so there is no send-keys on this path at all, which is what the count
// below is really saying: a delivery that pressed Return, or one that typed the
// command with send-keys -l, adds a third command and fails here.
//
// The payload is spelled out rather than read from compactCommand, so an edit to
// the constant cannot quietly move what this test is checking for.
func TestCompactUsesBufferPath(t *testing.T) {
	t.Parallel()

	f := newManagerFixture(t)
	s, _ := mustCreate(t, f, f.request())
	before := len(f.tmux.Calls())

	if err := f.mgr.Compact(context.Background(), *s); err != nil {
		t.Fatalf("Compact() unexpected error: %v", err)
	}

	name := "crswd-" + s.ID
	pane := "=" + name + ":"
	want := []tmuxctl.Call{
		{Op: tmuxctl.OpPaste, Argv: []string{"tmux", "load-buffer", "-b", name, "-"}, Stdin: []byte("/compact\n")},
		{Op: tmuxctl.OpPaste, Argv: []string{"tmux", "paste-buffer", "-d", "-b", name, "-t", pane}},
	}

	got := f.tmux.Calls()[before:]
	if len(got) != len(want) {
		t.Fatalf("Compact() ran %d tmux commands, want %d: %v", len(got), len(want), got)
	}
	for i := range want {
		if got[i].Op != want[i].Op || !slices.Equal(got[i].Argv, want[i].Argv) {
			t.Errorf("command %d is %s %q, want %s %q", i, got[i].Op, got[i].Argv, want[i].Op, want[i].Argv)
		}
		if !bytes.Equal(got[i].Stdin, want[i].Stdin) {
			t.Errorf("command %d stdin = %q, want %q", i, got[i].Stdin, want[i].Stdin)
		}
		if slices.ContainsFunc(got[i].Argv, func(arg string) bool { return strings.Contains(arg, "/compact") }) {
			t.Errorf("command %d put the delivered text on the command line: %q", i, got[i].Argv)
		}
	}
}

// data-model.md's one field change for this milestone. Compact is activity — it
// delivers into the session exactly as a prompt does — so it defers the idle
// deadline exactly as a prompt does.
//
// The API path gets that from Resolve. This is the other path: a browser holds no
// per-session credential, so it reaches its session through View, which is
// required not to touch the clock (FR-034f). If this method did not, nothing on
// that path would, and the reaper would go on measuring a session an operator is
// driving as idle.
func TestCompactDefersTheIdleDeadline(t *testing.T) {
	t.Parallel()

	f := newManagerFixture(t)
	s, _ := mustCreate(t, f, f.request())

	// Later than creation on purpose, for the reason the View/Resolve pair needs
	// it: Store.Touch only ever moves the clock forward, so a daemon whose clock
	// stood still where the record was written would prove nothing.
	later := f.now.Add(30 * time.Minute)
	mgr := f.managerAt(t, f.store, later)

	if err := mgr.Compact(context.Background(), *s); err != nil {
		t.Fatalf("Compact() unexpected error: %v", err)
	}

	stored := mustStored(t, f, s.ID)
	if !stored.LastActivity.Equal(later) {
		t.Errorf("the compact left the idle clock at %v, want it advanced to %v", stored.LastActivity, later)
	}
	if !stored.IdleDeadline().Equal(later.Add(IdleTimeout)) {
		t.Errorf("the idle deadline is %v, want the hour after the compact %v", stored.IdleDeadline(), later.Add(IdleTimeout))
	}
}

// The two records Compact refuses before it executes anything — Prompt's two,
// refused for Prompt's reasons. Neither is reachable through a handler: View
// answers a dead session as it answers an unknown id, and no route can produce a
// record with no id at all. They are checked here because nothing above this
// package can reach them, and a guard nothing exercises is a guard that will be
// missing when something does.
func TestCompactRefusesWhatItCannotDeliver(t *testing.T) {
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

			// Counted rather than compared: the fixture's create already ran four
			// commands, and what this asserts is that the refusal added none.
			before := len(f.tmux.Calls())
			stored := mustStored(t, f, live.ID)

			if err := f.mgr.Compact(context.Background(), c.session); !errors.Is(err, c.want) {
				t.Fatalf("Compact() = %v, want %v", err, c.want)
			}
			if after := len(f.tmux.Calls()); after != before {
				t.Errorf("the refused compact ran %v; a refusal must cost no tmux command",
					f.tmux.Calls()[before:after])
			}
			// The live record is the one a mistaken guard would have touched: both
			// refusals name a session the store either holds under another state or
			// does not hold at all.
			if after := mustStored(t, f, live.ID); after != stored {
				t.Errorf("the refused compact left %+v, want %+v unchanged", after, stored)
			}
		})
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

// Issue #21: a session that dies out of band — a host reboot, a tmux server
// restart, an operator's own kill-session — left a record nothing could collect
// until the idle bound or the 24-hour ceiling reached it, and until then the
// fleet drew it as a live card.
//
// The capture is where the daemon meets the host per request, so it is where the
// death is discovered. What follows is Destroy's own ending on Destroy's own
// evidence (FR-019, FR-020): the host is *asked*, and only an affirmative answer
// drops the record and the token hash with it, after which the id resolves
// exactly as one nobody was ever issued.
func TestOutputDropsTheRecordWhenTheHostConfirmsTheSessionIsGone(t *testing.T) {
	t.Parallel()

	f := newManagerFixture(t)
	s, tok := mustCreate(t, f, f.request())
	name := "crswd-" + s.ID
	// No kill: the session goes the way one whose shell exited goes, which is
	// the case the daemon had no path for.
	f.tmux.Vanish(name)
	before := len(f.tmux.Calls())

	got, err := f.mgr.Output(context.Background(), *s)
	if !errors.Is(err, ErrSessionDead) {
		t.Fatalf("Output() = _, %v, want %v", err, ErrSessionDead)
	}
	if got.Text != "" || !got.At.IsZero() {
		t.Errorf("Output() returned %+v alongside an error; want the zero Capture", got)
	}

	// The liveness check is a question, not an assumption, so it is on the wire.
	calls := f.tmux.Calls()[before:]
	wantOps := []tmuxctl.Op{tmuxctl.OpCapturePane, tmuxctl.OpHas}
	gotOps := make([]tmuxctl.Op, 0, len(calls))
	for _, c := range calls {
		gotOps = append(gotOps, c.Op)
	}
	if !slices.Equal(gotOps, wantOps) {
		t.Errorf("Output() ran %v, want %v — a record may only be dropped on an answer from the host", gotOps, wantOps)
	}

	if _, err := f.store.Get(s.ID, auth.CallerOperator); !errors.Is(err, ErrSessionNotFound) {
		t.Errorf("the record of a session the host says is gone survives the capture: Get() = _, %v, want %v", err, ErrSessionNotFound)
	}
	if _, err := f.mgr.Resolve(s.ID, auth.CallerOperator, tok); !errors.Is(err, ErrSessionNotFound) {
		t.Errorf("the credential still resolves after the record was dropped: Resolve() = _, %v, want %v", err, ErrSessionNotFound)
	}
}

// The other half of #21, and the half that must not regress: "I could not read
// the screen" is not "there is no screen". A record may only be dropped on an
// answer, so a session the host still has — or one it cannot be asked about —
// keeps everything it had, and the caller keeps the transient failure it always
// got.
func TestOutputKeepsTheRecordWhenTheSessionIsNotConfirmedGone(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		// arrange breaks the host in the way this case is about, having already
		// broken the capture itself.
		arrange func(f managerFixture, name string)
	}{
		"the pane could not be read but the session is there": {
			arrange: func(managerFixture, string) {},
		},
		"the session is gone and the liveness check could not be made": {
			arrange: func(f managerFixture, name string) {
				f.tmux.Vanish(name)
				// Both, because confirmGone falls back to List when Has cannot
				// answer: a host that can say nothing at all is the case where
				// "gone" would be a guess.
				f.tmux.FailOp(tmuxctl.OpHas, errTmuxBroken)
				f.tmux.FailOp(tmuxctl.OpList, errTmuxBroken)
			},
		},
	}

	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			f := newManagerFixture(t)
			s, _ := mustCreate(t, f, f.request())
			f.tmux.FailOp(tmuxctl.OpCapturePane, errTmuxBroken)
			c.arrange(f, "crswd-"+s.ID)

			_, err := f.mgr.Output(context.Background(), *s)
			if err == nil {
				t.Fatal("Output() reported success while the capture was failing")
			}
			if errors.Is(err, ErrSessionDead) {
				t.Errorf("Output() = _, %v; a session the host did not say was gone was declared dead", err)
			}

			if _, err := f.store.Get(s.ID, auth.CallerOperator); err != nil {
				t.Errorf("the record was dropped on an unreadable pane: Get() = _, %v, want the session", err)
			}
		})
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

// seedSurvivor puts on the host a session the daemon did not start this run —
// what a restart leaves behind. Seed records no tmux call, because the daemon
// made none, so every call an adoption test sees is one Adopt chose to make.
func (f managerFixture) seedSurvivor(id string, created time.Time) string {
	name := tmuxNamePrefix + id
	f.tmux.Seed(tmuxctl.SessionInfo{Name: name, Created: created, Managed: true})
	return name
}

// managerAt is the restarted daemon: the same host, a store that may or may not
// be the one before it, and a clock stopped wherever the test needs it.
func (f managerFixture) managerAt(t *testing.T, store *Store, now time.Time) *Manager {
	t.Helper()

	mgr, err := NewManagerWithClock(f.tmux, store, f.roots(), capNotUnderTest, stoppedClock{now: now})
	if err != nil {
		t.Fatalf("NewManagerWithClock() unexpected error: %v", err)
	}
	return mgr
}

func mustAdoptOne(t *testing.T, mgr *Manager) AdoptedSession {
	t.Helper()

	got, err := mgr.Adopt(context.Background())
	if err != nil {
		t.Fatalf("Adopt() unexpected error: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("Adopt() took %d sessions under management, want exactly 1", len(got))
	}
	return got[0]
}

// claimOne is the credential for an adopted session, taken the way the daemon
// takes it: Adopt mints none, and ClaimPending issues the one token that session
// will ever have, in the response that hands it over.
//
// Going through it rather than reading a field off AdoptedSession is what makes
// these assertions cover the delivery path. A token that exists but reaches
// nobody is the gap this replaced.
func claimOne(t *testing.T, mgr *Manager, id string) string {
	t.Helper()

	claimed, err := mgr.ClaimPending(auth.CallerOperator)
	if err != nil {
		t.Fatalf("ClaimPending() unexpected error: %v", err)
	}
	token, ok := claimed[id]
	if !ok {
		t.Fatalf("ClaimPending() issued no credential for adopted session %s; it returned %d", id, len(claimed))
	}
	return token
}

// US4 scenario 1, and the shape of every field an adopted record carries. The
// clock is the assertion that matters: CreatedAt is the host session's own start
// time and LastActivity is the moment of adoption, which is FR-024 in two lines
// — a restart may reset how long a session has been idle, and may not move the
// ceiling it dies at.
func TestAdoptTakesBackASurvivingSessionWithAFreshCredential(t *testing.T) {
	t.Parallel()

	f := newManagerFixture(t)
	id := testID("a")
	started := f.now.Add(-2 * time.Hour)
	name := f.seedSurvivor(id, started)
	before := len(f.tmux.Calls())

	got := mustAdoptOne(t, f.mgr)
	s := got.Session

	if s.ID != id {
		t.Errorf("Adopt() id = %q, want the id in the host session's name %q", s.ID, id)
	}
	if s.Owner != auth.CallerOperator {
		t.Errorf("Adopt() owner = %q, want the configured operator %q", s.Owner, auth.CallerOperator)
	}
	if !s.Adopted {
		t.Error("Adopt() left the record indistinguishable from one the API created")
	}
	if s.State != StateRunning {
		t.Errorf("Adopt() state = %q, want %q — the host was asked and answered", s.State, StateRunning)
	}
	// Neither survived the process that knew them, and neither was invented.
	if s.Name != "" || s.WorkDir != "" {
		t.Errorf("Adopt() invented name %q and work dir %q for a session it only knows the id of", s.Name, s.WorkDir)
	}

	if !s.CreatedAt.Equal(started) {
		t.Errorf("Adopt() created at %s, want the host's own start time %s", s.CreatedAt, started)
	}
	if !s.LastActivity.Equal(f.now) {
		t.Errorf("Adopt() last activity %s, want the adoption instant %s — only the idle clock resets", s.LastActivity, f.now)
	}
	if want := started.Add(AbsoluteLifetime); !s.AbsoluteDeadline().Equal(want) {
		t.Errorf("Adopt() absolute deadline %s, want %s", s.AbsoluteDeadline(), want)
	}
	if !s.TokenExpiry().Equal(s.AbsoluteDeadline()) {
		t.Error("Adopt() issued a credential whose life is not the session's own")
	}

	// Checked before the claim, because claiming is what rewrites TokenHash and
	// clears CredentialPending — comparing after would be comparing the record to
	// the state it was deliberately moved out of.
	if stored, err := f.store.Get(id, auth.CallerOperator); err != nil || stored != s {
		t.Errorf("the store holds %+v (err %v) for an adopted session, want the record Adopt returned", stored, err)
	}
	if !s.CredentialPending {
		t.Error("Adopt() left the session claimable by nobody: no credential is pending on it")
	}
	// Nothing can drive it yet. The hash Adopt stored is of a token it discarded,
	// so this is the interval where the session is owned, listed, capped and
	// reapable — and not drivable, which is what makes minting late safe.
	if _, err := f.mgr.Resolve(id, auth.CallerOperator, strings.Repeat("f", 64)); !errors.Is(err, ErrTokenMismatch) {
		t.Errorf("Resolve() on an unclaimed adopted session = _, %v; want %v", err, ErrTokenMismatch)
	}

	// Lengths and acceptance, never the value: a 64-character hex string in a
	// failure message is a credential in CI's logs.
	adoptedToken := claimOne(t, f.mgr, id)
	if !tokenShape.MatchString(adoptedToken) {
		t.Errorf("the adopted session's token is %d characters and does not match %s", len(adoptedToken), tokenShape)
	}
	if _, err := f.mgr.Resolve(id, auth.CallerOperator, adoptedToken); err != nil {
		t.Errorf("Resolve() with the claimed credential = _, %v; want the record", err)
	}

	// Once, and only once (FR-013). A second claim has nothing to hand over.
	if second, err := f.mgr.ClaimPending(auth.CallerOperator); err != nil || len(second) != 0 {
		t.Errorf("a second ClaimPending() returned %d credentials (err %v); want none — a token is issued once",
			len(second), err)
	}

	// One List for discovery (research D6) and one question per candidate. The
	// argv is spelled out rather than built from tmuxctl's helpers: this asserts
	// the command line tmux will receive, not that Adopt called a builder.
	want := []tmuxctl.Call{
		{Op: tmuxctl.OpList, Argv: []string{"tmux", "list-sessions", "-F", "#{session_name}|#{session_created}|#{@crswd-managed}|#{@crswd-name}|#{@crswd-workdir}|#{@crswd-start}"}},
		{Op: tmuxctl.OpHas, Argv: []string{"tmux", "has-session", "-t", "=" + name}},
	}
	calls := f.tmux.Calls()[before:]
	if len(calls) != len(want) {
		t.Fatalf("Adopt() ran %d tmux commands, want %d: %v", len(calls), len(want), calls)
	}
	for i := range want {
		if calls[i].Op != want[i].Op || !slices.Equal(calls[i].Argv, want[i].Argv) {
			t.Errorf("command %d is %s %q, want %s %q", i, calls[i].Op, calls[i].Argv, want[i].Op, want[i].Argv)
		}
	}
}

// US4 scenario 2, stated end to end: the daemon that issued the first credential
// is gone, and the credential is gone with it — it was never stored, so there is
// nothing for a restart to recover even in principle (FR-021). The session the
// second pass adopts is the one the first pass created, and the two hold
// different credentials for it.
func TestAdoptIssuesACredentialTheProcessBeforeItCannotHave(t *testing.T) {
	t.Parallel()

	f := newManagerFixture(t)
	// Pin the host's clock too, so the session's start time is an exact instant
	// rather than whenever the test ran.
	f.tmux.SetNow(func() time.Time { return f.now })
	created, before := mustCreate(t, f, f.request())

	restarted := f.managerAt(t, NewStore(), f.now.Add(time.Hour))
	got := mustAdoptOne(t, restarted)

	if got.Session.ID != created.ID {
		t.Fatalf("Adopt() took %q under management, want the session the previous run created, %q", got.Session.ID, created.ID)
	}
	restartedToken := claimOne(t, restarted, created.ID)
	if restartedToken == before {
		t.Fatal("the restart handed back the credential the previous run issued")
	}
	if _, err := restarted.Resolve(created.ID, auth.CallerOperator, before); !errors.Is(err, ErrTokenMismatch) {
		t.Errorf("Resolve() with the credential from before the restart = _, %v; want %v", err, ErrTokenMismatch)
	}
	if _, err := restarted.Resolve(created.ID, auth.CallerOperator, restartedToken); err != nil {
		t.Errorf("Resolve() with the credential the restart issued = _, %v; want the record", err)
	}
	// The ceiling is the one the first run set, an hour before this pass ran.
	if want := f.now.Add(AbsoluteLifetime); !got.Session.AbsoluteDeadline().Equal(want) {
		t.Errorf("Adopt() absolute deadline %s, want the original %s", got.Session.AbsoluteDeadline(), want)
	}
}

// US4 scenario 3 and the whole of FR-022's first half. Provenance is the marker,
// the prefix, and the shape of what follows it — a session failing any of them
// was not created here, so it is neither adopted nor touched. "Not touched" is
// half the requirement and the more dangerous half to get wrong: the operator's
// own tmux sessions are on this host.
func TestAdoptLeavesEverythingItDidNotCreateAlone(t *testing.T) {
	t.Parallel()

	cases := map[string]tmuxctl.SessionInfo{
		"the prefix without the marker":    {Name: tmuxNamePrefix + testID("a"), Managed: false},
		"the marker without the prefix":    {Name: "notes", Managed: true},
		"a marked name that is not an id":  {Name: tmuxNamePrefix + "not-an-id", Managed: true},
		"a marked id in the wrong case":    {Name: tmuxNamePrefix + strings.ToUpper(testID("b")), Managed: true},
		"a marked id one character short":  {Name: tmuxNamePrefix + testID("c")[:IDLen-1], Managed: true},
		"the bare prefix and nothing else": {Name: tmuxNamePrefix, Managed: true},
	}

	for name, info := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			f := newManagerFixture(t)
			info.Created = f.now.Add(-time.Hour)
			f.tmux.Seed(info)
			before := len(f.tmux.Calls())

			got, err := f.mgr.Adopt(context.Background())
			if err != nil {
				t.Fatalf("Adopt() unexpected error: %v", err)
			}
			if len(got) != 0 {
				t.Errorf("Adopt() took %d sessions under management, want none", len(got))
			}
			if n := f.store.Len(); n != 0 {
				t.Errorf("the store holds %d records after adopting nothing, want 0", n)
			}
			if _, ok := f.tmux.WorkDir(info.Name); !ok {
				t.Error("a session the daemon did not create is gone from the host")
			}
			// The listing and nothing else: a kill or even a has-session here
			// would be the daemon acting on a session that is not its business.
			if calls := f.tmux.Calls()[before:]; len(calls) != 1 || calls[0].Op != tmuxctl.OpList {
				t.Errorf("Adopt() ran %v, want the one listing", calls)
			}
		})
	}
}

// vanishingLister answers List honestly and then removes the session it named.
// It is the one shape the fake cannot produce on its own — present in the
// listing, gone by the time anything asks again — and it is what a session whose
// shell exited between the two questions looks like from here.
type vanishingLister struct {
	*tmuxctl.Fake

	name string
}

func (v vanishingLister) List(ctx context.Context) ([]tmuxctl.SessionInfo, error) {
	infos, err := v.Fake.List(ctx)
	v.Vanish(v.name)
	return infos, err
}

// US4 scenario 4, in the two shapes FR-022 distinguishes: a session that is no
// longer there when asked a second time, and one the host will not answer for at
// all. Neither becomes a record. A listing is not evidence a session is usable,
// and a record created on one would be the daemon reporting a session as healthy
// having never confirmed it.
func TestAdoptRecordsNothingItCouldNotConfirm(t *testing.T) {
	t.Parallel()

	t.Run("gone between the listing and the check", func(t *testing.T) {
		t.Parallel()

		f := newManagerFixture(t)
		id := testID("a")
		name := f.seedSurvivor(id, f.now.Add(-time.Hour))

		mgr, err := NewManagerWithClock(
			vanishingLister{Fake: f.tmux, name: name}, f.store, f.roots(), capNotUnderTest, stoppedClock{now: f.now},
		)
		if err != nil {
			t.Fatalf("NewManagerWithClock() unexpected error: %v", err)
		}

		got, err := mgr.Adopt(context.Background())
		if err != nil {
			t.Fatalf("Adopt() unexpected error: %v", err)
		}
		if len(got) != 0 {
			t.Errorf("Adopt() took %d sessions under management, want none — the window was gone", len(got))
		}
		if _, err := f.store.Get(id, auth.CallerOperator); !errors.Is(err, ErrSessionNotFound) {
			t.Errorf("the store holds a record for a session that vanished: %v", err)
		}
		if calls := f.tmux.Calls(); len(calls) != 2 || calls[1].Op != tmuxctl.OpHas {
			t.Errorf("Adopt() ran %v, want a listing and then a check", calls)
		}
	})

	t.Run("the host will not answer", func(t *testing.T) {
		t.Parallel()

		f := newManagerFixture(t)
		id := testID("a")
		f.seedSurvivor(id, f.now.Add(-time.Hour))
		f.tmux.FailOp(tmuxctl.OpHas, errTmuxBroken)

		got, err := f.mgr.Adopt(context.Background())
		if !errors.Is(err, errTmuxBroken) {
			t.Fatalf("Adopt() = _, %v; want the failure reported so startup can be fatal", err)
		}
		if len(got) != 0 {
			t.Errorf("Adopt() took %d sessions under management, want none", len(got))
		}
		if n := f.store.Len(); n != 0 {
			t.Errorf("the store holds %d records built on an unanswered question, want 0", n)
		}
	})
}

// US4 scenario 5, which is the point of reading the clock off the host: a
// session that started 23 hours before the daemon did dies an hour later, not 24
// hours later. The credential goes with it, because the two are one expression.
func TestAdoptCountsTheCeilingFromTheHostsOwnClock(t *testing.T) {
	t.Parallel()

	f := newManagerFixture(t)
	id := testID("a")
	f.seedSurvivor(id, f.now.Add(-23*time.Hour))

	got := mustAdoptOne(t, f.mgr)

	if want := f.now.Add(time.Hour); !got.Session.AbsoluteDeadline().Equal(want) {
		t.Fatalf("Adopt() absolute deadline %s, want %s — one hour of the ceiling is left", got.Session.AbsoluteDeadline(), want)
	}
	expiryToken := claimOne(t, f.mgr, id)
	if _, err := f.mgr.Resolve(id, auth.CallerOperator, expiryToken); err != nil {
		t.Errorf("Resolve() an hour before the ceiling = _, %v; want the record", err)
	}

	anHourLater := f.managerAt(t, f.store, f.now.Add(time.Hour))
	if _, err := anHourLater.Resolve(id, auth.CallerOperator, expiryToken); !errors.Is(err, ErrTokenExpired) {
		t.Errorf("Resolve() at the ceiling = _, %v; want %v", err, ErrTokenExpired)
	}
}

// US4 scenario 6 and FR-025: a session that outlived its ceiling while the
// daemon was down is torn down, not adopted into a state it is already past. The
// teardown is the verified one — a kill is asked for and then confirmed — so the
// outcome is the same claim Destroy makes and not a weaker one made at startup.
func TestAdoptDestroysWhatOutlivedItsCeiling(t *testing.T) {
	t.Parallel()

	ages := map[string]time.Duration{
		"exactly at the ceiling": AbsoluteLifetime,
		"an hour past it":        AbsoluteLifetime + time.Hour,
	}

	for name, age := range ages {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			f := newManagerFixture(t)
			id := testID("a")
			hostName := f.seedSurvivor(id, f.now.Add(-age))
			before := len(f.tmux.Calls())

			got, err := f.mgr.Adopt(context.Background())
			if err != nil {
				t.Fatalf("Adopt() unexpected error: %v", err)
			}
			if len(got) != 0 {
				t.Errorf("Adopt() took %d expired sessions under management, want none", len(got))
			}
			if n := f.store.Len(); n != 0 {
				t.Errorf("the store holds %d records for an expired session, want 0", n)
			}
			if _, ok := f.tmux.WorkDir(hostName); ok {
				t.Error("the expired session is still on the host after reconciliation")
			}

			wantOps := []tmuxctl.Op{tmuxctl.OpList, tmuxctl.OpKill, tmuxctl.OpHas}
			calls := f.tmux.Calls()[before:]
			if len(calls) != len(wantOps) {
				t.Fatalf("Adopt() ran %d tmux commands, want %d: %v", len(calls), len(wantOps), calls)
			}
			for i, op := range wantOps {
				if calls[i].Op != op {
					t.Errorf("command %d is %s, want %s", i, calls[i].Op, op)
				}
			}
		})
	}
}

// The other half of FR-025, and the one Principle VI turns on: an expired
// session the daemon could not confirm gone is reported, not swallowed. Startup
// is what makes that loud (T032) — and the session is deliberately still not
// adopted, because a record built for a session that is already past its ceiling
// is a record nothing would ever hand a credential for.
func TestAdoptReportsAnExpiredSessionItCouldNotTearDown(t *testing.T) {
	t.Parallel()

	f := newManagerFixture(t)
	id := testID("a")
	hostName := f.seedSurvivor(id, f.now.Add(-25*time.Hour))
	f.tmux.SurviveKill(hostName)

	got, err := f.mgr.Adopt(context.Background())
	if !errors.Is(err, ErrOrphanedSession) {
		t.Fatalf("Adopt() = _, %v; want one wrapping %v", err, ErrOrphanedSession)
	}
	if len(got) != 0 {
		t.Errorf("Adopt() took %d sessions under management, want none", len(got))
	}
	if n := f.store.Len(); n != 0 {
		t.Errorf("the store holds %d records for a session past its ceiling, want 0", n)
	}
	if _, ok := f.tmux.WorkDir(hostName); !ok {
		t.Error("the fixture did not leave the session present; the test proves nothing")
	}
}

// US4 scenario 7, in the shape a single process can produce it: a second pass
// over a store that already holds the record changes nothing about it. Adoption
// is what runs at startup, and a daemon that restarted in a loop would otherwise
// re-issue a credential and re-stamp an idle clock every time.
func TestAdoptLeavesARecordItAlreadyHasUntouched(t *testing.T) {
	t.Parallel()

	f := newManagerFixture(t)
	id := testID("a")
	f.seedSurvivor(id, f.now.Add(-time.Hour))

	first := mustAdoptOne(t, f.mgr)

	again := f.managerAt(t, f.store, f.now.Add(3*time.Hour))
	got, err := again.Adopt(context.Background())
	if err != nil {
		t.Fatalf("Adopt() a second time = _, %v; want success", err)
	}
	if len(got) != 0 {
		t.Fatalf("Adopt() took %d sessions under management a second time, want none", len(got))
	}

	stored, err := f.store.Get(id, auth.CallerOperator)
	if err != nil {
		t.Fatalf("the adopted record is gone after a second pass: %v", err)
	}
	if stored != first.Session {
		t.Errorf("the record is %+v after a second pass, want the one the first pass made", stored)
	}
	// The credential the first pass issued is still the session's, which is the
	// part a re-adoption would silently break for whoever is holding it.
	if _, err := again.Resolve(id, auth.CallerOperator, claimOne(t, again, id)); err != nil {
		t.Errorf("Resolve() with the first pass's credential = _, %v; want the record", err)
	}
}

// US4 scenario 7 again, in the shape a restart actually takes: a fresh store
// every time, and a ceiling that does not move. This is what makes the 24-hour
// bound a bound — if adoption read its own clock, a restart every 23 hours would
// hold a session open forever.
func TestAdoptDerivesTheSameCeilingHoweverManyRestartsItSurvives(t *testing.T) {
	t.Parallel()

	f := newManagerFixture(t)
	id := testID("a")
	started := f.now.Add(-3 * time.Hour)
	f.seedSurvivor(id, started)
	want := started.Add(AbsoluteLifetime)

	for i, at := range []time.Time{f.now, f.now.Add(4 * time.Hour), f.now.Add(9 * time.Hour)} {
		got := mustAdoptOne(t, f.managerAt(t, NewStore(), at))

		if !got.Session.AbsoluteDeadline().Equal(want) {
			t.Errorf("restart %d adopted a session with deadline %s, want the original %s", i, got.Session.AbsoluteDeadline(), want)
		}
		if !got.Session.LastActivity.Equal(at) {
			t.Errorf("restart %d left the idle clock at %s, want the adoption instant %s", i, got.Session.LastActivity, at)
		}
	}
}

// A host that cannot be listed is not an empty host. Reconciliation returns the
// failure rather than an empty result, because startup treats it as fatal
// (T032): carrying on would leave every surviving session unowned, uncapped and
// unreaped for as long as the daemon ran.
func TestAdoptReportsAHostItCannotList(t *testing.T) {
	t.Parallel()

	f := newManagerFixture(t)
	f.seedSurvivor(testID("a"), f.now.Add(-time.Hour))
	f.tmux.FailOp(tmuxctl.OpList, errTmuxBroken)

	got, err := f.mgr.Adopt(context.Background())
	if !errors.Is(err, errTmuxBroken) {
		t.Fatalf("Adopt() = _, %v; want the listing failure", err)
	}
	if got != nil {
		t.Errorf("Adopt() returned %v alongside a failure, want nothing", got)
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
		limit int
		clock Clock
	}{
		{"no controller", nil, NewStore(), wd.roots(), capNotUnderTest, stoppedClock{}},
		{"no store", tmuxctl.NewFake(), nil, wd.roots(), capNotUnderTest, stoppedClock{}},
		{"no clock", tmuxctl.NewFake(), NewStore(), wd.roots(), capNotUnderTest, nil},
		{"no approved roots", tmuxctl.NewFake(), NewStore(), nil, capNotUnderTest, stoppedClock{}},
		// A cap nobody set is the zero value, and a Manager built on it could
		// only ever refuse every create (FR-036).
		{"no session cap", tmuxctl.NewFake(), NewStore(), wd.roots(), 0, stoppedClock{}},
		{"a negative session cap", tmuxctl.NewFake(), NewStore(), wd.roots(), -1, stoppedClock{}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			m, err := NewManagerWithClock(tc.tmux, tc.store, tc.roots, tc.limit, tc.clock)
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

	m, err := NewManager(tmuxctl.NewFake(), NewStore(), wd.roots(), capNotUnderTest)
	if err != nil {
		t.Fatalf("NewManager() unexpected error: %v", err)
	}
	if _, ok := m.clock.(systemClock); !ok {
		t.Errorf("NewManager() clock is %T, want systemClock", m.clock)
	}
}

// fleetEventsSoFar is everything already delivered to a subscription.
//
// It needs no timeout and no polling because there is nothing to wait for:
// publish sends on the goroutine that changed the fleet, so every event a change
// produced is in the channel before the call that made it has returned. A
// deadline here would be hiding that property rather than testing it.
func fleetEventsSoFar(t *testing.T, ch <-chan FleetEvent) []FleetEvent {
	t.Helper()

	var got []FleetEvent
	for {
		select {
		case ev, open := <-ch:
			if !open {
				t.Fatal("the subscription was closed while the fleet was changing; a watcher is only ever dropped for falling behind")
			}
			got = append(got, ev)
		default:
			return got
		}
	}
}

// sortedFleet orders events so that a comparison is about which ones happened
// rather than in what order. Shutdown is why: DestroyAll walks the store's
// snapshot, which is deliberately in map order, so the sequence of a multi-record
// teardown is not a property anything may assert.
func sortedFleet(evs []FleetEvent) []FleetEvent {
	out := slices.Clone(evs)
	slices.SortFunc(out, func(a, b FleetEvent) int {
		if c := strings.Compare(a.ID, b.ID); c != 0 {
			return c
		}
		return strings.Compare(string(a.Kind), string(b.Kind))
	})
	return out
}

func appeared(id string) FleetEvent {
	return FleetEvent{Kind: FleetAppeared, ID: id, Owner: auth.CallerOperator}
}

func vanished(id string) FleetEvent {
	return FleetEvent{Kind: FleetVanished, ID: id, Owner: auth.CallerOperator}
}

// A fleet an operator is watching may not change behind their back (#15): the
// reaper destroyed a session while the dashboard went on drawing it as running,
// because nothing told the page. Every path that adds a record, removes one, or
// changes how one is drawn is driven here, through the same subscription the
// stream will hold.
//
// The three cases that expect nothing are the non-vacuous half. A create rolled
// back cleanly, a destroy the host would not confirm, and a request to a session
// that was already running all leave the fleet exactly as they found it, and
// announcing any of them would have an open page fetching a card that never
// appeared or dropping one that is still running.
func TestEveryFleetChangeEmits(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		// arrange sets the case up and returns two things: the Manager whose
		// events it is about, and the change to make once a subscriber is
		// watching. They are separate because the subscription must exist before
		// the change does — and because the Manager is not always the fixture's
		// own. The reaper and a restart each build a second one over the same
		// store, and an event belongs to the Manager the change went through.
		arrange func(t *testing.T, f managerFixture) (*Manager, func(t *testing.T) []FleetEvent)
	}{
		{
			name: "a create",
			arrange: func(_ *testing.T, f managerFixture) (*Manager, func(*testing.T) []FleetEvent) {
				return f.mgr, func(t *testing.T) []FleetEvent {
					s, _ := mustCreate(t, f, f.request())
					return []FleetEvent{appeared(s.ID)}
				}
			},
		},
		{
			// The record is kept, so the fleet gained a session even though the
			// create reported failure — and the dashboard will draw it.
			name: "a create whose teardown could not be verified",
			arrange: func(_ *testing.T, f managerFixture) (*Manager, func(*testing.T) []FleetEvent) {
				return f.mgr, func(t *testing.T) []FleetEvent {
					f.tmux.FailOp(tmuxctl.OpSendKeys, errTmuxBroken)
					f.tmux.FailOp(tmuxctl.OpKill, errTmuxBroken)

					if _, _, err := f.mgr.Create(context.Background(), f.request()); !errors.Is(err, ErrOrphanedSession) {
						t.Fatalf("Create() = _, _, %v; want one wrapping %v", err, ErrOrphanedSession)
					}
					held := f.store.List(auth.CallerOperator)
					if len(held) != 1 {
						t.Fatalf("the store holds %d records after an unverifiable teardown, want the retained one", len(held))
					}
					return []FleetEvent{appeared(held[0].ID)}
				}
			},
		},
		{
			name: "a create rolled back cleanly",
			arrange: func(_ *testing.T, f managerFixture) (*Manager, func(*testing.T) []FleetEvent) {
				return f.mgr, func(t *testing.T) []FleetEvent {
					f.tmux.FailOp(tmuxctl.OpSendKeys, errTmuxBroken)

					if _, _, err := f.mgr.Create(context.Background(), f.request()); err == nil {
						t.Fatal("Create() succeeded with send-keys failing")
					}
					if n := f.store.Len(); n != 0 {
						t.Fatalf("the store holds %d records after a verified rollback, want 0", n)
					}
					return nil
				}
			},
		},
		{
			name: "a destroy",
			arrange: func(t *testing.T, f managerFixture) (*Manager, func(*testing.T) []FleetEvent) {
				s, _ := mustCreate(t, f, f.request())
				return f.mgr, func(t *testing.T) []FleetEvent {
					if err := f.mgr.Destroy(context.Background(), *s); err != nil {
						t.Fatalf("Destroy() unexpected error: %v", err)
					}
					return []FleetEvent{vanished(s.ID)}
				}
			},
		},
		{
			// The record is retained for the next sweep, so nothing left the
			// fleet — which is the one thing a page must not be told.
			name: "a destroy the host would not confirm",
			arrange: func(t *testing.T, f managerFixture) (*Manager, func(*testing.T) []FleetEvent) {
				s, _ := mustCreate(t, f, f.request())
				return f.mgr, func(t *testing.T) []FleetEvent {
					f.tmux.FailOp(tmuxctl.OpKill, errTmuxBroken)

					if err := f.mgr.Destroy(context.Background(), *s); !errors.Is(err, ErrOrphanedSession) {
						t.Fatalf("Destroy() = %v; want one wrapping %v", err, ErrOrphanedSession)
					}
					if _, err := f.store.Get(s.ID, auth.CallerOperator); err != nil {
						t.Fatalf("the record was dropped on an unconfirmed teardown: Get() = _, %v", err)
					}
					return nil
				}
			},
		},
		{
			// Shutdown, and the reason a slow subscriber may not hold one: two
			// records, one event each, on the way out of the process.
			name: "shutdown tearing the whole fleet down",
			arrange: func(t *testing.T, f managerFixture) (*Manager, func(*testing.T) []FleetEvent) {
				first, _ := mustCreate(t, f, f.request())
				second, _ := mustCreate(t, f, f.request())
				return f.mgr, func(t *testing.T) []FleetEvent {
					if _, err := f.mgr.DestroyAll(context.Background()); err != nil {
						t.Fatalf("DestroyAll() unexpected error: %v", err)
					}
					return []FleetEvent{vanished(first.ID), vanished(second.ID)}
				}
			},
		},
		{
			// Issue #15 itself. Nobody asked for this teardown, so the event is
			// the only way an open page can learn of it.
			name: "the reaper's sweep",
			arrange: func(t *testing.T, f managerFixture) (*Manager, func(*testing.T) []FleetEvent) {
				s, _ := mustCreate(t, f, f.request())
				r := reaperAt(t, f, f.now.Add(IdleTimeout))
				return r.mgr, func(t *testing.T) []FleetEvent {
					if reaped := mustSweep(t, r); len(reaped) != 1 {
						t.Fatalf("the sweep took %d sessions, want the idle one", len(reaped))
					}
					return []FleetEvent{vanished(s.ID)}
				}
			},
		},
		{
			name: "startup adoption",
			arrange: func(_ *testing.T, f managerFixture) (*Manager, func(*testing.T) []FleetEvent) {
				id := testID("a")
				f.seedSurvivor(id, f.now.Add(-2*time.Hour))
				return f.mgr, func(t *testing.T) []FleetEvent {
					mustAdoptOne(t, f.mgr)
					return []FleetEvent{appeared(id)}
				}
			},
		},
		{
			// #21's discovery: a session that died on its own, found by the one
			// thing a read does that touches the window.
			name: "a capture that finds the session already gone",
			arrange: func(t *testing.T, f managerFixture) (*Manager, func(*testing.T) []FleetEvent) {
				s, _ := mustCreate(t, f, f.request())
				f.tmux.Vanish(tmuxNamePrefix + s.ID)
				return f.mgr, func(t *testing.T) []FleetEvent {
					if _, err := f.mgr.Output(context.Background(), *s); !errors.Is(err, ErrSessionDead) {
						t.Fatalf("Output() = _, %v; want %v", err, ErrSessionDead)
					}
					return []FleetEvent{vanished(s.ID)}
				}
			},
		},
		{
			// No record entered or left, and the card an open page is drawing is
			// wrong all the same: the pill said idle a moment ago.
			name: "activity that brings an idle session back",
			arrange: func(t *testing.T, f managerFixture) (*Manager, func(*testing.T) []FleetEvent) {
				s, tok := mustCreate(t, f, f.request())
				mgr := f.managerAt(t, f.store, f.now.Add(IdleTimeout))
				return mgr, func(t *testing.T) []FleetEvent {
					if _, err := mgr.Resolve(s.ID, auth.CallerOperator, tok); err != nil {
						t.Fatalf("Resolve() unexpected error: %v", err)
					}
					return []FleetEvent{{Kind: FleetChanged, ID: s.ID, Owner: auth.CallerOperator}}
				}
			},
		},
		{
			// No record entered or left, and the card an open page is drawing is
			// wrong all the same: it carries a label the daemon no longer holds.
			name: "a rename",
			arrange: func(t *testing.T, f managerFixture) (*Manager, func(*testing.T) []FleetEvent) {
				s, _ := mustCreate(t, f, f.request())
				return f.mgr, func(t *testing.T) []FleetEvent {
					if _, err := f.mgr.Rename(*s, hostileLabel); err != nil {
						t.Fatalf("Rename() unexpected error: %v", err)
					}
					return []FleetEvent{{Kind: FleetChanged, ID: s.ID, Owner: auth.CallerOperator}}
				}
			},
		},
		{
			// The Resolve case's fact, reached the way a browser reaches it. A
			// compact defers the idle deadline (data-model.md), so the pill that
			// said idle a moment ago is wrong — again with no record entering or
			// leaving, and again with nobody but the operator's own action behind it.
			name: "a compact that brings an idle session back",
			arrange: func(t *testing.T, f managerFixture) (*Manager, func(*testing.T) []FleetEvent) {
				s, _ := mustCreate(t, f, f.request())
				mgr := f.managerAt(t, f.store, f.now.Add(IdleTimeout))
				return mgr, func(t *testing.T) []FleetEvent {
					if err := mgr.Compact(context.Background(), *s); err != nil {
						t.Fatalf("Compact() unexpected error: %v", err)
					}
					return []FleetEvent{{Kind: FleetChanged, ID: s.ID, Owner: auth.CallerOperator}}
				}
			},
		},
		{
			name: "activity on a session that was already running",
			arrange: func(t *testing.T, f managerFixture) (*Manager, func(*testing.T) []FleetEvent) {
				s, tok := mustCreate(t, f, f.request())
				return f.mgr, func(t *testing.T) []FleetEvent {
					if _, err := f.mgr.Resolve(s.ID, auth.CallerOperator, tok); err != nil {
						t.Fatalf("Resolve() unexpected error: %v", err)
					}
					return nil
				}
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f := newManagerFixture(t)
			mgr, change := tc.arrange(t, f)

			events, cancel := mgr.Subscribe(auth.CallerOperator)
			defer cancel()

			want := change(t)
			got := fleetEventsSoFar(t, events)
			if !slices.Equal(sortedFleet(got), sortedFleet(want)) {
				t.Errorf("the fleet announced %v, want %v", got, want)
			}
		})
	}
}

// The non-blocking half of FR-019a, stated the way it will actually be broken: a
// dashboard tab that stopped reading — a laptop asleep, a browser that never
// closed the connection — must not be able to hold a teardown open.
//
// The subscriber is filled to the brim first, so the destroy's event is the send
// with nowhere to go. What follows is the whole ruling: the change completes, the
// events already accepted are still delivered, and the subscription is *closed*
// rather than quietly missing one. A page whose stream ends can say updates have
// stopped (FR-020); one still receiving events with a gap in them cannot.
func TestASubscriberThatStoppedReadingIsDroppedNotWaitedFor(t *testing.T) {
	t.Parallel()

	f := newManagerFixture(t)
	s, _ := mustCreate(t, f, f.request())

	// The deferred cancel is itself an assertion: the publisher below closes
	// this subscription, and a drop that was not idempotent would panic here
	// rather than in the body.
	events, cancel := f.mgr.Subscribe(auth.CallerOperator)
	defer cancel()
	for range fleetBacklog {
		f.mgr.emit(FleetChanged, *s)
	}

	if err := f.mgr.Destroy(context.Background(), *s); err != nil {
		t.Fatalf("Destroy() = %v; a watcher that stopped reading must not be able to hold a teardown", err)
	}
	if _, err := f.store.Get(s.ID, auth.CallerOperator); !errors.Is(err, ErrSessionNotFound) {
		t.Errorf("the record survived a destroy run against a full subscriber: Get() = _, %v, want %v", err, ErrSessionNotFound)
	}

	for i := range fleetBacklog {
		if _, open := <-events; !open {
			t.Fatalf("the subscription closed after %d of %d buffered events; nothing already accepted may be discarded", i, fleetBacklog)
		}
	}
	// A select rather than a receive, so that a subscription left open and empty
	// says so instead of hanging: the drop has already happened if it is going
	// to, because publish runs on the goroutine the destroy above returned from.
	select {
	case ev, open := <-events:
		if open {
			t.Fatalf("the subscription is still open after falling behind, next %+v; a watcher that cannot be kept current must be dropped", ev)
		}
	default:
		t.Fatal("the subscription is open and empty after falling behind; a watcher quietly missing an event is a page presenting a fleet it cannot vouch for")
	}
}

// The other half of the same rule, and the ordinary case in production: for most
// of a daemon's life nobody has a dashboard open at all. Every path that emits
// must complete with no subscriber to send to.
func TestAFleetChangeCompletesWithNobodyListening(t *testing.T) {
	t.Parallel()

	f := newManagerFixture(t)

	mustCreate(t, f, f.request())
	destroyed, _ := mustCreate(t, f, f.request())
	if err := f.mgr.Destroy(context.Background(), *destroyed); err != nil {
		t.Fatalf("Destroy() with nobody watching: %v", err)
	}

	// One record left, and the sweep is the path that has no request behind it
	// at all — the one #15 was reported against.
	r := reaperAt(t, f, f.now.Add(IdleTimeout))
	if got := mustSweep(t, r); len(got) != 1 {
		t.Fatalf("the sweep took %d sessions with nobody watching, want the 1 left", len(got))
	}

	mustCreate(t, f, f.request())
	if _, err := f.mgr.DestroyAll(context.Background()); err != nil {
		t.Fatalf("DestroyAll() with nobody watching: %v", err)
	}
	if n := f.store.Len(); n != 0 {
		t.Errorf("the store holds %d records after an unwatched shutdown, want 0", n)
	}
}

// Being a stream rather than a page is not a way around the ownership check
// (FR-019b). The filter is on the subscription rather than on what the
// subscriber does with what it receives, so a second identity's session cannot
// reach the wire even if the code that writes it forgets to look.
func TestAFleetEventReachesOnlyItsOwner(t *testing.T) {
	t.Parallel()

	const otherOwner auth.CallerID = "a-second-operator"

	f := newManagerFixture(t)

	mine, cancelMine := f.mgr.Subscribe(auth.CallerOperator)
	defer cancelMine()
	theirs, cancelTheirs := f.mgr.Subscribe(otherOwner)
	defer cancelTheirs()
	// An identity nobody established. Store.Add refuses a record without an
	// owner, so the only way to ask this is to have skipped the door.
	nobody, cancelNobody := f.mgr.Subscribe("")
	defer cancelNobody()

	req := f.request()
	req.Owner = otherOwner
	s, _ := mustCreate(t, f, req)

	if got := fleetEventsSoFar(t, mine); len(got) != 0 {
		t.Errorf("another identity's session produced %v on this owner's subscription; want nothing at all", got)
	}
	want := []FleetEvent{{Kind: FleetAppeared, ID: s.ID, Owner: otherOwner}}
	if got := fleetEventsSoFar(t, theirs); !slices.Equal(got, want) {
		t.Errorf("the owning identity received %v, want %v", got, want)
	}
	if ev, open := <-nobody; open {
		t.Errorf("Subscribe(\"\") is open and delivered %+v; a caller with no identity must receive nothing and find out at once", ev)
	}
}

// Ending a subscription twice is the ordinary case rather than a misuse: the
// stream defers its cancel and the daemon drops a subscriber that falls behind,
// so both can happen to one subscription. A close that ran twice would panic in
// a deferred call, taking down a goroutine that was cleaning up correctly.
func TestEndingASubscriptionTwiceIsSafe(t *testing.T) {
	t.Parallel()

	f := newManagerFixture(t)
	events, cancel := f.mgr.Subscribe(auth.CallerOperator)

	cancel()
	cancel()

	if ev, open := <-events; open {
		t.Errorf("a cancelled subscription is still open and delivered %+v", ev)
	}
	// Nothing reaches a subscription that ended, and the change itself is
	// unaffected by having had one.
	if _, _, err := f.mgr.Create(context.Background(), f.request()); err != nil {
		t.Errorf("Create() after a cancelled subscription: %v", err)
	}
}

// TestTheStartCommandIsTheConfiguredOne is #38's wiring, asserted at the one
// place it can be observed: the argv the host was actually given.
//
// The default path is checked alongside the configured one because the whole
// promise of this feature is that an operator who configures nothing gets the
// daemon they already had. A manager that was never given a set must still type
// the built-in command, and it must still type it through send-keys with the
// line the operator never chose.
func TestTheStartCommandIsTheConfiguredOne(t *testing.T) {
	t.Parallel()

	t.Run("unconfigured means the built-in default", func(t *testing.T) {
		t.Parallel()

		f := newManagerFixture(t)
		if _, _, err := f.mgr.Create(context.Background(), f.request()); err != nil {
			t.Fatalf("Create() = %v", err)
		}
		assertTypedIntoTheShell(t, f.tmux, claudeStartCommand)
	})

	t.Run("a configured name is the line that gets typed", func(t *testing.T) {
		t.Parallel()

		f := newManagerFixture(t)
		f.mgr.SetStartCommands(config.NewStartCommands(map[string]string{
			"default": "claude",
			"rc":      "claude remote-control --permission-mode bypassPermissions",
		}))

		req := f.request()
		req.StartCommand = "rc"
		if _, _, err := f.mgr.Create(context.Background(), req); err != nil {
			t.Fatalf("Create() = %v", err)
		}
		assertTypedIntoTheShell(t, f.tmux, "claude remote-control --permission-mode bypassPermissions")
	})

	// The refusal is the half worth having. A create that named a command this
	// daemon does not have must leave nothing behind — no record, no tmux
	// session, no token — exactly as an unusable name does, and it must not
	// quietly fall back to the default, because a caller who asked for remote
	// control and got a plain session has no way to find that out.
	t.Run("an unknown name creates nothing", func(t *testing.T) {
		t.Parallel()

		f := newManagerFixture(t)
		req := f.request()
		req.StartCommand = "no-such-command"

		_, _, err := f.mgr.Create(context.Background(), req)
		if !errors.Is(err, ErrUnknownStartCommand) {
			t.Fatalf("Create(unknown start command) = %v; want %v", err, ErrUnknownStartCommand)
		}
		if got := len(f.store.List(f.request().Owner)); got != 0 {
			t.Errorf("the store holds %d records; a refused create leaves none", got)
		}
		for _, call := range f.tmux.Calls() {
			if call.Op == tmuxctl.OpNew {
				t.Error("a refused create started a tmux session")
			}
		}
	})
}

// TestTheStartCommandCarriesTheSessionName is #58's second half: the name the
// operator gave a session here is the name that shows up on the other side.
//
// The assertions are byte-for-byte on the argv send-keys was handed, because
// that string is what gets typed at an unsandboxed shell. A test that only
// checked the name appeared *somewhere* in the line would pass for a command
// whose shape the name had changed, which is the one failure the substitution's
// licence rests on being impossible.
func TestTheStartCommandCarriesTheSessionName(t *testing.T) {
	t.Parallel()

	const rc = "claude remote-control --permission-mode bypassPermissions --spawn=same-dir --name {name}"

	t.Run("the rendered line carries this session's own name", func(t *testing.T) {
		t.Parallel()

		f := newManagerFixture(t)
		f.mgr.SetStartCommands(config.NewStartCommands(map[string]string{
			"default": "claude --dangerously-skip-permissions",
			"rc":      rc,
		}))

		req := f.request()
		req.StartCommand = "rc"
		if _, _, err := f.mgr.Create(context.Background(), req); err != nil {
			t.Fatalf("Create() = %v", err)
		}
		assertTypedIntoTheShell(t,
			f.tmux,
			"claude remote-control --permission-mode bypassPermissions --spawn=same-dir --name refactor-auth")
	})

	// Every boundary of ValidateName's ^[a-zA-Z0-9-]{1,64}$, each one producing a
	// command line with exactly the expected shape. These are the names that
	// would break the substitution if it were unsafe — and none of them can,
	// which is the property being pinned rather than assumed.
	t.Run("a name at every boundary renders exactly one argument", func(t *testing.T) {
		t.Parallel()

		names := []string{
			"a",
			"A",
			"0",
			"-",
			"---",
			"a-b-c",
			"0000000000",
			strings.Repeat("x", MaxNameLen),
			strings.Repeat("-", MaxNameLen),
			"UPPER-lower-0123456789",
		}
		for _, name := range names {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				f := newManagerFixture(t)
				f.mgr.SetStartCommands(config.NewStartCommands(map[string]string{
					"default": "claude --dangerously-skip-permissions",
					"rc":      rc,
				}))

				req := f.request()
				req.StartCommand = "rc"
				req.Name = name
				if _, _, err := f.mgr.Create(context.Background(), req); err != nil {
					t.Fatalf("Create(%q) = %v", name, err)
				}
				assertTypedIntoTheShell(t, f.tmux,
					"claude remote-control --permission-mode bypassPermissions --spawn=same-dir --name "+name)
			})
		}
	})

	// A command without the placeholder is typed byte for byte, so the daemon an
	// operator had before #58 is the daemon they still have.
	t.Run("a command with no placeholder is untouched", func(t *testing.T) {
		t.Parallel()

		f := newManagerFixture(t)
		f.mgr.SetStartCommands(config.NewStartCommands(map[string]string{
			"default": "claude --dangerously-skip-permissions",
		}))
		if _, _, err := f.mgr.Create(context.Background(), f.request()); err != nil {
			t.Fatalf("Create() = %v", err)
		}
		assertTypedIntoTheShell(t, f.tmux, "claude --dangerously-skip-permissions")
	})

	// A session with no name cannot supply one, and the refusal must leave
	// nothing behind — an empty --name would start a session the operator then
	// cannot find under the label they chose.
	t.Run("no name to substitute creates nothing", func(t *testing.T) {
		t.Parallel()

		f := newManagerFixture(t)
		f.mgr.SetStartCommands(config.NewStartCommands(map[string]string{
			"default": rc,
		}))

		req := f.request()
		req.Name = ""

		_, _, err := f.mgr.Create(context.Background(), req)
		if !errors.Is(err, ErrInvalidName) {
			t.Fatalf("Create(no name) = %v; want %v", err, ErrInvalidName)
		}
		for _, call := range f.tmux.Calls() {
			if call.Op == tmuxctl.OpNew {
				t.Error("a refused create started a tmux session")
			}
		}
	})
}

// assertTypedIntoTheShell finds the send-keys the start performed and checks the
// line it carried.
func assertTypedIntoTheShell(t *testing.T, fake *tmuxctl.Fake, want string) {
	t.Helper()

	for _, call := range fake.Calls() {
		if call.Op != tmuxctl.OpSendKeys {
			continue
		}
		for _, arg := range call.Argv {
			if arg == want {
				return
			}
		}
		t.Fatalf("send-keys argv = %q; want it to carry %q", call.Argv, want)
	}
	t.Fatalf("nothing was typed into the shell at all; want %q", want)
}

// TestLifetimeOverrides is #37: the bounds become the operator's to set, without
// stopping being bounds.
func TestLifetimeOverrides(t *testing.T) {
	t.Parallel()

	t.Run("unconfigured is the daemon that shipped", func(t *testing.T) {
		t.Parallel()

		f := newManagerFixture(t)
		s, _, err := f.mgr.Create(context.Background(), f.request())
		if err != nil {
			t.Fatalf("Create() = %v", err)
		}
		if got, want := s.AbsoluteDeadline(), s.CreatedAt.Add(AbsoluteLifetime); !got.Equal(want) {
			t.Errorf("absolute deadline = %v; want %v", got, want)
		}
		if got, want := s.IdleDeadline(), s.LastActivity.Add(IdleTimeout); !got.Equal(want) {
			t.Errorf("idle deadline = %v; want %v", got, want)
		}
	})

	t.Run("an override beats the default", func(t *testing.T) {
		t.Parallel()

		f := newManagerFixture(t)
		f.mgr.SetLifetimes(AbsoluteLifetime, 72*time.Hour, IdleTimeout, IdleTimeout)

		req := f.request()
		req.Lifetime = 48 * time.Hour
		s, _, err := f.mgr.Create(context.Background(), req)
		if err != nil {
			t.Fatalf("Create(48h) = %v", err)
		}
		if got, want := s.AbsoluteDeadline(), s.CreatedAt.Add(48*time.Hour); !got.Equal(want) {
			t.Errorf("absolute deadline = %v; want %v", got, want)
		}
	})

	// The ceiling is the whole point. An override past it is refused rather than
	// clamped: a caller who asked for thirty days and silently got one believes
	// they have thirty until the session is gone.
	t.Run("past the ceiling creates nothing", func(t *testing.T) {
		t.Parallel()

		f := newManagerFixture(t)
		f.mgr.SetLifetimes(AbsoluteLifetime, 48*time.Hour, IdleTimeout, IdleTimeout)

		req := f.request()
		req.Lifetime = 100 * time.Hour
		if _, _, err := f.mgr.Create(context.Background(), req); !errors.Is(err, ErrInvalidLifetime) {
			t.Fatalf("Create(past the ceiling) = %v; want %v", err, ErrInvalidLifetime)
		}
		if got := len(f.store.List(f.request().Owner)); got != 0 {
			t.Errorf("the store holds %d records; a refused create leaves none", got)
		}
	})

	// Disabling idle reaping is safe only because the absolute deadline still
	// fires. If that ever stops being true this is a hole, not a knob — so the
	// assertion is on the absolute deadline, not on the idle one.
	t.Run("idle disabled still expires absolutely", func(t *testing.T) {
		t.Parallel()

		f := newManagerFixture(t)
		req := f.request()
		req.Idle = -1
		s, _, err := f.mgr.Create(context.Background(), req)
		if err != nil {
			t.Fatalf("Create(idle disabled) = %v", err)
		}
		if !s.IdleDeadline().After(s.AbsoluteDeadline()) {
			t.Error("a session with idle reaping disabled can still be reaped for idleness first")
		}
		if got, want := s.AbsoluteDeadline(), s.CreatedAt.Add(AbsoluteLifetime); !got.Equal(want) {
			t.Errorf("absolute deadline = %v; want %v — disabling idle must not disable this", got, want)
		}
	})
}

// TestAdoptionRestoresNameAndWorkDir is #72: a session that survives a restart
// comes back as itself rather than as an anonymous row.
//
// This mattered little while adoption only ran after a crash. Sessions survive a
// restart now (#63), so it runs on every redeploy — and an operator was left
// with a fleet of unnamed cards in unknown directories, which is what a fleet
// view is for telling apart.
func TestAdoptionRestoresNameAndWorkDir(t *testing.T) {
	t.Parallel()

	f := newManagerFixture(t)
	req := f.request()
	created, _, err := f.mgr.Create(context.Background(), req)
	if err != nil {
		t.Fatalf("Create() = %v", err)
	}

	// A second manager on the same host with an empty store is the restart: the
	// records are gone, the tmux sessions are not.
	fresh, err := NewManagerWithClock(f.tmux, NewStore(), f.roots(), capNotUnderTest, stoppedClock{now: f.now})
	if err != nil {
		t.Fatalf("NewManagerWithClock() = %v", err)
	}
	if _, err := fresh.Adopt(context.Background()); err != nil {
		t.Fatalf("Adopt() = %v", err)
	}

	owned := fresh.List(req.Owner)
	if len(owned) != 1 {
		t.Fatalf("the fresh manager adopted %d sessions; want 1", len(owned))
	}
	got := owned[0]
	if got.Name != created.Name {
		t.Errorf("adopted name = %q; want %q — the label is written to the host at create for exactly this", got.Name, created.Name)
	}
	if got.WorkDir != created.WorkDir {
		t.Errorf("adopted work dir = %q; want %q", got.WorkDir, created.WorkDir)
	}
	if !got.Adopted {
		t.Error("a reclaimed session is not marked adopted")
	}
}
