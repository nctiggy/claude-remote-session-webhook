package httpapi

// browser.go is the browser door. middleware.go is the API door, and the two
// are deliberately separate files for the reason contracts/dashboard.md gives
// them separate route tables: each door refuses only by the check that applies
// to it (FR-012), so a browser request is never refused for carrying no
// signature and an API request is never refused for carrying no identity.
//
// What runs here is layer 1 and nothing else — whichever of this daemon's three
// doors was configured: the Cloudflare Access assertion validated by
// internal/access, the dashboard password's signed cookie (password.go), or the
// closed door that admits nobody. Layers 2 and 3 are untouched: no dashboard
// route asks for a signature, and no API route consults either credential.

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"github.com/nctiggy/claude-remote-session-webhook/internal/access"
	"github.com/nctiggy/claude-remote-session-webhook/internal/audit"
)

// headerAccessAssertion is the only place a browser identity is read from
// (contracts/access-jwt.md).
//
// The browser also carries a CF_Authorization cookie, and this daemon never
// reads it: the cookie is the browser's credential *to the edge*, while this
// header is the edge's product *for the daemon*, written after the edge's own
// check. Two sources for one identity are two things free to disagree.
const headerAccessAssertion = "Cf-Access-Jwt-Assertion"

// contentTypeHTML is what the browser door answers with. A refusal is a page,
// not an API error — the caller on this door is a person looking at a browser.
const contentTypeHTML = "text/html; charset=utf-8"

// bodyBrowserRefused is layer 1's uniform denial, and there is exactly one
// spelling of it for the reason bodyUnauthorized has exactly one: FR-010 is
// satisfied by every failure producing the *identical* response, and a body
// built per call site is a body that eventually differs by a space.
//
// All eleven steps of the validation sequence and the keys-unobtainable case
// answer with these bytes. A missing header, a forged signature, an expired
// assertion, the wrong audience, and an address that is not on the allowlist are
// indistinguishable from outside — the difference between them is which forgery
// to try next, which is reconnaissance and not information a stranger is owed.
//
// It is deliberately not the API door's JSON: each door has one uniform refusal
// of its own, and FR-010's uniformity is *within* the door, which is where an
// attacker probing it lives. It references no stylesheet, no script, and no
// external origin, so it renders the same under the CSP as without one.
var bodyBrowserRefused = []byte(`<!doctype html>
<html lang="en">
<head><meta charset="utf-8"><title>crswd</title></head>
<body><h1>Not authorised</h1><p>This request was not accepted.</p></body>
</html>
`)

// layer1 is the browser door's whole dependency: something that can turn a
// request into a verified operator, or refuse.
//
// It is an interface because this daemon has three of them — closedDoor below,
// assertionDoor, and the password door (password.go) — chosen between at exactly
// one place, verifiedLayer1. A middleware written against one and *adapted* for
// the others would be a second authorisation path, and the second path is the
// one nobody reads.
//
// It takes the request rather than a credential because the doors read
// *different* credentials: Access forwards a signed assertion in a header, and
// the password door carries a signed cookie. A middleware that read one and
// handed it over would have to know which door it was holding — and "which door
// is this" is exactly the question verifiedLayer1 exists to answer once, at
// startup, instead of on every request.
type layer1 interface {
	Verify(r *http.Request) (*access.VerifiedOperator, error)
}

// assertionValidator is what internal/access ships: something that turns the
// assertion the edge forwarded into a verified operator.
//
// The signature is restated here because that package declares its own copy
// unexported, and asserts both of its implementations against it — *Validator,
// and the //go:build dev *Bypass that skips layer 1 on a laptop where there is
// no Cloudflare Access to sign anything.
type assertionValidator interface {
	Verify(ctx context.Context, assertion string) (*access.VerifiedOperator, error)
}

