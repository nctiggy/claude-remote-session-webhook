package config_test

// The example systemd unit is the only place an operator sees the daemon's whole
// configuration laid out at once, and it drifted from config.go for forty
// iterations without anything noticing: it set CRSW_SESSION_CAP, CRSW_IDLE_TIMEOUT
// and CRSW_ABSOLUTE_LIFETIME, none of which is read, and passed a --listen flag to
// a binary that defines no flags. Both failures are silent in the worst direction
// — the operator who sets a cap of 8 runs with 5 and is never told — so they are
// checked here rather than left to review (Constitution Principle V).
//
// deploy/README.md is checked alongside it for the same reason: it is the
// procedure an operator follows once, from a shell, and its recipe is the only
// place that says which values must exist before the daemon will start.
//
// declaredVars lives in envexample_test.go and parses config.go, so a variable
// renamed there fails this file too.

import (
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/nctiggy/claude-remote-session-webhook/internal/config"
)

const (
	unitPath        = "../../deploy/crswd.example.service"
	cloudflaredPath = "../../deploy/cloudflared.example.yml"
	readmePath      = "../../deploy/README.md"
	rootReadmePath  = "../../README.md"

	// envFilePath is the file the README's recipe writes and the unit's
	// EnvironmentFile reads. It identifies the recipe among the README's other
	// shell blocks.
	envFilePath = "/.config/crswd/env"
)

// unitSettings returns the unit file's directives, comments dropped. A systemd
// directive is Key=Value and a comment line is not one, which is what keeps the
// block of deliberately-absent options at the foot of the file from reading as
// settings that are present.
func unitSettings(t *testing.T) map[string][]string {
	t.Helper()

	raw, err := os.ReadFile(unitPath)
	if err != nil {
		t.Fatalf("read %s: %v", unitPath, err)
	}

	settings := make(map[string][]string)
	for _, line := range strings.Split(string(raw), "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, "[") {
			continue
		}
		key, value, found := strings.Cut(trimmed, "=")
		if !found {
			t.Fatalf("%s: %q is neither a comment, a section header, nor a Key=Value directive", unitPath, trimmed)
		}
		settings[key] = append(settings[key], value)
	}

	if len(settings) == 0 {
		t.Fatalf("found no directives in %s; this test is not checking anything", unitPath)
	}
	return settings
}

// unitEnvironment returns the CRSW_ variables the unit sets inline, by name.
func unitEnvironment(t *testing.T) map[string]string {
	t.Helper()

	env := make(map[string]string)
	for _, assignment := range unitSettings(t)["Environment"] {
		// systemd's own quoting, undone here: an unquoted Environment= ends at
		// the first space, so a value with a space in it — a start command line —
		// can only be written `Environment="NAME=a b"`. Reading past the quotes is
		// what keeps such a variable visible to the checks below rather than
		// looking like a name nothing declares.
		assignment = strings.TrimSuffix(strings.TrimPrefix(assignment, `"`), `"`)

		name, value, found := strings.Cut(assignment, "=")
		if !found {
			t.Fatalf("%s: Environment=%q is not an assignment", unitPath, assignment)
		}
		if _, dup := env[name]; dup {
			t.Errorf("%s sets %s twice; systemd takes the last, which is not what reading the file suggests", unitPath, name)
		}
		env[name] = value
	}
	return env
}

// readmeEnvFileVars returns the CRSW_ variables deploy/README.md's shell recipe
// writes into the environment file. Only the fenced blocks naming that file are
// read: the README also names variables in prose and in a table, and a variable
// an operator is told about but never shown writing is exactly the gap below.
func readmeEnvFileVars(t *testing.T) map[string]bool {
	t.Helper()

	raw, err := os.ReadFile(readmePath)
	if err != nil {
		t.Fatalf("read %s: %v", readmePath, err)
	}

	assignment := regexp.MustCompile("(" + envPrefix + "[A-Z0-9_]+)=")
	vars := make(map[string]bool)
	// Splitting on the fence marker puts block contents at the odd indices; the
	// prose before the first fence is index 0.
	for i, segment := range strings.Split(string(raw), "```") {
		if i%2 == 0 || !strings.Contains(segment, envFilePath) {
			continue
		}
		for _, match := range assignment.FindAllStringSubmatch(segment, -1) {
			vars[match[1]] = true
		}
	}

	if len(vars) == 0 {
		t.Fatalf("%s has no %s assignment in a block writing %s; this test is not checking anything", readmePath, envPrefix, envFilePath)
	}
	return vars
}

