// Internal test, matching the rest of the package. Most of what a rate limiter
// claims is only visible from inside: the bucket a caller is left holding, the
// entry that is forgotten once it fills, and the clock the whole thing is driven
// by. Nothing here sleeps (FR-039) — the limiter is handed its own clock, which
// is the reason golang.org/x/time/rate was not imported (research.md D7).
package httpapi

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nctiggy/claude-remote-session-webhook/internal/audit"
	"github.com/nctiggy/claude-remote-session-webhook/internal/auth"
	"github.com/nctiggy/claude-remote-session-webhook/internal/config"
)

// testClock is a clock a test moves by hand. It is mutex-guarded because the
// concurrency case reads it from every goroutine at once, and a limiter whose
// clock races is not a limiter that can be trusted under -race.
type testClock struct {
	mu sync.Mutex
	at time.Time
}

func newTestClock(at time.Time) *testClock { return &testClock{at: at} }

func (c *testClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.at
}

func (c *testClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.at = c.at.Add(d)
}

func testLimiter(t *testing.T, perMinute int, clk clock) *limiter[auth.CallerID] {
	t.Helper()

	l, err := newLimiter[auth.CallerID]("create", perMinute, clk)
	if err != nil {
		t.Fatalf("newLimiter(%d, %T) = _, %v; want a limiter", perMinute, clk, err)
	}
	return l
}

// testLoginLimiter is the other budget, on a clock a test moves by hand.
//
// The sign-in limiter a server builds for itself is on the host clock, which is
// the right answer for a daemon and no answer at all for a test: a burst refused
// by a limiter that also refills while the test runs proves the refusal and not
// the recovery. Assigning over s.logins is how a test drives it, the way
// stream_test.go assigns over s.streams.
func testLoginLimiter(t *testing.T, clk clock) *limiter[loginSource] {
	t.Helper()

	l, err := newLimiter[loginSource]("sign-in", loginRatePerMin, clk)
	if err != nil {
		t.Fatalf("newLimiter[loginSource](%d, %T) = _, %v; want a limiter", loginRatePerMin, clk, err)
	}
	return l
}

// spend drives n requests through the limiter and reports how many were allowed.
func spend[K comparable](l *limiter[K], id K, n int) int {
	allowed := 0
	for i := 0; i < n; i++ {
		if l.allow(id) {
			allowed++
		}
	}
	return allowed
}

// TestTheBurstIsHalfTheRateAndNeverLessThanOne pins the derivation research.md
// D11 leaves implicit: it documents "6 a minute, burst 3" while data-model.md
// gives the environment only the rate, so the burst has to come from somewhere
// and this is where. The first row is the documented pair; the last two are the
// floor, which keeps a slow rate from meaning "refuse everything".
func TestTheBurstIsHalfTheRateAndNeverLessThanOne(t *testing.T) {
	t.Parallel()

	cases := map[int]int{
		config.DefaultCreateRatePerMin: 3,
		60:                             30,
		2:                              1,
		1:                              1,
	}
	for perMinute, want := range cases {
		if got := burstFor(perMinute); got != want {
			t.Errorf("burstFor(%d) = %d; want %d", perMinute, got, want)
		}
	}
}

// TestABurstIsSpentAndThenRefused is FR-037 at its simplest: a caller may ask a
// few times at once, and then it may not.
func TestABurstIsSpentAndThenRefused(t *testing.T) {
	t.Parallel()

	l := testLimiter(t, config.DefaultCreateRatePerMin, newTestClock(testTime))
	burst := burstFor(config.DefaultCreateRatePerMin)

	if allowed := spend(l, auth.CallerOperator, burst); allowed != burst {
		t.Fatalf("%d of the first %d creates were allowed; want all of them", allowed, burst)
	}
	if l.allow(auth.CallerOperator) {
		t.Fatalf("create %d was allowed with no time passed; the burst is %d", burst+1, burst)
	}
}

