# Design System

> Loaded when: touching UI, layout, spacing, or colour.
> These are rules, not suggestions.

The dashboard is a single-operator control surface for live terminal sessions.
The theme is **Matrix**: phosphor green on near-black, monospace, digital rain.

That is not a costume. This product is a wall of terminal output — green-on-black
monospace is what the content already *is*, so the theme is the subject rendered
honestly rather than decoration applied on top. Every rule below exists to keep it
readable while it looks like that.

## Non-negotiables

Referenced by `.specify/memory/constitution.md`. Violating one fails review.

1. **Tokens only.** Never hard-code a hex, px, or font-family in a template.
   If a value is not in this file, add it here first.
2. **Top-right always shows identity** — the Google account from the Access JWT,
   so it is never ambiguous whose credentials are driving sessions on this host.
3. **One scale.** Spacing comes from the scale below. No arbitrary margins.
4. **Pane output is text, never markup.** See `security.md` — a security rule that
   is also a design rule. Style the container, never the content.
5. **Never encode state by colour alone.** Every status carries a text label.
   Colour is reinforcement.

## Single theme, by choice

There is no light mode. A light-mode Matrix would misrepresent the product, so the
tokens are **pinned across every `data-theme`** rather than left to a viewer toggle
that could half-apply them:

```css
:root,
:root[data-theme="dark"],
:root[data-theme="light"] { /* tokens */ }
```

This is a deliberate commitment to one visual world, not an omission. If a second
theme is ever wanted, it is a design decision to reopen here — not something to
bolt on in a template.

## Colour tokens

The ground is near-black with a **green bias**, not pure black, so the phosphor sits
inside the palette rather than on top of it.

```css
:root {
  --ground:       #050705;  /* page */
  --surface:      #0a0f0a;  /* card, panel */
  --surface-lift: #101710;  /* hover, raised */
  --edge:         #17331f;  /* borders */
  --edge-bright:  #1f5c33;  /* emphasis borders, labels */

  --text:         #c6f7d0;  /* body — soft phosphor */
  --dim:          #6f9c7c;  /* secondary */
  --phosphor:     #00ff41;  /* THE accent. Spend sparingly. */
}
```

**Body text is `--text`, not `--phosphor`.** Full-saturation green as running text is
genuinely fatiguing. `--phosphor` is spent only on the brand, focus rings, the lead
glyph of a rain column, and the occasional emphasis inside a pane. If everything
glows, nothing does.

Contrast on `--ground`: `--text` ≈ 17:1, `--dim` ≈ 6.5:1, `--phosphor` ≈ 16:1. All
clear WCAG AA. Any new pairing must be measured, not eyeballed.

## State tokens

Semantic colour is **separate from the accent** and deliberately not monochrome —
legibility at a glance beats theme purity:

| State | Token | Value | Why this colour | Rendered when |
|---|---|---|---|---|
| `running` | `--state-running` | `#00ff41` | Alive, the phosphor itself | **Milestone 2** |
| `idle` | `--state-idle` | `#3fa85c` | Alive but waiting — dimmer, not absent | Withdrawn in milestone 15 |
| `needs-auth` | `--state-auth` | `#ffb000` | Amber phosphor. In-world: real terminals had amber | Milestone 4 |
| `dead` | `--state-dead` | `#ff4d4d` | The red pill — the one non-green the palette has earned | Not currently reachable |

Do not invent a parallel palette for state, and do not fold state back into green
for the sake of the theme. A dead session that reads as green is a bug.

**Display state is derived, not stored.** The daemon writes only `starting` and
`running` to a record, and deletes a record rather than marking it dead, so the
interface computes what to show — and since milestone 15 it computes **running**
for every live session. `starting` has no token and never appears: the distinction
from `running` lasts milliseconds and means nothing to an operator watching a
fleet.

`idle` was the second derived state, shown when a session had produced nothing for
longer than the reaper's idle threshold. The bound was withdrawn with constitution
2.0.0 and the state went with it. **The token stays** — like `needs-auth` and
`dead`, it must keep working in the status component, and a palette that lost a
colour every time a state was withdrawn would be a palette rewritten by feature
work.

`needs-auth` and `dead` keep their tokens and must keep working in the status
component. `needs-auth` arrives with the device-code relay; `dead` is unreachable
today because destroyed and reaped sessions leave no record, and a state that
renders wrongly the first time it occurs is a defect that ships in silence.

