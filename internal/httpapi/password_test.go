package httpapi

// password_test.go covers the third layer 1 (M12/T003): the door for a daemon
// with no Cloudflare in front of it.
//
// The negative cases are the point, as they are for the assertion door. A
// password door that admits the right cookie is easy; what is in doubt is
// whether a cookie signed under a different secret, under a password that has
// since changed, past its expiry, or with one hex digit moved is told apart from
// the operator's — and whether the telling apart leaks anything, either to the
// caller or onto the trail.

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/nctiggy/claude-remote-session-webhook/internal/audit"
	"github.com/nctiggy/claude-remote-session-webhook/internal/auth"
	"github.com/nctiggy/claude-remote-session-webhook/internal/config"
)

// testDashboardPassword is spelled in words rather than in anything credential
// shaped, for the reason testSecret is: gitleaks refuses a run of hex digits of
// credential length into this repository, correctly. It is past
// config.MinDashboardPasswordLen, so a fixture Config carrying it is one
// config.Load would accept.
//
//nolint:gosec // G101: a fixture password, in a test file, spelled so it cannot be mistaken for a real one.
const testDashboardPassword = "test-only-dashboard-password"

// testPasswordDoor is the door under test, on a clock that does not move. Every
// expiry below is measured against testTime, so no case depends on how long the
// suite took to reach it.
func testPasswordDoor(t *testing.T) *passwordDoor {
	t.Helper()
	return passwordDoorWith(t, testSecret(), []byte(testDashboardPassword))
}

func passwordDoorWith(t *testing.T, secret, password []byte) *passwordDoor {
	t.Helper()

	d, err := newPasswordDoor(secret, password)
	if err != nil {
		t.Fatalf("newPasswordDoor = _, %v; want a door", err)
	}
	d.clock = fixedClock{at: testTime}
	return d
}

// passwordConfig is a Config whose browser door is the password: the deployment
// this milestone exists for, with the three Access values unset.
func passwordConfig(listen string) *config.Config {
	cfg := noDoorConfig(listen)
	cfg.DashboardPassword = []byte(testDashboardPassword)
	return cfg
}

// cookied is a request carrying the values named, in the order named, under the
// one cookie name this daemon reads.
func cookied(values ...string) *http.Request {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	for _, v := range values {
		// G124 is about the attributes a *response* sets. A cookie on the way in
		// carries a name and a value and nothing else — a browser sends no
		// attributes back — so there is nothing here for the rule to be about.
		//
		//nolint:gosec // G124: a request-side cookie has no attributes to set.
		r.AddCookie(&http.Cookie{Name: cookieDashboardSession, Value: v})
	}
	return r
}

// live is the cookie the door would issue right now: signed by it, for this
// password, inside its lifetime.
func live(t *testing.T, d *passwordDoor) string {
	t.Helper()

	value, err := d.sign(testTime.Add(dashboardSessionLifetime).Unix())
	if err != nil {
		t.Fatalf("sign a session cookie: %v", err)
	}
	return value
}

// TestPasswordDoorAdmitsTheCookieItIssued is the positive case, and it pins the
// two facts everything behind the door depends on: the owner is the same
// constant the API door's caller carries, so a session created through one door
// is one the other can see (FR-037a), and the identity is non-empty, because the
// page token is bound to it and a token bound to the empty string is one every
// empty identity verifies.
//
// The request carries no assertion and no layer-2 signature and is admitted
// anyway, which is FR-012 from the third door: each door refuses only by the
// check that applies to it.
func TestPasswordDoorAdmitsTheCookieItIssued(t *testing.T) {
	t.Parallel()

	d := testPasswordDoor(t)

	operator, err := d.Verify(cookied(live(t, d)))
	if err != nil {
		t.Fatalf("Verify(a cookie this door issued) = _, %v; want an operator", err)
	}
	if operator.Owner != auth.CallerOperator {
		t.Errorf("the admitted operator owns sessions as %q; want %q", operator.Owner, auth.CallerOperator)
	}
	if operator.Email == "" {
		t.Error("the admitted operator has no identity; the page token would be bound to the empty string")
	}
	// Not an address, for the reason access.bypassEmail is not one: this door
	// checked a secret and not a person, and an email-shaped string in the
	// masthead reads as a person an identity provider vouched for.
	if strings.Contains(operator.Email, "@") {
		t.Errorf("the identity %q is address-shaped; this door verified nobody's address", operator.Email)
	}
	if strings.Contains(operator.Email, testDashboardPassword) {
		t.Errorf("the identity %q carries the password", operator.Email)
	}
}

