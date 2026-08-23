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
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
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

	// slowTickUnderTest is for the one test that asks whether something happened
	// *before* a tick could have. Ten milliseconds is a fine cadence for a test
	// that waits for ticks and a terrible budget for one that races them: it made
	// TestTheOpeningScreenIsSentWithoutWaitingOutAnInterval a measurement of how
	// loaded the machine was, and it failed in CI at 16ms while passing on every
	// developer's host.
	//
	// A second is long enough that no amount of scheduling noise turns "did not
	// wait for a tick" into "waited for a tick", and the test still finishes in
	// milliseconds — because the whole claim is that it does not wait.
	slowTickUnderTest = time.Second

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

// heartbeatLine is the suppressed tick as it arrives on the wire. Derived from
// the constant rather than spelled again here, so a change to it moves this with
// it, and split at the first newline because readGroup reads a group's first
// line and its terminator apart.
var heartbeatLine = firstLine(heartbeat)

func firstLine(group []byte) string {
	line, _, _ := strings.Cut(string(group), "\n")
	return line + "\n"
}

// screenLine is the other thing a tick can write: the first line of the event
// one screen is framed as.
//
// It goes through the production framing rather than spelling `data:` again, so
// that every expectation in this file follows a change to the wire format. The
// framing itself is pinned against bytes spelled out by hand in
// TestAScreenIsFramedAsOneJSONString — which is where the tautology this would
// otherwise be is closed.
func screenLine(t *testing.T, screen string) string {
	t.Helper()

	event, err := screenEvent(screen)
	if err != nil {
		t.Fatalf("screenEvent(%q) = _, %v; want the framed event", screen, err)
	}
	return firstLine(event)
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

// readScreen reads one group and insists it was the event carrying the screen
// the caller planted.
//
// The screen is a parameter rather than a wildcard, and that is the assertion
// rather than bookkeeping: a helper that accepted any `data:` line would go on
// passing against a stream that sent every tab the same constant, which is
// exactly what this route wrote before the framing landed.
func readScreen(t *testing.T, body *bufio.Reader, opened time.Time, screen string) {
	t.Helper()

	want := screenLine(t, screen)
	if line := readGroup(t, body, opened); line != want {
		t.Fatalf("the stream wrote %q; want %q", line, want)
	}
}

// awaitScreen reads groups until the event carrying the changed screen arrives,
// and reports how many heartbeats preceded it.
//
// A change is not expected on the very next group: the tick after a screen
// changed may find the shared buffer still inside its interval, in which case
// the reading that notices is one tick later. What is bounded is how long that
// may take, which is what ticksWatched is.
//
// Any *other* event fails immediately rather than being waited through. The
// screen a stream has already sent is suppressed, so the only event that can
// arrive between the plant and the change is one carrying something neither of
// them is.
func awaitScreen(t *testing.T, body *bufio.Reader, opened time.Time, screen string) int {
	t.Helper()

	changed := screenLine(t, screen)
	for quiet := 0; quiet < ticksWatched; quiet++ {
		switch line := readGroup(t, body, opened); line {
		case changed:
			return quiet
		case heartbeatLine:
		default:
			t.Fatalf("the stream wrote %q; want either the heartbeat or the screen %q", line, screen)
		}
	}
	t.Fatalf("a screen that changed went unsent for %d ticks", ticksWatched)
	return 0
}

// decodeScreen reads one group and hands back the screen it framed, which is the
// client's own half of the wire format: parse the `data:` field as JSON and take
// the string out of it.
//
// It is deliberately not readScreen with the comparison moved: what it asserts
// is that the payload round-trips, so it is the helper for the claims about
// bytes a session printed rather than for the claims about which screen arrived.
func decodeScreen(t *testing.T, body *bufio.Reader, opened time.Time) string {
	t.Helper()

	line := readGroup(t, body, opened)
	payload, ok := strings.CutPrefix(strings.TrimSuffix(line, "\n"), dataField)
	if !ok {
		t.Fatalf("the stream wrote %q; want an event carrying a screen", line)
	}

	var screen string
	if err := json.Unmarshal([]byte(payload), &screen); err != nil {
		t.Fatalf("the payload on the wire is not a JSON string: %q (%v)", payload, err)
	}
	return screen
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
	// The opening screen, which precedes the first tick. This session has printed
	// nothing, and the empty screen is still an event — `data: ""` rather than an
	// empty data buffer, which EventSource would drop.
	body := bufio.NewReader(resp.Body)
	readScreen(t, body, opened, "")
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
	readScreen(t, body, opened, "")
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
	readScreen(t, bufio.NewReader(first.Body), opened, "")

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
			readScreen(t, bufio.NewReader(third.Body), time.Now(), "")
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
// tick (research D5). Every claim below names the screen it expects on the wire,
// so a loop that sent the right screen at the wrong time and one that sent the
// wrong screen at the right time both fail here.

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

	const screen = "$ go test ./...\nok\tinternal/access\t0.31s\n"

	f, addr := watching(t)
	live, _ := f.fixture.plant(t, session.Session{Name: "watch me", WorkDir: f.fixture.repo})
	f.fixture.tmux.SetPane(live.TmuxName(), screen)

	opened := time.Now()
	resp := f.watch(t, addr, live.ID) //nolint:bodyclose // watch closes it in t.Cleanup, which the linter cannot see through.
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /sessions/%s/stream = %d; want %d", live.ID, resp.StatusCode, http.StatusOK)
	}

	body := bufio.NewReader(resp.Body)
	readScreen(t, body, opened, screen)

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

	const screen = "a screen the page already rendered"

	// A slow tick, deliberately. This test asks whether the opening screen is sent
	// *without* waiting for one, and the only honest way to ask that is to make a
	// tick long enough that waiting for one would be unmistakable. Against the
	// ordinary 10ms cadence the assertion was really "is this machine busy?" — and
	// on a loaded CI runner the answer was yes.
	f := watchingUnserved(t, config.DefaultMaxStreams)
	f.streamTick = slowTickUnderTest
	addr := serve(t, f)

	live, _ := f.fixture.plant(t, session.Session{Name: "watch me", WorkDir: f.fixture.repo})
	f.fixture.tmux.SetPane(live.TmuxName(), screen)

	opened := time.Now()
	resp := f.watch(t, addr, live.ID) //nolint:bodyclose // watch closes it in t.Cleanup, which the linter cannot see through.
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /sessions/%s/stream = %d; want %d", live.ID, resp.StatusCode, http.StatusOK)
	}

	readScreen(t, bufio.NewReader(resp.Body), opened, screen)
	if waited := time.Since(opened); waited >= slowTickUnderTest {
		t.Errorf("the opening screen arrived %v after the open, which is past the %v interval; it must not wait for a tick",
			waited.Round(time.Millisecond), slowTickUnderTest)
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

	const (
		prompt  = "$ "
		running = "$ go test ./...\n"
	)

	f, addr := watching(t)
	live, _ := f.fixture.plant(t, session.Session{Name: "watch me", WorkDir: f.fixture.repo})
	f.fixture.tmux.SetPane(live.TmuxName(), prompt)

	opened := time.Now()
	resp := f.watch(t, addr, live.ID) //nolint:bodyclose // watch closes it in t.Cleanup, which the linter cannot see through.
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /sessions/%s/stream = %d; want %d", live.ID, resp.StatusCode, http.StatusOK)
	}

	body := bufio.NewReader(resp.Body)
	readScreen(t, body, opened, prompt)

	f.fixture.tmux.SetPane(live.TmuxName(), running)

	// At most one quiet tick before it: the only thing that can suppress the
	// reading is the shared buffer still being inside its interval, and one
	// stream's ticks are an interval apart, so it cannot happen twice running.
	// More than that is a loop reading the session less often than it ticks.
	if quiet := awaitScreen(t, body, opened, running); quiet > 1 {
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

	const (
		prompt  = "$ "
		running = "$ go test ./...\n"
	)

	f, addr := watching(t)
	live, _ := f.fixture.plant(t, session.Session{Name: "watch me", WorkDir: f.fixture.repo})
	f.fixture.tmux.SetPane(live.TmuxName(), prompt)

	opened := time.Now()
	first := f.watch(t, addr, live.ID)  //nolint:bodyclose // watch closes it in t.Cleanup, which the linter cannot see through.
	second := f.watch(t, addr, live.ID) //nolint:bodyclose // watch closes it in t.Cleanup, which the linter cannot see through.
	for name, resp := range map[string]*http.Response{"the first tab": first, "the second tab": second} {
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("%s answered %d; want %d", name, resp.StatusCode, http.StatusOK)
		}
	}

	firstBody, secondBody := bufio.NewReader(first.Body), bufio.NewReader(second.Body)
	readScreen(t, firstBody, opened, prompt)
	readScreen(t, secondBody, opened, prompt)

	// Both tabs see the change, which is the half saying the sharing did not
	// silence one of them — and both see the same screen, which is the half saying
	// the buffer they share is the session's and not one tab's.
	f.fixture.tmux.SetPane(live.TmuxName(), running)
	awaitScreen(t, firstBody, opened, running)
	awaitScreen(t, secondBody, opened, running)

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

