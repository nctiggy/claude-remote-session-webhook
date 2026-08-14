package session

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nctiggy/claude-remote-session-webhook/internal/audit"
	"github.com/nctiggy/claude-remote-session-webhook/internal/auth"
	"github.com/nctiggy/claude-remote-session-webhook/internal/tmuxctl"
)

// reaperAt is the daemon a chosen amount of time later: the same host and the
// same store the fixture's own manager has, on a clock stopped at `at`.
//
// Every test here moves time this way rather than by waiting for it. The records
// were made at f.now, the sweep asks its question from `at`, and the difference
// between the two is the only thing under test — which is FR-039 in a helper.
func reaperAt(t *testing.T, f managerFixture, at time.Time) *Reaper {
	t.Helper()

	r, _ := auditedReaperAt(t, f, at)
	return r
}

// auditedReaperAt is reaperAt with the trail readable, for the tests that assert
// what a sweep wrote rather than what it destroyed. The sink is returned rather
// than reached for through the Reaper, which holds the Logger writing into it
// and not the bytes.
func auditedReaperAt(t *testing.T, f managerFixture, at time.Time) (*Reaper, *bytes.Buffer) {
	t.Helper()

	sink := &bytes.Buffer{}
	r, err := NewReaper(f.managerAt(t, f.store, at), audit.NewTo(sink, func() time.Time { return at }))
	if err != nil {
		t.Fatalf("NewReaper() unexpected error: %v", err)
	}
	return r, sink
}

// reapRecords decodes everything a sweep wrote to the trail.
func reapRecords(t *testing.T, sink *bytes.Buffer) []map[string]any {
	t.Helper()

	var out []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(sink.String()), "\n") {
		if line == "" {
			continue
		}
		var rec map[string]any
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Fatalf("audit line %q is not JSON: %v", line, err)
		}
		out = append(out, rec)
	}
	return out
}

// onlyReapRecord asserts that a sweep produced exactly one record — FR-041's
// whole claim, applied to the teardown that has no request behind it — and
// returns it.
func onlyReapRecord(t *testing.T, sink *bytes.Buffer) map[string]any {
	t.Helper()

	got := reapRecords(t, sink)
	if len(got) != 1 {
		t.Fatalf("the sweep emitted %d audit records (%v); FR-041 requires exactly one per session it acted on", len(got), got)
	}
	return got[0]
}

func mustSweep(t *testing.T, r *Reaper) []Reaped {
	t.Helper()

	reaped, err := r.Sweep(context.Background())
	if err != nil {
		t.Fatalf("Sweep() unexpected error: %v", err)
	}
	return reaped
}

// manualTicker is a heartbeat a test drives by hand.
//
// The channel is unbuffered on purpose, and that is what makes these tests
// deterministic without a sleep: Run takes one tick, sweeps, and only then comes
// back to the select, so a *second* send cannot complete until the first sweep
// has finished. Waiting for the second send is therefore waiting for the first
// sweep, exactly, with no polling and no guess at how long a sweep takes.
type manualTicker struct {
	ticks   chan time.Time
	stopped chan struct{}
}

func newManualTicker() *manualTicker {
	return &manualTicker{ticks: make(chan time.Time), stopped: make(chan struct{}, 1)}
}

func (m *manualTicker) source(time.Duration) (<-chan time.Time, func()) {
	return m.ticks, func() {
		select {
		case m.stopped <- struct{}{}:
		default:
		}
	}
}

// tick delivers one tick, returning once the loop has taken it.
func (m *manualTicker) tick() { m.ticks <- time.Time{} }

// run starts the loop and returns a function that stops it and waits for it to
// return, so every assertion afterwards reads state nothing is still writing.
func (m *manualTicker) run(t *testing.T, r *Reaper, ctx context.Context) func() {
	t.Helper()

	r.ticker = m.source
	stop := make(chan struct{})
	loop, cancel := context.WithCancel(ctx)

	go func() {
		defer close(stop)
		r.Run(loop)
	}()

	return func() {
		cancel()
		<-stop
	}
}

