//go:build dev

// Compiled only into a development build, for the reason
// internal/access/bypass_prod.go gives: a bypass excluded at build time is the
// requirement (FR-041), and a bypass merely defaulted off is a switch.
//
// There is no shipping half of this file. internal/access needs one because the
// pair documents an absence in the package that defines the bypass; here the
// absence is the whole file, and a !dev counterpart would exist only to declare
// nothing.

package httpapi

import (
	"errors"
	"io"
	"os"

	"github.com/nctiggy/claude-remote-session-webhook/internal/access"
	"github.com/nctiggy/claude-remote-session-webhook/internal/audit"
	"github.com/nctiggy/claude-remote-session-webhook/internal/config"
	"github.com/nctiggy/claude-remote-session-webhook/internal/tmuxctl"
)

// doorSentenceBypassed is what the settings page says on this build (M12/T006),
// and it lives here rather than beside the other three because it is the one
// sentence the shipping build must not even carry the words of.
//
// It says what is true and does not soften it. The bypass is layer 1 for a
// developer on a laptop with no Cloudflare Access to sign anything, and what it
// verifies is nothing at all — so this is the one daemon where "which door is
// live" has the answer "none, and every browser is admitted". access.Bypass
// already announces itself on every request (FR-040) and the masthead above this
// sentence names the operator NOBODY; this is the same fact where an operator
// goes to read facts.
const doorSentenceBypassed = "This dashboard's layer 1 is bypassed: this is a development build and it verifies nobody."

// NewWithBypass is New with layer 1 replaced by the development bypass: the
// daemon a developer runs on a laptop, where there is no Cloudflare Access to
// sign anything (US5, FR-038).
//
// Replaced, not accompanied. It goes in at the point verifiedLayer1 goes in for
// every other build, so there is still exactly one layer 1 behind the browser
// door and no request can meet two — the arrangement that makes "skips layer 1
// only" checkable rather than asserted. Layers 2 and 3 are untouched: the API
// door still verifies a signature, and the ownership check behind both doors
// still runs against the same auth.CallerOperator the real validator hands back.
//
// warn is where access.Bypass announces itself, on every request (FR-040). It is
// a parameter rather than os.Stderr because that is the seam every other
// out-of-process collaborator in this package has, and because a test asserting
// the warning has to be able to read it.
//
// The bypass refuses a non-loopback listener itself (FR-039), before this
// returns a server at all, so the one build that authenticates nobody cannot be
// the one build that is reachable off-host.
func NewWithBypass(cfg *config.Config, warn io.Writer) (*Server, error) {
	if cfg == nil {
		return nil, errors.New("httpapi: nil config")
	}
	// Its own tmux server, for the reason New gives — and this is the build a
	// developer runs *alongside* the real daemon to check a template change,
	// which is precisely the case #22 describes.
	tmux, err := tmuxctl.NewExec(tmuxctl.SocketFor(cfg.Listen), cfg.PaneBound, config.SessionEnvironment(os.Environ(), cfg.SessionEnvironment))
	if err != nil {
		return nil, err
	}
	return newWithLayer1(cfg, tmux, audit.New(), func(c *config.Config) (layer1, error) {
		b, err := access.NewBypass(c.Listen, warn)
		if err != nil {
			// Untyped nil for the reason verifiedLayer1 returns one.
			return nil, err
		}
		// Through the same assertionDoor the real validator reaches the middleware
		// through, and not beside it (M12/T003). The bypass reads no assertion, so
		// the wrapper changes nothing about what it does — what it keeps is the
		// property this whole file is written for: the door in front of the two is
		// one door, and the development build is the build where a drift between
		// them could still be seen.
		//
		// Named for what it is, which is the one thing the wrapper cannot work out
		// for itself: the two validators reaching the middleware through one type
		// is exactly why the settings page has to be told which of them this is
		// (M12/T006).
		return assertionDoor{validator: b, door: doorSentenceBypassed}, nil
	})
}
