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
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"debug/elf"
	"encoding/base64"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/nctiggy/claude-remote-session-webhook/internal/config"
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
// the workflow computes rather than builds or copies.
//
// The signature belongs to the same set and is the one whose absence is silent:
// a release missing it looks complete on the page and is refused by every
// installer and every daemon that meets it.
var generated = []string{"SHA256SUMS", "SHA256SUMS.sig"}

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
			t.Errorf("%s publishes %s, which is not one of the assets this test knows about.\nIf a release now carries it, add it here and to contracts/release.md — \"every asset\" holds only while this list is the whole list", workflowPath, name)
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

// committedKeysPath is the file the release checks its own signing key against:
// the one the daemon embeds and install.sh copies its RELEASE_KEYS block from.
// Relative to the checkout, because the step reads it that way and the replay
// below then reads the copy the test wrote instead of the operator's.
const committedKeysPath = "internal/updater/release_key.txt"

// TestReleaseIsSigned replays the signing step against a key made here, and
// checks what came out the way the daemon will: ed25519 over the exact bytes of
// SHA256SUMS.
//
// The failure it exists for is a release published without a signature, or with
// one nothing can verify. Neither is visible on the release page — every asset
// is there and every checksum is right — and both are refused by every host
// that meets them. A checksum file travels with the assets it describes, so on
// its own it says only that the two arrived together; an unsigned release is
// not a weaker release, it is a release with nothing at all behind it.
func TestReleaseIsSigned(t *testing.T) {
	t.Parallel()

	wf := readWorkflow(t)

	const (
		checksumStep = "Checksum every asset"
		signStep     = "Sign SHA256SUMS"
		publishStep  = "Publish"
	)

	// Both orderings fail quietly. Signing above the checksum step signs
	// whatever SHA256SUMS held before this run — on a fresh runner nothing, so
	// it reads as a missing file rather than as a wrong order. Signing below
	// Publish leaves the release on the page without the one asset that makes
	// the rest of it mean anything, and the run is green either way.
	if stepAt(t, wf, signStep) < stepAt(t, wf, checksumStep) {
		t.Fatalf("%s signs before it takes checksums, so SHA256SUMS.sig covers whatever the file held beforehand rather than this release", workflowPath)
	}
	if stepAt(t, wf, signStep) > stepAt(t, wf, publishStep) {
		t.Fatalf("%s publishes before it signs, so the release carries no SHA256SUMS.sig.\nThe daemon and install.sh both refuse a release without one, and `latest` resolves to it — which stops installing altogether rather than shipping one release people can skip", workflowPath)
	}

	// The name the operator is told to use, in keygen's own output and in the
	// handover. A name that drifts from those is an empty value here, and an
	// empty value is a release refused for the absence of a secret nobody
	// created under that spelling.
	if secret := find(t, wf, "the `RELEASE_SIGNING_KEY` secret", regexp.MustCompile(`RELEASE_SIGNING_KEY: \$\{\{ secrets\.(\w+) \}\}`)); secret != "RELEASE_SIGNING_KEY" {
		t.Errorf("%s signs with the secret %s; `crswd keygen` and %s both tell the operator to create RELEASE_SIGNING_KEY", workflowPath, secret, committedKeysPath)
	}

	keys := find(t, wf, "the `PUBLIC_KEYS` the step checks its own key against", regexp.MustCompile(`PUBLIC_KEYS: (\S+)`))
	if keys != committedKeysPath {
		t.Errorf("%s checks the signing key against %s, but the daemon embeds %s and install.sh copies its lines from there.\nA release checked against some third file can be signed by a key neither of them accepts", workflowPath, keys, committedKeysPath)
	}
	if _, err := os.Stat(filepath.Join(repoRoot, keys)); err != nil {
		t.Errorf("%s reads %s, which is not in the working tree: %v\nThe step fails in CI, after the build has already succeeded", workflowPath, keys, err)
	}

	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate a release key: %v", err)
	}
	// A second pair, for the two cases about a key that is not the one the
	// release is checked against. Both halves are ephemeral and neither leaves
	// this process: the repository holds no private key, which is the whole
	// reason T013 stopped for a human.
	otherPub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate a second key: %v", err)
	}

	script := stepScript(t, wf, signStep)

	// The DER header the step wraps the seed in, derived rather than typed. With
	// any other 16 bytes openssl reads a different key or refuses the file, and
	// the difference between those two outcomes is the difference between a
	// release signed by nobody and a release that does not publish. Asserted
	// before the replay, so it still holds where openssl is absent.
	pkcs8, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		t.Fatalf("marshal a PKCS#8 private key: %v", err)
	}
	if header := base64.StdEncoding.EncodeToString(pkcs8[:len(pkcs8)-ed25519.SeedSize]); !strings.Contains(script, "'"+header+"'") {
		t.Errorf("the %q step does not wrap the seed with %q.\nThat is the PKCS#8 header openssl reads an ed25519 private key from, and it is the only shape it reads one in", signStep, header)
	}

	needsOpenSSL(t)

	// What the checksum step writes, in the format `sha256sum -c` reads. The
	// signature is over these bytes exactly, so what they say matters only in
	// that a signature over anything else is distinguishable from one over this.
	sums := []byte(fmt.Sprintf("%x  crswd_v0.0-test_linux_amd64.tar.gz\n%x  crswd.service\n",
		sha256.Sum256([]byte("a tarball")), sha256.Sum256([]byte("a unit"))))

	line := func(key ed25519.PublicKey) string { return base64.StdEncoding.EncodeToString(key) }

	for _, tc := range []struct {
		name   string
		secret string // what RELEASE_SIGNING_KEY holds
		keys   string // what the committed key file holds
		signs  bool
		says   string // for a refusal, part of what it has to say about it
	}{
		{
			name:   "signs with the key the release is verified against",
			secret: base64.StdEncoding.EncodeToString(priv),
			keys:   "# a comment, and a blank line\n\n" + line(pub) + "\n",
			signs:  true,
		},
		{
			// Rotation is additive: the file carries every key a release still
			// worth installing might be signed by, so the one signing today is
			// one of the lines rather than the only line.
			name:   "signs while the file still carries a retired key",
			secret: base64.StdEncoding.EncodeToString(priv),
			keys:   line(otherPub) + "\n" + line(pub) + "\n",
			signs:  true,
		},
		{
			// The state before the operator creates the secret, and the state
			// after somebody deletes it. Publishing here is the failure the
			// whole task is about: an unsigned release is one nobody can
			// install, and it would be what `latest` resolves to.
			name:   "refuses to publish when there is no secret to sign with",
			secret: "",
			keys:   line(pub) + "\n",
			says:   "RELEASE_SIGNING_KEY is not set",
		},
		{
			// Rotation done in the wrong order: the secret replaced before the
			// new public line was committed. Every install and every update
			// then refuses a release that looks perfect.
			name:   "refuses a key whose public half is committed nowhere",
			secret: base64.StdEncoding.EncodeToString(priv),
			keys:   line(otherPub) + "\n",
			says:   "is not one",
		},
		{
			// Step 5 of the rotation in release_key.txt is retiring a key, and
			// commenting the line out is how somebody retires one without
			// deleting it. install.sh skips a commented line, so a release
			// signed by that key is refused by every installer — the key has to
			// be a line of its own here, not merely somewhere in the file.
			name:   "refuses a key the file has commented out",
			secret: base64.StdEncoding.EncodeToString(priv),
			keys:   "# retired 2026-08-08: " + line(pub) + "\n",
			says:   "is not one",
		},
		{
			// The two halves of what keygen prints are adjacent in its output,
			// and the public one is what goes in a file. A secret holding 32
			// bytes is the paste that took the wrong line.
			name:   "refuses a secret that is not a whole private key",
			secret: base64.StdEncoding.EncodeToString(priv.Seed()),
			keys:   line(pub) + "\n",
			says:   "64 bytes",
		},
		{
			name:   "refuses a secret that is not base64 at all",
			secret: "paste the line, not the sentence",
			keys:   line(pub) + "\n",
			says:   "not base64",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			dir := t.TempDir()
			dist := filepath.Join(dir, "dist")
			if err := os.MkdirAll(dist, 0o750); err != nil {
				t.Fatalf("make dist/: %v", err)
			}
			if err := os.WriteFile(filepath.Join(dist, "SHA256SUMS"), sums, 0o600); err != nil {
				t.Fatalf("write dist/SHA256SUMS: %v", err)
			}
			if err := os.MkdirAll(filepath.Join(dir, filepath.Dir(committedKeysPath)), 0o750); err != nil {
				t.Fatalf("make %s: %v", filepath.Dir(committedKeysPath), err)
			}
			if err := os.WriteFile(filepath.Join(dir, committedKeysPath), []byte(tc.keys), 0o600); err != nil {
				t.Fatalf("write %s: %v", committedKeysPath, err)
			}

			// `bash -e`, which is what GitHub runs a `run:` block with when the
			// step names no shell, and the step's own `env:` supplied here
			// because stepScript returns the script and not the block around it.
			replay := exec.Command("bash", "-e", "-c", script) //nolint:gosec // G204: the script is this repository's own committed workflow.
			replay.Dir = dir
			replay.Env = append(os.Environ(),
				"RELEASE_SIGNING_KEY="+tc.secret,
				"PUBLIC_KEYS="+committedKeysPath,
			)
			out, replayErr := replay.CombinedOutput()
			sig, readErr := os.ReadFile(filepath.Join(dist, "SHA256SUMS.sig")) //nolint:gosec // G304: dist is this test's own t.TempDir.

			if !tc.signs {
				if replayErr == nil {
					t.Fatalf("the %q step signed this release and let the run continue to Publish:\n%s", signStep, out)
				}
				if readErr == nil {
					t.Errorf("the %q step refused and left a %d-byte dist/SHA256SUMS.sig behind.\nPublish uploads whatever is at that path, so a refusal that writes one publishes a signature nothing can verify", signStep, len(sig))
				}
				if !strings.Contains(string(out), tc.says) {
					t.Errorf("the %q step refused with:\n%s\nwhich says nothing about %q — and this message is all the operator gets", signStep, out, tc.says)
				}
				return
			}

			if replayErr != nil {
				t.Fatalf("replay of the %q step: %v\n%s", signStep, replayErr, out)
			}
			if readErr != nil {
				t.Fatalf("the %q step wrote no dist/SHA256SUMS.sig, which Publish then uploads: %v\n%s", signStep, readErr, out)
			}
			if len(sig) != ed25519.SignatureSize {
				t.Fatalf("dist/SHA256SUMS.sig is %d bytes; a detached ed25519 signature is %d, and the daemon reads it as those bytes and nothing else", len(sig), ed25519.SignatureSize)
			}
			if !ed25519.Verify(pub, sums, sig) {
				t.Errorf("dist/SHA256SUMS.sig is not a signature over dist/SHA256SUMS by the key that made it.\nThat is the call internal/updater makes and what install.sh hands openssl, so a signature over anything else is a release refused everywhere")
			}
		})
	}
}

