//go:build dev

// An internal test (package access), like the rest of this package, and one that
// exists only in the build that has something to test: without -tags dev there
// is no Bypass to name, which is the property bypass_build_test.go asserts from
// the other side.
//
// What cannot be asserted here is FR-038's second half — that layers 2 and 3
// stay enforced — because both live in other packages and neither is reachable
// from this one. What *is* asserted here is the piece of it this package owns:
// the identity the bypass produces carries the same owner constant a verified
// operator does, so the ownership check behind the door is unchanged rather than
// waived. The end-to-end half is quickstart.md Story 5, which posts to a
// mutating route under an active bypass and expects the 401 layer 2 gives it.
package access

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/nctiggy/claude-remote-session-webhook/internal/auth"
)

// loopbackListen is the address every bypass in this file is built for. The
// non-loopback ones are the subject of exactly one test, and they are refused
// before a Bypass exists.
const loopbackListen = "127.0.0.1:8765"

// newTestBypass returns a bypass writing into a builder, with the startup banner
// already accounted for so a test can count the per-request ones.
func newTestBypass(t *testing.T) (*Bypass, *strings.Builder) {
	t.Helper()

	var warn strings.Builder
	b, err := NewBypass(loopbackListen, &warn)
	if err != nil {
		t.Fatalf("NewBypass(%q): %v", loopbackListen, err)
	}
	return b, &warn
}

// flakyWriter succeeds for ok writes and refuses every one after that, which is
// how a test reaches the announcement Verify makes without failing the one
// NewBypass makes first.
type flakyWriter struct {
	ok  int
	n   int
	err error
}

func (w *flakyWriter) Write(p []byte) (int, error) {
	w.n++
	if w.n > w.ok {
		return 0, w.err
	}
	return len(p), nil
}

// TestBypassAdmitsWithoutReadingTheAssertion is FR-038's first half: layer 1 is
// skipped, so what arrives in the assertion argument makes no difference at all.
//
// The forged rows matter more than the empty one. A bypass that admitted an
// absent assertion but still refused a malformed one would be a validator with a
// hole in it rather than a skipped layer, and the difference would show up as a
// developer unable to load the dashboard from a browser that had once talked to
// the real thing and kept the cookie.
func TestBypassAdmitsWithoutReadingTheAssertion(t *testing.T) {
	t.Parallel()

	assertions := map[string]string{
		"absent":              "",
		"not a JWS at all":    "hello",
		"two segments":        "aGVhZGVy.cGF5bG9hZA",
		"forged, well formed": "eyJhbGciOiJub25lIn0.eyJlbWFpbCI6ImludHJ1ZGVyQGV4YW1wbGUuY29tIn0.",
	}

	for name, assertion := range assertions {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			b, _ := newTestBypass(t)

			operator, err := b.Verify(context.Background(), assertion)
			if err != nil {
				t.Fatalf("the bypass refused %s: %v", name, err)
			}
			if operator.Email != bypassEmail {
				t.Fatalf("Email = %q, want the development address %q", operator.Email, bypassEmail)
			}
			// The bypass skips layer 1 and nothing else: ownership is still
			// compared, against the same constant the real door concludes with
			// (research D7). A bypass that produced a different owner would show
			// a developer an empty dashboard and no reason for it.
			if operator.Owner != auth.CallerOperator {
				t.Fatalf("Owner = %q, want the constant %q so the ownership check is unchanged", operator.Owner, auth.CallerOperator)
			}
		})
	}
}

// TestBypassIdentityIsAMarkerAndNotAnAddress pins what the header shows when
// layer 1 is off.
//
// contracts/access-jwt.md asks for "an explicit bypass marker, never a fabricated
// email", and the reason is FR-020: the header exists so it is never ambiguous
// whose credentials are driving unsandboxed sessions on this host. Under the
// bypass the truthful answer is nobody's. An email-shaped string reads at a
// glance as a person the daemon verified — even in a reserved domain, which the
// operator glancing at a header is not parsing for.
//
// Asserting the absence of "@" rather than the presence of particular words is
// deliberate: it is the property that makes the value unable to be an address at
// all, so no later edit toward something plausible can pass this.
func TestBypassIdentityIsAMarkerAndNotAnAddress(t *testing.T) {
	t.Parallel()

	if strings.Contains(bypassEmail, "@") {
		t.Fatalf("bypassEmail = %q, want a marker rather than anything address-shaped", bypassEmail)
	}
	if mustAllowlist(t, testEmail).permits(bypassEmail) {
		t.Fatal("the development identity is admitted by a real allowlist")
	}
}

// TestBypassWarnsOnEveryRequest is FR-040 exactly: not once at startup, every
// time.
//
// The requirement is a statement about the developer's attention rather than
// about the code. A banner printed an hour ago, above a screen of request logs,
// is not a warning anybody is reading when they wonder why the dashboard let
// them in without an assertion.
func TestBypassWarnsOnEveryRequest(t *testing.T) {
	t.Parallel()

	b, warn := newTestBypass(t)

	if got := strings.Count(warn.String(), bypassStartupWarning); got != 1 {
		t.Fatalf("startup announcements = %d, want exactly 1", got)
	}

	const requests = 3
	for i := range requests {
		if _, err := b.Verify(context.Background(), ""); err != nil {
			t.Fatalf("request %d: %v", i, err)
		}
	}

	if got := strings.Count(warn.String(), bypassRequestWarning); got != requests {
		t.Fatalf("per-request warnings = %d, want %d — one for each request", got, requests)
	}
	if got := strings.Count(warn.String(), bypassStartupWarning); got != 1 {
		t.Fatalf("startup announcements = %d after serving; the startup banner is not the per-request one", got)
	}
}

