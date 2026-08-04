# Implementation Plan

> Worked top-to-bottom by `ralph/loop.sh` — one task per iteration.
>
> **This is milestone 2.** Milestone 1 is complete, reviewed, and deployed; its task
> list is archived at [`archive/milestone-1-tasks.md`](archive/milestone-1-tasks.md)
> because `PROGRESS.md` references its T-numbers. Milestones 3–4 get their own plans.

> ✅ **Unblocked.** Iteration 1 of milestone 1 could not run Bash at all —
> `.claude/settings.json` had no `permissions` block. An operator added one in
> `902c249`, so build, test, lint, and commit all work from inside an iteration. Do
> not re-raise this.

## Status: generated from the spec

Generated from [`specs/002-access-dashboard/tasks.md`](../specs/002-access-dashboard/tasks.md),
which is the single source of truth. `spec.md`, `plan.md`, `research.md`, `data-model.md`
and the three files in `contracts/` supersede anything this file summarises.

**Before starting a task, read its matching `T0NN` entry in `tasks.md`.** The entries
below are the ordered checklist; the task file carries the file paths, the test each task
must include, and the reason behind the non-obvious ones. Several look wrong until you
read the reason — `alg` read only to reject, "no email" not meaning allow, a pane that
replaces rather than appends.

## What is already running

Milestone 1 is **live**, not merely built. Changes here land on a deployed daemon:

| | |
|---|---|
| Service | `crswd.service`, systemd user unit, loopback `127.0.0.1:8765` |
| Public | `https://crswd.craigcloud.io` via the `crswd` Cloudflare Tunnel |
| Edge | Access app `CRSWD Session Control`, two policies — Google identity, and Service Auth for the API client |
| Audit | `journalctl --user -u crswd -o cat \| jq .` |
| Secrets | `op://Lobster/crswd/{shared-secret,access-client-id,access-client-secret}` |

**The daemon does not yet validate the Access assertion — that is this milestone.**
Today the edge is the only thing checking who the browser is, and HMAC is the only thing
the daemon checks. Landing US1 is what makes the layering in `docs/auth-and-sessions.md`
real rather than described.

## Resolved decisions

Answered by the operator. **Do not re-litigate these in an iteration** — if one looks
wrong, write it in `PROGRESS.md` under `NEEDS CLARIFICATION` and stop.

| Question | Decision | Consequence |
|---|---|---|
| Access refuses the API client as readily as a stranger. How do two front doors share one hostname? | One hostname, two edge policies: identity for browsers, a **service token** for the API client. No path gets an edge bypass | The client sends two extra headers. Every path stays edge-protected, and each door still faces its own daemon-side check |
| A browser stream can carry neither the signature nor the per-session token. What authorises it? | The validated browser identity **plus** the ownership check, re-evaluated rather than established once. No credential in the URL | The per-session token is deliberately not required. It exists to tell apart callers who all authenticate as one shared secret; a verified per-person identity plus ownership already makes that distinction |
| Do the browser identity and the API identity resolve to one owner? | Yes, by construction — **not** a configuration knob | Its only correct value is the constant milestone 1 refuses to make settable. The comparison still runs, and is still tested against a synthetic second owner |
| The design system defines four display states; the daemon produces two | Derive at render time: **idle** from `Session.IdleDeadline()`, **running** otherwise, `dead` never | `Store.SetState` has no production caller. `docs/design-system.md` was amended to describe what can actually occur |
| Append-only transcript, or the current screen? | The **current screen**, replaced on each update | A Claude session is a full-screen program. Diffing repaints into appended lines was the review's pick for where this milestone would lose days |
| A JWT library, or hand-rolled verification? | **Hand-rolled**, standard library only | A library's value is the algorithms this daemon must refuse. `go.sum` still must not exist |

## Conventions

- `- [ ]` open · `- [x]` done · `- [!]` blocked (reason in `PROGRESS.md`)
- Priority order is meaningful — the loop always takes the topmost open item.
- Each task must be independently completable **and verifiable** in one iteration.
- **Every task ends green**: `go build ./... && go vet ./... && go test ./... && golangci-lint run`.
  Tests ship inside the task that implements the behaviour — never as a separate failing
  test, which step 6 of `PROMPT.md` would make the iteration revert.
- `go.sum` must never appear. An import needs justification under `docs/security.md` §5
  first, and this milestone already decided against the obvious one.
- **A task is not done when the code exists. It is done when something calls it.**
  Milestone 1 shipped a reaper and an idle clock that were fully implemented, fully
  tested, and never wired — found only by a cross-model review after the list was ticked.

