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
// *When* a stream writes and *what* it writes both live here. The capture loop
// below reads each watched session once per tick into a buffer every stream on
// that session shares, writes an event only when the screen differs from the one
// that stream last sent, and frames that screen as one JSON string (research
// D4). The encoding is the framing rather than a formality: SSE's wire format is
// line-oriented, so a screen written raw into a `data:` field is a screen framed
// by whatever it happens to contain — and what it contains is whatever an
// unsandboxed program chose to print.
//
// *How it ends* is the third subject, and the one that keeps the four checks
// above honest. Authorisation here is re-evaluated rather than established: every
// tick asks the daemon's own records whether there is still a session and whether
// it is still this viewer's, so a session that was destroyed, reaped, or has
// stopped being theirs stops being delivered within one interval and the watcher
// is told rather than left with a screen that quietly never changes again. The
// asking is also what keeps the two directions apart — nothing that tears a
// session down has to know a watcher exists, and no watcher may hold anything up:
// not the reaper, not a destroy, and not the shutdown that closes every stream
// before it drains what is left.
//
// The trail is written at the open, and this is the one route on either door
// that does not leave that to the middleware's deferred emit (FR-016a). A
// response deliberately without an end cannot be recorded when the handler
// returns: that moment is hours away, is whenever the browser goes away, and
// never arrives at all if the daemon dies first — which would leave no trace
// that a session's screen was being read. One record per stream request,
// carrying the authorisation decision, and none at the close.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/nctiggy/claude-remote-session-webhook/internal/auth"
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

// maxScreenBytes caps one encoded screen. A tmux pane holds kilobytes — an
// 80x24 default is about 2 KiB and a very large terminal a few hundred — so 8
// MiB is roughly three orders of magnitude above anything reachable, chosen so
// that it can only ever fire when the assumption behind it has broken rather
// than when a pane is merely big. See screenEvent for what that assumption is.
const maxScreenBytes = 8 << 20

// errScreenTooLarge is returned rather than truncating. A half a screen is not
// a smaller screen, it is a wrong one, and this route's whole framing design
// exists to make a partial event impossible.
var errScreenTooLarge = errors.New("encoded screen exceeds the wire limit")

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

// dataField, eventField and groupEnd are SSE's own punctuation: the field every
// event on this route carries, the name only the terminal event carries, and the
// blank line that ends any line group. Every write this route can make is
// terminated by the same constant, so they cannot drift into disagreeing about
// what ends a group.
const (
	dataField  = "data: "
	eventField = "event: "
	groupEnd   = "\n\n"
)

// heartbeat is an SSE comment, which is a line that is not an event and carries
// no data. It is what a tick writes when there is nothing to send.
//
// A quiet stream that wrote nothing at all would be a stream the daemon cannot
// tell from a live one: a browser that vanished without closing the connection
// would hold its slot forever, because nothing would ever fail. One write per
// tick is what turns a dead peer into a write error, and it is also what keeps
// an idle proxy at the edge from severing a stream that is working perfectly.
var heartbeat = []byte(":" + groupEnd)

// endEvent is what a stream writes when the session it was watching is no longer
// there, and it is the last thing it writes (FR-033, contracts/stream.md).
//
// It is the one *named* event on this route, and the name is what carries the
// meaning: the client can tell the end from a screen without looking at the
// payload, which is the only arrangement in which a session cannot announce its
// own ending by printing one. Its data is still a JSON string, so the client
// parses every event it receives the same way rather than keeping a second rule
// for the one event that is different.
//
// What the client must do with it is close (FR-033). Without an explicit close
// an EventSource reconnects for as long as the tab lives, and every reconnection
// after the session ended is a request the daemon answers with the uniform 404 —
// a polite client turned into a scanner of its own daemon.
var endEvent = []byte(eventField + "end\n" + dataField + `"ended"` + groupEnd)

