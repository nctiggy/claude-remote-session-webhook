// Internal test, matching the rest of the package. Every claim here is about the
// route rather than about the markup — what the door does with a caller it will
// not verify, what the trail says about a page that was served, and what this
// path answers a verb it has no route for — so each one drives /settings through
// the real router, the real browser door, and a real *access.Validator over a
// locally generated key pair. A handler called directly would prove none of them.
package httpapi

import (
	"bytes"
	"context"
	"errors"
	"html"
	"maps"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/nctiggy/claude-remote-session-webhook/internal/buildinfo"
	"github.com/nctiggy/claude-remote-session-webhook/internal/updater"

	"github.com/nctiggy/claude-remote-session-webhook/internal/audit"
	"github.com/nctiggy/claude-remote-session-webhook/internal/auth"
	"github.com/nctiggy/claude-remote-session-webhook/internal/config"
	"github.com/nctiggy/claude-remote-session-webhook/internal/session"
	"github.com/nctiggy/claude-remote-session-webhook/web"
)

// settingsPath is derived from the pattern the server registers rather than
// spelled again here, for the reason fleetStreamPath is: a renamed route would
// otherwise leave every test in this file passing against a path nothing claims.
var settingsPath = strings.TrimPrefix(patternSettings, http.MethodGet+" ")

// ask drives one request at any method through the whole daemon as the verified
// operator, and hands back whatever it answered.
//
// The credential is a genuine assertion because the claims below are about the
// *route*: a request refused by layer 1 would never reach the router at all, so a
// method the daemon has no route for has to be asked by somebody it would
// otherwise serve.
func (f *fleet) ask(t *testing.T, method, target string) *httptest.ResponseRecorder {
	t.Helper()

	r := httptest.NewRequest(method, target, nil)
	r.Header.Set(headerAccessAssertion, f.keys.mint(t, f.keys.claims()))

	w := httptest.NewRecorder()
	f.ServeHTTP(w, r)
	return w
}

// TestSettingsRequiresIdentity is contracts/settings-page.md's first row: a
// caller layer 1 will not verify gets this door's one uniform refusal, and the
// page is never rendered for them.
//
// The two rows are the two shapes of "not this operator" — no assertion at all,
// and a genuine assertion, signed by the published key and minted for this
// application, naming somebody the allowlist does not. The second is the one that
// matters: it is a real person at the edge, and the only thing standing between
// them and every configured value on this host is the allowlist check.
//
// The refusal is compared byte for byte against the fleet's refusal for the same
// credential, rather than merely asserted a 401. FR-010's uniformity is *within*
// the door, and a settings page whose refusal differed by a header would tell a
// stranger this daemon serves something at that address — which is the route
// table, disclosed one probe at a time.
//
// **Must fail when** the page is registered outside the identity middleware. That
// mutation still answers 401, because the handler fails closed on an absent
// operator — what it cannot produce is the record: authenticateBrowser is the
// only thing that emits one, so a route registered without it leaves the trail
// silent about a refusal that happened.
func TestSettingsRequiresIdentity(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name      string
		assertion func(t *testing.T, f *fleet) string
	}{
		{"no assertion at all", func(*testing.T, *fleet) string { return absent }},
		{"an assertion naming somebody the allowlist does not", func(t *testing.T, f *fleet) string {
			t.Helper()
			claims := f.keys.claims()
			claims["email"] = testStrangerEmail
			return f.keys.mint(t, claims)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f := newFleet(t)
			assertion := tc.assertion(t, f)

			w := f.openWith(t, settingsPath, assertion)

			if w.Code != http.StatusUnauthorized {
				t.Fatalf("GET %s with %s was answered %d (%s); want %d — the door's uniform refusal",
					settingsPath, tc.name, w.Code, w.Body.String(), http.StatusUnauthorized)
			}
			if got, want := w.Body.String(), string(bodyBrowserRefused); got != want {
				t.Errorf("GET %s with %s answered\n%s\nwant the door's one refusal\n%s", settingsPath, tc.name, got, want)
			}

			// The same credential at the fleet, on the same daemon: what this door
			// really refuses with, rather than this test's idea of it.
			elsewhere := f.openWith(t, "/", assertion)

			if w.Code != elsewhere.Code {
				t.Errorf("%s was refused %d at %s and %d at the fleet — the two are distinguishable",
					tc.name, w.Code, settingsPath, elsewhere.Code)
			}
			if got, want := w.Body.String(), elsewhere.Body.String(); got != want {
				t.Errorf("%s was refused at %s with\n%s\nand at the fleet with\n%s\nthe two are distinguishable",
					tc.name, settingsPath, got, want)
			}
			if !maps.EqualFunc(w.Header(), elsewhere.Header(), slices.Equal) {
				t.Errorf("%s was refused at %s with headers %v and at the fleet with %v — the two are distinguishable",
					tc.name, settingsPath, w.Header(), elsewhere.Header())
			}

			// One record per request (FR-041), both of them layer 1's own refusal.
			// A route registered without the middleware emits neither.
			got := f.records(t)
			if len(got) != 2 {
				t.Fatalf("two refused requests emitted %d audit records (%v); FR-041 requires exactly one each", len(got), got)
			}
			for _, rec := range got {
				if want := string(audit.ActionAccessReject); rec["action"] != want {
					t.Errorf("a refused request was recorded as %v; want %q", rec["action"], want)
				}
				if want := string(audit.Deny); rec["decision"] != want {
					t.Errorf("a refused request was recorded as %v; want %q", rec["decision"], want)
				}
			}
		})
	}
}

// TestSettingsEmitsExactlyOneAuditRecord is the contract's second row: a served
// settings page is one record under its own action, never one per configured
// value and never none at all.
//
// The action is its own rather than dashboard.view because this is the page that
// composes every configured value at render time, and an operator counting who
// read the configuration must not be counting fleet loads with them.
//
// **Must fail when** the handler audits per row, or not at all — and when the
// route is registered outside the identity middleware, which is what this half of
// the pair adds to the refusals above: that mutation cannot serve the page at
// all, because the handler fails closed on an operator the context does not hold.
func TestSettingsEmitsExactlyOneAuditRecord(t *testing.T) {
	t.Parallel()

	f := newFleet(t)

	w := f.open(t, settingsPath)
	if w.Code != http.StatusOK {
		t.Fatalf("GET %s = %d (%s); want %d", settingsPath, w.Code, w.Body.String(), http.StatusOK)
	}

	rec := f.only(t)
	if got, want := rec["action"], string(audit.ActionSettingsView); got != want {
		t.Errorf("the settings page was recorded as %v; want %q", got, want)
	}
	if got, want := rec["decision"], string(audit.Allow); got != want {
		t.Errorf("a served settings page was recorded as %v; want %q", got, want)
	}
	// The server-derived owner, which is what makes the record comparable with
	// every other one this daemon writes — never the verified address, which is a
	// claim value and stays out of the trail (FR-042).
	if got, want := rec["caller"], string(auth.CallerOperator); got != want {
		t.Errorf("the settings page was recorded for caller %v; want %q", got, want)
	}
	// This page is about the daemon and not about one session, so the field
	// data-model.md carries on the single-session view alone is absent here.
	if id, carried := rec["session_id"]; carried {
		t.Errorf("the settings page's record carries session_id %v; the page names no session", id)
	}

	// The page really was rendered for the identity layer 1 verified, rather than
	// answered by something that emitted the record and served nothing.
	if got, want := w.Header().Get(headerContentType), contentTypeHTML; got != want {
		t.Errorf("the settings page declared itself %q; want %q", got, want)
	}
	if page := w.Body.String(); !strings.Contains(page, testOperatorEmail) {
		t.Errorf("the settings page does not name the verified operator:\n%s", page)
	}
}

// TestNoMutatingVerbRegistered is the contract's "no mutating verb" row, and the
// safeguard the spec's Out of Scope section rests on: editing the operator's
// configuration file from a browser is not in this milestone, and the absence of
// a route is what makes that structural rather than a refusal somebody has to
// keep writing.
//
// It asserts the unknown-route answer and **not** the 405 the contract's table
// asks for, which is a deliberate departure from that literal. Milestone 3's
// FR-033 forbids a new route weakening the uniform response, and this repo has
// settled the same question four times already — on the destroy, rename, compact
// and fleet-stream paths — each time the same way: a method a path does not serve
// is a path nothing claims. A 405 says something *is* served here and an Allow
// header names what, which is the route table handed to whoever asks for it. The
// router could not produce one anyway: handleUnrouted's `/` catch-all matches
// every method, so ServeMux never reaches its own 405 branch.
//
// The two responses are compared whole rather than each being asserted a 404,
// because "answered exactly as any other unknown route is" is the claim.
//
// **Must fail when** an edit route is added. A POST registered on this path
// answers with something — a refusal, a redirect, a 500 — and none of them is the
// answer a path nothing claims gives.
func TestNoMutatingVerbRegistered(t *testing.T) {
	t.Parallel()

	for _, method := range []string{
		http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete,
	} {
		t.Run(strings.ToLower(method), func(t *testing.T) {
			t.Parallel()

			f := newFleet(t)
			w := f.ask(t, method, settingsPath)

			if w.Code == http.StatusMethodNotAllowed {
				t.Fatalf("%s %s was answered %d with %s: %q — which method a path serves is not a caller's to learn",
					method, settingsPath, w.Code, headerAllow, w.Header().Get(headerAllow))
			}
			if w.Code != http.StatusNotFound {
				t.Fatalf("%s %s was answered %d (%s); want %d — the unknown-route answer",
					method, settingsPath, w.Code, w.Body.String(), http.StatusNotFound)
			}
			if got := w.Header().Get(headerAllow); got != "" {
				t.Errorf("%s %s answered with %s: %q; want no such header", method, settingsPath, headerAllow, got)
			}

			// The same method at a path nothing claims, on the same daemon and the
			// same identity: what an unknown route really answers here.
			nowhere := f.ask(t, method, settingsPath+"-nonesuch")

			if w.Code != nowhere.Code {
				t.Errorf("%s %s answered %d; at a path nothing claims it answered %d — the two are distinguishable",
					method, settingsPath, w.Code, nowhere.Code)
			}
			if got, want := w.Body.String(), nowhere.Body.String(); got != want {
				t.Errorf("%s %s answered\n%s\nat a path nothing claims it answered\n%s\nthe two are distinguishable",
					method, settingsPath, got, want)
			}
			if !maps.EqualFunc(w.Header(), nowhere.Header(), slices.Equal) {
				t.Errorf("%s %s answered with headers %v; at a path nothing claims %v — the two are distinguishable",
					method, settingsPath, w.Header(), nowhere.Header())
			}

			// One record each, in the trail's existing vocabulary for a request that
			// reached no route — and never settings.view, which would say the page
			// was served to a verb that has no handler.
			got := f.records(t)
			if len(got) != 2 {
				t.Fatalf("two requests emitted %d audit records (%v); FR-041 requires exactly one each", len(got), got)
			}
			for _, rec := range got {
				if want := string(audit.ActionUnknownRoute); rec["action"] != want {
					t.Errorf("%s on a path with no such route was recorded as %v; want %q", method, rec["action"], want)
				}
			}
		})
	}
}

