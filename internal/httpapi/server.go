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
	"context"
	"errors"
	"fmt"
	"html/template"
	"net"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/nctiggy/claude-remote-session-webhook/internal/access"
	"github.com/nctiggy/claude-remote-session-webhook/internal/audit"
	"github.com/nctiggy/claude-remote-session-webhook/internal/auth"
	"github.com/nctiggy/claude-remote-session-webhook/internal/config"
	"github.com/nctiggy/claude-remote-session-webhook/internal/session"
	"github.com/nctiggy/claude-remote-session-webhook/internal/tmuxctl"
	"github.com/nctiggy/claude-remote-session-webhook/web"
)

// Server timeouts. net/http applies none of these by default, so one stalled
// peer would otherwise hold a connection — and the goroutine behind it — for as
// long as it liked. The spec fixes no values; these are chosen to be longer than
// any request this daemon actually serves, since every handler is a tmux exec or
// an in-memory read, and short enough that a stall is bounded.
//
// WriteTimeout is what milestone 2's SSE streaming cannot live under, and the
// answer is in stream.go rather than here: that handler lifts the deadline on
// its own response with http.NewResponseController (research D3). Zeroing this
// value would have been the same line of code and would have taken the deadline
// off all six routes below to serve one route above them.
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

// SessionScoped reports whether the route addresses one particular session,
// which is the same question as "does layer 3 apply here" (FR-014).
//
// It is derived from the pattern rather than listed in a table beside it. A
// seventh route carrying an {id} would have to be added to such a table as well
// as to the router, and the failure mode of forgetting is a session endpoint
// reachable with the shared secret alone.
func (r Route) SessionScoped() bool { return strings.Contains(r.Pattern, "{"+pathValueID+"}") }

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

	// authn is layer 2, applied to every route by handle. It is a field rather
	// than something a handler reaches for so that there is one Authenticator
	// per server — two would be two independent memories of which signatures
	// have been seen, which is a replay cache that does not refuse replays.
	authn *auth.Authenticator

	// browser is layer 1, applied to every dashboard route by
	// authenticateBrowser. One per server for a reason of the same shape: it
	// holds the cached signing key set, and a second one would be a second cache
	// — which is a key set fetched per door rather than per rotation (FR-008).
	browser layer1

	// trail is the audit sink. One record per request (FR-041), emitted by the
	// middleware.
	trail *audit.Logger

	// templates is the dashboard's template set, parsed from the embedded tree
	// once at construction (see render.go). It is read-only from here on, so
	// every handler executes the same set that startup proved compiles.
	templates *template.Template

	// sessions is the daemon's session store and tmux driver. It is the only
	// thing in this package that can cause execution on the host, which is why
	// no handler holds a Controller of its own: every rule standing in for the
	// permission prompt — the approved roots, the name alphabet, the ID-derived
	// target — lives behind this one field.
	sessions *session.Manager

	// creates is the per-caller budget for session creation (FR-037). It is one
	// limiter per server for the reason there is one Authenticator: two would be
	// two independent memories of how fast a caller has been asking, which is a
	// rate limit that does not limit the rate.
	creates *limiter

	// streams is the bound on how many output streams may be open at once
	// (FR-034e). One per server for the reason there is one create limiter: two
	// would be two independent counts of the same connections, which is a cap
	// that does not cap.
	//
	// It is built from the Config rather than injected, like the approved roots
	// and the session cap and unlike the clock or the listener: a caller may say
	// where tmux and the trail are, never how bounded the daemon is.
	streams *streamCap

	// panes is the shared screen buffer per watched session, and it is one per
	// server for a reason the two bounds above do not have: two registries would
	// be two buffers for the same session, which is two capture-pane execs a
	// second where contracts/stream.md allows one. The cap counts connections;
	// this is what makes the cost model count sessions.
	panes *panes

	// streamTick is how often an open stream writes (contracts/stream.md). It is
	// a field for the reason clock, listen and report are: a test seam that is
	// not a choice a caller has, since newServer chooses streamInterval and
	// nothing outside this package can name the field.
	//
	// The clock above cannot serve here and a fixture must not try. A stream is
	// real elapsed time on a real socket — what it is written against is the
	// server's own write deadline, which net/http sets from the host clock — so a
	// test that pinned time would be testing something other than the thing that
	// breaks. Shortening this is what keeps a stream's behaviour costing
	// milliseconds instead of seconds.
	streamTick time.Duration

	// clock is what the dashboard derives a display state and an age from, and it
	// is the host clock in production — the same one the session manager stamps a
	// record with and the reaper enforces a deadline against (FR-019c).
	//
	// A field rather than a constructor parameter because it is a test seam and
	// not a choice a caller has: newServer chooses systemClock, as newLimiter and
	// NewManager do, so there is no way to build a daemon whose dashboard reads a
	// different clock from its reaper. A fixture that pins one must pin the other,
	// or the page will disagree with the record the manager wrote.
	clock clock

	// report is where a failure with nowhere else to go is written — the audit
	// sink itself failing, or a response that could not be written. A field so
	// a test can read what was reported rather than watch stderr.
	report func(error)

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

