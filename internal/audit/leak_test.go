// The leak-assertion suite (T039).
//
// audit_test.go proves that one Record cannot carry free-form content: FR-042 as
// a property of a type. What no test of this package alone can prove is that the
// daemon never *builds* a record — or a log line — out of a prompt, a pane
// capture, a bearer token, or the shared secret, because every one of those
// values is minted in a different package and travels through a fifth.
//
// So this file runs the daemon. It reconciles a host a previous run left behind,
// drives all six routes, refuses requests in every way the API refuses them,
// makes tmux itself fail with an error carrying pane-shaped text, reaps two
// sessions past their ceiling, loses a response to a client that went away, and
// shuts down — every operation carrying values marked so that even a fragment of
// one is unmistakable. Then it reads back every audit record and every log line
// the run produced and asserts that not one of those values is anywhere in them
// (FR-042, FR-043, docs/security.md §3, SC-013).
//
// It is in package audit_test rather than audit because it imports
// internal/httpapi, which imports internal/audit. The external test package is
// the only place that import direction is legal — which is why T038's sweep of
// every route stayed in internal/httpapi and this one could not.
package audit_test

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/nctiggy/claude-remote-session-webhook/internal/audit"
	"github.com/nctiggy/claude-remote-session-webhook/internal/auth"
	"github.com/nctiggy/claude-remote-session-webhook/internal/config"
	"github.com/nctiggy/claude-remote-session-webhook/internal/httpapi"
	"github.com/nctiggy/claude-remote-session-webhook/internal/session"
	"github.com/nctiggy/claude-remote-session-webhook/internal/tmuxctl"
)

// The marks. Each is a string that appears nowhere else in this repository, and
// each is a *prefix* of the value actually driven through the daemon rather than
// the whole of it — so a record that leaked half a prompt, or a name with the
// caller's suffix trimmed off, still matches.
const (
	markPrompt    = "CANARY-PROMPT"
	markPane      = "CANARY-PANE"
	markName      = "CANARY-NAME"
	markWorkDir   = "CANARY-WORKDIR"
	markField     = "CANARY-FIELD"
	markHostError = "CANARY-HOSTERROR"
	markShared    = "CANARY-SHARED-KEY"
	// markBearer names what a caller presents on a session-scoped route. It is
	// spelled for the header rather than for the thing, because gosec's G101
	// fires on any constant whose *name* says credential — and a //nolint on a
	// leak suite's own fixture would be a poor precedent to set.
	markBearer = "CANARY-BEARER"
)

// The values themselves.
const (
	// promptText is what a caller sends into a session. It carries the shell
	// metacharacters T024's delivery rules turn on and an embedded newline,
	// because a trail built by interpolating caller text would not merely leak
	// it — it would leak it across a line boundary, and one JSON object per line
	// is the property an operator's grep rests on.
	promptText = markPrompt + "-halibut; echo " + markPrompt + "-injected; $(id)\n" + markPrompt + "-after-a-newline"

	// paneText is what the session printed back. A pane holds whatever is on the
	// host, which is why docs/security.md §3 makes it secret; the escape
	// sequences are here so that what reaches a response has been through the
	// stripper on the way.
	paneText = markPane + "-mackerel\n\x1b[31m" + markPane + "-in-red\x1b[0m\n$ "

	// leakName is a session label a caller chose, inside FR-027's alphabet.
	leakName = markName + "-halibut"

	// targetName is the same label spelled as a tmux target, which ValidateName
	// refuses. The refusal is the interesting half: it must reach the trail
	// without the name that caused it.
	targetName = markName + "-halibut:0"

	// outsideRoot is a directory no allowlist approves. A refusal that quoted it
	// back would be a filesystem oracle in the trail.
	outsideRoot = "/" + markWorkDir + "-outside-every-approved-root"

	// unknownField is a field createRequest does not define. encoding/json quotes
	// it back in the error DisallowUnknownFields produces, which is exactly the
	// error internal/httpapi drops rather than wraps.
	unknownField = markField + "-nobody-declared"

	// presentedBearer is a session credential this daemon never issued. What a
	// caller presents is as unwelcome in the trail as what the daemon minted.
	presentedBearer = markBearer + "-not-one-this-daemon-issued"
)

