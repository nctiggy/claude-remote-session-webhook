# Components

> Loaded when: adding or changing a session card, status pill, pane viewer, action
> control, button, form, or modal.

**The rule: use these. Do not invent a second one.**

A codebase with three button components has no button component. Before creating any
UI primitive, search for an existing one. If it *almost* fits, add a parameter — do
not fork it.

These are Go `html/template` partials in `web/templates/partials/`, rendered
server-side. **There is no htmx in this tree, and no client-side component
framework.** The page loads one script — `web/static/crswd.js` — and it draws
rain, reads panes and follows the fleet stream; every control that changes
something is a plain form post that works with the script switched off. Adding a
library is a decision for a PR, not a convenience.

## Canonical inventory

These exist. Use them.

| Component | Path | Use for |
|---|---|---|
| Header | `partials/header.html` | Product identity left, operator identity right; ambient rain canvas |
| Session card | `partials/session-card.html` | One session in the list, with its action row |
| Status pill | `partials/status-pill.html` | Session state, everywhere it appears |
| Pane viewer | `partials/pane.html` | Live terminal output |
| Create form | `partials/create-form.html` | The one control that starts a session |
| Page token | `partials/page-token.html` | The hidden field **every** mutating form carries |
| Empty state | `partials/empty.html` | Full-strength rain field + one sans-serif explanation |
| Rain canvas | `partials/rain.html` | `<canvas class="rain">` — header and empty state only |

### Specified here, not built

Button, Field, Form, Modal and Toast are named by this document and have **no
partial on disk**. They were written as an inventory before there was anything to
put in one: milestone 2's dashboard could only read, so it needed no control, and
milestone 3's four actions each needed a fragment of markup rather than a
component with a call site. Field is covered by Form below; Toast has no section
and no use — this dashboard answers an action in place, next to the control, with
`.card-outcome`, rather than in something that floats away on a timer.

That is not permission to invent a second vocabulary. The class names in those
sections are the ones the shipped templates already use — `.button`,
`.button-danger`, `.button-primary`, `.field`, `.field-label`, `.field-input` —
so the day one of them earns a partial, the markup lifts into it unchanged. What
is forbidden is a *third* spelling: a control styled with anything else is a
second button component, which is the defect this document exists to prevent.

Their `{{ template "x" (dict …) }}` call sites below are illustrative. **This
template set is parsed with no function map**, so there is no `dict` to call — a
partial takes the dot, and a value it needs is a field on the view the handler
built. A new partial follows that, not the sketch.

## Button

There is no Button partial. A control is a `<button class="button" …>` inside
the form it submits, and the variants are the classes the stylesheet defines:

| Class | Use for | Where it is today |
|---|---|---|
| `.button` | Every ordinary action | Rename, Compact |
| `.button .button-primary` | The one action a view exists for | Start session |
| `.button .button-danger` | An action that ends an unsandboxed shell | Destroy |

Rules:
- Exactly one `primary` button per view. If you need two, one is not primary.
- **A control that changes something lives inside its own `<form method="post">`,
  and that form carries the page token.** See Action controls below.
- In-flight state must disable the button where a double submit is a real event —
  `data-submit-once` on the form, handled by `crswd.js`. It is on the create form
  and deliberately nowhere else: a second create is a second unsandboxed shell,
  while a second destroy finds no record, a second rename is the same end state,
  and a second compact is a second delivery the operator asked for.
- Destructive actions use `.button-danger` **and** a confirming step. That step is
  a hidden field the page sends deliberately (`confirm=yes`), **not a modal** —
  there is no Modal partial, and a `<dialog>` would need script for an action that
  currently needs none. Colour is reinforcement and never the signal: the label
  reads "Destroy" and the outcome is a sentence.

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

One session: name, state pill, identifier, working directory, age, and its
action row.

```gotemplate
{{/* The dot is one card's view — ID, Name, DisplayState, WorkDir, Age,
     PageToken — built by cardOf() in internal/httpapi. An empty PageToken
     renders no action row. */}}
{{ template "session-card" . }}
```

Rules:
- The card is the **only** place a session's summary is composed. The list view and
  the detail header both use it, and the fleet stream re-fetches this same card.
- The card carries **exactly one `<a>`**, and it covers the whole readable
  half — name, pill, identifier, start command, meta list. Not the heading alone:
  a card reads as clickable end to end, so a target of a few words is the same
  defect as the identifier-only link it replaced.
- **No control is nested inside that link**: a link wrapping a submit control is
  two things occupying one target, and one of these controls ends an unsandboxed
  shell. The readable half and the action row are **separate elements**, and the
  rule between them is that split made visible rather than the split itself — a
  border says nothing to a screen reader and a high-contrast theme may drop it.
