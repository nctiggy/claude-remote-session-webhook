package updater

// What swap.go has to be true of, checked by swapping real files in a directory
// of this test's own — never the operator's ~/.local/bin.
//
// Every candidate here goes through the real Stager first, because the staged
// path is the whole of the interface between the two files: a test that wrote
// its own 0700 file into a temporary directory would be asserting against a
// candidate no release ever produced.
//
// The candidates are shell scripts rather than compiled binaries. What step 5
// asks is "does this run here, and what does it say it is", and a script answers
// both — while `go build` inside a test would need a toolchain, a network for
// nothing, and seconds per case. The one case a script cannot play is the one
// that matters most, so it is played by bytes instead: a file that cannot be
// executed at all.

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
)

// installerSource is install.sh, read by TestInstalledPathIsWhereTheInstallerWrites.
const installerSource = "../../install.sh"

// runningBytes is the binary this daemon was started from — an older release, so
// that "the previous binary was kept" is a claim about bytes rather than about a
// file existing.
var runningBytes = []byte("#!/bin/sh\necho crswd v0.41\n")

// wrongArchitecture is the case step 5 exists for: a file that passed both
// cryptographic checks and cannot be executed on this machine. An ELF header and
// nothing that follows it is what an amd64 kernel makes of an arm64 release —
// exec format error, which is the same refusal from the same syscall.
var wrongArchitecture = []byte("\x7fELF\x02\x01\x01\x00a release built for another machine\n")

// script is a candidate that runs, and says what it is told to say.
func script(lines string) []byte { return []byte("#!/bin/sh\n" + lines + "\n") }

// host is a machine with this project installed: a binary at the documented
// path, and a Swapper pointed at it whose step 7 is recorded rather than taken.
type host struct {
	bin      string
	previous string
	swapper  *Swapper
	exits    []int
}

func installedHost(t *testing.T) *host {
	t.Helper()

	h := &host{bin: filepath.Join(t.TempDir(), "crswd")}
	h.previous = h.bin + previousSuffix
	writeRunnable(t, h.bin, runningBytes)

	h.swapper = newSwapper(h.bin)
	h.swapper.exit = func(code int) { h.exits = append(h.exits, code) }
	return h
}

// swap runs the whole of swap.go over one candidate.
func (h *host) swap(t *testing.T, staged string) error {
	t.Helper()
	return h.swapper.Swap(context.Background(), staged, testVersion)
}

// requireUntouched is FR-028 as a property of the host: after a refusal this
// daemon is running exactly what it was running, and nothing has been kept as
// though an update had happened.
func (h *host) requireUntouched(t *testing.T) {
	t.Helper()

	requireBytes(t, h.bin, runningBytes, "the installed binary")
	if _, err := os.Stat(h.previous); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("a refused update kept a previous binary at %s: %v", h.previous, err)
	}
	if len(h.exits) != 0 {
		t.Fatalf("a refused update asked the process to exit with %v", h.exits)
	}
}

// stagedCandidate stages a release carrying the given binary, through the real
// Stager and against a key pair generated in process.
func stagedCandidate(t *testing.T, binary []byte) string {
	t.Helper()

	r, signer := staging(t)
	r.asset = tarball(t, map[string][]byte{binaryMember: binary})
	r.resign(signer)

	path, err := r.stage()
	if err != nil {
		t.Fatalf("stage a candidate: %v", err)
	}
	if got := mode(t, path); got != verifiedMode {
		t.Fatalf("a staged candidate is mode %#o, want %#o", got, verifiedMode)
	}
	return path
}

// writeRunnable writes a file something is expected to be able to exec.
func writeRunnable(t *testing.T, path string, body []byte) {
	t.Helper()

	if err := os.WriteFile(path, body, verifiedMode); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func requireBytes(t *testing.T, path string, want []byte, what string) {
	t.Helper()

	got, err := os.ReadFile(path) //nolint:gosec // G304: path is under this test's own t.TempDir().
	if err != nil {
		t.Fatalf("read %s (%s): %v", path, what, err)
	}
	if string(got) != string(want) {
		t.Fatalf("%s holds %q, want %q", what, got, want)
	}
}

func requireGone(t *testing.T, path string) {
	t.Helper()

	if _, err := os.Stat(path); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("%s is still there: %v", path, err)
	}
}

