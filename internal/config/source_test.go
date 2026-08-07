package config_test

// The four words below are what an operator reads in the settings page's source
// column to decide where to make a change that will actually take effect. The
// behavioural half of this test is trivial and the structural half is not: a
// fifth layer added to the shim is a word the page has never heard of, and no
// assertion about the four that exist can notice a fifth arriving. So the
// vocabulary is restated here as literals, and the package is walked for any
// Source constant it does not account for.

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/token"
	"io"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/nctiggy/claude-remote-session-webhook/internal/config"
)

// sourceTypeName is the type whose constants the settings page renders.
const sourceTypeName = "Source"

// sourceVocabulary is the whole of the source column, written out rather than
// derived from the package under test — a list read out of the answer agrees
// with the answer by construction, and a renamed word has to be catchable here.
var sourceVocabulary = []struct {
	name   string // as the package declares it
	source config.Source
	word   string // as the settings page prints it
}{
	{"SourceDefault", config.SourceDefault, "default"},
	{"SourceFile", config.SourceFile, "file"},
	{"SourceEnv", config.SourceEnv, "environment"},
	{"SourceFlag", config.SourceFlag, "flag"},
}

func TestSourceStringsAreTheSettingsPageVocabulary(t *testing.T) {
	t.Parallel()

	for _, tc := range sourceVocabulary {
		if got := tc.source.String(); got != tc.word {
			t.Errorf("%s.String() = %q, want %q: the settings page prints this word and an operator acts on it", tc.name, got, tc.word)
		}
	}

	// Provenance is a map keyed by environment-variable name, so a key nothing
	// supplied answers with the zero value. That has to be the layer meaning
	// "nothing supplied it", or an unrecorded key would claim to have come from
	// a file the operator never wrote.
	if config.SourceDefault != 0 {
		t.Errorf("SourceDefault = %d, want 0: an unrecorded key reads the zero value and must not report a layer it never came from", config.SourceDefault)
	}

	// A Source with no word of its own must not borrow one of these four. It
	// renders as its number instead, which is unmistakably a bug rather than an
	// answer to "where did this value come from?".
	unknown := config.SourceFlag + 1
	for _, tc := range sourceVocabulary {
		if got := unknown.String(); got == tc.word {
			t.Errorf("the Source one past SourceFlag renders as %q, which is %s's word: an unnamed layer must not read as a named one", got, tc.name)
		}
	}

	// The half that catches a fifth source. Adding one to the shim without
	// giving the page a word for it is invisible to every assertion above.
	fset, files := packageFiles(t)
	declared := sourceConstants(files)
	if len(declared) == 0 {
		t.Fatalf("no constant of type %s was found in this package, so the walk below is checking nothing; if the constants moved, sourceConstants has to move with them", sourceTypeName)
	}

	known := make(map[string]bool, len(sourceVocabulary))
	for _, tc := range sourceVocabulary {
		known[tc.name] = true
	}
	seen := make(map[string]bool, len(declared))
	for _, decl := range declared {
		seen[decl.name] = true
		if !known[decl.name] {
			t.Errorf("%s: %s is a configuration source the settings page has no word for. Add it to sourceVocabulary here and to %s.String(), or the source column silently gains a fifth meaning",
				fset.Position(decl.pos), decl.name, sourceTypeName)
		}
	}
	for _, tc := range sourceVocabulary {
		if !seen[tc.name] {
			t.Errorf("%s is asserted here but this package declares no such constant: the page's vocabulary and the shim's layers have drifted apart", tc.name)
		}
	}
}

// sourceConst is a declared configuration source, positioned so a failure names
// the line to go and look at.
type sourceConst struct {
	name string
	pos  token.Pos
}

