// Internal test, matching the rest of the package. Every claim here is about a
// response that outlives the deadline every other response in this daemon runs
// under, so most of them need a real listener: what cuts an ordinary response
// off is net/http setting a write deadline on the connection, and a connection
// is exactly what an httptest.ResponseRecorder does not have.
//
// That is not only a fixture inconvenience. A recorder cannot lift a write
// deadline either, so the stream route answers a recorder-driven request with a
// 500 — see TestAStreamThatCannotLiftItsWriteDeadlineIsNotServed, which is the
// fail-closed behaviour and also the reason every test of an *open* stream in
// this package has to bind a socket.
package httpapi

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nctiggy/claude-remote-session-webhook/internal/audit"
	"github.com/nctiggy/claude-remote-session-webhook/internal/auth"
	"github.com/nctiggy/claude-remote-session-webhook/internal/config"
	"github.com/nctiggy/claude-remote-session-webhook/internal/session"
	"github.com/nctiggy/claude-remote-session-webhook/internal/tmuxctl"
)

const (
	// writeDeadlineUnderTest stands in for the server's 30-second WriteTimeout
	// and tickUnderTest for the stream's one-second cadence. Both are the
	// production values divided by something a test can afford to wait for: what
	// is under test is whether the deadline applies to this response *at all*,
	// which is the same question at 200ms as at 30s, and waiting out the real one
	// would cost more than the rest of the suite together.
	writeDeadlineUnderTest = 200 * time.Millisecond
	tickUnderTest          = 10 * time.Millisecond

	// heartbeatsPastTheDeadline is how many writes must land after the deadline
	// has passed before the claim is made. One could be a write already in flight
	// when it fired; three is a response that is still being written to a
	// connection net/http would have closed.
	heartbeatsPastTheDeadline = 3

	// streamTestBudget bounds every exchange here, so a stream that never
	// delivers fails the test rather than hanging until the package times out.
	streamTestBudget = 10 * time.Second

	// ticksWatched is how many line groups a suppression claim reads before it is
	// made. It is well past the two a change can take to be noticed — the tick
	// that finds the buffer still inside its window, and the one that reads the
	// session — so a stream that resent a screen it had already sent has several
	// chances to do it.
	ticksWatched = 10
)

// The two things a tick can write, as they arrive on the wire. Derived from the
// constants rather than spelled again here, so a change to either moves these
// with it, and split at the first newline because readGroup reads a group's
// first line and its terminator apart.
var (
	heartbeatLine = firstLine(heartbeat)
	screenLine    = firstLine(screenChanged)
)

func firstLine(group []byte) string {
	line, _, _ := strings.Cut(string(group), "\n")
	return line + "\n"
}

// watching is a fleet serving on a real loopback socket, with the write deadline
// and the tick shortened, and its address.
func watching(t *testing.T) (*fleet, string) {
	t.Helper()
	return watchingWithCap(t, config.DefaultMaxStreams)
}

// watchingWithCap is watching with the concurrency cap chosen, so that a test of
// the cap can reach it with one connection instead of ten.
//
// The cap is replaced rather than configured, for the reason the tick and the
// write deadline are: what is under test is the behaviour at the boundary, which
// is the same behaviour at one as at ten, and a fixture that had to open the
// production default would spend nine sockets saying nothing.
func watchingWithCap(t *testing.T, limit int) (*fleet, string) {
	t.Helper()

	f := watchingUnserved(t, limit)
	return f, serve(t, f)
}

// watchingUnserved is the fleet the two above build, before anything is bound to
// a socket.
//
// It is a seam rather than a convenience. Every field on a Server is read-only
// once it is serving, and a stream's ticks run on a goroutine of net/http's for
// as long as the connection lives — so a test that must change something the
// handler reads (the failure reporter, below) has to change it before Serve, or
// it is writing a field another goroutine is reading.
func watchingUnserved(t *testing.T, limit int) *fleet {
	t.Helper()

	f := newFleet(t)
	f.http.WriteTimeout = writeDeadlineUnderTest
	f.streamTick = tickUnderTest
	f.streams = mustStreamCap(t, limit)
	return f
}

// serve binds a prepared fleet and hands back its address.
func serve(t *testing.T, f *fleet) string {
	t.Helper()

	if err := f.Listen(); err != nil {
		t.Fatalf("Listen() = %v; want a bound listener", err)
	}
	served := make(chan error, 1)
	go func() { served <- f.Serve() }()
	t.Cleanup(func() {
		if err := f.Close(); err != nil {
			t.Errorf("Close() = %v", err)
		}
		if err := <-served; err != nil {
			t.Errorf("Serve() = %v after a deliberate Close; want nil", err)
		}
	})

	return f.Addr().String()
}

// mustStreamCap builds a cap of the given size, since a limit is only a limit if
// the constructor accepted it.
func mustStreamCap(t *testing.T, limit int) *streamCap {
	t.Helper()

	c, err := newStreamCap(limit)
	if err != nil {
		t.Fatalf("newStreamCap(%d) = _, %v; want a cap", limit, err)
	}
	return c
}

// watch opens one session's stream as the verified operator would: the identity
// assertion in a header, no signature, and no credential anywhere in the URL.
func (f *fleet) watch(t *testing.T, addr, id string) *http.Response {
	t.Helper()
	return f.watchFrom(t, addr, id, absent)
}

// watchFrom is watch with the browser's own account of where the request came
// from, which watch sends nothing for — a client that is not a browser sends no
// Sec-Fetch-Site at all, and that case is the one every other test here drives.
//
// The response is returned unread and unjudged: some callers here expect a
// stream and one expects a refusal, and a helper that insisted on 200 could not
// fetch the second. Closing it twice is harmless, so a caller may end a stream
// early and leave the cleanup below to be a no-op.
func (f *fleet) watchFrom(t *testing.T, addr, id, site string) *http.Response {
	t.Helper()

	target := "http://" + addr + "/sessions/" + id + "/stream"
	r, err := http.NewRequestWithContext(context.Background(), http.MethodGet, target, nil)
	if err != nil {
		t.Fatalf("build the stream request: %v", err)
	}
	r.Header.Set(headerAccessAssertion, f.keys.mint(t, f.keys.claims()))
	if site != absent {
		r.Header.Set(headerSecFetchSite, site)
	}

	resp, err := (&http.Client{Timeout: streamTestBudget}).Do(r)
	if err != nil {
		t.Fatalf("GET %s: %v", target, err)
	}
	t.Cleanup(func() {
		if err := resp.Body.Close(); err != nil {
			t.Errorf("close the stream: %v", err)
		}
	})
	return resp
}

