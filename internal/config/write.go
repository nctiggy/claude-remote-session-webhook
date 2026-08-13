package config

// Writing the operator's file.
//
// Everything else in this package reads. This is the one file that writes, and
// it is separate for that reason: FR-008 says the daemon does not rewrite the
// operator's configuration on its own, and keeping the write path in one place
// is what makes "on its own" checkable rather than a claim.
//
// Nothing here runs at a start. Every caller is somebody asking: `crswd config
// migrate` and an update both rewrite a schema through MigrateFile below, the
// settings page's edit sets one key through Set and WriteFile, and
// internal/updater/place.go puts a systemd unit on disk through the same atomic
// write rather than a fourth copy of it.

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
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

const (
	// tempPrefix names the file an atomic write goes through, in the directory
	// it is writing into.
	tempPrefix = ".crswd-tmp-"

	// migratingSuffix names a migration while it is being checked, beside the
	// operator's own file so the rename that follows is inside one directory and
	// therefore atomic.
	//
	// A deterministic name rather than a random one, because this is a file the
	// operator may find: a host that lost power between the write and the rename
	// leaves it behind, and `config.migrating` beside `config` says what it is.
	// The next migration overwrites it — WriteFile renames over the name rather
	// than opening it, so neither a leftover nor a symlink planted under it is
	// followed.
	migratingSuffix = ".migrating"
)

// StagedPath is where a migration of path waits while it is checked, for the
// reason BackupPath is a rule rather than a constant: a test that asserts
// nothing was left behind has to name the same file the migration used.
func StagedPath(path string) string { return path + migratingSuffix }

// MigrateFile rewrites the configuration file at path into the current schema,
// keeps the file it replaced at BackupPath(path), and reports whether it wrote
// anything at all.
//
// # Why this is one function and not two
//
// Two things rewrite an operator's configuration: `crswd config migrate`, which
// they run, and an update, which runs unattended while they are somewhere else.
// They were written months apart and drifted immediately — one staged its result
// and checked it, the other wrote straight over the file, and each had its own
// copy of write-to-a-temporary-and-rename. The unattended one is the worse place
// to discover a difference, and the operator is the person who pays for it. What
// they disagreed about is now this function; what they still choose for
// themselves is accept, below.
//
// # What it guarantees
//
// A file that is not there, and a file already on this schema, are both "nothing
// to do": false and no error, with nothing written — not the file, and not a
// backup of it. A migration that rewrote a file it had no change to make would
// put a diff nobody asked for into whatever source control the operator keeps it
// under, which is FR-008 in different clothes.
//
// Otherwise the migration is written beside their file, read back off the disk
// it will be read off next time, and offered to accept. Only then is the backup
// written and the migration renamed into place. **Every error below leaves the
// operator holding exactly the file they had**, which is what makes this safe to
// run unattended: the daemon refuses to start on a configuration it cannot load,
// so a migration that landed and did not work would turn a working host into a
// restart loop at the moment its operator is least able to look.
//
// The read-back is not ceremony. What gets checked has to be what gets renamed,
// and the only way to say that about bytes on a disk is to read them from there.
//
// accept is the caller's last look at what landed, and it must not be nil. Its
// error is returned as it is, because a refusal this function did not make is
// one it cannot add anything true to: the update path answers "would this daemon
// still start", and the command answers the weaker question its environment can
// honestly answer. warn takes whatever the parse has to say about the file — the
// operator's terminal for the command, io.Discard for an update, which said it
// all at startup already.
func MigrateFile(path string, warn io.Writer, accept func(landed []byte) error) (migrated bool, err error) {
	info, err := os.Stat(path)
	if errors.Is(err, fs.ErrNotExist) {
		// Absence is not a failure here for the reason it is not one at startup
		// (FR-003): a daemon with no configuration file runs on its environment
		// and its defaults, and neither a migration nor an update is an occasion
		// to give it one.
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("inspect the configuration file %s: %w", path, err)
	}
	// The operator's own mode, carried onto both files this writes. A file they
	// deliberately left readable by their own group does not silently become
	// 0600, and one holding a secret has already been refused by the read that
	// got here.
	mode := info.Mode().Perm()

	current, err := os.ReadFile(path) //nolint:gosec // G304: the path is the configuration file this daemon loaded, or the one an operator named on their own command line.
	if err != nil {
		return false, fmt.Errorf("read the configuration file %s: %w", path, err)
	}

	next, changed, err := Migrate(path, current, warn)
	if err != nil {
		return false, fmt.Errorf("migrate the configuration file %s: %w", path, err)
	}
	if !changed {
		return false, nil
	}

	staged := StagedPath(path)
	defer func() {
		// Every ending removes it, the successful one included — after the rename
		// there is nothing at this name, and Remove says so. What is being
		// prevented is the other endings: a candidate configuration left beside
		// the real one is a second file for the next reader to work out the
		// standing of.
		if rmErr := os.Remove(staged); rmErr != nil && !errors.Is(rmErr, fs.ErrNotExist) {
			err = errors.Join(err, fmt.Errorf("remove the discarded migration %s: %w", staged, rmErr))
		}
	}()

	if err = WriteFile(staged, next, mode); err != nil {
		return false, fmt.Errorf("stage the migrated configuration: %w", err)
	}

	// Read back off the disk rather than checked from the bytes still in hand, so
	// that what is approved here is what the next reader will read.
	landed, err := os.ReadFile(staged) //nolint:gosec // G304: the path is the configuration file's own name with a constant suffix, composed here.
	if err != nil {
		return false, fmt.Errorf("read back the migrated configuration %s: %w", staged, err)
	}
	if err = accept(landed); err != nil {
		return false, err
	}

	// The backup is written before the file it is a backup of is replaced, so the
	// ending where one of the two writes fails is the ending where the operator
	// still has both copies of something. It is also the file LoadFrom falls back
	// to when the live one stops loading (FR-010).
	if err = WriteFile(BackupPath(path), current, mode); err != nil {
		return false, fmt.Errorf("keep the configuration this migration replaces: %w", err)
	}
	if err = os.Rename(staged, path); err != nil {
		return false, fmt.Errorf("install the migrated configuration over %s: %w", path, err)
	}
	return true, nil
}

// WriteFile replaces path with data, atomically, at mode.
//
// Through a temporary file in the same directory and a rename, so a reader never
// sees half a configuration and a crash mid-write leaves the previous file
// intact. A rename inside one directory is atomic; a write in place is not.
//
// The temporary file is named for this daemon and not for a configuration: what
// comes through here is also a systemd unit and the digest beside it
// (internal/updater/place.go), and a `.crswd-config-…` left in
// ~/.config/systemd/user by a crash mid-write would name the wrong thing to
// whoever found it.
func WriteFile(path string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, tempPrefix+"*")
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
// The candidate is layered under the environment and over nothing. WithoutConfigFile
// is what stops the loader reaching for the operator's file on disk, which would
// make this question unanswerable: the candidate IS that file, so its old
// contents would supply anything the new ones dropped and a removed key would
// validate as safe. It is also what made this pass on the author's machine and
// fail in CI, which is the second time in one evening that a check borrowed
// something from his home directory and called it a result.
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
	}, io.Discard, WithoutConfigFile())
	return err
}