// jobBlock returns one job's YAML, from its name to the next job's. Jobs sit at
// two spaces and everything inside one at four or more, so the next line at that
// indent is where this job ends — which matters because every assertion about a
// job is only about a job while it is read from inside one.
func jobBlock(t *testing.T, wf, job string) string {
	t.Helper()

	return find(t, wf, "job `"+job+"`",
		regexp.MustCompile(`(?s)\n  `+regexp.QuoteMeta(job)+`:\n(.*?)(?:\n  [a-z]|\z)`))
}

// stepAt returns where a step's `- name:` line appears in the workflow, so two
// steps can be placed against each other. Order is the one thing a replay
// cannot see: a step is replayed on its own, and every assertion about what it
// does still holds when it runs at the wrong point in the job.
func stepAt(t *testing.T, wf, step string) int {
	t.Helper()

	i := strings.Index(wf, "- name: "+step)
	if i < 0 {
		t.Fatalf("%s has no step named %q", workflowPath, step)
	}
	return i
}

// tagRange returns v0.<lo> … v0.<hi>, ascending — the order `gh release list`
// is least likely to answer in, so a step that keeps the first twenty it is
// handed fails here rather than by deleting the ten newest releases.
func tagRange(lo, hi int) []string {
	tags := make([]string, 0, hi-lo+1)
	for n := lo; n <= hi; n++ {
		tags = append(tags, fmt.Sprintf("v0.%d", n))
	}
	return tags
}

