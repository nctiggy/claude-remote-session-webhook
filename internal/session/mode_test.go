//go:build tmux

package session

// The only tests in this package that need a real tmux binary. They are
// excluded from `go test ./...` and run with:
//
//	go test -tags tmux ./internal/session
//
// They exist because the property under test is a round trip through tmux
// itself: a name written as a user option by one process and read back by the
// next one out of a format string. The fake models that round trip, but the
// fake is also where a format-string mistake would be invisible — it interpolates
// the same constants it reads. Only a real tmux can say the option survived.
//
// Everything runs on a private server socket, never tmux's default, so the
// kill-server in the cleanup can only ever reach sessions these tests made
// (#22). None of them call t.Parallel.

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nctiggy/claude-remote-session-webhook/internal/auth"
	"github.com/nctiggy/claude-remote-session-webhook/internal/config"
	"github.com/nctiggy/claude-remote-session-webhook/internal/tmuxctl"
)

// The two configured names these tests start sessions with. Neither runs
// Claude: what is under test is the name that survives, not what the name
// resolves to, and a suite that typed the daemon's own default into a real
// shell would start an unsandboxed assistant on whatever host ran it.
const (
	modeLocalName  = "default"
	modeRemoteName = "rc"
)

// What those two names run here, and both are chosen for what they leave in the
// pane rather than for what they do.
//
// The local one prints far more lines than a window is tall, so the first of
// them is in the *scrollback* rather than on the screen — which is what makes
// TestTogglePreservesSessionAndScrollback assert the thing it is named for
// instead of asserting that a screen was not cleared.
//
// The remote one is an echo because the transition appends --continue to
// whatever it restarts. A command that parsed its arguments would fail on the
// flag and print nothing, and the test would then be waiting for output that a
// correct implementation never produces.
const (
	modeLocalCommand  = "seq -f " + localScrollbackLine + " 1 200"
	modeRemoteCommand = "echo " + remoteMarker

	// localScrollbackLine is seq's format, so the pane fills with
	// crswd-local-1 … crswd-local-200. The first is what must survive the
	// transition and the last is how a test knows the command finished.
	localScrollbackLine = "crswd-local-%g"
	firstLocalLine      = "crswd-local-1"
	lastLocalLine       = "crswd-local-200"

	remoteMarker = "crswd-remote-marker"
)

// modeFixture is a Manager on a real tmux server and the real work-dir fixture.
// The store is separate from the Manager so a second one can be built on the
// same host with an empty store, which is what a restart looks like from
// adoption's side.
type modeFixture struct {
	workDirFixture

	tmux   *tmuxctl.Exec
	socket string
}

func newModeFixture(t *testing.T) modeFixture {
	t.Helper()

	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skipf("tmux is not installed: %v", err)
	}

	socket := modeSocketFor(t.Name())
	// The daemon's own bound, because these tests drive a real tmux and a screen
	// it renders is exactly what production captures.
	e, err := tmuxctl.NewExec(socket, config.DefaultPaneBound, config.SessionEnvironment(os.Environ(), nil))
	if err != nil {
		t.Fatalf("NewExec(%q): %v", socket, err)
	}
	t.Cleanup(func() {
		// Constant argv, and -L keeps this off the operator's own server.
		out, err := exec.Command("tmux", "-L", socket, "kill-server").CombinedOutput() //nolint:gosec // socket is modeSocketFor(t.Name())
		if err != nil && !strings.Contains(string(out), "no server running") &&
			!strings.Contains(string(out), "error connecting to") {
			t.Logf("cleanup kill-server: %v: %s", err, out)
		}
	})

	return modeFixture{workDirFixture: newWorkDirFixture(t), tmux: e, socket: socket}
}

// manager builds a daemon on this host with a store of its own. Two of them is
// a restart: the tmux server outlives the process, and the second one starts
// knowing nothing but what the host can tell it.
//
// The clock is the real one, unlike every fixture in manager_test.go, because
// tmux stamps #{session_created} from the host clock and adoption compares that
// against the daemon's. A stopped clock would make every real session look
// either newborn or long expired depending which way the two disagreed.
func (f modeFixture) manager(t *testing.T) (*Manager, *Store) {
	t.Helper()

	store := NewStore()
	mgr, err := NewManager(f.tmux, store, f.roots(), capNotUnderTest)
	if err != nil {
		t.Fatalf("NewManager() unexpected error: %v", err)
	}
	mgr.SetStartCommands(config.NewStartCommands(map[string]string{
		modeLocalName:  modeLocalCommand,
		modeRemoteName: modeRemoteCommand,
	}))
	// Which name means remote, the way httpapi's own wiring tells the manager
	// (#58). Without it every transition below would be refused for want of a
	// remote mode to move to, rather than tested.
	mgr.SetRemoteControlCommand(modeRemoteName)
	return mgr, store
}