// The ceiling is not renewed by use (FR-038), and this is the shape that proves
// it: a session driven right up to the 24-hour mark still dies at the mark. A
// reaper that renewed on use would keep this session alive forever, one request
// at a time, which is the arrangement the absolute lifetime exists to refuse.
func TestSweepDestroysASessionPastItsCeilingHoweverRecentlyItWasUsed(t *testing.T) {
	t.Parallel()

	f := newManagerFixture(t)
	s, _ := mustCreate(t, f, f.request())

	ceiling := f.now.Add(AbsoluteLifetime)
	if err := f.store.Touch(s.ID, ceiling); err != nil {
		t.Fatalf("Touch() unexpected error: %v", err)
	}

	reaped := mustSweep(t, reaperAt(t, f, ceiling))

	if len(reaped) != 1 {
		t.Fatalf("Sweep() took %d sessions at the ceiling, want exactly 1", len(reaped))
	}
	if reaped[0].Expiry != ExpiryAbsolute {
		t.Errorf("Sweep() reaped an actively used session as %q, want %q", reaped[0].Expiry, ExpiryAbsolute)
	}
	if _, ok := f.store.lookup(s.ID); ok {
		t.Error("Sweep() kept the record of a session past its ceiling")
	}
}

// "The same verified teardown as an explicit destroy" (FR-038), asserted as the
// commands rather than as the outcome: kill, then ask the host whether it worked.
// A reaper that killed directly would report every bound enforced whenever tmux
// was willing to say anything at all.
func TestSweepTearsDownTheWayAnExplicitDestroyDoes(t *testing.T) {
	t.Parallel()

	f := newManagerFixture(t)
	s, _ := mustCreate(t, f, f.request())
	before := len(f.tmux.Calls())

	mustSweep(t, reaperAt(t, f, f.now.Add(AbsoluteLifetime)))

	name := s.TmuxName()
	want := []tmuxctl.Call{
		// No list. The sweep asked the host for every session's activity time
		// until milestone 15, so the idle bound could see output the daemon never
		// mediated; with that bound withdrawn the ceiling is measured from
		// CreatedAt, which the record already holds. A list here would be an exec
		// per sweep for a question nothing asks.
		{Op: tmuxctl.OpKill, Argv: []string{"tmux", "kill-session", "-t", "=" + name}},
		{Op: tmuxctl.OpHas, Argv: []string{"tmux", "has-session", "-t", "=" + name}},
	}

	got := f.tmux.Calls()[before:]
	if len(got) != len(want) {
		t.Fatalf("Sweep() ran %d tmux commands, want %d: %v", len(got), len(want), got)
	}
	for i := range want {
		if got[i].Op != want[i].Op || !slices.Equal(got[i].Argv, want[i].Argv) {
			t.Errorf("command %d is %s %q, want %s %q", i, got[i].Op, got[i].Argv, want[i].Op, want[i].Argv)
		}
	}
	if _, ok := f.tmux.WorkDir(name); ok {
		t.Error("the tmux session survived a sweep that reported it destroyed")
	}
}

// Every record is judged against its own deadline and nothing else's. The two
// sessions here are the same age and only one of them is past its own bound —
// which is what a per-session lifetime override has to mean if it means anything.
func TestSweepLeavesEverySessionInsideItsBoundsAlone(t *testing.T) {
	t.Parallel()

	f := newManagerFixture(t)
	f.mgr.SetLifetimes(AbsoluteLifetime, 72*time.Hour)

	longer := f.request()
	longer.Lifetime = 48 * time.Hour
	kept, _, err := f.mgr.Create(context.Background(), longer)
	if err != nil {
		t.Fatalf("Create(48h) = %v", err)
	}
	expired, _ := mustCreate(t, f, f.request())

	reaped := mustSweep(t, reaperAt(t, f, f.now.Add(AbsoluteLifetime)))

	if len(reaped) != 1 {
		t.Fatalf("Sweep() took %d sessions, want exactly 1", len(reaped))
	}
	if reaped[0].Session.ID != expired.ID {
		t.Errorf("Sweep() reaped %q, want the expired %q", reaped[0].Session.ID, expired.ID)
	}
	if _, ok := f.store.lookup(kept.ID); !ok {
		t.Error("Sweep() destroyed a session still inside its own longer lifetime")
	}
	if _, ok := f.tmux.WorkDir(kept.TmuxName()); !ok {
		t.Error("Sweep() killed the tmux session of a record it left in the store")
	}
}

