package release_test

// What install.sh has to be true of, checked by running it.
//
// The installer is the one thing in this milestone that cannot be proven on
// this host: the binary, the unit and the configuration are already here and
// ~/.local/bin is already on PATH, so every precondition it exists to create is
// already true and a successful run demonstrates nothing. T012 moves that proof
// to a GitHub-hosted runner with a fresh HOME, running the published release.
//
// What can be proven from a working tree is the half that has nothing to do
// with the host — the order it does things in, what it refuses, and whether the
// three languages that spell the asset name still agree — so that is what is
// here. The script is run rather than pattern-matched, with bash functions
// shadowing the commands it calls, because "verifies before anything is
// executable" is a claim about a sequence of events and a regex cannot see one.
//
// The two failures worth naming:
//
//   - verification after unpacking. tar restores the mode stored in the
//     archive, so an installer that unpacks first has already written an
//     executable file it has not checked. Everything still prints "ok".
//   - a missing signature read as "nothing to verify". That is the one an
//     attacker chooses: serving a tarball and a matching SHA256SUMS is easy,
//     and the only thing standing in front of it is a file they cannot produce.

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

const (
	installerPath = repoRoot + "/install.sh"
	modulePath    = repoRoot + "/go.mod"

	// keyBlockOpen begins the heredoc install.sh reads its accepted release
	// keys from. It is empty in the repository and stays that way until the
	// operator commits a public key (T013), so the tests below that need a
	// signature to verify put an ephemeral one here first.
	keyBlockOpen = "cat <<'RELEASE_KEYS'\n"
)

// tarballName is Go's spelling of the release asset name — the third of the
// three contracts/release.md fixes, beside the YAML's and install.sh's. Nothing
// ships from this package, so this stays the Go side of that agreement until
// T018 gives the updater a real reason to build the string in production code;
// that constant then has to agree with this one too.
func tarballName(version, arch string) string {
	return "crswd_" + version + "_linux_" + arch + ".tar.gz"
}

func readInstaller(t *testing.T) string {
	t.Helper()

	raw, err := os.ReadFile(installerPath)
	if err != nil {
		t.Fatalf("read %s: %v", installerPath, err)
	}
	return string(raw)
}

// findIn is find(3) for a file other than the workflow: same contract, but it
// names the file it read, because "found no X" is only useful when it says
// where it looked.
func findIn(t *testing.T, path, text, what string, pattern *regexp.Regexp) string {
	t.Helper()

	m := pattern.FindStringSubmatch(text)
	if m == nil {
		t.Fatalf("%s: found no %s (looked for %v).\nIf it moved rather than went away, move this pattern with it", path, what, pattern)
	}
	return m[1]
}

// TestAssetNamesAgreeAcrossLanguages reads the name out of all three places it
// is written and requires them to be the same string.
//
// They cannot share a constant — one is YAML, one is shell, one is Go — so the
// duplication is unavoidable and only the drift is preventable. A drift here
// has no symptom until somebody runs the install line, at which point it is a
// 404 from a project that looks broken.
func TestAssetNamesAgreeAcrossLanguages(t *testing.T) {
	t.Parallel()

	const version, arch = "v0.42", "amd64"

	shell := strings.NewReplacer("${version}", version, "${arch}", arch).Replace(
		findIn(t, installerPath, readInstaller(t), "the tarball name it builds",
			regexp.MustCompile(`(?m)^\s*tarball="([^"]+)"`)))

	wf := readWorkflow(t)
	yaml := strings.NewReplacer("${VERSION}", version, "${arch}", arch).Replace(
		find(t, wf, "the tarball name it builds", regexp.MustCompile(`(crswd_\$\{VERSION\}_linux_[^"]+\.tar\.gz)`)))

	if want := tarballName(version, arch); shell != want || yaml != want {
		t.Errorf("the asset name is spelled three ways:\n  %s\t%s\n  %s\t%s\n  Go\t\t%s\nWhichever is wrong, the symptom is the same: the installer asks for a name the release does not publish, and the person running it sees a 404",
			installerPath, shell, workflowPath, yaml, want)
	}

	// The two names that carry no version, and so are spelled identically
	// everywhere. SHA256SUMS.sig is checked against install.sh alone until T014
	// publishes it and adds it to `generated`.
	script := readInstaller(t)
	for _, name := range append(append([]string{}, generated...), "SHA256SUMS.sig") {
		if !strings.Contains(script, name) {
			t.Errorf("%s never names %s.\nThe release publishes it and the installer has to ask for it by exactly that name", installerPath, name)
		}
	}
}

