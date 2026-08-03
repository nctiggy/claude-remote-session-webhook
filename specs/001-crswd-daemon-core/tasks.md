---

description: "Task list for crswd daemon core (milestone 1)"
---

# Tasks: crswd Daemon Core (Milestone 1)

**Input**: Design documents from `/specs/001-crswd-daemon-core/`

**Prerequisites**: [plan.md](./plan.md), [spec.md](./spec.md), [research.md](./research.md), [data-model.md](./data-model.md), [contracts/](./contracts/)

## Format: `[ID] [P?] [Story] Description`

- **[P]**: Touches only files no other open task touches — safe to parallelise with a human team
- **[Story]**: Which user story the task serves (US1–US6)
- Every task names exact file paths

---

## How these tasks are shaped, and why

These are written for `ralph/loop.sh`, which runs **one task per iteration with a fresh
context** and, per `ralph/PROMPT.md`, must finish each iteration with build, test, and
lint green before committing. Three consequences:

1. **Tests ship inside the task that implements the behaviour, never as a separate
   red-first task.** The usual Spec Kit shape — write failing tests, then implement —
   cannot work here: step 6 of `ralph/PROMPT.md` tells the iteration to revert if tests
   do not pass, so a test-first task would delete itself. Every task below therefore
   states both the code and the test that must fail without it, and ends green.

2. **Order is strictly sequential and meaningful.** The loop always takes the topmost
   unchecked item. `[P]` markers are recorded for a human team working in parallel;
   Ralph ignores them and goes top to bottom.

3. **Every task is independently verifiable.** Each names a concrete command that
   proves it, so an iteration can demonstrate completion rather than assert it.

**Definition of done for every task** (from `AGENTS.md`, non-negotiable):

```bash
go build ./... && go vet ./... && go test ./... && golangci-lint run
```

---

## Phase 1: Setup

**Purpose**: Get a compiling, lintable Go module in place. Note that creating `go.mod`
switches on the entire build/test/lint job in `.github/workflows/ci.yml`, which gates on
the presence of a stack manifest — from T001 onward, CI is real.

- [x] T001 Initialize the Go module in `go.mod` declaring `go 1.23.0` as the **minimum language version** so the module builds for anyone on 1.23+. The CI *toolchain* is 1.24 and matches the dev host — do not "fix" this to 1.24 with module path `github.com/nctiggy/claude-remote-session-webhook`, and add `cmd/crswd/main.go` that parses flags and exits cleanly with no other logic. Verify: `go build ./... && go vet ./...` pass and `go.sum` does not exist.
- [x] T002 Add `.golangci.yml` using the **v1 config schema** (CI pins `golangci-lint` v1.62; a v2-schema file fails) enabling at minimum `errcheck`, `gosec`, `govet`, `staticcheck`, `bodyclose`. Install the missing local tooling documented in [quickstart.md](./quickstart.md) (`golangci-lint@v1.62.2`, `goimports`). Verify: `golangci-lint run` executes and passes — confirm it actually ran, since `.claude/hooks/format-and-lint.sh` no-ops when the binary is absent.

---

## Phase 2: Foundational (Blocking Prerequisites)

**Purpose**: The tmux layer, configuration, and audit sink that every user story needs.

**⚠️ CRITICAL**: No user story work can begin until this phase is complete.