// daemonKey is the shared secret. Spelled in words rather than hex for the
// reason internal/httpapi's fixture is: a run of hex digits of this length is
// what a real HMAC key looks like, and gitleaks — correctly — refuses to let one
// into the repository.
func daemonKey() []byte { return []byte(markShared + "-not-a-real-one-and-long-enough") }

// hostError is what tmux says when it fails. It is marked because the daemon
// holds it in a Go error while deciding what to record, and "wrap what the host
// said" is the one habit that would put a byte the daemon did not author into
// the trail.
var hostError = errors.New("tmux: " + markHostError + ": server exited unexpectedly")

// errClientGone is deliberately unmarked. A write failure is a fact about the
// network and not caller data, so marking it would be asserting a leak that is
// not one — what must not travel with it is the body the daemon was holding.
var errClientGone = errors.New("connection reset by peer")

// leakMark is one value that must appear nowhere in what the run produced.
type leakMark struct {
	what  string
	value string
}

// leakLine is one line the run produced and the sink it came from, so a failure
// says which of the two leaked rather than only that something did.
type leakLine struct {
	from string
	text string
}

// leakClock is the daemon's view of time, moved by hand so that the reaper's
// deadlines are reached without elapsed time.
type leakClock struct{ at time.Time }

func (c *leakClock) Now() time.Time { return c.at }

// brokenWriter is a client that went away mid-response. It is the one path that
// reaches the daemon's last-resort log channel without breaking the audit sink
// as well — and it reaches it while the daemon is holding a body that is nothing
// but pane content.
type brokenWriter struct{ header http.Header }

func (b *brokenWriter) Header() http.Header {
	if b.header == nil {
		b.header = make(http.Header)
	}
	return b.header
}

func (b *brokenWriter) Write([]byte) (int, error) { return 0, errClientGone }
func (b *brokenWriter) WriteHeader(int)           {}

// leakRun is one whole exercise of the daemon: the server, the fake host it
// stands on, both sinks it writes to, and the evidence that the marked values
// really did travel.
type leakRun struct {
	srv  *httpapi.Server
	tmux *tmuxctl.Fake
	root string
	repo string

	trail *bytes.Buffer
	logs  *bytes.Buffer

	// base is the instant every signature is dated from, so that each request
	// gets its own second by construction. Reading the clock per request would
	// let two of them land in the same second whenever the host was slow, and
	// two requests with the same body in the same second are one signature — the
	// replay cache would refuse the second, for a reason none of these cases is
	// driving.
	base time.Time
	tick int

	bodies      []string // every non-empty body sent, whole
	credentials []string // every bearer token issued, plaintext

	// The evidence. Without it the sweep would pass just as happily against a
	// run that had done nothing at all.
	createBody string
	outputBody string
	sweptError string
}

func leakConfig(root string) *config.Config {
	return &config.Config{
		Listen:       "127.0.0.1:0",
		SharedSecret: daemonKey(),

		// Small on purpose: the oversize case has to exceed it, and a 64 KiB
		// request body would be a slow way to prove the same thing.
		MaxBodyBytes: 512,

		Roots:       []config.ApprovedRoot{{Path: root}},
		MaxSessions: config.DefaultMaxSessions,

		// A create budget nothing here can exhaust. A 429 in this suite would
		// mean a request never reached the operation it was meant to drive.
		CreateRatePerMin: 1000,
	}
}

