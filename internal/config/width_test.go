package config_test

import (
	"strconv"
	"strings"
	"testing"

	"github.com/nctiggy/claude-remote-session-webhook/internal/config"
)

// The bounds are a policy about a usable terminal, so the default has to sit
// inside them: a floor raised past 80 would silently widen every session that
// has never been reflowed, which is every session that predates milestone 16.
func TestTheDefaultWidthIsInsideTheBounds(t *testing.T) {
	t.Parallel()

	if config.DefaultPaneWidth < config.MinPaneWidth || config.DefaultPaneWidth > config.MaxPaneWidth {
		t.Errorf("DefaultPaneWidth %d is outside [%d, %d]",
			config.DefaultPaneWidth, config.MinPaneWidth, config.MaxPaneWidth)
	}
}

// Both edges and past both edges, which is what #120's "advisory only" means in
// practice: every one of these answers a width, and none of them is an error.
func TestClampPaneWidthBringsAWidthInside(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		cols int
		want int
	}{
		{"the floor itself is taken", config.MinPaneWidth, config.MinPaneWidth},
		{"the ceiling itself is taken", config.MaxPaneWidth, config.MaxPaneWidth},
		{"one below the floor comes up", config.MinPaneWidth - 1, config.MinPaneWidth},
		{"one above the ceiling comes down", config.MaxPaneWidth + 1, config.MaxPaneWidth},
		{"a phone's width is untouched", 44, 44},
		{"tmux's own default is untouched", config.DefaultPaneWidth, config.DefaultPaneWidth},
		{"zero comes up", 0, config.MinPaneWidth},
		{"a negative comes up", -44, config.MinPaneWidth},
		{"nine million comes down", 9_000_000, config.MaxPaneWidth},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := config.ClampPaneWidth(tc.cols); got != tc.want {
				t.Errorf("ClampPaneWidth(%d) = %d, want %d", tc.cols, got, tc.want)
			}
		})
	}
}

// A width reaches this daemon as text every time: a form field from a browser,
// and the tmux option a reflowed session carries across a restart. Nothing here
// may refuse, and nothing here may hand a caller a width it cannot use.
func TestParsePaneWidthNeverRefuses(t *testing.T) {
	t.Parallel()

	// Past what an int can hold, which strconv reports as a range error while
	// still handing back a saturated value. It is a number, so it clamps.
	huge := strings.Repeat("9", 40)

	for _, tc := range []struct {
		name string
		v    string
		want int
	}{
		{"a width a phone reports", "44", 44},
		{"surrounding space", "  44  ", 44},
		{"the floor itself", strconv.Itoa(config.MinPaneWidth), config.MinPaneWidth},
		{"the ceiling itself", strconv.Itoa(config.MaxPaneWidth), config.MaxPaneWidth},
		{"one below the floor", strconv.Itoa(config.MinPaneWidth - 1), config.MinPaneWidth},
		{"one above the ceiling", strconv.Itoa(config.MaxPaneWidth + 1), config.MaxPaneWidth},
		{"nine million", "9000000", config.MaxPaneWidth},
		{"more digits than an int holds", huge, config.MaxPaneWidth},
		{"a negative", "-44", config.MinPaneWidth},
		{"zero", "0", config.MinPaneWidth},
		{"an absent option", "", config.DefaultPaneWidth},
		{"a word", "wide", config.DefaultPaneWidth},
		{"a CSS length", "44px", config.DefaultPaneWidth},
		{"a fraction", "44.5", config.DefaultPaneWidth},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := config.ParsePaneWidth(tc.v)
			if got != tc.want {
				t.Errorf("ParsePaneWidth(%q) = %d, want %d", tc.v, got, tc.want)
			}
			// The property behind every row, asserted separately so that a
			// future bound change breaks the row and not the guarantee.
			if got < config.MinPaneWidth || got > config.MaxPaneWidth {
				t.Errorf("ParsePaneWidth(%q) = %d, outside [%d, %d]",
					tc.v, got, config.MinPaneWidth, config.MaxPaneWidth)
			}
		})
	}
}
