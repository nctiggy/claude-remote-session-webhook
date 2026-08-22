---
description: "Task list for 012-session-revival"
---

# Tasks: Session Revival

**Input**: Design documents from `/specs/012-session-revival/`

**Prerequisites**: plan.md, spec.md, research.md, data-model.md, contracts/

**Tests**: REQUIRED. `AGENTS.md` — *"Every PR needs a test that fails without the
change; auth and session code needs the negative cases too."* This feature is
session lifecycle code, so the negative cases are not optional.

**Organization**: grouped by user story so each is independently deliverable.

## Format: `[ID] [P?] [Story] Description`

- **[P]**: parallelisable — different file, no dependency on an incomplete task
- **[Story]**: US1–US4 from spec.md

---

## Phase 1: Setup

- [X] T001 Confirm the working tree is green before touching it: run `go build ./...`, `go vet ./...`, `go test ./...` and `golangci-lint run` from the repository root and record the result.

---

## Phase 2: Foundational (blocks every user story)

**These are the primitives every story below stands on. Nothing in Phase 3+ can start until they are done.**

- [X] T002 [P] Add `StateFailed State = "failed"` beside the existing states in `internal/session/session.go`, documenting that it is terminal and distinct from `dead` (a session nobody could save vs. a session somebody ended).
- [X] T003 [P] Add `ConversationID string`, `ReviveAttempts int` and `NextReviveAt time.Time` to `Session` in `internal/session/session.go`, with the field comments explaining why each is durable.
- [X] T004 [P] Add `OptionConversation = "@crswd-conversation"` to `internal/tmuxctl/controller.go` beside the five existing options, documenting that it is a cache and the journal is the authority.
- [X] T005 Extend the `list-sessions` format string in `internal/tmuxctl/exec.go` with `#{pane_current_command}` and the new option, and widen the row parser to match.
- [X] T006 Mirror the new format string and row shape in `internal/tmuxctl/fake.go` so the fake and the real controller cannot drift.
- [X] T007 Add `PaneCommand` and `ConversationID` to the `SessionInfo` type returned by `List` in `internal/tmuxctl/controller.go`.
- [X] T008 [P] Write `internal/tmuxctl/exec_test.go` cases for parsing a row with and without the new fields, including a session whose conversation option is unset (every pre-feature session).
- [X] T009 [P] Add `JournalPath(getenv, listen)` to `internal/config/file.go`, resolving `sessions-<listen-address>.jsonl` from the same base `DefaultPath` uses, and returning `""` when there is no absolute base.
- [X] T010 [P] Write `internal/config/file_test.go` cases for `JournalPath`: `XDG_CONFIG_HOME` set, only `HOME` set, neither set, and a relative directory ignored.

**Checkpoint**: `go build ./...` and `go test ./...` pass; nothing behaves differently yet.

---

## Phase 3: User Story 1 — A session that died comes back (Priority: P1) 🎯 MVP

**Goal**: a session whose Claude process stopped, or whose whole shell was destroyed, is running again within one sweep, continuing the same conversation, under the same identity and deadline.

**Independent test**: kill Claude in a live session, wait one sweep, confirm it is running and answers about work from before it died. Then `kill-session` the whole tmux session and confirm the same.

### Conversation identity

- [X] T011 [US1] Add `NewConversationID()` to `internal/session/id.go` returning a canonical lowercase UUID that satisfies the existing `isConversationID`, seeded from `crypto/rand`.
- [X] T012 [P] [US1] Write `internal/session/id_test.go` cases: the output always passes `ValidateResume`, two calls never collide, and a short read from the entropy source is an error rather than a weak id.
- [X] T013 [US1] In `Manager.Create` (`internal/session/manager.go`), mint a conversation id, insert `--session-id <uuid>` into the rendered start command with the existing `config.InsertStartFlags`, and write `@crswd-conversation` before the command is sent.
- [X] T014 [US1] Extend `Manager.Adopt` in `internal/session/manager.go` to read `@crswd-conversation` back, leaving it empty for sessions that predate the option.
- [X] T015 [P] [US1] Add `HasTranscript(conversationID, workDir string) bool` to `internal/session/conversation.go` — a directory-entry check only, opening no transcript, false on every error.
- [X] T016 [P] [US1] Write `internal/session/conversation_test.go` cases for `HasTranscript`: present, absent, unreadable directory, empty conversation id.

### The journal

