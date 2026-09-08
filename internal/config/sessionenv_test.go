package config_test

import (
	"io"
	"slices"
	"strings"
	"testing"

	"github.com/nctiggy/claude-remote-session-webhook/internal/config"
)

// envOf turns the composed slice back into a lookup, so a test asserts about a
// name rather than about the position of a string in a slice.
func envOf(t *testing.T, composed []string) map[string]string {
	t.Helper()

	out := make(map[string]string, len(composed))
	for _, kv := range composed {
		name, value, found := strings.Cut(kv, "=")
		if !found {
			t.Fatalf("composed entry %q is not NAME=value; cmd.Env would carry it to a shell as nonsense", kv)
		}
		if _, dup := out[name]; dup {
			t.Errorf("composed %s twice; the later one wins in exec and nothing here says which that is", name)
		}
		out[name] = value
	}
	return out
}

// daemonEnvironment is a daemon's own environment as this feature found it on
// the reference host: the operator's shell, plus everything the unit and the
// EnvironmentFile put there. Every CRSW_ name below was really present in a
// live session before this change.
func daemonEnvironment() []string {
	return []string{
		"HOME=/home/operator",
		"PATH=/usr/local/bin:/usr/bin:/bin",
		"SHELL=/bin/bash",
		"USER=operator",
		"LOGNAME=operator",
		"TERM=screen-256color",
		"LANG=en_US.UTF-8",
		"LC_TIME=en_GB.UTF-8",
		"XDG_RUNTIME_DIR=/run/user/1000",
		"TMUX_TMPDIR=/run/user/1000/tmux",
		"SSH_AUTH_SOCK=/run/user/1000/keyring/ssh",
		"EDITOR=vim",

		// Never carried: it marks a process as running inside a tmux pane, and
		// passing this daemon's would tell every session it was nested.
		"TMUX=/tmp/tmux-1000/default,123,0",

		// The three the daemon must never pass on, and the reason this file
		// exists. All arrive through EnvironmentFile in the shipped unit.
		// Obviously synthetic, and deliberately so: a fixture that looked like a
		// real secret would be one somebody eventually copied out of here, and
		// gitleaks would be right to stop this file every time it changed.
		"CRSW_SHARED_SECRET=not-a-real-secret-fixture-value",
		"CRSW_ACCESS_ALLOWED_EMAILS=operator@example.com",
		"CRSW_DASHBOARD_PASSWORD=hunter2",

		// Configuration rather than credential, and excluded all the same:
		// leaving these is what makes this project's own suite fail when run
		// inside a session it started.
		"CRSW_LISTEN=127.0.0.1:8765",
		"CRSW_MAX_SESSIONS=5",
		"CRSW_ALLOWED_ROOTS=/home/operator/code",
	}
}

// TestSessionEnvironmentCarriesNoSecret is the whole point of the file under
// test.
//
// **Must fail when** any CRSW_ name survives composition. A session is
// `claude --dangerously-skip-permissions`: whatever is in its environment is one
// `env` away from being pane content, which docs/security.md already classifies
// as secret and forbids shipping anywhere.
func TestSessionEnvironmentCarriesNoSecret(t *testing.T) {
	t.Parallel()

	got := envOf(t, config.SessionEnvironment(daemonEnvironment(), nil))

	for name := range got {
		if strings.HasPrefix(name, "CRSW_") {
			t.Errorf("a session would receive %s; no daemon variable may cross this boundary", name)
		}
		if config.IsSecret(config.KeyForVar(name)) {
			t.Errorf("a session would receive the secret %s", name)
		}
	}
}

// TestSessionEnvironmentIsAWorkingShell is the other half: a boundary that
// leaks nothing and also runs nothing is not a fix.
func TestSessionEnvironmentIsAWorkingShell(t *testing.T) {
	t.Parallel()

	got := envOf(t, config.SessionEnvironment(daemonEnvironment(), nil))

	for _, name := range []string{"HOME", "PATH", "SHELL", "USER", "LOGNAME", "TERM", "LANG", "XDG_RUNTIME_DIR", "TMUX_TMPDIR"} {
		if _, ok := got[name]; !ok {
			t.Errorf("a session would start without %s", name)
		}
	}

	// TMUX_TMPDIR earns its own sentence because dropping it does not fail, it
	// relocates: the server lands under /tmp/tmux-$UID while every client looks
	// under $TMUX_TMPDIR, so sessions are created and are then invisible.
	if got["TMUX_TMPDIR"] != "/run/user/1000/tmux" {
		t.Errorf("TMUX_TMPDIR = %q; a session whose tmux socket directory moved is one nothing can find", got["TMUX_TMPDIR"])
	}

	// TMUX is the opposite case: carrying it would tell every session it was
	// running inside a tmux pane that is not its own.
	if _, ok := got["TMUX"]; ok {
		t.Error("TMUX was carried into a session, which claims it is nested inside this daemon's own pane")
	}

	// LC_* is a family rather than a name. Locale variables are numerous and
	// distribution-specific, so they are matched by prefix; spelling them out
	// would mean a session losing LC_COLLATE on whichever host sets it.
	if _, ok := got["LC_TIME"]; !ok {
		t.Error("a session would start without LC_TIME; the LC_ family is carried by prefix")
	}
}

