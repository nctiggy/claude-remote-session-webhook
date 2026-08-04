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
// *When* a stream writes is here; *what* it writes is not, and the gap is
// deliberate. The capture loop below reads each watched session once per tick
// into a buffer every stream on that session shares, and writes an event only
// when the screen differs from the one that stream last sent — but the event's
// payload is a placeholder rather than the screen. Framing is T025: SSE's wire
// format is line-oriented, so a screen put into a `data:` field before the
// encoding that makes framing independent of content has landed is a screen
// framed by nobody. Until it lands no byte a session printed crosses this
// transport, and the buffer that task will marshal is already here.
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

	"github.com/nctiggy/claude-remote-session-webhook/internal/session"
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

// screenChanged is what a tick writes instead of the heartbeat when the capture
// differs from the screen that stream last sent.
//
// **The payload is a placeholder and not the screen**, which is the one thing
// about this constant worth reading twice. What decides *when* an event is
// written is here; what an event *carries* is T025, because SSE's wire format is
// line-oriented and a screen is inherently multi-line — a raw newline inside a
// `data:` field starts a new field, a lone `\r` is corrupted in the rejoin, and
// a line the session printed beginning `event:` would be a session choosing this
// daemon's framing. The encoding that makes all three impossible is one
// json.Marshal, and it belongs to the task that also proves it against exactly
// those payloads. Until then the buffer below holds the screen and the wire does
// not.
//
// It is an unnamed event, like the one contracts/stream.md frames, so that
// landing the framing changes the payload and nothing else. An empty `data:`
// field would have said the same thing with no placeholder at all, and is not an
// option: EventSource drops an event whose data buffer is empty rather than
// dispatching it, so a stream written that way would be a stream no browser
// hears.
var screenChanged = []byte("data: changed\n\n")

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

	// The shared buffer, taken after the response is a stream so that an open
	// which never became one neither creates one nor drops one. Deferred for the
	// reason the slot is: a buffer left behind by one forgotten return is a
	// session's screen held in memory for as long as the daemon runs, and pane
	// content is secret under docs/security.md §3.
	shared, unwatch := s.panes.attach(live.ID)
	defer unwatch()

	if err := sse.hold(r.Context(), s.streamTick, s.reader(live, shared)); err != nil {
		// A peer that closed cleanly ends through the context and returns no
		// error at all, so what reaches here is a write that failed against a
		// connection nobody closed — the vanished browser the heartbeat exists to
		// find. It is a fact about the host's connections rather than about the
		// request, so it goes where a failure with nowhere else to go goes, and
		// never into the response.
		s.report(fmt.Errorf("write the output stream for session %s: %w", live.ID, err))
	}
}

