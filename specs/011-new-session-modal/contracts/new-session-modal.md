# Contract: the New session trigger and its dialog

Covers FR-001, FR-002, FR-004, FR-005, FR-006, FR-007, FR-008, FR-012, FR-017.

## What the dashboard shows

One control:

```
[ New session ]
```

Nothing else about creating a session appears anywhere on the page — no field, no
hint, no roots list, no command preview, no heading. The rest lives in a dialog
that is closed until the operator opens it.

The control sits where the form sat: after the fleet, outside both the card grid
and the empty state. Outside the grid because a create names no session; outside
the empty state because rain never goes behind reading content, and a control in
the rain field is that violation.

## How it opens

**Declaratively, with no script:**

```html
<button type="button" command="show-modal" commandfor="create-dialog">New session</button>
```

The Invoker Commands API reached Baseline in 2026. On such a browser the dialog is
opened by the platform: focus moves into it, the page behind becomes inert, and
the focus trap is the platform's rather than a script's.

**By script, only where the platform cannot:**

```js
if (!('commandForElement' in HTMLButtonElement.prototype)) { /* bind showModal() */ }
```

The guard is the contract, not a detail. Bound unconditionally, a current browser
would open the dialog twice — once for the attribute and once for the listener.

## How it closes

| Way | Source | Required |
|---|---|---|
| `Esc` | The platform, because the dialog is modal | Always |
| The Cancel control | `command="close" commandfor="create-dialog"` | Always |
| A click on the backdrop | `closedby="any"` | Where the browser has it |

Focus returns to the trigger on every one of them. That is the platform's own
behaviour for a dialog opened from a button, and it is asserted rather than
assumed because it is the half of FR-006 a keyboard operator notices.

`closedby` is Limited availability as of mid-2026 — shipped in Chrome, Edge and
Firefox, on WebKit's Interop 2026 list. Where it is ignored, the attribute costs
nothing and the two rows above it still close the dialog. **Backdrop dismissal is
never the only way out.**

## What the dialog contains

Every control the create form carried, unchanged in number, order, name and
meaning:

| Control | Condition on rendering |
|---|---|
| Name | Always |
| Working directory, with its datalist and combo enhancement | Always; `list` and `<datalist>` only where there are suggestions |
| The allowed-roots hint | Where the daemon has roots, which is always |
| Remote control switch | Always |
| Never-expire switch and its note | Only where the daemon's own ceiling is removed |
| The standing-ceiling line | Only where it is not |
| Continue a conversation | Only where there is history |
| Command preview | Only where a command line resolved |

**A dialog is not permission to render an offer with nothing behind it.** Every
condition above is the one that control already carries, and moving the form does
not relax one of them.

## While a create is in flight

The submit disables itself and the in-progress note reveals — the existing
submit-once guard, unchanged. Both must be **inside the dialog**: a disabled
button in the top layer beside a note in the normal flow is a pressed control with
no explanation.

## What may not be here

- **No rain.** The dialog is reading content and rain has two permitted homes.
- **No second create form.** Moving the form leaves no copy behind, and the
  dashboard composes it by exactly one name.
- **No client-side validation that can refuse.** The hints stay hints; the working
  directory still carries no `pattern`; `ValidateName` and `ResolveWorkDir` remain
  the only things that can turn a create away.
- **No `<dialog>` nested in a `<dialog>`.** `docs/components.md`'s Modal rules.

## Degradation

| Condition | Behaviour |
|---|---|
| No page token minted | Neither the trigger nor the dialog renders |
| Baseline browser, script off | Opens declaratively; `Esc` and Cancel close it; a create is a form post and a 303 |
| Pre-2026 browser, script on | Opens by `showModal()`; everything else identical |
| Pre-March-2022 browser, script off | The dialog does not open. Accepted — see spec Assumptions |
| No `closedby` support | Backdrop clicks do nothing; `Esc` and Cancel are unaffected |
