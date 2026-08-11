package httpapi

// password.go is layer 1 for a daemon with no Cloudflare in front of it (M12):
// an operator on their own network, reaching the dashboard from the machine
// they are sitting at, proving who they are by knowing a secret in their own
// configuration file.
//
// It is a *third* implementation of the same layer1 interface closedDoor and
// assertionDoor implement, returned from the same verifiedLayer1 that returns
// those two, and that is the whole of how it reaches the browser door. There is
// no branch in authenticateBrowser and there must never be one — closedDoor's
// comment says why: "a nil validator and a special case in the middleware would
// be the second path, and the second path is the one nobody reads."
//
// What it authenticates is narrower than what Access authenticates, and the
// difference is stated rather than smoothed over. Access verifies a *person*
// against an identity provider at the edge, before this host is reachable at
// all. This verifies that whoever is asking knows one secret — so there is one
// operator behind it by construction, no allowlist to check, and no edge to fail
// closed for. Everything behind the door is unchanged: the same owner, the same
// ownership checks, the same action gate on anything that changes the host.
//
// **On a network without TLS the password crosses it in clear.** That is a real
// weakness of this mode rather than an oversight, and the cookie below is what
// keeps it to one crossing per session instead of one per request. An operator
// choosing this door is told so in the README (T008); nothing in this file can
// fix it, and pretending otherwise would be worse than saying it.

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/nctiggy/claude-remote-session-webhook/internal/access"
	"github.com/nctiggy/claude-remote-session-webhook/internal/auth"
)

// cookieDashboardSession is the one cookie this daemon issues, and the only one
// it reads. Cloudflare's own CF_Authorization is untouched and unread here for
// the reason headerAccessAssertion documents: that cookie is a browser's
// credential *to the edge*, and this one is this daemon's own.
//
// It is deliberately **not** spelled with the `__Host-` prefix, which would
// otherwise be the obvious hardening. Browsers refuse a `__Host-` cookie that is
// not Secure, and Secure is exactly what this door cannot promise: the
// deployment it exists for is a LAN with no TLS terminated anywhere. A prefix
// that made the plaintext case silently stop working is worse than no prefix.
const cookieDashboardSession = "crsw_dashboard"

// dashboardSessionLifetime is how long one sign-in lasts.
//
// The plan fixes every other property of this cookie and not this number, so it
// is a judgement rather than a requirement, and it is written where the next
// reader will find it. Twelve hours is pageTokenLifetime, chosen the same way:
// long enough that a dashboard open through a working day is not a login form
// twice, short enough to bound what one captured cookie is worth. Nothing else
// depends on it — expiry fails visibly, and signing in again fixes it.
//
// It is measured on the server's clock and carried *inside* the signed value.
// The cookie's own Max-Age is a request to the browser and nothing more: a
// client that ignores it presents an expired value and is refused here.
const dashboardSessionLifetime = 12 * time.Hour

// The signed cookie's punctuation and its domain label.
//
// dashboardSessionSeparator divides the two parts on the wire.
// dashboardSessionFieldSeparator divides the fields *inside* the MAC input, and
// is a byte the wire form does not use for the reason pagetoken.go keeps the two
// apart: one constant serving both makes a value that happens to contain it a
// value with two readings.
//
// dashboardSessionLabel is the distinct label the plan requires. The key is
// CRSW_SHARED_SECRET — no new key, so rotating the secret invalidates every
// outstanding sign-in, which is correct — and the label is what keeps this MAC's
// domain separate from layer 2's signature over a request line. It carries a
// version so a future change to what is signed can be a new label rather than a
// silent reinterpretation of the old bytes.
const (
	dashboardSessionSeparator      = "."
	dashboardSessionFieldSeparator = "\n"
	dashboardSessionLabel          = "crswd dashboard session v1"
)

