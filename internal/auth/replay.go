package auth

import (
	"sync"
	"time"
)

// replayTTL is how long a signature that has been accepted once stays refused
// (FR-010).
//
// It is derived from the window rather than restated as 600 seconds, because
// the two numbers are not independent. A request stamped at ts satisfies the
// window while |now - ts| <= maxSkew, so a signature first accepted at T can
// still satisfy it as late as T + 2×maxSkew — no earlier, no later. A shorter
// TTL leaves a gap at the far end of the window in which the same captured
// bytes buy a second unsandboxed session; a longer one only holds memory.
// Widening maxSkew widens this with it, which is the point.
const replayTTL = 2 * maxSkew

// replayCache remembers the signatures Verify has already accepted, so a
// captured request is good exactly once rather than for the whole window
// (FR-010).
//
// Entries are swept on write rather than by a goroutine: they are only of
// interest to a request that might replay them, every accepted request touches
// the map anyway, and a background sweeper is a second thing to shut down.
//
// Only signatures that have already passed hmac.Equal are ever recorded, so
// what the map holds is bounded by traffic that possesses the shared secret —
// a stranger cannot grow it. Verify enforces that ordering; see the call site.
type replayCache struct {
	clock Clock

	mu   sync.Mutex
	seen map[string]time.Time
}

func newReplayCache(clock Clock) *replayCache {
	return &replayCache{clock: clock, seen: make(map[string]time.Time)}
}

// Observe records a signature and reports whether this was its first use.
//
// The check and the record happen inside **one** critical section deliberately.
// Split into a Seen() followed by a Record(), two copies of the same captured
// request arriving together would both find the cache empty and both be
// allowed — the spec's "sent twice, concurrently" edge case — and one extra
// winner there is one extra unsandboxed session.
func (c *replayCache) Observe(signature string) bool {
	now := c.clock.Now()

	c.mu.Lock()
	defer c.mu.Unlock()

	if seenAt, ok := c.seen[signature]; ok && !c.expired(seenAt, now) {
		return false
	}

	for sig, seenAt := range c.seen {
		if c.expired(seenAt, now) {
			delete(c.seen, sig)
		}
	}

	c.seen[signature] = now
	return true
}

// expired answers whether an entry observed at seenAt has outlived its TTL.
//
// The boundary is exclusive — an entry exactly replayTTL old is still live —
// and that is not a rounding preference. A signature first seen at T may carry
// a timestamp as late as T+maxSkew and so passes the window right up to and
// including T+2×maxSkew. Expiring *at* the boundary would leave that final
// instant replayable.
//
// A clock that jumps backwards yields a negative elapsed time, which is not
// greater than the TTL, so the entry is kept. Failing closed is the only safe
// direction here: the cost is a refused honest request, not an accepted replay.
func (c *replayCache) expired(seenAt, now time.Time) bool {
	return now.Sub(seenAt) > replayTTL
}
