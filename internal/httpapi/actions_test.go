// Internal test, matching browser_test.go: the gate under test is an unexported
// method, and two of the three properties it is asked about are only visible from
// inside — the pending audit record, and whether the handler behind the gate ever
// ran at all.
//
// Every literal these cases compare against is written out here rather than read
// from the constant the code writes. contracts/actions.md fixes the status, the
// headers and the body byte for byte; a test that asserted against
// bodyActionRefused would prove only that the code agrees with itself, and would
// go on passing through an edit to the contract's own words.
package httpapi

import (
	"bytes"
	"maps"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/nctiggy/claude-remote-session-webhook/internal/audit"
)

// The uniform refusal, quoted from contracts/actions.md.
const (
	wantActionStatus      = http.StatusForbidden
	wantActionBody        = `<!doctype html><title>refused</title><p>This action was refused.</p>`
	wantActionContentType = "text/html; charset=utf-8"
	wantActionNosniff     = "nosniff"
)

// actionPath is a route-shaped path for a request that is driven straight at the
// gate. Nothing looks a session up in these cases — the gate refuses or admits
// before any handler runs — so the identifier only has to be the shape the route
// table will match.
const actionPath = "/dashboard/sessions/0123456789abcdef0123456789abcdef/destroy"

// contentTypeForm is what a browser sends a submitted form as, and the one
// content type ParseForm reads a body for.
const contentTypeForm = "application/x-www-form-urlencoded"

// actionDoor is the browser door with the action gate on it, and everything
// behind it readable: the trail it writes, and whether the guarded handler ran.
//
// It is newDoorFor's shape rather than a route registered on the mux, for the
// reason that fixture has it: the four action routes arrive with their own
// stories (T006, T009, T017, T020), and a test that invented a fifth would be
// asserting against a route contracts/actions.md does not have.
type actionDoor struct {
	*testServer
	served  int
	handler http.Handler
}

func newActionDoor(t *testing.T, browser layer1) *actionDoor {
	t.Helper()

	d := &actionDoor{testServer: newAuditedServerWith(t, browser)}
	d.handler = d.authorizeAction(audit.ActionDashboardDestroy, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		d.served++
		w.WriteHeader(http.StatusOK)
	}))
	return d
}

// actionRequest is one attempt at a mutating dashboard route, in the three parts
// the gate reads: who the edge said this is, where the browser said it came from,
// and what the form carried.
//
// Each may be absent, which is a different shape from present-and-empty and has
// to be reachable as its own case — the absent Sec-Fetch-Site is one of the five
// causes contracts/actions.md requires to be indistinguishable.
type actionRequest struct {
	assertion string
	site      string
	token     string
}

func (d *actionDoor) post(a actionRequest) *httptest.ResponseRecorder {
	form := url.Values{}
	if a.token != absent {
		form.Set(fieldPageToken, a.token)
	}

	r := httptest.NewRequest(http.MethodPost, actionPath, strings.NewReader(form.Encode()))
	r.Header.Set(headerContentType, contentTypeForm)
	if a.assertion != absent {
		r.Header.Set(headerAccessAssertion, a.assertion)
	}
	if a.site != absent {
		r.Header.Set(headerSecFetchSite, a.site)
	}

	w := httptest.NewRecorder()
	d.handler.ServeHTTP(w, r)
	return w
}

// gateReasons is every reason the gate itself can record — the two authored in
// browser.go and pagetoken.go's four. A test asserting that one of these did
// *not* reach the trail is asserting that the check that produces it never ran.
func gateReasons() []error {
	return []error{
		errActionCrossSite,
		errActionFormUnreadable,
		errPageTokenMissing,
		errPageTokenMalformed,
		errPageTokenExpired,
		errPageTokenMismatch,
	}
}