// tmuxSays runs one read-only tmux command on this fixture's private server and
// returns its output. Every argument is a constant or a name derived from a
// minted id, so nothing here builds a target out of caller text.
func (f modeFixture) tmuxSays(t *testing.T, args ...string) string {
	t.Helper()

	out, err := exec.Command("tmux", append([]string{"-L", f.socket}, args...)...).CombinedOutput() //nolint:gosec // socket is modeSocketFor(t.Name())
	if err != nil {
		t.Fatalf("tmux %v: %v: %s", args, err, out)
	}
	return string(out)
}

// onScreen is what a client would see right now — the visible pane and no
// history — which is the reading that tells a scrolled-off line from a lost one.
func (f modeFixture) onScreen(t *testing.T, s Session) string {
	t.Helper()
	return f.tmuxSays(t, "capture-pane", "-p", "-t", s.PaneTarget())
}

// withScrollback is the visible pane *and* everything above it, which is the
// half of FR-028 a capture of the screen alone cannot see.
func (f modeFixture) withScrollback(t *testing.T, s Session) string {
	t.Helper()
	return f.tmuxSays(t, "capture-pane", "-p", "-S", "-", "-t", s.PaneTarget())
}

// paneShell is the process id of the shell the pane is running, which is the one
// value a transition that rebuilt anything cannot preserve.
//
// It is deliberately not tmux's own #{session_id}. That was the first thing this
// test compared and it is worthless here, as running the mutation showed: these
// fixtures hold a single session on a private server, so killing it stops the
// server, and the session created next is numbered $0 all over again — the check
// passed against a destroy-and-recreate. A pid does not restart, and it catches
// the near miss as well: respawn-pane keeps the session and still hands the pane
// a new shell.
func (f modeFixture) paneShell(t *testing.T, s Session) string {
	t.Helper()
	return strings.TrimSpace(f.tmuxSays(t, "display-message", "-p", "-t", s.PaneTarget(), "#{pane_pid}"))
}

// containsLine reports whether page holds want as a whole line.
//
// Substring matching is wrong for exactly the reason it looks right here:
// "crswd-local-1" is inside "crswd-local-179", so a scrollback assertion written
// with strings.Contains would pass on a pane that had lost the line it is
// looking for and kept a later one.
func containsLine(page, want string) bool {
	for _, line := range strings.Split(page, "\n") {
		if strings.TrimSpace(line) == want {
			return true
		}
	}
	return false
}

// waitForPane blocks until the pane shows want, which is how a test written
// against a real terminal waits for a command it typed rather than for a call it
// made. The deadline is generous because what is being waited for is a shell
// starting on whatever host is running this.
func (f modeFixture) waitForPane(t *testing.T, s Session, want string) {
	t.Helper()

	deadline := time.Now().Add(15 * time.Second)
	for {
		if strings.Contains(f.onScreen(t, s), want) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("the pane never showed %q; it holds:\n%s", want, f.withScrollback(t, s))
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// repo is an ordinary working directory under the first approved root, spelled
// the way managerFixture spells it. It is restated rather than shared because
// that one hangs off managerFixture, which carries the tmux fake this file
// exists to avoid.
func (f modeFixture) repo() string { return filepath.Join(f.root, "repo") }

// modeSocketFor makes a test name usable as the filename tmux turns -L into.
func modeSocketFor(name string) string {
	return "crswd-test-" + strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			return r
		default:
			return '-'
		}
	}, name)
}

