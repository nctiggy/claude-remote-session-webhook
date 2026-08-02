# Contract: `internal/tmuxctl` Controller

**Feature**: [../spec.md](../spec.md) · **Research**: [../research.md](../research.md)

The only place in the codebase that executes anything. Every method builds an **argv
slice** for `exec.Command` — no `sh -c`, no `fmt.Sprintf` into a command line, ever
(FR-029).

Every argv below was verified against tmux 3.4 on an isolated socket. The exact forms
matter more than they look: three of them fail silently if written the obvious way.

---

## Interface

```go
type Controller interface {
    New(ctx context.Context, name, workDir string) error
    SendKeys(ctx context.Context, name string, keys ...string) error
    Paste(ctx context.Context, name string, payload []byte) error
    CapturePane(ctx context.Context, name string) (string, error)
    Kill(ctx context.Context, name string) error
    Has(ctx context.Context, name string) (bool, error)
    SetOption(ctx context.Context, name, option, value string) error
    List(ctx context.Context) ([]SessionInfo, error)
}

type SessionInfo struct {
    Name    string    // tmux session name, e.g. "crswd-9f2c..."
    Created time.Time // from #{session_created}
    Managed bool      // from #{@crswd-managed} — did WE create this?
}
```

`name` is always the tmux session name (`crswd-<id>`), built from the session ID alone.
No caller-supplied string reaches this package (FR-034).

---

## Target strings — the trap

tmux takes two *kinds* of target and they need **different exact-match syntax**. Getting
this wrong does not produce a type error; it produces a runtime "can't find pane" or,
worse, a prefix match against a different session.

```go
func SessionTarget(name string) string { return "=" + name }        // has-session, kill-session
func PaneTarget(name string) string    { return "=" + name + ":" }  // everything else
```

Verified (see [research D2](../research.md)):

| Command | `=name` | `=name:` |
|---|---|---|
| `has-session` | works | — |
| `kill-session` | works | — |
| `send-keys` | **`can't find pane`** | works |
| `capture-pane` | **fails** | works |
| `set-option` | **`no such session`** | works |
| `paste-buffer` | **fails** | works |

Two exported helpers, not one `Target()`, precisely so a caller cannot pick the wrong
one. A unit test asserts each helper's output format.

---

## `New` — create a session

```
tmux new-session -d -s <name> -c <workDir>
```

Starts the login shell only. The Claude command is delivered separately as keys, so a
Claude crash leaves an inspectable shell rather than a dead session (FR-018, and the
operator's resolved decision).

Immediately after, the manager marks provenance so reconciliation can recognise it later
([research D3](../research.md)):

```
tmux set-option -t =<name>: @crswd-managed 1
tmux set-option -t =<name>: @crswd-owner   <owner>
```

then sends the start command:

```
tmux send-keys -t =<name>: -l -- "claude --dangerously-skip-permissions"
tmux send-keys -t =<name>: Enter
```

`-l` is safe here because this string is a **daemon-authored constant**, not caller
input.

---

## `Paste` — deliver caller-supplied text (the important one)

```
tmux load-buffer -b crswd-<id> -        # payload on stdin, never on the command line
tmux paste-buffer -d -b crswd-<id> -t =<name>:
```

**Never `send-keys -l` for caller text.** tmux's own command parser strips a trailing
unescaped `;` from the final argument *before* `-l` applies, and `--` does not prevent
it. Verified:

| sent via `send-keys -l --` | landed |
|---|---|
| `;` | *(empty — swallowed)* |
| `foo;` | `foo` |
| `foo;;` | `foo;` |
| `foo;bar` | `foo;bar` (fine) |

The same payloads survive `load-buffer`/`paste-buffer` byte-for-byte, because the text
travels on stdin and never becomes part of a tmux command line. `-d` deletes the buffer
after pasting, so prompt text does not linger where another tmux client could read it —
prompts are secret under `docs/security.md` §3.

Implementation: `cmd.Stdin = bytes.NewReader(payload)`. Nothing is written to disk.

After a paste, `SendKeys(name, "Enter")` submits it.

---

## `CapturePane` — read the screen

```
tmux capture-pane -p -t =<name>:
```

**Never pass `-e`.** tmux stores the rendered screen, so the default output is already
plain text; `-e` *reconstructs* ANSI escapes from cell attributes and would hand raw
control bytes to the API and eventually to a browser ([research D5](../research.md)).

The result still passes through a defensive stripper for residual C0 control characters.
Golden-file tests in `testdata/` cover it.

---

## `Kill` and `Has` — teardown, verified

```
tmux kill-session -t =<name>
tmux has-session  -t =<name>        # exit 0 = present, exit 1 = gone
```

`Kill` alone is not teardown. The manager calls `Kill`, then `Has`, and reports success
only when `Has` returns false (FR-019). A surviving session yields `409` to the caller
and a prominent audit record — an orphaned tmux session is a live unsandboxed shell with
no owner.

`Has` must distinguish "gone" (exit 1) from "tmux itself failed" (binary missing, no
server, exec error). Collapsing a tmux failure into "gone" would report a successful
teardown that never happened. Exit code 1 with `can't find session` on stderr is the only
thing that means gone.

---

## `List` — one call for reconciliation

```
tmux list-sessions -F '#{session_name}|#{session_created}|#{@crswd-managed}'
```

Yields everything adoption needs in a single exec — name, creation time, and provenance:

```
crswd-abc123|1785706480|1
crswd-abc123-decoy|1785706480|
notours|1785706480|
```

`#{session_created}` is Unix epoch seconds and is the origin of the absolute deadline for
an adopted session (FR-024, SC-009). An empty third field means we did not create it, so
it is neither adopted nor touched (FR-022).

**No server running** is not an error: `list-sessions` exits non-zero with
`no server running on ...`. That is the normal first-boot case and must return an empty
slice, not a startup failure.

---

## The fake

`fake.go` implements `Controller` in memory for every other package's tests (FR-039's
sibling requirement — no real tmux in unit tests). It must be able to simulate:

| Scenario | Why |
|---|---|
| `Kill` succeeds but `Has` still reports present | The `409` path (FR-019) |
| A session that vanished on its own | `dead` transition |
| `List` returning a managed session, an unmanaged lookalike, and an unrelated one | Adoption discrimination (FR-022) |
| `List` returning a session created 25 hours ago | Destroy-not-adopt (FR-025) |
| tmux binary missing / exec error | Distinguishing failure from absence |
| Recording exact argv per call | Asserting no shell string is ever built (FR-029) |

That last one is the point: the fake records the argv slice it was given, so a test can
assert the daemon never constructed a shell string — turning FR-029 from a review comment
into an assertion.

---

## Integration tests

Real-tmux tests live behind `//go:build tmux` and run against an isolated socket
(`tmux -L crswd-test`) so they never touch the operator's own tmux server, with a
`t.Cleanup` that kills the test server. They are excluded from `go test ./...` by default
and are the only place a real tmux binary is required.
