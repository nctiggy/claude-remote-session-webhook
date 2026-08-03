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
// Verification is not yet complete. The 300-second window (FR-008) and the
// replay cache (FR-010) join this function in the next two tasks, against an
// injected clock; until then nothing in this repo binds a listener.
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

// The failure modes, one value each. They exist to be recorded server-side: the
// response a caller sees is uniform and reveals none of this (FR-011), and is
// built in internal/httpapi.
//
// No error here carries a header value, a body byte, or the secret. These
// strings reach the audit trail's reason field, which may never hold
// caller-supplied bytes (FR-042, FR-043) — which is also why the strconv parse
// error below is dropped rather than wrapped: it quotes its input.
var (
	ErrMissingTimestamp   = errors.New("missing request timestamp header")
	ErrMalformedTimestamp = errors.New("request timestamp header is not a decimal integer")
	ErrMissingSignature   = errors.New("missing request signature header")
	ErrSignatureMismatch  = errors.New("request signature does not match")
	ErrUnreadableBody     = errors.New("request body could not be read")
	ErrBodyTooLarge       = errors.New("request body exceeds the configured maximum")
)

// errUnsignable is unreachable — see sign — but a signature that could not be
// computed is a denial, never an accident that falls through to a comparison.
var errUnsignable = errors.New("request signature could not be computed")

// Authenticator verifies request signatures. It holds no per-request state and
// is safe for concurrent use by every handler.
type Authenticator struct {
	secret  []byte
	maxBody int64
}

// New fails closed on a configuration that would weaken the check. config.Load
// already refuses a secret under 32 bytes; this is the assertion that the two
// cannot drift apart, not a second opinion about the length.
func New(secret []byte, maxBody int64) (*Authenticator, error) {
	if len(secret) == 0 {
		return nil, errors.New("auth: no shared secret provided; refusing to start")
	}
	if maxBody < 1 {
		return nil, fmt.Errorf("auth: max body bytes must be at least 1, got %d; refusing to start", maxBody)
	}

	// Copied so that mutating the slice the config was loaded into cannot
	// change the key underneath a request already in flight.
	key := make([]byte, len(secret))
	copy(key, secret)

	return &Authenticator{secret: key, maxBody: maxBody}, nil
}

// Verify checks the signature over the request's timestamp and raw body, and
// leaves the body readable for the handler behind it.
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
	return nil
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
