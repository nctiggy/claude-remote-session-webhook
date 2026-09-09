# Ralph loop prompt

You are running autonomously, one iteration at a time, with a **fresh context**.
You have no memory of previous iterations. Everything you need is on disk.

## Do exactly this, in order

1. **Read the contract.** `AGENTS.md` at the repo root. Then read the `docs/` file
   that matches the task you are about to do (see its Progressive disclosure table).
   Also read `.specify/memory/constitution.md`.

2. **Read what done means.** `ralph/VALIDATION_CONTRACT.md`. It was written before
   the plan was decomposed, and a later pass checks the built system against it as
   a black box. If a task you are about to do would not move an assertion in that
   file closer to true, you are doing the wrong thing.

3. **Read the plan.** `ralph/IMPLEMENTATION_PLAN.md`. Its **Files touched**
   section is the blast radius for this milestone — a diff outside that list is
   rejected. Its **Out of scope** section is not advice.

4. **Read the notebook.** `ralph/PROGRESS.md` — this is what past iterations did,
   plus what was already measured before iteration 1. Do not redo finished work,
   and do not re-derive anything the notebook already records.

5. **Pick exactly ONE task.** The highest-priority unchecked item in
   `ralph/IMPLEMENTATION_PLAN.md`. One. Not two. Not "this one is small so I'll
   also do the next one." Resist that urge — it is how loops go off the rails.
   Each task has a matching detail section higher up the plan. Read it.

6. **Implement it.** Follow the conventions in `AGENTS.md`. Reuse what
   `docs/components.md` already defines. Do not invent requirements: if the task
   is ambiguous, do NOT guess — write the ambiguity into `ralph/PROGRESS.md` under
   `NEEDS CLARIFICATION`, mark the task `- [!]` blocked, and move to the next one.

7. **Test it.** Run the verification command the task names, and keep the repo
   green: `go build ./... && go vet ./... && go test ./...`, plus
   `node --test deploy/crswd-axi/test/` once that directory exists. They must
   pass. If you cannot make them pass, revert your change and log why. Never
   commit a broken tree.

8. **Commit.** One focused commit, imperative subject, explaining *why*:
   ```
   feat(auth): expire idle sessions after 30m

   Sessions previously lived until token expiry, so a shared machine kept a
   user signed in for hours. Adds an idle timer that clears state via signOut().
   ```

9. **Update the notebook.** Append to `ralph/PROGRESS.md`, in the shape that file
   documents:
   - what you did, in one or two lines
   - anything you learned that the next iteration would waste time rediscovering
   - what is left
   - any ad-hoc problem you noticed but did NOT fix (findings go here, not silently dropped)

10. **Tick the task** in `ralph/IMPLEMENTATION_PLAN.md`.

11. **Exit.** Do not start another task. The loop gives you a fresh context; that
    is the feature.

## Completion

When — and only when — every task in `ralph/IMPLEMENTATION_PLAN.md` is checked,
every assertion in `ralph/VALIDATION_CONTRACT.md` holds, and the tree is green,
append a line containing exactly `RALPH_COMPLETE` to `ralph/PROGRESS.md`.
The loop watches for that string and stops.

## Hard limits

- Never push to `main`. Never force-push. The `danger-guard` hook enforces this.
- Never commit a secret. gitleaks runs on every commit and scans full history in
  CI; the plan names the two fixture shapes that are safe by construction.
- Never disable or route around a hook.
- Never write outside the plan's **Files touched** list.
- If you are about to do something irreversible, stop and log it instead.
