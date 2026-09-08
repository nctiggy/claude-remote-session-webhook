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

	// Everything the parent did not supply must be a value this daemon states,
	// not one invented from an absence. Counting was the original assertion and
	// stopped saying that the moment defaults existed: it would have failed for
	// a default arriving correctly and passed for HOME being fabricated as long
	// as some other name went missing at the same time.
	delete(got, "PATH")
	for name := range got {
		if _, stated := config.SessionDefaultValue(name); !stated {
			t.Errorf("%s was composed from a parent that did not set it and no default states it", name)
		}
	}
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
		// A default is admitted here for the same reason as the base set and no
		// other: this file states its value. The rule stays "every name has a
		// rule that put it there" rather than becoming "every name the daemon
		// happened to have".
		if _, stated := config.SessionDefaultValue(name); stated {
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

// TestSessionEnvironmentSetsTheOAuth401Wait is the regression guard for the
// bursts.
//
// **Must fail when** a session is composed without CLAUDE_CODE_OAUTH_401_WAIT_MS.
// Every session on this host shares one credential store and one rotating
// refresh token, so an expiring access token is a race the losers are logged out
// by — observed as 3 sessions in 98s and 7 in 13min, not as one session at a
// time. Claude Code's own back-off covers exactly this case and is 60s only when
// the process is a remote session's child; a tmux pane is not, so it took the
// zero. The daemon's environment here holds no such variable, which is the point:
// on a real host nothing has any reason to have set it, so a fix that only
// carries an operator's value is a fix nobody receives.
func TestSessionEnvironmentSetsTheOAuth401Wait(t *testing.T) {
	t.Parallel()

	got := envOf(t, config.SessionEnvironment(daemonEnvironment(), nil))

	if got["CLAUDE_CODE_OAUTH_401_WAIT_MS"] != "60000" {
		t.Errorf("CLAUDE_CODE_OAUTH_401_WAIT_MS = %q, want %q; a session with no 401 back-off is a full participant in the refresh race",
			got["CLAUDE_CODE_OAUTH_401_WAIT_MS"], "60000")
	}
}

// TestSessionEnvironmentDefaultsReachEveryStartCommand is the reason this lives
// in the daemon rather than in a start command.
//
// **Must fail when** the value is reachable only through the operator's
// pass-through list. The first fix for this shipped inside one of three start
// commands — the wrapper script behind `rcq` — leaving `default` and `rc`, which
// run the real binary directly, still racing. Composition happens once for all
// of them, and a session made by hand from the dashboard has to be covered by
// the same thing that covers a scripted one.
func TestSessionEnvironmentDefaultsReachEveryStartCommand(t *testing.T) {
	t.Parallel()

	// An operator who has never heard of this variable and named nothing.
	got := envOf(t, config.SessionEnvironment(daemonEnvironment(), nil))

	if _, ok := got["CLAUDE_CODE_OAUTH_401_WAIT_MS"]; !ok {
		t.Fatal("a session composed for an operator who configured nothing has no 401 back-off")
	}
}

// TestSessionEnvironmentDefaultYieldsToTheOperator pins that these are defaults
// and not policy.
//
// **Must fail when** a value in the daemon's own environment is ignored or
// duplicated. Zero is upstream's own "off", so it is the value an operator
// reaches for to opt out — and a default that cannot be turned off is a setting
// the operator has to patch the binary to change.
func TestSessionEnvironmentDefaultYieldsToTheOperator(t *testing.T) {
	t.Parallel()

	parent := append(daemonEnvironment(), "CLAUDE_CODE_OAUTH_401_WAIT_MS=0")

	// envOf fails the test on a repeated name, which is the other half of this:
	// appending a default beside a carried value gives a slice where only exec's
	// last-wins rule says which one applies.
	got := envOf(t, config.SessionEnvironment(parent, nil))

	if got["CLAUDE_CODE_OAUTH_401_WAIT_MS"] != "0" {
		t.Errorf("CLAUDE_CODE_OAUTH_401_WAIT_MS = %q, want the operator's %q", got["CLAUDE_CODE_OAUTH_401_WAIT_MS"], "0")
	}
}

// TestSessionEnvironmentReplacesAnEmptyDefault covers the one value that is not
// a value.
//
// **Must fail when** an empty assignment is carried, or is carried *and* the
// default appended beside it. Every consumer of a default parses the string, and
// "" parses to nothing useful in any of them — Claude Code reads this one as an
// integer with a floor of zero — so an empty variable buys the rejection without
// the setting. It is treated as absent, and the name appears exactly once.
func TestSessionEnvironmentReplacesAnEmptyDefault(t *testing.T) {
	t.Parallel()

	parent := append(daemonEnvironment(), "CLAUDE_CODE_OAUTH_401_WAIT_MS=")

	got := envOf(t, config.SessionEnvironment(parent, nil))

	if got["CLAUDE_CODE_OAUTH_401_WAIT_MS"] != "60000" {
		t.Errorf("CLAUDE_CODE_OAUTH_401_WAIT_MS = %q, want the default %q; an empty assignment is not a setting",
			got["CLAUDE_CODE_OAUTH_401_WAIT_MS"], "60000")
	}
}

// TestSessionDefaultsCarryNoSecret is the structural guard on the second route
// into a session's environment.
//
// **Must fail when** a name in sessionDefaults is one the boundary refuses by
// the other route. Defaults are appended after the allowlist has run, so nothing
// the caller does screens them: without this, a default spelled CRSW_ or named
// by IsSecret would arrive in an unsandboxed shell having passed no check at all,
// and every existing test in this file would stay green. Unreachable while the
// list holds one CLAUDE_CODE_ name — which is why it is asserted now rather than
// after the list has grown.
func TestSessionDefaultsCarryNoSecret(t *testing.T) {
	t.Parallel()

	got := envOf(t, config.SessionEnvironment(daemonEnvironment(), nil))

	for name := range got {
		if _, stated := config.SessionDefaultValue(name); !stated {
			continue
		}
		if strings.HasPrefix(name, "CRSW_") {
			t.Errorf("%s is a default and a daemon variable; defaults are appended past the allowlist, so this reaches a session unscreened", name)
		}
		if config.IsSecret(config.KeyForVar(name)) {
			t.Errorf("%s is a default and a secret", name)
		}
	}
}
