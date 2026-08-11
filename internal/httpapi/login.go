package httpapi

// login.go is what puts the password door to work (M12/T004): the page an
// operator meets on a daemon with no Cloudflare in front of it, and the form
// post that turns knowing the configured secret into the cookie password.go
// verifies on every request afterwards.
//
// **These are the only two routes on this daemon in front of layer 1.** Every
// other route on the browser door is registered through handleBrowser or
// handleAction and cannot be reached without a verified operator; these cannot
// be reached *with* one, because their whole purpose is to produce the
// credential that makes one. That is not a hole in the door — it is the door's
// keyhole, and everything that makes it safe is written down here:
//
//   - They exist only when the password door is the configured layer 1
//     (newServer). Where Access is the door, or where there is no door at all,
//     nothing is registered and the path falls to the catch-all, which answers
//     from behind layer 1 like every other path nothing claims. A login form
//     standing beside a working Access door would be the second authorisation
//     path this milestone's plan forbids.
//   - They change nothing on the host. No session is created, destroyed, driven
//     or read here; the only thing that happens is a Set-Cookie, and only for a
//     caller who supplied the secret.
//   - Every refusal is the browser door's own uniform 401, byte for byte
//     (refuseBrowser), so a wrong password and an unreadable form are one answer.
//     Which it really was is on the trail, where the operator can read it.
//   - The password is read from PostForm and compared in passwordDoor.admits,
//     which is the only place in this daemon the two sides are ever compared. It
//     reaches no log, no record, no page, no error and no URL.
//
// What is *not* here is the action gate. It cannot be: two of its three checks
// are the layer-1 identity and a page token bound to it, and neither exists
// before this route runs. Half a gate would be a second, differently shaped
// authorisation path — the thing there is exactly one of on this door. What a
// hostile third-party page can therefore do is cause the operator's browser to
// submit guesses it cannot read the answer to; what bounds that is the limiter
// T005 puts in front of this route.

import (
	"context"
	"errors"
	"net/http"

	"github.com/nctiggy/claude-remote-session-webhook/internal/audit"
	"github.com/nctiggy/claude-remote-session-webhook/internal/auth"
)

// The sign-in route: one path, two verbs, spelled from one constant so the page
// and the form that posts to it cannot drift onto different addresses.
//
// A verb this path does not answer is an unknown route — handleUnrouted's
// catch-all takes it, exactly as it does for the action routes, so a PUT here is
// the browser door's 404 with no Allow header rather than a 405 confirming the
// path exists.
const (
	pathLogin          = "/login"
	patternLoginPage   = http.MethodGet + " " + pathLogin
	patternLoginSubmit = http.MethodPost + " " + pathLogin
)

// fieldPassword is the form field the password arrives in, spelled once so that
// the field the page renders and the field this route reads cannot drift apart —
// the arrangement fieldPageToken already has.
//
//nolint:gosec // G101 false positive: this is the field's *name*, which the login page carries in plain sight. The value it names is the operator's configured secret and appears in no source file.
const fieldPassword = "password"

// The sign-in refusals, authored here and for the trail alone.
//
// Distinct sentinels for the reason password.go's are: the operator reading
// their journal needs to know whether a form arrived at all or arrived with the
// wrong answer in it, and the record is the only place that is kept. **No caller
// ever learns which** — both leave through refuseBrowser, which writes the same
// bytes layer 1 writes for a missing cookie, so a stranger cannot tell a
// rejected guess from a malformed submission from a request that never reached
// this route.
//
// Neither carries a byte the caller sent, and in particular neither carries the
// submitted password or the configured one (FR-035, FR-042).
var (
	errLoginFormUnreadable = errors.New("the sign-in form could not be read")

	errLoginRefused = errors.New("the submitted password is not the configured one")
)

// handleLogin is the one place a sign-in route reaches the mux, so neither can
// be registered without the security headers, the audit record, and the absence
// of layer 1 that this route — and only this route — is allowed.
//
// It is a third registration function beside handleBrowser and handleAction, for
// the reason those two are separate from each other: the difference between them
// is what they promise, and making it a parameter would make it a thing a call
// site can get wrong quietly. "Registered in front of layer 1" is the most
// consequential promise of the three, so it is the one that takes a function of
// its own and a name somebody has to type.
func (s *Server) handleLogin(pattern string, action audit.Action, h http.HandlerFunc) {
	s.mux.Handle(pattern, s.serveLogin(action, h))
}