// assertionDoor is layer 1 when Cloudflare Access is the configured door: it
// reads the credential this door's caller presents and hands it to
// internal/access.
//
// Both of that package's implementations reach the middleware through this one
// type, which is what keeps them one door rather than one door and an adaptation
// of it. It holds the credential-reading and nothing else: no check moved in
// here, and none was left behind.
//
// The request's own context goes down with it, so a key-set fetch a browser
// abandoned is abandoned with it — and, because unobtainable keys refuse
// (FR-009), that ends as a denial rather than as an admission.
type assertionDoor struct {
	validator assertionValidator

	// door is what the settings page calls this layer 1 (M12/T006), and it is
	// here because this is the one door type that is two doors.
	//
	// Both of internal/access's implementations arrive through this wrapper, and
	// they are not the same news: one verifies a person against an identity
	// provider at the edge, and the other is the development bypass, which
	// verifies nobody. Nothing about a built door tells them apart — the
	// distinction was made at construction — so the constructor says which it
	// made rather than leaving the page to infer it. Inferring it from the Config
	// was tried and is wrong: WithAccessBypassActive lifts the *requirement* to
	// set the three Access values and not the ability to, so a developer running
	// the bypass against their ordinary configuration file has all three, and a
	// page reading them would report Cloudflare Access on the one build whose
	// layer 1 admits everybody without checking anything.
	//
	// It is inert. Verify never reads it, no caller supplies it, and the only
	// values it takes are constants in this package.
	door string
}

func (d assertionDoor) Verify(r *http.Request) (*access.VerifiedOperator, error) {
	if d.validator == nil {
		// Fail closed on the path that should not happen, for the reason
		// verifyBrowser refuses a nil door: a door with nothing behind it must
		// refuse rather than admit.
		return nil, errBrowserDoorUnwired
	}
	return d.validator.Verify(r.Context(), r.Header.Get(headerAccessAssertion))
}

// closedDoor is layer 1 on a daemon with no identity provider configured (#70).
//
// The Access variables used to be required, so the daemon refused to start
// without a Cloudflare account — which meant nobody but its author could run it.
// They are optional now, and this is what stands in their place: a layer 1 that
// admits nobody.
//
// A door that refuses everyone rather than no door at all, because "there is one
// layer 1 per server, always" is the property that keeps the browser middleware
// a single authorisation path. A nil validator and a special case in the
// middleware would be the second path, and the second path is the one nobody
// reads.
//
// The API is unaffected: it authenticates with a signature over the request, and
// has never had anything to do with layer 1.
type closedDoor struct{}

func (closedDoor) Verify(*http.Request) (*access.VerifiedOperator, error) {
	return nil, errNoIdentityProvider
}

// errNoIdentityProvider is server-side only, like every other layer-1 refusal.
// What a browser gets is the same uniform response an invalid assertion gets —
// an unconfigured daemon must not be distinguishable from one that simply said
// no, or the refusal itself becomes a configuration oracle.
var errNoIdentityProvider = errors.New("no identity provider is configured, so the dashboard admits nobody")

// The refusals this file authors itself, for the trail and never for a caller.
//
// Both are unreachable through newServer, which refuses a nil validator, and
// through internal/access, whose Verify returns an operator or an error and
// never neither. They exist because a door with nothing behind it must refuse
// rather than admit: fail-closed is only a property if it holds on the paths
// that should not happen.
var (
	errBrowserDoorUnwired = errors.New("the browser door was reached with no layer-1 validator behind it")

	errBrowserVerifiedNobody = errors.New("layer 1 named no operator and gave no reason")
)

// errScopeNoRoute is a request the contract has no route for — an unknown path,
// or a method a known path does not answer.
//
// It is milestone 1's own sentinel, moved here rather than reworded, because
// FR-013d changes the door that answers such a request and not what the trail
// says about it: an operator grepping their journal for this reason finds the
// same events after the move as before it (data-model.md).
var errScopeNoRoute = errors.New("no route in the contract matches this request")

// The not-found page's copy, supplied at the call site the way
// docs/components.md documents the empty state's parameters.
//
// The body says what a mistyped URL did rather than what to do about it. This
// door serves no navigation affordance — the empty state's action parameter is
// absent here for the reason it is absent on the fleet (FR-024a) — and an
// operator who has just been told a page does not exist is owed the one fact
// this daemon can state: asking for it touched nothing.
const (
	notFoundTitle = "No such page"
	notFoundBody  = "This daemon serves nothing at that address. Nothing on the host was touched by asking for it."
)

// notFoundView is the not-found page's composition: the header every page
// carries, and the canonical empty state carrying the explanation.
type notFoundView struct {
	// Operator is the identity layer 1 verified, passed straight to the header
	// component — the same pointer OperatorFrom returned, never a copy (FR-020,
	// FR-036).
	Operator *access.VerifiedOperator

	// Message is the empty state's parameters. The component is reused rather
	// than reproduced: an explanation where content would be is what it is for,
	// and a second one would be the second component docs/components.md forbids.
	Message emptyView
}

