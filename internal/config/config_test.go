package config_test

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nctiggy/claude-remote-session-webhook/internal/config"
)

// A valid secret, distinctive enough that a leak into an error string or a
// formatted Config is unmistakable. Exactly MinSecretBytes long, and spelled in
// words rather than hex: 32 hex characters next to the word "secret" is what a
// real HMAC key looks like, and the repo's gitleaks rules correctly say so.
const goodSecret = "test-only-shared-secret-32-bytes"

// The layer-1 values, each distinctive enough that finding it in an error string
// or a formatted Config is unambiguous. They name an organisation and a person,
// which is why several assertions below hunt for them.
const (
	goodTeamDomain = "example-team.cloudflareaccess.com"
	goodAUD        = "test-only-audience-tag"
	goodEmail      = "operator@example.com"
)

func env(pairs map[string]string) func(string) string {
	return func(key string) string { return pairs[key] }
}

// baseEnv is a configuration that loads cleanly, so each table case can change
// exactly one thing and blame the failure on it.
func baseEnv(t *testing.T) (map[string]string, string) {
	t.Helper()
	root := t.TempDir()
	return map[string]string{
		config.EnvSharedSecret:        goodSecret,
		config.EnvAllowedRoots:        root,
		config.EnvAccessTeamDomain:    goodTeamDomain,
		config.EnvAccessAUD:           goodAUD,
		config.EnvAccessAllowedEmails: goodEmail,
	}, root
}

func mustLoad(t *testing.T, pairs map[string]string) *config.Config {
	t.Helper()
	cfg, err := config.LoadFrom(env(pairs), io.Discard)
	if err != nil {
		t.Fatalf("LoadFrom() unexpected error: %v", err)
	}
	return cfg
}

func TestLoadFromDefaultsEverythingOptional(t *testing.T) {
	t.Parallel()

	pairs, root := baseEnv(t)
	cfg := mustLoad(t, pairs)

	if cfg.Listen != config.DefaultListen {
		t.Errorf("Listen = %q, want %q", cfg.Listen, config.DefaultListen)
	}
	if cfg.MaxSessions != config.DefaultMaxSessions {
		t.Errorf("MaxSessions = %d, want %d", cfg.MaxSessions, config.DefaultMaxSessions)
	}
	if cfg.CreateRatePerMin != config.DefaultCreateRatePerMin {
		t.Errorf("CreateRatePerMin = %d, want %d", cfg.CreateRatePerMin, config.DefaultCreateRatePerMin)
	}
	if cfg.MaxBodyBytes != config.DefaultMaxBodyBytes {
		t.Errorf("MaxBodyBytes = %d, want %d", cfg.MaxBodyBytes, config.DefaultMaxBodyBytes)
	}
	if cfg.MaxStreams != config.DefaultMaxStreams {
		t.Errorf("MaxStreams = %d, want %d", cfg.MaxStreams, config.DefaultMaxStreams)
	}
	if string(cfg.SharedSecret) != goodSecret {
		t.Error("SharedSecret did not survive the load")
	}
	// A bare team hostname is the normal form, and https is not optional for it.
	if want := "https://" + goodTeamDomain; cfg.AccessTeamDomain != want {
		t.Errorf("AccessTeamDomain = %q, want %q", cfg.AccessTeamDomain, want)
	}
	if cfg.AccessAUD != goodAUD {
		t.Errorf("AccessAUD = %q, want %q", cfg.AccessAUD, goodAUD)
	}
	if len(cfg.AccessAllowedEmails) != 1 || cfg.AccessAllowedEmails[0] != goodEmail {
		t.Errorf("AccessAllowedEmails = %v, want the one configured address", cfg.AccessAllowedEmails)
	}
	if len(cfg.Roots) != 1 || cfg.Roots[0].Path != resolve(t, root) {
		t.Errorf("Roots = %v, want the one configured root %q", cfg.Roots, root)
	}
	if cfg.Roots[0].IsDefault {
		t.Error("IsDefault is set on an explicitly configured root")
	}
}