// sourceConstants finds every constant in the package that is a configuration
// source.
//
// Two ways of spelling one, because the walk has to survive the next author's
// habits rather than the current author's: a constant typed Source — including
// the untyped continuations of an iota run, which carry the type of the spec
// that opened it — and a constant merely *named* Source-something, which catches
// `SourceExtra = Source(4)` in a block of its own. The name test costs a false
// positive on an unrelated Source-prefixed constant, which this package does not
// have and which would fail loudly and legibly if it ever did.
func sourceConstants(files map[string]*ast.File) []sourceConst {
	var found []sourceConst
	for _, file := range files {
		for _, decl := range file.Decls {
			gen, ok := decl.(*ast.GenDecl)
			if !ok || gen.Tok != token.CONST {
				continue
			}
			// An iota run is one spec with a type followed by specs with
			// neither a type nor a value; those inherit. A spec that supplies
			// its own value ends the inheritance.
			inRun := false
			for _, spec := range gen.Specs {
				value, ok := spec.(*ast.ValueSpec)
				if !ok {
					continue
				}
				switch {
				case value.Type != nil:
					ident, isIdent := value.Type.(*ast.Ident)
					inRun = isIdent && ident.Name == sourceTypeName
				case len(value.Values) > 0:
					inRun = false
				}
				for _, name := range value.Names {
					if inRun || strings.HasPrefix(name.Name, sourceTypeName) {
						found = append(found, sourceConst{name: name.Name, pos: name.Pos()})
					}
				}
			}
		}
	}
	return found
}

// The precedence shim (T007). Ordering is the security property here: reversed,
// a file left on a host silently overrides the environment a container was
// deployed with, and every test below still passes on the way to the wrong
// value. So each layer is asserted against the one beneath it by loading a real
// daemon configuration twice and changing exactly one thing.

// plantConfig writes a configuration file where the daemon looks for one:
// <configHome>/crswd/config, which is $XDG_CONFIG_HOME/crswd/config or
// ~/.config/crswd/config depending on which directory it is handed.
//
// The path is spelled out here rather than taken from config.DefaultPath. A
// fixture that asks the code under test where to write agrees with it by
// construction — including on the day it agrees about the wrong directory.
func plantConfig(t *testing.T, configHome, contents string) string {
	t.Helper()

	dir := filepath.Join(configHome, "crswd")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("create the fixture configuration directory: %v", err)
	}
	path := filepath.Join(dir, "config")
	// 0600, because these fixtures hold a secret and FR-007 refuses one that
	// another account could read.
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("write the fixture configuration file: %v", err)
	}
	return path
}

// homeWith is a HOME whose ~/.config/crswd/config holds contents.
func homeWith(t *testing.T, contents string) string {
	t.Helper()

	home := t.TempDir()
	plantConfig(t, filepath.Join(home, defaultConfigHomeName), contents)
	return home
}

// defaultConfigHomeName is ~/.config, restated rather than imported: the
// fallback location is what these tests are checking, so reading it out of the
// package under test would make every assertion about it vacuous.
const defaultConfigHomeName = ".config"

// fileLines is a whole configuration written in the file's own spelling, so a
// test can hand the daemon everything in a file and nothing in the environment.
func fileLines(root string, extra ...string) string {
	lines := []string{
		"shared_secret = " + goodSecret,
		"allowed_roots = " + root,
		"access_team_domain = " + goodTeamDomain,
		"access_aud = " + goodAUD,
		"access_allowed_emails = " + goodEmail,
	}
	return strings.Join(append(lines, append(extra, "")...), "\n")
}

