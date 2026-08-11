package httpapi

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"sync"
	"time"
)

// clock is this package's view of time, injected rather than read from the wall
// clock inside the limiter (FR-039 states the rule for the reaper; the reason is
// the same here). A limiter that called time.Now itself could only be tested by
// sleeping, and it is precisely why golang.org/x/time/rate was not imported —
// research.md D7 rejects it as one dependency for ~40 lines whose behaviour, on
// this daemon, is what happens as time passes.
type clock interface{ Now() time.Time }

// systemClock is the host clock, chosen in exactly one place (New) so that every
// other path takes whatever it was given.
type systemClock struct{}

func (systemClock) Now() time.Time { return time.Now() }

// burstFor is how many creates a caller may make back to back before the rate
// itself starts to bite.
//
// research.md D11 documents the pair as "6 a minute, burst 3", and the
// environment carries only the rate — data-model.md lists no burst variable — so
// the burst is derived from the rate rather than being a second knob free to be
// set in disagreement with the first. Half the rate reproduces the documented
// pair exactly at the documented default, and the floor of one keeps a rate of
// one per minute from meaning "refuse everything".
func burstFor(perMinute int) int { return max(1, perMinute/2) }

// limiter is the token bucket FR-037 requires on session creation: one bucket
// per key, filling at a rate a minute and holding burstFor(rate) tokens.
//
// A request spends a token whatever it goes on to answer. A malformed body and a
// session that could not be started both cost the host the work of getting that
// far, so a budget that only counted successes would let a caller spend the
// daemon's time for free by getting it wrong on purpose.
//
// It is per key and deliberately not one budget for the whole daemon. The bound
// that protects the host from the fleet as a whole is the concurrent session cap
// (FR-036); this one bounds how fast any single key may ask, so that milestone
// 2's second caller cannot be starved by the first.
//
// **What a key is differs by route, and that is why this is generic** (M12/T005).
// The create route keys by auth.CallerID, because it sits behind layer 2 and a
// create's budget is spent by an identity. The sign-in route has no identity to
// key by — producing one is the whole of what it does — so it keys by
// loginSource, the address the attempt arrived from. The type parameter is what
// keeps the two from being interchangeable: an address is not an identity, and a
// limiter that let one be passed as the other would be a budget spent by whoever
// could spell the other's key. The plan's rule is to reuse this bucket rather
// than copy it, and a copy is what a second key type in one map would have
// forced.
type limiter[K comparable] struct {
	perMinute int
	burst     float64
	clock     clock

	mu      sync.Mutex
	buckets map[K]bucket
}

// bucket is one caller's budget: how many tokens are left, and the instant that
// was last true. Nothing else is kept, because nothing else is decided from — and
// a bucket that has refilled to the top says exactly what an absent one says,
// which is what lets allow forget it.
type bucket struct {
	tokens float64
	last   time.Time
}

// newLimiter fails closed on a rate that would not bound anything.
//
// config.Load already refuses a rate below one, so this is the second check and
// deliberately not the only one: a Config is a struct, and a zero rate read as
// "no limit" would be an unbounded create endpoint behind a daemon that started
// cleanly.
//
// `what` names the budget in both refusals, and it exists because there are two
// of them now. A daemon that refused to start over "the create rate limiter"
// when what it could not build was the sign-in one would send its operator to
// the wrong variable, and a startup error is read by somebody who cannot see
// which line raised it.
func newLimiter[K comparable](what string, perMinute int, clk clock) (*limiter[K], error) {
	if perMinute < 1 {
		return nil, fmt.Errorf("httpapi: a %s rate of %d per minute bounds nothing; refusing to start", what, perMinute)
	}
	if clk == nil {
		return nil, fmt.Errorf("httpapi: no clock provided for the %s rate limiter; refusing to start", what)
	}
	return &limiter[K]{
		perMinute: perMinute,
		burst:     float64(burstFor(perMinute)),
		clock:     clk,
		buckets:   make(map[K]bucket),
	}, nil
}

