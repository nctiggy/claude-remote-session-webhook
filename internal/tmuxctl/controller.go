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

	// ReconcileServerEnvironment removes from the tmux server's global
	// environment everything a session's own environment would not carry, and
	// returns the names it removed.
	//
	// On the interface rather than only on *Exec because the daemon must be
	// able to call it at startup without reaching past the abstraction, and
	// because a fake that could not answer it would let a test claim a clean
	// startup path that was never exercised.
	//
	// A server that is not running is not an error: there is nothing to clean,
	// and the first session created will start one from a client this package
	// has already given a composed environment.
	ReconcileServerEnvironment(ctx context.Context) ([]string, error)
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

	// OptionStart carries the configured start-command *name* the session was
	// started with — never the command line. Mode is derived from that name
	// (contracts/session-mode.md), so a restart that lost it would silently move
	// every remote session back to local, which is a mode change nobody asked
	// for on a session the operator is still driving.
	//
	// It goes on raw, like OptionName and unlike OptionWorkDir: a name is
	// [a-z0-9-] and at most 32 characters (config.validateStartCommandName), so
	// it can carry neither the "|" list-sessions puts between fields nor a
	// newline that would end the row early.
	OptionStart = "@crswd-start"

	// OptionLifetime carries the session's own absolute-lifetime override, and
	// exists because until milestone 15 it did not (spec 009).
	//
	// A session created never to expire was adopted back after a restart with
	// the daemon's default lifetime, because this was the one field of the
	// record that the host did not hold — and on 2026-08-14 four such sessions
	// were destroyed an hour after a redeploy. A switch that works until the
	// next restart is a switch that works until it matters.
	//
	// Three values, and they are the configuration's own vocabulary rather than
	// a fourth spelling of it: "" for unset, `never` for the deadline switched
	// off, and a Go duration otherwise. internal/session parses it; this package
	// carries it, because what a lifetime means belongs to the type that owns
	// the rule.
	//
	// Raw, like OptionName and OptionStart. A duration is digits and unit
	// letters and `never` is five letters, so neither can carry the "|" that
	// separates list-sessions fields nor a newline that would end a row early.
	OptionLifetime = "@crswd-lifetime"

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

	// StartCommand is the configured start-command name read back off the host,
	// and empty for a session started before OptionStart existed. Absence is not
	// an error: it is the correct reading of a session this daemon started under
	// an older build, and every one of those was started with the default.
	StartCommand string

	// Lifetime is OptionLifetime read back verbatim — "", `never`, or a duration
	// string. It is a string rather than a time.Duration on purpose: this
	// package executes tmux and reports what it said, and a parse here would be
	// a second place that decides what a lifetime means.
	//
	// Empty for a session created before OptionLifetime existed, which every
	// caller must read as "unknown" and none as "none".
	Lifetime string
}
