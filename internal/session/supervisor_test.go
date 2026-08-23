// The supervisor is the first thing in this daemon that causes execution on the
// host with no request behind it, so the negative cases here matter more than
// the positive ones: what it must never start is a longer list than what it must.
package session

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nctiggy/claude-remote-session-webhook/internal/audit"
	"github.com/nctiggy/claude-remote-session-webhook/internal/tmuxctl"
)

// supervisorAt builds a supervisor whose trail is readable.
func supervisorAt(t *testing.T, f managerFixture, at time.Time) (*Supervisor, *bytes.Buffer) {
	t.Helper()

	sink := &bytes.Buffer{}
	s, err := NewSupervisor(f.managerAt(t, f.store, at), audit.NewTo(sink, func() time.Time { return at }))
	if err != nil {
		t.Fatalf("NewSupervisor() unexpected error: %v", err)
	}
	s.mgr.SetJournal(tempJournal(t))
	return s, sink
}

// liveSession creates a session and leaves the fake host reporting it healthy.
func liveSession(t *testing.T, f managerFixture) Session {
	t.Helper()

	s, _ := mustCreate(t, f, f.request())
	return *s
}

// revivableSession is liveSession with a transcript on disk, which is what the
// supervisor requires before it will resume anything (FR-014): an identifier
// with nothing behind it does not fail on resume, it starts something that is
// not the session the operator had.
//
// It sets HOME, which os.UserHomeDir reads, so a test using it cannot be
// parallel — the same trade conversationHome makes, and for the same reason.
func revivableSession(t *testing.T, f managerFixture) Session {
	t.Helper()

	home := t.TempDir()
	t.Setenv("HOME", home)

	s, _ := mustCreate(t, f, f.request())
	project := filepath.Join(home, ".claude", "projects", projectDirFor(s.WorkDir))
	if err := os.MkdirAll(project, 0o700); err != nil {
		t.Fatalf("plant a project directory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(project, s.ConversationID+conversationFileSuffix), []byte("{}\n"), 0o600); err != nil {
		t.Fatalf("plant a transcript: %v", err)
	}
	return *s
}

// claudeDied is the ordinary failure: the start command is gone and the login
// shell it was typed into is still sitting there.
func claudeDied(f managerFixture, s Session) {
	f.tmux.SetPaneCommand(s.TmuxName(), "bash")
}

// typedInto returns every line the host was asked to type into a session.
func typedInto(f managerFixture, s Session) []string {
	var out []string
	for _, call := range f.tmux.Calls() {
		if call.Op != tmuxctl.OpSendKeys {
			continue
		}
		for _, arg := range call.Argv {
			if strings.Contains(arg, "claude") {
				out = append(out, arg)
			}
		}
	}
	return out
}

func trailActions(t *testing.T, sink *bytes.Buffer) []string {
	t.Helper()

	var out []string
	for _, line := range strings.Split(strings.TrimSpace(sink.String()), "\n") {
		if line == "" {
			continue
		}
		for _, rec := range reapRecords(t, bytes.NewBufferString(line)) {
			if a, ok := rec["action"].(string); ok {
				out = append(out, a)
			}
		}
	}
	return out
}

// ---------------------------------------------------------------- US1: revival

func TestSupervisorRevivesInPlaceWhenClaudeDies(t *testing.T) {
	f := newManagerFixture(t)
	s := revivableSession(t, f)
	claudeDied(f, s)

	sup, sink := supervisorAt(t, f, f.now)
	if err := sup.Sweep(context.Background()); err != nil {
		t.Fatalf("Sweep() = %v", err)
	}

	typed := typedInto(f, s)
	if len(typed) < 2 {
		t.Fatalf("the host was asked to type %d commands, want the create's and a revival's: %q", len(typed), typed)
	}
	revival := typed[len(typed)-1]
	if !strings.Contains(revival, ResumeOneFlag+" "+s.ConversationID) {
		t.Errorf("the revival typed %q; want it to resume %s", revival, s.ConversationID)
	}
	if strings.Contains(revival, SessionIDFlag) {
		t.Errorf("the revival typed %q; a session rejoining its conversation is resumed, never given a new one", revival)
	}
	if got := trailActions(t, sink); len(got) == 0 || got[0] != string(audit.ActionSupervisorRevive) {
		t.Errorf("trail = %v, want it to open with %s", got, audit.ActionSupervisorRevive)
	}
}

func TestSupervisorRecreatesAVanishedShell(t *testing.T) {
	f := newManagerFixture(t)
	s := revivableSession(t, f)
	// The observed failure: the OOM killer took the whole tmux-spawn cgroup, so
	// the session is not merely idle, it is gone.
	f.tmux.Vanish(s.TmuxName())

	sup, sink := supervisorAt(t, f, f.now)
	if err := sup.Sweep(context.Background()); err != nil {
		t.Fatalf("Sweep() = %v", err)
	}

	var built bool
	var options []string
	for _, call := range f.tmux.Calls() {
		switch call.Op {
		case tmuxctl.OpNew:
			built = true
		case tmuxctl.OpSetOption:
			if built && len(call.Argv) >= 5 {
				options = append(options, call.Argv[4])
			}
		}
	}
	if !built {
		t.Fatal("a session whose shell had vanished was not given a new one")
	}
	// Marked owned before anything is typed, or a failure part-way leaves a live
	// unsandboxed shell with no owner (FR-015b).
	for _, want := range []string{tmuxctl.OptionManaged, tmuxctl.OptionOwner, tmuxctl.OptionConversation, tmuxctl.OptionBinary} {
		if !containsString(options, want) {
			t.Errorf("the recreated shell was not given %s; options written were %v", want, options)
		}
	}
	if got := trailActions(t, sink); len(got) == 0 || got[0] != string(audit.ActionSupervisorRecreate) {
		t.Errorf("trail = %v, want it to open with %s", got, audit.ActionSupervisorRecreate)
	}
}

// TestSupervisorLeavesAHealthySessionCompletelyAlone is the case that runs every
// thirty seconds forever, so it must cost nothing and write nothing.
func TestSupervisorLeavesAHealthySessionAlone(t *testing.T) {
	t.Parallel()

	f := newManagerFixture(t)
	liveSession(t, f)
	before := len(f.tmux.Calls())

	sup, sink := supervisorAt(t, f, f.now)
	if err := sup.Sweep(context.Background()); err != nil {
		t.Fatalf("Sweep() = %v", err)
	}

	after := f.tmux.Calls()
	for _, call := range after[before:] {
		if call.Op != tmuxctl.OpList {
			t.Errorf("a sweep over a healthy fleet ran %s; it may only ask one question", call.Op)
		}
	}
	if sink.Len() != 0 {
		t.Errorf("a sweep over a healthy fleet wrote to the trail: %s", sink)
	}
}

// TestSupervisorTreatsUnknownLivenessAsAlive is the fail-safe. A session started
// before spec 012 carries no expectation to compare the pane against, and
// restarting a healthy session is worse than missing a dead one by a sweep.
func TestSupervisorTreatsUnknownLivenessAsAlive(t *testing.T) {
	t.Parallel()

	f := newManagerFixture(t)
	s := liveSession(t, f)
	// No @crswd-binary: exactly what a session from an older build looks like.
	f.tmux.Seed(tmuxctl.SessionInfo{Name: s.TmuxName(), Created: f.now, Managed: true, Claude: tmuxctl.LivenessUnknown})

	sup, sink := supervisorAt(t, f, f.now)
	before := len(typedInto(f, s))
	if err := sup.Sweep(context.Background()); err != nil {
		t.Fatalf("Sweep() = %v", err)
	}
	if got := len(typedInto(f, s)); got != before {
		t.Errorf("a session of unknown liveness was restarted (%d lines typed, was %d)", got, before)
	}
	if sink.Len() != 0 {
		t.Errorf("a session of unknown liveness reached the trail: %s", sink)
	}
}

// TestRevivalKeepsEveryInvariant is FR-010: revival is not a second create and
// must change nothing about who owns a session or how long it may live.
func TestRevivalKeepsEveryInvariant(t *testing.T) {
	f := newManagerFixture(t)
	before := revivableSession(t, f)
	claudeDied(f, before)

	sup, _ := supervisorAt(t, f, f.now)
	if err := sup.Sweep(context.Background()); err != nil {
		t.Fatalf("Sweep() = %v", err)
	}

	after, ok := f.store.lookup(before.ID)
	if !ok {
		t.Fatal("the session vanished from the store during a revival")
	}
	switch {
	case !after.CreatedAt.Equal(before.CreatedAt):
		t.Errorf("CreatedAt moved from %v to %v; revival must never extend a lifetime", before.CreatedAt, after.CreatedAt)
	case !after.AbsoluteDeadline().Equal(before.AbsoluteDeadline()):
		t.Errorf("the absolute deadline moved from %v to %v", before.AbsoluteDeadline(), after.AbsoluteDeadline())
	case after.Owner != before.Owner:
		t.Errorf("Owner changed from %q to %q", before.Owner, after.Owner)
	case after.TokenHash != before.TokenHash:
		t.Error("revival minted a new credential; the record never went away, so its ownership did not change")
	case !after.LastActivity.Equal(before.LastActivity):
		t.Error("revival moved LastActivity; reviving is not driving, and the operator did not drive it")
	case after.ConversationID != before.ConversationID:
		t.Errorf("the conversation changed from %q to %q", before.ConversationID, after.ConversationID)
	}
}

// -------------------------------------------------- US2: what it must not start

func TestSupervisorNeverRevivesWhatTheOperatorEnded(t *testing.T) {
	t.Parallel()

	f := newManagerFixture(t)
	s := liveSession(t, f)
	if err := f.mgr.Destroy(context.Background(), s); err != nil {
		t.Fatalf("Destroy() = %v", err)
	}
	before := len(f.tmux.Calls())

	sup, sink := supervisorAt(t, f, f.now)
	for range 10 {
		if err := sup.Sweep(context.Background()); err != nil {
			t.Fatalf("Sweep() = %v", err)
		}
	}

	for _, call := range f.tmux.Calls()[before:] {
		if call.Op == tmuxctl.OpNew || call.Op == tmuxctl.OpSendKeys {
			t.Errorf("a destroyed session was %s after ten sweeps; destroy is final", call.Op)
		}
	}
	if _, ok := f.store.lookup(s.ID); ok {
		t.Error("a destroyed session reappeared in the store")
	}
	if sink.Len() != 0 {
		t.Errorf("a destroyed session reached the supervisor's trail: %s", sink)
	}
}

func TestSupervisorNeverRevivesAnExpiredSession(t *testing.T) {
	t.Parallel()

	f := newManagerFixture(t)
	s := liveSession(t, f)
	claudeDied(f, s)

	// Past its ceiling. The reaper owns this session; the supervisor must not
	// race it, or revival becomes a way to outlive a deadline.
	sup, sink := supervisorAt(t, f, s.AbsoluteDeadline().Add(time.Second))
	before := len(typedInto(f, s))
	if err := sup.Sweep(context.Background()); err != nil {
		t.Fatalf("Sweep() = %v", err)
	}
	if got := len(typedInto(f, s)); got != before {
		t.Errorf("an expired session was revived (%d lines typed, was %d)", got, before)
	}
	if sink.Len() != 0 {
		t.Errorf("an expired session reached the supervisor's trail: %s", sink)
	}
}

func TestSupervisorGivesUpOnADeAllowlistedDirectory(t *testing.T) {
	t.Parallel()

	f := newManagerFixture(t)
	s := liveSession(t, f)
	claudeDied(f, s)

	sup, sink := supervisorAt(t, f, f.now)
	// The allowlist shrank while the daemon was down. A session it no longer
	// covers is one this daemon may not start a shell in.
	sup.mgr.roots = nil

	before := len(typedInto(f, s))
	if err := sup.Sweep(context.Background()); err != nil {
		t.Fatalf("Sweep() = %v", err)
	}
	if got := len(typedInto(f, s)); got != before {
		t.Errorf("a session outside the allowlist was revived (%d lines typed, was %d)", got, before)
	}
	got, ok := f.store.lookup(s.ID)
	if !ok || got.State != StateFailed {
		t.Errorf("state = %q, want %q so the operator can see it", got.State, StateFailed)
	}
	if acts := trailActions(t, sink); !containsString(acts, string(audit.ActionSupervisorFailed)) {
		t.Errorf("trail = %v, want %s", acts, audit.ActionSupervisorFailed)
	}
}

func TestSupervisorGivesUpWithNoConversationToResume(t *testing.T) {
	t.Parallel()

	f := newManagerFixture(t)
	s := liveSession(t, f)
	claudeDied(f, s)
	// A session from before spec 012: nothing to resume, so reviving it would
	// start something that is not the session the operator had.
	if err := f.store.SetConversation(s.ID, ""); err != nil {
		t.Fatalf("SetConversation() = %v", err)
	}

	sup, sink := supervisorAt(t, f, f.now)
	if err := sup.Sweep(context.Background()); err != nil {
		t.Fatalf("Sweep() = %v", err)
	}
	got, ok := f.store.lookup(s.ID)
	if !ok || got.State != StateFailed {
		t.Errorf("state = %q, want %q", got.State, StateFailed)
	}
	if acts := trailActions(t, sink); !containsString(acts, string(audit.ActionSupervisorFailed)) {
		t.Errorf("trail = %v, want %s", acts, audit.ActionSupervisorFailed)
	}
}

// TestOneRestartAtATime is FR-015, and since spec 013 it covers both things that
// restart a session: a sweep reviving one, and an operator continuing a different
// conversation. Two start commands typed into one shell is two Claude processes
// in one session.
func TestOneRestartAtATime(t *testing.T) {
	f := newManagerFixture(t)
	s := revivableSession(t, f)
	claudeDied(f, s)

	sup, _ := supervisorAt(t, f, f.now)
	if !sup.mgr.claimRestart(s.ID) {
		t.Fatal("the first claim on an idle session was refused")
	}
	before := len(typedInto(f, s))
	if err := sup.Sweep(context.Background()); err != nil {
		t.Fatalf("Sweep() = %v", err)
	}
	if got := len(typedInto(f, s)); got != before {
		t.Errorf("a sweep revived a session already being revived (%d lines typed, was %d)", got, before)
	}
	sup.mgr.releaseRestart(s.ID)
	if !sup.mgr.claimRestart(s.ID) {
		t.Error("the session was still claimed after release")
	}
}

// ------------------------------------------------------------- US3: the bound

// TestRevivalStopsAtTheBound is the 2,826-restarts defence. A unit on this host
// restarted that many times in four hours against an error no retry could fix,
// and the damage was that it failed invisibly.
func TestRevivalStopsAtTheBound(t *testing.T) {
	f := newManagerFixture(t)
	s := revivableSession(t, f)
	claudeDied(f, s)

	at := f.now
	var attempts int
	// Well past the bound, each sweep after the backoff it asked for.
	for range maxReviveAttempts + 5 {
		sup, sink := supervisorAt(t, f, at)
		if err := sup.Sweep(context.Background()); err != nil {
			t.Fatalf("Sweep() = %v", err)
		}
		for _, a := range trailActions(t, sink) {
			if a == string(audit.ActionSupervisorRevive) {
				attempts++
			}
		}
		at = at.Add(time.Hour)
	}

	if attempts != maxReviveAttempts {
		t.Errorf("the supervisor attempted %d revivals, want exactly %d", attempts, maxReviveAttempts)
	}
	got, ok := f.store.lookup(s.ID)
	if !ok || got.State != StateFailed {
		t.Errorf("state = %q, want %q — a session nobody could save must be visible, not silently retried", got.State, StateFailed)
	}
}

// TestBackoffGrows is FR-017.
func TestBackoffGrows(t *testing.T) {
	t.Parallel()

	for i := 1; i < len(reviveBackoff); i++ {
		if reviveBackoff[i] <= reviveBackoff[i-1] {
			t.Errorf("attempt %d waits %v, which is not longer than attempt %d's %v", i+1, reviveBackoff[i], i, reviveBackoff[i-1])
		}
	}
}

// TestBackoffIsRespected: a second sweep inside the delay must not spend an
// attempt.
func TestBackoffIsRespected(t *testing.T) {
	f := newManagerFixture(t)
	s := revivableSession(t, f)
	claudeDied(f, s)

	sup, _ := supervisorAt(t, f, f.now)
	if err := sup.Sweep(context.Background()); err != nil {
		t.Fatalf("Sweep() = %v", err)
	}
	first, _ := f.store.lookup(s.ID)

	// Immediately again, well inside the first backoff.
	sup2, sink := supervisorAt(t, f, f.now)
	if err := sup2.Sweep(context.Background()); err != nil {
		t.Fatalf("Sweep() = %v", err)
	}
	second, _ := f.store.lookup(s.ID)
	if second.ReviveAttempts != first.ReviveAttempts {
		t.Errorf("attempts went %d → %d inside the backoff window", first.ReviveAttempts, second.ReviveAttempts)
	}
	if acts := trailActions(t, sink); containsString(acts, string(audit.ActionSupervisorRevive)) {
		t.Errorf("a sweep inside the backoff window attempted a revival: %v", acts)
	}
}

// TestSuccessResetsTheBound is FR-020: a session that came back is not one
// attempt closer to being given up on.
func TestSuccessResetsTheBound(t *testing.T) {
	f := newManagerFixture(t)
	s := revivableSession(t, f)
	claudeDied(f, s)

	sup, _ := supervisorAt(t, f, f.now)
	if err := sup.Sweep(context.Background()); err != nil {
		t.Fatalf("Sweep() = %v", err)
	}
	if got, _ := f.store.lookup(s.ID); got.ReviveAttempts == 0 {
		t.Fatal("the first attempt was not recorded")
	}

	// It came back.
	f.tmux.SetPaneCommand(s.TmuxName(), "claude")
	sup2, sink := supervisorAt(t, f, f.now.Add(time.Hour))
	if err := sup2.Sweep(context.Background()); err != nil {
		t.Fatalf("Sweep() = %v", err)
	}
	got, _ := f.store.lookup(s.ID)
	if got.ReviveAttempts != 0 {
		t.Errorf("ReviveAttempts = %d after a recovery, want 0", got.ReviveAttempts)
	}
	if acts := trailActions(t, sink); !containsString(acts, string(audit.ActionSupervisorRecovered)) {
		t.Errorf("trail = %v, want %s", acts, audit.ActionSupervisorRecovered)
	}
}

// TestFailedIsTerminal: nothing moves a session out of failed except destroying
// it. A bound the supervisor could undo is not a bound.
func TestFailedIsTerminal(t *testing.T) {
	t.Parallel()

	f := newManagerFixture(t)
	s := liveSession(t, f)
	if err := f.store.SetState(s.ID, StateFailed); err != nil {
		t.Fatalf("SetState() = %v", err)
	}
	if err := f.store.SetState(s.ID, StateRunning); err == nil {
		t.Error("a failed session was moved back to running")
	}
	if err := f.store.SetState(s.ID, StateDead); err != nil {
		t.Errorf("a failed session could not be destroyed: %v", err)
	}
}

// TestSupervisorTrailCarriesNoSecret is FR-022.
func TestSupervisorTrailCarriesNoSecret(t *testing.T) {
	f := newManagerFixture(t)
	s := revivableSession(t, f)
	claudeDied(f, s)

	sup, sink := supervisorAt(t, f, f.now)
	if err := sup.Sweep(context.Background()); err != nil {
		t.Fatalf("Sweep() = %v", err)
	}
	written := sink.String()
	for _, forbidden := range []string{s.WorkDir, s.ConversationID, "token", "secret"} {
		if forbidden != "" && strings.Contains(written, forbidden) {
			t.Errorf("the trail carries %q:\n%s", forbidden, written)
		}
	}
}

func TestNewSupervisorRefusesAMissingCollaborator(t *testing.T) {
	t.Parallel()

	f := newManagerFixture(t)
	if _, err := NewSupervisor(nil, audit.NewTo(&bytes.Buffer{}, nil)); err == nil {
		t.Error("NewSupervisor() built a supervisor with no manager")
	}
	if _, err := NewSupervisor(f.mgr, nil); err == nil {
		t.Error("NewSupervisor() built a supervisor with nowhere to record what it did")
	}
}

func containsString(haystack []string, needle string) bool {
	for _, h := range haystack {
		if h == needle {
			return true
		}
	}
	return false
}
