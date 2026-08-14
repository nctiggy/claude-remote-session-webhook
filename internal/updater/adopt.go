package updater

// adopt.go is the third option beside "replace it" and "leave it alone".
//
// unit.go's rule is unchanged and remains right: a unit with no recorded digest
// is one no install and no update may overwrite, because absence of evidence that
// this project wrote a file is not permission to replace it. What that rule has
// never had is a way *out*. A host whose operator hand-edited their unit — to
// make `sudo` work inside a session, which is the case #138 was written for — is
// offered a crswd.service.new by every release and can never take one, forever.
//
// #138 built the mechanism that resolves it: the relaxation belongs in
// <unit>.d/*.conf, which systemd merges over the unit and which nothing in this
// project ever touches, so the unit itself stays byte-identical to the release's
// and stays replaceable. It shipped only for fresh installs. This is that
// mechanism applied to a host that already has the problem.
//
// # It grants nothing
//
// Every line the drop-in gets is derived from a relaxation the operator's *own
// unit already makes*, never from a constant, and a relaxation this file cannot
// express is a refusal rather than something to leave behind. So what an adopted
// host permits is what it permitted an instant earlier — the same privileges,
// written where an update can survive them. That is the claim Principle VI's
// widening rule is measured against, and PlanAdoption is where it is enforced.
//
// # Decide, then write
//
// PlanAdoption reads and decides. Adopt writes and decides nothing. They are
// split for the reason every step in this package is split: a function that
// decided and wrote is one where the refusal somebody adds next year gets added
// after the first write, and a partly-adopted host is worse than an unadopted one
// because the operator has been told which of the two it is.

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/nctiggy/claude-remote-session-webhook/internal/config"
)

// The hardening directives the drop-in expresses, and the value each one takes
// there.
//
// **This table and deploy/crswd.service.d/10-relax.conf.example are one fact.**
// The example is what install.sh writes and what an operator reads; this is what
// decides whether an operator's own relaxation can be reproduced. If they
// disagree, adoption either refuses a host it could have taken or — far worse —
// reports having reproduced a setting the drop-in does not carry.
// TestTheDropInExpressesExactlyTheseSettings holds the two together.
//
// Every one of them is a boolean whose *false* is the relaxed direction, which is
// what makes "the current unit is more permissive" a comparison rather than a
// judgement. A future directive whose relaxation is not simply `false` does not
// belong in this table without the comparison being generalised first.
var dropInSettings = []string{
	"NoNewPrivileges",
	"RestrictSUIDSGID",
	"ProtectKernelTunables",
	"ProtectSystem",
}

// systemdDefaults is what a directive means when a unit does not assign it.
//
// **Absent is a value, not a silence**, and reading it as "no opinion" is the one
// mistake that would make this feature useless on the host it was written for:
// that host relaxes its hardening by commenting the lines out, so every relaxation
// it makes is an absence.
//
// All four default off, which is systemd's own posture — a service gets no
// hardening it does not ask for. That is why the release's unit has to assign
// them and why an operator removing an assignment is a relaxation.
var systemdDefaults = map[string]string{
	"NoNewPrivileges":       "false",
	"RestrictSUIDSGID":      "false",
	"ProtectKernelTunables": "false",
	"ProtectSystem":         "false",
}

// Relaxation is one hardening setting the operator's unit leaves more permissive
// than the release's, and which the drop-in will carry.
type Relaxation struct {
	// Setting is the systemd directive, and Value is what the drop-in assigns —
	// the operator's own effective value, never a constant.
	Setting string
	Value   string
}

// ConfigWrite is one setting moving from the operator's unit into their
// configuration file, so that dropping the unit's assignment of it changes
// nothing about what the daemon loads.
type ConfigWrite struct {
	// Var is the environment variable the unit assigns, Key is how the
	// configuration file spells the same setting, and Value is the unit's own
	// value — carried across unchanged, because preserving what this host does is
	// the whole point.
	Var, Key, Value string
}

// Refusal is one reason this host cannot be adopted, in the operator's terms.
//
// It carries what to do as well as what is wrong, because a refusal an operator
// cannot act on is a refusal that reads as a bug in this command.
type Refusal struct {
	What string
	Fix  string
}