// The two secret values these tests configure, and both are deliberately
// gibberish.
//
// A canary made of words would make the sweeps below unreliable in the direction
// that matters: "secret" or "here" turning up in a four-character window of the
// page is a coincidence with English prose, and a test that has to be argued with
// is a test that gets loosened. Nothing in this repository's markup, tokens or
// copy carries a run of these.
//
// Neither address is the viewer's. The fixture Config's allowlist is the
// operator's own address and the header renders that on every page in the
// product, so a page-wide sweep for "the allowlisted address" against the default
// fixture would find the identity layer 1 verified — a different thing wearing
// one string — and fail on entirely correct code.
//
// Both carry the `test-only-` prefix .gitleaks.toml allows by construction — the
// prefix is the claim, and a scanner that had to be argued with about a
// secret-shaped string beside the word "secret" is one that stops being run. The
// sweeps below therefore search for runs of the *body*, which is the part with
// entropy in it: the announcement is not secret material, and a page that printed
// it alone would have disclosed nothing. It also cannot be swept for safely — the
// fixture Config's Access audience is `test-only-audience-tag`, so a search for
// "test" would find that the moment T012 renders the value column.
//
// gosec G101 fires on canarySecret for the reason it fires on EnvSharedSecret in
// config.go: the identifier says "secret" and the value is a string literal. This
// one is a fixture, and the daemon this package builds authenticates with
// testSecret and never with this.
const (
	canaryAnnounce = "test-only-"
	canarySecret   = canaryAnnounce + "qx7v-zk2m-vb9n-ct4r-ls8w-pd3h-gj6f-wn5y-rt1u-mb0e" //nolint:gosec // G101: a canary a test sweeps for, not a credential
	canaryAllowed  = canaryAnnounce + "zq8t-kd4p-mn7v@vb6n-xw3r.hj9c"
)

// shortestRunWorthHaving is where a run of a secret starts being a disclosure.
// Four, because three characters of a credential is not a run and a page of
// markup carries plenty of them by accident.
const shortestRunWorthHaving = 4

// leakedRun reports the first run of a canary's entropy that appears in text,
// and which end of the value it was cut from.
//
// It is one function because it is the search both secret sweeps in this file
// make — of one page, and of every response the daemon has — and two copies of
// it would be two answers to "what counts as a disclosure", free to be loosened
// one at a time. That is T001's argument about IsSecret, made about the test
// that checks it.
//
// The *body* is searched rather than the whole value, for the reason
// canaryAnnounce gives: the `test-only-` prefix is an announcement to the secret
// scanner and not secret material, and a page that printed it alone would have
// disclosed nothing. Any masked form long enough to give away four characters of
// the entropy contains a run this finds, whichever end it was cut from.
func leakedRun(text, secret string) (run, cut string) {
	body := strings.TrimPrefix(secret, canaryAnnounce)
	for n := shortestRunWorthHaving; n < len(body); n++ {
		if prefix := body[:n]; strings.Contains(text, prefix) {
			return prefix, "the first"
		}
		if suffix := body[len(body)-n:]; strings.Contains(text, suffix) {
			return suffix, "the last"
		}
	}
	return "", ""
}

// settingsOn is a fleet whose Config the caller has adjusted before anything has
// read it, which is how every claim below is made about a configured daemon
// rather than about the fixture's defaults.
//
// The Config is adjusted after construction, in the shape watchingUnserved uses
// for the stream cap: no request has been served yet, the fixture's Config is its
// own rather than shared, and layer 1 here is a real validator built over the key
// server's allowlist and not over this value — so changing the daemon's copy of
// the allowlist changes what the page describes and not who may read it, which is
// exactly the distinction these tests are about.
func settingsOn(t *testing.T, adjust func(*config.Config)) *fleet {
	t.Helper()

	f := newFleet(t)
	adjust(f.cfg)
	return f
}

// settingsBody opens the settings page as the verified operator and hands back
// what a browser would receive.
// settingsEverySection is the whole page as an operator sees it across the menu.
//
// The page shows one section at a time (#103), so a test asking "does this page
// mention every key" has to walk the menu the way somebody would. Concatenated
// rather than merged, because what is being asserted is reachability: a key in
// no section, or a section the menu does not link, is a setting nobody can get
// to, and both look identical to a test that only ever loads the default page.
func settingsEverySection(t *testing.T, f *fleet) string {
	t.Helper()

	var all strings.Builder
	all.WriteString(settingsSectionBody(t, f, sectionUpdates))
	for _, section := range sectioned(settingsOf(testConfig(loopbackListen))) {
		all.WriteString(settingsSectionBody(t, f, section.Title))
	}
	return all.String()
}

// settingsChecked is the Updates section after the operator has pressed Check.
//
// The feed is not consulted on an ordinary render (#103's sequel): looking costs
// a request to somebody else's API, and this page's first job is reporting local
// configuration. So a test about what the check found has to ask for it, exactly
// as an operator does.
func settingsChecked(t *testing.T, f *fleet) string {
	t.Helper()

	w := f.open(t, settingsPath+"?"+querySection+"="+sectionUpdates+"&"+queryCheck+"=1")
	if w.Code != http.StatusOK {
		t.Fatalf("GET the checked Updates section = %d (%s); want %d", w.Code, w.Body.String(), http.StatusOK)
	}
	return w.Body.String()
}

// settingsSectionBody is one section as the menu reaches it.
func settingsSectionBody(t *testing.T, f *fleet, section string) string {
	t.Helper()

	w := f.open(t, settingsPath+"?"+querySection+"="+url.QueryEscape(section))
	if w.Code != http.StatusOK {
		t.Fatalf("GET %s section %q = %d (%s); want %d", settingsPath, section, w.Code, w.Body.String(), http.StatusOK)
	}
	return w.Body.String()
}

// settingsBody is what the settings page says, which since #103 means what it
// says across its menu rather than what one request returns.
//
// The page shows one section at a time, so a test asking "does this page mention
// X" has to walk the way an operator would. Every existing assertion kept its
// meaning through that change; what it costs is that a test wanting the literal
// default page asks for settingsDefaultBody instead, and there is exactly one.
func settingsBody(t *testing.T, f *fleet) string {
	t.Helper()

	return settingsEverySection(t, f)
}

// settingsDefaultBody is the one request an operator's first click makes.
func settingsDefaultBody(t *testing.T, f *fleet) string {
	t.Helper()

	w := f.open(t, settingsPath)
	if w.Code != http.StatusOK {
		t.Fatalf("GET %s = %d (%s); want %d", settingsPath, w.Code, w.Body.String(), http.StatusOK)
	}
	return w.Body.String()
}

// settingsRowFor isolates the row naming one key, so an assertion about what the
// page says *about a setting* cannot be satisfied by markup elsewhere on it —
// the same reason cardFor exists.
func settingsRowFor(t *testing.T, page, key string) string {
	t.Helper()

	for _, row := range strings.Split(page, "<tr>")[1:] {
		body, _, _ := strings.Cut(row, "</tr>")
		if strings.Contains(body, ">"+key+"<") {
			return body
		}
	}
	t.Fatalf("the settings page has no row for %q, so it says nothing at all about that setting:\n%s", key, page)
	return ""
}

// TestSettingsNeverRendersSecretValue is contracts/settings-page.md's secret row
// and SC-005 at the one page that holds every secret at render time.
//
// It sweeps for more than the value. A prefix, a suffix and a length are each a
// disclosure of their own — four characters a page is willing to print are four
// an attacker no longer has to guess, and a length turns a search space into a
// smaller one — so what is asserted is that no run of either secret longer than
// three characters reaches the browser, in either direction, and that neither
// length appears as a number.
//
// **Must fail when** a "masked" value like `qx7v…` is introduced. That is the
// mutation this exists for: it looks like a courtesy, it passes a test that
// searched for the whole value, and it is a disclosure.
// entities matches a numeric character reference, whose digits are an encoding
// artefact rather than anything the page is saying.
var entities = regexp.MustCompile(`&#[0-9]+;`)

