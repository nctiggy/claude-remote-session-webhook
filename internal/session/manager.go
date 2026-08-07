package session

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
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

// interruptKey is tmux's name for Ctrl-C, which the pane's line discipline turns
// into SIGINT for whatever holds the terminal. It is how a mode change ends the
// running process **without** ending the session (FR-028).
//
// The alternative was respawn-pane -k, which is deterministic where this is a
// request — and it was measured rather than argued about: respawn-pane restarts
// the pane's command with an empty screen, so the conversation an operator was
// reading is gone. That is the one thing SC-007 asks this transition to keep, and
// it is why the process is signalled through the terminal instead: nothing here
// resets the pane, so its history is untouched by construction.
//
// It is sent twice in one call, and the second one is the load-bearing half. An
// assistant that catches SIGINT reads the first as "cancel what you are doing"
// and only a second as "leave"; at a bare shell prompt both are the same no-op on
// an empty line. What neither can do is guarantee the process is gone — see
// SetMode, which says what this daemon may and may not claim about that.
const interruptKey = "C-c"

// continueFlag is what makes a mode change a change of mode rather than a new
// conversation (SC-007). The restarted command resumes what the pane was already
// holding, which is the whole reason the session, its window and its scrollback
// are preserved either side of it: a toggle that started fresh would have thrown
// away the work the operator is toggling in order to keep driving.
//
// It is daemon-authored and carries no ";" for tmux's parser to eat, so it
// travels the way the start command does rather than through Paste.
const continueFlag = "--continue"

// compactCommand is Claude Code's own /compact and the newline that submits it
// (FR-016, research D2). Nine bytes, every one of them delivered as data.
//
// The newline rides in the payload rather than arriving afterwards as an Enter
// key, which is the difference between this delivery and Prompt's two commands.
// It is not that these bytes are dangerous — the daemon authored them, and they
// carry no ";" for tmux's parser to eat. It is that a second way of putting
// characters into a pane is the one a later hand copies for text this daemon did
// not author, so there is one way, and FR-016 names it: the load-buffer path
// prompt text already takes.
const compactCommand = "/compact\n"

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

	// ErrModeUnavailable refuses a mode this daemon has no command for: remote
	// on a daemon configuring no remote-control command, and local on one whose
	// remote-control command *is* the default, where there is nothing to return
	// to. Both are configuration rather than request, so the refusal is the same
	// whoever asks and no session is touched by it.
	ErrModeUnavailable = errors.New("this daemon configures no command for that mode")

	// ErrModeUnchanged refuses a toggle to the mode the session is already in.
	//
	// It is a refusal rather than a silent success because of what carrying it out
	// would cost: the transition interrupts the process the operator is watching
	// and starts another in its place, which is not a no-op merely because the
	// mode either side of it is the same word. A stale card and a double
	// submission both arrive here, and both are better answered than acted on.
	ErrModeUnchanged = errors.New("the session is already in that mode")

	// ErrUnknownMode is a Mode value that is neither of the two. It is not
	// reachable through the dashboard — that door matches the two literals before
	// anything is looked up — and exists so a future caller handing this method a
	// mode built from a request fails closed rather than resolving it to a
	// command.
	ErrUnknownMode = errors.New("no such session mode")

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
// construction except the subscriber set, which carries its own lock, and the
// store and controller are both concurrency-safe.
type Manager struct {
	// maxLifetime and maxIdle are the ceilings a per-session override may not
	// exceed (#37). Zero means the built-in constants, so a manager nobody
	// configured refuses any override beyond what the daemon always allowed.
	maxLifetime time.Duration
	maxIdle     time.Duration

	// defaultLifetime and defaultIdle are what a create that asks for nothing
	// gets. Zero means the built-in constants.
	defaultLifetime time.Duration
	defaultIdle     time.Duration

	// startCommands is the operator's named set (#38). The zero value means
	// "only the daemon's own default", which is what every caller predating
	// this feature means.
	startCommands config.StartCommands

	// remoteControlCommand is the one name in that set which means remote
	// (#58, contracts/session-mode.md). Empty means this daemon configures no
	// remote control at all, so every session it runs is local and there is no
	// mode to switch to — which is the zero value, and the daemon that shipped
	// before the toggle existed.
	remoteControlCommand string

	tmux  tmuxctl.Controller
	store *Store
	roots []config.ApprovedRoot
	clock Clock

	// maxSessions is CRSW_MAX_SESSIONS, read once at construction. It is held
	// here rather than in the store because it is configuration and the store is
	// not configured — but it is *enforced* in the store, under the lock that
	// makes counting and inserting one act (see Store.AddCapped).
	maxSessions int

	// events is who is watching the fleet. Its zero value is a working event
	// source with nobody subscribed, so a Manager built by any constructor emits
	// correctly and no path has to remember to install one.
	events fleetEvents
}

// NewManager builds a Manager on the host clock. This is the constructor the
// daemon uses; tests reach for NewManagerWithClock.
// SetStartCommands gives the manager the operator's named command set (#38).
//
// It is a setter rather than a constructor parameter because every existing
// caller — and every test — means "the daemon's own default", and threading a
// fourth argument through all of them to say so would be the churn AR-008
// forbids. A manager that was never given a set starts sessions with
// claudeStartCommand, which is exactly what it did before this existed.
func (m *Manager) SetStartCommands(cmds config.StartCommands) { m.startCommands = cmds }

// SetRemoteControlCommand names which configured command means remote (#58).
//
// A setter for the reason SetStartCommands is one, and it takes the name
// config.Config already resolved rather than reading the environment: the loader
// has checked that name against the configured set and refused at startup if it
// was not there, so a name arriving here is one this daemon runs. A manager
// nobody told is a daemon with no remote control, which is what every caller
// predating the toggle means.
//
// The name lives here rather than on each record because which name means remote
// is startup configuration, and a copy on a record would be free to disagree with
// it after a restart — the second source of truth research R5 rejected.
func (m *Manager) SetRemoteControlCommand(name string) { m.remoteControlCommand = name }