func TestLoadFromAcceptsEveryOptionalOverride(t *testing.T) {
	t.Parallel()

	pairs, _ := baseEnv(t)
	pairs[config.EnvListen] = "127.0.0.9:9999"
	pairs[config.EnvMaxSessions] = "3"
	pairs[config.EnvCreateRatePerMin] = "12"
	pairs[config.EnvMaxBodyBytes] = "1048576"
	pairs[config.EnvMaxStreams] = "2"

	cfg := mustLoad(t, pairs)

	if cfg.Listen != "127.0.0.9:9999" {
		t.Errorf("Listen = %q, want the configured value", cfg.Listen)
	}
	if cfg.MaxSessions != 3 {
		t.Errorf("MaxSessions = %d, want 3", cfg.MaxSessions)
	}
	if cfg.CreateRatePerMin != 12 {
		t.Errorf("CreateRatePerMin = %d, want 12", cfg.CreateRatePerMin)
	}
	if cfg.MaxBodyBytes != 1048576 {
		t.Errorf("MaxBodyBytes = %d, want 1048576", cfg.MaxBodyBytes)
	}
	if cfg.MaxStreams != 2 {
		t.Errorf("MaxStreams = %d, want 2", cfg.MaxStreams)
	}
}

// TestLoadFromRejects is the table the task calls for: every missing, short, or
// invalid case. Each entry mutates the working base environment, so a case that
// stops failing is a real regression rather than a fixture that rotted.
func TestLoadFromRejects(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		// mutate receives a loadable environment and the temp root behind it.
		mutate func(t *testing.T, pairs map[string]string, root string)
		// wantIn must appear in the error: the operator has to be told which
		// variable to fix.
		wantIn string
	}{
		{
			name:   "shared secret unset",
			mutate: func(_ *testing.T, p map[string]string, _ string) { delete(p, config.EnvSharedSecret) },
			wantIn: config.EnvSharedSecret,
		},
		{
			name:   "shared secret empty",
			mutate: func(_ *testing.T, p map[string]string, _ string) { p[config.EnvSharedSecret] = "" },
			wantIn: config.EnvSharedSecret,
		},
		{
			name: "shared secret one byte short",
			mutate: func(_ *testing.T, p map[string]string, _ string) {
				p[config.EnvSharedSecret] = goodSecret[:config.MinSecretBytes-1]
			},
			wantIn: config.EnvSharedSecret,
		},
		{
			name:   "allowed root does not exist",
			mutate: func(_ *testing.T, p map[string]string, root string) { p[config.EnvAllowedRoots] = root + "/nope" },
			wantIn: config.EnvAllowedRoots,
		},
		{
			name:   "allowed root is relative",
			mutate: func(_ *testing.T, p map[string]string, _ string) { p[config.EnvAllowedRoots] = "code" },
			wantIn: "absolute",
		},
		{
			name:   "allowed root is dot-relative",
			mutate: func(_ *testing.T, p map[string]string, _ string) { p[config.EnvAllowedRoots] = "../code" },
			wantIn: "absolute",
		},
		{
			name:   "allowed roots has a trailing empty entry",
			mutate: func(_ *testing.T, p map[string]string, root string) { p[config.EnvAllowedRoots] = root + ":" },
			wantIn: "empty entry",
		},
		{
			name:   "allowed roots has a leading empty entry",
			mutate: func(_ *testing.T, p map[string]string, root string) { p[config.EnvAllowedRoots] = ":" + root },
			wantIn: "empty entry",
		},
		{
			name:   "allowed roots has an interior empty entry",
			mutate: func(_ *testing.T, p map[string]string, root string) { p[config.EnvAllowedRoots] = root + "::" + root },
			wantIn: "empty entry",
		},
		{
			name: "allowed root is a file, not a directory",
			mutate: func(t *testing.T, p map[string]string, root string) {
				file := filepath.Join(root, "not-a-dir")
				if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
					t.Fatalf("write fixture: %v", err)
				}
				p[config.EnvAllowedRoots] = file
			},
			wantIn: "not a directory",
		},
		{
			name: "second allowed root is bad even though the first is fine",
			mutate: func(_ *testing.T, p map[string]string, root string) {
				p[config.EnvAllowedRoots] = root + ":" + root + "/nope"
			},
			wantIn: config.EnvAllowedRoots,
		},
		{
			name: "allowed roots unset and HOME unset",
			mutate: func(_ *testing.T, p map[string]string, _ string) {
				delete(p, config.EnvAllowedRoots)
			},
			wantIn: "HOME",
		},
		{
			name: "allowed roots unset and HOME is relative",
			mutate: func(_ *testing.T, p map[string]string, _ string) {
				delete(p, config.EnvAllowedRoots)
				p["HOME"] = "home/u"
			},
			wantIn: "HOME",
		},
		{
			name: "allowed roots unset and the default root is missing",
			mutate: func(_ *testing.T, p map[string]string, root string) {
				delete(p, config.EnvAllowedRoots)
				p["HOME"] = root // no "code" directory under it
			},
			wantIn: config.EnvAllowedRoots,
		},
		{
			name:   "listen host is a wildcard",
			mutate: func(_ *testing.T, p map[string]string, _ string) { p[config.EnvListen] = "0.0.0.0:8765" },
			wantIn: "not loopback",
		},
		{
			name:   "listen host is a routable address",
			mutate: func(_ *testing.T, p map[string]string, _ string) { p[config.EnvListen] = "192.168.1.10:8765" },
			wantIn: "not loopback",
		},
		{
			name:   "listen host is the IPv6 wildcard",
			mutate: func(_ *testing.T, p map[string]string, _ string) { p[config.EnvListen] = "[::]:8765" },
			wantIn: "not loopback",
		},
		{
			name:   "listen host is empty, meaning every interface",
			mutate: func(_ *testing.T, p map[string]string, _ string) { p[config.EnvListen] = ":8765" },
			wantIn: "loopback IP literal",
		},
		{
			name:   "listen host is a name that could resolve anywhere",
			mutate: func(_ *testing.T, p map[string]string, _ string) { p[config.EnvListen] = "localhost:8765" },
			wantIn: "loopback IP literal",
		},
		{
			name:   "listen has no port",
			mutate: func(_ *testing.T, p map[string]string, _ string) { p[config.EnvListen] = "127.0.0.1" },
			wantIn: config.EnvListen,
		},
		{
			name:   "listen port is not a number",
			mutate: func(_ *testing.T, p map[string]string, _ string) { p[config.EnvListen] = "127.0.0.1:http" },
			wantIn: "port",
		},
		{
			name:   "listen port is zero",
			mutate: func(_ *testing.T, p map[string]string, _ string) { p[config.EnvListen] = "127.0.0.1:0" },
			wantIn: "port",
		},
		{
			name:   "listen port is out of range",
			mutate: func(_ *testing.T, p map[string]string, _ string) { p[config.EnvListen] = "127.0.0.1:65536" },
			wantIn: "port",
		},
		{
			name:   "max sessions is zero",
			mutate: func(_ *testing.T, p map[string]string, _ string) { p[config.EnvMaxSessions] = "0" },
			wantIn: config.EnvMaxSessions,
		},
		{
			name:   "max sessions is negative",
			mutate: func(_ *testing.T, p map[string]string, _ string) { p[config.EnvMaxSessions] = "-1" },
			wantIn: config.EnvMaxSessions,
		},
		{
			name:   "max sessions is not a number",
			mutate: func(_ *testing.T, p map[string]string, _ string) { p[config.EnvMaxSessions] = "five" },
			wantIn: config.EnvMaxSessions,
		},
		{
			name:   "max sessions is fractional",
			mutate: func(_ *testing.T, p map[string]string, _ string) { p[config.EnvMaxSessions] = "1.5" },
			wantIn: config.EnvMaxSessions,
		},
		{
			name:   "create rate is zero",
			mutate: func(_ *testing.T, p map[string]string, _ string) { p[config.EnvCreateRatePerMin] = "0" },
			wantIn: config.EnvCreateRatePerMin,
		},
		{
			name:   "create rate is not a number",
			mutate: func(_ *testing.T, p map[string]string, _ string) { p[config.EnvCreateRatePerMin] = "lots" },
			wantIn: config.EnvCreateRatePerMin,
		},
		{
			name:   "max body bytes is zero",
			mutate: func(_ *testing.T, p map[string]string, _ string) { p[config.EnvMaxBodyBytes] = "0" },
			wantIn: config.EnvMaxBodyBytes,
		},
		{
			name:   "max body bytes is negative",
			mutate: func(_ *testing.T, p map[string]string, _ string) { p[config.EnvMaxBodyBytes] = "-4096" },
			wantIn: config.EnvMaxBodyBytes,
		},
		{
			name:   "max body bytes is not a number",
			mutate: func(_ *testing.T, p map[string]string, _ string) { p[config.EnvMaxBodyBytes] = "64k" },
			wantIn: config.EnvMaxBodyBytes,
		},
		{
			name:   "max streams is zero",
			mutate: func(_ *testing.T, p map[string]string, _ string) { p[config.EnvMaxStreams] = "0" },
			wantIn: config.EnvMaxStreams,
		},
		{
			name:   "max streams is negative",
			mutate: func(_ *testing.T, p map[string]string, _ string) { p[config.EnvMaxStreams] = "-1" },
			wantIn: config.EnvMaxStreams,
		},
		{
			name:   "max streams is not a number",
			mutate: func(_ *testing.T, p map[string]string, _ string) { p[config.EnvMaxStreams] = "ten" },
			wantIn: config.EnvMaxStreams,
		},
		{
			name:   "team domain unset",
			mutate: func(_ *testing.T, p map[string]string, _ string) { delete(p, config.EnvAccessTeamDomain) },
			wantIn: config.EnvAccessTeamDomain,
		},
		{
			name:   "team domain empty",
			mutate: func(_ *testing.T, p map[string]string, _ string) { p[config.EnvAccessTeamDomain] = "" },
			wantIn: config.EnvAccessTeamDomain,
		},
		{
			name:   "team domain is only whitespace",
			mutate: func(_ *testing.T, p map[string]string, _ string) { p[config.EnvAccessTeamDomain] = "   " },
			wantIn: config.EnvAccessTeamDomain,
		},
		{
			name: "team domain is http on a routable host",
			mutate: func(_ *testing.T, p map[string]string, _ string) {
				p[config.EnvAccessTeamDomain] = "http://" + goodTeamDomain
			},
			wantIn: "loopback",
		},
		{
			// A name that resolves to loopback today can be pointed elsewhere by
			// /etc/hosts tomorrow, which is why EnvListen refuses names too.
			name: "team domain is http on the name localhost",
			mutate: func(_ *testing.T, p map[string]string, _ string) {
				p[config.EnvAccessTeamDomain] = "http://localhost:8099"
			},
			wantIn: "loopback",
		},
		{
			name: "team domain uses an unrelated scheme",
			mutate: func(_ *testing.T, p map[string]string, _ string) {
				p[config.EnvAccessTeamDomain] = "ftp://" + goodTeamDomain
			},
			wantIn: config.EnvAccessTeamDomain,
		},
		{
			name: "team domain carries a path, which would break the key-set URL",
			mutate: func(_ *testing.T, p map[string]string, _ string) {
				p[config.EnvAccessTeamDomain] = goodTeamDomain + "/cdn-cgi/access/certs"
			},
			wantIn: config.EnvAccessTeamDomain,
		},
		{
			name: "team domain carries a query",
			mutate: func(_ *testing.T, p map[string]string, _ string) {
				p[config.EnvAccessTeamDomain] = "https://" + goodTeamDomain + "?a=b"
			},
			wantIn: config.EnvAccessTeamDomain,
		},
		{
			name: "team domain carries a fragment",
			mutate: func(_ *testing.T, p map[string]string, _ string) {
				p[config.EnvAccessTeamDomain] = "https://" + goodTeamDomain + "#frag"
			},
			wantIn: config.EnvAccessTeamDomain,
		},
		{
			name: "team domain carries credentials",
			mutate: func(_ *testing.T, p map[string]string, _ string) {
				p[config.EnvAccessTeamDomain] = "https://user:pass@" + goodTeamDomain
			},
			wantIn: config.EnvAccessTeamDomain,
		},
		{
			name:   "team domain has no host",
			mutate: func(_ *testing.T, p map[string]string, _ string) { p[config.EnvAccessTeamDomain] = "https://" },
			wantIn: config.EnvAccessTeamDomain,
		},
		{
			name:   "team domain is not parseable at all",
			mutate: func(_ *testing.T, p map[string]string, _ string) { p[config.EnvAccessTeamDomain] = "https://a b.com" },
			wantIn: config.EnvAccessTeamDomain,
		},
		{
			name:   "audience unset",
			mutate: func(_ *testing.T, p map[string]string, _ string) { delete(p, config.EnvAccessAUD) },
			wantIn: config.EnvAccessAUD,
		},
		{
			name:   "audience empty",
			mutate: func(_ *testing.T, p map[string]string, _ string) { p[config.EnvAccessAUD] = "" },
			wantIn: config.EnvAccessAUD,
		},
		{
			name:   "audience is only whitespace",
			mutate: func(_ *testing.T, p map[string]string, _ string) { p[config.EnvAccessAUD] = "\t " },
			wantIn: config.EnvAccessAUD,
		},
		{
			name:   "allowed emails unset",
			mutate: func(_ *testing.T, p map[string]string, _ string) { delete(p, config.EnvAccessAllowedEmails) },
			wantIn: config.EnvAccessAllowedEmails,
		},
		{
			name:   "allowed emails empty",
			mutate: func(_ *testing.T, p map[string]string, _ string) { p[config.EnvAccessAllowedEmails] = "" },
			wantIn: config.EnvAccessAllowedEmails,
		},
		{
			name:   "allowed emails is only a separator",
			mutate: func(_ *testing.T, p map[string]string, _ string) { p[config.EnvAccessAllowedEmails] = "," },
			wantIn: config.EnvAccessAllowedEmails,
		},
		{
			name: "allowed emails has a trailing empty entry",
			mutate: func(_ *testing.T, p map[string]string, _ string) {
				p[config.EnvAccessAllowedEmails] = goodEmail + ","
			},
			wantIn: "empty entry",
		},
		{
			name: "allowed emails has a leading empty entry",
			mutate: func(_ *testing.T, p map[string]string, _ string) {
				p[config.EnvAccessAllowedEmails] = "," + goodEmail
			},
			wantIn: "empty entry",
		},
		{
			name: "allowed emails has an interior empty entry",
			mutate: func(_ *testing.T, p map[string]string, _ string) {
				p[config.EnvAccessAllowedEmails] = goodEmail + ",," + goodEmail
			},
			wantIn: "empty entry",
		},
		{
			// The separator typed wrong. Left to run this is not a startup failure
			// but a silent one: the operator is refused by their own allowlist.
			name: "allowed emails are separated by spaces",
			mutate: func(_ *testing.T, p map[string]string, _ string) {
				p[config.EnvAccessAllowedEmails] = goodEmail + " second@example.com"
			},
			wantIn: "whitespace",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			pairs, root := baseEnv(t)
			tt.mutate(t, pairs, root)

			cfg, err := config.LoadFrom(env(pairs), io.Discard)
			if err == nil {
				t.Fatalf("LoadFrom() accepted the configuration, got %v", cfg)
			}
			if cfg != nil {
				t.Errorf("LoadFrom() returned a config alongside an error: %v", cfg)
			}
			if !strings.Contains(err.Error(), tt.wantIn) {
				t.Errorf("error %q does not mention %q, so the operator is not told what to fix", err, tt.wantIn)
			}
			assertNoSecret(t, err.Error(), pairs[config.EnvSharedSecret])
			assertNoAccessValues(t, err.Error(), pairs)
		})
	}
}

