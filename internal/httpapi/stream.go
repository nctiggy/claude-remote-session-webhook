package httpapi

// stream.go is the live output stream — GET /sessions/{id}/stream on the browser
// door (contracts/stream.md). This file is its transport: the SSE response, and
// the deadline that response runs under.
//
// The deadline is the whole subject. Milestone 1's server carries a 30-second
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
// carried a screen would need to already be true — the door in front, the
// ownership check, the response's own description of itself, and the deadline.
//
// Three things this route still owes contracts/stream.md, each owned by a later
// task and none of them a disclosure while the stream carries no output: the
// cross-site refusal and the concurrency cap, both T023's (the cap bounds a cost
// the capture loop has not created yet), and the record emitted at open rather
// than at close, which is T027's — until then a stream's one audit record lands
// when the connection ends, which is exactly milestone 1's behaviour and exactly
// what FR-016a exists to change.

import (
	"context"
	"errors"
	"fmt"
	"net/http"
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

// heartbeat is an SSE comment, which is a line that is not an event and carries
// no data. It is what a tick writes when there is nothing to send.
//
// A quiet stream that wrote nothing at all would be a stream the daemon cannot
// tell from a live one: a browser that vanished without closing the connection
// would hold its slot forever, because nothing would ever fail. One write per
// tick is what turns a dead peer into a write error, and it is also what keeps
// an idle proxy at the edge from severing a stream that is working perfectly.
var heartbeat = []byte(":\n\n")

// errStreamNotOpened is what the trail records when a stream could not lift its
// own write deadline. Like every other reason in this repo it is a constant
// authored here rather than the wrapped error, which would name a type from
// whatever wrapped the response (FR-042).
var errStreamNotOpened = errors.New("the output stream could not be opened")

// sessionStream serves GET /sessions/{id}/stream (contracts/stream.md).
//
// The order is the guarantee, and it is the same order the session page uses
// because it is answering the same question: who is this, and is the session
// theirs. Nothing about the response — not a header, not the deadline, not a
// byte — happens before both are answered, so a caller who is refused cannot
// tell from the wire whether the id they asked for exists.
//
// The read is Manager.View, which resolves ownership without advancing the idle
// clock (FR-034f). Watching is not driving: a tab left open on a session nobody
// is prompting must not postpone its idle deadline, or a forgotten browser holds
// an unsandboxed shell alive for as long as it lives.
//
// Every failure is the uniform not-found page — an id that never existed, one
// this viewer does not own, and one whose session is already gone are one answer
// (FR-037b, SC-016). Which of them it really was is on the record the middleware
// emits, where the operator can read it.
func (s *Server) sessionStream(w http.ResponseWriter, r *http.Request) {
	operator, ok := OperatorFrom(r.Context())
	if !ok {
		AuditFrom(r.Context()).Deny(errDashboardNoOperator.Error())
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
	// just been matched to them.
	AuditFrom(r.Context()).SetSessionID(live.ID)

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