// SetLifetimes gives the manager the operator's configured defaults and
// ceilings (#37). A setter for the reason SetStartCommands is one: every
// existing caller means "the constants", and threading four arguments through
// them all to say so is the churn AR-008 forbids.
func (m *Manager) SetLifetimes(defaultLifetime, maxLifetime, defaultIdle, maxIdle time.Duration) {
	m.defaultLifetime, m.maxLifetime = defaultLifetime, maxLifetime
	m.defaultIdle, m.maxIdle = defaultIdle, maxIdle
}

// resolveLifetimes turns what a create asked for into what the record carries.
//
// An override above its ceiling is refused rather than clamped. Silently
// granting one day to a caller who asked for thirty is worse than refusing:
// they believe they have thirty, and nothing tells them otherwise until the
// session is gone.
func (m *Manager) resolveLifetimes(req CreateRequest) (lifetime, idle time.Duration, err error) {
	maxLife := orDefault(m.maxLifetime, AbsoluteLifetime)
	maxIdleAllowed := orDefault(m.maxIdle, IdleTimeout)

	lifetime = req.Lifetime
	if lifetime == 0 {
		lifetime = m.defaultLifetime
	}
	switch {
	case lifetime < 0:
		return 0, 0, fmt.Errorf("%w: a session lifetime may not be negative", ErrInvalidLifetime)
	case lifetime > maxLife:
		return 0, 0, fmt.Errorf("%w: %s exceeds the %s ceiling", ErrInvalidLifetime, lifetime, maxLife)
	}

	idle = req.Idle
	if idle == 0 {
		idle = m.defaultIdle
	}
	// A negative idle is the disable, not an error, and it is bounded by the
	// absolute deadline still applying.
	if idle > maxIdleAllowed {
		return 0, 0, fmt.Errorf("%w: an idle timeout of %s exceeds the %s ceiling", ErrInvalidLifetime, idle, maxIdleAllowed)
	}
	// An idle timeout that can never fire is a setting that does nothing, which
	// is worth refusing rather than accepting quietly.
	effectiveLife := orDefault(lifetime, AbsoluteLifetime)
	if idle > effectiveLife {
		return 0, 0, fmt.Errorf("%w: an idle timeout of %s can never fire inside a %s lifetime", ErrInvalidLifetime, idle, effectiveLife)
	}
	return lifetime, idle, nil
}

// resolveStartCommand turns the name a create asked for into the command line
// typed into the session's shell.
//
// An unconfigured manager answers the built-in default for any name, so the
// zero value is the daemon that shipped before this feature. A configured one
// refuses a name it does not know: a create that asks for "rc" on a daemon with
// no "rc" must not quietly get a plain session, because the operator would have
// no way to tell that is what happened.
func (m *Manager) resolveStartCommand(name string) (string, error) {
	if len(m.startCommands.Names()) == 0 {
		if name == "" || name == config.DefaultStartCommandName {
			return claudeStartCommand, nil
		}
		return "", fmt.Errorf("%w: %q", ErrUnknownStartCommand, name)
	}
	cmd, ok := m.startCommands.Command(name)
	if !ok {
		return "", fmt.Errorf("%w: %q", ErrUnknownStartCommand, name)
	}
	return cmd, nil
}

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

// FleetEventKind is what happened, in the vocabulary contracts/fleet-stream.md
// puts on the wire. These three values *are* the wire values — the stream names
// its SSE event with one of them — so a fourth added here without a line in that
// contract is an event no page knows how to read.
type FleetEventKind string

const (
	// FleetAppeared is a session entering the fleet by any means: a create
	// through either door, startup adoption, or a half-started create whose
	// record was kept because the host would not confirm the teardown.
	FleetAppeared FleetEventKind = "appeared"

	// FleetVanished is a session leaving it by any means: a destroy through
	// either door, the reaper, shutdown, or a capture that discovered the
	// session was already gone.
	FleetVanished FleetEventKind = "vanished"

	// FleetChanged is a session that is still there and no longer renders the
	// same — a displayed-state transition, or a rename.
	FleetChanged FleetEventKind = "changed"
)

// FleetEvent is one change to one owner's fleet.
//
// It carries an identifier and an owner and nothing else. The owner is not part
// of what goes on the wire; it is what decides whose wire the event reaches
// (FR-019b). The payload the stream writes is the id alone (research R6): a
// name, a path or a state here would be a session field travelling to a page
// that already has a route for fetching one, through a channel that renders
// nothing.
type FleetEvent struct {
	Kind  FleetEventKind
	ID    string
	Owner auth.CallerID
}

// fleetBacklog is how far behind one subscriber may fall before it is dropped
// rather than waited for.
//
// What matters is not the number but what happens at it: a full buffer ends the
// subscription instead of blocking the goroutine that changed the fleet.
// Shutdown is the largest burst the daemon can produce — DestroyAll emits one
// event per record — so a value well above CRSW_MAX_SESSIONS (5 by default)
// means a reader that is merely between selects is never dropped for a reason
// that is the daemon's rather than its own.
const fleetBacklog = 64

// fleetSub is one open subscription: whose fleet it is about, and where the
// events go. Subscribers are identified by pointer, so an operator with two
// dashboard tabs open is two subscribers rather than one.
type fleetSub struct {
	owner auth.CallerID
	ch    chan FleetEvent
}

// fleetEvents is the subscriber set. Its zero value is usable and empty.
type fleetEvents struct {
	mu   sync.Mutex
	subs map[*fleetSub]struct{}
}