// TestSmokeTestCatchesWrongArchitecture is the reason step 5 exists at all.
//
// **Must fail when** the smoke test is skipped: the candidate here is exactly
// what the release published and is signed by the key this binary carries, so
// every check before step 5 passes it. Without the exec, it becomes the
// installed binary and the next thing systemd does is fail to start it — an
// outage where a refusal was available.
func TestSmokeTestCatchesWrongArchitecture(t *testing.T) {
	t.Parallel()

	h := installedHost(t)
	staged := stagedCandidate(t, wrongArchitecture)

	if err := h.swap(t, staged); !errors.Is(err, ErrCandidateWillNotRun) {
		t.Fatalf("swapping in a release for another machine returned %v, want %v", err, ErrCandidateWillNotRun)
	}
	h.requireUntouched(t)
	// Removed rather than left at 0700 for the startup sweep to find: the sweep
	// cleans up after the process that died, not after the one that refused.
	requireGone(t, staged)
}

// TestSmokeTestRequiresMatchingVersion is the other half of step 5: what the
// candidate *printed*, not that it managed to print anything.
//
// **Must fail when** only the exit status is checked, which the runnable file
// that says nothing at all catches — and when the comparison is loosened to a
// prefix or a substring, which is what the build that never got stamped is for.
// `crswd v0.42 (not a release)` contains the right answer and is precisely the
// binary an operator must not be moved onto.
func TestSmokeTestRequiresMatchingVersion(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		printed []byte
		want    error
	}{
		{
			// The row every table in this package carries: without it, a fixture
			// broken for an unrelated reason makes every refusal pass while
			// proving nothing.
			name:    "the release that was asked for",
			printed: binaryBytes,
		},
		{
			name:    "a release that calls itself the version before it",
			printed: script(`echo crswd v0.41`),
			want:    ErrCandidateIsAnotherVersion,
		},
		{
			name:    "a build that was never stamped",
			printed: script(`echo "crswd dev (not a release)"`),
			want:    ErrCandidateIsAnotherVersion,
		},
		{
			name:    "a build that says the right version and then says more",
			printed: script(fmt.Sprintf(`echo "crswd %s (not a release)"`, testVersion)),
			want:    ErrCandidateIsAnotherVersion,
		},
		{
			name:    "a runnable file that says nothing at all",
			printed: script(`exit 0`),
			want:    ErrCandidateIsAnotherVersion,
		},
		{
			name:    "a release that says the right version and fails anyway",
			printed: script(fmt.Sprintf("echo crswd %s\nexit 3", testVersion)),
			want:    ErrCandidateWillNotRun,
		},
		{
			// Bounded, and the bound is not a refusal of its own: what makes this
			// one wrong is the line, which is still wrong after everything past
			// maxSmokeOutput is dropped.
			name:    "a release that will not stop talking",
			printed: script(fmt.Sprintf("i=0\nwhile [ $i -lt 2000 ]; do echo crswd %s; i=$((i+1)); done", testVersion)),
			want:    ErrCandidateIsAnotherVersion,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			h := installedHost(t)
			staged := stagedCandidate(t, c.printed)

			err := h.swap(t, staged)
			if c.want == nil {
				if err != nil {
					t.Fatalf("swapping in the published release: %v", err)
				}
				requireBytes(t, h.bin, c.printed, "the installed binary")
				return
			}
			if !errors.Is(err, c.want) {
				t.Fatalf("swapping in %s returned %v, want %v", c.name, err, c.want)
			}
			h.requireUntouched(t)
			requireGone(t, staged)
		})
	}
}

