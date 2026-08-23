// Package httpapi is the daemon's only external surface: six routes, a loopback
// listener, and nothing else.
//
// FR-006 makes the route table a closed set — no health, status, metrics, or
// version route, authenticated or otherwise — so the set is declared once, here,
// and a Server can only be built from it. A seventh entry is a contract change
// (contracts/http-api.md), not a convenience.
//
// The listener binds loopback unless a browser arriving off it would meet a door
// that can let them in (FR-005, M12). Reachability used to come from the
// Cloudflare Tunnel and nowhere else; it may now come from the operator's own
// network, and what did not change is the invariant underneath — this daemon is
// never reachable without authentication, so a dashboard that admits nobody
// stays where only the tunnel can find it. config.Load applies that rule to
// CRSW_LISTEN; this package applies it twice more — once on the configured
// string, and once on the address the kernel actually handed back, which is the
// only one of the three that is evidence rather than intent.
package httpapi

import (
	"context"
	"errors"
	"fmt"
	"html/template"
	"log"
	"net"
	"net/http"
	"os"
	"path"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/nctiggy/claude-remote-session-webhook/internal/updater"

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
	// releases caches what the release feed last said, so a page an operator
	// leaves open does not poll somebody else's API forever (settings.go).
	releases releaseCache

	// releaseFeed asks what is published, and is nil in tests.
	//
	// A seam rather than a direct call because composing a page must not depend
	// on the network: nil means the Updates section says it could not look, which
	// is the same answer an offline host gets and is therefore the behaviour
	// worth having tests exercise by default.
	releaseFeed func(context.Context) (*updater.Release, error)

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

	// pageKey is what a page token is minted and verified with (pagetoken.go).
	//
	// One per server for the reason there is one Authenticator, and one per
	// *process* on top of that: it is read from crypto/rand at construction and
	// never persisted, so a restart invalidates every outstanding token by
	// construction. Two of them would be two keys, each refusing the tokens the
	// other minted, which is a cross-site defence that refuses the operator.
	//
	// It is built here rather than passed in, like the roots and the caps and
	// unlike the clock or the listener: a caller may say where tmux and the trail
	// are, never what the browser door's second check is worth.
	pageKey pageKey

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
	creates *limiter[auth.CallerID]

	// logins is the per-source budget for sign-in attempts (M12/T005), one per
	// server for the same reason the create limiter is.
	//
	// It is built here rather than passed in, like the roots and the caps and
	// unlike the create limiter: that one's rate is the operator's to set, and
	// this one's is not — see loginRatePerMin. It exists on every server, not
	// only on the ones that register the sign-in routes, so that this field can
	// never be the nil a route was registered in front of.
	logins *limiter[loginSource]

	// streams is the bound on how many output streams may be open at once
	// (FR-034e). One per server for the reason there is one create limiter: two
	// would be two independent counts of the same connections, which is a cap
	// that does not cap.
	//
	// It is built from the Config rather than injected, like the approved roots
	// and the session cap and unlike the clock or the listener: a caller may say
	// where tmux and the trail are, never how bounded the daemon is.
	streams *streamCap

	// closing is how a response that is deliberately without an end is given one
	// at shutdown (FR-034f). Every open stream selects on it, and Shutdown closes
	// it before the drain begins — see Shutdown for why the order is the
	// requirement rather than a tidy detail.
	//
	// It is the one field here that is written after Listen, and it is written
	// exactly once: closeStreams guards it, and a receive on a closed channel is
	// safe from any number of goroutines. What the handlers read is the channel
	// value, which is fixed at construction like everything else.
	closing chan struct{}

	// closeStreams is that guard. A second close of the same channel panics, and
	// a shutdown that panicked would take the verified teardown of every session
	// down with it — which is the one thing shutdown exists to do.
	closeStreams sync.Once

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
	//
	// It writes to stderr and never to stdout, which is the trail's alone: a
	// diagnostic on stdout is a line the documented `grep '^{'` reader either
	// drops or chokes on, depending on what it happens to start with (FR-023a).
	report func(error)

	// updates is the four steps of contracts/self-update.md behind
	// POST /dashboard/update (T019). It is the one collaborator in this struct
	// that is *not* built by newServer, and that is the point: a server built for
	// a test carries no update path, so no case can download a release onto the
	// machine running the suite and rename it over the daemon already installed
	// there. The shipping build wires it in newWithLayer1, beside the real layer 1
	// and for the same reason — those are the two things a test must never get by
	// accident.
	updates selfUpdate

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
	if cfg == nil {
		return nil, errors.New("httpapi: nil config")
	}
	// One tmux server per daemon, named after the address this one listens on.
	// Two daemons sharing tmux's default server cannot tell each other's
	// sessions apart, so the second adopts the first's and reaps them on
	// shutdown (#22).
	//
	// The pane bound travels with it: it is the operator's setting, so the only
	// place it can enter the driver is where the driver is built.
	tmux, err := tmuxctl.NewExec(tmuxctl.SocketFor(cfg.Listen), cfg.PaneBound, config.SessionEnvironment(os.Environ(), cfg.SessionEnvironment))
	if err != nil {
		return nil, err
	}
	srv, err := NewWith(cfg, tmux, audit.New())
	if err != nil {
		return nil, err
	}
	// The real release feed, wired here and nowhere else. NewWith leaves it nil,
	// so every test composes the Updates section without a network call and
	// exercises the offline answer by default — which is the one an operator on a
	// disconnected host gets.
	srv.releaseFeed = func(ctx context.Context) (*updater.Release, error) {
		return updater.NewFetcher().Release(ctx, "")
	}
	return srv, nil
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
	return newWithLayer1(cfg, tmux, trail, verifiedLayer1)
}

