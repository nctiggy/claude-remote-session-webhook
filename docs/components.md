# Components

> Loaded when: adding or changing a session card, status pill, pane viewer, button,
> form, or modal.

**The rule: use these. Do not invent a second one.**

A codebase with three button components has no button component. Before creating any
UI primitive, search for an existing one. If it *almost* fits, add a parameter — do
not fork it.

These are Go `html/template` partials in `web/templates/partials/`, rendered
server-side and swapped by htmx. There is no client-side component framework, and
adding one is a decision for a PR, not a convenience.

## Canonical inventory

| Component | Path | Use for |
|---|---|---|
| Button | `partials/button.html` | Every clickable action |
| Field | `partials/field.html` | Every text entry, with its label |
| Form | `partials/form.html` | Every submission + validation |
| Modal | `partials/modal.html` | Every blocking confirmation |
| Header | `partials/header.html` | Product identity left, user identity right |
| Session card | `partials/session-card.html` | One session in the list |
| Status pill | `partials/status-pill.html` | Session state, everywhere it appears |
| Pane viewer | `partials/pane.html` | Live terminal output |
| Toast | `partials/toast.html` | Transient feedback |

## Button

Variants are a parameter, never a new component.

```gotemplate
{{ template "button" (dict
     "Variant" "primary"        /* primary | secondary | danger | ghost */
     "Size"    "md"             /* sm | md */
     "Label"   "Create session"
     "HxPost"  "/sessions"
     "HxConfirm" "") }}
```

Rules:
- Exactly one `primary` button per view. If you need two, one is not primary.
- In-flight state must disable the button — use `hx-disabled-elt="this"`. A
  double-submit spawns two sessions, which is a real bug here, not a UX quirk.
- Destructive actions use `Variant: "danger"` **and** a confirmation modal.

## Status pill

The single source of truth for how session state is rendered. Colour comes from the
state map in `design-system.md`; the label is always present.

```gotemplate
{{ template "status-pill" .Session.State }}   {{/* running | idle | needs-auth | dead */}}
```

Never render state as a bare coloured dot. Never hand-write the colour at a call site.

## Session card

One session: name, state pill, working directory, age, and its action row.

```gotemplate
{{ template "session-card" .Session }}
```

Rules:
- The card is the **only** place a session's summary is composed. The list view and
  the detail header both use it.
- The action row uses Button, never raw `<button>`.
- Destroy is `danger` + confirmation. Compact and rename are `secondary`.
- Working directory renders as text, truncated with a `title` attribute — it is
  caller-supplied (see `security.md`).

## Pane viewer

Live terminal output, streamed over SSE.

```gotemplate
<pre class="pane" hx-ext="sse" sse-connect="/sessions/{{ .ID }}/stream"
     sse-swap="line" hx-swap="beforeend">{{ .PaneText }}</pre>
```

Rules — these are security rules as much as design rules:
- **Text nodes only.** Never `safeHTML`, never `template.HTML`, never `innerHTML`.
- ANSI is stripped server-side before it reaches the template.
- The container scrolls, the page does not. Fixed height, `overflow-y: auto`.
- Auto-scroll to the bottom only when the user is already at the bottom — yanking
  the viewport while someone is reading scrollback is hostile.
- No animation on new lines.

## Form

```gotemplate
{{ template "form" (dict
     "Action" "/sessions" "Method" "post"
     "Fields" .Fields "Error" .Error) }}
```

Rules:
- Every input has a `<label>`. A placeholder is not a label.
- Show the error next to the field, not only in a toast.
- Validation is server-side and authoritative; client hints are convenience only.
- Disable submit while in flight.

## Modal

```gotemplate
{{ template "modal" (dict
     "Title" "Destroy session?"
     "Body"  "This kills the tmux session and any work in it. Not reversible."
     "Confirm" (dict "Variant" "danger" "Label" "Destroy" "HxDelete" "/sessions/abc")) }}
```

Rules:
- Focus moves into the modal on open and returns to the trigger on close.
- `Esc` closes. Clicking the backdrop closes. Both, always.
- Never nest a modal inside a modal.
- Uses `<dialog>`, so the focus trap comes from the platform rather than JS.

## Accessibility floor

Non-negotiable, applies to everything above:
- Keyboard reachable and operable. Tab order follows visual order.
- Visible focus ring. Never `outline: none` without a replacement.
- Interactive elements are `<button>`/`<a>`, not a `<div>` with a handler.
- Icon-only controls carry an accessible name.
- Live regions (`aria-live="polite"`) announce state changes and toasts. The pane
  itself is **not** a live region — announcing every terminal line is unusable.
