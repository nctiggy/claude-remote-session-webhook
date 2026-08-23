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
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/nctiggy/claude-remote-session-webhook/internal/audit"
	"github.com/nctiggy/claude-remote-session-webhook/internal/auth"
	"github.com/nctiggy/claude-remote-session-webhook/internal/config"
	"github.com/nctiggy/claude-remote-session-webhook/internal/session"
	"github.com/nctiggy/claude-remote-session-webhook/internal/tmuxctl"
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

// wantOutcome is what every one of the four actions answers with after T014: a
// 303 to the fleet carrying one code out of outcome.go's closed vocabulary, and
// no body at all.
//
// The location is spelled here rather than built by calling redirectOutcome, for
// the reason every body in this file is quoted rather than read from the
// constant it asserts: a test that asked the code what it writes proves only
// that the code agrees with itself.
//
// 303 rather than 302 is the load-bearing half. It is the status that turns the
// POST into a GET on the redirect, so the reload an operator reaches for after
// an action re-fetches the fleet instead of re-submitting the action — and a
// re-submitted create is a second unsandboxed shell.
func wantOutcome(t *testing.T, w *httptest.ResponseRecorder, code outcome) {
	t.Helper()

	if w.Code != http.StatusSeeOther {
		t.Fatalf("status = %d (%s); want %d — an action answers with a redirect to the fleet",
			w.Code, w.Body.String(), http.StatusSeeOther)
	}
	if got, want := w.Header().Get(headerLocation), "/?outcome="+string(code); got != want {
		t.Errorf("%s = %q; want %q", headerLocation, got, want)
	}
	// A 303 nobody renders is a body that only ever appears when something else
	// has already gone wrong, and a fragment left here would be the page an
	// operator lands on when their browser declines to follow.
	if got := strings.TrimSpace(w.Body.String()); got != "" {
		t.Errorf("the redirect carried a body: %q; want none", got)
	}
	if got, want := w.Header().Get(headerContentTypeOptions), wantActionNosniff; got != want {
		t.Errorf("%s = %q; want %q — an action's answer carries the same headers a page does", headerContentTypeOptions, got, want)
	}
}

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

// --- US1: destroy from the browser (T006) ----------------------------------
//
// The two fixtures above drive handlers of the test's own making, because the
// gate and the uniform not-found are answers no route had yet. Everything below
// drives the **registered** route through Server.ServeHTTP instead, and that is
// load-bearing rather than tidy: a destroy wired with handleBrowser instead of
// handleAction would leave every case above green with the milestone's entire
// cross-site defence absent from the one route that can end an unsandboxed shell.

// The destroy's own answers, which are outcome codes since T014 rather than the
// three fragments this route used to write. Each is spelled here rather than
// read from the constant the code writes, for the reason the refusal above is: a
// test asserting against the variable proves only that the code agrees with
// itself.
//
// What each code *says* is asserted where the page renders it, not here. A
// route's claim is now the pair — the code it chose, and the sentence the fleet
// maps it to — and splitting the two assertions is what keeps a redirect to the
// wrong code from being invisible behind copy that reads fine.
const (
	wantDestroyedOutcome   = outcome("destroyed")
	wantUnconfirmedOutcome = outcome("unconfirmed")
	wantUnverifiedOutcome  = outcome("teardown-unverified")
)

// destroyer is the registered destroy route with everything behind it readable:
// the store, the fake host, and the trail.
type destroyer struct {
	*testServer
	keys *keyServer
}

func newDestroyer(t *testing.T) *destroyer {
	t.Helper()

	keys := newKeyServer(t)
	return &destroyer{testServer: newAuditedServerWith(t, keys.validator(t)), keys: keys}
}

// live plants a running session of this operator's own, together with the tmux
// window the record names — the state a card on the fleet describes.
func (d *destroyer) live(t *testing.T) session.Session {
	t.Helper()

	planted, _ := d.fixture.plant(t, session.Session{Name: "to be destroyed", WorkDir: d.fixture.repo})
	return planted
}

// confirmed is the form a rendered card submits: the render's page token, and
// the confirming step FR-029 requires.
func (d *destroyer) confirmed(t *testing.T) url.Values {
	t.Helper()

	form := url.Values{}
	form.Set(fieldPageToken, mustMint(t, d.pageKey, testOperatorEmail, testTime))
	form.Set(fieldConfirm, confirmYes)
	return form
}

// post submits one form at the destroy route, as the browser this daemon
// rendered the page for.
func (d *destroyer) post(t *testing.T, id string, form url.Values) *httptest.ResponseRecorder {
	t.Helper()

	return d.send(t, http.MethodPost, "/dashboard/sessions/"+id+"/destroy", secFetchSiteSameOrigin, form)
}

// send is post with the method, the path, and the browser's own account of where
// the request came from all chosen by the caller — the three things the
// acceptance suite below has to vary and a rendered card never does.
//
// post is expressed through it rather than beside it so that the request a
// varied case sends differs from the ordinary one in exactly the field it means
// to vary. Two builders would be two requests free to drift apart, and a case
// refused for a difference it did not intend is a case proving something else.
//
// An initiator of absent sends no Sec-Fetch-Site header at all, which is a
// different shape from sending an empty one and is one of the causes
// contracts/actions.md requires to refuse.
func (d *destroyer) send(t *testing.T, method, path, site string, form url.Values) *httptest.ResponseRecorder {
	t.Helper()

	r := httptest.NewRequest(method, path, strings.NewReader(form.Encode()))
	r.Header.Set(headerContentType, contentTypeForm)
	r.Header.Set(headerAccessAssertion, d.keys.mint(t, d.keys.claims()))
	if site != absent {
		r.Header.Set(headerSecFetchSite, site)
	}

	w := httptest.NewRecorder()
	d.ServeHTTP(w, r)
	return w
}

// standing is what the daemon and the host say about a session after a request:
// whether the record survived, and whether the window did.
//
// The two are asserted together everywhere below because they answer different
// halves of the same question. A record dropped for a window that is still there
// is the orphan Principle VI forbids; a window torn down for a request that was
// refused is the state change FR-003 forbids.
func (d *destroyer) standing(t *testing.T, s session.Session) (recorded, running bool) {
	t.Helper()

	_, err := d.fixture.store.Get(s.ID, auth.CallerOperator)
	switch {
	case err == nil:
		recorded = true
	case !errors.Is(err, session.ErrSessionNotFound):
		t.Fatalf("read the store for session %s: %v", s.ID, err)
	}

	running, err = d.fixture.tmux.Has(context.Background(), s.TmuxName())
	if err != nil {
		t.Fatalf("ask the fake host about %s: %v", s.TmuxName(), err)
	}
	return recorded, running
}

// kills counts the teardowns that reached the host, which is the only way to
// tell a refusal that acted first from one that did not act at all: a kill whose
// session survived leaves the store and the host looking exactly like a request
// that was refused before it ran.
func (d *destroyer) kills() int {
	n := 0
	for _, c := range d.fixture.tmux.Calls() {
		if c.Op == tmuxctl.OpKill {
			n++
		}
	}
	return n
}

// TestDestroyTearsTheSessionDown is US1's first scenario end to end: the
// operator's own session, destroyed from the card, torn down with the teardown
// verified, its record and credential hash cleared, and one record in the trail.
//
// **Must fail when** the route stops calling Manager.Destroy, or answers before
// the host has confirmed: the window assertion goes red on the first and the
// outcome on the second.
//
// The answer is asserted as a redirect carrying the destroyed code rather than
// as a fragment (T014). A destroy that answered with a bare 303 to the fleet and
// no code would leave the operator looking at a card that quietly vanished,
// which FR-030 and FR-031 both forbid — the sentence has simply moved to the
// page they land on.
func TestDestroyTearsTheSessionDown(t *testing.T) {
	t.Parallel()

	d := newDestroyer(t)
	live := d.live(t)

	w := d.post(t, live.ID, d.confirmed(t))

	wantOutcome(t, w, wantDestroyedOutcome)

	if recorded, running := d.standing(t, live); recorded || running {
		t.Errorf("after a verified teardown the record is %v and the window is %v; want both gone",
			recorded, running)
	}
	if got := d.kills(); got != 1 {
		t.Errorf("the host was asked to kill %d times; want exactly 1 — a retried destroy is a second destroy (AR-006)", got)
	}

	rec := d.only(t)
	if got, want := rec["action"], string(audit.ActionDashboardDestroy); got != want {
		t.Errorf("action = %v; want %v — a browser destroy is not the API's session.destroy", got, want)
	}
	if got, want := rec["decision"], string(audit.Allow); got != want {
		t.Errorf("decision = %v; want %v", got, want)
	}
	if got := rec["session_id"]; got != live.ID {
		t.Errorf("session_id = %v; want %q", got, live.ID)
	}
}

// TestDestroyRunsBehindTheActionGate is the claim T003 cannot make about itself:
// the route that ends an unsandboxed shell is registered *through* the gate.
//
// **Must fail when** the route is registered with handleBrowser rather than
// handleAction. Both halves of the defence are driven here because either one
// alone leaves the other's absence invisible on this route — the formal
// independence proof is T008's, and this is the registration claim it rests on.
//
// The session is asserted alive afterwards, and the kill count with it. A gate
// that refused *after* the handler ran would answer 403 and still have torn the
// session down, which is the one failure a status code cannot see (FR-003).
func TestDestroyRunsBehindTheActionGate(t *testing.T) {
	t.Parallel()

	cases := map[string]func(t *testing.T, d *destroyer) (url.Values, string){
		"the form carried no page token": func(t *testing.T, d *destroyer) (url.Values, string) {
			t.Helper()
			form := d.confirmed(t)
			form.Del(fieldPageToken)
			return form, secFetchSiteSameOrigin
		},
		"the browser said the request came from another site": func(t *testing.T, d *destroyer) (url.Values, string) {
			t.Helper()
			return d.confirmed(t), "cross-site"
		},
	}

	for name, arrange := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			d := newDestroyer(t)
			live := d.live(t)
			form, site := arrange(t, d)

			r := httptest.NewRequest(http.MethodPost, "/dashboard/sessions/"+live.ID+"/destroy", strings.NewReader(form.Encode()))
			r.Header.Set(headerContentType, contentTypeForm)
			r.Header.Set(headerAccessAssertion, d.keys.mint(t, d.keys.claims()))
			r.Header.Set(headerSecFetchSite, site)

			w := httptest.NewRecorder()
			d.ServeHTTP(w, r)

			if w.Code != wantActionStatus {
				t.Fatalf("status = %d (%s); want %d — the destroy route is not behind the gate",
					w.Code, w.Body.String(), wantActionStatus)
			}
			if got := w.Body.String(); got != wantActionBody {
				t.Errorf("body\n%s\nwant the gate's uniform refusal\n%s", got, wantActionBody)
			}

			recorded, running := d.standing(t, live)
			if !recorded || !running {
				t.Errorf("a refused destroy left the record %v and the window %v; want both untouched",
					recorded, running)
			}
			if got := d.kills(); got != 0 {
				t.Errorf("a refused destroy asked the host to kill %d times; want 0 — the gate runs before any state change", got)
			}
			if got, want := d.only(t)["action"], string(audit.ActionDashboardReject); got != want {
				t.Errorf("action = %v; want %v", got, want)
			}
		})
	}
}

// TestDestroyRequiresConfirm is FR-029: a destroy without the confirming step is
// refused, and nothing is torn down.
//
// **Must fail when** the confirm check is removed — every case below then answers
// the destroyed outcome with the session gone.
//
// The confirming value is compared rather than interpreted, which is what the
// four near-misses are for. `on`, `true` and an upper-case `YES` are what a stray
// checkbox, a hand-built request and a helpful client produce, and none of them
// is the deliberate act FR-029 asks for on the one action that cannot be undone.
//
// The last case is the non-vacuity: the same form with `yes` destroys. Without it
// every assertion here is satisfied by a route that refuses everything.
func TestDestroyRequiresConfirm(t *testing.T) {
	t.Parallel()

	cases := map[string]string{
		"the field is absent":        absent,
		"the field is empty":         "",
		"the operator said no":       "no",
		"a checkbox said on":         "on",
		"a client said true":         "true",
		"the value is not lowercase": "YES",
	}

	for name, value := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			d := newDestroyer(t)
			live := d.live(t)

			form := d.confirmed(t)
			if value == absent {
				form.Del(fieldConfirm)
			} else {
				form.Set(fieldConfirm, value)
			}

			w := d.post(t, live.ID, form)

			wantOutcome(t, w, wantUnconfirmedOutcome)

			recorded, running := d.standing(t, live)
			if !recorded || !running {
				t.Errorf("an unconfirmed destroy left the record %v and the window %v; want both untouched",
					recorded, running)
			}
			if got := d.kills(); got != 0 {
				t.Errorf("an unconfirmed destroy asked the host to kill %d times; want 0", got)
			}

			rec := d.only(t)
			if got, want := rec["action"], string(audit.ActionDashboardDestroy); got != want {
				t.Errorf("action = %v; want %v — the gate admitted this request; the confirming step is what refused it", got, want)
			}
			if got, want := rec["decision"], string(audit.Deny); got != want {
				t.Errorf("decision = %v; want %v", got, want)
			}
			if got, want := rec["reason"], errDestroyUnconfirmed.Error(); got != want {
				t.Errorf("reason = %v; want %v", got, want)
			}
		})
	}

	confirmed := newDestroyer(t)
	live := confirmed.live(t)
	if w := confirmed.post(t, live.ID, confirmed.confirmed(t)); w.Header().Get(headerLocation) != "/?outcome="+string(wantDestroyedOutcome) {
		t.Fatalf("a confirmed destroy answered %d to %q; want the destroyed outcome — every refusal above is satisfied by a route that refuses everyone",
			w.Code, w.Header().Get(headerLocation))
	}
	if _, running := confirmed.standing(t, live); running {
		t.Error("a confirmed destroy left the window running")
	}
}

// TestDestroyUnverifiedTeardown is FR-010 and AR-004: a teardown the host will
// not confirm is reported as unverified, the record is **kept**, and there is no
// way to make the daemon claim otherwise.
//
// **Must fail when** the verification is skipped or its result ignored — the
// outcome code and the retained record move together, because Manager.Destroy
// only ever drops a record after confirmGone said the window is gone.
//
// The three arrangements are the three shapes a teardown fails in, taken from the
// API door's own suite so both doors are asked the same question: a kill that
// reported success and left the session there, a kill that failed outright, and a
// host that cannot say. The last one counts as surviving because Principle VI
// does not let an unanswered question be reported as a teardown.
func TestDestroyUnverifiedTeardown(t *testing.T) {
	t.Parallel()

	const tmuxMarker = "no-such-tmux-binary"

	cases := map[string]func(d *destroyer, live session.Session){
		"the kill reported success and the session is still there": func(d *destroyer, live session.Session) {
			d.fixture.tmux.SurviveKill(live.TmuxName())
		},
		"the kill itself failed": func(d *destroyer, _ session.Session) {
			d.fixture.tmux.FailOp(tmuxctl.OpKill, errors.New(tmuxMarker))
		},
		"the host cannot say whether it is gone": func(d *destroyer, _ session.Session) {
			// Both, because confirmGone falls back to List when Has cannot answer.
			d.fixture.tmux.FailOp(tmuxctl.OpHas, errors.New(tmuxMarker))
			d.fixture.tmux.FailOp(tmuxctl.OpList, errors.New(tmuxMarker))
		},
	}

	for name, arrange := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			d := newDestroyer(t)
			live := d.live(t)
			arrange(d, live)

			w := d.post(t, live.ID, d.confirmed(t))

			// An unverified teardown is not a teardown, and it is the one outcome
			// with a code of its own for exactly that reason (FR-023).
			wantOutcome(t, w, wantUnverifiedOutcome)

			// The record is the only thing carrying an owner and two deadlines for a
			// session that may still be running, and adoption runs at startup: a
			// record dropped here is a live unsandboxed shell forgotten for good.
			if _, err := d.fixture.store.Get(live.ID, auth.CallerOperator); err != nil {
				t.Errorf("the record of a session that may still be running was dropped: %v", err)
			}

			// Prominent means findable: the trail's own name for this operation, a
			// refusal, the daemon's id for the session, and the one reason that says
			// a live unsandboxed shell may exist.
			rec := d.only(t)
			if got, want := rec["action"], string(audit.ActionDashboardDestroy); got != want {
				t.Errorf("action = %v; want %v", got, want)
			}
			if got, want := rec["decision"], string(audit.Deny); got != want {
				t.Errorf("decision = %v; want %v", got, want)
			}
			if got, want := rec["reason"], errDestroyOrphaned.Error(); got != want {
				t.Errorf("reason = %v; want %v", got, want)
			}
			if got := rec["session_id"]; got != live.ID {
				t.Errorf("session_id = %v; want %q", got, live.ID)
			}

			outward := d.sink.String() + w.Body.String()
			if strings.Contains(outward, tmuxMarker) {
				t.Errorf("the host's own account of the failure travelled outward: %q", outward)
			}
		})
	}

	// AR-004 stated as a test rather than as a comment: a form asking to be
	// believed anyway changes nothing. **Must fail when** a force path is added.
	forced := newDestroyer(t)
	live := forced.live(t)
	forced.fixture.tmux.SurviveKill(live.TmuxName())

	form := forced.confirmed(t)
	for _, field := range []string{"force", "skip_verification", "verify"} {
		form.Set(field, confirmYes)
	}

	w := forced.post(t, live.ID, form)
	if got, want := w.Header().Get(headerLocation), "/?outcome="+string(wantUnverifiedOutcome); got != want {
		t.Fatalf("a destroy carrying a force field answered %d to %q; want %q — there is no force path (AR-004)",
			w.Code, got, want)
	}
	if _, err := forced.fixture.store.Get(live.ID, auth.CallerOperator); err != nil {
		t.Errorf("a destroy carrying a force field dropped the record of a session that may still be running: %v", err)
	}
}

// TestDestroyCrossOwnerUniform is FR-017 and SC-009 at the route that acts: an
// identifier no session ever had, one another operator owns, and one whose
// session is already gone are one answer, byte for byte.
//
// **Must fail when** the action distinguishes them — which is what the
// whole-header comparison and the Content-Length claim are for. TestNotFoundUniform
// makes the same claim about a fixture handler; this one makes it about the
// registered route, which is the only place a real branch could grow.
//
// The record is asserted in the opposite direction: an operator reads which of
// the three it was, and a caller cannot.
func TestDestroyCrossOwnerUniform(t *testing.T) {
	t.Parallel()

	// Not on the allowlist and not this operator's owner: a second operator whose
	// sessions this viewer must not be able to detect the existence of.
	const stranger auth.CallerID = "a-second-operator"

	cases := []struct {
		name   string
		id     func(t *testing.T, d *destroyer) string
		reason error
	}{
		{
			name:   "an identifier no session on this host ever had",
			id:     func(*testing.T, *destroyer) string { return strings.Repeat("c", session.IDLen) },
			reason: session.ErrSessionNotFound,
		},
		{
			name: "a session another operator owns",
			id: func(t *testing.T, d *destroyer) string {
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
			id: func(t *testing.T, d *destroyer) string {
				t.Helper()
				gone, _ := d.fixture.plant(t, session.Session{
					Name: "already gone", WorkDir: d.fixture.repo, State: session.StateDead,
				})
				return gone.ID
			},
			reason: session.ErrSessionDead,
		},
	}

	var (
		firstName    string
		firstBody    string
		firstHeaders http.Header
	)

	for _, c := range cases {
		d := newDestroyer(t)
		id := c.id(t, d)

		w := d.post(t, id, d.confirmed(t))

		if w.Code != wantNotFoundStatus {
			t.Errorf("%s: status = %d (%s); want %d", c.name, w.Code, w.Body.String(), wantNotFoundStatus)
		}
		if got := w.Body.String(); got != wantNotFoundBody {
			t.Errorf("%s: body\n%s\nwant\n%s", c.name, got, wantNotFoundBody)
		}
		if got, want := w.Header().Get(headerContentLength), strconv.Itoa(len(wantNotFoundBody)); got != want {
			t.Errorf("%s: %s = %q; want %q", c.name, headerContentLength, got, want)
		}
		if got := d.kills(); got != 0 {
			t.Errorf("%s: the host was asked to kill %d times; want 0 — a session that is not this operator's is never touched", c.name, got)
		}

		rec := d.only(t)
		if got, want := rec["reason"], c.reason.Error(); got != want {
			t.Errorf("%s: reason = %v; want %v — the record is the only place the cause is named", c.name, got, want)
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

	served := newDestroyer(t)
	mine := served.live(t)
	if w := served.post(t, mine.ID, served.confirmed(t)); w.Header().Get(headerLocation) != "/?outcome="+string(wantDestroyedOutcome) {
		t.Fatalf("a destroy of this operator's own session answered %d to %q; want the destroyed outcome — "+
			"every refusal above is satisfied by a route that refuses everyone",
			w.Code, w.Header().Get(headerLocation))
	}
}

// TestADestroyIdentifierOffTheAlphabetIsNoRoute is contracts/actions.md's `{id}`
// rule: 32 lowercase hex characters, and anything else takes the existing
// unknown-route path rather than this route's own not-found.
//
// **Must fail when** the shape check is dropped — every case below then answers
// with the action's uniform not-found fragment instead of the door's not-found
// page.
//
// It discloses nothing that the address of every rendered card does not already.
// An identifier off that alphabet cannot name a session on this host, so this is
// a shape check rather than an existence oracle; the identifiers that *could*
// name one all go through the uniform answer asserted above.
func TestADestroyIdentifierOffTheAlphabetIsNoRoute(t *testing.T) {
	t.Parallel()

	cases := map[string]string{
		"upper-case hex":                   strings.Repeat("C", session.IDLen),
		"one character short":              strings.Repeat("c", session.IDLen-1),
		"one character long":               strings.Repeat("c", session.IDLen+1),
		"off the alphabet":                 strings.Repeat("z", session.IDLen),
		"a name rather than an identifier": "refactor-auth",
	}

	for name, id := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			d := newDestroyer(t)
			live := d.live(t)

			w := d.post(t, id, d.confirmed(t))

			if w.Code != http.StatusNotFound {
				t.Fatalf("status = %d (%s); want %d", w.Code, w.Body.String(), http.StatusNotFound)
			}
			if got := w.Body.String(); !strings.Contains(got, notFoundBody) {
				t.Errorf("body\n%s\ndoes not carry the door's own not-found page (%q)", got, notFoundBody)
			}
			if got := w.Body.String(); got == string(bodyActionNotFound) {
				t.Errorf("an identifier that is not a route was answered with the action's not-found:\n%s", got)
			}

			if recorded, running := d.standing(t, live); !recorded || !running {
				t.Errorf("a request at no route left the record %v and the window %v; want both untouched",
					recorded, running)
			}
			if got, want := d.only(t)["reason"], errScopeNoRoute.Error(); got != want {
				t.Errorf("reason = %v; want %v", got, want)
			}
		})
	}
}

// --- US1 acceptance (T008) --------------------------------------------------
//
// Everything above proves one of the destroy route's own answers. What is left
// is the story's two claims that are about the route table and the gate rather
// than about the handler behind them: that this path answers nothing but a POST,
// and that each half of the cross-site defence refuses on its own.

// headerAllow is the header a 405 carries, and the one no route on this door may
// ever write: it names the methods a path really serves, which is the route table
// the uniform unknown-route answer exists not to hand out.
const headerAllow = "Allow"

// TestADestroyIsNoRouteOnAnyOtherMethod is contracts/actions.md's method rule
// with FR-033 behind it: a GET on the destroy path is an unknown route, answered
// exactly as any other unknown route is — never a 405, and never with an Allow
// header naming the method that would have worked.
//
// **Must fail when** a method-not-allowed path is added, and there are two ways
// to add one. ServeMux answers 405 itself, with an Allow header naming the route
// table, whenever a pattern matches the path but not the method and nothing else
// matches — so deleting handleUnrouted's `/` catch-all turns every row below into
// a 405. A hand-written 405 branch moves the same two things. Either way the
// status assertion and the Allow assertion go red together.
//
// The two responses are compared whole rather than each being asserted a 404,
// because "answered exactly as any other unknown route is" is the claim. A 404
// that mentioned the method would satisfy a status assertion and still be the
// enumeration this door's uniform answers close: it would tell a caller that
// *something* is served at that address, which is what a route table is made of.
//
// Each request carries everything a destroy that would have worked carries — a
// verified assertion, a same-origin initiator, a valid page token, the confirming
// step — so the only thing left that can refuse it is the method. A row refused
// for a missing token would be proving nothing about the route table.
func TestADestroyIsNoRouteOnAnyOtherMethod(t *testing.T) {
	t.Parallel()

	for _, method := range []string{
		http.MethodGet, http.MethodHead, http.MethodPut,
		http.MethodPatch, http.MethodDelete, http.MethodOptions,
	} {
		t.Run(strings.ToLower(method), func(t *testing.T) {
			t.Parallel()

			d := newDestroyer(t)
			live := d.live(t)

			w := d.send(t, method, "/dashboard/sessions/"+live.ID+"/destroy", secFetchSiteSameOrigin, d.confirmed(t))

			if w.Code == http.StatusMethodNotAllowed {
				t.Fatalf("%s on the destroy path was answered %d with %s: %q — which method a path serves is not a caller's to learn",
					method, w.Code, headerAllow, w.Header().Get(headerAllow))
			}
			if w.Code != http.StatusNotFound {
				t.Fatalf("%s on the destroy path was answered %d (%s); want %d — the unknown-route answer",
					method, w.Code, w.Body.String(), http.StatusNotFound)
			}
			if got := w.Header().Get(headerAllow); got != "" {
				t.Errorf("%s on the destroy path answered with %s: %q; want no such header", method, headerAllow, got)
			}
			if got := w.Body.String(); got == string(bodyActionNotFound) {
				t.Errorf("%s on the destroy path was answered with the action's own not-found:\n%s\nwant the door's "+
					"unknown-route page — the action's answer is a statement about a session, and this request reached no route",
					method, got)
			}

			// The same method at a path nothing claims, on the same daemon and the
			// same identity: what an unknown route really answers here, rather than
			// this test's idea of it.
			nowhere := d.send(t, method, "/dashboard/sessions/"+live.ID+"/nonesuch", secFetchSiteSameOrigin, d.confirmed(t))

			if w.Code != nowhere.Code {
				t.Errorf("%s on the destroy path answered %d; at a path nothing claims it answered %d — the two are distinguishable",
					method, w.Code, nowhere.Code)
			}
			if got, want := w.Body.String(), nowhere.Body.String(); got != want {
				t.Errorf("%s on the destroy path answered\n%s\nat a path nothing claims it answered\n%s\nthe two are distinguishable",
					method, got, want)
			}
			if !maps.EqualFunc(w.Header(), nowhere.Header(), slices.Equal) {
				t.Errorf("%s on the destroy path answered with headers %v; at a path nothing claims %v — the two are distinguishable",
					method, w.Header(), nowhere.Header())
			}

			// Nothing was torn down, which is the half a status code cannot see: a
			// route that acted and then answered 404 satisfies every comparison above.
			if recorded, running := d.standing(t, live); !recorded || !running {
				t.Errorf("%s on the destroy path left the record %v and the window %v; want both untouched",
					method, recorded, running)
			}
			if got := d.kills(); got != 0 {
				t.Errorf("%s on the destroy path asked the host to kill %d times; want 0", method, got)
			}

			// One record each, in the trail's existing vocabulary for a request that
			// reached no route (data-model.md): an operator grepping for route.unknown
			// finds a mistyped method among the mistyped paths, and nobody has to know
			// this milestone happened in order to read it.
			got := d.records(t)
			if len(got) != 2 {
				t.Fatalf("two requests emitted %d audit records (%v); FR-041 requires exactly one each", len(got), got)
			}
			for i, rec := range got {
				if want := string(audit.ActionUnknownRoute); rec["action"] != want {
					t.Errorf("record %d: action = %v; want %v — a %s that matched no route is not a destroy",
						i, rec["action"], want, method)
				}
				if want := string(audit.Deny); rec["decision"] != want {
					t.Errorf("record %d: decision = %v; want %v", i, rec["decision"], want)
				}
				if want := errScopeNoRoute.Error(); rec["reason"] != want {
					t.Errorf("record %d: reason = %v; want %v", i, rec["reason"], want)
				}
			}
		})
	}
}

