// Internal test, matching the rest of the package. Both claims here are about
// the route rather than about a string — what a running daemon answers when it
// is asked what it is, and what the trail says about having been asked — so each
// drives GET /dashboard/version through the real router, the real browser door,
// and a real *access.Validator over a locally generated key pair. A handler
// called directly would prove neither.
package httpapi

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/nctiggy/claude-remote-session-webhook/internal/audit"
	"github.com/nctiggy/claude-remote-session-webhook/internal/auth"
	"github.com/nctiggy/claude-remote-session-webhook/internal/buildinfo"
)

// versionPath is derived from the pattern the server registers rather than
// spelled again here, for the reason settingsPath is: a renamed route would
// otherwise leave every test in this file passing against a path nothing claims.
var versionPath = strings.TrimPrefix(patternVersion, http.MethodGet+" ")

// versionAnswer decodes what the route said, and fails on a body that is not the
// documented object — a route answering something else has not reported a
// version, whatever text it contains.
func versionAnswer(t *testing.T, f *fleet) versionResponse {
	t.Helper()

	w := f.open(t, versionPath)
	if w.Code != http.StatusOK {
		t.Fatalf("GET %s = %d (%s); want %d", versionPath, w.Code, w.Body.String(), http.StatusOK)
	}

	var got versionResponse
	dec := json.NewDecoder(strings.NewReader(w.Body.String()))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&got); err != nil {
		t.Fatalf("GET %s answered %q, which is not a version response: %v", versionPath, w.Body.String(), err)
	}
	return got
}

// TestFlagAndRouteAgree is contracts/version.md's third row: both readers read
// buildinfo.Version, so the two cannot disagree about what this build is.
//
// The flag's half of that claim is proved where the flag lives — cmd/crswd's
// version_test.go execs a real stamped binary and reads the line it prints, which
// is the only place it *can* be proved, since internal/httpapi must not import
// cmd/crswd and the import that exists runs the other way. What is left here is
// the half this package owns, and it is the stronger one: that the route holds no
// copy of the version at all.
//
// The variable is changed *after the server is built* and the route asked twice,
// which is what makes this an assertion about the read rather than about the
// string. A handler with a package-level constant fails the second ask; so does
// one that took a copy at construction, which is the mutation a field on Server
// would be and the one a test that stamped before newServer would miss entirely.
//
// It does not call t.Parallel, and that is deliberate: it writes a variable the
// route reads, and Go resumes parallel tests only once the sequential pass is
// over, so the window this mutation is visible in contains no other test. The
// value is restored before that pass continues.
//
// **Must fail when** the route is given its own copy of the version and the two
// drift.
func TestFlagAndRouteAgree(t *testing.T) {
	f := newFleet(t)

	if got, want := versionAnswer(t, f).Version, buildinfo.Version; got != want {
		t.Fatalf("GET %s reported %q on an unstamped build; want %q, the variable both readers read",
			versionPath, got, want)
	}

	// A version this repository will never be stamped with, so a route answering
	// it can only have read the variable.
	const stamped = "v0.4242-only-this-test"

	unstamped := buildinfo.Version
	t.Cleanup(func() { buildinfo.Version = unstamped })
	buildinfo.Version = stamped

	if got := versionAnswer(t, f).Version; got != stamped {
		t.Errorf("a daemon whose buildinfo.Version is %q reported %q at %s; the route is not reading the variable both readers read",
			stamped, got, versionPath)
	}
}

// TestVersionRouteEmitsOneAuditRecord is the contract's last row: one record for
// one request, under this route's own action.
//
// The action is its own rather than dashboard.view because an operator counting
// who asked this daemon what it is must not be counting page loads with them —
// and because anything watching for an update asks it far more often than a
// person opens the fleet.
//
// **Must fail when** the route is registered outside the middleware, which is the
// mutation the count catches here and the response cannot: a route handed
// straight to the mux still answers with a version, to anybody who reaches the
// listener, and leaves the trail silent about it.
func TestVersionRouteEmitsOneAuditRecord(t *testing.T) {
	t.Parallel()

	f := newFleet(t)

	w := f.open(t, versionPath)
	if w.Code != http.StatusOK {
		t.Fatalf("GET %s = %d (%s); want %d", versionPath, w.Code, w.Body.String(), http.StatusOK)
	}

	rec := f.only(t)
	if got, want := rec["action"], string(audit.ActionDashboardVersion); got != want {
		t.Errorf("the version route was recorded as %v; want %q", got, want)
	}
	if got, want := rec["decision"], string(audit.Allow); got != want {
		t.Errorf("a served version was recorded as %v; want %q", got, want)
	}
	// The server-derived owner, which is what makes the record comparable with
	// every other one this daemon writes — never the verified address, which is a
	// claim value and stays out of the trail (FR-042).
	if got, want := rec["caller"], string(auth.CallerOperator); got != want {
		t.Errorf("the version route was recorded for caller %v; want %q", got, want)
	}
	// This route is about the daemon and not about one session, so the field
	// data-model.md carries on the single-session view alone is absent here.
	if id, carried := rec["session_id"]; carried {
		t.Errorf("the version route's record carries session_id %v; it names no session", id)
	}

	// The answer really was served, rather than a record emitted by something that
	// served nothing.
	if got, want := w.Header().Get(headerContentType), contentTypeJSON; got != want {
		t.Errorf("the version route declared itself %q; want %q", got, want)
	}
}

// TestVersionRequiresIdentity is FR-006 at this route: a version is exactly the
// fact a scanner would like for free, and this door gives it nothing.
//
// The refusal is compared byte for byte against the fleet's refusal for the same
// credential rather than merely asserted a 401, for the reason
// TestSettingsRequiresIdentity compares them: FR-010's uniformity is *within* the
// door, and a version route whose refusal differed by a header would tell a
// stranger this daemon serves something at that address.
//
// **Must fail when** the route is registered ahead of the door, the way a health
// or metrics endpoint usually is.
func TestVersionRequiresIdentity(t *testing.T) {
	t.Parallel()

	f := newFleet(t)

	w := f.openWith(t, versionPath, absent)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("GET %s with no assertion at all was answered %d (%s); want %d — the door's uniform refusal",
			versionPath, w.Code, w.Body.String(), http.StatusUnauthorized)
	}
	if strings.Contains(w.Body.String(), buildinfo.Version) {
		t.Errorf("GET %s answered a caller layer 1 refused with the version:\n%s", versionPath, w.Body.String())
	}

	// The same credential at the fleet, on the same daemon: what this door really
	// refuses with, rather than this test's idea of it.
	elsewhere := f.openWith(t, "/", absent)
	if got, want := w.Body.String(), elsewhere.Body.String(); got != want {
		t.Errorf("an unverified caller was refused at %s with\n%s\nand at the fleet with\n%s\nthe two are distinguishable",
			versionPath, got, want)
	}
}
