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

	"github.com/nctiggy/claude-remote-session-webhook/internal/access"
	"github.com/nctiggy/claude-remote-session-webhook/internal/audit"
	"github.com/nctiggy/claude-remote-session-webhook/internal/auth"
	"github.com/nctiggy/claude-remote-session-webhook/internal/config"
	"github.com/nctiggy/claude-remote-session-webhook/internal/session"
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

// testRoot is the allowlist entry a fixture Config carries so that New can build
// a session manager at all. It is deliberately a path that does not exist: a
// server built through New here is never asked to create a session, and a root
// nothing can resolve under is the spelling of that which fails closed. The
// tests that do create a session build a manager over a real temp directory —
// see newSessionFixture.
const testRoot = "/nonexistent-crswd-test-root"

func testConfig(listen string) *config.Config {
	return &config.Config{
		Listen:       listen,
		SharedSecret: testSecret(),
		MaxBodyBytes: testMaxBody,
		Roots:        []config.ApprovedRoot{{Path: testRoot}},
		// The production default. A zero here would be a Config no Load ever
		// produced, and New refuses one — a session manager that may run no
		// sessions is not a daemon.
		MaxSessions: config.DefaultMaxSessions,

		// Deliberately not the production default, which is the opposite choice
		// from MaxSessions above and for the same reason: quickstart.md's cap
		// check needs six creates through one server to reach the cap, and a
		// fixture carrying the real rate would refuse the fourth of them as a
		// burst instead. Tests about the rate build their own limiter — see
		// ratelimit_test.go.
		CreateRatePerMin: rateNotUnderTest,

		// Layer 1's configuration, which config.Load has demanded since T001 and
		// New now builds a validator from. The team domain carries its scheme
		// because that is the normalised form loadTeamDomain returns, and a
		// fixture spelled the way no Load ever spells it is the thing the
		// MaxSessions note above warns about.
		//
		// The values need only be well-formed: nothing built through this fixture
		// presents an assertion, and browser_test.go points a validator at a key
		// server it controls rather than at this hostname.
		AccessTeamDomain:    "https://example-team.cloudflareaccess.com",
		AccessAUD:           "test-only-audience-tag",
		AccessAllowedEmails: []string{"operator@example.com"},
	}
}

// rateNotUnderTest is a create budget no test in this package can exhaust by
// accident, so that a 429 anywhere else is the concurrent-session cap and
// nothing else.
const rateNotUnderTest = 1000

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

// errStubRefuses is what testBrowser's layer 1 answers with. It is spelled here
// rather than reused from internal/access because that package's sentinels are
// unexported on purpose — a caller that could name which check refused is one
// step from putting it in a response.
var errStubRefuses = errors.New("the stub layer 1 admits nobody")

// stubLayer1 is a browser door with a fixed answer, for the tests that are not
// about layer 1 but still have to build a Server.
type stubLayer1 struct {
	operator *access.VerifiedOperator
	err      error
}

func (s stubLayer1) Verify(context.Context, string) (*access.VerifiedOperator, error) {
	return s.operator, s.err
}

// testBrowser is the layer 1 a milestone 1 test gets: something non-nil for
// newServer to accept, which admits nobody.
//
// Refusing rather than admitting is the deliberate default. Every test in this
// file drives the API door, where an assertion is neither read nor required
// (FR-012), so a fixture that admitted every browser could only ever make a
// future dashboard test pass for a reason it had not earned. browser_test.go
// builds a real *access.Validator over a locally generated key pair.
func testBrowser() layer1 { return stubLayer1{err: errStubRefuses} }

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
	// METHOD "\n" PATH "\n" timestamp "." body — the signature names what it
	// authorizes, so one signed read is not a valid destroy at the same instant.
	if _, err := mac.Write([]byte(r.Method + "\n" + r.URL.EscapedPath() + "\n" + ts + "." + string(body))); err != nil {
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

	// fixture is the session half — the tmux fake, the store, and the real
	// approved root the manager was built on. Named for what it is rather than
	// shadowing Server.sessions, which is the Manager standing on it.
	fixture sessionFixture
}

func newAuditedServer(t *testing.T) *testServer {
	t.Helper()
	return newAuditedServerWith(t, testBrowser())
}

