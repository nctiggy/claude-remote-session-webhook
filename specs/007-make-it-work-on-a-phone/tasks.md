# Tasks: Make it work on a phone

**Feature**: `specs/007-make-it-work-on-a-phone/`
**Branch**: `feat/m7-loop-1`
**Total**: 17 tasks

---

## Read this before T001

This milestone is a stylesheet. **Every failure mode here is a test the task did
not know existed**, so every task below carries its guards inline. You should not
need to open `contracts/guards.md` to find out what can fail you — but it is there
if you want the full inventory.

### The four rules that apply to every CSS task

1. **All width-conditional rules go INSIDE the existing
   `@media (max-width: 780px)` block at `web/static/crswd.css:1041`.** A second
   block fails `TestTheDashboardHasExactlyOneBreakpoint` **even at an identical
   width** — the guard counts occurrences of a width feature, it does not compare
   them.
2. **Every value is `var(--token)`.** `TestNoRuleCarriesAValueThatBelongsInAToken`
   fails a literal length or colour **inside media blocks too** — the sweep strips
   media *preludes*, not media *contents*. Its length pattern is
   `\d+(\.\d+)?(px|rem|em|pt|ch|ex|vh|vw)\b`. Note `%` is **not** in it, so
   `inset(50%)` and `100%` are legal.
3. **New rules go before the terminal `[hidden]` rule** at the end of the file, or
   `TestHiddenAlwaysWins` fails.
4. **Never use media query range syntax** (`(width <= 780px)`). It would slip past
   the breakpoint guard's regex while doing exactly what the guard forbids. That is
   routing around a hook, which `AGENTS.md` prohibits by name.

### Every assertion must read the parsed stylesheet

Use `blockFor(t, source, marker)` (line 1729) or `cssRules(source)` (line 1796) —
the **block the declaration lives in**, never a substring of the whole file.

```go
// CORRECT
pane := blockFor(t, stylesheet(t), ".pane")
if !strings.Contains(pane, "overscroll-behavior-x") { … }

// WRONG — passes when the declaration lands in the wrong rule
if !strings.Contains(stylesheet(t), "overscroll-behavior-x") { … }
```

**Milestone 4 shipped three green tasks while the control they were about went
unchanged.** The wrong form above is how. Note also that `stylesheet()` strips
comments, so no guard can ever be satisfied by writing the declaration in prose.

### The gate, at the end of every task

```bash
golangci-lint version        # MUST print 2.12.2
go build ./... && go vet ./... && go test ./... \
  && go test -tags tmux ./... && go test -tags quickstart ./... \
  && golangci-lint run
```

**v1.62.2 reads the v2 config, runs a subset, and reports a false green.** That
cost this project fourteen "green" iterations with fifteen real issues
outstanding. Check the version; do not assume it.

---

## Phase 1: Setup

- [x] **T001** Create `docs/mobile-open-questions.md` and correct the breakpoint section of `docs/design-system.md`

  **Part A — the open questions file.** Create `docs/mobile-open-questions.md`
  listing exactly three questions, each marked **UNANSWERED**, each with its
  fallback:

  | Question | Fallback if the answer is bad |
  |---|---|
  | Does the wrapped pane read acceptably against Claude Code's real TUI chrome? | Delete `white-space: pre-wrap` and `overflow-wrap: anywhere` from the 780px block. One-line revert. |
  | Once settings rows are stacked, does a bare provenance word ("default", "file") read as part of the value? | Render an explicit label in the row — a template change, specced, not built. |
  | Does the scrolling section menu disorient when the current chip starts offscreen? | Replace the scrolling row with a `<details>` disclosure — a template change, specced, not built. |

  The file must state, in its own words, that **a question is answered by the
  operator's report replacing it, never by a task deciding everything looks
  fine.** T017 re-reads this file and confirms all three are still unanswered.

  **Part B — fix the design system's breakpoint section.**
  `docs/design-system.md` (~line 205) currently says *"Breakpoint at `780px`:
  summary drops to two columns, the brand tagline hides. Two breakpoints is enough
  for one operator on a laptop and a phone."* Two problems: the sentence says
  *two* while the stylesheet has *one* and the test enforces *one*; and it
  enumerates two effects when the block does four things today and will do more
  after this milestone.

  Rewrite it to state: **one** width breakpoint, at 780px, and enumerate
  everything inside it. Add the policy this milestone establishes:

  > A pointer-conditioned block (`@media (pointer: coarse)`) changes
  > **ergonomics — size, spacing, input scale — and never layout.** Layout varies
  > on exactly one axis, and that axis is tested.

  Also amend the pane's typography row from `white-space: pre` to
  "`pre`; `pre-wrap` under the breakpoint".

  **Guards**: none — no CSS changes. The suite is inert to documentation.
  **Verification**: the suite stays green; the required sentences exist
  (`grep`). Part B is a prerequisite for T002, because the `designTokens` map is
  a transcription of this document.

