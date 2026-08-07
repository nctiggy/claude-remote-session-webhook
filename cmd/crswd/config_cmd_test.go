//go:build quickstart

package main

// `crswd config check`, `crswd config migrate`, and the backup a daemon falls
// back to (T009, FR-008 … FR-010), against the binary `go build` produces.
//
// These belong here rather than in internal/config because the three things
// they assert are all things only a real process has: a subcommand that answers
// and exits instead of binding a port, a write that lands on disk beside the
// file it replaced, and a daemon that starts on the older of two files and says
// so on the stream systemd keeps.
//
// The harness is quickstart_test.go's: same build tag, same package, same
// isolated tmux server, so a daemon started here cannot reach the operator's
// own sessions. See the note at the top of that file for why the isolation is
// asserted rather than assumed.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// runConfig runs `crswd config …` under a deadline.
//
// The deadline is the point. The failure this file exists to catch — a
// subcommand that starts the daemon instead of answering about a file — does
// not return at all, and without a bound it reports as the whole package timing
// out ten minutes later rather than as this test failing with a reason.
func (h *host) runConfig(over map[string]string, args ...string) (string, int) {
	h.t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), waitBudget)
	defer cancel()

	cmd := exec.CommandContext(ctx, h.bin, args...) //nolint:gosec // h.bin is built by this suite into t.TempDir()
	cmd.Env = h.env(over)
	out, err := cmd.CombinedOutput()
	if ctx.Err() != nil {
		h.t.Fatalf("`crswd %s` had not exited after %s, so it is not reporting on a file — it is running as a daemon:\n%s",
			strings.Join(args, " "), waitBudget, out)
	}

	code := 0
	var exit *exec.ExitError
	switch {
	case err == nil:
	case errorsAs(err, &exit):
		code = exit.ExitCode()
	default:
		h.t.Fatalf("run `crswd %s`: %v", strings.Join(args, " "), err)
	}
	return string(out), code
}

// writeFixtureConfig writes a config file at mode 0600 and backdates it, so a
// later assertion that nothing wrote to it can actually fail.
//
// The backdating is not decoration. The kernel stamps mtime from a coarse
// clock, so a file written and rewritten inside one test keeps the same mtime
// to the nanosecond, and an "it was not touched" assertion on a fresh file
// passes whatever happened to it.
func writeFixtureConfig(t *testing.T, path, contents string) {
	t.Helper()

	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatalf("set %s to 0600: %v", path, err)
	}
	backdate(t, path)
}

func backdate(t *testing.T, path string) {
	t.Helper()

	when := time.Date(2020, time.January, 2, 3, 4, 5, 0, time.UTC)
	if err := os.Chtimes(path, when, when); err != nil {
		t.Fatalf("backdate %s: %v", path, err)
	}
}

// fileState is what a file is, for a comparison against what it still is.
func fileState(t *testing.T, path string) ([]byte, time.Time) {
	t.Helper()

	data, err := os.ReadFile(path) //nolint:gosec // G304: the path is this test's own fixture.
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	return data, info.ModTime()
}

func assertUnchanged(t *testing.T, path string, data []byte, mtime time.Time) {
	t.Helper()

	after, afterMtime := fileState(t, path)
	if !bytes.Equal(data, after) {
		t.Errorf("%s was rewritten:\n%s", path, after)
	}
	if !afterMtime.Equal(mtime) {
		t.Errorf("%s was touched (%s → %s)", path, mtime, afterMtime)
	}
}