// passwordOperator is the identity this door hands on, in place of the address
// the edge would have verified.
//
// It is deliberately not an address, for the reason access.bypassEmail is not
// one: the masthead exists so it is never ambiguous whose credentials are
// driving unsandboxed sessions on this host, and an email-shaped string reads at
// a glance as a person an identity provider vouched for. What was proven here is
// knowledge of a secret, and that is what it says.
//
// Non-empty is load-bearing rather than cosmetic. The page token is bound to
// this string (pagetoken.go), and a token bound to the empty string is a token
// every empty identity verifies.
//
// It is not configurable, for the reason access.bypassEmail is not: an identity
// the operator could set would be a second place identity comes from, and on
// this door identity comes from exactly one thing.
const passwordOperator = "operator (dashboard password)"

// The refusals, authored here and for the trail alone.
//
// Distinct sentinels for the reason internal/access's and pagetoken.go's are:
// the operator reading their journal needs to know which check refused, and this
// is the only place it is kept. **No caller ever learns which** —
// authenticateBrowser answers all of them with the one uniform 401, because the
// difference between "no cookie" and "a cookie that does not verify" is which
// forgery to try next. Every string here is written from constants, so a reason
// on the record can never carry a byte a caller chose (FR-035, FR-042), and in
// particular never a byte of the password.
var (
	errPasswordDoorUnkeyed = errors.New("the password door was built without a signing key")

	errPasswordDoorNoPassword = errors.New("the password door was built without a password")

	errDashboardSessionMissing = errors.New("the request carried no dashboard session cookie")

	errDashboardSessionMalformed = errors.New("the dashboard session cookie is not an expiry and a MAC")

	errDashboardSessionExpired = errors.New("the dashboard session cookie's expiry has passed")

	errDashboardSessionMismatch = errors.New("the dashboard session cookie does not verify")

	errDashboardSessionUnsignable = errors.New("the dashboard session cookie's MAC could not be computed")
)

// passwordDoor is layer 1 when a dashboard password is the configured door.
//
// It holds no session state and keeps no record of who has signed in. A cookie
// is verified by recomputing it, exactly as the page token and the layer-2
// signature are — so there is still nothing to fixate, sweep, expire, or leave
// behind, which is the property verifyBrowser's comment defends and this door
// had to keep rather than be excused from.
type passwordDoor struct {
	// key is CRSW_SHARED_SECRET. A copy, so that a caller who kept the slice it
	// passed cannot rewrite the key this door signs with.
	key []byte

	// digest is SHA-256 of the configured password, and the password itself is
	// not kept at all. Comparing digests is what makes the compare
	// constant-*length* as well as constant-time: two hashes are always 32 bytes,
	// so the comparison cannot leak how long the real password is, which a
	// compare over the raw values would.
	digest [sha256.Size]byte

	// clock is the server's, and a field for the reason Server.clock is one: a
	// test seam rather than a choice a caller has. A cookie's expiry must be
	// measured by the same reading of time that minted it.
	clock clock
}

// newPasswordDoor builds the door for a validated Config's two values.
//
// Both are refused when empty rather than defaulted, because either one missing
// is a door that admits everybody or a door with no key behind its cookies —
// and docs/security.md §4 ranks a daemon that starts with weak auth below one
// that does not start. config.Validate already refuses both, so reaching either
// of these is a wiring defect; a wiring defect must not produce a door.
func newPasswordDoor(secret, password []byte) (*passwordDoor, error) {
	switch {
	case len(secret) == 0:
		return nil, errPasswordDoorUnkeyed
	case len(password) == 0:
		return nil, errPasswordDoorNoPassword
	}
	return &passwordDoor{
		key:    append([]byte(nil), secret...),
		digest: sha256.Sum256(password),
		clock:  systemClock{},
	}, nil
}

