// Internal test, matching the rest of the package. Three of the properties this
// task turns on are only reachable from inside: the reason each refusal is
// recorded under, the token hash the store kept for a token only the response
// carries, and the record that survives a create whose teardown could not be
// confirmed.
package httpapi

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/nctiggy/claude-remote-session-webhook/internal/audit"
	"github.com/nctiggy/claude-remote-session-webhook/internal/auth"
	"github.com/nctiggy/claude-remote-session-webhook/internal/config"
	"github.com/nctiggy/claude-remote-session-webhook/internal/session"
	"github.com/nctiggy/claude-remote-session-webhook/internal/tmuxctl"
)

// sessionFixture is the session half of a test server: the in-memory tmux fake,
// the store behind the manager, and one approved root that really exists.
//
// The filesystem half is not avoidable. ResolveWorkDir asks EvalSymlinks real
// questions — which is the whole of FR-028's symlink-escape rule — so a create
// tested against a fake filesystem would be testing nothing the daemon does.
type sessionFixture struct {
	mgr   *session.Manager
	tmux  *tmuxctl.Fake
	store *session.Store

	// root is the approved root; repo is an ordinary directory inside it, which
	// is what a well-formed create asks for.
	root string
	repo string
}

func newSessionFixture(t *testing.T) sessionFixture {
	t.Helper()

	// Resolved, because config.Load resolves its roots at startup and the
	// containment check compares two already-canonical paths. On a host where
	// the temp directory is itself a symlink, an unresolved root would make
	// every create in this file fail for a reason that has nothing to do with
	// the code under test.
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("resolve the fixture root: %v", err)
	}

	repo := filepath.Join(root, "repo")
	if err := os.Mkdir(repo, 0o750); err != nil {
		t.Fatalf("create the fixture working directory: %v", err)
	}

	fake := tmuxctl.NewFake()
	store := session.NewStore()
	// The production default, not a number chosen here: quickstart.md's cap check
	// is written against CRSW_MAX_SESSIONS=5, and a fixture with a cap of its own
	// would make the 429 test assert something no operator will ever run.
	mgr, err := session.NewManagerWithClock(
		fake, store, []config.ApprovedRoot{{Path: root}}, config.DefaultMaxSessions, fixedClock{at: testTime},
	)
	if err != nil {
		t.Fatalf("session.NewManagerWithClock = _, %v; want a manager", err)
	}

	return sessionFixture{mgr: mgr, tmux: fake, store: store, root: root, repo: repo}
}

// plant puts a session straight into the store and returns it with the only copy
// of its bearer credential, filling in whatever the caller left unset.
//
// It goes around Manager.Create on purpose. What the layer-3 tests need is a
// record in a particular *shape* — owned by someone else, created 25 hours ago,
// already dead — and a create can produce none of those: it stamps the manager's
// clock, takes its owner from the caller, and always starts a session running.
// Driving the API for the healthy case as well would also spend a signature per
// fixture, which the replay cache counts.
func (f sessionFixture) plant(t *testing.T, s session.Session) (session.Session, string) {
	t.Helper()

	// Through the real generator, so the record carries a hash of exactly what a
	// caller would have been handed and the compare under test is the real one.
	issued, hash, err := session.NewToken()
	if err != nil {
		t.Fatalf("session.NewToken = _, _, %v; want a credential", err)
	}
	s.TokenHash = hash

	if s.ID == "" {
		id, err := session.NewID()
		if err != nil {
			t.Fatalf("session.NewID = _, %v; want an id", err)
		}
		s.ID = id
	}
	if s.Owner == "" {
		s.Owner = auth.CallerOperator
	}
	if s.State == "" {
		s.State = session.StateStarting
	}
	if s.CreatedAt.IsZero() {
		s.CreatedAt = testTime
	}
	if s.LastActivity.IsZero() {
		s.LastActivity = s.CreatedAt
	}

	if err := f.store.Add(s); err != nil {
		t.Fatalf("plant session %s: %v", s.ID, err)
	}

	// The tmux session the record names exists too, seeded rather than created
	// so no call is recorded and an argv assertion still sees only what the
	// request caused. A planted record whose host session is missing is a real
	// case — a session that died on its own — but it is not the one most tests
	// mean, and before T024 nothing noticed the difference because no handler
	// touched tmux per request.
	f.tmux.Seed(tmuxctl.SessionInfo{Name: s.TmuxName(), Created: s.CreatedAt, Managed: true})

	return s, issued
}

// createBody is the well-formed create every test here starts from, varied per
// case by the callers that need to.
func createBody(f sessionFixture) []byte {
	return []byte(`{"name":"refactor-auth","work_dir":"` + f.repo + `"}`)
}

// promptBody is the well-formed prompt every test here starts from. The text is
// deliberately unremarkable; the hostile payloads live in their own table.
func promptBody() []byte { return []byte(`{"text":"run the tests"}`) }

// bodyFor is what a route sweep should send to reach a route's handler: a real
// body for the two routes that take one, nothing for the four that do not. A
// sweep that sent nothing to a route with a required body would be stopped by
// the decoder with a 400 and would prove only that.
func bodyFor(f sessionFixture, route Route) []byte {
	switch route {
	case Route{Method: http.MethodPost, Pattern: "/sessions"}:
		return createBody(f)
	case Route{Method: http.MethodPost, Pattern: "/sessions/{id}/prompt"}:
		return promptBody()
	}
	return nil
}

// reachedStatus is what each route answers once a signed request carrying
// bodyFor's body has got past layer 2.
//
// It is a literal table rather than one constant because the six do not answer
// alike: T022, T024, T025, T026, T027, and T029 gave all of them handlers, so
// they answer 201, 202, and 200 rather than one status. What the sweeps assert
// through it is unchanged — the request reached a handler, which is only
// possible through the middleware.
var reachedStatus = map[Route]int{
	{Method: http.MethodPost, Pattern: "/sessions"}:             http.StatusCreated,
	{Method: http.MethodGet, Pattern: "/sessions"}:              http.StatusOK,
	{Method: http.MethodGet, Pattern: "/sessions/{id}"}:         http.StatusOK,
	{Method: http.MethodDelete, Pattern: "/sessions/{id}"}:      http.StatusOK,
	{Method: http.MethodPost, Pattern: "/sessions/{id}/prompt"}: http.StatusAccepted,
	{Method: http.MethodGet, Pattern: "/sessions/{id}/output"}:  http.StatusOK,
}

// created is one drive of POST /sessions through the whole stack — signature,
// middleware, decoder, manager, tmux fake — because every claim T022 makes is
// about a request and not about a function.
type created struct {
	answer *httptest.ResponseRecorder
	body   map[string]any
}

func postSessions(t *testing.T, s *testServer, body []byte) created {
	t.Helper()
	return postSessionsAt(t, s, body, testTime)
}

// postSessionsAt is postSessions with the signing instant chosen, for the tests
// that drive several creates through one server. The signature covers the
// timestamp and the body and nothing else, so two requests that happen to carry
// the same body would otherwise share a signature and the second would be
// refused as a replay — a 401 where the test was asking about a 400.
func postSessionsAt(t *testing.T, s *testServer, body []byte, at time.Time) created {
	t.Helper()

	req := httptest.NewRequest(http.MethodPost, "/sessions", bytes.NewReader(body))
	signRequest(t, req, body, at)

	answer := httptest.NewRecorder()
	s.ServeHTTP(answer, req)

	out := created{answer: answer}
	if answer.Body.Len() > 0 {
		if err := json.Unmarshal(answer.Body.Bytes(), &out.body); err != nil {
			t.Fatalf("the response %q is not JSON: %v", answer.Body, err)
		}
	}
	return out
}

// field reads a string field the contract requires, failing rather than
// returning "" so a missing field cannot pass as an empty one.
func (c created) field(t *testing.T, name string) string {
	t.Helper()

	v, ok := c.body[name]
	if !ok {
		t.Fatalf("the response has no %q field: %v", name, c.body)
	}
	s, ok := v.(string)
	if !ok {
		t.Fatalf("%s = %v (%T); want a string", name, v, v)
	}
	return s
}

// TestCreateAnswersTheContractResponse is contracts/http-api.md's 201 example,
// field by field. The values are pinned against the fixture's own clock and
// directory rather than read back out of the record, so the test says what the
// caller is promised and not what the code produced.
func TestCreateAnswersTheContractResponse(t *testing.T) {
	t.Parallel()

	s := newAuditedServer(t)
	got := postSessions(t, s, createBody(s.fixture))

	if got.answer.Code != http.StatusCreated {
		t.Fatalf("status = %d (%q); want %d", got.answer.Code, got.answer.Body, http.StatusCreated)
	}
	if ct := got.answer.Header().Get(headerContentType); ct != contentTypeJSON {
		t.Errorf("Content-Type = %q; want %q — every response is JSON", ct, contentTypeJSON)
	}

	want := map[string]string{
		"name":       "refactor-auth",
		"work_dir":   s.fixture.repo,
		"state":      "starting",
		"created_at": testTime.Format(time.RFC3339),
		"expires_at": testTime.Add(24 * time.Hour).Format(time.RFC3339),
	}
	for name, value := range want {
		if got := got.field(t, name); got != value {
			t.Errorf("%s = %q; want %q", name, got, value)
		}
	}

	id := got.field(t, "id")
	if len(id) != 32 {
		t.Errorf("id = %q (%d characters); want 32", id, len(id))
	}
	if _, err := hex.DecodeString(id); err != nil {
		t.Errorf("id = %q; want lowercase hex: %v", id, err)
	}
	if strings.ToLower(id) != id {
		t.Errorf("id = %q; want lowercase hex", id)
	}

	// The contract's field set exactly: a response carrying anything else is a
	// response that has grown a field nobody reviewed.
	wantFields := []string{"created_at", "expires_at", "id", "name", "state", "token", "work_dir"}
	if fields := slices.Sorted(maps.Keys(got.body)); !slices.Equal(fields, wantFields) {
		t.Errorf("response fields = %v; want %v", fields, wantFields)
	}
}

// TestExpiresAtIsExactlyOneDayAfterCreatedAt is the arithmetic T022 names, done
// on the response rather than on the record: the two instants a caller is given
// must be 24 hours apart whatever the clock said, which is what makes the token
// expiry and the session's absolute deadline the same instant (FR-015).
func TestExpiresAtIsExactlyOneDayAfterCreatedAt(t *testing.T) {
	t.Parallel()

	s := newAuditedServer(t)
	got := postSessions(t, s, createBody(s.fixture))

	createdAt, err := time.Parse(time.RFC3339, got.field(t, "created_at"))
	if err != nil {
		t.Fatalf("created_at is not RFC 3339: %v", err)
	}
	expiresAt, err := time.Parse(time.RFC3339, got.field(t, "expires_at"))
	if err != nil {
		t.Fatalf("expires_at is not RFC 3339: %v", err)
	}

	if d := expiresAt.Sub(createdAt); d != 24*time.Hour {
		t.Errorf("expires_at - created_at = %v; want exactly 24h", d)
	}
	if zone, _ := expiresAt.Zone(); zone != "UTC" {
		t.Errorf("expires_at is in %q; the contract writes every instant in UTC", zone)
	}
}

// TestTheTokenIsHandedOverOnceAndNeverKept is FR-013. The response is the only
// copy that will ever exist: the store holds a SHA-256 it can compare against
// and nothing that could be replayed out of it.
func TestTheTokenIsHandedOverOnceAndNeverKept(t *testing.T) {
	t.Parallel()

	s := newAuditedServer(t)
	got := postSessions(t, s, createBody(s.fixture))
	token := got.field(t, "token")

	if len(token) != session.TokenLen {
		t.Errorf("token is %d characters; want %d", len(token), session.TokenLen)
	}
	if _, err := hex.DecodeString(token); err != nil {
		t.Errorf("token = %q; want lowercase hex: %v", token, err)
	}

	record, err := s.fixture.store.Get(got.field(t, "id"), auth.CallerOperator)
	if err != nil {
		t.Fatalf("the created session is not in the store: %v", err)
	}
	if !record.TokenMatches(token) {
		t.Error("the token in the response does not match the stored hash")
	}
	if strings.Contains(fmt.Sprintf("%+v", record), token) {
		t.Error("the stored record carries the plaintext token")
	}
	if written := s.sink.String(); strings.Contains(written, token) {
		t.Errorf("the audit trail carries the token: %q", written)
	}
}

