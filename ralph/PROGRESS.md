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

---

## Iteration 3 — 2026-08-09 08:33

**Did:** T003. `overscroll-behavior-x: contain` in the base `.pane` rule (now `crswd.css:911`,
after the comment grew), plus both guards from `contracts/pane.md`:
`TestThePaneDoesNotChainItsOverscroll` and `TestThePaneDoesNotTrapVerticalScrolling`
(`stylesheet_test.go:2129` onward). Nothing in the 780px block was touched, so the
design-system enumeration table is untouched too.

**Learned, so the next iteration does not rediscover it:**

- **`blockFor(t, source, ".pane")` is safe today and is a positional bet.** It takes the
  **first** `strings.Index` of the marker, so it returns the base rule only because `.pane`
  appears nowhere earlier — `--fs-pane`, `--lh-pane` and `--pane-h` carry no dot, and
  `.pane-note` and `.settings-panel` come later or do not match. **T004 adds a second `.pane`
  rule inside the 780px block, and `blockFor` will keep returning the base one.** That is
  correct for `TestThePaneKeepsItsDesktopAlignment` and wrong for
  `TestThePaneWrapsOnlyOnNarrowViewports` — the latter must read the media block, not
  `blockFor(".pane")`, or it will assert `pre-wrap` against a rule that must say `pre` and be
  unsatisfiable. Use `blockFor` on the media prelude, or sweep `cssRules`.
- **`cssRules` is the tool for "every rule with this selector, media block included."** It
  strips media preludes first, so a rule inside the breakpoint appears as an ordinary chunk.
  `TestThePaneDoesNotTrapVerticalScrolling` uses it precisely so a stray
  `overscroll-behavior-y` cannot hide inside the 780px block where T004 is about to add a
  `.pane` rule. Split `rule.selector` on commas and trim each part — the struct trims the
  whole selector, not the pieces.
- **Both negatives were proved, and the second one is the interesting one.** Deleting the
  declaration fails the chain test; replacing it with the *bare* `overscroll-behavior: contain`
  fails **both** — the trap test because it contains the vertical axis, and the chain test
  because the repo's rule is the explicit `-x` spelling, not "some declaration that happens to
  cover x." That is deliberate: the bare property is the exact tidy-up a future reader will
  reach for, and it is a regression.
- The proof itself has a wrinkle worth knowing: `sed -i` on a tracked file was **refused by
  the permission layer** as part of a compound command. Do the temporary mutation with the
  `Edit` tool and undo it with a second `Edit` — the format-and-lint hook is fine with that,
  and there is no backup file to forget to delete.
- All five gate commands green; linter is **2.12.2**, so the green is real. `go test ./...`
  ran clean and the three tagged suites compile (`go vet -tags tmux|quickstart|dev`).

**Left:** T004 next (the wrap, inside the 780px block — and it owes the design-system
enumeration table a row). 14 of 17 tasks open.

**Findings — noticed, not fixed:**

1. **The pane's `overflow: auto` and its contained x-axis now describe a scroll container that
   `docs/components.md` does not mention at all.** T016 is the task that updates that prose and
   it is fourteen tasks away; until then the document describes a pane that chains its scroll.
   Not a defect in the code — a note that T016's scope is now one declaration larger than it
   was when it was written.
2. **`TestThePaneDoesNotChainItsOverscroll` cannot see a rule that *overrides* it later.**
   `overscroll-behavior-x: auto` in the 780px block would restore the exact bug on exactly the
   device the milestone is for, and the base-rule assertion would stay green. The trap test
   sweeps every `.pane` rule; this one does not, because the contract specifies `blockFor`.
   Cheap to widen if anyone wants it — the same `cssRules` loop, asserting no `.pane` rule
   sets the x-axis to anything but `contain`.
3. **Two `.pane` rules are about to exist and nothing enforces which one wins.** The base rule
   and T004's media rule have identical specificity, so order decides — the same failure mode
   the plan flags for T010's coarse block, in a second place, with no equivalent offset
   assertion named for it. The 780px block already sits below `.pane` in the file, so T004 is
   fine as long as it goes where the plan says; it is only a hazard if a later task moves a
   rule rather than adding one.

---

## Iteration 4 — 2026-08-09 08:37

