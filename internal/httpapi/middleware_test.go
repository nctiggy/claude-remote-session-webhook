// Internal test, matching server_test.go. The middleware is reached through the
// router in most of these, but three of the properties are only visible from
// inside: the route→action table, the pending audit record a handler amends, and
// the failure sink that a broken audit write reports to.
package httpapi

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/nctiggy/claude-remote-session-webhook/internal/audit"
	"github.com/nctiggy/claude-remote-session-webhook/internal/auth"
	"github.com/nctiggy/claude-remote-session-webhook/internal/config"
)

// testMaxBody is deliberately far below the 64 KiB default so the oversize case
// is a cheap test rather than a slow one.
const testMaxBody = 1024

// testTime is the instant in contracts/http-api.md's signing example, so a
// signature computed here can be checked against the document by hand.
var testTime = time.Unix(1785706480, 0).UTC()

// testSecret is spelled in words and not in hex: a run of hex digits of this
// length is what a real HMAC key looks like, and gitleaks — correctly — refuses
// to let one into the repository.
func testSecret() []byte { return []byte("test-only-shared-secret-32-bytes") }

func testConfig(listen string) *config.Config {
	return &config.Config{
		Listen:       listen,
		SharedSecret: testSecret(),
		MaxBodyBytes: testMaxBody,
	}
}

type fixedClock struct{ at time.Time }

func (c fixedClock) Now() time.Time { return c.at }

func testAuth(t *testing.T) *auth.Authenticator {
	t.Helper()
	a, err := auth.NewWithClock(testSecret(), testMaxBody, fixedClock{at: testTime})
	if err != nil {
		t.Fatalf("auth.NewWithClock = _, %v; want an authenticator", err)
	}
	return a
}

func testTrail(t *testing.T) *audit.Logger {
	t.Helper()
	return audit.NewTo(io.Discard, func() time.Time { return testTime })
}

// signRequest computes the layer-2 credential the same way the contract
// documents it, from first principles rather than by calling the auth package's
// own signer — a test that signs with the code under test proves only that the
// code agrees with itself.
func signRequest(t *testing.T, r *http.Request, body []byte, at time.Time) {
	t.Helper()

	ts := strconv.FormatInt(at.Unix(), 10)
	mac := hmac.New(sha256.New, testSecret())
	if _, err := mac.Write([]byte(ts + "." + string(body))); err != nil {
		t.Fatalf("sign the test request: %v", err)
	}

	r.Header.Set(auth.HeaderTimestamp, ts)
	r.Header.Set(auth.HeaderSignature, "sha256="+hex.EncodeToString(mac.Sum(nil)))
}

// testServer is a Server whose audit trail and failure reports can be read back.
// The sink is named for what it is rather than shadowing Server.trail, which is
// the Logger writing into it.
type testServer struct {
	*Server
	sink   *bytes.Buffer
	failed []error
}

func newAuditedServer(t *testing.T) *testServer {
	t.Helper()

	buf := &bytes.Buffer{}
	s, err := newServer(
		testConfig(loopbackListen),
		net.Listen,
		testAuth(t),
		audit.NewTo(buf, func() time.Time { return testTime }),
	)
	if err != nil {
		t.Fatalf("newServer = _, %v; want a server", err)
	}

	ts := &testServer{Server: s, sink: buf}
	s.report = func(err error) { ts.failed = append(ts.failed, err) }
	return ts
}

// records decodes everything written to the trail so far.
func (s *testServer) records(t *testing.T) []map[string]any {
	t.Helper()

	var out []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(s.sink.String()), "\n") {
		if line == "" {
			continue
		}
		var rec map[string]any
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Fatalf("audit line %q is not JSON: %v", line, err)
		}
		out = append(out, rec)
	}
	return out
}

// only asserts that a request produced exactly one record — FR-041's whole
// claim — and returns it.
func (s *testServer) only(t *testing.T) map[string]any {
	t.Helper()

	got := s.records(t)
	if len(got) != 1 {
		t.Fatalf("the request emitted %d audit records (%v); FR-041 requires exactly one", len(got), got)
	}
	return got[0]
}

// pathFor fills the {id} wildcard with something ID-shaped. No session exists,
// which is fine: every test here stops at or just past the middleware.
func pathFor(route Route) string {
	return strings.ReplaceAll(route.Pattern, "{id}", "0123456789abcdef0123456789abcdef")
}

