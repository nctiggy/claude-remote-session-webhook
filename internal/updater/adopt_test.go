package updater

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A commented directive is absent, and absent is systemd's default rather than
// "no opinion". That reading is the whole feature: the host this was written for
// relaxes its hardening by commenting the lines out, so a parser that treated a
// comment as anything else would find no relaxation on the one host that has one.
func TestParseUnitReadsCommentsAsAbsence(t *testing.T) {
	t.Parallel()

	u := parseUnit([]byte(`
[Service]
# NoNewPrivileges=true   # relaxed on this host so sudo works in a session
RestrictSUIDSGID=true
; ProtectKernelTunables=true
ProtectSystem=false
`))

	if _, ok := u.service("NoNewPrivileges"); ok {
		t.Error("a commented directive was read as assigned")
	}
	if _, ok := u.service("ProtectKernelTunables"); ok {
		t.Error("a directive commented with ';' was read as assigned")
	}
	if got, ok := u.service("RestrictSUIDSGID"); !ok || got != "true" {
		t.Errorf("RestrictSUIDSGID = %q, %v; want the assigned value", got, ok)
	}
	if got := effective(u, "NoNewPrivileges"); got != "false" {
		t.Errorf("effective(NoNewPrivileges) = %q; an absent hardening directive is systemd's default, which is off", got)
	}
}

// systemd takes the last assignment of a non-list directive, and a directive in
// another section is a different setting entirely. Reading `[Unit]`'s value as
// `[Service]`'s would find a relaxation where there is none.
func TestParseUnitTakesTheLastValueInTheRightSection(t *testing.T) {
	t.Parallel()

	u := parseUnit([]byte(`
[Unit]
ProtectSystem=strict

[Service]
ProtectSystem=full
ProtectSystem=false
`))

	if got, _ := u.service("ProtectSystem"); got != "false" {
		t.Errorf("ProtectSystem = %q; want the last assignment in [Service]", got)
	}
}

// Environment= is the one directive here that legitimately repeats, so it is kept
// as a list. A map keyed by directive name would hold the last line only, and
// every earlier variable would silently vanish from the comparison FR-012 makes.
func TestParseUnitKeepsEveryEnvironmentLine(t *testing.T) {
	t.Parallel()

	u := parseUnit([]byte(`
[Service]
Environment=CRSW_LISTEN=127.0.0.1:8765
Environment=CRSW_MAX_SESSIONS=5
Environment="CRSW_START_COMMANDS="
Environment=CRSW_DISCOVER_ROOTS=
`))

	env := u.environment()
	for name, want := range map[string]string{
		"CRSW_LISTEN":         "127.0.0.1:8765",
		"CRSW_MAX_SESSIONS":   "5",
		"CRSW_START_COMMANDS": "",
		"CRSW_DISCOVER_ROOTS": "",
	} {
		got, ok := env[name]
		if !ok {
			t.Errorf("%s is missing from the parsed environment: %v", name, env)
			continue
		}
		if got != want {
			t.Errorf("%s = %q, want %q", name, got, want)
		}
	}
	if len(env) != 4 {
		t.Errorf("the parsed environment holds %d names, want 4: %v", len(env), env)
	}
}

// The drop-in this command writes and the one install.sh writes must grant the
// same set. If they disagree, adoption either refuses a host it could have taken
// or reports having reproduced a setting the drop-in does not carry — and the
// second is a host silently hardened at the moment its operator was told nothing
// changed.
func TestTheDropInExpressesExactlyTheseSettings(t *testing.T) {
	t.Parallel()

	example, err := os.ReadFile(filepath.Join("..", "..", "deploy", "crswd.service.d", "10-relax.conf.example"))
	if err != nil {
		t.Fatalf("read the documented drop-in: %v", err)
	}
	documented := parseUnit(example)

	for _, setting := range dropInSettings {
		got, ok := documented.service(setting)
		if !ok {
			t.Errorf("this command can express %s and the documented drop-in does not set it", setting)
			continue
		}
		if !isRelaxed(setting, got) {
			t.Errorf("the documented drop-in sets %s=%s, which is not the relaxed direction this command assumes", setting, got)
		}
	}

	for setting := range documented.sections[serviceSection] {
		var known bool
		for _, s := range dropInSettings {
			if s == setting {
				known = true
			}
		}
		if !known {
			t.Errorf("the documented drop-in sets %s and this command would not reproduce it, so an adopted host loses it", setting)
		}
	}
	if len(systemdDefaults) != len(dropInSettings) {
		t.Errorf("%d settings can be expressed and %d have a recorded default; a setting with no default is one whose absence cannot be read", len(dropInSettings), len(systemdDefaults))
	}
}

