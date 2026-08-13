//go:build tmux

package tmuxctl

// The reconciliation in env.go, driven against a real tmux server, because the
// behaviour it depends on is tmux's and not this package's: a server keeps the
// environment of whichever client started it, hands that to every session
// created afterwards, and lets it be removed with `set-environment -g -u`.
//
// A stub could assert the argv this package sends. Only a real server can say
// whether sending it actually stops a secret reaching a pane, which is the only
// question worth asking here.

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// leakedName is a stand-in for CRSW_SHARED_SECRET: a name a composed session
// environment does not admit, so reconciliation must remove it.
const leakedName = "CRSW_TEST_LEAKED_SECRET"

const leakedValue = "a-value-no-session-may-see"

// startDirtyServer starts a tmux server the way an older build of this daemon
// did — with its own whole environment — so the test begins from the state every
// existing deployment is really in.
func startDirtyServer(t *testing.T, e *Exec) {
	t.Helper()

	cmd := exec.Command("tmux", "-L", e.socket, "new-session", "-d", "-s", "dirty", "sleep 300") //nolint:gosec // socket is socketFor(t.Name())
	cmd.Env = append(os.Environ(), leakedName+"="+leakedValue)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("start a server holding %s: %v: %s", leakedName, err, out)
	}
}

// paneEnvironment runs `env` inside a NEW session on the server and returns what
// that pane's process actually had.
//
// The pane's own process is the only thing worth asking. `show-environment -t`
// reads the SESSION table, which does not hold what the server's global table
// carries — during this feature's research that difference reported a clean host
// that was not.
func paneEnvironment(t *testing.T, e *Exec, session string) string {
	t.Helper()

	dir := t.TempDir()
	out := filepath.Join(dir, "env.txt")

	if err := e.New(context.Background(), session, dir); err != nil {
		t.Fatalf("new-session %s: %v", session, err)
	}
	if err := e.SendKeys(context.Background(), session, "env > "+out, "Enter"); err != nil {
		t.Fatalf("send-keys to %s: %v", session, err)
	}

	waitFor(t, "the pane to write its environment", func() bool {
		info, err := os.Stat(out)
		return err == nil && info.Size() > 0
	})

	raw, err := os.ReadFile(out) //nolint:gosec // path is under t.TempDir()
	if err != nil {
		t.Fatalf("read the pane's environment: %v", err)
	}
	return string(raw)
}

// TestReconcileStopsAnAdoptedServerLeaking is the R2 finding, asserted.
//
// **Must fail when** the reconciliation is dropped and only exec.go's cmd.Env
// remains. That combination is the one that looks correct on a fresh install and
// is inert on every host that already exists: the server keeps the environment
// an older build gave it, and every session created on it inherits the secret
// through the server rather than through the client this daemon just fixed.
func TestReconcileStopsAnAdoptedServerLeaking(t *testing.T) {
	e := newTestExec(t)
	startDirtyServer(t, e)

	// The premise. If this ever stops holding, the rest of the test proves
	// nothing and would pass for the wrong reason.
	if got := paneEnvironment(t, e, "before"); !strings.Contains(got, leakedName) {
		t.Fatalf("a session on a dirty server did not carry %s, so this test cannot show that reconciliation removes it", leakedName)
	}

	removed, err := e.ReconcileServerEnvironment(context.Background())
	if err != nil {
		t.Fatalf("ReconcileServerEnvironment: %v", err)
	}
	if !containsName(removed, leakedName) {
		t.Errorf("reconciliation did not report removing %s; it reported %v", leakedName, removed)
	}

	if got := paneEnvironment(t, e, "after"); strings.Contains(got, leakedName) {
		t.Errorf("a session created after reconciliation still receives %s", leakedName)
	}
}

// TestReconcileLeavesRunningSessionsAlone pins data-model V12, and states the
// limit in V13 as a fact rather than leaving it to be discovered.
//
// A process's environment cannot be changed from outside it. The session started
// before reconciliation keeps what it was given, and this test asserts that
// rather than pretending otherwise — a host reported as fixed while a pane on it
// is still holding the secret is worse than one reported as leaking.
func TestReconcileLeavesRunningSessionsAlone(t *testing.T) {
	e := newTestExec(t)
	startDirtyServer(t, e)

	before, err := e.List(context.Background())
	if err != nil {
		t.Fatalf("List before: %v", err)
	}

	if _, err := e.ReconcileServerEnvironment(context.Background()); err != nil {
		t.Fatalf("ReconcileServerEnvironment: %v", err)
	}

	after, err := e.List(context.Background())
	if err != nil {
		t.Fatalf("List after: %v", err)
	}
	if len(before) != len(after) {
		t.Errorf("reconciliation changed the session count from %d to %d; it must remove variables, never sessions", len(before), len(after))
	}
}

// TestReconcileKeepsWhatASessionNeeds is the other direction. A reconciliation
// that emptied the global table would produce sessions with no PATH and no HOME,
// which start and then fail in ways that look like a bug in tmux.
func TestReconcileKeepsWhatASessionNeeds(t *testing.T) {
	e := newTestExec(t)
	startDirtyServer(t, e)

	if _, err := e.ReconcileServerEnvironment(context.Background()); err != nil {
		t.Fatalf("ReconcileServerEnvironment: %v", err)
	}

	got := paneEnvironment(t, e, "usable")
	for _, name := range []string{"HOME=", "PATH="} {
		if !strings.Contains(got, name) {
			t.Errorf("a session after reconciliation has no %s", strings.TrimSuffix(name, "="))
		}
	}
}

// TestReconcileOnNoServerIsNotAnError is the cold start, which is most starts.
func TestReconcileOnNoServerIsNotAnError(t *testing.T) {
	e := newTestExec(t)

	removed, err := e.ReconcileServerEnvironment(context.Background())
	if err != nil {
		t.Errorf("ReconcileServerEnvironment with no server running: %v", err)
	}
	if len(removed) != 0 {
		t.Errorf("removed %v from a server that does not exist", removed)
	}
}

func containsName(names []string, want string) bool {
	for _, name := range names {
		if name == want {
			return true
		}
	}
	return false
}
