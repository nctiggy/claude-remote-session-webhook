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
// under source control where the reformat becomes a diff nobody asked for. This
// file does not open one either: it is handed bytes, so there is no path here
// for a write to be added to later.
//
// It carries the grammar and nothing else. Which keys exist, which have been
// renamed, what mode the file must be, and what happens when it is absent are
// decisions about a *configuration* rather than about a line of text, and they
// arrive with T004 to T006.

import (
	"fmt"
	"strings"
)

const (
	// envPrefix is what a file key is an environment variable name without.
	envPrefix = "CRSW_"

	commentPrefix     = "#"
	keyValueSeparator = "="
)

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

// ParseFile turns the bytes of a configuration file into the settings it makes.
//
// path is used to say *where* a defect is and for nothing else — no error in
// here carries the content of a line. A malformed line may be the shared secret
// with a typo in it, and a startup error is written to stderr and kept in the
// journal forever; a line number is everything the operator needs and nothing an
// attacker does.
func ParseFile(path string, data []byte) (*File, error) {
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

		// Only the ends are trimmed. Whitespace *inside* a value belongs to the
		// operator: start_commands holds whole command lines, and collapsing
		// their spaces would change what runs.
		//
		// A repeated key overwrites here, which is not the answer — refusing it
		// is, and that arrives with the rest of the file-level refusals in T004.
		// The grammar's job is to say what a line means, not which of two lines
		// meaning different things should win.
		f.values[key] = strings.TrimSpace(rawValue)
	}

	return f, nil
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
	for _, c := range key {
		if (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '_' {
			return fmt.Errorf("config file %s:%d has a key outside [a-z0-9_]; keys are lower-case, and are the environment variable without its %s prefix; refusing to start",
				path, line, envPrefix)
		}
	}
	return nil
}
