// Internal test, matching the rest of the package. Every claim here is about the
// fleet event stream — GET /dashboard/fleet/stream, contracts/fleet-stream.md —
// and most of them need a real listener for the reason stream_test.go's do: an
// httptest.ResponseRecorder cannot lift a write deadline, so a request that gets
// past the two checks is answered with a 500 rather than a stream.
//
// That is also the seam the refusal table below uses. On this route a 500 from a
// recorder means the request was *admitted*, and nothing else does.
package httpapi

import (
	"bufio"
	"context"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/nctiggy/claude-remote-session-webhook/internal/audit"
	"github.com/nctiggy/claude-remote-session-webhook/internal/auth"
	"github.com/nctiggy/claude-remote-session-webhook/internal/session"
)

// fleetGroupsWatched bounds how many line groups a claim about this stream reads
// before it gives up.
//
// It is generous on purpose. The events these tests wait for are caused after the
// stream is open, and everything between the cause and the event — a create
// through the manager, a verified teardown against the tmux fake — happens on a
// machine that may be busy, while the heartbeat goes on writing a group every
// tickUnderTest. What the bound prevents is a stream that never delivers hanging
// the package; it is not a claim about latency.
const fleetGroupsWatched = 300

// followTheFleet opens the fleet stream the way the dashboard's own page does:
// the identity assertion in a header, the browser's own account of where the
// request came from, no signature, and no credential anywhere in the URL.
func (f *fleet) followTheFleet(t *testing.T, addr string) *http.Response {
	t.Helper()
	return f.followTheFleetFrom(t, addr, secFetchSiteSameOrigin)
}

// followTheFleetFrom is followTheFleet with Sec-Fetch-Site chosen by the caller.
//
// The response is returned unread and unjudged, as watchFrom's is: some callers
// want a stream and some want a refusal, and a helper that insisted on 200 could
// not fetch the second.
func (f *fleet) followTheFleetFrom(t *testing.T, addr, site string) *http.Response {
	t.Helper()

	target := "http://" + addr + fleetStreamPath
	r, err := http.NewRequestWithContext(context.Background(), http.MethodGet, target, nil)
	if err != nil {
		t.Fatalf("build the fleet stream request: %v", err)
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
			t.Errorf("close the fleet stream: %v", err)
		}
	})
	return resp
}

