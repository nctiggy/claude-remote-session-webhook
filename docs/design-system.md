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

| State | Token | Value | Why this colour |
|---|---|---|---|
| `running` | `--state-running` | `#00ff41` | Alive, the phosphor itself |
| `idle` | `--state-idle` | `#3fa85c` | Alive but waiting — dimmer, not absent |
| `needs-auth` | `--state-auth` | `#ffb000` | Amber phosphor. In-world: real terminals had amber |
| `dead` | `--state-dead` | `#ff4d4d` | The red pill — the one non-green the palette has earned |

Do not invent a parallel palette for state, and do not fold state back into green
for the sake of the theme. A dead session that reads as green is a bug.

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
| Pane output | `12.5px / 1.45` | mono, `white-space: pre`, `tab-size: 4` |

Pane output sets `font-variant-ligatures: none` — ligatures misrepresent terminal text.

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
│ [running] [idle] [needs auth] [dead]         │  ← summary first
│ ┌────────┐ ┌────────┐ ┌────────┐             │
│ │ card   │ │ card   │ │ card   │             │  ← auto-fill, min 310px
└──────────────────────────────────────────────┘
```

Shell max-width `1160px`, gutters `--s5`. Session grid is
`repeat(auto-fill, minmax(310px, 1fr))`.

Breakpoint at `780px`: summary drops to two columns, the brand tagline hides. Two
breakpoints is enough for one operator on a laptop and a phone.

Wide content — panes, tables — scrolls inside its own `overflow-x: auto` container.
The page body never scrolls sideways.
