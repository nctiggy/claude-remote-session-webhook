// Internal test, matching internal/session. Two of the properties this task is
// really about are only reachable from inside the package: the assertion on the
// address a listener came back with (New refuses the configuration that would
// produce a non-loopback one, so the only way to reach it is to hand the Server
// a listener that lies), and the http.Server's own timeout fields, which are not
// exported anywhere.
package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"maps"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
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

// loopbackListen is the address every fixture asks for. Port 0 lets the kernel
// choose, so nothing here can collide with a real crswd or with another test.
const loopbackListen = "127.0.0.1:0"

// newTestServer builds a server on the fixed clock and a discarded audit trail.
// It goes through newServer rather than New so that a test serving a request
// does not write real audit records onto the test binary's stdout; New itself is
// covered by the construction and loopback tests below.
func newTestServer(t *testing.T, listen string) *Server {
	t.Helper()
	cfg := testConfig(listen)
	s, err := newServer(cfg, net.Listen, testAuth(t), testBrowser(), testTrail(t), newSessionFixture(t).mgr,
		testLimiter(t, cfg.CreateRatePerMin, fixedClock{at: testTime}))
	if err != nil {
		t.Fatalf("newServer(%q) = _, %v; want a server", listen, err)
	}
	pinClock(s)
	return s
}

// pinClock stands the server where the session fixture's manager already stands.
//
// The two clocks are one host clock in production, and a fixture that pinned only
// the manager would leave the dashboard deriving a display state and an age from
// the wall clock against a record stamped in 2026 — every session idle, every age
// counted in days, and a test that fails or passes depending on the day it runs.
func pinClock(s *Server) { s.clock = fixedClock{at: testTime} }

// TestNewRegistersExactlyTheContractRoutes pins FR-006: six operations and no
// others, in particular no unauthenticated health, status, metrics, or version
// route. The expected set is spelled out here rather than read from the routes
// table, so that the test asserts the contract and not that the code agrees with
// itself.
func TestNewRegistersExactlyTheContractRoutes(t *testing.T) {
	t.Parallel()

	want := []Route{
		{"POST", "/sessions"},
		{"GET", "/sessions"},
		{"GET", "/sessions/{id}"},
		{"DELETE", "/sessions/{id}"},
		{"POST", "/sessions/{id}/prompt"},
		{"GET", "/sessions/{id}/output"},
	}

	got := newTestServer(t, loopbackListen).Routes()
	if len(got) != len(want) {
		t.Fatalf("registered %d routes (%v); want exactly %d (%v)", len(got), got, len(want), want)
	}
	for i, w := range want {
		if got[i] != w {
			t.Errorf("route %d = %q; want %q", i, got[i], w)
		}
	}
}

// TestRoutesReturnsACopy keeps the sweep in T020 honest: a caller that could
// rewrite the returned slice could shorten the list of routes a test iterates.
func TestRoutesReturnsACopy(t *testing.T) {
	t.Parallel()

	s := newTestServer(t, loopbackListen)
	got := s.Routes()
	got[0] = Route{"GET", "/healthz"}

	if again := s.Routes(); again[0] == got[0] {
		t.Fatalf("mutating the returned slice changed the server's routes: %v", again)
	}
}

// TestEveryRegisteredRouteIsReachable sweeps the real router rather than a
// hand-written list. Today every authenticated route answers 501; T022–T029
// replace that, and this test then proves each handler is wired to the pattern
// it belongs to.
//
// The request has to be signed now — reaching a handler at all is what layer 2
// gates — which makes this the positive half of the sweep in middleware_test.go.
func TestEveryRegisteredRouteIsReachable(t *testing.T) {
	t.Parallel()

	s := newAuditedServer(t)
	for i, route := range s.Routes() {
		// A distinct timestamp per route, because the signature covers the
		// timestamp and the body and *not* the method or path: six identical
		// empty-bodied requests would share one signature and the second would
		// be refused as a replay. See middleware_test.go's replay case.
		req := requestFor(t, s, route, testTime.Add(-time.Duration(i)*time.Second))

		rec := httptest.NewRecorder()
		s.ServeHTTP(rec, req)

		if want := reachedStatus[route]; rec.Code != want {
			t.Errorf("%s %s = %d; want %d — the route is not wired to the mux",
				route.Method, req.URL.Path, rec.Code, want)
		}
	}
}

// TestNoRouteOutsideTheContractIsServed is the other half of FR-006. A health or
// metrics route is the classic accidental exemption, and on this daemon an
// exempt route is an unauthenticated door to unsandboxed execution.
//
// `GET /` was in this list and is not any more. It is not an exemption: it is the
// fleet page, which contracts/dashboard.md's route table gives to the **browser**
// door, so it is served by layer 1 rather than by a signature and answers a
// signed API request with the browser door's refusal. The API's own six routes
// are what this test is about and they are untouched (FR-014); that `GET /` is
// now the browser's is asserted in dashboard_test.go, where the identity that
// opens it lives.
func TestNoRouteOutsideTheContractIsServed(t *testing.T) {
	t.Parallel()

	unserved := []struct{ method, path string }{
		{"GET", "/healthz"},
		{"GET", "/health"},
		{"GET", "/status"},
		{"GET", "/metrics"},
		{"GET", "/version"},
		{"GET", "/debug/pprof/"},
		{"GET", "/sessions/abc/pane"},
		{"POST", "/sessions/abc/prompt/again"},
		{"GET", "/sessions/abc/output/raw"},
	}

	s := newTestServer(t, loopbackListen)
	for _, u := range unserved {
		// Unauthenticated: the uniform 401, not a 404. Every request passes
		// through a door before the router can answer, so a caller with no
		// credential cannot tell a path that exists from one that does not — the
		// same enumeration FR-033 closes for session IDs, closed for the route
		// table.
		rec := httptest.NewRecorder()
		s.ServeHTTP(rec, httptest.NewRequest(u.method, u.path, nil))

		if rec.Code != http.StatusUnauthorized {
			t.Errorf("%s %s = %d; want %d — an unauthenticated caller may not learn which paths exist",
				u.method, u.path, rec.Code, http.StatusUnauthorized)
		}

		// Signed, and still refused, because since T016 an unrouted path is the
		// browser door's (FR-013d) and a layer-2 signature is not an identity.
		// This fixture's layer 1 admits nobody, so what a signed probe reaches is
		// that door's one uniform refusal — which is the half FR-006 is about:
		// nothing outside the six routes may be *served*, whatever it presents.
		signed := httptest.NewRequest(u.method, u.path, nil)
		signRequest(t, signed, nil, testTime)

		rec = httptest.NewRecorder()
		s.ServeHTTP(rec, signed)

		if rec.Code != http.StatusUnauthorized {
			t.Errorf("%s %s signed = %d; want %d — nothing outside the six contract routes may be served",
				u.method, u.path, rec.Code, http.StatusUnauthorized)
		}
		if body := rec.Body.String(); body != string(bodyBrowserRefused) {
			t.Errorf("%s %s signed body = %q; want the browser door's one refusal — no handler may run here",
				u.method, u.path, body)
		}
	}
}

// TestAMethodTheContractDoesNotDefineIsRefused covers the other way a route can
// leak in: the same path under a verb no handler was written for. ServeMux
// matches method and pattern together, so these reach nothing.
//
// It no longer asserts ServeMux's own 405. That answer carried an `Allow` header
// naming the methods the path *does* serve, unauthenticated — a route table
// anyone who could reach the listener could read off. A method the contract does
// not define now answers exactly as a path that does not exist, which since T016
// means the browser door answers it (FR-013d): the uniform refusal here, because
// this fixture's layer 1 admits nobody. The mistake this guards against is
// unchanged — a handler registered for a verb the contract does not define — and
// is caught by the refusal body rather than by a status only the mux could
// produce.
func TestAMethodTheContractDoesNotDefineIsRefused(t *testing.T) {
	t.Parallel()

	refused := []struct{ method, path string }{
		{"PUT", "/sessions"},
		{"DELETE", "/sessions"},
		{"PATCH", "/sessions/abc"},
		{"PUT", "/sessions/abc/prompt"},
		{"DELETE", "/sessions/abc/output"},
	}

	s := newTestServer(t, loopbackListen)
	for _, r := range refused {
		rec := httptest.NewRecorder()
		s.ServeHTTP(rec, httptest.NewRequest(r.method, r.path, nil))

		if rec.Code != http.StatusUnauthorized {
			t.Errorf("%s %s = %d; want %d — an unauthenticated caller may not learn which methods a path serves",
				r.method, r.path, rec.Code, http.StatusUnauthorized)
		}
		if allow := rec.Header().Get("Allow"); allow != "" {
			t.Errorf("%s %s carried Allow: %q — that is the route table, handed out unauthenticated",
				r.method, r.path, allow)
		}

		signed := httptest.NewRequest(r.method, r.path, nil)
		signRequest(t, signed, nil, testTime)

		rec = httptest.NewRecorder()
		s.ServeHTTP(rec, signed)

		if rec.Code != http.StatusUnauthorized {
			t.Errorf("%s %s signed = %d; want %d — the contract defines no such operation, so it must reach no handler",
				r.method, r.path, rec.Code, http.StatusUnauthorized)
		}
		if body := rec.Body.String(); body != string(bodyBrowserRefused) {
			t.Errorf("%s %s signed body = %q; want the browser door's one refusal — a method the contract does not define reaches nothing",
				r.method, r.path, body)
		}
		if allow := rec.Header().Get("Allow"); allow != "" {
			t.Errorf("%s %s signed carried Allow: %q — the route table is not something a caller is owed on either door",
				r.method, r.path, allow)
		}
	}
}