// --- T025: what an event carries ---------------------------------------------
//
// One JSON string holding the whole screen, in one `data:` field (research D4,
// contracts/stream.md). The claim is not that the payload is legible on the
// wire: it is that no byte a session prints can decide where this daemon's
// events begin and end, which on a line-oriented transport is a claim that has
// to be made against the bytes that would otherwise decide it.

// TestAScreenIsFramedAsOneJSONString spells the wire out by hand, row by row.
//
// Spelled rather than derived, deliberately. Every other expectation in this
// file goes through screenEvent, so this is the one place where a change to the
// framing has to be written down twice before the suite agrees with it — which
// is what keeps the rest of them from being restatements of the code.
//
// Each row is then decoded back, because the two halves fail differently. Bytes
// that are wrong but still parse are a client rendering the wrong screen; bytes
// that parse to the right screen but carry a newline of their own are a client
// that never sees the event at all.
func TestAScreenIsFramedAsOneJSONString(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct {
		screen, want string
	}{
		"a screen of plain text": {
			screen: "$ go test ./...",
			want:   `data: "$ go test ./..."` + groupEnd,
		},
		"the several lines a screen really is": {
			screen: "$ go test ./...\nok\tinternal/access\t0.31s\n",
			want:   `data: "$ go test ./...\nok\tinternal/access\t0.31s\n"` + groupEnd,
		},
		"a lone carriage return, which rejoining split fields loses": {
			screen: "downloading\rdownloaded\n",
			want:   `data: "downloading\rdownloaded\n"` + groupEnd,
		},
		// encoding/json escapes the three characters that could close a tag in an
		// HTML document, which is neither asked for here nor in the way: the client
		// parses before it renders, and JSON.parse gives the same string back. It is
		// spelled out rather than smoothed away so that a future encoder configured
		// to stop doing it is a change somebody sees.
		"markup the session printed": {
			screen: "<script>alert('pwned')</script>",
			want:   "data: \"\\u003cscript\\u003ealert('pwned')\\u003c/script\\u003e\"" + groupEnd,
		},
		"quotes and a backslash": {
			screen: `he said "run it" \ then left`,
			want:   `data: "he said \"run it\" \\ then left"` + groupEnd,
		},
		"a session that has printed nothing at all": {
			screen: "",
			want:   `data: ""` + groupEnd,
		},
		"a line the session printed that spells this transport's own framing": {
			screen: "event: end\ndata: \"ended\"\n",
			want:   `data: "event: end\ndata: \"ended\"\n"` + groupEnd,
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			event, err := screenEvent(tc.screen)
			if err != nil {
				t.Fatalf("screenEvent(%q) = _, %v; want the framed event", tc.screen, err)
			}
			if got := string(event); got != tc.want {
				t.Errorf("screenEvent(%q) framed it as %q; want %q", tc.screen, got, tc.want)
			}

			// One field and one terminator, whatever the screen holds. A third
			// newline is the whole failure this encoding exists to prevent: it is a
			// session deciding where an event ends, and everything after it is a
			// field this daemon did not write.
			if lines := strings.Count(string(event), "\n"); lines != 2 {
				t.Errorf("the event for %q holds %d newlines; want 2 — the end of the one `data:` field, and the blank line that ends the group",
					tc.screen, lines)
			}

			// And back to the identical bytes, which is the half the client's
			// JSON.parse will perform.
			payload, ok := strings.CutPrefix(strings.TrimSuffix(string(event), groupEnd), dataField)
			if !ok {
				t.Fatalf("the event for %q is %q; want one %q field", tc.screen, event, dataField)
			}
			var back string
			if err := json.Unmarshal([]byte(payload), &back); err != nil {
				t.Fatalf("the payload for %q is not a JSON string: %q (%v)", tc.screen, payload, err)
			}
			if back != tc.screen {
				t.Errorf("the payload for %q parses back to %q; a screen must survive the wire byte for byte", tc.screen, back)
			}
		})
	}
}

// TestTheScreenOnTheWireIsTheScreenTheSessionPrinted is the framing where it
// has to hold: through the handler, over a socket, decoded the way the client
// will decode it.
//
// The table above would pass against a handler that never called screenEvent at
// all — which is this project's own recurring failure, code that exists and
// nothing runs. The screen here is the one a session with something to gain
// would print: markup, quotes, several lines, and a line spelling this
// transport's framing.
//
// The lone `\r` from the table is deliberately not in it. Strip removes carriage
// returns with the rest of C0 before a capture ever reaches the framing, so a
// `\r` asserted over the wire would be an assertion about the stripper — which
// is exactly why the framing is proved against one where it can actually be
// handed one, rather than being trusted to a stripper that agrees with it today.
func TestTheScreenOnTheWireIsTheScreenTheSessionPrinted(t *testing.T) {
	t.Parallel()

	const printed = "$ cat page.html\n<script>alert(\"pwned\")</script>\ndata: \"not an event\"\n$ "

	f, addr := watching(t)
	live, _ := f.fixture.plant(t, session.Session{Name: "watch me", WorkDir: f.fixture.repo})
	f.fixture.tmux.SetPane(live.TmuxName(), printed)

	opened := time.Now()
	resp := f.watch(t, addr, live.ID) //nolint:bodyclose // watch closes it in t.Cleanup, which the linter cannot see through.
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /sessions/%s/stream = %d; want %d", live.ID, resp.StatusCode, http.StatusOK)
	}

	// readGroup has already insisted the payload was one line ending in a blank
	// one, so a screen that framed itself fails inside this call rather than in
	// the comparison after it.
	body := bufio.NewReader(resp.Body)
	if got := decodeScreen(t, body, opened); got != printed {
		t.Errorf("the wire carried %q; want the screen the session printed, byte for byte:\n%q", got, printed)
	}

	// The ordinary quiet tick after it, which is what says the stream carried a
	// screen like that rather than ending on one.
	readHeartbeat(t, body, opened)
}

