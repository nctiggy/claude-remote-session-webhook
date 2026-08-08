package updater

// swap.go is steps 5, 6 and 7 of the order contracts/self-update.md fixes: run
// the verified candidate once, rename it over ~/.local/bin/crswd keeping what it
// replaced, and exit so systemd starts the new one.
//
// # Why the smoke test exists
//
// **A checksum proves the bytes are the published bytes and says nothing about
// whether they run here.** An arm64 build on an amd64 host passes every check in
// verify.go — it is exactly the file the release published, signed by the key
// this binary carries — and then fails to exec. Without step 5 that discovery
// happens *after* the rename, on a host whose service manager is now restarting
// a binary that cannot start. Running it once, first, turns an outage into a
// refusal (FR-028).
//
// So the check is on what the candidate *printed*, never on its exit status
// alone: `true` is a runnable file that exits 0, and a check satisfied by it is
// not a check. The candidate must call itself exactly the release being
// installed — which is also why this step could not exist before US1 gave the
// daemon a --version to answer with.
//
// # Why the previous binary is linked rather than moved
//
// Two renames — the installed binary aside, then the candidate into its place —
// leave an instant where ~/.local/bin/crswd does not exist, and this process is
// about to exit on purpose. A host that lost power in that window would come
// back with a unit whose ExecStart names nothing. A hard link has no such
// window: the running binary gains a second name, and the rename that follows
// replaces one directory entry atomically.
//
// The rename is also why the swap works at all on the file this process is
// executing: a running binary cannot be *written* (ETXTBSY), but the name it was
// started from can be pointed somewhere else.
//
// # Why the exit is a separate call
//
// Step 7 is a method of its own because the route in T019 has to answer the
// browser and write its audit record before the process ends. A Swap that exited
// on the way out would take the response and the record with it — the operator
// would see a dropped connection and the journal would carry no trace of the
// update that succeeded.

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const (
	// installedPath is where install.sh puts the binary, relative to HOME. The
	// shell spelling is `readonly BINARY` and TestInstalledPathIsWhereTheInstallerWrites
	// holds the two together: a drift here installs a release at a path nothing
	// runs, and the symptom is an update that reports success and changes
	// nothing.
	installedPath = ".local/bin/crswd"

	// previousSuffix names what the update replaced. One deep, and the next
	// successful update overwrites it — README documents the rollback as
	// installing this file back over the binary, and a chain of them would grow
	// the disk by a binary per update while making "the one before this" a
	// question rather than a filename.
	previousSuffix = ".previous"

	// versionArg is the whole of the argv this package ever builds. A literal,
	// beside a program path this package composed itself: nothing from a request
	// reaches either.
	versionArg = "--version"

	// versionPrefix is what a stamped `crswd --version` prints before the
	// version, and it is the second spelling of a line cmd/crswd's printVersion
	// owns. They cannot share a constant without this package importing that
	// command, so TestSmokeTestExpectsWhatTheDaemonPrints reads the other one
	// and requires the two to agree. A drift makes every update refuse a release
	// that is perfectly good, and nothing says so until somebody updates.
	versionPrefix = "crswd "

	// smokeTimeout bounds a candidate that starts and then does not finish. It
	// is a backstop under the caller's context rather than a replacement for it:
	// the request that asked for the update is what should decide how long it
	// waits, and this is what stops a binary that ignores --version from holding
	// an update open when nothing else does.
	smokeTimeout = 10 * time.Second

	// smokeWaitDelay bounds the second way a child hangs: one that exits while
	// something it started still holds the output pipe open, which keeps the read
	// blocked long after the process itself is gone.
	smokeWaitDelay = time.Second

	// maxSmokeOutput bounds what is kept of what the candidate said. A version
	// line is under thirty bytes; everything past this is dropped, which is safe
	// because the comparison below is against one exact line — a candidate that
	// printed more than this has already failed it.
	maxSmokeOutput = 4 << 10
)

