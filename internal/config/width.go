package config

import (
	"errors"
	"strconv"
	"strings"
)

// width.go holds the one number in this package that the operator does not
// write.
//
// Every other value here is read from an environment variable or a
// configuration file at startup, and every loader refuses what it cannot
// understand: a daemon running with a setting nobody wrote is worse than a
// daemon that will not start. A pane width inverts both halves of that. It is
// reported by a browser (#120) — nobody typed it, nobody can correct it, and the
// person who would have to read a refusal is holding a phone. So it is
// advisory: a value outside the bounds is brought inside them, a value that is
// not a number is the default, and neither is an error.
//
// That is also why there is no CRSW_PANE_WIDTH beside CRSW_PANE_BOUND. A width
// is a property of one session — written onto that session and read back from
// it — and not a setting the daemon holds for all of them.

const (
	// DefaultPaneWidth is tmux's own width for a window no client has ever
	// attached to (measured 80 on tmux 3.4). It is a default here because it is
	// what a session with nothing recorded about its width already *is*: every
	// session started before this milestone, and every session since that nobody
	// has reflowed.
	DefaultPaneWidth = 80

	// MinPaneWidth and MaxPaneWidth bound what makes a terminal usable, which is
	// a different question from what tmux accepts. internal/tmuxctl clamps to
	// tmux's own 1..10000 where the argv is built, because that is the last
	// boundary before the exec; this is the narrower policy in front of it, and
	// the duplication is deliberate.
	//
	// A reflow changes the session for every reader at once — that is the whole
	// of the offered-not-taken policy — so the floor is not a display
	// preference. It is the point below which one viewer would make the session
	// unreadable for all of them. Twenty columns is well under the narrowest
	// width measured from a real viewer here (44, on a phone) and about where a
	// shell prompt carrying a path stops fitting on one line.
	//
	// The ceiling is past the widest display anyone reads a pane on, and the
	// control that reports a width is only offered to a viewer *narrower* than
	// the session, so no honest reflow arrives anywhere near it. What it is for
	// is that no width this daemon accepts can be a width tmux would refuse.
	MinPaneWidth = 20
	MaxPaneWidth = 500
)

// ClampPaneWidth brings a width inside the bounds. It never fails, for the
// reason stated at the top of this file.
func ClampPaneWidth(cols int) int {
	if cols < MinPaneWidth {
		return MinPaneWidth
	}
	if cols > MaxPaneWidth {
		return MaxPaneWidth
	}
	return cols
}

// ParsePaneWidth reads a width that arrived as text — a form field from the
// browser, or the tmux option a session carries its width in, read back after a
// restart — and answers a width that is always usable.
//
// Anything numeric is clamped, *including* a digit string too large to hold:
// strconv saturates on ErrRange, so the value it hands back with that error
// still clamps to the ceiling. Treating it as gibberish instead would put a
// cliff between nine billion, which clamps, and one digit more, which would not.
// Only something that is not a number at all falls back to the default — which
// is what an empty field and an absent option both are.
func ParsePaneWidth(v string) int {
	cols, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil && !errors.Is(err, strconv.ErrRange) {
		return DefaultPaneWidth
	}
	return ClampPaneWidth(cols)
}
