package session

import (
	"cmp"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/nctiggy/claude-remote-session-webhook/internal/auth"
	"github.com/nctiggy/claude-remote-session-webhook/internal/config"
	"github.com/nctiggy/claude-remote-session-webhook/internal/tmuxctl"
)

// AbsoluteLifetime is the one bound every session is measured against
// (FR-038, Principle VI). It is the daemon's built-in default rather than the
// bound itself: the operator configures both the default and its ceiling —
// CRSW_SESSION_LIFETIME and CRSW_SESSION_LIFETIME_MAX, handed over by
// SetLifetimes — and a create may override it for one session under that
// ceiling, refused above it rather than clamped to it (#37).
//
// What bounds the blast radius is therefore the ceiling, not this constant. It is
// measured from CreatedAt and is never renewed, so a relaxed bound is one the
// operator allowed, never one a caller took.
//
// It can be switched off outright (LifetimeDisabled, milestone 13), and switching
// it off takes *two* operator decisions, because a create may only ask for it on
// a daemon whose own ceiling is already unbounded. A caller alone cannot reach it.
//
// There was a second bound here until milestone 15: an idle timeout, measured
// from the last activity. It was withdrawn with constitution 2.0.0 because it
// bounded the wrong thing — a session waiting for a human is quiet, and being
// quiet was never a reason to destroy one. Nothing replaced it, which is why the
// sentence above about the ceiling is now the whole of the story.
const AbsoluteLifetime = 24 * time.Hour

// neverSpan is how far past its origin a bound that has been switched off sits.
//
// A disabled bound is an unreachable deadline rather than a case in every
// comparison. expiredAt, CheckToken and adoption each ask only "is now past this
// instant", and a rule all three had to remember to skip is a rule one of them
// would eventually not — the reaper is where forgetting it destroys a session
// its operator was promised would live. What makes that sound is that the
// instant is genuinely unreachable: a century out, and far enough below
// time.Duration's own ceiling that adding it to any real CreatedAt cannot wrap.
const neverSpan = 100 * 365 * 24 * time.Hour

// tmuxNamePrefix is the daemon's reserved prefix (FR-018). Reconciliation reads
// it back off the host to decide what it owns (FR-021), so the name written and
// the name matched are spelled in exactly one place.
const tmuxNamePrefix = "crswd-"

// State is where a session is in the lifecycle drawn in data-model.md.
type State string

const (
	// StateStarting means the tmux session exists and the Claude command has
	// been sent, with nothing confirmed yet. It accepts prompts and reads.
	StateStarting State = "starting"

	// StateRunning means the session was confirmed present at the last check.
	StateRunning State = "running"

	// StateDead means confirmed gone, or teardown verified. It is terminal:
	// endpoints for a dead session answer exactly as they do for an unknown ID
	// (FR-033), so a record that could leave this state would be a session that
	// came back from a 404.
	StateDead State = "dead"

	// StateFailed means the supervisor tried to bring this session back, ran out
	// of attempts, and stopped (spec 012, FR-018).
	//
	// It is terminal for revival and for nothing else. The record is still owned,
	// still counted against the cap, still reapable, and still readable — because
	// an operator whose session could not be saved needs to be able to look at it
	// and end it, and a state that hid it would be the invisible failure this
	// whole feature exists to remove.
	//
	// It is a separate state rather than StateDead, and the distinction is the
	// point: dead is a session somebody ended, failed is a session the daemon
	// could not save. An operator reading a card must be able to tell those
	// apart, because only one of them is a surprise.
	StateFailed State = "failed"
)

// Valid reports whether s is one of the three states. Anything else is a record
// the store refuses rather than stores, because a session in no state has no
// answer to "does this accept a prompt?".
func (s State) Valid() bool {
	switch s {
	case StateStarting, StateRunning, StateDead, StateFailed:
		return true
	}
	return false
}

// DisplayState is how the dashboard labels a session, derived at render time and
// never stored (FR-019a).
//
// It is a second vocabulary because the first one cannot answer the question an
// operator is actually asking. The daemon writes only StateStarting and
// StateRunning, SetState has no production caller, and a destroyed session is
// deleted rather than marked — so a dashboard that rendered State directly would
// show one label for the whole life of every session, and never the one fact
// worth showing: that this session is minutes from being reaped.
//
// There is no dead member, because a dead session has no record to render — the
// reaper and Destroy both delete (FR-019b). needs-auth keeps its token in the
// design system and arrives with milestone 4's device-code relay; a state
// produced before it can be rendered would be a label nothing knows how to draw.
type DisplayState string

