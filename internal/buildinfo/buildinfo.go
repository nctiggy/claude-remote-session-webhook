// Package buildinfo holds what this build calls itself, and nothing else.
//
// It is a package rather than a variable in `main` because two readers need the
// same string: the `--version` flag in cmd/crswd, and GET /dashboard/version in
// internal/httpapi. A `main` variable is unreachable from internal/httpapi, and
// the alternative — a second copy over there — is two strings that can drift.
// One source is the whole reason this package exists.
package buildinfo

// Version is what this build calls itself.
//
// The release workflow stamps it at link time:
//
//	-ldflags "-X github.com/nctiggy/claude-remote-session-webhook/internal/buildinfo.Version=v0.42"
//
// "dev" is the DEFAULT rather than a value anything assigns, and that direction
// is the point: forgetting the ldflags leaves a build saying it is not a
// release, which is true. A version-shaped default would fail the other way —
// silently, because every consumer would accept it — and a working tree would
// claim to be something an operator could roll back to.
var Version = "dev"
