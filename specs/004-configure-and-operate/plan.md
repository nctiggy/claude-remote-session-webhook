# Implementation Plan: Configure and operate it

**Branch**: `004-configure-and-operate` | **Date**: 2026-08-07 | **Spec**: [spec.md](./spec.md)
**Input**: Feature specification from `specs/004-configure-and-operate/spec.md`

## Summary

Ten open issues, consolidated. The theme is that `crswd` is now used daily and the
friction is in **configuring** it and in the dashboard's **rough edges**.

The plan's central finding is that this milestone is mostly **finishing and
integrating, not building**. Four abandoned lane branches carry roughly 3,800
lines of real, working code between them — every one of them builds standing
alone, and every one of them broke only when merged against a `main` that has
since moved. Restarting any of them would be the most expensive way to get a
worse result. Task text therefore names the branch and the file to carry forward.

The second finding is architectural, and it is what keeps this milestone small.
`internal/config/config.go` already exposes:

```go
func LoadFrom(getenv func(string) string, warn io.Writer, opts ...Option) (*Config, error)
```

The configuration file does not need to become a second set of rules, a second
validator, or a second set of bounds. It becomes a **fallback `getenv`**: a shim
that answers from the environment first and from the parsed file second. Every
value a file supplies then travels through the exact loader an environment
variable travels through, so a bound cannot mean one thing in a unit test and
another in a file. Precedence has exactly one implementation, and it is four
lines long. Provenance — which source supplied each value, the thing that makes
"why is my change not applied?" answerable on the settings page — falls out of
the same shim for free, because the shim is the only code that knows.

## Technical Context

**Language/Version**: Go 1.23
**Primary Dependencies**: None. Standard library only; `go.sum` must remain absent (SC-012).
**Storage**: tmux user options (session records survive daemon restart); the operator's config file (read-only to the daemon).
**Testing**: `go test ./...`, plus `-tags tmux` and `-tags quickstart` where touched.
**Target Platform**: Linux and macOS, single host, one allowlisted identity.
**Project Type**: Single Go module with an embedded web interface.
**Performance Goals**: Not a driver. Startup parse of one small file; the settings page renders what is already in memory.
**Constraints**: FR-054 through FR-059, carried forward unchanged and non-negotiable.
**Scale/Scope**: One operator, one host, a fleet of tens of sessions.

### Unknowns

None. Everything the milestone needed decided is decided in
[research.md](./research.md) — grammar, precedence, secret classification,
provenance, mode storage, and the no-JavaScript degradation path. No
NEEDS CLARIFICATION markers remain.

## Constitution Check

| Principle | Gate | Verdict |
|---|---|---|
| I. Security is a gate | Every new route runs the identity check, the cross-site gate and the page token before its handler. The settings page is a new route and gets all three. | **PASS** — no new route bypasses the middleware; the settings page adds no mutating verb at all. |
| II. Unknowns surfaced | Six named design questions were open at planning time. | **PASS** — all six are answered in research.md with rationale and alternatives, none guessed. |
| III. Every change verifiable | Each contract carries a test table with a **must fail when** column. | **PASS** — a task that cannot state its failing condition is not in the list. |
| IV. Smallest correct change | The config file could have been a parallel configuration system. | **PASS** — it is a `getenv` shim instead. Validation, bounds and refusals are untouched. |
| V. Standards enforced | Full gate per task: build, vet, test, `golangci-lint run` at the pinned v2.12.2. | **PASS** — carried into every task's exit condition. |
| VI. Blast radius bounded | New behaviour goes in new files, so a task's blast radius is one file. | **PASS** — see Source Code below. |
| VII. Design system binding | The combobox, the card split, and the settings page are all new surface. | **PASS** — tokens only, one anchor per card, nothing animating under reduced motion, focus rings visible. |

**Post-design re-evaluation**: unchanged. The one design choice that could have
violated a principle was giving the settings page a write path; the spec puts
editing out of scope, and the plan keeps the route read-only with no mutating
verb registered — a page that cannot write cannot write badly.

## Project Structure

### Documentation (this feature)

```
specs/004-configure-and-operate/
├── spec.md
├── plan.md              # this file
├── research.md          # the six decisions, with rationale and alternatives
├── data-model.md        # config keys, provenance, session mode, suggestions
├── quickstart.md        # how to prove each user story by hand
└── contracts/
    ├── config-file.md       # grammar, keys, refusals, worked example
    ├── config-precedence.md # the shim, and where precedence is decided
    ├── settings-page.md     # route, rendering, secret handling, audit
    ├── dependency-check.md  # startup probes and install commands
    ├── session-mode.md      # the toggle, persistence, --continue
    ├── directory-picker.md  # datalist, discovery, degradation
    └── card-layout.md       # the readable/actionable split
```

### Source Code (repository root)

New behaviour goes in new files, so that a task edits one file and a failure
names one file.

```
internal/config/
├── config.go              # EXISTING — gains only the shim wiring
├── file.go                # CARRY FORWARD from claude/issue-issue-65-20260807-0112
├── source.go              # NEW — provenance: which source supplied each value
├── depcheck.go            # NEW — tmux and start-command probes (#71)
└── *_test.go

internal/httpapi/
├── settings.go            # NEW — the read-only settings page (#49 phase 1)
├── outcome.go             # CARRY FORWARD from the #42 branch
├── actions.go             # EXISTING — gains the mode toggle route (#45, #58)
└── *_test.go

internal/session/
├── manager.go             # EXISTING — mode change, --continue, fifth tmux option
└── conversation.go        # NEW — prior-conversation listing (#44), names and times only

web/templates/
├── settings.html          # NEW
└── partials/
    ├── create-form.html   # CARRY FORWARD from the #59 branch (datalist)
    └── session-card.html  # CARRY FORWARD from the #60 branch (the split)
```

**Structure Decision**: Single project. The web interface is embedded in the
same binary via `go:embed` and is not a separate deployable, so a second
top-level tree would be a fiction.

## What already exists, and what each branch is worth

Verified at planning time: each branch builds standing alone; each fails only
against the current `main`. The work is salvage, not archaeology.

| Branch | Lines | Carry forward | Still to do |
|---|---|---|---|
| `claude/issue-issue-65-...0112` | 1,471 | `internal/config/file.go` and its tests: the parser, the four refusals, the rename map, the version key. Its reasoning is sound and its comments are the best documentation of the format. | Wire it as the shim (it currently parses without being consulted). Rebase onto `main`. |
| `claude/issue-issue-59-...0055` | 1,213 | The combobox markup and the discovery walk. | Replace the JS-first control with `<datalist>` so it degrades (FR-043). Rebase. |
| `claude/issue-issue-60-...0406` | 543 | The card split and the rename disclosure. | Reconcile with the toast and anchor work that landed on `main` after it. |
| `claude/issue-issue-42-...1832` | 590 | All four routes answering 303, `outcome.go`, the banner partial. | The ~19 tests still asserting fragment responses. This is **finish the tests**, not build PRG. |

## Complexity Tracking

No constitutional violations to justify. One deliberate simplification is worth
recording, because it looks like a shortcut and is not:

| Choice | Why it is not a violation |
|---|---|
| Session mode is **derived** from the start-command name rather than stored as its own field | Two fields that must agree are two fields that can disagree. FR-031 requires the record to carry the mode; it does, by carrying the name that determines it. The one real gap — the start command not surviving a restart — is closed by persisting a fifth tmux option, which is a smaller change than a second source of truth. |