const (
	// DisplayRunning is a live session. StateStarting displays this way too: the
	// distinction lasts one tmux exec, and it is not one an operator watching a
	// fleet could act on.
	//
	// It is the only display state the *lifecycle* — State — produces on its
	// own. There was a second until milestone 15 — a session the idle bound had
	// caught up with, shown so an operator could see it was about to be
	// destroyed — and it went when the bound did. DisplayBlocked and
	// DisplayUnknown below are not a revival of that: they answer a different
	// question, asked of a different input (see Session.DisplayState's own
	// comment), and neither is measured from a clock.
	DisplayRunning DisplayState = "running"

	// DisplayFailed is a session the supervisor could not bring back (spec 012).
	//
	// It is the second display state, and the vocabulary grew for the reason it
	// shrank to one: a state exists here when an operator can act on it. This one
	// they can — a failed session is not coming back on its own, and the choice
	// of destroying it or fixing what broke is theirs.
	//
	// It is deliberately not shown for a session that is merely being retried. A
	// card that flickered between running and failed for three attempts would
	// teach an operator to ignore it, which is the opposite of what a state that
	// only ever appears for a genuine problem is for.
	DisplayFailed DisplayState = "failed"

	// DisplayBlocked is a session whose pane matched a named entry in
	// dialog.go's registry (see DetectDialog) — a TUI dialog crswd did not
	// spawn and cannot answer, sitting where a prompt or the tool's own output
	// would otherwise be. It is the direct fix for the failure PR #150 found:
	// every other observable this daemon has (process up, tmux pane present,
	// `POST /prompt` accepting bytes) says exactly what DisplayRunning says, so
	// without a pane-content check a parked session is indistinguishable from
	// a healthy one. Which dialog is named beside this value rather than
	// folded into it (see cardOf, promptResponse) — this stays one word so it
	// keeps being the pill's own CSS class suffix, exactly as DisplayRunning
	// and DisplayFailed already are.
	DisplayBlocked DisplayState = "blocked"

	// DisplayUnknown is a pane that looks dialog-shaped — it carries one of
	// dialog.go's suspiciousMarkers — but named no signature the registry
	// holds. This is the registry falling behind a dialog nobody has
	// catalogued yet, and it is deliberately its own state rather than a
	// silent fallback to DisplayRunning: a heuristic that goes quiet the
	// moment it does not recognise something is the same bug PR #150 fixed,
	// wearing a signature list instead of a slash command. See dialog.go's
	// package comment for why this list is a heuristic over a TUI crswd does
	// not own, and why that makes "unknown" the fail-closed answer rather than
	// "healthy".
	DisplayUnknown DisplayState = "unknown"
)

// Mode is where a session is driven from: the operator's own dashboard, or
// claude.ai through a remote-control command (FR-026, FR-031).
//
// It is a second derived vocabulary for the reason DisplayState is one, and it
// is derived from the same record rather than stored beside it — see Mode.
type Mode string

const (
	// ModeLocal is a session running any command other than the operator's
	// configured remote-control one. It is what every session started before
	// remote control existed is, and what a daemon configuring no remote-control
	// command runs exclusively.
	ModeLocal Mode = "local"

	// ModeRemote is a session started under the name the operator configured as
	// their remote-control command, and so reachable from claude.ai.
	ModeRemote Mode = "remote"
)