// AdoptPlan is everything PlanAdoption decided, and the only input Adopt takes.
type AdoptPlan struct {
	// Adoptable is whether Adopt would write anything. False with no Refusals is
	// the ordinary "nothing to do" host — already managed, or nothing waiting.
	Adoptable bool

	// Why is the sentence for a host with nothing to do, and empty otherwise.
	Why string

	// The four paths involved, named so the report and the writes cannot disagree
	// about which files this is about.
	Unit, Waiting, Backup, DropIn, Record string

	// Relaxations is what the drop-in will carry, empty when the operator's unit
	// relaxes nothing the release's does not.
	Relaxations []Relaxation

	// DropInExists is an override already on the host. It is never overwritten
	// (FR-009): it is the operator's file, and Relaxations has already been
	// checked against what it grants.
	DropInExists bool

	// Dropped is the environment assignments the waiting unit does not make and
	// which change nothing when they go — reported so an operator can see what
	// adoption concluded rather than being asked to trust it.
	Dropped []string

	// ConfigWrites is the settings that must move into the operator's
	// configuration file for the unit's assignment of them to be droppable.
	//
	// **These used to be refusals**, and the refusal said "put that value in your
	// configuration file, then try again". That is the daemon handing an operator
	// a chore it can do itself: it knows the variable, it knows the value, it
	// knows the file, and it already owns an atomic writer that keeps backups.
	// A command whose whole purpose is "make this host adoptable" that stops to
	// dictate homework is a command that has not finished.
	//
	// It is still a refusal where the file *disagrees* — see refuseOnConflict.
	ConfigWrites []ConfigWrite

	// PlaceBinary is where the running executable must be copied for the waiting
	// unit's ExecStart to find one, and is empty when there is already an
	// executable there.
	//
	// Also a refusal until now, and for the same reason it should not have been:
	// the file that needs to be at that path is the one already running this
	// code. The self-update swaps the binary in place at whatever path it was
	// started from, which is right — it updates what is running — and leaves a
	// host whose unit names the other path exactly where this one is.
	PlaceBinary string

	// StaleBinary is the copy left behind by PlaceBinary, or empty.
	//
	// Reported rather than removed. It is on the operator's PATH, possibly ahead
	// of the new one, so after an adoption `crswd` at a prompt and `crswd` under
	// systemd can be two different builds — which is a confusing enough state to
	// be worth naming, and not one this command should resolve by deleting a
	// binary somebody may have put there on purpose.
	StaleBinary string

	// Refusals is why not. Non-empty means Adoptable is false and Adopt writes
	// nothing at all.
	Refusals []Refusal

	// ConfigFile is the file ConfigWrites are written to, and is empty when there
	// are none.
	ConfigFile string

	// contents is the waiting unit's bytes, read during planning so that Adopt
	// installs what was checked rather than re-reading a file that may have
	// changed in between.
	contents []byte

	// binaryFrom is the executable PlaceBinary copies, captured during planning
	// for contents' reason: what is installed is what was checked.
	binaryFrom string
}

// ConfigResolver answers what this daemon would load for an environment variable
// if the unit stopped assigning it — the configuration file's value, or the
// built-in default.
//
// It is an interface rather than a direct call into internal/config — which this
// package already imports — because the question is about the *whole* precedence
// chain, and the place that has already resolved it is cmd/crswd: it has loaded
// the operator's file and knows what the defaults came out as. Reaching back into
// config from here would be a second resolution of the same chain, free to
// disagree with the one the daemon actually runs on.
//
// A test passes a map, which is the other half of why it is a seam.
type ConfigResolver interface {
	// Resolve returns what the daemon would load for name without an environment
	// assignment, and whether it can answer at all. False means the file has no
	// opinion, which is what makes a setting *movable* into it rather than a
	// reason to refuse.
	Resolve(name string) (string, bool)

	// Path is the configuration file those answers came from, and where a setting
	// moving out of the unit is written. Empty means this host has no
	// configuration file to write to, which is a refusal rather than a guess:
	// creating one is the installer's job and would be this command inventing a
	// file the operator never asked for.
	Path() string
}

