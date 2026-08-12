package config_test

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

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

// TestWorkdirSuggestionsIsRead is the loader assertion this repository keeps
// having to make (T005). CRSW_DESTROY_ON_SHUTDOWN had a constant, a field, a
// settings row and a consumer, and no loader — so it was false on every daemon
// that ever ran and nothing said so. A key whose value never reaches Config is
// exactly that again, and the union in suggestions.go would union an empty list
// forever while every test around it passed.
//
// It asserts the whole trip: unset is nothing, a configured list arrives in the
// operator's own order, and the shim recorded where it came from — the settings
// page reads that record, and it is the page that caught the last one.
// TestTheLifetimeCeilingHasASpellingForNever is milestone 13's operator-facing
// half: the ceiling is what decides whether a session on this host may be
// exempt from the one deadline that is never renewed, so it needs a value that
// says so and cannot be arrived at by mistake.
//
// The refusals are the point rather than the acceptance. `0` and `-1h` are both
// things a person writes in a configuration file meaning "no time at all", and
// either of them silently meaning the opposite would switch off that deadline on
// a host running unsandboxed shells.
//
// **Must fail when** a negative duration is accepted as a second spelling of
// `never`, when `never` does not reach the manager as the absence of a ceiling,
// or when a ceiling that is not there is refused for sitting "below" its own
// default.
func TestTheLifetimeCeilingHasASpellingForNever(t *testing.T) {
	t.Parallel()

	t.Run("never is carried as no ceiling at all", func(t *testing.T) {
		t.Parallel()

		pairs, _ := baseEnv(t)
		pairs[config.EnvSessionLifetimeMax] = config.NeverLifetime

		cfg := mustLoad(t, pairs)
		if cfg.SessionLifetimeMax >= 0 {
			t.Fatalf("SessionLifetimeMax = %v; want a negative, which is what resolveLifetimes reads as an absent ceiling", cfg.SessionLifetimeMax)
		}
		// The default is untouched by it, and that asymmetry is the design: the
		// ceiling opens the door and a create walks through it. A daemon whose
		// every session were immortal without asking is not what was configured
		// here.
		if cfg.SessionLifetime <= 0 {
			t.Errorf("SessionLifetime = %v; an unbounded ceiling must not disable the default every create still gets", cfg.SessionLifetime)
		}
	})

	// A word the operator typed, in whatever case their editor left it.
	t.Run("the word is read case-insensitively", func(t *testing.T) {
		t.Parallel()

		pairs, _ := baseEnv(t)
		pairs[config.EnvSessionLifetimeMax] = " Never "

		if got := mustLoad(t, pairs).SessionLifetimeMax; got >= 0 {
			t.Errorf("SessionLifetimeMax = %v; want an absent ceiling", got)
		}
	})

	// The ordinary ceiling still loads, and still bounds. Removing a bound must
	// not remove the setting.
	t.Run("a duration is still a duration", func(t *testing.T) {
		t.Parallel()

		pairs, _ := baseEnv(t)
		pairs[config.EnvSessionLifetimeMax] = "72h"

		if got := mustLoad(t, pairs).SessionLifetimeMax; got != 72*time.Hour {
			t.Errorf("SessionLifetimeMax = %v, want 72h", got)
		}
	})

	t.Run("a negative duration is refused and told the word", func(t *testing.T) {
		t.Parallel()

		pairs, _ := baseEnv(t)
		pairs[config.EnvSessionLifetimeMax] = "-1h"

		_, err := config.LoadFrom(env(pairs), io.Discard)
		if err == nil {
			t.Fatal("LoadFrom(-1h) started a daemon whose lifetime ceiling is a negative duration; a second spelling of never is how one of them stops being documented")
		}
		for _, want := range []string{config.EnvSessionLifetimeMax, config.NeverLifetime} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("the refusal does not name %q, so the operator cannot tell what to write instead: %v", want, err)
			}
		}
	})
}

