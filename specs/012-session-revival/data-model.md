# Data Model: Session Revival

**Feature**: 012-session-revival | **Date**: 2026-08-22

## Session — three new fields

| Field | Type | Meaning |
|---|---|---|
| `ConversationID` | `string` | The UUID the daemon minted at create and passes to `--session-id`, then to `--resume`. Empty for every session created before this feature — such a session is supervised but never revived by identifier (FR-005). |
| `ReviveAttempts` | `int` | Consecutive failed revivals since the last success. Reset to 0 on success (FR-020). |
| `NextReviveAt` | `time.Time` | Earliest instant the next attempt may be made. Zero means "now". |

`State` gains one value:

```
starting → running → dead        (existing)
              ↓
           failed                (new: revival gave up)
```

| State | Meaning | Revivable |
|---|---|---|
| `starting` | Created, command not yet observed running | no — not yet expected to be up |
| `running` | Claude observed running | n/a — nothing to do |
| `dead` | Destroyed by operator or reaper | **never** (FR-012) |
| `failed` | Revival exhausted its attempts (FR-018) | **never**, until the operator acts |

`dead` and `failed` are both terminal, and they are distinct on purpose: `dead`
is a session somebody ended, `failed` is a session the daemon could not save. An
operator reading a card must be able to tell those apart.

## Tmux user option — one new

| Option | Value | Read by |
|---|---|---|
| `@crswd-conversation` | the conversation UUID | `Adopt`, as the fast path when the tmux session survived |

Joins `@crswd-managed`, `@crswd-owner`, `@crswd-name`, `@crswd-workdir`,
`@crswd-start`, `@crswd-lifetime`. It is a **cache, not the authority** — the
journal is the authority, because this option dies with the tmux session and the
observed failure is exactly that (research D2).

## Journal record

One JSON object per line in `~/.config/crswd/sessions-<listen-address>.jsonl`. Last record for an
id wins.

| Field | Type | Notes |
|---|---|---|
| `v` | `int` | Schema version, `1`. An unknown version is skipped, not fatal. |
| `at` | RFC3339 | When the record was written. |
| `id` | `string` | Session id, 32 lowercase hex. |
| `event` | `string` | `created` \| `revived` \| `failed` \| `ended` |
| `owner` | `string` | Caller id, so a replayed session is re-owned rather than unowned. |
| `conversation` | `string` | The UUID. |
| `workdir` | `string` | Canonical, allowlist-checked at replay **again** — an allowlist that shrank must shrink what replays. |
| `start` | `string` | Start command **name**, never a command line. |
| `lifetime` | `string` | Duration, so a session never meant to expire replays as such. |
| `created` | RFC3339 | Original creation, so the absolute deadline is not restarted by replay (FR-010). |
| `attempts` | `int` | Carried so a daemon restart cannot reset the bound (FR-019). |

**What it must never carry**: a token or token hash, pane content, conversation
content, or any caller-supplied free text beyond the fields above. The journal is
a file on disk with no request behind it, and FR-022 applies to it as it applies
to the trail.

### Why these fields and no others

Everything here is something the daemon cannot rediscover after the tmux session
is gone. Anything it *can* rediscover — whether a session is currently running,
what its pane shows — is deliberately absent, because a persisted copy of a fact
the host owns is a second source that can disagree with the host.

## Revival decision

Evaluated per session, per sweep, in order. The first match wins.

| # | Condition | Outcome |
|---|---|---|
| 1 | `State` is `dead` or `failed` | skip — terminal (FR-012, FR-018) |
| 2 | Past its absolute deadline | skip; the reaper owns it (FR-012) |
| 3 | Tmux session present **and** pane command is the start binary | healthy — nothing to do (US1 §4) |
| 4 | `now < NextReviveAt` | skip — backing off (FR-017) |
| 5 | `ReviveAttempts >= 3` | mark `failed`, record give-up (FR-018) |
| 6 | `WorkDir` no longer resolves in the allowlist | mark `failed`, record refusal (FR-013) |
| 7 | `ConversationID` empty, or no transcript on disk | mark `failed`, record refusal (FR-014, FR-005) |
| 8 | Reviving this session already in flight | skip (FR-015) |
| 9 | Tmux session present, Claude not running | **revive in place**: send the start command with `--resume` |
| 10 | Tmux session absent | **recreate**: new tmux session, re-mark managed and owned, then as 9 (FR-015a–c) |

Rows 9 and 10 both increment `ReviveAttempts` and set `NextReviveAt` *before*
acting, so a daemon that dies mid-revival resumes backing off rather than
retrying instantly.

## Entities that do not change

`Conversation`, `ValidateResume`, `ValidateName`, `ResolveWorkDir`,
`Store`'s cap accounting, and the audit record shape are all reused as they
stand.
