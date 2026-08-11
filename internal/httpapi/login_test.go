package httpapi

// login_test.go covers the sign-in page and the route that answers it
// (M12/T004): the two routes this daemon registers in front of layer 1, and the
// only way a password door ever issues the cookie it spends the rest of its life
// verifying.
//
// Three claims carry the task, and each is a whole test below rather than an
// assertion inside another one:
//
//  1. The routes exist on a password daemon and on no other. A login form beside
//     a working Access door is the second authorisation path this milestone's
//     plan forbids, and the way to not have one is to not register it.
//  2. Signing in produces a cookie that really opens the dashboard — driven
//     end to end through the real middleware, because a route that sets a header
//     nothing admits is a task that looks finished.
//  3. Every wrong answer is the same wrong answer, byte for byte, and what it
//     really was is on the trail with no byte of the password in it.

import (
	"fmt"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/nctiggy/claude-remote-session-webhook/internal/audit"
	"github.com/nctiggy/claude-remote-session-webhook/internal/auth"
	"github.com/nctiggy/claude-remote-session-webhook/web"
)

// loginDaemon is a whole daemon whose door is the dashboard password, built
// through the *real* verifiedLayer1.
//
// Through the real selection deliberately: what this suite is about is which
// routes a daemon registers, and that is decided from the door newServer was
// handed. A fixture that passed a hand-built *passwordDoor would prove the
// registration works for a door no configuration produces, which is the half of
// the claim that was never in doubt.
type loginDaemon struct {
	*testServer

	// door is the same door the server is holding, kept so a test can mint the
	// cookie the route is supposed to write and compare the two.
	door *passwordDoor
}

func newLoginDaemon(t *testing.T) *loginDaemon {
	t.Helper()

	cfg := passwordConfig(loopbackListen)
	built, err := verifiedLayer1(cfg)
	if err != nil {
		t.Fatalf("verifiedLayer1(a password Config) = _, %v; want the password door", err)
	}
	door, ok := built.(*passwordDoor)
	if !ok {
		t.Fatalf("verifiedLayer1(a password Config) built %s; want the password door", typeName(built))
	}

	s := newAuditedServerOn(t, cfg, door)
	// The daemon's clock and the door's, pinned to the one instant every expiry
	// in this file is measured against. pinClock reaches the Server's; the door
	// carries its own, because a cookie's expiry must be measured by the same
	// reading of time that minted it.
	door.clock = fixedClock{at: testTime}
	return &loginDaemon{testServer: s, door: door}
}

// get asks for one path with no credential of any kind, which is what a browser
// arriving at a password daemon for the first time really sends.
func (d *loginDaemon) get(t *testing.T, target string) *httptest.ResponseRecorder {
	t.Helper()

	w := httptest.NewRecorder()
	d.ServeHTTP(w, httptest.NewRequest(http.MethodGet, target, nil))
	return w
}

// submit posts a form to the sign-in route the way the rendered page does.
func (d *loginDaemon) submit(t *testing.T, form url.Values) *httptest.ResponseRecorder {
	t.Helper()

	r := httptest.NewRequest(http.MethodPost, pathLogin, strings.NewReader(form.Encode()))
	r.Header.Set(headerContentType, contentTypeForm)

	w := httptest.NewRecorder()
	d.ServeHTTP(w, r)
	return w
}

// signIn is the whole sign-in, asserted, and hands back the cookie it produced.
func (d *loginDaemon) signIn(t *testing.T) *http.Cookie {
	t.Helper()

	w := d.submit(t, url.Values{fieldPassword: {testDashboardPassword}})
	if w.Code != http.StatusSeeOther {
		t.Fatalf("POST %s with the configured password = %d (%s); want %d",
			pathLogin, w.Code, w.Body.String(), http.StatusSeeOther)
	}

	cookies := (&http.Response{Header: w.Header()}).Cookies()
	if len(cookies) != 1 {
		t.Fatalf("a successful sign-in set %d cookies; want exactly 1", len(cookies))
	}
	return cookies[0]
}

