// Internal test, matching the rest of the package. Every claim here is about the
// route rather than about the markup — what the door does with a caller it will
// not verify, what the trail says about a page that was served, and what this
// path answers a verb it has no route for — so each one drives /settings through
// the real router, the real browser door, and a real *access.Validator over a
// locally generated key pair. A handler called directly would prove none of them.
package httpapi

import (
	"html"
	"maps"
	"net/http"
	"net/http/httptest"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/nctiggy/claude-remote-session-webhook/internal/audit"
	"github.com/nctiggy/claude-remote-session-webhook/internal/auth"
	"github.com/nctiggy/claude-remote-session-webhook/internal/config"
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
func settingsBody(t *testing.T, f *fleet) string {
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
func TestSettingsNeverRendersSecretValue(t *testing.T) {
	t.Parallel()

	f := settingsOn(t, func(cfg *config.Config) {
		cfg.SharedSecret = []byte(canarySecret)
		cfg.AccessAllowedEmails = []string{canaryAllowed}
	})
	page := settingsBody(t, f)

	// Four, because three characters of a credential is not a run and a page of
	// markup carries plenty of them by accident. Four is where a prefix starts
	// being worth having.
	const shortestRunWorthHaving = 4

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
		// The body rather than the whole value, for the reason canaryAnnounce
		// gives. Any masked form long enough to disclose four characters of the
		// entropy contains a run this finds, whichever end it was cut from.
		body := strings.TrimPrefix(secret.value, canaryAnnounce)
		for n := shortestRunWorthHaving; n < len(body); n++ {
			if prefix := body[:n]; strings.Contains(page, prefix) {
				t.Errorf("the settings page carries %q, the first %d characters of %s; a masked value is still a disclosure", prefix, n, secret.what)
				break
			}
		}
		for n := shortestRunWorthHaving; n < len(body); n++ {
			if suffix := body[len(body)-n:]; strings.Contains(page, suffix) {
				t.Errorf("the settings page carries %q, the last %d characters of %s; a masked value is still a disclosure", suffix, n, secret.what)
				break
			}
		}
		// The length, which is what "shorter than the required 32 bytes" is
		// careful never to measure in a startup error either (loadSecret).
		length := regexp.MustCompile(`\b` + strconv.Itoa(len(secret.value)) + `\b`)
		if match := length.FindString(page); match != "" {
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

	page := settingsBody(t, newFleet(t))

	at := -1
	for _, name := range config.Vars() {
		key := config.KeyForVar(name)
		found := strings.Index(page, ">"+key+"<")
		if found < 0 {
			t.Errorf("the settings page has no row for %s, so it says nothing at all about that setting", key)
			continue
		}
		if found < at {
			t.Errorf("the settings page renders %s out of the order config.go declares it in", key)
		}
		at = found
	}
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
		if want := "<td>" + html.EscapeString(tc.want) + "</td>"; !strings.Contains(row, want) {
			t.Errorf("the %s row is %q; want a value cell holding exactly %q", tc.key, row, want)
		}
	}
}