// TestEveryRegisteredRouteRefusesAnUnauthenticatedRequest is the sweep FR-007
// exists for, and it iterates the router rather than a list written here on
// purpose: a hand-written list is exactly the thing a seventh route would be
// forgotten from, and on this daemon a forgotten route is an unauthenticated
// door to unsandboxed execution.
func TestEveryRegisteredRouteRefusesAnUnauthenticatedRequest(t *testing.T) {
	t.Parallel()

	s := newTestServer(t, loopbackListen)
	routes := s.Routes()
	if len(routes) == 0 {
		t.Fatal("the router registered no routes, so this sweep would pass vacuously")
	}

	for _, route := range routes {
		rec := httptest.NewRecorder()
		s.ServeHTTP(rec, httptest.NewRequest(route.Method, pathFor(route), nil))

		if rec.Code != http.StatusUnauthorized {
			t.Errorf("%s = %d; want %d — the route is not behind the middleware",
				route, rec.Code, http.StatusUnauthorized)
		}
		if got := rec.Body.String(); got != string(bodyUnauthorized) {
			t.Errorf("%s body = %q; want %q", route, got, bodyUnauthorized)
		}
		if got := rec.Header().Get(headerContentType); got != contentTypeJSON {
			t.Errorf("%s Content-Type = %q; want %q", route, got, contentTypeJSON)
		}
	}
}

// layer2Failures is every way a request can fail authentication. Each must
// produce the same answer as all the others (FR-011, SC-001).
func layer2Failures(t *testing.T) map[string]*http.Request {
	t.Helper()

	newReq := func(body []byte) *http.Request {
		return httptest.NewRequest(http.MethodPost, "/sessions", bytes.NewReader(body))
	}
	signed := func(body []byte, at time.Time) *http.Request {
		r := newReq(body)
		signRequest(t, r, body, at)
		return r
	}

	valid := []byte(`{"name":"probe"}`)

	noTimestamp := signed(valid, testTime)
	noTimestamp.Header.Del(auth.HeaderTimestamp)

	noSignature := signed(valid, testTime)
	noSignature.Header.Del(auth.HeaderSignature)

	malformed := signed(valid, testTime)
	malformed.Header.Set(auth.HeaderTimestamp, "the day before yesterday")

	wrongSignature := signed(valid, testTime)
	wrongSignature.Header.Set(auth.HeaderSignature, "sha256="+strings.Repeat("0", 64))

	// Signed over one body, sent with another — the case a signature over the
	// method and path alone would wave through.
	tampered := signed(valid, testTime)
	tampered.Body = io.NopCloser(bytes.NewReader([]byte(`{"name":"something-else"}`)))

	return map[string]*http.Request{
		"no credential at all": newReq(valid),
		"no timestamp":         noTimestamp,
		"no signature":         noSignature,
		"malformed timestamp":  malformed,
		"stale timestamp":      signed(valid, testTime.Add(-301*time.Second)),
		"future timestamp":     signed(valid, testTime.Add(301*time.Second)),
		"wrong signature":      wrongSignature,
		"tampered body":        tampered,
		"oversize body":        signed(bytes.Repeat([]byte("a"), testMaxBody+1), testTime),
	}
}

// TestEveryLayer2FailureAnswersTheIdenticalResponse is FR-011 stated as bytes.
// A caller must not be able to tell a forged signature from a stale clock from a
// replay: each difference is a probe that tells an attacker which half of the
// credential it already has right.
func TestEveryLayer2FailureAnswersTheIdenticalResponse(t *testing.T) {
	t.Parallel()

	s := newAuditedServer(t)
	answers := map[string]*httptest.ResponseRecorder{}
	for name, req := range layer2Failures(t) {
		rec := httptest.NewRecorder()
		s.ServeHTTP(rec, req)
		answers[name] = rec
	}

	// The replay has to be made against the same server, after a first use.
	first := httptest.NewRequest(http.MethodGet, "/sessions", nil)
	signRequest(t, first, nil, testTime)
	firstAnswer := httptest.NewRecorder()
	s.ServeHTTP(firstAnswer, first)
	if firstAnswer.Code != http.StatusNotImplemented {
		t.Fatalf("the first use of the signed request = %d; want %d, or the replay case proves nothing",
			firstAnswer.Code, http.StatusNotImplemented)
	}

	replay := httptest.NewRequest(http.MethodGet, "/sessions", nil)
	signRequest(t, replay, nil, testTime)
	replayed := httptest.NewRecorder()
	s.ServeHTTP(replayed, replay)
	answers["replayed request"] = replayed

	for name, rec := range answers {
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("%s = %d; want %d", name, rec.Code, http.StatusUnauthorized)
		}
		if got := rec.Body.String(); got != string(bodyUnauthorized) {
			t.Errorf("%s body = %q; want %q", name, got, bodyUnauthorized)
		}
	}

	// Not "each looks right" but "no two differ": headers included, since a
	// Content-Length or a WWW-Authenticate present on one and not another
	// distinguishes them just as well as a body would.
	names := slices.Sorted(maps.Keys(answers))
	for _, name := range names[1:] {
		a, b := answers[names[0]], answers[name]
		if !reflect.DeepEqual(a.Header(), b.Header()) {
			t.Errorf("%q answered with headers %v but %q answered with %v; the denial must be uniform",
				names[0], a.Header(), name, b.Header())
		}
		if !bytes.Equal(a.Body.Bytes(), b.Body.Bytes()) {
			t.Errorf("%q answered %q but %q answered %q; the denial must be uniform",
				names[0], a.Body, name, b.Body)
		}
	}
}

