package config_test

// The configuration file (#65). Every case here drives config.LoadFrom with a
// real file on disk and a fake environment, because the file is only worth
// having if it reaches the same loader an environment variable reaches — a test
// that parsed bytes and stopped would prove the parser and none of the point.

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nctiggy/claude-remote-session-webhook/internal/config"
)

// writeConfig puts contents at dir/name with the mode the daemon insists on.
func writeConfig(t *testing.T, dir, name, contents string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	return path
}

// fileEnv is an environment that supplies nothing at all, so anything the
// resulting Config carries came out of the file.
func fileEnv() func(string) string { return func(string) string { return "" } }

func TestLoadFromReadsEverySettingFromAFile(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	path := writeConfig(t, t.TempDir(), "config", `
# The containment boundary.
allowed_roots = `+root+`

shared_secret = `+goodSecret+`
listen = 127.0.0.1:9999
max_sessions = 3
session_lifetime = 8h
access_team_domain = `+goodTeamDomain+`
access_aud = `+goodAUD+`
access_allowed_emails = `+goodEmail+`
`)

	cfg, err := config.LoadFrom(fileEnv(), io.Discard, config.WithFile(path))
	if err != nil {
		t.Fatalf("LoadFrom() unexpected error: %v", err)
	}

	if got := string(cfg.SharedSecret); got != goodSecret {
		t.Errorf("SharedSecret from file = %q, want the file's value", got)
	}
	if cfg.Listen != "127.0.0.1:9999" {
		t.Errorf("Listen = %q, want the file's value", cfg.Listen)
	}
	if cfg.MaxSessions != 3 {
		t.Errorf("MaxSessions = %d, want 3", cfg.MaxSessions)
	}
	if cfg.SessionLifetime.Hours() != 8 {
		t.Errorf("SessionLifetime = %s, want 8h", cfg.SessionLifetime)
	}
	if len(cfg.Roots) != 1 || cfg.Roots[0].IsDefault {
		t.Errorf("Roots = %v, want the file's single configured root", cfg.Roots)
	}
	if cfg.AccessAUD != goodAUD {
		t.Errorf("AccessAUD = %q, want the file's value", cfg.AccessAUD)
	}
	if cfg.File != path {
		t.Errorf("File = %q, want %q — a settings page can only name the file the loader remembers", cfg.File, path)
	}
}

// The precedence rule, which is the one thing an operator debugging a live
// daemon has to be able to predict: the variable wins.
func TestEnvironmentOverridesTheFile(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	path := writeConfig(t, t.TempDir(), "config", "shared_secret = "+goodSecret+"\n"+
		"allowed_roots = "+root+"\n"+
		"max_sessions = 3\n"+
		"listen = 127.0.0.1:9999\n")

	cfg, err := config.LoadFrom(env(map[string]string{
		config.EnvMaxSessions: "7",
	}), io.Discard, config.WithFile(path))
	if err != nil {
		t.Fatalf("LoadFrom() unexpected error: %v", err)
	}

	if cfg.MaxSessions != 7 {
		t.Errorf("MaxSessions = %d, want the environment's 7 over the file's 3", cfg.MaxSessions)
	}
	if cfg.Listen != "127.0.0.1:9999" {
		t.Errorf("Listen = %q; a variable that overrides one key must not discard the rest of the file", cfg.Listen)
	}
}

// The file is a source, not a set of rules: a value that would be refused from
// the environment is refused identically from a file, and blamed on the same
// variable name so the message stays one message.
func TestAFileValueFacesTheSameValidation(t *testing.T) {
	t.Parallel()

	path := writeConfig(t, t.TempDir(), "config", "shared_secret = "+goodSecret+"\n"+
		"allowed_roots = "+t.TempDir()+"\n"+
		"listen = 0.0.0.0:8765\n")

	_, err := config.LoadFrom(fileEnv(), io.Discard, config.WithFile(path))
	if err == nil {
		t.Fatal("LoadFrom() accepted a non-loopback listen address from a file")
	}
	if !strings.Contains(err.Error(), "loopback") {
		t.Errorf("error = %q, want the loopback refusal the environment gets", err)
	}
}

