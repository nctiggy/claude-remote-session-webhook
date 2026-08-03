package httpapi

import (
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/nctiggy/claude-remote-session-webhook/internal/auth"
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

// limiter is the per-caller token bucket FR-037 requires on session creation:
// one bucket per authenticated identity, filling at CRSW_CREATE_RATE_PER_MIN and
// holding burstFor(rate) tokens.
//
// A create spends a token whatever it goes on to answer. A malformed body and a
// session that could not be started both cost the host the work of getting that
// far, so a budget that only counted successes would let a caller spend the
// daemon's time for free by getting it wrong on purpose.
//
// It is per caller and deliberately not one budget for the whole daemon. The
// bound that protects the host from the fleet as a whole is the concurrent
// session cap (FR-036); this one bounds how fast any single identity may ask, so
// that milestone 2's second caller cannot be starved by the first.
type limiter struct {
	perMinute int
	burst     float64
	clock     clock

	mu      sync.Mutex
	buckets map[auth.CallerID]bucket
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
func newLimiter(perMinute int, clk clock) (*limiter, error) {
	if perMinute < 1 {
		return nil, fmt.Errorf("httpapi: a create rate of %d per minute bounds nothing; refusing to start", perMinute)
	}
	if clk == nil {
		return nil, errors.New("httpapi: no clock provided for the create rate limiter; refusing to start")
	}
	return &limiter{
		perMinute: perMinute,
		burst:     float64(burstFor(perMinute)),
		clock:     clk,
		buckets:   make(map[auth.CallerID]bucket),
	}, nil
}

// allow reports whether this caller may spend a create now, and spends it if so.
//
// The check and the spend happen inside **one** critical section, for the reason
// the replay cache's do: split into a "may I" followed by a "then I will", a
// burst of requests arriving together would all read the same budget and all be
// allowed, and one extra winner here is one extra unsandboxed session.
func (l *limiter) allow(id auth.CallerID) bool {
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
func (l *limiter) refill(b bucket, now time.Time) bucket {
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
// shut down. What the map can hold is bounded by the identities layer 2 has
// authenticated — there is exactly one today — so a stranger cannot grow it.
//
// The buckets are refilled to now before being judged, because only the caller
// doing the asking has had its own entry brought up to date.
func (l *limiter) forgetFull(now time.Time) {
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
