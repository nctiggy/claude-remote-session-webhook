# Components

> Loaded when: adding or changing a button, form, modal, nav, or table.

**The rule: use these. Do not invent a second one.**

A codebase with three button components has no button component. Before creating
any UI primitive, search for an existing one. If it *almost* fits, extend it with
a prop — do not fork it.

## Canonical inventory

| Component | Path | Use for |
|---|---|---|
| Button | `<FILL IN>` | Every clickable action |
| Input / Field | `<FILL IN>` | Every text entry |
| Form | `<FILL IN>` | Every submission + validation |
| Modal / Dialog | `<FILL IN>` | Every blocking interaction |
| Nav | `<FILL IN>` | Primary navigation |
| Table | `<FILL IN>` | Tabular data |
| Toast / Alert | `<FILL IN>` | Transient feedback |

## Button

Variants are a prop, never a new component.

```
<FILL IN: canonical example>
// e.g.
// <Button variant="primary|secondary|danger|ghost" size="sm|md|lg"
//         disabled={bool} loading={bool} onClick={fn}>Label</Button>
```

Rules:
- Exactly one `primary` button per view. If you need two, one is not primary.
- `loading` must disable the button. A double-submit is a bug, not a UX quirk.
- Destructive actions use `variant="danger"` **and** a confirmation step.

## Form

```
<FILL IN: canonical example including validation + error display>
```

Rules:
- Validate on blur, re-validate on submit. Never only on submit.
- Every input has a `<label>`. Placeholder is not a label.
- Show the error next to the field, not only in a toast.
- Disable submit while in flight.

## Modal

```
<FILL IN: canonical example>
```

Rules:
- Focus moves into the modal on open and returns to the trigger on close.
- `Esc` closes. Clicking the backdrop closes. Both, always.
- Never nest a modal inside a modal.

## Accessibility floor

Non-negotiable, applies to everything above:
- Keyboard reachable and operable. Tab order follows visual order.
- Visible focus ring. Never `outline: none` without a replacement.
- Interactive elements are `<button>`/`<a>`, not a `<div>` with `onClick`.
- Icon-only controls carry an accessible name.
