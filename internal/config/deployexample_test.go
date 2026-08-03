package config_test

// The example systemd unit is the only place an operator sees the daemon's whole
// configuration laid out at once, and it drifted from config.go for forty
// iterations without anything noticing: it set CRSW_SESSION_CAP, CRSW_IDLE_TIMEOUT
// and CRSW_ABSOLUTE_LIFETIME, none of which is read, and passed a --listen flag to
// a binary that defines no flags. Both failures are silent in the worst direction
// — the operator who sets a cap of 8 runs with 5 and is never told — so they are
// checked here rather than left to review (Constitution Principle V).
//
// declaredVars lives in envexample_test.go and parses config.go, so a variable
// renamed there fails this file too.

import (
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/nctiggy/claude-remote-session-webhook/internal/config"
)

const (
	unitPath        = "../../deploy/crswd.example.service"
	cloudflaredPath = "../../deploy/cloudflared.example.yml"
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

// TestUnitSetsOnlyVariablesTheDaemonReads is the silent-wrong guard. A variable
// nothing reads leaves the operator believing they configured something.
func TestUnitSetsOnlyVariablesTheDaemonReads(t *testing.T) {
	t.Parallel()

	declared := declaredVars(t)
	set := unitEnvironment(t)

	for name := range set {
		if !declared[name] {
			t.Errorf("%s sets %s and nothing in config.go reads it, so the operator who sets it runs on the default and is never told", unitPath, name)
		}
	}

	// The other direction, because the unit claims inline to be the complete list.
	// The secret is the one exception and has its own test below.
	for name := range declared {
		if name == config.EnvSharedSecret {
			continue
		}
		if _, ok := set[name]; !ok {
			t.Errorf("config.go reads %s and %s never sets it, so the unit is no longer the one place the whole configuration is visible", name, unitPath)
		}
	}
}

// TestUnitNeverCarriesTheSecret is the committed-secret guard for this file. A
// unit is readable by anyone who can run `systemctl --user show`, and this
// example is in a public repository, so the secret has exactly one route in:
// an EnvironmentFile written from 1Password outside the repo.
func TestUnitNeverCarriesTheSecret(t *testing.T) {
	t.Parallel()

	settings := unitSettings(t)
	if _, ok := unitEnvironment(t)[config.EnvSharedSecret]; ok {
		t.Errorf("%s sets %s inline; it must arrive through EnvironmentFile, which is not committed", unitPath, config.EnvSharedSecret)
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