// TestLoadFromNeverRevealsTheSecret covers FR-043 at the one boundary where the
// secret is in hand and something is being written: the startup error. Not even
// its length may appear, so the short value here is seven bytes and "7" is what
// the assertion hunts for.
func TestLoadFromNeverRevealsTheSecret(t *testing.T) {
	t.Parallel()

	const short = "toosmal" // 7 bytes, deliberately

	pairs, _ := baseEnv(t)
	pairs[config.EnvSharedSecret] = short

	_, err := config.LoadFrom(env(pairs), io.Discard)
	if err == nil {
		t.Fatal("a 7-byte secret was accepted")
	}

	msg := err.Error()
	assertNoSecret(t, msg, short)
	if strings.Contains(msg, fmt.Sprint(len(short))) {
		t.Errorf("error %q leaks the secret's length", msg)
	}
	if !strings.Contains(msg, fmt.Sprint(config.MinSecretBytes)) {
		t.Errorf("error %q does not name the required minimum", msg)
	}
}

// TestLoadFromNormalisesTheTeamDomain covers the accepted spellings of the one
// value two derivations must agree on. Whatever an operator types, the issuer
// and the key-set origin are the same string.
func TestLoadFromNormalisesTheTeamDomain(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		configured string
		want       string
	}{
		{"bare team hostname is https", goodTeamDomain, "https://" + goodTeamDomain},
		{"explicit https origin", "https://" + goodTeamDomain, "https://" + goodTeamDomain},
		{"trailing slash is not part of the origin", "https://" + goodTeamDomain + "/", "https://" + goodTeamDomain},
		{"surrounding whitespace is not part of the origin", "  " + goodTeamDomain + "  ", "https://" + goodTeamDomain},
		{"host case is not significant to DNS but is to the issuer comparison", "HTTPS://Example-Team.CloudflareAccess.com", "https://" + goodTeamDomain},
		{"http on a loopback literal, for the quickstart's own key server", "http://127.0.0.1:8099", "http://127.0.0.1:8099"},
		{"http on the IPv6 loopback literal", "http://[::1]:8099", "http://[::1]:8099"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			pairs, _ := baseEnv(t)
			pairs[config.EnvAccessTeamDomain] = tt.configured

			if got := mustLoad(t, pairs).AccessTeamDomain; got != tt.want {
				t.Errorf("AccessTeamDomain = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestLoadFromKeepsEveryAllowedAddress(t *testing.T) {
	t.Parallel()

	pairs, _ := baseEnv(t)
	// Spaces around the separator are how a person writes a list, and are not
	// part of any address.
	pairs[config.EnvAccessAllowedEmails] = goodEmail + ", second@example.com"

	cfg := mustLoad(t, pairs)

	want := []string{goodEmail, "second@example.com"}
	if len(cfg.AccessAllowedEmails) != len(want) {
		t.Fatalf("AccessAllowedEmails = %v, want %v", cfg.AccessAllowedEmails, want)
	}
	for i, address := range want {
		if cfg.AccessAllowedEmails[i] != address {
			t.Errorf("AccessAllowedEmails[%d] = %q, want %q", i, cfg.AccessAllowedEmails[i], address)
		}
	}
}

// TestLoadFromDoesNotDemandLayerOneUnderTheBypass is FR-042. Without it, local
// development would need a Cloudflare account to supply an audience the bypass
// then ignores.
func TestLoadFromDoesNotDemandLayerOneUnderTheBypass(t *testing.T) {
	t.Parallel()

	pairs, _ := baseEnv(t)
	for _, name := range []string{config.EnvAccessTeamDomain, config.EnvAccessAUD, config.EnvAccessAllowedEmails} {
		delete(pairs, name)
	}

	cfg, err := config.LoadFrom(env(pairs), io.Discard, config.WithAccessBypassActive())
	if err != nil {
		t.Fatalf("LoadFrom() with the bypass active refused a configuration it does not need: %v", err)
	}
	if cfg.AccessTeamDomain != "" || cfg.AccessAUD != "" || len(cfg.AccessAllowedEmails) != 0 {
		t.Errorf("the bypass invented layer-1 configuration: %v", cfg)
	}
	// The bypass skips layer 1 only. Everything that bounds the host is still
	// demanded, and still defaulted.
	if cfg.MaxStreams != config.DefaultMaxStreams || len(cfg.Roots) != 1 || string(cfg.SharedSecret) != goodSecret {
		t.Errorf("the bypass relaxed something other than layer 1: %v", cfg)
	}
}

// The bypass lifts the requirement to be present, never the requirement to be
// valid. The operator meant the value they set, and will one day run without the
// bypass — with a daemon that has never once told them the value is wrong.
func TestLoadFromStillValidatesLayerOneUnderTheBypass(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct{ name, key, value string }{
		{"team domain is http on a routable host", config.EnvAccessTeamDomain, "http://" + goodTeamDomain},
		{"allowed emails has an empty entry", config.EnvAccessAllowedEmails, goodEmail + ",,"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			pairs, _ := baseEnv(t)
			pairs[tt.key] = tt.value

			if _, err := config.LoadFrom(env(pairs), io.Discard, config.WithAccessBypassActive()); err == nil {
				t.Fatalf("the bypass accepted a malformed %s", tt.key)
			}
		})
	}
}

// The shipping build's half of FR-011: no option, no relaxation, and each value
// fatal on its own rather than only as a set.
func TestLoadFromRefusesEveryLayerOneValueIndependently(t *testing.T) {
	t.Parallel()

	for _, name := range []string{config.EnvAccessTeamDomain, config.EnvAccessAUD, config.EnvAccessAllowedEmails} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			pairs, _ := baseEnv(t)
			delete(pairs, name)

			if _, err := config.LoadFrom(env(pairs), io.Discard); err == nil {
				t.Fatalf("started with %s unset, so the browser door has nothing to verify against", name)
			}
		})
	}
}

