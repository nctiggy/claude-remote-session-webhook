package httpapi

// pagetoken.go is the second half of the cross-site defence: a value carried by
// a page this daemon rendered, proving that a mutating request came from that
// page and was made by the identity the page was rendered for (FR-002b, FR-007).
// The first half is crossSite in stream.go, which is reused rather than
// reproduced (research R1). Two independent checks, because a request that
// passes authentication on this host is unsandboxed code execution and a single
// header a future proxy could strip must not be the whole defence.
//
// **Nothing here is stored.** No map, no sweep, no "already minted" set: the
// token is verified by recomputing it, exactly as the layer-2 request signature
// is (research R2). Milestone 2 refused per-browser state on purpose and wrote
// down why — caching one "would be the daemon's first cross-request browser
// state, and with it the expiry, invalidation and fixation questions this design
// exists not to have" (verifyBrowser). A minted-and-remembered token is that
// state exactly, plus a map an unauthenticated-until-layer-1 caller can grow. A
// stateless token has none of them: nothing to sweep, nothing to grow, nothing
// to fixate.
//
// The gate that calls this lives in browser.go and runs it *after* layer 1, in
// the same middleware. That order is what makes FR-008 structural rather than
// bookkeeping: an identity whose Access session has ended is refused before its
// token is ever examined, so no record can drift out of step with the Access
// session — because there is no record.

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"
)

// pageKeyBytes is the size of the key every token is MACed with — the entropy
// behind session.NewToken, for a value guarding the same thing.
const pageKeyBytes = 32

// pageTokenLifetime is how long a rendered page may act (research R3).
//
// Long enough that a dashboard left open through a working day still works,
// short enough to bound what one captured token is worth. Nothing else in the
// design depends on the number: the failure at expiry is visible and recoverable
// by reloading, which is FR-031's requirement that a failed action say so rather
// than revert in silence.
const pageTokenLifetime = 12 * time.Hour

// The token's punctuation, spelled once so that mint and verify cannot drift
// into disagreeing about what a token looks like.
//
// pageTokenSeparator divides the two parts on the wire. pageTokenFieldSeparator
// divides identity from expiry *inside* the MAC input, and is deliberately a
// byte the wire form does not use: the two are different questions, and one
// constant serving both would make a token that happened to contain the field
// separator a token with two readings.
const (
	pageTokenSeparator      = "."
	pageTokenFieldSeparator = "\n"
)

// pageTokenMACLen is the encoded MAC's length: two hex characters per byte.
const pageTokenMACLen = sha256.Size * 2

// The refusals, authored here and for the trail alone.
//
// They are distinct sentinels for the reason internal/access's are: the operator
// reading their journal needs to know which check refused, and this is the only
// place that is kept. **No caller ever learns which** — browser.go answers all of
// them with one uniform 403, byte-identical including Content-Length, because
// the difference between "malformed" and "not yours" is which forgery to try
// next. Every one of these strings is written here from constants, so a reason
// on the record can never carry a byte a caller chose (FR-035, FR-042).
var (
	errPageTokenNoIdentity = errors.New("a page token was asked for without a verified identity to bind it to")

	errPageTokenMissing = errors.New("the request carried no page token")

	errPageTokenMalformed = errors.New("the page token is not an expiry and a MAC")

	errPageTokenExpired = errors.New("the page token's expiry has passed")

	errPageTokenMismatch = errors.New("the page token does not verify for this identity")

	errPageTokenUnsignable = errors.New("the page token's MAC could not be computed")
)

