package auth

import (
	"errors"
)

// CallerID is the authenticated origin of a request (FR-012).
//
// It is a named type rather than a bare string so that milestone 2's Cloudflare
// Access identity is a new *source* for the same shape: handlers already compare
// session.Owner against a CallerID, and nothing about them changes when a second
// credential can produce one.
type CallerID string

// CallerOperator is the identity behind the shared secret — the single caller
// milestone 1 has, spelled the way data-model.md's audit table spells it.
//
// It is a constant and not a configured value on purpose. Anything an operator
// could set here would be a second place for the identity to disagree with the
// one adopted sessions are given at startup (T031), and an ownership check is
// only as good as both sides naming the same thing.
const CallerOperator CallerID = "operator"

// Caller is who the daemon decided a verified request came from.
//
// Verify returns one only on success, so possessing a *Caller is itself the
// evidence that layer 2 passed — a handler cannot accidentally act on an
// identity for a request that was refused.
type Caller struct {
	// ID is the server-derived identity. It is what session ownership is
	// recorded and checked against, and what the audit trail's caller field
	// carries.
	ID CallerID
}

// caller names the identity behind the credential this Authenticator holds.
//
// It deliberately takes no *http.Request. FR-012 says identity is never taken
// from a field the caller supplies in the body, path, or headers, and the way
// to keep that true through every future change is to give the derivation
// nothing to read: an X-CRSW-Caller header, an "owner" key in the JSON, or a
// spoofed X-Forwarded-User cannot reach a function that was never handed them.
// A caller proves who it is by producing a signature over its request, and the
// only thing that can produce one is the shared secret.
func (a *Authenticator) caller() *Caller {
	return &Caller{ID: CallerOperator}
}

// ErrUnauthorized is what every verification failure presents to whatever called
// Verify. It carries the same word the client is eventually answered with, and
// it is the only thing about a denial that leaves this package by accident.
var ErrUnauthorized = errors.New("unauthorized")

// denial is the single opaque error every failed Verify returns (FR-011).
//
// The reason is on an unexported field, reachable only by calling Reason, which
// is a deliberate act at a call site rather than something a handler does by
// rendering an error. That is the whole design: the type has one message, one
// Unwrap target, and one dynamic type, so a missing timestamp, a forged
// signature, a stale clock, and a replayed request are indistinguishable to
// everything except code that explicitly asks for the audit reason.
//
// Unwrap deliberately does *not* return the reason. Unwrapping to it would make
// errors.Is(err, ErrSignatureMismatch) answer true, and then which check failed
// would be one honest-looking branch away from the response body.
type denial struct{ reason error }

// deny wraps a server-side reason in the opaque value.
func deny(reason error) error {
	if reason == nil {
		// Unreachable — every call site passes a sentinel — but a denial with
		// nothing behind it is still a denial, never a hole that reads as
		// success further up.
		reason = ErrUnauthorized
	}
	return &denial{reason: reason}
}

func (d *denial) Error() string { return ErrUnauthorized.Error() }

func (d *denial) Unwrap() error { return ErrUnauthorized }

// Reason reports the server-side account of why Verify refused a request, for
// the audit trail's reason field and for nothing else (FR-011, FR-042).
//
// The errors it returns are this package's sentinels: fixed strings authored
// here, never built from anything the caller sent. Passing one to a client
// undoes the uniform response the denial exists to produce.
//
// An error from anywhere else is returned unchanged, and nil stays nil, so a
// middleware can reach for the reason without first asking where the error came
// from.
func Reason(err error) error {
	var d *denial
	if errors.As(err, &d) {
		return d.reason
	}
	return err
}
