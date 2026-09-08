package tmuxctl

// env.go is the half of the environment boundary that exec.go cannot do, and
// leaving it out makes exec.go's half cosmetic on every host that already exists.
//
// # Why one is not enough
//
// exec.go sets the environment of the tmux CLIENT this daemon runs. That is
// correct and it is what makes a freshly started server clean, because a tmux
// server takes its global environment from whichever client command happened to
// start it.
//
// But a server keeps that environment for its whole life, and this daemon's
// server outlives the daemon on purpose: startup adoption reclaims sessions
// across a restart, `Restart=always` brings the process back, and the self-update
// path exits deliberately so systemd can start the new binary. The process is
// replaced regularly; the server is not. So on every host that ran an older
// build, the server is still holding the environment that build gave it — on the
// reference host, twenty CRSW_ variables including the shared secret — and every
// new session created on it inherits them through the server rather than through
// the client this daemon just fixed.
//
// A fix that only did exec.go would therefore be correct on a new install and
// inert on every existing one, while looking identical from the outside. That is
// the shape of defect this project keeps finding, so it is named here rather
// than discovered again later.
//
// # The same defect, in the other direction
//
// The paragraph above was written about variables the server has and should not.
// It is equally true of variables the server lacks and should have, and that
// half was missed — with the result it predicts.
//
// On 2026-09-08 a fix gave every session CLAUDE_CODE_OAUTH_401_WAIT_MS by
// composing it into the client environment. It merged, released, and deployed,
// and did nothing: the reference host's server had been up since 12:18 and the
// daemon restarted at 15:17, so a session created at 15:29 still had no such
// variable. Measured on a throwaway server, both directions:
//
//   - server started WITHOUT the marker, new session created by a client that
//     HAS it -> the pane does NOT have it. The client environment reaches a new
//     session only by way of the server it started;
//   - server started WITH it -> the pane has it;
//   - after `set-environment -g MARKER value` on the running server, a NEWLY
//     created session's pane HAS it, and the sessions already there do not.
//
// So this function reconciles in both directions. A name the server should not
// have is unset, as before; a name it should have and does not, or holds a
// different value for, is set. Anything less makes every future addition to a
// session's environment inert on exactly the hosts that already run this daemon,
// which is the sentence above, and it was already true once.
//
// # What was measured
//
// On a throwaway server, with a marker variable in the environment that started
// it:
//
//   - the pane process HAS the marker — so the leak is real and travels through
//     the server's global table;
//   - `show-environment -t <session> MARKER` answers "unknown variable" — the
//     SESSION table does not hold it, so looking there would have reported a
//     clean host that was not;
//   - after `set-environment -g -u MARKER`, a NEWLY created session's pane does
//     not have it.
//
// # What it cannot do
//
// A process's environment cannot be changed from outside it. Sessions already
// running keep what they were started with until they are recreated, and nothing
// in this file reaches them. That limit is documented for the operator in
// deploy/README.md rather than papered over — a host reported as fixed while
// still leaking is worse than one reported as leaking.

import (
	"context"
	"fmt"
	"slices"
	"strings"
)

// Reconciliation is what one pass changed, so a caller can report it and a test
// can assert on it.
//
// The two are kept apart rather than summed because they are different events to
// an operator reading a trail: Removed is an older build's leak being cleaned
// up, and Set is a value this build intends that the server did not have. A
// single count would make the second invisible behind the first.
type Reconciliation struct {
	// Removed are names unset because a session should not receive them.
	Removed []string

	// Set are names given the value this daemon composes, because the server
	// either lacked them or held something else.
	Set []string
}

// Empty reports whether the pass changed nothing.
func (r Reconciliation) Empty() bool { return len(r.Removed) == 0 && len(r.Set) == 0 }