// TestAnUnroutedPathIsAnsweredByTheDashboardsNotFound is FR-013d: the one
// deliberate change this milestone makes to what milestone 1 answered.
//
// A verified operator who mistypes a URL was receiving `{"error":"not found"}`
// from an interface they never used. What they get now is a page in the
// dashboard's own dress, carrying the identity every other page carries — and it
// is a 404 as well as a page, because a mistyped address that answered 200 would
// be a success no one asked for.
//
// The wrong-method case is here for a reason of its own. ServeMux answers 405
// whenever any pattern matches the path, so a route table with only `/` in it
// would never reach this handler for `PUT /sessions` — and 405 carries an
// `Allow` header, which is the route table handed to whoever asked.
func TestAnUnroutedPathIsAnsweredByTheDashboardsNotFound(t *testing.T) {
	t.Parallel()

	cases := map[string]struct{ method, path string }{
		"a path nothing claims":           {http.MethodGet, "/not-a-route"},
		"a path below one that is served": {http.MethodGet, "/sessions/abc/pane"},
		"a method no route answers":       {http.MethodPut, "/sessions"},
		"a method no route answers, at /": {http.MethodPost, "/"},
	}

	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			f := newFleet(t)
			r := httptest.NewRequest(c.method, c.path, nil)
			r.Header.Set(headerAccessAssertion, f.keys.mint(t, f.keys.claims()))

			w := httptest.NewRecorder()
			f.ServeHTTP(w, r)

			body := w.Body.String()
			if w.Code != http.StatusNotFound {
				t.Fatalf("%s %s = %d; want %d:\n%s", c.method, c.path, w.Code, http.StatusNotFound, body)
			}
			if got := w.Header().Get(headerContentType); got != contentTypeHTML {
				t.Errorf("%s %s answered as %q; want %q — this door answers a person with a document", c.method, c.path, got, contentTypeHTML)
			}
			if allow := w.Header().Get("Allow"); allow != "" {
				t.Errorf("%s %s carried Allow: %q — that is the route table, and no answer here may name it", c.method, c.path, allow)
			}

			// The page, the identity on it, and the interface it did not come
			// from. The last is the whole of FR-013d's complaint.
			for _, want := range []string{notFoundTitle, notFoundBody, testOperatorEmail} {
				if !strings.Contains(body, want) {
					t.Errorf("%s %s does not render %q:\n%s", c.method, c.path, want, body)
				}
			}
			if strings.Contains(body, string(bodyNotFound)) {
				t.Errorf("%s %s answered with the API's refusal body; FR-013d exists because a browser never used that interface:\n%s", c.method, c.path, body)
			}
			// Not the fleet either: `GET /` is confined by `{$}`, and a page that
			// answered every unrouted path with the fleet would be showing a
			// session list under an address that does not exist.
			if strings.Contains(body, emptyFleetTitle) {
				t.Errorf("%s %s was answered with the fleet page:\n%s", c.method, c.path, body)
			}

			rec := f.only(t)
			if got, want := rec["action"], string(audit.ActionUnknownRoute); got != want {
				t.Errorf("action = %v; want %v — the door that answers changed, the trail's name for it did not", got, want)
			}
			if got, want := rec["decision"], string(audit.Deny); got != want {
				t.Errorf("decision = %v; want %v — the request was authenticated and still not served", got, want)
			}
		})
	}
}

// TestAnUnroutedPathTellsAnUnverifiedCallerNothing is the other half of FR-013d,
// and it is what makes the page above safe to serve.
//
// Moving unrouted paths to the browser door would be a disclosure if the door
// answered them distinguishably: a scanner could then map the route table by
// comparing refusals. Layer 1 runs first, so every one of these is the same
// bytes as the refusal the fleet's own path gives — the difference between a
// path this daemon serves and one it does not is kept in the trail, where the
// operator can read it and the caller cannot.
func TestAnUnroutedPathTellsAnUnverifiedCallerNothing(t *testing.T) {
	t.Parallel()

	f := newFleet(t)
	answer := func(name, method, path string, present func(*http.Request)) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, nil)
		present(r)

		w := httptest.NewRecorder()
		f.ServeHTTP(w, r)
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("%s = %d; want %d:\n%s", name, w.Code, http.StatusUnauthorized, w.Body.String())
		}
		return w
	}

	// The yardstick: a route this daemon really serves, refused.
	want := answer("the fleet path with no assertion", http.MethodGet, "/", func(*http.Request) {})

	cases := map[string]func(*http.Request){
		"no credential at all": func(*http.Request) {},
		// A real API client that mistyped a path. Its signature is valid and
		// still not an identity, which is FR-012 from this side: this door
		// refuses only by the check that applies to it.
		"the API's own signature": func(r *http.Request) { signRequest(t, r, nil, testTime) },
		"a forged assertion":      func(r *http.Request) { r.Header.Set(headerAccessAssertion, "not.a.jwt") },
	}

	for name, present := range cases {
		got := answer(name, http.MethodGet, "/not-a-route", present)
		if got.Body.String() != want.Body.String() {
			t.Errorf("%s on an unrouted path answered %q, and the fleet's own path answers %q; the route table is not something a stranger is owed",
				name, got.Body.String(), want.Body.String())
		}
		if !maps.EqualFunc(got.Header(), want.Header(), func(a, b []string) bool { return strings.Join(a, "\x00") == strings.Join(b, "\x00") }) {
			t.Errorf("%s on an unrouted path answered with headers %v, and the fleet's own path answers with %v",
				name, got.Header(), want.Header())
		}
	}

	// One record per request, each naming the layer that refused — which the
	// caller was told nothing about.
	for _, rec := range f.records(t) {
		if got, want := rec["action"], string(audit.ActionAccessReject); got != want {
			t.Errorf("action = %v; want %v — layer 1 refused before the path was ever a question", got, want)
		}
	}
}

// TestTheAPIDoorIsUnaffectedByTheUnroutedMove is the regression the change above
// could cause and nothing else would notice.
//
// The catch-all registers a method-less pattern for every path a route uses, to
// take the wrong-method case away from ServeMux's 405. Those patterns sit on the
// browser door now, so a mistake in their specificity would move a *contract*
// route onto it — and a signed API request would then be refused for carrying no
// browser identity, which is exactly what FR-012 forbids. The audit action is
// the sharper half of the assertion: a route answered by the catch-all would
// record route.unknown rather than its own name.
func TestTheAPIDoorIsUnaffectedByTheUnroutedMove(t *testing.T) {
	t.Parallel()

	// The contract's own table, not s.Routes(): a route the catch-all swallowed
	// because it was never registered would be missing from the server's list,
	// and this sweep would then pass by not looking at it.
	registered := routes
	if len(registered) == 0 {
		t.Fatal("the contract names no routes, so this sweep would pass vacuously")
	}

	s := newAuditedServer(t)
	for i, route := range registered {
		// A distinct instant per route: the signature covers the timestamp and
		// the body, so identical empty-bodied requests would share one and the
		// replay cache would refuse the second.
		w := httptest.NewRecorder()
		s.ServeHTTP(w, requestFor(t, s, route, testTime.Add(-time.Duration(i)*time.Second)))

		if want := reachedStatus[route]; w.Code != want {
			t.Errorf("%s = %d; want %d — the request carried no browser identity, and this door never asks for one", route, w.Code, want)
		}
		if got := w.Header().Get(headerContentType); got != contentTypeJSON {
			t.Errorf("%s answered as %q; want %q — milestone 1's responses are frozen byte for byte", route, got, contentTypeJSON)
		}
	}

	got := s.records(t)
	if len(got) != len(registered) {
		t.Fatalf("%d records for %d requests; FR-041 requires exactly one each", len(got), len(registered))
	}
	for i, route := range registered {
		if want := string(routeActions[route]); got[i]["action"] != want {
			t.Errorf("%s recorded as %v; want %v — a contract route answered by the catch-all would say %q",
				route, got[i]["action"], want, audit.ActionUnknownRoute)
		}
	}
}

