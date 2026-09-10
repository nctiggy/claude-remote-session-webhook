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

The exit sentinel goes in at the end and never before it. `ralph/PROMPT.md`'s
Completion section says what it is and `ralph/loop.sh` greps for it as a whole
line — so this file must not carry it until milestone 17 is finished and green.

Milestone 16's notebook, plan and prompt are in `ralph/archive/2026-09-10/`.

---

## Iteration 0 — 2026-09-10 — the milestone, investigated before it was planned

**Did:** Read the repository rather than the ticket. Established the baseline for
milestone 17 — lifting PR #154's two pane-level 401-wait acceptance cases onto
`main` — and wrote `ralph/IMPLEMENTATION_PLAN.md`, `ralph/VALIDATION_CONTRACT.md`
and `ralph/PROMPT.md` against what was measured.

**Learned — the four facts worth an iteration each:**

- **`go build` fails in this worktree.** `error obtaining VCS status: exit status
  128`, from the Go toolchain's VCS stamping. It is not the sandbox — it fails with
  the sandbox off. Consequence: bare `go test ./...` fails on `internal/release`,
  and the *entire* `-tags quickstart` suite fails at its first line, both on
  unmodified `main` with nothing applied. `GOFLAGS=-buildvcs=false` in front of
  every Go command makes all of it green. Two suites shell out to `go build` from
  inside a test, which is why a flag on the outer command is not enough.

- **The baseline is green with that flag.** Measured today, on this host:
  `GOFLAGS=-buildvcs=false go test ./...` exit 0;
  `GOFLAGS=-buildvcs=false go test -tags quickstart ./cmd/crswd -count=1` exit 0 in
  36.7s; `golangci-lint run` → `0 issues.`; `go vet -tags quickstart ./cmd/crswd`
  exit 0. `tmux`, `jq` and `127.0.0.1:8765` are all available here, so the
  acceptance suite really does run rather than skip.

- **The lift is not verbatim, and the reason is a design decision, not a
  conflict.** Branch `feat/lm-95-oauth-401-wait-default` (PR #154, closed) changed
  `internal/config/sessionenv.go` so a supplied default's name joins the base set —
  an operator overrides it by setting it in the daemon's environment *alone*. That
  half never merged. On `main`, per `README.md` and `sessionenv.go`'s own comment,
  the override needs the name in `CRSW_SESSION_ENVIRONMENT` **and** the value in the
  daemon's environment. So `TestQuickstart401WaitYieldsToOperator` lifted verbatim
  fails on `main`: the `0` is filtered out on the way in and the default is appended
  over it. The plan adds `CRSW_SESSION_ENVIRONMENT` to that case rather than
  reopening #153's design.

- **Neither test name exists on `main`.** `grep -rn "401Wait\|shimEnv" cmd/crswd/`
  is empty, and `cmd/crswd/quickstart_test.go` ends at
  `TestSessionCarriesWhatRevivalNeeds` (2412 lines) — the same anchor the branch
  appended to, so the three insertion points in the plan are still where it says.

**Left:** all four tasks in `ralph/IMPLEMENTATION_PLAN.md`.

**Findings — noticed, not fixed:**

- **The buildvcs failure is a repo-wide papercut, not this milestone's.** Anything
  driven from a git worktree — this loop, any parallel session — hits it, and the
  two suites that shell out to `go build` fail in a way that reads like a broken
  test rather than a broken environment. Worth a real fix (`-buildvcs=false` in the
  two harnesses' own `exec.Command`, or a documented `GOFLAGS` line in `AGENTS.md`)
  in the fix lane, by someone who has checked what CI does — CI runs on a normal
  checkout and is unaffected, which is exactly why this has survived.

- **`ralph/archive/2026-09-10/` is untracked.** `loop.sh` refuses to start on a
  dirty tree, so milestone 16's archived plan, prompt and notebook must be committed
  before the loop is dispatched. Not done here: this pass was told to write four
  files and nothing else.

- **The stand-in every acceptance case shares gains a line of output.** Task 1
  changes `writeShim`, which `cmd/crswd/quickstart_dashboard_test.go` also uses.
  Nothing there looked positional on a read, and the suite is green today, so the
  expectation is that it stays untouched — but "the new cases pass" is not the claim
  to check, and the plan's last task says so.

- **`feat/lm-95-oauth-401-wait-default` is the only copy of this code.** PR #154 is
  closed; the branch is local and on `origin`. It must survive until this milestone
  has merged. There is also a sibling branch `feat/lm-95-oauth-401-wait` — the
  earlier attempt — which nothing here reads.

- **The readiness gate reads a validation assertion as ONE line, not one bullet.**
  The first draft of `ralph/VALIDATION_CONTRACT.md` wrapped each assertion over
  several lines with the `go test` command on the second or third; the gate
  rejected three of the four as `unverifiable-contract`, quoting each bullet's first
  physical line truncated at the wrap. Rewritten so every assertion is a single long
  line carrying its own backticked command. Same trap applies to plan tasks — keep
  the command on the `- [ ]` line itself. Nothing else changed; the four tasks and
  the blast radius are as they were.
