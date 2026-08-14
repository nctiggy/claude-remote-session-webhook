package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/nctiggy/claude-remote-session-webhook/internal/updater"
)

// The report is the same text whether or not the adoption is about to happen —
// check and adopt differ in what they do, never in what they claim. A command
// whose dry run described something other than what it went on to do would be
// the one thing an operator uses it for, broken.
func TestTheUnitReportSaysTheSameThingBothWays(t *testing.T) {
	t.Parallel()

	plan := updater.AdoptPlan{
		Adoptable:   true,
		Unit:        "/home/o/.config/systemd/user/crswd.service",
		Waiting:     "/home/o/.config/systemd/user/crswd.service.new",
		DropIn:      "/home/o/.config/systemd/user/crswd.service.d/10-relax.conf",
		Relaxations: []updater.Relaxation{{Setting: "ProtectSystem", Value: "false"}},
		Dropped:     []string{"CRSW_LISTEN"},
	}

	var out bytes.Buffer
	sayPlan(&out, plan)
	got := out.String()

	for _, want := range []string{
		plan.Unit,
		plan.Waiting,
		plan.DropIn,
		"ProtectSystem=false",
		"CRSW_LISTEN",
		// The sentence that matters most: an operator reading this is being asked
		// to let a command rewrite what systemd executes, and what makes that
		// reasonable is that it grants nothing.
		"grants nothing",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the report does not mention %q:\n%s", want, got)
		}
	}
}

// A refusal names what it refused on and what to do, and says plainly that
// nothing was written. An operator left unsure whether their unit had been
// touched is worse off than one who never ran the command.
func TestTheUnitReportNamesEveryRefusalAndItsFix(t *testing.T) {
	t.Parallel()

	plan := updater.AdoptPlan{
		Unit:    "/u",
		Waiting: "/w",
		Refusals: []updater.Refusal{
			{What: "the waiting unit runs /home/o/.local/bin/crswd, and there is no executable there", Fix: "re-run the installer"},
			{What: "your unit sets CRSW_PANE_BOUND to a value your configuration file does not", Fix: "put that value in your configuration file"},
		},
	}

	var out bytes.Buffer
	sayPlan(&out, plan)
	got := out.String()

	for _, refusal := range plan.Refusals {
		if !strings.Contains(got, refusal.What) {
			t.Errorf("the report does not name the refusal %q:\n%s", refusal.What, got)
		}
		if !strings.Contains(got, refusal.Fix) {
			t.Errorf("the report does not say what to do about %q:\n%s", refusal.What, got)
		}
	}
}

// An unrecognised subcommand is refused rather than ignored. Ignored, it falls
// through to starting a daemon — a mistyped `crswd unit adpot` on a live host
// would bind a second listener and reconcile the first daemon's sessions onto
// itself, which is the accident every subcommand in this program is shaped to
// prevent.
func TestUnitRefusesWhatItDoesNotUnderstand(t *testing.T) {
	t.Parallel()

	for name, args := range map[string][]string{
		"no subcommand":                {"unit"},
		"a subcommand typo":            {"unit", "adpot"},
		"an argument it takes none of": {"unit", "check", "/some/path"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			var out, errOut bytes.Buffer
			if code := runUnitCommand(&out, &errOut, args); code != 2 {
				t.Errorf("runUnitCommand(%v) = %d; want the usage exit", args, code)
			}
			if out.Len() != 0 {
				t.Errorf("a refused invocation wrote to stdout: %s", out.String())
			}
			if !strings.Contains(errOut.String(), "crswd unit") {
				t.Errorf("the refusal does not show the usage:\n%s", errOut.String())
			}
		})
	}
}
