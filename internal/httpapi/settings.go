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
	"github.com/nctiggy/claude-remote-session-webhook/internal/config"
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

	// Settings is the table's rows, projected from the Config at render time by
	// settingsOf. Today it holds the secret keys and only those (T011); T012
	// widens the same walk to every key config.go declares and adds the source
	// column beside the value.
	Settings []settingRow
}

// settingRow is one configuration key as the page states it.
//
// There is a Value and there is no raw value beside it, which is the whole
// arrangement: what reaches the template is what a browser is allowed to see, so
// there is no field for a render to reach for by mistake and no second
// projection where a "helpful" masked form could be added without touching
// settingsOf.
type settingRow struct {
	// Key is the file spelling — the environment variable minus CRSW_,
	// lower-cased — because that is the spelling an operator wrote in their own
	// file and the spelling config.IsSecret takes.
	Key string

	// Value is the effective value, or — for a secret — one of the two words
	// below and nothing else.
	Value string
}

// The value column's entire vocabulary for a secret (FR-017,
// contracts/settings-page.md). Not a length, not a prefix, not a suffix, not a
// hash: a masked value is still a disclosure, and the four characters of a
// credential a page is willing to print are four an attacker no longer has to
// guess.
const (
	secretPresent = "present"
	secretAbsent  = "absent"
)

// settingsOf projects a Config into the rows the settings page renders.
//
// config.IsSecret is the gate, and it is deliberately the same predicate the
// 0600 refusal in internal/config/file.go asks (T001). A second list of secret
// keys here would be invisible while every test passed: this page would
// confidently print the value that check thought too sensitive to leave
// group-readable, and the disagreement would surface as a credential in a
// browser rather than as a failure.
//
// It walks config.Vars() rather than a list of its own, so the page cannot
// invent a setting or forget one, and the order is the order config.go declares
// them.
func settingsOf(cfg *config.Config) []settingRow {
	rows := make([]settingRow, 0, len(config.Vars()))
	for _, name := range config.Vars() {
		key := config.KeyForVar(name)
		// Every other key is T012's, which owns the value column and the source
		// beside it. This task owns the one cell whose contents are a security
		// decision, and shipping it first is what keeps that decision from being
		// retrofitted onto a table that already prints everything.
		if !config.IsSecret(key) {
			continue
		}
		configured, known := secretConfigured(cfg, name)
		rows = append(rows, settingRow{Key: key, Value: presence(configured && known)})
	}
	return rows
}

// presence is the only function that turns a fact about a secret into text, so
// there is one place that could ever be made to say more than two words.
func presence(configured bool) string {
	if configured {
		return secretPresent
	}
	return secretAbsent
}

// secretConfigured reports whether a secret setting has a value on this daemon,
// and whether this page can tell.
//
// It reads the Config's own field rather than the provenance map, because
// provenance answers a different question — which layer supplied a value — and a
// secret with no default makes the two look identical right up until one of them
// is wrong.
//
// The second return exists for the key that is not here yet. config.IsSecret is
// the classifier, so a third secret added there is hidden by settingsOf whether
// or not this switch has heard of it; what it would otherwise lose is the
// ability to say anything true about it. An unknown one reads as absent rather
// than as present because that is the answer an operator investigates: reading
// "absent" for a configured secret sends somebody to look, and reading "present"
// for one that is missing does not. TestEverySecretKeyReportsItsPresence keeps
// the branch unreachable.
func secretConfigured(cfg *config.Config, name string) (configured, known bool) {
	switch name {
	case config.EnvSharedSecret:
		return len(cfg.SharedSecret) > 0, true
	case config.EnvAccessAllowedEmails:
		return len(cfg.AccessAllowedEmails) > 0, true
	default:
		return false, false
	}
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
	// The Config the server was built from, read here and nowhere else: every
	// value on this page was resolved once, at startup, so the page cannot
	// disagree with the daemon it describes.
	s.renderPage(w, r, http.StatusOK, "settings", settingsView{
		Operator: operator,
		Settings: settingsOf(s.cfg),
	})
}