// A value in the file is used, which is the assertion that fails when the parser
// has no caller — the exact bug left on the abandoned branch, and the one the
// plan says to watch for here. Every value below comes from the file and differs
// from the built-in default, so a daemon that never opened the file answers with
// the default and is caught.
func TestFileBeatsDefault(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	home := homeWith(t, fileLines(root,
		"listen = 127.0.0.1:9999",
		"max_sessions = 3",
		"max_streams = 4",
		"create_rate_per_min = 2",
		"max_body_bytes = 4096",
		"idle_timeout = 17m",
		"session_lifetime = 3h",
		"start_commands = rc=claude remote-control --name {name}",
	))

	cfg, err := config.LoadFrom(env(map[string]string{"HOME": home}), io.Discard)
	if err != nil {
		t.Fatalf("LoadFrom() with the whole configuration in a file: %v", err)
	}

	// The secret and the allowlist first: a daemon that read neither from the
	// file could not have started at all, so these two are what prove the file
	// is the source and not a decoration on one.
	if string(cfg.SharedSecret) != goodSecret {
		t.Errorf("the shared secret did not come from the file")
	}
	resolved, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatalf("resolve the fixture root: %v", err)
	}
	if len(cfg.Roots) != 1 || cfg.Roots[0].Path != resolved {
		t.Errorf("Roots = %v, want the one root the file names (%s)", cfg.Roots, resolved)
	}
	if cfg.Roots[0].IsDefault {
		t.Errorf("a root the file names is reported as the built-in default, so FR-004's warning fires at an operator who did set one")
	}

	for _, tc := range []struct {
		key       string
		got, want any
		builtIn   any
	}{
		{"listen", cfg.Listen, "127.0.0.1:9999", config.DefaultListen},
		{"max_sessions", cfg.MaxSessions, 3, config.DefaultMaxSessions},
		{"max_streams", cfg.MaxStreams, 4, config.DefaultMaxStreams},
		{"create_rate_per_min", cfg.CreateRatePerMin, 2, config.DefaultCreateRatePerMin},
		{"max_body_bytes", cfg.MaxBodyBytes, int64(4096), int64(config.DefaultMaxBodyBytes)},
		{"idle_timeout", cfg.IdleTimeout, 17 * time.Minute, time.Hour},
		{"session_lifetime", cfg.SessionLifetime, 3 * time.Hour, 24 * time.Hour},
		{"access_aud", cfg.AccessAUD, goodAUD, ""},
	} {
		if tc.got != tc.want {
			t.Errorf("%s = %v, want %v from the file", tc.key, tc.got, tc.want)
		}
		// Restated so the failure says which of the two ways it went wrong: a
		// value equal to the built-in default is a file that was never read,
		// which is a different defect from a file that was read wrongly.
		if tc.want == tc.builtIn {
			t.Errorf("%s: the fixture value equals the built-in default, so this row cannot tell a file that was read from one that was not", tc.key)
		}
	}

	// A named start command from a file reaches the set a create chooses from,
	// which is the value with the longest journey: it is parsed on the first `=`,
	// validated by loadStartCommands, and refused by it if it carried a ";".
	if cmd, ok := cfg.StartCommands.Command("rc"); !ok || cmd != "claude remote-control --name {name}" {
		t.Errorf("StartCommands.Command(%q) = %q, %v; want the command line the file gives it", "rc", cmd, ok)
	}
	if cfg.RemoteControlCommand != "rc" {
		t.Errorf("RemoteControlCommand = %q, want %q: the switch resolves against the set the file configured", cfg.RemoteControlCommand, "rc")
	}
}

// The environment beats the file, so a container or a test can change one value
// without writing one (FR-004). Reversed, a stale file silently overrides the
// environment a deployment was configured with.
func TestEnvBeatsFile(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	home := homeWith(t, fileLines(root,
		"listen = 127.0.0.1:9999",
		"max_sessions = 3",
		"idle_timeout = 17m",
	))

	cfg, err := config.LoadFrom(env(map[string]string{
		"HOME":                 home,
		config.EnvListen:       "127.0.0.1:8888",
		config.EnvMaxSessions:  "7",
		config.EnvSharedSecret: goodSecret + "-from-the-environment",
		config.EnvAccessAUD:    "audience-from-the-environment",
	}), io.Discard)
	if err != nil {
		t.Fatalf("LoadFrom() with a file and an environment: %v", err)
	}

	for _, tc := range []struct {
		what      string
		got, want any
		inFile    any
	}{
		{"listen", cfg.Listen, "127.0.0.1:8888", "127.0.0.1:9999"},
		{"max_sessions", cfg.MaxSessions, 7, 3},
		{"shared_secret", string(cfg.SharedSecret), goodSecret + "-from-the-environment", goodSecret},
		{"access_aud", cfg.AccessAUD, "audience-from-the-environment", goodAUD},
	} {
		if tc.got != tc.want {
			if tc.got == tc.inFile {
				t.Errorf("%s = %v: the file overrode the environment, so a stale file on a host beats the environment a container was deployed with", tc.what, tc.got)
				continue
			}
			t.Errorf("%s = %v, want %v", tc.what, tc.got, tc.want)
		}
	}

	// The layers are per key and not all-or-nothing: an environment that names
	// one setting must not switch the file off for the rest.
	if cfg.IdleTimeout != 17*time.Minute {
		t.Errorf("IdleTimeout = %s, want 17m: an environment naming other keys stopped the file answering for this one", cfg.IdleTimeout)
	}
}

