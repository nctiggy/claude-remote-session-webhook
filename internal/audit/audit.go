// Package audit emits the daemon's audit trail: one JSON object per line on
// standard output, captured by the host service manager (FR-041). There is no
// file mode, no rotation, and no disk to fill — `journalctl --user -u crswd`
// reads it.
//
// The record is a fixed struct with a closed set of string fields, and that is
// the whole design. Sessions run with --dangerously-skip-permissions, so the
// audit trail is the only account of what a request caused; FR-042 forbids
// prompt text, pane content, tokens, and the shared secret from ever appearing
// in one. A sink taking map[string]any or slog.Any("req", r) would make that a
// review item that has to be re-checked on every future call site. A record
// with no field capable of holding arbitrary data cannot leak arbitrary data,
// which turns FR-042 into a property of the type instead.
package audit

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"time"
)

// The emitted JSON keys, exactly the set in data-model.md's AuditRecord table
// and nothing else. slog's own "level" and "msg" are dropped: an audit record
// has one severity and its message is its action.
const (
	keyTime      = "time"
	keyAction    = "action"
	keyCaller    = "caller"
	keySessionID = "session_id"
	keyDecision  = "decision"
	keyReason    = "reason"
	keyRemote    = "remote"
)

// Action names the operation being recorded. The constants are the actions
// milestone 1 knows about; a later route adds its own rather than reusing an
// approximate one, since "what happened" is the question the trail exists to
// answer.
type Action string

const (
	ActionSessionCreate  Action = "session.create"
	ActionSessionPrompt  Action = "session.prompt"
	ActionSessionDestroy Action = "session.destroy"
	ActionAuthReject     Action = "auth.reject"
	ActionReaperDestroy  Action = "reaper.destroy"
	ActionStartupAdopt   Action = "startup.adopt"

	// The three read operations. data-model.md's action column lists the six
	// above as examples and names no action for a read, but FR-041 wants one
	// record for *every* request and contracts/http-api.md defines six routes —
	// so three of them would otherwise have no action to be recorded under.
	// These are named for the contract's own headings ("list", "detail", "read
	// the pane") rather than approximated onto session.create, which is the
	// reuse this type's doc comment warns against.
	ActionSessionList   Action = "session.list"
	ActionSessionDetail Action = "session.detail"
	ActionSessionOutput Action = "session.output"

	// ActionUnknownRoute names a request that matched no route in the contract —
	// a path that does not exist, or a method the path does not answer. It exists
	// so that "one record per request" (FR-041) means every request and not every
	// *routed* request: without it, a scan of the listener produced no trail at
	// all, which is precisely the traffic an incident review would want.
	ActionUnknownRoute Action = "route.unknown"

	// The browser door's four, from data-model.md's AuditRecord additions. The
	// record's shape is frozen — these add actions and nothing else, because a
	// struct that cannot carry arbitrary data cannot leak it.

	// ActionAccessReject is any layer-1 failure: the browser door's own
	// auth.reject, kept apart from it because the two doors fail for unrelated
	// reasons and an operator counting refusals of one must not be counting the
	// other's as well. The specific reason stays server-side (FR-010), and the
	// refused address is never among the things recorded — an allowlist refusal
	// is recorded under a reason authored in this repo, never bytes the request
	// supplied.
	ActionAccessReject Action = "access.reject"

	// ActionDashboardView is a dashboard page served, carrying SessionID when the
	// page is one session's view and taking it from the daemon's own record
	// rather than from the path.
	ActionDashboardView Action = "dashboard.view"

	// ActionDashboardAsset is an embedded static asset served. An asset fetch is
	// a request, FR-016 wants exactly one record for every request, and this
	// type's rule forbids recording traffic under an approximate neighbour — so
	// the alternative to naming it is either a silent request or a page view that
	// was not one.
	ActionDashboardAsset Action = "dashboard.asset"

	// ActionStreamOpen is a stream request decided, allow or deny, emitted when
	// the decision is made rather than when the handler returns (FR-016a). A
	// stream lasts hours; milestone 1's emit-on-return would leave a daemon that
	// died mid-stream with no trace that session output was being read at all.
	// It is the stream's only record — there is deliberately no close record,
	// which would make "exactly one per request" false at this door alone.
	ActionStreamOpen Action = "stream.open"

	// The browser door's four actions, its refusal, and the fleet stream — the
	// vocabulary milestone 3 adds (research R5). They are prefixed rather than
	// flagged: FR-024 wants a browser-initiated change distinguishable from the
	// API's, and a `caller` field on session.create would leave `grep
	// 'dashboard\.'` unable to answer "what did the browser change".

	// ActionDashboardCreate, ActionDashboardDestroy, ActionDashboardRename and
	// ActionDashboardCompact are the four named actions the dashboard can take.
	// dashboard.compact records that /compact was delivered, never that a
	// compaction happened — the daemon cannot see what the assistant is carrying
	// — and, like every record here, carries none of the delivered text (FR-016b).
	ActionDashboardCreate  Action = "dashboard.create"
	ActionDashboardDestroy Action = "dashboard.destroy"
	ActionDashboardRename  Action = "dashboard.rename"
	ActionDashboardCompact Action = "dashboard.compact"

	// ActionDashboardReject is a mutating browser request refused by the
	// cross-site defence, and is deliberately not ActionAccessReject: an identity
	// that passed layer 1 and then failed the cross-site check is a different and
	// far more alarming event than one that never got in, and FR-026 needs an
	// operator to tell an attack from a mistake. Collapsing the two would bury it
	// in the noise of ordinary sign-in failures. Reason names which check failed
	// and stays server-side, since the response itself is uniform (FR-004).
	ActionDashboardReject Action = "dashboard.reject"

	// ActionFleetOpen is one fleet stream opened — one record per open, not per
	// event, for the reason ActionStreamOpen gives: the alternative is a trail
	// whose volume is set by how busy the fleet is rather than by who asked to
	// watch it.
	ActionFleetOpen Action = "fleet.open"
)

