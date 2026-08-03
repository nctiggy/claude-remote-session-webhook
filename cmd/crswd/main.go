// Command crswd is the claude-remote-session-webhook daemon.
//
// Configuration is environment-only (CRSW_*), so no flags are defined here yet;
// flag.Parse still runs so -h reports usage rather than an unknown-flag error.
package main

import (
	"context"
	"flag"
	"log"

	"github.com/nctiggy/claude-remote-session-webhook/internal/config"
	"github.com/nctiggy/claude-remote-session-webhook/internal/httpapi"
)

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
// Graceful shutdown — draining the listener and tearing every live session down
// with its teardown verified — is T037's, and is why Serve is the last call here
// rather than the only one.
func run(ctx context.Context) error {
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
	if err := srv.Listen(); err != nil {
		return err
	}
	return srv.Serve()
}