---

## Tasks

### Setup

- [x] T001 `internal/config/config.go`: add `CRSW_ACCESS_TEAM_DOMAIN`, `CRSW_ACCESS_AUD`, `CRSW_ACCESS_ALLOWED_EMAILS`, `CRSW_MAX_STREAMS`. Fatal when absent or malformed, except under the dev bypass. Table test every case
- [x] T002 `web/` tree plus `go:embed` and template-set parsing in `internal/httpapi/render.go`, so a broken template fails at startup rather than on a request. The directives themselves sit in `web/embed.go`: a `go:embed` pattern cannot name a path outside its own file's tree

### Foundation — layer 1 (`internal/access`) and the audit actions

- [x] T003 `internal/access/keys.go`: key set fetched from the team domain, cached, refetched **only on an unknown kid**, with a refetch floor. Unobtainable keys **refuse** — an identity that cannot be verified is not an identity
- [x] T004 `internal/access/verify.go`: RS256 verification in the exact order `contracts/access-jwt.md` gives. **`alg` is read only to reject anything that is not RS256, never to select a verifier.** Claims parsed only after the signature verifies. Tests mint from a local key pair, so nothing expires and nothing needs the network
- [x] T005 `internal/access/claims.go`: both assertion shapes. Identity carries `email`; a service token carries `common_name`, empty `sub`, and **no email**. The dashboard requires a non-empty allowlisted email — **"no email" must never read as "allow"**, or every API call the operator makes is admitted to the dashboard
- [x] T006 `internal/access/allowlist.go`: the daemon's own email check. The edge is the gate; this asserts the gate is configured as believed. The refused address never reaches the trail
- [x] T007 **[delivers US5]** `internal/access/bypass_{dev,prod}.go`: skips layer 1 only, refuses off loopback, warns every request, and is **absent** from the shipping build. In Foundation because there is no Access in front of a laptop — without it nothing here can be developed locally
- [x] T008 `internal/audit/audit.go`: add `access.reject`, `dashboard.view`, `dashboard.asset`, `stream.open`, keeping the fixed-struct shape

### US1 — See every session at a glance (P1, MVP)

- [x] T009 `internal/httpapi/browser.go`: the browser door — layer 1 on every dashboard route, one audit record per request, one uniform refusal that reveals nothing about which check failed
- [x] T010 `internal/httpapi/render.go`: the security headers from `docs/security.md` verbatim, and **no `Access-Control-Allow-*` on any route, either door**. That absence is load-bearing: the browser credential is an ambient cookie, so same-origin policy is the protection. Swept across every registered route
- [ ] T011 `internal/session/session.go`: `DisplayState()` — idle from `IdleDeadline()`, running otherwise. Calls milestone 1's own method so the dashboard and the reaper cannot disagree
- [ ] T012 `internal/session/manager.go`: a read that resolves ownership **without advancing the idle clock**, and amend `Resolve`'s comment claiming to be the only path from a request to a session — this milestone makes that false
- [ ] T013 `web/templates/partials/`: header with the verified identity top-right, status pill, session card, empty state, rain canvas. The documented action rows are a **parameter that is absent here**, not deleted code milestone 3 must restore
- [ ] T014 `internal/httpapi/dashboard.go`: `GET /` — state summary before detail, one card per session **the viewer owns**, read through T012's non-touching path. Adopted sessions have no name and no working directory; render that as an explicit unknown, never a plausible-looking placeholder
- [ ] T015 `web/static/crswd.css`: tokens from `docs/design-system.md` only — no hard-coded colour, size or font anywhere. Focus ring, one breakpoint, and `prefers-reduced-motion` removing the rain entirely
- [ ] T016 `internal/httpapi/server.go`: unmatched paths answer through the **browser** door, so a signed-in operator who mistypes a URL does not get the API's raw JSON
- [ ] T017 US1 acceptance suite: zero external origins in the rendered markup, every state distinguishable without colour, and the cross-owner refusal exercised **through the dashboard's own route** with a synthetic second owner — milestone 1's API test does not satisfy this

### US3 — A browser that should not be here gets nothing (P3)

- [ ] T018 The full negative sweep from `contracts/access-jwt.md`: absent, malformed, expired, future-dated, wrong signature, `alg: none`, `alg: HS256`, unknown kid, wrong aud, wrong iss, disallowed email, and **a valid service-token assertion presented to the dashboard**. All byte-identical; the reason server-side only
- [ ] T019 Fail-closed end to end: with the key set unobtainable every browser request is refused rather than admitted, and the daemon neither crashes nor hangs