// TestTheDenialTellsTheCallerNothingAboutWhichCheckFailed is the other half of
// FR-011: the reason is recorded, and the recording must not travel out with the
// response.
func TestTheDenialTellsTheCallerNothingAboutWhichCheckFailed(t *testing.T) {
	t.Parallel()

	leaks := []string{"timestamp", "signature", "replay", "already", "window", "skew", "body", "secret"}

	s := newAuditedServer(t)
	for name, req := range layer2Failures(t) {
		rec := httptest.NewRecorder()
		s.ServeHTTP(rec, req)

		answer := strings.ToLower(rec.Body.String() + " " + fmt.Sprint(rec.Header()))
		for _, word := range leaks {
			if strings.Contains(answer, word) {
				t.Errorf("the %s denial mentions %q: %q", name, word, answer)
			}
		}
	}
}

// TestARejectedRequestIsAuditedWithItsRealReason pins the pairing FR-011 is
// really about: one uniform answer outward, the specific cause recorded inward.
func TestARejectedRequestIsAuditedWithItsRealReason(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		mutate func(t *testing.T, r *http.Request)
		reason error
	}{
		"no timestamp": {
			mutate: func(t *testing.T, r *http.Request) { t.Helper(); r.Header.Del(auth.HeaderTimestamp) },
			reason: auth.ErrMissingTimestamp,
		},
		"no signature": {
			mutate: func(t *testing.T, r *http.Request) { t.Helper(); r.Header.Del(auth.HeaderSignature) },
			reason: auth.ErrMissingSignature,
		},
		"a forged signature": {
			mutate: func(t *testing.T, r *http.Request) {
				t.Helper()
				r.Header.Set(auth.HeaderSignature, "sha256="+strings.Repeat("0", 64))
			},
			reason: auth.ErrSignatureMismatch,
		},
	}

	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			s := newAuditedServer(t)
			req := httptest.NewRequest(http.MethodGet, "/sessions", nil)
			signRequest(t, req, nil, testTime)
			c.mutate(t, req)
			s.ServeHTTP(httptest.NewRecorder(), req)

			rec := s.only(t)
			if rec["action"] != string(audit.ActionAuthReject) {
				t.Errorf("action = %v; want %q", rec["action"], audit.ActionAuthReject)
			}
			if rec["decision"] != string(audit.Deny) {
				t.Errorf("decision = %v; want %q", rec["decision"], audit.Deny)
			}
			if rec["caller"] != audit.CallerUnknown {
				t.Errorf("caller = %v; want %q — no identity was established", rec["caller"], audit.CallerUnknown)
			}
			if rec["reason"] != c.reason.Error() {
				t.Errorf("reason = %v; want %q", rec["reason"], c.reason)
			}
			if rec["remote"] == nil || rec["remote"] == "" {
				t.Error("the record names no peer; an operator reading the trail cannot tell where the attempt came from")
			}
		})
	}
}

