package main

// `crswd config check` and `crswd config migrate` (FR-009) — the two things an
// operator does to a configuration file that are not starting a daemon.
//
// This file holds the only code in the repository that writes a configuration
// file (FR-008). internal/config parses one, and produces the bytes a migration
// should have, and opens nothing for writing at all; the write is here, it
// happens only when the operator asks for it by name, and it keeps the file it
// replaced. That division is what makes "the daemon never rewrites your file" a
// claim a reader can check rather than one they have to trust.

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/nctiggy/claude-remote-session-webhook/internal/config"
)

const configUsage = `usage: crswd                          start the daemon
       crswd config check [path]      report on a configuration file without starting
       crswd config migrate [path]    rewrite one into the current schema, keeping a backup

With no path, both subcommands read the file the daemon itself would: ` +
	"$CRSW_CONFIG_FILE, else $XDG_CONFIG_HOME/crswd/config, else ~/.config/crswd/config.\n"

// runConfigCommand answers `crswd config …` and returns the process's exit code.
//
// An argument this program does not understand is refused rather than ignored.
// Ignored, it would fall through to starting the daemon — a mistyped `crswd
// cofnig check` on a live host would bind a second listener and reconcile the
// first daemon's sessions onto itself. This is the one program in the
// repository where an unrecognised instruction must not read as "carry on".
func runConfigCommand(out, errOut io.Writer, args []string) int {
	if args[0] != "config" {
		say(errOut, "crswd: unknown argument %q\n%s", args[0], configUsage)
		return 2
	}
	if len(args) < 2 {
		say(errOut, "crswd: config needs a subcommand\n%s", configUsage)
		return 2
	}

	var run func(io.Writer, string) error
	switch args[1] {
	case "check":
		run = configCheck
	case "migrate":
		run = configMigrate
	default:
		say(errOut, "crswd: unknown config subcommand %q\n%s", args[1], configUsage)
		return 2
	}

	path, err := configPath(args[2:])
	if err == nil {
		err = run(out, path)
	}
	if err != nil {
		say(errOut, "crswd: %v\n", err)
		return 1
	}
	return 0
}

// configPath is which file the subcommand is about: the one named on the
// command line, else the one the daemon itself would read.
//
// The default is resolved by the daemon's own DefaultPath rather than by a copy
// of the rule, because an operator checking one file while the daemon reads
// another is precisely the confusion this milestone exists to end.
func configPath(rest []string) (string, error) {
	switch len(rest) {
	case 0:
		path := config.DefaultPath(os.Getenv)
		if path == "" {
			return "", fmt.Errorf("there is no default configuration file on this host: neither CRSW_CONFIG_FILE, XDG_CONFIG_HOME nor HOME names one, so name the file on the command line")
		}
		return path, nil
	case 1:
		return rest[0], nil
	default:
		return "", fmt.Errorf("one file at a time; %d paths were given", len(rest))
	}
}

// configCheck parses the file and reports, without starting anything (FR-009).
//
// What it checks is the *file*: its grammar, the keys it sets, the schema it
// declares, and the mode it sits on disk in — every refusal internal/config
// makes about a file, made by exactly the code that makes them at startup.
//
// What it does not check is the *values*. Those are validated against the
// environment the daemon runs in, and an operator running this from their own
// shell has a different one; a check that failed because their terminal carries
// no CRSW_SHARED_SECRET would report a problem that does not exist, and teach
// them to stop running it. The last line says so rather than leaving it implied.
func configCheck(out io.Writer, path string) error {
	f, err := config.ReadFile(path, out)
	if err != nil {
		return err
	}
	if f == nil {
		// Not a failure, because it is not one at startup either: a daemon with
		// no configuration file runs on its environment and its built-in
		// defaults, which is what every deployment before this milestone was
		// (FR-003). Reporting it as an error would say the daemon will not
		// start, which is false.
		say(out, "No configuration file at %s.\nThe daemon starts from its environment and its built-in defaults.\n", path)
		return nil
	}

	say(out, "config file %s parses, and sets:\n", path)
	set := 0
	for _, name := range config.Vars() {
		if _, ok := f.Lookup(name); ok {
			// The key, never the value. This output is read over a shoulder and
			// pasted into issues, and one of the keys it can name is the shared
			// secret (docs/security.md §3).
			say(out, "  %s\n", config.KeyForVar(name))
			set++
		}
	}
	if set == 0 {
		sayln(out, "  nothing — every setting comes from the environment or a default")
	}
	sayln(out, "The values themselves are checked when the daemon starts, against the environment it starts in.")
	return nil
}

