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

---

## Iteration 5 — 2026-08-09 08:41

**Did:** T005. `TestNoPageClampsTheZoom` (`stylesheet_test.go:2275` onward), walking the whole
`web.Templates` tree and failing on `maximum-scale` at any value or `user-scalable=no`. No
CSS, no template, no doc changed — the four pages already carry a clean
`width=device-width, initial-scale=1`, so this task is purely the guard that keeps them that
way now that T004 has given someone a reason to reach for the clamp.

**Learned, so the next iteration does not rediscover it:**

- **The sweep is on the whole markup, not inside the viewport meta.** `viewportMeta` is used
  only to *count* — the vacuity guard — while `zoomClamp` runs over the file. That way a clamp
  in a second meta element, or in a partial that grows one, is caught by the same expression
  without the test having to model where a meta may legally appear. The two regexes are
  adjacent to the test at the end of the file.
- **All three negatives were proved.** `maximum-scale=1` in `session.html` fails naming the
  file and the match; `user-scalable=no` likewise; and renaming the attribute in **all four**
  pages hits the `viewports == 0` fatal rather than passing green. The third is the one worth
  the effort — a sweep with nothing to sweep is this repo's recorded failure mode, and the
  guard fires.
- **`git checkout -- <path>` is refused by the permission layer**, the same way iteration 3
  found `sed -i` refused. Undo a temporary mutation with a second `Edit`, then confirm with
  `git status --short` that only the intended file is modified. That confirmation is not
  optional when the mutation touched four files.
- **`templateComment` (`partials_test.go:1510`) is reusable from `stylesheet_test.go`** — same
  package, internal test — and `renderedClasses` already uses it. A `{{/* … */}}` comment
  naming `maximum-scale` is therefore not a failure, which is right: the comment cannot reach
  a browser.
- All five gate commands green; linter is **2.12.2**, so the green is real. `go test ./...`
  clean, all three tagged suites compile. `-tags quickstart` was not *run*: this task touches
  one test file and no `cmd/crswd`.

**Left:** T006 next — delete `overflow-x: auto` from `.settings` and add it to
`.settings-panel`. It **must land before T008**, and it starts the settings phase, which is
the surface the operator actually reported. 12 of 17 tasks open.

**Findings — noticed, not fixed:**

1. **This test lives in `stylesheet_test.go` and that file's header says it does not do
   this.** The header (line 1) draws the division as "partials_test.go sweeps the embedded
   template tree; this one covers the other embedded tree" — and `TestNoPageClampsTheZoom`
   walks templates. I put it beside the four other pane assertions because `contracts/pane.md`
   names `stylesheet_test.go` in its file list and lists all five tests together, and because
   a reader auditing the wrap trade should find its mitigation next to it. `renderedClasses`
   already crosses the same line. But the header comment is now inaccurate by two callers, and
   whoever next edits that header should either widen it or move this test to
   `partials_test.go`. Not worth a commit on its own.
2. **Nothing asserts that a page has a viewport meta — only that the tree has at least one.**
   A new page shipping without one would lay out at desktop width on a phone and this guard
   would stay green on the strength of the other four. The per-page version already has a
   pattern to copy: `TestEveryPageLoadsTheLoopThatDrivesItsRain` (`partials_test.go:1615`)
   filters `path.Dir(p) != "templates"` to get pages rather than partials. I did not add it
   because T005 specifies the clamp and the constitution's second principle says not to invent
   the rest — but it is a one-line addition to this test whenever someone decides that rule.
3. **The trade this guard protects is still unverified on a real device.** Question 1 in
   `docs/mobile-open-questions.md` is UNANSWERED and must stay so. Worth restating here
   because T005 completes the pane's test coverage, and "every pane task is green" is exactly
   the moment a later iteration might read the phase as settled. It is not: green proves five
   declarations exist, and nothing in this repository has a thumb.

---

## Iteration 6 — 2026-08-09 08:46

**Did:** T006. Deleted the whole `.settings { overflow-x: auto }` rule (was `crswd.css:1165`)
and added `overflow-x: auto` to `.settings-panel` (now `crswd.css:1394`), unconditionally.
Shipped `TestWideSettingsPanTheirOwnPanel` (`stylesheet_test.go:2317`) asserting both halves.
Nothing in the 780px block was touched, so the design-system enumeration table owes no row.

**Learned, so the next iteration does not rediscover it:**

- **There are THREE rules whose selector is exactly `.settings`, and the plan's line numbers
  only name two of them.** `crswd.css:1079` (inside the 780px block, `grid-template-columns:
  1fr`), the deleted one at 1165, and `1335` — the grid wrapper proper, `display: grid` with
  the menu/panel columns. The one at 1165 was a header for the *element-selector* block
  (`.settings table`, `.settings th/td`, `.settings caption`, `.settings p`) and sat 170 lines
  above the rule that actually creates the wrapper. **T015 is the task that goes through those
  element selectors**, and it should know that their shared comment block is now the first
  thing under that heading, because this task removed the rule it was attached to.
- **`blockFor(t, source, ".settings")` returns the media-block rule, not either top-level
  one** — 1079 is the first occurrence in the file. That is why the new test sweeps `cssRules`
  and compares the comma-split selector to `.settings` exactly, the same shape
  `TestThePaneDoesNotTrapVerticalScrolling` uses. Any later assertion about "the settings
  wrapper" must do the same or it will silently be reading the breakpoint override. **T007
  reads `.settings-menu` and `.settings-menu-list` from inside the 780px block and should use
  the two-step `blockFor(media)` → `blockFor(selector)` nesting T004 established, not
  `blockFor` on the bare selector.**
- **`blockFor(t, source, ".settings-panel")` is safe and stays safe**, because `stylesheet()`
  strips comments before anything reads it (`cssComment`, `stylesheet_test.go:74`) — so the
  new comment above `.settings table`, which now names `.settings-panel` in prose, cannot be
  what `strings.Index` finds. Worth knowing generally: prose in this file is invisible to
  every guard, in both directions.