// askToWatch drives one stream open through a recorder, carrying the credential
// and the fetch-metadata header the caller chose.
//
// A recorder is enough for every *refusal* in the open sequence, because all
// four are decided before the response is touched — which is the same fact
// TestAStreamThatCannotLiftItsWriteDeadlineIsNotServed pins from the other side:
// an open that gets past all four answers a recorder with a 500, since a
// recorder cannot lift a write deadline. So a 500 here means the sequence
// admitted the request, and nothing else does.
func (f *fleet) askToWatch(t *testing.T, id, assertion, site string) *httptest.ResponseRecorder {
	t.Helper()

	r := httptest.NewRequest(http.MethodGet, "/sessions/"+id+"/stream", nil)
	if assertion != absent {
		r.Header.Set(headerAccessAssertion, assertion)
	}
	if site != absent {
		r.Header.Set(headerSecFetchSite, site)
	}

	w := httptest.NewRecorder()
	f.ServeHTTP(w, r)
	return w
}

// readGroup reads one whole SSE line group off the wire — its first line and the
// blank line that ends it — and hands back the first line, failing the test if
// the stream stopped instead.
//
// It reads a *group* rather than a line because that is what keeps a count
// honest: a caller asking for three heartbeats must not be handed one comment
// and its own terminator. Every group this transport writes is two lines, since
// the two things a tick can write are a comment and a one-field event
// (contracts/stream.md).
func readGroup(t *testing.T, body *bufio.Reader, opened time.Time) string {
	t.Helper()

	first := readStreamLine(t, body, opened)
	if line := readStreamLine(t, body, opened); line != "\n" {
		t.Fatalf("%q was followed by %q; an SSE line group ends with a blank line", first, line)
	}
	return first
}

// readHeartbeat reads one group and insists it was the suppressed-tick comment.
//
// It asserts the wire format rather than the constant behind it: a comment is a
// line beginning `:`, and a stream that wrote an event here wrote one for a
// screen that did not change.
func readHeartbeat(t *testing.T, body *bufio.Reader, opened time.Time) {
	t.Helper()

	if line := readGroup(t, body, opened); line != heartbeatLine {
		t.Fatalf("the stream wrote %q; want the heartbeat a tick writes when the screen has not changed", line)
	}
}

// readScreen reads one group and insists it was the changed-screen event.
//
// The payload it expects is the placeholder rather than a screen, and that is
// the assertion rather than a concession: T024 decides *when* an event is
// written and T025 decides what it carries, so a test that accepted any `data:`
// line would go on passing through the change that puts an unframed screen on a
// line-oriented wire.
func readScreen(t *testing.T, body *bufio.Reader, opened time.Time) {
	t.Helper()

	if line := readGroup(t, body, opened); line != screenLine {
		t.Fatalf("the stream wrote %q; want %q", line, screenLine)
	}
}

// awaitScreen reads groups until the changed-screen event arrives, and reports
// how many heartbeats preceded it.
//
// A change is not expected on the very next group: the tick after a screen
// changed may find the shared buffer still inside its interval, in which case
// the reading that notices is one tick later. What is bounded is how long that
// may take, which is what ticksWatched is.
func awaitScreen(t *testing.T, body *bufio.Reader, opened time.Time) int {
	t.Helper()

	for quiet := 0; quiet < ticksWatched; quiet++ {
		switch line := readGroup(t, body, opened); line {
		case screenLine:
			return quiet
		case heartbeatLine:
		default:
			t.Fatalf("the stream wrote %q; want either the heartbeat or the changed-screen event", line)
		}
	}
	t.Fatalf("a screen that changed went unsent for %d ticks", ticksWatched)
	return 0
}

func readStreamLine(t *testing.T, body *bufio.Reader, opened time.Time) string {
	t.Helper()

	line, err := body.ReadString('\n')
	if err != nil {
		t.Fatalf("the stream stopped %v after it opened, which is around the %v write deadline this server carries: %v",
			time.Since(opened).Round(time.Millisecond), writeDeadlineUnderTest, err)
	}
	return line
}

// TestTheStreamOutlivesTheWriteDeadlineTheOtherRoutesKeep is T022's whole claim
// (research D3, FR-034): a response that is deliberately without an end, served
// by a server whose every other response is cut off after WriteTimeout.
//
// The deadline is lifted per response rather than by zeroing WriteTimeout, so
// the six routes milestone 1 shipped keep theirs — TestServerTimeoutsAreSet is
// where that half is pinned, and it fails on a non-positive value as well as on
// a changed one. TestAWriteDeadlineThisShortReallyCutsAResponseOff is the other
// half of this one: without it, a server that had simply lost its deadline
// entirely would pass here.
func TestTheStreamOutlivesTheWriteDeadlineTheOtherRoutesKeep(t *testing.T) {
	t.Parallel()

	f, addr := watching(t)
	live, _ := f.fixture.plant(t, session.Session{Name: "watch me", WorkDir: f.fixture.repo})

	opened := time.Now()
	resp := f.watch(t, addr, live.ID) //nolint:bodyclose // watch closes it in t.Cleanup, which the linter cannot see through.

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /sessions/%s/stream = %d; want %d", live.ID, resp.StatusCode, http.StatusOK)
	}
	if got := resp.Header.Get(headerContentType); got != contentTypeEventStream {
		t.Errorf("the stream declares itself as %q; want %q", got, contentTypeEventStream)
	}
	// Pane content is secret under docs/security.md §3, and the stream is not the
	// asset exemption: a cached copy of a session's screen outlives the screen.
	if got := resp.Header.Get(headerCacheControl); got != cacheControlNoStore {
		t.Errorf("the stream answered with %s: %q; want %q", headerCacheControl, got, cacheControlNoStore)
	}
	// The URL carries the session ID and no credential (FR-034). A stream
	// authorised by something in its address would put the key to an unsandboxed
	// shell in every intermediary's logs.
	if q := resp.Request.URL.RawQuery; q != "" {
		t.Errorf("the stream's URL carries a query string %q; the address may hold the session ID and nothing else", q)
	}

	// The assertion itself: keep reading until enough writes have landed after
	// the instant net/http would have closed this connection. Every read past
	// that instant is one the unlifted deadline would have turned into an error.
	body := bufio.NewReader(resp.Body)
	readScreen(t, body, opened) // the opening screen, which precedes the first tick
	deadline := opened.Add(writeDeadlineUnderTest)
	for late := 0; late < heartbeatsPastTheDeadline; {
		readHeartbeat(t, body, opened)
		if time.Now().After(deadline) {
			late++
		}
	}
}

