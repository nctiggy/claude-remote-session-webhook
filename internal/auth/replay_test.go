// This file is deliberately an *internal* test (package auth), unlike
// hmac_test.go next to it. FR-010's TTL is longer than anything observable from
// outside: a signature stops passing the window at the same instant its cache
// entry expires, never before it, so "the entry outlived the window" cannot be
// demonstrated past that point through Verify alone. Sweeping on write is
// likewise a statement about the map, not about a response. Testing those from
// inside costs a few duplicated fixtures and keeps replayCache from having to
// be exported — or, worse, from growing a Size() method that exists only so a
// test can call it.
package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// Spelled in plain words for the reason iterations 8 and 10 recorded: a run of
// hex next to the word "secret" is what a real key looks like, to gitleaks and
// to gosec alike. Exactly 32 bytes, so the fixture is still realistic.
const replaySecret = "test-only-replay-secret-for-auth"

// The example instant from contracts/http-api.md, as in hmac_test.go, so a
// fixture moved between the two files keeps its meaning.
const replayTimestamp int64 = 1785706480

// replayWindow and replayWindowTTL restate FR-008's 300 seconds and FR-010's
// "twice the signature window" rather than reading maxSkew and replayTTL. A
// test that imports the number it is checking cannot notice that number
// changing; TestReplayTTLIsTwiceTheSignatureWindow is where the two meet.
const (
	replayWindow    = 300 * time.Second
	replayWindowTTL = 2 * replayWindow
)

const replayMaxBody int64 = 4096

// stepClock is the advanceable clock the window tests did not need. Guarded,
// because the concurrency tests read it from every goroutine at once.
type stepClock struct {
	mu  sync.Mutex
	now time.Time
}

func newStepClock() *stepClock {
	return &stepClock{now: time.Unix(replayTimestamp, 0)}
}

func (c *stepClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *stepClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// replaySignatureOver recomputes the header value from the contract's own
// description, independently of sign(). Calling the production code to build
// the fixture would mirror a payload bug into the thing meant to catch it.
func replaySignatureOver(t *testing.T, timestamp int64, body string) string {
	t.Helper()

	mac := hmac.New(sha256.New, []byte(replaySecret))
	if _, err := io.WriteString(mac, strconv.FormatInt(timestamp, 10)+"."+body); err != nil {
		t.Fatalf("building the signature fixture: %v", err)
	}
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

// replayRequest builds one request. Every call produces an independent
// *http.Request carrying byte-identical headers and body — which is what a
// replay is on the wire, and is why a test cannot simply re-Verify the same
// value: Verify consumes the body and re-buffers it.
func replayRequest(t *testing.T, timestamp int64, body string) *http.Request {
	t.Helper()

	r := httptest.NewRequest(http.MethodPost, "/sessions", strings.NewReader(body))
	r.Header.Set(HeaderTimestamp, strconv.FormatInt(timestamp, 10))
	r.Header.Set(HeaderSignature, replaySignatureOver(t, timestamp, body))
	return r
}

func newReplayAuth(t *testing.T, clock Clock) *Authenticator {
	t.Helper()

	a, err := NewWithClock([]byte(replaySecret), replayMaxBody, clock)
	if err != nil {
		t.Fatalf("NewWithClock() unexpected error: %v", err)
	}
	return a
}

// verifyReason returns the server-side reason behind a refusal, since Verify's
// own error is opaque and identical for every check (caller.go). The helper of
// the same name in hmac_test.go cannot be shared: that file is package
// auth_test and this one is package auth.
//
// It carries the same invariant with it — an identity comes back exactly when
// the request was accepted — so the replay cases assert it too.
func verifyReason(t *testing.T, a *Authenticator, r *http.Request) error {
	t.Helper()

	caller, err := a.Verify(r)
	switch {
	case err != nil && caller != nil:
		t.Error("Verify() returned a Caller alongside a denial")
	case err == nil && caller == nil:
		t.Error("Verify() accepted a request without naming its caller")
	}
	return Reason(err)
}

func (c *replayCache) entryCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.seen)
}