// --- T027: the record, written at the open ------------------------------------
//
// FR-016a's whole claim, in the two halves it has: the record is on the trail
// while the stream is still running, and the stream still produces exactly one.

// TestTheStreamsRecordIsOnTheTrailWhileTheStreamIsStillOpen is the claim
// itself, and it is only a claim at all because it is made *during* the stream.
//
// Milestone 1's shape — the middleware emitting on the way out — is correct for
// six routes that answer in a millisecond and wrong for one that answers for
// hours. A daemon that dies mid-stream under it leaves nothing behind saying a
// session's screen was being read, which is the fact an incident review most
// needs from this route and the only fact it uniquely has.
//
// The group read off the wire is what makes the assertion after it meaningful:
// it is written by the loop that runs after the open, so a trail read once it
// has arrived is a trail read while the response is genuinely still being
// served.
func TestTheStreamsRecordIsOnTheTrailWhileTheStreamIsStillOpen(t *testing.T) {
	t.Parallel()

	const screen = "$ go test ./...\nok\tinternal/httpapi\t5.4s\n"

	f, addr := watching(t)
	live, _ := f.fixture.plant(t, session.Session{Name: "watch me", WorkDir: f.fixture.repo})
	f.fixture.tmux.SetPane(live.TmuxName(), screen)

	opened := time.Now()
	resp := f.watch(t, addr, live.ID) //nolint:bodyclose // watch closes it in t.Cleanup, which the linter cannot see through.
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /sessions/%s/stream = %d; want %d", live.ID, resp.StatusCode, http.StatusOK)
	}

	body := bufio.NewReader(resp.Body)
	readScreen(t, body, opened, screen)

	records := f.records(t)
	if len(records) != 1 {
		t.Fatalf("a stream that is still open has emitted %d audit records (%v); want exactly one, written at the open",
			len(records), records)
	}
	rec := records[0]
	if got, want := rec["action"], string(audit.ActionStreamOpen); got != want {
		t.Errorf("action = %v; want %v", got, want)
	}
	if got, want := rec["decision"], string(audit.Allow); got != want {
		t.Errorf("decision = %v; want %v — the record carries the authorisation decision the open made", got, want)
	}
	if got, want := rec["session_id"], live.ID; got != want {
		t.Errorf("session_id = %v; want %v — the id off the daemon's own record", got, want)
	}
	if got, want := rec["caller"], string(auth.CallerOperator); got != want {
		t.Errorf("caller = %v; want %v — the server-derived owner, never the verified address", got, want)
	}

	// And it carries none of what the stream carries. The screen went over the
	// wire in the group read above, which is the only place in this daemon it may
	// appear (FR-035, FR-042).
	if trail := f.sink.String(); strings.Contains(trail, screen) || strings.Contains(trail, live.Name) {
		t.Errorf("the trail carries what the stream is showing:\n%s", trail)
	}

	// The stream is still delivering after the record was written, which is what
	// separates "emitted at the open" from "emitted because it ended".
	readHeartbeat(t, body, opened)
}

// TestARecordAlreadyWrittenIsNotWrittenAgainWhenTheHandlerReturns is the other
// half: one record per stream request, and no close record
// (contracts/stream.md, SC-008).
//
// It is stated here rather than over the wire because the second emit is the
// middleware's deferred one, which runs after the handler has unwound on
// net/http's own goroutine — there is no event a test can wait on for it, so a
// wire test could only ever say "no second record had appeared yet". What the
// guarantee actually rests on is that the record is written at most once, which
// is a property of the pair below and is exactly what this drives.
//
// The amendment between the two emits is the trap that comes with emitting
// early, pinned so it stays a known rule rather than a surprise: a handler that
// denies after emitting has denied nobody.
func TestARecordAlreadyWrittenIsNotWrittenAgainWhenTheHandlerReturns(t *testing.T) {
	t.Parallel()

	s := newAuditedServer(t)
	ra := &RequestAudit{rec: audit.Record{
		Action:   audit.ActionStreamOpen,
		Caller:   string(auth.CallerOperator),
		Decision: audit.Allow,
	}}

	s.emit(ra)
	ra.Deny(errStreamCapReached.Error())
	s.emit(ra)

	rec := s.only(t)
	if got, want := rec["decision"], string(audit.Allow); got != want {
		t.Errorf("decision = %v; want %v — the record on the trail is the one written at the open", got, want)
	}
	if reason, ok := rec["reason"]; ok {
		t.Errorf("reason = %v; want none — an amendment after the emit reaches nobody", reason)
	}
	if len(s.failed) != 0 {
		t.Errorf("a second emit reported %v; want nothing — writing once is not a failure", s.failed)
	}
}

// --- T028: the lifecycle ------------------------------------------------------
//
// A stream is authorised at the open and re-authorised for as long as it runs
// (FR-034b), it never postpones the deadline of the session it is watching
// (FR-034f), and it is a response the daemon can end — by the session going
// away, and by the daemon itself stopping.

// awaitEnd reads until the terminal event arrives, and asserts its bytes.
//
// The bytes are spelled here by hand rather than derived from endEvent, for the
// reason TestAScreenIsFramedAsOneJSONString spells its table: this is the one
// place a change to the terminal event has to be written down twice before the
// suite agrees with it. A client that is not this repository reads them, so what
// they are is contracts/stream.md's business rather than an implementation
// detail.
//
// Ordinary groups before it are read through rather than refused. The session
// ends between two ticks, so whatever the stream was doing when it happened — a
// heartbeat, or an event for a screen that changed just before — is allowed to
// arrive first. What is bounded is how long the end may take, which is SC-015's
// one interval expressed in writes.
func awaitEnd(t *testing.T, body *bufio.Reader, opened time.Time) {
	t.Helper()

	for read := 0; read < ticksWatched; read++ {
		line := readStreamLine(t, body, opened)
		if line != "event: end\n" {
			// An ordinary two-line group: its terminator, then on to the next.
			if term := readStreamLine(t, body, opened); term != "\n" {
				t.Fatalf("%q was followed by %q; an SSE line group ends with a blank line", line, term)
			}
			continue
		}

		if data := readStreamLine(t, body, opened); data != "data: \"ended\"\n" {
			t.Fatalf("the terminal event carried %q; contracts/stream.md spells its payload as one JSON string", data)
		}
		if term := readStreamLine(t, body, opened); term != "\n" {
			t.Fatalf("the terminal event was followed by %q; an SSE line group ends with a blank line", term)
		}
		return
	}
	t.Fatalf("the watched session ended and no terminal event arrived within %d writes; a view that stops in silence is a session that looks quiet",
		ticksWatched)
}

// assertStreamIsOver insists the response ended, cleanly, with nothing after the
// terminal event.
//
// Both halves matter and they fail differently. Bytes after the end are a client
// that was told to close and then handed more; a read that fails rather than
// reaching EOF is a connection torn down instead of a response completed, which
// is what a browser reconnects from.
func assertStreamIsOver(t *testing.T, body *bufio.Reader) {
	t.Helper()

	rest, err := io.ReadAll(body)
	if err != nil {
		t.Errorf("the stream did not end cleanly: %v", err)
	}
	if len(rest) != 0 {
		t.Errorf("the stream wrote %q after it ended; nothing follows the terminal event", rest)
	}
}