func TestSettingsNeverRendersSecretValue(t *testing.T) {
	t.Parallel()

	f := settingsOn(t, func(cfg *config.Config) {
		cfg.SharedSecret = []byte(canarySecret)
		cfg.AccessAllowedEmails = []string{canaryAllowed}
	})
	page := settingsBody(t, f)

	for _, secret := range []struct {
		what  string
		value string
	}{
		{"the shared secret", canarySecret},
		{"the allowlisted addresses", canaryAllowed},
	} {
		if strings.Contains(page, secret.value) {
			t.Errorf("the settings page renders %s verbatim:\n%s", secret.what, page)
		}
		if run, cut := leakedRun(page, secret.value); run != "" {
			t.Errorf("the settings page carries %q, %s %d characters of %s; a masked value is still a disclosure", run, cut, len(run), secret.what)
		}
		// The length, which is what "shorter than the required 32 bytes" is
		// careful never to measure in a startup error either (loadSecret).
		length := regexp.MustCompile(`\b` + strconv.Itoa(len(secret.value)) + `\b`)
		// Numeric character references are stripped first: `&#39;` is an
		// apostrophe, and a page carrying prose will carry entities whose digits
		// have nothing to do with any secret. Matching them was a false positive
		// that arrived the moment this page rendered text somebody else wrote.
		if match := length.FindString(entities.ReplaceAllString(page, " ")); match != "" {
			t.Errorf("the settings page carries %q, which is the length of %s", match, secret.what)
		}
	}
}

// TestSecretRendersPresentOrAbsent is the value column's whole vocabulary, held
// for both secret keys in both states.
//
// The unset half is not symmetry for its own sake. It is the page an operator
// reads when a credential is missing, and it has to say so in the same column and
// the same two words — a blank cell there reads as a page that failed to render
// rather than as a daemon with no secret configured (FR-018a).
//
// **Must fail when** the template prints the raw value for either secret key, and
// when either word is composed from the value rather than chosen from the fact.
func TestSecretRendersPresentOrAbsent(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name   string
		key    string
		adjust func(*config.Config)
		want   string
	}{
		{"a configured shared secret", "shared_secret", func(cfg *config.Config) {
			cfg.SharedSecret = []byte(canarySecret)
		}, secretPresent},
		{"no shared secret at all", "shared_secret", func(cfg *config.Config) {
			cfg.SharedSecret = nil
		}, secretAbsent},
		{"a configured allowlist", "access_allowed_emails", func(cfg *config.Config) {
			cfg.AccessAllowedEmails = []string{canaryAllowed}
		}, secretPresent},
		{"an empty allowlist", "access_allowed_emails", func(cfg *config.Config) {
			cfg.AccessAllowedEmails = nil
		}, secretAbsent},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			page := settingsBody(t, settingsOn(t, tc.adjust))
			row := settingsRowFor(t, page, tc.key)

			if !strings.Contains(row, "<td>"+tc.want+"</td>") {
				t.Errorf("with %s, the %s row is %q; want a value cell holding exactly %q", tc.name, tc.key, row, tc.want)
			}
			other := secretPresent
			if tc.want == secretPresent {
				other = secretAbsent
			}
			if strings.Contains(row, other) {
				t.Errorf("with %s, the %s row also says %q: %q", tc.name, tc.key, other, row)
			}
		})
	}
}

// TestAllowedIdentitiesTreatedAsSecret is the second key, and the one a reader
// has to be told about: it is not a credential and it authenticates nobody.
//
// It is secret because it names *who* may reach a daemon that runs unsandboxed
// code on this host, which is worth exactly as little publication as the secret
// that authenticates them — and because one predicate deciding for both is what
// makes the permission refusal in internal/config and this page unable to
// disagree (T001).
//
// **Must fail when** only `shared_secret` is classified. That mutation prints no
// address on today's page — it drops the row, because settingsOf renders the keys
// config.IsSecret names — so the row lookup is what catches it here, and the
// address sweep is what will catch it once T012 fills the value column for every
// other key.
func TestAllowedIdentitiesTreatedAsSecret(t *testing.T) {
	t.Parallel()

	const second = "wt3k-fp9r@nq5d-bz7l.mv2x"
	page := settingsBody(t, settingsOn(t, func(cfg *config.Config) {
		cfg.AccessAllowedEmails = []string{canaryAllowed, second}
	}))

	row := settingsRowFor(t, page, "access_allowed_emails")
	if !strings.Contains(row, "<td>"+secretPresent+"</td>") {
		t.Errorf("the allowlist row is %q; a configured allowlist is %q", row, secretPresent)
	}
	for _, address := range []string{canaryAllowed, second} {
		if strings.Contains(page, address) {
			t.Errorf("the settings page names the allowlisted address %q; the list says who may reach an unsandboxed shell on this host, and it is published nowhere", address)
		}
	}
	// Nor how many of them there are. A count is the same disclosure made
	// smaller, and it is a shape Config.String settles on for a *log* line —
	// where the alternative is naming them — rather than a licence for a page
	// that has the two words it needs.
	if strings.Contains(row, strconv.Itoa(2)) {
		t.Errorf("the allowlist row is %q; it carries how many addresses are allowlisted", row)
	}
}

// TestEverySecretKeyReportsItsPresence closes the drift secretConfigured's second
// return value exists for.
//
// config.IsSecret is the classifier, so a third secret key added there is kept
// out of the value column whether or not this package has heard of it — that half
// is safe by construction. What is not safe is the sentence the page then writes
// about it: a key nothing here can ask about reads as absent forever, which is
// the settings page lying about a configured credential rather than leaking one.
//
// **Must fail when** a key joins config.IsSecret and nothing here learns how to
// tell whether it is set.
func TestEverySecretKeyReportsItsPresence(t *testing.T) {
	t.Parallel()

	secrets := 0
	for _, name := range config.Vars() {
		key := config.KeyForVar(name)
		if !config.IsSecret(key) {
			continue
		}
		secrets++
		if _, known := secretConfigured(testConfig(loopbackListen), name); !known {
			t.Errorf("config.IsSecret calls %s secret and the settings page cannot tell whether it is configured, so it will report %q for a value that is set", key, secretAbsent)
		}
	}
	if secrets == 0 {
		t.Fatal("no key in config.Vars() is secret, so this test asserted nothing — and the page has no secret to protect")
	}

	// And every one of them is stated in those two words on the page, which is
	// the half that has to be counted now that every key has a row: a secret
	// whose cell held a value would still have a row, so the assertion is about
	// the vocabulary rather than about the number of rows. A classifier nothing
	// consults is the failure this milestone has shipped three times: the code
	// exists and nothing calls it.
	rows := settingsOf(testConfig(loopbackListen))
	stated := 0
	for _, row := range rows {
		if row.Value == secretPresent || row.Value == secretAbsent {
			stated++
		}
	}
	if stated != secrets {
		t.Errorf("the settings page states presence for %d keys and config.IsSecret names %d (%v)", stated, secrets, rows)
	}
}

// TestEverySettingRendersAValue is the value column's half of the same drift
// guard, and it is the direction that fails silently: a variable added to
// config.go and to config.Vars() gets a row on this page whether or not anything
// here knows how to state it, so the failure is a blank cell on a page an
// operator is reading to find out what this daemon is configured to do.
//
// Blank rather than wrong is deliberate — see settingValue — but it is still a
// setting the page cannot describe, and the fix is one case in that switch.
//
// **Must fail when** config.Vars() grows a variable settingValue has no case
// for. It says which one, because that is the whole of the fix.
func TestEverySettingRendersAValue(t *testing.T) {
	t.Parallel()

	cfg := testConfig(loopbackListen)
	stated := 0
	for _, name := range config.Vars() {
		if config.IsSecret(config.KeyForVar(name)) {
			continue
		}
		stated++
		if _, known := settingValue(cfg, name); !known {
			t.Errorf("config.Vars() names %s and the settings page has no way to state its value, so its row renders an empty cell", name)
		}
	}
	if stated == 0 {
		t.Fatal("every key in config.Vars() is secret, so this test asserted nothing")
	}
}

// TestSettingsRendersOneRowPerKey is the contract's first sentence about the
// table: one row per configuration key, in the order config.go declares them.
//
// The order is asserted rather than the set, because the two failures are
// different and only one of them is visible to a reader. A missing key is a
// setting the page silently does not mention; a reordered table is a page that
// no longer matches config.example, the file it exists to be compared against.
func TestSettingsRendersOneRowPerKey(t *testing.T) {
	t.Parallel()

	f := newFleet(t)
	page := settingsBody(t, f)

	// The page shows one section at a time now, so this walks the menu the way an
	// operator would and requires the union to cover every key.
	//
	// That is a better assertion than the flat page's was. A key that no section
	// claims, or a menu entry that leads nowhere, both leave a setting an
	// operator cannot reach — and the flat table could not tell those apart from
	// working, because everything was on one page regardless of the menu.
	seen := map[string]int{}
	for _, section := range sectioned(settingsOf(testConfig(loopbackListen))) {
		body := settingsSectionBody(t, f, section.Title)
		for _, name := range config.Vars() {
			key := config.KeyForVar(name)
			seen[key] += strings.Count(body, ">"+key+"<")
		}
	}
	for _, name := range config.Vars() {
		key := config.KeyForVar(name)
		if seen[key] != 1 {
			t.Errorf("walking every section reaches %s %d times; each key belongs to exactly one section and must be reachable from the menu", key, seen[key])
		}
	}
	_ = page
	if rows := settingsOf(testConfig(loopbackListen)); len(rows) != len(config.Vars()) {
		t.Errorf("the settings page renders %d rows for %d configuration keys", len(rows), len(config.Vars()))
	}
}