func TestConfigFormattingRedactsTheSecret(t *testing.T) {
	t.Parallel()

	pairs, _ := baseEnv(t)
	cfg := mustLoad(t, pairs)

	// Every verb a log line or a debug print might reach for.
	for _, verb := range []string{"%v", "%s", "%+v", "%#v", "%q"} {
		for _, subject := range []any{cfg, *cfg} {
			got := fmt.Sprintf(verb, subject)
			assertNoSecret(t, got, goodSecret)
			if !strings.Contains(got, "redacted") {
				t.Errorf("Sprintf(%q) = %s, want the secret redacted", verb, got)
			}
			// The allowed addresses are a list of real people. A count says
			// everything an operator reading a log line needs to know.
			if strings.Contains(got, goodEmail) {
				t.Errorf("Sprintf(%q) = %s, want the allowed addresses counted rather than named", verb, got)
			}
		}
	}
}

func TestLoadFromResolvesSymlinkedRoots(t *testing.T) {
	t.Parallel()

	target := t.TempDir()
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	pairs, _ := baseEnv(t)
	pairs[config.EnvAllowedRoots] = link

	cfg := mustLoad(t, pairs)

	// Resolving once here is what stops a swapped symlink from racing the
	// containment check on the request path.
	if want := resolve(t, target); cfg.Roots[0].Path != want {
		t.Errorf("Roots[0].Path = %q, want the resolved target %q", cfg.Roots[0].Path, want)
	}
}