// TestTheOwnerIsTheAuthenticatedCallerAndNotTheBody is FR-012 at this handler.
// The identity comes from the credential; a body that tries to name one is
// refused as an unknown field rather than quietly ignored, so a caller probing
// for it is a caller the trail records.
func TestTheOwnerIsTheAuthenticatedCallerAndNotTheBody(t *testing.T) {
	t.Parallel()

	s := newAuditedServer(t)
	got := postSessions(t, s, createBody(s.fixture))

	record, err := s.fixture.store.Get(got.field(t, "id"), auth.CallerOperator)
	if err != nil {
		t.Fatalf("the created session is not owned by the authenticated caller: %v", err)
	}
	if record.Owner != auth.CallerOperator {
		t.Errorf("owner = %q; want %q", record.Owner, auth.CallerOperator)
	}

	claimed := newAuditedServer(t)
	body := []byte(`{"name":"refactor-auth","work_dir":"` + claimed.fixture.repo + `","owner":"somebody-else"}`)
	if refused := postSessions(t, claimed, body); refused.answer.Code != http.StatusBadRequest {
		t.Errorf("a body naming its own owner = %d; want %d", refused.answer.Code, http.StatusBadRequest)
	}
	if n := claimed.fixture.store.Len(); n != 0 {
		t.Errorf("the refused create left %d session(s) in the store; want 0", n)
	}
}

// TestCreateStartsTheSessionItPromised: the 201 is a claim about the host, so
// the fake is asked what actually ran. The target is built from the ID alone
// (FR-034), which is why the caller's name may not appear in any argv.
func TestCreateStartsTheSessionItPromised(t *testing.T) {
	t.Parallel()

	s := newAuditedServer(t)
	got := postSessions(t, s, createBody(s.fixture))
	id := got.field(t, "id")

	var ops []tmuxctl.Op
	for _, call := range s.fixture.tmux.Calls() {
		ops = append(ops, call.Op)
		// FR-034 is about *targets*, and that is what this asserts rather than
		// mere presence. The name is recorded as an option value now (#72), so
		// it does reach tmux — as its own argv element, in a position that
		// addresses nothing. What must never happen is the name appearing in the
		// -t argument, because that is the one place a string selects a window.
		for i, arg := range call.Argv {
			if arg != "-t" || i+1 >= len(call.Argv) {
				continue
			}
			if strings.Contains(call.Argv[i+1], "refactor-auth") {
				t.Errorf("the caller's name reached a tmux target: %q", strings.Join(call.Argv, " "))
			}
		}
	}
	// Five options now, the fifth being the start-command name mode is derived
	// from (contracts/session-mode.md).
	wantOps := []tmuxctl.Op{
		tmuxctl.OpNew,
		tmuxctl.OpSetOption, tmuxctl.OpSetOption, tmuxctl.OpSetOption, tmuxctl.OpSetOption, tmuxctl.OpSetOption,
		tmuxctl.OpSendKeys,
	}
	if !slices.Equal(ops, wantOps) {
		t.Errorf("tmux calls = %v; want %v", ops, wantOps)
	}

	if dir, ok := s.fixture.tmux.WorkDir("crswd-" + id); !ok || dir != s.fixture.repo {
		t.Errorf("the session runs in %q (found: %v); want %q", dir, ok, s.fixture.repo)
	}
}

// TestTheResponseCarriesTheResolvedPathAndNotTheCallersSpelling is FR-028's
// other half. The allowlist check happens on the resolved path, so the session
// runs somewhere the caller only named indirectly — and a response that echoed
// the request back would tell the caller its symlink was honoured when what was
// honoured is where the link pointed.
func TestTheResponseCarriesTheResolvedPathAndNotTheCallersSpelling(t *testing.T) {
	t.Parallel()

	s := newAuditedServer(t)
	link := filepath.Join(s.fixture.root, "inside-link")
	if err := os.Symlink(s.fixture.repo, link); err != nil {
		t.Fatalf("create the fixture symlink: %v", err)
	}

	got := postSessions(t, s, []byte(`{"name":"probe","work_dir":"`+link+`"}`))
	if got.answer.Code != http.StatusCreated {
		t.Fatalf("status = %d (%q); want %d", got.answer.Code, got.answer.Body, http.StatusCreated)
	}
	if dir := got.field(t, "work_dir"); dir != s.fixture.repo {
		t.Errorf("work_dir = %q; want the resolved %q", dir, s.fixture.repo)
	}
	if dir, ok := s.fixture.tmux.WorkDir("crswd-" + got.field(t, "id")); !ok || dir != s.fixture.repo {
		t.Errorf("the session runs in %q (found: %v); want the resolved %q", dir, ok, s.fixture.repo)
	}
}

// TestCreateIsRecordedOnceUnderTheDaemonsOwnSessionID is FR-041 for this route.
// The session_id must come off the record the daemon made, which for a create is
// the only ID in the exchange the caller did not choose.
func TestCreateIsRecordedOnceUnderTheDaemonsOwnSessionID(t *testing.T) {
	t.Parallel()

	s := newAuditedServer(t)
	got := postSessions(t, s, createBody(s.fixture))

	rec := s.only(t)
	if rec["action"] != string(audit.ActionSessionCreate) {
		t.Errorf("action = %v; want %q", rec["action"], audit.ActionSessionCreate)
	}
	if rec["decision"] != string(audit.Allow) {
		t.Errorf("decision = %v; want %q", rec["decision"], audit.Allow)
	}
	if want := string(auth.CallerOperator); rec["caller"] != want {
		t.Errorf("caller = %v; want %q", rec["caller"], want)
	}
	if want := got.field(t, "id"); rec["session_id"] != want {
		t.Errorf("session_id = %v; want %q", rec["session_id"], want)
	}
	if reason, ok := rec["reason"]; ok {
		t.Errorf("an allowed create recorded a reason: %v", reason)
	}
}

// refusedCreates is every way a create is refused at field validation, with the
// reason each is recorded under. The bodies are built against the fixture, so
// this is a function rather than a table.
//
// The reasons are the point. The answer outward is one status and one body for
// all of them — a caller that could tell "outside an approved root" from "does
// not exist" would be reading the filesystem through a 400 — so the trail is the
// only place the difference survives.
func refusedCreates(t *testing.T, f sessionFixture) map[string]struct {
	body   []byte
	reason error
} {
	t.Helper()

	outside := filepath.Dir(f.root)

	// The prefix trap: a sibling whose path carries the approved root as a
	// string prefix and is not under it (FR-028).
	lookalike := f.root + "EVIL"
	if err := os.Mkdir(lookalike, 0o750); err != nil {
		t.Fatalf("create the lookalike root: %v", err)
	}

	// A symlink that sits inside the approved root and points out of it. Only
	// resolving before checking containment catches this one.
	escape := filepath.Join(f.root, "escape")
	if err := os.Symlink(outside, escape); err != nil {
		t.Fatalf("create the escaping symlink: %v", err)
	}

	file := filepath.Join(f.root, "a-file")
	if err := os.WriteFile(file, []byte("not a directory"), 0o600); err != nil {
		t.Fatalf("create the fixture file: %v", err)
	}

	body := func(name, workDir string) []byte {
		return []byte(`{"name":"` + name + `","work_dir":"` + workDir + `"}`)
	}

	return map[string]struct {
		body   []byte
		reason error
	}{
		"a name carrying tmux's window separator": {body("refactor:auth", f.repo), session.ErrNameIsTmuxTarget},
		"a name carrying tmux's pane separator":   {body("refactor.auth", f.repo), session.ErrNameIsTmuxTarget},
		"a name outside the alphabet":             {body("refactor auth", f.repo), session.ErrInvalidName},
		"a name with a slash":                     {body("refactor/auth", f.repo), session.ErrInvalidName},
		"an empty name":                           {body("", f.repo), session.ErrInvalidName},
		"a name past 64 characters":               {body(strings.Repeat("a", 65), f.repo), session.ErrInvalidName},

		"a path outside every approved root": {body("probe", outside), session.ErrWorkDirOutsideRoots},
		"the prefix trap":                    {body("probe", lookalike), session.ErrWorkDirOutsideRoots},
		"a symlink escaping the root":        {body("probe", escape), session.ErrWorkDirOutsideRoots},
		"a traversal out of the root": {
			body("probe", filepath.Join(f.repo, "..", "..")), session.ErrWorkDirOutsideRoots,
		},
		"a relative path":       {body("probe", "repo"), session.ErrWorkDirNotAbsolute},
		"a path that is a file": {body("probe", file), session.ErrWorkDirNotDirectory},
		"a path that does not exist": {
			body("probe", filepath.Join(f.repo, "nope")), session.ErrWorkDirUnresolvable,
		},
		"an empty path": {body("probe", ""), session.ErrInvalidWorkDir},

		"an unknown field": {
			[]byte(`{"name":"probe","work_dir":"` + f.repo + `","surprise":true}`), errBodyUnknownField,
		},
		"no fields at all": {[]byte(`{}`), session.ErrInvalidName},
	}
}

// TestCreateRefusesEveryInvalidRequest is the contract's 400 row for this route:
// a bad name, a path outside the allowlist, a symlink escape, and an unknown
// field alike. Nothing is executed and nothing is recorded except why.
func TestCreateRefusesEveryInvalidRequest(t *testing.T) {
	t.Parallel()

	// The outer fixture names the cases; each subtest rebuilds them against a
	// server of its own, because the bodies are paths and a path is only refused
	// relative to the allowlist the manager was built on.
	for name := range refusedCreates(t, newSessionFixture(t)) {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			// A server of its own, so "no session was created" and "nothing ran"
			// are claims about this request alone.
			s := newAuditedServer(t)
			c := refusedCreates(t, s.fixture)[name]
			got := postSessions(t, s, c.body)

			if got.answer.Code != http.StatusBadRequest {
				t.Fatalf("status = %d (%q); want %d", got.answer.Code, got.answer.Body, http.StatusBadRequest)
			}
			if body := got.answer.Body.String(); body != string(bodyBadRequest) {
				t.Errorf("body = %q; want %q", body, bodyBadRequest)
			}
			if n := s.fixture.store.Len(); n != 0 {
				t.Errorf("the refused create left %d session(s) in the store; want 0", n)
			}
			if calls := s.fixture.tmux.Calls(); len(calls) != 0 {
				t.Errorf("the refused create ran %v; a rejected request must cost no tmux command", calls)
			}

			rec := s.only(t)
			if rec["decision"] != string(audit.Deny) {
				t.Errorf("decision = %v; want %q", rec["decision"], audit.Deny)
			}
			if rec["reason"] != c.reason.Error() {
				t.Errorf("reason = %v; want %q", rec["reason"], c.reason.Error())
			}
			if rec["action"] != string(audit.ActionSessionCreate) {
				t.Errorf("action = %v; want %q — refusing must not rename the operation",
					rec["action"], audit.ActionSessionCreate)
			}
			if id, ok := rec["session_id"]; ok {
				t.Errorf("a refused create recorded a session_id: %v", id)
			}
		})
	}
}

// TestEveryRefusedCreateAnswersTheIdenticalResponse is the oracle rule stated as
// bytes. The fields being validated are a session name and a filesystem path, so
// a caller able to tell one refusal from another could walk the host's directory
// tree through a 400 without ever creating a session.
func TestEveryRefusedCreateAnswersTheIdenticalResponse(t *testing.T) {
	t.Parallel()

	s := newAuditedServer(t)
	cases := refusedCreates(t, s.fixture)
	names := slices.Sorted(maps.Keys(cases))

	answers := map[string]*httptest.ResponseRecorder{}
	for i, name := range names {
		// Two of these cases resolve to the same directory by design — a
		// traversal and an absolute path both land outside the root — so they
		// carry the same body and need distinct instants to not replay.
		answers[name] = postSessionsAt(t, s, cases[name].body, testTime.Add(-time.Duration(i)*time.Second)).answer
	}

	for _, name := range names[1:] {
		a, b := answers[names[0]], answers[name]
		if !reflect.DeepEqual(a.Header(), b.Header()) {
			t.Errorf("%q answered with headers %v but %q answered with %v; the refusal must be uniform",
				names[0], a.Header(), name, b.Header())
		}
		if !bytes.Equal(a.Body.Bytes(), b.Body.Bytes()) {
			t.Errorf("%q answered %q but %q answered %q; the refusal must be uniform",
				names[0], a.Body, name, b.Body)
		}
		if a.Code != b.Code {
			t.Errorf("%q answered %d but %q answered %d", names[0], a.Code, name, b.Code)
		}
	}
}

// TestARefusedCreateCarriesNoCallerBytesAnywhere is FR-042 and FR-043 for the
// two fields this handler reads. A path is caller-controlled text; the audit
// reason is one %w away from carrying it at all times, which is why the reasons
// recorded are sentinels rather than the errors that wrap them.
func TestARefusedCreateCarriesNoCallerBytesAnywhere(t *testing.T) {
	t.Parallel()

	const marker = "kumquat"
	s := newAuditedServer(t)
	bodies := map[string][]byte{
		"a path outside the roots": []byte(`{"name":"probe","work_dir":"/` + marker + `"}`),
		"a path that is missing":   []byte(`{"name":"probe","work_dir":"` + s.fixture.root + `/` + marker + `"}`),
		"a relative path":          []byte(`{"name":"probe","work_dir":"` + marker + `"}`),
		"a rejected name":          []byte(`{"name":"` + marker + `:1","work_dir":"` + s.fixture.repo + `"}`),
		"an unknown field":         []byte(`{"` + marker + `":true}`),
	}

	for name, body := range bodies {
		got := postSessions(t, s, body)
		outward := got.answer.Body.String() + " " + fmt.Sprint(got.answer.Header())
		if strings.Contains(outward, marker) {
			t.Errorf("the %s refusal echoed the request back: %q", name, outward)
		}
	}
	if written := s.sink.String(); strings.Contains(written, marker) {
		t.Errorf("a refusal put caller bytes in the audit trail: %q", written)
	}
}