// TestActionGateOrder is FR-003 and FR-008 together: the three checks run in the
// order contracts/actions.md fixes them, layer 1 first, and a request layer 1
// refuses is never asked for a token at all.
//
// **Must fail when** the order is swapped. A gate placed in front of layer 1
// answers the first case below with the action refusal and a dashboard.reject
// record, so the status, the body and the recorded reason all move — and the last
// of those is the direct claim: a reason from the token check on a request layer 1
// refused is the token check having run for an identity the daemon never verified.
//
// The second and third cases are not decoration. Without them a gate that never
// ran at all would satisfy the first, and the assertion would be about a check
// that does not exist rather than about the order two existing checks run in.
func TestActionGateOrder(t *testing.T) {
	t.Parallel()

	keys := newKeyServer(t)
	assertion := keys.mint(t, keys.claims())

	// Layer 1 fails, and steps 2 and 3 would fail too: no assertion, a cross-site
	// initiator, and no token. The answer must be layer 1's alone.
	refused := newActionDoor(t, keys.validator(t))
	w := refused.post(actionRequest{assertion: absent, site: "cross-site", token: absent})

	if w.Code != http.StatusUnauthorized {
		t.Errorf("a request layer 1 refused was answered %d; want %d — the door in front of the gate",
			w.Code, http.StatusUnauthorized)
	}
	if !bytes.Equal(w.Body.Bytes(), bodyBrowserRefused) {
		t.Errorf("a request layer 1 refused was answered:\n%s\nwant layer 1's own refusal:\n%s",
			w.Body.String(), bodyBrowserRefused)
	}
	if refused.served != 0 {
		t.Errorf("the handler behind the gate ran %d times for a request layer 1 refused; want 0", refused.served)
	}

	rec := refused.only(t)
	if got, want := rec["action"], string(audit.ActionAccessReject); got != want {
		t.Errorf("action = %v; want %v — a layer-1 failure is not a cross-site rejection", got, want)
	}
	for _, reason := range gateReasons() {
		if rec["reason"] == reason.Error() {
			t.Errorf("a request layer 1 refused was recorded with the gate's reason %q; "+
				"the gate ran ahead of layer 1", reason)
		}
	}

	// Layer 1 satisfied, step 2 failing. This is what makes the case above about
	// the *order* rather than about a gate that is never reached.
	crossed := newActionDoor(t, keys.validator(t))
	w = crossed.post(actionRequest{
		assertion: assertion,
		site:      "cross-site",
		token:     mustMint(t, crossed.pageKey, testOperatorEmail, testTime),
	})

	if w.Code != wantActionStatus {
		t.Errorf("a verified identity acting cross-site was answered %d; want %d", w.Code, wantActionStatus)
	}
	if crossed.served != 0 {
		t.Errorf("the handler behind the gate ran %d times for a cross-site request; want 0 — "+
			"FR-003 puts the gate ahead of every state change", crossed.served)
	}
	if got, want := crossed.only(t)["action"], string(audit.ActionDashboardReject); got != want {
		t.Errorf("action = %v; want %v", got, want)
	}

	// All three satisfied, so every refusal above is a refusal and not a fixture
	// that never worked.
	served := newActionDoor(t, keys.validator(t))
	w = served.post(actionRequest{
		assertion: assertion,
		site:      secFetchSiteSameOrigin,
		token:     mustMint(t, served.pageKey, testOperatorEmail, testTime),
	})

	if w.Code != http.StatusOK {
		t.Fatalf("a request satisfying all three checks was answered %d (%s); want %d",
			w.Code, w.Body.String(), http.StatusOK)
	}
	if served.served != 1 {
		t.Errorf("the handler behind the gate ran %d times for an admitted request; want exactly 1", served.served)
	}
	rec = served.only(t)
	if got, want := rec["action"], string(audit.ActionDashboardDestroy); got != want {
		t.Errorf("action = %v; want %v — an admitted action is recorded as the action, not as a rejection", got, want)
	}
	if got, want := rec["decision"], string(audit.Allow); got != want {
		t.Errorf("decision = %v; want %v", got, want)
	}
}

// refusalCause is one of the five ways contracts/actions.md requires the gate to
// refuse, and the reason each must leave on the record.
//
// The reasons differ from each other on purpose. FR-026 wants an operator to be
// able to read which check refused; FR-004 wants a caller to be unable to. Both
// claims are made below against the same five requests.
type refusalCause struct {
	name   string
	site   string
	token  func(t *testing.T, k pageKey) string
	reason error
}