// New builds the production server for a validated Config: the real tmux and the
// real audit sink. It binds nothing; Listen does. This is the only form
// cmd/crswd needs, and it is the pairing config.Load and audit.New have.
//
// The Authenticator, the audit sink, the session manager, and the create limiter
// are built below rather than passed in: cmd/crswd loads the config and hands it
// over, and a daemon whose auth is assembled by its caller is a daemon that can
// be assembled without it. The manager gets cfg.Roots and cfg.MaxSessions
// directly and the limiter gets cfg.CreateRatePerMin, so the allowlist a session
// is checked against, the cap it is counted against, and the rate it is spent
// from are the ones config.Load resolved at startup.
//
// NewWith is the one place the host clock is chosen. Everything below it takes
// the clock it was given, which is what makes the limiter's behaviour testable
// without elapsed time.
func New(cfg *config.Config) (*Server, error) {
	return NewWith(cfg, tmuxctl.NewExec(), audit.New())
}

// NewWith is New with the two collaborators that reach outside the process
// injected: the controller that drives tmux, and the sink the trail is written
// to. It is the seam config.LoadFrom, audit.NewTo, auth.NewWithClock, and
// session.NewManagerWithClock each have, and it exists for the same reason — a
// test of the whole daemon needs one that starts no real session and whose trail
// it can read back, and those are exactly the two things production reaches out
// of the process for.
//
// Everything else still comes from the Config, deliberately. The approved roots,
// the concurrent-session cap, the create budget, and the shared secret are the
// constraints standing in for the permission prompt (Principle VI), so they are
// not injectable here: a caller may say where tmux and the trail are, never how
// bounded the daemon is.
//
// internal/audit/leak_test.go is the caller (T039). It proves FR-042 across the
// whole daemon at once, which means driving the real routes through the real
// middleware — and it lives in package audit_test, where newServer is out of
// reach, because internal/httpapi imports internal/audit and not the other way
// round.
func NewWith(cfg *config.Config, tmux tmuxctl.Controller, trail *audit.Logger) (*Server, error) {
	if cfg == nil {
		return nil, errors.New("httpapi: no configuration provided; refusing to start")
	}
	authn, err := auth.New(cfg.SharedSecret, cfg.MaxBodyBytes)
	if err != nil {
		return nil, fmt.Errorf("httpapi: build the request authenticator: %w", err)
	}
	// A nil controller is refused by NewManager and a nil sink by newServer, so
	// neither is checked twice here: an injected collaborator that is missing
	// fails closed at the same line a production one would.
	sessions, err := session.NewManager(tmux, session.NewStore(), cfg.Roots, cfg.MaxSessions)
	if err != nil {
		return nil, fmt.Errorf("httpapi: build the session manager: %w", err)
	}
	creates, err := newLimiter(cfg.CreateRatePerMin, systemClock{})
	if err != nil {
		return nil, fmt.Errorf("httpapi: build the create rate limiter: %w", err)
	}
	// Built from the Config for the reason the roots and the caps are: layer 1's
	// audience, issuer and allowlist are constraints, not collaborators, so a
	// caller may say where tmux and the trail are and never how bounded the
	// daemon is. It opens no connection here — the key set is fetched when an
	// assertion first names a key, never at startup and never per request.
	//
	// Last of the four deliberately, so that a milestone 1 environment with a
	// second defect in it still reports that defect first. Every one of these is
	// a startup failure, and the order only decides which message an operator
	// working through their configuration meets first.
	//
	// The development bypass is the one thing that cannot come through this line,
	// since config.WithAccessBypassActive leaves these three values empty and
	// access.New rightly refuses them. It is built instead of a Validator, never
	// alongside one, by the //go:build dev half of cmd/crswd — which does not
	// exist yet, and is why newServer takes layer 1 as a parameter.
	browser, err := access.New(cfg.AccessTeamDomain, cfg.AccessAUD, cfg.AccessAllowedEmails)
	if err != nil {
		return nil, fmt.Errorf("httpapi: build the Access assertion validator: %w", err)
	}
	return newServer(cfg, net.Listen, authn, browser, trail, sessions, creates)
}

