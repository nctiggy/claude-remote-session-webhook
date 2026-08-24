package httpapi

// logout_test.go covers the way out of the password door (M12/T007).
//
// Four claims carry the task, and each is a whole test below rather than an
// assertion inside another one:
//
//  1. The route exists on a password daemon and on no other, exactly as the
//     sign-in routes do — where Cloudflare Access is the door there is no cookie
//     of this daemon's to clear, and a Sign out that cleared nothing would be
//     worse than none.
//  2. Signing out really ends the session. Not that a Set-Cookie was written:
//     that the browser holding what came back is refused by the door afterwards.
//  3. It is behind the action gate like every other mutating browser route, and
//     each half of that gate refuses on its own — with the cookie surviving,
//     because a refused sign-out that cleared anything would be a route a hostile
//     page could use to log the operator out at will.
//  4. The settings page offers it, on the section that asks the question it
//     answers, and only where it is registered.

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/nctiggy/claude-remote-session-webhook/internal/audit"
	"github.com/nctiggy/claude-remote-session-webhook/internal/auth"
)

// signOutForm is what the settings page's Sign out button submits: the page
// token minted for the identity this door hands on, and nothing else.
//
// passwordOperator and not testOperatorEmail, which is the difference between
// this route's fixtures and every other action's: the token is bound to the
// identity layer 1 verified on the request, and on this door that identity is
// the one the cookie produces rather than an address the edge signed.
func signOutForm(t *testing.T, d *loginDaemon) url.Values {
	t.Helper()

	form := url.Values{}
	form.Set(fieldPageToken, mustMint(t, d.pageKey, passwordOperator, testTime))
	return form
}

// signOut posts that form the way a browser does, with the credential and the
// browser's own account of where the request came from chosen by the caller —
// the two things the gate's cases have to vary and a rendered form never does.
func (d *loginDaemon) signOut(t *testing.T, cookie *http.Cookie, form url.Values, site string) *httptest.ResponseRecorder {
	t.Helper()
	return d.sendAs(t, http.MethodPost, pathLogout, cookie, form, site)
}

func (d *loginDaemon) sendAs(t *testing.T, method, target string, cookie *http.Cookie, form url.Values, site string) *httptest.ResponseRecorder {
	t.Helper()

	r := httptest.NewRequest(method, target, strings.NewReader(form.Encode()))
	r.Header.Set(headerContentType, contentTypeForm)
	if cookie != nil {
		//nolint:gosec // G124: a request-side cookie has no attributes to set.
		r.AddCookie(&http.Cookie{Name: cookie.Name, Value: cookie.Value})
	}
	if site != absent {
		r.Header.Set(headerSecFetchSite, site)
	}

	w := httptest.NewRecorder()
	d.ServeHTTP(w, r)
	return w
}

// setCookies is every cookie a response wrote, which is what the refusal cases
// assert the absence of.
func setCookies(w *httptest.ResponseRecorder) []*http.Cookie {
	return (&http.Response{Header: w.Header()}).Cookies()
}

// opensTheFleet reports whether a browser holding this cookie is still admitted.
//
// It is the only honest way to ask whether a sign-out worked. A Set-Cookie in
// the answer says what this daemon *asked* the browser to do; this says what the
// door does with what the browser has, which is the property the operator is
// pressing the button for.
func (d *loginDaemon) opensTheFleet(t *testing.T, cookie *http.Cookie) bool {
	t.Helper()
	return d.openAs(t, pathFleet, cookie).Code == http.StatusOK
}