// A missing file at the default path is every deployment that exists today.
func TestAMissingDefaultFileIsNotAnError(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	root := t.TempDir()
	cfg, err := config.LoadFrom(env(map[string]string{
		"HOME":                 home,
		config.EnvSharedSecret: goodSecret,
		config.EnvAllowedRoots: root,
	}), io.Discard)
	if err != nil {
		t.Fatalf("LoadFrom() with no file: %v", err)
	}
	if cfg.File != "" {
		t.Errorf("File = %q, want empty — a file that was not there was not read", cfg.File)
	}
}

// A file the operator named themselves is different: they said which bounds they
// meant, and starting on defaults because of a typo in the path would be the
// daemon quietly running with none of them.
func TestAMissingNamedFileIsAnError(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "not-there")
	_, err := config.LoadFrom(fileEnv(), io.Discard, config.WithFile(path))
	if err == nil {
		t.Fatal("LoadFrom() started without the file --config named")
	}
	if !errors.Is(err, config.ErrConfigFile) {
		t.Errorf("error = %v, want ErrConfigFile", err)
	}
	if !strings.Contains(err.Error(), path) {
		t.Errorf("error = %q, want the path that could not be opened", err)
	}
}

func TestTheDefaultPathIsUnderXDGConfigHome(t *testing.T) {
	t.Parallel()

	xdg := t.TempDir()
	root := t.TempDir()
	dir := filepath.Join(xdg, "crswd")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	path := writeConfig(t, dir, "config", "shared_secret = "+goodSecret+"\nallowed_roots = "+root+"\n")

	cfg, err := config.LoadFrom(env(map[string]string{"XDG_CONFIG_HOME": xdg}), io.Discard)
	if err != nil {
		t.Fatalf("LoadFrom() unexpected error: %v", err)
	}
	if cfg.File != path {
		t.Errorf("File = %q, want %q", cfg.File, path)
	}
}

func TestTheDefaultPathFallsBackToDotConfig(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	dir := filepath.Join(home, ".config", "crswd")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	root := t.TempDir()
	path := writeConfig(t, dir, "config", "shared_secret = "+goodSecret+"\nallowed_roots = "+root+"\n")

	cfg, err := config.LoadFrom(env(map[string]string{"HOME": home}), io.Discard)
	if err != nil {
		t.Fatalf("LoadFrom() unexpected error: %v", err)
	}
	if cfg.File != path {
		t.Errorf("File = %q, want %q", cfg.File, path)
	}
}

func TestDefaultPath(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		vars map[string]string
		want string
	}{
		{"xdg wins", map[string]string{"XDG_CONFIG_HOME": "/xdg", "HOME": "/home/op"}, "/xdg/crswd/config"},
		{"home fallback", map[string]string{"HOME": "/home/op"}, "/home/op/.config/crswd/config"},
		{"relative xdg is ignored", map[string]string{"XDG_CONFIG_HOME": "relative", "HOME": "/home/op"}, "/home/op/.config/crswd/config"},
		// A container with no HOME is configured by variables, which is a
		// deployment this daemon has always supported.
		{"nothing to derive", map[string]string{}, ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := config.DefaultPath(env(tc.vars)); got != tc.want {
				t.Errorf("DefaultPath() = %q, want %q", got, tc.want)
			}
		})
	}
}

// The secret is the reason a file beats an Environment= line a `systemctl show`
// prints to anyone who asks. A mode-0644 file gives that advantage straight back.
func TestAReadableFileIsRefused(t *testing.T) {
	t.Parallel()

	for _, mode := range []os.FileMode{0o644, 0o640, 0o604, 0o666} {
		t.Run(mode.String(), func(t *testing.T) {
			t.Parallel()
			path := writeConfig(t, t.TempDir(), "config", "shared_secret = "+goodSecret+"\n")
			if err := os.Chmod(path, mode); err != nil {
				t.Fatalf("chmod: %v", err)
			}

			_, err := config.LoadFrom(fileEnv(), io.Discard, config.WithFile(path))
			if err == nil {
				t.Fatalf("LoadFrom() started on a mode-%04o config file", mode.Perm())
			}
			if !errors.Is(err, config.ErrConfigFile) {
				t.Errorf("error = %v, want ErrConfigFile", err)
			}
			msg := err.Error()
			for _, want := range []string{path, "chmod 600"} {
				if !strings.Contains(msg, want) {
					t.Errorf("error = %q, want it to contain %q — naming the file and the fix is the whole message", msg, want)
				}
			}
			if strings.Contains(msg, goodSecret) {
				t.Error("the refusal carried the secret it was refusing to expose")
			}
		})
	}
}

