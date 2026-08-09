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

---

## Iteration 0 — the guard-gap backlog begins

**Did:** Archived milestone 7 and opened a fresh notebook.

**Left:** five tasks, each closing a gap that milestone 7 surfaced by tripping over it.

**Findings:** Every task in this list exists because **something in this repository
stated an invariant that nothing enforced**. That is the shape to look for, and it
recurs:

- `designTokens` says it transcribes `docs/design-system.md`. No test opens that file.
- `settings.go` says no mutating verb reaches the config. `POST /settings/edit` does.
- `components.md` says the Toast "has no section and no use". It ships in three templates.
- The class sweep says it holds the stylesheet and the markup to the same names. It
  cannot see an element selector, which is how four dead rules survived two milestones.
- `freePort` says it returns a free port. It returns a port that *was* free.

Four of the five are a **document or a comment that is confidently wrong**, and in a
pipeline whose executor reads comments as contract, that is not tidiness — it is a
defect that costs an iteration or, worse, is believed.

The fifth (#123) is the one to be most careful with: it is the only change here that
touches a test harness rather than a document, and a wrong fix makes the suite flaky
in a new way instead of the old one.

**T003 is the task most likely to grow.** Once the sweep can see element selectors it
may report rules nobody has looked at since #103. Anything it finds must be checked
against the templates before deletion — T015 of milestone 7 kept two of four rules it
was expected to delete, and deleting either would have left an unstyled page no guard
could report.
