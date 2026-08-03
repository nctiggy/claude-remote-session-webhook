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