---

## Phase 2: Foundational

**Blocking. Everything after this spends these tokens.**

- [ ] **T002** ⚠️ **RISKY — three files or the premise breaks.** Add `--tap` and `--fs-input` in `docs/design-system.md`, `web/static/crswd.css`, and `internal/httpapi/stylesheet_test.go`

  Add to the token block — **the first rule of `crswd.css`**, which ends around
  line 133:

  ```css
  --tap: 44px;
  --fs-input: 16px;
  ```

  **THE THREE-FILE OBLIGATION, IN ONE COMMIT:**

  1. `docs/design-system.md` — declare both, with the reason each exists
     (`--tap` is the published platform touch minimum; `--fs-input` is the size at
     or above which mobile browsers do not zoom on focus)
  2. `web/static/crswd.css` — the token block, positionally first
  3. `internal/httpapi/stylesheet_test.go` — the `designTokens` map at **line 31**

  **Why all three.** The map is a **hand transcription of the document**, not a
  read of the stylesheet — the comment at line 21 says why: *"a test that compared
  the file against its own spelling would still pass on a palette that had quietly
  drifted."* Adding to the stylesheet and the map but **not** the document passes
  every test and breaks the map's stated premise. That is the failure mode.

  **Guards**: G4 `TestTheTokenBlockIsTheDesignSystem` (line 133) fails if a token
  in the map is missing from the stylesheet or spelled differently. G3
  `TestEveryTokenReferencedExists` (line 2076) is why this task comes before
  every task that spends a token.

  **Verification**: `TestTheTokenBlockIsTheDesignSystem` green with the map
  extended by two entries.
  **Must fail when** a token is added to two of the three files.

---

## Phase 3: US1 — Read what Claude said, from a phone (P1)

**Goal**: prose readable by vertical scrolling alone at 390px; no scroll-chaining
off the page mid-read.

**Independent test**: open a session with prose output at 390px — every line
reachable without horizontal panning; panning at a scroll edge does not navigate.

- [ ] **T003** [P] [US1] Contain the pane's horizontal overscroll in `web/static/crswd.css`

  In the existing `.pane` rule at **line 891**, add one declaration:

  ```css
  overscroll-behavior-x: contain;
  ```

  **Unconditional** — it belongs in the base rule, not the media block. Scroll
  chaining is not phone-only (a trackpad does it), and even after T004 an unbroken
  run longer than the viewport still scrolls horizontally.

  **Do NOT add `overscroll-behavior-y` or bare `overscroll-behavior`.** The pane is
  `max-block-size: var(--pane-h)` = 30rem = 480px on a ~660px viewport — most of
  the screen. Containing the vertical axis means a flick starting inside the pane
  stops at the pane's end instead of scrolling the page, trapping the reader in a
  box that fills their display.

  **Guards**: G2 — `contain` is a keyword, not a length. G7 — `.pane` is far above
  `[hidden]`.

  **Test** (`internal/httpapi/stylesheet_test.go`):
  `TestThePaneDoesNotChainItsOverscroll` — asserts `blockFor(t, source, ".pane")`
  carries `overscroll-behavior-x: contain`.
  **Must fail when** a pan at the scroll edge chains into the browser's navigation
  gesture and throws the reader off the page mid-session.

  Add a second assertion, `TestThePaneDoesNotTrapVerticalScrolling`, that the
  `.pane` block does **not** carry `overscroll-behavior-y` or a bare
  `overscroll-behavior`.
  **Must fail when** the vertical axis is contained too, trapping the reader in a
  box that fills most of their screen.