// An empty variable falls through to the file. `CRSW_LISTEN=` in a unit is an
// operator clearing a variable, not setting it to a value the loader could use —
// and a shim that returned it anyway would answer with the built-in default
// while the operator's file sat there saying otherwise.
func TestEmptyEnvValueDoesNotBeatFile(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	home := homeWith(t, fileLines(root, "listen = 127.0.0.1:9999", "max_sessions = 3"))

	cfg, err := config.LoadFrom(env(map[string]string{
		"HOME": home,
		// Present and empty. A getenv cannot tell this from unset, which is
		// exactly why the shim tests the value and not its presence.
		config.EnvListen:      "",
		config.EnvMaxSessions: "",
	}), io.Discard)
	if err != nil {
		t.Fatalf("LoadFrom() with empty variables over a file: %v", err)
	}

	if cfg.Listen != "127.0.0.1:9999" {
		t.Errorf("Listen = %q, want the file's %q: an empty variable took precedence and the daemon fell to %q", cfg.Listen, "127.0.0.1:9999", config.DefaultListen)
	}
	if cfg.MaxSessions != 3 {
		t.Errorf("MaxSessions = %d, want the file's 3: an empty variable took precedence", cfg.MaxSessions)
	}
}

// A value from a file is refused by the same check, with the same message, as
// the same value from a variable. The file is a second *source* and never a
// second set of rules: a file layer with validation of its own is a bound that
// means one thing in a unit test and another in production.
func TestFileValueIsValidatedIdentically(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name  string
		key   string
		value string
	}{
		{"a listen host that is a name", "listen", "localhost:8765"},
		{"a listen host that is not loopback", "listen", "0.0.0.0:8765"},
		{"a session cap that is not a number", "max_sessions", "several"},
		{"a session cap below one", "max_sessions", "0"},
		{"an idle timeout that is not a duration", "idle_timeout", "soon"},
		{"a negative lifetime", "session_lifetime", "-1h"},
		// The one where identical validation is the security property rather
		// than a convenience: tmux's parser eats a trailing ";" before the line
		// is typed, so a start command carrying one is refused at startup. A
		// file that skipped that check would deliver a truncated command line to
		// an unsandboxed shell.
		{"a start command carrying a semicolon", "start_commands", "rc=claude; rm -rf /"},
		{"a start command with an unknown placeholder", "start_commands", "rc=claude --dir {working_dir}"},
		{"an allowed root that is not absolute", "allowed_roots", "relative/path"},
		{"an allowed address with whitespace in it", "access_allowed_emails", "one@example.com two@example.com"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			fromEnv, _ := baseEnv(t)
			fromEnv[config.VarForKey(tc.key)] = tc.value
			// No HOME, so DefaultPath finds nothing to read: this is the
			// pre-milestone path through the loader.
			_, envErr := config.LoadFrom(env(fromEnv), io.Discard)

			fromFile, _ := baseEnv(t)
			// The variable has to go, or the environment answers first and the
			// file's value is never validated at all — which is a green test
			// that proves the opposite of what it says.
			delete(fromFile, config.VarForKey(tc.key))
			fromFile["HOME"] = homeWith(t, tc.key+" = "+tc.value+"\n")
			_, fileErr := config.LoadFrom(env(fromFile), io.Discard)

			if envErr == nil {
				t.Fatalf("the environment accepted %s = %q, so this case proves nothing about the file", config.VarForKey(tc.key), tc.value)
			}
			if fileErr == nil {
				t.Fatalf("the file was allowed to set %s = %q, which the environment refuses", tc.key, tc.value)
			}
			if envErr.Error() != fileErr.Error() {
				t.Errorf("the file is validated by rules of its own:\n  from the environment: %v\n  from the file:        %v", envErr, fileErr)
			}
		})
	}
}

