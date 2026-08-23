# Contract: continue a conversation

## Route

```
POST /dashboard/sessions/{id}/continue
```

Registered with `handleAction`, so it inherits the action gate every mutating
dashboard route has: an authenticated operator, a valid page token, and the
cross-site checks. No new door.

## Request

| Field | Meaning |
|---|---|
| `conversation` | The conversation to continue. A validated identifier, never free text. |
| `proof` | The page token, as every action requires. |
| `confirm` | Must be exactly `yes`. |

**There is no directory field.** The working directory is read from the session's
own record. A route that accepted one would be a route that could list and resume
conversations from a directory the session was never started in.

**`confirm=yes` is compared, never parsed** — the same rule destroy and mode
apply. Continuing ends the process the operator is watching; `on`, `true`, `1` and
an empty value are what a stray checkbox or a hand-built request produce, and none
of them is the deliberate act this asks for.

## Refusals

Every refusal answers the caller identically and is distinguished only in the
trail, exactly as the other session-scoped actions are.

| Condition | Trail reason |
|---|---|
| Identifier is not a conversation identifier | invalid conversation |
| Identifier is the retired "most recent" value | invalid conversation — it is refused like any other unrecognised value |
| Session is not the caller's, or does not exist | the existing resolve vocabulary |
| Session is dead or failed | session is not running |
| Working directory no longer inside the allowlist | working directory refused |
| No transcript for that conversation on this host | nothing to continue |
| A restart of this session is already in flight | busy |

## What it does

1. Writes the chosen conversation to the record, the `@crswd-conversation` tmux
   option and the journal.
2. Interrupts the pane and re-sends the session's own start command with
   `--resume <conversation>`.

Steps in that order. A daemon that dies between them comes back knowing which
conversation the session is on, and its supervisor revives it into that one.

## What it never does

- Extend a lifetime, or move `CreatedAt`. Continuing restarts a process, not a
  session.
- Mint a credential. The record never went away.
- Change the concurrent-session count.
- Move `LastActivity`... **it does**, and deliberately: unlike revival, a human
  asked for this. It is a driving, and the three calls that drive a session
  already touch it.
- Run while another restart of the same session is in flight.

## Audit

One record per request, `dashboard.continue`, carrying the session id from the
daemon's own record and a reason constant. Never the conversation identifier —
it is caller-supplied text, and FR-042 keeps caller text out of the trail.