func TestLoadFromKeepsMultipleRootsAsASet(t *testing.T) {
	t.Parallel()

	first, second := t.TempDir(), t.TempDir()

	pairs, _ := baseEnv(t)
	// The same directory twice, once with a redundant "." element, plus a
	// genuinely different one.
	pairs[config.EnvAllowedRoots] = strings.Join([]string{first, first + "/.", second}, ":")

	cfg := mustLoad(t, pairs)

	if len(cfg.Roots) != 2 {
		t.Fatalf("Roots = %v, want the two distinct directories", cfg.Roots)
	}
	if cfg.Roots[0].Path != resolve(t, first) || cfg.Roots[1].Path != resolve(t, second) {
		t.Errorf("Roots = %v, want [%q %q] in the configured order", cfg.Roots, first, second)
	}
}

func TestLoadFromWarnsLoudlyOnEveryDefaultedStart(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	want := filepath.Join(home, config.DefaultRootName)
	if err := os.Mkdir(want, 0o750); err != nil {
		t.Fatalf("mkdir default root: %v", err)
	}

	pairs, _ := baseEnv(t)
	delete(pairs, config.EnvAllowedRoots)
	pairs["HOME"] = home

	// Twice, with a fresh sink each time: FR-004 requires the warning on every
	// start that defaults, not only the first.
	for attempt := 1; attempt <= 2; attempt++ {
		var sink strings.Builder
		cfg, err := config.LoadFrom(env(pairs), &sink)
		if err != nil {
			t.Fatalf("start %d: LoadFrom() unexpected error: %v", attempt, err)
		}

		got := sink.String()
		if !strings.Contains(got, config.EnvAllowedRoots) {
			t.Errorf("start %d: warning %q does not name %s", attempt, got, config.EnvAllowedRoots)
		}
		if !strings.Contains(got, resolve(t, want)) {
			t.Errorf("start %d: warning %q does not name the path in force", attempt, got)
		}
		if !strings.Contains(got, "WARNING") {
			t.Errorf("start %d: warning %q is not prominent", attempt, got)
		}
		// The quickstart reads this with `head -5`, so the variable and the
		// path have to be inside the first five lines.
		if lines := strings.SplitN(got, "\n", 6); !strings.Contains(strings.Join(lines[:5], "\n"), want) {
			t.Errorf("start %d: the path is not within the first five lines of %q", attempt, got)
		}

		if len(cfg.Roots) != 1 || !cfg.Roots[0].IsDefault {
			t.Errorf("start %d: Roots = %v, want one root marked IsDefault", attempt, cfg.Roots)
		}
		if cfg.Roots[0].Path == home {
			t.Errorf("start %d: the default root is HOME itself, which makes the allowlist decorative", attempt)
		}
	}
}

