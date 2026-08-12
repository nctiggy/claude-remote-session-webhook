package updater

// What config.go has to be true of, against real files in a directory of this
// test's own — never the operator's ~/.config/crswd.
//
// Two of these cases carry the whole point of the file. The first is that a
// migration keeps everything: an operator's comments, their spacing, and every
// value they set, with the schema stamp added. The second is that a migration
// which would not load is thrown away with the original untouched — because the
// daemon refuses to start on a configuration it cannot read, and an update that
// wrote one would turn a working host into a restart loop at the moment its
// operator is least able to look.
//
// The validation is driven through the production predicate rather than through
// the seam wherever it can be, so what these cases prove about "would this load"
// is what the daemon itself would answer.

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/nctiggy/claude-remote-session-webhook/internal/config"
)

// loadableSecret is long enough for the loader to accept, and it is a literal in
// a test rather than anything this repository ships.
const loadableSecret = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

// configFixture is a configuration file on disk and the environment the daemon
// reading it would have.
type configFixture struct {
	path   string
	getenv func(string) string
}

// newConfigFixture writes contents to a config file in a directory of this
// test's own, and returns the fixture with an environment that loads.
//
// The environment supplies the secret and nothing else, so that everything the
// validation has to say about the *file* comes from the file. allowed_roots in
// particular is left to the fixtures: an environment that set it would override
// whatever they said and make the refusal case unarrangeable.
func newConfigFixture(t *testing.T, contents string, mode fs.FileMode) *configFixture {
	t.Helper()

	dir := t.TempDir()
	path := filepath.Join(dir, "config")
	if err := os.WriteFile(path, []byte(contents), mode); err != nil {
		t.Fatalf("write the fixture configuration: %v", err)
	}
	env := map[string]string{"CRSW_SHARED_SECRET": loadableSecret, envHome: dir}
	return &configFixture{path: path, getenv: func(name string) string { return env[name] }}
}

// root is a directory that exists, for a fixture's allowed_roots.
func (f *configFixture) root() string { return filepath.Dir(f.path) }

func (f *configFixture) migrator() *ConfigMigrator {
	return NewConfigMigrator(f.path, f.getenv)
}

func (f *configFixture) read(t *testing.T, path string) string {
	t.Helper()

	data, err := os.ReadFile(path) //nolint:gosec // G304: a path inside this test's own temporary directory.
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}

// absent fails unless nothing is at path.
func (f *configFixture) absent(t *testing.T, path, why string) {
	t.Helper()

	if _, err := os.Stat(path); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("%s exists after a migration that %s", path, why)
	}
}

// TestMigrateAddsWhatTheSchemaAddsAndKeepsEverythingElse is the case the
// milestone is for: a file written before this schema existed comes back
// current, with every line the operator wrote still in it.
//
// **Must fail when** the migration reformats, drops a comment, or loses a
// setting — the reason this format is not JSON is that the commentary carries
// why each bound is what it is, and a migration that took it away would cost the
// operator more than the stamp is worth.
func TestMigrateAddsWhatTheSchemaAddsAndKeepsEverythingElse(t *testing.T) {
	t.Parallel()

	fixture := newConfigFixture(t, "", 0o600)
	before := fmt.Sprintf(`# Why this daemon is bounded the way it is. The comment outlives the value.
allowed_roots = %s

# Two sessions, because this host has two cores to lose.
max_sessions = 2
`, filepath.Dir(fixture.path))
	if err := os.WriteFile(fixture.path, []byte(before), 0o600); err != nil {
		t.Fatalf("write the fixture configuration: %v", err)
	}

	migrated, err := fixture.migrator().Migrate()
	if err != nil {
		t.Fatalf("Migrate() = _, %v; want a migration", err)
	}
	if !migrated {
		t.Fatal("Migrate() reported nothing to do for a file with no schema version")
	}

	after := fixture.read(t, fixture.path)
	if want := fmt.Sprintf("version = %d", config.SchemaVersion); !strings.Contains(after, want) {
		t.Errorf("the migrated file has no %q line:\n%s", want, after)
	}
	for _, line := range strings.Split(before, "\n") {
		if line == "" {
			continue
		}
		if !strings.Contains(after, line) {
			t.Errorf("the migration did not carry this line through unchanged:\n%s", line)
		}
	}

	// The file it replaced, byte for byte, at the name LoadFrom falls back to
	// when the live one stops loading (FR-010).
	if got := fixture.read(t, config.BackupPath(fixture.path)); got != before {
		t.Errorf("the backup is not the file that was replaced:\n%s", got)
	}
	fixture.absent(t, fixture.path+migratingSuffix, "succeeded")

	// And the result is a file this daemon starts on, which is the only claim
	// that makes the write safe to have done at all.
	if err := config.Validate([]byte(after), fixture.getenv); err != nil {
		t.Errorf("the daemon would not start on the file the migration wrote: %v", err)
	}
}

