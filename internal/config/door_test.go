package config_test

// Which browser door this daemon has, decided at load and nowhere else (M12).
//
// It is a file of its own rather than more rows in config_test.go because the
// question is not "does this value parse" but "which of two authentications is
// live", and getting that wrong does not produce a bad value — it produces a
// daemon that admits the wrong people to unsandboxed code execution on the host.
// Every row below is a line of the selection table in ralph/IMPLEMENTATION_PLAN.md,
// plus the two rows that table does not name and this daemon has to answer
// anyway: the deployment that configured Access before the declaration existed,
// and a password configured beside it.

import (
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/nctiggy/claude-remote-session-webhook/internal/config"
)

// goodPassword is long enough to load and distinctive enough that finding it in
// an error, a banner, or a formatted Config is unambiguous. It is deliberately
// not a plausible password: every assertion that hunts for it is asserting that
// a live credential did not reach somewhere it must never reach.
//
// gosec G101 fires for the reason it fires on goodSecret's neighbours in
// config.go: the identifier says "password" and the value is a string literal.
// The `test-only-` prefix is the claim, and it is the one .gitleaks.toml allows
// by construction.
const goodPassword = "test-only-dashboard-password" //nolint:gosec // G101: a fixture a test sweeps for, not a credential

// shortPassword is one character under the bound, so the refusal it triggers is
// about the bound rather than about being obviously empty.
const shortPassword = "fifteen-charact"

// doorEnv is a configuration that loads and has no browser door at all: the
// shared secret and a root, and nothing that admits a browser. Each case below
// adds exactly the settings its row is about.
func doorEnv(t *testing.T) map[string]string {
	t.Helper()
	return map[string]string{
		config.EnvSharedSecret: goodSecret,
		config.EnvAllowedRoots: t.TempDir(),
	}
}

// withAccess adds the three Access values, which are all-or-nothing to the
// loader and therefore always set together here.
func withAccess(pairs map[string]string) map[string]string {
	pairs[config.EnvAccessTeamDomain] = goodTeamDomain
	pairs[config.EnvAccessAUD] = goodAUD
	pairs[config.EnvAccessAllowedEmails] = goodEmail
	return pairs
}

