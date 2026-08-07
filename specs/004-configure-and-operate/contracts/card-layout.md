# Contract: The card's readable and actionable halves

**Files**: `web/templates/partials/session-card.html`, `web/static/crswd.css`
**Tests**: `internal/httpapi/partials_test.go`, `internal/httpapi/stylesheet_test.go`
**Satisfies**: FR-046 … FR-051, SC-010
**Carry forward**: `claude/issue-issue-60-20260806-0406` (543 lines), reconciled
with the toast and anchor work that landed on `main` afterwards.

---

## The split

```
┌─────────────────────────────────┐
│  READABLE  ← one anchor covers  │  name, state, mode, workdir, age
│              this whole block   │
├─────────────────────────────────┤  ← visible boundary
│  ACTIONABLE                     │  destroy, compact, mode toggle
└─────────────────────────────────┘
```

| Rule | FR |
|---|---|
| Exactly one link per card | FR-046 |
| The link covers the **whole readable block**, not the name alone | FR-046, SC-010 |
| No control sits inside the link | FR-047 |
| A visible boundary separates the halves, and is **not the only cue** | FR-048 |
| Rename does not appear on the fleet | FR-049 |
| Rename appears on the session's own page, revealed on request | FR-050 |
| Selecting text inside the link does not navigate | FR-051 |

## Why the boundary is not only a line

FR-048's second clause is an accessibility requirement, not a style preference. A
rule that separates the halves visually says nothing to a screen reader and
nothing at all under a high-contrast theme that drops borders. The halves are
therefore **structurally** distinct — separate elements with distinct roles — and
the line is the visual expression of a separation that already exists in the
markup.

## Selection must not navigate

FR-051 is the one behaviour that needs script, and it needs very little: a click
that ends a text selection is not a click on a link. Without script the card
still works — this is a papercut fix, not a functional dependency.

## Contract tests

| Test | Asserts | **Must fail when** |
|---|---|---|
| `TestCardHasExactlyOneAnchor` | Exactly one `<a>` per rendered card | A second link is added — the recurring regression here |
| `TestAnchorCoversReadableBlock` | The anchor's subtree contains name, state, mode and workdir | The anchor wraps the name alone (SC-010) |
| `TestNoControlInsideAnchor` | No `<button>`, `<form>` or `<input>` inside the anchor | A control is nested, producing invalid HTML (FR-047) |
| `TestBoundaryIsNotColourAlone` | The halves are distinct elements, not one block split by a border | The split is presentational only (FR-048) |
| `TestRenameAbsentFromFleet` | The dashboard renders no rename control | Rename returns to the card (FR-049) |
| `TestRenameOnSessionPageIsDisclosure` | Present, and not expanded by default | It becomes a resident field (FR-050) |
| `TestNothingAnimatesUnderReducedMotion` | Under `prefers-reduced-motion`, no transition or animation applies | A transition is added to the disclosure (FR-059) |
| `TestFocusRingVisibleOnEveryControl` | Each control has a visible focus indicator | An `outline: none` is introduced (FR-059) |
| `TestModeShownTextually` | Mode is words, not colour alone | Mode becomes a coloured dot (FR-059) |