// TestATmuxFailureAnswersFiveHundredWithNoDetail: the session could not be
// started, the record is gone because the teardown was verified, and the caller
// is told nothing about the host. "tmux: command not found" is a fact about the
// machine, and the caller who triggered it is the last party who should have it.
func TestATmuxFailureAnswersFiveHundredWithNoDetail(t *testing.T) {
	t.Parallel()

	const marker = "no-such-tmux-binary"
	s := newAuditedServer(t)
	s.fixture.tmux.FailOp(tmuxctl.OpNew, errors.New(marker))

	got := postSessions(t, s, createBody(s.fixture))

	if got.answer.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d (%q); want %d", got.answer.Code, got.answer.Body, http.StatusInternalServerError)
	}
	if body := got.answer.Body.String(); body != string(bodyInternalError) {
		t.Errorf("body = %q; want %q", body, bodyInternalError)
	}
	if ct := got.answer.Header().Get(headerContentType); ct != contentTypeJSON {
		t.Errorf("Content-Type = %q; want %q", ct, contentTypeJSON)
	}
	if n := s.fixture.store.Len(); n != 0 {
		t.Errorf("the failed create left %d session(s) in the store; the teardown was verified, so want 0", n)
	}

	rec := s.only(t)
	if rec["decision"] != string(audit.Deny) {
		t.Errorf("decision = %v; want %q", rec["decision"], audit.Deny)
	}
	if rec["reason"] != errCreateRefused.Error() {
		t.Errorf("reason = %v; want %q", rec["reason"], errCreateRefused.Error())
	}
	if written := s.sink.String() + got.answer.Body.String(); strings.Contains(written, marker) {
		t.Errorf("the tmux failure travelled outward: %q", written)
	}
}

// TestACreateThatMayHaveLeftAShellRunningSaysSoInTheTrail is Principle VI's
// worst case: the shell started, something later failed, and the kill could not
// be confirmed. The caller gets the same detail-free 500 — it holds no token, so
// the session is drivable by nobody — but the trail must carry the one fact an
// operator needs, and the record must stay so that the reaper still has an owner
// and a deadline for it.
func TestACreateThatMayHaveLeftAShellRunningSaysSoInTheTrail(t *testing.T) {
	t.Parallel()

	s := newAuditedServer(t)
	s.fixture.tmux.FailOp(tmuxctl.OpSendKeys, errors.New("the pane went away"))
	s.fixture.tmux.FailOp(tmuxctl.OpKill, errors.New("tmux is not answering"))
	s.fixture.tmux.FailOp(tmuxctl.OpHas, errors.New("tmux is not answering"))

	got := postSessions(t, s, createBody(s.fixture))

	if got.answer.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d (%q); want %d", got.answer.Code, got.answer.Body, http.StatusInternalServerError)
	}
	if body := got.answer.Body.String(); body != string(bodyInternalError) {
		t.Errorf("body = %q; want %q", body, bodyInternalError)
	}
	if n := s.fixture.store.Len(); n != 1 {
		t.Errorf("the store holds %d session(s); a session that may still be running must keep its record", n)
	}

	if reason := s.only(t)["reason"]; reason != errCreateOrphaned.Error() {
		t.Errorf("reason = %v; want %q — an operator has to be able to grep for this one",
			reason, errCreateOrphaned.Error())
	}
}

// TestCreateRefusesARequestWithNoAuthenticatedCaller is unreachable through the
// router, which is the point: the handler is checked directly to prove it fails
// closed rather than defaulting to an identity. A session owned by the zero
// CallerID is one every later ownership check would pass over.
func TestCreateRefusesARequestWithNoAuthenticatedCaller(t *testing.T) {
	t.Parallel()

	s := newAuditedServer(t)
	body := createBody(s.fixture)
	req := httptest.NewRequest(http.MethodPost, "/sessions", bytes.NewReader(body))

	answer := httptest.NewRecorder()
	s.createSession(answer, req)

	if answer.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d; want %d", answer.Code, http.StatusInternalServerError)
	}
	if n := s.fixture.store.Len(); n != 0 {
		t.Errorf("a create with no caller made %d session(s); want 0", n)
	}
	if calls := s.fixture.tmux.Calls(); len(calls) != 0 {
		t.Errorf("a create with no caller ran %v; want nothing", calls)
	}
}

// TestASuccessfulCreateNeverReachesTheHostTwice guards the property the
// signature does not: one signed create must produce one session. The replay is
// refused by layer 2, so the second request never reaches tmux.
func TestASuccessfulCreateNeverReachesTheHostTwice(t *testing.T) {
	t.Parallel()

	s := newAuditedServer(t)
	body := createBody(s.fixture)

	first := postSessions(t, s, body)
	if first.answer.Code != http.StatusCreated {
		t.Fatalf("the first create = %d (%q); want %d", first.answer.Code, first.answer.Body, http.StatusCreated)
	}

	replayed := postSessions(t, s, body)
	if replayed.answer.Code != http.StatusUnauthorized {
		t.Errorf("the replayed create = %d; want %d", replayed.answer.Code, http.StatusUnauthorized)
	}
	if n := s.fixture.store.Len(); n != 1 {
		t.Errorf("the store holds %d session(s) after one create and its replay; want 1", n)
	}
}

// TestCreatePastTheCapAnswersTooManyRequests is quickstart.md's cap check driven
// through the API: with the default cap of 5 the sixth create is a 429 and the
// first five are untouched (FR-036).
//
// Each create is signed at its own instant. The bodies are identical, and the
// signature covers the timestamp and the body alone, so without that the second
// would be refused as a replay and the test would be asserting layer 2 instead of
// the cap.
func TestCreatePastTheCapAnswersTooManyRequests(t *testing.T) {
	t.Parallel()

	s := newAuditedServer(t)

	ids := make([]string, 0, config.DefaultMaxSessions)
	for i := 0; i < config.DefaultMaxSessions; i++ {
		got := postSessionsAt(t, s, createBody(s.fixture), testTime.Add(-time.Duration(i)*time.Second))
		if got.answer.Code != http.StatusCreated {
			t.Fatalf("create %d = %d (%q); want %d", i+1, got.answer.Code, got.answer.Body, http.StatusCreated)
		}
		ids = append(ids, got.field(t, "id"))
	}

	before := len(s.fixture.tmux.Calls())
	refused := postSessionsAt(t, s, createBody(s.fixture),
		testTime.Add(-time.Duration(config.DefaultMaxSessions)*time.Second))

	if refused.answer.Code != http.StatusTooManyRequests {
		t.Fatalf("the create past the cap = %d (%q); want %d",
			refused.answer.Code, refused.answer.Body, http.StatusTooManyRequests)
	}
	if body := refused.answer.Body.String(); body != string(bodyTooManyRequests) {
		t.Errorf("body = %q; want %q", body, bodyTooManyRequests)
	}
	if ct := refused.answer.Header().Get(headerContentType); ct != contentTypeJSON {
		t.Errorf("Content-Type = %q; want %q — every response is JSON", ct, contentTypeJSON)
	}
	if extra := s.fixture.tmux.Calls()[before:]; len(extra) != 0 {
		t.Errorf("the refused create ran %v; a request the cap refuses must cost no tmux command", extra)
	}

	// The five already running are unaffected: still recorded, still owned, and
	// still answering as themselves.
	if n := s.fixture.store.Len(); n != config.DefaultMaxSessions {
		t.Errorf("the store holds %d session(s) after the refusal; want %d", n, config.DefaultMaxSessions)
	}
	for i, id := range ids {
		if _, err := s.fixture.store.Get(id, auth.CallerOperator); err != nil {
			t.Errorf("session %d is no longer the caller's after a refused create: %v", i+1, err)
		}
	}

	// The trail carries what the caller is not told: which of the two conditions
	// behind this status it was.
	records := s.records(t)
	if len(records) != config.DefaultMaxSessions+1 {
		t.Fatalf("%d requests emitted %d audit records; FR-041 requires exactly one each",
			config.DefaultMaxSessions+1, len(records))
	}
	rec := records[len(records)-1]
	if rec["decision"] != string(audit.Deny) {
		t.Errorf("decision = %v; want %q", rec["decision"], audit.Deny)
	}
	if rec["reason"] != errCreateCapReached.Error() {
		t.Errorf("reason = %v; want %q", rec["reason"], errCreateCapReached.Error())
	}
	if rec["action"] != string(audit.ActionSessionCreate) {
		t.Errorf("action = %v; want %q — refusing must not rename the operation",
			rec["action"], audit.ActionSessionCreate)
	}
	if id, ok := rec["session_id"]; ok {
		t.Errorf("a refused create recorded a session_id: %v", id)
	}
}

// The 429 says nothing about the fleet it is refusing on behalf of. A caller
// owns none of the sessions it is counted against — there is one operator today,
// and milestone 2's identities make that literal — so a body or a header naming
// the cap, the count, or a session would be the daemon reporting on somebody
// else's work.
func TestTheCapRefusalDisclosesNothingAboutTheFleet(t *testing.T) {
	t.Parallel()

	s := newAuditedServer(t)

	ids := make([]string, 0, config.DefaultMaxSessions)
	for i := 0; i < config.DefaultMaxSessions; i++ {
		got := postSessionsAt(t, s, createBody(s.fixture), testTime.Add(-time.Duration(i)*time.Second))
		ids = append(ids, got.field(t, "id"))
	}

	refused := postSessionsAt(t, s, createBody(s.fixture),
		testTime.Add(-time.Duration(config.DefaultMaxSessions)*time.Second))
	outward := refused.answer.Body.String() + " " + fmt.Sprint(refused.answer.Header())

	for i, id := range ids {
		if strings.Contains(outward, id) {
			t.Errorf("the refusal named session %d: %q", i+1, outward)
		}
	}
	if strings.Contains(outward, fmt.Sprint(config.DefaultMaxSessions)) {
		t.Errorf("the refusal disclosed the cap: %q", outward)
	}
}

// promptFixture is an audited server plus one live session and the only copy of
// its credential, which is everything a prompt request needs before it can be
// about the prompt at all.
type promptFixture struct {
	*testServer

	live  session.Session
	token string
}

func newPromptFixture(t *testing.T) promptFixture {
	t.Helper()

	s := newAuditedServer(t)
	live, issued := s.fixture.plant(t, session.Session{})
	return promptFixture{testServer: s, live: live, token: issued}
}

// post drives one signed, credentialled prompt through the whole stack —
// signature, middleware, resolver, decoder, manager, tmux fake — because every
// claim T024 makes is about a request and not about a function.
//
// The instant is a parameter for the reason postSessionsAt takes one: the
// signature covers the timestamp and the body and nothing else, so two identical
// prompts to the same server share a signature and the second is refused as a
// replay unless they are signed a second apart.
func (f promptFixture) post(t *testing.T, body []byte, at time.Time) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()

	req := httptest.NewRequest(http.MethodPost, "/sessions/"+f.live.ID+"/prompt", bytes.NewReader(body))
	signRequest(t, req, body, at)
	// After signing, because layer 3 is a separate credential and not part of
	// the signed payload.
	req.Header.Set(headerAuthorization, bearerScheme+f.token)

	answer := httptest.NewRecorder()
	f.ServeHTTP(answer, req)

	var decoded map[string]any
	if answer.Body.Len() > 0 {
		if err := json.Unmarshal(answer.Body.Bytes(), &decoded); err != nil {
			t.Fatalf("the response %q is not JSON: %v", answer.Body, err)
		}
	}
	return answer, decoded
}

// TestPromptAnswersTheContractResponse is contracts/http-api.md's 202 example,
// field by field. 202 and not 200: what is confirmed is that the keystrokes
// reached the pane, not that Claude has read them.
func TestPromptAnswersTheContractResponse(t *testing.T) {
	t.Parallel()

	f := newPromptFixture(t)
	answer, body := f.post(t, promptBody(), testTime)

	if answer.Code != http.StatusAccepted {
		t.Fatalf("status = %d (%q); want %d", answer.Code, answer.Body, http.StatusAccepted)
	}
	if ct := answer.Header().Get(headerContentType); ct != contentTypeJSON {
		t.Errorf("Content-Type = %q; want %q", ct, contentTypeJSON)
	}
	if got := body["id"]; got != f.live.ID {
		t.Errorf("id = %v; want %q — the daemon's own ID for the resolved session", got, f.live.ID)
	}
	if got := body["delivered"]; got != true {
		t.Errorf("delivered = %v; want true", got)
	}
	if len(body) != 2 {
		t.Errorf("the response carries %d fields (%v); the contract defines exactly two", len(body), body)
	}
}

