package session

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/nctiggy/claude-remote-session-webhook/internal/audit"
	"github.com/nctiggy/claude-remote-session-webhook/internal/tmuxctl"
)

// The supervisor is the reaper's opposite number and lives beside it rather than
// inside it. They sweep on the same cadence and share a shape, but one ends
// sessions and the other starts them, and a single type that did both would be a
// worse thing to review than two that each do one — Reaper's own vocabulary
// (Reaped, Expiry, "the thing that ends a session nobody came back for") does not
// stretch to cover revival without being rewritten.
//
// It is also the first thing in this daemon that causes execution on the host
// with no request behind it. That is the whole of its risk, and every bound the
// constitution places on a create is re-established here rather than assumed:
// the allowlist is re-resolved, the cap is re-checked, the deadline is carried
// and never refreshed, and a recreated shell is marked owned before anything is
// typed into it.

// maxReviveAttempts is how many times one death may be answered before the
// daemon stops and says so.
//
// It is a constant for the reason SweepInterval is one: an operator who could
// widen it could widen the blast radius the constitution bounds by construction.
//
// The number is not decoration. On 2026-08-17 a unit on this host restarted
// 2,826 times in four hours, every five seconds, against `Workspace not trusted`
// — an error no retry could ever fix. The damage was not that it failed; it was
// that it failed invisibly. StateFailed is the half of this bound that matters.
const maxReviveAttempts = 3

// reviveBackoff is the delay before each attempt, indexed by attempts already
// made. The first is short because the common case is a transient death that
// recovers immediately; the last is long because by then the daemon is probably
// wrong about being able to help.
var reviveBackoff = [maxReviveAttempts]time.Duration{
	5 * time.Second,
	30 * time.Second,
	3 * time.Minute,
}

// Reasons the trail records. They are constants here rather than sentences at
// the call site so that what an operator greps for is spelled once.
const (
	reasonClaudeStopped   = "the session's start command is no longer running in its pane"
	reasonShellGone       = "the session's shell no longer exists on the host"
	reasonRecovered       = "the session is running its start command again"
	reasonAttemptsSpent   = "revival was attempted the maximum number of times and stopped"
	reasonWorkDirRefused  = "the session's working directory is no longer inside the allowlist"
	reasonNoTranscript    = "the session has no conversation on this host to resume"
	reasonNoConversation  = "the session was created before conversations were recorded"
	reasonFleetAtCapacity = "the concurrent-session cap leaves no room to recreate this session"
	reasonReplayExpired   = "the session was already past its absolute deadline when the daemon came back"
)

// Supervisor is FR-006 and FR-009: the thing that notices a session has stopped
// and brings it back.
type Supervisor struct {
	mgr   *Manager
	trail *audit.Logger

	interval time.Duration
	ticker   ticker
	report   func(error)

	// inflight is FR-015. A revival that outlives its sweep must not have a
	// second one started on top of it, because two start commands typed into one
	// shell is two Claude processes in one session.
	mu       sync.Mutex
	inflight map[string]bool
}

// NewSupervisor builds the sweep. It mirrors NewReaper deliberately: the same
// refusals, the same injectable ticker, the same report seam.
func NewSupervisor(m *Manager, trail *audit.Logger) (*Supervisor, error) {
	switch {
	case m == nil:
		return nil, errors.New("session: no session manager provided for the supervisor; refusing to start")
	case trail == nil:
		return nil, errors.New("session: no audit sink provided for the supervisor; refusing to start")
	}
	return &Supervisor{
		mgr:      m,
		trail:    trail,
		interval: SweepInterval,
		ticker:   systemTicker,
		report:   reportToLog,
		inflight: make(map[string]bool),
	}, nil
}

// Run sweeps on every tick until ctx is done. It is the goroutine the daemon
// starts once and never restarts.
func (s *Supervisor) Run(ctx context.Context) {
	ticks, stop := s.ticker(s.interval)
	defer stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticks:
			if err := s.Sweep(ctx); err != nil {
				s.report(err)
			}
		}
	}
}

