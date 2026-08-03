// Package auth implements layer 2 of the daemon's authentication: the
// HMAC-SHA256 signature every request must carry (FR-007).
//
// Layer 1 is Cloudflare Access and arrives in milestone 2; layer 3 is the
// per-session bearer token. They are not substitutes for one another — if the
// tunnel is ever misconfigured, or another local process reaches the loopback
// listener, this signature is the only thing standing between a stranger and an
// unsandboxed shell (docs/auth-and-sessions.md).
//
// The signed payload is the timestamp and the raw body *together*, which is why
// the body is buffered here rather than by the handler: a signature over the
// method and path alone would let a captured request be replayed with a
// different prompt in it.
//
// Verify now runs all four of the layer-2 checks: the signature (FR-007,
// FR-009), the 300-second window (FR-008), and the seen-signature cache
// (FR-010), the last two on the same injected clock. What it does not yet do
// is name the caller or hide which check failed — the Caller type and the one
// opaque error arrive in T012, and the uniform 401 that FR-011 is really about
// is built in internal/httpapi. Nothing in this repo binds a listener yet.
package auth

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"
)

// The headers carrying the credential, per contracts/http-api.md.
const (
	HeaderTimestamp = "X-CRSW-Timestamp"
	HeaderSignature = "X-CRSW-Signature"
)

const (
	// signaturePrefix names the algorithm inside the header value, so adding a
	// second one later is a new prefix rather than a silent reinterpretation of
	// the same bytes. It is covered by the constant-time compare like the rest
	// of the value.
	signaturePrefix = "sha256="

	// payloadSeparator sits between the timestamp and the body so that a body
	// beginning with digits cannot be shifted into the timestamp and still
	// produce the same payload.
	payloadSeparator = "."
)

// maxSkew is the window either side of the daemon's own clock within which a
// request's timestamp is accepted (FR-008).
//
// It is not a tolerance to be widened when clocks disagree — it is the interval
// during which a captured request is replayable, and the replay cache's TTL is
// derived from it (replayTTL). Widening this widens both. Fix the clock.
const maxSkew = 300 * time.Second

// The failure modes, one value each. They exist to be recorded server-side: the
// response a caller sees is uniform and reveals none of this (FR-011), and is
// built in internal/httpapi.
//
// No error here carries a header value, a body byte, or the secret. These
// strings reach the audit trail's reason field, which may never hold
// caller-supplied bytes (FR-042, FR-043) — which is also why the strconv parse
// error below is dropped rather than wrapped: it quotes its input.
//
// ErrTimestampOutsideWindow is named for the window rather than for staleness:
// it is returned just as readily for a timestamp in the future, which is the
// direction that matters more.
var (
	ErrMissingTimestamp       = errors.New("missing request timestamp header")
	ErrMalformedTimestamp     = errors.New("request timestamp header is not a decimal integer")
	ErrTimestampOutsideWindow = errors.New("request timestamp is outside the accepted window")
	ErrMissingSignature       = errors.New("missing request signature header")
	ErrSignatureMismatch      = errors.New("request signature does not match")
	ErrReplayedRequest        = errors.New("request signature has already been used")
	ErrUnreadableBody         = errors.New("request body could not be read")
	ErrBodyTooLarge           = errors.New("request body exceeds the configured maximum")
)

// errUnsignable is unreachable — see sign — but a signature that could not be
// computed is a denial, never an accident that falls through to a comparison.
var errUnsignable = errors.New("request signature could not be computed")

// Authenticator verifies request signatures. It holds no per-request state and
// is safe for concurrent use by every handler.
type Authenticator struct {
	secret  []byte
	maxBody int64
	clock   Clock
	replay  *replayCache
}

// New builds an Authenticator on the host's clock. This is the constructor the
// daemon uses; tests reach for NewWithClock.
func New(secret []byte, maxBody int64) (*Authenticator, error) {
	return NewWithClock(secret, maxBody, systemClock{})
}

// NewWithClock fails closed on a configuration that would weaken the check.
// config.Load already refuses a secret under 32 bytes; this is the assertion
// that the two cannot drift apart, not a second opinion about the length.
func NewWithClock(secret []byte, maxBody int64, clock Clock) (*Authenticator, error) {
	if len(secret) == 0 {
		return nil, errors.New("auth: no shared secret provided; refusing to start")
	}
	if maxBody < 1 {
		return nil, fmt.Errorf("auth: max body bytes must be at least 1, got %d; refusing to start", maxBody)
	}
	// A nil clock would panic on the first request rather than at startup, and
	// an Authenticator that cannot tell the time cannot enforce the window at
	// all — which is a daemon that starts with half its auth disabled.
	if clock == nil {
		return nil, errors.New("auth: no clock provided; refusing to start")
	}

	// Copied so that mutating the slice the config was loaded into cannot
	// change the key underneath a request already in flight.
	key := make([]byte, len(secret))
	copy(key, secret)

	// One cache per Authenticator, on the same clock the window is measured
	// against. Two Authenticators would be two independent memories of what has
	// been seen, which is a replay cache that does not refuse replays.
	return &Authenticator{
		secret:  key,
		maxBody: maxBody,
		clock:   clock,
		replay:  newReplayCache(clock),
	}, nil
}