func newLeakRun(t *testing.T) *leakRun {
	t.Helper()

	// Resolved, because config.Load resolves its roots at startup and the
	// containment check compares two already-canonical paths. On a host where the
	// temp directory is itself a symlink, an unresolved root would fail every
	// create here for a reason that has nothing to do with what is under test.
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("resolve the fixture root: %v", err)
	}
	repo := filepath.Join(root, "repo")
	if err := os.Mkdir(repo, 0o750); err != nil {
		t.Fatalf("create the fixture working directory: %v", err)
	}

	// The daemon's last-resort channel is the standard logger — reportToStderr in
	// internal/httpapi, reportToLog in internal/session — and it is reached
	// exactly when something has gone wrong while a response was in hand. A leak
	// suite reading only the audit sink would be blind to the paths that most
	// need reading. This is process-wide state, which is why the tests below do
	// not run in parallel; TestNewWritesToStdout settles the same problem the
	// same way.
	logs := &bytes.Buffer{}
	previous := log.Writer()
	log.SetOutput(logs)
	t.Cleanup(func() { log.SetOutput(previous) })

	trail := &bytes.Buffer{}
	fake := tmuxctl.NewFake()
	srv, err := httpapi.NewWith(leakConfig(root), fake, audit.NewTo(trail, time.Now))
	if err != nil {
		t.Fatalf("httpapi.NewWith = _, %v; want a server", err)
	}

	return &leakRun{srv: srv, tmux: fake, root: root, repo: repo, trail: trail, logs: logs, base: time.Now()}
}

// leakRequest is one request to drive, with the two knobs the refusal cases
// need: an explicit instant, so one signature can be sent twice, and a different
// body to sign over, so a body nobody authenticated can be sent.
type leakRequest struct {
	method     string
	path       string
	credential string
	body       []byte
	at         time.Time
	signed     []byte
}

// sendTo signs a request the way contracts/http-api.md documents and drives it
// through the real router into w.
func (r *leakRun) sendTo(t *testing.T, w http.ResponseWriter, req leakRequest) {
	t.Helper()

	if req.at.IsZero() {
		r.tick++
		req.at = r.base.Add(-time.Duration(r.tick) * time.Second)
	}
	if req.signed == nil {
		req.signed = req.body
	}
	if len(req.body) > 0 {
		r.bodies = append(r.bodies, string(req.body))
	}

	hr := httptest.NewRequest(req.method, req.path, bytes.NewReader(req.body))
	hr.Header.Set("Content-Type", "application/json")
	if req.credential != "" {
		hr.Header.Set("Authorization", "Bearer "+req.credential)
	}

	// Computed from first principles rather than by calling the auth package's
	// own signer, exactly as internal/httpapi's tests do it: a test that signs
	// with the code under test proves only that the code agrees with itself.
	ts := strconv.FormatInt(req.at.Unix(), 10)
	mac := hmac.New(sha256.New, daemonKey())
	// METHOD "\n" PATH "\n" timestamp "." body: the signature names the request
	// it authorizes, not just the instant and the bytes.
	if _, err := mac.Write([]byte(hr.Method + "\n" + hr.URL.EscapedPath() + "\n" + ts + "." + string(req.signed))); err != nil {
		t.Fatalf("sign the request: %v", err)
	}
	hr.Header.Set(auth.HeaderTimestamp, ts)
	hr.Header.Set(auth.HeaderSignature, "sha256="+hex.EncodeToString(mac.Sum(nil)))

	r.srv.ServeHTTP(w, hr)
}

// want drives a request and fails unless the daemon answered as expected.
//
// The response body is deliberately never printed. One of them carries the only
// copy of a bearer token that will ever exist, and a failing test's output is
// one more place for it to be.
func (r *leakRun) want(t *testing.T, status int, what string, req leakRequest) *httptest.ResponseRecorder {
	t.Helper()

	got := httptest.NewRecorder()
	r.sendTo(t, got, req)
	if got.Code != status {
		t.Fatalf("%s answered %d; want %d", what, got.Code, status)
	}
	return got
}