// serveLogin is everything authenticateBrowser does except the one thing this
// route cannot have.
//
// The headers are written before the handler runs, so a refusal leaves with the
// identical set to a served page (FR-026) and the login page cannot become the
// one response on this door that a cache may keep.
//
// The audit emit is deferred rather than called at each return, which makes one
// record per request (FR-016) a property of the control flow instead of a rule
// two exit paths have to remember, and holds even if the handler panics. Deny is
// the starting decision, so a path that never reaches a verdict records a
// refusal rather than an approval — and on this route in particular, a handler
// that fell over silently must not read afterwards as somebody who got in.
//
// No operator is put in the context, deliberately. There is none: a handler
// under this middleware that called OperatorFrom would be told so, which is what
// keeps a page needing an identity from being registered here by mistake.
func (s *Server) serveLogin(action audit.Action, next http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ra := &RequestAudit{rec: audit.Record{
			Action:   action,
			Decision: audit.Deny,
			Remote:   r.RemoteAddr,
		}}
		defer s.emit(ra)

		setBrowserSecurityHeaders(w.Header())

		next(w, r.WithContext(context.WithValue(r.Context(), auditContextKey, ra)))
	})
}

// loginPage serves the form.
//
// The record is marked served before the render rather than after it, because
// renderPage records its own refusal when a template fails — so a page that
// could not be built overwrites this rather than being reported as one that was.
//
// The caller is deliberately left empty, which the trail writes as `unknown`:
// whoever asked for this page has proved nothing, and a record naming the
// operator would make asking for the form indistinguishable from passing it.
//
// It renders against no data at all. Nothing on this page is a fact about the
// daemon — no version, no address, no operator — and a view struct would be an
// invitation to add one.
func (s *Server) loginPage(w http.ResponseWriter, r *http.Request) {
	AuditFrom(r.Context()).allow("")
	s.renderPage(w, r, http.StatusOK, "login", nil)
}

// login answers the form: the cookie on success, the door's uniform refusal on
// anything else.
//
// It takes the door as an argument rather than reading s.browser, so that the
// concrete *passwordDoor is the thing the route was registered with. A handler
// that asserted the type per request would be a handler that has to decide what
// to do when the assertion fails — which is the branch this route exists on the
// far side of, taken once at startup where it can be read.
//
// 303 rather than a rendered fleet, so the operator's browser follows with a GET
// and a reload does not repost the password. It goes to the fleet because that
// is where somebody signing in was going; there is no "return to" parameter and
// there deliberately is not one, since an unauthenticated caller supplying the
// address a successful sign-in redirects to is an open redirect with a login
// form in front of it.
func (s *Server) login(door *passwordDoor) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := s.admitLogin(w, r, door); err != nil {
			AuditFrom(r.Context()).Deny(err.Error())
			s.refuseBrowser(w)
			return
		}

		// The owner both doors resolve to (FR-037a), and not passwordOperator: the
		// trail names who acts on this host, and every other record on it — the
		// API's included — spells that one way.
		AuditFrom(r.Context()).allow(string(auth.CallerOperator))
		http.Redirect(w, r, pathFleet, http.StatusSeeOther)
	}
}

// admitLogin is the check itself, and it reports which half refused.
//
// It returns the sentinel rather than a boolean because the two audiences
// differ, exactly as admitAction's does: the caller gets one response whichever
// it was, and the trail gets the truth.
//
// The body is bounded by the same limit the API door decodes under. ParseForm
// carries its own 10MB ceiling, which is three orders of magnitude more than one
// short field can need, and a limit nobody chose is not a limit.
//
// PostForm and never Form, for the reason the page token is read that way: the
// second holds the query string, and a password this daemon would accept from a
// URL is a password in a browser history, a referrer header and every proxy log
// between here and the browser. A form that put it there must fail rather than
// work.
//
// Nothing distinguishes a missing field from a wrong one, here or anywhere
// after. `admits` compares the digest of whatever arrived — the empty string
// included — so there is no branch on presence to be timed, and no third
// sentinel saying the field was absent.
func (s *Server) admitLogin(w http.ResponseWriter, r *http.Request, door *passwordDoor) error {
	r.Body = http.MaxBytesReader(w, r.Body, s.cfg.MaxBodyBytes)
	if err := r.ParseForm(); err != nil {
		// Not the parse error, which names a type from net/http and a byte count
		// derived from what the caller sent. The constant says what happened.
		return errLoginFormUnreadable
	}

	if !door.admits([]byte(r.PostForm.Get(fieldPassword))) {
		return errLoginRefused
	}

	// Last, so that a cookie is written only for a request that has already been
	// admitted: a Set-Cookie beside a refusal would be a credential handed to
	// somebody the line above turned away.
	return door.issue(w, r)
}