// TestTheServerHasItsOwnHandler guards against the quietest routing bug there
// is: an http.Server with a nil Handler serves http.DefaultServeMux, which any
// imported package can register onto — net/http/pprof does it from an import
// with no other effect.
func TestTheServerHasItsOwnHandler(t *testing.T) {
	t.Parallel()

	s := newTestServer(t, loopbackListen)
	if s.http.Handler == nil {
		t.Fatal("http.Server.Handler is nil, so the daemon would serve http.DefaultServeMux")
	}

	// Unsigned, so the answer is the uniform 401 — which is a stronger proof
	// than a 501 was: http.DefaultServeMux would 404 an unregistered pattern,
	// and only this server's mux carries the middleware that refuses.
	rec := httptest.NewRecorder()
	s.http.Handler.ServeHTTP(rec, httptest.NewRequest("GET", "/sessions", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("the server's own handler answered GET /sessions with %d; want %d",
			rec.Code, http.StatusUnauthorized)
	}
}

// TestServerTimeoutsAreSet pins every deadline. net/http defaults all of these
// to zero — meaning no limit — so an unset field is not a slower server but a
// connection that is never taken back.
func TestServerTimeoutsAreSet(t *testing.T) {
	t.Parallel()

	s := newTestServer(t, loopbackListen)
	cases := []struct {
		field string
		got   time.Duration
		want  time.Duration
	}{
		{"ReadHeaderTimeout", s.http.ReadHeaderTimeout, readHeaderTimeout},
		{"ReadTimeout", s.http.ReadTimeout, readTimeout},
		{"WriteTimeout", s.http.WriteTimeout, writeTimeout},
		{"IdleTimeout", s.http.IdleTimeout, idleTimeout},
	}
	for _, c := range cases {
		if c.got <= 0 {
			t.Errorf("%s = %v; a non-positive timeout is no timeout at all", c.field, c.got)
		}
		if c.got != c.want {
			t.Errorf("%s = %v; want %v", c.field, c.got, c.want)
		}
	}

	if s.http.MaxHeaderBytes != maxHeaderBytes {
		t.Errorf("MaxHeaderBytes = %d; want %d", s.http.MaxHeaderBytes, maxHeaderBytes)
	}
	if s.http.MaxHeaderBytes <= 0 || s.http.MaxHeaderBytes >= 1<<20 {
		t.Errorf("MaxHeaderBytes = %d; want a positive value below net/http's 1 MiB default", s.http.MaxHeaderBytes)
	}
}

// TestNewRefusesAListenAddressTheDoorDoesNotEarn is FR-005 at the second of its
// three gates. Every case here is a daemon that would have been reachable from
// off the host with nobody able to log in to it, or one whose address a resolver
// could reinterpret later.
func TestNewRefusesAListenAddressTheDoorDoesNotEarn(t *testing.T) {
	t.Parallel()

	refused := []struct {
		name, listen string
		// door is the configuration this address is refused under. The addresses
		// that are refused whatever the door keep the Access one, so that the
		// case is about the address; the rest are about the daemon nobody can get
		// into (M12/T002).
		door func(string) *config.Config
	}{
		{name: "all interfaces v4", listen: "0.0.0.0:8765", door: noDoorConfig},
		{name: "all interfaces v6", listen: "[::]:8765", door: noDoorConfig},
		{name: "a LAN address", listen: "192.168.1.10:8765", door: noDoorConfig},
		{name: "a public address", listen: "203.0.113.7:8765", door: noDoorConfig},
		{name: "a hostname a resolver controls", listen: "localhost:8765", door: testConfig},
		{name: "an empty host", listen: ":8765", door: testConfig},
		{name: "no port at all", listen: "8765", door: testConfig},
		{name: "nothing configured", listen: "", door: testConfig},
	}

	for _, c := range refused {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			s, err := New(c.door(c.listen))
			if err == nil {
				t.Fatalf("New(%q) built a server; want a refusal", c.listen)
			}
			if s != nil {
				t.Errorf("New(%q) returned a server alongside its error", c.listen)
			}
			if strings.Contains(err.Error(), "8765") && !strings.Contains(err.Error(), c.listen) {
				t.Errorf("error %q names a port but not the address it blames", err)
			}
		})
	}
}

// TestNewRefusesANonLoopbackListenAddressWithTheSentinel keeps the refusal
// distinguishable from an ordinary bind failure, which is what lets startup say
// why it will not run.
func TestNewRefusesANonLoopbackListenAddressWithTheSentinel(t *testing.T) {
	t.Parallel()

	if _, err := New(noDoorConfig("0.0.0.0:8765")); !errors.Is(err, ErrNotLoopback) {
		t.Fatalf("New(\"0.0.0.0:8765\") = _, %v; want an error matching ErrNotLoopback", err)
	}
}

// TestNewBindsOffLoopbackOnlyForADaemonSomebodyCanGetInto is the relaxation and
// its bound in one place (M12/T002).
//
// The two halves of the answer are asked separately here because they are
// different facts: the configuration is what the operator asked for, and the
// door is what was actually wired in front of the dashboard. Either one missing
// keeps the daemon on loopback — so a wiring mistake that left a closedDoor in
// front of a configured door cannot put an unauthenticated listener on the
// network, and neither can a Config whose door was never configured at all.
//
// Delete the IsLoopback check in assertBindAddress and the last three cases go
// green.
func TestNewBindsOffLoopbackOnlyForADaemonSomebodyCanGetInto(t *testing.T) {
	t.Parallel()

	// Every row names its layer 1 rather than taking it from verifiedLayer1,
	// including the two that could now have the real one: what this test is about
	// is the guard reading *both* halves, so the two halves have to be settable
	// apart. The rows where they agree — a password configuration behind the
	// password door verifiedLayer1 actually builds — are in password_test.go
	// (M12/T003), which is where the wiring is what is under test.

	for _, c := range []struct {
		name    string
		cfg     *config.Config
		door    layer1
		wantErr bool
		why     string
	}{
		{
			name: "an access door, on the network",
			cfg:  testConfig("192.168.1.10:8765"),
			door: testBrowser(),
			why:  "an operator behind Cloudflare who wants the daemon reachable on their own network too",
		},
		{
			name: "a password door, on the network",
			cfg:  passwordConfig("0.0.0.0:8765"),
			door: testBrowser(),
			why:  "the deployment this milestone exists for: no Cloudflare, and a listener the LAN can reach",
		},
		{
			name: "no door at all, on loopback",
			cfg:  noDoorConfig(loopbackListen),
			door: closedDoor{},
			why:  "today's daemon, unchanged",
		},
		{
			name:    "no door at all, on the network",
			cfg:     noDoorConfig("0.0.0.0:8765"),
			door:    closedDoor{},
			wantErr: true,
			why:     "reachable and admitting nobody: the state the guard has always existed to prevent",
		},
		{
			name:    "a configured door with a closed one wired in front of it",
			cfg:     testConfig("192.168.1.10:8765"),
			door:    closedDoor{},
			wantErr: true,
			why: "the file names a door and the dashboard has none. That is a wiring defect, and a wiring " +
				"defect must not be what decides the listener is allowed on the network",
		},
		{
			name:    "a door that admits somebody in front of a daemon that configured none",
			cfg:     noDoorConfig("0.0.0.0:8765"),
			door:    testBrowser(),
			wantErr: true,
			why: "the other way round, and this is where the development bypass lands: its layer 1 is not a " +
				"closedDoor and it authenticates nobody, so the configuration is what has to say no",
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			s, err := newServer(c.cfg, net.Listen, testAuth(t), c.door, testTrail(t), newSessionFixture(t).mgr,
				testLimiter(t, c.cfg.CreateRatePerMin, fixedClock{at: testTime}))
			switch {
			case c.wantErr && err == nil:
				t.Fatalf("newServer(%q) built a server: %s", c.cfg.Listen, c.why)
			case c.wantErr && !errors.Is(err, ErrNotLoopback):
				t.Fatalf("newServer(%q) = _, %v; want an error matching ErrNotLoopback: %s", c.cfg.Listen, err, c.why)
			case !c.wantErr && err != nil:
				t.Fatalf("newServer(%q) = _, %v; want a server: %s", c.cfg.Listen, err, c.why)
			case !c.wantErr && s == nil:
				t.Fatalf("newServer(%q) returned no server and no error: %s", c.cfg.Listen, c.why)
			}
		})
	}
}

// TestNewAcceptsEveryLoopbackSpelling — the check must not be a comparison
// against 127.0.0.1. The whole 127.0.0.0/8 block and ::1 are loopback.
func TestNewAcceptsEveryLoopbackSpelling(t *testing.T) {
	t.Parallel()

	for _, listen := range []string{"127.0.0.1:8765", "127.0.0.1:0", "127.0.0.53:8765", "[::1]:8765"} {
		if _, err := New(testConfig(listen)); err != nil {
			t.Errorf("New(%q) = _, %v; want a server", listen, err)
		}
	}
}

