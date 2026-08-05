// Internal test, matching internal/session/token_test.go: the key and the two
// functions that use it are unexported by design — the key never leaves this
// package — and a test that could only reach them through an exported wrapper
// would be asking for the wrapper to exist.
//
// Every MAC this file compares against is computed from first principles rather
// than by calling mac: a test that mints with the code under test proves only
// that the code agrees with itself.
package httpapi

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"strconv"
	"strings"
	"testing"
	"time"
)

// The two identities every case here is about. They differ in the local part
// alone, so nothing in these tests can pass by comparing domains.
const (
	operatorA = "operator@example.com"
	operatorB = "someone-else@example.com"
)

func testPageKey(t *testing.T) pageKey {
	t.Helper()

	k, err := newPageKey()
	if err != nil {
		t.Fatalf("newPageKey() = _, %v; want a key", err)
	}
	return k
}

func mustMint(t *testing.T, k pageKey, identity string, now time.Time) string {
	t.Helper()

	tok, err := k.mint(identity, now)
	if err != nil {
		t.Fatalf("mint(%q) = _, %v; want a token", identity, err)
	}
	return tok
}

// wantPageMAC is the contract's own computation — HMAC-SHA256 over identity
// "\n" expiry, hex-encoded — written out here so that a change to mac's payload
// has to be agreed to in two places.
func wantPageMAC(t *testing.T, k pageKey, identity string, expiry int64) string {
	t.Helper()

	mac := hmac.New(sha256.New, k[:])
	if _, err := mac.Write([]byte(identity + "\n" + strconv.FormatInt(expiry, 10))); err != nil {
		t.Fatalf("compute the expected page token MAC: %v", err)
	}
	return hex.EncodeToString(mac.Sum(nil))
}

// splitToken takes a token apart the way a tamperer would: the two halves, so a
// case can rebuild one of them and keep the other.
func splitToken(t *testing.T, token string) (expiry, mac string) {
	t.Helper()

	expiry, mac, ok := strings.Cut(token, pageTokenSeparator)
	if !ok {
		t.Fatalf("mint produced %q, which carries no %q", token, pageTokenSeparator)
	}
	return expiry, mac
}

// TestTokenBoundToIdentity is FR-007: a token minted into one operator's page is
// not a token another operator can act with.
//
// **Must fail when** the identity is dropped from the MAC input — a MAC over the
// expiry alone verifies for whoever presents it, which is a page token that
// proves the page and not the person.
//
// The positive half is not decoration. verify returning an error unconditionally
// would satisfy every negative assertion in this file, so each test asserts the
// acceptance it is the counterexample to.
func TestTokenBoundToIdentity(t *testing.T) {
	t.Parallel()

	key := testPageKey(t)
	token := mustMint(t, key, operatorA, testTime)

	if err := key.verify(token, operatorA, testTime); err != nil {
		t.Fatalf("verify(minted for A, as A) = %v; want it accepted", err)
	}

	if err := key.verify(token, operatorB, testTime); !errors.Is(err, errPageTokenMismatch) {
		t.Errorf("verify(minted for A, as B) = %v; want %v — the token is not bound to the identity",
			err, errPageTokenMismatch)
	}

	// The other direction too, so a MAC that happened to ignore its input
	// entirely cannot pass by symmetry.
	if err := key.verify(mustMint(t, key, operatorB, testTime), operatorA, testTime); !errors.Is(err, errPageTokenMismatch) {
		t.Errorf("verify(minted for B, as A) = %v; want %v", err, errPageTokenMismatch)
	}
}

// TestTokenExpiryIsCovered is the other half of what the MAC names: the holder
// may not extend their own token by editing the only part of it they can read.
//
// **Must fail when** the expiry is excluded from the MAC input — the edited
// token below then verifies, and every token becomes permanent in the hands of
// whoever holds one.
func TestTokenExpiryIsCovered(t *testing.T) {
	t.Parallel()

	key := testPageKey(t)
	token := mustMint(t, key, operatorA, testTime)

	if err := key.verify(token, operatorA, testTime); err != nil {
		t.Fatalf("verify(freshly minted) = %v; want it accepted", err)
	}

	// A year further out, with the MAC left exactly as minted.
	_, mac := splitToken(t, token)
	extended := strconv.FormatInt(testTime.Add(365*24*time.Hour).Unix(), 10) + pageTokenSeparator + mac

	if err := key.verify(extended, operatorA, testTime); !errors.Is(err, errPageTokenMismatch) {
		t.Errorf("verify(expiry moved forward, MAC kept) = %v; want %v — the expiry is not covered",
			err, errPageTokenMismatch)
	}

	// And backwards, which a holder has no reason to want and a proxy rewriting
	// a form field might produce anyway.
	shortened := strconv.FormatInt(testTime.Add(time.Minute).Unix(), 10) + pageTokenSeparator + mac
	if err := key.verify(shortened, operatorA, testTime); !errors.Is(err, errPageTokenMismatch) {
		t.Errorf("verify(expiry moved back, MAC kept) = %v; want %v", err, errPageTokenMismatch)
	}
}