// SC-002: a daemon with no configuration file starts and behaves exactly as it
// did before there was one to have. Every deployment of this daemon is that
// daemon, so this is the assertion an upgrade rests on.
func TestNoFileMatchesTodayExactly(t *testing.T) {
	t.Parallel()

	// Four ways to have no file, including the two an upgrade actually meets:
	// nobody has ever run this with a file, so neither ~/.config/crswd nor the
	// file inside it exists.
	empty := t.TempDir()
	withConfigDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(withConfigDir, defaultConfigHomeName, "crswd"), 0o700); err != nil {
		t.Fatalf("create an empty configuration directory: %v", err)
	}

	homes := []struct {
		name string
		home string
	}{
		{"no HOME at all", ""},
		{"a HOME with nothing in it", empty},
		{"a HOME with an empty configuration directory", withConfigDir},
	}

	// The reference is the loader with no HOME, which is the environment every
	// unit test in this package has always used.
	reference, _ := baseEnv(t)
	want, err := config.LoadFrom(env(reference), io.Discard)
	if err != nil {
		t.Fatalf("LoadFrom() on a plain environment: %v", err)
	}

	// Restated as literals so this fails on the day absence changes a default,
	// not merely on the day the three loads disagree with each other.
	for _, tc := range []struct {
		what      string
		got, want any
	}{
		{"listen", want.Listen, config.DefaultListen},
		{"max_sessions", want.MaxSessions, config.DefaultMaxSessions},
		{"max_streams", want.MaxStreams, config.DefaultMaxStreams},
		{"create_rate_per_min", want.CreateRatePerMin, config.DefaultCreateRatePerMin},
		{"max_body_bytes", want.MaxBodyBytes, int64(config.DefaultMaxBodyBytes)},
		{"session_lifetime", want.SessionLifetime, 24 * time.Hour},
		{"idle_timeout", want.IdleTimeout, time.Hour},
		{"remote_control_command", want.RemoteControlCommand, ""},
	} {
		if tc.got != tc.want {
			t.Errorf("with no file, %s = %v, want the built-in %v", tc.what, tc.got, tc.want)
		}
	}
	if cmd, ok := want.StartCommands.Command(""); !ok || cmd != config.DefaultStartCommand {
		t.Errorf("with no file, the default start command = %q, %v; want the one this daemon has always typed", cmd, ok)
	}

	for _, home := range homes {
		t.Run(home.name, func(t *testing.T) {
			t.Parallel()

			pairs, _ := baseEnv(t)
			// The same roots as the reference, so the only difference between
			// the two loads is where the loader went looking for a file.
			pairs[config.EnvAllowedRoots] = reference[config.EnvAllowedRoots]
			if home.home != "" {
				pairs["HOME"] = home.home
			}

			got, err := config.LoadFrom(env(pairs), io.Discard)
			if err != nil {
				t.Fatalf("LoadFrom() with no configuration file: %v", err)
			}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("a daemon with no configuration file loaded a different configuration:\n got %v\nwant %v", got, want)
			}

			// And the errors too. A refusal that changed shape would break every
			// acceptance suite SC-002 is verified against.
			bad := maps.Clone(pairs)
			bad[config.EnvListen] = "0.0.0.0:8765"
			_, gotErr := config.LoadFrom(env(bad), io.Discard)

			badReference := maps.Clone(reference)
			badReference[config.EnvListen] = "0.0.0.0:8765"
			_, wantErr := config.LoadFrom(env(badReference), io.Discard)

			if gotErr == nil || wantErr == nil {
				t.Fatalf("a non-loopback listen address was accepted: %v, %v", gotErr, wantErr)
			}
			if gotErr.Error() != wantErr.Error() {
				t.Errorf("looking for a file changed a refusal:\n got %v\nwant %v", gotErr, wantErr)
			}
		})
	}
}