// askToFollowTheFleet drives one open through a recorder, carrying the credential
// and the fetch-metadata header the caller chose. See the file comment for why a
// 500 here means the request was admitted.
func (f *fleet) askToFollowTheFleet(t *testing.T, assertion, site string) *httptest.ResponseRecorder {
	t.Helper()

	r := httptest.NewRequest(http.MethodGet, fleetStreamPath, nil)
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

// fleetStreamPath is the route's address, derived from the pattern rather than
// spelled again, so a contract change moves every request in this file with it.
var fleetStreamPath = strings.TrimPrefix(patternFleetStream, "GET ")

// readFleetGroup reads one whole SSE line group off the wire and hands back the
// lines before the blank one that ends it.
//
// The two-line ceiling is an assertion rather than a guard. Every group this
// route may write is a comment on its own or an `event:` and a `data:` together
// (contracts/fleet-stream.md), so a third line is a field the contract does not
// name — an `id:` or a `retry:` would both change what a client does with the
// stream, and neither is a thing this route gets to add quietly.
func readFleetGroup(t *testing.T, body *bufio.Reader, opened time.Time) []string {
	t.Helper()

	var group []string
	for {
		line := readStreamLine(t, body, opened)
		if line == "\n" {
			return group
		}
		group = append(group, line)
		if len(group) > 2 {
			t.Fatalf("the fleet stream wrote a line group of %q; the contract's groups are a comment, or an event and its data", group)
		}
	}
}

// awaitFleetChange reads groups until one is not the heartbeat, and returns it.
//
// Heartbeats are read through rather than counted: they arrive every tick for as
// long as the connection lives, so a claim about *which* change was written has
// to be able to say "and nothing else was written in between", which is what
// returning the first non-comment group makes possible.
func awaitFleetChange(t *testing.T, body *bufio.Reader, opened time.Time) []string {
	t.Helper()

	for quiet := 0; quiet < fleetGroupsWatched; quiet++ {
		if group := readFleetGroup(t, body, opened); !isHeartbeat(group) {
			return group
		}
	}
	t.Fatalf("no fleet change arrived within %d line groups", fleetGroupsWatched)
	return nil
}

func isHeartbeat(group []string) bool {
	return len(group) == 1 && group[0] == heartbeatLine
}

// fleetChange is one event as contracts/fleet-stream.md spells it on the wire,
// written out by hand here.
//
// Spelled by hand deliberately, and it is the whole of TestFleetPayloadIsIdOnly:
// an expectation built by calling fleetEvent would follow the code into whatever
// it started writing, and this is the one thing in the file that must not. The
// payload is an object with one member — no name, no path, no state, no owner —
// and the id is the daemon's own.
func fleetChange(kind session.FleetEventKind, id string) []string {
	return []string{"event: " + string(kind) + "\n", `data: {"id":"` + id + `"}` + "\n"}
}

// TestFleetStreamOwnershipFiltered is FR-019b: being a stream rather than a page
// does not exempt this route from the ownership check.
//
// Both sessions leave the fleet, the stranger's first. The order is the test: the
// manager publishes on the goroutine that changed the fleet, so an unfiltered
// stream would have the stranger's event queued *ahead* of the one this waits
// for, and the first change to arrive would name a session this identity may not
// see. A test that destroyed only the stranger's could be satisfied by a route
// that writes nothing at all.
func TestFleetStreamOwnershipFiltered(t *testing.T) {
	t.Parallel()

	const stranger auth.CallerID = "a-second-operator"

	f, addr := watching(t)
	theirs, _ := f.fixture.plant(t, session.Session{Owner: stranger, Name: "not yours", WorkDir: f.fixture.repo})
	mine, _ := f.fixture.plant(t, session.Session{Name: "mine", WorkDir: f.fixture.repo})

	opened := time.Now()
	resp := f.followTheFleet(t, addr) //nolint:bodyclose // followTheFleet closes it in t.Cleanup, which the linter cannot see through.
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s = %d; want %d", fleetStreamPath, resp.StatusCode, http.StatusOK)
	}
	body := bufio.NewReader(resp.Body)

	for _, gone := range []session.Session{theirs, mine} {
		if err := f.fixture.mgr.Destroy(context.Background(), gone); err != nil {
			t.Fatalf("Destroy(%s) = %v; want the fleet to lose it", gone.ID, err)
		}
	}

	got := awaitFleetChange(t, body, opened)
	if want := fleetChange(session.FleetVanished, mine.ID); !slices.Equal(got, want) {
		t.Fatalf("the first change on this identity's stream was %q; want %q — a session another identity owns must never produce a byte here",
			got, want)
	}
	if strings.Contains(strings.Join(got, ""), theirs.ID) {
		t.Errorf("the change names a session belonging to %s: %q", stranger, got)
	}

	// Nothing follows it either. The stranger's event was published first, so this
	// is belt and braces rather than the claim — but a route that delivered late
	// would fail here rather than passing quietly.
	for quiet := 0; quiet < ticksWatched; quiet++ {
		if group := readFleetGroup(t, body, opened); !isHeartbeat(group) {
			t.Fatalf("the stream wrote %q after the only change this identity owns; want heartbeats", group)
		}
	}
}

// TestFleetPayloadIsIdOnly is research R6 and FR-025 together: the event names
// what changed and carries nothing else.
//
// The session is created with a name and a working directory a search can find,
// because the failure this guards against is not a malformed payload — it is a
// helpful one. A `"name"` here would put a session field on a connection that
// lives for hours, on a route whose whole justification is that the page
// re-fetches the card it needs.
func TestFleetPayloadIsIdOnly(t *testing.T) {
	t.Parallel()

	const name = "payload-canary"

	f, addr := watching(t)

	opened := time.Now()
	resp := f.followTheFleet(t, addr) //nolint:bodyclose // followTheFleet closes it in t.Cleanup, which the linter cannot see through.
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s = %d; want %d", fleetStreamPath, resp.StatusCode, http.StatusOK)
	}
	body := bufio.NewReader(resp.Body)

	created, _, err := f.fixture.mgr.Create(context.Background(), session.CreateRequest{
		Owner: auth.CallerOperator, Name: name, WorkDir: f.fixture.repo,
	})
	if err != nil {
		t.Fatalf("Create = _, _, %v; want a session", err)
	}

	got := awaitFleetChange(t, body, opened)
	if want := fleetChange(session.FleetAppeared, created.ID); !slices.Equal(got, want) {
		t.Fatalf("the stream wrote %q; want %q", got, want)
	}

	// The same claim from the other side, so a failure says which field escaped
	// rather than only that the bytes differ.
	event := strings.Join(got, "")
	for what, secret := range map[string]string{
		"the session's name":                name,
		"the session's working directory":   f.fixture.repo,
		"the session's state":               string(created.State),
		"the owner the event was routed by": string(created.Owner),
	} {
		if strings.Contains(event, secret) {
			t.Errorf("the event carries %s: %q", what, event)
		}
	}
}