// notFound answers a request matching no route in either table (FR-013d).
//
// It is a page and a 404 together. Milestone 1 answered these through the API
// door with the uniform JSON not-found, which a browser renders as raw text from
// an interface its operator never used; the six operations and their responses
// are untouched by the move (FR-014), because no route in the contract reaches
// this handler.
//
// A stranger never sees it. The door in front runs first, so an unverified
// caller receives the same uniform refusal every other browser route gives them
// and learns nothing about which paths this daemon really serves — which is why
// serving a distinguishable page to a *verified* operator discloses nothing.
//
// The refusal is recorded before the page is rendered, so a page that then fails
// to render replaces the reason with its own rather than the other way round.
func (s *Server) notFound(w http.ResponseWriter, r *http.Request) {
	operator, ok := OperatorFrom(r.Context())
	if !ok {
		AuditFrom(r.Context()).Deny(errDashboardNoOperator.Error())
		s.refuseBrowser(w)
		return
	}

	// A refusal in the trail even though layer 1 admitted the caller: the request
	// was authenticated and still not served, which is what milestone 1 recorded
	// for the same event and what an operator counting unserved requests reads.
	AuditFrom(r.Context()).Deny(errScopeNoRoute.Error())

	s.renderNotFound(w, r, operator)
}

// renderNotFound writes this door's 404 — the page a request that reached no
// route and a request that reached no session are both answered with.
//
// One page because there is one answer. The single-session view gives an id that
// never existed, one the viewer does not own, and one whose session is already
// gone the same uniform 404 (FR-037b, SC-016), and the difference between those
// three and "no such route at all" is no more a caller's to have: each is a fact
// about what exists on this host. Which of them it really was is on the record
// the middleware emits, where the operator can read it.
//
// The copy says what asking did rather than what to do about it, and it is true
// of every one of those cases: this daemon serves nothing at that address, and
// nothing on the host was touched by asking.
func (s *Server) renderNotFound(w http.ResponseWriter, r *http.Request, operator *access.VerifiedOperator) {
	s.renderPage(w, r, http.StatusNotFound, "not-found", notFoundView{
		Operator: operator,
		Message:  emptyView{Title: notFoundTitle, Body: notFoundBody},
	})
}

// OperatorFrom reports the identity layer 1 verified for this request.
//
// It is the only way a dashboard handler learns who is looking, and what it
// returns came from the assertion the edge signed — never from a path, a query,
// a body, or a header a caller chose (FR-020, FR-036). A handler behind the door
// can rely on ok being true, and should treat a false as a refusal rather than
// as an anonymous viewer, which is why the boolean exists at all.
func OperatorFrom(ctx context.Context) (*access.VerifiedOperator, bool) {
	op, ok := ctx.Value(operatorContextKey).(*access.VerifiedOperator)
	return op, ok && op != nil
}

