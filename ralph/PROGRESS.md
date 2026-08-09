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

---

## Iteration 2 — 2026-08-09 08:29

**Did:** T002. `--tap: 44px` and `--fs-input: 16px` in all three files in one commit —
`docs/design-system.md` (declared as CSS inside the pointer-coarse section, with the reason
each number is what it is), the `crswd.css` token block, and `designTokens`. Nothing spends
them yet; T010 and T011 do.

**Learned, so the next iteration does not rediscover it:**

- **No test requires a declared token to be referenced.** `TestEveryTokenReferencedExists`
  (`stylesheet_test.go:2076`) sweeps `var(--x)` references and checks each is defined — the
  other direction is not held anywhere. That is why T002 could land two tokens ahead of the
  block that spends them without going red, and it is also a gap: a token nothing uses will
  sit in the block forever without a complaint.
- **The `designTokens` comment already draws the line T002 needed and does not need
  amending.** It says the typography and layout values are absent *because the document gives
  them in tables without token names*. `--fs-input` is a font size and it belongs in the map
  anyway, because design-system.md now declares it as CSS with its name. Table-without-a-name
  → out; declared-as-CSS → in. A future token should be added to the document in a ```css
  block, not as a table row, or that distinction stops being readable.
- **Finding 2 from iteration 1 is resolved: `--pane-h: 30rem` is correct.** The token block
  at `crswd.css:113` declares exactly that. The Layout prose in design-system.md was accurate.
  T004 does not need to check it.
- **The negative case is cheap to prove here and worth proving.** Deleting the two
  declarations from `crswd.css` and running `TestTheTokenBlockIsTheDesignSystem` fails naming
  both tokens. Doing that took one edit and four seconds, and it is the only evidence that a
  guard this milestone leans on actually fires.
- All five gate commands green; linter is 2.12.2, so the green is real. `-tags quickstart`
  took 33s and found `127.0.0.1:8765` free again.

**Left:** T003 next (the pane's `overscroll-behavior-x`), then everything after. 15 of 17
tasks open.

**Findings — noticed, not fixed:**

1. **T002's third-file obligation is still unenforced, and now it has an accomplice.** The
   commit is the only thing that checked the document. A future token added to the stylesheet
   and the map alone would pass every test — the same gap iteration 1 recorded, confirmed
   from the other side now that a token has actually gone through it. What would close it is
   a test that parses the ```css blocks out of `docs/design-system.md` and compares them to
   `designTokens`; that is a new guard and out of scope here, but it is the concrete shape of
   the fix if anyone wants it.
2. **The token block has no ordering rule and is drifting toward one.** I put the two touch
   tokens after the focus pair and before `--shell-max`, matching the document's section
   order, but nothing says that is where they go and nothing would have complained if they
   had landed between `--s7` and `--r`. There are now nine loose groups in that rule
   separated only by blank lines and comments. Not worth a task on its own; worth knowing
   before someone adds the tenth.
3. **`--fs-nano` at `crswd.css:66` carries two comments describing the same token**, one of
   which appears to be a leftover from an earlier edit ("One step below the eyebrow, for the
   version beside the wordmark." immediately followed by a longer comment saying the same
   thing differently). Cosmetic, in a file this milestone touches repeatedly, and a one-line
   deletion for whoever is next in that region — but AR-008 says not from inside T002.
