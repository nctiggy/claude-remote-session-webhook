# Implementation Plan: Dashboard Actions

**Branch**: `003-dashboard-actions` | **Date**: 2026-08-05 | **Spec**: [spec.md](./spec.md)

**Input**: Feature specification from `specs/003-dashboard-actions/spec.md`

## Summary

Make the fleet dashboard able to act — destroy, create, rename, compact — and keep itself current,
without weakening the reasoning that made milestone 2's ambient-cookie reads safe.

Two findings from reading the existing code changed the shape of this plan, and both make it
smaller than the spec anticipated:

1. **The same-origin check already exists.** `crossSite()` in `stream.go` reads `Sec-Fetch-Site`
   and admits only `same-origin`, fail-closed, already tested. FR-002a asked for an `Origin` check;
   reusing this is stronger and smaller ([research R1](./research.md)).
2. **The page token must be stateless**, because milestone 2 deliberately holds no per-browser
   state and wrote down why. A stored token map would reintroduce precisely the expiry, growth and
   fixation questions that design removed ([research R2](./research.md)).

The second is the plan's central constraint. Everything else is four routes, one stream, and card
controls.

## Technical Context

**Language/Version**: Go 1.23 (module), built with the Go 1.24 toolchain

**Primary Dependencies**: None. Standard library only — `crypto/hmac`, `crypto/sha256`,
`crypto/rand`, `net/http`, `html/template`. `go.sum` must remain absent (FR-032, AR-009)

**Storage**: In-memory only. No new persistence, and specifically no stored token state (FR-034,
research R2)

**Testing**: `go test ./...`, plus the `tmux`, `dev` and `quickstart` build tags

**Target Platform**: Linux host, loopback listener, reached only through a Cloudflare Tunnel

**Project Type**: Single Go module — daemon with server-rendered HTML

**Performance Goals**: Fleet changes visible within 5s (SC-006); one stream write per second, as
milestone 2 fixed for the pane stream

**Constraints**: Zero dependencies; no per-browser server state; every response on the browser
door uniform on failure; one audit record per request

**Scale/Scope**: One allowlisted identity, `CRSW_MAX_SESSIONS` concurrent sessions (default 5)

## Constitution Check

*GATE: must pass before Phase 0 research, re-checked after Phase 1 design.*

| Principle | Gate | Status |
|---|---|---|
| I — Security is a gate | The cross-site defence is FR-001, specified before any action, and every action route is refused before state change without it | **PASS**. Both halves independently disableable in test (FR-002c), which is what makes the gate real rather than declared |
| II — Unknowns surfaced | Three spec-level unknowns were raised and answered as D1–D3; the two implementation-level ones this plan found are R1 and R2, both resolved with rejected alternatives recorded | **PASS** |
| III — Every change verifiable | Every requirement has a row in the spec's Verification Map naming the check *and the condition under which it must fail* | **PASS** |
| IV — Smallest correct change | R1 reuses an existing check rather than adding a parallel one; R7 satisfies idempotence from existing semantics rather than new machinery | **PASS** |
| V — Standards enforced | New routes inherit the existing browser-door middleware, so uniform refusal and one-record-per-request are structural rather than remembered | **PASS** |
| VI — Blast radius bounded | Destroy keeps verified teardown and the unverified outcome (FR-010); AR-004 forbids a force path; the concurrent cap and rate limit still bound create | **PASS** |
| VII — Design system binding | Controls are text-labelled (FR-030), keyboard-operable (FR-028), outside the card's single anchor (FR-027), and silent under reduced motion (FR-022) | **PASS** |

**Post-design re-check**: no violations introduced. The Complexity Tracking table is empty because
nothing in this design needed justifying against a simpler alternative — where a simpler one
existed (R1, R7), it was taken.

## Project Structure

### Documentation (this feature)

```text
specs/003-dashboard-actions/
├── plan.md                    # This file
├── research.md                # Phase 0 — R1..R7
├── data-model.md              # Phase 1
├── quickstart.md              # Phase 1 — the acceptance procedure
├── contracts/
│   ├── actions.md             # The four mutating routes + the page token
│   └── fleet-stream.md        # The new authenticated stream (FR-019a)
├── checklists/requirements.md
└── tasks.md                   # /speckit-tasks output, not created here
```

### Source Code (repository root)

```text
internal/
├── httpapi/
│   ├── pagetoken.go           # NEW — mint and verify, stateless (R2)
│   ├── pagetoken_test.go      # NEW
│   ├── actions.go             # NEW — the four mutating handlers
│   ├── actions_test.go        # NEW
│   ├── fleet.go               # NEW — the fleet event stream
│   ├── fleet_test.go          # NEW
│   ├── browser.go             # the browser door — action routes register through it
│   ├── stream.go              # crossSite() reused from here (R1)
│   ├── server.go              # route registration
│   └── dashboard.go           # card rendering
├── session/
│   └── manager.go             # Rename and Compact added; Destroy already exists
└── audit/
    └── audit.go               # six new action constants (R5)

web/
├── templates/partials/
│   ├── session-card.html      # action controls, outside the existing anchor
│   └── fleet-events.html      # NEW — the stream client
└── static/crswd.css           # control and outcome styling
```

**Structure Decision**: The existing layout is kept exactly. New behaviour lands in new files
beside their siblings rather than growing existing ones, so a task's blast radius is one file and
AR-008 (no refactoring outside the task) is easy to honour rather than a matter of restraint.

## Phase 0 — Research

Complete. See [research.md](./research.md): R1 same-origin reuse, R2 stateless token, R3 12-hour
lifetime, R4 form posts, R5 audit vocabulary, R6 stream carries identifiers, R7 idempotence from
semantics.

## Phase 1 — Design & Contracts

Complete:

- [data-model.md](./data-model.md) — the page token, the fleet event, the rename and compact fields
- [contracts/actions.md](./contracts/actions.md) — four routes, the token, every failure byte-exact
- [contracts/fleet-stream.md](./contracts/fleet-stream.md) — the new authenticated route (FR-019a)
- [quickstart.md](./quickstart.md) — the acceptance procedure, one section per story

Contracts are written to be **executed rather than interpreted**: literal header names, literal
paths, literal bodies, literal status codes, literal audit action strings, a worked request and
response per route, and a contract-test table whose rows mirror the spec's Verification Map
including the *must fail when* column.

## Complexity Tracking

No constitution violations to justify. Table intentionally empty.