// Session is the daemon's record of one live Claude Code session, held in memory
// for the process lifetime.
//
// Restart recovery is primarily adoption: the daemon reconciles with the tmux
// sessions still on the host (FR-021) rather than trusting anything it wrote
// down. Since spec 012 there is also a journal, and it is deliberately the
// junior partner — it records what *should* be running so that a session whose
// shell the host has lost can be rebuilt, and the host still wins on anything
// that is running now. What the journal holds is in journal.go; it holds no
// credential and no content.
//
// Every value that can be computed from these fields is a method rather than a
// field. Storing a derived value is storing a second copy that is free to drift
// from the first, and the two that would matter most — the tmux target and the
// token expiry — are exactly the two that must not.
type Session struct {
	// ID is 32 lowercase hex characters from NewID, and it is the only thing a
	// tmux target is ever built from (FR-034).
	ID string

	// Owner is the identity that created the session, derived server-side from
	// the credential presented (FR-012, FR-017). It is never read from a
	// request field, and it is what every session-scoped request is authorised
	// against (FR-032).
	Owner auth.CallerID

	// Name is a caller-supplied display label, validated by ValidateName. It is
	// deliberately not part of any tmux target — see TmuxName.
	Name string

	// Lifetime is how long this session may live from CreatedAt (#37). Zero
	// means the daemon's configured default; a negative disables the absolute
	// deadline for this session alone.
	//
	// Negative rather than zero, for one reason: zero already means "the
	// operator said nothing", and one value cannot also mean "the operator said
	// none". What switching it off costs is written at AbsoluteDeadline.
	//
	// **It is durable.** The value here is written onto the tmux session as
	// @crswd-lifetime and read back by Adopt (milestone 15), because until it
	// was, a session created never to expire came back from a restart carrying
	// the daemon's default and was destroyed on the next sweep. A record whose
	// most important field did not survive the thing it was meant to survive was
	// the whole of that defect.
	//
	// It is a duration rather than an instant for the reason TokenExpiry is a
	// method: a stored deadline is a second value that can disagree with the
	// rule that produced it, and this is read through AbsoluteDeadline exactly
	// so there is one expression of it.
	Lifetime time.Duration

	// Width is how many columns this session's window has been reflowed to
	// (#120). Zero means nobody has reflowed it, which is every session that
	// predates milestone 16 and every session since that nobody has narrowed —
	// read it through Columns rather than comparing it against zero here.
	//
	// Zero is kept distinct from "80" deliberately, though both render into the
	// same 80-column window. A reflow takes the window permanently out of tmux's
	// automatic sizing, so this field is also the daemon's only record that the
	// session stopped following a terminal attached on the host, and that is the
	// sentence #120 requires the operator be told before they take one.
	//
	// **It is durable**, written onto the tmux session as @crswd-width and read
	// back by Adopt, for the reason Lifetime is: a restart that lost it would put
	// a record saying 80 in front of a window that is 44, which is the milestone
	// 15 defect wearing a different field.
	Width int

	// StartCommand is the name — never the command line — of the command typed
	// into this session's shell (#38). The name is what a card shows, what the
	// audit trail records, and what a restart resolves again; the line itself is
	// configuration and stays in config, because a record carrying a command
	// line would be a record that could be made to carry any command line.
	//
	// Empty means the daemon's default, which is what every session created
	// before this field existed was started with.
	StartCommand string

	// WorkDir is the canonical, symlink-resolved, allowlist-checked directory
	// from ResolveWorkDir (FR-028). Never the caller's spelling of it.
	WorkDir string

	// TokenHash is SHA-256 of the bearer token handed out once at creation. The
	// plaintext is never stored (FR-013).
	//
	// The json tag is the only one on this struct and it is a guard, not a wire
	// format: Session is a domain type that no handler should be marshalling,
	// but if one ever does, the field FR-042 forbids in any output is the one
	// field that cannot come along.
	TokenHash [32]byte `json:"-"`

	// CreatedAt is the origin of the absolute deadline. For an adopted session
	// it is the tmux session's own start time, not the moment of adoption, so a
	// daemon restart cannot extend a session past the ceiling (FR-024).
	CreatedAt time.Time

	// LastActivity is when a request last drove this session, moved by
	// Store.Touch on the three calls that drive one — Resolve, Compact, SetMode.
	// Reading moves nothing, by construction (docs/auth-and-sessions.md,
	// "watching is not driving").
	//
	// **No deadline is measured from it.** It was half of the idle clock until
	// milestone 15 and is now a fact the interface shows and the daemon acts on
	// nowhere: when this session was last driven, for an operator deciding what
	// to do with it. Nothing here can shorten a session's life.
	LastActivity time.Time

	// State is the lifecycle position above.
	State State

	// Adopted records that this session was reconciled from the host rather
	// than created through the API. It is audit-visible and changes no rule:
	// an adopted session is subject to the same ownership check and the same
	// timeouts as any other (FR-023).
	Adopted bool

	// CredentialPending marks a session whose bearer token has not been handed
	// to anyone yet, which only adoption produces. A create returns its token in
	// the same response that mints it; adoption has no response to put one in,
	// because it happens at startup with nobody asking.
	//
	// While it is set, TokenHash is the hash of a token that was generated and
	// immediately discarded, so no credential a caller can present will match —
	// the session is owned, listed, capped and reapable, and nothing can drive
	// it. ClaimPending is what ends that: it mints the real token, returns it
	// once, and clears this. Minting late rather than at adoption is what keeps
	// FR-013's "never stored" true — a plaintext token held from startup until
	// somebody asked for it would be exactly the storage that forbids.
	CredentialPending bool `json:"-"`

	// ConversationID is the Claude conversation this session is having: a UUID
	// this daemon minted at create and passed as --session-id, and the value it
	// passes to --resume to bring the session back (spec 012, FR-001).
	//
	// The daemon chooses it rather than discovering it afterwards, because the
	// alternatives cannot tell two sessions in one working directory apart —
	// --continue resolves to "most recent", and a display name is silently
	// renamed by the CLI when it collides with a live session.
	//
	// **It is durable in two places, and the journal is the authority.** It is
	// written onto the tmux session as @crswd-conversation, which is the cheap
	// read while that session exists, and into the journal, which is the only
	// copy that outlives it. On 2026-08-22 the kernel OOM killer took a whole
	// tmux-spawn cgroup — Claude, its shell and its tmux session together — and
	// every option on that session went with it. A handle kept only on the
	// running shell is lost in exactly the failure it exists for.
	//
	// Empty for every session created before spec 012. Such a session is
	// supervised like any other and revived by identifier never, because there
	// is no identifier it was ever started with (FR-005).
	ConversationID string

	// ReviveAttempts is how many consecutive revivals have failed since the last
	// success, and it is what stops this daemon becoming the thing it is meant to
	// fix (FR-016).
	//
	// It is durable for one reason: a count that lived only in memory would be
	// reset by every daemon restart, and a supervisor whose bound resets is a
	// supervisor with no bound. On 2026-08-17 a unit on this host restarted 2,826
	// times in four hours against an error no retry could fix.
	ReviveAttempts int

	// NextReviveAt is the earliest instant the next attempt may be made, so
	// consecutive attempts are spaced further and further apart (FR-017).
	//
	// Zero means "now", which is what a session that has never failed carries.
	// It is written *before* an attempt rather than after, so a daemon that dies
	// mid-revival comes back backing off rather than retrying instantly.
	NextReviveAt time.Time
}

// TmuxName is the host session name this record addresses.
//
// It derives from the ID alone. That is the whole of FR-034: there is no path
// from a caller-supplied string to a tmux target, so a hostile Name cannot
// address a window no matter what a future handler does with it.
func (s Session) TmuxName() string { return tmuxNamePrefix + s.ID }

// SessionTarget addresses the session itself — has-session, kill-session.
func (s Session) SessionTarget() string { return tmuxctl.SessionTarget(s.TmuxName()) }

// PaneTarget addresses the session's active pane — send-keys, capture-pane,
// paste-buffer, set-option. Different syntax from SessionTarget, which is why
// tmuxctl offers two helpers and this offers two methods (research D2).
func (s Session) PaneTarget() string { return tmuxctl.PaneTarget(s.TmuxName()) }