// TestReplayTTLIsTwiceTheSignatureWindow is the one place the derived constant
// is checked against the two numbers FR-008 and FR-010 actually name. It fails
// if someone "simplifies" replayTTL to a literal that then drifts from maxSkew.
func TestReplayTTLIsTwiceTheSignatureWindow(t *testing.T) {
	t.Parallel()

	if maxSkew != replayWindow {
		t.Errorf("maxSkew = %v, want the %v FR-008 names", maxSkew, replayWindow)
	}
	if replayTTL != replayWindowTTL {
		t.Errorf("replayTTL = %v, want %v (twice the window, FR-010)", replayTTL, replayWindowTTL)
	}
}

// TestVerifyRefusesAReplayedRequest is the whole point of the task, in the
// shape AGENTS.md gives it: the first use of a correctly signed request passes,
// and the identical bytes sent again do not.
func TestVerifyRefusesAReplayedRequest(t *testing.T) {
	t.Parallel()

	const body = `{"name":"demo","work_dir":"/home/u/code/x"}`
	a := newReplayAuth(t, newStepClock())

	if err := verifyReason(t, a, replayRequest(t, replayTimestamp, body)); err != nil {
		t.Fatalf("Verify() rejected a correctly signed first use: %v", err)
	}

	err := verifyReason(t, a, replayRequest(t, replayTimestamp, body))
	if !errors.Is(err, ErrReplayedRequest) {
		t.Fatalf("Verify() on a replayed request = %v, want ErrReplayedRequest", err)
	}
}

// TestVerifyRefusesAReplayAnywhereInTheWindow is the assertion the TTL exists
// for: for as long as the window would still accept a captured request, the
// cache must still be refusing it.
//
// The last case is the extreme the TTL is sized against — a request stamped 300
// seconds ahead, first used at once, and replayed 600 seconds later, which is
// the final instant at which any signature can still satisfy the window. An
// entry that expired *at* its TTL rather than after it would let that one
// through, and no other test in the suite would notice.
func TestVerifyRefusesAReplayAnywhereInTheWindow(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		stamp time.Duration // where the request's timestamp sits relative to the clock
		later time.Duration // how far the clock moves between the two uses
	}{
		{name: "immediately", stamp: 0, later: 0},
		{name: "a minute later", stamp: 0, later: time.Minute},
		{name: "at the far edge of the window", stamp: 0, later: replayWindow},
		{name: "stamped early, replayed at once", stamp: -replayWindow, later: 0},
		{name: "stamped late, replayed at the last acceptable instant", stamp: replayWindow, later: replayWindowTTL},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			clock := newStepClock()
			a := newReplayAuth(t, clock)
			stamp := replayTimestamp + int64(tt.stamp.Seconds())

			if err := verifyReason(t, a, replayRequest(t, stamp, "")); err != nil {
				t.Fatalf("Verify() rejected the first use: %v", err)
			}

			clock.Advance(tt.later)

			// ErrTimestampOutsideWindow here would mean the case had drifted
			// out of the range where the cache is what refuses a replay, and
			// the case would be proving nothing.
			if err := verifyReason(t, a, replayRequest(t, stamp, "")); !errors.Is(err, ErrReplayedRequest) {
				t.Fatalf("Verify() %v after the first use = %v, want ErrReplayedRequest", tt.later, err)
			}
		})
	}
}

// TestVerifyDistinguishesDifferentRequests guards against a keying mistake. The
// cache keys on the signature, so requests differing in body or in timestamp
// are separate entries and one must not shadow another — a cache that refused
// every second request would pass the test above for entirely the wrong reason.
func TestVerifyDistinguishesDifferentRequests(t *testing.T) {
	t.Parallel()

	a := newReplayAuth(t, newStepClock())

	requests := []*http.Request{
		replayRequest(t, replayTimestamp, `{"name":"one"}`),
		replayRequest(t, replayTimestamp, `{"name":"two"}`),
		replayRequest(t, replayTimestamp-1, `{"name":"one"}`),
	}

	for i, r := range requests {
		if err := verifyReason(t, a, r); err != nil {
			t.Fatalf("Verify() rejected distinct request %d: %v", i, err)
		}
	}
}

