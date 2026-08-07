package config

// The configuration file (#65). It is a second *source* for the values config.go
// already validates, and deliberately not a second set of rules: everything a
// file supplies goes through exactly the loader an environment variable goes
// through, so a value cannot mean one thing in a unit and another in a file.
//
// The format is `key = value` with `#` comments, flat, no sections and no
// nesting. That choice is forced rather than preferred: docs/security.md §5 and
// the constitution keep this repository free of `go.sum`, which rules out YAML
// and TOML — neither has a standard-library parser and neither is safe to
// hand-roll. JSON was the other candidate and would have deleted the commentary
// in config.example, which is the most useful documentation this repo has about
// what each bound is for.
//
// A key is its environment variable minus the CRSW_ prefix, lower-cased:
// `allowed_roots` is CRSW_ALLOWED_ROOTS. That is a rule and not a table, so a
// variable added to config.go is readable from a file the same day, and a key
// that maps to no variable is an unknown key rather than a silent no-op.
//
// **Nothing here ever writes.** The operator's file is the operator's; a daemon
// that reformats one has taken a decision that was not its to take, especially
// under source control where the reformat becomes a diff nobody asked for.

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const (
	// envPrefix is what a file key is an environment variable name without.
	envPrefix = "CRSW_"

	xdgConfigHomeVar = "XDG_CONFIG_HOME"

	// The ordinary Linux arrangement: ~/.local/bin/crswd, the unit under
	// ~/.config/systemd/user, and this under ~/.config/crswd.
	configDirName      = "crswd"
	configFileName     = "config"
	defaultConfigHome  = ".config"
	commentPrefix      = "#"
	keyValueSeparator  = "="
	maxConfigFileBytes = 1 << 20

	// maxKeyLen bounds what an error message is willing to quote back.
	//
	// The longest key this daemon has is 22 characters, so no legitimate key is
	// anywhere near this. The bound exists for the illegitimate case: `openssl
	// rand -hex 32` produces 64 characters that are all within the key alphabet,
	// so a secret pasted onto a line without its key would otherwise be a
	// "unknown key %q" message with the secret in it, written to stderr and kept
	// in the journal forever. Over the bound, the message says the line number
	// and nothing else.
	maxKeyLen = 32

	// versionKey records which schema the file was written against. It is not a
	// setting and maps to no environment variable; absent means SchemaVersion 1,
	// which is what every hand-written file is.
	versionKey = "version"
)

// SchemaVersion is the newest config-file schema this daemon understands. A file
// declaring a higher one is refused rather than read optimistically: the reason
// to bump this is that a key changed meaning, and guessing at the new meaning is
// how a containment boundary ends up set to something the operator did not write.
const SchemaVersion = 1

// ErrConfigFile is returned for every defect in the file itself — unreadable,
// too permissive, malformed, or naming a key this daemon does not have. Callers
// branch on it to tell "your file is wrong" from "your configuration is wrong",
// which are different things for an operator to go and fix.
var ErrConfigFile = errors.New("configuration file")

// renamedKeys maps a former spelling to its current one.
//
// It is empty today because nothing has been renamed yet, and it exists anyway
// because the alternative is discovering the need for it during the release that
// renames something. A key in here is *not* unknown: it loads, and startup says
// loudly that both spellings name one setting and which one to move to. That
// distinction is the whole point — it is what tells a typo apart from an
// operator whose file predates a rename.
var renamedKeys = map[string]string{}

// Vars is every environment variable the daemon reads, and therefore every key a
// configuration file may set. Order is the order config.go declares them.
//
// It is a function rather than a slice so a caller cannot append to the list the
// unknown-key check is made of. TestVarsNamesEveryDeclaredVariable pins it to the
// constants in config.go, so a variable added there and forgotten here fails the
// build rather than becoming a key an operator is told does not exist.
func Vars() []string {
	return []string{
		EnvSharedSecret,
		EnvAllowedRoots,
		EnvListen,
		EnvMaxSessions,
		EnvDestroyOnShutdown,
		EnvSessionLifetime,
		EnvSessionLifetimeMax,
		EnvIdleTimeout,
		EnvIdleTimeoutMax,
		EnvCreateRatePerMin,
		EnvMaxBodyBytes,
		EnvAccessTeamDomain,
		EnvAccessAUD,
		EnvAccessAllowedEmails,
		EnvMaxStreams,
		EnvStartCommand,
		EnvStartCommands,
		EnvRemoteControlCommand,
	}
}

// KeyForVar is the file key that sets an environment variable, and VarForKey is
// its inverse. Both are pure string surgery on purpose: a table would be a third
// place for the two spellings of one setting to disagree.
func KeyForVar(name string) string {
	return strings.ToLower(strings.TrimPrefix(name, envPrefix))
}