// TestInstallerNamesNobody is FR-020. The account name in the fetch URL is
// unavoidable — it is where the bytes are, and contracts/installer.md says so
// explicitly — but a home directory, a username or an address belongs to one
// person and this script belongs to the project.
func TestInstallerNamesNobody(t *testing.T) {
	t.Parallel()

	script := readInstaller(t)

	// Read from go.mod rather than typed here: writing the account name into a
	// test that checks for its absence puts the same string in one more file.
	mod, err := os.ReadFile(modulePath)
	if err != nil {
		t.Fatalf("read %s: %v", modulePath, err)
	}
	owner := findIn(t, modulePath, string(mod), "the module's account name",
		regexp.MustCompile(`(?m)^module\s+[^/\s]+/([^/\s]+)/`))

	for i, line := range strings.Split(script, "\n") {
		if strings.Contains(line, owner) && !strings.Contains(line, "https://") {
			t.Errorf("%s:%d names the account outside a URL:\n\t%s\nThe URL it fetches from is the one place that is unavoidable", installerPath, i+1, strings.TrimSpace(line))
		}
	}

	// A path under someone's home is the author's machine leaking into a script
	// meant for anybody's. $HOME and ~ are the same requirement satisfied.
	for _, home := range []string{"/home/", "/Users/", "/root/"} {
		if strings.Contains(script, home) {
			t.Errorf("%s hardcodes a path under %s.\nIt runs as whoever ran it; the only home it may write to is theirs", installerPath, home)
		}
	}

	if addr := regexp.MustCompile(`[\w.+-]+@[\w-]+\.[a-z]{2,}`).FindString(script); addr != "" {
		t.Errorf("%s carries an address (%s). It belongs to the project, not to a person", installerPath, addr)
	}
}

// stubs shadow the commands install.sh calls with bash functions — the same
// trick ghStub plays on gh(1): the script calls them as plain commands, so a
// function of that name is what it reaches, and nothing has to be written to a
// directory on PATH or made executable.
//
// Only curl and uname answer for themselves, because only they would otherwise
// reach the network or describe this host. The rest wrap the real tool and
// record that they ran: order is the property under test, and a stub standing
// in for openssl or tar would be the test agreeing with itself.
const stubs = `
_log() { printf '%s\n' "$*" >> "$STUB_LOG"; }

uname() {
  case "${1-}" in
    -s) printf '%s\n' "$STUB_OS" ;;
    -m) printf '%s\n' "$STUB_MACHINE" ;;
    *) command uname "$@" ;;
  esac
}

curl() {
  local out="" write="" url=""
  while [ $# -gt 0 ]; do
    case "$1" in
      -o) out="$2"; shift 2 ;;
      -w) write="$2"; shift 2 ;;
      -*) shift ;;
      *) url="$1"; shift ;;
    esac
  done
  _log "curl $url"
  case "$url" in
    */releases/latest)
      [ -z "$write" ] || printf '%s' "$STUB_RESOLVED"
      return 0
      ;;
  esac
  if [ -f "$STUB_RELEASE/${url##*/}" ]; then
    cp "$STUB_RELEASE/${url##*/}" "$out"
    return 0
  fi
  echo "curl: (22) The requested URL returned error: 404" >&2
  return 22
}

openssl() { _log openssl; command openssl "$@"; }
sha256sum() { _log sha256sum; command sha256sum "$@"; }
tar() { _log tar; command tar "$@"; }
chmod() { _log chmod; command chmod "$@"; }
install() { _log install; command install "$@"; }
`

// run is one execution of install.sh under those stubs.
type run struct {
	stdout string
	stderr string
	events []string
	err    error
}

// index is where an event first appears, or -1. The invariant every case below
// shares is an ordering between two of them: nothing that makes a file
// executable may run before both checks have passed.
func (r run) index(event string) int {
	for i, got := range r.events {
		if got == event || strings.HasPrefix(got, event+" ") {
			return i
		}
	}
	return -1
}

func (r run) ran(event string) bool { return r.index(event) >= 0 }