// authenticateBrowser is layer 1, applied to every dashboard route at the single
// point where one reaches the mux, so a dashboard route cannot be registered
// without it — the arrangement authenticate has on the other door, for the same
// reason.
//
// The audit emit is deferred rather than called at each return, which makes one
// record per request (FR-016) a property of the control flow instead of a rule
// two exit paths have to remember, and holds even if the handler panics.
//
// action is what a served request is recorded as — dashboard.view for a page,
// dashboard.asset for a stylesheet, route.unknown for a path nothing claims. A
// refusal is recorded as access.reject instead, kept apart from the API door's
// auth.reject because the two doors fail for unrelated reasons and an operator
// counting refusals of one must not be counting the other's as well.
func (s *Server) authenticateBrowser(action audit.Action, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Deny is the starting decision, so a path that never reaches a verdict
		// records a refusal rather than an approval.
		ra := &RequestAudit{rec: audit.Record{
			Action:   action,
			Decision: audit.Deny,
			Remote:   r.RemoteAddr,
		}}
		defer s.emit(ra)

		// Before layer 1 runs, so that a refusal carries them too (FR-026) and no
		// exit path below can leave without them. See setBrowserSecurityHeaders.
		setBrowserSecurityHeaders(w.Header())

		operator, err := s.verifyBrowser(r)
		if err != nil {
			ra.rec.Action = audit.ActionAccessReject
			// internal/access documents that every error it returns is a
			// sentinel authored in that package — none carries a byte of the
			// assertion, a key id, a claim value, or the refused address, all of
			// which are caller-authored text the trail may never hold (FR-035,
			// FR-042). That discipline is what makes the reason safe to record
			// here, and it is why the specific reason is recorded rather than
			// flattened to one string: an operator diagnosing a refusal needs to
			// know which check refused, and this is the only place it is kept.
			ra.rec.Reason = err.Error()
			s.refuseBrowser(w)
			return
		}

		// The server-derived owner, never the verified address. The two doors
		// resolve to one owner by construction (FR-037a), so a session created
		// through the API is a session the dashboard shows — and the address
		// itself is a claim value, which stays out of the trail.
		ra.rec.Caller = string(operator.Owner)
		ra.rec.Decision = audit.Allow

		if action == audit.ActionDashboardAsset {
			// The stylesheet and the script are contracts/dashboard.md's one
			// exemption from no-store: they hold no session data, and caching them
			// is what makes the page cheap. It is taken here rather than in
			// setBrowserSecurityHeaders because only an *admitted* request may take
			// it — a refusal that varied by which route was asked for would tell a
			// stranger which paths this daemon really serves, which is the
			// disclosure the uniform refusal exists to close.
			w.Header().Del(headerCacheControl)
		}

		ctx := context.WithValue(r.Context(), operatorContextKey, operator)
		ctx = context.WithValue(ctx, auditContextKey, ra)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// verifyBrowser is the whole of what this door asks, and it asks it of every
// request: there is no per-browser session and no "already checked" state, so a
// VerifiedOperator is derived per request and never stored (FR-036). Caching one
// would be the daemon's first cross-request browser state, and with it the
// expiry, invalidation and fixation questions this design exists not to have.
//
// That holds under the password door too, which is why its cookie is verified by
// recomputing it rather than by looking it up (password.go). A signed cookie is
// a credential the browser carries, not a session record this daemon keeps.
//
// Which credential is read is the door's own business and never this function's
// — see layer1. Whatever this daemon was configured with, exactly one question
// is asked here, of exactly one thing.
func (s *Server) verifyBrowser(r *http.Request) (*access.VerifiedOperator, error) {
	if s.browser == nil {
		return nil, errBrowserDoorUnwired
	}

	operator, err := s.browser.Verify(r)
	switch {
	case err != nil:
		return nil, err
	case operator == nil:
		return nil, errBrowserVerifiedNobody
	}
	return operator, nil
}

// refuseBrowser answers every layer-1 failure identically (FR-010, SC-001).
//
// It is one function for the reason writeUnauthorized is one, and it takes no
// reason argument on purpose: there is nothing a caller could pass that would be
// allowed to change a byte of what is written, so the parameter would only be an
// invitation. What was really refused is on the record the middleware emits.
func (s *Server) refuseBrowser(w http.ResponseWriter) {
	w.Header().Set(headerContentType, contentTypeHTML)
	w.WriteHeader(http.StatusUnauthorized)
	if _, err := w.Write(bodyBrowserRefused); err != nil {
		s.report(fmt.Errorf("write the browser door's refusal: %w", err))
	}
}

// --- The action gate -------------------------------------------------------
//
// Everything above this line is layer 1, and every route it guards only reads.
// What follows is what a route that *changes* something passes in addition
// (contracts/actions.md): the two checks that together answer "did the operator's
// own dashboard page ask for this, or did something else ask on their behalf".
//
// The daemon's layer-1 credential is an ambient Access cookie. It rides on
// requests a hostile third-party page triggers, and the edge turns those into a
// valid assertion this daemon accepts — so until this milestone every mutating
// route demanded an HMAC signature no browser can produce, and that, not the
// cookie, is what made the ambient credential safe. The first route that accepts
// a form ends that argument. These two checks replace it.

// fieldPageToken is the form field a mutating dashboard request carries its page
// token in (contracts/actions.md), spelled once so that the field a page renders
// and the field the gate reads cannot drift apart.
const fieldPageToken = "crsw_page_token" //nolint:gosec // G101 false positive: this is the field's *name*, which every rendered page carries in plain sight. The value it names is minted in pagetoken.go and appears in no source file.

// headerContentLength is written by hand on the one response whose length is part
// of what it promises. See refuseAction.
const headerContentLength = "Content-Length"

// bodyActionRefused is the gate's uniform denial, byte for byte from
// contracts/actions.md, and there is exactly one spelling of it for the reason
// bodyBrowserRefused has exactly one: FR-004 is satisfied by every failure
// producing the *identical* response, and a body built per call site is a body
// that eventually differs by a space.
//
// Six reasons end here — a cross-site initiator, an absent one, and pagetoken.go's
// four — and none of them is distinguishable from outside. The difference between
// "malformed" and "not yours" is which forgery to try next.
//
// It references no stylesheet, no script and no external origin, so it renders
// the same under the CSP as without one.
var bodyActionRefused = []byte(`<!doctype html><title>refused</title><p>This action was refused.</p>`)

// The gate's own refusals, authored here and for the trail alone.
//
// Neither is ever written into a response: every failure of step 2 or step 3
// leaves through refuseAction, which writes one fixed body. They are distinct
// sentinels for the reason internal/access's are — the operator reading their
// journal needs to know which check refused, and this is the only place it is
// kept.
var (
	// errActionCrossSite is the same-origin half doing its job (FR-002a).
	//
	// It deliberately does not spell any of the values Sec-Fetch-Site can carry,
	// for the reason errStreamCrossSite does not: the header is caller-authored
	// text, and a reason that quoted one is a reason nobody can tell apart from a
	// reason built from the request (FR-042).
	errActionCrossSite = errors.New("a mutating dashboard request came from somewhere other than the dashboard")

	// errActionFormUnreadable is a body that could not be taken apart into fields
	// at all — over the configured limit, or not form-encoded.
	//
	// Its own sentinel rather than folded into "no token", for the reason
	// errPageTokenMissing is not errPageTokenMalformed: on the record, a form that
	// never arrived and a form that arrived without the field are different
	// faults, and the record is the only place either one is visible.
	errActionFormUnreadable = errors.New("the mutating request's form could not be read")
)

// authorizeAction is the browser door with the action gate on it: the three
// checks contracts/actions.md fixes, in the order it fixes them.
//
//  1. Layer 1, the Cloudflare Access assertion — authenticateBrowser, unchanged.
//  2. The same-origin check, crossSite from stream.go **reused** rather than an
//     Origin comparison added beside it (research R1).
//  3. The page token from the form, verified against the identity step 1
//     produced (pagetoken.go).
//
// The order is load-bearing rather than tidy, and the composition is what
// enforces it: the gate is wrapped *inside* authenticateBrowser, so a route
// cannot be registered with the two the other way round without rewriting this
// line. That is what makes FR-008 structural instead of bookkeeping — an identity
// whose Access session has ended is refused by the door in front, so its token is
// never examined and no record can drift out of step with the Access session,
// because there is no record.
//
// Steps 2 and 3 are independently load-bearing (FR-002c). Either one alone
// refuses, and neither is tested only in the other's company: two checks never
// tested apart are one check with extra steps.
func (s *Server) authorizeAction(action audit.Action, next http.Handler) http.Handler {
	return s.authenticateBrowser(action, s.gateAction(next))
}

// gateAction is steps 2 and 3 themselves, and it runs before next is called —
// before any handler, and therefore before any state changes (FR-003). A request
// that is going to be refused never reaches the code that could tear a session
// down, so "refused" and "refused after acting" cannot be the same event.
func (s *Server) gateAction(next http.Handler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		operator, ok := OperatorFrom(r.Context())
		if !ok {
			// Fail closed on the path that should not happen: the door in front
			// puts the operator in the context, so a false here is a gate wired
			// without one. It answers with layer 1's refusal rather than the
			// action's, because what failed is layer 1's promise and not the
			// caller's request.
			AuditFrom(r.Context()).Deny(errDashboardNoOperator.Error())
			s.refuseBrowser(w)
			return
		}

		if err := s.admitAction(w, r, operator); err != nil {
			if ra := AuditFrom(r.Context()); ra != nil {
				// dashboard.reject and not access.reject, set the way layer 1's
				// refusal above sets its own action (FR-026, research R5): an
				// identity that got in and *then* failed the cross-site check is a
				// different and more alarming event than one that never got in, and
				// an operator counting one must not be counting the other with it.
				ra.rec.Action = audit.ActionDashboardReject
				// Every reason reachable here is a sentinel authored in this
				// package — the two above and pagetoken.go's four — so the record
				// names the check that refused without carrying a byte the caller
				// wrote (FR-035, FR-042). The caller learns none of it.
				ra.Deny(err.Error())
			}
			s.refuseAction(w)
			return
		}

		next.ServeHTTP(w, r)
	}
}