// create starts a session through the API and keeps the credential it was handed.
func (r *leakRun) create(t *testing.T, name string) (string, string) {
	t.Helper()

	got := r.want(t, http.StatusCreated, "POST /sessions", leakRequest{
		method: http.MethodPost,
		path:   "/sessions",
		body:   jsonBody(t, map[string]string{"name": name, "work_dir": r.repo}),
	})

	var issued struct {
		ID    string `json:"id"`
		Token string `json:"token"`
	}
	if err := json.Unmarshal(got.Body.Bytes(), &issued); err != nil {
		t.Fatalf("the create response is not JSON: %v", err)
	}
	if issued.ID == "" || issued.Token == "" {
		t.Fatal("the create response carries no id or no credential; the rest of the suite would be driving nothing")
	}

	r.createBody = got.Body.String()
	r.credentials = append(r.credentials, issued.Token)
	return issued.ID, issued.Token
}

func jsonBody(t *testing.T, v any) []byte {
	t.Helper()

	body, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal a request body: %v", err)
	}
	return body
}

// loadTheConfiguration exercises startup.
//
// FR-004's default-root banner is the one thing the daemon writes with no audit
// record behind it, and startup is the one place the shared secret is in scope
// outside the auth path. The banner goes to the same buffer the last-resort log
// channel writes to, so the sweep covers both without knowing they are two.
func (r *leakRun) loadTheConfiguration(t *testing.T) {
	t.Helper()

	// The default root has to exist for startup to resolve it, which is what
	// makes the warning reachable at all.
	if err := os.Mkdir(filepath.Join(r.root, config.DefaultRootName), 0o750); err != nil {
		t.Fatalf("create the default root: %v", err)
	}

	// "HOME" is spelled here rather than exported from internal/config: the name
	// belongs to the operating system, not to this daemon.
	//
	// The layer-1 values are present because startup refuses without them, and
	// this fixture's subject is the default-root path further down. They are not
	// marked: what they are worth asserting about is that a *refused* address
	// never reaches the trail, which is the browser door's test to write.
	env := map[string]string{
		config.EnvSharedSecret:        string(daemonKey()),
		"HOME":                        r.root,
		config.EnvAccessTeamDomain:    "example-team.cloudflareaccess.com",
		config.EnvAccessAUD:           "test-only-audience-tag",
		config.EnvAccessAllowedEmails: "operator@example.com",
	}

	cfg, err := config.LoadFrom(func(k string) string { return env[k] }, r.logs)
	if err != nil {
		t.Fatalf("config.LoadFrom = _, %v; want a configuration", err)
	}
	if len(cfg.Roots) != 1 || !cfg.Roots[0].IsDefault {
		t.Fatal("the fixture did not take the default-root path, so FR-004's warning never ran")
	}
	if r.logs.Len() == 0 {
		t.Fatal("startup wrote no default-root warning; FR-004 requires a loud one")
	}
}