## Typography

A real pairing, with a reason: **monospace for what the machine says, sans for what
a human wrote.** Mono is data, labels, panes, session names. Sans is help text,
empty-state copy, error explanations.

```css
--mono: ui-monospace, "SF Mono", "JetBrains Mono", "Fira Mono", Menlo, Consolas, monospace;
--sans: ui-sans-serif, system-ui, -apple-system, "Segoe UI", Roboto, sans-serif;
```

**No webfont URLs.** The daemon serves under a strict CSP with no CDN (`security.md`),
so a linked face would fail silently and fall back. If a face is ever genuinely
needed, inline it as a `@font-face` data URI and embed it via `go:embed`.

| Role | Size / line-height | Treatment |
|---|---|---|
| Brand | `1.05rem` | 700, uppercase, `letter-spacing: .22em`, phosphor + glow |
| Section eyebrow | `.72rem` | 600, uppercase, `letter-spacing: .2em`, `--dim` |
| Body | `14px / 1.5` | mono, `--text` |
| Prose / help | `.86rem` | **sans**, `--dim` |
| Label / pill | `.64–.68rem` | uppercase, `letter-spacing: .14em` |
| Pane output | `12.5px / 1.45` | mono, `white-space: pre`; `pre-wrap` under the breakpoint, `tab-size: 4` |

Pane output sets `font-variant-ligatures: none` — ligatures misrepresent terminal text.

`pre-wrap` below `780px` is a **trade, not a fix**: it costs the alignment of
Claude Code's own box borders and dividers, and buys reading prose without a
horizontal pan per line — the dominant phone task, which otherwise fails outright.
Shrink-to-fit was rejected with arithmetic: 80 columns in a 390px viewport needs a
~6.9px font. Whether the wrapped chrome is acceptable is
[an open question](mobile-open-questions.md) whose fallback is deleting two
declarations.

## Spacing scale

```css
--s1: .25rem; --s2: .5rem;  --s3: .75rem; --s4: 1rem;
--s5: 1.5rem; --s6: 2rem;   --s7: 3rem;
```

## Radius, border, elevation

```css
--r: 3px;                          /* terminals are not rounded */
--edge-width: 1px;
```

Elevation comes from `--surface-lift` and borders, never shadows. The single
exception is a modal overlay.

**That exception is now spent, and by one rule.** `.modal` — the create dialog
(milestone 11, see `components.md`) — carries `box-shadow: var(--glow)`, which
is the only shadow on a surface in this stylesheet. It had been a theoretical
grant since this sentence was written; it is a real one now, and it stays a
single exception rather than becoming a precedent. A card, a panel, a menu or a
toast reaching for `--glow` is still the drift Principle VII forbids, and
`TestNoBackgroundSpendsAShadowToken` still refuses the specific mistake of
spending it on a `background`, where it computes to nothing and takes the whole
declaration with it.

## Digital rain

The signature effect. It appears in exactly **two** places, at two strengths:

| Surface | Opacity | Why it is allowed there |
|---|---|---|
| Header band | `.16` | Ambient. Low enough that the masthead never competes |
| Empty state | `.5` | There is no data to compete with — it fills the void instead of a shrug |

Rules:
- **Never behind reading content.** Not behind a pane, a card grid, a form, or a
  table. If content sits on it, the rain is too strong or in the wrong place.
- Katakana + digits, `ui-monospace`, `--phosphor`, lead glyph in `--text`.
- One shared `requestAnimationFrame` loop over every `.rain` canvas, throttled to
  ~14fps. It reads as rain and costs almost nothing.
- Wipe with a translucent fill (`rgba(5,7,5,.22)`), not `clearRect` — the trail is
  the effect.
- **`prefers-reduced-motion: reduce` removes it entirely.**
- Canvas, never hand-authored SVG or DOM nodes per glyph.

A third home is reasonable when a login screen exists. Anywhere else needs a
justification in the PR.

## Scanlines

`repeating-linear-gradient` at **3% opacity, on the pane viewer only**, as a
`::after` overlay. Never page-wide. Removed under `prefers-reduced-motion`.

## Motion

```css
--transition: .12s ease;
```

Hover, focus, and disclosure only. **Never animate pane output** — live terminal
text that slides or fades is unreadable. New lines appear instantly.