// verifiedLayer1 is the door's first layer as the shipping build builds it, and
// **the one place a door is chosen**. It returns exactly one of three things: the
// validator that verifies a Cloudflare Access assertion against the account's
// published keys, the password door that verifies a cookie this daemon signed
// (M12/T003), or the closed door that admits nobody.
//
// One place, because the alternative is a browser middleware that asks which
// door it is holding — and closedDoor's own comment says what that costs. The
// selection is here, at startup, made once, from configuration config.Validate
// has already refused every ambiguous spelling of.
//
// It is a function rather than the expression it wraps so that the //go:build
// dev half of this package can put the development bypass at exactly this point
// in the sequence below, instead of alongside it. There is one layer 1 per
// server, always — a Validator that could be accompanied by a Bypass would be
// the "defaulted off" switch FR-041 forbids, wearing an interface.
func verifiedLayer1(cfg *config.Config) (layer1, error) {
	// Access first, because a daemon carrying the three Access values has had the
	// Access door since long before the password existed, and config.validateDoors
	// refuses a configuration that names both. The order therefore decides nothing
	// a loaded Config can reach — it decides what a hand-built one gets, and the
	// answer that keeps an existing deployment's door is the right one.
	switch {
	case cfg.AccessTeamDomain != "":
		v, err := access.New(cfg.AccessTeamDomain, cfg.AccessAUD, cfg.AccessAllowedEmails)
		if err != nil {
			// Untyped, so that newServer's nil check below reads a nil interface
			// rather than an interface holding a nil *Validator.
			return nil, err
		}
		return assertionDoor{validator: v, door: doorSentenceAccess}, nil

	case len(cfg.DashboardPassword) > 0:
		d, err := newPasswordDoor(cfg.SharedSecret, cfg.DashboardPassword)
		if err != nil {
			// Untyped, for the reason one line above.
			return nil, err
		}
		return d, nil

	default:
		// Neither configured means a dashboard that admits nobody, rather than a
		// daemon that will not start (#70). The API is untouched — it has never had
		// anything to do with layer 1.
		return closedDoor{}, nil
	}
}