func refusalCauses() []refusalCause {
	valid := func(t *testing.T, k pageKey) string {
		t.Helper()
		return mustMint(t, k, testOperatorEmail, testTime)
	}

	return []refusalCause{
		{
			name:   "the browser said the request came from another site",
			site:   "cross-site",
			token:  valid,
			reason: errActionCrossSite,
		},
		{
			// The case an Origin comparison cannot cleanly see, and the reason
			// crossSiteAction is stricter here than crossSite is on the pane
			// stream: a mutating request nobody's page asked for is not one this
			// daemon owes anyone.
			name:   "the browser said nothing about where the request came from",
			site:   absent,
			token:  valid,
			reason: errActionCrossSite,
		},
		{
			name:   "the form carried no token field",
			site:   secFetchSiteSameOrigin,
			token:  func(*testing.T, pageKey) string { return absent },
			reason: errPageTokenMissing,
		},
		{
			name:   "the token is not an expiry and a MAC",
			site:   secFetchSiteSameOrigin,
			token:  func(*testing.T, pageKey) string { return "not-a-page-token" },
			reason: errPageTokenMalformed,
		},
		{
			name: "the token was minted more than its lifetime ago",
			site: secFetchSiteSameOrigin,
			token: func(t *testing.T, k pageKey) string {
				t.Helper()
				// Genuinely expired rather than approximated: minted by the real
				// code, with a real MAC over a real expiry, one hour past it.
				return mustMint(t, k, testOperatorEmail, testTime.Add(-pageTokenLifetime-time.Hour))
			},
			reason: errPageTokenExpired,
		},
	}
}

// TestRefusalIsByteIdentical is FR-004: all five causes produce the same status,
// the same headers, the same body and the same Content-Length, so a caller cannot
// tell which check refused them — or that there is more than one to fail.
//
// **Must fail when** any cause produces a distinguishable response. That is the
// whole point of comparing the header maps whole rather than field by field: a
// future cause that added one header, or shortened the body by a character, is
// caught without anyone having remembered to assert against it.
//
// Content-Length is compared as its own claim as well as inside the header map,
// because it is the one field that a body differing by a single byte moves while
// every other part of the response stays put.
func TestRefusalIsByteIdentical(t *testing.T) {
	t.Parallel()

	keys := newKeyServer(t)
	assertion := keys.mint(t, keys.claims())

	var (
		firstName    string
		firstHeaders http.Header
	)

	for _, c := range refusalCauses() {
		d := newActionDoor(t, keys.validator(t))
		w := d.post(actionRequest{assertion: assertion, site: c.site, token: c.token(t, d.pageKey)})

		if w.Code != wantActionStatus {
			t.Errorf("%s: answered %d; want %d", c.name, w.Code, wantActionStatus)
		}
		if got := w.Body.String(); got != wantActionBody {
			t.Errorf("%s: body\n%s\nwant\n%s", c.name, got, wantActionBody)
		}
		if got, want := w.Header().Get(headerContentType), wantActionContentType; got != want {
			t.Errorf("%s: %s = %q; want %q", c.name, headerContentType, got, want)
		}
		if got, want := w.Header().Get(headerContentTypeOptions), wantActionNosniff; got != want {
			t.Errorf("%s: %s = %q; want %q", c.name, headerContentTypeOptions, got, want)
		}
		if got, want := w.Header().Get(headerContentLength), strconv.Itoa(len(wantActionBody)); got != want {
			t.Errorf("%s: %s = %q; want %q", c.name, headerContentLength, got, want)
		}

		// Nothing was acted on. A uniform refusal that had already torn a session
		// down would be uniform about nothing that matters (FR-003).
		if d.served != 0 {
			t.Errorf("%s: the handler behind the gate ran %d times; want 0", c.name, d.served)
		}

		// The trail names the check. The response, asserted above, does not — and
		// this is the direct claim that it does not: the reason's own words are
		// searched for in what the caller received.
		rec := d.only(t)
		if got, want := rec["action"], string(audit.ActionDashboardReject); got != want {
			t.Errorf("%s: action = %v; want %v", c.name, got, want)
		}
		if got, want := rec["decision"], string(audit.Deny); got != want {
			t.Errorf("%s: decision = %v; want %v", c.name, got, want)
		}
		if got, want := rec["reason"], c.reason.Error(); got != want {
			t.Errorf("%s: reason = %v; want %v — the record is the only place the failed check is named",
				c.name, got, want)
		}
		if strings.Contains(w.Body.String(), c.reason.Error()) {
			t.Errorf("%s: the response quotes the reason back:\n%s", c.name, w.Body.String())
		}

		if firstHeaders == nil {
			firstName, firstHeaders = c.name, w.Header().Clone()
			continue
		}
		if !maps.EqualFunc(firstHeaders, w.Header(), slices.Equal) {
			t.Errorf("%s answers with headers %v; %s answers with %v — the two refusals are distinguishable",
				c.name, w.Header(), firstName, firstHeaders)
		}
	}
}