// TestExpiredTokenRefused pins the lifetime itself, on both sides of the
// boundary.
//
// **Must fail when** the expiry comparison is removed — the last case is then
// accepted, and a captured token works forever — **or inverted**, which the two
// cases before it catch.
func TestExpiredTokenRefused(t *testing.T) {
	t.Parallel()

	key := testPageKey(t)
	token := mustMint(t, key, operatorA, testTime)

	for _, tt := range []struct {
		name    string
		at      time.Time
		wantErr error
	}{
		{
			name: "at the moment it was minted",
			at:   testTime,
		},
		{
			name: "one second before it lapses",
			at:   testTime.Add(pageTokenLifetime - time.Second),
		},
		{
			// data-model.md step 4 is "strictly greater than now", so the expiry
			// second itself is already too late. The boundary belongs on the
			// refusing side: a lifetime that is twelve hours plus however long the
			// last request takes is not a lifetime anyone bounded.
			name:    "at the expiry second exactly",
			at:      testTime.Add(pageTokenLifetime),
			wantErr: errPageTokenExpired,
		},
		{
			name:    "an hour after it lapsed",
			at:      testTime.Add(pageTokenLifetime + time.Hour),
			wantErr: errPageTokenExpired,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := key.verify(token, operatorA, tt.at)
			if !errors.Is(err, tt.wantErr) {
				t.Errorf("verify(%s) = %v; want %v", tt.name, err, tt.wantErr)
			}
		})
	}
}

// TestPageKeyIsNotTheSharedSecret is FR-006 at the point it is easiest to lose:
// the page key must have no relationship to CRSW_SHARED_SECRET, because a token
// is served to a browser and the shared secret authorises the whole API.
//
// **Must fail when** pageKey is derived from CRSW_SHARED_SECRET, by any of the
// three routes a hurried change would take — the secret used as the key, hashed
// into one, or copied wholesale.
//
// Two servers from one configuration is the assertion that catches a derivation
// this test did not think of: whatever the derivation, it is a function of a
// configuration both of these share, so both would mint the same MAC. The key
// being random is exactly the property that they do not.
func TestPageKeyIsNotTheSharedSecret(t *testing.T) {
	t.Parallel()

	first := newTestServer(t, loopbackListen)
	second := newTestServer(t, loopbackListen)

	if first.pageKey == second.pageKey {
		t.Fatal("two servers built from one configuration hold the same page key; it is derived from the configuration, not read from crypto/rand")
	}
	if first.pageKey == (pageKey{}) {
		t.Fatal("the page key is all zeroes; newServer left it unset")
	}

	secret := testSecret()
	if bytes.Equal(first.pageKey[:], secret) {
		t.Error("the page key is the shared secret itself")
	}
	if first.pageKey == pageKey(sha256.Sum256(secret)) {
		t.Error("the page key is a hash of the shared secret")
	}

	// The value a derived key would have put on the wire, computed from the
	// secret rather than from the key under test.
	expiry := testTime.Add(pageTokenLifetime).Unix()
	forbidden := hmac.New(sha256.New, secret)
	if _, err := forbidden.Write([]byte(operatorA + "\n" + strconv.FormatInt(expiry, 10))); err != nil {
		t.Fatalf("compute the forbidden MAC: %v", err)
	}
	derived := hex.EncodeToString(forbidden.Sum(nil))

	token := mustMint(t, first.pageKey, operatorA, testTime)
	if _, mac := splitToken(t, token); mac == derived {
		t.Error("the minted MAC is HMAC(CRSW_SHARED_SECRET, …); the page key is derived from the signing secret")
	}
	if strings.Contains(token, string(secret)) || strings.Contains(token, hex.EncodeToString(secret)) {
		t.Error("the token carries the shared secret")
	}

	// The second server refuses the first's tokens, which is the operator-visible
	// consequence of the key being per-process: a restart invalidates every
	// outstanding token (research R2), and this is what that looks like.
	if err := second.pageKey.verify(token, operatorA, testTime); !errors.Is(err, errPageTokenMismatch) {
		t.Errorf("a second server accepted the first's token: %v", err)
	}
}