func newServer(
	cfg *config.Config,
	listen func(network, address string) (net.Listener, error),
	authn *auth.Authenticator,
	browser layer1,
	trail *audit.Logger,
	sessions *session.Manager,
	creates *limiter,
) (*Server, error) {
	switch {
	case cfg == nil:
		return nil, errors.New("httpapi: no configuration provided; refusing to start")
	case listen == nil:
		return nil, errors.New("httpapi: no listener source provided; refusing to start")
	case authn == nil:
		// A server that starts without an Authenticator is a server with no
		// authentication at all, which docs/security.md §4 ranks as worse than
		// one that does not start.
		return nil, errors.New("httpapi: no authenticator provided; refusing to start")
	case browser == nil:
		// The same ruling one line up, for the other door. A server that starts
		// without layer 1 serves the dashboard to whoever reaches the listener,
		// and the browser door's own middleware refusing a nil validator is the
		// backstop for a wiring mistake — not a licence to make one.
		return nil, errors.New("httpapi: no Access assertion validator provided; refusing to start")
	case trail == nil:
		return nil, errors.New("httpapi: no audit sink provided; refusing to start")
	case sessions == nil:
		// Refused rather than tolerated with the create route disabled: a daemon
		// serving five of six routes is a daemon whose failure is discovered one
		// request at a time.
		return nil, errors.New("httpapi: no session manager provided; refusing to start")
	case creates == nil:
		// Refused rather than defaulted to an unlimited one: Principle VI makes
		// the bounds structural, and a daemon serving creates with no budget is
		// one whose only remaining brake is the cap — which bounds how many
		// sessions exist, not how much work asking for them costs.
		return nil, errors.New("httpapi: no create rate limiter provided; refusing to start")
	}
	if err := assertLoopbackAddress(cfg.Listen); err != nil {
		return nil, err
	}

	// The last of the configured bounds, and a startup failure like the rest of
	// them: a daemon that does not know how many streams it may serve does not
	// get to serve any.
	streams, err := newStreamCap(cfg.MaxStreams)
	if err != nil {
		return nil, err
	}

	// Deliberately after every configuration check above. This one refuses a
	// broken build rather than a mistyped variable, and an operator working
	// through their environment should not meet it first.
	templates, err := parseTemplates(web.Templates)
	if err != nil {
		return nil, err
	}

	// The other embedded tree, refused on the same terms and for the same reason:
	// an asset the daemon cannot type is a browser rendering an unstyled page or
	// running no script, which is a defect that ships in silence.
	assets, err := loadAssets(web.Static)
	if err != nil {
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
		listen:     listen,
		authn:      authn,
		browser:    browser,
		trail:      trail,
		templates:  templates,
		sessions:   sessions,
		creates:    creates,
		streams:    streams,
		panes:      newPanes(),
		streamTick: streamInterval,
		clock:      systemClock{},
		report:     reportToStderr,
	}

	for _, r := range routes {
		if err := s.handle(r, s.handlerFor(r)); err != nil {
			return nil, err
		}
	}
	s.handleBrowser(patternFleet, audit.ActionDashboardView, s.dashboard)
	// The page every card on the fleet links to, on the same door and under the
	// same action: both are pages, and what tells them apart in the trail is the
	// session_id this one stamps on its own record (data-model.md). It is
	// registered here rather than added to `routes` for the reason handleBrowser
	// appends nothing to s.registered — that table is contracts/http-api.md's
	// closed set of six operations, each authorised by a signature, and this is
	// neither.
	s.handleBrowser(patternSessionView, audit.ActionDashboardView, s.sessionPage)
	// The live half of that page, on the same door and under an action of its
	// own: a stream is not a page, and an operator counting who read a session's
	// output must not be counting page loads with it. It is registered here for
	// the reason the two pages are — s.registered is the API's closed set of six
	// operations, and a route authorised by an identity rather than a signature
	// is not one of them.
	s.handleBrowser(patternSessionStream, audit.ActionStreamOpen, s.sessionStream)
	// One route per embedded asset, so `/static/` names exactly the files the
	// binary carries and a path that is not one of them is a path nothing claims
	// (contracts/dashboard.md's route table; see loadAssets for why a wildcard is
	// the weaker shape). They go on the browser door like every other page: an
	// asset is not secret, but a door that admitted one request unverified is a
	// door with an exception in it.
	for _, a := range assets {
		s.handleBrowser(a.pattern, audit.ActionDashboardAsset, s.serveAsset(a))
	}
	s.handleUnrouted()
	return s, nil
}