// TestShowsSourcePerKey is contracts/settings-page.md's source row, and every
// case below is built so that the value alone cannot produce the right answer.
//
// That is the point of the column. A source *inferred* at render time can only
// compare the value against the built-in default, which is right about the two
// uninteresting cases and wrong about both of the ones an operator is on this
// page to ask about: a file that set a value the daemon would have defaulted to
// anyway, and a value that differs from the default because something *else*
// supplied it. So the fixture holds the default value under a file source and a
// non-default value under a default source — a comparison would get both
// backwards, and reading the shim's record gets both right.
//
// **Must fail when** the source is inferred at render time instead of read from
// the map the precedence shim wrote (T008).
func TestShowsSourcePerKey(t *testing.T) {
	t.Parallel()

	f := settingsOn(t, func(cfg *config.Config) {
		// The value a daemon with no configuration at all would have. An
		// inference says "default"; the record says an operator wrote it.
		cfg.Listen = config.DefaultListen
		// The reverse: nothing supplied it, and it is not the built-in default
		// either — which is every daemon whose ceiling was derived from another
		// setting rather than configured (loadWith defaults the MAX pair to the
		// pair below them).
		cfg.MaxStreams = config.DefaultMaxStreams + 3
		cfg.MaxSessions = 3
		cfg.Sources = map[string]config.Source{
			config.EnvListen:      config.SourceFile,
			config.EnvMaxStreams:  config.SourceDefault,
			config.EnvMaxSessions: config.SourceEnv,
		}
	})
	page := settingsBody(t, f)

	for _, tc := range []struct {
		key  string
		want config.Source
		why  string
	}{
		{"listen", config.SourceFile, "the file set it to the value it would have defaulted to; nothing about the value says so"},
		{"max_streams", config.SourceDefault, "nothing supplied it, and it is not the value a comparison would call the default"},
		{"max_sessions", config.SourceEnv, "the environment answered this lookup"},
		{"idle_timeout", config.SourceDefault, "no lookup for it was ever recorded, which is the zero value's whole job"},
	} {
		row := settingsRowFor(t, page, tc.key)
		if !strings.Contains(row, "<td>"+tc.want.String()+"</td>") {
			t.Errorf("the %s row is %q; want a source cell holding exactly %q — %s", tc.key, row, tc.want, tc.why)
		}
	}
}

// TestNamesConfigFileRead is FR-018's first half. The path appears verbatim,
// because an operator comparing this page against the file they just edited is
// checking that the two are the same file — and a page that named it in prose
// ("your configuration file") would answer a question nobody has.
//
// **Must fail when** the page omits it.
func TestNamesConfigFileRead(t *testing.T) {
	t.Parallel()

	// Not the path this host's daemon would read: a fixture that agreed with
	// config.DefaultPath would pass against a page that had worked the path out
	// for itself, which is the mutation this and the Config field exist to stop.
	const read = "/nonexistent-crswd-test-home/.config/crswd/config"

	page := settingsBody(t, settingsOn(t, func(cfg *config.Config) { cfg.FilePath = read }))

	if !strings.Contains(page, "Read from "+read) {
		t.Errorf("the settings page does not name %s as the file it was configured from:\n%s", read, page)
	}
	if strings.Contains(page, noFileRead) {
		t.Errorf("the settings page says %q while naming a file it read", noFileRead)
	}
}

// noFileRead is the sentence a daemon configured entirely by its environment
// says, spelled here as contracts/settings-page.md spells it — the full stop
// included, because it is a sentence and the path above it is not.
const noFileRead = "No configuration file was read."

// TestSaysWhenNoFileRead is FR-018's other half, and it is the deployment every
// daemon before this milestone was: no configuration file, every value from the
// environment or a default.
//
// The sentence is required rather than the line being empty. A blank space above
// the table reads as a page that failed to render one, and the operator's next
// move is to go looking for the file it did not name — which is the search this
// line exists to end.
//
// **Must fail when** the line is blank instead.
func TestSaysWhenNoFileRead(t *testing.T) {
	t.Parallel()

	page := settingsBody(t, settingsOn(t, func(cfg *config.Config) { cfg.FilePath = "" }))

	if !strings.Contains(page, noFileRead) {
		t.Errorf("the settings page does not say that no configuration file was read:\n%s", page)
	}
	if strings.Contains(page, "Read from") {
		t.Errorf("the settings page says it read from something while no file was read:\n%s", page)
	}
}

// TestSettingsStatesTheValueOfEveryNonSecretKey is the value column at the two
// keys a reader would most expect to be missing from it, and the pair the two
// halves of this page are decided by.
//
// allowed_roots is SC-004: an operator whose working directory was refused reads
// this cell and sees immediately whether theirs is under one of them, so the
// paths are spelled out rather than counted.
//
// start_commands is the decision this task had to make, and it is worth a
// reviewer's eye. The command lines are not secret by config.IsSecret, they are
// the operator's own configuration, and the identity reading them is the identity
// that may start a session running them. Config.String names them without
// spelling them — but that is a rule for log lines, and applying it here would be
// a second redaction rule outside IsSecret, which is exactly what T001 exists to
// prevent.
func TestSettingsStatesTheValueOfEveryNonSecretKey(t *testing.T) {
	t.Parallel()

	const (
		firstRoot  = "/nonexistent-crswd-test-root"
		secondRoot = "/nonexistent-crswd-test-work"
		rcCommand  = "claude remote-control --name {name}"
	)

	page := settingsBody(t, settingsOn(t, func(cfg *config.Config) {
		cfg.Roots = []config.ApprovedRoot{{Path: firstRoot}, {Path: secondRoot}}
		cfg.StartCommands = config.NewStartCommands(map[string]string{
			config.DefaultStartCommandName: config.DefaultStartCommand,
			"rc":                           rcCommand,
		})
		cfg.RemoteControlCommand = "rc"
		cfg.MaxSessions = 4
	}))

	for _, tc := range []struct {
		key  string
		want string
	}{
		{"allowed_roots", firstRoot + ", " + secondRoot},
		{"listen", loopbackListen},
		{"max_sessions", "4"},
		{"start_command", config.DefaultStartCommand},
		{"start_commands", config.DefaultStartCommandName + "=" + config.DefaultStartCommand + "," + "rc=" + rcCommand},
		{"remote_control_command", "rc"},
	} {
		row := settingsRowFor(t, page, tc.key)
		// Escaped as the template escapes it, so the assertion is about the value
		// the browser renders rather than about the bytes on the wire.
		// Either shape states the value: a plain cell for a key this page will
		// not write, and an input carrying it for one it will. What is being
		// asserted is that the page says what the setting currently holds — the
		// editing arrived after this test and changed the markup, not the claim.
		escaped := html.EscapeString(tc.want)
		stated := strings.Contains(row, "<td>"+escaped+"</td>") ||
			strings.Contains(row, `value="`+escaped+`"`)
		if !stated {
			t.Errorf("the %s row is %q; want it to state the value %q, in a cell or in a field", tc.key, row, tc.want)
		}
	}
}

// --- T013: the whole route table, searched (SC-005) --------------------------
//
// TestSettingsNeverRendersSecretValue holds the one page that composes every
// secret at render time. SC-005 is the requirement that page cannot satisfy on
// its own: no secret value appears in *any* response or *any* record — which is
// a claim nobody can make by reading the handler they happen to be editing, and
// which fails silently, since a leak on some other route breaks no test anybody
// wrote for that route.
//
// So the sweep below exercises every route this daemon registers — the API's six
// operations, the pages, the two embedded assets, the two streams, the four
// actions, the paths nothing claims, and each door's refusals — and searches
// everything that came back, together with the whole audit trail, for either
// configured secret.

// sweptAnswer is one exercised route: what was asked for, and everything the
// daemon answered with.
//
// The headers are kept beside the body because a response is both. A value that
// reached a caller through a Location, an entity tag or a header some later
// middleware adds is disclosed exactly as far as one printed in the markup, and
// a search that read only bodies would be looking at the half nobody has ever
// leaked a secret through by accident.
type sweptAnswer struct {
	what string
	text string
}

// sweep is one daemon with both secrets configured, its whole route table
// driven, and everything it answered kept for a single search.
type sweep struct {
	*fleet

	answers []sweptAnswer

	// router carries this daemon's own patterns on a mux of the test's own, so
	// that each request below can be asked which route it reached.
	//
	// net/http will not enumerate a ServeMux, so the daemon's registrations
	// cannot be read back off it and a table like registeredPatterns' is the
	// nearest thing to evidence there is. What it buys is real all the same: it
	// catches the likelier mistake by far — a target in the sweep that quietly
	// falls to the catch-all instead of the route it names, which would leave a
	// route swept in name only.
	router *http.ServeMux

	// reached is every pattern registeredPatterns names, against whether some
	// request below actually drove it.
	reached map[string]bool
}

