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
| Header | `partials/header.html` | Product identity left, operator identity right; ambient rain canvas |
| Session card | `partials/session-card.html` | One session in the list |
| Status pill | `partials/status-pill.html` | Session state, everywhere it appears |
| Pane viewer | `partials/pane.html` | Live terminal output |
| Toast | `partials/toast.html` | Transient feedback |
| Empty state | `partials/empty.html` | Full-strength rain field + one sans-serif explanation |
| Rain canvas | `partials/rain.html` | `<canvas class="rain">` — header and empty state only |

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

Never render state as a bare coloured dot, and never hand-write the colour at a call
site — it comes from the state token map in `design-system.md`. `needs-auth` is amber
and `dead` is red on purpose: legibility at a glance beats theme purity, and a dead
session that reads as green is a bug.

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
{{/* The initial screen. Server-rendered, escaped by html/template. */}}
<pre class="pane" id="pane-{{ .ID }}" data-stream="/sessions/{{ .ID }}/stream">{{ .PaneText }}</pre>
```

```js
// The live half. Each event carries the WHOLE current screen as a JSON string,
// and replaces what was there — a Claude session is a full-screen program that
// repaints in place, not a log that appends. Reconstructing a transcript by
// diffing redraws produces torn lines from every cursor move and spinner.
//
// textContent, never innerHTML: the payload is untrusted bytes from the host,
// and this is the project's only XSS surface. JSON.parse yields a string, and
// the only thing done with a string is assign it — there is no path from here
// to markup, which is what "closed by construction" means.
new EventSource(pane.dataset.stream).onmessage = (e) => {
  pane.textContent = JSON.parse(e.data);
};
```

> **Do not use htmx's `sse-swap` / `hx-swap="beforeend"` for pane output.** That
> inserts the payload **as markup**, which is precisely what `security.md`
> forbids: *"never `hx-swap` a raw pane payload into the DOM as markup — stream it
> into a `<pre>` as text."* This snippet used to show exactly that, and it was
> wrong: a session printing `<img src=x onerror=...>` would have executed it. htmx
> is still the right tool for the rest of the dashboard; pane output is the one
> place it must not do the swapping.

Rules — these are security rules as much as design rules:
- **Text nodes only.** Never `safeHTML`, never `template.HTML`, never `innerHTML`,
  and never an htmx swap that treats the payload as markup.
- ANSI is stripped server-side before it reaches the template.
- The container scrolls, the page does not. Fixed height, `overflow-y: auto`.
- **The pane shows the live screen, not scrollback.** A repainting screen has no
  "bottom" to follow, so an update must never move the viewport for the reader.
  History is what `tmux attach` is for, and the interface should say so rather
  than imply a transcript it does not keep.
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

## Empty state

The one surface where the rain runs at full strength — there is no data competing
with it, so it fills the void instead of leaving a shrug.

```gotemplate
{{ template "empty" (dict
     "Title" "No sessions running"
     "Body"  "Nothing is executing on this host right now. Start one to open a Claude session in a tmux window."
     "Action" (dict "Label" "Start a session" "HxPost" "/sessions")) }}
```

Rules:
- Rain at `.5` opacity behind, message burned through with a radial vignette so the
  text never fights the glyphs.
- Body copy is **sans**, not mono — a human wrote it (see `design-system.md`).
- The canvas is `aria-hidden`; it carries no information.
- Removed entirely under `prefers-reduced-motion: reduce`, leaving the message.

## Rain canvas

```gotemplate
<canvas class="rain" aria-hidden="true"></canvas>
```

Permitted in **two** places only: behind the header (`.16`) and in the empty state
(`.5`). Never behind reading content — not a pane, a card grid, a form, or a table.
A third home is reasonable when a login screen exists; anywhere else needs a
justification in the PR. See `design-system.md` for the full rule set.