// ReconcileServerEnvironment makes the tmux server's global environment match
// the one this daemon composes for a session.
//
// Both directions, and the second is not decoration: a tmux server hands its
// global table to every session created on it, so a variable this daemon added
// after the server started can reach a new session no other way. See the note
// above for the deploy where exactly that happened.
//
// It is called once at startup, before any session is created, so that a server
// adopted from an older build stops handing its environment to the sessions
// created next and starts handing them this build's.
//
// A server that is not running is not an error: there is nothing to reconcile,
// and the first session created will start one from an already-correct client.
func (e *Exec) ReconcileServerEnvironment(ctx context.Context) (Reconciliation, error) {
	var done Reconciliation

	if e.socket == "" {
		return done, ErrNoSocket
	}
	if len(e.sessionEnv) == 0 {
		return done, ErrNoSessionEnv
	}

	stdout, stderr, err := e.run(ctx, []string{"tmux", "show-environment", "-g"}, nil)
	if err != nil {
		// No server yet, which is the common case on a cold start. tmux says so
		// on stderr and exits non-zero; there is nothing to reconcile — the first
		// session created will start a server from an already-correct client.
		if noServer(err, stderr) {
			return done, nil
		}
		return done, fmt.Errorf("tmux show-environment -g: %w", withStderr(err, stderr))
	}

	want := make(map[string]string, len(e.sessionEnv))
	for _, kv := range e.sessionEnv {
		if name, value, found := strings.Cut(kv, "="); found {
			want[name] = value
		}
	}

	// The server's side, read once. An explicitly-unset name (tmux prints it as
	// `-NAME`) is absent from what a session receives, so it is treated as absent
	// here — which is what makes such a name eligible to be set below rather than
	// mistaken for one already correct.
	have := make(map[string]string, len(want))
	for _, line := range strings.Split(stdout, "\n") {
		if name := globalName(line); name != "" {
			_, value, _ := strings.Cut(strings.TrimSpace(line), "=")
			have[name] = value
		}
	}

	// Removals first, and sorted, so that a failure part-way leaves the server
	// strictly closer to correct rather than in an order nothing can describe.
	// Sorting is for the caller and the tests: an operator reading "removed 20
	// variables" wants the same list twice, and a map's order is not one.
	for _, name := range sortedNames(have) {
		if _, keep := want[name]; keep {
			continue
		}
		if _, stderr, err := e.run(ctx, []string{"tmux", "set-environment", "-g", "-u", name}, nil); err != nil {
			return done, fmt.Errorf("tmux set-environment -g -u %s: %w", name, withStderr(err, stderr))
		}
		done.Removed = append(done.Removed, name)
	}

	for _, name := range sortedNames(want) {
		if current, present := have[name]; present && current == want[name] {
			continue
		}
		// The value reaches tmux as its own argv element, so a value containing
		// spaces or an `=` is carried literally rather than re-parsed. It is
		// visible in `ps` for the moment this runs, which is why nothing secret
		// may be in a composed session environment in the first place —
		// config.SessionEnvironment's excluded() is what makes that true, and
		// this is one of the places relying on it.
		if _, stderr, err := e.run(ctx, []string{"tmux", "set-environment", "-g", name, want[name]}, nil); err != nil {
			return done, fmt.Errorf("tmux set-environment -g %s: %w", name, withStderr(err, stderr))
		}
		done.Set = append(done.Set, name)
	}
	return done, nil
}

// sortedNames is the keys of m in a stable order.
func sortedNames(m map[string]string) []string {
	names := make([]string, 0, len(m))
	for name := range m {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}

// globalName is the variable name on one line of `show-environment -g`, or ""
// for a line that names none.
//
// tmux prints `NAME=value` for a variable that is set and `-NAME` for one that
// is explicitly unset. The second is already absent from what a session would
// receive, so removing it again would be a no-op that the caller would
// nonetheless report as work done.
func globalName(line string) string {
	line = strings.TrimSpace(line)
	if line == "" || strings.HasPrefix(line, "-") {
		return ""
	}
	name, _, found := strings.Cut(line, "=")
	if !found {
		return ""
	}
	return name
}

// The "there is no server" case is exec.go's noServer, reused rather than
// written again here. A second answer to "is tmux running" would be free to
// disagree with the one List already gives on the same host, and the shape of
// that disagreement is a reconciliation silently skipped on a server that is
// really there.
