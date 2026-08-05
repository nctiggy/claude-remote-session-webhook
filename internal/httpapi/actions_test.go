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
	"github.com/nctiggy/claude-remote-session-webhook/internal/auth"
	"github.com/nctiggy/claude-remote-session-webhook/internal/session"
)

// The uniform refusal, quoted from contracts/actions.md.
const (
	wantActionStatus      = http.StatusForbidden
	wantActionBody        = `<!doctype html><title>refused</title><p>This action was refused.</p>`
	wantActionContentType = "text/html; charset=utf-8"
	wantActionNosniff     = "nosniff"
)

// The uniform not-found, quoted from contracts/actions.md. Its content type and
// its nosniff header are the refusal's own, above: every response this door
// writes carries the identical pair, which is what FR-026 is for.
const (
	wantNotFoundStatus = http.StatusNotFound
	wantNotFoundBody   = `<!doctype html><title>not found</title><p>No such session.</p>`
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

// lookupDoor is the gate with the lookup all four action routes do first behind
// it: resolve the request's `{id}` against the operator layer 1 verified, and
// answer the uniform not-found when nothing comes back.
//
// It is a fixture handler rather than a route on the mux for the reason
// actionDoor is one — the four routes arrive with their own stories (T006, T009,
// T017, T020), and a test that invented a fifth would be asserting against a
// route contracts/actions.md does not have. What it does behind the gate is what
// sessionPage already does in front of it: Manager.View, resolveReason onto the
// record, and the door's own not-found. Everything about *which* session the
// three causes below produce is therefore the real resolver's answer and not
// this file's opinion of it.
type lookupDoor struct {
	*testServer
	served  int
	handler http.Handler
}

func newLookupDoor(t *testing.T, browser layer1) *lookupDoor {
	t.Helper()

	d := &lookupDoor{testServer: newAuditedServerWith(t, browser)}
	d.handler = d.authorizeAction(audit.ActionDashboardDestroy, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		operator, ok := OperatorFrom(r.Context())
		if !ok {
			t.Errorf("the gate admitted a request with no operator in its context")
			return
		}

		if _, err := d.sessions.View(r.PathValue(pathValueID), operator.Owner); err != nil {
			AuditFrom(r.Context()).Deny(resolveReason(err).Error())
			d.notFoundAction(w)
			return
		}
		d.served++
		w.WriteHeader(http.StatusOK)
	}))
	return d
}

// act is one well-formed action against an identifier: everything the gate asks
// for is satisfied, so the only thing left that can refuse is the lookup.
//
// The path value is set by hand because no mux filled it in — the fixture is a
// handler and not a registered route — and it carries the same bytes the path
// does, so the resolver reads what a real `{id}` wildcard would have given it.
func (d *lookupDoor) act(t *testing.T, assertion, id string) *httptest.ResponseRecorder {
	t.Helper()

	form := url.Values{}
	form.Set(fieldPageToken, mustMint(t, d.pageKey, testOperatorEmail, testTime))

	r := httptest.NewRequest(http.MethodPost, "/dashboard/sessions/"+id+"/destroy", strings.NewReader(form.Encode()))
	r.SetPathValue(pathValueID, id)
	r.Header.Set(headerContentType, contentTypeForm)
	r.Header.Set(headerAccessAssertion, assertion)
	r.Header.Set(headerSecFetchSite, secFetchSiteSameOrigin)

	w := httptest.NewRecorder()
	d.handler.ServeHTTP(w, r)
	return w
}

// notFoundCause is one of the three ways an action can name a session that is
// not there, and the reason each leaves on the record.
//
// Two of the three resolve to the same sentinel and that is the resolver's own
// doing (FR-032, FR-033): Store.Get takes the owner, so an unknown identifier
// and another operator's are one answer from one lookup. The third is distinct
// on the record and identical to the caller, which is the shape this whole test
// is about.
type notFoundCause struct {
	name   string
	id     func(t *testing.T, d *lookupDoor) string
	reason error
}

func notFoundCauses() []notFoundCause {
	// Not on the allowlist and not testOperatorEmail's owner: a second operator
	// whose sessions this viewer must not be able to detect the existence of.
	const stranger auth.CallerID = "a-second-operator"

	return []notFoundCause{
		{
			name: "an identifier no session on this host ever had",
			// Well-formed — 32 lowercase hex — so the route would match it and the
			// refusal under test is the lookup's rather than the router's.
			id:     func(*testing.T, *lookupDoor) string { return strings.Repeat("c", session.IDLen) },
			reason: session.ErrSessionNotFound,
		},
		{
			name: "a session another operator owns",
			id: func(t *testing.T, d *lookupDoor) string {
				t.Helper()
				theirs, _ := d.fixture.plant(t, session.Session{
					Owner: stranger, Name: "not yours", WorkDir: d.fixture.repo,
				})
				return theirs.ID
			},
			reason: session.ErrSessionNotFound,
		},
		{
			name: "a session of the operator's own that is no longer there",
			id: func(t *testing.T, d *lookupDoor) string {
				t.Helper()
				gone, _ := d.fixture.plant(t, session.Session{
					Name: "already gone", WorkDir: d.fixture.repo, State: session.StateDead,
				})
				return gone.ID
			},
			reason: session.ErrSessionDead,
		},
	}
}

