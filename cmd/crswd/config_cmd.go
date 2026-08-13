package main

// `crswd config check` and `crswd config migrate` (FR-009) — the two things an
// operator does to a configuration file that are not starting a daemon.
//
// Neither of them starts anything, and that is the property both are about: a
// daemon is already serving on this host, and a subcommand that bound its port
// or rewrote the file under it would be the accident it was written to prevent.
// Nothing on a start writes a configuration file (FR-008); a migration is the
// operator asking for one by name, and it keeps the file it replaced.
//
// The rewrite itself is config.MigrateFile's, shared with the migration an
// update runs unattended (internal/updater/config.go). What is this command's
// own is below: which file, what it reports, and how much of the result it can
// honestly check from a shell that is not the daemon's.

import (
	"fmt"
	"io"
	"os"

	"github.com/nctiggy/claude-remote-session-webhook/internal/config"
)

const configUsage = `usage: crswd                          start the daemon
       crswd config check [path]      report on a configuration file without starting
       crswd config migrate [path]    rewrite one into the current schema, keeping a backup
       crswd keygen                   print a new ed25519 release key pair

With no path, check and migrate read the file the daemon itself would: ` +
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
// The rewrite is config.MigrateFile's, which stages the result beside the
// operator's file and replaces nothing until it has been read back and
// accepted; the backup it keeps is the file LoadFrom falls back to (FR-010),
// which is why a migration is worth doing before an edit rather than after — it
// is what puts a known-good copy on disk.
//
// What is this command's own is the decision to run it at all, and the report:
// an operator typed this, so they are told which of the three endings happened
// and where their previous file is.
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

	migrated, err := config.MigrateFile(path, out, migrationReadsBack(path))
	switch {
	case err != nil && migrated:
		// The migration landed and something after it did not. Every other
		// ending leaves the operator's file exactly as it was, and telling them
		// that here would send them to look at the wrong file.
		return fmt.Errorf("%s was migrated to schema %d, and then: %w", path, config.SchemaVersion, err)
	case err != nil:
		return fmt.Errorf("%s is unchanged: %w", path, err)
	case !migrated:
		say(out, "config file %s is already schema %d. Nothing was written.\n", path, config.SchemaVersion)
		return nil
	}

	say(out, "config file %s migrated to schema %d.\nThe file it replaced is at %s, which is what the daemon falls back to if this one stops loading.\n",
		path, config.SchemaVersion, config.BackupPath(path))
	return nil
}

// migrationReadsBack is this command's last look at what it staged: the bytes
// that landed on disk go back through the parser, and a migration that does not
// come back is discarded with the operator's file untouched.
//
// The parser and not the loader, for the reason configCheck states above. The
// values are checked against the environment the *daemon* runs in, and an
// operator running this from their own shell has a different one — a migration
// refused because their terminal carries no CRSW_SHARED_SECRET would be refusing
// a file the daemon starts on perfectly well, and teaching them not to migrate.
// The update path asks the loader as well, and may: it runs inside that
// environment, unattended, moments before the restart that would find out
// (internal/updater/config.go).
func migrationReadsBack(path string) func([]byte) error {
	return func(landed []byte) error {
		if _, err := config.ParseFile(path, landed, io.Discard); err != nil {
			return fmt.Errorf("the migration of %s did not read back, so it was discarded: %w", path, err)
		}
		return nil
	}
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
