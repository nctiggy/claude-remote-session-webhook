# Contract: Where directory suggestions come from

**Files**: `internal/config/suggestions.go` (new), `internal/config/config.go`, `internal/httpapi/dashboard.go`
**Tests**: `internal/config/suggestions_test.go`, `internal/httpapi/partials_test.go`
**Satisfies**: FR-006 … FR-010

---

## The defect this fixes

`dashboard.go:254` reads:

```go
suggestions := s.cfg.DiscoveredWorkDirs()
```

That is the **only** source, and discovery is off by default. `CRSW_WORKDIR_SUGGESTIONS`
does not exist, despite milestone 4's `data-model.md` listing an explicit list as a
source since the plan landed.

So on a default install the picker has no sources at all and renders a plain text
field — which is why the operator reported the feature as missing. They were right
in every way that matters.

## The three sources

| Source | Key | Default | Costs a disclosure? |
|---|---|---|---|
| The approved roots themselves | `allowed_roots` | **always on** | **No** — the operator configured these paths |
| An explicit list | `workdir_suggestions` | empty | No |
| Discovered children | `discover_roots` | **off** | **Yes** — it reads the filesystem |

Combined as a **union**, deduplicated, sorted. Never empty when the daemon can
create a session at all, because the roots are always there.

## Why the roots are the right default

They are the one source guaranteed non-empty whenever a session can be created,
and they reveal **nothing the daemon was not already told**. That is what makes
them correct rather than merely convenient — the fix for a discoverability bug
must not quietly become a disclosure.

Their *children* are the opposite: enumerating them reads the filesystem, which is
exactly what `discover_roots` exists to keep opt-in. Turning that flag on by
default would trade a privacy decision for a usability one, silently.

A root is also a legitimate working directory in its own right, so offering it is
a real answer and not a placeholder.

## A suggestion is never an authorisation

**The security half, and it does not move.** A path chosen from the list is
validated exactly as a typed one is — same allowlist check, same refusal, same
audit record (FR-009). The list is presentation; `allowed_roots` is the control.

A path appearing in the list grants nothing. A path absent from it is still
acceptable if typed (FR-008).

## Worked example

```
allowed_roots       = /home/nctiggy/code,/home/nctiggy/work
workdir_suggestions = /srv/scratch
discover_roots      = false
```

Offered: `/home/nctiggy/code`, `/home/nctiggy/work`, `/srv/scratch`.

Note `/srv/scratch` is offered and — unless it is under a root — **refused on
submit**. That is not a contradiction; it is the contract working. The list is a
convenience, and an operator who configures a suggestion outside their own
containment boundary gets a refusal that names the real rule.

With `discover_roots = true`, the children of both roots join the list.

## Contract tests

| Test | Asserts | **Must fail when** |
|---|---|---|
| `TestRootsAreOfferedByDefault` | Roots configured, nothing else → those roots are offered | Discovery remains the only source — the shipped defect |
| `TestDefaultInstallRendersOptions` | With only roots configured, the **rendered create form** contains at least one `<option>` | The route would offer them but the form does not render them |
| `TestExplicitListIsOffered` | `workdir_suggestions` entries appear | The key is declared and never read — this repo's recurring failure |
| `TestSourcesAreUnionedAndDeduped` | A path in two sources appears once | Concatenation without dedup |
| `TestSuggestionsAreSorted` | Stable order across calls | Map iteration order reaches the markup, making the page differ between renders |
| `TestDiscoveryStillOffByDefault` | Without `discover_roots`, no child appears | The fix for emptiness turns discovery on |
| `TestSuggestedPathOutsideRootsRefused` | An offered path not under a root is refused identically to a typed one | The handler trusts a value because it was offered |
| `TestNoSuggestionSourceRendersPlainField` | All sources empty → no `<datalist>` at all | An empty `<datalist>` is emitted |