func TestWorkdirSuggestionsIsRead(t *testing.T) {
	t.Parallel()

	pairs, _ := baseEnv(t)
	if got := mustLoad(t, pairs).WorkdirSuggestions; len(got) != 0 {
		t.Errorf("WorkdirSuggestions = %v with %s unset, want nothing offered that the operator did not configure",
			got, config.EnvWorkdirSuggestions)
	}

	// Not under the temp root on purpose, and neither path exists: this list is
	// presentation, so nothing about it reads the filesystem or the allowlist.
	// A path here that is refused on create is the contract working
	// (contracts/directory-suggestions.md), and asserting it loads is what says
	// the check lives at create time rather than here.
	//
	// The surrounding spaces are the second half: an operator writes `a, b`, and
	// an entry that kept its space would be a suggestion no create could resolve.
	pairs[config.EnvWorkdirSuggestions] = "/srv/scratch, /srv/second"

	cfg := mustLoad(t, pairs)
	want := []string{"/srv/scratch", "/srv/second"}
	if got := cfg.WorkdirSuggestions; !slices.Equal(got, want) {
		t.Fatalf("WorkdirSuggestions = %v, want %v; %s is declared and nothing reads it, so the picker offers what the operator wrote nowhere",
			got, want, config.EnvWorkdirSuggestions)
	}
	if src := cfg.Sources[config.EnvWorkdirSuggestions]; src != config.SourceEnv {
		t.Errorf("Sources[%s] = %v, want %v; a key read outside the shim has no provenance, and /settings answers \"why did my edit do nothing?\" from that record",
			config.EnvWorkdirSuggestions, src, config.SourceEnv)
	}
}

// TestLoadFromDefaultsTheStartCommandToTodaysBehaviour is the promise the
// configurable start command was added under (#38): an operator who sets nothing
// gets exactly the daemon they had, typing exactly the line it always typed.
func TestLoadFromDefaultsTheStartCommandToTodaysBehaviour(t *testing.T) {
	t.Parallel()

	pairs, _ := baseEnv(t)
	// Set to whitespace rather than left out, so this covers both spellings of
	// "the operator configured nothing": empty is the default here exactly as it
	// is for every other optional variable.
	pairs[config.EnvStartCommand] = "  "
	pairs[config.EnvStartCommands] = ""
	cfg := mustLoad(t, pairs)

	if got := cfg.StartCommands.Len(); got != 1 {
		t.Errorf("StartCommands.Len() = %d, want 1; an unconfigured daemon offers one command", got)
	}
	cmd, ok := cfg.StartCommands.Command("")
	if !ok {
		t.Fatal("a create naming no start command resolves to nothing")
	}
	if cmd != config.DefaultStartCommand {
		t.Errorf("the default start command is %q, want %q", cmd, config.DefaultStartCommand)
	}
	if named, _ := cfg.StartCommands.Command(config.DefaultStartCommandName); named != cmd {
		t.Errorf("%q resolves to %q, want the same command the empty name resolves to (%q)",
			config.DefaultStartCommandName, named, cmd)
	}
	if _, ok := cfg.StartCommands.Command("rc"); ok {
		t.Error("a name nobody configured resolves to a command")
	}
}

// TestLoadFromTakesTheOperatorsStartCommands covers the two ways an operator
// configures one, together: the default replaced, and a second name beside it.
func TestLoadFromTakesTheOperatorsStartCommands(t *testing.T) {
	t.Parallel()

	const (
		mine = "claude --resume"
		rc   = "claude remote-control --permission-mode bypassPermissions"
	)

	pairs, _ := baseEnv(t)
	pairs[config.EnvStartCommand] = mine
	pairs[config.EnvStartCommands] = "rc=" + rc + ",quiet=claude --print"

	cfg := mustLoad(t, pairs)

	for name, want := range map[string]string{
		"":                             mine,
		config.DefaultStartCommandName: mine,
		"rc":                           rc,
		"quiet":                        "claude --print",
	} {
		got, ok := cfg.StartCommands.Command(name)
		if !ok {
			t.Errorf("Command(%q) resolved to nothing", name)
			continue
		}
		if got != want {
			t.Errorf("Command(%q) = %q, want %q", name, got, want)
		}
	}

	// Sorted, so a dashboard renders the same order on every render.
	if got, want := strings.Join(cfg.StartCommands.Names(), ","), "default,quiet,rc"; got != want {
		t.Errorf("Names() = %s, want %s", got, want)
	}
}