// TestAWriteDeadlineThisShortReallyCutsAResponseOff is the control the test
// above needs to mean anything.
//
// It is the same server configuration and the same shape of response — write,
// flush, wait past the deadline, write again — differing by the one line the
// stream handler runs and this handler does not. Without it, "the stream
// survived 200ms" would be equally true of a daemon that had lost its write
// deadline altogether, which is precisely the mistake research D3 rejected.
func TestAWriteDeadlineThisShortReallyCutsAResponseOff(t *testing.T) {
	t.Parallel()

	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		rc := http.NewResponseController(w) //nolint:bodyclose // false positive: a ResponseController is not a response and has no body to close.
		if _, err := w.Write([]byte(":\n\n")); err != nil {
			return
		}
		if err := rc.Flush(); err != nil {
			return
		}
		time.Sleep(2 * writeDeadlineUnderTest)
		if _, err := w.Write([]byte("late\n")); err != nil {
			return
		}
		_ = rc.Flush() //nolint:errcheck // the failure is the subject: what this test reads is what reached the client.
	}))
	srv.Config.WriteTimeout = writeDeadlineUnderTest
	srv.Start()
	t.Cleanup(srv.Close)

	opened := time.Now()
	resp, err := (&http.Client{Timeout: streamTestBudget}).Get(srv.URL)
	if err != nil {
		t.Fatalf("GET %s: %v", srv.URL, err)
	}
	t.Cleanup(func() {
		if err := resp.Body.Close(); err != nil {
			t.Errorf("close the response: %v", err)
		}
	})

	body := bufio.NewReader(resp.Body)
	readHeartbeat(t, body, opened)

	line, err := body.ReadString('\n')
	if err == nil {
		t.Fatalf("a response that kept its write deadline delivered %q %v after it opened; then the deadline in this fixture bounds nothing and the test above proves nothing",
			line, time.Since(opened).Round(time.Millisecond))
	}
}

// TestAStreamOfASessionTheViewerCannotHaveIsTheUniformNotFound is FR-037b and
// SC-016 on this route: an id that never existed, one held by a second owner,
// and a path this daemon has no route for at all are one answer, byte for byte.
//
// The difference between "not yours" and "does not exist" is what enumeration is
// made of, and a stream is the most valuable thing on this door to enumerate —
// it is a live view of a shell. Nothing about the response happens before both
// questions are answered, so a refused caller cannot even tell from the headers
// that they asked for a stream.
//
// Non-vacuity is TestTheStreamOutlivesTheWriteDeadlineTheOtherRoutesKeep, where
// this same route opens a stream for a session the viewer does own.
func TestAStreamOfASessionTheViewerCannotHaveIsTheUniformNotFound(t *testing.T) {
	t.Parallel()

	const stranger auth.CallerID = "a-second-operator"

	f := newFleet(t)
	theirs, _ := f.fixture.plant(t, session.Session{Owner: stranger, Name: "not yours", WorkDir: f.fixture.repo})
	unknown := strings.Repeat("d", session.IDLen)

	notMine := f.open(t, "/sessions/"+theirs.ID+"/stream")
	neverExisted := f.open(t, "/sessions/"+unknown+"/stream")
	noRoute := f.open(t, "/not-a-route")

	if notMine.Code != http.StatusNotFound {
		t.Errorf("streaming another owner's session answers %d; want %d — it is not this viewer's to know about",
			notMine.Code, http.StatusNotFound)
	}
	for name, other := range map[string]*httptest.ResponseRecorder{
		"one that never existed": neverExisted,
		"a path nothing claims":  noRoute,
	} {
		if notMine.Code != other.Code {
			t.Errorf("another owner's session answers %d and %s answers %d; the difference is what enumeration is made of",
				notMine.Code, name, other.Code)
		}
		if notMine.Body.String() != other.Body.String() {
			t.Errorf("another owner's session and %s answer differently:\ngot:\n%s\nwant:\n%s",
				name, notMine.Body.String(), other.Body.String())
		}
	}
	if got := notMine.Header().Get(headerContentType); got == contentTypeEventStream {
		t.Errorf("a refused stream declared itself as %q, which says the id resolved to something", got)
	}
	if strings.Contains(notMine.Body.String(), theirs.Name) {
		t.Errorf("the answer for another owner's session names it:\n%s", notMine.Body.String())
	}
}

// TestAStreamThatCannotLiftItsWriteDeadlineIsNotServed pins the fail-closed half
// of opening a stream, and it is reachable: any wrapper around the response that
// does not implement Unwrap costs the handler its deadline control, and every
// httptest.ResponseRecorder is one.
//
// Serving anyway would produce the worst failure this route has — a stream that
// works, is watched, and stops after thirty seconds with no event and no error,
// which an operator reads as a session that went quiet.
func TestAStreamThatCannotLiftItsWriteDeadlineIsNotServed(t *testing.T) {
	t.Parallel()

	f := newFleet(t)
	live, _ := f.fixture.plant(t, session.Session{Name: "watch me", WorkDir: f.fixture.repo})

	// The request carries a deadline of its own, and nothing here waits for it:
	// the open is expected to refuse long before the loop behind it is reached.
	// It is here so that a change admitting a writer with no deadline control
	// fails this test instead of hanging it — a stream held open by a background
	// context writes heartbeats into a recorder for as long as the binary lives.
	ctx, cancel := context.WithTimeout(context.Background(), streamTestBudget/10)
	defer cancel()

	r := httptest.NewRequest(http.MethodGet, "/sessions/"+live.ID+"/stream", nil).WithContext(ctx)
	r.Header.Set(headerAccessAssertion, f.keys.mint(t, f.keys.claims()))
	w := httptest.NewRecorder()
	f.ServeHTTP(w, r)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("a stream that could not lift its write deadline answered %d; want %d",
			w.Code, http.StatusInternalServerError)
	}
	if body := w.Body.String(); body != "" {
		t.Errorf("the refusal carried %q; a response that never started must not describe why", body)
	}
	if len(f.failed) != 1 {
		t.Errorf("the failure was reported %d times; want exactly 1 — an operator has to be able to find it", len(f.failed))
	}

	// The trail's own half: one record, under the stream's own action, saying the
	// request was not served.
	records := f.records(t)
	if len(records) != 1 {
		t.Fatalf("one request emitted %d audit records (%v); want exactly one", len(records), records)
	}
	if got, want := records[0]["action"], string(audit.ActionStreamOpen); got != want {
		t.Errorf("action = %v; want %v — a stream is not a page, and an operator counting who read a session's output must not be counting page loads too", got, want)
	}
	if got, want := records[0]["decision"], string(audit.Deny); got != want {
		t.Errorf("decision = %v; want %v", got, want)
	}
	if got, want := records[0]["session_id"], live.ID; got != want {
		t.Errorf("session_id = %v; want %v — the id off the daemon's own record", got, want)
	}
}