// Subscribe returns the caller's own fleet changes and the call that ends the
// subscription. It is how an open dashboard learns about a change no request of
// its own caused, which is the whole of issue #15.
//
// Ownership is a parameter rather than something the subscriber filters on
// afterwards, for the reason it is one on Store.Get and Store.List: an event
// about another identity's session must never reach the wire (FR-019b), and a
// check performed by the subscriber is a check the second subscriber may forget.
// An empty owner matches nothing — Add refuses a record without one, so the only
// way to ask this question is to have skipped authentication — and it gets a
// channel that is already closed rather than one that stays quiet, so a caller
// that came in the wrong way finds out at once instead of watching an empty
// fleet forever.
//
// The channel is closed when the subscription ends, whichever side ended it. A
// subscriber that falls fleetBacklog events behind is dropped exactly that way,
// because the alternative is a page still presenting a fleet it can no longer
// vouch for (FR-020): a closed channel is the stream's cue to end the response
// and the page's cue to say so.
//
// The returned cancel is idempotent, and safe after the daemon has already
// dropped the subscriber.
func (m *Manager) Subscribe(owner auth.CallerID) (<-chan FleetEvent, func()) {
	sub := &fleetSub{owner: owner, ch: make(chan FleetEvent, fleetBacklog)}
	if owner == "" {
		close(sub.ch)
		return sub.ch, func() {}
	}

	m.events.add(sub)
	return sub.ch, func() { m.events.drop(sub) }
}

// emit records one change to the fleet. It is called from beside the store
// mutation rather than from the handler that asked for one, and that placement
// is the whole design.
//
// Every path that changes the fleet ends in Store.Add, Store.AddCapped or
// Store.Delete in this file. So the reaper emits without knowing an event source
// exists — it tears down through Manager.Destroy — and so does shutdown, and so
// does a capture that discovers the session was already gone. Issue #15 is
// precisely a fleet change with no request behind it, and the way not to miss
// one is not to keep a list of the paths that have to remember.
func (m *Manager) emit(kind FleetEventKind, s Session) {
	m.events.publish(FleetEvent{Kind: kind, ID: s.ID, Owner: s.Owner})
}

func (e *fleetEvents) add(sub *fleetSub) {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.subs == nil {
		e.subs = make(map[*fleetSub]struct{})
	}
	e.subs[sub] = struct{}{}
}

// drop ends one subscription, once. The membership check is not defensive
// tidiness: publish drops a subscriber that has fallen behind, and every caller
// of Subscribe defers its cancel, so closing a channel twice is the ordinary
// case rather than the impossible one.
func (e *fleetEvents) drop(sub *fleetSub) {
	e.mu.Lock()
	defer e.mu.Unlock()

	if _, open := e.subs[sub]; !open {
		return
	}
	delete(e.subs, sub)
	close(sub.ch)
}