// TestAuditRecordsGoToStdout is FR-023a where the daemon actually stands: a
// server built the way cmd/crswd builds one writes its records to the process's
// own standard output, and the diagnostic that runs beside them does not go
// there.
//
// internal/audit's TestNewWritesToStdout makes the first claim about the
// *constructor*. This makes it about the daemon's use of it, which is the other
// failure and the one this repository has shipped four times — code that is
// right and a caller that never reaches it. It is also the only test in this
// package that goes through New rather than newServer, deliberately: every
// other one avoids exactly this, so that its records do not land on the test
// binary's stdout (see newTestServer).
//
// The second claim is the invariant #88 is really about. `grep '^{' | jq .` is
// a correct reader of the journal only while every line on stdout is a record;
// one warning printed there is either a dropped record or a parse failure, and
// which of the two it is depends on the first character of a sentence nobody
// wrote with that in mind.
//
// Not parallel: it swaps the process's standard output and redirects the
// standard logger, both of which every other test in this binary shares.
// internal/audit's stdout test settles the same problem the same way.
func TestAuditRecordsGoToStdout(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe() = %v", err)
	}

	realStdout := os.Stdout
	os.Stdout = w
	// Through Cleanup rather than at the end of the body: a t.Fatalf below must
	// print where the operator running the suite can read it, not into the pipe.
	t.Cleanup(func() { os.Stdout = realStdout })

	// The last-resort channel is the standard logger, which holds the *os.File
	// it was given when package log initialised — swapping os.Stderr would not
	// move it. So it is redirected the way internal/audit's leak suite redirects
	// it, and what this test proves about it is negative anyway: wherever it
	// goes, it is not the trail.
	diagnostics := &bytes.Buffer{}
	previous := log.Writer()
	log.SetOutput(diagnostics)
	t.Cleanup(func() { log.SetOutput(previous) })

	// After the swap and not before. New calls audit.New, which reads os.Stdout
	// at the moment it is called; a server built first would hold the real one,
	// and this test would read an empty pipe while the records it is about went
	// to the terminal.
	s, err := New(testConfig(loopbackListen))
	if err != nil {
		t.Fatalf("New = _, %v; want a server", err)
	}

	// Two unsigned requests, so both records are auth.reject and neither needs a
	// session fixture. The second is answered into a writer that fails, which is
	// the one way to make the daemon reach its report channel without breaking
	// the audit sink this test is reading.
	lost := errors.New("the connection went away")
	s.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/sessions", nil))
	s.ServeHTTP(&failingWriter{err: lost}, httptest.NewRequest(http.MethodGet, "/sessions", nil))

	os.Stdout = realStdout
	if err := w.Close(); err != nil {
		t.Fatalf("close the pipe: %v", err)
	}
	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("read the pipe: %v", err)
	}
	if err := r.Close(); err != nil {
		t.Fatalf("close the pipe reader: %v", err)
	}

	// The diagnostic has to have happened, or the negative claim below is about
	// a run in which nothing was written anywhere.
	if !strings.Contains(diagnostics.String(), lost.Error()) {
		t.Fatalf("the failed write was never reported, so this run proves nothing about where a report goes:\n%s", diagnostics)
	}

	// Named apart from the count below, which would otherwise report the one
	// line strings.Split makes out of nothing at all.
	if len(out) == 0 {
		t.Fatal("nothing reached stdout; the trail this daemon was built with is not writing where the journal reads")
	}

	lines := strings.Split(strings.TrimSuffix(string(out), "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("two requests wrote %d lines to stdout; want one record each:\n%s", len(lines), out)
	}
	for i, line := range lines {
		var rec map[string]any
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Errorf("stdout line %d is not a record — `grep '^{' | jq .` reads this stream: %v (%q)", i+1, err, line)
			continue
		}
		if rec["action"] != string(audit.ActionAuthReject) {
			t.Errorf("stdout line %d recorded %v; want %q", i+1, rec["action"], audit.ActionAuthReject)
		}
	}
	if strings.Contains(string(out), lost.Error()) {
		t.Errorf("the report of a failed write reached stdout, where the trail is:\n%s", out)
	}
}

// TestNonLoopbackCRSWListenIsFatalAtLoad is the first of the three gates, kept
// here so the env var, the requirement, and the server that depends on it are
// asserted in one place. config_test.go covers the parsing in detail.
func TestNonLoopbackCRSWListenIsFatalAtLoad(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	env := map[string]string{
		config.EnvSharedSecret: "test-only-shared-secret-32-bytes",
		config.EnvAllowedRoots: root,
		config.EnvListen:       "0.0.0.0:8765",
	}
	cfg, err := config.LoadFrom(func(k string) string { return env[k] }, io.Discard)
	if err == nil {
		t.Fatalf("LoadFrom accepted %s=%q and returned %v; want a startup failure",
			config.EnvListen, env[config.EnvListen], cfg)
	}
}

// TestNewRefusesMissingDependencies covers every way the server can be built
// without something it must not run without. The authenticator is the one that
// matters most: docs/security.md §4 ranks a daemon that starts with auth
// disabled as worse than one that does not start.
func TestNewRefusesMissingDependencies(t *testing.T) {
	t.Parallel()

	cfg := testConfig(loopbackListen)
	mgr := newSessionFixture(t).mgr
	lim := func() *limiter[auth.CallerID] { return testLimiter(t, cfg.CreateRatePerMin, fixedClock{at: testTime}) }
	cases := map[string]func() (*Server, error){
		"no config": func() (*Server, error) { return New(nil) },
		"no listen source": func() (*Server, error) {
			return newServer(cfg, nil, testAuth(t), testBrowser(), testTrail(t), mgr, lim())
		},
		"no authenticator": func() (*Server, error) {
			return newServer(cfg, net.Listen, nil, testBrowser(), testTrail(t), mgr, lim())
		},
		// A dashboard served with no layer 1 behind it is the whole of milestone
		// 2 missing, and the failure mode is a browser admitted rather than a
		// route that 500s — so it is a startup refusal like the one above it.
		"no access validator": func() (*Server, error) {
			return newServer(cfg, net.Listen, testAuth(t), nil, testTrail(t), mgr, lim())
		},
		"no audit sink": func() (*Server, error) {
			return newServer(cfg, net.Listen, testAuth(t), testBrowser(), nil, mgr, lim())
		},
		"no session manager": func() (*Server, error) {
			return newServer(cfg, net.Listen, testAuth(t), testBrowser(), testTrail(t), nil, lim())
		},
		// A create route with no budget behind it is FR-037's bound missing on
		// the one operation that spawns a process.
		"no create rate limiter": func() (*Server, error) {
			return newServer(cfg, net.Listen, testAuth(t), testBrowser(), testTrail(t), mgr, nil)
		},
		// A stream cap of zero is Principle VI's bound set to "refuse everything"
		// rather than missing, and it is still a startup failure: the dashboard
		// would serve every card, every session page, and open none of their
		// streams — a daemon whose defect is discovered one click at a time.
		"a stream cap that admits nothing": func() (*Server, error) {
			capless := *cfg
			capless.MaxStreams = 0
			return newServer(&capless, net.Listen, testAuth(t), testBrowser(), testTrail(t), mgr, lim())
		},
		// The pane bound the operator configured must be the one the tmux driver
		// is built with, and a zero is the shape that a number hard-coded here
		// would hide: tmuxctl.NewExec refuses a bound below one line, so this
		// case goes red the moment New stops passing cfg.PaneBound (T033).
		"a pane bound that states nothing": func() (*Server, error) {
			unbounded := *cfg
			unbounded.PaneBound = 0
			return New(&unbounded)
		},
		"no shared secret": func() (*Server, error) { return New(&config.Config{Listen: loopbackListen, MaxBodyBytes: 64}) },
		"no body size cap": func() (*Server, error) {
			return New(&config.Config{Listen: loopbackListen, SharedSecret: testSecret()})
		},
		// An empty allowlist is a daemon with nowhere to run a session, which
		// config.Load never produces (FR-004 defaults it loudly) and New must
		// still refuse: discovering it one 400 per create is not a startup
		// failure.
		"no approved roots": func() (*Server, error) {
			return New(&config.Config{Listen: loopbackListen, SharedSecret: testSecret(), MaxBodyBytes: 64})
		},
		"a short secret": func() (*Server, error) {
			return New(&config.Config{Listen: loopbackListen, SharedSecret: []byte{}, MaxBodyBytes: 64})
		},
		"a negative amount": func() (*Server, error) {
			return New(&config.Config{Listen: loopbackListen, SharedSecret: testSecret(), MaxBodyBytes: -1})
		},
	}
	for name, build := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if s, err := build(); err == nil {
				t.Fatalf("built a server with %s: %v; want a refusal", name, s)
			}
		})
	}
}

// TestListenBindsLoopbackAndServes is the end-to-end statement of SC-016: a real
// socket, a real request over it, and the address the kernel actually chose.
func TestListenBindsLoopbackAndServes(t *testing.T) {
	t.Parallel()

	s := newTestServer(t, loopbackListen)
	if addr := s.Addr(); addr != nil {
		t.Fatalf("Addr() = %v before Listen; want nil", addr)
	}
	if err := s.Listen(); err != nil {
		t.Fatalf("Listen() = %v; want a bound listener", err)
	}

	addr, ok := s.Addr().(*net.TCPAddr)
	if !ok {
		t.Fatalf("Addr() = %v (%T); want a *net.TCPAddr", s.Addr(), s.Addr())
	}
	if !addr.IP.IsLoopback() {
		t.Fatalf("bound to %v; the listener must be on loopback", addr)
	}
	if addr.Port == 0 {
		t.Fatalf("bound to %v; the kernel should have chosen a port", addr)
	}

	served := make(chan error, 1)
	go func() { served <- s.Serve() }()

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get("http://" + addr.String() + "/sessions")
	if err != nil {
		t.Fatalf("GET over the bound listener: %v", err)
	}
	if err := resp.Body.Close(); err != nil {
		t.Errorf("close the response body: %v", err)
	}
	// Unsigned over a real socket: the middleware is in the tree the listener
	// actually serves, not only the one ServeHTTP reaches in the other tests.
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("GET /sessions over the socket = %d; want %d", resp.StatusCode, http.StatusUnauthorized)
	}

	if err := s.Close(); err != nil {
		t.Fatalf("Close() = %v", err)
	}
	if err := <-served; err != nil {
		t.Errorf("Serve() = %v after a deliberate Close; want nil", err)
	}
}

func TestListenRefusesASecondBind(t *testing.T) {
	t.Parallel()

	s := newTestServer(t, loopbackListen)
	if err := s.Listen(); err != nil {
		t.Fatalf("Listen() = %v; want a bound listener", err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Errorf("Close() = %v", err)
		}
	})

	if err := s.Listen(); err == nil {
		t.Fatal("a second Listen() succeeded; the first listener would have been leaked")
	}
}

func TestServeRequiresListen(t *testing.T) {
	t.Parallel()

	if err := newTestServer(t, loopbackListen).Serve(); err == nil {
		t.Fatal("Serve() succeeded with nothing bound; want a refusal")
	}
}

