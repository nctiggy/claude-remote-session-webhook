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
// ReadFile is the only function here that opens anything, it opens read-only,
// and it hands the bytes to a parser that is given no way to write them back.
// `crswd config migrate` is the only code in this repository that writes a
// config file (FR-008).
//
// It carries the grammar and the refusals a *configuration* makes — which keys
// exist, which have been renamed, and which schema the file was written against
// — and the one refusal the *file on disk* makes: its mode (FR-007). What
// happens when it is absent arrives with T006.

import (
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
)

const (
	// envPrefix is what a file key is an environment variable name without.
	envPrefix = "CRSW_"

	commentPrefix     = "#"
	keyValueSeparator = "="

	// maxConfigFileBytes bounds what is read into memory before anything looks
	// at it. The largest plausible file this daemon has is a few hundred bytes,
	// so the bound is not about configuration — it is about the path being the
	// operator's own environment: an io.ReadAll aimed at /dev/zero by a typo in
	// a unit file is a daemon that never starts and never says why. Past the
	// bound it refuses rather than truncating, because a truncated file is a
	// daemon running on a subset of the bounds its operator wrote.
	maxConfigFileBytes = 1 << 20

	// maxKeyLen bounds what an error message is willing to quote back.
	//
	// The longest key this daemon has is 22 characters, so no legitimate key is
	// anywhere near this. The bound exists for the illegitimate case: `openssl
	// rand -hex 32` produces 64 characters that are all inside [a-z0-9_], so a
	// secret pasted onto a line without its key parses as a perfectly valid key
	// and would otherwise become an `unknown key %q` message with the secret in
	// it, written to stderr and kept in the journal forever. Over the bound, the
	// message says the line number and nothing else.
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

// renamedKeys maps a former spelling to its current one.
//
// It is empty today because nothing has been renamed yet, and it exists anyway
// because the alternative is discovering the need for it during the release that
// renames something. A key in here is *not* unknown: it loads, and startup says
// loudly that both spellings name one setting and which one to move to. That
// distinction is the whole point — it is what tells a typo apart from an
// operator whose file predates a rename.
//
// It stays empty until a rename actually ships. Entering a spelling that never
// shipped would invent version skew: an operator would be warned to migrate off
// a key no released daemon ever read.
var renamedKeys = map[string]string{}

// Vars is every environment variable the daemon reads, and therefore every key a
// configuration file may set. Order is the order config.go declares them.
//
// It is a function rather than a slice so a caller cannot append to the list the
// unknown-key check is made of. TestVarsNamesEveryDeclaredVariable pins it to the
// constants in config.go, so a variable added there and forgotten here fails the
// suite rather than becoming a key an operator is told does not exist.
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

// File is a parsed configuration file: the operator's own statement of how this
// daemon behaves.
//
// Values are held under the file's own spelling rather than under
// environment-variable names, because that is the spelling they were written in,
// the spelling IsSecret takes, and the spelling the settings page renders in its
// key column. Lookup converts at the single point where the other vocabulary is
// wanted, so the two spellings of one setting meet in exactly one place.
type File struct {
	path   string
	values map[string]string
}

// Path is the file these values came from, which the settings page names above
// the table so that "why did my edit do nothing?" can be answered by an operator
// who edited a different file from the one that was read.
//
// A nil *File is the deployment configured entirely by environment variables,
// and it reports no path rather than panicking on the way to saying so.
func (f *File) Path() string {
	if f == nil {
		return ""
	}
	return f.path
}

// Lookup answers with the file's value for an environment variable name, which
// is the form the precedence shim asks in: it hands one name to getenv and the
// same name here, so the two layers cannot end up disagreeing about which
// setting is being asked for.
//
// The second return distinguishes "the file sets this to the empty string" from
// "the file does not set this", which is the distinction the shim needs and the
// one a bare map lookup would throw away.
func (f *File) Lookup(name string) (string, bool) {
	if f == nil {
		return "", false
	}
	v, ok := f.values[KeyForVar(name)]
	return v, ok
}

// KeyForVar is the file key that sets an environment variable, and VarForKey is
// its inverse. Both are pure string surgery on purpose: a table would be a third
// place for the two spellings of one setting to disagree, and it would need
// maintaining every time config.go grows a variable.
func KeyForVar(name string) string {
	return strings.ToLower(strings.TrimPrefix(name, envPrefix))
}

// VarForKey maps a file key back to its environment variable name.
func VarForKey(key string) string { return envPrefix + strings.ToUpper(key) }

// ReadFile reads the configuration file at path, parses it, and refuses a file
// that holds a secret and is reachable by another account on this host (FR-007).
//
// The mode is taken from the open handle rather than from a second os.Stat of
// the name, so the permission this refusal is made about belongs to the bytes
// that were actually read. Checking one file and reading another is a
// distinction that only ever matters on the day it is being exploited.
//
// The mode is checked *after* the parse because the refusal is gated on what the
// file contains, and there is no way to know that without reading it. Reading
// first costs nothing: this process can already read the file, which is exactly
// what is wrong with the mode.
func ReadFile(path string, warn io.Writer) (*File, error) {
	handle, err := os.Open(path) //nolint:gosec // G304: the path is the operator's own config file, not something a request names.
	if err != nil {
		return nil, fmt.Errorf("config file %s cannot be opened; refusing to start: %w", path, err)
	}
	defer func() { _ = handle.Close() }()

	info, err := handle.Stat()
	if err != nil {
		return nil, fmt.Errorf("config file %s cannot be inspected; refusing to start: %w", path, err)
	}

	data, err := io.ReadAll(io.LimitReader(handle, maxConfigFileBytes+1))
	if err != nil {
		return nil, fmt.Errorf("config file %s cannot be read; refusing to start: %w", path, err)
	}
	if len(data) > maxConfigFileBytes {
		return nil, fmt.Errorf("config file %s is larger than %d bytes; refusing to start", path, maxConfigFileBytes)
	}

	f, err := ParseFile(path, data, warn)
	if err != nil {
		return nil, err
	}

	// A secret in a mode-0644 file has already leaked to every account on this
	// host, which is the whole reason a file is better than an Environment= line
	// in a unit that `systemctl show` prints to anyone who asks. Refusing is the
	// only answer that leaves the operator better off than they started: the
	// alternative is a daemon that starts, works, and never mentions it again.
	//
	// Group and world are one test rather than two. A file the operator's group
	// can read is a file read by however many accounts that group has, which is
	// a number nothing here can know; and 0o077 also catches the write bits,
	// where the exposure is not the secret leaving but a command line arriving.
	if perm := info.Mode().Perm(); perm&0o077 != 0 && f.holdsSecret() {
		return nil, fmt.Errorf("config file %s is mode %04o, so it is readable by other accounts on this host and may hold the shared secret; run chmod 600 %s",
			path, perm, path)
	}

	return f, nil
}

// holdsSecret reports whether this file sets any key IsSecret classifies as one.
//
// It is what makes the mode refusal something an operator can act on. A file
// holding only allowed_roots is not a secret file, and a daemon refusing to
// start over its mode would be demanding a change that protects nothing — the
// kind of refusal that teaches an operator to stop reading them.
//
// Asking IsSecret rather than testing for the key here is the point of T001:
// this refusal and the settings page's value column are the two places that
// decide what a secret is, and a second list is how they come to disagree.
func (f *File) holdsSecret() bool {
	if f == nil {
		return false
	}
	for key := range f.values {
		if IsSecret(key) {
			return true
		}
	}
	return false
}

// ParseFile turns the bytes of a configuration file into the settings it makes,
// writing the renamed-key warning to warn.
//
// path is used to say *where* a defect is and for nothing else — no error in
// here carries the content of a line. A malformed line may be the shared secret
// with a typo in it, and a startup error is written to stderr and kept in the
// journal forever; a line number is everything the operator needs and nothing an
// attacker does. The one exception is a key short enough to be a key (see
// maxKeyLen), because naming the misspelling is the difference between a
// five-second fix and an afternoon.
func ParseFile(path string, data []byte, warn io.Writer) (*File, error) {
	return parseFile(path, data, renamedKeys, warn)
}

// parseFile takes the rename table as a parameter rather than reading the
// package global, so the mechanism can be exercised with a fixture table instead
// of requiring a real rename to exist before it is known to work. A rename that
// is first proven by the release that needs it is a rename proven in production.
func parseFile(path string, data []byte, renames map[string]string, warn io.Writer) (*File, error) {
	if warn == nil {
		// Discarding here would make the renamed-key warning silent, and a file
		// that still works is the one thing that will never prompt an operator
		// to update it. LoadFrom makes the same substitution for the same reason.
		warn = os.Stderr
	}

	known := knownKeys()
	seen := make(map[string]int)
	f := &File{path: path, values: make(map[string]string)}

	for i, raw := range strings.Split(string(data), "\n") {
		line := i + 1
		text := strings.TrimSpace(strings.TrimSuffix(raw, "\r"))

		// `#` is a comment marker only where it is the first non-blank character
		// on a line. There are no trailing comments, because shared_secret may
		// legitimately contain one and stripping from the first `#` would
		// silently truncate a secret into a daemon that starts, looks healthy,
		// and rejects every request it is sent.
		if text == "" || strings.HasPrefix(text, commentPrefix) {
			continue
		}

		// The separator is the FIRST `=`, which is why this is Cut and never
		// Split: start_commands always carries `=` inside its value, and a
		// parser that refused the ambiguity would be refusing valid
		// configuration.
		rawKey, rawValue, found := strings.Cut(text, keyValueSeparator)
		if !found {
			return nil, fmt.Errorf("config file %s:%d is not a comment, blank, or key=value; refusing to start", path, line)
		}

		key := strings.TrimSpace(rawKey)
		if err := validateKey(path, line, key); err != nil {
			return nil, err
		}

		// The rename resolves before the repeated-key check rather than after,
		// so a file setting both spellings of one setting is the repetition it
		// is. Checked the other way round the two spellings look like two keys
		// and one silently overwrites the other — which is the exact outcome
		// refusing a repeat exists to prevent.
		if current, renamed := renames[key]; renamed {
			if err := warnRenamedKey(warn, path, line, key, current); err != nil {
				return nil, err
			}
			key = current
		}

		if first, dup := seen[key]; dup {
			return nil, fmt.Errorf("config file %s:%d repeats key %q, first set on line %d; refusing to start", path, line, key, first)
		}
		seen[key] = line

		// Only the ends are trimmed. Whitespace *inside* a value belongs to the
		// operator: start_commands holds whole command lines, and collapsing
		// their spaces would change what runs.
		value := strings.TrimSpace(rawValue)

		// version is the one key that is not a setting, so it is consumed here
		// rather than stored. Left in, the settings page would render a row for
		// something no environment variable sets and no operator can change the
		// meaning of.
		if key == versionKey {
			if err := checkSchemaVersion(path, line, value); err != nil {
				return nil, err
			}
			continue
		}

		// A key that maps to no variable is refused, never skipped. A misspelled
		// `alowed_roots` that quietly did nothing is how a containment boundary
		// ends up unset on a daemon whose operator believes they set it.
		if !known[key] {
			return nil, fmt.Errorf("config file %s:%d has unknown key %q; refusing to start", path, line, key)
		}

		f.values[key] = value
	}

	return f, nil
}

// knownKeys is the file spelling of every variable the daemon reads. It is built
// per parse rather than kept as a package map so there is no shared table for a
// caller to add a key to.
func knownKeys() map[string]bool {
	names := Vars()
	keys := make(map[string]bool, len(names))
	for _, name := range names {
		keys[KeyForVar(name)] = true
	}
	return keys
}

// validateKey holds a key to the alphabet an environment variable name maps onto.
//
// It refuses a key outside [a-z0-9_] rather than folding it to lower case, and
// the difference is load-bearing. IsSecret matches `shared_secret` exactly, so a
// parser that took `Shared_Secret` as written would hand T005's mode check a
// file that holds a secret under a key nothing classifies as one: the refusal
// would not fire and the secret would sit in a group-readable file. Folding
// would close that and open another, since two spellings would then set one
// setting — the repeated-key case wearing a disguise. Refusing is the only
// answer that leaves each setting exactly one spelling.
func validateKey(path string, line int, key string) error {
	if key == "" {
		return fmt.Errorf("config file %s:%d has no key before the %q; refusing to start", path, line, keyValueSeparator)
	}
	// Refused before anything quotes it. Everything past this point names the
	// key in its message, and a 64-character run of hex inside [a-z0-9_] is a
	// secret pasted where a key belongs far more often than it is a key.
	if len(key) > maxKeyLen {
		return fmt.Errorf("config file %s:%d has a key longer than %d characters, so no key this daemon has could be meant; refusing to start",
			path, line, maxKeyLen)
	}
	for _, c := range key {
		if (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '_' {
			return fmt.Errorf("config file %s:%d has a key outside [a-z0-9_]; keys are lower-case, and are the environment variable without its %s prefix; refusing to start",
				path, line, envPrefix)
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
//
// The value is never quoted back. `version = <secret>` is a plausible typo, and
// the number the operator wrote is not information the message needs.
func checkSchemaVersion(path string, line int, value string) error {
	n, err := strconv.Atoi(value)
	if err != nil {
		return fmt.Errorf("config file %s:%d has a version that is not a whole number; refusing to start", path, line)
	}
	if n < 1 {
		return fmt.Errorf("config file %s:%d has version %d; the first schema is 1; refusing to start", path, line, n)
	}
	if n > SchemaVersion {
		return fmt.Errorf("config file %s:%d has version %d; this daemon understands %d; refusing to start", path, line, n, SchemaVersion)
	}
	return nil
}

// warnRenamedKey says loudly that a key loaded under its former spelling.
//
// A warning and not a refusal, because the operator's file is not wrong — it is
// old, and refusing to start over a spelling this daemon still understands would
// make every rename a breaking change. Loud, and on every start rather than
// once: the file still works, so nothing else will ever prompt anyone to change
// it. A write failure is fatal for the reason warnDefaultRoot's is — a warning
// nobody receives is a warning that was not emitted.
func warnRenamedKey(warn io.Writer, path string, line int, former, current string) error {
	banner := fmt.Sprintf(
		"crswd: config file %s:%d: %q was renamed to %q; accepting it for now — both spellings set the same thing, so update the file when convenient.\n",
		path, line, former, current)

	if _, err := io.WriteString(warn, banner); err != nil {
		return fmt.Errorf("emit the renamed-key warning for the config file at line %d: %w", line, err)
	}
	return nil
}
