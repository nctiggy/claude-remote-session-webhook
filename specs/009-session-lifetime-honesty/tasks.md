# Tasks: Session Lifetime Honesty

**Branch**: `009-session-lifetime-honesty` | **Spec**: [spec.md](spec.md) | **Plan**: [plan.md](plan.md)

Ordered so the tree is green at every numbered step. `[P]` marks tasks with no
dependency on the one before them.

**Definition of done for every task**: `go build ./... && go vet ./... && go test ./...`
passes, plus the tagged suite for anything it touches.

---

## Phase 0 — Prerequisite

- [x] **T001** Amend constitution Principle VI: remove the idle requirement, state
      what a switched-off absolute deadline costs. Bump 1.0.0 → 2.0.0.
      *(done before planning — the spec could not legitimately exist without it)*

## Phase 1 — Remove the idle bound (US1, FR-001…FR-006)

- [x] **T002** `internal/session/session.go`: delete `IdleTimeout`, `Session.Idle`,
      `Session.TmuxActivity`, `IdleDeadline`, `IdleSince`, `IdleDisabled`,
      `DisplayIdle`. Keep `LastActivity`, `AbsoluteDeadline`, `LifetimeDisabled`,
      `neverSpan`.
- [x] **T003** `internal/session/reaper.go`: delete `ExpiryIdle`, `reasonPastIdle`,
      and the idle arm of `expiredAt`. Keep `Expiry`.
- [x] **T004** `internal/session/manager.go`: `SetLifetimes` loses its two idle
      arguments; `resolveLifetimes` returns one duration; delete `syncActivity` and
      its call in `Sweep`; `CreateRequest.Idle` goes.
- [x] **T005** `internal/tmuxctl`: delete `SessionInfo.Activity` and
      `#{session_activity}` from the `List` format and the fake.
- [x] **T006** `internal/config`: delete `EnvIdleTimeout`, `EnvIdleTimeoutMax` and
      their `Config` fields and loaders.
- [x] **T007** `internal/config/file.go`: `SchemaVersion` 1 → 2; add `retiredKeys`
      and refuse a file carrying one, pointing at `crswd config migrate`.
- [x] **T008** `cmd/crswd/config_cmd.go`: `migrate` drops retired keys and writes
      `version = 2`.
- [x] **T009** `internal/httpapi`: delete `fieldIdleTimeout`, the idle half of
      `parseLifetimeOverrides`, the settings rows, and idle from views and JSON.
- [x] **T010** `web/templates/partials/create-form.html`: remove the "Never die
      when idle" switch and rewrite the never-expires hint, which currently
      describes the restart defect T012 fixes.
- [x] **T011 [P]** Docs sweep: `README.md`, `.env.example`, `config.example`,
      `deploy/crswd.example.service`, `docs/auth-and-sessions.md`, `docs/security.md`.

## Phase 2 — Make the lifetime survive a restart (US2, FR-007…FR-013)

- [x] **T012** `internal/tmuxctl/controller.go`: add `OptionLifetime` and
      `SessionInfo.Lifetime`; add the field to the `List` format and both parsers.
- [x] **T013** `internal/session/manager.go`: `start` writes `@crswd-lifetime`,
      always, using the `""` / `never` / duration vocabulary.
- [x] **T014** `internal/session/manager.go`: `Adopt` restores it, re-checks it
      against the current ceiling, and records a substitution when it cannot be
      granted. Ordered before the past-its-deadline check.
- [x] **T015** Tests: create writes the option; adopt restores `never`, a duration,
      empty, and garbage; the ceiling substitution; the audit reason.
- [x] **T016 [P]** `-tags tmux`: the option round-trips through a real tmux server.

## Phase 3 — Show the command line (US3, FR-014…FR-018)

- [x] **T017** `internal/config`: `InsertStartFlags(template, flags...)` — insert
      after the first token. Unit tests including a template with no arguments.
- [x] **T018** `internal/httpapi/dashboard.go`: the create view carries the resolved
      command line per mode.
- [x] **T019** `web/templates/partials/create-form.html`: render the preview as a
      `<pre>`, escaped, not editable.
- [x] **T020** `web/static/crswd.js`: update the preview on mode, resume and name
      changes by selecting between server-supplied lines.
- [x] **T021 [P]** `web/static/crswd.css`: one `.command-preview` block, tokens only.
- [x] **T022** Tests: the rendered line equals what `start` types; a name with `<`
      escapes; no preview block when a mode has no command.

## Phase 4 — Continue a conversation (US4, FR-019…FR-025)

- [x] **T023** `internal/session/conversation.go`: `Conversation`, `Conversations`,
      the workdir encoding, newest-first ordering, never an error.
- [x] **T024** `internal/session`: `ValidateResume` — `latest` or a strict UUID.
      Negative tests are the point: metacharacters, uppercase, prefixes, paths.
- [x] **T025** `internal/session/manager.go`: `CreateRequest.Resume`, validated
      again before rendering, flags inserted via T017.
- [x] **T026** `internal/httpapi`: read and validate the `resume` field; refuse
      uniformly; pass the working directory through `ResolveWorkDir` before listing.
- [x] **T027** `web/templates/partials/create-form.html`: the resume control and its
      list, showing id and recency only.
- [x] **T028** Tests: the six negative cases in the contract, plus the two positive
      command shapes, plus "no `.jsonl` is ever opened".

## Phase 5 — Say what a session is (US5, FR-026, FR-027)

- [x] **T029** `internal/httpapi/view.go`: age, remote-control provenance, and
      never-expires on the session view.
- [x] **T030** `web/templates/partials/session-card.html`: render the three facts.
- [x] **T031** Tests: all three correct before and after an adoption.

## Phase 6 — Close out

- [x] **T032** Full suite: `go test ./...`, `-tags tmux`, `-tags dev`,
      `golangci-lint run`, and `go vet -tags quickstart ./...` — all clean.

      The `quickstart` suite as a whole was **not** run: `127.0.0.1:8765` is held
      by the deployed daemon and two of its startup cases bind that exact port.
      The four cases that take their own port were run in full and pass
      (`TestMigrateKeepsBackup`, `TestConfigCheckDoesNotStart`,
      `TestFallsBackToBackupLoudly`,
      `TestEveryDocumentedTrailCommandSurvivesTheStream`). Stopping the running
      deployment to free the port was available and deliberately not taken —
      that is the operator's own fleet, and CI runs this suite on a host with
      nothing on the port.
- [x] **T033** `docs/fixes-log.md`: one line for the adoption defect.
- [ ] **T034** PR against `main`, stating the constitutional amendment and what it
      breaks.