- [x] T003 [P] Define the `Controller` interface (`New`, `SetOption`, `SendKeys`, `Paste`, `CapturePane`, `Kill`, `Has`, `List`) and the `SessionInfo` struct in `internal/tmuxctl/controller.go`, plus **two** exported target helpers in `internal/tmuxctl/target.go`: `SessionTarget(name) = "=" + name` and `PaneTarget(name) = "=" + name + ":"`. Per [contracts/tmuxctl.md](./contracts/tmuxctl.md) these are not interchangeable — `send-keys -t =name` fails with `can't find pane`. Test in `internal/tmuxctl/target_test.go` asserting both output formats exactly.
- [x] T004 [P] Implement the in-memory fake in `internal/tmuxctl/fake.go` satisfying `Controller`, recording the exact argv of every call, and able to simulate: kill-succeeds-but-Has-still-reports-present, a session that vanished on its own, `List` returning a managed session plus an unmanaged lookalike plus an unrelated session, a session created 25 hours ago, and a tmux exec failure distinct from absence. Test in `internal/tmuxctl/fake_test.go`.
- [x] T005 Implement the real controller over `exec.Command` with **argv slices only, no shell string anywhere**, in `internal/tmuxctl/exec.go`, using the exact commands in [contracts/tmuxctl.md](./contracts/tmuxctl.md): `new-session -d -s <name> -c <dir>`; `set-option -t =<name>:`; `send-keys -t =<name>: -l --` for daemon-authored constants only; `Paste` via `load-buffer -b <buf> -` with the payload on **stdin** plus `paste-buffer -d -b <buf> -t =<name>:`; `capture-pane -p -t =<name>:` **without `-e`**; `kill-session -t =<name>`; `has-session -t =<name>`; `list-sessions -F '#{session_name}|#{session_created}|#{@crswd-managed}'`. `Has` must distinguish exit-1 "gone" from an exec failure, and `List` must treat `no server running` as an empty slice, not an error. Add an integration test behind `//go:build tmux` in `internal/tmuxctl/exec_tmux_test.go` running against an isolated socket (`tmux -L crswd-test`) with a `t.Cleanup` that kills the test server.
- [x] T006 [P] Add the defensive control-sequence stripper in `internal/tmuxctl/ansi.go` (belt-and-braces: capture already omits ANSI because `-e` is never passed — see [research.md D5](./research.md)), with golden-file tests in `internal/tmuxctl/ansi_test.go` and fixtures in `internal/tmuxctl/testdata/`.
- [x] T007 [P] Implement `Load()` in `internal/config/config.go` reading `CRSW_SHARED_SECRET` (**required**; fatal if unset or under 32 bytes), `CRSW_ALLOWED_ROOTS` (optional, `:`-separated, default `$HOME/code`, fatal if a listed root does not exist), `CRSW_LISTEN` (default `127.0.0.1:8765`, **fatal if the host is not loopback**), `CRSW_MAX_SESSIONS` (default 5), `CRSW_CREATE_RATE_PER_MIN` (default 6), `CRSW_MAX_BODY_BYTES` (default 65536), per the Config table in [data-model.md](./data-model.md). Emit a prominent warning naming `CRSW_ALLOWED_ROOTS` and the path in force on **every** start that defaults. The secret must never appear in any error or log, not even its length. Table test every missing/short/invalid case in `internal/config/config_test.go`, including that the default-root warning is emitted.
- [x] T008 [P] Implement the audit sink in `internal/audit/audit.go` as a **fixed struct** (`time`, `action`, `caller`, `session_id`, `decision`, `reason`, `remote`) emitted as JSON on stdout via `log/slog` — no `map[string]any`, no `slog.Any` passthrough, so the record shape cannot carry arbitrary data. Test in `internal/audit/audit_test.go` asserting field presence and that the type offers no way to attach free-form content.

**Checkpoint**: tmux is drivable through a fake, config fails closed, audit records exist.

---

## Phase 3: User Story 1 — Start a session from a signed request (Priority: P1) 🎯 MVP

**Goal**: A signed create request starts a real session and returns its ID and one-time token; every unsigned, missigned, stale, replayed, or out-of-bounds request gets nothing.

**Independent Test**: `POST /sessions` with a valid signature yields 201 and a live tmux session; unsigned, tampered, ±301s-skewed, and replayed variants each yield an identical 401 with zero sessions created; traversal, symlink-escape, bad-name, and unknown-field bodies each yield 400.

