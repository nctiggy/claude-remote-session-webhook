# Design System

> Loaded when: touching UI, layout, spacing, or colour.
> `<FILL IN>` = replace per project. Everything else is a rule, not a suggestion.

## Non-negotiables

These are referenced by `.specify/memory/constitution.md`. Violating one fails review.

1. **Tokens only.** Never hard-code a hex, px, or font-family in a component.
   If a value is not in this file, add it here first.
2. **Top-right always shows identity.** Every authenticated page header shows the
   signed-in user (avatar/name) and the sign-out affordance in the top-right.
   No page may render an authenticated view without it — that is how users know
   *which account* they are in. See `auth-and-sessions.md` for the bleed rule.
3. **One scale.** Spacing comes from the scale below. No arbitrary margins.

## Colour tokens

`<FILL IN>` — define semantically (by role), never by appearance. `--color-danger`,
not `--color-red`; the red may change, the meaning will not.

```css
:root {
  --color-bg:        <FILL IN>;
  --color-surface:   <FILL IN>;
  --color-text:      <FILL IN>;
  --color-muted:     <FILL IN>;
  --color-primary:   <FILL IN>;
  --color-danger:    <FILL IN>;
  --color-success:   <FILL IN>;
}
```

Contrast: body text must meet WCAG AA (4.5:1). Verify, do not eyeball.

## Typography

```css
--font-sans: <FILL IN>;
--font-mono: <FILL IN>;
```

| Role | Size | Weight |
|---|---|---|
| Page title | `<FILL IN>` | `<FILL IN>` |
| Section heading | `<FILL IN>` | `<FILL IN>` |
| Body | `<FILL IN>` | `<FILL IN>` |
| Caption / meta | `<FILL IN>` | `<FILL IN>` |

## Spacing scale

Use these steps only.

```
4px · 8px · 12px · 16px · 24px · 32px · 48px · 64px
```

```css
--space-1: 4px;  --space-2: 8px;  --space-3: 12px; --space-4: 16px;
--space-5: 24px; --space-6: 32px; --space-7: 48px; --space-8: 64px;
```

## Header layout rule

```
┌──────────────────────────────────────────────────────────┐
│ [logo] [primary nav]              [search] [user ▾] ← ALWAYS │
└──────────────────────────────────────────────────────────┘
```

Left: identity of the *product*. Right: identity of the *user*. Never swap them.

## Responsive

Breakpoints: `<FILL IN: e.g. 640 / 768 / 1024 / 1280>`.
Mobile-first. A layout that only works at desktop width is incomplete.