// defenceRow is one arrangement of the gate's two halves against the registered
// destroy route: what the browser said about the initiator, what the form
// carried, and which half must therefore be the one that refused.
type defenceRow struct {
	name string

	// disabled names the half this row satisfies so that it cannot be the reason
	// the request was refused. It is the whole point of the row and appears in
	// every failure message, because a reader looking at a red line here needs to
	// know which half was meant to be doing the work alone.
	disabled string

	site  string
	token func(t *testing.T, d *destroyer) string

	// refusedBy is the sentinel the remaining half leaves on the record. The
	// response cannot carry it (FR-004), so this is the only place the claim
	// "*this* half refused" can be made at all.
	refusedBy error
}

// TestEitherHalfOfTheDefenceRefusesAlone is FR-002c and SC-001a: neither check
// authorises a state change on its own, and neither is load-bearing only in the
// other's company.
//
// Every row disables exactly one half by **satisfying** it, which is the only
// disabling AR-005 permits and the only one this suite may leave behind: a build
// tag or a flag that turned a check off would be a way to turn it off in the
// shipping build, which is the defect this milestone was written to prevent. A
// half that has nothing left to object to cannot be the reason a row was refused,
// so the row carrying a valid token from a foreign initiator is the same-origin
// half working alone, and the same-origin row carrying no token is the page-token
// half working alone.
//
// **Must fail when** either half is load-bearing only in combination. A gate
// spelled `if crossSite && tokenBad` admits both single-fault rows; a gate that
// dropped one check entirely admits that check's rows. Either way the status, the
// store and the host all move together, because the handler behind the gate is
// the one that ends an unsandboxed shell.
//
// The recorded reason is asserted per row, and that is what makes this a proof
// about *which* half refused rather than that something did. All six causes
// answer a caller identically (FR-004), so the trail is the only place the two
// halves are distinguishable — and a test reading the status alone would pass
// just as happily on a gate where one check refused every row.
func TestEitherHalfOfTheDefenceRefusesAlone(t *testing.T) {
	t.Parallel()

	valid := func(t *testing.T, d *destroyer) string {
		t.Helper()
		return mustMint(t, d.pageKey, testOperatorEmail, testTime)
	}
	none := func(*testing.T, *destroyer) string { return absent }

	rows := []defenceRow{
		{
			// The page token alone. The initiator is the one value crossSiteAction
			// admits, so that check has nothing to say about this request.
			name:      "the form carried no page token",
			disabled:  "the same-origin half",
			site:      secFetchSiteSameOrigin,
			token:     none,
			refusedBy: errPageTokenMissing,
		},
		{
			// The page token alone again, and the sharper half of it: a token that is
			// real, unexpired, and minted by this very daemon — for somebody else.
			// FR-007 at the route rather than at the function, which is where a
			// handler that verified the token against a value out of the request
			// would show up.
			name:     "the form carried another identity's token",
			disabled: "the same-origin half",
			site:     secFetchSiteSameOrigin,
			token: func(t *testing.T, d *destroyer) string {
				t.Helper()
				return mustMint(t, d.pageKey, testStrangerEmail, testTime)
			},
			refusedBy: errPageTokenMismatch,
		},
		{
			// The same-origin half alone, and the milestone's central case: the token
			// is exactly the one this operator's own page renders, and the browser
			// says the request was initiated somewhere else (US1 scenario 3).
			name:      "the browser said the request came from another site",
			disabled:  "the page-token half",
			site:      "cross-site",
			token:     valid,
			refusedBy: errActionCrossSite,
		},
		{
			// Same-site is not same-origin. A sibling hostname under the same
			// registrable domain is a different origin and a different page, and the
			// contract admits exactly one spelling.
			name:      "the request came from a sibling site",
			disabled:  "the page-token half",
			site:      "same-site",
			token:     valid,
			refusedBy: errActionCrossSite,
		},
		{
			// A URL opened by no page at all — the case an Origin comparison cannot
			// cleanly see, and the reason research R1 reused Sec-Fetch-Site.
			name:      "no page initiated the request",
			disabled:  "the page-token half",
			site:      "none",
			token:     valid,
			refusedBy: errActionCrossSite,
		},
		{
			// Absent is not evidence of same-origin initiation. crossSite admits it
			// on the pane stream on purpose; crossSiteAction does not, because a
			// check anything can opt out of by omitting a header is optional.
			name:      "the browser said nothing about where the request came from",
			disabled:  "the page-token half",
			site:      absent,
			token:     valid,
			refusedBy: errActionCrossSite,
		},
		{
			// Neither half satisfied, which is what a hostile page really sends: no
			// token it could not compute, and an initiator it cannot lie about. The
			// same-origin check is named on the record because it runs first and the
			// order is fixed — the token is never examined.
			name:      "a hostile page sent a bare request with the ambient cookie",
			disabled:  "neither half",
			site:      "cross-site",
			token:     none,
			refusedBy: errActionCrossSite,
		},
	}

	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()

			d := newDestroyer(t)
			live := d.live(t)

			form := d.confirmed(t)
			if tok := row.token(t, d); tok == absent {
				form.Del(fieldPageToken)
			} else {
				form.Set(fieldPageToken, tok)
			}

			w := d.send(t, http.MethodPost, "/dashboard/sessions/"+live.ID+"/destroy", row.site, form)

			if w.Code != wantActionStatus {
				t.Fatalf("with %s disabled, status = %d (%s); want %d — the other half authorised a state change on its own",
					row.disabled, w.Code, w.Body.String(), wantActionStatus)
			}
			if got := w.Body.String(); got != wantActionBody {
				t.Errorf("with %s disabled, body\n%s\nwant the gate's uniform refusal\n%s", row.disabled, got, wantActionBody)
			}

			// The session is the claim. A refusal that had already torn it down would
			// be a refusal of nothing that matters (FR-003), and it is the only
			// failure a status code cannot see.
			if recorded, running := d.standing(t, live); !recorded || !running {
				t.Errorf("with %s disabled, the refused destroy left the record %v and the window %v; want both untouched",
					row.disabled, recorded, running)
			}
			if got := d.kills(); got != 0 {
				t.Errorf("with %s disabled, the refused destroy asked the host to kill %d times; want 0",
					row.disabled, got)
			}

			rec := d.only(t)
			if got, want := rec["action"], string(audit.ActionDashboardReject); got != want {
				t.Errorf("with %s disabled, action = %v; want %v", row.disabled, got, want)
			}
			if got, want := rec["decision"], string(audit.Deny); got != want {
				t.Errorf("with %s disabled, decision = %v; want %v", row.disabled, got, want)
			}
			if got, want := rec["reason"], row.refusedBy.Error(); got != want {
				t.Errorf("with %s disabled, reason = %v; want %v — the remaining half is not the one that refused",
					row.disabled, got, want)
			}
		})
	}

	// The non-vacuity, and it has to destroy something rather than merely answer
	// the destroyed outcome: every row above is satisfied by a route that refuses
	// everyone, and a gate that refuses everyone is not a defence with two halves —
	// it is a broken dashboard that would ship looking secure.
	admitted := newDestroyer(t)
	live := admitted.live(t)

	w := admitted.post(t, live.ID, admitted.confirmed(t))
	wantOutcome(t, w, wantDestroyedOutcome)
	if recorded, running := admitted.standing(t, live); recorded || running {
		t.Errorf("a destroy satisfying both halves left the record %v and the window %v; want both gone", recorded, running)
	}
	if got, want := admitted.only(t)["action"], string(audit.ActionDashboardDestroy); got != want {
		t.Errorf("action = %v; want %v — an admitted action is recorded as the action, not as a rejection", got, want)
	}
}

// --- US2: create from the browser (T009) ------------------------------------
//
// The other direction of the same authority. A destroy ends an unsandboxed shell;
// this route starts one, on a door whose credential is an ambient cookie — which
// is why it is registered through the same gate, and why the claims below are
// about what reached the host as much as about what the caller was told.

// The create's own answers, which are outcome codes since T014. Spelled here
// rather than read from the constants outcome.go writes, for the reason every
// other literal in this file is: a test asserting against the variable proves
// only that the code agrees with itself.
const (
	wantCreatedOutcome          = outcome("created")
	wantCreateBadNameOutcome    = outcome("bad-name")
	wantCreateBadWorkDirOutcome = outcome("bad-work-dir")
	wantCreateLimitedOutcome    = outcome("limited")
	wantCreateFailedOutcome     = outcome("create-failed")

	// The switch's own refusal, and it is the toggle's code rather than one of
	// this route's: the fact is the same either way — a browser named a state
	// this daemon does not offer — and the sentence behind it says nothing about
	// which route asked. Spelled out here for the reason the four above are.
	wantCreateBadModeOutcome = outcome("bad-mode")
)

// The switch as a form carries it (contracts/remote-control-toggle.md). Written
// out rather than read from fieldRemoteControl and remoteControlOn, because what
// a browser posts is the contract's strings and a test that asked the code what
// it reads could not notice the two parting company — the arrangement createPath
// has for the route.
const (
	remoteControlField  = "remote_control"
	remoteControlTicked = "on"
)

// createPath is the route from contracts/actions.md's table, written out rather
// than read from patternDashboardCreate: what a browser posts to is the
// contract's string, and a test that asked the code where to post could not
// notice the two parting company.
const createPath = "/dashboard/sessions"

// creator is the registered create route with everything behind it readable: the
// store, the fake host, and the trail.
//
// It is destroyer's shape for the other route rather than a method on it. The two
// stories arrange different things — one plants a session, the other must find
// that none was planted — and a fixture serving both would be one whose helpers
// are half unused at every call site.
type creator struct {
	*testServer
	keys *keyServer
}

func newCreator(t *testing.T) *creator {
	t.Helper()

	keys := newKeyServer(t)
	return &creator{testServer: newAuditedServerWith(t, keys.validator(t)), keys: keys}
}

// asked is the form a rendered create control submits: the render's page token,
// and the two fields contracts/actions.md fixes.
func (c *creator) asked(t *testing.T, name, workDir string) url.Values {
	t.Helper()

	form := url.Values{}
	form.Set(fieldPageToken, mustMint(t, c.pageKey, testOperatorEmail, testTime))
	form.Set(fieldName, name)
	form.Set(fieldWorkDir, workDir)
	return form
}

// wellFormed is the create every case below varies from — a name on the permitted
// alphabet and a real directory under the fixture's approved root.
func (c *creator) wellFormed(t *testing.T) url.Values {
	t.Helper()
	return c.asked(t, "refactor-auth", c.fixture.repo)
}

// post submits one form at the create route, as the browser this daemon rendered
// the page for.
func (c *creator) post(t *testing.T, form url.Values) *httptest.ResponseRecorder {
	t.Helper()
	return c.send(t, http.MethodPost, createPath, secFetchSiteSameOrigin, form)
}

// send is post with the method, the path and the browser's own account of where
// the request came from all chosen by the caller — destroyer.send's arrangement,
// and for its reason: a varied case must differ from the ordinary one in exactly
// the field it means to vary.
func (c *creator) send(t *testing.T, method, path, site string, form url.Values) *httptest.ResponseRecorder {
	t.Helper()

	r := httptest.NewRequest(method, path, strings.NewReader(form.Encode()))
	r.Header.Set(headerContentType, contentTypeForm)
	r.Header.Set(headerAccessAssertion, c.keys.mint(t, c.keys.claims()))
	if site != absent {
		r.Header.Set(headerSecFetchSite, site)
	}

	w := httptest.NewRecorder()
	c.ServeHTTP(w, r)
	return w
}

// page opens the fleet as the same operator, which is where a session appears
// once the create that made it has been answered.
func (c *creator) page(t *testing.T) *httptest.ResponseRecorder {
	t.Helper()

	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.Header.Set(headerAccessAssertion, c.keys.mint(t, c.keys.claims()))

	w := httptest.NewRecorder()
	c.ServeHTTP(w, r)
	return w
}

// owned is every record this operator holds, read from the store rather than from
// a page: a claim that nothing was created is about the daemon's own state and not
// about what it chose to render.
func (c *creator) owned() []session.Session { return c.fixture.store.List(auth.CallerOperator) }

// started counts the sessions the host was asked to start.
//
// It is asserted alongside the store everywhere below because the two answer
// different halves of one question. A record with no window is a create that never
// reached the host; a window with no record is a live unsandboxed shell nothing can
// see, which is the failure Principle VI is about — and a refusal that started
// something before refusing looks, in the store alone, exactly like one that
// started nothing.
func (c *creator) started() int {
	n := 0
	for _, call := range c.fixture.tmux.Calls() {
		if call.Op == tmuxctl.OpNew {
			n++
		}
	}
	return n
}

// TestBrowserCreateStartsTheSessionAndAnswersWithItsCard is US2's first scenario
// end to end: the operator's own create, validated by milestone 1's rules, started
// on the host, and answered with the card the fleet will draw for it.
//
// **Must fail when** the route stops calling Manager.Create — the store and the
// host both go quiet — or when the fleet the operator is returned to does not
// draw the session that was just started, which is what makes a created session
// look on arrival exactly as it will look on every later page load.
//
// The card is asserted on the fleet rather than in the answer since T014: the
// route redirects, and the grid is where the new card is. That is the same claim
// the fragment used to make, moved to the page that owns the grid — and it is a
// stronger one, because the card is now drawn beside its siblings by the handler
// that draws all of them rather than by a second render here.
//
// The card is asserted to carry a page token as well. A card whose form had none
// would render no action row at all (session-card.html), so the session this route
// just started would be the one card on the fleet the operator cannot act on.
func TestBrowserCreateStartsTheSessionAndAnswersWithItsCard(t *testing.T) {
	t.Parallel()

	c := newCreator(t)

	w := c.post(t, c.wellFormed(t))

	wantOutcome(t, w, wantCreatedOutcome)

	owned := c.owned()
	if len(owned) != 1 {
		t.Fatalf("the store holds %d records after one create; want exactly 1", len(owned))
	}
	live := owned[0]

	if live.Owner != auth.CallerOperator {
		t.Errorf("the created session is owned by %q; want %q — the owner is the verified identity, never a field",
			live.Owner, auth.CallerOperator)
	}
	if want := "refactor-auth"; live.Name != want {
		t.Errorf("name = %q; want %q", live.Name, want)
	}
	if live.WorkDir != c.fixture.repo {
		t.Errorf("work_dir = %q; want the resolved %q", live.WorkDir, c.fixture.repo)
	}

	// The host, which is the half the store cannot see: a record for a session
	// nobody started is a card describing nothing.
	running, err := c.fixture.tmux.Has(context.Background(), live.TmuxName())
	if err != nil {
		t.Fatalf("ask the fake host about %s: %v", live.TmuxName(), err)
	}
	if !running {
		t.Errorf("the host is not running %s; a created session with no window is a card describing nothing",
			live.TmuxName())
	}
	if dir, ok := c.fixture.tmux.WorkDir(live.TmuxName()); !ok || dir != c.fixture.repo {
		t.Errorf("the host started %s in %q (found: %t); want the resolved %q",
			live.TmuxName(), dir, ok, c.fixture.repo)
	}

	// The trail, read before the fleet is opened: that page is a request of its
	// own and leaves a dashboard.view record, so the create's one record has to be
	// counted while it is still the only one (FR-041).
	rec := c.only(t)
	if got, want := rec["action"], string(audit.ActionDashboardCreate); got != want {
		t.Errorf("action = %v; want %v — a browser create is not the API's session.create", got, want)
	}
	if got, want := rec["decision"], string(audit.Allow); got != want {
		t.Errorf("decision = %v; want %v", got, want)
	}
	if got := rec["session_id"]; got != live.ID {
		t.Errorf("session_id = %v; want %q", got, live.ID)
	}

	// The fleet the redirect sends the operator back to, which is where the new
	// card is.
	landed := c.page(t)
	if landed.Code != http.StatusOK {
		t.Fatalf("the fleet the create redirected to = %d (%s); want %d", landed.Code, landed.Body.String(), http.StatusOK)
	}
	body := landed.Body.String()
	if !strings.Contains(body, `<article class="card"`) {
		t.Errorf("the fleet draws no canonical card for the session that was just started:\n%s", body)
	}
	for what, want := range map[string]string{
		"the identifier":        live.ID,
		"the name":              live.Name,
		"the working directory": live.WorkDir,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the card does not carry %s (%q):\n%s", what, want, body)
		}
	}
	if !strings.Contains(body, fieldPageToken) {
		t.Errorf("the card carries no %s, so the session it announces offers no control the gate would admit:\n%s",
			fieldPageToken, body)
	}
}

// servedTheToken reports whether any run of TokenLen characters in what was served
// is the bearer token behind hash.
//
// A sliding window rather than a search for a known string, because there is no
// known string: the plaintext exists for the length of one assignment in
// createFromBrowser and is never named, so a test cannot be handed it. What the
// daemon keeps is the hash, and hashing every candidate run against it asks the
// only question there is to ask — is the credential for this record anywhere in
// these bytes — without caring what markup surrounds it.
func servedTheToken(served string, hash [sha256.Size]byte) bool {
	for i := 0; i+session.TokenLen <= len(served); i++ {
		if sha256.Sum256([]byte(served[i:i+session.TokenLen])) == hash {
			return true
		}
	}
	return false
}

// TestBrowserCreateNeverServesToken is FR-013 at the browser door: the bearer
// token a create mints reaches no response, no template, and no record.
//
// **Must fail when** the token is kept where a render can reach it. Storing it on
// the record in place of the hash puts it in front of cardOf, which renders it
// into the fleet's hidden field, and the sweep finds it there — which is the whole
// point of sweeping the bytes rather than asserting that some particular field is
// absent: a token disclosed through a field nobody thought to check is disclosed
// all the same.
//
// The route's own answer is swept as well as the fleet, because the two are
// different claims. The first is that this route did not write the token out —
// which since T014 is a 303 that must stay a 303 with nothing in it, including a
// Location a credential could ride in. The second is that nothing kept it where a
// later render could: Store.List hands every field of the record to the page
// handler, and the difference between a page that never shows the credential and
// one that happens not to today is the difference between a record holding a hash
// and a record holding the token.
//
// The trail is swept for the reason a leak test exists at all: a session token in
// journald is the key to an unsandboxed shell, and it would sit there long after
// the response that could have carried it was gone.
func TestBrowserCreateNeverServesToken(t *testing.T) {
	t.Parallel()

	// The sweep, proved able to find a token before it is used to claim there is
	// none. Without this every assertion below is satisfied by a search that
	// matches nothing whatever it is handed.
	planted, hash, err := session.NewToken()
	if err != nil {
		t.Fatalf("session.NewToken = _, _, %v; want a credential", err)
	}
	if !servedTheToken(`<p class="outcome">`+planted+`</p>`, hash) {
		t.Fatal("the sweep cannot find a token it was handed, so finding none below would prove nothing")
	}

	c := newCreator(t)

	w := c.post(t, c.wellFormed(t))
	wantOutcome(t, w, wantCreatedOutcome)

	owned := c.owned()
	if len(owned) != 1 {
		t.Fatalf("the store holds %d records after one create; want exactly 1", len(owned))
	}
	live := owned[0]

	if servedTheToken(w.Body.String(), live.TokenHash) {
		t.Errorf("the create's own answer carries the session's bearer token:\n%s", w.Body.String())
	}

	page := c.page(t)
	if page.Code != http.StatusOK {
		t.Fatalf("GET / after the create = %d (%s); want %d", page.Code, page.Body.String(), http.StatusOK)
	}
	if servedTheToken(page.Body.String(), live.TokenHash) {
		t.Errorf("the fleet page rendered after the create carries the session's bearer token:\n%s", page.Body.String())
	}

	if servedTheToken(c.sink.String(), live.TokenHash) {
		t.Errorf("the audit trail carries the session's bearer token:\n%s", c.sink.String())
	}
}

// workDirRefusal is one way a working directory can be refused, and the reason it
// must leave on the record.
//
// The reasons differ from each other on purpose, and the bodies must not. FR-012
// wants an operator to be able to read which rule refused; the filesystem-oracle
// argument wants a caller to be unable to.
type workDirRefusal struct {
	name   string
	dir    func(t *testing.T, c *creator) string
	reason error
}

func workDirRefusals() []workDirRefusal {
	return []workDirRefusal{
		{
			name:   "a traversal out of the approved root",
			dir:    func(_ *testing.T, c *creator) string { return c.fixture.repo + "/../.." },
			reason: session.ErrWorkDirOutsideRoots,
		},
		{
			name:   "an absolute path outside the approved roots",
			dir:    func(_ *testing.T, c *creator) string { return filepath.Dir(c.fixture.root) },
			reason: session.ErrWorkDirOutsideRoots,
		},
		{
			// Inside an approved root by spelling and outside it by resolution,
			// which is the whole reason ResolveWorkDir runs EvalSymlinks.
			name: "a symlink inside a root pointing out of it",
			dir: func(t *testing.T, c *creator) string {
				t.Helper()

				outside, err := filepath.EvalSymlinks(t.TempDir())
				if err != nil {
					t.Fatalf("resolve a directory outside the approved root: %v", err)
				}
				link := filepath.Join(c.fixture.root, "escape")
				if err := os.Symlink(outside, link); err != nil {
					t.Fatalf("link %s out of the approved root: %v", link, err)
				}
				return link
			},
			reason: session.ErrWorkDirOutsideRoots,
		},
		{
			name: "a regular file rather than a directory",
			dir: func(t *testing.T, c *creator) string {
				t.Helper()

				file := filepath.Join(c.fixture.root, "notes.txt")
				if err := os.WriteFile(file, []byte("not a directory"), 0o600); err != nil {
					t.Fatalf("write %s: %v", file, err)
				}
				return file
			},
			reason: session.ErrWorkDirNotDirectory,
		},
		{
			// The row the oracle argument is really about. A directory that is
			// simply not there must be refused in the same words as one that is
			// there and not permitted, or a caller holding a form can map this host
			// by reading the difference.
			name:   "a directory that is not there at all",
			dir:    func(_ *testing.T, c *creator) string { return filepath.Join(c.fixture.root, "nope") },
			reason: session.ErrWorkDirUnresolvable,
		},
	}
}

// TestWorkDirRefusalsAreOneMessage is FR-012: every working-directory refusal
// answers with the identical bytes, and the rule that refused is kept server-side.
//
// **Must fail when** the messages diverge. Each row is compared against the first
// row's whole response — status, body and Location — rather than each being
// asserted a 303, because "one message" is the claim and a second answer that
// happened to also be a 303 would satisfy anything weaker. Since T014 the message
// travels as an outcome code, so the Location *is* the message: a route that told
// two causes apart would do it by redirecting to two codes.
//
// The rows are driven in one loop rather than as parallel subtests for that
// comparison's sake: the claim is *between* rows, so the responses have to exist
// at the same time to be compared at all.
//
// Nothing is created on any of them, asserted in the store and on the host both. A
// refusal that started a session before refusing would be a filesystem oracle with
// an unsandboxed shell attached.
func TestWorkDirRefusalsAreOneMessage(t *testing.T) {
	t.Parallel()

	var (
		firstName string
		first     *httptest.ResponseRecorder
	)

	for _, row := range workDirRefusals() {
		c := newCreator(t)
		w := c.post(t, c.asked(t, "refactor-auth", row.dir(t, c)))

		wantOutcome(t, w, wantCreateBadWorkDirOutcome)

		if first == nil {
			firstName, first = row.name, w
		} else {
			if w.Code != first.Code {
				t.Errorf("%s answered %d; %s answered %d — the two are distinguishable",
					row.name, w.Code, firstName, first.Code)
			}
			if got, want := w.Body.String(), first.Body.String(); got != want {
				t.Errorf("%s answered\n%s\n%s answered\n%s\nthe two are distinguishable", row.name, got, firstName, want)
			}
			if got, want := w.Header().Get(headerLocation), first.Header().Get(headerLocation); got != want {
				t.Errorf("%s answered %s: %q; %s answered %q — the two are distinguishable",
					row.name, headerLocation, got, firstName, want)
			}
		}

		if owned := c.owned(); len(owned) != 0 {
			t.Errorf("%s: the store holds %d records; want none — a refused create creates nothing", row.name, len(owned))
		}
		if got := c.started(); got != 0 {
			t.Errorf("%s: the host was asked to start %d sessions; want 0", row.name, got)
		}

		// The half a caller may not have, kept where the operator can read it.
		rec := c.only(t)
		if got, want := rec["action"], string(audit.ActionDashboardCreate); got != want {
			t.Errorf("%s: action = %v; want %v — a refused create is still a create", row.name, got, want)
		}
		if got, want := rec["decision"], string(audit.Deny); got != want {
			t.Errorf("%s: decision = %v; want %v", row.name, got, want)
		}
		if got, want := rec["reason"], row.reason.Error(); got != want {
			t.Errorf("%s: reason = %v; want %v — the rule that refused is the one thing kept server-side",
				row.name, got, want)
		}
	}
}

// suggestedWorkDir is one arrangement in which the create form offers a path the
// allowlist behind the create route will not accept, and what makes it so.
//
// Both rows are the same sentence — a path present in the datalist and outside
// the allowlist — reached the two ways this daemon can reach it: the page and the
// check reading different configuration, and the host changing between the render
// and the submission. Only the first of them can still be offered at the moment
// the create is checked, which is why the test's mutation note names it.
type suggestedWorkDir struct {
	name string

	// offer arranges the suggestion and returns the path the rendered form must
	// carry. The test asserts the markup really holds it before submitting it, so
	// a row that quietly stopped being offered fails on the render rather than
	// going on to make a claim about a suggestion nobody was given.
	offer func(t *testing.T, c *creator) string

	// moved runs between the render and the submission, for the row whose host
	// changes underneath a page the operator is still looking at. nil for the row
	// that needs no change.
	moved func(t *testing.T, path string)
}

func suggestedWorkDirs() []suggestedWorkDir {
	return []suggestedWorkDir{
		{
			// The row that carries the claim. The page's suggestions and the
			// manager's allowlist are read from different places — the walk from
			// the Config the server holds, the check from the roots the manager
			// was built on — and pointing them at different directories is the
			// only way to observe a handler consulting the first. The divergence
			// is the instrument; that the handler reaches the same allowlist
			// whatever the page offered is the claim.
			//
			// It is also the shape the *explicit* suggestion source has.
			// contracts/directory-picker.md names two, and `workdir_suggestions`
			// is an operator's own list of paths — nothing constrains one of them
			// to sit under an approved root, so a page offering a directory this
			// daemon will refuse is that source working exactly as specified.
			name: "a suggestion source the allowlist does not share",
			offer: func(t *testing.T, c *creator) string {
				t.Helper()

				elsewhere, err := filepath.EvalSymlinks(t.TempDir())
				if err != nil {
					t.Fatalf("resolve a directory outside the approved root: %v", err)
				}
				offered := filepath.Join(elsewhere, "not-allowlisted")
				if err := os.Mkdir(offered, 0o750); err != nil {
					t.Fatalf("create %s: %v", offered, err)
				}
				c.cfg.Roots = []config.ApprovedRoot{{Path: elsewhere}}
				c.cfg.DiscoverRoots = true
				return offered
			},
		},
		{
			// The row that is a fact about time rather than about configuration:
			// a datalist is a snapshot of the host as it was at the render, and
			// an operator may submit it minutes later. The suggestion was inside
			// the root when the page was drawn and resolves out of it when the
			// create is checked, which is the same escape the typed symlink row
			// of workDirRefusals sends — and it must be refused in the same words,
			// because a page this daemon rendered is not evidence about a
			// filesystem it no longer describes.
			name: "a suggestion the host moved out from under the page",
			offer: func(t *testing.T, c *creator) string {
				t.Helper()

				c.cfg.Roots = []config.ApprovedRoot{{Path: c.fixture.root}}
				c.cfg.DiscoverRoots = true
				return c.fixture.repo
			},
			moved: func(t *testing.T, path string) {
				t.Helper()

				outside, err := filepath.EvalSymlinks(t.TempDir())
				if err != nil {
					t.Fatalf("resolve a directory outside the approved root: %v", err)
				}
				if err := os.Remove(path); err != nil {
					t.Fatalf("remove the offered directory %s: %v", path, err)
				}
				if err := os.Symlink(outside, path); err != nil {
					t.Fatalf("link %s out of the approved root: %v", path, err)
				}
			},
		},
	}
}

