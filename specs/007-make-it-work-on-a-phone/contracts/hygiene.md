# Contract: Cards, masthead, and what the sweep found

**Files**: `web/static/crswd.css`, `internal/httpapi/stylesheet_test.go`
**Satisfies**: FR-017 … FR-023
**Decomposed**: four tasks — cards, masthead, the `--glow` bug, the dead rules.

---

## Part 1 — Card text wraps on narrow viewports (FR-017, FR-018)

**The fault**: `.card-name` and `.card-path` at `crswd.css:440` ellipsize, with the
full text in a `title` attribute. **A `title` needs a hover, and a touch device
does not have one.** On a phone the full session name and the full working
directory are simply unreachable — that is data loss, not styling.

**Inside the existing `@media (max-width: 780px)` block:**

```css
  /* The title attribute is the escape hatch for the ellipsis, and it needs a
     hover a phone does not have — so the full name and path were unreachable.
     One column makes variable card heights harmless. */
  .card-name,
  .card-path {
    white-space: normal;
    overflow-wrap: anywhere;
  }
```

Desktop truncation is unchanged.

---

## Part 2 — The masthead (FR-019, FR-020)

**Two faults**: `.masthead-bar` keeps `padding-inline: var(--s5)` (24px) while
`.shell` drops to `var(--s4)` (16px) at the breakpoint, so the header is visibly
out of alignment with everything under it. And `.operator` is a flex item whose
**hypothetical** (full email) width drives wrapping — shrinking applies only after
the wrap has happened — so a longer identity wraps the sticky bar to two rows.

**Inside the same 780px block:**

```css
  /* The masthead kept desktop gutters after the page narrowed, so the header sat
     8px out of line with everything beneath it. */
  .masthead-bar {
    padding-inline: var(--s4);
  }

  /* flex-basis 0 rather than auto: a flex item wraps on its hypothetical size,
     and the operator's full email is what that is. Basis 0 lets the ellipsis
     that is already there do the work, instead of only helping after the bar has
     already wrapped to two rows. */
  .operator {
    flex: 1 1 0;
    min-inline-size: 0;
    text-align: end;
  }
```

---

## Part 3 — The declaration that has never rendered (FR-021)

**This is a live bug on every device, desktop included.**

`--glow` is declared at `crswd.css:133` as a **shadow list**:

```css
--glow: 0 0 var(--s2) var(--phosphor);
```

It is spent as a **background** in two places — `crswd.css:1339` and `1352`:

```css
.settings-menu            { background: var(--glow); }   /* invalid → renders nothing */
.settings-menu-link[aria-current="page"] { background: var(--glow); }
```

`background: 0 0 .5rem #00ff41` is invalid at computed-value time. The declaration
is dropped. **The settings menu has no surface and the current section has no tint,
and never has.**

**The fix:**

```css
.settings-menu {
  background: var(--surface);
}

.settings-menu-link[aria-current="page"] {
  background: var(--surface-lift);
}
```

### Read the comment above these lines before changing them

`crswd.css:1330` currently says:

> "That rule earned its keep here. The colour was `var(--bright)`, which is not a
> token this stylesheet defines, so it resolved to nothing and the current section
> was never tinted at all. The border and aria-current carried the whole marker
> and nobody noticed, which is exactly what 'not by hue alone' is for: the
> redundant cue was the working one."

**The comment describing this exact class of defect sits directly above another
instance of it.** `TestEveryTokenReferencedExists` was added in response to
`--bright` and cannot catch `--glow`, because it checks that a referenced token
**exists** — not that it is of a *kind the property accepts*.

Update the comment to record both, and to name the gap: a token can exist, be
referenced correctly, and still be the wrong kind for the property spending it.

**The redundant cue is why this was survivable.** The border and `aria-current`
carried the marker for both bugs. That is the second time "never by hue alone" has
silently saved this page, which is worth writing down rather than fixing quietly.

### The guard worth adding

```go
func TestNoBackgroundSpendsAShadowToken(t *testing.T) {
  // --glow is a shadow list. A background built from one is dropped at
  // computed-value time and renders nothing, which no existing guard catches.
}
```

