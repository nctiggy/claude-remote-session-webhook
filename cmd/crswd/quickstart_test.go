//go:build quickstart

package main

// The milestone's acceptance run: specs/001-crswd-daemon-core/quickstart.md,
// executed against a real build and a real tmux rather than eyeballed (T042).
//
//	go test -tags quickstart ./cmd/crswd
//
// It is behind a build tag for the reason internal/tmuxctl's integration tests
// are: it compiles a binary, binds a loopback port, and starts real tmux
// sessions running a real program. None of that belongs in `go test ./...`,
// which every other task in this milestone had to keep fast and hermetic.
//
// Two departures from the literal shell in quickstart.md. Both make the run
// reproducible and neither weakens an assertion:
//
//   - Every daemon runs with TMUX_TMPDIR pointing at the test's own directory,
//     and with TMUX and TMUX_PANE removed from its environment, so the sessions
//     it starts land on a tmux server of their own rather than the operator's.
//     Dropping TMUX is not belt-and-braces, it is the whole mechanism: tmux
//     ignores TMUX_TMPDIR whenever TMUX is set, resolving to the socket named
//     there instead. Inside a tmux session — which is where ralph/loop.sh runs
//     — TMUX is always set, so an earlier version of this file believed it was
//     isolated, ran kill-server against the operator's own server, and took
//     down every session on the host. It did that twice.
//
//     This file's own tmux calls do not rely on that at all: they pass -S with
//     an explicit socket path, which is the advice internal/tmuxctl/exec.go
//     already gives — "isolation carried in the argv, not in an environment
//     variable that would isolate them right up until it silently did not".
//     The path is the one the daemon's own default-socket resolution lands on,
//     so the two share a server without either trusting the environment.
//   - PATH is prefixed with a directory holding a `claude` shim that echoes
//     each line it reads back with a marker. Story 2 asserts what the pane's
//     *foreground program* received; real Claude Code is a TUI that reflows and
//     redraws what it is handed, so a byte-for-byte assertion against its screen
//     would be a test of the terminal rather than of the daemon. The shim is
//     also what makes "no extra command ran" checkable at all: with a program in
//     the foreground, the text the shell would otherwise have executed is text
//     the shell never sees.
//
// Nothing else is stubbed. The binary is the one `go build` produces, the
// signatures are computed the way the skill will compute them, and every tmux
// session is a real window running a real shell.

import (
	"bytes"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/nctiggy/claude-remote-session-webhook/internal/tmuxctl"
)

// shimReady is what the stand-in prints once it is reading. The harness waits
// for it before prompting: a create returns as soon as the start keys are sent,
// and asserting what a program received before it is running would be asserting
// against the shell's echo instead.
const shimReady = "crswd-quickstart-shim-ready"

// shimEcho prefixes every line the stand-in read. A payload appearing after this
// marker is a payload that reached the foreground program's stdin, which is a
// stronger claim than it appearing on the screen — the tty echoes what is typed
// whether or not anything is reading it.
const shimEcho = "shim-read:"

// waitBudget bounds every poll in this file. These are real processes starting;
// a fixed sleep would be either flaky or slow, and an unbounded wait would hang
// the run instead of failing it.
const waitBudget = 20 * time.Second

// host is one acceptance run's fixtures: the built binary, the secret, the tmux
// server, and the PATH the daemon inherits.
type host struct {
	t *testing.T

	bin     string
	secret  string
	tmuxDir string
	shimDir string
	dir     string

	// socketDir is $TMUX_TMPDIR/tmux-$UID, the directory every server this run
	// starts puts its socket in.
	socketDir string

	// defaultSocket is where a tmux client with no -L lands once TMUX_TMPDIR is
	// honoured: $TMUX_TMPDIR/tmux-$UID/default. Nothing the daemon starts goes
	// there any more — it names its own server with -L (#22) — but it is still
	// what assertIsolated probes, because the environment is what that checks.
	defaultSocket string

	// socket is the server this file's own tmux commands address with -S: the
	// one the most recently started daemon drives. Set by startBinary, since the
	// name derives from the address that daemon listens on.
	socket string

	// home is the HOME the daemon runs with, and it is empty of shell startup
	// files on purpose. tmux starts the pane as a *login* shell, which sources
	// ~/.profile and ~/.bashrc — and this operator's ~/.bashrc re-prepends
	// ~/.local/bin, which puts the real `claude` ahead of the stand-in and
	// starts an actual Claude Code TUI instead. Pointing HOME at an empty
	// directory means there is nothing to source, so the PATH the daemon was
	// given survives into the pane.
	home string

	// workDir is the directory sessions are created in. It is the repo itself,
	// which is what quickstart.md uses and is under the default approved root.
	workDir string

	// roots is CRSW_ALLOWED_ROOTS: $HOME/code, exactly as quickstart.md sets it.
	roots string
}

func newHost(t *testing.T) *host {
	t.Helper()

	if _, err := exec.LookPath("tmux"); err != nil {
		t.Fatalf("tmux is not installed, and this run is the whole point of it being real: %v", err)
	}

	dir := t.TempDir()
	h := &host{
		t:       t,
		dir:     dir,
		bin:     filepath.Join(dir, "crswd"),
		tmuxDir: filepath.Join(dir, "tmux"),
		shimDir: filepath.Join(dir, "bin"),
		secret:  newSecret(t),
	}

	// tmux refuses a socket directory anyone else can write to. The tmux-$UID
	// level is created here rather than left to tmux because -S names a path
	// inside it, and tmux will not create a parent for a socket it was handed.
	socketDir := filepath.Join(h.tmuxDir, fmt.Sprintf("tmux-%d", os.Getuid()))
	h.home = filepath.Join(dir, "home")

	// $HOME/code under the daemon's HOME, so the unset-CRSW_ALLOWED_ROOTS case
	// has a default root that exists and reaches the warning rather than dying
	// on the root check before it.
	for _, d := range []string{h.tmuxDir, h.shimDir, socketDir, h.home, filepath.Join(h.home, "code")} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatalf("make %s: %v", d, err)
		}
	}
	h.socketDir = socketDir
	h.defaultSocket = filepath.Join(socketDir, "default")

	// Until a daemon starts there is no daemon server to address. Pointing at
	// the default one means a stray call reads "no server running" rather than
	// an empty string tmux would take as a relative path.
	h.socket = h.defaultSocket

	h.writeShim()
	h.build()

	repo, err := filepath.Abs("../..")
	if err != nil {
		t.Fatalf("locate the repo: %v", err)
	}
	h.workDir = repo

	// The approved root is the directory the checkout sits in, which is the
	// property quickstart.md's `$HOME/code` was standing in for: the working
	// directory these stories use is this repo, and it has to be under a root
	// or every create is refused for the wrong reason.
	//
	// It used to be `$HOME/code` literally, with a Stat that failed the suite if
	// it was absent. That held on a machine where the checkout happens to live
	// there and nowhere else — which is why this suite passed for one person and
	// could not run in CI at all (#87). A test that requires the developer's
	// directory layout is a test that only that developer can run, and it is
	// indistinguishable from a passing test until somebody else tries.
	//
	// Derived rather than assumed, so it is correct on both: on a workstation
	// with the checkout in ~/code this is still exactly ~/code, and on a runner
	// it is whatever the workspace parent happens to be.
	h.roots = filepath.Dir(repo)

	// Isolation is asserted before anything is started, not hoped for. If the
	// daemon's environment ever stops redirecting it, this fails one test rather
	// than destroying the operator's tmux server on the way out.
	h.assertIsolated()

	t.Cleanup(func() {
		// Every socket in this run's directory, not just the last daemon's: a
		// test may start several daemons, and since #22 each one has a server of
		// its own. Reaches only this run's servers — the directory is under
		// t.TempDir() and -S names each path explicitly.
		entries, err := os.ReadDir(socketDir)
		if err != nil {
			t.Logf("cleanup: read %s: %v", socketDir, err)
			return
		}
		for _, entry := range entries {
			out, err := h.tmuxOn(filepath.Join(socketDir, entry.Name()), "kill-server")
			if err != nil && !strings.Contains(out, "no server running") && !strings.Contains(out, "No such file") {
				t.Logf("cleanup kill-server on %s: %v: %s", entry.Name(), err, out)
			}
		}
	})
	return h
}