// AbsoluteDeadline is when the session dies regardless of use (FR-038).
//
// A zero Lifetime means AbsoluteLifetime, so a record written before this field
// existed — and every test that does not care — carries the deadline it always
// did. A *negative* Lifetime is the operator switching this bound off, and it is
// worth being plain about what that is rather than presenting it as a knob: this
// is the one deadline that is never renewed, so removing it removes the bound
// Principle VI actually rests on. What is left containing the session is
// allowed_roots and the doors, not the reaper.
//
// It is a decision the operator makes twice. resolveLifetimes grants it only on
// a daemon whose configured ceiling is itself unbounded, so a create cannot
// reach it on a host that did not already say so — which is the difference
// between a bound the operator relaxed and one a caller took (#37).
//
// TokenExpiry follows it, as it follows every other value of this deadline, so
// such a session's bearer token does not expire either. That is FR-015's "equal
// by construction" working rather than failing: a token that outlived its
// session would be a credential for nothing, and one that died first would leave
// a live unsandboxed session its owner cannot destroy.
func (s Session) AbsoluteDeadline() time.Time {
	if s.LifetimeDisabled() {
		return s.CreatedAt.Add(neverSpan)
	}
	return s.CreatedAt.Add(orDefault(s.Lifetime, AbsoluteLifetime))
}

// LifetimeDisabled reports that the absolute deadline is off for this session,
// which is what a negative Lifetime spells (milestone 13).
//
// It exists so that "negative means off" has one expression. The dashboard has to
// know — a card must say there is no lifetime limit rather than render the
// century-out instant AbsoluteDeadline returns for such a session — and a caller
// comparing the duration against zero itself would be a second reading of the
// rule, free to disagree with this one the day the spelling changes.
//
// And this is now the *only* bound, so its absence is worth saying out loud
// wherever it is asked about. A method named for the fact makes that possible;
// a `< 0` at each call site does not.
func (s Session) LifetimeDisabled() bool { return s.Lifetime < 0 }

// Columns is how wide this session's window is (#120).
//
// Zero means nobody has reflowed it, and what such a session is, is tmux's own
// width for a window no client has attached — the same zero-means-inherited rule
// AbsoluteDeadline applies to Lifetime, expressed once here so that a caller
// wanting only the number does not have to know the rule.
func (s Session) Columns() int {
	if s.Width == 0 {
		return config.DefaultPaneWidth
	}
	return s.Width
}

// encodeWidth renders a session's width for the host to hold
// (tmuxctl.OptionWidth).
//
// Empty for a session nobody has reflowed, so that never-set and set-to-nothing
// read back the same — encodeLifetime's shape, minus its word, because a width
// has no `never` to spell.
//
// Clamped on the way out as well as on the way in. This is the value a restart
// will believe, and a width the operator could not have asked for has no business
// surviving one.
func encodeWidth(cols int) string {
	if cols == 0 {
		return ""
	}
	return strconv.Itoa(config.ClampPaneWidth(cols))
}

// decodeWidth reads back what encodeWidth wrote.
//
// Absent is zero rather than the default width spelled out, because the two say
// different things: "80 because nothing has touched this window" and "80 because
// somebody reflowed it to 80" differ by whether tmux is still sizing it
// automatically, which is the thing the operator has to be told. Columns
// collapses them again for anything that only wants the number.
//
// **Nothing here fails**, for decodeLifetime's reason and #120's: a width is
// advisory, so an unreadable one is defaulted by config.ParsePaneWidth rather
// than costing the session its adoption.
func decodeWidth(raw string) int {
	if strings.TrimSpace(raw) == "" {
		return 0
	}
	return config.ParsePaneWidth(raw)
}

// neverLifetime is the negative this package writes when a lifetime has been
// switched off. Any negative reads as off (LifetimeDisabled); this is the one
// that gets written, so that a value making a round trip through the host comes
// back spelled the way it left.
const neverLifetime = -1 * time.Nanosecond

// encodeLifetime renders a session's own lifetime for the host to hold
// (tmuxctl.OptionLifetime, milestone 15).
//
// Three values, in the configuration's own vocabulary rather than a fourth
// spelling of it: empty for unset, `never` for the deadline switched off, and a
// Go duration otherwise. `never` rather than a negative number because a
// negative duration written to a tmux option would be a value an operator
// reading `tmux show-options` could not interpret, and because config already
// took that word for this meaning.
func encodeLifetime(d time.Duration) string {
	switch {
	case d == 0:
		return ""
	case d < 0:
		return config.NeverLifetime
	default:
		return d.String()
	}
}

// decodeLifetime reads back what encodeLifetime wrote.
//
// **Nothing here fails.** Every unreadable value — a truncated duration, a word
// from a future build, a positive number with no unit — decodes to zero, which
// means "unset" and hands the session the daemon's configured default. That is
// FR-010: the alternative to a defaulted lifetime is a session left unadopted,
// and an unadopted session is an unowned unsandboxed shell. The daemon has no
// business preferring the second.
//
// A negative duration string is read as unset rather than as the switch. The
// switch has one spelling and it is a word; accepting `-1ns` as well would be a
// second way to reach the only bound Principle VI still rests on, arriving from
// a string on the host rather than from the two operator decisions that grant it.
func decodeLifetime(raw string) time.Duration {
	if raw == "" {
		return 0
	}
	if strings.EqualFold(raw, config.NeverLifetime) {
		return neverLifetime
	}
	d, err := time.ParseDuration(raw)
	if err != nil || d <= 0 {
		return 0
	}
	return d
}

// orDefault is the zero-means-inherited rule, in one place so that what an unset
// field means is settled once.
func orDefault(d, fallback time.Duration) time.Duration {
	if d == 0 {
		return fallback
	}
	return d
}

