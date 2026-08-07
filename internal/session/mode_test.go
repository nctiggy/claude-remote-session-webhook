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
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

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
	e, err := tmuxctl.NewExec(socket)
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
		modeLocalName:  "true",
		modeRemoteName: "true",
	}))
	return mgr, store
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