// The orphan path (FR-019), reached by the reaper rather than by a caller. tmux
// reports the kill worked and the session is still there, so the record is kept —
// and because the deadline it passed does not move, the next sweep tries again.
// That retry is the property: nothing else is coming for this session.
func TestSweepKeepsARecordItCouldNotConfirmGoneAndTriesAgain(t *testing.T) {
	t.Parallel()

	f := newManagerFixture(t)
	s, _ := mustCreate(t, f, f.request())
	f.tmux.SurviveKill(s.TmuxName())

	r := reaperAt(t, f, f.now.Add(AbsoluteLifetime))

	reaped, err := r.Sweep(context.Background())
	if err == nil {
		t.Fatal("Sweep() reported success for a session that is still running")
	}
	if !errors.Is(err, ErrOrphanedSession) {
		t.Errorf("Sweep() error = %v, want one wrapping %v", err, ErrOrphanedSession)
	}
	if len(reaped) != 0 {
		t.Errorf("Sweep() reported %d sessions destroyed that are still on the host", len(reaped))
	}
	if _, ok := f.store.lookup(s.ID); !ok {
		t.Fatal("Sweep() dropped the record of a session that may still be running")
	}

	if _, err := r.Sweep(context.Background()); !errors.Is(err, ErrOrphanedSession) {
		t.Errorf("the next sweep = %v, want another attempt wrapping %v", err, ErrOrphanedSession)
	}
}

// One session the host cannot answer for must not leave the rest of an expired
// fleet standing. They are unsandboxed shells past their bounds, and the reason
// the reaper runs without a request is that nothing else will come for them.
func TestSweepReportsOneFailureWithoutStoppingAtIt(t *testing.T) {
	t.Parallel()

	f := newManagerFixture(t)
	stuck, _ := mustCreate(t, f, f.request())
	doomed, _ := mustCreate(t, f, f.request())
	f.tmux.SurviveKill(stuck.TmuxName())

	reaped, err := reaperAt(t, f, f.now.Add(AbsoluteLifetime)).Sweep(context.Background())
	if !errors.Is(err, ErrOrphanedSession) {
		t.Errorf("Sweep() error = %v, want one wrapping %v", err, ErrOrphanedSession)
	}
	if len(reaped) != 1 {
		t.Fatalf("Sweep() took %d sessions, want the one it could confirm", len(reaped))
	}
	if reaped[0].Session.ID != doomed.ID {
		t.Errorf("Sweep() reaped %q, want %q", reaped[0].Session.ID, doomed.ID)
	}
	if _, ok := f.store.lookup(stuck.ID); !ok {
		t.Error("Sweep() dropped the record of the session it could not tear down")
	}
}

// spec.md's concurrency edge case: a destroy racing the reaper on the same
// session. Both are the same verified teardown, so whoever loses finds the
// session gone and the record already dropped — and that is success for both.
// A reaper that reported failure here would put an orphan in the trail every
// time an operator destroyed a session at the moment it expired.
func TestDestroyRacingTheReaperReportsSuccessToBoth(t *testing.T) {
	t.Parallel()

	f := newManagerFixture(t)
	s, _ := mustCreate(t, f, f.request())
	r := reaperAt(t, f, f.now.Add(AbsoluteLifetime))

	var (
		wg                   sync.WaitGroup
		destroyErr, sweepErr error
		reaped               []Reaped
	)
	wg.Add(2)
	go func() {
		defer wg.Done()
		destroyErr = f.mgr.Destroy(context.Background(), *s)
	}()
	go func() {
		defer wg.Done()
		reaped, sweepErr = r.Sweep(context.Background())
	}()
	wg.Wait()

	if destroyErr != nil {
		t.Errorf("Destroy() racing the reaper = %v, want success: the session is gone and so is the record", destroyErr)
	}
	if sweepErr != nil {
		t.Errorf("Sweep() racing a destroy = %v, want success: the session is gone and so is the record", sweepErr)
	}
	if len(reaped) > 1 {
		t.Errorf("Sweep() reported %d sessions destroyed, and there was one", len(reaped))
	}
	if n := f.store.Len(); n != 0 {
		t.Errorf("the store holds %d records after a destroy raced the reaper, want 0", n)
	}
	if _, ok := f.tmux.WorkDir(s.TmuxName()); ok {
		t.Error("the tmux session survived a destroy that raced the reaper")
	}
}

