# Tasks: Dashboard Actions

**Feature**: [spec.md](./spec.md) · **Plan**: [plan.md](./plan.md) · **Contracts**: [contracts/](./contracts/)

23 tasks, each completable and verifiable in one Ralph iteration.

## How to read a task

Every task below names **the file**, **the literals** (quoted from the contracts, not
paraphrased), **the test function**, and **the condition under which that test must fail**. That
last part is not decoration: a test that cannot fail is not verification, and this repository has
shipped code nothing called three times.

### 🔒 Security-critical tasks

**T002, T003, T004** implement the cross-site defence. A mistake in any of them silently removes
the protection this entire milestone exists to add, while every test still passes. Have them
reviewed, or run them on a stronger model. The rest of the list is mechanical once they are right.

### Binding on every task

- **AR-005**: tests must **satisfy** the cross-site checks, never disable them. A test that sets
  `Sec-Fetch-Site: same-origin` and mints a valid token is correct. A test that turns a check off,
  or a build tag that skips one, is the defect this milestone is designed to prevent.
- **AR-008**: no refactoring, renaming, or tidying outside the task at hand, however obvious.
- **Every task ends green**: `go build ./... && go vet ./... && go test ./... && golangci-lint run`.
  Add `-tags tmux` when the task touches tmux, `-tags quickstart` when it touches `cmd/crswd`.
- `go.sum` must never appear.
- **A task is not done when the code exists. It is done when something calls it.**

---

## Phase 1: Setup

- [x] T001 `internal/audit/audit.go`: add six constants, keeping the existing fixed-struct shape and `noun.verb` naming — `ActionDashboardCreate Action = "dashboard.create"`, `ActionDashboardDestroy Action = "dashboard.destroy"`, `ActionDashboardRename Action = "dashboard.rename"`, `ActionDashboardCompact Action = "dashboard.compact"`, `ActionDashboardReject Action = "dashboard.reject"`, `ActionFleetOpen Action = "fleet.open"`. `dashboard.reject` is deliberately **not** `access.reject`: an identity that passed layer 1 and then failed the cross-site check is a different and more alarming event than one that never got in (FR-026). Test `TestDashboardActionsAreDistinctFromAPI` in `internal/audit/audit_test.go` asserts each new value differs from every existing one and that no new value equals `session.create`/`session.destroy`. **Must fail when** a browser action reuses an API action name.

---

## Phase 2: Foundational — blocks every user story