// TestNotFoundUniform is FR-017 and SC-009: an identifier that never existed,
// one another operator owns, and one whose session is already gone are one
// answer, byte for byte, to the caller.
//
// **Must fail when** unknown and not-owned produce different bytes. That is what
// the whole-header comparison and the Content-Length claim are for, and it is
// why the three causes are driven through the real resolver rather than by
// calling the not-found three times: a test that did the latter would be
// asserting that one function agrees with itself, and would go on passing when a
// route grew a branch that answered "not yours" more helpfully than "unknown".
//
// The record is asserted too, in the opposite direction. FR-026 wants an
// operator to be able to read which of the three it was; FR-017 wants a caller
// to be unable to. Both claims are made below against the same three requests.
//
// The last case is the non-vacuity: the same fixture, for a session this
// operator does own, serves. Without it every comparison here is satisfied by a
// door that answers 404 to everyone.
func TestNotFoundUniform(t *testing.T) {
	t.Parallel()

	keys := newKeyServer(t)
	assertion := keys.mint(t, keys.claims())

	var (
		firstName    string
		firstBody    string
		firstHeaders http.Header
	)

	for _, c := range notFoundCauses() {
		d := newLookupDoor(t, keys.validator(t))
		w := d.act(t, assertion, c.id(t, d))

		if w.Code != wantNotFoundStatus {
			t.Errorf("%s: answered %d; want %d", c.name, w.Code, wantNotFoundStatus)
		}
		if got := w.Body.String(); got != wantNotFoundBody {
			t.Errorf("%s: body\n%s\nwant\n%s", c.name, got, wantNotFoundBody)
		}
		if got, want := w.Header().Get(headerContentType), wantActionContentType; got != want {
			t.Errorf("%s: %s = %q; want %q", c.name, headerContentType, got, want)
		}
		if got, want := w.Header().Get(headerContentTypeOptions), wantActionNosniff; got != want {
			t.Errorf("%s: %s = %q; want %q", c.name, headerContentTypeOptions, got, want)
		}
		if got, want := w.Header().Get(headerContentLength), strconv.Itoa(len(wantNotFoundBody)); got != want {
			t.Errorf("%s: %s = %q; want %q", c.name, headerContentLength, got, want)
		}

		// Nothing was acted on. An action that tore a session down and then
		// answered "no such session" would be uniform about nothing that matters.
		if d.served != 0 {
			t.Errorf("%s: the handler acted %d times; want 0", c.name, d.served)
		}

		// The trail names which of the three it was. The response, asserted above,
		// does not — and this is the direct claim that it does not: the reason's
		// own words are searched for in what the caller received.
		rec := d.only(t)
		if got, want := rec["action"], string(audit.ActionDashboardDestroy); got != want {
			t.Errorf("%s: action = %v; want %v — an identity that passed the gate and then named nothing "+
				"is not a cross-site rejection", c.name, got, want)
		}
		if got, want := rec["decision"], string(audit.Deny); got != want {
			t.Errorf("%s: decision = %v; want %v", c.name, got, want)
		}
		if got, want := rec["reason"], c.reason.Error(); got != want {
			t.Errorf("%s: reason = %v; want %v — the record is the only place the cause is named",
				c.name, got, want)
		}
		if strings.Contains(w.Body.String(), c.reason.Error()) {
			t.Errorf("%s: the response quotes the reason back:\n%s", c.name, w.Body.String())
		}

		if firstHeaders == nil {
			firstName, firstBody, firstHeaders = c.name, w.Body.String(), w.Header().Clone()
			continue
		}
		if got := w.Body.String(); got != firstBody {
			t.Errorf("%s answers\n%s\n%s answers\n%s\nthe two are distinguishable", c.name, got, firstName, firstBody)
		}
		if !maps.EqualFunc(firstHeaders, w.Header(), slices.Equal) {
			t.Errorf("%s answers with headers %v; %s answers with %v — the two are distinguishable",
				c.name, w.Header(), firstName, firstHeaders)
		}
	}

	served := newLookupDoor(t, keys.validator(t))
	mine, _ := served.fixture.plant(t, session.Session{Name: "mine to act on", WorkDir: served.fixture.repo})
	if w := served.act(t, assertion, mine.ID); w.Code != http.StatusOK {
		t.Fatalf("an action against a session this operator owns answered %d (%s); want %d — "+
			"every refusal above is satisfied by a door that refuses everyone",
			w.Code, w.Body.String(), http.StatusOK)
	}
	if served.served != 1 {
		t.Errorf("the handler behind the lookup acted %d times for a session the operator owns; want exactly 1", served.served)
	}
}