// without returns tags minus drop: "everything past the limit except the one a
// pointer is holding".
func without(tags []string, drop string) []string {
	kept := make([]string, 0, len(tags))
	for _, tag := range tags {
		if tag != drop {
			kept = append(kept, tag)
		}
	}
	return kept
}

// ghStub is a bash function that shadows gh(1) for the replay. The step calls
// gh as a plain command, so a function of that name is what it reaches, and
// nothing has to be put on PATH or made executable.
//
// It answers the step's two reads the way gh answers them — one tag per line
// for the list, the tag the `latest` pointer resolves to for the view, and an
// error when there is no such pointer — and decides nothing else. Filtering
// here instead would move the rules under test into the stub, where a step that
// had stopped applying them would go on passing.
func ghStub(dir string, tags []string, latest string) string {
	// What `gh release view` does against a repository with no release at all,
	// which is why the step tolerates a failure from it.
	view := "    echo 'release not found' >&2\n    return 1"
	if latest != "" {
		view = fmt.Sprintf("    printf '%%s\\n' '%s'", latest)
	}

	quoted := make([]string, 0, len(tags))
	for _, tag := range tags {
		quoted = append(quoted, "'"+tag+"'")
	}

	return fmt.Sprintf(`gh() {
  case "$1 $2" in
  "release view")
%s
    ;;
  "release list")
    printf '%%s\n' %s
    ;;
  "release delete")
    printf '%%s\n' "$3" >> %q
    ;;
  *)
    echo "the step called gh in a way this stub does not answer: gh $*" >&2
    return 1
    ;;
  esac
}
`, view, strings.Join(quoted, " "), filepath.Join(dir, "deleted"))
}

