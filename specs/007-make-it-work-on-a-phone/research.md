# Research: Make it work on a phone

Eight questions were open. **The most valuable answer is R1**, and it is not a
design decision at all — it is an inventory of the guards a CSS task can trip.

This milestone has no new behaviour, no new route, no new state. Its entire risk
is that a fresh-context iteration writes a correct rule in the wrong place, or
spells a correct value the wrong way, and burns its iteration discovering a test
it was never told about. Every line below exists to stop that.

All line numbers are as of `06ace3c`.

---

## R1 — The guard inventory

`internal/httpapi/stylesheet_test.go` is 2125 lines and it **is** this project's
design system enforcement. A CSS change can trip nine of its tests. Here is every
one, what trips it, and the single correct way to satisfy it.

### The reading helpers, first — because every guard inherits their behaviour

| Helper | Line | What it does | Why a task must know |
|---|---|---|---|
| `stylesheet(t)` | 83 | Reads the embedded CSS and **strips every `/* … */` comment** | A guard can never be satisfied by a comment. Writing the declaration in prose beside the rule does nothing. |
| `tokenBlockAndRules(t)` | 97 | Splits at the **first** `{` and **first** `}` | The token block is positionally the first rule. A token declared anywhere else is not a token. |
| `declarations(block)` | 117 | Splits on `;` then `:` | Only reads flat `name: value` pairs. |
| `blockFor(t, source, marker)` | 1729 | First block whose prelude contains `marker`, **brace-counted** | This is how a task asserts about one rule. Brace counting means it works on a media block and returns everything inside it. |
| `cssRules(source)` | 1796 | Strips media **preludes**, splits on `}` | A rule inside a media query is read as an ordinary rule. The query's closing brace becomes a chunk with no `{` and is skipped. |
| `mediaOpen` | 77 | `@media[^{]*\{` | Strips the whole prelude, so `(pointer: coarse)` and `(max-width: 780px)` both vanish before the value sweeps run. |

**The single most useful consequence**: because `mediaOpen` strips preludes,
rules inside a media block are swept by the value guards *exactly* like top-level
rules. There is no exemption for being inside a query.

### The nine guards

| # | Test | Line | Trips when | The one correct way to satisfy it |
|---|---|---|---|---|
| G1 | `TestTheDashboardHasExactlyOneBreakpoint` | 235 | `regexp` `\((?:max\|min)-width\s*:\s*([^)]+)\)` matches ≠1 time, or the value ≠ `780px` | **Every** width-conditional rule goes inside the block that already exists at line 1041. A second `@media (max-width: 780px)` fails even at an identical width. |
| G2 | `TestNoRuleCarriesAValueThatBelongsInAToken` | 173 | A literal colour, colour function, colour keyword, **length**, or external origin appears below the token block — media preludes stripped first | Every value is `var(--token)`. Length pattern is `\d+(\.\d+)?(px\|rem\|em\|pt\|ch\|ex\|vh\|vw)\b` — **`%` is absent from it** (see R4). |
| G3 | `TestEveryTokenReferencedExists` | 2076 | A `var(--x)` names an `--x` no line defines | Add the token to the block at the top **in the same commit** as the rule that spends it. |
| G4 | `TestTheTokenBlockIsTheDesignSystem` | 133 | A token in the hand-transcribed `designTokens` map (line 31) is missing from the stylesheet or spelled differently | The map is a **manual transcription of `docs/design-system.md`**, deliberately not read from the stylesheet. A new token must be added to the doc, the stylesheet, and the map. |
| G5 | `TestTheStylesheetAndTheMarkupNameTheSameThings` | 443 | A rendered class has no rule, **or** a styled class is rendered nowhere | Only add rules for classes the templates already render. `styledClasses` (414) strips media preludes, so a class styled only inside a query still counts as styled and still needs markup. |
| G6 | `TestTheComponentsDocumentNamesThePickerTheSwitchAndTheHeader` | 499 | A `.combo*`, `.switch*` or `.masthead*` rule exists that `docs/components.md` never names, or the reverse | **Verified**: the doc and the stylesheet currently agree exactly on eight names. Adding a *rule* for an already-named class is free. Introducing a **new** class in those three families requires a `docs/components.md` edit in the same commit. |
| G7 | `TestHiddenAlwaysWins` | 1766 | No `[hidden] { … display: none !important }`, **or** a `\n\.[a-z-]+\s*\{[^}]*display:` match appears after it | Put every new rule **before** the `[hidden]` rule at the end of the file. |
| G8 | `TestReducedMotionStopsEveryTransition` | 274 | The reduced-motion block's `*` rule lacks `transition: none`, or anything in that block sets a non-`none` transition | Add no transition and no animation anywhere in this milestone. The universal reset already covers what exists. |
| G9 | `TestTheFocusRingSurvives` | 217 | `:focus-visible` stops setting `outline`/`var(--phosphor)`/`outline-offset`, or `outline: none\|0` appears anywhere | Never write `outline: none`. Nothing in this milestone should touch the ring at all. |

