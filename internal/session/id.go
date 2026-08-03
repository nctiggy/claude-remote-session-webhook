// Package session holds the session record, its identifiers, and the manager
// that keeps them in step with the tmux sessions they name.
package session

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
)

// idBytes is the raw entropy behind an ID, before encoding. FR-016 sets the
// floor at 128 bits and 16 bytes meets it exactly (research D9).
const idBytes = 16

// IDLen is the length of an encoded ID: two hex characters per byte.
const IDLen = idBytes * 2

// NewID returns a fresh session identifier: 16 bytes from the system CSPRNG
// rendered as 32 lowercase hex characters (FR-016, research D9).
//
// Hex rather than base64 because the ID becomes part of a tmux session name.
// Base64 brings "+", "/" and "=" and base64url brings "-", "_" and "=" — tmux
// reads ":" and "." as target syntax and the padding is not name-safe, so an
// alphabet of [a-f0-9] is the one that cannot produce a name tmux parses as
// something other than a name.
//
// Deliberately not crypto/rand.Text: it landed in Go 1.24 and go.mod declares
// 1.23.0 as the minimum language version, so reaching for it would raise the
// floor for every consumer of this module to buy an encoding this file already
// has. It also emits base32, which is the alphabet problem above.
func NewID() (string, error) {
	return newIDFrom(rand.Reader)
}

// newIDFrom is the seam NewID is a wrapper over, so the exhausted-entropy path
// is reachable from a test.
//
// It stays unexported on purpose. An exported version would let a caller hand
// the daemon its own randomness, and an ID is what a bearer token is scoped to
// — a predictable one is a session another caller can name. The only source
// that reaches production is rand.Reader, chosen here and nowhere else.
func newIDFrom(r io.Reader) (string, error) {
	var b [idBytes]byte
	// ReadFull, not Read: a short read must fail rather than yield an ID with
	// fewer random bytes behind it than its length advertises.
	if _, err := io.ReadFull(r, b[:]); err != nil {
		return "", fmt.Errorf("read %d random bytes for a session id: %w", idBytes, err)
	}
	return hex.EncodeToString(b[:]), nil
}