// TestMigrateKeepsTheOperatorsMode holds the one property a second copy of a
// configuration file can quietly lose.
//
// **Must fail when** either file this writes is created at the umask's mode
// rather than the original's: the file may hold the shared secret, and a 0644
// backup of a 0600 file publishes it to every account on the host — the exact
// condition config.ReadFile refuses to start on.
func TestMigrateKeepsTheOperatorsMode(t *testing.T) {
	t.Parallel()

	fixture := newConfigFixture(t, "", 0o600)
	if err := os.WriteFile(fixture.path, []byte(fmt.Sprintf("allowed_roots = %s\n", fixture.root())), 0o600); err != nil {
		t.Fatalf("write the fixture configuration: %v", err)
	}

	if _, err := fixture.migrator().Migrate(); err != nil {
		t.Fatalf("Migrate() = _, %v; want a migration", err)
	}

	for _, path := range []string{fixture.path, config.BackupPath(fixture.path)} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("inspect %s: %v", path, err)
		}
		if got := info.Mode().Perm(); got != 0o600 {
			t.Errorf("%s is mode %04o after a migration of a 0600 file; want 0600", path, got)
		}
	}
}

// TestMigrateDiscardsAMigrationThatWouldNotLoad is the guard, proven by breaking
// it: a file that parses and does not load leaves the operator holding exactly
// what they had.
//
// The unloadable value is an allowed root that is not there, which is what the
// loader refuses at startup. It reaches the validation because the parse ahead
// of it is about grammar and keys — a migration cannot know that a directory
// stopped existing, which is the whole reason the result is loaded rather than
// assumed.
//
// **Must fail when** the migration is renamed into place before it has been
// loaded: the daemon exits for a restart moments later, and a configuration it
// cannot read turns that restart into a loop, from a phone, mid-update.
func TestMigrateDiscardsAMigrationThatWouldNotLoad(t *testing.T) {
	t.Parallel()

	const before = "allowed_roots = /definitely/not/a/directory/on/this/host\n"
	fixture := newConfigFixture(t, before, 0o600)

	migrated, err := fixture.migrator().Migrate()
	if !errors.Is(err, ErrConfigWouldNotLoad) {
		t.Fatalf("Migrate() = _, %v; want %v", err, ErrConfigWouldNotLoad)
	}
	if migrated {
		t.Error("Migrate() reported a migration it discarded")
	}

	if got := fixture.read(t, fixture.path); got != before {
		t.Errorf("the operator's file was changed by a migration that was refused:\n%s", got)
	}
	// No backup either. A .bak is what LoadFrom falls back to, and writing one of
	// a file that was never replaced would put a second copy of a configuration
	// beside the first for no reason anybody could reconstruct later.
	fixture.absent(t, config.BackupPath(fixture.path), "was refused")
	fixture.absent(t, fixture.path+migratingSuffix, "was refused")
}