// FR-041 at the one teardown with no request behind it: a session nobody came
// back for is destroyed on nobody's say-so, and the record is the entire account
// of it. One per session acted on, under the caller the ownership check would
// have compared against, naming the session by the daemon's own id.
func TestSweepRecordsEverySessionItTakes(t *testing.T) {
	t.Parallel()

	f := newManagerFixture(t)
	first, _ := mustCreate(t, f, f.request())
	second, _ := mustCreate(t, f, f.request())

	r, sink := auditedReaperAt(t, f, f.now.Add(AbsoluteLifetime))
	if n := len(mustSweep(t, r)); n != 2 {
		t.Fatalf("Sweep() took %d sessions, want both", n)
	}

	got := reapRecords(t, sink)
	if len(got) != 2 {
		t.Fatalf("the sweep emitted %d audit records for two sessions (%v); FR-041 requires one each", len(got), got)
	}

	recorded := make(map[string]bool)
	for _, rec := range got {
		if want := string(audit.ActionReaperDestroy); rec["action"] != want {
			t.Errorf("the sweep recorded action %v, want %q", rec["action"], want)
		}
		if want := string(audit.Allow); rec["decision"] != want {
			t.Errorf("a confirmed teardown was recorded as %v, want %q", rec["decision"], want)
		}
		if want := string(auth.CallerOperator); rec["caller"] != want {
			t.Errorf("the sweep recorded caller %v, want the session's own owner %q", rec["caller"], want)
		}
		if _, ok := rec["remote"]; ok {
			t.Errorf("the sweep recorded a peer address (%v); there is no request behind a sweep", rec["remote"])
		}
		id, ok := rec["session_id"].(string)
		if !ok {
			t.Errorf("the sweep recorded session_id %v, which is not the 32-hex id of a session", rec["session_id"])
			continue
		}
		recorded[id] = true
	}
	for _, want := range []string{first.ID, second.ID} {
		if !recorded[want] {
			t.Errorf("the sweep destroyed %q and recorded no session under that id", want)
		}
	}
}

// The trail is the only place the bound a session died of is readable, and an
// operator greps one string to find every session that hit a ceiling. There were
// two bounds to name until milestone 15 and there is one now; the table stays a
// table, because Expiry stays a type and a second bound would be a row.
func TestSweepNamesTheBoundItEnforcedInTheRecord(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		after time.Duration
		want  string
	}{
		{"absolute", AbsoluteLifetime, reasonPastAbsolute},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f := newManagerFixture(t)
			mustCreate(t, f, f.request())

			r, sink := auditedReaperAt(t, f, f.now.Add(tc.after))
			mustSweep(t, r)

			if got := onlyReapRecord(t, sink)["reason"]; got != tc.want {
				t.Errorf("a session past its %s bound was recorded as %v, want %q", tc.name, got, tc.want)
			}
		})
	}
}

// The denial is the loudest thing this daemon has to say. tmux reported the kill
// worked and the session is still there, so what the trail must carry is that the
// bound was reached and the shell is *still running* — recording an allow here
// would tell an operator the host is clean when it is not.
func TestSweepRecordsATeardownItCouldNotConfirmAsARefusal(t *testing.T) {
	t.Parallel()

	f := newManagerFixture(t)
	s, _ := mustCreate(t, f, f.request())
	f.tmux.SurviveKill(s.TmuxName())

	r, sink := auditedReaperAt(t, f, f.now.Add(AbsoluteLifetime))
	if _, err := r.Sweep(context.Background()); !errors.Is(err, ErrOrphanedSession) {
		t.Fatalf("Sweep() error = %v, want one wrapping %v", err, ErrOrphanedSession)
	}

	rec := onlyReapRecord(t, sink)
	if want := string(audit.Deny); rec["decision"] != want {
		t.Errorf("a session the host would not confirm gone was recorded as %v, want %q", rec["decision"], want)
	}
	if rec["session_id"] != s.ID {
		t.Errorf("the refusal names session %v, want the one still running (%q)", rec["session_id"], s.ID)
	}
	reason, ok := rec["reason"].(string)
	if !ok {
		t.Fatalf("the refusal records reason %v, which is not a string", rec["reason"])
	}
	if !strings.HasPrefix(reason, reasonUnconfirmed) {
		t.Errorf("the refusal reads %q, and does not say the teardown was unconfirmed", reason)
	}
	if !strings.Contains(reason, reasonPastAbsolute) {
		t.Errorf("the refusal reads %q, and does not name the bound the session was past", reason)
	}
}