// TestRetentionKeepsTwentyAndNeverTheNewestTwo replays the prune step against a
// stubbed gh and reads back which releases it asked to delete.
//
// Pruning by age alone is the version of this that looks finished. It bounds
// the list, which is the requirement as stated, and it deletes whatever
// `latest` resolves to on any day that pointer is not the newest release — a
// prerelease, a draft, or one marked by hand moves it. The result is an
// install URL that 404s, which to the person running the installer is
// indistinguishable from the project being broken.
//
// Ranking is the same failure in a quieter form. The version is the commit
// count, so the number in the tag is the ordering; ranking by date puts a
// re-published old release at the top, and ranking as text puts v0.9 above
// v0.30.
func TestRetentionKeepsTwentyAndNeverTheNewestTwo(t *testing.T) {
	t.Parallel()

	wf := readWorkflow(t)

	const pruneStep = "Prune old releases"

	// Pruning before publishing counts a list the new release is not in yet, so
	// it prunes one release too many — and if the publish then fails, it has
	// made room for a release that does not exist.
	if stepAt(t, wf, pruneStep) < stepAt(t, wf, "Publish") {
		t.Fatalf("%s prunes before it publishes, so it prunes to twenty and then makes it twenty-one, and a publish that fails afterwards has already cost a release", workflowPath)
	}

	keep := find(t, wf, "`KEEP` declaration", regexp.MustCompile(`(?m)^\s*KEEP:\s*"?(\d+)"?\s*$`))
	if keep != "20" {
		t.Errorf("%s keeps the last %s releases; contracts/release.md fixes the limit at 20", workflowPath, keep)
	}

	script := stepScript(t, wf, pruneStep)

	tests := []struct {
		name    string
		tags    []string
		latest  string
		keep    string
		deleted []string
	}{
		{
			// Thirty releases, the pointer where it usually is. Ten go.
			name:    "prunes to the limit, oldest first",
			tags:    tagRange(1, 30),
			latest:  "v0.30",
			keep:    keep,
			deleted: tagRange(1, 10),
		},
		{
			// The same list with the pointer somewhere it is allowed to be.
			// v0.4 is past the limit and stays, because deleting it is what
			// turns "install with one command" into a 404.
			name:    "never what the latest pointer resolves to",
			tags:    tagRange(1, 30),
			latest:  "v0.4",
			keep:    keep,
			deleted: without(tagRange(1, 10), "v0.4"),
		},
		{
			name:   "nothing to prune at the limit",
			tags:   tagRange(1, 20),
			latest: "v0.20",
			keep:   keep,
		},
		{
			// The floor, which is only visible below the limit. A retention
			// number is exactly the knob somebody turns down in a hurry, and
			// rolling back needs something to roll back to.
			name:    "the newest two survive a lowered limit",
			tags:    tagRange(1, 5),
			latest:  "v0.5",
			keep:    "0",
			deleted: tagRange(1, 3),
		},
		{
			// This workflow publishes v0.<count> and prunes v0.<count>.
			// Anything else in the list arrived another way and is not this
			// step's to delete.
			name:    "leaves tags this workflow did not publish",
			tags:    append(tagRange(1, 5), "nightly", "v1.0.0"),
			latest:  "v0.5",
			keep:    "0",
			deleted: tagRange(1, 3),
		},
		{
			name:    "no pointer at all",
			tags:    tagRange(1, 25),
			latest:  "",
			keep:    keep,
			deleted: tagRange(1, 5),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			dir := t.TempDir()

			// `bash -e`, the flags GitHub runs a `run:` block with when the step
			// names no shell, with the stub prepended so `gh` is a function
			// rather than the real client.
			replay := exec.Command("bash", "-e", "-c", ghStub(dir, tt.tags, tt.latest)+"\n"+script) //nolint:gosec // G204: the script is this repository's own committed workflow.
			replay.Dir = dir
			// KEEP is step-level `env:` in the workflow, which the replayed
			// `run:` body does not carry with it.
			replay.Env = append(os.Environ(), "KEEP="+tt.keep)
			out, err := replay.CombinedOutput()
			if err != nil {
				t.Fatalf("replay of the %q step: %v\n%s", pruneStep, err, out)
			}

			raw, err := os.ReadFile(filepath.Join(dir, "deleted")) //nolint:gosec // G304: dir is this test's own t.TempDir.
			if err != nil && !errors.Is(err, fs.ErrNotExist) {
				t.Fatalf("read what the step deleted: %v", err)
			}

			got := map[string]bool{}
			for _, tag := range strings.Fields(string(raw)) {
				got[tag] = true
			}
			want := map[string]bool{}
			for _, tag := range tt.deleted {
				want[tag] = true
			}

			for tag := range want {
				if !got[tag] {
					t.Errorf("the step left %s in place. With %d releases and KEEP=%s it is past the limit and no rule holds it, so the list only grows", tag, len(tt.tags), tt.keep)
				}
			}
			for tag := range got {
				if !want[tag] {
					t.Errorf("the step deleted %s.\nWhat is kept past the limit is not a nicety: `latest` is the URL the installer downloads through, the newest two are what a rollback has to land on, and a tag this workflow did not publish is not this workflow's to remove", tag)
				}
			}
			if t.Failed() {
				t.Logf("the step deleted %v and said:\n%s", strings.Fields(string(raw)), out)
			}
		})
	}
}