**G7's ordering check has a loophole, and a task must not rely on it.** The regex
`\n\.[a-z-]+\s*\{[^}]*display:` requires the class to start at column zero, so an
*indented* rule inside a media block placed after `[hidden]` would not be caught.
It would still be correct CSS — `!important` wins regardless of order — but
"passes because the regex cannot see it" is not the same as "is right". Place new
blocks before `[hidden]` and the question does not arise.

**Alternatives considered.** Relaxing G1 to permit a second block at the same
width — rejected: the guard's value is that it is unconditional, and the moment it
takes an argument it stops being read as a rule. Teaching the sweeps about media
blocks — rejected: they already handle them correctly, and the current behaviour
(no exemption inside a query) is the stricter and better one.

---

## R2 — Where the pointer-coarse block goes, and why it is honest

**Decision.** One new block, `@media (pointer: coarse) { … }`, placed
**immediately after the `@media (prefers-reduced-motion: reduce)` block and before
the `[hidden]` rule** at the end of the file.

**Why it passes G1, quoted rather than asserted.** The guard's expression is:

```go
regexp.MustCompile(`\((?:max|min)-width\s*:\s*([^)]+)\)`)
```

It matches only a **width** feature. `(pointer: coarse)` contains no `max-width`
or `min-width` substring, so the count stays at one. This is the guard being
satisfied, not evaded: G1's subject is *layout variation by viewport*, and a
pointer query changes no layout — see the policy below.

**Why it must sit after the rules it overrides.** These declarations are
single-class overrides of existing single-class rules — identical specificity —
so order alone decides. `.button { … }` at line 400-odd and
`@media (pointer: coarse) { .button { min-block-size: var(--tap) } }` tie on
specificity, and the later one wins. Placed *before* `.button`, the block would
parse, pass every guard, and do nothing — the exact silent failure mode `--bright`
and `--glow` already demonstrate in this file.

**The policy this establishes, which `docs/design-system.md` must state:** a
pointer-conditioned block changes **ergonomics — size, spacing, input scale — and
never layout**. That is what keeps G1 honest rather than merely satisfied: layout
still varies on exactly one axis, and that axis is still tested.

**Alternatives considered.** `@media (any-pointer: coarse)` — rejected; it matches
a desktop with a touchscreen attached, inflating controls for a mouse user.
`(hover: none)` — rejected; it answers a different question (can this device
hover) and would miss a touch device with a stylus. Range syntax `(width <= 780px)`
for the layout half — **rejected in the strongest terms**: it would slip past G1's
regex while doing exactly what G1 forbids. That is routing around a hook, which
`AGENTS.md` prohibits by name.

---

## R3 — The two new tokens

**Decision.**

```css
--tap: 44px;        /* the touch minimum */
--fs-input: 16px;   /* the size below which mobile browsers zoom on focus */
```

Both go in the **first** rule of the stylesheet (the token block), because
`tokenBlockAndRules` splits positionally at the first `{`/`}` pair.

**Why they must be tokens rather than literals**: G2's length pattern fails
`44px` and `16px` anywhere below the token block, including inside a media query.
There is no way to write these values inline.

**Keeping the transcription honest (G4).** `designTokens` at
`stylesheet_test.go:31` is a **hand transcription of `docs/design-system.md`**,
deliberately not read from the stylesheet it checks — the comment at line 21 says
why: a test that compared the file against its own spelling would still pass on a
palette that had quietly drifted.

So a new token is a **three-file change, in one commit**:

1. `docs/design-system.md` — declare it, with the reason it exists
2. `web/static/crswd.css` — add it to the token block
3. `internal/httpapi/stylesheet_test.go` — add it to `designTokens`

Doing two of the three is the failure mode. Adding to the map and the stylesheet
but not the doc passes every test and breaks the map's stated premise.

**Why `--fs-input` and not `--fs-touch`**: the existing typography tokens are all
`--fs-*` (`--fs-nano`, `--fs-eyebrow`, `--fs-label`, `--fs-pane`). The name
follows the family it joins.

**Alternatives considered.** A single `--touch` token used for both — rejected;
one is a length and one is a font size, and collapsing them would make the next
change to either a change to both. Deriving `--tap` from the spacing scale
(`calc(var(--s5) * 2)` ≈ 48px) — rejected; 44px is a published platform figure and
naming it directly is more honest than arriving at a near-miss by arithmetic.

---

## R4 — Stacking table rows, and the percentage question