// TestTheSignOutRouteExistsOnlyOnAPasswordDaemon is the registration decision,
// asked of all three doors, and it is the same claim the sign-in routes carry
// for the same reason: a control that appears where it cannot work is a lie
// about the daemon.
//
// The Access row is the one worth having. That browser holds Cloudflare's own
// CF_Authorization, which this daemon does not read and could not end — so a
// route answering "signed out" there would send the operator away believing
// something that did not happen, and their next request would be served exactly
// as before. It is asked as a *verified* operator with a valid token, because
// that is the only caller who can tell a route that does not exist from a door
// that refused.
//
// **Must fail when** the registration stops asking which door was built — a
// daemon that registered this route unconditionally, or from cfg.
// DashboardPassword rather than from the door newServer was handed.
func TestTheSignOutRouteExistsOnlyOnAPasswordDaemon(t *testing.T) {
	t.Parallel()

	t.Run("the password door serves it", func(t *testing.T) {
		t.Parallel()

		d := newLoginDaemon(t)
		w := d.signOut(t, d.signIn(t), signOutForm(t, d), secFetchSiteSameOrigin)

		if w.Code != http.StatusSeeOther {
			t.Fatalf("POST %s as the signed-in operator = %d (%s); want %d",
				pathLogout, w.Code, w.Body.String(), http.StatusSeeOther)
		}
		if got := w.Header().Get("Location"); got != pathLogin {
			t.Errorf("a sign-out sends the browser to %q; want %q — the form is the only answer that shows it worked",
				got, pathLogin)
		}
	})

	t.Run("an Access daemon answers the uniform not-found", func(t *testing.T) {
		t.Parallel()

		f := newFleet(t)

		form := url.Values{}
		form.Set(fieldPageToken, mustMint(t, f.pageKey, testOperatorEmail, testTime))
		r := httptest.NewRequest(http.MethodPost, pathLogout, strings.NewReader(form.Encode()))
		r.Header.Set(headerContentType, contentTypeForm)
		r.Header.Set(headerAccessAssertion, f.keys.mint(t, f.keys.claims()))
		r.Header.Set(headerSecFetchSite, secFetchSiteSameOrigin)

		w := httptest.NewRecorder()
		f.ServeHTTP(w, r)

		if w.Code != http.StatusNotFound {
			t.Fatalf("POST %s on an Access daemon = %d (%s); want %d — this route is not registered there",
				pathLogout, w.Code, w.Body.String(), http.StatusNotFound)
		}
		if !strings.Contains(w.Body.String(), notFoundTitle) {
			t.Errorf("POST %s on an Access daemon answered something other than the not-found page:\n%s",
				pathLogout, w.Body.String())
		}
		// And it did not touch the browser's credential on the way to refusing.
		if got := setCookies(w); len(got) != 0 {
			t.Errorf("an Access daemon set %d cookies answering %s; want none", len(got), pathLogout)
		}
	})

	t.Run("a daemon with no door refuses as it refuses everything", func(t *testing.T) {
		t.Parallel()

		cfg := noDoorConfig(loopbackListen)
		s := newAuditedServerOn(t, cfg, doorFor(t, cfg))

		w := httptest.NewRecorder()
		s.ServeHTTP(w, httptest.NewRequest(http.MethodPost, pathLogout, nil))

		if w.Code != http.StatusUnauthorized {
			t.Fatalf("POST %s on a closed-door daemon = %d (%s); want %d",
				pathLogout, w.Code, w.Body.String(), http.StatusUnauthorized)
		}
		if got := w.Body.String(); got != string(bodyBrowserRefused) {
			t.Errorf("a closed-door daemon answered %s with something other than the door's uniform refusal:\n%s", pathLogout, got)
		}
	})
}

// TestSigningOutEndsTheSessionItWasGiven is the task's own definition of done.
//
// Not that the handler wrote a Set-Cookie — that a browser holding what came
// back is refused by the door afterwards, which is the whole reason an operator
// presses the button. It is driven through the real router, the real gate and
// the real middleware, because a route that cleared a cookie under a path the
// door never issued one on would pass every assertion short of this one and
// leave the operator signed in.
//
// **Must fail when** the deletion drifts from the issue — a different name or a
// narrower path, either of which leaves the browser holding a cookie it was
// never asked to drop.
func TestSigningOutEndsTheSessionItWasGiven(t *testing.T) {
	t.Parallel()

	d := newLoginDaemon(t)
	cookie := d.signIn(t)

	// The premise: before the sign-out, this cookie is a working credential.
	if !d.opensTheFleet(t, cookie) {
		t.Fatal("the cookie the sign-in issued does not open the fleet, so this test proves nothing about ending it")
	}

	w := d.signOut(t, cookie, signOutForm(t, d), secFetchSiteSameOrigin)
	if w.Code != http.StatusSeeOther {
		t.Fatalf("POST %s = %d (%s); want %d", pathLogout, w.Code, w.Body.String(), http.StatusSeeOther)
	}

	cleared := setCookies(w)
	if len(cleared) != 1 {
		t.Fatalf("a sign-out set %d cookies; want exactly 1", len(cleared))
	}
	if cleared[0].Name != cookieDashboardSession {
		t.Fatalf("a sign-out cleared the cookie %q; want %q", cleared[0].Name, cookieDashboardSession)
	}
	if cleared[0].MaxAge >= 0 {
		t.Errorf("the cleared cookie's Max-Age is %d; want a negative one, which is the browser's instruction to drop it",
			cleared[0].MaxAge)
	}

	// What the browser is left holding, presented back: a client that applied the
	// deletion sends nothing, and one that did not sends this. Both are refused,
	// and the second is the one only this assertion covers.
	if d.opensTheFleet(t, cleared[0]) {
		t.Error("the value a sign-out left in the browser still opens the fleet")
	}
	if d.get(t, pathFleet).Code != http.StatusUnauthorized {
		t.Error("a browser that dropped the cookie is still admitted, so the door is not reading it at all")
	}
}

