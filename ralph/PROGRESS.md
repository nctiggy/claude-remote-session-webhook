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

---

## Iteration 1 — 2026-08-09 08:24

**Did:** T001. Created `docs/mobile-open-questions.md` — three questions, each `Status:
UNANSWERED`, each with its named fallback and the sentence that a question is answered by the
operator's report replacing it, never by a task. Rewrote the breakpoint section of
`docs/design-system.md`: one breakpoint, the test that holds it, the full enumeration of the
780px block, the pointer-coarse policy, and the pane's typography row amended to `pre`;
`pre-wrap` under the breakpoint.

**Learned, so the next iteration does not rediscover it:**

- **No test reads `docs/design-system.md`.** Only `docs/components.md` is read by the suite
  (`componentsDocPath`, `stylesheet_test.go:466`). Every other doc reference in Go is a
  comment. So design-system.md changes are genuinely inert to the gate — which cuts both
  ways: **T002's three-file obligation has no test behind the third file.** Nothing will fail
  if the tokens land in the stylesheet and the map but not the document. Only the commit
  does that check.
- **The 780px block contains exactly four rules today**: `.shell` padding `--s4`, `.summary`
  two columns, `.brand-tag` hidden, `.settings` one column. `crswd.css:1041`, closing brace
  at `1062`. The reduced-motion block follows immediately at `1067` — so T010's coarse block
  goes after `1067`'s closing brace, not after `1062`.
- **The breakpoint guard's regex is `\((?:max|min)-width\s*:\s*([^)]+)\)`**
  (`stylesheet_test.go:238`). It is a count of matches, then a value comparison on the single
  match. This confirms from the code, not from prose, why range syntax evades it and why a
  second block at an identical width still fails.
- **The design-system enumeration now carries an in-document obligation**: adding a rule to
  the 780px block adds a row to that table in the same commit. T004, T007, T008, T012 and
  T013 each add rules to that block, and **no later task in the plan tells them to update
  this table** — the doc is the only place that says so. Do not skip it; a stale enumeration
  is the exact defect T001 existed to fix.
- The linter here is **2.12.2**, so its green is real. All five gate commands pass, including
  `-tags tmux` and `-tags quickstart` (the latter took 34s and found `127.0.0.1:8765` free —
  the deployed daemon was not holding it).

**Left:** T002 (the two tokens, three files, one commit) then everything after it. 16 of 17
tasks open.

**Findings — noticed, not fixed:**

1. **`docs/design-system.md` and the pane's real behaviour are now deliberately out of step
   by one task.** The typography row says `pre-wrap` under the breakpoint; T004 lands it. This
   is the order T001 specifies (the document is what following tasks transcribe from), but if
   the milestone stops before T004, that row is a lie. Same for the pointer-coarse policy,
   which describes a block T010 creates.
2. **The `--pane-h: 30rem` note in the Layout section is unverified prose.** I moved those two
   paragraphs so they would not fall under the new `###` subheading, but did not check the
   value against the stylesheet. Worth a grep during T004, which is in that rule anyway.
3. **No guard exists for the token-block/document relationship in either direction.** The
   `designTokens` map is a transcription of design-system.md and the comment at
   `stylesheet_test.go:21` explains why it is hand-written — but nothing verifies the
   transcription against its source. A token could be deleted from the document and every
   test would stay green. That is a real gap and it is exactly what makes T002 risky; it is
   not in this milestone's scope to close.
