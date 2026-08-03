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
`RALPH_COMPLETE` — `loop.sh` stops on it.

---

## Iteration 0 — template initialized
**Did:** Scaffolded the repo. No project work yet.
**Learned:** n/a
**Left:** Everything in `ralph/IMPLEMENTATION_PLAN.md`.
**Findings:** none

---

## Iteration 1 — 2026-08-03 01:25

**Did:** Attempted T001. Wrote `go.mod` (module
`github.com/nctiggy/claude-remote-session-webhook`, `go 1.23.0`) and
`cmd/crswd/main.go`. **Left them uncommitted — T001 is NOT ticked** because the
verification commands could not be run at all (see below). Do not treat T001 as
done; the next iteration should re-verify and commit it.

**Left:** T001 (pending verification), then T002–T042.

### NEEDS CLARIFICATION — the loop cannot run its own verification commands

**This blocks every task in the plan, not just T001.** An operator has to resolve
it before another iteration is worth spending.

`ralph/loop.sh` invokes:

```
claude -p "$(cat ralph/PROMPT.md)" --permission-mode acceptEdits --settings .claude/settings.json
```

`.claude/settings.json` carries a `hooks` block but **no `permissions` block**, and
`acceptEdits` auto-approves Write/Edit only — never Bash. So in an autonomous
iteration every one of these is refused:

- `go build ./...`, `go vet ./...`, `go test ./...` (PROMPT step 6)
- `golangci-lint run` (PROMPT step 6)
- `git add` / `git commit` (PROMPT step 7)

Read-only basics (`ls`, `grep`, `git status`, `date`) are allowed by the default
safe list, which is why this looks like it works right up until the first build.

The consequence is worse than a stall: `loop.sh` sweeps the dirty tree into a
`ralph: iteration N (sweep uncommitted changes)` commit with `--no-verify`, so
**unverified, gitleaks-unscanned work lands in git anyway**. Every task in this
plan ends with "must be green", and none of them can be.

Editing `.claude/settings.json` is itself refused from inside an iteration, so the
loop cannot repair this on its own. **An operator must apply it by hand.** Proposed
patch — narrow, command-scoped, adds no wildcard, and leaves `danger-guard.sh`
fully in force since PreToolUse hooks fire regardless of the allowlist:

```jsonc
// .claude/settings.json — add as a sibling of "hooks"
"permissions": {
  "allow": [
    "Bash(go mod:*)", "Bash(go build:*)", "Bash(go test:*)", "Bash(go vet:*)",
    "Bash(go fmt:*)", "Bash(go run:*)", "Bash(go env:*)", "Bash(go version)",
    "Bash(gofmt:*)", "Bash(goimports:*)", "Bash(golangci-lint:*)",
    "Bash(git add:*)", "Bash(git commit:*)", "Bash(git status:*)",
    "Bash(git diff:*)", "Bash(git log:*)", "Bash(git show:*)",
    "Bash(git branch:*)", "Bash(git switch:*)", "Bash(git restore:*)"
  ]
}
```

`git push` is deliberately absent — the hard rule is branch-and-PR, never push.

It cannot go in `.claude/settings.local.json`: that file is gitignored and
`loop.sh` passes the checked-in `settings.json` explicitly, so a local override
would not reach the autonomous run.

**Learned (do not rediscover):**

- T001's "parses flags" has no flags behind it. Config is environment-only
  (`CRSW_*`, T007); neither `spec.md`, `plan.md`, nor `quickstart.md` defines a
  single CLI flag. `main.go` therefore calls bare `flag.Parse()` and nothing else
  — that satisfies "parses flags and exits cleanly with no other logic" without
  inventing a flag. Later wiring arrives in T032 (adopt) and T037 (shutdown).
- `plan.md:120` describes `cmd/crswd/main.go` as "flags, wiring, signal handling,
  exit codes. No logic." — confirming main.go stays a shell until T032/T037.

**Findings (noticed, not fixed):**

1. **`loop.sh`'s sweep commit uses `--no-verify`**, bypassing the gitleaks
   pre-commit hook that `AGENTS.md` lists as an enforced guardrail. The sweep
   exists to catch an agent that forgot to commit, but it also silently commits
   work that failed verification. Worth a guard: refuse to sweep, or sweep to a
   scratch branch, rather than committing unverified changes to the working
   branch.
2. **`golangci-lint` and `goimports` are not installed on this host** (quickstart
   line 17 already flags golangci-lint). `.claude/hooks/format-and-lint.sh`
   no-ops silently when a tool is missing, so formatting is currently unenforced.
   T002 owns installing them — but T002 needs Bash, so it is blocked by the same
   issue above.
3. No `go.sum` was created, as required. Nothing was imported beyond stdlib.

---

## Iteration 2 — 2026-08-03

**Did:** Closed out **T001**. No new code — iteration 1's `go.mod` and
`cmd/crswd/main.go` were committed by an operator in `902c249`; this iteration ran
the verification iteration 1 could not, confirmed all four gates green, and ticked
T001 in both `ralph/IMPLEMENTATION_PLAN.md` and
`specs/001-crswd-daemon-core/tasks.md`. Also deleted the stale ⛔ BLOCKED banner at
the top of the plan, which was still telling every fresh context that no task could
run.

T001 acceptance, actually executed rather than asserted:

```
go build ./...      OK
go vet ./...        OK
go test ./...       OK (cmd/crswd: no test files)
golangci-lint run   OK (clean, no config yet — T002 owns .golangci.yml)
gofmt -l .          empty
go.sum              absent  ✅ zero third-party deps still holds
```

**Learned (do not rediscover):**

- **The Bash block from iteration 1 is gone.** `.claude/settings.json` now carries
  the `permissions.allow` list proposed in that entry. Build, vet, test, lint,
  `git add`, and `git commit` all run from inside an autonomous iteration. Do not
  re-raise this; do not re-propose the patch.
- **Iteration 1's finding #2 is now stale — the tooling IS installed:**
  `golangci-lint` at `/home/nctiggy/bin/golangci-lint`, `goimports` at
  `/usr/bin/goimports`. So `.claude/hooks/format-and-lint.sh` no longer silently
  no-ops.
- **`golangci-lint` is v1.62.2** — exactly the version T002 pins, so T002's
  "**v1 config schema**" instruction is correct as written and needs no adjusting.
  A v2-schema `.golangci.yml` would fail against this binary.
- The host toolchain is **go1.24.0** while `go.mod` declares `go 1.23.0`. That
  mismatch is deliberate and correct (1.23.0 is the *minimum language version*,
  1.24 is the toolchain). T001 and T013 both warn against "fixing" it — T013
  depends on it, since `crypto/rand.Text` would require a 1.24 directive.
- `golangci-lint run` with **no** `.golangci.yml` still exits clean today, so it
  proves nothing yet. T002 must confirm its config is actually loaded, not just
  that the command exits 0.

**Left:** T002 is next (`.golangci.yml`, v1 schema, `errcheck`/`gosec`/`govet`/
`staticcheck`/`bodyclose`), then T003–T042. Its "install the missing local
tooling" half is already satisfied — only the config file remains.

**Findings (noticed, not fixed):**

1. **Iteration 1's finding #1 still stands and is still unfixed:** `loop.sh`'s
   sweep commit uses `--no-verify`, bypassing the gitleaks pre-commit hook that
   `AGENTS.md` lists as an enforced guardrail. Now that Bash works the sweep should
   rarely fire, which makes it *more* dangerous, not less — it is now the quiet
   path that only opens when an iteration has already gone wrong. Worth a guard:
   refuse to sweep, or sweep to a scratch branch.
2. **`ralph/IMPLEMENTATION_PLAN.md` and `specs/.../tasks.md` carry duplicate
   checkbox state** and must now be ticked in lockstep by hand. `PROMPT.md` step 9
   only names the plan file, so a future iteration will likely tick one and not the
   other. Either derive the plan's list from `tasks.md` or make step 9 name both.
3. ~~`git config core.hooksPath` unverified.~~ **Resolved during this iteration:**
   this commit tripped the hook (`gitleaks … 1 commits scanned … no leaks found`),
   which proves `core.hooksPath` is already pointed at `.githooks` on this host.
   No action needed — and this is the reason finding #1 matters, since `loop.sh`'s
   `--no-verify` sweep is now the only path that skips a hook known to be live.

---

## Iteration 3 — 2026-08-03 01:38

**Did:** Completed **T002**. Added `.golangci.yml` (v1 schema) enabling the five
required linters — `errcheck`, `gosec`, `govet`, `staticcheck`, `bodyclose` — plus
v1's own defaults (`gosimple`, `ineffassign`, `unused`) named explicitly, so
`disable-all: true` cannot leave a weaker gate than an unconfigured run. Ticked T002
in **both** `ralph/IMPLEMENTATION_PLAN.md` and `specs/.../tasks.md`. The task's
"install the tooling" half was already satisfied by an operator (iteration 2).

Gate, executed not asserted:

```
go build ./...      OK
go vet ./...        OK
go test ./...       OK (cmd/crswd: no test files)
golangci-lint run   OK
gofmt -l .          empty
go.sum              absent  ✅ zero third-party deps still holds
```

**Learned (do not rediscover):**

- **Iteration 2 was right that a clean `golangci-lint run` proves nothing**, so this
  iteration did not take it on faith. Verified three ways: `golangci-lint config path`
  now prints `.golangci.yml` (it printed `level=warning msg="No config file detected"`
  beforehand); `golangci-lint linters` lists exactly the eight enabled; and a
  throwaway probe file was linted to confirm findings are actually *reported*, then
  deleted. The tree is clean — the probe was never committed. **Reuse this trick**
  whenever a task's deliverable is a config rather than code: a passing linter and an
  absent linter look identical from the exit code.
- **`exclude-use-default: false` is load-bearing and measured.** Probed both ways on
  identical source. Default exclusions ON reports 1 finding; OFF reports 3. The two
  that the defaults would have silently eaten:
  - `defer f.Close()` unchecked (errcheck EXC0001) — the exact shape by which
    "teardown is verified" (Constitution VI) decays into teardown assumed. **T028
    and T036 depend on this being visible.**
  - `G304: Potential file inclusion via variable` (gosec EXC0010) — which is
    literally T015's subject matter (`workdir.go`, path containment).
- **gosec `G204` is a precise alarm here, and better than expected.** Probed all
  three call shapes. It fires on `exec.Command("sh", "-c", "echo "+user)` — a
  constructed shell string, the thing `docs/security.md` bans outright. It does
  **not** fire on `exec.Command(name, args...)` (argv slice) or on
  `exec.Command("tmux", "send-keys", user)` (constant argv0, variable arg). So
  **T005's sanctioned argv-slice style will not need a `//nolint`** — do not add one
  pre-emptively, and if G204 ever does fire in `internal/tmuxctl/exec.go`, that is a
  real defect, not lint noise. (It fires regardless of `exclude-use-default`; the
  earlier assumption that the defaults muted it was wrong and is corrected here.)
- `errcheck` is set to `check-blank: true` + `check-type-assertions: true`, so
  `_ = someErr()` and `s := i.(string)` both fail the build. Write `if err := …`.
- `max-issues-per-linter: 0` / `max-same-issues: 0` — reports are uncapped. A
  truncated lint report reads as "clean" when it is not.

**Left:** T003 is next (`internal/tmuxctl`: `Controller` interface + `SessionInfo` in
`controller.go`, and the two target helpers in `target.go` — `SessionTarget` → `=name`,
`PaneTarget` → `=name:`, which are **not** interchangeable). Then T004–T042. T003–T008
are the Foundational phase and block every user story.

**Findings (noticed, not fixed):**

1. **`.golangci.yml` sets no `build-tags`, so T005's `//go:build tmux` integration
   test will never be linted** — not by `golangci-lint run` locally and not by CI.
   Deliberately left alone here rather than pre-solving a task I was not on (Principle
   IV), but **T005 should add `run: build-tags: [tmux]`** as part of its own change,
   or ship a file that no linter ever reads. Same trap applies to any future build tag.
2. **Iteration 1 finding #1 / iteration 2 finding #1 still stands, still unfixed:**
   `loop.sh`'s sweep commit uses `--no-verify`, bypassing the gitleaks pre-commit hook
   that iteration 2 proved is live on this host. Third iteration carrying it. It needs
   an operator or a task of its own — no iteration will ever pick it up, because it is
   not in the plan.
3. **Iteration 2 finding #2 still stands:** `ralph/IMPLEMENTATION_PLAN.md` and
   `specs/.../tasks.md` carry duplicate checkbox state, and `PROMPT.md` step 9 names
   only the plan. This iteration ticked both by hand *because the finding was written
   down* — which is the notebook working, but it is one fresh context away from
   breaking. Worth fixing at the source.
4. `.editorconfig` and the format-and-lint hook run `gofmt`/`goimports` on Go files
   but nothing formats YAML. `.golangci.yml` was hand-formatted; the hook's
   `npx --no-install prettier` path no-ops without a `node_modules`, which this repo
   deliberately does not have. Not a problem, just not the safety net it looks like.

---

## Iteration 4 — 2026-08-03 01:52

**Did:** Completed **T003**. Created the `internal/tmuxctl` package: the `Controller`
interface and `SessionInfo` in `controller.go` (signatures copied exactly from
`specs/001-crswd-daemon-core/contracts/tmuxctl.md`, not paraphrased), the two target
helpers in `target.go`, and `target_test.go`. Ticked T003 in **both**
`ralph/IMPLEMENTATION_PLAN.md` and `specs/.../tasks.md`. First code under `internal/`.

Gate, executed not asserted:

```
go build ./...      OK
go vet ./...        OK
go test ./...       OK (internal/tmuxctl 4 tests pass; cmd/crswd: no test files)
golangci-lint run   OK
gofmt -l .          empty
go.sum              absent  ✅ zero third-party deps still holds
```

**Learned (do not rediscover):**

- **Iteration 3's config-probe trick generalises to code, and it caught a real bug
  here.** The tests were mutation-probed by editing `PaneTarget` to return `"=" + name`
  and re-running: 4 assertions failed, then the edit was reverted and the gate re-run.
  Doing this *before* committing is what surfaced a wrong `wantPane` in the lookalike
  table row — a passing test suite and a test suite that asserts nothing look identical
  from the exit code. **`sed`/`cp` are not in the permissions allowlist, so do the
  mutation with the Edit tool, not Bash.** (Only `go`/`git`/`gofmt`/`goimports`/
  `golangci-lint` are allowed; a compound Bash command is refused if *any* part of it
  is outside the list.)
- **The contract file is the source of truth for signatures, and it disagrees with the
  task text in ordering only.** `tasks.md` lists the methods as `New, SetOption,
  SendKeys, Paste, CapturePane, Kill, Has, List`; `contracts/tmuxctl.md` declares them
  `New, SendKeys, Paste, CapturePane, Kill, Has, SetOption, List`. Same eight methods,
  same signatures — order is not semantic in a Go interface. Resolved by taking the
  contract's exact signatures and the task's reading order. Nothing to clarify.
- **The package holds no `Target()` and must not grow one.** Two exported helpers is a
  deliberate API choice from the contract, so a caller physically cannot pass the wrong
  target kind. A future "simplification" that merges them re-opens `can't find pane`.
- **Test package is `tmuxctl_test` (external).** Nothing here needs unexported access,
  and it keeps T004's fake honest about what the rest of the daemon can actually reach.
- T003 deliberately ships **no implementation** — no `fake.go` (T004), no `exec.go`
  (T005), no `ansi.go` (T006). An interface with no implementer still builds, vets, and
  lints clean, so the phase splits exactly as the plan assumes.

**Left:** T004 is next (`internal/tmuxctl/fake.go` + `fake_test.go`: in-memory
`Controller` recording exact argv, simulating kill-succeeds-but-`Has`-still-present, a
self-vanished session, a `List` with managed + unmanaged-lookalike + unrelated, a
25-hour-old session, and exec failure distinct from absence). The lookalike name
`crswd-<id>-decoy` is already used in `target_test.go` — reuse it so the adoption tests
and the target tests describe the same trap. Then T005–T042.

**Findings (noticed, not fixed):**

1. **Iteration 3 finding #1 still stands and is now one task away from biting:**
   `.golangci.yml` sets no `build-tags`, so T005's `//go:build tmux` file in
   `exec_tmux_test.go` will be invisible to `golangci-lint run` locally *and* in CI.
   T005 must add `run: build-tags: [tmux]` as part of its own change.
2. **Iteration 1 #1 / 2 #1 / 3 #2 still stands, still unfixed, fourth iteration
   carrying it:** `loop.sh`'s sweep commit uses `--no-verify`, bypassing the gitleaks
   pre-commit hook proven live in iteration 2. No iteration will ever fix it — it is not
   in the plan, and Principle IV says do not wander outside the task. It needs an
   operator or a task of its own.
3. **Iteration 2 #2 / 3 #3 still stands:** duplicate checkbox state in
   `IMPLEMENTATION_PLAN.md` and `specs/.../tasks.md`, with `PROMPT.md` step 9 naming
   only the plan. Ticked both by hand again, again only because the finding was written
   down. Three iterations of manual compensation for a one-line fix to step 9.

---

## Iteration 5 — 2026-08-03 01:50

**Did:** Completed **T004**. Added `internal/tmuxctl/fake.go` (in-memory `Controller`,
recording the full argv of every call plus the stdin that rode with it) and
`fake_test.go` (30 tests). Ticked T004 in **both** `ralph/IMPLEMENTATION_PLAN.md` and
`specs/.../tasks.md`. All six scenarios the contract requires are reproducible:
`SurviveKill` (the 409 path), `Vanish` (died on its own), `Seed` (managed + unmanaged
lookalike + unrelated + a 25-hour-old survivor), `FailOp` (tmux itself failing, kept
distinct from absence), and argv recording.

Gate, executed not asserted:

```
go build ./...            OK
go vet ./...              OK
go test -count=1 ./...    OK (internal/tmuxctl 30 tests; cmd/crswd: no test files)
go test -race ./...       OK
golangci-lint run         OK
gofmt -l .                empty
go.sum                    absent  ✅ zero third-party deps still holds
```

**Learned (do not rediscover):**

- **The argv builders are package-level (`argvNew`, `argvSendKeys`, `argvCapturePane`,
  …), not private to the fake — deliberately, and T005 should USE them rather than
  write its own.** `fake.go` and `exec.go` are the same package, so `exec.go` can call
  them directly: `exec.CommandContext(ctx, argv[0], argv[1:]...)`. Each returns a fresh
  slice with `"tmux"` at index 0. A fake whose argv agrees with the contract but
  disagrees with the real controller is worse than no fake, and this is the only
  structural defence against that drift. If T005 duplicates the strings instead, every
  argv assertion written against the fake between now and then becomes decorative.
- **`SendKeys` argv is `send-keys -t =name: -- <keys...>` with NO `-l`, and that is a
  reasoned decision, not an oversight.** `contracts/tmuxctl.md` shows two forms — `-l --`
  for the claude constant and a bare `Enter` — which a single variadic method cannot both
  produce. `-l` would send `Enter` as five literal characters instead of the Enter key;
  omitting it still delivers the claude constant literally, because tmux sends any
  argument that is not a known key name as a literal string. So the uniform no-`-l` shape
  is the only one correct for both. `--` is kept as a guard for a key name beginning with
  `-`. **T005's real-tmux integration test is what settles this for good** — if it
  disagrees, fix the shared builder and both sides move together.
