---

description: "Task list for Access validation and the read-only dashboard (milestone 2)"
---

# Tasks: Access Validation & Read-Only Dashboard (Milestone 2)

**Input**: Design documents from `/specs/002-access-dashboard/`

**Prerequisites**: [plan.md](./plan.md), [spec.md](./spec.md), [research.md](./research.md), [data-model.md](./data-model.md), [contracts/](./contracts/), [quickstart.md](./quickstart.md)

## Format: `[ID] [P?] [Story] Description`

- **[P]**: Touches only files no other open task touches
- **[Story]**: Which user story the task serves (US1–US5)
- Every task names exact file paths

---

## How these tasks are shaped, and why

Same rules as milestone 1, for the same reason. `ralph/loop.sh` runs **one task per
iteration with a fresh context**, and step 6 of `ralph/PROMPT.md` reverts an iteration
whose tests do not pass. So:

1. **Tests ship inside the task that implements the behaviour.** A red-first task would
   delete itself.
2. **Order is strictly sequential.** `[P]` markers are for a human team; Ralph goes top
   to bottom.
3. **Every task ends green**: `go build ./... && go vet ./... && go test ./... && golangci-lint run`.

**US2 is deliberately last.** The design review's judgement was that this is
one-and-a-half milestones: layer 1 plus the static dashboard is a coherent shippable
unit, and the stream carries nearly all the unresolved risk. Nothing in US2 may begin
until US1 and US3 are green and deployable.

### The eight things a fresh context will get wrong

Each is written into the task that touches it, but they are collected here because every
one of them is a decision that *looks* like a mistake until you know the reason:

| Trap | The rule | Why |
|---|---|---|
| `alg` | Read it **only to reject** anything that is not `RS256`. Never to select a verifier | Selecting on `alg` is the entire class of JWT break — `none` and algorithm confusion |
| Service-token assertions | Require a **non-empty allowlisted `email`**. "No email" must never read as "allow" | Every API call produces one of these. `sub` is empty and `common_name` is set. The wrong reading admits all of them to the dashboard, and passes any test that only tries a valid identity token |
| The pane | **Mirrors** the current screen: `pane.textContent = JSON.parse(e.data)`. It does not append | A Claude session is a full-screen program. `docs/components.md` was corrected twice for this — once for the XSS, once for the framing |
| Write deadline | `http.NewResponseController(w).SetWriteDeadline(time.Time{})` on the stream route | `WriteTimeout: 0` removes the timeout from the six routes milestone 1 shipped with it |
| Display state | Derived from `Session.IdleDeadline()`. Never stored | `Store.SetState` has **no production caller**; `StateDead` is never written. A stored field would show one label forever |
| CORS | **No `Access-Control-Allow-*` header on any route, ever** | The browser credential is an ambient cookie. Same-origin policy is the protection, and it holds only while the daemon never opts out |
| The idle clock | A dashboard read must **not** advance it | `Manager.Resolve` does, and its comment claims to be the only path from a request to a session. This milestone makes that false |
| Dependencies | Zero. `go.sum` must not appear | `TestQuickstartNoDependencies` fails the build if it does |

---

## Phase 1: Setup

- [x] T001 Add the layer-1 configuration to `internal/config/config.go`: `CRSW_ACCESS_TEAM_DOMAIN`, `CRSW_ACCESS_AUD`, `CRSW_ACCESS_ALLOWED_EMAILS` (comma-separated, at least one), `CRSW_MAX_STREAMS` (default 10). Each is **fatal when absent or malformed** (FR-011), matching how the shared secret already behaves — except under the dev bypass, where they are not required (FR-042), because demanding an audience the bypass then ignores would make local development need a Cloudflare account. Table test every missing/invalid case in `internal/config/config_test.go`, including that the values never appear in an error string.
- [x] T002 [P] Create the `web/` tree and embed it: `web/templates/` and `web/static/` with `go:embed` in `internal/httpapi/render.go`, plus template-set parsing at construction so a broken template fails at startup rather than on a request. A placeholder page is enough here — the real markup arrives in US1. Test in `internal/httpapi/render_test.go` that the embed compiles, every template parses, and parsing failure is fatal. **Amended in flight:** the `go:embed` directives live in `web/embed.go`, not in `render.go` — a pattern may not name a path outside its own file's directory tree, and `web/` is at the repository root. `render.go` owns the parsing, which is what this task is about, and the dependency `plan.md` draws (`httpapi → web`) is unchanged.