// --- T023: the ordered open sequence ----------------------------------------
//
// Four checks in one order, from contracts/stream.md: identity, the cross-site
// refusal, ownership, then capacity. Each test below is one step's refusal, and
// two of them are about the *order* rather than about a check — which is the
// half a per-check test cannot state, and the half that decides what a caller
// who was going to be refused anyway gets to learn about this host.

// TestAValidServiceTokenCannotOpenAStream is step one, on the route it matters
// most for (FR-013c).
//
// The assertion is genuine: this fleet's own key server signed it, it is inside
// its validity, and it names the audience the daemon is pinned to. What it does
// not carry is an email, because a service token is the API client's credential
// at the edge and not a person — and "no email" must never read as "allow", or
// every call the operator makes through the API is admitted to a live view of
// their own shells.
func TestAValidServiceTokenCannotOpenAStream(t *testing.T) {
	t.Parallel()

	f := newFleet(t)
	live, _ := f.fixture.plant(t, session.Session{Name: "watch me", WorkDir: f.fixture.repo})

	w := f.askToWatch(t, live.ID, f.keys.mint(t, f.keys.serviceTokenClaims()), absent)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("a service token opened a stream with %d; want %d:\n%s", w.Code, http.StatusUnauthorized, w.Body.String())
	}
	body := w.Body.String()
	if body != string(bodyBrowserRefused) {
		t.Errorf("the refusal answered %q; want the browser door's one refusal", body)
	}
	if strings.Contains(body, live.ID) || strings.Contains(body, live.Name) {
		t.Errorf("the refusal names the session it was refused a view of:\n%s", body)
	}

	rec := f.only(t)
	if got, want := rec["action"], string(audit.ActionAccessReject); got != want {
		t.Errorf("action = %v; want %v — layer 1 refused this, and the two doors' refusals are counted apart", got, want)
	}
	if got, want := rec["decision"], string(audit.Deny); got != want {
		t.Errorf("decision = %v; want %v", got, want)
	}
}

// TestTheStreamRefusesAnOpenTheBrowserCallsCrossSite is step two (FR-034d,
// research D8).
//
// The credential is impeccable in every row: the operator's own assertion, on
// their own session. The only thing wrong is where the browser says the request
// came from — which is the shape of the attack this check exists for, since the
// layer-1 credential is an ambient cookie that rides on a request some other
// page triggered and the edge turns into a valid assertion.
//
// The admitted values are asserted elsewhere rather than here: an absent header
// is every other test in this file, and same-origin is
// TestAStreamOpenedFromTheDashboardItselfIsServed. What this table needs from
// them is non-vacuity, which is also why the last two rows are spellings no
// browser sends — a value this daemon cannot place refuses, because "not
// same-origin" is the fail-closed reading of one.
func TestTheStreamRefusesAnOpenTheBrowserCallsCrossSite(t *testing.T) {
	t.Parallel()

	for name, site := range map[string]string{
		"a request from another site":                    "cross-site",
		"a request from another origin on the same site": "same-site",
		"a request no page made at all":                  "none",
		"a spelling the Fetch standard does not define":  "Same-Origin",
		"a value that is not one of the four":            "definitely-same-origin",
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			f := newFleet(t)
			live, _ := f.fixture.plant(t, session.Session{Name: "watch me", WorkDir: f.fixture.repo})

			w := f.askToWatch(t, live.ID, f.keys.mint(t, f.keys.claims()), site)

			if w.Code != http.StatusUnauthorized {
				t.Fatalf("%s opened a stream with %d; want %d:\n%s", name, w.Code, http.StatusUnauthorized, w.Body.String())
			}
			if body := w.Body.String(); body != string(bodyBrowserRefused) {
				t.Errorf("%s was answered %q; want the browser door's one refusal — a second shape here is a shape that varies with the request", name, body)
			}

			rec := f.only(t)
			if got, want := rec["reason"], errStreamCrossSite.Error(); got != want {
				t.Errorf("reason = %v; want %v", got, want)
			}
			if reason := fmt.Sprint(rec["reason"]); strings.Contains(reason, site) {
				t.Errorf("the trail records the header the caller sent: %q", reason)
			}
		})
	}
}

// TestTheCrossSiteRefusalHappensBeforeAnythingIsLookedUp is the *order* half of
// step two, and the reason the check sits where it does rather than beside the
// ownership call.
//
// Same daemon, same id, one header apart: without it the id is looked up and
// answered with the uniform 404, and with it nothing is looked up at all. A
// cross-site request that reached the store would be a hostile page able to ask
// this host which session ids exist on it, one 404 at a time — and it would get
// its answer through the operator's own riding cookie.
func TestTheCrossSiteRefusalHappensBeforeAnythingIsLookedUp(t *testing.T) {
	t.Parallel()

	f := newFleet(t)
	unknown := strings.Repeat("d", session.IDLen)
	assertion := f.keys.mint(t, f.keys.claims())

	sameSite := f.askToWatch(t, unknown, assertion, absent)
	crossSite := f.askToWatch(t, unknown, assertion, "cross-site")

	if sameSite.Code != http.StatusNotFound {
		t.Fatalf("an id nothing claims answered %d; want %d — then this test compares nothing", sameSite.Code, http.StatusNotFound)
	}
	if crossSite.Code != http.StatusUnauthorized {
		t.Errorf("a cross-site open of the same id answered %d; want %d — the refusal must precede the lookup", crossSite.Code, http.StatusUnauthorized)
	}

	records := f.records(t)
	if len(records) != 2 {
		t.Fatalf("two requests emitted %d audit records (%v); want one each", len(records), records)
	}
	if got, want := records[1]["reason"], errStreamCrossSite.Error(); got != want {
		t.Errorf("the cross-site open was refused as %v; want %v — a request refused by the lookup is a request the lookup ran for", got, want)
	}
}

