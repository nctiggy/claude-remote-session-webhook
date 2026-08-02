# Implementation Plan: crswd Daemon Core (Milestone 1)

**Branch**: `001-crswd-daemon-core` | **Date**: 2026-08-02 | **Spec**: [spec.md](./spec.md)

**Input**: Feature specification from `/specs/001-crswd-daemon-core/spec.md`

## Summary

Build `crswd`: a loopback-bound Go daemon that starts, drives, and reaps Claude Code
sessions in tmux on the operator's own machine, authenticated by HMAC-signed requests
plus a per-session bearer token, with every action audited and every session bounded by
a cap, an idle timeout, and a 24-hour ceiling. No web UI in this milestone.

The approach is standard-library-only across six packages that mirror the layout already
fixed in `AGENTS.md`. Three findings from Phase 0 shape the design more than anything in
the spec did: prompt text must reach tmux through `load-buffer`/`paste-buffer` rather
than `send-keys -l`, because tmux's own command parser silently eats a trailing
semicolon ([research.md D4](./research.md)); tmux target strings need an explicit
exact-match prefix whose *syntax differs* between session-targets (`=name`) and
pane-targets (`=name:`) ([D2](./research.md)); and daemon-created sessions are tagged
with a tmux user option so startup reconciliation can tell them from a lookalike
([D3](./research.md)). All three were verified against tmux 3.4 rather than assumed.

## Technical Context

**Language/Version**: Go 1.23 (`go.mod` declares `go 1.23.0`). CI pins `go-version:
'1.23'`; the local toolchain is 1.24.0 and compiles it fine, but 1.24-only APIs fail in
CI while passing locally — `crypto/rand.Text` and `testing/synctest` are therefore out
([research.md D8](./research.md)).

**Primary Dependencies**: None. Standard library only — `net/http` (incl. ServeMux
method+wildcard patterns, Go 1.22+), `crypto/hmac`, `crypto/sha256`, `crypto/rand`,
`encoding/json`, `log/slog`, `os/exec`, `path/filepath`, `os/signal`. `go.sum` stays
absent. The one contested case, a rate limiter, is hand-rolled rather than importing
`golang.org/x/time/rate` ([D7](./research.md)).

**Storage**: In-memory only. No database, no files, no persistence — restart recovery
comes from adopting live tmux sessions. External state lives in tmux itself.

**Testing**: `go test ./...`, table-driven with `t.Parallel()`. No network, no real
tmux in unit tests — `tmuxctl.Controller` is an interface with a fake. Real-tmux
integration tests sit behind a `//go:build tmux` tag. Time is injected everywhere it
matters, so no test sleeps.

**Target Platform**: Linux workstation running the daemon as a systemd *user* service
behind a Cloudflare Tunnel. tmux 3.4 verified on this host.

**Project Type**: Single Go module — one daemon binary plus internal packages.

**Performance Goals**: Not throughput-bound. One operator, at most `CRSW_MAX_SESSIONS`
(default 5) concurrent sessions, a handful of requests per minute. The only latency that
matters is that a create returns within one round trip (SC-004), and that the reaper's
resolution is finer than the timeouts it enforces (a 30s tick against a 60m idle
timeout).

**Constraints**: Loopback bind is a startup invariant, not a config option (FR-005).
Every session-scoped read resolves its tmux target from the ownership-checked record,
never from caller input (FR-034). No shell string is constructed anywhere (FR-029). No
secret, token, prompt, or pane content in any log line (FR-043).

**Scale/Scope**: 6 packages, 6 endpoints, 43 functional requirements, 18 success
criteria. Single operator, single tenant.

## Constitution Check

*GATE: evaluated before Phase 0 and re-evaluated after Phase 1 design.*