// DisplayState is the label the dashboard shows for this session (FR-019b).
//
// It answers running for every session it is asked about, and the parameter it
// ignores is kept deliberately. Until milestone 15 this compared the clock
// against the idle deadline and could answer idle; with that bound withdrawn,
// the only way a live session ends is the operator destroying it or the ceiling
// passing, and a record in either of those states is not one a card is being
// rendered for — the reaper drops it.
//
// The signature keeps its clock rather than becoming a constant because the
// question "what does this session look like *now*" is the right question for a
// fleet to ask, and a future state that does depend on the clock — a session
// approaching its ceiling, say — would otherwise have to change every caller
// back. FR-019c's rule is unchanged and now trivially true: the dashboard and
// the sweep still read one clock, because the sweep is the only one left reading
// one at all.
//
// State is not consulted at all. Reading it is what FR-019a forbids, and both
// values it can hold in production are this method's running anyway.
//
// It answers from the record alone and never from a pane. DisplayBlocked and
// DisplayUnknown are produced by DetectDialog against pane text this method is
// never given — a caller who has captured a pane composes the two answers
// itself (see internal/httpapi's effectiveDisplayState) rather than this
// method reaching for tmux, which would turn a pure, in-memory read into one
// that can fail, block, or disagree with a capture the caller already made for
// its own reasons.
func (s Session) DisplayState(_ time.Time) DisplayState {
	if s.State == StateFailed {
		return DisplayFailed
	}
	return DisplayRunning
}

// Mode is which mode this session is in, derived from StartCommand and stored
// nowhere (research R5, FR-031).
//
// remoteCommand is the operator's configured remote-control command *name* —
// config.Config.RemoteControlCommand, which the loader has already resolved
// against the configured set, so a name that reaches here is one this daemon
// runs. Empty means the daemon configures no remote control at all, and a daemon
// with nothing to switch on has no remote sessions to report.
//
// It is a parameter for the reason DisplayState takes a clock: which name means
// remote is startup configuration and not a property of a record, so passing it
// keeps one answer in one place. A record carrying its own copy would be the
// second source of truth research R5 rejected — free to disagree with the
// operator's configuration after a rename, a restart, or a toggle that half
// succeeded. There is deliberately no Mode field and no RemoteControl bool: the
// name that determines the mode is the one thing persisted (@crswd-start), and
// carrying it is the whole of FR-031.
//
// A name is never a command line, in either direction (FR-030). This compares
// two configured names and can reach nothing else, which is what lets the mode a
// browser reads and the mode a browser sets be the same word.
//
// An empty StartCommand is DefaultStartCommandName, exactly as
// config.StartCommands.Command reads one — a session created naming no command
// runs the default. So an operator whose remote-control command *is* the default
// gets ModeRemote for it, rather than a mode that depends on whether the create
// happened to spell the name it resolved to.
func (s Session) Mode(remoteCommand string) Mode {
	// Stated rather than left to fall out of the comparison below: no configured
	// remote command means nothing this daemon runs is remote, and that is the
	// case worth being unable to get wrong.
	if remoteCommand == "" {
		return ModeLocal
	}

	name := s.StartCommand
	if name == "" {
		name = config.DefaultStartCommandName
	}
	if name == remoteCommand {
		return ModeRemote
	}
	return ModeLocal
}

// TokenExpiry is when the bearer token stops being accepted (FR-015).
//
// It returns AbsoluteDeadline rather than computing the same sum a second time,
// and it is a method rather than a field, because the operator's ruling is that
// a credential can neither outlive nor under-live the session it belongs to. A
// stored TokenExpiry could disagree with the deadline; an independently
// computed one could be edited to disagree. Delegation makes "equal by
// construction" mean what it says: there is one expression, and shortening the
// token's life is not possible without shortening the session's.
func (s Session) TokenExpiry() time.Time { return s.AbsoluteDeadline() }

var (
	// ErrSessionNotFound is returned for an unknown ID *and* for a session
	// owned by someone else, deliberately indistinguishably (FR-033). A handler
	// that cannot tell the two apart cannot leak the difference, which is what
	// keeps session IDs unenumerable without every call site remembering to be
	// careful.
	ErrSessionNotFound = errors.New("session not found")

	// ErrSessionExists guards the one-record-per-ID invariant. Reaching it
	// means either a repeat adoption or a collision in NewID, and both want a
	// loud failure rather than a silently replaced record — the record being
	// overwritten holds the token hash its owner is holding the token for.
	ErrSessionExists = errors.New("session already exists")

	// ErrTooManySessions refuses a create that would put the daemon over
	// CRSW_MAX_SESSIONS (FR-036). It is the one refusal in this package that is
	// about the host rather than about the request: nothing the caller sent is
	// wrong, there is simply no room, which is why the handler answers 429 and
	// not 400.
	ErrTooManySessions = errors.New("the concurrent-session cap is reached")

	// ErrSessionDead marks an attempt to move a terminal record, and — from
	// Manager.Output — a session the host has just confirmed is no longer there
	// (#21). One sentinel for both, because it is one fact to every caller:
	// dead is the end of the state machine in data-model.md, and a dead session
	// is answered exactly as an id nobody was ever issued is.
	ErrSessionDead = errors.New("session is dead")

	// ErrRestartInFlight refuses a second restart of a session whose process is
	// already being restarted (spec 013, FR-012).
	//
	// It is a refusal about timing rather than about the request: nothing the
	// caller sent is wrong, and trying again in a moment is the right response —
	// which is why it is its own sentinel rather than folded into ErrSessionDead.
	ErrRestartInFlight = errors.New("a restart of this session is already in flight")

	// ErrUnknownStartCommand is a create naming a start command the operator did
	// not configure. It is refused rather than falling back to the default: a
	// caller that asked for remote control and silently got a plain session has
	// no way to discover that is what happened (#38).
	ErrUnknownStartCommand = errors.New("no such start command")

	// ErrInvalidLifetime is a per-session lifetime override the daemon will not
	// grant: past the operator's ceiling, or asking for a session that never
	// expires on a daemon whose ceiling still stands (#37).
	ErrInvalidLifetime = errors.New("invalid session lifetime")

	// ErrCredentialNotPending marks a claim on a session whose credential has
	// already been handed out. It is not reachable through the API — ClaimPending
	// only ever asks about sessions it has just seen are pending — and exists so
	// that a future caller reaching SetCredential twice fails loudly rather than
	// silently invalidating a token somebody is using.
	ErrCredentialNotPending = errors.New("session credential is not pending")

	// ErrInvalidSession wraps every malformed-record rejection from Add.
	ErrInvalidSession = errors.New("invalid session record")

	// ErrInvalidState marks a state value outside the three.
	ErrInvalidState = errors.New("invalid session state")
)

