package main

// unit.go says what became of this host's systemd unit, once, at startup, into
// the journal (M15/T005).
//
// # Why the journal as well as the page
//
// The settings page has said this since M15/T004, and a page is not enough. The
// operator this milestone is for has a unit that no update will ever replace —
// they wrote it themselves, so there is no recorded digest, so install.sh and the
// updater both leave it alone, correctly — and it still carries the ExecStart
// path v0.80 fixed and no EnvironmentFile line at all. Nothing on the host has
// ever told them. A deployment that is quietly behind looks exactly like one that
// is current, and the journal is where an operator looks when something is wrong;
// it is also the only place a host nobody has pointed a browser at can be told
// anything at all.
//
// The daemon already prints its absent-identity-provider banner for precisely
// this reason, on every start rather than once: a weakened posture nobody is
// reminded of is one nobody remembers.
//
// # Why the words are internal/updater's
//
// The sentences, the waiting file and the diff command all come from
// updater.DescribeUnit, which is the same call the settings page composes from.
// One file described two ways would be two accounts an operator has no way to
// reconcile — a page and a journal disagreeing about whether an update replaces
// this unit is a question with no tie-breaker on the host. What this file adds is
// the one thing a journal can carry and a browser must not: when the read itself
// failed, the error that says which file and why.

import (
	"fmt"
	"io"
	"strings"

	"github.com/nctiggy/claude-remote-session-webhook/internal/updater"
)

// sayWhatBecameOfTheUnit writes this daemon's account of the host's systemd unit
// to the diagnostic stream, and takes the report exactly as updater.Report
// returns it — value and error together — because the error is a sentence rather
// than a reason to say nothing.
//
// A failed read must never fall through to silence or to the zero UnitReport:
// that value reads as "this host has no unit", whose sentence promises an update
// will install one. DescribeUnit is what refuses that, and it is given both
// halves so it can.
//
// **A write that fails is returned, and the caller treats it as fatal**, on the
// same terms as internal/config's two startup banners. A daemon whose diagnostic
// stream will not take a line has nowhere to report anything below this point, so
// the alternative is not "start anyway with one message missing" — it is a
// process whose next failure is invisible, discovered by swallowing an error this
// repository's conventions do not allow to be swallowed.
func sayWhatBecameOfTheUnit(warn io.Writer, report updater.UnitReport, readErr error) error {
	facts := updater.DescribeUnit(report, readErr)

	var banner strings.Builder
	banner.WriteString("crswd: " + facts.Sentence + "\n")

	// Only the journal gets this. The page states the same sentence and stops,
	// because an error naming a path on this disk is a diagnostic for whoever
	// administers the host rather than something a browser is owed.
	if readErr != nil {
		banner.WriteString("crswd: the read failed: " + readErr.Error() + "\n")
	}

	// The file and the command, which are the half an operator acts on. A line
	// saying a newer unit exists with no way to find it is a difference nobody can
	// see, and a difference nobody can see is a decision nobody can take.
	if facts.Waiting != "" {
		banner.WriteString("crswd: the newer unit is waiting at " + facts.Waiting + "\n")
		banner.WriteString("crswd: compare them with: " + facts.Compare + "\n")
	}

	if _, err := io.WriteString(warn, banner.String()); err != nil {
		return fmt.Errorf("emit the systemd-unit report: %w", err)
	}
	return nil
}
