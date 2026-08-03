package session

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/nctiggy/claude-remote-session-webhook/internal/auth"
	"github.com/nctiggy/claude-remote-session-webhook/internal/config"
	"github.com/nctiggy/claude-remote-session-webhook/internal/tmuxctl"
)

// claudeStartCommand is typed into the fresh session's shell rather than being
// the thing tmux runs (FR-018, research D3).
//
// The distinction is the whole reason this constant exists instead of a workDir
// argument to new-session: if Claude were the session's command, a crash would
// take the tmux session with it and the scrollback that says why. A shell that
// was handed a command line keeps the window — and keeps a prompt for
// milestone 4's device-code relay to type into.
//
// It is a constant, so it never travels through Paste. Paste exists because
// tmux's parser eats a trailing unescaped ";" from caller text (research D4);
// this string is daemon-authored and contains no ";" at all.
const claudeStartCommand = "claude --dangerously-skip-permissions"

// enterKey is tmux's name for the Return key. SendKeys is called without -l so
// that this is looked up as a key rather than sent as five characters, which is
// also why the command above rides in the same call: tmux consumes the
// arguments in order, so one exec both types the line and runs it.
const enterKey = "Enter"

// Clock is the daemon's view of time, injectable so that the deadlines derived
// from a record are exact in a test rather than approximately now.
//
// It is declared here rather than imported from internal/auth even though the
// shape is identical. The two measure different things — auth's clock decides
// whether a signature is inside its 300s window, this one decides when a
// session dies — and a package that had to import the other's clock would make
// them one setting. Go's interfaces are structural, so a test that already has
// a stopped clock satisfies both without either package knowing.
type Clock interface {
	Now() time.Time
}

// systemClock is the host clock, chosen in exactly one place (NewManager) so
// that no other constructor can reach for time.Now directly.
type systemClock struct{}

func (systemClock) Now() time.Time { return time.Now() }

var (
	// ErrMissingOwner refuses a create with no authenticated identity behind it.
	// Owner comes from the credential, server-side (FR-012, FR-017), so an empty
	// one means the call skipped authentication rather than that the caller
	// omitted a field — and a session nobody owns is a session no ownership
	// check can refuse.
	ErrMissingOwner = errors.New("a session owner is required")

	// ErrOrphanedSession is the loud answer to a teardown that could not be
	// confirmed (Principle VI): a create that failed *after* tmux had already
	// started a shell, or a Destroy whose session was still there — or could not
	// be asked about — afterwards. It is one sentinel for both because the fact
	// that matters is the same one, and it is the fact the audit trail carries: a
	// live unsandboxed session may exist on the host. Create answers 500 and
	// Destroy answers 409 (FR-019), and the record is kept either way — see
	// rollback and Destroy.
	ErrOrphanedSession = errors.New("the tmux session could not be confirmed gone and may still be running")

	// ErrEmptyPrompt refuses a prompt with no text in it. The contract makes
	// text required and non-empty, and the refusal lives here rather than in the
	// handler for the reason ValidateName does: the rule about what may reach a
	// session belongs to the package that reaches it. An empty prompt would
	// paste nothing and then press Return, which is a bare newline typed into
	// Claude — an action the caller did not ask for and cannot take back.
	ErrEmptyPrompt = errors.New("prompt text is required")
)

// Manager owns the mapping between session records and the tmux sessions they
// name. It is safe for concurrent use: every field is read-only after
// construction and the store and controller are both concurrency-safe.
type Manager struct {
	tmux  tmuxctl.Controller
	store *Store
	roots []config.ApprovedRoot
	clock Clock

	// maxSessions is CRSW_MAX_SESSIONS, read once at construction. It is held
	// here rather than in the store because it is configuration and the store is
	// not configured — but it is *enforced* in the store, under the lock that
	// makes counting and inserting one act (see Store.AddCapped).
	maxSessions int
}

// NewManager builds a Manager on the host clock. This is the constructor the
// daemon uses; tests reach for NewManagerWithClock.
func NewManager(tmux tmuxctl.Controller, store *Store, roots []config.ApprovedRoot, maxSessions int) (*Manager, error) {
	return NewManagerWithClock(tmux, store, roots, maxSessions, systemClock{})
}

