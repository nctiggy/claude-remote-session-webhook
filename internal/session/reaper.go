package session

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"
)

// SweepInterval is how often Run asks the store whether anything has expired.
//
// plan.md fixes it at 30 seconds, and the only thing that matters about the
// number is the property behind it: the sweep's resolution must be finer than
// the timeouts it enforces, or the bounds an operator reads in
// docs/auth-and-sessions.md are not the bounds the host applies. The deadlines
// themselves stay exact — they are derived from the record, never from whenever
// a sweep happened to run — so the interval only decides how long a session
// outlives the instant it was already over.
//
// It is a constant rather than configuration for the reason the two lifetimes
// are: an operator who could widen it could widen the blast radius the
// constitution bounds by construction (Principle VI).
const SweepInterval = 30 * time.Second

// Expiry is which bound a session was past when the reaper took it.
//
// The distinction is not the caller's — nobody is holding a request open for a
// reaped session — it is the operator's. "Idle for an hour" and "ran for a day"
// are different facts about how the host is being used, and the trail is where
// they are read (T038 emits reaper.destroy).
type Expiry string

const (
	// ExpiryIdle is IdleTimeout since the last request touched the record
	// (FR-038). It is the bound a session that was abandoned rather than
	// destroyed dies of.
	ExpiryIdle Expiry = "idle"

	// ExpiryAbsolute is AbsoluteLifetime since the session began, and it is the
	// one that cannot be renewed: no amount of use moves it, which is exactly
	// what makes it a ceiling rather than a longer idle timeout.
	ExpiryAbsolute Expiry = "absolute"
)

// Reaped is one session a sweep destroyed, together with the bound it was past.
//
// The record is the copy the sweep acted on, not a fresh read: the store no
// longer has one, because a verified teardown drops it (FR-020). It carries the
// token hash, as every Session does — what may leave the daemon is settled at
// the HTTP boundary, and a reaped session leaves through the trail, which
// carries an id and an owner and nothing else (FR-042).
type Reaped struct {
	Session Session
	Expiry  Expiry
}

// ticker is the sweep's heartbeat: the channel that fires every interval, and
// the stop that releases it.
//
// It is a field on Reaper rather than a time.Ticker built inside Run so that a
// test can hand the loop its own ticks. FR-039 makes the reaper's notion of time
// injectable, and a clock alone does not satisfy it here — a loop that built its
// own ticker could only be tested by waiting 30 real seconds for one, which is
// the sort of test that gets a shorter interval hard-coded into production to
// make it bearable.
type ticker func(time.Duration) (<-chan time.Time, func())

func systemTicker(d time.Duration) (<-chan time.Time, func()) {
	t := time.NewTicker(d)
	return t.C, t.Stop
}

// Reaper is FR-038: the thing that ends a session nobody came back for.
//
// It exists because the alternative is enforcing the two lifetimes on the next
// request, and the session that most needs to die is the one no request is
// coming for. Principle VI names an idle timeout and an absolute lifetime
// "enforced by a reaper, not by the next request" for that reason, and it is the
// difference between a bound and a hope.
//
// Everything it decides from belongs to the Manager it was built on — the same
// clock, the same store, the same verified teardown. That is deliberate: a
// reaper with a clock of its own could hold a session past a deadline the
// credential check had already enforced, or destroy one the daemon still
// considered live.
type Reaper struct {
	mgr      *Manager
	interval time.Duration

	// ticker and report are the two seams. Both are fields rather than calls
	// into the package they come from, so that Run's loop and its loud-failure
	// path are reachable from a test without elapsed time and without stderr.
	ticker ticker
	report func(error)
}

// NewReaper builds the reaper for a manager, on the documented interval.
//
// A nil manager is refused rather than tolerated: a Reaper without one has no
// store to sweep, no clock to sweep on, and no verified teardown to sweep with,
// and a daemon that started with one would be a daemon whose sessions have no
// end at all. That is the failure Principle VI ranks above not starting.
func NewReaper(m *Manager) (*Reaper, error) {
	if m == nil {
		return nil, errors.New("session: no session manager provided for the reaper; refusing to start")
	}
	return &Reaper{mgr: m, interval: SweepInterval, ticker: systemTicker, report: reportToLog}, nil
}

