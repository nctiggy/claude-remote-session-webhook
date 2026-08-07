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

---

## Iteration 0 — milestone 5 begins

**Did:** Archived milestones 1–4 to `archive/progress-milestones-1-4.md` (3,256 lines) and
started a fresh notebook, exactly as at the start of milestone 4.

**Learned:** The archived file ended with a line reading exactly `RALPH_COMPLETE` — loop.sh's
own exit sentinel, matched with `grep -qxF`. **This is the second milestone in a row where
that trap was live.** Starting against it exits after one iteration reporting the plan
complete with every task still open. Iteration 0 of milestone 4 recorded this and it happened
again anyway, because the note lives in the file that gets archived. If a third milestone
starts this way, fix `loop.sh` rather than the notebook: have it refuse to start when the
sentinel is already present, which is a two-line guard and permanent.

**Left:** T001–T016 in `IMPLEMENTATION_PLAN.md`, all open.

**Findings:** Two tasks close defects in code milestone 4 shipped, both found by *running* the
daemon during planning rather than reading it. T014 fixes a dependency probe that has warned
on every start of the live deployment that `claude` is missing while sessions using it work —
it asks the service manager's PATH and the command runs in a login shell. T013/T015 fix an
audit-trail command that cannot work because the daemon's diagnostics share a stream with its
records; `_COMM=crswd` still fails, which is how the cause was identified.

The create-form chain T001 → T003 → T006 → T008 is strict. Four stories touch that one
template, and the order exists so a failure names one story.
