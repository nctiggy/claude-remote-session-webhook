# Contract: continuing a prior conversation

Covers FR-019 through FR-025. This is the feature's one new security surface.

## The form field

`resume`, with three states:

| Posted | Meaning | Command gains |
|---|---|---|
| absent or empty | start fresh | nothing |
| `latest` | most recent conversation in the working directory | `--continue` |
| a UUID | that conversation | `--resume <uuid>` |

## Validation — the control that matters

The start command is **typed into a live shell** by `SendKeys`. A resume
identifier is a flag argument, so it must be on that line; it cannot go through
`Paste`.

Therefore: `resume` is accepted only as the literal `latest` or as a strict
UUID — `^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`.
Lowercase hexadecimal and hyphens, exactly 36 characters. Everything else is
refused with the route's uniform refusal.

The check runs **twice**: at the HTTP boundary, and again in the daemon before the
value is rendered into a command line. The second is not redundant — it is the
check that holds if a future caller reaches `Create` another way.

That alphabet contains no shell metacharacter, no whitespace, no quote and no
newline. A value that passes cannot change the shape of the line it lands on. No
quoting or escaping is attempted, because escaping is a thing to get wrong.

## Where the flags land

Immediately after the first whitespace-separated token of the configured template
— the binary — and before everything else:

```
claude --dangerously-skip-permissions "/rc {name}"
claude --continue --dangerously-skip-permissions "/rc {name}"
claude --resume 88e5294c-... --dangerously-skip-permissions "/rc {name}"
```

Appending at the end is refused by design: the configured commands end in a quoted
prompt argument, and whether an argument parser honours a flag after a positional
is not something this daemon may assume.

The insertion lives in `internal/config` beside `RenderStartCommand`.

## Discovery

`Conversations(workDir)` lists `$HOME/.claude/projects/<encoded>/*.jsonl`.

- **Encoding**: the working directory with `/` and `.` replaced by `-`.
- **Read**: directory entries only. **No `.jsonl` is ever opened** — `FR-025`, and
  the largest on the deployed host is 115 MB.
- **Returned**: id and modification time, newest first. Nothing else.
- **Never errors**: no `$HOME`, no directory, an unreadable directory, or a
  non-UUID name all yield an empty slice. The form still renders; a create still
  works.

### Bounding the disclosure

The working directory is resolved through `ResolveWorkDir` — the create's own
allowlist check — **before** it is encoded into a path. The set of directories
whose conversations can be listed is exactly the set the operator may start a
session in. A directory outside the roots is refused identically to a create
there, and the refusal does not distinguish "outside the roots" from "not there".

## Failure to satisfy

A create asking to continue a conversation that no longer exists must not quietly
start a fresh session — `FR-024`. The daemon does not pre-check existence (it
would be a race and a second disclosure); instead the operator is told what was
requested, and the started session shows the CLI's own answer in its pane, which
is where the truth is.

A `latest` request in a directory with no conversations starts a fresh session,
which is what `--continue` does, and the form says so beside the control rather
than offering a choice that reads like a promise.

## Tests that must fail without the change

- `resume=latest` → the typed command contains `--continue` after the binary.
- `resume=<uuid>` → the typed command contains `--resume <uuid>` after the binary.
- `resume=;rm -rf ~` → refused, no session created, no command typed.
- `resume=88E5294C-...` (uppercase) → refused.
- `resume=` absent → the command is byte-identical to today's.
- `Conversations` on a missing directory → empty, no error.
- `Conversations` on a directory outside the roots → refused before any read.
- A `.jsonl` file is never opened: assert against a directory whose file would
  error on read.
