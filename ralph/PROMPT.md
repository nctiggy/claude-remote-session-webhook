# Ralph loop prompt

You are running autonomously, one iteration at a time, with a **fresh context**.
You have no memory of previous iterations. Everything you need is on disk.

## Do exactly this, in order

1. **Read the contract.** `AGENTS.md` at the repo root. This milestone is Go test
   code under `cmd/crswd`, so also read `docs/conventions.md` from its Progressive
   disclosure table. Also read `.specify/memory/constitution.md`.

2. **Read the plan.** `ralph/IMPLEMENTATION_PLAN.md`. Read it *whole*, not just the
   task list: the sections above the tasks carry the code to lift, the one place
   the lift must not be verbatim, and facts already measured on this host that cost
   an iteration each to rediscover.

3. **Read what done means.** `ralph/VALIDATION_CONTRACT.md`. It is checked later as
   a black box, with no diff and no history. A task is not done because it compiles.

4. **Read the notebook.** `ralph/PROGRESS.md` — this is what past iterations did.
   Do not redo finished work.

5. **Pick exactly ONE task.** The topmost unchecked item in
   `ralph/IMPLEMENTATION_PLAN.md`. One. Not two. Not "this one is small so I'll
   also do the next one." Resist that urge — it is how loops go off the rails.

6. **Implement it.** Follow the conventions in `AGENTS.md`. Do not invent
   requirements: if the task is ambiguous, do NOT guess — write the ambiguity into
   `ralph/PROGRESS.md` under `NEEDS CLARIFICATION`, mark the task blocked, and stop.

7. **Test it.** Every Go command in this repository needs `GOFLAGS=-buildvcs=false`
   in front of it. This is a git worktree; without that flag `go build` dies with
   `error obtaining VCS status: exit status 128`, and both the acceptance suite and
   `internal/release` fail before asserting anything. That failure is not yours and
   fixing it is not this milestone. Run what the task names, and it must pass. If
   you cannot make it pass, revert your change and log why. Never commit a broken
   tree.

8. **Stay inside the blast radius.** `ralph/IMPLEMENTATION_PLAN.md` has a **Files
   touched** section. Before committing, run `git status --porcelain` and confirm
   every changed path is on that list. One task deliberately edits
   `internal/config/sessionenv.go` to prove a test fails — that edit must be
   reverted with `git checkout -- internal/config/sessionenv.go` and must not reach
   a commit.

9. **Commit.** One focused commit, imperative subject, explaining *why*:
   ```
   test(crswd): read the 401 back-off out of a pane, not off the map

   #153 asserted the composed environment. A fix that reached one start
   command and not the others would have passed that and still left a
   dashboard session racing for the refresh token.
   ```

10. **Update the notebook.** Append to `ralph/PROGRESS.md`:
    - what you did, in one or two lines
    - anything you learned that the next iteration would waste time rediscovering
    - what is left
    - any ad-hoc problem you noticed but did NOT fix (findings go here, not
      silently dropped)

11. **Tick the task** in `ralph/IMPLEMENTATION_PLAN.md`.

12. **Exit.** Do not start another task. The loop gives you a fresh context; that
    is the feature.

## Completion

When — and only when — every task in `ralph/IMPLEMENTATION_PLAN.md` is checked, the
assertions in `ralph/VALIDATION_CONTRACT.md` hold, and the tree is green, append a
line to `ralph/PROGRESS.md` containing exactly `RALPH_COMPLETE` and nothing else.
`loop.sh` greps for that line, on its own, and stops. Do not write it early and do
not write it inside a sentence: a line that merely mentions it counts.

## Hard limits

- Never push to `main`. Never force-push. The `danger-guard` hook enforces this.
- **Never delete the branch `feat/lm-95-oauth-401-wait-default`.** It is the only
  copy of the code this milestone lifts; PR #154 is closed.
- Never commit a secret.
- Never disable or route around a hook.
- If you are about to do something irreversible, stop and log it instead.
