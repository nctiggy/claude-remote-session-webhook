# Contributing

This is the from-source path, for changing the daemon. To *run* it, use the
one-line install in [`README.md`](README.md#install) — a released binary needs no
compiler, which is the point of publishing one.

## Working in this repo

Read [`AGENTS.md`](AGENTS.md) first — it is the contract for humans and agents
alike, and it names which `docs/` file to load for a given change.

```bash
git config core.hooksPath .githooks   # once per clone — gitleaks pre-commit
cp .env.example .env                  # names only; fill in locally, never commit

go mod download
go build ./...
go test ./...
golangci-lint run
```

A change is not done until those four pass, which are the commands CI runs and
nothing else. **Three suites are behind build tags and neither `go test ./...`
nor CI's untagged run reaches them**, so a tagged suite reports nothing whether
or not it still compiles: `tmux`, `quickstart` and `dev`. `AGENTS.md` says which
one covers what, and what each needs on the host.

The standards themselves are in [`.specify/memory/constitution.md`](.specify/memory/constitution.md)
and `docs/`, and they are enforced by the hooks in `.claude/hooks/` rather than
by review. Do not route around a hook; if a guard is wrong, fix the guard in a
PR.

## Planning a milestone

```
/prd                    # write the PRD
/prd-critic-loop        # Staff Engineer / SRE / Security passes
```

Then convert the milestone's tasks into `ralph/IMPLEMENTATION_PLAN.md` as a
checklist. Spec Kit (`/speckit-specify` → `/speckit-plan` → `/speckit-tasks`) is
the alternative route and is wired into this repo.

> **Note:** `loop.sh` reads `ralph/IMPLEMENTATION_PLAN.md` (markdown checklist).
> It does **not** read snarktank `prd.json` — that is a different Ralph.

## Running a loop

```bash
git switch -c feat/milestone-1     # the loop refuses to run on main
./ralph/loop.sh 5                  # cap at 5 iterations
```

Run `claude -p "$(cat ralph/PROMPT.md)"` by hand two or three times first and watch
what it does. Wrap it in the loop only once the behaviour is boring.

**One checkout, one loop.** Two iterations sharing a working tree is not a faster
loop — it is a corrupt notebook, because both pick the same topmost open task and
either may commit the other's half-finished edits. If you want two, give them
separate worktrees.
