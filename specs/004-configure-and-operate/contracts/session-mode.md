# Contract: Session mode and the remote-control toggle

**Files**: `internal/session/manager.go`, `internal/httpapi/actions.go`
**Tests**: `internal/session/mode_test.go`, `internal/httpapi/actions_test.go`
**Satisfies**: FR-026 … FR-032, SC-007

---

## Mode is derived

```go
func (s Session) Mode() Mode   // ModeLocal | ModeRemote
```

Derived from `Session.StartCommand`, which is a configured **name** and never a
command line. There is no `RemoteControl` boolean, because two fields that must
agree are two fields that can disagree — after a rename, after a restart, or
after a toggle that half-succeeded.

Which names mean remote is configuration: `remote_start_commands`, a list of
names that must each exist in `start_commands`. A name in one and not the other
is a startup refusal, not a runtime surprise.

## Persistence

`StartCommand` becomes the **fifth** tmux user option, joining the four that
already survive a daemon restart:

| Option | Holds |
|---|---|
| `@crswd-managed` | (existing) |
| `@crswd-owner` | (existing) |
| `@crswd-name` | (existing) |
| `@crswd-workdir` | (existing, base64 — paths may contain `\|`) |
| `@crswd-start` | **new** — the configured start-command *name* |

A restored session with no `@crswd-start` is `ModeLocal`. That is the correct
reading of a session started before this milestone, and it is not an error.

## The toggle

| Property | Literal |
|---|---|
| Path | `/dashboard/sessions/{id}/mode` |
| Method | `POST` |
| Form field | `mode`, one of `local` or `remote` |
| Confirming field | `confirm`, must equal `yes` (FR-029) |
| Audit action | `session.mode` |
| Success | `303` to the session page (per [the PRG contract](../spec.md)) |
| Cross-site gate | Both halves, unchanged |

### What a transition must preserve

Ending and restarting the process inside the pane, **without** ending the
session:

1. The tmux session, its window, and its scrollback survive (FR-028).
2. The new process is started with `--continue`, so the conversation survives.
3. The session identifier, credential and lifetime are unchanged — same session,
   different mode.

### Ambiguity is a refusal

Where "the last conversation in this directory" could resolve to another
session's, the daemon **refuses** rather than resuming the wrong one (FR-032).
Resuming a stranger's conversation into this operator's pane is the failure this
prevents, and a refusal the operator can retry is strictly better than a success
they cannot undo.

## No command line, in either direction

`mode` is `local` or `remote`. It selects among commands the **operator
configured**. No command line reaches the browser, and none arrives from it
(FR-030). A `mode` value naming anything else is the uniform refusal.

## Contract tests

| Test | Asserts | **Must fail when** |
|---|---|---|
| `TestModeDerivedFromStartCommand` | A session started with an `rc` name reports `ModeRemote` | A second stored field is introduced |
| `TestStartCommandSurvivesRestart` | `@crswd-start` is set at start and read back after re-adoption | The fifth option is not written |
| `TestRestoredSessionWithoutOptionIsLocal` | No `@crswd-start` → `ModeLocal`, no error | Absence is treated as a failure |
| `TestToggleRequiresConfirm` | `confirm` absent or ≠ `yes` → refused, mode unchanged | The confirming step is dropped (FR-029) |
| `TestTogglePreservesSessionAndScrollback` | Same tmux session id and scrollback after toggling | The implementation destroys and recreates |
| `TestTogglePassesContinue` | The restarted command includes `--continue` | The flag is omitted — the conversation is lost, breaking SC-007 |
| `TestToggleKeepsIdentifierAndLifetime` | Session id, credential and lifetime unchanged | A toggle mints a new record |
| `TestArbitraryModeValueRefused` | `mode=claude --dangerously-skip-permissions` → uniform refusal | The value is passed through as a command |
| `TestModeNotInStartCommandsRefusedAtStartup` | A `remote_start_commands` name absent from `start_commands` refuses at startup | The mismatch is deferred to runtime |
| `TestAmbiguousResumeRefuses` | Two candidate conversations → refusal, not a guess | The daemon picks the most recent (FR-032) |
| `TestToggleEmitsExactlyOneAuditRecord` | One record, action `session.mode` | Start and stop each emit one |
| `TestToggleCrossSiteBothHalves` | Missing `Sec-Fetch-Site` **and** a bad page token each refuse independently | A test disables either half instead of satisfying it (AR-005) |
| `TestCardShowsMode` | The card renders the mode textually, not by colour alone | Mode is shown as a coloured dot (FR-031, FR-059) |
