# Implementation Plan

> Worked top-to-bottom by `ralph/loop.sh` — one task per iteration.
>
> **This is milestone 3.** Milestones 1 and 2 are complete, reviewed, and deployed; their
> task lists are archived at [`archive/milestone-1-tasks.md`](archive/milestone-1-tasks.md)
> and [`archive/milestone-2-tasks.md`](archive/milestone-2-tasks.md) because
> `PROGRESS.md` references their T-numbers. Milestone 4 gets its own plan.

## Status: generated from the spec

Generated from [`specs/003-dashboard-actions/tasks.md`](../specs/003-dashboard-actions/tasks.md),
which is the single source of truth. `spec.md`, `plan.md`, `research.md`, `data-model.md`
and the two files in `contracts/` supersede anything this file summarises.

**Before starting a task, read its matching `T0NN` entry in `tasks.md`.** The entries below
are the ordered checklist; the task file carries the exact literals, the test each task must
include, and — for every task that adds behaviour — **the condition under which that test
must fail**. That last part is the load-bearing half. Several tasks look wrong until you read
the reason: a `403` where a `401` seems natural, a compact that reports delivery rather than
success, a token that is deliberately never stored.

## 🔒 Three tasks are security-critical

**T002, T003 and T004** implement the cross-site defence. A mistake in any of them silently
removes the protection this entire milestone exists to add, **while every other test still
passes**. If an iteration is running on a smaller model, stop after each of these and get it
reviewed rather than proceeding on green alone. Everything after T005 is mechanical.

## What is already running

Milestones 1 and 2 are **live**, not merely built. Changes here land on a deployed daemon:

| | |
|---|---|
| Service | `crswd.service`, systemd user unit, loopback `127.0.0.1:8765` |
| tmux | **Its own server**, `-L crswd-<listen>` — never the operator's default server (#22) |
| Public | `https://crswd.craigcloud.io` via the `crswd` Cloudflare Tunnel |
| Edge | Access app `CRSWD Session Control`, two policies — Google identity, and Service Auth for the API client |
| Daemon | Validates the Access assertion itself; the dashboard reads the fleet and streams panes |
| Audit | `journalctl --user -u crswd -o cat \| jq .` |
| Secrets | `op://Lobster/crswd/{shared-secret,access-client-id,access-client-secret}` |

**The dashboard can currently only read — that is what this milestone changes.** Everything
it does today is safe on an ambient Access cookie *because* every mutating route demands an
HMAC signature a browser cannot produce. The first task that adds a write ends that argument,
which is why T002–T005 come before any action.

## Resolved decisions

Answered by the operator or settled in the plan. **Do not re-litigate these in an iteration** —
if one looks wrong, write it in `PROGRESS.md` under `NEEDS CLARIFICATION` and stop.

| Question | Decision | Consequence |
|---|---|---|
| An ambient cookie plus a mutating route is CSRF. What evidence must a write carry? | **Both** a same-origin check and a per-page token bound to the Access identity | Two independent checks. FR-002c requires each to be **separately disableable in a test that then fails** — two checks never tested apart are one check with extra steps |
| Which same-origin mechanism? | **Reuse `crossSite()` from `stream.go`** — `Sec-Fetch-Site`, not a new `Origin` check | Milestone 2 already built it, fail-closed and tested, and it sees the `none` case (a URL opened by no page at all) that `Origin` cannot cleanly distinguish. FR-002a is satisfied by a different mechanism than it names |
| A `SameSite` cookie policy, or a signature the page computes? | **Neither is available** | The Access cookie is Cloudflare's, issued under its own domain policy — outside this project's control and untestable by it. A page-computed HMAC would mean shipping the layer-2 secret to the browser, which hands out the API |
| Where is the page token stored? | **Nowhere.** Self-authenticating: `<expiry>.<HMAC(pageKey, identity + "\n" + expiry)>` | Milestone 2 refused per-browser state on purpose — caching one "would be the daemon's first cross-request browser state, and with it the expiry, invalidation and fixation questions this design exists not to have". A stored token map puts all of that back |
| How does a token die with its Access session? | **By construction, not bookkeeping** | The token check runs *after* layer 1 in the same middleware, so an ended Access session is refused before its token is examined. No record can drift out of step, because there is no record |
| What does "compact" mean? | Deliver Claude Code's own **`/compact`** into the session | The daemon cannot see what the assistant is carrying, so it must report **delivered**, never compacted. Claiming otherwise asserts something it did not observe |
| Does rename touch tmux? | **No** — record only | `TmuxName()` is `crswd-<id>`, so every tmux target and route parameter is unaffected. Read from the code, not assumed |
| How does an open dashboard learn about changes? | A **fleet-level event stream**, contract written first | It is a new authenticated route, and this repo has never added one without a contract. Refreshing only after the page acts was rejected because it leaves #15 unfixed — reaper and API changes still go unseen |