// admitAction runs the two checks and reports which one refused.
//
// It returns the sentinel rather than a boolean because the two audiences differ:
// the caller gets one response whichever it was, and the trail gets the truth.
func (s *Server) admitAction(w http.ResponseWriter, r *http.Request, operator *access.VerifiedOperator) error {
	// Step 2, before the body is read: a request the browser itself calls
	// cross-site costs this daemon one header lookup and nothing else, which is
	// the arrangement sessionStream has and for the same reason.
	if crossSiteAction(r) {
		return errActionCrossSite
	}

	// The same bound the API door decodes under. ParseForm carries its own 10MB
	// ceiling, which is three orders of magnitude more than two short fields can
	// need, and a limit nobody chose is not a limit.
	r.Body = http.MaxBytesReader(w, r.Body, s.cfg.MaxBodyBytes)
	if err := r.ParseForm(); err != nil {
		// Not the parse error, which names a type from net/http and a byte count
		// derived from what the caller sent. The constant says what happened.
		return errActionFormUnreadable
	}

	// Step 3, out of PostForm and deliberately never out of Form: the second holds
	// the query string too, and a token this daemon would accept from a URL is a
	// token in a referrer header, a browser history and a proxy log — the exact
	// disclosure T004 keeps it out of links to avoid. A page that put it there
	// must fail rather than work.
	//
	// The identity is the operator layer 1 verified and never a value read out of
	// the body, the token, or a header the caller chose. That is the whole of
	// FR-007. Absent, malformed, expired and not-yours are pagetoken.go's four
	// sentinels; verify decides which, and the clock is the server's own — the
	// same one that measured the expiry at the mint.
	return s.pageKey.verify(r.PostForm.Get(fieldPageToken), operator.Email, s.clock.Now())
}

