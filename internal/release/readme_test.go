package release_test

// What README.md has to be true of.
//
// It is the only file here that somebody reads before they have the repository,
// so the install line in it is the first thing this project ever asks anyone to
// run. Three claims stop being true silently:
//
//   - the command drifts from the one install.sh documents for itself, and the
//     README asks a stranger to curl a URL that 404s
//   - "clone and build" creeps back above it, which is the state this milestone
//     exists to end: a compiler is not a prerequisite for running a binary that
//     has already been built and published
//   - the rollback path moves, and the page that names it does not follow. A
//     rollback is read exactly once, by somebody whose daemon will not start,
//     and a wrong path there is worth less than no path at all
//
// The go.sum assertion is here rather than only in cmd/crswd's quickstart suite
// because that one is behind a build tag: `go test ./...` does not reach it, and
// neither does the untagged half of CI. An import added in between would be
// caught by a suite nobody ran on the change that added it.

import (
	"bytes"
	"os"
	"regexp"
	"strings"
	"testing"
)

const readmePath = repoRoot + "/README.md"

func readReadme(t *testing.T) string {
	t.Helper()

	raw, err := os.ReadFile(readmePath)
	if err != nil {
		t.Fatalf("read %s: %v", readmePath, err)
	}
	return string(raw)
}

// TestReadmeLeadsWithTheOneLiner is FR-012 as the reader meets it: installation
// is a single command requiring no prior knowledge of the project.
//
// The command is read out of install.sh's own header rather than typed here.
// The two are the same string or one of them is wrong, and the one that gets run
// is the one in the README — so a base URL that moves in the script and not on
// the page is a failure here instead of a 404 for whoever is installing.
func TestReadmeLeadsWithTheOneLiner(t *testing.T) {
	t.Parallel()

	readme := readReadme(t)

	oneLiner := findIn(t, installerPath, readInstaller(t), "the install command it documents for itself",
		regexp.MustCompile(`(?m)^#\s+(curl\s+\S+\s+\S+\s*\|\s*bash)\s*$`))

	at := strings.Index(readme, oneLiner)
	if at < 0 {
		t.Fatalf("%s never carries the command %s documents:\n\t%s\nThey are written twice and cannot share a constant; only the drift is preventable",
			readmePath, installerPath, oneLiner)
	}

	// "Leads with" is a position, not a mention. Anything asking for a compiler
	// or a checkout has to come after it — the from-source path is for people
	// changing the daemon, and it was the top of this file until this milestone.
	for _, fromSource := range []string{"go build", "git clone", "go mod download"} {
		if i := strings.Index(readme, fromSource); i >= 0 && i < at {
			t.Errorf("%s reaches %q at byte %d, before the install line at byte %d.\nThe first instruction on this page must not need a toolchain: a release exists so that it does not",
				readmePath, fromSource, i, at)
		}
	}
}

// TestReadmeDocumentsRollingBack is FR-022's other half. Naming a version is the
// rollback for a daemon that still answers; crswd.previous is the rollback for
// the one that does not, and that is the case somebody is reading this page in.
//
// The path is derived from where install.sh puts the binary, because the two
// have to agree: crswd.previous is whatever the swap renamed away from, and it
// sits beside the file the installer placed.
func TestReadmeDocumentsRollingBack(t *testing.T) {
	t.Parallel()

	readme := readReadme(t)

	binary := findIn(t, installerPath, readInstaller(t), "where it installs the binary",
		regexp.MustCompile(`(?m)^readonly BINARY="([^"]+)"`))

	for _, want := range []string{
		"~/" + binary + ".previous",
		"POST /dashboard/update",
		"version=",
	} {
		if !strings.Contains(readme, want) {
			t.Errorf("%s never names %q.\nA rollback is read once, by somebody whose daemon will not start; what it leaves out is not looked up elsewhere", readmePath, want)
		}
	}
}

// TestReleaseHasNoDependencies is FR-034, and the reason FR-010 is achievable at
// all: a static binary that runs on a host with no compiler and no development
// library is what this milestone ships, and it stays possible only while nothing
// is imported.
//
// go.sum's absence is the whole assertion — a module with no requirements cannot
// produce one. The go.mod check is the same property one step earlier, so that a
// `require` block added without a build still fails.
func TestReleaseHasNoDependencies(t *testing.T) {
	t.Parallel()

	const sumPath = repoRoot + "/go.sum"

	switch _, err := os.Stat(sumPath); {
	case err == nil:
		t.Errorf("%s exists, so something was imported.\nEvery import here needs justification under docs/security.md §5 first — and the signing design in particular is built on there being none", sumPath)
	case !os.IsNotExist(err):
		t.Errorf("stat %s: %v", sumPath, err)
	}

	mod, err := os.ReadFile(modulePath)
	if err != nil {
		t.Fatalf("read %s: %v", modulePath, err)
	}
	if bytes.Contains(mod, []byte("require")) {
		t.Errorf("%s carries a require block:\n%s", modulePath, mod)
	}
}
