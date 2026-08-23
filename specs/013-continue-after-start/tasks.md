---
description: "Task list for 013-continue-after-start"
---

# Tasks: Continue a Conversation After the Session Is Running

**Input**: [spec.md](spec.md), [plan.md](plan.md), [contracts/](contracts/)

**Tests**: REQUIRED. `AGENTS.md` — every PR needs a test that fails without the
change, and session code needs the negative cases too. This adds a session-scoped
action, so the negative cases are most of the work.

**Organisation**: by user story. US1 is the MVP and is independent of US2/US3.

---

## Phase 1: Setup

- [X] T001 Confirm the tree is green before touching it: `go build ./...`, `go vet ./...`, `go test ./...`, `golangci-lint run`.

---

## Phase 2: Foundational — the single-restart claim

**Blocks US1: continue and revival must not both restart one shell.**

- [X] T002 Move the in-flight claim out of `Supervisor` and onto `Manager` in `internal/session/`, so both the supervisor and a continue take the same claim rather than two locks with one job.
- [X] T003 Update `internal/session/supervisor.go` to take the claim through the manager, leaving its behaviour unchanged.
- [X] T004 [P] Write `internal/session/supervisor_test.go` cases: a continue in flight blocks a sweep's revival of the same session, and the reverse; two different sessions are unaffected by each other.

**Checkpoint**: `go test ./...` green, no behaviour changed yet.

---

## Phase 3: User Story 1 — continue from a running session (P1) 🎯 MVP

**Goal**: a running session can be pointed at any prior conversation in its own directory.

**Independent test**: start a session in a directory with history, continue one, confirm it answers about that conversation's work and keeps its identifier and expiry.

- [X] T005 [US1] Add `Manager.Continue(ctx, s, conversationID)` to `internal/session/manager.go` implementing the ordered steps in plan.md: validate, refuse non-running, re-resolve the working directory, require a transcript, claim, persist, then restart.
- [X] T006 [US1] Persist the new conversation to the store, the `@crswd-conversation` tmux option and the journal **before** the restart, adding a `continued` journal event in `internal/session/journal.go`.
- [X] T007 [US1] Reuse `revive`'s restart path in `internal/session/manager.go` rather than a second send, so continue and revival cannot diverge in what they type.
- [X] T008 [P] [US1] Write `internal/session/manager_test.go` positive cases: the pane is interrupted and restarted with `--resume <chosen>`; the tmux option, the record and the journal all carry the new conversation; the restart happens after they do.
- [X] T009 [P] [US1] Write `internal/session/manager_test.go` invariant cases: `CreatedAt`, `AbsoluteDeadline`, `Owner`, `TokenHash`, `WorkDir`, `StartCommand` and the session count are unchanged; `LastActivity` **does** move, because a human asked.
- [X] T010 [P] [US1] Write `internal/session/manager_test.go` refusal cases: a dead session, a failed session, an identifier that is not one, the retired `latest` value, a conversation with no transcript, a de-allowlisted directory, and a session already being restarted.
- [X] T011 [US1] Add `patternDashboardContinue` and `continueFromBrowser` to `internal/httpapi/actions.go`, mirroring `modeFromBrowser` step for step, per `contracts/session-continue.md`.
- [X] T012 [US1] Register the action in `internal/httpapi/server.go` with `handleAction` and a new `dashboard.continue` audit action in `internal/audit/audit.go`.
- [X] T013 [US1] Offer the session's own directory's conversations on the session view in `internal/httpapi/dashboard.go` and `internal/httpapi/view.go`, reusing `conversationsForDir`.
- [X] T014 [P] [US1] Write `internal/httpapi/actions_test.go` cases: unauthenticated refused; another operator's session refused as not-found; missing or wrong `confirm` refused; missing page token refused; a directory field in the body is ignored; the trail carries the record's id and never the conversation identifier.
- [X] T015 [US1] Render the Continue control in `web/templates/partials/session-card.html` from the existing action and select vocabulary, stating plainly when there is nothing to continue.
- [X] T016 [P] [US1] Write `internal/httpapi/partials_test.go` cases: the control renders its options as text, carries the page token and the confirm field, and renders an explicit "nothing to continue" state rather than an empty select.