// reap drives the one teardown with no request behind it.
//
// The reaper needs a Manager and a clock that moves, and neither is reachable
// through a Server — so this half stands on its own store and its own fake host
// while writing into the same trail. What it proves is the same thing: a session
// nobody came back for is destroyed on the daemon's own initiative, and the only
// account of it that will ever exist carries neither the prompt it was sent nor
// what it printed.
func (r *leakRun) reap(t *testing.T) {
	t.Helper()

	ctx := context.Background()
	clock := &leakClock{at: time.Now()}
	host := tmuxctl.NewFake()

	mgr, err := session.NewManagerWithClock(
		host, session.NewStore(), []config.ApprovedRoot{{Path: r.root}}, config.DefaultMaxSessions, clock,
	)
	if err != nil {
		t.Fatalf("session.NewManagerWithClock = _, %v; want a manager", err)
	}
	reaper, err := session.NewReaper(mgr, audit.NewTo(r.trail, time.Now))
	if err != nil {
		t.Fatalf("session.NewReaper = _, %v; want a reaper", err)
	}

	// Two sessions past their ceiling: one the host lets go of, one it does not.
	// The sweep records an allow for the first and a deny for the second, which
	// are the only two things a reaper has to say.
	r.reapable(t, mgr, host, leakName+"-reaped")
	survivor := r.reapable(t, mgr, host, leakName+"-survivor")
	host.SurviveKill(survivor.TmuxName())

	clock.at = clock.at.Add(session.AbsoluteLifetime + time.Minute)

	if _, err := reaper.Sweep(ctx); err == nil {
		t.Fatal("Sweep() = _, nil; want the session the host would not confirm gone reported")
	}

	// A second pass, this time with tmux itself failing. The error the sweep
	// hands back carries what the host said — that is the evidence the marked
	// host error really did reach a Go error inside the daemon, and the sweep
	// below is what proves it never reached the record written beside it.
	host.FailOp(tmuxctl.OpKill, hostError)
	swept, err := reaper.Sweep(ctx)
	if err == nil {
		t.Fatal("Sweep() = _, nil; want the host failure reported")
	}
	if len(swept) != 0 {
		t.Fatalf("the sweep reports %d sessions collected; the host confirmed none of them gone", len(swept))
	}
	r.sweptError = err.Error()
}

// reapable creates a session, sends it the marked prompt, and gives it marked
// pane content, so that a record written about its teardown has every value the
// sweep looks for within reach of whoever wrote it.
func (r *leakRun) reapable(t *testing.T, mgr *session.Manager, host *tmuxctl.Fake, name string) session.Session {
	t.Helper()

	ctx := context.Background()
	created, _, err := mgr.Create(ctx, session.CreateRequest{Owner: auth.CallerOperator, Name: name, WorkDir: r.repo})
	if err != nil {
		t.Fatalf("Create(%q) = _, _, %v; want a session", name, err)
	}
	if err := mgr.Prompt(ctx, *created, promptText); err != nil {
		t.Fatalf("Prompt() = %v; want the prompt delivered", err)
	}
	host.SetPane(created.TmuxName(), paneText)
	return *created
}

