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
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/nctiggy/claude-remote-session-webhook/internal/audit"
	"github.com/nctiggy/claude-remote-session-webhook/internal/auth"
	"github.com/nctiggy/claude-remote-session-webhook/internal/session"
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
)

// watching is a fleet serving on a real loopback socket, with the write deadline
// and the tick shortened, and its address.
func watching(t *testing.T) (*fleet, string) {
	t.Helper()

	f := newFleet(t)
	f.http.WriteTimeout = writeDeadlineUnderTest
	f.streamTick = tickUnderTest

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

	return f, f.Addr().String()
}

// watch opens one session's stream as the verified operator would: the identity
// assertion in a header, no signature, and no credential anywhere in the URL.
func (f *fleet) watch(t *testing.T, addr, id string) *http.Response {
	t.Helper()

	target := "http://" + addr + "/sessions/" + id + "/stream"
	r, err := http.NewRequestWithContext(context.Background(), http.MethodGet, target, nil)
	if err != nil {
		t.Fatalf("build the stream request: %v", err)
	}
	r.Header.Set(headerAccessAssertion, f.keys.mint(t, f.keys.claims()))

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

// readHeartbeat reads one whole SSE comment off the wire — the `:` line and the
// blank line that ends the group — failing the test if the stream stopped
// instead.
//
// It asserts the wire format rather than the constant behind it (a comment is a
// line beginning `:`, and every line group ends with a blank line;
// contracts/stream.md). Reading both lines is what keeps the count honest: a
// caller asking for three heartbeats must not be handed one comment and its own
// terminator. Anything else arriving is a failure of its own — this transport
// carries heartbeats and nothing else until the framing task lands, and a screen
// appearing before then would be a screen framed by nobody.
func readHeartbeat(t *testing.T, body *bufio.Reader, opened time.Time) {
	t.Helper()

	if line := readStreamLine(t, body, opened); line != ":\n" {
		t.Fatalf("the stream wrote %q; this transport carries heartbeats and nothing else", line)
	}
	if line := readStreamLine(t, body, opened); line != "\n" {
		t.Fatalf("the heartbeat was followed by %q; an SSE line group ends with a blank line", line)
	}
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