**Checkpoint**: quickstart scenarios 2, 3 and 4 pass by hand.

---

## Phase 4: User Story 2 — the create dialog asks only what it needs (P2)

- [X] T017 [US2] Remove the resume control from `web/templates/partials/create-form.html`, along with the comment block describing it.
- [X] T018 [US2] Remove the hint text "Removes the absolute lifetime. Nothing then reaps this session." from `web/templates/partials/create-form.html`.
- [X] T019 [US2] Remove the create-form conversation script from `web/static/crswd.js`, and the `data-conversations` / `data-resume-latest` attributes it read.
- [X] T020 [US2] Remove `Conversations`, `ResumeLatest`, `ResumeLatestFlag` and `ResumeOneFlag` from the create view in `internal/httpapi/view.go` and `internal/httpapi/dashboard.go`.
- [X] T021 [US2] Remove `CreateRequest.Resume` and its plumbing from `internal/session/manager.go` and `internal/httpapi/actions.go`; `conversationFor` now only mints.
- [X] T022 [P] [US2] Update `internal/httpapi/dashboard_test.go` and `internal/httpapi/partials_test.go`: the create form renders no `name="resume"`, no conversation identifiers, and none of the removed hint text.

**Checkpoint**: quickstart scenario 1 passes.

---

## Phase 5: User Story 3 — "the most recent" is gone (P3)

- [X] T023 [US3] Remove `ResumeLatest` and `ResumeLatestFlag` from `internal/session/`, and make `ValidateResume` refuse the value like any other unrecognised one.
- [X] T024 [US3] Change `SetMode` in `internal/session/manager.go` to restart with `--resume <ConversationID>` instead of `--continue`, and with no flag at all when the session carries no identifier.
- [X] T025 [P] [US3] Write `internal/session/mode_test.go` cases: a mode switch on a session with a conversation carries `--resume <that one>`; one without carries no resume flag; neither carries `--continue`.
- [X] T026 [P] [US3] Write `internal/session/conversation_test.go` cases: `ValidateResume("latest")` is refused with the same sentinel as any other bad value.
- [X] T027 [US3] Assert the absence structurally: a test that fails if `--continue` appears anywhere in the non-test source of `internal/`.

**Checkpoint**: quickstart scenario 5 passes.

---

## Phase 6: Polish

- [X] T028 [P] Update `docs/auth-and-sessions.md`: continuing is a session action, what it may not change, and that `--continue` is gone.
- [X] T029 [P] Update `docs/components.md` with the Continue control.
- [X] T030 Update `specs/012-session-revival/` where it documents `--continue` or `ResumeLatest` as live behaviour, so the older spec does not describe a daemon that no longer exists.
- [X] T031 Run the full gate: build, vet, `go test ./...`, `-tags tmux`, `-tags dev`, `-tags quickstart ./cmd/crswd`, `gofmt -l .`, `golangci-lint run`.
- [X] T032 Append to `ralph/PROGRESS.md`: what was built, what was removed, what was verified, and the `SetMode` consequence.

---

## Dependencies

```
T001 → T002-T004 → ┌─ Phase 3 (US1, P1)  ← MVP
                   ├─ Phase 4 (US2, P2)  independent of US1
                   └─ Phase 5 (US3, P3)  touches the same files as US2
                          ↓
                       Phase 6
```

US2 and US5 overlap in `create-form.html` and `manager.go`, so they are done in
order rather than in parallel. US1 shares no file with either.

## Implementation strategy

US1 first and verify it by hand — it is the capability being asked for. US2 and
US3 are removal, and removal is safest once the thing replacing it works.