// PlanAdoption decides whether this host's unit can be brought under management,
// and what that would do.
//
// It writes nothing. An error means the question could not be asked — no home
// directory, or a file that is there and unreadable — and is never a refusal:
// those are values, because a refusal is a fact about the host that the operator
// is meant to read and act on.
func (u *Unit) PlanAdoption(cfg ConfigResolver) (AdoptPlan, error) {
	if u.path == "" || u.record == "" {
		return AdoptPlan{}, ErrNoUnitHome
	}

	plan := AdoptPlan{
		Unit:    u.path,
		Waiting: u.NewPath(),
		Backup:  u.path + backupSuffix,
		DropIn:  u.DropInPath(),
		Record:  u.record,
	}

	waiting, err := os.ReadFile(u.NewPath()) //nolint:gosec // G304: the path is HOME joined with constants this package declares, not anything a request named.
	if errors.Is(err, fs.ErrNotExist) {
		// Not a refusal. There is nothing to adopt because no update has offered
		// anything, which is most hosts most of the time.
		plan.Why = "no newer unit is waiting beside yours, so there is nothing to adopt"
		return plan, nil
	}
	if err != nil {
		return AdoptPlan{}, fmt.Errorf("read the unit a release left waiting %s: %w", u.NewPath(), err)
	}
	plan.contents = waiting

	current, err := os.ReadFile(u.path) //nolint:gosec // G304: as above.
	if errors.Is(err, fs.ErrNotExist) {
		// A waiting unit beside no unit at all. Adoption is not the right verb
		// for it — install.sh places a unit on a host that has none, and doing it
		// here would be a second installer.
		plan.Why = "this host has no unit to adopt; run the installer, which places one"
		return plan, nil
	}
	if err != nil {
		return AdoptPlan{}, fmt.Errorf("read the systemd unit %s: %w", u.path, err)
	}

	// Already managed, whether or not it is current. An update replaces it, which
	// is the state this command exists to reach — so reaching it is not a failure
	// and not work to do.
	recorded, err := u.recorded()
	if err != nil {
		return AdoptPlan{}, err
	}
	if recorded != "" && recorded == unitDigest(current) {
		plan.Why = "your unit is already one this daemon wrote, so updates replace it without this"
		return plan, nil
	}

	currentUnit := parseUnit(current)
	waitingUnit := parseUnit(waiting)

	plan.Relaxations = relaxations(currentUnit, waitingUnit)
	place, from, execRefusals := planExecStart(waitingUnit, u.home())
	plan.PlaceBinary, plan.binaryFrom = place, from
	if place != "" {
		plan.StaleBinary = from
	}
	plan.Refusals = append(plan.Refusals, execRefusals...)
	plan.Refusals = append(plan.Refusals, refuseOnUnexpressibleRelaxation(currentUnit, waitingUnit)...)

	dropped, writes, envRefusals := planEnvironment(currentUnit, waitingUnit, cfg)
	plan.Dropped = dropped
	plan.ConfigWrites = writes
	plan.Refusals = append(plan.Refusals, envRefusals...)
	if len(writes) > 0 {
		if cfg == nil || cfg.Path() == "" {
			plan.Refusals = append(plan.Refusals, Refusal{
				What: "settings in your unit need to move into a configuration file and this host has none",
				Fix:  "create one — deploy/README.md has the shape — then try again",
			})
		} else {
			plan.ConfigFile = cfg.Path()
		}
	}

	// An override the operator already wrote is theirs. What matters is whether it
	// grants what their unit grants — a drop-in that relaxes less would silently
	// harden this host at the moment it was adopted.
	if existing, err := os.ReadFile(u.DropInPath()); err == nil { //nolint:gosec // G304: as above.
		plan.DropInExists = true
		plan.Refusals = append(plan.Refusals, refuseOnWeakerDropIn(parseUnit(existing), plan.Relaxations)...)
	} else if !errors.Is(err, fs.ErrNotExist) {
		return AdoptPlan{}, fmt.Errorf("read the hardening override beside this host's unit %s: %w", u.DropInPath(), err)
	}

	plan.Adoptable = len(plan.Refusals) == 0
	return plan, nil
}

// backupSuffix is what adoption calls the unit it replaced. It is the operator's
// route back, so it is named beside the file it is a copy of rather than
// somewhere this package would prefer.
const backupSuffix = ".adopted.bak"

