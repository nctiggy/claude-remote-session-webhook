package config_test

// What `crswd config migrate` rewrites, and — more of these cases than not —
// what it leaves alone (T009).
//
// A migration's failure mode is not that it does too little. It is that it
// hands the operator's file back with the commentary gone, the spacing
// normalised, and a diff nobody asked for in whatever source control they keep
// it under. So the assertions here are mostly about lines that must come back
// byte for byte, and the one that matters most is the case where nothing needed
// doing at all: that one writes nothing.

import (
	"bytes"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/nctiggy/claude-remote-session-webhook/internal/config"
)

// The schema stamp is the migration that exists today, and the only one that
// can: renamedKeys is empty until a rename actually ships. A file with no
// version line is every hand-written file, and stamping it is what gives the
// next rename something to be measured against.
func TestMigrateStampsTheSchemaVersion(t *testing.T) {
	t.Parallel()

	const before = `# crswd configuration. This comment is the reason the format is not JSON,
# so a migration that dropped it would have taken away more than it fixed.
listen = 127.0.0.1:8787

# name=command pairs, with an "=" inside the value and spaces that belong to
# the operator.
start_commands = default=claude --dangerously-skip-permissions
`

	out, changed, err := config.Migrate("fixture", []byte(before), io.Discard)
	if err != nil {
		t.Fatalf("Migrate(): %v", err)
	}
	if !changed {
		t.Fatal("Migrate() reported no change to a file with no version line")
	}

	lines := strings.Split(string(out), "\n")
	stamp := "version = 1"
	if config.SchemaVersion != 1 {
		t.Fatalf("this fixture is written for schema 1 and the daemon is on %d", config.SchemaVersion)
	}

	// Below the header comment and above the first setting: the header is what
	// an operator reads first, and a line in front of it reads as though the
	// daemon had taken the file over.
	at := -1
	for i, line := range lines {
		if line == stamp {
			at = i
		}
	}
	if at < 0 {
		t.Fatalf("Migrate() produced no %q line:\n%s", stamp, out)
	}
	if want := "# so a migration that dropped it would have taken away more than it fixed."; lines[at-1] != want {
		t.Errorf("the version line landed after %q, want it directly after the header comment", lines[at-1])
	}
	if lines[at+1] != "listen = 127.0.0.1:8787" {
		t.Errorf("the version line landed before %q, want it directly before the first setting", lines[at+1])
	}

	// Every original line survives exactly as it was written. The internal
	// spacing of start_commands is a command line, and collapsing it would
	// change what runs.
	for _, line := range strings.Split(before, "\n") {
		if line == "" {
			continue
		}
		if !strings.Contains(string(out), line) {
			t.Errorf("Migrate() did not carry this line through unchanged:\n%s", line)
		}
	}

	// And the result is a file this daemon reads back.
	if _, err := config.ParseFile("fixture", out, io.Discard); err != nil {
		t.Errorf("the migrated file does not parse: %v", err)
	}
}

// The case that writes nothing. A file already on the current schema has no
// change to make, and a migration that rewrote it anyway would put a diff in
// front of its operator and a mtime on a file the daemon promises never to
// touch (FR-008).
func TestMigrateIsANoOpOnACurrentFile(t *testing.T) {
	t.Parallel()

	out, changed, err := config.Migrate("fixture", []byte(workedExample), io.Discard)
	if err != nil {
		t.Fatalf("Migrate(): %v", err)
	}
	if changed {
		t.Errorf("Migrate() reported a change to a file already on schema %d:\n%s", config.SchemaVersion, out)
	}
	if out != nil {
		t.Errorf("Migrate() produced bytes for a file it had no change to make:\n%s", out)
	}
}

// The other half, exercised with a fixture table because renamedKeys is empty
// and stays empty until a rename ships. Written without this, the rewrite would
// first run during the release that renames something, against the one copy of
// the file the operator has.
func TestMigrateRewritesARenamedKey(t *testing.T) {
	t.Parallel()

	renames := map[string]string{"bind_address": "listen"}
	const before = `version = 1
# Why this daemon listens where it does. The comment outlives the spelling.
bind_address =    127.0.0.1:8787
`

	var warn bytes.Buffer
	out, changed, err := config.MigrateWithRenames("fixture", []byte(before), renames, &warn)
	if err != nil {
		t.Fatalf("MigrateWithRenames(): %v", err)
	}
	if !changed {
		t.Fatal("MigrateWithRenames() reported no change to a file using a former spelling")
	}
	if got := string(out); !strings.Contains(got, "listen = 127.0.0.1:8787") {
		t.Errorf("the renamed key was not rewritten:\n%s", got)
	}
	if strings.Contains(string(out), "bind_address") {
		t.Errorf("the former spelling is still in the file:\n%s", out)
	}
	if !strings.Contains(string(out), "# Why this daemon listens where it does.") {
		t.Errorf("the comment above the renamed key was dropped:\n%s", out)
	}

	// The parse that precedes the rewrite still warns, which is what tells the
	// operator what the migration is about to do before it does it.
	if !strings.Contains(warn.String(), "bind_address") {
		t.Errorf("the migration said nothing about the rename:\n%s", warn.String())
	}
}

// A file whose grammar is broken is not migrated. Rewriting it means guessing
// what the operator meant with their original already replaced by the guess,
// and `crswd config check` is what says where the defect is instead.
func TestMigrateRefusesAFileThatWillNotParse(t *testing.T) {
	t.Parallel()

	for name, contents := range map[string]string{
		"a line that is not a pair": "listen 127.0.0.1:8787\n",
		"an unknown key":            "alowed_roots = /tmp\n",
		"a repeated key":            "listen = a\nlisten = b\n",
		"a future schema":           "version = 99\nlisten = a\n",
	} {
		out, changed, err := config.Migrate("fixture", []byte(contents), io.Discard)
		if err == nil {
			t.Errorf("%s: Migrate() rewrote a file the daemon refuses to read", name)
		}
		if changed || out != nil {
			t.Errorf("%s: Migrate() produced bytes for a file it refused", name)
		}
	}
}

// FR-008 from the inside: nothing in internal/config has a write path, so
// producing a migration cannot touch the file it is a migration of. The whole
// division of labour behind `crswd config migrate` rests on this — the write
// lives in cmd/crswd, on purpose, where an operator asked for it by name.
func TestMigrateNeverWrites(t *testing.T) {
	t.Parallel()

	path := writeConfig(t, "listen = 127.0.0.1:8787\n", 0o600)
	if err := os.Chtimes(path, longAgo, longAgo); err != nil {
		t.Fatalf("backdate the fixture: %v", err)
	}
	before, beforeInfo := snapshot(t, path)

	if _, _, err := config.Migrate(path, before, io.Discard); err != nil {
		t.Fatalf("Migrate(): %v", err)
	}

	after, afterInfo := snapshot(t, path)
	if !bytes.Equal(before, after) {
		t.Errorf("producing a migration rewrote the file:\n%s", after)
	}
	if !afterInfo.ModTime().Equal(beforeInfo.ModTime()) {
		t.Errorf("producing a migration touched the file's mtime (%s → %s)", beforeInfo.ModTime(), afterInfo.ModTime())
	}
}
