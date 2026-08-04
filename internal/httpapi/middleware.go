package httpapi

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"

	"github.com/nctiggy/claude-remote-session-webhook/internal/audit"
	"github.com/nctiggy/claude-remote-session-webhook/internal/auth"
	"github.com/nctiggy/claude-remote-session-webhook/internal/session"
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

// bodyNotFound is the layer-3 denial, byte for byte from contracts/http-api.md,
// and a package-level value for the reason bodyUnauthorized is one.
//
// An unknown ID, another owner's session, a wrong bearer token, an expired one,
// and a session already dead all answer with these bytes and no others
// (FR-033). The difference between "does not exist" and "not yours" is what an
// enumeration attack is made of; it is kept in the trail, where the operator can
// read it and the caller cannot.
var bodyNotFound = []byte(`{"error":"not found"}`)

const (
	headerContentType   = "Content-Type"
	headerAuthorization = "Authorization"
	contentTypeJSON     = "application/json"

	// bearerScheme is the only credential scheme this API accepts, with its one
	// separating space. Compared case-insensitively, since RFC 7235 makes the
	// scheme name case-insensitive and a client that sends "bearer" is holding
	// the right credential.
	bearerScheme = "Bearer "

	// pathValueID is the wildcard every session-scoped route carries. Spelled
	// once, because the string in the pattern and the string read back out of
	// the request are the same string: a typo in either would make PathValue
	// return "" and every lookup miss.
	pathValueID = "id"
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
	sessionContextKey

	// operatorContextKey is the browser door's equivalent of callerContextKey
	// and is deliberately not the same key: a request comes through one door,
	// and a handler that read whichever value happened to be present would be
	// one that could be reached by the wrong credential. See browser.go.
	operatorContextKey
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
type RequestAudit struct {
	rec audit.Record

	// emitted is what keeps "exactly one record" true for a handler that writes
	// its own record before it returns. The stream is the one that does
	// (FR-016a): a response lasting hours cannot wait for the emit above, so it
	// emits at the open, and this is what stops the middleware writing a second
	// record hours later saying the same thing.
	//
	// The consequence is an ordering rule for any handler that emits early:
	// everything the record is to say has to be said first, because an amendment
	// after the emit changes nothing that reaches the trail.
	emitted bool
}

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

// SessionFrom reports the session a session-scoped request was resolved to.
//
// It is the only way a handler on an {id} route may learn which session it is
// acting on. The record it returns has already been matched against the caller's
// identity and the presented credential, and every target derives from its ID —
// which is FR-034 stated as an API: a handler that took the {id} out of the path
// itself would be addressing a window on a caller's say-so.
//
// The Session is a copy, as Store hands out. Mutating it changes nothing.
func SessionFrom(ctx context.Context) (session.Session, bool) {
	s, ok := ctx.Value(sessionContextKey).(session.Session)
	return s, ok
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

// The reasons the resolver records, authored here.
//
// None is ever written into a response, and none is built from a byte the caller
// sent — in particular never the {id} out of the path, which is caller-supplied
// text the trail may not carry (FR-042). The refusals that come from
// internal/session are recorded under that package's own sentinels instead; see
// resolveReason.
var (
	// errScopeNoCaller is unreachable behind the authentication middleware,
	// which is why it fails closed rather than resolving against an empty
	// identity: Store.Get treats the zero CallerID as matching nothing, so this
	// would already refuse — but it would refuse as "no such session", and a
	// wiring mistake that removed layer 2 deserves a reason of its own in the
	// trail rather than a 404 that looks routine.
	errScopeNoCaller = errors.New("a session-scoped route was reached with no authenticated caller")

	// errScopeNoCredential is a request with no bearer token at all. It is a 404
	// and not a 401: holding the shared secret is not evidence about any
	// particular session, so "you did not present a session credential" and
	// "that session is not yours" must read alike from outside (FR-014).
	errScopeNoCredential = errors.New("no session credential was presented")

	// errScopeRefused is the fail-closed reason for a refusal none of the
	// sentinels explain. A refusal nobody classified is still a refusal.
	errScopeRefused = errors.New("the session could not be resolved")
)

// resolveSession is layer 3 (FR-014), applied by handle to every route carrying
// an {id} and to no others — at the same single point where a route reaches the
// mux, so a session-scoped route physically cannot be registered without it.
//
// Everything it decides, it delegates. The lookup, the ownership match, the
// credential compare, the expiry, and the dead-record rule are one call into
// internal/session, where the store and the clock live; what belongs here is the
// half that is about HTTP: reading the credential off the request, answering
// every failure with the identical 404, and handing the resolved record to the
// handler so that nothing downstream ever reads the {id} again.
func (s *Server) resolveSession(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		caller, ok := CallerFrom(r.Context())
		if !ok {
			s.refuseSession(w, r, errScopeNoCaller)
			return
		}

		presented, ok := bearerToken(r)
		if !ok {
			s.refuseSession(w, r, errScopeNoCredential)
			return
		}

		resolved, err := s.sessions.Resolve(r.PathValue(pathValueID), caller.ID, presented)
		if err != nil {
			s.refuseSession(w, r, resolveReason(err))
			return
		}

		// The ID off the daemon's own record, never the bytes the caller put in
		// the path — which is the rule SetSessionID exists to keep, and is only
		// safe to do here because the record has just been matched to them.
		AuditFrom(r.Context()).SetSessionID(resolved.ID)

		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), sessionContextKey, resolved)))
	})
}