// newAuditedServerWith is newAuditedServer with layer 1 chosen by the caller,
// which is what browser_test.go needs and no test of the API door does: that
// door reads no assertion, so the validator behind it never runs.
func newAuditedServerWith(t *testing.T, browser layer1) *testServer {
	t.Helper()

	buf := &bytes.Buffer{}
	fixture := newSessionFixture(t)
	cfg := testConfig(loopbackListen)
	s, err := newServer(
		cfg,
		net.Listen,
		testAuth(t),
		browser,
		audit.NewTo(buf, func() time.Time { return testTime }),
		fixture.mgr,
		testLimiter(t, cfg.CreateRatePerMin, fixedClock{at: testTime}),
	)
	if err != nil {
		t.Fatalf("newServer = _, %v; want a server", err)
	}

	ts := &testServer{Server: s, sink: buf, fixture: fixture}
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
// which is fine for a request that is meant to be refused before anything looks
// one up.
func pathFor(route Route) string {
	return strings.ReplaceAll(route.Pattern, "{id}", "0123456789abcdef0123456789abcdef")
}

// requestFor builds the signed request a sweep needs to reach a route's handler.
//
// Since T023 a session-scoped route stops at the layer-3 resolver unless the
// request names a session the caller owns and carries the credential issued for
// it, so this plants one and presents it. That keeps the sweeps asserting what
// they always asserted — the handler was reached, which is only possible through
// the middleware — rather than being weakened to accept the resolver's 404.
func requestFor(t *testing.T, s *testServer, route Route, at time.Time) *http.Request {
	t.Helper()

	path, credential := pathFor(route), ""
	if route.SessionScoped() {
		live, issued := s.fixture.plant(t, session.Session{})
		path = strings.ReplaceAll(route.Pattern, "{id}", live.ID)
		credential = bearerScheme + issued
	}

	body := bodyFor(s.fixture, route)
	req := httptest.NewRequest(route.Method, path, bytes.NewReader(body))
	signRequest(t, req, body, at)
	if credential != "" {
		// After signing, because the signature covers the timestamp and the body
		// and nothing else — layer 3 is a separate credential, not part of one.
		req.Header.Set(headerAuthorization, credential)
	}
	return req
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

	// The replay has to be made against the same server, after a first use. What
	// the first use answers is read out of reachedStatus rather than written
	// here, because the only thing this assertion needs is that it was *allowed*
	// — T026 moved this route from 501 to 200, and a literal would have made that
	// look like a failure of the replay cache.
	listRoute := Route{Method: http.MethodGet, Pattern: "/sessions"}
	first := httptest.NewRequest(http.MethodGet, "/sessions", nil)
	signRequest(t, first, nil, testTime)
	firstAnswer := httptest.NewRecorder()
	s.ServeHTTP(firstAnswer, first)
	if firstAnswer.Code != reachedStatus[listRoute] {
		t.Fatalf("the first use of the signed request = %d; want %d, or the replay case proves nothing",
			firstAnswer.Code, reachedStatus[listRoute])
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
			// A distinct instant per route: the signature covers the timestamp
			// and the body only, so six identical empty-bodied requests would
			// share a signature and all but the first would be replays.
			req := requestFor(t, s, route, testTime.Add(-time.Duration(i)*time.Second))

			rec := httptest.NewRecorder()
			s.ServeHTTP(rec, req)
			if want := reachedStatus[route]; rec.Code != want {
				t.Fatalf("%s = %d; want %d — a signed request must reach the handler",
					route, rec.Code, want)
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

	// Signed, because every path now passes through layer 2 first and an unsigned
	// probe would answer 401 whether or not the route had been registered — which
	// would make this assertion pass for the wrong reason. With the signature, a
	// registered /healthz would answer as its handler, and the uniform 404 is
	// proof that nothing but the catch-all is there.
	signed := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	signRequest(t, signed, nil, testTime)

	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, signed)
	if rec.Code != http.StatusNotFound {
		t.Errorf("GET /healthz = %d; the refused route reached the mux anyway", rec.Code)
	}
	if body := rec.Body.String(); body != string(bodyNotFound) {
		t.Errorf("GET /healthz body = %q; want the uniform %q", body, bodyNotFound)
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

// scopedRoute is the route the layer-3 tests drive. Any of the four would do —
// TestEverySessionScopedRouteIsBehindTheResolver sweeps them all — and this one
// is the read, so a case that somehow got past the resolver would be asking for
// another session's detail, which is the failure SC-005 is about.
var scopedRoute = Route{Method: http.MethodGet, Pattern: "/sessions/{id}"}

// scopedRequest is one request against a session ID with a bearer credential of
// the caller's choosing, signed at a distinct instant so that several of them
// can be driven through one server without the second becoming a replay.
func scopedRequest(t *testing.T, id, presented string, at time.Time) *http.Request {
	t.Helper()

	req := httptest.NewRequest(scopedRoute.Method, "/sessions/"+id, nil)
	signRequest(t, req, nil, at)
	if presented != "" {
		req.Header.Set(headerAuthorization, bearerScheme+presented)
	}
	return req
}

// layer3Failures is every way a session-scoped request can fail authorisation,
// each built against the same server so the answers are comparable.
//
// The synthetic second owner is how a single-operator milestone tests
// cross-owner isolation at all: the ownership check exists from day one
// (Resolved decisions, IMPLEMENTATION_PLAN.md) precisely so that milestone 2's
// second identity does not arrive to find it missing.
func layer3Failures(t *testing.T, s *testServer) map[string]*http.Request {
	t.Helper()

	const otherOwner auth.CallerID = "a-second-operator"

	mine, issued := s.fixture.plant(t, session.Session{})
	theirs, theirCredential := s.fixture.plant(t, session.Session{Owner: otherOwner})
	expired, expiredCredential := s.fixture.plant(t, session.Session{
		// Created 25 hours ago on the fixture's fixed clock, so its 24-hour
		// deadline is an hour in the past at testTime.
		CreatedAt: testTime.Add(-25 * time.Hour),
	})
	atTheDeadline, deadlineCredential := s.fixture.plant(t, session.Session{
		CreatedAt: testTime.Add(-session.AbsoluteLifetime),
	})
	dead, deadCredential := s.fixture.plant(t, session.Session{State: session.StateDead})

	// An ID of the right shape that was never issued. The unknown-ID answer is
	// the one every other case here must be indistinguishable from.
	unknown, err := session.NewID()
	if err != nil {
		t.Fatalf("session.NewID = _, %v; want an id", err)
	}

	// Spelled in words rather than hex: gitleaks refuses a hex run of credential
	// length into the repository, and this is a value whose *rejection* is the
	// point.
	const neverIssued = "a-value-that-was-never-issued-for-any-session"

	at := func(i int) time.Time { return testTime.Add(-time.Duration(i) * time.Second) }
	return map[string]*http.Request{
		"an unknown session":              scopedRequest(t, unknown, issued, at(1)),
		"another owner's session":         scopedRequest(t, theirs.ID, theirCredential, at(2)),
		"another owner's id, own token":   scopedRequest(t, theirs.ID, issued, at(3)),
		"own id, another session's token": scopedRequest(t, mine.ID, theirCredential, at(4)),
		"a credential never issued":       scopedRequest(t, mine.ID, neverIssued, at(5)),
		"no credential at all":            scopedRequest(t, mine.ID, "", at(6)),
		"an expired credential":           scopedRequest(t, expired.ID, expiredCredential, at(7)),
		"a credential at the deadline":    scopedRequest(t, atTheDeadline.ID, deadlineCredential, at(8)),
		"a dead session":                  scopedRequest(t, dead.ID, deadCredential, at(9)),
	}
}

// TestEveryLayer3FailureAnswersTheIdenticalNotFound is FR-033 stated as bytes,
// and it is the reason the resolver has one exit rather than five.
//
// An unknown ID, someone else's ID, the right ID with the wrong credential, and
// a credential that has aged out must be one answer. Any difference — a status,
// a body, a header, a Content-Length — is an oracle that turns a session ID into
// something worth guessing at, and behind an ID is an unsandboxed shell.
func TestEveryLayer3FailureAnswersTheIdenticalNotFound(t *testing.T) {
	t.Parallel()

	s := newAuditedServer(t)
	answers := map[string]*httptest.ResponseRecorder{}
	for name, req := range layer3Failures(t, s) {
		rec := httptest.NewRecorder()
		s.ServeHTTP(rec, req)
		answers[name] = rec
	}

	for name, rec := range answers {
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s = %d; want %d", name, rec.Code, http.StatusNotFound)
		}
		if got := rec.Body.String(); got != string(bodyNotFound) {
			t.Errorf("%s body = %q; want %q", name, got, bodyNotFound)
		}
		if got := rec.Header().Get(headerContentType); got != contentTypeJSON {
			t.Errorf("%s Content-Type = %q; want %q", name, got, contentTypeJSON)
		}
	}

	// Not "each looks right" but "no two differ", headers included.
	names := slices.Sorted(maps.Keys(answers))
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
	}
}

// TestALayer3RefusalTellsTheCallerNothingAboutWhichCheckFailed is the other half
// of FR-033: the reason is recorded, and the recording must not travel out with
// the response.
func TestALayer3RefusalTellsTheCallerNothingAboutWhichCheckFailed(t *testing.T) {
	t.Parallel()

	leaks := []string{"owner", "expired", "credential", "token", "dead", "match", "session id"}

	s := newAuditedServer(t)
	for name, req := range layer3Failures(t, s) {
		rec := httptest.NewRecorder()
		s.ServeHTTP(rec, req)

		answer := strings.ToLower(rec.Body.String() + " " + fmt.Sprint(rec.Header()))
		for _, word := range leaks {
			if strings.Contains(answer, word) {
				t.Errorf("the refusal for %s mentions %q: %q", name, word, answer)
			}
		}
	}
}

// TestALayer3RefusalIsAuditedWithItsRealReason pins the pairing the uniform 404
// is really about: one answer outward, the specific cause recorded inward. Every
// reason here is a fixed string authored in this repo — never the {id} the
// caller sent, which is why the record is worth having at all (FR-042).
func TestALayer3RefusalIsAuditedWithItsRealReason(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		plant  session.Session
		mutate func(id, issued string) (string, string)
		reason error
	}{
		"an unknown session": {
			mutate: func(_, issued string) (string, string) {
				return "0123456789abcdef0123456789abcdef", issued
			},
			reason: session.ErrSessionNotFound,
		},
		"another owner's session": {
			plant:  session.Session{Owner: "a-second-operator"},
			reason: session.ErrSessionNotFound,
		},
		"a credential never issued": {
			mutate: func(id, _ string) (string, string) {
				return id, "a-value-that-was-never-issued-for-any-session"
			},
			reason: session.ErrTokenMismatch,
		},
		"no credential at all": {
			mutate: func(id, _ string) (string, string) { return id, "" },
			reason: errScopeNoCredential,
		},
		"an expired credential": {
			plant:  session.Session{CreatedAt: testTime.Add(-25 * time.Hour)},
			reason: session.ErrTokenExpired,
		},
		"a dead session": {
			plant:  session.Session{State: session.StateDead},
			reason: session.ErrSessionDead,
		},
	}

	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			s := newAuditedServer(t)
			planted, issued := s.fixture.plant(t, c.plant)
			id, presented := planted.ID, issued
			if c.mutate != nil {
				id, presented = c.mutate(planted.ID, issued)
			}
			s.ServeHTTP(httptest.NewRecorder(), scopedRequest(t, id, presented, testTime))

			rec := s.only(t)
			if rec["decision"] != string(audit.Deny) {
				t.Errorf("decision = %v; want %q", rec["decision"], audit.Deny)
			}
			if rec["reason"] != c.reason.Error() {
				t.Errorf("reason = %v; want %q", rec["reason"], c.reason)
			}
			// The action stays the operation that was attempted: a refused read
			// is still a read, and renaming it would hide it from the operator
			// grepping for one.
			if rec["action"] != string(audit.ActionSessionDetail) {
				t.Errorf("action = %v; want %q", rec["action"], audit.ActionSessionDetail)
			}
			if got, ok := rec["session_id"]; ok && got != planted.ID {
				t.Errorf("session_id = %v; a refusal may record the daemon's own id or none, never %q", got, id)
			}
		})
	}
}

