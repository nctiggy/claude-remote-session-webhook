# Implementation Plan: Session Revival

**Branch**: `feat/012-session-revival` | **Date**: 2026-08-22 | **Spec**: [spec.md](spec.md)

**Input**: Feature specification from `/specs/012-session-revival/spec.md`

## Summary

A session whose Claude process stops — or whose whole shell is destroyed, as the
kernel OOM killer did on 2026-08-22 — is brought back automatically, continuing
the conversation it was already having, under the same identity, owner and
absolute deadline. The daemon mints a conversation identifier at create
(`--session-id`), keeps it in a durable append-only journal beside its
configuration, notices both kinds of death from the one tmux listing it already
makes, and revives with `--resume`. Revival is bounded: three attempts with
growing delays, then the session goes `failed` and visible rather than looping.
Separately, the new-session form's conversation list follows the working
directory the operator actually chose.

## Technical Context

**Language/Version**: Go 1.24 (`go.mod`)

**Primary Dependencies**: standard library; `tmux` as an external binary driven
through `internal/tmuxctl`; the `claude` CLI as the process being supervised. No
new module dependency.

**Storage**: one append-only JSONL journal at
`$XDG_CONFIG_HOME/crswd/sessions-<listen-address>.jsonl` (else `~/.config/crswd/`). No database —
explicitly ruled out by the operator, and nothing here needs queries.

**Testing**: `go test ./...` (table-driven, `t.Parallel()`, no network, no real
tmux) plus the `tmux` tag for the real-binary path and `quickstart` for
acceptance.

**Target Platform**: Linux, systemd user unit, loopback listener behind a tunnel.

**Project Type**: single Go service with server-rendered templates.

**Performance Goals**: one `tmux list-sessions` per 30s sweep regardless of fleet
size; a dead session is running again within one sweep (SC-001).

**Constraints**: no new inbound route may widen the auth surface; no shell string
may be constructed; nothing caller-supplied may reach a command line unvalidated;
no secret or pane content in the journal or the trail.

**Scale/Scope**: a single operator's fleet, bounded by `CRSW_MAX_SESSIONS`
(single digits in practice).

## Constitution Check

*Checked before Phase 0 and re-checked after Phase 1. Both passes recorded.*

| Principle | Assessment | Pass |
|---|---|---|
| **I — Security is a gate** | One new route (`GET /sessions/conversations`) behind the existing door, with the existing ownership check. It discloses strictly less than the form already renders and is bounded by the same `ResolveWorkDir`. The only new value reaching a command line is a daemon-minted UUID, already covered by `ValidateResume`. No shell string is constructed — `config.InsertStartFlags` is reused. | ✅ |
| **II — Unknowns surfaced** | Two open questions were put to the operator before planning and answered: the clean-exit distinction (FR-027) and the scope boundary, the latter re-decided against journal evidence rather than preference. No `NEEDS CLARIFICATION` remains. | ✅ |
| **III — Every change is verifiable** | Every FR is an observable behaviour; `quickstart.md` gives a runnable check per user story; the supervisor takes an injectable ticker and clock so its bound is testable without waiting three minutes. | ✅ |
| **IV — Smallest correct change** | Reuses `Conversations`, `ValidateResume`, `ResolveWorkDir`, `InsertStartFlags`, `Adopt`, the `@crswd-*` option convention and the `State` vocabulary. Adds one type, one option, one route, one file. | ✅ |
| **V — Standards enforced** | No guardrail changed. The existing hooks and CI gates apply unmodified. | ✅ |
| **VI — Blast radius bounded** | The one principle this feature could weaken, because it *starts unsandboxed shells on the daemon's own initiative*. Every existing bound is re-applied per revival and none is relaxed — see below. | ✅ |
| **VII — Design system binding** | One new state on the existing session card, using existing tokens and the canonical component. No new component. Pane output still rendered as text. | ✅ |

### Principle VI in detail — what revival may not reach

Revival is the first thing in this daemon that causes execution without a request
behind it. That is the whole of its risk, and each of the six bounds is
re-established rather than assumed:

