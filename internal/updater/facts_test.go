package updater

// What facts.go has to be true of. Both cases moved here from
// internal/httpapi/settings_test.go with the vocabulary itself (M15/T005), and
// they cover the two halves nothing else in this tree would notice: a judgement
// the page and the journal both make, and quoting neither of them exercises.

import (
	"strings"
	"testing"
)

// TestTheUnitSentenceIsNeverAReassuranceNothingEarned is the one arm of
// DescribeUnit that is a judgement rather than a reading.
//
// An account composed from a report nobody could take must not fall through to
// the zero UnitReport, because that value reads as "this host has no unit" — and
// the sentence for *that* is the one arrangement in which this daemon promises to
// install one. Telling an operator with an unreadable unit that an update will
// place a fresh one is worse than the silence this milestone set out to fix.
//
// **Must fail when** the error is dropped and the report is read anyway.
func TestTheUnitSentenceIsNeverAReassuranceNothingEarned(t *testing.T) {
	t.Parallel()

	facts := DescribeUnit(UnitReport{}, ErrNoUnitHome)

	if facts.Sentence != UnitSentenceUnknown {
		t.Errorf("a report that could not be taken says %q; want %q", facts.Sentence, UnitSentenceUnknown)
	}
	if facts.Waiting != "" || facts.Compare != "" {
		t.Errorf("a report that could not be taken names %q and offers %q; neither is a file this daemon ever looked at", facts.Waiting, facts.Compare)
	}
}

// TestEveryArrangementOfAHostGetsItsOwnSentence is the property both readers
// rest on: the settings page and the startup banner each say one of these, and a
// host with a newer unit waiting must not read like a host with nothing waiting.
// That pair is the defect this milestone exists to end.
//
// **Must fail when** any two arrangements collapse into one sentence — folding
// UnitTheirs into UnitAbsent, say, which would promise an update installs a unit
// on a host whose own unit is the thing being protected.
func TestEveryArrangementOfAHostGetsItsOwnSentence(t *testing.T) {
	t.Parallel()

	const unit = "/home/o/.config/systemd/user/crswd.service"

	for _, c := range []struct {
		name    string
		report  UnitReport
		want    string
		waiting bool
		why     string
	}{
		{
			name:    "a newer unit is waiting beside the operator's own",
			report:  UnitReport{Path: unit, Present: true, Offer: unit + ".new"},
			want:    UnitSentenceOffered,
			waiting: true,
			why:     "this is the case the milestone exists for: their edit kept, and the release's own unit named so they can diff it",
		},
		{
			name:   "the unit is this daemon's to replace",
			report: UnitReport{Path: unit, Present: true, Ours: true},
			want:   UnitSentenceOurs,
			why:    "the digest install.sh recorded describes the file that is there, so an update brings it forward and nothing is waiting",
		},
		{
			name:   "the operator wrote their own and nothing is waiting",
			report: UnitReport{Path: unit, Present: true},
			want:   UnitSentenceTheirs,
			why:    "no record is every host deployed before the installer existed, and this daemon has to say that an update will never replace that file",
		},
		{
			name:   "this host has no unit at all",
			report: UnitReport{Path: unit},
			want:   UnitSentenceAbsent,
			why:    "nothing is being protected from anything, so what an update does is install one — a different answer from leaving a file alone",
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			facts := DescribeUnit(c.report, nil)

			if facts.Sentence != c.want {
				t.Errorf("DescribeUnit() says %q; want %q.\n%s", facts.Sentence, c.want, c.why)
			}

			// The file and the command, which are the half an operator acts on. A
			// sentence saying a newer unit exists and no way to find it is a
			// difference nobody can see, and a difference nobody can see is a
			// decision nobody can take.
			if named := facts.Waiting != ""; named != c.waiting {
				t.Errorf("DescribeUnit() names a waiting file: %t; want %t", named, c.waiting)
			}
			if compared := facts.Compare != ""; compared != c.waiting {
				t.Errorf("DescribeUnit() carries the diff command: %t; want %t.\nOn a host with nothing waiting it would send the operator to compare a file that is not there", compared, c.waiting)
			}
		})
	}
}

// TestTheDiffCommandSurvivesAHomeWithASpaceInIt is the one thing about that
// command that is not obvious from reading it.
//
// It is printed for an operator to paste, so a path with a space in it becomes a
// command that quietly diffs two other files — and the operator reads its output
// as the difference between their unit and the release's.
//
// **Must fail when** the quoting is dropped. Nothing else in this tree would
// notice: every path a test builds is under a temporary directory with no spaces
// in it, and the command is never executed here or in production.
func TestTheDiffCommandSurvivesAHomeWithASpaceInIt(t *testing.T) {
	t.Parallel()

	got := unitCompare("/home/a b/.config/systemd/user/crswd.service", "/home/a b/.config/systemd/user/crswd.service.new")

	if want := `diff '/home/a b/.config/systemd/user/crswd.service' '/home/a b/.config/systemd/user/crswd.service.new'`; got != want {
		t.Errorf("unitCompare() = %s\nwant %s", got, want)
	}
	// And a quote in the path closes the quoting rather than escaping out of it.
	if got := unitCompare("/home/it's/crswd.service", "/x"); !strings.Contains(got, `'/home/it'\''s/crswd.service'`) {
		t.Errorf("unitCompare() = %s; a quote in the path was not escaped, so the command ends early", got)
	}
}
