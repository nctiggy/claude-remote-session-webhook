//go:build quickstart

package main

// `crswd --version`, against real builds:
//
//	go test -tags quickstart ./cmd/crswd
//
// Behind the acceptance tag because the thing under test is the *link*, and no
// unit test can reach it. `internal/buildinfo.Version` is a variable with a
// default, and the release workflow replaces that default with `-ldflags -X`
// naming the symbol as a string. Nothing checks that string: a typo in the
// package path, or in the variable's name, is not an error at build time and
// not an error at run time either — the linker simply sets nothing, the default
// survives, and the binary reports "dev" while claiming to be a release build.
// It fails silently precisely because the default is itself a valid string.
//
// So the assertion has to be made from outside: build with the flag the
// workflow will use, run the artifact, and read what it says about itself. Both
// halves are here rather than only the stamped one, because "prints something
// plausible" is what the broken case does too.
//
// Neither test starts a daemon. Both run with the shared secret unset — an
// environment the daemon refuses to start in (story 1's startup failures) — so
// an exit 0 with nothing on either stream but the version line is the proof
// that --version answered *instead of* the daemon rather than beside it.

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// theStampedSymbol is the ldflags path the release workflow will use, spelled
// once. A test that hard-coded a different one from the workflow would pass
// while the release it is supposed to guard shipped unstamped.
const theStampedSymbol = "github.com/nctiggy/claude-remote-session-webhook/internal/buildinfo.Version"

// noDaemonPossible is an environment the daemon will not start in. See the file
// comment: it turns "exit 0" into evidence.
func noDaemonPossible() map[string]string {
	return map[string]string{"CRSW_SHARED_SECRET": unset}
}

// TestUnreleasedBuildSaysSo is the default half of T002. A build nobody stamped
// must say it is not a release in words, not print a bare "dev" that an
// operator has to know how to read.
func TestUnreleasedBuildSaysSo(t *testing.T) {
	h := newHost(t)

	out, code := h.runBinary(h.bin, noDaemonPossible(), "--version")

	if code != 0 {
		t.Fatalf("--version exited %d, want 0:\n%s", code, out)
	}
	if want := "crswd dev (not a release)\n"; out != want {
		t.Errorf("--version printed %q, want %q.\nThe whole output is asserted, so anything else here is a diagnostic that reached a stream --version owns, or a daemon that started anyway", out, want)
	}
}

// TestStampedVersionIsReported is the half that can fail silently. It builds
// with the release workflow's own ldflags and asserts the artifact reports what
// they set.
//
// **This is the test that catches a wrong symbol path.** When the path is
// wrong, the linker sets nothing, the default survives, and the artifact prints
// the line the test above asserts — a green run of that one and a release that
// calls itself "dev" forever. Read a failure here as "the ldflags string is
// wrong" before reading it as anything else.
func TestStampedVersionIsReported(t *testing.T) {
	h := newHost(t)
	bin := h.buildStamped("v0.42")

	out, code := h.runBinary(bin, noDaemonPossible(), "--version")

	if code != 0 {
		t.Fatalf("--version exited %d, want 0:\n%s", code, out)
	}
	if want := "crswd v0.42\n"; out != want {
		t.Errorf("a build stamped v0.42 printed %q, want %q.\nIf that is the unreleased line, the link set nothing: check -X %s names the package path and the variable exactly",
			out, want, theStampedSymbol)
	}
}

// buildStamped builds the artifact the way the release workflow will, and
// returns its path.
func (h *host) buildStamped(version string) string {
	h.t.Helper()

	bin := filepath.Join(h.dir, "crswd-"+strings.ReplaceAll(version, ".", "-"))
	cmd := exec.Command("go", "build",
		"-ldflags", "-X "+theStampedSymbol+"="+version,
		"-o", bin, "./cmd/crswd")
	cmd.Dir = "../.."
	if out, err := cmd.CombinedOutput(); err != nil {
		h.t.Fatalf("go build -ldflags -X %s=%s ./cmd/crswd: %v\n%s", theStampedSymbol, version, err, out)
	}
	return bin
}