// A sweep that took nothing says nothing. The trail is what an operator greps
// for a session that ended without them; a record per tick per living session
// would bury the ones that mean something.
func TestSweepRecordsNothingForASessionInsideItsBounds(t *testing.T) {
	t.Parallel()

	f := newManagerFixture(t)
	mustCreate(t, f, f.request())

	r, sink := auditedReaperAt(t, f, f.now.Add(AbsoluteLifetime-time.Nanosecond))
	if n := len(mustSweep(t, r)); n != 0 {
		t.Fatalf("Sweep() took %d sessions that are inside their bound", n)
	}

	if got := reapRecords(t, sink); len(got) != 0 {
		t.Errorf("a sweep that destroyed nothing wrote %d audit records: %v", len(got), got)
	}
}

// brokenSink is an audit destination that cannot be written to — a closed pipe,
// a full disk, a journald that went away.
type brokenSink struct{ err error }

func (s brokenSink) Write([]byte) (int, error) { return 0, s.err }

// A record that could not be written is a session torn down with no account of
// it, which is the failure FR-041 exists to prevent. The sweep cannot undo the
// teardown, so what is left is to refuse to be silent: the failure joins the
// sweep's error and reaches Run's loud-failure path, rather than being dropped
// because the teardown itself worked.
func TestSweepReportsAnAuditWriteItCouldNotMake(t *testing.T) {
	t.Parallel()

	f := newManagerFixture(t)
	s, _ := mustCreate(t, f, f.request())

	want := errors.New("the journal went away")
	at := f.now.Add(AbsoluteLifetime)
	r, err := NewReaper(f.managerAt(t, f.store, at), audit.NewTo(brokenSink{err: want}, func() time.Time { return at }))
	if err != nil {
		t.Fatalf("NewReaper() unexpected error: %v", err)
	}

	reaped, err := r.Sweep(context.Background())
	if !errors.Is(err, want) {
		t.Errorf("Sweep() error = %v, want one wrapping the sink's %v", err, want)
	}
	if len(reaped) != 1 {
		t.Errorf("Sweep() reported %d sessions destroyed; the teardown succeeded and the record is what failed", len(reaped))
	}
	if _, ok := f.store.lookup(s.ID); ok {
		t.Error("Sweep() kept the record of a session it did tear down, because it could not audit it")
	}
}

// The loop, driven a tick at a time. Nothing here sleeps: the second send cannot
// complete until Run has finished the sweep the first one started.
func TestRunSweepsOnEveryTickAndStopsWithItsContext(t *testing.T) {
	t.Parallel()

	f := newManagerFixture(t)
	s, _ := mustCreate(t, f, f.request())

	r := reaperAt(t, f, f.now.Add(AbsoluteLifetime))
	tk := newManualTicker()
	stop := tk.run(t, r, context.Background())

	tk.tick()
	tk.tick()
	stop()

	if _, ok := f.store.lookup(s.ID); ok {
		t.Error("Run() left an expired session's record in the store after a tick")
	}
	if _, ok := f.tmux.WorkDir(s.TmuxName()); ok {
		t.Error("Run() left an expired session running on the host after a tick")
	}
	select {
	case <-tk.stopped:
	default:
		t.Error("Run() returned without stopping its ticker")
	}
}