// assertStreamStoppedAtShutdown insists the response ended and that whatever was
// still in flight when it did carried nothing.
//
// It is deliberately not assertStreamIsOver, and the difference is hold's own
// select: when shutdown closes the channel while a tick is already due, both
// cases are ready and Go picks between them at random, so the last thing on the
// wire may be one more comment. That changes nothing an operator saw — a comment
// is not an event and carries no data — but a test forbidding it outright fails
// one run in several against a daemon behaving exactly as contracts/stream.md
// describes, which is what it did before this helper existed.
//
// What is still forbidden is anything with data in it: a screen delivered after
// the daemon stopped serving, or the farewell contracts/stream.md declines to
// promise at shutdown.
func assertStreamStoppedAtShutdown(t *testing.T, body *bufio.Reader) {
	t.Helper()

	rest, err := io.ReadAll(body)
	if err != nil {
		t.Errorf("the stream did not end cleanly: %v", err)
	}
	for _, line := range strings.Split(string(rest), "\n") {
		if line != "" && !strings.HasPrefix(line, ":") {
			t.Errorf("the stream wrote %q as the daemon shut down; a tick already due may still write its comment, and nothing else may follow", line)
		}
	}
}

// TestAReapedSessionEndsTheStreamThatWasWatchingIt is FR-034b, SC-015 and US2
// scenario 7 in one exchange: the reaper takes a session out from under a
// browser that is watching it, and the browser is told.
//
// The reap is a real sweep rather than a record deleted by hand, because the
// claim is about the daemon's own teardown reaching a watcher that teardown
// knows nothing about. Nothing in Sweep looks for streams — that is the point.
// The stream finds out by asking, which is what makes "authorisation is
// re-evaluated, not established" a mechanism rather than a sentence.
func TestAReapedSessionEndsTheStreamThatWasWatchingIt(t *testing.T) {
	t.Parallel()

	const screen = "$ claude --dangerously-skip-permissions\n"

	f, addr := watching(t)
	// Already past its idle bound at the fixture's instant, which gives the sweep
	// below something to take without moving a clock: the stream's cadence is real
	// elapsed time and the manager's is pinned, and the two must not have to agree
	// about anything.
	live, _ := f.fixture.plant(t, session.Session{
		Name: "watch me", WorkDir: f.fixture.repo,
		CreatedAt: pastItsCeilingAt(testTime), LastActivity: pastItsCeilingAt(testTime),
	})
	f.fixture.tmux.SetPane(live.TmuxName(), screen)

	opened := time.Now()
	resp := f.watch(t, addr, live.ID) //nolint:bodyclose // watch closes it in t.Cleanup, which the linter cannot see through.
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /sessions/%s/stream = %d; want %d — a session past its ceiling is still running until the sweep takes it",
			live.ID, resp.StatusCode, http.StatusOK)
	}

	// Delivering before the reap, so that what follows is caused by it.
	body := bufio.NewReader(resp.Body)
	readScreen(t, body, opened, screen)

	reaper, err := session.NewReaper(f.fixture.mgr, testTrail(t))
	if err != nil {
		t.Fatalf("session.NewReaper = _, %v; want a reaper", err)
	}
	// Teardown does not block on watchers: this returns while the stream is still
	// open, and a sweep that waited for one would be the reaper's bound decided by
	// a browser tab.
	reaped, err := reaper.Sweep(context.Background())
	if err != nil {
		t.Fatalf("Sweep() = _, %v; want the watched session reaped", err)
	}
	if len(reaped) != 1 {
		t.Fatalf("the sweep took %d sessions; want the one being watched", len(reaped))
	}

	awaitEnd(t, body, opened)
	assertStreamIsOver(t, body)

	// The buffer goes with the stream that held it. A session's screen is secret
	// under docs/security.md §3, and one kept for a session that no longer exists
	// is material a daemon running for weeks accumulates for nobody.
	//
	// Both questions are asked, because the count alone answers neither: a
	// registry that never dropped anything reports zero watchers for a buffer it
	// is still holding, which is the leak stated rather than the leak refuted.
	if watchers := f.panes.watching(live.ID); watchers != 0 {
		t.Errorf("%d watchers still share the screen buffer of a reaped session; want none", watchers)
	}
	if f.panes.holds(live.ID) {
		t.Error("the daemon is still holding the screen of a session it reaped; a buffer with no watchers is a session's output kept for nobody")
	}
}

// TestWatchingASessionNeverAdvancesItsIdleClock is FR-034f's first half, and the
// reason Manager.View exists at all.
//
// Watching is not driving. A stream that touched the record on every tick would
// postpone the idle deadline for as long as the tab stayed open — and a tab left
// open on a laptop nobody is at would hold an unsandboxed shell alive
// indefinitely, which is the bound Principle VI calls non-negotiable, defeated
// by the feature that was only supposed to look at it.
//
// The record is read out of the store rather than off a response, because what
// must not move is the field the reaper judges, not the field the dashboard
// renders.
func TestWatchingASessionNeverAdvancesItsIdleClock(t *testing.T) {
	t.Parallel()

	const screen = "$ "

	f, addr := watching(t)
	live, _ := f.fixture.plant(t, session.Session{
		Name: "watch me", WorkDir: f.fixture.repo, LastActivity: runningAt(testTime),
	})
	f.fixture.tmux.SetPane(live.TmuxName(), screen)

	opened := time.Now()
	resp := f.watch(t, addr, live.ID) //nolint:bodyclose // watch closes it in t.Cleanup, which the linter cannot see through.
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /sessions/%s/stream = %d; want %d", live.ID, resp.StatusCode, http.StatusOK)
	}

	// Many ticks, so the claim is about a stream that has been watching rather
	// than one that has only opened: every one of them resolves the record again.
	body := bufio.NewReader(resp.Body)
	readScreen(t, body, opened, screen)
	for tick := 1; tick <= ticksWatched; tick++ {
		readGroup(t, body, opened)
	}

	after, err := f.fixture.store.Get(live.ID, auth.CallerOperator)
	if err != nil {
		t.Fatalf("the record of a watched session is gone: %v", err)
	}
	if !after.LastActivity.Equal(live.LastActivity) {
		t.Errorf("%d ticks of watching moved the idle clock from %v to %v; a forgotten tab must not hold a session open",
			ticksWatched, live.LastActivity.UTC(), after.LastActivity.UTC())
	}
}

// TestShutdownIsNotDelayedByOpenStreams is FR-034f's other half.
//
// A stream is an in-flight request that never finishes on its own, and shutdown
// drains in-flight requests before it tears every session down. Left to the
// drain, an open tab would spend the whole budget and then be closed anyway —
// and the budget is not what is being protected. Behind it is the verified
// teardown of unsandboxed shells the daemon is about to stop owning, which is
// not a queue a browser gets to hold up.
//
// Two failures are asserted rather than one, because they are the two shapes
// this goes wrong in: a drain that ran out reports its own deadline, and a
// shutdown that spent the budget spent it out of the teardown's.
func TestShutdownIsNotDelayedByOpenStreams(t *testing.T) {
	t.Parallel()

	const (
		screen = "$ "
		tabs   = 3
	)

	f, addr := watching(t)
	live, _ := f.fixture.plant(t, session.Session{Name: "watch me", WorkDir: f.fixture.repo})
	f.fixture.tmux.SetPane(live.TmuxName(), screen)

	opened := time.Now()
	bodies := make([]*bufio.Reader, 0, tabs)
	for tab := 1; tab <= tabs; tab++ {
		resp := f.watch(t, addr, live.ID) //nolint:bodyclose // watch closes it in t.Cleanup, which the linter cannot see through.
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("tab %d answered %d; want %d", tab, resp.StatusCode, http.StatusOK)
		}
		// Read before the clock starts, so every stream is genuinely being served
		// when shutdown begins rather than merely requested.
		body := bufio.NewReader(resp.Body)
		readScreen(t, body, opened, screen)
		bodies = append(bodies, body)
	}

	began := time.Now()
	if err := f.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown() with %d streams open = %v; want a clean stop — a drain that waits out an endless response reports its own deadline",
			tabs, err)
	}
	// Half the budget is slack rather than a requirement: streams are ended before
	// the drain begins, so what is left for it is the six short routes it was
	// sized for. Anything near the whole budget is a drain that waited for a
	// response that was never going to finish.
	if took := time.Since(began); took > shutdownDrain/2 {
		t.Errorf("shutdown with %d streams open took %v; the whole drain budget is %v and the verified teardown waits behind it",
			tabs, took.Round(time.Millisecond), shutdownDrain)
	}

	// No farewell is asserted. contracts/stream.md declines to promise one at
	// shutdown — the daemon is racing its own service manager — and what the
	// client relies on instead is the reconnect: to a live daemon it
	// re-authorises, and to a dead one it fails visibly.
	for _, body := range bodies {
		assertStreamStoppedAtShutdown(t, body)
	}
}