// handleUnrouted puts every request that matches no route behind a door and the
// same trail as one that does.
//
// Without it, ServeMux answered them itself and the daemon never saw them: an
// unknown path got `404 page not found` as text/plain, a wrong method got 405
// with an Allow header naming the methods that do work, and a non-clean path got
// a 301 with a Location — all unauthenticated, and none of them audited. No
// handler ran, so this was never an auth bypass; it was a hole in the trail
// (FR-041 says one record per request) and a way to map the route table without
// holding the secret, which is the enumeration FR-033 closes everywhere else.
//
// The door it puts them behind is the **browser's** (FR-013d), which is the one
// deliberate behaviour change this milestone makes to milestone 1's contract. A
// signed-in operator who mistypes a URL was receiving the API's raw JSON
// refusal, which is neither useful nor from an interface they ever used; they
// now get the dashboard's own not-found page. A caller layer 1 does not verify
// gets that door's one uniform refusal, so a scanner still learns nothing —
// and the six operations are untouched, because none of them reaches here.
//
// Two kinds of pattern are registered. `/` catches paths nothing claims — as a
// subtree pattern, which is why the fleet's own route carries `{$}` (see
// patternFleet). A method-less pattern for each path a route uses is the second
// belt on the wrong-method case: ServeMux answers 405 itself, with an `Allow`
// header naming the route table, whenever a pattern matches the path but not the
// method. Milestone 1's comment here said those patterns were what prevented
// that. They are not — `/` carries no method, so it already matches `PUT
// /sessions` and no 405 is ever reached; deleting the loop below changes no
// response this suite can observe. They stay because the guarantee they hold is
// one the catch-all's shape must not be able to lose quietly, and a method-ful
// pattern is the more specific of the two, so the contract's own routes still
// win for the methods they serve — which is what keeps this change off the API
// door entirely.
func (s *Server) handleUnrouted() {
	seen := map[string]bool{"/": true}
	s.handleBrowser("/", audit.ActionUnknownRoute, s.notFound)
	for _, r := range routes {
		if seen[r.Pattern] {
			continue
		}
		seen[r.Pattern] = true
		s.handleBrowser(r.Pattern, audit.ActionUnknownRoute, s.notFound)
	}
}

// handleBrowser is the browser door's equivalent of handle: the one place a
// dashboard route reaches the mux, so a page cannot be registered without layer 1
// in front of it or without an action to be recorded under (FR-016).
//
// The action is a parameter rather than a lookup in a table beside the pattern,
// because on this door it is a property of what is being served and not of the
// path — a page, an asset, a path nothing claims — and browser.go reads it to
// decide which response may be cached.
//
// Nothing is appended to s.registered, deliberately. That list is
// contracts/http-api.md's closed set of six operations, and the sweeps that prove
// milestone 1's responses are unchanged (FR-014) drive every route in it as an
// API request; a browser route among them would be swept as though a signature
// were the thing that authorises it.
func (s *Server) handleBrowser(pattern string, action audit.Action, h http.HandlerFunc) {
	s.mux.Handle(pattern, s.authenticateBrowser(action, h))
}