// assertIsolated proves the daemon's environment lands on this run's socket and
// not the operator's. It starts a server through exactly the environment the
// daemon will get, with no -S, and checks where it went.
//
// This is the regression guard for the bug that motivated it: TMUX_TMPDIR alone
// looks like isolation and is not, and the symptom is every tmux session on the
// host disappearing rather than a red test.
func (h *host) assertIsolated() {
	h.t.Helper()

	// A real session, because a server with none exits the moment it is started
	// and there would be nothing left to ask. Every command here goes through
	// the daemon's environment with no -S, since the environment is the thing
	// under test.
	const probe = "crswd-quickstart-isolation-probe"

	cmd := exec.Command("tmux", "new-session", "-d", "-s", probe)
	cmd.Env = h.env(nil)
	if out, err := cmd.CombinedOutput(); err != nil {
		h.t.Fatalf("start a session the way the daemon will: %v: %s", err, out)
	}

	cmd = exec.Command("tmux", "display-message", "-p", "-t", probe, "#{socket_path}")
	cmd.Env = h.env(nil)
	out, err := cmd.CombinedOutput()

	// Torn down through the same environment, so it is removed from whichever
	// server it actually landed on — including the operator's, if the check
	// below is about to report that this went wrong. Killing one named session
	// is safe anywhere; kill-server would not be.
	kill := exec.Command("tmux", "kill-session", "-t", "="+probe)
	kill.Env = h.env(nil)
	_ = kill.Run()

	if err != nil {
		h.t.Fatalf("resolve the daemon's socket: %v: %s", err, out)
	}

	got := strings.TrimSpace(string(out))
	if got != h.defaultSocket {
		h.t.Fatalf("the daemon's environment resolves to %s, want %s.\n"+
			"tmux ignores TMUX_TMPDIR when TMUX is set; refusing to run, because the "+
			"cleanup would kill the operator's tmux server and every session on this host.",
			got, h.socket)
	}
}

// newSecret is quickstart.md's `head -c 48 /dev/urandom | base64`.
func newSecret(t *testing.T) string {
	t.Helper()

	raw := make([]byte, 48)
	if _, err := rand.Read(raw); err != nil {
		t.Fatalf("read a shared secret: %v", err)
	}
	return base64.StdEncoding.EncodeToString(raw)
}

func (h *host) build() {
	h.t.Helper()

	cmd := exec.Command("go", "build", "-o", h.bin, "./cmd/crswd")
	cmd.Dir = "../.."
	if out, err := cmd.CombinedOutput(); err != nil {
		h.t.Fatalf("go build ./cmd/crswd: %v\n%s", err, out)
	}
}

// writeShim installs the stand-in described at the top of this file.
func (h *host) writeShim() {
	h.t.Helper()

	script := "#!/bin/sh\n" +
		"# Stand-in for `claude --dangerously-skip-permissions` during the acceptance run.\n" +
		"printf '%s\\n' " + shimReady + "\n" +
		"while IFS= read -r line; do printf '" + shimEcho + "%s\\n' \"$line\"; done\n"

	path := filepath.Join(h.shimDir, "claude")
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		h.t.Fatalf("write the claude stand-in: %v", err)
	}
}

// env is the daemon's environment: the process's own, with the quickstart's
// variables and the two isolating ones layered over it.
func (h *host) env(over map[string]string) []string {
	base := map[string]string{
		"CRSW_SHARED_SECRET": h.secret,
		"CRSW_ALLOWED_ROOTS": h.roots,
		"TMUX_TMPDIR":        h.tmuxDir,
		"PATH":               h.shimDir + string(os.PathListSeparator) + os.Getenv("PATH"),

		// Milestone 2's layer-1 configuration, which the daemon now refuses to
		// start without. No story here presents a browser assertion — these are
		// the API door's stories — so the values need only be well-formed. That
		// the six routes still answer a signed request with these set is what the
		// suite proves, and is not a change to any story.
		"CRSW_ACCESS_TEAM_DOMAIN":    "example-team.cloudflareaccess.com",
		"CRSW_ACCESS_AUD":            "quickstart-only-audience-tag",
		"CRSW_ACCESS_ALLOWED_EMAILS": "operator@example.com",

		// The two that make TMUX_TMPDIR mean anything. A tmux client with TMUX
		// set connects to the server named in it and ignores TMUX_TMPDIR
		// entirely, so leaving these through would point the daemon at the
		// operator's own server — see the note at the top of this file.
		"TMUX":      unset,
		"TMUX_PANE": unset,

		// Empty of shell startup files, so the pane's login shell cannot undo
		// the PATH above and shadow the stand-in with the real thing.
		"HOME": h.home,
	}
	for k, v := range over {
		base[k] = v
	}

	env := make([]string, 0, len(os.Environ())+len(base))
	for _, kv := range os.Environ() {
		name, _, _ := strings.Cut(kv, "=")
		if _, replaced := base[name]; !replaced {
			env = append(env, kv)
		}
	}
	for k, v := range base {
		if v == unset {
			continue
		}
		env = append(env, k+"="+v)
	}
	return env
}

// unset is the value that removes a variable rather than setting it, so a case
// like "CRSW_ALLOWED_ROOTS is not set" is expressible in the same map as the
// rest.
const unset = "\x00unset\x00"

// tmux runs one tmux command against the server the most recently started
// daemon drives.
func (h *host) tmux(args ...string) (string, error) {
	return h.tmuxOn(h.socket, args...)
}

// tmuxOn runs one tmux command against the named server, named explicitly with
// -S. The socket is in the argv rather than the environment so that a
// kill-server here cannot reach the operator's sessions even if the environment
// is wrong — the failure mode this file was written to stop causing.
func (h *host) tmuxOn(socket string, args ...string) (string, error) {
	cmd := exec.Command("tmux", append([]string{"-S", socket}, args...)...) //nolint:gosec // socket is under t.TempDir()
	cmd.Env = h.env(nil)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// socketFor is where a daemon listening on addr puts its tmux server. It calls
// the daemon's own derivation rather than repeating it, so a test cannot pass by
// agreeing with a copy of the rule that has since changed.
func (h *host) socketFor(addr string) string {
	return filepath.Join(h.socketDir, tmuxctl.SocketFor(addr))
}

// sessionNames is `tmux ls` on the most recently started daemon's server.
func (h *host) sessionNames() []string {
	h.t.Helper()
	return h.namesOn(h.socket)
}

// sessionNames is this daemon's own fleet, whichever daemon started last. The
// two-daemon story is the only caller, and it is the one that would silently
// pass if it read the wrong server.
func (d *daemon) sessionNames() []string {
	d.t.Helper()
	return d.h.namesOn(d.socket)
}

// namesOn is `tmux ls` reduced to the names, with an absent server read as
// an empty fleet — which is what it means.
func (h *host) namesOn(socket string) []string {
	h.t.Helper()

	out, err := h.tmuxOn(socket, "list-sessions", "-F", "#{session_name}")
	if err != nil {
		if strings.Contains(out, "no server running") || strings.Contains(out, "No such file") {
			return nil
		}
		h.t.Fatalf("tmux list-sessions: %v: %s", err, out)
	}

	var names []string
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if line != "" {
			names = append(names, line)
		}
	}
	return names
}

func (h *host) hasSession(name string) bool {
	h.t.Helper()
	return slices.Contains(h.sessionNames(), name)
}

// freePort asks the kernel for one and gives it straight back. CRSW_LISTEN takes
// no port 0, so the address has to be decided before the daemon starts.
func freePort(t *testing.T) string {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("find a free port: %v", err)
	}
	defer func() { _ = ln.Close() }()
	return ln.Addr().String()
}

// freeAddrOn is freePort for the two startup cases whose address is deliberately
// not 127.0.0.1: it asks the kernel for a port free under that exact spelling of
// the host, and gives it straight back.
//
// quickstart.md writes both as port 8765, and so did this file. That is the port
// .env.example, the systemd unit and the live deployment all name, so on the one
// host an operator would run an acceptance suite on — the host running the
// product — the "nothing bound" probe below was reporting the deployed daemon's
// listener rather than a refusal that had leaked one. The host spelling is what
// each of those two cases is about; the port never was.
func freeAddrOn(t *testing.T, host string) string {
	t.Helper()

	ln, err := net.Listen("tcp", net.JoinHostPort(host, "0"))
	if err != nil {
		t.Fatalf("find a free port on %s: %v", host, err)
	}
	defer func() { _ = ln.Close() }()

	_, port, err := net.SplitHostPort(ln.Addr().String())
	if err != nil {
		t.Fatalf("read the port back out of %s: %v", ln.Addr(), err)
	}
	// The spelling the case asked for, not the one the kernel resolved it to: a
	// "localhost" case that ran against 127.0.0.1 would be the 127.0.0.1 case.
	return net.JoinHostPort(host, port)
}