// TestTheSignInRoutesExistOnlyOnAPasswordDaemon is the registration decision,
// asserted from all three doors this daemon can be built with.
//
// It is the half of T004 that is about absence, and absence is what nobody
// tests. A login form served beside a working Access door would be a second way
// into a dashboard whose first way is an identity provider — the operator would
// have configured Google and still be one guessable secret from an unsandboxed
// shell, and every existing test in this package would pass.
//
// The Access row asks as a *verified* operator, because that is the only caller
// who can tell a route that does not exist from a door that refused: a stranger
// gets the same 401 either way, which is exactly the point of the uniform
// refusal. The closed-door row asks as a stranger, since on that daemon there is
// nobody else to be.
//
// **Must fail when** the registration stops asking which door was built — a
// daemon that registered these routes unconditionally, or from cfg.
// DashboardPassword rather than from the door newServer was handed.
func TestTheSignInRoutesExistOnlyOnAPasswordDaemon(t *testing.T) {
	t.Parallel()

	t.Run("the password door serves them", func(t *testing.T) {
		t.Parallel()

		d := newLoginDaemon(t)

		page := d.get(t, pathLogin)
		if page.Code != http.StatusOK {
			t.Fatalf("GET %s = %d (%s); want %d", pathLogin, page.Code, page.Body.String(), http.StatusOK)
		}
		if !strings.Contains(page.Body.String(), `name="`+fieldPassword+`"`) {
			t.Errorf("the sign-in page carries no %s field:\n%s", fieldPassword, page.Body.String())
		}
	})

	t.Run("an Access daemon answers the uniform not-found", func(t *testing.T) {
		t.Parallel()

		f := newFleet(t)
		for _, c := range []struct {
			what string
			w    *httptest.ResponseRecorder
		}{
			{what: "GET", w: f.open(t, pathLogin)},
			{what: "POST", w: f.ask(t, http.MethodPost, pathLogin)},
		} {
			if c.w.Code != http.StatusNotFound {
				t.Errorf("%s %s on an Access daemon = %d (%s); want %d — this route is not registered there",
					c.what, pathLogin, c.w.Code, c.w.Body.String(), http.StatusNotFound)
			}
			if body := c.w.Body.String(); strings.Contains(body, `name="`+fieldPassword+`"`) {
				t.Errorf("%s %s on an Access daemon rendered a sign-in form:\n%s", c.what, pathLogin, body)
			}
			if body := c.w.Body.String(); !strings.Contains(body, notFoundTitle) {
				t.Errorf("%s %s on an Access daemon answered something other than the not-found page:\n%s", c.what, pathLogin, body)
			}
		}
	})

	t.Run("a daemon with no door refuses as it refuses everything", func(t *testing.T) {
		t.Parallel()

		cfg := noDoorConfig(loopbackListen)
		door, err := verifiedLayer1(cfg)
		if err != nil {
			t.Fatalf("verifiedLayer1(a Config naming no door) = _, %v; want the closed door", err)
		}
		s := newAuditedServerOn(t, cfg, door)

		w := httptest.NewRecorder()
		s.ServeHTTP(w, httptest.NewRequest(http.MethodGet, pathLogin, nil))

		if w.Code != http.StatusUnauthorized {
			t.Fatalf("GET %s on a closed-door daemon = %d (%s); want %d",
				pathLogin, w.Code, w.Body.String(), http.StatusUnauthorized)
		}
		if got := w.Body.String(); got != string(bodyBrowserRefused) {
			t.Errorf("a closed-door daemon answered %s with something other than the door's uniform refusal:\n%s", pathLogin, got)
		}
	})
}

// TestSigningInIssuesACookieThatOpensTheDashboard is the task's own definition
// of done: not that the code exists, but that something calls it.
//
// Until this route existed, passwordDoor.issue had a test caller and no
// production one — so a password daemon started, bound where its operator could
// reach it, and refused every browser. This drives the whole path through the
// real router and the real middleware: the form goes in, the cookie comes out,
// and the cookie opens the fleet.
//
// The last step is the one that matters. A route that wrote a Set-Cookie no door
// admits would pass every assertion above it and leave the dashboard exactly as
// unreachable as it was.
//
// **Must fail when** the two halves drift — a cookie minted under a different
// label, key, or expiry than the one Verify recomputes.
func TestSigningInIssuesACookieThatOpensTheDashboard(t *testing.T) {
	t.Parallel()

	d := newLoginDaemon(t)
	cookie := d.signIn(t)

	if cookie.Name != cookieDashboardSession {
		t.Fatalf("a successful sign-in set the cookie %q; want %q", cookie.Name, cookieDashboardSession)
	}

	// The fleet, asked for by a browser carrying nothing but what the sign-in
	// gave it: no assertion, no signature, no bearer token.
	r := httptest.NewRequest(http.MethodGet, pathFleet, nil)
	//nolint:gosec // G124: a request-side cookie has no attributes to set.
	r.AddCookie(&http.Cookie{Name: cookie.Name, Value: cookie.Value})

	w := httptest.NewRecorder()
	d.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("GET %s carrying the cookie the sign-in issued = %d (%s); want %d — the dashboard is still unreachable",
			pathFleet, w.Code, w.Body.String(), http.StatusOK)
	}
	// And it is the operator's own dashboard, named by the identity this door
	// hands on rather than by an address nobody verified.
	if body := w.Body.String(); !strings.Contains(body, passwordOperator) {
		t.Errorf("the page the cookie opened names no operator:\n%s", body)
	}
}