---

## Phase 2: Foundational (Blocking Prerequisites)

**Purpose**: The `internal/access` package — layer 1 and nothing else — plus the audit
actions everything downstream emits.

**⚠️ CRITICAL**: No user story work can begin until this phase is complete.

**US5 is delivered here, by T007.** It sits in Foundational rather than in a phase of
its own because every other story needs it to be developable at all — there is no
Cloudflare Access in front of a laptop, so without the bypass the dashboard cannot be
run locally. It keeps the `[delivers US5]` marker so the story is traceable to a task
rather than silently absent from the list.

- [x] T003 Implement the signing-key set in `internal/access/keys.go`: fetch `https://<team>.cloudflareaccess.com/cdn-cgi/access/certs`, cache it, refetch **only on an unknown key id** and never per request (FR-008), with a refetch floor so an unknown-kid flood cannot become an outbound request flood. When keys cannot be obtained, **refuse** (FR-009) — an identity that cannot be verified is not an identity. Test in `internal/access/keys_test.go` against an `httptest` server: cached hit does not refetch, unknown kid refetches once, floor is honoured, unreachable server fails closed, concurrent first fetches do not stampede.
- [x] T004 Implement RS256 verification in `internal/access/verify.go` following the ordered sequence in [contracts/access-jwt.md](./contracts/access-jwt.md) exactly. **The `alg` header is read only to reject anything that is not `RS256`, and is never used to select a verifier** — that inversion is the whole reason this is hand-written rather than a library (see [research D1](./research.md)). A `crit` header is refused. Claims are parsed **only after** the signature verifies, because until then they are attacker-authored bytes. Tests in `internal/access/verify_test.go` mint assertions from a **locally generated RSA key pair**, so every negative case is offline and none expires: valid, `alg: none`, `alg: HS256` signed with the public key as an HMAC secret, wrong key, tampered payload, `crit` present, malformed segments.
- [x] T005 Implement claim handling in `internal/access/claims.go` for **both** documented assertion shapes ([research D2](./research.md)): an identity assertion carries `email`; a service-token assertion carries `common_name`, an **empty `sub`**, and **no `email` at all**. The dashboard requires a non-empty `email` — **"no email" must never read as "allow"**. This is not hypothetical: every API call the operator's client makes produces a service-token assertion, and a validator written as "reject if email is present and not allowed" admits all of them. Verify `aud`, `iss`, `exp`, `nbf`/`iat` with the documented leeway. Test both shapes in `internal/access/claims_test.go`, including a service-token assertion presented to the dashboard.
- [x] T006 [P] Implement the daemon-side allowlist in `internal/access/allowlist.go`: the address from a verified assertion must appear in the daemon's own configured list (FR-007). The edge is the gate; this is the daemon's assertion that the gate is configured as believed. A refusal is recorded with a repo-authored constant reason and **never** the address itself, following milestone 1's discipline that trail reasons are never caller-supplied text. Test in `internal/access/allowlist_test.go`.
- [x] T007 **[delivers US5]** Implement the development bypass across `internal/access/bypass_dev.go` (`//go:build dev`) and `internal/access/bypass_prod.go` (`//go:build !dev`). It skips **layer 1 only** — layers 2 and 3 stay enforced (FR-038); it refuses to operate unless the listener is loopback (FR-039); it warns on **every request**, not only at startup (FR-040); and it is **absent** from the shipping artifact, excluded at build time rather than defaulted off (FR-041), because a production binary that can disable authentication by flag is a backdoor. Test in `internal/access/bypass_test.go`, plus a check that the default build exposes no bypass symbol.
- [x] T008 [P] Add the milestone's audit actions to `internal/audit/audit.go`: `access.reject`, `dashboard.view`, `dashboard.asset`, `stream.open`. Keep the fixed-struct shape — no `map[string]any`, no free-form attachment — so a record still cannot carry arbitrary data. Test in `internal/audit/audit_test.go` that each new action serialises in the existing shape.