// bearerToken reads the credential out of the Authorization header.
//
// The parse is deliberately strict: one scheme, one space, and a non-empty
// remainder, with no trimming. A token surrounded by whitespace is not the token
// that was issued and would fail the compare anyway, so leniency here would buy
// nothing except a second spelling of a credential.
func bearerToken(r *http.Request) (string, bool) {
	header := r.Header.Get(headerAuthorization)
	if len(header) <= len(bearerScheme) || !strings.EqualFold(header[:len(bearerScheme)], bearerScheme) {
		return "", false
	}
	return header[len(bearerScheme):], true
}

// resolveReason picks the most specific account of a refusal that is still a
// fixed string, exactly as createReason does and for the same reason: the
// wrapped error is never used directly, so no rewording — or one %w — in
// internal/session can put a caller-supplied path value into the trail.
func resolveReason(err error) error {
	for _, reason := range []error{
		session.ErrTokenMismatch,
		session.ErrTokenExpired,
		session.ErrSessionDead,
		session.ErrSessionNotFound,
	} {
		if errors.Is(err, reason) {
			return reason
		}
	}
	return errScopeRefused
}

// refuseSession answers every layer-3 failure identically (FR-033) and records
// which one it really was.
//
// It is one function for the reason writeUnauthorized is one: FR-033 is
// satisfied by every refusal producing the *identical* response, and a status
// and body assembled at five call sites is a response that eventually differs by
// a space. No Content-Length variation, no header naming the check, no hint.
func (s *Server) refuseSession(w http.ResponseWriter, r *http.Request, reason error) {
	AuditFrom(r.Context()).Deny(reason.Error())

	w.Header().Set(headerContentType, contentTypeJSON)
	w.WriteHeader(http.StatusNotFound)
	if _, err := w.Write(bodyNotFound); err != nil {
		s.report(fmt.Errorf("write the not-found response: %w", err))
	}
}

// writeUnauthorized answers every layer-2 failure identically (FR-011).
func (s *Server) writeUnauthorized(w http.ResponseWriter) {
	w.Header().Set(headerContentType, contentTypeJSON)
	w.WriteHeader(http.StatusUnauthorized)
	if _, err := w.Write(bodyUnauthorized); err != nil {
		s.report(fmt.Errorf("write the unauthorized response: %w", err))
	}
}

// emit writes the request's one record, and writes it once.
//
// A failed write does not change the response, and that is deliberate. The
// answer a caller gets must depend on the request alone: a 500 that appeared
// only when the audit sink broke would make the uniform 401 non-uniform and turn
// the trail into a side channel. What it does instead is refuse to be silent —
// FR-041 makes the record mandatory, so a daemon that cannot write one is broken
// and its operator has to find out from somewhere.
//
// Writing at most once is what lets a handler put its record on the trail at the
// moment the decision is made rather than when it returns (FR-016a). Both
// middlewares still defer this call on every path out, including a panic, so the
// count is exactly one whether the handler emitted or not — and the guard lives
// here rather than at the one call site that needs it, because "exactly one
// record per request" is this function's invariant and not a rule a handler is
// trusted to keep.
//
// The nil case is a handler called outside either door, which every other method
// on *RequestAudit already tolerates for the same reason.
func (s *Server) emit(ra *RequestAudit) {
	if ra == nil || ra.emitted {
		return
	}
	ra.emitted = true

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