// --- T029: the US2 acceptance suite -------------------------------------------
//
// The story's own claims rather than a handler's, made the way an operator would
// meet them: over a socket, against the number the daemon was configured with,
// and read back out of the daemon's own records. Each test below is one of the
// five things T029 names — the cap past CRSW_MAX_STREAMS, two tabs on one
// session, a browser that vanished, a screen that reaches no record, and the
// refusal a hostile page earns — plus the one claim the tasks above could only
// make structurally: that a whole stream request leaves exactly one record
// behind.
//
// What they add over the tests above them is the arrangement rather than the
// behaviour. T023 asks each check on its own through a recorder, which is the
// right way to state an ordering and cannot state anything about a daemon that
// is already busy; these ask what happens when several browsers, a configured
// bound, and a session that is printing are all in play at once.

// watchingAsConfigured is a serving fleet with nothing replaced but the tick.
//
// It is the fixture the cap's acceptance test needs and the one no other test
// here wants. watchingWithCap installs a cap of its own so that a boundary can
// be reached with one connection, which is the right trade for a test of the
// boundary and the wrong one for a test of the *number*: a fixture that injects
// the limit cannot tell a daemon enforcing CRSW_MAX_STREAMS from one enforcing
// whatever a test handed it.
func watchingAsConfigured(t *testing.T) (*fleet, string) {
	t.Helper()

	f := newFleet(t)
	f.http.WriteTimeout = writeDeadlineUnderTest
	f.streamTick = tickUnderTest
	return f, serve(t, f)
}

// awaitRecord polls the trail until a record the caller recognises appears.
//
// Polling rather than reading once, because the records this suite waits for are
// written by the middleware *after* the handler returned, and a response in the
// test's hand does not order that: the daemon writes it on net/http's own
// goroutine some microseconds later. A record that never comes fails on the
// budget rather than hanging.
func awaitRecord(t *testing.T, f *fleet, what string, match func(map[string]any) bool) map[string]any {
	t.Helper()

	for deadline := time.Now().Add(streamTestBudget); ; {
		for _, rec := range f.records(t) {
			if match(rec) {
				return rec
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("no record of %s reached the trail within %v; the trail holds:\n%s", what, streamTestBudget, f.sink.String())
		}
		time.Sleep(tickUnderTest)
	}
}

// awaitWatchers polls until a session's screen buffer is shared by as many
// streams as the caller expects.
//
// A stream gives up its share when its handler unwinds, which happens on
// net/http's goroutine some time after the browser went away — there is no event
// a test can wait on for it, exactly as there is none for the cap's slot.
func awaitWatchers(t *testing.T, f *fleet, id string, want int) {
	t.Helper()

	for deadline := time.Now().Add(streamTestBudget); ; {
		got := f.panes.watching(id)
		if got == want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("the screen of session %s is shared by %d streams; want %d", id, got, want)
		}
		time.Sleep(tickUnderTest)
	}
}

// awaitScreenDropped polls until the daemon is no longer holding a session's
// screen at all.
//
// It is a different question from awaitWatchers, and the difference is the one
// that matters here: a buffer nobody is watching and a buffer that is gone are
// the same count and are not the same daemon. The first is a session's output
// kept in memory for nobody, which is the material docs/security.md §3 says a
// long-running daemon must not accumulate.
func awaitScreenDropped(t *testing.T, f *fleet, id string) {
	t.Helper()

	for deadline := time.Now().Add(streamTestBudget); ; {
		if !f.panes.holds(id) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("the daemon was still holding the screen of session %s %v after its last watcher went away", id, streamTestBudget)
		}
		time.Sleep(tickUnderTest)
	}
}

// awaitOpenSlot keeps asking for a stream until the daemon has room for one.
//
// The room comes back on the daemon's own initiative, whenever it notices the
// browser that held the slot is gone. Refusals along the way are the expected
// answer; anything else fails at once, so a daemon refusing for some other
// reason is not waited out for the whole budget.
func awaitOpenSlot(t *testing.T, f *fleet, addr, id string) *http.Response {
	t.Helper()

	for deadline := time.Now().Add(streamTestBudget); ; {
		resp := f.watch(t, addr, id) //nolint:bodyclose // watch closes it in t.Cleanup, which the linter cannot see through.
		if resp.StatusCode == http.StatusOK {
			return resp
		}
		if resp.StatusCode != http.StatusTooManyRequests {
			t.Fatalf("an open answered %d; want either a stream or the cap's %d", resp.StatusCode, http.StatusTooManyRequests)
		}
		if time.Now().After(deadline) {
			t.Fatalf("the slot the departed browser held was still held %v later", streamTestBudget)
		}
		time.Sleep(tickUnderTest)
	}
}