// TestTheResolvedSessionReachesTheHandler covers the seam T024–T029 build on. A
// handler must take the session from here and never from the path (FR-034).
func TestTheResolvedSessionReachesTheHandler(t *testing.T) {
	t.Parallel()

	s := newAuditedServer(t)
	planted, issued := s.fixture.plant(t, session.Session{})

	var got session.Session
	var ok bool
	probe := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		got, ok = SessionFrom(r.Context())
	})

	req := scopedRequest(t, planted.ID, issued, testTime)
	req.SetPathValue(pathValueID, planted.ID)
	s.authenticate(audit.ActionSessionDetail, s.resolveSession(probe)).ServeHTTP(httptest.NewRecorder(), req)

	if !ok {
		t.Fatal("the handler found no session in the request context")
	}
	if got.ID != planted.ID {
		t.Errorf("session = %q; want %q", got.ID, planted.ID)
	}
	if got.Owner != auth.CallerOperator {
		t.Errorf("owner = %q; want %q — the handler was handed a record it may act on", got.Owner, auth.CallerOperator)
	}
	if got.TokenHash != planted.TokenHash {
		t.Error("the handler was handed a record whose credential hash is not the planted one")
	}
}

func TestSessionFromAnUnresolvedContextReportsNoSession(t *testing.T) {
	t.Parallel()

	if got, ok := SessionFrom(context.Background()); ok || got.ID != "" {
		t.Errorf("SessionFrom(background) = %v, %v; want the zero session, false", got, ok)
	}
	//nolint:staticcheck // SA1029 is the point: a key of another type must not be readable as ours.
	ctx := context.WithValue(context.Background(), "session", session.Session{ID: "impostor"})
	if got, ok := SessionFrom(ctx); ok || got.ID != "" {
		t.Errorf("a session planted under a foreign key was read back as %v, %v", got, ok)
	}
}