// registeredPatterns is every pattern newServer hands the mux.
//
// The API's six and their method-less twins are read off the contract's own
// table, and the assets off the embedded tree, so neither can drift from what
// the daemon registers. The browser door's eleven are listed here because there
// is nowhere to read them from — but they are the *constants* newServer
// registers, so a renamed route moves this sweep with it. A twelfth would have to
// be added here by hand, and that is the one gap this arrangement cannot close.
// The version route was the eleventh, and it was added here by hand exactly as
// this comment says it would have to be.
func registeredPatterns(t *testing.T) []string {
	t.Helper()

	patterns := []string{
		patternFleet,
		patternSessionView,
		patternSessionStream,
		patternFleetStream,
		patternSettings,
		patternVersion,
		patternDashboardCreate,
		patternDashboardDestroy,
		patternDashboardRename,
		patternDashboardCompact,
		patternDashboardMode,
		// handleUnrouted's catch-all, which is a registered route like any other:
		// it is what answers a path nothing claims, from behind the browser door.
		"/",
	}
	// Each operation, and the method-less pattern handleUnrouted registers beside
	// it so that a wrong method on a contract path is answered as a path nothing
	// claims rather than as a 405 naming the route table.
	for _, r := range routes {
		patterns = append(patterns, r.String(), r.Pattern)
	}
	for _, a := range sweptAssets(t) {
		patterns = append(patterns, a.pattern)
	}
	return patterns
}

// sweptAssets is the embedded asset tree resolved into the routes newServer
// registers for it, so a third file added under web/static/ is swept without
// anybody remembering to add it here.
func sweptAssets(t *testing.T) []asset {
	t.Helper()

	assets, err := loadAssets(web.Static)
	if err != nil {
		t.Fatalf("loadAssets(web.Static) = _, %v; want the routes this daemon serves its assets on", err)
	}
	return assets
}

// newSweep is the daemon this sweep drives: the fleet's, with both secret
// settings configured to values that appear nowhere else in this repository.
//
// The Config is adjusted after construction, in settingsOn's shape, and that is
// worth stating plainly because it means the Authenticator behind the API door
// is holding testSecret rather than the canary. It does not weaken the claim:
// auth.NewWithClock copies the key into an unexported field and hands it back
// through no method, so cfg.SharedSecret is the only copy of it a handler can
// reach at all. `s.cfg` is read in exactly four places — the two body limits,
// the create form's command names, and the settings page — and every one of them
// is driven below. The key layer 2 really checks against is swept for as well,
// at the foot of the test, so a response that echoed *that* would still be found.
func newSweep(t *testing.T) *sweep {
	t.Helper()

	s := &sweep{
		fleet: settingsOn(t, func(cfg *config.Config) {
			cfg.SharedSecret = []byte(canarySecret)
			cfg.AccessAllowedEmails = []string{canaryAllowed}
		}),
		router:  http.NewServeMux(),
		reached: map[string]bool{},
	}
	for _, pattern := range registeredPatterns(t) {
		// routes names two of its paths twice over, and a mux panics on the
		// second registration of a pattern — which is why handleUnrouted dedupes
		// too.
		if _, registered := s.reached[pattern]; registered {
			continue
		}
		s.reached[pattern] = false
		s.router.HandleFunc(pattern, func(http.ResponseWriter, *http.Request) {})
	}
	return s
}

// drive serves one request, notes which registered route it reached, and keeps
// what came back.
func (s *sweep) drive(t *testing.T, what string, r *http.Request) *httptest.ResponseRecorder {
	t.Helper()

	if _, pattern := s.router.Handler(r); pattern != "" {
		s.reached[pattern] = true
	}

	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)

	answer := &strings.Builder{}
	for _, name := range slices.Sorted(maps.Keys(w.Header())) {
		answer.WriteString(name + ": " + strings.Join(w.Header()[name], ", ") + "\n")
	}
	answer.WriteString(w.Body.String())

	s.answers = append(s.answers, sweptAnswer{what: what, text: answer.String()})
	return w
}

// signed builds the API door's request for one route: the contract's own path,
// the body that route takes, a signature over both, and — for a route naming a
// session — the credential issued for it.
func (s *sweep) signed(t *testing.T, route Route, id, credential string, at time.Time) *http.Request {
	t.Helper()

	body := bodyFor(s.fixture, route)
	r := httptest.NewRequest(route.Method, strings.ReplaceAll(route.Pattern, "{"+pathValueID+"}", id), bytes.NewReader(body))
	signRequest(t, r, body, at)
	if credential != "" {
		// After signing, because the signature covers the timestamp and the body
		// and nothing else — layer 3 is a separate credential, not part of one.
		r.Header.Set(headerAuthorization, bearerScheme+credential)
	}
	return r
}

// asOperator builds the browser door's request as the verified operator's
// browser makes it: the identity assertion, the fetch-metadata header a real
// navigation carries, a form when there is one, and no signature anywhere.
//
// Sec-Fetch-Site goes on every row rather than on the four that need it, because
// a browser sends it on every navigation and the reads ignore it. A sweep whose
// reads and writes differed in a header would be two sweeps.
func (s *sweep) asOperator(t *testing.T, method, target string, form url.Values) *http.Request {
	t.Helper()

	var r *http.Request
	if form == nil {
		r = httptest.NewRequest(method, target, nil)
	} else {
		r = httptest.NewRequest(method, target, strings.NewReader(form.Encode()))
		r.Header.Set(headerContentType, contentTypeForm)
	}
	r.Header.Set(headerAccessAssertion, s.keys.mint(t, s.keys.claims()))
	r.Header.Set(headerSecFetchSite, secFetchSiteSameOrigin)
	return r
}

// answerFor is what one named request came back with, so a claim about a single
// response can be made of the same bytes the search below reads.
func (s *sweep) answerFor(t *testing.T, what string) string {
	t.Helper()

	for _, answer := range s.answers {
		if answer.what == what {
			return answer.text
		}
	}
	t.Fatalf("the sweep kept no answer for %q, so nothing it drove was that request", what)
	return ""
}