- **Both negatives proved.** Adding `overflow-x` back to the `.settings` grid rule at 1335
  while leaving the panel's copy in place fails the sweep half naming the wrapper's body —
  that is the mutation the contract calls out, because it renders identically to before the
  task and fixes nothing. Removing it from the panel fails the other half. Temporary
  mutations done with `Edit` and undone with `Edit`, per iterations 3–5.
- All gate commands green; linter is **2.12.2**, so the green is real. `go test ./...` clean,
  all three tagged suites compile (`go vet -tags tmux|quickstart|dev`). `-tags quickstart` was
  not *run*: this task touches one CSS file and one test file, no `cmd/crswd`.

**Left:** T007 next (the section menu becomes a scrolling row, inside the 780px block — and it
owes the design-system enumeration table a row, which T004 is the precedent for). 11 of 17
tasks open.

**Findings — noticed, not fixed:**

1. **The stale comment above the 780px block that iteration 4 flagged is still stale, and
   T006 was not the task to fix it** — this task added no rule to that block. T007, T008, T012
   and T013 all do. The first of them to open that block should either delete the enumeration
   from the comment or point it at the design-system table. Restating it because it has now
   survived two iterations that each had a reason not to be the one.
2. **`.settings-menu` sets `background: var(--glow)` at `crswd.css:1386` — the same live bug
   T014 fixes at 1339 and 1352.** T014's task text names two line numbers and this is a third
   occurrence of the identical defect (a shadow list spent as a background is dropped at
   computed-value time, so the menu bar has never had a surface). The plan's Resolved
   decisions section says "the settings menu has never had a surface", so this is known — but
   the *task* names `1339` and `1352`, which are `.settings-menu-link[aria-current]` and
   `.settings-menu`'s neighbours, and after this commit every line number in T014 has shifted
   by +6. **T014 must find these by selector, not by line.** Its own
   `TestNoBackgroundSpendsAShadowToken` will catch any it misses, provided that test sweeps
   the file rather than the two rules named.
3. **Nothing asserts that the panel's scroll container is inside the grid rather than around
   it.** The new test proves `.settings-panel` scrolls and `.settings` does not, but a future
   edit that moved the menu inside the panel would satisfy both assertions and reintroduce the
   exact fault. That is a template change and G5 would not see it either — no new class is
   involved. It is a theoretical regression rather than a likely one, and the honest note is
   that this guard checks which *rule* carries the property, never which elements the rule
   ends up wrapping.

---

## Iteration 7 — 2026-08-09 08:57

**Did:** T007. The section menu is a scrolling row below the breakpoint —
`.settings-menu { position: static }`, `.settings-menu-list { grid-auto-flow: column;
justify-content: start; overflow-x: auto }`, `.settings-menu-link { white-space: nowrap }`,
and the `aria-current` marker moved from `border-inline-start` to `border-block-end`. Four
rows added to the design-system enumeration table, plus a third "how it is written" rule.
Three contract tests shipped, plus a fourth that had to exist first — see below.

**AND: the `@media (max-width: 780px)` block was moved, unchanged, to the end of the
stylesheet.** This was not optional decoration and it is the headline of the iteration.

**Learned, so the next iteration does not rediscover it:**

- **THE BLOCK WAS ABOVE THE RULES IT OVERRIDES, AND HALF OF IT HAS NEVER DONE ANYTHING.**
  A media query adds **no specificity**. At HEAD the block sat at `crswd.css:1061` and every
  settings rule was below it — `.settings` at 1332, `.settings-menu-list` 1339,
  `.settings-menu-link` 1347, `[aria-current]` 1367, `.settings-menu` 1378. So
  `.settings { grid-template-columns: 1fr }` inside the breakpoint lost the tie to
  `grid-template-columns: minmax(var(--menu-min), var(--menu-max)) 1fr` 250 lines further
  down. **The settings page has never been one column on a phone**: it has been rendering a
  10rem-minimum menu column beside the table in a 390px viewport. That is very likely the
  whole of *"seeing settings is tricky right now as well"*. `.shell`, `.summary`,
  `.brand-tag` and `.pane` were unaffected — their base rules are at 235, 290, 378 and 910,
  all above 1061 — which is exactly why this survived: **four of the five rules worked, so
  the block looked fine.**
- **T007 as written would have been inert in the same way.** `position: static` would have
  lost to `position: sticky` at 1378, and `border-inline-start: none` to the base marker at
  1367. Both would have parsed, passed every guard in the milestone, and changed nothing —
  the failure the plan names for T010 and the notebook has recorded four times.