// TestChosenPathValidatedIdentically is FR-042, and it is the one real
// vulnerability the picker could introduce: a suggestion is never an
// authorisation.
//
// **Must fail when** the handler trusts a value because it was suggested — a
// membership test against the offered list, a flag on the form saying the path
// came from the datalist, anything that reaches a decision. The first row is what
// catches it: its suggestion is still on the list at the moment the create is
// checked, so a handler consulting the list accepts a directory outside the
// allowlist and starts an unsandboxed shell in it. The second row would survive
// that mutation, because by then the walk no longer offers the path either; what
// it pins is the other half — a page is a snapshot, and one rendered before the
// host changed grants nothing after it.
//
// The refusal is compared against a *typed* one byte for byte rather than merely
// asserted to be a refusal. "Refused as well" is a weaker claim than "refused
// identically": a route that told the two apart at all — a different outcome
// code, a different status, a record naming the suggestion — would be a route
// where being on the list is a fact the daemon acts on, and the difference is
// readable from outside. The record is compared too, because that is the half a
// caller cannot see and the operator can.
//
// Nothing is created on either row, asserted in the store and on the host both. A
// refusal that started a session before refusing would be this test passing over
// the exact failure it exists to prevent.
func TestChosenPathValidatedIdentically(t *testing.T) {
	t.Parallel()

	// The control, reached by typing a path the form never offered. It is built
	// first so the rows below have something to be identical *to*; asserting each
	// row is a 303 on its own would be satisfied by a second answer that merely
	// happened to also be one.
	typed := newCreator(t)
	control := typed.post(t, typed.asked(t, "refactor-auth", filepath.Dir(typed.fixture.root)))
	wantOutcome(t, control, wantCreateBadWorkDirOutcome)
	controlReason := typed.only(t)["reason"]
	if want := session.ErrWorkDirOutsideRoots.Error(); controlReason != want {
		t.Fatalf("the typed control was refused for %v; want %v — the rows below are compared against it", controlReason, want)
	}

	for _, row := range suggestedWorkDirs() {
		c := newCreator(t)
		offered := row.offer(t, c)

		// The render, which is what makes this a claim about a path the operator
		// was handed rather than about any path at all.
		page := c.page(t)
		if page.Code != http.StatusOK {
			t.Fatalf("%s: the fleet = %d (%s); want %d", row.name, page.Code, page.Body.String(), http.StatusOK)
		}
		create := sectionOf(t, page.Body.String(), "create")
		if suggestion := `<option value="` + offered + `">`; !strings.Contains(create, suggestion) {
			t.Fatalf("%s: the create form offers no %s, so submitting it proves nothing about a suggested path:\n%s",
				row.name, offered, create)
		}

		if row.moved != nil {
			row.moved(t, offered)
		}

		w := c.post(t, c.asked(t, "refactor-auth", offered))

		if w.Code != control.Code {
			t.Errorf("%s answered %d; the typed path answered %d — a suggested path is told apart from a typed one",
				row.name, w.Code, control.Code)
		}
		if got, want := w.Body.String(), control.Body.String(); got != want {
			t.Errorf("%s answered\n%s\nthe typed path answered\n%s\nthe two are distinguishable", row.name, got, want)
		}
		if got, want := w.Header().Get(headerLocation), control.Header().Get(headerLocation); got != want {
			t.Errorf("%s answered %s: %q; the typed path answered %q — being on the list changed the answer",
				row.name, headerLocation, got, want)
		}

		if owned := c.owned(); len(owned) != 0 {
			t.Errorf("%s: the store holds %d records; want none — a suggestion outside the allowlist creates nothing",
				row.name, len(owned))
		}
		if got := c.started(); got != 0 {
			t.Errorf("%s: the host was asked to start %d sessions; want 0 — a suggested path outside the roots is a shell that must not run",
				row.name, got)
		}

		// The trail, which is the half a caller does not get. Two records: the
		// render's own dashboard.view, then the refused create — one per request,
		// which is FR-041 asserted across both.
		recs := c.records(t)
		if len(recs) != 2 {
			t.Fatalf("%s: the render and the create emitted %d records (%v); want one each", row.name, len(recs), recs)
		}
		rec := recs[1]
		if got, want := rec["action"], string(audit.ActionDashboardCreate); got != want {
			t.Errorf("%s: action = %v; want %v — a refused create is still a create", row.name, got, want)
		}
		if got, want := rec["decision"], string(audit.Deny); got != want {
			t.Errorf("%s: decision = %v; want %v", row.name, got, want)
		}
		if got := rec["reason"]; got != controlReason {
			t.Errorf("%s: reason = %v; the typed path was recorded as %v — the record says which path the operator picked it from",
				row.name, got, controlReason)
		}
	}
}

// TestSuggestedPathOutsideRootsRefused is FR-009 on the daemon an operator can
// actually configure: a suggestion is never an authorisation. The list is
// presentation, allowed_roots is the control, and a path in the rendered
// <datalist> that is not under a root is refused exactly as a typed one is.
//
// TestChosenPathValidatedIdentically above makes the same claim and this is not a
// second copy of it. That test can only put an unacceptable path in the datalist
// by pointing the page's suggestions and the manager's allowlist at different
// directories — a divergence no deployed daemon has, since server.go builds both
// from one cfg.Roots — so what it pins is a handler's behaviour in an arrangement
// that has to be constructed. The explicit suggestion source makes the same
// arrangement ordinary: workdir_suggestions is a list the operator writes,
// nothing constrains an entry to sit under an approved root, and the loader
// deliberately does not check that it does, because whether a directory may hold
// a shell is the create route's question and answering it twice is how two
// answers begin. contracts/directory-suggestions.md's worked example is this
// daemon exactly, down to offering a path and refusing it on submit.
//
// **Must fail when** the handler trusts a value because the picker offered it —
// a membership test against SuggestedWorkDirs, a flag on the form saying the path
// came from the datalist, anything that reaches a decision. The suggestion here
// is a configured constant rather than a walk's finding, so it is still on the
// list at the instant the create is checked: such a handler accepts a directory
// outside the allowlist and starts an unsandboxed shell in it.
//
// The refusal is compared against a typed one byte for byte rather than merely
// asserted to be a refusal. "Refused as well" is the weaker claim — a route that
// told the two apart at all, by another outcome code, another status, or a record
// naming where the path came from, would be one where having been offered is a
// fact the daemon acts on, and the difference is readable from outside. The
// record is compared for the same reason it is there: it is the half a caller
// does not get and the operator does.
func TestSuggestedPathOutsideRootsRefused(t *testing.T) {
	t.Parallel()

	// The control, reached by typing a path this form never offered. Built first
	// so the suggested submission has something to be identical *to*: asserting on
	// its own that it is a 303 would be satisfied by a second answer that merely
	// happened to also be one.
	typed := newCreator(t)
	control := typed.post(t, typed.asked(t, "refactor-auth", filepath.Dir(typed.fixture.root)))
	wantOutcome(t, control, wantCreateBadWorkDirOutcome)
	controlReason := typed.only(t)["reason"]
	if want := session.ErrWorkDirOutsideRoots.Error(); controlReason != want {
		t.Fatalf("the typed control was refused for %v; want %v — the suggested path below is compared against it", controlReason, want)
	}

	c := newCreator(t)

	// One configuration, the way a real daemon is built: the page's suggestions
	// and the allowlist the create is checked against are the same roots. The
	// divergence the test above needs is deliberately not used here — the point is
	// that a coherent daemon offers this path.
	c.cfg.Roots = []config.ApprovedRoot{{Path: c.fixture.root}}

	// A real directory outside those roots, offered because the operator wrote it
	// in workdir_suggestions. Discovery stays off, so nothing here walks the host
	// and the offer comes from the explicit source alone.
	elsewhere, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("resolve a directory outside the approved root: %v", err)
	}
	offered := filepath.Join(elsewhere, "scratch")
	if err := os.Mkdir(offered, 0o750); err != nil {
		t.Fatalf("create %s: %v", offered, err)
	}
	c.cfg.WorkdirSuggestions = []string{offered}

	// The render, which is what makes this a claim about a path the operator was
	// handed rather than about any path at all. A row that quietly stopped being
	// offered fails here rather than going on to assert something about a
	// suggestion nobody was given.
	page := c.page(t)
	if page.Code != http.StatusOK {
		t.Fatalf("the fleet = %d (%s); want %d", page.Code, page.Body.String(), http.StatusOK)
	}
	create := sectionOf(t, page.Body.String(), "create")
	if suggestion := `<option value="` + offered + `">`; !strings.Contains(create, suggestion) {
		t.Fatalf("the create form offers no %s, so submitting it proves nothing about a suggested path:\n%s", offered, create)
	}

	w := c.post(t, c.asked(t, "refactor-auth", offered))

	if w.Code != control.Code {
		t.Errorf("the suggested path answered %d; the typed one answered %d — being on the list is told apart",
			w.Code, control.Code)
	}
	if got, want := w.Body.String(), control.Body.String(); got != want {
		t.Errorf("the suggested path answered\n%s\nthe typed one answered\n%s\nthe two are distinguishable", got, want)
	}
	if got, want := w.Header().Get(headerLocation), control.Header().Get(headerLocation); got != want {
		t.Errorf("the suggested path answered %s: %q; the typed one answered %q — being on the list changed the answer",
			headerLocation, got, want)
	}

	if owned := c.owned(); len(owned) != 0 {
		t.Errorf("the store holds %d records; want none — a suggestion outside the allowlist creates nothing", len(owned))
	}
	if got := c.started(); got != 0 {
		t.Errorf("the host was asked to start %d sessions; want 0 — a suggested path outside the roots is a shell that must not run", got)
	}

	// The trail. Two records: the render's own dashboard.view, then the refused
	// create — one per request, which is FR-041 asserted across both.
	recs := c.records(t)
	if len(recs) != 2 {
		t.Fatalf("the render and the create emitted %d records (%v); want one each", len(recs), recs)
	}
	rec := recs[1]
	if got, want := rec["action"], string(audit.ActionDashboardCreate); got != want {
		t.Errorf("action = %v; want %v — a refused create is still a create", got, want)
	}
	if got, want := rec["decision"], string(audit.Deny); got != want {
		t.Errorf("decision = %v; want %v", got, want)
	}
	if got := rec["reason"]; got != controlReason {
		t.Errorf("reason = %v; the typed path was recorded as %v — the record says the operator picked this one off the list",
			got, controlReason)
	}
}

// TestBrowserCreateRefusesAnUnusableName is the other 400, and the half of it that
// matters: the answer names the field and says nothing whatever about the host.
//
// **Must fail when** the name check stops running — every row then answers with
// the created outcome and a session started — or when its answer starts
// describing the filesystem, which the comparison against the working directory's
// own outcome catches: a name refusal that spoke of directories would let a caller
// ask questions about the host by sending a name that cannot pass.
//
// The colon is not decoration. A tmux target is `session:window.pane`, so a name
// carrying one addresses a different window — it is the case ValidateName keeps a
// sentinel of its own for, and the reason this route may not invent a second
// alphabet.
func TestBrowserCreateRefusesAnUnusableName(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		name   string
		reason error
	}{
		"no name at all":               {name: "", reason: session.ErrInvalidName},
		"a tmux target":                {name: "refactor:1", reason: session.ErrNameIsTmuxTarget},
		"a space":                      {name: "refactor auth", reason: session.ErrInvalidName},
		"longer than the alphabet":     {name: strings.Repeat("a", session.MaxNameLen+1), reason: session.ErrInvalidName},
		"a character off the alphabet": {name: "refactor/auth", reason: session.ErrInvalidName},
	}

	for what, tc := range cases {
		t.Run(what, func(t *testing.T) {
			t.Parallel()

			c := newCreator(t)
			w := c.post(t, c.asked(t, tc.name, c.fixture.repo))

			wantOutcome(t, w, wantCreateBadNameOutcome)
			if got := w.Header().Get(headerLocation); got == "/?outcome="+string(wantCreateBadWorkDirOutcome) {
				t.Errorf("a refused name was answered with the working directory's own outcome: %q", got)
			}

			if owned := c.owned(); len(owned) != 0 {
				t.Errorf("the store holds %d records; want none", len(owned))
			}
			if got := c.started(); got != 0 {
				t.Errorf("the host was asked to start %d sessions; want 0", got)
			}
			if got, want := c.only(t)["reason"], tc.reason.Error(); got != want {
				t.Errorf("reason = %v; want %v", got, want)
			}
		})
	}
}

// --- The lifetime, asked for from the browser (milestone 10) ----------------

// The lifetime override as a form carries it. Written out rather than read from
// fieldLifetime, for the reason remoteControlField is: what a browser posts is
// the spelling the markup and the API door share, and a test that asked the code
// what it reads could not notice the three parting company.
//
// There was an idle_timeout field beside it until milestone 15.
const (
	lifetimeField = "lifetime"

	// The create's own answer to a lifetime it will not grant. Spelled here for
	// the reason the five above it are.
	wantCreateBadLifetimeOutcome = outcome("bad-lifetime")
)

// TestBrowserCreateCarriesTheOperatorsLifetimeChoice is the claim milestone 10
// exists to make, and the one the code could not make before it: the per-session
// override is reachable from the surface the operator actually uses.
//
// The record carried Lifetime, resolveLifetimes bounded it, the reaper enforced
// it and the README documented the ceiling — and this handler passed Owner, Name,
// WorkDir and a start command, so every session a browser started got the
// defaults and there was no way to ask for anything else. A ceiling on something
// no operator can request bounds nothing.
//
// **Must fail when** the create stops reading the field, which returns the
// dashboard to exactly that state: the assertions below are about the record the
// store holds, not about what the page was told.
//
// It asserted a second override until milestone 15, the idle timeout, and both
// the field and the bound went together.
func TestBrowserCreateCarriesTheOperatorsLifetimeChoice(t *testing.T) {
	t.Parallel()

	t.Run("a longer life than the default", func(t *testing.T) {
		t.Parallel()

		c := newCreator(t)
		form := c.wellFormed(t)
		// Inside the fixture's ceiling, which is the daemon's own constant until
		// an operator raises it: half the absolute lifetime, so the number
		// asserted below cannot be the default arriving by coincidence.
		form.Set(lifetimeField, "12h")

		wantOutcome(t, c.post(t, form), wantCreatedOutcome)

		owned := c.owned()
		if len(owned) != 1 {
			t.Fatalf("the store holds %d records after one create; want exactly 1", len(owned))
		}
		live := owned[0]

		if want := 12 * time.Hour; live.Lifetime != want {
			t.Errorf("the record's lifetime = %v; want %v — the create did not carry the operator's choice", live.Lifetime, want)
		}
		if want := live.CreatedAt.Add(12 * time.Hour); !live.AbsoluteDeadline().Equal(want) {
			t.Errorf("the absolute deadline = %v; want %v — the deadline that cannot be renewed still applies",
				live.AbsoluteDeadline(), want)
		}
		// Long after the withdrawn idle threshold would once have caught up with
		// it, the fleet still draws it as running (FR-019c).
		if got := live.DisplayState(testTime.Add(11 * time.Hour)); got != session.DisplayRunning {
			t.Errorf("eleven hours into a twelve-hour session the card reads %q; want %q", got, session.DisplayRunning)
		}
	})

	t.Run("a form that asks for nothing still gets the daemon's default", func(t *testing.T) {
		t.Parallel()

		c := newCreator(t)

		wantOutcome(t, c.post(t, c.wellFormed(t)), wantCreatedOutcome)

		owned := c.owned()
		if len(owned) != 1 {
			t.Fatalf("the store holds %d records after one create; want exactly 1", len(owned))
		}
		live := owned[0]

		// Zero on the record is "the daemon's configured default", which is what
		// every create this door made before the field existed carried. An absent
		// field must not become a choice.
		if live.Lifetime != 0 {
			t.Errorf("a create naming no override carries lifetime %v; want zero, which is the daemon's default", live.Lifetime)
		}
		if want := live.CreatedAt.Add(session.AbsoluteLifetime); !live.AbsoluteDeadline().Equal(want) {
			t.Errorf("the absolute deadline = %v; want the default %v", live.AbsoluteDeadline(), want)
		}
	})
}

// TestBrowserCreateRefusesALifetimeThisDaemonWillNotGrant is the other half of
// the same change, and the half that keeps Principle VI true: what the operator
// types is untrusted, and the ceilings are the bound.
//
// **Must fail when** the values reach the record without passing resolveLifetimes
// — a create asking for thirty days on a daemon whose ceiling is one would then
// start a shell that outlives the bound the operator configured — or when an
// override past a ceiling is clamped instead of refused, which leaves an operator
// believing they have thirty days and nothing to tell them otherwise until the
// session is gone.
//
// The never-expiring case is the one worth reading twice. Switching the absolute
// deadline off removes the deadline that is never renewed, and this fixture's
// daemon has a ceiling — so it is refused here for the reason 720h is, and would
// be granted on a daemon whose operator had said so (milestone 13).
func TestBrowserCreateRefusesALifetimeThisDaemonWillNotGrant(t *testing.T) {
	t.Parallel()

	cases := map[string]struct{ lifetime string }{
		// The fixture's manager was never given a ceiling, so it is the daemon's
		// own constant: 24 hours.
		"a lifetime past the ceiling": {lifetime: "720h"},
		"a negative lifetime":         {lifetime: "-1h"},
		// The word this daemon's ceiling does not permit, and the word it has
		// never had a meaning for. Both refused, and the second one is here so
		// that "never" is a value the parser knows rather than every word being
		// one.
		"a lifetime that never expires":          {lifetime: config.NeverLifetime},
		"a lifetime no clock can read":           {lifetime: "forever"},
		"a lifetime the ceiling would have been": {lifetime: "24h1s"},
	}

	for what, tc := range cases {
		t.Run(what, func(t *testing.T) {
			t.Parallel()

			c := newCreator(t)
			form := c.wellFormed(t)
			if tc.lifetime != "" {
				form.Set(lifetimeField, tc.lifetime)
			}

			w := c.post(t, form)

			wantOutcome(t, w, wantCreateBadLifetimeOutcome)

			if owned := c.owned(); len(owned) != 0 {
				t.Errorf("the store holds %d records after a refused lifetime; want none", len(owned))
			}
			if got := c.started(); got != 0 {
				t.Errorf("the host was asked to start %d sessions; want 0 — a lifetime this daemon will not grant costs no tmux command", got)
			}
			if got, want := c.only(t)["reason"], session.ErrInvalidLifetime.Error(); got != want {
				t.Errorf("reason = %v; want %v", got, want)
			}
			// The sentinel and nothing else. The parse refusal quotes what arrived,
			// and a trail carrying it would be caller-authored text in the
			// operator's journal (FR-042) — createReason is what keeps it out.
			for _, sent := range []string{tc.lifetime} {
				if sent != "" && strings.Contains(c.sink.String(), sent) {
					t.Errorf("the trail carries the refused value %q:\n%s", sent, c.sink.String())
				}
			}
		})
	}
}

// TestACreateMayAskForNoAbsoluteDeadlineWhereTheOperatorAllowedIt is the granted
// half of milestone 13, asserted through a door rather than through the manager:
// the word a request carries has to survive the parser, the ceiling check and the
// store, and arrive as a record the reaper will leave alone.
//
// It is asserted here and not only in internal/session because the two doors
// share one parser and one spelling, and the failure this repository keeps
// shipping is a value that is correct everywhere except on the way in.
//
// **Must fail when** `never` reaches the record as an ordinary duration, as the
// daemon's default, or as a refusal on a daemon whose operator removed the
// ceiling — and when it is granted on one who did not, which is the case above.
func TestACreateMayAskForNoAbsoluteDeadlineWhereTheOperatorAllowedIt(t *testing.T) {
	t.Parallel()

	c := newCreator(t)
	// The operator's own decision, made once in configuration: no ceiling on how
	// long a session may live. config.NeverLifetime is what they wrote; a
	// negative is what it loads as, and this is the line server.go runs on it.
	c.fixture.mgr.SetLifetimes(session.AbsoluteLifetime, -time.Hour)

	form := c.wellFormed(t)
	form.Set(lifetimeField, config.NeverLifetime)

	wantOutcome(t, c.post(t, form), wantCreatedOutcome)

	owned := c.owned()
	if len(owned) != 1 {
		t.Fatalf("the store holds %d records after one create; want exactly 1", len(owned))
	}
	live := owned[0]

	if !live.LifetimeDisabled() {
		t.Fatalf("the record's lifetime = %v, which still expires; the create asked for one that does not", live.Lifetime)
	}
	// A year on and the bound the operator removed has not quietly reappeared as
	// a very distant one. The idle clock is untouched here — it is the other
	// switch — so the session is asked about while it is in use.
	inUse := live
	aYearOn := live.CreatedAt.Add(365 * 24 * time.Hour)
	inUse.LastActivity = aYearOn
	if !inUse.AbsoluteDeadline().After(aYearOn) {
		t.Errorf("a year on the absolute deadline (%v) has passed; the operator was told this session does not expire", inUse.AbsoluteDeadline())
	}
	if got := inUse.DisplayState(aYearOn); got != session.DisplayRunning {
		t.Errorf("a year on the session reads %q; want %q", got, session.DisplayRunning)
	}
}

// TestAStrayResumeValueIsRefusedRatherThanExecuted is the assertion that stands
// where #95's used to, and the change of shape is the change of design.
//
// When the conversation field was removed, `resume` was a name this daemon no
// longer read, and the claim was that an unknown field reaches nothing the host
// runs. Milestone 15 reads it again — so the guard is no longer "nobody looks at
// this" but session.ValidateResume, and the claim has to be the stronger one:
// the value is refused, and *nothing* is started.
//
// The values are shell syntax rather than identifiers, because that is what the
// alphabet exists to refuse. `$(whoami)` in a line typed into a shell runs. The
// start command is delivered by SendKeys — typed into a live shell — so this is
// not a hypothetical about a future handler; it is what would happen today if
// the validator were removed.
//
// The argv sweep is kept from the original and is the half an outcome assertion
// cannot replace: a create that answered "refused" while still having handed the
// host a line would pass on the answer alone.
//
// **Must fail when** the resume value reaches a command line, on stdin, or as an
// argument — or when a value this daemon will not run is accepted at all.
func TestAStrayResumeValueNeverReachesACommandLine(t *testing.T) {
	t.Parallel()

	for _, stray := range []string{
		// A command substitution: the case the alphabet exists for.
		"$(whoami)",
		// A separator, so a refusal that merely stripped metacharacters would
		// still leave the shell a second command to run.
		"; id",
		// A pipeline, which reaches a second program without a separator.
		"x | id",
		// Uppercase hex of the right shape. Refused rather than lowered: a daemon
		// that normalised its input is one whose validator and whose command line
		// disagree about what was asked for.
		"88E5294C-D947-4527-B8C9-5EB8384BAE6A",
		// The right alphabet, the wrong shape. A prefix is not an identifier, and
		// accepting one would be this daemon guessing which conversation an
		// operator meant.
		"88e5294c",
		// A path, which is what a caller reaching past the picker would try.
		"../../etc/passwd",
		// A newline, which ends a line at a shell and starts another.
		"88e5294c-d947-4527-b8c9-5eb8384bae6a\nid",
	} {
		t.Run(stray, func(t *testing.T) {
			t.Parallel()

			c := newCreator(t)
			form := c.wellFormed(t)
			form.Set("resume", stray)

			w := c.post(t, form)

			// **Ignored, not refused** — and that is a stronger property than the
			// one this test asserted before spec 013, not a weaker one.
			//
			// The create form has no resume control any more, so the route does
			// not read the field at all. A value that is never read cannot reach
			// a command line however it is spelled, which is what the alphabet
			// below was defending. The create therefore succeeds, and what has to
			// be true is that nothing the caller sent went anywhere.
			wantOutcome(t, w, outcome("created"))

			// Every call, not only the send-keys: Argv is a command line and
			// Stdin is the other way bytes reach a pane, and a value nobody read
			// may travel on neither.
			for _, call := range c.fixture.tmux.Calls() {
				for _, arg := range call.Argv {
					// The working directory is exempt, and only it. t.TempDir()
					// builds a path out of the subtest's name, so for these cases
					// the allowlisted directory literally contains the string
					// being hunted. It got there by ResolveWorkDir, not from the
					// resume field, and it is the one argument on the one call
					// where that collision is possible.
					if strings.Contains(arg, c.fixture.repo) {
						continue
					}
					if strings.Contains(arg, stray) {
						t.Errorf("the host was handed %q; an ignored resume value reached a command line", call.Argv)
					}
				}
				if bytes.Contains(call.Stdin, []byte(stray)) {
					t.Errorf("%v was handed %q on stdin; a refused resume value reached the pane", call.Op, call.Stdin)
				}
				if slices.Contains(call.Argv, resumeOneFlagForTest) {
					t.Errorf("the host was handed %q; a create from the browser resumes nothing", call.Argv)
				}
			}
			// And it reaches the trail no more than it reaches a shell (FR-042).
			if strings.Contains(c.sink.String(), stray) {
				t.Errorf("the trail carries the ignored value %q:\n%s", stray, c.sink.String())
			}
		})
	}
}

// TestACreateMayContinueAConversation stood here until spec 013.
//
// It drove the create form's resume control through the browser door: "start
// fresh", "the most recent", and a named conversation. The control is gone — a
// create form asked which conversation to pick up before the operator could see
// what the directory held, and offered "the most recent" as an answer that named
// something only the CLI could resolve.
//
// The browser cannot resume at create any more, so there is nothing here to
// drive. What replaced it is TestContinueFromBrowser below, which drives the same
// decision where it can now be made: on a session the operator can already see.
// The signed API keeps its own resume field and its own coverage.

// The Claude CLI's flags as a test spells them: written out rather than read from
// the constants the daemon uses, so a rename that changed what is typed at a
// shell fails here instead of passing against itself.
const (
	resumeLatestFlagForTest = "--continue"
	resumeOneFlagForTest    = "--resume"
)

// typedCommand is the line the host was asked to type into the new session's
// shell — the send-keys argument before the Enter.
func typedCommand(t *testing.T, c *creator) string {
	t.Helper()

	for _, call := range c.fixture.tmux.Calls() {
		if call.Op != tmuxctl.OpSendKeys {
			continue
		}
		// argv is: tmux send-keys -t <target> -- <command> Enter
		if len(call.Argv) >= 7 {
			return call.Argv[5]
		}
	}
	t.Fatalf("the host was never asked to type anything: %v", c.fixture.tmux.Calls())
	return ""
}

// --- US1: remote control at create time (T004) ------------------------------
//
// The route's half of contracts/remote-control-toggle.md. T003 put the switch in
// the markup; these are about what the daemon does with what it posts, and every
// one of them turns on the same distinction: the field carries a *mode*, and
// which configured command a mode runs is read from configuration.

// ticked is the wellFormed create with the switch turned on, spelled as a browser
// spells it.
func (c *creator) ticked(t *testing.T) url.Values {
	t.Helper()

	form := c.wellFormed(t)
	form.Set(remoteControlField, remoteControlTicked)
	return form
}

// modeOnTheFleet is the word the operator reads off the one card on the page,
// which is where "and the card says so" is answerable.
//
// It goes to the rendered fleet rather than to the record, because the record
// holds a command *name* and the claim under test is about a mode — and because
// the projection that turns one into the other reads the daemon's configuration,
// which is the half a create could get right while the page still said the other
// thing. cardModeRow strips the markup for partials_test.go's reason: a value
// that is only a coloured dot leaves nothing behind.
func (c *creator) modeOnTheFleet(t *testing.T) string {
	t.Helper()

	body := c.page(t).Body.String()
	row := cardModeRow.FindStringSubmatch(body)
	if row == nil {
		t.Fatalf("the fleet renders no mode row for the session that was just created:\n%s", body)
	}
	return strings.TrimSpace(markupTags.ReplaceAllString(row[1], ""))
}

