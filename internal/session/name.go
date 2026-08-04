package session

import (
	"errors"
	"fmt"
)

// MaxNameLen is the ceiling FR-027 sets, counted in bytes. Every byte the name
// alphabet admits is one ASCII character, so a byte count and a character count
// are the same number here — which is what lets the check below run over bytes
// and still mean what the spec's regexp means.
const MaxNameLen = 64

// tmux reads a target as "session:window.pane" (research D2). Named so the two
// characters and the reason they are special cannot drift apart.
const (
	tmuxWindowSeparator = ':'
	tmuxPaneSeparator   = '.'
)

var (
	// ErrInvalidName wraps every rejection, so a handler answering 400 has
	// exactly one thing to branch on (contracts/http-api.md).
	ErrInvalidName = errors.New("invalid session name")

	// ErrNameIsTmuxTarget marks the two characters that are not merely outside
	// the alphabet but *meaningful*. It is wrapped alongside ErrInvalidName so
	// the explicit rejection FR-027 asks for is observable: without it, deleting
	// the dedicated guard would change nothing a test could see, because the
	// character class rejects ":" and "." on its way past anyway.
	ErrNameIsTmuxTarget = errors.New(`tmux reads ":" and "." as target syntax`)
)

// ValidateName enforces FR-027's ^[a-zA-Z0-9-]{1,64}$ on a caller-supplied
// session name, returning an error wrapping ErrInvalidName if it does not hold.
//
// The name is a display label; data-model.md keeps it out of every tmux target,
// which is built from the ID alone. This check is the second line of that
// defence rather than the first — if a later change ever does put a name near a
// target, near a shell, or in a page, the alphabet is already too boring to
// address, quote, or close a tag.
//
// Nothing is trimmed, lowercased, or otherwise repaired. A silently corrected
// name is one the caller never chose, and the record, the audit trail, and the
// dashboard would each then be free to disagree about which name it was.
func ValidateName(name string) error {
	if name == "" {
		return fmt.Errorf("%w: a name is required", ErrInvalidName)
	}
	if len(name) > MaxNameLen {
		return fmt.Errorf("%w: longer than %d characters", ErrInvalidName, MaxNameLen)
	}

	for i := 0; i < len(name); i++ {
		c := name[i]
		// Checked before the alphabet, not after, so that widening the alphabet
		// later cannot quietly re-admit the two characters that address a
		// different tmux object.
		if c == tmuxWindowSeparator || c == tmuxPaneSeparator {
			return fmt.Errorf("%w: %w", ErrInvalidName, ErrNameIsTmuxTarget)
		}
		if !isNameByte(c) {
			return fmt.Errorf(`%w: only ASCII letters, digits and "-" are allowed`, ErrInvalidName)
		}
	}

	return nil
}

// isNameByte is FR-027's character class, spelled out a byte at a time.
//
// Deliberately not unicode.IsLetter or unicode.IsDigit: those admit thousands of
// characters that render like ASCII and are not one — a full-width colon reads
// as ":" to a human reviewing an audit record. A name is stored, logged, and
// displayed, never parsed, so the alphabet is kept to what all three agree on.
func isNameByte(c byte) bool {
	return c >= 'a' && c <= 'z' ||
		c >= 'A' && c <= 'Z' ||
		c >= '0' && c <= '9' ||
		c == '-'
}