// fakeListener hands back whatever address it is told to. It is the only way to
// reach the post-bind assertion: New refuses every configuration that would make
// net.Listen return a non-loopback address, so the case has to be constructed.
type fakeListener struct {
	addr   net.Addr
	closed int
}

func (l *fakeListener) Accept() (net.Conn, error) {
	return nil, errors.New("fakeListener: Accept is not supported")
}

func (l *fakeListener) Close() error   { l.closed++; return nil }
func (l *fakeListener) Addr() net.Addr { return l.addr }

// unixishAddr is a net.Addr that is not a *net.TCPAddr, for the fail-closed
// branch: an address whose type the check does not recognise is not evidence of
// loopback.
type unixishAddr struct{ path string }

func (a unixishAddr) Network() string { return "unix" }
func (a unixishAddr) String() string  { return a.path }

func TestListenRefusesAndClosesAListenerThatIsNotOnLoopback(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		addr net.Addr
	}{
		{"all interfaces", &net.TCPAddr{IP: net.IPv4zero, Port: 8765}},
		{"a LAN address", &net.TCPAddr{IP: net.IPv4(192, 168, 1, 10), Port: 8765}},
		{"no address at all", &net.TCPAddr{Port: 8765}},
		{"an unrecognised address type", unixishAddr{path: "/tmp/crswd.sock"}},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			// A daemon with no door: the last of the three gates is about the
			// listener a daemon nobody can log in to is not allowed to keep, and
			// a configured door would make every address here permitted (T002).
			ln := &fakeListener{addr: c.addr}
			s, err := newServer(noDoorConfig(loopbackListen), func(string, string) (net.Listener, error) {
				return ln, nil
			}, testAuth(t), closedDoor{}, testTrail(t), newSessionFixture(t).mgr,
				testLimiter(t, config.DefaultCreateRatePerMin, fixedClock{at: testTime}))
			if err != nil {
				t.Fatalf("newServer = _, %v; want a server", err)
			}

			err = s.Listen()
			if !errors.Is(err, ErrNotLoopback) {
				t.Fatalf("Listen() bound to %v and returned %v; want an error matching ErrNotLoopback", c.addr, err)
			}
			if ln.closed != 1 {
				t.Errorf("the refused listener was closed %d times; want exactly 1 — the socket already exists by then", ln.closed)
			}
			if s.Addr() != nil {
				t.Errorf("Addr() = %v after a refused bind; the server must not keep the listener", s.Addr())
			}
			if err := s.Serve(); err == nil {
				t.Error("Serve() succeeded after a refused bind")
			}
		})
	}
}

// TestListenKeepsANonLoopbackListenerWhenTheDoorAdmitsSomebody is the other side
// of the gate above, and the one an operator on a LAN depends on: the socket the
// kernel handed back is off loopback, and the daemon keeps it (M12/T002).
//
// It goes through the same fake listener, because a server that refused here
// would fail with a closed socket and no daemon rather than with a message — the
// last check runs after the bind has already happened.
func TestListenKeepsANonLoopbackListenerWhenTheDoorAdmitsSomebody(t *testing.T) {
	t.Parallel()

	addr := &net.TCPAddr{IP: net.IPv4(192, 168, 1, 10), Port: 8765}
	ln := &fakeListener{addr: addr}
	cfg := testConfig("192.168.1.10:8765")
	s, err := newServer(cfg, func(string, string) (net.Listener, error) {
		return ln, nil
	}, testAuth(t), testBrowser(), testTrail(t), newSessionFixture(t).mgr,
		testLimiter(t, cfg.CreateRatePerMin, fixedClock{at: testTime}))
	if err != nil {
		t.Fatalf("newServer = _, %v; want a server", err)
	}

	if err := s.Listen(); err != nil {
		t.Fatalf("Listen() = %v; want the listener kept: a daemon with a door may be where a browser can reach it", err)
	}
	if ln.closed != 0 {
		t.Errorf("the listener was closed %d times; want 0", ln.closed)
	}
	if s.Addr() == nil {
		t.Error("Addr() = nil after a bind that was allowed; the server did not keep the listener")
	}
	if err := s.Close(); err != nil {
		t.Errorf("Close() = %v", err)
	}
}

// hostID builds an ID-shaped value without putting a 32-character hex string in
// the source, which gitleaks reads as a credential. internal/session spells its
// own the same way, and for the same reason.
func hostID(ch string) string { return strings.Repeat(ch, session.IDLen) }

// survivorName is the tmux name the daemon would have given this ID. It comes
// from the session package's own spelling rather than a literal "crswd-" here,
// so a test cannot go on passing after the prefix moves.
func survivorName(id string) string { return session.Session{ID: id}.TmuxName() }

// seedSurvivor puts on the fake host a session this run did not start — what a
// restart leaves behind. Seed records no tmux call, so every call a
// reconciliation test sees is one Reconcile chose to make.
func seedSurvivor(t *testing.T, s *testServer, id string, created time.Time) {
	t.Helper()
	s.fixture.tmux.Seed(tmuxctl.SessionInfo{Name: survivorName(id), Created: created, Managed: true})
}

// tokenShape is what a session credential looks like on the wire: 64 hex
// characters. A session ID is 32, so a match is a credential and never an ID.
var tokenShape = regexp.MustCompile(`[0-9a-f]{64}`)

// TestReconcileTakesBackWhatARestartLeftRunning is US4 stated at the seam
// cmd/crswd uses: the sessions are under management again, and the trail carries
// exactly one startup.adopt record for each.
//
// The store assertions are the half that matters most. An adopted session that
// never reached the store is a live shell running with
// --dangerously-skip-permissions that no ownership check, no cap, and no reaper
// can see — the state FR-021 exists to make unreachable.
func TestReconcileTakesBackWhatARestartLeftRunning(t *testing.T) {
	t.Parallel()

	s := newAuditedServer(t)
	ids := []string{hostID("a"), hostID("b")}
	for _, id := range ids {
		seedSurvivor(t, s, id, testTime.Add(-2*time.Hour))
	}

	if err := s.Reconcile(context.Background()); err != nil {
		t.Fatalf("Reconcile() = %v; want the host reconciled", err)
	}

	if n := s.fixture.store.Len(); n != len(ids) {
		t.Fatalf("the store holds %d records after %d sessions were adopted; want one each", n, len(ids))
	}
	for _, id := range ids {
		if _, err := s.fixture.store.Get(id, auth.CallerOperator); err != nil {
			t.Errorf("the store has no record of adopted session %q for %q: %v", id, auth.CallerOperator, err)
		}
	}

	got := s.records(t)
	if len(got) != len(ids) {
		t.Fatalf("reconciliation emitted %d audit records (%v); want exactly one per adopted session (%d)", len(got), got, len(ids))
	}

	recorded := make(map[string]bool, len(got))
	for i, rec := range got {
		// The spellings are data-model.md's and contracts', written out rather
		// than read back out of the audit package: a test that took them from
		// the constants would prove only that the code agrees with itself.
		if rec["action"] != "startup.adopt" {
			t.Errorf("record %d action = %v; want %q", i, rec["action"], "startup.adopt")
		}
		if rec["decision"] != "allow" {
			t.Errorf("record %d decision = %v; want %q", i, rec["decision"], "allow")
		}
		if rec["caller"] != string(auth.CallerOperator) {
			t.Errorf("record %d caller = %v; want the configured operator %q", i, rec["caller"], auth.CallerOperator)
		}
		if remote, ok := rec["remote"]; ok {
			t.Errorf("record %d carries remote %v; no request is behind a startup adoption", i, remote)
		}
		id, ok := rec["session_id"].(string)
		if !ok {
			t.Errorf("record %d names no session; an adoption record is about one particular session", i)
			continue
		}
		recorded[id] = true
	}
	for _, id := range ids {
		if !recorded[id] {
			t.Errorf("no audit record names adopted session %q", id)
		}
	}
}

// TestReconcileKeepsAnAdoptedCredentialOutOfTheTrail is FR-042 on the one path
// that mints a credential nobody asked for. A session token in journald is the
// key to an unsandboxed shell, and it stays out of the trail even though there is
// nowhere else in milestone 1 for it to go.
func TestReconcileKeepsAnAdoptedCredentialOutOfTheTrail(t *testing.T) {
	t.Parallel()

	s := newAuditedServer(t)
	seedSurvivor(t, s, hostID("c"), testTime.Add(-time.Hour))

	if err := s.Reconcile(context.Background()); err != nil {
		t.Fatalf("Reconcile() = %v; want the host reconciled", err)
	}
	// The match is never printed: a failure message carrying the value would put
	// the credential in CI's logs, which is the thing being tested.
	if tokenShape.MatchString(s.sink.String()) {
		t.Error("a 64-character hex string reached the audit trail; a session credential may never appear in one (FR-042)")
	}
}

// TestReconcileOnAnEmptyHostRecordsNothing keeps the trail readable: a restart
// that adopted nothing is the ordinary case, and a record per startup rather than
// per adopted session would say a session was taken over when none was.
func TestReconcileOnAnEmptyHostRecordsNothing(t *testing.T) {
	t.Parallel()

	s := newAuditedServer(t)
	if err := s.Reconcile(context.Background()); err != nil {
		t.Fatalf("Reconcile() on an empty host = %v; want success", err)
	}
	if got := s.records(t); len(got) != 0 {
		t.Errorf("reconciliation of an empty host emitted %v; want no records", got)
	}
	if n := s.fixture.store.Len(); n != 0 {
		t.Errorf("the store holds %d records after adopting nothing", n)
	}
}