// TestEveryOpenPastTheConfiguredCapIsRefusedAndTheRestCarryOn is FR-034e as an
// operator meets it: the daemon admits exactly the number CRSW_MAX_STREAMS
// names, refuses the next, and neither closes nor disturbs the ones already
// running.
//
// The number is the configured one rather than a fixture's, which is the whole
// reason this test exists beside the cap tests above it. Those replace the cap
// to reach the boundary with one connection; this opens as many streams as the
// daemon was configured for, so a cap wired to the wrong number — or to nothing
// — fails here and passes there.
//
// "The rest carry on" is the half a boundary test cannot state, and it is the
// failure that would be worst in practice: a cap that made room by ending the
// stream somebody was reading would look, from the tab that was evicted, exactly
// like the session going quiet.
func TestEveryOpenPastTheConfiguredCapIsRefusedAndTheRestCarryOn(t *testing.T) {
	t.Parallel()

	const screen = "$ "

	f, addr := watchingAsConfigured(t)
	limit := f.cfg.MaxStreams
	if limit < 2 {
		t.Fatalf("the daemon is configured for %d streams; this test needs one that admits more than a single tab", limit)
	}

	live, _ := f.fixture.plant(t, session.Session{Name: "watch me", WorkDir: f.fixture.repo})
	f.fixture.tmux.SetPane(live.TmuxName(), screen)

	opened := time.Now()
	tabs := make([]*http.Response, 0, limit)
	bodies := make([]*bufio.Reader, 0, limit)
	for tab := 1; tab <= limit; tab++ {
		resp := f.watch(t, addr, live.ID) //nolint:bodyclose // watch closes it in t.Cleanup, which the linter cannot see through.
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("open %d of a daemon configured for %d streams answered %d; want %d",
				tab, limit, resp.StatusCode, http.StatusOK)
		}
		// Read before the next open, so every stream counted is one the daemon is
		// genuinely serving rather than one it has merely accepted.
		body := bufio.NewReader(resp.Body)
		readScreen(t, body, opened, screen)
		tabs = append(tabs, resp) //nolint:bodyclose // watch closes it in t.Cleanup, which the linter cannot see through.
		bodies = append(bodies, body)
	}

	past := f.watch(t, addr, live.ID) //nolint:bodyclose // watch closes it in t.Cleanup, which the linter cannot see through.
	if past.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("the open past %d streams answered %d; want %d", limit, past.StatusCode, http.StatusTooManyRequests)
	}
	if body, err := io.ReadAll(past.Body); err != nil || len(body) != 0 {
		t.Errorf("the refusal carried %q (read error %v); a stream that was never admitted is a response that never started", body, err)
	}
	refusal := awaitRecord(t, f, "an open refused for want of room", func(rec map[string]any) bool {
		return rec["reason"] == errStreamCapReached.Error()
	})
	if got, want := refusal["session_id"], live.ID; got != want {
		t.Errorf("session_id = %v; want %v — a refusal for want of room still says which session went unwatched", got, want)
	}
	if got, want := refusal["action"], string(audit.ActionStreamOpen); got != want {
		t.Errorf("action = %v; want %v", got, want)
	}

	// Every stream already running is untouched by the refusal.
	for tab, body := range bodies {
		if line := readGroup(t, body, opened); line != heartbeatLine && line != screenLine(t, screen) {
			t.Fatalf("tab %d wrote %q after a later open was refused; the cap refuses the newcomer, never the watcher", tab+1, line)
		}
	}

	// And a slot is a slot: one tab leaves, exactly one open is admitted, and the
	// one after that is refused again.
	if err := tabs[0].Body.Close(); err != nil {
		t.Fatalf("close the first tab: %v", err)
	}
	freed := awaitOpenSlot(t, f, addr, live.ID) //nolint:bodyclose // every response it opens is closed in t.Cleanup, which the linter cannot see through.
	readScreen(t, bufio.NewReader(freed.Body), time.Now(), screen)

	full := f.watch(t, addr, live.ID) //nolint:bodyclose // watch closes it in t.Cleanup, which the linter cannot see through.
	if full.StatusCode != http.StatusTooManyRequests {
		t.Errorf("with the freed slot taken, the next open answered %d; want %d — one browser leaving frees one slot, not the bound",
			full.StatusCode, http.StatusTooManyRequests)
	}
}

// TestTwoTabsWatchOneSessionAndOneLeavingLeavesTheOtherWatching is US2 with the
// operator's real habit in it: the same session open in more than one tab.
//
// The claim above it counts execs; this one is about the tabs. Both must see the
// session change, and — the half nothing else states — one of them closing must
// leave the other exactly as it was. The shared buffer is what makes that a real
// question rather than a trivial one: the two streams do not have a screen each,
// so a release that ran on the wrong count would drop the survivor's buffer with
// the departed tab's.
func TestTwoTabsWatchOneSessionAndOneLeavingLeavesTheOtherWatching(t *testing.T) {
	t.Parallel()

	const (
		prompt   = "$ "
		running  = "$ go test ./...\n"
		finished = "$ go test ./...\nok\tinternal/httpapi\t5.7s\n"
	)

	f, addr := watching(t)
	live, _ := f.fixture.plant(t, session.Session{Name: "watch me", WorkDir: f.fixture.repo})
	f.fixture.tmux.SetPane(live.TmuxName(), prompt)

	opened := time.Now()
	first := f.watch(t, addr, live.ID)  //nolint:bodyclose // watch closes it in t.Cleanup, which the linter cannot see through.
	second := f.watch(t, addr, live.ID) //nolint:bodyclose // watch closes it in t.Cleanup, which the linter cannot see through.
	for name, resp := range map[string]*http.Response{"the first tab": first, "the second tab": second} {
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("%s answered %d; want %d", name, resp.StatusCode, http.StatusOK)
		}
	}

	firstBody, secondBody := bufio.NewReader(first.Body), bufio.NewReader(second.Body)
	readScreen(t, firstBody, opened, prompt)
	readScreen(t, secondBody, opened, prompt)

	f.fixture.tmux.SetPane(live.TmuxName(), running)
	awaitScreen(t, firstBody, opened, running)
	awaitScreen(t, secondBody, opened, running)

	if watchers := f.panes.watching(live.ID); watchers != 2 {
		t.Fatalf("two tabs on one session share the screen between %d streams; want the two that are watching it", watchers)
	}

	// One tab goes away, and the other is still watching a session that is still
	// printing. A survivor left with a buffer nobody refills would keep its
	// connection, keep its heartbeat, and never show another screen — which is
	// the failure that looks least like a failure.
	if err := first.Body.Close(); err != nil {
		t.Fatalf("close the first tab: %v", err)
	}
	awaitWatchers(t, f, live.ID, 1)
	if !f.panes.holds(live.ID) {
		t.Fatal("one tab leaving dropped the screen the other is still watching")
	}

	f.fixture.tmux.SetPane(live.TmuxName(), finished)
	awaitScreen(t, secondBody, opened, finished)
}

// TestABrowserThatVanishedGivesBackItsSlotAndItsScreen is the edge case the spec
// names: a browser that goes away without closing anything.
//
// The connection is destroyed rather than closed — SetLinger(0) makes the close
// an RST instead of a FIN — which is a laptop that slept, a network that went, or
// a process that was killed, and is the case a polite `Close` cannot stand in
// for. Nothing tells the daemon; it has to notice.
//
// Both halves of noticing are asserted, because they are held by different code
// and leak different things. The cap's slot is an admission that never comes back
// — a daemon that eventually refuses everybody — and the shared buffer is a
// session's screen, which is secret under docs/security.md §3 and must not
// accumulate in a daemon that runs for weeks.
func TestABrowserThatVanishedGivesBackItsSlotAndItsScreen(t *testing.T) {
	t.Parallel()

	const screen = "$ "

	f, addr := watchingWithCap(t, 1)
	live, _ := f.fixture.plant(t, session.Session{Name: "watch me", WorkDir: f.fixture.repo})
	f.fixture.tmux.SetPane(live.TmuxName(), screen)

	opened := time.Now()
	resp, vanish := f.watchThatCanVanish(t, addr, live.ID) //nolint:bodyclose // watchThatCanVanish closes it in t.Cleanup, which the linter cannot see through.
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /sessions/%s/stream = %d; want %d", live.ID, resp.StatusCode, http.StatusOK)
	}
	readScreen(t, bufio.NewReader(resp.Body), opened, screen)
	if watchers := f.panes.watching(live.ID); watchers != 1 {
		t.Fatalf("the session being watched is shared by %d streams; want the one that is watching it", watchers)
	}

	vanish()

	awaitScreenDropped(t, f, live.ID)
	again := awaitOpenSlot(t, f, addr, live.ID) //nolint:bodyclose // every response it opens is closed in t.Cleanup, which the linter cannot see through.
	readScreen(t, bufio.NewReader(again.Body), time.Now(), screen)
}