func TestAnOwnerOnlyFileIsAccepted(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	for _, mode := range []os.FileMode{0o600, 0o400, 0o700} {
		t.Run(mode.String(), func(t *testing.T) {
			t.Parallel()
			path := writeConfig(t, t.TempDir(), "config", "shared_secret = "+goodSecret+"\nallowed_roots = "+root+"\n")
			if err := os.Chmod(path, mode); err != nil {
				t.Fatalf("chmod: %v", err)
			}
			if _, err := config.LoadFrom(fileEnv(), io.Discard, config.WithFile(path)); err != nil {
				t.Errorf("LoadFrom() refused a mode-%04o file: %v", mode.Perm(), err)
			}
		})
	}
}

// A misspelled `alowed_roots` that silently did nothing is exactly how a
// containment boundary ends up unset.
func TestAnUnknownKeyIsAStartupFailure(t *testing.T) {
	t.Parallel()

	path := writeConfig(t, t.TempDir(), "config", "shared_secret = "+goodSecret+"\nalowed_roots = /tmp\n")

	_, err := config.LoadFrom(fileEnv(), io.Discard, config.WithFile(path))
	if err == nil {
		t.Fatal("LoadFrom() accepted a key the daemon does not read")
	}
	if !errors.Is(err, config.ErrConfigFile) {
		t.Errorf("error = %v, want ErrConfigFile", err)
	}
	if !strings.Contains(err.Error(), "alowed_roots") {
		t.Errorf("error = %q, want the misspelling named", err)
	}
	if !strings.Contains(err.Error(), ":2") {
		t.Errorf("error = %q, want the line number", err)
	}
}

func TestTheFileIsRefusedFor(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		contents string
		want     string
	}{
		{"a line that is not key = value", "shared_secret = x\nnonsense\n", ":2"},
		{"a line with no key", "= value\n", ":1"},
		{"a key outside the alphabet", "Shared_Secret = x\n", "[a-z0-9_]"},
		{"the same key twice", "max_sessions = 1\nmax_sessions = 2\n", "again"},
		{"a version that is not a number", "version = one\n", "whole number"},
		{"a version below the first schema", "version = 0\n", "the first schema is 1"},
		{"a version from a newer daemon", "version = 99", "newer crswd"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			path := writeConfig(t, t.TempDir(), "config", tc.contents)
			_, err := config.LoadFrom(fileEnv(), io.Discard, config.WithFile(path))
			if err == nil {
				t.Fatalf("LoadFrom() accepted %s", tc.name)
			}
			if !errors.Is(err, config.ErrConfigFile) {
				t.Errorf("error = %v, want ErrConfigFile", err)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %q, want it to contain %q", err, tc.want)
			}
		})
	}
}

// The rule the parser exists to keep: a malformed line may be the shared secret
// with a typo in it, and a startup error is written to stderr and lives in the
// journal forever.
func TestAParseErrorNeverCarriesTheLine(t *testing.T) {
	t.Parallel()

	// Each of these is the secret alone on a line, malformed in one of the ways
	// the parser refuses — no separator, no key, and a key long enough to be a
	// hex secret pasted where a key belongs.
	for _, contents := range []string{
		goodSecret + "\n",
		"= " + goodSecret + "\n",
		goodSecret + " = x\n",
		"0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef = x\n",
	} {
		path := writeConfig(t, t.TempDir(), "config", contents)
		_, err := config.LoadFrom(fileEnv(), io.Discard, config.WithFile(path))
		if err == nil {
			t.Fatalf("LoadFrom() accepted %q", contents)
		}
		for _, secret := range []string{goodSecret, "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"} {
			if strings.Contains(err.Error(), secret) {
				t.Errorf("error for a malformed line quoted it back: %q", err)
			}
		}
	}
}