- [x] T002 🔒 **[P]** `internal/httpapi/pagetoken.go`: mint and verify, **stateless** — no map, no sweep, no stored value ([research R2](./research.md)). Format `<expiry>.<mac>` where `mac` is `HMAC-SHA256(pageKey, identity + "\n" + expiry)` as 64 lowercase hex. `pageKey` is 32 bytes from `crypto/rand` at startup, never persisted, never served, and **unrelated to `CRSW_SHARED_SECRET`**. Lifetime 12h ([R3](./research.md)). Verify in the order [data-model.md](./data-model.md#validation) gives, splitting on the **last** `.`, and compare with `hmac.Equal` — never `==`. Tests in `internal/httpapi/pagetoken_test.go`: `TestTokenBoundToIdentity` (a token minted for A fails as B — **must fail when** identity is dropped from the MAC input), `TestTokenExpiryIsCovered` (editing `expiry` forward while keeping the MAC fails — **must fail when** `expiry` is excluded from the MAC input), `TestExpiredTokenRefused` (**must fail when** the expiry comparison is removed or inverted), `TestPageKeyIsNotTheSharedSecret` (**must fail when** `pageKey` is derived from `CRSW_SHARED_SECRET`).

- [x] T003 🔒 `internal/httpapi/browser.go`: the action gate, applied to mutating dashboard routes **before any handler runs and before any state changes**, in this exact order — (1) layer 1 `verifyBrowser`, (2) `crossSite(r)` **reused from `stream.go`**, not a new `Origin` check ([R1](./research.md)), (3) the T002 token from form field `crsw_page_token`. Order is load-bearing: the token check running *after* layer 1 is what makes FR-008 structural rather than bookkeeping — an ended Access session is refused before its token is examined. Any failure of (2) or (3) writes the uniform refusal from [contracts/actions.md](./contracts/actions.md): `403`, `Content-Type: text/html; charset=utf-8`, `X-Content-Type-Options: nosniff`, body exactly `<!doctype html><title>refused</title><p>This action was refused.</p>` — and one `dashboard.reject` record whose `Reason` names the failed check **server-side only**. Tests in `internal/httpapi/actions_test.go`: `TestActionGateOrder` (layer-1 failure never reaches the token check — **must fail when** the order is swapped), `TestRefusalIsByteIdentical` (all five causes — wrong origin, absent origin, missing token, malformed token, expired token — identical status, headers, body **and `Content-Length`** — **must fail when** any cause produces a distinguishable response).

- [x] T004 🔒 `internal/httpapi/dashboard.go` and `web/templates/partials/`: mint one token per page render, bound to that request's **verified** identity, and place it in a hidden field named `crsw_page_token` in every action form. Never in a URL, never in a cookie, never in a `data-` attribute, never logged. Test `TestPageTokenNotInURLsOrLogs` in `internal/httpapi/dashboard_test.go` asserts the rendered page contains the field, that no rendered `href`/`src` contains it, and that a full audit capture of the render contains it zero times. **Must fail when** the token is placed in a link or reaches a record.

- [x] T005 **[P]** `internal/httpapi/actions.go`: the shared not-found used by all four routes — `404`, `Content-Type: text/html; charset=utf-8`, `X-Content-Type-Options: nosniff`, body exactly `<!doctype html><title>not found</title><p>No such session.</p>`. One function, no reason parameter, for the same purpose `refuseBrowser` has none: there is nothing a caller could pass that may change a byte, so the parameter would only be an invitation. Test `TestNotFoundUniform` in `internal/httpapi/actions_test.go`. **Must fail when** unknown and not-owned produce different bytes.

---

## Phase 3: US1 — Destroy from the browser (P1, MVP)

**Independent test**: destroy a session from a card, confirm it is gone from the daemon's own tmux
server, and confirm a cross-origin replay is refused and audited.

- [x] T006 `internal/httpapi/actions.go`: `POST /dashboard/sessions/{id}/destroy`, registered through the T003 gate. `{id}` is 32 lowercase hex; anything else is not a route match. Requires form field `confirm` equal to exactly `yes` (FR-029) — a destroy without it is `400` and **nothing is torn down**. On success uses the existing verified-teardown path and answers `200` with the card fragment. On unverified teardown answers `409` with body `<p class="card-outcome">Teardown could not be verified. This session may still be running on the host.</p>`, **retains the record**, and audits prominently. **There is no force path** (AR-004). Emits exactly one `dashboard.destroy` record. Tests in `internal/httpapi/actions_test.go`: `TestDestroyRequiresConfirm` (**must fail when** the confirm check is removed), `TestDestroyUnverifiedTeardown` using the existing fake reporting survival (**must fail when** the verification is skipped or its result ignored), `TestDestroyCrossOwnerUniform` (byte-identical to unknown id — **must fail when** any action distinguishes them).

- [x] T007 `web/templates/partials/session-card.html` and `web/static/crswd.css`: the destroy control as a form **outside the card's existing anchor** — the card must still contain exactly one `<a>` (FR-027). A submit button is keyboard-operable by construction (FR-028); the focus ring already exists and must not be overridden. Outcome text is a text node, never colour alone (FR-030), and the in-progress, complete and failed states are distinguishable with a failure that **says so rather than silently reverting** (FR-031). Nothing animates under `prefers-reduced-motion` — the universal `transition: none` from #23 already covers this; do not add motion that escapes it. Test `TestCardHasExactlyOneAnchor` in `internal/httpapi/partials_test.go`. **Must fail when** a control is added inside the anchor.

- [x] T008 `internal/httpapi/actions_test.go`: the US1 acceptance suite. `GET` on the destroy path returns the unknown-route response — never `405`, never an `Allow` header (**must fail when** a method-not-allowed path is added). Each half of the defence is disabled **separately in the test build** and the other still refuses (FR-002c, SC-001a) — **must fail when** either half is load-bearing only in combination. **AR-005 applies with full force here**: this task disables checks to prove they work, and must not leave any way to disable one in the shipping build.

---

## Phase 4: US2 — Create from the browser (P2)

**Independent test**: create from the dashboard, confirm it appears and is live on the daemon's
tmux server, and confirm a directory outside the approved roots is refused with nothing created.

- [x] T009 `internal/httpapi/actions.go`: `POST /dashboard/sessions`, form fields `name` (`^[a-zA-Z0-9-]{1,64}$`) and `work_dir`, plus `crsw_page_token`. Reuses milestone 1's validation — clean, `EvalSymlinks`, containment at a path-separator boundary — **calling the existing code rather than reimplementing it** (AR-008). `400` for an invalid name; `400` for traversal, an absolute escape, a symlink escape, or a non-directory, all with **one identical message**, because distinguishing "does not exist" from "not permitted" is a filesystem oracle. `429` at the cap or rate limit, existing sessions unaffected. `500` when tmux fails. **The bearer token minted by the create is discarded without ever reaching a response, a template, or a log** (FR-013). Tests: `TestBrowserCreateNeverServesToken` (search the response **and** the re-rendered fleet page — **must fail when** the token is passed to the template), `TestWorkDirRefusalsAreOneMessage` (**must fail when** the four messages diverge).

- [ ] T010 `web/templates/partials/`: the create form, outside every card. Submit disables itself on submission so a double-click cannot produce two sessions ([R7](./research.md) — the only genuine idempotence exposure of the four actions). Test `TestCreateFormCarriesToken` in `internal/httpapi/partials_test.go`. **Must fail when** the form renders without `crsw_page_token`.

- [ ] T011 `internal/httpapi/actions_test.go`: the US2 acceptance suite — cap reached returns `429` with existing sessions untouched, and one `dashboard.create` record per attempt including the refused ones.

---

## Phase 5: US3 — The fleet stays current (P3, closes #15)

**Independent test**: with a dashboard open, create and destroy a session entirely through the
API, and confirm the open page reflects both without interaction.

- [ ] T012 `internal/session/manager.go`: an event source emitting `appeared`, `vanished`, `changed` for **every** path that changes the fleet — API create, dashboard create, API destroy, dashboard destroy, the reaper, startup adoption, rename, and a `DisplayState` transition. Non-blocking: a slow or absent subscriber must never delay a destroy, a reap, or shutdown. Test `TestEveryFleetChangeEmits` in `internal/session/manager_test.go` drives each path and asserts exactly one event. **Must fail when** any path changes the fleet silently — the reaper is the one most likely to be missed, and it is precisely the case #15 reported.

- [ ] T013 `internal/httpapi/fleet.go`: `GET /dashboard/fleet/stream` per [contracts/fleet-stream.md](./contracts/fleet-stream.md). Layer 1 plus `crossSite`, **no page token** — it mutates nothing, and requiring one would be inconsistent with the pane stream it otherwise mirrors. `Content-Type: text/event-stream; charset=utf-8`, `Cache-Control: no-store`, write deadlines extended per write via `http.ResponseController` — **do not set `WriteTimeout: 0`**. Framing constants reused from `stream.go`. Payload is exactly `{"id":"<32 hex>"}` — no name, path, state, or markup. Ownership filtered **server-side before the event is written**. One `fleet.open` record per open, not per event. Tests in `internal/httpapi/fleet_test.go`: `TestFleetStreamOwnershipFiltered` (a second identity's session produces zero bytes on the first's stream — **must fail when** the ownership filter is removed), `TestFleetPayloadIsIdOnly` (**must fail when** any session field is added), `TestOneRecordPerOpen` (**must fail when** a record is written per event).

- [x] T014 `web/static/crswd.js` and `web/templates/partials/`: subscribe, and on each event re-fetch only the affected card. On a severed stream the page **says so** rather than continuing to present a fleet it cannot vouch for (FR-020). No animation on update (FR-022). Test `TestStreamLossIsVisible` in `internal/httpapi/partials_test.go` asserts the markup carries a disconnected state. **Must fail when** the page keeps presenting the fleet as current.

- [ ] T015 `internal/httpapi/fleet_test.go`: the US3 acceptance suite — an API-created session yields one `appeared`, a reaper destroy yields one `vanished`, a quiet stream past one second yields a heartbeat comment rather than an event, and a `POST` to the stream path returns the unknown-route response.

---

## Phase 6: US4 — Rename (P4)

- [ ] T016 `internal/session/manager.go`: `Rename`, changing **only** the record's display name with the same validation as create. **`TmuxName` is not touched** — it derives from the identifier (`crswd-<id>`), so every tmux target and route parameter is unaffected (FR-015). Names need not be unique; the daemon never addresses a session by name. Emits `changed`. Test `TestRenameLeavesTmuxNameAlone` in `internal/session/manager_test.go`. **Must fail when** rename touches the tmux name.

- [ ] T017 `internal/httpapi/actions.go` + `web/templates/partials/session-card.html`: `POST /dashboard/sessions/{id}/rename`, form field `name`, `200` with the re-rendered card, `400` on an invalid name with the existing name unchanged. One `dashboard.rename` record. Control outside the card's anchor.

- [ ] T018 `internal/httpapi/actions_test.go`: `TestRenameThenIdentifierOperations` — rename, then run **every** identifier-based operation and assert unchanged behaviour (SC-012). **Must fail when** any operation depends on the name.

---

## Phase 7: US5 — Compact (P5)

- [ ] T019 `internal/session/manager.go`: `Compact`, delivering the literal bytes `/compact` followed by a newline via the existing `load-buffer` + `paste-buffer -d` path — **never `send-keys`**, which mangles a trailing `;` (milestone 1 research D4). Touches `LastActivity`: compact is activity and defers the idle deadline exactly as a prompt does. The delivered text is **never** audited or logged (FR-016b, AR-007). Test `TestCompactUsesBufferPath` in `internal/session/manager_test.go` asserts the argv. **Must fail when** it is delivered with `send-keys`.

- [ ] T020 `internal/httpapi/actions.go` + `web/templates/partials/session-card.html`: `POST /dashboard/sessions/{id}/compact`, answering `202` with body exactly `<p class="card-outcome">Compact delivered. The session decides what to do with it.</p>`. **It must say delivered, never compacted** — the daemon cannot see what the assistant is carrying, so claiming the compaction happened asserts something it did not observe (FR-016a). One `dashboard.compact` record carrying no delivered text. Test `TestCompactReportsDeliveryNotSuccess` in `internal/httpapi/actions_test.go`. **Must fail when** the response claims the compaction succeeded.

---

## Phase 8: Polish & cross-cutting

- [ ] T021 `internal/audit/leak_test.go`: extend the leak corpus to all four action routes and the fleet stream, asserting zero occurrences of the shared secret, any bearer token, any page token, prompt text, the literal `/compact`, and pane escape sequences. **Must fail when** any is recorded. The existing suite drives the API and the dashboard reads; nothing yet proves the *action* routes do not leak.

- [ ] T022 Amend `docs/auth-and-sessions.md` and `docs/security.md`: both describe a browser that can only read. Add the action gate, the stateless page token and why it holds no state, and the `403`-versus-`401` distinction. `docs/components.md` gains the action controls its card section currently describes as a parameter that is absent.

- [ ] T023 Run `specs/003-dashboard-actions/quickstart.md` end to end, plus `go test -tags tmux ./...` and `go test -tags quickstart ./cmd/crswd`, and record the outcome in `ralph/PROGRESS.md`. Confirm milestone 1 and 2 acceptance suites pass **unchanged** — if a story needs editing to accommodate this milestone, that is a regression to fix in the code, not in the test.

---

## Dependencies

```
T001 ──┐
T002 ──┼─> T003 ──> T004 ──> T005 ──> US1 (T006-T008)
       │                              │
       │                              ├─> US2 (T009-T011)
       │                              ├─> US4 (T016-T018)
       │                              └─> US5 (T019-T020)
       │
       └─> T012 ──> T013 ──> T014 ──> T015        (US3)
```

- **T001–T005 block everything.** No action route can be built before the gate exists.
- **US1 is the MVP.** It proves the security model; every later action reuses the same gate.
- US2, US4, US5 are independent of each other once US1 is green.
- US3 (T012–T015) is independent of the action stories and may run in parallel with them.

## Parallel opportunities

- T002 and T005 touch different files with no shared dependency — `[P]`.
- Once T008 is green, US2, US4, US5 and US3 are four independent tracks.
- Within US3, T012 must precede T013; T014 needs T013's route.

## Shippable at T008

T001–T008 are the demonstrable MVP and are genuinely deployable on their own: a dashboard that can
destroy a session, with the cross-site defence proven in both halves. Everything after it reuses
that gate without changing it.

## Format validation

All 23 tasks carry a checkbox, a sequential ID, a file path, and — where they add behaviour — a
named test and its must-fail condition. Story labels are omitted in favour of phase headings,
matching the milestone 1 and 2 lists this loop already reads.