// watchThatCanVanish opens a stream on a connection the test keeps a handle on,
// and hands back the way to destroy it.
//
// The handle is the point. Closing a response body is a browser closing a tab —
// the daemon is told, through the request context — and the case this fixture
// exists for is the one where it is told nothing at all. SetLinger(0) turns the
// close into a reset, so the connection ends the way a vanished peer's does
// rather than the way a departing one's does.
func (f *fleet) watchThatCanVanish(t *testing.T, addr, id string) (*http.Response, func()) {
	t.Helper()

	var (
		mu   sync.Mutex
		conn *net.TCPConn
	)
	transport := &http.Transport{
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			c, err := (&net.Dialer{}).DialContext(ctx, network, address)
			if err != nil {
				return nil, err
			}
			tcp, ok := c.(*net.TCPConn)
			if !ok {
				return nil, fmt.Errorf("the fixture dialled a %T; want a connection it can destroy", c)
			}
			mu.Lock()
			defer mu.Unlock()
			conn = tcp
			return tcp, nil
		},
	}
	t.Cleanup(transport.CloseIdleConnections)

	target := "http://" + addr + "/sessions/" + id + "/stream"
	r, err := http.NewRequestWithContext(context.Background(), http.MethodGet, target, nil)
	if err != nil {
		t.Fatalf("build the stream request: %v", err)
	}
	r.Header.Set(headerAccessAssertion, f.keys.mint(t, f.keys.claims()))

	resp, err := (&http.Client{Transport: transport, Timeout: streamTestBudget}).Do(r)
	if err != nil {
		t.Fatalf("GET %s: %v", target, err)
	}
	t.Cleanup(func() {
		// Logged rather than failed: by the time this runs the connection under it
		// has been destroyed on purpose, and a client half that objects to being
		// closed afterwards is not what the test is about.
		if err := resp.Body.Close(); err != nil {
			t.Logf("closing the stream of a destroyed connection: %v", err)
		}
	})

	return resp, func() {
		mu.Lock()
		defer mu.Unlock()

		if conn == nil {
			t.Fatal("the fixture never captured a connection, so nothing here vanished")
		}
		if err := conn.SetLinger(0); err != nil {
			t.Fatalf("make the close a reset rather than a graceful shutdown: %v", err)
		}
		if err := conn.Close(); err != nil {
			t.Fatalf("destroy the browser's connection: %v", err)
		}
	}
}

// deadPeer is a browser that went away without closing: a response its writes
// fail against from a chosen write onwards.
//
// It can lift a write deadline, which is what a ResponseRecorder cannot do and
// what makes openStream here the production one rather than a stand-in.
type deadPeer struct {
	header http.Header

	// written is every line group this peer was handed, so a claim can be made
	// about *which* write found the failure and not only that one did.
	written []string
	failAt  int
	gone    error
}

func (p *deadPeer) Header() http.Header {
	if p.header == nil {
		p.header = make(http.Header)
	}
	return p.header
}

func (p *deadPeer) WriteHeader(int) {}
func (p *deadPeer) Flush()          {}

func (p *deadPeer) SetWriteDeadline(time.Time) error { return nil }

func (p *deadPeer) Write(b []byte) (int, error) {
	p.written = append(p.written, string(b))
	if len(p.written) >= p.failAt {
		return 0, p.gone
	}
	return len(b), nil
}

// TestAQuietStreamStillFindsAPeerThatWentAway is why the heartbeat exists, said
// where it can actually be said.
//
// The session here prints nothing, ever — which is what a Claude session at a
// prompt does for minutes at a time, and which the suppression rule turns into a
// stream with no events to write. Without a write per tick that stream would be
// silent, and a browser that vanished behind it would hold its slot and its
// share of a session's screen for as long as the daemon ran, because nothing
// would ever fail.
//
// So the assertion is about the write that found the peer: it is the comment a
// suppressed tick makes, and the stream stops on it.
func TestAQuietStreamStillFindsAPeerThatWentAway(t *testing.T) {
	t.Parallel()

	const screen = "$ "

	// The second write is the first heartbeat: the opening screen goes out, and
	// then nothing changes for as long as this runs.
	gone := errors.New("connection reset by peer")
	peer := &deadPeer{failAt: 2, gone: gone}

	sse, err := openStream(peer)
	if err != nil {
		t.Fatalf("openStream = _, %v; want a stream", err)
	}

	// A backstop rather than a deadline anything is measured against: a hold that
	// never noticed would otherwise write heartbeats until the package timed out,
	// which is a failure that says nothing.
	ctx, cancel := context.WithTimeout(context.Background(), streamTestBudget)
	defer cancel()

	held := sse.hold(ctx, tickUnderTest, nil, func(context.Context) (string, error) { return screen, nil })
	if ctx.Err() != nil {
		t.Fatal("the stream ran until the test's own budget expired; a browser that vanished was never noticed")
	}
	if !errors.Is(held, gone) {
		t.Fatalf("hold() = %v; want the write failure that says nobody is at the other end", held)
	}
	if len(peer.written) != peer.failAt {
		t.Errorf("the stream made %d writes; want %d — a write that failed is a connection with nobody on it, and nothing follows it",
			len(peer.written), peer.failAt)
	}
	if last := peer.written[len(peer.written)-1]; last != string(heartbeat) {
		t.Errorf("the write that found the peer was %q; want the heartbeat — a screen that never changes is written to by nothing else, which is the whole reason a quiet tick still writes",
			last)
	}
}

// TestOutputWithMarkupAndEscapesArrivesAsTextAndReachesNoRecord is US2's own
// independent test and FR-035 in one exchange: the distinctive output, watched
// live, and then the trail read back for any trace of it.
//
// The screen is what a session with something to gain would print — a terminal
// escape, a script element, and a line spelling this transport's own framing —
// and three claims are made about it at once. It arrives with the escapes gone
// (FR-029, scenario 3), it arrives with the markup intact as characters rather
// than as tags (scenario 2, and the client's textContent is the other half), and
// none of it is anywhere in what the daemon wrote about it.
//
// The trail is asked while the daemon has every reason to have written it down:
// a record was emitted for this very stream, a capture then failed and was
// reported, and the failure happened with the last good screen still in the
// shared buffer.
func TestOutputWithMarkupAndEscapesArrivesAsTextAndReachesNoRecord(t *testing.T) {
	t.Parallel()

	const (
		printed = "$ cat exploit.html\n\x1b[31m<script>alert('pwned')</script>\x1b[0m\ndata: \"not an event\"\n$ "
		text    = "$ cat exploit.html\n<script>alert('pwned')</script>\ndata: \"not an event\"\n$ "
		canary  = "pwned"
	)

	f := watchingUnserved(t, config.DefaultMaxStreams)
	live, _ := f.fixture.plant(t, session.Session{Name: "watch me", WorkDir: f.fixture.repo})
	f.fixture.tmux.SetPane(live.TmuxName(), printed)

	// The fixture's own recorder appends without a lock, which is not safe while
	// a stream's ticks are running. Installed before Serve, for the reason
	// watchingUnserved exists.
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

	opened := time.Now()
	resp := f.watch(t, addr, live.ID) //nolint:bodyclose // watch closes it in t.Cleanup, which the linter cannot see through.
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /sessions/%s/stream = %d; want %d", live.ID, resp.StatusCode, http.StatusOK)
	}

	body := bufio.NewReader(resp.Body)
	got := decodeScreen(t, body, opened)
	if got != text {
		t.Errorf("the wire carried %q; want the screen as text, with the escapes stripped where the capture is read:\n%q", got, text)
	}
	if strings.ContainsRune(got, 0x1b) {
		t.Errorf("an escape sequence reached the browser: %q", got)
	}

	// A capture that fails while that screen is the newest thing the daemon holds,
	// which is the moment a report is most tempted to say what it could not read.
	f.fixture.tmux.FailOp(tmuxctl.OpCapturePane, errors.New("tmux is not answering"))
	for tick := 1; tick <= ticksWatched; tick++ {
		readGroup(t, body, opened)
	}
	if err := resp.Body.Close(); err != nil {
		t.Fatalf("close the stream: %v", err)
	}

	// The record really was written about this stream, so the silence below is a
	// daemon that recorded the open without recording the screen rather than a
	// daemon that recorded nothing.
	rec := awaitRecord(t, f, "the stream that was opened", func(rec map[string]any) bool {
		return rec["action"] == string(audit.ActionStreamOpen)
	})
	if got, want := rec["decision"], string(audit.Allow); got != want {
		t.Errorf("decision = %v; want %v", got, want)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(failures) == 0 {
		t.Fatal("the failing capture was never reported, so nothing here proves a report carries no screen")
	}

	written := []string{f.sink.String()}
	for _, err := range failures {
		written = append(written, err.Error())
	}
	for _, line := range written {
		for what, secret := range map[string]string{
			"the screen as the session printed it":   printed,
			"the screen as the browser received it":  text,
			"a fragment of what the session printed": canary,
		} {
			if strings.Contains(line, secret) {
				t.Errorf("%s reached what the daemon wrote about the stream (FR-035):\n%s", what, line)
			}
		}
	}
}

