# Contract: conversations for a directory

## Route

```
GET /sessions/conversations?dir=<spelling>
```

Same authentication and same authorization as every other dashboard route. No
new door.

## Request

| Param | Meaning |
|---|---|
| `dir` | The operator's spelling of a working directory, exactly as typed on the form. |

## Response

`200` with the conversations for that directory, newest first:

```json
{"conversations":[{"id":"7f3a…","modified":"2026-08-22T16:31:00Z"}]}
```

`200` with an empty list for: a directory outside the allowlist, a directory that
does not exist, a directory with no history, an unreadable directory, or a
Claude layout this daemon does not recognise.

**Every failure is an empty list.** There is no 400 and no 404, for the reason
`session.Conversations` never returns an error: a form that refused to render
because somebody else's release moved a directory would be this daemon broken by
a change it has no part in, and the worst outcome of an empty list is an operator
who starts a fresh session — which is what they got before the feature existed.

## What it discloses

Exactly what the form already discloses today: that work has happened in a
directory, how many times, and when. **No transcript is opened** (FR-025). No
title, no first message, no size, no path.

## What bounds it

`session.Conversations` resolves `dir` through `ResolveWorkDir` before it becomes
a path, so the set of directories whose conversations can be listed is exactly the
set the operator may start a session in. An unowned or unlisted directory
discloses nothing, and discloses it the same way an empty one does — so the
response cannot be used to probe which directories exist.