## Conventions

- `- [ ]` open · `- [x]` done · `- [!]` blocked (reason in `PROGRESS.md`)
- Priority order is meaningful — the loop always takes the topmost open item.
- Each task must be independently completable **and verifiable** in one iteration.
- **Every task ends green**: `go build ./... && go vet ./... && go test ./... && golangci-lint run`.
  Add `-tags tmux` when the task touches tmux and `-tags quickstart` when it touches
  `cmd/crswd`. Tests ship inside the task that implements the behaviour — never as a separate
  failing test, which step 6 of `PROMPT.md` would make the iteration revert.
- **Check the linter is v2 before trusting it.** A pre-v2 binary reads this repo's v2 config,
  runs zero linters, and exits 0 — a green that means nothing. The session-start hook warns
  when this is the case; believe it (#26).
- `go.sum` must never appear. An import needs justification under `docs/security.md` §5 first.
- **AR-005: a test satisfies the cross-site checks, it never disables them.** Setting
  `Sec-Fetch-Site: same-origin` and minting a valid token is correct. A build tag or flag that
  turns a check off is the exact defect this milestone exists to prevent — T008 disables them
  deliberately to prove they work, and must leave no way to do so in the shipping build.
- **AR-008: no refactoring outside the task**, however obvious the improvement.
- **A task is not done when the code exists. It is done when something calls it.** This repo
  has shipped that failure three times — a reaper with no caller, `Store.Touch` with no caller,
  and a PR-opener script no workflow invoked.

---

## Tasks

### Setup

- [x] T001 `internal/audit/audit.go`: six new constants — `dashboard.create`, `dashboard.destroy`, `dashboard.rename`, `dashboard.compact`, `dashboard.reject`, `fleet.open`. `dashboard.reject` is deliberately **not** `access.reject`: an identity that passed layer 1 and then failed the cross-site check is a different and more alarming event than one that never got in

### Foundation — the cross-site gate (blocks every story)

- [x] T002 🔒 `internal/httpapi/pagetoken.go`: mint and verify, **stateless** — no map, no sweep. `<expiry>.<HMAC-SHA256(pageKey, identity + "\n" + expiry)>`, `pageKey` 32 random bytes at startup, unrelated to `CRSW_SHARED_SECRET`, never served. `hmac.Equal`, never `==`. Four tests, each with its must-fail condition
- [x] T003 🔒 `internal/httpapi/browser.go`: the gate, in the exact order layer 1 → `crossSite` → token, before any handler and before any state change. **Reuse `crossSite` from `stream.go`; do not add an `Origin` check.** One uniform `403`, byte-identical across all five causes including `Content-Length`; the failed check named **server-side only**
- [x] T004 🔒 `internal/httpapi/dashboard.go` + templates: one token per render, bound to that request's verified identity, in a hidden `crsw_page_token` field. Never in a URL, a cookie, a `data-` attribute, or a log
- [x] T005 `internal/httpapi/actions.go`: the shared uniform `404` for unknown, not-owned, and no-longer-exists. One function, no reason parameter — there is nothing a caller could pass that may change a byte

### US1 — Destroy from the browser (P1, MVP)

- [x] T006 `POST /dashboard/sessions/{id}/destroy`: requires `confirm=yes` (FR-029), verified teardown, `409` with the record **retained** when it cannot be verified. **No force path**
- [x] T007 `web/templates/partials/session-card.html` + CSS: the control **outside** the card's single anchor. Outcome as text, never colour alone; a failure says so rather than silently reverting
- [x] T008 US1 acceptance: `GET` on the path is an unknown route, never `405`. **Each half of the defence disabled separately, the other still refuses.** AR-005 applies hardest here

### US2 — Create from the browser (P2)

- [x] T009 `POST /dashboard/sessions`: reuse milestone 1's validation by **calling it**, not reimplementing it. The four `work_dir` refusals share **one** message — distinguishing "does not exist" from "not permitted" is a filesystem oracle. **The bearer token is discarded, never served**
- [x] T010 The create form; submit disables on submission — the only genuine idempotence exposure of the four actions
- [x] T011 US2 acceptance: `429` at the cap with existing sessions untouched, one record per attempt including refusals

### US3 — The fleet stays current (P3, closes #15)

- [x] T012 `internal/session/manager.go`: an event source covering **every** path that changes the fleet — including the reaper and startup adoption. Non-blocking: a slow subscriber must never delay a destroy or shutdown. The reaper is the case most likely to be missed and is exactly what #15 reported
- [x] T013 `internal/httpapi/fleet.go`: `GET /dashboard/fleet/stream`. Layer 1 + `crossSite`, **no page token** — it mutates nothing. Payload is `{"id":"..."}` only. Ownership filtered **before the event is written**. One `fleet.open` record per open, not per event
- [x] T014 `web/static/crswd.js` + templates: re-fetch only the affected card; a severed stream **says so** rather than presenting a fleet it cannot vouch for
- [x] T015 US3 acceptance: API create yields `appeared`, a reaper destroy yields `vanished`, a quiet stream yields a heartbeat comment

### US4 — Rename (P4)

- [x] T016 `internal/session/manager.go`: `Rename` — record only. **`TmuxName` is not touched**
- [ ] T017 `POST /dashboard/sessions/{id}/rename`, control outside the anchor
- [ ] T018 Rename, then run **every** identifier-based operation and assert unchanged behaviour

### US5 — Compact (P5)

- [ ] T019 `internal/session/manager.go`: `Compact` — `/compact` + newline via `load-buffer` + `paste-buffer -d`, **never `send-keys`**. Touches `LastActivity`. The delivered text is never audited
- [ ] T020 `POST /dashboard/sessions/{id}/compact` → `202`. **Says delivered, never compacted**

### Ship it

- [ ] T021 `internal/audit/leak_test.go`: extend the corpus to all four action routes and the fleet stream. Nothing yet proves the *action* routes do not leak
- [ ] T022 Amend `docs/auth-and-sessions.md`, `docs/security.md` and `docs/components.md` — all three describe a browser that can only read
- [ ] T023 Run `specs/003-dashboard-actions/quickstart.md` end to end plus both tagged suites, and confirm milestone 1 and 2 acceptance passes **unchanged**. A story needing edits to accommodate this milestone is a regression to fix in the code, not in the test

---

## Shippable at T008

T001–T008 are the demonstrable MVP and are deployable on their own: a dashboard that can destroy
a session, with the cross-site defence proven in **both** halves independently. Every later
action reuses that gate without changing it, so the security review happens once, early, on the
smallest surface that exercises it.

T012–T015 close issue #15 and are independent of the action stories — they can run in parallel
if an iteration is blocked elsewhere.

---

## Out of scope

Deliberately NOT in milestone 3, so no iteration wanders into them:

- The Claude device-code login relay, and the `needs-auth` state — milestone 4
- The companion Claude skill
- **Sending arbitrary prompt text from the browser.** This milestone adds four *named* actions;
  a general "type into the session" control is a much larger surface with its own questions
- Editing a session's working directory after creation
- Bulk actions across multiple sessions at once
- Persisting session records, dashboard state, or output history to disk
- Any change to milestone 1's signing procedure, six operations, or audit record shape — the
  API path must keep working **unchanged** (FR-005). This milestone adds a second way to
  authorise a write; it does not alter the first
- Multi-user support beyond one allowlisted identity and the ownership check that already
  exists. The check still runs on every action, and is still tested against a synthetic second
  owner — a check removed because it always passes is a check that will be missing when it
  stops always passing