// VarForKey maps a file key to its environment variable name.
func VarForKey(key string) string { return envPrefix + strings.ToUpper(key) }

// DefaultPath is where the daemon looks when no --config names a file:
// $XDG_CONFIG_HOME/crswd/config, falling back to ~/.config/crswd/config.
//
// It returns "" when neither variable gives an absolute directory to start from,
// which means "there is no default file to look for" and not an error — a
// container with no HOME is configured by environment variables, which is a
// deployment this daemon supports and always has.
func DefaultPath(getenv func(string) string) string {
	if dir := strings.TrimSpace(getenv(xdgConfigHomeVar)); filepath.IsAbs(dir) {
		return filepath.Join(dir, configDirName, configFileName)
	}
	if home := strings.TrimSpace(getenv(envHome)); filepath.IsAbs(home) {
		return filepath.Join(home, defaultConfigHome, configDirName, configFileName)
	}
	return ""
}

// readConfigFile reads and parses the file at path, returning the values keyed by
// environment variable name.
//
// required says the operator named this file themselves, with --config. An
// absent file is then a startup failure, because they said which file they meant
// and starting on defaults instead would silently ignore every bound in it. At
// the default path an absent file is simply the deployment that has none.
func readConfigFile(path string, required bool, warn io.Writer) (map[string]string, error) {
	f, err := os.Open(path) //nolint:gosec // G304: the path is the operator's own --config or their XDG default.
	if err != nil {
		if os.IsNotExist(err) && !required {
			return nil, nil
		}
		return nil, fmt.Errorf("%w %s cannot be opened; refusing to start: %w", ErrConfigFile, path, err)
	}
	defer func() { _ = f.Close() }()

	info, err := f.Stat()
	if err != nil {
		return nil, fmt.Errorf("%w %s cannot be inspected; refusing to start: %w", ErrConfigFile, path, err)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%w %s is not a regular file; refusing to start", ErrConfigFile, path)
	}
	// The shared secret lives in here. A secret in a mode-0644 file is a secret
	// that has already leaked to every account on the host, which is the whole
	// reason this file is better than an Environment= line in a unit that
	// `systemctl show` prints to anyone who asks. Refusing is the only answer
	// that leaves the operator better off than where they started.
	if perm := info.Mode().Perm(); perm&0o077 != 0 {
		return nil, fmt.Errorf("%w %s is mode %04o, so it is readable by other accounts on this host and may hold the shared secret; run `chmod 600 %s`; refusing to start",
			ErrConfigFile, path, perm, path)
	}

	data, err := io.ReadAll(io.LimitReader(f, maxConfigFileBytes+1))
	if err != nil {
		return nil, fmt.Errorf("%w %s cannot be read; refusing to start: %w", ErrConfigFile, path, err)
	}
	if len(data) > maxConfigFileBytes {
		return nil, fmt.Errorf("%w %s is larger than %d bytes; refusing to start", ErrConfigFile, path, maxConfigFileBytes)
	}

	return parseConfigFile(path, data, renamedKeys, warn)
}

// parseConfigFile turns the bytes of a config file into values keyed by
// environment variable name.
//
// **No error in here carries the content of a line.** A malformed line may be
// the shared secret with a typo in it, and a startup error is written to stderr
// and kept in the journal forever. A line number says where to look, which is
// everything the operator needs and nothing an attacker does. The one exception
// is a key short enough to be a key (see maxKeyLen), because naming the
// misspelling is the difference between a five-second fix and an afternoon.
//
// The rename table is a parameter rather than a package global read directly, so
// the mechanism can be exercised with a fixture table instead of requiring a
// real rename to exist before it is known to work.
func parseConfigFile(path string, data []byte, renames map[string]string, warn io.Writer) (map[string]string, error) {
	known := make(map[string]string, len(Vars()))
	for _, name := range Vars() {
		known[KeyForVar(name)] = name
	}

	values := make(map[string]string)
	seen := make(map[string]int)

	for i, raw := range strings.Split(string(data), "\n") {
		line := i + 1
		text := strings.TrimSpace(strings.TrimSuffix(raw, "\r"))
		if text == "" || strings.HasPrefix(text, commentPrefix) {
			continue
		}

		rawKey, value, found := strings.Cut(text, keyValueSeparator)
		if !found {
			return nil, fmt.Errorf("%w %s:%d is neither blank, a comment, nor `key = value`; refusing to start", ErrConfigFile, path, line)
		}
		key := strings.TrimSpace(rawKey)
		if err := validateKey(path, line, key); err != nil {
			return nil, err
		}
		if prev, dup := seen[key]; dup {
			return nil, fmt.Errorf("%w %s:%d sets %s again, first set on line %d; which one wins is not something to leave to the parser; refusing to start",
				ErrConfigFile, path, line, key, prev)
		}
		seen[key] = line

		// A `#` after a value is part of the value, not a comment. A shared
		// secret may contain one, and a parser that guessed would silently
		// truncate it into an authentication layer that refuses every request.
		value = strings.TrimSpace(value)

		if key == versionKey {
			if err := checkSchemaVersion(path, line, value); err != nil {
				return nil, err
			}
			continue
		}

		if current, renamed := renames[key]; renamed {
			if err := warnRenamedKey(warn, path, key, current); err != nil {
				return nil, err
			}
			key = current
		}

		name, ok := known[key]
		if !ok {
			return nil, fmt.Errorf("%w %s:%d sets %s, which this daemon does not read; a misspelled key that quietly did nothing is how a containment boundary ends up unset; refusing to start",
				ErrConfigFile, path, line, key)
		}
		values[name] = value
	}

	return values, nil
}