// toggled is a live local session that has finished printing, moved to remote,
// and finished printing again — the whole of the transition, with the pane read
// at each end. Every test below asserts something different about the same run,
// so it is one helper rather than three copies of a sequence that takes real
// seconds.
//
// The scrollback is what makes this fixture worth the wait: 200 lines into a
// window 24 rows tall means firstLocalLine is above the screen before the toggle
// runs, so a transition that reset the pane and one that scrolled it are
// distinguishable afterwards.
func (f modeFixture) toggled(t *testing.T) toggleRun {
	t.Helper()

	ctx := context.Background()
	mgr, _ := f.manager(t)

	live, _, err := mgr.Create(ctx, CreateRequest{
		Owner:        auth.CallerOperator,
		Name:         "mode-transition",
		WorkDir:      f.repo(),
		StartCommand: modeLocalName,
	})
	if err != nil {
		t.Fatalf("Create() unexpected error: %v", err)
	}
	f.waitForPane(t, *live, lastLocalLine)

	// Asserted before the transition rather than assumed by it: if the local
	// command's output still fit on the screen, everything below would be a test
	// of a screen that was not cleared, which is a weaker claim than FR-028's.
	if containsLine(f.onScreen(t, *live), firstLocalLine) {
		t.Fatalf("%q is still on the visible screen, so nothing has scrolled and this fixture cannot tell scrollback from screen:\n%s",
			firstLocalLine, f.onScreen(t, *live))
	}

	// Read here, while the pane is still running the shell the create started.
	shell := f.paneShell(t, *live)

	moved, err := mgr.SetMode(ctx, *live, ModeRemote)
	if err != nil {
		t.Fatalf("SetMode() unexpected error: %v", err)
	}
	f.waitForPane(t, moved, remoteMarker)

	return toggleRun{before: *live, after: moved, shell: shell}
}

// toggleRun is what one transition leaves a test to assert on: the record either
// side of it, and the pane's shell process **read before it ran**.
//
// That last part is the whole reason this is a struct. Reading the host after the
// transition for both ends would compare a value with itself, and a
// destroy-and-recreate — the exact defect the test is named for — would answer
// the rebuilt pane to both reads and pass.
type toggleRun struct {
	before, after Session
	shell         string
}

// FR-028 and SC-007 against a real terminal, which is the only place the claim
// can be made: the session, its window and its scrollback are the same ones
// after the transition as before it.
//
// **Must fail when** the implementation destroys and recreates the session.
// tmux's own #{session_id} is what catches that — a rebuilt session under the
// same name is a different one — and the scrollback catches the near miss the
// probe for this task actually found: respawn-pane -k ends the process in place,
// keeps the session, and hands back an empty screen with the conversation gone.
func TestTogglePreservesSessionAndScrollback(t *testing.T) {
	f := newModeFixture(t)

	run := f.toggled(t)

	if got := f.paneShell(t, run.after); got != run.shell {
		t.Errorf("the pane is running shell %s, want the %s it started with — the transition rebuilt the session rather than restarting what was inside it",
			got, run.shell)
	}
	if got := f.tmuxSays(t, "list-windows", "-t", run.after.SessionTarget()); strings.Count(got, "\n") != 1 {
		t.Errorf("the session has these windows:\n%s\nwant the one it started with", got)
	}
	if page := f.withScrollback(t, run.after); !containsLine(page, firstLocalLine) {
		t.Errorf("%q is gone from the scrollback, so the conversation an operator was reading did not survive the toggle:\n%s",
			firstLocalLine, page)
	}
}

// SC-007's other half: the restarted command resumes rather than starts over.
//
// **Must fail when** the flag is omitted. It is asserted off the pane rather
// than off an argv because that is where a shell records what it was asked to
// run — and because the argv assertion already exists in the untagged suite,
// against the fake, where it cannot show that a real tmux delivered the line
// intact.
func TestTogglePassesContinue(t *testing.T) {
	f := newModeFixture(t)

	page := f.withScrollback(t, f.toggled(t).after)
	if !strings.Contains(page, remoteMarker+" --continue") {
		t.Errorf("the restarted command in the pane is not the remote one with --continue:\n%s", page)
	}
}

// The third thing a transition may not disturb, read off the daemon rather than
// off the host: same record, different mode.
//
// **Must fail when** a toggle mints a new record. The untagged suite asserts the
// same fields against the fake; what this adds is that they still hold when the
// transition ran against a real tmux, where a rebuilt session is the shape the
// implementation would most plausibly take.
func TestToggleKeepsIdentifierAndLifetime(t *testing.T) {
	f := newModeFixture(t)

	run := f.toggled(t)
	before, after := run.before, run.after

	switch {
	case after.ID != before.ID:
		t.Errorf("the session is now %q, want %q — a mode change is not a new session", after.ID, before.ID)
	case after.TokenHash != before.TokenHash:
		t.Error("the credential hash changed; the token its owner holds no longer opens the session they still have")
	case !after.CreatedAt.Equal(before.CreatedAt):
		t.Errorf("CreatedAt moved to %v from %v, which restarts the absolute ceiling", after.CreatedAt, before.CreatedAt)
	case !after.AbsoluteDeadline().Equal(before.AbsoluteDeadline()):
		t.Errorf("the absolute deadline moved to %v from %v", after.AbsoluteDeadline(), before.AbsoluteDeadline())
	case after.Mode(modeRemoteName) != ModeRemote:
		t.Errorf("the session reports mode %q, want %q — the one thing the transition is for", after.Mode(modeRemoteName), ModeRemote)
	}
}

