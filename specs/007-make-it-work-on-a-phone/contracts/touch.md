# Contract: Touch ergonomics

**Files**: `web/static/crswd.css`, `internal/httpapi/stylesheet_test.go`, `docs/design-system.md`
**Satisfies**: FR-011 … FR-016
**Decomposed**: two tasks — target sizes, then input scale.

---

## The decision that shapes this whole contract

**Touch ergonomics are conditioned on the pointer, not the viewport.**

```css
@media (pointer: coarse) { … }
```

**The argument**: a tablet in landscape is a touch device at a desktop width, and
the 780px breakpoint never reaches it. A narrow desktop window is a mouse, and
should not get inflated controls. **Target size is a property of the pointer, not
the viewport.**

**Why this passes G1 honestly rather than by evasion.** The guard's expression is:

```go
regexp.MustCompile(`\((?:max|min)-width\s*:\s*([^)]+)\)`)
```

It matches only a **width** feature. `(pointer: coarse)` contains neither
`max-width` nor `min-width`, so the count stays at one. G1's subject is layout
variation by viewport, and this block changes no layout.

**The policy that keeps that true, and `docs/design-system.md` must state it:**

> A pointer-conditioned block changes **ergonomics — size, spacing, input scale —
> and never layout.**

A task that puts a `grid-template-columns` or a `display` change in this block has
broken the policy even though every test still passes. That is the one rule here
no guard enforces.

---

## Where the block goes

**Immediately after the `@media (prefers-reduced-motion: reduce)` block, before
the `[hidden]` rule at the end of `crswd.css`.**

**It must sit after the rules it overrides.** These are single-class overrides of
single-class rules — identical specificity — so order alone decides. Placed before
`.button`, the block would parse, pass every guard, and **do nothing**. That is the
exact silent failure `--bright` and `--glow` already demonstrate in this file: a
rule that looks correct, passes, and has no effect.

---

## Part 1 — Target sizes

**Two new tokens first** (see `guards.md` G4 — this is a three-file change in one
commit):

```css
/* in the token block, the first rule of crswd.css */
--tap: 44px;
--fs-input: 16px;
```

**The block:**

```css
/* Touch ergonomics, conditioned on the pointer rather than the viewport: a
   tablet in landscape is a touch device at a desktop width, and a narrow desktop
   window is a mouse. Size follows the pointer.
   This block changes size, spacing and input scale — never layout. Layout still
   varies on exactly one axis, and that axis is still tested. */
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

  /* Padded to a thumb without changing the bar's height: the extra hit area is
     taken back from the layout with a negative margin. */
  .masthead-link {
    padding-block: var(--s3);
    margin-block: calc(var(--s3) * -1);
  }

  .field-switch {
    min-block-size: var(--tap);
  }

  /* Destroy sits beside Compact. Labels already distinguish them — this makes
     the geometry agree, so a mis-tap costs a thumb's width rather than a shell. */
  .card-actions {
    gap: var(--s3);
  }
}
```

**Confirm each selector renders before writing it.** `.rename-summary`,
`.release-notes summary` and `.field-switch` must be checked against the templates
— a rule for a class nothing renders fails G5. If one does not exist, drop that
selector and say so; do not add a class to a template to make a rule valid.

---

## Part 2 — Input scale

In the **same** `@media (pointer: coarse)` block:

```css
  /* 16px is the threshold below which mobile browsers zoom the page on focus.
     Every text input here was 14px, so renaming a session, creating one, or
     editing a setting zoomed and panned the layout every single time. */
  .field-input,
  .setting-input {
    font-size: var(--fs-input);
  }
```

Base `.field-input` is at `crswd.css:622` and `.setting-input` at `1379`, both far
above where this block goes — so the override wins on order.

---

## Guards these tasks can trip

| Guard | Risk | How to satisfy |
|---|---|---|
| G1 breakpoint count | **Medium** — a new media block | `(pointer: coarse)` is not a width feature. Verify by running the test, not by reasoning. |
| G2 literal values | **High** — `44px` and `16px` are exactly what it fails | Spend `var(--tap)` and `var(--fs-input)`. Never inline. |
| G3 token exists | **High** | Both tokens land in the same commit as the rules spending them. |
| G4 token transcription | **High** | Three files: `docs/design-system.md`, `crswd.css`, `designTokens` map. All three, one commit. |
| G6 components doc | **Medium** — `.masthead-link`, `.combo-list`, `.switch-*` are in the documented families | All are **already named** in `docs/components.md`. Adding a rule for an existing name is free. Do not introduce a new one. |
| G7 `[hidden]` last | **Medium** | The whole block goes before the terminal rule. |

---

## Contract tests

| Test | Asserts | **Must fail when** |
|---|---|---|
| `TestTouchTargetsFollowThePointerNotTheWidth` | A `@media (pointer: coarse)` block exists and spends `var(--tap)` | Targets are sized inside the 780px block instead, so a tablet at 1024px — a touch device — never gets them |
| `TestTheCoarseBlockChangesNoLayout` | No rule inside the coarse block sets `display`, `grid-template-*`, `flex-direction` or `position` | The pointer block starts carrying layout, so layout varies on an axis nothing tests and G1's guarantee quietly becomes false |
| `TestEveryButtonIsThumbSized` | The coarse block's `.button` rule carries `min-block-size: var(--tap)` | Only some controls are enlarged, so Destroy stays a 24px target beside Compact |
| `TestDestroyIsSeparatedFromCompact` | The coarse block sets `gap` on `.card-actions` | The two stay 8px apart and a mis-tap ends a shell the operator wanted |
| `TestInputsDoNotTriggerFocusZoom` | The coarse block sets `font-size: var(--fs-input)` on `.field-input` **and** `.setting-input` | One of the two is missed, so half the forms still zoom and the fix reads as done |
| `TestTheTouchTokensAreDeclared` | `--tap` and `--fs-input` are in the token block **and** in `designTokens` | A token is added to the stylesheet and the map but not to `docs/design-system.md`, which passes every test and breaks the map's stated premise |
| `TestTheCoarseBlockOverridesRatherThanPrecedes` | The coarse block's offset in the file is **after** the base `.button` and `.field-input` rules | The block is placed early, parses, passes every guard, and has no effect — the silent failure `--bright` and `--glow` already demonstrate here |

`TestTheCoarseBlockOverridesRatherThanPrecedes` is the one worth writing carefully.
It is the only assertion in this milestone that catches a change which is
syntactically perfect and behaviourally absent.

---

## Worked example

```
Operator taps Destroy on a session card, on a phone.

Before: the button is ~24px tall and sits 8px from Compact. The thumb covers
        both. Which one fires is luck.

After:  44px tall, 12px apart. The labels still distinguish them — colour is
        not carrying the difference, and never was.

Operator taps into the rename field.

Before: 14px input. The browser zooms to ~140% and pans. The operator pinches
        back out afterwards. Every time.

After:  16px input. No zoom. Nothing moves.

Desktop, mouse: every control is exactly the size it is today. The block does
        not match.
```