// TestTokensComeBackAtTheConfiguredRate is the half a sleeping test could never
// state exactly. At six a minute a token is worth ten seconds — so a nanosecond
// short of ten seconds must still refuse, and the ten-second mark must allow
// exactly one more and no second one.
func TestTokensComeBackAtTheConfiguredRate(t *testing.T) {
	t.Parallel()

	clk := newTestClock(testTime)
	l := testLimiter(t, config.DefaultCreateRatePerMin, clk)
	spend(l, auth.CallerOperator, burstFor(config.DefaultCreateRatePerMin))

	const oneToken = time.Minute / config.DefaultCreateRatePerMin

	clk.advance(oneToken - time.Nanosecond)
	if l.allow(auth.CallerOperator) {
		t.Fatalf("a create was allowed a nanosecond before %v had passed", oneToken)
	}

	clk.advance(time.Nanosecond)
	if !l.allow(auth.CallerOperator) {
		t.Fatalf("no create was allowed after exactly %v; the rate is %d a minute",
			oneToken, config.DefaultCreateRatePerMin)
	}
	if l.allow(auth.CallerOperator) {
		t.Fatalf("%v bought two creates; a token is worth one", oneToken)
	}
}

// TestABucketNeverFillsPastItsBurst is what keeps an idle caller from banking a
// day's worth of creates and spending them all at once — which would make the
// rate a limit on the average and not on the burst, and it is the burst that
// spawns processes.
func TestABucketNeverFillsPastItsBurst(t *testing.T) {
	t.Parallel()

	clk := newTestClock(testTime)
	l := testLimiter(t, config.DefaultCreateRatePerMin, clk)
	burst := burstFor(config.DefaultCreateRatePerMin)

	spend(l, auth.CallerOperator, burst)
	clk.advance(24 * time.Hour)

	if allowed := spend(l, auth.CallerOperator, burst); allowed != burst {
		t.Fatalf("%d creates were allowed after a day idle; want the full burst of %d", allowed, burst)
	}
	if l.allow(auth.CallerOperator) {
		t.Fatalf("a day idle bought more than the burst of %d", burst)
	}
}

// TestOneCallersBudgetIsNotAnothers uses the synthetic second identity the
// ownership tests use. There is one caller in milestone 1, so this is written
// against milestone 2's: a per-caller limit that was really a global one would
// let the first identity through the door starve every other.
func TestOneCallersBudgetIsNotAnothers(t *testing.T) {
	t.Parallel()

	const somebodyElse auth.CallerID = "someone-else"

	l := testLimiter(t, config.DefaultCreateRatePerMin, newTestClock(testTime))
	burst := burstFor(config.DefaultCreateRatePerMin)

	spend(l, auth.CallerOperator, burst)
	if l.allow(auth.CallerOperator) {
		t.Fatal("the first caller was allowed past its burst, so this proves nothing about the second")
	}

	if allowed := spend(l, somebodyElse, burst); allowed != burst {
		t.Errorf("the second caller was allowed %d creates; want its own full burst of %d", allowed, burst)
	}
}

// TestABackwardsClockHandsOutNoTokens fails closed on the case a host really
// produces: an NTP correction, or a machine resumed from suspend. Refilling for
// negative time would be a windfall, and moving the mark backwards would be the
// same windfall paid a moment later.
func TestABackwardsClockHandsOutNoTokens(t *testing.T) {
	t.Parallel()

	clk := newTestClock(testTime)
	l := testLimiter(t, config.DefaultCreateRatePerMin, clk)
	spend(l, auth.CallerOperator, burstFor(config.DefaultCreateRatePerMin))

	clk.advance(-time.Hour)
	if l.allow(auth.CallerOperator) {
		t.Fatal("a create was allowed after the clock jumped an hour backwards")
	}

	// Back where it started, plus one token's worth of real time. The recovery is
	// measured from the mark the bucket kept, not from the excursion.
	clk.advance(time.Hour + time.Minute/config.DefaultCreateRatePerMin)
	if !l.allow(auth.CallerOperator) {
		t.Fatal("the caller never recovered after the clock came back; a correction must not cost it its budget")
	}
	if l.allow(auth.CallerOperator) {
		t.Fatal("the backwards excursion was paid out as tokens once the clock returned")
	}
}