// home is the directory the unit paths were composed from, recovered so `%h` in
// an ExecStart can be expanded the way systemd expands it.
//
// Derived rather than stored, because storing it would be a second copy of a
// value NewUnit already used and a chance for the two to disagree about which
// home this daemon is running under.
func (u *Unit) home() string {
	return strings.TrimSuffix(u.path, string(filepath.Separator)+unitPath)
}

// relaxations is every drop-in-expressible setting the current unit leaves more
// permissive than the waiting one.
//
// The comparison is of *effective* values — what systemd would apply, defaults
// included — because the relaxation this exists for is a commented-out line.
//
// Only one direction counts. A setting the *release* relaxed and the operator
// hardened is not carried: adoption installs the release's unit, and an operator
// who wants to keep a hardening the release dropped can add it to their own
// drop-in. Carrying it would be this command inventing an override nobody asked
// for out of a difference the release intended.
func relaxations(current, waiting unitFile) []Relaxation {
	var out []Relaxation
	for _, setting := range dropInSettings {
		now, was := effective(current, setting), effective(waiting, setting)
		if now == was {
			continue
		}
		if !isRelaxed(setting, now) {
			continue
		}
		out = append(out, Relaxation{Setting: setting, Value: now})
	}
	return out
}

// effective is a directive's value after systemd's own rules are applied:
// absent means the default, and **a value systemd cannot parse also means the
// default**, because systemd logs a warning and ignores the assignment.
//
// The second half is not a hypothetical. This project's own deployed unit carries
//
//	ProtectSystem=false      # relaxed on this host so /usr is writable
//
// and systemd has no trailing comments — comments are whole lines beginning with
// `#` or `;`. So that directive's value is the entire string including the `#`,
// which is not a boolean, so systemd ignores the line and ProtectSystem is off by
// default. Off is what the operator wanted, and they have been running that way
// for months.
//
// A reader that took the raw string at face value would conclude the setting was
// neither relaxed nor hardened, carry nothing into the drop-in, and install a unit
// that sets ProtectSystem=full — silently making /usr read-only inside every
// session on the host, at the moment its operator ran a command that told them
// nothing would change. That is the exact failure FR-011 exists to prevent,
// arriving through a parse rather than through a missing check.
func effective(u unitFile, setting string) string {
	raw, assigned := u.service(setting)
	if !assigned {
		return systemdDefaults[setting]
	}
	if v, ok := normalise(setting, raw); ok {
		return v
	}
	return systemdDefaults[setting]
}

// normalise turns a directive's written value into the token systemd would act
// on, and reports whether systemd could parse it at all.
//
// The booleans take systemd's full set of spellings. ProtectSystem takes those
// plus `full` and `strict`, which are the two values that are stricter than
// `true` — which is why it cannot be treated as a plain boolean anywhere here.
func normalise(setting, raw string) (string, bool) {
	v := strings.ToLower(strings.TrimSpace(raw))
	switch v {
	case "true", "yes", "on", "1":
		return "true", true
	case "false", "no", "off", "0":
		return "false", true
	}
	if setting == "ProtectSystem" && (v == "full" || v == "strict") {
		return v, true
	}
	return "", false
}

// isRelaxed reports whether a value is the permissive direction of a setting.
//
// Every directive in dropInSettings is a boolean whose false is permissive, and
// ProtectSystem is the one that is not *only* a boolean — `full` and `strict` are
// both stricter than `false`. So the rule is stated as "false is the relaxed
// value" rather than "not true", which would read `full` as a relaxation of
// `true`.
//
// An unparseable value is **not** relaxed by this function. Callers that care
// about what systemd would actually do go through effective, which resolves such
// a value to the default first; this answers only about a value in hand.
func isRelaxed(setting, value string) bool {
	v, ok := normalise(setting, value)
	return ok && v == "false"
}

