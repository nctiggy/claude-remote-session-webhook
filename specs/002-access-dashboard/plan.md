# Implementation Plan: Access Validation & Read-Only Dashboard (Milestone 2)

**Branch**: `002-access-dashboard` | **Date**: 2026-08-04 | **Spec**: [spec.md](./spec.md)

**Input**: Feature specification from `/specs/002-access-dashboard/spec.md`

## Summary

Add layer 1 — daemon-side validation of the Cloudflare Access assertion — and the first
thing worth looking at through it: a read-only dashboard showing every session with its
live screen. Together they are what makes the public hostname safe to create, which is
the only reason `crswd.craigcloud.io` has no DNS record today.

Standard library only, continuing milestone 1's zero-dependency property. The three
decisions that shape this more than the spec does, all from Phase 0: RS256 verification
is ~120 lines of stdlib rather than two dependencies, and being specific is what makes it
stricter than a general library ([D1](./research.md)); `http.ResponseController` clears
the write deadline per response, so streaming does not require weakening the timeout the
other six routes rely on ([D3](./research.md)); and the pane mirrors the current screen
as a JSON-encoded SSE payload assigned with `textContent`, which closes the XSS surface
by construction rather than by escaping ([D4](./research.md)).

## Technical Context

**Language/Version**: Go 1.23 (`go.mod` unchanged). CI pins 1.23; `http.ResponseController`
is 1.20+, so the streaming answer is available.

**Primary Dependencies**: None, still. `go.sum` must not appear —
`TestQuickstartNoDependencies` fails the build if it does. New standard-library surface:
`crypto/rsa`, `math/big`, `html/template`, `embed`.

**Storage**: In-memory, unchanged. No dashboard state, no output history, no persistence.

**Testing**: `go test ./...` table-driven with `t.Parallel()`. Access validation is tested
against a **locally generated RSA key pair** — the tests mint their own assertions, so
there is no network and no fixture that expires. Real-tmux tests stay behind
`//go:build tmux`; the acceptance run stays behind `//go:build quickstart`.

**Target Platform**: Unchanged — systemd user service on loopback behind a cloudflared
tunnel, now with an Access application in front carrying two policies.

**Project Type**: Single Go module; one binary; templates and assets embedded.

**Performance Goals**: Not throughput-bound. One operator, ≤5 sessions, ≤N concurrent
streams (FR-034e). The cost that matters is one `capture-pane` exec per watched session
per second, and identical screens are not re-sent ([D5](./research.md)).

**Constraints**: Loopback bind unchanged. No CORS header on any route (FR-034c) — this
is load-bearing, not hygiene, because the browser credential is an ambient cookie. No
inline script or style; every asset embedded. Milestone 1's signing payload, six
operations, audit shape and uniform 401/404 are frozen.

**Scale/Scope**: 5 user stories, 63 functional requirements, 17 success criteria. One new
package (`internal/access`), one new tree (`web/`), additions to `httpapi`, `config`,
`session` and `audit`.

## Constitution Check

*GATE: evaluated before Phase 0 and re-evaluated after Phase 1 design.*

| Principle | Gate | Pre-Phase 0 | Post-Phase 1 |
|---|---|---|---|
| **I. Security is a gate** (NON-NEGOTIABLE) | Authorization server-side every request; ownership checked; input validated; fail closed on config | PASS — FR-001–011 add layer 1 without relaxing layers 2–3 | **PASS** — algorithm pinned before parsing, audience and issuer pinned, allowlist re-checked in the daemon, uniform refusal, startup failure on missing config. D2 catches the one shape that arrives in production daily |
| **II. Unknowns surfaced, never invented** | No guess stands in for a decision | PASS — three ambiguities raised and answered at specify time | **PASS** — a cross-model review then found four more, including two the spec asserted about code that does not exist; all resolved, and D10 re-verifies every remaining claim against the source |
| **III. Every change is verifiable** | Build, test, lint green; a test that fails without the change | PASS — SC-017 | **PASS** — assertions are minted from a local key pair, so every negative case is testable offline and none expires |
| **IV. Smallest correct change** | No unrequested churn | PASS — read-only, no write path | **PASS** — one new package; no dependency; no state machine added when a derivation would do (D6); milestone 1's constants reused rather than redefined |
| **V. Standards enforced, not documented** | Rules in hooks and CI | PASS | **PASS** — no-CORS and no-`go.sum` are swept by tests, not left to review |
| **VI. Blast radius bounded** (NON-NEGOTIABLE) | Allowlisted dirs, capped sessions, timeouts, verified teardown, loopback | PASS — read-only adds no execution path | **PASS** — and the two new ways to widen it are closed explicitly: streams are capped (FR-034e) and must not advance the idle clock or delay teardown (FR-034f) |
| **VII. Design system is binding** | Tokens only, canonical components, pane output as text | **First milestone where it applies** | **PASS** — with a correction: the canonical pane viewer *documented an XSS*, and `docs/components.md` is amended as part of this work. FR-023/024/024a and SC-009/010/011 make the rest testable |

**Result: PASS.** Complexity Tracking records one deliberate deviation.

Two gate-adjacent notes:

- **A binding document was wrong and is being changed.** `docs/components.md` showed
  htmx's `sse-swap` for pane output, which inserts payloads as markup — the thing
  `docs/security.md` forbids in terms. Under Principle VII an implementer reusing the
  canonical component would have shipped the project's only XSS. Amending a binding
  document is a reviewed change like any other; it is in scope here rather than deferred,
  because leaving it would mean the plan instructs the defect.