// TestConfigFormattingNeverSpellsAStartCommand keeps the command lines out of
// anywhere a Config is formatted. They are not secret, but they are the closest
// thing this daemon has to an executable payload, and a log line is not where an
// operator should discover what their sessions are being started with.
func TestConfigFormattingNeverSpellsAStartCommand(t *testing.T) {
	t.Parallel()

	const rc = "claude remote-control --permission-mode bypassPermissions"

	pairs, _ := baseEnv(t)
	pairs[config.EnvStartCommands] = "rc=" + rc

	cfg := mustLoad(t, pairs)

	for _, verb := range []string{"%v", "%s", "%#v"} {
		got := fmt.Sprintf(verb, cfg)
		if strings.Contains(got, rc) {
			t.Errorf("Sprintf(%q) spells a start command line: %s", verb, got)
		}
		if !strings.Contains(got, "rc") {
			t.Errorf("Sprintf(%q) = %s, want the configured names named", verb, got)
		}
	}
}

// TestLoadFromResolvesTheRemoteControlCommand is what the dashboard's switch
// means on this daemon (#58): a name the operator can read out of their own
// configuration, never a command line the page decided on.
func TestLoadFromResolvesTheRemoteControlCommand(t *testing.T) {
	t.Parallel()

	const rc = "claude remote-control --permission-mode bypassPermissions --name {name}"

	// The whole point of naming a default: an operator who spells their command
	// `rc` gets a working switch without configuring a second thing.
	t.Run("rc by default when the operator configured one", func(t *testing.T) {
		t.Parallel()

		pairs, _ := baseEnv(t)
		pairs[config.EnvStartCommands] = "rc=" + rc
		cfg := mustLoad(t, pairs)

		if got := cfg.RemoteControlCommand; got != config.DefaultRemoteControlCommandName {
			t.Errorf("RemoteControlCommand = %q, want %q", got, config.DefaultRemoteControlCommandName)
		}
	})

	// A daemon that configures no remote control has no switch to offer, and
	// offering one whose only outcome is a refusal is worse than offering none.
	t.Run("empty when no such command is configured", func(t *testing.T) {
		t.Parallel()

		pairs, _ := baseEnv(t)
		pairs[config.EnvStartCommands] = ""
		cfg := mustLoad(t, pairs)

		if got := cfg.RemoteControlCommand; got != "" {
			t.Errorf("RemoteControlCommand = %q, want empty; nothing configured means no switch", got)
		}
	})

	// A daemon that spells it differently still works, which is the reason this
	// is configuration rather than a constant.
	t.Run("the operator's own name", func(t *testing.T) {
		t.Parallel()

		pairs, _ := baseEnv(t)
		pairs[config.EnvStartCommands] = "remote=" + rc
		pairs[config.EnvRemoteControlCommand] = "remote"
		cfg := mustLoad(t, pairs)

		if got := cfg.RemoteControlCommand; got != "remote" {
			t.Errorf("RemoteControlCommand = %q, want %q", got, "remote")
		}
		if cmd, ok := cfg.StartCommands.Command(cfg.RemoteControlCommand); !ok || cmd != rc {
			t.Errorf("the switch's name resolves to %q (ok=%t), want %q", cmd, ok, rc)
		}
	})
}

