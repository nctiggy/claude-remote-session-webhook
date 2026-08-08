package config

// Writing the operator's file.
//
// Everything else in this package reads. This is the one file that writes, and
// it is separate for that reason: FR-008 says the daemon does not rewrite the
// operator's configuration on its own, and keeping the write path in one place
// is what makes "on its own" checkable rather than a claim.
//
// Two callers reach it, both explicit: `crswd config migrate`, and the settings
// page's edit. Neither happens without somebody asking.

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// ErrNotEditable is a key the browser may not set.
var ErrNotEditable = errors.New("that setting is not editable from a browser")

// Editable reports whether a settings page may write this key.
//
// # Why the answer is IsSecret
//
// Issue #49 argued that six settings are boundaries rather than preferences and
// that none should be browser-editable: the containment roots, who may sign in,
// which identity provider is believed, what gets typed into a shell, where it
// listens, and the shared secret.
//
// Four of those are already reachable by anyone holding the dashboard. That
// operator can create a session in an approved root running an assistant with
// permissions skipped — code execution as the owner of this process — and that
// session can edit this file and restart the daemon. A form does not grant power
// there; it makes an existing path convenient, and refusing it buys
// inconvenience rather than safety.
//
// What stays out is what IsSecret already names, and the reason is narrower and
// better than "it is a boundary": **a form that edits a secret puts the secret in
// the page, in the browser's history, and in a POST body.** The settings page
// spent a whole milestone learning to render these as `present` and never as a
// value; an input carrying one would undo that in the same file that does it.
//
// It is deliberately the same predicate the mode refusal and the render use.
// TestIsSecretIsTheOnlyClassifier exists to stop a second list of secret keys
// appearing, and a second list of "keys too sensitive to edit" is that list
// under another name.
func Editable(key string) bool { return !IsSecret(key) && VarForKey(key) != "" }

// Set returns the file's contents with one key given a new value.
//
// It edits rather than regenerates: the operator's comments, their ordering and
// their blank lines are theirs, and a daemon that reformatted a file to change
// one line would be taking a decision nobody asked for (FR-008). A key that is
// already present is replaced in place; one that is not is appended.
func Set(contents []byte, key, value string) []byte {
	var out []string
	replaced := false
	for _, line := range strings.Split(string(contents), "\n") {
		trimmed := strings.TrimSpace(line)
		if !replaced && trimmed != "" && !strings.HasPrefix(trimmed, "#") {
			if name, _, found := strings.Cut(trimmed, "="); found && strings.TrimSpace(name) == key {
				out = append(out, key+" = "+value)
				replaced = true
				continue
			}
		}
		out = append(out, line)
	}
	if !replaced {
		if len(out) > 0 && strings.TrimSpace(out[len(out)-1]) == "" {
			out = out[:len(out)-1]
		}
		out = append(out, key+" = "+value, "")
	}
	return []byte(strings.Join(out, "\n"))
}

// WriteFile replaces path with data, atomically, at mode.
//
// Through a temporary file in the same directory and a rename, so a reader never
// sees half a configuration and a crash mid-write leaves the previous file
// intact. A rename inside one directory is atomic; a write in place is not.
func WriteFile(path string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".crswd-config-*")
	if err != nil {
		return fmt.Errorf("stage a replacement for %s: %w", path, err)
	}
	name := tmp.Name()

	if err := writeAndSync(tmp, data, mode); err != nil {
		_ = os.Remove(name) //nolint:errcheck // the write already failed; a failed cleanup changes neither the error nor what the operator does.
		return fmt.Errorf("write %s: %w", path, err)
	}
	if err := os.Rename(name, path); err != nil {
		_ = os.Remove(name) //nolint:errcheck // same.
		return fmt.Errorf("replace %s: %w", path, err)
	}
	return nil
}

// writeAndSync writes, sets the mode, and flushes before the rename.
//
// The sync matters: a rename is atomic with respect to other readers, not with
// respect to power loss. Without it a crash can leave the new name pointing at
// bytes that were never written.
func writeAndSync(f *os.File, data []byte, mode os.FileMode) error {
	defer func() { _ = f.Close() }() //nolint:errcheck // the sync below is what this depends on; a close error after it says nothing new.

	if _, err := f.Write(data); err != nil {
		return err
	}
	if err := f.Chmod(mode); err != nil {
		return err
	}
	return f.Sync()
}

// Validate answers "would this daemon still start on these bytes?".
//
// It is the check that makes editing safe to do while running: the candidate
// goes through exactly the parser and loader a startup goes through, so a value
// that would stop the daemon coming up is refused while it is still up. Anything
// weaker would let a page write a file that only fails at the next restart —
// which is the worst time to find out, because by then nobody is watching.
//
// The environment is layered over the file exactly as LoadFrom layers it, which
// is the only arrangement that answers the question actually being asked. A file
// checked in isolation refuses every edit on a daemon that takes any part of its
// configuration from the environment — which is most of them, and was this
// project's own deployment until a day ago. What matters is whether THIS daemon
// would start, and this daemon starts with both.
func Validate(contents []byte, getenv func(string) string) error {
	file, err := ParseFile("the edited configuration", contents, io.Discard)
	if err != nil {
		return err
	}
	_, err = LoadFrom(func(name string) string {
		if v := getenv(name); v != "" {
			return v
		}
		if v, ok := file.Lookup(name); ok {
			return v
		}
		return ""
	}, io.Discard)
	return err
}