// TestFullRouteSweepLeaksNoSecret is SC-005, and it is the test that catches a
// leak nobody thought to look for.
//
// Every route is driven at its handler rather than at a refusal in front of it —
// that is what the per-row status assertions are for — because a sweep of eleven
// uniform 401s would search eleven copies of the same fixed body and find
// nothing, whatever the pages behind them print.
//
// The refusals are then driven deliberately, at the end, because "any page or
// error path" is the requirement and an error path is exactly where a value
// nobody meant to print gets printed: it is the branch with no screenshot, no
// design review, and usually no reader.
//
// **Must fail when** any page or error path prints either configured secret —
// verbatim, or as a run of four characters of it from either end.
func TestFullRouteSweepLeaksNoSecret(t *testing.T) {
	t.Parallel()

	s := newSweep(t)

	// Two sessions of the operator's own, so that every route naming one is
	// answered by its handler rather than by the uniform 404 an identifier
	// nothing matches gets.
	api, credential := s.fixture.plant(t, session.Session{Name: "swept-through-the-api", WorkDir: s.fixture.repo})
	browser, _ := s.fixture.plant(t, session.Session{Name: "swept-through-the-browser", WorkDir: s.fixture.repo})

	// The API door's six operations, read off the router rather than listed here
	// — a seventh is swept the day it is registered — and each signed at its own
	// instant, because the replay cache counts a signature twice presented.
	at := testTime
	for _, route := range s.Routes() {
		at = at.Add(-time.Second)

		id, bearer := "", ""
		switch {
		case route.Method == http.MethodDelete:
			// A session of its own: this route ends the one it names, and the
			// operations after it in the table still need theirs standing.
			doomed, issued := s.fixture.plant(t, session.Session{Name: "swept-and-torn-down", WorkDir: s.fixture.repo})
			id, bearer = doomed.ID, issued
		case route.SessionScoped():
			id, bearer = api.ID, credential
		}

		w := s.drive(t, route.String(), s.signed(t, route, id, bearer, at))
		if want := reachedStatus[route]; w.Code != want {
			t.Errorf("%s = %d (%s); want %d — this sweep is only a claim about a route whose handler it reached",
				route, w.Code, w.Body.String(), want)
		}
	}

	// The forms the browser door's four actions submit. Each carries the page
	// token a rendered page would have carried, since an action refused by the
	// gate is an action whose handler never ran.
	token := func() url.Values {
		form := url.Values{}
		form.Set(fieldPageToken, mustMint(t, s.pageKey, testOperatorEmail, testTime))
		return form
	}
	create, rename, compact, mode, destroy := token(), token(), token(), token(), token()
	create.Set(fieldName, "swept-into-existence")
	create.Set(fieldWorkDir, s.fixture.repo)
	rename.Set(fieldName, "swept-and-renamed")
	// The toggle carries both its fields, so the sweep reads what the route
	// answers rather than what it refuses a malformed request with: a refusal
	// before the lookup is a shorter path through the handler, and the point here
	// is to drive the long one.
	mode.Set(fieldMode, string(session.ModeLocal))
	mode.Set(fieldConfirm, confirmYes)
	destroy.Set(fieldConfirm, confirmYes)

	// The browser door's whole surface. The destroy comes last of the four
	// actions because it ends the session the three above it name.
	for _, row := range []struct {
		what   string
		method string
		target string
		form   url.Values
		want   int
	}{
		{"the fleet", http.MethodGet, "/", nil, http.StatusOK},
		{"the settings page", http.MethodGet, settingsPath, nil, http.StatusOK},
		// Every section, because the page shows one at a time (#103) and this
		// sweep's whole claim is that NO response carries a secret. A sweep that
		// loaded only the default section would be searching a fraction of what
		// this route can return and reporting it as all of it.
		{"the settings page, where it listens", http.MethodGet, settingsPath + "?section=Where+it+listens", nil, http.StatusOK},
		{"the settings page, who may reach it", http.MethodGet, settingsPath + "?section=Who+may+reach+it", nil, http.StatusOK},
		{"the settings page, what it may touch", http.MethodGet, settingsPath + "?section=What+it+may+touch", nil, http.StatusOK},
		{"the settings page, what it runs", http.MethodGet, settingsPath + "?section=What+it+runs", nil, http.StatusOK},
		{"the settings page, limits", http.MethodGet, settingsPath + "?section=Limits", nil, http.StatusOK},
		{"the settings page, updates", http.MethodGet, settingsPath + "?section=Updates", nil, http.StatusOK},
		{"what the daemon calls itself", http.MethodGet, versionPath, nil, http.StatusOK},
		{"the page a card links to", http.MethodGet, "/sessions/" + browser.ID + "/view", nil, http.StatusOK},
		// A recorder cannot lift a write deadline, so an open that got past
		// identity, the cross-site check, the ownership lookup and the cap answers
		// 500 — which is what askToWatch documents, and what makes these two rows
		// claims about the open sequence rather than about the transport.
		//
		// What this sweep therefore does not read is a *delivered* stream, and the
		// reason that is not a hole is that neither stream handler reads the
		// Config at all: the four places `s.cfg` is read are the two body limits,
		// the create form's command names and the settings page, and all four are
		// driven here.
		{"a session's live stream", http.MethodGet, "/sessions/" + browser.ID + "/stream", nil, http.StatusInternalServerError},
		{"the fleet stream", http.MethodGet, fleetStreamPath, nil, http.StatusInternalServerError},
		// The four actions answer 303 since T014, and the sweep reads what they
		// write rather than following the redirect: the fleet they redirect *to* is
		// the first row of this table, already swept, and a Location is a header
		// like any other — so a secret that reached one would be found here.
		{"a create from the browser", http.MethodPost, "/dashboard/sessions", create, http.StatusSeeOther},
		{"a rename from the browser", http.MethodPost, "/dashboard/sessions/" + browser.ID + "/rename", rename, http.StatusSeeOther},
		{"a compact from the browser", http.MethodPost, "/dashboard/sessions/" + browser.ID + "/compact", compact, http.StatusSeeOther},
		{"a mode change from the browser", http.MethodPost, "/dashboard/sessions/" + browser.ID + "/mode", mode, http.StatusSeeOther},
		{"a destroy from the browser", http.MethodPost, "/dashboard/sessions/" + browser.ID + "/destroy", destroy, http.StatusSeeOther},
		{"a path nothing claims", http.MethodGet, "/not-a-route", nil, http.StatusNotFound},
		{"a mutating verb at the settings page", http.MethodPost, settingsPath, nil, http.StatusNotFound},
		// Refused by ServeHTTP ahead of the router, which is a path of its own:
		// it is answered before any pattern matches, so it is the one request here
		// no route table entry accounts for.
		{"a path no router would clean", http.MethodGet, "/static/../templates/dashboard.html", nil, http.StatusNotFound},
	} {
		w := s.drive(t, row.what, s.asOperator(t, row.method, row.target, row.form))
		if w.Code != row.want {
			t.Errorf("%s: %s %s = %d (%s); want %d", row.what, row.method, row.target, w.Code, w.Body.String(), row.want)
		}
	}

	// The embedded assets, on their own routes.
	for _, a := range sweptAssets(t) {
		target := strings.TrimPrefix(a.pattern, http.MethodGet+" ")
		if w := s.drive(t, target, s.asOperator(t, http.MethodGet, target, nil)); w.Code != http.StatusOK {
			t.Errorf("GET %s = %d (%s); want %d", target, w.Code, w.Body.String(), http.StatusOK)
		}
	}

	// handleUnrouted's other half: one wrong-method request per contract path, so
	// the method-less patterns are swept as well as the operations they sit
	// beside. PUT, because no route on this daemon serves it.
	asked := map[string]bool{}
	for _, route := range routes {
		if asked[route.Pattern] {
			continue
		}
		asked[route.Pattern] = true

		target := strings.ReplaceAll(route.Pattern, "{"+pathValueID+"}", api.ID)
		if w := s.drive(t, "PUT "+route.Pattern, s.asOperator(t, http.MethodPut, target, nil)); w.Code != http.StatusNotFound {
			t.Errorf("PUT %s = %d (%s); want %d — a method no route answers is a path nothing claims",
				target, w.Code, w.Body.String(), http.StatusNotFound)
		}
	}

	// The refusals, one per door plus the action gate's.
	for _, refused := range []struct {
		what string
		want int
		req  *http.Request
	}{
		{"the fleet with no assertion at all", http.StatusUnauthorized, httptest.NewRequest(http.MethodGet, "/", nil)},
		{"the settings page with no assertion at all", http.StatusUnauthorized, httptest.NewRequest(http.MethodGet, settingsPath, nil)},
		{"an API request nothing signed", http.StatusUnauthorized, httptest.NewRequest(http.MethodGet, "/sessions", nil)},
		{"an action carrying no page token", http.StatusForbidden, s.asOperator(t, http.MethodPost, "/dashboard/sessions/"+api.ID+"/compact", url.Values{})},
	} {
		if w := s.drive(t, refused.what, refused.req); w.Code != refused.want {
			t.Errorf("%s = %d (%s); want %d", refused.what, w.Code, w.Body.String(), refused.want)
		}
	}

	// Nothing below is a claim about a route this sweep never drove.
	for pattern, exercised := range s.reached {
		if !exercised {
			t.Errorf("%s is registered on this daemon and nothing above drove it, so the search below says nothing about it", pattern)
		}
	}
	if len(s.answers) == 0 {
		t.Fatal("the sweep drove no route at all, so the search below would pass vacuously")
	}

	// The search itself: every response, and the whole trail behind them.
	//
	// The length check TestSettingsNeverRendersSecretValue makes stays with that
	// page rather than being repeated here. A bare number is a disclosure where a
	// page is describing a secret and is nothing at all elsewhere, and a sweep of
	// every response that refused any body carrying "59" would be a test somebody
	// has to argue with about a timestamp.
	trail := s.sink.String()
	for _, secret := range []struct {
		what  string
		value string
	}{
		{"the shared secret", canarySecret},
		{"the allowlisted addresses", canaryAllowed},
	} {
		for _, answer := range s.answers {
			if strings.Contains(answer.text, secret.value) {
				t.Errorf("%s answered with %s verbatim:\n%s", answer.what, secret.what, answer.text)
				continue
			}
			if run, cut := leakedRun(answer.text, secret.value); run != "" {
				t.Errorf("%s answered with %q, %s %d characters of %s; a masked value is still a disclosure:\n%s",
					answer.what, run, cut, len(run), secret.what, answer.text)
			}
		}
		if strings.Contains(trail, secret.value) {
			t.Errorf("the audit trail carries %s verbatim:\n%s", secret.what, trail)
			continue
		}
		if run, cut := leakedRun(trail, secret.value); run != "" {
			t.Errorf("the audit trail carries %q, %s %d characters of %s:\n%s", run, cut, len(run), secret.what, trail)
		}
	}

	// The key layer 2 is really checking signatures against, which is not the
	// canary above — see newSweep. Whole rather than in runs: it is spelled in
	// words rather than in entropy, and it shares its `test-only-` announcement
	// with the fixture's Access audience, which the settings page renders.
	for _, answer := range append(slices.Clone(s.answers), sweptAnswer{what: "the audit trail", text: trail}) {
		if strings.Contains(answer.text, string(testSecret())) {
			t.Errorf("%s carries the key layer 2 checks every signature against:\n%s", answer.what, answer.text)
		}
	}

	// And the daemon really was holding both secrets while it answered. A fixture
	// that had lost them would sweep an unconfigured daemon and find nothing,
	// which is the one way this test passes while proving nothing at all.
	//
	// It is asked *after* the search rather than before it, and reported rather
	// than fatal, because the two failures overlap: a page rendering the value
	// instead of the word fails this as well as leaking, and a precondition that
	// stopped the test first would report the leak as a broken fixture.
	page := s.answerFor(t, "the settings page, who may reach it")
	for _, key := range []string{"shared_secret", "access_allowed_emails"} {
		if row := settingsRowFor(t, page, key); !strings.Contains(row, "<td>"+secretPresent+"</td>") {
			t.Errorf("the swept daemon reports %s as %q; this is a claim about a daemon that has both secrets configured", key, row)
		}
	}

	// The trail really was written to. Searching an empty string finds nothing in
	// it, and FR-041 puts one record behind every request above.
	if records := s.records(t); len(records) < len(s.answers) {
		t.Errorf("%d requests left %d audit records; the trail this searched is missing some of them", len(s.answers), len(records))
	}
}

// TestSettingsOffersTheUpdate is #103, and it reads the markup because that is
// the whole of what went wrong.
//
// Milestone 6 built self-update — fetch, checksum, ed25519 signature, smoke
// test, atomic swap — verified it against real releases, and rendered no control
// for it anywhere. Every test asserted on the route or the record; none read a
// page. That is milestone 4's FR-026 exactly, in the milestone that came after
// the one which learned it.
//
// **Must fail when** the route accepts an update but no page offers one.
func TestSettingsOffersTheUpdate(t *testing.T) {
	t.Parallel()

	f := newFleet(t)
	f.releaseFeed = func(context.Context) (*updater.Release, error) {
		return &updater.Release{Version: "v9.99", Notes: "## What's Changed\nthings"}, nil
	}
	page := settingsChecked(t, f)

	for _, want := range []string{
		`action="/dashboard/update"`,
		`name="version" value="v9.99"`,
		`name="confirm" value="yes"`,
		fieldPageToken,
	} {
		if !strings.Contains(page, want) {
			t.Errorf("the settings page carries no %s, so the update route has no control:\n%s", want, page)
		}
	}
	if !strings.Contains(page, "v9.99") {
		t.Error("the page does not name the version it would move to")
	}
	if !strings.Contains(page, "things") {
		t.Error("the page does not carry the release's own notes, so an operator cannot read what they would be taking")
	}
}