// NewManagerWithClock fails closed on anything that would let a session start
// without the constraints standing in for the permission prompt.
//
// An empty root list is the case worth naming: ResolveWorkDir already refuses
// every path when there are no roots, so a Manager built without them would
// merely be useless rather than dangerous. It is still refused here, because
// "no directory is approved" reaching a request as a 400 per create is a
// misconfiguration discovered by the caller instead of at startup, and
// config.Load's contract is that a Config is ready to use as-is.
//
// A cap under 1 is refused for the same reason and a sharper one: it is either a
// Config that never went through Load or a zero value nobody set, and both would
// give a Manager whose only correct behaviour is to refuse every create. A
// daemon that cannot say how many unsandboxed sessions it may run must not
// start (Principle VI).
func NewManagerWithClock(tmux tmuxctl.Controller, store *Store, roots []config.ApprovedRoot, maxSessions int, clock Clock) (*Manager, error) {
	switch {
	case tmux == nil:
		return nil, errors.New("session: no tmux controller provided; refusing to start")
	case store == nil:
		return nil, errors.New("session: no session store provided; refusing to start")
	case clock == nil:
		return nil, errors.New("session: no clock provided; refusing to start")
	case len(roots) == 0:
		return nil, errors.New("session: no approved working-directory roots provided; refusing to start")
	case maxSessions < 1:
		return nil, fmt.Errorf("session: a concurrent-session cap of %d would permit no session at all; refusing to start", maxSessions)
	}

	return &Manager{tmux: tmux, store: store, roots: roots, maxSessions: maxSessions, clock: clock}, nil
}

// CreateRequest is everything a caller may influence about a new session.
//
// Owner is in the struct but is not caller input: T022 fills it from the
// *auth.Caller that Verify returned, and Create refuses a zero one. It is a
// field rather than a separate parameter so that the ownership half of every
// later authorisation check has the same origin as the record it lands on.
type CreateRequest struct {
	// Owner is the authenticated identity, derived server-side (FR-012).
	Owner auth.CallerID

	// Name is the display label, validated by ValidateName. It reaches no tmux
	// target (FR-034).
	Name string

	// WorkDir is the caller's spelling of a directory. Only the resolved,
	// allowlist-checked result of ResolveWorkDir is ever used (FR-028).
	WorkDir string
}

// Create starts a session and returns the record plus the only copy of its
// bearer token (FR-013). The token is returned, never stored — the record
// carries its SHA-256 and nothing else.
//
// The order is the security property. Everything the caller supplied is
// validated before anything is executed, so a rejected request costs no tmux
// command at all; the record is claimed before the shell exists, so there is no
// moment where a live session has no owner and no deadline; and the tmux
// session is marked as ours before the Claude command is sent, so a failure
// mid-way leaves something startup reconciliation can recognise (FR-021).
//
// The concurrent-session cap is enforced at that same claim (FR-036), which is
// what makes it a bound rather than an estimate: the count and the insert happen
// under one lock, so creates racing at the boundary cannot both find room, and a
// create refused for want of room has still run no tmux command.
//
// A failure after tmux has started the shell goes through rollback, which
// verifies rather than assumes the teardown. Create returns an error in that
// case either way — but whether the record survives it depends on whether the
// shell did, which is the distinction Principle VI turns on.
//
// Nothing here emits an audit record. One record per request is the
// middleware's job (T020/T038); a manager that logged as well would produce two
// for one action, and the reason a create failed is server-side detail that
// belongs in the handler's record, not in a second one from underneath it.
func (m *Manager) Create(ctx context.Context, req CreateRequest) (*Session, string, error) {
	if req.Owner == "" {
		return nil, "", fmt.Errorf("create session: %w", ErrMissingOwner)
	}
	if err := ValidateName(req.Name); err != nil {
		return nil, "", fmt.Errorf("create session: %w", err)
	}
	workDir, err := ResolveWorkDir(req.WorkDir, m.roots)
	if err != nil {
		return nil, "", fmt.Errorf("create session: %w", err)
	}

	id, err := NewID()
	if err != nil {
		return nil, "", fmt.Errorf("create session: %w", err)
	}
	token, hash, err := NewToken()
	if err != nil {
		return nil, "", fmt.Errorf("create session %s: %w", id, err)
	}

	now := m.clock.Now()
	s := Session{
		ID:        id,
		Owner:     req.Owner,
		Name:      req.Name,
		WorkDir:   workDir,
		TokenHash: hash,
		CreatedAt: now,
		// Equal to CreatedAt, not a second reading of the clock: a session that
		// has never been used has been idle since it was created.
		LastActivity: now,
		State:        StateStarting,
	}

	// Claimed before the shell exists. A record without a tmux session is a
	// record whose endpoints fail; a tmux session without a record is a live
	// unsandboxed shell that no ownership check, cap, or reaper can see. Only
	// one of those two is survivable, so the ID is taken first.
	//
	// The claim is also where the cap is answered, and it is the only place it
	// is asked: a check before this line would be a second reading of the count
	// that the one under the lock could disagree with.
	if err := m.store.AddCapped(s, m.maxSessions); err != nil {
		return nil, "", fmt.Errorf("create session %s: %w", id, err)
	}

	if err := m.start(ctx, s); err != nil {
		return nil, "", m.rollback(ctx, s, err)
	}

	return &s, token, nil
}