**Checkpoint**: layer 1 verifies offline against a local key pair, and refuses everything it should.

---

## Phase 3: User Story 1 — See every session at a glance (Priority: P1) 🎯 MVP

**Goal**: A browser carrying a valid, allowed Access identity sees the fleet. Everything else sees nothing.

**Independent Test**: With an assertion minted from the quickstart's local key pair, load the dashboard and see a state summary plus one card per owned session, with the verified identity in the header. Without one — or with a forged, expired, wrong-audience, or service-token assertion — get the uniform refusal.

- [x] T009 [US1] Implement the browser door in `internal/httpapi/browser.go`: a middleware validating layer 1 on every dashboard route, emitting exactly one audit record per request, and returning **one uniform refusal** that reveals nothing about which check failed (FR-010). Test in `internal/httpapi/browser_test.go` that every failure shape produces a byte-identical response including headers.
- [x] T010 [US1] Send the security headers in `internal/httpapi/render.go` exactly as [contracts/dashboard.md](./contracts/dashboard.md) lists them — the CSP from `docs/security.md` verbatim, `nosniff`, `no-referrer`, HSTS, and `Cache-Control: no-store` on pages. **No `Access-Control-Allow-*` header on any route, on either door.** That absence is load-bearing rather than tidy: the browser's credential is an ambient cookie that rides on requests a hostile page triggers, so same-origin policy is what stops that page reading the response ([research D8](./research.md)). Test with a sweep over **every** registered route asserting the headers are present and zero CORS headers appear anywhere.
- [x] T011 [P] [US1] Add `DisplayState()` to `internal/session/session.go`, deriving **idle** when `now` is at or past `Session.IdleDeadline()` and **running** otherwise (FR-019a–c). It calls milestone 1's own method rather than defining a second threshold, so the dashboard and the reaper cannot disagree about which sessions are idle. `starting` displays as running — the distinction lasts one `tmux` exec — and `dead` is never displayed, because `Store.SetState` has no production caller and destroyed sessions leave no record. Table test in `internal/session/session_test.go` either side of the threshold.
- [x] T012 [US1] Add a session read to `internal/session/manager.go` that resolves ownership **without advancing the idle clock**, for the dashboard and the stream to use. Amend `Manager.Resolve`'s comment claiming to be the only path from a request to a session — this milestone makes that false, and an inconsistency left in place is one a later iteration "fixes" in the wrong direction. Test in `internal/session/manager_test.go` that the dashboard path leaves `LastActivity` untouched while the API path still advances it.
- [x] T013 [US1] Build the canonical components in `web/templates/partials/` — header (verified identity top-right), status pill, session card, empty state, rain canvas — per `docs/components.md`. The card and empty state are documented **with action rows**; those MUST be a parameter that is simply absent here (FR-024a), not deleted code milestone 3 has to restore. A browser cannot sign an API request, so the actions would be non-functional as well as out of scope. Test that rendering a card emits no `hx-post`, `hx-delete`, or button element.
- [x] T014 [US1] Implement `GET /` in `internal/httpapi/dashboard.go`: a state summary row before any detail, then one card per session **the viewer owns** (FR-017), read through the non-touching path from T012. Adopted sessions have **no name and no working directory** — milestone 1 records neither, on purpose — so render that absence as an explicit "unknown" rather than a placeholder that reads like a real value (FR-018a). Test in `internal/httpapi/dashboard_test.go` including the adopted-session case and the empty fleet.
- [x] T015 [P] [US1] Write `web/static/crswd.css` from the tokens in `docs/design-system.md`. **No hard-coded colour, size, or font anywhere** (FR-023) — if a value is not a token, add it to the design system first. Include the focus ring, the single 780px breakpoint, and the `prefers-reduced-motion` rule that removes the rain entirely. Test that the stylesheet contains no literal hex colour outside the token block.
- [x] T016 [US1] Route unmatched paths to the **browser** door in `internal/httpapi/server.go` (FR-013d), so a signed-in operator who mistypes a URL gets the dashboard's refusal rather than the API's raw JSON. This is a deliberate amendment to milestone 1's catch-all, which currently answers everything behind layer 2. Test that an unknown path under a valid identity renders the dashboard's not-found, and that the API's own routes are unaffected.
- [ ] T017 [US1] Add the US1 acceptance suite in `internal/httpapi/dashboard_test.go` covering [contracts/dashboard.md](./contracts/dashboard.md)'s test table: rendered markup references **zero external origins** (SC-005), every state carries a text label and is distinguishable without colour (SC-009), the cross-owner refusal is exercised **through the dashboard's own route** with a synthetic second owner (FR-037b — pointing at milestone 1's API test does not satisfy this), and the identity in the header comes from the assertion and never from a request field.

**Checkpoint**: the dashboard is deployable. This is the point at which the milestone could ship.

---

## Phase 4: User Story 3 — A browser that should not be here gets nothing (Priority: P3)

**Goal**: Prove the layering holds when the edge does not.

**Independent Test**: Present every shape of bad assertion directly to the daemon's listener and get the same refusal for each, with the reason server-side only.

- [ ] T018 [US3] Add the full negative sweep in `internal/access/verify_test.go` and `internal/httpapi/browser_test.go`, covering every row of [contracts/access-jwt.md](./contracts/access-jwt.md)'s test table: absent, malformed, not three segments, expired, future-dated, wrong signature, `alg: none`, `alg: HS256`, unknown `kid`, wrong `aud`, wrong `iss`, disallowed email, and **a valid service-token assertion presented to the dashboard**. Assert all responses are byte-identical (SC-001) and that the audit trail records the distinct reason for each while the caller learns nothing.
- [ ] T019 [US3] Assert the fail-closed path end to end in `internal/httpapi/browser_test.go`: with the key set unobtainable, every browser request is refused rather than admitted (FR-009, SC-013), and the daemon does not crash or hang waiting on it.

**Checkpoint**: layer 1 is proven, not asserted.

---

## Phase 5: User Story 4 — My existing API client keeps working (Priority: P4)

**Goal**: Milestone 1 does not regress.

**Independent Test**: Run milestone 1's own acceptance suite unchanged and get the same results.

- [ ] T020 [US4] Add a non-regression guard in `internal/httpapi/server_test.go`: an API request carrying **no** browser identity is not refused for lacking one, and a browser request carrying **no** signature is not refused for lacking one (FR-012). Each door refuses only by the check that applies to it. Assert milestone 1's six routes keep their exact status codes and response bodies (FR-015).
- [ ] T021 [US4] Run `go test -tags quickstart ./cmd/crswd` and confirm milestone 1's acceptance suite passes **unchanged** (SC-007). If a story needs editing to accommodate this milestone, that is a regression to fix in the code, not in the test — record the outcome in `ralph/PROGRESS.md`.

**Checkpoint**: the shipped contract is intact.

---

## Phase 6: User Story 2 — Watch a session's output as it happens (Priority: P2)

**Goal**: The live screen, mirrored, as text.

**⚠️ Do not begin until US1, US3 and US4 are green.** This phase carries nearly all of the milestone's unresolved risk.

**Independent Test**: Produce output containing markup, escapes and a script element; watch it arrive without a reload, rendered as visible text with nothing executed.

- [ ] T022 [US2] Implement the SSE response in `internal/httpapi/stream.go`, clearing the write deadline **per response** with `http.NewResponseController(w).SetWriteDeadline(time.Time{})` ([research D3](./research.md)). **Do not set `WriteTimeout: 0`** — that removes the timeout from the six routes milestone 1 shipped with it. `internal/httpapi/server.go:40` carries the note this answers. Test that the stream survives past the server's write timeout while an ordinary route still times out.
- [ ] T023 [US2] Implement the ordered open sequence from [contracts/stream.md](./contracts/stream.md) in `internal/httpapi/stream.go`: validated **identity** assertion (a service-token assertion is refused here as everywhere on this door) → cross-site refusal when `Sec-Fetch-Site` is present and not `same-origin`, treated as *present-and-wrong means refuse* never *absent means refuse* → ownership through the non-touching read from T012 → capacity **last**, so an unauthorised caller can never observe the cap's state. Count and admit in one critical section, closing the same race `Store.AddCapped` closes. Test each step's refusal in `internal/httpapi/stream_test.go`.
- [ ] T024 [US2] Implement the capture loop in `internal/httpapi/stream.go`: capture on a 1s tick and emit **only when the screen differs from the last one sent** ([research D5](./research.md)) — an idle session would otherwise push an identical screen every second to every open tab forever. Emit a comment heartbeat on suppressed ticks so an intermediary does not time the connection out. Test with the fake controller that identical captures produce no event and a changed capture produces exactly one.
- [ ] T025 [US2] Frame each event in `internal/httpapi/stream.go` as **one JSON string holding the whole screen** ([research D4](./research.md)). SSE's wire format is line-oriented, so a raw newline would start a new field; JSON encoding makes the framing independent of the content and guarantees the client receives a string. Test that a screen containing newlines, a lone `\r`, `<script>`, and quotes arrives intact and re-parses to the identical bytes.
- [ ] T026 [US2] Build the pane in `web/templates/partials/pane.html` and `web/static/crswd.js`: `pane.textContent = JSON.parse(e.data)` — **the screen is replaced, not appended**. `docs/components.md` was corrected twice for this, once for the XSS (an htmx swap inserts payloads as markup) and once for the framing. Updating must never move the reader's scroll position, since a repainting screen has no bottom to follow (FR-032). Test that output containing markup renders as visible text with zero execution (SC-004).
- [ ] T027 [US2] Emit the audit record at stream **open**, carrying the authorisation decision (FR-016a) — milestone 1 emits after the handler returns, which for a stream lasting hours means a daemon that dies mid-stream leaves no trace that session output was being read. One record per stream, no close record. Test in `internal/audit/audit_test.go`.
- [ ] T028 [US2] Enforce the stream lifecycle in `internal/httpapi/stream.go` (FR-034b, FR-034f): re-evaluate authorisation rather than establishing it once, stop delivering when the session ends or expires and say so in the view, **never advance the idle clock**, and never delay session teardown or daemon shutdown. Watching is not driving — a stream that postponed the idle deadline would let a forgotten browser tab hold an unsandboxed shell open indefinitely. Test that a reaped session ends its stream and that shutdown with open streams completes within the budget.
- [ ] T029 [US2] Add the US2 acceptance suite in `internal/httpapi/stream_test.go` covering [contracts/stream.md](./contracts/stream.md)'s test table: the cap refuses past `CRSW_MAX_STREAMS`, two tabs on one session both work, a vanished browser is cleaned up, session output appears in **zero** audit records or log lines (FR-035), and the cross-site refusal fires.

**Checkpoint**: all five stories independently functional.

---

## Phase 7: Polish & Cross-Cutting Concerns

- [ ] T030 [P] Amend `docs/auth-and-sessions.md` — it is binding and this milestone makes it wrong in three ways. It opens "There are no browser sessions and no human login form" (this milestone creates both); its layer-1 code sample uses a JWT library, contradicting the zero-dependency decision ([research D1](./research.md)); and its two-door table has no service token. Add the stream-authorisation rule, which currently exists only in the spec.
- [ ] T031 [P] Amend `docs/security.md` — its two-door table predates the service token, and its header table says nothing about cross-origin headers, which FR-034c now makes load-bearing rather than incidental.
- [ ] T032 [P] Add the missing commands to `AGENTS.md`'s table: `go test -tags tmux ./...` and `go test -tags quickstart ./cmd/crswd`. "Test (all)" currently names neither of the two suites that touch real tmux — a finding milestone 1 carried for 43 iterations without fixing.
- [ ] T033 [P] Document the new environment variables in `.env.example` (names and descriptions only, never a value) and in `deploy/README.md` alongside the Access service token already recorded there.
- [ ] T034 Run the full [quickstart.md](./quickstart.md) validation end to end — it mints assertions from a locally generated key pair against a loopback key server, so it needs no Cloudflare account — and record the outcome in `ralph/PROGRESS.md`. Any deviation is a defect in the code or the doc; fix one, do not paper over it.

---

## Dependencies & Execution Order

- **Setup (T001–T002)**: no dependencies.
- **Foundational (T003–T008)**: depends on Setup. **Blocks every user story.**
- **US1 (T009–T017)**: depends on Foundational. Blocks everything after it.
- **US3 (T018–T019)**: depends on US1 — it proves what US1 built.
- **US4 (T020–T021)**: depends on US1's middleware existing on the same server.
- **US2 (T022–T029)**: depends on US1, US3 **and** US4 being green. This ordering is the design review's recommendation and is not negotiable within this milestone.
- **Polish (T030–T034)**: depends on everything.
- **US5** is T007, in Foundational. It is a prerequisite rather than a deliverable: no
  other story can be developed locally without it.

### Honest note on story independence

These stories are not independent, and saying otherwise would mislead. US3 and US4 are
assertions *about* US1; US2 extends the same server. What is true is that each is
independently **testable and demonstrable** once its predecessors land, and that the
milestone has a real shipping point at the end of US4.

### Parallel opportunities

`[P]` tasks touch disjoint files. Ralph ignores them.

- Foundational: T006 and T008 are parallel with T003–T005.
- US1: T011 and T015 are parallel with the httpapi work.
- Polish: T030–T033 are mutually parallel.

---

## Implementation Strategy

### MVP scope

**US1 (T001–T017)** is the demonstrable MVP: a browser with a verified, allowed identity
sees the fleet, and nothing else sees anything.

Unlike milestone 1, **this MVP is genuinely shippable** — it adds a check without adding
an execution path, so deploying it strictly improves the current posture. The hostname is
already live behind edge-only Access; landing US1 is what makes the daemon itself
validate the identity, which is the layering `docs/auth-and-sessions.md` describes.

### Incremental delivery

1. T001–T008 → layer 1 verifies offline against a local key pair
2. T009–T017 → US1, **deployable**; the daemon now checks who the browser is
3. T018–T019 → US3, the refusals proven rather than assumed
4. T020–T021 → US4, milestone 1 confirmed intact
5. T022–T029 → US2, live output — the risky half, on a foundation that already works
6. T030–T034 → binding documents corrected, quickstart validated

---

## Notes

- **Every task ends green.** Build, vet, test, lint before the commit.
- **One task per iteration.** The fresh context is the feature, not the overhead.
- **Ambiguity stops the iteration**: write it into `ralph/PROGRESS.md` under
  `NEEDS CLARIFICATION`, mark the task `- [!]`, and stop. Principle II is non-negotiable.
- **`go.sum` must not appear.** Zero third-party dependencies is a checked property; if
  a task seems to need a library, that is the signal to stop and write it down.