// reader is what one held stream reads its screen from: the buffer shared by
// every stream watching this session, the capture that fills it, and the single
// report an outage earns.
//
// The capture goes through Manager.Output rather than the controller, which is
// what keeps one stripper rather than a second that agrees today (FR-029) — and
// what keeps the stream off the idle clock, since Output takes the record it was
// given and reaches no store (FR-034f). Watching is not driving, on every tick
// and not only at the open.
//
// Failures are reported once per outage rather than once per tick. A session
// whose window has gone answers every capture the same way, and a stream
// reporting each of them fills an operator's journal at one line a second for as
// long as the tab stays open — which buries the first line, the only one that
// says anything. The flag is per stream because it belongs to the reporting and
// not to the screen: two tabs on one dead session are two reports, which is a
// number an operator can read, and the state that would make it one is state the
// shared buffer would then have to unwind on every attach.
func (s *Server) reader(live session.Session, shared *pane) func(context.Context) (string, error) {
	failing := false
	return func(ctx context.Context) (string, error) {
		screen, err := shared.current(ctx, s.streamTick, func(ctx context.Context) (string, error) {
			capture, err := s.sessions.Output(ctx, live)
			return capture.Text, err
		})
		switch {
		case err == nil:
			failing = false
		case !failing:
			failing = true
			// Wrapped with the id off the daemon's own record and never with what
			// was read: a partial capture in an error string is pane content in
			// whatever records the error (FR-042).
			s.report(fmt.Errorf("capture the screen for the stream of session %s: %w", live.ID, err))
		}
		return screen, err
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

	// sent is the screen this stream last put on the wire, and everSent says
	// whether it ever put one there. The pair is what suppression compares
	// against, and it is per stream rather than per session on purpose: two tabs
	// that attached a second apart have seen different screens, and a shared
	// "last sent" would silence the one that is behind.
	//
	// everSent is not a check for an empty screen. A session that has printed
	// nothing has an empty screen, and "" is a screen this stream must send once
	// — collapsing the two would leave a fresh tab on a quiet session with no
	// event at all until something happened to print.
	sent     string
	everSent bool
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
// exactly one thing per tick.
//
// Exactly one write per tick is the invariant contracts/stream.md asks for, and
// tick keeps it by choosing between an event and a comment rather than by adding
// a second write.
//
// The opening screen is written before the first tick, and it is the one write
// here that is not one. A browser attaches to this stream from a page that was
// rendered with a capture of its own, and a stream that waited out its first
// interval would leave that capture the newest thing the operator has for a
// whole second — while the screen it would have sent has already been read into
// the shared buffer by whoever else is watching. Nothing is written when that
// first capture fails: the heartbeat is a tick's answer to a quiet screen, and
// this is not a tick.
//
// A cancelled context is the ordinary ending and not a failure: it is what a
// closed tab, a dropped connection, and a closed server all arrive as, and every
// one of them means this response is over.
func (s *stream) hold(ctx context.Context, every time.Duration, read func(context.Context) (string, error)) error {
	if screen, err := read(ctx); err == nil {
		if err := s.send(screen); err != nil {
			return err
		}
	}

	ticker := time.NewTicker(every)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if err := s.tick(ctx, read); err != nil {
				return err
			}
		}
	}
}

// tick writes the event when the screen changed since this stream last sent one,
// and the heartbeat comment when it did not (research D5).
//
// Suppression is the point rather than an optimisation. A session idling at a
// prompt repaints nothing, and a tick that re-sent the identical screen would
// push it to every open tab every second for as long as the tab lives — burning
// an exec, a write, and a wake-up on each for zero information. What the tab
// needs from a quiet second is only that the connection is still there, which is
// what a comment says and an event does not.
//
// A capture that failed is a suppressed tick and not the end of the stream. The
// window may have gone between one tick and the next, and tmux may answer again
// a second later; what turns a session that really ended into a closed stream is
// the re-evaluation against the daemon's own records that T028 owns, never one
// exec that did not answer.
func (s *stream) tick(ctx context.Context, read func(context.Context) (string, error)) error {
	screen, err := read(ctx)
	if err != nil || (s.everSent && screen == s.sent) {
		return s.write(heartbeat)
	}
	return s.send(screen)
}