// TestPasswordDoorRefusesEveryCookieItDidNotIssue is the table, and every row is
// a cookie that is genuine in every way but one.
//
// The reason each row must record is named, and the rows are read against each
// other: two rows meaning different faults must never record the same sentinel,
// because the record is the only place an operator can see which check refused.
// None of it reaches the caller — authenticateBrowser answers all of them with
// the one uniform 401.
func TestPasswordDoorRefusesEveryCookieItDidNotIssue(t *testing.T) {
	t.Parallel()

	// A door that is identical except for the key, and one identical except for
	// the password. Between them they are the two rotations that must end a
	// session: the shared secret, and the password itself.
	otherSecret := func(t *testing.T) *passwordDoor {
		t.Helper()
		return passwordDoorWith(t, []byte("test-only-other-secret-32-bytes!"), []byte(testDashboardPassword))
	}
	otherPassword := func(t *testing.T) *passwordDoor {
		t.Helper()
		return passwordDoorWith(t, testSecret(), []byte("test-only-dashboard-password-was-changed"))
	}
	// The MAC over a live expiry, so a row about the MAC is not also a row about
	// the expiry.
	macFor := func(t *testing.T, d *passwordDoor) (string, string) {
		t.Helper()

		value := live(t, d)
		i := strings.LastIndex(value, dashboardSessionSeparator)
		return value[:i], value[i+1:]
	}

	for _, c := range []struct {
		name   string
		cookie func(t *testing.T, d *passwordDoor) *http.Request
		want   error
		why    string
	}{
		{
			name:   "no cookie at all",
			cookie: func(*testing.T, *passwordDoor) *http.Request { return httptest.NewRequest(http.MethodGet, "/", nil) },
			want:   errDashboardSessionMissing,
			why:    "a browser that has never signed in — the ordinary first request",
		},
		{
			name:   "an empty cookie",
			cookie: func(*testing.T, *passwordDoor) *http.Request { return cookied("") },
			want:   errDashboardSessionMissing,
			why:    "a cleared cookie is the absence of one, not a value that failed to verify",
		},
		{
			name:   "no separator",
			cookie: func(*testing.T, *passwordDoor) *http.Request { return cookied("deadbeef") },
			want:   errDashboardSessionMalformed,
			why:    "not the shape this door writes at all",
		},
		{
			name:   "an expiry that is not a number",
			cookie: func(*testing.T, *passwordDoor) *http.Request { return cookied("never." + strings.Repeat("a", 64)) },
			want:   errDashboardSessionMalformed,
			why:    "the expiry has to be read before it can be compared",
		},
		{
			name: "an expiry spelled a second way",
			cookie: func(t *testing.T, d *passwordDoor) *http.Request {
				t.Helper()
				_, mac := macFor(t, d)
				return cookied("+" + strconv.FormatInt(testTime.Add(dashboardSessionLifetime).Unix(), 10) + "." + mac)
			},
			want: errDashboardSessionMalformed,
			why: "ParseInt accepts a leading plus and leading zeroes, and the MAC covers the rendered form; " +
				"without the canonical check one instant would have an unbounded family of cookies",
		},
		{
			name: "expired by a second",
			cookie: func(t *testing.T, d *passwordDoor) *http.Request {
				t.Helper()
				value, err := d.sign(testTime.Add(-time.Second).Unix())
				if err != nil {
					t.Fatalf("sign an expired cookie: %v", err)
				}
				return cookied(value)
			},
			want: errDashboardSessionExpired,
			why:  "the lifetime is the daemon's to enforce; Max-Age is only a request to the browser",
		},
		{
			name: "expiring exactly now",
			cookie: func(t *testing.T, d *passwordDoor) *http.Request {
				t.Helper()
				value, err := d.sign(testTime.Unix())
				if err != nil {
					t.Fatalf("sign a cookie expiring now: %v", err)
				}
				return cookied(value)
			},
			want: errDashboardSessionExpired,
			why:  "the boundary belongs on the refusing side; a lifetime plus one request is not a lifetime",
		},
		{
			name: "the MAC in upper case",
			cookie: func(t *testing.T, d *passwordDoor) *http.Request {
				t.Helper()
				expiry, mac := macFor(t, d)
				return cookied(expiry + "." + strings.ToUpper(mac))
			},
			want: errDashboardSessionMalformed,
			why:  "hex has two spellings per byte; accepting both would give every cookie an uppercase twin",
		},
		{
			name: "a MAC of the wrong length",
			cookie: func(t *testing.T, d *passwordDoor) *http.Request {
				t.Helper()
				expiry, mac := macFor(t, d)
				return cookied(expiry + "." + mac[:len(mac)-1])
			},
			want: errDashboardSessionMalformed,
			why:  "refused on its shape, before this daemon computes a MAC for it",
		},
		{
			name: "one hex digit moved",
			cookie: func(t *testing.T, d *passwordDoor) *http.Request {
				t.Helper()
				expiry, mac := macFor(t, d)
				flipped := "0"
				if mac[0] == '0' {
					flipped = "1"
				}
				return cookied(expiry + "." + flipped + mac[1:])
			},
			want: errDashboardSessionMismatch,
			why:  "the shape is right and the signature is not, which is the forgery this door is against",
		},
		{
			name: "signed with a different shared secret",
			cookie: func(t *testing.T, d *passwordDoor) *http.Request {
				t.Helper()
				return cookied(live(t, otherSecret(t)))
			},
			want: errDashboardSessionMismatch,
			why:  "rotating CRSW_SHARED_SECRET ends every outstanding sign-in, which is the point of using it",
		},
		{
			name: "signed before the password changed",
			cookie: func(t *testing.T, d *passwordDoor) *http.Request {
				t.Helper()
				return cookied(live(t, otherPassword(t)))
			},
			want: errDashboardSessionMismatch,
			why: "an operator who changes the password because they think it is known must not keep admitting " +
				"whoever holds a cookie minted under the old one",
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			d := testPasswordDoor(t)

			operator, err := d.Verify(c.cookie(t, d))
			if operator != nil {
				t.Fatalf("Verify admitted an operator: %s", c.why)
			}
			if !errors.Is(err, c.want) {
				t.Fatalf("Verify = _, %v; want %v: %s", err, c.want, c.why)
			}
			// No refusal may name what it refused over. Every sentinel here is
			// written from constants in password.go, and this is what keeps it that
			// way when one is reworded.
			if strings.Contains(err.Error(), testDashboardPassword) {
				t.Errorf("the refusal %q carries the password", err)
			}
		})
	}
}