- **The fix is to move the block, not to fight it.** Relocating it below `.check-line` and
  above `[hidden]` keeps *one* width query, so `TestTheDashboardHasExactlyOneBreakpoint`
  is untouched, and the rules inside are byte-identical. Checked before moving: no test
  calls `blockFor(".settings")` (which used to return the media-block rule and now would
  return `.settings table`'s), `blockFor(".pane")` still finds the base rule at 910, and
  `TestHiddenAlwaysWins`'s trailing-`display` regex is anchored on `\n\.` so indented rules
  inside a media block never match it.
- **`TestTheBreakpointOverridesRatherThanPrecedes` is the guard this needed and did not
  have.** It is written **per selector, not per block**: it collects the selectors declared
  inside the breakpoint and fails if any of them is declared again *after* the block. That
  catches the real hazard, which is a base rule declared low in the file rather than the
  block having moved. Proved by mutation two ways — a stray `.settings-menu { position:
  sticky }` after the block, and a faithful reconstruction of the historical layout (a
  duplicate two-column `.settings` after the block), which fails naming `.settings` and the
  exact declaration that beat it.
- **T010 inherits this problem and iteration 1's note about it is now wrong.** Iteration 1
  said the coarse block "goes after 1067's closing brace" (the reduced-motion block). That
  would put it **above** `.setting-input` (now ~1417), `.field-input` and the button rules
  it is meant to override — inert, exactly as this was. **T010's coarse block belongs at the
  end of the file, next to the width block.** Its own
  `TestTheCoarseBlockOverridesRatherThanPrecedes` should be written the same per-selector way
  rather than as "after the reduced-motion block", which is an assertion about the wrong
  thing.
- **`ruleFor(t, source, selector)` is new, in `stylesheet_test.go`, and is blockFor's
  exact-match sibling.** `blockFor` takes the first `strings.Index` of a marker, and on this
  page that is a coin toss: `.settings` is a prefix of `.settings table`, `.settings-menu`
  and `.settings-panel`; `.settings-menu` is a prefix of `.settings-menu-list` and
  `-link`. Iterations 3, 4 and 6 each had to reason about this by hand. Use `ruleFor` for
  any settings selector.
- **`TestTheSettingsMenuIsStillLinks` went in `partials_test.go`, not `stylesheet_test.go`**,
  against the contract's file list. It reads rendered markup, `partials_test.go` already
  owns `renderComponent`/`anchorTo`/`cardAnchor`, and iteration 5's finding 1 asked for the
  division to be respected going forward. Reusing those helpers was the deciding factor.
- All gate commands green; linter is **2.12.2**, so the green is real. `go test ./...` clean,
  all three tagged suites compile. `-tags quickstart` was not *run*: CSS, two test files and
  one doc, no `cmd/crswd`.

**Left:** T008 next (stack the setting rows — `clip-path: inset(50%)`, **never** the `1px`
recipe). T006 has landed, so its ordering constraint is satisfied. 10 of 17 tasks open.

**Findings — noticed, not fixed:**

1. **`crswd.js` may have a scroll assumption that the sticky menu used to satisfy, and nobody
   checked.** T007 removes `position: sticky` below 780px. I did not read `crswd.js` for
   anything keyed to the menu's position, because nothing in the contract suggested one and
   the class sweep says the menu is styled from CSS only. If a later task touches the script,
   it is worth a grep for `settings-menu`.
2. **The stale comment above the 780px block that iterations 4 and 6 both flagged is now
   fixed** — it no longer enumerates and points at the design-system table instead, which is
   the copy with an obligation attached. Recording it here so a third iteration does not go
   looking for it. The enumeration duplication is gone, so the "adding a rule adds a row"
   rule now has exactly one place to be obeyed.
3. **`TestTheBreakpointOverridesRatherThanPrecedes` compares selectors as strings, so it
   cannot see a *more specific* rule declared later.** `.settings-panel .settings-menu` after
   the block would beat the narrow rule and this guard would not notice, because the selector
   text differs. Real cascade specificity is not something a regex sweep can compute. The
   guard covers the exact-tie case, which is the one that has actually happened here twice
   (`.settings`, and `.pane` narrowly avoided in iteration 4). Worth knowing before someone
   trusts it further than that.
4. **The one-column collapse is now live for the first time, and no human has seen it.** This
   iteration changed what the settings page *renders* on a phone in a way no previous
   milestone-7 task did — T003–T006 added or moved declarations that were already reachable.
   Nothing in this repository renders CSS. The menu row, the stacked panel and the marker on
   the bottom edge are all unverified against a real device, and question 3 in
   `docs/mobile-open-questions.md` (does the scrolling menu disorient when the current chip
   starts offscreen?) is now a question about something that actually happens rather than a
   hypothetical. It must stay UNANSWERED; T017 verifies that.

---

## Iteration 8 — 2026-08-09 09:03

**Did:** T008. Setting rows stack below the breakpoint — `.settings-table tr { display: grid;
grid-template-columns: minmax(0, 1fr) }`, `.settings-table thead` clipped with
`position: absolute; clip-path: inset(50%); overflow: hidden`, and `overflow-wrap: anywhere`
on the cells. All three inside the existing 780px block, after the menu rules, before its
closing brace. Two contract tests shipped: `TestSettingRowsStackOnNarrowViewports` and
`TestTheHeadersAreHiddenAccessiblyNotRemoved` (`stylesheet_test.go:2527` onward). Three rows
added to the design-system enumeration table, per the obligation T004 and T007 set.

**Learned, so the next iteration does not rediscover it:**

- **The `1px` trap is real and fires exactly as the plan says.** Mutating the thead rule to
  the conventional visually-hidden recipe (`inline-size: 1px; block-size: 1px`) fails
  `TestNoRuleCarriesAValueThatBelongsInAToken` naming `"1px"` — from *inside* a media block,
  which is the part that is easy to assume is exempt. `inset(50%)` passes because `%` is
  absent from the sweep's unit list (`forbiddenInRules`, `stylesheet_test.go:166`). Confirmed
  by mutation, not by reading the regex.
- **`ruleFor(t, narrow, selector)` — T007's helper — is the whole pattern for a task like
  this**, and it does something worth naming: because it reads the *narrow block's* rules, it
  fatals if the declaration was written at the top level instead. So "I put the rule in the
  wrong place entirely" is caught for free. What it does **not** catch is the rule existing in
  both places; see finding 1.
- **`.settings-table` was already a rendered class, so G5 never came near this.** Verified
  against `settings.html:143` before writing a selector, per the contract's instruction to
  confirm the class name. The three new selectors are that class plus *element* selectors, so
  nothing new enters `styledClasses` — which is the same blindness T015 is warned about from
  the other direction.
- **`TestHiddenAlwaysWins`'s trailing-`display` regex is anchored on `\n\.`**, so `display:
  grid` on an indented `.settings-table tr` inside the media block cannot trip it. Iteration 7
  established this; T008 is the first task to actually put a `display` in that block since,
  and it holds.
- **`border-collapse: collapse` on `.settings-table` stops applying to a row that is
  `display: grid`.** The per-cell `border-block-end` on `.settings th/td` is what draws the
  separation in the stacked layout, and it happens to be the right thing — each of key, value
  and source gets an underline, so the three lines read as three fields rather than a
  paragraph. That is a consequence, not a decision anyone made; it is worth knowing before
  someone "tidies" the base border rules.
- All gate commands green; linter is **2.12.2**, so the green is real. `go test ./...` clean,
  all three tagged suites compile (`go vet -tags tmux|quickstart|dev`). `-tags quickstart` was
  not *run*: this task touches one CSS file, one test file and one doc, no `cmd/crswd`.

**Left:** T009 next (rewrite `settings.html`'s header comment, which claims the page has no
form, no token, no action row and no live region — it has all four). That is the last task of
the settings phase, and the plan's "Shippable at T009" line means the reported surface is
complete after it. 9 of 17 tasks open.

**Findings — noticed, not fixed:**

1. **Nothing stops these rules being declared at the top level *as well*, which would take the
   desktop table apart.** The pane pair got `TestThePaneKeepsItsDesktopAlignment` for exactly
   this (iteration 4: the misplaced declaration renders identically on a phone and costs every
   desktop reader). `contracts/settings.md` lists two tests for part 3 and neither is that one,
   so I did not invent a third — the constitution's second principle. The shape of the fix if
   anyone wants it is one `ruleFor`-style sweep asserting no top-level `.settings-table tr`
   rule sets `display`. This is now the **second** milestone-7 rule pair with no
   desktop-side guard.
2. **`TestTheOpeningScreenIsSentWithoutWaitingOutAnInterval` (`stream_test.go:991`) is flaky
   under load.** It failed once during this iteration — *"the opening screen arrived 17ms after
   the open, which is past the 10ms interval"* — then passed on a plain re-run of the full
   suite and five consecutive `-count=5` runs in isolation. It is a wall-clock assertion with a
   10ms budget, so a loaded machine fails it and nothing is wrong with the daemon. Unrelated to
   this task (CSS and a test file cannot affect stream timing except as load). Worth a real fix
   — the budget should be relative to the interval rather than an absolute 10ms — but that is
   `internal/httpapi/stream_test.go` and outside this milestone. **If a future iteration sees
   this fail, re-run before believing it.**
3. **The stacked layout makes open question 2 concrete, the way iteration 7 made question 3
   concrete.** *"Does a bare provenance word read as part of the value once rows stack?"* was
   hypothetical until this commit; `default` or `file` now literally sits on its own line
   beneath a value with its column header clipped away. The `th` still carries the name for a
   screen reader — that is what `inset(50%)` buys — but a sighted operator gets no label at
   all. Its fallback (render an explicit label in the row) is specced and not built, and the
   question must stay UNANSWERED. T017 verifies that; this iteration did not touch
   `docs/mobile-open-questions.md`.

---

## Iteration 9 — 2026-08-09 09:11

**Did:** T009. Rewrote `settings.html`'s header comment: the page acts as well as reads, it
carries mutating forms with `crsw_page_token` in each, the controls that submit them, and the
live region at the foot of the file; `POST /settings/edit` and `POST /dashboard/update` are the
two routes that receive them, while `GET /settings` is still the only verb on the path itself.
Added the paragraph the rewrite actually needs — the absence of a route *was* the safeguard and
is not any more, so the bound is the action gate plus `config.Editable`. Shipped
`TestTheSettingsCommentDescribesThePage` (`partials_test.go:3265` onward), which the contract
lists and the task text does not. That closes the settings phase: the plan's "Shippable at
T009" line is now true.

**Learned, so the next iteration does not rediscover it:**

- **A regex over a template comment must unwrap it first, and this is not hypothetical.**
  Restoring the false paragraph verbatim to prove the negative fired three of four claims —
  the page-token one evaded it because the sentence wraps as `carries no page\n  token`. One
  `strings.Join(strings.Fields(header), " ")` and all four fire. **Any future assertion about
  prose in this repo's templates or docs has this hole**; the comments are hand-wrapped at
  ~78 columns, so a two-word phrase straddles a line roughly one time in six.
- **The header comment is separable from the markup at the doctype**, which is why the test
  does not sweep every `{{/* … */}}` in the file. Two comments further down say "carries no
  token" *truthfully* — the check-for-updates link is a GET and carries none by design. A sweep
  of the whole file would have to distinguish those, and it cannot.
- **The contract and the task text disagree about whether T009 ships a test.**
  `contracts/settings.md` lists `TestTheSettingsCommentDescribesThePage` in its Contract tests
  table with a "Must fail when"; `tasks.md:424` says *"Guards: none. Every sweep strips
  comments, so the suite is inert to this."* Both are right about different things — no
  *existing* guard can see a comment, which is exactly why this one had to be written. I
  shipped it: it is named in the contract, so it is not invented (iteration 8 declined a test
  that was not).
- **`fieldPageToken` (`browser.go:336`) is the constant to build a token assertion from**, not
  the literal — `dashboard_test.go:1553` already pins its spelling to `contracts/actions.md`,
  so a rename fails there once instead of everywhere.
- All gate commands green; linter is **2.12.2**, so the green is real. `go test ./...` clean on
  a re-run (see finding 2), all three tagged suites compile. `-tags quickstart` was not *run*:
  this task touches one template comment and one test file, no `cmd/crswd`.

**Left:** T010 next — the `@media (pointer: coarse)` block, which the plan flags as the
milestone's riskiest task and which **iteration 7 corrected the placement for**: it goes at the
**end of the file** beside the width block, *not* after the reduced-motion block as iteration 1
said, or it sits above `.setting-input`/`.field-input` and is inert. Write its offset assertion
per-selector, the way `TestTheBreakpointOverridesRatherThanPrecedes` is written. 8 of 17 tasks
open.

**Findings — noticed, not fixed:**

1. **The same false claim is still live in two Go files and one doc, and T009 did not cover
   them.** `docs/components.md:56-66` ("The settings page has no component of its own") says the
   page "deliberately carries **none** of the three things every actionable page in this tree
   carries: no page token, no action row, no live region" and that "a form rendered there would
   be a form the daemon has no route to receive". `server.go:569-578` says "no mutating verb is
   registered on the path at all" and "editing the operator's file from a browser is out of
   scope this milestone". `settings.go:3` and `:388` call it "the read-only account". All four
   are the identical defect T009 exists to fix, in files T009 does not name — AR-008 and the
   constitution's fourth principle say not to fix them from inside this task. **T016 updates
   `docs/components.md`** and is the natural home for the first; the two Go comments have no
   task in this milestone and need one, or a fix-lane line in `docs/fixes-log.md`. The new test
   only reads `settings.html`, so nothing catches these.
2. **`TestTheLeakSuiteReallyDrivesTheDaemon` (`internal/audit/leak_test.go:1601`) is
   time-flaky, and the cause is certain rather than suspected.** It failed once during this
   iteration — *"the card a create rendered back carries no page token"* — then passed three
   times in isolation and on a full re-run. `pageKey.mint` (`pagetoken.go:156`) builds the token
   from `now.Add(12h).Unix()`, so **two renders one second apart produce different tokens**, and
   the test asserts the token from one render appears verbatim in a card from a later one. It
   passes only while both renders land inside the same wall-clock second. This is the second
   load-sensitive test the notebook has recorded (iteration 8 found
   `TestTheOpeningScreenIsSentWithoutWaitingOutAnInterval`), and unlike that one it is not a
   timing budget that could be widened — the fix is to assert the card carries *a* verifiable
   token rather than the *same* string. Outside this milestone. **Re-run before believing it.**
3. **The rewritten comment now describes a page whose narrow layout no human has seen.**
   It says the controls "sit in the row or the panel they act on", which after T008 means a
   stacked block on a phone. That is the arrangement open question 2 is about — a bare
   provenance word under a value with its header clipped — so the comment is accurate about the
   markup and silent about whether it reads. Deliberate: a comment is not where an unanswered
   question gets answered. `docs/mobile-open-questions.md` was not touched.

---

## Iteration 10 — 2026-08-09 09:21

**Did:** T010, the milestone's flagged-riskiest task. The `@media (pointer: coarse)` block —
`.button` and `.field-switch` to `min-block-size: var(--tap)`; `padding-block: var(--s3)` on
`.settings-menu-link`, `.combo-list li`, `.rename-summary` and `.release-notes summary`;
`.masthead-link` padded with the height given back by a negative `margin-block`;
`.card-actions` gap widened to `--s3`. Placed **after `.check-line` and before the width
block**, so it is below every rule it overrides. Five contract tests shipped
(`stylesheet_test.go:2634` onward) plus an enumeration table in the pointer section of
`docs/design-system.md`, carrying the same "a rule adds a row" obligation the width table does.

**Learned, so the next iteration does not rediscover it:**

- **The coarse block cannot go after the width block, and iteration 9's note stopped one step
  short of saying so.** Iteration 9 correctly said "end of the file, beside the width block"
  rather than after reduced-motion. But *which side* matters:
  `TestTheBreakpointOverridesRatherThanPrecedes` fails if any selector the width block declares
  is declared again below it, and the coarse block declares `.settings-menu-link` — which the
  width block also declares, for `white-space`. So coarse goes **immediately before** the width
  block. Both are then below every base rule, and the two blocks share no property on that
  selector, so the order between them is behaviourally free.
- **That collision is also why the new offset assertion is per *property* and not only per
  selector.** A copy of `TestTheBreakpointOverridesRatherThanPrecedes`'s exact shape would have
  failed on `.settings-menu-link` on the day it was written. `TestTheCoarseBlockOverrides
  RatherThanPrecedes` is therefore two halves: (a) per selector — a selector the block names
  that is declared below it and *nowhere above* is the block sitting too high; (b) per property
  — a rule below that sets a property the block set. They are complementary: (a) cannot see a
  new base rule added below because the selector exists in both places, and (b) cannot see a
  selector whose only rule moved below without a property clash.
- **`propertiesCollide` is a prefix test in both directions, and it is load-bearing rather than
  fussy.** `.settings-menu-link` sets `padding` and the block sets `padding-block` on it — a
  later `padding` resets the longhand entirely while sharing not one character of its name. A
  plain string equality would have read that as no collision. Proved: adding
  `.settings-menu-link { padding: var(--s2) var(--s3) }` inside the width block fails, naming
  both properties.
- **The headline negative was proved with the real mutation.** Moving the whole block (comment
  and all) above `.masthead` fails naming **all seven** selectors and then fatals on the
  vacuity guard — "no selector the pointer block names has a rule above it". Three more
  proved: `display: grid` in the block fails `TestTheCoarseBlockChangesNoLayout`;
  `min-block-size: var(--tap)` on `.card-name` inside the **width** block fails
  `TestTouchTargetsFollowThePointerNotTheWidth` (the plausible wrong answer, which looks right
  on whatever phone the author was holding); and a `.button` rule that sizes something else
  fails `TestEveryButtonIsThumbSized`.
- **`ruleFor(t, source, sel)` on the whole file is a positional bet the moment a selector is
  declared twice, and this task created the second one.** During the move-the-block proof
  `TestDestroyIsSeparatedFromCompact` failed for the *wrong reason*: `ruleFor` returned the
  coarse `.card-actions` as "the base rule" and compared it to itself. Fixed by reading the
  base from `source[:strings.Index(source, coarse)]`. **T011 adds a second `.field-input` and a
  second `.setting-input`** — any assertion about their base rules has exactly this hole.
- **`.masthead-link` is a flex item, so `padding-block` and the negative `margin-block` both do
  what the comment claims.** It is an `<a>` and vertical padding on an *inline* box grows the
  paint area without moving layout, which would have made the negative margin a no-op; but
  `.masthead-bar` is `display: flex` (`crswd.css:177`), so the link is blockified and the two
  declarations cancel as described. Checked rather than assumed.
- All gate commands green; linter is **2.12.2**, so the green is real. `go test ./...` clean
  first time, all three tagged suites compile (`go vet -tags tmux|quickstart|dev`).
  `-tags quickstart` was not *run*: one CSS file, one test file, one doc, no `cmd/crswd`.

**Left:** T011 next — `font-size: var(--fs-input)` for `.field-input` **and** `.setting-input`
in the block this iteration created. It is the second half of the same contract and the
smallest task remaining; the doc table owes it a row. 7 of 17 tasks open.

**Findings — noticed, not fixed:**

1. **`TestTheCoarseBlockChangesNoLayout` reads the block's declarations, not what they do.**
   `min-block-size` is on its allow-list and is a sizing property by name — but on a flex or
   grid *container* a minimum size changes how its children are distributed, which is layout by
   any honest reading. `.field-switch` is `grid-auto-flow: column` and now carries a minimum
   height. The policy the test enforces is "no layout *property*"; the policy
   `docs/design-system.md` states is "changes no layout". Those are not the same sentence, and
   the gap is not closable by a regex. Worth knowing before someone reads a green as proof the
   policy holds.
2. **Two milestone-7 rule pairs still have no desktop-side guard, and this task adds a third
   kind.** Iterations 4 and 8 recorded that `.pane` and `.settings-table tr` have no assertion
   that the narrow declaration is *absent* from the base rule. The coarse block's equivalent is
   that nothing stops `min-block-size: var(--tap)` being added to the base `.button` as well,
   which would give every mouse user a 44px button and pass all five new tests. The shape of
   the fix is the same one-rule sweep in all three places.
3. **The block is now the second thing in this milestone that changes what a phone renders and
   that no human has seen** — iteration 7's one-column settings collapse was the first. Every
   control on a touch device is a different size as of this commit. None of the three open
   questions is about target size, so this does not touch
   `docs/mobile-open-questions.md`; but "green" here means five declarations exist in the right
   order, and nothing in this repository has a thumb.
4. **`docs/components.md` now describes button and action-row geometry that is conditional.**
   G6 was satisfied because every name the block spends is already documented — but the
   component prose gives sizes as though there is one. T016 is the task in that file and its
   scope is one item larger than written, which is the same note iteration 3 left about the
   pane's scroll container.

---

## Iteration 11 — 2026-08-09 09:26

**Did:** T011, the second half of T010's contract. `font-size: var(--fs-input)` on
`.field-input` **and** `.setting-input` inside the `@media (pointer: coarse)` block, plus
`TestInputsDoNotTriggerFocusZoom` (`stylesheet_test.go:2831` onward) and the row the pointer
table in `docs/design-system.md` owes for it. Three files, one commit, no template touched.

**Learned, so the next iteration does not rediscover it:**

- **Iteration 10's warning about a second `.field-input`/`.setting-input` did not bite, and it
  is worth knowing why rather than assuming it went away.** Nothing in the suite reads either
  base rule — `grep` finds them only in `partials_test.go:1998`, as markup. So the positional
  `ruleFor` hazard iteration 10 hit on `.card-actions` had no equivalent here. **It is still
  loaded for the next task that wants a base `.field-input` rule**: the file now declares each
  of these twice, and `ruleFor` on the whole source returns whichever comes first.
- **The value sweep does double duty on this task and catches the plausible wrong answer for
  free.** Spelling the rule `font-size: 16px` fails `TestNoRuleCarriesAValueThatBelongsInAToken`
  *and* `TestInputsDoNotTriggerFocusZoom`, because the latter matches on `var(--fs-input)` and
  not on the property. So a respell of the number cannot pass as the token — no separate
  "is it the token" assertion was needed, unlike `TestDestroyIsSeparatedFromCompact`, which had
  to compare against the base rule because `gap: var(--s2)` and `gap: var(--s3)` are both
  tokens.
- **`.field-input` sets the `font` shorthand and the override is the `font-size` longhand.**
  That works only on order, and order is what holds it: `propertiesCollide` reads
  `font`/`font-size` as a collision by its prefix test, so if a base `.field-input` rule is ever
  added *below* the pointer block, `TestTheCoarseBlockOverridesRatherThanPrecedes` fires rather
  than the size silently reverting. Checked, not assumed.
- **Neither selector appears in the width block**, so the pointer block's placement immediately
  before it stayed free — no property collision to arbitrate, unlike `.settings-menu-link`.
- Both negatives proved by mutation: dropping `.setting-input` fails naming `.setting-input`;
  dropping `.field-input` and inlining `16px` fails naming `.field-input`, `.setting-input`
  *and* the sweep. All gate commands green; linter is **2.12.2**, so the green is real.
  `go test -count=1 ./internal/httpapi` clean after the restore, all three tagged suites
  compile. `-tags quickstart` was not *run*: one CSS block, one test, one doc row, no
  `cmd/crswd`.

**Left:** T012 next — wrap `.card-name` / `.card-path` inside the 780px block, because a `title`
attribute needs a hover a phone does not have. T013 is parallel to it. 6 of 17 tasks open;
US3/US4 (touch) is now complete.

**Findings — noticed, not fixed:**

1. **This closes the touch phase, and every finding iteration 10 left about it still stands
   unchanged.** In particular finding 2 — nothing stops a `font-size: var(--fs-input)` being
   added to the *base* `.field-input` as well, which would give every mouse user a 16px field
   and pass all six tests in the coarse suite. That is now the fourth milestone-7 rule pair with
   no desktop-side guard (`.pane`, `.settings-table tr`, `.button`, and these two inputs), and
   FR-016 is the requirement it maps to: *"Control sizes and input sizes MUST be unchanged on a
   mouse-operated pointer."* **No task in this milestone asserts FR-016 at all.** One sweep —
   for each selector the coarse block names, the base rule must not set the property the block
   sets to the same value — would cover all four at once. It has no task; it is either a T016-
   adjacent addition or a fix-lane item, and it is the largest hole this phase leaves.
2. **The commit message claims a behaviour change on a device nothing here has.** Third in a
   row: iteration 7's one-column collapse, iteration 10's target sizes, and now every text input
   on a phone. Green means the declaration exists in the right block below the rules it
   overrides. Whether a 16px mono field in a 390px viewport still fits the working-directory
   paths it is asked to hold is not something this repository can find out. None of the three
   open questions is about input scale, so `docs/mobile-open-questions.md` was not touched —
   but the create form's `.field-input` is the widest content on the narrowest page, and that is
   the nearest thing to a fourth question the milestone has produced.
3. **`docs/components.md:311-316` shows a `.field-input` example with no note that its size is
   now pointer-conditional** — the same shape as iteration 10's finding 4 about button geometry.
   T016 is the task in that file; its scope is now *two* items larger than written (the pane's
   scroll container, button/action-row geometry, and input scale).

---

## Iteration 12 — 2026-08-09 09:31

**Did:** T012. `.card-name, .card-path { white-space: normal; overflow-wrap: anywhere }` at the
end of the 780px block, base rule left ellipsizing. Both contract tests shipped —
`TestCardTextWrapsOnNarrowViewports` and `TestTheCardKeepsItsDesktopTruncation`
(`stylesheet_test.go:2884` onward) — plus the row the design-system enumeration table owes.

**Learned, so the next iteration does not rediscover it:**

- **The card grid is already one column on a phone, and it is intrinsic rather than a rule.**
  `.grid` is `repeat(auto-fill, minmax(var(--card-min), 1fr))` with `--card-min: 310px`; two
  tracks need `310*2 + --s4` = 636px of content, and `.shell` spends `--s4` per edge below the
  breakpoint, so a second track needs a **~668px viewport**. That is *below* the 780px
  breakpoint, so between 668 and 780 the cards are still two columns while the wrap is
  live — harmless, because a grid row sizes to its tallest track, but it means "one column
  makes variable heights harmless" (the wording in `contracts/hygiene.md`) is only true under
  668px. The comment in the file says what is actually true. **T013 is in the same region and
  the same arithmetic applies to the masthead's alignment claim** — `.shell` at `--s4` is what
  `.masthead-bar` is being brought into line with.
- **`ruleFor` over the whole file was the wrong tool for the base rule and the right one for
  the narrow rule.** As of this commit `.card-name` is declared twice, so the desktop test
  reads `source[:strings.Index(source, breakpointPrelude)]` — iteration 10's `.card-actions`
  lesson, applied before being bitten this time rather than after. The wrap test uses
  `ruleFor(t, narrow, sel)`, which fatals if the declaration were written at the top level
  instead, so "wrong place entirely" is caught for free (iteration 8's note).
- **Asserting per selector rather than on the selector list is what makes the half-fix fail.**
  Dropping `.card-name` and keeping `.card-path` fails naming `.card-name` exactly —
  the same shape `TestInputsDoNotTriggerFocusZoom` needed for its two forms. A single
  `blockFor` on the rule body would have passed that mutation.
- **All three negatives proved by mutation.** (a) The base rule edited instead of overridden —
  `white-space: normal` + `overflow-wrap: anywhere` moved up to line 450 — fails
  `TestTheCardKeepsItsDesktopTruncation` naming both selectors **while the wrap test stays
  green**, which is the whole point: that mutation renders identically on a phone and costs
  every desktop card its row height. (b) `white-space: normal` alone — the plausible half-fix —
  fails the second assertion, because a working-directory path has no break opportunity in it.
  (c) One selector covered, the other missed — fails by name. Mutations done with `Edit` and
  undone with `Edit`, per iterations 3–11.
- All gate commands green; linter is **2.12.2**, so the green is real. `go test ./...` clean
  first time, all three tagged suites compile (`go vet -tags tmux|quickstart|dev`).
  `-tags quickstart` was not *run*: one CSS rule, one test file, one doc row, no `cmd/crswd`.

**Left:** T013 next — `padding-inline: var(--s4)` on `.masthead-bar` and `flex: 1 1 0` on
`.operator`, inside the same block, and it owes that table two more rows. It is the last of the
reachability phase. 5 of 17 tasks open.

**Findings — noticed, not fixed:**

1. **This is the fifth milestone-7 rule pair, and the first one that got its desktop-side
   guard.** Iteration 11's finding 1 named four pairs with no assertion that the narrow
   declaration is absent from the base rule (`.pane` — which does have one — plus
   `.settings-table tr`, `.button`, and the two inputs). `contracts/hygiene.md` specified
   `TestTheCardKeepsItsDesktopTruncation` for this pair, so it exists here by contract rather
   than by invention. The gap is unchanged for the other three, and **FR-016 still has no
   assertion anywhere in the milestone**. Worth noting *why* the cards got one and the coarse
   block did not: the contract that specified the touch block did not ask for it. That is a
   contract gap, not a discipline gap, and it is fixable in one sweep as iteration 11
   described.
2. **The `title` attribute is now redundant on a phone and still load-bearing on a desktop, and
   nothing says so.** `session-card.html:67` and `:116` render `title="{{ .Name }}"` /
   `title="{{ .WorkDir }}"`. Below the breakpoint the visible text is the whole value, so the
   tooltip duplicates what is on screen — harmless, but a future reader may take the attribute
   as the reason the truncation is acceptable and not notice it stops applying at 780px. A
   template comment would say it; T012 is a CSS task and AR-008 says not from here.
3. **`docs/components.md` describes the session card's name and path as truncated, full stop.**
   Same shape as iterations 3, 10 and 11: T016 is the task in that file and its scope is now
   *three* items larger than written (the pane's scroll container, button/action-row geometry,
   input scale, and now card text truncation). That is four notes pointing at one task; T016
   should be read as "reconcile the document with everything milestone 7 made conditional",
   not as the two prose edits its title names.

---

## Iteration 13 — 2026-08-09 09:37

**Did:** T013, the last of the reachability phase. `.masthead-bar { padding-inline: var(--s4) }`
and `.operator { flex: 1 1 0; min-inline-size: 0; text-align: end }` at the end of the 780px
block, plus both contract tests — `TestTheMastheadAlignsWithThePage` and
`TestALongIdentityDoesNotWrapTheBar` (`stylesheet_test.go:2945` onward) — and the two rows the
design-system enumeration table owes. Three files, one commit, no template touched.

**Learned, so the next iteration does not rediscover it:**

- **The alignment assertion reads `.shell`'s own narrow gutter instead of naming `var(--s4)`,
  and that is the difference between holding a relationship and holding a number.** "The header
  is in line with the page" is only true relative to whatever gutter the page spends; a test
  that names the token passes forever if `.shell` later moves to `--s3`. It reads the narrow
  `.shell` rule, `t.Fatal`s if that value has stopped being a single field (the premise it
  compares against would be gone), and otherwise requires the same string. Worth copying for
  any future pair of rules that must agree rather than each be correct.
- **The two rules are spelled with different properties on purpose and the test has to bridge
  it.** `.shell` sets the `padding` shorthand and `.masthead-bar` sets the `padding-inline`
  longhand — the longhand is the right rule here because the block padding must survive, and
  the shorthand would reset it. Two regexes, one per spelling; comparing their *text* would
  have compared nothing.
- **`.operator`'s `min-inline-size: 0` is redundant today and was still written**, because the
  base rule's `overflow: hidden` is what currently disables a flex item's automatic minimum
  size. It is in the contract, and the comment says which property is doing the work — so a
  future change to `overflow` does not silently floor the basis back up to a full email. It is
  deliberately **not** asserted: a test on it would fail a correct simplification.
- **`text-align: end` is asserted, and it is not decoration.** Basis 0 grows the item to fill
  the bar, so without it the address starts at the *left* edge of a box spanning the header —
  `docs/design-system.md`'s second non-negotiable (top-right is always identity) is what the
  wrap fix would otherwise have cost. That mutation passes the flex assertion on its own.
- **Neither selector collides with anything the pointer block names**, so the width block's
  position below it stayed free: `TestTheCoarseBlockOverridesRatherThanPrecedes`'s per-property
  half only looks at selectors the coarse block itself declares, and it declares
  `.masthead-link`, not `.masthead-bar`. Checked rather than assumed — the `.settings-menu-link`
  collision iteration 10 hit is the same shape.
- All three negatives proved by mutation: `var(--s5)` for the gutter fails naming both values;
  `flex: 1 1 auto` with `text-align` dropped fails **both** `.operator` assertions separately;
  renaming the selector so the rule is not in the block fatals in `ruleFor`, which is how "wrote
  it at the top level instead" is caught for free. Mutations done with `Edit` and undone with
  `Edit`, per iterations 3–12.
- All gate commands green; linter is **2.12.2**, so the green is real. `go test ./...` clean,
  all three tagged suites compile (`go vet -tags tmux|quickstart|dev`). `-tags quickstart` was
  not *run*: two CSS rules, two tests, two doc rows, no `cmd/crswd`.

**Left:** T014 next — replace `background: var(--glow)` with `var(--surface)` /
`var(--surface-lift)` at the two settings-menu rules, add `TestNoBackgroundSpendsAShadowToken`,
and update the comment above them. It is the only task in this milestone that fixes a bug
visible on a **desktop**. Then T015 (delete the dead settings rules), T016, T017. 4 of 17 open;
US5/US6 is complete.

**Findings — noticed, not fixed:**

1. **This is the sixth milestone-7 rule pair and the first where the desktop side is genuinely
   unreachable by a guard.** The pattern iterations 11 and 12 tracked — nothing asserts the
   narrow declaration is *absent* from the base rule — does not apply cleanly here: a base
   `.masthead-bar { padding-inline: var(--s4) }` would not be a silent regression, it would be
   a visible 8px change on every desktop. So FR-016's gap is unchanged at four pairs
   (`.settings-table tr`, `.button`, `.field-input`, `.setting-input`) rather than five, and
   the one-sweep fix iteration 11 described still covers all of them.
2. **`.operator` now grows to fill on a phone, and `margin-inline-start: auto` on its base rule
   has nothing left to do there.** The auto margin is what puts identity at the end of a bar
   `space-between` was written for two children; with basis 0 the item takes the free space
   itself and `text-align` positions the text inside it. Both are correct, neither is wrong to
   keep, and the comment at `crswd.css:247` explains the margin as though it is the only
   mechanism. Harmless — but a reader debugging the bar below 780px will find two things
   claiming the same job. AR-008 says not from a CSS task whose scope is the media block.
3. **`docs/components.md:74-80` gives the masthead's classes with no note that two of them
   change below the breakpoint** — the fourth item now pointing at T016 (the pane's scroll
   container, button/action-row geometry, input scale, card truncation, and now the header's
   gutter and the identity's flex). Five notes, one task. T016's title names two prose edits;
   read it as "reconcile the document with everything milestone 7 made conditional" or it will
   ship a document that describes a desktop.