var (
	// ErrNoInstalledBinary is a daemon with nothing at ~/.local/bin/crswd to
	// replace: no absolute HOME, or a host where this project was put somewhere
	// else. It is a refusal rather than a create, and that is the whole point —
	// writing the release to a path nothing runs would be an update that reports
	// success, exits for a restart, and comes back as the version it started as.
	ErrNoInstalledBinary = errors.New("this daemon has no installed binary to replace")

	// ErrCandidateWillNotRun is step 5 catching what steps 2 and 3 cannot see:
	// a release that is cryptographically perfect and not for this machine.
	ErrCandidateWillNotRun = errors.New("the staged release does not run on this host")

	// ErrCandidateIsAnotherVersion is the same step catching the weaker failure —
	// it ran, and it is not what was asked for. A separate sentinel from the one
	// above because the two say different things to whoever is reading: one is
	// "this build is not for this host", the other "this release is mislabelled".
	ErrCandidateIsAnotherVersion = errors.New("the staged release calls itself another version")
)

// Swapper installs a staged release over the running one. It holds no state
// about an update in progress; one is safe to keep for the life of the daemon.
type Swapper struct {
	// bin is ~/.local/bin/crswd, or "" when this process has no HOME to find one
	// under. Empty means Swap refuses.
	bin string

	// exit is step 7, named as a field for the same reason Stager.verify is: a
	// test has to watch this happen, and a test that really called os.Exit would
	// end the run rather than assert anything.
	//
	// **The shipping build's value is os.Exit** and TestSwapExitsForSystemd pins
	// it — a seam that could be left pointing at a no-op is a daemon that swaps
	// its binary and then keeps running the old one.
	exit func(int)
}

// NewSwapper returns the Swapper the daemon runs.
func NewSwapper(getenv func(string) string) *Swapper {
	return &Swapper{bin: InstalledPath(getenv), exit: os.Exit}
}

// newSwapper is NewSwapper with the path supplied, for tests that must not
// rename anything over the operator's own binary.
func newSwapper(bin string) *Swapper {
	return &Swapper{bin: bin, exit: os.Exit}
}

// InstalledPath is ~/.local/bin/crswd.
//
// It returns "" when the process has no absolute HOME — the same answer, and for
// the same reason, that StagingDir gives: where a binary is renamed over may not
// depend on the directory somebody happened to be in when they ran systemctl.
func InstalledPath(getenv func(string) string) string {
	home := strings.TrimSpace(getenv(envHome))
	if !filepath.IsAbs(home) {
		return ""
	}
	return filepath.Join(home, installedPath)
}

// Bin is the binary this Swapper replaces, or "" if it cannot.
func (s *Swapper) Bin() string { return s.bin }

// Swap is steps 5 and 6: smoke-test the staged candidate, keep the running
// binary at crswd.previous, and rename the candidate into its place.
//
// staged is what Stager.Stage returned — a 0700 file that both checks passed —
// and version is the release it is meant to be. Any failure removes the
// candidate and leaves this daemon running exactly what it was running
// (FR-028); nothing is renamed on the way to refusing.
//
// It returns after the rename. The process is still running the old binary at
// that point, which is what makes ExitForRestart the caller's to time.
func (s *Swapper) Swap(ctx context.Context, staged, version string) (err error) {
	// Every return below removes the candidate, including the ones that are
	// nobody's fault — data-model.md §4 has the refused state going back to
	// absent from every step. It is also the difference between a refusal and
	// the startup sweep: the sweep exists to clean up after the process that
	// *died*, and arriving there on purpose would make it routine.
	defer func() {
		if err == nil {
			return
		}
		if rmErr := os.Remove(staged); rmErr != nil && !errors.Is(rmErr, fs.ErrNotExist) {
			// Joined rather than swallowed: what is left behind is an executable
			// file in the directory this daemon renames its own binary out of.
			err = errors.Join(err, fmt.Errorf("remove the refused candidate %s: %w", staged, rmErr))
		}
	}()

	if s.bin == "" {
		return ErrNoInstalledBinary
	}
	// Asked before anything is executed rather than discovered by the rename. A
	// host where this project was installed somewhere else is one where the
	// whole update is pointless, and saying so is more use than a link(2) error
	// naming a path the operator never chose.
	switch _, statErr := os.Stat(s.bin); {
	case errors.Is(statErr, fs.ErrNotExist):
		return fmt.Errorf("%s: %w", s.bin, ErrNoInstalledBinary)
	case statErr != nil:
		return fmt.Errorf("look for the installed binary %s: %w", s.bin, statErr)
	}

	if err = s.smokeTest(ctx, staged, version); err != nil {
		return err
	}
	if err = s.keepPrevious(); err != nil {
		return err
	}

	// Step 6, and the only irreversible line in this package. Atomic on one
	// filesystem, which is what makes it safe to do to a path a service manager
	// is about to exec: there is no moment at which ~/.local/bin/crswd is half
	// of either binary.
	if err = os.Rename(staged, s.bin); err != nil {
		return fmt.Errorf("install the staged release over %s: %w", s.bin, err)
	}
	return nil
}