// crossSiteAction is step 2: crossSite, plus the one thing a mutating route needs
// that a stream does not — the header must be *there*.
//
// crossSite admits an absent Sec-Fetch-Site on purpose (research D8,
// docs/security.md): browsers send the header and non-browser clients do not, so
// requiring it on the pane stream would refuse the quickstart's curl while adding
// nothing against the attack it is about. That argument does not survive the move
// to a route that changes something. The only legitimate caller of an action
// route is a form this daemon rendered, submitted by a browser, which always
// sends the header; a script that wants to change something uses the API door and
// its signature (FR-005). So absent refuses here, which is what
// contracts/actions.md and FR-002a both require: an absent header is not evidence
// of same-origin initiation, and treating it as such makes the check optional for
// anything that can omit it.
//
// crossSite itself is untouched, and that is deliberate rather than incidental.
// The stream's behaviour is milestone 2's, tested, and FR-014's promise is that
// this milestone changes no response an existing client already sees.
func crossSiteAction(r *http.Request) bool {
	return crossSite(r) || r.Header.Get(headerSecFetchSite) == ""
}

// refuseAction answers every failure of step 2 or step 3 identically (FR-004,
// SC-001): one status, one header set, one body, one Content-Length, whichever of
// the six reasons applied.
//
// It takes no reason argument, for the reason refuseBrowser takes none: there is
// nothing a caller could pass that would be allowed to change a byte, so the
// parameter would only be an invitation.
//
// 403 and not this door's 401 (contracts/actions.md). A 401 says "authenticate",
// and this caller already did, successfully — reusing it would tell an attacker
// their Access credential was the problem when it was not, and would invite a
// browser to re-prompt for a login that cannot help.
//
// The length is written rather than left to net/http, so that byte-identical is a
// property of this function rather than a property of how the response happened
// to be buffered. Everything else the response carries — nosniff among it — was
// written by setBrowserSecurityHeaders before layer 1 ran, so this refusal leaves
// with the identical header set to a served page (FR-026).
func (s *Server) refuseAction(w http.ResponseWriter) {
	w.Header().Set(headerContentType, contentTypeHTML)
	w.Header().Set(headerContentLength, strconv.Itoa(len(bodyActionRefused)))
	w.WriteHeader(http.StatusForbidden)
	if _, err := w.Write(bodyActionRefused); err != nil {
		s.report(fmt.Errorf("write the browser door's action refusal: %w", err))
	}
}