// TestEveryRouteAuditsAnAllowedRequestUnderItsOwnAction sweeps the router again,
// this time with a valid credential. "What happened" is the question the trail
// answers, so a read must not be recorded as a create.
func TestEveryRouteAuditsAnAllowedRequestUnderItsOwnAction(t *testing.T) {
	t.Parallel()

	for i, route := range newTestServer(t, loopbackListen).Routes() {
		t.Run(route.String(), func(t *testing.T) {
			t.Parallel()

			s := newAuditedServer(t)
			req := httptest.NewRequest(route.Method, pathFor(route), nil)
			// A distinct instant per route: the signature covers the timestamp
			// and the body only, so six identical empty-bodied requests would
			// share a signature and all but the first would be replays.
			signRequest(t, req, nil, testTime.Add(-time.Duration(i)*time.Second))

			rec := httptest.NewRecorder()
			s.ServeHTTP(rec, req)
			if rec.Code != http.StatusNotImplemented {
				t.Fatalf("%s = %d; want %d — a signed request must reach the handler",
					route, rec.Code, http.StatusNotImplemented)
			}

			got := s.only(t)
			if want := wantActions[route]; got["action"] != want {
				t.Errorf("action = %v; want %q", got["action"], want)
			}
			if got["decision"] != string(audit.Allow) {
				t.Errorf("decision = %v; want %q", got["decision"], audit.Allow)
			}
			if want := string(auth.CallerOperator); got["caller"] != want {
				t.Errorf("caller = %v; want %q", got["caller"], want)
			}
		})
	}
}

// wantActions is the operation each route is recorded as, written out here as
// literals rather than read from routeActions. A test that asks the table what
// the table says proves only that the code agrees with itself; these strings are
// what an operator greps the trail for, so they are pinned against the contract
// and not against the source.
var wantActions = map[Route]string{
	{http.MethodPost, "/sessions"}:             "session.create",
	{http.MethodGet, "/sessions"}:              "session.list",
	{http.MethodGet, "/sessions/{id}"}:         "session.detail",
	{http.MethodDelete, "/sessions/{id}"}:      "session.destroy",
	{http.MethodPost, "/sessions/{id}/prompt"}: "session.prompt",
	{http.MethodGet, "/sessions/{id}/output"}:  "session.output",
}

// TestEveryContractRouteHasAnAuditAction is what makes "one record per request"
// survive a seventh route: the router, the table, and the contract are checked
// against each other rather than any of them being trusted.
func TestEveryContractRouteHasAnAuditAction(t *testing.T) {
	t.Parallel()

	registered := newTestServer(t, loopbackListen).Routes()
	for _, route := range registered {
		action, ok := routeActions[route]
		if !ok {
			t.Errorf("route %s has no audit action, so its traffic would be invisible in the trail", route)
			continue
		}
		if want := wantActions[route]; string(action) != want {
			t.Errorf("%s is audited as %q; want %q", route, action, want)
		}
	}
	if len(routeActions) != len(registered) {
		t.Errorf("routeActions has %d entries for %d registered routes; the table names a route the router does not serve",
			len(routeActions), len(registered))
	}

	// Distinct actions, so two operations cannot be confused for one another in
	// the trail an operator greps.
	seen := map[audit.Action]Route{}
	for route, action := range routeActions {
		if other, dup := seen[action]; dup {
			t.Errorf("%s and %s both audit as %q", route, other, action)
		}
		seen[action] = route
	}
}

// TestHandleRefusesARouteWithNoAuditAction is the startup half of the same
// guarantee. A route that cannot be recorded is not registered at all.
func TestHandleRefusesARouteWithNoAuditAction(t *testing.T) {
	t.Parallel()

	s := newTestServer(t, loopbackListen)
	before := len(s.Routes())

	if err := s.handle(Route{http.MethodGet, "/healthz"}, s.notImplemented); err == nil {
		t.Fatal("registered a route with no audit action; want a refusal")
	}
	if after := len(s.Routes()); after != before {
		t.Errorf("the refused route was still recorded: %d routes, was %d", after, before)
	}

	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if rec.Code != http.StatusNotFound {
		t.Errorf("GET /healthz = %d; the refused route reached the mux anyway", rec.Code)
	}
}

// TestAnAllowedRequestCarriesTheCallerIntoTheHandler covers the seam T022 builds
// on. Identity is derived server-side (FR-012), so a handler must take it from
// here and never from the request.
func TestAnAllowedRequestCarriesTheCallerIntoTheHandler(t *testing.T) {
	t.Parallel()

	s := newAuditedServer(t)

	var got *auth.Caller
	var ok bool
	probe := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		got, ok = CallerFrom(r.Context())
	})

	req := httptest.NewRequest(http.MethodGet, "/sessions", nil)
	signRequest(t, req, nil, testTime)
	s.authenticate(audit.ActionSessionList, probe).ServeHTTP(httptest.NewRecorder(), req)

	if !ok {
		t.Fatal("the handler found no caller in the request context")
	}
	if got.ID != auth.CallerOperator {
		t.Errorf("caller = %q; want %q", got.ID, auth.CallerOperator)
	}
}

