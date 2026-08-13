package config_test

// What MigrateFile has to be true of, and — the half T007 is really about —
// that both migrations in this repository are it.
//
// `crswd config migrate` and the migration an update runs unattended were
// written months apart and drifted immediately: one staged its result and
// checked it before replacing anything, the other wrote straight over the
// operator's file, and each carried its own copy of write-to-a-temporary-and-
// rename. Two code paths that rewrite an operator's configuration differently is
// the drift this repository keeps finding, and the unattended one is the worse
// place to discover it — nobody is watching, and the daemon is about to restart
// on whatever was written. So the properties are asserted here once, and
// TestBothMigrationsAreThisOne asserts that "once" is enough.

import (
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/nctiggy/claude-remote-session-webhook/internal/config"
)

// accepted is an accept that approves whatever landed, for the cases that are
// not about the check.
func accepted([]byte) error { return nil }

// TestMigrateFileStagesAndKeepsWhatItReplaced is the ordinary ending: the file
// is current afterwards, the one it replaced is beside it byte for byte, and
// nothing else was left in the directory.
//
// **Must fail when** the migration is written over the operator's file directly.
// A staged file that is read back and checked is the only arrangement in which
// "the daemon still starts on this" can be said about the bytes that are
// actually going to be read, and it is what the update path has always done.
func TestMigrateFileStagesAndKeepsWhatItReplaced(t *testing.T) {
	t.Parallel()

	const before = `# Why this daemon is bounded the way it is. The comment outlives the value.
max_sessions = 2
`
	path := writeConfig(t, before, 0o600)

	var landed []byte
	migrated, err := config.MigrateFile(path, io.Discard, func(b []byte) error {
		landed = b
		// What accept is looking at is a file, on the disk it will be read off
		// next time, beside an operator's configuration that has not been touched
		// yet. All three halves of that are the arrangement: a check that ran
		// before the write, or after the rename, is a check of something other
		// than what the daemon is about to start on.
		if staged, _ := snapshot(t, config.StagedPath(path)); string(staged) != string(b) {
			t.Errorf("accept was given %q while %s holds %q", b, config.StagedPath(path), staged)
		}
		if live, _ := snapshot(t, path); string(live) != before {
			t.Errorf("the operator's file was already replaced when accept ran:\n%s", live)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("MigrateFile() = _, %v; want a migration", err)
	}
	if !migrated {
		t.Fatal("MigrateFile() reported nothing to do for a file with no schema version")
	}

	after, _ := snapshot(t, path)
	if want := fmt.Sprintf("version = %d", config.SchemaVersion); !strings.Contains(string(after), want) {
		t.Errorf("the migrated file has no %q line:\n%s", want, after)
	}
	if !strings.Contains(string(after), "# Why this daemon is bounded the way it is. The comment outlives the value.") {
		t.Errorf("the migration dropped the commentary, which is why this format is not JSON:\n%s", after)
	}

	// And what was approved is what was installed, byte for byte.
	if string(landed) != string(after) {
		t.Errorf("the bytes offered to accept are not the bytes that landed:\nchecked %q\ninstalled %q", landed, after)
	}

	if kept, _ := snapshot(t, config.BackupPath(path)); string(kept) != before {
		t.Errorf("%s is not the file that was replaced:\n%s", config.BackupPath(path), kept)
	}
	assertNothingLeftBehind(t, path)
}

// TestMigrateFileDiscardsWhatTheCallerRefuses is the guard, and the reason this
// function reads its own result back off the disk.
//
// **Must fail when** the migration is renamed into place before accept has seen
// it. The daemon refuses to start on a configuration it cannot load, so on the
// update path that ordering is the difference between a host that carries on and
// a host that restart-loops with nobody at the keyboard.
func TestMigrateFileDiscardsWhatTheCallerRefuses(t *testing.T) {
	t.Parallel()

	refused := errors.New("not this one")
	const before = "max_sessions = 2\n"
	path := writeConfig(t, before, 0o600)

	migrated, err := config.MigrateFile(path, io.Discard, func([]byte) error { return refused })
	if !errors.Is(err, refused) {
		t.Fatalf("MigrateFile() = _, %v; want the caller's own refusal %v — a refusal this function did not make is one it cannot add anything true to", err, refused)
	}
	if migrated {
		t.Error("MigrateFile() reported a migration it discarded")
	}

	if after, _ := snapshot(t, path); string(after) != before {
		t.Errorf("the operator's file was changed by a migration that was refused:\n%s", after)
	}
	// No backup either. A .bak is what LoadFrom falls back to, and writing one of
	// a file that was never replaced puts a second copy of a configuration beside
	// the first for no reason anybody could reconstruct later.
	if _, err := os.Stat(config.BackupPath(path)); err == nil {
		t.Error("a migration that was refused wrote a backup of a file it never replaced")
	}
	assertNothingLeftBehind(t, path)
}

// TestMigrateFileWritesNothingItHasNoReasonTo covers the two endings that write
// nothing at all: a file already on this schema, and no file.
//
// **Must fail when** either becomes a write. A migration that rewrote a file it
// had no change to make is a diff nobody asked for in whatever source control
// the operator keeps it under, and a migration that created one is a
// configuration they never wrote — a daemon with no file runs on its environment
// and its defaults (FR-003).
func TestMigrateFileWritesNothingItHasNoReasonTo(t *testing.T) {
	t.Parallel()

	t.Run("a file already on this schema", func(t *testing.T) {
		t.Parallel()

		path := writeConfig(t, fmt.Sprintf("version = %d\nmax_sessions = 2\n", config.SchemaVersion), 0o600)
		if err := os.Chtimes(path, longAgo, longAgo); err != nil {
			t.Fatalf("backdate the fixture: %v", err)
		}
		before, beforeInfo := snapshot(t, path)

		migrated, err := config.MigrateFile(path, io.Discard, accepted)
		if err != nil {
			t.Fatalf("MigrateFile() = _, %v; want nothing to do", err)
		}
		if migrated {
			t.Errorf("MigrateFile() rewrote a file already on schema %d", config.SchemaVersion)
		}

		after, afterInfo := snapshot(t, path)
		if string(after) != string(before) || !afterInfo.ModTime().Equal(beforeInfo.ModTime()) {
			t.Errorf("a file with nothing to migrate was touched (%s → %s)", beforeInfo.ModTime(), afterInfo.ModTime())
		}
		if _, err := os.Stat(config.BackupPath(path)); err == nil {
			t.Error("a migration with nothing to do wrote a backup anyway")
		}
	})

	t.Run("no file at all", func(t *testing.T) {
		t.Parallel()

		path := filepath.Join(t.TempDir(), "config")
		migrated, err := config.MigrateFile(path, io.Discard, accepted)
		if err != nil || migrated {
			t.Errorf("MigrateFile() on an absent file = %v, %v; want nothing to do", migrated, err)
		}
		if _, err := os.Stat(path); err == nil {
			t.Error("a migration created a configuration file the operator never wrote")
		}
	})
}

// TestMigrateFileKeepsTheOperatorsMode holds the property a second copy of a
// configuration file loses quietly.
//
// **Must fail when** either file this writes is created at the umask's mode
// rather than the original's: the file may hold the shared secret, and a 0644
// backup of a 0600 file publishes it to every account on the host — the exact
// condition ReadFile refuses to start on.
func TestMigrateFileKeepsTheOperatorsMode(t *testing.T) {
	t.Parallel()

	for _, mode := range []os.FileMode{0o600, 0o644} {
		t.Run(fmt.Sprintf("%04o", mode), func(t *testing.T) {
			t.Parallel()

			path := writeConfig(t, "max_sessions = 2\n", mode)
			if _, err := config.MigrateFile(path, io.Discard, accepted); err != nil {
				t.Fatalf("MigrateFile() = _, %v; want a migration", err)
			}
			for _, name := range []string{path, config.BackupPath(path)} {
				info, err := os.Stat(name)
				if err != nil {
					t.Fatalf("inspect %s: %v", name, err)
				}
				if got := info.Mode().Perm(); got != mode {
					t.Errorf("%s is mode %04o after a migration of a %04o file", name, got, mode)
				}
			}
		})
	}
}

// TestMigrateFileRefusesAFileThatWillNotParse holds the line migrate.go draws,
// from the outside: rewriting a file whose grammar is broken means guessing what
// the operator meant with their original already replaced by the guess.
func TestMigrateFileRefusesAFileThatWillNotParse(t *testing.T) {
	t.Parallel()

	const before = "max_sessions 2\n"
	path := writeConfig(t, before, 0o600)

	if _, err := config.MigrateFile(path, io.Discard, accepted); err == nil {
		t.Fatal("MigrateFile() = _, nil; want a refusal of a file the daemon will not read")
	}
	if after, _ := snapshot(t, path); string(after) != before {
		t.Errorf("a file the migration refused was rewritten anyway:\n%s", after)
	}
	assertNothingLeftBehind(t, path)
}

// assertNothingLeftBehind fails unless the operator's configuration and its
// backup are the only files in the directory: no staged migration, and no
// temporary file from the atomic write.
//
// Either one left behind is a second file for the next reader to work out the
// standing of, in the directory where the answer decides what the daemon starts
// on.
func assertNothingLeftBehind(t *testing.T, path string) {
	t.Helper()

	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatalf("read %s: %v", filepath.Dir(path), err)
	}
	for _, entry := range entries {
		switch name := entry.Name(); name {
		case filepath.Base(path), filepath.Base(config.BackupPath(path)):
		default:
			t.Errorf("a migration left %s beside the operator's configuration", name)
		}
	}
}

// TestBothMigrationsAreThisOne is T007 itself, and it is the assertion that
// makes the rest of this file worth having: the command an operator runs and the
// migration an update runs unattended both come through MigrateFile, and neither
// has kept a write of its own.
//
// **Must fail when** either grows a second implementation. That is not
// hypothetical — it is what happened, and the two disagreed about whether the
// result was checked before it replaced anything. A property proven of one
// function is a property of this repository only for as long as both callers are
// still calling it, which is a thing a test can watch and a comment cannot.
func TestBothMigrationsAreThisOne(t *testing.T) {
	t.Parallel()

	for _, path := range []string{"../../cmd/crswd/config_cmd.go", "../updater/config.go"} {
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}

		var calls []string
		ast.Inspect(file, func(n ast.Node) bool {
			sel, ok := n.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			pkg, ok := sel.X.(*ast.Ident)
			if !ok {
				return true
			}
			calls = append(calls, pkg.Name+"."+sel.Sel.Name)
			return true
		})

		if !slices.Contains(calls, "config.MigrateFile") {
			t.Errorf("%s migrates a configuration file without going through config.MigrateFile", path)
		}
		// The marks of a second write path. Both of these were in cmd/crswd until
		// T007, one rename and one CreateTemp away from the copy in this package.
		for _, mark := range []string{"os.Rename", "os.CreateTemp"} {
			if slices.Contains(calls, mark) {
				t.Errorf("%s calls %s, so it has a write of the operator's configuration of its own", path, mark)
			}
		}
	}
}
