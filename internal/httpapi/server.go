// Package httpapi is the daemon's only external surface: six routes, a loopback
// listener, and nothing else.
//
// FR-006 makes the route table a closed set — no health, status, metrics, or
// version route, authenticated or otherwise — so the set is declared once, here,
// and a Server can only be built from it. A seventh entry is a contract change
// (contracts/http-api.md), not a convenience.
//
// The listener binds loopback and nothing else (FR-005). Reachability comes from
// the Cloudflare Tunnel, so a listener on any other interface is the one change
// docs/security.md calls simply wrong. config.Load already refuses a non-loopback
// CRSW_LISTEN; this package refuses it twice more — once on the configured
// string, and once on the address the kernel actually handed back, which is the
// only one of the three that is evidence rather than intent.
package httpapi

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"slices"
	"time"

	"github.com/nctiggy/claude-remote-session-webhook/internal/config"
)

// Server timeouts. net/http applies none of these by default, so one stalled
// peer would otherwise hold a connection — and the goroutine behind it — for as
// long as it liked. The spec fixes no values; these are chosen to be longer than
// any request this daemon actually serves, since every handler is a tmux exec or
// an in-memory read, and short enough that a stall is bounded.
//
// Milestone 2's SSE streaming cannot live under WriteTimeout and will need its
// own answer — a per-route override or a hijacked deadline — rather than this
// value being raised for everything.
const (
	// readHeaderTimeout also closes the Slowloris class outright (gosec G112).
	readHeaderTimeout = 5 * time.Second
	readTimeout       = 15 * time.Second
	writeTimeout      = 30 * time.Second
	idleTimeout       = 60 * time.Second

	// maxHeaderBytes is far under net/http's 1 MiB default. The only header a
	// caller controls the size of that this API reads is the bearer token, which
	// is a fixed-length hex string, and a header is read and hashed before any
	// route can decide it was nonsense — CRSW_MAX_BODY_BYTES covers the body
	// only. Bounding it here keeps that work small by construction.
	maxHeaderBytes = 16 << 10
)

// ErrNotLoopback is the refusal FR-005 exists for. It is a sentinel so that
// startup can tell "the daemon would have been reachable from off-host" from any
// other bind failure, and so a test can assert which check fired.
var ErrNotLoopback = errors.New("the listener must be bound to loopback; reachability comes from the tunnel")

// Route is one operation FR-006 permits, in the two halves net/http.ServeMux
// matches together since Go 1.22: a method and a pattern. Matching both is what
// keeps a path registered for one verb from being silently reachable by another.
type Route struct {
	Method  string
	Pattern string
}

// String is the ServeMux registration string, and is also what an error message
// or a test failure prints. One spelling, so the two cannot drift.
func (r Route) String() string { return r.Method + " " + r.Pattern }

// routes is the complete set from contracts/http-api.md, in the order the
// contract documents them. Nothing else may be registered.
var routes = []Route{
	{http.MethodPost, "/sessions"},
	{http.MethodGet, "/sessions"},
	{http.MethodGet, "/sessions/{id}"},
	{http.MethodDelete, "/sessions/{id}"},
	{http.MethodPost, "/sessions/{id}/prompt"},
	{http.MethodGet, "/sessions/{id}/output"},
}

// Server is the wiring: the mux, the timeouts, and the loopback listener. It
// holds no session state — that lives in internal/session — and every field is
// read-only after Listen, so the handlers it serves need no lock to reach it.
type Server struct {
	cfg  *config.Config
	mux  *http.ServeMux
	http *http.Server

	// registered records what was actually handed to the mux, which is not the
	// same claim as the routes table above. See Routes.
	registered []Route

	// listen is net.Listen in production. It is a field so that a test can hand
	// back a listener claiming a non-loopback address and prove the assertion
	// below refuses it — the one case that cannot be reached through New,
	// because New refuses the configuration that would produce it.
	listen func(network, address string) (net.Listener, error)

	ln net.Listener
}

// New builds the server for a validated Config. It binds nothing; Listen does.
func New(cfg *config.Config) (*Server, error) { return newServer(cfg, net.Listen) }

func newServer(cfg *config.Config, listen func(network, address string) (net.Listener, error)) (*Server, error) {
	switch {
	case cfg == nil:
		return nil, errors.New("httpapi: no configuration provided; refusing to start")
	case listen == nil:
		return nil, errors.New("httpapi: no listener source provided; refusing to start")
	}
	if err := assertLoopbackAddress(cfg.Listen); err != nil {
		return nil, err
	}

	mux := http.NewServeMux()
	s := &Server{
		cfg: cfg,
		mux: mux,
		http: &http.Server{
			Handler:           mux,
			ReadHeaderTimeout: readHeaderTimeout,
			ReadTimeout:       readTimeout,
			WriteTimeout:      writeTimeout,
			IdleTimeout:       idleTimeout,
			MaxHeaderBytes:    maxHeaderBytes,
		},
		listen: listen,
	}

	for _, r := range routes {
		s.handle(r, s.notImplemented)
	}
	return s, nil
}