// Comments and blank lines are the reason this format was chosen over JSON, and
// a `#` inside a value belongs to the value: a secret may contain one, and a
// parser that guessed would truncate it into an auth layer that refuses
// everything.
func TestCommentsAndValues(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	path := writeConfig(t, t.TempDir(), "config", `
# A leading comment.

   # An indented one.
shared_secret = a-secret#with-a-hash-in-it-32-bytes
   allowed_roots =    `+root+`
# start_command = never read, this line is a comment
`)

	cfg, err := config.LoadFrom(fileEnv(), io.Discard, config.WithFile(path))
	if err != nil {
		t.Fatalf("LoadFrom() unexpected error: %v", err)
	}
	if got := string(cfg.SharedSecret); got != "a-secret#with-a-hash-in-it-32-bytes" {
		t.Errorf("SharedSecret = %q; a `#` inside a value is part of the value", got)
	}
	if cfg.Roots[0].Path != root {
		t.Errorf("Roots[0] = %q, want %q — surrounding whitespace is trimmed", cfg.Roots[0].Path, root)
	}
	if cmd, _ := cfg.StartCommands.Command(""); cmd != config.DefaultStartCommand {
		t.Errorf("a commented-out key was read: start command = %q", cmd)
	}
}

// version is not a setting, and reading one the daemon did not write is the
// point of having it.
func TestTheCurrentSchemaVersionIsAccepted(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	path := writeConfig(t, t.TempDir(), "config",
		"version = 1\nshared_secret = "+goodSecret+"\nallowed_roots = "+root+"\n")

	if _, err := config.LoadFrom(fileEnv(), io.Discard, config.WithFile(path)); err != nil {
		t.Fatalf("LoadFrom() refused version %d: %v", config.SchemaVersion, err)
	}
}

// The operator's file is the operator's. A daemon that reformatted one — or
// wrote a version key into it — would have taken a decision that was not its to
// take, and under source control the reformat becomes a diff nobody asked for.
func TestTheDaemonNeverWritesTheFile(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	contents := "shared_secret = " + goodSecret + "\nallowed_roots = " + root + "\n"
	dir := t.TempDir()
	path := writeConfig(t, dir, "config", contents)

	if _, err := config.LoadFrom(fileEnv(), io.Discard, config.WithFile(path)); err != nil {
		t.Fatalf("LoadFrom() unexpected error: %v", err)
	}

	after, err := os.ReadFile(path) //nolint:gosec // G304: a path this test wrote.
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if string(after) != contents {
		t.Errorf("the daemon rewrote the file:\n%q", after)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	if len(entries) != 1 {
		t.Errorf("the daemon left %d files beside the config; it must leave none", len(entries)-1)
	}
}

func TestAFormattedConfigNeverSpellsTheFilesSecret(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	path := writeConfig(t, t.TempDir(), "config", "shared_secret = "+goodSecret+"\nallowed_roots = "+root+"\n")

	cfg, err := config.LoadFrom(fileEnv(), io.Discard, config.WithFile(path))
	if err != nil {
		t.Fatalf("LoadFrom() unexpected error: %v", err)
	}
	for _, formatted := range []string{cfg.String(), cfg.GoString()} {
		if strings.Contains(formatted, goodSecret) {
			t.Error("a formatted Config carried the secret it read from the file")
		}
		if !strings.Contains(formatted, path) {
			t.Errorf("a formatted Config does not name the file it read: %q", formatted)
		}
	}
}

// Vars is what the unknown-key check is made of, so a variable added to
// config.go and forgotten here becomes a key an operator is told does not exist.
func TestVarsNamesEveryDeclaredVariable(t *testing.T) {
	t.Parallel()

	declared := declaredVars(t)
	listed := make(map[string]bool)
	for _, name := range config.Vars() {
		if listed[name] {
			t.Errorf("config.Vars() names %s twice", name)
		}
		listed[name] = true
	}

	for name := range declared {
		if !listed[name] {
			t.Errorf("%s declares %s and config.Vars() omits it, so `%s` in a config file would be refused as unknown",
				configSourcePath, name, config.KeyForVar(name))
		}
	}
	for name := range listed {
		if !declared[name] {
			t.Errorf("config.Vars() names %s and %s does not declare it", name, configSourcePath)
		}
	}
}

func TestKeyForVarAndVarForKeyAreInverses(t *testing.T) {
	t.Parallel()

	for _, name := range config.Vars() {
		key := config.KeyForVar(name)
		if key == "" || strings.ContainsAny(key, " ABCDEFGHIJKLMNOPQRSTUVWXYZ") {
			t.Errorf("KeyForVar(%s) = %q, which is not a key this parser accepts", name, key)
		}
		if got := config.VarForKey(key); got != name {
			t.Errorf("VarForKey(KeyForVar(%s)) = %s", name, got)
		}
	}
}