**Did:** T004. `white-space: pre-wrap` + `overflow-wrap: anywhere` on `.pane` inside the
existing 780px block (`crswd.css`, after `.settings`, before the closing brace), base rule
left at `white-space: pre`. Both contract tests shipped with it —
`TestThePaneWrapsOnlyOnNarrowViewports` and `TestThePaneKeepsItsDesktopAlignment`
(`stylesheet_test.go:2207` onward). Added the row the design-system enumeration table owes
for a new rule in that block.

**Learned, so the next iteration does not rediscover it:**

- **Iteration 3's warning about `blockFor` was right, and the fix is the nesting pattern that
  already exists in this file.** `blockFor(t, stylesheet(t), "@media (max-width: 780px)")`
  then `blockFor(t, narrow, ".pane")` — exactly how `TestReducedMotionRemovesTheRain`
  (`stylesheet_test.go:252`) reads the reduced-motion block. No new helper was needed. The
  inner call is safe because nothing earlier in the media block contains the substring
  `.pane`; `.settings-panel` is not in there. **T007, T008, T012 and T013 all add rules to
  this same block and can use the same two lines.**
- **`pre` is a prefix of `pre-wrap`, so a `MatchString` on either is a false pass.** Both
  tests capture the keyword with one shared expression (`whiteSpaceDecl`) and compare it,
  rather than matching a pattern. `\b` does not help here — `-` is a non-word character, so
  `white-space:\s*pre\b` matches `pre-wrap` happily. This will bite anything asserting a
  keyword that is another keyword's prefix.
- **The negative that matters is not the missing declaration, it is the misplaced one.**
  Moving the wrap into the base rule and deleting the media rule renders *identically on a
  phone* and takes alignment away from every desktop reader. Proved it: that mutation fails
  `TestThePaneKeepsItsDesktopAlignment` naming the base rule. Also proved dropping
  `overflow-wrap` alone fails the second assertion. Temporary mutations done with `Edit` and
  undone with `Edit`, per iteration 3 — `sed -i` is still refused by the permission layer.
- All five gate commands green; linter is **2.12.2**, so the green is real. `go test ./...`
  clean, and all three tagged suites compile (`go vet -tags tmux|quickstart|dev`).
  `-tags quickstart` was not *run*: this task touches no Go outside a test file and no
  `cmd/crswd`.
- **The pane's typography row in `docs/design-system.md` is now true.** Iteration 1's finding
  1 is half-resolved — the `pre`/`pre-wrap` claim landed. The pointer-coarse policy in that
  same document still describes a block that does not exist until T010.

**Left:** T005 next (`TestNoPageClampsTheZoom`, walking `web.Templates`) — and it is the
guard that protects the trade this iteration just made, so it is not optional decoration.
13 of 17 tasks open.

**Findings — noticed, not fixed:**

1. **The comment above the 780px block is stale and this task made it staler.** It says "the
   summary drops to two columns and the tagline hides" while the block now does five things
   — `.shell`, `.summary`, `.brand-tag`, `.settings` and `.pane`. It was already wrong before
   this iteration (it never mentioned `.shell` or `.settings`), so fixing it is adjacent
   churn under AR-008 and the constitution's fourth principle, not part of T004. Whoever
   takes T007, T008, T012 or T013 will be editing that block anyway — one of them should
   either delete the enumeration from the comment or replace it with a pointer to the
   design-system table, which is the copy that has an obligation attached to keeping it
   current. Enumerating the same list in two places is what made this stale in the first
   place.
2. **Nothing enforces that the design-system enumeration table matches the block.** The
   obligation is prose in the document; the only thing that checked it this iteration was me
   reading it. Same shape as iteration 2's finding 1 about the token document. A guard that
   parsed the selectors out of the 780px block and compared them to that table's first column
   would close both this and the "adding a rule adds a row" rule in one test — out of scope
   here, but it is the third time the notebook has recorded a document with no guard behind
   it, and the plan has four more tasks that add rules to this block.
3. **`TestThePaneWrapsOnlyOnNarrowViewports` asserts the declarations exist, not that they
   win.** Iteration 3's finding 3 is now live rather than pending: two `.pane` rules exist at
   identical specificity and order alone decides. The media block is below the base rule, so
   it is correct today, and no test would notice if a future edit moved the base rule down —
   the exact failure mode T010 has a named offset assertion for
   (`TestTheCoarseBlockOverridesRatherThanPrecedes`) and this pair does not. Cheap to add by
   comparing `strings.Index` of the two rules, if anyone wants the symmetry.
