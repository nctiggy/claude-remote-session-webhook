// Package tmuxctl is the only place in the daemon that executes anything.
//
// Every method builds an argv slice for exec.Command. No shell string is ever
// constructed — not with sh -c, not with fmt.Sprintf into a command line. A
// request that reaches this package has already passed authentication, and
// sessions run with --dangerously-skip-permissions, so this boundary is the
// last one that still holds.
package tmuxctl

import (
	"context"
	"time"
)

// Controller drives a tmux server. It is an interface so that every other
// package's tests run against the in-memory fake rather than a real tmux
// binary, and so the argv of each call can be asserted rather than reviewed.
//
// name is always the tmux session name (crswd-<id>), derived from the session
// ID alone. No caller-supplied string reaches this package.
type Controller interface {
	// New creates a detached session running the login shell only. The Claude
	// command arrives separately via SendKeys, so a Claude crash leaves an
	// inspectable shell instead of a vanished session.
	New(ctx context.Context, name, workDir string) error

	// SetOption sets a tmux user option on the session, which is how
	// provenance (@crswd-managed, @crswd-owner) survives a daemon restart.
	SetOption(ctx context.Context, name, option, value string) error

	// SendKeys sends daemon-authored key constants. Caller text must go
	// through Paste: tmux's parser strips a trailing unescaped ';' from the
	// final argument before -l applies, and -- does not prevent it.
	SendKeys(ctx context.Context, name string, keys ...string) error

	// Paste delivers caller-supplied text byte-for-byte by writing it to a
	// tmux buffer over stdin, so the payload never becomes part of a tmux
	// command line. Submit it afterwards with SendKeys(name, "Enter").
	Paste(ctx context.Context, name string, payload []byte) error

	// CapturePane returns the rendered pane as plain text. The implementation
	// must never pass -e, which would reconstruct ANSI escapes from cell
	// attributes and hand raw control bytes to the API.
	CapturePane(ctx context.Context, name string) (string, error)

	// Kill asks tmux to destroy the session. It is not teardown on its own —
	// callers confirm with Has, because an orphaned session is a live
	// unsandboxed shell with no owner.
	Kill(ctx context.Context, name string) error

	// Has reports whether the session exists. An error means tmux itself
	// failed and the answer is unknown; collapsing that into "gone" would
	// report a teardown that never happened.
	Has(ctx context.Context, name string) (bool, error)

	// List returns every session on the server in one call, which is all
	// startup reconciliation needs. A server that is not running yields an
	// empty slice, not an error — that is the normal first-boot case.
	List(ctx context.Context) ([]SessionInfo, error)
}

// The tmux user options the daemon writes onto every session it creates, and
// reads back on startup to decide what it owns (research D3).
//
// They live here, next to the List format string that interpolates
// OptionManaged, because the name written and the name matched must be the same
// string. internal/session sets them and cannot export a constant into this
// package — the import runs the other way.
const (
	// OptionManaged is provenance: its presence means we created the session.
	// FR-022 turns on it, so a session missing it is neither adopted nor killed.
	OptionManaged = "@crswd-managed"

	// OptionOwner records which identity created the session, so adoption after
	// a restart has an owner to hand the record rather than a guess.
	OptionOwner = "@crswd-owner"

	// OptionName and OptionWorkDir carry the two facts a record holds that the
	// host otherwise does not (#72). They exist because adoption stopped being a
	// crash-recovery path and became the ordinary one: sessions now survive a
	// restart (#63), so every redeploy runs adoption, and a fleet of unnamed
	// sessions in unknown directories is what the operator sees afterwards.
	//
	// Stored on the tmux session itself rather than derived, because neither can
	// be recovered any other way: the label was never anywhere but the daemon's
	// memory, and a pane's current directory is where the shell has wandered to
	// rather than where the session was started.
	OptionName    = "@crswd-name"
	OptionWorkDir = "@crswd-workdir"

	// OptionManagedValue is what OptionManaged is set to. List only tests the
	// option for emptiness, so this is a marker rather than data — but it is
	// spelled once so a future reader does not have to check whether the value
	// carries meaning.
	OptionManagedValue = "1"
)

// SessionInfo is one row of List. Created is the origin of an adopted
// session's absolute deadline, so it comes from tmux's own #{session_created}
// rather than from the time we happened to notice the session.
type SessionInfo struct {
	Name    string
	Created time.Time
	Managed bool // did we create it? Anything else is neither adopted nor touched.

	// Label and WorkDir are what the daemon recorded when it created the session
	// (#72), read back so adoption can restore them. Empty for a session created
	// before these options existed, which is why every caller must treat absence
	// as "unknown" rather than as a value.
	//
	// Label, not Name: Name above is tmux's own session name, which is the id.
	Label   string
	WorkDir string
}