// screenEvent frames one screen as the event a tick writes instead of the
// heartbeat when the capture differs from the screen that stream last sent
// (research D4, contracts/stream.md).
//
// The whole screen goes into one `data:` field as one JSON string, and the
// encoding is what makes the framing independent of the content. SSE's wire
// format is line-oriented and a screen is inherently multi-line: a raw newline
// inside `data:` starts a new field, a payload split across fields loses a lone
// `\r` when the client rejoins them, and a line the session printed beginning
// `event:` would be a session choosing this daemon's framing. A JSON string can
// carry none of the three, because every byte that could delimit anything is
// escaped — so what a screen contains and how it is delimited become separate
// questions, which is the only form in which the answer survives a program that
// prints whatever it likes.
//
// It is an unnamed event, which is what an EventSource dispatches as `message`.
// The empty screen is still an event: a session that has printed nothing encodes
// as `""` rather than as nothing at all, and an event whose data buffer is
// genuinely empty is one EventSource drops instead of dispatching — so a fresh
// tab on a session that has yet to print would otherwise hear silence and be
// unable to tell it from a stream that is not working.
//
// The error cannot arise today: encoding/json replaces invalid UTF-8 rather than
// refusing it, and Strip has already removed anything that would be. It is
// returned rather than dropped because the only other thing to do with it is
// write a half-framed event, which is precisely what this function exists to
// make impossible.
func screenEvent(screen string) ([]byte, error) {
	encoded, err := json.Marshal(screen)
	if err != nil {
		// Never the screen itself, and never a prefix of it: a payload that could
		// not be encoded is still pane content, and pane content in an error string
		// is pane content in whatever records the error (FR-042).
		return nil, fmt.Errorf("encode the screen for the wire: %w", err)
	}

	// The bound the allocation below relies on, stated where it is relied upon.
	//
	// tmuxctl.CapturePane passes -p with no -S or -E, so what arrives is the
	// visible screen rather than the scrollback behind it — kilobytes for any
	// real pane. This check does not defend against that being large; it defends
	// against it having *silently stopped being bounded*, which is what adding
	// -S upstream would do (issue #41).
	//
	// The limit is deliberately far above any screen a terminal can hold, so it
	// never truncates what a reader is trying to read. Refusing is also the right
	// failure: send() does not record an unframed screen as sent, so the next
	// capture retries rather than the stream going quiet on a stale screen.
	//
	// The size is safe to name in the error. The screen is not (FR-042).
	if len(encoded) > maxScreenBytes {
		return nil, fmt.Errorf("%w: %d bytes exceeds the %d-byte screen limit",
			errScreenTooLarge, len(encoded), maxScreenBytes)
	}

	event := make([]byte, 0, len(dataField)+len(encoded)+len(groupEnd))
	event = append(event, dataField...)
	event = append(event, encoded...)
	return append(event, groupEnd...), nil
}

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

// errWatchedSessionEnded is the re-evaluation's verdict, and it is the one error
// in this file that is neither a refusal nor a failure: the stream was
// authorised, served, and is now over because what it was watching is gone
// (FR-034b).
//
// It reaches neither the response nor the trail. The response gets the terminal
// event, which says the same thing in the transport's own vocabulary; the trail
// got its one record at the open, and after that emit an amendment reaches
// nobody — so a stream that ends carries no second record, which is the choice
// contracts/stream.md makes and the invariant FR-041 keeps.
//
// It is deliberately not the error View returned. That error distinguishes an
// unknown session from one this viewer no longer owns from a dead record, and
// the three are one answer here for the reason they are one answer at the open:
// a watcher who is told which of them happened is a watcher being told about
// records that are not theirs.
var errWatchedSessionEnded = errors.New("the watched session is no longer there to read")

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
//     recording a driving (FR-034f). Watching is not driving: a tab left
//     open on a session nobody is prompting must not report itself as
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

	// The record goes on the trail here, at the open, rather than when this
	// handler returns (FR-016a). Everything above this line has either denied and
	// returned or is the decision this record carries, and everything below it is
	// the stream itself — so this is the last instant at which the trail can be
	// written and still be written promptly.
	//
	// After the open rather than before it, so that a response which could not be
	// turned into a stream is still recorded as the refusal it is: that path
	// denies and returns above, and its record is emitted by the middleware
	// microseconds later, which for a request that ended is the same instant.
	//
	// Nothing below amends the record, and nothing may: after this call an
	// amendment reaches nobody. There is deliberately no second record when the
	// stream ends (contracts/stream.md) — the close carries no authorisation fact
	// the open did not, and a pair would make this the one door where exactly one
	// record per request is false.
	s.emit(AuditFrom(r.Context()))

	// The shared buffer, taken after the response is a stream so that an open
	// which never became one neither creates one nor drops one. Deferred for the
	// reason the slot is: a buffer left behind by one forgotten return is a
	// session's screen held in memory for as long as the daemon runs, and pane
	// content is secret under docs/security.md §3.
	shared, unwatch := s.panes.attach(live.ID)
	defer unwatch()

	if err := sse.hold(r.Context(), s.streamTick, s.closing, s.reader(live, operator.Owner, shared)); err != nil {
		// A peer that closed cleanly ends through the context and returns no
		// error at all, so what reaches here is a write that failed against a
		// connection nobody closed — the vanished browser the heartbeat exists to
		// find. It is a fact about the host's connections rather than about the
		// request, so it goes where a failure with nowhere else to go goes, and
		// never into the response.
		s.report(fmt.Errorf("write the output stream for session %s: %w", live.ID, err))
	}
}