- [x] T009 [US1] Implement HMAC-SHA256 verification over `timestamp + "." + rawBody` using `hmac.Equal` in `internal/auth/hmac.go`, re-buffering the body so the handler can still read it. Table test in `internal/auth/hmac_test.go`: valid, bad signature, body tampered after signing, missing signature header, missing timestamp header.
- [x] T010 [US1] Enforce the 300-second timestamp window **in both directions** in `internal/auth/hmac.go`, against a one-method `Clock` interface defined in `internal/auth/clock.go` (defined at point of use — no shared util package). Test in `internal/auth/hmac_test.go` proving a far-future timestamp is rejected, not just a stale one.
- [x] T011 [US1] Implement the replay cache in `internal/auth/replay.go` keyed on the full signature with TTL `2 × 300s`, sweeping expired entries on write. `Observe(sig) bool` must check and record **inside one critical section**. Test in `internal/auth/replay_test.go`: a second use of a valid request is refused, and two concurrent replays produce exactly one winner.
- [x] T012 [US1] Add the `Caller` type and server-side identity derivation in `internal/auth/caller.go`, plus a single opaque error value returned by every verification failure so no caller can tell which check failed. Test in `internal/auth/caller_test.go` that identity never derives from a request-supplied field and that all failure modes return an indistinguishable error.
- [x] T013 [P] [US1] Implement session ID generation in `internal/session/id.go`: 16 bytes from `crypto/rand` rendered as 32 lowercase hex characters (**not** `crypto/rand.Text` — it requires a `go 1.24` directive, and go.mod deliberately declares 1.23.0). Test in `internal/session/id_test.go` asserting the `^[a-f0-9]{32}$` shape, non-sequential values, and no collisions across a large sample.
- [x] T014 [P] [US1] Implement session-name validation in `internal/session/name.go` enforcing `^[a-zA-Z0-9-]{1,64}$` and rejecting `:` and `.` explicitly. Hostile table test in `internal/session/name_test.go`: `:`, `.`, path separators, leading `-`, empty, 65 characters, control characters, non-ASCII.
- [x] T015 [P] [US1] Implement the working-directory allowlist in `internal/session/workdir.go`: `filepath.Clean`, then `filepath.EvalSymlinks` (failing closed when the path does not exist), then containment under an approved root checked **at a path-separator boundary**. Test in `internal/session/workdir_test.go` covering `..` traversal, an absolute path outside the roots, a symlink pointing outside, a non-directory, and the string-prefix trap where `/home/u/codeEVIL` must be rejected against root `/home/u/code`.
- [x] T016 [US1] Add the `Session` model, `State` values (`starting`, `running`, `dead`), and the in-memory store with an `Owner` field in `internal/session/session.go`, following the field and derived-value tables in [data-model.md](./data-model.md) — the tmux name derives from the ID alone, and token expiry is **derived** from `CreatedAt + 24h`, never stored separately. Test in `internal/session/session_test.go`.
- [x] T017 [US1] Implement per-session bearer tokens in `internal/session/token.go`: 32 bytes from `crypto/rand` as 64 hex characters, stored only as `sha256.Sum256`, compared with `hmac.Equal`. Test in `internal/session/token_test.go` asserting the plaintext is never retained on the record and that comparison is constant-time.
- [x] T018 [US1] Implement `Manager.Create` in `internal/session/manager.go`: create the tmux session named `crswd-<id>` in the validated working directory, set `@crswd-managed` and `@crswd-owner`, then send `claude --dangerously-skip-permissions` as keys so a Claude crash leaves an inspectable shell. Test against the fake in `internal/session/manager_test.go`, asserting the exact call order and that the tmux target derives only from the ID.
- [x] T019 [US1] Implement the HTTP server in `internal/httpapi/server.go`: `net/http.ServeMux` with Go 1.22 method+wildcard patterns, read/write/idle timeouts, and a **startup assertion that the resolved listen address is loopback**. Test in `internal/httpapi/server_test.go` asserting the bound address is loopback and that a non-loopback `CRSW_LISTEN` is fatal.
- [x] T020 [US1] Implement the authentication middleware in `internal/httpapi/middleware.go` wrapping **every** registered route with signature verification, emitting exactly one audit record per request, and returning the uniform `401 {"error":"unauthorized"}` on any failure. Test in `internal/httpapi/middleware_test.go` that iterates the router's **registered** routes (not a hand-written list, so a future route cannot be forgotten) and asserts each returns 401 unauthenticated.
- [x] T021 [P] [US1] Implement the body decoder in `internal/httpapi/decode.go` using `http.MaxBytesReader` at `CRSW_MAX_BODY_BYTES` and `json.Decoder.DisallowUnknownFields`. Test in `internal/httpapi/decode_test.go`: unknown field rejected, oversize body rejected, truncated body rejected, wrong-shape body rejected — all 400.
- [x] T022 [US1] Implement `POST /sessions` in `internal/httpapi/sessions.go` returning 201 with `id`, `name`, `work_dir`, `state`, `created_at`, `expires_at`, and `token` exactly once, per [contracts/http-api.md](./contracts/http-api.md). Test in `internal/httpapi/sessions_test.go`: happy path; 400 for bad name, out-of-root path, symlink escape, unknown field; `expires_at` is exactly 24h after `created_at`.
- [x] T023 [US1] Implement the session-scoped request resolver in `internal/httpapi/middleware.go` — bearer token match **and** owner match — returning the uniform `404 {"error":"not found"}` for unknown, not-owned, and wrong-token alike, and rejecting a token past its session's expiry. Test in `internal/httpapi/middleware_test.go` that the three failure responses are **byte-identical including headers**.