// adoptFixture is a host: a unit, optionally an offer beside it, optionally a
// record, and a home the ExecStart check can find a binary under.
type adoptFixture struct {
	home string
	unit *Unit
}

func newAdoptFixture(t *testing.T) adoptFixture {
	t.Helper()

	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".config", "systemd", "user"), 0o700); err != nil {
		t.Fatalf("make the unit directory: %v", err)
	}
	// The binary the shipped unit names, so the ExecStart check passes unless a
	// case is about it.
	bin := filepath.Join(home, ".local", "bin")
	if err := os.MkdirAll(bin, 0o700); err != nil {
		t.Fatalf("make the bin directory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(bin, "crswd"), []byte("#!/bin/sh\n"), 0o755); err != nil { //nolint:gosec // G306: it stands in for an installed binary and must be executable.
		t.Fatalf("plant the binary: %v", err)
	}

	return adoptFixture{
		home: home,
		unit: newUnit(
			filepath.Join(home, unitPath),
			filepath.Join(home, unitRecordPath),
		),
	}
}

func (f adoptFixture) write(t *testing.T, path, contents string) {
	t.Helper()

	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("make %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// The motivating host's own unit: the relaxation is four commented-out lines and
// a ProtectSystem=false, and the ExecStart points at the pre-installer path.
const handEditedUnit = `[Service]
Type=simple
ExecStart=%h/bin/crswd
# NoNewPrivileges=true   # relaxed on this host so sudo works in a session
# RestrictSUIDSGID=true  # relaxed on this host so sudo works in a session
# ProtectKernelTunables=true
ProtectControlGroups=true
ProtectSystem=false
Environment=CRSW_LISTEN=127.0.0.1:8765
Environment=CRSW_MAX_SESSIONS=5
Environment=CRSW_DISCOVER_ROOTS=
`

// The unit a release ships: hardened, and running from the installer's path.
const shippedUnit = `[Service]
Type=simple
ExecStart=%h/.local/bin/crswd
NoNewPrivileges=true
RestrictSUIDSGID=true
ProtectKernelTunables=true
ProtectControlGroups=true
ProtectSystem=full
`

// resolver stands in for what the daemon would load without an environment
// assignment.
type resolver map[string]string

func (r resolver) Resolve(name string) (string, bool) {
	v, ok := r[name]
	return v, ok
}

// The whole feature on the host it was written for: a hand-edited unit, an offer
// waiting, and a configuration file that already carries what the unit assigns.
//
// **Must fail when** the relaxations are not derived from the operator's own
// unit, or the plan proposes writing something on a host it should refuse.
func TestPlanAdoptionCarriesTheOperatorsOwnRelaxations(t *testing.T) {
	t.Parallel()

	f := newAdoptFixture(t)
	f.write(t, f.unit.Path(), handEditedUnit)
	f.write(t, f.unit.NewPath(), shippedUnit)

	plan, err := f.unit.PlanAdoption(resolver{
		"CRSW_LISTEN":       "127.0.0.1:8765",
		"CRSW_MAX_SESSIONS": "5",
	})
	if err != nil {
		t.Fatalf("PlanAdoption: %v", err)
	}
	if !plan.Adoptable {
		t.Fatalf("this host is not adoptable: %+v", plan.Refusals)
	}

	want := map[string]string{
		"NoNewPrivileges":       "false",
		"RestrictSUIDSGID":      "false",
		"ProtectKernelTunables": "false",
		"ProtectSystem":         "false",
	}
	got := map[string]string{}
	for _, r := range plan.Relaxations {
		got[r.Setting] = r.Value
	}
	if len(got) != len(want) {
		t.Fatalf("the plan carries %d relaxations, want %d: %+v", len(got), len(want), plan.Relaxations)
	}
	for setting, value := range want {
		if got[setting] != value {
			t.Errorf("relaxation %s = %q, want %q", setting, got[setting], value)
		}
	}

	// ProtectControlGroups is true in both, so it is not a relaxation and must not
	// be written into an override that would then be claiming to grant something.
	if _, carried := got["ProtectControlGroups"]; carried {
		t.Error("the plan carries ProtectControlGroups, which both units harden")
	}

	// The three environment assignments the shipped unit drops, all of which the
	// configuration file or an empty value covers.
	if len(plan.Dropped) != 3 {
		t.Errorf("the plan drops %v; want the three assignments that change nothing", plan.Dropped)
	}
}

// Each refusal alone, because a test that only ever saw them together would pass
// against a plan that had stopped making one of them.
//
// **Must fail when** a refusal stops firing, or a plan that refuses still
// proposes writes.
func TestPlanAdoptionRefuses(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct {
		unit, waiting, dropIn string
		resolve               resolver
		noBinary              bool
		want                  string
	}{
		"the binary the waiting unit names is not there": {
			unit: handEditedUnit, waiting: shippedUnit,
			resolve:  resolver{"CRSW_LISTEN": "127.0.0.1:8765", "CRSW_MAX_SESSIONS": "5"},
			noBinary: true,
			want:     "no executable there",
		},
		"a relaxation the drop-in cannot express": {
			unit: handEditedUnit + "PrivateTmp=false\n",
			waiting: shippedUnit + `PrivateTmp=true
`,
			resolve: resolver{"CRSW_LISTEN": "127.0.0.1:8765", "CRSW_MAX_SESSIONS": "5"},
			want:    "PrivateTmp",
		},
		"an environment assignment the file does not carry": {
			unit: handEditedUnit, waiting: shippedUnit,
			// CRSW_MAX_SESSIONS is missing, so dropping the unit's line would
			// change what the daemon loads.
			resolve: resolver{"CRSW_LISTEN": "127.0.0.1:8765"},
			want:    "CRSW_MAX_SESSIONS",
		},
		"an environment assignment the file disagrees with": {
			unit: handEditedUnit, waiting: shippedUnit,
			resolve: resolver{"CRSW_LISTEN": "127.0.0.1:8765", "CRSW_MAX_SESSIONS": "9"},
			want:    "CRSW_MAX_SESSIONS",
		},
		"an existing override that grants less": {
			unit: handEditedUnit, waiting: shippedUnit,
			resolve: resolver{"CRSW_LISTEN": "127.0.0.1:8765", "CRSW_MAX_SESSIONS": "5"},
			dropIn: `[Service]
NoNewPrivileges=false
`,
			want: "RestrictSUIDSGID",
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			f := newAdoptFixture(t)
			if tc.noBinary {
				if err := os.Remove(filepath.Join(f.home, ".local", "bin", "crswd")); err != nil {
					t.Fatalf("remove the binary: %v", err)
				}
			}
			f.write(t, f.unit.Path(), tc.unit)
			f.write(t, f.unit.NewPath(), tc.waiting)
			if tc.dropIn != "" {
				f.write(t, f.unit.DropInPath(), tc.dropIn)
			}

			plan, err := f.unit.PlanAdoption(tc.resolve)
			if err != nil {
				t.Fatalf("PlanAdoption: %v", err)
			}
			if plan.Adoptable {
				t.Fatalf("this host was adopted and should have been refused: %+v", plan)
			}
			if len(plan.Refusals) == 0 {
				t.Fatal("the plan is not adoptable and gives no reason, so the operator has nothing to act on")
			}

			var found bool
			for _, r := range plan.Refusals {
				if strings.Contains(r.What, tc.want) {
					found = true
				}
				if strings.TrimSpace(r.Fix) == "" {
					t.Errorf("the refusal %q says nothing about what to do", r.What)
				}
			}
			if !found {
				t.Errorf("no refusal names %q: %+v", tc.want, plan.Refusals)
			}

			// FR-014: a refusal writes nothing, asserted through Adopt rather
			// than by inspecting the host, because Adopt is what a caller with a
			// refused plan would call.
			if err := f.unit.Adopt(plan); err == nil {
				t.Error("Adopt() performed a plan that was refused")
			}
			if _, err := os.Stat(f.unit.DropInPath()); err == nil && tc.dropIn == "" {
				t.Error("a refused plan left a drop-in behind")
			}
			if _, err := os.Stat(plan.Backup); err == nil {
				t.Error("a refused plan left a backup behind, so it had begun writing")
			}
		})
	}
}

// The hosts with nothing to do. None of them is a failure, and each says which
// one it is — an operator told "cannot adopt" about an already-managed host would
// go looking for a problem that is not there.
func TestPlanAdoptionSaysWhenThereIsNothingToDo(t *testing.T) {
	t.Parallel()

	t.Run("no offer waiting", func(t *testing.T) {
		t.Parallel()

		f := newAdoptFixture(t)
		f.write(t, f.unit.Path(), handEditedUnit)

		plan, err := f.unit.PlanAdoption(resolver{})
		if err != nil {
			t.Fatalf("PlanAdoption: %v", err)
		}
		if plan.Adoptable || len(plan.Refusals) != 0 {
			t.Errorf("a host with nothing waiting was reported as %+v", plan)
		}
		if !strings.Contains(plan.Why, "nothing to adopt") {
			t.Errorf("Why = %q; want it to say there is nothing waiting", plan.Why)
		}
	})

	t.Run("already managed", func(t *testing.T) {
		t.Parallel()

		f := newAdoptFixture(t)
		f.write(t, f.unit.Path(), handEditedUnit)
		f.write(t, f.unit.NewPath(), shippedUnit)
		f.write(t, f.unit.RecordPath(), unitDigest([]byte(handEditedUnit))+"\n")

		plan, err := f.unit.PlanAdoption(resolver{})
		if err != nil {
			t.Fatalf("PlanAdoption: %v", err)
		}
		if plan.Adoptable {
			t.Error("a unit this daemon already wrote was proposed for adoption")
		}
		if !strings.Contains(plan.Why, "already") {
			t.Errorf("Why = %q; want it to say the unit is already managed", plan.Why)
		}
	})
}

// The adoption itself, and the property that makes it a relocation: the effective
// hardening after is what it was before.
//
// It is computed from the merged unit and drop-in rather than compared as file
// text, because systemd's merge is the thing being relied on and a test that
// compared files would pass on a drop-in systemd never reads.
//
// **Must fail when** the host's hardening changes, the unit is not the release's,
// or the record does not make it managed.
func TestAdoptRelocatesTheRelaxationAndChangesNothingElse(t *testing.T) {
	t.Parallel()

	f := newAdoptFixture(t)
	f.write(t, f.unit.Path(), handEditedUnit)
	f.write(t, f.unit.NewPath(), shippedUnit)

	before := mergedHardening(parseUnit([]byte(handEditedUnit)), unitFile{})

	plan, err := f.unit.PlanAdoption(resolver{"CRSW_LISTEN": "127.0.0.1:8765", "CRSW_MAX_SESSIONS": "5"})
	if err != nil {
		t.Fatalf("PlanAdoption: %v", err)
	}
	if err := f.unit.Adopt(plan); err != nil {
		t.Fatalf("Adopt: %v", err)
	}

	installed, err := os.ReadFile(f.unit.Path())
	if err != nil {
		t.Fatalf("read the installed unit: %v", err)
	}
	if string(installed) != shippedUnit {
		t.Errorf("the installed unit is not the one that was waiting:\n%s", installed)
	}

	dropIn, err := os.ReadFile(f.unit.DropInPath())
	if err != nil {
		t.Fatalf("read the drop-in: %v", err)
	}
	after := mergedHardening(parseUnit(installed), parseUnit(dropIn))

	for setting, was := range before {
		if after[setting] != was {
			t.Errorf("%s was %q before the adoption and is %q after; this command relocates a relaxation and grants nothing",
				setting, was, after[setting])
		}
	}

	// Managed from now on, which is the whole point: the next release replaces
	// this unit without the operator touching a file.
	standing, err := f.unit.Standing([]byte("a later release's unit\n"))
	if err != nil {
		t.Fatalf("Standing: %v", err)
	}
	if standing != UnitOurs {
		t.Errorf("after adoption the unit stands as %v; want it to be this daemon's to replace", standing)
	}

	// The route back, and the offer gone now that it has been taken.
	backup, err := os.ReadFile(plan.Backup)
	if err != nil {
		t.Fatalf("read the backup: %v", err)
	}
	if string(backup) != handEditedUnit {
		t.Error("the backup is not the unit that was replaced")
	}
	if _, err := os.Stat(f.unit.NewPath()); err == nil {
		t.Error("the offer is still waiting after being taken, so the next report claims a newer unit exists")
	}
}

// An override the operator wrote is theirs, and adoption reads it rather than
// replacing it — even when it grants exactly what would have been written.
func TestAdoptLeavesAnExistingOverrideAlone(t *testing.T) {
	t.Parallel()

	const theirs = `# mine
[Service]
NoNewPrivileges=false
RestrictSUIDSGID=false
ProtectKernelTunables=false
ProtectSystem=false
`

	f := newAdoptFixture(t)
	f.write(t, f.unit.Path(), handEditedUnit)
	f.write(t, f.unit.NewPath(), shippedUnit)
	f.write(t, f.unit.DropInPath(), theirs)

	plan, err := f.unit.PlanAdoption(resolver{"CRSW_LISTEN": "127.0.0.1:8765", "CRSW_MAX_SESSIONS": "5"})
	if err != nil {
		t.Fatalf("PlanAdoption: %v", err)
	}
	if !plan.Adoptable {
		t.Fatalf("an override granting everything needed was refused: %+v", plan.Refusals)
	}
	if err := f.unit.Adopt(plan); err != nil {
		t.Fatalf("Adopt: %v", err)
	}

	got, err := os.ReadFile(f.unit.DropInPath())
	if err != nil {
		t.Fatalf("read the drop-in: %v", err)
	}
	if string(got) != theirs {
		t.Errorf("the operator's own override was rewritten:\n%s", got)
	}
}

// mergedHardening is systemd's merge for the settings this feature reasons about:
// the unit, then the drop-in over it, then defaults for what neither assigns.
func mergedHardening(unit, dropIn unitFile) map[string]string {
	out := make(map[string]string, len(dropInSettings))
	for _, setting := range dropInSettings {
		if v, ok := dropIn.service(setting); ok {
			out[setting] = strings.ToLower(v)
			continue
		}
		out[setting] = effective(unit, setting)
	}
	return out
}

// The case that catches a drop-in written from a constant rather than derived.
//
// The motivating host relaxes all four settings, so a `dropInFor` that ignored
// its argument and wrote the whole table would pass every test above it. This one
// relaxes exactly one, and the adopted host must end up with exactly one relaxed
// — because the difference between those two implementations is the difference
// between relocating a privilege and granting three.
//
// **Must fail when** the drop-in carries a setting the operator's unit did not
// already relax.
func TestAdoptGrantsOnlyWhatTheUnitAlreadyGranted(t *testing.T) {
	t.Parallel()

	// Hardened everywhere except ProtectSystem, which is the one thing this
	// operator loosened.
	const oneRelaxation = `[Service]
Type=simple
ExecStart=%h/.local/bin/crswd
NoNewPrivileges=true
RestrictSUIDSGID=true
ProtectKernelTunables=true
ProtectSystem=false
`

	f := newAdoptFixture(t)
	f.write(t, f.unit.Path(), oneRelaxation)
	f.write(t, f.unit.NewPath(), shippedUnit)

	plan, err := f.unit.PlanAdoption(resolver{})
	if err != nil {
		t.Fatalf("PlanAdoption: %v", err)
	}
	if !plan.Adoptable {
		t.Fatalf("refused: %+v", plan.Refusals)
	}
	if len(plan.Relaxations) != 1 || plan.Relaxations[0].Setting != "ProtectSystem" {
		t.Fatalf("the plan carries %+v; this unit relaxes ProtectSystem and nothing else", plan.Relaxations)
	}

	if err := f.unit.Adopt(plan); err != nil {
		t.Fatalf("Adopt: %v", err)
	}

	dropIn, err := os.ReadFile(f.unit.DropInPath())
	if err != nil {
		t.Fatalf("read the drop-in: %v", err)
	}
	written := parseUnit(dropIn)
	for _, setting := range dropInSettings {
		_, carried := written.service(setting)
		if setting == "ProtectSystem" {
			if !carried {
				t.Errorf("the drop-in does not carry %s, which this host had relaxed:\n%s", setting, dropIn)
			}
			continue
		}
		if carried {
			t.Errorf("the drop-in relaxes %s, which this host's unit hardened — that is a grant, not a relocation:\n%s", setting, dropIn)
		}
	}

	// And the merged result is what it was: three hardened, one not.
	installed, err := os.ReadFile(f.unit.Path())
	if err != nil {
		t.Fatalf("read the installed unit: %v", err)
	}
	after := mergedHardening(parseUnit(installed), written)
	for setting, want := range map[string]string{
		"NoNewPrivileges":       "true",
		"RestrictSUIDSGID":      "true",
		"ProtectKernelTunables": "true",
		"ProtectSystem":         "false",
	} {
		if after[setting] != want {
			t.Errorf("after adoption %s = %q, want %q", setting, after[setting], want)
		}
	}
}

// systemd has no trailing comments, and this is the case that proves the reader
// knows it.
//
// The deployed unit this feature was written for carries, verbatim:
//
//	ProtectSystem=false      # relaxed on this host so /usr is writable
//
// Comments in a unit are whole lines beginning with `#` or `;`, so that
// directive's value is the entire string including the `#`. systemd cannot parse
// it as a boolean, logs a warning, ignores the line, and ProtectSystem is off by
// its default — which is what the operator wanted and how the host has been
// running.
//
// A reader that took the raw string at face value would find the setting neither
// relaxed nor hardened, carry nothing into the drop-in, and install a unit
// setting ProtectSystem=full. /usr goes read-only inside every session on the
// host, at the moment its operator ran a command that told them nothing would
// change.
//
// **Must fail when** an unparseable value stops resolving to systemd's default.
func TestAValueSystemdCannotParseIsItsDefault(t *testing.T) {
	t.Parallel()

	withTrailingComment := parseUnit([]byte(`[Service]
ProtectSystem=false      # relaxed on this host so /usr is writable
NoNewPrivileges=true     # this one is not a comment either
`))

	if got := effective(withTrailingComment, "ProtectSystem"); got != "false" {
		t.Errorf("ProtectSystem = %q; systemd cannot parse that value, so it is off by default", got)
	}
	// The same rule in the other direction, and the more alarming half: a value
	// that *looks* hardened but is unparseable is not hardening this host, and a
	// reader that believed it would leave a real relaxation uncarried.
	if got := effective(withTrailingComment, "NoNewPrivileges"); got != "false" {
		t.Errorf("NoNewPrivileges = %q; an unparseable value is ignored, so the default stands", got)
	}

	// End to end: the whole unit, as deployed, must yield all four relaxations.
	const asDeployed = `[Service]
Type=simple
ExecStart=%h/.local/bin/crswd
# NoNewPrivileges=true   # relaxed on this host so sudo works in a session
# RestrictSUIDSGID=true  # relaxed on this host so sudo works in a session
# ProtectKernelTunables=true
ProtectControlGroups=true
ProtectSystem=false      # relaxed on this host so /usr is writable
`

	f := newAdoptFixture(t)
	f.write(t, f.unit.Path(), asDeployed)
	f.write(t, f.unit.NewPath(), shippedUnit)

	plan, err := f.unit.PlanAdoption(resolver{})
	if err != nil {
		t.Fatalf("PlanAdoption: %v", err)
	}
	if !plan.Adoptable {
		t.Fatalf("refused: %+v", plan.Refusals)
	}
	if len(plan.Relaxations) != len(dropInSettings) {
		t.Fatalf("the plan carries %+v; this unit relaxes all four", plan.Relaxations)
	}

	// And the drop-in it would write is one systemd can read, which the operator's
	// own line was not.
	for _, r := range plan.Relaxations {
		if _, ok := normalise(r.Setting, r.Value); !ok {
			t.Errorf("the drop-in would carry %s=%s, which systemd cannot parse", r.Setting, r.Value)
		}
	}
}