// TestPreviousBinaryKept is what makes the documented rollback possible.
//
// **Must fail when** the binary being replaced is renamed away and forgotten, or
// overwritten in place — either way `crswd.previous` is missing or is the new
// release, and the one-liner in the README restores nothing.
func TestPreviousBinaryKept(t *testing.T) {
	t.Parallel()

	t.Run("the first update this host has ever done", func(t *testing.T) {
		t.Parallel()

		h := installedHost(t)
		staged := stagedCandidate(t, binaryBytes)

		if err := h.swap(t, staged); err != nil {
			t.Fatalf("swap in the published release: %v", err)
		}

		requireBytes(t, h.bin, binaryBytes, "the installed binary")
		requireBytes(t, h.previous, runningBytes, "the binary that was running")
		requireGone(t, staged)

		// The rename carries the staged mode across, and an installed binary
		// that is not executable is a host that cannot start.
		if got := mode(t, h.bin); got&0o100 == 0 {
			t.Fatalf("the installed binary is mode %#o", got)
		}
		// Step 7 is the caller's to time: the route has a response to write and
		// an audit record to emit before this process may end.
		if len(h.exits) != 0 {
			t.Fatalf("Swap exited the process on its own, with %v", h.exits)
		}
	})

	t.Run("the update after that one", func(t *testing.T) {
		t.Parallel()

		h := installedHost(t)
		// What the update before this one kept. link(2) refuses a name that
		// exists, so a swap that does not clear this first fails outright — and
		// one that keeps a chain instead makes "the one before this" a question
		// rather than a filename.
		writeRunnable(t, h.previous, []byte("#!/bin/sh\necho crswd v0.40\n"))

		staged := stagedCandidate(t, binaryBytes)
		if err := h.swap(t, staged); err != nil {
			t.Fatalf("swap in the published release: %v", err)
		}

		requireBytes(t, h.bin, binaryBytes, "the installed binary")
		requireBytes(t, h.previous, runningBytes, "the binary that was running")
	})

	t.Run("the previous binary is not the running one's other name", func(t *testing.T) {
		t.Parallel()

		// The link is made before the rename, so for an instant the two names
		// are one file. If the rename wrote *through* the link rather than
		// replacing the directory entry, both would end up as the new release
		// and there would be nothing to roll back to — the failure this asserts
		// against is invisible to a test that only reads crswd.previous first.
		h := installedHost(t)
		staged := stagedCandidate(t, binaryBytes)

		if err := h.swap(t, staged); err != nil {
			t.Fatalf("swap in the published release: %v", err)
		}
		requireBytes(t, h.previous, runningBytes, "the binary that was running")
		requireBytes(t, h.bin, binaryBytes, "the installed binary")
	})
}

// TestRefusedSwapLeavesTheRunningBinary is contracts/self-update.md's "nothing
// renamed, version unchanged", which the tasks before this one could only
// assert the first half of — until now there was nothing renameable.
//
// **Must fail when** a refusal happens after something has already been moved.
func TestRefusedSwapLeavesTheRunningBinary(t *testing.T) {
	t.Parallel()

	t.Run("a candidate that will not run", func(t *testing.T) {
		t.Parallel()

		h := installedHost(t)
		if err := h.swap(t, stagedCandidate(t, wrongArchitecture)); err == nil {
			t.Fatal("a release for another machine was installed")
		}
		h.requireUntouched(t)
	})

	t.Run("a binary somewhere this daemon cannot write", func(t *testing.T) {
		t.Parallel()

		// The case that used to be "a daemon with no HOME". That premise went
		// away when InstalledPath started asking the process what it is running
		// instead of composing a path from the environment — a process always
		// knows its own executable, so there is no longer a daemon that cannot
		// name one.
		//
		// The concern underneath it survives and is better served here. A
		// container's binary comes from its image and must not be replaced, and
		// an image layer is read-only: the refusal an operator actually gets is
		// the filesystem's, and it is worth proving this reaches them as a
		// refusal that leaves the running binary alone rather than as a panic or
		// a half-finished rename.
		dir := t.TempDir()
		bin := filepath.Join(dir, "crswd")
		if err := os.WriteFile(bin, binaryBytes, 0o700); err != nil { //nolint:gosec // G306: an executable under t.TempDir() has to be executable; that is what is being replaced.
			t.Fatalf("write %s: %v", bin, err)
		}
		if err := os.Chmod(dir, 0o500); err != nil { //nolint:gosec // G302: read-only-and-traversable is the condition under test.
			t.Fatalf("make %s read-only: %v", dir, err)
		}
		t.Cleanup(func() { _ = os.Chmod(dir, 0o700) }) //nolint:errcheck,gosec // best-effort restore so t.TempDir can clean up; a failure here fails nothing under test.

		before, err := os.ReadFile(bin) //nolint:gosec // G304: bin is filepath.Join of t.TempDir() and a literal.
		if err != nil {
			t.Fatalf("read %s: %v", bin, err)
		}

		staged := stagedCandidate(t, binaryBytes)
		if err := newSwapper(bin).Swap(context.Background(), staged, testVersion); err == nil {
			t.Fatal("a binary in a directory this daemon cannot write was replaced anyway")
		}

		after, err := os.ReadFile(bin) //nolint:gosec // G304: same path, same reason.
		if err != nil {
			t.Fatalf("read %s after the refusal: %v", bin, err)
		}
		if !bytes.Equal(before, after) {
			t.Error("the refusal changed the running binary; a refused update must leave it exactly as it was")
		}
		requireGone(t, staged)
	})

	t.Run("a host where this project was installed somewhere else", func(t *testing.T) {
		t.Parallel()

		// Nothing at ~/.local/bin/crswd. Refused rather than created: writing
		// the release to a path nothing runs is an update that reports success,
		// exits for a restart, and comes back as the version it started as.
		absent := filepath.Join(t.TempDir(), "crswd")
		staged := stagedCandidate(t, binaryBytes)

		if err := newSwapper(absent).Swap(context.Background(), staged, testVersion); !errors.Is(err, ErrNoInstalledBinary) {
			t.Fatalf("swapping onto a host carrying no binary returned %v, want %v", err, ErrNoInstalledBinary)
		}
		requireGone(t, absent)
		requireGone(t, staged)
	})

	t.Run("a candidate that never finishes", func(t *testing.T) {
		t.Parallel()

		// The smoke test runs a program this daemon did not write, from a route
		// somebody is waiting on. A candidate that ignores --version and sits
		// there must cost the update rather than the daemon, so the caller's
		// context bounds it — smokeTimeout is only the backstop for a caller
		// that brought none.
		h := installedHost(t)
		staged := stagedCandidate(t, script("sleep 60"))

		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer cancel()

		if err := h.swapper.Swap(ctx, staged, testVersion); !errors.Is(err, ErrCandidateWillNotRun) {
			t.Fatalf("swapping in a candidate that never finishes returned %v, want %v", err, ErrCandidateWillNotRun)
		}
		h.requireUntouched(t)
		requireGone(t, staged)
	})
}