// TestOneRecordPerOpen is FR-023 on this route: the trail grows with requests,
// never with the fleet's activity.
//
// The count is taken after Shutdown for the reason
// TestOneStreamRequestLeavesExactlyOneRecordBehind takes its own there: the
// record is written at the open (FR-016a) and the middleware's deferred emit runs
// when the handler unwinds, which is whenever the browser goes away and on
// net/http's goroutine — so a count taken while the stream is open could only ever
// say "no second record has appeared *yet*". A drained connection is a handler
// that has finished, deferred emit included.
func TestOneRecordPerOpen(t *testing.T) {
	t.Parallel()

	f, addr := watching(t)
	doomed, _ := f.fixture.plant(t, session.Session{Name: "briefly here", WorkDir: f.fixture.repo})

	opened := time.Now()
	resp := f.followTheFleet(t, addr) //nolint:bodyclose // followTheFleet closes it in t.Cleanup, which the linter cannot see through.
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s = %d; want %d", fleetStreamPath, resp.StatusCode, http.StatusOK)
	}
	body := bufio.NewReader(resp.Body)

	rec := f.only(t)
	if got, want := rec["action"], string(audit.ActionFleetOpen); got != want {
		t.Errorf("the open was recorded as %v; want %v — an operator counting who watched the fleet must not be counting page loads with it", got, want)
	}
	if got, want := rec["decision"], string(audit.Allow); got != want {
		t.Errorf("decision = %v; want %v", got, want)
	}
	if rec["session_id"] != nil {
		t.Errorf("the record names a session (%v); this stream is about the fleet and not about one of them", rec["session_id"])
	}

	// Several changes, so that a record written per event would be several records.
	created, _, err := f.fixture.mgr.Create(context.Background(), session.CreateRequest{
		Owner: auth.CallerOperator, Name: "arrival", WorkDir: f.fixture.repo,
	})
	if err != nil {
		t.Fatalf("Create = _, _, %v; want a session", err)
	}
	if err := f.fixture.mgr.Destroy(context.Background(), doomed); err != nil {
		t.Fatalf("Destroy(%s) = %v; want the fleet to lose it", doomed.ID, err)
	}
	for _, want := range [][]string{
		fleetChange(session.FleetAppeared, created.ID),
		fleetChange(session.FleetVanished, doomed.ID),
	} {
		if got := awaitFleetChange(t, body, opened); !slices.Equal(got, want) {
			t.Fatalf("the stream wrote %q; want %q", got, want)
		}
	}

	// The browser goes away, and then the daemon stops.
	if err := resp.Body.Close(); err != nil {
		t.Fatalf("close the fleet stream: %v", err)
	}
	if err := f.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown() = %v; want a clean stop", err)
	}

	opens := 0
	for _, rec := range f.records(t) {
		if rec["action"] == string(audit.ActionFleetOpen) {
			opens++
		}
	}
	if opens != 1 {
		t.Errorf("one open of the fleet stream left %d %s records behind; want exactly one — FR-023 counts requests, and a record per event is a trail that grows with the fleet's activity",
			opens, audit.ActionFleetOpen)
	}
}

