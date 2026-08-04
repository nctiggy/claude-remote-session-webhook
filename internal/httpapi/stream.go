package httpapi

// stream.go is the live output stream — GET /sessions/{id}/stream on the browser
// door (contracts/stream.md): the four checks that admit one, and the SSE
// response they admit it to.
//
// The four are ordered, and their order is a guarantee rather than a style —
// sessionStream carries the reason each of them is where it is, and the whole of
// it is that no caller who was going to be refused anyway learns anything about
// this host on the way to being refused.
//
// The response's own subject is the deadline. Milestone 1's server carries a 30-second
// WriteTimeout for six routes that are each one tmux exec or one map read, and a
// response deliberately without an end cannot live under it — server.go has
// carried a note saying so since before this milestone existed. The answer is
// per-response and standard library (research D3): this handler lifts its own
// deadline and the six keep theirs. Setting WriteTimeout: 0 on the server would
// be the same line of code and would take the protection off every route that
// still wants it.
//
// What a stream carries is deliberately not here yet. A stream opened by this
// file writes heartbeat comments and nothing else: the capture loop is T024 and
// the framing that makes a payload's shape independent of its content is T025,
// so no byte a session printed crosses this transport before the task that makes
// crossing it safe has landed. What *is* here is everything a stream that
// carried a screen would need to already be true — the four-step open sequence
// in front of it, the response's own description of itself, and the deadline.
//
// One thing this route still owes contracts/stream.md, owned by a later task
// and not a disclosure while the stream carries no output: the record emitted at
// open rather than at close, which is T027's — until then a stream's one audit
// record lands when the connection ends, which is exactly milestone 1's
// behaviour and exactly what FR-016a exists to change.

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"
)

// patternSessionStream is the stream's route, spelled through pathValueID like
// every other wildcard in this package so the name in the pattern and the name
// read back out of the request cannot drift.
//
// It is registered on the browser door and is deliberately not in the `routes`
// table: that table is contracts/http-api.md's closed set of six operations,
// each authorised by a signature, and this is authorised by a verified identity
// and an ownership check instead (FR-034a). The URL carries the session ID and
// no credential — a browser cannot hold one, and the two places it could keep
// one are the URL and a script the page can read, neither of which may hold the
// key to an unsandboxed shell.
const patternSessionStream = "GET /sessions/{" + pathValueID + "}/stream"

// contentTypeEventStream is what an SSE response declares itself as. The
// charset is spelled out for the reason it is on the HTML pages: with `nosniff`
// on every browser-door response a browser believes what it is told rather than
// guessing.
const contentTypeEventStream = "text/event-stream; charset=utf-8"

// streamInterval is the cadence contracts/stream.md fixes: one write per stream
// per second. It sits under the lag a human notices and bounds the work a
// watched session costs the host to one exec per second no matter how many tabs
// are open.
const streamInterval = time.Second

// headerSecFetchSite is the browser's own account of where a request came from,
// and secFetchSiteSameOrigin is the one value that admits a stream (FR-034d,
// research D8). See crossSite for why it is read this way round.
const (
	headerSecFetchSite     = "Sec-Fetch-Site"
	secFetchSiteSameOrigin = "same-origin"
)

// heartbeat is an SSE comment, which is a line that is not an event and carries
// no data. It is what a tick writes when there is nothing to send.
//
// A quiet stream that wrote nothing at all would be a stream the daemon cannot
// tell from a live one: a browser that vanished without closing the connection
// would hold its slot forever, because nothing would ever fail. One write per
// tick is what turns a dead peer into a write error, and it is also what keeps
// an idle proxy at the edge from severing a stream that is working perfectly.
var heartbeat = []byte(":\n\n")