// TestAStreamOpenedFromTheDashboardItselfIsServed is the admitted half of step
// two, and the whole reason the check is spelled "present and wrong refuses"
// rather than "present and right admits".
//
// It needs a socket for the reason every admitted open here does: a recorder
// cannot lift a write deadline, so a served stream is the one case a recorder
// cannot show.
func TestAStreamOpenedFromTheDashboardItselfIsServed(t *testing.T) {
	t.Parallel()

	f, addr := watching(t)
	live, _ := f.fixture.plant(t, session.Session{Name: "watch me", WorkDir: f.fixture.repo})

	opened := time.Now()
	resp := f.watchFrom(t, addr, live.ID, secFetchSiteSameOrigin) //nolint:bodyclose // watchFrom closes it in t.Cleanup, which the linter cannot see through.

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("a same-origin open answered %d; want %d", resp.StatusCode, http.StatusOK)
	}
	body := bufio.NewReader(resp.Body)
	readScreen(t, body, opened)
	readHeartbeat(t, body, opened)
}

// TestTheStreamCapIsAskedLastOfAll is step four, and it is a test of the order
// rather than of the cap: with the daemon's only slot taken, every caller an
// earlier step would refuse is still refused by that step.
//
// This is the whole reason capacity is last. A cap checked first answers a
// stranger, a hostile page, and a viewer asking about somebody else's session
// with the one fact the three checks above exist to withhold — whether anything
// is being watched on this host, and how much.
//
// The slot is taken here rather than by opening a stream, because what has to be
// full is the count and nothing else; a fixture holding a live socket open to
// say so would be testing the transport again.
func TestTheStreamCapIsAskedLastOfAll(t *testing.T) {
	t.Parallel()

	const stranger auth.CallerID = "a-second-operator"

	f := newFleet(t)
	f.streams = mustStreamCap(t, 1)
	release, admitted := f.streams.admit()
	if !admitted {
		t.Fatal("a fresh cap of one refused the first stream; want it admitted")
	}

	mine, _ := f.fixture.plant(t, session.Session{Name: "watch me", WorkDir: f.fixture.repo})
	theirs, _ := f.fixture.plant(t, session.Session{Owner: stranger, Name: "not yours", WorkDir: f.fixture.repo})
	unknown := strings.Repeat("d", session.IDLen)
	assertion := f.keys.mint(t, f.keys.claims())

	// The caller who really is only short of room, and the record that says so.
	full := f.askToWatch(t, mine.ID, assertion, absent)
	if full.Code != http.StatusTooManyRequests {
		t.Fatalf("an open past the cap answered %d; want %d — then nothing below is about a full cap", full.Code, http.StatusTooManyRequests)
	}
	if body := full.Body.String(); body != "" {
		t.Errorf("the refusal carried %q; a stream that was never admitted is a response that never started", body)
	}
	rec := f.only(t)
	if got, want := rec["action"], string(audit.ActionStreamOpen); got != want {
		t.Errorf("action = %v; want %v", got, want)
	}
	if got, want := rec["reason"], errStreamCapReached.Error(); got != want {
		t.Errorf("reason = %v; want %v", got, want)
	}
	if got, want := rec["session_id"], mine.ID; got != want {
		t.Errorf("session_id = %v; want %v — a refusal for want of room still says which session went unwatched", got, want)
	}

	// And everyone an earlier step answers, answered by that step.
	for name, refused := range map[string]struct {
		id, assertion, site string
		want                int
	}{
		"a caller layer 1 cannot verify": {id: mine.ID, assertion: absent, site: absent, want: http.StatusUnauthorized},
		"an open from another site":      {id: mine.ID, assertion: assertion, site: "cross-site", want: http.StatusUnauthorized},
		"another owner's session":        {id: theirs.ID, assertion: assertion, site: absent, want: http.StatusNotFound},
		"an id nothing claims":           {id: unknown, assertion: assertion, site: absent, want: http.StatusNotFound},
	} {
		w := f.askToWatch(t, refused.id, refused.assertion, refused.site)
		if w.Code != refused.want {
			t.Errorf("with the cap full, %s answered %d; want %d — the cap must not answer for a check that comes before it",
				name, w.Code, refused.want)
		}
	}

	// Non-vacuity: the same open once the slot comes back. A recorder cannot lift
	// a write deadline, so reaching the 500 is what "the sequence admitted it"
	// looks like here (TestAStreamThatCannotLiftItsWriteDeadlineIsNotServed).
	release()
	if again := f.askToWatch(t, mine.ID, assertion, absent); again.Code != http.StatusInternalServerError {
		t.Errorf("with the slot back, the same open answered %d; want %d — the cap was the only thing that had refused it",
			again.Code, http.StatusInternalServerError)
	}
}

// TestAStreamThatFailedToOpenGivesItsSlotBack is the release path with no stream
// behind it: an open admitted by all four checks that then fails on the deadline
// it cannot lift.
//
// A slot leaked here would be a cap that shrinks by one every time a response
// could not be turned into a stream, until a daemon nobody is watching refuses
// everybody. The second ask is the whole test — it can only be answered at all
// if the first gave its slot back.
func TestAStreamThatFailedToOpenGivesItsSlotBack(t *testing.T) {
	t.Parallel()

	f := newFleet(t)
	f.streams = mustStreamCap(t, 1)
	live, _ := f.fixture.plant(t, session.Session{Name: "watch me", WorkDir: f.fixture.repo})
	assertion := f.keys.mint(t, f.keys.claims())

	for attempt := 1; attempt <= 2; attempt++ {
		w := f.askToWatch(t, live.ID, assertion, absent)
		if w.Code != http.StatusInternalServerError {
			t.Fatalf("open %d of 2 answered %d; want %d — a slot the failed open kept is a cap that shrinks as the daemon runs",
				attempt, w.Code, http.StatusInternalServerError)
		}
	}
}

