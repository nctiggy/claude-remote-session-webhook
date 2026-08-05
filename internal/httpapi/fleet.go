package httpapi

// fleet.go is the fleet event stream — GET /dashboard/fleet/stream
// (contracts/fleet-stream.md), and it is the answer to issue #15: an open
// dashboard learns about a change it did not cause.
//
// It is the pane stream's shape with one thing moved. Milestone 2's stream is a
// poll — it wakes on its own clock, asks tmux what one session's screen says, and
// writes when the answer differs from the last one it sent. This one is a
// subscription: it wakes when the daemon changes its own fleet, because
// Manager.Subscribe delivers from beside the store mutation that made the change
// (T012). That difference is the whole of why the reaper is covered here at all.
// Nothing about a reap is a request, so a route that polled would see one only by
// re-reading the store on a timer and diffing it — which is the fleet snapshot
// research R6 rejected, kept on the server instead of on the wire.
//
// What it carries is an identifier and nothing else (R6). The event says which
// card is stale; the page re-fetches that card through the routes it already has.
// A rendered fragment here would couple the stream to the template set and a
// snapshot would send O(fleet) bytes for a one-card change — but the reason that
// matters at this line rather than in a note about bandwidth is FR-025: a payload
// carrying a name, a path or a state is session data on a connection that lives
// for hours, and the id alone is the smallest thing that tells the page what it
// needs to know.
//
// The ownership filter is deliberately not in this file, and that is what
// Manager.Subscribe taking an owner is for (FR-019b). The filter is a property of
// the subscription rather than a check the writer performs, so an event about
// another identity's session is never delivered to this goroutine at all — there
// is nothing here that could forget it, and no later edit to the writing below
// can lose it.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/nctiggy/claude-remote-session-webhook/internal/session"
)

// patternFleetStream is the fleet stream's route (contracts/fleet-stream.md).
//
// It lives under /dashboard/ with the four action routes so that milestone 1's
// surface stays exactly the six operations it contracted (FR-005), and it carries
// no {id} because it is about the fleet rather than about a session — which is
// also why it is the one route on this door with no uniform not-found to give.
//
// The method is part of the pattern for the reason the action routes' is: a POST
// here matches no pattern of this route's, falls to handleUnrouted's `/`, and is
// answered as a path nothing claims — never a 405 and never an Allow header
// (FR-033).
const patternFleetStream = "GET /dashboard/fleet/stream"

// The refusals this route records, authored here and never written into a
// response.
//
// Both are constants for the reason every other reason in this repo is one: the
// trail may carry nothing a caller wrote (FR-042), and in particular not the
// value of Sec-Fetch-Site, which is caller-authored text.
var (
	// errFleetCrossSite is the same-origin check doing its job on this route
	// (FR-019b). It is a sentinel of its own rather than the pane stream's,
	// because an operator reading their journal needs to know which stream was
	// opened from somewhere other than the dashboard.
	errFleetCrossSite = errors.New("the fleet stream was opened from somewhere other than the dashboard")

	// errFleetNotOpened is what the trail records when the response could not have
	// its write deadline lifted, which is the same fail-closed refusal
	// errStreamNotOpened records for the pane stream.
	errFleetNotOpened = errors.New("the fleet stream could not be opened")
)

// fleetPayload is the whole of what goes on this wire: one identifier, under the
// key contracts/fleet-stream.md spells.
//
// It is a type of its own rather than session.FleetEvent with tags on it, and
// that is the enforcement rather than tidiness. A FleetEvent carries an Owner —
// it has to, since the owner is what decides whose stream an event reaches — and
// a struct that both routed and rendered would put that identity one `json:` tag
// away from the wire. Two types means the only field that can be written is the
// one named here, and adding a second is a thing somebody has to type into this
// file (AR-010, FR-025).
type fleetPayload struct {
	ID string `json:"id"`
}