## Focus

```css
:focus-visible { outline: 2px solid var(--phosphor); outline-offset: 2px; }
```

Never remove the outline without replacing it.

## Layout

Sticky common header: brand left, operator identity right. Below it a **state
summary row before any detail** — a dashboard is scanned, not read — then the
session grid.

```
┌──────────────────────────────────────────────┐
│ CRSWD  session control        [operator ▾]   │  ← rain behind, .16
├──────────────────────────────────────────────┤
│ [running]                                    │  ← summary first
│ ┌────────┐ ┌────────┐ ┌────────┐             │
│ │ card   │ │ card   │ │ card   │             │  ← auto-fill, min 310px
└──────────────────────────────────────────────┘
```

Shell max-width `1160px`, gutters `--s5`. Session grid is
`repeat(auto-fill, minmax(310px, 1fr))`.

Wide content — panes, tables — scrolls inside its own `overflow-x: auto` container.
The page body never scrolls sideways.

The pane viewer is that container in both axes: a fixed block size of
`--pane-h: 30rem` with `overflow: auto`, so a screen scrolls inside the pane and
never moves the page around it (`components.md`: the container scrolls, the page
does not).

### One width breakpoint, at `780px`

**There is exactly one**, and `TestTheDashboardHasExactlyOneBreakpoint` enforces
both the count and the value. One operator with a laptop and a phone needs one
threshold; each additional one is a layout nobody looks at until it is already
wrong. Everything that varies with width varies at the same place.

Everything inside `@media (max-width: 780px)`:

| Selector | Change | Why |
|---|---|---|
| `.shell` | gutters `--s5` → `--s4` | A phone cannot spend `--s5` on each edge and still hold a card |
| `.summary` | two columns | Four state pills side by side are unreadable at 390px |
| `.brand-tag` | hidden | The tagline is the first thing that is decoration, not information |
| `.settings` | one column | The section menu goes above its panel rather than beside it |
| `.pane` | `white-space: pre-wrap` + `overflow-wrap: anywhere` | 80 columns through a 44-character window is a pan per line. The trade is described under Typography; the base rule keeps `pre` |
| `.settings-menu` | `position: static` | Sticky has no travel room in a one-column grid, so it does nothing — said rather than left to lapse |
| `.settings-menu-list` | `grid-auto-flow: column` + `justify-content: start` + `overflow-x: auto` | Stacked, seven sections are ~300px of links above the thing the operator opened the page to read |
| `.settings-menu-link` | `white-space: nowrap` | A chip that wraps is two rows of one entry |
| `.settings-menu-link[aria-current="page"]` | marker moves to `border-block-end` | A start-edge bar reads as a divider between chips rather than a mark on one. Still a border as well as a colour |
| `.settings-table thead` | `position: absolute` + `clip-path: inset(50%)` + `overflow: hidden` | Once a row is a stack there is no column for a header to head. Clipped rather than `display: none` so the column name stays in the accessibility tree — a stacked value has lost its column and the `th` is the only thing still carrying its name. `inset(50%)` is also the only spelling this file can afford: the conventional visually-hidden recipe sizes in absolute lengths, and the value sweep fails a literal length inside a media query too |
| `.settings-table tr` | `display: grid` + `grid-template-columns: minmax(0, 1fr)` | Key, value and source do not fit ~358px. The editable row is why it matters rather than the width alone — its input and Save button make it the widest row on the page, so changing a setting meant typing inside a horizontal pan |
| `.settings-table th`, `.settings-table td` | `overflow-wrap: anywhere` | A config key is one unbroken token with no column beside it left to give way |
| `.card-name`, `.card-path` | `white-space: normal` + `overflow-wrap: anywhere` | The ellipsis puts the full value in a `title`, and a `title` needs a hover a touch device does not have — so both were unreachable rather than shortened. The base rule keeps `nowrap`: on a desktop the ellipsis is what stops one long path setting the height of a whole grid row |
| `.masthead-bar` | gutters `--s5` → `--s4` | `.shell` narrows here and the header did not, so the one band on every screen sat 8px outside every card and table beneath it. The rule is the page's gutter rather than a number of its own, and the test reads `.shell` rather than naming a token, so the two cannot drift apart quietly |
| `.operator` | `flex: 1 1 0` + `min-inline-size: 0` + `text-align: end` | A flex line wraps on its items' *hypothetical* sizes, so a long address wrapped the sticky bar to two rows and the ellipsis already on the identity only helped after it had. Basis `0` makes that hypothetical size zero and the ellipsis does the work instead. `min-inline-size` says what the base rule's `overflow: hidden` already buys; `text-align` is because basis `0` grows the item to fill, and identity at the top right is the second non-negotiable of this file |

