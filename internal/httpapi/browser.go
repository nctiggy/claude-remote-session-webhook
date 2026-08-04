package httpapi

// browser.go is the browser door. middleware.go is the API door, and the two
// are deliberately separate files for the reason contracts/dashboard.md gives
// them separate route tables: each door refuses only by the check that applies
// to it (FR-012), so a browser request is never refused for carrying no
// signature and an API request is never refused for carrying no identity.
//
// What runs here is layer 1 and nothing else — the Cloudflare Access assertion,
// validated by internal/access. Layers 2 and 3 are untouched: no dashboard route
// asks for a signature, and no API route consults an assertion.

import (
	"context"
	"errors"
	"fmt"
	"net/http"

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

// layer1 is the browser door's whole dependency: something that can turn an
// assertion into a verified operator, or refuse.
//
// It is an interface because internal/access ships two implementations —
// *access.Validator, and the //go:build dev *access.Bypass that skips layer 1 on
// a laptop where there is no Cloudflare Access to sign anything. That package
// declares the same signature and asserts both types against it, so the door in
// front of them is one door. A middleware written against *Validator and
// *adapted* for the bypass would be a second authorisation path, and the second
// path is the one nobody reads.
type layer1 interface {
	Verify(ctx context.Context, assertion string) (*access.VerifiedOperator, error)
}

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
// The request's own context is passed down, so a key-set fetch a browser
// abandoned is abandoned with it — and, because unobtainable keys refuse
// (FR-009), that ends as a denial rather than as an admission.
func (s *Server) verifyBrowser(r *http.Request) (*access.VerifiedOperator, error) {
	if s.browser == nil {
		return nil, errBrowserDoorUnwired
	}

	operator, err := s.browser.Verify(r.Context(), r.Header.Get(headerAccessAssertion))
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