// publish delivers ev to the subscribers it belongs to and waits for none of
// them.
//
// The non-blocking send is the requirement rather than an optimisation. This
// runs on the goroutine that just changed the fleet — a request handler, the
// reaper's sweep, or shutdown — so a subscriber able to hold the send would be a
// browser tab able to hold a destroy, delay a reap, or keep the daemon from
// exiting.
//
// The lock is held across the fan-out, which is safe precisely because no send
// here can block: the worst case is every channel full, and each of those costs
// a delete and a close.
func (e *fleetEvents) publish(ev FleetEvent) {
	e.mu.Lock()
	defer e.mu.Unlock()

	for sub := range e.subs {
		if sub.owner != ev.Owner {
			continue
		}
		select {
		case sub.ch <- ev:
		default:
			// fleetBacklog behind: the subscription ends here rather than the
			// fleet change waiting for it. See Subscribe.
			delete(e.subs, sub)
			close(sub.ch)
		}
	}
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

	// Lifetime and Idle are optional per-session overrides (#37). Zero means the
	// daemon's default; a negative Idle disables idle reaping for this session.
	// Both are checked against the daemon's ceilings before anything is built.
	Lifetime time.Duration
	Idle     time.Duration

	// StartCommand names which configured command to type into the new session's
	// shell (#38). Empty means the daemon's default. It is a name, never a
	// command line — a create route that accepted a command line would be a
	// remote shell wearing a session manager's clothes.
	StartCommand string

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
	// Refused here rather than at start: a create that named a command this
	// daemon does not have must produce no record, no tmux session and no token,
	// exactly as an unusable name or a forbidden directory does. Resolving it at
	// start would mean tearing down something already built.
	command, err := m.resolveStartCommand(req.StartCommand)
	if err != nil {
		return nil, "", err
	}

	lifetime, idle, err := m.resolveLifetimes(req)
	if err != nil {
		return nil, "", err
	}

	if err := ValidateName(req.Name); err != nil {
		return nil, "", fmt.Errorf("create session: %w", err)
	}
	// A command that asks for {name} and a session that has none is refused
	// before anything is built (#58), rather than started with an empty --name
	// that would send the operator looking for a session by the wrong label. It
	// is an invalid name, and answered as one: ValidateName has already passed,
	// so in practice this only fires if these two ever disagree about the
	// alphabet — which is exactly when a substitution must not proceed.
	if _, err := config.RenderStartCommand(command, req.Name); err != nil {
		return nil, "", fmt.Errorf("create session: %w: %w", ErrInvalidName, err)
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
		ID:           id,
		Owner:        req.Owner,
		Name:         req.Name,
		StartCommand: req.StartCommand,
		Lifetime:     lifetime,
		Idle:         idle,
		WorkDir:      workDir,
		TokenHash:    hash,
		CreatedAt:    now,
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

	// After the shell exists, not after the claim: a create that fails between
	// the two is rolled back, and the fleet an open dashboard is watching gains
	// a session only if the record survives that. rollback announces the one
	// case where it does.
	m.emit(FleetAppeared, s)

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
	// One call, not two. The credential and its lifetime are enforced together in
	// token.go (FR-014, FR-015) so that this is not the place either of them is
	// spelled — a second session-scoped path added later cannot reproduce the
	// match and omit the expiry, because there is nothing here to copy.
	if err := s.CheckToken(presented, m.clock.Now()); err != nil {
		return Session{}, fmt.Errorf("resolve session: %w", err)
	}
	// A dead record answers exactly as an unknown ID does (data-model.md). Dead
	// is terminal, so this is not a race to lose: the session is gone and the
	// record is waiting to be collected.
	if s.State == StateDead {
		return Session{}, fmt.Errorf("resolve session: %w", ErrSessionDead)
	}

	// The idle clock moves here, and only here. This is no longer the only path
	// from a request to a session — View is a second one, for callers that watch
	// rather than drive — but it is the only path that drives, and every route
	// that acts on a session still reaches its record through this call. A
	// handler touching the clock instead would be a rule each new route has to
	// remember, and the cost of forgetting is a live session the reaper destroys
	// out from under an operator who is using it.
	//
	// The asymmetry between the two is deliberate and must not be tidied away.
	// View is *required* not to touch the clock (FR-034f): a browser tab left
	// open on a session nobody is driving would otherwise postpone its idle
	// deadline for as long as the tab lived, which is the bound Principle VI
	// calls non-negotiable. An iteration that "fixes" the inconsistency by adding
	// the touch to View is the failure this paragraph exists to prevent.
	//
	// After the checks, never before: a request that failed the owner or the
	// credential is not activity on the session, and letting it postpone the
	// deadline would let anyone who can reach the listener keep a session alive
	// forever without ever authenticating to it.
	//
	// The returned copy is the pre-touch one. LastActivity is the field this just
	// wrote, so re-reading the record to carry it back would cost a second lock
	// for a value no caller of Resolve reads — the handler renders the session it
	// was given, and GET /sessions reads the store afresh.
	now := m.clock.Now()
	displayed := s.DisplayState(now)
	if err := m.store.Touch(s.ID, now); err != nil {
		return Session{}, fmt.Errorf("resolve session: %w", err)
	}
	s.LastActivity = now

	// Activity that brings a session back from idle changes what the dashboard
	// draws, so the card an open page is showing is now wrong — which is a fleet
	// change by any definition an operator would recognise, even though no
	// record entered or left.
	//
	// The two readings are compared rather than the one transition being spelled
	// out, so a third display state (milestone 4's needs-auth) is covered here
	// without this line being revisited. Both are taken from the same instant:
	// what changed is LastActivity, not the time it is being judged at.
	if after := s.DisplayState(now); after != displayed {
		m.emit(FleetChanged, s)
	}
	return s, nil
}

// View is the read a watcher gets: the record must exist and must be the
// caller's own, and the call changes nothing about it. It is how the dashboard
// and the output stream reach a session (FR-017, FR-034f).
//
// It is Resolve without two of its checks, and both omissions are decisions
// rather than shortcuts.
//
// No credential is checked, because a browser holds none — and must not. A
// per-session token would have to ride in the URL or sit in script the page can
// read, and neither is a place to keep the credential to an unsandboxed shell.
// What authorises this call instead is the validated Access identity the browser
// door established, plus the ownership Store.Get enforces right here, both
// re-evaluated per request rather than established once. That pair is *more*
// specific than layer 2's shared secret, not less: the per-session token exists
// to tell apart callers who all authenticate as one secret, and a verified
// person who owns the session is already told apart.
//
// The idle clock is *not advanced*, which is the whole reason this is a second
// method rather than a flag on Resolve. Watching is not driving (FR-034f). The
// property holds by construction — there is no clock reading in this method to
// hand to Touch — rather than by every call site passing the right argument.
//
// Nothing expires here either, and that is not an omission: what Resolve
// refuses past a deadline is the *credential*, and this call presents none. A
// session's own life is ended by the reaper, which deletes the record — so a
// session past a bound stops being viewable by ceasing to exist, and until that
// sweep the dashboard shows an unsandboxed shell that is genuinely still
// running. Hiding it would be the one lie a read-only view cannot afford.
func (m *Manager) View(id string, owner auth.CallerID) (Session, error) {
	// Ownership is not a step here any more than it is in Resolve: Store.Get
	// takes the owner, so an unknown id and someone else's id are one answer from
	// one lookup (FR-032, FR-033). Nothing is wrapped with the id — it is
	// caller-supplied text and the trail may not carry it (FR-042).
	s, err := m.store.Get(id, owner)
	if err != nil {
		return Session{}, fmt.Errorf("view session: %w", err)
	}
	// A dead record answers exactly as an unknown id does (data-model.md), the
	// same answer Resolve gives it. The sentinel is distinct for the trail and
	// for nothing else: the caller gets one uniform refusal for all three.
	if s.State == StateDead {
		return Session{}, fmt.Errorf("view session: %w", ErrSessionDead)
	}
	return s, nil
}

// List is every session the caller owns, oldest first (FR-032).
//
// It is a method here rather than a handler reaching into the store, for the
// reason Resolve is: the store is not reachable through a Manager, so every path
// from a request to a record runs through one of these three calls — Resolve,
// View, or this one — and every one of them takes the owner as a parameter
// rather than as something a call site remembers to check. A handler holding a
// *Store would be a fourth path, free to forget it.
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

// ClaimPending mints the credential for every session of the caller's that was
// adopted and has not been handed one yet, and returns the plaintext keyed by
// session ID. Each token is returned by exactly one call; a second asks about
// nothing.
//
// This is how FR-021's "freshly issued credential" reaches the operator without
// a seventh route (FR-006) and without a plaintext token sitting in memory from
// startup (FR-013). Adoption happens with nobody asking, so there is no response
// to put a credential in; the first list *is* somebody asking, from a caller who
// has already proved layer 2 and owns the session. That is the same standing a
// create has when it receives a token, so nothing is being handed to a weaker
// claim than before.
//
// A session that cannot be given a credential is skipped and reported rather
// than silently listed as drivable. The caller decides what to do with a partial
// result; every token in the map is real.
func (m *Manager) ClaimPending(owner auth.CallerID) (map[string]string, error) {
	var failures []error
	claimed := map[string]string{}

	for _, s := range m.store.List(owner) {
		if !s.CredentialPending {
			continue
		}

		token, hash, err := NewToken()
		if err != nil {
			failures = append(failures, fmt.Errorf("mint a credential for an adopted session: %w", err))
			continue
		}
		// Stored before it is returned. The other order would hand a caller a
		// token the daemon might then fail to record, which is a credential that
		// looks issued and opens nothing.
		if err := m.store.SetCredential(s.ID, hash); err != nil {
			failures = append(failures, fmt.Errorf("record a credential for an adopted session: %w", err))
			continue
		}
		claimed[s.ID] = token
	}
	return claimed, errors.Join(failures...)
}

// Rename replaces a session's display label and changes nothing else (FR-015).
//
// It takes no context, and that is the statement rather than an oversight: there
// is no tmux command to run, because there is no tmux name to change. TmuxName
// derives from the ID alone (FR-034), so every target, every audit SessionID and
// every route parameter goes on naming exactly what it named before — which is
// what makes SC-012's "every identifier-based operation behaves identically"
// verifiable rather than aspirational.
//
// The record is the one View returned, for the reason Prompt and Destroy take
// one: ownership has already been settled, and there is no second lookup here to
// disagree with the first.
//
// Validation is ValidateName — Create's own check, called rather than restated. A
// name the create path refuses must not be reachable by renaming into it, and the
// only thing that keeps that true through a later widening of the alphabet is
// there being one check to widen.
//
// Names are not required to be unique. Nothing addresses a session by name, so a
// uniqueness rule would refuse a caller something that costs the daemon nothing —
// and would make the second dashboard tab's rename fail for a reason no operator
// could act on.
//
// There is no empty-id guard, unlike Prompt, Output and Destroy. Theirs exists
// because an empty id builds the bare prefix as a *target*; nothing here builds
// one, and Store.SetName already answers ErrSessionNotFound for an id it does not
// hold. A guard added here would be a branch no test could tell from the one
// below it.
func (m *Manager) Rename(s Session, name string) (Session, error) {
	if err := ValidateName(name); err != nil {
		return Session{}, fmt.Errorf("rename session %s: %w", s.ID, err)
	}

	if err := m.store.SetName(s.ID, name); err != nil {
		return Session{}, fmt.Errorf("rename session %s: %w", s.ID, err)
	}
	// The caller's copy carries the field this call just wrote, the way Resolve
	// carries the one it wrote: re-reading the store would cost a second lock for
	// the single field this method is the writer of.
	s.Name = name

	// Beside the store mutation rather than in the handler that asked for one,
	// like every other emit here. No record entered or left, and the card an open
	// page is drawing is wrong all the same — it shows a label the daemon no
	// longer holds, which is a fleet change by the only definition that matters.
	m.emit(FleetChanged, s)
	return s, nil
}

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

// Compact asks a session to compact itself, by delivering Claude Code's own
// /compact into it (FR-016).
//
// What it may claim is the whole of its shape. The daemon cannot see what the
// assistant is carrying, so nothing here observes a compaction: a nil error
// means the bytes reached the pane and means nothing else (FR-016a). A caller
// turning that into "compacted" would be asserting something no part of this
// process looked at.
//
// The text is a constant and not a parameter, which is what keeps this a *named*
// action rather than a way to type into a session from a browser — spec.md puts
// that surface out of scope for this milestone, and a text argument here is how
// it would arrive without anyone deciding to add it.
//
// The delivered bytes are never audited, logged, or named in an error (FR-016b,
// AR-007), exactly as prompt text is not. They hold no secret, being the
// daemon's own; the trail is searched for them anyway, because a record carrying
// what was delivered into a session is the shape of the leak whatever this
// particular payload says.
//
// The record is the one View returned, for the reason Prompt takes the one
// Resolve returned: ownership has already been settled, the target derives from
// the ID alone (FR-034), and there is no second lookup here to disagree with the
// first.
func (m *Manager) Compact(ctx context.Context, s Session) error {
	// Prompt's two guards, refused for Prompt's reasons: an empty ID would build
	// the bare prefix as a target, and a dead session's window is already gone.
	// Neither is reachable behind View, and both fail closed here so that a
	// future caller with a hand-made record cannot deliver into whatever an empty
	// target names.
	if s.ID == "" {
		return fmt.Errorf("compact session: %w", ErrSessionNotFound)
	}
	if s.State == StateDead {
		return fmt.Errorf("compact session %s: %w", s.ID, ErrSessionDead)
	}

	// The clock moves, and it moves before the bytes do. Both halves of that are
	// decisions.
	//
	// It moves at all because compact is activity: it delivers into the session
	// exactly as a prompt does, so it defers the idle deadline exactly as a
	// prompt does (data-model.md). On the API path Resolve is what does this, and
	// this is not that path — a browser presents no per-session credential, so it
	// reaches its session through View, which is required *not* to touch the
	// clock (FR-034f). A compact that left it alone too would be an operator
	// driving a session the reaper is still measuring as idle.
	//
	// It moves first because Touch is the store's own answer to whether this
	// record is still there and still live, taken under the store's lock rather
	// than off the copy the caller is holding. A record the reaper collected
	// between that caller's View and this call is refused here, before anything
	// is pasted. The other order fails worse: a Touch that failed after a
	// delivery had already landed would report a compact that did happen.
	now := m.clock.Now()
	displayed := s.DisplayState(now)
	if err := m.store.Touch(s.ID, now); err != nil {
		return fmt.Errorf("compact session %s: %w", s.ID, err)
	}
	s.LastActivity = now

	// Resolve's comparison, here for Resolve's reason: a deferred idle deadline
	// can move a card from idle back to running (data-model.md), which is a fleet
	// change an operator would recognise even though no record entered or left.
	// It sits beside the store mutation rather than after the delivery, so the
	// event stays true whatever tmux then says — what changed is the record, and
	// the record changed on the line above.
	if after := s.DisplayState(now); after != displayed {
		m.emit(FleetChanged, s)
	}

	// One call, and nothing after it. The newline is in the payload, so there is
	// no Return to press and no send-keys on this path at all. The error names
	// the session and never what was delivered.
	if err := m.tmux.Paste(ctx, s.TmuxName(), []byte(compactCommand)); err != nil {
		return fmt.Errorf("deliver the compact command to session %s: %w", s.ID, err)
	}
	return nil
}

// commandForMode is which configured name each mode runs, decided in one place
// because it is the mapping the whole toggle turns on (contracts/session-mode.md).
//
// It lives beside the transition that uses it rather than on the route that
// admits a mode, so there is no second copy free to disagree about what "remote"
// runs. Neither branch can produce anything but a name the operator configured:
// one is the name the loader resolved, the other is the daemon's own default, and
// a Mode that is neither of the two words is refused rather than resolved.
//
// The two refusals are the same fact from opposite ends — this daemon has no
// command for the mode asked for — and both are configuration rather than
// request, which is why they are refused before any session is read.
func (m *Manager) commandForMode(mode Mode) (string, error) {
	switch mode {
	case ModeRemote:
		if m.remoteControlCommand == "" {
			return "", fmt.Errorf("%w: no remote-control command is configured", ErrModeUnavailable)
		}
		return m.remoteControlCommand, nil
	case ModeLocal:
		// The operator pointed remote control at the default command, so the two
		// modes name one command and there is nowhere for local to go. Refused
		// rather than restarting the session under the command it is already
		// running, which would be an interruption bought for no change at all.
		if m.remoteControlCommand == config.DefaultStartCommandName {
			return "", fmt.Errorf("%w: the remote-control command is this daemon's default", ErrModeUnavailable)
		}
		return config.DefaultStartCommandName, nil
	default:
		// Never the value. It is not caller text today — the dashboard matches the
		// two literals before anything reaches here — and an error that printed it
		// would be the one place a future caller's bytes could enter the trail
		// (FR-042).
		return "", fmt.Errorf("%w", ErrUnknownMode)
	}
}

// SetMode moves a live session between local and remote by restarting the
// process inside its pane, and it is the whole of US4's transition (FR-026 …
// FR-031, SC-007).
//
// What it must not disturb is the definition. The tmux session, its window and
// its scrollback all survive, because nothing here creates, kills or respawns
// anything: two sends reach the pane the session already had. The identifier, the
// credential and both deadlines are untouched for the same reason — this is one
// record being described differently, not a new one. There is no mode field to
// write: the name the mode derives from is the single thing that moves, on the
// record and on the host (research R5).
//
// The order is what a failure is allowed to leave behind. Everything that can
// refuse refuses first, so a toggle this daemon would not carry out costs no tmux
// command at all; the interrupt goes before the new command line, because the
// pane is a terminal and the order the bytes arrive in is the order they are
// read; and the host is written before the record, so a daemon that stopped
// between the two comes back describing the session by what its pane is actually
// running.
//
// **What this may not claim.** The interrupt is a signal, not an acknowledgement,
// and nothing in tmux's vocabulary reports whether the process took it — so a nil
// error means the keys reached the pane and means exactly that, the way Compact's
// does (FR-016a). A process that ignores SIGINT would receive the new command
// line as input instead of the shell running it. The daemon cannot see the
// difference, and it is written down here rather than asserted away.
//
// The record is the one View returned, for the reason Rename and Compact take
// one: ownership is already settled, the target derives from the ID alone
// (FR-034), and there is no second lookup here to disagree with the first.
func (m *Manager) SetMode(ctx context.Context, s Session, mode Mode) (Session, error) {
	// Prompt's two guards, refused for Prompt's reasons: an empty ID would build
	// the bare prefix as a target, and a dead session's window is already gone.
	// Neither is reachable behind View.
	if s.ID == "" {
		return Session{}, fmt.Errorf("change session mode: %w", ErrSessionNotFound)
	}
	if s.State == StateDead {
		return Session{}, fmt.Errorf("change the mode of session %s: %w", s.ID, ErrSessionDead)
	}

	target, err := m.commandForMode(mode)
	if err != nil {
		return Session{}, fmt.Errorf("change the mode of session %s: %w", s.ID, err)
	}
	// Compared as modes rather than as names, so a session started under some
	// third configured command is correctly already local and is not restarted to
	// be told so.
	if s.Mode(m.remoteControlCommand) == mode {
		return Session{}, fmt.Errorf("change the mode of session %s: %w", s.ID, ErrModeUnchanged)
	}

	// Resolved before anything is sent, the way Create resolves before anything is
	// built: a mode naming a command this daemon cannot render must leave the pane
	// exactly as it was, rather than interrupting a session and then discovering
	// there is nothing to start in its place.
	template, err := m.resolveStartCommand(target)
	if err != nil {
		return Session{}, fmt.Errorf("resolve the start command for session %s: %w", s.ID, err)
	}
	command, err := config.RenderStartCommand(template, s.Name)
	if err != nil {
		return Session{}, fmt.Errorf("render the start command for session %s: %w", s.ID, err)
	}

	// Compact's touch, first and for Compact's reasons: a mode change drives the
	// session, so it defers the idle deadline as a prompt does, and Touch is the
	// store's own answer — under the store's lock — to whether this record is still
	// there and still live. A record the reaper collected between the caller's View
	// and this call is refused here, before the pane is interrupted.
	now := m.clock.Now()
	if err := m.store.Touch(s.ID, now); err != nil {
		return Session{}, fmt.Errorf("change the mode of session %s: %w", s.ID, err)
	}
	s.LastActivity = now

	name := s.TmuxName()
	if err := m.tmux.SendKeys(ctx, name, interruptKey, interruptKey); err != nil {
		return Session{}, fmt.Errorf("interrupt the process in session %s: %w", s.ID, err)
	}
	// One call for the line and the Return, exactly as start sends it: tmux
	// consumes the arguments in order, so a single exec both types the command and
	// runs it. The flag is appended by the daemon rather than configured, because
	// resuming is a property of this transition and not of the operator's command
	// — a configured --continue would also be there on every create, which is a
	// session that never starts fresh.
	if err := m.tmux.SendKeys(ctx, name, command+" "+continueFlag, enterKey); err != nil {
		return Session{}, fmt.Errorf("restart the process in session %s: %w", s.ID, err)
	}

	// The fifth option, rewritten (T017). Adoption reads it, so a mode change that
	// moved the pane and left the option would come back as the old mode after the
	// next restart — the silent reversal contracts/session-mode.md exists to
	// prevent — and it is written before the record for that reason: the host is
	// what a restarted daemon believes.
	if err := m.tmux.SetOption(ctx, name, tmuxctl.OptionStart, target); err != nil {
		return Session{}, fmt.Errorf("record the session start command: %w", err)
	}
	if err := m.store.SetStartCommand(s.ID, target); err != nil {
		return Session{}, fmt.Errorf("change the mode of session %s: %w", s.ID, err)
	}
	// The caller's copy carries the field this call just wrote, the way Rename's
	// does: re-reading the store would cost a second lock for the one field this
	// method is the writer of.
	s.StartCommand = target

	// Beside the store mutation rather than in the handler that asked for one,
	// like every other emit here. No record entered or left, and the card an open
	// page is drawing is wrong all the same — it names the mode this session is no
	// longer in, which is a fleet change by the only definition that matters.
	m.emit(FleetChanged, s)
	return s, nil
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
//
// A capture that fails is not the end of the question, and that is what
// unreadable resolves. "I could not read the screen" and "there is no screen"
// are different facts about the host, and until they were told apart the second
// one was rendered as the first — a record for a session that had died on its
// own kept a card on the fleet, a state pill, and a note inviting a reload that
// could never work (#21).
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

	text, err := m.tmux.CapturePane(ctx, s.TmuxName())
	if err != nil {
		return Capture{}, m.unreadable(ctx, s, err)
	}

	return Capture{Text: tmuxctl.Strip(text), At: m.clock.Now()}, nil
}

// unreadable is what a failed capture means, once the host has been asked.
//
// The daemon verifies teardown when *it* destroys a session (FR-019). A session
// that dies on its own — a host reboot, a tmux server restart, an operator's own
// kill-session — had no equivalent path, so its record outlived it until the
// reaper's idle bound or the 24-hour ceiling collected it (#21). The evidence
// arrives here, because the capture is the one thing the daemon does per request
// that touches the window rather than the record.
//
// The question is confirmGone's, and it is the same question a destroy asks for
// the same reason: tmux failing to answer is not tmux answering "it is gone", so
// only an affirmative "the session is not there" drops anything. A pane that
// merely could not be read leaves the record exactly as it was, and the caller
// gets what it always got — the honest "not just now", which is now true whenever
// it renders.
//
// Dropping the record is what Destroy does with a confirmed teardown (FR-020),
// and it is the same act on the same evidence: the record and the token hash go,
// and every endpoint for the id answers as it does for one nobody was ever
// issued. Nothing is killed on this path — there is nothing left to kill — and
// nothing is marked instead, because a record kept in StateDead would still be a
// card on the fleet, which is the defect rather than the fix.
//
// The idle clock is untouched here, so this remains safe on the watching path:
// what a stream discovers through Output is that the session is over, and the
// next tick's View turns that into the terminal event a viewer can read
// (FR-034f).
//
// No error is swallowed. tmux's account of the failed capture, and of a
// liveness check that could not be made, both travel back to the caller — which
// on every path is a report channel an operator reads, never a response body.
// None of them carries captured text: a partial read in an error string is pane
// content in whatever records that error (FR-042).
func (m *Manager) unreadable(ctx context.Context, s Session, cause error) error {
	gone, confirmErr := m.confirmGone(ctx, s.TmuxName())
	switch {
	case confirmErr != nil:
		return fmt.Errorf("capture pane of session %s: %w", s.ID, errors.Join(cause, confirmErr))
	case !gone:
		return fmt.Errorf("capture pane of session %s: %w", s.ID, cause)
	}

	// A record already gone is not a failure, for the reason it is not one in
	// Destroy: the session is confirmed gone and so is the record, which is the
	// end state this path exists to reach. Only a delete that failed for some
	// other reason is worth carrying back, and it is carried *alongside* the
	// answer rather than instead of it — the session is over either way.
	//
	// The event is Destroy's, on the same terms: this discovery is one of the
	// ways a session leaves the fleet without anyone asking it to, and only the
	// call that actually removed the record announces it.
	switch err := m.store.Delete(s.ID); {
	case err == nil:
		m.emit(FleetVanished, s)
	case !errors.Is(err, ErrSessionNotFound):
		return fmt.Errorf("drop the record of vanished session %s: %w: %w", s.ID, ErrSessionDead, err)
	}
	return fmt.Errorf("capture pane of session %s: %w", s.ID, ErrSessionDead)
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
	//
	// The event goes with the delete rather than with the kill, and only when
	// this call is the one that removed the record. The other half of that same
	// race has already announced the session, and a second vanished would have
	// an open page re-fetching a card that was gone the first time.
	switch err := m.store.Delete(s.ID); {
	case err == nil:
		m.emit(FleetVanished, s)
	case !errors.Is(err, ErrSessionNotFound):
		return fmt.Errorf("destroy session %s: %w", s.ID, err)
	}
	return nil
}

// DestroyAll tears down every session the daemon holds a record of, verified,
// and reports which ones the host confirmed gone (FR-040). It is what shutdown
// is made of.
//
// It is owner-blind, and that is the point. Shutdown acts on the daemon's own
// behalf, so there is no caller whose sessions these are — every record names a
// tmux session running with --dangerously-skip-permissions, and the process is
// about to stop being the thing that owns it. A teardown scoped to one caller
// would leave the rest as exactly the unowned shells Principle VI forbids.
//
// Teardown is Manager.Destroy for the reason the reaper's is: tmux answering "I
// asked" is not tmux answering "it is gone". A session that cannot be confirmed
// gone keeps its record and comes back in the error, where the shutdown path
// makes it loud rather than exiting quietly on a host that may still be carrying
// it.
//
// Failures are collected rather than returned at the first one, and for a
// sharper reason than the reaper's: nothing comes after this. A session skipped
// because an earlier one could not be confirmed would be left running by a
// process that is exiting, with no later sweep to catch it.
func (m *Manager) DestroyAll(ctx context.Context) ([]Session, error) {
	destroyed := make([]Session, 0, m.store.Len())
	var failures []error

	for _, s := range m.store.snapshot() {
		if err := m.Destroy(ctx, s); err != nil {
			failures = append(failures, err)
			continue
		}
		destroyed = append(destroyed, s)
	}

	return destroyed, errors.Join(failures...)
}

// AdoptedSession is one reconciled record together with the only copy of the
// credential issued for it (FR-021).
//
// The token comes back to the caller rather than going into the record, for the
// reason Create's does: the plaintext exists in one place and the record keeps
// only its hash. Whatever the caller does with it, the trail is not one of the
// options — an adopted session's token in an audit record is the credential to
// an unsandboxed shell sitting in journald (FR-042).
// AdoptedSession is one session taken back from the host at startup.
//
// It carries no token. Adoption mints none — see Adopt — because there is no
// response at startup to hand one to, and holding a plaintext credential in
// memory until somebody asked would be the storage FR-013 rules out. The
// credential is minted by ClaimPending, in the reply that delivers it.
type AdoptedSession struct {
	Session Session
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
			// Restored from the host, which is where they were written when the
			// session was created (#72). They were empty here for as long as
			// adoption only ran after a crash; sessions survive a restart now
			// (#63), so this runs on every redeploy and an operator was left
			// with a fleet of unnamed cards in unknown directories.
			//
			// Still empty for a session created before those options existed,
			// and that is correct: an invented value would describe nothing.
			// Neither field is ever used to build a tmux target — the id is.
			Name:    info.Label,
			WorkDir: info.WorkDir,
			// The name mode is derived from, restored the same way and empty on
			// the same terms. Empty is the daemon's default and therefore local,
			// which is the correct reading of a session started before the option
			// existed — not a failure, and not a reason to skip the adoption. A
			// session left unadopted over it would be an unowned unsandboxed
			// shell, which is the trade Principle VI never makes.
			StartCommand: info.StartCommand,
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

		// The token is generated and thrown away. What is kept is its hash, which
		// makes the session undrivable by anyone — including whoever held the
		// credential before the restart, which FR-021 requires — without holding
		// a live plaintext token from startup until somebody asks for it. That
		// storage is what FR-013 forbids, and it is why the real credential is
		// minted later, by ClaimPending, in the response that hands it over.
		_, hash, err := NewToken()
		if err != nil {
			failures = append(failures, fmt.Errorf("adopt session %s: %w", id, err))
			continue
		}
		s.TokenHash = hash
		s.CredentialPending = true

		if err := m.store.Add(s); err != nil {
			failures = append(failures, fmt.Errorf("adopt session %s: %w", id, err))
			continue
		}
		// Nobody is listening here: adoption runs at startup, before the
		// listener binds (T032). The emit is written anyway because the rule is
		// that a path which changes the fleet announces it — not that a path
		// whose events somebody happens to be reading does. A reconciliation run
		// with the daemon up would otherwise be the silent one.
		m.emit(FleetAppeared, s)
		adopted = append(adopted, AdoptedSession{Session: s})
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
// An error means the answer is unknown, and both callers treat unknown as
// surviving: Destroy keeps the record and reports an orphan, and a failed
// capture stays the transient "could not be read" it has always been.
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
	// The two facts the host would otherwise lose (#72). Adoption is the ordinary
	// path now that sessions survive a restart (#63), and a fleet of unnamed
	// cards in unknown directories is what an operator saw after every redeploy.
	//
	// The label goes on raw — ValidateName restricts it to letters, digits and
	// hyphens, so it can carry no separator and no shell metacharacter. The
	// working directory is base64 because a path can contain anything, including
	// the "|" that list-sessions uses between fields.
	if err := m.tmux.SetOption(ctx, name, tmuxctl.OptionName, s.Name); err != nil {
		return fmt.Errorf("record the session label: %w", err)
	}
	if err := m.tmux.SetOption(ctx, name, tmuxctl.OptionWorkDir, base64.StdEncoding.EncodeToString([]byte(s.WorkDir))); err != nil {
		return fmt.Errorf("record the session working directory: %w", err)
	}
	// The third fact the host would otherwise lose, and the one mode is derived
	// from (contracts/session-mode.md). It is the configured *name*, so what
	// survives a restart is a reference into the operator's own configuration
	// rather than a command line — a stored command line would be a command line
	// a restart could be made to run.
	//
	// Written even when it is empty, which is the daemon's default. Setting the
	// option to nothing and never setting it at all read back identically, and
	// the branch that skipped one of them would be a branch no adoption could
	// tell from the other.
	if err := m.tmux.SetOption(ctx, name, tmuxctl.OptionStart, s.StartCommand); err != nil {
		return fmt.Errorf("record the session start command: %w", err)
	}
	// The command is resolved from the name the record carries, not from the
	// request: by the time a session is being started its name has already been
	// checked against the configured set, and re-reading caller input here would
	// be a second place for that check to be missing.
	template, err := m.resolveStartCommand(s.StartCommand)
	if err != nil {
		return fmt.Errorf("resolve the start command for session %s: %w", s.ID, err)
	}
	// The session's own name goes into the command line here and nowhere else
	// (#58). Create has already refused anything RenderStartCommand would, so a
	// failure at this point is a record that reached start without passing
	// ValidateName — fail closed rather than type a line with a hole in it.
	command, err := config.RenderStartCommand(template, s.Name)
	if err != nil {
		return fmt.Errorf("render the start command for session %s: %w", s.ID, err)
	}
	if err := m.tmux.SendKeys(ctx, name, command, enterKey); err != nil {
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
		// The record is kept, so the fleet has gained a session — one nobody
		// holds a token for and the reaper will collect, but one the dashboard
		// renders until it does. A change only a reload would reveal is exactly
		// what this event source exists to stop.
		m.emit(FleetAppeared, s)
		return fmt.Errorf("create session %s: %w: %w",
			s.ID, ErrOrphanedSession, errors.Join(cause, killErr, verifyErr))
	}

	if delErr := m.store.Delete(s.ID); delErr != nil {
		return fmt.Errorf("create session %s: %w", s.ID, errors.Join(cause, delErr))
	}
	return fmt.Errorf("create session %s: %w", s.ID, cause)
}