**Adding a rule to that block adds a row to this table, in the same commit.** An
enumeration that has gone stale is worse than no enumeration — this table already
said "two effects" once while the block did four things.

Three rules about how it is written:

- **Additions go inside the existing block.** A second `@media` block fails the
  guard *even at an identical width* — it counts occurrences of a width feature, it
  does not compare them.
- **Never range syntax** (`(width <= 780px)`). It slips past the guard's regex
  while doing exactly what the guard forbids, which is routing around a hook.
- **The block sits after every rule it overrides**, which is why it is the last
  thing in the stylesheet before `[hidden]`. A media query adds no specificity, so
  `.settings` inside it and `.settings` at the top level are a tie broken by source
  order alone. The block spent milestone 6 declared *above* the settings rules,
  where the one-column collapse parsed, passed every guard and lost to the
  two-column rule 250 lines further down — the settings page was never one column
  on a phone. `TestTheBreakpointOverridesRatherThanPrecedes` now holds it, per
  selector rather than per block, because the hazard is a base rule declared low
  in the file rather than the block having moved.

### Pointer, not width, for touch

A tablet in landscape is a touch device at a desktop width; a narrow desktop window
is a mouse. So touch ergonomics are conditioned on the pointer, and:

> A pointer-conditioned block (`@media (pointer: coarse)`) changes **ergonomics —
> size, spacing, input scale — and never layout.** Layout varies on exactly one
> axis, and that axis is tested.

`(pointer: coarse)` is not a width feature, so it passes the breakpoint guard
honestly rather than by evasion. It sits **after** the rules it overrides: at equal
specificity, order alone decides, and a coarse block placed above them parses
cleanly, passes every guard, and does nothing.
`TestTheCoarseBlockOverridesRatherThanPrecedes` holds that, per selector and then
per property, because the block can be wrong either by being placed early or by a
base rule arriving below it later.

Everything inside `@media (pointer: coarse)`:

| Selector | Change | Why |
|---|---|---|
| `.button` | `min-block-size: var(--tap)` | Padding alone gives a button roughly half the published minimum. On the component rather than on Destroy, so the next button inherits the size rather than needing it |
| `.settings-menu-link`, `.combo-list li`, `.rename-summary`, `.release-notes summary` | `padding-block: var(--s3)` | Four controls that are a line of text tall. A disclosure and a listbox option are as tappable as a button and get none of a button's box |
| `.masthead-link` | `padding-block: var(--s3)` + `margin-block: calc(var(--s3) * -1)` | The hit area grows and the bar's height does not — the margin gives the layout back what the padding took. It is a flex item, so both apply |
| `.field-switch` | `min-block-size: var(--tap)` | The row is the target: the checkbox and its label are one control, and the box alone is `--s4` square |
| `.card-actions` | `gap: var(--s3)` | Destroy sits beside Compact and a thumb covers both. Enlarging the buttons without moving them apart makes that worse, not better |
| `.field-input`, `.setting-input` | `font-size: var(--fs-input)` | Below `16px` a mobile browser zooms the page on focus, so every rename, create and settings edit zoomed and panned and the operator pinched back out. Both, because they are different forms — create/rename and settings editing — and one of the two leaves half of them zooming |

**Adding a rule to that block adds a row to this table, in the same commit** — the
same obligation the width block carries, for the same reason.

The two values that block spends are declared here, because rule 1 makes this file
the only door a value comes through:

```css
--tap: 44px;       /* the published platform minimum for a touch target */
--fs-input: 16px;  /* at or above this, a mobile browser does not zoom on focus */
```

Both are named rather than spelled inline so the *reason* travels with the number:
`44px` is a figure the platforms publish, and `16px` is a browser threshold, not a
size anyone chose for how it looks. They stay **two** tokens — one length, one font
size — because collapsing them would make the next change to either a change to
both.

Questions about the phone layout that no test here can settle live in
[`mobile-open-questions.md`](mobile-open-questions.md).
