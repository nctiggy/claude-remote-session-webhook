// Internal test, matching internal/session. Two of the properties this task is
// really about are only reachable from inside the package: the assertion on the
// address a listener came back with (New refuses the configuration that would
// produce a non-loopback one, so the only way to reach it is to hand the Server
// a listener that lies), and the http.Server's own timeout fields, which are not
// exported anywhere.
package httpapi

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
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
	return s
}

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
func TestNoRouteOutsideTheContractIsServed(t *testing.T) {
	t.Parallel()

	unserved := []struct{ method, path string }{
		{"GET", "/"},
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
		// Unauthenticated: the uniform 401, not a 404. Every request now passes
		// through layer 2 before the router can answer, so a caller without the
		// secret cannot tell a path that exists from one that does not — the same
		// enumeration FR-033 closes for session IDs, closed for the route table.
		rec := httptest.NewRecorder()
		s.ServeHTTP(rec, httptest.NewRequest(u.method, u.path, nil))

		if rec.Code != http.StatusUnauthorized {
			t.Errorf("%s %s = %d; want %d — an unauthenticated caller may not learn which paths exist",
				u.method, u.path, rec.Code, http.StatusUnauthorized)
		}

		// Authenticated: the uniform 404, and still no handler. This is the half
		// FR-006 is about — nothing outside the six routes may be *served*.
		signed := httptest.NewRequest(u.method, u.path, nil)
		signRequest(t, signed, nil, testTime)

		rec = httptest.NewRecorder()
		s.ServeHTTP(rec, signed)

		if rec.Code != http.StatusNotFound {
			t.Errorf("%s %s signed = %d; want %d — nothing outside the six contract routes may be served",
				u.method, u.path, rec.Code, http.StatusNotFound)
		}
		if body := rec.Body.String(); body != string(bodyNotFound) {
			t.Errorf("%s %s signed body = %q; want %q — an unknown route answers as an unknown session does",
				u.method, u.path, body, bodyNotFound)
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
// not define now answers exactly as a path that does not exist: 401 without the
// secret, the uniform 404 with it. The mistake this guards against is unchanged —
// a handler registered for a verb the contract does not define — and is now
// caught by the 404 body rather than by a status only the mux could produce.
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

		if rec.Code != http.StatusNotFound {
			t.Errorf("%s %s signed = %d; want %d — the contract defines no such operation, so it must reach no handler",
				r.method, r.path, rec.Code, http.StatusNotFound)
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

// TestNewRefusesANonLoopbackListenAddress is FR-005 at the second of its three
// gates. Every case here is a daemon that would have been reachable from off the
// host, or one whose reachability a resolver could change later.
func TestNewRefusesANonLoopbackListenAddress(t *testing.T) {
	t.Parallel()

	refused := []struct {
		name, listen string
	}{
		{"all interfaces v4", "0.0.0.0:8765"},
		{"all interfaces v6", "[::]:8765"},
		{"a LAN address", "192.168.1.10:8765"},
		{"a public address", "203.0.113.7:8765"},
		{"a hostname a resolver controls", "localhost:8765"},
		{"an empty host", ":8765"},
		{"no port at all", "8765"},
		{"nothing configured", ""},
	}

	for _, c := range refused {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			s, err := New(testConfig(c.listen))
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

	if _, err := New(testConfig("0.0.0.0:8765")); !errors.Is(err, ErrNotLoopback) {
		t.Fatalf("New(\"0.0.0.0:8765\") = _, %v; want an error matching ErrNotLoopback", err)
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
	lim := func() *limiter { return testLimiter(t, cfg.CreateRatePerMin, fixedClock{at: testTime}) }
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

			ln := &fakeListener{addr: c.addr}
			s, err := newServer(testConfig(loopbackListen), func(string, string) (net.Listener, error) {
				return ln, nil
			}, testAuth(t), testBrowser(), testTrail(t), newSessionFixture(t).mgr,
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
	s.Server.trail = audit.NewTo(brokenSink{err: want}, func() time.Time { return testTime })

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