- [ ] **T004** [US1] Wrap the pane below the breakpoint in `web/static/crswd.css`

  **Inside** the existing `@media (max-width: 780px)` block at **line 1041**,
  before its closing brace:

  ```css
  .pane {
    white-space: pre-wrap;
    overflow-wrap: anywhere;
  }
  ```

  **Leave `white-space: pre` in the base `.pane` rule untouched.** Desktop keeps
  80 columns and alignment; that is what a terminal is for. This overrides, it
  does not replace.

  `anywhere` rather than `break-word`: it breaks within an unbroken run — a long
  path, a URL, a base64 blob — which is what a real terminal does at its column
  edge. `break-word` leaves such a run overflowing, reintroducing the pan for the
  worst case.

  **This is a trade, not a fix, and the commit message should say so.** Claude
  Code draws box borders, dividers and an input box at full terminal width; every
  one wraps into a line plus a stub. Alignment-dependent output (tables, diffs) is
  misrepresented on a phone. What it buys is that reading prose stops requiring a
  horizontal pan per line — the dominant phone task, which today fails outright.

  Safe because captures are right-trimmed: `argvCapturePane` in
  `internal/tmuxctl/exec.go` is `capture-pane -p -t <target>` with **no `-N`**, so
  blank padding does not wrap.

  **Guards**: G1 — **inside** the existing block; a second `@media (max-width:
  780px)` fails even at the same width. G2 — both values are keywords.

  **Tests**: `TestThePaneWrapsOnlyOnNarrowViewports` — the 780px block's `.pane`
  rule carries **both** `white-space: pre-wrap` and `overflow-wrap: anywhere`.
  **Must fail when** wrapping is added to the base rule, so a desktop loses column
  alignment to fix a phone.

  `TestThePaneKeepsItsDesktopAlignment` — the **base** `.pane` rule still carries
  `white-space: pre`.
  **Must fail when** the base declaration is changed rather than overridden, and
  every desktop reader loses alignment silently.

- [ ] **T005** [P] [US1] Guard that no page clamps the zoom, in `internal/httpapi/stylesheet_test.go`

  Add `TestNoPageClampsTheZoom`, walking `web.Templates` (all four pages, so a
  fifth is covered when it arrives) and asserting no viewport meta tag contains
  `maximum-scale` or `user-scalable=no`.

  **Why this is a task rather than a note.** Pinch-zoom is the operator's escape
  hatch for exactly the alignment-dependent output T004 damages. Someone
  "fixing" a layout later by clamping scale would remove the mitigation at the
  same moment the problem exists.

  **Must fail when** someone disables zoom to fix a layout, removing the only
  mitigation for the trade T004 makes.

---

## Phase 4: US2 — Read and change a setting, from a phone (P1)

**Goal**: the page the operator reported. Content on the first screen, the pan
scoped to the content, the input usable.

**Independent test**: at 390px, open settings, switch sections, edit one value —
no scrolling past a screen of menu, no menu movement when content pans, input and
Save both visible.

**Order within this phase is not arbitrary: T006 must land before T008**, or the
intermediate state pans a taller menu beside a taller table.

- [ ] **T006** [US2] Move the settings overflow off the wrapper in `web/static/crswd.css`

  `overflow-x: auto` currently sits on `.settings` — **the grid wrapper holding
  both the menu and the panel** — so content wider than the viewport pans the
  section menu along with the table.

  **Delete the whole rule at line 1131:**

  ```css
  .settings {
    overflow-x: auto;
  }
  ```

  **Add one line to `.settings-panel` (~line 1358):**

  ```css
  .settings-panel {
    min-inline-size: 0;
    padding-inline-start: var(--s2);
    overflow-x: auto;
  }
  ```

  **Careful**: `.settings` has a *second* rule inside the 780px block
  (`grid-template-columns: 1fr`). Delete only the `overflow-x` rule at 1131; leave
  the media-block rule alone.

  Unconditional — as right on a desktop as on a phone. It went unnoticed only
  because a desktop is wide enough that nothing overflows.

  **Test**: `TestWideSettingsPanTheirOwnPanel` — `blockFor(.settings-panel)`
  carries `overflow-x`, **and** the `.settings` wrapper rule does not.
  **Must fail when** the property is added to the panel without being removed from
  the wrapper, so the menu still pans with the table and nothing observable
  changed.