func TestLoadFromIsSilentWhenRootsAreConfigured(t *testing.T) {
	t.Parallel()

	pairs, _ := baseEnv(t)
	// HOME is present and its code directory exists, so a warning here could
	// only come from warning unconditionally.
	home := t.TempDir()
	if err := os.Mkdir(filepath.Join(home, config.DefaultRootName), 0o750); err != nil {
		t.Fatalf("mkdir default root: %v", err)
	}
	pairs["HOME"] = home

	var sink strings.Builder
	if _, err := config.LoadFrom(env(pairs), &sink); err != nil {
		t.Fatalf("LoadFrom() unexpected error: %v", err)
	}
	if sink.String() != "" {
		t.Errorf("warned about the default root while %s was set: %q", config.EnvAllowedRoots, sink.String())
	}
}

// A warning that cannot be written is an allowlist nobody was told about, which
// is exactly what FR-004 forbids. Refusing to start is the only honest answer.
func TestLoadFromFailsWhenTheWarningCannotBeEmitted(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	if err := os.Mkdir(filepath.Join(home, config.DefaultRootName), 0o750); err != nil {
		t.Fatalf("mkdir default root: %v", err)
	}

	pairs, _ := baseEnv(t)
	delete(pairs, config.EnvAllowedRoots)
	pairs["HOME"] = home

	if _, err := config.LoadFrom(env(pairs), brokenWriter{}); err == nil {
		t.Fatal("started with the default root in force and no way to say so")
	}
}