// daemon is one running crswd, with everything it wrote captured.
type daemon struct {
	t    *testing.T
	h    *host
	cmd  *exec.Cmd
	addr string

	// socket is the tmux server this daemon drives, which since #22 is its own:
	// the name is derived from addr, so no two daemons share one.
	socket string

	// trail holds stdout and stderr together. The audit records are stdout and
	// the default-root warning is stderr; under the systemd unit both land in
	// the same journal, so reading them from one file is what an operator sees.
	trail string

	done chan error
}

// start runs the daemon and waits until it is actually accepting.
func (h *host) start(over map[string]string) *daemon {
	h.t.Helper()
	return h.startBinary(h.bin, over)
}

// startBinary is start with the artifact named, because milestone 2's story 5
// runs a second one: the -tags dev build, with a flag the shipping binary does
// not define. Every story here still goes through start and so through h.bin.
func (h *host) startBinary(bin string, over map[string]string, args ...string) *daemon {
	h.t.Helper()

	addr, ok := over["CRSW_LISTEN"]
	if !ok {
		addr = freePort(h.t)
		if over == nil {
			over = map[string]string{}
		}
		over["CRSW_LISTEN"] = addr
	}

	trail := filepath.Join(h.dir, fmt.Sprintf("trail-%d.jsonl", time.Now().UnixNano()))
	f, err := os.Create(trail)
	if err != nil {
		h.t.Fatalf("create the trail file: %v", err)
	}

	cmd := exec.Command(bin, args...) //nolint:gosec // bin is built by this suite into t.TempDir()
	cmd.Env = h.env(over)
	cmd.Stdout = f
	cmd.Stderr = f
	if err := cmd.Start(); err != nil {
		h.t.Fatalf("start the daemon: %v", err)
	}

	d := &daemon{t: h.t, h: h, cmd: cmd, addr: addr, socket: h.socketFor(addr), trail: trail, done: make(chan error, 1)}

	// The harness's own tmux commands follow the daemon just started, which is
	// the one every single-daemon story here is about. A story with two daemons
	// addresses each through d.sessionNames.
	h.socket = d.socket
	go func() {
		err := cmd.Wait()
		_ = f.Close()
		d.done <- err
	}()

	h.t.Cleanup(func() { d.stop(syscall.SIGKILL) })
	d.waitUntilServing()
	return d
}