// Verify checks that the request's timestamp is inside the window and that its
// signature covers that timestamp and the raw body, and leaves the body readable
// for the handler behind it.
//
// The returned error names which check failed, for the audit trail only. Every
// caller of Verify answers all of them identically.
func (a *Authenticator) Verify(r *http.Request) error {
	rawTimestamp := r.Header.Get(HeaderTimestamp)
	if rawTimestamp == "" {
		return ErrMissingTimestamp
	}
	timestamp, err := strconv.ParseInt(rawTimestamp, 10, 64)
	if err != nil {
		return ErrMalformedTimestamp
	}
	// Before the body is read, let alone hashed: an unauthenticated caller
	// should not be able to make the daemon buffer and MAC a full-size body by
	// sending a timestamp from last year.
	if !a.withinWindow(timestamp) {
		return ErrTimestampOutsideWindow
	}

	signature := r.Header.Get(HeaderSignature)
	if signature == "" {
		return ErrMissingSignature
	}

	body, err := a.readBody(r)
	if err != nil {
		return err
	}

	want, err := a.sign(timestamp, body)
	if err != nil {
		return err
	}

	// hmac.Equal, never ==. A byte-by-byte compare that stops at the first
	// difference leaks the expected signature under timing (FR-009).
	if !hmac.Equal([]byte(want), []byte(signature)) {
		return ErrSignatureMismatch
	}

	// Last, and only for a signature that has already proved genuine. Observing
	// first would hand two things to anyone who can reach the listener and has
	// no secret at all: the cache would grow an entry per unauthenticated
	// request, and — worse — sending a copy of the bytes an honest caller is
	// about to send would record that request's signature before it arrived,
	// refusing it as a replay. The value keyed on is the one the daemon
	// computed, which hmac.Equal has just proved is the one the caller sent.
	if !a.replay.Observe(want) {
		return ErrReplayedRequest
	}
	return nil
}

// withinWindow answers FR-008: the timestamp must be within maxSkew of the
// daemon's own clock in *both* directions.
//
// The future bound is the one that is easy to leave out and expensive to omit.
// A timestamp bounded only from below never goes stale, so a single captured
// request signed a year ahead outlives the replay cache — which remembers a
// signature for 2 × maxSkew and no longer — and becomes a permanent key to an
// unsandboxed shell.
func (a *Authenticator) withinWindow(timestamp int64) bool {
	return a.clock.Now().Sub(time.Unix(timestamp, 0)).Abs() <= maxSkew
}

// readBody buffers the body so it can be signed over, then puts it back for the
// handler.
func (a *Authenticator) readBody(r *http.Request) ([]byte, error) {
	if r.Body == nil {
		return nil, nil
	}

	// One byte past the limit, so "exactly at the maximum" and "over it" are
	// distinguishable. A bare io.LimitReader truncates in silence, which would
	// hand the MAC a prefix of the caller's body and report a signature
	// mismatch for what is really an oversize request.
	body, err := io.ReadAll(io.LimitReader(r.Body, a.maxBody+1))

	// Restored on every path, including the failures: whatever touches this
	// request next finds a whole body or an empty one, never a half-drained
	// reader. The original Closer is dropped deliberately — net/http closes the
	// body it created, and this replacement has nothing to close.
	r.Body = io.NopCloser(bytes.NewReader(body))

	if err != nil {
		return nil, ErrUnreadableBody
	}
	if int64(len(body)) > a.maxBody {
		return nil, ErrBodyTooLarge
	}
	return body, nil
}

// sign builds the expected header value for a timestamp and body.
//
// The timestamp is re-rendered from the parsed value rather than copied out of
// the header, so one instant has exactly one signed spelling and a padded or
// signed variant cannot become a second valid form of the same request.
func (a *Authenticator) sign(timestamp int64, body []byte) (string, error) {
	payload := make([]byte, 0, len(payloadSeparator)+len(body)+20)
	payload = strconv.AppendInt(payload, timestamp, 10)
	payload = append(payload, payloadSeparator...)
	payload = append(payload, body...)

	mac := hmac.New(sha256.New, a.secret)
	// hash.Hash documents that Write never returns an error. errcheck is
	// configured to take nothing on trust, and the honest response to a
	// signature that could not be computed is to deny — so the impossible case
	// is returned rather than discarded.
	if _, err := mac.Write(payload); err != nil {
		return "", errUnsignable
	}
	return signaturePrefix + hex.EncodeToString(mac.Sum(nil)), nil
}