// validateKey holds a key to the alphabet an environment variable name maps
// onto, and refuses a long one without quoting it — see maxKeyLen.
func validateKey(path string, line int, key string) error {
	if key == "" {
		return fmt.Errorf("%w %s:%d has no key before the %q; refusing to start", ErrConfigFile, path, line, keyValueSeparator)
	}
	if len(key) > maxKeyLen {
		return fmt.Errorf("%w %s:%d has a key longer than %d characters, so no key this daemon has could be meant; refusing to start",
			ErrConfigFile, path, line, maxKeyLen)
	}
	for _, c := range key {
		if (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '_' {
			return fmt.Errorf("%w %s:%d has a key outside [a-z0-9_]; keys are lower-case, and are the environment variable without its %s prefix; refusing to start",
				ErrConfigFile, path, line, envPrefix)
		}
	}
	return nil
}

// checkSchemaVersion reads the one key that is not a setting.
//
// A file outlives the binary that reads it, and an operator who updates the
// daemon must not find it refusing to start because their file predates a
// rename. This is half of that: the version says which schema the file was
// written against, and renamedKeys is the other half. The daemon never writes
// this key — reading one it did not write is the point.
func checkSchemaVersion(path string, line int, value string) error {
	n, err := strconv.Atoi(value)
	if err != nil {
		return fmt.Errorf("%w %s:%d has a %s that is not a whole number; refusing to start", ErrConfigFile, path, line, versionKey)
	}
	if n < 1 {
		return fmt.Errorf("%w %s:%d has %s %d; the first schema is 1; refusing to start", ErrConfigFile, path, line, versionKey, n)
	}
	if n > SchemaVersion {
		return fmt.Errorf("%w %s:%d declares %s %d and this daemon reads at most %d; it was written for a newer crswd, and guessing at what its keys now mean is not something to do with a containment boundary; refusing to start",
			ErrConfigFile, path, line, versionKey, n, SchemaVersion)
	}
	return nil
}

// warnRenamedKey says loudly that a key loaded under its former spelling.
//
// Loud, and on every start rather than once: the file still works, so nothing
// else will ever prompt the operator to change it. A write failure is fatal for
// the reason warnDefaultRoot's is — a warning nobody receives is a warning that
// was not emitted.
func warnRenamedKey(warn io.Writer, path, former, current string) error {
	banner := fmt.Sprintf(
		"crswd: %s: %s has been renamed to %s. The old spelling still works and sets the same thing; update the file when convenient.\n",
		path, former, current)
	if _, err := io.WriteString(warn, banner); err != nil {
		return fmt.Errorf("emit the renamed-key warning for %s: %w", former, err)
	}
	return nil
}

// layeredEnv is the precedence rule, and it is one line for a reason: an
// environment variable overrides the file, the file overrides the defaults, and
// there is no third case.
//
// The variables stay because they are how a container is configured and how a
// test overrides one value without writing a file. The file becomes the source
// of truth and the variables become the override — which is also the order an
// operator debugging a live daemon expects, since the variable is the thing they
// can change without touching the file they will later forget they edited.
//
// Empty is unset, exactly as the rest of this package already treats it: a
// variable set to the empty string has never meant anything different here, and
// making it mean "override the file back to the default" would be a new rule
// invented at the seam between two sources.
func layeredEnv(getenv func(string) string, file map[string]string) func(string) string {
	if len(file) == 0 {
		return getenv
	}
	return func(name string) string {
		if v := getenv(name); v != "" {
			return v
		}
		return file[name]
	}
}