// TestReconcileIsFatalWhenTheHostCannotBeAsked is the other half of T032: a tmux
// failure at startup stops the daemon rather than being skipped.
//
// The distinction is not cosmetic. A host the daemon cannot list is a host that
// may be carrying sessions this daemon started and no longer owns, and a daemon
// that logged the failure and served anyway would leave them unowned, uncapped,
// and unreaped for the life of the process.
func TestReconcileIsFatalWhenTheHostCannotBeAsked(t *testing.T) {
	t.Parallel()

	s := newAuditedServer(t)
	seedSurvivor(t, s, hostID("d"), testTime.Add(-time.Hour))
	broken := errors.New("tmux: no server running on /tmp/tmux-1000/default")
	s.fixture.tmux.FailOp(tmuxctl.OpList, broken)

	err := s.Reconcile(context.Background())
	if err == nil {
		t.Fatal("Reconcile() succeeded on a host it could not list; startup must fail rather than skip reconciliation")
	}
	if !errors.Is(err, broken) {
		t.Errorf("Reconcile() = %v; want the tmux failure wrapped so startup can say why", err)
	}
	if n := s.fixture.store.Len(); n != 0 {
		t.Errorf("the store holds %d records after a reconciliation that failed", n)
	}
	if got := s.records(t); len(got) != 0 {
		t.Errorf("a failed reconciliation emitted %v; a record must describe a session actually adopted", got)
	}
}

// TestReconcileRefusesToRunOnceTheListenerIsBound is "before the listener binds"
// made a property of the type instead of a line order in cmd/crswd. A request
// served before reconciliation finished would be answered by a daemon that does
// not yet know what is running on its own host.
func TestReconcileRefusesToRunOnceTheListenerIsBound(t *testing.T) {
	t.Parallel()

	s := newAuditedServer(t)
	seedSurvivor(t, s, hostID("e"), testTime.Add(-time.Hour))
	if err := s.Listen(); err != nil {
		t.Fatalf("Listen() = %v; want a bound listener", err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Errorf("Close() = %v", err)
		}
	})

	if err := s.Reconcile(context.Background()); err == nil {
		t.Fatal("Reconcile() ran after the listener was bound; want a refusal")
	}
	if n := s.fixture.store.Len(); n != 0 {
		t.Errorf("the refused reconciliation adopted %d session(s) anyway", n)
	}
	if calls := s.fixture.tmux.Calls(); len(calls) != 0 {
		t.Errorf("the refused reconciliation ran %v; it must cost no tmux command", calls)
	}
}

// TestReconcileRecordsWhatItAdoptedEvenWhenThePassAlsoFailed: startup is about to
// exit, and the sessions it did take over were genuinely taken over. A trail that
// dropped them would be missing the half an operator has to act on.
func TestReconcileRecordsWhatItAdoptedEvenWhenThePassAlsoFailed(t *testing.T) {
	t.Parallel()

	s := newAuditedServer(t)
	healthy := hostID("a")
	seedSurvivor(t, s, healthy, testTime.Add(-2*time.Hour))

	// Past its ceiling, so it is destroyed rather than adopted (FR-025) — and
	// the kill does not take, so the teardown cannot be confirmed and the pass
	// fails around the adoption that succeeded.
	expired := hostID("b")
	seedSurvivor(t, s, expired, testTime.Add(-session.AbsoluteLifetime-time.Hour))
	s.fixture.tmux.SurviveKill(survivorName(expired))

	if err := s.Reconcile(context.Background()); err == nil {
		t.Fatal("Reconcile() reported success with a session it could not tear down still on the host")
	}

	rec := s.only(t)
	if rec["session_id"] != healthy {
		t.Errorf("record names session %v; want the one that was adopted, %q", rec["session_id"], healthy)
	}
	if _, err := s.fixture.store.Get(healthy, auth.CallerOperator); err != nil {
		t.Errorf("the healthy survivor was left unowned by a pass that failed elsewhere: %v", err)
	}
	if _, err := s.fixture.store.Get(expired, auth.CallerOperator); err == nil {
		t.Error("a session past its ceiling was adopted; FR-025 destroys it instead")
	}
}

// TestReconcileIsFatalWhenTheTrailCannotBeWritten. FR-041 makes the record
// mandatory, and startup is the one moment where refusing to run is still an
// option — unlike a request, which has already happened by the time its record
// fails to write.
func TestReconcileIsFatalWhenTheTrailCannotBeWritten(t *testing.T) {
	t.Parallel()

	s := newAuditedServer(t)
	seedSurvivor(t, s, hostID("f"), testTime.Add(-time.Hour))

	want := errors.New("stdout is closed")
	s.trail = audit.NewTo(brokenSink{err: want}, func() time.Time { return testTime })

	err := s.Reconcile(context.Background())
	if err == nil {
		t.Fatal("Reconcile() succeeded with no record of what it adopted; want a startup failure")
	}
	if !errors.Is(err, want) {
		t.Errorf("Reconcile() = %v; want the write failure wrapped", err)
	}
}

func TestListenReportsABindFailure(t *testing.T) {
	t.Parallel()

	want := errors.New("address already in use")
	s, err := newServer(testConfig(loopbackListen), func(string, string) (net.Listener, error) {
		return nil, want
	}, testAuth(t), testBrowser(), testTrail(t), newSessionFixture(t).mgr,
		testLimiter(t, config.DefaultCreateRatePerMin, fixedClock{at: testTime}))
	if err != nil {
		t.Fatalf("newServer = _, %v; want a server", err)
	}

	err = s.Listen()
	if !errors.Is(err, want) {
		t.Fatalf("Listen() = %v; want the bind failure wrapped", err)
	}
	if errors.Is(err, ErrNotLoopback) {
		t.Error("a bind failure was reported as a loopback violation; startup could not tell them apart")
	}
}

// killedAndVerified reports whether the fake was asked to kill name and then
// asked whether it was gone. Both halves matter: a teardown that only killed
// would be reporting what it asked for rather than what happened, which is the
// difference FR-040 and Principle VI turn on.
func killedAndVerified(calls []tmuxctl.Call, name string) (killed, verified bool) {
	for _, c := range calls {
		switch {
		case c.Op == tmuxctl.OpKill && slices.Contains(c.Argv, tmuxctl.SessionTarget(name)):
			killed = true
		case c.Op == tmuxctl.OpHas && slices.Contains(c.Argv, tmuxctl.SessionTarget(name)):
			verified = killed
		}
	}
	return killed, verified
}

// TestShutdownTearsDownEverySessionWithVerification is FR-040 over a real
// socket: the daemon stops serving, and every session it was holding is gone
// from the host before it exits.
//
// The second session belongs to another owner on purpose. Shutdown acts on the
// daemon's own behalf and there is no caller to scope it to — a teardown that
// swept only one identity's sessions would leave the rest running with
// --dangerously-skip-permissions and nothing left alive that owns them.
func TestShutdownTearsDownEverySessionWithVerification(t *testing.T) {
	t.Parallel()

	const otherOwner auth.CallerID = "a-second-operator"

	s := newAuditedServer(t)
	mine, _ := s.fixture.plant(t, session.Session{})
	theirs, _ := s.fixture.plant(t, session.Session{Owner: otherOwner})

	if err := s.Listen(); err != nil {
		t.Fatalf("Listen() = %v; want a bound listener", err)
	}
	addr := s.Addr().String()
	served := make(chan error, 1)
	go func() { served <- s.Serve() }()

	if err := s.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown() = %v; want a drained listener and an empty host", err)
	}
	if err := <-served; err != nil {
		t.Errorf("Serve() = %v after Shutdown; a deliberate stop is not an error", err)
	}

	calls := s.fixture.tmux.Calls()
	for _, live := range []session.Session{mine, theirs} {
		killed, verified := killedAndVerified(calls, live.TmuxName())
		if !killed {
			t.Errorf("session %s owned by %q was never killed at shutdown", live.ID, live.Owner)
		}
		if !verified {
			t.Errorf("session %s owned by %q was killed but never confirmed gone", live.ID, live.Owner)
		}
		if has, err := s.fixture.tmux.Has(context.Background(), live.TmuxName()); err != nil || has {
			t.Errorf("session %s is still on the host after shutdown (present=%v, err=%v)", live.ID, has, err)
		}
	}
	if n := s.fixture.store.Len(); n != 0 {
		t.Errorf("the store holds %d records after shutdown; a torn-down session keeps none", n)
	}

	// The socket is the other half of "stopped serving": a request that still
	// connected here would be one arriving at a daemon whose sessions are gone.
	client := &http.Client{Timeout: 5 * time.Second}
	if resp, err := client.Get("http://" + addr + "/sessions"); err == nil {
		if err := resp.Body.Close(); err != nil {
			t.Errorf("close the response body: %v", err)
		}
		t.Errorf("GET /sessions after Shutdown answered %d; the listener must be closed", resp.StatusCode)
	}
}

