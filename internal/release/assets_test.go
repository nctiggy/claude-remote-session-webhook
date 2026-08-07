package release_test

// What a release is, checked against the workflow that produces one.
//
// Nothing here can run the release — it publishes to GitHub — so these tests do
// the two things that can be done from a working tree: they read
// .github/workflows/release.yml for the decisions that are silent when they go
// wrong, and they replay its build so the artifact's properties are measured
// rather than asserted about.
//
// Four of the six failures this guards are silent on the builder and loud on
// somebody else's machine, which is the whole subject of this milestone:
//
//   - cgo left on          — links libc, runs here, fails where there is no libc
//   - a mistyped -X path   — the linker sets nothing and the release says "dev"
//   - a shallow checkout   — `git rev-list --count HEAD` counts 1, forever
//   - the binary alone     — it builds, it publishes, and whoever downloads it
//     still has to write a unit file by hand
//
// The other two are silent everywhere. A tag-only trigger means releases simply
// stop happening and self-update finds nothing; and a SHA256SUMS covering only
// the tarballs leaves the deployment files — the ones written straight into
// ~/.config — with nothing to check them against, which reads as verified.

import (
	"crypto/sha256"
	"debug/elf"
	"fmt"
	"os"
	"os/exec"
	"path"
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

// deployed is the other half of a release: the files an operator needs before
// the binary is any use. The key is the published asset name, the value where
// those bytes come from — deploy/ already carries working, commented examples,
// and the release ships those rather than a second copy that drifts.
//
// The unit is renamed on the way out, to the name it is installed under. An
// asset called crswd.example.service invites carrying the word "example" into
// ~/.config/systemd/user.
var deployed = map[string]string{
	"crswd.service":           "deploy/crswd.example.service",
	"cloudflared.example.yml": "deploy/cloudflared.example.yml",
	"crswd-api":               "deploy/crswd-api",
}

// generated is the rest of the seven names contracts/release.md fixes: assets
// the workflow computes rather than builds or copies. SHA256SUMS.sig joins it
// at T014, which cannot run until the operator holds a signing key.
var generated = []string{"SHA256SUMS"}

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

// uploadedAssets returns the asset names the publish step hands to
// `gh release create`, spelled as the YAML spells them.
//
// Read from that one command rather than from the file as a whole: a path
// written anywhere else is a file that was made, not a file anybody can
// download, and the difference is the entire subject of TestReleaseCarriesEveryAsset.
func uploadedAssets(t *testing.T, wf string) map[string]bool {
	t.Helper()

	// One backslash-continued line per asset, so the command ends at the first
	// line that does not continue. Reading to the end of the file instead would
	// swallow the verify-install job T012 adds after it.
	cmd := find(t, wf, "`gh release create` command", regexp.MustCompile(`gh release create((?:[^\n]*\\\n)*[^\n]*)`))

	// Every asset argument is a path; --generate-notes and "$VERSION" are not.
	names := map[string]bool{}
	for _, m := range regexp.MustCompile(`"([^"\s]*/[^"\s]*)"`).FindAllStringSubmatch(cmd, -1) {
		names[path.Base(m[1])] = true
	}
	return names
}

// TestReleaseCarriesEveryAsset checks the published set against the one
// contracts/release.md names — in both directions, because "every asset" is a
// claim about the whole list and stops being true the moment the workflow grows
// one this file has not heard of.
//
// The failure it exists for is the tempting one: the tarballs are obviously the
// release, so the deployment files read as documentation and get dropped. The
// CI run stays green, the release page looks right, and the operator who
// downloads it is back to writing a systemd unit from scratch.
func TestReleaseCarriesEveryAsset(t *testing.T) {
	t.Parallel()

	wf := readWorkflow(t)

	want := map[string]bool{}
	for _, arch := range published {
		want["crswd_${VERSION}_linux_"+arch+".tar.gz"] = true
	}
	for name := range deployed {
		want[name] = true
	}
	for _, name := range generated {
		want[name] = true
	}

	got := uploadedAssets(t, wf)

	for name := range want {
		if !got[name] {
			t.Errorf("%s publishes no asset named %s.\ncontracts/release.md names it as part of a release. A deployment file left out is one whoever downloads this writes by hand, which is the state this milestone exists to end; a computed one left out is a file that only ever existed on the runner", workflowPath, name)
		}
	}
	for name := range got {
		if !want[name] {
			t.Errorf("%s publishes %s, which is not one of the assets this test knows about.\nIf a release now carries it, add it here and to contracts/release.md — \"every asset\" holds only while this list is the whole list.\nSHA256SUMS.sig arrives with T014 and belongs in `generated` then", workflowPath, name)
		}
	}

	// Each deployment asset has to be a copy of something that exists. The
	// workflow names these paths in a string no compiler reads, so a rename in
	// deploy/ is caught here rather than by a release that publishes nothing
	// under a name the installer then asks for.
	for name, src := range deployed {
		staged := regexp.MustCompile(`(?m)^.*(deploy/\S+).*dist/` + regexp.QuoteMeta(name) + `(?:\s|$)`).FindStringSubmatch(wf)
		if staged == nil {
			t.Errorf("%s uploads %s but no step copies it out of deploy/.\nAn asset built from nothing is an empty file with the right name on it", workflowPath, name)
			continue
		}
		if staged[1] != src {
			t.Errorf("%s stages %s from %s; the release is meant to carry %s", workflowPath, name, staged[1], src)
		}
		if _, err := os.Stat(filepath.Join(repoRoot, staged[1])); err != nil {
			t.Errorf("%s copies %s, which is not in the working tree: %v\nThe copy fails in CI, after the build has already succeeded", workflowPath, staged[1], err)
		}
	}
}

// stepScript returns the named step's `run:` script, dedented so it can be
// executed. What a checksum file covers is a property of the file the step
// writes, not of the shell that writes it — a regex over the command would
// agree with anything that looks about right, so the step is replayed and the
// result read, the same way TestBinaryIsStaticallyLinked replays the build.
func stepScript(t *testing.T, wf, step string) string {
	t.Helper()

	block := find(t, wf, "step `"+step+"`",
		regexp.MustCompile(`(?s)\n      - name: `+regexp.QuoteMeta(step)+`\n(.*?)(?:\n      - |\z)`))
	body := find(t, block, "`run:` block in step `"+step+"`",
		regexp.MustCompile(`(?s)run: \|\n(.*)`))

	// A YAML block scalar is indented by its first line; strip that, and stop
	// at the first line holding less, which is the next key rather than shell.
	var script []string
	indent := ""
	for _, line := range strings.Split(body, "\n") {
		if strings.TrimSpace(line) == "" {
			script = append(script, "")
			continue
		}
		if indent == "" {
			indent = line[:len(line)-len(strings.TrimLeft(line, " "))]
		}
		if !strings.HasPrefix(line, indent) {
			break
		}
		script = append(script, strings.TrimPrefix(line, indent))
	}
	return strings.Join(script, "\n") + "\n"
}

// TestEveryAssetHasAChecksum runs the workflow's checksum step against a dist/
// holding everything the publish step uploads, and reads what it wrote.
//
// The failure it exists for is the reasonable-looking one: the tarballs are the
// thing being verified, so `sha256sum *.tar.gz` reads as complete. It is not.
// The unit file and the API helper are the assets that get written into
// ~/.config, and an installer that verifies the binary and takes the rest on
// trust has verified the half nobody doubted.
func TestEveryAssetHasAChecksum(t *testing.T) {
	t.Parallel()

	wf := readWorkflow(t)

	const (
		stageStep    = "Stage the deployment files"
		checksumStep = "Checksum every asset"
		publishStep  = "Publish"
	)

	// Order is the silent form of this failure. A checksum step above the
	// staging step sums exactly the two tarballs, and every assertion about
	// how it sums them still passes.
	at := func(step string) int {
		t.Helper()

		i := strings.Index(wf, "- name: "+step)
		if i < 0 {
			t.Fatalf("%s has no step named %q", workflowPath, step)
		}
		return i
	}
	if at(checksumStep) < at(stageStep) {
		t.Fatalf("%s takes checksums before it stages the deployment files, so SHA256SUMS covers the two tarballs and nothing else — which is this test's whole subject, reached without the run ever going red", workflowPath)
	}
	if at(checksumStep) > at(publishStep) {
		t.Fatalf("%s publishes before it takes checksums, so the uploaded SHA256SUMS is whatever a previous run left behind, or nothing at all", workflowPath)
	}

	const version = "v0.0-test"

	// Everything Publish uploads, minus the names that cannot be in the list:
	// SHA256SUMS is the file being written, and SHA256SUMS.sig (T014) signs it,
	// so neither exists when the sums are taken. Distinct contents per file, so
	// a checksum can be wrong as well as absent.
	want := map[string]string{}
	for name := range uploadedAssets(t, wf) {
		if strings.HasPrefix(name, "SHA256SUMS") {
			continue
		}
		name = strings.ReplaceAll(name, "${VERSION}", version)
		want[name] = "the bytes of " + name + "\n"
	}
	if len(want) == 0 {
		t.Fatalf("%s uploads nothing for a checksum to cover", workflowPath)
	}

	dir := t.TempDir()
	dist := filepath.Join(dir, "dist")
	if err := os.MkdirAll(dist, 0o750); err != nil {
		t.Fatalf("make dist/: %v", err)
	}
	for name, body := range want {
		if err := os.WriteFile(filepath.Join(dist, name), []byte(body), 0o600); err != nil {
			t.Fatalf("write dist/%s: %v", name, err)
		}
	}
	// The per-architecture build directories the tarballs are made from. They
	// live in dist/ and are not assets, so a step that sums them names files
	// nobody can download beside SHA256SUMS — and `sha256sum -c` then fails
	// against a release that is in fact correct.
	for _, arch := range published {
		if err := os.MkdirAll(filepath.Join(dist, arch), 0o750); err != nil {
			t.Fatalf("make dist/%s/: %v", arch, err)
		}
		if err := os.WriteFile(filepath.Join(dist, arch, "crswd"), []byte("an input, not an asset"), 0o600); err != nil {
			t.Fatalf("write dist/%s/crswd: %v", arch, err)
		}
	}

	// `bash -e`, which is what GitHub runs a `run:` block with when the step
	// names no shell. Replaying under stricter flags would report failures the
	// release itself would not have.
	//
	// VERSION is in the environment because the asset names carry it, so a step
	// that ever spells one out has it to spell it with.
	replay := exec.Command("bash", "-e", "-c", stepScript(t, wf, checksumStep)) //nolint:gosec // G204: the script is this repository's own committed workflow.
	replay.Dir = dir
	replay.Env = append(os.Environ(), "VERSION="+version)
	if out, err := replay.CombinedOutput(); err != nil {
		t.Fatalf("replay of the %q step: %v\n%s", checksumStep, err, out)
	}

	raw, err := os.ReadFile(filepath.Join(dist, "SHA256SUMS")) //nolint:gosec // G304: dist is this test's own t.TempDir.
	if err != nil {
		t.Fatalf("the %q step wrote no dist/SHA256SUMS, which Publish then uploads: %v", checksumStep, err)
	}

	// The format sha256sum itself emits and reads. quickstart.md documents
	// `sha256sum -c SHA256SUMS` against a downloaded release, so a homemade
	// layout here is a documented command that fails.
	entry := regexp.MustCompile(`^([0-9a-f]{64})  (\S.*)$`)
	got := map[string]string{}
	for _, line := range strings.Split(strings.TrimRight(string(raw), "\n"), "\n") {
		m := entry.FindStringSubmatch(line)
		if m == nil {
			t.Fatalf("dist/SHA256SUMS holds %q, which `sha256sum -c` cannot read", line)
		}
		got[m[2]] = m[1]
	}

	for name, body := range want {
		sum, covered := got[name]
		if !covered {
			t.Errorf("SHA256SUMS does not cover %s, which the release publishes.\nAn asset with no checksum is one the installer has to take on trust, and it is the deployment files — not the binary — that get written straight into ~/.config", name)
			continue
		}
		if digest := fmt.Sprintf("%x", sha256.Sum256([]byte(body))); sum != digest {
			t.Errorf("SHA256SUMS gives %s as %s; those bytes hash to %s", name, sum, digest)
		}
	}
	for name := range got {
		if _, known := want[name]; known {
			continue
		}
		if strings.ContainsRune(name, '/') {
			t.Errorf("SHA256SUMS names %q with a path in it.\nThe file is checked from the directory the assets were downloaded into, where no such directory exists — the names have to be bare", name)
			continue
		}
		t.Errorf("SHA256SUMS covers %q, which is not a published asset.\nEvery name in it has to be downloadable beside it, or `sha256sum -c SHA256SUMS` fails against a correct release", name)
	}

	// The claim quickstart.md makes, made by the tool that has to honour it.
	check := exec.Command("sha256sum", "-c", "SHA256SUMS")
	check.Dir = dist
	if out, err := check.CombinedOutput(); err != nil {
		t.Errorf("`sha256sum -c SHA256SUMS` beside the assets: %v\n%s", err, out)
	}
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