// Resolve is the whole of layer 3 (FR-014): the record must exist, be the
// caller's own, carry the credential presented, still be inside the lifetime
// that credential was issued for, and not already be dead.
//
// The five are one call rather than five repeated at each session-scoped
// endpoint, because the failure this guards against is not a check written
// wrongly — it is a future endpoint that remembers four of them. Ownership is
// not even a step: Store.Get takes the owner and answers ErrSessionNotFound
// without it, so an unknown ID and someone else's ID are already one answer
// from one lookup (FR-032, FR-033).
//
// The distinct sentinels exist for the audit trail and for nothing else. A
// caller must not be able to tell "no such session" from "not yours" from
// "wrong token" from "expired" — that difference is what enumeration is made of
// — so the handler answers all of them identically (FR-033) and the operator
// reads which one it was in the trail. Nothing here is wrapped with the id: it
// is caller-supplied text, and the trail may not carry it (FR-042).
//
// Time comes from the manager's clock, the same one the reaper will enforce the
// absolute deadline on (T036), so a credential cannot be expired by one and
// live by the other.
func (m *Manager) Resolve(id string, owner auth.CallerID, presented string) (Session, error) {
	s, err := m.store.Get(id, owner)
	if err != nil {
		return Session{}, fmt.Errorf("resolve session: %w", err)
	}
	if !s.TokenMatches(presented) {
		return Session{}, fmt.Errorf("resolve session: %w", ErrTokenMismatch)
	}
	// Before, not !After: at the deadline the credential is already gone. The
	// boundary belongs on the refusing side of the comparison for the same
	// reason the signature window does — a lifetime that is "24 hours plus
	// however long the last request takes" is not a lifetime anyone bounded.
	if !m.clock.Now().Before(s.TokenExpiry()) {
		return Session{}, fmt.Errorf("resolve session: %w", ErrTokenExpired)
	}
	// A dead record answers exactly as an unknown ID does (data-model.md). Dead
	// is terminal, so this is not a race to lose: the session is gone and the
	// record is waiting to be collected.
	if s.State == StateDead {
		return Session{}, fmt.Errorf("resolve session: %w", ErrSessionDead)
	}
	return s, nil
}

// List is every session the caller owns, oldest first (FR-032).
//
// It is a method here rather than a handler reaching into the store, for the
// reason Resolve is: the store is not reachable through a Manager, so every path
// from a request to a record runs through one of these two calls and both take
// the owner as a parameter rather than as something a caller site remembers to
// check. A handler holding a *Store would be a second path, free to forget it.
//
// There is no lookup by name, by state, or by anything else, and adding one
// would need a reason: an owner is the only filter that is also an authorisation
// (FR-032), and every other one is a way of asking about records that are not
// the caller's.
//
// The records are copies and they carry the token hash, because a Session does.
// What may leave the daemon is settled at the HTTP boundary, where
// contracts/http-api.md names the fields — and the hash is not among them.
func (m *Manager) List(owner auth.CallerID) []Session { return m.store.List(owner) }