// TestTheStreamCapAdmitsExactlyItsLimitWhenOpensRace is FR-034e's "counted and
// admitted in one critical section", which a sequential test cannot claim.
//
// Every caller in a round is released at once and all of them ask for the same
// few slots. Split into a count and then a take, they would read the same
// number, find room, and be admitted together — the race Store.AddCapped closes
// for sessions, closed here the same way.
//
// It is many rounds rather than one because the window a split would open is a
// few instructions wide, and one round of it lands only under the scheduling
// -race happens to produce. A suite that catches this defect only when somebody
// remembers -race is a suite that does not catch it: the rounds are what make a
// plain `go test ./...` fail on it, and they cost milliseconds.
func TestTheStreamCapAdmitsExactlyItsLimitWhenOpensRace(t *testing.T) {
	t.Parallel()

	const (
		limit   = 4
		callers = 64
		rounds  = 1000
	)

	c := mustStreamCap(t, limit)
	for round := range rounds {
		var (
			start    sync.WaitGroup
			done     sync.WaitGroup
			admitted atomic.Int64
		)
		releases := make(chan func(), callers)
		start.Add(1)
		for range callers {
			done.Add(1)
			go func() {
				defer done.Done()
				start.Wait()
				if release, ok := c.admit(); ok {
					admitted.Add(1)
					releases <- release
				}
			}()
		}
		start.Done()
		done.Wait()
		close(releases)

		if got := admitted.Load(); got != limit {
			t.Fatalf("round %d: %d callers racing a cap of %d were admitted %d times; want exactly %d",
				round, callers, limit, got, limit)
		}

		// And the slots really come back for the next round — released twice
		// each, because a release that ran twice would free a slot still held,
		// which is an admission over the cap and the one arithmetic mistake this
		// type may not make.
		for release := range releases {
			release()
			release()
		}
	}

	for i := range limit {
		if _, ok := c.admit(); !ok {
			t.Fatalf("after every slot was released, admission %d of %d was refused", i+1, limit)
		}
	}
	if _, ok := c.admit(); ok {
		t.Errorf("a cap of %d admitted %d streams and then one more", limit, limit)
	}
}

// TestAClosedStreamGivesItsSlotBack is the cap over the wire, which is the only
// place the ending that matters can be observed: a browser that goes away.
//
// The release is the daemon's own doing on its own goroutine some time after the
// peer disappears, and there is no event a test can wait on for it — so this
// polls, bounded by the same budget as everything else here. A leaked slot fails
// it by running that budget out rather than by hanging.
func TestAClosedStreamGivesItsSlotBack(t *testing.T) {
	t.Parallel()

	f, addr := watchingWithCap(t, 1)
	live, _ := f.fixture.plant(t, session.Session{Name: "watch me", WorkDir: f.fixture.repo})

	opened := time.Now()
	first := f.watch(t, addr, live.ID) //nolint:bodyclose // watchFrom closes it in t.Cleanup, which the linter cannot see through.
	if first.StatusCode != http.StatusOK {
		t.Fatalf("the first open answered %d; want %d", first.StatusCode, http.StatusOK)
	}
	readScreen(t, bufio.NewReader(first.Body), opened)

	// The second tab, on a daemon with room for one.
	second := f.watch(t, addr, live.ID) //nolint:bodyclose // watchFrom closes it in t.Cleanup, which the linter cannot see through.
	if second.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("a second stream past a cap of one answered %d; want %d", second.StatusCode, http.StatusTooManyRequests)
	}
	if body, err := io.ReadAll(second.Body); err != nil || len(body) != 0 {
		t.Errorf("the refusal carried %q (read error %v); a stream that was never admitted is a response that never started", body, err)
	}

	// The browser goes away, and the slot comes back.
	if err := first.Body.Close(); err != nil {
		t.Fatalf("close the first stream: %v", err)
	}
	for deadline := time.Now().Add(streamTestBudget); ; {
		third := f.watch(t, addr, live.ID) //nolint:bodyclose // watchFrom closes it in t.Cleanup, which the linter cannot see through.
		if third.StatusCode == http.StatusOK {
			readScreen(t, bufio.NewReader(third.Body), time.Now())
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("the slot a closed stream held was still held %v later; the last open answered %d",
				streamTestBudget, third.StatusCode)
		}
		time.Sleep(tickUnderTest)
	}
}

// --- T024: the capture loop, and what it does not send ------------------------
//
// One reading per watched session per interval, an event only when the screen
// differs from the one this stream last sent, and the heartbeat on every other
// tick (research D5). What an event *carries* is T025's and deliberately absent:
// readScreen insists on the placeholder, so the change that puts an unframed
// screen on a line-oriented wire fails here rather than passing quietly.

// TestAnUnchangedScreenIsNeverSentTwice is the suppression rule (research D5),
// and it is the reason the loop is not simply "capture and write".
//
// A Claude session idling at a prompt repaints nothing for minutes at a time. A
// tick that re-sent the identical screen would push it to every open tab every
// second for as long as the tab lives — an exec, a write and a wake-up each, for
// zero information, on a connection whose whole purpose is to be quiet until
// something happens.
func TestAnUnchangedScreenIsNeverSentTwice(t *testing.T) {
	t.Parallel()

	f, addr := watching(t)
	live, _ := f.fixture.plant(t, session.Session{Name: "watch me", WorkDir: f.fixture.repo})
	f.fixture.tmux.SetPane(live.TmuxName(), "$ go test ./...\nok\tinternal/access\t0.31s\n")

	opened := time.Now()
	resp := f.watch(t, addr, live.ID) //nolint:bodyclose // watch closes it in t.Cleanup, which the linter cannot see through.
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /sessions/%s/stream = %d; want %d", live.ID, resp.StatusCode, http.StatusOK)
	}

	body := bufio.NewReader(resp.Body)
	readScreen(t, body, opened)

	for tick := 1; tick <= ticksWatched; tick++ {
		if line := readGroup(t, body, opened); line != heartbeatLine {
			t.Fatalf("tick %d of a session that printed nothing wrote %q; want the heartbeat — an unchanged screen is not re-sent",
				tick, line)
		}
	}
}

