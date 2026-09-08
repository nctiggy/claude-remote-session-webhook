package session

import "testing"

// The fixtures below are stripped pane captures — tmuxctl.Strip has already
// run, exactly as it has by the time anything reaches DetectDialog — shaped
// after what tmux actually hands back for these screens (PR #150's commit
// message quotes the /rc menu's own wording verbatim; the workspace-trust
// wording is Claude Code's own, quoted the same way in
// loop/trust-seed.sh, ai-lawnmower). They are deliberately not the box art
// verbatim — the box-drawing characters and exact column widths vary with
// terminal size and Claude Code version, which is the whole reason
// dialogSignature matches on a stable phrase rather than the frame around it.

const healthyPromptPane = `╭──────────────────────────────────────────────╮
│ >                                              │
╰──────────────────────────────────────────────╯
  ? for shortcuts`

const workspaceTrustPane = `╭──────────────────────────────────────────────╮
│ Do you trust the files in this folder?        │
│                                                │
│ /home/craig/code/some-repo                     │
│                                                │
│ ❯ 1. Yes, proceed                              │
│   2. No, exit                                  │
╰──────────────────────────────────────────────╯
   Enter to confirm · Esc to exit`

const rcMenuPane = `╭──────────────────────────────────────────────╮
│ ❯ Disconnect this session                      │
│   Show QR code                                 │
│   Continue                                     │
╰──────────────────────────────────────────────╯
  Enter to select · Esc to continue`

// uncatalogedDialogPane is what a dialog this registry has never seen looks
// like on the wire: it carries a generic marker several known dialogs share,
// but none of dialogSignatures' own named phrases. This is the case
// DisplayUnknown exists for.
const uncatalogedDialogPane = `╭──────────────────────────────────────────────╮
│ A future Claude Code dialog nobody has         │
│ catalogued in this registry yet                │
╰──────────────────────────────────────────────╯
  Enter to confirm · Esc to cancel`

func TestDetectDialog(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name       string
		pane       string
		wantName   string
		wantDialog bool
	}{
		{name: "an empty pane matches nothing", pane: "", wantDialog: false},
		{name: "an ordinary prompt is not a dialog", pane: healthyPromptPane, wantDialog: false},
		{name: "the workspace-trust dialog is named", pane: workspaceTrustPane, wantName: "workspace-trust", wantDialog: true},
		{name: "the /rc remote-control menu is named (#150)", pane: rcMenuPane, wantName: "rc-menu", wantDialog: true},
		{
			name:       "a dialog-shaped pane with no catalogued signature is unknown, not healthy",
			pane:       uncatalogedDialogPane,
			wantName:   "",
			wantDialog: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			name, dialog := DetectDialog(tc.pane)
			if dialog != tc.wantDialog {
				t.Fatalf("DetectDialog(%q) dialog = %v, want %v", tc.name, dialog, tc.wantDialog)
			}
			if name != tc.wantName {
				t.Fatalf("DetectDialog(%q) name = %q, want %q", tc.name, name, tc.wantName)
			}
		})
	}
}

// TestASuspiciousMarkerNeverNamesADialog holds the registry's own rule against
// itself: a phrase in suspiciousMarkers must never also be one of a
// dialogSignature's own phrases. If it were, a dialog sharing only the generic
// marker would resolve to that signature's name — a match this confident it
// has not earned — instead of the honest "unknown" a bare marker is supposed
// to produce.
func TestASuspiciousMarkerNeverNamesADialog(t *testing.T) {
	t.Parallel()
	for _, marker := range suspiciousMarkers {
		for _, sig := range dialogSignatures {
			for _, phrase := range sig.phrases {
				if phrase == marker {
					t.Fatalf("suspicious marker %q is also dialogSignature %q's own phrase", marker, sig.name)
				}
			}
		}
	}
}

// TestNoTwoSignaturesShareAName pins the registry against a copy-paste that
// gave two entries the same identifier, which would leave DetectDialog's name
// ambiguous about which entry actually matched.
func TestNoTwoSignaturesShareAName(t *testing.T) {
	t.Parallel()
	seen := make(map[string]bool, len(dialogSignatures))
	for _, sig := range dialogSignatures {
		if seen[sig.name] {
			t.Fatalf("dialogSignatures has two entries named %q", sig.name)
		}
		seen[sig.name] = true
	}
}