// hostilePrompts is research D4's table plus the newline SC-012 names. Every one
// of these is a payload send-keys -l would mangle or a shell would act on, and
// the point of each is that neither happens: the bytes travel on stdin and land
// in the pane unchanged.
var hostilePrompts = map[string]string{
	"a lone semicolon":          ";",
	"a trailing semicolon":      "foo;",
	"two trailing semicolons":   "foo;;",
	"a shell injection attempt": "a; echo PWNED; $(id) `whoami`",
	"an embedded newline":       "first line\nsecond line",
}

// TestAPromptIsDeliveredByteForByte is SC-012, and it is the reason this route
// does not use send-keys. tmux's own parser eats a trailing unescaped ";" from
// the last argument before -l applies and "--" does not stop it, so ";" would
// arrive empty and "foo;" would arrive as "foo" — silently, on the one prompt in
// a hundred that happens to end in a semicolon.
//
// The assertions are two halves of the same claim: the exact bytes reached tmux
// on stdin, and they appear in no argv at all. The second is what "no shell
// string is ever built" looks like as a test rather than a review comment.
func TestAPromptIsDeliveredByteForByte(t *testing.T) {
	t.Parallel()

	for name, payload := range hostilePrompts {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			f := newPromptFixture(t)
			body, err := json.Marshal(promptRequest{Text: payload})
			if err != nil {
				t.Fatalf("marshal the prompt body: %v", err)
			}

			answer, _ := f.post(t, body, testTime)
			if answer.Code != http.StatusAccepted {
				t.Fatalf("status = %d (%q); want %d", answer.Code, answer.Body, http.StatusAccepted)
			}

			tmuxName, pane := f.live.TmuxName(), f.live.PaneTarget()
			want := []tmuxctl.Call{
				{Op: tmuxctl.OpPaste, Argv: []string{"tmux", "load-buffer", "-b", tmuxName, "-"}, Stdin: []byte(payload)},
				{
					Op:   tmuxctl.OpPaste,
					Argv: []string{"tmux", "paste-buffer", "-d", "-b", tmuxName, "-t", pane},
				},
				{Op: tmuxctl.OpSendKeys, Argv: []string{"tmux", "send-keys", "-t", pane, "--", "Enter"}},
			}

			got := f.fixture.tmux.Calls()
			if len(got) != len(want) {
				t.Fatalf("the prompt ran %v; want exactly %v", got, want)
			}
			for i, c := range got {
				if c.Op != want[i].Op || !slices.Equal(c.Argv, want[i].Argv) {
					t.Errorf("call %d = %s %v; want %s %v", i, c.Op, c.Argv, want[i].Op, want[i].Argv)
				}
				if !bytes.Equal(c.Stdin, want[i].Stdin) {
					t.Errorf("call %d stdin = %q; want %q — the payload must arrive byte-for-byte",
						i, c.Stdin, want[i].Stdin)
				}
			}
			for _, c := range got {
				if slices.ContainsFunc(c.Argv, func(arg string) bool { return strings.Contains(arg, payload) }) {
					t.Errorf("%s put the prompt on the command line: %v", c.Op, c.Argv)
				}
			}
		})
	}
}

// TestThePromptTextReachesNoAuditRecordOrLog is FR-042 for this route. Prompt
// text is secret under docs/security.md §3 — it is whatever the operator was
// about to ask Claude to do, on whatever it was about to do it to — and the one
// record this request produces says which session was prompted, never with what.
func TestThePromptTextReachesNoAuditRecordOrLog(t *testing.T) {
	t.Parallel()

	// Distinctive enough that finding it anywhere is proof rather than
	// coincidence, and shaped like the prompts that matter.
	const marker = "zzz-secret-prompt-text-zzz"

	f := newPromptFixture(t)
	body, err := json.Marshal(promptRequest{Text: "deploy " + marker + " to production"})
	if err != nil {
		t.Fatalf("marshal the prompt body: %v", err)
	}

	answer, _ := f.post(t, body, testTime)
	if answer.Code != http.StatusAccepted {
		t.Fatalf("status = %d (%q); want %d", answer.Code, answer.Body, http.StatusAccepted)
	}

	if written := f.sink.String(); strings.Contains(written, marker) {
		t.Errorf("the audit trail carries the prompt text: %q", written)
	}
	if outward := answer.Body.String() + " " + fmt.Sprint(answer.Header()); strings.Contains(outward, marker) {
		t.Errorf("the response echoed the prompt back: %q", outward)
	}
	if len(f.failed) != 0 {
		t.Errorf("the request reported %v; want nothing", f.failed)
	}

	rec := f.only(t)
	if rec["action"] != string(audit.ActionSessionPrompt) {
		t.Errorf("action = %v; want %q", rec["action"], audit.ActionSessionPrompt)
	}
	if rec["decision"] != string(audit.Allow) {
		t.Errorf("decision = %v; want %q", rec["decision"], audit.Allow)
	}
	if rec["session_id"] != f.live.ID {
		t.Errorf("session_id = %v; want %q", rec["session_id"], f.live.ID)
	}
	if reason, ok := rec["reason"]; ok {
		t.Errorf("an allowed prompt recorded a reason: %v", reason)
	}
}

// refusedPrompts is every way this route's body can be bad. The empty cases are
// the contract's "required, non-empty" rule; the rest are the decoder's, and
// they are here to prove this handler goes through it rather than reading the
// body its own way.
var refusedPrompts = map[string]struct {
	body   []byte
	reason error
}{
	"an empty text":            {[]byte(`{"text":""}`), session.ErrEmptyPrompt},
	"no text field at all":     {[]byte(`{}`), session.ErrEmptyPrompt},
	"a text of the wrong type": {[]byte(`{"text":42}`), errBodyWrongShape},
	"an unknown field":         {[]byte(`{"text":"hi","surprise":true}`), errBodyUnknownField},
	"a body that is not JSON":  {[]byte(`{"text":`), errBodyMalformed},
	"no body at all":           {nil, errBodyMissing},
	"two JSON values in one body": {
		[]byte(`{"text":"first"} {"text":"second"}`), errBodyTrailingData,
	},
}

// TestARefusedPromptCostsNoTmuxCommand is the contract's 400 row for this route.
// Every refusal answers with the identical body — an oracle here would be worth
// less than on create, but a second spelling of a 400 is how the uniform ones
// stop being uniform — and, more to the point, nothing reaches the host: a body
// the daemon would not accept must not put keystrokes into a live session.
func TestARefusedPromptCostsNoTmuxCommand(t *testing.T) {
	t.Parallel()

	for name, c := range refusedPrompts {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			// A server of its own, so "nothing ran" and "exactly one record" are
			// claims about this request alone.
			f := newPromptFixture(t)
			answer, _ := f.post(t, c.body, testTime)

			if answer.Code != http.StatusBadRequest {
				t.Fatalf("status = %d (%q); want %d", answer.Code, answer.Body, http.StatusBadRequest)
			}
			if body := answer.Body.String(); body != string(bodyBadRequest) {
				t.Errorf("body = %q; want %q", body, bodyBadRequest)
			}
			if calls := f.fixture.tmux.Calls(); len(calls) != 0 {
				t.Errorf("the refused prompt ran %v; a rejected request must cost no tmux command", calls)
			}

			rec := f.only(t)
			if rec["decision"] != string(audit.Deny) {
				t.Errorf("decision = %v; want %q", rec["decision"], audit.Deny)
			}
			if rec["reason"] != c.reason.Error() {
				t.Errorf("reason = %v; want %q", rec["reason"], c.reason.Error())
			}
			if rec["action"] != string(audit.ActionSessionPrompt) {
				t.Errorf("action = %v; want %q — refusing must not rename the operation",
					rec["action"], audit.ActionSessionPrompt)
			}
			// The resolver ran before the body was read, so the session it
			// resolved is on the record even though the prompt was refused.
			if rec["session_id"] != f.live.ID {
				t.Errorf("session_id = %v; want %q", rec["session_id"], f.live.ID)
			}
		})
	}
}

// TestATmuxFailureDuringAPromptAnswersFiveHundredWithNoDetail covers both halves
// of the delivery. A failed paste means the text never arrived; a failed Return
// means it is sitting in the pane unsubmitted. Neither is a delivery, so neither
// may answer 202 — and the caller learns nothing about the host either way.
func TestATmuxFailureDuringAPromptAnswersFiveHundredWithNoDetail(t *testing.T) {
	t.Parallel()

	for name, op := range map[string]tmuxctl.Op{
		"the paste failed":  tmuxctl.OpPaste,
		"the return failed": tmuxctl.OpSendKeys,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			const tmuxMarker = "no-such-tmux-binary"
			const textMarker = "zzz-secret-prompt-text-zzz"

			f := newPromptFixture(t)
			f.fixture.tmux.FailOp(op, errors.New(tmuxMarker))

			body, err := json.Marshal(promptRequest{Text: textMarker})
			if err != nil {
				t.Fatalf("marshal the prompt body: %v", err)
			}
			answer, _ := f.post(t, body, testTime)

			if answer.Code != http.StatusInternalServerError {
				t.Fatalf("status = %d (%q); want %d", answer.Code, answer.Body, http.StatusInternalServerError)
			}
			if got := answer.Body.String(); got != string(bodyInternalError) {
				t.Errorf("body = %q; want %q", got, bodyInternalError)
			}

			rec := f.only(t)
			if rec["decision"] != string(audit.Deny) {
				t.Errorf("decision = %v; want %q", rec["decision"], audit.Deny)
			}
			if rec["reason"] != errPromptUndelivered.Error() {
				t.Errorf("reason = %v; want %q", rec["reason"], errPromptUndelivered.Error())
			}
			outward := f.sink.String() + answer.Body.String()
			if strings.Contains(outward, tmuxMarker) {
				t.Errorf("the tmux failure travelled outward: %q", outward)
			}
			if strings.Contains(outward, textMarker) {
				t.Errorf("the undelivered prompt text travelled outward: %q", outward)
			}
		})
	}
}

// TestPromptRefusesARequestWithNoResolvedSession is unreachable through the
// router, which is the point: the handler is checked directly to prove it fails
// closed rather than falling back to the {id} in the path. A handler that read
// the path itself would address a window on a caller's say-so, which is the one
// thing FR-034 exists to prevent.
func TestPromptRefusesARequestWithNoResolvedSession(t *testing.T) {
	t.Parallel()

	f := newPromptFixture(t)
	req := httptest.NewRequest(http.MethodPost, "/sessions/"+f.live.ID+"/prompt", bytes.NewReader(promptBody()))
	req.SetPathValue(pathValueID, f.live.ID)

	answer := httptest.NewRecorder()
	f.promptSession(answer, req)

	if answer.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d (%q); want %d", answer.Code, answer.Body, http.StatusInternalServerError)
	}
	if calls := f.fixture.tmux.Calls(); len(calls) != 0 {
		t.Errorf("a prompt with no resolved session ran %v; want nothing", calls)
	}
}

// outputFixture is an audited server, one live session with the only copy of its
// credential, and a pane already holding whatever the test is about.
type outputFixture struct {
	*testServer

	live  session.Session
	token string
}

func newOutputFixture(t *testing.T, pane string) outputFixture {
	t.Helper()

	s := newAuditedServer(t)
	live, issued := s.fixture.plant(t, session.Session{})
	// The tmux session already exists — plant seeds it — so this arranges what
	// capture-pane will return without recording a call the request did not make.
	s.fixture.tmux.SetPane(live.TmuxName(), pane)

	return outputFixture{testServer: s, live: live, token: issued}
}

// get drives one signed, credentialled capture through the whole stack, and
// returns the raw recorder as well as the decoded body: half of what this route
// must be asked is about the bytes on the wire rather than the value a client
// decodes from them.
func (f outputFixture) get(t *testing.T, at time.Time) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()

	req := httptest.NewRequest(http.MethodGet, "/sessions/"+f.live.ID+"/output", nil)
	signRequest(t, req, nil, at)
	// After signing, because layer 3 is a separate credential and not part of
	// the signed payload.
	req.Header.Set(headerAuthorization, bearerScheme+f.token)

	answer := httptest.NewRecorder()
	f.ServeHTTP(answer, req)

	var decoded map[string]any
	if answer.Body.Len() > 0 {
		if err := json.Unmarshal(answer.Body.Bytes(), &decoded); err != nil {
			t.Fatalf("the response %q is not JSON: %v", answer.Body, err)
		}
	}
	return answer, decoded
}