func TestLoadFromSelectsOneBrowserDoor(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		// with mutates doorEnv into the row's configuration.
		with func(map[string]string)
		// wantErr are substrings every one of which the refusal must carry. Empty
		// means the row loads.
		wantErr []string
		// The door that results, when it loads.
		wantAccess   bool
		wantPassword bool
		why          string
	}{
		{
			name:       "access declared and configured",
			with:       func(p map[string]string) { withAccess(p); p[config.EnvAccessEnabled] = "true" },
			wantAccess: true,
			why:        "the first row of the table: Access is the door and the three values are there",
		},
		{
			name:         "a password and no Access",
			with:         func(p map[string]string) { p[config.EnvDashboardPassword] = goodPassword },
			wantPassword: true,
			why:          "the second row: the new door, for a daemon that is not behind Cloudflare",
		},
		{
			name: "access declared beside a password",
			with: func(p map[string]string) {
				withAccess(p)
				p[config.EnvAccessEnabled] = "true"
				p[config.EnvDashboardPassword] = goodPassword
			},
			wantErr: []string{config.EnvAccessEnabled, config.EnvDashboardPassword, "refusing to start"},
			why:     "the third row: two doors is an ambiguity, and picking one silently is the defect",
		},
		{
			name: "access configured beside a password",
			with: func(p map[string]string) {
				withAccess(p)
				p[config.EnvDashboardPassword] = goodPassword
			},
			wantErr: []string{config.EnvAccessTeamDomain, config.EnvDashboardPassword},
			why: "the third row reached without the declaration. The three values are a configured " +
				"Access door whether or not anybody said so, so a password beside them is the same collision",
		},
		{
			name: "access declared, not configured, beside a password",
			with: func(p map[string]string) {
				p[config.EnvAccessEnabled] = "true"
				p[config.EnvDashboardPassword] = goodPassword
			},
			wantErr: []string{config.EnvAccessEnabled, config.EnvDashboardPassword},
			why: "two defects at once, and the collision is the one the operator has to resolve: " +
				"they are asking for both doors, and adding the three Access values would not fix it",
		},
		{
			name: "neither",
			with: func(map[string]string) {},
			why:  "the fourth row: today's daemon with nothing configured. The API works and the dashboard admits nobody",
		},
		{
			name:       "access configured without the declaration",
			with:       func(p map[string]string) { withAccess(p) },
			wantAccess: true,
			why: "the row the table does not name, and the one every existing deployment is on: three " +
				"Access values written before this variable existed. Reading them as no door would take " +
				"the dashboard away from a daemon whose operator changed nothing",
		},
		{
			name:       "access declared off with the three set",
			with:       func(p map[string]string) { withAccess(p); p[config.EnvAccessEnabled] = "false" },
			wantAccess: true,
			why: "false and unset are one value here — an unchecked box submits nothing — so this is the " +
				"row above with a line written out. It cannot mean 'turn Access off' without meaning it " +
				"for every daemon that never set it",
		},
		{
			name:    "access declared and not configured",
			with:    func(p map[string]string) { p[config.EnvAccessEnabled] = "true" },
			wantErr: []string{config.EnvAccessEnabled, config.EnvAccessTeamDomain, config.EnvAccessAUD, config.EnvAccessAllowedEmails},
			why: "the declaration is what makes the three required. Started anyway, this daemon would " +
				"look configured and refuse every login",
		},
		{
			name:    "a password under the bound",
			with:    func(p map[string]string) { p[config.EnvDashboardPassword] = shortPassword },
			wantErr: []string{config.EnvDashboardPassword, "16", "refusing to start"},
			why:     "weak auth configuration is a startup failure, not a warning (Principle I)",
		},
		{
			name:    "a declaration that is not a boolean",
			with:    func(p map[string]string) { withAccess(p); p[config.EnvAccessEnabled] = "yes" },
			wantErr: []string{config.EnvAccessEnabled, "true or false"},
			why:     "an operator who wrote `yes` said on, and reading it as off would leave the demand they made unmade",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			pairs := doorEnv(t)
			tc.with(pairs)

			cfg, err := config.LoadFrom(env(pairs), io.Discard, config.WithoutConfigFile())

			if len(tc.wantErr) > 0 {
				if err == nil {
					t.Fatalf("LoadFrom() started: %s", tc.why)
				}
				for _, want := range tc.wantErr {
					if !strings.Contains(err.Error(), want) {
						t.Errorf("error = %q, want it to name %q: %s", err, want, tc.why)
					}
				}
				// The refusal is written to a terminal and kept in the journal
				// forever. It may name the variables that collided and never the
				// credential one of them holds.
				assertUnspoken(t, err.Error(), "the refusal")
				return
			}

			if err != nil {
				t.Fatalf("LoadFrom() refused: %v — %s", err, tc.why)
			}
			if got := cfg.AccessTeamDomain != ""; got != tc.wantAccess {
				t.Errorf("an Access door = %v, want %v: %s", got, tc.wantAccess, tc.why)
			}
			if got := len(cfg.DashboardPassword) > 0; got != tc.wantPassword {
				t.Errorf("a password door = %v, want %v: %s", got, tc.wantPassword, tc.why)
			}
			// Both at once is the state the whole function exists to make
			// unreachable, and it is worth asserting separately from the two
			// fields above: a row that expected one and got both would otherwise
			// fail on only one line and read as a lesser problem than it is.
			if cfg.AccessTeamDomain != "" && len(cfg.DashboardPassword) > 0 {
				t.Error("this daemon has two browser doors; exactly one may ever be live")
			}
			if tc.wantPassword && string(cfg.DashboardPassword) != goodPassword {
				t.Error("the configured password did not survive the load, so nothing downstream could check it")
			}
		})
	}
}

// TestFormattingAConfigNeverSpellsTheDashboardPassword is the other half of
// calling it a secret. IsSecret keeps it off the settings page; this keeps it out
// of every log line, panic, and debug print, which is where the shared secret's
// own redaction has always had to hold.
func TestFormattingAConfigNeverSpellsTheDashboardPassword(t *testing.T) {
	t.Parallel()

	pairs := doorEnv(t)
	pairs[config.EnvDashboardPassword] = goodPassword

	cfg, err := config.LoadFrom(env(pairs), io.Discard, config.WithoutConfigFile())
	if err != nil {
		t.Fatalf("LoadFrom() refused a password door: %v", err)
	}

	for _, verb := range []string{"%v", "%s", "%+v", "%#v", "%q"} {
		assertUnspoken(t, fmt.Sprintf(verb, cfg), "Sprintf("+verb+")")
	}
	assertUnspoken(t, cfg.String(), "String()")
	assertUnspoken(t, cfg.GoString(), "GoString()")

	// And the formatted Config still says a password door is live, because
	// which door this daemon has is the fact an operator reads a log line for.
	if got := cfg.String(); !strings.Contains(got, "dashboard_password:<redacted>") {
		t.Errorf("String() = %s, want it to say a password door is configured", got)
	}
}