**Checkpoint**: US1 is independently demonstrable. Note it is not yet *shippable* — the concurrency cap and reaper required by Constitution Principle VI arrive in US5.

---

## Phase 4: User Story 2 — Drive a session and read it back (Priority: P2)

**Goal**: Prompts reach the session byte-for-byte; output comes back as plain text.

**Independent Test**: Send `;`, `foo;`, and `a; echo PWNED; $(id)` as prompts; every byte arrives verbatim, nothing executes, and the captured output contains no escape bytes.

- [x] T024 [US2] Implement `POST /sessions/{id}/prompt` in `internal/httpapi/sessions.go`, delivering text through `Controller.Paste` (`load-buffer` from stdin + `paste-buffer -d`) followed by an `Enter` key, returning 202. **Never `send-keys -l` for caller text** — [research.md D4](./research.md) shows tmux strips a trailing unescaped `;` before `-l` applies. Test in `internal/httpapi/sessions_test.go` with payloads `";"`, `"foo;"`, `"foo;;"`, `"a; echo PWNED; $(id) \`whoami\`"`, and an embedded newline, asserting the fake received each byte-for-byte on stdin and that the prompt text appears in no audit record.
- [x] T025 [US2] Implement `GET /sessions/{id}/output` in `internal/httpapi/sessions.go` returning captured pane text with the stripper applied, per [contracts/http-api.md](./contracts/http-api.md). Test in `internal/httpapi/sessions_test.go` asserting the response contains no ESC bytes and that pane content appears in no audit record or log line.

**Checkpoint**: US1 and US2 both work independently.

---

## Phase 5: User Story 3 — See the fleet and tear it down for certain (Priority: P3)

**Goal**: Owner-scoped listing, and destruction that is verified rather than assumed.

**Independent Test**: Two owners' sessions never cross; destroy leaves nothing in tmux; a kill that does not take effect reports 409 instead of success.

- [x] T026 [P] [US3] Implement `GET /sessions` in `internal/httpapi/sessions.go` returning only the caller's sessions and never a token or hash. Test in `internal/httpapi/sessions_test.go` that another owner's sessions are absent.
- [x] T027 [P] [US3] Implement `GET /sessions/{id}` in `internal/httpapi/sessions.go` returning one session's detail. Test in `internal/httpapi/sessions_test.go`.
- [x] T028 [US3] Implement `Manager.Destroy` in `internal/session/manager.go`: `Kill`, then `Has` to **verify absence**, reporting success only on confirmation, and clearing the record, token hash, and any buffered output. A surviving session must return an error and leave the record intact. Test against the fake in `internal/session/manager_test.go` covering both the verified-gone and still-present paths.
- [x] T029 [US3] Implement `DELETE /sessions/{id}` in `internal/httpapi/sessions.go` returning 200 on verified teardown and **409** with a prominent audit record when the session survives. Test in `internal/httpapi/sessions_test.go` for both outcomes.
- [x] T030 [US3] Add the cross-session isolation suite in `internal/httpapi/isolation_test.go`: create session A as owner 1, produce distinctive output, create session B as a synthetic owner 2, then assert every read endpoint scoped to B returns nothing from A, and that B's token against A's ID returns a 404 **byte-identical** to the unknown-ID response. Required by `docs/auth-and-sessions.md` and `AGENTS.md`.

**Checkpoint**: All three CRUD stories are independently functional.

---

## Phase 6: User Story 4 — Restart without leaving unowned shells (Priority: P4)

**Goal**: Surviving daemon-created sessions come back under management; lookalikes are untouched; the 24-hour ceiling survives restarts.

**Independent Test**: Kill the daemon with a session live, plant a hand-made `crswd-*` session, restart; the real one is listed and destroyable with a fresh token, the lookalike is absent from the listing and still alive in tmux.