// TestOutputAnswersTheContractResponse is contracts/http-api.md's 200 example,
// field by field, down to the pane text the contract prints.
func TestOutputAnswersTheContractResponse(t *testing.T) {
	t.Parallel()

	const pane = "$ go test ./...\nok  \tinternal/auth\t0.012s\n"

	f := newOutputFixture(t, pane)
	answer, body := f.get(t, testTime)

	if answer.Code != http.StatusOK {
		t.Fatalf("status = %d (%q); want %d", answer.Code, answer.Body, http.StatusOK)
	}
	if ct := answer.Header().Get(headerContentType); ct != contentTypeJSON {
		t.Errorf("Content-Type = %q; want %q", ct, contentTypeJSON)
	}
	if got := body["id"]; got != f.live.ID {
		t.Errorf("id = %v; want %q — the daemon's own ID for the resolved session", got, f.live.ID)
	}
	// The fixture's manager and its audit sink read the same stopped clock, so
	// this is the instant of the capture and not approximately it.
	if want := testTime.UTC().Format(timestampFormat); body["captured_at"] != want {
		t.Errorf("captured_at = %v; want %q", body["captured_at"], want)
	}
	if got := body["text"]; got != pane {
		t.Errorf("text = %q; want the pane verbatim, %q", got, pane)
	}
	if len(body) != 3 {
		t.Errorf("the response carries %d fields (%v); the contract defines exactly three", len(body), body)
	}
}

// escByte is ESC, named rather than spelled for the reason tmuxctl names it:
// this whole test is about a byte that is invisible at the point of use.
const escByte = 0x1B

// hostilePanes is what a session can put on its own screen, which is anything at
// all: colour, a title-setting OSC, cursor movement, a sequence nobody
// terminated, and a raw NUL. Every one of them is a control sequence a browser
// or a terminal would act on, and FR-031 makes removing them a requirement.
var hostilePanes = map[string]struct {
	pane string
	want string
}{
	"colour":               {"\x1b[31mFAIL\x1b[0m", "FAIL"},
	"a window title":       {"\x1b]0;pwned\x07ok", "ok"},
	"cursor movement":      {"a\x1b[2Ab", "ab"},
	"an unterminated CSI":  {"visible\x1b[", "visible"},
	"a bare control byte":  {"before\x00after", "beforeafter"},
	"a terminal reset":     {"\x1bcclean", "clean"},
	"text with no escapes": {"$ ls\nREADME.md\n", "$ ls\nREADME.md\n"},
}

// TestOutputStripsEveryControlSequence is FR-031 at the boundary that matters:
// not "does Strip work" — tmuxctl's golden tests own that — but "is anything on
// this route capable of returning a control byte to a client".
//
// Both halves are needed, and the second is the one to be careful about.
// encoding/json escapes a control byte on the way out, so a raw body can never
// carry a literal ESC and a test that looked only for one would pass against a
// handler that stripped nothing at all. What a client decodes is the real claim;
// the body is then checked for the escape's own spelling, which arrives as the
// byte itself the moment anything parses it.
func TestOutputStripsEveryControlSequence(t *testing.T) {
	t.Parallel()

	for name, c := range hostilePanes {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			f := newOutputFixture(t, c.pane)
			answer, body := f.get(t, testTime)

			if answer.Code != http.StatusOK {
				t.Fatalf("status = %d (%q); want %d", answer.Code, answer.Body, http.StatusOK)
			}
			text, ok := body["text"].(string)
			if !ok {
				t.Fatalf("text = %v (%T); want a string", body["text"], body["text"])
			}
			if text != c.want {
				t.Errorf("text = %q; want %q", text, c.want)
			}
			for _, r := range text {
				if r == '\n' || r == '\t' {
					continue
				}
				if r < 0x20 || r == 0x7F || (r >= 0x80 && r <= 0x9F) {
					t.Errorf("text carries the control character %q: %q", r, text)
				}
			}
			// The needle comes from the encoder rather than being written out
			// here: it is exactly what this response would carry if an escape
			// survived, and a literal one in this file is a byte no diff shows.
			encoded, err := json.Marshal(string(rune(escByte)))
			if err != nil {
				t.Fatalf("marshal the escape byte: %v", err)
			}
			needle := strings.ToLower(strings.Trim(string(encoded), `"`))
			if strings.Contains(strings.ToLower(answer.Body.String()), needle) {
				t.Errorf("the response body carries %s, which decodes to an escape: %q", needle, answer.Body)
			}
		})
	}
}

// TestThePaneContentReachesNoAuditRecordOrLog is FR-042 and docs/security.md §3
// for this route, and it is the one that matters most on it: a pane holds
// whatever the session printed — a key it echoed, a customer's data, a file it
// read — and the record of a capture says which session was read, never what was
// in it. A trail that carried the answer would be a second, permanent copy of
// everything every session ever printed.
func TestThePaneContentReachesNoAuditRecordOrLog(t *testing.T) {
	t.Parallel()

	// Distinctive enough that finding it anywhere is proof rather than
	// coincidence, and shaped like the thing that would actually hurt.
	const marker = "zzz-secret-pane-content-zzz"

	f := newOutputFixture(t, "$ env\nAWS_SECRET_ACCESS_KEY="+marker+"\n")
	answer, body := f.get(t, testTime)

	if answer.Code != http.StatusOK {
		t.Fatalf("status = %d (%q); want %d", answer.Code, answer.Body, http.StatusOK)
	}
	// The caller asked for the pane and must get it; without this the rest of the
	// test would pass just as well on a handler that returned nothing at all.
	text, ok := body["text"].(string)
	if !ok || !strings.Contains(text, marker) {
		t.Fatalf("text = %v; want the pane the caller asked for", body["text"])
	}

	if written := f.sink.String(); strings.Contains(written, marker) {
		t.Errorf("the audit trail carries pane content: %q", written)
	}
	if len(f.failed) != 0 {
		t.Errorf("the request reported %v; want nothing", f.failed)
	}

	rec := f.only(t)
	if rec["action"] != string(audit.ActionSessionOutput) {
		t.Errorf("action = %v; want %q", rec["action"], audit.ActionSessionOutput)
	}
	if rec["decision"] != string(audit.Allow) {
		t.Errorf("decision = %v; want %q", rec["decision"], audit.Allow)
	}
	if rec["session_id"] != f.live.ID {
		t.Errorf("session_id = %v; want %q", rec["session_id"], f.live.ID)
	}
	if reason, ok := rec["reason"]; ok {
		t.Errorf("an allowed capture recorded a reason: %v", reason)
	}
}

// TestOutputReadsThePaneTheRecordNames is FR-034 for this route: one command,
// against a target built from the resolved record's ID and from nothing else.
// The second session is there so that "the right pane" is a claim with something
// to be wrong about.
func TestOutputReadsThePaneTheRecordNames(t *testing.T) {
	t.Parallel()

	const otherMarker = "zzz-other-sessions-pane-zzz"

	f := newOutputFixture(t, "mine")
	other, _ := f.fixture.plant(t, session.Session{})
	f.fixture.tmux.SetPane(other.TmuxName(), otherMarker)

	answer, body := f.get(t, testTime)
	if answer.Code != http.StatusOK {
		t.Fatalf("status = %d (%q); want %d", answer.Code, answer.Body, http.StatusOK)
	}
	if got := body["text"]; got != "mine" {
		t.Errorf("text = %q; want this session's own pane", got)
	}
	if strings.Contains(answer.Body.String(), otherMarker) {
		t.Errorf("the response carries another session's pane: %q", answer.Body)
	}

	want := []string{"tmux", "capture-pane", "-p", "-t", f.live.PaneTarget()}
	got := f.fixture.tmux.Calls()
	if len(got) != 1 {
		t.Fatalf("the capture ran %v; want exactly one command", got)
	}
	if got[0].Op != tmuxctl.OpCapturePane || !slices.Equal(got[0].Argv, want) {
		t.Errorf("call = %s %v; want %s %v", got[0].Op, got[0].Argv, tmuxctl.OpCapturePane, want)
	}
}

// TestATmuxFailureDuringACaptureAnswersFiveHundredWithNoDetail: a pane that could
// not be read is a fact about the host, and the caller learns only that the
// request failed. What tmux said and what the pane held both stop here.
func TestATmuxFailureDuringACaptureAnswersFiveHundredWithNoDetail(t *testing.T) {
	t.Parallel()

	const tmuxMarker = "no-such-tmux-binary"
	const paneMarker = "zzz-secret-pane-content-zzz"

	f := newOutputFixture(t, paneMarker)
	f.fixture.tmux.FailOp(tmuxctl.OpCapturePane, errors.New(tmuxMarker))

	answer, _ := f.get(t, testTime)
	if answer.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d (%q); want %d", answer.Code, answer.Body, http.StatusInternalServerError)
	}
	if got := answer.Body.String(); got != string(bodyInternalError) {
		t.Errorf("body = %q; want %q", got, bodyInternalError)
	}

	rec := f.only(t)
	if rec["decision"] != string(audit.Deny) {
		t.Errorf("decision = %v; want %q", rec["decision"], audit.Deny)
	}
	if rec["reason"] != errOutputUncaptured.Error() {
		t.Errorf("reason = %v; want %q", rec["reason"], errOutputUncaptured.Error())
	}
	outward := f.sink.String() + answer.Body.String()
	if strings.Contains(outward, tmuxMarker) {
		t.Errorf("the tmux failure travelled outward: %q", outward)
	}
	if strings.Contains(outward, paneMarker) {
		t.Errorf("pane content travelled outward with the failure: %q", outward)
	}
}

// TestOutputRefusesARequestWithNoResolvedSession is unreachable through the
// router, for the reason its prompt twin is: the handler is checked directly to
// prove it fails closed rather than falling back to the {id} in the path.
func TestOutputRefusesARequestWithNoResolvedSession(t *testing.T) {
	t.Parallel()

	f := newOutputFixture(t, "secret")
	req := httptest.NewRequest(http.MethodGet, "/sessions/"+f.live.ID+"/output", nil)
	req.SetPathValue(pathValueID, f.live.ID)

	answer := httptest.NewRecorder()
	f.sessionOutput(answer, req)

	if answer.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d (%q); want %d", answer.Code, answer.Body, http.StatusInternalServerError)
	}
	if calls := f.fixture.tmux.Calls(); len(calls) != 0 {
		t.Errorf("a capture with no resolved session ran %v; want nothing", calls)
	}
}

// getSessions drives one signed list through the whole stack — signature,
// middleware, handler, store — and hands back the raw recorder as well as the
// decoded body, because half of what this route must be asked is about the bytes
// on the wire rather than the value a client decodes from them.
//
// There is no bearer credential, deliberately: GET /sessions names no session,
// so it is caller-scoped rather than session-scoped and layer 3 never runs
// (contracts/http-api.md). The signature is the whole of its authentication and
// the caller identity behind it is the whole of its authorisation.
//
// The instant is a parameter for the reason postSessionsAt takes one: two list
// requests to the same server carry the same (empty) body, so signed at the same
// second they would share a signature and the second would be refused as a
// replay.
func getSessions(t *testing.T, s *testServer, at time.Time) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()

	req := httptest.NewRequest(http.MethodGet, "/sessions", nil)
	signRequest(t, req, nil, at)

	answer := httptest.NewRecorder()
	s.ServeHTTP(answer, req)

	var decoded map[string]any
	if answer.Body.Len() > 0 {
		if err := json.Unmarshal(answer.Body.Bytes(), &decoded); err != nil {
			t.Fatalf("the response %q is not JSON: %v", answer.Body, err)
		}
	}
	return answer, decoded
}

// listed pulls the entries out of a list body, failing rather than returning
// nothing so that a response with no "sessions" array cannot pass as an empty
// fleet — which is the one wrong answer this route could give that looks exactly
// like a right one.
func listed(t *testing.T, body map[string]any) []map[string]any {
	t.Helper()

	raw, ok := body["sessions"]
	if !ok {
		t.Fatalf("the response has no %q field: %v", "sessions", body)
	}
	array, ok := raw.([]any)
	if !ok {
		t.Fatalf("sessions = %v (%T); want an array", raw, raw)
	}

	out := make([]map[string]any, 0, len(array))
	for i, e := range array {
		entry, ok := e.(map[string]any)
		if !ok {
			t.Fatalf("sessions[%d] = %v (%T); want an object", i, e, e)
		}
		out = append(out, entry)
	}
	return out
}

// ids reads the id off each entry, for the tests whose claim is about which
// sessions came back rather than about what each one says.
func ids(t *testing.T, entries []map[string]any) []string {
	t.Helper()

	out := make([]string, 0, len(entries))
	for i, entry := range entries {
		id, ok := entry["id"].(string)
		if !ok {
			t.Fatalf("sessions[%d].id = %v (%T); want a string", i, entry["id"], entry["id"])
		}
		out = append(out, id)
	}
	return out
}