// TestUnitRestartsAlways is the last link in the self-update chain and the only
// one that is not Go. Step 7 of contracts/self-update.md is a deliberate
// `exit 0`, taken once the staged binary has been verified and renamed into
// place: the daemon stops, and systemd starting it again is what actually puts
// the new binary into service.
//
// `Restart=on-failure` — what this unit shipped with, and the value that reads
// as the careful one — treats that exit as success and does nothing at all. The
// update then completes, reports success, and leaves the host with a new binary
// and no daemon running it. Nothing logs an error, because nothing failed.
//
// The comment is checked too, because on its own the directive invites the
// correction that breaks it: a daemon that exits on purpose and is restarted
// anyway looks like a unit nobody tightened.
func TestUnitRestartsAlways(t *testing.T) {
	t.Parallel()

	// Read through the path the release publishes this file from, rather than
	// naming it a second time here — a rename in deploy/ then fails in one place.
	src := deployed["crswd.service"]
	raw, err := os.ReadFile(filepath.Join(repoRoot, src))
	if err != nil {
		t.Fatalf("read %s: %v", src, err)
	}

	// Restart= is only a restart policy inside [Service]; anywhere else systemd
	// rejects the whole unit. The comment block immediately above it is kept as
	// the reason for whichever directive follows — a blank line ends a block, so
	// prose further up the file cannot stand in for one that is missing here.
	var (
		section string
		comment []string
		values  []string
		reason  string
	)
	for _, line := range strings.Split(string(raw), "\n") {
		trimmed := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(trimmed, "#"):
			comment = append(comment, trimmed)
			continue
		case trimmed == "":
			comment = nil
			continue
		case strings.HasPrefix(trimmed, "["):
			section, comment = trimmed, nil
			continue
		}
		// RestartSec= is not a restart policy; the `=` is what separates them.
		if section == "[Service]" && strings.HasPrefix(trimmed, "Restart=") {
			values = append(values, strings.TrimPrefix(trimmed, "Restart="))
			reason = strings.Join(comment, "\n")
		}
		comment = nil
	}

	switch len(values) {
	case 1:
	case 0:
		t.Fatalf("%s sets no Restart= in [Service].\nsystemd's default is Restart=no, so the exit self-update ends with is just the daemon stopping: every check passes, the new binary is in place, and nothing on the host is running it", src)
	default:
		t.Fatalf("%s sets Restart= %d times in [Service] (%v); systemd takes the last, which is not what reading the file suggests", src, len(values), values)
	}

	if got := values[0]; got != "always" {
		t.Errorf("%s sets Restart=%s, want Restart=always.\nSelf-update finishes by exiting 0 on purpose (contracts/self-update.md, step 7) and depends on systemd starting the daemon again; %s does not restart after a clean exit, so the update succeeds and the service never comes back", src, got, got)
	}

	if !strings.Contains(strings.ToLower(reason), "update") {
		t.Errorf("%s sets Restart=always with no comment above it naming self-update as the reason. What is above it is:\n%s\nUnexplained, the directive reads as a daemon nobody bothered to bound, and on-failure is the obvious tightening — which silently removes the last step of the update path", src, reason)
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

const (
	// verifyInstallJob is contracts/installer.md's fourth task, and the only
	// place any claim about the installer is made on a machine that has never
	// seen this project.
	verifyInstallJob = "verify-install"

	// freshStep is the step that refuses to prove anything on a host where the
	// answer is already yes.
	freshStep = "Nothing the installer writes is here yet"

	// installStep is the first of the two runs, and the only one that meets a
	// host with nothing on it. Everything the installer writes for the first
	// time is measured there.
	installStep = "Install"

	// secondRunStep is the run that carries the requirement. The first one only
	// proves an install; this one is the re-run an operator does to take a newer
	// binary, on a host they have configured since.
	secondRunStep = "Install again, over an edited config and an edited unit"
)

// commands strips the comments and the `::error::` messages out of a block of
// workflow, leaving what a job actually runs.
//
// Both halves are traps this test fell into while it was being written, and both
// fail in the same direction. A step that explains itself names the command it
// runs and the answer it wants twice over — once in the comment above the check
// and once in the sentence printed when the check fails — so a search over the
// raw text is satisfied by the prose and goes on being satisfied after the check
// itself has been deleted. Two of the mutations written against this test passed
// for exactly that reason before this existed.
func commands(block string) string {
	var kept []string
	for _, line := range strings.Split(block, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "#") || strings.Contains(line, "::error::") {
			continue
		}
		kept = append(kept, line)
	}
	return strings.Join(kept, "\n")
}