// Prompt delivers caller text into a session verbatim and submits it (FR-030).
//
// The record is the one Resolve returned, so ownership, the credential, and the
// deadline have already been settled and the target derives from its ID alone
// (FR-034). Prompt takes the record rather than an ID for exactly that reason:
// there is no second lookup here to disagree with the first, and no path by
// which a caller's spelling of an ID could name a window.
//
// Two commands, in this order and no other. Paste writes the bytes to a tmux
// buffer over stdin, so the payload never becomes part of a command line
// (FR-029) — research D4 is the reason this is not send-keys -l, which drops a
// trailing unescaped ";" from caller text before -l ever applies. The Return
// that follows is a daemon-authored key constant and travels the way the Claude
// start command does. Nothing escapes, quotes, or inspects the text: it is data.
//
// A failure leaves the text where it fell. The buffer paste-buffer -d would have
// deleted may survive a failed submit, which is prompt text sitting in a named
// tmux buffer another client could read — the caller is told the prompt did not
// land, and the buffer is overwritten by the next prompt to the same session.
func (m *Manager) Prompt(ctx context.Context, s Session, text string) error {
	// Unreachable behind the resolver, and refused rather than trusted: an empty
	// ID would build the bare prefix as a target, and a dead session's window is
	// already gone. Both fail closed here so that a future caller reaching this
	// method with a hand-made record cannot type into whatever that names.
	if s.ID == "" {
		return fmt.Errorf("prompt session: %w", ErrSessionNotFound)
	}
	if s.State == StateDead {
		return fmt.Errorf("prompt session %s: %w", s.ID, ErrSessionDead)
	}
	if text == "" {
		return fmt.Errorf("prompt session %s: %w", s.ID, ErrEmptyPrompt)
	}

	name := s.TmuxName()

	// The error deliberately names the session and nothing else. Prompt text is
	// secret under docs/security.md §3, so it may not travel back to a caller in
	// an error string any more than it may reach the trail (FR-042).
	if err := m.tmux.Paste(ctx, name, []byte(text)); err != nil {
		return fmt.Errorf("paste the prompt into session %s: %w", s.ID, err)
	}
	if err := m.tmux.SendKeys(ctx, name, enterKey); err != nil {
		return fmt.Errorf("submit the prompt in session %s: %w", s.ID, err)
	}
	return nil
}

// Capture is one reading of a session's pane, taken by Output.
//
// Text has already been through the stripper and is what will leave the daemon.
// It is secret under docs/security.md §3 — a pane holds whatever the session
// printed, which is anything on the host — so it may not be logged, audited, or
// put in an error message, and this type exists partly so that a value carrying
// it is recognisable at a glance.
type Capture struct {
	Text string
	At   time.Time
}

// Output reads a session's pane and returns it as text safe to hand to a client
// (FR-031).
//
// The record is the one Resolve returned, for the reason Prompt takes one: the
// target derives from the record's ID alone (FR-034), so there is no second
// lookup here to disagree with the first and no path from a caller's spelling of
// an ID to a window.
//
// Stripping happens here rather than inside CapturePane, which is what makes it
// a property of the daemon instead of one Controller: capture-pane is run
// without -e so tmux already renders plain text, and Strip is the second line of
// defence between a future -e — or a control byte the renderer let through — and
// a JSON string that ends up in a browser.
//
// The instant is read after the capture, not before. It is what the pane held as
// of *at most* that time, and a timestamp taken first would claim content newer
// than itself whenever the exec was slow.
func (m *Manager) Output(ctx context.Context, s Session) (Capture, error) {
	// Fail closed on the same two records Prompt refuses, and for the same
	// reason: an empty ID builds the bare prefix as a target, and a dead
	// session's window is gone. Neither is reachable behind the resolver.
	if s.ID == "" {
		return Capture{}, fmt.Errorf("capture pane: %w", ErrSessionNotFound)
	}
	if s.State == StateDead {
		return Capture{}, fmt.Errorf("capture pane of session %s: %w", s.ID, ErrSessionDead)
	}

	// The error names the session and wraps what tmux said, and deliberately
	// carries no captured text: a partial read reaching an error string is pane
	// content in whatever records that error (FR-042).
	text, err := m.tmux.CapturePane(ctx, s.TmuxName())
	if err != nil {
		return Capture{}, fmt.Errorf("capture pane of session %s: %w", s.ID, err)
	}

	return Capture{Text: tmuxctl.Strip(text), At: m.clock.Now()}, nil
}

