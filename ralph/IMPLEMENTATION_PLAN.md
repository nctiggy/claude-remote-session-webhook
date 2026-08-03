# Implementation Plan

> Worked top-to-bottom by `ralph/loop.sh` — one task per iteration.
>
> **These are milestone 1 only.** Scope is deliberately capped: a working daemon
> with no UI. Milestones 2–4 (dashboard, actions, Claude login relay) get their own
> plan and their own loop. One loop for the whole product drifts.

> ✅ **Unblocked.** Iteration 1 could not run Bash at all — `.claude/settings.json`
> had no `permissions` block. An operator added one in `902c249`, so build, test,
> lint, and commit all work from inside an iteration now. Do not re-raise this.

## Status: generated from the spec

Generated from [`specs/001-crswd-daemon-core/tasks.md`](../specs/001-crswd-daemon-core/tasks.md),
which is the single source of truth. `spec.md`, `plan.md`, and `research.md` in that
directory supersede the provisional list this file used to carry.

**Before starting a task, read its matching `T0NN` entry in `tasks.md`.** The entries
below are the ordered checklist; the task file carries the exact file paths, the test
each task must include, and the reason behind the non-obvious ones. Several tasks look
wrong until you read the reason — `Paste` not `SendKeys`, `=name` versus `=name:`, hex
not `crypto/rand.Text`.

## Resolved decisions

Answered by the operator. **Do not re-litigate these in an iteration** — if one
looks wrong, write it in `PROGRESS.md` under `NEEDS CLARIFICATION` and stop.

| Question | Decision | Consequence |
|---|---|---|
| What does session ownership mean with one shared secret? | Single operator, but the `Owner` field and the ownership check exist from day one | Milestone 2's Access identity drops in without touching a handler. The cross-owner test uses a synthetic second owner |
| What does the tmux window run? | The shell, then the daemon sends `claude --dangerously-skip-permissions` as keys | Window survives a Claude crash, so scrollback is inspectable — and milestone 4's device-code relay has a prompt to type into |
| What happens to tmux sessions that outlive the daemon? | **Adopt** them on startup | An orphaned window is a live unsandboxed shell with no owner (Principle VI). Ignoring them is not an option |
| Where does the audit log go? | Structured JSON on stdout, captured by journald | No file mode, rotation, or disk-fill to get wrong. `journalctl --user -u crswd` |

Three further ambiguities were raised and answered during `/speckit-specify`; they are
recorded under "Resolved during specification" in `specs/001-crswd-daemon-core/spec.md`
and are equally settled: the approved-roots default and its loud warning, the session
token lifetime raised to 24h to match the absolute lifetime, and the adopted-session
owner plus its clock origin.

## Conventions

- `- [ ]` open · `- [x]` done · `- [!]` blocked (reason in `PROGRESS.md`)
- Priority order is meaningful — the loop always takes the topmost open item.
- Each task must be independently completable **and verifiable** in one iteration.
- **Every task ends green**: `go build ./... && go vet ./... && go test ./... && golangci-lint run`.
  Tests ship inside the task that implements the behaviour — never as a separate
  failing-test task, which step 6 of `PROMPT.md` would make the iteration revert.
- `go.sum` must never appear. Zero third-party dependencies is a checked property of
  this milestone; an import needs justification under `docs/security.md` §5 first.

---

## Tasks

### Setup

- [x] T001 Initialize the Go module in `go.mod` declaring `go 1.23.0` as the **minimum language version** (the CI toolchain is 1.24 and matches the dev host — do not "fix" this to 1.24) and add `cmd/crswd/main.go` that parses flags and exits cleanly. Creating `go.mod` switches CI's build/test/lint job on
- [x] T002 Add `.golangci.yml` using the **v1 config schema** (CI pins golangci-lint v1.62) enabling `errcheck`, `gosec`, `govet`, `staticcheck`, `bodyclose`; install `golangci-lint` and `goimports` locally — both are currently missing, so the format-and-lint hook silently no-ops

### Foundation — tmux, config, audit