// TestTheClearedCookieIsTheOneTheSignInIssued holds the two ends of this
// cookie's life to one description of it.
//
// A deletion is matched by the browser on name, domain and path, so an attribute
// that drifts is a Sign out that reports success and changes nothing — the
// single failure this route can have that looks exactly like working. The
// comparison is against `issue`'s own output rather than against constants
// written here, so the two cannot agree with this test while disagreeing with
// each other.
//
// Secure is the one that varies, and it follows the transport for issue's
// reason: a browser that would not accept a Secure cookie over plaintext will
// not accept the deletion of one either, and the LAN this door exists for has no
// TLS on it.
//
// **Must fail when** clear stops mirroring issue in any of the four, or when
// Secure is pinned either way.
func TestTheClearedCookieIsTheOneTheSignInIssued(t *testing.T) {
	t.Parallel()

	for _, c := range []struct {
		name       string
		target     string
		wantSecure bool
	}{
		{name: "over plaintext", target: "http://crswd.example.com/", wantSecure: false},
		{name: "over TLS", target: "https://crswd.example.com/", wantSecure: true},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			door := testPasswordDoor(t)
			r := httptest.NewRequest(http.MethodPost, c.target, nil)

			issued := httptest.NewRecorder()
			if err := door.issue(issued, r); err != nil {
				t.Fatalf("issue = %v; want a cookie", err)
			}
			opened := setCookies(issued)
			if len(opened) != 1 {
				t.Fatalf("issue set %d cookies; want exactly 1", len(opened))
			}

			ended := httptest.NewRecorder()
			door.clear(ended, r)
			closedOut := setCookies(ended)
			if len(closedOut) != 1 {
				t.Fatalf("clear set %d cookies; want exactly 1", len(closedOut))
			}

			// Independent statements and never a chain: each attribute is what makes
			// the browser match the deletion against what it holds, and a chain that
			// stopped at the first mismatch would let one breakage hide the next.
			if closedOut[0].Name != opened[0].Name {
				t.Errorf("clear names the cookie %q; issue named it %q — the browser drops neither", closedOut[0].Name, opened[0].Name)
			}
			if closedOut[0].Path != opened[0].Path {
				t.Errorf("clear sets Path %q; issue set %q — the browser keeps the one it has", closedOut[0].Path, opened[0].Path)
			}
			if !closedOut[0].HttpOnly {
				t.Error("the deletion is not HttpOnly, so it does not describe the cookie it is replacing")
			}
			if closedOut[0].SameSite != opened[0].SameSite {
				t.Errorf("clear sets SameSite %v; issue set %v", closedOut[0].SameSite, opened[0].SameSite)
			}
			if closedOut[0].Secure != c.wantSecure {
				t.Errorf("clear sets Secure %t; want %t — it follows the transport exactly as issue does", closedOut[0].Secure, c.wantSecure)
			}
			if closedOut[0].Value != "" {
				t.Errorf("clear leaves the value %q; want it emptied", closedOut[0].Value)
			}
			if closedOut[0].MaxAge >= 0 {
				t.Errorf("clear sets Max-Age %d; want a negative one", closedOut[0].MaxAge)
			}
		})
	}
}