- **Mutation-probing (iteration 4's trick) again earned its keep.** Four mutations were
  injected and each was caught by the intended test: `-e` added to `capture-pane`
  (2 tests), `Kill` ignoring `SurviveKill` (1), `Has` swallowing the exec error into
  `false, nil` (1), and the Paste payload appended to argv (2, incl. all 5 hostile
  payloads). Then reverted and the gate re-run. Do this *before* committing, not after.
- **`Paste` records TWO calls**, matching the two tmux commands: `load-buffer -b <name> -`
  carrying the payload on `Stdin`, then `paste-buffer -d -b <name> -t =<name>:`. The
  buffer is named for the session, so two sessions pasting at once cannot read each
  other's text. `Calls()` deep-copies, so a test cannot corrupt the record it just read.
- **`List` sorts by name** so tests are not at the mercy of map iteration order. Do not
  read that as tmux guaranteeing the same ordering — it is a determinism aid, nothing more.
  Note `crswd-9f2c…` sorts *before* `crswd-expired` (`9` < `e`); an expected-order table
  got this wrong on the first run.
- **`Managed` comes from `@crswd-managed` being non-empty, never from the name.** `New`
  alone does not set it — the manager sets it explicitly in T018. That is what makes the
  `-decoy` lookalike a provenance test rather than a string test.
- `SetNow(func() time.Time)` injects the clock that stamps `Created` on sessions made
  through `New`, so T031/T036 can age a session without waiting for one. `Seed` takes
  `Created` directly for survivors.

**Left:** T005 is next (`internal/tmuxctl/exec.go`, the real controller — and it must add
`run: build-tags: [tmux]` to `.golangci.yml`, see finding 1). Then T006–T042.

**Findings (noticed, not fixed):**

1. **Iteration 3 #1 / 4 #1 — now due.** `.golangci.yml` sets no `build-tags`, so T005's
   `//go:build tmux` file in `exec_tmux_test.go` will be invisible to `golangci-lint run`
   locally *and* in CI. **T005 must add `run: build-tags: [tmux]` as part of its own
   change**, or ship a file no linter ever reads.
2. **Iteration 1 #1 / 2 #1 / 3 #2 / 4 #2 still stands, fifth iteration carrying it:**
   `loop.sh`'s sweep commit uses `--no-verify`, bypassing the gitleaks pre-commit hook
   (which fired correctly on this iteration's commit — `1 commits scanned … no leaks
   found`). No iteration will ever fix it: it is not in the plan and Principle IV forbids
   wandering. It needs an operator or a task of its own.
3. **Iteration 2 #2 / 3 #3 / 4 #3 still stands:** duplicate checkbox state in
   `IMPLEMENTATION_PLAN.md` and `specs/.../tasks.md`, `PROMPT.md` step 9 naming only the
   plan. Ticked both by hand again, again only because the finding was written down.
   Fourth iteration of manual compensation for a one-line fix to step 9.
4. **New this iteration:** `fake.go` compiles into the production package, not just into
   tests, because other packages' `_test.go` files must import it (the `httptest` pattern).
   It is unreachable from `cmd/crswd` and adds no dependency, but it does mean the fake's
   knobs — `SurviveKill`, `FailOp`, `Vanish` — are exported API on a package the daemon
   links. Nothing calls them outside tests today. Worth a lint rule or a build-tag split
   if that ever stops being true.

---

## Iteration 6 — 2026-08-03 02:07

**Did:** Completed **T005**. Added `internal/tmuxctl/exec.go` (the real `Controller`,
built on the argv builders `fake.go` already exports so the two cannot drift),
`exec_test.go` (36 tests, no tmux required), and `exec_tmux_test.go` (8 real-tmux
tests behind `//go:build tmux`). Also added `run: build-tags: [tmux]` to
`.golangci.yml`, which T005 owed since iteration 3. Ticked T005 in **both**
`ralph/IMPLEMENTATION_PLAN.md` and `specs/.../tasks.md`.

Gate, executed not asserted:

```
go build ./...              OK
go vet ./...                OK
go vet -tags tmux ./...     OK   ← plain `go vet` does NOT typecheck the tagged file
go test -count=1 ./...      OK (internal/tmuxctl 66 tests)
go test -race ./...         OK
go test -tags tmux ./...    OK (8 integration tests, real tmux, isolated sockets)
golangci-lint run           OK
gofmt -l .                  empty
go.sum                      absent  ✅ zero third-party deps still holds
```

**Learned (do not rediscover):**

- **The integration test found a real bug, and it was worth the whole task.** tmux has
  **two** "there is no server" messages, not one. `contracts/tmuxctl.md` and
  `research.md` only ever saw the first, because the research host had run tmux before:
  - socket file exists, nothing listening → `no server running on <path>`
  - socket file absent entirely → `error connecting to <path> (No such file or directory)`

  A machine that has never started tmux has no `/tmp/tmux-<uid>/` at all, so it gives
  the **second**. Matching only the first — which is what the contract says — meant
  `List` would return an error on a fresh host, and T032 makes a tmux failure at startup
  fatal. **The daemon would have refused to boot on exactly the clean machine it is
  meant to be deployed to.** `noServer()` now matches both. It deliberately does *not*
  match `error connecting to … (Permission denied)`: same prefix, but there the socket
  exists and a server with live sessions may be behind it, and calling that "empty"
  would walk adoption past every session it was supposed to reclaim.
- **Killing the last session takes the tmux server down with it, so the `Has` that was
  meant to confirm the teardown cannot answer.** `Has` refuses to read "no server" as
  "gone" (contract, and it is the right call), so `Kill` + `Has` on the *only* live
  session returns an **error**, every time. **T028 must handle this or verified destroy
  will return 409 on every successful teardown of the last session.** The way out is
  already proven in `TestTmuxKillingTheLastSessionStopsTheServer`: `List` treats no
  server as the empty slice, so "not in `List`" confirms the teardown. Do not "fix"
  this by loosening `Has`.
- **`go vet ./...` silently skips `//go:build tmux` files.** So does `go test ./...`.
  Only `golangci-lint` sees them now, and only because of the `build-tags` addition —
  which was verified by observing it report findings *inside* `exec_tmux_test.go`, not
  by trusting the config. Run `go vet -tags tmux ./...` too; it is not in `AGENTS.md`.
- **Iteration 3's gosec G204 note was too optimistic and is corrected here.** G204 *does*
  fire on `exec.CommandContext(ctx, argv[0], args...)` — a variable in the program slot
  or the argument slot is enough; only all-literal calls stay quiet. It is unavoidable
  in this package by construction, so `run()` carries a `//nolint:gosec` with the
  reasoning. **This does not weaken the FR-029 guarantee**, which is now an assertion
  rather than a comment: `TestExecSendsTheContractArgv` records the argv the child
  process actually received, and `TestExecPasteKeepsCallerTextOffTheCommandLine` proves
  every hostile payload rides on stdin and appears in no argument.
- **Unit tests drive the real exec path with no tmux anywhere**, by symlinking the test
  binary into a temp dir as `tmux` and putting it at the front of `PATH`; `TestMain`
  notices an env var and acts as the stub, recording argv + stdin as JSON lines and
  reproducing a chosen exit status, stdout, and stderr. That is what makes exit-status
  and stderr discrimination testable **in CI**, where no tmux exists. A shell-script
  stub would have needed a `0o755` write (gosec G306) and a `#!/bin/sh` in the one repo
  whose whole point is that no shell is involved. **Reuse this for any future exec.**
  Note it forces `t.Setenv`, so those tests cannot call `t.Parallel()`.
- **Give each integration test its own `-L` socket.** A shared one let one test's
  `kill-server` race the next test's `new-session` (`server exited unexpectedly`), and
  leaked sessions between `List` assertions. `socketFor(t.Name())` sanitises the name
  into a filename. The `-L` isolation is also why `Exec` has a `socket` field at all:
  `TMUX_TMPDIR` would isolate the tests too, right up until it silently did not, and
  the cleanup here runs `kill-server`.
- **`parseSessions` splits rows from the right, not the left.** tmux permits `|` in a
  session name and `list-sessions` returns *every* session on the host, including the
  operator's own — so `weird|name|1785706480|` must parse as one session called
  `weird|name`. A left-to-right split reads the name as `weird`, fails on the creation
  time, and fails the whole call, taking adoption of every managed session with it.
- Real tmux confirmed the shape iteration 5 reasoned out: `send-keys` with **no `-l`**
  delivers `claude --dangerously-skip-permissions` literally *and* sends `Enter` as the
  Enter key. The question is settled; the builder is correct as written.
- Real tmux also confirmed D4 and D5 first-hand: all five hostile payloads (`;`,
  `foo;`, `foo;;`, `a; echo PWNED; $(id) \`whoami\``, and an embedded newline) arrive
  byte-for-byte through `load-buffer`/`paste-buffer`, and `capture-pane` without `-e`
  returns colour output with **zero** ESC bytes.
- Mutation-probing (iterations 4 and 5) again earned its keep — five mutations injected,
  each caught by the intended test: `Has` ignoring stderr (2 tests), left-to-right row
  parsing (2), the Paste payload moved into argv (7), `List` swallowing every error (4),
  and `noServer` accepting any `error connecting to` (1). Reverted, then the gate re-run.

**Left:** T006 is next (`internal/tmuxctl/ansi.go`, the defensive control-sequence
stripper, golden files in `testdata/`). Note T006 owns a decision T005 deliberately did
not make: `CapturePane` currently returns tmux's output **verbatim**. The contract says
"the result still passes through a defensive stripper", but `ansi.go` did not exist yet
and pre-empting it would have been wandering. T006 must decide whether `Exec.CapturePane`
calls the stripper or T025's handler does — and say which in its commit. Then T007–T042.

**Findings (noticed, not fixed):**

1. **T028 will report a false failure on the last session.** See "Learned" above: killing
   the only session stops the tmux server, and `Has` then errors rather than returning
   false. Pinned by a passing test so it cannot be rediscovered the hard way, but the fix
   belongs to T028, not here.
2. **A failed `paste-buffer` leaves the prompt sitting in a named tmux buffer.** `Paste`
   runs `load-buffer` then `paste-buffer -d`; `-d` deletes the buffer *as it pastes*, so
   if the paste fails the buffer survives with caller prompt text in it, readable by any
   tmux client until the next prompt for that session overwrites it. Prompts are secret
   under `docs/security.md` §3. Not fixed: the cleanup needs a `delete-buffer` argv
   builder, `fake.go` would have to record it too, and that is T004's file — the drift
   between fake and real is the exact thing this task was built to prevent. Small, bounded
   (same-uid, one session's own buffer), but real.
3. **`contracts/tmuxctl.md` is now wrong on one point** — it names only `no server
   running` for the empty-server case. The code is right and the doc is stale. Left alone
   under Principle IV; worth an operator fixing the contract so the next reader of the
   spec is not misled.
4. **Iteration 1 #1 / 2 #1 / 3 #2 / 4 #2 / 5 #2 still stands, sixth iteration carrying
   it:** `loop.sh`'s sweep commit uses `--no-verify`, bypassing the gitleaks pre-commit
   hook (which fired correctly on this iteration's commit — `1 commits scanned … no leaks
   found`). No iteration will ever fix it: not in the plan, and Principle IV forbids
   wandering. Needs an operator or a task of its own.
5. **Iteration 2 #2 / 3 #3 / 4 #3 / 5 #3 still stands:** duplicate checkbox state in
   `IMPLEMENTATION_PLAN.md` and `specs/.../tasks.md`, `PROMPT.md` step 9 naming only the
   plan. Ticked both by hand again, again only because the finding was written down.
   Fifth iteration of manual compensation for a one-line fix to step 9.
6. **`AGENTS.md`'s command table has no entry for the tagged tests.** `go test ./...` and
   `go vet ./...` both skip `//go:build tmux` files entirely, so the table's "Test (all)"
   is not all. A future iteration running only the documented commands will believe a
   broken integration test is green.

---

## Iteration 7 — 2026-08-03 02:20

**Did:** Completed **T006**. Added `internal/tmuxctl/ansi.go` (`Strip`, an ESC-sequence
state machine), `ansi_test.go` (18 tests), and 14 golden fixture pairs in
`internal/tmuxctl/testdata/`. Ticked T006 in **both** `ralph/IMPLEMENTATION_PLAN.md` and
`specs/.../tasks.md`.

Gate, executed not asserted:

```
go build ./...              OK
go vet ./...                OK
go vet -tags tmux ./...     OK
go test -count=1 ./...      OK (internal/tmuxctl 84 tests)
go test -race -count=1 ./...   OK
go test -tags tmux ./...    OK (8 integration tests, real tmux)
golangci-lint run           OK
gofmt -l . / goimports -l . empty
go.sum                      absent  ✅ zero third-party deps still holds
```

**The decision iteration 6 left to this task: `Strip` is NOT wired into
`Exec.CapturePane`.** T025's handler calls it, at the point output leaves the daemon.
Reasons, so T025 does not re-litigate it:

- FR-031's boundary is "before it leaves the daemon". The handler is the only place
  every read path converges, so stripping there holds for **every** `Controller` — the
  real one, the fake, and anything added later. Stripping inside `Exec` would cover one
  implementation.
- More concretely: T025's test runs against `fake.go`. If `Exec` stripped, the fake would
  not, and T025's "no ESC bytes in the response" assertion would either be testing the
  fake or be impossible to write honestly. The fake must be able to emit ESC bytes.
- `exec.go` already says so in a comment T005 wrote ("the defensive stripper is a
  separate second line of defence"), so this is consistent, not a reversal.

**T025 must call `tmuxctl.Strip` on the captured text. Nothing else does.**

**Learned (do not rediscover):**

- **The Write tool silently eats `\uXXXX` in file content.** Writing a fixture line
  containing the six characters backslash-u-0-0-9-b produced the *actual* U+009B in the
  file instead; `\\u009b` produced two literal backslashes. `\x1b`, `\n`, `\t`, and
  `\U0001F600` all pass through literally — only the four-hex-digit `\u` form is
  rewritten. It cost two wrong writes of `c1-runes.in` before `grep -H ''` showed it.
  **Express C1 runes as their UTF-8 bytes (`\xc2\x9b`), not as a backslash-u escape,** in any file
  written by tool rather than by shell.
- **Bash in this session refused `mkdir`, `printf`, and `od`** ("requires approval"),
  even though `grep`, `sed`, and `ls` ran. So raw-byte fixtures cannot be produced with
  `printf`. This is *why* the fixtures are Go-quoted literals decoded by
  `strconv.Unquote` — but that turned out to be the better format anyway: a `.golden`
  full of real ESC bytes shows a reviewer a blank space where the subject of the test
  should be, and `"\x1b]52;c;…"` is legible in a diff.
- **`os.DirFS("testdata")` + `fs.ReadFile` avoids gosec G304 with no `//nolint`.** G304
  matches `os.Open`/`os.ReadFile` with a non-constant path, which is un-muted in this
  repo (`exclude-use-default: false`). An `fs.FS` is not in its rule set *and* it refuses
  to escape its root. **Reuse this for any future golden-file test.**
- **The golden table carries an `unchanged bool` per case, and it is load-bearing.** For
  every case not marked unchanged, the test asserts `input != golden` *before* comparing
  — otherwise a fixture with nothing to strip passes against a `Strip` that returns its
  argument. Two cases (`plain-text`, `utf8-preserved`) are marked unchanged and assert
  the opposite. `TestGoldenFixturesAndCasesAgree` then checks the `*.in` files on disk
  against the table, so a fixture added without a case is not a test nobody runs.
- **Mutation-probing (iterations 4–6) earned its keep for a fourth time.** Five mutations,
  each caught by the intended test and no others: OSC/DCS/APC introducers treated as
  two-byte escapes (6 golden + 3 edge cases), the C1 range narrowed to 0x8F (1 golden +
  the property test), invalid UTF-8 emitted rather than dropped (1 + property),
  `needsStrip` inverted (23 tests), and charset intermediates not consumed (1 golden —
  `escape-two-byte` is the *only* case covering `ESC ( B`, so without it that branch is
  untested). Reverted, then the gate re-run.
- **Two parser choices are deliberate and are pinned by tests, not left to chance:**
  an unterminated sequence consumes the rest of the input (a malformed sequence must
  never out-emit a well-formed one), and a C0 byte inside a sequence *aborts* it — a real
  terminal would execute the C0 and finish the sequence, so `\x1b[3\x07m` yields a visible
  `m` here. That difference can only ever produce extra inert characters, never a
  surviving escape.
- **Only the 7-bit forms are parsed.** A raw 0x80–0x9F introducer cannot occur in valid
  UTF-8, so it is dropped as an invalid byte rather than treated as CSI; the parameters
  that followed survive as inert text. C1 arriving as a *decoded rune* (`\xc2\x9b`) is a
  separate case and is dropped explicitly — valid-UTF-8 filtering alone would pass it.

**Left:** T007 is next (`internal/config/config.go`: `CRSW_SHARED_SECRET` required and
fatal under 32 bytes, `CRSW_ALLOWED_ROOTS` defaulting to `$HOME/code` with a loud warning
on every defaulted start, non-loopback `CRSW_LISTEN` fatal, plus `CRSW_MAX_SESSIONS`,
`CRSW_CREATE_RATE_PER_MIN`, `CRSW_MAX_BODY_BYTES` — see the Config table in
`data-model.md`). Then T008–T042. T007 and T008 finish the Foundational phase.

**Findings (noticed, not fixed):**

1. **Bidi and invisible Unicode are NOT stripped, by design, and nothing downstream knows
   it.** `Strip` removes terminal control sequences; it leaves U+202A–U+202E and
   U+2066–U+2069 (the Trojan Source overrides), U+200B–U+200D, and U+2028/U+2029. Those
   are inert in a terminal but reorder or hide text in a browser, and
   `docs/security.md` is explicit that everything a session prints reaches the dashboard.
   Out of scope here under Principle IV — this is a control-*sequence* stripper — but the
   milestone-2 dashboard needs to decide it. Note `html/template` escaping does not help:
   these are legitimate characters, not markup.
2. **Iteration 6 #2 still stands:** a failed `paste-buffer` leaves caller prompt text in a
   named tmux buffer, readable by any tmux client until the next prompt overwrites it.
   Prompts are secret under `docs/security.md` §3. Needs a `delete-buffer` argv builder in
   `fake.go` *and* `exec.go` together, which is why no single task owns it.
3. **Iteration 6 #1 still stands:** T028 will report a false failure on the last session —
   killing the only session stops the tmux server, and `Has` then errors rather than
   returning false. Pinned by a passing test; the fix belongs to T028. Use `List`, which
   treats no server as empty. Do not loosen `Has`.
4. **Iteration 6 #3 still stands:** `contracts/tmuxctl.md` names only `no server running`
   for the empty-server case and is stale — the code correctly matches both messages.
   Worth an operator fixing the contract.
5. **Iteration 1 #1 / 2 #1 / 3 #2 / 4 #2 / 5 #2 / 6 #4 still stands, seventh iteration
   carrying it:** `loop.sh`'s sweep commit uses `--no-verify`, bypassing the gitleaks
   pre-commit hook (which fired correctly on this iteration's commit — `1 commits
   scanned … no leaks found`). No iteration will ever fix it: not in the plan, and
   Principle IV forbids wandering. Needs an operator or a task of its own.
6. **Iteration 2 #2 / 3 #3 / 4 #3 / 5 #3 / 6 #5 still stands:** duplicate checkbox state in
   `IMPLEMENTATION_PLAN.md` and `specs/.../tasks.md`, `PROMPT.md` step 9 naming only the
   plan. Ticked both by hand again, again only because the finding was written down.
   Sixth iteration of manual compensation for a one-line fix to step 9.
7. **Iteration 6 #6 still stands:** `AGENTS.md`'s command table has no entry for
   `go test -tags tmux ./...` or `go vet -tags tmux ./...`, so "Test (all)" is not all.

---

## Iteration 8 — 2026-08-03 02:37

**Did:** Completed **T007**. Added `internal/config/config.go` (`Load()` plus the
injectable `LoadFrom(getenv, warn)`) and `config_test.go` (45 tests). Every value in
`data-model.md`'s Config table is read, validated, and fatal on anything weak; the
unset root list is the one non-fatal case and is loud. Ticked T007 in **both**
`ralph/IMPLEMENTATION_PLAN.md` and `specs/.../tasks.md`.

Gate, executed not asserted:

```
go build ./...              OK
go vet ./...                OK
go vet -tags tmux ./...     OK
go test -count=1 ./...      OK (internal/config 45 tests, internal/tmuxctl 84)
go test -race -count=1 ./...   OK
go test -tags tmux ./...    OK (8 integration tests, real tmux)
golangci-lint run           OK
gofmt -l . / goimports -l . empty
go.sum                      absent  ✅ zero third-party deps still holds
```

**Learned (do not rediscover):**

- **`Load()` is a two-line wrapper over `LoadFrom(getenv func(string) string, warn
  io.Writer)`, and T032 should call `Load()`.** The seam exists because the alternative
  is `t.Setenv`, which forbids `t.Parallel()` for the whole table — and because FR-004's
  warning has to be *asserted*, which means capturing it, which means it cannot be
  hard-wired to `os.Stderr`. Two tests still use `t.Setenv` (non-parallel, deliberately)
  purely to prove `Load()` is wired to `os.Getenv`; without them the delegation is
  untested. Same injection style as `tmuxctl`'s `SetNow`.
- **gosec G101 fires on the `EnvSharedSecret = "CRSW_SHARED_SECRET"` constant** — an
  identifier saying "secret" next to a string literal. It is an env var *name*, published
  verbatim in `.env.example`, so it carries a `//nolint:gosec` with the reasoning, in the
  style `exec.go` established for G204. **Expect the same on any future `Env*Secret`/
  `*Token` constant; it is not a signal there.**
- **gitleaks blocked the first commit attempt, and it was right.** The test fixture was a
  constant named `goodSecret` holding a run of 32 lowercase hex digits — which is exactly
  the shape of a real HMAC key, and matches both the repo's own `crsw-shared-secret` rule
  and the default generic-key rule. Fixed by spelling the fixture in words
  (`"test-only-shared-secret-32-bytes"`, still exactly 32 bytes), **not** by adding a
  `.gitleaks.toml` allowlist entry — an exception would have widened the scanner for
  every future file. **Do not write hex-shaped fixtures in the auth tests T009–T012 or
  the token tests T017/T035**, and do not paste one into this notebook either: the
  writeup tripped the same rule on its own commit, because `ralph/` is not in the
  `.gitleaks.toml` path allowlist (only `docs/*.md` and the deploy examples are).
  Describe the shape, never reproduce it. Note `gitleaks` is not in the Bash allowlist,
  so the only way to see a finding is to attempt the commit and read the hook output.
- **Decisions made here that T015 (`workdir.go`) inherits, so it does not re-decide them:**
  roots arrive already `Clean`ed, `EvalSymlinks`-resolved, absolute, verified to be
  directories, and deduplicated by canonical path. T015 owns only the containment check
  (the path-separator boundary and the `/home/u/codeEVIL` trap). `ApprovedRoot{Path,
  IsDefault}` lives in `config` per `data-model.md`; the plan's dependency arrows do not
  have `session` importing `config`, so T015 should take `[]config.ApprovedRoot` or
  `[]string` as a **parameter** rather than importing config into the model.
- **Three fail-closed rulings the spec did not spell out.** Each is stricter than silence,
  never looser; flagged here so an operator can overrule any of them:
  1. **A missing default root is fatal.** `spec.md` says the daemon "starts either way"
     when roots are unset, but `ApprovedRoot` requires symlinks resolved at startup and
     you cannot resolve a path that does not exist. A phantom allowlist that no work_dir
     can ever satisfy is worse than refusing to boot. `~/code` exists on this host.
  2. **A hostname in `CRSW_LISTEN` is refused, not resolved.** `/etc/hosts` or a resolver
     can point `localhost` off loopback without the operator's configured value changing,
     which would defeat FR-005 invisibly. Only a loopback IP literal is accepted, so
     `localhost:8765` is a startup failure — worth knowing before someone tries it.
  3. **A root that is not a directory, an empty `:` entry, a relative path, and port 0 or
     >65535 are all fatal.** Each is a typo that would otherwise produce a working daemon
     with a meaningless allowlist or an unreachable listener.
- **A warning that cannot be written is fatal.** FR-004 says an unconfigured allowlist is
  never silent, so `warnDefaultRoot` propagates the `io.Writer` error rather than dropping
  it — pinned by `TestLoadFromFailsWhenTheWarningCannotBeEmitted`. `errcheck` would have
  caught the dropped error anyway; the ruling is about what to do with it.
- **The banner is 5 lines on purpose.** `quickstart.md:125` reads it with `head -5`, so a
  6-line banner would cut the path off in the documented validation. A test asserts the
  path falls inside the first five lines.
- **`Config` has `String()` *and* `GoString()`, both redacting.** `%v`, `%s`, `%+v` and
  `%q` all route through `String`; `%#v` does not — it needs `GoString` — and `%#v` in a
  debug print is a realistic leak path. Tested across both verbs and both `Config` and
  `*Config`.
- **Mutation-probing (iterations 4–7) earned its keep a fifth time.** Seven mutations,
  each caught only by its intended test: the 32-byte minimum removed (2 tests), the
  loopback check removed (3), the default root set to `$HOME` itself (2), the warning's
  write error swallowed (1), `String` printing the secret (1), the `IsDir` check removed
  (1), and empty `:` entries silently skipped (3). Reverted, then the gate re-run.

**Left:** T008 is next (`internal/audit/audit.go`: structured JSON on stdout via
`log/slog` as a **fixed struct** — `time`, `action`, `caller`, `session_id`, `decision`,
`reason`, `remote` — with no `map[string]any` and no `slog.Any` passthrough, and a test
asserting the type offers no way to attach free-form content). T008 closes the
Foundational phase; T009 onwards is US1. Note `config` deliberately does **not** import
`audit` — the default-root warning predates any audit sink and writes plain text to an
`io.Writer`; the `startup` audit record naming `IsDefault` belongs to T032.

**Findings (noticed, not fixed):**

1. **New this iteration: `.env.example` does not exist yet**, so the `.gitleaks.toml`
   allowlist entry for it (`\.env\.example$`) currently guards nothing. T040 owns
   creating it. Harmless today, but the allowlist reads as though the file is there.
2. **New this iteration: the loud default-root warning goes to the injected writer
   (stderr from `Load()`), while T008's audit records go to stdout.** `quickstart.md:125`
   pipes `2>&1`, so its check passes either way — but an operator reading only
   `journalctl` stdout, or a future change that captures just stdout, would lose the
   warning. Worth deciding deliberately in T032 when startup wiring lands.
3. **Iteration 7 #1 still stands:** bidi and invisible Unicode (U+202A–U+202E,
   U+2066–U+2069, U+200B–U+200D, U+2028/U+2029) are not stripped by `tmuxctl.Strip`, by
   design — it is a control-*sequence* stripper. The milestone-2 dashboard must decide
   this; `html/template` escaping does not help, as these are legitimate characters.
4. **Iteration 6 #2 / 7 #2 still stands:** a failed `paste-buffer` leaves caller prompt
   text in a named tmux buffer, readable by any tmux client until the next prompt
   overwrites it. Prompts are secret under `docs/security.md` §3. Needs a `delete-buffer`
   argv builder in `fake.go` *and* `exec.go` together, which is why no single task owns it.
5. **Iteration 6 #1 / 7 #3 still stands:** T028 will report a false failure on the last
   session — killing the only session stops the tmux server, and `Has` then errors rather
   than returning false. Use `List`, which treats no server as empty. Do not loosen `Has`.
6. **Iteration 6 #3 / 7 #4 still stands:** `contracts/tmuxctl.md` names only `no server
   running` for the empty-server case and is stale — the code correctly matches both
   messages. Worth an operator fixing the contract.
7. **Iteration 1 #1 / 2 #1 / 3 #2 / 4 #2 / 5 #2 / 6 #4 / 7 #5 still stands, eighth
   iteration carrying it:** `loop.sh`'s sweep commit uses `--no-verify`, bypassing the
   gitleaks pre-commit hook. **This iteration is the concrete proof of why that matters:**
   the hook fired on a real false-positive-shaped fixture and blocked the commit until it
   was fixed. A sweep would have committed it unscanned. Not in the plan, and Principle IV
   forbids wandering — needs an operator or a task of its own.
8. **Iteration 2 #2 / 3 #3 / 4 #3 / 5 #3 / 6 #5 / 7 #6 still stands:** duplicate checkbox
   state in `IMPLEMENTATION_PLAN.md` and `specs/.../tasks.md`, `PROMPT.md` step 9 naming
   only the plan. Ticked both by hand again, again only because the finding was written
   down. Seventh iteration of manual compensation for a one-line fix to step 9.
9. **Iteration 6 #6 / 7 #7 still stands:** `AGENTS.md`'s command table has no entry for
   `go test -tags tmux ./...` or `go vet -tags tmux ./...`, so "Test (all)" is not all.

---

## Iteration 9 — 2026-08-03 02:47

**Did:** Completed **T008**, closing the Foundational phase. Added
`internal/audit/audit.go` (`Record`, `Action`, `Decision`, `Logger`, `New`/`NewTo`,
`Emit`) and `audit_test.go` (36 cases). One JSON object per line on stdout via
`log/slog`, carrying exactly the seven fields in `data-model.md`'s AuditRecord table.
Ticked T008 in **both** `ralph/IMPLEMENTATION_PLAN.md` and `specs/.../tasks.md`.

Gate, executed not asserted:

```
go build ./...              OK
go vet ./...                OK
go vet -tags tmux ./...     OK
go test -count=1 ./...      OK (internal/audit 36 cases, config 45, tmuxctl 84)
go test -race -count=1 ./...   OK
go test -tags tmux ./...    OK (8 integration tests, real tmux)
golangci-lint run           OK
gofmt -l . / goimports -l . empty
go.sum                      absent  ✅ zero third-party deps still holds
```

**Learned (do not rediscover):**

- **The FR-042 guarantee is enforced by two reflection tests, and they are the point of
  the task — do not delete them when adding a field.** `TestRecordCannotCarryFreeFormContent`
  restates the six field names as literals and asserts every field's `Kind` is `String`,
  so an `Extra map[string]any`, a `[]slog.Attr`, or an `any` fails the build's tests.
  `TestLoggerOffersNoFreeFormEntryPoint` walks `Logger`'s method set and rejects any
  variadic method or any parameter of interface/map/slice/array/chan kind, which is what
  stops an `EmitWith(rec, extra ...any)` helper from reopening the hole. Both were
  mutation-probed and both fired.
- **slog's JSON handler emits `level` and `msg` whether you want them or not; they are
  dropped in `ReplaceAttr` by returning a zero `slog.Attr`.** The emitted record is
  exactly `time, action, caller, session_id, decision, reason, remote` — asserted as an
  exact key set, so a stray attribute added later is a failure rather than a surprise in
  a journal. This is also why `Emit` builds the `slog.Record` by hand
  (`slog.NewRecord` + `handler.Handle`) instead of calling `logger.Info`: the message
  slot has nothing to say, and building the record by hand is what lets the clock be
  injected at all — `Logger.Info` stamps `time.Now()` internally with no seam.
- **`Handle` returns an error and `errcheck` has `check-blank: true`, so it cannot be
  discarded — which forced the ruling: `Emit` returns `error`.** FR-041 makes the record
  mandatory, so a caller that could not write one has not completed the request it was
  auditing, and **T038's handlers must not ignore it** — the same shape as config's "a
  warning that cannot be written is fatal". There is no retry and no fallback sink: an
  unwritable stdout is a broken daemon, not a degraded one.
- **Three rulings the spec did not spell out.** Each is stricter than silence; flagged so
  an operator can overrule any of them:
  1. **`Decision` is validated as a closed set (`allow`/`deny`), `Action` is only checked
     for emptiness.** `data-model.md` gives exactly two decisions, so an unknown one is a
     bug worth refusing. Its action list is *examples* — and US2/US3 add routes
     (`GET /sessions`, `GET /sessions/{id}`, `.../output`) that will need names not on it.
     Closing `Action` here would have pre-decided T024–T027's vocabulary. **T024–T027 and
     T038 should add an `Action` constant per route** rather than reusing an approximate
     one; the constants exist to be extended.
  2. **An empty `Caller` becomes `unknown`** rather than an absent field, so `caller` is
     present on every record including a rejection — `data-model.md` names `unknown` for
     exactly that case.
  3. **`session_id`, `reason`, and `remote` are omitted when empty, not emitted as `""`.**
     `data-model.md` says session_id is "the 32-hex ID, or absent", and `reaper.destroy`
     and `startup.adopt` have no peer address to name.
- **Timestamps are UTC RFC3339 at second precision**, matching `data-model.md`'s example
  literally rather than slog's default (local zone, nanoseconds). Sub-second ordering is
  not lost in practice — the trail is a `jsonl` stream and line order is arrival order.
  Worth knowing before someone diffs a record against slog's usual output and thinks it
  is broken.
- **`NewTo(w io.Writer, now func() time.Time)` is the injection seam and `New()` is the
  two-line wrapper T032 should call**, same pattern as `config.LoadFrom` and `tmuxctl`'s
  `SetNow`. `TestNewWritesToStdout` proves the wiring by swapping `os.Stdout` for an
  `os.Pipe` — it is deliberately **not** `t.Parallel()`, since it mutates process state.
  Reuse that for any future "writes to stdout" assertion.
- **One `Logger` is safe to share across every request path.** slog's handler serialises
  its writes, so the concurrency test asserts 16 goroutines produce 16 parseable lines
  with no interleaving, under `-race`.
- **Mutation-probing (iterations 4–8) earned its keep a sixth time.** Eight mutations,
  each caught only by its intended test: `level`/`msg` left in (3 tests), local-zone
  nanosecond timestamps (2), the caller default removed (1), the optional fields always
  emitted (1), the write error swallowed (1), the decision check removed (1 test / 3
  subtests), an `Extra map[string]any` field added (1), and an `EmitWith(..., ...any)`
  method added (1). Reverted, then the gate re-run.
- **Fixture note, extending iteration 8's gitleaks lesson:** the session-ID fixture is
  deliberately *not* 32 hex characters (`"a-test-session-id"`). A realistic ID is exactly
  the shape the scanner reads as a credential, and this package stores whatever it is
  handed — the ID's shape is **T013's** assertion to make, not this one's.

**Left:** **The Foundational phase (T003–T008) is complete.** T009 is next and starts US1
(`internal/auth/hmac.go`: HMAC-SHA256 over `timestamp + "." + rawBody` with `hmac.Equal`,
the body re-buffered so the handler can still read it; table test covering valid, bad
signature, body tampered after signing, missing signature header, missing timestamp
header). Then T010–T042. Remember iteration 8's warning: **do not write hex-shaped
fixtures in T009–T012 or T017/T035** — gitleaks will block the commit.

**Findings (noticed, not fixed):**

1. **New this iteration: `Reason` is the one audit field that can carry arbitrary text,
   and nothing structural stops caller input reaching it.** The type is closed against
   maps and passthroughs, but `Reason` is a plain string, and an error built as
   `fmt.Errorf("bad name %q", name)` wrapped into a record would put caller-supplied
   bytes in the trail — the shape FR-042 forbids. Not fixed here: closing `Reason` to a
   vocabulary means designing T038's reason strings from T008, which is guessing. **T038
   should pass server-authored constants, and T039's leak test is the assertion that
   catches it if not.**
2. **New this iteration: `Emit`'s error has no obvious handler yet, and that is a decision
   T038/T020 must actually make, not inherit.** A record that cannot be written means the
   request was not audited; the honest response is to fail the request rather than perform
   an unaudited action. Whatever is chosen, it must not be a silently dropped error —
   `errcheck` will refuse the discard, but `if err != nil { return }` with an empty body
   would pass lint and lose the trail.
3. **Iteration 8 #2 still stands:** the loud default-root warning goes to stderr while
   audit records go to stdout. `quickstart.md:125` pipes `2>&1` so its check passes either
   way, but an operator reading only stdout would lose the warning. T032 owns deciding this
   when startup wiring lands.
4. **Iteration 8 #1 still stands:** `.env.example` does not exist yet, so the
   `.gitleaks.toml` allowlist entry for it guards nothing. T040 owns creating it.
5. **Iteration 7 #1 / 8 #3 still stands:** bidi and invisible Unicode (U+202A–U+202E,
   U+2066–U+2069, U+200B–U+200D, U+2028/U+2029) are not stripped by `tmuxctl.Strip`, by
   design — it is a control-*sequence* stripper. The milestone-2 dashboard must decide it;
   `html/template` escaping does not help, as these are legitimate characters.
6. **Iteration 6 #2 / 7 #2 / 8 #4 still stands:** a failed `paste-buffer` leaves caller
   prompt text in a named tmux buffer, readable by any tmux client until the next prompt
   overwrites it. Prompts are secret under `docs/security.md` §3. Needs a `delete-buffer`
   argv builder in `fake.go` *and* `exec.go` together, which is why no single task owns it.
7. **Iteration 6 #1 / 7 #3 / 8 #5 still stands:** T028 will report a false failure on the
   last session — killing the only session stops the tmux server, and `Has` then errors
   rather than returning false. Use `List`, which treats no server as empty. Do not loosen
   `Has`.
8. **Iteration 6 #3 / 7 #4 / 8 #6 still stands:** `contracts/tmuxctl.md` names only `no
   server running` for the empty-server case and is stale — the code correctly matches both
   messages. Worth an operator fixing the contract.
9. **Iteration 1 #1 / 2 #1 / 3 #2 / 4 #2 / 5 #2 / 6 #4 / 7 #5 / 8 #7 still stands, ninth
   iteration carrying it:** `loop.sh`'s sweep commit uses `--no-verify`, bypassing the
   gitleaks pre-commit hook (which fired correctly on this iteration's commit — `1 commits
   scanned … no leaks found`). Not in the plan, and Principle IV forbids wandering —
   needs an operator or a task of its own.
10. **Iteration 2 #2 / 3 #3 / 4 #3 / 5 #3 / 6 #5 / 7 #6 / 8 #8 still stands:** duplicate
    checkbox state in `IMPLEMENTATION_PLAN.md` and `specs/.../tasks.md`, `PROMPT.md` step 9
    naming only the plan. Ticked both by hand again, again only because the finding was
    written down. Eighth iteration of manual compensation for a one-line fix to step 9.
11. **Iteration 6 #6 / 7 #7 / 8 #9 still stands:** `AGENTS.md`'s command table has no entry
    for `go test -tags tmux ./...` or `go vet -tags tmux ./...`, so "Test (all)" is not all.

---

## Iteration 10 — 2026-08-03 02:59

**Did:** Completed **T009**, opening US1. Added `internal/auth/hmac.go`
(`Authenticator`, `New`, `Verify`, the `Header*` constants and six sentinel errors) and
`hmac_test.go` (46 tests / 82 cases). HMAC-SHA256 over `timestamp + "." + rawBody`,
compared with `hmac.Equal`, body buffered and put back for the handler. Ticked T009 in
**both** `ralph/IMPLEMENTATION_PLAN.md` and `specs/.../tasks.md`.

Gate, executed not asserted:

```
go build ./...              OK
go vet ./...                OK
go vet -tags tmux ./...     OK
go test -count=1 ./...      OK (internal/auth 46 tests / 82 cases)
go test -race -count=1 ./...   OK
go test -tags tmux ./...    OK (real tmux)
golangci-lint run           OK
gofmt -l . / goimports -l . empty
go.sum                      absent  ✅ zero third-party deps still holds
```

**Learned (do not rediscover):**

- **`Verify(r *http.Request) error` today; T012 changes the *return*, not the checks.**
  The six exported sentinels (`ErrMissingTimestamp`, `ErrMalformedTimestamp`,
  `ErrMissingSignature`, `ErrSignatureMismatch`, `ErrUnreadableBody`, `ErrBodyTooLarge`)
  exist so the table test can say *which* check failed. FR-011's uniformity is a property
  of the **HTTP response** (T020), not of this function — T012 should collapse these into
  one opaque value at the boundary while keeping the specific one for the audit reason,
  and should not need to touch the verification logic to do it.
- **`fmt.Fprintf(mac, ...)` does not pass the lint gate**, and neither does `_ =` —
  `errcheck` has `check-blank: true`. `hash.Hash` documents that `Write` never errors, so
  `sign` builds the payload with `strconv.AppendInt` + `append` and does **one checked
  `mac.Write`**, returning an unreachable `errUnsignable`. **Expect this on every future
  `hash.Hash`/`io.Writer` write in this repo**; the shape above is the one that lints.
- **The signed timestamp is the *parsed* value re-rendered (`strconv.AppendInt`), not the
  header string.** One instant therefore has exactly one signature, which is what T011's
  replay cache keys on. Pinned by `TestVerifyCanonicalisesTheTimestamp` (header
  `+1785706480` signed canonically still verifies). Non-canonical spellings that sign
  their own spelling simply mismatch — fail-closed either way.
- **The strconv parse error is dropped, never wrapped.** `strconv.ParseInt` quotes its
  input in the message, and these errors become the audit `reason`, which may not carry
  caller-supplied bytes (iteration 9's finding #1 is exactly this hole).
  `TestVerifyErrorsRevealNothing` is the guard and it fired when probed.
- **The body is read through `LimitReader(r.Body, maxBody+1)`, not `maxBody`.** A bare
  limit truncates silently, so an oversize request would be reported as a *signature
  mismatch* — indistinguishable from an attack in the trail, and it would hide the real
  cause from the operator. `TestVerifyStopsReadingAtTheLimit` also proves the limit bounds
  the read itself, not just the verdict; buffering to sign is the one place the daemon
  holds caller bytes before deciding anything.
- **The body is put back on *every* path, including the denials.** T020 still has to
  audit and answer a rejected request, and a half-drained reader is a trap for whatever
  reads next. Two separate tests cover the success and failure paths.
- **`auth.New` takes `(secret []byte, maxBody int64)` and copies the secret.** Aliasing
  `config.SharedSecret` would let a key change under a request in flight. `New` also
  refuses an empty secret and a non-positive `maxBody` — config already enforces the
  32-byte minimum, so this is the assertion that the two cannot drift, not a second
  opinion on the length.
- **gosec G101 fires on a test constant named `testSecret` when the *value* has enough
  entropy** — `"test-only-auth-secret-32-bytes!!"` tripped it, `"test-only-shared-secret-
  for-auth"` does not, and `config_test.go`'s equivalent never did. G101 scores entropy as
  well as matching the identifier. Fix by spelling the fixture in plain lowercase words
  (still exactly 32 bytes, still gitleaks-safe per iteration 8), **not** with a nolint.
- **Test fixtures compute the signature independently** (`signatureOver` re-derives it
  from the contract's description with `crypto/hmac`), so a bug in the production payload
  layout cannot be mirrored by the fixture meant to catch it. `testTimestamp` is
  `contracts/http-api.md`'s own example instant, so **T010 can pin its fake clock to that
  one value and every fixture here keeps passing.**
- **Mutation-probing (iterations 4–9) earned its keep a seventh time.** Six mutations,
  each caught only by its intended test: the payload separator removed (5 tests), the
  read limit off by one (2), re-buffering removed (2), the `sha256=` prefix dropped (5),
  the secret not copied (1), and the strconv error wrapped (1). Reverted, then the gate
  re-run. One mutation is **not** caught and cannot be: replacing `hmac.Equal` with `==`
  passes every test, because timing is not observable from a unit test. It is a review
  item forever — `docs/auth-and-sessions.md` lists it first for that reason.

**Left:** T010 (300s window both directions against a `Clock` in `internal/auth/clock.go`)
is next, then T011 (replay cache), T012 (`Caller` + one opaque error), and on to T042.
Iteration 8's warning still applies to T010–T012 and T017/T035: **no hex-shaped
fixtures** — gitleaks blocks the commit.

**Findings (noticed, not fixed):**

1. **New this iteration: `contracts/http-api.md` promises `400` for an oversize body, but
   auth runs before the decoder, so the size limit is enforced twice and the first one
   wins.** `Verify` returns `ErrBodyTooLarge` for a body over `CRSW_MAX_BODY_BYTES`;
   T021's `MaxBytesReader` never sees it. **T020 must decide** whether that maps to `400`
   (matching the contract's status-code table and its test matrix row) or is folded into
   the uniform `401` (matching FR-011's "any layer-2 failure"). The sentinel is separate
   from the auth failures precisely so T020 can choose; it is a genuine gap between
   `docs/auth-and-sessions.md`'s sample `Verify` and the contract, not a code defect.
2. **New this iteration: the signature covers the timestamp and body, but not the method
   or the path.** That is what FR-007 and the contract specify, and it is sufficient today
   because every route's effect is determined by its body — but it means a signed body is
   valid on *any* route. `POST /sessions` and `POST /sessions/{id}/prompt` take different
   shapes, so `DisallowUnknownFields` (T021) rejects a cross-route replay in practice.
   Worth an operator knowing the defence is the decoder, not the signature.
3. **Iteration 9 #1 still stands and now has a second guard:** `Reason` can carry
   arbitrary text. This package's errors are all fixed strings and a test enforces it —
   **T038 should pass server-authored constants for the same reason**, and T039's leak
   test is the assertion that catches it if not.
4. **Iteration 9 #2 still stands:** `audit.Emit`'s error has no handler yet. T020 owns the
   ruling — a request that could not be audited has not been completed.
5. **Iteration 8 #2 / 9 #3 still stands:** the loud default-root warning goes to stderr
   while audit records go to stdout. T032 owns deciding this when startup wiring lands.
6. **Iteration 8 #1 / 9 #4 still stands:** `.env.example` does not exist, so the
   `.gitleaks.toml` allowlist entry for it guards nothing. T040 owns creating it.
7. **Iteration 7 #1 / 8 #3 / 9 #5 still stands:** bidi and invisible Unicode
   (U+202A–U+202E, U+2066–U+2069, U+200B–U+200D, U+2028/U+2029) are not stripped by
   `tmuxctl.Strip`, by design. The milestone-2 dashboard must decide it.
8. **Iteration 6 #2 / 7 #2 / 8 #4 / 9 #6 still stands:** a failed `paste-buffer` leaves
   caller prompt text in a named tmux buffer. Needs a `delete-buffer` argv builder in
   `fake.go` *and* `exec.go` together, which is why no single task owns it.
9. **Iteration 6 #1 / 7 #3 / 8 #5 / 9 #7 still stands:** T028 will report a false failure
   on the last session — killing the only session stops the tmux server and `Has` then
   errors rather than returning false. Use `List`. Do not loosen `Has`.
10. **Iteration 6 #3 / 7 #4 / 8 #6 / 9 #8 still stands:** `contracts/tmuxctl.md` names only
    `no server running` for the empty-server case and is stale.
11. **Iteration 1 #1 / 2 #1 / 3 #2 / 4 #2 / 5 #2 / 6 #4 / 7 #5 / 8 #7 / 9 #9 still stands,
    tenth iteration carrying it:** `loop.sh`'s sweep commit uses `--no-verify`, bypassing
    the gitleaks pre-commit hook (which ran clean on this iteration's commit — `1 commits
    scanned … no leaks found`). Not in the plan, and Principle IV forbids wandering —
    needs an operator or a task of its own.
12. **Iteration 2 #2 / 3 #3 / 4 #3 / 5 #3 / 6 #5 / 7 #6 / 8 #8 / 9 #10 still stands:**
    duplicate checkbox state in `IMPLEMENTATION_PLAN.md` and `specs/.../tasks.md`,
    `PROMPT.md` step 9 naming only the plan. Ticked both by hand again, again only because
    the finding was written down. Ninth iteration of manual compensation for a one-line
    fix to step 9.
13. **Iteration 6 #6 / 7 #7 / 8 #9 / 9 #11 still stands:** `AGENTS.md`'s command table has
    no entry for `go test -tags tmux ./...` or `go vet -tags tmux ./...`, so "Test (all)"
    is not all.

---

## Iteration 11 — 2026-08-03 03:10

**Did:** Completed **T010**. Added `internal/auth/clock.go` (the one-method `Clock`
interface plus `systemClock`) and enforced the 300-second window in `Verify` **in both
directions** against it, with `ErrTimestampOutsideWindow` as the seventh sentinel.
`hmac_test.go` grew five window tests (17 test functions / 63 subtests / 80 runs in the
package now). Ticked T010 in **both** `ralph/IMPLEMENTATION_PLAN.md` and
`specs/.../tasks.md`.

Gate, executed not asserted:

```
go build ./...              OK
go vet ./...                OK
go vet -tags tmux ./...     OK
go test -count=1 ./...      OK (auth 17 tests / 80 runs, audit 36, config 45, tmuxctl 84)
go test -race -count=1 ./...   OK
go test -tags tmux ./...    OK (real tmux)
golangci-lint run           OK
gofmt -l . / goimports -l . empty
go.sum                      absent  ✅ zero third-party deps still holds
```

**Learned (do not rediscover):**

- **`New(secret, maxBody)` is now a one-line wrapper over
  `NewWithClock(secret, maxBody, Clock)`, matching `config.Load`/`LoadFrom` and
  `audit.New`/`NewTo`. T019/T020 should call `New`.** `NewWithClock` refuses a nil clock
  for the same reason it refuses an empty secret: an Authenticator that cannot tell the
  time cannot enforce the window, and discovering that on the first request is a daemon
  that started with half its auth missing.
- **The window is checked *before* the body is read**, which is a deliberate ordering and
  is pinned by `TestVerifyChecksTheWindowBeforeTheBody` (it asserts **zero** bytes of the
  body were read). `contracts/http-api.md`'s verification order says so, and the reason is
  concrete: everything past that point buffers and MACs up to `CRSW_MAX_BODY_BYTES`, and
  an unauthenticated caller should not be able to buy that work with a timestamp from last
  year. **A consequence T020 inherits: on this path `r.Body` is never replaced**, because
  `readBody` is not reached — the original reader is still whole and unread, which is the
  same guarantee by a different route.
- **`now.Sub(time.Unix(ts, 0)).Abs() <= maxSkew` is overflow-safe, and this was measured,
  not assumed.** A throwaway probe test walked `math.MaxInt64`, `math.MinInt64`, and both
  sides of the point where `time.Unix` overflows internally (`MaxInt64 - 62135596800`,
  the epoch↔year-1 offset): `Time.Sub` clamps to `maxDuration` rather than wrapping with
  it, and `Duration.Abs` maps `minDuration` to `maxDuration`, so an absurd timestamp can
  only ever land *outside* the window. Those values are now permanent cases in
  `TestVerifyRejectsExtremeTimestamps`, so a future rewrite into `int64` subtraction —
  which **would** overflow — fails a test instead of accepting a request.
- **`maxSkew` is unexported and `T011 must derive its TTL from it (2 × maxSkew)`, not
  restate 600s.** Same package, no reason to export. The *test* restates 300s as its own
  `testWindow` constant on purpose: a test that imports the number it is checking cannot
  notice that number changing, and the widening probe below proves the restatement works.
- **`ErrTimestampOutsideWindow` is one sentinel for both directions**, named for the
  window rather than for staleness. Two sentinels would let the audit trail distinguish
  "clock drift" from "stamped in the future" — the latter is a much stronger attack
  signal — but that is T038's vocabulary to design, not T010's to pre-empt. Flagged as a
  finding below rather than guessed at.
- **`rm <file>` IS permitted by Bash here**, despite iteration 4's note that only
  `go`/`git`/`gofmt`/`goimports`/`golangci-lint` run. That is what made the overflow probe
  disposable. Iteration 3's "throwaway probe, then delete" trick is therefore fully
  available — write it as a `_test.go` in the package under study, run it with
  `go test -run`, read the `t.Logf` output, delete it. Compound commands are still refused
  if *any* part is outside the allowlist, and `grep -c '^pattern$'` was refused for
  quoting reasons, so keep probe commands single and simple.
- **The fake clock is a value type (`fakeClock{now}`) with a `driftedClock(d)` helper**,
  so every window test shares one immutable clock and `t.Parallel()` needs no lock.
  Positive drift = the daemon's clock is ahead = the request is stale; negative drift =
  the request is from the future. **T011 will likely need an *advanceable* clock** to age
  entries out of the replay cache — that is a different type, not a change to this one.
- **Mutation-probing (iterations 4–10) earned its keep an eighth time.** Five mutations,
  each caught only by its intended tests: `.Abs()` dropped so only stale requests are
  refused (5 future rows + the far-future test + one extreme row), `<=` narrowed to `<`
  (both boundary rows), `maxSkew` widened to 3000s (6 rows + the host-clock test),
  the window check moved after the signature compare (the ordering test + the epoch row),
  and `systemClock.Now` stopped at the zero time (`TestNewUsesTheHostClock` alone).
  Reverted, then the gate re-run.

**Left:** T011 is next (`internal/auth/replay.go`: the replay cache keyed on the full
`sha256=…` signature, TTL `2 × maxSkew`, expired entries swept opportunistically on write,
`Observe` checking and recording in **one** critical section; tests prove a second use is
refused and that two concurrent replays produce exactly one winner). Then T012 (`Caller` +
one opaque error) and on to T042. Iteration 8's warning still applies to T011/T012 and
T017/T035: **no hex-shaped fixtures** — gitleaks blocks the commit.

**Findings (noticed, not fixed):**

1. **New this iteration: the audit trail cannot tell clock drift from a forged future
   timestamp.** Both return `ErrTimestampOutsideWindow`, so `auth.reject` records the same
   reason for an operator whose laptop clock slipped and for a request stamped a year
   ahead — which is a real attack signal, not an operations problem. Splitting the
   sentinel is a two-line change, but the reason vocabulary belongs to T038; deciding it
   here would pre-empt that. **T038 should decide whether to split it.**
2. **New this iteration: nothing yet forces the daemon's clock to be monotonic or even
   roughly right, and the window is only as good as it is.** `systemClock` reads
   `time.Now()`, so a host whose clock jumps backwards by an hour refuses every honest
   request, and one that jumps forwards accepts requests signed in what is now its past.
   There is no NTP assertion at startup and no plan task for one. Bounded — it fails
   closed in the direction that matters — but worth an operator knowing before deployment
   (T041).
3. **Iteration 10 #1 still stands:** `contracts/http-api.md` promises `400` for an
   oversize body, but auth runs first and returns `ErrBodyTooLarge`. **T020 must decide**
   whether that maps to `400` (the contract's table) or is folded into the uniform `401`
   (FR-011). Note the window now sits *ahead* of that check too, so an oversize body with
   a stale timestamp reports the timestamp — the ordering is deliberate and documented in
   `Verify`.
4. **Iteration 10 #2 still stands:** the signature covers the timestamp and body but not
   the method or path, so a signed body is valid on any route. `DisallowUnknownFields`
   (T021) is the defence, not the signature.
5. **Iteration 9 #1 / 10 #3 still stands:** `audit.Record.Reason` can carry arbitrary
   text. This package's errors are all fixed strings and a test enforces it — T038 should
   pass server-authored constants for the same reason.
6. **Iteration 9 #2 / 10 #4 still stands:** `audit.Emit`'s error has no handler yet. T020
   owns the ruling — a request that could not be audited has not been completed.
7. **Iteration 8 #2 / 9 #3 / 10 #5 still stands:** the loud default-root warning goes to
   stderr while audit records go to stdout. T032 owns deciding this.
8. **Iteration 8 #1 / 9 #4 / 10 #6 still stands:** `.env.example` does not exist, so the
   `.gitleaks.toml` allowlist entry for it guards nothing. T040 owns creating it.
9. **Iteration 7 #1 / 8 #3 / 9 #5 / 10 #7 still stands:** bidi and invisible Unicode are
   not stripped by `tmuxctl.Strip`, by design. The milestone-2 dashboard must decide it.
10. **Iteration 6 #2 / 7 #2 / 8 #4 / 9 #6 / 10 #8 still stands:** a failed `paste-buffer`
    leaves caller prompt text in a named tmux buffer. Needs a `delete-buffer` argv builder
    in `fake.go` *and* `exec.go` together, which is why no single task owns it.
11. **Iteration 6 #1 / 7 #3 / 8 #5 / 9 #7 / 10 #9 still stands:** T028 will report a false
    failure on the last session — killing the only session stops the tmux server and `Has`
    then errors rather than returning false. Use `List`. Do not loosen `Has`.
12. **Iteration 6 #3 / 7 #4 / 8 #6 / 9 #8 / 10 #10 still stands:** `contracts/tmuxctl.md`
    names only `no server running` for the empty-server case and is stale.
13. **Iteration 1 #1 / 2 #1 / 3 #2 / 4 #2 / 5 #2 / 6 #4 / 7 #5 / 8 #7 / 9 #9 / 10 #11
    still stands, eleventh iteration carrying it:** `loop.sh`'s sweep commit uses
    `--no-verify`, bypassing the gitleaks pre-commit hook (which ran clean on this
    iteration's commit — `1 commits scanned … no leaks found`). Not in the plan, and
    Principle IV forbids wandering — needs an operator or a task of its own.
14. **Iteration 2 #2 / 3 #3 / 4 #3 / 5 #3 / 6 #5 / 7 #6 / 8 #8 / 9 #10 / 10 #12 still
    stands:** duplicate checkbox state in `IMPLEMENTATION_PLAN.md` and
    `specs/.../tasks.md`, `PROMPT.md` step 9 naming only the plan. Ticked both by hand
    again, again only because the finding was written down. Tenth iteration of manual
    compensation for a one-line fix to step 9.
15. **Iteration 6 #6 / 7 #7 / 8 #9 / 9 #11 / 10 #13 still stands:** `AGENTS.md`'s command
    table has no entry for `go test -tags tmux ./...` or `go vet -tags tmux ./...`, so
    "Test (all)" is not all.

---

## Iteration 12 — 2026-08-03 03:25

**Did:** Completed **T011**. Added `internal/auth/replay.go` (`replayCache`, `Observe`,
`replayTTL = 2 × maxSkew`) and `replay_test.go` (11 tests / 16 runs; the package is now
28 tests / 96 runs), and wired the cache into `Verify` as the fourth and last layer-2
check with `ErrReplayedRequest` as the eighth sentinel. Ticked T011 in **both**
`ralph/IMPLEMENTATION_PLAN.md` and `specs/.../tasks.md`.

Gate, executed not asserted:

```
go build ./...              OK
go vet ./...                OK
go vet -tags tmux ./...     OK
go test -count=1 ./...      OK (auth 28 tests / 96 runs, audit 36, config 45, tmuxctl 84)
go test -race -count=1 ./...   OK
go test -tags tmux ./...    OK (real tmux)
golangci-lint run           OK
gofmt -l . / goimports -l . empty
go.sum                      absent  ✅ zero third-party deps still holds
```

**Learned (do not rediscover):**

- **`Observe(sig string) bool` takes no timestamp, and the three specs disagree about
  that.** `tasks.md` and `data-model.md` say `Observe(sig) bool`; `research.md` D10 and
  the sample in `docs/auth-and-sessions.md` say `Observe(sig, ts)`. Took `tasks.md`, which
  the plan names as the single source of truth — and the shorter signature is also the
  correct one: the entry is stamped with the *observation* time, and observation time is
  the only clock reading that makes the TTL exact. Recorded as a finding so an operator
  can reconcile the docs rather than a future task re-deciding it.
- **The TTL is not a free parameter, and this is the reasoning worth keeping.** A request
  stamped at `ts` satisfies the window while `|now - ts| <= maxSkew`. A signature first
  accepted at `T` therefore had `ts ∈ [T-300, T+300]`, and stays acceptable until
  `ts+300 <= T+600`. So `2 × maxSkew` is exactly right — anything shorter opens a gap at
  the far end of the window, anything longer only holds memory. `replayTTL` is written as
  `2 * maxSkew` so widening one widens both.
- **The expiry boundary is exclusive (`elapsed > TTL`, not `>=`) and that one instant is
  real.** The extreme case: a request stamped 300s in the future, first used at once,
  replayed 600s later — still inside the window, and its entry is *exactly* `replayTTL`
  old at that moment. `>=` would let it through. Pinned twice, at cache level and through
  `Verify`. A clock jumping backwards yields a negative elapsed time, which is not `>` the
  TTL, so entries are kept — fails closed.
- **`Observe` runs *after* `hmac.Equal`, and the attack it forecloses is not the obvious
  one.** The cache keys on the value the *daemon* computes, so an attacker cannot insert
  an entry for a signature they cannot compute. What they *can* do, if the record happened
  before the compare, is send a copy of the bytes an honest caller is about to send (a
  guessable create body at a predictable second) under a junk signature — the daemon
  computes the real signature for those bytes, records it, and refuses the genuine request
  as a replay when it arrives. No secret required. Plus the map would grow on
  unauthenticated traffic. **The first version of this test asserted the wrong thing and
  the mutation probe is what caught it** — see below.
- **`replay_test.go` is `package auth` (internal) while `hmac_test.go` next to it is
  `package auth_test`. Both in one directory is legal Go and deliberate here.** The TTL
  outlives what `Verify` can show (a signature stops passing the window at the same
  instant its entry expires, never after), and "swept on write" is a claim about the map,
  not about a response. Testing from inside kept `replayCache` unexported and avoided a
  `Size()` method existing only for tests. Cost: ~30 lines of duplicated fixtures, which
  is the cheaper half of the trade.
- **A plain "release all goroutines at once" concurrency test does NOT catch a split
  critical section — measured, not assumed.** Replacing the single locked section with
  check-under-lock / release / record-under-lock passed the first version of
  `TestReplayCacheObservesAtomically` every time. What works is a **gate clock**: `Observe`
  reads the clock as its first act, so a `Clock` that blocks every caller until all N have
  arrived parks the racers immediately before the section under test. With 256 racers ×
  64 rounds that catches the split **20/20 runs under `-race`** — but only **1/20 without
  it**, because the detector's instrumentation is what widens the unlock→relock gap.
  Detection is also strongly super-linear in rounds (8 rounds caught it 16/30, 64 rounds
  20/20), so do not "tidy" the round count down. **Reuse the gate-clock shape for T033's
  cap-boundary race and T036's destroy-racing-the-reaper**, and run those under `-race`.
- **Mutation-probing (iterations 4–11) earned its keep a ninth time, and this was its most
  valuable outing yet — it invalidated a test rather than the code.** Seven mutations:
  the expiry boundary made inclusive (3 tests incl. the Verify-level extreme), the
  critical section split (1, and only after the gate clock was built), `Observe` moved
  ahead of `hmac.Equal` (2), `replayTTL` halved to one window (4), the sweep removed (1),
  `.Abs()` applied to elapsed time (1), and the cache populated but never consulted (3).
  All reverted, gate re-run. The third of those initially caught **nothing**, which is how
  the forged-request fixture was found to be constructed wrongly — it varied the body
  instead of the signature, so it never exercised the ordering at all.
- `ErrReplayedRequest` is the eighth sentinel. T012 collapses all of them into one opaque
  value at the boundary; the specific one stays for the audit reason.

**Left:** T012 is next (`internal/auth/caller.go`: the `Caller` type, identity derived
server-side only, and a single opaque error so no caller learns which check failed —
`Verify`'s **return** changes, not its checks). Then T013–T042. Iteration 8's warning
still applies to T012 and T017/T035: **no hex-shaped fixtures** — gitleaks blocks the
commit.

**Findings (noticed, not fixed):**

1. **New this iteration: CI never runs `-race`.** `.github/workflows/ci.yml:178` runs
   `go test ./...` and nothing else. Every iteration of this loop has run `-race` by hand,
   which is the only reason the concurrency guarantees are checked at all — and as measured
   above, `TestReplayCacheObservesAtomically` catches a split critical section 20/20 under
   `-race` and 1/20 without it. So the repo's one real concurrency guard is currently
   ~95% invisible to CI, and T033/T034/T036 add three more. A one-line change to the
   workflow, but it is a guardrail change and Principle V says that is a reviewed PR, not
   something an iteration slips in. **Worth an operator doing before T033.**
2. **New this iteration: three specs disagree on `Observe`'s signature.** `tasks.md` and
   `data-model.md` say `Observe(sig) bool`; `research.md` D10 says `Observe(sig, ts) bool`
   and `docs/auth-and-sessions.md`'s sample calls `a.replay.Observe(want, ts)`. Took
   `tasks.md` (the plan names it the single source of truth) and the code is right, but two
   documents now describe an API that does not exist. Worth an operator reconciling them.
3. **New this iteration: the cache is unbounded in count, only in age.** Nothing caps
   `len(seen)`; growth is bounded only by how many *validly signed* requests arrive within
   600 seconds. That is a holder of the shared secret, so it is not a stranger's lever, and
   T034's rate limit bounds it further — but a compromised or buggy client could still grow
   it without limit, and no task in the plan owns a cap. Small (a signature is ~80 bytes),
   real, and worth an operator knowing.
4. **Iteration 11 #1 still stands:** the audit trail cannot tell clock drift from a forged
   future timestamp — both return `ErrTimestampOutsideWindow`. T038 should decide whether
   to split the sentinel.
5. **Iteration 11 #2 still stands:** nothing forces the daemon's clock to be monotonic or
   roughly right, and both the window and now the replay TTL are only as good as it is.
   No NTP assertion at startup and no plan task for one. Worth an operator knowing before
   T041.
6. **Iteration 10 #1 / 11 #3 still stands:** `contracts/http-api.md` promises `400` for an
   oversize body, but auth runs first and returns `ErrBodyTooLarge`. **T020 must decide**
   whether that maps to `400` or is folded into the uniform `401`.
7. **Iteration 10 #2 / 11 #4 still stands:** the signature covers the timestamp and body
   but not the method or path, so a signed body is valid on any route.
   `DisallowUnknownFields` (T021) is the defence, not the signature. Note the replay cache
   narrows this: the same signed body can now only be spent on **one** route, once.
8. **Iteration 9 #1 / 10 #3 / 11 #5 still stands:** `audit.Record.Reason` can carry
   arbitrary text. T038 should pass server-authored constants.
9. **Iteration 9 #2 / 10 #4 / 11 #6 still stands:** `audit.Emit`'s error has no handler
   yet. T020 owns the ruling — a request that could not be audited has not been completed.
10. **Iteration 8 #2 / 9 #3 / 10 #5 / 11 #7 still stands:** the loud default-root warning
    goes to stderr while audit records go to stdout. T032 owns deciding this.
11. **Iteration 8 #1 / 9 #4 / 10 #6 / 11 #8 still stands:** `.env.example` does not exist,
    so the `.gitleaks.toml` allowlist entry for it guards nothing. T040 owns creating it.
12. **Iteration 7 #1 / 8 #3 / 9 #5 / 10 #7 / 11 #9 still stands:** bidi and invisible
    Unicode are not stripped by `tmuxctl.Strip`, by design. The milestone-2 dashboard must
    decide it.
13. **Iteration 6 #2 / 7 #2 / 8 #4 / 9 #6 / 10 #8 / 11 #10 still stands:** a failed
    `paste-buffer` leaves caller prompt text in a named tmux buffer. Needs a
    `delete-buffer` argv builder in `fake.go` *and* `exec.go` together.
14. **Iteration 6 #1 / 7 #3 / 8 #5 / 9 #7 / 10 #9 / 11 #11 still stands:** T028 will report
    a false failure on the last session — killing the only session stops the tmux server
    and `Has` then errors rather than returning false. Use `List`. Do not loosen `Has`.
15. **Iteration 6 #3 / 7 #4 / 8 #6 / 9 #8 / 10 #10 / 11 #12 still stands:**
    `contracts/tmuxctl.md` names only `no server running` for the empty-server case and is
    stale.
16. **Iteration 1 #1 / 2 #1 / 3 #2 / 4 #2 / 5 #2 / 6 #4 / 7 #5 / 8 #7 / 9 #9 / 10 #11 /
    11 #13 still stands, twelfth iteration carrying it:** `loop.sh`'s sweep commit uses
    `--no-verify`, bypassing the gitleaks pre-commit hook (which ran clean on this
    iteration's commit — `1 commits scanned … no leaks found`). Not in the plan, and
    Principle IV forbids wandering — needs an operator or a task of its own.
17. **Iteration 2 #2 / 3 #3 / 4 #3 / 5 #3 / 6 #5 / 7 #6 / 8 #8 / 9 #10 / 10 #12 / 11 #14
    still stands:** duplicate checkbox state in `IMPLEMENTATION_PLAN.md` and
    `specs/.../tasks.md`, `PROMPT.md` step 9 naming only the plan. Ticked both by hand
    again, again only because the finding was written down. Eleventh iteration of manual
    compensation for a one-line fix to step 9.
18. **Iteration 6 #6 / 7 #7 / 8 #9 / 9 #11 / 10 #13 / 11 #15 still stands:** `AGENTS.md`'s
    command table has no entry for `go test -tags tmux ./...` or `go vet -tags tmux ./...`,
    so "Test (all)" is not all. Finding #1 above is the same shape: the commands that
    actually protect this repo are not the documented ones.

---

## Iteration 13 — 2026-08-03 03:38

**Did:** Completed **T012**. Added `internal/auth/caller.go` (`CallerID`,
`CallerOperator`, `Caller`, the unexported `denial`, `ErrUnauthorized`, and `Reason`) and
`caller_test.go`. `Verify`'s signature changed from `error` to `(*Caller, error)`: it now
names the caller server-side and answers every failure with one opaque value. The package
is 34 tests / 134 runs. Ticked T012 in **both** `ralph/IMPLEMENTATION_PLAN.md` and
`specs/.../tasks.md`.

Gate, executed not asserted:

```
go build ./...              OK
go vet ./...                OK
go vet -tags tmux ./...     OK
go test -count=1 ./...      OK (auth 34 tests / 134 runs, audit 36, config 45, tmuxctl 84)
go test -race -count=1 ./...   OK
go test -tags tmux ./...    OK (real tmux)
golangci-lint run           OK
gofmt -l . / goimports -l . empty
go.sum                      absent  ✅ zero third-party deps still holds
```

**Learned (do not rediscover):**

- **`Verify(r) (*Caller, error)` is the shape T019/T020 wire up, and the two returns are
  mutually exclusive by construction:** an identity comes back exactly when the error is
  nil. Both test files' `verifyReason` helper asserts that on every one of the ~50
  pre-existing call sites, so a future edit that names a caller for a refused request
  fails in ten tests rather than handing a handler an owner it should not have.
- **The eight sentinels are no longer what `Verify` returns.** They are wrapped in an
  unexported `denial` whose `Error()` is the single word `unauthorized`, whose `Unwrap`
  points at `ErrUnauthorized` (**not** at the reason), and whose reason is reachable only
  through `auth.Reason(err)`. **T020's audit call is `auth.Reason(err).Error()`**; the
  response is the uniform 401 and never touches the reason. `Reason` is nil-safe and
  passes a non-denial straight through, so middleware needs no type check first.
- **`Unwrap` returning the reason was tried as a mutation and is exactly the wrong
  design.** It makes `errors.Is(err, ErrSignatureMismatch)` answer true, which puts "which
  check failed" one honest-looking branch away from the response body. Ten cases fail if
  someone re-introduces it. Same for `Error()` returning the reason.
- **The derivation takes no `*http.Request` at all, and that is the enforcement mechanism,
  not a style choice.** `docs/auth-and-sessions.md`'s sample shows `a.callerFor(r)`; this
  package deliberately does not, because a function that was never handed the request
  cannot read an `X-CRSW-Caller` header from it. The mutation that added the parameter and
  read one header was caught by exactly one case out of twelve — which is the test earning
  its keep, but note it only fires because the case names that header. **If milestone 2's
  Access identity needs the request, pass it the *validated* token, never the raw
  request.**
- **`CallerOperator` is `"operator"`, restated as a literal in `caller_test.go` because
  `data-model.md`'s audit table spells it that way.** **T031 must give adopted sessions
  this same constant** — an ownership check is only as good as both sides naming the same
  thing, and an adopted session whose owner is spelled differently is unreachable by its
  own operator. It is deliberately not configurable: a second place to set it is a second
  place for the two to disagree.
- **`CallerID` lives in `auth`, so T016's `Session.Owner` has to come from somewhere.**
  `data-model.md` types `Owner` as `CallerID`, but `plan.md`'s dependency arrows show
  `httpapi → {auth, session}` and `session → tmuxctl` — no `session → auth` edge. Either
  T016 imports `auth` for the type (harmless: `auth` imports nothing from the project) or
  it stores a plain string and `httpapi` converts. **Flagged, not decided — T016 owns it.**
- **Two test files in one directory can each define `verifyReason` because they are in
  different packages** (`auth_test` and `auth`), which iteration 12 set up. The
  duplication is deliberate; the alternative is exporting a test seam.
- **Mutation-probing (iterations 4–12) earned its keep a tenth time.** Six mutations, each
  caught only by its intended tests: `Unwrap` returning the reason (10 cases + the
  `ErrUnauthorized` assertion), `Error()` returning the reason (20), identity read from an
  `X-CRSW-Caller` header (1 — the case written for it), a caller returned alongside a
  denial (4 tests across both test files), `Reason` flattened to `ErrUnauthorized` (10
  tests / 64 subtests), and `CallerOperator` renamed to `root` (1). Reverted, gate re-run.

**Left:** T013 is next (`internal/session/id.go`: 16 bytes from `crypto/rand` → 32
lowercase hex, **not** `crypto/rand.Text`, which needs a `go 1.24` directive go.mod
deliberately does not have; test asserts the `^[a-f0-9]{32}$` shape, non-sequential values,
and no collisions across a large sample). T013–T015 are `[P]` and start the `session`
package. Then T016–T042. Iteration 8's warning still applies to T017/T035: **no hex-shaped
fixtures** — gitleaks blocks the commit. Note T013's own output *is* hex, so assert its
shape with a regexp rather than pasting a sample ID into a fixture or into this notebook.

**Findings (noticed, not fixed):**

1. **New this iteration: `docs/auth-and-sessions.md`'s sample is now wrong in two ways.**
   Its `Verify` returns `(*Caller, error)` — which is right — but it returns
   `errors.New("bad timestamp")`-style errors directly rather than a uniform value, and it
   derives identity with `a.callerFor(r)`, passing the whole request into the derivation.
   The code is right and the doc is stale on both counts. It is the file the progressive-
   disclosure table sends the next auth task to. Worth an operator fixing.
2. **New this iteration: nothing yet stops a handler from putting `auth.Reason(err)` in a
   response.** The denial is opaque, but `Reason` is exported and its result is an ordinary
   error; the uniformity guarantee ends at the point T020 decides what to write. The
   sentinels' strings are all fixed and caller-free (a test enforces that), so the leak
   would be "which check failed", not caller data. **T020 should assert the 401 body is
   byte-identical across failure modes** — the assertion T023 already owes for the 404.
3. **Iteration 12 #1 still stands:** CI never runs `-race`
   (`.github/workflows/ci.yml:178` runs `go test ./...` only), so the repo's concurrency
   guards are ~95% invisible to it. Worth an operator doing before T033.
4. **Iteration 12 #2 still stands:** three specs disagree on `Observe`'s signature —
   `tasks.md`/`data-model.md` say `Observe(sig)`, `research.md` D10 and
   `docs/auth-and-sessions.md` say `Observe(sig, ts)`. The code follows `tasks.md`.
5. **Iteration 12 #3 still stands:** the replay cache is unbounded in count, only in age.
   No task in the plan owns a cap.
6. **Iteration 11 #1 / 12 #4 still stands:** the audit trail cannot tell clock drift from a
   forged future timestamp — both reasons are `ErrTimestampOutsideWindow`. T038 should
   decide whether to split the sentinel. Note this iteration makes it cheaper to fix: the
   split is now invisible to clients by construction.
7. **Iteration 11 #2 / 12 #5 still stands:** nothing forces the daemon's clock to be
   monotonic or roughly right, and both the window and the replay TTL are only as good as
   it is. No NTP assertion at startup and no plan task for one.
8. **Iteration 10 #1 / 11 #3 / 12 #6 still stands:** `contracts/http-api.md` promises `400`
   for an oversize body, but auth runs first and `ErrBodyTooLarge` now arrives wrapped in
   the denial like every other refusal. **T020 must decide** whether to reach for
   `auth.Reason` to answer `400` or fold it into the uniform `401` (FR-011). The denial
   makes `401` the default and `400` the deliberate exception, which is the right way round
   but is now a decision someone has to make on purpose.
9. **Iteration 10 #2 / 11 #4 / 12 #7 still stands:** the signature covers the timestamp and
   body but not the method or path, so a signed body is valid on any route.
   `DisallowUnknownFields` (T021) is the defence; the replay cache narrows it to one route,
   once.
10. **Iteration 9 #1 / 10 #3 / 11 #5 / 12 #8 still stands:** `audit.Record.Reason` can carry
    arbitrary text. T038 should pass server-authored constants — `auth.Reason(err).Error()`
    is exactly that shape.
11. **Iteration 9 #2 / 10 #4 / 11 #6 / 12 #9 still stands:** `audit.Emit`'s error has no
    handler yet. T020 owns the ruling — a request that could not be audited has not been
    completed.
12. **Iteration 8 #2 / 9 #3 / 10 #5 / 11 #7 / 12 #10 still stands:** the loud default-root
    warning goes to stderr while audit records go to stdout. T032 owns deciding this.
13. **Iteration 8 #1 / 9 #4 / 10 #6 / 11 #8 / 12 #11 still stands:** `.env.example` does not
    exist, so the `.gitleaks.toml` allowlist entry for it guards nothing. T040 owns it.
14. **Iteration 7 #1 / 8 #3 / 9 #5 / 10 #7 / 11 #9 / 12 #12 still stands:** bidi and
    invisible Unicode are not stripped by `tmuxctl.Strip`, by design. The milestone-2
    dashboard must decide it.
15. **Iteration 6 #2 / 7 #2 / 8 #4 / 9 #6 / 10 #8 / 11 #10 / 12 #13 still stands:** a failed
    `paste-buffer` leaves caller prompt text in a named tmux buffer. Needs a
    `delete-buffer` argv builder in `fake.go` *and* `exec.go` together.
16. **Iteration 6 #1 / 7 #3 / 8 #5 / 9 #7 / 10 #9 / 11 #11 / 12 #14 still stands:** T028
    will report a false failure on the last session — killing the only session stops the
    tmux server and `Has` then errors rather than returning false. Use `List`.
17. **Iteration 6 #3 / 7 #4 / 8 #6 / 9 #8 / 10 #10 / 11 #12 / 12 #15 still stands:**
    `contracts/tmuxctl.md` names only `no server running` for the empty-server case and is
    stale.
18. **Iteration 1 #1 / 2 #1 / 3 #2 / 4 #2 / 5 #2 / 6 #4 / 7 #5 / 8 #7 / 9 #9 / 10 #11 /
    11 #13 / 12 #16 still stands, thirteenth iteration carrying it:** `loop.sh`'s sweep
    commit uses `--no-verify`, bypassing the gitleaks pre-commit hook (which ran clean on
    this iteration's commit — `1 commits scanned … no leaks found`). Not in the plan, and
    Principle IV forbids wandering — needs an operator or a task of its own.
19. **Iteration 2 #2 / 3 #3 / 4 #3 / 5 #3 / 6 #5 / 7 #6 / 8 #8 / 9 #10 / 10 #12 / 11 #14 /
    12 #17 still stands:** duplicate checkbox state in `IMPLEMENTATION_PLAN.md` and
    `specs/.../tasks.md`, `PROMPT.md` step 9 naming only the plan. Ticked both by hand
    again, again only because the finding was written down. Twelfth iteration of manual
    compensation for a one-line fix to step 9.
20. **Iteration 6 #6 / 7 #7 / 8 #9 / 9 #11 / 10 #13 / 11 #15 / 12 #18 still stands:**
    `AGENTS.md`'s command table has no entry for `go test -tags tmux ./...` or
    `go vet -tags tmux ./...`, so "Test (all)" is not all.

---

## Iteration 14 — 2026-08-03 09:22

**Did:** Completed **T013** by *verifying* it. The code (`internal/session/id.go`,
`id_test.go`, 8 tests / 15 runs) arrived in `aec3274` from an iteration killed before it
could prove anything, and was deliberately left unticked so this iteration would own the
proof. **No code changed this iteration** — the tree is byte-identical to `aec3274` plus
two ticked checkboxes and this entry. Read `id.go` against T013's `tasks.md` entry
(16 bytes `crypto/rand` → 32 lowercase hex, not `crypto/rand.Text`), ran the gate, then
mutation-probed the tests. Ticked T013 in **both** `ralph/IMPLEMENTATION_PLAN.md` and
`specs/.../tasks.md`.

Gate, executed not asserted:

```
go build ./...              OK
go vet ./...                OK
go vet -tags tmux ./...     OK
go test -count=1 ./...      OK (session 8 tests / 15 runs, auth 34, audit 36, config 45,
                                tmuxctl 84)
go test -race -count=1 ./...   OK
go test -tags tmux ./...    OK (real tmux)
golangci-lint run           OK
gofmt -l . / goimports -l . empty
go.sum                      absent  ✅ zero third-party deps still holds
git status                  clean against HEAD after every probe was reverted
```

**Learned (do not rediscover):**

- **Verifying salvaged code is a real task, not a formality, and the split worked.** Five
  mutations, each killed only by the tests written for it: uppercase hex (4 cases), a
  short read tolerated via `r.Read` instead of `io.ReadFull` (2), `idBytes` halved to 8
  (6), a zeroed 3-byte prefix (the column test, naming byte 0, 1, and 2 individually), and
  the partially-read bytes interpolated into the error (1). Reverted each, gate re-run
  green at the end.
- **The short-read mutation is the one worth remembering for T017.** `r.Read` on a source
  one byte short returns an ID of exactly the right shape and the right length, with a
  byte of the zero value standing in for entropy that was never read — it fails *open* and
  looks perfect. `io.ReadFull` is what makes it fail closed. **T017's token generation has
  the identical shape and must use `ReadFull` too**, and its test needs the
  "runs out one byte early" case, not just an "empty source" case, to catch it.
- **`TestNewIDIsNotSequential`'s per-byte-column assertion is what catches a fake source.**
  Shape and collision tests both pass a counter, a fixed prefix, or a random suffix on a
  constant — 100k draws of `0000…` + 13 random bytes collide with nobody and match
  `^[a-f0-9]{32}$`. Only "every byte position takes ≥64 distinct values across 2048 draws"
  fails, and it reports the offending position. **Copy this shape into T017.**
- **`newIDFrom(io.Reader)` stays unexported and that is load-bearing.** It is the seam that
  makes the exhausted-entropy path testable; exporting it would let a caller supply the
  randomness behind an ID that a bearer token is scoped to. `id_test.go` is therefore an
  *internal* test (`package session`, not `session_test`). **T014/T015/T016 land in the
  same directory — if any of them wants an external test package, the two can coexist
  (iteration 12 set that up in `auth`), but do not "fix" `id_test.go` into `session_test`;
  it would lose `newIDFrom`, `idBytes`, and four of the eight tests.**
- **Iteration 13's no-hex-fixtures warning had a subtlety worth stating:** the ban is on
  hex *in files*, and it costs nothing here because every assertion is a regexp, a decode,
  or a value computed in the test (`strings.Repeat`, `fmt.Sprintf("%x", …)`). `fmt`'s `%x`
  as an independent oracle is the trick — it does not route through `encoding/hex`, so
  agreeing with it is evidence rather than a restatement of the implementation. Note the
  *failure output* of a mutated build does print live IDs to the terminal; that is fine,
  but do not paste one into this notebook or a fixture.
- **`IDLen` is exported and `idBytes` is not**, so a future caller sizing a buffer cannot
  drift from the encoder. `TestIDLenIsTwoHexCharactersPerByte` asserts the 128-bit floor
  separately from the shape, because a shrunk ID would still match the regexp if `IDLen`
  moved with it — assert entropy on its own or it is not asserted at all.

**Left:** T014 is next (`internal/session/name.go`: `^[a-zA-Z0-9-]{1,64}$`, rejecting `:`
and `.` explicitly because they address a different tmux target; hostile table test
covering `:`, `.`, path separators, leading `-`, empty, 65 characters, control characters,
non-ASCII). Then T015 (workdir allowlist, including the `/home/u/codeEVIL` prefix trap),
T016 (which still owes the `Owner`-type ruling flagged in iteration 13), and T017–T042.
T014 and T015 are `[P]` with T013 and touch no shared file.

**Findings (noticed, not fixed):**

1. **New this iteration: `git checkout -- <path>` is not in the permission allowlist, so an
   iteration cannot cheaply revert a file.** This matters for `PROMPT.md` step 6, which
   instructs "revert your change and log why" — the documented recovery path needs an
   approval an autonomous run cannot give. I reverted the five mutations with `Edit` in
   reverse instead, which works but is manual and error-prone (verified byte-identical to
   `HEAD` with `git status --porcelain` afterwards). Also blocked: `{ … }` shell grouping
   and `if …; then` compound commands trip the guard's "brace with quote character" and
   multi-operation checks, so probe loops have to be run one command at a time. **Worth an
   operator adding `git checkout --` and `git restore` to the allowlist.**
2. **New this iteration: the `format-and-lint` hook rewrites imports underneath an edit.**
   Changing the return to `strings.ToUpper(…)` caused the hook to add `"strings"` to the
   import block before the test ran. Harmless here — reverting the return line made the
   hook drop it again — but an iteration that edits a file, reads its own diff, and does
   not expect an import to appear will be confused. It also means a mutation probe is
   never *only* the line you changed.
3. **Iteration 13 #1 still stands:** `docs/auth-and-sessions.md`'s `Verify` sample is stale
   in two ways — errors returned directly rather than uniformly, and identity derived from
   the whole request via `a.callerFor(r)`.
4. **Iteration 13 #2 still stands:** nothing stops a handler from putting `auth.Reason(err)`
   in a response body. T020 should assert the 401 body is byte-identical across failure
   modes.
5. **Iteration 12 #1 / 13 #3 still stands:** CI never runs `-race`
   (`.github/workflows/ci.yml:178` runs `go test ./...` only). Worth an operator doing
   before T033.
6. **Iteration 12 #2 / 13 #4 still stands:** three specs disagree on `Observe`'s signature.
   The code follows `tasks.md`.
7. **Iteration 12 #3 / 13 #5 still stands:** the replay cache is unbounded in count, only in
   age. No task owns a cap.
8. **Iteration 11 #1 / 12 #4 / 13 #6 still stands:** the audit trail cannot tell clock drift
   from a forged future timestamp. T038 should decide whether to split the sentinel.
9. **Iteration 11 #2 / 12 #5 / 13 #7 still stands:** nothing forces the daemon's clock to be
   monotonic or roughly right, and both the window and the replay TTL depend on it.
10. **Iteration 10 #1 / 11 #3 / 12 #6 / 13 #8 still stands:** `contracts/http-api.md`
    promises `400` for an oversize body but auth runs first. **T020 must decide.**
11. **Iteration 10 #2 / 11 #4 / 12 #7 / 13 #9 still stands:** the signature covers timestamp
    and body but not method or path, so a signed body is valid on any route.
12. **Iteration 9 #1 / 10 #3 / 11 #5 / 12 #8 / 13 #10 still stands:** `audit.Record.Reason`
    can carry arbitrary text. T038 should pass server-authored constants.
13. **Iteration 9 #2 / 10 #4 / 11 #6 / 12 #9 / 13 #11 still stands:** `audit.Emit`'s error
    has no handler yet. T020 owns the ruling.
14. **Iteration 8 #2 / 9 #3 / 10 #5 / 11 #7 / 12 #10 / 13 #12 still stands:** the loud
    default-root warning goes to stderr while audit records go to stdout. T032 owns it.
15. **Iteration 8 #1 / 9 #4 / 10 #6 / 11 #8 / 12 #11 / 13 #13 still stands:** `.env.example`
    does not exist, so the `.gitleaks.toml` allowlist entry for it guards nothing. T040.
16. **Iteration 7 #1 / 8 #3 / 9 #5 / 10 #7 / 11 #9 / 12 #12 / 13 #14 still stands:** bidi and
    invisible Unicode are not stripped by `tmuxctl.Strip`, by design. Milestone 2 decides.
17. **Iteration 6 #2 / 7 #2 / 8 #4 / 9 #6 / 10 #8 / 11 #10 / 12 #13 / 13 #15 still stands:** a
    failed `paste-buffer` leaves caller prompt text in a named tmux buffer.
18. **Iteration 6 #1 / 7 #3 / 8 #5 / 9 #7 / 10 #9 / 11 #11 / 12 #14 / 13 #16 still stands:**
    T028 will report a false failure on the last session — killing the only session stops
    the tmux server and `Has` then errors rather than returning false. Use `List`.
19. **Iteration 6 #3 / 7 #4 / 8 #6 / 9 #8 / 10 #10 / 11 #12 / 12 #15 / 13 #17 still stands:**
    `contracts/tmuxctl.md` names only `no server running` for the empty-server case.
20. **Iteration 1 #1 / … / 13 #18 still stands, fourteenth iteration carrying it:**
    `loop.sh`'s sweep commit uses `--no-verify`, bypassing the gitleaks pre-commit hook
    (which ran clean on this iteration's commit). Needs an operator or a task of its own.
21. **Iteration 2 #2 / … / 13 #19 still stands:** duplicate checkbox state in
    `IMPLEMENTATION_PLAN.md` and `specs/.../tasks.md`, `PROMPT.md` step 9 naming only the
    plan. Ticked both by hand again, again only because the finding was written down.
    Thirteenth iteration of manual compensation for a one-line fix to step 9.
22. **Iteration 6 #6 / … / 13 #20 still stands:** `AGENTS.md`'s command table has no entry
    for `go test -tags tmux ./...` or `go vet -tags tmux ./...`, so "Test (all)" is not all.

---

## Iteration 15 — 2026-08-03 09:31

**Did:** Completed **T014**. Added `internal/session/name.go` (`ValidateName`, `MaxNameLen`,
`ErrInvalidName`, `ErrNameIsTmuxTarget`, the unexported `isNameByte`, and the two tmux
separator constants) and `name_test.go`. The package is now 15 tests / 73 runs, up from
8 / 15. Ticked T014 in **both** `ralph/IMPLEMENTATION_PLAN.md` and `specs/.../tasks.md`.

Gate, executed not asserted:

```
go build ./...              OK
go vet ./...                OK
go vet -tags tmux ./...     OK
go test -count=1 ./...      OK (session 15 tests / 73 runs, auth 34, audit 36, config 45,
                                tmuxctl 84)
go test -race -count=1 ./...   OK
go test -tags tmux ./...    OK (real tmux)
golangci-lint run           OK
gofmt -l . / goimports -l . empty
go.sum                      absent  ✅ zero third-party deps still holds
git status                  clean against HEAD after every probe was reverted
gitleaks (pre-commit)       1 commits scanned … no leaks found
```

**Learned (do not rediscover):**

- **"Reject `:` and `.` *explicitly*" is not free — done naively it is untestable.** The
  character class already refuses both, so deleting a dedicated guard changes nothing an
  external test can see. What makes it enforceable is giving that guard its **own wrapped
  sentinel** (`ErrNameIsTmuxTarget`, wrapped alongside `ErrInvalidName` with Go 1.20's
  double `%w`) and asserting `errors.Is` per case, including the *negative* — an accented
  letter must **not** report the tmux reason. Deleting the guard then fails 9 subtests, and
  reordering it after the class check fails the same 9. **T015 has the identical trap:** its
  `/home/u/codeEVIL` prefix check and its `EvalSymlinks` failure both need a reason a test
  can name, or the separator-boundary rule is prose.
- **The guards' order is load-bearing and is now pinned by tests.** `:`/`.` are checked
  *before* the alphabet so that widening the alphabet later cannot silently re-admit them.
  That claim only holds while the order does, which is why the swap is one of the five
  probes.
- **`../etc` is refused for the tmux reason, not the path reason** — it opens with the same
  `.`. The first offending byte decides the reason, and the reason only ever reaches an
  audit record (the response is 400 either way). My first hostile table asserted the path
  reason and was wrong; the *code* was right. Worth knowing before writing T015's table,
  which will want the same string.
- **The independent-oracle trick from T013 generalises, and it is the test earning the most
  here.** `name.go` is a hand-rolled byte loop; `name_test.go` compiles
  `^[a-zA-Z0-9-]{1,64}$` from `spec.md` itself and asserts the two agree over a generated
  corpus — each of the 256 byte values alone, prefixed, suffixed, and embedded, plus every
  length from 0 to 66, plus every byte value in the last position of a 64-byte name. That
  corpus alone killed three of the five mutations, including two the hand-written tables
  would have missed. **Note Go's `$` matches end of text, not before a trailing newline**,
  so the transcription is faithful — a name with a trailing newline is in the hostile table
  because in a laxer regexp flavour it would pass.
- **Byte-wise iteration is what makes the byte ceiling and the character ceiling the same
  number**, and that is only true because the alphabet is ASCII. Thirty-two accented letters
  are 64 bytes and 32 characters; they pass the length check and die on the alphabet. If
  anyone widens the class beyond ASCII, `MaxNameLen` silently stops meaning characters.
- **`unicode.IsLetter`/`IsDigit` are the wrong tool, and the full-width colon says why:**
  U+FF1A renders as `:` in an audit record a human reads, while being no character the class
  knows. The alphabet is kept to what the store, the log, and the eventual dashboard agree on.
- **Iteration 14 #2 reproduced exactly:** the `format-and-lint` hook added `"strings"` to
  `name.go`'s import block the moment a probe used `strings.TrimSpace`, and dropped it again
  on revert. Expect a probe's diff to be larger than the line you changed.
- **Iteration 5's backslash-u warning still bites.** Every non-ASCII fixture in
  `name_test.go` is written as UTF-8 bytes for that reason; `\x` and `\n` pass through the
  Write tool untouched. Verified with `grep` after writing.

**Left:** T015 is next (`internal/session/workdir.go`: `filepath.Clean`, then
`filepath.EvalSymlinks` failing closed when the path does not exist, then containment under
an approved root **at a path-separator boundary**; tests cover `..`, an absolute escape, a
symlink pointing outside, a non-directory, and the `/home/u/codeEVIL` prefix trap against
root `/home/u/code`). `config.ApprovedRoot` already resolves the roots at startup, so T015
consumes those rather than re-resolving. Then T016 (which still owes the `Owner`-type ruling
flagged in iteration 13), T017 (`io.ReadFull`, the per-byte-column test, and no hex fixtures
— see iteration 14), and T018–T042.

**Findings (noticed, not fixed):**

1. **New this iteration: FR-027's class admits a leading `-`, and the task list calls a
   leading `-` hostile.** `tasks.md` T014 lists "leading `-`" among the hostile cases, but
   `^[a-zA-Z0-9-]{1,64}$` — stated verbatim in `spec.md` FR-027, `data-model.md`,
   `contracts/http-api.md`, `docs/security.md` §2, and both plan files — accepts `-foo`.
   **Resolved in favour of the regexp**, which is the higher-authority statement and appears
   five times, so this is a reading rather than a guess: a leading hyphen is an accepted
   case, with a comment naming what makes it safe (`data-model.md` builds every tmux target
   from the ID alone, so a name never reaches an argv slot where a leading `-` reads as a
   flag). **If an operator wanted it refused, that is one `if` and one moved test case** —
   but it would mean editing FR-027 too. Worth a ruling, because milestone 2 renders names
   in a UI and milestone 3 adds rename.
2. **New this iteration: nothing yet calls `ValidateName`.** T022 owns wiring it into
   `POST /sessions`, and until then the rule is enforced only in a test. The same will be
   true of T015's workdir check. **T022's test must cover a bad name, not only a bad path**,
   or both boundary checks ship unreferenced.
3. **Iteration 13 #1 / 14 #3 still stands:** `docs/auth-and-sessions.md`'s `Verify` sample is
   stale in two ways — errors returned directly rather than uniformly, and identity derived
   from the whole request via `a.callerFor(r)`.
4. **Iteration 13 #2 / 14 #4 still stands:** nothing stops a handler from putting
   `auth.Reason(err)` in a response body. T020 should assert the 401 body is byte-identical
   across failure modes.
5. **Iteration 12 #1 / 13 #3 / 14 #5 still stands:** CI never runs `-race`
   (`.github/workflows/ci.yml:178` runs `go test ./...` only). Worth an operator doing before
   T033.
6. **Iteration 12 #2 / 13 #4 / 14 #6 still stands:** three specs disagree on `Observe`'s
   signature. The code follows `tasks.md`.
7. **Iteration 12 #3 / 13 #5 / 14 #7 still stands:** the replay cache is unbounded in count,
   only in age. No task owns a cap.
8. **Iteration 11 #1 / 12 #4 / 13 #6 / 14 #8 still stands:** the audit trail cannot tell clock
   drift from a forged future timestamp. T038 should decide whether to split the sentinel.
9. **Iteration 11 #2 / 12 #5 / 13 #7 / 14 #9 still stands:** nothing forces the daemon's clock
   to be monotonic or roughly right, and both the window and the replay TTL depend on it.
10. **Iteration 10 #1 / 11 #3 / 12 #6 / 13 #8 / 14 #10 still stands:** `contracts/http-api.md`
    promises `400` for an oversize body but auth runs first. **T020 must decide.**
11. **Iteration 10 #2 / 11 #4 / 12 #7 / 13 #9 / 14 #11 still stands:** the signature covers
    timestamp and body but not method or path, so a signed body is valid on any route.
12. **Iteration 9 #1 / … / 14 #12 still stands:** `audit.Record.Reason` can carry arbitrary
    text. T038 should pass server-authored constants. This iteration adds two more candidates
    for that field — `ErrInvalidName`'s reasons are fixed strings carrying no caller input by
    construction (a test enforces it), so they are safe to pass through.
13. **Iteration 9 #2 / … / 14 #13 still stands:** `audit.Emit`'s error has no handler yet.
    T020 owns the ruling.
14. **Iteration 8 #2 / … / 14 #14 still stands:** the loud default-root warning goes to stderr
    while audit records go to stdout. T032 owns it.
15. **Iteration 8 #1 / … / 14 #15 still stands:** `.env.example` does not exist, so the
    `.gitleaks.toml` allowlist entry for it guards nothing. T040.
16. **Iteration 7 #1 / … / 14 #16 still stands:** bidi and invisible Unicode are not stripped
    by `tmuxctl.Strip`, by design. Milestone 2 decides. Session *names* are now closed to both
    by this task's alphabet — the gap is pane output only.
17. **Iteration 6 #2 / … / 14 #17 still stands:** a failed `paste-buffer` leaves caller prompt
    text in a named tmux buffer.
18. **Iteration 6 #1 / … / 14 #18 still stands:** T028 will report a false failure on the last
    session — killing the only session stops the tmux server and `Has` then errors rather than
    returning false. Use `List`.
19. **Iteration 6 #3 / … / 14 #19 still stands:** `contracts/tmuxctl.md` names only
    `no server running` for the empty-server case.
20. **Iteration 14 #1 still stands:** `git checkout -- <path>` and `git restore` are not in the
    permission allowlist, so `PROMPT.md` step 6's documented recovery path needs an approval an
    autonomous run cannot give. Five probes were reverted with `Edit` in reverse again this
    iteration, verified byte-identical to `HEAD` with `git status --porcelain`. **Worth an
    operator adding both.** New this iteration: writing outside the repo is also refused, and
    a heredoc past a few dozen lines aborts the Bash parser — this entry went in as six
    appends.
21. **Iteration 1 #1 / … / 14 #20 still stands, fifteenth iteration carrying it:** `loop.sh`'s
    sweep commit uses `--no-verify`, bypassing the gitleaks pre-commit hook (which ran clean on
    this iteration's commit). Needs an operator or a task of its own.
22. **Iteration 2 #2 / … / 14 #21 still stands:** duplicate checkbox state in
    `IMPLEMENTATION_PLAN.md` and `specs/.../tasks.md`, `PROMPT.md` step 9 naming only the plan.
    Ticked both by hand again, again only because the finding was written down. Fourteenth
    iteration of manual compensation for a one-line fix to step 9.
23. **Iteration 6 #6 / … / 14 #22 still stands:** `AGENTS.md`'s command table has no entry for
    `go test -tags tmux ./...` or `go vet -tags tmux ./...`, so "Test (all)" is not all.

---

## Iteration 16 — 2026-08-03 09:43

**Did:** Completed **T015**. Added `internal/session/workdir.go` (`ResolveWorkDir`,
`ErrInvalidWorkDir` plus four reason sentinels, and the unexported `underAnyRoot` /
`underRoot`) and `workdir_test.go`. The package is now 21 tests / 197 runs, up from
15 / 73. Ticked T015 in **both** `ralph/IMPLEMENTATION_PLAN.md` and `specs/.../tasks.md`.

Gate, executed not asserted:

```
go build ./...              OK
go vet ./...                OK
go vet -tags tmux ./...     OK
go test -count=1 ./...      OK (session 21 tests / 197 runs, auth, audit, config, tmuxctl)
go test -race -count=1 ./...   OK
go test -tags tmux ./...    OK (real tmux)
golangci-lint run           OK
gofmt -l . / goimports -l . empty
go.sum                      absent  ✅ zero third-party deps still holds
git status                  clean against HEAD after every probe was reverted
gitleaks (pre-commit)       1 commits scanned … no leaks found
```

**Learned (do not rediscover):**

- **The suite was green on the first run, which is the one result that means nothing.**
  Seven mutations, each killed only by the case written for it: bare `strings.HasPrefix`
  (3 subtests, in both tables), containment against the cleaned path instead of the
  resolved one (4), the `EvalSymlinks` error wrapped verbatim (the canary test), fail-open
  on unresolvable (1), no `IsAbs` (5), no `IsDir` (1), and the two checks in swapped order
  (1). Reverted each with `Edit`; `git status --porcelain` showed only the two new files
  afterwards.
- **`os.Stat` turns out to be a second gate, and it hides a fail-open.** Probe 4 (fall back
  to the cleaned path when `EvalSymlinks` errors) killed only the NUL case — a non-existent
  path and a dangling symlink are still refused, because containment passes on the cleaned
  spelling and the later `Stat` fails anyway. Only the *reason* changes. Worth knowing for
  **T028/T031**, which have the same shape: two checks that overlap look like one test
  covering both until a mutation says otherwise. The reason-per-sentinel table is what made
  the difference visible at all.
- **Iteration 15's "a rule needs a reason a test can name" applies twice here, and one of
  the two was missing on my first pass.** The containment/`IsDir` order is a claim about
  which reason reaches the audit trail, and nothing observed it until I added *a regular
  file outside the approved roots* — the case where both rules apply. Probe 7 then failed
  exactly that subtest and nothing else. **Any comment of the form "X is checked before Y"
  needs the input where both fire, or it is prose.**
- **`filepath.Clean` is textual and that is a feature, not a hole.** `<root>/repo/../repo`
  and `<root>/../code/repo` are accepted, because the rule is where a path *lands*, never
  what it was spelled with. Both are in the accepted table so a future change cannot start
  refusing traversal syntax and call it hardening.
- **The error deliberately drops the `os.PathError`.** It carries the caller's path, and
  this error is headed for `audit.Record.Reason` (free text — finding #12) and a log line;
  echoing it would let a caller put arbitrary bytes, newlines included, into the audit
  trail by naming a directory. `config.resolveRoot` *does* interpolate its paths, and that
  is correct there: those come from the operator's environment at startup, not off the
  wire. **Same rule applies to T024's prompt errors and T022's decode errors.**
- **The degenerate root `/` needed the `TrimSuffix`.** `/` is the one cleaned path that
  already ends in a separator, so `root + "/"` would be `//` and match nothing — an
  allowlist of `/` would have silently refused every path. `config` accepts `/` as a root,
  so this is reachable.
- **Roots are consumed exactly as `config` resolved them and are deliberately not
  re-resolved** (data-model.md: a swap between check and spawn is a race a caller wins).
  `TestResolveWorkDirFailsClosedOnAnUnresolvedRoot` pins the direction that breaks in — a
  legitimate path under a symlinked root is refused, never a path outside one admitted.
- **`internal/session` now imports `internal/config`** for `ApprovedRoot`. No cycle
  (`config` imports only stdlib), and it avoids a duplicate root type. First edge in that
  direction; T016/T018 will want the same.

**Left:** T016 is next (`internal/session/session.go`: the `Session` model with `Owner`,
the `starting`/`running`/`dead` states, and the in-memory store; token expiry **derived**
from `CreatedAt + 24h`, never stored). It still owes the `Owner`-type ruling flagged in
iteration 13. Then T017 (`io.ReadFull`, the per-byte-column test, no hex fixtures — see
iteration 14), T018, and T019–T042. `ValidateName` and `ResolveWorkDir` are both written
and both still unreferenced; **T022 owns wiring them into `POST /sessions`** and its test
must drive a bad name *and* a bad path, or two boundary checks ship dead.

**Findings (noticed, not fixed):**

1. **New this iteration: `ResolveWorkDir` is a create-time check with an unavoidable TOCTOU
   window.** Nothing stops the resolved directory from being renamed or replaced with a
   symlink between the check and `tmux new-session -c`. Closing it properly needs an
   `openat2(RESOLVE_BENEATH)`-style handle or a re-check inside the spawn, neither of which
   any task owns, and the fd cannot be handed to tmux anyway. The mitigation on the books is
   Principle VI's bounded lifetime and verified teardown. **Stated here rather than
   silently, because the docstring claims it and no test can.** An operator ruling would be
   welcome; my reading is that it is acceptable for a single-operator daemon whose approved
   roots are the operator's own directories.
2. **New this iteration: a rejected path is an existence oracle, weakly.** The reason
   sentinels distinguish "does not exist" from "outside the roots", so anything that renders
   a reason to a *caller* would leak whether an arbitrary host path exists.
   `contracts/http-api.md` answers 400 either way, so today this only reaches the audit
   record — the same shape as iteration 13 #2 for auth. **T020/T022 must keep the reason
   server-side**, and finding #4 below already asks for a byte-identical-body assertion.
3. **New this iteration: nothing enforces that an approved root is not a symlink at the
   moment of use.** `config` resolves at startup; if a root is *replaced* by a symlink while
   the daemon runs, every containment check silently narrows (fails closed) rather than
   widening. Recorded because the failure is invisible — legitimate creates would start
   returning 400 with no clue why. A startup-time re-stat in T032's adopt path would catch
   the common case.
4. **Iteration 15 #1 still stands:** FR-027's class admits a leading `-` while `tasks.md`
   T014 calls it hostile. Resolved in favour of the regexp (it appears five times); worth an
   operator ruling before milestone 2 renders names and milestone 3 adds rename.
5. **Iteration 15 #2 still stands, and now covers two files:** neither `ValidateName` nor
   `ResolveWorkDir` has a caller. T022 owns both.
6. **Iteration 13 #1 / 14 #3 / 15 #3 still stands:** `docs/auth-and-sessions.md`'s `Verify`
   sample is stale in two ways.
7. **Iteration 13 #2 / 14 #4 / 15 #4 still stands:** nothing stops a handler from putting
   `auth.Reason(err)` in a response body. T020 should assert the 401 body is byte-identical
   across failure modes.
8. **Iteration 12 #1 / 13 #3 / 14 #5 / 15 #5 still stands:** CI never runs `-race`
   (`.github/workflows/ci.yml:178` runs `go test ./...` only). Worth an operator doing before
   T033.
9. **Iteration 12 #2 / … / 15 #6 still stands:** three specs disagree on `Observe`'s
   signature. The code follows `tasks.md`.
10. **Iteration 12 #3 / … / 15 #7 still stands:** the replay cache is unbounded in count,
    only in age. No task owns a cap.
11. **Iteration 11 #1 / … / 15 #8 still stands:** the audit trail cannot tell clock drift
    from a forged future timestamp. T038 should decide whether to split the sentinel.
12. **Iteration 11 #2 / … / 15 #9 still stands:** nothing forces the daemon's clock to be
    monotonic or roughly right.
13. **Iteration 10 #1 / … / 15 #10 still stands:** `contracts/http-api.md` promises `400` for
    an oversize body but auth runs first. **T020 must decide.**
14. **Iteration 10 #2 / … / 15 #11 still stands:** the signature covers timestamp and body
    but not method or path, so a signed body is valid on any route.
15. **Iteration 9 #1 / … / 15 #12 still stands:** `audit.Record.Reason` can carry arbitrary
    text. T038 should pass server-authored constants. This iteration adds four more
    candidates: `ErrInvalidWorkDir`'s reasons are fixed strings carrying no caller input by
    construction, and a test enforces it.
16. **Iteration 9 #2 / … / 15 #13 still stands:** `audit.Emit`'s error has no handler yet.
    T020 owns the ruling.
17. **Iteration 8 #2 / … / 15 #14 still stands:** the loud default-root warning goes to
    stderr while audit records go to stdout. T032 owns it.
18. **Iteration 8 #1 / … / 15 #15 still stands:** `.env.example` does not exist, so the
    `.gitleaks.toml` allowlist entry for it guards nothing. T040.
19. **Iteration 7 #1 / … / 15 #16 still stands:** bidi and invisible Unicode are not stripped
    by `tmuxctl.Strip`, by design. Milestone 2 decides. Pane output only — names and now
    working directories are both closed to them.
20. **Iteration 6 #2 / … / 15 #17 still stands:** a failed `paste-buffer` leaves caller
    prompt text in a named tmux buffer.
21. **Iteration 6 #1 / … / 15 #18 still stands:** T028 will report a false failure on the
    last session — killing the only session stops the tmux server and `Has` then errors
    rather than returning false. Use `List`.
22. **Iteration 6 #3 / … / 15 #19 still stands:** `contracts/tmuxctl.md` names only
    `no server running` for the empty-server case.
23. **Iteration 14 #1 / 15 #20 still stands:** `git checkout -- <path>` and `git restore` are
    not in the permission allowlist, so `PROMPT.md` step 6's documented recovery path needs
    an approval an autonomous run cannot give. Seven probes reverted with `Edit` in reverse
    again this iteration. Also confirmed again: `set -e` with `;`-joined commands is refused
    as a multi-operation command, so the gate runs one command per call.
24. **Iteration 1 #1 / … / 15 #21 still stands, sixteenth iteration carrying it:** `loop.sh`'s
    sweep commit uses `--no-verify`, bypassing the gitleaks pre-commit hook (which ran clean
    on this iteration's commit). Needs an operator or a task of its own.
25. **Iteration 2 #2 / … / 15 #22 still stands:** duplicate checkbox state in
    `IMPLEMENTATION_PLAN.md` and `specs/.../tasks.md`, `PROMPT.md` step 9 naming only the
    plan. Ticked both by hand again, again only because the finding was written down.
    Fifteenth iteration of manual compensation for a one-line fix to step 9.
26. **Iteration 6 #6 / … / 15 #23 still stands:** `AGENTS.md`'s command table has no entry for
    `go test -tags tmux ./...` or `go vet -tags tmux ./...`, so "Test (all)" is not all.

---

## Iteration 17 — 2026-08-03 09:57

**Did:** Completed **T016**. Added `internal/session/session.go` (`Session`, `State` and its
three values, the derived-value methods, and `Store`) plus `session_test.go`. The package is
now 39 tests / 164 runs. Ticked T016 in **both** `ralph/IMPLEMENTATION_PLAN.md` and
`specs/.../tasks.md`.

Gate, executed not asserted:

```
go build ./...              OK
go vet ./...                OK
go vet -tags tmux ./...     OK
go test -count=1 ./...      OK (session 39 tests / 164 runs, auth, audit, config, tmuxctl)
go test -race -count=1 ./...   OK
go test -tags tmux ./...    OK (real tmux)
golangci-lint run           OK
gofmt -l . / goimports -l . empty
go.sum                      absent  ✅ zero third-party deps still holds
git status                  clean against HEAD after every probe was reverted
gitleaks (pre-commit)       1 commits scanned … no leaks found
```

**Decided (the ruling iterations 13–16 kept forwarding):**

- **`Session.Owner` is typed `auth.CallerID`, so `internal/session` now imports
  `internal/auth`.** `data-model.md` types it that way and `auth/caller.go`'s own doc comment
  already says "handlers already compare `session.Owner` against a `CallerID`". `plan.md`'s
  arrows show no `session → auth` edge, but the three invariants that paragraph actually
  states — `tmuxctl` imports nothing from the project, `auth` knows nothing about tmux, no
  package imports `httpapi` — all still hold, and `auth` is a leaf so there is no cycle. Same
  shape as iteration 16's `session → config` edge for `ApprovedRoot`. The alternative (a plain
  string plus a conversion at each comparison) puts a cast on the ownership check, which is
  exactly where a mistake is invisible. **Treat this as settled; do not re-litigate in T017+.**

**Learned (do not rediscover):**

- **Green on the first run again, and again it meant nothing.** Nine mutations, each killed
  only by the case written for it: owner-blind `Get` (2 subtests), the record returned
  alongside the not-found error (2), `Touch` without the forward-only guard (1), dead not
  terminal (1), `TokenHash` without `json:"-"` (3 assertions in one test), `TmuxName` built
  from `Name` (8 subtests across 2 tests), `TokenExpiry` computing its own 12h (3), `List`
  unsorted *and* owner-blind (2 tests), `Add` without validation or the duplicate guard (7),
  and `Delete` tombstoning instead of removing (3). Reverted each with `Edit`;
  `git status --porcelain` showed only the two new files afterwards.
- **The one test whose mutation is a compile break, not a failure:
  `TestStoreGetReturnsACopy`.** Making the store hold `map[string]*Session` — the change that
  test exists to catch — does not make it *fail*, it makes it *not build* (`got != (Session{})`
  stops compiling). Recorded so nobody later reads the passing test as evidence a mutation was
  run against it. The live evidence for the value-semantics claim is
  `TestStoreIsSafeForConcurrentUse` under `-race`, which does run.
- **`errcheck` in this repo has `check-blank` on.** `_, _ = st.Get(...)` is a lint failure, not
  an escape hatch — an ignored error needs a real branch. Cost one gate round trip; T020+ will
  hit the same thing wiring handlers.
- **The format-and-lint hook fights a mutation probe that orphans an import.** Deleting the
  `slices.SortFunc` call from `List` made `goimports` strip `cmp` and `slices` on the *probe*
  edit, so the revert had to restore the body first and the import block second (a
  hook-modified region needs a `Read` before the next `Edit` targets it). Probe bodies that
  drop the last use of an import are more expensive to revert than they look — prefer probes
  that swap an expression over ones that delete a call.
- **`Get` takes the owner as a parameter rather than trusting the call site.** That is a
  deliberate narrowing of what T023 has left to do: "bearer token match **and** owner match"
  still holds, but the owner half is now impossible to skip. There is no exported owner-blind
  lookup at all — **T031 (`Adopt`) and T036 (the reaper) will need an unexported one plus an
  all-owners view, and they should add those, not export `Get`'s twin.** Deliberately not
  written now: an unexported method with no production caller is dead code the `unused` linter
  flags.
- **`Store.Len()` counts every record whatever its state**, including one that went `dead` on
  its own and has not been reaped yet. **T033's cap is written against that number**, so a
  fleet of dead-but-unreaped records can refuse a create. That is the fail-closed direction,
  but T033 should say so on purpose rather than inherit it.
- **`AbsoluteLifetime` and `IdleTimeout` are constants, not config.** `data-model.md`'s Config
  table has no env var for either, and Principle VI bounds the blast radius *by construction* —
  an operator who could widen them could widen that. If T041's deployment work wants them
  tunable, that needs a spec change first.

**Left:** T017 is next (`internal/session/token.go`: 32 bytes from `crypto/rand` → 64 hex,
stored only as `sha256.Sum256`, compared with `hmac.Equal`; test asserts the plaintext is
never retained on the record and that comparison is constant-time). Iteration 14's notes
apply: use `io.ReadFull`, and **no hex-shaped fixtures** — gitleaks blocks the commit, which
is why this iteration's test IDs are built with `strings.Repeat` rather than pasted. The
`TokenHash` field, its `json:"-"` guard, and `TokenExpiry()` are already in place for it.
Then T018 (`Manager.Create`), T019–T023, and T024–T042. `ValidateName` and `ResolveWorkDir`
are both still unreferenced; **T022 owns wiring them into `POST /sessions`**.

**Findings (noticed, not fixed):**

1. **New this iteration: the store cannot tell the audit trail *why* a lookup failed.**
   `Get` collapses unknown-id and wrong-owner into one `ErrSessionNotFound` on purpose
   (FR-033), but unlike `auth`, which keeps the server-side reason reachable through
   `Reason(err)`, nothing here preserves the distinction even for the operator. Milestone 1
   has one caller so a wrong-owner request is unreachable in production; **milestone 2's
   second identity makes "someone probed another session" a thing worth seeing.** The fix
   shape already exists in `auth/caller.go` — a `denial`-style wrapper — and **T020/T038 own
   the ruling**, not this task.
2. **New this iteration: `Delete`'s hash scrub is best effort and the comment says so.**
   Overwriting the map value before `delete` reuses the same bucket slot, but Go offers no
   guarantee about when that memory is reused, and nothing zeroes copies already handed out by
   `Get`. FR-013's real guarantee is that the plaintext was never stored at all. **No test can
   assert this**, which is why it is here rather than in a comment only.
3. **New this iteration: nothing enforces that a `Session.ID` in the store came from `NewID`.**
   `Add` validates that an id is non-empty, not that it is 32 hex characters, so a future
   caller could store a record whose `TmuxName()` is not a name tmux will accept. `NewID` is
   the only producer today and T018 is the only caller planned, so this is a shape worth
   pinning in T018's test rather than a guard worth adding here.
4. **Iteration 16 #1 still stands:** `ResolveWorkDir` is a create-time check with an
   unavoidable TOCTOU window before `tmux new-session -c`. Operator ruling welcome.
5. **Iteration 16 #2 still stands:** a rejected path is a weak existence oracle; the reason
   must stay server-side. T020/T022.
6. **Iteration 16 #3 still stands:** nothing re-stats an approved root, so a root replaced by
   a symlink after startup narrows silently.
7. **Iteration 15 #1 / 16 #4 still stands:** FR-027's class admits a leading `-` while
   `tasks.md` T014 calls it hostile. Resolved in favour of the regexp; worth an operator ruling
   before milestone 2 renders names and milestone 3 adds rename.
8. **Iteration 15 #2 / 16 #5 still stands:** neither `ValidateName` nor `ResolveWorkDir` has a
   caller. T022 owns both.
9. **Iteration 13 #1 / … / 16 #6 still stands:** `docs/auth-and-sessions.md`'s `Verify` sample
   is stale in two ways.
10. **Iteration 13 #2 / … / 16 #7 still stands:** nothing stops a handler from putting
    `auth.Reason(err)` in a response body. T020 should assert the 401 body is byte-identical
    across failure modes.
11. **Iteration 12 #1 / … / 16 #8 still stands:** CI never runs `-race`
    (`.github/workflows/ci.yml:178` runs `go test ./...` only). **This iteration is the first
    to ship a mutex, so the gap now has teeth** — `TestStoreIsSafeForConcurrentUse` passes
    locally under `-race` and CI would not notice if it stopped. Worth an operator doing
    before T033.
12. **Iteration 12 #2 / … / 16 #9 still stands:** three specs disagree on `Observe`'s
    signature. The code follows `tasks.md`.
13. **Iteration 12 #3 / … / 16 #10 still stands:** the replay cache is unbounded in count,
    only in age. No task owns a cap.
14. **Iteration 11 #1 / … / 16 #11 still stands:** the audit trail cannot tell clock drift
    from a forged future timestamp. T038 should decide whether to split the sentinel.
15. **Iteration 11 #2 / … / 16 #12 still stands:** nothing forces the daemon's clock to be
    monotonic or roughly right. **`Store.Touch`'s forward-only rule is a partial mitigation**
    — a lagging read can no longer shorten a session's idle life — but the absolute deadline
    still trusts `CreatedAt` outright.
16. **Iteration 10 #1 / … / 16 #13 still stands:** `contracts/http-api.md` promises `400` for
    an oversize body but auth runs first. **T020 must decide.**
17. **Iteration 10 #2 / … / 16 #14 still stands:** the signature covers timestamp and body but
    not method or path, so a signed body is valid on any route.
18. **Iteration 9 #1 / … / 16 #15 still stands:** `audit.Record.Reason` can carry arbitrary
    text. T038 should pass server-authored constants. This iteration adds five more safe
    candidates: `ErrInvalidSession`, `ErrSessionNotFound`, `ErrSessionExists`,
    `ErrSessionDead`, and `ErrInvalidState` are fixed strings carrying no caller input —
    except that `SetState`'s message interpolates the requested state, which is server-chosen
    today and would not be if a handler ever passed a body field through.
19. **Iteration 9 #2 / … / 16 #16 still stands:** `audit.Emit`'s error has no handler yet.
    T020 owns the ruling.
20. **Iteration 8 #2 / … / 16 #17 still stands:** the loud default-root warning goes to stderr
    while audit records go to stdout. T032 owns it.
21. **Iteration 8 #1 / … / 16 #18 still stands:** `.env.example` does not exist, so the
    `.gitleaks.toml` allowlist entry for it guards nothing. T040.
22. **Iteration 7 #1 / … / 16 #19 still stands:** bidi and invisible Unicode are not stripped
    by `tmuxctl.Strip`, by design. Milestone 2 decides. Pane output only.
23. **Iteration 6 #2 / … / 16 #20 still stands:** a failed `paste-buffer` leaves caller prompt
    text in a named tmux buffer.
24. **Iteration 6 #1 / … / 16 #21 still stands:** T028 will report a false failure on the last
    session — killing the only session stops the tmux server and `Has` then errors rather than
    returning false. Use `List`.
25. **Iteration 6 #3 / … / 16 #22 still stands:** `contracts/tmuxctl.md` names only
    `no server running` for the empty-server case.
26. **Iteration 14 #1 / … / 16 #23 still stands:** `git checkout -- <path>` and `git restore`
    are not in the permission allowlist, so `PROMPT.md` step 6's documented recovery path needs
    an approval an autonomous run cannot give. Nine probes reverted with `Edit` in reverse
    again this iteration, one of them needing two reverts because the formatter had stripped an
    import in between. Also confirmed again: **a heredoc past a few dozen lines aborts the Bash
    parser** — this entry went in as two `Edit` appends instead, which worked first time and is
    the better tool for the job.
27. **Iteration 1 #1 / … / 16 #24 still stands, seventeenth iteration carrying it:** `loop.sh`'s
    sweep commit uses `--no-verify`, bypassing the gitleaks pre-commit hook (which ran clean on
    this iteration's commit). Needs an operator or a task of its own.
28. **Iteration 2 #2 / … / 16 #25 still stands:** duplicate checkbox state in
    `IMPLEMENTATION_PLAN.md` and `specs/.../tasks.md`, `PROMPT.md` step 9 naming only the plan.
    Ticked both by hand again, again only because the finding was written down. Sixteenth
    iteration of manual compensation for a one-line fix to step 9.
29. **Iteration 6 #6 / … / 16 #26 still stands:** `AGENTS.md`'s command table has no entry for
    `go test -tags tmux ./...` or `go vet -tags tmux ./...`, so "Test (all)" is not all.

---

## Iteration 18 — 2026-08-03 10:07

**Did:** Completed **T017**. Added `internal/session/token.go` (`NewToken`, `newTokenFrom`,
`hashToken`, `Session.TokenMatches`, `Session.hasToken`) plus `token_test.go`. The package is
now 54 tests / 197 runs. Ticked T017 in **both** `ralph/IMPLEMENTATION_PLAN.md` and
`specs/.../tasks.md`.

Gate, executed not asserted:

```
go build ./...              OK
go vet ./...                OK
go vet -tags tmux ./...     OK
go test -count=1 ./...      OK (session, auth, audit, config, tmuxctl)
go test -race -count=1 ./...   OK
go test -tags tmux ./...    OK (real tmux)
golangci-lint run           OK
gofmt -l . / goimports -l . empty
go.sum                      absent  ✅ zero third-party deps still holds
git status                  only the two new files after every probe was reverted
gitleaks (pre-commit)       1 commits scanned … no leaks found
```

**Decided (write these down, they are not re-derivable from the code alone):**

- **The hash is taken over the *encoded* token, not the 32 bytes behind it.**
  `hex.DecodeString` accepts both cases, so hashing decoded bytes would give every
  credential an uppercase twin — two strings that open the same session, one of which was
  never issued and appears in no audit record. `data-model.md` says `sha256.Sum256(token)`
  where `token` is the transported value, so this follows the document; the reason is
  written into the docstring because the sample in `docs/auth-and-sessions.md` hashes raw
  bytes and a future reader will find that first. **A test pins it**
  (`TestHashTokenIsOverTheEncodedForm`).
- **`NewToken` returns `(plaintext, hash, error)` and writes to nothing.** FR-013's "never
  stored" is a property of the call graph, not of a comment: there is no setter, no field
  to assign, and no re-issue path. T022 is the only planned caller and it puts the hash on
  the record and the plaintext in the response.

**Learned (do not rediscover):**

- **Green on the first run, again meaning nothing until probed.** Nine mutations, each
  killed only by the case written for it: `hmac.Equal` → `==` (the AST test, and *only* the
  AST test — see below), case-folded hashing (2 tests), `io.ReadFull` → `Read` (2), a
  non-zero hash returned beside an error (3 subtests), a partial token returned beside an
  error (3), uppercase encoding plus a mismatched hash (5 tests), `TrimSpace` on the
  presented token (2 subtests), `hasToken` → `return true` (1). Reverted each with `Edit`;
  `git status --porcelain` showed only the two new files afterwards.
- **`hmac.Equal` versus `==` on two `[32]byte` values is behaviourally identical, so no
  input can kill that mutation.** The assertion that can is a source-level one:
  `TestTokenMatchesComparesInConstantTime` parses `token.go` with `go/parser`, finds the
  `TokenMatches` declaration, and asserts it calls `hmac.Equal` and contains no `==`/`!=` at
  all. That is why the zero-hash guard lives in its own `hasToken` method — keeping
  `TokenMatches` free of comparison operators makes the assertion a flat "none", with no
  allowlist to maintain. Import `go/token` as `gotoken` in that file: `token` is the
  obvious local name for the thing under test.
- **A timing measurement was deliberately not written.** It would be flaky on a shared
  runner and prove nothing on a fast pass. It would also be measuring the wrong thing:
  hashing first avalanches, so where an early-exit compare stops says nothing about where
  the guess was wrong. The constant-time compare is still there — see the docstring — but
  the property that can actually be enforced is "the code calls `hmac.Equal`".
- **`TokenMatches`'s zero-hash guard is itself unkillable, and the linter does not catch it
  either.** Deleting `if !s.hasToken() { return false }` leaves every test passing (no
  preimage of the zero hash exists to present) *and* `golangci-lint run` clean — `unused`
  counts the test's own call to `hasToken` as a use. Recorded so nobody later reads the
  passing suite as evidence the guard was exercised. What is pinned is `hasToken` itself,
  in both directions.
- **The formatter strips an orphaned import mid-probe, so reverts go body-first,
  import-second.** Swapping `hmac.Equal(...)` for `==` made `goimports` delete
  `crypto/hmac` on the probe edit; restoring the body brought the import back on its own
  (the hook re-runs on the revert), and `go build ./...` was the check that said so.
  Iteration 17 hit the same thing with `slices`/`cmp`.
- **No hex-shaped literal is in either new file.** Every expectation is built with
  `strings.Repeat`, `bytes.Repeat`, or `fmt.Sprintf("%x", …)`, and failure messages print
  lengths rather than values. gitleaks reads a 64-character hex string as a credential and
  would block the commit — a 64-char one more readily than the 32-char IDs of iteration 8.
  Test failure output is a place a token would otherwise land in CI logs.
- **`Session.TokenMatches` is a method on the record, not a free function over a hash.**
  T023 wires "bearer token match **and** owner match" together; the owner half is already
  impossible to skip (`Store.Get` takes it as a parameter), and this makes the token half
  reachable only from a record that has already passed it.

**Left:** T018 is next (`internal/session/manager.go`: `Manager.Create` — tmux session
`crswd-<id>` in the validated directory, set `@crswd-managed` and `@crswd-owner`, then send
`claude --dangerously-skip-permissions` as keys; test asserts call order and that the target
derives only from the ID). `Session.TmuxName`/`SessionTarget`/`PaneTarget` and
`tmuxctl.Fake` are both already in place for it. Then T019–T023 and T024–T042. `NewToken`
has no caller yet — **T022 owns wiring it into `POST /sessions`**, alongside `ValidateName`
and `ResolveWorkDir`, which are still unreferenced too.

**Findings (noticed, not fixed):**

1. **New this iteration: `Store.Add` does not require a `TokenHash`, so a record with no
   credential is storable.** `TokenMatches` fails closed on it, but nothing stops a future
   caller from adding a session nobody can drive — including its owner. T031 (`Adopt`) is
   the likely place to get this wrong, because it issues fresh tokens for sessions that
   already exist. Adding `TokenHash` to `validate()` would be the structural fix; it was not
   done here because it changes T016's code and its test fixtures, which is not this task.
2. **New this iteration: nothing bounds the length of a presented token before it is
   hashed.** `TokenMatches` will SHA-256 whatever string it is handed, so a caller sending a
   megabyte `Authorization` header buys a megabyte hash on the auth path. The body limit
   (`CRSW_MAX_BODY_BYTES`) does not cover headers, and Go's default header cap (1 MB) is
   what is left. **T023 should reject anything that is not exactly `TokenLen` before
   hashing** — a length check leaks nothing here, since the length is public.
3. **Iteration 17 #1 still stands:** the store cannot tell the audit trail *why* a lookup
   failed. Milestone 2's second identity makes "someone probed another session" worth
   seeing. T020/T038 own the ruling.
4. **Iteration 17 #2 still stands:** `Delete`'s hash scrub is best effort; no test can
   assert it. FR-013's real guarantee — now implemented — is that the plaintext was never
   stored at all.
5. **Iteration 17 #3 still stands:** nothing enforces that a `Session.ID` in the store came
   from `NewID`. Worth pinning in T018's test.
6. **Iteration 16 #1 / 17 #4 still stands:** `ResolveWorkDir` is a create-time check with an
   unavoidable TOCTOU window before `tmux new-session -c`. Operator ruling welcome.
7. **Iteration 16 #2 / 17 #5 still stands:** a rejected path is a weak existence oracle; the
   reason must stay server-side. T020/T022.
8. **Iteration 16 #3 / 17 #6 still stands:** nothing re-stats an approved root, so a root
   replaced by a symlink after startup narrows silently.
9. **Iteration 15 #1 / … / 17 #7 still stands:** FR-027's class admits a leading `-` while
   `tasks.md` T014 calls it hostile. Resolved in favour of the regexp; worth an operator
   ruling before milestone 2 renders names and milestone 3 adds rename.
10. **Iteration 15 #2 / 17 #8 still stands, and now covers three files:** `ValidateName`,
    `ResolveWorkDir`, and now `NewToken` have no caller. T022 owns all three.
11. **Iteration 13 #1 / … / 17 #9 still stands, and this iteration adds a third way:**
    `docs/auth-and-sessions.md`'s samples are stale — the `Verify` one in two ways, and the
    layer-3 sample hashes the raw token bytes (`sha256.Sum256(tok)` over a `[]byte`) where
    the contract and this implementation hash the transported hex string. The sample also
    assigns `sess.TokenHash` directly, which is the shape FR-013 exists to prevent.
12. **Iteration 13 #2 / … / 17 #10 still stands:** nothing stops a handler from putting
    `auth.Reason(err)` in a response body. T020 should assert the 401 body is byte-identical
    across failure modes.
13. **Iteration 12 #1 / … / 17 #11 still stands:** CI never runs `-race`
    (`.github/workflows/ci.yml:178` runs `go test ./...` only). Worth an operator doing
    before T033.
14. **Iteration 12 #2 / … / 17 #12 still stands:** three specs disagree on `Observe`'s
    signature. The code follows `tasks.md`.
15. **Iteration 12 #3 / … / 17 #13 still stands:** the replay cache is unbounded in count,
    only in age. No task owns a cap.
16. **Iteration 11 #1 / … / 17 #14 still stands:** the audit trail cannot tell clock drift
    from a forged future timestamp. T038 should decide whether to split the sentinel.
17. **Iteration 11 #2 / … / 17 #15 still stands:** nothing forces the daemon's clock to be
    monotonic or roughly right. The token's expiry is `TokenExpiry()` — i.e. `CreatedAt +
    24h` — so a clock that jumps backwards extends a credential's life, not only a
    session's.
18. **Iteration 10 #1 / … / 17 #16 still stands:** `contracts/http-api.md` promises `400` for
    an oversize body but auth runs first. **T020 must decide.**
19. **Iteration 10 #2 / … / 17 #17 still stands:** the signature covers timestamp and body
    but not method or path, so a signed body is valid on any route. **The bearer token now
    narrows this** — a signed body replayed onto another session's route still fails the
    token check — but it does not close it for the caller's own sessions.
20. **Iteration 9 #1 / … / 17 #18 still stands:** `audit.Record.Reason` can carry arbitrary
    text. T038 should pass server-authored constants.
21. **Iteration 9 #2 / … / 17 #19 still stands:** `audit.Emit`'s error has no handler yet.
    T020 owns the ruling.
22. **Iteration 8 #2 / … / 17 #20 still stands:** the loud default-root warning goes to
    stderr while audit records go to stdout. T032 owns it.
23. **Iteration 8 #1 / … / 17 #21 still stands:** `.env.example` does not exist, so the
    `.gitleaks.toml` allowlist entry for it guards nothing. T040.
24. **Iteration 7 #1 / … / 17 #22 still stands:** bidi and invisible Unicode are not stripped
    by `tmuxctl.Strip`, by design. Milestone 2 decides. Pane output only.
25. **Iteration 6 #2 / … / 17 #23 still stands:** a failed `paste-buffer` leaves caller
    prompt text in a named tmux buffer.
26. **Iteration 6 #1 / … / 17 #24 still stands:** T028 will report a false failure on the
    last session — killing the only session stops the tmux server and `Has` then errors
    rather than returning false. Use `List`.
27. **Iteration 6 #3 / … / 17 #25 still stands:** `contracts/tmuxctl.md` names only
    `no server running` for the empty-server case.
28. **Iteration 14 #1 / … / 17 #26 still stands:** `git checkout -- <path>` and `git restore`
    are not in the permission allowlist, so `PROMPT.md` step 6's documented recovery path
    needs an approval an autonomous run cannot give. Nine probes reverted with `Edit` in
    reverse again this iteration. Also new this iteration: **`git config core.hooksPath`
    (a read) is refused as part of a `;`-joined command** — one command per call, always.
29. **Iteration 1 #1 / … / 17 #27 still stands, eighteenth iteration carrying it:**
    `loop.sh`'s sweep commit uses `--no-verify`, bypassing the gitleaks pre-commit hook
    (which ran clean on this iteration's commit). Needs an operator or a task of its own.
30. **Iteration 2 #2 / … / 17 #28 still stands:** duplicate checkbox state in
    `IMPLEMENTATION_PLAN.md` and `specs/.../tasks.md`, `PROMPT.md` step 9 naming only the
    plan. Ticked both by hand again, again only because the finding was written down.
    Seventeenth iteration of manual compensation for a one-line fix to step 9.
31. **Iteration 6 #6 / … / 17 #29 still stands:** `AGENTS.md`'s command table has no entry
    for `go test -tags tmux ./...` or `go vet -tags tmux ./...`, so "Test (all)" is not all.

---

## Iteration 19 — 2026-08-03 10:26

**Did:** Completed **T018**. Added `internal/session/manager.go` (`Manager`, `NewManager`,
`NewManagerWithClock`, `Clock`, `CreateRequest`, `Create`, `start`, `rollback`) plus
`manager_test.go`. Moved the two tmux option names into `internal/tmuxctl` as
`OptionManaged`/`OptionOwner`/`OptionManagedValue` and used them from `fake.go`'s
`argvList`, `List`, and `Seed`. The session package is now 64 tests / 227 runs. Ticked T018
in **both** `ralph/IMPLEMENTATION_PLAN.md` and `specs/.../tasks.md`.

Gate, executed not asserted:

```
go build ./...              OK
go vet ./...                OK
go vet -tags tmux ./...     OK
go test -count=1 ./...      OK (session, auth, audit, config, tmuxctl)
go test -race -count=1 ./internal/session ./internal/tmuxctl   OK
go test -tags tmux ./...    OK (real tmux)
golangci-lint run           OK
gofmt -l . / goimports -l . empty
go.sum                      absent  ✅ zero third-party deps still holds
git status                  only the four files after every probe was reverted
gitleaks (pre-commit)       1 commits scanned … no leaks found
```

**Decided (write these down, they are not re-derivable from the code alone):**

- **The record is claimed before the tmux session exists.** `Store.Add` runs before
  `tmux new-session`, not after. A record without a session is a record whose endpoints
  fail; a session without a record is a live unsandboxed shell that no ownership check,
  no cap, and no reaper can see. Only one of those two is survivable (Principle VI), so
  the ID is taken first. **A test pins the ordering** — moving `Add` after `start` makes
  `TestCreateKeepsTheRecordWhenTeardownCannotBeVerified` fail, because there is then no
  record left to keep.
- **A create that fails after `new-session` kills *and verifies*, and keeps the record
  when it cannot.** `rollback` asks `Kill`, then `Has`. Confirmed gone → drop the record
  and return the original failure. Still present, or `Has` errored → keep the record and
  wrap `ErrOrphanedSession`. Keeping it is the fail-closed direction: adoption runs at
  startup only (T031/T032), so a live session the *running* daemon has forgotten is
  forgotten for good, whereas a record for an already-dead session is a phantom the
  reaper resolves. The caller holds no token on either path — the plaintext dies with the
  failed response — so a retained record is drivable by nobody and reapable by the daemon.
- **`Kill`'s error is folded into the result rather than returned.** A `New` that failed
  may have left nothing to kill, so `can't find session` is the *expected* answer there;
  only `Has` decides. The error is still carried, via `errors.Join`, on the orphan path.
- **`ErrMissingOwner` earns its own sentinel even though `Store.Add`'s `validate` also
  refuses an empty owner.** An empty `Owner` means the call skipped authentication, not
  that a caller omitted a field, and T022 needs to tell that (a 500-shaped bug) from a
  malformed record. Probed: deleting the guard still fails the test, but with
  `invalid session record: an owner is required` — the wrong story for a handler to act on.
- **`session.Clock` is declared in `internal/session`, not imported from `internal/auth`.**
  The shapes are identical and the meanings are not: auth's clock decides whether a
  signature is inside its 300s window, this one decides when a session dies. Importing
  one into the other would make them a single setting. Go's interfaces are structural, so
  one stopped clock in a test satisfies both.
- **The two option names moved into `tmuxctl`.** `argvList` interpolates `@crswd-managed`
  into the `-F` format string, and T031 matches on what T018 wrote. The name written and
  the name matched must be one string, and `session` cannot export a constant into
  `tmuxctl` — the import runs the other way. Three call sites in `fake.go` now read the
  constant; `exec.go` needed no change because the argv builders already live in `fake.go`.

**Learned (do not rediscover):**

- **Green on the first run, again meaning nothing until probed.** Nine mutations, each
  killed: swapped `SetOption` order, dropped `Enter`, `New` addressed by
  `tmuxNamePrefix+s.Name` (caught, but *incidentally* — the fake then refuses the
  follow-up calls), **every** target built from the name consistently (caught directly, by
  both the argv test and the derives-from-the-ID test), the caller's unresolved `WorkDir`
  used instead of `ResolveWorkDir`'s answer, `rollback` removed entirely, `rollback`
  deleting unconditionally, `if present` alone (i.e. "could not ask" read as "it is
  gone"), `LastActivity` a minute ahead of `CreatedAt`, the empty-roots constructor guard,
  and a one-character typo in `OptionManaged`. Reverted each with `Edit`;
  `git status --porcelain` showed only the four files afterwards.
- **Mutating one call's target is a weaker probe than mutating them all.** Changing only
  `New`'s name makes the fake return `can't find session` for the next call, so tests fail
  for the wrong reason. The probe that actually tests the assertion is `name :=
  tmuxNamePrefix + s.Name` at the top of `start`, where the fake stays internally
  consistent and only the argv assertions can catch it. Worth remembering for T024/T025.
- **`Session` is comparable, so `stored != *s` is a whole-record assertion.** Every field
  is a string, `time.Time`, `[32]byte`, or `bool`. No `reflect.DeepEqual` needed, and a
  future field of map or slice type would break the build here rather than silently
  widening what the test ignores.
- **The happy-path argv is spelled out literally in the test, not built from
  `tmuxctl.SessionTarget`/`PaneTarget`.** Reusing the builders would assert only that
  `Create` called them. `internal/tmuxctl/exec_test.go` already pins the same strings from
  the other side, which is why the `OptionManaged` typo probe failed in three places.
- **`t.TempDir()` per subtest is what makes the "no tmux command ran" assertion honest.**
  The validation table builds a fresh `managerFixture` (and so a fresh `Fake`) per case, so
  `len(Calls()) == 0` means *this* request executed nothing rather than that some earlier
  case had not yet run. `newWorkDirFixture` is reused unchanged from T015 — the filesystem
  half is not fakeable, because `EvalSymlinks` is the thing under test.
- **No hex-shaped literal is in the new test file.** IDs and tokens are only ever compared
  to `idShape`/`tokenShape` (T013/T017's package-level regexps) or matched with
  `TokenMatches`; every failure message prints a length. gitleaks blocks a 64-character hex
  string, and a `t.Errorf` is a place a token would otherwise reach CI logs.

**Left:** T019 is next (`internal/httpapi/server.go`: `ServeMux` with Go 1.22
method+wildcard patterns, read/write/idle timeouts, and a startup assertion that the
resolved listen address is loopback). Then T020–T023 close US1, and T024–T042 follow.
`Manager.Create` has no caller yet — **T022 owns wiring it into `POST /sessions`**, which
is also where `ValidateName`, `ResolveWorkDir`, and `NewToken` finally get theirs.
`Manager.Destroy` (T028) and `Manager.Adopt` (T031) will extend this file; both can reuse
`rollback`'s kill-then-verify shape, and T028 should read finding 6 below before doing so.

**Findings (noticed, not fixed):**

1. **New this iteration: the concurrent-session cap will not be enforceable by checking
   `Store.Len()` from `Create`.** The record is added inside `Store.Add`'s lock, so a
   check-then-`Add` in `Create` is a check-then-act race — two creates at the boundary both
   pass. **T033 should push the cap *into* the store** (an `AddCapped(s, max)` that counts
   and inserts in one critical section), not add an `if m.store.Len() >= max` to `Create`.
2. **New this iteration: `rollback`'s verification uses `Has`, which errors rather than
   returning false when the session being killed was the tmux server's last one**
   (iteration 6 #1, still open below). On a host where crswd's session is the only one,
   every rollback therefore takes the orphan path and retains a record for a session that
   really is gone. It fails in the safe direction and the reaper resolves it, but T028
   switching to `List` should switch this call with it — they are the same question.
3. **New this iteration: the Claude start command races the login shell's own startup.**
   `send-keys` lands in the pty whenever tmux gets there; a shell that has not finished
   sourcing its rc files yet will still see the bytes through the line discipline, but a
   shell that prints a prompt *after* reading will swallow them. The fake cannot show this.
   **T042's quickstart run against real tmux is where it appears** — if `claude` is not
   running in a fresh window, this is why.
4. **New this iteration: a `crswd-`-prefixed session that is missing `@crswd-managed`
   leaks forever.** `Create` sets the option immediately after `new-session`, and a failure
   in between is rolled back — but if that rollback cannot be verified, what survives is an
   unmarked session. FR-022 tells T031 not to adopt lookalikes, and nothing else ever looks
   at unmarked sessions, so it is never reaped. Options: adopt-and-destroy anything
   matching the prefix but missing the marker, or log it loudly at startup. **T031 owns the
   ruling** and should not silently ignore the prefix.
5. **Iteration 18 #1 still stands:** `Store.Add` does not require a `TokenHash`, so a
   record with no credential is storable. `Create` always supplies one, so the gap is now
   only reachable from T031's adoption path.
6. **Iteration 18 #2 still stands:** nothing bounds the length of a presented token before
   it is hashed. **T023 should reject anything that is not exactly `TokenLen`.**
7. **Iteration 17 #1 / 18 #3 still stands:** the store cannot tell the audit trail *why* a
   lookup failed. T020/T038 own the ruling.
8. **Iteration 17 #2 / 18 #4 still stands:** `Delete`'s hash scrub is best effort; no test
   can assert it.
9. **Iteration 17 #3 / 18 #5 is now partly answered:** nothing enforces that a
   `Session.ID` in the store came from `NewID`, but every ID `Create` puts there does, and
   `TestCreateIssuesADistinctIdentityEachTime` pins the shape and the uniqueness of eight
   consecutive ones. A record `Add`ed directly by a future caller is still unchecked.
10. **Iteration 16 #1 / … / 18 #6 still stands:** `ResolveWorkDir` is a create-time check
    with an unavoidable TOCTOU window before `tmux new-session -c`, and `Create` is now the
    code that opens it. Operator ruling welcome.
11. **Iteration 16 #2 / … / 18 #7 still stands:** a rejected path is a weak existence
    oracle; the reason must stay server-side. T020/T022.
12. **Iteration 16 #3 / … / 18 #8 still stands:** nothing re-stats an approved root, so a
    root replaced by a symlink after startup narrows silently.
13. **Iteration 15 #1 / … / 18 #9 still stands:** FR-027's class admits a leading `-` while
    `tasks.md` T014 calls it hostile.
14. **Iteration 15 #2 / 18 #10 still stands, and now covers four files:** `ValidateName`,
    `ResolveWorkDir`, `NewToken`, and now `Manager.Create` have no caller. T022 owns all.
15. **Iteration 13 #1 / … / 18 #11 still stands:** `docs/auth-and-sessions.md`'s samples
    are stale in three ways.
16. **Iteration 13 #2 / … / 18 #12 still stands:** nothing stops a handler from putting
    `auth.Reason(err)` in a response body. T020.
17. **Iteration 12 #1 / … / 18 #13 still stands:** CI never runs `-race`
    (`.github/workflows/ci.yml:178`). Worth an operator doing before T033 — finding 1 above
    is exactly the kind of bug it would catch.
18. **Iteration 12 #2 / … / 18 #14 still stands:** three specs disagree on `Observe`'s
    signature.
19. **Iteration 12 #3 / … / 18 #15 still stands:** the replay cache is unbounded in count.
20. **Iteration 11 #1 / … / 18 #16 still stands:** the audit trail cannot tell clock drift
    from a forged future timestamp. T038.
21. **Iteration 11 #2 / … / 18 #17 still stands:** nothing forces the daemon's clock to be
    monotonic or roughly right, and `Create` now stamps `CreatedAt` from it.
22. **Iteration 10 #1 / … / 18 #18 still stands:** `contracts/http-api.md` promises `400`
    for an oversize body but auth runs first. **T020 must decide.**
23. **Iteration 10 #2 / … / 18 #19 still stands:** the signature covers timestamp and body
    but not method or path.
24. **Iteration 9 #1 / … / 18 #20 still stands:** `audit.Record.Reason` can carry arbitrary
    text. T038.
25. **Iteration 9 #2 / … / 18 #21 still stands:** `audit.Emit`'s error has no handler yet.
26. **Iteration 8 #2 / … / 18 #22 still stands:** the loud default-root warning goes to
    stderr while audit records go to stdout. T032.
27. **Iteration 8 #1 / … / 18 #23 still stands:** `.env.example` does not exist. T040.
28. **Iteration 7 #1 / … / 18 #24 still stands:** bidi and invisible Unicode are not
    stripped by `tmuxctl.Strip`, by design.
29. **Iteration 6 #2 / … / 18 #25 still stands:** a failed `paste-buffer` leaves caller
    prompt text in a named tmux buffer.
30. **Iteration 6 #1 / … / 18 #26 still stands, and finding 2 above is its first real
    consequence:** killing the only session stops the tmux server and `Has` then errors
    rather than returning false. Use `List`.
31. **Iteration 6 #3 / … / 18 #27 still stands:** `contracts/tmuxctl.md` names only
    `no server running` for the empty-server case.
32. **Iteration 14 #1 / … / 18 #28 still stands:** `git checkout -- <path>` and
    `git restore` are not in the permission allowlist, so `PROMPT.md` step 6's documented
    recovery path needs an approval an autonomous run cannot give. Nine probes reverted
    with `Edit` in reverse again. `git config core.hooksPath` (a read) was refused again
    this iteration, on its own and not `;`-joined — it is the command, not the joining.
33. **Iteration 1 #1 / … / 18 #29 still stands, nineteenth iteration carrying it:**
    `loop.sh`'s sweep commit uses `--no-verify`, bypassing the gitleaks pre-commit hook
    (which ran clean on this iteration's commit).
34. **Iteration 2 #2 / … / 18 #30 still stands:** duplicate checkbox state in
    `IMPLEMENTATION_PLAN.md` and `specs/.../tasks.md`, `PROMPT.md` step 9 naming only the
    plan. Ticked both by hand again, again only because the finding was written down.
    Eighteenth iteration of manual compensation for a one-line fix to step 9.
35. **Iteration 6 #6 / … / 18 #31 still stands:** `AGENTS.md`'s command table has no entry
    for `go test -tags tmux ./...` or `go vet -tags tmux ./...`, so "Test (all)" is not all.

---

## Iteration 20 — 2026-08-03 10:39

**Did:** Completed **T019**. Added `internal/httpapi/server.go` (`Route`, the `routes` table,
`Server`, `New`/`newServer`, `handle`, `Routes`, `ServeHTTP`, `Listen`, `Addr`, `Serve`,
`Close`, `notImplemented`, `assertLoopbackAddress`, `assertLoopbackAddr`, `ErrNotLoopback`)
plus `server_test.go` — 17 tests / 29 runs. First code in the package that becomes the
daemon's only external surface. Ticked T019 in **both** `ralph/IMPLEMENTATION_PLAN.md` and
`specs/.../tasks.md`.

Gate, executed not asserted:

```
go build ./...              OK
go vet ./...                OK
go vet -tags tmux ./...     OK
go test -count=1 ./...      OK (httpapi, session, auth, audit, config, tmuxctl)
go test -race -count=1 ./internal/httpapi ./internal/session   OK
go test -tags tmux ./...    OK (real tmux)
golangci-lint run           OK
gofmt -l . / goimports -l . empty
go.sum                      absent  ✅ zero third-party deps still holds
git status                  only the two new files after every probe was reverted
gitleaks (pre-commit)       1 commits scanned … no leaks found
```

**Decided (write these down, they are not re-derivable from the code alone):**

- **The route table is declared once and `handle` is the only path to the mux.** FR-006
  makes the six operations a closed set, so they are a package-level `routes` slice and
  `newServer` loops over it. `handle` is deliberately the single choke point: **T020 wraps
  the auth middleware around `h` on that one line**, which is what makes "no route is
  exempt" a property of the wiring rather than a rule six call sites have to remember.
- **`Routes()` reports what was *registered*, not what was declared.** They are different
  claims, and T020's test is required to sweep the router rather than a hand-written list.
  `handle` appends as it registers, so the two cannot diverge; the slice is `slices.Clone`d
  because a caller holding the backing array could shorten the very list a test iterates.
- **The stubs answer `501` with no body, and both halves are deliberate.** A stub already
  returning the uniform `401` would let T020's "every registered route refuses an
  unauthenticated request" sweep pass green **with no middleware in the tree at all** — the
  one thing that sweep exists to catch. And the JSON error bodies belong to the tasks that
  own the responses (T020's `401`, T023's `404`); wiring does not get to invent them, so
  this task ships no JSON writer. **T022 owns the first one.**
- **FR-005 is now checked three times, and only the third is evidence.** `config.loadListen`
  refuses a non-loopback host; `newServer` refuses it again, because a `Config` is a struct
  and a test, a future caller, or a reordered startup can produce one that never went
  through `Load`; and `Listen` checks `ln.Addr()` — what the kernel actually bound — then
  **closes** the listener rather than serving on it. The socket already exists by then,
  which is why the close is not optional and is asserted.
- **Timeout values are chosen here, not specified anywhere.** `spec.md`, `plan.md`, and
  `data-model.md` all say "timeouts" and fix no numbers, so: header 5s (which also closes
  the Slowloris class, gosec G112), read 15s, write 30s, idle 60s. Every handler in this
  milestone is a tmux exec or an in-memory read, so these are far longer than any real
  request. **Milestone 2's SSE cannot live under a 30s `WriteTimeout`** and needs a
  per-route answer rather than this value being raised for everything.
- **`MaxHeaderBytes` is set to 16 KiB, well under net/http's 1 MiB default.** This is a
  partial answer to iteration 18 #2: a header is read (and, for the bearer token, hashed)
  before any route can decide it was nonsense, and `CRSW_MAX_BODY_BYTES` covers the body
  only. It bounds the work; it does not replace T023's length check on the token itself.
- **`Close` only — there is no `Shutdown`.** T037 owns graceful shutdown and needs a
  context this cannot take. `Close` drops in-flight requests and is what the tests use.

**Learned (do not rediscover):**

- **Green on the first run, again meaning nothing until probed.** Eleven mutations, each
  killed by the case written for it: the post-bind assertion deleted (4 subtests), the
  configured-address check deleted (9), an unrecognised address type read as loopback (1),
  `WriteTimeout` dropped (1), a seventh route added (2 — the exact-set test *and* the
  "nothing outside the contract is served" sweep), a contract route dropped (1),
  `Routes()` returning the backing slice (1), the refused listener kept and not closed
  (2 assertions, independently), `Handler` left nil so the server would fall back to
  `http.DefaultServeMux` (2), the method dropped from every registration (see below), and
  one route registered under `PATCH` instead of `DELETE` (2). Reverted each with `Edit`;
  `git status --porcelain` showed only the two new files afterwards.
- **Dropping the method from *every* registration panics instead of failing an
  assertion** — `ServeMux` refuses two patterns that match the same requests, and
  `POST /sessions` + `GET /sessions` both collapse to `/sessions`. Caught, but
  incidentally, exactly as iteration 19 warned. The probe that actually tests the
  assertion is **one** route registered under the wrong method, where the mux stays
  consistent and only the route-table and wrong-verb tests can catch it.
- **`http.Server.Handler` left nil is the quietest routing bug available:** net/http then
  serves `http.DefaultServeMux`, which any imported package can register onto —
  `net/http/pprof` does it from an import with no other effect, which is precisely an
  unauthenticated route on a daemon whose route set is supposed to be closed.
  `TestTheServerHasItsOwnHandler` pins it.
- **ServeMux answers an unregistered path with `404` and a registered path under an
  unregistered method with `405`, both `text/plain`.** See finding 1 — the contract says
  every response is JSON, and neither of those is.
- **`errcheck` with `check-blank: true` bans `_ = s.Serve()` in a test goroutine.** Send
  the error to a buffered channel and assert it instead — which is better anyway, because
  it also pins that `Serve` returns `nil` after a deliberate `Close` rather than
  `http.ErrServerClosed`. Same rule means a test that makes a real request must check
  `resp.Body.Close()`'s error (`bodyclose`, plus `exclude-use-default: false`).
- **The formatter deleted an orphaned import mid-probe again** — `slices`, when `Routes`
  stopped cloning. Restoring the body brought it back on the hook's re-run, and
  `go build ./...` is what said so. Iterations 17, 18, and 19 all hit this; it is now
  simply how probing works in this repo.
- **`t.Parallel()` everywhere, including the test that binds a real socket.** The fixture
  asks for `127.0.0.1:0` so the kernel picks the port; nothing here can collide with a
  real crswd or with another test. `Addr()` is what the test reads back, never the
  configured string.
- **No secret-shaped literal is in the new test file.** `testConfig` leaves
  `SharedSecret` nil — nothing in this file authenticates — and the one `config.LoadFrom`
  case spells its secret in words, as iteration 8 established.

**Left:** T020 is next (`internal/httpapi/middleware.go`: authentication on **every**
registered route, one audit record per request, uniform `401`, with a test that iterates
`Server.Routes()` rather than a hand-written list). It wraps `h` inside `handle` — that is
the whole seam, and it is one line. T020 also inherits three rulings the notebook has been
carrying for it: findings 2, 17, and 22 below. Then T021 (decoder), T022 (`POST /sessions`,
which finally gives `ValidateName`, `ResolveWorkDir`, `NewToken`, and `Manager.Create` their
first caller), and T023 close US1. T024–T042 follow.

**Findings (noticed, not fixed):**

1. **New this iteration: the mux's own `404` and `405` responses are `text/plain`, and
   `contracts/http-api.md` says every response is `application/json`.** An unregistered
   path gets `404`; a registered path under an unregistered method gets `405` with an
   `Allow` header, which also discloses that the path is in the contract (harmless — the
   contract is published — but it is a different answer for a real route than for a
   fictional one). **T020 owns uniform responses and should decide**: a catch-all `/`
   handler emitting the JSON `404` would fix the content type and collapse `405` into
   `404`, at the cost of losing `Allow`. Not done here: this task owns wiring, and
   inventing an error body would collide with T020's and T023's.
2. **New this iteration: the six routes are registered and unauthenticated until T020.**
   Nothing imports `httpapi` yet (`cmd/crswd` gets its wiring in T032) so nothing is
   exposed, and the stubs execute nothing — but the window exists in-tree, and it closes
   only when the middleware lands. **T020 is not optional before T032.**
3. **New this iteration: none of `docs/security.md`'s "Transport & exposure" headers are
   applied by anything, and no task in the plan owns them.** `Content-Security-Policy`,
   `Strict-Transport-Security`, `X-Content-Type-Options: nosniff`, and `Referrer-Policy`
   are a binding table in a binding doc; `grep` finds no mention of any of them in
   `specs/` or `ralph/`. CSP and HSTS mostly matter to milestone 2's browser UI, but
   `nosniff` matters to a JSON API today. **Worth an operator adding a task** rather than
   discovering it when the dashboard ships.
4. **New this iteration: nothing bounds the number of connections.** The timeouts bound
   how long one connection may live, not how many may exist; `MaxHeaderBytes` bounds one
   request's headers, not the count. Loopback-only plus the tunnel makes this a
   same-host concern, and Principle VI's cap is about *sessions*, so no task owns it. Low
   risk, recorded because "the server has timeouts" reads as though it were covered.
5. **New this iteration: `Server` has no `Shutdown`, deliberately.** `Close` drops
   in-flight requests. **T037 owns adding `Shutdown(ctx)`** and must not reach for `Close`
   because it is already there.
6. **Iteration 18 #1 / 19 #5 still stands:** `Store.Add` does not require a `TokenHash`, so
   a record with no credential is storable. Reachable only from T031's adoption path.
7. **Iteration 18 #2 / 19 #6 still stands, now half-mitigated:** nothing bounds the length
   of a presented token before it is hashed. `MaxHeaderBytes` (16 KiB) caps the damage at
   the transport, but **T023 should still reject anything that is not exactly `TokenLen`**
   before hashing — the length is public, so a length check leaks nothing.
8. **Iteration 17 #1 / … / 19 #7 still stands:** the store cannot tell the audit trail *why*
   a lookup failed. T020/T038 own the ruling.
9. **Iteration 17 #2 / … / 19 #8 still stands:** `Delete`'s hash scrub is best effort; no
   test can assert it.
10. **Iteration 17 #3 / … / 19 #9 still stands, partly answered:** nothing enforces that a
    `Session.ID` in the store came from `NewID`, though every ID `Create` puts there does.
11. **Iteration 16 #1 / … / 19 #10 still stands:** `ResolveWorkDir` is a create-time check
    with an unavoidable TOCTOU window before `tmux new-session -c`. Operator ruling welcome.
12. **Iteration 16 #2 / … / 19 #11 still stands:** a rejected path is a weak existence
    oracle; the reason must stay server-side. T020/T022.
13. **Iteration 16 #3 / … / 19 #12 still stands:** nothing re-stats an approved root, so a
    root replaced by a symlink after startup narrows silently.
14. **Iteration 15 #1 / … / 19 #13 still stands:** FR-027's class admits a leading `-` while
    `tasks.md` T014 calls it hostile.
15. **Iteration 15 #2 / … / 19 #14 still stands, four files:** `ValidateName`,
    `ResolveWorkDir`, `NewToken`, and `Manager.Create` have no caller. **T022 owns all
    four**, and the route they hang off now exists.
16. **Iteration 13 #1 / … / 19 #15 still stands:** `docs/auth-and-sessions.md`'s samples are
    stale in three ways.
17. **Iteration 13 #2 / … / 19 #16 still stands and is now due:** nothing stops a handler
    from putting `auth.Reason(err)` in a response body. **T020 should assert the 401 body is
    byte-identical across every failure mode.**
18. **Iteration 12 #1 / … / 19 #17 still stands:** CI never runs `-race`
    (`.github/workflows/ci.yml:178`). Worth an operator doing before T033.
19. **Iteration 12 #2 / … / 19 #18 still stands:** three specs disagree on `Observe`'s
    signature. The code follows `tasks.md`.
20. **Iteration 12 #3 / … / 19 #19 still stands:** the replay cache is unbounded in count,
    only in age.
21. **Iteration 11 #1 / … / 19 #20 still stands:** the audit trail cannot tell clock drift
    from a forged future timestamp. T038.
22. **Iteration 11 #2 / … / 19 #21 still stands:** nothing forces the daemon's clock to be
    monotonic or roughly right.
23. **Iteration 10 #1 / … / 19 #22 still stands and is now due:**
    `contracts/http-api.md` promises `400` for an oversize body but auth runs first, and the
    signature covers the bytes as received. **T020 must decide** whether the limit is
    applied before or after verification; T021 implements whichever it says.
24. **Iteration 10 #2 / … / 19 #23 still stands, and the routes it is about now exist:** the
    signature covers timestamp and body but not method or path, so a signed body is valid on
    any of the six routes. The bearer token narrows it for session-scoped routes; it does not
    close it for the caller's own sessions or for `POST /sessions`.
25. **Iteration 9 #1 / … / 19 #24 still stands:** `audit.Record.Reason` can carry arbitrary
    text. T038 should pass server-authored constants.
26. **Iteration 9 #2 / … / 19 #25 still stands and is now due:** `audit.Emit`'s error has no
    handler. **T020 owns the ruling** — it is the task that emits one record per request.
27. **Iteration 8 #2 / … / 19 #26 still stands:** the loud default-root warning goes to
    stderr while audit records go to stdout. T032.
28. **Iteration 8 #1 / … / 19 #27 still stands:** `.env.example` does not exist. T040.
29. **Iteration 7 #1 / … / 19 #28 still stands:** bidi and invisible Unicode are not stripped
    by `tmuxctl.Strip`, by design. Milestone 2 decides.
30. **Iteration 6 #2 / … / 19 #29 still stands:** a failed `paste-buffer` leaves caller
    prompt text in a named tmux buffer.
31. **Iteration 6 #1 / … / 19 #30 still stands:** killing the only session stops the tmux
    server and `Has` then errors rather than returning false. T028 should use `List`, and
    should switch `Manager.rollback`'s call with it (iteration 19 #2).
32. **Iteration 6 #3 / … / 19 #31 still stands:** `contracts/tmuxctl.md` names only
    `no server running` for the empty-server case.
33. **Iteration 14 #1 / … / 19 #32 still stands:** `git checkout -- <path>` and `git restore`
    are not in the permission allowlist, so `PROMPT.md` step 6's documented recovery path
    needs an approval an autonomous run cannot give. Eleven probes reverted with `Edit` in
    reverse again this iteration.
34. **Iteration 1 #1 / … / 19 #33 still stands, twentieth iteration carrying it:** `loop.sh`'s
    sweep commit uses `--no-verify`, bypassing the gitleaks pre-commit hook (which ran clean
    on this iteration's commit). Needs an operator or a task of its own.
35. **Iteration 2 #2 / … / 19 #34 still stands:** duplicate checkbox state in
    `IMPLEMENTATION_PLAN.md` and `specs/.../tasks.md`, `PROMPT.md` step 9 naming only the
    plan. Ticked both by hand again, again only because the finding was written down.
    Nineteenth iteration of manual compensation for a one-line fix to step 9.
36. **Iteration 6 #6 / … / 19 #35 still stands:** `AGENTS.md`'s command table has no entry
    for `go test -tags tmux ./...` or `go vet -tags tmux ./...`, so "Test (all)" is not all.
