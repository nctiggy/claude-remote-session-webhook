package tmuxctl

// tmux takes two kinds of target and they need different exact-match syntax.
// Getting it wrong is not a type error: send-keys, capture-pane, set-option and
// paste-buffer all fail at runtime against a bare "=name", and without the
// leading "=" tmux falls back to prefix matching, which would let a lookalike
// session named crswd-abc-decoy answer for crswd-abc.
//
// Two helpers rather than one Target(), so a caller cannot pick the wrong one.

// SessionTarget addresses the session itself: has-session, kill-session.
func SessionTarget(name string) string { return "=" + name }

// PaneTarget addresses the session's active pane: everything else.
func PaneTarget(name string) string { return "=" + name + ":" }