- [x] T031 [US4] Implement `Manager.Adopt` in `internal/session/manager.go`: `List` once, adopt only sessions carrying `@crswd-managed`, set `Owner` to the configured operator identity, derive the absolute deadline from `#{session_created}` (**not** from adoption time), reset only the idle clock, issue a fresh token, and **destroy rather than adopt** anything already past 24 hours. Test against the fake in `internal/session/manager_test.go` covering: a healthy survivor, an unmanaged lookalike left alone, a half-dead session resolved to a definite state, a 23-hour-old survivor that dies an hour later, an already-expired survivor destroyed, and repeated adoptions leaving the deadline unchanged.
- [x] T032 [US4] Wire `Adopt` into daemon startup in `cmd/crswd/main.go` before the listener binds, emitting a `startup.adopt` audit record per adopted session. Test in `internal/session/manager_test.go` that reconciliation runs before serving and that a tmux failure at startup is fatal rather than silently skipped.

**Checkpoint**: A restart can no longer orphan an unsandboxed shell.

---

## Phase 7: User Story 5 — Abandoned sessions die on their own (Priority: P5)

**Goal**: The Principle VI bounds — cap, rate limit, idle timeout, absolute lifetime, clean shutdown — all hold without a client request.

**Independent Test**: The 6th create at cap 5 returns 429; advancing an injected clock past 60m idle and past 24h destroys sessions with verified teardown; SIGTERM leaves zero `crswd-` sessions in tmux.

- [x] T033 [P] [US5] Enforce the concurrent-session cap in `internal/session/manager.go`, refusing creation past `CRSW_MAX_SESSIONS` and surfacing 429 from `POST /sessions`. Test in `internal/session/manager_test.go` and `internal/httpapi/sessions_test.go` that the cap holds under concurrent creates racing at the boundary and that existing sessions are unaffected.
- [x] T034 [P] [US5] Implement a hand-rolled token-bucket rate limiter in `internal/httpapi/ratelimit.go` on the same injected clock (no `golang.org/x/time/rate` — see [research.md D7](./research.md)), applied to session creation per caller, returning 429. Test in `internal/httpapi/ratelimit_test.go` without sleeping.
- [x] T035 [P] [US5] Enforce session-token expiry at 24 hours in `internal/session/token.go`, equal to the absolute lifetime by construction so the two cannot diverge. Test in `internal/session/token_test.go` that a token is accepted for the whole session life and refused the instant the session expires.
- [x] T036 [US5] Implement the reaper goroutine in `internal/session/reaper.go` destroying sessions idle beyond 60 minutes and any session beyond its 24-hour absolute deadline, using the same verified teardown as an explicit destroy, driven by an injected clock. Test in `internal/session/reaper_test.go` **without sleeping**, covering idle expiry, absolute expiry on an actively used session, and a destroy racing the reaper on the same session.
- [x] T037 [US5] Implement graceful shutdown in `cmd/crswd/main.go` using `signal.NotifyContext` and `http.Server.Shutdown`, tearing down every live session with verification before exit. Test in `internal/httpapi/server_test.go` that shutdown reaps sessions and that a teardown failure during shutdown is logged loudly rather than swallowed.

**Checkpoint**: Every Constitution Principle VI bound is enforced structurally. The milestone is now shippable.

---

## Phase 8: User Story 6 — The audit trail answers "what happened" (Priority: P6)

**Goal**: One record per request, covering every action, leaking nothing.

**Independent Test**: Exercise every endpoint including failures, then grep the capture for prompt text, tokens, the shared secret, and escape bytes — all zero.

- [x] T038 [US6] Wire audit records through every remaining path in `internal/httpapi/middleware.go`, `internal/session/reaper.go`, and `cmd/crswd/main.go` so that `session.create`, `session.prompt`, `session.destroy`, `auth.reject`, `reaper.destroy`, and `startup.adopt` each emit exactly one record with caller, session ID, and decision. Test in `internal/audit/audit_test.go` that every route produces exactly one record, allowed or denied.
- [ ] T039 [US6] Add the leak-assertion suite in `internal/audit/leak_test.go`: drive an end-to-end exercise of every operation with distinctive prompt text and pane content, capture all emitted records and log lines, and assert **zero** occurrences of prompt text, pane output, bearer tokens, token hashes, the shared secret, or any full request body. Required by `docs/security.md` §3 and SC-013.