| Principle | Gate | Pre-Phase 0 | Post-Phase 1 |
|---|---|---|---|
| **I. Security is a gate** (NON-NEGOTIABLE) | Authorization server-side on every request; ownership checked, not just authn; input validated at the boundary; no shell string; weak auth config = startup failure; no secret in repo | PASS — FR-007/011/012/014/026–030/032–034 cover each clause; the secret comes from the environment | **PASS** — one auth middleware wraps every route with no exemption; the `contracts/` surface has no unauthenticated path; `decode[T]` is the only body path |
| **II. Unknowns surfaced, never invented** | No guess stands in for a decision | PASS — three ambiguities were raised at `/speckit-specify` and answered by the operator | **PASS** — Phase 0 resolved the remaining unknowns by *measuring* tmux, not assuming; two non-blocking items are flagged openly in research.md |
| **III. Every change is verifiable** | Build, test, lint green; new behaviour has a test that fails without it | PASS — SC-018 makes this a success criterion | **PASS** — quickstart.md gives a runnable check per user story; the fake controller makes negative paths testable without tmux |
| **IV. Smallest correct change** | No unrequested churn | PASS — scope is the milestone task list | **PASS** — no package outside the six already fixed in `AGENTS.md` plus `internal/audit` from the seeded list; no persistence layer, no router, no third-party module |
| **V. Standards are enforced, not documented** | Rules live in hooks and CI | PASS — hooks and CI already exist | **PASS** — `.golangci.yml` (v1 schema, matching the pinned v1.62) is a task, not a suggestion; adding `go.mod` auto-activates the CI build/test/lint job |
| **VI. Blast radius bounded** (NON-NEGOTIABLE) | Allowlisted dirs, capped sessions, idle + absolute timeouts, verified teardown, loopback bind | PASS — FR-003/028 (roots), FR-036 (cap), FR-038 (reaper), FR-019 (verified kill), FR-005 (loopback) | **PASS** — and *strengthened*: the adoption clock reads `session_created` from tmux (D6), so a restart loop cannot extend a session past 24h, and D2's exact-match targeting removes a class of wrong-session kills |
| **VII. Design system is binding** | Tokens, canonical components, pane output as text | **N/A** — no UI in this milestone | **N/A** — nothing visual ships; `web/` is untouched |

**Result: PASS, no violations.** Complexity Tracking is therefore empty.

Two gate-adjacent notes that are not violations but should not be silent:

- **The default working-directory root widens reachability by construction.** FR-003
  lets the daemon start with no `CRSW_ALLOWED_ROOTS` set, falling back to `~/code`.
  Principle VI says a feature that widens a bound needs an explicit justification naming
  what becomes reachable: what becomes reachable is *every repository under `~/code`*,
  and nothing above it — never `$HOME` itself, so SSH keys, cloud credentials, and
  browser profiles stay outside. The operator chose this over a hard startup failure,
  and FR-004 makes the fallback loud on every start (SC-015) so it cannot be in force
  unnoticed.
- **`golangci-lint` and `goimports` are absent on this machine** and the format-and-lint
  hook no-ops when a tool is missing ([D12](./research.md)). Until the setup task
  installs them, "lint passes" locally means "lint did not run". CI catches it either
  way; the cost of ignoring it is discovering a wall of findings late.

## Project Structure

### Documentation (this feature)

```text
specs/001-crswd-daemon-core/
├── plan.md              # This file
├── research.md          # Phase 0 output — 12 decisions, tmux behaviour verified
├── data-model.md        # Phase 1 output — entities, invariants, state machine
├── quickstart.md        # Phase 1 output — runnable validation per user story
├── contracts/
│   ├── http-api.md      # The 6 endpoints, headers, signing, status codes
│   └── tmuxctl.md       # The Controller interface + exact argv for every command
├── checklists/
│   └── requirements.md  # Spec quality checklist (from /speckit-specify)
└── tasks.md             # Phase 2 output — NOT created by /speckit-plan
```

### Source Code (repository root)

The layout is already fixed by `AGENTS.md` and `ralph/IMPLEMENTATION_PLAN.md`; this plan
adds no package beyond them. `internal/audit` is the one addition, and it is named in the
seeded task list ("`internal/audit`: structured JSON records on stdout").