// TestPasswordDoorReadsPastACookieThatDoesNotVerify is about an outage rather
// than an admission.
//
// A second cookie of this name cannot let anybody in — it would still need a MAC
// this daemon wrote — but one sitting ahead of the real one in the header would
// refuse the operator on every request, and a door that can be jammed from
// outside is a door with an outage in it. That is why Verify reads every cookie
// of the name rather than the first.
func TestPasswordDoorReadsPastACookieThatDoesNotVerify(t *testing.T) {
	t.Parallel()

	d := testPasswordDoor(t)

	if _, err := d.Verify(cookied("0.deadbeef", live(t, d))); err != nil {
		t.Fatalf("Verify(a shadowing cookie, then a valid one) = _, %v; want an operator", err)
	}
	// And the shadow itself still admits nobody, which is what makes reading past
	// it safe rather than lenient.
	if _, err := d.Verify(cookied("0.deadbeef")); err == nil {
		t.Fatal("Verify admitted a cookie with no valid MAC")
	}
}

// TestPasswordDoorAdmitsOnlyTheWholePassword is the login check.
//
// The compare is constant-time over SHA-256 of both sides, and neither property
// is observable from a test — what is observable is that nothing but the exact
// password is accepted, in particular not a prefix, an extension, or a different
// case. A compare that had been weakened to a prefix match or a length check
// fails here.
func TestPasswordDoorAdmitsOnlyTheWholePassword(t *testing.T) {
	t.Parallel()

	d := testPasswordDoor(t)

	if !d.admits([]byte(testDashboardPassword)) {
		t.Fatal("the configured password was refused")
	}
	for _, submitted := range []string{
		"",
		testDashboardPassword[:len(testDashboardPassword)-1],
		testDashboardPassword + "!",
		strings.ToUpper(testDashboardPassword),
		" " + testDashboardPassword,
		testDashboardPassword + " ",
		strings.Repeat("x", len(testDashboardPassword)),
	} {
		if d.admits([]byte(submitted)) {
			t.Errorf("admits(%q) = true; want false", submitted)
		}
	}
}