func TestLoadFromRefusesWithoutAnEnvironment(t *testing.T) {
	t.Parallel()

	if _, err := config.LoadFrom(nil, io.Discard); err == nil {
		t.Fatal("LoadFrom(nil, …) invented a configuration out of nothing")
	}
}

// TestLoadReadsTheProcessEnvironment covers the one thing LoadFrom cannot: that
// Load() is wired to os.Getenv. It cannot be parallel — t.Setenv forbids it.
func TestLoadReadsTheProcessEnvironment(t *testing.T) {
	root := t.TempDir()
	t.Setenv(config.EnvSharedSecret, goodSecret)
	t.Setenv(config.EnvAllowedRoots, root)
	t.Setenv(config.EnvListen, "127.0.0.1:9001")
	t.Setenv(config.EnvAccessTeamDomain, goodTeamDomain)
	t.Setenv(config.EnvAccessAUD, goodAUD)
	t.Setenv(config.EnvAccessAllowedEmails, goodEmail)

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load() unexpected error: %v", err)
	}
	if cfg.Listen != "127.0.0.1:9001" || cfg.Roots[0].Path != resolve(t, root) {
		t.Errorf("Load() = %v, want the values from the process environment", cfg)
	}
}

func TestLoadRefusesAWeakProcessEnvironment(t *testing.T) {
	t.Setenv(config.EnvSharedSecret, "")
	t.Setenv(config.EnvAllowedRoots, t.TempDir())
	// Complete apart from the secret, so the refusal under test is the one the
	// test names rather than whichever check happens to run first.
	t.Setenv(config.EnvAccessTeamDomain, goodTeamDomain)
	t.Setenv(config.EnvAccessAUD, goodAUD)
	t.Setenv(config.EnvAccessAllowedEmails, goodEmail)

	if _, err := config.Load(); err == nil {
		t.Fatal("Load() started with no shared secret")
	}
}