// Where the daemon looks (FR-001). The file it reads has to be the file the
// operator edited, and an operator who is told "~/.config/crswd/config" and
// whose XDG_CONFIG_HOME points elsewhere is editing the wrong one either way
// round — so which wins is asserted, not assumed.
func TestTheFileIsReadFromTheOperatorsConfigDirectory(t *testing.T) {
	t.Parallel()

	t.Run("XDG_CONFIG_HOME wins over HOME", func(t *testing.T) {
		t.Parallel()

		root := t.TempDir()
		home := homeWith(t, fileLines(root, "listen = 127.0.0.1:1111"))
		xdg := t.TempDir()
		plantConfig(t, xdg, fileLines(root, "listen = 127.0.0.1:2222"))

		cfg, err := config.LoadFrom(env(map[string]string{
			"HOME":           home,
			xdgConfigHomeVar: xdg,
		}), io.Discard)
		if err != nil {
			t.Fatalf("LoadFrom(): %v", err)
		}
		if cfg.Listen != "127.0.0.1:2222" {
			t.Errorf("Listen = %q, want the file under XDG_CONFIG_HOME", cfg.Listen)
		}
	})

	t.Run("HOME is the fallback", func(t *testing.T) {
		t.Parallel()

		root := t.TempDir()
		home := homeWith(t, fileLines(root, "listen = 127.0.0.1:1111"))

		cfg, err := config.LoadFrom(env(map[string]string{"HOME": home}), io.Discard)
		if err != nil {
			t.Fatalf("LoadFrom(): %v", err)
		}
		if cfg.Listen != "127.0.0.1:1111" {
			t.Errorf("Listen = %q, want the file under ~/.config/crswd", cfg.Listen)
		}
	})

	t.Run("a relative directory names no file", func(t *testing.T) {
		t.Parallel()

		// Joined to whatever the process was started in, this would make the
		// allowlist a session runs inside depend on somebody's shell.
		for _, pairs := range []map[string]string{
			{"HOME": "home/operator"},
			{xdgConfigHomeVar: ".config"},
		} {
			if path := config.DefaultPath(env(pairs)); path != "" {
				t.Errorf("DefaultPath(%v) = %q, want no file at all", pairs, path)
			}
		}
	})

	t.Run("no HOME names no file", func(t *testing.T) {
		t.Parallel()

		// The container deployment: configured entirely by variables, and a
		// daemon that refused to start without a HOME would break it.
		if path := config.DefaultPath(env(nil)); path != "" {
			t.Errorf("DefaultPath() = %q, want no file at all", path)
		}
	})

	t.Run("a defect in the file it found refuses at startup", func(t *testing.T) {
		t.Parallel()

		// The wiring assertion: a refusal that belongs to file.go can only reach
		// an operator if LoadFrom actually opened the file. A parser with no
		// caller passes every test in file_test.go and this one alone.
		pairs, _ := baseEnv(t)
		pairs["HOME"] = homeWith(t, "alowed_roots = /tmp\n")

		_, err := config.LoadFrom(env(pairs), io.Discard)
		if err == nil {
			t.Fatal("LoadFrom() started on a file with a misspelled key, so a containment boundary an operator believes they set does nothing")
		}
		if !strings.Contains(err.Error(), "alowed_roots") {
			t.Errorf("LoadFrom() = %v, want the misspelled key named", err)
		}
	})
}

// xdgConfigHomeVar is restated rather than exported from the package: these
// tests are what pin the two variables the daemon looks at, and a name read out
// of the answer agrees with the answer by construction.
const xdgConfigHomeVar = "XDG_CONFIG_HOME"

// Provenance (T008). The shim records which layer answered *as it answers*,
// which is the only moment the answer is known. Everything below is an assertion
// about that timing rather than about the four words: a source worked out after
// the load — by comparing the environment against the file — is right about
// every case except the one an operator is asking about.

// varWithNoLoader is the one CRSW_ constant LoadFrom never asks the shim for.
//
// CRSW_DESTROY_ON_SHUTDOWN has a constant, a Config.DestroyOnShutdown field, and
// a consumer in internal/httpapi. It has no loader: nothing in LoadFrom reads
// it, so the field is false in every daemon that has ever run and an operator
// who sets the variable changes nothing. That is a milestone-3 defect and not
// T008's to fix, but it is precisely the shape this test exists to catch — so it
// is named here rather than quietly skipped, and it is pinned in both
// directions. The day it gets a loader this line fails, and deleting it is the
// whole of the fix.
const varWithNoLoader = config.EnvDestroyOnShutdown

// Every setting the daemon reads has a recorded source, because every setting is
// read through the shim. A variable that reaches config.go without reaching the
// shim is a row the settings page renders as "default" whatever the operator
// wrote — the page lying about provenance is worse than a page without one.
func TestSourceRecordedForEveryKey(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	pairs, _ := baseEnv(t)
	// Both layers in play, so a key is recorded whichever one answers for it.
	pairs["HOME"] = homeWith(t, fileLines(root,
		"listen = 127.0.0.1:9999",
		"max_sessions = 3",
		"start_command = claude --dangerously-skip-permissions",
	))

	cfg, err := config.LoadFrom(env(pairs), io.Discard)
	if err != nil {
		t.Fatalf("LoadFrom(): %v", err)
	}

	for name := range declaredVars(t) {
		_, recorded := cfg.Sources[name]
		if name == varWithNoLoader {
			if recorded {
				t.Errorf("%s now has a recorded source, so it has a loader: delete varWithNoLoader and its exemption below, which exists only to name a variable nothing reads", name)
			}
			continue
		}
		if !recorded {
			t.Errorf("config.go declares %s and no lookup for it reached the precedence shim, so the settings page reports it as %q however it was configured",
				name, config.SourceDefault)
		}
	}
}

