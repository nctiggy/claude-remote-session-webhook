# Contract: Settings on a phone

**Files**: `web/static/crswd.css`, `internal/httpapi/stylesheet_test.go`, `docs/components.md`, `web/templates/settings.html`
**Satisfies**: FR-005 … FR-010
**Decomposed**: four tasks, **in this order**: overflow → menu → rows → comment.

**This is the page the operator reported.** *"Seeing settings is tricky right now
as well"* — and the audit confirms it is the worst surface in the product.

---

## Task order is not arbitrary

The overflow move must land **before** the stacked rows. Stacking makes each row
taller; if the pan is still on the wrapper at that point, the intermediate state
pans a taller menu alongside a taller table, which is worse than either end state.

---

## Part 1 — Move the overflow off the wrapper

**The fault**: `overflow-x: auto` sits on `.settings`, which is the **grid wrapper
holding both the menu and the panel**. Content wider than the viewport therefore
pans the section menu along with the table.

**At `crswd.css:1131`, delete the whole rule:**

```css
.settings {
  overflow-x: auto;      /* ← DELETE. Wrong element. */
}
```

**At `crswd.css:1358`, add one line to `.settings-panel`:**

```css
.settings-panel {
  min-inline-size: 0;
  padding-inline-start: var(--s2);
  overflow-x: auto;      /* ← ADD. Wide content pans itself, not the menu. */
}
```

Unconditional — as right on a desktop as on a phone. It went unnoticed only
because a desktop is wide enough that nothing overflows.

**Careful**: `.settings` has a second rule inside the 780px block
(`grid-template-columns: 1fr`). Delete only the `overflow-x` rule at 1131; leave
the media-block rule alone.

---

## Part 2 — The section menu becomes a scrolling row

**The fault**: at ≤780px the grid collapses to one column, so the seven-entry menu
stacks **above** the panel and consumes roughly 300px — the whole first screen.
Each section is a fresh GET landing at the top, so choosing a section means
scrolling past the entire menu again to read the result.

**Inside the existing `@media (max-width: 780px)` block**, add:

```css
  /* The menu becomes one scrolling row so the panel starts on the first screen.
     Same markup, same real links, same aria-current — a phone gets a different
     shape, not a different mechanism.
     Sticky is dropped rather than kept: in a one-column grid each item's area is
     its own height, so it has no travel room and does nothing. Stating that is
     better than relying on the geometry to keep being true. */
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

  /* A start-edge bar reads wrongly on a chip in a row; the marker moves to the
     bottom edge. Still a border as well as a colour — never hue alone. */
  .settings-menu-link[aria-current="page"] {
    border-inline-start: none;
    border-block-end: var(--edge-width) solid var(--edge-bright);
  }
```

**The menu stays real links.** No `<select>`, no `<details>`, no JavaScript. FR-008
and SC-009 are about the mechanism, and the mechanism does not change.

---

## Part 3 — Setting rows stack

**The fault**: a three-column table (key / value / provenance) cannot fit ~358px.
On an editable row, `.setting-form` carries a 128px-min input plus a Save button,
pushing the row past 430px — **so editing means typing inside a horizontally
panning table**.

**Inside the same 780px block**, add:

