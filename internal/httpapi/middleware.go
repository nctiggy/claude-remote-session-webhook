package httpapi

import (
	"context"
	"fmt"
	"log"
	"net/http"

	"github.com/nctiggy/claude-remote-session-webhook/internal/audit"
	"github.com/nctiggy/claude-remote-session-webhook/internal/auth"
)

// bodyUnauthorized is the layer-2 denial, byte for byte from
// contracts/http-api.md. It is a package-level value and not a fmt call so that
// there is exactly one spelling of it: FR-011 is satisfied by every failure
// producing the *identical* response, and a body built per call site is a body
// that eventually differs by a space.
//
// No trailing newline, no WWW-Authenticate header, no hint of which of the four
// checks refused. A caller learns only that it is not authenticated.
var bodyUnauthorized = []byte(`{"error":"unauthorized"}`)

const (
	headerContentType = "Content-Type"
	contentTypeJSON   = "application/json"
)

// routeActions names the audit action for every route in the contract.
//
// It is a map rather than a switch so that "does this route have an action?" is
// a lookup a startup check can make (see handle). FR-041 wants exactly one
// record per request, which is only possible if every route the mux serves has
// something to record it under — so a seventh route added without an entry here
// fails at startup rather than serving unrecorded traffic.
var routeActions = map[Route]audit.Action{
	{http.MethodPost, "/sessions"}:             audit.ActionSessionCreate,
	{http.MethodGet, "/sessions"}:              audit.ActionSessionList,
	{http.MethodGet, "/sessions/{id}"}:         audit.ActionSessionDetail,
	{http.MethodDelete, "/sessions/{id}"}:      audit.ActionSessionDestroy,
	{http.MethodPost, "/sessions/{id}/prompt"}: audit.ActionSessionPrompt,
	{http.MethodGet, "/sessions/{id}/output"}:  audit.ActionSessionOutput,
}

// contextKey is unexported and its values are unexported, so nothing outside
// this package can plant a Caller or a RequestAudit in a context. Identity is
// derived server-side (FR-012); a key another package could construct would be
// a way to supply one.
type contextKey int

const (
	callerContextKey contextKey = iota
	auditContextKey
)

// RequestAudit is the single record a request will produce, held open while the
// handler runs so the handler can say what happened before it is emitted.
//
// It exists because FR-041 says *exactly one* record per request. A handler that
// emitted its own would make two for one request — and the alternative, emitting
// before the handler runs, cannot record a 404 for someone else's session or a
// teardown that could not be verified, which are the two outcomes an operator
// most needs in the trail. So the middleware opens the record, the handler
// amends it, and the middleware emits it once on the way out.
//
// Not safe for concurrent use: it belongs to one request, on the goroutine
// net/http gave that request.
type RequestAudit struct{ rec audit.Record }

// SetSessionID records which session the request acted on.
//
// It must be given the ID from the daemon's own record, never the {id} the
// caller put in the path: the trail may not carry caller-supplied bytes
// (FR-042), and an unknown ID is not a session.
func (a *RequestAudit) SetSessionID(id string) {
	if a == nil {
		return
	}
	a.rec.SessionID = id
}

// Deny turns the record into a refusal with a server-authored reason. It is what
// an ownership or token failure calls (T023) — the client still gets the uniform
// 404, and this is where the truth of it is kept.
//
// The reason must be a constant authored in this repo. Never an error built from
// the request, a path, a name, or a body (FR-042, FR-043).
func (a *RequestAudit) Deny(reason string) {
	if a == nil {
		return
	}
	a.rec.Decision = audit.Deny
	a.rec.Reason = reason
}

// CallerFrom reports the identity the middleware authenticated for this request.
//
// A handler behind the middleware can rely on ok being true — the handler is not
// reached otherwise — and should treat a false as a refusal rather than as an
// anonymous caller, which is why the boolean exists at all.
func CallerFrom(ctx context.Context) (*auth.Caller, bool) {
	caller, ok := ctx.Value(callerContextKey).(*auth.Caller)
	return caller, ok && caller != nil
}

// AuditFrom returns the record this request will emit, or nil if the request did
// not come through the middleware. Every method on *RequestAudit is nil-safe, so
// a handler under direct test does not need to check.
func AuditFrom(ctx context.Context) *RequestAudit {
	ra, ok := ctx.Value(auditContextKey).(*RequestAudit)
	if !ok {
		return nil
	}
	return ra
}

// authenticate is layer 2 (FR-007), applied by handle to every route with no
// exemption possible: it is wrapped around the handler at the single point where
// a route reaches the mux, so an unauthenticated route cannot be written, only
// unwired.
//
// The audit emit is deferred rather than called at each return. That makes one
// record per request a property of the control flow instead of a rule four exit
// paths have to remember — and it holds even if the handler panics, which is
// exactly the request an operator would want a record of.
func (s *Server) authenticate(action audit.Action, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Deny is the starting decision, so a path that never reaches a verdict
		// records a refusal rather than an approval.
		ra := &RequestAudit{rec: audit.Record{
			Action:   action,
			Decision: audit.Deny,
			Remote:   r.RemoteAddr,
		}}
		defer s.emit(ra)

		caller, err := s.authn.Verify(r)
		if err != nil {
			ra.rec.Action = audit.ActionAuthReject
			// auth.Reason documents that it returns that package's own
			// sentinels — fixed strings authored there, never built from
			// anything the caller sent — which is what makes this safe to put
			// in the trail (FR-042).
			ra.rec.Reason = auth.Reason(err).Error()
			s.writeUnauthorized(w)
			return
		}

		ra.rec.Caller = string(caller.ID)
		ra.rec.Decision = audit.Allow

		ctx := context.WithValue(r.Context(), callerContextKey, caller)
		ctx = context.WithValue(ctx, auditContextKey, ra)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// writeUnauthorized answers every layer-2 failure identically (FR-011).
func (s *Server) writeUnauthorized(w http.ResponseWriter) {
	w.Header().Set(headerContentType, contentTypeJSON)
	w.WriteHeader(http.StatusUnauthorized)
	if _, err := w.Write(bodyUnauthorized); err != nil {
		s.report(fmt.Errorf("write the unauthorized response: %w", err))
	}
}

// emit writes the request's one record.
//
// A failed write does not change the response, and that is deliberate. The
// answer a caller gets must depend on the request alone: a 500 that appeared
// only when the audit sink broke would make the uniform 401 non-uniform and turn
// the trail into a side channel. What it does instead is refuse to be silent —
// FR-041 makes the record mandatory, so a daemon that cannot write one is broken
// and its operator has to find out from somewhere.
func (s *Server) emit(ra *RequestAudit) {
	if err := s.trail.Emit(ra.rec); err != nil {
		s.report(err)
	}
}

// reportToStderr is the last-resort channel for a failure with nowhere else to
// go — the audit sink itself failing, or a response that could not be written.
//
// It uses log rather than the audit logger on purpose: this is what is left when
// the audit logger is the thing that broke. It carries an error string built in
// this repo and never a secret, a token, a body, or pane output (FR-043).
func reportToStderr(err error) { log.Printf("crswd: %v", err) }
