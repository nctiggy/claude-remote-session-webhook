package main

// What unit.go has to be true of, and — the half that matters more — that
// startup actually calls it. M15's own plan says a task is not done when the code
// exists but when something calls it, because this milestone exists at all only
// because `crswd config migrate` was written and never run.

import (
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
	"testing"

	"github.com/nctiggy/claude-remote-session-webhook/internal/updater"
)

// TestTheJournalSaysWhatBecameOfTheUnit is M15/T005.
//
// The settings page says this already. A page is not enough: the operator this
// milestone is for runs a unit two fixes behind that no update will ever replace,
// on a host whose journal is the first place they look when something is wrong.
//
// **Must fail when** any two arrangements produce the same lines — a host with a
// newer unit waiting and a host with nothing waiting reading alike is the whole
// defect — or when the file and the command stop being named beside the sentence
// that says one is waiting.
func TestTheJournalSaysWhatBecameOfTheUnit(t *testing.T) {
	t.Parallel()

	const unit = "/home/o/.config/systemd/user/crswd.service"

	for _, c := range []struct {
		name    string
		report  updater.UnitReport
		readErr error
		want    string
		unwant  string
		waiting bool
		why     string
	}{
		{
			name:    "a newer unit is waiting beside the operator's own",
			report:  updater.UnitReport{Path: unit, Present: true, Offer: unit + ".new"},
			want:    updater.UnitSentenceOffered,
			waiting: true,
			why:     "this is the case the milestone exists for, and the journal is where a host with no browser on it gets told",
		},
		{
			name:   "the unit is this daemon's to replace",
			report: updater.UnitReport{Path: unit, Present: true, Ours: true},
			want:   updater.UnitSentenceOurs,
			unwant: updater.UnitSentenceTheirs,
			why:    "install.sh's record describes the file that is there, so an update brings it forward",
		},
		{
			name:   "the operator wrote their own and nothing is waiting",
			report: updater.UnitReport{Path: unit, Present: true},
			want:   updater.UnitSentenceTheirs,
			unwant: updater.UnitSentenceOurs,
			why:    "no record is every host deployed before the installer existed, this one included",
		},
		{
			name:   "this host has no unit at all",
			report: updater.UnitReport{Path: unit},
			want:   updater.UnitSentenceAbsent,
			unwant: updater.UnitSentenceTheirs,
			why:    "nothing is being protected, so what an update does is install one",
		},
		{
			name:    "the unit could not be read",
			readErr: updater.ErrNoUnitHome,
			want:    updater.UnitSentenceUnknown,
			unwant:  updater.UnitSentenceAbsent,
			why:     "a read nobody could take must never read as \"no unit here\", whose sentence promises an update installs one",
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			var journal strings.Builder
			if err := sayWhatBecameOfTheUnit(&journal, c.report, c.readErr); err != nil {
				t.Fatalf("sayWhatBecameOfTheUnit() = %v; want the report written", err)
			}
			said := journal.String()

			if !strings.Contains(said, c.want) {
				t.Errorf("the journal does not say %q.\n%s\ngot:\n%s", c.want, c.why, said)
			}
			if c.unwant != "" && strings.Contains(said, c.unwant) {
				t.Errorf("the journal says %q about this host as well, so it states two things about one file:\n%s", c.unwant, said)
			}

			named := strings.Contains(said, unit+".new")
			// Spelled out rather than composed with updater's own helper, so a
			// banner carrying some other command fails rather than agreeing with
			// itself.
			compared := strings.Contains(said, "diff '"+unit+"' '"+unit+".new'")
			if named != c.waiting {
				t.Errorf("the journal names the waiting unit: %t; want %t.\n%s", named, c.waiting, said)
			}
			if compared != c.waiting {
				t.Errorf("the journal carries the diff command: %t; want %t.\nOn a host with nothing waiting it sends the operator to compare a file that is not there:\n%s", compared, c.waiting, said)
			}

			// A journal line is a line. Everything here is read by eye beside
			// records and beside every other banner this daemon prints.
			if !strings.HasSuffix(said, "\n") {
				t.Errorf("the report does not end in a newline, so the next line printed continues it:\n%q", said)
			}
			for _, line := range strings.Split(strings.TrimSuffix(said, "\n"), "\n") {
				if !strings.HasPrefix(line, "crswd: ") {
					t.Errorf("%q is not prefixed like the daemon's other startup diagnostics", line)
				}
			}
		})
	}
}