// pageKey is the secret behind every page token: 32 bytes from crypto/rand at
// startup, never persisted, never served by any route in any form (FR-006), and
// **unrelated to CRSW_SHARED_SECRET**.
//
// Unrelated is the load-bearing word, and it is a decision rather than an
// accident (research R2). Deriving this from the signing secret would put a
// value the daemon hands to a browser into a relationship with the secret that
// authorises the entire API, in exchange for the one property this design does
// not want: surviving a restart. The key is regenerated on every start, so a
// restart invalidates every outstanding token — anticipated, not tolerated.
// Session records do not survive a restart either, and an open page across one
// gets a single clear failure that a reload fixes.
//
// An array rather than a slice, so that a copy of a pageKey is a copy of the key
// and not a second reference to the one the server holds.
type pageKey [pageKeyBytes]byte

// newPageKey reads a fresh key from the process's entropy source. It is called
// once per server, at construction.
func newPageKey() (pageKey, error) {
	return newPageKeyFrom(rand.Reader)
}

// newPageKeyFrom is the seam newPageKey wraps, so that the exhausted-entropy
// path is reachable from a test. It stays unexported for the reason
// session.newTokenFrom does: the only randomness that reaches production is
// rand.Reader, chosen in one place.
func newPageKeyFrom(r io.Reader) (pageKey, error) {
	var k pageKey
	// ReadFull, not Read: a short read must fail rather than yield a key with
	// fewer random bytes behind it than its length claims. A partially random
	// key would mint tokens that verify perfectly and are worth guessing.
	if _, err := io.ReadFull(r, k[:]); err != nil {
		// The error is wrapped without the bytes that were read. There is no
		// half-key in this message and no length either — see
		// session.newIDFrom, which refuses the same disclosure.
		return pageKey{}, fmt.Errorf("read %d random bytes for the page token key: %w", pageKeyBytes, err)
	}
	return k, nil
}

// mint issues the token for one page render, bound to the identity that render
// was authorised for (FR-007).
//
// The instant arrives as a parameter and never from time.Now, so a page's expiry
// is measured on the server's own clock — the same one verify measures it
// against. There is no arrangement where a token is live by one reading of time
// and expired by another.
//
// What comes back is returned to the caller and recorded nowhere. It goes into a
// hidden form field and into no URL, no cookie, no data- attribute, and no audit
// record (T004): a token in a link is a token in a referrer header, a browser
// history, and a proxy log.
func (k pageKey) mint(identity string, now time.Time) (string, error) {
	// Fail closed on the path that should not happen. Layer 1 refuses an
	// assertion that names no person, so every caller behind the browser door has
	// a non-empty identity — and a token bound to the empty string would be a
	// token bound to nobody, which every other empty identity would then verify.
	// The alternative to this guard is trusting that no future caller ever mints
	// one before it knows who is looking.
	if identity == "" {
		return "", errPageTokenNoIdentity
	}

	expiry := now.Add(pageTokenLifetime).Unix()
	mac, err := k.mac(identity, expiry)
	if err != nil {
		return "", err
	}
	return strconv.FormatInt(expiry, 10) + pageTokenSeparator + mac, nil
}