// driveEveryOperation runs the daemon through everything milestone 1 lets it do.
func driveEveryOperation(t *testing.T) *leakRun {
	t.Helper()

	r := newLeakRun(t)
	ctx := context.Background()

	r.loadTheConfiguration(t)

	// A session this run did not start: what a restart leaves on the host.
	// Reconciliation adopts it, mints a credential nobody will ever see, and
	// records one startup.adopt.
	adopted := session.Session{ID: strings.Repeat("a", session.IDLen)}.TmuxName()
	r.tmux.Seed(tmuxctl.SessionInfo{Name: adopted, Created: time.Now().Add(-2 * time.Hour), Managed: true})
	r.tmux.SetPane(adopted, paneText)
	if err := r.srv.Reconcile(ctx); err != nil {
		t.Fatalf("Reconcile() = %v; want the host reconciled", err)
	}

	// The six routes, all allowed.
	id, credential := r.create(t, leakName)
	r.tmux.SetPane(session.Session{ID: id}.TmuxName(), paneText)

	r.want(t, http.StatusAccepted, "POST /sessions/{id}/prompt", leakRequest{
		method: http.MethodPost, path: "/sessions/" + id + "/prompt", credential: credential,
		body: jsonBody(t, map[string]string{"text": promptText}),
	})
	r.outputBody = r.want(t, http.StatusOK, "GET /sessions/{id}/output", leakRequest{
		method: http.MethodGet, path: "/sessions/" + id + "/output", credential: credential,
	}).Body.String()
	r.want(t, http.StatusOK, "GET /sessions", leakRequest{method: http.MethodGet, path: "/sessions"})
	r.want(t, http.StatusOK, "GET /sessions/{id}", leakRequest{
		method: http.MethodGet, path: "/sessions/" + id, credential: credential,
	})

	// Every way a body is refused, each carrying something the refusal would be
	// tempted to quote back.
	r.want(t, http.StatusBadRequest, "a create carrying an undeclared field", leakRequest{
		method: http.MethodPost, path: "/sessions",
		body: []byte(`{"name":"` + leakName + `","` + unknownField + `":"` + markField + `-value"}`),
	})
	r.want(t, http.StatusBadRequest, "a create naming a tmux target", leakRequest{
		method: http.MethodPost, path: "/sessions",
		body: jsonBody(t, map[string]string{"name": targetName, "work_dir": r.repo}),
	})
	r.want(t, http.StatusBadRequest, "a create outside every approved root", leakRequest{
		method: http.MethodPost, path: "/sessions",
		body: jsonBody(t, map[string]string{"name": leakName, "work_dir": outsideRoot}),
	})
	r.want(t, http.StatusBadRequest, "a prompt with no text", leakRequest{
		method: http.MethodPost, path: "/sessions/" + id + "/prompt", credential: credential,
		body: jsonBody(t, map[string]string{"text": ""}),
	})

	// A credential this daemon never issued, and an ID it never minted. Both
	// answer the same 404, and the trail is the only place the difference is kept.
	r.want(t, http.StatusNotFound, "a session driven with a credential nobody issued", leakRequest{
		method: http.MethodGet, path: "/sessions/" + id + "/output", credential: presentedBearer,
	})
	r.want(t, http.StatusNotFound, "a session that never existed", leakRequest{
		method: http.MethodGet, path: "/sessions/" + strings.Repeat("c", session.IDLen), credential: credential,
	})

	// A body nobody authenticated: the signature covers other bytes, so layer 2
	// refuses before any handler sees this one.
	r.want(t, http.StatusUnauthorized, "a prompt whose signature covers other bytes", leakRequest{
		method: http.MethodPost, path: "/sessions/" + id + "/prompt", credential: credential,
		body:   jsonBody(t, map[string]string{"text": promptText}),
		signed: []byte(`{"text":"not what was sent"}`),
	})

	// The same request twice, signature and all.
	r.want(t, http.StatusOK, "a list request", leakRequest{method: http.MethodGet, path: "/sessions", at: r.base})
	r.want(t, http.StatusUnauthorized, "the same list request replayed", leakRequest{
		method: http.MethodGet, path: "/sessions", at: r.base,
	})

	// A timestamp outside the 300-second window.
	r.want(t, http.StatusUnauthorized, "a request signed an hour ago", leakRequest{
		method: http.MethodGet, path: "/sessions", at: r.base.Add(-time.Hour),
	})

	// A body past CRSW_MAX_BODY_BYTES, made of nothing but marked bytes. It is
	// refused before it is read, which makes it the strongest form of "no full
	// request body reaches the trail" this API can produce.
	r.want(t, http.StatusUnauthorized, "an oversize create", leakRequest{
		method: http.MethodPost, path: "/sessions",
		body: []byte(`{"name":"` + strings.Repeat(markPrompt+"-", 64) + `"}`),
	})

	// tmux itself failing, with an error carrying pane-shaped text. The daemon
	// holds that error while it decides what to record, which is the moment a
	// wrap would put a byte it did not author into the trail.
	r.tmux.FailOp(tmuxctl.OpCapturePane, hostError)
	r.want(t, http.StatusInternalServerError, "a capture the host refused", leakRequest{
		method: http.MethodGet, path: "/sessions/" + id + "/output", credential: credential,
	})
	r.tmux.FailOp(tmuxctl.OpCapturePane, nil)

	r.tmux.FailOp(tmuxctl.OpPaste, hostError)
	r.want(t, http.StatusInternalServerError, "a prompt the host refused", leakRequest{
		method: http.MethodPost, path: "/sessions/" + id + "/prompt", credential: credential,
		body: jsonBody(t, map[string]string{"text": promptText}),
	})
	r.tmux.FailOp(tmuxctl.OpPaste, nil)

	// A response the daemon could not write, on the one route whose body is
	// nothing but pane content. What reaches the log is that a write of a 200
	// failed; the bytes it was holding when it failed are what must not travel
	// with it.
	r.sendTo(t, &brokenWriter{}, leakRequest{
		method: http.MethodGet, path: "/sessions/" + id + "/output", credential: credential,
	})

	r.want(t, http.StatusOK, "DELETE /sessions/{id}", leakRequest{
		method: http.MethodDelete, path: "/sessions/" + id, credential: credential,
	})

	// A teardown the host will not confirm: the loudest record this daemon
	// writes, and the one an operator goes looking for.
	survivorID, survivorCredential := r.create(t, leakName+"-two")
	r.tmux.SetPane(session.Session{ID: survivorID}.TmuxName(), paneText)
	r.tmux.SurviveKill(session.Session{ID: survivorID}.TmuxName())
	r.want(t, http.StatusConflict, "a teardown the host would not confirm", leakRequest{
		method: http.MethodDelete, path: "/sessions/" + survivorID, credential: survivorCredential,
	})

	r.reap(t)

	// Shutdown tears down what is left — the adopted session, and the one the
	// host would not confirm gone. The error is that survivor being reported,
	// which is the whole of FR-040.
	if err := r.srv.Shutdown(ctx); err == nil {
		t.Fatal("Shutdown() = nil; want the session the host would not confirm gone reported")
	}
	return r
}