**Checkpoint**: All six stories independently functional and verified.

---

## Phase 9: Polish & Cross-Cutting Concerns

- [ ] T040 [P] Document every environment variable from [data-model.md](./data-model.md) in `.env.example` (**names and descriptions only, never a value**) and add a configuration section to `README.md`.
- [ ] T041 Fill in `deploy/crswd.example.service` and `deploy/cloudflared.example.yml` with the secret sourced from 1Password rather than any repo file, and complete the deployment section of `README.md` including `journalctl --user -u crswd` for reading the audit trail.
- [ ] T042 Run the full [quickstart.md](./quickstart.md) validation end to end against a real build and real tmux, and record the outcome in `ralph/PROGRESS.md`. Any deviation from the documented expectations is a defect in the code or the doc — fix one of them, do not paper over it.

---

## Dependencies & Execution Order

### Phase dependencies

- **Setup (T001–T002)**: no dependencies. T001 activates CI.
- **Foundational (T003–T008)**: depends on Setup. **Blocks every user story.**
- **US1 (T009–T023)**: depends on Foundational. Blocks US2–US6 in practice — it builds the auth layer, the session store, and the server every later story extends.
- **US2 (T024–T025)** and **US3 (T026–T030)**: depend on US1, notably the session-scoped resolver (T023).
- **US4 (T031–T032)**: depends on US1 plus `Manager.Destroy` (T028) for the destroy-not-adopt path.
- **US5 (T033–T037)**: depends on US1 and T028.
- **US6 (T038–T039)**: depends on every prior phase, since it asserts across all of them.
- **Polish (T040–T042)**: depends on everything.

### Honest note on story independence

The template's ideal is fully independent stories. That is not true here and pretending
otherwise would mislead whoever picks this up: this is one daemon with one auth layer,
and US2–US6 all extend the server US1 builds. What *is* true is that each story is
independently **testable and demonstrable** once its predecessors land, which is the
property the checkpoints verify.

### Parallel opportunities

`[P]` tasks touch disjoint files. Ralph ignores these and runs top to bottom; they matter
only for a human team.

- Foundational: T003, T004, T006, T007, T008 are mutually parallel (T005 depends on T003).
- US1: T013, T014, T015 (three separate `internal/session` files) and T021 are parallel.
- US3: T026 and T027 are parallel.
- US5: T033, T034, T035 are parallel.

---

## Implementation Strategy

### MVP scope

**US1 (T001–T023)** is the demonstrable MVP: a signed request starts a real session and
everything else is refused.

**It is deliberately not deployable.** Constitution Principle VI requires a concurrency
cap, an idle timeout, an absolute lifetime, and verified teardown, and those land in US3
and US5. Running the US1 MVP as a service would put an uncapped, unreaped, unsandboxed
shell on the public internet. Demo it locally; ship after T037.

### Incremental delivery

1. T001–T008 → foundation, CI live, tmux drivable through a fake
2. T009–T023 → US1, MVP demonstrable locally
3. T024–T025 → US2, sessions become useful
4. T026–T030 → US3, fleet manageable and isolation proven
5. T031–T032 → US4, restarts stop orphaning shells
6. T033–T037 → US5, **bounds enforced — now shippable**
7. T038–T039 → US6, audit trail complete and proven leak-free
8. T040–T042 → deployment and end-to-end validation

---

## Notes

- **Every task ends green.** `go build ./... && go vet ./... && go test ./... && golangci-lint run` before the commit, per `ralph/PROMPT.md` step 6.
- **Tests live with their code**, not in a separate phase — see the shaping note at the top.
- **One task per iteration.** The loop's value is the fresh context; batching two tasks discards it.
- **Ambiguity stops the iteration.** Per Constitution Principle II and `ralph/PROMPT.md` step 5, write the ambiguity into `ralph/PROGRESS.md` under `NEEDS CLARIFICATION`, mark the task blocked with `- [!]`, and stop. Do not guess.
- **`go.sum` must not appear.** Zero third-party dependencies is a verifiable property of this milestone ([research.md D7](./research.md)); a `go.sum` file means something was imported that needs justification under `docs/security.md` §5.
- Commit messages follow `ralph/PROMPT.md` step 7: imperative subject, body explaining *why*.