// Destroy tears a session down and reports success only once the host has
// confirmed it is gone (FR-019), then clears the record and the credential hash
// with it (FR-020).
//
// The order is the requirement. A kill that reports success is not evidence —
// tmux answering "I asked" is not tmux answering "it is gone" — so nothing is
// dropped until confirmGone says so. Until then the record stays, because a
// record is the only thing carrying an owner and two deadlines for a session
// that may still be running, and adoption runs at startup: a live session the
// running daemon has forgotten is forgotten for good.
//
// The Kill error is folded into the result rather than returned on its own, for
// the reason rollback does it. A session whose shell already exited is gone
// before the kill lands, so "can't find session" here is an ordinary outcome
// that ends in success, and only the verification decides.
//
// The state checks Prompt and Output make are deliberately absent. They refuse a
// dead record because their action needs a live window; this one's action is
// removal, and refusing it would leave the record nothing could clear. Only the
// empty id is refused, and for the same reason as there: it would build the bare
// prefix as a target.
//
// FR-020's other two clauses are satisfied by construction rather than by code
// here. There is no buffered output to clear — Output captures a pane per
// request and caches nothing, and a cache added later must be cleared in this
// method — and the daemon creates no working directory, since ResolveWorkDir
// only ever approves one that already existed.
//
// Nothing here emits an audit record, for the reason Create does not: one record
// per request belongs to the middleware, and the surviving-session case is
// prominent in the trail because the handler answers 409 (T029), not because
// this method logged underneath it.
func (m *Manager) Destroy(ctx context.Context, s Session) error {
	if s.ID == "" {
		return fmt.Errorf("destroy session: %w", ErrSessionNotFound)
	}

	name := s.TmuxName()
	killErr := m.tmux.Kill(ctx, name)

	gone, verifyErr := m.confirmGone(ctx, name)
	if verifyErr != nil || !gone {
		if detail := errors.Join(killErr, verifyErr); detail != nil {
			return fmt.Errorf("destroy session %s: %w: %w", s.ID, ErrOrphanedSession, detail)
		}
		return fmt.Errorf("destroy session %s: %w", s.ID, ErrOrphanedSession)
	}

	// A record already gone is not a failure. spec.md names a destroy racing the
	// reaper as an edge case, and both of them end at this line: the session is
	// confirmed gone and so is the record, which is exactly what the caller
	// asked for. Reporting an error for a teardown that completed would be the
	// one lie Principle VI cannot afford in either direction.
	if err := m.store.Delete(s.ID); err != nil && !errors.Is(err, ErrSessionNotFound) {
		return fmt.Errorf("destroy session %s: %w", s.ID, err)
	}
	return nil
}

// AdoptedSession is one reconciled record together with the only copy of the
// credential issued for it (FR-021).
//
// The token comes back to the caller rather than going into the record, for the
// reason Create's does: the plaintext exists in one place and the record keeps
// only its hash. Whatever the caller does with it, the trail is not one of the
// options — an adopted session's token in an audit record is the credential to
// an unsandboxed shell sitting in journald (FR-042).
type AdoptedSession struct {
	Session Session
	Token   string
}