// TestShutdownIsLoudAboutASessionItCouldNotConfirmGone is the half that must
// never be swallowed. A kill tmux reported success for, with the session still
// there afterwards, is a live unsandboxed shell outliving the daemon that owned
// it — the error goes back to cmd/crswd, which reports it and exits non-zero, so
// the service manager records a stop that left something behind.
func TestShutdownIsLoudAboutASessionItCouldNotConfirmGone(t *testing.T) {
	t.Parallel()

	s := newAuditedServer(t)
	survivor, _ := s.fixture.plant(t, session.Session{})
	rest, _ := s.fixture.plant(t, session.Session{})
	s.fixture.tmux.SurviveKill(survivor.TmuxName())

	err := s.Shutdown(context.Background())
	if err == nil {
		t.Fatal("Shutdown() reported success with a session it could not confirm gone still on the host")
	}
	if !errors.Is(err, session.ErrOrphanedSession) {
		t.Errorf("Shutdown() = %v; want an error matching ErrOrphanedSession", err)
	}
	if !strings.Contains(err.Error(), survivor.ID) {
		t.Errorf("Shutdown() = %v; want the id of the session that survived, %q", err, survivor.ID)
	}

	// The one that could not be confirmed must not take the rest of the fleet
	// down with it: nothing comes after shutdown, so a session skipped here is a
	// session left running by a process that is exiting.
	if _, err := s.fixture.store.Get(rest.ID, auth.CallerOperator); err == nil {
		t.Errorf("session %s still has a record; one unconfirmed teardown must not stop the sweep", rest.ID)
	}
	if has, err := s.fixture.tmux.Has(context.Background(), rest.TmuxName()); err != nil || has {
		t.Errorf("session %s is still on the host (present=%v, err=%v); the sweep stopped at the survivor", rest.ID, has, err)
	}

	// And the survivor keeps its record, because the record is the only thing
	// naming an owner and an id for a session that may still be running.
	if _, err := s.fixture.store.Get(survivor.ID, auth.CallerOperator); err != nil {
		t.Errorf("the record for unconfirmed session %s was dropped: %v", survivor.ID, err)
	}
}

// TestShutdownReportsAHostItCannotAskAbout: "we could not ask" is not "it is
// gone". A tmux that cannot answer at shutdown must end in the same loud failure
// as a session that plainly survived.
func TestShutdownReportsAHostItCannotAskAbout(t *testing.T) {
	t.Parallel()

	s := newAuditedServer(t)
	live, _ := s.fixture.plant(t, session.Session{})
	// Both questions the verification asks, because either one answering is an
	// answer: a host that only fails has-session is still one List can confirm.
	broken := errors.New("tmux: no server running on /tmp/tmux-1000/default")
	s.fixture.tmux.FailOp(tmuxctl.OpHas, broken)
	s.fixture.tmux.FailOp(tmuxctl.OpList, broken)

	err := s.Shutdown(context.Background())
	if err == nil {
		t.Fatal("Shutdown() reported success on a host it could not ask about")
	}
	if !errors.Is(err, broken) {
		t.Errorf("Shutdown() = %v; want the tmux failure wrapped so an operator can say why", err)
	}
	if _, err := s.fixture.store.Get(live.ID, auth.CallerOperator); err != nil {
		t.Errorf("the record for unverified session %s was dropped: %v", live.ID, err)
	}
}

// TestShutdownOnAnIdleDaemonIsClean keeps the ordinary stop ordinary: nothing
// running, nothing to drain, and no error for an operator to chase.
func TestShutdownOnAnIdleDaemonIsClean(t *testing.T) {
	t.Parallel()

	s := newAuditedServer(t)
	if err := s.Listen(); err != nil {
		t.Fatalf("Listen() = %v; want a bound listener", err)
	}
	served := make(chan error, 1)
	go func() { served <- s.Serve() }()

	if err := s.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown() with no sessions = %v; want a clean stop", err)
	}
	if err := <-served; err != nil {
		t.Errorf("Serve() = %v after Shutdown; want nil", err)
	}
	if calls := s.fixture.tmux.Calls(); len(calls) != 0 {
		t.Errorf("shutting down an idle daemon ran %v; it must cost no tmux command", calls)
	}
}

// --- T020: two doors on one hostname, each refusing by its own check only ----
//
// FR-012's failure is a quiet one. A daemon that refused an API request for
// carrying no browser identity, or a browser request for carrying no signature,
// still serves both doors perfectly to a caller holding both credentials — and
// every caller in this suite before now has held exactly the one credential its
// door wanted. What follows presents each door's credential to the *other*
// door's routes and requires the answer not to move.
//
// FR-015 is the other half and it is a claim about bytes rather than behaviour:
// the six operations answer exactly what contracts/http-api.md froze, from
// before this milestone put a second door on the same hostname. A client written
// against milestone 1 is the thing being protected, so the bodies below are
// written out as that client receives them.

// The values contracts/http-api.md uses in its own examples, so the frozen
// bodies can be read against the contract line by line.
const (
	frozenName = "refactor-auth"

	// The contract's example pane, tab and newline included. Both survive
	// tmuxctl.Strip, which is what makes them worth having here: a response body
	// this test compares byte for byte is also the only place the escaping of
	// pane content into JSON is pinned.
	frozenPane = "$ go test ./...\nok  \tinternal/auth\t0.012s\n"

	// The fixture's instant and the absolute deadline 24 hours after it, spelled
	// out rather than computed from session.AbsoluteLifetime. A body built by
	// re-running the arithmetic under test would agree with the code whatever the
	// code did; these two strings say what the caller is promised.
	frozenCreatedAt = "2026-08-02T21:34:40Z"
	frozenExpiresAt = "2026-08-03T21:34:40Z"
)

// frozenSessionID is the session every session-scoped row addresses. It is
// planted rather than generated because a body compared byte for byte cannot
// name a random ID — and pinning it is also what keeps the comparison sharp: a
// detail response describing the wrong session would differ here rather than
// being normalised away.
var frozenSessionID = hostID("a")

// The two values in a frozen body that cannot be literals, replaced by their
// shape before the comparison. Only a create mints anything (FR-013), and the
// substitution is itself an assertion: a token that was not 64 hex characters,
// or an ID that was not 32, leaves the placeholder unwritten and the comparison
// fails with the raw body in the message.
const (
	idPlaceholder    = "<32 hex>"
	tokenPlaceholder = "<64 hex>"
)

// idShape is a session ID on the wire. tokenShape, declared above, is the
// credential; it is applied first because 64 hex characters contain 32.
var idShape = regexp.MustCompile(`[0-9a-f]{32}`)

func canonicaliseMinted(body string) string {
	return idShape.ReplaceAllString(tokenShape.ReplaceAllString(body, tokenPlaceholder), idPlaceholder)
}

// frozenAnswer is what one of milestone 1's six operations answers: the status,
// and the response verbatim.
//
// body is a function of the fixture because exactly one value in these bodies
// cannot be a literal — work_dir is the approved root the test was given, which
// is a temp directory chosen per run. Everything else is fixed by the plant, by
// the pinned clock, or by the contract.
type frozenAnswer struct {
	status int
	body   func(f sessionFixture) string

	// mints says this operation's response carries values the daemon generated
	// rather than values a fixture planted: the new session's ID and the only
	// copy of its token. That row is compared after canonicaliseMinted; no other
	// row is normalised at all.
	mints bool
}

// frozenEntry is the one object contracts/http-api.md defines for both the list
// and the detail response. It is spelled once here for the reason the contract
// gives it one shape: a daemon that let the two drift is the regression this
// table exists to catch, and two literals could drift with it.
func frozenEntry(f sessionFixture) string {
	return `{"id":"` + frozenSessionID + `","name":"` + frozenName + `","work_dir":"` + f.repo +
		`","state":"running","created_at":"` + frozenCreatedAt + `","expires_at":"` + frozenExpiresAt +
		`","last_activity":"` + frozenCreatedAt + `","adopted":false}`
}

var frozenAnswers = map[Route]frozenAnswer{
	{Method: http.MethodPost, Pattern: "/sessions"}: {
		status: http.StatusCreated,
		mints:  true,
		body: func(f sessionFixture) string {
			return `{"id":"` + idPlaceholder + `","name":"` + frozenName + `","work_dir":"` + f.repo +
				`","state":"starting","created_at":"` + frozenCreatedAt + `","expires_at":"` + frozenExpiresAt +
				`","token":"` + tokenPlaceholder + `"}`
		},
	},
	{Method: http.MethodGet, Pattern: "/sessions"}: {
		status: http.StatusOK,
		body:   func(f sessionFixture) string { return `{"sessions":[` + frozenEntry(f) + `]}` },
	},
	{Method: http.MethodGet, Pattern: "/sessions/{id}"}: {
		status: http.StatusOK,
		body:   frozenEntry,
	},
	{Method: http.MethodDelete, Pattern: "/sessions/{id}"}: {
		status: http.StatusOK,
		body:   func(sessionFixture) string { return `{"id":"` + frozenSessionID + `","destroyed":true}` },
	},
	{Method: http.MethodPost, Pattern: "/sessions/{id}/prompt"}: {
		status: http.StatusAccepted,
		body:   func(sessionFixture) string { return `{"id":"` + frozenSessionID + `","delivered":true}` },
	},
	{Method: http.MethodGet, Pattern: "/sessions/{id}/output"}: {
		status: http.StatusOK,
		body: func(sessionFixture) string {
			return `{"id":"` + frozenSessionID + `","captured_at":"` + frozenCreatedAt +
				`","text":"$ go test ./...\nok  \tinternal/auth\t0.012s\n"}`
		},
	},
}

