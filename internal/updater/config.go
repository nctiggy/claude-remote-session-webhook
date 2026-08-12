package updater

// config.go is the half of an update that is not the binary.
//
// # Why an update touches the operator's configuration file at all
//
// A release can rename a key or move the schema forward, and until this file
// existed the only thing that applied any of that was an operator running
// `crswd config migrate` by hand — a command nothing ever told them to run. The
// binary moved forward on its own and the file it reads stayed where it was.
// That is the shape of every "two fixes behind and no way to find out" this
// milestone exists about.
//
// # Why this is allowed to write, when FR-008 says the daemon never does
//
// FR-008 is about a *start*. A daemon that rewrote the operator's file every
// time it read one would put a diff nobody asked for into whatever source
// control they keep it under, and `crswd config check`'s whole value is that it
// can be run against a live host and change nothing. An update is the other
// thing entirely: the operator asked for it by name, with a confirming step, and
// it is already replacing the binary this file configures. Nothing here runs on
// a start, and a file that is already current is not written at all — not the
// file, and not a backup of it.
//
// # Why the result is loaded before it lands
//
// The daemon refuses to start on a configuration it cannot load. A migration
// that produced an unloadable file would turn a working daemon into a restart
// loop, and it would do it at the moment its operator is least able to look —
// mid-update, from a phone. So the migration is written beside the operator's
// file, read back off the disk it will be read off next time, and put through
// the same loader a startup goes through. Only then is it moved into place. A
// migration that does not validate is discarded and the update carries on with
// the configuration exactly as it was.
//
// The read-back is not ceremony. What gets validated has to be what gets
// renamed, and the only way to say that about bytes on a disk is to read them
// from there.

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"

	"github.com/nctiggy/claude-remote-session-webhook/internal/config"
)

// migratingSuffix names the migrated file while it is being checked, beside the
// operator's own so the rename that follows is inside one directory and
// therefore atomic.
//
// A deterministic name rather than a random one, because this is a file the
// operator may find: a host that lost power between the write and the rename
// leaves it behind, and `config.migrating` beside `config` says what it is. The
// next migration overwrites it — config.WriteFile renames over the name rather
// than opening it, so neither a leftover nor a symlink planted under it is
// followed.
const migratingSuffix = ".migrating"

// ErrConfigWouldNotLoad is the migration that was thrown away: the rewritten
// file did not load, so the operator still has the one they had.
//
// It is a sentinel because it is the one failure here that is not about the
// filesystem. Every other error on this path means a write did not happen;
// this one means a write happened, into a file nothing will ever read, and was
// then undone on purpose.
var ErrConfigWouldNotLoad = errors.New("the migrated configuration would not load, so the operator's file was left as it was")

// ConfigMigrator brings the operator's configuration file to the schema this
// binary understands. It holds no state about an update in progress; one is safe
// to keep for the life of the daemon.
type ConfigMigrator struct {
	// path is the configuration file this daemon actually loaded, not the one it
	// would find by looking again. An operator whose CRSW_CONFIG_FILE names one
	// file while $XDG_CONFIG_HOME holds another must not have the second migrated
	// on the strength of the first — migrating a file the daemon does not read is
	// a change with no visible effect, which is the worst kind.
	//
	// Empty means there is no file: a daemon configured entirely by environment
	// has nothing to migrate, and creating one would be inventing a configuration
	// the operator never wrote.
	path string

	// getenv is what the validation below layers over the candidate, exactly as
	// LoadFrom layers it at a start. The question being asked is "would *this*
	// daemon still come up", and this daemon comes up with both.
	getenv func(string) string

	// validate is config.Validate, named as a field for the reason Stager.verify
	// is: a test has to watch a migration be discarded, and the only other way to
	// arrange that is to find a value that parses, fails to load, and keeps
	// failing to load for as long as this test lives.
	//
	// **The shipping build's value is config.Validate** — the same predicate the
	// settings page's edit asks before it writes — and
	// TestTheMigratorValidatesWithTheDaemonsOwnLoader pins it.
	validate func(contents []byte, getenv func(string) string) error
}