// TestTheResolvedSessionIsAuditedUnderTheDaemonsOwnID is FR-042 at the one place
// the trail could most easily pick up caller bytes: the {id} in the path.
func TestTheResolvedSessionIsAuditedUnderTheDaemonsOwnID(t *testing.T) {
	t.Parallel()

	s := newAuditedServer(t)
	planted, issued := s.fixture.plant(t, session.Session{})
	s.ServeHTTP(httptest.NewRecorder(), scopedRequest(t, planted.ID, issued, testTime))

	rec := s.only(t)
	if rec["session_id"] != planted.ID {
		t.Errorf("session_id = %v; want %q", rec["session_id"], planted.ID)
	}
	if rec["decision"] != string(audit.Allow) {
		t.Errorf("decision = %v; want %q — the credential matched", rec["decision"], audit.Allow)
	}
	if written := s.sink.String(); strings.Contains(written, issued) {
		t.Errorf("the audit trail carries the bearer credential: %q", written)
	}
}

// TestEverySessionScopedRouteIsBehindTheResolver sweeps the real router for the
// same reason the layer-2 sweep does: FR-014 admits no exempt {id} route, and a
// hand-written list is exactly what a seventh one would be forgotten from. Here
// a forgotten route is a session drivable by anyone holding the shared secret.
func TestEverySessionScopedRouteIsBehindTheResolver(t *testing.T) {
	t.Parallel()

	scoped := 0
	for i, route := range newTestServer(t, loopbackListen).Routes() {
		if !route.SessionScoped() {
			continue
		}
		scoped++

		t.Run(route.String(), func(t *testing.T) {
			t.Parallel()

			s := newAuditedServer(t)
			planted, _ := s.fixture.plant(t, session.Session{})

			// Signed, owned, and pointed at a real session — everything except
			// the session credential. Only layer 3 can refuse this.
			body := bodyFor(s.fixture, route)
			path := strings.ReplaceAll(route.Pattern, "{id}", planted.ID)
			req := httptest.NewRequest(route.Method, path, bytes.NewReader(body))
			signRequest(t, req, body, testTime.Add(-time.Duration(i)*time.Second))

			rec := httptest.NewRecorder()
			s.ServeHTTP(rec, req)

			if rec.Code != http.StatusNotFound {
				t.Errorf("%s with no bearer credential = %d; want %d — the route is not behind the resolver",
					route, rec.Code, http.StatusNotFound)
			}
			if got := rec.Body.String(); got != string(bodyNotFound) {
				t.Errorf("%s body = %q; want %q", route, got, bodyNotFound)
			}
		})
	}

	if scoped == 0 {
		t.Fatal("the router registered no session-scoped routes, so this sweep would pass vacuously")
	}
}