// TestMintedTokenMatchesTheContractFormat pins the wire form data-model.md
// documents — <expiry>.<64 lowercase hex> — and the twelve-hour lifetime R3
// fixes. **Must fail when** either changes without the document changing.
func TestMintedTokenMatchesTheContractFormat(t *testing.T) {
	t.Parallel()

	key := testPageKey(t)
	token := mustMint(t, key, operatorA, testTime)

	rawExpiry, mac := splitToken(t, token)
	if strings.Contains(mac, pageTokenSeparator) {
		t.Fatalf("mint produced %q, which has more than two parts", token)
	}

	wantExpiry := testTime.Add(12 * time.Hour).Unix()
	if got, err := strconv.ParseInt(rawExpiry, 10, 64); err != nil || got != wantExpiry {
		t.Errorf("expiry = %q (%v); want %d — twelve hours after minting", rawExpiry, err, wantExpiry)
	}
	if want := wantPageMAC(t, key, operatorA, wantExpiry); mac != want {
		t.Errorf("mac = %q; want %q — HMAC-SHA256(pageKey, identity \\n expiry)", mac, want)
	}
	if len(mac) != 64 || mac != strings.ToLower(mac) {
		t.Errorf("mac = %q; want 64 lowercase hex characters", mac)
	}
}

// TestMalformedTokensAreRefused walks the validation sequence one step at a
// time. Each case is a token that passes every step before the one it is about,
// so a check deleted from the middle of the sequence shows up here as a case
// that is accepted rather than as a case that fails for the wrong reason.
//
// The sentinels are asserted rather than "some error", because they are what the
// trail records. No caller sees any of this: browser.go answers all of them with
// one uniform 403 (T003).
func TestMalformedTokensAreRefused(t *testing.T) {
	t.Parallel()

	key := testPageKey(t)
	live := testTime.Add(pageTokenLifetime).Unix()
	rawLive := strconv.FormatInt(live, 10)
	mac := wantPageMAC(t, key, operatorA, live)

	for _, tt := range []struct {
		name    string
		token   string
		wantErr error
	}{
		{
			name:    "absent",
			token:   "",
			wantErr: errPageTokenMissing,
		},
		{
			name:    "no separator",
			token:   rawLive + mac,
			wantErr: errPageTokenMalformed,
		},
		{
			name:    "no expiry at all",
			token:   pageTokenSeparator + mac,
			wantErr: errPageTokenMalformed,
		},
		{
			name:    "expiry is not a number",
			token:   "tomorrow" + pageTokenSeparator + mac,
			wantErr: errPageTokenMalformed,
		},
		{
			// Split on the last separator, so the expiry here is "<live>.<junk>".
			name:    "a second separator",
			token:   rawLive + pageTokenSeparator + "junk" + pageTokenSeparator + mac,
			wantErr: errPageTokenMalformed,
		},
		{
			// ParseInt accepts these and the MAC covers the rendered form, so
			// without the canonical-spelling check each is a second valid token for
			// an instant that already has one.
			name:    "expiry signed",
			token:   "+" + rawLive + pageTokenSeparator + mac,
			wantErr: errPageTokenMalformed,
		},
		{
			name:    "expiry zero-padded",
			token:   "000" + rawLive + pageTokenSeparator + mac,
			wantErr: errPageTokenMalformed,
		},
		{
			name:    "mac too short",
			token:   rawLive + pageTokenSeparator + mac[:len(mac)-1],
			wantErr: errPageTokenMalformed,
		},
		{
			name:    "mac not hex",
			token:   rawLive + pageTokenSeparator + strings.Repeat("z", pageTokenMACLen),
			wantErr: errPageTokenMalformed,
		},
		{
			// The uppercase twin. Refused rather than accepted, so exactly the
			// bytes minted are the bytes that work.
			name:    "mac uppercased",
			token:   rawLive + pageTokenSeparator + strings.ToUpper(mac),
			wantErr: errPageTokenMalformed,
		},
		{
			name:    "mac one character off",
			token:   rawLive + pageTokenSeparator + flipLastHex(mac),
			wantErr: errPageTokenMismatch,
		},
		{
			name:    "a MAC of the right shape from nowhere",
			token:   rawLive + pageTokenSeparator + strings.Repeat("0", pageTokenMACLen),
			wantErr: errPageTokenMismatch,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if err := key.verify(tt.token, operatorA, testTime); !errors.Is(err, tt.wantErr) {
				t.Errorf("verify(%q) = %v; want %v", tt.token, err, tt.wantErr)
			}
		})
	}

	// The control: the token every case above was built by damaging is accepted.
	if err := key.verify(rawLive+pageTokenSeparator+mac, operatorA, testTime); err != nil {
		t.Fatalf("verify(undamaged) = %v; want it accepted — every case above passes for the wrong reason", err)
	}
}