// Store is the in-memory set of session records, safe for concurrent use.
//
// It holds Sessions by value and hands out copies. A store of pointers would let
// a handler mutate a live record without the lock, and the reaper reads every
// record on its own goroutine — so the race would be real, and it would be over
// the fields the absolute deadline derives from. Every mutation goes
// through a method here, under the lock, or it does not happen.
type Store struct {
	mu   sync.RWMutex
	byID map[string]Session
}

// NewStore returns an empty store.
func NewStore() *Store {
	return &Store{byID: make(map[string]Session)}
}

// Add records a new session, refusing a malformed record or a duplicate ID.
//
// The validation is not defensive politeness. A record with no owner would pass
// an ownership check against the zero CallerID; one with a zero CreatedAt would
// carry a deadline in the year 1. Both fail closed, and neither should be
// reachable — so the store refuses them here rather than letting a caller
// discover which way they fail.
//
// It is deliberately uncapped, and adoption is its only caller. FR-036 caps
// *creation*; a session the host is already running has to be taken back
// whatever the count says, because refusing it there would leave a live
// unsandboxed shell with no owner, no deadline and no reaper — which is the one
// outcome Principle VI ranks above being over the cap. An adopted record still
// counts against every later create, since AddCapped counts records and not
// creates.
func (st *Store) Add(s Session) error {
	if err := validate(s); err != nil {
		return err
	}

	st.mu.Lock()
	defer st.mu.Unlock()

	return st.addLocked(s)
}

// AddCapped records a new session unless the store already holds limit of them
// (FR-036). It is what Manager.Create adds through.
//
// The count and the insert are one critical section, and that is the whole
// reason this exists rather than a Len check in the manager: two creates racing
// at the boundary would both read limit-1, both find room, and both insert. No
// caller can close that window, because it is between the two calls rather than
// inside either — so the check belongs where the lock already is.
//
// A limit below 1 refuses everything. config.Load makes a cap under 1 fatal and
// NewManagerWithClock refuses one as well, so this is the third answer to a
// question that should never be asked, and it is the fail-closed one: a daemon
// that does not know how many sessions it may run does not get to run any.
func (st *Store) AddCapped(s Session, limit int) error {
	if err := validate(s); err != nil {
		return err
	}

	st.mu.Lock()
	defer st.mu.Unlock()

	if len(st.byID) >= limit {
		return fmt.Errorf("add session: %w", ErrTooManySessions)
	}
	return st.addLocked(s)
}

// addLocked is the insert both add paths end in. The caller holds the write
// lock, which is what lets AddCapped count and insert without a gap.
func (st *Store) addLocked(s Session) error {
	if _, ok := st.byID[s.ID]; ok {
		return fmt.Errorf("add session: %w", ErrSessionExists)
	}
	st.byID[s.ID] = s
	return nil
}

// validate holds the record invariants Add enforces, named one per reason so a
// rejection says which invariant it was.
func validate(s Session) error {
	switch {
	case s.ID == "":
		return fmt.Errorf("%w: an id is required", ErrInvalidSession)
	case s.Owner == "":
		return fmt.Errorf("%w: an owner is required", ErrInvalidSession)
	case !s.State.Valid():
		return fmt.Errorf("%w: %w", ErrInvalidSession, ErrInvalidState)
	case s.CreatedAt.IsZero():
		return fmt.Errorf("%w: a creation time is required", ErrInvalidSession)
	case s.LastActivity.IsZero():
		return fmt.Errorf("%w: a last-activity time is required", ErrInvalidSession)
	}
	return nil
}

// Get returns the caller's own session, or ErrSessionNotFound.
//
// Ownership is a parameter and not something a caller may skip, so "unknown ID"
// and "someone else's ID" are one answer from one lookup (FR-032, FR-033). The
// alternative — an owner-blind Get plus a comparison at each call site — makes
// cross-session isolation a thing every future handler has to remember, and the
// isolation rule in docs/auth-and-sessions.md is the one rule this project
// cannot afford to enforce by remembering.
//
// Reconciliation and the reaper act on the daemon's own behalf and so have no
// caller to check against. They will want an owner-blind lookup of their own —
// unexported, so that everything reached from a request still comes through
// here.
//
// The returned Session is a copy. Mutating it changes nothing in the store.
func (st *Store) Get(id string, owner auth.CallerID) (Session, error) {
	st.mu.RLock()
	defer st.mu.RUnlock()

	s, ok := st.byID[id]
	// An empty owner matches nothing: Add refuses a record without one, so the
	// only way to ask this question is to have skipped authentication.
	if !ok || owner == "" || s.Owner != owner {
		return Session{}, ErrSessionNotFound
	}
	return s, nil
}

