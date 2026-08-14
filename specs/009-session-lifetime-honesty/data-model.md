# Data Model: Session Lifetime Honesty

What changes on each type. Nothing here is a new store; the only durable additions
are one tmux user option and one schema version.

---

## `session.Session`

| Field | Change | Note |
|---|---|---|
| `Idle time.Duration` | **removed** | The per-session idle override. Nothing replaces it. |
| `TmuxActivity time.Time` | **removed** | Fed `IdleSince` and nothing else. |
| `Lifetime time.Duration` | **unchanged in shape, newly durable** | Zero means the configured default; negative means the absolute deadline is off; positive is that lifetime. Now written to and read from the host. |
| `CreatedAt` | unchanged | Still the origin of the absolute deadline, still tmux's `#{session_created}` for an adopted session. |
| `LastActivity` | **kept** | It is still moved by `Resolve`, `Compact` and `SetMode`, and still shown as "last driven". It no longer feeds any deadline. |

Methods removed: `IdleDeadline`, `IdleSince`, `IdleDisabled`.
Method kept unchanged: `AbsoluteDeadline`, `LifetimeDisabled`.
`DisplayState`: `DisplayIdle` removed; a session that would have shown it now shows
`DisplayRunning`.

**Invariant that survives**: `TokenExpiry` follows `AbsoluteDeadline`, so a session
that never expires still has a token that never expires — "equal by construction",
unchanged.

---

## `tmuxctl.SessionInfo`

| Field | Change |
|---|---|
| `Activity time.Time` | **removed** |
| `Lifetime string` | **added** — the raw option value, `""`, `never`, or a duration string. Parsed by `internal/session`, not here. |

`tmuxctl` stays a transport: it reads the option as a string and forms no opinion
about what a lifetime means. Parsing lives with the type that owns the rule.

### The `List` format string

Seven fields become seven — one removed, one added — but the **order changes**, so
`parseSessions` and the fake must move together:

```
#{session_name}|#{session_created}|#{@crswd-managed}|#{@crswd-name}|#{@crswd-workdir}|#{@crswd-start}|#{@crswd-lifetime}
```

`session_name` may contain `|`, so the last six splits are still found from the
right and everything before them is the name. The count guard that keeps the format
and the parser in step is a test, not a comment.

---

## tmux user options

| Option | Status | Value |
|---|---|---|
| `@crswd-managed` | unchanged | `1` |
| `@crswd-owner` | unchanged | the caller id |
| `@crswd-name` | unchanged | the label, raw |
| `@crswd-workdir` | unchanged | base64 |
| `@crswd-start` | unchanged | the configured command *name*, raw |
| `@crswd-lifetime` | **new** | `""`, `never`, or `time.Duration.String()`, raw |

Written by `Manager.start` in the same sequence as the others, and — like
`@crswd-start` — **written even when empty**, so that "set to nothing" and "never
set" cannot be told apart by adoption and no branch exists to get wrong.

---

## `config.Config`

| Field | Change |
|---|---|
| `IdleTimeout time.Duration` | **removed** |
| `IdleTimeoutMax time.Duration` | **removed** |
| `SessionLifetime`, `SessionLifetimeMax` | unchanged |

Environment constants `EnvIdleTimeout` and `EnvIdleTimeoutMax` are removed, which
removes their file keys by the package's own rule (a key is its variable minus the
prefix, lower-cased).

### Schema

`SchemaVersion` 1 → 2, and a new `retiredKeys` set:

```go
var retiredKeys = map[string]string{
    "idle_timeout":     "the idle timeout was removed in schema 2; sessions are no longer reaped for inactivity",
    "idle_timeout_max": "the idle timeout was removed in schema 2; sessions are no longer reaped for inactivity",
}
```

A file carrying one is refused with that sentence and the `crswd config migrate`
instruction. `config migrate` drops them and writes `version = 2`, keeping the
backup it already keeps.

---

## `session.Manager`

| Member | Change |
|---|---|
| `defaultIdle`, `maxIdle` | **removed** |
| `SetLifetimes(defaultLifetime, maxLifetime, defaultIdle, maxIdle)` | → `SetLifetimes(defaultLifetime, maxLifetime)` |
| `resolveLifetimes` | returns `(lifetime, error)`; the idle half and the "an idle timeout that can never fire" refusal go with it |
| `syncActivity` | **removed** |
| `CreateRequest.Idle` | **removed** |
| `CreateRequest.Resume` | **added** — `ResumeNone`, `ResumeLatest`, or a validated conversation id |

---

## `session.Conversation` (new)

```go
type Conversation struct {
    ID       string    // a validated UUID
    Modified time.Time // the file's mtime — "last written"
}
```

Returned newest-first by `Conversations(workDir string) []Conversation`, which
never returns an error: every failure is an empty slice (`FR-021b`). No field
carries content, a title, a size, or a path — `FR-025`.

---

## `httpapi` view models

| Field | Change |
|---|---|
| create view `IdleDefault`, idle hint | **removed** |
| create view `Commands map[bool]string` | **added** — the resolved command line per mode, for the preview |
| create view `Conversations []Conversation` | **added** |
| session view `IdleDeadline`, `IdleDisabled` | **removed** |
| session view `NeverExpires bool`, `Age`, `RemoteControl bool` | **present or added** — `US5`'s three facts |
| `sessionResponse.idle_deadline` (JSON) | **removed** |

---

## Form fields

| Field | Change |
|---|---|
| `name`, `work_dir`, `remote_control` | unchanged |
| `lifetime` | unchanged — `never`, or a duration |
| `idle_timeout` | **removed** |
| `resume` | **added** — absent, `latest`, or a UUID |
