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

// SessionInfo is one row of List. Created is the origin of an adopted
// session's absolute deadline, so it comes from tmux's own #{session_created}
// rather than from the time we happened to notice the session.
type SessionInfo struct {
	Name    string
	Created time.Time
	Managed bool // did we create it? Anything else is neither adopted nor touched.
}
