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
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/nctiggy/claude-remote-session-webhook/internal/config"
	"github.com/nctiggy/claude-remote-session-webhook/internal/updater"
)

const (
	installerPath = repoRoot + "/install.sh"
	modulePath    = repoRoot + "/go.mod"

	// keyBlockOpen begins the heredoc install.sh reads its accepted release
	// keys from. It is empty in the repository and stays that way until the
	// operator commits a public key (T013), so the tests below that need a
	// signature to verify put an ephemeral one here first.
	keyBlockOpen = "cat <<'RELEASE_KEYS'\n"
	// keyBlockClose is the heredoc terminator, so a case can strip the block
	// rather than depend on the repository having left it empty.
	keyBlockClose = "RELEASE_KEYS\n"
)

// tarballName is Go's spelling of the release asset name — the third of the
// three contracts/release.md fixes, beside the YAML's and install.sh's.
//
// It reads the shipped one rather than holding a copy. T018 gave the updater a
// real reason to build this string in production code, and a second spelling
// here would be a test that agreed with itself while the daemon asked for
// something else.
func tarballName(version, arch string) string {
	return updater.AssetName(version, arch)
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

# Answers rather than wrapping, and the one stub whose job is to never be
# reached: FR-018 is that the installer enables and starts nothing, and a
# systemctl that succeeded quietly is exactly how that requirement gets lost.
systemctl() { _log "systemctl $*"; }
`

// run is one execution of install.sh under those stubs.
type run struct {
	stdout string
	stderr string
	events []string
	// home is the directory the run was given as $HOME. Everything the
	// installer places is under it, and it belongs to the test rather than to
	// whoever is running the suite.
	home string
	err  error
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
// release directory the caller built, on a host nothing has touched.
//
// seed runs against the home directory before the installer does, for the cases
// whose subject is a host that already has something on it.
func runInstaller(t *testing.T, script, releaseDir, version string, seed ...func(t *testing.T, home string)) run {
	t.Helper()

	return runInstallerIn(t, t.TempDir(), script, releaseDir, version, seed...)
}

// runInstallerIn is the same run against a home the caller names, which is how
// the same installer is run twice against what the first run left behind. The
// event log is deliberately not under that home: two runs sharing one would
// read as one run that did everything twice.
func runInstallerIn(t *testing.T, dir, script, releaseDir, version string, seed ...func(t *testing.T, home string)) run {
	t.Helper()

	log := filepath.Join(t.TempDir(), "events")

	for _, prepare := range seed {
		prepare(t, dir)
	}

	// $HOME is this test's, and so is everything the installer writes under it.
	// The XDG variables are dropped rather than passed through: the daemon
	// reads $XDG_CONFIG_HOME ahead of ~/.config, and on the day the installer
	// is taught the same rule an inherited one would send these writes into the
	// home of whoever is running the suite.
	env := make([]string, 0, len(os.Environ()))
	for _, pair := range os.Environ() {
		if strings.HasPrefix(pair, "XDG_") {
			continue
		}
		env = append(env, pair)
	}

	cmd := exec.Command("bash", "-c", stubs+"\n"+script) //nolint:gosec // G204: the script is this repository's own committed installer.
	cmd.Dir = dir
	cmd.Env = append(env,
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
	return run{stdout: stdout.String(), stderr: stderr.String(), events: events, home: dir, err: err}
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

	// The deployment files, written as well as summed, because a published
	// release carries them and the installer downloads one of them: the unit,
	// which it verifies and then writes into ~/.config/systemd/user. Their
	// names come from `deployed` rather than being typed here, so a rename in
	// the release workflow surfaces as an installer asking for a name nothing
	// publishes — which is the 404 whoever is installing would otherwise be the
	// first to see.
	//
	// The two it never fetches still matter: SHA256SUMS covers every asset, so
	// they are the names with no file beside them in the download directory,
	// which is what an installer running `sha256sum -c SHA256SUMS` gets wrong
	// against a release that is entirely correct.
	for name := range deployed {
		r.write(t, name, []byte("the bytes of "+name+"\n"))
	}
	r.republish(t)
	return r
}

// republish sums whatever the release directory now holds and signs the result
// — what the workflow does at the end of every run, and what makes an asset
// changed by a test a release rather than a tampered one.
//
// The names are taken from the directory rather than from a list, so an asset a
// test adds is covered exactly as the workflow's `find`-driven step would cover
// it. Anything called SHA256SUMS* is skipped: a signature is made from the sums
// file and so cannot be inside it.
func (r *release) republish(t *testing.T) {
	t.Helper()

	entries, err := os.ReadDir(r.dir)
	if err != nil {
		t.Fatalf("read the release directory: %v", err)
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || strings.HasPrefix(entry.Name(), "SHA256SUMS") {
			continue
		}
		names = append(names, entry.Name())
	}
	sort.Strings(names)

	var lines strings.Builder
	for _, name := range names {
		fmt.Fprintf(&lines, "%x  %s\n", sha256.Sum256(r.read(t, name)), name)
	}
	r.write(t, "SHA256SUMS", []byte(lines.String()))
	r.sign(t, r.priv)
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

// withoutKeys strips every committed key from the installer, so a case about
// "carrying no key" measures that rather than whatever the repository happens
// to hold today.
//
// It exists because the test that needed it broke the moment a real key was
// committed: it had been asserting the empty-block message while reading the
// real install.sh, so its precondition was the state of the repository rather
// than something the case set up. A test that inherits its premise passes for a
// reason it does not state, and fails on a change that was not a regression.
func withoutKeys(t *testing.T, script string) string {
	t.Helper()

	open := strings.Index(script, keyBlockOpen)
	if open < 0 {
		t.Fatalf("%s no longer opens its key list with %q", installerPath, keyBlockOpen)
	}
	rest := open + len(keyBlockOpen)
	end := strings.Index(script[rest:], keyBlockClose)
	if end < 0 {
		t.Fatalf("%s no longer closes its key list with %q", installerPath, keyBlockClose)
	}
	return script[:rest] + script[rest+end:]
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
			// The other file the installer downloads, and the one it is
			// tempting to take on trust because it is "just configuration".
			// It is not: it is what systemd reads to decide what to execute,
			// where, and with which environment, and it is written into
			// ~/.config/systemd/user on a host that is about to enable it.
			name: "refuses a unit that does not match its checksum",
			tamper: func(t *testing.T, r *release) {
				r.write(t, "crswd.service", []byte("[Service]\nExecStart=/bin/false\n"))
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
			} else {
				// Strip whatever the repository carries, so this case is about
				// an installer with no key rather than about an installer whose
				// key does not match — two different refusals with two
				// different messages.
				script = withoutKeys(t, script)
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

// needsOpenSSL skips a case that cannot run without it. Every test that gets as
// far as placing a file has to get past verify_signature first, and there is no
// standing in for openssl there: a stub would be the test agreeing with itself.
func needsOpenSSL(t *testing.T) {
	t.Helper()

	if _, err := exec.LookPath("openssl"); err != nil {
		t.Skipf("openssl is not on PATH, so no release can be verified here: %v", err)
	}
}

// installs runs the whole installer against a release it published itself, and
// fails if it refused one. Every case below starts here: what they are about is
// what is on the host afterwards.
func installs(t *testing.T, seed ...func(t *testing.T, home string)) run {
	t.Helper()

	r := fakeRelease(t, "v0.42", "amd64")
	got := runInstaller(t, r.signedBy(t, readInstaller(t)), r.dir, r.version, seed...)
	if got.err != nil {
		t.Fatalf("the installer refused a release it published itself: %v\nstdout:\n%sstderr:\n%s", got.err, got.stdout, got.stderr)
	}
	return got
}

// placed reads back a file the installer wrote, with the mode it wrote it at.
func placed(t *testing.T, got run, rel string) ([]byte, fs.FileMode) {
	t.Helper()

	path := filepath.Join(got.home, rel)
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("~/%s: %v\nThe installer verified a release and then left that behind.\nWhat it said:\n%s", rel, err, got.stdout)
	}
	body, err := os.ReadFile(path) //nolint:gosec // G304: the home is this test's own t.TempDir.
	if err != nil {
		t.Fatalf("read ~/%s: %v", rel, err)
	}
	return body, info.Mode().Perm()
}

// TestInstallPlacesWhatItDownloaded is steps 4 to 6 of contracts/installer.md,
// and it reads the host rather than the transcript: an installer that prints
// "installed" and wrote nothing says exactly what a working one says.
//
// The record is the part with no symptom of its own. It is what T011 compares
// an installed unit against, and a run that placed a unit and recorded nothing
// leaves the next run unable to tell "we wrote this" from "somebody else did" —
// at which point the only safe answer is to refuse, forever, on a host this
// installer set up itself.
func TestInstallPlacesWhatItDownloaded(t *testing.T) {
	t.Parallel()
	needsOpenSSL(t)

	got := installs(t)

	// The unit, byte for byte what the release published. The bytes matter as
	// much as the path: this is the file systemd reads to decide what to run.
	unit, mode := placed(t, got, ".config/systemd/user/crswd.service")
	if want := "the bytes of crswd.service\n"; string(unit) != want {
		t.Errorf("~/.config/systemd/user/crswd.service holds %q, not the published unit %q", unit, want)
	}
	if mode != 0o644 {
		t.Errorf("the installed unit is mode %04o, want 0644", mode)
	}

	body, mode := placed(t, got, ".local/bin/crswd")
	if !strings.Contains(string(body), "crswd v0.42") {
		t.Errorf("~/.local/bin/crswd holds %q, which is not what the tarball carried", body)
	}
	if mode&0o111 == 0 {
		t.Errorf("~/.local/bin/crswd is mode %04o, which nothing can execute.\nsystemd reports that as 203/EXEC, long after the install said it worked", mode)
	}

	record, _ := placed(t, got, ".local/share/crswd/crswd.service.sha256")
	if want := fmt.Sprintf("%x", sha256.Sum256(unit)); strings.TrimSpace(string(record)) != want {
		t.Errorf("the recorded unit hash is %q; the unit it just wrote hashes to %s.\nT011 compares those two to tell an edited unit from one this installer wrote, and a record of the wrong bytes means every later run reads its own unit as edited", strings.TrimSpace(string(record)), want)
	}
}

// TestConfigModeIs0600 is FR-017. The file is written to hold the shared
// secret, and a mode inherited from the umask is 0644 on a stock host: a
// credential for a daemon that runs unsandboxed code, readable by every account
// on the machine, written that way by the install step itself.
func TestConfigModeIs0600(t *testing.T) {
	t.Parallel()
	needsOpenSSL(t)

	const config = ".config/crswd/config"

	t.Run("written 0600 on a host that has none", func(t *testing.T) {
		t.Parallel()

		// A stock umask, set here rather than inherited, so what this asserts
		// does not depend on the shell the suite was started from. Under 0022 a
		// file created without care is 0644, which is the failure itself.
		r := fakeRelease(t, "v0.42", "amd64")
		got := runInstaller(t, "umask 0022\n"+r.signedBy(t, readInstaller(t)), r.dir, r.version)
		if got.err != nil {
			t.Fatalf("the installer refused a release it published itself: %v\nstderr:\n%s", got.err, got.stderr)
		}

		body, mode := placed(t, got, config)
		if mode != 0o600 {
			t.Errorf("~/%s is mode %04o, want 0600.\nThat file is where the shared secret goes, and 0644 is what a `cat >` inherits from a stock umask", config, mode)
		}

		// Every other setting arrives commented out, which is what makes this
		// file behave as no file at all in each of them until the operator edits
		// it. Three exceptions, and each is one for a stated reason: the schema
		// version, the secret because no default could stand in for a credential
		// (TestInstallGeneratesASharedSecret), and the allowlist because its
		// default only applies where the directory already exists
		// (TestInstallSetsTheContainmentRoot).
		//
		// The key is named in the failure and the value never is. A line that
		// should not be here is diagnosed by which setting it is, and a test
		// that printed the whole line would print the secret on the day this
		// list is wrong.
		set := map[string]bool{"version": true, "shared_secret": true, "allowed_roots": true}
		for _, line := range strings.Split(string(body), "\n") {
			line = strings.TrimSpace(line)
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			key, _, ok := strings.Cut(line, "=")
			if key = strings.TrimSpace(key); ok && set[key] {
				continue
			}
			t.Errorf("the installed configuration sets %q.\nEverything with a default has to arrive commented out: a value here is one the operator did not choose, on a file they are about to be told is theirs", key)
		}
	})

	t.Run("a configuration already there is not touched at all", func(t *testing.T) {
		t.Parallel()

		// 0644 and holding no secret, which is a file the daemon accepts: the
		// refusal fires on contents, not on the name. So this is not a mode the
		// installer may quietly correct — it is somebody's file.
		const theirs = "# mine\nmax_sessions = 2\n"
		got := installs(t, func(t *testing.T, home string) {
			t.Helper()

			dir := filepath.Join(home, ".config", "crswd")
			if err := os.MkdirAll(dir, 0o750); err != nil {
				t.Fatalf("make %s: %v", dir, err)
			}
			if err := os.WriteFile(filepath.Join(dir, "config"), []byte(theirs), 0o644); err != nil { //nolint:gosec // G306: the mode is the subject of this case.
				t.Fatalf("write the operator's configuration: %v", err)
			}
		})

		body, mode := placed(t, got, config)
		if string(body) != theirs {
			t.Errorf("the installer rewrote a configuration that was already there:\n%s\nIt is the one file on this host the operator authored, and this is an operation they think of as safe", body)
		}
		if mode != 0o644 {
			t.Errorf("the installer changed the mode of an existing configuration to %04o.\nLeaving it alone means the mode too — that file holds no secret, so 0644 is a mode the daemon accepts and not one to correct on somebody's behalf", mode)
		}
	})
}

// installedValue is one setting out of a configuration the installer wrote,
// read by the daemon's own parser rather than by a second one written here.
//
// Which is the claim worth making. The installer writes that file for the daemon
// to read, and a parser living in this package could agree with the installer
// about a file the daemon refuses — the operator would meet that as a service
// that will not come up, holding a file every test here called correct.
//
// Callers say what an absent setting costs, because it is a different sentence
// for each of them, but none may treat "" as an answer: every one compares what
// it gets back against something, and an empty string compares equal to another
// empty string.
func installedValue(t *testing.T, body []byte, key string) (string, bool) {
	t.Helper()

	// Warnings to io.Discard rather than nil, which means os.Stderr: nothing
	// here is about a renamed key, and the writer is not optional.
	f, err := config.ParseFile(installedConfig, body, io.Discard)
	if err != nil {
		t.Fatalf("the daemon's own parser refuses the configuration the installer wrote: %v\nThat file exists for this parser to read, and one it refuses is a host where the service does not start", err)
	}
	return f.Lookup(key)
}

// installedSecret is the shared secret, and it fails rather than returning ""
// for a file that sets none.
func installedSecret(t *testing.T, body []byte) string {
	t.Helper()

	secret, ok := installedValue(t, body, config.EnvSharedSecret)
	if !ok {
		t.Fatal("the installed configuration sets no shared_secret.\nThe daemon refuses to start without one, so the install stopped one hand-edit short of a host that works — and the hand that fills in a required credential is the hand that types something it can remember")
	}
	return secret
}

// TestInstallGeneratesASharedSecret is T001. The daemon refuses to start without
// a secret, so an installer that wrote the line commented out and told the
// operator to fill it in stopped one hand-edit short of a host that works — and
// the hand that fills in a required credential is the hand that types something
// it can remember.
//
// Three claims, and the second is the one with no symptom of its own: an
// installer that generates a perfectly good secret and prints it has put a
// credential into a terminal scrollback, a CI log, and the far end of a pipe
// from curl, and every other thing about the install still looks right.
//
// No failure below names a value. What is under test is a secret, and a test
// that printed it on the way to reporting a leak would be the leak.
func TestInstallGeneratesASharedSecret(t *testing.T) {
	t.Parallel()
	needsOpenSSL(t)

	t.Run("long enough for the daemon to accept it", func(t *testing.T) {
		t.Parallel()

		got := installs(t)
		body, _ := placed(t, got, installedConfig)

		if secret := installedSecret(t, body); len(secret) < config.MinSecretBytes {
			t.Errorf("the installed configuration sets a shared_secret shorter than the %d bytes config.MinSecretBytes requires.\nThe daemon refuses to start on it, and whoever installed this meets that as a service that will not come up", config.MinSecretBytes)
		}
	})

	t.Run("never printed", func(t *testing.T) {
		t.Parallel()

		got := installs(t)
		body, _ := placed(t, got, installedConfig)
		secret := installedSecret(t, body)

		if strings.Contains(got.stdout+got.stderr, secret) {
			t.Error("the installer printed the shared secret it generated.\nIts output is a terminal scrollback, a CI log, and often the far end of a pipe from curl: printed once is a copy in all three, and rotating it is the only way back. Say that a secret was generated, never which one")
		}
	})

	t.Run("a different one on every host", func(t *testing.T) {
		t.Parallel()

		// Two installs, two homes, and the only thing asserted is that they
		// disagree. A fixture committed to the installer would satisfy every
		// other case here and hand one credential to everybody who ran it.
		first, _ := placed(t, installs(t), installedConfig)
		second, _ := placed(t, installs(t), installedConfig)

		if installedSecret(t, first) == installedSecret(t, second) {
			t.Error("two installs generated the same shared_secret.\nThat is a constant in the installer rather than a secret, and it authenticates every API caller on every host that ever ran it")
		}
	})

	t.Run("a second run leaves the generated one alone", func(t *testing.T) {
		t.Parallel()

		// Read between the runs. They share a home, so a value read afterwards
		// would be compared against itself and pass against an installer that
		// rewrote the file.
		var afterFirst string

		_, second := twice(t, func(t *testing.T, home string, _ *release) {
			t.Helper()

			raw, err := os.ReadFile(filepath.Join(home, installedConfig)) //nolint:gosec // G304: the home is this test's own t.TempDir.
			if err != nil {
				t.Fatalf("read the configuration the first run wrote: %v", err)
			}
			afterFirst = installedSecret(t, raw)
		})

		body, _ := placed(t, second, installedConfig)
		if installedSecret(t, body) != afterFirst {
			t.Error("the second run replaced the generated shared secret.\nRe-running the one-liner is how a host takes a newer binary, and every client signing with the old secret stops being able to reach the daemon the moment it does")
		}
	})
}

// TestInstallSetsTheContainmentRoot is T002. allowed_roots is the whole of what
// bounds a session running with the permission prompt turned off, and the
// installer used to leave it commented out over a note naming the default.
//
// That default is only a default where the directory already exists: every
// entry has to resolve at startup, so on a host with no ~/code the daemon
// refuses to boot rather than falling back to the built-in root. The two halves
// below are one requirement — a configuration naming a directory nobody created
// is a service that will not come up, and a directory nobody named is a
// boundary the operator cannot read off the file they were told is theirs.
func TestInstallSetsTheContainmentRoot(t *testing.T) {
	t.Parallel()
	needsOpenSSL(t)

	t.Run("named in the configuration, and there on the disk", func(t *testing.T) {
		t.Parallel()

		got := installs(t)
		body, _ := placed(t, got, installedConfig)

		roots, ok := installedValue(t, body, config.EnvAllowedRoots)
		if !ok {
			t.Fatal("the installed configuration sets no allowed_roots.\nThe daemon then uses its built-in default, which resolves only where that directory happens to exist — and the operator reads a file that says nothing about what a session can reach")
		}

		// $HOME itself, spelled out because it is the failure with no symptom:
		// the daemon starts, every session is contained, and the allowlist holds
		// the SSH keys, the cloud credentials and the browser profiles that live
		// directly under a home directory.
		if roots == got.home {
			t.Fatalf("the installed configuration allows sessions to run anywhere under %s.\nThat is the whole home directory: an allowlist that contains a private key is not a containment boundary", roots)
		}
		if want := filepath.Join(got.home, config.DefaultRootName); roots != want {
			t.Errorf("the installed configuration sets allowed_roots to %q; the default this file and the daemon both name is %q.\nThe two have to agree: the systemd unit sets CRSW_ALLOWED_ROOTS to the daemon's default and the environment beats the file, so a third answer here is one the operator reads and the daemon never uses", roots, want)
		}

		// Stat what the file names rather than what this test expected it to
		// name. It is the path the daemon resolves at startup, and the one it
		// refuses to start on.
		info, err := os.Stat(roots)
		if err != nil {
			t.Fatalf("the configuration names %s and the installer did not create it: %v\nEvery allowed_roots entry must resolve at startup, so this host has a daemon that refuses to boot and a configuration that looks complete", roots, err)
		}
		if !info.IsDir() {
			t.Errorf("%s is not a directory (mode %v).\nThe daemon refuses to start on an allowed_roots entry that is not one", roots, info.Mode())
		}
	})

	t.Run("a host that already has a configuration gets no directory either", func(t *testing.T) {
		t.Parallel()

		// The other half of "an existing configuration is never touched". That
		// file may point containment somewhere else entirely, and a directory
		// created to satisfy a default it does not use is this installer making
		// a decision on a host it was told to leave alone.
		got := installs(t, func(t *testing.T, home string) {
			t.Helper()

			dir := filepath.Join(home, ".config", "crswd")
			if err := os.MkdirAll(dir, 0o750); err != nil {
				t.Fatalf("make %s: %v", dir, err)
			}
			if err := os.WriteFile(filepath.Join(dir, "config"), []byte("version = 1\nallowed_roots = /tmp\n"), 0o600); err != nil {
				t.Fatalf("write the operator's configuration: %v", err)
			}
		})

		if _, err := os.Stat(filepath.Join(got.home, config.DefaultRootName)); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("the installer created ~/%s on a host whose configuration it left alone (%v).\nThat file already says where sessions may run, and it is not here", config.DefaultRootName, err)
		}
	})
}

// TestInstallPrintsNextSteps is FR-018 and FR-019 together, because they are the
// same step: the installer stops, and the only reason that is not an unfinished
// job is that it says what is left.
//
// It cannot enable the unit — the daemon refuses to start without the secret, so
// a service enabled here fails on first boot, and an operator who has watched
// this service fail once reads the next failure as normal.
func TestInstallPrintsNextSteps(t *testing.T) {
	t.Parallel()
	needsOpenSSL(t)

	got := installs(t)

	for _, want := range []string{
		// The two settings with no default that will do, named as the
		// configuration file spells them rather than as prose.
		"shared_secret",
		"allowed_roots",
		// Where to set them, and the command that is deliberately not run.
		"~/.config/crswd/config",
		"systemctl --user enable --now crswd",
	} {
		if !strings.Contains(got.stdout, want) {
			t.Errorf("the installer never says %q:\n%s\nIt has just stopped one command short of a working daemon; whoever ran it has to be told which one", want, got.stdout)
		}
	}

	if got.ran("systemctl") {
		t.Errorf("the installer called systemctl:\n%v\nIt cannot start: the secret is not set yet, so the service it enabled would fail on first boot and teach its operator to ignore a failing service", got.events)
	}
}

// Where the installer puts the two files it must never clobber, and the record
// that decides one of those two questions. Spelled once here because every case
// below is about one of those paths being compared against that record.
const (
	installedUnit   = ".config/systemd/user/crswd.service"
	installedConfig = ".config/crswd/config"
	unitRecord      = ".local/share/crswd/crswd.service.sha256"
)

// twice is the installer run against the same host twice, which is the only way
// the questions below can be asked at all: what the second run does is decided
// entirely by what the first one left behind.
//
// between runs on that host after the first install and before the second — an
// operator editing what the next steps told them to edit, or a later release
// arriving. seed runs before the first, for a host that already had something on
// it before this installer had ever been near it.
//
// Both runs are required to succeed. Meeting a host that already has crswd on it
// is not an error: it is what an operator does to take a newer binary, and what
// anybody does who is not sure the first run finished.
func twice(t *testing.T, between func(t *testing.T, home string, r *release), seed ...func(t *testing.T, home string)) (first, second run) {
	t.Helper()

	r := fakeRelease(t, "v0.42", "amd64")
	script := r.signedBy(t, readInstaller(t))
	home := t.TempDir()

	first = runInstallerIn(t, home, script, r.dir, r.version, seed...)
	if first.err != nil {
		t.Fatalf("the installer refused a release it published itself: %v\nstdout:\n%sstderr:\n%s", first.err, first.stdout, first.stderr)
	}
	if between != nil {
		between(t, home, r)
	}
	second = runInstallerIn(t, home, script, r.dir, r.version)
	if second.err != nil {
		t.Fatalf("the second run failed on the host the first one set up: %v\nstdout:\n%sstderr:\n%s", second.err, second.stdout, second.stderr)
	}
	return first, second
}

// TestInstallNeverOverwritesConfig asks FR-016 the only way it can be asked:
// install, edit the file the installer has just told the operator to edit, then
// install again. That file is the one thing on this host they authored, and the
// command that would destroy it is one they think of as safe.
func TestInstallNeverOverwritesConfig(t *testing.T) {
	t.Parallel()
	needsOpenSSL(t)

	// Deliberately not a shared secret: what is asserted is that the bytes come
	// back unchanged, and a credential-shaped fixture is a credential in the
	// repository. max_sessions is a setting with a default, so this is a file
	// the daemon would accept.
	const theirs = "version = 1\nmax_sessions = 2\nallowed_roots = /tmp/crswd-roots\n"

	first, second := twice(t, func(t *testing.T, home string, _ *release) {
		t.Helper()

		if err := os.WriteFile(filepath.Join(home, installedConfig), []byte(theirs), 0o600); err != nil {
			t.Fatalf("edit the configuration the first run wrote: %v", err)
		}
	})

	if want := "wrote ~/" + installedConfig; !strings.Contains(first.stdout, want) {
		t.Fatalf("the first run never wrote a configuration, so the second had nothing to leave alone:\n%s", first.stdout)
	}

	body, mode := placed(t, second, installedConfig)
	if string(body) != theirs {
		t.Errorf("the second run rewrote the configuration.\ngot:\n%s\nwant:\n%s\nEvery setting the operator was told to make lives in that file, and this is the command they were told to run", body, theirs)
	}
	if mode != 0o600 {
		t.Errorf("the configuration is mode %04o after a second run, want 0600", mode)
	}
	if want := "~/" + installedConfig + " exists"; !strings.Contains(second.stdout, want) {
		t.Errorf("the second run never said %q:\n%s\nA file quietly left alone reads exactly like a file quietly overwritten", want, second.stdout)
	}
}

// TestInstallNeverOverwritesEditedUnit is the first two rows of the table in
// contracts/installer.md, and the pair is the whole point: the comparison has to
// be against the hash this installer recorded, never against the unit the
// release ships.
//
// Both readings leave an edited unit alone, so the edited case cannot tell them
// apart. Only the recorded one still delivers a *changed* unit to a host that
// never touched the old one — which is every host that ever takes an update, and
// the reason T008's Restart=always could otherwise never reach one.
func TestInstallNeverOverwritesEditedUnit(t *testing.T) {
	t.Parallel()
	needsOpenSSL(t)

	t.Run("an edited unit survives, and so does the record of the one we wrote", func(t *testing.T) {
		t.Parallel()

		// The published bytes plus a line: an edit, rather than a different
		// file, because that is what an operator does to a unit that works.
		const edited = "the bytes of crswd.service\nEnvironment=CRSW_MAX_SESSIONS=1\n"

		// Read between the two runs, and it has to be: they share a home, so a
		// read taken afterwards would be the same bytes compared with themselves
		// — which is a comparison that passes against an installer that rewrote
		// the record here, and this test's whole second half.
		var recorded []byte

		_, second := twice(t, func(t *testing.T, home string, _ *release) {
			t.Helper()

			raw, err := os.ReadFile(filepath.Join(home, unitRecord)) //nolint:gosec // G304: the home is this test's own t.TempDir.
			if err != nil {
				t.Fatalf("read the record the first run wrote: %v", err)
			}
			recorded = raw

			if err := os.WriteFile(filepath.Join(home, installedUnit), []byte(edited), 0o644); err != nil { //nolint:gosec // G306: 0644 is the mode the installer writes a unit at, and this is standing in for it.
				t.Fatalf("edit the unit the first run wrote: %v", err)
			}
		})

		body, _ := placed(t, second, installedUnit)
		if string(body) != edited {
			t.Errorf("the second run replaced an edited unit.\ngot:\n%s\nwant:\n%s\nThat file is what systemd reads to decide what to execute and with which environment; whatever was changed in it was changed for a reason this installer cannot see", body, edited)
		}
		if want := "has been modified"; !strings.Contains(second.stdout, want) {
			t.Errorf("the second run left an edited unit alone without saying so:\n%s\nIt has to say %q, because the operator is about to restart a daemon that is not running the unit they think they just installed", second.stdout, want)
		}

		// The record still describes the unit we wrote. Recording the operator's
		// bytes here would make the *next* run find a record that matches and
		// replace them without a word — a refusal that lasts exactly one command.
		after, _ := placed(t, second, unitRecord)
		if !bytes.Equal(recorded, after) {
			t.Errorf("the second run rewrote the record for a unit it did not write: %q, was %q.\nThe run after it finds a record that matches the operator's unit and replaces it without a word", after, recorded)
		}
	})

	t.Run("a unit we wrote is replaced when the release publishes a new one", func(t *testing.T) {
		t.Parallel()

		const newUnit = "[Service]\nExecStart=%h/.local/bin/crswd\nRestart=always\n"

		_, second := twice(t, func(t *testing.T, _ string, r *release) {
			t.Helper()

			r.write(t, "crswd.service", []byte(newUnit))
			r.republish(t)
		})

		body, _ := placed(t, second, installedUnit)
		if string(body) != newUnit {
			t.Errorf("the installer kept the old unit: %q.\nNobody had touched it — it hashed to exactly what the previous run recorded — so this is a comparison against the copy the release ships rather than against what this installer wrote, and no host would ever receive a corrected unit", body)
		}
		record, _ := placed(t, second, unitRecord)
		if want := fmt.Sprintf("%x", sha256.Sum256([]byte(newUnit))); strings.TrimSpace(string(record)) != want {
			t.Errorf("the record says %q for a unit that hashes to %s.\nEvery later run compares against that record, and one holding the wrong bytes makes this installer read its own unit as edited from here on", strings.TrimSpace(string(record)), want)
		}
	})
}

// TestInstallLeavesNoRecordAlone is the third row, and it is not an edge case.
// This daemon has been deployed by writing the unit by hand, so every host
// running it today has a unit and no record of one — including the host that
// publishes these releases. Absence of evidence that we wrote a file is not
// permission to replace it.
func TestInstallLeavesNoRecordAlone(t *testing.T) {
	t.Parallel()
	needsOpenSSL(t)

	// A unit that predates this installer: nothing like the published bytes, and
	// pointing somewhere the published one does not.
	const theirs = "[Unit]\nDescription=crswd, written by hand\n\n[Service]\nExecStart=%h/bin/crswd\n"

	first, second := twice(t, nil, func(t *testing.T, home string) {
		t.Helper()

		dir := filepath.Join(home, filepath.Dir(installedUnit))
		if err := os.MkdirAll(dir, 0o750); err != nil {
			t.Fatalf("make %s: %v", dir, err)
		}
		if err := os.WriteFile(filepath.Join(dir, filepath.Base(installedUnit)), []byte(theirs), 0o600); err != nil {
			t.Fatalf("write the unit this host already had: %v", err)
		}
	})

	// Twice, because the failure this guards is not "it overwrote a stranger's
	// unit" so much as "it recorded one, and then overwrote it on the next run".
	for i, got := range []run{first, second} {
		if want := "was not written by this installer"; !strings.Contains(got.stdout, want) {
			t.Errorf("run %d left a unit it has no record of alone without saying so:\n%s\nIt has to say %q: the operator is running a unit this installer did not write and will not update", i+1, got.stdout, want)
		}
	}

	body, _ := placed(t, second, installedUnit)
	if string(body) != theirs {
		t.Fatalf("the installer replaced a unit it has no record of writing:\n%s\nIt cannot tell one written by hand from one it wrote itself, and the only safe answer to that is the one that leaves the file", body)
	}
	if _, err := os.Stat(filepath.Join(second.home, unitRecord)); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("the installer recorded a hash for a unit it did not write (%v).\nThe run after that finds a record that matches and replaces the operator's unit without a word", err)
	}

	// Refusing to touch one file is not a reason to leave the host without a
	// binary: the rest of the install still has to have happened.
	if _, mode := placed(t, second, ".local/bin/crswd"); mode&0o111 == 0 {
		t.Errorf("~/.local/bin/crswd is mode %04o after a run that left the unit alone", mode)
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

// TestKeyListsAgree pins the two places a release key has to live.
//
// The daemon embeds internal/updater/release_key.txt; install.sh carries the
// same lines in its RELEASE_KEYS block, because it is fetched on its own and has
// no checkout to read from. Neither can import the other — one is Go, one is
// shell — so the duplication is unavoidable.
//
// The drift is not, and it is worse than either file being wrong alone. A key in
// the daemon and not the installer is a release an existing host updates to and
// a new host refuses to install; the reverse is a release a new host installs and
// no existing host will take. Both look like the system working until somebody
// compares two machines.
//
// This exists because a human compared them once, by hand, and that is not a
// mechanism.
func TestKeyListsAgree(t *testing.T) {
	t.Parallel()

	keyPath := filepath.Join("..", "updater", "release_key.txt")
	blob, err := os.ReadFile(keyPath) //nolint:gosec // G304 false positive: keyPath is a constant path built from filepath.Join literals, not from input.
	if err != nil {
		t.Fatalf("read %s: %v", keyPath, err)
	}
	embedded := keyLines(t, string(blob))

	script := readInstaller(t)
	open := strings.Index(script, keyBlockOpen)
	if open < 0 {
		t.Fatalf("%s no longer opens its key list with %q", installerPath, keyBlockOpen)
	}
	rest := open + len(keyBlockOpen)
	end := strings.Index(script[rest:], keyBlockClose)
	if end < 0 {
		t.Fatalf("%s no longer closes its key list with %q", installerPath, keyBlockClose)
	}
	carried := keyLines(t, script[rest:rest+end])

	if strings.Join(embedded, "\n") != strings.Join(carried, "\n") {
		t.Errorf("the daemon and the installer trust different keys.\n"+
			"internal/updater/release_key.txt: %v\ninstall.sh RELEASE_KEYS:         %v\n"+
			"A key in one and not the other is a release one of them installs and the other refuses.",
			embedded, carried)
	}
}

// keyLines is the parse both readers agree on: comments and blanks dropped,
// everything else significant and in order.
func keyLines(t *testing.T, blob string) []string {
	t.Helper()

	var out []string
	for _, line := range strings.Split(blob, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		out = append(out, line)
	}
	return out
}

// unitPath is the service file the installer ships and installs.
const unitPath = repoRoot + "/deploy/crswd.example.service"

// TestTheUnitExecsWhatTheInstallerInstalls is the guard for the defect that
// made this daemon unstartable on every machine the installer ever set up.
//
// The unit's ExecStart was `%h/bin/crswd`. The installer writes
// `~/.local/bin/crswd`. Nothing compared them, so systemd answered
// `Failed to spawn 'start' task: No such file or directory` on a fresh host —
// and never here, because this machine's own deployment predates the installer
// and has a binary at the older path.
//
// **Must fail when** the installer's destination and the unit's ExecStart drift
// apart. They are written in two languages in two files and cannot share a
// constant, which is the same shape as the asset-name duplication one file over:
// the duplication is unavoidable, the drift is not.
func TestTheUnitExecsWhatTheInstallerInstalls(t *testing.T) {
	t.Parallel()

	unit, err := os.ReadFile(unitPath)
	if err != nil {
		t.Fatalf("read %s: %v", unitPath, err)
	}
	script, err := os.ReadFile(installerPath)
	if err != nil {
		t.Fatalf("read %s: %v", installerPath, err)
	}

	exec := regexp.MustCompile(`(?m)^ExecStart=%h/(\S+)`).FindSubmatch(unit)
	if exec == nil {
		t.Fatal("the unit has no ExecStart under %h, so nothing here can check where it points")
	}
	dest := regexp.MustCompile(`(?m)^readonly BINARY="([^"]+)"`).FindSubmatch(script)
	if dest == nil {
		t.Fatal("install.sh declares no BINARY, so nothing here can check what it installs")
	}

	if got, want := string(exec[1]), string(dest[1]); got != want {
		t.Errorf("the unit execs %%h/%s and the installer installs ~/%s.\nsystemd answers \"Failed to spawn 'start' task: No such file or directory\" on any host that does not already have a binary at the unit's path, which is every host the installer has ever set up", got, want)
	}
}

// TestTheUnitDoesNotRequireAFileTheInstallerNeverWrites is the other half of the
// same journal entry: `Failed to load environment files: No such file or
// directory`.
//
// The installer writes ~/.config/crswd/config. The unit required
// ~/.config/crswd/env, which nothing creates. A mandatory EnvironmentFile that
// is absent is a unit systemd refuses to start at all.
//
// **Must fail when** an EnvironmentFile is made mandatory again. The leading `-`
// is what keeps a legacy env file working without requiring one.
func TestTheUnitDoesNotRequireAFileTheInstallerNeverWrites(t *testing.T) {
	t.Parallel()

	unit, err := os.ReadFile(unitPath)
	if err != nil {
		t.Fatalf("read %s: %v", unitPath, err)
	}
	for _, m := range regexp.MustCompile(`(?m)^EnvironmentFile=(\S+)`).FindAllSubmatch(unit, -1) {
		if !bytes.HasPrefix(m[1], []byte("-")) {
			t.Errorf("the unit requires the environment file %s and the installer never writes one; systemd refuses to start a unit whose mandatory environment file is absent, which is every fresh install", m[1])
		}
	}
}