// TestDeployREADMERecipeStartsTheDaemon holds the deployment procedure to the
// configuration the daemon actually demands. The recipe wrote CRSW_SHARED_SECRET
// alone for twenty iterations after the three layer-1 values became required, and
// an operator following it got a daemon that refused to start with nothing in the
// file it was being followed from to explain why (Constitution Principle V).
func TestDeployREADMERecipeStartsTheDaemon(t *testing.T) {
	t.Parallel()

	recipe := readmeEnvFileVars(t)
	declared := declaredVars(t)
	pairs, _ := baseEnv(t)

	// The environment an operator following the README ends up with is the
	// recipe's assignments and nothing else. The unit's inline values are left
	// out because TestUnitInlineValuesAreTheDaemonDefaults pins each to the
	// daemon's own default, so setting them changes nothing — except
	// CRSW_ALLOWED_ROOTS, whose default is $HOME/code and whose unit spelling is
	// systemd's %h, which is not a path anything outside systemd can resolve.
	home := t.TempDir()
	if err := os.Mkdir(filepath.Join(home, config.DefaultRootName), 0o750); err != nil {
		t.Fatalf("mkdir default root: %v", err)
	}
	following := map[string]string{"HOME": home}

	for name := range recipe {
		if !declared[name] {
			t.Errorf("%s writes %s into the environment file and nothing in config.go reads it", readmePath, name)
			continue
		}
		value, ok := pairs[name]
		if !ok {
			t.Fatalf("%s writes %s and this test has no valid sample value for it; add one to baseEnv", readmePath, name)
		}
		following[name] = value
	}

	if _, err := config.LoadFrom(env(following), io.Discard); err != nil {
		t.Errorf("an operator who follows %s gets a daemon that refuses to start: %v", readmePath, err)
	}
}

// deploymentSpecific are the variables whose values may never be committed, so
// the unit names them in a comment and they arrive through the EnvironmentFile
// instead. The secret is the obvious one; the three layer-1 values name a
// Cloudflare team, an Access application, and a person, and a unit's contents
// are readable by anyone who can run `systemctl --user show`.
//
// Being on this list is not an exemption from the rule below — it is the other
// half of it. The unit must still mention each of these by name, so the file
// stays the one place an operator can see the whole configuration at once.
func deploymentSpecific() map[string]bool {
	return map[string]bool{
		config.EnvSharedSecret:        true,
		config.EnvAccessTeamDomain:    true,
		config.EnvAccessAUD:           true,
		config.EnvAccessAllowedEmails: true,
	}
}

// TestUnitSetsOnlyVariablesTheDaemonReads is the silent-wrong guard. A variable
// nothing reads leaves the operator believing they configured something.
func TestUnitSetsOnlyVariablesTheDaemonReads(t *testing.T) {
	t.Parallel()

	declared := declaredVars(t)
	set := unitEnvironment(t)
	private := deploymentSpecific()

	for name := range set {
		if !declared[name] {
			t.Errorf("%s sets %s and nothing in config.go reads it, so the operator who sets it runs on the default and is never told", unitPath, name)
		}
	}

	// The other direction, because the unit claims inline to be the complete list.
	raw, err := os.ReadFile(unitPath)
	if err != nil {
		t.Fatalf("read %s: %v", unitPath, err)
	}
	for name := range declared {
		if private[name] {
			// Not inline — TestUnitNeverCarriesADeploymentValue — but the operator
			// still has to learn from this file that it exists and is required.
			if !strings.Contains(string(raw), name) {
				t.Errorf("config.go reads %s, and %s neither sets it nor names it, so an operator following this file gets a daemon that refuses to start", name, unitPath)
			}
			continue
		}
		if _, ok := set[name]; !ok {
			t.Errorf("config.go reads %s and %s never sets it, so the unit is no longer the one place the whole configuration is visible", name, unitPath)
		}
	}
}

// TestUnitNeverCarriesADeploymentValue is the committed-value guard for this
// file. A unit is readable by anyone who can run `systemctl --user show`, and
// this example is in a public repository, so each of these has exactly one route
// in: an EnvironmentFile written outside the repo.
func TestUnitNeverCarriesADeploymentValue(t *testing.T) {
	t.Parallel()

	settings := unitSettings(t)
	set := unitEnvironment(t)
	for name := range deploymentSpecific() {
		if _, ok := set[name]; ok {
			t.Errorf("%s sets %s inline; it must arrive through EnvironmentFile, which is not committed", unitPath, name)
		}
	}
	if len(settings["EnvironmentFile"]) == 0 {
		t.Errorf("%s has no EnvironmentFile, so there is no route for %s at all", unitPath, config.EnvSharedSecret)
	}
}

// TestUnitExecStartPassesNoFlags pins the unit to a daemon that defines none.
// flag.Parse rejects a flag it does not know, so a stray one here is not a
// mis-set value — it is a service that never starts.
func TestUnitExecStartPassesNoFlags(t *testing.T) {
	t.Parallel()

	for _, cmd := range unitSettings(t)["ExecStart"] {
		if _, args, found := strings.Cut(strings.TrimSpace(cmd), " "); found && strings.TrimSpace(args) != "" {
			t.Errorf("%s runs %q; the daemon defines no flags and flag.Parse exits non-zero on one it does not know", unitPath, cmd)
		}
	}
}