- [ ] **T007** [US2] Reflow the section menu into a scrolling row in `web/static/crswd.css`

  At ≤780px the grid collapses to one column, so the seven-entry menu stacks
  **above** the panel and consumes ~300px — the whole first screen. Each section
  is a fresh GET landing at the top, so choosing one means scrolling past the
  entire menu again.

  **Inside** the `@media (max-width: 780px)` block:

  ```css
  .settings-menu {
    position: static;
  }

  .settings-menu-list {
    grid-auto-flow: column;
    justify-content: start;
    overflow-x: auto;
  }

  .settings-menu-link {
    white-space: nowrap;
  }

  .settings-menu-link[aria-current="page"] {
    border-inline-start: none;
    border-block-end: var(--edge-width) solid var(--edge-bright);
  }
  ```

  `position: static` because in a one-column grid each item's area is its own
  height, so `sticky` has no travel room and does nothing. Stating it is better
  than relying on the geometry to keep being true.

  The marker moves to the bottom edge because a start-edge bar reads wrongly on a
  chip in a row. **It stays a border as well as a colour — never hue alone.**

  **The menu stays real links.** No `<select>`, no `<details>`, no JavaScript.

  **Guards**: G1 — inside the existing block. G6 — `.settings-*` is not in the
  `.combo|switch|masthead` family, so `docs/components.md` is not implicated.

  **Tests**: `TestTheSettingsMenuIsARowOnNarrowViewports` — the 780px block sets
  `grid-auto-flow: column` on `.settings-menu-list` and `position: static` on
  `.settings-menu`.
  **Must fail when** the menu keeps stacking, so the panel still starts below a
  full screen of links.

  `TestTheCurrentSectionIsNotColourAlone` — the `aria-current` rule carries a
  `border-*` declaration at **both** widths.
  **Must fail when** the narrow override drops the border, leaving the current
  section marked by colour alone.

  `TestTheSettingsMenuIsStillLinks` — `settings.html` renders `<a href` per
  section; no `<select>`, no `<form>` around the menu.
  **Must fail when** the menu is rebuilt as a control that needs JavaScript.

- [ ] **T008** ⚠️ **RISKY — weakens table semantics; the 1px recipe fails the sweep.** [US2] Stack setting rows below the breakpoint in `web/static/crswd.css`

  A three-column table (key / value / provenance) cannot fit ~358px. On an
  editable row, `.setting-form` carries a 128px-min input plus a Save button,
  pushing the row past 430px — **so editing means typing inside a horizontally
  panning table**.

  **Inside** the `@media (max-width: 780px)` block:

  ```css
  .settings-table thead {
    position: absolute;
    clip-path: inset(50%);
    overflow: hidden;
  }

  .settings-table tr {
    display: grid;
    grid-template-columns: minmax(0, 1fr);
  }

  .settings-table th,
  .settings-table td {
    overflow-wrap: anywhere;
  }
  ```

  **DO NOT use the conventional visually-hidden recipe.** It spends
  `inline-size: 1px` and `block-size: 1px`, and **G2 fails a literal length**.
  `inset(50%)` is a percentage, and `%` is absent from the sweep's unit list —
  verified against the regex, not assumed.

  **DO NOT use `display: none` on `thead`.** That removes the headers from
  assistive technology entirely, which is the failure this recipe exists to avoid.

  **The cost, which the commit message should state**: `display: grid` on a `<tr>`
  removes row semantics in most accessibility mappings. Mitigated by hiding the
  headers accessibly rather than deleting them, and by a linear order — key,
  value, provenance — that reads correctly on its own. The residual risk is
  question 2 in `docs/mobile-open-questions.md`; **do not mark it answered.**

  **Guards**: G1 — inside the existing block. G2 — `inset(50%)`, never `1px`.
  G7 — `display: grid` involved, so these rules must sit before the terminal
  `[hidden]` rule.

  **Tests**: `TestSettingRowsStackOnNarrowViewports` — the 780px block sets
  `display: grid` on the table's `tr` and hides `thead` with `clip-path`.
  **Must fail when** rows stay three-column, so editing a setting means typing
  inside a horizontally panning table.

  `TestTheHeadersAreHiddenAccessiblyNotRemoved` — the `thead` rule uses
  `clip-path` and **not** `display: none`.
  **Must fail when** the headers are removed from the accessibility tree entirely.