// Adopt reconciles the daemon with the host: every live tmux session it created
// but has no record of is taken back under management, and anything already past
// its ceiling is destroyed instead of adopted (FR-021, FR-025).
//
// It runs at startup before the listener binds (T032), which is what makes an
// empty store the ordinary case rather than an assumption. A candidate the store
// already knows is left exactly as it is — that is US4 scenario 7, and it is the
// reason a restart loop cannot hold a session open: nothing here writes to a
// record that already exists.
//
// Discovery is one List, and everything reconciliation needs arrives in that one
// call — name, creation time, and provenance (research D6). What follows is per
// candidate, and per candidate the daemon asks one further question, because
// FR-022 will not let a present-but-unusable session be recorded as healthy and
// the listing on its own offers nothing to resolve that against.
//
// The clock is the deliberate part. CreatedAt is tmux's own #{session_created},
// so an adopted session keeps the absolute deadline it always had; only
// LastActivity is reset, because the daemon genuinely does not know when the
// session was last driven (FR-024). A restart therefore buys nothing.
//
// Every adoption mints a fresh credential. The one the dead process issued is
// unrecoverable by design (FR-021) — it was never stored, so this is not a
// choice made here so much as the only thing that was ever possible.
//
// Nothing here is capped, and Store.Add says why: a session already running on
// the host is taken back however many there are, because the alternative to an
// over-cap record is an unowned unsandboxed shell. What the adopted records do
// is count — every later create is refused until the reaper has brought the
// fleet back under CRSW_MAX_SESSIONS, which is FR-036 applied to the host the
// daemon actually woke up on.
//
// Failures are collected rather than returned at the first one. A single session
// the host cannot answer for must not leave the rest unowned, and startup treats
// any returned error as fatal (T032), so nothing here is quietly skipped.
func (m *Manager) Adopt(ctx context.Context) ([]AdoptedSession, error) {
	infos, err := m.tmux.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("reconcile with the host: %w", err)
	}

	now := m.clock.Now()
	adopted := make([]AdoptedSession, 0, len(infos))
	var failures []error

	for _, info := range infos {
		id, ours := adoptableID(info)
		if !ours {
			continue
		}
		// FR-021 adopts what has no record. A second pass over a live store must
		// leave the first pass's work — and its deadline, and its credential —
		// untouched.
		if _, known := m.store.lookup(id); known {
			continue
		}

		s := Session{
			ID:    id,
			Owner: auth.CallerOperator,
			// Name and WorkDir stay empty on purpose. Neither survives the
			// process that knew them: nothing on the host carries the caller's
			// label at all, and while tmux does know the session's directory,
			// SessionInfo does not carry it and widening that contract is not
			// this task. A value invented here would describe nothing, and the
			// id is the only field a tmux target is ever built from anyway.
			CreatedAt:    info.Created,
			LastActivity: now,
			State:        StateRunning,
			Adopted:      true,
		}

		// FR-025: a session that outlived its ceiling while the daemon was down
		// is torn down, not adopted into an already-expired state. Destroy
		// verifies rather than assumes, and the record was never added — so the
		// delete it ends with is the harmless no-op it already tolerates.
		if !now.Before(s.AbsoluteDeadline()) {
			if err := m.Destroy(ctx, s); err != nil {
				failures = append(failures, fmt.Errorf("a session was past its ceiling at startup: %w", err))
			}
			continue
		}

		// The second observation, and the only one this Controller can make:
		// List said the session was there, and this asks again by name. A
		// session that is gone between the two questions is resolved to the
		// definite state "gone" by never becoming a record — there is nothing to
		// tear down, and a record for a window that does not exist is a session
		// nobody can drive answering as though somebody could.
		//
		// An error is not an answer, and adopting on one would be recording a
		// session as healthy on no evidence at all — the single thing FR-022
		// names. It is reported instead, which at startup is fatal, and the next
		// boot lists the session again.
		present, err := m.tmux.Has(ctx, s.TmuxName())
		if err != nil {
			failures = append(failures, fmt.Errorf("confirm session %s is still on the host: %w", id, err))
			continue
		}
		if !present {
			continue
		}

		token, hash, err := NewToken()
		if err != nil {
			failures = append(failures, fmt.Errorf("adopt session %s: %w", id, err))
			continue
		}
		s.TokenHash = hash

		if err := m.store.Add(s); err != nil {
			failures = append(failures, fmt.Errorf("adopt session %s: %w", id, err))
			continue
		}
		adopted = append(adopted, AdoptedSession{Session: s, Token: token})
	}

	return adopted, errors.Join(failures...)
}

// adoptableID is the whole of FR-022: which host sessions are ours to take back,
// and what the id of the record for one is.
//
// Three signals must agree, and no session the daemon created can fail any of
// them:
//
//   - @crswd-managed is provenance. A session that merely resembles the prefix
//     was not created here, and is neither adopted nor destroyed.
//   - The reserved prefix is what makes the record's TmuxName address the window
//     it was built from. Without it there is no id to build a record around, and
//     a record whose target named some other session would be worse than none.
//   - The shape is provenance a second time. Every id the daemon mints is 32
//     lowercase hex characters (NewID), so a marked, prefixed session named
//     anything else was marked by something that is not this daemon — and its id
//     would go on to be what API responses and path values are made of.
//
// Failing any of them means leaving the session alone, which is the same answer
// FR-022 gives a lookalike: not adopted, and not touched.
func adoptableID(info tmuxctl.SessionInfo) (string, bool) {
	if !info.Managed {
		return "", false
	}

	id, ok := strings.CutPrefix(info.Name, tmuxNamePrefix)
	if !ok || len(id) != IDLen {
		return "", false
	}
	// Ranging over the string rather than its bytes costs nothing and refuses a
	// multi-byte rune on the same branch as an out-of-class byte.
	for _, c := range id {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return "", false
		}
	}
	return id, true
}