### US4 — My existing API client keeps working (P4)

- [ ] T020 Non-regression guard: an API request is not refused for carrying no browser identity, and a browser request is not refused for carrying no signature. Each door refuses only by the check that applies to it
- [ ] T021 Run `go test -tags quickstart ./cmd/crswd` and confirm milestone 1's acceptance suite passes **unchanged**. If a story needs editing to accommodate this milestone, that is a regression to fix in the code, not in the test

### US2 — Watch a session's output as it happens (P2)

> **Do not begin until US1, US3 and US4 are green.** This phase carries nearly all of the
> milestone's unresolved risk, and the design review's recommendation was explicit.

- [ ] T022 `internal/httpapi/stream.go`: clear the write deadline **per response** with `http.NewResponseController`. **Do not set `WriteTimeout: 0`** — that strips the timeout from the six routes milestone 1 shipped with it
- [ ] T023 The ordered open sequence: identity → cross-site refusal on `Sec-Fetch-Site` (present-and-wrong refuses; absent does not) → ownership via T012's non-touching read → capacity **last**, so an unauthorised caller never observes the cap's state
- [ ] T024 Capture on a 1s tick, emitting **only when the screen changed**, with a comment heartbeat on suppressed ticks. An idle session would otherwise push an identical screen every second to every open tab forever
- [ ] T025 Frame each event as **one JSON string holding the whole screen**. SSE is line-oriented, so a raw newline would start a new field; encoding makes framing independent of content
- [ ] T026 `web/templates/partials/pane.html` and `web/static/crswd.js`: `pane.textContent = JSON.parse(e.data)` — **the screen is replaced, not appended**, and updating never moves the reader's scroll position. `docs/components.md` was corrected twice for this
- [ ] T027 Emit the audit record at stream **open**, carrying the authorisation decision. Milestone 1 emits after the handler returns, which for a stream lasting hours leaves no trace until it ends
- [ ] T028 Lifecycle: re-evaluate authorisation rather than establishing it once; stop and say so when the session ends; **never advance the idle clock**; never delay teardown or shutdown. Watching is not driving
- [ ] T029 US2 acceptance suite: the cap refuses past `CRSW_MAX_STREAMS`, two tabs both work, a vanished browser is cleaned up, and session output appears in **zero** audit records or log lines

### Ship it

- [ ] T030 Amend `docs/auth-and-sessions.md`: it still says there are no browser sessions and no human login form, its layer-1 sample uses a JWT library, and its two-door table has no service token. Add the stream-authorisation rule, which currently exists only in the spec
- [ ] T031 Amend `docs/security.md`: the two-door table predates the service token, and the header table says nothing about cross-origin headers, which are now load-bearing
- [ ] T032 `AGENTS.md`: add `go test -tags tmux ./...` and `go test -tags quickstart ./cmd/crswd` to the command table. "Test (all)" names neither suite that touches real tmux — carried unfixed for 43 iterations
- [ ] T033 Document the new variables in `.env.example` (names only, never a value) and in `deploy/README.md`
- [ ] T034 Run the full `specs/002-access-dashboard/quickstart.md` end to end — it needs no Cloudflare account — and record the outcome in `PROGRESS.md`

---

## Shippable at T021

T001–T017 are the demonstrable MVP, and unlike milestone 1 this one is genuinely
deployable: it adds a check without adding an execution path, so landing it strictly
improves the current posture. The hostname is already live behind edge-only Access;
US1 is what makes the daemon validate the identity itself.

T018–T021 then prove the refusals and confirm milestone 1 is intact. US2 is additive
after that.

---

## Out of scope

Deliberately NOT in milestone 2, so no iteration wanders into them:

- Create, destroy, rename, and compact from the dashboard — milestone 3. It will need a
  cross-site-request answer of its own: this milestone's streams are authorised by an
  ambient cookie, which is safe **only** because every mutating route requires a
  signature a browser cannot produce. That reasoning does not survive the first
  browser-driven write and must not be inherited by assumption
- The Claude device-code login relay, and the `needs-auth` state — milestone 4
- The companion Claude skill
- Persisting session records, dashboard state, or output history to disk
- Any change to milestone 1's signing procedure, six operations, or audit record shape
- Real state observation in the daemon. `Store.SetState` stays uncalled; display state
  is derived. Wiring it would be daemon work this milestone does not need
- Multi-user support beyond one allowlisted identity and the ownership check that
  already exists
