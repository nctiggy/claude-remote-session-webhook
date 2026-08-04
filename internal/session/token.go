package session

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"time"
)

var (
	// ErrTokenMismatch is a credential that is not the one issued for the
	// session it was presented against (FR-014).
	//
	// It is distinct from ErrSessionNotFound for the operator reading the trail
	// and for nobody else: a wrong token means the caller already knew a real
	// session ID, which is a different event from a guess at one. The *client*
	// must not be able to tell the two apart, so both answer the uniform 404.
	ErrTokenMismatch = errors.New("session credential does not match")

	// ErrTokenExpired is a credential presented at or after the session's
	// absolute deadline (FR-015). Since TokenExpiry is AbsoluteDeadline, this is
	// also the moment the session itself stops being reachable — there is no
	// window where one outlives the other, and no renewal.
	ErrTokenExpired = errors.New("session credential has expired")
)

// tokenBytes is the raw entropy behind a bearer token, before encoding
// (data-model.md, SessionToken). Twice an ID's, because an ID is a name the
// daemon hands out freely and this is the credential that name is protected by
// — FR-014 makes the token the only thing standing between a caller who holds
// the shared secret and a session they did not create.
const tokenBytes = 32

// TokenLen is the length of an encoded token: two hex characters per byte.
const TokenLen = tokenBytes * 2

// NewToken returns a fresh bearer token and the SHA-256 hash that is all the
// daemon ever keeps of it (FR-013).
//
// The plaintext is returned, never stored: it exists in the create response and
// nowhere else, which is why this returns both values instead of writing either
// one to a record. A caller that loses it has lost the session — there is no
// re-issue path (FR-015), and adding one would mean keeping something that could
// mint the credential again.
//
// Hex for the same reason NewID uses it, minus the tmux argument: an alphabet of
// [a-f0-9] survives a URL, a header, and a shell transcript without a second
// encoding that could give one credential two spellings.
func NewToken() (string, [sha256.Size]byte, error) {
	return newTokenFrom(rand.Reader)
}

// newTokenFrom is the seam NewToken wraps, so the exhausted-entropy path is
// reachable from a test. It stays unexported for the reason newIDFrom does: the
// only randomness that reaches production is rand.Reader, chosen in one place.
func newTokenFrom(r io.Reader) (string, [sha256.Size]byte, error) {
	var b [tokenBytes]byte
	// ReadFull, not Read: a short read must fail rather than yield a token with
	// fewer random bytes behind it than its length advertises.
	if _, err := io.ReadFull(r, b[:]); err != nil {
		return "", [sha256.Size]byte{}, fmt.Errorf("read %d random bytes for a session token: %w", tokenBytes, err)
	}

	tok := hex.EncodeToString(b[:])
	return tok, hashToken(tok), nil
}

// hashToken is the one place a token becomes a hash, so the value stored at
// creation and the value compared on every request are produced by the same
// expression rather than by two that agree today.
//
// It hashes the *encoded* token, not the bytes behind it. Hex has two spellings
// for every byte and hex.DecodeString accepts both, so hashing decoded bytes
// would give every credential an uppercase twin: two strings that open the same
// session, only one of which was ever issued. Hashing the transported form means
// exactly the bytes handed out are the bytes that work.
func hashToken(tok string) [sha256.Size]byte {
	return sha256.Sum256([]byte(tok))
}

// CheckToken is the whole of a session's credential check: presented must be the
// bearer token issued for this record, and it must arrive inside the lifetime
// that token was issued for (FR-014, FR-015).
//
// The two are one call rather than two for the reason Manager.Resolve is one
// call rather than five: the failure worth designing against is not a check
// written wrongly, it is a future session-scoped path that remembers the match
// and forgets the expiry. A credential verified by that path would have no end,
// and the session behind it runs with --dangerously-skip-permissions.
//
// The instant arrives as a parameter and never from time.Now. The deadline it is
// measured against is derived from the record, so a caller passing the manager's
// clock gets the answer the reaper will give on the same clock (T036) — there is
// no arrangement where a credential is live by one reading of time and expired by
// another.
//
// Order is deliberate: the match is answered first. A value that was never issued
// is a guess whatever the clock says, and the trail should record it as one —
// answering "expired" would tell the operator that a credential which never
// existed had once been real. No caller learns the difference either way; both
// sentinels reach the same uniform 404 (FR-033).
func (s Session) CheckToken(presented string, now time.Time) error {
	if !s.TokenMatches(presented) {
		return ErrTokenMismatch
	}
	// Before, not !After: at the deadline the credential is already gone. The
	// boundary belongs on the refusing side of the comparison for the same reason
	// the signature window's does — a lifetime that is "24 hours plus however long
	// the last request takes" is not a lifetime anyone bounded.
	//
	// TokenExpiry, never CreatedAt.Add(AbsoluteLifetime) spelled again here. The
	// sum exists in exactly one expression, and that expression returns
	// AbsoluteDeadline, so FR-015's "equal by construction" is a property of the
	// code rather than of two numbers that agree today: shortening the
	// credential's life without shortening the session's is not an edit that can
	// be made in this method.
	if !now.Before(s.TokenExpiry()) {
		return ErrTokenExpired
	}
	return nil
}

// TokenMatches reports whether presented is the bearer token issued for this
// session (FR-014).
//
// It answers the match and nothing else. CheckToken is what a request path
// calls: a caller that stops here has checked a credential without checking
// whether it is still one.
//
// hmac.Equal, never ==. Hashing first already destroys the correlation an
// early-exit compare could leak — SHA-256 avalanches, so two tokens differing in
// one character produce hashes differing in about half their bytes, and where
// the compare stops says nothing about where the guess was wrong. The
// constant-time compare is here anyway because "this particular == happens to
// be safe" is a claim every future reader has to re-derive, and one of them will
// re-derive it in a place where it is false.
//
// Ownership is not checked here. Store.Get takes the owner as a parameter and
// answers ErrSessionNotFound without it, so a record only reaches this method
// having already matched its owner (FR-032) — this is the second of the two
// checks T023 wires together, not a replacement for the first.
func (s Session) TokenMatches(presented string) bool {
	if !s.hasToken() {
		return false
	}

	got := hashToken(presented)
	return hmac.Equal(got[:], s.TokenHash[:])
}

// hasToken reports whether a credential was ever issued for this record.
//
// Store.Add does not require a TokenHash, so a record can exist with the zero
// value in that field. Nothing should create one — but a record that never had a
// credential must not be able to accept one, and the alternative to this guard
// is trusting that nobody ever presents a preimage of the zero hash. That is
// true, and it is not the kind of true this project rests an unsandboxed shell
// on.
func (s Session) hasToken() bool {
	return s.TokenHash != [sha256.Size]byte{}
}