// Sweeping is what a tick is for, and nothing else triggers one. A daemon that
// swept on the way in would be re-doing the work reconciliation just did, and one
// that swept on the way out would be doing T037's job with a cancelled context.
func TestRunSweepsNothingWithoutATick(t *testing.T) {
	t.Parallel()

	f := newManagerFixture(t)
	s, _ := mustCreate(t, f, f.request())
	before := len(f.tmux.Calls())

	// Long past every bound, so a sweep would certainly have taken it.
	r := reaperAt(t, f, f.now.Add(AbsoluteLifetime))
	stop := newManualTicker().run(t, r, context.Background())
	stop()

	if n := len(f.tmux.Calls()) - before; n != 0 {
		t.Errorf("Run() ran %d tmux commands without a tick, want 0", n)
	}
	if _, ok := f.store.lookup(s.ID); !ok {
		t.Error("Run() destroyed a session without a tick")
	}
}

// A sweep that cannot confirm a teardown is the loudest thing this daemon has to
// say, and the loop is the one place it could be silently dropped: Run discards
// the sweep's result, so without this the failure would go nowhere at all.
func TestRunReportsAFailedSweepRatherThanSwallowingIt(t *testing.T) {
	t.Parallel()

	f := newManagerFixture(t)
	s, _ := mustCreate(t, f, f.request())
	f.tmux.SurviveKill(s.TmuxName())

	r := reaperAt(t, f, f.now.Add(AbsoluteLifetime))
	// Non-blocking, so a later sweep cannot wedge the loop against a full
	// channel and hang the test instead of failing it.
	reports := make(chan error, 1)
	r.report = func(err error) {
		select {
		case reports <- err:
		default:
		}
	}

	tk := newManualTicker()
	stop := tk.run(t, r, context.Background())
	tk.tick()

	var reported error
	select {
	case reported = <-reports:
	case <-time.After(5 * time.Second):
		t.Fatal("Run() swept a session it could not tear down and reported nothing")
	}
	stop()

	if !errors.Is(reported, ErrOrphanedSession) {
		t.Errorf("Run() reported %v, want an error wrapping %v", reported, ErrOrphanedSession)
	}
	if _, ok := f.store.lookup(s.ID); !ok {
		t.Error("Run() dropped the record of a session that may still be running")
	}
}

// A reaper without a manager has no store to sweep, no clock to sweep on, and no
// verified teardown to sweep with; one without a sink destroys unsandboxed shells
// with nothing left to say it happened. Both are refused at construction, because
// a daemon that started with either would be a daemon whose sessions have no end
// at all, or no end anyone can account for.
func TestNewReaperFailsClosed(t *testing.T) {
	t.Parallel()

	f := newManagerFixture(t)
	cases := []struct {
		name  string
		mgr   *Manager
		trail *audit.Logger
	}{
		{"nothing to reap", nil, audit.NewTo(io.Discard, nil)},
		{"nowhere to record a reaping", f.mgr, nil},
		{"neither", nil, nil},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			r, err := NewReaper(tc.mgr, tc.trail)
			if err == nil {
				t.Fatal("NewReaper() accepted a reaper with " + tc.name)
			}
			if r != nil {
				t.Error("NewReaper() returned a Reaper alongside an error")
			}
		})
	}
}

// The interval is plan.md's, and the property behind the number is what this
// asserts: a sweep coarser than the bounds it enforces would let a session run
// well past a deadline the documentation states exactly.
func TestTheSweepIsFinerThanTheBoundsItEnforces(t *testing.T) {
	t.Parallel()

	f := newManagerFixture(t)
	r, err := NewReaper(f.mgr, audit.NewTo(io.Discard, nil))
	if err != nil {
		t.Fatalf("NewReaper() unexpected error: %v", err)
	}

	if r.interval != SweepInterval {
		t.Errorf("NewReaper() sweeps every %s, want the documented %s", r.interval, SweepInterval)
	}
	if SweepInterval >= AbsoluteLifetime {
		t.Errorf("a %s sweep cannot resolve a %s ceiling", SweepInterval, AbsoluteLifetime)
	}
	if r.ticker == nil || r.report == nil {
		t.Error("NewReaper() left the reaper without a heartbeat or somewhere to report a failure")
	}
}