// TestConcurrentCreatesSpendOneTokenEach is the reason the check and the spend
// share one critical section. Sixteen requests arriving together must find one
// budget between them, not sixteen copies of the same one — and an extra winner
// here is an extra unsandboxed session.
func TestConcurrentCreatesSpendOneTokenEach(t *testing.T) {
	t.Parallel()

	const askers = 16

	l := testLimiter(t, config.DefaultCreateRatePerMin, newTestClock(testTime))
	burst := burstFor(config.DefaultCreateRatePerMin)

	results := make(chan bool, askers)
	var ready, done sync.WaitGroup
	ready.Add(askers)
	done.Add(askers)
	start := make(chan struct{})

	for i := 0; i < askers; i++ {
		go func() {
			defer done.Done()
			ready.Done()
			<-start
			results <- l.allow(auth.CallerOperator)
		}()
	}

	ready.Wait()
	close(start)
	done.Wait()
	close(results)

	allowed := 0
	for ok := range results {
		if ok {
			allowed++
		}
	}
	if allowed != burst {
		t.Fatalf("%d of %d concurrent creates were allowed; want exactly the burst of %d", allowed, askers, burst)
	}
}

// TestAFullBucketIsForgotten is the bound on what the limiter remembers. A
// caller whose bucket has refilled to the top is in the state a caller it has
// never seen is in, so keeping the entry would be memory held for a decision
// already made — and milestone 2 turns one identity into several.
func TestAFullBucketIsForgotten(t *testing.T) {
	t.Parallel()

	clk := newTestClock(testTime)
	l := testLimiter(t, config.DefaultCreateRatePerMin, clk)

	if !l.allow(auth.CallerOperator) {
		t.Fatal("the first create was refused")
	}
	if n := l.held(); n != 1 {
		t.Fatalf("the limiter holds %d bucket(s) for a caller mid-burst; want 1", n)
	}

	// Long enough for the bucket to have refilled to the top, spent by another
	// caller so that the sweep is not the asking caller's own write.
	clk.advance(time.Hour)
	l.allow("someone-else")

	if n := l.held(); n != 1 {
		t.Fatalf("the limiter holds %d bucket(s); want only the caller that is mid-burst", n)
	}
	if _, kept := l.buckets[auth.CallerOperator]; kept {
		t.Error("a bucket that had refilled to the top was kept; it says nothing an absent one does not")
	}
}

// TestALimiterRefusesARateThatBoundsNothing is the second check on a value
// config.Load already refuses. A Config is a struct, and a zero read as "no
// limit" would be an unbounded create endpoint behind a daemon that started
// cleanly (docs/security.md §4).
func TestALimiterRefusesARateThatBoundsNothing(t *testing.T) {
	t.Parallel()

	for _, perMinute := range []int{0, -1} {
		if l, err := newLimiter[auth.CallerID]("create", perMinute, newTestClock(testTime)); err == nil {
			t.Errorf("newLimiter(%d, _) = %v, nil; want a refusal", perMinute, l)
		}
	}
	if l, err := newLimiter[auth.CallerID]("create", config.DefaultCreateRatePerMin, nil); err == nil {
		t.Errorf("newLimiter(_, nil) = %v, nil; want a refusal — a limiter with no clock never refills", l)
	}

	// The refusal names the budget it is about (M12/T005). There are two now, and
	// a startup error is read by an operator who cannot see which line raised it —
	// so one that said "create" while the sign-in limiter was what failed would
	// send them to a variable that is not the problem.
	_, err := newLimiter[loginSource]("sign-in", 0, newTestClock(testTime))
	if err == nil {
		t.Fatal("newLimiter[loginSource](0, _) built a limiter; want a refusal")
	}
	if !strings.Contains(err.Error(), "sign-in") {
		t.Errorf("the sign-in limiter's refusal is %q; it never names which budget could not be built", err)
	}
}