- [x] T003 `internal/tmuxctl`: `Controller` interface and `SessionInfo` in `controller.go`, plus **two** target helpers in `target.go` — `SessionTarget` → `=name`, `PaneTarget` → `=name:`. They are not interchangeable
- [x] T004 `internal/tmuxctl/fake.go`: in-memory `Controller` recording exact argv, able to simulate kill-succeeds-but-still-present, self-vanished sessions, a lookalike in `List`, a 25-hour-old session, and exec failure distinct from absence
- [x] T005 `internal/tmuxctl/exec.go`: real controller over `exec.Command`, argv slices only. `Paste` uses `load-buffer -b <buf> -` with the payload on **stdin** plus `paste-buffer -d`; `capture-pane -p` **without `-e`**; `Has` distinguishes exit-1 from exec failure; `List` treats "no server running" as empty. Integration test behind `//go:build tmux` on an isolated socket
- [x] T006 `internal/tmuxctl/ansi.go`: defensive control-sequence stripper with golden-file tests in `testdata/`
- [x] T007 `internal/config/config.go`: load from environment. `CRSW_SHARED_SECRET` required — **startup fails** if unset or under 32 bytes. `CRSW_ALLOWED_ROOTS` optional, defaulting to `$HOME/code` with a **loud warning on every defaulted start**. Non-loopback `CRSW_LISTEN` is fatal. Table test every case
- [x] T008 `internal/audit/audit.go`: structured JSON on stdout via `log/slog`, as a **fixed struct** — no `map[string]any`, no `slog.Any` passthrough, so the record shape cannot carry arbitrary data

### US1 — Start a session from a signed request (P1, MVP)

- [x] T009 `internal/auth/hmac.go`: HMAC-SHA256 over `timestamp + "." + rawBody` with `hmac.Equal`, body re-buffered for the handler. Tests: valid, bad signature, tampered body, missing headers
- [x] T010 `internal/auth/hmac.go`: 300s timestamp window enforced in **both** directions against an injected `Clock`; test proves a far-future timestamp is rejected
- [x] T011 `internal/auth/replay.go`: replay cache keyed on signature, TTL `2 × skew`, with `Observe` checking and recording in **one** critical section; test proves a second use is refused and two concurrent replays yield one winner
- [x] T012 `internal/auth/caller.go`: `Caller` identity derived server-side only, plus one opaque error so no caller learns which check failed
- [x] T013 `internal/session/id.go`: IDs from `crypto/rand`, 16 bytes → 32 lowercase hex (**not** `crypto/rand.Text` — it needs a `go 1.24` directive; go.mod deliberately declares 1.23.0). Test asserts shape, non-sequential, non-colliding
- [x] T014 `internal/session/name.go`: name validation `^[a-zA-Z0-9-]{1,64}$`, rejecting `:` and `.` explicitly — they address a different tmux target. Hostile table test
- [x] T015 `internal/session/workdir.go`: `filepath.Clean` + `EvalSymlinks` (failing closed), then containment under an approved root **at a path-separator boundary**. Tests cover `..`, absolute escapes, a symlink pointing outside, and the prefix trap where `/home/u/codeEVIL` must fail against `/home/u/code`
- [x] T016 `internal/session/session.go`: `Session` model with `Owner`, the `starting`/`running`/`dead` states, and the in-memory store. Token expiry is **derived** from `CreatedAt + 24h`, never stored separately
- [x] T017 `internal/session/token.go`: per-session bearer tokens from `crypto/rand`, returned once, stored as SHA-256, compared with `hmac.Equal`. Test asserts the plaintext is never persisted
- [x] T018 `internal/session/manager.go`: `Manager.Create` — tmux session `crswd-<id>` in the validated directory, set `@crswd-managed` and `@crswd-owner`, then send `claude --dangerously-skip-permissions` as keys. Test asserts call order and that the target derives only from the ID
- [x] T019 `internal/httpapi/server.go`: `ServeMux` with Go 1.22 method+wildcard patterns, server timeouts, and a startup assertion that the listen address is **loopback**
- [x] T020 `internal/httpapi/middleware.go`: authentication on **every** registered route, one audit record per request, uniform `401`. The test must iterate the router's registered routes, not a hand-written list, so a future route cannot be forgotten
- [x] T021 `internal/httpapi/decode.go`: `MaxBytesReader` + `DisallowUnknownFields`. Tests cover unknown fields, oversize, truncated, and wrong-shape bodies
- [x] T022 `internal/httpapi/sessions.go`: `POST /sessions` → 201 with the token returned exactly once and `expires_at` exactly 24h after `created_at`
- [x] T023 `internal/httpapi/middleware.go`: session-scoped resolver — bearer token **and** owner match, expired tokens refused, returning a `404` **byte-identical** for unknown, not-owned, and wrong-token

### US2 — Drive a session and read it back (P2)

- [x] T024 `internal/httpapi/sessions.go`: `POST /sessions/{id}/prompt` delivering text via `Controller.Paste` then `Enter`. **Never `send-keys -l` for caller text** — tmux's parser strips a trailing unescaped `;` before `-l` applies, and `--` does not prevent it; `;` lands empty and `foo;` lands as `foo`. Test asserts byte-for-byte delivery of `;`, `foo;`, `foo;;`, `a; echo PWNED; $(id)`, and an embedded newline
- [x] T025 `internal/httpapi/sessions.go`: `GET /sessions/{id}/output` — captured pane text, stripped. Test asserts no ESC bytes and no pane content in any log or audit record