// lookup is the owner-blind read Get's comment promises reconciliation and the
// reaper: both act on the daemon's own behalf, and neither has a caller to check
// a record against.
//
// It is unexported so that this stays true by construction rather than by
// habit. Everything reached from a request goes through Get, which takes an
// owner and cannot be called without one — a caller-facing path that wanted this
// instead would have to be written inside this package, where the reason it may
// not is written down.
func (st *Store) lookup(id string) (Session, bool) {
	st.mu.RLock()
	defer st.mu.RUnlock()

	s, ok := st.byID[id]
	return s, ok
}

// snapshot is every record the store holds, as copies, in no particular order.
//
// It is the owner-blind read lookup's comment promises the reaper, and it is
// unexported for the same reason: the reaper acts on the daemon's own behalf and
// has no caller to check a record against, while everything reached from a
// request goes through Get, which cannot be called without an owner.
//
// A copy rather than a callback under the lock, deliberately. Reaping is several
// tmux commands per session, and a store held locked for the length of one would
// stall every request behind a host that is slow to answer. The cost is that a
// record can be deleted between the copy and its teardown — which is the destroy
// racing the reaper that spec.md names, and Manager.Destroy already ends in
// success for both of them.
//
// Order is unspecified because nothing about a sweep depends on it: each record
// is judged against its own two deadlines and nothing else's.
func (st *Store) snapshot() []Session {
	st.mu.RLock()
	defer st.mu.RUnlock()

	out := make([]Session, 0, len(st.byID))
	for _, s := range st.byID {
		out = append(out, s)
	}
	return out
}

// List returns the caller's own sessions, oldest first, as copies.
//
// Ordering is by CreatedAt then ID because map iteration is randomised, and a
// list endpoint whose order changes between identical requests is one no client
// can page or diff.
func (st *Store) List(owner auth.CallerID) []Session {
	if owner == "" {
		return nil
	}

	st.mu.RLock()
	defer st.mu.RUnlock()

	out := make([]Session, 0, len(st.byID))
	for _, s := range st.byID {
		if s.Owner == owner {
			out = append(out, s)
		}
	}
	slices.SortFunc(out, func(a, b Session) int {
		if c := a.CreatedAt.Compare(b.CreatedAt); c != 0 {
			return c
		}
		return cmp.Compare(a.ID, b.ID)
	})
	return out
}

// Len is the number of records held, whatever their state and owner — the same
// count AddCapped enforces the cap on (FR-036).
//
// It is a read and nothing more. The cap itself is not enforced through it, for
// the reason AddCapped exists: a caller that read this and then added would have
// a window between the two where another create could fit.
func (st *Store) Len() int {
	st.mu.RLock()
	defer st.mu.RUnlock()

	return len(st.byID)
}

// SetState moves a record through the lifecycle, refusing to revive a dead one.
//
// Dead is terminal by design, not by convention: a record that could leave it
// would be a session whose endpoints stopped answering 404 and started answering
// again, for an ID whose tmux session is already gone.
func (st *Store) SetState(id string, next State) error {
	if !next.Valid() {
		return fmt.Errorf("set state %q: %w", next, ErrInvalidState)
	}

	st.mu.Lock()
	defer st.mu.Unlock()

	s, ok := st.byID[id]
	if !ok {
		return fmt.Errorf("set state: %w", ErrSessionNotFound)
	}
	if s.State == StateDead && next != StateDead {
		return fmt.Errorf("set state: %w", ErrSessionDead)
	}
	// Failed is one-way too, with one exit: destroying it. A supervisor that
	// could move a session back out of failed would be a supervisor whose bound
	// is advisory, and the bound is the whole defence against a retry loop.
	if s.State == StateFailed && next != StateFailed && next != StateDead {
		return fmt.Errorf("set state: %w", ErrSessionDead)
	}
	s.State = next
	st.byID[id] = s
	return nil
}

// Touch records that a request drove this live session.
//
// It only ever moves forward. A reading behind the one already recorded is
// dropped rather than stored, so a lagging clock read cannot make a session look
// staler than it is.
//
// **It feeds no deadline.** Until milestone 15 this moved the clock the reaper
// judged idleness on, and a call missing from a driving path was a session
// destroyed while in use. Now it records a fact for the operator to read and
// nothing acts on it, so the cost of an omission here is a card that understates
// how recently a session was driven.
//
// A dead session is still refused: advancing anything on a record the reaper
// should be collecting would keep it.
func (st *Store) Touch(id string, now time.Time) error {
	st.mu.Lock()
	defer st.mu.Unlock()

	s, ok := st.byID[id]
	if !ok {
		return fmt.Errorf("touch session: %w", ErrSessionNotFound)
	}
	if s.State == StateDead {
		return fmt.Errorf("touch session: %w", ErrSessionDead)
	}
	if now.After(s.LastActivity) {
		s.LastActivity = now
		st.byID[id] = s
	}
	return nil
}

// SetCredential replaces a record's token hash and clears CredentialPending. It
// is how an adopted session acquires the one credential it will ever have.
//
// It refuses a session that is not pending, so a second claim cannot rotate a
// credential that has already been handed out — that would let anyone able to
// call the route invalidate a token another client is holding, and would make
// "returned once" (FR-013) mean "returned once per caller who asks".
func (st *Store) SetCredential(id string, hash [32]byte) error {
	st.mu.Lock()
	defer st.mu.Unlock()

	s, ok := st.byID[id]
	if !ok {
		return fmt.Errorf("set credential: %w", ErrSessionNotFound)
	}
	if s.State == StateDead {
		return fmt.Errorf("set credential: %w", ErrSessionDead)
	}
	if !s.CredentialPending {
		return fmt.Errorf("set credential: %w", ErrCredentialNotPending)
	}
	s.TokenHash = hash
	s.CredentialPending = false
	st.byID[id] = s
	return nil
}