- **The dashboard is a second reader of session records**, and milestone 1 advances the
  idle clock inside the single place a request resolves to a session. FR-034f makes that
  no longer universally true. The plan routes dashboard reads around the clock and
  amends the comment that claims otherwise — an inconsistency left in place is one a
  later iteration "fixes" in the wrong direction.

## Project Structure

### Documentation (this feature)

```text
specs/002-access-dashboard/
├── plan.md              # This file
├── research.md          # Phase 0 — 10 decisions; Cloudflare's behaviour verified from docs
├── data-model.md        # Phase 1 — entities, derivations, stream lifecycle
├── quickstart.md        # Phase 1 — runnable validation per story
├── contracts/
│   ├── access-jwt.md    # What is validated, in what order, and what is refused
│   ├── dashboard.md     # Routes, rendering rules, the response headers
│   └── stream.md        # SSE framing, authorisation, lifecycle, limits
├── checklists/
│   └── requirements.md  # Spec quality checklist
└── tasks.md             # Phase 2 — NOT created by /speckit-plan
```

### Source Code (repository root)

Continues milestone 1's layout. One new package, one new tree — both named in `AGENTS.md`.

```text
cmd/crswd/
└── main.go                        # + wire the Access validator and the stream registry

internal/access/                   # NEW — layer 1, and nothing else
├── verify.go                      # RS256 only; alg pinned before anything is parsed
├── claims.go                      # identity vs service-token shapes (research D2)
├── keys.go                        # key set cache; refetch on unknown kid, never per request
├── allowlist.go                   # the daemon's own email check
├── bypass_dev.go                  # //go:build dev      — loopback-only, warns every request
├── bypass_prod.go                 # //go:build !dev     — the bypass does not exist
└── *_test.go                      # assertions minted from a locally generated key pair

internal/config/
└── config.go                      # + CRSW_ACCESS_TEAM_DOMAIN, _AUD, _ALLOWED_EMAILS,
                                   #   _MAX_STREAMS. Fatal when absent (FR-011), except
                                   #   under the dev bypass (FR-042)

internal/httpapi/
├── browser.go                     # NEW — layer-1 middleware; the dashboard's door
├── dashboard.go                   # NEW — GET / and GET /sessions/{id}/view handlers
├── stream.go                      # NEW — SSE; ResponseController; cap; cross-site refusal
├── render.go                      # NEW — html/template set, embedded assets, CSP headers
├── server.go                      # + register dashboard routes; unrouted → dashboard door
└── *_test.go

internal/session/
├── session.go                     # + DisplayState() deriving idle from IdleDeadline (D6)
└── manager.go                     # + a read path that does NOT advance the idle clock

internal/audit/
└── audit.go                       # + actions: dashboard.view, stream.open, access.reject

web/                               # NEW — embedded, no npm
├── templates/
│   ├── layout.html                # shell: header, summary row, grid
│   ├── dashboard.html
│   ├── session.html
│   └── partials/                  # button, status-pill, session-card, pane, header,
│                                  #   empty, rain — the canonical set, action rows absent
└── static/
    ├── crswd.css                  # tokens from docs/design-system.md; no hard-coded values
    └── crswd.js                   # EventSource + textContent; rain canvas. No inline script
```

**Structure Decision**: `internal/access` is a package rather than functions inside
`internal/auth` because the two answer different questions with different failure modes —
`auth` proves a *request* is genuine, `access` proves a *person* is allowed — and because
a key-set cache with a background refetch has no business inside the file that computes
HMACs. `internal/auth` continues to know nothing about browsers.

Dependency direction stays one-way:

```
cmd/crswd → httpapi → {access, auth, session, audit, config}
                          session → tmuxctl (interface)
                          httpapi → web (embedded assets)
```

`access` imports nothing from the project except `audit`. Nothing imports `httpapi`.

## Complexity Tracking

| Deviation | Why | Simpler alternative rejected because |
|---|---|---|
| Hand-rolled RS256 verification instead of a JWT library | `docs/security.md` §5 requires justification for a dependency; milestone 1's zero-dependency property is asserted by a test | A library's value is the algorithms this daemon must **refuse**; its historical CVEs are `alg` confusion and `alg: none`, both of which come from generality. One pinned algorithm is smaller *and* stricter (D1) |

Rejected and recorded so it is not rediscovered:

| Rejected | Looked attractive | Why not |
|---|---|---|
| `WriteTimeout: 0` on the server | One line, makes SSE work | Removes the timeout from all six milestone-1 routes. `ResponseController` lifts it per response instead (D3) |
| htmx `sse-swap` for pane output | It is what `docs/components.md` showed | Inserts the payload as markup — the exact XSS `docs/security.md` forbids. The document was wrong and is corrected |
| A `starting` status pill | The daemon writes that state | The design system has no token for it and forbids inventing one; the distinction lasts one exec. Displayed as running (D6) |
| Wiring `SetState` to make `dead` reachable | Would match the documented state machine | Records are deleted on destroy and reap, so nothing would ever *see* dead. Real state observation is daemon work this milestone does not need |
| A configurable owner mapping | FR-037a's first wording asked for it | Its only correct value is a constant milestone 1 deliberately refuses to make settable; a knob here reproduces the empty-dashboard-with-green-tests failure one level down (D7) |