// send puts the changed screen on the wire and remembers it, which is what makes
// the next identical capture a heartbeat.
//
// It records what was sent before the write rather than after. A write that
// fails ends the stream, so there is no next comparison for the difference to
// matter to — and recording afterwards would mean a partially written event was
// remembered as unsent, which on a transport that retried would send it twice.
func (s *stream) send(screen string) error {
	s.sent, s.everSent = screen, true
	return s.write(screenChanged)
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

// panes is the daemon's set of shared screen buffers: one per session somebody
// is watching, and none for a session nobody is.
//
// It exists because the cap and the cost model count different things
// (contracts/stream.md). CRSW_MAX_STREAMS bounds *connections*, which is what a
// browser opens; the work a watched session costs this host is one capture-pane
// exec per *session* per interval, which is what tmux is asked for. Ten tabs on
// one session must be one exec a second and not ten — otherwise a single
// operator with a tiling window manager is a load generator, and the bound
// Principle VI calls non-negotiable bounds the wrong quantity.
//
// A buffer is dropped when its last watcher leaves rather than kept against the
// next one. It holds a session's screen, which is secret under
// docs/security.md §3 and is exactly the material a long-running daemon must not
// accumulate — and the screen it would have kept is stale by then anyway, since
// what makes a reading worth reusing is that it was taken this second.
type panes struct {
	mu      sync.Mutex
	watched map[string]*pane
}

func newPanes() *panes {
	return &panes{watched: make(map[string]*pane)}
}

// attach hands back this session's buffer — creating it if this is the first
// stream to watch the session — and the release that drops it when the last
// stream leaves.
//
// The release is returned rather than being a second method for the reason the
// cap's is, and it runs at most once for the same reason: a release that ran
// twice would drop a buffer other streams are still sharing, and the tick after
// it would exec against a buffer nobody else can see. That is not a correctness
// failure the way a double-released slot is — it costs execs, not admissions —
// which is precisely why it would go unnoticed.
func (p *panes) attach(id string) (shared *pane, release func()) {
	p.mu.Lock()
	defer p.mu.Unlock()

	shared, ok := p.watched[id]
	if !ok {
		shared = &pane{}
		p.watched[id] = shared
	}
	shared.watchers++

	var once sync.Once
	return shared, func() { once.Do(func() { p.detach(id) }) }
}

func (p *panes) detach(id string) {
	p.mu.Lock()
	defer p.mu.Unlock()

	shared, ok := p.watched[id]
	if !ok {
		return
	}
	shared.watchers--
	if shared.watchers <= 0 {
		delete(p.watched, id)
	}
}

// pane is one watched session's screen, shared by every stream watching it: the
// last reading, when it was taken, and how it went.
type pane struct {
	// watchers is guarded by the registry's mutex rather than by mu below,
	// because it is the registry's bookkeeping and not the screen's: attaching
	// and dropping a buffer has to be one critical section with the count, or two
	// streams racing the last detach both find zero and the second deletes a
	// buffer the first has just recreated.
	watchers int

	mu     sync.Mutex
	screen string
	err    error

	// taken is when the reading was *started*, not when it came back, and the
	// difference is the cadence rather than pedantry: a window measured from the
	// answer makes the period the interval plus however long tmux took, which is
	// always longer than the interval a ticker running at the interval fires at —
	// so every other tick would find the buffer a hair's breadth inside the window
	// and the screen would update at half the rate it was configured to.
	//
	// It is the host clock rather than the Server's, deliberately. The stream's
	// whole cadence is real elapsed time on a real socket, and a fixture that
	// pinned this would freeze every reading as fresh forever and never exec
	// twice, which is the opposite of what a test of the interval is for. What a
	// test shortens instead is the interval itself, which is Server.streamTick and
	// already a seam.
	taken time.Time
}

// current returns the session's screen, capturing it only when the last reading
// is older than interval.
//
// The capture happens with the lock held, which is what makes "one exec per
// session per interval" true rather than likely. Two ticks arriving together
// would otherwise both find the buffer stale and both exec; here the second
// waits out the first, then finds the reading it was about to take.
//
// The reading is cached whether it succeeded or failed, so an outage costs one
// exec per interval like a working session does — a session whose window has
// gone is the case where an uncached failure would have every watcher exec
// against a tmux that is already answering slowly.
//
// A failure that is this caller's own cancelled request is the one thing not
// cached. It is a fact about one connection that closed rather than about the
// session, and leaving it in the buffer would hand every other stream on this
// session an error from a browser that is not theirs.
func (p *pane) current(ctx context.Context, interval time.Duration, capture func(context.Context) (string, error)) (string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if !p.taken.IsZero() && time.Since(p.taken) < interval {
		return p.screen, p.err
	}
	previous := p.taken
	p.taken = time.Now()

	screen, err := capture(ctx)
	switch {
	case err != nil && ctx.Err() != nil:
		// This caller's own request ended mid-capture. The window goes back where
		// it was rather than counting, so the next watcher to ask reads the session
		// instead of being told for a whole interval what one closed browser found.
		p.taken = previous
		return "", err
	case err != nil:
		// The last good screen is left where it is rather than cleared. Nothing
		// reads it while err is set, and a transient failure that emptied the
		// buffer would make the recapture after it look like a change — which on
		// this transport is an event saying the screen went blank and came back.
		p.err = err
		return "", err
	}

	p.screen, p.err = screen, nil
	return screen, nil
}
