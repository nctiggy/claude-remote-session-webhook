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

	// Adopt is the command that takes the waiting unit, and is empty on every
	// host where taking it would be refused.
	//
	// **Offered only where it would be granted**, which is the whole of FR-018. A
	// banner naming a command that goes on to refuse teaches an operator that the
	// suggestion is noise, and the next real one is the one they do not read. The
	// refusals are real and common — the binary at the wrong path is the case this
	// project's own host is in — so this is not a theoretical caution.
	//
	// Filled in only by a caller that has planned an adoption, which is the
	// startup banner. A render has not, which is why Inspect exists beside this.
	Adopt string

	// Inspect is the command that *reports* on the waiting unit, and is set
	// wherever one is waiting at all — whether or not taking it would be granted.
	//
	// It exists because the other two fields left the settings page with nothing
	// to say. The page named the waiting file and the diff and stopped, so an
	// operator who read it — which is where this project's own operator read it —
	// learned that a unit was waiting and not that anything on the host could do
	// something about it. Adopt could not fill that gap: a page render has not
	// planned an adoption and must not, because planning reads the operator's
	// configuration and a page that did it per request would be as slow and as
	// fallible as the file system underneath it.
	//
	// So the page points at the command that *can* answer, rather than answering.
	// That is honest about what a render knows, and it is one step from where the
	// operator already is.
	Inspect string

	// Compare is the command that shows what is in it and not in theirs.
	//
	// It is a string to be read and pasted, never one this daemon runs: nothing in
	// this project executes a shell, and docs/security.md's rule that no shell
	// string is ever constructed is about what this process does rather than about
	// what it prints for a person. It is quoted all the same, because a home
	// directory with a space in it is a command that silently diffs two other
	// files.
	Compare string

	// Override is the hardening drop-in changing what this unit produces, and
	// Overridden is the sentence about it. Both empty on a host with none, which
	// is most of them.
	//
	// **A separate fact rather than a fifth arrangement**, because it is
	// orthogonal to the other four: a host can be in any of them AND carry an
	// override. Folding it into Sentence would mean four more sentences and a
	// combination somebody eventually forgets to write.
	//
	// It exists because without it the other four can each be true and the
	// report still misleading. systemd merges <unit>.d/*.conf over the unit, so a
	// host can run the release's unit byte for byte under hardening the release
	// never shipped — "the one this release ships" is then a true statement about
	// a file and a false impression of a host.
	Override   string
	Overridden string
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
// UnitInspectCommand reports whether the waiting unit can be taken, and why not
// when it cannot. Spelled here because both the page and the journal name it, and
// a command named two ways is one an operator gets wrong once.
const UnitInspectCommand = "crswd unit check"

const (
	UnitSentenceOffered = "A newer systemd unit is waiting. An update never replaces a unit this daemon did not write, so nothing here was changed and this one is yours to take or to leave."

	UnitSentenceOurs = "This host's systemd unit is the one this daemon installed, and an update replaces it."

	UnitSentenceTheirs = "This host's systemd unit is not one this daemon wrote, so an update never replaces it. Nothing newer is waiting beside it."

	UnitSentenceAbsent = "This host has no systemd unit for this daemon. An update installs the one the release ships."

	UnitSentenceUnknown = "This daemon could not read the systemd unit on this host."

	// UnitSentenceOverridden is said in addition to one of the five above, never
	// instead of one.
	//
	// It states what the file does rather than which settings it changes: this
	// daemon reads the drop-in's existence, not its contents, and a sentence
	// naming settings it never parsed would be a claim about a file somebody may
	// since have edited. Naming the path is what lets the operator go and read
	// the settings themselves.
	UnitSentenceOverridden = "A hardening override is in effect, so this host does not run under the hardening its unit alone describes. An update never touches it."
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

	var facts UnitFacts
	switch {
	case report.Offer != "":
		facts = UnitFacts{
			Sentence: UnitSentenceOffered,
			Waiting:  report.Offer,
			Compare:  unitCompare(report.Path, report.Offer),
			Adopt:    report.AdoptCommand,
			Inspect:  UnitInspectCommand,
		}
	case !report.Present:
		facts = UnitFacts{Sentence: UnitSentenceAbsent}
	case report.Ours:
		facts = UnitFacts{Sentence: UnitSentenceOurs}
	default:
		facts = UnitFacts{Sentence: UnitSentenceTheirs}
	}

	// Added to whichever of the four applies, never in place of one. A host with
	// an override is still absent, ours, theirs or offered — the override says
	// that the unit is not the whole story about what systemd runs.
	if report.Override != "" {
		facts.Override = report.Override
		facts.Overridden = UnitSentenceOverridden
	}
	return facts
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