// TestSessionEnvironmentCarriesPathUnchanged pins data-model V4.
//
// Scrubbing the environment is not the occasion to change which commands a
// session can find. internal/config/depcheck.go already distinguishes this
// daemon's PATH from the one a login shell gives a session, and re-deciding it
// here would move a question that file has already reasoned about.
func TestSessionEnvironmentCarriesPathUnchanged(t *testing.T) {
	t.Parallel()

	got := envOf(t, config.SessionEnvironment(daemonEnvironment(), nil))
	if want := "/usr/local/bin:/usr/bin:/bin"; got["PATH"] != want {
		t.Errorf("PATH is %q, want the daemon's own %q", got["PATH"], want)
	}
}

// TestSessionEnvironmentOmitsRatherThanEmpties pins data-model V3. An empty HOME
// is worse than an unset one: a shell with HOME="" writes dotfiles into the
// filesystem root and reports no error doing it.
func TestSessionEnvironmentOmitsRatherThanEmpties(t *testing.T) {
	t.Parallel()

	got := envOf(t, config.SessionEnvironment([]string{"PATH=/usr/bin"}, nil))

	if _, ok := got["HOME"]; ok {
		t.Error("HOME was composed from a parent that did not set it")
	}
	if got["PATH"] != "/usr/bin" {
		t.Errorf("PATH is %q, want the parent's %q", got["PATH"], "/usr/bin")
	}

	// Nothing beyond PATH may come from the parent. The count is not asserted
	// directly because the result is a subset of the parent PLUS
	// sessionDefaults, which this parent does not set — so a bare length would
	// change whenever a default is added and would say nothing about the
	// property under test.
	for name := range got {
		if name == "PATH" || isSessionDefaultName(name) {
			continue
		}
		t.Errorf("%s was composed from a parent that set only PATH", name)
	}
}

// isSessionDefaultName names the values SessionEnvironment supplies rather than
// inherits. Spelled out here, as the base set is, so that the tests state what
// they expect instead of asking the code under test to confirm itself.
func isSessionDefaultName(name string) bool {
	return name == "CLAUDE_CODE_OAUTH_401_WAIT_MS"
}

// TestSessionEnvironmentIsTheWholeEnvironment pins data-model V5, and it is the
// property that makes the two tests above mean anything.
//
// **Must fail when** composition becomes a filter applied over an inherited
// environment rather than a set built from nothing. If the result were merged
// with the parent's, excluding a name here would exclude it from the additions
// only, and every CRSW_ variable would arrive by the other route.
func TestSessionEnvironmentIsTheWholeEnvironment(t *testing.T) {
	t.Parallel()

	composed := config.SessionEnvironment(daemonEnvironment(), nil)
	got := envOf(t, composed)

	// Every name in the result must be one the rules admit. A name here that
	// nothing allowed means the set was inherited from somewhere.
	allowed := func(name string) bool {
		if strings.HasPrefix(name, "LC_") {
			return true
		}
		// A default is a rule that admits a name, so it belongs in this list.
		// It is the one entry here that does NOT mean "the parent had it".
		if isSessionDefaultName(name) {
			return true
		}
		return slices.Contains([]string{"HOME", "PATH", "SHELL", "USER", "LOGNAME", "TERM", "LANG", "XDG_RUNTIME_DIR", "TMUX_TMPDIR"}, name)
	}
	for name := range got {
		if !allowed(name) {
			t.Errorf("%s is in a session's environment and no rule admits it; the set is being inherited rather than composed", name)
		}
	}

	// SSH_AUTH_SOCK and EDITOR are in the parent and are not in the base set.
	// They are the canaries: harmless in themselves, and their presence would
	// mean everything else in the parent arrives too.
	for _, canary := range []string{"SSH_AUTH_SOCK", "EDITOR"} {
		if _, ok := got[canary]; ok {
			t.Errorf("%s crossed without being named; an allowlist that admits it admits the shared secret by the same route", canary)
		}
	}
}