// NewConfigMigrator returns the migrator the daemon runs, for the configuration
// file it loaded at startup.
func NewConfigMigrator(path string, getenv func(string) string) *ConfigMigrator {
	return &ConfigMigrator{path: path, getenv: getenv, validate: config.Validate}
}

// Path is the file this migrator rewrites, or "" if there is none.
func (m *ConfigMigrator) Path() string { return m.path }

// Migrate rewrites the configuration file into the current schema and reports
// whether it wrote anything.
//
// False with a nil error is the ordinary answer and it means nothing happened:
// no file, or a file already on this schema. False with an error means the
// operator's file is untouched and says why — ErrConfigWouldNotLoad for a
// migration that was produced and then discarded, and a wrapped filesystem
// error for one that could not be written at all.
//
// Nothing here refuses an update. By the time this runs the binary is already
// installed, and a configuration that could not be migrated is a configuration
// the daemon was running perfectly well on a moment ago.
func (m *ConfigMigrator) Migrate() (migrated bool, err error) {
	if m.path == "" {
		return false, nil
	}

	info, err := os.Stat(m.path)
	if errors.Is(err, fs.ErrNotExist) {
		// Absence is not a failure here for the reason it is not one at startup
		// (FR-003): a daemon with no configuration file runs on its environment
		// and its defaults, and an update is no occasion to give it one.
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("inspect the configuration file %s: %w", m.path, err)
	}
	// The operator's own mode, carried onto both files this writes. A file they
	// deliberately left readable by their own group does not silently become
	// 0600, and one holding a secret was refused at startup if it was not.
	mode := info.Mode().Perm()

	current, err := os.ReadFile(m.path) //nolint:gosec // G304: the path is the configuration file this daemon loaded at startup, not anything a request named.
	if err != nil {
		return false, fmt.Errorf("read the configuration file %s: %w", m.path, err)
	}

	// Warnings are discarded rather than written into the journal. This daemon
	// parsed this same file at startup and said whatever there was to say about
	// it then; a second copy of those lines, hours later and attributed to an
	// update, tells the operator nothing they were not already told.
	next, changed, err := config.Migrate(m.path, current, io.Discard)
	if err != nil {
		return false, fmt.Errorf("migrate the configuration file %s: %w", m.path, err)
	}
	if !changed {
		// Nothing written, not even a backup. A migration that rewrote a file it
		// had no change to make is FR-008 in different clothes.
		return false, nil
	}

	staged := m.path + migratingSuffix
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

	if err = config.WriteFile(staged, next, mode); err != nil {
		return false, fmt.Errorf("stage the migrated configuration: %w", err)
	}

	// Read back off the disk rather than validated from the bytes still in hand,
	// so that what this daemon approves is what the next one will read.
	landed, err := os.ReadFile(staged) //nolint:gosec // G304: the path is the configuration file's own name with a constant suffix, composed here.
	if err != nil {
		return false, fmt.Errorf("read back the migrated configuration %s: %w", staged, err)
	}
	if err = m.validate(landed, m.getenv); err != nil {
		return false, fmt.Errorf("%w: %w", ErrConfigWouldNotLoad, err)
	}

	// The backup is written before the file it is a backup of is replaced, so the
	// ending where one of the two writes fails is the ending where the operator
	// still has both copies of something. It is also the file LoadFrom falls back
	// to when the live one stops loading (FR-010).
	if err = config.WriteFile(config.BackupPath(m.path), current, mode); err != nil {
		return false, fmt.Errorf("keep the configuration this update replaces: %w", err)
	}
	if err = os.Rename(staged, m.path); err != nil {
		return false, fmt.Errorf("install the migrated configuration over %s: %w", m.path, err)
	}
	return true, nil
}