**The question asked**: does G2's sweep treat `clip-path: inset(50%)` as a literal
length?

**Answer: no, and this is verified rather than assumed.** G2's length pattern is:

```go
{"a hard-coded length", regexp.MustCompile(`\d+(\.\d+)?(px|rem|em|pt|ch|ex|vh|vw)\b`)}
```

The unit alternation is `px|rem|em|pt|ch|ex|vh|vw`. **`%` is not in it.** A
percentage is not a length this design system tokenises — and correctly so: `100%`
and `50%` are relationships to a container, not values from a palette. The
stylesheet already spends `inline-size: 100%` on `.settings table` (line 1136)
and passes.

**Decision.** Accessible hiding uses:

```css
position: absolute;
inline-size: 1px;          /* ← FAILS G2 */
```

— **no.** The conventional visually-hidden recipe spends `1px` twice, and G2
fails it. The recipe that survives this stylesheet's rules is:

```css
.settings-table thead {
  position: absolute;
  clip-path: inset(50%);
  overflow: hidden;
}
```

`inset(50%)` collapses the element to nothing while leaving it in the
accessibility tree, spends only a percentage, and needs no token.

**Decision on the rows themselves**: inside the existing 780px block, `tr` becomes
`display: grid` with one column, and `th`/`td` become blocks with
`overflow-wrap: anywhere`.

**The cost, recorded rather than hidden**: `display: grid` on a `<tr>` removes the
row's table-row semantics in most browsers' accessibility mapping. The column
headers are hidden accessibly rather than deleted, and the linear order — key,
value, provenance — reads correctly on its own. This is a real trade and the spec
records it as an open question with a named fallback.

**Alternatives considered.** `clip: rect(0 0 0 0)` — rejected; deprecated, and the
`0`s would need a token to avoid looking like a literal. `display: none` on
`thead` — rejected; it removes the headers from assistive technology entirely,
which is the failure this recipe exists to avoid. A `<dl>` per setting via a
template change — rejected under R7; it is markup churn for a presentational
problem, and it would break the desktop table.

---

## R5 — Moving the settings overflow

**Decision.** `.settings` loses `overflow-x: auto`; `.settings-panel` gains it.

```css
/* before — crswd.css:1131 */          /* after */
.settings {                            .settings-panel {
  overflow-x: auto;                      min-inline-size: 0;
}                                        padding-inline-start: var(--s2);
                                         overflow-x: auto;
                                       }
```

**Why this is the whole fix for one third of the settings complaint**: `.settings`
is the **grid wrapper holding both the menu and the panel**. Any content wider
than the viewport therefore pans the menu along with the table. Moving the
property one level down scopes the pan to the content that is actually too wide.

This is unconditional — it is as right on a desktop as on a phone, and the reason
it was never noticed is that a desktop is wide enough that nothing overflows.

**The assertion**:

```go
if strings.Contains(blockFor(t, source, ".settings-panel"), "overflow-x") == false { … }
```

plus the negative half — `blockFor(t, source, ".settings {")` must **not** carry
`overflow-x` — because the point is the move, and a task that added the second
without removing the first would leave the pan exactly where it was.

**Alternatives considered.** Wrapping the table in a new scroll container element —
rejected under R7; it is a template change, and it would need a new class, which
G5 then requires markup for and G6 might require documentation for. The property
already exists; it is on the wrong element.

---

## R6 — The pane

**Decision, in two parts with deliberately different scopes.**

```css
/* unconditional, in the .pane rule at line 891 */
overscroll-behavior-x: contain;

/* inside the existing @media (max-width: 780px) block */
.pane {
  white-space: pre-wrap;
  overflow-wrap: anywhere;
}
```

**Why the wrap is conditional.** Above the breakpoint the pane is wide enough for
80 columns and alignment is preserved, which is what a terminal is for. Wrapping
there would damage the desktop experience to fix a phone.

**Why the overscroll containment is not.** Scroll chaining out of a horizontally
panning container is not a phone-only problem — a trackpad does it too — and even
after the wrap ships, an unbroken run longer than the viewport still produces a
horizontal scrollbar. The declaration is correct at every width, so it is written
once at every width.

**Why vertical overscroll is deliberately left alone.** `overscroll-behavior-y:
contain` on an element with `max-block-size: var(--pane-h)` (30rem = 480px) on a
~660px viewport would mean that a flick starting inside the pane — which is most
of the screen — **stops at the pane's end instead of scrolling the page**. That
traps the reader in a box occupying most of their display. The horizontal axis has
no such cost because the page does not scroll horizontally at all.