// TestListAnswersTheContractResponse is contracts/http-api.md's list example,
// field by field, against a record whose values are all different from one
// another — last_activity is deliberately not created_at, so a handler that
// echoed one for the other would be caught rather than agreed with.
func TestListAnswersTheContractResponse(t *testing.T) {
	t.Parallel()

	s := newAuditedServer(t)
	used := testTime.Add(33 * time.Minute)
	live, _ := s.fixture.plant(t, session.Session{
		Name:         "refactor-auth",
		WorkDir:      s.fixture.repo,
		State:        session.StateRunning,
		LastActivity: used,
	})

	answer, body := getSessions(t, s, testTime)
	if answer.Code != http.StatusOK {
		t.Fatalf("status = %d (%q); want %d", answer.Code, answer.Body, http.StatusOK)
	}
	if ct := answer.Header().Get(headerContentType); ct != contentTypeJSON {
		t.Errorf("Content-Type = %q; want %q — every response is JSON", ct, contentTypeJSON)
	}

	entries := listed(t, body)
	if len(entries) != 1 {
		t.Fatalf("the list carries %d session(s) (%v); want exactly the one planted", len(entries), entries)
	}
	entry := entries[0]

	want := map[string]any{
		"id":            live.ID,
		"name":          "refactor-auth",
		"work_dir":      s.fixture.repo,
		"state":         "running",
		"created_at":    testTime.Format(time.RFC3339),
		"expires_at":    testTime.Add(24 * time.Hour).Format(time.RFC3339),
		"last_activity": used.Format(time.RFC3339),
		"adopted":       false,
	}
	for name, value := range want {
		if got := entry[name]; got != value {
			t.Errorf("%s = %v; want %v", name, got, value)
		}
	}

	// The contract's field set exactly: an entry carrying anything else is an
	// entry that has grown a field nobody reviewed, which on this route is how a
	// hash or a token would arrive.
	wantFields := slices.Sorted(maps.Keys(want))
	if fields := slices.Sorted(maps.Keys(entry)); !slices.Equal(fields, wantFields) {
		t.Errorf("entry fields = %v; want %v", fields, wantFields)
	}
	if len(body) != 1 {
		t.Errorf("the response carries %d fields (%v); the contract defines exactly one", len(body), body)
	}
}

// TestListReturnsOnlyTheCallersOwnSessions is FR-032 at this route, and it is
// the whole of T026. The second owner is synthetic — there is one operator
// today — but the ownership check is not, and milestone 2's Access identity
// arrives on a handler that already answers this correctly.
func TestListReturnsOnlyTheCallersOwnSessions(t *testing.T) {
	t.Parallel()

	const otherOwner auth.CallerID = "a-second-operator"
	const otherName = "zzz-other-owners-session-zzz"

	s := newAuditedServer(t)
	mine, _ := s.fixture.plant(t, session.Session{Name: "mine-first", WorkDir: s.fixture.repo})
	alsoMine, _ := s.fixture.plant(t, session.Session{
		Name:      "mine-second",
		WorkDir:   s.fixture.repo,
		CreatedAt: testTime.Add(time.Minute),
	})
	theirs, _ := s.fixture.plant(t, session.Session{
		Owner:   otherOwner,
		Name:    otherName,
		WorkDir: s.fixture.repo,
	})

	answer, body := getSessions(t, s, testTime)
	if answer.Code != http.StatusOK {
		t.Fatalf("status = %d (%q); want %d", answer.Code, answer.Body, http.StatusOK)
	}

	got := ids(t, listed(t, body))
	if want := []string{mine.ID, alsoMine.ID}; !slices.Equal(got, want) {
		t.Errorf("the list carries %v; want exactly the caller's own %v", got, want)
	}

	// Not just absent from the array: absent from the response altogether. An ID
	// that leaked in a count, a header, or a field nobody looked at would still
	// be a session the caller may not know exists.
	outward := answer.Body.String() + " " + fmt.Sprint(answer.Header())
	if strings.Contains(outward, theirs.ID) {
		t.Errorf("another owner's session id reached the caller: %q", outward)
	}
	if strings.Contains(outward, otherName) {
		t.Errorf("another owner's session name reached the caller: %q", outward)
	}
}

// TestListNeverCarriesATokenOrItsHash is the other half of T026 and FR-013. The
// plaintext exists only in the create response its owner already has; the hash
// is the daemon's, and a list that handed it out would be handing out the thing
// every session-scoped request is checked against.
func TestListNeverCarriesATokenOrItsHash(t *testing.T) {
	t.Parallel()

	s := newAuditedServer(t)
	live, issued := s.fixture.plant(t, session.Session{Name: "probe", WorkDir: s.fixture.repo})

	record, err := s.fixture.store.Get(live.ID, auth.CallerOperator)
	if err != nil {
		t.Fatalf("the planted session is not in the store: %v", err)
	}

	answer, body := getSessions(t, s, testTime)
	if answer.Code != http.StatusOK {
		t.Fatalf("status = %d (%q); want %d", answer.Code, answer.Body, http.StatusOK)
	}

	for _, entry := range listed(t, body) {
		if token, ok := entry["token"]; ok {
			t.Errorf("a list entry carries a token: %v", token)
		}
	}

	// Three spellings, because the field set assertion above only catches a field
	// somebody named honestly. The hash is checked as hex and as its own bytes;
	// the plaintext is checked because plant issued one and the record must be
	// the only thing that knows it.
	for name, needle := range map[string]string{
		"the plaintext token": issued,
		"the token hash":      hex.EncodeToString(record.TokenHash[:]),
	} {
		if strings.Contains(strings.ToLower(answer.Body.String()), strings.ToLower(needle)) {
			t.Errorf("the list carries %s: %q", name, answer.Body)
		}
	}
	if bytes.Contains(answer.Body.Bytes(), record.TokenHash[:]) {
		t.Errorf("the list carries the raw token hash: %q", answer.Body)
	}
	if written := s.sink.String(); strings.Contains(written, issued) {
		t.Errorf("the audit trail carries the token: %q", written)
	}
}

// TestAnEmptyFleetIsAnEmptyArray: a caller with no sessions gets [], never null.
// Go's zero slice marshals as null, so this is the difference between a contract
// a client can rely on and one it has to defend against — and it is a property
// of one `make` that nothing else in the file would notice losing.
func TestAnEmptyFleetIsAnEmptyArray(t *testing.T) {
	t.Parallel()

	s := newAuditedServer(t)
	answer, body := getSessions(t, s, testTime)

	if answer.Code != http.StatusOK {
		t.Fatalf("status = %d (%q); want %d", answer.Code, answer.Body, http.StatusOK)
	}
	if entries := listed(t, body); len(entries) != 0 {
		t.Errorf("a fresh server listed %v; want nothing", entries)
	}
	if got, want := answer.Body.String(), `{"sessions":[]}`; got != want {
		t.Errorf("body = %q; want %q — an empty fleet is an empty array, not a null", got, want)
	}
}

// TestListIsOldestFirstAndCarriesEachRecordAsItIs pins the two things a client
// reading this route repeatedly depends on: the order does not change between
// identical requests — map iteration is randomised, so without the store's sort
// it would — and each entry says what its own record says, adoption included.
func TestListIsOldestFirstAndCarriesEachRecordAsItIs(t *testing.T) {
	t.Parallel()

	s := newAuditedServer(t)
	oldest, _ := s.fixture.plant(t, session.Session{
		Name:      "adopted-at-startup",
		WorkDir:   s.fixture.repo,
		CreatedAt: testTime.Add(-2 * time.Hour),
		State:     session.StateRunning,
		Adopted:   true,
	})
	middle, _ := s.fixture.plant(t, session.Session{
		Name:      "middle",
		WorkDir:   s.fixture.repo,
		CreatedAt: testTime.Add(-time.Hour),
	})
	newest, _ := s.fixture.plant(t, session.Session{
		Name:    "newest",
		WorkDir: s.fixture.repo,
	})

	answer, body := getSessions(t, s, testTime)
	if answer.Code != http.StatusOK {
		t.Fatalf("status = %d (%q); want %d", answer.Code, answer.Body, http.StatusOK)
	}

	entries := listed(t, body)
	if want := []string{oldest.ID, middle.ID, newest.ID}; !slices.Equal(ids(t, entries), want) {
		t.Fatalf("the list is ordered %v; want oldest first, %v", ids(t, entries), want)
	}
	if got := entries[0]["adopted"]; got != true {
		t.Errorf("adopted = %v; want true — the record says the daemon did not start this one", got)
	}
	if got := entries[2]["adopted"]; got != false {
		t.Errorf("adopted = %v; want false", got)
	}
	if got, want := entries[0]["expires_at"], oldest.CreatedAt.Add(24*time.Hour).Format(time.RFC3339); got != want {
		t.Errorf("expires_at = %v; want %q — the deadline runs from the session's own creation", got, want)
	}
}

// TestListIsRecordedOnceUnderNoSessionID is FR-041 for this route. A list acts
// on no session, so the record names the caller and the action and stops there:
// stamping one of the returned IDs on it would make the trail claim an operation
// on a session that was only read about.
func TestListIsRecordedOnceUnderNoSessionID(t *testing.T) {
	t.Parallel()

	s := newAuditedServer(t)
	s.fixture.plant(t, session.Session{Name: "probe", WorkDir: s.fixture.repo})

	answer, _ := getSessions(t, s, testTime)
	if answer.Code != http.StatusOK {
		t.Fatalf("status = %d (%q); want %d", answer.Code, answer.Body, http.StatusOK)
	}

	rec := s.only(t)
	if rec["action"] != string(audit.ActionSessionList) {
		t.Errorf("action = %v; want %q", rec["action"], audit.ActionSessionList)
	}
	if rec["decision"] != string(audit.Allow) {
		t.Errorf("decision = %v; want %q", rec["decision"], audit.Allow)
	}
	if want := string(auth.CallerOperator); rec["caller"] != want {
		t.Errorf("caller = %v; want %q", rec["caller"], want)
	}
	if id, ok := rec["session_id"]; ok {
		t.Errorf("a list recorded a session_id: %v", id)
	}
	if reason, ok := rec["reason"]; ok {
		t.Errorf("an allowed list recorded a reason: %v", reason)
	}
	if len(s.failed) != 0 {
		t.Errorf("the request reported %v; want nothing", s.failed)
	}
}

// TestListCostsNoTmuxCommand: a list is a read of the daemon's own records. A
// handler that asked tmux about each session would make the fleet view fail
// whenever one window did, and would put the cost of a `list-sessions` exec on a
// route a dashboard is going to poll.
func TestListCostsNoTmuxCommand(t *testing.T) {
	t.Parallel()

	s := newAuditedServer(t)
	s.fixture.plant(t, session.Session{Name: "one", WorkDir: s.fixture.repo})
	s.fixture.plant(t, session.Session{Name: "two", WorkDir: s.fixture.repo})

	answer, _ := getSessions(t, s, testTime)
	if answer.Code != http.StatusOK {
		t.Fatalf("status = %d (%q); want %d", answer.Code, answer.Body, http.StatusOK)
	}
	if calls := s.fixture.tmux.Calls(); len(calls) != 0 {
		t.Errorf("the list ran %v; want nothing — it reads records, not the host", calls)
	}
}

// TestListRefusesARequestWithNoAuthenticatedCaller is unreachable through the
// router, which is the point: the handler is checked directly to prove it fails
// closed rather than listing against an empty identity. Store.List answers the
// zero CallerID with nothing today, so a handler that trusted that would look
// correct — until the day something else does.
func TestListRefusesARequestWithNoAuthenticatedCaller(t *testing.T) {
	t.Parallel()

	s := newAuditedServer(t)
	live, _ := s.fixture.plant(t, session.Session{Name: "probe", WorkDir: s.fixture.repo})

	answer := httptest.NewRecorder()
	s.listSessions(answer, httptest.NewRequest(http.MethodGet, "/sessions", nil))

	if answer.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d (%q); want %d", answer.Code, answer.Body, http.StatusInternalServerError)
	}
	if got := answer.Body.String(); got != string(bodyInternalError) {
		t.Errorf("body = %q; want %q", got, bodyInternalError)
	}
	if strings.Contains(answer.Body.String(), live.ID) {
		t.Errorf("a list with no caller answered with a session: %q", answer.Body)
	}
}

// detailCreated ages the fixture's session so that created_at and last_activity
// are different instants and a handler echoing either for the other is caught
// rather than agreed with.
//
// The distance is on the *creation* rather than on the activity because reaching
// a session through Manager.Resolve moves its idle clock — that is what stops the
// reaper destroying a session someone is using — so any planted last_activity is
// overwritten by the very request that reads it. Which makes detailUsed below the
// stronger fixture: it is not a value this file wrote, it is the one the daemon
// wrote while answering.
var detailCreated = testTime.Add(-33 * time.Minute)

// detailUsed is what last_activity must be after a detail request: the instant of
// that request, on the daemon's own clock.
var detailUsed = testTime

// detailFixture is a server holding one live session and the only copy of its
// credential — what every claim on GET /sessions/{id} starts from. The record is
// filled in rather than left to plant's defaults because this route's whole
// output is that record, so a field left zero would be a field this file could
// not tell apart from one the handler dropped.
type detailFixture struct {
	*testServer

	live  session.Session
	token string
}

