package release_test

// What a release is, checked against the workflow that produces one.
//
// Nothing here can run the release — it publishes to GitHub — so these tests do
// the two things that can be done from a working tree: they read
// .github/workflows/release.yml for the decisions that are silent when they go
// wrong, and they replay its build so the artifact's properties are measured
// rather than asserted about.
//
// Three of the four failures this guards are silent on the builder and loud on
// somebody else's machine, which is the whole subject of this milestone:
//
//   - cgo left on          — links libc, runs here, fails where there is no libc
//   - a mistyped -X path   — the linker sets nothing and the release says "dev"
//   - a shallow checkout   — `git rev-list --count HEAD` counts 1, forever
//
// The fourth, a tag-only trigger, is silent everywhere: releases simply stop
// happening and self-update finds nothing.

import (
	"debug/elf"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

const (
	// repoRoot is where `go build ./cmd/crswd` means something.
	repoRoot = "../.."

	workflowPath = repoRoot + "/.github/workflows/release.yml"

	// versionTestPath holds theStampedSymbol, the ldflags path T002 proved
	// reaches the variable. It is read as text because it is an unexported
	// constant in cmd/crswd's own test binary, which no other package can
	// import — and copying it here would be the drift this checks for.
	versionTestPath = repoRoot + "/cmd/crswd/version_test.go"
)

// published is the platform list the release names, and the one the assertions
// below are written against. The workflow's own list is read from the file and
// checked against this rather than trusted, so dropping an architecture is a
// failure here instead of a 404 for whoever runs on it.
var published = []string{"amd64", "arm64"}

func readWorkflow(t *testing.T) string {
	t.Helper()

	raw, err := os.ReadFile(workflowPath)
	if err != nil {
		t.Fatalf("read %s: %v", workflowPath, err)
	}
	return string(raw)
}

// find returns the first submatch of pattern in text, or fails naming what the
// workflow was expected to say. Every caller is reading one decision out of the
// YAML; a pattern that stops matching means the decision moved, and a test that
// quietly matched nothing would report that as agreement.
func find(t *testing.T, text, what string, pattern *regexp.Regexp) string {
	t.Helper()

	m := pattern.FindStringSubmatch(text)
	if m == nil {
		t.Fatalf("%s: found no %s (looked for %v).\nIf it moved rather than went away, move this pattern with it — a release is not checkable by hand",
			workflowPath, what, pattern)
	}
	return m[1]
}

// TestReleasePublishedOnMerge is the contract's trigger case. A tag-only
// workflow is the failure that looks like nothing at all: the repository keeps
// merging, no release appears, and self-update has nothing to find.
func TestReleasePublishedOnMerge(t *testing.T) {
	t.Parallel()

	wf := readWorkflow(t)

	// The `on:` block, up to the first top-level key after it. Checking the
	// whole file would pass on a `branches: [main]` belonging to anything else.
	trigger := find(t, wf, "`on:` block", regexp.MustCompile(`(?s)\non:\n(.*?)\n[a-z]`))
	if !strings.Contains(trigger, "push:") || !strings.Contains(trigger, "branches: [main]") {
		t.Errorf("%s does not trigger on a push to main:\non:\n%s\nA release nobody triggers is not a release; self-update looks for one and finds nothing",
			workflowPath, trigger)
	}

	// The names are written in YAML here, in install.sh at T009, and in Go —
	// three languages that cannot share a constant. A drift is a 404 at the
	// exact moment somebody is installing.
	for _, arch := range published {
		asset := "crswd_${VERSION}_linux_" + arch + ".tar.gz"
		if !strings.Contains(wf, asset) {
			t.Errorf("%s uploads no asset named %s.\nThe installer and the updater ask for that name and nothing else", workflowPath, asset)
		}
	}
}

// TestVersionIsTheCommitCount covers both halves of the version: where the
// number comes from, and the checkout that makes it true. A shallow clone is
// the interesting one — `git rev-list --count HEAD` succeeds against it and
// answers 1, so every release would be v0.1 and none would outrank another.
func TestVersionIsTheCommitCount(t *testing.T) {
	t.Parallel()

	wf := readWorkflow(t)

	if !strings.Contains(wf, "git rev-list --count HEAD") {
		t.Errorf("%s does not count commits for the version.\nresearch.md settled this: github.run_number resets when a workflow is recreated, which makes an older release outrank a newer one", workflowPath)
	}
	if !strings.Contains(wf, "v0.$(git rev-list --count HEAD)") {
		t.Errorf("%s does not build the version as v0.<count>", workflowPath)
	}
	if !strings.Contains(wf, "fetch-depth: 0") {
		t.Errorf("%s checks out without `fetch-depth: 0`.\nactions/checkout fetches one commit by default, so the count is 1 and every release is v0.1 — a failure with no error in it", workflowPath)
	}
}

// TestWorkflowStampsTheSymbolTheBinaryReads is the second silent failure. A -X
// path naming no existing variable is not an error: the linker sets nothing,
// internal/buildinfo's "dev" default survives, and the release reports itself as
// an unreleased build to everyone who installs it. cmd/crswd's
// TestStampedVersionIsReported proves the spelling in theStampedSymbol works;
// this is what keeps the workflow using that one.
func TestWorkflowStampsTheSymbolTheBinaryReads(t *testing.T) {
	t.Parallel()

	raw, err := os.ReadFile(versionTestPath)
	if err != nil {
		t.Fatalf("read %s: %v", versionTestPath, err)
	}
	proven := regexp.MustCompile(`(?m)^const theStampedSymbol = "([^"]+)"$`).FindStringSubmatch(string(raw))
	if proven == nil {
		t.Fatalf("%s no longer declares theStampedSymbol; this test and the workflow have nothing left to agree with", versionTestPath)
	}

	stamped := find(t, readWorkflow(t), "-X assignment in the build's ldflags", regexp.MustCompile(`-X ([^\s=]+)=`))
	if stamped != proven[1] {
		t.Errorf("%s stamps %q; %s proves %q reaches the variable.\nA wrong symbol links cleanly and the release calls itself \"dev\" forever",
			workflowPath, stamped, versionTestPath, proven[1])
	}
}

// TestBinaryIsStaticallyLinked builds what the release builds, for every
// architecture it publishes, and reads the result.
//
// The declaration is checked as well as the artifact because the two catch
// different things. Dropping CGO_ENABLED=0 is invisible on a builder that has a
// C library — which every runner does — so the property alone would only fail
// somewhere nobody is looking; and the property alone would also pass on a host
// with no C compiler, where Go turns cgo off by itself.
//
// debug/elf rather than `ldd`: PT_INTERP is what ldd reports on, and ldd cannot
// read the cross-compiled arm64 artifact on an amd64 runner — which is exactly
// the artifact most likely to be wrong.
func TestBinaryIsStaticallyLinked(t *testing.T) {
	t.Parallel()

	wf := readWorkflow(t)

	cgo := find(t, wf, "CGO_ENABLED declaration", regexp.MustCompile(`(?m)^\s*CGO_ENABLED:\s*"?([^"\s#]*)"?\s*$`))
	if cgo != "0" {
		t.Fatalf("%s builds with CGO_ENABLED=%q, want \"0\".\nWith cgo on, net resolves through the host's C library and the binary links it — which works on the builder and fails on the host that downloads it", workflowPath, cgo)
	}

	// The architectures the workflow itself loops over, so dropping one is a
	// failure here rather than a missing asset.
	arches := strings.Fields(find(t, wf, "architecture loop", regexp.MustCompile(`(?m)^\s*for arch in ([^;]+); do\s*$`)))
	if strings.Join(arches, " ") != strings.Join(published, " ") {
		t.Fatalf("%s builds %v; the release publishes %v", workflowPath, arches, published)
	}

	// Replayed rather than approximated: link flags decide linkage too, so a
	// future -linkmode there has to reach this build to be caught by it.
	link := strings.ReplaceAll(find(t, wf, "ldflags on the build", regexp.MustCompile(`-ldflags "([^"]*)"`)), "$VERSION", "v0.0-test")

	for _, arch := range arches {
		t.Run(arch, func(t *testing.T) {
			t.Parallel()

			bin := filepath.Join(t.TempDir(), "crswd")
			build := exec.Command("go", "build", "-ldflags", link, "-o", bin, "./cmd/crswd") //nolint:gosec // G204: every argument is this test's own, read from a committed workflow.
			build.Dir = repoRoot
			build.Env = append(os.Environ(), "GOOS=linux", "GOARCH="+arch, "CGO_ENABLED="+cgo)
			if out, err := build.CombinedOutput(); err != nil {
				t.Fatalf("GOARCH=%s go build ./cmd/crswd: %v\n%s", arch, err, out)
			}

			f, err := elf.Open(bin)
			if err != nil {
				t.Fatalf("open the %s artifact as ELF: %v", arch, err)
			}
			defer f.Close() //nolint:errcheck // read-only, and a failure to close it says nothing about the artifact.

			for _, p := range f.Progs {
				if p.Type == elf.PT_INTERP {
					t.Errorf("the %s artifact names a dynamic loader; it needs a libc on the host that runs it, and the download-and-run promise is that it does not", arch)
				}
			}
			if libs, err := f.ImportedLibraries(); err != nil {
				t.Errorf("read the %s artifact's dynamic section: %v", arch, err)
			} else if len(libs) != 0 {
				t.Errorf("the %s artifact links %v at run time; a host without them cannot start it", arch, libs)
			}
		})
	}
}