// planExecStart is FR-010: what has to be true of the binary the waiting unit
// names, and — since milestone 11 — what to do about it rather than only what to
// say.
//
// The release's unit names %h/.local/bin/crswd. The host this feature was written
// for runs %h/bin/crswd, because it predates the installer that chose the other
// path. Installing the waiting unit there produces a service that will not start,
// which is the single most embarrassing failure this command could cause.
//
// A refusal rather than a rewrite: a unit adoption edited is a unit with no
// recorded digest again, which is the whole problem returning through another
// door.
func planExecStart(waiting unitFile, home string) (dest, src string, refusals []Refusal) {
	exec, ok := waiting.service("ExecStart")
	if !ok || strings.TrimSpace(exec) == "" {
		return "", "", []Refusal{{
			What: "the waiting unit names no ExecStart, so this daemon cannot tell what it would run",
			Fix:  "re-run the installer, which writes a complete unit",
		}}
	}

	// The binary is the first token; systemd allows arguments after it, and this
	// project's unit has none. %h is the only specifier used, and it is the home
	// the unit paths were composed from.
	path := strings.Fields(exec)[0]
	path = strings.ReplaceAll(path, "%h", home)
	if strings.HasPrefix(path, "-") || strings.HasPrefix(path, "@") {
		// systemd's prefixes for "ignore failure" and "override argv[0]". This
		// project's unit uses neither, and a unit that did is one this check
		// cannot reason about.
		return "", "", []Refusal{{
			What: "the waiting unit's ExecStart carries a prefix this daemon does not interpret: " + exec,
			Fix:  "adopt it by hand, or re-run the installer",
		}}
	}

	info, err := os.Stat(path)
	if err == nil && !info.IsDir() && info.Mode().Perm()&0o111 != 0 {
		return "", "", nil
	}

	// Nothing usable at that path. This was a refusal until milestone 11, and it
	// should not have been: the file that has to be there is the one already
	// running this code. The self-update swaps the binary in place at whatever
	// path it was started from — right, because it updates what is running — which
	// leaves a host whose unit names the other path exactly here, forever.
	//
	// So the plan is to put it there, and the refusal is kept only for the case
	// where this process cannot say what it is.
	running, err := os.Executable()
	if err != nil {
		return "", "", []Refusal{{
			What: "the waiting unit runs " + path + ", there is no executable there, and this process cannot tell where its own binary is",
			Fix:  "re-run the installer, which puts the binary at that path, then try again",
		}}
	}
	if running == path {
		// It is running from the path it says is empty, which means the stat above
		// failed for a reason other than absence — a permission, a broken mount.
		// Copying a file onto itself would not fix that and might destroy it.
		return "", "", []Refusal{{
			What: "the waiting unit runs " + path + ", which is where this daemon is running from, and this daemon cannot read it",
			Fix:  "check the permissions on " + path + ", then try again",
		}}
	}
	return path, running, nil
}

// refuseOnUnexpressibleRelaxation is FR-011, and it is what makes "this grants
// nothing" a checked claim rather than an intention.
//
// Every [Service] directive where the operator's unit differs from the release's
// is examined. One the drop-in can express becomes a Relaxation; one it cannot,
// and where the operator's value is the more permissive, is a refusal — because
// adopting would install a unit that hardens something the operator had
// deliberately loosened, and they would find out when whatever they loosened it
// for stopped working.
//
// A difference where the *operator* is stricter is left alone and not refused.
// The release's unit is what is being installed, and an operator who wants their
// stricter value keeps it by writing their own drop-in — which this command
// leaves untouched.
func refuseOnUnexpressibleRelaxation(current, waiting unitFile) []Refusal {
	expressible := make(map[string]bool, len(dropInSettings))
	for _, s := range dropInSettings {
		expressible[s] = true
	}

	var out []Refusal
	for key, mine := range current.sections[serviceSection] {
		if expressible[key] || key == "ExecStart" {
			continue
		}
		theirs, assigned := waiting.service(key)
		if assigned && strings.EqualFold(strings.TrimSpace(theirs), strings.TrimSpace(mine)) {
			continue
		}
		// A directive the release does not assign at all, or assigns differently.
		// Only the permissive direction refuses: `X=false` where the release says
		// `X=true` is a relaxation this file has no line for.
		//
		// Compared as systemd would read them, so a value it could not parse is
		// judged as the default it falls back to rather than as the string that
		// was written — effective's reason, applied to the directives this file
		// has no table for.
		if !isRelaxed(key, mine) {
			continue
		}
		if assigned && isRelaxed(key, theirs) {
			// The release relaxed it too. Nothing is being taken away.
			continue
		}
		out = append(out, Refusal{
			What: "your unit sets " + key + "=" + mine + " and the drop-in this command writes has no line for it, so adopting would harden it",
			Fix:  "add " + key + "=" + mine + " to " + DropInName + " yourself first, then try again",
		})
	}
	return out
}

