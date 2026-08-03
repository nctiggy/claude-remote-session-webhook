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
	mgr, err := session.NewManagerWithClock(
		fake, store, []config.ApprovedRoot{{Path: root}}, fixedClock{at: testTime},
	)
	if err != nil {
		t.Fatalf("session.NewManagerWithClock = _, %v; want a manager", err)
	}

	return sessionFixture{mgr: mgr, tmux: fake, store: store, root: root, repo: repo}
}

// createBody is the well-formed create every test here starts from, varied per
// case by the callers that need to.
func createBody(f sessionFixture) []byte {
	return []byte(`{"name":"refactor-auth","work_dir":"` + f.repo + `"}`)
}

// bodyFor is what a route sweep should send to reach a route's handler: a real
// create body for the one route that has one, nothing for the five that do not.
func bodyFor(f sessionFixture, route Route) []byte {
	if route == (Route{Method: http.MethodPost, Pattern: "/sessions"}) {
		return createBody(f)
	}
	return nil
}

// reachedStatus is what each route answers once a signed request carrying
// bodyFor's body has got past layer 2.
//
// It is a literal table rather than one constant because the six no longer
// answer alike: T022 gave POST /sessions a handler, so it answers 201 where the
// five unimplemented routes still answer 501. Each later task moves one row, and
// what the sweeps assert through it is unchanged — the request reached a
// handler, which is only possible through the middleware.
var reachedStatus = map[Route]int{
	{Method: http.MethodPost, Pattern: "/sessions"}:             http.StatusCreated,
	{Method: http.MethodGet, Pattern: "/sessions"}:              http.StatusNotImplemented,
	{Method: http.MethodGet, Pattern: "/sessions/{id}"}:         http.StatusNotImplemented,
	{Method: http.MethodDelete, Pattern: "/sessions/{id}"}:      http.StatusNotImplemented,
	{Method: http.MethodPost, Pattern: "/sessions/{id}/prompt"}: http.StatusNotImplemented,
	{Method: http.MethodGet, Pattern: "/sessions/{id}/output"}:  http.StatusNotImplemented,
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
		if argv := strings.Join(call.Argv, " "); strings.Contains(argv, "refactor-auth") {
			t.Errorf("the caller's name reached tmux: %q", argv)
		}
	}
	wantOps := []tmuxctl.Op{tmuxctl.OpNew, tmuxctl.OpSetOption, tmuxctl.OpSetOption, tmuxctl.OpSendKeys}
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