// Run sweeps on every tick until ctx is done. It is the goroutine the daemon
// starts once and never restarts.
//
// A failed sweep is reported and the loop continues. Stopping would be the worst
// available answer: the sessions a sweep could not tear down are precisely the
// ones that most need the next attempt, and their records are kept for exactly
// that (see Manager.Destroy). A record whose deadline has passed stays past it,
// so every later tick retries until the host confirms the session is gone.
//
// Nothing is swept on the way in or on the way out. The first sweep is one
// interval away because a daemon that has just reconciled with the host has
// already destroyed anything past its ceiling (FR-025); the last is skipped
// because a cancelled context is the shutdown path, where tearing down *every*
// session — not only the expired ones — is T037's job and needs a context this
// one no longer has.
func (r *Reaper) Run(ctx context.Context) {
	ticks, stop := r.ticker(r.interval)
	defer stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticks:
			if _, err := r.Sweep(ctx); err != nil {
				r.report(err)
			}
		}
	}
}

// Sweep destroys every session past either bound, once, and reports what it took
// (FR-038).
//
// Teardown is Manager.Destroy and not a kill of its own, which is the whole of
// "the same verified teardown as an explicit destroy": tmux answering "I asked"
// is not tmux answering "it is gone", so a session the host cannot confirm gone
// keeps its record and comes back to the next sweep. A reaper that killed
// directly would report a bound enforced whenever tmux was willing to say
// anything at all.
//
// The instant is read once, so every record in one pass is judged against the
// same time. Two readings could put one session on either side of a deadline the
// pass had already used for another, which is a sweep that disagrees with itself
// about what time it is.
//
// Failures are collected rather than returned at the first one. One session the
// host cannot answer for must not leave the rest of an expired fleet standing —
// they are unsandboxed shells past their bounds, and the reason this runs
// without a request is that nothing else will come for them.
func (r *Reaper) Sweep(ctx context.Context) ([]Reaped, error) {
	now := r.mgr.clock.Now()

	var reaped []Reaped
	var failures []error

	for _, s := range r.mgr.store.snapshot() {
		expiry := expiredAt(s, now)
		if expiry == "" {
			continue
		}

		// The error names the bound and lets Destroy name the session. Neither
		// carries the caller's label, the working directory, or anything else a
		// request supplied — this string reaches a log line (FR-042, FR-043).
		if err := r.mgr.Destroy(ctx, s); err != nil {
			failures = append(failures, fmt.Errorf("reap a session past its %s bound: %w", expiry, err))
			continue
		}
		reaped = append(reaped, Reaped{Session: s, Expiry: expiry})
	}

	return reaped, errors.Join(failures...)
}

// expiredAt reports which bound s is past at now, or "" for a session still
// inside both.
//
// The ceiling is asked about first, so a session past both is recorded as having
// hit the bound that cannot be renewed. That ordering is for the trail: an
// operator reading "idle" about a session that had also been running for a day
// would be reading the smaller of two true facts, and the one that could have
// been avoided by using it.
//
// Both comparisons put the boundary on the dying side, matching CheckToken: at
// the deadline the session is already over. A lifetime that is "24 hours plus
// however long until the next tick" is not a lifetime anyone bounded — the tick
// decides when the daemon notices, and it may not also decide when the session
// was allowed to live until.
func expiredAt(s Session, now time.Time) Expiry {
	switch {
	case !now.Before(s.AbsoluteDeadline()):
		return ExpiryAbsolute
	case !now.Before(s.IdleDeadline()):
		return ExpiryIdle
	default:
		return ""
	}
}

// reportToLog is where a sweep's failures go until T038 gives the reaper the
// audit sink (reaper.destroy).
//
// It uses log rather than the trail for the reason httpapi's own last-resort
// reporter does: this is what is left when there is nowhere structured to write.
// What it prints is an error built in this repo out of a bound name and a session
// id, and never a secret, a token, a prompt, or pane content (FR-043).
func reportToLog(err error) { log.Printf("crswd: %v", err) }