```css
  /* Three columns do not fit a phone, and the editable row is the reason it
     matters: the input and its Save button need the row's whole width or the
     operator types inside a pan.
     The headers are hidden accessibly rather than removed — inset(50%) collapses
     the element while leaving it in the accessibility tree, and spends a
     percentage, which is not a length the value sweep tokenises. */
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

**Do not** use the conventional visually-hidden recipe — it spends `inline-size:
1px` and `block-size: 1px`, and **G2 fails a literal length**. `inset(50%)` is a
percentage; `%` is absent from the sweep's unit list. Verified.

**Confirm the class name before writing this.** The selector must match what
`settings.html` actually renders. If the table carries no `.settings-table` class,
use the element selector under the section class that does exist — and do **not**
add a class to the template, because a new class pulls in G5.

---

## Part 4 — The comment that contradicts its own file

`web/templates/settings.html`'s header comment states the page has:

> "There is no form here, and there is not going to be one… this page carries no
> page token, no action row and no live region"

The page below it renders `.setting-form` rows, the update form, a page token, and
an `<output id="action-toast">` live region. **All four.**

Rewrite the paragraph to describe the page that exists. Comment-only; every sweep
strips comments, so the suite is inert to this and the verification is the new
text existing.

**Why this is a defect and not tidying**: in a pipeline whose executor reads
comments as contract, a false comment costs an iteration. This one asserts the
opposite of the truth about a security-adjacent property (whether the page carries
a token), which is the worst kind to leave lying.

---

## Guards these tasks can trip

| Guard | Risk | How to satisfy |
|---|---|---|
| G1 breakpoint count | **High** — three of four parts are width-conditional | Everything goes inside the block at line 1041. |
| G2 literal values | **High** — the visually-hidden recipe is the trap | `inset(50%)`. Never `1px`. |
| G5 class sweep | None | No new class. If a part seems to need one, it has left the plan. |
| G7 `[hidden]` last | **Medium** — `display: grid` and `position: static` are involved | All rules go before the terminal `[hidden]`. |

---

## Contract tests

| Test | Asserts | **Must fail when** |
|---|---|---|
| `TestWideSettingsPanTheirOwnPanel` | `blockFor(.settings-panel)` carries `overflow-x`, **and** the `.settings` wrapper rule does not | The property is added to the panel without being removed from the wrapper, so the menu still pans with the table and nothing observable changed |
| `TestTheSettingsMenuIsARowOnNarrowViewports` | The 780px block sets `grid-auto-flow: column` on `.settings-menu-list` and `position: static` on `.settings-menu` | The menu keeps stacking, so the panel still starts below a full screen of links |
| `TestTheCurrentSectionIsNotColourAlone` | The `aria-current` rule carries a `border-*` declaration at **both** widths | The narrow override drops the border, leaving the current section marked by colour alone |
| `TestSettingRowsStackOnNarrowViewports` | The 780px block sets `display: grid` on the table's `tr` and hides `thead` with `clip-path` | Rows stay three-column, so editing a setting means typing inside a horizontally panning table |
| `TestTheHeadersAreHiddenAccessiblyNotRemoved` | The `thead` rule uses `clip-path`, and **not** `display: none` | The headers are removed from the accessibility tree entirely, which is the failure the recipe exists to avoid |
| `TestTheSettingsMenuIsStillLinks` | `settings.html` renders `<a href` per section — no `<select>`, no `<form>` around the menu | The menu is rebuilt as a control that needs JavaScript, breaking SC-009 |
| `TestTheSettingsCommentDescribesThePage` | `settings.html`'s header comment does not claim the page has no form / no token / no live region | The comment keeps asserting the opposite of the file, and the next fresh-context iteration believes it |

---

## Worked example

```
Operator opens /settings?section=Limits on a 390px phone.

Before: ~300px of menu fills the first screen. Scroll past it to reach the
        panel. The table is 430px wide in a 358px viewport, so panning to
        reach the Save button drags the menu sideways too. Tapping into the
        input zooms the page (see touch.md).

After:  one scrolling row of section chips, then the panel immediately below.
        Each setting is a stacked block: key, then value, then provenance.
        The input and Save have the full width. Nothing pans.

Desktop: unchanged — two columns, three-column table, sticky menu.
```

---

## OPEN — two questions, carried to the end, not resolved by these tasks

**Does a bare provenance word read as part of the value once rows are stacked?**
"default" or "file" sitting beneath a value, with its column header now hidden,
could momentarily read as the value itself.
**Fallback**: render an explicit label in the row — a template change, specced
here, not built.

**Does the scrolling menu disorient when the current chip starts offscreen?** With
seven entries the row scrolls, and with no JavaScript nothing can scroll the
current entry into view. An operator deep in the list may land with the current
section offscreen.
**Fallback**: replace the scrolling row with a `<details>` disclosure — a template
change, specced here, not built.

Both must still be listed as unanswered in `docs/mobile-open-questions.md` when the
milestone closes.