// TestMigrateWritesNothingForAFileThatIsCurrent is FR-008 surviving this file.
//
// **Must fail when** an update rewrites a configuration it had no change to
// make: that is a diff nobody asked for in whatever source control the operator
// keeps the file under, and a fresh mtime on a file this daemon promises not to
// touch on its own.
func TestMigrateWritesNothingForAFileThatIsCurrent(t *testing.T) {
	t.Parallel()

	before := fmt.Sprintf("version = %d\n", config.SchemaVersion)
	fixture := newConfigFixture(t, "", 0o600)
	before += fmt.Sprintf("allowed_roots = %s\n", fixture.root())
	if err := os.WriteFile(fixture.path, []byte(before), 0o600); err != nil {
		t.Fatalf("write the fixture configuration: %v", err)
	}
	stamp, err := os.Stat(fixture.path)
	if err != nil {
		t.Fatalf("inspect the fixture configuration: %v", err)
	}

	migrated, err := fixture.migrator().Migrate()
	if err != nil {
		t.Fatalf("Migrate() = _, %v; want no change and no error", err)
	}
	if migrated {
		t.Errorf("Migrate() rewrote a file already on schema %d", config.SchemaVersion)
	}

	after, err := os.Stat(fixture.path)
	if err != nil {
		t.Fatalf("inspect the configuration after the migration: %v", err)
	}
	if !after.ModTime().Equal(stamp.ModTime()) {
		t.Errorf("a file with nothing to migrate was touched (%s → %s)", stamp.ModTime(), after.ModTime())
	}
	fixture.absent(t, config.BackupPath(fixture.path), "had nothing to do")
}

// TestMigrateHasNothingToDoWithoutAFile covers the two daemons that have no
// configuration file: one configured entirely by environment, and one whose file
// was removed between startup and the update.
//
// **Must fail when** either is treated as an error, or — worse — as an occasion
// to create a file the operator never wrote.
func TestMigrateHasNothingToDoWithoutAFile(t *testing.T) {
	t.Parallel()

	missing := filepath.Join(t.TempDir(), "config")
	for name, m := range map[string]*ConfigMigrator{
		"no configuration file at all": NewConfigMigrator("", os.Getenv),
		"a file that is not there":     NewConfigMigrator(missing, os.Getenv),
	} {
		migrated, err := m.Migrate()
		if err != nil {
			t.Errorf("%s: Migrate() = _, %v; want nothing to do", name, err)
		}
		if migrated {
			t.Errorf("%s: Migrate() reported a migration", name)
		}
	}
	if _, err := os.Stat(missing); !errors.Is(err, fs.ErrNotExist) {
		t.Error("a migration created a configuration file the operator never wrote")
	}
}

// TestMigrateRefusesAFileThatWillNotParse holds the line internal/config draws:
// a file whose grammar is broken is not rewritten, because rewriting it means
// guessing what the operator meant with their original already replaced by the
// guess.
//
// **Must fail when** an update "repairs" such a file. `crswd config check` is
// what says where the defect is, and the operator fixes it.
func TestMigrateRefusesAFileThatWillNotParse(t *testing.T) {
	t.Parallel()

	const before = "allowed_roots /this/line/has/no/separator\n"
	fixture := newConfigFixture(t, before, 0o600)

	if _, err := fixture.migrator().Migrate(); err == nil {
		t.Fatal("Migrate() = _, nil; want a refusal of a file the daemon will not read")
	}
	if got := fixture.read(t, fixture.path); got != before {
		t.Errorf("a file the migration refused was rewritten anyway:\n%s", got)
	}
	fixture.absent(t, config.BackupPath(fixture.path), "was refused")
}

// TestTheMigratorValidatesWithTheDaemonsOwnLoader pins the shipping build's
// answer to the one seam in config.go.
//
// **Must fail when** the default is left pointing at something weaker than the
// predicate the settings page's edit asks before it writes. A seam a test can
// replace is a seam production can be left holding the replacement of — and the
// weakest replacement of this one, a validator that approves everything, passes
// every other case in this file.
func TestTheMigratorValidatesWithTheDaemonsOwnLoader(t *testing.T) {
	t.Parallel()

	m := NewConfigMigrator("/somewhere/config", os.Getenv)
	if m.validate == nil {
		t.Fatal("a ConfigMigrator was built with no validation at all")
	}
	if reflect.ValueOf(m.validate).Pointer() != reflect.ValueOf(config.Validate).Pointer() {
		t.Fatal("a ConfigMigrator was built validating with something other than the loader a startup uses")
	}
}