// handle is the single place a route reaches the mux. Everything that must be
// true of every route gets applied here exactly once — T020 wraps the
// authentication middleware around h at this line, so that a route physically
// cannot be registered without it.
func (s *Server) handle(r Route, h http.HandlerFunc) {
	s.mux.Handle(r.String(), h)
	s.registered = append(s.registered, r)
}

// Routes reports what was registered, in registration order.
//
// It exists so a test can sweep the real router instead of a hand-written list:
// FR-007 admits no exempt route, and a list written by hand is exactly the thing
// a seventh route would be forgotten from. The slice is a copy — a caller
// holding the server's own backing array could rewrite the route table.
func (s *Server) Routes() []Route { return slices.Clone(s.registered) }

// ServeHTTP makes the Server the handler it wires, so that a test — and any
// future wrapper — can drive a route through httptest without binding a socket.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) { s.mux.ServeHTTP(w, r) }

// Listen binds the configured address and refuses to keep a listener that is not
// on loopback.
//
// The assertion is on ln.Addr(), not on the string that was asked for. The
// configured value has been checked twice by the time it gets here, but only
// what the kernel returns says what is actually reachable; a listener that came
// back bound to a wildcard is closed rather than served, because by then the
// socket already exists.
func (s *Server) Listen() error {
	if s.ln != nil {
		return fmt.Errorf("httpapi: already listening on %s", s.ln.Addr())
	}

	ln, err := s.listen("tcp", s.cfg.Listen)
	if err != nil {
		return fmt.Errorf("httpapi: bind %s: %w", s.cfg.Listen, err)
	}
	if err := assertLoopbackAddr(ln.Addr()); err != nil {
		return errors.Join(err, ln.Close())
	}

	s.ln = ln
	return nil
}

// Addr is the address actually bound, or nil before Listen has succeeded. Tests
// and the startup audit record read it rather than the configured string, which
// carries a port of 0 when the kernel chose one.
func (s *Server) Addr() net.Addr {
	if s.ln == nil {
		return nil
	}
	return s.ln.Addr()
}

// Serve serves until the server is closed. A deliberate close is not an error;
// anything else is.
func (s *Server) Serve() error {
	if s.ln == nil {
		return errors.New("httpapi: Listen must succeed before Serve")
	}
	if err := s.http.Serve(s.ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("httpapi: serve on %s: %w", s.ln.Addr(), err)
	}
	return nil
}

// Close stops the server and its listener immediately, dropping in-flight
// requests. Graceful shutdown — draining, then reaping every live session with
// its teardown verified — is T037's, and needs a context this cannot take.
func (s *Server) Close() error {
	if err := s.http.Close(); err != nil {
		return fmt.Errorf("httpapi: close the server: %w", err)
	}
	return nil
}

// notImplemented answers the six routes until T022–T029 give them handlers.
//
// It replies 501 with no body, and both halves are deliberate. A stub that
// already returned the uniform 401 would let T020's "every registered route
// refuses an unauthenticated request" sweep pass green without any middleware
// existing, which is the one thing that sweep is for. And the JSON error bodies
// belong to the tasks that own the responses; wiring does not get to invent them.
func (s *Server) notImplemented(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusNotImplemented)
}

// assertLoopbackAddress re-checks the configured address (FR-005).
//
// config.Load already refuses a non-loopback host, so this is the second of
// three checks and deliberately not the last. A Config is a struct: a test, a
// future caller, or a startup path that reorders two lines can produce one that
// never went through Load. The guarantee has to belong to the Server, not to one
// route into it.
func assertLoopbackAddress(addr string) error {
	if addr == "" {
		return fmt.Errorf("httpapi: no listen address configured: %w", ErrNotLoopback)
	}

	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("httpapi: listen address %q is not a host:port address: %w", addr, err)
	}

	ip := net.ParseIP(host)
	if ip == nil {
		// A name is refused rather than resolved, matching config.loadListen:
		// /etc/hosts or a resolver can point "localhost" off loopback without
		// this value changing, which would move the bind invisibly.
		return fmt.Errorf("httpapi: listen host %q must be a loopback IP literal such as 127.0.0.1 or ::1: %w", host, ErrNotLoopback)
	}
	if !ip.IsLoopback() {
		return fmt.Errorf("httpapi: listen host %q is not loopback: %w", host, ErrNotLoopback)
	}
	return nil
}

// assertLoopbackAddr checks the address a listener came back with. It fails
// closed on an address it does not recognise: an unknown type is not evidence of
// loopback, and this is the last check before the socket starts accepting.
func assertLoopbackAddr(addr net.Addr) error {
	tcp, ok := addr.(*net.TCPAddr)
	if !ok {
		return fmt.Errorf("httpapi: bound to %v, which is not a TCP address but a %T: %w", addr, addr, ErrNotLoopback)
	}
	if !tcp.IP.IsLoopback() {
		return fmt.Errorf("httpapi: bound to %v, which is not loopback: %w", addr, ErrNotLoopback)
	}
	return nil
}
