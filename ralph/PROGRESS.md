# Progress

> Append-only notebook. Newest at the bottom. Never edit or delete past entries —
> this is the loop's only memory across fresh contexts.

Each iteration appends:

```
## Iteration N — YYYY-MM-DD HH:MM
**Did:** one or two lines.
**Learned:** anything that would otherwise be rediscovered the hard way.
**Left:** what remains.
**Findings:** problems noticed but not fixed (ad-hoc bugs, smells, risks).
```

Findings are the point of this file as much as progress is. An observation that
dies in a context window is a bug you will pay for twice. Real ad-hoc fixes also
get a one-liner in `docs/fixes-log.md`.

When the whole plan is done and green, append a line containing exactly

---

## Iteration 0 — make idle mean what it says

**Did:** Reset the notebook. The previous plan (a config template) was **wrong and
was correctly blocked**: `config.example` already exists at the repository root,
already carries every key, and is already guarded in both directions by
`configExamplePath` in `internal/config/file_test.go`. The plan told an iteration
to create a duplicate and it refused. That block was right.

**Left:** the tasks below.

**The operator's question, and the answer they were owed:** *"I have no idea what
idle means… even if I am using the session I think it is still considered idle."*

They are right, and it is worse than they put it. `LastActivity` is advanced by
exactly three things — `Resolve` (the signed-API credential path), `Compact`, and
`SetMode`. Reading does not: the dashboard, the session page and the **live
stream** all go through `View`, which has no clock reading in it by construction.
There is no route to type into a session from the browser at all; `Prompt` exists
only on the signed API.

**So a browser-driven operator watching a session all day has it reaped at sixty
minutes**, and someone attached to the tmux session directly on the host is
invisible to the clock entirely. "Idle" measures daemon-mediated mutating HTTP
requests and calls that activity.

**What makes this fixable rather than a trade:** tmux tracks the real thing.
`#{session_activity}` is a timestamp of when the session last produced output.
`argvList()` in `internal/tmuxctl/fake.go:94` already runs `list-sessions -F` with
six fields on every reconciliation — this is a seventh field on an exec that
already happens, not a new call.

It also answers the objection that killed the obvious fix: counting browser reads
would let a forgotten tab hold an unsandboxed shell open forever. Real tmux
activity is not a forgotten tab.