- **Allowlist** — the recorded working directory is re-resolved through
  `ResolveWorkDir` at every revival and again at journal replay. A directory that
  left the allowlist takes its session to `failed` (FR-013).
- **Cap** — a revived session was already counted and stays counted; recreation is
  refused if the fleet is at `CRSW_MAX_SESSIONS` (FR-011).
- **Absolute lifetime** — `CreatedAt` is carried through revival and replay, never
  refreshed. Revival cannot extend a life, and a session past its ceiling is left
  to the reaper (FR-010, FR-012).
- **Verified teardown** — untouched. Revival never destroys; `dead` is terminal.
- **Loopback** — untouched. No listener change.
- **Ownership** — a recreated shell is re-marked `@crswd-managed` and re-owned
  *before* the start command is sent, so a failure part-way leaves something
  `Adopt` recognises rather than an unowned unsandboxed shell (FR-015b).

The bound that is genuinely new is the **give-up rule**, and it is a bound on the
daemon rather than on the caller: at most three attempts per death, written
before the attempt so a crash mid-revival cannot reset them.

## Project Structure

### Documentation (this feature)

```text
specs/012-session-revival/
├── plan.md                            # this file
├── spec.md
├── research.md                        # D1–D8
├── data-model.md
├── quickstart.md
├── contracts/
│   ├── session-revival.md
│   ├── session-journal.md
│   └── conversation-lookup.md
├── checklists/requirements.md
└── tasks.md                           # /speckit-tasks
```

### Source Code (repository root)

```text
internal/session/
├── supervisor.go       NEW  the sweep that revives; ticker/clock shape borrowed from reaper.go
├── supervisor_test.go  NEW
├── journal.go          NEW  append-only read/write/replay of the session journal
├── journal_test.go     NEW
├── session.go          MOD  +ConversationID +ReviveAttempts +NextReviveAt, +StateFailed
├── manager.go          MOD  mint the UUID at Create; Revive/Recreate; replay at startup
├── conversation.go     MOD  HasTranscript(id, workDir) for FR-014
└── reaper.go           —    unchanged; the supervisor sits beside it

internal/tmuxctl/
├── controller.go       MOD  +OptionConversation; List carries the pane command
├── exec.go             MOD  format string + row parse
└── fake.go             MOD  match the new format

internal/config/
└── file.go             MOD  JournalPath, resolved from the same base as DefaultPath

internal/httpapi/
├── routes.go           MOD  GET /sessions/conversations
├── dashboard.go        MOD  the handler; card shows `failed`
└── view.go             MOD  the view type

web/
├── templates/partials/create-form.html   MOD  the select is refreshed, not fixed
├── static/crswd.js                       MOD  fetch on working-directory change
└── static/crswd.css                      MOD  the `failed` pill, from existing tokens

cmd/crswd/
└── main.go             MOD  replay the journal, then StartSupervisor beside StartReaper
```

**Structure Decision**: the existing layout is kept exactly. Two new files in
`internal/session` because both are session lifecycle concerns and that package
already owns the reaper; nothing new anywhere else.

## Complexity Tracking

> No constitution violations. One deliberate cost is recorded here because a
> reviewer will otherwise ask why the same fact is stored twice.

| Choice | Why needed | Simpler alternative rejected because |
|---|---|---|
| Conversation id stored **both** as a tmux option and in the journal | The option is the cheap path when the shell survived and is what `Adopt` already reads; the journal is the only copy that outlives the shell | Option only — the observed failure (OOM killing the whole `tmux-spawn` cgroup) destroys the tmux session and every option on it, which is precisely the case that must recover. Journal only — would make `Adopt` read a file for a fact tmux is already holding |
| `Supervisor` as a type separate from `Reaper` | They move in opposite directions: one ends sessions, one starts them | Folding into `Reaper.Sweep` — one fewer file, but one type that both starts and stops unsandboxed shells, and `Reaper`'s stated contract would have to be rewritten (Principle IV) |
