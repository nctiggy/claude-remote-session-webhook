//go:build dev

// The development artifact's startup, and the only place the -dev-auth-bypass
// flag exists (US5, FR-038–FR-042).
//
// This is the half internal/httpapi/server.go anticipated and internal/access
// was built for: without it the bypass is a type nothing constructs, and US5 is
// a story with no artifact behind it. What it adds is a flag on a build that
// never ships — bypass_prod.go is the shipping half, and it does not define the
// flag, so the shipping artifact cannot be asked for the bypass at all.
//
// The flag is required rather than the bypass being implied by the build tag. A
// developer building with -tags dev to run the tagged tests, or to reproduce a
// report, gets a daemon that authenticates exactly as the shipping one does; the
// bypass takes an explicit request, in the same session, on the command line.

package main

import (
	"flag"
	"os"

	"github.com/nctiggy/claude-remote-session-webhook/internal/config"
	"github.com/nctiggy/claude-remote-session-webhook/internal/httpapi"
)

// devAuthBypass is quickstart.md's --dev-auth-bypass. Go's flag package accepts
// one dash or two for the same name, which is why the document may spell it with
// two and this may declare it with one.
var devAuthBypass = flag.Bool("dev-auth-bypass", false,
	"skip the browser identity check (development builds only): every browser reaching the listener is admitted as the operator")

// loadConfig stops demanding the three layer-1 values when the bypass is active,
// and demands them exactly as the shipping build does when it is not (FR-042).
//
// Demanding an audience and a team domain the bypass then ignores would make
// developing the dashboard on a laptop need a Cloudflare account, which is the
// thing US5 exists to avoid. Everything else config.Load enforces is untouched:
// the shared secret, the approved roots, the caps and the loopback listener are
// the bounds standing in for the permission prompt, and this build has no more
// licence to widen them than any other.
func loadConfig() (*config.Config, error) {
	if *devAuthBypass {
		return config.Load(append(configOptions(), config.WithAccessBypassActive())...)
	}
	return config.Load(configOptions()...)
}

// newDaemon builds the server with the bypass in place of layer 1 when it was
// asked for, and with the real validator when it was not.
//
// os.Stderr is where the bypass announces itself, on every request (FR-040),
// which is the same stream config.Load's default-root warning uses and the same
// journal an operator is already reading. It is deliberately not the audit
// trail: the trail belongs to requests and records decisions, and this is the
// daemon saying what it is.
func newDaemon(cfg *config.Config) (*httpapi.Server, error) {
	if *devAuthBypass {
		return httpapi.NewWithBypass(cfg, os.Stderr)
	}
	return httpapi.New(cfg)
}