// TestASuccessfulSignInSendsTheBrowserToTheFleet pins the answer's shape.
//
// 303 and not a rendered page, so the browser follows with a GET and a reload
// does not repost the password — the arrangement every action route on this
// daemon already has, for a body that matters more than any of theirs.
func TestASuccessfulSignInSendsTheBrowserToTheFleet(t *testing.T) {
	t.Parallel()

	d := newLoginDaemon(t)
	w := d.submit(t, url.Values{fieldPassword: {testDashboardPassword}})

	if w.Code != http.StatusSeeOther {
		t.Fatalf("POST %s = %d (%s); want %d", pathLogin, w.Code, w.Body.String(), http.StatusSeeOther)
	}
	if got := w.Header().Get("Location"); got != pathFleet {
		t.Errorf("a successful sign-in redirects to %q; want %q — and never to somewhere the caller named", got, pathFleet)
	}
	// The password is not in the redirect either. A Location is a header, and a
	// header is as disclosed as a body.
	if header := strings.Join(w.Header().Values("Location"), " "); strings.Contains(header, testDashboardPassword) {
		t.Errorf("the redirect carries the password: %q", header)
	}
}

// TestEveryRefusedSignInIsTheSameRefusal is the uniformity requirement, and the
// rows are chosen so that no two of them fail the same way.
//
// Every one answers with layer 1's own bytes — the same response a request
// carrying no cookie at all receives — because the difference between "no
// password field", "the wrong password" and "a body I could not read" is which
// forgery to try next. What each really was is on the trail, and the rows are
// read against each other there: two faults meaning different things must not
// record the same sentinel, or the record stops being able to answer the
// question it is the only place to ask.
//
// The query-string row is the one that is not about uniformity. A password this
// daemon would accept out of a URL is a password in a browser history, a
// referrer header and every proxy log in between — so a submission that puts it
// there must fail, and this is what holds PostForm in place against a Form that
// would look identical in every other test.
//
// **Must fail when** a refusal gains a distinguishing byte, when a refused
// attempt sets a cookie, or when the password is accepted from anywhere but the
// posted body.
func TestEveryRefusedSignInIsTheSameRefusal(t *testing.T) {
	t.Parallel()

	for _, c := range []struct {
		name    string
		request func(t *testing.T) *http.Request
		want    error
		why     string
	}{
		{
			name: "no password field at all",
			request: func(*testing.T) *http.Request {
				return signInWith(url.Values{})
			},
			want: errLoginRefused,
			why:  "an empty submission is a wrong answer, not a missing one; there is no third sentinel to time",
		},
		{
			name: "an empty password",
			request: func(*testing.T) *http.Request {
				return signInWith(url.Values{fieldPassword: {""}})
			},
			want: errLoginRefused,
			why:  "a daemon that treated the empty string as absent would be a daemon with a branch on presence",
		},
		{
			name: "the wrong password",
			request: func(*testing.T) *http.Request {
				return signInWith(url.Values{fieldPassword: {"test-only-not-the-password"}})
			},
			want: errLoginRefused,
			why:  "the ordinary guess, and the one the limiter in T005 is about",
		},
		{
			name: "the password with a trailing space",
			request: func(*testing.T) *http.Request {
				return signInWith(url.Values{fieldPassword: {testDashboardPassword + " "}})
			},
			want: errLoginRefused,
			why:  "nothing here trims: a password that is nearly right is wrong",
		},
		{
			name: "the password in the query string",
			request: func(*testing.T) *http.Request {
				r := signInWith(url.Values{})
				r.URL.RawQuery = url.Values{fieldPassword: {testDashboardPassword}}.Encode()
				return r
			},
			want: errLoginRefused,
			why:  "read from PostForm and never Form; a password in a URL is a password in three logs",
		},
		{
			name: "a body that is not a form",
			request: func(*testing.T) *http.Request {
				r := httptest.NewRequest(http.MethodPost, pathLogin, strings.NewReader("%zz"))
				r.Header.Set(headerContentType, contentTypeForm)
				return r
			},
			want: errLoginFormUnreadable,
			why:  "a form that never arrived and a form with the wrong answer in it are different faults",
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			d := newLoginDaemon(t)

			w := httptest.NewRecorder()
			d.ServeHTTP(w, c.request(t))

			if w.Code != http.StatusUnauthorized {
				t.Fatalf("the attempt was answered %d (%s); want %d: %s", w.Code, w.Body.String(), http.StatusUnauthorized, c.why)
			}
			if got := w.Body.String(); got != string(bodyBrowserRefused) {
				t.Errorf("the refusal is not this door's own bytes:\n%s", got)
			}
			if cookies := (&http.Response{Header: w.Header()}).Cookies(); len(cookies) != 0 {
				t.Errorf("a refused attempt set %d cookies; a credential must not leave beside a refusal", len(cookies))
			}

			rec := d.only(t)
			if got, want := rec["action"], string(audit.ActionLoginSubmit); got != want {
				t.Errorf("action = %v; want %v", got, want)
			}
			if got, want := rec["decision"], string(audit.Deny); got != want {
				t.Errorf("decision = %v; want %v", got, want)
			}
			if got, want := rec["reason"], c.want.Error(); got != want {
				t.Errorf("reason = %v; want %v: %s", got, want, c.why)
			}
			if trail := d.sink.String(); strings.Contains(trail, testDashboardPassword) {
				t.Errorf("the trail carries the password:\n%s", trail)
			}
		})
	}
}

