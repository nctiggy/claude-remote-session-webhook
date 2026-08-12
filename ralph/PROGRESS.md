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

---

## Iteration 1 — 2026-08-12 — T001, the seventh field

**Did:** `argvList()` now asks tmux for `#{session_activity}` as a seventh field,
`parseSessions` reads it, and it rides on `SessionInfo.Activity`. The `Fake`
stores it, stamps it from the injected clock in `New`, carries it through `Seed`,
and exposes `SetActivity` for a test that needs a session tmux says is busy while
the daemon has not heard from it. Nothing consumes `Activity` yet — that is T002.

**Learned:**

- **The real tmux here renders `#{session_activity}` as a Unix timestamp.**
  Verified, not assumed: `TestTmuxListReportsProvenanceAndCreation` now asserts
  it lands within seconds of now, so a tmux that did not know the format (it
  would emit the literal text) fails loudly instead of silently falling every
  session back to the old clock. `go test -tags tmux ./internal/tmuxctl` passes.
- **Activity is the last field, so it is the first cut from the right.** The
  start-command name moved to second-from-right. Everything after the session
  name is still digits, a flag, a validated label, base64, and a validated
  command name — nothing that can carry a `|` — so cutting from the right still
  holds. Only the session name may contain the separator.
- **The parse is deliberately asymmetric.** An unreadable *creation* time still
  fails the row; an unreadable *activity* time yields the zero time and the row
  parses. Failing the row would abandon reconciliation and leave every managed
  session on the host unadopted, which is far worse than measuring idle the way
  yesterday's build did. Two table cases pin this and two more pin that a bad
  creation time still errors.
- **Three places assert the six-field argv**, and all three had to move together:
  `internal/tmuxctl/fake_test.go`, `internal/tmuxctl/exec_test.go`, and
  `internal/session/manager_test.go` (the Adopt argv). Grep for
  `session_created` before changing the format string again.
- **Proved by breaking it.** With the seventh field removed the real-tmux test
  fails on `unreadable row`; with the `Activity` assignments dropped, seven
  parse cases and the fake round-trip test fail. Both restored.

**Left:** T002–T007. T002 is the one that makes any of this matter — nothing
reads `SessionInfo.Activity` yet, and per `docs/conventions.md` a test that
cannot fail is not a test: assert the caller.

**Findings:**

- **⚠️ `go test -tags quickstart ./cmd/crswd` ran while this iteration's tree was
  dirty, and the tree came back clean with the work in `git stash`.** Nothing in
  the repo runs `git stash` (grepped `.claude/`, `.githooks/`, `ralph/`, and the
  suite itself), so the stash came from outside it — the reflog shows the branch
  was also moved from `feat/m13b-idle-real` to `main` at the same moment. **The
  lesson stands regardless of cause: commit before running the quickstart
  acceptance suite, never with uncommitted work in the tree.** That suite builds
  a real binary and binds a real port; it is the one command here with a foot
  outside the process.
- **A leftover `stash@{0}` on this branch is a duplicate of this commit** and can
  be dropped. `stash@{1}` is older and from `fix/42-prg` — not mine, left alone.
- **An iteration can find itself on a different branch than it started on.**
  This one did, and nearly committed T001 onto `main`, which the hard rules
  forbid. `git branch --show-current` before `git commit` is cheap; the loop's
  clean-tree check at `ralph/loop.sh:32` does not cover this.
- **`Fake.Seed` silently drops `Label`, `WorkDir` and `StartCommand`** from the
  `SessionInfo` it is handed — it only carries `Created` and `Managed` (and now
  `Activity`). A test that seeds a labelled session and asserts the label back
  would be testing nothing. Not fixed: outside T001, and no caller relies on it
  today.

---

## Iteration 0b — the finding that nearly cost the milestone

**Did:** Added T000 and recorded why the rest of this plan is worth running.

**The operator, mid-milestone:** *"In the remote control sessions, the only ones
that matter TBH, there is no new activity in the tmux session."*

They were right, and the consequence is larger than the idle clock. Their `rc`
command carried `--spawn`, which makes the tmux session a **launcher**: the
conversation lives on the relay and the pane goes quiet after startup. So for the
sessions this operator actually runs, the pane is empty, the pill cannot tell
running from idle from needs-auth, `compact` types into a pane nobody reads, and
`#{session_activity}` never moves.

