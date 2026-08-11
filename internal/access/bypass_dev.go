//go:build dev

// Compiled only into a development build. See bypass_prod.go for why the
// exclusion is a build tag and never a flag or a configuration value.

package access

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"

	"github.com/nctiggy/claude-remote-session-webhook/internal/auth"
)

// bypassEmail is what the bypass hands on in place of the address the edge would
// have verified.
//
// It is deliberately **not** an address. contracts/access-jwt.md asks for "an
// explicit bypass marker, never a fabricated email", and the reason is FR-020:
// the header exists so it is never ambiguous whose credentials are driving
// unsandboxed sessions on this host. Under the bypass the truthful answer is
// nobody's, and an email-shaped string — however reserved its domain — reads at a
// glance as a person the daemon checked. This reads as the absence of one.
//
// It cannot collide with a real allowlist entry because it contains a space and
// no "@", so it is not an address any allowlist could hold. It is
// deliberately not configurable — an identity the operator could set here would
// be the bypass quietly growing into a login form, which is layer 1 rebuilt
// badly rather than skipped.
//
// The name keeps the word "bypass" on purpose, and pays a //nolint for it:
// gosec's G101 pattern for a hardcoded credential matches "pass", which
// "bypass" contains. The name is worth the line, because
// TestShippingBuildDeclaresNoBypassSymbol scans the shipping build for exactly
// that word — a constant renamed to something neutral could be moved into a file
// with no build tag and the scan would not notice.
//
//nolint:gosec // G101 false positive: an address in a reserved domain, not a credential.
const bypassEmail = "NOBODY — layer 1 bypassed (dev build)"

// errBypassUnannounced refuses the request the bypass would otherwise have
// admitted, because it could not write FR-040's warning.
//
// config.warnDefaultRoot takes the same line for the same reason: a warning
// nobody was told about is the requirement not being met, and here the thing
// going unannounced is that no browser identity was checked at all. A bypass
// that falls silent is indistinguishable from a daemon that is verifying
// identities, which is the one impression this build must never give.
var errBypassUnannounced = errors.New("the development bypass could not announce itself; refusing the request it would have admitted")

// Bypass is layer 1 with layer 1 taken out: it admits every browser as the
// operator without looking at an assertion, so the dashboard can be developed on
// a laptop, where there is no Cloudflare Access to sign anything (FR-038, US5).
//
// It skips layer 1 and nothing else. The Owner it hands back is the same
// auth.CallerOperator constant a real verified operator carries, so the
// ownership check behind the door is the check it always was, and layers 2 and 3
// are in internal/auth and internal/httpapi where this package cannot reach
// them.
type Bypass struct {
	// mu serialises the warning so that two concurrent requests produce two
	// legible banners rather than one interleaved line neither of them can be
	// read from.
	mu sync.Mutex

	// warn is where the announcement goes: cmd/crswd's stderr, a buffer in a
	// test. It is never the audit trail — the trail belongs to requests and
	// records decisions, and this is the daemon saying what it is, on every
	// request, to whoever is watching the process.
	warn io.Writer
}

// layer1 is what the browser door asks of whatever it was given, and exists so
// that both halves of the pair are held to one signature.
//
// The door must not care which it holds: a middleware written against *Validator
// and adapted for *Bypass would be a second authorisation path, and the second
// path is the one nobody reads. The assertions below fail the development build
// the moment the two drift apart, which is the build where the drift can still
// be seen.
type layer1 interface {
	Verify(ctx context.Context, assertion string) (*VerifiedOperator, error)
}

var (
	_ layer1 = (*Validator)(nil)
	_ layer1 = (*Bypass)(nil)
)