// TestSwapExitsForSystemd is step 7, and the fact that the shipping build really
// takes it.
//
// **Must fail when** the seam is left pointing at something that returns — a
// daemon that swapped its binary and then carried on running the old one is one
// whose update silently did not happen until the next restart, whenever that is.
func TestSwapExitsForSystemd(t *testing.T) {
	t.Parallel()

	t.Run("the daemon exits 0 for the restart", func(t *testing.T) {
		t.Parallel()

		h := installedHost(t)
		h.swapper.ExitForRestart()

		// 0, not a failure: a successful update that recorded one would walk the
		// unit towards its start limit.
		if !reflect.DeepEqual(h.exits, []int{0}) {
			t.Fatalf("the restart asked for %v, want [0]", h.exits)
		}
	})

	t.Run("the shipping build exits for real", func(t *testing.T) {
		t.Parallel()

		for _, s := range []*Swapper{NewSwapper(func(string) string { return "/home/somebody" }), newSwapper("/home/somebody/.local/bin/crswd")} {
			if s.exit == nil {
				t.Fatal("a Swapper was built with no way to end the process")
			}
			if reflect.ValueOf(s.exit).Pointer() != reflect.ValueOf(os.Exit).Pointer() {
				t.Fatal("a Swapper was built exiting through something other than os.Exit")
			}
		}
	})
}

// TestInstalledPathIsWhatIsRunning holds the contract that replaced a composed
// path: this daemon updates the binary it is executing, not the one an installer
// would have written.
//
// It used to assert ~/.local/bin/crswd built from HOME, which is where install.sh
// puts a binary and therefore looks correct. It is correct only for hosts that
// installed the way the installer installs. The host this was written on runs
// /home/nctiggy/bin/crswd, placed by hand before there was an installer — and
// against it the old behaviour verified a release, renamed it over a file nothing
// executes, and exited for systemd to restart the *old* binary. The update
// reported success and changed nothing.
//
// **Must fail when** InstalledPath is composed from an environment variable
// again. The test proves it by handing over a getenv that fails the test if it is
// consulted at all: where this daemon renames may not be a guess about how it was
// installed.
func TestInstalledPathIsWhatIsRunning(t *testing.T) {
	t.Parallel()

	self, err := os.Executable()
	if err != nil {
		t.Skipf("this platform cannot name its own executable: %v", err)
	}
	want, err := filepath.EvalSymlinks(self)
	if err != nil {
		t.Fatalf("resolve %s: %v", self, err)
	}

	got := InstalledPath(func(name string) string {
		t.Fatalf("the installed binary was resolved from the environment (%s) rather than from the running process", name)
		return ""
	})
	if got != want {
		t.Fatalf("installed path %q, want the running binary %q", got, want)
	}
	if !filepath.IsAbs(got) {
		t.Fatalf("installed path %q is not absolute; which file is renamed over may not depend on a working directory", got)
	}
}