// runInstaller executes the real script with the stubs prepended, against a
// release directory the caller built.
func runInstaller(t *testing.T, script, releaseDir, version string) run {
	t.Helper()

	dir := t.TempDir()
	log := filepath.Join(dir, "events")

	cmd := exec.Command("bash", "-c", stubs+"\n"+script) //nolint:gosec // G204: the script is this repository's own committed installer.
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"STUB_LOG="+log,
		"STUB_OS=Linux",
		"STUB_MACHINE=x86_64",
		"STUB_RELEASE="+releaseDir,
		"STUB_RESOLVED=https://github.com/example/example/releases/tag/"+version,
		// Its scratch directory, and the daemon's home, both somewhere this
		// test owns rather than the one running the tests.
		"TMPDIR="+dir,
		"HOME="+dir,
	)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()

	var events []string
	if raw, readErr := os.ReadFile(log); readErr == nil { //nolint:gosec // G304: log is this test's own t.TempDir.
		events = strings.Split(strings.TrimRight(string(raw), "\n"), "\n")
	}
	return run{stdout: stdout.String(), stderr: stderr.String(), events: events, err: err}
}

// release is a published release as the workflow produces one, built in a
// temporary directory so a test can tamper with it.
type release struct {
	dir     string
	version string
	arch    string
	tarball string
	pub     ed25519.PublicKey
	priv    ed25519.PrivateKey
}

// fakeRelease writes the assets install.sh asks for: a tarball holding an
// executable crswd, a SHA256SUMS covering every asset the release carries, and
// a detached ed25519 signature over it.
//
// The key is generated here and never leaves the process. The repository holds
// none — an example key that happens to be valid is a real key in the
// repository, which is the whole reason T013 stops for a human.
func fakeRelease(t *testing.T, version, arch string) *release {
	t.Helper()

	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate a release key: %v", err)
	}
	r := &release{dir: t.TempDir(), version: version, arch: arch, tarball: tarballName(version, arch), pub: pub, priv: priv}

	// The archive the workflow makes: `tar -C dist/$arch -czf … crswd`, one
	// member, executable. The mode is what makes unpacking the moment this
	// stops being inert data.
	body := []byte("#!/bin/sh\necho crswd " + version + "\n")
	var gz bytes.Buffer
	zw := gzip.NewWriter(&gz)
	tw := tar.NewWriter(zw)
	if err := tw.WriteHeader(&tar.Header{Name: "crswd", Mode: 0o755, Size: int64(len(body))}); err != nil {
		t.Fatalf("write the tar header: %v", err)
	}
	if _, err := tw.Write(body); err != nil {
		t.Fatalf("write the tar body: %v", err)
	}
	for _, closer := range []func() error{tw.Close, zw.Close} {
		if err := closer(); err != nil {
			t.Fatalf("close the archive: %v", err)
		}
	}
	r.write(t, r.tarball, gz.Bytes())

	// SHA256SUMS covers every asset, and only the tarball is downloaded — so
	// the four names with no file beside them are the case an installer that
	// runs `sha256sum -c SHA256SUMS` gets wrong, against a release that is
	// entirely correct.
	sums := map[string][]byte{r.tarball: gz.Bytes()}
	for name := range deployed {
		sums[name] = []byte("the bytes of " + name + "\n")
	}
	names := make([]string, 0, len(sums))
	for name := range sums {
		names = append(names, name)
	}
	sort.Strings(names)

	var lines strings.Builder
	for _, name := range names {
		fmt.Fprintf(&lines, "%x  %s\n", sha256.Sum256(sums[name]), name)
	}
	r.write(t, "SHA256SUMS", []byte(lines.String()))
	r.sign(t, priv)
	return r
}