// The refusals this route records, authored here.
//
// None is ever written into a response — a refused caller gets the door's one
// uniform answer for its step and nothing else — and none is built from a byte
// the caller sent, in particular never the value of Sec-Fetch-Site, which is
// caller-authored text the trail may not carry (FR-042).
var (
	// errStreamNotOpened is what the trail records when a stream could not lift
	// its own write deadline. Like every other reason in this repo it is a
	// constant rather than the wrapped error, which would name a type from
	// whatever wrapped the response.
	errStreamNotOpened = errors.New("the output stream could not be opened")

	// errStreamCrossSite is FR-034d doing its job: the browser itself said this
	// request came from somewhere other than the dashboard.
	//
	// It deliberately does not spell any of the values Sec-Fetch-Site can carry.
	// The reason is a constant authored here and the header is caller-authored
	// text, and a reason that happened to quote one is a reason nobody can tell
	// apart from a reason built from the request.
	errStreamCrossSite = errors.New("the output stream was opened from somewhere other than the dashboard")

	// errStreamCapReached is the concurrency cap doing its job (FR-034e). Like
	// the session cap's refusal it says nothing was wrong with the request, and
	// it belongs in the trail for the same reason: an operator seeing it
	// repeatedly is looking at either tabs nobody closed or a cap set below the
	// way the host is watched.
	errStreamCapReached = errors.New("the concurrent stream cap was reached, so the stream was refused")
)