// TestAFailedReadIsNamedInTheJournalAndNowhereElse is the one thing this banner
// carries that the settings page deliberately does not.
//
// An operator whose unit could not be read is told a sentence on the page and
// nothing more, because the error names a path on this disk. In the journal that
// same detail is the whole value: "could not read the unit" with no reason is a
// line that sends somebody looking, and the file and the errno are what stop that
// being a search.
//
// **Must fail when** the error is reduced to the sentence updater already gives.
func TestAFailedReadIsNamedInTheJournalAndNowhereElse(t *testing.T) {
	t.Parallel()

	readErr := errors.New("read the systemd unit /home/o/.config/systemd/user/crswd.service: permission denied")

	var journal strings.Builder
	if err := sayWhatBecameOfTheUnit(&journal, updater.UnitReport{}, readErr); err != nil {
		t.Fatalf("sayWhatBecameOfTheUnit() = %v; want the report written", err)
	}

	if !strings.Contains(journal.String(), readErr.Error()) {
		t.Errorf("the journal does not carry why the read failed, so an operator is told to go looking:\n%s", journal.String())
	}
}

// TestAReportThatCouldNotBeWrittenIsReturned keeps the write out of the class of
// things this daemon does and forgets.
//
// **Must fail when** the write error is dropped — which is the shape a "this is
// only a banner" simplification takes, and which would leave the startup sequence
// with one step that cannot fail for a reason nobody wrote down.
func TestAReportThatCouldNotBeWrittenIsReturned(t *testing.T) {
	t.Parallel()

	err := sayWhatBecameOfTheUnit(refusingWriter{}, updater.UnitReport{Path: "/x", Present: true}, nil)

	if err == nil {
		t.Fatal("sayWhatBecameOfTheUnit() = nil on a stream that refused the line; want the failure returned")
	}
	if !errors.Is(err, errRefused) {
		t.Errorf("sayWhatBecameOfTheUnit() = %v; want it to wrap what the stream said", err)
	}
}

var errRefused = errors.New("this stream takes nothing")

// refusingWriter is a diagnostic stream that will not take a line — fd 2 closed,
// or a full disk behind a redirect.
type refusingWriter struct{}

func (refusingWriter) Write([]byte) (int, error) { return 0, errRefused }

// TestStartupSaysWhatBecameOfTheUnit is the assertion this milestone was written
// for: not that the code exists, but that a start runs it.
//
// M15 exists because `crswd config migrate` was written, tested, and called by
// nothing for four milestones. A banner nothing prints is that defect again, and
// it is invisible to every behavioural test in this file — they all call the
// function themselves.
//
// A wiring assertion, in TestStartupDiagnosticsGoToStderr's shape and for its
// reason: the sink is a parameter, so what is left to get wrong is the line in
// main.go that chooses it.
//
// **Must fail when** the call is dropped from run, duplicated, or pointed at a
// stream other than stderr — stdout carries the audit trail and nothing else.
func TestStartupSaysWhatBecameOfTheUnit(t *testing.T) {
	t.Parallel()

	const report = "sayWhatBecameOfTheUnit"

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "main.go", nil, 0)
	if err != nil {
		t.Fatalf("parse main.go: %v", err)
	}

	var calls int
	ast.Inspect(file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		fn, ok := call.Fun.(*ast.Ident)
		if !ok || fn.Name != report {
			return true
		}

		calls++
		if len(call.Args) == 0 {
			t.Errorf("%s: %s is called with no arguments", fset.Position(call.Pos()), report)
			return true
		}
		if got := render(call.Args[0]); got != "os.Stderr" {
			t.Errorf("%s: the unit report is written to %s; it is a diagnostic and stdout is the trail's", fset.Position(call.Pos()), got)
		}
		return true
	})

	if calls != 1 {
		t.Fatalf("main.go calls %s %d times; want exactly one, in the startup sequence. A report nothing prints is this milestone's own defect repeated", report, calls)
	}
}