// handlerFor is where a route acquires its behaviour, and it is a switch with a
// default rather than a map so that a route can never be registered with no
// handler at all — the six are fixed above, and one that has yet to be
// implemented answers 501 instead of nil-panicking on its first request.
func (s *Server) handlerFor(r Route) http.HandlerFunc {
	switch r {
	case Route{Method: http.MethodPost, Pattern: "/sessions"}:
		return s.createSession
	case Route{Method: http.MethodGet, Pattern: "/sessions"}:
		return s.listSessions
	case Route{Method: http.MethodGet, Pattern: "/sessions/{id}"}:
		return s.sessionDetail
	case Route{Method: http.MethodDelete, Pattern: "/sessions/{id}"}:
		return s.destroySession
	case Route{Method: http.MethodPost, Pattern: "/sessions/{id}/prompt"}:
		return s.promptSession
	case Route{Method: http.MethodGet, Pattern: "/sessions/{id}/output"}:
		return s.sessionOutput
	default:
		return s.notImplemented
	}
}

// handle is the single place a route reaches the mux. Everything that must be
// true of every route is applied here exactly once — the authentication
// middleware wraps h at this line, so that a route physically cannot be
// registered without it (FR-007), and a session-scoped one is wrapped again by
// the layer-3 resolver so that it cannot be registered without that either.
//
// The order is the guarantee. authenticate is outermost, so a request with no
// valid signature is refused as unauthenticated before anything asks which
// session it meant; the resolver only ever runs for a caller layer 2 has already
// named, which is what makes the ownership check a comparison and not a guess.
// The rate limit sits between the two — behind the identity it spends, ahead of
// every lookup a refused request would otherwise pay for.
//
// It fails rather than registering a route with no audit action. FR-041 wants
// one record per request, and a route the trail has no name for is a route whose
// traffic is invisible; refusing at startup is how a seventh route gets noticed.
func (s *Server) handle(r Route, h http.HandlerFunc) error {
	action, ok := routeActions[r]
	if !ok {
		return fmt.Errorf("httpapi: route %s has no audit action; refusing to register it", r)
	}

	var handler http.Handler = h
	if r.SessionScoped() {
		handler = s.resolveSession(handler)
	}
	if rateLimited[r] {
		handler = s.limitCreates(handler)
	}

	s.mux.Handle(r.String(), s.authenticate(action, handler))
	s.registered = append(s.registered, r)
	return nil
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

// reasonAdopted is the server-authored account of a startup adoption. Like every
// other reason in the trail it is a constant in this repo, built from nothing the
// host or a caller supplied (FR-042).
const reasonAdopted = "took back a tmux session that outlived the daemon that started it"

// Reconcile takes back the sessions a previous run left behind and records one
// startup.adopt entry for each (FR-021, FR-025). It is the daemon's first act.
//
// It refuses to run once Listen has bound, and that is the whole point of the
// method living here rather than in cmd/crswd: an adopted session is one an
// ownership check, the cap, and the reaper can all see, and a request served
// before reconciliation finished would be answered by a daemon that does not yet
// know what is running on its own host. Ordering the two calls correctly in main
// is then a thing the type helps with rather than a thing a reader has to notice.
//
// A failure is returned rather than logged and stepped over, because startup
// treats it as fatal: a tmux the daemon cannot ask about is a host that may be
// carrying live unsandboxed shells with no owner, and serving anyway would be the
// silent skip Principle VI forbids. Nothing is retried — the next boot lists the
// host again.
//
// Records are emitted for whatever was adopted even when the pass also failed.
// The process is about to exit, but the sessions were genuinely taken over first,
// and a trail that omitted them would be missing the part an operator needs.
//
// The credential Adopt minted for each session is deliberately dropped. It may
// not go in the trail (FR-042 — a session token in journald is the key to an
// unsandboxed shell), and milestone 1 has nowhere else to put it, so an adopted
// session is owned, listed, capped, and reapable, but drivable by nobody. That is
// a known gap in US4 scenario 1, not an oversight of this method.
func (s *Server) Reconcile(ctx context.Context) error {
	if s.ln != nil {
		return fmt.Errorf("httpapi: the host must be reconciled before the listener binds, and this one is already bound to %s", s.ln.Addr())
	}

	adopted, err := s.sessions.Adopt(ctx)
	failures := []error{err}

	for _, a := range adopted {
		// The owner is the record's own, not a constant repeated here: the
		// caller field has to name whoever the ownership check will compare
		// against, and Adopt is the one that decided it.
		if err := s.trail.Emit(audit.Record{
			Action:    audit.ActionStartupAdopt,
			Caller:    string(a.Session.Owner),
			SessionID: a.Session.ID,
			Decision:  audit.Allow,
			Reason:    reasonAdopted,
			// Remote is empty: there is no request behind this record.
		}); err != nil {
			failures = append(failures, err)
		}
	}

	if err := errors.Join(failures...); err != nil {
		return fmt.Errorf("httpapi: reconcile with the host before serving: %w", err)
	}
	return nil
}

// StartReaper launches the idle and absolute-lifetime sweep and returns once it
// is running. It stops when ctx is done.
//
// It lives here rather than in main because the reaper needs the manager and the
// audit sink, and this package owns both — main holding either of them would be a
// second route to the one thing in the daemon that can cause execution on the
// host. Failing to build one is fatal to the caller for the same reason a missing
// secret is: a daemon serving without a reaper has no idle timeout and no
// ceiling, which is two of the five bounds Principle VI calls non-negotiable, and
// the sessions it starts would then only ever end by an explicit destroy or a
// restart.
//
// A goroutine rather than a blocking call, because the reaper's whole point is
// that an abandoned session dies without a request arriving to notice. Nothing
// waits on it: Run returns on cancellation, and the teardown that matters at
// shutdown is Shutdown's, which reaps every session rather than only the expired
// ones.
func (s *Server) StartReaper(ctx context.Context) error {
	reaper, err := session.NewReaper(s.sessions, s.trail)
	if err != nil {
		return fmt.Errorf("httpapi: start the reaper: %w", err)
	}
	go reaper.Run(ctx)
	return nil
}

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
// requests and leaving every session running. It is the abrupt stop, kept for
// tests and for a startup that has to unwind; the ending a termination signal
// gets is Shutdown.
func (s *Server) Close() error {
	if err := s.http.Close(); err != nil {
		return fmt.Errorf("httpapi: close the server: %w", err)
	}
	return nil
}

// shutdownDrain is how long in-flight requests get to finish once shutdown has
// begun.
//
// It is bounded, and deliberately a fraction of the budget a caller passes in,
// because draining is the merely polite half of shutdown. The half that is not
// optional is the teardown after it (FR-040): a peer holding a connection open
// may cost the daemon this long and no longer before the sessions go down
// anyway, since the alternative is a stalled request keeping unsandboxed shells
// alive past the moment the daemon that owns them stopped.
const shutdownDrain = 10 * time.Second

// Shutdown stops serving and then tears every session down with its teardown
// verified (FR-040). It is what a termination signal ends in.
//
// The order is the requirement. Draining first means a prompt already in flight
// finishes against a session that still exists, rather than being killed
// mid-request by the teardown behind it; and because http.Shutdown stops
// accepting first, nothing new can create a session after the sweep has decided
// what there was to destroy.
//
// The drain is bounded separately from the teardown so that one cannot consume
// the other's budget. A drain that runs out closes what is left rather than
// waiting: shutdown continues to the teardown either way, and the requests being
// dropped were about to lose their sessions.
//
// Failures are joined and returned rather than logged here. A session the host
// could not confirm gone is a live unsandboxed shell outliving the daemon, which
// is the one thing this method exists to prevent — the caller is cmd/crswd,
// which reports it and exits non-zero, so an operator's service manager records
// a failed stop instead of a clean one.
func (s *Server) Shutdown(ctx context.Context) error {
	drain, cancel := context.WithTimeout(ctx, shutdownDrain)
	defer cancel()

	var failures []error
	if err := s.http.Shutdown(drain); err != nil {
		failures = append(failures, fmt.Errorf("httpapi: drain requests in flight: %w", errors.Join(err, s.http.Close())))
	}

	if _, err := s.sessions.DestroyAll(ctx); err != nil {
		failures = append(failures, fmt.Errorf("httpapi: tear every session down before exit: %w", err))
	}
	return errors.Join(failures...)
}

// notImplemented is handlerFor's default, and since T029 gave the sixth route a
// handler nothing reaches it.
//
// It stays because handlerFor is a switch on a table declared elsewhere in this
// file: a seventh route added to routes without a case above would otherwise be
// registered with a nil handler and panic on its first request. A 501 that only
// authenticated traffic can reach, recorded in the trail like everything else,
// is the quieter failure and the one an operator can find.
//
// It replies with no body on purpose. The JSON error bodies belong to the tasks
// that own the responses; wiring does not get to invent one.
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
