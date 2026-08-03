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