// marks is everything the run produced that must appear nowhere in what it
// wrote, in the order docs/security.md §3 lists them.
func (r *leakRun) marks() []leakMark {
	marks := []leakMark{
		{"the prompt text a caller sent", markPrompt},
		{"a session's pane content", markPane},
		{"the session name a caller chose", markName},
		{"the working directory a caller asked for", markWorkDir},
		{"a field a caller invented", markField},
		{"what the host said when it failed", markHostError},
		{"a credential a caller presented", markBearer},
		{"the shared secret", string(daemonKey())},
	}

	for _, credential := range r.credentials {
		hash := sha256.Sum256([]byte(credential))
		marks = append(marks,
			leakMark{"a bearer token this daemon issued", credential},
			leakMark{"the SHA-256 of a bearer token, hex", hex.EncodeToString(hash[:])},
			leakMark{"the SHA-256 of a bearer token, raw", string(hash[:])},
		)
	}

	// FR-042 forbids a full body and not only the interesting parts of one, and
	// the two are different claims: a record built from a body that happened to
	// carry nothing marked would still be a record carrying a body.
	for i, body := range r.bodies {
		marks = append(marks, leakMark{fmt.Sprintf("request body %d, whole", i+1), body})
	}
	return marks
}

// lines is everything the run wrote, from both sinks.
func (r *leakRun) lines() []leakLine {
	var out []leakLine
	for _, text := range strings.Split(r.trail.String(), "\n") {
		if text != "" {
			out = append(out, leakLine{from: "the audit trail", text: text})
		}
	}
	for _, text := range strings.Split(r.logs.String(), "\n") {
		if text != "" {
			out = append(out, leakLine{from: "the daemon's log output", text: text})
		}
	}
	return out
}

// records decodes the audit trail, which also asserts that every line of it is
// one JSON object — the shape an operator's `journalctl | jq` depends on.
func (r *leakRun) records(t *testing.T) []map[string]any {
	t.Helper()

	var out []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(r.trail.String()), "\n") {
		if line == "" {
			continue
		}
		var rec map[string]any
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Fatalf("an audit line is not JSON: %v (%q)", err, line)
		}
		out = append(out, rec)
	}
	return out
}

