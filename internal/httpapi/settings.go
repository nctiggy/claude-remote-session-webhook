package httpapi

// settings.go is GET /settings — the read-only account of how this daemon was
// configured, and the page that makes US1 legible: an operator who edited a file
// and saw nothing change reads here which layer actually supplied each value.
//
// It reads the Config the server was built from and nothing else. Every value on
// it was resolved once, at startup, by the one shim in internal/config that
// decides precedence — so the page cannot infer a provenance the loader did not
// record, and cannot disagree with the daemon it describes (research R4). There
// is no re-read, no lookup of the environment, and no path taken from the
// request.
//
// **No mutating verb is registered on this path, and that absence is the
// safeguard rather than a handler that refuses.** Writing the operator's
// configuration file from a browser is the highest-consequence surface in the
// product (spec, Out of Scope); a route that does not exist cannot be exploited,
// mis-gated, or reached by a future refactor that forgets which door it is on.

import (
	"net/http"

	"github.com/nctiggy/claude-remote-session-webhook/internal/access"
)

// patternSettings is the settings page's route, and the method is part of it for
// the reason it is part of every other pattern in this package: net/http matches
// both halves, so a path registered for GET is not silently reachable by POST.
//
// It carries no `{$}` because it names an exact path rather than the root — only
// `/` is a subtree pattern in net/http's router, so `GET /settings` matches that
// one path and nothing beneath it.
const patternSettings = "GET /settings"

// settingsView is the whole of what the settings page renders against.
//
// It is a page rather than a component, which is why it is here beside the
// handler and not in view.go — the same division fleetView and sessionPageView
// follow.
type settingsView struct {
	// Operator is the identity layer 1 verified, passed straight to the header
	// component as the same pointer OperatorFrom returned (FR-020, FR-036). It is
	// the only thing on this page that comes from the request at all, and it did
	// not come from anything the caller wrote.
	Operator *access.VerifiedOperator
}

// settings serves GET /settings (FR-016 … FR-020, contracts/settings-page.md).
//
// It mints no page token, and that is a decision rather than an omission. A page
// token authorises a write, this page offers none, and the one on a rendered page
// is a value worth not minting where nothing can spend it — the fleet and the
// session view each carry one because each carries forms.
func (s *Server) settings(w http.ResponseWriter, r *http.Request) {
	operator, ok := OperatorFrom(r.Context())
	if !ok {
		// Fail closed on the path that should not happen, exactly as the fleet
		// does: newServer registers this route behind authenticateBrowser, so a
		// false here is a wiring mistake and deserves a reason of its own in the
		// trail rather than a page rendered for nobody.
		AuditFrom(r.Context()).Deny(errDashboardNoOperator.Error())
		s.refuseBrowser(w)
		return
	}

	// No SetSessionID: this page is about the daemon and not about one session.
	// The record the middleware emits carries settings.view and the identity that
	// asked, which is the whole of what an operator counting who read the
	// configuration needs.
	s.renderPage(w, r, http.StatusOK, "settings", settingsView{Operator: operator})
}