// fleetEvent frames one change as the SSE event the contract fixes: the kind in
// an `event:` field, the identifier in a `data:` field, and the blank line that
// ends any line group.
//
// The kind is checked against the three the contract names rather than written
// through. It is not caller input — the values come from internal/session's own
// constants — so this is not a validation but a framing guarantee: `event:` is a
// single line, and a fourth kind added to the manager without a line in
// contracts/fleet-stream.md is an event no page knows how to read. Refusing to
// frame it ends the stream, which is the visible failure; writing it would be a
// silent one.
//
// The error cannot arise from the payload today, for the reason screenEvent's
// cannot: encoding/json replaces invalid UTF-8 rather than refusing it, and an id
// is 32 hex characters off the daemon's own record either way. It is returned
// rather than dropped because the only other thing to do with it is write a
// half-framed event, which is precisely what this function exists to make
// impossible.
func fleetEvent(ev session.FleetEvent) ([]byte, error) {
	switch ev.Kind {
	case session.FleetAppeared, session.FleetVanished, session.FleetChanged:
	default:
		// Never the kind itself: an unrecognised value is a value this daemon has
		// no account of, and a reason built from one is a reason nobody can tell
		// apart from a reason built from the request.
		return nil, errors.New("a fleet change arrived under a kind contracts/fleet-stream.md does not name")
	}

	payload, err := json.Marshal(fleetPayload{ID: ev.ID})
	if err != nil {
		return nil, fmt.Errorf("encode the fleet change for the wire: %w", err)
	}

	event := make([]byte, 0, len(eventField)+len(ev.Kind)+1+len(dataField)+len(payload)+len(groupEnd))
	event = append(event, eventField...)
	event = append(event, ev.Kind...)
	event = append(event, '\n')
	event = append(event, dataField...)
	event = append(event, payload...)
	return append(event, groupEnd...), nil
}