// TestTheOpenAHostilePageTriggersIsRefusedWhereTheDashboardsIsServed is FR-034d
// as the attack it exists for, over the wire.
//
// The two requests differ by one header and nothing else. Both carry the
// operator's own valid identity, because that is the situation: the credential
// on this door is an ambient cookie, so a page the operator has never seen can
// trigger a request the edge turns into a perfectly good assertion. What tells
// the two apart is the browser's own account of where the request came from.
//
// The refusal is checked for what it is not as much as for what it is. A hostile
// page learns nothing from a uniform 401; it would learn that the id resolves —
// and could then read the screen — from a response that declared itself an event
// stream.
func TestTheOpenAHostilePageTriggersIsRefusedWhereTheDashboardsIsServed(t *testing.T) {
	t.Parallel()

	const screen = "$ ssh production\n"

	f, addr := watching(t)
	live, _ := f.fixture.plant(t, session.Session{Name: "watch me", WorkDir: f.fixture.repo})
	f.fixture.tmux.SetPane(live.TmuxName(), screen)

	hostile := f.watchFrom(t, addr, live.ID, "cross-site") //nolint:bodyclose // watchFrom closes it in t.Cleanup, which the linter cannot see through.
	if hostile.StatusCode != http.StatusUnauthorized {
		t.Fatalf("an open the browser called cross-site answered %d; want %d", hostile.StatusCode, http.StatusUnauthorized)
	}
	if got := hostile.Header.Get(headerContentType); got == contentTypeEventStream {
		t.Errorf("the refusal declared itself as %q, which says the id resolved to something", got)
	}
	refused, err := io.ReadAll(hostile.Body)
	if err != nil {
		t.Fatalf("read the refusal: %v", err)
	}
	if string(refused) != string(bodyBrowserRefused) {
		t.Errorf("the refusal answered %q; want the browser door's one refusal", refused)
	}
	if strings.Contains(string(refused), screen) || strings.Contains(string(refused), live.ID) {
		t.Errorf("the refusal names what it refused:\n%s", refused)
	}
	rec := awaitRecord(t, f, "the open refused as cross-site", func(rec map[string]any) bool {
		return rec["reason"] == errStreamCrossSite.Error()
	})
	if got, want := rec["decision"], string(audit.Deny); got != want {
		t.Errorf("decision = %v; want %v", got, want)
	}

	// The dashboard's own open, on the same daemon and the same credential. It is
	// the non-vacuity this test needs: without it, a route that refused every
	// stream would pass.
	opened := time.Now()
	served := f.watchFrom(t, addr, live.ID, secFetchSiteSameOrigin) //nolint:bodyclose // watchFrom closes it in t.Cleanup, which the linter cannot see through.
	if served.StatusCode != http.StatusOK {
		t.Fatalf("the dashboard's own open answered %d; want %d", served.StatusCode, http.StatusOK)
	}
	readScreen(t, bufio.NewReader(served.Body), opened, screen)
}

// TestOneStreamRequestLeavesExactlyOneRecordBehind is SC-008 on the one route
// where it was previously only structural.
//
// The record is written at the open (FR-016a) and the middleware's deferred emit
// runs when the handler unwinds, which for a stream is hours later and on
// net/http's own goroutine — so a test that read the trail while the stream was
// open could only ever say "no second record has appeared *yet*". What is needed
// is a moment that is provably after the handler returned, and Shutdown is one:
// it drains, and a connection is not drained until the whole handler chain that
// was serving it — the deferred emit included — has finished.
//
// The count is of this route's own action rather than of every line, because
// shutdown tears the fixture's session down behind the drain and says so.
func TestOneStreamRequestLeavesExactlyOneRecordBehind(t *testing.T) {
	t.Parallel()

	const screen = "$ "

	f, addr := watching(t)
	live, _ := f.fixture.plant(t, session.Session{Name: "watch me", WorkDir: f.fixture.repo})
	f.fixture.tmux.SetPane(live.TmuxName(), screen)

	opened := time.Now()
	resp := f.watch(t, addr, live.ID) //nolint:bodyclose // watch closes it in t.Cleanup, which the linter cannot see through.
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /sessions/%s/stream = %d; want %d", live.ID, resp.StatusCode, http.StatusOK)
	}
	body := bufio.NewReader(resp.Body)
	readScreen(t, body, opened, screen)

	if records := f.records(t); len(records) != 1 {
		t.Fatalf("a stream that is still open has emitted %d records (%v); want the one written at the open", len(records), records)
	}

	// The browser goes away, and then the daemon stops.
	if err := resp.Body.Close(); err != nil {
		t.Fatalf("close the stream: %v", err)
	}
	if err := f.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown() = %v; want a clean stop", err)
	}

	opens := 0
	for _, rec := range f.records(t) {
		if rec["action"] == string(audit.ActionStreamOpen) {
			opens++
		}
	}
	if opens != 1 {
		t.Errorf("one stream request left %d %s records behind; want exactly one — the close carries no authorisation fact the open did not, and a pair would make this the one door where FR-041 is false",
			opens, audit.ActionStreamOpen)
	}
}

// TestScreenEventRefusesAnOversizedScreen pins the bound screenEvent's
// allocation relies on.
//
// It is not defending against a large pane — capture-pane takes the visible
// screen, which cannot approach this size. It is defending against that stopping
// being true: adding -S upstream would capture scrollback instead, and every
// caller sizing a buffer from the result would go on assuming a bound that no
// longer held (issue #41).
//
// The refusal, rather than truncation, is the point. send() does not record an
// unframed screen as sent, so a refused screen is retried on the next capture
// instead of leaving the stream quiet on a stale one.
func TestScreenEventRefusesAnOversizedScreen(t *testing.T) {
	t.Parallel()

	if _, err := screenEvent(strings.Repeat("x", 64)); err != nil {
		t.Fatalf("screenEvent(small screen) = %v; want it framed", err)
	}

	oversized := strings.Repeat("x", maxScreenBytes+1)
	_, err := screenEvent(oversized)
	if !errors.Is(err, errScreenTooLarge) {
		t.Fatalf("screenEvent(oversized) = %v; want %v", err, errScreenTooLarge)
	}

	// The screen itself must not travel in the error, however large (FR-042).
	if strings.Contains(err.Error(), "xxxxxxxx") {
		t.Error("the refusal carries pane content; only its size may be named")
	}
}