// newWithLayer1 is NewWith with the door's first layer named by the caller. The
// two callers are NewWith, in every build, and NewWithBypass, in the development
// build alone — see verifiedLayer1.
func newWithLayer1(
	cfg *config.Config,
	tmux tmuxctl.Controller,
	trail *audit.Logger,
	buildLayer1 func(*config.Config) (layer1, error),
) (*Server, error) {
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
	// The daemon's only durable memory (spec 012). It is resolved from the
	// process environment rather than from cfg for the reason config.JournalPath
	// documents: the journal belongs beside whichever configuration file this
	// daemon actually read, so a second daemon started against its own
	// configuration cannot replay the sessions of the one the operator is
	// running. An empty path is a working daemon that remembers nothing, which
	// is a container with no home and is exactly what this daemon did before.
	// Named after the listen address, exactly as this daemon's tmux server is:
	// two daemons on one host must not replay each other's sessions any more than
	// they may see each other's shells.
	sessions.SetJournal(session.NewJournal(config.JournalPath(os.Getenv, cfg.Listen)))
	// The named start-command set reaches the manager here, and nowhere else
	// (#38). Without this line the whole of internal/config's start-command
	// handling is configuration nothing reads — which is the failure this repo
	// has now shipped three times, and the reason the setter exists rather than
	// the manager reading the environment itself.
	sessions.SetStartCommands(cfg.StartCommands)
	// Which of those names means remote reaches the manager here, and nowhere
	// else (#58). Without this line the toggle would refuse every request on a
	// correctly configured daemon — the transition resolves the mode against this
	// name, so a manager nobody told is one with no remote mode to switch to.
	sessions.SetRemoteControlCommand(cfg.RemoteControlCommand)
	// The configured lifetimes reach the manager here, and nowhere else (#37).
	sessions.SetLifetimes(cfg.SessionLifetime, cfg.SessionLifetimeMax)
	creates, err := newLimiter[auth.CallerID]("create", cfg.CreateRatePerMin, systemClock{})
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
	// The development bypass is the one thing that cannot come through the
	// shipping build's spelling of this line, since config.WithAccessBypassActive
	// leaves these three values empty and access.New rightly refuses them. It is
	// built instead of a Validator, never alongside one, by NewWithBypass — the
	// //go:build dev half of this package, called by the //go:build dev half of
	// cmd/crswd, and the reason this step is a parameter.
	browser, err := buildLayer1(cfg)
	if err != nil {
		return nil, fmt.Errorf("httpapi: build the Access assertion validator: %w", err)
	}
	srv, err := newServer(cfg, net.Listen, authn, browser, trail, sessions, creates)
	if err != nil {
		return nil, err
	}
	// The update path, wired here rather than in newServer for the reason layer 1
	// is chosen here: this and the door in front of it are the two collaborators
	// that reach outside the process in a way no test may reach by accident. A
	// server built through newServer has none, and its update route refuses;
	// every server a daemon runs has this one, which is what
	// TestTheShippingBuildWiresTheRealUpdatePath pins from the other side.
	srv.updates = liveSelfUpdate(cfg.FilePath)
	return srv, nil
}