// refuseOnWeakerDropIn is FR-009's other half: an existing override is never
// overwritten, so it must already grant what the operator's unit grants.
//
// Without this, a host with a drop-in that relaxes two of the four settings would
// be adopted into a state where the other two are hardened — silently, at the
// moment the operator ran a command described as changing nothing.
func refuseOnWeakerDropIn(existing unitFile, needed []Relaxation) []Refusal {
	var out []Refusal
	for _, r := range needed {
		got, ok := existing.service(r.Setting)
		if ok && strings.EqualFold(strings.TrimSpace(got), r.Value) {
			continue
		}
		out = append(out, Refusal{
			What: "your unit relaxes " + r.Setting + " and the override already beside it does not, and this command will not rewrite an override you wrote",
			Fix:  "add " + r.Setting + "=" + r.Value + " to " + DropInName + ", then try again",
		})
	}
	return out
}

// planEnvironment is FR-012: which of the current unit's environment assignments
// the waiting unit does not make, and whether losing each one changes anything.
//
// The waiting unit comments these out deliberately (#137): an assignment here
// beats the same key in the operator's configuration file, silently, and
// `allowed_roots` was the case that mattered. So dropping them is the *fix* —
// but only where the daemon would load the same value without them, which is a
// question internal/config can answer and this command must ask rather than
// assume.
//
// An empty assignment is always droppable and it is not a special case: config's
// own precedence treats an empty environment value as unset, so the daemon
// already loads that key from the file.
func planEnvironment(current, waiting unitFile, cfg ConfigResolver) ([]string, []ConfigWrite, []Refusal) {
	theirs := waiting.environment()

	var dropped []string
	var writes []ConfigWrite
	var refusals []Refusal

	// Sorted, because a plan an operator reads twice should read the same twice,
	// and a map range does not.
	for _, name := range sortedKeys(current.environment()) {
		mine := current.environment()[name]
		if _, assigned := theirs[name]; assigned {
			continue
		}
		if strings.TrimSpace(mine) == "" {
			dropped = append(dropped, name)
			continue
		}
		if cfg == nil {
			refusals = append(refusals, Refusal{
				What: "your unit sets " + name + " and this daemon could not work out what it would load without it",
				Fix:  "put that value in your configuration file, remove the line from your unit, and try again",
			})
			continue
		}

		switch would, ok := cfg.Resolve(name); {
		case ok && would == mine:
			// The file already produces this value, so the unit's line has been
			// doing nothing. Dropping it is the fix #137 was about.
			dropped = append(dropped, name)

		case ok:
			// The file says something *else*. The daemon has been running on the
			// unit's value, because an environment assignment beats a file — so
			// the two are in genuine conflict and which one the operator meant is
			// not this command's to guess. Writing the unit's value would silently
			// overwrite what they wrote in the file; writing neither would change
			// what the host does. Both values are named so they can decide.
			refusals = append(refusals, Refusal{
				What: "your unit sets " + name + " to " + mine + " and your configuration file sets it to " + would + "; the unit has been winning, and this daemon will not choose between them for you",
				Fix:  "make the two agree — put " + mine + " in your configuration file to keep what this host does now — then try again",
			})

		default:
			// The file has no opinion, so the unit's value can move into it with
			// nothing overwritten and nothing changed.
			writes = append(writes, ConfigWrite{Var: name, Key: config.KeyForVar(name), Value: mine})
		}
	}
	return dropped, writes, refusals
}