func TestCallerFromAnUnauthenticatedContextReportsNoCaller(t *testing.T) {
	t.Parallel()

	if caller, ok := CallerFrom(context.Background()); ok || caller != nil {
		t.Errorf("CallerFrom(background) = %v, %v; want nil, false", caller, ok)
	}
	//nolint:staticcheck // SA1029 is the point: a key of another type must not be readable as ours.
	ctx := context.WithValue(context.Background(), "caller", &auth.Caller{ID: "impostor"})
	if caller, ok := CallerFrom(ctx); ok || caller != nil {
		t.Errorf("a caller planted under a foreign key was read back as %v, %v", caller, ok)
	}
}

// TestAHandlerCanAmendTheOneRecord covers what the pending record is for: T023's
// ownership refusal and T029's unverified teardown are the two outcomes an
// operator most needs in the trail, and neither is known until the handler runs.
func TestAHandlerCanAmendTheOneRecord(t *testing.T) {
	t.Parallel()

	const (
		sessionID = "9f2c1ab47e0d4f6a8b3c5d7e1f0a2b4c"
		reason    = "session is owned by another caller"
	)

	s := newAuditedServer(t)
	probe := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		ra := AuditFrom(r.Context())
		ra.SetSessionID(sessionID)
		ra.Deny(reason)
	})

	req := httptest.NewRequest(http.MethodGet, "/sessions/"+sessionID, nil)
	signRequest(t, req, nil, testTime)
	s.authenticate(audit.ActionSessionDetail, probe).ServeHTTP(httptest.NewRecorder(), req)

	got := s.only(t)
	if got["session_id"] != sessionID {
		t.Errorf("session_id = %v; want %q", got["session_id"], sessionID)
	}
	if got["decision"] != string(audit.Deny) {
		t.Errorf("decision = %v; want %q — the handler refused", got["decision"], audit.Deny)
	}
	if got["reason"] != reason {
		t.Errorf("reason = %v; want %q", got["reason"], reason)
	}
	if got["action"] != string(audit.ActionSessionDetail) {
		t.Errorf("action = %v; want %q — amending must not rename the operation",
			got["action"], audit.ActionSessionDetail)
	}
}

// TestAuditFromIsSafeOutsideTheMiddleware keeps a handler under direct unit test
// from having to nil-check before it can record anything.
func TestAuditFromIsSafeOutsideTheMiddleware(t *testing.T) {
	t.Parallel()

	ra := AuditFrom(context.Background())
	if ra != nil {
		t.Fatalf("AuditFrom(background) = %v; want nil", ra)
	}
	ra.SetSessionID("abc")
	ra.Deny("nothing to record")
}

// TestAPanickingHandlerStillProducesARecord is why the emit is deferred. A
// request that crashed the handler is precisely the one an operator needs to
// find in the trail.
func TestAPanickingHandlerStillProducesARecord(t *testing.T) {
	t.Parallel()

	s := newAuditedServer(t)
	boom := http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panic("handler exploded") })

	req := httptest.NewRequest(http.MethodPost, "/sessions", nil)
	signRequest(t, req, nil, testTime)

	func() {
		defer func() {
			if recovered := recover(); recovered == nil {
				t.Error("the panic did not propagate; net/http would not have seen it either")
			}
		}()
		s.authenticate(audit.ActionSessionCreate, boom).ServeHTTP(httptest.NewRecorder(), req)
	}()

	if got := s.only(t); got["action"] != string(audit.ActionSessionCreate) {
		t.Errorf("action = %v; want %q", got["action"], audit.ActionSessionCreate)
	}
}

// brokenSink is an audit destination that cannot be written to — a closed
// stdout, a full pipe.
type brokenSink struct{ err error }

func (s brokenSink) Write([]byte) (int, error) { return 0, s.err }