// TestTheSessionCookieCarriesTheAttributesThatMakeItSafe pins every one of the
// plan's four, plus the one that is conditional.
//
// Each is load-bearing on its own: HttpOnly keeps the credential out of reach of
// script, Lax is what lets an operator arrive by typing a URL while still being
// withheld from a cross-site POST, Path=/ is every page on this door, and Secure
// follows the transport because a Secure cookie on a plaintext LAN is one the
// browser never sends back.
func TestTheSessionCookieCarriesTheAttributesThatMakeItSafe(t *testing.T) {
	t.Parallel()

	for _, c := range []struct {
		name       string
		tls        bool
		wantSecure bool
	}{
		{name: "over plaintext", wantSecure: false},
		{name: "over TLS", tls: true, wantSecure: true},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			d := testPasswordDoor(t)
			r := httptest.NewRequest(http.MethodGet, "/", nil)
			if c.tls {
				r = httptest.NewRequest(http.MethodGet, "https://crswd.example.com/", nil)
			}

			w := httptest.NewRecorder()
			if err := d.issue(w, r); err != nil {
				t.Fatalf("issue = %v; want a cookie", err)
			}

			cookies := (&http.Response{Header: w.Header()}).Cookies()
			if len(cookies) != 1 {
				t.Fatalf("issue set %d cookies; want exactly 1", len(cookies))
			}
			got := cookies[0]

			// Independent statements and never a switch: each attribute is
			// load-bearing on its own, and a chain that stopped at the first
			// mismatch would let one breakage hide the next.
			if got.Name != cookieDashboardSession {
				t.Errorf("cookie name = %q; want %q", got.Name, cookieDashboardSession)
			}
			if !got.HttpOnly {
				t.Error("the session cookie is not HttpOnly; script can read the credential")
			}
			if got.SameSite != http.SameSiteLaxMode {
				t.Errorf("cookie SameSite = %v; want %v — Strict withholds it on the operator's first navigation",
					got.SameSite, http.SameSiteLaxMode)
			}
			if got.Path != "/" {
				t.Errorf("cookie Path = %q; want %q", got.Path, "/")
			}
			if got.Secure != c.wantSecure {
				t.Errorf("cookie Secure = %t; want %t", got.Secure, c.wantSecure)
			}
			if got.MaxAge != int(dashboardSessionLifetime/time.Second) {
				t.Errorf("cookie Max-Age = %d; want %d", got.MaxAge, int(dashboardSessionLifetime/time.Second))
			}

			// The whole header, not just the value: a password that reached an
			// attribute would be as disclosed as one in the value.
			if header := strings.Join(w.Header().Values("Set-Cookie"), "\n"); strings.Contains(header, testDashboardPassword) ||
				strings.Contains(header, hex.EncodeToString(digestOf(testDashboardPassword))) {
				t.Errorf("the Set-Cookie header carries password material:\n%s", header)
			}

			// And the cookie it just wrote is one this door admits, so the two halves
			// cannot drift into disagreeing about what a session looks like.
			if _, err := d.Verify(cookied(got.Value)); err != nil {
				t.Errorf("Verify(the cookie issue just wrote) = _, %v; want an operator", err)
			}
		})
	}
}

func digestOf(s string) []byte {
	sum := sha256.Sum256([]byte(s))
	return sum[:]
}

// TestNewPasswordDoorRefusesToBeBuiltWithoutBothHalves is docs/security.md §4 on
// this door: a missing value that would weaken auth is a refusal, never a
// default. Either half absent is a door that signs nothing or admits everyone.
func TestNewPasswordDoorRefusesToBeBuiltWithoutBothHalves(t *testing.T) {
	t.Parallel()

	for _, c := range []struct {
		name     string
		secret   []byte
		password []byte
		want     error
	}{
		{name: "no shared secret", password: []byte(testDashboardPassword), want: errPasswordDoorUnkeyed},
		{name: "no password", secret: testSecret(), want: errPasswordDoorNoPassword},
		{name: "neither", want: errPasswordDoorUnkeyed},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			d, err := newPasswordDoor(c.secret, c.password)
			if d != nil {
				t.Fatal("newPasswordDoor built a door with half its inputs")
			}
			if !errors.Is(err, c.want) {
				t.Fatalf("newPasswordDoor = _, %v; want %v", err, c.want)
			}
		})
	}
}