// sessionStream serves GET /sessions/{id}/stream (contracts/stream.md).
//
// The order is the guarantee, and it is the contract's own: identity, then the
// cross-site refusal, then ownership, then capacity. Nothing about the response
// — not a header, not the deadline, not a byte — happens until all four have
// been answered, so a caller who is refused cannot tell from the wire whether
// the id they asked for exists.
//
// Each step is where it is for a reason the next one cannot supply:
//
//   - Identity first, because nothing session-shaped may be asked before the
//     caller is known. Layer 1 has already run in front of this handler, which
//     is also where a service-token assertion is refused as it is everywhere on
//     this door (FR-013c) — a caller with no email is not a person, and the
//     dashboard serves people.
//   - The cross-site refusal second, before any lookup: it is decided from one
//     header and nothing else, so a request the browser itself calls cross-site
//     never causes a session to be looked up at all.
//   - Ownership third, through Manager.View, which resolves it without
//     advancing the idle clock (FR-034f). Watching is not driving: a tab left
//     open on a session nobody is prompting must not postpone its idle
//     deadline, or a forgotten browser holds an unsandboxed shell alive for as
//     long as it lives.
//   - Capacity **last**, so that no caller who was going to be refused anyway
//     can observe whether the daemon has room (FR-034e). A cap checked first
//     would answer a stranger's probe with the one fact about this host that
//     the three checks above exist to withhold: how much is being watched.
//
// Every ownership failure is the uniform not-found page — an id that never
// existed, one this viewer does not own, and one whose session is already gone
// are one answer (FR-037b, SC-016). Which of them it really was is on the record
// the middleware emits, where the operator can read it.
func (s *Server) sessionStream(w http.ResponseWriter, r *http.Request) {
	operator, ok := OperatorFrom(r.Context())
	if !ok {
		AuditFrom(r.Context()).Deny(errDashboardNoOperator.Error())
		s.refuseBrowser(w)
		return
	}

	if crossSite(r) {
		// The door's own uniform refusal, byte-identical to the one an unverified
		// caller gets. A second shape here would be a shape that varies with
		// something about the request, and the page that triggered this one is not
		// owed the knowledge that its request was recognised for what it was.
		AuditFrom(r.Context()).Deny(errStreamCrossSite.Error())
		s.refuseBrowser(w)
		return
	}

	live, err := s.sessions.View(r.PathValue(pathValueID), operator.Owner)
	if err != nil {
		AuditFrom(r.Context()).Deny(resolveReason(err).Error())
		s.renderNotFound(w, r, operator)
		return
	}
	// The id off the daemon's own record and never the bytes in the path, which
	// is safe here for the reason it is safe on the session page: the record has
	// just been matched to them. It is stamped before the cap is asked, so a
	// refusal for want of room still says which session went unwatched.
	AuditFrom(r.Context()).SetSessionID(live.ID)

	release, admitted := s.streams.admit()
	if !admitted {
		AuditFrom(r.Context()).Deny(errStreamCapReached.Error())
		s.refuseStreamNoRoom(w)
		return
	}
	// Deferred rather than released at each ending, so the slot comes back on
	// every path out of this handler — a failed open, a write against a browser
	// that vanished, a cancelled request, and an unwinding panic alike. A slot
	// leaked by one forgotten return is a cap that shrinks by one for as long as
	// the daemon runs.
	defer release()

	sse, err := openStream(w)
	if err != nil {
		// Refusing to serve is the fail-closed answer and not a pedantic one. A
		// response that could not lift its deadline is a stream that would be cut
		// off mid-screen thirty seconds in, and a viewer watching a session go
		// quiet cannot tell that from a session that went quiet.
		AuditFrom(r.Context()).Deny(errStreamNotOpened.Error())
		s.report(fmt.Errorf("open the output stream for session %s: %w", live.ID, err))
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	if err := sse.hold(r.Context(), s.streamTick); err != nil {
		// A peer that closed cleanly ends through the context and returns no
		// error at all, so what reaches here is a write that failed against a
		// connection nobody closed — the vanished browser the heartbeat exists to
		// find. It is a fact about the host's connections rather than about the
		// request, so it goes where a failure with nowhere else to go goes, and
		// never into the response.
		s.report(fmt.Errorf("write the output stream for session %s: %w", live.ID, err))
	}
}

// crossSite reports whether the browser said this request came from another
// site (FR-034d, research D8).
//
// Present-and-wrong refuses; absent does not. The header is sent by current
// browsers and by nothing else — the quickstart's curl does not send it, and
// neither does any other non-browser client — so requiring it would refuse the
// callers it was never about while adding nothing against the one it is: a
// hostile page triggering a request that the operator's own ambient cookie rides
// on, which the edge then turns into a valid assertion. What has to hold when
// the header is absent is the CORS absence in render.go, which is why this is
// belt-and-braces and that is the protection.
//
// Only same-origin admits, so `same-site` and `none` are refused with
// `cross-site`. `none` is the surprising one: it means a URL typed, bookmarked,
// or otherwise opened by no page at all — and the dashboard opens this address
// from its own page, so a stream nobody's page asked for is not a stream this
// daemon owes anyone.
//
// The comparison is exact rather than case-folded. The Fetch standard spells
// these values as lowercase tokens, so any other spelling is not a value a
// browser sent, and reading an unrecognised one as "not same-origin" is the
// fail-closed direction.
func crossSite(r *http.Request) bool {
	site := r.Header.Get(headerSecFetchSite)
	return site != "" && site != secFetchSiteSameOrigin
}

// refuseStreamNoRoom answers the open that found no slot (FR-034e).
//
// 429 is what milestone 1 answers the session cap and the create rate with, and
// it is the same statement for the same reason: nothing about the request is
// wrong, there is no room. It is deliberately the only response on this door
// that is neither the uniform refusal nor the uniform 404 — those two exist to
// say nothing about the host, and this one says something about the host to a
// caller who has already been verified and has already been found to own the
// session.
//
// It carries no body, like the failed open below it. This door's documents are
// pages for a person; this response is read by the script that opened the
// stream, and a page rendered for it would be a document nobody looks at. What
// was refused is on the record, where the operator reads it.
func (s *Server) refuseStreamNoRoom(w http.ResponseWriter) {
	w.WriteHeader(http.StatusTooManyRequests)
}

// streamCap bounds how many output streams are open at once (FR-034e).
//
// The bound is global rather than per session or per viewer, because what it
// protects is global: every open stream is a long-lived connection doing
// periodic exec work against this host, and unbounded streams are the same local
// denial of service the concurrent-session cap exists to prevent (Principle VI).
//
// It is a type rather than a comparison in the handler for the reason
// Store.AddCapped is a method rather than a Len check in the manager: the count
// and the admission have to be one critical section. Two opens racing the last
// slot would otherwise both read limit-1, both find room, and both be admitted,
// and no caller can close that window because it is between the two calls rather
// than inside either.
type streamCap struct {
	limit int

	mu   sync.Mutex
	open int
}

// newStreamCap fails closed on a limit that would refuse everything.
//
// config.Load already refuses a cap below one, so this is the second check and
// deliberately not the only one: a Config is a struct, and a daemon built from
// one that never went through Load would serve a dashboard whose every session
// links to a stream it will not open — a defect discovered one click at a time
// rather than at startup.
func newStreamCap(limit int) (*streamCap, error) {
	if limit < 1 {
		return nil, fmt.Errorf("httpapi: a stream cap of %d would refuse every stream; refusing to start", limit)
	}
	return &streamCap{limit: limit}, nil
}

// admit takes a slot if there is one, and hands back the release that frees it.
//
// The release is returned rather than being a second method for the same reason
// the count and the take are one critical section: a caller that had to remember
// which of two calls it had made is a caller that can release a slot it never
// took. It runs at most once, whatever a caller does with it — a double release
// would free a slot that is still held, which admits a stream over the cap, and
// that is the failure this type exists to prevent rather than a tidy detail.
func (c *streamCap) admit() (release func(), ok bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.open >= c.limit {
		return nil, false
	}
	c.open++

	var once sync.Once
	return func() { once.Do(c.free) }, true
}

func (c *streamCap) free() {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.open--
}

// stream is one SSE response being written: the writer, and the controller whose
// deadline it lifted.
//
// The controller is kept rather than rebuilt per write because it is the same
// response throughout — and because holding it is what makes "this response's
// deadline was lifted" a property of the value the loop writes through, instead
// of a call some later tick has to remember to have made.
type stream struct {
	w  http.ResponseWriter
	rc *http.ResponseController
}

// openStream turns a response into an SSE stream: it lifts the write deadline,
// declares what the response is, and flushes the head.
//
// The deadline is lifted before a byte is written, deliberately. A writer that
// cannot lift it — anything wrapping the response without an Unwrap, and every
// httptest.ResponseRecorder — then produces a response that never started rather
// than one that is cut off partway through a screen, which is the failure that
// looks like a session going quiet.
//
// The head is flushed because an EventSource does not consider itself open until
// the response arrives, and the first tick may be a second away. Everything
// after it is flushed too (see write): an SSE event sitting in a buffer is an
// update the operator is not seeing.
func openStream(w http.ResponseWriter) (*stream, error) {
	rc := http.NewResponseController(w) //nolint:bodyclose // false positive: a ResponseController is not a response and has no body to close.
	if err := rc.SetWriteDeadline(time.Time{}); err != nil {
		return nil, fmt.Errorf("lift this response's write deadline: %w", err)
	}

	// The security headers and no-store are already on the response —
	// authenticateBrowser writes them before layer 1 decides anything, so a
	// stream and a refusal leave with the identical set. Pane content is secret
	// under docs/security.md §3 and no-store is what keeps it out of every cache
	// between here and the browser.
	w.Header().Set(headerContentType, contentTypeEventStream)
	w.WriteHeader(http.StatusOK)

	s := &stream{w: w, rc: rc}
	if err := s.flush(); err != nil {
		return nil, err
	}
	return s, nil
}

// hold keeps the stream open until the request ends or a write fails, writing
// one heartbeat per tick.
//
// Exactly one write per tick is the invariant contracts/stream.md asks for, and
// the capture loop keeps it by choosing between an event and a comment rather
// than by adding a second write (T024).
//
// A cancelled context is the ordinary ending and not a failure: it is what a
// closed tab, a dropped connection, and a closed server all arrive as, and every
// one of them means this response is over.
func (s *stream) hold(ctx context.Context, every time.Duration) error {
	tick := time.NewTicker(every)
	defer tick.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-tick.C:
			if err := s.write(heartbeat); err != nil {
				return err
			}
		}
	}
}

// write puts one SSE line group on the wire and flushes it.
//
// Failure is returned rather than counted or retried. A stream whose write fails
// is a stream with nobody at the other end, and the connection is the only thing
// a caller of this can do anything about.
func (s *stream) write(b []byte) error {
	if _, err := s.w.Write(b); err != nil {
		return fmt.Errorf("write to the stream: %w", err)
	}
	return s.flush()
}

func (s *stream) flush() error {
	if err := s.rc.Flush(); err != nil {
		return fmt.Errorf("flush the stream: %w", err)
	}
	return nil
}
