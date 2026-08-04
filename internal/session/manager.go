package session

import (
	"context"
	"errors"
	"fmt"
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

	// ErrOrphanedSession is the loud answer to a create that failed *after* tmux
	// had already started a shell, where the rollback could not confirm the shell
	// is gone (Principle VI). It is a sentinel so a handler can answer 500 and
	// the audit trail can carry the one thing that matters: a live unsandboxed
	// session may exist on the host. The record is kept when this is returned —
	// see rollback.
	ErrOrphanedSession = errors.New("the tmux session could not be confirmed gone and may still be running")
)

// Manager owns the mapping between session records and the tmux sessions they
// name. It is safe for concurrent use: every field is read-only after
// construction and the store and controller are both concurrency-safe.
type Manager struct {
	tmux  tmuxctl.Controller
	store *Store
	roots []config.ApprovedRoot
	clock Clock
}

// NewManager builds a Manager on the host clock. This is the constructor the
// daemon uses; tests reach for NewManagerWithClock.
func NewManager(tmux tmuxctl.Controller, store *Store, roots []config.ApprovedRoot) (*Manager, error) {
	return NewManagerWithClock(tmux, store, roots, systemClock{})
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
func NewManagerWithClock(tmux tmuxctl.Controller, store *Store, roots []config.ApprovedRoot, clock Clock) (*Manager, error) {
	switch {
	case tmux == nil:
		return nil, errors.New("session: no tmux controller provided; refusing to start")
	case store == nil:
		return nil, errors.New("session: no session store provided; refusing to start")
	case clock == nil:
		return nil, errors.New("session: no clock provided; refusing to start")
	case len(roots) == 0:
		return nil, errors.New("session: no approved working-directory roots provided; refusing to start")
	}

	return &Manager{tmux: tmux, store: store, roots: roots, clock: clock}, nil
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
	if err := m.store.Add(s); err != nil {
		return nil, "", fmt.Errorf("create session %s: %w", id, err)
	}

	if err := m.start(ctx, s); err != nil {
		return nil, "", m.rollback(ctx, s, err)
	}

	return &s, token, nil
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