```text
cmd/crswd/
└── main.go                    # flags, wiring, signal handling, exit codes. No logic.

internal/config/
├── config.go                  # Load() from environment; validation; loud default warning
└── config_test.go             # table: every missing/short/invalid field case

internal/tmuxctl/
├── controller.go              # Controller interface: New, Kill, Has, SendKeys, Paste,
│                              #   CapturePane, List, SetOption
├── exec.go                    # real impl over exec.Command, argv slices only
├── target.go                  # SessionTarget()="=name" vs PaneTarget()="=name:"  (D2)
├── ansi.go                    # defensive control-sequence stripper           (D5)
├── fake.go                    # in-memory fake for every other package's tests
├── exec_tmux_test.go          # //go:build tmux — real tmux integration
├── ansi_test.go               # golden files
└── testdata/                  # golden pane captures

internal/session/
├── session.go                 # Session model, state machine, Owner
├── manager.go                 # Create, Get, List, Destroy (verified), Adopt
├── id.go                      # crypto/rand → 32 hex chars                    (D9)
├── name.go                    # ^[a-zA-Z0-9-]{1,64}$, explicit : and . rejection
├── workdir.go                 # Clean + EvalSymlinks + under-approved-root check
├── reaper.go                  # idle 60m / absolute 24h, injected Clock
└── *_test.go

internal/auth/
├── hmac.go                    # verify over timestamp + "." + rawBody; hmac.Equal
├── replay.go                  # seen-signature cache, TTL 2×skew, atomic Observe (D10)
├── token.go                   # per-session bearer: crypto/rand, SHA-256 at rest
├── caller.go                  # Caller identity, derived server-side only
└── *_test.go                  # incl. negative: bad sig, skew both ways, replay

internal/audit/
├── audit.go                   # slog JSON → stdout; fixed field set
└── audit_test.go              # asserts no prompt/pane/token/secret ever appears

internal/httpapi/
├── server.go                  # ServeMux wiring, loopback assertion, timeouts
├── middleware.go              # auth on every route; uniform 401; one audit record
├── decode.go                  # DisallowUnknownFields + MaxBytesReader
├── ratelimit.go               # hand-rolled token bucket on injected Clock    (D7)
├── sessions.go                # the 6 handlers
└── *_test.go                  # incl. cross-owner 404 and per-route 401 sweeps

.golangci.yml                  # v1 schema — matches pinned golangci-lint v1.62 (D12)
go.mod                         # go 1.23.0 — no go.sum, zero dependencies
```

**Structure Decision**: Single Go module, `cmd/` + `internal/`, exactly the six packages
named in `AGENTS.md` plus `internal/audit` from the seeded task list. Tests are colocated
with the package they cover, per the repo convention — there is no top-level `tests/`
tree. `web/` and `skill/` are untouched by this milestone; `deploy/` only by the final
task.

The dependency direction is strictly one-way, which is what keeps the fake usable and the
handlers testable:

```
cmd/crswd → httpapi → {auth, session, audit, config}
                          session → tmuxctl (interface)
```

`tmuxctl` imports nothing from the project. `auth` knows nothing about tmux. No package
imports `httpapi`.

## Complexity Tracking

No constitutional violations to justify — the table is intentionally empty.

Complexity that was *considered and rejected*, recorded because "we thought about it" is
cheaper than rediscovering it:

| Rejected | Why it looked attractive | Why not |
|---|---|---|
| `golang.org/x/time/rate` | Correct, well-tested limiter | One dependency for ~40 lines on one endpoint; it reads the wall clock internally, so the reaper's injected-clock test discipline would not extend to it (D7) |
| A third-party HTTP router | Path params, method routing | `net/http.ServeMux` has both since Go 1.22 (D7) |
| One tmux session with a window per crswd session | Fewer tmux sessions | Window indices are renumbered by tmux, so a stored index can address another session's window — session-bleed by construction (D1) |
| Persisting session records | Survives restart cleanly | Explicitly out of scope; adoption already solves restart, and a store is a new failure mode plus a new place for a token hash to leak |
| Pre-escaping prompt text for `send-keys` | Keeps the obvious API | Escaping hostile input to feed a command line is exactly what `docs/security.md` §2 says not to do; `load-buffer` from stdin removes the command line entirely (D4) |