func (r *release) write(t *testing.T, name string, body []byte) {
	t.Helper()

	if err := os.WriteFile(filepath.Join(r.dir, name), body, 0o600); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

func (r *release) read(t *testing.T, name string) []byte {
	t.Helper()

	raw, err := os.ReadFile(filepath.Join(r.dir, name)) //nolint:gosec // G304: the directory is this test's own t.TempDir.
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return raw
}

// remove deletes an asset, so the installer meets a release that does not
// carry it.
func (r *release) remove(t *testing.T, name string) {
	t.Helper()

	if err := os.Remove(filepath.Join(r.dir, name)); err != nil {
		t.Fatalf("remove %s: %v", name, err)
	}
}

// sign replaces SHA256SUMS.sig with a signature by key — the published one, or
// one this installer has never heard of.
func (r *release) sign(t *testing.T, key ed25519.PrivateKey) {
	t.Helper()

	r.write(t, "SHA256SUMS.sig", ed25519.Sign(key, r.read(t, "SHA256SUMS")))
}

// signedBy returns install.sh with r's public key committed to it, which is
// what the operator does by hand once — see the key block in the script.
func (r *release) signedBy(t *testing.T, script string) string {
	t.Helper()

	if !strings.Contains(script, keyBlockOpen) {
		t.Fatalf("%s no longer opens its key list with %q, so this test cannot commit a key to it", installerPath, keyBlockOpen)
	}
	line := base64.StdEncoding.EncodeToString(r.pub)
	return strings.Replace(script, keyBlockOpen, keyBlockOpen+line+"\n", 1)
}

// TestInstallVerifiesBeforeExecutable is FR-013, run rather than read.
//
// Every refusal below has the same two obligations: leave with a non-zero
// status, and leave nothing that could be run. The second is the one that is
// easy to lose — an installer that unpacks and then checks has already written
// an executable file, and undoing that is not the same as never having done it.
func TestInstallVerifiesBeforeExecutable(t *testing.T) {
	t.Parallel()

	const version, arch = "v0.42", "amd64"

	// The DER header install.sh prepends to a raw key, checked against the one
	// the standard library emits. It is a constant in a printf and nothing
	// about a wrong one is visible except that every signature fails to verify,
	// which reads exactly like a bad key.
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate a key: %v", err)
	}
	spki, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		t.Fatalf("marshal a public key: %v", err)
	}
	prefix := base64.StdEncoding.EncodeToString(spki[:len(spki)-ed25519.PublicKeySize])
	if script := readInstaller(t); !strings.Contains(script, prefix) {
		t.Errorf("%s does not wrap a raw key with %q.\nThat is the SubjectPublicKeyInfo header for ed25519; with any other prefix openssl reads a different key and every release fails to verify", installerPath, prefix)
	}

	if _, err := exec.LookPath("openssl"); err != nil {
		t.Skipf("openssl is not on PATH, so nothing here can verify a signature: %v", err)
	}

	tests := []struct {
		name string
		// tamper runs after the release is built and before the installer sees
		// it. A nil tamper is the release exactly as published.
		tamper func(t *testing.T, r *release)
		// commit reports whether the installer carries the release's key.
		commit   bool
		installs bool
		says     string
	}{
		{
			name:     "verifies, then unpacks",
			commit:   true,
			installs: true,
		},
		{
			// A byte flipped after the sums were taken: what a tampered
			// download looks like when the signature is genuine.
			name: "refuses a tarball that does not match its checksum",
			tamper: func(t *testing.T, r *release) {
				body := r.read(t, r.tarball)
				body[len(body)/2] ^= 0xff
				r.write(t, r.tarball, body)
			},
			commit: true,
			says:   "checksum",
		},
		{
			// A well-formed signature by a key nobody committed. Every byte
			// arrived intact and it still proves nothing.
			name: "refuses a signature by a key it does not carry",
			tamper: func(t *testing.T, r *release) {
				_, other, err := ed25519.GenerateKey(rand.Reader)
				if err != nil {
					t.Fatalf("generate another key: %v", err)
				}
				r.sign(t, other)
			},
			commit: true,
			says:   "not signed by any key",
		},
		{
			// The one an attacker picks. Serving a tarball and a matching
			// SHA256SUMS is easy; the signature is the file they cannot make,
			// so the cheapest attack is to serve no signature at all and hope
			// absence reads as "nothing to check".
			name:   "refuses a release carrying no signature",
			tamper: func(t *testing.T, r *release) { r.remove(t, "SHA256SUMS.sig") },
			commit: true,
			says:   "SHA256SUMS.sig",
		},
		{
			// The repository's own state until the operator commits a public
			// key (T013). Refusing is the correct answer, and the message has
			// to say which of the two it is.
			name:   "refuses when it carries no key at all",
			commit: false,
			says:   "no release key",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			r := fakeRelease(t, version, arch)
			if tt.tamper != nil {
				tt.tamper(t, r)
			}

			script := readInstaller(t)
			if tt.commit {
				script = r.signedBy(t, script)
			}
			got := runInstaller(t, script, r.dir, version)

			// Whatever else happened, nothing may have been made runnable
			// before both checks passed. tar is in this list because it
			// restores the archive's mode: unpacking is the moment inert bytes
			// become a file the host will execute.
			for _, event := range []string{"tar", "chmod", "install"} {
				at := got.index(event)
				if at < 0 {
					continue
				}
				for _, check := range []string{"openssl", "sha256sum"} {
					if verified := got.index(check); verified < 0 || verified > at {
						t.Errorf("the installer ran %s before %s.\nA file that is executable before it is verified has already been written; checking afterwards is a different thing from checking first.\nWhat it did: %v", event, check, got.events)
					}
				}
			}

			if tt.installs {
				if got.err != nil {
					t.Fatalf("the installer refused a release it published itself: %v\nstdout:\n%sstderr:\n%s", got.err, got.stdout, got.stderr)
				}
				if !got.ran("tar") {
					t.Errorf("the installer verified %s and never unpacked it.\nWhat it did: %v", r.tarball, got.events)
				}
				return
			}

			if got.err == nil {
				t.Fatalf("the installer accepted this release.\nstdout:\n%sstderr:\n%s", got.stdout, got.stderr)
			}
			for _, event := range []string{"tar", "chmod", "install"} {
				if got.ran(event) {
					t.Errorf("the installer refused the release and ran %s anyway.\nA refusal that leaves an executable behind is not a refusal.\nWhat it did: %v", event, got.events)
				}
			}
			if !strings.Contains(got.stderr, tt.says) {
				t.Errorf("the installer refused without saying %q:\n%s\nThe operator has to be able to tell a tampered download from a release this installer has no key for", tt.says, got.stderr)
			}
		})
	}
}