// TestTheOpeningScreenIsSentWithoutWaitingOutAnInterval is the other half of the
// cadence rule, and the one a suppression test cannot state.
//
// A browser attaches to this stream from a page rendered with a capture of its
// own. A stream that waited out its first interval would leave that capture the
// newest thing the operator has for a whole second, on a page whose entire
// purpose is to be live — while the screen it would have sent is already in the
// shared buffer, put there by the open itself.
func TestTheOpeningScreenIsSentWithoutWaitingOutAnInterval(t *testing.T) {
	t.Parallel()

	f, addr := watching(t)
	live, _ := f.fixture.plant(t, session.Session{Name: "watch me", WorkDir: f.fixture.repo})
	f.fixture.tmux.SetPane(live.TmuxName(), "a screen the page already rendered")

	opened := time.Now()
	resp := f.watch(t, addr, live.ID) //nolint:bodyclose // watch closes it in t.Cleanup, which the linter cannot see through.
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /sessions/%s/stream = %d; want %d", live.ID, resp.StatusCode, http.StatusOK)
	}

	readScreen(t, bufio.NewReader(resp.Body), opened)
	if waited := time.Since(opened); waited >= tickUnderTest {
		t.Errorf("the opening screen arrived %v after the open, which is past the %v interval; it must not wait for a tick",
			waited.Round(time.Millisecond), tickUnderTest)
	}
}

// TestAChangedScreenIsSentOnceAndThenSuppressed is the event half: exactly one
// event per change, and nothing after it until the next one.
//
// Both halves are the claim. A loop that sent on every tick would pass a test
// that only asked whether the change arrived, and a loop that never sent would
// pass one that only counted heartbeats.
func TestAChangedScreenIsSentOnceAndThenSuppressed(t *testing.T) {
	t.Parallel()

	f, addr := watching(t)
	live, _ := f.fixture.plant(t, session.Session{Name: "watch me", WorkDir: f.fixture.repo})
	f.fixture.tmux.SetPane(live.TmuxName(), "$ ")

	opened := time.Now()
	resp := f.watch(t, addr, live.ID) //nolint:bodyclose // watch closes it in t.Cleanup, which the linter cannot see through.
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /sessions/%s/stream = %d; want %d", live.ID, resp.StatusCode, http.StatusOK)
	}

	body := bufio.NewReader(resp.Body)
	readScreen(t, body, opened)

	f.fixture.tmux.SetPane(live.TmuxName(), "$ go test ./...\n")

	// At most one quiet tick before it: the only thing that can suppress the
	// reading is the shared buffer still being inside its interval, and one
	// stream's ticks are an interval apart, so it cannot happen twice running.
	// More than that is a loop reading the session less often than it ticks.
	if quiet := awaitScreen(t, body, opened); quiet > 1 {
		t.Errorf("a screen that changed went unsent for %d ticks before it arrived; want at most 1", quiet)
	}

	for tick := 1; tick <= ticksWatched; tick++ {
		if line := readGroup(t, body, opened); line != heartbeatLine {
			t.Fatalf("the screen changed once and was sent again on tick %d as %q; want exactly one event per change",
				tick, line)
		}
	}
}

// TestTwoTabsOnOneSessionCostOneReadingBetweenThem is the cost model
// contracts/stream.md splits from the cap: CRSW_MAX_STREAMS counts connections,
// and the work a watched session costs this host counts sessions.
//
// Ten tabs on one session must be one capture-pane a second and not ten, or a
// single operator with a tiling window manager is a load generator and the bound
// Principle VI calls non-negotiable bounds the wrong quantity.
//
// The bound asserted is one the buffer guarantees by construction rather than
// one this host happened to produce: readings are separated by at least the
// interval, so however many streams are attached, a stretch of D can hold no
// more than D/interval of them. Without the sharing the same stretch costs twice
// that, which is what makes the assertion discriminate.
func TestTwoTabsOnOneSessionCostOneReadingBetweenThem(t *testing.T) {
	t.Parallel()

	f, addr := watching(t)
	live, _ := f.fixture.plant(t, session.Session{Name: "watch me", WorkDir: f.fixture.repo})
	f.fixture.tmux.SetPane(live.TmuxName(), "$ ")

	opened := time.Now()
	first := f.watch(t, addr, live.ID)  //nolint:bodyclose // watch closes it in t.Cleanup, which the linter cannot see through.
	second := f.watch(t, addr, live.ID) //nolint:bodyclose // watch closes it in t.Cleanup, which the linter cannot see through.
	for name, resp := range map[string]*http.Response{"the first tab": first, "the second tab": second} {
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("%s answered %d; want %d", name, resp.StatusCode, http.StatusOK)
		}
	}

	firstBody, secondBody := bufio.NewReader(first.Body), bufio.NewReader(second.Body)
	readScreen(t, firstBody, opened)
	readScreen(t, secondBody, opened)

	// Both tabs see the change, which is the half saying the sharing did not
	// silence one of them.
	f.fixture.tmux.SetPane(live.TmuxName(), "$ go test ./...\n")
	awaitScreen(t, firstBody, opened)
	awaitScreen(t, secondBody, opened)

	// Twice ticksWatched, so that the count the sharing produces and the count two
	// independent buffers would produce are far enough apart that neither the
	// scheduler nor a loaded host can put one where the other belongs.
	for tick := 1; tick <= 2*ticksWatched; tick++ {
		readGroup(t, firstBody, opened)
		readGroup(t, secondBody, opened)
	}
	watched := time.Since(opened)

	captures := 0
	for _, c := range f.fixture.tmux.Calls() {
		if c.Op == tmuxctl.OpCapturePane {
			captures++
		}
	}
	// Three of slack: the reading the open made before any interval had begun, the
	// one in flight while the count is taken, and the truncation in the division.
	if want := int(watched/tickUnderTest) + 3; captures > want {
		t.Errorf("two tabs watching one session for %v cost %d captures; want at most %d — the cap counts connections, the exec counts sessions",
			watched.Round(time.Millisecond), captures, want)
	}
}