// fleetStream serves GET /dashboard/fleet/stream (contracts/fleet-stream.md).
//
// Two checks admit it, and they are the pane stream's first two rather than
// versions of them: layer 1, which the door in front has already run, and
// crossSite. There is deliberately no page token. The token authorises a state
// change (contracts/actions.md), this route changes nothing, and requiring one
// would make a read on this door inconsistent with the read it otherwise mirrors
// exactly — it would also mean an EventSource had to carry a form field, which is
// not a thing an EventSource can do.
//
// crossSite rather than crossSiteAction, which is the same reading the pane
// stream makes and for the same reason: an absent Sec-Fetch-Site is a caller that
// is not a browser, and the attack the check is about is a hostile page whose
// browser always sends the header. What a mutating route may not do is treat an
// absent header as evidence of same-origin initiation, because a script that
// wants to change something can omit one — and this route lets a script observe
// only what the operator's own credential already lets it fetch a page of.
//
// The order below is the contract's. Nothing about the response — not a header,
// not the deadline, not a byte — happens until both checks have been answered, so
// a caller who is refused cannot tell a refusal on this route from a refusal on
// any other page of this dashboard.
func (s *Server) fleetStream(w http.ResponseWriter, r *http.Request) {
	operator, ok := OperatorFrom(r.Context())
	if !ok {
		// Fail closed on the path that should not happen: the door in front puts
		// the operator in the context, so a false here is a route registered
		// without one.
		AuditFrom(r.Context()).Deny(errDashboardNoOperator.Error())
		s.refuseBrowser(w)
		return
	}

	if crossSite(r) {
		// The door's own uniform refusal, byte-identical to the one the pane stream
		// gives and to the one an unverified caller gets. A shape of its own here
		// would be a shape that varies with something about the request, and the
		// page that triggered this one is not owed the knowledge that its request
		// was recognised for what it was.
		AuditFrom(r.Context()).Deny(errFleetCrossSite.Error())
		s.refuseBrowser(w)
		return
	}

	// Subscribed before the response is touched, so that the window in which a
	// change could happen unheard is as small as this side can make it. The window
	// that remains is between the page's own render and this request, which no
	// arrangement here can close — it is why the page re-fetches rather than
	// trusting what it was rendered with.
	//
	// The owner is the identity layer 1 verified and never a value the request
	// carried (FR-019b). An empty one would be a caller who reached here without
	// authentication, and Subscribe answers that with a channel that is already
	// closed rather than one that stays quiet — so this handler serves an empty
	// stream that ends at once instead of a stream that appears to be working.
	events, unsubscribe := s.sessions.Subscribe(operator.Owner)
	// Deferred rather than cancelled at each ending, so the subscription goes back
	// on every path out of this handler — a failed open, a write against a browser
	// that vanished, a cancelled request, and an unwinding panic alike. It is
	// idempotent and safe after the daemon has already dropped this subscriber for
	// falling behind, which is the ordinary case rather than the impossible one.
	defer unsubscribe()

	sse, err := openStream(w)
	if err != nil {
		// Refusing to serve is the fail-closed answer here for the reason it is on
		// the pane stream: a response that could not lift its deadline is a stream
		// that would be cut off thirty seconds in, and a dashboard whose updates
		// stopped cannot tell that from a fleet where nothing is happening.
		AuditFrom(r.Context()).Deny(errFleetNotOpened.Error())
		s.report(fmt.Errorf("open the fleet stream: %w", err))
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	// The record goes on the trail at the open and not when this handler returns,
	// for the reason the pane stream's does (FR-016a): that moment is hours away,
	// is whenever the browser goes away, and never arrives at all if the daemon
	// dies first. One record per open, never one per event — an event-per-record
	// trail would grow with the fleet's activity rather than with requests, and
	// FR-023 counts requests.
	//
	// Nothing below amends the record, and nothing may: after this call an
	// amendment reaches nobody.
	s.emit(AuditFrom(r.Context()))

	if err := sse.follow(r.Context(), s.streamTick, s.closing, events); err != nil {
		// A peer that closed cleanly ends through the context and returns no error
		// at all, so what reaches here is a write that failed against a connection
		// nobody closed — the vanished browser the heartbeat exists to find. It is a
		// fact about the host's connections rather than about the request, so it
		// goes where a failure with nowhere else to go goes, and never into the
		// response.
		s.report(fmt.Errorf("write the fleet stream: %w", err))
	}
}

// follow keeps the stream open until the request ends, the daemon closes, the
// subscription ends, or a write fails — writing one event per change and one
// comment per quiet tick.
//
// The two sources are why this is not stream.hold with a different reader. A pane
// stream writes exactly one thing per tick because its subject is a screen that
// is always there to be read; a fleet stream writes when the fleet changes, which
// is not on any clock the daemon owns. The ticker here is only the heartbeat: a
// stream that wrote nothing at all between changes would hold a slot for a
// browser that vanished without closing, because nothing would ever fail, and an
// idle proxy at the edge would sever a connection that is working perfectly. The
// cadence is the pane stream's own, so a dead peer is found identically on both
// routes.
//
// The heartbeat is not suppressed by an event that has just been written. It
// could be — the connection was proved alive a moment ago — but the saving is one
// comment per second per open dashboard, and what it would cost is a timer that
// has to be reset from two places and a quiet stream whose cadence depends on how
// recently the fleet changed. A comment on a live connection is the cheapest byte
// this daemon writes.
//
// A closed channel ends the response, and that is the daemon saying it could not
// keep this stream current: Manager.publish drops a subscriber that has fallen
// fleetBacklog events behind rather than letting it hold up the destroy, the
// reap, or the shutdown that is publishing. It ends with no farewell event, and
// that is deliberate. contracts/fleet-stream.md names three events and none of
// them is an ending, so a fourth invented here would be an event no page has a
// rule for — while a connection that simply ends is a thing every EventSource
// already reports, which is what FR-020 needs the page to be able to say. The
// reconnection that follows is the right recovery rather than a nuisance: it
// re-authorises, subscribes again, and the page re-fetches the fleet it can no
// longer vouch for, which is exactly what a dropped subscriber has to do.
//
// A cancelled context and a closing daemon are the other two endings, and neither
// is a failure either. Shutdown ends every stream before the drain for the reason
// it ends the pane streams there: a response deliberately without an end would
// otherwise spend the whole drain budget, and behind that budget is the verified
// teardown of every session.
func (st *stream) follow(ctx context.Context, every time.Duration, closing <-chan struct{}, events <-chan session.FleetEvent) error {
	ticker := time.NewTicker(every)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-closing:
			return nil
		case ev, open := <-events:
			if !open {
				return nil
			}
			event, err := fleetEvent(ev)
			if err != nil {
				return err
			}
			if err := st.write(event); err != nil {
				return err
			}
		case <-ticker.C:
			if err := st.write(heartbeat); err != nil {
				return err
			}
		}
	}
}
