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

	// ActionSettingsView is the read-only settings page served, and it is its own
	// action rather than a second dashboard.view for the reason dashboard.asset
	// is: this is the one page in the product that composes every configured
	// value at render time, and an operator counting who looked at it must not be
	// counting fleet loads with them. The page can only be read — no mutating
	// verb is registered on it — so there is no settings.* write to tell apart
	// from this, and if one is ever added it will need its own name here.
	ActionSettingsView Action = "settings.view"

	// ActionSettingsEdit is one setting written to the operator's configuration
	// file from the settings page.
	//
	// The record names the key and never the value. What was set is the
	// operator's business and may be a path or a bound they would rather not see
	// in a journal that outlives the process; that a setting changed, and which,
	// is what an audit trail is for.
	ActionSettingsEdit Action = "settings.edit"

	// ActionSessionMode is a session's mode changed, or refused, from the
	// dashboard (T019, contracts/session-mode.md).
	//
	// The contract spells it session.mode rather than dashboard.mode, and that is
	// the spelling here: what the record is about is a fact of the session — what
	// it is running — and there is no API operation of this name for an operator
	// to be counting it with. settings.view is already the precedent for a
	// browser-door action named for its subject rather than for its door.
	//
	// It never carries what the session was switched to run. The record says the
	// mode was changed and against which session; which command line either mode
	// names is the operator's configuration, and FR-030 keeps it out of every
	// request and every response — a trail that printed it would be the one copy
	// of it that left the daemon.
	ActionSessionMode Action = "session.mode"

	// ActionDashboardVersion is the running daemon asked what version it is
	// (milestone 6, contracts/version.md). It is its own action for the reason
	// settings.view is: this is the question an operator asks before deciding
	// whether to update, and one asked repeatedly by anything watching the fleet
	// — counting it as a page view would bury both in the dashboard's own traffic.
	//
	// It is dashboard.version rather than version.view because the route is on the
	// dashboard's door and the command-line reader emits nothing at all: there is
	// no second way to ask a *running* daemon this, so the name says which door
	// answered rather than which subject was read.
	ActionDashboardVersion Action = "dashboard.version"

	// ActionDashboardUpdate is this daemon asked to replace its own binary
	// (milestone 6, contracts/self-update.md), whether it did or refused.
	//
	// It is the highest-consequence record in the vocabulary and the only one
	// about the daemon rather than about a session: everything else here changes
	// what is running *under* this process, and this changes the process. An
	// operator auditing a host has one line to grep for the question "when did
	// what is running here last change, and who asked".
	//
	// dashboard.update rather than daemon.update, because the door is what the
	// prefix names throughout this block and there is no second way to ask a
	// running daemon for this. It never carries the version, the asset, or a
	// staging path — the record shape is frozen (FR-016) and none of the three is
	// a field on it; which release was installed is what the version route
	// answers afterwards.
	ActionDashboardUpdate Action = "dashboard.update"

	// ActionDashboardRestart is this daemon asked to end so its service manager
	// starts it again (milestone 9), whether it did or refused.
	//
	// It is its own action and deliberately not a second dashboard.update, which
	// is the tempting reuse: both end with the same call and the same exit, so
	// the daemon does identical work behind them. What differs is the fact an
	// operator is auditing. dashboard.update means the binary at ExecStart is not
	// the one that was there before; dashboard.restart means it is. Collapsing
	// them would leave the trail unable to answer "did what is running here
	// change", which is the one question ActionDashboardUpdate exists for.
	//
	// It carries no version, for the reason none of the records here carries what
	// it delivered: the record shape is frozen (FR-016), and what came back up is
	// what the version route answers afterwards.
	ActionDashboardRestart Action = "dashboard.restart"

	// ActionLoginView is the sign-in form served, and ActionLoginSubmit is one
	// sign-in attempt decided — allow or deny, one record per attempt (M12/T004).
	//
	// They are the only two actions in this vocabulary recorded for a request that
	// reached no layer 1, because the routes behind them are the only two
	// registered in front of one: they exist on a daemon whose door is the
	// dashboard password, and they are what turns knowing that password into the
	// cookie every other record's caller was admitted by. An operator auditing a
	// host on a network reads login.submit the way they read auth.reject on a
	// public one — it is the count that says whether anybody is trying.
	//
	// Two names rather than one, for the reason settings.view and settings.edit
	// are two: asking for the form and answering it are different events, and a
	// scanner that fetched the page a thousand times must not be counted with a
	// thousand guesses at the password.
	//
	// They are login.* rather than dashboard.*, which is the tempting prefix, for
	// the reason session.mode is named for its subject: what the record is about
	// is the door rather than the dashboard behind it, and a daemon behind
	// Cloudflare Access registers neither route at all — so an operator grepping
	// `login\.` is asking a question only one kind of deployment can answer.
	//
	// Neither ever carries password material. The record's shape is frozen
	// (FR-016) and the reason on a refusal is one of login.go's own sentinels, so
	// there is nowhere for the submitted bytes to be attached even by accident.
	ActionLoginView   Action = "login.view"
	ActionLoginSubmit Action = "login.submit"

	// ActionLoginSignOut is one dashboard session ended by the operator holding
	// it (M12/T007) — the cookie login.submit issued, cleared.
	//
	// It is login.* like the two above and for their reason: what it is about is
	// the door rather than the dashboard behind it, and a daemon behind
	// Cloudflare Access registers no route for it at all, so an operator grepping
	// `login\.` reads the whole life of a sign-in on the one kind of deployment
	// that has one. It is deliberately not a third spelling of login.submit,
	// which is the tempting reuse: an operator counting attempts at their
	// password must not be counting departures with them.
	//
	// Unlike the two above, the route behind it is *behind* layer 1 and through
	// the action gate, so the caller on this record is an identity that was
	// verified rather than the `unknown` a login.view carries.
	//
	// It carries no cookie material, for the reason nothing here carries a
	// credential: the record's shape is frozen (FR-016), and what was cleared is
	// not a fact this daemon keeps anywhere.
	ActionLoginSignOut Action = "login.signout"
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