// TestSessionEnvironmentPassesThroughWhatTheOperatorNamed covers FR-006 and
// data-model V8.
func TestSessionEnvironmentPassesThroughWhatTheOperatorNamed(t *testing.T) {
	t.Parallel()

	got := envOf(t, config.SessionEnvironment(daemonEnvironment(), []string{"SSH_AUTH_SOCK", "NOT_SET_ANYWHERE"}))

	if got["SSH_AUTH_SOCK"] != "/run/user/1000/keyring/ssh" {
		t.Errorf("the operator named SSH_AUTH_SOCK and a session did not get it: %q", got["SSH_AUTH_SOCK"])
	}
	// V8: naming a variable the daemon does not have is intent about an
	// environment that may change, not an error and not an empty value.
	if _, ok := got["NOT_SET_ANYWHERE"]; ok {
		t.Error("a named variable absent from the daemon's environment was composed anyway")
	}
	if _, ok := got["EDITOR"]; ok {
		t.Error("naming one variable admitted another")
	}
}

// TestSessionEnvironmentListRefusesWhatWouldUndoTheBoundary covers FR-007.
//
// The refusal is a startup failure, and that is the requirement rather than an
// implementation choice. A warning about a credential is a credential that
// ships, and a silent drop leaves the operator with an escape hatch they believe
// is working — both are the "starts, works, never mentions it again" failure
// this project refuses everywhere else in the auth path.
func TestSessionEnvironmentListRefusesWhatWouldUndoTheBoundary(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name  string
		value string
		want  string
	}{
		{"a secret by name", "SSH_AUTH_SOCK,CRSW_SHARED_SECRET", "CRSW_SHARED_SECRET"},
		{"the dashboard password", "CRSW_DASHBOARD_PASSWORD", "CRSW_DASHBOARD_PASSWORD"},
		{"the allowed-emails list", "CRSW_ACCESS_ALLOWED_EMAILS", "CRSW_ACCESS_ALLOWED_EMAILS"},
		{"configuration rather than credential", "CRSW_MAX_SESSIONS", "CRSW_MAX_SESSIONS"},
		{"a stray comma", "SSH_AUTH_SOCK,,EDITOR", "empty entry"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			pairs, _ := baseEnv(t)
			pairs[config.EnvSessionEnvironment] = tc.value

			_, err := config.LoadFrom(env(pairs), io.Discard)
			if err == nil {
				t.Fatalf("%s = %q loaded; a session would have received it", config.EnvSessionEnvironment, tc.value)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("refusal does not name %q, so the operator cannot act on it: %v", tc.want, err)
			}

			// docs/security.md: a refusal may name the path, the key and the
			// line, and never the value it refused over. These entries are
			// variable names rather than values, so the check that matters is
			// that no VALUE from the environment leaked into the message.
			if strings.Contains(err.Error(), goodSecret) {
				t.Error("the refusal quoted the shared secret")
			}
		})
	}
}

// TestSessionEnvironmentListAcceptsAnOrdinaryName is the other direction: the
// escape hatch has to actually open, or the allowlist has no answer for the
// workflow it breaks.
func TestSessionEnvironmentListAcceptsAnOrdinaryName(t *testing.T) {
	t.Parallel()

	pairs, _ := baseEnv(t)
	pairs[config.EnvSessionEnvironment] = "SSH_AUTH_SOCK, EDITOR"

	cfg := mustLoad(t, pairs)
	if got, want := cfg.SessionEnvironment, []string{"SSH_AUTH_SOCK", "EDITOR"}; !slices.Equal(got, want) {
		t.Errorf("SessionEnvironment = %v, want %v (surrounding spaces trimmed)", got, want)
	}
}