// TestInstalledPathIsWhereTheInstallerWrites holds this package and install.sh
// to one path.
//
// **Must fail when** either moves. They cannot share a constant — one is Go and
// one is shell — and a drift has no symptom until somebody updates, at which
// point the release is installed where nothing runs and the daemon comes back as
// the version it already was.
func TestInstalledPathIsWhereTheInstallerWrites(t *testing.T) {
	t.Parallel()

	installer, err := os.ReadFile(installerSource)
	if err != nil {
		t.Fatalf("read %s: %v", installerSource, err)
	}
	declared := regexp.MustCompile(`(?m)^readonly BINARY="([^"]+)"`).FindStringSubmatch(string(installer))
	if declared == nil {
		t.Fatalf("%s declares no BINARY, so this test can no longer see where the installer writes", installerSource)
	}
	if declared[1] != installedPath {
		t.Fatalf("%s installs to %q and this package replaces %q", installerSource, declared[1], installedPath)
	}
}

// TestSmokeTestExpectsWhatTheDaemonPrints holds the smoke test to the line
// cmd/crswd actually prints.
//
// **Must fail when** either spelling of that line moves. This package cannot
// import the command it execs, so the two are separate constants, and a drift
// makes the daemon refuse a release that is perfectly good — with no symptom
// until an operator tries to update and cannot.
func TestSmokeTestExpectsWhatTheDaemonPrints(t *testing.T) {
	t.Parallel()

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, mainSource, nil, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("parse %s: %v", mainSource, err)
	}

	// The AST rather than a grep, for the reason TestStagingSweptAtStartup
	// gives: that file explains itself at length, and prose about the version it
	// prints satisfies any string search for it.
	want := versionPrefix + testVersion + "\n"
	found := false
	ast.Inspect(funcNamed(t, file, "printVersion").Body, func(n ast.Node) bool {
		lit, ok := n.(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			return true
		}
		format, err := strconv.Unquote(lit.Value)
		if err != nil {
			return true
		}
		if fmt.Sprintf(format, testVersion) == want {
			found = true
		}
		return true
	})
	if !found {
		t.Fatalf("%s prints nothing that reads %q for a stamped build, so every update would refuse", mainSource, want)
	}
}

// TestSmokeTestRunsTheCandidateWithNothingFromThisProcess is docs/security.md §3
// at the one place this package starts a program.
//
// **Must fail when** the daemon's environment is inherited: it carries
// CRSW_SHARED_SECRET, and a child that has no use for a secret must not be
// handed one.
func TestSmokeTestRunsTheCandidateWithNothingFromThisProcess(t *testing.T) {
	// The one test in this file that is not parallel, and it cannot be: it puts
	// a secret in this process's own environment to have something to leak, and
	// testing refuses t.Setenv beside t.Parallel for exactly that reason.
	const secret = "CRSW_SHARED_SECRET" //nolint:gosec // G101: an env var name, not a credential — the same nolint config.EnvSharedSecret carries.
	t.Setenv(secret, strings.Repeat("k", 32))

	h := installedHost(t)
	// The candidate says whether it was handed the secret, never what it was
	// handed: a test that proved a leak by printing the value would put one in
	// its own failure output. It is asked about that variable rather than shown
	// its whole environment because a shell manufactures PWD for itself even
	// when it is started with none — `env` is not a probe for what was
	// inherited, and this is.
	staged := stagedCandidate(t, script(fmt.Sprintf(`echo crswd %s; printf '%%s' "${%s:+ and the secret}"`, testVersion, secret)))

	if err := h.swap(t, staged); err != nil {
		t.Fatalf("swap in a candidate reporting what it was handed: %v", err)
	}
	// A pass means the child printed the version line and nothing after it: the
	// variable was unset, so the exact-line comparison saw only the version.
}