Assert no `background` or `background-color` declaration anywhere in the
stylesheet references `var(--glow)`. Narrow and honest: it closes the instance
without pretending to a general type system for CSS.

---

## Part 4 — Rules that match markup the page does not render (FR-022)

At `crswd.css:1135–1176`, styling from the settings page's **pre-#103 flat-table
design** still applies to today's markup by descendant selector:

| Rule | Status |
|---|---|
| `.settings table` | still matches — the sectioned page renders tables |
| `.settings th`, `.settings td` | still matches |
| `.settings p` | still matches `.settings-source` |
| `.settings caption` | **matches nothing — `settings.html` renders no `<caption>` at all** (verified: zero occurrences) |

**The class sweep (G5) cannot see any of this**, because these are *element*
selectors under a class. That is the gap this part closes.

**The task**: delete `.settings caption` outright. For the other three, confirm
against `settings.html` whether the sectioned markup still depends on them; keep
what is load-bearing and delete what is not, and **say in the commit message which
was which**. Do not delete on suspicion — a removed rule that was doing work is an
unstyled page, and the sweep will not catch that either.

---

## Guards these tasks can trip

| Guard | Risk | How to satisfy |
|---|---|---|
| G1 breakpoint count | **Medium** — parts 1 and 2 are width-conditional | Inside the block at line 1041. |
| G2 literal values | Low | `normal`, `anywhere`, `end`, `0` and tokens. `flex: 1 1 0` — the bare `0` carries no unit, so the length pattern does not match it. |
| G5 class sweep | **Medium** — part 4 deletes rules | Deleting a rule for a class that still renders fails G5 in the other direction. Check each before removing. |
| G7 `[hidden]` last | Low | All edits are above the terminal rule. |

---

## Contract tests

| Test | Asserts | **Must fail when** |
|---|---|---|
| `TestCardTextWrapsOnNarrowViewports` | The 780px block sets `white-space: normal` on `.card-name` and `.card-path` | The ellipsis stays, so the full name and working directory remain reachable only by a hover a phone does not have |
| `TestTheCardKeepsItsDesktopTruncation` | The **base** `.card-name`/`.card-path` rule still ellipsizes | The base rule is edited instead of overridden, so every desktop card grows to fit its longest path |
| `TestTheMastheadAlignsWithThePage` | The 780px block sets `padding-inline: var(--s4)` on `.masthead-bar` | The header keeps desktop gutters and sits out of line with the content beneath it |
| `TestALongIdentityDoesNotWrapTheBar` | The 780px block sets `flex: 1 1 0` on `.operator` | Basis stays `auto`, so the bar wraps to two rows on a long email and the ellipsis only helps afterwards |
| `TestNoBackgroundSpendsAShadowToken` | No `background`/`background-color` references `var(--glow)` | A shadow list is used as a background — valid-looking, silently dropped, renders nothing. The bug this part fixes, made unrepeatable |
| `TestTheSettingsMenuHasASurface` | `blockFor(.settings-menu)` carries a `background` naming a surface token | The menu is left with no background at all, which is the current state and looks deliberate |
| `TestNoRuleTargetsACaption` | The stylesheet has no `.settings caption` rule | A rule survives for an element the page has not rendered since #103, invisible to the class sweep because it selects an element |

---

## Worked example

```
Operator opens the dashboard on a phone. A session is running in
~/code/some-project-with-a-long-name/internal/service.

Before: the card shows "…/internal/service" and the rest is in a tooltip
        that needs a hover. On a phone it is unreachable. The masthead sits
        8px wider than the cards below it. If the operator's email is long,
        the sticky header is two rows tall.

After:  the path wraps and reads in full. The masthead lines up. One row.

Operator opens settings, on any device including a desktop.

Before: the menu has no background — the rule is there, it is invalid, it
        renders nothing. The current section has no tint for the same reason.
        Only the border and aria-current say where you are.

After:  the menu has a surface, the current section has a lift, and the
        border is still there — because the marker was never allowed to be
        colour alone, which is the reason this was survivable for two bugs
        running.
```