// TestInstallRefusesUnknownPlatform is FR-014. Refusing is half of it; naming
// what is published is the other half, because the person reading it is on a
// machine that cannot run this and needs to know that is the reason.
func TestInstallRefusesUnknownPlatform(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		os      string
		machine string
	}{
		{name: "another operating system", os: "Darwin", machine: "arm64"},
		{name: "a 32-bit host", os: "Linux", machine: "i686"},
		{name: "an architecture nobody builds for", os: "Linux", machine: "riscv64"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			dir := t.TempDir()
			log := filepath.Join(dir, "events")

			cmd := exec.Command("bash", "-c", stubs+"\n"+readInstaller(t)) //nolint:gosec // G204: the script is this repository's own committed installer.
			cmd.Dir = dir
			cmd.Env = append(os.Environ(),
				"STUB_LOG="+log,
				"STUB_OS="+tt.os,
				"STUB_MACHINE="+tt.machine,
				"STUB_RELEASE="+dir,
				"STUB_RESOLVED=https://github.com/example/example/releases/tag/v0.42",
				"TMPDIR="+dir,
				"HOME="+dir,
			)
			var stdout, stderr bytes.Buffer
			cmd.Stdout, cmd.Stderr = &stdout, &stderr

			if err := cmd.Run(); err == nil {
				t.Fatalf("the installer carried on for %s/%s.\nThe amd64 build on an arm host is a file that cannot exec, and systemd is where that gets discovered", tt.os, tt.machine)
			}
			// The log exists only if a stubbed command ran, and the first one
			// this reaches is curl. Refusing after downloading is refusing
			// late: the platform is known before anything is asked for.
			if raw, err := os.ReadFile(log); err == nil { //nolint:gosec // G304: log is this test's own t.TempDir.
				t.Errorf("the installer reached the network before deciding it had nothing to download:\n%s", raw)
			}
			for _, want := range strings.Fields("linux/amd64 linux/arm64") {
				if !strings.Contains(stderr.String(), want) {
					t.Errorf("the installer refused %s/%s without naming %s as published:\n%s\nWhoever reads this is on a machine that cannot run it and needs to know which ones can", tt.os, tt.machine, want, stderr.String())
				}
			}
		})
	}
}

// TestInstallerCarriesTheCommittedKeys keeps the two copies of the key list
// together. install.sh cannot read internal/updater/release_key.txt — it is
// fetched on its own and there is no checkout to read from — so the lines are
// written twice, and a rotation that reaches only one of them leaves the
// installer and the daemon disagreeing about which releases are genuine.
func TestInstallerCarriesTheCommittedKeys(t *testing.T) {
	t.Parallel()

	const committedKeys = repoRoot + "/internal/updater/release_key.txt"

	raw, err := os.ReadFile(committedKeys)
	if err != nil {
		t.Skipf("%s does not exist yet; T013 creates it, empty, and the operator fills both copies at once", committedKeys)
	}

	block := findIn(t, installerPath, readInstaller(t), "the release key list",
		regexp.MustCompile("(?ms)"+regexp.QuoteMeta(keyBlockOpen)+"(.*?)^RELEASE_KEYS$"))

	keys := func(text string) []string {
		var lines []string
		for _, line := range strings.Split(text, "\n") {
			line = strings.TrimSpace(line)
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			lines = append(lines, line)
		}
		return lines
	}

	if got, want := strings.Join(keys(block), "\n"), strings.Join(keys(string(raw)), "\n"); got != want {
		t.Errorf("%s accepts releases signed by:\n%s\n%s accepts:\n%s\nA key in one and not the other is a release the daemon will install and the installer will refuse, or the other way round", installerPath, got, committedKeys, want)
	}
}