func newDetailFixture(t *testing.T) detailFixture {
	t.Helper()

	s := newAuditedServer(t)
	live, issued := s.fixture.plant(t, session.Session{
		Name:         "refactor-auth",
		WorkDir:      s.fixture.repo,
		State:        session.StateRunning,
		CreatedAt:    detailCreated,
		LastActivity: detailCreated,
	})
	return detailFixture{testServer: s, live: live, token: issued}
}

// getSession drives one signed, credentialled detail through the whole stack and
// hands back the raw recorder as well as the decoded body, because half of what
// this route must be asked is about the bytes on the wire rather than the value
// a client decodes from them.
//
// The request comes from scopedRequest, which builds exactly this route — layer
// 3's tests drive it too — so a change to how a session-scoped request is spelled
// cannot leave these tests driving a shape nothing else does.
func getSession(t *testing.T, s *testServer, id, presented string, at time.Time) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()

	answer := httptest.NewRecorder()
	s.ServeHTTP(answer, scopedRequest(t, id, presented, at))

	var decoded map[string]any
	if answer.Body.Len() > 0 {
		if err := json.Unmarshal(answer.Body.Bytes(), &decoded); err != nil {
			t.Fatalf("the response %q is not JSON: %v", answer.Body, err)
		}
	}
	return answer, decoded
}

func (f detailFixture) get(t *testing.T, at time.Time) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	return getSession(t, f.testServer, f.live.ID, f.token, at)
}

// TestDetailAnswersTheContractResponse is contracts/http-api.md's detail body,
// field by field: the same object as one list entry, at the top level rather
// than inside an array.
func TestDetailAnswersTheContractResponse(t *testing.T) {
	t.Parallel()

	f := newDetailFixture(t)
	answer, body := f.get(t, testTime)

	if answer.Code != http.StatusOK {
		t.Fatalf("status = %d (%q); want %d", answer.Code, answer.Body, http.StatusOK)
	}
	if ct := answer.Header().Get(headerContentType); ct != contentTypeJSON {
		t.Errorf("Content-Type = %q; want %q — every response is JSON", ct, contentTypeJSON)
	}

	want := map[string]any{
		"id":            f.live.ID,
		"name":          "refactor-auth",
		"work_dir":      f.fixture.repo,
		"state":         "running",
		"created_at":    detailCreated.Format(time.RFC3339),
		"expires_at":    detailCreated.Add(24 * time.Hour).Format(time.RFC3339),
		"last_activity": detailUsed.Format(time.RFC3339),
		"adopted":       false,
	}
	for name, value := range want {
		if got := body[name]; got != value {
			t.Errorf("%s = %v; want %v", name, got, value)
		}
	}

	// The contract's field set exactly. A detail is the response a caller who
	// already holds the session's credential gets, which makes it the most
	// tempting place to add "just one more field" — and the field that would
	// arrive that way is the token hash.
	wantFields := slices.Sorted(maps.Keys(want))
	if fields := slices.Sorted(maps.Keys(body)); !slices.Equal(fields, wantFields) {
		t.Errorf("the response fields = %v; want %v", fields, wantFields)
	}
}

// TestADetailIsTheObjectTheListCarries is the contract's "same object shape as
// one list entry" asserted as sameness rather than as two lists of fields that
// happen to match today.
//
// It is the property entryFor exists for. Two renderers would pass every other
// test in this file and still let one route describe a session the other does
// not — which for a dashboard reading both is a fleet that disagrees with itself.
func TestADetailIsTheObjectTheListCarries(t *testing.T) {
	t.Parallel()

	f := newDetailFixture(t)

	_, detail := f.get(t, testTime)
	// A second later, because both requests carry an empty body: signed at the
	// same instant they would share a signature and the second would be refused
	// as a replay.
	_, list := getSessions(t, f.testServer, testTime.Add(-time.Second))

	entries := listed(t, list)
	if len(entries) != 1 {
		t.Fatalf("the list carries %d session(s) (%v); want exactly the one planted", len(entries), entries)
	}
	if !reflect.DeepEqual(detail, entries[0]) {
		t.Errorf("the detail is %v but the list entry is %v; the contract makes them one object", detail, entries[0])
	}
}

// TestDetailDescribesTheSessionTheCredentialNamed is FR-034 for this route. The
// caller's other session is older, so a handler that reached for a record rather
// than the resolved one — the store sorts oldest first — would answer with it
// and be caught.
func TestDetailDescribesTheSessionTheCredentialNamed(t *testing.T) {
	t.Parallel()

	const otherName = "zzz-the-callers-other-session-zzz"

	f := newDetailFixture(t)
	other, _ := f.fixture.plant(t, session.Session{
		Name:      otherName,
		WorkDir:   f.fixture.repo,
		CreatedAt: testTime.Add(-time.Hour),
	})

	answer, body := f.get(t, testTime)
	if answer.Code != http.StatusOK {
		t.Fatalf("status = %d (%q); want %d", answer.Code, answer.Body, http.StatusOK)
	}
	if got := body["id"]; got != f.live.ID {
		t.Errorf("id = %v; want %q — the resolved session and no other", got, f.live.ID)
	}
	if got := answer.Body.String(); strings.Contains(got, other.ID) || strings.Contains(got, otherName) {
		t.Errorf("the detail carries another session: %q", got)
	}
}

// TestDetailNeverCarriesATokenOrItsHash is FR-013 on the route where it is
// easiest to argue it does not matter — the caller already holds this session's
// token. It matters anyway: the plaintext is the caller's one copy and the daemon
// keeps none, and the hash is what every session-scoped request is checked
// against, so a route that handed it back would be handing back the check.
func TestDetailNeverCarriesATokenOrItsHash(t *testing.T) {
	t.Parallel()

	f := newDetailFixture(t)
	record, err := f.fixture.store.Get(f.live.ID, auth.CallerOperator)
	if err != nil {
		t.Fatalf("the planted session is not in the store: %v", err)
	}

	answer, body := f.get(t, testTime)
	if answer.Code != http.StatusOK {
		t.Fatalf("status = %d (%q); want %d", answer.Code, answer.Body, http.StatusOK)
	}
	if token, ok := body["token"]; ok {
		t.Errorf("the detail carries a token: %v", token)
	}

	// Three spellings, because the field-set assertion above only catches a field
	// somebody named honestly: the hash as hex and as its own bytes, and the
	// plaintext the caller presented — which arrives on this request and must not
	// come back on it.
	for name, needle := range map[string]string{
		"the presented token": f.token,
		"the token hash":      hex.EncodeToString(record.TokenHash[:]),
	} {
		if strings.Contains(strings.ToLower(answer.Body.String()), strings.ToLower(needle)) {
			t.Errorf("the detail carries %s: %q", name, answer.Body)
		}
	}
	if bytes.Contains(answer.Body.Bytes(), record.TokenHash[:]) {
		t.Errorf("the detail carries the raw token hash: %q", answer.Body)
	}
	if written := f.sink.String(); strings.Contains(written, f.token) {
		t.Errorf("the audit trail carries the token: %q", written)
	}
}

// TestDetailIsRecordedOnceUnderItsOwnAction is FR-041 for this route. A read is
// recorded as a read, under the daemon's own ID for the session the resolver
// matched — never the {id} the caller wrote, which is caller-supplied text the
// trail may not carry (FR-042).
func TestDetailIsRecordedOnceUnderItsOwnAction(t *testing.T) {
	t.Parallel()

	f := newDetailFixture(t)
	answer, _ := f.get(t, testTime)
	if answer.Code != http.StatusOK {
		t.Fatalf("status = %d (%q); want %d", answer.Code, answer.Body, http.StatusOK)
	}

	rec := f.only(t)
	if rec["action"] != string(audit.ActionSessionDetail) {
		t.Errorf("action = %v; want %q", rec["action"], audit.ActionSessionDetail)
	}
	if rec["decision"] != string(audit.Allow) {
		t.Errorf("decision = %v; want %q", rec["decision"], audit.Allow)
	}
	if want := string(auth.CallerOperator); rec["caller"] != want {
		t.Errorf("caller = %v; want %q", rec["caller"], want)
	}
	if rec["session_id"] != f.live.ID {
		t.Errorf("session_id = %v; want %q", rec["session_id"], f.live.ID)
	}
	if reason, ok := rec["reason"]; ok {
		t.Errorf("an allowed detail recorded a reason: %v", reason)
	}
	if len(f.failed) != 0 {
		t.Errorf("the request reported %v; want nothing", f.failed)
	}
}

// TestDetailCostsNoTmuxCommand: a detail is a read of the daemon's own record,
// for the reason a list is. A handler that asked tmux whether the window was
// still there would make this route fail exactly when an operator most needs to
// read it — and would put an exec on a route a dashboard is going to poll.
func TestDetailCostsNoTmuxCommand(t *testing.T) {
	t.Parallel()

	f := newDetailFixture(t)
	answer, _ := f.get(t, testTime)

	if answer.Code != http.StatusOK {
		t.Fatalf("status = %d (%q); want %d", answer.Code, answer.Body, http.StatusOK)
	}
	if calls := f.fixture.tmux.Calls(); len(calls) != 0 {
		t.Errorf("the detail ran %v; want nothing — it reads records, not the host", calls)
	}
}

// TestDetailRefusesARequestWithNoResolvedSession is unreachable through the
// router, which is the point: the handler is checked directly to prove it fails
// closed rather than falling back to the {id} in the path. That fallback is the
// one bug this route could have that would answer every request correctly right
// up until it described a session to someone who does not own it.
func TestDetailRefusesARequestWithNoResolvedSession(t *testing.T) {
	t.Parallel()

	f := newDetailFixture(t)
	req := httptest.NewRequest(http.MethodGet, "/sessions/"+f.live.ID, nil)
	req.SetPathValue(pathValueID, f.live.ID)

	answer := httptest.NewRecorder()
	f.sessionDetail(answer, req)

	if answer.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d (%q); want %d", answer.Code, answer.Body, http.StatusInternalServerError)
	}
	if got := answer.Body.String(); got != string(bodyInternalError) {
		t.Errorf("body = %q; want %q", got, bodyInternalError)
	}
	if strings.Contains(answer.Body.String(), f.live.ID) {
		t.Errorf("a detail with no resolved session answered with a session: %q", answer.Body)
	}
}

// --- DELETE /sessions/{id} (T029) --------------------------------------------

// destroyFixture is a server holding one live session and the only copy of its
// credential — what every claim on DELETE /sessions/{id} starts from. plant
// seeds the tmux session the record names, so the kill this route issues has
// something real to remove and the verification that follows it has something to
// find or not find.
type destroyFixture struct {
	*testServer

	live  session.Session
	token string
}

func newDestroyFixture(t *testing.T) destroyFixture {
	t.Helper()

	s := newAuditedServer(t)
	live, issued := s.fixture.plant(t, session.Session{
		Name:    "refactor-auth",
		WorkDir: s.fixture.repo,
		State:   session.StateRunning,
	})
	return destroyFixture{testServer: s, live: live, token: issued}
}

// deleteSession drives one signed, credentialled destroy through the whole
// stack, and hands back the raw recorder as well as the decoded body because
// half of what this route must be asked is about the bytes on the wire — the 409
// is the one place this API answers with something other than a uniform error,
// and its body is quoted in the contract.
func deleteSession(t *testing.T, s *testServer, id, presented string, at time.Time) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()

	req := httptest.NewRequest(http.MethodDelete, "/sessions/"+id, nil)
	signRequest(t, req, nil, at)
	if presented != "" {
		// After signing, for the reason scopedRequest does it: layer 3 is a
		// separate credential, not part of the layer-2 signature.
		req.Header.Set(headerAuthorization, bearerScheme+presented)
	}

	answer := httptest.NewRecorder()
	s.ServeHTTP(answer, req)

	var decoded map[string]any
	if answer.Body.Len() > 0 {
		if err := json.Unmarshal(answer.Body.Bytes(), &decoded); err != nil {
			t.Fatalf("the response %q is not JSON: %v", answer.Body, err)
		}
	}
	return answer, decoded
}

func (f destroyFixture) delete(t *testing.T, at time.Time) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	return deleteSession(t, f.testServer, f.live.ID, f.token, at)
}

// TestDestroyAnswersTheContractResponse is contracts/http-api.md's 200 body,
// field by field. Two fields and no more: an ID and a claim, with nowhere to put
// a token, a hash, or an account of what tmux did.
func TestDestroyAnswersTheContractResponse(t *testing.T) {
	t.Parallel()

	f := newDestroyFixture(t)
	answer, body := f.delete(t, testTime)

	if answer.Code != http.StatusOK {
		t.Fatalf("status = %d (%q); want %d", answer.Code, answer.Body, http.StatusOK)
	}
	if ct := answer.Header().Get(headerContentType); ct != contentTypeJSON {
		t.Errorf("Content-Type = %q; want %q — every response is JSON", ct, contentTypeJSON)
	}

	want := map[string]any{"id": f.live.ID, "destroyed": true}
	for name, value := range want {
		if got := body[name]; got != value {
			t.Errorf("%s = %v; want %v", name, got, value)
		}
	}
	wantFields := slices.Sorted(maps.Keys(want))
	if fields := slices.Sorted(maps.Keys(body)); !slices.Equal(fields, wantFields) {
		t.Errorf("the response fields = %v; want %v", fields, wantFields)
	}
	if strings.Contains(answer.Body.String(), f.token) {
		t.Errorf("the destroy carries the presented credential back: %q", answer.Body)
	}
}