// TestVerifiedLayer1PicksExactlyOneDoor is the selection itself, and it is the
// reason there is no branch anywhere else: this is the only place in the
// shipping build that decides which layer 1 a browser meets.
//
// The last row is the one no config.Load can produce — config.validateDoors
// refuses a file naming both — and it is here because a hand-built Config can,
// and because "which one wins" must be an answer this test can read rather than
// a surprise. Access wins, which is the door every existing deployment already
// has.
func TestVerifiedLayer1PicksExactlyOneDoor(t *testing.T) {
	t.Parallel()

	both := passwordConfig(loopbackListen)
	both.AccessTeamDomain = testConfig(loopbackListen).AccessTeamDomain
	both.AccessAUD = testConfig(loopbackListen).AccessAUD
	both.AccessAllowedEmails = testConfig(loopbackListen).AccessAllowedEmails

	for _, c := range []struct {
		name string
		cfg  *config.Config
		want string
	}{
		{name: "the three Access values", cfg: testConfig(loopbackListen), want: "httpapi.assertionDoor"},
		{name: "a dashboard password", cfg: passwordConfig(loopbackListen), want: "*httpapi.passwordDoor"},
		{name: "neither", cfg: noDoorConfig(loopbackListen), want: "httpapi.closedDoor"},
		{name: "both, which no file may name", cfg: both, want: "httpapi.assertionDoor"},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			door, err := verifiedLayer1(c.cfg)
			if err != nil {
				t.Fatalf("verifiedLayer1 = _, %v; want a door", err)
			}
			if got := typeName(door); got != c.want {
				t.Fatalf("verifiedLayer1 built %s; want %s", got, c.want)
			}
		})
	}
}

// typeName is what the selection test reads. A type assertion per row would say
// "it is at least this", and what the rows need to say is "it is exactly this":
// a door that had grown a second implementation wrapping the first would pass
// the assertion and fail here.
func typeName(v any) string { return fmt.Sprintf("%T", v) }

// TestVerifiedLayer1RefusesAPasswordDaemonWithNoSharedSecret keeps the door's
// own refusal reachable from the place the daemon is assembled: a Config that
// names a password and no secret produces no server, rather than a server whose
// dashboard cannot be signed into.
func TestVerifiedLayer1RefusesAPasswordDaemonWithNoSharedSecret(t *testing.T) {
	t.Parallel()

	cfg := passwordConfig(loopbackListen)
	cfg.SharedSecret = nil

	door, err := verifiedLayer1(cfg)
	if door != nil {
		t.Fatal("verifiedLayer1 built a door with no key to sign cookies with")
	}
	if !errors.Is(err, errPasswordDoorUnkeyed) {
		t.Fatalf("verifiedLayer1 = _, %v; want %v", err, errPasswordDoorUnkeyed)
	}
}

// TestTheBrowserDoorAdmitsAPasswordCookieThroughTheSameMiddleware is the claim
// the whole task rests on: the password door reaches a browser through
// authenticateBrowser unchanged, with no branch added for it.
//
// The request carries no assertion header at all — the credential the *other*
// door reads — and is served, which is only possible because the middleware asks
// its door for a verdict rather than asking which door it is.
func TestTheBrowserDoorAdmitsAPasswordCookieThroughTheSameMiddleware(t *testing.T) {
	t.Parallel()

	pd := testPasswordDoor(t)
	d := newDoor(t, pd)

	w := httptest.NewRecorder()
	d.handler.ServeHTTP(w, cookied(live(t, pd)))

	if w.Code != http.StatusOK {
		t.Fatalf("a valid session cookie was answered %d (%s); want %d", w.Code, w.Body.String(), http.StatusOK)
	}
	if d.served != 1 {
		t.Fatalf("the handler behind the door ran %d times; want exactly 1", d.served)
	}
	if got, want := w.Header().Get("X-Test-Operator"), passwordOperator+" "+string(auth.CallerOperator); got != want {
		t.Errorf("the handler saw operator %q; want %q", got, want)
	}

	rec := d.only(t)
	if got, want := rec["decision"], string(audit.Allow); got != want {
		t.Errorf("decision = %v; want %v", got, want)
	}
	if got, want := rec["caller"], string(auth.CallerOperator); got != want {
		t.Errorf("caller = %v; want %v", got, want)
	}
}