// A value written identically in the environment and the file still reports the
// environment. This is the case inference cannot get right: the two layers hold
// the same bytes, so nothing about the values says which one was used — and it
// is the case an operator hits, because they edited the file, saw no change, and
// came to the page to ask why.
func TestSourceIsNotInferred(t *testing.T) {
	t.Parallel()

	// One address, written in both layers. Identical on purpose.
	const inBothLayers = "127.0.0.1:9999"

	root := t.TempDir()
	home := homeWith(t, fileLines(root, "listen = "+inBothLayers, "max_sessions = 3"))

	cfg, err := config.LoadFrom(env(map[string]string{
		"HOME":           home,
		config.EnvListen: inBothLayers,
	}), io.Discard)
	if err != nil {
		t.Fatalf("LoadFrom(): %v", err)
	}
	if cfg.Listen != inBothLayers {
		t.Fatalf("Listen = %q, want %q from either layer", cfg.Listen, inBothLayers)
	}

	for _, tc := range []struct {
		what string
		name string
		want config.Source
		why  string
	}{
		{
			what: "a value the environment and the file spell identically",
			name: config.EnvListen,
			want: config.SourceEnv,
			why:  "the two layers hold the same bytes, so a source computed by comparing them cannot say which was used — and this is the only case where the answer matters",
		},
		{
			what: "a value only the file sets",
			name: config.EnvMaxSessions,
			want: config.SourceFile,
			why:  "the file answered this lookup",
		},
		{
			what: "a value neither layer sets",
			name: config.EnvMaxStreams,
			want: config.SourceDefault,
			why:  "nothing supplied it and the built-in default stands",
		},
	} {
		got, recorded := cfg.Sources[tc.name]
		if !recorded {
			t.Errorf("%s (%s) has no recorded source at all", tc.what, tc.name)
			continue
		}
		if got != tc.want {
			t.Errorf("%s (%s) reports %q, want %q: %s", tc.what, tc.name, got, tc.want, tc.why)
		}
	}
}

// The record is names and layers, and never a value. Recording provenance means
// the shim now sees every secret this daemon has at the moment it decides, so
// the one thing it must not grow is a line saying what it resolved: a startup
// warning is written to stderr and kept in the journal forever (FR-043).
func TestSecretNeverInProvenanceLog(t *testing.T) {
	t.Parallel()

	// A load that actually warns, so the assertion below is made about a sink
	// with something in it: no allowed_roots anywhere means FR-004's default-root
	// banner, which needs $HOME/code to exist.
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, config.DefaultRootName), 0o700); err != nil {
		t.Fatalf("create the default root: %v", err)
	}
	plantConfig(t, filepath.Join(home, defaultConfigHomeName), strings.Join([]string{
		"shared_secret = " + goodSecret,
		"access_team_domain = " + goodTeamDomain,
		"access_aud = " + goodAUD,
		"access_allowed_emails = " + goodEmail,
		"",
	}, "\n"))

	var warn bytes.Buffer
	cfg, err := config.LoadFrom(env(map[string]string{"HOME": home}), &warn)
	if err != nil {
		t.Fatalf("LoadFrom(): %v", err)
	}
	if warn.Len() == 0 {
		t.Fatal("nothing was written to the warning sink, so this test is searching an empty buffer for a secret it could never have found")
	}

	// The two the file holds and IsSecret classifies: the credential, and the
	// list naming who may reach this daemon.
	for _, value := range []struct{ what, value string }{
		{"the shared secret", goodSecret},
		{"an allowed address", goodEmail},
	} {
		if strings.Contains(warn.String(), value.value) {
			t.Errorf("startup wrote %s to the warning sink; a startup line is kept in the journal forever", value.what)
		}
		if rendered := fmt.Sprint(cfg.Sources); strings.Contains(rendered, value.value) {
			t.Errorf("the provenance record carries %s; it is keyed by variable name and holds a layer, so a value in it is a second copy of the secret", value.what)
		}
	}

	// And it does record where the secret came from, which is the point: the page
	// can say "file" without ever holding the value.
	if got := cfg.Sources[config.EnvSharedSecret]; got != config.SourceFile {
		t.Errorf("the shared secret's source is %q, want %q: the file set it", got, config.SourceFile)
	}
}