### US3 — See the fleet and tear it down for certain (P3)

- [x] T026 `internal/httpapi/sessions.go`: `GET /sessions`, owner-scoped, never returning a token or hash
- [x] T027 `internal/httpapi/sessions.go`: `GET /sessions/{id}` detail
- [x] T028 `internal/session/manager.go`: `Manager.Destroy` — kill, then **verify gone** via `Has`; report success only on confirmation; clear record, token hash, and buffered output. A survivor returns an error and keeps the record
- [x] T029 `internal/httpapi/sessions.go`: `DELETE /sessions/{id}` → 200 on verified teardown, **409** plus a prominent audit record when the session survives
- [ ] T030 `internal/httpapi/isolation_test.go`: cross-session isolation suite — session A's output unreachable through any request scoped to B, and B's token on A's ID returning a 404 byte-identical to the unknown-ID response

### US4 — Restart without leaving unowned shells (P4)

- [ ] T031 `internal/session/manager.go`: `Manager.Adopt` — one `List`, adopt only sessions carrying `@crswd-managed`, owner set to the configured operator, absolute deadline derived from `#{session_created}` (**not** adoption time), only the idle clock reset, fresh tokens issued, and anything already past 24h **destroyed rather than adopted**. Tests cover a healthy survivor, an untouched lookalike, a half-dead session, a 23-hour-old survivor dying an hour later, an expired survivor, and repeated adoptions leaving the deadline unchanged
- [ ] T032 `cmd/crswd/main.go`: run `Adopt` at startup before the listener binds, one `startup.adopt` audit record per adopted session; a tmux failure here is fatal, not silently skipped

### US5 — Abandoned sessions die on their own (P5)

- [ ] T033 `internal/session/manager.go`: concurrent-session cap, refusing past `CRSW_MAX_SESSIONS` with 429 rather than degrading the host. Test covers creates racing at the boundary
- [ ] T034 `internal/httpapi/ratelimit.go`: hand-rolled token bucket on the injected clock (no `golang.org/x/time/rate`), applied per caller to create, returning 429. Test does not sleep
- [ ] T035 `internal/session/token.go`: enforce token expiry at 24h, equal to the absolute lifetime by construction so the two cannot diverge
- [ ] T036 `internal/session/reaper.go`: reaper goroutine enforcing idle (60m) and absolute (24h) lifetimes with the same verified teardown, driven by an injected clock so tests do not sleep. Covers a destroy racing the reaper
- [ ] T037 `cmd/crswd/main.go`: graceful shutdown on `SIGTERM` via `signal.NotifyContext` and `Server.Shutdown`, reaping every live session with verification; a failed teardown is logged loudly, never swallowed

### US6 — The audit trail answers "what happened" (P6)

- [ ] T038 Wire audit records through every remaining path in `internal/httpapi/middleware.go`, `internal/session/reaper.go`, and `cmd/crswd/main.go` so `session.create`, `session.prompt`, `session.destroy`, `auth.reject`, `reaper.destroy`, and `startup.adopt` each emit exactly one record
- [ ] T039 `internal/audit/leak_test.go`: drive every operation with distinctive prompt text and pane content, then assert **zero** occurrences of prompt text, pane output, tokens, token hashes, the shared secret, or any full body across all records and log lines

### Ship it

- [ ] T040 Document every environment variable in `.env.example` (**names and descriptions only, never a value**) and add a configuration section to `README.md`
- [ ] T041 `deploy/`: fill in the systemd user unit and `cloudflared` config, with the secret sourced from 1Password rather than a repo file. Complete the README deployment section, including `journalctl --user -u crswd` for reading the audit trail
- [ ] T042 Run the full `specs/001-crswd-daemon-core/quickstart.md` validation end to end against a real build and real tmux; record the outcome in `PROGRESS.md`. Any deviation is a defect in the code or the doc — fix one, do not paper over it

---

## Not shippable before T037

T001–T023 make a demonstrable MVP, but Constitution Principle VI requires a concurrency
cap, an idle timeout, an absolute lifetime, and verified teardown — those land in T028
and T033–T037. Running the daemon as a service before then puts an uncapped, unreaped,
unsandboxed shell behind a public hostname. Demo it locally; deploy after T037.

---

## Out of scope

Deliberately NOT in milestone 1, so no iteration wanders into them:

- The web dashboard, templates, htmx, CSS, and SSE streaming — milestone 2
- Cloudflare Access JWT validation — milestone 2
- Rename and compact — milestone 3
- The Claude device-code login relay — milestone 4
- The companion Claude skill in `skill/` — after the API is stable
- Persisting session records to disk. The store stays in-memory — restart recovery
  comes from adopting live tmux sessions, not from a database
- Multi-user support. There is exactly one operator
