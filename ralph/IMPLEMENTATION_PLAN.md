# Implementation Plan

**Milestone 7 — Make it work on a phone.**

Spec: `specs/007-make-it-work-on-a-phone/`
Contracts: `specs/007-make-it-work-on-a-phone/contracts/`

---

## Status: generated from the spec

17 tasks, from a Fable 5 audit of every page, partial and rule at a 390px
viewport. The operator's own report, verbatim: *"Right now mobile is rough"* and
*"Seeing settings is tricky right now as well"*.

---

## ⚠️ THE RISK HERE IS THE GUARDS, NOT THE DESIGN

This milestone is a stylesheet, two comments, and documentation. No new route, no
new state, **no new class**.

So the danger is not getting the design wrong. It is that a **correct-looking CSS
change fails a test you did not know existed**, and most of these failures are
invisible from the rule itself:

- A rule in the wrong **position** parses fine and does nothing.
- A value spelled **directly instead of as a token** fails a sweep in another file.
- A second media query at an **identical width** fails a count.

**Every task carries its guards inline.** You should not need to open
`contracts/guards.md` — but it holds the full inventory of all nine if you want it.

### The four rules that apply to every CSS task

1. **All width-conditional rules go INSIDE the existing `@media (max-width:
   780px)` block at `web/static/crswd.css:1041`.** A second block fails
   `TestTheDashboardHasExactlyOneBreakpoint` **even at an identical width** — the
   guard counts occurrences, it does not compare them.
2. **Every value is `var(--token)`.** `TestNoRuleCarriesAValueThatBelongsInAToken`
   fails a literal length or colour **inside media blocks too**. Its pattern is
   `\d+(\.\d+)?(px|rem|em|pt|ch|ex|vh|vw)\b` — `%` is **not** in it, so
   `inset(50%)` is legal and `1px` is not.
3. **New rules go before the terminal `[hidden]` rule**, or `TestHiddenAlwaysWins`
   fails.
4. **Never use range syntax** (`(width <= 780px)`). It slips past the breakpoint
   guard's regex while doing exactly what the guard forbids — routing around a
   hook, which `AGENTS.md` prohibits by name.

### Every assertion reads the PARSED stylesheet

Use `blockFor(t, source, marker)` (`stylesheet_test.go:1729`) or `cssRules`
(1796) — the **block the declaration lives in**, never a substring of the file.

```go
pane := blockFor(t, stylesheet(t), ".pane")          // CORRECT
if !strings.Contains(stylesheet(t), "pre-wrap") { }  // WRONG
```

**Milestone 4 shipped three green tasks while the control they were about went
unchanged.** The wrong form is how. `stylesheet()` strips comments, so no guard
can ever be satisfied by writing the declaration in prose beside the rule.

---

## 🚫 THREE QUESTIONS SHIP OPEN. THE LOOP MUST NOT ANSWER THEM.

`docs/mobile-open-questions.md` is created by **T001** and verified by **T017**.

| Question | Fallback |
|---|---|
| Does the wrapped pane read against Claude Code's real TUI chrome? | Delete two declarations from the 780px block. One-line revert. |
| Does a bare provenance word read as part of the value once rows stack? | Render an explicit label in the row (template change, specced, not built). |
| Does the scrolling menu disorient when the current chip starts offscreen? | Replace it with a `<details>` disclosure (template change, specced, not built). |

**Nothing in this repository renders CSS and nothing in it has a thumb.** Every
task here lands green, and green proves a declaration exists — not that a page is
usable on a phone.

**A question is answered by the operator's report replacing it, never by a task
deciding everything looks fine.** T017 verifies they survived; it does not resolve
them. A milestone closing with all three still open is **correct and expected**.

---

## 🔒 Four tasks are risky, in this order

1. **T010 — the `@media (pointer: coarse)` block.** Placed before the rules it
   overrides, it parses, passes every guard, and **does nothing**. Identical
   specificity means order alone decides.
   `TestTheCoarseBlockOverridesRatherThanPrecedes` is the only assertion in this
   milestone that catches a change which is syntactically perfect and
   behaviourally absent.
2. **T015 — deleting the superseded settings rules.** A removed rule that was
   doing work is an unstyled page, and the class sweep **cannot see it**: these
   are *element* selectors under a class, and `styledClasses` collects only class
   names. That blindness is why they survived #103. Check each against the
   template; do not delete on suspicion.
