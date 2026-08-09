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

---

## Iteration 1 — 2026-08-09

**Did:** T001 (#116). `TestTheTranscriptionIsTheDocumentItTranscribes` in
`internal/httpapi/stylesheet_test.go` now reads `docs/design-system.md` and compares it
with the `designTokens` map in both directions. Only that test file changed; the
document and the stylesheet are byte-identical to before.

**Learned:**

- **The document declares tokens in three shapes, not one.** The map's old comment said
  "every token that document declares as CSS", and that was already wrong when it was
  written. Fenced CSS blocks carry 22 of them; the **state table** carries the four
  `--state-*` tokens in two cells and never as CSS; and `--pane-h: 30rem` is a single
  inline code span in one sentence of the Layout section. A sweep that read only the
  fences would have reported the four state tokens as fabrications and missed `--pane-h`
  entirely — wrong in both directions at once.
- **`--pane-h` was a live instance of the very drift the map exists to catch.** It is
  declared in the document (line 217) and in the stylesheet, and the map omitted it, so
  nothing held one to the other. It is in the map now.
- **Two regex traps, both silent.** A token *reference* looks like a declaration: the
  pointer-coarse table is full of `var(--tap)` and `var(--s3)`. What separates them is
  the character before — `(` is not admitted by the leading class — and the colon. And
  the inline declaration is delimited by backticks on **both** sides, so the value class
  has to exclude a backtick or `--pane-h`'s value swallows the rest of the sentence, and
  the leading class has to *admit* one or the declaration is never found at all. The
  first attempt got the value right and the lead wrong, and the symptom was `--pane-h`
  reported as a fabrication rather than any parse error.
- **The state-table parse anchors on the second cell being nothing but a backticked
  token.** That is what keeps it off the breakpoint and pointer tables, whose second cell
  is a declaration made *about* a selector (`` `min-block-size: var(--tap)` ``) rather
  than a token being declared. It is shape-based and therefore brittle by nature, so
  `designSystemTokens` fatals if any of `documentedStates`' four tokens is missing from
  the table parse — a parser that has gone blind must not read as a map that invented
  the palette.
- **Breaking it is cheap here and worth doing all four ways.** Direction 1 was proved by
  adding a token to the stylesheet *and* the map: `TestTheTokenBlockIsTheDesignSystem`
  stays green on that, which is precisely the hole. Watch out for name collisions when
  doing this — `--glow` is already declared further down `crswd.css`, which muddies the
  proof; `--halo` is free.

**Left:** T002 (#117), T003 (#118), T004 (#119), T005 (#123).

**Findings (noticed, not fixed — no code changed for any of these):**

- **The stylesheet declares ~20 tokens the design system never names**, and nothing
  reports it: `--fs-brand`, `--fs-nano`, `--fs-micro`, `--fs-eyebrow`, `--fs-body`,
  `--lh-body`, `--fs-prose`, `--fs-label`, `--fs-pane`, `--lh-pane`, `--tuck`,
  `--ls-brand`, `--ls-eyebrow`, `--ls-label`, `--focus-width`, `--focus-offset`,
  `--shell-max`, `--field-min`, `--menu-min`, `--menu-max`, `--card-min`, `--toast-max`,
  `--combo-h`, `--rain-wipe`, `--glow`. Most are legitimate — the design system gives
  typography and layout in tables *without* token names, and each declaration carries a
  comment saying so. But that is an argument made in comments, not a rule anything holds,
  and it is the same shape as every task in this milestone: **the third direction of
  this sweep, stylesheet → document, does not exist.** Note the tension before opening
  it — closing it either means naming two dozen tokens in the design system or writing
  down an explicit exemption list, and an exemption list nobody prunes is how a
  fabricated token hides. Not in scope for T001, which was about the map.
- **`--focus-width: 2px` / `--focus-offset: 2px` are the closest thing to a real gap in
  that list.** The design system gives the focus rule verbatim as CSS
  (`outline: 2px solid var(--phosphor); outline-offset: 2px;`), so unlike the typography
  table those two lengths *are* in the document — just spelled as literals inside a rule
  rather than as tokens. `designSystemTokens` correctly does not read them, because they
  are not declarations of a `--name`. A future task could hold the focus rule's two
  lengths to the document the way the breakpoint's `780px` already is.
- **The design system's Layout section is the one place a token is declared in prose.**
  If that sentence is ever rewritten to drop the value, the new test reports `--pane-h`
  as a fabrication in the map rather than a document that stopped saying something. The
  message names the file, so it should not cost more than a minute — but it is the
  failure mode of reading a value out of English.