func (d *daemon) waitUntilServing() {
	d.t.Helper()

	deadline := time.Now().Add(waitBudget)
	for time.Now().Before(deadline) {
		select {
		case err := <-d.done:
			d.done <- err
			d.t.Fatalf("the daemon exited before it served (%v):\n%s", err, d.readTrail())
		default:
		}

		conn, err := net.DialTimeout("tcp", d.addr, 200*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	d.t.Fatalf("the daemon never accepted on %s:\n%s", d.addr, d.readTrail())
}

// stop signals the daemon and waits for it to go, returning its exit error.
func (d *daemon) stop(sig syscall.Signal) error {
	if d.cmd.Process == nil {
		return nil
	}
	// An already-exited process is not an error to this helper: several stories
	// stop the daemon themselves and the cleanup still runs.
	_ = d.cmd.Process.Signal(sig)

	select {
	case err := <-d.done:
		d.done <- err
		return err
	case <-time.After(waitBudget):
		_ = d.cmd.Process.Kill()
		return fmt.Errorf("the daemon did not exit within %s of %v", waitBudget, sig)
	}
}

func (d *daemon) readTrail() string {
	b, err := os.ReadFile(d.trail)
	if err != nil {
		return fmt.Sprintf("<trail unreadable: %v>", err)
	}
	return string(b)
}

// auditRecord is one line of the trail, in internal/audit's shape.
type auditRecord struct {
	Time      string `json:"time"`
	Action    string `json:"action"`
	Caller    string `json:"caller"`
	SessionID string `json:"session_id"`
	Decision  string `json:"decision"`
	Reason    string `json:"reason"`
	Remote    string `json:"remote"`
}

// records parses the trail, keeping only the lines that are audit records. The
// warning banner and anything log.Printf wrote are not JSON and are skipped
// here; the leak assertions read the raw file instead.
func (d *daemon) records() []auditRecord {
	d.t.Helper()

	var out []auditRecord
	for _, line := range strings.Split(d.readTrail(), "\n") {
		if !strings.HasPrefix(strings.TrimSpace(line), "{") {
			continue
		}
		var rec auditRecord
		if err := json.Unmarshal([]byte(line), &rec); err != nil || rec.Action == "" {
			continue
		}
		out = append(out, rec)
	}
	return out
}

// response is what a call came back with, kept whole so two refusals can be
// compared byte for byte rather than by status alone.
type response struct {
	Status int
	Header http.Header
	Body   []byte
}

// fingerprint is everything a caller can observe about a refusal except the
// clock: the status, the body, and every header but Date. FR-033 wants the
// unknown, the not-yours, and the wrong-token 404s to be indistinguishable, and
// a difference in Content-Length alone would be an enumeration oracle.
func (r response) fingerprint() string {
	var b strings.Builder
	fmt.Fprintf(&b, "status=%d\n", r.Status)

	names := make([]string, 0, len(r.Header))
	for name := range r.Header {
		if name != "Date" {
			names = append(names, name)
		}
	}
	slices.Sort(names)
	for _, name := range names {
		fmt.Fprintf(&b, "%s: %s\n", name, strings.Join(r.Header[name], ","))
	}
	fmt.Fprintf(&b, "body=%q", r.Body)
	return b.String()
}

func (r response) json(t *testing.T) map[string]any {
	t.Helper()

	var v map[string]any
	if err := json.Unmarshal(r.Body, &v); err != nil {
		t.Fatalf("decode %q: %v", r.Body, err)
	}
	return v
}

// sign is the signing helper from quickstart.md, in Go: HMAC-SHA256 over
// METHOD "\n" PATH "\n" timestamp "." rawBody, hex, behind the sha256= prefix.
//
// Method and path are in the payload because a signature has to name what it
// authorizes. Over the timestamp and body alone, every empty-body GET in one
// second signed identically — so the daemon refused its own second read as a
// replay — and a signed read of one route was a valid write to another.
func (h *host) sign(method, path string, ts int64, body string) string {
	mac := hmac.New(sha256.New, []byte(h.secret))
	mac.Write([]byte(method))
	mac.Write([]byte("\n"))
	mac.Write([]byte(path))
	mac.Write([]byte("\n"))
	mac.Write([]byte(strconv.FormatInt(ts, 10)))
	mac.Write([]byte("."))
	mac.Write([]byte(body))
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

// request builds a signed request without sending it, so a story can send the
// same bytes twice (the replay case) or alter one header (the tamper case).
func (d *daemon) request(method, path, body, bearer string, ts int64) *http.Request {
	d.t.Helper()

	var rdr io.Reader
	if body != "" {
		rdr = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, "http://"+d.addr+path, rdr)
	if err != nil {
		d.t.Fatalf("build %s %s: %v", method, path, err)
	}

	req.Header.Set("X-CRSW-Timestamp", strconv.FormatInt(ts, 10))
	// EscapedPath, so the fixture signs the bytes the daemon will read off the
	// request line rather than a decoded spelling of them.
	req.Header.Set("X-CRSW-Signature", d.h.sign(method, req.URL.EscapedPath(), ts, body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	return req
}

func (d *daemon) do(req *http.Request) response {
	d.t.Helper()

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		d.t.Fatalf("%s %s: %v", req.Method, req.URL.Path, err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		d.t.Fatalf("read the response to %s %s: %v", req.Method, req.URL.Path, err)
	}
	return response{Status: resp.StatusCode, Header: resp.Header.Clone(), Body: body}
}

// call is quickstart.md's crswd_call.
func (d *daemon) call(method, path, body, bearer string) response {
	d.t.Helper()
	return d.do(d.request(method, path, body, bearer, time.Now().Unix()))
}

// bodyOf sends the same request twice, which is the replay case: the second use
// of one signature must be refused.
func (d *daemon) replay(req *http.Request, body string) (response, response) {
	d.t.Helper()

	first := d.do(cloneWithBody(d.t, req, body))
	second := d.do(cloneWithBody(d.t, req, body))
	return first, second
}

func cloneWithBody(t *testing.T, req *http.Request, body string) *http.Request {
	t.Helper()

	clone := req.Clone(req.Context())
	if body != "" {
		clone.Body = io.NopCloser(strings.NewReader(body))
		clone.ContentLength = int64(len(body))
	}
	return clone
}

// created is the 201 body, the one time a token exists outside the daemon.
type created struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	WorkDir   string `json:"work_dir"`
	State     string `json:"state"`
	CreatedAt string `json:"created_at"`
	ExpiresAt string `json:"expires_at"`
	Token     string `json:"token"`
}

func (d *daemon) createSession(name string) created {
	d.t.Helper()

	body := fmt.Sprintf(`{"name":%q,"work_dir":%q}`, name, d.h.workDir)
	resp := d.call(http.MethodPost, "/sessions", body, "")
	if resp.Status != http.StatusCreated {
		d.t.Fatalf("POST /sessions = %d, want 201: %s", resp.Status, resp.Body)
	}

	var c created
	if err := json.Unmarshal(resp.Body, &c); err != nil {
		d.t.Fatalf("decode the create response: %v", err)
	}
	return c
}

// waitForPane polls the pane until it holds what the caller is waiting for.
func (d *daemon) waitForPane(id, token, want string) string {
	d.t.Helper()

	deadline := time.Now().Add(waitBudget)
	var last string
	for time.Now().Before(deadline) {
		resp := d.call(http.MethodGet, "/sessions/"+id+"/output", "", token)
		if resp.Status == http.StatusOK {
			var out struct {
				Text string `json:"text"`
			}
			if err := json.Unmarshal(resp.Body, &out); err == nil {
				last = out.Text
				if strings.Contains(last, want) {
					return last
				}
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	d.t.Fatalf("timed out waiting for %q in the pane; last capture:\n%s", want, last)
	return ""
}

// ---------------------------------------------------------------------------
// Prerequisites
// ---------------------------------------------------------------------------

// TestQuickstartPrerequisites checks the table at the top of quickstart.md
// against the host it claims to describe.
func TestQuickstartPrerequisites(t *testing.T) {
	for _, tool := range []struct {
		name string
		args []string
	}{
		{"go", []string{"version"}},
		{"tmux", []string{"-V"}},
		{"golangci-lint", []string{"--version"}},
		{"goimports", nil},
	} {
		path, err := exec.LookPath(tool.name)
		if err != nil {
			t.Errorf("%s: %v", tool.name, err)
			continue
		}
		version := path
		if tool.args != nil {
			out, err := exec.Command(tool.name, tool.args...).CombinedOutput()
			if err != nil {
				t.Errorf("%s %v: %v", tool.name, tool.args, err)
				continue
			}
			version = strings.TrimSpace(string(out))
		}
		t.Logf("%-14s %s", tool.name, version)
	}
}

// ---------------------------------------------------------------------------
// Story 1 (P1) — start a session, and prove nothing else can
// ---------------------------------------------------------------------------

func TestQuickstartStory1HappyPath(t *testing.T) {
	h := newHost(t)
	d := h.start(nil)

	c := d.createSession("demo")
	if c.Token == "" {
		t.Fatal("the create response carried no token, which is the only time it is ever shown")
	}
	t.Logf("created %s in %s", c.ID, c.WorkDir)

	createdAt, err := time.Parse(time.RFC3339, c.CreatedAt)
	if err != nil {
		t.Fatalf("parse created_at %q: %v", c.CreatedAt, err)
	}
	expiresAt, err := time.Parse(time.RFC3339, c.ExpiresAt)
	if err != nil {
		t.Fatalf("parse expires_at %q: %v", c.ExpiresAt, err)
	}
	if got := expiresAt.Sub(createdAt); got != 24*time.Hour {
		t.Errorf("expires_at - created_at = %s, want 24h", got)
	}

	// quickstart.md: tmux has-session -t "=crswd-<id>"
	if !h.hasSession("crswd" + "-" + c.ID) {
		t.Errorf("no tmux session crswd-%s; the fleet is %v", c.ID, h.sessionNames())
	}

	// The token appears in the 201 and nowhere else: not in a list entry, not
	// in a detail.
	list := d.call(http.MethodGet, "/sessions", "", "")
	if strings.Contains(string(list.Body), c.Token) {
		t.Error("GET /sessions returned the session token")
	}
	detail := d.call(http.MethodGet, "/sessions/"+c.ID, "", c.Token)
	if strings.Contains(string(detail.Body), c.Token) {
		t.Error("GET /sessions/{id} returned the session token")
	}
}

func TestQuickstartStory1Refusals(t *testing.T) {
	h := newHost(t)
	d := h.start(nil)

	body := fmt.Sprintf(`{"name":"x","work_dir":%q}`, h.workDir)
	before := len(h.sessionNames())

	unsigned, err := http.NewRequest(http.MethodPost, "http://"+d.addr+"/sessions", strings.NewReader(body))
	if err != nil {
		t.Fatalf("build the unsigned request: %v", err)
	}
	unsigned.Header.Set("Content-Type", "application/json")

	now := time.Now().Unix()
	tampered := d.request(http.MethodPost, "/sessions", `{"name":"a"}`, "", now)
	tampered.Body = io.NopCloser(strings.NewReader(body))
	tampered.ContentLength = int64(len(body))

	cases := map[string]*http.Request{
		"unsigned":    unsigned,
		"tampered":    tampered,
		"stale":       d.request(http.MethodPost, "/sessions", body, "", now-3600),
		"far future":  d.request(http.MethodPost, "/sessions", body, "", now+3600),
		"no headers":  mustRequest(t, http.MethodGet, "http://"+d.addr+"/sessions"),
		"bad secret":  withSignature(d.request(http.MethodPost, "/sessions", body, "", now), "sha256="+strings.Repeat("0", 64)),
		"no sha256=":  withSignature(d.request(http.MethodPost, "/sessions", body, "", now), strings.Repeat("0", 64)),
		"unparseable": withTimestamp(d.request(http.MethodPost, "/sessions", body, "", now), "not-a-number"),
	}

	var fingerprints []string
	for name, req := range cases {
		resp := d.do(req)
		if resp.Status != http.StatusUnauthorized {
			t.Errorf("%s = %d, want 401: %s", name, resp.Status, resp.Body)
		}
		fingerprints = append(fingerprints, name+"\n"+resp.fingerprint())
	}
	assertIdentical(t, "the 401s", fingerprints)

	// The replay case: the exact same bytes twice. The first must pass and the
	// second must not, and exactly one session may exist afterwards.
	first, second := d.replay(d.request(http.MethodPost, "/sessions", body, "", time.Now().Unix()), body)
	if first.Status != http.StatusCreated {
		t.Fatalf("the first use of a signature = %d, want 201: %s", first.Status, first.Body)
	}
	if second.Status != http.StatusUnauthorized {
		t.Errorf("the replayed request = %d, want 401: %s", second.Status, second.Body)
	}

	if got, want := len(h.sessionNames()), before+1; got != want {
		t.Errorf("the host holds %d sessions, want %d — a refused request started one", got, want)
	}
}

func TestQuickstartStory1BoundaryRefusals(t *testing.T) {
	h := newHost(t)
	// The rate limit is raised so that *validation* is what refuses each case.
	// At the default six a minute the burst is three, and a table of nine
	// bodies is refused 429 from the fourth on — the right answer to a
	// different question, and it would hide whether the path check works at
	// all. Story 5 asserts the limiter itself.
	d := h.start(map[string]string{"CRSW_CREATE_RATE_PER_MIN": "600"})

	// The symlink escape, planted inside an approved root so that what is being
	// refused is the *resolution* and not the literal path: $HOME/code/<tmp>
	// is approved, and $HOME/code/<tmp>/escape resolves to /etc, which is not.
	inRoot, err := os.MkdirTemp(h.roots, ".crswd-quickstart-")
	if err != nil {
		t.Fatalf("make a directory under %s: %v", h.roots, err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(inRoot) })

	escape := filepath.Join(inRoot, "escape")
	if err := os.Symlink("/etc", escape); err != nil {
		t.Fatalf("plant the escaping symlink: %v", err)
	}

	before := len(h.sessionNames())
	cases := map[string]string{
		"colon in the name": fmt.Sprintf(`{"name":"bad:name","work_dir":%q}`, h.roots),
		"dot in the name":   fmt.Sprintf(`{"name":"bad.name","work_dir":%q}`, h.roots),
		"traversal":         fmt.Sprintf(`{"name":"ok","work_dir":%q}`, h.roots+"/../../etc"),
		"outside the roots": `{"name":"ok","work_dir":"/etc"}`,
		"unknown field":     fmt.Sprintf(`{"name":"ok","work_dir":%q,"extra":1}`, h.roots),
		"symlink escape":    fmt.Sprintf(`{"name":"ok","work_dir":%q}`, escape),
		"relative work_dir": `{"name":"ok","work_dir":"code"}`,
		"empty name":        fmt.Sprintf(`{"name":"","work_dir":%q}`, h.roots),
		"not a directory":   fmt.Sprintf(`{"name":"ok","work_dir":%q}`, filepath.Join(h.workDir, "go.mod")),
	}

	var fingerprints []string
	for name, body := range cases {
		resp := d.call(http.MethodPost, "/sessions", body, "")
		if resp.Status != http.StatusBadRequest {
			t.Errorf("%s = %d, want 400: %s", name, resp.Status, resp.Body)
		}
		fingerprints = append(fingerprints, name+"\n"+resp.fingerprint())
	}
	assertIdentical(t, "the 400s", fingerprints)

	if got := len(h.sessionNames()); got != before {
		t.Errorf("the host holds %d sessions, want %d — a refused request started one", got, before)
	}
}

// TestQuickstartStory1LoudDefault is SC-015: the warning appears on every start
// that defaults, not only the first.
func TestQuickstartStory1LoudDefault(t *testing.T) {
	h := newHost(t)

	// Two starts, both with CRSW_ALLOWED_ROOTS unset. Each is pointed at an
	// address that is already taken so that it warns, fails to bind, and exits
	// on its own rather than needing to be killed mid-assertion.
	taken, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("hold a port: %v", err)
	}
	defer func() { _ = taken.Close() }()

	for attempt := 1; attempt <= 2; attempt++ {
		out, code := h.run(map[string]string{
			"CRSW_ALLOWED_ROOTS": unset,
			"CRSW_LISTEN":        taken.Addr().String(),
		})
		if code == 0 {
			t.Errorf("start %d exited 0 with the port already taken", attempt)
		}
		// h.home, not the operator's: env() gives the daemon its own HOME, so
		// the default root it names is the one under that.
		for _, want := range []string{"WARNING", "CRSW_ALLOWED_ROOTS", filepath.Join(h.home, "code")} {
			if !strings.Contains(out, want) {
				t.Errorf("start %d: the default-root warning does not mention %q:\n%s", attempt, want, out)
			}
		}
	}
}

// TestQuickstartStory1StartupFailures is SC-014: a weak configuration is a
// startup failure, and nothing binds.
func TestQuickstartStory1StartupFailures(t *testing.T) {
	h := newHost(t)
	addr := freePort(t)

	cases := map[string]map[string]string{
		"the secret is unset":      {"CRSW_SHARED_SECRET": unset, "CRSW_LISTEN": addr},
		"the secret is too short":  {"CRSW_SHARED_SECRET": "tooshort", "CRSW_LISTEN": addr},
		"the secret is 31 bytes":   {"CRSW_SHARED_SECRET": strings.Repeat("a", 31), "CRSW_LISTEN": addr},
		"the listener is public":   {"CRSW_LISTEN": freeAddrOn(t, "0.0.0.0")},
		"the listener is a name":   {"CRSW_LISTEN": freeAddrOn(t, "localhost")},
		"the root does not exist":  {"CRSW_ALLOWED_ROOTS": filepath.Join(h.dir, "nope"), "CRSW_LISTEN": addr},
		"the cap is not a number":  {"CRSW_MAX_SESSIONS": "many", "CRSW_LISTEN": addr},
		"the cap bounds nothing":   {"CRSW_MAX_SESSIONS": "0", "CRSW_LISTEN": addr},
		"the rate bounds nothing":  {"CRSW_CREATE_RATE_PER_MIN": "0", "CRSW_LISTEN": addr},
		"the body limit is absurd": {"CRSW_MAX_BODY_BYTES": "-1", "CRSW_LISTEN": addr},
	}

	for name, over := range cases {
		out, code := h.run(over)
		if code == 0 {
			t.Errorf("%s: exit=0, want non-zero:\n%s", name, out)
		}
		if strings.Contains(out, h.secret) {
			t.Errorf("%s: the startup error carries the shared secret:\n%s", name, out)
		}

		// Nothing bound: the address the case asked for is still free.
		want := over["CRSW_LISTEN"]
		if ln, err := net.Listen("tcp", want); err != nil {
			t.Errorf("%s: %s is still held after the refusal: %v", name, want, err)
		} else {
			_ = ln.Close()
		}
		t.Logf("%-26s exit=%d %s", name, code, firstLine(out))
	}
}

// run starts the daemon and waits for it to exit on its own, which is what the
// startup-failure cases do.
func (h *host) run(over map[string]string) (string, int) {
	h.t.Helper()
	return h.runBinary(h.bin, over)
}

// runBinary is run with the artifact and its arguments named, for the same
// reason startBinary exists: milestone 2's story 5 asks both builds to refuse
// something, and one of the two refusals is of a command-line flag.
func (h *host) runBinary(bin string, over map[string]string, args ...string) (string, int) {
	h.t.Helper()

	cmd := exec.Command(bin, args...) //nolint:gosec // bin is built by this suite into t.TempDir()
	cmd.Env = h.env(over)
	out, err := cmd.CombinedOutput()

	code := 0
	var exit *exec.ExitError
	switch {
	case err == nil:
	case errorsAs(err, &exit):
		code = exit.ExitCode()
	default:
		h.t.Fatalf("run the daemon: %v", err)
	}
	return string(out), code
}

// ---------------------------------------------------------------------------
// Story 2 (P2) — drive a session, hostilely
// ---------------------------------------------------------------------------

// TestQuickstartStory2Prompt is the regression research D4 is about: a lone
// semicolon that arrives as an empty string is what send-keys -l would have
// produced, and it must not happen here.
func TestQuickstartStory2Prompt(t *testing.T) {
	h := newHost(t)
	d := h.start(nil)

	c := d.createSession("hostile")
	d.waitForPane(c.ID, c.Token, shimReady)

	payloads := []string{
		";",
		"foo;",
		"foo;;",
		"a; echo PWNED; $(id) `whoami`",
		`"quoted" 'single' \backslash\ $HOME %s`,
	}
	for _, text := range payloads {
		body, err := json.Marshal(map[string]string{"text": text})
		if err != nil {
			t.Fatalf("encode the prompt: %v", err)
		}
		resp := d.call(http.MethodPost, "/sessions/"+c.ID+"/prompt", string(body), c.Token)
		if resp.Status != http.StatusAccepted {
			t.Fatalf("prompt %q = %d, want 202: %s", text, resp.Status, resp.Body)
		}
		// Waited for one at a time so a payload that never arrives fails against
		// itself rather than against whatever followed it.
		pane := d.waitForPane(c.ID, c.Token, shimEcho+text)
		t.Logf("delivered %q", text)
		_ = pane
	}

	// The embedded newline is the one payload that cannot arrive as a single
	// line: a line-oriented reader sees two, which is delivery and not loss.
	body, err := json.Marshal(map[string]string{"text": "first\nsecond"})
	if err != nil {
		t.Fatalf("encode the prompt: %v", err)
	}
	if resp := d.call(http.MethodPost, "/sessions/"+c.ID+"/prompt", string(body), c.Token); resp.Status != http.StatusAccepted {
		t.Fatalf("prompt with an embedded newline = %d, want 202: %s", resp.Status, resp.Body)
	}
	d.waitForPane(c.ID, c.Token, shimEcho+"first")
	pane := d.waitForPane(c.ID, c.Token, shimEcho+"second")

	// PWNED appears only as text the program read back. A line that is PWNED and
	// nothing else, or any output from id, would mean the shell ran it.
	for _, line := range strings.Split(pane, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "PWNED" {
			t.Errorf("a bare PWNED line is in the pane, so the text was executed:\n%s", pane)
		}
		if strings.HasPrefix(trimmed, "uid=") {
			t.Errorf("id ran: %q\n%s", trimmed, pane)
		}
	}

	// FR-031: what leaves the daemon is plain text.
	if strings.ContainsRune(pane, 0x1b) {
		t.Errorf("the captured pane carries an ESC byte:\n%q", pane)
	}

	// An empty prompt is the one thing a caller can fix, and is a 400.
	if resp := d.call(http.MethodPost, "/sessions/"+c.ID+"/prompt", `{"text":""}`, c.Token); resp.Status != http.StatusBadRequest {
		t.Errorf("an empty prompt = %d, want 400: %s", resp.Status, resp.Body)
	}

	// FR-042: none of it reached the trail.
	trail := d.readTrail()
	for _, secret := range []string{"PWNED", "whoami", c.Token, h.secret, shimEcho} {
		if strings.Contains(trail, secret) {
			t.Errorf("the trail carries %q", secret)
		}
	}
}

// ---------------------------------------------------------------------------
// Story 3 (P3) — isolation and verified teardown
// ---------------------------------------------------------------------------

func TestQuickstartStory3Isolation(t *testing.T) {
	h := newHost(t)
	d := h.start(nil)

	a := d.createSession("alpha")
	b := d.createSession("bravo")

	// Every refusal on a session-scoped route must be the identical 404. The
	// cross-*owner* case needs a second identity, which milestone 1's single
	// shared secret cannot produce by hand — quickstart.md says so, and
	// internal/httpapi/isolation_test.go covers it with a synthetic owner.
	// Each probe is signed at its own instant inside the window. Three of these
	// differ from one another *only* by bearer token — and the token is layer 3,
	// deliberately not part of the layer-2 payload — so at one shared timestamp
	// they sign identically and the replay cache refuses the second and third as
	// what they look like on the wire: the same request twice. That refusal is
	// correct, and it is not what this test is asking about. Distinct timestamps
	// make them distinct requests without weakening a single assertion.
	unknown := strings.Repeat("0", 32)
	probe := func(method, path, body, bearer string, age int) response {
		return d.do(d.request(method, path, body, bearer, time.Now().Unix()-int64(age)))
	}
	cases := map[string]response{
		"an unknown id":            probe(http.MethodGet, "/sessions/"+unknown, "", a.Token, 1),
		"another session's token":  probe(http.MethodGet, "/sessions/"+a.ID, "", b.Token, 2),
		"a token that is nonsense": probe(http.MethodGet, "/sessions/"+a.ID, "", strings.Repeat("f", 64), 3),
		"no token at all":          probe(http.MethodGet, "/sessions/"+a.ID, "", "", 4),
		"an id that is not hex":    probe(http.MethodGet, "/sessions/not-an-id", "", a.Token, 5),
		"b's output through a":     probe(http.MethodGet, "/sessions/"+b.ID+"/output", "", a.Token, 6),
		"a prompt into b as a":     probe(http.MethodPost, "/sessions/"+b.ID+"/prompt", `{"text":"x"}`, a.Token, 7),
		"a destroy of b as a":      probe(http.MethodDelete, "/sessions/"+b.ID, "", a.Token, 8),
	}

	var fingerprints []string
	for name, resp := range cases {
		if resp.Status != http.StatusNotFound {
			t.Errorf("%s = %d, want 404: %s", name, resp.Status, resp.Body)
		}
		fingerprints = append(fingerprints, name+"\n"+resp.fingerprint())
	}
	assertIdentical(t, "the 404s", fingerprints)

	// b survived every one of those.
	if !h.hasSession("crswd-" + b.ID) {
		t.Errorf("session b is gone after being probed through a's credential")
	}

	// Verified teardown.
	resp := d.call(http.MethodDelete, "/sessions/"+a.ID, "", a.Token)
	if resp.Status != http.StatusOK {
		t.Fatalf("DELETE /sessions/%s = %d, want 200: %s", a.ID, resp.Status, resp.Body)
	}
	if got := resp.json(t)["destroyed"]; got != true {
		t.Errorf(`the destroy response says "destroyed":%v, want true`, got)
	}
	if h.hasSession("crswd-" + a.ID) {
		t.Errorf("tmux still holds crswd-%s after a 200 teardown", a.ID)
	}

	// A second DELETE is the identical 404, not an idempotent 200. Signed a
	// second earlier so that it is a new request rather than a byte-for-byte
	// repeat of the one that just succeeded, which the replay cache would refuse
	// before the handler could answer.
	again := d.do(d.request(http.MethodDelete, "/sessions/"+a.ID, "", a.Token, time.Now().Unix()-1))
	if again.Status != http.StatusNotFound {
		t.Errorf("a second DELETE = %d, want 404: %s", again.Status, again.Body)
	}
	assertIdentical(t, "a second DELETE against an unknown id", []string{
		"second delete\n" + again.fingerprint(),
		"unknown id\n" + d.call(http.MethodGet, "/sessions/"+unknown, "", b.Token).fingerprint(),
	})
}

// ---------------------------------------------------------------------------
// Story 4 (P4) — restart without orphans
// ---------------------------------------------------------------------------

func TestQuickstartStory4Restart(t *testing.T) {
	h := newHost(t)
	addr := freePort(t)
	d := h.start(map[string]string{"CRSW_LISTEN": addr})

	c := d.createSession("survivor")
	d.waitForPane(c.ID, c.Token, shimReady)

	// SIGKILL, not SIGTERM. A termination signal is the ending FR-040 gives the
	// daemon and it reaps every session on the way out, so the danger state this
	// story is about — a live session with no daemon — can only be reached by
	// the daemon dying without warning.
	_ = d.stop(syscall.SIGKILL)
	if !h.hasSession("crswd-" + c.ID) {
		t.Fatalf("the session did not outlive the crash, so there is nothing to adopt")
	}

	const lookalike = "crswd-notours-by-hand"
	if out, err := h.tmux("new-session", "-d", "-s", lookalike); err != nil {
		t.Fatalf("plant the lookalike: %v: %s", err, out)
	}

	restarted := h.start(map[string]string{"CRSW_LISTEN": addr})

	list := restarted.call(http.MethodGet, "/sessions", "", "")
	if list.Status != http.StatusOK {
		t.Fatalf("GET /sessions = %d, want 200: %s", list.Status, list.Body)
	}
	var listed struct {
		Sessions []struct {
			ID      string `json:"id"`
			Adopted bool   `json:"adopted"`
			State   string `json:"state"`
			// Present on exactly one list: the first after adoption, which is the
			// only response an adopted session's credential can arrive in.
			Token string `json:"token"`
		} `json:"sessions"`
	}
	if err := json.Unmarshal(list.Body, &listed); err != nil {
		t.Fatalf("decode the list: %v", err)
	}

	found := false
	for _, s := range listed.Sessions {
		if s.ID == c.ID {
			found = true
			if !s.Adopted {
				t.Errorf("session %s is listed but not marked adopted", s.ID)
			}
		}
		if strings.Contains(lookalike, s.ID) {
			t.Errorf("the hand-made session %s was adopted (FR-022)", s.ID)
		}
	}
	if !found {
		t.Errorf("the surviving session %s is not in the list: %s", c.ID, list.Body)
	}
	if len(listed.Sessions) != 1 {
		t.Errorf("the list holds %d sessions, want 1: %s", len(listed.Sessions), list.Body)
	}

	// FR-022: the lookalike was left alone and is still running.
	if !h.hasSession(lookalike) {
		t.Errorf("%s was destroyed; the daemon touched a session it did not start", lookalike)
	}

	// FR-021: the pre-restart token is dead, and its refusal is the uniform 404.
	stale := restarted.call(http.MethodGet, "/sessions/"+c.ID, "", c.Token)
	if stale.Status != http.StatusNotFound {
		t.Errorf("the pre-restart token = %d, want 404: %s", stale.Status, stale.Body)
	}

	// One startup.adopt record for the session, and none for the lookalike.
	adopts := 0
	for _, rec := range restarted.records() {
		if rec.Action == "startup.adopt" {
			adopts++
			if rec.SessionID != c.ID {
				t.Errorf("startup.adopt names session %q, want %q", rec.SessionID, c.ID)
			}
		}
	}
	if adopts != 1 {
		t.Errorf("%d startup.adopt records, want 1", adopts)
	}

	// FR-021's other half: the fresh credential has to reach the operator, or
	// "taken under management" means a session nobody can drive. It arrives in
	// the first list after adoption — the first moment anyone asks — and the list
	// above was that moment, so its token is the one this session will ever have.
	var claimed string
	for _, s := range listed.Sessions {
		if s.ID == c.ID {
			claimed = s.Token
		}
	}
	if claimed == "" {
		t.Fatalf("the list after adoption carried no credential for %s: %s", c.ID, list.Body)
	}
	if claimed == c.Token {
		t.Error("the adopted session was handed back the credential from before the restart")
	}

	// And it works, on every session-scoped route.
	if resp := restarted.call(http.MethodPost, "/sessions/"+c.ID+"/prompt", `{"text":"x"}`, claimed); resp.Status != http.StatusAccepted {
		t.Errorf("prompt with the claimed credential = %d, want 202: %s", resp.Status, resp.Body)
	}
	if resp := restarted.call(http.MethodGet, "/sessions/"+c.ID+"/output", "", claimed); resp.Status != http.StatusOK {
		t.Errorf("output with the claimed credential = %d, want 200: %s", resp.Status, resp.Body)
	}

	// Once, and only once. A second list must not re-issue it — that would make
	// "returned once" mean "returned to whoever asks" (FR-013).
	second := restarted.call(http.MethodGet, "/sessions", "", "")
	if strings.Contains(string(second.Body), claimed) {
		t.Error("a second list returned the adopted session's credential again")
	}
	if strings.Contains(string(second.Body), `"token"`) {
		t.Errorf("a second list still carries a token field: %s", second.Body)
	}

	// The credential never reaches the trail, adopted or not (FR-042).
	if strings.Contains(restarted.readTrail(), claimed) {
		t.Error("the adopted session's credential is in the audit trail")
	}

	// And shutdown leaves it standing, which is what makes a restart cheap
	// enough to do (#63). This daemon sets no CRSW_DESTROY_ON_SHUTDOWN, so it
	// gets the default, and the default changed: the session this test adopted
	// once is available to be adopted again.
	//
	// That the teardown path still works when an operator asks for it is
	// TestQuickstartStory5Cap's subject, which opts in — asserted there rather
	// than here so that neither case is quietly proving both.
	if err := restarted.stop(syscall.SIGTERM); err != nil {
		t.Errorf("SIGTERM: %v\n%s", err, restarted.readTrail())
	}
	if !h.hasSession("crswd-" + c.ID) {
		t.Errorf("the adopted session was destroyed by a shutdown that no longer reaps by default")
	}
	if !h.hasSession(lookalike) {
		t.Errorf("shutdown destroyed %s, which the daemon never owned", lookalike)
	}
}

// ---------------------------------------------------------------------------
// Two daemons on one host (#22)
// ---------------------------------------------------------------------------

// The failure this is the guard for: on tmux's shared default server, daemon B
// cannot tell daemon A's sessions from its own — they carry the crswd- prefix
// and @crswd-managed, which is the whole adoption signal — so B adopts them at
// startup and its own graceful shutdown reaps them, with verified teardown,
// exactly as designed. Starting and stopping a dev build alongside the real
// daemon destroyed live unsandboxed work sessions, silently, on both sides.
//
// Started and stopped for real rather than reasoned about, because the argument
// that it cannot happen is precisely the argument that was wrong.
// The name is kept short on purpose: t.TempDir() builds it into TMUX_TMPDIR, and
// the socket path underneath it has to stay inside sun_path's 108 bytes.
func TestQuickstartSecondDaemonSparesTheFirst(t *testing.T) {
	h := newHost(t)

	a := h.start(nil)
	first := a.createSession("held-by-a")
	if !slices.Contains(a.sessionNames(), "crswd-"+first.ID) {
		t.Fatalf("daemon A's own session is not on its server: %v", a.sessionNames())
	}

	// A second daemon, on its own free port — the developer's dev build next to
	// the real one.
	b := h.start(nil)
	// Not fatal: a shared server is the bug, and the assertions below are what
	// say what it costs. Stopping here would report the cause and hide the harm.
	if b.socket == a.socket {
		t.Errorf("both daemons drive %s; a shared tmux server is the bug itself", b.socket)
	}

	// B adopts nothing, because there is nothing of A's it can reach. Asserted
	// on the server and through the API: the record and the session have to
	// agree, or shutdown reaps from one list what the other still holds.
	if got := b.sessionNames(); len(got) != 0 {
		t.Errorf("daemon B's server holds %v, want nothing of daemon A's", got)
	}
	var listed struct {
		Sessions []json.RawMessage `json:"sessions"`
	}
	list := b.call(http.MethodGet, "/sessions", "", "")
	if err := json.Unmarshal(list.Body, &listed); err != nil {
		t.Fatalf("decode daemon B's list: %v: %s", err, list.Body)
	}
	if len(listed.Sessions) != 0 {
		t.Errorf("daemon B adopted %d sessions at startup, want 0: %s", len(listed.Sessions), list.Body)
	}

	// The destructive half: a graceful shutdown, which reaps everything the
	// daemon believes it owns.
	if err := b.stop(syscall.SIGTERM); err != nil {
		t.Errorf("SIGTERM to daemon B: %v\n%s", err, b.readTrail())
	}

	if !slices.Contains(a.sessionNames(), "crswd-"+first.ID) {
		t.Fatalf("daemon B's shutdown destroyed daemon A's session; A's server now holds %v", a.sessionNames())
	}

	// Alive, not merely listed: A's record and the session behind it both
	// survived, so A is not left holding a record for a session that is gone.
	if resp := a.call(http.MethodGet, "/sessions/"+first.ID+"/output", "", first.Token); resp.Status != http.StatusOK {
		t.Errorf("daemon A's session after B came and went = %d, want 200: %s", resp.Status, resp.Body)
	}
}

// ---------------------------------------------------------------------------
// Story 5 (P5) — bounds hold
// ---------------------------------------------------------------------------

func TestQuickstartStory5Cap(t *testing.T) {
	h := newHost(t)
	// The rate is raised so that the cap is what refuses. At the default of six
	// a minute the burst is three (burstFor), so a loop of six creates is
	// refused by the rate limiter on its fourth — the same 429, for a different
	// reason. The two bounds are asserted separately for exactly that reason.
	d := h.start(map[string]string{
		"CRSW_MAX_SESSIONS":        "5",
		"CRSW_CREATE_RATE_PER_MIN": "120",
		// Opted into, because since #63 it is no longer the default: a daemon
		// that stops now leaves its fleet standing and reclaims it on the way
		// back up. This case is the one that still proves verified teardown
		// works when it is asked for — the tear-down code has no other caller
		// in the suite, and a reaper nothing exercises is how this repo has
		// shipped a reaper with no caller before.
		"CRSW_DESTROY_ON_SHUTDOWN": "1",
	})

	for i := 1; i <= 5; i++ {
		c := d.createSession(fmt.Sprintf("cap-%d", i))
		if !h.hasSession("crswd-" + c.ID) {
			t.Errorf("session %d is not on the host", i)
		}
	}

	sixth := d.call(http.MethodPost, "/sessions", fmt.Sprintf(`{"name":"cap-6","work_dir":%q}`, h.workDir), "")
	if sixth.Status != http.StatusTooManyRequests {
		t.Errorf("the sixth create = %d, want 429: %s", sixth.Status, sixth.Body)
	}

	// The first five are unaffected.
	if got := len(h.sessionNames()); got != 5 {
		t.Errorf("the host holds %d sessions, want 5: %v", got, h.sessionNames())
	}
	list := d.call(http.MethodGet, "/sessions", "", "")
	var listed struct {
		Sessions []json.RawMessage `json:"sessions"`
	}
	if err := json.Unmarshal(list.Body, &listed); err != nil {
		t.Fatalf("decode the list: %v", err)
	}
	if len(listed.Sessions) != 5 {
		t.Errorf("the daemon lists %d sessions, want 5", len(listed.Sessions))
	}

	// The trail says which of the two 429s it was, which the caller cannot tell.
	if !strings.Contains(d.readTrail(), "concurrent-session cap") {
		t.Errorf("no cap refusal in the trail:\n%s", d.readTrail())
	}

	// quickstart.md's last block: SIGTERM reaps the fleet with verification.
	if err := d.stop(syscall.SIGTERM); err != nil {
		t.Errorf("SIGTERM: %v\n%s", err, d.readTrail())
	}
	for _, name := range h.sessionNames() {
		if strings.HasPrefix(name, "crswd-") {
			t.Errorf("%s outlived the daemon's shutdown", name)
		}
	}
}

func TestQuickstartStory5RateLimit(t *testing.T) {
	h := newHost(t)
	// Six a minute is the documented default and gives a burst of three.
	d := h.start(map[string]string{"CRSW_CREATE_RATE_PER_MIN": "6", "CRSW_MAX_SESSIONS": "50"})

	// A distinct name per request, because identical bodies inside the same
	// second sign to an identical signature and the replay cache refuses the
	// second one with a 401 — the limiter never gets asked. Layer 2 is stricter
	// than the rate limit and answers first, which is correct and is also why
	// this has to vary the body to reach what it is testing.
	statuses := make([]int, 0, 5)
	for i := 0; i < 5; i++ {
		body := fmt.Sprintf(`{"name":"burst-%d","work_dir":%q}`, i, h.workDir)
		statuses = append(statuses, d.call(http.MethodPost, "/sessions", body, "").Status)
	}

	// Asserted as a shape, not as a count. "At least one 429" was the whole claim
	// here once, and it would have been satisfied by a limiter that refused all
	// five — or by four 500s and a 429 — neither of which is a rate limit. What
	// makes this one is that some creates succeed, the rest are refused, and the
	// refusals come last.
	allowed, limited := 0, 0
	for i, s := range statuses {
		switch s {
		case http.StatusCreated:
			allowed++
			if limited > 0 {
				t.Errorf("create %d succeeded after %d had already been refused: %v — "+
					"a token bucket that refills mid-burst is not bounding anything", i, limited, statuses)
			}
		case http.StatusTooManyRequests:
			limited++
		default:
			t.Errorf("create %d = %d, want 201 or 429: %v", i, s, statuses)
		}
	}
	if allowed == 0 {
		t.Errorf("every create in the burst was refused: %v — that is not a rate limit, it is a wall", statuses)
	}
	if limited == 0 {
		t.Errorf("no create in the burst was refused: %v — the limiter never engaged", statuses)
	}
	t.Logf("burst of five at six a minute: %v (%d allowed, %d limited)", statuses, allowed, limited)

	if !strings.Contains(d.readTrail(), "create rate limit") {
		t.Errorf("no rate-limit refusal in the trail:\n%s", d.readTrail())
	}
}

// ---------------------------------------------------------------------------
// Story 6 (P6) — the audit trail answers "what happened" and leaks nothing
// ---------------------------------------------------------------------------

func TestQuickstartStory6Audit(t *testing.T) {
	h := newHost(t)
	d := h.start(nil)

	c := d.createSession("audited")
	d.waitForPane(c.ID, c.Token, shimReady)

	const canary = "PWNED-CANARY-0f1e2d3c"
	body, err := json.Marshal(map[string]string{"text": "echo " + canary})
	if err != nil {
		t.Fatalf("encode the prompt: %v", err)
	}

	// Every route, and a failure on each shape of refusal.
	calls := []func() response{
		func() response { return d.call(http.MethodPost, "/sessions/"+c.ID+"/prompt", string(body), c.Token) },
		func() response { return d.call(http.MethodGet, "/sessions/"+c.ID+"/output", "", c.Token) },
		func() response { return d.call(http.MethodGet, "/sessions", "", "") },
		func() response { return d.call(http.MethodGet, "/sessions/"+c.ID, "", c.Token) },
		func() response {
			return d.call(http.MethodGet, "/sessions/"+strings.Repeat("0", 32), "", c.Token)
		},
		func() response { return d.call(http.MethodPost, "/sessions", `{"name":"bad:name"}`, "") },
		func() response { return d.call(http.MethodDelete, "/sessions/"+c.ID, "", c.Token) },
	}

	before := len(d.records())
	for _, call := range calls {
		call()
	}

	// One unauthenticated request too: FR-041 counts it like any other.
	unsigned, err := http.NewRequest(http.MethodGet, "http://"+d.addr+"/sessions", nil)
	if err != nil {
		t.Fatalf("build the unsigned request: %v", err)
	}
	d.do(unsigned)

	// The trail is written before the response is, but the write and the read
	// are two processes; wait for the count rather than racing it.
	want := before + len(calls) + 1
	deadline := time.Now().Add(waitBudget)
	var records []auditRecord
	for time.Now().Before(deadline) {
		records = d.records()
		if len(records) >= want {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if got := len(records); got != want {
		t.Errorf("the trail holds %d records, want %d — FR-041 is one per request", got, want)
	}

	seen := map[string]int{}
	for _, rec := range records {
		seen[rec.Action]++
		if rec.Decision != "allow" && rec.Decision != "deny" {
			t.Errorf("record %+v has decision %q", rec, rec.Decision)
		}
		if rec.Caller == "" {
			t.Errorf("record %+v has no caller", rec)
		}
	}
	for _, action := range []string{
		"session.create", "session.prompt", "session.output",
		"session.list", "session.detail", "session.destroy", "auth.reject",
	} {
		if seen[action] == 0 {
			t.Errorf("no %s record in the trail: %v", action, seen)
		}
	}
	t.Logf("actions recorded: %v", seen)

	// SC-013, proved as the negative rather than eyeballed.
	trail := d.readTrail()
	for name, secret := range map[string]string{
		"the prompt text":   canary,
		"the shared secret": h.secret,
		"the bearer token":  c.Token,
		"an ESC byte":       "\x1b",
		"the pane marker":   shimEcho,
	} {
		if strings.Contains(trail, secret) {
			t.Errorf("%s is in the trail", name)
		}
	}
	if strings.Contains(trail, "bad:name") {
		t.Error("the refused session name is in the trail, which is caller-supplied text")
	}
}

// TestQuickstartRefusesWithoutTmux is SC-011, and it is here rather than only in
// internal/config because the unit tests prove the probe is right and this
// proves it is *reached*: a dependency check nothing calls is the failure this
// repo has shipped three times.
//
// PATH is the shim directory alone, so the daemon can see the claude stand-in
// and no tmux at all — which is the host the check exists for, and which
// otherwise only exists on a machine nobody has installed tmux on.
func TestQuickstartRefusesWithoutTmux(t *testing.T) {
	h := newHost(t)
	addr := freePort(t)

	out, code := h.run(map[string]string{"CRSW_LISTEN": addr, "PATH": h.shimDir})
	if code == 0 {
		t.Fatalf("started with no tmux on PATH:\n%s", out)
	}
	// The probe's own sentence, not merely the word "tmux": a daemon with no
	// check at all also exits non-zero here, because reconciliation shells out
	// to tmux and reports the exec failure — which is the failure deferred to
	// the first thing that needs the host rather than a startup probe, and is
	// exactly what this test would otherwise pass on.
	for _, want := range []string{"tmux", "cannot manage a session without it"} {
		if !strings.Contains(out, want) {
			t.Errorf("the refusal does not say %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "reconcile") {
		t.Errorf("the host was asked about its sessions before the probe refused:\n%s", out)
	}

	// Nothing bound: a refusal that had already taken the port would be a
	// refusal the operator has to notice before their next start works.
	if ln, err := net.Listen("tcp", addr); err != nil {
		t.Errorf("%s is still held after the refusal: %v", addr, err)
	} else {
		_ = ln.Close()
	}
}

// ---------------------------------------------------------------------------
// Definition of done
// ---------------------------------------------------------------------------

// TestQuickstartNoDependencies is the last box in quickstart.md's checklist:
// zero third-party dependencies, which is a property of this milestone and not
// an accident of nobody having added one yet (research D7).
func TestQuickstartNoDependencies(t *testing.T) {
	if _, err := os.Stat("../../go.sum"); err == nil {
		t.Error("go.sum exists, so something was imported")
	} else if !os.IsNotExist(err) {
		t.Errorf("stat go.sum: %v", err)
	}

	mod, err := os.ReadFile("../../go.mod")
	if err != nil {
		t.Fatalf("read go.mod: %v", err)
	}
	if bytes.Contains(mod, []byte("require")) {
		t.Errorf("go.mod carries a require block:\n%s", mod)
	}
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

// assertIdentical is FR-011 and FR-033 as an assertion: a set of refusals that
// must be indistinguishable from outside.
func assertIdentical(t *testing.T, what string, fingerprints []string) {
	t.Helper()

	if len(fingerprints) < 2 {
		return
	}
	slices.Sort(fingerprints)

	_, first, _ := strings.Cut(fingerprints[0], "\n")
	for _, other := range fingerprints[1:] {
		name, body, _ := strings.Cut(other, "\n")
		if body != first {
			t.Errorf("%s are not identical; %s differs:\n--- want\n%s\n--- got\n%s", what, name, first, body)
		}
	}
}

func mustRequest(t *testing.T, method, url string) *http.Request {
	t.Helper()

	req, err := http.NewRequest(method, url, nil)
	if err != nil {
		t.Fatalf("build %s %s: %v", method, url, err)
	}
	return req
}

func withSignature(req *http.Request, sig string) *http.Request {
	req.Header.Set("X-CRSW-Signature", sig)
	return req
}

func withTimestamp(req *http.Request, ts string) *http.Request {
	req.Header.Set("X-CRSW-Timestamp", ts)
	return req
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(s), "\n")
	return line
}

// errorsAs is errors.As, kept local so the import list stays the acceptance
// run's own.
func errorsAs(err error, target **exec.ExitError) bool {
	e, ok := err.(*exec.ExitError)
	if ok {
		*target = e
	}
	return ok
}
