package session

import (
	"cmp"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/nctiggy/claude-remote-session-webhook/internal/auth"
	"github.com/nctiggy/claude-remote-session-webhook/internal/tmuxctl"
)

// The two lifetimes every session is bounded by (FR-038, Principle VI). They are
// constants rather than configuration on purpose: an operator who could widen
// them could widen the blast radius the constitution bounds by construction.
const (
	// AbsoluteLifetime is measured from CreatedAt and is never renewed.
	AbsoluteLifetime = 24 * time.Hour

	// IdleTimeout is measured from LastActivity and moves with it.
	IdleTimeout = 60 * time.Minute
)

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
)

// Valid reports whether s is one of the three states. Anything else is a record
// the store refuses rather than stores, because a session in no state has no
// answer to "does this accept a prompt?".
func (s State) Valid() bool {
	switch s {
	case StateStarting, StateRunning, StateDead:
		return true
	}
	return false
}

// Session is the daemon's record of one live Claude Code session, held in memory
// for the process lifetime. There is no schema and no file on disk — restart
// recovery comes from adopting live tmux sessions (FR-021), not from storage.
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

	// LastActivity is the origin of the idle deadline, moved by Store.Touch.
	LastActivity time.Time

	// State is the lifecycle position above.
	State State

	// Adopted records that this session was reconciled from the host rather
	// than created through the API. It is audit-visible and changes no rule:
	// an adopted session is subject to the same ownership check and the same
	// timeouts as any other (FR-023).
	Adopted bool
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
func (s Session) AbsoluteDeadline() time.Time { return s.CreatedAt.Add(AbsoluteLifetime) }

// IdleDeadline is when the session dies for want of a request (FR-038).
func (s Session) IdleDeadline() time.Time { return s.LastActivity.Add(IdleTimeout) }

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

	// ErrSessionDead marks an attempt to move a terminal record. Dead is the
	// end of the state machine in data-model.md.
	ErrSessionDead = errors.New("session is dead")

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
// the fields the idle and absolute deadlines derive from. Every mutation goes
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
	s.State = next
	st.byID[id] = s
	return nil
}

// Touch moves the idle clock forward for a live session.
//
// It only ever moves forward. A reading behind the one already recorded is
// dropped rather than stored, so a lagging clock read cannot shorten a session's
// remaining idle time — the deadline this feeds is enforced by a reaper the
// caller cannot see, and a silently shortened one would look like an arbitrary
// disappearance.
//
// A dead session is refused: its idle clock has no one left to run for, and
// advancing it would keep a record the reaper should be collecting.
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