// sortedKeys is a map's keys in order, so that a plan reads the same way twice.
func sortedKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Adopt performs the plan, and decides nothing.
//
// A plan that is not adoptable writes nothing at all (FR-014). That is checked
// here rather than trusted from the caller, because the caller that skips the
// check is the one somebody adds next year.
//
// The order is deliberate. The drop-in first, so a host that fails midway is one
// whose *old* unit is still installed under the hardening it expects. The backup
// before the unit it is a copy of, for MigrateFile's reason: the ending where one
// write fails is the ending where the operator still has both copies. The record
// last, because a record written before the unit is a claim about a file that is
// not there yet — and a claim that would make the *next* update replace a unit
// this one never installed.
func (u *Unit) Adopt(plan AdoptPlan) error {
	if !plan.Adoptable {
		return errors.New("this host cannot be adopted; nothing was written")
	}
	if len(plan.contents) == 0 {
		return errors.New("the plan carries no unit to install; nothing was written")
	}

	// The configuration file first, and before anything about the unit, because
	// it is the only step whose *absence* would change what the daemon loads. A
	// host that failed after this one is a host running its old unit against a
	// file that now states what that unit was already assigning — which is the
	// same daemon, described in one more place.
	if len(plan.ConfigWrites) > 0 {
		if err := appendSettings(plan.ConfigFile, plan.ConfigWrites); err != nil {
			return err
		}
	}

	// Then the binary, because a unit is what points at one: a host that failed
	// after this step has a spare executable and its old unit, which is nothing.
	// The other order leaves a unit naming a binary that is not there.
	if plan.PlaceBinary != "" {
		if err := copyExecutable(plan.binaryFrom, plan.PlaceBinary); err != nil {
			return err
		}
	}

	if len(plan.Relaxations) > 0 && !plan.DropInExists {
		if err := os.MkdirAll(u.DropInDir(), 0o700); err != nil {
			return fmt.Errorf("create the drop-in directory %s: %w", u.DropInDir(), err)
		}
		if err := config.WriteFile(u.DropInPath(), dropInFor(plan.Relaxations), 0o644); err != nil {
			return fmt.Errorf("write the hardening override %s: %w", u.DropInPath(), err)
		}
	}

	current, err := os.ReadFile(u.path) //nolint:gosec // G304: the path is HOME joined with a constant this package declares.
	if err != nil {
		return fmt.Errorf("read the unit being replaced %s: %w", u.path, err)
	}
	if err := config.WriteFile(plan.Backup, current, 0o644); err != nil {
		return fmt.Errorf("keep a copy of the unit being replaced %s: %w", plan.Backup, err)
	}

	if err := config.WriteFile(u.path, plan.contents, 0o644); err != nil {
		return fmt.Errorf("install the waiting unit as %s: %w", u.path, err)
	}

	if err := os.MkdirAll(filepath.Dir(u.record), 0o700); err != nil {
		return fmt.Errorf("create the directory for the unit record %s: %w", u.record, err)
	}
	if err := config.WriteFile(u.record, []byte(unitDigest(plan.contents)+"\n"), 0o600); err != nil {
		return fmt.Errorf("record the unit this daemon now owns %s: %w", u.record, err)
	}

	// The offer has been taken, so the file that represented it is gone. Left
	// behind it would make the next Report say a newer unit is waiting when the
	// operator is running it — which is the false claim this whole area exists to
	// stop making.
	if err := os.Remove(u.NewPath()); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("remove the offer now that it has been taken %s: %w", u.NewPath(), err)
	}
	return nil
}