// TestTheFleetStreamAdmitsOnlyTheDashboardsOwnOpen is the contract's
// authorisation rows: layer 1, then crossSite, and nothing else.
//
// Every refusal is checked against the same bytes the pane stream refuses with,
// which is contracts/fleet-stream.md's own wording — a caller learns nothing from
// this route that they do not learn from any other page on this door, in
// particular not that it exists.
//
// The two admitted rows are the non-vacuity. Without them a handler that refused
// every open would pass, and the 500 they answer with is what an admitted request
// gets from a recorder that cannot lift a write deadline.
func TestTheFleetStreamAdmitsOnlyTheDashboardsOwnOpen(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		assertion func(t *testing.T, f *fleet) string
		site      string
		want      int
		action    audit.Action
		reason    string
	}{
		"the dashboard's own open": {
			assertion: mintedAssertion, site: secFetchSiteSameOrigin,
			want: http.StatusInternalServerError, action: audit.ActionFleetOpen,
		},
		"a client that is not a browser": {
			// No Sec-Fetch-Site at all: the quickstart's curl, and every other
			// non-browser client. crossSite admits it for the reason it admits one on
			// the pane stream — the header is sent by browsers and by nothing else, so
			// requiring it would refuse the callers it was never about while adding
			// nothing against the hostile page it is about.
			assertion: mintedAssertion, site: absent,
			want: http.StatusInternalServerError, action: audit.ActionFleetOpen,
		},
		"an open a hostile page triggered": {
			assertion: mintedAssertion, site: "cross-site",
			want: http.StatusUnauthorized, action: audit.ActionFleetOpen, reason: errFleetCrossSite.Error(),
		},
		"an open from a sibling of this site": {
			assertion: mintedAssertion, site: "same-site",
			want: http.StatusUnauthorized, action: audit.ActionFleetOpen, reason: errFleetCrossSite.Error(),
		},
		"an address opened by no page at all": {
			// `none` is a URL typed, bookmarked, or otherwise opened by nothing. The
			// dashboard opens this address from its own page, so a fleet stream
			// nobody's page asked for is not one this daemon owes anyone.
			assertion: mintedAssertion, site: "none",
			want: http.StatusUnauthorized, action: audit.ActionFleetOpen, reason: errFleetCrossSite.Error(),
		},
		"an open carrying no identity at all": {
			// Layer 1 refuses it in front of the handler, so the cross-site check never
			// runs and the record is the door's own rejection rather than this route's.
			assertion: func(*testing.T, *fleet) string { return absent }, site: secFetchSiteSameOrigin,
			want: http.StatusUnauthorized, action: audit.ActionAccessReject,
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			f := newFleet(t)
			w := f.askToFollowTheFleet(t, tc.assertion(t, f), tc.site)
			if w.Code != tc.want {
				t.Fatalf("%s answered %d (%s); want %d", name, w.Code, w.Body.String(), tc.want)
			}
			if tc.want == http.StatusUnauthorized && w.Body.String() != string(bodyBrowserRefused) {
				t.Errorf("the refusal answered %q; want the browser door's one refusal", w.Body.String())
			}
			if got := w.Header().Get(headerContentType); tc.want == http.StatusUnauthorized && got == contentTypeEventStream {
				t.Errorf("the refusal declared itself as %q, which says the open got somewhere", got)
			}

			rec := f.only(t)
			if got := rec["action"]; got != string(tc.action) {
				t.Errorf("the open was recorded as %v; want %v", got, tc.action)
			}
			if tc.reason != "" && rec["reason"] != tc.reason {
				t.Errorf("reason = %v; want %q — the trail is the only place the failed check is named", rec["reason"], tc.reason)
			}
		})
	}
}

func mintedAssertion(t *testing.T, f *fleet) string {
	t.Helper()
	return f.keys.mint(t, f.keys.claims())
}

// TestTwoTabsOnOneFleetBothHearTheChange is the ordinary case: an operator with
// the dashboard open twice.
//
// Subscribers are identified by pointer rather than by owner, so two tabs are two
// subscriptions rather than one — and the fan-out is the part that would break if
// they were not. A daemon that delivered to the first subscriber it found would
// pass every other test in this file.
func TestTwoTabsOnOneFleetBothHearTheChange(t *testing.T) {
	t.Parallel()

	f, addr := watching(t)

	opened := time.Now()
	first := f.followTheFleet(t, addr)  //nolint:bodyclose // followTheFleet closes it in t.Cleanup, which the linter cannot see through.
	second := f.followTheFleet(t, addr) //nolint:bodyclose // followTheFleet closes it in t.Cleanup, which the linter cannot see through.
	for _, tab := range []*http.Response{first, second} {
		if tab.StatusCode != http.StatusOK {
			t.Fatalf("GET %s = %d; want %d", fleetStreamPath, tab.StatusCode, http.StatusOK)
		}
	}

	created, _, err := f.fixture.mgr.Create(context.Background(), session.CreateRequest{
		Owner: auth.CallerOperator, Name: "seen-twice", WorkDir: f.fixture.repo,
	})
	if err != nil {
		t.Fatalf("Create = _, _, %v; want a session", err)
	}

	want := fleetChange(session.FleetAppeared, created.ID)
	for tab, resp := range map[string]*http.Response{"the first tab": first, "the second tab": second} {
		if got := awaitFleetChange(t, bufio.NewReader(resp.Body), opened); !slices.Equal(got, want) {
			t.Errorf("%s read %q; want %q", tab, got, want)
		}
	}
}