// FR-009's first half: a way to check a file *without starting*. An operator
// runs this on the host the daemon is already serving on, so a check that
// started one would bind the port the running daemon holds and reconcile its
// sessions onto a second process — and would then never exit.
func TestConfigCheckDoesNotStart(t *testing.T) {
	h := newHost(t)
	addr := freePort(t)
	over := map[string]string{"CRSW_LISTEN": addr}

	path := filepath.Join(h.dir, "check-config")
	writeFixtureConfig(t, path, "# Why this daemon is capped where it is.\n"+
		"version = 1\n"+
		"max_sessions = 5\n"+
		"shared_secret = "+h.secret+"\n")
	before, mtime := fileState(t, path)

	out, code := h.runConfig(over, "config", "check", path)
	if code != 0 {
		t.Fatalf("`config check` on a valid file exited %d:\n%s", code, out)
	}

	// It reports the file and the keys it sets, and never a value — this output
	// is read over a shoulder and pasted into issues, and one of the keys it can
	// name is the shared secret.
	for _, want := range []string{path, "max_sessions", "shared_secret"} {
		if !strings.Contains(out, want) {
			t.Errorf("`config check` did not report %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, h.secret) {
		t.Errorf("`config check` printed the shared secret:\n%s", out)
	}

	// Nothing bound. This is the assertion the test is named for.
	if ln, err := net.Listen("tcp", addr); err != nil {
		t.Errorf("%s is held after a check, so `config check` started a daemon: %v", addr, err)
	} else {
		_ = ln.Close()
	}

	// And nothing written: reading a file is not an occasion to reformat it
	// (FR-008).
	assertUnchanged(t, path, before, mtime)

	t.Run("a defect is reported by line, and never by value", func(t *testing.T) {
		bad := filepath.Join(h.dir, "broken-config")
		writeFixtureConfig(t, bad, "listen = 127.0.0.1:8787\n"+h.secret+"\n")

		out, code := h.runConfig(over, "config", "check", bad)
		if code == 0 {
			t.Fatalf("`config check` accepted a malformed file:\n%s", out)
		}
		if !strings.Contains(out, bad+":2") {
			t.Errorf("`config check` did not name the line it refused:\n%s", out)
		}
		if strings.Contains(out, h.secret) {
			t.Errorf("`config check` quoted the line it refused, and that line was the secret:\n%s", out)
		}
	})

	t.Run("an absent file is what it is at startup: not a failure", func(t *testing.T) {
		out, code := h.runConfig(over, "config", "check", filepath.Join(h.dir, "not-there"))
		if code != 0 {
			t.Errorf("`config check` on an absent file exited %d; a daemon with no file starts (FR-003):\n%s", code, out)
		}
		if !strings.Contains(out, "No configuration file") {
			t.Errorf("`config check` did not say the file is absent:\n%s", out)
		}
	})
}

// FR-009's second half. `config migrate` is the only code in this repository
// that writes a configuration file, and the backup is what makes that
// tolerable: the file it replaced is still there, both for the operator and for
// the fallback in FR-010.
func TestMigrateKeepsBackup(t *testing.T) {
	h := newHost(t)

	// No version line, which is every hand-written file: the migration this
	// schema has is stamping it.
	const contents = `# Why this daemon is bounded the way it is. A migration that dropped this
# comment would have taken away more than it fixed.
max_sessions = 5

start_commands = default=claude --dangerously-skip-permissions
`
	path := filepath.Join(h.dir, "migrate-config")
	writeFixtureConfig(t, path, contents)
	backup := path + ".bak"

	out, code := h.runConfig(nil, "config", "migrate", path)
	if code != 0 {
		t.Fatalf("`config migrate` exited %d:\n%s", code, out)
	}

	// The file it replaced, byte for byte. Anything less is not a way back.
	kept, err := os.ReadFile(backup) //nolint:gosec // G304: the path is this test's own fixture.
	if err != nil {
		t.Fatalf("read the backup `config migrate` promised to keep: %v", err)
	}
	if string(kept) != contents {
		t.Errorf("%s is not the file that was replaced:\n%s", backup, kept)
	}
	if info, err := os.Stat(backup); err != nil {
		t.Fatalf("stat the backup: %v", err)
	} else if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("the backup is mode %04o, want the original's 0600 — a copy of a secret-bearing file must not be more readable than the file", perm)
	}

	migrated, err := os.ReadFile(path) //nolint:gosec // G304: the path is this test's own fixture.
	if err != nil {
		t.Fatalf("read the migrated file: %v", err)
	}
	if !strings.Contains(string(migrated), "version = 1") {
		t.Errorf("the migrated file records no schema version:\n%s", migrated)
	}
	// Everything else survives. The commentary is why this format is not JSON,
	// and start_commands' internal spacing is a command line.
	for _, line := range strings.Split(contents, "\n") {
		if line != "" && !strings.Contains(string(migrated), line) {
			t.Errorf("the migration did not carry this line through:\n%s", line)
		}
	}

	// The migrated file is one the daemon reads back.
	if out, code := h.runConfig(nil, "config", "check", path); code != 0 {
		t.Errorf("`config check` on the migrated file exited %d:\n%s", code, out)
	}

	t.Run("a file already current is not rewritten", func(t *testing.T) {
		backdate(t, path)
		backdate(t, backup)
		fileBefore, fileMtime := fileState(t, path)
		backupBefore, backupMtime := fileState(t, backup)

		out, code := h.runConfig(nil, "config", "migrate", path)
		if code != 0 {
			t.Fatalf("the second `config migrate` exited %d:\n%s", code, out)
		}

		// Neither file: a migration with no change to make that rewrote the file
		// anyway would put a diff nobody asked for in front of its operator, and
		// would overwrite the last known-good copy with a file identical to the
		// live one.
		assertUnchanged(t, path, fileBefore, fileMtime)
		assertUnchanged(t, backup, backupBefore, backupMtime)
	})

	t.Run("the operator's mode survives the rewrite", func(t *testing.T) {
		// A file holding no secret may legitimately be readable by the
		// operator's group — config.ReadFile refuses a mode only on a file that
		// holds one — and a migration is not an occasion to decide otherwise. It
		// is also the only case that can catch a write that left the mode to
		// whatever umask the migration happened to run under.
		open := filepath.Join(h.dir, "open-config")
		writeFixtureConfig(t, open, "max_sessions = 5\n")
		if err := os.Chmod(open, 0o644); err != nil {
			t.Fatalf("set the fixture to 0644: %v", err)
		}

		if out, code := h.runConfig(nil, "config", "migrate", open); code != 0 {
			t.Fatalf("`config migrate` exited %d:\n%s", code, out)
		}
		for _, name := range []string{open, open + ".bak"} {
			info, err := os.Stat(name)
			if err != nil {
				t.Fatalf("stat %s: %v", name, err)
			}
			if perm := info.Mode().Perm(); perm != 0o644 {
				t.Errorf("%s is mode %04o after a migration, want the 0644 the operator set", name, perm)
			}
		}
	})

	t.Run("nothing else was left in the directory", func(t *testing.T) {
		// The temporary file the atomic write goes through is renamed into
		// place, never left beside the configuration where the next reader would
		// have to work out which of two files the daemon reads.
		entries, err := os.ReadDir(h.dir)
		if err != nil {
			t.Fatalf("read %s: %v", h.dir, err)
		}
		for _, entry := range entries {
			if strings.Contains(entry.Name(), ".tmp-") {
				t.Errorf("`config migrate` left %s behind", entry.Name())
			}
		}
	})
}

// FR-010, end to end and on the real binary: the operator's file will not load,
// the last known-good copy is beside it, and the daemon starts on that copy
// rather than refusing.
//
// This is the recovery that needs no shell access — the operator who broke the
// file may be nowhere near a terminal on this host, and the daemon is how they
// reach it. It is also, incidentally, the proof that CRSW_CONFIG_FILE is
// honoured by the shipping binary: the file it reads here is at neither default
// location.
func TestFallsBackToBackupLoudly(t *testing.T) {
	h := newHost(t)

	dir := filepath.Join(h.dir, "fallback")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("make %s: %v", dir, err)
	}
	path := filepath.Join(dir, "config")
	backup := path + ".bak"

	// A file defect, not a value defect: a line that is neither a comment, nor
	// blank, nor a pair.
	writeFixtureConfig(t, path, "# One bad line is all it takes.\nmax_sessions 5\n")

	// The last known-good file caps this daemon at one session, which is a bound
	// nothing else in this run sets — so a daemon that answers with it is a
	// daemon that read this file. The rate is raised so the second create is
	// refused by the cap rather than by the rate limiter.
	writeFixtureConfig(t, backup, "# The file that still works.\nversion = 1\nmax_sessions = 1\ncreate_rate_per_min = 120\n")

	brokenBefore, brokenMtime := fileState(t, path)
	backupBefore, backupMtime := fileState(t, backup)

	d := h.start(map[string]string{"CRSW_CONFIG_FILE": path})

	// Loud, on the stream systemd keeps, naming both files: the daemon is up on
	// the older one, and the newer one is still there with the defect in it.
	trail := d.readTrail()
	for _, want := range []string{path, backup, "NOT in effect"} {
		if !strings.Contains(trail, want) {
			t.Errorf("the daemon did not say it had fallen back to the backup (%q missing):\n%s", want, trail)
		}
	}

	// And it is running on the backup's bounds rather than on the defaults,
	// which is the difference between a fallback and a shrug.
	first := d.createSession("fallback-1")
	if !h.hasSession("crswd-" + first.ID) {
		t.Errorf("the first session is not on the host")
	}
	second := d.call(http.MethodPost, "/sessions", fmt.Sprintf(`{"name":"fallback-2","work_dir":%q}`, h.workDir), "")
	if second.Status != http.StatusTooManyRequests {
		t.Errorf("the second create = %d, want 429 from the backup's cap of one: %s", second.Status, second.Body)
	}
	if !strings.Contains(d.readTrail(), "concurrent-session cap") {
		t.Errorf("the second create was refused by something other than the cap:\n%s", d.readTrail())
	}
	var listed struct {
		Sessions []json.RawMessage `json:"sessions"`
	}
	if err := json.Unmarshal(d.call(http.MethodGet, "/sessions", "", "").Body, &listed); err != nil {
		t.Fatalf("decode the list: %v", err)
	}
	if len(listed.Sessions) != 1 {
		t.Errorf("the daemon holds %d sessions, want the one its backup allows", len(listed.Sessions))
	}

	if err := d.stop(syscall.SIGTERM); err != nil {
		t.Errorf("SIGTERM: %v\n%s", err, d.readTrail())
	}

	// Neither file was touched. The broken one is what the operator will fix,
	// and the daemon reading a file is never an occasion to write one (FR-008).
	assertUnchanged(t, path, brokenBefore, brokenMtime)
	assertUnchanged(t, backup, backupBefore, backupMtime)
}
