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
// # Why so little of that is in this file
//
// The staging, the read-back and the backup are config.MigrateFile's, and so is
// `crswd config migrate`'s — the command an operator runs and the migration an
// update runs unattended are one implementation, because the unattended one is
// the worse of the two places to discover a difference. What is left here is the
// part that is an update's alone: which file, whose environment, and the answer
// to "would this daemon still start on it".

import (
	"errors"
	"fmt"
	"io"

	"github.com/nctiggy/claude-remote-session-webhook/internal/config"
)

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
func (m *ConfigMigrator) Migrate() (bool, error) {
	if m.path == "" {
		return false, nil
	}
	// Warnings are discarded rather than written into the journal. This daemon
	// parsed this same file at startup and said whatever there was to say about
	// it then; a second copy of those lines, hours later and attributed to an
	// update, tells the operator nothing they were not already told.
	return config.MigrateFile(m.path, io.Discard, m.wouldStillStart)
}

// wouldStillStart is what an update asks of the migration that landed beside the
// operator's file, and it is the whole reason an update may write one at all: the
// candidate goes through the loader a start goes through, in this daemon's own
// environment, before anything is replaced.
//
// The failure is named rather than described, because the caller two layers up
// renders it: an update that discarded a migration is not an update that failed.
func (m *ConfigMigrator) wouldStillStart(landed []byte) error {
	if err := m.validate(landed, m.getenv); err != nil {
		return fmt.Errorf("%w: %w", ErrConfigWouldNotLoad, err)
	}
	return nil
}