// TestVerifyRecordsOnlyGenuineSignatures pins the ordering inside Verify: the
// cache is written only after hmac.Equal has passed.
//
// The attack it forecloses needs no secret. The cache is keyed on the value the
// *daemon* computes, so an attacker who knows only the bytes an honest caller
// is about to send — a guessable create body at a predictable second — can send
// those bytes first under a junk signature. If the entry were recorded before
// the compare, that junk request would reserve the honest caller's signature
// and the real request would be refused as a replay when it arrived. The same
// ordering is what keeps the map from growing on unauthenticated traffic.
func TestVerifyRecordsOnlyGenuineSignatures(t *testing.T) {
	t.Parallel()

	const genuine = `{"name":"demo","work_dir":"/home/u/code/x"}`
	a := newReplayAuth(t, newStepClock())

	forged := replayRequest(t, replayTimestamp, genuine)
	forged.Header.Set(HeaderSignature, "sha256=not-a-real-signature")

	if err := verifyReason(t, a, forged); !errors.Is(err, ErrSignatureMismatch) {
		t.Fatalf("Verify() on a junk signature = %v, want ErrSignatureMismatch", err)
	}
	if got := a.replay.entryCount(); got != 0 {
		t.Fatalf("cache holds %d entries after a request that failed the signature check, want 0", got)
	}

	if err := verifyReason(t, a, replayRequest(t, replayTimestamp, genuine)); err != nil {
		t.Fatalf("Verify() rejected the genuine request after a forgery raced it with the same bytes: %v", err)
	}
}

// TestVerifyRefusesConcurrentReplays is the spec's "sent twice, concurrently"
// edge case, and the reason Observe is one critical section. Run with -race.
func TestVerifyRefusesConcurrentReplays(t *testing.T) {
	t.Parallel()

	const racers = 16
	a := newReplayAuth(t, newStepClock())

	// Built up front: t.Fatalf may not be called from these goroutines.
	requests := make([]*http.Request, racers)
	for i := range requests {
		requests[i] = replayRequest(t, replayTimestamp, `{"name":"demo"}`)
	}

	var (
		start sync.WaitGroup
		done  sync.WaitGroup
	)
	start.Add(1)
	errs := make([]error, racers)

	for i := range requests {
		done.Add(1)
		go func() {
			defer done.Done()
			start.Wait()
			errs[i] = verifyReason(t, a, requests[i])
		}()
	}

	start.Done()
	done.Wait()

	winners := 0
	for i, err := range errs {
		switch {
		case err == nil:
			winners++
		case errors.Is(err, ErrReplayedRequest):
		default:
			t.Errorf("Verify() racer %d = %v, want nil or ErrReplayedRequest", i, err)
		}
	}
	if winners != 1 {
		t.Fatalf("%d of %d concurrent replays were accepted, want exactly 1", winners, racers)
	}
}

// TestReplayCacheRefusesASecondObservation is Observe's contract on its own,
// away from a request.
func TestReplayCacheRefusesASecondObservation(t *testing.T) {
	t.Parallel()

	c := newReplayCache(newStepClock())

	if !c.Observe("sha256=a-first-signature") {
		t.Fatal("Observe() refused a signature it had never seen")
	}
	if c.Observe("sha256=a-first-signature") {
		t.Fatal("Observe() accepted the same signature twice")
	}
	if !c.Observe("sha256=a-second-signature") {
		t.Fatal("Observe() refused a different signature")
	}
}

// TestReplayCacheHoldsAnEntryForTheWholeTTL walks the expiry boundary from the
// inside, where it can be walked past the point the window would stop a replay
// anyway. The boundary is exclusive on purpose — see expired().
func TestReplayCacheHoldsAnEntryForTheWholeTTL(t *testing.T) {
	t.Parallel()

	const sig = "sha256=an-aging-signature"

	clock := newStepClock()
	c := newReplayCache(clock)

	if !c.Observe(sig) {
		t.Fatal("Observe() refused a signature it had never seen")
	}

	clock.Advance(replayWindowTTL - time.Nanosecond)
	if c.Observe(sig) {
		t.Fatal("Observe() forgot a signature a nanosecond before its TTL elapsed")
	}

	clock.Advance(time.Nanosecond)
	if c.Observe(sig) {
		t.Fatalf("Observe() forgot a signature at exactly %v; the window still accepts one there", replayWindowTTL)
	}

	clock.Advance(time.Nanosecond)
	if !c.Observe(sig) {
		t.Fatalf("Observe() still refused a signature past %v, when no window can accept it", replayWindowTTL)
	}
}