// SetName replaces a record's display label (FR-015). It is the only writer of
// Name after Add, and it writes no other field.
//
// What it cannot reach is the point rather than an omission. TmuxName derives
// from the ID, and there is no parameter here that could carry a new one — so
// "a rename does not touch tmux" is a property of this signature rather than of
// its body, and stays true however the body is later edited.
//
// The name is not validated here. ValidateName runs in Manager.Rename, which is
// the same call Create makes, so there is one check rather than two free to come
// to disagree about what a name may be.
//
// A dead session is refused, as Touch and SetCredential refuse one: its record is
// waiting to be collected, and a new label on it would be a card the dashboard is
// about to stop drawing under a name nobody has seen before.
func (st *Store) SetName(id, name string) error {
	st.mu.Lock()
	defer st.mu.Unlock()

	s, ok := st.byID[id]
	if !ok {
		return fmt.Errorf("set name: %w", ErrSessionNotFound)
	}
	if s.State == StateDead {
		return fmt.Errorf("set name: %w", ErrSessionDead)
	}
	s.Name = name
	st.byID[id] = s
	return nil
}

// SetWidth records the width a reflow left this session's window at (#120), and
// writes no other field.
//
// The value is the one Manager.Reflow sent to tmux, already clamped, and this
// deliberately does not clamp it again: the record must say what the window was
// actually made, and a second clamp here could only make the two disagree.
//
// A dead session is refused, as SetName and Touch refuse one: its record is
// waiting to be collected, and a width written onto it would describe a window
// that is already gone.
func (st *Store) SetWidth(id string, cols int) error {
	st.mu.Lock()
	defer st.mu.Unlock()

	s, ok := st.byID[id]
	if !ok {
		return fmt.Errorf("set width: %w", ErrSessionNotFound)
	}
	if s.State == StateDead {
		return fmt.Errorf("set width: %w", ErrSessionDead)
	}
	s.Width = cols
	st.byID[id] = s
	return nil
}

// SetStartCommand replaces the configured start-command *name* a record carries,
// and writes no other field. It is what a mode change moves (FR-031), and the
// only writer of the field after Add.
//
// What it cannot reach is the point, as it is in SetName. There is no mode
// parameter here and no mode field to write, so "the mode is derived" stays a
// property of the store rather than a convention the next writer has to observe —
// and the name and the mode cannot come to disagree, because there is only one of
// them.
//
// The name is not validated here. Manager.SetMode resolves it through
// resolveStartCommand before anything is sent, which is the same check Create
// makes, so there is one place deciding what a configured name is rather than two
// free to differ.
//
// A dead session is refused, as SetName and Touch refuse one: its record is
// waiting to be collected, and a mode written onto it would be a card the
// dashboard is about to stop drawing, described by a command nothing is running.
func (st *Store) SetStartCommand(id, name string) error {
	st.mu.Lock()
	defer st.mu.Unlock()

	s, ok := st.byID[id]
	if !ok {
		return fmt.Errorf("set start command: %w", ErrSessionNotFound)
	}
	if s.State == StateDead {
		return fmt.Errorf("set start command: %w", ErrSessionDead)
	}
	s.StartCommand = name
	st.byID[id] = s
	return nil
}

// SetConversation records which Claude conversation this session is having
// (spec 012, FR-001).
//
// Written once at create and again by adoption, never by a caller: a request
// that could choose which conversation a session resumes would be a request that
// could read somebody else's work.
func (st *Store) SetConversation(id, conversationID string) error {
	st.mu.Lock()
	defer st.mu.Unlock()

	s, ok := st.byID[id]
	if !ok {
		return fmt.Errorf("set conversation: %w", ErrSessionNotFound)
	}
	if s.State == StateDead {
		return fmt.Errorf("set conversation: %w", ErrSessionDead)
	}
	s.ConversationID = conversationID
	st.byID[id] = s
	return nil
}

// SetRevive records an attempt against a session's give-up bound (spec 012,
// FR-016, FR-017, FR-020).
//
// Both fields move together because they are one fact — "this is attempt N and
// the next may not be before T" — and a caller able to move one without the
// other could produce a session that backs off forever or one that never does.
//
// Unlike the setters above it is allowed on a failed session, because zeroing
// the count is how a success clears one, and it is refused on a dead one for the
// reason all of them are.
func (st *Store) SetRevive(id string, attempts int, next time.Time) error {
	st.mu.Lock()
	defer st.mu.Unlock()

	s, ok := st.byID[id]
	if !ok {
		return fmt.Errorf("set revive state: %w", ErrSessionNotFound)
	}
	if s.State == StateDead {
		return fmt.Errorf("set revive state: %w", ErrSessionDead)
	}
	s.ReviveAttempts = attempts
	s.NextReviveAt = next
	st.byID[id] = s
	return nil
}

// Delete drops a record and its token hash (FR-020).
//
// The record is overwritten before it is removed so the hash bytes do not sit in
// the map's bucket until that memory is reused. Go offers nothing stronger for a
// value held in a map, and this is honest about being best effort rather than a
// guarantee — the guarantee FR-013 does make is that the plaintext was never
// here at all.
func (st *Store) Delete(id string) error {
	st.mu.Lock()
	defer st.mu.Unlock()

	if _, ok := st.byID[id]; !ok {
		return fmt.Errorf("delete session: %w", ErrSessionNotFound)
	}
	st.byID[id] = Session{}
	delete(st.byID, id)
	return nil
}