// TestTheBrowserDoorRefusesAMissingCookieAsItRefusesEverythingElse is the other
// half: a refusal on this door is byte-identical to the assertion door's, and
// the reason lives only on the trail.
//
// Byte-identical matters more here than it looks. If a password daemon's refusal
// differed from an Access daemon's, the refusal itself would tell a stranger
// which door this host is behind — and therefore which credential is worth
// attacking.
func TestTheBrowserDoorRefusesAMissingCookieAsItRefusesEverythingElse(t *testing.T) {
	t.Parallel()

	d := newDoor(t, testPasswordDoor(t))

	w := httptest.NewRecorder()
	d.handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", nil))

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("a request with no session cookie was answered %d; want %d", w.Code, http.StatusUnauthorized)
	}
	if got := w.Body.String(); got != string(bodyBrowserRefused) {
		t.Errorf("the refusal body is this door's own:\n%s", got)
	}
	if d.served != 0 {
		t.Fatalf("the handler behind the door ran %d times; want 0", d.served)
	}

	rec := d.only(t)
	if got, want := rec["action"], string(audit.ActionAccessReject); got != want {
		t.Errorf("action = %v; want %v", got, want)
	}
	if got, want := rec["reason"], errDashboardSessionMissing.Error(); got != want {
		t.Errorf("reason = %v; want %v", got, want)
	}
	if strings.Contains(d.sink.String(), testDashboardPassword) {
		t.Errorf("the trail carries the password:\n%s", d.sink.String())
	}
}

// TestAPasswordDaemonMayBindWhereItsOperatorCanReachIt closes the loop T002 left
// open, and it is the whole observable point of this milestone.
//
// T002 relaxed the bind for a daemon whose layer 1 admits somebody, and read
// that as *both* what the file configured and what was actually wired. Until
// this task the second half said no for a password daemon — verifiedLayer1
// returned closedDoor — so a correctly configured LAN daemon still refused to
// start. Both halves agree now, and this asserts it through the real selection
// rather than through a stand-in door.
func TestAPasswordDaemonMayBindWhereItsOperatorCanReachIt(t *testing.T) {
	t.Parallel()

	for _, c := range []struct {
		name    string
		cfg     *config.Config
		wantErr bool
		why     string
	}{
		{
			name: "a password door, on the network",
			cfg:  passwordConfig("0.0.0.0:8765"),
			why:  "the deployment this milestone exists for: no Cloudflare, and a listener the LAN can reach",
		},
		{
			name: "a password door, on loopback",
			cfg:  passwordConfig(loopbackListen),
			why:  "the same daemon behind a proxy, which the relaxation must not have broken",
		},
		{
			name:    "no door at all, on the network",
			cfg:     noDoorConfig("0.0.0.0:8765"),
			wantErr: true,
			why:     "still refused: the bound was relaxed for a daemon somebody can get into, not removed",
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			door, err := verifiedLayer1(c.cfg)
			if err != nil {
				t.Fatalf("verifiedLayer1 = _, %v; want a door", err)
			}

			s, err := newServer(c.cfg, net.Listen, testAuth(t), door, testTrail(t), newSessionFixture(t).mgr,
				testLimiter(t, c.cfg.CreateRatePerMin, fixedClock{at: testTime}))
			switch {
			case c.wantErr && err == nil:
				t.Fatalf("newServer(%q) built a server: %s", c.cfg.Listen, c.why)
			case c.wantErr && !errors.Is(err, ErrNotLoopback):
				t.Fatalf("newServer(%q) = _, %v; want an error matching ErrNotLoopback: %s", c.cfg.Listen, err, c.why)
			case !c.wantErr && err != nil:
				t.Fatalf("newServer(%q) = _, %v; want a server: %s", c.cfg.Listen, err, c.why)
			case !c.wantErr && s == nil:
				t.Fatalf("newServer(%q) returned no server and no error: %s", c.cfg.Listen, c.why)
			}
		})
	}
}