- [X] T017 [US1] Create `internal/session/journal.go` with the record type from data-model.md, an `Append` that opens `O_APPEND|O_CREATE|O_WRONLY` at `0600` and fsyncs, and a `Replay` that reads in order with last-record-wins.
- [X] T018 [US1] Make `Replay` discard a truncated final line and skip an unknown `v`, returning the count of each so startup can report it, per `contracts/session-journal.md`.
- [X] T019 [P] [US1] Write `internal/session/journal_test.go` cases: round trip; last record wins; truncated final line discarded; unknown version skipped; missing file is not an error; unreadable file is an error; no token, token hash or pane content is ever marshalled.
- [X] T020 [US1] Append a `created` record in `Manager.Create` and an `ended` record wherever a session becomes terminal (`Destroy`, `DestroyAll`, the reaper's collection path) in `internal/session/manager.go`.

### The supervisor

- [X] T021 [US1] Create `internal/session/supervisor.go` with a `Supervisor` type taking the manager, the audit logger, an injectable `ticker` and the manager's `Clock`, mirroring `reaper.go`'s constructor and `Run` shape.
- [X] T022 [US1] Implement `Supervisor.Sweep` in `internal/session/supervisor.go` against the ordered decision table in data-model.md, taking exactly one `tmux.List` call and judging every session from it.
- [X] T023 [US1] Implement revive-in-place in `internal/session/supervisor.go`: resolve the start command by name, render it, insert `--resume <uuid>`, `SendKeys` the line and Enter into the surviving shell.
- [X] T024 [US1] Implement recreate in `internal/session/supervisor.go`: `New` a tmux session under the same session id, re-write every `@crswd-*` option **before** the command is sent, then revive in place.
- [X] T025 [US1] Persist `ReviveAttempts` and `NextReviveAt` to the journal **before** each attempt, so a daemon that dies mid-revival resumes backing off rather than retrying instantly.
- [X] T026 [US1] Reset `ReviveAttempts` to zero and append a `revived` record on a sweep that observes a previously-dead session running again.
- [X] T027 [P] [US1] Write `internal/session/supervisor_test.go` positive cases: dead Claude with a live shell is revived in place; a missing tmux session is recreated then revived; a healthy session is left completely alone (zero tmux writes).
- [X] T028 [P] [US1] Write `internal/session/supervisor_test.go` invariant cases: revival carries `CreatedAt` unchanged, does not move `LastActivity`, does not mint a credential, and does not change `Owner`.

### Startup

- [X] T029 [US1] In `cmd/crswd/main.go`, replay the journal before the listener binds and before `Adopt`, so tmux wins on conflict, and report the counts of skipped and discarded records.
- [X] T030 [US1] Add `StartSupervisor` beside `StartReaper` in the daemon wiring and start it from `cmd/crswd/main.go`.

**Checkpoint**: quickstart Scenarios 1 and 2 pass by hand; `go test ./...` green.

---

## Phase 4: User Story 2 — Revival never resurrects what the operator ended (Priority: P1)

**Goal**: destroy is final. Nothing the daemon does on its own initiative starts a session the operator ended, or one the reaper collected, or one whose directory left the allowlist.

**Independent test**: destroy a session, run many sweeps, confirm nothing starts and no record reappears.

- [X] T031 [US2] Enforce decision rows 1 and 2 in `internal/session/supervisor.go`: `dead` and `failed` are terminal and a session past its absolute deadline is left to the reaper.
- [X] T032 [US2] Re-resolve `WorkDir` through `ResolveWorkDir` at every revival in `internal/session/supervisor.go`, taking the session to `failed` when the allowlist no longer contains it.
- [X] T033 [US2] Re-check the allowlist, the deadline and the cap for every replayed candidate in `internal/session/journal.go`'s consumer, dropping and recording those that fail.
- [X] T034 [US2] Refuse a recreate that would take the fleet over `CRSW_MAX_SESSIONS`, and ensure a revived session is never double-counted, in `internal/session/supervisor.go`.
- [X] T035 [US2] Guard against two revivals in flight for one session in `internal/session/supervisor.go`.
- [X] T036 [P] [US2] Write `internal/session/supervisor_test.go` negative cases: a destroyed session is never revived across many sweeps; a reaped session is never revived; an expired session is never revived; a de-allowlisted session goes `failed` and starts nothing; an over-cap recreate is refused; overlapping sweeps produce one revival.
- [X] T037 [P] [US2] Write `internal/session/journal_test.go` replay-refusal cases: a replayed session whose directory left the allowlist, whose deadline has passed, or which would exceed the cap is dropped rather than started.

**Checkpoint**: quickstart Scenario 3 passes; every negative case is covered.

---

## Phase 5: User Story 3 — Revival gives up loudly (Priority: P2)

**Goal**: at most three attempts with growing delays, then `failed`, visible on the dashboard, surviving a daemon restart.

**Independent test**: make revival fail deterministically, run many sweeps, confirm attempts stop at the bound and the card shows failed. Restart the daemon; confirm attempts do not resume.

- [X] T038 [US3] Add the bound and the schedule as constants in `internal/session/supervisor.go` — 3 attempts at 5s / 30s / 3m — documented as constants for the same reason `SweepInterval` is.
- [X] T039 [US3] Mark a session `failed` on exhaustion in `internal/session/supervisor.go`, append a `failed` journal record, and stop attempting it.
- [X] T040 [US3] Emit `supervisor.revive`, `supervisor.recreate`, `supervisor.recovered` and `supervisor.failed` audit records per `contracts/session-revival.md`, carrying the session id and a reason constant and nothing else.
- [X] T041 [US3] Ensure a healthy sweep emits no audit record at all, so the four lines that matter are not buried.
- [X] T042 [P] [US3] Write `internal/session/supervisor_test.go` bound cases: attempts stop at three; delays grow; a success resets the count to zero; a restart mid-backoff does not reset the count; a `failed` session is never attempted again.
- [X] T043 [P] [US3] Write `internal/session/supervisor_test.go` trail cases: each action emits exactly one record; no record carries pane content, a credential, a directory the caller spelled, or free text.
- [X] T044 [P] [US3] Add the `failed` pill to the session card in `web/templates/partials/` and `web/static/crswd.css` using existing design tokens only, reusing the canonical status pill rather than adding a second.
- [X] T045 [P] [US3] Surface `failed` in the session view type in `internal/httpapi/view.go` and the card rendering in `internal/httpapi/dashboard.go`.

**Checkpoint**: quickstart Scenario 4 passes, restart included.

---

## Phase 6: User Story 4 — Resume options follow the directory (Priority: P3)

**Goal**: the conversation list on the new-session form belongs to the directory currently chosen, not to whichever one the page suggested at render.

**Independent test**: change the working directory on the form between two allowlisted directories with history; confirm the offered conversations change.

- [X] T046 [US4] Add `GET /sessions/conversations` to `internal/httpapi/routes.go` behind the existing auth and ownership check.
- [X] T047 [US4] Implement the handler in `internal/httpapi/dashboard.go` per `contracts/conversation-lookup.md`: read `dir`, call `session.Conversations`, answer `200` with a possibly-empty list, never 400 and never 404.
- [X] T048 [P] [US4] Write `internal/httpapi/dashboard_test.go` cases: a directory with history; one without; one outside the allowlist; a nonexistent one; an unauthenticated request refused; and that no transcript is opened.
- [X] T049 [US4] Refresh the `resume` select from that route when the working directory changes, in `web/static/crswd.js`, leaving the form usable when the request fails.
- [X] T050 [US4] Update `web/templates/partials/create-form.html` so the select is populated dynamically, and correct the comment that says the list is the first suggested directory's.

**Checkpoint**: quickstart Scenario 5 passes.

---

## Phase 7: Polish & Cross-Cutting

- [X] T051 [P] Document the revival lifecycle, the journal and the `failed` state in `docs/auth-and-sessions.md`, which the constitution's quality gate requires for any session-lifecycle change.
- [X] T052 [P] Document the journal as a new on-disk artifact in `docs/security.md` — what it holds, what it must never hold, and its mode.
- [X] T053 [P] Add the `failed` pill to `docs/components.md` so the card vocabulary stays the single source.
- [X] T054 Add a `quickstart`-tagged acceptance case in `cmd/crswd/` that starts a real daemon, kills Claude in a real tmux session, and asserts revival.
- [X] T055 Add a `tmux`-tagged case in `internal/tmuxctl/` asserting the new format string parses against the real tmux binary.
- [X] T056 Run the full gate: `go build ./...`, `go vet ./...`, `go test ./...`, `go test -tags tmux ./...`, `go vet -tags quickstart ./...`, `go test -tags dev ./...`, `gofmt -l .`, `golangci-lint run`.
- [X] T057 Update `ralph/PROGRESS.md` with what was built, what was verified, and anything that could not be run here.

---

## Dependencies

```
Phase 1  →  Phase 2  →  ┌─ Phase 3 (US1, P1)  ← MVP
                        │      ↓
                        ├─ Phase 4 (US2, P1)  depends on US1's supervisor
                        │      ↓
                        ├─ Phase 5 (US3, P2)  depends on US1's supervisor
                        └─ Phase 6 (US4, P3)  INDEPENDENT of US1–US3
                                 ↓
                              Phase 7
```

- **US1** is the MVP and blocks US2 and US3, which both refine the supervisor it creates.
- **US4** touches only the form and one new read-only route. It shares no file with US1–US3 and can be built at any point after Phase 1.

## Parallel opportunities

- **Phase 2**: T002, T003, T004 are three different files; T008, T009, T010 likewise. T005–T007 are sequential (same two files).
- **Phase 3**: T012, T015, T016 run alongside the journal work; T027 and T028 are the same file and must not be split.
- **Phase 5**: T044 and T045 (the visual work) run alongside T042 and T043 (the tests).
- **Phase 6** runs entirely alongside Phases 3–5.
- **Phase 7**: T051, T052, T053 are three different documents.

## Implementation strategy

Deliver US1 first and stop to verify it by hand against quickstart Scenarios 1
and 2 — that alone fixes the reported failure. US2 is next despite being the same
priority, because it is what makes US1 safe to leave running unattended. US3
makes it safe to leave running when it is *not* working. US4 is a genuinely
separate slice and can ship whenever.