// TestSettingsOffersNoUpdateWhenCurrent is the other half: a button that would
// refuse is worse than no button, because it invites a click and answers with a
// refusal the operator cannot act on.
func TestSettingsOffersNoUpdateWhenCurrent(t *testing.T) {
	t.Parallel()

	f := newFleet(t)
	f.releaseFeed = func(context.Context) (*updater.Release, error) {
		return &updater.Release{Version: buildinfo.Version}, nil
	}
	if page := settingsBody(t, f); strings.Contains(page, `action="/dashboard/update"`) {
		t.Errorf("the settings page offers an update to the version already running:\n%s", page)
	}
}

// TestSettingsSurvivesAnUnreachableFeed is the offline host, and the default
// every other test in this file runs under.
//
// **Must fail when** composing this page depends on the network. Its first job is
// reporting local configuration, which needs none, and an operator asking why a
// working directory was refused must not wait on somebody else's API to find out.
func TestSettingsSurvivesAnUnreachableFeed(t *testing.T) {
	t.Parallel()

	f := newFleet(t)
	f.releaseFeed = func(context.Context) (*updater.Release, error) {
		return nil, errors.New("the release feed is unreachable")
	}
	page := settingsChecked(t, f)

	if !strings.Contains(page, "could not reach the release feed") {
		t.Errorf("the page does not say it could not look, which is a different fact from being current:\n%s", page)
	}
	// The configuration is still reachable from the same page, which is the
	// claim: a feed that could not be reached costs the Updates section and
	// nothing else. Asked for through the menu, because that is how the page is
	// arranged now — one section at a time.
	if !strings.Contains(settingsSectionBody(t, f, "What it may touch"), "allowed_roots") {
		t.Error("an unreachable feed cost the page its configuration, which needs no network at all")
	}
}

// TestEverySettingAppearsInASection guards the grouping, not the sections.
//
// **Must fail when** a key is added to config.go that no section claims and the
// grouping drops it. It cannot: settingSectionOf falls through to "Other", which
// is visible on the page and is the prompt to classify it. A key that vanished
// instead would leave the page quietly incomplete, which is worse than ungrouped.
func TestEverySettingAppearsInASection(t *testing.T) {
	t.Parallel()

	rows := settingsOf(testConfig(loopbackListen))
	var grouped int
	for _, section := range sectioned(rows) {
		grouped += len(section.Settings)
	}
	if grouped != len(rows) {
		t.Errorf("sectioning holds %d of %d settings; a grouping that loses a key is a page that is quietly incomplete", grouped, len(rows))
	}
}

// TestSettingsMenuReachesEverySection is the menu's own obligation.
//
// **Must fail when** a section exists that the menu does not link, which is a
// setting an operator cannot reach — invisible to any test that asserts on the
// data rather than the page, because sectioned() would still hold it.
func TestSettingsMenuReachesEverySection(t *testing.T) {
	t.Parallel()

	f := newFleet(t)
	page := settingsDefaultBody(t, f)

	for _, section := range sectioned(settingsOf(testConfig(loopbackListen))) {
		// %20 rather than +, because html/template escapes for a URL context and
		// that is the encoding it chooses. Asserting the rendered form rather
		// than a guess at it is the point: a test that built the link itself
		// would agree with its own assumption and not with the page.
		link := `href="/settings?section=` + strings.ReplaceAll(section.Title, " ", "%20") + `"`
		if !strings.Contains(page, link) {
			t.Errorf("the menu has no link to %q, so that section is unreachable:\n%s", section.Title, page)
		}
	}
	if !strings.Contains(page, sectionUpdates) {
		t.Error("the menu does not offer Updates")
	}
}

// TestSettingsMarksWhereYouAre is the highlight, said to somebody who cannot see
// it.
//
// **Must fail when** the current section is marked by colour alone. aria-current
// is what a screen reader reads, and the stylesheet pairs it with a border for
// the same reason: no state on this interface is conveyed by hue.
func TestSettingsMarksWhereYouAre(t *testing.T) {
	t.Parallel()

	f := newFleet(t)
	for _, section := range []string{sectionUpdates, "Limits"} {
		body := settingsSectionBody(t, f, section)
		if !strings.Contains(body, `aria-current="page"`) {
			t.Errorf("the %s section does not mark itself current, so only sighted operators know where they are", section)
		}
	}
}

// TestSettingsShowsOnlyTheChosenSection is what the menu is for.
//
// **Must fail when** every section renders regardless, which would make the menu
// decoration over a page that never changed.
func TestSettingsShowsOnlyTheChosenSection(t *testing.T) {
	t.Parallel()

	f := newFleet(t)
	limits := settingsSectionBody(t, f, "Limits")

	if !strings.Contains(limits, ">max_sessions<") {
		t.Error("the Limits section does not carry max_sessions")
	}
	if strings.Contains(limits, ">allowed_roots<") {
		t.Errorf("the Limits section also renders allowed_roots, so the menu chooses nothing:\n%s", limits)
	}
}

// TestSettingsMenuNeedsNoScript is the degradation this menu was built for.
//
// **Must fail when** the entries stop being links. A scripted tab strip would be
// faster and would leave an operator with scripting off looking at one section
// and no way to reach the rest — on the page whose job is answering "how is this
// daemon configured?".
func TestSettingsMenuNeedsNoScript(t *testing.T) {
	t.Parallel()

	page := settingsDefaultBody(t, newFleet(t))
	if strings.Contains(page, "<button") && !strings.Contains(page, `class="settings-menu-link"`) {
		t.Error("the settings menu is buttons rather than links, so it cannot work without script")
	}
	if !strings.Contains(page, `<a class="settings-menu-link" href="/settings?section=`) {
		t.Errorf("the menu is not made of real links:\n%s", page)
	}
}

// TestSettingsDoesNotCheckUnlessAsked is why Check exists as a button.
//
// **Must fail when** composing this page reaches the release feed on an ordinary
// render. That makes the settings page only as fast and as available as somebody
// else's API, on behalf of an operator who may have come to read a root — and it
// asks GitHub once per page view for a fact that changes on merges.
func TestSettingsDoesNotCheckUnlessAsked(t *testing.T) {
	t.Parallel()

	f := newFleet(t)
	var asked int
	f.releaseFeed = func(context.Context) (*updater.Release, error) {
		asked++
		return &updater.Release{Version: "v9.99"}, nil
	}

	if body := settingsSectionBody(t, f, sectionUpdates); strings.Contains(body, "v9.99") {
		t.Error("the page named a release it was never asked to look for")
	}
	if asked != 0 {
		t.Errorf("composing the page asked the release feed %d times; want 0 until the operator presses Check", asked)
	}

	if body := settingsChecked(t, f); !strings.Contains(body, "v9.99") {
		t.Errorf("pressing Check did not reach the feed:\n%s", body)
	}
	if asked != 1 {
		t.Errorf("Check asked the feed %d times; want exactly 1", asked)
	}
}

// TestSettingsOffersCheckBeforeUpdate is the two-step the operator asked for.
//
// **Must fail when** an update button appears before anything has looked. It
// would be offering a version the page has no reason to believe exists.
func TestSettingsOffersCheckBeforeUpdate(t *testing.T) {
	t.Parallel()

	f := newFleet(t)
	before := settingsSectionBody(t, f, sectionUpdates)

	if !strings.Contains(before, "Check for updates") {
		t.Errorf("the Updates section offers no way to look:\n%s", before)
	}
	if strings.Contains(before, `action="/dashboard/update"`) {
		t.Error("an update button was offered before anything had been checked")
	}
}

// TestSettingsCarriesTheToastRegion is not about toasts.
//
// crswd.js's submit module returns at the top when #action-toast is absent, and
// that module is what intercepts a form posting to /dashboard/. Without this
// element the update form did an ordinary browser submit: the address became
// /dashboard/update and a navigation happened where a spinner was meant to.
//
// **Must fail when** the region goes missing from this page. The symptom is not
// a missing toast — it is the update navigating, which took three fixes before
// the cause turned out to be one element.
func TestSettingsCarriesTheToastRegion(t *testing.T) {
	t.Parallel()

	if page := settingsDefaultBody(t, newFleet(t)); !strings.Contains(page, `id="action-toast"`) {
		t.Errorf("the settings page carries no action-toast, so crswd.js will not intercept the update form and it will navigate:\n%s", page)
	}
}

// --- T003: the boolean rows are switches -------------------------------------

// settingControlPattern is the one input a row offers for its value, whichever
// kind it is.
//
// Anchored on the class rather than on the tag, so the hidden token and key
// fields beside it are not candidates: those are the form's machinery, and a
// sweep that counted them could never say a row offers exactly one control.
var settingControlPattern = regexp.MustCompile(`<input class="(?:setting-input|switch-input)"[^>]*>`)

// settingControlValue is what that input submits.
var settingControlValue = regexp.MustCompile(`value="([^"]*)"`)

// settingControl is the control one settings row offers, read out of the markup
// a browser was handed.
//
// Reading the rendered page rather than the view behind it is the whole point of
// the assertions below. Milestone 4 shipped three green tasks about a control an
// operator never saw change, because every one of them asserted what the handler
// accepted; the only thing that catches a template nobody edited is reading the
// template's output.
func settingControl(t *testing.T, page, key string) string {
	t.Helper()

	row := settingsRowFor(t, page, key)
	found := settingControlPattern.FindAllString(row, -1)
	if len(found) != 1 {
		t.Fatalf("the %s row offers %d value controls; a row edits one setting with one control:\n%s", key, len(found), row)
	}
	return found[0]
}