// TestTheSignOutRouteIsBehindTheActionGate is the half of this task that is
// about refusing, and every row is asserted with the *other* checks satisfied:
// two checks never tested apart are one check with extra steps (FR-002c).
//
// The second assertion in each row is the one this route needs and the others do
// not. A refused sign-out must leave the session exactly where it was — a route
// that cleared the cookie on its way to answering 403 would be a route any
// hostile page could use to log the operator out of an interface that starts
// unsandboxed shells, which is a denial of service dressed as a safety check.
//
// AR-005: the rows satisfy the gate and vary one thing. Nothing here switches a
// check off.
//
// **Must fail when** the route is registered with handleBrowser instead of
// handleAction, which removes both halves at once and which no test of the
// handler alone would notice.
func TestTheSignOutRouteIsBehindTheActionGate(t *testing.T) {
	t.Parallel()

	for _, c := range []struct {
		name string
		form func(*testing.T, *loginDaemon) url.Values
		site string
		want int
	}{
		{
			name: "with no page token",
			form: func(*testing.T, *loginDaemon) url.Values { return url.Values{} },
			site: secFetchSiteSameOrigin,
			want: http.StatusForbidden,
		},
		{
			name: "with a token minted for somebody else",
			form: func(t *testing.T, d *loginDaemon) url.Values {
				t.Helper()
				form := url.Values{}
				form.Set(fieldPageToken, mustMint(t, d.pageKey, "somebody-else@example.com", testTime))
				return form
			},
			site: secFetchSiteSameOrigin,
			want: http.StatusForbidden,
		},
		{
			name: "submitted from another site",
			form: signOutForm,
			site: "cross-site",
			want: http.StatusForbidden,
		},
		{
			name: "submitted with no initiator at all",
			form: signOutForm,
			site: absent,
			want: http.StatusForbidden,
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			d := newLoginDaemon(t)
			cookie := d.signIn(t)

			w := d.signOut(t, cookie, c.form(t, d), c.site)
			if w.Code != c.want {
				t.Fatalf("POST %s %s = %d (%s); want %d", pathLogout, c.name, w.Code, w.Body.String(), c.want)
			}
			if got := setCookies(w); len(got) != 0 {
				t.Errorf("a refused sign-out set %d cookies; want none — a refusal must not end the session it turned away", len(got))
			}
			if !d.opensTheFleet(t, cookie) {
				t.Error("a refused sign-out ended the session anyway, so anyone who can cause this request can log the operator out")
			}
		})
	}

	// The one refusal that is layer 1's rather than the gate's: nobody at all.
	// It answers 401 and not 403, because what failed is the door's promise and
	// not the caller's request — and a stranger must not learn from a sign-out
	// that this daemon has one.
	t.Run("with no session to end", func(t *testing.T) {
		t.Parallel()

		d := newLoginDaemon(t)
		w := d.signOut(t, nil, signOutForm(t, d), secFetchSiteSameOrigin)

		if w.Code != http.StatusUnauthorized {
			t.Fatalf("POST %s carrying no cookie = %d (%s); want %d", pathLogout, w.Code, w.Body.String(), http.StatusUnauthorized)
		}
		if got := w.Body.String(); got != string(bodyBrowserRefused) {
			t.Errorf("a sign-out with no session answered something other than the door's uniform refusal:\n%s", got)
		}
	})
}

// TestAGetOnTheSignOutRouteIsAPathNothingClaims keeps this route's wrong-method
// answer the same as every other action's.
//
// A 405 with an `Allow` header would confirm the path exists to a caller who
// only guessed at it, which is the enumeration the uniform not-found closes
// everywhere else on this door — and `/logout` is a path somebody types.
//
// **Must fail when** the pattern loses its method, which makes GET reach the
// handler and a sign-out something a link can cause.
func TestAGetOnTheSignOutRouteIsAPathNothingClaims(t *testing.T) {
	t.Parallel()

	d := newLoginDaemon(t)
	cookie := d.signIn(t)
	w := d.openAs(t, pathLogout, cookie)

	if w.Code != http.StatusNotFound {
		t.Fatalf("GET %s = %d (%s); want %d", pathLogout, w.Code, w.Body.String(), http.StatusNotFound)
	}
	if got := w.Header().Get("Allow"); got != "" {
		t.Errorf("the refusal carries Allow: %q, which names the method that does work", got)
	}
	if got := setCookies(w); len(got) != 0 {
		t.Errorf("a GET set %d cookies; want none", len(got))
	}
	if !d.opensTheFleet(t, cookie) {
		t.Error("a GET ended the session, so a sign-out is something an <img> tag can cause")
	}
}

