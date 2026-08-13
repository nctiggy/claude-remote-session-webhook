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
	"strings"
)

// ReconcileServerEnvironment removes from the tmux server's global environment
// every variable this daemon would not put in a session's.
//
// It is called once at startup, before any session is created, so that a server
// adopted from an older build stops handing its environment to the sessions
// created next.
//
// A server that is not running is not an error: there is nothing to reconcile,
// and the first session created will start one from an already-correct client.
func (e *Exec) ReconcileServerEnvironment(ctx context.Context) (removed []string, err error) {
	if e.socket == "" {
		return nil, ErrNoSocket
	}
	if len(e.sessionEnv) == 0 {
		return nil, ErrNoSessionEnv
	}

	stdout, stderr, err := e.run(ctx, []string{"tmux", "show-environment", "-g"}, nil)
	if err != nil {
		// No server yet, which is the common case on a cold start. tmux says so
		// on stderr and exits non-zero; there is nothing to clean and nothing to
		// report.
		if noServer(err, stderr) {
			return nil, nil
		}
		return nil, fmt.Errorf("tmux show-environment -g: %w", withStderr(err, stderr))
	}

	keep := make(map[string]bool, len(e.sessionEnv))
	for _, kv := range e.sessionEnv {
		if name, _, found := strings.Cut(kv, "="); found {
			keep[name] = true
		}
	}

	for _, line := range strings.Split(stdout, "\n") {
		name := globalName(line)
		if name == "" || keep[name] {
			continue
		}
		if _, stderr, err := e.run(ctx, []string{"tmux", "set-environment", "-g", "-u", name}, nil); err != nil {
			return removed, fmt.Errorf("tmux set-environment -g -u %s: %w", name, withStderr(err, stderr))
		}
		removed = append(removed, name)
	}
	return removed, nil
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