// ExitForRestart is step 7: end this process so systemd starts the binary that
// is now at ExecStart.
//
// The daemon does not restart itself — it cannot, having just replaced the file
// it is running — so this depends on `Restart=always` in the unit, which is why
// T008 put it there. Exit 0, because a successful update is not a failure and a
// unit that recorded one would eventually hit its start-limit.
func (s *Swapper) ExitForRestart() { s.exit(0) }

// smokeTest is step 5: run the candidate and require it to say exactly what it
// is.
func (s *Swapper) smokeTest(ctx context.Context, staged, version string) error {
	ctx, cancel := context.WithTimeout(ctx, smokeTimeout)
	defer cancel()

	//nolint:gosec // G204: the program is <staging dir>/crswd.<version>, a path this package composed from a version matched against versionShape, and it is executable at all only because both the checksum and the signature verified. The argv past it is a literal.
	cmd := exec.CommandContext(ctx, staged, versionArg)
	// Given nothing rather than this process's environment. Printing a version
	// needs none of it, and the daemon's own carries CRSW_SHARED_SECRET — a
	// child that has no use for a secret is a child that should not be handed
	// one (docs/security.md §3).
	cmd.Env = []string{}
	cmd.Stdin = nil
	out := &boundedBuffer{}
	cmd.Stdout = out
	// Discarded rather than captured for the same disclosure reason
	// config.loginShellPATH gives: this error is written to a journal that
	// outlives the process, and stderr is where a program puts whatever it likes.
	cmd.Stderr = io.Discard
	cmd.WaitDelay = smokeWaitDelay

	if err := cmd.Run(); err != nil {
		// The wrong-architecture case arrives here as "exec format error", and a
		// candidate that ran and failed arrives as an exit status. Both are the
		// same answer: this is not a binary to install.
		return fmt.Errorf("%w: %w", ErrCandidateWillNotRun, err)
	}

	// Exactly the line, not a prefix and not a substring: `crswd v0.42 (not a
	// release)` contains the right answer and is a build that was never stamped.
	// Trailing whitespace is not what is being pinned, so it is trimmed rather
	// than made part of the contract between two packages.
	want := versionPrefix + version
	if got := strings.TrimSpace(out.String()); got != want {
		return fmt.Errorf("the staged release printed %q rather than %q: %w", got, want, ErrCandidateIsAnotherVersion)
	}
	return nil
}

// keepPrevious gives the running binary a second name, so the update it is about
// to be replaced by has something to roll back to.
func (s *Swapper) keepPrevious() error {
	previous := s.bin + previousSuffix

	// link(2) refuses an existing name, and on every update after the first this
	// one exists. Removed rather than kept: one deep is what README documents.
	if err := os.Remove(previous); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("remove the binary kept by the last update %s: %w", previous, err)
	}
	if err := os.Link(s.bin, previous); err != nil {
		return fmt.Errorf("keep the running binary at %s: %w", previous, err)
	}
	return nil
}

// boundedBuffer keeps at most maxSmokeOutput bytes of what the candidate wrote.
//
// It reports every write as complete even where it kept none of it, which is
// deliberate: a short write is an error to the child, and killing a candidate
// over how much it printed would refuse a release for a reason that has nothing
// to do with whether it runs.
type boundedBuffer struct{ kept bytes.Buffer }

func (b *boundedBuffer) Write(p []byte) (int, error) {
	// Read before p is narrowed. io.Copy treats a short count as
	// io.ErrShortWrite, closes the pipe and leaves the candidate to die of
	// SIGPIPE — which arrives here as "this release does not run on this host",
	// a refusal of a release that ran perfectly well and merely talked a lot.
	written := len(p)
	if room := maxSmokeOutput - b.kept.Len(); room > 0 {
		if len(p) > room {
			p = p[:room]
		}
		b.kept.Write(p)
	}
	return written, nil
}

func (b *boundedBuffer) String() string { return b.kept.String() }