- Destroy is `.button-danger` + `confirm=yes`. Rename and Compact are plain
  `.button`.
- Working directory renders as text, truncated with a `title` attribute — it is
  caller-supplied (see `security.md`).
- An absent name or working directory renders as a sentence saying the value is
  unknown, in dim sans. Never a placeholder that reads like a real name or a real
  path: a card showing an invented directory tells an operator something false
  about an unsandboxed shell.

## Action controls

The four things the dashboard can change, and the shape all four share. Read
`docs/auth-and-sessions.md` before altering any of it — the markup here is half of
a security control, not decoration.

```gotemplate
{{ with .PageToken }}
<form method="post" action="/dashboard/sessions/{{ $.ID }}/destroy">
  {{ template "page-token" . }}
  <input type="hidden" name="confirm" value="yes">
  <button class="button button-danger" type="submit"
          aria-describedby="card-id-{{ $.ID }}">Destroy</button>
</form>
{{ end }}
```

| Action | Route | Answers with |
|---|---|---|
| Destroy | `POST /dashboard/sessions/{id}/destroy` | `200` and a sentence; `409` when teardown could not be verified, and the record is **retained** |
| Create | `POST /dashboard/sessions` | `200` and the new card; `429` at the cap or the rate limit |
| Rename | `POST /dashboard/sessions/{id}/rename` | `200` and the renamed card |
| Compact | `POST /dashboard/sessions/{id}/compact` | `202` — **delivered**, never "compacted" |

All four also answer `400` for input they refuse, `404` for a session that is not
this operator's to act on, and `500` when the host would not do it — each one
`.card-outcome` sentence and never a status alone.

Rules — these are security rules as much as design rules:
- **Every mutating form includes `{{ template "page-token" . }}`.** It renders a
  hidden `crsw_page_token` field and nothing else. Never a URL, never a cookie,
  never a `data-` attribute. A form without it is refused by the gate, uniformly
  and with no way for the page to tell why.
- **The whole row renders from the token** (`{{ with .PageToken }}`). A card built
  without one offers no controls rather than controls that are certain to fail —
  the same discipline that makes an absent name a sentence instead of a
  placeholder. This is what fills the action-row parameter earlier versions of this
  document described as present-but-empty.
- **The outcome is text.** A route answers with `<p class="card-outcome">…</p>`,
  which replaces what it acted on. One class for all of them, success and failure
  alike: an outcome is told apart by what it says, never by a shade. A control that
  failed must say so — a card that quietly comes back unchanged is the silent
  revert this dashboard forbids.
- **Compact reports delivery, not compaction.** The daemon hands `/compact` to the
  session and cannot see what the assistant does with it. Copy that claims
  otherwise asserts something this daemon never observed.
- Each button is `aria-describedby` its session's identifier. A fleet of adopted
  sessions has no names, so without it a screen reader hears a column of controls
  announcing the same word, each acting on a different shell.

## Pane viewer

Live terminal output, streamed over SSE.

```gotemplate
{{/* The initial screen. Server-rendered, escaped by html/template. */}}
<pre class="pane" id="pane-{{ .ID }}" tabindex="0" data-stream="/sessions/{{ .ID }}/stream" data-ended="pane-ended-{{ .ID }}">{{ .Text }}</pre>
{{/* Revealed when the daemon says the session ended. The copy lives here, not
     in the script: what the interface says to a person belongs to a template. */}}
<p class="pane-note" id="pane-ended-{{ .ID }}" hidden>This session has ended. The screen above is the last one it printed.</p>
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
const live = new EventSource(pane.dataset.stream);
live.onmessage = (e) => {
  const screen = JSON.parse(e.data);

  // The pane is its own scroll container, and replacing its content empties it
  // for an instant — long enough for the browser to clamp the offset against a
  // box with nothing in it. So the reader's place is read here and put back
  // below, rather than followed to a "bottom" a repainting screen never has.
  const top = pane.scrollTop;
  const left = pane.scrollLeft;

  pane.textContent = screen;

  pane.scrollTop = top;
  pane.scrollLeft = left;
};

// The session ending is the daemon's one NAMED event, so a session cannot
// announce its own by printing the bytes of one — every screen arrives unnamed.
// The close is not politeness: without it EventSource reconnects for as long as
// the tab lives, and each reconnection after the end is answered with the
// uniform 404, which is the dashboard scanning its own daemon.
live.addEventListener('end', () => {
  const note = document.getElementById(pane.dataset.ended);
  if (note) {
    note.hidden = false;
  }
  live.close();
});
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
- **A stream that stops says why.** When the watched session ends, the note beside
  the pane is revealed and the source is closed. Updates that simply cease look
  exactly like a session sitting quietly at a prompt, and the last screen stays on
  the page rather than being replaced by the sentence about it.
- No animation on new lines.

## Form

No Form partial. Two forms are shipped, and they are the pattern: the create form
(`partials/create-form.html`, outside every card, because a create names no
session) and the rename on the card. A text entry is a `<div class="field">`
holding a `<label class="field-label">` and an `<input class="field-input">`.

```gotemplate
<div class="field">
  <label class="field-label" for="create-name">Name</label>
  <input class="field-input" id="create-name" type="text" name="name"
         required maxlength="64" pattern="[-a-zA-Z0-9]+"
         autocomplete="off" spellcheck="false">