3. **T002 — the two tokens.** Three files or the premise breaks. The
   `designTokens` map is a transcription of `docs/design-system.md`, **not** a
   read of the stylesheet. Landing two of three passes every test and breaks what
   the map is for.
4. **T008 — stacked table rows.** Weakens table semantics. Use
   `clip-path: inset(50%)`, **never** the conventional `1px` visually-hidden
   recipe, which the value sweep fails.

---

## What is already running

`crswd v0.71` on this host, from a config file at `~/.config/crswd/config`,
updated through the browser. The deployment is not part of this milestone; the
milestone ends with a release the operator can update into.

---

## Resolved decisions

From `specs/007-make-it-work-on-a-phone/research.md` — settled, so no iteration
needs to re-argue them.

- **The pane wraps below the breakpoint, and it is a TRADE.** Claude Code's own
  box borders and dividers wrap into a line plus a stub; alignment-dependent
  output is misrepresented on a phone. What it buys is that reading prose stops
  requiring a horizontal pan per line — the dominant phone task, which today fails
  outright. Rejected with arithmetic: shrink-to-fit needs a ~6.9px font.
  **Reverting is one declaration.**
- **Safe because captures are right-trimmed.** `capture-pane -p -t <target>`
  carries no `-N`, so tmux strips trailing spaces and blank padding does not wrap.
  Verified in `internal/tmuxctl/exec.go`, not assumed.
- **`overscroll-behavior-x` is unconditional; the vertical axis is deliberately
  left alone.** Containing vertical scroll on an element that is most of the
  screen traps the reader in a box.
- **One width breakpoint stays one.** Everything triggers at the same threshold,
  and the layouts that do not need it are already intrinsic.
- **Touch ergonomics follow the POINTER, not the viewport.** A tablet in landscape
  is a touch device at a desktop width; a narrow desktop window is a mouse.
  `(pointer: coarse)` is not a width feature, so it passes the breakpoint guard
  honestly. **Policy: a pointer block changes ergonomics, never layout.**
- **The settings menu stays real links**, reflowed into a scrolling row. Rejected:
  a `<select>` in a GET form, a `<details>` disclosure, an accordion — each
  argued and priced in research.md R8.
- **No template change is needed for any layout fix.** Confirmed selector by
  selector. Because no new class is introduced, **two of the nine guards cannot
  fire in this milestone at all**.
- **`background: var(--glow)` is a live bug on every device.** `--glow` is a
  shadow list; used as a background it is dropped at computed-value time. The
  settings menu has never had a surface. The comment above it already describes
  this exact class of defect for `var(--bright)` — and
  `TestEveryTokenReferencedExists` cannot catch it, because it checks a token
  *exists*, not that it is of a *kind the property accepts*.

---

## Conventions

- `- [ ]` open · `- [x]` done · `- [!]` blocked (reason in `PROGRESS.md`)
- Priority order is meaningful — the loop always takes the topmost open item.
- Each task must be independently completable **and verifiable** in one iteration.
- **Every task ends green**: `go build ./... && go vet ./... && go test ./... && golangci-lint run`.
  Add `-tags tmux` when the task touches tmux and `-tags quickstart` when it touches
  `cmd/crswd`. Tests ship inside the task that implements the behaviour — never as a separate
  failing test, which step 6 of `PROMPT.md` would make the iteration revert.
