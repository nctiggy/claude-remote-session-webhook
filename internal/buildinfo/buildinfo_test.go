// An external test (package buildinfo_test) because the two readers this
// package exists for are external too: they see exactly what this file sees.
package buildinfo_test

import (
	"regexp"
	"testing"

	"github.com/nctiggy/claude-remote-session-webhook/internal/buildinfo"
)

// versionShaped is what a release calls itself — `v0.<count>`, per
// contracts/version.md. The default must never match it.
var versionShaped = regexp.MustCompile(`^v[0-9]`)

// TestDefaultVersionIsDev pins the sentinel.
//
// `go test` links no ldflags, so this build is exactly the build an operator
// gets from `go build ./...` — the one whose honest answer is that it came from
// nobody's release. The failure this guards against is silent by construction:
// a version-shaped default is a perfectly valid string, every consumer accepts
// it, and the first sign of trouble is an operator trying to roll back to a
// release that was never published.
func TestDefaultVersionIsDev(t *testing.T) {
	t.Parallel()

	if buildinfo.Version != "dev" {
		t.Fatalf("unstamped build reports %q, want %q", buildinfo.Version, "dev")
	}

	// Belt and braces, and it is the assertion that says *why*: whatever the
	// default becomes, it may never be something an operator reads as a release.
	if versionShaped.MatchString(buildinfo.Version) {
		t.Fatalf("default version %q is version-shaped; an unreleased build must not claim to be a release", buildinfo.Version)
	}
}