// TestBypassRefusesTheRequestItCannotAnnounce keeps the warning and the
// admission together.
//
// config.warnDefaultRoot already treats a failed warning as fatal for the same
// reason: a requirement met only when the writer cooperates is not met. Here the
// thing going unsaid is that the request was authenticated by nothing, and a
// daemon that admitted it silently would be indistinguishable from one checking
// identities.
func TestBypassRefusesTheRequestItCannotAnnounce(t *testing.T) {
	t.Parallel()

	broken := errors.New("the log went away")
	// One successful write for NewBypass's own banner, then nothing.
	b, err := NewBypass(loopbackListen, &flakyWriter{ok: 1, err: broken})
	if err != nil {
		t.Fatalf("NewBypass: %v", err)
	}

	operator, err := b.Verify(context.Background(), "")
	if err == nil {
		t.Fatal("a request was admitted by an unannounced bypass")
	}
	if operator != nil {
		t.Fatal("a refused request still produced an operator")
	}
	if !errors.Is(err, errBypassUnannounced) {
		t.Fatalf("err = %v, want the unannounced-bypass refusal", err)
	}
	// The underlying write failure travels with it: the refusal says what the
	// daemon refused, the join says why it could not say it.
	if !errors.Is(err, broken) {
		t.Fatalf("err = %v, want the write failure joined to it", err)
	}
}

// TestNewBypassNeedsSomewhereToAnnounceItself refuses at startup what
// TestBypassRefusesTheRequestItCannotAnnounce refuses per request. A nil writer
// is a bypass that can never meet FR-040, and the honest time to say so is
// before the listener binds.
func TestNewBypassNeedsSomewhereToAnnounceItself(t *testing.T) {
	t.Parallel()

	if _, err := NewBypass(loopbackListen, nil); err == nil {
		t.Fatal("a bypass with nowhere to warn was allowed to start")
	}

	broken := errors.New("the log went away")
	if _, err := NewBypass(loopbackListen, &flakyWriter{ok: 0, err: broken}); err == nil {
		t.Fatal("a bypass that could not announce itself was allowed to start")
	}
}

// TestNewBypassRefusesANonLoopbackListener is FR-039, and the table is the same
// one config.loadListen is held to — including its refusal of host names, which
// is the row that looks wrong until you notice "localhost" is whatever a
// resolver says it is.
//
// config refuses these addresses already. This is a third check of the same rule
// on purpose: the other two protect a daemon that still authenticates, and the
// defining property of this one is that it does not.
func TestNewBypassRefusesANonLoopbackListener(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		listen string
		admit  bool
	}{
		"loopback IPv4":            {"127.0.0.1:8765", true},
		"loopback IPv4, any port":  {"127.0.0.1:0", true},
		"loopback IPv4, elsewhere": {"127.9.9.9:8765", true},
		"loopback IPv6":            {"[::1]:8765", true},

		"every interface":     {"0.0.0.0:8765", false},
		"every interface, v6": {"[::]:8765", false},
		"no host at all":      {":8765", false},
		"a name, not an IP":   {"localhost:8765", false},
		"a private address":   {"192.168.1.10:8765", false},
		"a public address":    {"203.0.113.4:8765", false},
		"no port":             {"127.0.0.1", false},
		"nothing":             {"", false},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			var warn strings.Builder
			b, err := NewBypass(tc.listen, &warn)

			if tc.admit {
				if err != nil {
					t.Fatalf("NewBypass(%q) = %v, want a bypass", tc.listen, err)
				}
				return
			}

			if err == nil {
				t.Fatalf("NewBypass(%q) was allowed to start off loopback", tc.listen)
			}
			if b != nil {
				t.Fatalf("NewBypass(%q) refused and returned a bypass anyway", tc.listen)
			}
			// A refusal is not a start, so it does not announce one. An operator
			// reading the banner in a log has been told the daemon is running
			// without layer 1; it must never appear above a startup that failed.
			if warn.Len() != 0 {
				t.Fatalf("a refused bypass announced itself: %q", warn.String())
			}
		})
	}
}

// TestBypassWarningsSurviveConcurrentRequests: the warning is only prominent
// while it is legible, and every route on this daemon is served concurrently.
// Two banners written without a lock interleave into lines that belong to
// neither.
func TestBypassWarningsSurviveConcurrentRequests(t *testing.T) {
	t.Parallel()

	b, warn := newTestBypass(t)

	const requests = 20
	var wg sync.WaitGroup
	for range requests {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := b.Verify(context.Background(), ""); err != nil {
				t.Errorf("concurrent request: %v", err)
			}
		}()
	}
	wg.Wait()

	if got := strings.Count(warn.String(), bypassRequestWarning); got != requests {
		t.Fatalf("per-request warnings = %d, want %d", got, requests)
	}
	for i, line := range strings.Split(strings.TrimSuffix(warn.String(), "\n"), "\n") {
		if !strings.HasPrefix(line, "!!!") {
			t.Fatalf("line %d is not a whole banner line, so two writes interleaved: %q", i, line)
		}
	}
}