// confirmGone answers whether the host still has the session, and refuses to
// guess when it cannot tell (FR-019).
//
// Has is the direct question, and whenever tmux can answer it that answer is
// taken. The fallback exists because of what a *successful* teardown does to a
// host running one session: tmux exits with its last session, so the has-session
// that follows reports "no server running" rather than "can't find session".
// Has is required to call that an error (contracts/tmuxctl.md) — a dead server
// is exactly what a tmux that never started looks like, and reading it as
// absence would let a broken binary confirm every teardown. So Has cannot answer
// it, and without a second question every correct destruction of the last
// session on the host would be reported as an orphan.
//
// List is that second question, and it is sound where collapsing Has would not
// be: a server that is not running has no sessions, and List distinguishes that
// from a server it merely could not reach — a socket that exists but refuses the
// connection is still an error there, so a reachable-but-broken tmux cannot look
// like an empty host. The exec costs one command, and only on the path where the
// first one already failed.
//
// An error means the answer is unknown, and Destroy treats unknown as surviving.
func (m *Manager) confirmGone(ctx context.Context, name string) (bool, error) {
	present, err := m.tmux.Has(ctx, name)
	if err == nil {
		return !present, nil
	}

	sessions, listErr := m.tmux.List(ctx)
	if listErr != nil {
		return false, errors.Join(err, listErr)
	}
	return !slices.ContainsFunc(sessions, func(info tmuxctl.SessionInfo) bool {
		return info.Name == name
	}), nil
}

// start runs the four tmux commands FR-018 describes, in the only order that
// works: the session must exist before an option can be set on it, and it must
// be marked as ours before it runs anything, because an unmarked session is one
// reconciliation will not adopt (FR-022) and therefore never tears down.
//
// Every target derives from s.TmuxName(), which derives from the ID alone. The
// caller's Name is not passed to this package and could not reach a target if it
// were.
func (m *Manager) start(ctx context.Context, s Session) error {
	name := s.TmuxName()

	if err := m.tmux.New(ctx, name, s.WorkDir); err != nil {
		return fmt.Errorf("start tmux session: %w", err)
	}
	if err := m.tmux.SetOption(ctx, name, tmuxctl.OptionManaged, tmuxctl.OptionManagedValue); err != nil {
		return fmt.Errorf("mark tmux session managed: %w", err)
	}
	if err := m.tmux.SetOption(ctx, name, tmuxctl.OptionOwner, string(s.Owner)); err != nil {
		return fmt.Errorf("mark tmux session owner: %w", err)
	}
	if err := m.tmux.SendKeys(ctx, name, claudeStartCommand, enterKey); err != nil {
		return fmt.Errorf("send the claude start command: %w", err)
	}

	return nil
}

// rollback undoes a half-started session and returns the error Create answers
// with. It is deliberately asymmetric about what it does not know.
//
// The kill is asked for and then *verified*, because a kill that reports success
// is not evidence (FR-019). Three outcomes:
//
//   - Confirmed gone: the record is dropped and the original failure is
//     returned. Nothing survives the failed create.
//   - Confirmed still there, or tmux could not be asked: the record is **kept**
//     and ErrOrphanedSession is wrapped in. A kept record is a session with an
//     owner, an idle deadline, and an absolute deadline, which is the only thing
//     that will ever collect it — adoption runs at startup, so a live session
//     the running daemon has forgotten is forgotten for good. The cost is a
//     record for a session that may already be dead, which the reaper resolves;
//     the alternative cost is an unowned unsandboxed shell, which nothing does.
//
// The caller of a create that ends here holds no token: the plaintext is
// discarded with the failed response, so the retained record is drivable by
// nobody and reapable by the daemon. That is the intended end state.
//
// The Kill error is folded into the result rather than returned on its own. A
// New that failed may have left nothing to kill, so "can't find session" here is
// the expected case and only Has's answer decides.
func (m *Manager) rollback(ctx context.Context, s Session, cause error) error {
	name := s.TmuxName()
	killErr := m.tmux.Kill(ctx, name)

	present, verifyErr := m.tmux.Has(ctx, name)
	if verifyErr != nil || present {
		return fmt.Errorf("create session %s: %w: %w",
			s.ID, ErrOrphanedSession, errors.Join(cause, killErr, verifyErr))
	}

	if delErr := m.store.Delete(s.ID); delErr != nil {
		return fmt.Errorf("create session %s: %w", s.ID, errors.Join(cause, delErr))
	}
	return fmt.Errorf("create session %s: %w", s.ID, cause)
}