// TestUnitInlineValuesAreTheDaemonDefaults enforces the claim the unit makes
// about itself: every value it sets inline is the daemon's own default, so a
// line deleted from it changes nothing. That is the only reason it is safe to
// ship a unit that hard-codes numbers at all — and an unenforced claim of that
// shape is how the file came to say 8787 and a cap of 8 in the first place.
func TestUnitInlineValuesAreTheDaemonDefaults(t *testing.T) {
	t.Parallel()

	set := unitEnvironment(t)
	for name, want := range map[string]string{
		config.EnvMaxSessions:      strconv.Itoa(config.DefaultMaxSessions),
		config.EnvCreateRatePerMin: strconv.Itoa(config.DefaultCreateRatePerMin),
		config.EnvMaxBodyBytes:     strconv.FormatInt(config.DefaultMaxBodyBytes, 10),
		config.EnvMaxStreams:       strconv.Itoa(config.DefaultMaxStreams),
		config.EnvStartCommand:     config.DefaultStartCommand,
		// Empty is this one's default: the named set adds to the default command
		// and nothing else exists until an operator writes an entry.
		config.EnvStartCommands: "",
	} {
		if got := set[name]; got != want {
			t.Errorf("%s sets %s=%s, want the daemon's default %s", unitPath, name, got, want)
		}
	}

	// %h is systemd's expansion of the home directory, so the unit's root is the
	// default root spelled the only way a unit file can spell it.
	if roots, want := set[config.EnvAllowedRoots], "%h/"+config.DefaultRootName; roots != want {
		t.Errorf("%s sets %s=%s, want %s to match the built-in default root", unitPath, config.EnvAllowedRoots, roots, want)
	}
}

// auditPipe matches a documented `journalctl … | jq …` recipe, which is the
// audit trail being read, as distinct from prose that merely names the tool. The
// first alternation stops at the pipe so a command with several of them — the
// `| sort | uniq -c` histogram — is still one match starting at journalctl.
var auditPipe = regexp.MustCompile(`journalctl[^\n|]*\|[^\n]*\bjq\b`)

// TestDocumentedAuditCommandsSelectOnlyTheDaemon holds every published reading of
// the audit trail to a filter that can actually survive one.
//
// `-u crswd` selects the unit's whole cgroup: systemd's own lifecycle lines, the
// CPU-time summary it prints on stop, and anything a session's helpers send to
// syslog. None of it is JSON, jq exits on the first one, and every deployment
// that has ever restarted has them — so the command shipped in three files was
// broken for everyone and worked in review because a first-boot journal has
// nothing in it but records (#88). `-t` selects by syslog identifier instead,
// which is why the unit sets SyslogIdentifier= rather than letting systemd infer
// one from the ExecStart basename: an inferred identifier is not a promise the
// docs can be written against.
func TestDocumentedAuditCommandsSelectOnlyTheDaemon(t *testing.T) {
	t.Parallel()

	identifiers := unitSettings(t)["SyslogIdentifier"]
	if len(identifiers) != 1 {
		t.Fatalf("%s sets SyslogIdentifier %d times, want exactly one: without it `-t` selects on a string systemd infers, and the documented commands have nothing to be correct against", unitPath, len(identifiers))
	}
	want := "-t " + identifiers[0]

	for _, path := range []string{unitPath, readmePath, rootReadmePath} {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}

		commands := auditPipe.FindAllString(string(raw), -1)
		if len(commands) == 0 {
			t.Errorf("%s documents no `journalctl … | jq` command; this file is meant to carry one and the test is not checking it", path)
			continue
		}
		for _, cmd := range commands {
			if !strings.Contains(cmd, want) {
				t.Errorf("%s documents %q, which lacks %q: it selects the unit's whole cgroup, so systemd's own lines reach jq and the operator's first read of the audit trail fails", path, cmd, want)
			}
		}
	}
}

// TestOriginAddressesAgreeWithTheDefault holds the three spellings of one address
// together. The repo carried 8787 in two files while config.DefaultListen said
// 8765, and a tunnel pointed at the wrong port is a 502 with nothing in the
// daemon's log to explain it.
func TestOriginAddressesAgreeWithTheDefault(t *testing.T) {
	t.Parallel()

	if listen := unitEnvironment(t)[config.EnvListen]; listen != config.DefaultListen {
		t.Errorf("%s sets %s=%s, want the daemon's default %s", unitPath, config.EnvListen, listen, config.DefaultListen)
	}

	raw, err := os.ReadFile(cloudflaredPath)
	if err != nil {
		t.Fatalf("read %s: %v", cloudflaredPath, err)
	}

	const originPrefix = "service: http://"
	found := false
	for _, line := range strings.Split(string(raw), "\n") {
		trimmed := strings.TrimPrefix(strings.TrimSpace(line), "- ")
		if !strings.HasPrefix(trimmed, originPrefix) {
			continue
		}
		found = true
		if origin := strings.TrimSpace(strings.TrimPrefix(trimmed, originPrefix)); origin != config.DefaultListen {
			t.Errorf("%s proxies to %s, want the daemon's default %s", cloudflaredPath, origin, config.DefaultListen)
		}
	}
	if !found {
		t.Fatalf("%s has no %q ingress rule; this test is not checking anything", cloudflaredPath, originPrefix)
	}
}
