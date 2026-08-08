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
	"io/fs"
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
// release directory the caller built.
//
// seed runs against the home directory before the installer does, for the cases
// whose subject is a host that already has something on it.
func runInstaller(t *testing.T, script, releaseDir, version string, seed ...func(t *testing.T, home string)) run {
	t.Helper()

	dir := t.TempDir()
	log := filepath.Join(dir, "events")

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
	sums := map[string][]byte{r.tarball: gz.Bytes()}
	for name := range deployed {
		sums[name] = []byte("the bytes of " + name + "\n")
		r.write(t, name, sums[name])
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

		// Every setting commented out, which is what docs/security.md §3 says
		// keeps a copy of the example from being a file that holds a secret —
		// and what makes this file behave exactly as no file at all until the
		// operator edits it.
		for _, line := range strings.Split(string(body), "\n") {
			line = strings.TrimSpace(line)
			if line == "" || strings.HasPrefix(line, "#") || line == "version = 1" {
				continue
			}
			t.Errorf("the installed configuration sets %q.\nEverything but the schema version has to arrive commented out: a value here is one the operator did not choose, on a file they are about to be told is theirs", line)
		}
		for _, key := range []string{"shared_secret", "allowed_roots"} {
			if !strings.Contains(string(body), key) {
				t.Errorf("the installed configuration never mentions %s, which the next steps tell the operator to set in it", key)
			}
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