// signInWith is one posted sign-in form, built the way the page submits it.
func signInWith(form url.Values) *http.Request {
	r := httptest.NewRequest(http.MethodPost, pathLogin, strings.NewReader(form.Encode()))
	r.Header.Set(headerContentType, contentTypeForm)
	return r
}

// TestTheSignInSentinelsAreFourDifferentStrings is what keeps the table above
// honest.
//
// Every row asserts the reason it expects, so two sentinels that had been
// reworded into one string would satisfy every one of them while leaving an
// operator unable to tell a malformed submission from a rejected guess — or,
// since T005, a guess from a guess nobody read and a guess somebody else's page
// submitted. The caller is told none of it, so the trail is the only place these
// four events are ever distinguishable, and a duplicate here is a distinction
// that has already been lost.
func TestTheSignInSentinelsAreFourDifferentStrings(t *testing.T) {
	t.Parallel()

	sentinels := map[string]error{
		"a form that could not be read":  errLoginFormUnreadable,
		"a wrong password":               errLoginRefused,
		"a source out of budget":         errLoginRateExceeded,
		"a submission from another site": errLoginCrossSite,
	}
	seen := make(map[string]string, len(sentinels))
	for what, err := range sentinels {
		if also, clash := seen[err.Error()]; clash {
			t.Errorf("%s and %s record the same reason (%q); the trail is the only place they are told apart", what, also, err)
		}
		seen[err.Error()] = what
	}
}

// TestASignInLeavesOneRecordAndNoPasswordAnywhere is FR-041 and FR-042 on the
// one route that handles the secret itself.
//
// One record per attempt, allowed or denied, and the allowed one names the owner
// both doors resolve to rather than an identity of its own — an operator reading
// their journal must be able to count sign-ins the same way they count
// everything else this caller does.
//
// The page view is asserted beside it because it is a different event: a scanner
// fetching the form a thousand times must not be counted with a thousand guesses
// at the password.
func TestASignInLeavesOneRecordAndNoPasswordAnywhere(t *testing.T) {
	t.Parallel()

	t.Run("the form served", func(t *testing.T) {
		t.Parallel()

		d := newLoginDaemon(t)
		d.get(t, pathLogin)

		rec := d.only(t)
		if got, want := rec["action"], string(audit.ActionLoginView); got != want {
			t.Errorf("action = %v; want %v", got, want)
		}
		if got, want := rec["decision"], string(audit.Allow); got != want {
			t.Errorf("decision = %v; want %v", got, want)
		}
		// Nobody has proved anything by asking for the form, and the record says so
		// rather than naming the operator.
		if got, want := rec["caller"], audit.CallerUnknown; got != want {
			t.Errorf("caller = %v; want %v — asking for the form identifies nobody", got, want)
		}
	})

	t.Run("the form answered", func(t *testing.T) {
		t.Parallel()

		d := newLoginDaemon(t)
		d.signIn(t)

		rec := d.only(t)
		if got, want := rec["action"], string(audit.ActionLoginSubmit); got != want {
			t.Errorf("action = %v; want %v", got, want)
		}
		if got, want := rec["decision"], string(audit.Allow); got != want {
			t.Errorf("decision = %v; want %v", got, want)
		}
		if got, want := rec["caller"], string(auth.CallerOperator); got != want {
			t.Errorf("caller = %v; want %v", got, want)
		}
		if trail := d.sink.String(); strings.Contains(trail, testDashboardPassword) {
			t.Errorf("the trail carries the password:\n%s", trail)
		}
	})
}

// TestTheSignInRoutesCarryTheBrowserDoorsHeaders is FR-026 on the two routes
// that do not go through the middleware that writes them.
//
// These are the only responses on this door assembled by a middleware of their
// own, which is exactly how a header set gets lost: the page renders, the form
// works, and the one page a stranger can reach is the one served without a
// content security policy. `no-store` is asserted with them, because a cached
// copy of a sign-in page is a cached copy of an authorisation decision.
//
// **Must fail when** serveLogin stops writing the headers, or writes them after
// a handler has already answered.
func TestTheSignInRoutesCarryTheBrowserDoorsHeaders(t *testing.T) {
	t.Parallel()

	d := newLoginDaemon(t)
	for name, w := range map[string]*httptest.ResponseRecorder{
		"the sign-in page":     d.get(t, pathLogin),
		"a refused attempt":    d.submit(t, url.Values{fieldPassword: {"test-only-not-the-password"}}),
		"a successful sign-in": d.submit(t, url.Values{fieldPassword: {testDashboardPassword}}),
	} {
		for header, want := range securityHeaders {
			if got := w.Header().Get(header); got != want {
				t.Errorf("%s: %s = %q; want %q", name, header, got, want)
			}
		}
		if got := w.Header().Get(headerCacheControl); got != cacheControlNoStore {
			t.Errorf("%s: %s = %q; want %q", name, headerCacheControl, got, cacheControlNoStore)
		}
	}
}