**The precondition that makes wrapping safe, verified rather than assumed.**
`argvCapturePane` in `internal/tmuxctl/exec.go` is `capture-pane -p -t <target>`
with **no `-N` flag**, so tmux right-trims trailing whitespace on every captured
line. Without that, every blank-padded 80-column line would wrap into a line plus
an empty stub and the pane would double in height. With it, only lines carrying
real content past the viewport wrap.

**Why `overflow-wrap: anywhere` rather than `break-word`**: `anywhere` breaks
within an unbroken run — a long path, a URL, a base64 blob — which is exactly what
a real terminal does at its column edge. `break-word` leaves such a run
overflowing, which reintroduces the horizontal pan for the worst case.

**What is deliberately not done**: `maximum-scale` is never added to any viewport
meta tag. Pinch-zoom is the operator's escape hatch for the alignment-dependent
output this trade damages, and clamping it would remove the mitigation at the same
time as introducing the problem.

---

## R7 — Does any of this need a template change?

**Decision: no. Confirmed, not assumed.** Every layout, sizing and overflow change
in this milestone is achievable against markup that already exists.

Checked individually:

| Change | Markup it needs | Already rendered? |
|---|---|---|
| Pane wrap | `.pane` | yes |
| Settings overflow move | `.settings-panel` | yes |
| Menu → scrolling row | `.settings-menu`, `.settings-menu-list`, `.settings-menu-link` | yes |
| Stacked rows | `.settings-table` + `thead`/`tr`/`th`/`td` | yes |
| Touch targets | `.button`, `.masthead-link`, `.combo-list`, `.switch-*`, `.settings-menu-link`, `.card-actions` | yes |
| Input size | `.field-input`, `.setting-input` | yes |
| Card text wrap | `.card-name`, `.card-path` | yes |
| Masthead | `.masthead-bar`, `.operator` | yes |
| `--glow` fix | `.settings-menu`, `.settings-menu-link[aria-current]` | yes |

**This matters more than it looks.** Because no new class is introduced, **G5 and
G6 cannot fire in this milestone at all** — every rule targets a class the
templates already render, and no new `.combo*`/`.switch*`/`.masthead*` name is
created. That removes two of the nine guards from every task's risk surface, and
it is the strongest argument for doing this in CSS alone.

**The two non-CSS edits, and they are both prose:**

1. `web/templates/settings.html` — the header comment states the page has "no
   form… no page token, no action row and no live region" and the page renders all
   four. A false comment in a repository whose executor reads comments as contract
   is a defect.
2. `docs/design-system.md` and `docs/components.md` — the breakpoint paragraph,
   the new tokens, the pointer-coarse policy, and the pane's typography row.

**Refuted alternative**: a `<dl>`-per-setting template variant. It would give
cleaner stacking semantics than restyled table rows, and it is rejected because it
requires either abandoning the desktop table or rendering two markup shapes for
one page — and because the accessibility cost it fixes is one of the three
questions the spec deliberately leaves open for a device check. Building the
heavier answer before knowing the lighter one is inadequate is the wrong order.

---

## R8 — Carrying the three open questions to the end

**The problem this answers.** The spec records three questions no test here can
settle. The failure mode is not that they go unanswered — it is that a task
closing out the milestone ticks them because everything is green, and the
milestone reports success while nobody has looked at a phone. That is milestone
4's failure with a different subject.

**Decision.** Three mechanisms, because one is not enough:

1. **`docs/mobile-open-questions.md`** — a committed file, created by the first
   task and required to still exist and still list three unanswered questions at
   the last. A question is answered by the operator's report replacing it, never
   by a task.
2. **Each affected contract carries the question inline**, marked `OPEN`, beside
   the change that raises it — so an iteration implementing the stacked rows reads
   the ambiguity risk in the same document as the rule.
3. **The final task's completion criteria include re-reading that file** and
   confirming all three are still recorded with their fallbacks. Its proof is the
   file's content, not a claim.

**The fallbacks, named in advance so a bad answer is a decision rather than a
redesign:**

| Question | Fallback |
|---|---|
| Wrapped pane against real TUI chrome | Delete `white-space: pre-wrap` and `overflow-wrap: anywhere` from the 780px block. One-line revert. |
| Provenance word reads as part of the value | Render an explicit label in the row — a template change, specced but not built |
| Scrolling menu's active chip starts offscreen | Replace the scrolling row with a `<details>` disclosure — a template change, specced but not built |

**Alternatives considered.** A `NEEDS CLARIFICATION` marker in the spec — rejected;
it blocks planning on an answer that cannot arrive until after the code ships. A
`t.Skip` test — rejected; a skipped test is noise that gets deleted. A GitHub
issue per question — reasonable, and kept as the follow-up once the operator has
looked; an issue filed now would say "check this on your phone", which is what the
file says, in the repository, next to the thing it is about.