// allow reports whether this caller may spend a create now, and spends it if so.
//
// The check and the spend happen inside **one** critical section, for the reason
// the replay cache's do: split into a "may I" followed by a "then I will", a
// burst of requests arriving together would all read the same budget and all be
// allowed, and one extra winner here is one extra unsandboxed session.
func (l *limiter[K]) allow(id K) bool {
	now := l.clock.Now()

	l.mu.Lock()
	defer l.mu.Unlock()

	b, seen := l.buckets[id]
	if !seen {
		// A caller the limiter has never decided about starts full, which is the
		// same state a caller whose bucket was forgotten starts in. The two must
		// be identical or forgetting one would be a decision.
		b = bucket{tokens: l.burst, last: now}
	}
	b = l.refill(b, now)

	spend := b.tokens >= 1
	if spend {
		b.tokens--
	}
	// Written back on the refusal path too: time passed for a refused caller as
	// much as for an allowed one, and dropping the refill would restart its
	// recovery from whenever it last stopped asking.
	l.buckets[id] = b

	l.forgetFull(now)
	return spend
}

// refill adds the tokens that accrued since the bucket was last touched, never
// past the burst.
//
// A clock that has not moved forward adds nothing and does not move the mark.
// That is the fail-closed direction for a backwards jump — an NTP correction, a
// host resumed from suspend — where moving the mark back would either hand out
// tokens for negative time or leave a mark in the future that pays a windfall
// later. It costs an honest caller a delayed create and nothing else.
func (l *limiter[K]) refill(b bucket, now time.Time) bucket {
	elapsed := now.Sub(b.last)
	if elapsed <= 0 {
		return b
	}

	// Multiplied before it is divided. At the documented rate of six a minute,
	// (10 × 6) / 60 is exactly one token after exactly ten seconds; 10 × (6/60)
	// is exactly nothing in particular.
	b.tokens = min(l.burst, b.tokens+elapsed.Seconds()*float64(l.perMinute)/60)
	b.last = now
	return b
}

// forgetFull drops every bucket that has refilled to the top.
//
// A caller whose bucket is full is in the state a caller the limiter has never
// seen is in, so the entry holds memory for a decision that is already made.
// Sweeping on write rather than from a goroutine matches the replay cache: every
// spend touches the map anyway, and a background sweeper is a second thing to
// shut down.
//
// **This sweep is the whole of what bounds the map, and since M12/T005 that
// matters.** The create limiter's keys are identities layer 2 authenticated —
// there is exactly one — so nothing a stranger did could grow it. The sign-in
// limiter's keys are source addresses, and a stranger chooses those. What holds
// is that a bucket only survives while it is partly spent: every allow drops
// every full one, and a bucket refills in burst/rate minutes, so the map holds
// about as many entries as there were distinct sources in that window and not
// one from before it. The cost of the sweep is linear in that number, paid by
// the request that grew it.
//
// The buckets are refilled to now before being judged, because only the caller
// doing the asking has had its own entry brought up to date.
func (l *limiter[K]) forgetFull(now time.Time) {
	for id, b := range l.buckets {
		if l.refill(b, now).tokens >= l.burst {
			delete(l.buckets, id)
		}
	}
}

// The reasons the limiter records, authored here.
//
// Neither is ever written into a response — the caller gets the same 429 bytes
// the concurrent-session cap answers with — and neither is built from anything
// the caller sent (FR-042, FR-043).
var (
	// errLimitNoCaller is unreachable behind the authentication middleware, and
	// fails closed rather than limiting against an empty identity: every caller
	// with no name would share one budget, which is either a global limit nobody
	// asked for or no limit at all, depending on how many of them there are.
	errLimitNoCaller = errors.New("the create rate limiter was reached with no authenticated caller")

	// errCreateRateExceeded is the rate limit doing its job (FR-037). Like the
	// cap's refusal it says nothing was wrong with the request, and it belongs in
	// the trail for the same reason: an operator seeing it repeatedly is looking
	// at either a runaway client or a rate set below the way the host is used.
	errCreateRateExceeded = errors.New("the create rate limit was exceeded, so the session was refused")
)

// rateLimited is the set of routes a caller spends its budget on.
//
// It is a set rather than a condition on Route so that the answer to "which
// routes are limited" is a list somebody has to add to deliberately. FR-037
// names session creation and only that: the other five routes are an in-memory
// read or a single tmux command, while a create spawns a Claude process, and a
// limit on reads would make the fleet harder to see under exactly the load that
// makes seeing it matter.
var rateLimited = map[Route]bool{
	{http.MethodPost, "/sessions"}: true,
}