// TestTheSignInPageIsAFormThatWorksWithNothingRunning is the plan's "everything
// works with no JavaScript", read against the bytes a browser is handed.
//
// The two spellings are held together here rather than trusted: the address the
// form posts to and the field it submits are constants in login.go, and a
// template is parsed with no function map, so both are typed a second time in
// the markup. A drift in either renders a page that looks right and signs
// nobody in.
func TestTheSignInPageIsAFormThatWorksWithNothingRunning(t *testing.T) {
	t.Parallel()

	page := newLoginDaemon(t).get(t, pathLogin).Body.String()

	for _, want := range []string{
		`method="post"`,
		`action="` + pathLogin + `"`,
		`name="` + fieldPassword + `"`,
		`type="password"`,
		// A real label, per docs/components.md's Form rules: a placeholder is not
		// a label.
		`<label class="field-label" for="login-password">`,
		`class="field-input"`,
		`class="button button-primary"`,
	} {
		if !strings.Contains(page, want) {
			t.Errorf("the sign-in page carries no %s:\n%s", want, page)
		}
	}
	if strings.Contains(page, "<script") {
		t.Errorf("the sign-in page loads a script; this form works with nothing running:\n%s", page)
	}
	// No new class, which is the plan's own bound on this page. Anything outside
	// the vocabulary docs/components.md already defines would be a second
	// spelling of a control that exists.
	for _, attr := range classAttr.FindAllStringSubmatch(page, -1) {
		for _, class := range strings.Fields(attr[1]) {
			if !loginPageVocabulary[class] {
				t.Errorf("the sign-in page renders %q, which is not in the vocabulary it was allowed: %v", class, loginPageVocabulary)
			}
		}
	}
}

// loginPageVocabulary is every class the sign-in page may render: the four the
// plan names, the wordmark it says what it is with, and the shell every page in
// this tree is laid out in. A fifth is a new component, which is the thing
// docs/components.md exists to prevent.
var loginPageVocabulary = map[string]bool{
	"shell":          true,
	"brand":          true,
	"brand-tag":      true,
	"field":          true,
	"field-label":    true,
	"field-input":    true,
	"button":         true,
	"button-primary": true,
}

// TestTheSignInPageTellsAStrangerNothingAboutThisDaemon is the disclosure half,
// and it is the reason this page composes no header.
//
// It is the one page this daemon serves to somebody it has not identified, so
// everything on it is public. The version in particular: every other page shows
// it, GET /dashboard/version is deliberately behind the door because "a version
// is exactly the fact a scanner would like for free", and a login page carrying
// one would hand over what that route refuses to.
//
// **Must fail when** the page gains a header, a version, an operator, or the
// password it is asking for.
func TestTheSignInPageTellsAStrangerNothingAboutThisDaemon(t *testing.T) {
	t.Parallel()

	d := newLoginDaemon(t)
	page := d.get(t, pathLogin).Body.String()

	for _, disclosed := range []struct {
		what string
		text string
	}{
		{what: "the configured password", text: testDashboardPassword},
		{what: "the shared secret", text: string(testSecret())},
		{what: "an operator identity", text: passwordOperator},
		{what: "the address it listens on", text: loopbackListen},
		{what: "a route only a verified operator may reach", text: settingsPath},
	} {
		if strings.Contains(page, disclosed.text) {
			t.Errorf("the sign-in page discloses %s to a caller it has not identified:\n%s", disclosed.what, page)
		}
	}

	// The version is asked of the template rather than of the rendered bytes: a
	// development build's version string is short enough to appear in this page by
	// coincidence, and what is being asserted is that the page never asks for it.
	if strings.Contains(loginTemplateSource(t), "version") {
		t.Error("the sign-in page renders the running version; a scanner is owed less than an operator who is already in")
	}
}

// TestASignInDaemonStillRefusesEveryOtherRouteWithoutTheCookie is the bound on
// what these two routes opened.
//
// They are registered in front of layer 1, which is a sentence worth being
// nervous about. What keeps it safe is that they are exactly two: every other
// path on this daemon — the fleet, the settings page, an action, a path nothing
// claims — still meets the door and still answers the uniform refusal to a
// browser that has not signed in.
//
// **Must fail when** the sign-in middleware is put in front of anything else.
func TestASignInDaemonStillRefusesEveryOtherRouteWithoutTheCookie(t *testing.T) {
	t.Parallel()

	d := newLoginDaemon(t)
	for _, target := range []string{
		pathFleet,
		settingsPath,
		versionPath,
		"/not-a-route",
		pathLogin + "/",
	} {
		w := d.get(t, target)
		if w.Code != http.StatusUnauthorized {
			t.Errorf("GET %s with no cookie = %d (%s); want %d — only the sign-in route is in front of layer 1",
				target, w.Code, w.Body.String(), http.StatusUnauthorized)
		}
		if got := w.Body.String(); got != string(bodyBrowserRefused) {
			t.Errorf("GET %s answered something other than the door's uniform refusal:\n%s", target, got)
		}
	}
}