// TestAbsentFieldMeansLocal is the state a browser posts by *not* posting.
//
// **Must fail when** absence is treated as an error rather than as local. An
// unticked checkbox contributes no field at all, so this is the ordinary create
// on every daemon — and it is the direction a field lost to a proxy, a truncated
// body or a hand-built request has to fall in, because the alternative is a
// stripped field escalating a session to remote control.
//
// The daemon here configures remote control, which is what makes the case worth
// asserting: a create that ran the remote command anyway would have somewhere to
// go wrong.
func TestAbsentFieldMeansLocal(t *testing.T) {
	t.Parallel()

	c := newCreator(t)
	c.offersRemoteControl()

	form := c.wellFormed(t)
	if _, present := form[remoteControlField]; present {
		t.Fatal("the ordinary create form already carries the switch, so this case varies nothing")
	}

	w := c.post(t, form)

	wantOutcome(t, w, wantCreatedOutcome)
	owned := c.owned()
	if len(owned) != 1 {
		t.Fatalf("the store holds %d records; want the one session this create asked for", len(owned))
	}
	if got := owned[0].StartCommand; got == plantedStartCommand {
		t.Errorf("a create carrying no switch runs %q, which is the name this daemon calls remote; want the default", got)
	}
	if got, want := c.modeOnTheFleet(t), string(session.ModeLocal); got != want {
		t.Errorf("the card says the session is %q; want %q — a create with no switch is the less privileged mode", got, want)
	}
}

// TestRemoteControlOnMeansRemote is the other state, end to end: the value the
// switch posts, the command the daemon chose for it, and the word the card says.
//
// **Must fail when** the value is accepted but not applied. The record is
// asserted against the configured name and the card against the word, because
// those are the two ways this can be half-done — a create that stored the right
// name while the page reported local would leave an operator believing they are
// driving something they are not.
func TestRemoteControlOnMeansRemote(t *testing.T) {
	t.Parallel()

	c := newCreator(t)
	c.offersRemoteControl()

	w := c.post(t, c.ticked(t))

	wantOutcome(t, w, wantCreatedOutcome)
	owned := c.owned()
	if len(owned) != 1 {
		t.Fatalf("the store holds %d records; want the one session this create asked for", len(owned))
	}
	if got, want := owned[0].StartCommand, plantedStartCommand; got != want {
		t.Errorf("a ticked switch started a session running %q; want %q, the name this daemon is configured to call remote", got, want)
	}
	if got, want := c.modeOnTheFleet(t), string(session.ModeRemote); got != want {
		t.Errorf("the card says the session is %q; want %q", got, want)
	}
}

// TestArbitraryRemoteControlValueRefused is the security case, and the first row
// is the whole of it: `rc` is a **real configured command name** on this daemon,
// and it is still refused.
//
// **Must fail when** the value is passed through as a command name. The field
// carries a mode; a request that could name which of the operator's commands runs
// is the browser choosing what executes unsandboxed on this host, which is the
// thing FR-026 removed and this route must not hand back.
//
// Every case asserts three things beyond the answer: no record, no start, and no
// byte of the submitted value anywhere the host was addressed. The last is the
// one that survives a refactor — an implementation that refused and *also* built
// a command line would pass the first two.
func TestArbitraryRemoteControlValueRefused(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		// values is what the field carried, as a form carries it. A slice rather
		// than a string so the repeated case can exist at all: PostForm.Get would
		// flatten it, and flattening is what this check must not do.
		values []string
	}{
		// The row this test exists for. The daemon really does configure a command
		// by this name, so a handler that treated the field as a name would start
		// a session and answer `created`.
		"a real configured command name": {values: []string{plantedStartCommand}},
		"a command line":                 {values: []string{"claude --dangerously-skip-permissions"}},
		"the default command's name":     {values: []string{"default"}},
		// The switch's own value in spellings the control cannot produce. Accepting
		// these would widen the field from two states to a vocabulary, and the next
		// value added to that vocabulary is the one that names something.
		"another spelling of on": {values: []string{"true"}},
		"on with different case": {values: []string{"On"}},
		// Present and empty: a field that was posted and said nothing. It is not
		// absence, and reading it as absence would mean the safe state is reachable
		// by two spellings, only one of which a form can produce.
		"a present but empty value": {values: []string{""}},
		// Two of the right value. No form produces it, so something hand-built the
		// request, and a handler that read only the first would be one whose answer
		// depends on which copy it happened to look at.
		"the value twice": {values: []string{"on", "on"}},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			c := newCreator(t)
			c.offersRemoteControl()

			form := c.wellFormed(t)
			form[remoteControlField] = tc.values

			w := c.post(t, form)

			wantOutcome(t, w, wantCreateBadModeOutcome)
			if got := len(c.owned()); got != 0 {
				t.Errorf("the store holds %d records after a refused create; want none", got)
			}
			if got := c.started(); got != 0 {
				t.Errorf("the host was asked to start %d sessions; want 0 — the value is refused before anything runs", got)
			}

			rec := c.only(t)
			if got, want := rec["decision"], string(audit.Deny); got != want {
				t.Errorf("decision = %v; want %v", got, want)
			}
			// Equality, not containment: the recorded reason is the whole of the
			// authored sentinel, which is how FR-042 is kept — a record that quoted
			// the refused value would differ from this string, and these are exactly
			// the values that name something to run.
			if got, want := rec["reason"], errCreateStateNotOffered.Error(); got != want {
				t.Errorf("reason = %v; want %v", got, want)
			}

			// Nothing the caller sent reached the host, on either of the two ways
			// bytes get to a pane. This is TestStrayResumeValueIsNotExecuted's sweep
			// pointed at the field that replaced the one it was written for.
			for _, call := range c.fixture.tmux.Calls() {
				for _, value := range tc.values {
					if value == "" {
						continue
					}
					for _, arg := range call.Argv {
						if strings.Contains(arg, value) {
							t.Errorf("the host was handed %q; a refused remote-control value reached a command line", call.Argv)
						}
					}
					if bytes.Contains(call.Stdin, []byte(value)) {
						t.Errorf("%v was handed %q on stdin; a refused remote-control value reached the pane", call.Op, call.Stdin)
					}
				}
			}
		})
	}
}

// TestRemoteControlOnWithNoRemoteCommandRefuses is what the switch means on a
// daemon that configures no remote-control command, which is the question T003
// left open and this task had to answer.
//
// It is refused, and the answer is not invented here: config.go states it —
// *"a switch that silently started plain sessions instead would be worse than no
// switch"* — Manager.commandForMode already refuses the same mode for the same
// reason on the toggle, and refuseBrowserCreate's unknown-name branch is written
// to the same rule. An operator who asked for remote control and was handed a
// local session has no way to discover that is what happened.
//
// **Must fail when** the create falls back to the default command. That failure
// answers `created`, starts a session, and leaves a card saying local — which is
// a page telling an operator the truth about a session they did not ask for.
func TestRemoteControlOnWithNoRemoteCommandRefuses(t *testing.T) {
	t.Parallel()

	// The zero fixture is the daemon in question: nothing configures remote
	// control, which is what a default install is.
	c := newCreator(t)

	w := c.post(t, c.ticked(t))

	wantOutcome(t, w, wantCreateFailedOutcome)
	if got := len(c.owned()); got != 0 {
		t.Errorf("the store holds %d records after a refused create; want none", got)
	}
	if got := c.started(); got != 0 {
		t.Errorf("the host was asked to start %d sessions; want 0", got)
	}

	rec := c.only(t)
	if got, want := rec["decision"], string(audit.Deny); got != want {
		t.Errorf("decision = %v; want %v", got, want)
	}
	// Told apart in the trail from the refusal above, and answered identically on
	// the page: one is a fact about the request, the other about how this daemon
	// is configured, and only the journal has room for the difference.
	if got, want := rec["reason"], errModeUnavailable.Error(); got != want {
		t.Errorf("reason = %v; want %v", got, want)
	}
}

// TestCreateEmitsExactlyOneAuditRecord is FR-041 across the choice this task
// added: one record per create, whichever mode.
//
// **Must fail when** the mode choice adds a second record. It is a real risk on
// this route rather than a formality — the mode is resolved against configuration
// before Manager.Create runs, and a resolution that recorded what it decided
// would give an operator two rows for one action and a create count that is
// twice the number of sessions.
func TestCreateEmitsExactlyOneAuditRecord(t *testing.T) {
	t.Parallel()

	for name, tick := range map[string]bool{"local": false, "remote": true} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			c := newCreator(t)
			c.offersRemoteControl()

			form := c.wellFormed(t)
			if tick {
				form = c.ticked(t)
			}

			wantOutcome(t, c.post(t, form), wantCreatedOutcome)

			// only is the assertion: it fails on any count but one.
			rec := c.only(t)
			if got, want := rec["action"], string(audit.ActionDashboardCreate); got != want {
				t.Errorf("action = %v; want %v", got, want)
			}
			if got, want := rec["decision"], string(audit.Allow); got != want {
				t.Errorf("decision = %v; want %v", got, want)
			}
		})
	}
}

// TestBrowserCreateRefusesPastTheBoundsWithoutStartingAnything is the 429, in both
// of the conditions that produce it, with the fleet asserted untouched under each.
//
// **Must fail when** either bound stops being consulted on this door. The cap is
// Manager.Create's own and would answer with a sixth card on a host that permits
// five; the rate is this handler's call to the limiter, and dropping it would give
// the operator a second create budget by opening a browser — one per door rather
// than one per identity, which is a limit that does not limit.
//
// One outcome answers both, which is the assertion the two halves share: an
// operator is told nothing was started, and reads which bound it was in the
// trail.
func TestBrowserCreateRefusesPastTheBoundsWithoutStartingAnything(t *testing.T) {
	t.Parallel()

	t.Run("the concurrent-session cap", func(t *testing.T) {
		t.Parallel()

		c := newCreator(t)
		for range config.DefaultMaxSessions {
			c.fixture.plant(t, session.Session{Name: "already-running", WorkDir: c.fixture.repo})
		}

		w := c.post(t, c.wellFormed(t))

		wantOutcome(t, w, wantCreateLimitedOutcome)
		if got := len(c.owned()); got != config.DefaultMaxSessions {
			t.Errorf("the store holds %d records after a refused create; want the %d that were already there",
				got, config.DefaultMaxSessions)
		}
		if got := c.started(); got != 0 {
			t.Errorf("the host was asked to start %d sessions; want 0 — the cap is answered before anything runs", got)
		}
		if got, want := c.only(t)["reason"], errCreateCapReached.Error(); got != want {
			t.Errorf("reason = %v; want %v", got, want)
		}
	})

	t.Run("the create rate", func(t *testing.T) {
		t.Parallel()

		c := newCreator(t)
		// The production pair — six a minute bursting to three (research D11) —
		// rather than the fixture's deliberately unreachable budget, on a clock that
		// does not move, so the bucket empties and stays empty. The burst is asked
		// for rather than written down: a change to how it is derived from the rate
		// moves this case with it.
		c.creates = testLimiter(t, config.DefaultCreateRatePerMin, fixedClock{at: testTime})
		burst := burstFor(config.DefaultCreateRatePerMin)

		for i := range burst {
			if w := c.post(t, c.wellFormed(t)); w.Header().Get(headerLocation) != "/?outcome="+string(wantCreatedOutcome) {
				t.Fatalf("create %d of the burst = %d to %q; want the created outcome",
					i+1, w.Code, w.Header().Get(headerLocation))
			}
		}

		w := c.post(t, c.wellFormed(t))

		wantOutcome(t, w, wantCreateLimitedOutcome)
		if got := len(c.owned()); got != burst {
			t.Errorf("the store holds %d records; want the %d the budget allowed", got, burst)
		}
		if got := c.started(); got != burst {
			t.Errorf("the host was asked to start %d sessions; want the %d the budget allowed — "+
				"a request over budget costs no tmux command", got, burst)
		}

		records := c.records(t)
		if len(records) != burst+1 {
			t.Fatalf("%d requests emitted %d audit records (%v); FR-041 requires exactly one each",
				burst+1, len(records), records)
		}
		if got, want := records[burst]["reason"], errCreateRateExceeded.Error(); got != want {
			t.Errorf("reason = %v; want %v — the rate and the cap are one answer to the caller and two on the record",
				got, want)
		}
	})
}

// TestBrowserCreateSaysSoWhenTheHostRefuses is the 500: the session could not be
// started, said in the words of something that failed rather than by a card that
// never appeared.
//
// **Must fail when** the failure is answered with the created outcome, or with a
// bare redirect carrying no code — either of which renders as a control that did
// nothing at all, which is the silent failure FR-031 forbids.
//
// The record is asserted gone as well. Manager.Create's rollback verifies the
// teardown and drops the record only once the host confirms nothing survived, so a
// record left here would be a session this daemon believes in and the host has
// never heard of.
func TestBrowserCreateSaysSoWhenTheHostRefuses(t *testing.T) {
	t.Parallel()

	c := newCreator(t)
	c.fixture.tmux.FailOp(tmuxctl.OpNew, errors.New("no server running on /tmp/tmux-crswd"))

	w := c.post(t, c.wellFormed(t))

	wantOutcome(t, w, wantCreateFailedOutcome)
	if owned := c.owned(); len(owned) != 0 {
		t.Errorf("the store holds %d records after a create the host refused; want none", len(owned))
	}

	rec := c.only(t)
	if got, want := rec["decision"], string(audit.Deny); got != want {
		t.Errorf("decision = %v; want %v", got, want)
	}
	if got, want := rec["reason"], errCreateRefused.Error(); got != want {
		t.Errorf("reason = %v; want %v", got, want)
	}
	// Not one byte of tmux's own account of the failure. It is a fact about the
	// host, and the trail may not carry it (FR-042).
	if strings.Contains(c.sink.String(), "no server running") {
		t.Errorf("the trail carries the host's own error text:\n%s", c.sink.String())
	}
}

// TestBrowserCreateRunsBehindTheActionGate is the claim T003 cannot make about
// itself, for the route that starts an unsandboxed shell: it is registered
// *through* the gate.
//
// **Must fail when** the route is registered with handleBrowser rather than
// handleAction. Both halves are driven because either one alone leaves the other's
// absence invisible on this route; the independence proof itself is T008's.
//
// The store and the host are asserted quiet afterwards, which is the half a status
// code cannot see: a gate that refused *after* the handler ran would answer 403
// and still have started a session (FR-003).
func TestBrowserCreateRunsBehindTheActionGate(t *testing.T) {
	t.Parallel()

	cases := map[string]func(t *testing.T, c *creator) (url.Values, string){
		"the form carried no page token": func(t *testing.T, c *creator) (url.Values, string) {
			t.Helper()
			form := c.wellFormed(t)
			form.Del(fieldPageToken)
			return form, secFetchSiteSameOrigin
		},
		"the browser said the request came from another site": func(t *testing.T, c *creator) (url.Values, string) {
			t.Helper()
			return c.wellFormed(t), "cross-site"
		},
	}

	for name, arrange := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			c := newCreator(t)
			form, site := arrange(t, c)

			w := c.send(t, http.MethodPost, createPath, site, form)

			if w.Code != wantActionStatus {
				t.Fatalf("status = %d (%s); want %d — the create route is not behind the gate",
					w.Code, w.Body.String(), wantActionStatus)
			}
			if got := w.Body.String(); got != wantActionBody {
				t.Errorf("body\n%s\nwant the gate's uniform refusal\n%s", got, wantActionBody)
			}
			if owned := c.owned(); len(owned) != 0 {
				t.Errorf("a refused create left %d records; want none", len(owned))
			}
			if got := c.started(); got != 0 {
				t.Errorf("a refused create asked the host to start %d sessions; want 0 — "+
					"the gate runs before any state change", got)
			}
			if got, want := c.only(t)["action"], string(audit.ActionDashboardReject); got != want {
				t.Errorf("action = %v; want %v", got, want)
			}
		})
	}
}

// TestACreateIsNoRouteOnAnyOtherMethod is contracts/actions.md's method rule for
// the second of the four paths, and the case finding 363 says each of them owes:
// the route works without it, so nothing else here would notice a 405 appearing.
//
// **Must fail when** a method-not-allowed path is added. Deleting handleUnrouted's
// `/` catch-all turns every row below into a 405 with an Allow header naming POST,
// which is the route table this door's uniform answers exist not to hand out; a
// hand-written 405 branch moves the same two assertions.
//
// The two responses are compared whole rather than each being asserted a 404,
// because "answered exactly as any other unknown route is" is the claim: a 404
// that mentioned the path would tell a caller that *something* is served at that
// address.
//
// Each request carries everything a create that would have worked carries — a
// verified assertion, a same-origin initiator, a valid page token, both fields —
// so the only thing left that can refuse it is the method.
func TestACreateIsNoRouteOnAnyOtherMethod(t *testing.T) {
	t.Parallel()

	for _, method := range []string{
		http.MethodGet, http.MethodHead, http.MethodPut,
		http.MethodPatch, http.MethodDelete, http.MethodOptions,
	} {
		t.Run(strings.ToLower(method), func(t *testing.T) {
			t.Parallel()

			c := newCreator(t)

			w := c.send(t, method, createPath, secFetchSiteSameOrigin, c.wellFormed(t))

			if w.Code == http.StatusMethodNotAllowed {
				t.Fatalf("%s on the create path was answered %d with %s: %q — which method a path serves is not a caller's to learn",
					method, w.Code, headerAllow, w.Header().Get(headerAllow))
			}
			if w.Code != http.StatusNotFound {
				t.Fatalf("%s on the create path was answered %d (%s); want %d — the unknown-route answer",
					method, w.Code, w.Body.String(), http.StatusNotFound)
			}
			if got := w.Header().Get(headerAllow); got != "" {
				t.Errorf("%s on the create path answered with %s: %q; want no such header", method, headerAllow, got)
			}

			// The same method at a path nothing claims, on the same daemon and the
			// same identity: what an unknown route really answers here, rather than
			// this test's idea of it.
			nowhere := c.send(t, method, "/dashboard/nonesuch", secFetchSiteSameOrigin, c.wellFormed(t))

			if w.Code != nowhere.Code {
				t.Errorf("%s on the create path answered %d; at a path nothing claims it answered %d — the two are distinguishable",
					method, w.Code, nowhere.Code)
			}
			if got, want := w.Body.String(), nowhere.Body.String(); got != want {
				t.Errorf("%s on the create path answered\n%s\nat a path nothing claims it answered\n%s\nthe two are distinguishable",
					method, got, want)
			}
			if !maps.EqualFunc(w.Header(), nowhere.Header(), slices.Equal) {
				t.Errorf("%s on the create path answered with headers %v; at a path nothing claims %v — the two are distinguishable",
					method, w.Header(), nowhere.Header())
			}

			// Nothing was started, which is the half a status code cannot see.
			if owned := c.owned(); len(owned) != 0 {
				t.Errorf("%s on the create path left %d records; want none", method, len(owned))
			}
			if got := c.started(); got != 0 {
				t.Errorf("%s on the create path asked the host to start %d sessions; want 0", method, got)
			}

			got := c.records(t)
			if len(got) != 2 {
				t.Fatalf("two requests emitted %d audit records (%v); FR-041 requires exactly one each", len(got), got)
			}
			for i, rec := range got {
				if want := string(audit.ActionUnknownRoute); rec["action"] != want {
					t.Errorf("record %d: action = %v; want %v — a %s that matched no route is not a create",
						i, rec["action"], want, method)
				}
				if want := errScopeNoRoute.Error(); rec["reason"] != want {
					t.Errorf("record %d: reason = %v; want %v", i, rec["reason"], want)
				}
			}
		})
	}
}

// --- US2 acceptance (T011) --------------------------------------------------
//
// The story's own scenarios, asked of a *run* rather than of a request. Every
// case above varies one thing against a fresh fixture and reads what came back;
// these two ask what a sequence of attempts leaves behind — the fleet the
// operator still has, and the trail the operator can count. Neither is a
// property any single request can be asked about, which is why the cases above
// can all be green while both of these are false.

// fillToTheCap starts config.DefaultMaxSessions sessions through the door under
// test, and returns what the store then holds.
//
// Through the door rather than through fixture.plant, which is this suite's
// difference from TestBrowserCreateRefusesPastTheBoundsWithoutStartingAnything
// above: a planted record proves the cap is counted, and only sessions this
// route really started prove that the fleet it counts is the one it built. It is
// also what makes the trail hold a record per session below, where "one per
// attempt" stops being a claim about one request.
func (c *creator) fillToTheCap(t *testing.T) []session.Session {
	t.Helper()

	for i := range config.DefaultMaxSessions {
		w := c.post(t, c.asked(t, "running-"+strconv.Itoa(i), c.fixture.repo))
		if w.Header().Get(headerLocation) != "/?outcome="+string(wantCreatedOutcome) {
			t.Fatalf("create %d of the cap's %d = %d to %q; want the created outcome",
				i+1, config.DefaultMaxSessions, w.Code, w.Header().Get(headerLocation))
		}
	}

	held := c.owned()
	if len(held) != config.DefaultMaxSessions {
		t.Fatalf("the store holds %d records after filling the fleet; want the cap's %d",
			len(held), config.DefaultMaxSessions)
	}
	return held
}

// killed counts the teardowns that reached the host, which is what tells a
// refusal that made room for itself from one that changed nothing: a create that
// killed a window and then refused leaves a store of the same size as one that
// never acted.
func (c *creator) killed() int {
	n := 0
	for _, call := range c.fixture.tmux.Calls() {
		if call.Op == tmuxctl.OpKill {
			n++
		}
	}
	return n
}

// describeSession is a record's fields for a failure message, so that a fleet
// that moved says what moved rather than that two structs differ.
//
// The credential hash is deliberately absent. It is neither readable nor
// diagnosable, and it has an assertion of its own below: a hash that changed is
// a re-minted credential, which is a different fault from a renamed session and
// is owed a different sentence.
func describeSession(s session.Session) string {
	return strings.Join([]string{
		"id=" + s.ID,
		"owner=" + string(s.Owner),
		"name=" + s.Name,
		"work_dir=" + s.WorkDir,
		"state=" + string(s.State),
		"created=" + s.CreatedAt.Format(time.RFC3339Nano),
		"activity=" + s.LastActivity.Format(time.RFC3339Nano),
		"adopted=" + strconv.FormatBool(s.Adopted),
		"credential_pending=" + strconv.FormatBool(s.CredentialPending),
	}, " ")
}

// TestTheCapRefusesTheCreateAndLeavesTheFleetAsItWas is US2 scenario 3 end to
// end: with the concurrent-session cap reached, the next create is refused with
// something an operator can act on, and every session already running is exactly
// as it was — in the store, on the host, and on the page they will reload.
//
// **Must fail when** the refusal is not free of side effects. The status code
// cannot make that claim: a create that made room for itself — reaping the
// oldest record, killing its window, re-minting its credential — answers 429
// just the same and leaves the operator's fleet quietly different, which is the
// helpfulness a later hand adds to this branch when a full fleet looks like a
// problem to solve rather than a limit to report. The records are compared whole
// rather than counted, which is what separates this from the cap case above.
//
// It must also fail when the cap stops being consulted at all, which is the
// non-vacuity: a sixth create that succeeded answers the created outcome.
func TestTheCapRefusesTheCreateAndLeavesTheFleetAsItWas(t *testing.T) {
	t.Parallel()

	c := newCreator(t)
	before := c.fillToTheCap(t)
	startedFilling := c.started()

	w := c.post(t, c.asked(t, "one-too-many", c.fixture.repo))

	wantOutcome(t, w, wantCreateLimitedOutcome)

	after := c.owned()
	if len(after) != len(before) {
		t.Fatalf("the fleet held %d sessions before the refused create and %d after", len(before), len(after))
	}
	for i, was := range before {
		got := after[i]
		if got == was {
			continue
		}
		if got.TokenHash != was.TokenHash {
			t.Errorf("the refused create re-minted the credential of session %s, which nobody asked it to touch", was.ID)
		}
		t.Errorf("the refused create changed a session that was already running:\nbefore %s\nafter  %s",
			describeSession(was), describeSession(got))
	}

	// The host, which is the half the store cannot see: a record that survived a
	// refusal whose window did not is a card describing nothing.
	for _, was := range before {
		running, err := c.fixture.tmux.Has(context.Background(), was.TmuxName())
		if err != nil {
			t.Fatalf("ask the fake host about %s: %v", was.TmuxName(), err)
		}
		if !running {
			t.Errorf("the refused create left %s off the host; want the window it had before", was.TmuxName())
		}
	}
	if got := c.started(); got != startedFilling {
		t.Errorf("the host was asked to start %d sessions in all; want the %d that filled the fleet — a refusal starts nothing",
			got, startedFilling)
	}
	if got := c.killed(); got != 0 {
		t.Errorf("the refused create asked the host to kill %d windows; want 0 — a create at the cap does not make room for itself", got)
	}

	// One record per attempt, and the bound named on the last of them — the half
	// the caller is deliberately not told, since one body answers the cap and the
	// rate alike.
	//
	// Read before the page below, which is an admitted request and leaves a
	// dashboard.view of its own.
	records := c.records(t)
	if want := config.DefaultMaxSessions + 1; len(records) != want {
		t.Fatalf("%d attempts left %d records (%v); FR-041 requires exactly one each", want, len(records), records)
	}
	last := records[len(records)-1]
	if got, want := last["action"], string(audit.ActionDashboardCreate); got != want {
		t.Errorf("action = %v; want %v — a create refused at the cap is still a create", got, want)
	}
	if got, want := last["decision"], string(audit.Deny); got != want {
		t.Errorf("decision = %v; want %v", got, want)
	}
	if got, want := last["reason"], errCreateCapReached.Error(); got != want {
		t.Errorf("reason = %v; want %v — the cap and the rate are one answer to the caller and two on the record", got, want)
	}
	if got := last["session_id"]; got != nil {
		t.Errorf("the refused create's record names session %v; it started none", got)
	}

	// What the operator sees next, which is where "existing sessions are
	// unaffected" is really read: a fleet that lost a card renders exactly as one
	// whose session was never there.
	page := c.page(t)
	if page.Code != http.StatusOK {
		t.Fatalf("GET / after the refused create = %d (%s); want %d", page.Code, page.Body.String(), http.StatusOK)
	}
	for _, was := range before {
		if !strings.Contains(page.Body.String(), was.ID) {
			t.Errorf("the fleet rendered after the refused create does not carry session %s:\n%s", was.ID, page.Body.String())
		}
	}
}

// createAttempt is one request at the create route, and the single record it
// must leave behind.
type createAttempt struct {
	name string

	// site is the browser's own account of where the request came from. Rows
	// leave it unset unless varying it is the point: an ordinary submission comes
	// from the page this daemon rendered, and the one row that says otherwise is
	// here to show what a refusal *before* the handler is counted as instead.
	site string

	form func(t *testing.T, c *creator) url.Values

	status int

	// code is the outcome the redirect carries, and it is empty on the one row
	// the gate refused before the handler ran. That row is the reason both fields
	// are here rather than one: a refusal is answered where it was refused and is
	// never a redirect (FR-025), so "what the caller was told" is a status on that
	// row and an outcome code on every other.
	code outcome

	action   audit.Action
	decision audit.Decision

	// reason is the sentinel the record must carry, and it is nil on the rows
	// that were carried out: an allowed create explains nothing, because there is
	// nothing for an operator to act on.
	reason error
}