// TestDestroyKillsTheSessionTheCredentialNamed is FR-034 for the one route where
// getting it wrong destroys something. The caller's other session is older, so a
// handler reaching for a record rather than the resolved one — the store sorts
// oldest first — would kill it and be caught.
//
// The verification is asserted as a command, not merely as an outcome: FR-019
// makes "gone" something the host said, and a Destroy that killed and returned
// would pass an outcome assertion while confirming nothing.
func TestDestroyKillsTheSessionTheCredentialNamed(t *testing.T) {
	t.Parallel()

	f := newDestroyFixture(t)
	other, _ := f.fixture.plant(t, session.Session{
		Name:      "zzz-the-callers-other-session-zzz",
		WorkDir:   f.fixture.repo,
		CreatedAt: testTime.Add(-time.Hour),
	})

	answer, _ := f.delete(t, testTime)
	if answer.Code != http.StatusOK {
		t.Fatalf("status = %d (%q); want %d", answer.Code, answer.Body, http.StatusOK)
	}

	want := []struct {
		op   tmuxctl.Op
		argv []string
	}{
		{tmuxctl.OpKill, []string{"tmux", "kill-session", "-t", f.live.SessionTarget()}},
		{tmuxctl.OpHas, []string{"tmux", "has-session", "-t", f.live.SessionTarget()}},
	}

	got := f.fixture.tmux.Calls()
	if len(got) != len(want) {
		t.Fatalf("the destroy ran %v; want the kill and the verification that follows it", got)
	}
	for i, w := range want {
		if got[i].Op != w.op || !slices.Equal(got[i].Argv, w.argv) {
			t.Errorf("call %d = %s %v; want %s %v", i, got[i].Op, got[i].Argv, w.op, w.argv)
		}
	}

	for _, c := range got {
		if slices.Contains(c.Argv, other.SessionTarget()) {
			t.Errorf("the destroy addressed another session: %v", c.Argv)
		}
	}
	if _, err := f.fixture.store.Get(other.ID, auth.CallerOperator); err != nil {
		t.Errorf("destroying one session removed another: %v", err)
	}
}

// TestAVerifiedTeardownClearsTheRecordAndItsCredential is FR-020. The record is
// what carries the token hash, so a destroy that killed the window and kept the
// record would leave a live credential for a session that no longer exists.
func TestAVerifiedTeardownClearsTheRecordAndItsCredential(t *testing.T) {
	t.Parallel()

	f := newDestroyFixture(t)
	answer, _ := f.delete(t, testTime)
	if answer.Code != http.StatusOK {
		t.Fatalf("status = %d (%q); want %d", answer.Code, answer.Body, http.StatusOK)
	}

	if _, err := f.fixture.store.Get(f.live.ID, auth.CallerOperator); !errors.Is(err, session.ErrSessionNotFound) {
		t.Errorf("the store still holds the destroyed session: %v", err)
	}
	if written := f.sink.String(); strings.Contains(written, f.token) {
		t.Errorf("the audit trail carries the credential of a destroyed session: %q", written)
	}
}

// TestASurvivingSessionAnswersConflict is FR-019's whole point, in the three
// shapes a teardown fails in. Two of them are a session that is demonstrably
// still there; the third is one nobody can ask about, which counts as surviving
// because Principle VI does not let an unanswered question be reported as a
// teardown.
//
// The record must be kept in all three. A record is the only thing carrying an
// owner and two deadlines for a session that may still be running, and adoption
// runs at startup — so a record dropped here is a live unsandboxed shell the
// running daemon has forgotten for good.
func TestASurvivingSessionAnswersConflict(t *testing.T) {
	t.Parallel()

	const tmuxMarker = "no-such-tmux-binary"

	cases := map[string]func(f destroyFixture){
		"the kill reported success and the session is still there": func(f destroyFixture) {
			f.fixture.tmux.SurviveKill(f.live.TmuxName())
		},
		"the kill itself failed": func(f destroyFixture) {
			f.fixture.tmux.FailOp(tmuxctl.OpKill, errors.New(tmuxMarker))
		},
		"tmux cannot say whether it is gone": func(f destroyFixture) {
			// Both, because confirmGone falls back to List when Has cannot
			// answer: a host whose last session just died reports "no server
			// running" to has-session, and reading that as absence would let a
			// broken tmux confirm every teardown.
			f.fixture.tmux.FailOp(tmuxctl.OpHas, errors.New(tmuxMarker))
			f.fixture.tmux.FailOp(tmuxctl.OpList, errors.New(tmuxMarker))
		},
	}

	for name, arrange := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			f := newDestroyFixture(t)
			arrange(f)

			answer, _ := f.delete(t, testTime)
			if answer.Code != http.StatusConflict {
				t.Fatalf("status = %d (%q); want %d — an unverified teardown is not a teardown",
					answer.Code, answer.Body, http.StatusConflict)
			}
			if got := answer.Body.String(); got != string(bodyTeardownUnverified) {
				t.Errorf("body = %q; want %q", got, bodyTeardownUnverified)
			}
			if ct := answer.Header().Get(headerContentType); ct != contentTypeJSON {
				t.Errorf("Content-Type = %q; want %q", ct, contentTypeJSON)
			}

			if _, err := f.fixture.store.Get(f.live.ID, auth.CallerOperator); err != nil {
				t.Errorf("the record of a session that may still be running was dropped: %v", err)
			}

			// Prominent means findable: the trail's own name for this operation,
			// a refusal, the daemon's ID for the session, and the one reason that
			// says a live unsandboxed shell may exist.
			rec := f.only(t)
			if rec["action"] != string(audit.ActionSessionDestroy) {
				t.Errorf("action = %v; want %q", rec["action"], audit.ActionSessionDestroy)
			}
			if rec["decision"] != string(audit.Deny) {
				t.Errorf("decision = %v; want %q", rec["decision"], audit.Deny)
			}
			if rec["reason"] != errDestroyOrphaned.Error() {
				t.Errorf("reason = %v; want %q", rec["reason"], errDestroyOrphaned.Error())
			}
			if rec["session_id"] != f.live.ID {
				t.Errorf("session_id = %v; want %q", rec["session_id"], f.live.ID)
			}

			outward := f.sink.String() + answer.Body.String()
			if strings.Contains(outward, tmuxMarker) {
				t.Errorf("the tmux failure travelled outward: %q", outward)
			}
		})
	}
}

// TestASessionThatVanishedOnItsOwnIsDestroyed: a window whose shell already
// exited is gone, and the caller asked for it to be gone. tmux answers the kill
// with "can't find session", which is an ordinary outcome on this path and not a
// failure — only the verification decides.
func TestASessionThatVanishedOnItsOwnIsDestroyed(t *testing.T) {
	t.Parallel()

	f := newDestroyFixture(t)
	f.fixture.tmux.Vanish(f.live.TmuxName())

	answer, body := f.delete(t, testTime)
	if answer.Code != http.StatusOK {
		t.Fatalf("status = %d (%q); want %d", answer.Code, answer.Body, http.StatusOK)
	}
	if got := body["destroyed"]; got != true {
		t.Errorf("destroyed = %v; want true", got)
	}
	if _, err := f.fixture.store.Get(f.live.ID, auth.CallerOperator); !errors.Is(err, session.ErrSessionNotFound) {
		t.Errorf("the store still holds a session confirmed gone: %v", err)
	}
}

// TestASecondDestroyIsTheUnknownAnswer pins the decision rather than the
// accident. Destroy is not idempotent: the record is gone, so layer 3 refuses
// the second request exactly as it refuses an ID that never existed — and it has
// to, because a 200 for a session the daemon has no record of would mean the
// resolver telling "destroyed" apart from "not yours", which is the difference
// FR-033 closes.
func TestASecondDestroyIsTheUnknownAnswer(t *testing.T) {
	t.Parallel()

	f := newDestroyFixture(t)

	// Three distinct instants, because all three requests carry an empty body:
	// signed alike they would share a signature and the later ones would be
	// refused as replays.
	first, _ := f.delete(t, testTime)
	if first.Code != http.StatusOK {
		t.Fatalf("the first destroy = %d (%q); want %d", first.Code, first.Body, http.StatusOK)
	}

	unknown, err := session.NewID()
	if err != nil {
		t.Fatalf("session.NewID = _, %v; want an id", err)
	}
	again, _ := deleteSession(t, f.testServer, f.live.ID, f.token, testTime.Add(-time.Second))
	never, _ := deleteSession(t, f.testServer, unknown, f.token, testTime.Add(-2*time.Second))

	if again.Code != http.StatusNotFound {
		t.Errorf("the second destroy = %d (%q); want %d", again.Code, again.Body, http.StatusNotFound)
	}
	if again.Code != never.Code || again.Body.String() != never.Body.String() {
		t.Errorf("a destroyed session answers %d %q while an unknown one answers %d %q; the two must be identical",
			again.Code, again.Body, never.Code, never.Body)
	}
	if got := again.Body.String(); got != string(bodyNotFound) {
		t.Errorf("body = %q; want %q", got, bodyNotFound)
	}
}

// TestDestroyIsRecordedOnceUnderItsOwnAction is FR-041 for this route, and the
// action is the one data-model.md already names. The session ID is the daemon's
// own, off the record the resolver matched — never the {id} the caller wrote,
// which is caller-supplied text the trail may not carry (FR-042).
func TestDestroyIsRecordedOnceUnderItsOwnAction(t *testing.T) {
	t.Parallel()

	f := newDestroyFixture(t)
	answer, _ := f.delete(t, testTime)
	if answer.Code != http.StatusOK {
		t.Fatalf("status = %d (%q); want %d", answer.Code, answer.Body, http.StatusOK)
	}

	rec := f.only(t)
	if rec["action"] != string(audit.ActionSessionDestroy) {
		t.Errorf("action = %v; want %q", rec["action"], audit.ActionSessionDestroy)
	}
	if rec["decision"] != string(audit.Allow) {
		t.Errorf("decision = %v; want %q", rec["decision"], audit.Allow)
	}
	if want := string(auth.CallerOperator); rec["caller"] != want {
		t.Errorf("caller = %v; want %q", rec["caller"], want)
	}
	if rec["session_id"] != f.live.ID {
		t.Errorf("session_id = %v; want %q", rec["session_id"], f.live.ID)
	}
	if reason, ok := rec["reason"]; ok {
		t.Errorf("a verified teardown recorded a reason: %v", reason)
	}
	if len(f.failed) != 0 {
		t.Errorf("the request reported %v; want nothing", f.failed)
	}
}

// TestDestroyRefusesARequestWithNoResolvedSession is unreachable through the
// router, which is the point: the handler is checked directly to prove it fails
// closed rather than falling back to the {id} in the path. On this route that
// fallback would kill a window on a caller's say-so, so the assertion that
// matters is not the 500 but that tmux was never asked to do anything.
func TestDestroyRefusesARequestWithNoResolvedSession(t *testing.T) {
	t.Parallel()

	f := newDestroyFixture(t)
	req := httptest.NewRequest(http.MethodDelete, "/sessions/"+f.live.ID, nil)
	req.SetPathValue(pathValueID, f.live.ID)

	answer := httptest.NewRecorder()
	f.destroySession(answer, req)

	if answer.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d (%q); want %d", answer.Code, answer.Body, http.StatusInternalServerError)
	}
	if got := answer.Body.String(); got != string(bodyInternalError) {
		t.Errorf("body = %q; want %q", got, bodyInternalError)
	}
	if calls := f.fixture.tmux.Calls(); len(calls) != 0 {
		t.Errorf("a destroy with no resolved session ran %v; want nothing", calls)
	}
	if _, err := f.fixture.store.Get(f.live.ID, auth.CallerOperator); err != nil {
		t.Errorf("a destroy with no resolved session dropped a record: %v", err)
	}
}

// TestAFailedResponseWriteIsReported: AGENTS.md bans a swallowed error, and a
// 201 that never reached the socket is a session the caller does not know it
// owns — with the only copy of its token lost on the way out.
func TestAFailedResponseWriteIsReported(t *testing.T) {
	t.Parallel()

	want := errors.New("the connection went away")
	s := newAuditedServer(t)

	body := createBody(s.fixture)
	req := httptest.NewRequest(http.MethodPost, "/sessions", bytes.NewReader(body))
	signRequest(t, req, body, testTime)
	s.ServeHTTP(&failingWriter{err: want}, req)

	if len(s.failed) != 1 {
		t.Fatalf("the failed write was reported %d times; want exactly 1", len(s.failed))
	}
	if !errors.Is(s.failed[0], want) {
		t.Errorf("reported %v; want the write failure wrapped", s.failed[0])
	}
}