// TestReplayCacheSweepsExpiredEntriesOnWrite is the memory claim. Nothing in a
// response reveals it, so it is asserted against the map: a cache driven for
// days must not still be holding every signature it has ever seen.
func TestReplayCacheSweepsExpiredEntriesOnWrite(t *testing.T) {
	t.Parallel()

	clock := newStepClock()
	c := newReplayCache(clock)

	for _, sig := range []string{"sha256=one", "sha256=two", "sha256=three"} {
		if !c.Observe(sig) {
			t.Fatalf("Observe(%q) refused a signature it had never seen", sig)
		}
	}
	if got := c.entryCount(); got != 3 {
		t.Fatalf("cache holds %d entries after three distinct signatures, want 3", got)
	}

	// Nothing is swept while the entries are live, however many writes arrive.
	clock.Advance(replayWindowTTL)
	if !c.Observe("sha256=four") {
		t.Fatal("Observe() refused a fresh signature")
	}
	if got := c.entryCount(); got != 4 {
		t.Fatalf("cache holds %d entries while all four are live, want 4", got)
	}

	// One nanosecond past the TTL the first three are expired, and the next
	// write is what clears them. The fourth is younger and stays.
	clock.Advance(time.Nanosecond)
	if !c.Observe("sha256=five") {
		t.Fatal("Observe() refused a fresh signature")
	}
	if got := c.entryCount(); got != 2 {
		t.Fatalf("cache holds %d entries after the sweep, want 2 (four and five)", got)
	}
}

// TestReplayCacheKeepsEntriesWhenTheClockGoesBackwards covers the host whose
// clock is corrected backwards mid-flight. Elapsed time goes negative, which
// must not read as "expired long ago" and hand back every signature the daemon
// has accepted in the last ten minutes.
func TestReplayCacheKeepsEntriesWhenTheClockGoesBackwards(t *testing.T) {
	t.Parallel()

	const sig = "sha256=a-signature-seen-before-the-jump"

	clock := newStepClock()
	c := newReplayCache(clock)

	if !c.Observe(sig) {
		t.Fatal("Observe() refused a signature it had never seen")
	}

	clock.Advance(-time.Hour)
	if c.Observe(sig) {
		t.Fatal("Observe() accepted a replay after the clock jumped backwards")
	}
}

// gateClock holds every caller of Now until a fixed number of them have
// arrived, then releases them together.
//
// It exists because a plain "start all the goroutines at once" test does not
// actually catch a split critical section — measured, not assumed: a version of
// Observe that checked under one lock and recorded under another passed that
// test every time. Observe reads the clock as its first act, so blocking there
// parks every racer immediately before the section under test and lets them all
// go at once, which turns a rare interleaving into the expected one.
type gateClock struct {
	now time.Time

	mu      sync.Mutex
	arrived int
	waiting int
	open    chan struct{}
}

func newGateClock(racers int) *gateClock {
	return &gateClock{
		now:     time.Unix(replayTimestamp, 0),
		waiting: racers,
		open:    make(chan struct{}),
	}
}

func (c *gateClock) Now() time.Time {
	c.mu.Lock()
	c.arrived++
	if c.arrived == c.waiting {
		close(c.open)
	}
	c.mu.Unlock()

	<-c.open
	return c.now
}

// TestReplayCacheObservesAtomically is the unit-level twin of the concurrent
// Verify test, and the one that would notice Observe being split into a Seen()
// and a Record(): every racer is held at the gate until all of them have
// arrived, so they enter the critical section together. Exactly one may win.
// Run with -race.
func TestReplayCacheObservesAtomically(t *testing.T) {
	t.Parallel()

	const racers = 256
	const rounds = 64
	const sig = "sha256=a-contended-signature"

	for round := range rounds {
		c := newReplayCache(newGateClock(racers))

		var (
			done sync.WaitGroup
			mu   sync.Mutex
		)
		winners := 0

		for range racers {
			done.Add(1)
			go func() {
				defer done.Done()
				if c.Observe(sig) {
					mu.Lock()
					winners++
					mu.Unlock()
				}
			}()
		}

		done.Wait()

		if winners != 1 {
			t.Fatalf("round %d: %d of %d concurrent Observe calls won, want exactly 1", round, winners, racers)
		}
		if got := c.entryCount(); got != 1 {
			t.Fatalf("round %d: cache holds %d entries for one signature, want 1", round, got)
		}
	}
}