- [ ] **T009** [P] [US2] Correct the header comment in `web/templates/settings.html`

  The comment states the page has *"no form here, and there is not going to be
  one… no page token, no action row and no live region"*. The page below it
  renders `.setting-form` rows, the update form, a page token, and an
  `<output id="action-toast">` live region. **All four.**

  Rewrite the paragraph to describe the page that exists.

  **Why this is a defect and not tidying**: in a pipeline whose executor reads
  comments as contract, a false comment costs an iteration. This one asserts the
  opposite of the truth about a security-adjacent property — whether the page
  carries a token — which is the worst kind to leave lying.

  **Guards**: none. Every sweep strips comments, so the suite is inert to this.
  **Verification**: the new text exists and the suite stays green.
  **Must fail when** the comment keeps asserting the opposite of the file, and the
  next fresh-context iteration believes it.

---

## Phase 5: US3 + US4 — Touch (P2)

**Goal**: every control hittable with a thumb; no form interaction zooms the page.
Desktop unchanged.

**Independent test**: on a real touch device every control is reliably hittable
and no input zooms on focus; on a desktop with a mouse nothing has grown.

> **A desktop browser's responsive emulator has a mouse**, so
> `@media (pointer: coarse)` does not match in it. These two tasks are invisible
> there. They must be judged on a real device.

- [ ] **T010** ⚠️ **RISKY — silent no-op if misplaced.** [US3] Add the `@media (pointer: coarse)` block with touch target sizes in `web/static/crswd.css`

  **Placement is the whole risk of this task.** The block goes **immediately after
  the `@media (prefers-reduced-motion: reduce)` block and before the `[hidden]`
  rule** at the end of the file.

  These are single-class overrides of single-class rules — **identical
  specificity** — so order alone decides. Placed *before* `.button` (line 533) or
  `.field-input` (line 622), the block parses, passes every guard, and **does
  nothing**. That is the exact silent failure `--bright` and `--glow` already
  demonstrate in this file: a rule that looks correct, passes, and has no effect.

  ```css
  @media (pointer: coarse) {
    .button {
      min-block-size: var(--tap);
    }

    .settings-menu-link,
    .combo-list li,
    .rename-summary,
    .release-notes summary {
      padding-block: var(--s3);
    }

    .masthead-link {
      padding-block: var(--s3);
      margin-block: calc(var(--s3) * -1);
    }

    .field-switch {
      min-block-size: var(--tap);
    }

    .card-actions {
      gap: var(--s3);
    }
  }
  ```

  All six selectors are confirmed present in both the stylesheet and the
  templates. The negative margin on `.masthead-link` takes the extra hit area back
  from the layout so the bar's height is unchanged.

  `.card-actions` gets a wider gap because **Destroy sits ~8px from Compact**, both
  ~24px tall. Labels already distinguish them; this makes the geometry agree, so a
  mis-tap costs a thumb's width rather than a shell.

  **Guards**: G1 — `(pointer: coarse)` is not a width feature; the guard's regex
  is `\((?:max|min)-width\s*:\s*([^)]+)\)` and matches only width. Verify by
  running the test, not by reasoning. G2 — spend `var(--tap)` and `var(--s3)`;
  `44px` inline fails. G3 — `--tap` exists as of T002. G6 — `.masthead-link`,
  `.combo-list` and `.switch-*` are **already named** in `docs/components.md`, so
  adding rules for them is free; **do not introduce a new name in those
  families**. G7 — before the terminal `[hidden]` rule.

  **The policy this block must respect, which no guard enforces**: a
  pointer-conditioned block changes **ergonomics — size, spacing, input scale —
  and never layout**. A `display` or `grid-template-*` here breaks the policy even
  though every test passes.

  **Tests**:

  `TestTouchTargetsFollowThePointerNotTheWidth` — a `@media (pointer: coarse)`
  block exists and spends `var(--tap)`.
  **Must fail when** targets are sized inside the 780px block instead, so a tablet
  at 1024px — a touch device — never gets them.

  `TestTheCoarseBlockOverridesRatherThanPrecedes` — **the assertion that makes
  this task safe.** The coarse block's byte offset in the file is **after** the
  base `.button` and `.field-input` rules.
  **Must fail when** the block is placed early, parses, passes every other guard,
  and has no effect. This is the only assertion in this milestone that catches a
  change which is syntactically perfect and behaviourally absent.

  `TestTheCoarseBlockChangesNoLayout` — no rule inside the coarse block sets
  `display`, `grid-template-*`, `flex-direction` or `position`.
  **Must fail when** the pointer block starts carrying layout, so layout varies on
  an axis nothing tests and the breakpoint guard's guarantee quietly becomes
  false.

  `TestEveryButtonIsThumbSized` — the coarse block's `.button` rule carries
  `min-block-size: var(--tap)`.
  **Must fail when** only some controls are enlarged, so Destroy stays a 24px
  target beside Compact.

  `TestDestroyIsSeparatedFromCompact` — the coarse block sets `gap` on
  `.card-actions`.
  **Must fail when** the two stay 8px apart and a mis-tap ends a shell the
  operator wanted.