// held reports how many buckets the limiter is holding, for the sweep's test.
func (l *limiter[K]) held() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.buckets)
}

// rateFixture is an audited server whose create budget is the production default
// and whose limiter clock a test can move.
//
// The limiter is installed after newServer rather than passed to it, which is
// the seam the listener and the failure sink already use. It is also what makes
// the wiring claim honest: the limiter this fixture hands over is the one the
// route has to consult for any of these tests to see a 429 at all.
type rateFixture struct {
	*testServer

	clock *testClock
	burst int
}

func newRateFixture(t *testing.T) rateFixture {
	t.Helper()

	s := newAuditedServer(t)
	clk := newTestClock(testTime)
	s.creates = testLimiter(t, config.DefaultCreateRatePerMin, clk)

	return rateFixture{testServer: s, clock: clk, burst: burstFor(config.DefaultCreateRatePerMin)}
}

// create posts the nth well-formed create of this fixture's run. The signing
// instant varies with n because the signature covers the timestamp and the body
// and nothing else — identical bodies at one instant share a signature, and the
// replay cache would answer the second with a 401.
func (f rateFixture) create(t *testing.T, n int) created {
	t.Helper()
	return postSessionsAt(t, f.testServer, createBody(f.fixture), testTime.Add(-time.Duration(n)*time.Second))
}

// exhaust spends the whole burst through the API, asserting each one landed, and
// returns how many requests have been made.
func (f rateFixture) exhaust(t *testing.T) int {
	t.Helper()

	for i := 0; i < f.burst; i++ {
		if got := f.create(t, i); got.answer.Code != http.StatusCreated {
			t.Fatalf("create %d = %d (%q); want %d", i+1, got.answer.Code, got.answer.Body, http.StatusCreated)
		}
	}
	return f.burst
}

// TestCreatePastTheRateLimitAnswersTooManyRequests is FR-037 as a request: the
// burst lands, the next one is refused with the contract's 429, and the refusal
// costs the host nothing.
func TestCreatePastTheRateLimitAnswersTooManyRequests(t *testing.T) {
	t.Parallel()

	f := newRateFixture(t)
	sent := f.exhaust(t)

	sessions := f.fixture.store.Len()
	before := len(f.fixture.tmux.Calls())
	refused := f.create(t, sent)

	if refused.answer.Code != http.StatusTooManyRequests {
		t.Fatalf("the create past the rate = %d (%q); want %d",
			refused.answer.Code, refused.answer.Body, http.StatusTooManyRequests)
	}
	if body := refused.answer.Body.String(); body != string(bodyTooManyRequests) {
		t.Errorf("body = %q; want %q", body, bodyTooManyRequests)
	}
	if ct := refused.answer.Header().Get(headerContentType); ct != contentTypeJSON {
		t.Errorf("Content-Type = %q; want %q — every response is JSON", ct, contentTypeJSON)
	}
	if extra := f.fixture.tmux.Calls()[before:]; len(extra) != 0 {
		t.Errorf("the refused create ran %v; a rate-limited request must cost no tmux command", extra)
	}
	if n := f.fixture.store.Len(); n != sessions {
		t.Errorf("the store holds %d session(s) after the refusal; want the %d that were already there", n, sessions)
	}

	// The trail carries what the caller is not told: which of the two conditions
	// behind this status it was.
	records := f.records(t)
	if len(records) != sent+1 {
		t.Fatalf("%d requests emitted %d audit records; FR-041 requires exactly one each", sent+1, len(records))
	}
	rec := records[len(records)-1]
	if rec["decision"] != string(audit.Deny) {
		t.Errorf("decision = %v; want %q", rec["decision"], audit.Deny)
	}
	if rec["reason"] != errCreateRateExceeded.Error() {
		t.Errorf("reason = %v; want %q", rec["reason"], errCreateRateExceeded.Error())
	}
	if rec["action"] != string(audit.ActionSessionCreate) {
		t.Errorf("action = %v; want %q — refusing must not rename the operation",
			rec["action"], audit.ActionSessionCreate)
	}
	if id, ok := rec["session_id"]; ok {
		t.Errorf("a refused create recorded a session_id: %v", id)
	}
}