// verify answers whether token was minted by this daemon, for this identity, and
// is still inside its lifetime. It runs data-model.md's six steps in that order.
//
// identity is the request's **own verified identity** — what layer 1 put in the
// context — and never a value read out of the body, the token, or a header the
// caller chose. That is the whole of FR-007: a token minted for one operator
// recomputes to a different MAC when presented as another, so it fails, and no
// amount of editing the request can make the daemon check it against the
// identity its holder would prefer.
//
// A nil error is the only acceptance. Every refusal is one of this file's
// sentinels, for the record alone — the caller sees one uniform 403 whichever it
// was.
func (k pageKey) verify(token, identity string, now time.Time) error {
	if identity == "" {
		return errPageTokenNoIdentity
	}

	// Step 1. Absent is its own sentinel and not "malformed": on the record, a
	// form that carried no field at all is a page that never rendered one, which
	// is a different fault from a value that was tampered with.
	if token == "" {
		return errPageTokenMissing
	}

	// Step 2, splitting on the **last** separator. Either end refuses every
	// malformed token — a second separator leaves the expiry unparseable this way
	// round and the MAC unhexable the other — so the choice is about there being
	// one rule rather than about which rule is safer, and data-model.md names
	// this one.
	i := strings.LastIndex(token, pageTokenSeparator)
	if i < 0 {
		return errPageTokenMalformed
	}
	rawExpiry, mac := token[:i], token[i+len(pageTokenSeparator):]

	// Step 3.
	expiry, err := strconv.ParseInt(rawExpiry, 10, 64)
	if err != nil {
		return errPageTokenMalformed
	}
	// The canonical spelling and no other, for the reason auth.sign re-renders
	// the request timestamp instead of copying it out of the header: ParseInt
	// accepts "+1785749600" and "0001785749600", and the MAC covers the rendered
	// form, so without this every token would have an unbounded family of
	// spellings that all verify. One instant, one token.
	if rawExpiry != strconv.FormatInt(expiry, 10) {
		return errPageTokenMalformed
	}

	// Step 4. Strictly greater than now: at the expiry second the token is
	// already gone. The boundary belongs on the refusing side of the comparison
	// for the reason session.CheckToken's does — a lifetime that is "12 hours
	// plus however long the last request takes" is not a lifetime anyone bounded.
	if !now.Before(time.Unix(expiry, 0)) {
		return errPageTokenExpired
	}

	// Step 5, before the recomputation and not after it: a value that is not the
	// shape of a MAC is refused without this daemon computing one for it.
	if !isHexMAC(mac) {
		return errPageTokenMalformed
	}

	// Step 6.
	want, err := k.mac(identity, expiry)
	if err != nil {
		return err
	}
	// hmac.Equal, never ==. A byte-by-byte compare that stops at the first
	// difference leaks the expected MAC under timing, and this one is compared
	// against a value a caller supplied — which is the case the constant-time
	// compare exists for (FR-009, docs/security.md §1).
	if !hmac.Equal([]byte(want), []byte(mac)) {
		return errPageTokenMismatch
	}
	return nil
}

// mac is the one place a token's MAC exists, so that the value minted into a
// page and the value recomputed on every action are produced by the same
// expression rather than by two that agree today.
//
// The payload is identity "\n" expiry. Both are inside it because the token has
// to name what it authorises: without the identity it is a token any operator's
// page could hand to any other (FR-007), and without the expiry its holder could
// extend it by editing one number that nothing covers.
//
// The expiry is rendered from the parsed integer rather than copied out of the
// token — see verify's canonical-spelling check for why that is not the same
// thing.
func (k pageKey) mac(identity string, expiry int64) (string, error) {
	mac := hmac.New(sha256.New, k[:])
	// hash.Hash documents that Write never returns an error. errcheck is
	// configured to take nothing on trust, and the honest response to a MAC that
	// could not be computed is to refuse — so the impossible case is returned
	// rather than discarded, exactly as auth.sign returns it.
	if _, err := io.WriteString(mac, identity+pageTokenFieldSeparator+strconv.FormatInt(expiry, 10)); err != nil {
		return "", errPageTokenUnsignable
	}
	return hex.EncodeToString(mac.Sum(nil)), nil
}

// isHexMAC reports whether s is exactly the shape mac produces: 64 characters,
// lowercase hex, and nothing else.
//
// Lowercase is required rather than accepted in either case, for the reason
// session.hashToken hashes the encoded token rather than the bytes behind it:
// hex has two spellings for every byte, so accepting both would give every token
// an uppercase twin — two strings that authorise the same action, only one of
// which was ever minted. It also means the constant-time compare above runs over
// two strings of equal length, which is the shape it is written for.
//
// The password door's session cookie is the same shape and is checked by this
// same function (password.go). It is named for what it tests rather than for the
// first caller that needed it: two copies of this predicate would be two rules
// about what a MAC looks like, free to disagree about the uppercase twin.
func isHexMAC(s string) bool {
	if len(s) != pageTokenMACLen {
		return false
	}
	for _, c := range s {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}