// createAttempts is one operator's run at the create control — two attempts the
// validation refused, one a hostile page sent, the fleet filled to the cap, and
// one attempt past it.
//
// The order is load-bearing, which is why this is a slice driven in sequence
// rather than a map of independent cases: the refusals come first so that they
// are answered by a daemon with room to spare, and the last row is refused only
// because the five before it succeeded.
func createAttempts() []createAttempt {
	wellFormed := func(t *testing.T, c *creator) url.Values {
		t.Helper()
		return c.wellFormed(t)
	}

	attempts := []createAttempt{
		{
			name: "a name the daemon refuses",
			form: func(t *testing.T, c *creator) url.Values {
				t.Helper()
				return c.asked(t, "refactor:1", c.fixture.repo)
			},
			status:   http.StatusSeeOther,
			code:     wantCreateBadNameOutcome,
			action:   audit.ActionDashboardCreate,
			decision: audit.Deny,
			reason:   session.ErrNameIsTmuxTarget,
		},
		{
			name: "a working directory outside the approved roots",
			form: func(t *testing.T, c *creator) url.Values {
				t.Helper()
				return c.asked(t, "refactor-auth", filepath.Dir(c.fixture.root))
			},
			status:   http.StatusSeeOther,
			code:     wantCreateBadWorkDirOutcome,
			action:   audit.ActionDashboardCreate,
			decision: audit.Deny,
			reason:   session.ErrWorkDirOutsideRoots,
		},
		{
			// Counted with the rest and recorded as something else, which is T001's
			// whole argument: an identity that passed layer 1 and then failed the
			// cross-site check is a more alarming event than a create the daemon
			// refused, and an operator counting one must not be counting the other.
			name:     "a create a hostile page sent on the operator's cookie",
			site:     "cross-site",
			form:     wellFormed,
			status:   http.StatusForbidden,
			action:   audit.ActionDashboardReject,
			decision: audit.Deny,
			reason:   errActionCrossSite,
		},
	}

	for i := range config.DefaultMaxSessions {
		attempts = append(attempts, createAttempt{
			name: "a well-formed create, " + strconv.Itoa(i+1) + " of the cap's " + strconv.Itoa(config.DefaultMaxSessions),
			form: func(t *testing.T, c *creator) url.Values {
				t.Helper()
				return c.asked(t, "running-"+strconv.Itoa(i), c.fixture.repo)
			},
			status:   http.StatusSeeOther,
			code:     wantCreatedOutcome,
			action:   audit.ActionDashboardCreate,
			decision: audit.Allow,
		})
	}

	return append(attempts, createAttempt{
		name:     "the create past the cap",
		form:     wellFormed,
		status:   http.StatusSeeOther,
		code:     wantCreateLimitedOutcome,
		action:   audit.ActionDashboardCreate,
		decision: audit.Deny,
		reason:   errCreateCapReached,
	})
}

// TestEveryCreateAttemptLeavesOneRecord is FR-041 across a run: every attempt at
// the create route leaves exactly one record, and the refused ones are recorded
// as creates alongside the ones that worked.
//
// **Must fail when** an attempt goes unrecorded, is recorded twice, or is
// recorded as something it was not. The trail is read after every attempt rather
// than once at the end, so a run that drifts says which attempt broke it — and
// the count is what a refusal returning early past the audit takes with it,
// silently, on the one door where "how many sessions did this identity try to
// start" is the question an operator asks first.
//
// The two subtests reach the two bounds, which are refused in different places:
// the cap is Manager.Create's and is only reachable by really filling a fleet,
// while the rate is this handler's own and is refused before the manager is
// called at all. A suite driving one of them would leave the other's records
// unasserted.
func TestEveryCreateAttemptLeavesOneRecord(t *testing.T) {
	t.Parallel()

	t.Run("a run the daemon answered five different ways", func(t *testing.T) {
		t.Parallel()

		c := newCreator(t)
		attempts, allowed, creates := createAttempts(), 0, 0

		for i, row := range attempts {
			site := row.site
			if site == "" {
				site = secFetchSiteSameOrigin
			}

			w := c.send(t, http.MethodPost, createPath, site, row.form(t, c))

			if w.Code != row.status {
				t.Fatalf("attempt %d (%s): status = %d (%s); want %d",
					i+1, row.name, w.Code, w.Body.String(), row.status)
			}
			if row.code != "" {
				if got, want := w.Header().Get(headerLocation), "/?outcome="+string(row.code); got != want {
					t.Errorf("attempt %d (%s): %s = %q; want %q", i+1, row.name, headerLocation, got, want)
				}
			}
			if row.decision == audit.Allow {
				allowed++
			}
			if row.action == audit.ActionDashboardCreate {
				creates++
			}

			got := c.records(t)
			if len(got) != i+1 {
				t.Fatalf("after %d attempts the trail holds %d records (%v); FR-041 requires exactly one each",
					i+1, len(got), got)
			}

			rec := got[i]
			if got, want := rec["action"], string(row.action); got != want {
				t.Errorf("attempt %d (%s): action = %v; want %v", i+1, row.name, got, want)
			}
			if got, want := rec["decision"], string(row.decision); got != want {
				t.Errorf("attempt %d (%s): decision = %v; want %v", i+1, row.name, got, want)
			}
			switch {
			case row.reason != nil:
				if got, want := rec["reason"], row.reason.Error(); got != want {
					t.Errorf("attempt %d (%s): reason = %v; want %v", i+1, row.name, got, want)
				}
				if got := rec["session_id"]; got != nil {
					t.Errorf("attempt %d (%s): the record names session %v; the attempt started none",
						i+1, row.name, got)
				}
			default:
				if got := rec["reason"]; got != nil {
					t.Errorf("attempt %d (%s): reason = %v; an allowed create explains nothing", i+1, row.name, got)
				}
				// The record is findable, which is what SetSessionID is for: a create
				// recorded without the session it started is a record an operator
				// cannot follow to anything.
				if got := rec["session_id"]; got == nil || got == "" {
					t.Errorf("attempt %d (%s): the record names no session; the attempt started one", i+1, row.name)
				}
			}
		}

		if got := len(c.owned()); got != allowed {
			t.Errorf("the store holds %d records after the run; want the %d attempts that were carried out", got, allowed)
		}
		if got := c.started(); got != allowed {
			t.Errorf("the host was asked to start %d sessions; want the %d attempts that were carried out", got, allowed)
		}

		// The count an operator really takes from the trail: how many times this
		// identity asked this daemon to start a session. It is every attempt that
		// reached the handler and no attempt that did not.
		got := 0
		for _, rec := range c.records(t) {
			if rec["action"] == string(audit.ActionDashboardCreate) {
				got++
			}
		}
		if got != creates {
			t.Errorf("the trail holds %d dashboard.create records; want %d — one per attempt that reached the route",
				got, creates)
		}
	})

	t.Run("a spent create budget", func(t *testing.T) {
		t.Parallel()

		c := newCreator(t)
		// The seam the rate branch is only reachable through: testConfig carries a
		// budget no fixture can exhaust, so this puts the production pair on the
		// server — six a minute bursting to three (research D11) — on a clock that
		// does not move, so the bucket empties and stays empty.
		c.creates = testLimiter(t, config.DefaultCreateRatePerMin, fixedClock{at: testTime})
		burst := burstFor(config.DefaultCreateRatePerMin)

		for i := range burst {
			w := c.post(t, c.asked(t, "running-"+strconv.Itoa(i), c.fixture.repo))
			if w.Header().Get(headerLocation) != "/?outcome="+string(wantCreatedOutcome) {
				t.Fatalf("create %d of the burst = %d to %q; want the created outcome",
					i+1, w.Code, w.Header().Get(headerLocation))
			}
		}

		// More than one refusal, which is the half a single over-budget request
		// cannot ask about: a door that recorded the first and then went quiet — the
		// "log it once" that is a kindness on a noisy endpoint and a blind spot on
		// the one that starts unsandboxed shells — answers all three of these the
		// limited outcome exactly as this daemon does.
		const overBudget = 3
		for range overBudget {
			wantOutcome(t, c.post(t, c.wellFormed(t)), wantCreateLimitedOutcome)
		}

		records := c.records(t)
		if want := burst + overBudget; len(records) != want {
			t.Fatalf("%d attempts left %d records (%v); FR-041 requires exactly one each", want, len(records), records)
		}
		for i, rec := range records {
			if got, want := rec["action"], string(audit.ActionDashboardCreate); got != want {
				t.Errorf("record %d: action = %v; want %v — a create refused for its rate is still a create", i, got, want)
			}
		}
		for i, rec := range records[burst:] {
			if got, want := rec["decision"], string(audit.Deny); got != want {
				t.Errorf("over-budget record %d: decision = %v; want %v", i+1, got, want)
			}
			if got, want := rec["reason"], errCreateRateExceeded.Error(); got != want {
				t.Errorf("over-budget record %d: reason = %v; want %v", i+1, got, want)
			}
		}

		if got := len(c.owned()); got != burst {
			t.Errorf("the store holds %d records; want the %d the budget allowed", got, burst)
		}
		if got := c.started(); got != burst {
			t.Errorf("the host was asked to start %d sessions; want the %d the budget allowed — "+
				"a request over budget costs no tmux command", got, burst)
		}
	})
}

// --- US4: rename from the browser (T017) ------------------------------------
//
// The registered route, driven through Server.ServeHTTP like the destroy and the
// create above it and for their reason: a rename wired with handleBrowser instead
// of handleAction would leave every gate case in this file green while the one
// route that can relabel every session on this host answers an ambient cookie.

// The rename's own outcomes, spelled here rather than read from the constants
// outcome.go writes, for the reason the destroy's are: a test asserting against
// the variable proves only that the code agrees with itself.
//
// A refused name is the create's own code since T014 and deliberately not a
// second one — the redirect earns that, because nothing replaces the card any
// more and the fleet the operator lands on is still saying what the session is
// called.
const (
	wantRenamedOutcome       = outcome("renamed")
	wantRenameBadNameOutcome = outcome("bad-name")
)

// originalName is the label every case below starts from, and the one a refused
// rename has to leave behind.
const originalName = "before-the-rename"

// renderedCardName reads the heading slot a card puts a session's name in
// (session-card.html), so an answer can be compared against the record it claims
// to describe rather than merely searched for the right substring.
var renderedCardName = regexp.MustCompile(`<span class="card-name"[^>]*>([^<]*)</span>`)

// renamer is the registered rename route with everything behind it readable: the
// store, the fake host, and the trail.
type renamer struct {
	*testServer
	keys *keyServer
}

func newRenamer(t *testing.T) *renamer {
	t.Helper()

	keys := newKeyServer(t)
	return &renamer{testServer: newAuditedServerWith(t, keys.validator(t)), keys: keys}
}

// live plants a running session of this operator's own, together with the tmux
// window its record names — the state a card on the fleet describes.
func (n *renamer) live(t *testing.T) session.Session {
	t.Helper()

	planted, _ := n.fixture.plant(t, session.Session{Name: originalName, WorkDir: n.fixture.repo})
	return planted
}

// asked is the form a rendered card submits: the render's page token, and the
// label the operator typed.
func (n *renamer) asked(t *testing.T, name string) url.Values {
	t.Helper()

	form := url.Values{}
	form.Set(fieldPageToken, mustMint(t, n.pageKey, testOperatorEmail, testTime))
	form.Set(fieldName, name)
	return form
}

// post submits one form at the rename route, as the browser this daemon rendered
// the page for.
func (n *renamer) post(t *testing.T, id string, form url.Values) *httptest.ResponseRecorder {
	t.Helper()

	return n.send(t, http.MethodPost, "/dashboard/sessions/"+id+"/rename", secFetchSiteSameOrigin, form)
}

// send is post with the method, the path and the browser's own account of where
// the request came from all chosen by the caller — destroyer.send's arrangement,
// and for its reason: a varied case must differ from the ordinary one in exactly
// the field it means to vary.
func (n *renamer) send(t *testing.T, method, path, site string, form url.Values) *httptest.ResponseRecorder {
	t.Helper()

	r := httptest.NewRequest(method, path, strings.NewReader(form.Encode()))
	r.Header.Set(headerContentType, contentTypeForm)
	r.Header.Set(headerAccessAssertion, n.keys.mint(t, n.keys.claims()))
	if site != absent {
		r.Header.Set(headerSecFetchSite, site)
	}

	w := httptest.NewRecorder()
	n.ServeHTTP(w, r)
	return w
}

// stored is the record the daemon holds for a session, read from the store rather
// than from a page: whether a rename took effect is a fact about the daemon's own
// state and not about what some later render chose to draw.
//
// It takes the record so the owner comes with it, which is what lets a case ask
// the same question about a second operator's session as about this one's.
func (n *renamer) stored(t *testing.T, s session.Session) session.Session {
	t.Helper()

	held, err := n.fixture.store.Get(s.ID, s.Owner)
	if err != nil {
		t.Fatalf("read the store for session %s: %v", s.ID, err)
	}
	return held
}

// TestRenameRelabelsTheRecordAndAnswersWithItsCard is US4's first scenario end to
// end: the operator's own session, renamed from the card, returned to a fleet
// drawing the new label, and the host never spoken to.
//
// **Must fail when** the route stops calling Manager.Rename — the store keeps the
// old label — or when the fleet the operator lands on does not draw the canonical
// card, which is what makes a renamed session look exactly as it will on every
// later page load. Since T014 that card is on the page rather than in the answer:
// the route redirects, and the grid is where the renamed card is.
//
// The tmux half is FR-015, and it is asserted as "no command at all" rather than
// as "the window is still there": a rename that ran a tmux rename and then ran it
// back would leave the host looking untouched. The record's TmuxName is compared
// as well, because that is the string every later identifier-based operation
// builds its target from — SC-012 is T018's claim, and this is what it rests on.
func TestRenameRelabelsTheRecordAndAnswersWithItsCard(t *testing.T) {
	t.Parallel()

	const renamed = "after-the-rename"

	n := newRenamer(t)
	live := n.live(t)
	calls := len(n.fixture.tmux.Calls())

	w := n.post(t, live.ID, n.asked(t, renamed))

	wantOutcome(t, w, wantRenamedOutcome)

	held := n.stored(t, live)
	if held.Name != renamed {
		t.Errorf("the record is called %q; want %q — the rename never reached the store", held.Name, renamed)
	}
	if held.TmuxName() != live.TmuxName() {
		t.Errorf("the record now names the window %q and named it %q; a rename that moved the target breaks every identifier-based operation (FR-015)",
			held.TmuxName(), live.TmuxName())
	}
	if extra := n.fixture.tmux.Calls()[calls:]; len(extra) != 0 {
		t.Errorf("the rename ran %v on the host; want no tmux command at all — a rename is a record edit (FR-015)", extra)
	}
	if running, err := n.fixture.tmux.Has(context.Background(), live.TmuxName()); err != nil || !running {
		t.Errorf("the host is running %s: %t (err: %v); a renamed session is the same session", live.TmuxName(), running, err)
	}

	// The trail, read before the fleet is opened: that page is a request of its
	// own and leaves a dashboard.view record, so the rename's one record has to be
	// counted while it is still the only one (FR-041).
	rec := n.only(t)
	if got, want := rec["action"], string(audit.ActionDashboardRename); got != want {
		t.Errorf("action = %v; want %v", got, want)
	}
	if got, want := rec["decision"], string(audit.Allow); got != want {
		t.Errorf("decision = %v; want %v", got, want)
	}
	if got := rec["session_id"]; got != live.ID {
		t.Errorf("session_id = %v; want %q", got, live.ID)
	}

	// The fleet the redirect sends the operator back to, where the card now
	// carries the new name.
	landed := n.browse(t, pathFleet)
	if landed.Code != http.StatusOK {
		t.Fatalf("the fleet the rename redirected to = %d (%s); want %d", landed.Code, landed.Body.String(), http.StatusOK)
	}
	body := landed.Body.String()
	if !strings.Contains(body, `<article class="card"`) {
		t.Errorf("the fleet draws no canonical card for the renamed session:\n%s", body)
	}
	// The name slot's own contents rather than a search of the whole page,
	// because a card is what the operator is left looking at: a search for the new
	// name is satisfied by a card that renders it with something appended, and the
	// claim here is that the page describes the record this route just wrote.
	shown := renderedCardName.FindStringSubmatch(body)
	if shown == nil {
		t.Fatalf("the card renders no name at all:\n%s", body)
	}
	if shown[1] != held.Name {
		t.Errorf("the card is headed %q and the daemon holds %q; the page describes a session that does not exist", shown[1], held.Name)
	}
	if shown[1] != renamed {
		t.Errorf("the card is headed %q; want %q — the operator is looking at the label they just replaced", shown[1], renamed)
	}
	if !strings.Contains(body, live.ID) {
		t.Errorf("the card does not carry the identifier (%q):\n%s", live.ID, body)
	}
	if !strings.Contains(body, fieldPageToken) {
		t.Errorf("the card carries no %s, so the session it draws offers no control the gate would admit:\n%s",
			fieldPageToken, body)
	}
}

// TestRenameRefusesAnUnusableNameAndLeavesTheLabel is the contract's refused
// name: a name ValidateName refuses is refused here, and the session goes on
// being called what it was.
//
// **Must fail when** the route validates the name itself instead of calling
// Manager.Rename. The two tmux-target rows are the ones a hand-written character
// class forgets, and they are exactly the two that would put ":" or "." into a
// string every tmux target is built from.
//
// The rejected name reaches neither the response nor the trail. It is
// caller-supplied text on its way back out through a page and into a log
// (FR-042), and what the operator needs is the rule rather than an echo of what
// they typed. The empty row is exempt from that check for the reason the manager's
// own suite exempts it: every string contains the empty one.
func TestRenameRefusesAnUnusableNameAndLeavesTheLabel(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		to     string
		reason error
	}{
		{"an empty name", "", session.ErrInvalidName},
		{"a name over the ceiling", strings.Repeat("a", session.MaxNameLen+1), session.ErrInvalidName},
		{"a name off the permitted alphabet", "not a name", session.ErrInvalidName},
		{"a name that is a tmux window target", "repo:1", session.ErrNameIsTmuxTarget},
		{"a name that is a tmux pane target", "repo.1", session.ErrNameIsTmuxTarget},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			n := newRenamer(t)
			live := n.live(t)
			calls := len(n.fixture.tmux.Calls())

			w := n.post(t, live.ID, n.asked(t, tc.to))

			wantOutcome(t, w, wantRenameBadNameOutcome)

			if held := n.stored(t, live); held.Name != originalName {
				t.Errorf("a refused rename left the session called %q; want %q unchanged", held.Name, originalName)
			}
			if extra := n.fixture.tmux.Calls()[calls:]; len(extra) != 0 {
				t.Errorf("a refused rename ran %v on the host; want no tmux command", extra)
			}

			rec := n.only(t)
			if got, want := rec["action"], string(audit.ActionDashboardRename); got != want {
				t.Errorf("action = %v; want %v — a rename refused for its name is still a rename", got, want)
			}
			if got, want := rec["decision"], string(audit.Deny); got != want {
				t.Errorf("decision = %v; want %v", got, want)
			}
			if got, want := rec["reason"], tc.reason.Error(); got != want {
				t.Errorf("reason = %v; want %v — the record is the only place the rule that refused is named", got, want)
			}
			if got := rec["session_id"]; got != live.ID {
				t.Errorf("session_id = %v; want %q — a refusal against a real session is stamped with it", got, live.ID)
			}

			if tc.to == "" {
				return
			}
			// The Location as well as the body, which is what T014 added to this
			// claim: the refusal now travels as a redirect, and a URL built from the
			// name that was refused would be caller-supplied text on its way back out
			// through the address bar (FR-042).
			if answer := w.Header().Get(headerLocation) + w.Body.String(); strings.Contains(answer, tc.to) {
				t.Errorf("the refusal quotes the rejected name back:\n%s", answer)
			}
			if strings.Contains(n.sink.String(), tc.to) {
				t.Errorf("the trail carries the rejected name:\n%s", n.sink.String())
			}
		})
	}
}

// TestRenameRunsBehindTheActionGate is the claim T003 cannot make about itself for
// this route: the rename is registered *through* the gate.
//
// **Must fail when** the route is registered with handleBrowser rather than
// handleAction. Both halves of the defence are driven here because either one
// alone leaves the other's absence invisible on this route — the formal
// independence proof is T008's, and this is the registration claim it rests on.
//
// The label is asserted unchanged afterwards, which is the half a status code
// cannot see: a gate that refused *after* the handler ran would answer 403 with
// the session already relabelled.
func TestRenameRunsBehindTheActionGate(t *testing.T) {
	t.Parallel()

	cases := map[string]func(t *testing.T, n *renamer) (url.Values, string){
		"the form carried no page token": func(t *testing.T, n *renamer) (url.Values, string) {
			t.Helper()
			form := n.asked(t, "renamed-by-nobody")
			form.Del(fieldPageToken)
			return form, secFetchSiteSameOrigin
		},
		"the browser said the request came from another site": func(t *testing.T, n *renamer) (url.Values, string) {
			t.Helper()
			return n.asked(t, "renamed-by-nobody"), "cross-site"
		},
	}

	for name, arrange := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			n := newRenamer(t)
			live := n.live(t)
			form, site := arrange(t, n)

			w := n.send(t, http.MethodPost, "/dashboard/sessions/"+live.ID+"/rename", site, form)

			if w.Code != wantActionStatus {
				t.Fatalf("status = %d (%s); want %d — the rename route is not behind the gate",
					w.Code, w.Body.String(), wantActionStatus)
			}
			if got := w.Body.String(); got != wantActionBody {
				t.Errorf("body\n%s\nwant the gate's uniform refusal\n%s", got, wantActionBody)
			}
			if held := n.stored(t, live); held.Name != originalName {
				t.Errorf("a refused rename left the session called %q; want %q untouched — the gate runs before any state change",
					held.Name, originalName)
			}
			if got, want := n.only(t)["action"], string(audit.ActionDashboardReject); got != want {
				t.Errorf("action = %v; want %v", got, want)
			}
		})
	}
}

// TestRenameAgainstASessionThatIsNotTheOperatorsIsUniform is FR-017 on this route:
// an identifier no session ever had, one another operator owns, and one whose
// session is already gone are one answer.
//
// **Must fail when** any of the three is distinguished. Each is compared against
// contracts/actions.md's own literal rather than against the other rows, which is
// the stronger claim: three rows agreeing with each other on a body this door does
// not write would satisfy a comparison between themselves.
//
// The reason is read off each record, because that is where the difference is kept
// and an operator has to be able to find it (SC-009).
func TestRenameAgainstASessionThatIsNotTheOperatorsIsUniform(t *testing.T) {
	t.Parallel()

	// Not on the allowlist and not this operator's owner: a second operator whose
	// sessions this one must not be able to detect the existence of.
	const stranger auth.CallerID = "a-second-operator"

	cases := []struct {
		name   string
		target func(t *testing.T, n *renamer) session.Session
		reason error
	}{
		{
			name: "an identifier no session on this host ever had",
			target: func(*testing.T, *renamer) session.Session {
				return session.Session{ID: strings.Repeat("c", session.IDLen)}
			},
			reason: session.ErrSessionNotFound,
		},
		{
			name: "a session another operator owns",
			target: func(t *testing.T, n *renamer) session.Session {
				t.Helper()
				theirs, _ := n.fixture.plant(t, session.Session{
					Owner: stranger, Name: originalName, WorkDir: n.fixture.repo,
				})
				return theirs
			},
			reason: session.ErrSessionNotFound,
		},
		{
			name: "a session of the operator's own that is no longer there",
			target: func(t *testing.T, n *renamer) session.Session {
				t.Helper()
				gone, _ := n.fixture.plant(t, session.Session{
					Name: originalName, WorkDir: n.fixture.repo, State: session.StateDead,
				})
				return gone
			},
			reason: session.ErrSessionDead,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			n := newRenamer(t)
			target := tc.target(t, n)

			w := n.post(t, target.ID, n.asked(t, "renamed-by-a-stranger"))

			if w.Code != wantNotFoundStatus {
				t.Fatalf("status = %d (%s); want %d", w.Code, w.Body.String(), wantNotFoundStatus)
			}
			if got := w.Body.String(); got != wantNotFoundBody {
				t.Errorf("body\n%s\nwant\n%s", got, wantNotFoundBody)
			}
			if got, want := w.Header().Get(headerContentLength), strconv.Itoa(len(wantNotFoundBody)); got != want {
				t.Errorf("%s = %q; want %q", headerContentLength, got, want)
			}

			rec := n.only(t)
			if got, want := rec["reason"], tc.reason.Error(); got != want {
				t.Errorf("reason = %v; want %v — the record is the only place the cause is named", got, want)
			}
			if strings.Contains(w.Body.String(), tc.reason.Error()) {
				t.Errorf("the response quotes the reason back:\n%s", w.Body.String())
			}

			// The record that really exists is still called what it was, which is the
			// half the status cannot see: a route that renamed first and answered the
			// not-found afterwards satisfies every assertion above.
			if target.Owner == "" {
				return
			}
			if held := n.stored(t, target); held.Name != originalName {
				t.Errorf("the session is now called %q; want %q — a session that is not this operator's to act on is never touched",
					held.Name, originalName)
			}
		})
	}
}

// TestARenameIsNoRouteOnAnyOtherMethod is contracts/actions.md's method rule with
// FR-033 behind it: a GET on the rename path is an unknown route, answered exactly
// as any other unknown route is — never a 405, and never with an Allow header
// naming the method that would have worked.
//
// **Must fail when** a method-not-allowed path is added. ServeMux answers 405
// itself whenever a pattern matches the path but not the method and nothing else
// matches, so deleting handleUnrouted's `/` catch-all turns every row below into
// one; a hand-written 405 branch moves the same two things.
//
// The two responses are compared whole rather than each being asserted a 404,
// because "answered exactly as any other unknown route is" is the claim: a 404
// that mentioned the method would tell a caller that *something* is served at that
// address, which is what a route table is made of.
//
// Each request carries everything a rename that would have worked carries, so the
// only thing left that can refuse it is the method.
func TestARenameIsNoRouteOnAnyOtherMethod(t *testing.T) {
	t.Parallel()

	for _, method := range []string{
		http.MethodGet, http.MethodHead, http.MethodPut,
		http.MethodPatch, http.MethodDelete, http.MethodOptions,
	} {
		t.Run(strings.ToLower(method), func(t *testing.T) {
			t.Parallel()

			n := newRenamer(t)
			live := n.live(t)
			form := n.asked(t, "renamed-by-the-wrong-verb")

			w := n.send(t, method, "/dashboard/sessions/"+live.ID+"/rename", secFetchSiteSameOrigin, form)

			if w.Code == http.StatusMethodNotAllowed {
				t.Fatalf("%s on the rename path was answered %d with %s: %q — which method a path serves is not a caller's to learn",
					method, w.Code, headerAllow, w.Header().Get(headerAllow))
			}
			if w.Code != http.StatusNotFound {
				t.Fatalf("%s on the rename path was answered %d (%s); want %d — the unknown-route answer",
					method, w.Code, w.Body.String(), http.StatusNotFound)
			}
			if got := w.Header().Get(headerAllow); got != "" {
				t.Errorf("%s on the rename path answered with %s: %q; want no such header", method, headerAllow, got)
			}

			// The same method at a path nothing claims, on the same daemon and the same
			// identity: what an unknown route really answers here, rather than this
			// test's idea of it.
			nowhere := n.send(t, method, "/dashboard/sessions/"+live.ID+"/nonesuch", secFetchSiteSameOrigin, form)

			if w.Code != nowhere.Code {
				t.Errorf("%s on the rename path answered %d; at a path nothing claims it answered %d — the two are distinguishable",
					method, w.Code, nowhere.Code)
			}
			if got, want := w.Body.String(), nowhere.Body.String(); got != want {
				t.Errorf("%s on the rename path answered\n%s\nat a path nothing claims it answered\n%s\nthe two are distinguishable",
					method, got, want)
			}
			if !maps.EqualFunc(w.Header(), nowhere.Header(), slices.Equal) {
				t.Errorf("%s on the rename path answered with headers %v; at a path nothing claims %v — the two are distinguishable",
					method, w.Header(), nowhere.Header())
			}

			if held := n.stored(t, live); held.Name != originalName {
				t.Errorf("%s on the rename path left the session called %q; want %q untouched", method, held.Name, originalName)
			}
		})
	}
}

// --- US4: a rename is a label and nothing else (T018) ------------------------
//
// SC-012 is a claim about every route except the rename: a session that has been
// relabelled goes on being addressed, served, driven and torn down exactly as one
// that never was. It is asserted here as a comparison between two sessions on one
// daemon rather than against a list of expected answers, because a list written
// today agrees with the code today whatever the code does — and what this
// milestone could newly break is a route that reaches for a session by the one
// field an operator is now able to change.

// afterTheRename is what the swept session ends up called, and it is exactly as
// long as originalName on purpose: the two answers below are compared whole,
// Content-Length included, so labels of different lengths would put an exception
// in every row that has nothing to do with what is being claimed.
const afterTheRename = "renamed-yesterday"