// TestTheBudgetRecoversWithoutARestart is the other half of a rate limit: it is
// a delay, not a ban. The clock the daemon reads is the only thing that changes
// here — no session is destroyed, and nothing is reset by hand.
func TestTheBudgetRecoversWithoutARestart(t *testing.T) {
	t.Parallel()

	f := newRateFixture(t)
	sent := f.exhaust(t)

	if got := f.create(t, sent); got.answer.Code != http.StatusTooManyRequests {
		t.Fatalf("the create past the rate = %d; want %d", got.answer.Code, http.StatusTooManyRequests)
	}
	sent++

	f.clock.advance(time.Minute / config.DefaultCreateRatePerMin)

	if got := f.create(t, sent); got.answer.Code != http.StatusCreated {
		t.Fatalf("the create after a token's worth of time = %d (%q); want %d",
			got.answer.Code, got.answer.Body, http.StatusCreated)
	}
}

// TestTheRateRefusalIsTheSameAnswerAsTheCapRefusal is why both conditions share
// one body. A caller cannot act on the difference — the answer to either is to
// wait — and telling them apart would say how busy the host is to a caller that
// owns none of it. The operator reads which it was in the trail, and this test
// is also what pins that the two reasons stay distinct there.
func TestTheRateRefusalIsTheSameAnswerAsTheCapRefusal(t *testing.T) {
	t.Parallel()

	byRate := newRateFixture(t)
	rateRefused := byRate.create(t, byRate.exhaust(t))

	// The cap fixture's own budget is rateNotUnderTest, so the sixth create here
	// is refused by the cap and by nothing else.
	byCap := newAuditedServer(t)
	var capRefused created
	for i := 0; i <= config.DefaultMaxSessions; i++ {
		capRefused = postSessionsAt(t, byCap, createBody(byCap.fixture), testTime.Add(-time.Duration(i)*time.Second))
	}

	if rateRefused.answer.Code != capRefused.answer.Code {
		t.Fatalf("the rate refusal answered %d and the cap refusal %d; a caller must not be able to tell them apart",
			rateRefused.answer.Code, capRefused.answer.Code)
	}
	if got, want := rateRefused.answer.Body.String(), capRefused.answer.Body.String(); got != want {
		t.Errorf("the rate refusal body %q differs from the cap refusal body %q", got, want)
	}
	if got, want := rateRefused.answer.Header(), capRefused.answer.Header(); !reflect.DeepEqual(got, want) {
		t.Errorf("the rate refusal headers %v differ from the cap refusal headers %v", got, want)
	}

	rateReason, capReason := lastReason(t, byRate.testServer), lastReason(t, byCap)
	if rateReason != errCreateRateExceeded.Error() {
		t.Errorf("the rate refusal was recorded as %q; want %q", rateReason, errCreateRateExceeded.Error())
	}
	if capReason != errCreateCapReached.Error() {
		t.Errorf("the cap refusal was recorded as %q; want %q", capReason, errCreateCapReached.Error())
	}
	if rateReason == capReason {
		t.Error("both refusals were recorded under one reason; the trail is where the difference is kept")
	}
}

// lastReason is the account the trail kept of a fixture's most recent request.
func lastReason(t *testing.T, s *testServer) string {
	t.Helper()

	records := s.records(t)
	if len(records) == 0 {
		t.Fatal("the server emitted no audit records, so there is no refusal to read")
	}

	last := records[len(records)-1]
	reason, ok := last["reason"].(string)
	if !ok {
		t.Fatalf("the last record carries no reason: %v", last)
	}
	return reason
}