// TestTheSignInPageIsNotServedToAnAccessDaemonsOperatorEither is the sweep half
// of the registration claim, made against a Config rather than a route table.
//
// A daemon that names a password *and* an Access door is one config.Load refuses
// to produce (validateDoors), and verifiedLayer1 hands back the Access door for
// it anyway. This asserts the consequence rather than the choice: the login route
// follows the door that was built, so a hand-built Config carrying both still
// gets no login form.
func TestTheSignInPageIsNotServedToAnAccessDaemonsOperatorEither(t *testing.T) {
	t.Parallel()

	cfg := passwordConfig(loopbackListen)
	access := testConfig(loopbackListen)
	cfg.AccessTeamDomain, cfg.AccessAUD, cfg.AccessAllowedEmails = access.AccessTeamDomain, access.AccessAUD, access.AccessAllowedEmails

	door, err := verifiedLayer1(cfg)
	if err != nil {
		t.Fatalf("verifiedLayer1 = _, %v; want a door", err)
	}
	s := newAuditedServerOn(t, cfg, door)

	w := httptest.NewRecorder()
	s.ServeHTTP(w, httptest.NewRequest(http.MethodGet, pathLogin, nil))

	if strings.Contains(w.Body.String(), `name="`+fieldPassword+`"`) {
		t.Errorf("a daemon whose door is Access served a sign-in form:\n%s", w.Body.String())
	}
}

// --- The budget in front of the door (M12/T005) ----------------------------

// wrongGuess is a password that is not the configured one, spelled once.
const wrongGuess = "test-only-not-the-password"

// The two addresses these tests count attempts against. Both are documentation
// ranges (RFC 5737), like the one httptest.NewRequest fills in, so nothing here
// names a host that could exist.
const (
	oneSource     = "198.51.100.7"
	anotherSource = "203.0.113.9"
)

// meterSignIns puts the daemon's sign-in budget on a clock this test moves, and
// hands back both the clock and the burst.
//
// The limiter is installed after the server is built rather than passed to it,
// which is the seam newRateFixture already uses for the create budget and the
// one stream_test.go uses for the stream cap. It is also what makes the wiring
// claim honest: the limiter installed here is the one the route has to consult
// for any of these tests to see a refusal at all.
//
// A clock of its own because the daemon's is the host's: a burst refused by a
// limiter that is also refilling while the test runs proves the refusal and not
// the recovery, and the recovery is half of what a rate limit promises.
func (d *loginDaemon) meterSignIns(t *testing.T) (*testClock, int) {
	t.Helper()

	clk := newTestClock(testTime)
	d.logins = testLoginLimiter(t, clk)
	return clk, burstFor(loginRatePerMin)
}

// attempt posts one sign-in from a named address.
func (d *loginDaemon) attempt(t *testing.T, from string, password string) *httptest.ResponseRecorder {
	t.Helper()

	r := signInWith(url.Values{fieldPassword: {password}})
	r.RemoteAddr = from

	w := httptest.NewRecorder()
	d.ServeHTTP(w, r)
	return w
}

// form asks for the sign-in page from a named address.
//
// The address matters, which is the whole reason this exists beside get: a
// budget claim made about two verbs arriving from *different* sources is a claim
// about two buckets, and it holds however the one bucket was spent. Both halves
// of the page's test therefore name one source and use it throughout.
func (d *loginDaemon) form(t *testing.T, from string) *httptest.ResponseRecorder {
	t.Helper()

	r := httptest.NewRequest(http.MethodGet, pathLogin, nil)
	r.RemoteAddr = from

	w := httptest.NewRecorder()
	d.ServeHTTP(w, r)
	return w
}

// refusedBy asserts the uniform refusal and the reason the trail kept for it.
//
// Every refusal on this route is the same 401 with the same bytes and no cookie,
// whichever of the four it was, so the response cannot be what a test reads to
// tell them apart — reading the trail is the only way to assert the difference,
// which is exactly the property being asserted.
func (d *loginDaemon) refusedBy(t *testing.T, w *httptest.ResponseRecorder, want error, why string) {
	t.Helper()

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("the attempt was answered %d (%s); want %d: %s", w.Code, w.Body.String(), http.StatusUnauthorized, why)
	}
	if got := w.Body.String(); got != string(bodyBrowserRefused) {
		t.Errorf("the refusal is not this door's own bytes: %s", why)
	}
	if cookies := (&http.Response{Header: w.Header()}).Cookies(); len(cookies) != 0 {
		t.Errorf("a refused attempt set %d cookie(s); a credential must not leave beside a refusal", len(cookies))
	}

	records := d.records(t)
	if len(records) == 0 {
		t.Fatalf("the attempt left no audit record; one per attempt is the whole of what the operator can read: %s", why)
	}
	if got, want := records[len(records)-1]["reason"], want.Error(); got != want {
		t.Errorf("reason = %v; want %v: %s", got, want, why)
	}
}

