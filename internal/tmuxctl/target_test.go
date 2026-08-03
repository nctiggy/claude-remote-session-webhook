package tmuxctl_test

import (
	"strings"
	"testing"

	"github.com/nctiggy/claude-remote-session-webhook/internal/tmuxctl"
)

func TestTargets(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		session     string
		wantSession string
		wantPane    string
	}{
		{
			name:        "typical session name",
			session:     "crswd-9f2c4a1b8e6d3f7a0c5b2e9d4f1a7c3b",
			wantSession: "=crswd-9f2c4a1b8e6d3f7a0c5b2e9d4f1a7c3b",
			wantPane:    "=crswd-9f2c4a1b8e6d3f7a0c5b2e9d4f1a7c3b:",
		},
		{
			// The lookalike from the adoption tests: without the leading "="
			// tmux prefix-matches, and this name would answer for the one above.
			name:        "lookalike sharing a prefix",
			session:     "crswd-9f2c4a1b8e6d3f7a0c5b2e9d4f1a7c3b-decoy",
			wantSession: "=crswd-9f2c4a1b8e6d3f7a0c5b2e9d4f1a7c3b-decoy",
			wantPane:    "=crswd-9f2c4a1b8e6d3f7a0c5b2e9d4f1a7c3b-decoy:",
		},
		{
			name:        "short name",
			session:     "a",
			wantSession: "=a",
			wantPane:    "=a:",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := tmuxctl.SessionTarget(tc.session); got != tc.wantSession {
				t.Errorf("SessionTarget(%q) = %q, want %q", tc.session, got, tc.wantSession)
			}
			if got := tmuxctl.PaneTarget(tc.session); got != tc.wantPane {
				t.Errorf("PaneTarget(%q) = %q, want %q", tc.session, got, tc.wantPane)
			}
		})
	}
}

// The two helpers exist separately because tmux rejects the wrong one at
// runtime rather than at compile time: send-keys against a bare "=name" fails
// with "can't find pane". If they ever return the same string, that guard is
// gone and nothing else in the codebase would notice.
func TestTargetsAreNotInterchangeable(t *testing.T) {
	t.Parallel()

	const name = "crswd-abc123"
	if tmuxctl.SessionTarget(name) == tmuxctl.PaneTarget(name) {
		t.Fatalf("SessionTarget and PaneTarget both returned %q", tmuxctl.SessionTarget(name))
	}
}

// The leading "=" is what makes a target an exact match. Drop it and tmux falls
// back to prefix matching, so a request scoped to one session could reach
// another — the isolation property the whole daemon rests on.
func TestTargetsAreExactMatch(t *testing.T) {
	t.Parallel()

	const name = "crswd-abc123"
	for _, got := range []string{tmuxctl.SessionTarget(name), tmuxctl.PaneTarget(name)} {
		if !strings.HasPrefix(got, "=") {
			t.Errorf("target %q is not an exact-match target", got)
		}
	}
}
