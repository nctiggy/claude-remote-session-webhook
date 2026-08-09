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

## Iteration 0 — milestone 7 begins

**Did:** Archived milestone 6 and started a fresh notebook. The sentinel guard in `loop.sh`
did its job: it refused nothing, because this was done before starting rather than after being
stopped.

**Left:** the milestone 7 task list, once it is generated.

**Findings:** This milestone is a mobile sweep, and its subject is the one surface that has
never had a pass. Two things the tasks will have to reckon with, both enforced by tests rather
than documented:

1. **`TestTheDashboardHasExactlyOneBreakpoint` enforces exactly one width breakpoint.** A task
   that needs a second must argue past that test, not delete it. `docs/design-system.md` claims
   two breakpoints exist; there is one. That discrepancy is itself worth fixing.
2. **The stylesheet sweep fails a rule whose class never appears in rendered markup**, and
   `TestNoRuleCarriesAValueThatBelongsInAToken` fails a literal length or colour. Every rule a
   task adds must be reachable from a template and built from tokens.

The hardest single problem is the pane: terminal output is 80 columns and a phone viewport is
about 30. Whatever the plan chooses there — scroll, shrink, wrap, zoom — it is a trade rather
than a fix, and the task should say which trade it is making.

The operator's own report, from a phone, is that **settings is the worst of it**. Weight that
above anything derived from reading the CSS.