// TestOnlyTheCreateRouteSpendsTheBudget keeps FR-037 to the operation it names.
// A caller that has run out of creates must still be able to see, drive, and —
// most of all — destroy the sessions it already has: a rate limit that locked a
// caller out of DELETE would leave live unsandboxed shells running because
// somebody asked for one session too many.
func TestOnlyTheCreateRouteSpendsTheBudget(t *testing.T) {
	t.Parallel()

	f := newRateFixture(t)
	sent := f.exhaust(t)
	if got := f.create(t, sent); got.answer.Code != http.StatusTooManyRequests {
		t.Fatalf("the budget was not exhausted: create = %d; want %d", got.answer.Code, http.StatusTooManyRequests)
	}
	sent++

	for _, route := range f.Routes() {
		if rateLimited[route] {
			continue
		}

		sent++
		rec := httptest.NewRecorder()
		f.ServeHTTP(rec, requestFor(t, f.testServer, route, testTime.Add(-time.Duration(sent)*time.Second)))

		if rec.Code != reachedStatus[route] {
			t.Errorf("%s = %d (%q) with the create budget spent; want %d — only creation is rate limited",
				route, rec.Code, rec.Body, reachedStatus[route])
		}
	}
}

// TestAnUnauthenticatedFloodSpendsNoBudget is why the limiter sits behind layer
// 2 rather than in front of it. Anyone can open a socket to a tunnel; if
// unsigned requests spent the operator's creates, a stranger who cannot
// authenticate at all could still keep the daemon from ever starting a session.
func TestAnUnauthenticatedFloodSpendsNoBudget(t *testing.T) {
	t.Parallel()

	f := newRateFixture(t)

	for i := 0; i < 10*f.burst; i++ {
		rec := httptest.NewRecorder()
		f.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/sessions", strings.NewReader(`{"name":"probe"}`)))
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("the unsigned create = %d; want %d", rec.Code, http.StatusUnauthorized)
		}
	}

	f.exhaust(t)
}

// TestTheRateRefusalDisclosesNothingAboutTheBudget matches the cap's own
// disclosure test. The 429 says to try later and nothing else: a body or a
// header naming the rate, the burst, or how long to wait would tell a caller
// holding a stolen secret exactly how fast it may keep going unnoticed.
func TestTheRateRefusalDisclosesNothingAboutTheBudget(t *testing.T) {
	t.Parallel()

	f := newRateFixture(t)
	refused := f.create(t, f.exhaust(t))
	outward := refused.answer.Body.String() + " " + fmt.Sprint(refused.answer.Header())

	for _, disclosed := range []string{
		fmt.Sprint(config.DefaultCreateRatePerMin),
		fmt.Sprint(f.burst),
		"Retry-After",
	} {
		if strings.Contains(outward, disclosed) {
			t.Errorf("the refusal disclosed %q: %q", disclosed, outward)
		}
	}
}

// TestNewBuildsTheLimiterFromTheConfiguredRate is the production wiring, which
// no fixture asserts: every other test in this file installs a limiter of its
// own, so without this one the daemon could ship reading the wrong field, or no
// field at all.
func TestNewBuildsTheLimiterFromTheConfiguredRate(t *testing.T) {
	t.Parallel()

	cfg := testConfig(loopbackListen)
	cfg.CreateRatePerMin = config.DefaultCreateRatePerMin

	s, err := New(cfg)
	if err != nil {
		t.Fatalf("New = _, %v; want a server", err)
	}
	if s.creates == nil {
		t.Fatal("New built a server with no create limiter; the create route would be unbounded")
	}
	if s.creates.perMinute != cfg.CreateRatePerMin {
		t.Errorf("the limiter runs at %d a minute; want the configured %d", s.creates.perMinute, cfg.CreateRatePerMin)
	}
	if want := float64(burstFor(cfg.CreateRatePerMin)); s.creates.burst != want {
		t.Errorf("the limiter bursts to %v; want %v", s.creates.burst, want)
	}
	if _, ok := s.creates.clock.(systemClock); !ok {
		t.Errorf("the limiter runs on a %T; the daemon must use the host clock", s.creates.clock)
	}
}