// limitCreates is the rate limit as middleware, applied by handle to the routes
// above and to no others.
//
// It sits between authentication and everything else on purpose. Behind layer 2,
// so that the budget is spent by an identity rather than by whoever can open a
// socket — an unauthenticated flood must not be able to exhaust the operator's
// own creates — and ahead of the decoder and the manager, so that a refused
// request costs no body read, no path resolution, and no tmux command.
func (s *Server) limitCreates(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		caller, ok := CallerFrom(r.Context())
		if !ok {
			s.failInternal(w, r, errLimitNoCaller)
			return
		}

		if !s.creates.allow(caller.ID) {
			s.failTooManyRequests(w, r, errCreateRateExceeded)
			return
		}

		next.ServeHTTP(w, r)
	})
}

// --- The sign-in route's budget (M12/T005) ---------------------------------

// loginRatePerMin is how many sign-in attempts one source may make a minute,
// giving burstFor(6) = 3 back to back before the rate itself starts to bite.
//
// A constant and not a variable, deliberately. Every other bound on this daemon
// is configurable because the operator is the one who knows their host; this one
// is about an attacker's budget rather than the operator's work, and a variable
// would be a variable somebody can set to 6000 on the one route in front of
// layer 1. dashboardSessionLifetime and MinDashboardPasswordLen were fixed the
// same way and for the same reason.
//
// **The number is a judgement and the bound is not.** Six a minute is the rate
// research.md D11 documents for creates, chosen here independently: a human
// signing in mistypes once or twice and is not stopped, and an attacker gets
// 8,640 guesses a day against a password config refuses below sixteen
// characters. This limit is not what makes guessing hopeless — the length
// minimum is. What it buys is that the attempt is slow, is loud on the trail,
// and cannot be turned into a flood that costs this host real work. Change the
// constant if you want another.
const loginRatePerMin = 6

// loginSource is one address sign-in attempts are counted against.
//
// It is its own type rather than auth.CallerID, which is the reuse that would
// have saved making the limiter generic. A CallerID is an identity layer 2
// authenticated, and this route has none — establishing one is the whole of what
// it does. An address carried in that type would be the same lie as a function
// called assertLoopback that returns nil for 0.0.0.0, and the type parameter is
// what makes the compiler keep the two budgets from being spent by each other's
// keys.
type loginSource string

// sourceOf is the key: the host the request came from, with the port dropped.
//
// **The port must go.** A browser opens a new ephemeral port per connection, so
// a bucket per host:port is a fresh budget for every attempt, which is a limiter
// that permits everything while looking like one that does not.
//
// It is r.RemoteAddr and never X-Forwarded-For, X-Real-IP, or any other header.
// Those are caller-authored text, and a limiter keyed by a value the caller
// chooses is one the caller opts out of by varying it. This daemon has no
// configured proxy to believe — the same ruling password.go makes about reading
// r.TLS rather than X-Forwarded-Proto — so an operator who does put one in front
// of it gets one bucket for the proxy, which is a limit that is too strict
// rather than one that is absent.
//
// An address with no port in it is used whole. That is the fail-closed
// direction: the alternative readings are to key every such request separately
// (no limit at all) or to refuse them (a daemon reachable over a transport whose
// addresses net cannot split — a Unix socket says "@" — with no way in). One
// shared bucket is neither.
func sourceOf(r *http.Request) loginSource {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return loginSource(r.RemoteAddr)
	}
	return loginSource(host)
}

// errLoginRateExceeded is the sign-in budget doing its job (M12/T005), and it is
// errCreateRateExceeded's counterpart on the other door.
//
// It goes on the trail and never into a response: the caller gets this door's
// uniform 401, byte for byte the same one a wrong password gets, so a stranger
// cannot tell a refused guess from a guess that was never read. That is the
// opposite of the create route's 429 above, and the difference is which caller
// is being answered — a caller that proved who it is may be told to slow down,
// and one that has proved nothing is told nothing at all. What it costs is that
// an operator locked out by their own retries learns why from the trail rather
// than from the page; what it buys is that a brute-forcer cannot tell whether
// their guesses are still being read, and so cannot pace them.
var errLoginRateExceeded = errors.New("the sign-in rate limit was exceeded, so the attempt was refused")