// TestAnUnscopedRouteNeedsNoSessionCredential is the complement: layer 3 applies
// to the routes that name a session and to no others. POST /sessions cannot
// require a session credential — it is where the credential comes from — and
// GET /sessions is scoped to the caller rather than to a session.
func TestAnUnscopedRouteNeedsNoSessionCredential(t *testing.T) {
	t.Parallel()

	for i, route := range newTestServer(t, loopbackListen).Routes() {
		if route.SessionScoped() {
			continue
		}

		t.Run(route.String(), func(t *testing.T) {
			t.Parallel()

			s := newAuditedServer(t)
			body := bodyFor(s.fixture, route)
			req := httptest.NewRequest(route.Method, route.Pattern, bytes.NewReader(body))
			signRequest(t, req, body, testTime.Add(-time.Duration(i)*time.Second))

			rec := httptest.NewRecorder()
			s.ServeHTTP(rec, req)

			if want := reachedStatus[route]; rec.Code != want {
				t.Errorf("%s with no bearer credential = %d; want %d — the route is not session-scoped",
					route, rec.Code, want)
			}
		})
	}
}

// TestTheCredentialSchemeIsReadStrictly pins how the Authorization header is
// parsed. The scheme is case-insensitive per RFC 7235; everything else about the
// value is not, because a second accepted spelling of a credential is a second
// credential.
func TestTheCredentialSchemeIsReadStrictly(t *testing.T) {
	t.Parallel()

	accepted := map[string]func(issued string) string{
		"the scheme as written": func(issued string) string { return "Bearer " + issued },
		"a lowercase scheme":    func(issued string) string { return "bearer " + issued },
		"an uppercase scheme":   func(issued string) string { return "BEARER " + issued },
	}
	refused := map[string]func(issued string) string{
		"no scheme at all":       func(issued string) string { return issued },
		"another scheme":         func(issued string) string { return "Token " + issued },
		"basic credentials":      func(issued string) string { return "Basic " + issued },
		"no separating space":    func(issued string) string { return "Bearer" + issued },
		"two separating spaces":  func(issued string) string { return "Bearer  " + issued },
		"a trailing space":       func(issued string) string { return "Bearer " + issued + " " },
		"the scheme and nothing": func(string) string { return "Bearer " },
		"an empty header":        func(string) string { return "" },
	}

	drive := func(t *testing.T, header func(issued string) string) int {
		t.Helper()

		s := newAuditedServer(t)
		planted, issued := s.fixture.plant(t, session.Session{})

		req := httptest.NewRequest(scopedRoute.Method, "/sessions/"+planted.ID, nil)
		signRequest(t, req, nil, testTime)
		req.Header.Set(headerAuthorization, header(issued))

		rec := httptest.NewRecorder()
		s.ServeHTTP(rec, req)
		return rec.Code
	}

	// Read out of reachedStatus rather than written here, so that a task giving
	// this route a different answer moves one row instead of teaching a layer-3
	// test what a handler returns. What is asserted is unchanged: the request got
	// past the resolver, which only the issued credential can do.
	for name, header := range accepted {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got, want := drive(t, header), reachedStatus[scopedRoute]
			if got != want {
				t.Errorf("%s = %d; want %d — the credential is the one that was issued",
					name, got, want)
			}
		})
	}
	for name, header := range refused {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if got := drive(t, header); got != http.StatusNotFound {
				t.Errorf("%s = %d; want %d", name, got, http.StatusNotFound)
			}
		})
	}
}