// reader is what one held stream reads its screen from: the authorisation it
// re-answers every time, the buffer shared by every stream watching this
// session, the capture that fills it, and the single report an outage earns.
//
// The re-evaluation is first, and it is a check rather than a lookup for
// convenience (FR-034b). Authorisation here is *not established* at the open and
// then remembered: a session that was destroyed, reaped, or has stopped being
// this viewer's must stop delivering within one interval (SC-015), and a stream
// holding the record it was admitted with would go on reading a window whose
// record the daemon has already dropped. Asking the daemon's own store every
// tick is what makes the ending a discovery rather than a race — teardown does
// not have to find the watchers, because the watchers keep asking.
//
// The record the capture uses is the one that ask just returned, never the one
// the open closed over. They agree today; the point is that if they ever
// disagreed, the fresh one is the daemon's answer and the stale one is this
// connection's memory.
//
// The capture goes through Manager.Output rather than the controller, which is
// what keeps one stripper rather than a second that agrees today (FR-029) — and
// what keeps the stream off that clock, since Output takes the record it was
// given and advances nothing on it (FR-034f). The one write it may make is the
// opposite of an extension: a session the host has confirmed is gone loses its
// record there, and the next tick's re-evaluation turns that into this stream's
// terminal event (#21). View does not advance it either, by
// construction rather than by argument, so watching is not driving on every tick
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
func (s *Server) reader(live session.Session, owner auth.CallerID, shared *pane) func(context.Context) (string, error) {
	failing := false
	return func(ctx context.Context) (string, error) {
		current, err := s.sessions.View(live.ID, owner)
		if err != nil {
			// Nothing is reported and nothing is wrapped. A session that ended is
			// the ordinary end of a stream rather than a failure of one, and the
			// caller's answer to it is the terminal event.
			return "", errWatchedSessionEnded
		}

		screen, err := shared.current(ctx, s.streamTick, func(ctx context.Context) (string, error) {
			capture, err := s.sessions.Output(ctx, current)
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
//
// There are two other endings, and neither is a failure either.
//
// The session going away ends the stream with the terminal event, because a view
// that stopped updating in silence would be indistinguishable from a session
// that had gone quiet (FR-033) — an operator would go on watching a screen that
// is never coming back. It is discovered by the read, one interval at most after
// it happened (SC-015), rather than delivered by whatever tore the session down:
// teardown does not block on watchers, and nothing about it has to know that any
// exist.
//
// The daemon shutting down ends the stream at once and *without* a farewell
// (FR-034f, contracts/stream.md). Streams are ended here, before the drain, for a
// blunt reason: a response deliberately without an end is an in-flight request
// that never finishes, so a drain waiting for one would spend its whole budget
// on a connection that was never going to complete — and that budget belongs to
// the six short routes it was sized for, with the verified teardown of every
// session waiting behind it. No farewell is guaranteed because the daemon is
// racing its own service manager, and the client's reconnect covers the
// difference: a reconnection to a live daemon re-authorises, and one to a dead
// daemon fails visibly.
func (s *stream) hold(ctx context.Context, every time.Duration, closing <-chan struct{}, read func(context.Context) (string, error)) error {
	switch screen, err := read(ctx); {
	case errors.Is(err, errWatchedSessionEnded):
		// The session went between the open's ownership check and this first read.
		// Rare, and served rather than special-cased: the operator gets the same
		// account of it they would have got a tick later.
		return s.end()
	case err == nil:
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
		case <-closing:
			return nil
		case <-ticker.C:
			switch err := s.tick(ctx, read); {
			case errors.Is(err, errWatchedSessionEnded):
				return s.end()
			case err != nil:
				return err
			}
		}
	}
}

// end writes the terminal event, and a stream writes nothing after it.
//
// The write's own failure is still returned. The connection is gone either way,
// but a farewell that could not be delivered is a browser that will reconnect
// rather than close, and the caller's report is the only place that is visible.
func (s *stream) end() error {
	return s.write(endEvent)
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
// the re-evaluation against the daemon's own records, never one exec that did not
// answer. That verdict is the one error passed back to the caller rather than
// answered with a heartbeat, and the distinction is the whole of why the two are
// separate errors: an unanswered exec is a quiet second, and a session the daemon
// no longer has a record of is not coming back.
func (s *stream) tick(ctx context.Context, read func(context.Context) (string, error)) error {
	screen, err := read(ctx)
	switch {
	case errors.Is(err, errWatchedSessionEnded):
		return err
	case err != nil, s.everSent && screen == s.sent:
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
//
// The framing happens before the record for the opposite reason: a screen that
// could not be framed never reached the wire at all, and remembering it as sent
// would suppress the next capture of the identical screen.
func (s *stream) send(screen string) error {
	event, err := screenEvent(screen)
	if err != nil {
		return err
	}

	s.sent, s.everSent = screen, true
	return s.write(event)
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

// watching is how many streams share this session's buffer, and zero for a
// session nobody is watching — which is also the answer for one whose buffer has
// been dropped.
//
// It takes the registry's own lock, which is the whole reason it exists: the map
// is written by every attach and detach, on whichever goroutine net/http gave
// the handler, so an assertion that reached in from another one would be a data
// race dressed up as a check.
func (p *panes) watching(id string) int {
	p.mu.Lock()
	defer p.mu.Unlock()

	shared, ok := p.watched[id]
	if !ok {
		return 0
	}
	return shared.watchers
}

// holds reports whether this session's screen is still in the registry at all,
// which is deliberately not the question watching answers.
//
// Zero watchers and no buffer are the same answer from watching and two very
// different states of the daemon: a buffer whose release did not run has no
// watchers and is still a session's screen held in memory for nobody, which is
// exactly the accumulation docs/security.md §3 forbids and exactly what an
// assertion counting watchers cannot see. It takes the registry's lock for the
// reason watching does — the map is written on whichever goroutine net/http gave
// the handler.
func (p *panes) holds(id string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()

	_, ok := p.watched[id]
	return ok
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
