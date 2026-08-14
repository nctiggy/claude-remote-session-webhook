package main

// `crswd unit check` and `crswd unit adopt` — the two things an operator does to
// a systemd unit that are not starting a daemon.
//
// They are the same pair as `config check` and `config migrate`, and named that
// way on purpose: an operator who has run one already knows what the other does.
// Check reads and reports; adopt does the thing and keeps what it replaced.
//
// # Why this is a terminal command and not a dashboard action
//
// It replaces what systemd executes and under which hardening. The dashboard
// already *reports* the unit's standing and the journal already names the file an
// update left waiting; both now name this command as well. What neither does is
// perform it, because a browser action whose blast radius is the service manager
// is a bigger door than this project has anywhere else — and the existing pair is
// the precedent an operator already has in their hands.
//
// # What it does not decide
//
// Nothing. Every decision is internal/updater's PlanAdoption, which reads and
// refuses; this file turns a plan into sentences and, on `adopt`, calls the write
// half. A command that made its own judgement about a host would be a second
// answer to "may this unit be replaced", free to disagree with the one an update
// acts on.

import (
	"fmt"
	"io"
	"os"

	"github.com/nctiggy/claude-remote-session-webhook/internal/config"
	"github.com/nctiggy/claude-remote-session-webhook/internal/updater"
)

// unitCommand is the argument main dispatches on, spelled once.
const unitCommand = "unit"

// adoptCommandLine is what the startup banner tells an operator to run, and what
// this command's own report tells them to run. One spelling, because a banner
// naming a command that does not exist is worse than a banner saying nothing.
const adoptCommandLine = "crswd unit adopt"

const unitUsage = `usage: crswd unit check    report whether this host's systemd unit can be adopted
       crswd unit adopt    take the unit a release left waiting, keeping your hardening
`

// runUnitCommand answers `crswd unit …` and returns the process's exit code.
//
// An unrecognised subcommand is refused rather than ignored, for
// runConfigCommand's reason: ignored, it falls through to starting a daemon, and
// a mistyped `crswd unit adpot` on a live host would bind a second listener and
// reconcile the first daemon's sessions onto itself.
func runUnitCommand(out, errOut io.Writer, args []string) int {
	if len(args) < 2 {
		say(errOut, "crswd: unit needs a subcommand\n%s", unitUsage)
		return 2
	}

	adopt := false
	switch args[1] {
	case "check":
	case "adopt":
		adopt = true
	default:
		say(errOut, "crswd: unknown unit subcommand %q\n%s", args[1], unitUsage)
		return 2
	}
	if len(args) > 2 {
		say(errOut, "crswd: unit %s takes no arguments\n%s", args[1], unitUsage)
		return 2
	}

	if err := runUnit(out, adopt); err != nil {
		say(errOut, "crswd: %v\n", err)
		return 1
	}
	return 0
}

// runUnit plans, reports, and — when asked and when the plan allows — performs.
func runUnit(out io.Writer, adopt bool) error {
	unit := updater.NewUnit(os.Getenv)

	plan, err := unit.PlanAdoption(configResolver())
	if err != nil {
		return err
	}

	// The hosts with nothing to do. Not a failure and not a refusal: an operator
	// told "cannot adopt" about an already-managed host would go looking for a
	// problem that is not there.
	if plan.Why != "" {
		say(out, "%s.\n", plan.Why)
		return nil
	}

	sayPlan(out, plan)

	if !plan.Adoptable {
		// Exit non-zero so a script can tell the two apart, and say plainly that
		// nothing happened — a refusal that left an operator unsure whether their
		// unit had been touched is worse than no command at all.
		return fmt.Errorf("this host cannot be adopted yet; nothing was written")
	}
	if !adopt {
		say(out, "\nRun `%s` to take it.\n", adoptCommandLine)
		return nil
	}

	if err := unit.Adopt(plan); err != nil {
		return err
	}

	say(out, "\nAdopted.\n")
	say(out, "  the unit that was replaced is at %s\n", plan.Backup)
	say(out, "  the digest of the one now installed is at %s\n", plan.Record)
	// FR-008: a file on disk is not a running service. A command that reported
	// success while systemd was still running the old unit would be stating a fact
	// about a file as a fact about a host — which is the whole class of silence
	// this area of the project has been closing.
	say(out, "\nNothing has changed for the running service yet. Put it into effect with:\n")
	say(out, "  systemctl --user daemon-reload && systemctl --user restart crswd\n")
	return nil
}

// sayPlan is the report, and it is the same text whether or not the adoption is
// about to happen — check and adopt differ in what they do, never in what they
// claim.
func sayPlan(out io.Writer, plan updater.AdoptPlan) {
	say(out, "Your unit:      %s\n", plan.Unit)
	say(out, "The one waiting: %s\n", plan.Waiting)

	if len(plan.Relaxations) > 0 {
		if plan.DropInExists {
			say(out, "\nYour unit relaxes hardening that the override already beside it carries:\n")
		} else {
			say(out, "\nYour unit relaxes hardening, which would move into %s:\n", plan.DropIn)
		}
		for _, r := range plan.Relaxations {
			say(out, "  %s=%s\n", r.Setting, r.Value)
		}
		say(out, "\nThose are your own settings, read out of your own unit. This grants nothing\nthat is not already in effect on this host.\n")
	}

	if len(plan.Dropped) > 0 {
		say(out, "\nYour unit assigns these, and the waiting one does not. Your configuration\nfile already produces the same values, so dropping the lines changes nothing:\n")
		for _, name := range plan.Dropped {
			say(out, "  %s\n", name)
		}
	}

	if len(plan.Refusals) > 0 {
		say(out, "\nThis host cannot be adopted yet:\n")
		for _, r := range plan.Refusals {
			say(out, "  - %s\n", r.What)
			say(out, "    %s\n", r.Fix)
		}
	}
}

// configResolver answers what the daemon would load for a variable if the unit
// stopped assigning it, which is FR-012's whole question.
//
// It reads the operator's configuration file and nothing else. That is the point:
// precedence is `environment > file > default`, so a variable the *unit* assigns
// is one whose file value is currently being shadowed — and what adoption has to
// know is what would surface underneath it.
//
// A host with no configuration file resolves nothing, and every non-empty
// assignment on such a host becomes a refusal. That is the right direction: the
// operator's settings live in their unit, dropping the lines would lose them, and
// the fix the refusal names — put them in a configuration file — is exactly what
// they should do.
func configResolver() updater.ConfigResolver {
	path := config.DefaultPath(os.Getenv)
	if path == "" {
		return fileResolver{}
	}
	data, err := os.ReadFile(path) //nolint:gosec // G304: the path is the one the daemon itself would read, from its own resolution rule.
	if err != nil {
		return fileResolver{}
	}
	// io.Discard: a renamed-key warning belongs to a startup, and this command is
	// not one. What it is doing with the file is asking what is in it.
	file, err := config.ParseFile(path, data, io.Discard)
	if err != nil {
		return fileResolver{}
	}
	return fileResolver{file: file}
}

// fileResolver is the operator's configuration file as a ConfigResolver.
type fileResolver struct{ file *config.File }

func (r fileResolver) Resolve(name string) (string, bool) {
	if r.file == nil {
		return "", false
	}
	return r.file.Lookup(name)
}