// TestEveryBooleanRowIsASwitchAndNothingElseIs is the operator's request read at
// the markup: "all true/false settings should be check boxes".
//
// It sweeps every editable key rather than naming the two booleans, so it states
// the rule in both directions and cannot go stale. A third boolean added to the
// loader arrives here as a text field and fails; a key wrongly reported boolean
// arrives as a box and fails too — and that second direction is the one that
// matters, because a box is the control whose absence the edit route reads as
// `false`.
//
// **Must fail when** the template renders one control for every row, which is
// what it did before this task and what a task marked done by its handler alone
// would have left it doing.
func TestEveryBooleanRowIsASwitchAndNothingElseIs(t *testing.T) {
	t.Parallel()

	page := settingsEverySection(t, newFleet(t))

	swept := 0
	for _, name := range config.Vars() {
		key := config.KeyForVar(name)
		if !config.Editable(key) {
			continue
		}
		swept++
		control := settingControl(t, page, key)
		if config.IsBool(key) {
			if !strings.Contains(control, `type="checkbox"`) {
				t.Errorf("%s is a true/false setting and its row offers %q; an operator is asked to type `true` where a box belongs", key, control)
			}
			continue
		}
		if !strings.Contains(control, `type="text"`) {
			t.Errorf("%s is not a boolean and its row offers %q; a box on that row submits nothing when it is cleared, and the route reads nothing as `false` only for the keys config.IsBool names", key, control)
		}
	}
	if swept == 0 {
		t.Fatal("no editable row was reached, so this sweep asserted nothing about any control")
	}
}

// TestTheSwitchReflectsTheSetting is FR-018a's discipline applied to a control:
// a box that always rendered unticked would tell an operator their setting is
// off, and be believed.
//
// Both states and both keys. One direction is not interesting on its own — an
// always-unticked box passes any assertion made against a setting that is
// already off, which is the shape of a test that cannot fail.
func TestTheSwitchReflectsTheSetting(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		key string
		set func(*config.Config, bool)
	}{
		{"discover_roots", func(cfg *config.Config, on bool) { cfg.DiscoverRoots = on }},
		{"destroy_on_shutdown", func(cfg *config.Config, on bool) { cfg.DestroyOnShutdown = on }},
	} {
		for _, on := range []bool{true, false} {
			f := settingsOn(t, func(cfg *config.Config) { tc.set(cfg, on) })
			control := settingControl(t, settingsEverySection(t, f), tc.key)
			if strings.Contains(control, " checked") != on {
				t.Errorf("%s is %v and its box renders %q; the control states the setting or it misinforms about it", tc.key, on, control)
			}
		}
	}
}

// TestTheSwitchSubmitsTheSpellingThisPageWrites holds the template's literal to
// boolOn, the constant the rest of this page is written in terms of.
//
// The value a checkbox submits is one of the few things in this tree spelled
// twice — a template parsed with no function map cannot reach a Go constant — so
// this is the arrangement `confirm=yes` already has on the card.
//
// **Must fail when** the attribute is dropped, which leaves the browser's own
// `on`. TestTheTickedSwitchIsAValueTheLoaderAccepts is what says why that
// matters.
func TestTheSwitchSubmitsTheSpellingThisPageWrites(t *testing.T) {
	t.Parallel()

	control := settingControl(t, settingsEverySection(t, newFleet(t)), "discover_roots")
	found := settingControlValue.FindStringSubmatch(control)
	if found == nil {
		t.Fatalf("the switch carries no value at all, so a tick submits the browser's own `on`: %q", control)
	}
	if found[1] != boolOn {
		t.Errorf("a ticked switch submits %q and this page writes %q; the file would carry a word its own loader refuses", found[1], boolOn)
	}
}

// --- T005: the restart control -----------------------------------------------

// restartFormPattern is the form on this page that posts to the restart route,
// opening tag and all.
var restartFormPattern = regexp.MustCompile(`(?s)<form[^>]*action="` + wantRestartPath + `"[^>]*>.*?</form>`)

// hiddenFormField is a field the page sends without the operator typing it: the
// token that authorises the write and the confirming step that says it was meant.
var hiddenFormField = regexp.MustCompile(`<input type="hidden" name="([^"]*)" value="([^"]*)">`)

// restartFormIn isolates that form, which is settingsRowFor's discipline and
// matters more here than anywhere else on this page.
//
// The update form sits three lines above it carrying a page token, a confirming
// step and a sentence ending "Sessions survive it" of its own. Every assertion
// below would therefore pass, word for word, against a page that offers no
// restart control at all — which is precisely the state #103 found and the state
// this task exists to leave behind.
func restartFormIn(t *testing.T, page string) string {
	t.Helper()

	found := restartFormPattern.FindAllString(page, -1)
	if len(found) != 1 {
		t.Fatalf("the settings page carries %d forms posting to %s; want exactly 1 — none is a route no operator can reach, and two is a page asking the same question twice:\n%s",
			len(found), wantRestartPath, page)
	}
	return found[0]
}

// TestSettingsOffersTheRestart is #103's rule read against T004: a route that
// ends this daemon and a page that offers no way to ask for it is a milestone of
// green tests and a feature nobody can use.
//
// It reads the section *before* anything has been checked, and that is the second
// claim rather than a convenience. A restart has nothing to do with what a
// release feed says; an operator restarting a wedged daemon on a host with no
// network must not be made to ask GitHub a question first.
//
// **Must fail when** the Updates section renders no restart control, when it
// renders one only after a check, or when the control it renders is missing
// either half of what the route reads.
func TestSettingsOffersTheRestart(t *testing.T) {
	t.Parallel()

	form := restartFormIn(t, settingsSectionBody(t, newFleet(t), sectionUpdates))

	if !strings.Contains(form, `method="post"`) {
		t.Errorf("the restart form declares no post method; a GET on that path is a route this daemon does not serve:\n%s", form)
	}
	if !strings.Contains(form, `type="submit"`) {
		t.Errorf("the restart form carries no control that submits it, so it is markup rather than a button:\n%s", form)
	}

	fields := make(map[string]string)
	for _, field := range hiddenFormField.FindAllStringSubmatch(form, -1) {
		fields[field[1]] = field[2]
	}
	if fields[fieldPageToken] == "" {
		t.Errorf("the restart form carries no page token, so the gate refuses it uniformly and the operator is told nothing at all:\n%s", form)
	}
	if got := fields[fieldConfirm]; got != confirmYes {
		t.Errorf("the restart form's confirming step is %q and the route reads %q; a control certain to be turned away is worse than no control", got, confirmYes)
	}
}

// TestTheRestartSaysSessionsSurviveIt is the half of this control that is copy,
// and it is the half the operator's decision actually rests on.
//
// A restart that might take every session on the host with it is a button nobody
// presses. This one does not — they are tmux windows on this host rather than
// children of this process (#63) — and a page that knows that and does not say it
// has left the operator to guess about unsandboxed shells they cannot get back.
//
// **Must fail when** the control ships without the sentence beside it. Asserted
// inside the form for restartFormIn's reason: the update's own caution says the
// same four words a little way up the page.
func TestTheRestartSaysSessionsSurviveIt(t *testing.T) {
	t.Parallel()

	form := restartFormIn(t, settingsSectionBody(t, newFleet(t), sectionUpdates))

	if !strings.Contains(form, "Sessions survive it") {
		t.Errorf("the restart control never says sessions survive it, so an operator weighing it has to assume they do not:\n%s", form)
	}
	if !strings.Contains(form, `class="update-caution"`) {
		t.Errorf("the restart's sentence is not the caution line this section already has, which is a second spelling for what the update above says in one:\n%s", form)
	}
}

// TestTheRenderedRestartFormIsAcceptedByTheRoute is the only assertion about this
// control that says it *works*.
//
// Everything above reads markup and everything in restart_test.go builds a form
// in Go, and between the two sits the gap milestone 4 shipped three green tasks
// across: a page and a handler that each satisfy their own tests and disagree
// about a field. So this takes the fields the page rendered, posts exactly those
// and nothing else, and asks the daemon what it did about it.
//
// The one server renders and receives, which is what makes the token real: a
// token minted by one fixture and posted to another proves only that two test
// servers were built differently.
//
// **Must fail when** the rendered form is refused by the route it names — a
// missing token, a confirming step spelled some other way, or a method that route
// does not serve.
func TestTheRenderedRestartFormIsAcceptedByTheRoute(t *testing.T) {
	t.Parallel()

	d := newRestartDoor(t)

	page := d.send(t, http.MethodGet, settingsPath+"?"+querySection+"="+sectionUpdates, secFetchSiteSameOrigin, nil)
	if page.Code != http.StatusOK {
		t.Fatalf("GET the Updates section = %d (%s); want %d", page.Code, page.Body.String(), http.StatusOK)
	}

	submitted := url.Values{}
	for _, field := range hiddenFormField.FindAllStringSubmatch(restartFormIn(t, page.Body.String()), -1) {
		// Unescaped because a browser submits the value, not the attribute: what
		// html/template wrote here is escaped for an attribute context, and posting
		// that verbatim would be this test typing something no operator can.
		submitted.Set(field[1], html.UnescapeString(field[2]))
	}

	w := d.send(t, http.MethodPost, wantRestartPath, secFetchSiteSameOrigin, submitted)

	wantRestartingPage(t, w)
	d.steps.waitForExit(t)
	if got := d.steps.count(); got != 1 {
		t.Errorf("the form this page rendered ended the process %d times; want exactly 1 — a control its own route refuses is a button that does nothing and says nothing", got)
	}
}