// Verify is layer 1's question on this door: does this request carry a session
// cookie this daemon signed, for a password that is still the configured one,
// inside its lifetime?
//
// The credential is read here rather than by the middleware, which is what lets
// one middleware serve a door whose credential is a header and a door whose
// credential is a cookie without asking which it is holding (see layer1).
//
// Every refusal returns a nil operator and a sentinel for the trail. The caller
// answers all of them with the one uniform 401.
func (d *passwordDoor) Verify(r *http.Request) (*access.VerifiedOperator, error) {
	// CookiesNamed rather than Cookie, which returns the first and stops. A
	// second cookie of this name cannot admit anybody — it would still need a MAC
	// this daemon wrote — but a stale or injected one sitting ahead of the real
	// one in the header would refuse the operator, and a door that can be jammed
	// from outside is a door with an outage in it. None of them is admitted
	// without verifying, so trying each costs a refusal nothing.
	refusal := error(errDashboardSessionMissing)
	for _, c := range r.CookiesNamed(cookieDashboardSession) {
		if err := d.verifyValue(c.Value, d.clock.Now()); err != nil {
			refusal = err
			continue
		}
		// A fresh value per call, never a shared one: a pointer handed to every
		// handler is a pointer any handler can rewrite for all the others.
		return &access.VerifiedOperator{Email: passwordOperator, Owner: auth.CallerOperator}, nil
	}
	return nil, refusal
}

// admits is the login check: does this submitted password match the configured
// one? It is what T004's POST route asks, and the only place the two are ever
// compared.
//
// The comparison is constant-time, over SHA-256 of both sides. Both properties
// are needed and neither implies the other: a byte-by-byte compare that stops at
// the first difference leaks the password one character at a time under timing,
// and a constant-time compare over the *raw* values still leaks its length,
// because subtle.ConstantTimeCompare answers 0 immediately for two slices of
// different lengths. Hashing first makes every comparison a fixed 32 bytes.
//
// It returns a bool and not an error on purpose: there is exactly one reason to
// refuse here and nothing for a caller to branch on. Neither side of the compare
// is ever logged, recorded, rendered, or put in an error.
func (d *passwordDoor) admits(submitted []byte) bool {
	got := sha256.Sum256(submitted)
	return subtle.ConstantTimeCompare(got[:], d.digest[:]) == 1
}

// issue writes the session cookie for a login that succeeded. T004's POST route
// is its caller; nothing else in this daemon may mint one.
//
// The attributes are the plan's, and each is load-bearing:
//
//   - HttpOnly, so the value is out of reach of script. This dashboard runs one
//     script of its own and no third-party code, and that is exactly the
//     assumption an XSS would break — the credential must not be the thing it
//     reaches first.
//   - SameSite=Lax rather than Strict, because the operator arrives by typing a
//     URL and Strict withholds the cookie on that first top-level navigation,
//     which reads as "the password did not work". Lax still withholds it from the
//     cross-site POSTs the action gate exists for, and the gate does not depend
//     on it either way.
//   - Path=/, because every page on this door needs it and a narrower path is a
//     dashboard that forgets who you are somewhere.
//   - Secure only when the request arrived over TLS, read from r.TLS and never
//     from X-Forwarded-Proto. A forwarded header is caller-authored text
//     (docs/security.md §1), and this daemon has no configured trusted proxy to
//     believe one from; setting Secure on a plaintext LAN would mean the browser
//     never sends the cookie back and the operator can never stay signed in.
//
// Max-Age matches the expiry inside the signed value so a browser drops the
// cookie at the same moment this door stops accepting it. The value inside is
// the authority: a client that ignores Max-Age presents an expired cookie and is
// refused.
func (d *passwordDoor) issue(w http.ResponseWriter, r *http.Request) error {
	value, err := d.sign(d.clock.Now().Add(dashboardSessionLifetime).Unix())
	if err != nil {
		return err
	}
	// G124 reads Secure as unset because it is conditional, and it is conditional
	// on purpose: see the comment above. A Secure cookie on the plaintext LAN this
	// door exists for is one the browser never sends back, so taking the linter's
	// advice would leave the operator unable to stay signed in. HttpOnly and
	// SameSite, which the same rule is also about, are set unconditionally.
	//
	//nolint:gosec // G124: Secure follows the transport deliberately; see issue.
	http.SetCookie(w, &http.Cookie{
		Name:     cookieDashboardSession,
		Value:    value,
		Path:     "/",
		MaxAge:   int(dashboardSessionLifetime / time.Second),
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   r.TLS != nil,
	})
	return nil
}

