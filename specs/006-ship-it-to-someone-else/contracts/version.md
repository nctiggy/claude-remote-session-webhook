# Contract: Version

**Files**: `internal/buildinfo/buildinfo.go` (new), `cmd/crswd/main.go`, `internal/httpapi/version.go` (new)
**Tests**: `internal/buildinfo/buildinfo_test.go`, `cmd/crswd/version_test.go`, `internal/httpapi/version_test.go`
**Satisfies**: FR-001 … FR-005

---

## One variable, one default, and the default is the truth

```go
package buildinfo

// Version is what this build calls itself. "dev" is the honest answer for a
// build no release produced, and it is the DEFAULT precisely so that forgetting
// the ldflags cannot make a working tree claim to be a release.
var Version = "dev"
```

```
-ldflags "-X github.com/nctiggy/claude-remote-session-webhook/internal/buildinfo.Version=v0.42"
```

A package rather than `main`, because `internal/httpapi` must report the same
string and a `main` variable is unreachable from there. **Two readers, one
source** — so they cannot disagree.

## The two readers

| Reader | Output |
|---|---|
| `crswd --version` | `crswd v0.42`, or `crswd dev (not a release)` |
| `GET /dashboard/version` | the same string, plus the latest available |

`flag.Parse()` already runs in `main.go` with no flags defined, so `--version`
joins there.

## Where the number comes from

`v0.<git rev-list --count HEAD>` at the merge commit.

**Monotonic, and derived from the repository itself.** `github.run_number` was the
obvious choice and resets if a workflow is renamed or recreated — which would make
an older release outrank a newer one, breaking both the updater's "is this newer"
question and the retention rule. A number that lives in the repository survives CI
being rebuilt around it.

Not semver: semver promises a compatibility contract this project has not made.

## Contract tests

| Test | Asserts | **Must fail when** |
|---|---|---|
| `TestDefaultVersionIsDev` | An unstamped build reports `dev` | The default is changed to a version-shaped string, so a local build claims to be a release |
| `TestUnreleasedBuildSaysSo` | `--version` on an unstamped build says it is not a release | It prints a bare `dev` an operator could mistake for a version |
| `TestFlagAndRouteAgree` | Both read `buildinfo.Version` | One is given its own copy, and they drift |
| `TestStampedVersionIsReported` | A build stamped `v0.42` reports `v0.42` from both readers | The ldflags path is wrong, which fails silently — the default is a valid string |
| `TestVersionRouteEmitsOneAuditRecord` | Exactly one record | It is registered outside the middleware |
