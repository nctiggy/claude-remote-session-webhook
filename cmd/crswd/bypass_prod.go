//go:build !dev

// The shipping artifact's startup: the configuration the daemon always demands,
// and the server with layer 1 in front of the dashboard. There is no bypass
// here, no flag that could ask for one, and no symbol naming one — a production
// artifact that can disable authentication by flag is a backdoor, and
// docs/auth-and-sessions.md says it in those words (FR-041).
//
// Its counterpart, bypass_dev.go, carries //go:build dev and defines the
// -dev-auth-bypass flag. Nothing here mentions that flag, so this build does not
// merely ignore it: flag.Parse has never heard of it, and an operator who tries
// it gets the usage message and a non-zero exit. That is SC-012's "requested by
// any means" — the request has nowhere to land.
//
// bypass_build_test.go asserts both halves, in the build that ships.

package main

import (
	"github.com/nctiggy/claude-remote-session-webhook/internal/config"
	"github.com/nctiggy/claude-remote-session-webhook/internal/httpapi"
)

func loadConfig() (*config.Config, error) { return config.Load() }

func newDaemon(cfg *config.Config) (*httpapi.Server, error) { return httpapi.New(cfg) }
