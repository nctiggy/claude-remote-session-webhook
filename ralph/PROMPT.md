# Ralph loop prompt

You are running autonomously, one iteration at a time, with a **fresh context**.
You have no memory of previous iterations. Everything you need is on disk.

## Do exactly this, in order

1. **Read the contract.** `AGENTS.md` at the repo root. Then read the `docs/` file
   that matches the task you are about to do (see its Progressive disclosure table).
   This milestone touches login, sessions and pane rendering, so
   `docs/auth-and-sessions.md` and `docs/security.md` are both live for most tasks.
   Also read `.specify/memory/constitution.md`.

2. **Read what done means.** `ralph/VALIDATION_CONTRACT.md`. It was written before
   the plan and it outranks the plan: a task finished in a way that contradicts an
   assertion there is not finished.

3. **Read the plan.** `ralph/IMPLEMENTATION_PLAN.md` — the tasks, the files-touched
   allowlist, and the measurements already taken so you do not retake them.

4. **Read the notebook.** `ralph/PROGRESS.md` — this is what past iterations did.
   Do not redo finished work.

5. **Pick exactly ONE task.** The highest-priority unchecked item in
   `ralph/IMPLEMENTATION_PLAN.md`. One. Not two. Not "this one is small so I'll
   also do the next one." Resist that urge — it is how loops go off the rails.

6. **Implement it.** Follow the conventions in `AGENTS.md`. Reuse what
   `docs/components.md` already defines. Stay inside the plan's **Files touched**
   allowlist. Do not invent requirements: if the task is ambiguous, do NOT guess —
   write the ambiguity into `ralph/PROGRESS.md` under `NEEDS CLARIFICATION`, mark
   the task blocked with `- [!]`, and move to the next one.

7. **Test it.** Run the project's test and lint commands from `AGENTS.md`, plus the
   command the task itself names. They must pass. If you cannot make them pass,
   revert your change and log why. Never commit a broken tree.

8. **Commit.** One focused commit, imperative subject, explaining *why*:
   ```
   feat(auth): report a logged-out session as needs-auth

   A session parked on Claude Code's login screen matched no signature, so the
   board rendered it `running` and a dead fleet read as healthy. Adds the
   captured signature and derives the state from it.
   ```

9. **Update the notebook.** Append to `ralph/PROGRESS.md`:
   - what you did, in one or two lines
   - anything you learned that the next iteration would waste time rediscovering
   - what is left
   - any ad-hoc problem you noticed but did NOT fix (findings go here, not silently dropped)

10. **Tick the task** in `ralph/IMPLEMENTATION_PLAN.md`.

11. **Exit.** Do not start another task. The loop gives you a fresh context; that
    is the feature.

## This milestone's own hard rules

These are the ones a fresh context is most likely to break. They are in
`ralph/IMPLEMENTATION_PLAN.md` too; they are repeated because breaking one of them
ships a credential.

- **The relayed login code is a live credential.** Never write it to the audit
  trail, never log it, never render it back into a page, never name it in an error
  string. The trail records *that* a relay happened, never *what* was relayed.
- **The sign-in URL is a one-shot PKCE challenge.** It belongs on the parked
  session's own page and nowhere else — not the fleet grid, not the trail, not a
  log line.
- **Never auto-submit anything the operator did not type.** The daemon relays; it
  does not decide.
- **Never add a pane signature from memory or from a description.** Use the text
  captured in the plan. A phrase that was invented rather than observed looks like
  coverage and is not.

## Completion

When — and only when — every task in `ralph/IMPLEMENTATION_PLAN.md` is checked and
the tree is green, append a line containing exactly `RALPH_COMPLETE` to
`ralph/PROGRESS.md`. The loop watches for that string and stops.

Do not append it while any task is open, and do not append it with a sentence
claiming a live end-to-end re-auth was performed. That run needs a real
logged-out session and a browser, which no iteration can reach — see
`ralph/IMPLEMENTATION_PLAN.md` § *What the loop cannot do*.

## Hard limits

- Never push to `main`. Never force-push. The `danger-guard` hook enforces this.
- Never commit a secret.
- Never disable or route around a hook.
- If you are about to do something irreversible, stop and log it instead.