// FR-026: the start-command name is what mode is derived from, so a restart
// that lost it would move every remote session back to local without anyone
// asking. The name is written as @crswd-start at create and read back by the
// next daemon's adoption — this asserts the whole round trip through a real
// tmux, which is the only place a wrong format string shows up.
func TestStartCommandSurvivesRestart(t *testing.T) {
	ctx := context.Background()
	f := newModeFixture(t)

	before, _ := f.manager(t)
	s, _, err := before.Create(ctx, CreateRequest{
		Owner:        auth.CallerOperator,
		Name:         "mode-probe",
		WorkDir:      f.repo(),
		StartCommand: modeRemoteName,
	})
	if err != nil {
		t.Fatalf("Create() unexpected error: %v", err)
	}

	// The restart. A second Manager on the same host with a store that has never
	// heard of this session is exactly what the daemon looks like after systemd
	// has restarted it (#63).
	after, _ := f.manager(t)
	adopted, err := after.Adopt(ctx)
	if err != nil {
		t.Fatalf("Adopt() unexpected error: %v", err)
	}
	if len(adopted) != 1 {
		t.Fatalf("Adopt() took back %d sessions, want 1", len(adopted))
	}

	restored := adopted[0].Session
	if restored.ID != s.ID {
		t.Fatalf("Adopt() restored session %q, want %q", restored.ID, s.ID)
	}
	if restored.StartCommand != modeRemoteName {
		t.Errorf("restored StartCommand = %q, want %q — @crswd-start did not survive the restart",
			restored.StartCommand, modeRemoteName)
	}

	// What the surviving name is for: the mode is derived from it, so a restart
	// that kept the option but lost the reading would be the same outcome to an
	// operator looking at the card.
	if got := restored.Mode(modeRemoteName); got != ModeRemote {
		t.Errorf("restored Mode() = %q, want %q", got, ModeRemote)
	}
}

// A session started before @crswd-start existed carries no such option, and
// show-options renders an unset user option as the empty string. That empty
// name is the daemon's default, which is local — the correct reading of a
// session this daemon started under an older build.
//
// It must not be a failure, and it must not cost the session its adoption:
// FR-021's whole point is that a live session the running daemon has forgotten
// is an unowned unsandboxed shell, and the mode it is displayed with is not
// worth one.
func TestRestoredSessionWithoutOptionIsLocal(t *testing.T) {
	ctx := context.Background()
	f := newModeFixture(t)

	// Built by hand rather than through Create, because Create is now the thing
	// that writes the fifth option. This is the older daemon's session: the
	// reserved prefix, a minted id, provenance — and nothing else.
	id, err := NewID()
	if err != nil {
		t.Fatalf("NewID(): %v", err)
	}
	name := tmuxNamePrefix + id
	if err := f.tmux.New(ctx, name, f.repo()); err != nil {
		t.Fatalf("New(%q): %v", name, err)
	}
	if err := f.tmux.SetOption(ctx, name, tmuxctl.OptionManaged, tmuxctl.OptionManagedValue); err != nil {
		t.Fatalf("SetOption(%s): %v", tmuxctl.OptionManaged, err)
	}

	mgr, _ := f.manager(t)
	adopted, err := mgr.Adopt(ctx)
	if err != nil {
		t.Fatalf("Adopt() refused a session with no %s: %v", tmuxctl.OptionStart, err)
	}
	if len(adopted) != 1 {
		t.Fatalf("Adopt() took back %d sessions, want 1 — absence of %s cost the session its adoption",
			len(adopted), tmuxctl.OptionStart)
	}
	if got := adopted[0].Session.StartCommand; got != "" {
		t.Errorf("restored StartCommand = %q, want %q — an absent option was read as a value", got, "")
	}
	if got := adopted[0].Session.Mode(modeRemoteName); got != ModeLocal {
		t.Errorf("restored Mode() = %q, want %q — a session from before this option is local, not an error", got, ModeLocal)
	}
}