// sign renders the wire form: the expiry, then the MAC over it.
//
// The expiry is in the value and covered by the MAC because the cookie has to
// name what it authorises. Without it, its holder could keep it forever; with it
// unsigned, they could extend it by editing one number.
func (d *passwordDoor) sign(expiry int64) (string, error) {
	mac, err := d.mac(expiry)
	if err != nil {
		return "", err
	}
	return strconv.FormatInt(expiry, 10) + dashboardSessionSeparator + mac, nil
}

// verifyValue runs the checks in the order pagetoken.verify runs its own, and
// for the same reasons: shape before arithmetic, arithmetic before cryptography,
// so a value that was never a cookie this daemon wrote is refused without one
// MAC being computed for it.
func (d *passwordDoor) verifyValue(value string, now time.Time) error {
	if value == "" {
		return errDashboardSessionMissing
	}

	// The **last** separator. Either end refuses every malformed value — a second
	// separator leaves the expiry unparseable this way round and the MAC unhexable
	// the other — so what matters is that there is one rule, and this is
	// pagetoken.go's.
	i := strings.LastIndex(value, dashboardSessionSeparator)
	if i < 0 {
		return errDashboardSessionMalformed
	}
	rawExpiry, mac := value[:i], value[i+len(dashboardSessionSeparator):]

	expiry, err := strconv.ParseInt(rawExpiry, 10, 64)
	if err != nil {
		return errDashboardSessionMalformed
	}
	// The canonical spelling and no other. ParseInt accepts "+1785749600" and
	// "0001785749600", and the MAC covers the rendered form, so without this every
	// cookie would have an unbounded family of spellings that all verify.
	if rawExpiry != strconv.FormatInt(expiry, 10) {
		return errDashboardSessionMalformed
	}

	// Strictly greater than now: at the expiry second the cookie is already gone.
	// The boundary belongs on the refusing side for the reason pagetoken.verify's
	// does — a lifetime that is "twelve hours plus however long the last request
	// takes" is not a lifetime anyone bounded.
	if !now.Before(time.Unix(expiry, 0)) {
		return errDashboardSessionExpired
	}

	if !isHexMAC(mac) {
		return errDashboardSessionMalformed
	}

	want, err := d.mac(expiry)
	if err != nil {
		return err
	}
	// hmac.Equal, never ==, on a value a caller supplied.
	if !hmac.Equal([]byte(want), []byte(mac)) {
		return errDashboardSessionMismatch
	}
	return nil
}

// mac is the one place a cookie's MAC exists, so that the value written into a
// browser and the value recomputed on every request are produced by the same
// expression rather than by two that agree today.
//
// The payload is label "\n" password digest "\n" expiry, and the third field is
// the one the plan does not ask for. It is there because a changed password
// should end the sessions the old one opened: without it, an operator who
// changes the password because they think it is known keeps admitting whoever
// holds a cookie minted under it, for up to a lifetime, with nothing they can do
// about it short of rotating the shared secret. It costs nothing and it makes
// forging a cookie need the password *and* the secret rather than the secret
// alone. The digest is hex-encoded rather than written raw, so that no byte of
// it can be read as the field separator.
//
// The expiry is rendered from the parsed integer rather than copied out of the
// cookie — see verifyValue's canonical-spelling check for why those are not the
// same thing.
func (d *passwordDoor) mac(expiry int64) (string, error) {
	mac := hmac.New(sha256.New, d.key)
	payload := strings.Join([]string{
		dashboardSessionLabel,
		hex.EncodeToString(d.digest[:]),
		strconv.FormatInt(expiry, 10),
	}, dashboardSessionFieldSeparator)
	// hash.Hash documents that Write never returns an error. errcheck is
	// configured to take nothing on trust, and the honest response to a MAC that
	// could not be computed is to refuse — so the impossible case is returned
	// rather than discarded, exactly as pageKey.mac returns it.
	if _, err := io.WriteString(mac, payload); err != nil {
		return "", errDashboardSessionUnsignable
	}
	return hex.EncodeToString(mac.Sum(nil)), nil
}