- **Check the linter is v2 before trusting it.** A pre-v2 binary reads this repo's v2 config,
  runs zero linters, and exits 0 — a green that means nothing. The session-start hook warns
  when this is the case; believe it (#26).
- `go.sum` must never appear. An import needs justification under `docs/security.md` §5 first.
- **AR-005: a test satisfies the cross-site checks, it never disables them.**
- **AR-008: no refactoring outside the task**, however obvious the improvement.
- **A task is not done when the code exists. It is done when something calls it.** This repo
  has shipped that failure four times — a reaper with no caller, `Store.Touch` with no caller,
  a PR-opener no workflow invoked, and `CRSW_DESTROY_ON_SHUTDOWN`, which was false on every
  daemon that ever ran. **In this milestone the equivalent is T010**: a CSS block placed
  before the rules it overrides is a rule with no effect, which is the same failure wearing
  a stylesheet.

---

## Tasks

### Setup — the docs everything else transcribes from

- [x] **T001** Create `docs/mobile-open-questions.md` (three questions, three fallbacks, all UNANSWERED) and correct the breakpoint section of `docs/design-system.md` — it says "Two breakpoints is enough" while the stylesheet has one and the test enforces one. Add the pointer-coarse policy and amend the pane's typography row.

### Foundational — blocking

- [x] **T002** 🔒 Add `--tap: 44px` and `--fs-input: 16px` to **all three** of `docs/design-system.md`, the token block in `web/static/crswd.css`, and the `designTokens` map at `internal/httpapi/stylesheet_test.go:31`, in ONE commit.

### US1 — Read what Claude said, from a phone (P1)

- [x] **T003** Add `overscroll-behavior-x: contain` to `.pane` (`crswd.css:891`), unconditionally. Do NOT add the vertical axis.
- [x] **T004** Add `white-space: pre-wrap` and `overflow-wrap: anywhere` to `.pane` INSIDE the 780px block. Leave `white-space: pre` in the base rule.
- [x] **T005** Add `TestNoPageClampsTheZoom`, walking `web.Templates` for `maximum-scale` / `user-scalable=no`.

### US2 — Read and change a setting, from a phone (P1) — the reported surface

- [x] **T006** Delete `overflow-x: auto` from `.settings` (`crswd.css:1131`, the grid wrapper) and add it to `.settings-panel`. **Must land before T008.**
- [x] **T007** Reflow `.settings-menu-list` to `grid-auto-flow: column` with `position: static` on `.settings-menu`, inside the 780px block. Move the `aria-current` marker to `border-block-end`. Links stay links.
- [x] **T008** 🔒 Stack `.settings-table` rows inside the 780px block. `clip-path: inset(50%)` for the headers — **never** the `1px` recipe.
- [x] **T009** Rewrite `web/templates/settings.html`'s header comment, which claims the page has no form, no token, no action row and no live region. It has all four.

### US3 + US4 — Touch (P2)

- [ ] **T010** 🔒 Add the `@media (pointer: coarse)` block **after** the reduced-motion block and **before** `[hidden]`. Include `TestTheCoarseBlockOverridesRatherThanPrecedes` — the offset assertion.
- [ ] **T011** Add `font-size: var(--fs-input)` for `.field-input` **and** `.setting-input` to the same coarse block.

### US5 + US6 — Content reachability (P3)

- [ ] **T012** Wrap `.card-name` / `.card-path` inside the 780px block. The `title` attribute needs a hover a phone does not have.
- [ ] **T013** `padding-inline: var(--s4)` on `.masthead-bar` and `flex: 1 1 0` on `.operator`, inside the 780px block.

### US7 — What the sweep found (P3)

- [ ] **T014** Replace `background: var(--glow)` with `var(--surface)` and `var(--surface-lift)` at `crswd.css:1339` and `1352`. Add `TestNoBackgroundSpendsAShadowToken`. Update the comment above them to record the gap.
- [ ] **T015** 🔒 Delete `.settings caption` (verified dead — zero captions rendered). Check `.settings table`, `.settings th/td` and `.settings p` against the template; keep what is load-bearing and say which in the commit message.

### Ship it

- [ ] **T016** Update the pane and settings-menu prose in `docs/components.md`.
- [ ] **T017** Re-read `docs/mobile-open-questions.md` and confirm all three questions are **still UNANSWERED** with their fallbacks intact. **This task verifies; it does not answer.** If any has been ticked, un-tick it and record what happened in `PROGRESS.md`.

---

## Shippable at T009

T001, T002 and Phase 4 together ship a settings page usable on a phone — the
surface the operator actually reported, and the one the audit ranked worst. Every
task after that is additive, and none of them is required for that to be true.

The pane (T003–T005) is second only because settings was **reported** and the pane
was **inferred**. On the audit's own ranking they are both P1.

---

## Out of scope

No task may wander into these.

- **Resizing the tmux PTY to the reader's viewport.** The correct answer to
  terminal reflow, and a daemon change with a genuine multiple-concurrent-readers
  problem — a desktop viewer, the companion skill, and the operator attached on
  the host may all be watching at different widths. Named as future work.
- **A wrap/zoom toggle control for the pane.** Permissible later as progressive
  enhancement; it is a new component, new tokens and new tests for what is
  currently a guess about a preference. Not before the wrap has been used in
  anger.
- **A second width breakpoint**, and any change to
  `TestTheDashboardHasExactlyOneBreakpoint`.
- **Native apps, service workers, offline support, install prompts.** This is a
  website that must work on a phone.
- **Any change to routing, session semantics, or the security posture.**
- **Auto-recovery of a crashed session** (#95). Still deliberately unspecified.
- **Answering the three open questions.** They belong to the operator and a real
  device.
