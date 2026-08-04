// Command crswd is the claude-remote-session-webhook daemon.
//
// Configuration is environment-only (CRSW_*), so no flags are defined here yet;
// flag.Parse still runs so -h reports usage rather than an unknown-flag error.
package main

import (
	"context"
	"errors"
	"flag"
	"log"
	"os/signal"
	"syscall"
	"time"

	"github.com/nctiggy/claude-remote-session-webhook/internal/config"
	"github.com/nctiggy/claude-remote-session-webhook/internal/httpapi"
)

// shutdownBudget is how long the daemon gives itself between a termination
// signal and exit: the drain, and then a verified teardown of every live
// session.
//
// It is well inside systemd's default TimeoutStopSec of 90 seconds on purpose.
// The one ending where sessions are certainly left running is a SIGKILL from the
// service manager, so the daemon must be finished — or have said loudly that it
// could not finish — before that escalation is due.
const shutdownBudget = 30 * time.Second

func main() {
	flag.Parse()

	// log rather than the audit trail: the trail is stdout and belongs to
	// requests, and this is what is left when there is no daemon yet to audit.
	// It carries an error built in this repo and never a secret (FR-043) —
	// config.Config redacts the shared secret in every format verb.
	if err := run(context.Background()); err != nil {
		log.Fatalf("crswd: %v", err)
	}
}

// run is the startup sequence, and its order is the requirement.
//
// Configuration first, because a missing or weak secret, a non-loopback listen
// address, or an unresolvable root is a startup failure and not a warning
// (docs/security.md §4) — nothing below runs on a Config that failed to load.
//
// Then reconciliation, before anything binds (FR-021). A tmux session this
// daemon started and then lost is a live shell running with
// --dangerously-skip-permissions and no owner, no idle deadline, and no ceiling;
// Reconcile is what gives it all three back, and anything already past 24 hours
// is destroyed rather than adopted (FR-025). It is fatal on failure, because a
// host the daemon cannot ask about is a host that may be carrying exactly those
// shells, and serving anyway would leave them unowned for the life of the
// process.
//
// Only then the listener. Every error on the way is returned rather than logged
// and survived: this is the one part of the daemon where refusing to start is
// still an option.
//
// Serving then runs until a termination signal arrives or the listener stops on
// its own, and the ending is Shutdown's: the requests in flight are drained and
// every live session is torn down with its teardown verified (FR-040). A
// teardown that could not be confirmed comes back as an error and ends in a
// non-zero exit, so the host's service manager records a stop that left
// unsandboxed shells behind rather than a clean one.
func run(ctx context.Context) error {
	// SIGINT as well as SIGTERM: an operator running this in a terminal presses
	// Ctrl-C, and a daemon that reaped its sessions under systemd but not under a
	// shell would leave the developer host carrying exactly what FR-040 exists to
	// prevent.
	ctx, stop := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	cfg, err := config.Load()
	if err != nil {
		return err
	}

	srv, err := httpapi.New(cfg)
	if err != nil {
		return err
	}

	if err := srv.Reconcile(ctx); err != nil {
		return err
	}

	// Before the listener binds, so that no session can exist without the sweep
	// that bounds it already running (FR-038). It stops when ctx does, which is
	// the same signal that begins shutdown — and shutdown tears down every
	// session, not only the expired ones, so there is no window where the two
	// disagree about who is responsible for the fleet.
	if err := srv.StartReaper(ctx); err != nil {
		return err
	}

	if err := srv.Listen(); err != nil {
		return err
	}

	serving := make(chan error, 1)
	go func() { serving <- srv.Serve() }()

	var serveErr error
	stoppedOnItsOwn := false
	select {
	case serveErr = <-serving:
		// The listener stopped without being asked to. There is nothing left to
		// drain, but the sessions it started are still on the host and nothing
		// else is coming for them, so shutdown runs anyway.
		stoppedOnItsOwn = true
	case <-ctx.Done():
		// Restore default signal handling before the slow part. A second SIGTERM
		// during a shutdown that is not making progress must kill the process,
		// not be swallowed by a handler that has already fired.
		stop()
	}

	// Not derived from ctx's cancellation: by now ctx is the thing that reported
	// the signal and is already done, and a teardown on a cancelled context would
	// exec no tmux command at all — the daemon would exit reporting a fleet it
	// never asked the host about.
	shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), shutdownBudget)
	defer cancel()

	shutdownErr := srv.Shutdown(shutdownCtx)
	if !stoppedOnItsOwn {
		// Shutdown has stopped the server, so Serve has returned or is about to.
		serveErr = <-serving
	}
	return errors.Join(serveErr, shutdownErr)
}
