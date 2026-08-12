package updater

// facts.go is the one vocabulary for what became of this host's systemd unit,
// and it lives here rather than in the page that first needed it because two
// readers ask the same question of the same files.
//
// M15/T004 put these sentences on the settings page. M15/T005 puts them in the
// journal, which is where an operator looks when something is wrong and the only
// place a host that has never had a browser pointed at it can be told anything.
// Writing them twice would be two accounts of one file, free to drift — and the
// shape of that drift is a page and a journal disagreeing about whether an
// update will replace this operator's unit, a question with no tie-breaker
// anywhere on the host. Two implementations of one answer is the defect this
// milestone keeps finding in this repository (M15/T007); it is not worth
// introducing a fresh one in the milestone that exists to close it.
//
// unit.go reads the disk and this file says what the reading means. The split is
// this package's usual one: a step that shares a file with the next one is a step
// somebody removes with an early return.

import "strings"

// UnitFacts is what an operator is told about the systemd unit on this host: one
// sentence about where it stands, and — when a newer one is waiting — the file it
// is waiting in and the command that shows the difference.
//
// The three travel together in one value, taken once, because composed
// separately they would be free to disagree: the shape of that disagreement is
// "nothing newer is waiting" printed directly above the command to diff the thing
// that is.
//
// **Naming the file is the requirement, not a courtesy.** An operator who cannot
// find the new unit cannot compare it, and a difference nobody can see is a
// decision nobody can take — which is the silence this whole milestone exists to
// end.
type UnitFacts struct {
	// Sentence is which of the answers below applies. Empty is the zero value and
	// says nothing at all, which is what a caller with no unit to describe emits.
	Sentence string

	// Waiting is the file a release's own unit was left in, and is empty unless
	// that file is really on this disk (UnitReport.Offer).
	Waiting string

	// Compare is the command that shows what is in it and not in theirs.
	//
	// It is a string to be read and pasted, never one this daemon runs: nothing in
	// this project executes a shell, and docs/security.md's rule that no shell
	// string is ever constructed is about what this process does rather than about
	// what it prints for a person. It is quoted all the same, because a home
	// directory with a space in it is a command that silently diffs two other
	// files.
	Compare string
}

// The whole vocabulary for what became of this host's unit (M15/T004, M15/T005),
// and the end of this daemon's silence about it.
//
// A host two fixes behind looked exactly like a current one, because nothing on
// it could say which it was — the operator this milestone is for is running a
// unit with the ExecStart path v0.80 fixed and no EnvironmentFile line at all,
// and has had no way to find out. These are that fact, said: four arrangements a
// host can be in, and a fifth sentence for a read that could not be made at all.
//
// **Each says only what this daemon actually observed**, which is why the third
// is shaped the way it is. Absence of a crswd.service.new means no update has
// left one; it does not mean the operator's own unit matches the release, and
// only an update — which has the published bytes — is in a position to say that.
// A sentence claiming "yours is current" on the strength of a missing file would
// be exactly the false reassurance this milestone set out to end, on the host
// where it is least true: one that has never been updated at all.
const (
	UnitSentenceOffered = "A newer systemd unit is waiting. An update never replaces a unit this daemon did not write, so nothing here was changed and this one is yours to take or to leave."

	UnitSentenceOurs = "This host's systemd unit is the one this daemon installed, and an update replaces it."

	UnitSentenceTheirs = "This host's systemd unit is not one this daemon wrote, so an update never replaces it. Nothing newer is waiting beside it."

	UnitSentenceAbsent = "This host has no systemd unit for this daemon. An update installs the one the release ships."

	UnitSentenceUnknown = "This daemon could not read the systemd unit on this host."
)

// DescribeUnit turns what Report found on this disk into what an operator is
// told, and it is handed the error as well as the report on purpose.
//
// A read that failed is a sentence of its own rather than a blank. The zero
// UnitReport reads as "this host has no unit", which is the one answer that
// invites an operator to expect an update to install one — so a report nobody
// could take must not be allowed to fall through to it, and Report refuses rather
// than returning that zero value for the same reason.
//
// The offer is asked about first, ahead of everything else that is true of the
// host. It is the answer the operator has to act on, and it is the only one of
// the four that names a file they can put in front of them.
func DescribeUnit(report UnitReport, err error) UnitFacts {
	if err != nil {
		return UnitFacts{Sentence: UnitSentenceUnknown}
	}
	switch {
	case report.Offer != "":
		return UnitFacts{
			Sentence: UnitSentenceOffered,
			Waiting:  report.Offer,
			Compare:  unitCompare(report.Path, report.Offer),
		}
	case !report.Present:
		return UnitFacts{Sentence: UnitSentenceAbsent}
	case report.Ours:
		return UnitFacts{Sentence: UnitSentenceOurs}
	default:
		return UnitFacts{Sentence: UnitSentenceTheirs}
	}
}

// unitCompare is the command an operator runs to see what they would be taking.
//
// `diff` and nothing cleverer: it is on every host this daemon runs on, it takes
// two paths, and what it prints is the whole of the decision. The operator's own
// file is named first, so what the command prints reads as "what would change"
// rather than as "what you have lost".
func unitCompare(unit, offer string) string {
	return "diff " + shellQuoted(unit) + " " + shellQuoted(offer)
}

// shellQuoted wraps a path so that the command above survives being pasted into
// a shell.
//
// This is for reading, never for running — nothing in this daemon executes a
// shell, and docs/security.md's rule that no shell string is ever constructed is
// about what this process does rather than about what it prints for a person.
// What it buys is that a home directory with a space in it does not become a
// command that quietly diffs two other files.
func shellQuoted(path string) string {
	return "'" + strings.ReplaceAll(path, "'", `'\''`) + "'"
}