</div>
```

Rules:
- Every input has a `<label>`. A placeholder is not a label.
- **`id` and `for` are qualified by the session's identifier** on anything a card
  renders. A fleet of cards each carrying `id="rename-name"` is a page where every
  label names the first one.
- Show the error where the operator is looking: a refused action replaces what it
  acted on with `.card-outcome`, next to the control, not only somewhere transient.
- **Validation is server-side and authoritative.** Client hints are convenience,
  and they are pinned to the daemon's own rules by a test — a hint that drifted
  would refuse a name this daemon would have accepted, in a native bubble this
  daemon never wrote. There is deliberately **no hint on the working directory**:
  the roots are configuration, and a pattern spelling them puts a map of the host
  in the markup.
- Disable submit while in flight where a double submit is a real event — see
  Button.

## Modal

**Not built, and nothing needs one.** The confirming step for the one destructive
action is a hidden field the page sends deliberately (`confirm=yes`), which costs
the same deliberate act and needs no script; a `<dialog>` needs script to open,
and this tree's one script draws rain and reads panes.

Kept as a specification so that the first blocking confirmation that genuinely
needs one is built once, to these rules, rather than improvised:

```gotemplate
{{/* Illustrative — see "Specified here, not built" above. */}}
{{ template "modal" (dict
     "Title" "Destroy session?"
     "Body"  "This kills the tmux session and any work in it. Not reversible."
     "Confirm" (dict "Variant" "danger" "Label" "Destroy")) }}
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
- Live regions (`aria-live="polite"`, or `role="status"`, which is that role by
  another name) announce **the state changes nobody is looking at**: a live
  connection that stopped, a background thing that failed. The severed-fleet note
  carries `role="status"` for exactly that reason — it appears when the operator is
  not acting.
- **A fleet that changes shape says so, in one sentence, once.** A session
  appearing or vanishing is announced by the note beside the severed-fleet one;
  the grid itself is still not a live region. The distinction is the boundary
  below rather than an exception to it: what is announced is that the page now
  holds a different set of cards, which is the fact an operator who cannot see it
  has no other way to learn. The sentences are the page's
  (`data-fleet-appeared`, `data-fleet-vanished`), and the region is rendered
  present and empty rather than hidden — a live region has to be in the
  accessibility tree before its text arrives for the announcement to happen at
  all.

  This paragraph is new, and what it replaced said the opposite: that a fleet
  changing was noise and nothing about the grid was announced. That rule was
  written when a shape change reloaded the whole page, which re-announced it as a
  side effect of throwing it away. The reload is gone (issue #51), so the
  announcement that came with it has to be made on purpose or not at all, and a
  page that silently rearranges is worse for a non-sighted operator than one that
  reloads.
- Nothing else is announced, and the boundary is deliberate. The pane itself is
  **not** a live region — announcing every terminal line is unusable — and neither
  is the card grid: a card that changed state or name is replaced in place, and
  narrating every one of those on a busy host is the same noise. An outcome the
  operator just caused needs no live region either; it replaces the control they
  used, where focus already is.

## Empty state

The one surface where the rain runs at full strength — there is no data competing
with it, so it fills the void instead of leaving a shrug.

```gotemplate
{{/* Title, Body and Hidden. The dot is an emptyView; the Action field exists and
     this dashboard passes none. */}}
{{ template "empty" . }}
```

Rules:
- **`Hidden` is a page saying it composed this state without showing it.** The
  fleet renders both of its shapes — the summary with its grid, and this — and
  hides whichever does not apply, so a session appearing or vanishing is the live
  half revealing markup the daemon authored rather than composing an empty state
  of its own (issue #51). A second composition is a second empty state. Every
  other call site leaves it false, which is the state showing.
- **The `Action` parameter stays absent here, and the create form sits beside the
  empty state rather than inside it.** That is this document's own rule applied to
  itself: the empty state is the one surface where the rain runs at full strength,
  and rain never goes behind reading content — "not a pane, a card grid, a form, or
  a table". A form nested in the rain field is that violation. The not-found page
  passes no action either, for a different reason: an operator who has just been
  told a page does not exist is owed the fact that asking touched nothing, not a
  navigation affordance this door does not serve.
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