// TestAConfigWithNoPasswordSaysSo pins the other side of that line. A daemon with
// no password door must not print one: `<redacted>` beside a door that is not
// there is a log line describing a daemon that does not exist.
func TestAConfigWithNoPasswordSaysSo(t *testing.T) {
	t.Parallel()

	pairs, _ := baseEnv(t)
	cfg := mustLoad(t, pairs)

	if got := cfg.String(); !strings.Contains(got, "dashboard_password:unset") {
		t.Errorf("String() = %s, want it to say there is no password door", got)
	}
}

// TestTheDashboardPasswordIsNotBrowserEditable is the rule the plan states
// plainly: a password settable from the page it protects is not a door.
//
// config.Editable is !IsSecret, so this passes the moment IsSecret names the key
// — which is exactly the argument for one predicate. It is asserted anyway,
// because "the settings page will not write this key" is the behaviour, and a
// later narrowing of IsSecret would put a text input holding a live credential on
// the page with nothing else failing.
func TestTheDashboardPasswordIsNotBrowserEditable(t *testing.T) {
	t.Parallel()

	if config.Editable("dashboard_password") {
		t.Error("the settings page may write dashboard_password, so the door is settable from the page it protects")
	}
	// The declaration beside it is an ordinary setting and stays editable: it
	// holds no credential, and an operator turning it on writes a `true` that the
	// edit's own validation refuses if the three Access values are not there.
	if !config.Editable("access_enabled") {
		t.Error("access_enabled is not editable; it is a boolean holding no secret and there is no reason to withhold it")
	}
}

// TestEditedConfigurationRefusesTwoDoors runs the selection through the path an
// operator actually reaches it by. config.Validate is what the settings page asks
// before writing the file, so a candidate that would stop the daemon coming up is
// refused while it is still up — and a second configured door is exactly that.
func TestEditedConfigurationRefusesTwoDoors(t *testing.T) {
	t.Parallel()

	pairs := doorEnv(t)
	candidate := fmt.Sprintf("access_enabled = true\ndashboard_password = %s\n", goodPassword)

	err := config.Validate([]byte(candidate), env(pairs))
	if err == nil {
		t.Fatal("Validate() accepted a file configuring both doors; the daemon would not start on it")
	}
	assertUnspoken(t, err.Error(), "the refusal")
}

// TestTheClosedDoorBannerNamesBothDoors keeps the startup warning true. It fires
// for a daemon that admits nobody, so a password door must silence it — a banner
// saying the dashboard admits nobody, on a daemon whose operator can sign in, is
// how an operator learns to stop reading banners.
func TestTheClosedDoorBannerNamesBothDoors(t *testing.T) {
	t.Parallel()

	var closed strings.Builder
	if _, err := config.LoadFrom(env(doorEnv(t)), &closed, config.WithoutConfigFile()); err != nil {
		t.Fatalf("LoadFrom() refused a daemon with no door at all: %v", err)
	}
	if !strings.Contains(closed.String(), "admits nobody") {
		t.Errorf("a daemon with no browser door emitted %q, want the warning it has always emitted", closed.String())
	}
	if !strings.Contains(closed.String(), config.EnvDashboardPassword) {
		t.Errorf("the warning = %q, want the other door named: an operator with no Cloudflare account cannot act on the three Access variables", closed.String())
	}

	withPassword := doorEnv(t)
	withPassword[config.EnvDashboardPassword] = goodPassword

	var open strings.Builder
	if _, err := config.LoadFrom(env(withPassword), &open, config.WithoutConfigFile()); err != nil {
		t.Fatalf("LoadFrom() refused a password door: %v", err)
	}
	if strings.Contains(open.String(), "admits nobody") {
		t.Errorf("a daemon with a password door warned that it admits nobody: %q", open.String())
	}
	assertUnspoken(t, open.String(), "the startup output")
}

// assertUnspoken fails if a live credential reached text this daemon writes.
func assertUnspoken(t *testing.T, text, where string) {
	t.Helper()

	if strings.Contains(text, goodPassword) {
		t.Errorf("%s spells the dashboard password: %s", where, text)
	}
}