- [ ] **T011** [US4] Raise input size under a coarse pointer in `web/static/crswd.css`

  In the **same** `@media (pointer: coarse)` block T010 created:

  ```css
  .field-input,
  .setting-input {
    font-size: var(--fs-input);
  }
  ```

  Every text input is 14px, and mobile browsers zoom the page on focus of anything
  under 16px. Renaming a session, creating one, or editing a setting zooms and
  pans the layout **every time**, and the operator pinches back out afterwards.

  Base `.field-input` is at line 622 and `.setting-input` at line 1379 — both far
  above where the coarse block sits, so the override wins on order.

  **Both selectors, not one.** They cover different forms: `.field-input` is
  create and rename, `.setting-input` is settings editing.

  **Guards**: G2 — `16px` inline fails; spend `var(--fs-input)`. G3 —
  `--fs-input` exists as of T002.

  **Test**: `TestInputsDoNotTriggerFocusZoom` — the coarse block sets
  `font-size: var(--fs-input)` on `.field-input` **and** `.setting-input`.
  **Must fail when** one of the two is missed, so half the forms still zoom and
  the fix reads as done.

---

## Phase 6: US5 + US6 — Content reachability (P3)

- [ ] **T012** [P] [US5] Wrap card text below the breakpoint in `web/static/crswd.css`

  `.card-name` and `.card-path` (line 440) ellipsize, with the full text in a
  `title` attribute. **A `title` needs a hover, and a touch device does not have
  one** — so on a phone the full session name and full working directory are
  unreachable. That is data loss, not styling.

  **Inside** the `@media (max-width: 780px)` block:

  ```css
  .card-name,
  .card-path {
    white-space: normal;
    overflow-wrap: anywhere;
  }
  ```

  The single-column grid at this width makes variable card heights harmless.

  **Tests**: `TestCardTextWrapsOnNarrowViewports` — the 780px block sets
  `white-space: normal` on both.
  **Must fail when** the ellipsis stays, so the full name and working directory
  remain reachable only by a hover a phone does not have.

  `TestTheCardKeepsItsDesktopTruncation` — the **base** rule still ellipsizes.
  **Must fail when** the base rule is edited instead of overridden, so every
  desktop card grows to fit its longest path.