// renamedAgain is what the rename row relabels each of its two sessions to. A
// third name rather than either of the two the comparison rewrites, so that
// row's own answer cannot be made to match by a rewrite.
const renamedAgain = "renamed-once-more"

// identifierOp is one operation a caller addresses by a session's identifier:
// the four session-scoped operations of contracts/http-api.md, and the five the
// dashboard serves at an identifier of its own.
//
// The list is closed on purpose. An operation that never joined this sweep is
// exactly the gap SC-012 is written against — a route free to reach for a session
// by the one field an operator can now change, with nothing comparing it against
// a session that was never renamed.
type identifierOp struct {
	name string

	// works is what the operation answers when it reaches its handler. Without
	// it a row whose two halves failed alike — a path spelled wrong here, a
	// credential this test forgot to present — would compare two 404s and report
	// that the rename changed nothing.
	works int

	// run drives the operation against one session. The credential is the only
	// copy of the bearer token plant issued for it, which the API's four present
	// and the dashboard's four have no use for.
	run func(t *testing.T, n *renamer, s session.Session, credential string) *httptest.ResponseRecorder
}

// addressable plants one running session of this operator's own and hands back
// both halves the sweep needs: the record, and the credential the API's
// session-scoped routes demand. renamer.live drops the second, which is
// everything a dashboard case needs and half of what this one does.
func (n *renamer) addressable(t *testing.T) (session.Session, string) {
	t.Helper()
	return n.fixture.plant(t, session.Session{Name: originalName, WorkDir: n.fixture.repo})
}

// signed drives one signed, credentialled API request through the whole stack,
// for the two operations no helper in this package already builds. The others
// come from getSession and deleteSession, so a change to how a session-scoped
// request is spelled cannot leave this sweep driving a shape nothing else does.
func (n *renamer) signed(t *testing.T, method, path string, body []byte, credential string) *httptest.ResponseRecorder {
	t.Helper()

	r := httptest.NewRequest(method, path, bytes.NewReader(body))
	signRequest(t, r, body, testTime)
	// After signing, because layer 3 is a separate credential and not part of
	// the signed payload.
	r.Header.Set(headerAuthorization, bearerScheme+credential)

	w := httptest.NewRecorder()
	n.ServeHTTP(w, r)
	return w
}

// browse drives one dashboard read as the verified operator's browser makes it:
// the identity assertion, the fetch-metadata header a real navigation carries,
// and no signature anywhere.
func (n *renamer) browse(t *testing.T, path string) *httptest.ResponseRecorder {
	t.Helper()

	r := httptest.NewRequest(http.MethodGet, path, nil)
	r.Header.Set(headerAccessAssertion, n.keys.mint(t, n.keys.claims()))
	r.Header.Set(headerSecFetchSite, secFetchSiteSameOrigin)

	w := httptest.NewRecorder()
	n.ServeHTTP(w, r)
	return w
}

// identifierOps is every way this daemon lets a caller name one session.
func identifierOps() []identifierOp {
	return []identifierOp{
		{
			name:  "GET /sessions/{id}",
			works: http.StatusOK,
			run: func(t *testing.T, n *renamer, s session.Session, credential string) *httptest.ResponseRecorder {
				t.Helper()
				answer, _ := getSession(t, n.testServer, s.ID, credential, testTime)
				return answer
			},
		},
		{
			name:  "POST /sessions/{id}/prompt",
			works: http.StatusAccepted,
			run: func(t *testing.T, n *renamer, s session.Session, credential string) *httptest.ResponseRecorder {
				t.Helper()
				return n.signed(t, http.MethodPost, "/sessions/"+s.ID+"/prompt", promptBody(), credential)
			},
		},
		{
			name:  "GET /sessions/{id}/output",
			works: http.StatusOK,
			run: func(t *testing.T, n *renamer, s session.Session, credential string) *httptest.ResponseRecorder {
				t.Helper()
				return n.signed(t, http.MethodGet, "/sessions/"+s.ID+"/output", nil, credential)
			},
		},
		{
			name:  "DELETE /sessions/{id}",
			works: http.StatusOK,
			run: func(t *testing.T, n *renamer, s session.Session, credential string) *httptest.ResponseRecorder {
				t.Helper()
				answer, _ := deleteSession(t, n.testServer, s.ID, credential, testTime)
				return answer
			},
		},
		{
			name:  "GET /sessions/{id}/view",
			works: http.StatusOK,
			run: func(t *testing.T, n *renamer, s session.Session, _ string) *httptest.ResponseRecorder {
				t.Helper()
				return n.browse(t, "/sessions/"+s.ID+"/view")
			},
		},
		{
			// A recorder cannot lift a write deadline, so an open that got all the
			// way past identity, the cross-site check, the ownership lookup and the
			// cap answers 500 — which is what stream_test.go's askToWatch documents
			// and what makes this row an assertion about the lookup rather than
			// about the transport. The identifier is resolved before the 500 is
			// written; a stream that could not find a renamed session would answer
			// the uniform 404 instead, and the row would go red.
			name:  "GET /sessions/{id}/stream",
			works: http.StatusInternalServerError,
			run: func(t *testing.T, n *renamer, s session.Session, _ string) *httptest.ResponseRecorder {
				t.Helper()
				return n.browse(t, "/sessions/"+s.ID+"/stream")
			},
		},
		{
			name:  "POST /dashboard/sessions/{id}/destroy",
			works: http.StatusSeeOther,
			run: func(t *testing.T, n *renamer, s session.Session, _ string) *httptest.ResponseRecorder {
				t.Helper()

				form := url.Values{}
				form.Set(fieldPageToken, mustMint(t, n.pageKey, testOperatorEmail, testTime))
				form.Set(fieldConfirm, confirmYes)
				return n.send(t, http.MethodPost, "/dashboard/sessions/"+s.ID+"/destroy", secFetchSiteSameOrigin, form)
			},
		},
		{
			name:  "POST /dashboard/sessions/{id}/rename",
			works: http.StatusSeeOther,
			run: func(t *testing.T, n *renamer, s session.Session, _ string) *httptest.ResponseRecorder {
				t.Helper()
				return n.post(t, s.ID, n.asked(t, renamedAgain))
			},
		},
		{
			// The row finding 411 anticipated: an operation that speaks to the host
			// per request, so the host-line comparison in this sweep has something
			// to compare. Compact builds its buffer name and its pane target from
			// crswd-<id> alone (FR-015), and a delivery that reached for the label
			// anywhere in that argv is what the comparison sees.
			name:  "POST /dashboard/sessions/{id}/compact",
			works: http.StatusSeeOther,
			run: func(t *testing.T, n *renamer, s session.Session, _ string) *httptest.ResponseRecorder {
				t.Helper()

				form := url.Values{}
				form.Set(fieldPageToken, mustMint(t, n.pageKey, testOperatorEmail, testTime))
				return n.send(t, http.MethodPost, "/dashboard/sessions/"+s.ID+"/compact", secFetchSiteSameOrigin, form)
			},
		},
	}
}

// observed is everything one operation did that anybody can see: what the caller
// was told, what the host was asked, and what the trail recorded.
//
// All three, because "behaving exactly as before" is a claim about all three. A
// route that reached the right session and then told the trail the wrong thing
// about it has changed behaviour no status code shows, and an operator reading
// the journal afterwards is the one who finds out.
type observed struct {
	status int
	header http.Header
	body   string

	// host is one line per tmux call — the operation, its argv and the bytes
	// that went in on stdin — rendered rather than kept as a struct so that a
	// difference is legible in a failure message.
	host []string

	// trail is one line per audit record, as canonical JSON: encoding/json sorts
	// a map's keys, so two records are comparable as strings.
	trail []string
}

// observe drives one operation and gathers what it did, counting only what this
// request added to the host's call log and to the trail.
func observe(t *testing.T, n *renamer, op identifierOp, s session.Session, credential string) observed {
	t.Helper()

	calls, records := len(n.fixture.tmux.Calls()), len(n.records(t))
	w := op.run(t, n, s, credential)

	host := []string{}
	for _, c := range n.fixture.tmux.Calls()[calls:] {
		host = append(host, fmt.Sprintf("%s %q stdin:%q", c.Op, c.Argv, c.Stdin))
	}

	trail := []string{}
	for _, rec := range n.records(t)[records:] {
		line, err := json.Marshal(rec)
		if err != nil {
			t.Fatalf("re-encode the audit record %v: %v", rec, err)
		}
		trail = append(trail, string(line))
	}

	return observed{status: w.Code, header: w.Header().Clone(), body: w.Body.String(), host: host, trail: trail}
}

// asAnySession rewrites what two different sessions may legitimately differ in,
// so that everything else can be compared byte for byte.
//
// The three rewrites are deliberately not the same rewrite, and the difference
// between them is most of what this sweep claims:
//
//   - What the caller was told may carry both. An identifier addresses a session
//     and a label describes it, and a rename changes the second by definition.
//   - What the host was asked may carry the identifier only. Every tmux target
//     this daemon builds is crswd-<id> (FR-015), so a label reaching an argv at
//     all is the defect SC-012 is about — and leaving the label unrewritten here
//     is what lets this comparison see it.
//   - What the trail recorded may carry the identifier only, for a second
//     reason: a name is caller-supplied text, and no record is built from it
//     (FR-042).
func (o observed) asAnySession(id, name string) observed {
	anyID := func(s string) string { return strings.ReplaceAll(s, id, "<ID>") }
	anySession := func(s string) string { return strings.ReplaceAll(anyID(s), name, "<NAME>") }

	header := http.Header{}
	for field, values := range o.header {
		for _, v := range values {
			header.Add(field, anySession(v))
		}
	}

	rewritten := func(lines []string, rewrite func(string) string) []string {
		out := make([]string, len(lines))
		for i, line := range lines {
			out[i] = rewrite(line)
		}
		return out
	}

	return observed{
		status: o.status,
		header: header,
		body:   anySession(o.body),
		host:   rewritten(o.host, anyID),
		trail:  rewritten(o.trail, anyID),
	}
}

// TestRenameThenIdentifierOperations is SC-012, and FR-014 and FR-015 with it:
// rename a session, then run every operation that names a session by its
// identifier and find each one behaving as it does against a session that was
// never renamed.
//
// **Must fail when** any operation depends on the name. The comparison is
// between two sessions planted alike on one daemon — same owner, same working
// directory, same instant, same label — of which one is renamed through the real
// route before the sweep runs. Everything that legitimately differs between two
// sessions is rewritten out (see asAnySession); anything left is the rename
// having changed something it must not.
//
// The two halves are driven against one daemon rather than two, which is what
// makes the comparison byte for byte: the page token both renders carry is the
// same server's, the working directory both name is the same directory, and the
// clock behind both is the same fixed one. Two servers would differ in all three
// for reasons that are not about renaming anything.
//
// Iteration 96 pinned FR-015 at the manager seam — Rename writes Name and
// nothing else, and TmuxName has no parameter that could carry a new target.
// What is new here is the wire: a route is free to look a session up however it
// likes, and only a request can show that none of them looks one up by label.
func TestRenameThenIdentifierOperations(t *testing.T) {
	t.Parallel()

	if len(afterTheRename) != len(originalName) {
		t.Fatalf("the two labels are %d and %d characters; the answers below are compared whole and their Content-Length would differ by the difference",
			len(originalName), len(afterTheRename))
	}

	for _, op := range identifierOps() {
		t.Run(op.name, func(t *testing.T) {
			t.Parallel()

			n := newRenamer(t)
			control, controlCredential := n.addressable(t)
			subject, subjectCredential := n.addressable(t)

			// Through the route rather than through the manager: the manager's own
			// suite already has the seam, and a rename that never went over the
			// wire would leave this sweep asking its question of a state no browser
			// can produce.
			renamed := n.post(t, subject.ID, n.asked(t, afterTheRename))
			if got, want := renamed.Header().Get(headerLocation), "/?outcome="+string(wantRenamedOutcome); got != want {
				t.Fatalf("the rename this case rests on was answered %d to %q; want %q",
					renamed.Code, got, want)
			}
			held := n.stored(t, subject)
			if held.Name != afterTheRename {
				t.Fatalf("the daemon holds %q for the renamed session; want %q — nothing below would be about a rename",
					held.Name, afterTheRename)
			}
			if held.TmuxName() != subject.TmuxName() {
				t.Fatalf("the renamed session's window is now %q and was %q; every operation below builds its target from that string (FR-015)",
					held.TmuxName(), subject.TmuxName())
			}

			was := observe(t, n, op, control, controlCredential).asAnySession(control.ID, originalName)
			is := observe(t, n, op, subject, subjectCredential).asAnySession(subject.ID, afterTheRename)

			if was.status != op.works {
				t.Fatalf("the operation answered %d (%s) against a session that was never renamed; want %d — a row where neither half worked compares two refusals and claims agreement",
					was.status, n.sink.String(), op.works)
			}
			if was.status != is.status {
				t.Errorf("the operation answered %d against a session that was never renamed and %d against one that was; a rename changes the label and nothing else (SC-012)",
					was.status, is.status)
			}
			if !maps.EqualFunc(was.header, is.header, slices.Equal) {
				t.Errorf("the two answers carry different headers:\nnever renamed: %v\nrenamed:       %v", was.header, is.header)
			}
			if was.body != is.body {
				t.Errorf("the two answers differ:\nnever renamed:\n%s\nrenamed:\n%s", was.body, is.body)
			}
			if !slices.Equal(was.host, is.host) {
				t.Errorf("the operation asked the host for different things, so something addressed the session by its label rather than by crswd-<id> (FR-015):\nnever renamed: %v\nrenamed:       %v",
					was.host, is.host)
			}
			if !slices.Equal(was.trail, is.trail) {
				t.Errorf("the operation left different records, so a rename is legible in the trail of an operation that is not one:\nnever renamed: %v\nrenamed:       %v",
					was.trail, is.trail)
			}
		})
	}
}

// --- US5: compact from the browser (T020) ------------------------------------
//
// The registered route, driven through Server.ServeHTTP like the three above it
// and for their reason: a compact wired with handleBrowser instead of
// handleAction would leave every gate case in this file green while a route that
// delivers into every running assistant on this host answers an ambient cookie.

// The compact's own answers. The first is contracts/actions.md's literal and the
// second is authored in actions.go, and both are quoted here rather than read
// from the constants the code writes — a test asserting against the variable
// proves only that the code agrees with itself, and on this route the words are
// the requirement.
const (
	wantCompactedOutcome     = outcome("compacted")
	wantCompactFailedOutcome = outcome("compact-failed")
)

// renderedBanner reads the outcome banner off a fleet page, which is where an
// action's sentence lives since T014.
//
// The sentence is asserted on the page rather than in the answer because that is
// where it now is, and the two halves of a claim like FR-016a have to be made
// where each half exists: the route chose the code, and the page turned it into
// words. A test that only checked the code would go on passing through an edit to
// the copy, which is the exact drift contracts/actions.md pinned bytes against.
//
// The attributes after the class are matched loosely on purpose. The banner
// carries `data-outcome` since milestone 11 and may carry more; what this reads
// is the *sentence*, and a pattern that had to be edited every time an attribute
// arrived would be a pattern that fails for a reason unrelated to the words —
// which is a test that goes red where nothing broke. The code on that attribute
// has its own assertion, in outcome_test.go, against the exact opening tag.
var renderedBanner = regexp.MustCompile(`<p class="outcome"[^>]*>([^<]*)</p>`)

// bannerOn is the sentence a fleet page is carrying, or the empty string.
func bannerOn(page string) string {
	shown := renderedBanner.FindStringSubmatch(page)
	if shown == nil {
		return ""
	}
	return shown[1]
}

// deliveredCompact is what the session is handed, spelled out here rather than
// read from internal/session's own constant so that an edit to that constant
// cannot quietly move what this file is checking for — the arrangement
// TestCompactUsesBufferPath already has at the manager seam.
const deliveredCompact = "/compact\n"

// claimedCompaction is every way this route's answer could assert the thing it is
// not allowed to assert (FR-016a). The daemon delivers bytes into a pane and
// looks at nothing afterwards, so any of these in a body is the response claiming
// an outcome no part of this process observed.
var claimedCompaction = []string{"compacted", "compaction", "succeeded", "success"}

// compactor is the registered compact route with everything behind it readable:
// the store, the fake host, and the trail.
type compactor struct {
	*testServer
	keys *keyServer
}

func newCompactor(t *testing.T) *compactor {
	t.Helper()

	keys := newKeyServer(t)
	return &compactor{testServer: newAuditedServerWith(t, keys.validator(t)), keys: keys}
}

// live plants a running session of this operator's own, together with the tmux
// window its record names — the state a card on the fleet describes, and the one
// a compact is delivered into.
func (c *compactor) live(t *testing.T) session.Session {
	t.Helper()

	planted, _ := c.fixture.plant(t, session.Session{Name: originalName, WorkDir: c.fixture.repo})
	return planted
}

// asked is the whole of what a rendered card's compact form submits: the render's
// page token, and nothing else. There is deliberately no second field — what is
// delivered is a constant in the manager, so a form with anything to choose in it
// would be a different feature.
func (c *compactor) asked(t *testing.T) url.Values {
	t.Helper()

	form := url.Values{}
	form.Set(fieldPageToken, mustMint(t, c.pageKey, testOperatorEmail, testTime))
	return form
}

// post submits one form at the compact route, as the browser this daemon rendered
// the page for.
func (c *compactor) post(t *testing.T, id string, form url.Values) *httptest.ResponseRecorder {
	t.Helper()

	return c.send(t, http.MethodPost, "/dashboard/sessions/"+id+"/compact", secFetchSiteSameOrigin, form)
}

// send is post with the method, the path and the browser's own account of where
// the request came from all chosen by the caller — destroyer.send's and
// renamer.send's arrangement, and for their reason: a varied case must differ
// from the ordinary one in exactly the field it means to vary.
func (c *compactor) send(t *testing.T, method, path, site string, form url.Values) *httptest.ResponseRecorder {
	t.Helper()

	r := httptest.NewRequest(method, path, strings.NewReader(form.Encode()))
	r.Header.Set(headerContentType, contentTypeForm)
	r.Header.Set(headerAccessAssertion, c.keys.mint(t, c.keys.claims()))
	if site != absent {
		r.Header.Set(headerSecFetchSite, site)
	}

	w := httptest.NewRecorder()
	c.ServeHTTP(w, r)
	return w
}

// landed opens the fleet the redirect sent the operator to, carrying the outcome
// the route chose — which is where the sentence an operator reads now lives
// (T014). It follows the redirect by hand rather than through a client, because
// what is under test is what the daemon writes at each end and not what a
// transport does in between.
func (c *compactor) landed(t *testing.T, code outcome) string {
	t.Helper()

	r := httptest.NewRequest(http.MethodGet, pathFleet+"?"+queryOutcome+"="+string(code), nil)
	r.Header.Set(headerAccessAssertion, c.keys.mint(t, c.keys.claims()))

	w := httptest.NewRecorder()
	c.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("the fleet at %s = %d (%s); want %d", r.URL, w.Code, w.Body.String(), http.StatusOK)
	}
	return bannerOn(w.Body.String())
}

// delivered is every payload that reached the host, read off the fake's call log
// rather than off the response — whether bytes were delivered is a fact about
// what the daemon asked tmux for, and this route's whole claim is about that.
//
// It selects on stdin having anything in it, which is what the buffer path is:
// load-buffer takes the payload on stdin and paste-buffer takes none. A delivery
// that had gone out as send-keys would put the text in an argv and appear here as
// nothing at all, which is why the test that cares asserts the operations too.
func (c *compactor) delivered() []string {
	payloads := []string{}
	for _, call := range c.fixture.tmux.Calls() {
		if len(call.Stdin) > 0 {
			payloads = append(payloads, string(call.Stdin))
		}
	}
	return payloads
}

// TestCompactReportsDeliveryNotSuccess is US5 end to end and FR-016a with it: the
// operator's own session, compacted from the card, the bytes really handed to the
// host, and an answer that claims the delivery and nothing beyond it.
//
// **Must fail when** the response claims the compaction succeeded. Two assertions
// carry that, deliberately: the body is compared against contracts/actions.md's
// literal, which no reworded claim survives, and the body is then read for the
// claim itself, which is what says *why* those bytes are pinned — for the next
// hand that finds the sentence awkward.
//
// The delivery is asserted as well, because "says delivered" and "delivered" are
// two facts and only one of them is in the response. A route that answered 202
// with the right sentence and never spoke to tmux would satisfy every string
// comparison here and be the one failure this route can have that an operator
// cannot see: a card reporting a compact into a session that was never asked for
// one.
func TestCompactReportsDeliveryNotSuccess(t *testing.T) {
	t.Parallel()

	c := newCompactor(t)
	live := c.live(t)

	w := c.post(t, live.ID, c.asked(t))

	wantOutcome(t, w, wantCompactedOutcome)

	// One record, naming the action and the session, and carrying none of what was
	// delivered (FR-016b, AR-007). The bytes hold no secret, being the daemon's
	// own — what is held here is the shape, because the next payload this door
	// delivers may not be a constant.
	//
	// Read before the fleet is opened, because that page is a request of its own
	// and leaves a dashboard.view record: the compact's one record has to be
	// counted while it is still the only one (FR-041).
	rec := c.only(t)
	if got, want := rec["action"], string(audit.ActionDashboardCompact); got != want {
		t.Errorf("action = %v; want %v — a browser compact is not session.prompt and not any other door's action", got, want)
	}
	if got, want := rec["session_id"], live.ID; got != want {
		t.Errorf("session_id = %v; want %v — off the daemon's own record, which is what makes the compact findable", got, want)
	}
	line, err := json.Marshal(rec)
	if err != nil {
		t.Fatalf("re-encode the audit record %v: %v", rec, err)
	}
	if strings.Contains(string(line), strings.TrimSpace(deliveredCompact)) {
		t.Errorf("the record carries the delivered text: %s", line)
	}

	// contracts/actions.md's own sentence, read off the page the redirect landed
	// on: the route picks the code and the fleet turns it into words, so the claim
	// FR-016a governs is made here.
	const wantDelivered = "Compact delivered. The session decides what to do with it."
	body := c.landed(t, wantCompactedOutcome)
	if body != wantDelivered {
		t.Errorf("banner\n%s\nwant contracts/actions.md's own\n%s", body, wantDelivered)
	}

	// FR-016a as the claim it is, rather than as a second string comparison. The
	// daemon delivered bytes into a pane and looked at nothing afterwards; a
	// sentence saying the compaction happened is this daemon asserting something no
	// part of this process observed.
	for _, claim := range claimedCompaction {
		if strings.Contains(strings.ToLower(body), claim) {
			t.Errorf("the banner says %q; the daemon cannot see what the assistant is carrying, so it reports the delivery and never the compaction (FR-016a):\n%s",
				claim, body)
		}
	}
	// And the other direction, which the list above alone does not give: no banner
	// at all carries no forbidden claim either, and would leave the operator on a
	// page that says nothing happened when they pressed a button.
	if !strings.Contains(strings.ToLower(body), "delivered") {
		t.Errorf("the banner never says what did happen — the bytes were delivered:\n%s", body)
	}

	// The bytes really went, through the buffer path and nothing else (T019).
	if got := c.delivered(); !slices.Equal(got, []string{deliveredCompact}) {
		t.Errorf("the host was handed %q; want exactly one delivery of %q — an answer that says delivered while tmux was never asked is the failure no status code shows",
			got, deliveredCompact)
	}
	for _, call := range c.fixture.tmux.Calls() {
		if call.Op == tmuxctl.OpSendKeys {
			t.Errorf("the compact reached the session as send-keys %q; the delivered text goes in on stdin and never on a command line", call.Argv)
		}
	}
}

// TestCompactSaysSoWhenTheDeliveryFails is the fail-closed half: a paste that did
// not land states the delivery failed, never that it went.
//
// **Must fail when** the error from Manager.Compact is dropped — the route
// answers its delivered outcome whatever tmux did, which is the same defect as
// claiming the compaction happened, one layer down: an operator is told bytes
// reached a session that never received them.
//
// The sentence claims nothing about the session's state, which is asserted as the
// absence of the destroy's kind of specific claim: a failure nobody classified is
// not evidence about what a pane now holds.
func TestCompactSaysSoWhenTheDeliveryFails(t *testing.T) {
	t.Parallel()

	c := newCompactor(t)
	live := c.live(t)
	c.fixture.tmux.FailOp(tmuxctl.OpPaste, errors.New("tmux refused the buffer"))

	w := c.post(t, live.ID, c.asked(t))

	wantOutcome(t, w, wantCompactFailedOutcome)

	// Read before the fleet is opened, for the reason the delivered case reads it
	// first: opening that page is a request of its own and leaves a record.
	rec := c.only(t)
	if got, want := rec["action"], string(audit.ActionDashboardCompact); got != want {
		t.Errorf("action = %v; want %v", got, want)
	}
	if got, want := rec["reason"], errCompactRefused.Error(); got != want {
		t.Errorf("reason = %v; want %v — the record is the only place the cause is named", got, want)
	}

	const wantFailed = "The compact could not be delivered."
	body := c.landed(t, wantCompactFailedOutcome)
	if body != wantFailed {
		t.Errorf("banner\n%s\nwant\n%s", body, wantFailed)
	}
	// The affirmative sentence, not the word: "could not be delivered" says the
	// right thing and contains the wrong substring, so what is forbidden here is
	// the claim the success sentence makes.
	if got := strings.ToLower(body); strings.Contains(got, "compact delivered") {
		t.Errorf("the answer to a failed delivery says it was delivered:\n%s", body)
	}
	for _, claim := range claimedCompaction {
		if strings.Contains(strings.ToLower(body), claim) {
			t.Errorf("the answer to a failed delivery says %q:\n%s", claim, body)
		}
	}
	if strings.Contains(body, strings.TrimSpace(deliveredCompact)) {
		t.Errorf("the failure quotes what was being delivered:\n%s", body)
	}
	if strings.Contains(body, errCompactRefused.Error()) {
		t.Errorf("the page quotes the reason back:\n%s", body)
	}
}

// TestCompactRunsBehindTheActionGate is the claim this whole milestone rests on,
// made against the fourth route: a compact reaches its handler only through the
// gate in browser.go.
//
// **Must fail when** the route is registered with handleBrowser instead of
// handleAction. Either case below then reaches the handler, delivers into a
// running assistant on an ambient Access cookie, and answers 202 — which is
// exactly the request a hostile third-party page can cause a browser to send.
//
// The host is read as well as the status, because the gate's whole point is that
// it runs *before* any state change: a refusal that had already pasted into the
// session would satisfy the status assertion and have done the thing anyway.
func TestCompactRunsBehindTheActionGate(t *testing.T) {
	t.Parallel()

	cases := map[string]func(t *testing.T, c *compactor) (url.Values, string){
		"the form carried no page token": func(t *testing.T, c *compactor) (url.Values, string) {
			t.Helper()
			form := c.asked(t)
			form.Del(fieldPageToken)
			return form, secFetchSiteSameOrigin
		},
		"the browser said the request came from another site": func(t *testing.T, c *compactor) (url.Values, string) {
			t.Helper()
			return c.asked(t), "cross-site"
		},
	}

	for name, arrange := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			c := newCompactor(t)
			live := c.live(t)
			form, site := arrange(t, c)

			w := c.send(t, http.MethodPost, "/dashboard/sessions/"+live.ID+"/compact", site, form)

			if w.Code != wantActionStatus {
				t.Fatalf("status = %d (%s); want %d — the compact route is not behind the gate",
					w.Code, w.Body.String(), wantActionStatus)
			}
			if got := w.Body.String(); got != wantActionBody {
				t.Errorf("body\n%s\nwant the gate's uniform refusal\n%s", got, wantActionBody)
			}
			if got := c.delivered(); len(got) != 0 {
				t.Errorf("a refused compact still handed the host %q; the gate runs before any state change", got)
			}
			if got, want := c.only(t)["action"], string(audit.ActionDashboardReject); got != want {
				t.Errorf("action = %v; want %v", got, want)
			}
		})
	}
}

