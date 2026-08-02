# Ralph loop prompt

You are running autonomously, one iteration at a time, with a **fresh context**.
You have no memory of previous iterations. Everything you need is on disk.

## Do exactly this, in order

1. **Read the contract.** `AGENTS.md` at the repo root. Then read the `docs/` file
   that matches the task you are about to do (see its Progressive disclosure table).
   Also read `.specify/memory/constitution.md`.

2. **Read the plan.** `ralph/IMPLEMENTATION_PLAN.md`.

3. **Read the notebook.** `ralph/PROGRESS.md` — this is what past iterations did.
   Do not redo finished work.

4. **Pick exactly ONE task.** The highest-priority unchecked item in the plan.
   One. Not two. Not "this one is small so I'll also do the next one."
   Resist that urge — it is how loops go off the rails.

5. **Implement it.** Follow the conventions in `AGENTS.md`. Reuse what
   `docs/components.md` already defines. Do not invent requirements: if the task
   is ambiguous, do NOT guess — write the ambiguity into `PROGRESS.md` under
   `NEEDS CLARIFICATION`, mark the task blocked, and move to the next one.

6. **Test it.** Run the project's test and lint commands from `AGENTS.md`.
   They must pass. If you cannot make them pass, revert your change and log why.
   Never commit a broken tree.

7. **Commit.** One focused commit, imperative subject, explaining *why*:
   ```
   feat(auth): expire idle sessions after 30m

   Sessions previously lived until token expiry, so a shared machine kept a
   user signed in for hours. Adds an idle timer that clears state via signOut().
   ```

8. **Update the notebook.** Append to `ralph/PROGRESS.md`:
   - what you did, in one or two lines
   - anything you learned that the next iteration would waste time rediscovering
   - what is left
   - any ad-hoc problem you noticed but did NOT fix (findings go here, not silently dropped)

9. **Tick the task** in `ralph/IMPLEMENTATION_PLAN.md`.

10. **Exit.** Do not start another task. The loop gives you a fresh context; that
    is the feature.

## Completion

When — and only when — every task in the plan is checked and the tree is green,
append a line containing exactly `RALPH_COMPLETE` to `ralph/PROGRESS.md`.
The loop watches for that string and stops.

## Hard limits

- Never push to `main`. Never force-push. The `danger-guard` hook enforces this.
- Never commit a secret.
- Never disable or route around a hook.
- If you are about to do something irreversible, stop and log it instead.
