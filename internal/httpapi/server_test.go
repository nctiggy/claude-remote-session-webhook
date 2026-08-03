// Internal test, matching internal/session. Two of the properties this task is
// really about are only reachable from inside the package: the assertion on the
// address a listener came back with (New refuses the configuration that would
// produce a non-loopback one, so the only way to reach it is to hand the Server
// a listener that lies), and the http.Server's own timeout fields, which are not
// exported anywhere.
package httpapi

import (
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/nctiggy/claude-remote-session-webhook/internal/config"
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
	s, err := newServer(testConfig(listen), net.Listen, testAuth(t), testTrail(t))
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

	s := newTestServer(t, loopbackListen)
	for i, route := range s.Routes() {
		path := strings.ReplaceAll(route.Pattern, "{id}", "0123456789abcdef")

		// A distinct timestamp per route, because the signature covers the
		// timestamp and the body and *not* the method or path: six identical
		// empty-bodied requests would share one signature and the second would
		// be refused as a replay. See middleware_test.go's replay case.
		req := httptest.NewRequest(route.Method, path, nil)
		signRequest(t, req, nil, testTime.Add(-time.Duration(i)*time.Second))

		rec := httptest.NewRecorder()
		s.ServeHTTP(rec, req)

		if rec.Code != http.StatusNotImplemented {
			t.Errorf("%s %s = %d; want %d — the route is not wired to the mux",
				route.Method, path, rec.Code, http.StatusNotImplemented)
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
		rec := httptest.NewRecorder()
		s.ServeHTTP(rec, httptest.NewRequest(u.method, u.path, nil))

		if rec.Code != http.StatusNotFound {
			t.Errorf("%s %s = %d; want %d — nothing outside the six contract routes may be served",
				u.method, u.path, rec.Code, http.StatusNotFound)
		}
	}
}

// TestAMethodTheContractDoesNotDefineIsRefused covers the other way a route can
// leak in: the same path under a verb no handler was written for. ServeMux
// matches method and pattern together, so these reach nothing.
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

		if rec.Code == http.StatusNotImplemented {
			t.Errorf("%s %s reached a handler (%d); the contract defines no such operation",
				r.method, r.path, rec.Code)
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
	cases := map[string]func() (*Server, error){
		"no config":        func() (*Server, error) { return New(nil) },
		"no listen source": func() (*Server, error) { return newServer(cfg, nil, testAuth(t), testTrail(t)) },
		"no authenticator": func() (*Server, error) { return newServer(cfg, net.Listen, nil, testTrail(t)) },
		"no audit sink":    func() (*Server, error) { return newServer(cfg, net.Listen, testAuth(t), nil) },
		"no shared secret": func() (*Server, error) { return New(&config.Config{Listen: loopbackListen, MaxBodyBytes: 64}) },
		"no body size cap": func() (*Server, error) {
			return New(&config.Config{Listen: loopbackListen, SharedSecret: testSecret()})
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
			}, testAuth(t), testTrail(t))
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

func TestListenReportsABindFailure(t *testing.T) {
	t.Parallel()

	want := errors.New("address already in use")
	s, err := newServer(testConfig(loopbackListen), func(string, string) (net.Listener, error) {
		return nil, want
	}, testAuth(t), testTrail(t))
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