// Sweep asks the host one question and judges every session from the answer.
//
// One List call, regardless of fleet size: Adopt already takes everything it
// needs from one, and a sweep whose cost grew with the fleet is a sweep an
// operator learns to fear.
func (s *Supervisor) Sweep(ctx context.Context) error {
	infos, err := s.mgr.tmux.List(ctx)
	if err != nil {
		return fmt.Errorf("supervise the fleet: %w", err)
	}
	onHost := make(map[string]tmuxctl.SessionInfo, len(infos))
	for _, info := range infos {
		onHost[info.Name] = info
	}

	now := s.mgr.clock.Now()
	var failures []error

	for _, sess := range s.mgr.store.snapshot() {
		info, present := onHost[sess.TmuxName()]
		if err := s.judge(ctx, sess, info, present, now); err != nil {
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}

// judge is the ordered decision table from data-model.md. The first match wins,
// and the order is the contract: terminal states are checked before anything
// else so that nothing below can resurrect one.
func (s *Supervisor) judge(ctx context.Context, sess Session, info tmuxctl.SessionInfo, present bool, now time.Time) error {
	// 1 & 2 — terminal, or the reaper's to collect. Nothing here may touch a
	// session the operator ended, one the reaper took, or one already past its
	// ceiling: revival must never be a way to outlive a deadline.
	if sess.State == StateDead || sess.State == StateFailed {
		return nil
	}
	if !sess.AbsoluteDeadline().IsZero() && !now.Before(sess.AbsoluteDeadline()) {
		return nil
	}

	// 3 — healthy. LivenessUnknown lands here too, and must: a session whose
	// liveness this daemon could not establish is left alone, because restarting
	// a healthy session is worse than missing a dead one by a sweep.
	if present && info.Claude != tmuxctl.LivenessStopped {
		if sess.ReviveAttempts > 0 {
			return s.recovered(sess)
		}
		// A session the journal put back, or one whose start was never
		// confirmed, is running after all. Promoting it here is what keeps a
		// replayed record from claiming "starting" for the rest of its life.
		if sess.State == StateStarting {
			if err := s.mgr.store.SetState(sess.ID, StateRunning); err != nil {
				return fmt.Errorf("confirm session %s is running: %w", sess.ID, err)
			}
		}
		return nil
	}

	// 4 — backing off.
	if now.Before(sess.NextReviveAt) {
		return nil
	}

	// 8 — already being revived. Checked before the bound is spent so a slow
	// revival does not burn an attempt on every tick while it runs.
	if !s.claim(sess.ID) {
		return nil
	}
	defer s.release(sess.ID)

	// 5 — out of attempts.
	if sess.ReviveAttempts >= maxReviveAttempts {
		return s.giveUp(sess, reasonAttemptsSpent)
	}
	// 6 — the allowlist may have shrunk since this session was created, and a
	// session it no longer covers is one this daemon may not start a shell in.
	if _, err := ResolveWorkDir(sess.WorkDir, s.mgr.roots); err != nil {
		return s.giveUp(sess, reasonWorkDirRefused)
	}
	// 7 — nothing to resume. Resuming an identifier with no transcript behind it
	// does not fail, it starts something that is not the session the operator had.
	if sess.ConversationID == "" {
		return s.giveUp(sess, reasonNoConversation)
	}
	if !s.mgr.HasTranscript(sess.ConversationID, sess.WorkDir) {
		return s.giveUp(sess, reasonNoTranscript)
	}
	// The cap covers a recreate, which adds a shell to the host. A revive in
	// place adds none — the shell is already there — so it is not asked.
	if !present && s.mgr.store.Len() > s.mgr.maxSessions && s.mgr.maxSessions > 0 {
		return s.giveUp(sess, reasonFleetAtCapacity)
	}

	// The bound is written *before* the attempt, and the order is the whole
	// defence: a daemon that died mid-revival and had recorded nothing would come
	// back and retry instantly, forever, which is the failure this bound exists
	// to prevent.
	attempt := sess.ReviveAttempts + 1
	if err := s.mgr.store.SetRevive(sess.ID, attempt, now.Add(reviveBackoff[min(attempt, maxReviveAttempts)-1])); err != nil {
		return fmt.Errorf("record a revival attempt for session %s: %w", sess.ID, err)
	}
	sess.ReviveAttempts = attempt
	if err := s.mgr.journal.Append(reviveRecord(sess, journalRevived)); err != nil {
		return fmt.Errorf("journal a revival attempt for session %s: %w", sess.ID, err)
	}

	action := audit.ActionSupervisorRevive
	reason := reasonClaudeStopped
	if !present {
		action, reason = audit.ActionSupervisorRecreate, reasonShellGone
	}
	if err := s.trail.Emit(audit.Record{
		Action:    action,
		Caller:    string(sess.Owner),
		SessionID: sess.ID,
		Decision:  audit.Allow,
		Reason:    fmt.Sprintf("%s (attempt %d of %d)", reason, attempt, maxReviveAttempts),
	}); err != nil {
		s.report(fmt.Errorf("record a revival in the trail: %w", err))
	}

	if err := s.mgr.revive(ctx, sess, present); err != nil {
		return fmt.Errorf("revive session %s: %w", sess.ID, err)
	}
	return nil
}

// recovered clears the bound on a session that came back (FR-020).
func (s *Supervisor) recovered(sess Session) error {
	if err := s.mgr.store.SetRevive(sess.ID, 0, time.Time{}); err != nil {
		return fmt.Errorf("clear the revival state of session %s: %w", sess.ID, err)
	}
	if err := s.trail.Emit(audit.Record{
		Action:    audit.ActionSupervisorRecovered,
		Caller:    string(sess.Owner),
		SessionID: sess.ID,
		Decision:  audit.Allow,
		Reason:    reasonRecovered,
	}); err != nil {
		s.report(fmt.Errorf("record a recovery in the trail: %w", err))
	}
	return nil
}

// giveUp is the half of the bound an operator sees.
func (s *Supervisor) giveUp(sess Session, reason string) error {
	if err := s.mgr.store.SetState(sess.ID, StateFailed); err != nil {
		return fmt.Errorf("mark session %s failed: %w", sess.ID, err)
	}
	sess.State = StateFailed
	if err := s.mgr.journal.Append(reviveRecord(sess, journalFailed)); err != nil {
		return fmt.Errorf("journal a failed session %s: %w", sess.ID, err)
	}
	if err := s.trail.Emit(audit.Record{
		Action:    audit.ActionSupervisorFailed,
		Caller:    string(sess.Owner),
		SessionID: sess.ID,
		Decision:  audit.Deny,
		Reason:    reason,
	}); err != nil {
		s.report(fmt.Errorf("record a give-up in the trail: %w", err))
	}
	// The card an open dashboard is drawing now says the wrong thing, which is a
	// fleet change by the only definition that matters.
	s.mgr.emit(FleetChanged, sess)
	return nil
}

func (s *Supervisor) claim(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.inflight[id] {
		return false
	}
	s.inflight[id] = true
	return true
}

func (s *Supervisor) release(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.inflight, id)
}

// reviveRecord is the journal line for a session whose revival state moved.
func reviveRecord(s Session, event string) journalRecord {
	return journalRecord{
		At:           time.Now().UTC(),
		ID:           s.ID,
		Event:        event,
		Owner:        string(s.Owner),
		Conversation: s.ConversationID,
		WorkDir:      s.WorkDir,
		Start:        s.StartCommand,
		Lifetime:     encodeLifetime(s.Lifetime),
		Created:      s.CreatedAt,
		Attempts:     s.ReviveAttempts,
	}
}

// revive brings one session back, either into the shell it still has or into a
// new one built for it.
//
// It is a Manager method because the Controller is the Manager's: nothing outside
// this package holds one, and inside it the rule that everything standing in for
// the permission prompt goes through one type is worth keeping.
func (m *Manager) revive(ctx context.Context, s Session, shellSurvives bool) error {
	name := s.TmuxName()

	if !shellSurvives {
		// A new shell for a session that already has an identity. The options go
		// on *before* the command is sent, exactly as Create writes them before
		// it sends: a failure part-way must leave something Adopt recognises
		// rather than a live unsandboxed shell with no owner (FR-015b).
		if err := m.tmux.New(ctx, name, s.WorkDir); err != nil {
			return fmt.Errorf("recreate the shell: %w", err)
		}
		if err := m.markSession(ctx, s); err != nil {
			return err
		}
	}

	template, err := m.resolveStartCommand(s.StartCommand)
	if err != nil {
		return fmt.Errorf("resolve the start command: %w", err)
	}
	// Validated again here rather than trusted from the record. The value is
	// about to be typed into a live shell, and ValidateResume is the control that
	// makes that safe — a second check costs nothing and removes the question of
	// whether the first one still held.
	resume, err := ValidateResume(s.ConversationID)
	if err != nil {
		return fmt.Errorf("check the conversation to resume: %w", err)
	}
	// The conversation travels as --resume, never as --session-id: this session
	// already has a conversation and is rejoining it, which is a different thing
	// from being given one.
	command, err := m.renderStart(template, resume, "", s.Name)
	if err != nil {
		return fmt.Errorf("render the start command: %w", err)
	}
	if err := m.tmux.SendKeys(ctx, name, command, enterKey); err != nil {
		return fmt.Errorf("send the claude start command: %w", err)
	}
	return nil
}

// markSession writes every @crswd-* option a session carries. Create writes them
// inline; a recreated session needs the same set written again, and having one
// place that knows what "the full set" is means a seventh option cannot be added
// to one path and forgotten on the other.
func (m *Manager) markSession(ctx context.Context, s Session) error {
	template, err := m.resolveStartCommand(s.StartCommand)
	if err != nil {
		return fmt.Errorf("resolve the start command: %w", err)
	}
	options := []struct{ option, value string }{
		{tmuxctl.OptionManaged, tmuxctl.OptionManagedValue},
		{tmuxctl.OptionOwner, string(s.Owner)},
		{tmuxctl.OptionName, s.Name},
		{tmuxctl.OptionWorkDir, base64.StdEncoding.EncodeToString([]byte(s.WorkDir))},
		{tmuxctl.OptionStart, s.StartCommand},
		{tmuxctl.OptionLifetime, encodeLifetime(s.Lifetime)},
		{tmuxctl.OptionConversation, s.ConversationID},
		{tmuxctl.OptionBinary, startBinary(template)},
	}
	for _, o := range options {
		if err := m.tmux.SetOption(ctx, s.TmuxName(), o.option, o.value); err != nil {
			return fmt.Errorf("record the session %s option: %w", o.option, err)
		}
	}
	return nil
}