// frozenRequest is the signed request one frozen row is driven by: requestFor
// with the session planted at a fixed ID and a known pane, so the response is a
// literal rather than something read back out of the code that produced it.
//
// The session is planted for every route, including the two that do not address
// one. The list needs it to have an entry to render, and the create is unharmed
// by it — a second session exists on the host, and the 201 describes only the
// one that was just made.
func frozenRequest(t *testing.T, f *fleet, route Route) *http.Request {
	t.Helper()

	planted, issued := f.fixture.plant(t, session.Session{
		ID:           frozenSessionID,
		Name:         frozenName,
		WorkDir:      f.fixture.repo,
		State:        session.StateRunning,
		CreatedAt:    testTime,
		LastActivity: testTime,
	})
	f.fixture.tmux.SetPane(planted.TmuxName(), frozenPane)

	body := bodyFor(f.fixture, route)
	req := httptest.NewRequest(route.Method, strings.ReplaceAll(route.Pattern, "{id}", planted.ID), bytes.NewReader(body))
	signRequest(t, req, body, testTime)
	if route.SessionScoped() {
		// After signing, because the signature covers the timestamp and the body
		// and nothing else — layer 3 is a separate credential, not part of one.
		req.Header.Set(headerAuthorization, bearerScheme+issued)
	}
	return req
}

// TestTheSixOperationsAnswerTheContractsBytesWhateverBrowserCredentialArrives is
// FR-015 and the API half of FR-012 in one sweep, because they are one claim
// from the client's side: the operation answers what it always answered, and no
// browser credential — present, absent, forged, or genuine — changes a byte of
// it.
//
// The four presentations are chosen for what each would break. `none at all` is
// the production shape and is what an API door that had grown an identity check
// would refuse. `a forged assertion` is the row a *lenient* such check would
// still pass, since layer 1 would be reached and would refuse. `the service
// token` is the one malformed-looking assertion that arrives in normal operation
// (FR-013c): the edge writes it on every call the real client makes, the
// dashboard must refuse it, and this door must not notice it at all.
//
// The door behind this fixture is a real *access.Validator over a local key
// pair, not the stub that admits nobody, so each of those rows is genuinely
// distinguishable to a daemon that looked.
func TestTheSixOperationsAnswerTheContractsBytesWhateverBrowserCredentialArrives(t *testing.T) {
	t.Parallel()

	if len(frozenAnswers) != len(routes) {
		t.Fatalf("%d frozen answers for %d contract routes; every operation's response is frozen, or the sweep passes by not looking",
			len(frozenAnswers), len(routes))
	}

	presentations := map[string]func(t *testing.T, f *fleet) string{
		"none at all":        func(*testing.T, *fleet) string { return absent },
		"a forged assertion": func(*testing.T, *fleet) string { return "not.a.jwt" },
		"a verified operator's own assertion": func(t *testing.T, f *fleet) string {
			return f.keys.mint(t, f.keys.claims())
		},
		"the service token the API client is admitted by": func(t *testing.T, f *fleet) string {
			return f.keys.mint(t, f.keys.serviceTokenClaims())
		},
	}

	for name, credential := range presentations {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			for _, route := range routes {
				want, ok := frozenAnswers[route]
				if !ok {
					t.Fatalf("%s has no frozen answer; a seventh operation is a change to milestone 1's contract", route)
				}

				// One server per row: the bodies below are byte-exact, and a
				// destroy or a create that ran against a fixture another row had
				// already changed would be comparing against a different fleet.
				f := newFleet(t)
				req := frozenRequest(t, f, route)
				if c := credential(t, f); c != absent {
					req.Header.Set(headerAccessAssertion, c)
				}

				w := httptest.NewRecorder()
				f.ServeHTTP(w, req)

				got := w.Body.String()
				if want.mints {
					got = canonicaliseMinted(got)
				}
				if w.Code != want.status {
					t.Errorf("%s = %d; want %d — this door reads no assertion, so nothing a browser carries may move it:\n%s",
						route, w.Code, want.status, got)
				}
				if body := want.body(f.fixture); got != body {
					t.Errorf("%s answered\n\t%s\nwant\n\t%s\n— a client written against milestone 1 receives the second", route, got, body)
				}
				if ct := w.Header().Get(headerContentType); ct != contentTypeJSON {
					t.Errorf("%s answered as %q; want %q", route, ct, contentTypeJSON)
				}

				// Which door answered, which no status can say: this fixture's
				// browser door refuses a forgery and admits a genuine assertion,
				// so a contract route that had moved behind it would answer
				// plausibly for two of these four rows and record route.unknown
				// for all of them.
				rec := f.only(t)
				if got, want := rec["action"], string(routeActions[route]); got != want {
					t.Errorf("%s recorded as %v; want %v — this operation is the API door's", route, got, want)
				}
				if got, want := rec["decision"], string(audit.Allow); got != want {
					t.Errorf("%s recorded %v; want %v — the request was authenticated and served", route, got, want)
				}
			}
		})
	}
}

// browserAnswer drives one browser-surface row as the verified operator, plus
// whatever layer-2 credential the caller chose to attach. The assertion is
// always genuine; what varies is the credential belonging to the other door.
func browserAnswer(t *testing.T, f *fleet, b browserRequest, present func(*testing.T, *http.Request)) *httptest.ResponseRecorder {
	t.Helper()

	r := httptest.NewRequest(b.method, b.target, nil)
	r.Header.Set(headerAccessAssertion, f.keys.mint(t, f.keys.claims()))
	present(t, r)

	w := httptest.NewRecorder()
	f.ServeHTTP(w, r)
	return w
}

// TestTheBrowserSurfaceIsServedWhateverSignatureItCarries is FR-012 from the
// other side, and the presentations are picked the same way: by what each one
// would break.
//
// `none at all` is what a browser can actually send — no page could require a
// signature and still be openable — and it is the yardstick every other row is
// compared against. The two *invalid* signatures are the sharper half: a door
// that had grown a layer-2 check would refuse them, while a valid signature
// would sail through and leave the suite green. And the valid one is not merely
// a control: a dashboard route that answered a signature would be the daemon
// reachable by anyone holding the shared secret rather than by a verified
// person, which is the failure dashboard_test.go names on the refusal path.
//
// Bodies and headers are compared, not just statuses. Every row here answers 200
// or 404 by design, so a status alone would not notice a page served with the
// wrong door's headers on it.
func TestTheBrowserSurfaceIsServedWhateverSignatureItCarries(t *testing.T) {
	t.Parallel()

	signatures := []struct {
		name    string
		present func(*testing.T, *http.Request)
	}{
		{"none at all", func(*testing.T, *http.Request) {}},
		{"one layer 2 would accept", func(t *testing.T, r *http.Request) {
			signRequest(t, r, nil, testTime)
		}},
		{"one layer 2 would refuse for its skew", func(t *testing.T, r *http.Request) {
			signRequest(t, r, nil, testTime.Add(-2*time.Hour))
		}},
		{"one layer 2 would refuse as forged", func(t *testing.T, r *http.Request) {
			signRequest(t, r, nil, testTime)
			r.Header.Set(auth.HeaderSignature, "sha256="+strings.Repeat("0", 64))
		}},
	}

	f := newFleet(t)
	planted, _ := f.fixture.plant(t, session.Session{Name: "a session no signature was needed to see", WorkDir: f.fixture.repo})
	rows := browserSurface(planted.ID)

	for _, row := range rows {
		var want *httptest.ResponseRecorder
		for _, sig := range signatures {
			got := browserAnswer(t, f, row, sig.present)

			if want == nil {
				// The yardstick, and the sweep's non-vacuity with it: a row that
				// answered the door's refusal to a browser carrying nothing but
				// its identity would make every comparison below trivially true.
				if got.Code != row.served {
					t.Fatalf("%s with %s = %d; want %d — this door asks for no signature:\n%s",
						row.name, sig.name, got.Code, row.served, got.Body.String())
				}
				want = got
				continue
			}

			if got.Code != want.Code {
				t.Errorf("%s with %s = %d; with no signature at all it is %d — a browser cannot sign, so neither answer may depend on one",
					row.name, sig.name, got.Code, want.Code)
			}
			if got.Body.String() != want.Body.String() {
				t.Errorf("%s with %s answered\n\t%s\nand with no signature at all\n\t%s",
					row.name, sig.name, got.Body.String(), want.Body.String())
			}
			if !maps.EqualFunc(got.Header(), want.Header(), func(a, b []string) bool {
				return strings.Join(a, "\x00") == strings.Join(b, "\x00")
			}) {
				t.Errorf("%s with %s answered with headers %v, and with no signature at all %v",
					row.name, sig.name, got.Header(), want.Header())
			}
		}
	}

	// One record per request, each naming what this door served rather than what
	// the other door would have refused. auth.reject appearing here would be
	// layer 2 running on a route that never asked for it, which is the whole
	// mistake — and it would be invisible above, since a refusal this uniform is
	// identical whichever door wrote it.
	records := f.records(t)
	if len(records) != len(rows)*len(signatures) {
		t.Fatalf("%d requests emitted %d audit records; FR-016 requires exactly one each",
			len(rows)*len(signatures), len(records))
	}
	for i, row := range rows {
		for j, sig := range signatures {
			rec := records[i*len(signatures)+j]
			if got, want := rec["action"], string(row.action); got != want {
				t.Errorf("%s with %s recorded as %v; want %v", row.name, sig.name, got, want)
			}
			if got, want := rec["decision"], string(audit.Allow); row.served == http.StatusOK && got != want {
				t.Errorf("%s with %s recorded %v; want %v — the operator was verified and served", row.name, sig.name, got, want)
			}
		}
	}
}