// resolve mirrors what the loader does to a root, so an assertion compares
// canonical paths on hosts where the temp directory is itself a symlink
// (/tmp → /private/tmp, and similar).
func resolve(t *testing.T, path string) string {
	t.Helper()
	got, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatalf("EvalSymlinks(%q): %v", path, err)
	}
	return got
}

func assertNoSecret(t *testing.T, text, secret string) {
	t.Helper()
	if secret != "" && strings.Contains(text, secret) {
		t.Errorf("the shared secret appears in %q", text)
	}
}

// assertNoAccessValues holds the layer-1 half of the same rule the secret has.
// These values are not secrets, but a team domain names an organisation and an
// allowed address names a person, and a startup error is written to stderr and
// kept in the journal for as long as the host keeps logs. The variable's name is
// what the operator needs; its value is what they already typed.
func assertNoAccessValues(t *testing.T, text string, pairs map[string]string) {
	t.Helper()
	for _, name := range []string{config.EnvAccessTeamDomain, config.EnvAccessAUD, config.EnvAccessAllowedEmails} {
		value := strings.TrimSpace(pairs[name])
		if value != "" && strings.Contains(text, value) {
			t.Errorf("the configured %s appears in %q", name, text)
		}
	}
}

type brokenWriter struct{}

func (brokenWriter) Write([]byte) (int, error) { return 0, errors.New("sink closed") }