// TestASignOutLeavesOneRecordSayingWhatItWas is FR-041 on this route, and the
// action is the point of it.
//
// login.signout and not login.submit: an operator counting attempts at their
// password must not be counting departures with them. The caller is the identity
// the door verified rather than the `unknown` a sign-in's own records carry,
// because by the time this route runs the caller is somebody.
func TestASignOutLeavesOneRecordSayingWhatItWas(t *testing.T) {
	t.Parallel()

	d := newLoginDaemon(t)
	cookie := d.signIn(t)
	// The sign-in wrote one of its own, so this asks what the *sign-out* added
	// rather than resetting the trail — a suite that emptied the journal to make
	// an assertion easy would be one that could no longer see a route writing
	// twice.
	before := len(d.records(t))

	if w := d.signOut(t, cookie, signOutForm(t, d), secFetchSiteSameOrigin); w.Code != http.StatusSeeOther {
		t.Fatalf("POST %s = %d (%s); want %d", pathLogout, w.Code, w.Body.String(), http.StatusSeeOther)
	}

	after := d.records(t)
	if len(after) != before+1 {
		t.Fatalf("a sign-out emitted %d audit records; FR-041 requires exactly one", len(after)-before)
	}
	rec := after[len(after)-1]
	if got, want := rec["action"], string(audit.ActionLoginSignOut); got != want {
		t.Errorf("action = %v; want %v — a departure is not an attempt at the password", got, want)
	}
	if got, want := rec["decision"], string(audit.Allow); got != want {
		t.Errorf("decision = %v; want %v", got, want)
	}
	if got, want := rec["caller"], string(auth.CallerOperator); got != want {
		t.Errorf("caller = %v; want %v", got, want)
	}
}

// TestTheSettingsPageOffersTheWayOut is the other half of "done": a route
// nothing points at is a form nobody can find.
//
// It is asserted on the section whose heading is the question the control
// answers, and asserted absent on the daemon where it would not work — the
// Access row is the one that matters, because there the browser's credential is
// the edge's and this daemon cannot end it.
//
// **Must fail when** the control is drawn from something other than the door the
// server was built with, or lands on a section that is not the door's.
func TestTheSettingsPageOffersTheWayOut(t *testing.T) {
	t.Parallel()

	t.Run("behind the dashboard password", func(t *testing.T) {
		t.Parallel()

		section := doorSectionAsOperator(t, newLoginDaemon(t))
		if !strings.Contains(section, `action="`+pathLogout+`"`) {
			t.Errorf("the door section offers no way out:\n%s", section)
		}
		if !strings.Contains(section, `name="`+fieldPageToken+`"`) {
			t.Errorf("the sign-out form carries no page token, so the gate is certain to refuse it:\n%s", section)
		}
	})

	t.Run("behind Cloudflare Access there is nothing to offer", func(t *testing.T) {
		t.Parallel()

		f := newFleet(t)
		section := settingsSectionBody(t, f, sectionWhoMayReachIt)
		if strings.Contains(section, pathLogout) {
			t.Errorf("an Access daemon draws a Sign out that would clear a cookie it did not issue:\n%s", section)
		}
	})

	// Where it is not is the half nobody tests. The page shows one section at a
	// time, so a control composed onto the view rather than onto its section
	// would follow the operator into General — and the fact that the route is
	// registered is not a reason for every heading to offer it.
	t.Run("and nowhere but the section that asks the question", func(t *testing.T) {
		t.Parallel()

		d := newLoginDaemon(t)
		cookie := d.signIn(t)

		elsewhere := []string{sectionUpdates}
		for _, section := range sectioned(settingsOf(testConfig(loopbackListen)), doorFacts{}) {
			if section.Title != sectionWhoMayReachIt {
				elsewhere = append(elsewhere, section.Title)
			}
		}
		if len(elsewhere) < 2 {
			t.Fatal("this page has no section besides the door's, so nothing below asserts where the control is not")
		}

		for _, title := range elsewhere {
			w := d.openAs(t, settingsPath+"?"+querySection+"="+url.QueryEscape(title), cookie)
			if w.Code != http.StatusOK {
				t.Fatalf("GET the %s section as the signed-in operator = %d; want %d", title, w.Code, http.StatusOK)
			}
			if strings.Contains(w.Body.String(), pathLogout) {
				t.Errorf("section %q offers the way out; it belongs under %q alone:\n%s", title, sectionWhoMayReachIt, w.Body.String())
			}
		}
	})
}
