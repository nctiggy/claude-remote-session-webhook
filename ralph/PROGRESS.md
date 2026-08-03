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