// TestASignInBurstIsSpentAndThenRefused is T005's first half: a source may guess
// a few times at once, and then it may not.
//
// The claim is deliberately made about the *right* password as well as the wrong
// one. A limiter placed after the comparison would refuse every wrong guess past
// the burst and let a correct one through, which is a limiter that stops nobody
// who is about to succeed — so the budget is checked before the two sides are
// ever compared, and this is where that ordering is pinned.
//
// The recovery is asserted with it because a rate limit is a delay and not a
// ban. Nothing is reset by hand here: the only thing that changes is the clock
// the limiter reads.
//
// **Must fail when** the limiter is removed from admitLogin, moved below the
// password comparison, or keyed by something a single source varies per request.
func TestASignInBurstIsSpentAndThenRefused(t *testing.T) {
	t.Parallel()

	d := newLoginDaemon(t)
	clk, burst := d.meterSignIns(t)

	for i := 0; i < burst; i++ {
		d.refusedBy(t, d.attempt(t, oneSource+":4001", wrongGuess), errLoginRefused,
			"a guess inside the burst is read and refused on its merits")
	}

	d.refusedBy(t, d.attempt(t, oneSource+":4002", wrongGuess), errLoginRateExceeded,
		"the guess past the burst is refused by the budget rather than by the password")
	d.refusedBy(t, d.attempt(t, oneSource+":4003", testDashboardPassword), errLoginRateExceeded,
		"the configured password does not buy its way past a spent budget")

	// One record per attempt, allowed or denied — T005 states it and the deferred
	// emit is what makes it true. A refusal that skipped the trail would be the
	// only event on this daemon an operator cannot count.
	if got, want := len(d.records(t)), burst+2; got != want {
		t.Errorf("%d attempts left %d audit records; want exactly one each", want, got)
	}
	if trail := d.sink.String(); strings.Contains(trail, testDashboardPassword) || strings.Contains(trail, wrongGuess) {
		t.Errorf("the trail carries password material:\n%s", trail)
	}

	clk.advance(time.Minute / loginRatePerMin)

	w := d.attempt(t, oneSource+":4004", testDashboardPassword)
	if w.Code != http.StatusSeeOther {
		t.Fatalf("the sign-in after a token's worth of time = %d (%s); want %d — a rate limit is a delay, not a ban",
			w.Code, w.Body.String(), http.StatusSeeOther)
	}
}

// TestTheSignInBudgetIsPerSourceAndNotPerConnection is the key itself, and it is
// two claims that fail in opposite directions.
//
// Keyed too narrowly — by the whole RemoteAddr, port and all — every attempt
// arrives on a new ephemeral port and gets a fresh budget, which is a limiter
// that permits everything while looking like one that does not. Keyed too widely
// — one bucket for the daemon — a stranger with a fast connection locks the
// operator out of their own dashboard by guessing badly enough.
//
// **Must fail when** sourceOf stops dropping the port, or the limiter stops
// keying by source at all.
func TestTheSignInBudgetIsPerSourceAndNotPerConnection(t *testing.T) {
	t.Parallel()

	d := newLoginDaemon(t)
	_, burst := d.meterSignIns(t)

	// Every one from a different port, which is what a browser really does.
	for i := 0; i < burst; i++ {
		d.refusedBy(t, d.attempt(t, fmt.Sprintf("%s:%d", oneSource, 5000+i), wrongGuess), errLoginRefused,
			"a guess inside the burst is read whichever port it came from")
	}
	d.refusedBy(t, d.attempt(t, oneSource+":5999", wrongGuess), errLoginRateExceeded,
		"a new connection from a spent source is not a new budget")

	for i := 0; i < burst; i++ {
		d.refusedBy(t, d.attempt(t, fmt.Sprintf("%s:%d", anotherSource, 6000+i), wrongGuess), errLoginRefused,
			"another source has its own budget; one host guessing must not lock the operator out")
	}
}

