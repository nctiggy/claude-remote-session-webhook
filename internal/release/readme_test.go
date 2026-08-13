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

	"github.com/nctiggy/claude-remote-session-webhook/internal/updater"
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

// TestReadmeAndInstallerNameTheSameDoors is the README's half of one fact stated
// twice: the installed configuration is complete except for a door, and until the
// operator picks one the dashboard admits nobody.
//
// It is stated twice because it is met twice — once in the terminal the installer
// prints to, once on the page somebody reads before running it — and until this
// test nothing held the page to it. The failure that leaves is silent and
// expensive: a README that names one door, or names a key by a spelling the
// configuration file does not take, sends its reader to a daemon that starts,
// stays healthy, and refuses their browser exactly as it refuses a stranger's.
//
// The vocabulary is declared in install_test.go and read by both tests, so the
// two documents cannot be reworded one at a time. That is what makes this an
// agreement rather than a second list; the installer's own half is checked
// against its output rather than its text, and the reason is written there.
//
// **Must fail when** the README drops a door or rewords the phrase.
func TestReadmeAndInstallerNameTheSameDoors(t *testing.T) {
	t.Parallel()

	readme := readReadme(t)

	for _, want := range append(append([]string{}, doorKeys...), doorClosedPhrase) {
		if !strings.Contains(readme, want) {
			t.Errorf("%s never says %q.\nThe installer's next steps say it, and a reader who arrives by the page rather than the terminal is owed the same sentence: the daemon they installed is closed rather than broken, and this is the setting that opens it", readmePath, want)
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

// deployREADMEPath is the operator's longer half of the deployment story.
const deployREADMEPath = repoRoot + "/deploy/README.md"

// TestTheDocsSendOperatorsToPathsThatExist holds the prose to the constants the
// code composes.
//
// Every path named in these documents is one an operator will type. A drop-in
// path that drifts from the one systemd reads sends them to edit a file that
// changes nothing — present, correct, and silently not in effect, which is the
// hardest possible thing to debug and exactly what this milestone set out to
// stop producing.
func TestTheDocsSendOperatorsToPathsThatExist(t *testing.T) {
	t.Parallel()

	home := "/home/operator"
	unit := updater.NewUnit(func(name string) string {
		if name == "HOME" {
			return home
		}
		return ""
	})

	// As an operator reads them: `~/…` rather than an absolute path.
	tilde := func(p string) string { return "~" + strings.TrimPrefix(p, home) }

	for _, path := range []string{deployREADMEPath, readmePath} {
		body, err := os.ReadFile(path) //nolint:gosec // G304: path comes from the two constants above.
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		if !strings.Contains(string(body), tilde(unit.DropInPath())) {
			t.Errorf("%s never names %s, so an operator told to add a hardening override has no path to add it at",
				path, tilde(unit.DropInPath()))
		}
	}
}

// TestTheDeployDocsNameTheProtectKernelTunablesTrap is FR-015.
//
// Measured: relaxing NoNewPrivileges alone leaves NoNewPrivs at 1, because
// ProtectKernelTunables=true implies it and systemd treats that as a floor. An
// operator who trims the override to "just the setting I need" gets a file that
// does nothing, and `sudo` still fails with no visible cause. Documentation that
// omitted this would be documentation that produces that afternoon.
func TestTheDeployDocsNameTheProtectKernelTunablesTrap(t *testing.T) {
	t.Parallel()

	body, err := os.ReadFile(deployREADMEPath)
	if err != nil {
		t.Fatalf("read %s: %v", deployREADMEPath, err)
	}
	for _, want := range []string{"ProtectKernelTunables", "implies"} {
		if !strings.Contains(string(body), want) {
			t.Errorf("%s does not mention %q; an override missing that line is inert rather than weaker", deployREADMEPath, want)
		}
	}
}

// TestTheDeployDocsSayRunningSessionsKeepTheirEnvironment is the limit stated
// rather than papered over.
//
// A process's environment cannot be changed from outside it, so a pane started
// by an older build keeps the secret it was given until it is recreated — and
// nothing on the host can tell the operator that, because nothing can look. A
// host reported as fixed while still leaking is worse than one reported as
// leaking, so the documentation is the only place this can be said.
func TestTheDeployDocsSayRunningSessionsKeepTheirEnvironment(t *testing.T) {
	t.Parallel()

	body, err := os.ReadFile(deployREADMEPath)
	if err != nil {
		t.Fatalf("read %s: %v", deployREADMEPath, err)
	}
	for _, want := range []string{"recreate", "cannot be changed from outside"} {
		if !strings.Contains(string(body), want) {
			t.Errorf("%s does not tell the operator that already-running sessions keep their environment (%q missing)", deployREADMEPath, want)
		}
	}
}