// TestACredentialIsAcceptedUntilTheDeadlineAndNotAtIt pins the boundary FR-015
// turns on, on both sides. The session and its credential end at the same
// instant by construction; this is that instant asserted rather than assumed.
func TestACredentialIsAcceptedUntilTheDeadlineAndNotAtIt(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		age  time.Duration
		want int
	}{
		"a second inside the lifetime": {
			age: session.AbsoluteLifetime - time.Second,
			// The handler's own answer, read out of reachedStatus for the reason
			// TestTheCredentialSchemeIsReadStrictly reads it: what this case
			// asserts is that the credential was still accepted, not what the
			// route does afterwards.
			want: reachedStatus[scopedRoute],
		},
		"exactly at the deadline": {
			age:  session.AbsoluteLifetime,
			want: http.StatusNotFound,
		},
		"a second past it": {
			age:  session.AbsoluteLifetime + time.Second,
			want: http.StatusNotFound,
		},
	}

	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			s := newAuditedServer(t)
			planted, issued := s.fixture.plant(t, session.Session{CreatedAt: testTime.Add(-c.age)})

			rec := httptest.NewRecorder()
			s.ServeHTTP(rec, scopedRequest(t, planted.ID, issued, testTime))

			if rec.Code != c.want {
				t.Errorf("a session created %v ago = %d; want %d", c.age, rec.Code, c.want)
			}
		})
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
		// A body that is inside the limit reaches the create handler, which
		// refuses a run of "a" as malformed JSON. The 400 is the evidence the
		// test wants — only a handler past layer 2 produces one — and the 401
		// below is the evidence that one byte more never got that far.
		"one byte under the limit": {size: testMaxBody - 1, want: http.StatusBadRequest},
		"exactly at the limit":     {size: testMaxBody, want: http.StatusBadRequest},
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