// TestTheSignInPageIsNotOnTheGuessBudget is the decision T005 takes about the
// other verb on this path, asserted from both sides.
//
// The budget is about guesses. Serving the form costs this daemon what refusing
// an unauthenticated request costs it on any other route of this door, none of
// which is limited either — while a budget a page load could spend is a budget
// an <img src="/login"> on a hostile page could empty, and what it would empty is
// the operator's own way in. So a spent source is still shown the form, and a
// page fetched all day still leaves every guess unspent.
//
// **Must fail when** the limiter is wrapped around handleLogin rather than
// applied to the submission, which is the tempting single line: both routes
// would then share one bucket.
func TestTheSignInPageIsNotOnTheGuessBudget(t *testing.T) {
	t.Parallel()

	t.Run("a spent source is still served the form", func(t *testing.T) {
		t.Parallel()

		d := newLoginDaemon(t)
		_, burst := d.meterSignIns(t)
		for i := 0; i <= burst; i++ {
			d.attempt(t, oneSource+":7000", wrongGuess)
		}

		if w := d.form(t, oneSource+":7001"); w.Code != http.StatusOK {
			t.Fatalf("GET %s from a source that is out of guesses = %d; want %d — the form is how a lockout ends",
				pathLogin, w.Code, http.StatusOK)
		}
	})

	t.Run("page loads spend no guesses", func(t *testing.T) {
		t.Parallel()

		d := newLoginDaemon(t)
		_, burst := d.meterSignIns(t)
		for i := 0; i <= burst; i++ {
			d.form(t, oneSource+":7002")
		}

		if w := d.attempt(t, oneSource+":7003", testDashboardPassword); w.Code != http.StatusSeeOther {
			t.Fatalf("the sign-in after %d page loads = %d (%s); want %d",
				burst+1, w.Code, w.Body.String(), http.StatusSeeOther)
		}
	})
}

// TestACrossSiteSignInIsRefusedAndSpendsNothing is the check the budget made
// necessary, which is why it arrives with the budget rather than before it.
//
// Without it the cheapest way to empty the operator's guesses is a hostile page
// that makes the operator's own browser submit them, from the operator's own
// address — a lockout an attacker holds for as long as the tab is open, on the
// one route that can end a lockout. It is not half an action gate: it authorises
// nobody, and the gate's other two checks cannot exist in front of the door that
// produces them.
//
// crossSite and not crossSiteAction, so an absent header is still read: a script
// signing in with curl sends none, and it has an address and therefore a budget
// of its own, which is what bounds it.
//
// **Must fail when** the check is dropped, moved below the limiter, or widened
// to refuse a header that is merely absent.
func TestACrossSiteSignInIsRefusedAndSpendsNothing(t *testing.T) {
	t.Parallel()

	t.Run("the browser's own verdict decides", func(t *testing.T) {
		t.Parallel()

		for _, c := range []struct {
			site string
			want error
			why  string
		}{
			{site: secFetchSiteSameOrigin, want: errLoginRefused, why: "the form this daemon rendered is what signs the operator in"},
			{site: "", want: errLoginRefused, why: "absent is not evidence of a foreign initiator, and a script has a budget of its own"},
			{site: "cross-site", want: errLoginCrossSite, why: "a hostile page must not spend the operator's guesses"},
			{site: "same-site", want: errLoginCrossSite, why: "only same-origin admits; a sibling host is not this page"},
			{site: "none", want: errLoginCrossSite, why: "no page asked for this, and the sign-in form is a page"},
		} {
			t.Run("Sec-Fetch-Site: "+c.site, func(t *testing.T) {
				t.Parallel()

				d := newLoginDaemon(t)
				d.meterSignIns(t)

				r := signInWith(url.Values{fieldPassword: {wrongGuess}})
				if c.site != "" {
					r.Header.Set(headerSecFetchSite, c.site)
				}
				w := httptest.NewRecorder()
				d.ServeHTTP(w, r)

				d.refusedBy(t, w, c.want, c.why)
			})
		}
	})

	t.Run("a refused submission costs no budget", func(t *testing.T) {
		t.Parallel()

		d := newLoginDaemon(t)
		_, burst := d.meterSignIns(t)

		for i := 0; i <= burst; i++ {
			r := signInWith(url.Values{fieldPassword: {wrongGuess}})
			r.RemoteAddr = oneSource + ":8000"
			r.Header.Set(headerSecFetchSite, "cross-site")
			d.ServeHTTP(httptest.NewRecorder(), r)
		}

		if w := d.attempt(t, oneSource+":8001", testDashboardPassword); w.Code != http.StatusSeeOther {
			t.Fatalf("the operator's sign-in after %d cross-site submissions from their own address = %d (%s); want %d",
				burst+1, w.Code, w.Body.String(), http.StatusSeeOther)
		}
	})
}

// loginTemplateSource is the page's markup as it is embedded, with its own
// commentary removed — for the one assertion that is about what the template
// asks for rather than about what a render happened to produce.
//
// The comment has to go: this file's own prose explains why the version is
// absent, and a sweep that read it would fail on the paragraph saying so.
func loginTemplateSource(t *testing.T) string {
	t.Helper()

	source, err := fs.ReadFile(web.Templates, "templates/login.html")
	if err != nil {
		t.Fatalf("read the sign-in page's template: %v", err)
	}
	return string(templateComment.ReplaceAll(source, nil))
}