// TestVerifyInstallProvesItOnAnotherMachine reads the job that is the whole
// answer to "how is the installer proven at all".
//
// Nothing here can run it — it installs a published release onto a host that has
// never seen this project, which is precisely what this machine is not — so what
// a working tree can check is the shape the job has to keep. Every assertion
// below is a way for it to go on passing while proving nothing:
//
//   - moved to the self-hosted runners, which are the operator's own machines,
//     where the binary, the unit, the configuration and the PATH entry are all
//     already there and the installer could do nothing at all and still be green
//   - run before or beside the release, so what it installs is the version
//     before this one
//   - run once, which cannot ask the question the second run exists for
//   - the freshness list drifting from what install.sh writes, so a file the
//     installer creates is one this job found already sitting there
//
// The last arrives on its own: install.sh grows a fifth path, nothing here
// changes, and that file is never once watched being created.
func TestVerifyInstallProvesItOnAnotherMachine(t *testing.T) {
	t.Parallel()

	wf := readWorkflow(t)
	job := commands(jobBlock(t, wf, verifyInstallJob))

	// GitHub-hosted is the requirement here rather than the fallback. #77 moved
	// ci.yml to self-hosted runners for a reason that does not reach this job:
	// those machines are the operator's, and this project is installed on them.
	if runner := find(t, job, "`runs-on:` in the "+verifyInstallJob+" job",
		regexp.MustCompile(`(?m)^\s*runs-on: (.+)$`)); runner != "ubuntu-latest" {
		t.Errorf("the %s job runs on %s; contracts/installer.md requires ubuntu-latest.\nThe self-hosted runners are the operator's own machines, where every precondition the installer exists to create is already true — a run there is green whatever the installer does, which is the failure this job was written against",
			verifyInstallJob, runner)
	}

	// After the release, because the release is what it installs.
	if needs := find(t, job, "`needs:` in the "+verifyInstallJob+" job",
		regexp.MustCompile(`(?m)^\s*needs: (.+)$`)); needs != "release" {
		t.Errorf("the %s job needs %q; it has to run after `release`.\nWithout that it installs whatever the `latest` pointer resolved to before this run, and reports the result as a check on what this run published",
			verifyInstallJob, needs)
	}

	// Every path install.sh writes, read out of install.sh — the four are
	// spelled there relative to $HOME and nowhere else, so a fifth one added the
	// same way is picked up here without this test being told about it.
	script := readInstaller(t)
	var written []string
	for _, m := range regexp.MustCompile(`(?m)^readonly [A-Z_]+="(\.[^"]+)"$`).FindAllStringSubmatch(script, -1) {
		written = append(written, m[1])
	}
	if len(written) < 4 {
		t.Fatalf("%s declares %d paths under $HOME (%v); it writes at least four — the binary, the unit, the record and the config.\nIf they are spelled some other way now, spell them that way here too: this test is the only thing keeping the CI job's freshness check level with them",
			installerPath, len(written), written)
	}

	fresh := commands(stepScript(t, wf, freshStep))
	for _, rel := range written {
		if !strings.Contains(fresh, rel) {
			t.Errorf("the %q step does not check ~/%s, which %s writes.\nA path this job does not know about is one it never sees created: it is there after the install because it was there before, and nothing says so",
				freshStep, rel, installerPath)
		}
	}

	// The one the loop above cannot find: allowed_roots names a directory, and
	// it is spelled without a leading dot. It is also the path whose absence has
	// no symptom here — nothing in this job starts the daemon, and an
	// allowed_roots entry that does not resolve is a startup failure — so the
	// only thing standing behind it is that this job watched it appear and read
	// the configuration that names it.
	root := config.DefaultRootName
	if !regexp.MustCompile(`(?m)^\s*` + regexp.QuoteMeta(root) + `\b`).MatchString(fresh) {
		t.Errorf("the %q step does not check ~/%s, the directory the installed configuration allows sessions to run in.\nA runner image that shipped one would turn \"the installer created it\" into a description of what was already there",
			freshStep, root)
	}
	install := commands(stepScript(t, wf, installStep))
	if !strings.Contains(install, "$HOME/"+root) {
		t.Errorf("the %q step never names $HOME/%s.\nThe configuration written on that host says a session may only run there, and a configuration naming a directory nobody created is a daemon that refuses to boot on a host where every file looks right",
			installStep, root)
	}
	if !strings.Contains(install, "-d ") {
		t.Errorf("the %q step asks nothing about a directory.\nThat the configuration names the root is half of it; whether the root is there is the half the daemon fails on",
			installStep)
	}

	// Twice, and the second run is the one that carries the requirement: a
	// re-run is how an operator takes a newer binary, and the two files it must
	// leave alone are the only two on that host they wrote themselves.
	runs := regexp.MustCompile(`bash install\.sh`).FindAllStringIndex(job, -1)
	if len(runs) != 2 {
		t.Fatalf("the %s job holds %d `bash install.sh`; contracts/installer.md says two — one to prove it installs, one to prove it does not overwrite.\nA single run cannot ask the second question at all",
			verifyInstallJob, len(runs))
	}

	// The edits happen inside the second step and before its install, which is
	// the only place they can be read as edits. Measured against that step
	// rather than against everything between the two runs: the first step also
	// names the config, to check its mode, and a span that wide is satisfied by
	// a job that reads both files and changes neither.
	second := commands(stepScript(t, wf, secondRunStep))
	before, _, _ := strings.Cut(second, "bash install.sh")
	for _, rel := range []string{installedConfig, installedUnit} {
		if !strings.Contains(before, rel) {
			t.Errorf("the %q step does not touch ~/%s before it runs the installer again.\nThe second run only means something on a host somebody has since configured; against an untouched one it takes the same branch as the first and agrees with itself",
				secondRunStep, rel)
		}
	}
	if !strings.Contains(before, ">>") {
		t.Errorf("the %q step changes neither file before running the installer again.\nNaming them is not editing them, and an installer asked to replace what it wrote itself is the one case that is supposed to say yes",
			secondRunStep)
	}

	// And compared as bytes afterwards. "Still there" is what an installer that
	// rewrote the config from its own template also leaves behind — a file of
	// the right shape holding none of the operator's settings.
	if after := job[runs[1][1]:]; !strings.Contains(after, "sha256sum -c") {
		t.Errorf("the %s job does not compare the files it edited after the second install.\nWhat it must show is that they came back byte-identical, which is not the same as them still existing",
			verifyInstallJob)
	}

	// The one assertion the task names, and the one no Go test can make: a
	// systemd that was actually asked, and an answer that is actually compared.
	asked := strings.Contains(job, "systemctl --user is-active crswd")
	compared := strings.Contains(job, "inactive")
	if !asked || !compared {
		t.Errorf("the %s job does not run `systemctl --user is-active crswd` and require it to answer `inactive` (asked=%t, compared=%t).\nAn installer that started it would have enabled a daemon that spawns shells with the permission prompt turned off, at boot, on a host whose operator has not yet said who may reach the dashboard",
			verifyInstallJob, asked, compared)
	}
}