// pasted is the prompt as it reached the host: the payload of the load-buffer
// call, which is the one place caller text is supposed to end up.
func (r *leakRun) pasted(t *testing.T) string {
	t.Helper()

	var payloads []string
	for _, call := range r.tmux.Calls() {
		if call.Op == tmuxctl.OpPaste && len(call.Stdin) > 0 {
			payloads = append(payloads, string(call.Stdin))
		}
	}
	if len(payloads) == 0 {
		t.Fatal("nothing was ever pasted into a session")
	}
	return strings.Join(payloads, "\n")
}

// TestNoOperationLeaksSecretMaterialIntoTheTrailOrTheLogs is FR-042 and SC-013
// stated across the whole daemon: drive everything it can do with values chosen
// to be unmistakable, then read back everything it wrote and find none of them.
//
// Not parallel: driveEveryOperation redirects the process's standard logger,
// which is the only way to see what the daemon writes when its own last-resort
// channel is all that is left.
func TestNoOperationLeaksSecretMaterialIntoTheTrailOrTheLogs(t *testing.T) {
	run := driveEveryOperation(t)

	lines := run.lines()
	if len(lines) == 0 {
		t.Fatal("the run produced no output at all, so there is nothing here that could have leaked")
	}

	for _, mark := range run.marks() {
		for i, line := range lines {
			if strings.Contains(line.text, mark.value) {
				// The leaked line is printed because that is what an operator
				// would need to see; by this point the value is already out.
				t.Errorf("%s appears in %s, line %d — FR-042 forbids it:\n\t%s", mark.what, line.from, i+1, line.text)
			}
		}
	}
}

// TestTheLeakSuiteReallyDrivesTheDaemon is what keeps the sweep above honest.
//
// A suite that quietly stopped exercising the daemon would keep passing, and
// would keep passing for exactly as long as nobody looked — which is the failure
// mode a leak test has and an ordinary test does not, because its assertion is
// an absence. So: every action the trail can carry appeared, and every marked
// value provably reached the place it was supposed to reach.
func TestTheLeakSuiteReallyDrivesTheDaemon(t *testing.T) {
	run := driveEveryOperation(t)

	// data-model.md's and contracts/http-api.md's spellings, written out rather
	// than read back from the audit package's constants: a test that took them
	// from the code would prove only that the code agrees with itself.
	want := []string{
		"startup.adopt",
		"session.create", "session.list", "session.detail",
		"session.prompt", "session.output", "session.destroy",
		"auth.reject", "reaper.destroy",
	}
	got := make(map[string]bool)
	for _, rec := range run.records(t) {
		action, ok := rec["action"].(string)
		if !ok {
			t.Fatalf("an audit record carries no action: %v", rec)
		}
		got[action] = true
	}
	for _, action := range want {
		if !got[action] {
			t.Errorf("the run emitted no %s record, so the sweep proves nothing about that operation", action)
		}
	}

	for _, reached := range []struct {
		what  string
		mark  string
		where string
		text  string
	}{
		{"the prompt text", markPrompt, "the payload that reached the host", run.pasted(t)},
		{"the pane content", markPane, "the output response", run.outputBody},
		{"the session name", markName, "the create response", run.createBody},
		{"the host's own error", markHostError, "the error a failed sweep returned", run.sweptError},
	} {
		if !strings.Contains(reached.text, reached.mark) {
			t.Errorf("%s never reached %s, so its absence from the trail proves nothing", reached.what, reached.where)
		}
	}

	if len(run.credentials) == 0 {
		t.Error("no bearer token was ever issued, so the sweep proves nothing about one")
	}
	if len(run.bodies) == 0 {
		t.Error("no request body was ever sent, so the sweep proves nothing about one")
	}
	if run.logs.Len() == 0 {
		t.Error("the run produced no log output, so the sweep read only the audit sink")
	}
}