- [ ] **T013** [P] [US6] Align the masthead and stop it wrapping in `web/static/crswd.css`

  Two faults. `.masthead-bar` (line 165) keeps `padding-inline: var(--s5)` (24px)
  while `.shell` drops to `var(--s4)` (16px) at the breakpoint, so the header sits
  8px out of line with everything beneath it. And `.operator` (line 243) is a flex
  item whose **hypothetical** (full email) width drives wrapping — shrinking
  applies only *after* the wrap — so a longer identity wraps the sticky bar to two
  rows.

  **Inside** the `@media (max-width: 780px)` block:

  ```css
  .masthead-bar {
    padding-inline: var(--s4);
  }

  .operator {
    flex: 1 1 0;
    min-inline-size: 0;
    text-align: end;
  }
  ```

  `flex-basis: 0` rather than `auto` lets the ellipsis that is already there do
  the work, instead of only helping after the bar has already wrapped.

  **Guards**: G2 — the bare `0` in `flex: 1 1 0` carries no unit, so the length
  pattern does not match it. G6 — `.masthead-bar` is already named in
  `docs/components.md`; adding a rule is free.

  **Tests**: `TestTheMastheadAlignsWithThePage` — the 780px block sets
  `padding-inline: var(--s4)` on `.masthead-bar`.
  **Must fail when** the header keeps desktop gutters and sits out of line with the
  content beneath it.

  `TestALongIdentityDoesNotWrapTheBar` — the 780px block sets `flex: 1 1 0` on
  `.operator`.
  **Must fail when** basis stays `auto`, so the bar wraps to two rows on a long
  email and the ellipsis only helps afterwards.

---

## Phase 7: US7 — What the sweep found (P3)

- [ ] **T014** [US7] Fix `background: var(--glow)` and guard the class of bug, in `web/static/crswd.css` and `internal/httpapi/stylesheet_test.go`

  **This is a live bug on every device, desktop included.**

  `--glow` is declared at line 133 as a **shadow list**:
  `--glow: 0 0 var(--s2) var(--phosphor)`. It is spent as a **background** in two
  places — line 1339 (`.settings-menu`) and line 1352
  (`.settings-menu-link[aria-current="page"]`).

  `background: 0 0 .5rem #00ff41` is invalid at computed-value time. The
  declaration is dropped. **The settings menu has no surface and the current
  section has no tint, and never has.**

  ```css
  .settings-menu {
    background: var(--surface);
  }

  .settings-menu-link[aria-current="page"] {
    background: var(--surface-lift);
  }
  ```

  **Read the comment at line 1330 before changing these lines.** It says
  `var(--bright)` resolved to nothing, the current section was never tinted, *"the
  border and aria-current carried the whole marker and nobody noticed, which is
  exactly what 'not by hue alone' is for: the redundant cue was the working one."*

  **The comment describing this exact class of defect sits directly above another
  instance of it.** Update it to record both, and to name the gap:
  `TestEveryTokenReferencedExists` was added in response to `--bright` and cannot
  catch `--glow`, because it checks a referenced token **exists** — not that it is
  of a *kind the property accepts*.

  This is the second time "never by hue alone" has silently saved this page. Worth
  writing down rather than fixing quietly.

  **Tests**: `TestNoBackgroundSpendsAShadowToken` — no `background` or
  `background-color` declaration anywhere references `var(--glow)`. Narrow and
  honest: it closes the instance without pretending to a general type system for
  CSS.
  **Must fail when** a shadow list is used as a background — valid-looking,
  silently dropped, renders nothing.

  `TestTheSettingsMenuHasASurface` — `blockFor(.settings-menu)` carries a
  `background` naming a surface token.
  **Must fail when** the menu is left with no background at all, which is the
  current state and looks deliberate.

- [ ] **T015** ⚠️ **RISKY — an unstyled page is invisible to the class sweep.** [US7] Retire the superseded settings rules in `web/static/crswd.css`

  At lines 1135–1176, styling from the settings page's **pre-#103 flat-table
  design** still applies to today's markup by descendant selector:

  | Rule | Status |
  |---|---|
  | `.settings caption` | **confirmed dead — `settings.html` renders zero `<caption>` elements** |
  | `.settings table` | still matches — the sectioned page renders tables |
  | `.settings th`, `.settings td` | still matches |
  | `.settings p` | still matches `.settings-source` |

  **Delete `.settings caption` outright.** It is verified dead.

  **For the other three: check each against `web/templates/settings.html` before
  touching it.** Keep what is load-bearing, delete what is not, and **say in the
  commit message which was which**.

  **Do not delete on suspicion.** A removed rule that was doing work is an
  unstyled page, and the class sweep **cannot catch it** — these are *element*
  selectors under a class, and `styledClasses` only collects class names. That
  blindness is the whole reason these rules survived #103.

  **Guards**: G5 — deleting a rule for a class that still renders fails the sweep
  in the other direction. Check before removing.

  **Test**: `TestNoRuleTargetsACaption` — the stylesheet has no `.settings caption`
  rule.
  **Must fail when** a rule survives for an element the page has not rendered since
  #103, invisible to the class sweep because it selects an element.

