# Contract: The pane

**Files**: `web/static/crswd.css`, `internal/httpapi/stylesheet_test.go`, `docs/design-system.md`, `docs/components.md`
**Satisfies**: FR-001 … FR-004
**Decomposed**: two tasks — overscroll, then wrap.

---

## The change, literally

**Part one — unconditional.** In the existing `.pane` rule at `crswd.css:891`:

```css
.pane {
  overflow: auto;
  overscroll-behavior-x: contain;   /* ← ADD THIS LINE */
  max-block-size: var(--pane-h);
  ...
  white-space: pre;                 /* ← UNCHANGED. Desktop keeps alignment. */
}
```

**Part two — inside the existing `@media (max-width: 780px)` block at
`crswd.css:1041`**, before its closing brace:

```css
  /* 80 columns through a 44-character window is a horizontal pan per line, and
     most of what a session prints is prose. Wrapping trades the alignment of
     tables and TUI chrome for the ability to read a paragraph at all.
     Safe because captures are right-trimmed — capture-pane carries no -N — so
     blank padding does not wrap. `anywhere` rather than `break-word` because an
     unbroken run is what a real terminal breaks at its column edge. */
  .pane {
    white-space: pre-wrap;
    overflow-wrap: anywhere;
  }
```

**Nothing else.** No font change, no `max-block-size` change, no viewport meta
change.

---

## The three things that must NOT happen

**No `maximum-scale`, ever, on any page.** Pinch-zoom is the operator's escape
hatch for exactly the alignment-dependent output this trade damages. Clamping it
would remove the mitigation at the same moment the problem is introduced.

**No `overscroll-behavior-y`.** The pane is `max-block-size: var(--pane-h)` =
30rem = 480px on a ~660px viewport, so it is most of the screen. Containing the
vertical axis means a flick starting inside the pane stops at the pane's end
instead of scrolling the page — the reader is trapped in a box filling their
display. The horizontal axis has no such cost, because the page does not scroll
horizontally at all.

**No wrap above the breakpoint.** `white-space: pre` stays in the base rule. A
desktop is wide enough for 80 columns and alignment is what a terminal is for.

---

## Guards this task can trip

| Guard | Risk here | How to satisfy |
|---|---|---|
| G1 breakpoint count | **High** — the wrap is width-conditional | Put it **inside** the block at line 1041. A second `@media (max-width: 780px)` fails even at the same width. |
| G2 literal values | Low | `contain`, `pre-wrap`, `anywhere` are keywords. No length, no colour. |
| G7 `[hidden]` last | Low | Both edits are far above the terminal rule. |

---

## Contract tests

| Test | Asserts | **Must fail when** |
|---|---|---|
| `TestThePaneDoesNotChainItsOverscroll` | `blockFor(.pane)` carries `overscroll-behavior-x: contain` | A pan at the scroll edge chains into the browser's navigation gesture and throws the reader off the page mid-session |
| `TestThePaneWrapsOnlyOnNarrowViewports` | The 780px block's `.pane` rule carries `white-space: pre-wrap` **and** `overflow-wrap: anywhere` | Wrapping is added to the base rule, so a desktop loses column alignment to fix a phone |
| `TestThePaneKeepsItsDesktopAlignment` | The **base** `.pane` rule still carries `white-space: pre` | The base declaration is changed rather than overridden, and every desktop reader loses alignment silently |
| `TestNoPageClampsTheZoom` | No template's viewport meta contains `maximum-scale` or `user-scalable=no` | Someone "fixes" the layout by disabling zoom, removing the only mitigation for the trade this contract makes |
| `TestThePaneDoesNotTrapVerticalScrolling` | `blockFor(.pane)` does **not** carry `overscroll-behavior-y` or bare `overscroll-behavior` | The vertical axis is contained too, trapping the reader in a box that fills most of their screen |

`TestNoPageClampsTheZoom` walks `web.Templates` rather than reading one file —
there are four pages and a fifth would otherwise be unguarded.

---

## Worked example

```
Operator opens /session/<id> on a 390px phone. Claude has printed three
paragraphs of prose and a diff.

Before: every line of prose is 80 columns in a 44-character window. Reading
        paragraph one means panning right and back, per line, eleven times.
        Panning at the right edge triggers the browser's back gesture.

After:  the prose wraps and reads top to bottom with vertical scrolling only.
        The diff wraps too and its alignment is wrong — the operator pinch-zooms
        to read it, which still works because no page clamps the scale.

Desktop: unchanged. white-space: pre, 80 columns, aligned.
```

---

## OPEN — carried to the end of the milestone, not resolved by this task

**Does the wrapped pane read acceptably against Claude Code's real interface
chrome?** Claude Code draws box borders, dividers and an input box at full
terminal width. Every one of those wraps into a line plus a stub — roughly one
scruffy wrapped border per screenful.

The precondition (right-trimmed captures) is verified. **The visual judgement is
not, and cannot be by any test in this repository.**

**Fallback if the answer is bad**: delete `white-space: pre-wrap` and
`overflow-wrap: anywhere` from the 780px block. One-line revert, no other change.

This question must still be listed as unanswered in
`docs/mobile-open-questions.md` when the milestone closes. A task that ticks it is
reproducing milestone 4's failure with a different subject.
