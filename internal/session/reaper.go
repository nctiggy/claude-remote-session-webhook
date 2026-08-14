package session

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/nctiggy/claude-remote-session-webhook/internal/audit"
)

// SweepInterval is how often Run asks the store whether anything has expired.
//
// plan.md fixes it at 30 seconds, and the only thing that matters about the
// number is the property behind it: the sweep's resolution must be finer than
// the lifetime it enforces, or the bound an operator reads in
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
// There is one, and the type is kept anyway. The trail names the bound in every
// reaper.destroy record, and a record that stopped naming it would be a change to
// the operator's own vocabulary made in passing while removing something else —
// which is the churn Principle IV rules out. A second bound added here later
// finds the shape it needs already in place.
type Expiry string

const (
	// ExpiryAbsolute is AbsoluteLifetime since the session began, and it is the
	// one that cannot be renewed: no amount of use moves it, which is exactly
	// what makes it a ceiling.
	//
	// It became the only one in milestone 15. There was an idle bound beside it
	// until then — the bound that destroyed four sessions on 2026-08-14 one hour
	// after a restart re-adopted them — and constitution 2.0.0 withdrew it.
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
// It exists because the alternative is enforcing the lifetime on the next
// request, and the session that most needs to die is the one no request is
// coming for. Principle VI names the absolute lifetime "enforced by a reaper,
// not by the next request" for that reason, and it is the difference between a
// bound and a hope.
//
// Everything it decides from belongs to the Manager it was built on — the same
// clock, the same store, the same verified teardown. That is deliberate: a
// reaper with a clock of its own could hold a session past a deadline the
// credential check had already enforced, or destroy one the daemon still
// considered live.
type Reaper struct {
	mgr      *Manager
	interval time.Duration

	// trail is the audit sink, and a sweep is the one teardown that needs it
	// most: there is no request behind it and so no response for the outcome to
	// travel back in. FR-041 makes the record the whole account of what the
	// daemon did to a session nobody came back for.
	trail *audit.Logger

	// ticker and report are the two seams. Both are fields rather than calls
	// into the package they come from, so that Run's loop and its loud-failure
	// path are reachable from a test without elapsed time and without stderr.
	ticker ticker
	report func(error)
}

// NewReaper builds the reaper for a manager and an audit sink, on the documented
// interval.
//
// A nil manager is refused rather than tolerated: a Reaper without one has no
// store to sweep, no clock to sweep on, and no verified teardown to sweep with,
// and a daemon that started with one would be a daemon whose sessions have no
// end at all. That is the failure Principle VI ranks above not starting.
//
// A nil sink is refused for the same reason newServer refuses one. A reaper
// destroys unsandboxed shells on nobody's request; one that could not write a
// record would do it with nothing left to say it happened, which is the trail
// FR-041 requires being absent exactly where nothing else reports.
func NewReaper(m *Manager, trail *audit.Logger) (*Reaper, error) {
	switch {
	case m == nil:
		return nil, errors.New("session: no session manager provided for the reaper; refusing to start")
	case trail == nil:
		return nil, errors.New("session: no audit sink provided for the reaper; refusing to start")
	}
	return &Reaper{mgr: m, trail: trail, interval: SweepInterval, ticker: systemTicker, report: reportToLog}, nil
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
//
// Every session the sweep acts on gets one record, whichever way the teardown
// went (FR-041). It is written here rather than in Run because Run discards what
// a sweep returns, and because a record is the only account there is: a caller
// learning its session is gone is a response, and a sweep has none.
func (r *Reaper) Sweep(ctx context.Context) ([]Reaped, error) {
	now := r.mgr.clock.Now()

	var reaped []Reaped
	var failures []error

	// No question is put to the host before judging. There was one until
	// milestone 15 — the sweep read every session's #{session_activity} so the
	// idle bound could see output the daemon never mediated — and the bound it
	// served is gone. The ceiling is measured from CreatedAt, which the record
	// already holds and nothing on the host can revise.
	for _, s := range r.mgr.store.snapshot() {
		expiry := expiredAt(s, now)
		if expiry == "" {
			continue
		}

		err := r.mgr.Destroy(ctx, s)
		if emitErr := r.trail.Emit(reapRecord(s, expiry, err)); emitErr != nil {
			failures = append(failures, emitErr)
		}
		if err != nil {
			// The error names the bound and lets Destroy name the session.
			// Neither carries the caller's label, the working directory, or
			// anything else a request supplied — this string reaches a log line
			// (FR-042, FR-043).
			failures = append(failures, fmt.Errorf("reap a session past its %s bound: %w", expiry, err))
			continue
		}
		reaped = append(reaped, Reaped{Session: s, Expiry: expiry})
	}

	return reaped, errors.Join(failures...)
}

// The reasons a sweep records, authored here as constants for the reason
// httpapi's refusal reasons are: the trail may carry no byte a caller supplied
// (FR-042), and a reason built at the call site out of an error, a name, or a
// path is how one gets in.
const (
	reasonPastAbsolute = "the session had reached its absolute lifetime"

	// reasonPastUnnamedBound is unreachable — expiredAt returns the one above or
	// nothing at all — and fails closed rather than recording a bound the daemon
	// did not decide, the same ruling errScopeRefused makes.
	reasonPastUnnamedBound = "the session was past a bound this daemon has no name for"

	// reasonUnconfirmed prefixes the reason of a teardown tmux would not
	// confirm. It is a prefix rather than a fourth pair of constants so that the
	// bound reads the same in both records: an operator greps one string to find
	// every session that hit a ceiling, whether or not it actually died.
	reasonUnconfirmed = "the host could not confirm the teardown, so the record is kept for the next sweep: "
)

// reapRecord is the trail's account of one session a sweep acted on: allowed
// when the host confirmed the teardown, denied when it did not (FR-020). A
// denial is the loudest thing this daemon has to say — the session is a live
// unsandboxed shell past its bound — and the trail is where an operator finds
// out it is still there.
//
// The caller is the session's own owner, not a constant repeated here: the field
// names whoever the ownership check would have compared against, and the reaper
// acts on nobody's request. Remote stays empty for the same reason.
//
// The error is deliberately not in the record. What it would add is the session
// id, which the record already carries under its own field, and every future
// rewording of it would be a new chance for FR-042 to be broken by a %w.
func reapRecord(s Session, expiry Expiry, err error) audit.Record {
	rec := audit.Record{
		Action:    audit.ActionReaperDestroy,
		Caller:    string(s.Owner),
		SessionID: s.ID,
		Decision:  audit.Allow,
		Reason:    reapReason(expiry),
	}
	if err != nil {
		rec.Decision = audit.Deny
		rec.Reason = reasonUnconfirmed + rec.Reason
	}
	return rec
}

func reapReason(expiry Expiry) string {
	switch expiry {
	case ExpiryAbsolute:
		return reasonPastAbsolute
	default:
		return reasonPastUnnamedBound
	}
}

// expiredAt reports which bound s is past at now, or "" for a session still
// inside it.
//
// The comparison puts the boundary on the dying side, matching CheckToken: at
// the deadline the session is already over. A lifetime that is "24 hours plus
// however long until the next tick" is not a lifetime anyone bounded — the tick
// decides when the daemon notices, and it may not also decide when the session
// was allowed to live until.
//
// A session whose absolute deadline is switched off is never past it: the
// deadline is a century out (neverSpan), so this returns "" for the whole of any
// real uptime rather than by way of a case that has to remember to skip it.
func expiredAt(s Session, now time.Time) Expiry {
	if !now.Before(s.AbsoluteDeadline()) {
		return ExpiryAbsolute
	}
	return ""
}

// reportToLog is the last-resort channel for what a sweep could not say in the
// trail: a teardown the host would not confirm, and the audit write that was
// supposed to record it.
//
// It uses log rather than the trail for the reason httpapi's own last-resort
// reporter does: this is what is left when the sink is the thing that broke.
// What it prints is an error built in this repo out of a bound name and a session
// id, and never a secret, a token, a prompt, or pane content (FR-043).
func reportToLog(err error) { log.Printf("crswd: %v", err) }