// configMigrate rewrites the file into the current schema, keeping the one it
// replaced at config.bak (FR-009).
//
// The backup is written first and the file second, so the ending where one of
// the two writes fails is the ending where the operator still has both copies
// of something. It is also the file LoadFrom falls back to (FR-010), which is
// why a migration is worth doing before an edit rather than after: it is what
// puts a known-good copy on disk.
func configMigrate(out io.Writer, path string) error {
	// The daemon's own answer to "is this file acceptable" first, so a migration
	// refuses exactly what a start refuses — including the mode, since migrating
	// a group-readable file holding a secret would otherwise produce a second
	// copy of it beside the first.
	f, err := config.ReadFile(path, out)
	if err != nil {
		return err
	}
	if f == nil {
		return fmt.Errorf("there is no configuration file at %s to migrate", path)
	}

	data, mode, err := readFileAndMode(path)
	if err != nil {
		return err
	}
	next, changed, err := config.Migrate(path, data, out)
	if err != nil {
		return err
	}
	if !changed {
		say(out, "config file %s is already schema %d. Nothing was written.\n", path, config.SchemaVersion)
		return nil
	}

	backup := config.BackupPath(path)
	if err := writeConfigFile(backup, data, mode); err != nil {
		return fmt.Errorf("%s is unchanged, because its backup could not be written: %w", path, err)
	}
	if err := writeConfigFile(path, next, mode); err != nil {
		return fmt.Errorf("%s is unchanged, and a copy of it is at %s: %w", path, backup, err)
	}

	say(out, "config file %s migrated to schema %d.\nThe file it replaced is at %s, which is what the daemon falls back to if this one stops loading.\n",
		path, config.SchemaVersion, backup)
	return nil
}

// readFileAndMode reads the bytes to migrate and the mode to write them back
// under. The mode is the operator's: a file they deliberately left readable by
// their own group does not silently become 0600, and one holding a secret has
// already been refused by the check above if it was not.
func readFileAndMode(path string) ([]byte, os.FileMode, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, 0, fmt.Errorf("inspect %s: %w", path, err)
	}
	data, err := os.ReadFile(path) //nolint:gosec // G304: the path is the operator's own config file, named by the operator on their own command line.
	if err != nil {
		return nil, 0, fmt.Errorf("read %s: %w", path, err)
	}
	return data, info.Mode().Perm(), nil
}

// writeConfigFile replaces path with data, atomically, at mode.
//
// Written to a temporary file in the same directory and renamed over the
// target, because the alternative — truncating the operator's file and writing
// into it — has a window in which a full disk, a signal or a power cut leaves
// them holding half a configuration. A rename inside one directory is atomic,
// so at every instant the path holds either the whole old file or the whole new
// one, which is the property that makes running this on a live host reasonable.
//
// The mode is set explicitly rather than left to the umask: this file may hold
// the shared secret, and a umask of 022 would publish it to every account on
// the host — the exact condition config.ReadFile refuses to start on.
func writeConfigFile(path string, data []byte, mode os.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("create a temporary file beside %s: %w", path, err)
	}
	name := tmp.Name()

	err = writeAndSync(tmp, data, mode)
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err == nil {
		err = os.Rename(name, path)
	}
	if err != nil {
		// The half-written temporary file is removed rather than left beside the
		// configuration, where the next reader would have to work out which of
		// two files the daemon reads.
		_ = os.Remove(name) //nolint:errcheck // the write already failed; a failed cleanup of its leftovers changes neither the error returned nor what the operator must do.
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

func writeAndSync(f *os.File, data []byte, mode os.FileMode) error {
	if err := f.Chmod(mode); err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		return err
	}
	// Synced before the rename, so the ending where the host loses power between
	// the two is a directory entry pointing at bytes that are actually there.
	return f.Sync()
}

// say and sayln write to a report stream and drop the write error, in one place
// rather than at each of the dozen call sites below.
//
// The drop is deliberate and it is not a swallowed error in the sense AGENTS.md
// bans. Every caller here is reporting to stdout or stderr, and there is no
// answer to a failed write to the stream you would report the failure on. The
// repo's errcheck runs with check-blank, so `_, _ =` at each site would be
// flagged too — centralising it means a reviewer reads the argument once and can
// count the exceptions, instead of skimming twelve identical //nolint comments.
func say(w io.Writer, format string, args ...any) {
	_, _ = fmt.Fprintf(w, format, args...) //nolint:errcheck // see the comment above.
}

func sayln(w io.Writer, line string) {
	_, _ = fmt.Fprintln(w, line) //nolint:errcheck // see the comment above.
}