// TestSessionEnvironmentRefusesAPassThroughSecret is defence in depth for
// data-model V1 and V2.
//
// Configuration loading refuses these entries at startup (FR-007), so reaching
// this function with one should be impossible. It is enforced here anyway
// because V5 requires the exclusions be properties of the *result* rather than
// of the care taken by whoever built the argument.
func TestSessionEnvironmentRefusesAPassThroughSecret(t *testing.T) {
	t.Parallel()

	got := envOf(t, config.SessionEnvironment(daemonEnvironment(), []string{
		"CRSW_SHARED_SECRET", "CRSW_LISTEN", "CRSW_DASHBOARD_PASSWORD",
	}))

	if len(got) == 0 {
		t.Fatal("composed nothing at all; the base set should still be present")
	}
	for name := range got {
		if strings.HasPrefix(name, "CRSW_") {
			t.Errorf("%s was passed through because it was named; naming must not override the exclusion", name)
		}
	}
}

// TestSessionEnvironmentCarriesTheOAuthWaitToEverySession pins the one default
// this daemon supplies rather than passes on.
//
// **Must fail when** a composed environment lacks CLAUDE_CODE_OAUTH_401_WAIT_MS.
// Every session is a separate `claude` process against one credential store;
// when the eight-hour access token expires they race to refresh it, refresh
// tokens rotate, and the loser replays a consumed one and is told 401. Claude
// Code's own back-off for that case reads this variable and defaults it to 0 for
// a local process, so the loser treats the 401 as fatal and demands a login on a
// host whose credential is fine.
//
// The parent below is a daemon environment that has never heard of the variable,
// which is the deployment this exists for: it must arrive without the operator
// naming it, and without depending on which start command made the session.
func TestSessionEnvironmentCarriesTheOAuthWaitToEverySession(t *testing.T) {
	t.Parallel()

	got := envOf(t, config.SessionEnvironment(daemonEnvironment(), nil))

	value, ok := got["CLAUDE_CODE_OAUTH_401_WAIT_MS"]
	if !ok {
		t.Fatalf("a session would start with no CLAUDE_CODE_OAUTH_401_WAIT_MS; it races every other session for the credential and loses fatally")
	}
	if value != "60000" {
		t.Errorf("CLAUDE_CODE_OAUTH_401_WAIT_MS is %q, want %q — upstream's own number for many processes over one credential store", value, "60000")
	}
}

// TestSessionEnvironmentLetsTheOperatorOverrideADefault is the escape hatch, and
// the reason a default is not simply hard-coded at the point of use.
//
// **Must fail when** a default is appended over a value the operator supplied.
// Two entries for one name in cmd.Env means the last wins, and nothing in this
// package says which that is — so the operator's answer would be decided by
// append order rather than by the operator.
func TestSessionEnvironmentLetsTheOperatorOverrideADefault(t *testing.T) {
	t.Parallel()

	parent := append(daemonEnvironment(), "CLAUDE_CODE_OAUTH_401_WAIT_MS=0")
	composed := config.SessionEnvironment(parent, []string{"CLAUDE_CODE_OAUTH_401_WAIT_MS"})

	seen := 0
	for _, kv := range composed {
		if name, _, _ := strings.Cut(kv, "="); name == "CLAUDE_CODE_OAUTH_401_WAIT_MS" {
			seen++
		}
	}
	if seen != 1 {
		t.Fatalf("CLAUDE_CODE_OAUTH_401_WAIT_MS appears %d times in cmd.Env, want 1: %v", seen, composed)
	}

	got := envOf(t, composed)
	if got["CLAUDE_CODE_OAUTH_401_WAIT_MS"] != "0" {
		t.Errorf("the operator set the wait to %q and got %q; a default must not beat a stated value", "0", got["CLAUDE_CODE_OAUTH_401_WAIT_MS"])
	}
}

// TestSessionDefaultsAreNotAWayPastTheBoundary holds the file's central rule
// over the one construct that could quietly escape it.
//
// **Must fail when** a default names anything this daemon must never hand on. A
// default skips the allowlist by construction — it is added because it is on a
// list here, not because the parent had it — so the exclusions have to be
// asserted against the result, exactly as they are for an inherited name.
func TestSessionDefaultsAreNotAWayPastTheBoundary(t *testing.T) {
	t.Parallel()

	// A parent with nothing in it: whatever survives came from the defaults.
	for _, kv := range config.SessionEnvironment(nil, nil) {
		name, _, _ := strings.Cut(kv, "=")
		if strings.HasPrefix(name, "CRSW_") {
			t.Errorf("the default %s is this daemon's own configuration and must never reach a session", name)
		}
		if config.IsSecret(config.KeyForVar(name)) {
			t.Errorf("the default %s is a secret and must never reach a session", name)
		}
	}
}