// TestAFailedAuditWriteIsReportedAndChangesNothingElse is the ruling this task
// owed: the answer a caller gets must depend on the request alone. A 500 that
// appeared only when stdout broke would make the uniform 401 non-uniform and
// turn the trail into a side channel — but the failure must not be silent
// either, because FR-041 makes the record mandatory.
func TestAFailedAuditWriteIsReportedAndChangesNothingElse(t *testing.T) {
	t.Parallel()

	want := errors.New("stdout is gone")
	s := newAuditedServer(t)
	s.Server.trail = audit.NewTo(brokenSink{err: want}, func() time.Time { return testTime })

	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/sessions", nil))

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("= %d; want %d — a broken audit sink must not change the answer", rec.Code, http.StatusUnauthorized)
	}
	if got := rec.Body.String(); got != string(bodyUnauthorized) {
		t.Errorf("body = %q; want %q", got, bodyUnauthorized)
	}
	if len(s.failed) != 1 {
		t.Fatalf("the failed audit write was reported %d times; want exactly 1 — FR-041 makes the record mandatory", len(s.failed))
	}
	if !errors.Is(s.failed[0], want) {
		t.Errorf("reported %v; want the write failure wrapped", s.failed[0])
	}
}

// TestABodyAtTheLimitIsAcceptedAndOneByteOverIsRefused pins the ruling on where
// the size limit sits.
//
// It is enforced *before* verification and the refusal is the uniform 401, not
// the 400 contracts/http-api.md promises for an oversize body. That is not a
// choice so much as an consequence: the signature covers the bytes as received,
// so a body the daemon refused to finish reading is a body whose signature
// cannot be computed — and reading an unbounded one first, for a caller who has
// not authenticated, is the denial of service the limit exists to prevent.
// T021's MaxBytesReader is then defence in depth rather than the thing that
// produces the 400.
func TestABodyAtTheLimitIsAcceptedAndOneByteOverIsRefused(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		size int
		want int
	}{
		"one byte under the limit": {size: testMaxBody - 1, want: http.StatusNotImplemented},
		"exactly at the limit":     {size: testMaxBody, want: http.StatusNotImplemented},
		"one byte over the limit":  {size: testMaxBody + 1, want: http.StatusUnauthorized},
	}

	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			body := bytes.Repeat([]byte("a"), c.size)
			req := httptest.NewRequest(http.MethodPost, "/sessions", bytes.NewReader(body))
			signRequest(t, req, body, testTime)

			s := newAuditedServer(t)
			rec := httptest.NewRecorder()
			s.ServeHTTP(rec, req)

			if rec.Code != c.want {
				t.Errorf("a %d-byte body = %d; want %d", c.size, rec.Code, c.want)
			}
			// Still exactly one record, and still no body bytes in it.
			if got := s.only(t); strings.Contains(fmt.Sprint(got), "aaaa") {
				t.Errorf("the audit record carries body bytes: %v", got)
			}
		})
	}
}

// TestTheAuditTrailCarriesNoCredentialAndNoBody is a narrow standing check on
// the one record this package writes; T039 does the exhaustive version across
// every operation. It is here because the middleware is the one place that holds
// the signature, the body, and the bearer token at the same moment.
func TestTheAuditTrailCarriesNoCredentialAndNoBody(t *testing.T) {
	t.Parallel()

	const (
		promptText = "run the tests and summarise the failures"

		// Named neutrally and spelled in words: gosec G101 fires on an
		// identifier that says "token" beside a string literal, and gitleaks on
		// anything hex-shaped. This is the fixture whose *absence* the test is
		// about, not a credential.
		presented = "a-bearer-value-that-must-never-be-recorded"
	)

	s := newAuditedServer(t)
	body := []byte(`{"text":"` + promptText + `"}`)

	for i, valid := range []bool{true, false} {
		req := httptest.NewRequest(http.MethodPost, "/sessions/abc/prompt", bytes.NewReader(body))
		signRequest(t, req, body, testTime.Add(-time.Duration(i)*time.Second))
		req.Header.Set("Authorization", "Bearer "+presented)
		if !valid {
			req.Header.Set(auth.HeaderSignature, "sha256="+strings.Repeat("0", 64))
		}
		s.ServeHTTP(httptest.NewRecorder(), req)
	}

	written := s.sink.String()
	forbidden := map[string]string{
		"the prompt text":     promptText,
		"the bearer token":    presented,
		"the shared secret":   string(testSecret()),
		"the signed body":     string(body),
		"the request headers": auth.HeaderSignature,
	}
	for what, secret := range forbidden {
		if strings.Contains(written, secret) {
			t.Errorf("the audit trail contains %s: %q", what, written)
		}
	}
}