**T001 and T002 would have measured a clock that never ticks, for the only
sessions that matter.** It was one iteration from shipping.

The fix is a start command, not daemon code:

    rc=claude --dangerously-skip-permissions "/rc {name}"

An ordinary interactive session that drives itself into remote control, so the
tmux session *is* the session. Verified twice: the operator ran the form and it
worked, and the config parser passes the quotes through intact — `send-keys` is
called without `-l`, so the shell parses them normally.

**The lesson is about where the daemon's assumptions come from.** Everything the
dashboard does — the pane, the pill's inferred states, compact, milestone 7's
whole mobile sweep — assumes the session renders into its own pane. Nothing
checked that assumption, and a start command an operator was free to configure
had quietly broken it for their primary use.

---

## Iteration 2 — 2026-08-12 — T000, the default that renders in its own pane

**Did:** `config.example` and `.env.example` now show
`rc=claude --dangerously-skip-permissions "/rc {name}"` as the remote-control
command, with the `--spawn` launcher kept in both as a documented alternative
and one sentence on why it is no longer the example. A new guard in
`internal/config/file_test.go` loads the uncommentable `start_commands` line
through `config.LoadFrom` and fails if the command the switch resolves to spawns
its session elsewhere.

**Learned:**

- **`config.go` ships no built-in remote-control command line.** T000 said to
  change `DefaultRemoteControlCommand` "or whatever `config.go` names the
  built-in" — there is none. `DefaultRemoteControlCommandName = "rc"` is a
  *name*, and `loadRemoteControlCommand` resolves it only if the operator
  configured a command by that name; otherwise the dashboard renders no switch
  at all. So the two example files **are** the whole of the shipped default, and
  adding a built-in `rc` would have handed a remote-control switch to every
  daemon that configures nothing — a widening nobody asked for. Not done.
- **The guard loads rather than greps.** The claim about that line is that a
  daemon starts on it, so it goes through the daemon's own loader; the
  `--spawn` check is the last assertion, not the only one. Proven by breaking:
  with the launcher form restored it fails naming the file, the line and the
  consequence.
- **The duplicate-key trap is real and it was one indentation away.** Any
  comment line whose text before the first `=` is a known key counts as that
  key's line, indented or not — so the alternative form is written as prose
  (`claude remote-control --spawn=same-dir …`) and never as
  `# start_commands = …`. `exampleLines` in `file_test.go:1298` trims the `#`
  *and* the whitespace before matching.
- **The config-file parser trims only the ends of a value**, so the quotes reach
  `send-keys` intact. That is what makes the quoted form safe *in
  `config.example`*, and it is a property of that parser only.

**Left:** T002–T007. T002 is the one that makes T001 matter — nothing reads
`SessionInfo.Activity` yet.

**Findings:**

- **⚠️ The quoted form may not survive an env file, and I could not verify it.**
  `deploy/crswd.example.service` reads `EnvironmentFile=-%h/.config/crswd/env`
  and sets `Environment="CRSW_START_COMMAND=…"`; systemd parses quotes in both.
  If it strips them, the operator's env-file spelling delivers
  `claude --dangerously-skip-permissions /rc {name}` — two positional arguments
  rather than one prompt — and that is exactly the class of silent breakage this
  task exists to fix. The sandbox refused the `systemd-run` check, so
  `.env.example` says only that the quotes are part of the command line and to
  confirm whatever reads that file leaves them on, and points at
  `config.example`, which takes the value verbatim. **Someone with a shell on a
  systemd host should settle this**; if the quotes are eaten, `deploy/` needs
  the escaped spelling and the unit's `Environment=` line does too.
- **`deploy/README.md`'s recipe and the example unit still teach the env-file
  path for command lines**, and neither was touched here — T000 named
  `config.example` and `.env.example` only. Out of scope, worth a task.
- Iteration 1's finding stands: **commit before running
  `go test -tags quickstart`**. Not run this iteration — nothing under
  `cmd/crswd` changed. `go vet -tags quickstart ./...` and `-tags dev` compile;
  `go test -tags tmux ./...` passes.
