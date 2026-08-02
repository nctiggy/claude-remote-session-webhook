# Design System

> Loaded when: touching UI, layout, spacing, or colour.
> These are rules, not suggestions.

The dashboard is a single-operator control surface for terminal sessions. It is dark
by default, dense, and reads like tooling rather than a product. Every decision below
follows from that: legibility of monospace output first, decoration never.

## Non-negotiables

These are referenced by `.specify/memory/constitution.md`. Violating one fails review.

1. **Tokens only.** Never hard-code a hex, px, or font-family in a template.
   If a value is not in this file, add it here first.
2. **Top-right always shows identity.** Every page header shows the Google account
   from the Cloudflare Access JWT, so there is never ambiguity about whose
   credentials are driving sessions on this host.
3. **One scale.** Spacing comes from the scale below. No arbitrary margins.
4. **Pane output is text, never markup.** See `security.md` — a security rule that
   is also a design rule. Style the container, never the content.

## Colour tokens

Semantic by role, never by appearance — `--color-danger`, not `--color-red`. The
red may change; the meaning will not.

```css
:root {
  --color-bg:             #0d1117;
  --color-surface:        #161b22;
  --color-surface-raised: #21262d;
  --color-border:         #30363d;
  --color-text:           #e6edf3;
  --color-muted:          #8b949e;
  --color-primary:        #2f81f7;
  --color-danger:         #f85149;
  --color-success:        #3fb950;
  --color-warning:        #d29922;
}
```

Session state maps onto those roles — do not invent a parallel palette:

| State | Token | Means |
|---|---|---|
| `running` | `--color-success` | Claude is working |
| `idle` | `--color-muted` | Alive, waiting at a prompt |
| `needs-auth` | `--color-warning` | Waiting on a device-code login (milestone 4) |
| `dead` | `--color-danger` | Exited, or teardown failed |

Contrast: `--color-text` on `--color-bg` is 14.2:1; `--color-muted` on `--color-bg`
is 6.4:1. Both clear WCAG AA. Any new pairing must be measured, not eyeballed.

**Never encode state by colour alone.** Every status pill carries a text label; the
colour is reinforcement.

## Typography

```css
--font-sans: ui-sans-serif, system-ui, -apple-system, "Segoe UI", Roboto, sans-serif;
--font-mono: ui-monospace, SFMono-Regular, "SF Mono", Menlo, Consolas,
             "Liberation Mono", monospace;
```

| Role | Size / line-height | Weight |
|---|---|---|
| Page title | `1.5rem / 2rem` | 600 |
| Section heading | `1.125rem / 1.75rem` | 600 |
| Body | `0.9375rem / 1.5rem` | 400 |
| Caption / meta | `0.8125rem / 1.25rem` | 400, `--color-muted` |
| Pane output | `0.8125rem / 1.4` | 400, `--font-mono` |

Pane output is always `--font-mono` with `white-space: pre`, `tab-size: 4`, and
`font-variant-ligatures: none` — ligatures misrepresent terminal text.

## Spacing scale

Use these steps only. 4px base.

```css
--space-1: 4px;  --space-2: 8px;  --space-3: 12px; --space-4: 16px;
--space-5: 24px; --space-6: 32px; --space-7: 48px; --space-8: 64px;
```

## Radius, border, elevation

```css
--radius-sm: 4px;   --radius-md: 6px;   --radius-full: 999px;
--border: 1px solid var(--color-border);
--shadow-overlay: 0 8px 24px rgb(1 4 9 / 0.6);   /* modals only */
```

Elevation is carried by `--color-surface-raised` and borders, not shadows. The lone
exception is the modal overlay.

## Motion

```css
--transition: 120ms ease-out;
```

Hover, focus, and disclosure only. **Never animate pane output** — live terminal
text that slides or fades is unreadable. New lines appear instantly.

Honour `prefers-reduced-motion: reduce` by dropping all transitions to `0ms`.

## Focus

Every interactive element shows a visible focus ring. Never remove the outline
without replacing it.

```css
:focus-visible {
  outline: 2px solid var(--color-primary);
  outline-offset: 2px;
}
```

## Header layout rule

```
┌──────────────────────────────────────────────────────────┐
│ crswd  [sessions]                    [craig@… ▾] ← ALWAYS │
└──────────────────────────────────────────────────────────┘
```

Left: identity of the *product*. Right: identity of the *user*. Never swap them.

## Responsive

Single column, max width `72rem`, centred, `--space-5` gutters. The session list is
a grid that collapses to one column below the small breakpoint.

Breakpoints: `640px` and `1024px`. Two is enough for one operator on a laptop and a
phone; do not add a third without a reason.