func newServer(
	cfg *config.Config,
	listen func(network, address string) (net.Listener, error),
	authn *auth.Authenticator,
	browser layer1,
	trail *audit.Logger,
	sessions *session.Manager,
	creates *limiter[auth.CallerID],
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
	// After the nil checks above, because the answer depends on which layer 1
	// this server was handed and a nil one has no answer to give.
	if err := assertBindAddress(cfg.Listen, mayBindOffLoopback(cfg, browser)); err != nil {
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

	// A startup failure like every other missing piece of the auth path
	// (docs/security.md §4). A daemon that could not read 32 random bytes can
	// neither mint a page token nor verify one, so it would serve a dashboard
	// whose every action it must refuse — and the tempting repair for that, a key
	// derived from something already in hand, is the one research R2 rejects.
	// Refusing to start is the honest version of the same news.
	key, err := newPageKey()
	if err != nil {
		return nil, fmt.Errorf("httpapi: build the page token key: %w", err)
	}

	// The sign-in budget (M12/T005), built from a constant rather than from the
	// Config and so unable to fail on a daemon an operator configured — but built
	// through the same constructor as the create limiter, which fails closed on a
	// rate that bounds nothing. A daemon that could not build the budget for the
	// one route in front of layer 1 does not get to serve that route.
	logins, err := newLimiter[loginSource]("sign-in", loginRatePerMin, systemClock{})
	if err != nil {
		return nil, fmt.Errorf("httpapi: build the sign-in rate limiter: %w", err)
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
		pageKey:    key,
		templates:  templates,
		sessions:   sessions,
		creates:    creates,
		logins:     logins,
		streams:    streams,
		closing:    make(chan struct{}),
		panes:      newPanes(),
		streamTick: streamInterval,
		clock:      systemClock{},
		report:     reportToStderr,
	}

	// The Server, not the mux. ServeHTTP refuses a non-clean path before the mux
	// can answer it with net/http's own cleanPath 301 — a redirect that runs
	// ahead of layer 1, ahead of the audit middleware, and without the headers a
	// refusal must carry. Handing the mux straight to http.Server would leave that
	// hole open on the listener while every test driving s.ServeHTTP saw it
	// closed, which is the worst of both.
	s.http.Handler = s

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
	// The other stream, and the one that is about the fleet rather than about a
	// session (#15). It goes on handleBrowser and not handleAction because it
	// changes nothing: what admits it is layer 1 and the same-origin check the pane
	// stream carries, with no page token — an EventSource cannot submit a form
	// field, and the token exists to authorise a write. Its action is its own, so
	// an operator counting who watched the fleet is not counting page loads or pane
	// reads with it.
	s.handleBrowser(patternFleetStream, audit.ActionFleetOpen, s.fleetStream)
	// The read-only account of how this daemon was configured, on the same door as
	// the pages above and under an action of its own: an operator counting who read
	// the configuration must not be counting fleet loads with them.
	//
	// handleBrowser and deliberately never handleAction, because there is nothing
	// here for the gate to authorise — this route only reads, and no mutating verb
	// is registered on the path at all. Editing the operator's file from a browser
	// is out of scope this milestone, and the absence of a POST is the safeguard
	// rather than a POST that refuses (contracts/settings-page.md).
	s.handleBrowser(patternSettings, audit.ActionSettingsView, s.settings)
	// What this daemon calls itself, on the same door as the pages above and under
	// an action of its own (T003). It goes through handleBrowser like every other
	// read here, rather than being answered ahead of the door as a health endpoint
	// usually is: FR-006's closed set admits no unauthenticated route, and a
	// version is exactly the fact a scanner would like for free.
	//
	// It is not on `routes` for the reason none of the browser door's routes are —
	// that table is contracts/http-api.md's six operations, each authorised by a
	// signature, and this is authorised by an identity.
	s.handleBrowser(patternVersion, audit.ActionDashboardVersion, s.version)
	// The resume control's own read (spec 012). It is a browser route rather than
	// an action because it changes nothing, and it carries ActionDashboardView
	// rather than an action of its own because what it discloses is what the
	// dashboard already discloses.
	s.handleBrowser(patternConversations, audit.ActionDashboardView, s.conversations)
	// The first route on this door that changes something, and the reason
	// handleAction exists one line below handleBrowser rather than instead of it:
	// everything above only reads, and a read is authorised by layer 1 alone.
	s.handleAction(patternDashboardDestroy, audit.ActionDashboardDestroy, s.destroyFromBrowser)
	// The second, and the one that starts an unsandboxed shell rather than ending
	// one. It goes through handleAction like the destroy above and unlike the
	// pages: a route that spawns a Claude session on an ambient Access cookie alone
	// is the exact request a hostile third-party page can cause a browser to send.
	s.handleAction(patternDashboardCreate, audit.ActionDashboardCreate, s.createFromBrowser)
	// The third, and the only one of the four that touches nothing on the host. It
	// goes through handleAction all the same: what makes the gate necessary is that
	// a request changes state on an ambient cookie, not what the change costs — a
	// third-party page that can relabel every session on this host can hide one,
	// and the operator reading the fleet afterwards has no way to tell.
	s.handleAction(patternDashboardRename, audit.ActionDashboardRename, s.renameFromBrowser)
	// The fourth, and the one that reaches furthest into a session without saying
	// anything about what happens there: it delivers Claude Code's own /compact
	// into a running assistant. It goes through handleAction like the three above
	// because it is a write — a third-party page that can deliver into every
	// session on this host is one that can interrupt whatever each of them was
	// doing, and the operator watching a pane has no way to tell that from the
	// assistant's own decision.
	s.handleAction(patternDashboardCompact, audit.ActionDashboardCompact, s.compactFromBrowser)
	// The fifth, and the only one that takes a value naming what a session runs
	// (T019). It goes through handleAction like the four above, and it is the one
	// of the five where the gate's second half earns its keep twice over: a
	// third-party page that could reach this route could restart every session on
	// this host under whichever of the operator's configured commands it named.
	// What it may name is two words, checked against internal/session's own
	// vocabulary before anything is looked up — no command line arrives from a
	// browser, in either direction (FR-030).
	s.handleAction(patternDashboardMode, audit.ActionSessionMode, s.modeFromBrowser)
	s.handleAction(patternDashboardContinue, audit.ActionDashboardContinue, s.continueFromBrowser)
	// The sixth, and the only one of the six that changes this daemon rather than
	// a session it manages (T019). It goes through handleAction like the five
	// above, and the argument for the gate is at its strongest here: a
	// third-party page that could reach this route could make this host download
	// and execute a binary, which is the whole threat model in one request. What
	// admits the bytes is a signature made before the request existed, and the
	// door does not relax it (FR-029b).
	s.handleAction(patternDashboardUpdate, audit.ActionDashboardUpdate, s.updateFromBrowser)
	// The seventh, and the second of the seven about this daemon rather than a
	// session it manages (milestone 9). It goes through handleAction like the six
	// above, and it is registered by the same call rather than by hand for the
	// reason that call exists: the gate, the record and the uniform refusal are
	// inherited, and a route that re-implemented them would be free to
	// re-implement them differently. What a third-party page could do through it
	// is end this process — less than the update above it, which can make this
	// host download and execute a binary, and more than enough to be worth the
	// gate.
	s.handleAction(patternDashboardRestart, audit.ActionDashboardRestart, s.restartFromBrowser)

	// The settings edit, through the same call every other write uses. The
	// cross-site gate, the audit record and the ownership check are inherited
	// rather than restated, which is what keeps one door rather than two.
	s.handleAction(patternSettingsEdit, audit.ActionSettingsEdit, s.editSetting)
	// One route per embedded asset, so `/static/` names exactly the files the
	// binary carries and a path that is not one of them is a path nothing claims
	// (contracts/dashboard.md's route table; see loadAssets for why a wildcard is
	// the weaker shape). They go on the browser door like every other page: an
	// asset is not secret, but a door that admitted one request unverified is a
	// door with an exception in it.
	for _, a := range assets {
		s.handleBrowser(a.pattern, audit.ActionDashboardAsset, s.serveAsset(a))
	}
	// The sign-in page and the form it posts (M12/T004), and the only two routes
	// this daemon ever registers in front of layer 1 — see login.go for what makes
	// that safe.
	//
	// **Registered only when the password door is the layer 1 this server was
	// actually built with**, which is what this assertion asks. Not "the Config
	// names a password": that is intent, and a daemon whose file named a door the
	// server did not build is a wiring defect, which must never be the thing that
	// puts a login form on the network. The distinction is mayBindOffLoopback's,
	// one screen up, and it is the same one for the same reason.
	//
	// Where Access is the door — or where there is no door — nothing is registered
	// and /login is a path nothing claims, answered by the browser door's own 404
	// from behind layer 1. A login form standing beside a working Access door
	// would be the second authorisation path this milestone forbids, and the way
	// to not have one is to not register it.
	//
	// The way back out is registered here too (M12/T007) and from the same
	// question, so the door a browser can open and the door it can close appear
	// and disappear together. It is the one of the three that is *not* in front of
	// layer 1: it goes through handleAction like every other mutating browser
	// route, because by the time it runs the caller holds the credential the other
	// two exist to produce. See logout.go.
	if door, ok := passwordDoorOf(browser); ok {
		s.handleLogin(patternLoginPage, audit.ActionLoginView, s.loginPage)
		s.handleLogin(patternLoginSubmit, audit.ActionLoginSubmit, s.login(door))
		s.handleAction(patternLogout, audit.ActionLoginSignOut, s.logout(door))
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

// handleAction is handleBrowser for a route that changes something: the one
// place a mutating dashboard route reaches the mux, so an action cannot be
// registered without the gate contracts/actions.md puts in front of it.
//
// It is a second function rather than a boolean on the first because the two
// differ in what they promise, and the promise is the whole of milestone 3's
// defence. A route registered with handleBrowser is authorised by an ambient
// Access cookie alone — which is exactly what a hostile third-party page can
// cause a browser to send — and every test in this package would still pass with
// the cross-site checks absent. Making the choice a parameter would make it a
// thing a call site can get wrong quietly; making it a different function makes
// registering an action without the gate a thing somebody has to type.
//
// Nothing is appended to s.registered, for the reason handleBrowser appends
// nothing: that list is contracts/http-api.md's closed set of six operations,
// each authorised by a signature, and these are authorised by an identity and a
// page token instead.
func (s *Server) handleAction(pattern string, action audit.Action, h http.HandlerFunc) {
	s.mux.Handle(pattern, s.authorizeAction(action, h))
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
//
// It refuses a non-clean path itself rather than letting the mux answer, because
// ServeMux applies its own cleanPath redirect *before* it matches a pattern. A
// request for `//sessions` or `/static/../sessions` was therefore answered by
// net/http with a bare 301 — ahead of layer 1, ahead of the audit middleware, and
// carrying none of the headers docs/security.md requires on a refusal. That is a
// hole in FR-016's "one record per request": a scan of the listener using only
// non-clean paths produced no trail at all, which is precisely the traffic
// route.unknown exists to record.
//
// Refusing rather than redirecting is deliberate. A redirect would send the
// caller somewhere the signature it computed no longer covers — milestone 1 signs
// EscapedPath, so a client that followed one would fail layer 2 for a reason it
// could not see. There is no legitimate caller of a non-clean path here: the
// dashboard emits none, and the API client signs the path it sends.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if p := r.URL.EscapedPath(); p != "" && p != cleanPath(p) {
		s.authenticateBrowser(audit.ActionUnknownRoute, http.HandlerFunc(s.notFound)).ServeHTTP(w, r)
		return
	}
	s.mux.ServeHTTP(w, r)
}

// cleanPath is net/http's own normalisation, reproduced because the version in
// net/http is unexported and this needs to ask the same question the mux would
// ask a moment later. Matching its answer is the point: a path this calls clean
// and the mux does not would be a request refused here that would have been
// served, and the reverse would leave the hole open.
func cleanPath(p string) string {
	if p == "" {
		return "/"
	}
	if p[0] != '/' {
		p = "/" + p
	}
	np := path.Clean(p)
	// path.Clean removes a trailing slash except for the root; the mux keeps one
	// that was there, so restore it before comparing.
	if p[len(p)-1] == '/' && np != "/" {
		np += "/"
	}
	return np
}

// reasonAdopted is the server-authored account of a startup adoption. Like every
// other reason in the trail it is a constant in this repo, built from nothing the
// host or a caller supplied (FR-042).
const reasonAdopted = "took back a tmux session that outlived the daemon that started it"

// reasonRecovered is the server's account of a session the journal remembered
// and the host had lost — the OOM case spec 012 exists for, where the whole
// tmux-spawn cgroup went and took the session with it.
const reasonRecovered = "took back a session the journal recorded and the host no longer had"

// reasonRecoverRefused is the same act refused. It is Allow/Deny rather than an
// error for the reason a narrowed adoption is: the operator's current
// configuration declining to reinstate a session is the allowlist, the ceiling
// or the cap working, not the daemon failing.
const reasonRecoverRefused = "did not take back a session the journal recorded"

// reasonAdoptedNarrowed is the same act with one thing worth saying about it: the
// lifetime the host had recorded for the session is not one this daemon's current
// configuration would grant, so the session came back under the configured
// default instead (FR-011).
//
// It is a separate constant rather than the reason above with a value appended,
// for reasonAdopted's own reason: the trail carries no byte the host or a caller
// supplied, and a lifetime read off a tmux option is exactly that.
//
// An operator finds out here or not at all. The card will simply show a deadline
// where the operator remembers switching one off, and nothing else in the daemon
// is going to mention that it changed its mind.
const reasonAdoptedNarrowed = "took back a tmux session, under this daemon's configured lifetime: the one recorded for it is past the ceiling now in force"

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

	// Before Adopt, and before anything creates a session. A tmux server keeps
	// the environment of whichever client started it for its whole life, and
	// this daemon's server outlives the daemon — adoption is the reason it does.
	// So a host that ran a build predating the environment boundary still has a
	// server holding that build's whole environment, shared secret included, and
	// hands it to every session created next.
	//
	// Reported rather than fatal. A daemon that refused to start because it
	// could not tidy a tmux server would be trading a reachable host for a
	// tidier one, and the sessions already running are unreachable to this
	// either way (see internal/tmuxctl/env.go).
	failures := []error{}
	if removed, err := s.sessions.ReconcileEnvironment(ctx); err != nil {
		failures = append(failures, fmt.Errorf("reconcile the tmux server environment: %w", err))
	} else if len(removed) > 0 {
		if err := s.trail.Emit(audit.Record{
			Action:   audit.ActionStartupScrubEnv,
			Caller:   string(auth.CallerOperator),
			Decision: audit.Allow,
			Reason:   fmt.Sprintf("removed %d variable(s) an older build left in the tmux server environment", len(removed)),
		}); err != nil {
			failures = append(failures, err)
		}
	}

	// Before Adopt, and the order is the contract (contracts/session-journal.md):
	// the host is the authority on what is *running* and the journal on what
	// *should be*, so anything tmux still has is left entirely to Adopt below.
	//
	// Nothing is started here. What this does is put the records back, so that
	// the supervisor's ordinary sweep recreates them under the same rules as any
	// other revival rather than through a second path that could differ.
	recovered, stats, err := s.sessions.ReplayJournal(ctx)
	failures = append(failures, err)
	if stats.Discarded > 0 || stats.SkippedVersion > 0 {
		// Said out loud rather than swallowed. A discarded line is the
		// half-written record of an unclean stop, and an operator who is losing
		// them should learn it from the daemon rather than from a session that
		// did not come back.
		log.Printf("crswd: session journal: %d record(s) read, %d discarded as unreadable, %d written by a newer version",
			stats.Records, stats.Discarded, stats.SkippedVersion)
	}
	for _, r := range recovered {
		reason, decision := reasonRecovered, audit.Allow
		if r.Reason != "" {
			reason, decision = reasonRecoverRefused+": "+r.Reason, audit.Deny
		}
		if err := s.trail.Emit(audit.Record{
			Action:    audit.ActionStartupAdopt,
			Caller:    string(r.Session.Owner),
			SessionID: r.Session.ID,
			Decision:  decision,
			Reason:    reason,
		}); err != nil {
			failures = append(failures, err)
		}
	}

	adopted, err := s.sessions.Adopt(ctx)
	failures = append(failures, err)

	for _, a := range adopted {
		// The owner is the record's own, not a constant repeated here: the
		// caller field has to name whoever the ownership check will compare
		// against, and Adopt is the one that decided it.
		reason := reasonAdopted
		if a.LifetimeNarrowed {
			reason = reasonAdoptedNarrowed
		}
		if err := s.trail.Emit(audit.Record{
			Action:    audit.ActionStartupAdopt,
			Caller:    string(a.Session.Owner),
			SessionID: a.Session.ID,
			Decision:  audit.Allow,
			Reason:    reason,
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

// StartReaper launches the absolute-lifetime sweep and returns once it
// is running. It stops when ctx is done.
//
// It lives here rather than in main because the reaper needs the manager and the
// audit sink, and this package owns both — main holding either of them would be a
// second route to the one thing in the daemon that can cause execution on the
// host. Failing to build one is fatal to the caller for the same reason a missing
// secret is: a daemon serving without a reaper has no ceiling, which is one of
// the bounds Principle VI calls non-negotiable, and the sessions it starts would
// then only ever end by an explicit destroy or a restart.
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

// StartSupervisor launches the revival sweep and returns once it is running
// (spec 012).
//
// It is a second goroutine beside the reaper rather than more work inside it,
// because the two move in opposite directions: the reaper ends sessions the
// operator stopped coming back for, and this one restarts sessions the operator
// never asked to lose.
//
// Started at the same point in startup and for the same reason: no session
// should exist without the sweep that watches it already running.
func (s *Server) StartSupervisor(ctx context.Context) error {
	supervisor, err := session.NewSupervisor(s.sessions, s.trail)
	if err != nil {
		return fmt.Errorf("httpapi: start the supervisor: %w", err)
	}
	go supervisor.Run(ctx)
	return nil
}

// Listen binds the configured address and refuses to keep a listener the daemon
// is not allowed to have.
//
// The assertion is on ln.Addr(), not on the string that was asked for. The
// configured value has been checked twice by the time it gets here, but only
// what the kernel returns says what is actually reachable; a listener that came
// back bound to a wildcard on a daemon whose dashboard admits nobody is closed
// rather than served, because by then the socket already exists.
func (s *Server) Listen() error {
	if s.ln != nil {
		return fmt.Errorf("httpapi: already listening on %s", s.ln.Addr())
	}

	ln, err := s.listen("tcp", s.cfg.Listen)
	if err != nil {
		return fmt.Errorf("httpapi: bind %s: %w", s.cfg.Listen, err)
	}
	if err := assertBoundAddress(ln.Addr(), mayBindOffLoopback(s.cfg, s.browser)); err != nil {
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
//
// Streams are ended before any of it (FR-034f). An output stream is an in-flight
// request that never finishes on its own, so a drain that had to wait for one
// would wait out its entire budget and then close it anyway — and the budget is
// not the cost. Behind the drain is the verified teardown of every session, so a
// forgotten browser tab would be deciding how long unsandboxed shells outlive
// the daemon that owns them, which is precisely the bound Principle VI refuses
// to leave to a peer. Ending them first costs the watcher a farewell that
// contracts/stream.md never promised.
func (s *Server) Shutdown(ctx context.Context) error {
	s.closeStreams.Do(func() { close(s.closing) })

	drain, cancel := context.WithTimeout(ctx, shutdownDrain)
	defer cancel()

	var failures []error
	if err := s.http.Shutdown(drain); err != nil {
		failures = append(failures, fmt.Errorf("httpapi: drain requests in flight: %w", errors.Join(err, s.http.Close())))
	}

	// Sessions survive a stop by default (#63).
	//
	// This used to tear the fleet down unconditionally, and the reasoning was
	// Principle VI: no unsandboxed shell outliving the daemon that owns it. The
	// reasoning is right and the conclusion was too broad. A *crash* already
	// leaves every session running — that is why startup adoption exists — so
	// destroying here only ever covered the graceful case, which is precisely
	// the case where the daemon is usually about to start again ten seconds
	// later. Every redeploy cost an operator their whole fleet.
	//
	// What replaces it is adoption, which is not a weaker bound: a reclaimed
	// session gets its deadlines back from the host's own session_created, so
	// the absolute ceiling is measured from when the work actually began rather
	// than from when the daemon noticed it. A session cannot outlive its bound
	// by being handed between daemons.
	//
	// The old behaviour is one variable away for a deployment that wants it —
	// a host being decommissioned rather than updated, say.
	if s.cfg.DestroyOnShutdown {
		if _, err := s.sessions.DestroyAll(ctx); err != nil {
			failures = append(failures, fmt.Errorf("httpapi: tear every session down before exit: %w", err))
		}
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

// mayBindOffLoopback reports whether this daemon is allowed to be somewhere the
// network can reach it — which it is exactly when a browser arriving there meets
// a door that can let them in (M12).
//
// Both halves are asked, and they are not the same question. The configuration
// is what the operator asked for; the door is what was actually wired in front
// of the dashboard. A closedDoor daemon refuses off loopback however its file
// reads, because a listener nobody can authenticate through is the one thing the
// bind guard has always existed to prevent, and a daemon whose configuration
// names a door it did not build is a wiring defect rather than a licence.
//
// It is also what keeps the development build where it has always been. That
// build's layer 1 is not a closedDoor and admits every browser without checking
// anything, so the door half alone would hand the one build that authenticates
// nobody the widest bind — the config half says no, since a bypass daemon
// configures no door at all, and internal/access refuses a non-loopback listener
// under it besides.
func mayBindOffLoopback(cfg *config.Config, door layer1) bool {
	if _, closed := door.(closedDoor); closed {
		return false
	}
	return cfg.AccessTeamDomain != "" || len(cfg.DashboardPassword) > 0
}

// assertBindAddress re-checks the configured address (FR-005).
//
// config.Load already applies this rule, so this is the second of three checks
// and deliberately not the last. A Config is a struct: a test, a future caller,
// or a startup path that reorders two lines can produce one that never went
// through Load. The guarantee has to belong to the Server, not to one route into
// it.
//
// offLoopbackOK is mayBindOffLoopback's answer, taken as an argument so that the
// two callers below cannot ask it differently. It relaxes the loopback demand
// and nothing else: an address that is not an IP literal is refused either way,
// because a name is whatever a resolver says it is and the point of this value
// is to say where the daemon will be.
func assertBindAddress(addr string, offLoopbackOK bool) error {
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
		return fmt.Errorf("httpapi: listen host %q must be an IP literal such as 127.0.0.1 or ::1: %w", host, ErrNotLoopback)
	}
	if !ip.IsLoopback() && !offLoopbackOK {
		return fmt.Errorf("httpapi: listen host %q is not loopback and this daemon's dashboard admits nobody: %w", host, ErrNotLoopback)
	}
	return nil
}

// assertBoundAddress checks the address a listener came back with, and is the
// last check before the socket starts accepting.
//
// It fails closed on an address it does not recognise: an unknown type is not
// evidence of loopback. A daemon permitted off loopback has nothing left for
// this check to establish — it is allowed to be reachable, whatever the kernel
// handed back — so the question it answers is only ever asked of the daemon that
// is not.
func assertBoundAddress(addr net.Addr, offLoopbackOK bool) error {
	if offLoopbackOK {
		return nil
	}
	tcp, ok := addr.(*net.TCPAddr)
	if !ok {
		return fmt.Errorf("httpapi: bound to %v, which is not a TCP address but a %T: %w", addr, addr, ErrNotLoopback)
	}
	if !tcp.IP.IsLoopback() {
		return fmt.Errorf("httpapi: bound to %v, which is not loopback: %w", addr, ErrNotLoopback)
	}
	return nil
}