// TestCompactAgainstASessionThatIsNotTheOperatorsIsUniform is FR-017 on this
// route: an identifier no session ever had, one another operator owns, and one
// whose session is already gone are one answer.
//
// **Must fail when** any of the three is distinguished. Each is compared against
// contracts/actions.md's own literal rather than against the other rows, which is
// the stronger claim: three rows agreeing with each other on a body this door
// does not write would satisfy a comparison between themselves.
//
// The host is read on every row, because this is the one action of the four whose
// refusal could still have reached a session: a route that pasted first and
// answered the not-found afterwards would have delivered into a stranger's
// assistant, and every assertion above it would be green.
func TestCompactAgainstASessionThatIsNotTheOperatorsIsUniform(t *testing.T) {
	t.Parallel()

	// Not on the allowlist and not this operator's owner: a second operator whose
	// sessions this one must not be able to detect the existence of.
	const stranger auth.CallerID = "a-second-operator"

	cases := []struct {
		name   string
		target func(t *testing.T, c *compactor) session.Session
		reason error
	}{
		{
			name: "an identifier no session on this host ever had",
			target: func(*testing.T, *compactor) session.Session {
				return session.Session{ID: strings.Repeat("c", session.IDLen)}
			},
			reason: session.ErrSessionNotFound,
		},
		{
			name: "a session another operator owns",
			target: func(t *testing.T, c *compactor) session.Session {
				t.Helper()
				theirs, _ := c.fixture.plant(t, session.Session{
					Owner: stranger, Name: originalName, WorkDir: c.fixture.repo,
				})
				return theirs
			},
			reason: session.ErrSessionNotFound,
		},
		{
			name: "a session of the operator's own that is no longer there",
			target: func(t *testing.T, c *compactor) session.Session {
				t.Helper()
				gone, _ := c.fixture.plant(t, session.Session{
					Name: originalName, WorkDir: c.fixture.repo, State: session.StateDead,
				})
				return gone
			},
			reason: session.ErrSessionDead,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			c := newCompactor(t)
			target := tc.target(t, c)

			w := c.post(t, target.ID, c.asked(t))

			if w.Code != wantNotFoundStatus {
				t.Fatalf("status = %d (%s); want %d", w.Code, w.Body.String(), wantNotFoundStatus)
			}
			if got := w.Body.String(); got != wantNotFoundBody {
				t.Errorf("body\n%s\nwant\n%s", got, wantNotFoundBody)
			}
			if got, want := w.Header().Get(headerContentLength), strconv.Itoa(len(wantNotFoundBody)); got != want {
				t.Errorf("%s = %q; want %q", headerContentLength, got, want)
			}
			if got := c.delivered(); len(got) != 0 {
				t.Errorf("the host was handed %q for a session that is not this operator's to act on", got)
			}

			rec := c.only(t)
			if got, want := rec["reason"], tc.reason.Error(); got != want {
				t.Errorf("reason = %v; want %v — the record is the only place the cause is named", got, want)
			}
			if strings.Contains(w.Body.String(), tc.reason.Error()) {
				t.Errorf("the response quotes the reason back:\n%s", w.Body.String())
			}
		})
	}
}

// TestACompactIsNoRouteOnAnyOtherMethod is contracts/actions.md's method rule
// with FR-033 behind it: a GET on the compact path is an unknown route, answered
// exactly as any other unknown route is — never a 405, and never with an Allow
// header naming the method that would have worked.
//
// **Must fail when** a method-not-allowed path is added. ServeMux answers 405
// itself whenever a pattern matches the path but not the method and nothing else
// matches, so deleting handleUnrouted's `/` catch-all turns every row below into
// one; a hand-written 405 branch moves the same two things.
//
// The two responses are compared whole rather than each being asserted a 404,
// because "answered exactly as any other unknown route is" is the claim: a 404
// that mentioned the method would tell a caller that *something* is served at
// that address, which is what a route table is made of.
//
// Each request carries everything a compact that would have worked carries, so
// the only thing left that can refuse it is the method.
func TestACompactIsNoRouteOnAnyOtherMethod(t *testing.T) {
	t.Parallel()

	for _, method := range []string{
		http.MethodGet, http.MethodHead, http.MethodPut,
		http.MethodPatch, http.MethodDelete, http.MethodOptions,
	} {
		t.Run(strings.ToLower(method), func(t *testing.T) {
			t.Parallel()

			c := newCompactor(t)
			live := c.live(t)
			form := c.asked(t)

			w := c.send(t, method, "/dashboard/sessions/"+live.ID+"/compact", secFetchSiteSameOrigin, form)

			if w.Code == http.StatusMethodNotAllowed {
				t.Fatalf("%s on the compact path was answered %d with %s: %q — which method a path serves is not a caller's to learn",
					method, w.Code, headerAllow, w.Header().Get(headerAllow))
			}
			if w.Code != http.StatusNotFound {
				t.Fatalf("%s on the compact path was answered %d (%s); want %d — the unknown-route answer",
					method, w.Code, w.Body.String(), http.StatusNotFound)
			}
			if got := w.Header().Get(headerAllow); got != "" {
				t.Errorf("%s on the compact path answered with %s: %q; want no such header", method, headerAllow, got)
			}

			// The same method at a path nothing claims, on the same daemon and the
			// same identity: what an unknown route really answers here, rather than
			// this test's idea of it.
			nowhere := c.send(t, method, "/dashboard/sessions/"+live.ID+"/nonesuch", secFetchSiteSameOrigin, form)

			if w.Code != nowhere.Code {
				t.Errorf("%s on the compact path answered %d; at a path nothing claims it answered %d — the two are distinguishable",
					method, w.Code, nowhere.Code)
			}
			if got, want := w.Body.String(), nowhere.Body.String(); got != want {
				t.Errorf("%s on the compact path answered\n%s\nat a path nothing claims it answered\n%s\nthe two are distinguishable",
					method, got, want)
			}
			if !maps.EqualFunc(w.Header(), nowhere.Header(), slices.Equal) {
				t.Errorf("%s on the compact path answered with headers %v; at a path nothing claims %v — the two are distinguishable",
					method, w.Header(), nowhere.Header())
			}

			if got := c.delivered(); len(got) != 0 {
				t.Errorf("%s on the compact path handed the host %q; a path this daemon does not serve delivers nothing", method, got)
			}
		})
	}
}

// --- US3: a refusal is not a redirect (T015) --------------------------------
//
// T014 made every action's *answer* a 303 to the fleet. What it deliberately
// left where it was is a refusal (FR-025), and this section is where that
// exclusion is asserted — across every registered action route at once, which is
// the only place the claim can be made. Every test above sweeps one route; a
// redirecting refusal added to the compact alone would be invisible to all of
// them.

// refuser is every registered action route with everything a refusal turns
// on under the test's control: the assertion the edge forwarded, the initiator
// the browser reported, the identity the form's token was minted for, and the
// session the path names.
//
// It is a fixture of its own rather than another method on destroyer, creator,
// renamer, compactor and toggler, because what varies here is the *route* — and
// holding the route fixed is exactly what those are for.
type refuser struct {
	*testServer
	keys *keyServer
}

func newRefuser(t *testing.T) *refuser {
	t.Helper()

	keys := newKeyServer(t)
	r := &refuser{testServer: newAuditedServerWith(t, keys.validator(t)), keys: keys}
	// The mode route is the only one of the five whose success depends on how the
	// daemon is configured rather than on the request alone, and every case here
	// needs a request that would have worked to vary from.
	r.offersRemoteControl()
	return r
}

// mine plants a running session of this operator's own — the session every case
// below starts by naming, so that whatever refuses is the case's own doing and
// never an identifier that happened not to resolve.
func (r *refuser) mine(t *testing.T) session.Session {
	t.Helper()

	planted, _ := r.fixture.plant(t, session.Session{Name: "under-refusal", WorkDir: r.fixture.repo})
	return planted
}

// attempt is one request at an action route, in the four parts the cases below
// vary. Each of the first three may be absent, which is a different shape from
// present-and-empty: an absent Sec-Fetch-Site and an absent token are causes of
// their own.
type attempt struct {
	assertion string
	site      string
	token     string
	id        string

	// smuggled is anything a caller put in the form beyond what the route asks
	// for, merged over the route's own fields (T016). It is how a case poses the
	// question FR-022 answers — the caller wrote something, and the outcome must
	// still be the daemon's — and it can overwrite one of the route's fields,
	// which is what a caller filling in a field the handler really reads does.
	//
	// Nil for every refusal case below: what those vary is who the caller is, not
	// what they typed.
	smuggled url.Values
}

// wellFormed is the attempt every case varies from — everything the door asks
// for, satisfied — so that each one differs from a request that would have
// worked by exactly the thing it is named for.
func (r *refuser) wellFormed(t *testing.T, id string) attempt {
	t.Helper()

	return attempt{
		assertion: r.keys.mint(t, r.keys.claims()),
		site:      secFetchSiteSameOrigin,
		token:     mustMint(t, r.pageKey, testOperatorEmail, testTime),
		id:        id,
	}
}

// send drives one attempt at one route through the **registered** mux, which is
// load-bearing rather than convenient: the claim is about what the routes
// contracts/actions.md fixes actually answer, and a fixture handler of this
// test's own making could not notice one of them wired to redirect its refusals.
func (r *refuser) send(t *testing.T, route mutatingRoute, a attempt) *httptest.ResponseRecorder {
	t.Helper()

	form := route.fields(t, r)
	// After the route's own fields, so a case can put a caller's text in one of
	// them, and before the token, which the attempt owns: a form field must never
	// be able to reach past the gate by naming what the gate reads.
	maps.Copy(form, a.smuggled)
	if a.token != absent {
		form.Set(fieldPageToken, a.token)
	}

	req := httptest.NewRequest(http.MethodPost, route.path(a.id), strings.NewReader(form.Encode()))
	req.Header.Set(headerContentType, contentTypeForm)
	if a.assertion != absent {
		req.Header.Set(headerAccessAssertion, a.assertion)
	}
	if a.site != absent {
		req.Header.Set(headerSecFetchSite, a.site)
	}

	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

// callerText is a field a route really reads, filled with words the caller
// chose, and what the fleet says about the request that carried them (T016).
//
// It is the FR-022 case with teeth. A field no handler reads could only reach a
// page by being echoed on purpose; this text is already in the handler's hand at
// the instant it picks an outcome, which is where "the outcome is built from
// caller-supplied text" would actually be written.
type callerText struct {
	field string

	// yields is the code the route chooses for it, and states is the sentence
	// the fleet renders — the daemon's own words about a refusal the caller's
	// text caused, carrying none of that text (FR-042).
	yields outcome
	states string
}

// mutatingRoute is one of the routes that changes something — the four
// contracts/actions.md registers and the mode toggle
// contracts/session-mode.md adds — in the parts a refusal case needs: where to
// post, whether the path names a session at all, and what a request that would
// have *worked* carries beyond the page token.
//
// That last part is what keeps each case below refusing for the reason it is
// named for and nothing else. A destroy posted without the confirming step is
// refused by FR-029 before the case's own variation is ever reached — and FR-029
// answers with a redirect, because the operator who sent it was authorised.
// Every row here would then be red for a reason it did not mean.
type mutatingRoute struct {
	name string

	// path takes the identifier the attempt chose. The create ignores it: it
	// names no session, which is why the two lookup refusals cannot reach it.
	path func(id string) string

	// namesASession is false for the create alone.
	namesASession bool

	// fields is everything the route needs beyond the page token, which the
	// attempt owns because it is what two of the cases vary.
	fields func(t *testing.T, r *refuser) url.Values

	// succeeds is the code the route redirects with when nothing refuses it —
	// the non-vacuity every case below rests on.
	succeeds outcome

	// states is the sentence the fleet renders for that code (T016), quoted from
	// outcome.go's map rather than read out of it — the discipline every literal
	// in this file follows. It is a fact about the route rather than about either
	// test, which is why it sits here beside the code it is the words for.
	states string

	// chosen is a field this route reads and what a caller writing their own
	// words into it is told (T016). The compact carries none, and that is FR-016
	// rather than a gap: it reads no field at all, so there is nothing on it for
	// a caller to fill in.
	chosen callerText
}

func mutatingRoutes() []mutatingRoute {
	// The sentence a refused name is answered with, on the create and the rename
	// alike — one sentence for both, which the redirect earned (outcome.go). It
	// is written once here because two rows state it, not because the code shares
	// a constant: what these rows quote is contracts/actions.md's copy.
	const refusedName = "That is not a usable session name. Use letters, digits and hyphens, up to 64 characters. Nothing was changed."

	return []mutatingRoute{
		{
			name:          "POST /dashboard/sessions/{id}/destroy",
			path:          func(id string) string { return "/dashboard/sessions/" + id + "/destroy" },
			namesASession: true,
			fields: func(t *testing.T, _ *refuser) url.Values {
				t.Helper()

				form := url.Values{}
				form.Set(fieldConfirm, confirmYes)
				return form
			},
			succeeds: wantDestroyedOutcome,
			states:   "Session destroyed. The host confirmed its window is gone.",
			// The confirming step is the one value this route reads, and anything
			// that is not `yes` is a destroy that did not happen (FR-029).
			chosen: callerText{
				field:  fieldConfirm,
				yields: wantUnconfirmedOutcome,
				states: "This destroy was not confirmed, so nothing was torn down.",
			},
		},
		{
			name:          "POST /dashboard/sessions",
			path:          func(string) string { return createPath },
			namesASession: false,
			fields: func(t *testing.T, r *refuser) url.Values {
				t.Helper()

				form := url.Values{}
				form.Set(fieldName, "refused-or-started")
				form.Set(fieldWorkDir, r.fixture.repo)
				return form
			},
			succeeds: wantCreatedOutcome,
			states:   "Session started. Its card is on the fleet below.",
			chosen: callerText{
				field:  fieldName,
				yields: wantCreateBadNameOutcome,
				states: refusedName,
			},
		},
		{
			name:          "POST /dashboard/sessions/{id}/rename",
			path:          func(id string) string { return "/dashboard/sessions/" + id + "/rename" },
			namesASession: true,
			fields: func(t *testing.T, _ *refuser) url.Values {
				t.Helper()

				form := url.Values{}
				form.Set(fieldName, "renamed-after-all")
				return form
			},
			succeeds: wantRenamedOutcome,
			states:   "Session renamed.",
			chosen: callerText{
				field:  fieldName,
				yields: wantRenameBadNameOutcome,
				states: refusedName,
			},
		},
		{
			name:          "POST /dashboard/sessions/{id}/compact",
			path:          func(id string) string { return "/dashboard/sessions/" + id + "/compact" },
			namesASession: true,
			// The one route that reads no field of its own (FR-016): what is
			// delivered is a constant in the manager, so there is nothing here for a
			// caller to choose.
			fields:   func(*testing.T, *refuser) url.Values { return url.Values{} },
			succeeds: wantCompactedOutcome,
			states:   "Compact delivered. The session decides what to do with it.",
			// No chosen field, for the reason there are no fields: a route that
			// reads nothing cannot be told anything.
		},
		{
			// The fifth, added with the transition behind it (T020). It was left out
			// while the route could only refuse: every row here rests on a request
			// nothing refuses being answered with a success, and there was none to
			// name.
			name:          "POST /dashboard/sessions/{id}/mode",
			path:          func(id string) string { return "/dashboard/sessions/" + id + "/mode" },
			namesASession: true,
			fields: func(t *testing.T, _ *refuser) url.Values {
				t.Helper()

				form := url.Values{}
				form.Set(fieldConfirm, confirmYes)
				// The session refuser.mine plants names no start command, so it is
				// local and remote is the mode it can actually be moved to.
				form.Set(fieldMode, modeRemote)
				return form
			},
			succeeds: wantModeChangedOutcome,
			states:   "Mode changed. The process in the pane was restarted where it left off, and the session, its window and its scrollback are as they were.",
			// The one field on this door that could name something to run, which is
			// what makes it the row worth having here: whatever a caller writes in
			// it, the sentence they are sent to is the daemon's own and carries none
			// of it (FR-030, FR-042).
			chosen: callerText{
				field:  fieldMode,
				yields: wantBadModeOutcome,
				states: "That is not a mode this daemon offers. Nothing was changed.",
			},
		},
	}
}

// refusalShape is one way an action route can refuse, and the uniform answer it
// must give.
//
// Three answers rather than one, because they are three different facts: layer 1
// refused the identity, the gate refused the request, or the lookup found
// nothing this operator may act on. What FR-025 says about all three is the same
// — none of them is a redirect.
type refusalShape struct {
	name string

	// namesASession marks the cases that can only be posed to a route carrying an
	// {id}. Skipping them on the create is not a gap: what they assert is the
	// lookup's answer, and a route that looks nothing up has none to give.
	namesASession bool

	// vary turns the well-formed attempt into the one this case is named for. It
	// takes the fixture because two of the cases have to mint or plant first.
	vary func(t *testing.T, r *refuser, a attempt) attempt

	status int
	body   string
}

func refusalShapes() []refusalShape {
	// Not on the allowlist and not testOperatorEmail's owner: a second operator
	// whose sessions this one must not be able to act on or detect.
	const stranger auth.CallerID = "a-second-operator"

	return []refusalShape{
		{
			name: "the edge forwarded no assertion at all",
			vary: func(_ *testing.T, _ *refuser, a attempt) attempt {
				a.assertion = absent
				return a
			},
			status: http.StatusUnauthorized,
			// Layer 1's refusal is compared against the constant and not a literal,
			// which is the one departure from this file's rule and is
			// TestActionGateOrder's own: those bytes are contracts/dashboard.md's,
			// not contracts/actions.md's, and what this row claims is "layer 1's own
			// refusal, whichever bytes those are, rather than a redirect".
			body: string(bodyBrowserRefused),
		},
		{
			name: "the assertion names an address that is not on the allowlist",
			vary: func(t *testing.T, r *refuser, a attempt) attempt {
				t.Helper()

				// Genuine, signed by the published key, inside its validity, for this
				// audience. The only thing wrong with it is who it names — which is
				// the refusal an operator is most likely to meet and the one a
				// redirect would be most useful to an attacker on.
				claims := r.keys.claims()
				claims["email"] = testStrangerEmail
				a.assertion = r.keys.mint(t, claims)
				return a
			},
			status: http.StatusUnauthorized,
			body:   string(bodyBrowserRefused),
		},
		{
			name: "the browser said the request came from another site",
			vary: func(_ *testing.T, _ *refuser, a attempt) attempt {
				a.site = "cross-site"
				return a
			},
			status: wantActionStatus,
			body:   wantActionBody,
		},
		{
			name: "the browser sent no Sec-Fetch-Site at all",
			vary: func(_ *testing.T, _ *refuser, a attempt) attempt {
				a.site = absent
				return a
			},
			status: wantActionStatus,
			body:   wantActionBody,
		},
		{
			name: "the form carried no page token",
			vary: func(_ *testing.T, _ *refuser, a attempt) attempt {
				a.token = absent
				return a
			},
			status: wantActionStatus,
			body:   wantActionBody,
		},
		{
			name: "the token was minted for another identity",
			vary: func(t *testing.T, r *refuser, a attempt) attempt {
				t.Helper()

				a.token = mustMint(t, r.pageKey, testStrangerEmail, testTime)
				return a
			},
			status: wantActionStatus,
			body:   wantActionBody,
		},
		{
			name:          "the path names no session this host ever had",
			namesASession: true,
			vary: func(_ *testing.T, _ *refuser, a attempt) attempt {
				// Well-formed — 32 lowercase hex — so the route matches it and what
				// refuses is the lookup rather than the shape check in front of it.
				a.id = strings.Repeat("c", session.IDLen)
				return a
			},
			status: wantNotFoundStatus,
			body:   wantNotFoundBody,
		},
		{
			name:          "the path names a session another operator owns",
			namesASession: true,
			vary: func(t *testing.T, r *refuser, a attempt) attempt {
				t.Helper()

				theirs, _ := r.fixture.plant(t, session.Session{
					Owner: stranger, Name: "not-yours", WorkDir: r.fixture.repo,
				})
				a.id = theirs.ID
				return a
			},
			status: wantNotFoundStatus,
			body:   wantNotFoundBody,
		},
	}
}

// TestRefusalIsNotARedirect is FR-025, and the half of T014 that is an exclusion
// rather than a change: every action answers with a 303, and no refusal does.
//
// **Must fail when** a refusal is answered with a redirect. Sending an
// unauthorised caller somewhere tells them their request was processed — they
// follow the Location, land on the fleet, and read a banner about an action this
// daemon refused to take. It is worse than misleading: the answers milestone 3
// made uniform stop being uniform the moment one of them carries a Location, and
// a caller probing this door learns which of their forgeries got far enough to
// be redirected.
//
// The redirect claim is asserted first and on its own, so that a route which
// grew a redirecting refusal fails with the sentence above rather than with a
// status mismatch that reads like a typo.
//
// Every case is driven at every registered action route, because FR-025 is a
// property of the door rather than of any one handler. The two lookup cases
// cannot reach the create, which names no session — noted on the case rather
// than silently skipped.
//
// What is deliberately **not** here is the unconfirmed destroy. That one does
// redirect, and must: the operator was verified, the gate admitted them, and
// what they are told is that nothing was torn down (FR-029). FR-025 is about a
// caller this daemon would not act for at all, which is what every row below is.
//
// The last block is the non-vacuity and it is not decoration: every assertion
// above is satisfied by a daemon that refuses everything and redirects nowhere.
// It makes the opposite claim on the same routes through the same fixture —
// a request nothing refuses *is* answered with a 303 — which is what makes the
// rows above an exclusion rather than a description of a door that never works.
func TestRefusalIsNotARedirect(t *testing.T) {
	t.Parallel()

	for _, route := range mutatingRoutes() {
		t.Run(route.name, func(t *testing.T) {
			t.Parallel()

			for _, c := range refusalShapes() {
				if c.namesASession && !route.namesASession {
					continue
				}

				t.Run(c.name, func(t *testing.T) {
					t.Parallel()

					r := newRefuser(t)
					w := r.send(t, route, c.vary(t, r, r.wellFormed(t, r.mine(t).ID)))

					// The claim, stated on its own. The status is what a browser acts
					// on and the Location is what it acts on it with, so both are
					// asserted: a 303 with no Location strands the caller, and a
					// Location on a 403 is a header the next well-meaning edit turns
					// into a redirect.
					if w.Code >= http.StatusMultipleChoices && w.Code < http.StatusBadRequest {
						t.Fatalf("the refusal was answered %d to %q; a refusal is never a redirect (FR-025) — "+
							"sending an unauthorised caller somewhere tells them their request was processed",
							w.Code, w.Header().Get(headerLocation))
					}
					if got := w.Header().Get(headerLocation); got != "" {
						t.Errorf("the refusal carried %s: %q; want no such header", headerLocation, got)
					}

					// And it is still the uniform answer it was before T014. A refusal
					// that stopped redirecting by answering something new of its own
					// would satisfy both claims above and lose what FR-004 and FR-017
					// are for.
					if w.Code != c.status {
						t.Fatalf("status = %d (%s); want %d — the uniform answer this refusal had before the redirect",
							w.Code, w.Body.String(), c.status)
					}
					if got := w.Body.String(); got != c.body {
						t.Errorf("body\n%s\nwant the uniform answer\n%s", got, c.body)
					}
				})
			}

			t.Run("a request nothing refuses is answered with a redirect", func(t *testing.T) {
				t.Parallel()

				r := newRefuser(t)
				wantOutcome(t, r.send(t, route, r.wellFormed(t, r.mine(t).ID)), route.succeeds)
			})
		})
	}
}

// --- US3: every action with no script at all (T016) -------------------------
//
// SC-006, per action, and the claim US3 exists for. T014 made every action
// answer a 303 and T015 fixed what deliberately does not; neither follows the
// redirect, and the operator this story is about is the one who does. Without a
// script that is the whole interaction: a form post, and the GET the browser
// makes of the Location it was handed. Milestone 3 answered the first half
// correctly and left that operator on a bare `<p>` with no page around it, which
// is a defect only a test that makes the second request can see.
//
// No audit assertion in this section, deliberately, and not because there is
// nothing to say: the four per-route tests above each count their action's
// record while it is still the only one. Following a redirect is a second
// request and leaves a dashboard.view of its own, so a count taken here would
// either have to run before the page is opened — where it duplicates theirs — or
// be a count of two requests, which is not what FR-041 claims about either.

// withoutScript is every registered action route driven the way a browser with
// no script drives them: the post refuser already builds, and then the GET of
// whatever Location came back.
//
// It embeds that fixture rather than replacing it because what varies here is
// still the route — the thing refuser and mutatingRoutes exist for — and a
// fixture of this section's own would be a second way to build the one request
// these doors take. What it adds is the second half of the interaction, which is
// the half US3 is about.
type withoutScript struct{ *refuser }

// wholePage is what tells the page an action returns to from the fragment
// milestone 3 answered with.
//
// The first two are the document itself, which a fragment has none of. The third
// is the fleet's own shell, so a page that is *some* whole page — the not-found,
// a refusal — does not pass. The fourth is the one that makes "usable" more than
// "renders": the create form is on it, carrying a freshly minted token, so the
// operator who just acted can act again without going anywhere. That is the
// difference between landing somewhere sensible and landing somewhere.
var wholePage = []string{
	"<!doctype html>",
	`<html lang="en">`,
	`<main class="shell"`,
	`<form class="create-form"`,
}

// callerSentence is what a caller writes into a form when the thing they are
// attacking is the page rather than the host: a claim about this daemon, wrapped
// in markup, addressed to the one operator who trusts this page about their own
// machine.
const callerSentence = `<em>the daemon says your key is exposed</em>`

// callerFragments is every shape of it a page must not carry. The escaped forms
// are named because escaping is not the claim — html/template would render a
// reflected value harmlessly, and a harmless lie on this page is still a lie
// this daemon told.
var callerFragments = []string{
	callerSentence,
	"the daemon says your key is exposed",
	"<em",
	"&lt;em",
}

// follows is the browser doing what the 303 told it to, with nothing running on
// the page: it re-issues the Location as a GET and reads what comes back.
//
// The address comes off the response's own header rather than being rebuilt
// here. A URL this test composed would prove that the fleet renders a code the
// test chose; what is under test is that it renders the one the daemon wrote.
//
// It carries the edge's assertion and no Sec-Fetch-Site, which is what a
// navigation is: the cross-site check guards the actions, and a GET of the fleet
// that demanded it would refuse every operator who followed a link to it.
func (s withoutScript) follows(t *testing.T, w *httptest.ResponseRecorder) string {
	t.Helper()

	to := w.Header().Get(headerLocation)
	if to == "" {
		t.Fatalf("the action answered %d with no %s; a browser with no script has nowhere to go from here",
			w.Code, headerLocation)
	}

	r := httptest.NewRequest(http.MethodGet, to, nil)
	r.Header.Set(headerAccessAssertion, s.keys.mint(t, s.keys.claims()))

	landed := httptest.NewRecorder()
	s.ServeHTTP(landed, r)
	if landed.Code != http.StatusOK {
		t.Fatalf("following %s to %q = %d (%s); want %d — an action that redirects somewhere unusable is the defect US3 is about",
			headerLocation, to, landed.Code, landed.Body.String(), http.StatusOK)
	}
	return landed.Body.String()
}

// states asserts the sentence the page an operator landed on is carrying.
func statesOutcome(t *testing.T, page, want string) {
	t.Helper()

	if got := bannerOn(page); got != want {
		t.Errorf("the page states\n%q\nwant outcome.go's own\n%q\nfull page:\n%s", got, want, page)
	}
}

// carriesNothingOfTheCallers is FR-022 read at the far end: whatever the caller
// wrote, none of it is on the page they were sent to.
func carriesNothingOfTheCallers(t *testing.T, page string) {
	t.Helper()

	for _, fragment := range callerFragments {
		if strings.Contains(page, fragment) {
			t.Errorf("the page carries %q, which the caller wrote — an outcome is chosen from a fixed set by the daemon and never built from what arrived (FR-022):\n%s",
				fragment, page)
		}
	}
}

// TestEveryActionIsUsableWithoutScript is SC-006 and FR-021 together, for each
// of create, destroy, rename, compact and the mode toggle: the action answers
// 303, and the page the browser then asks for is a whole, usable fleet stating
// what happened in words from outcome.go's fixed vocabulary.
//
// The toggle joined the sweep with the transition behind it (T020). It was named
// for four while the fifth route could only refuse — a row here needs a success
// to drive, and answering one this daemon had not performed is the defect that
// route's own tests are about.
//
// **Must fail when** an outcome is built from caller-supplied text (FR-022). It
// fails three ways, because there are three ways to write that defect. A handler
// that redirected with a field it reads — `outcome(r.PostForm.Get(fieldName))` is
// the whole of it — sends a code no vocabulary spells, and the fleet renders no
// banner at all: the first case sees a page that says nothing happened. A handler
// that let the caller name the code outright is the second case, which sends a
// *real* code the route did not choose and would land an operator on an alarm
// about a teardown nobody attempted. And a refusal that quoted the text it
// refused is the third, which is the one a well-meaning hand actually writes —
// "that name is not usable" reads better with the name in it, and it is caller
// text on its way back out through a page (FR-042).
//
// It must also fail when the answer stops being a page. Following the redirect is
// what makes that visible: milestone 3's four routes answered exactly one request
// each, correctly, and the operator with no script was left on a fragment — so a
// test that stopped at the 303 would have passed against the defect this story
// was written for.
func TestEveryActionIsUsableWithoutScript(t *testing.T) {
	t.Parallel()

	for _, route := range mutatingRoutes() {
		t.Run(route.name, func(t *testing.T) {
			t.Parallel()

			t.Run("it lands the browser on a whole page that says what happened", func(t *testing.T) {
				t.Parallel()

				s := withoutScript{newRefuser(t)}
				w := s.send(t, route, s.wellFormed(t, s.mine(t).ID))

				wantOutcome(t, w, route.succeeds)

				page := s.follows(t, w)
				for _, mark := range wholePage {
					if !strings.Contains(page, mark) {
						t.Errorf("the page carries no %s, so this is not a fleet an operator can act from again:\n%s", mark, page)
					}
				}
				statesOutcome(t, page, route.states)
			})

			t.Run("nothing the caller sent decides what the page says", func(t *testing.T) {
				t.Parallel()

				s := withoutScript{newRefuser(t)}
				a := s.wellFormed(t, s.mine(t).ID)
				// Three fields no route reads, and the first is the attack proper: a
				// code that really is in the vocabulary, so a handler taking the
				// outcome from the form would not merely render nothing — it would
				// tell this operator a shell may have survived a destroy they never
				// asked for.
				a.smuggled = url.Values{
					queryOutcome: []string{string(outcomeTeardownUnverified)},
					"message":    []string{callerSentence},
					"banner":     []string{callerSentence},
				}
				w := s.send(t, route, a)

				wantOutcome(t, w, route.succeeds)

				page := s.follows(t, w)
				statesOutcome(t, page, route.states)
				if strings.Contains(page, outcomeAlarmClass) {
					t.Errorf("a form field named the alarming outcome and the page rendered it:\n%s", page)
				}
				carriesNothingOfTheCallers(t, page)
			})

			// The compact reads no field, so there is nothing on it to fill in —
			// see mutatingRoute.chosen. Skipping it is the absence of a case rather
			// than a case that does not apply.
			if route.chosen.field == "" {
				return
			}

			t.Run("a refusal the caller's own words caused is stated in the daemon's", func(t *testing.T) {
				t.Parallel()

				s := withoutScript{newRefuser(t)}
				a := s.wellFormed(t, s.mine(t).ID)
				a.smuggled = url.Values{route.chosen.field: []string{callerSentence}}
				w := s.send(t, route, a)

				wantOutcome(t, w, route.chosen.yields)

				page := s.follows(t, w)
				statesOutcome(t, page, route.chosen.states)
				carriesNothingOfTheCallers(t, page)
			})
		})
	}
}

// --- US4: the mode toggle (T019) ---------------------------------------------
//
// The fifth action route, and the only one that reads a value naming what a
// session runs. Everything below drives the **registered** route through
// Server.ServeHTTP, for the reason the destroy's cases do: a toggle wired with
// handleBrowser instead of handleAction would leave the cross-site defence
// absent from the one route that can restart an unsandboxed assistant under a
// different command, and every case that drove the handler directly would still
// be green.

// The toggle's own answers, and the action its records carry. Each is written
// out here rather than read from the constant the code writes, which is the
// discipline every literal in this file follows: a test asserting against
// outcomeBadMode would prove only that the code agrees with itself, and
// contracts/session-mode.md is what fixes `session.mode`.
const (
	wantBadModeOutcome         = outcome("bad-mode")
	wantModeUnconfirmedOutcome = outcome("mode-unconfirmed")
	wantModeFailedOutcome      = outcome("mode-failed")
	wantModeChangedOutcome     = outcome("mode-changed")

	wantModeAction = "session.mode"
)

// The two words this route accepts, spelled as a form carries them.
const (
	modeLocal  = "local"
	modeRemote = "remote"
)

// plantedStartCommand is the command *name* every session below is started
// under, and the field the mode is derived from (T018).
//
// Every refusal here asserts the record still carries it afterwards, and that is
// what "the mode is unchanged" means on a record: there is deliberately no mode
// field to read, so the name is the whole of the state a toggle can move.
const plantedStartCommand = "rc"

// offersRemoteControl configures this daemon the way an operator who wants the
// toggle configures theirs: a named set with something other than the default in
// it, and which of those names means remote (#58).
//
// It is a method on the server fixture rather than on either of the two that use
// it, because both need it for the same reason — a transition resolves the mode
// against this configuration, so a manager nobody told has no remote mode to
// move to and refuses every toggle. Nothing here runs: the fake executes
// nothing, so the command lines are spelled to be recognisable in an argv.
// The config is told as well as the manager, because a production daemon tells
// both from one value (server.go) and the two answer different halves of the same
// question: the manager decides what a mode *runs*, and the config is what cardOf
// derives the word a card *says* from. A fixture that set only one would be a
// daemon that cannot exist, and it would hide exactly the disagreement between
// them that a create asserting on both is there to catch.
func (s *testServer) offersRemoteControl() {
	s.fixture.mgr.SetStartCommands(config.NewStartCommands(map[string]string{
		config.DefaultStartCommandName: "local-command",
		plantedStartCommand:            "remote-command",
	}))
	s.fixture.mgr.SetRemoteControlCommand(plantedStartCommand)
	s.cfg.RemoteControlCommand = plantedStartCommand
}

// toggler is the registered mode route with everything behind it readable: the
// store, the fake host, and the trail.
type toggler struct {
	*testServer
	keys *keyServer
}

func newToggler(t *testing.T) *toggler {
	t.Helper()

	keys := newKeyServer(t)
	return &toggler{testServer: newAuditedServerWith(t, keys.validator(t)), keys: keys}
}

// live plants a running session of this operator's own, started under a
// configured command name.
func (g *toggler) live(t *testing.T) session.Session {
	t.Helper()

	planted, _ := g.fixture.plant(t, session.Session{
		Name:         "to-be-toggled",
		WorkDir:      g.fixture.repo,
		StartCommand: plantedStartCommand,
	})
	return planted
}

// asked is the form a rendered control submits: the page token, the confirming
// step FR-029 requires, and the mode.
func (g *toggler) asked(t *testing.T, mode string) url.Values {
	t.Helper()

	form := url.Values{}
	form.Set(fieldPageToken, mustMint(t, g.pageKey, testOperatorEmail, testTime))
	form.Set(fieldConfirm, confirmYes)
	form.Set(fieldMode, mode)
	return form
}

// post submits one form at the mode route, as the browser this daemon rendered
// the page for.
func (g *toggler) post(t *testing.T, id string, form url.Values) *httptest.ResponseRecorder {
	t.Helper()

	return g.send(t, "/dashboard/sessions/"+id+"/mode", secFetchSiteSameOrigin, form)
}

// send is post with the browser's own account of where the request came from
// chosen by the caller — the one thing the cross-site cases have to vary and a
// rendered control never does.
//
// post is expressed through it rather than beside it so that a varied case
// differs from the ordinary one in exactly the field it means to vary. An
// initiator of absent sends no Sec-Fetch-Site header at all, which is a
// different shape from sending an empty one and is its own cause on a route that
// changes something.
func (g *toggler) send(t *testing.T, path, site string, form url.Values) *httptest.ResponseRecorder {
	t.Helper()

	r := httptest.NewRequest(http.MethodPost, path, strings.NewReader(form.Encode()))
	r.Header.Set(headerContentType, contentTypeForm)
	r.Header.Set(headerAccessAssertion, g.keys.mint(t, g.keys.claims()))
	if site != absent {
		r.Header.Set(headerSecFetchSite, site)
	}

	w := httptest.NewRecorder()
	g.ServeHTTP(w, r)
	return w
}

// runs is the command name the daemon's own record still carries, read back out
// of the store rather than off the copy the test planted.
func (g *toggler) runs(t *testing.T, s session.Session) string {
	t.Helper()

	live, err := g.fixture.store.Get(s.ID, auth.CallerOperator)
	if err != nil {
		t.Fatalf("read the store for session %s: %v", s.ID, err)
	}
	return live.StartCommand
}

// untouched is the assertion every refusal below shares: the record still names
// the command it named, and nothing reached the host at all.
//
// The second half is what the first cannot see. A toggle that restarted the pane
// and then failed to write the record would leave the store looking exactly like
// a request refused before it ran, and the difference between those two is a
// process the operator did not ask for, running unsandboxed.
func (g *toggler) untouched(t *testing.T, s session.Session) {
	t.Helper()

	if got := g.runs(t, s); got != plantedStartCommand {
		t.Errorf("the record runs %q; want %q — a refused toggle changes nothing", got, plantedStartCommand)
	}
	if calls := g.fixture.tmux.Calls(); len(calls) != 0 {
		t.Errorf("a refused toggle reached the host %d times (%v); want 0 — nothing is restarted by a request this daemon would not carry out",
			len(calls), calls)
	}
}

// answered is the Location and the body together, which is the whole of what a
// caller gets back from an action: a 303 answering a POST carries no body, so
// anything of theirs that survived is in the one header that varies.
//
// The security headers are deliberately not searched. `script-src` contains the
// two letters of a command name the table below submits, and a test that read
// them would fail on the content-security policy rather than on a disclosure.
func answered(w *httptest.ResponseRecorder) string {
	return w.Header().Get(headerLocation) + "\n" + w.Body.String()
}

// TestArbitraryModeValueRefused is FR-030 in the direction that matters most: a
// `mode` field naming anything other than the two words this daemon has is
// refused, uniformly, and nothing runs.
//
// **Must fail when** the value is passed through as a command. The host-call
// count is the direct claim — a handler that took the field as something to run
// would have reached tmux — and the answer and the trail are the two places the
// value could otherwise surface.
//
// The table is the shapes a hostile or careless value arrives in. The first is
// the attack proper, spelled as contracts/session-mode.md spells it. The second
// is subtler and is the one a well-meaning edit produces: `rc` is a real
// start-command *name* on this daemon, and a route accepting names rather than
// modes would be the browser choosing which configured command to run — a wider
// surface than the two-word toggle FR-030 permits, and one nothing here would
// catch if the check were a lookup instead of a comparison. The rest are
// near-misses — case, whitespace, both modes at once — because the check is a
// comparison rather than a parse, and each is what a hand-built request or a
// helpful client sends.
//
// The last stanza is the non-vacuity: a real mode is answered differently.
// Without it every case here is satisfied by a route that refuses everyone.
func TestArbitraryModeValueRefused(t *testing.T) {
	t.Parallel()

	cases := map[string]string{
		"a command line":                   "claude --dangerously-skip-permissions",
		"a configured start-command name":  plantedStartCommand,
		"the default start command's name": "default",
		"the field is absent":              absent,
		"the field is empty":               "",
		"the value is not lowercase":       "Local",
		"the value carries whitespace":     " remote ",
		"both modes at once":               "local,remote",
		"a mode with something after it":   "local; claude --dangerously-skip-permissions",
		"a path to something on this host": "/usr/local/bin/claude",
	}

	for name, value := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			g := newToggler(t)
			live := g.live(t)

			form := g.asked(t, modeLocal)
			if value == absent {
				form.Del(fieldMode)
			} else {
				form.Set(fieldMode, value)
			}

			w := g.post(t, live.ID, form)

			wantOutcome(t, w, wantBadModeOutcome)
			g.untouched(t, live)

			// Only the values long enough that a match is a disclosure rather than
			// a coincidence. Two of the entries above are shorter than the fixed
			// strings a redirect and a reason are built from, and asserting on
			// those would be asserting about the alphabet.
			if len(value) >= 4 {
				if got := answered(w); strings.Contains(got, value) {
					t.Errorf("the answer carried the submitted value back:\n%s", got)
				}
				if got := g.sink.String(); strings.Contains(got, value) {
					t.Errorf("the trail carried the submitted value:\n%s", got)
				}
			}

			rec := g.only(t)
			if got, want := rec["action"], wantModeAction; got != want {
				t.Errorf("action = %v; want %v — the gate admitted this request; the value is what refused it", got, want)
			}
			if got, want := rec["decision"], string(audit.Deny); got != want {
				t.Errorf("decision = %v; want %v", got, want)
			}
			if got, want := rec["reason"], errModeNotOffered.Error(); got != want {
				t.Errorf("reason = %v; want %v — the reason names the check and never the value (FR-042)", got, want)
			}
		})
	}

	offered := newToggler(t)
	live := offered.live(t)
	if w := offered.post(t, live.ID, offered.asked(t, modeRemote)); w.Header().Get(headerLocation) == "/?outcome="+string(wantBadModeOutcome) {
		t.Fatalf("a toggle naming %q was refused as a mode this daemon does not offer; every case above is satisfied by a route that refuses everyone", modeRemote)
	}
}