// Decision is the allow/deny outcome, and unlike Action it is closed: two
// values, both spelled out in data-model.md. Emit refuses anything else, so a
// record can never be ambiguous about whether the thing it describes happened.
type Decision string

const (
	Allow Decision = "allow"
	Deny  Decision = "deny"
)

// CallerUnknown is the caller of a request that was rejected before an identity
// could be established. Identity is always derived server-side (FR-011, T012);
// a request never names its own caller.
const CallerUnknown = "unknown"

// Record is one audit entry. Every field is a plain string by construction —
// there is deliberately no map, no slice, no any, and no attribute passthrough,
// so there is nowhere for a prompt, a pane capture, a token, or a request body
// to be attached. Adding such a field is the thing FR-042 forbids, and
// audit_test.go fails if one appears.
type Record struct {
	// Action is required.
	Action Action

	// Caller is the server-derived identity. Empty becomes CallerUnknown, so
	// the field is present on every record including a rejection.
	Caller string

	// SessionID is the 32-hex session ID, omitted when the action is not about
	// one particular session.
	SessionID string

	// Decision is required and must be Allow or Deny.
	Decision Decision

	// Reason explains a decision for the operator reading the trail. It is
	// server-authored and stays server-side: the client always sees a uniform
	// message (FR-011). It must never be built from caller-supplied text, a
	// prompt, pane output, a token, or the shared secret (FR-042, FR-043).
	Reason string

	// Remote is the peer address, omitted for actions with no request behind
	// them such as the reaper and startup adoption.
	Remote string
}

// Logger writes records to one sink. Safe for concurrent use: slog's handler
// serialises writes, which is what lets every request path share one Logger.
type Logger struct {
	handler slog.Handler
	now     func() time.Time
}

// New writes the trail to standard output, which is the only destination the
// daemon has (FR-041). It is the form cmd/crswd needs.
func New() *Logger { return NewTo(os.Stdout, time.Now) }

// NewTo is New with the sink and the clock injected, so tests can read back
// exactly what was written and assert on a fixed timestamp. Same seam as
// config.LoadFrom and tmuxctl's SetNow.
func NewTo(w io.Writer, now func() time.Time) *Logger {
	if w == nil {
		w = os.Stdout
	}
	if now == nil {
		now = time.Now
	}
	return &Logger{handler: newHandler(w), now: now}
}

func newHandler(w io.Writer) slog.Handler {
	return slog.NewJSONHandler(w, &slog.HandlerOptions{
		ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {
			if len(groups) > 0 {
				return a
			}
			switch a.Key {
			case slog.TimeKey:
				// UTC at second precision, matching data-model.md's example
				// exactly. A trail read across hosts should not depend on which
				// zone each daemon happened to run in.
				return slog.String(keyTime, a.Value.Time().UTC().Format(time.RFC3339))
			case slog.LevelKey, slog.MessageKey:
				return slog.Attr{}
			}
			return a
		},
	})
}

// Emit writes one record as one line.
//
// The error is returned rather than swallowed on purpose. FR-041 makes the
// record mandatory, so a caller that cannot write one has not completed the
// request it was auditing and should say so — the same ruling config makes for
// the default-root warning. Nothing here retries or falls back to a second
// sink; an unwritable stdout is a broken daemon, not a degraded one.
func (l *Logger) Emit(rec Record) error {
	if rec.Action == "" {
		return errors.New("audit record has no action; refusing to emit")
	}
	if rec.Decision != Allow && rec.Decision != Deny {
		return fmt.Errorf("audit record for %q has decision %q, which is neither %q nor %q; refusing to emit",
			rec.Action, rec.Decision, Allow, Deny)
	}

	caller := rec.Caller
	if caller == "" {
		caller = CallerUnknown
	}

	// Attribute order is the field order of data-model.md's table, so a record
	// read raw in a journal is in the order the spec documents it.
	r := slog.NewRecord(l.now(), slog.LevelInfo, "", 0)
	r.AddAttrs(
		slog.String(keyAction, string(rec.Action)),
		slog.String(keyCaller, caller),
	)
	if rec.SessionID != "" {
		r.AddAttrs(slog.String(keySessionID, rec.SessionID))
	}
	r.AddAttrs(slog.String(keyDecision, string(rec.Decision)))
	if rec.Reason != "" {
		r.AddAttrs(slog.String(keyReason, rec.Reason))
	}
	if rec.Remote != "" {
		r.AddAttrs(slog.String(keyRemote, rec.Remote))
	}

	// The action is named so a failed write is diagnosable; nothing else from
	// the record is, since the error travels back into a handler that may log
	// it in turn.
	if err := l.handler.Handle(context.Background(), r); err != nil {
		return fmt.Errorf("write audit record %q: %w", rec.Action, err)
	}
	return nil
}