// A session's mode is derived from its start-command name against the name the
// operator configured as remote control (research R5), so a remote-control name
// the set does not carry is a mode no session could ever be in and a toggle
// whose only outcome is a refusal.
//
// contracts/session-mode.md puts that mismatch at startup rather than at the
// toggle: the operator is reading the daemon's output when it starts, and a
// refusal they meet hours later — on a running session, from a browser — tells
// them nothing about which of the two names they misspelled. This is the same
// refusal the table above exercises as one row among many, named for the
// contract it satisfies.
func TestModeNotInStartCommandsRefusedAtStartup(t *testing.T) {
	t.Parallel()

	pairs, _ := baseEnv(t)
	pairs[config.EnvStartCommands] = "rc=claude remote-control --name {name}"
	pairs[config.EnvRemoteControlCommand] = "remote"

	cfg, err := config.LoadFrom(env(pairs), io.Discard)
	if err == nil {
		t.Fatalf("LoadFrom() started a daemon whose remote-control name it configures no command for: %v", cfg)
	}
	if cfg != nil {
		t.Errorf("LoadFrom() returned a config alongside the refusal: %v", cfg)
	}

	// Both variables and the name that is in one and not the other: the operator
	// has two places they could have meant to spell it, and the refusal is only
	// actionable if it says which name it looked for and where it looked.
	for _, want := range []string{config.EnvRemoteControlCommand, config.EnvStartCommands, "remote"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal %q does not mention %q, so the operator is not told what to fix", err, want)
		}
	}
	assertNoSecret(t, err.Error(), pairs[config.EnvSharedSecret])
}

