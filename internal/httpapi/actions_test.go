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
	"errors"
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

// The destroy's own answers. Each is quoted here rather than read from the
// constant the code writes, for the reason the refusal above is: a test asserting
// against the variable proves only that the code agrees with itself.
const (
	wantDestroyedBody   = `<p class="card-outcome">Session destroyed. The host confirmed its window is gone.</p>`
	wantUnconfirmedBody = `<p class="card-outcome">This destroy was not confirmed, so nothing was torn down.</p>`
	// From contracts/actions.md, which fixes this one byte for byte.
	wantUnverifiedBody = `<p class="card-outcome">Teardown could not be verified. This session may still be running on the host.</p>`
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
// status on the second.
//
// The response is asserted as bytes rather than as "a 200", because the fragment
// is what replaces the card. A destroy that answered with an empty body would
// leave the operator looking at a card that quietly vanished, which FR-030 and
// FR-031 both forbid.
func TestDestroyTearsTheSessionDown(t *testing.T) {
	t.Parallel()

	d := newDestroyer(t)
	live := d.live(t)

	w := d.post(t, live.ID, d.confirmed(t))

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d (%s); want %d", w.Code, w.Body.String(), http.StatusOK)
	}
	if got := w.Body.String(); got != wantDestroyedBody {
		t.Errorf("body\n%s\nwant\n%s", got, wantDestroyedBody)
	}
	if got, want := w.Header().Get(headerContentType), wantActionContentType; got != want {
		t.Errorf("%s = %q; want %q", headerContentType, got, want)
	}
	if got, want := w.Header().Get(headerContentLength), strconv.Itoa(len(wantDestroyedBody)); got != want {
		t.Errorf("%s = %q; want %q", headerContentLength, got, want)
	}
	if got, want := w.Header().Get(headerContentTypeOptions), wantActionNosniff; got != want {
		t.Errorf("%s = %q; want %q — an action's answer carries the same headers a page does", headerContentTypeOptions, got, want)
	}

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
// 200 with the session gone.
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

			if w.Code != http.StatusBadRequest {
				t.Fatalf("status = %d (%s); want %d", w.Code, w.Body.String(), http.StatusBadRequest)
			}
			if got := w.Body.String(); got != wantUnconfirmedBody {
				t.Errorf("body\n%s\nwant\n%s", got, wantUnconfirmedBody)
			}

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
	if w := confirmed.post(t, live.ID, confirmed.confirmed(t)); w.Code != http.StatusOK {
		t.Fatalf("a confirmed destroy answered %d (%s); want %d — every refusal above is satisfied by a route that refuses everyone",
			w.Code, w.Body.String(), http.StatusOK)
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
// status, the body and the retained record all move at once, because
// Manager.Destroy only ever drops a record after confirmGone said the window is
// gone.
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

			if w.Code != http.StatusConflict {
				t.Fatalf("status = %d (%s); want %d — an unverified teardown is not a teardown",
					w.Code, w.Body.String(), http.StatusConflict)
			}
			if got := w.Body.String(); got != wantUnverifiedBody {
				t.Errorf("body\n%s\nwant\n%s", got, wantUnverifiedBody)
			}
			if got, want := w.Header().Get(headerContentType), wantActionContentType; got != want {
				t.Errorf("%s = %q; want %q", headerContentType, got, want)
			}

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
	if w.Code != http.StatusConflict {
		t.Fatalf("a destroy carrying a force field answered %d (%s); want %d — there is no force path (AR-004)",
			w.Code, w.Body.String(), http.StatusConflict)
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
	if w := served.post(t, mine.ID, served.confirmed(t)); w.Code != http.StatusOK {
		t.Fatalf("a destroy of this operator's own session answered %d (%s); want %d — "+
			"every refusal above is satisfied by a route that refuses everyone",
			w.Code, w.Body.String(), http.StatusOK)
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
	// 200: every row above is satisfied by a route that refuses everyone, and a
	// gate that refuses everyone is not a defence with two halves — it is a broken
	// dashboard that would ship looking secure.
	admitted := newDestroyer(t)
	live := admitted.live(t)

	w := admitted.post(t, live.ID, admitted.confirmed(t))
	if w.Code != http.StatusOK {
		t.Fatalf("a destroy satisfying both halves answered %d (%s); want %d",
			w.Code, w.Body.String(), http.StatusOK)
	}
	if recorded, running := admitted.standing(t, live); recorded || running {
		t.Errorf("a destroy satisfying both halves left the record %v and the window %v; want both gone", recorded, running)
	}
	if got, want := admitted.only(t)["action"], string(audit.ActionDashboardDestroy); got != want {
		t.Errorf("action = %v; want %v — an admitted action is recorded as the action, not as a rejection", got, want)
	}
}