// appendSettings adds the settings moving out of the unit to the operator's
// configuration file, keeping what it replaced.
//
// **Appended, never rewritten.** The file is the operator's — their comments,
// their ordering, their explanations of why a bound is what it is, which is the
// reason this format is not JSON. A writer that reformatted it would take away
// more than it added, which is the ruling internal/config's migration already
// makes about the same file.
//
// Every value comes from the unit that has been assigning it, so what the daemon
// loads after this is what it loaded before: the setting is stated in one file
// instead of another. That is the whole claim, and it is why planEnvironment
// refuses rather than writes when the file already says something different.
func appendSettings(path string, writes []ConfigWrite) error {
	current, err := os.ReadFile(path) //nolint:gosec // G304: the path is the configuration file this daemon itself resolves.
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("read the configuration file %s: %w", path, err)
	}
	if err == nil {
		// The copy an operator goes back to, beside the file it is a copy of and
		// named the way this command names its other one.
		if err := config.WriteFile(path+backupSuffix, current, 0o600); err != nil {
			return fmt.Errorf("keep a copy of the configuration file %s: %w", path+backupSuffix, err)
		}
	}

	var b strings.Builder
	b.Write(current)
	if len(current) > 0 && !strings.HasSuffix(string(current), "\n") {
		b.WriteString("\n")
	}
	b.WriteString("\n# Moved here from this host's systemd unit by `crswd unit adopt`.\n")
	b.WriteString("# The unit was assigning these, which silently beat this file; stating them\n")
	b.WriteString("# here is what lets the unit be replaced without changing what this daemon\n")
	b.WriteString("# loads. The values are the ones that were already in effect.\n")
	for _, w := range writes {
		b.WriteString(w.Key + " = " + w.Value + "\n")
	}

	// 0600, matching what the daemon requires of a file that may hold a secret,
	// and what install.sh writes. A file created here must not be more readable
	// than one created there.
	if err := config.WriteFile(path, []byte(b.String()), 0o600); err != nil {
		return fmt.Errorf("write the settings moving out of your unit into %s: %w", path, err)
	}
	return nil
}

// copyExecutable puts the running binary where the waiting unit expects to find
// one.
//
// It copies rather than moves, and leaves the original where it is. The original
// is on the operator's PATH — possibly ahead of the new location — so removing it
// would change what `crswd` means at their prompt, which is a bigger decision
// than this command is entitled to take. The plan reports it as stale instead.
//
// Written through the same atomic writer as everything else here, so a failure
// mid-copy cannot leave a truncated executable at a path systemd is about to run.
func copyExecutable(from, to string) error {
	data, err := os.ReadFile(from) //nolint:gosec // G304: the path is this process's own executable, from os.Executable.
	if err != nil {
		return fmt.Errorf("read this daemon's own binary %s: %w", from, err)
	}
	if err := os.MkdirAll(filepath.Dir(to), 0o700); err != nil {
		return fmt.Errorf("create the directory the waiting unit runs from %s: %w", filepath.Dir(to), err)
	}
	if err := config.WriteFile(to, data, 0o755); err != nil { //nolint:gosec // G302: it is an executable, and the unit runs it.
		return fmt.Errorf("place this daemon's binary where the waiting unit runs it %s: %w", to, err)
	}
	return nil
}

// dropInFor writes the override for a set of relaxations.
//
// **Every line comes from the operator's own unit.** The file says so, because an
// operator reading it a year from now needs to know it was derived rather than
// chosen — and because the sentence is the honest description of what this
// command did: it moved their decision, it did not make one.
//
// It is deliberately not the example file's text. That one is written for
// somebody answering install.sh's question and explains a decision they are about
// to take; this one records a decision already taken, somewhere else, by them.
func dropInFor(relaxations []Relaxation) []byte {
	var b strings.Builder
	b.WriteString("# Written by `crswd unit adopt`.\n")
	b.WriteString("#\n")
	b.WriteString("# Every setting below was already in effect on this host: it came from your\n")
	b.WriteString("# own systemd unit, which this command replaced with the one the release\n")
	b.WriteString("# ships. Moving them here is what lets that unit be updated from now on —\n")
	b.WriteString("# systemd merges this file over it, and nothing in this project touches\n")
	b.WriteString("# this directory.\n")
	b.WriteString("#\n")
	b.WriteString("# Nothing here was granted by that command. It relocated what you had\n")
	b.WriteString("# already decided.\n")
	b.WriteString("#\n")
	b.WriteString("# What these grant, stated plainly: a path from an authenticated request to\n")
	b.WriteString("# ROOT on this host, not just to your account. `allowed_roots` does not\n")
	b.WriteString("# bound it — that bounds which directory a session starts in, and a root\n")
	b.WriteString("# shell is not bounded by its working directory.\n")
	b.WriteString("#\n")
	b.WriteString("# Delete this file, then `systemctl --user daemon-reload && systemctl --user\n")
	b.WriteString("# restart crswd`, to give the privilege back.\n")
	b.WriteString("\n[Service]\n")
	for _, r := range relaxations {
		b.WriteString(r.Setting + "=" + r.Value + "\n")
	}
	return []byte(b.String())
}