// TestAScreenThatCannotBeReadIsSuppressedAndReportedOnce is the failing capture:
// the window went away, or tmux stopped answering.
//
// It is a suppressed tick and not the end of the stream, because an exec that did
// not answer is not a session that ended — that judgement is made against the
// daemon's own records, and it is T028's. What it must never be is an event: a
// stream that sent an empty screen on a failed capture would be telling the
// operator their session had wiped itself.
//
// Reported once rather than once per tick. The same failure repeats every second
// for as long as the tab stays open, and a journal filling at that rate buries
// the first line, which is the only one that says anything.
func TestAScreenThatCannotBeReadIsSuppressedAndReportedOnce(t *testing.T) {
	t.Parallel()

	f := watchingUnserved(t, config.DefaultMaxStreams)
	live, _ := f.fixture.plant(t, session.Session{Name: "watch me", WorkDir: f.fixture.repo})

	// The fixture's own recorder appends without a lock, which is safe for a
	// handler running inside ServeHTTP on the test's goroutine and is not safe for
	// a stream's ticks. Installed before Serve, for the reason watchingUnserved
	// exists.
	var (
		mu       sync.Mutex
		failures []error
	)
	f.report = func(err error) {
		mu.Lock()
		defer mu.Unlock()
		failures = append(failures, err)
	}
	addr := serve(t, f)

	broken := errors.New("tmux is not answering")
	f.fixture.tmux.FailOp(tmuxctl.OpCapturePane, broken)

	opened := time.Now()
	resp := f.watch(t, addr, live.ID) //nolint:bodyclose // watch closes it in t.Cleanup, which the linter cannot see through.
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /sessions/%s/stream = %d; want %d — an unreadable screen is not a refusal", live.ID, resp.StatusCode, http.StatusOK)
	}

	body := bufio.NewReader(resp.Body)
	for tick := 1; tick <= ticksWatched; tick++ {
		if line := readGroup(t, body, opened); line != heartbeatLine {
			t.Fatalf("a stream that could not read the screen wrote %q on tick %d; a capture that failed is a suppressed tick",
				line, tick)
		}
	}
	if err := resp.Body.Close(); err != nil {
		t.Fatalf("close the stream: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(failures) != 1 {
		t.Fatalf("%d ticks of an unreadable screen were reported %d times; want exactly 1 — an operator has to be able to find the first",
			ticksWatched, len(failures))
	}
	if !errors.Is(failures[0], broken) {
		t.Errorf("reported %v; want what tmux said, wrapped", failures[0])
	}
	if got := failures[0].Error(); !strings.Contains(got, live.ID) {
		t.Errorf("reported %q; it names no session, and an operator reading it has nothing to act on", got)
	}
}

// TestOneReadingFillsEveryWatcherWithinTheInterval is the buffer's own claim,
// stated without a socket or a stopwatch: the interval decides how often the
// session is read, and the number of watchers decides nothing.
func TestOneReadingFillsEveryWatcherWithinTheInterval(t *testing.T) {
	t.Parallel()

	var (
		shared   pane
		captures int
	)
	read := func(context.Context) (string, error) {
		captures++
		return fmt.Sprintf("reading %d", captures), nil
	}

	// An interval nothing in a test can outlast, so every ask inside it is the
	// same ask.
	for watcher := 1; watcher <= ticksWatched; watcher++ {
		screen, err := shared.current(context.Background(), time.Hour, read)
		if err != nil {
			t.Fatalf("watcher %d: current() = _, %v; want the shared reading", watcher, err)
		}
		if screen != "reading 1" {
			t.Errorf("watcher %d read %q; want the reading the first watcher took", watcher, screen)
		}
	}
	if captures != 1 {
		t.Errorf("%d watchers inside one interval cost %d captures; want 1", ticksWatched, captures)
	}

	// And an interval that has already elapsed reads the session again, or the
	// buffer above is not a cache but a freeze.
	if _, err := shared.current(context.Background(), 0, read); err != nil {
		t.Fatalf("current() past the interval = _, %v; want a fresh reading", err)
	}
	if captures != 2 {
		t.Errorf("an ask past the interval cost %d captures in total; want 2", captures)
	}
}

// TestWatchersRacingAStaleBufferReadTheSessionOnce is the same claim under
// contention, which the sequential one above cannot make.
//
// The staleness check and the reading have to be one critical section. Split
// into "is it stale?" and then "read it", every watcher that arrived while the
// first was still waiting on tmux would find the buffer stale and exec too —
// which is the N-tabs-N-execs failure the buffer exists to prevent, appearing
// only when the host is slow enough for it to matter.
//
// Rounds rather than one shot, for the reason the stream cap's race test has
// them: the window is a few instructions wide, and a suite that catches this
// only under -race is a suite that does not catch it.
func TestWatchersRacingAStaleBufferReadTheSessionOnce(t *testing.T) {
	t.Parallel()

	const (
		watchers = 64
		rounds   = 200
	)

	for round := range rounds {
		var (
			shared   pane
			captures atomic.Int64
			start    sync.WaitGroup
			done     sync.WaitGroup
		)
		read := func(context.Context) (string, error) {
			captures.Add(1)
			return "one screen", nil
		}
		start.Add(1)
		for range watchers {
			done.Add(1)
			go func() {
				defer done.Done()
				start.Wait()
				if _, err := shared.current(context.Background(), time.Hour, read); err != nil {
					t.Errorf("current() = _, %v; want the shared reading", err)
				}
			}()
		}
		start.Done()
		done.Wait()

		if got := captures.Load(); got != 1 {
			t.Fatalf("round %d: %d watchers racing an empty buffer read the session %d times; want exactly 1",
				round, watchers, got)
		}
	}
}

// TestASharedScreenIsDroppedWhenItsLastWatcherLeaves is the buffer's lifetime.
//
// A screen is whatever the session printed, which is anything on the host, and
// it is secret under docs/security.md §3. A registry that kept one per session
// anybody had ever watched would be a daemon accumulating exactly that material
// for as long as it runs — and the reading it kept would be stale anyway, since
// what makes one worth sharing is that it was taken this second.
func TestASharedScreenIsDroppedWhenItsLastWatcherLeaves(t *testing.T) {
	t.Parallel()

	const id = "0123456789abcdef0123456789abcdef"
	p := newPanes()

	first, leaveFirst := p.attach(id)
	second, leaveSecond := p.attach(id)
	if first != second {
		t.Fatal("two streams watching one session were handed two buffers; then each of them reads the session on its own")
	}

	// Twice, because a release that ran twice would drop a buffer another stream
	// is still sharing — which costs execs rather than correctness, and so would
	// go unnoticed.
	leaveFirst()
	leaveFirst()
	if _, watched := p.watched[id]; !watched {
		t.Fatal("the buffer was dropped while a stream was still watching")
	}

	leaveSecond()
	if _, watched := p.watched[id]; watched {
		t.Error("the buffer outlived its last watcher; a session's screen may not accumulate in a daemon that runs for weeks")
	}
}