// TestRenderStartCommand pins the one substitution this daemon performs, and the
// boundary of what it will substitute.
//
// The whole safety argument for interpolating a value into a line typed at an
// unsandboxed shell is that a session name is ^[a-zA-Z0-9-]{1,64}$ and therefore
// cannot change the shape of that line. This is where that argument is checked
// rather than assumed — including with names no caller can actually reach here,
// because the day the two alphabets disagree is the day the substitution must
// refuse rather than proceed.
func TestRenderStartCommand(t *testing.T) {
	t.Parallel()

	const rc = "claude remote-control --permission-mode bypassPermissions --spawn=same-dir --name {name}"

	t.Run("substituted", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name    string
			command string
			session string
			want    string
		}{
			{
				name:    "no placeholder is untouched",
				command: config.DefaultStartCommand,
				session: "refactor-auth",
				want:    config.DefaultStartCommand,
			},
			{
				name:    "no placeholder and no name is still untouched",
				command: config.DefaultStartCommand,
				want:    config.DefaultStartCommand,
			},
			{
				name:    "the remote-control line",
				command: rc,
				session: "refactor-auth",
				want:    "claude remote-control --permission-mode bypassPermissions --spawn=same-dir --name refactor-auth",
			},
			{
				name:    "one character",
				command: rc,
				session: "a",
				want:    "claude remote-control --permission-mode bypassPermissions --spawn=same-dir --name a",
			},
			{
				name:    "the longest name there is",
				command: rc,
				session: strings.Repeat("z", 64),
				want:    "claude remote-control --permission-mode bypassPermissions --spawn=same-dir --name " + strings.Repeat("z", 64),
			},
			{
				name:    "all hyphens",
				command: rc,
				session: "---",
				want:    "claude remote-control --permission-mode bypassPermissions --spawn=same-dir --name ---",
			},
			{
				name:    "every placeholder is replaced",
				command: "claude --name {name} --tag {name}",
				session: "x-1",
				want:    "claude --name x-1 --tag x-1",
			},
			{
				// The shell's own braces are the shell's business. This daemon
				// never interprets them, so it never substitutes into them either.
				name:    "a shell expansion is left alone",
				command: "claude --dir ${HOME} --name {name}",
				session: "abc",
				want:    "claude --dir ${HOME} --name abc",
			},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()

				got, err := config.RenderStartCommand(tt.command, tt.session)
				if err != nil {
					t.Fatalf("RenderStartCommand(%q, %q) = %v", tt.command, tt.session, err)
				}
				if got != tt.want {
					t.Errorf("RenderStartCommand(%q, %q)\n got %q\nwant %q", tt.command, tt.session, got, tt.want)
				}
			})
		}
	})

	t.Run("refused", func(t *testing.T) {
		t.Parallel()

		// Each of these would change the shape of the command line, which is
		// exactly what the licence to substitute at all depends on being
		// impossible. None can reach here through Create — ValidateName refuses
		// them first — and each is refused here anyway.
		names := []string{
			"",
			"a b",
			"a;rm -rf /",
			"a`id`",
			"a$(id)",
			"a'b",
			`a"b`,
			"a\nb",
			"a|b",
			"a&b",
			"a>b",
			"a:b",
			"a.b",
			"a/b",
			strings.Repeat("z", 65),
		}
		for _, name := range names {
			t.Run(strconv.Quote(name), func(t *testing.T) {
				t.Parallel()

				got, err := config.RenderStartCommand(rc, name)
				if !errors.Is(err, config.ErrStartCommandName) {
					t.Fatalf("RenderStartCommand(_, %q) = %q, %v; want %v", name, got, err, config.ErrStartCommandName)
				}
				if got != "" {
					t.Errorf("a refused substitution returned %q; want no command line at all", got)
				}
			})
		}
	})
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
			// A suggestion outside the roots is deliberately *not* here — the
			// create refuses it, which is the contract. These two are the
			// entries no configuration could ever make usable.
			name: "workdir suggestion is relative",
			mutate: func(_ *testing.T, p map[string]string, _ string) {
				p[config.EnvWorkdirSuggestions] = "code"
			},
			wantIn: "absolute",
		},
		{
			name: "workdir suggestions have a trailing empty entry",
			mutate: func(_ *testing.T, p map[string]string, _ string) {
				p[config.EnvWorkdirSuggestions] = "/srv/scratch,"
			},
			wantIn: "empty entry",
		},
		// The three below take the Access values back out first. A non-loopback
		// address is refused because this daemon's dashboard would admit nobody,
		// not because the address is what it is — door_test.go has the pairing in
		// full, including the doors that permit these exact addresses.
		{
			name: "listen host is a wildcard and nothing admits a browser",
			mutate: func(_ *testing.T, p map[string]string, _ string) {
				withoutAccess(p)
				p[config.EnvListen] = "0.0.0.0:8765"
			},
			wantIn: "not loopback",
		},
		{
			name: "listen host is a routable address and nothing admits a browser",
			mutate: func(_ *testing.T, p map[string]string, _ string) {
				withoutAccess(p)
				p[config.EnvListen] = "192.168.1.10:8765"
			},
			wantIn: "not loopback",
		},
		{
			name: "listen host is the IPv6 wildcard and nothing admits a browser",
			mutate: func(_ *testing.T, p map[string]string, _ string) {
				withoutAccess(p)
				p[config.EnvListen] = "[::]:8765"
			},
			wantIn: "not loopback",
		},
		{
			// These two keep the Access door on, because a name is refused under
			// every door: 0.0.0.0 says where the listener will be and "" does not.
			name:   "listen host is empty, meaning every interface",
			mutate: func(_ *testing.T, p map[string]string, _ string) { p[config.EnvListen] = ":8765" },
			wantIn: "never a name",
		},
		{
			name:   "listen host is a name that could resolve anywhere",
			mutate: func(_ *testing.T, p map[string]string, _ string) { p[config.EnvListen] = "localhost:8765" },
			wantIn: "never a name",
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
		{
			// research D4: tmux's parser eats a ";" from the final argument before
			// -l applies, so this command would be typed truncated. Refused here
			// rather than delivered short, which is the failure that research
			// exists to prevent.
			name: "the start command carries a semicolon",
			mutate: func(_ *testing.T, p map[string]string, _ string) {
				p[config.EnvStartCommand] = "claude --dangerously-skip-permissions ; rm -rf /"
			},
			wantIn: config.EnvStartCommand,
		},
		{
			name: "a named start command carries a semicolon",
			mutate: func(_ *testing.T, p map[string]string, _ string) {
				p[config.EnvStartCommands] = "rc=claude remote-control ; echo hi"
			},
			wantIn: config.EnvStartCommands,
		},
		{
			// A newline would submit the line before it was finished, running half
			// a command and typing the rest at whatever came up.
			name: "a start command carries a newline",
			mutate: func(_ *testing.T, p map[string]string, _ string) {
				p[config.EnvStartCommand] = "claude\n--dangerously-skip-permissions"
			},
			wantIn: "control character",
		},
		{
			name:   "a start command entry has no name",
			mutate: func(_ *testing.T, p map[string]string, _ string) { p[config.EnvStartCommands] = "claude --print" },
			wantIn: config.EnvStartCommands,
		},
		{
			name:   "a start command entry is empty",
			mutate: func(_ *testing.T, p map[string]string, _ string) { p[config.EnvStartCommands] = "rc=claude,," },
			wantIn: "empty entry",
		},
		{
			name: "a start command name is outside the alphabet",
			mutate: func(_ *testing.T, p map[string]string, _ string) {
				p[config.EnvStartCommands] = "Remote Control=claude"
			},
			wantIn: "[a-z0-9-]",
		},
		{
			name: "a start command name is repeated",
			mutate: func(_ *testing.T, p map[string]string, _ string) {
				p[config.EnvStartCommands] = "rc=claude,rc=claude --print"
			},
			wantIn: "twice",
		},
		{
			name: "a named start command is empty",
			mutate: func(_ *testing.T, p map[string]string, _ string) {
				p[config.EnvStartCommands] = "rc="
			},
			wantIn: config.EnvStartCommands,
		},
		{
			// Which of two spellings of one value wins is the last thing to leave
			// to chance: the answer decides what is typed into an unsandboxed shell
			// on every create that names no command.
			name: "the default start command is set twice",
			mutate: func(_ *testing.T, p map[string]string, _ string) {
				p[config.EnvStartCommand] = "claude --resume"
				p[config.EnvStartCommands] = config.DefaultStartCommandName + "=claude --print"
			},
			wantIn: config.EnvStartCommand,
		},
		{
			// A typo is refused, and the refusal names the token. Typed through as
			// a literal it would be a brace expansion at a shell, and the operator
			// who wrote it believes a value is being substituted (#58).
			name: "a start command carries a misspelled placeholder",
			mutate: func(_ *testing.T, p map[string]string, _ string) {
				p[config.EnvStartCommands] = "rc=claude remote-control --dir {worrking_dir}"
			},
			wantIn: "{worrking_dir}",
		},
		{
			name: "the default start command carries an unknown placeholder",
			mutate: func(_ *testing.T, p map[string]string, _ string) {
				p[config.EnvStartCommand] = "claude --session {id}"
			},
			wantIn: "{id}",
		},
		{
			// The placeholder set is case-sensitive: a near miss is a miss, because
			// "close enough" is how a value silently stops being substituted.
			name: "a placeholder differs only in case",
			mutate: func(_ *testing.T, p map[string]string, _ string) {
				p[config.EnvStartCommands] = "rc=claude remote-control --name {Name}"
			},
			wantIn: "{Name}",
		},
		{
			// An operator who spelled their command differently has said which one
			// they mean; a switch that quietly started plain sessions instead is
			// the failure an unknown create name is refused to avoid.
			name: "the remote-control command names nothing configured",
			mutate: func(_ *testing.T, p map[string]string, _ string) {
				p[config.EnvStartCommands] = "rc=claude remote-control"
				p[config.EnvRemoteControlCommand] = "remote"
			},
			wantIn: config.EnvRemoteControlCommand,
		},
		{
			name: "the remote-control command name is outside the alphabet",
			mutate: func(_ *testing.T, p map[string]string, _ string) {
				p[config.EnvRemoteControlCommand] = "Remote Control"
			},
			wantIn: "[a-z0-9-]",
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
	// HOME is pointed at an empty directory, so this test is about the
	// environment it sets rather than about whoever runs it.
	//
	// Since the config file landed, Load falls through to
	// $XDG_CONFIG_HOME/crswd/config when a variable is empty — so on a developer
	// machine that has one, the secret this test clears is supplied by their own
	// file and Load succeeds. It failed the moment this project's author
	// migrated his own daemon to a config file, which is exactly the shape of
	// bug the installer's fresh-host job exists for: a test that reads the real
	// home directory is a test about that home directory.
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
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

// TestAccessIsOptional is #70: the daemon has to be runnable by someone without
// a Cloudflare account, which it was not.
//
// The all-or-nothing case is the one worth pinning. None of the three is a
// deployment this supports; some of them is an operator who configured a door
// and got one detail wrong — and a daemon that started anyway would refuse every
// login while looking correctly configured, which is worse than either.
func TestAccessIsOptional(t *testing.T) {
	t.Parallel()

	base := map[string]string{
		"CRSW_SHARED_SECRET": strings.Repeat("k", 32),
		"CRSW_ALLOWED_ROOTS": t.TempDir(),
		"CRSW_LISTEN":        "127.0.0.1:8765",
	}
	env := func(extra map[string]string) func(string) string {
		return func(k string) string {
			if v, ok := extra[k]; ok {
				return v
			}
			return base[k]
		}
	}

	t.Run("none configured starts, and says so", func(t *testing.T) {
		t.Parallel()

		var warn strings.Builder
		cfg, err := config.LoadFrom(env(nil), &warn)
		if err != nil {
			t.Fatalf("config.LoadFrom() with no Access configured = %v; want a daemon that starts", err)
		}
		if cfg.AccessTeamDomain != "" {
			t.Errorf("team domain = %q; want empty", cfg.AccessTeamDomain)
		}
		if !strings.Contains(warn.String(), "admits nobody") {
			t.Errorf("nothing warned that the dashboard is closed:\n%s", warn.String())
		}
	})

	t.Run("all three configured is accepted", func(t *testing.T) {
		t.Parallel()

		cfg, err := config.LoadFrom(env(map[string]string{
			"CRSW_ACCESS_TEAM_DOMAIN":    "https://team.cloudflareaccess.com",
			"CRSW_ACCESS_AUD":            "aud-value",
			"CRSW_ACCESS_ALLOWED_EMAILS": "op@example.com",
		}), &strings.Builder{})
		if err != nil {
			t.Fatalf("config.LoadFrom() with Access configured = %v", err)
		}
		if cfg.AccessTeamDomain == "" {
			t.Error("the team domain was dropped")
		}
	})

	// Each partial combination refuses, because each is a door with a hole in it.
	for name, extra := range map[string]map[string]string{
		"domain alone": {"CRSW_ACCESS_TEAM_DOMAIN": "https://team.cloudflareaccess.com"},
		"aud alone":    {"CRSW_ACCESS_AUD": "aud-value"},
		"emails alone": {"CRSW_ACCESS_ALLOWED_EMAILS": "op@example.com"},
		"two of three": {
			"CRSW_ACCESS_TEAM_DOMAIN": "https://team.cloudflareaccess.com",
			"CRSW_ACCESS_AUD":         "aud-value",
		},
	} {
		t.Run("refuses "+name, func(t *testing.T) {
			t.Parallel()

			if _, err := config.LoadFrom(env(extra), &strings.Builder{}); err == nil {
				t.Fatal("config.LoadFrom() started with a partly-configured identity provider; want a refusal")
			}
		})
	}
}
