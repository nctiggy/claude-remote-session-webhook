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

// conversationBytes is the entropy behind a conversation identifier. A UUID is
// 16 bytes by definition, six bits of which are spent on the version and variant
// markers below.
const conversationBytes = 16

// NewConversationID returns a fresh Claude conversation identifier: a random
// (version 4) UUID in the canonical 8-4-4-4-12 lowercase hexadecimal form (spec
// 012, FR-001, FR-002).
//
// # Why the daemon chooses it rather than discovering it
//
// The Claude CLI will take a conversation identifier at start (--session-id) and
// resume one by it (--resume). Everything else that identifies a conversation is
// derived and therefore ambiguous: --continue means "the most recent in this
// directory", which cannot tell two sessions in one directory apart, and a
// display name is silently renamed by the CLI when it collides with a live
// session. An identifier minted here is neither.
//
// It also fails safe at the far end. --resume opens an *interactive picker* when
// it cannot resolve what it was given, and a picker in a detached tmux pane is a
// session that hangs while still looking alive. A canonical UUID never reaches
// that path.
//
// The output is deliberately in the alphabet ValidateResume already accepts, so
// nothing minted here needs a second validator on its way to a command line.
func NewConversationID() (string, error) {
	return newConversationIDFrom(rand.Reader)
}

// newConversationIDFrom is the seam, exactly as newIDFrom is one and for the
// same reason: the exhausted-entropy path must be reachable from a test, and no
// caller may hand this daemon its own randomness.
func newConversationIDFrom(r io.Reader) (string, error) {
	var b [conversationBytes]byte
	if _, err := io.ReadFull(r, b[:]); err != nil {
		return "", fmt.Errorf("read %d random bytes for a conversation id: %w", conversationBytes, err)
	}
	// Version 4 and the RFC 4122 variant. They are set because a value that
	// claims to be a UUID and is not is a value some other tool is entitled to
	// reject, and this one is handed to a CLI this daemon does not own.
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80

	h := hex.EncodeToString(b[:])
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:32], nil
}