// NewBypass refuses to operate unless the listener is loopback (FR-039), and
// announces itself before it returns.
//
// The other two readings of this rule — config.loadListen, and httpapi.Server on
// the address the kernel actually bound — stopped being absolute at M12: a
// daemon whose layer 1 admits somebody may bind where the network can reach it.
// This one did not, and that is the whole reason it is written here rather than
// inherited. The other two protect a daemon that still authenticates; the
// property of this one is that it does not, so it must hold on its own and can
// never be relaxed by a door it has none of.
//
// warn is required rather than defaulted, because a bypass with nowhere to
// announce itself is the silent one FR-040 exists to prevent.
func NewBypass(listen string, warn io.Writer) (*Bypass, error) {
	if warn == nil {
		return nil, errors.New("access: the development bypass has nowhere to announce itself; refusing to start")
	}
	if err := assertLoopbackListen(listen); err != nil {
		return nil, err
	}

	b := &Bypass{warn: warn}
	if err := b.announce(bypassStartupWarning); err != nil {
		return nil, fmt.Errorf("access: %w", err)
	}
	return b, nil
}

// Verify admits the caller as the operator, and that is the whole of it: the
// assertion is not read, because not reading it is what the bypass is.
//
// The context is unused for the same reason — there is no key set to fetch and
// no signature to check. Both arguments stay in the signature so that this and
// *Validator.Verify are one method to the door in front of them.
//
// The warning is written per call, not per process (FR-040). A developer who
// started the daemon an hour ago is not looking at the startup banner, and the
// request they are serving right now was authenticated by nothing.
func (b *Bypass) Verify(_ context.Context, _ string) (*VerifiedOperator, error) {
	if err := b.announce(bypassRequestWarning); err != nil {
		return nil, err
	}

	// A fresh value per call, never a shared one: a pointer handed to every
	// handler is a pointer any handler can rewrite for all the others.
	return &VerifiedOperator{Email: bypassEmail, Owner: auth.CallerOperator}, nil
}

const (
	bypassStartupWarning = "this daemon was built with -tags dev and is running with the development bypass"
	bypassRequestWarning = "served with NO verified browser identity: the development bypass admitted this request"
)

// announce writes one banner, whole, under the lock.
//
// The banner is loud in the shape config.warnDefaultRoot already established,
// because these two warnings say the same kind of thing — the daemon is running
// on a rule the operator did not choose — and an operator scanning a log should
// not have to learn a second way of spotting one.
func (b *Bypass) announce(what string) error {
	const rule = "!!! ==========================================================================="
	banner := strings.Join([]string{
		rule,
		"!!! WARNING: " + what + ".",
		"!!! Layer 1 is skipped: every browser reaching this listener is the operator.",
		"!!! This build must never be deployed.",
		rule,
		"",
	}, "\n")

	b.mu.Lock()
	defer b.mu.Unlock()

	if _, err := io.WriteString(b.warn, banner); err != nil {
		return errors.Join(errBypassUnannounced, err)
	}
	return nil
}

// assertLoopbackListen holds the bypass to the rule config.loadListen holds a
// daemon with no browser door to, including its refusal of host *names*.
//
// A name is refused rather than resolved because "localhost" is whatever
// /etc/hosts or a resolver says it is, and a bypass that trusted the answer
// would be one DNS entry away from serving an unauthenticated dashboard to the
// network. Only an IP literal says where the listener will be.
func assertLoopbackListen(listen string) error {
	host, _, err := net.SplitHostPort(listen)
	if err != nil {
		return fmt.Errorf("access: the development bypass cannot read the listen address %q as host:port; refusing to start", listen)
	}
	ip := net.ParseIP(host)
	if ip == nil {
		// The wildcard binds arrive here too: "" from ":8765" parses as no IP
		// at all, which is exactly the address that would publish this build to
		// every interface on the host.
		return fmt.Errorf("access: the development bypass requires a loopback IP literal such as 127.0.0.1 or ::1, not %q; refusing to start", host)
	}
	if !ip.IsLoopback() {
		return fmt.Errorf("access: the development bypass refuses a listener on %q, which is not loopback; it authenticates nobody and must not be reachable off-host", host)
	}
	return nil
}