---

## Phase 8: Polish

- [ ] **T016** [P] Update the component prose in `docs/components.md`

  Two components changed behaviour and the document still describes the old one:

  - **The pane** — "the container scrolls, the page does not" is still true, and
    it now wraps under the breakpoint. Record the trade: TUI chrome wraps into a
    line plus a stub, and alignment-dependent output is misrepresented on a phone.
    Name pinch-zoom as the escape hatch and note that no page may clamp scale.
  - **The settings menu** — a bar beside the panel on a desktop, one scrolling row
    of the same real links on a phone. Same markup, same `aria-current`, no
    JavaScript either way. Record the cost: the row scrolls, and with no
    JavaScript nothing can scroll the current chip into view.

  **Guards**: G6 — `docs/components.md` and the stylesheet must name the same
  `.combo*`, `.switch*` and `.masthead*` classes. **This task adds no class**, so
  the balance is unchanged; do not introduce a name here that the stylesheet does
  not carry, or the test fails in the other direction.

  **Verification**: `TestTheComponentsDocumentNamesThePickerTheSwitchAndTheHeader`
  stays green.

- [ ] **T017** Verify the three open questions survived, in `docs/mobile-open-questions.md`

  **THIS TASK VERIFIES THAT THE QUESTIONS ARE STILL OPEN. IT DOES NOT ANSWER
  THEM.**

  Re-read `docs/mobile-open-questions.md` and confirm:

  1. All three questions are still listed and still marked **UNANSWERED**
  2. Each still carries its named fallback
  3. No task in this milestone has marked any of them resolved

  **If any has been ticked, that is a defect and this task must un-tick it and
  record what happened in `ralph/PROGRESS.md`.** A question is answered by the
  operator's report replacing it — never by a task deciding everything looks fine.

  **Why this task exists.** Every task in this milestone lands with a green
  assertion, and an assertion proves a declaration exists — **not that a page is
  usable on a phone**. Nothing in this repository renders CSS and nothing in it has
  a thumb. Milestone 4 shipped three green tasks while the control they were about
  went unchanged; the same mechanism is available to every task here.

  A milestone that closes with all three still open is **correct and expected**.

  Then run the full gate one final time and confirm `docs/design-system.md`,
  `docs/components.md` and the stylesheet agree.

---

## Dependencies

```
T001 (docs + open questions)
  └─→ T002 (tokens — needs the doc to transcribe from)
        ├─→ T003, T004, T005      US1  pane
        ├─→ T006 → T007 → T008    US2  settings  (T006 BEFORE T008)
        │     └─→ T009            US2  comment   [P]
        ├─→ T010 → T011           US3/US4 touch  (T011 needs T010's block)
        ├─→ T012, T013            US5/US6        [P]
        └─→ T014, T015            US7            [P]
                                    └─→ T016, T017  polish
```

**Only two hard orderings inside a phase**: T006 before T008 (or the intermediate
state pans a taller menu beside a taller table), and T010 before T011 (T011 adds
rules to the block T010 creates).

**Parallel opportunities**: T003/T005, T012/T013, T014/T015, and T009 against any
of Phase 4 — all touch different rules or different files.

## MVP scope

**T001 + T002 + Phase 4 (T006–T009)** — the settings page, which is the surface
the operator reported and the audit ranked worst. It is independently useful: it
ships a settings page usable on a phone even if nothing else lands.

**The next most valuable increment is Phase 3** (the pane), because it is the
product's core screen — and it is second only because settings was reported and
the pane was inferred.