// flipLastHex changes exactly one character of a MAC, keeping it 64 lowercase
// hex characters, so the case using it reaches the comparison rather than being
// turned away by the shape check before it.
func flipLastHex(mac string) string {
	last := mac[len(mac)-1]
	if last == 'a' {
		return mac[:len(mac)-1] + "b"
	}
	return mac[:len(mac)-1] + "a"
}

// TestPageTokenRefusesAnEmptyIdentity covers the path that should not happen.
// Layer 1 refuses an assertion naming no person, so no caller behind the browser
// door has an empty identity — and a token bound to one would be a token every
// other empty identity verifies. **Must fail when** the guard is removed at
// either end: minting without an identity, or verifying against none.
func TestPageTokenRefusesAnEmptyIdentity(t *testing.T) {
	t.Parallel()

	key := testPageKey(t)

	if _, err := key.mint("", testTime); !errors.Is(err, errPageTokenNoIdentity) {
		t.Errorf("mint(\"\") = _, %v; want %v", err, errPageTokenNoIdentity)
	}
	if err := key.verify(mustMint(t, key, operatorA, testTime), "", testTime); !errors.Is(err, errPageTokenNoIdentity) {
		t.Errorf("verify(_, \"\") = %v; want %v", err, errPageTokenNoIdentity)
	}
}

// TestNewPageKeyFromRefusesShortEntropy is why newPageKey has a seam: a key with
// fewer random bytes behind it than its length claims is a key worth guessing,
// and the daemon must refuse to start rather than mint tokens with one.
func TestNewPageKeyFromRefusesShortEntropy(t *testing.T) {
	t.Parallel()

	errNoEntropy := errors.New("entropy source unavailable")

	for _, tt := range []struct {
		name    string
		reader  io.Reader
		wantErr error
	}{
		{
			name:    "the source failed",
			reader:  errPageReader{err: errNoEntropy},
			wantErr: errNoEntropy,
		},
		{
			name:    "the source ran out",
			reader:  bytes.NewReader(make([]byte, pageKeyBytes-1)),
			wantErr: io.ErrUnexpectedEOF,
		},
		{
			name:    "the source was empty",
			reader:  bytes.NewReader(nil),
			wantErr: io.EOF,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			k, err := newPageKeyFrom(tt.reader)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("newPageKeyFrom(%s) = _, %v; want %v", tt.name, err, tt.wantErr)
			}
			if k != (pageKey{}) {
				t.Error("newPageKeyFrom returned a partially filled key alongside its error")
			}
		})
	}

	// Exactly enough bytes is a key, and it is those bytes: a key built from a
	// short read padded out would be indistinguishable from this.
	full := make([]byte, pageKeyBytes)
	for i := range full {
		full[i] = byte(i)
	}
	k, err := newPageKeyFrom(bytes.NewReader(full))
	if err != nil {
		t.Fatalf("newPageKeyFrom(exactly %d bytes) = _, %v; want a key", pageKeyBytes, err)
	}
	if !bytes.Equal(k[:], full) {
		t.Errorf("newPageKeyFrom read %x; want %x", k[:], full)
	}
}

// errPageReader is an entropy source that only fails. It is spelled here rather
// than shared with internal/session's fixture of the same shape because the two
// packages do not export test helpers to each other.
type errPageReader struct{ err error }

func (r errPageReader) Read([]byte) (int, error) { return 0, r.err }