// TestToggleRequiresConfirm is FR-029 on the toggle: a mode change without the
// confirming step is refused, and the session goes on running what it was
// running.
//
// **Must fail when** the confirm check is removed — every case below then reaches
// the transition and answers something other than the unconfirmed outcome.
//
// The near-misses are the destroy's, for the destroy's reason: `on`, `true` and
// an upper-case `YES` are what a stray checkbox, a hand-built request and a
// helpful client produce, and none of them is the deliberate act FR-029 asks
// for. What a toggle ends is the process the operator is watching, and the
// conversation inside it survives only because the transition asks for it to —
// so a mode change nobody meant to make is not a refusal worth softening.
func TestToggleRequiresConfirm(t *testing.T) {
	t.Parallel()

	cases := map[string]string{
		"the field is absent":        absent,
		"the field is empty":         "",
		"the operator said no":       "no",
		"a checkbox said on":         "on",
		"a client said true":         "true",
		"the value is not lowercase": "YES",
	}

	for name, value := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			g := newToggler(t)
			live := g.live(t)

			form := g.asked(t, modeRemote)
			if value == absent {
				form.Del(fieldConfirm)
			} else {
				form.Set(fieldConfirm, value)
			}

			w := g.post(t, live.ID, form)

			wantOutcome(t, w, wantModeUnconfirmedOutcome)
			g.untouched(t, live)

			rec := g.only(t)
			if got, want := rec["action"], wantModeAction; got != want {
				t.Errorf("action = %v; want %v — the gate admitted this request; the confirming step is what refused it", got, want)
			}
			if got, want := rec["decision"], string(audit.Deny); got != want {
				t.Errorf("decision = %v; want %v", got, want)
			}
			if got, want := rec["reason"], errModeUnconfirmed.Error(); got != want {
				t.Errorf("reason = %v; want %v", got, want)
			}
		})
	}

	confirmed := newToggler(t)
	live := confirmed.live(t)
	if w := confirmed.post(t, live.ID, confirmed.asked(t, modeRemote)); w.Header().Get(headerLocation) == "/?outcome="+string(wantModeUnconfirmedOutcome) {
		t.Fatal("a confirmed toggle was answered as unconfirmed; every refusal above is satisfied by a route that refuses everyone")
	}
}

// TestToggleEmitsExactlyOneAuditRecord is FR-041 on the fifth route: one
// request, one record, under the action contracts/session-mode.md names.
//
// **Must fail when** the transition emits records of its own. A toggle ends one
// process and starts another, and the shape this is written against is the
// obvious one — a destroy-like record for the stop and a create-like record for
// the start — which would leave an operator counting two events for a thing they
// did once, on the route where "how many times did this happen" matters most.
//
// Every shape a request can take is driven rather than only the accepted one,
// because the count is what FR-041 claims about *requests* and not about
// successes: a refusal recorded twice is the same defect. The decision is
// deliberately not asserted here — what this holds is the count and the name,
// and both stay true whichever way the transition behind the door goes.
func TestToggleEmitsExactlyOneAuditRecord(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		vary       func(form url.Values)
		namesTheID bool
	}{
		"a toggle this daemon admits": {
			vary:       func(url.Values) {},
			namesTheID: true,
		},
		"a toggle naming no mode this daemon has": {
			vary: func(form url.Values) { form.Set(fieldMode, "claude --dangerously-skip-permissions") },
		},
		"a toggle nobody confirmed": {
			vary: func(form url.Values) { form.Del(fieldConfirm) },
		},
	}

	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			g := newToggler(t)
			live := g.live(t)

			form := g.asked(t, modeRemote)
			c.vary(form)
			g.post(t, live.ID, form)

			rec := g.only(t)
			if got, want := rec["action"], wantModeAction; got != want {
				t.Errorf("action = %v; want %v", got, want)
			}
			if !c.namesTheID {
				return
			}
			// Stamped from the daemon's own record, and only once the ownership
			// check has matched it to this operator: it is what makes a toggle
			// findable in the trail under the session it was against.
			if got := rec["session_id"]; got != live.ID {
				t.Errorf("session_id = %v; want %q", got, live.ID)
			}
		})
	}
}

// TestToggleCrossSiteBothHalves is AR-005 on the newest action route: each half
// of the cross-site defence refuses on its own, with the other satisfied.
//
// **Must fail when** the route is registered with handleBrowser rather than
// handleAction, and when a case satisfies neither half — which is why each one
// below sends a *valid* page token or a *real* same-origin header for the half it
// is not varying. Nothing here disables a check: a build tag or a flag that
// turned one off is the exact defect this gate exists to prevent, and it would
// leave every case passing against a daemon with no defence at all.
//
// The absent header is a case of its own rather than a variant of the wrong one.
// A stream tolerates it — non-browser clients send no fetch metadata — and a
// route that changes something does not, because the only legitimate caller here
// is a form this daemon rendered, and a browser sends the header on every one.
//
// The record is asserted as dashboard.reject rather than session.mode, which is
// the distinction research R5 draws: an identity that got in and *then* failed
// the cross-site check is a more alarming event than one that never got in, and
// an operator counting toggles must not be counting these with them.
func TestToggleCrossSiteBothHalves(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		site string
		vary func(form url.Values)
	}{
		"the form carried no page token": {
			site: secFetchSiteSameOrigin,
			vary: func(form url.Values) { form.Del(fieldPageToken) },
		},
		"the form carried a page token this daemon never minted": {
			site: secFetchSiteSameOrigin,
			vary: func(form url.Values) { form.Set(fieldPageToken, "not-a-token-this-daemon-minted") },
		},
		"the browser said the request came from another site": {
			site: "cross-site",
			vary: func(url.Values) {},
		},
		"the browser said nothing about where the request came from": {
			site: absent,
			vary: func(url.Values) {},
		},
	}

	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			g := newToggler(t)
			live := g.live(t)

			form := g.asked(t, modeRemote)
			c.vary(form)

			w := g.send(t, "/dashboard/sessions/"+live.ID+"/mode", c.site, form)

			if w.Code != wantActionStatus {
				t.Fatalf("status = %d (%s); want %d — the mode route is not behind the gate",
					w.Code, w.Body.String(), wantActionStatus)
			}
			if got := w.Body.String(); got != wantActionBody {
				t.Errorf("body\n%s\nwant the gate's uniform refusal\n%s", got, wantActionBody)
			}
			g.untouched(t, live)

			if got, want := g.only(t)["action"], string(audit.ActionDashboardReject); got != want {
				t.Errorf("action = %v; want %v", got, want)
			}
		})
	}

	admitted := newToggler(t)
	live := admitted.live(t)
	if w := admitted.post(t, live.ID, admitted.asked(t, modeRemote)); w.Code == wantActionStatus {
		t.Fatalf("a toggle satisfying both halves was answered %d; every case above is satisfied by a route that refuses everyone", w.Code)
	}
}

// TestToggleAgainstASessionThatIsNotTheOperatorsIsUniform is the ownership half
// of the security checklist on the newest route: a toggle naming a session this
// operator may not act on is the uniform not-found, whichever of the three
// causes applied (FR-017, SC-009).
//
// **Must fail when** the refusal becomes a redirect. FR-025 keeps a refusal off
// the outcome path — sending an unauthorised caller to a page is telling them
// their request was processed — and the three causes are told apart on the
// record alone, which is what stops this route being an oracle for which
// identifiers on this host are real.
//
// There is deliberately no mutation that removes the ownership check here,
// because there is nothing to remove: Manager.View cannot be called without an
// owner, so a handler that skipped the check would have to reach past the
// manager into the store. What this holds is the answer's shape.
func TestToggleAgainstASessionThatIsNotTheOperatorsIsUniform(t *testing.T) {
	t.Parallel()

	// Not on the allowlist and not this operator's owner: a second operator whose
	// sessions this one must not be able to detect the existence of.
	const stranger auth.CallerID = "a-second-operator"

	cases := []struct {
		name   string
		target func(t *testing.T, g *toggler) session.Session
		reason error
	}{
		{
			name: "an identifier no session on this host ever had",
			target: func(*testing.T, *toggler) session.Session {
				return session.Session{ID: strings.Repeat("d", session.IDLen)}
			},
			reason: session.ErrSessionNotFound,
		},
		{
			name: "a session another operator owns",
			target: func(t *testing.T, g *toggler) session.Session {
				t.Helper()
				theirs, _ := g.fixture.plant(t, session.Session{
					Owner: stranger, Name: "not-this-operators", WorkDir: g.fixture.repo,
					StartCommand: plantedStartCommand,
				})
				return theirs
			},
			reason: session.ErrSessionNotFound,
		},
		{
			name: "a session of the operator's own that is no longer there",
			target: func(t *testing.T, g *toggler) session.Session {
				t.Helper()
				gone, _ := g.fixture.plant(t, session.Session{
					Name: "already-gone", WorkDir: g.fixture.repo, State: session.StateDead,
					StartCommand: plantedStartCommand,
				})
				return gone
			},
			reason: session.ErrSessionDead,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			g := newToggler(t)
			target := tc.target(t, g)

			w := g.post(t, target.ID, g.asked(t, modeRemote))

			if w.Code != wantNotFoundStatus {
				t.Fatalf("status = %d (%s); want %d", w.Code, w.Body.String(), wantNotFoundStatus)
			}
			if got := w.Body.String(); got != wantNotFoundBody {
				t.Errorf("body\n%s\nwant\n%s", got, wantNotFoundBody)
			}
			if calls := g.fixture.tmux.Calls(); len(calls) != 0 {
				t.Errorf("the host was reached %d times (%v) for a session that is not this operator's to act on", len(calls), calls)
			}

			rec := g.only(t)
			if got, want := rec["reason"], tc.reason.Error(); got != want {
				t.Errorf("reason = %v; want %v — the record is the only place the cause is named", got, want)
			}
			if strings.Contains(w.Body.String(), tc.reason.Error()) {
				t.Errorf("the response quotes the reason back:\n%s", w.Body.String())
			}
		})
	}
}

// TestToggleSaysSoWhenItCannotAct is the honest half of the route, now that the
// transition behind it exists (T020): a toggle this daemon admits but cannot
// carry out is told it did not happen, and the two reasons it cannot are told
// apart in the trail.
//
// **Must fail when** this route answers a success it did not perform. An
// operator told their session is now remote-controlled, on a daemon that has no
// remote-control command to run, would be reading a card describing a session
// that is still local — the one claim this route must never get wrong, and it is
// the reason both arms below assert the record as well as the answer.
//
// Neither refusal is a fact about the session. One says this daemon configures
// no such command and the other says the session is already there, and both are
// answers an operator can read off their own settings page — which is what makes
// them safe to distinguish in the journal while the page says the same sentence
// for each.
func TestToggleSaysSoWhenItCannotAct(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		// configure is what this daemon was told about modes. The zero fixture is
		// a daemon that configures no remote control at all, which is the first
		// case and needs no setup.
		configure func(g *toggler)
		mode      string
		reason    error
	}{
		"remote, on a daemon configuring no remote-control command": {
			configure: func(*toggler) {},
			mode:      modeRemote,
			reason:    errModeUnavailable,
		},
		"the mode the session is already in": {
			configure: (*toggler).offersRemoteControl,
			// The planted session runs plantedStartCommand, which is the name this
			// daemon has just been told means remote.
			mode:   modeRemote,
			reason: errModeUnchanged,
		},
	}

	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			g := newToggler(t)
			c.configure(g)
			live := g.live(t)

			w := g.post(t, live.ID, g.asked(t, c.mode))

			wantOutcome(t, w, wantModeFailedOutcome)
			g.untouched(t, live)

			rec := g.only(t)
			if got, want := rec["decision"], string(audit.Deny); got != want {
				t.Errorf("decision = %v; want %v — a mode this daemon did not change is not an allowed action", got, want)
			}
			if got, want := rec["reason"], c.reason.Error(); got != want {
				t.Errorf("reason = %v; want %v", got, want)
			}
		})
	}
}

// TestToggleRestartsTheSessionUnderTheOtherCommand is the route's success, and
// the non-vacuity every refusal above rests on: a daemon configured for both
// modes carries the transition out, says so in the fixed vocabulary, and leaves
// the record naming the command the session is now running.
//
// **Must fail when** the route answers `mode-changed` without the transition
// having happened. The host calls are the direct claim — the two sends and the
// option write Manager.SetMode is made of — because an answer alone is exactly
// what a stub produces, and this daemon's whole job on this route is that the
// word on the card and the process in the pane agree.
func TestToggleRestartsTheSessionUnderTheOtherCommand(t *testing.T) {
	t.Parallel()

	g := newToggler(t)
	g.offersRemoteControl()
	live := g.live(t)

	w := g.post(t, live.ID, g.asked(t, modeLocal))

	wantOutcome(t, w, wantModeChangedOutcome)

	if got := g.runs(t, live); got != config.DefaultStartCommandName {
		t.Errorf("the record runs %q; want %q — the mode a card derives comes from this name", got, config.DefaultStartCommandName)
	}
	// The pane was restarted rather than the record merely relabelled. Asserted as
	// a count of what reached the host and the flag on the line that was typed,
	// which is the whole of what the daemon claims to have done.
	calls := g.fixture.tmux.Calls()
	if len(calls) == 0 {
		t.Fatal("the toggle reached the host 0 times; a mode the daemon only wrote down is a card that does not describe its session")
	}
	// What this fixture can show is that the toggle restarts *in place*. Whether
	// the restarted command resumes the session's conversation is asserted in
	// internal/session, where a conversation can be put on the record — these
	// stand-in start commands are not claude, so a session created under them is
	// given no conversation identifier at all (see claudeBinary).
	var typed bool
	for _, call := range calls {
		if call.Op == tmuxctl.OpSendKeys {
			typed = true
		}
		if call.Op == tmuxctl.OpNew || call.Op == tmuxctl.OpKill {
			t.Errorf("the toggle ran %s (%q); the session and its scrollback are the things it must not touch", call.Op, call.Argv)
		}
		for _, arg := range call.Argv {
			if strings.Contains(arg, "--continue") {
				t.Errorf("the toggle typed %q; --continue left this daemon in spec 013", arg)
			}
		}
	}
	if !typed {
		t.Errorf("nothing was typed into the pane, so the toggle changed no running process: %v", calls)
	}

	rec := g.only(t)
	if got, want := rec["decision"], string(audit.Allow); got != want {
		t.Errorf("decision = %v; want %v", got, want)
	}
	if got := rec["session_id"]; got != live.ID {
		t.Errorf("session_id = %v; want %q", got, live.ID)
	}
}
