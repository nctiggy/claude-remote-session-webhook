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

**Everything below describes the desktop.** Two conditions change it, and both are
enumerated rule by rule, with the reason for each, in `design-system.md`:

- `@media (max-width: 780px)` — the one width breakpoint. **Layout**, and only
  layout: what stacks, what wraps, what hides.
- `@media (pointer: coarse)` — a touch pointer at any width. **Ergonomics** —
  size, spacing and input scale — and never layout. A tablet in landscape is a
  touch device at a desktop width; a narrow desktop window is a mouse.

A section here says what a condition does to *that* component and why. It does not
repeat the list: the same enumeration in two places is how the copy nobody has an
obligation to keep current goes stale, and the copy with the obligation attached
is the one in `design-system.md`.

## Canonical inventory

These exist. Use them.

| Component | Path | Use for |
|---|---|---|
| Header | `partials/header.html` | Product identity left, operator identity right, the settings link after it; ambient rain canvas |
| Session card | `partials/session-card.html` | One session in the list, with its action row |
| Status pill | `partials/status-pill.html` | Session state, everywhere it appears |
| Pane viewer | `partials/pane.html` | Live terminal output |
| Create form | `partials/create-form.html` | The one control that starts a session — name, the working-directory picker, the remote-control switch |
| Page token | `partials/page-token.html` | The hidden field **every** mutating form carries |
| Empty state | `partials/empty.html` | Full-strength rain field + one sans-serif explanation |
| Rain canvas | `partials/rain.html` | `<canvas class="rain">` — header and empty state only |

### Specified here, not built

Button, Field, Form and Modal are named by this document and have **no partial on
disk**. They were written as an inventory before there was anything to put in
one: milestone 2's dashboard could only read, so it needed no control, and
milestone 3's four actions each needed a fragment of markup rather than a
component with a call site. Field is covered by Form below.

**Toast has left this list**, and what it left behind is worth keeping. The
clause that stood here said the Toast had "no section and no use" — that this
dashboard answers an action in place, next to the control, rather than in
something that floats away on a timer. That was a design position written down as
a fact, and it had been false since issue #42: `.action-toast` is rendered by
`dashboard.html`, `session.html` and `settings.html`, styled in `crswd.css`, and
filled by `crswd.js` on every action an operator takes with the script running.
Being rendered *and* styled is why no sweep reported it for four milestones — the
drift was between the code and this document, which is the one direction only
this document's own guard can see (#119). It has a section of its own below;
having no partial is now the only thing it still shares with the names above it.

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

### The settings page

`web/templates/settings.html` is the header partial, a menu of sections, and the
panel one section renders into: a table of what this daemon is configured to do,
one row per key, with the layer that supplied each value beside it.

**It acts as well as reads, and it carries all three of the things every
actionable page in this tree carries** — a page token inside every mutating form,
the controls that submit them, and a live region at the foot of the file. Four
routes receive them: `POST /settings/edit` for one configuration key,
`POST /dashboard/update` for the binary, `POST /dashboard/restart` for the
process running it, and `POST /logout` for the browser's own sign-in, on the
daemons that have one. `GET /settings` is still the only verb registered on the path
itself, which is why the header's link to it is a link to a page and nothing more.

That paragraph replaced one asserting the opposite — no page token, no action row,
no live region, and no route for a form to be received by. It was written when the
page was read-only and it outlived the milestone that gave it forms, here and in
the template's own header comment. **The absence of a route is no longer what
bounds this page.** What bounds it now is the action gate those two routes sit
behind, together with `config.Editable`, which answers no for every secret — so
the form that would put a credential in a page cannot be rendered from here at
all. A row it refuses, and every row when no token could be minted, renders the
text it always had: a control certain to be turned away is not offered, which is
the discipline that builds a card with no actions rather than actions that fail.

**"Who may reach it" carries one sentence the other sections do not: which door
is live.** It sits directly under that heading, above the file line, and it says
whether this daemon is behind Cloudflare Access, behind the dashboard password,
or behind a closed door that admits nobody. It is the most consequential fact
about the daemon and it was on no page at all until M12; the keys beneath it say
what the operator configured, and this says which of them the daemon actually
built a door out of, which is not the same claim. It carries **no class**, for
the reason the restart form below carries none: the sentence under it is
`.settings-source`, a name that means "which file these values came from", and a
second element wearing it would tell every reader something untrue. It renders no
value of any kind — every sentence it can say is a constant in Go, chosen from
the door this server was built with and never from the configuration, and
`dashboard_password` stays `present`/`absent` on its row.

**It also carries the one control that ends a sign-in**, directly under that sentence,
because it is the same fact with a verb on it: the heading that answers "who may reach
it" is the one that should offer "and stop being one of them". It is a plain `.button` —
the update owns the view's one primary, and `.button-danger` is for an action that ends
an unsandboxed shell, which this is not — inside a form carrying no class of its own,
the same decision the restart form makes. It is drawn only where the route behind it is
registered, which is a daemon whose door is the dashboard password; behind Cloudflare
Access there is nothing this daemon could clear, and a Sign out that cleared nothing
would be worse than none. That is decided in Go (`doorFacts.SignOut`) and lands on the
same section the sentence does, so the control cannot be composed onto the page and
follow the operator into "Limits". It posts to `/logout` and deliberately not to
`/dashboard/logout`: the script intercepts that prefix and answers in the toast, which
for this one action would leave the operator looking at a dashboard that is dead in
their hands.

Its controls are this document's — `.button`, `.button-primary`, and the
page-token partial. The Updates section's three — Check, Update, Restart — are
that vocabulary and nothing else: Restart is a plain `.button`, because exactly
one primary per view is the rule above and the update owns it here, and because
`.button-danger` is for an action that ends an unsandboxed shell and a restart
ends none. Its form carries no class at all rather than a second spelling of the
update's, which is the same discipline the rest of this page follows. Its own
vocabulary is the names nothing else uses:
`.settings-menu`, `.settings-panel`, `.settings-table`, and the per-row edit form
(`.setting-form`, `.setting-input`, `.setting-save`). A further spelling for a
text entry on this page is the defect this document exists to prevent. The row's
input is labelled by `aria-label` rather than a visible `<label>`, because the row
header beside it already says the key and a second copy is the same word twice to
anybody reading it aloud.

No rain sits behind it: the effect is permitted behind the header and in the empty
state and nowhere else, and a table of values is content being read.

#### The section menu

A bar beside the panel on a desktop; a collapsible disclosure of the same links on a
phone.

| Class | What it is |
|---|---|
| `.settings-menu` | The `<nav>`. A surface and a border, so an edge says "this is the index, that is the content" before any text is read. `position: sticky` above the breakpoint, `static` below it |
| `.settings-menu-list` | The sections. A column beside the panel; a wrapping row below the breakpoint, so no section sits off screen |
| `.settings-sections` | The `<details>` the list sits in. Carries `open` in the markup — one markup shape and no script means it is open everywhere or closed everywhere, and closed-by-default would need CSS to reopen it on a desktop, which `content-visibility` on `::details-content` resists |
| `.settings-sections-summary` | The disclosure's control, naming the section being shown so it answers "where am I" while collapsed. Hidden above the breakpoint, where the sidebar has room for all seven |
| `.settings-menu-link` | One section — a real `<a>` to `/settings?section=…`, carrying `aria-current="page"` when it is the one being shown |
| `.settings-panel` | What the chosen section renders into, and the element that pans when a table is wider than the viewport — deliberately not the wrapper that holds the menu as well |

Rules:
- **Same markup, same real links, same `aria-current`, and no JavaScript either
  way.** A phone gets a different shape, not a different mechanism. The
  alternatives were priced and rejected in `research.md` R8 — a `<select>` in a
  GET form, a `<details>` disclosure, an accordion — because each replaces a list
  of links with a widget, on the one page an operator reaches when something is
  already confusing.
- **The marker is a border as well as a colour, and which edge it sits on follows
  the shape.** A start-edge bar on a column; the bottom edge on a row, where a
  start edge reads as a divider *between* chips rather than a mark *on* one. Never
  hue alone, in either shape.
- **The cost of the row is that it scrolls and nothing scrolls it back.** Seven
  sections do not fit a phone's width, so the current chip can start offscreen,
  and with no script on this page nothing can bring it into view. That is
  [an open question](mobile-open-questions.md) rather than a settled trade, and
  its fallback is the `<details>` disclosure — specced and not built.
- **The panel pans; the grid wrapper holding both of them does not.** `overflow-x`
  lived on that wrapper, so content wider than the viewport dragged the section
  index off the screen along with it, and reaching a Save button cost the operator
  their place in the page. It is unconditional: a desktop is only wide enough that
  nothing overflows to prove it.
- **Below the breakpoint the menu sits above the panel**, and each section's table
  stacks — key, value and source become three lines, with the column headers
  clipped rather than removed so a `th` still carries the name a stacked value has
  lost. Whether a bare provenance word reads as part of the value it now sits
  under is the second [open question](mobile-open-questions.md).

## Header

Every page carries it, which is what makes the design system's second
non-negotiable hold: it is never ambiguous whose credentials are driving sessions
on this host.

| Class | What it is |
|---|---|
| `.masthead` | The band, and the only place besides the empty state a rain canvas may sit |
| `.masthead-bar` | The row inside it: brand, operator, settings link |
| `.brand` | The page's one `<h1>`, holding the wordmark link and the tagline |
| `.operator` | The verified identity layer 1 built for this request |
| `.masthead-link` | A link to another page of this daemon. Today there is exactly one, `/settings` |

Rules:
- **The settings link is in the bar, after the operator, and outside the
  heading.** The wordmark is the route home and it lives inside the page's one
  first-level heading; a second anchor in there would compete for that role, and
  a heading holding two links is a heading that has become a menu.
- **One link is not a navigation bar.** If a third element ever wants a place
  here, that is the moment to reconsider the shape rather than to keep appending.
- **`.operator` carries `margin-inline-start: auto`, and it is load-bearing.**
  The bar is `space-between`; a third child without it puts the identity in the
  centre, which is a different component from the one this document describes.
  Nothing in the markup can see that, so it is written down here.
- **Below the breakpoint the bar's gutters are the page's gutters.** `.shell`
  narrows there and this band did not, so the one element visible on every screen
  was the one thing not lining up with the cards and tables beneath it. It is
  written as the page's gutter rather than as a number of its own, and the test
  reads `.shell` rather than naming a token, so the two cannot drift apart
  quietly.
- **`.operator` grows to fill the bar below the breakpoint**, with `text-align:
  end` keeping identity at the end of it. A flex line wraps on its items'
  *hypothetical* sizes, so a long address broke the bar into two rows before the
  ellipsis the identity already carries could help; a basis of `0` makes that
  hypothetical size zero and the ellipsis does the work instead. The auto margin
  above is what does the same job at a desktop width — two mechanisms, one per
  side of the breakpoint, and neither is redundant where it applies.
- **`.masthead-link` is padded to a thumb on a touch pointer, and the bar does not
  grow**: the padding's height is handed back with a negative margin, so the hit
  area is bigger and the header is the size it always was.
- The link is a link to a page and nothing more. `GET /settings` is the only verb
  registered on that path, so reaching it from every page adds a route to read and
  nothing that acts. What the page it leads to can change, and what bounds that,
  is under "The settings page" above — it is not this link.
- The header renders from the verified operator, never from the request. No
  request-supplied value is in scope in that template, so there is nothing for a
  future edit to reach for by accident.

## Button

There is no Button partial. A control is a `<button class="button" …>` inside
the form it submits, and the variants are the classes the stylesheet defines:

| Class | Use for | Where it is today |
|---|---|---|
| `.button` | Every ordinary action | Compact on the card, Rename on the session page |
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
- **On a touch pointer every button is at least `--tap` tall, and the card's
  action row spreads to match.** Padding alone gives a button roughly half the
  published minimum, and enlarging Destroy and Compact without moving them apart
  would make a mis-tap more likely rather than less. The size is on the component,
  so the next button inherits it rather than needing it — and it follows the
  pointer, never the viewport.
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

One session: name, state pill, identifier, start command, mode, working
directory, age, its last activity, both of its deadlines, and its action row.

```gotemplate
{{/* The dot is one card's view — ID, Name, DisplayState, StartCommand, Mode,
     WorkDir, Age, IdleSince, IdleDeadline, AbsoluteDeadline, PageToken — built
     by cardOf() in internal/httpapi. An empty PageToken renders no action row. */}}
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
- Destroy is `.button-danger` + `confirm=yes`. Compact is a plain `.button`.
- **Rename is not on the card**, and its absence is a rule rather than an
  omission. A fleet is twenty cards, and a text entry on each is twenty fields
  between an operator and what a dashboard is scanned for — spent on the one
  action of the four that changes nothing on the host. It lives on the session's
  own page, inside a `<details>` that is rendered closed: revealed on request,
  operable by keyboard, announced as a disclosure, and opened by the platform
  rather than by script.
- Working directory renders as text, truncated with a `title` attribute — it is
  caller-supplied (see `security.md`).
- **Below the breakpoint the name and the path wrap rather than truncate.** The
  ellipsis is only half a design: the whole value lives in that `title`, and a
  `title` needs a hover a touch device does not have — so on a phone both were
  unreachable rather than shortened, and the card stopped being able to answer
  which session it is. The desktop keeps the ellipsis, because it is what stops
  one long path setting the height of a whole grid row.
- An absent name or working directory renders as a sentence saying the value is
  unknown, in dim sans. Never a placeholder that reads like a real name or a real
  path: a card showing an invented directory tells an operator something false
  about an unsandboxed shell.
- **Both deadlines are on the card, and one of them alone would be a lie.**
  There are two clocks: the idle bound moves with every request and an operator
  may turn it off for one session, and the absolute bound is counted from
  creation, is never renewed, and cannot be turned off at all. A card carrying
  only the idle row would read as "this session never dies" for exactly the
  session whose operator relaxed the bound — the same defect as copy claiming a
  compact happened when the daemon only delivered the request. They are rows of
  the `.card-meta` list, like the mode, so neither has a class of its own.
- **The idle deadline is rendered with the activity it is counted from, in the
  row directly above it.** A deadline alone says when a session dies and nothing
  about what it was judged on, which is the whole of the question that produced
  this row: an operator working in a session all afternoon could not tell from
  the card whether the daemon could see them at all. The value is the *later* of
  the two clocks a session has — a request that drove it, or the host seeing it
  print — so it is the instant the reaper measures from and never the narrower
  `last_activity` the signed API reports. A card naming one and counting from the
  other would be the page disagreeing with itself about one session. It is coarse
  on purpose: the host's reading reaches a record only when a sweep stores it, so
  the value is as fresh as the last sweep and a rendered timestamp would claim a
  precision the daemon does not have. Another `.card-meta` row, like the two
  under it — no class, no token, no stylesheet rule.
- **A session with idle reaping off says there is no idle limit, never a date.**
  `IdleDeadline` answers four hundred lifetimes out for such a session, which is
  the manager's way of spelling "this comparison never fires"; formatted onto a
  card it would read "in 400 days", a fact nothing in the daemon believes. A
  deadline already reached reads as due rather than as time remaining — the
  reaper is entitled to take that session on its next sweep, and a card claiming
  otherwise is the dashboard disagreeing with the thing that acts.

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

## Toast

Where an action's answer lands when the script is running. There is no partial:
each page renders the region itself, as the last element in its body.

```gotemplate
{{/* On dashboard.html, session.html and settings.html. The id is a contract and
     not decoration — see the rules. */}}
<output class="action-toast" id="action-toast" role="status" aria-live="polite" hidden></output>
```

| Class | What it is |
|---|---|
| `.action-toast` | The region. Fixed to the foot of the viewport, `--toast-max` wide, centred by an auto inline margin, and `pointer-events: none` so it can never sit between an operator and the card underneath it |

Rules:
- **It is the enhancement over the redirect, never the thing that makes an action
  work.** Every action form is a real form posting to a real route, and every one
  of those routes answers `303` (`redirectOutcome`). A browser running no script
  follows that redirect and reads the outcome banner on the fleet it lands on
  (`partials/outcome.html`). The script posts the form instead, follows the same
  redirect, and lifts that banner's sentence into this region — one vocabulary
  (`internal/httpapi/outcome.go`) down both paths, so what an operator is told
  does not depend on which one they came down.
- **It floats because nothing navigated**, and that is the whole of when it is
  used instead of an answer in place. The scripted path deliberately does not
  throw the page away, so there is no page arriving for the sentence to be first
  on. It is fixed to the viewport because that is where the eye is after a click,
  and inert to the pointer because an answer must never become an obstacle.
- **`#action-toast` must be in the page's markup, and the id is the half that
  bites.** `crswd.js` looks this element up once, by id, and returns immediately
  when it is absent — and the delegated submit handler is inside that module. So
  a page without it does not lose its toast, it loses the interception: every
  form on that page does an ordinary browser submit and navigates. That is what
  the settings page's update button did, and three fixes went into the navigation
  before the cause turned out to be an element the page did not carry. The region
  is the page's, like every other live region here, and never created by the
  script.
- **One per page.** `getElementById` answers with one, so a second
  `#action-toast` is markup nothing will ever write to.
- **Text, never markup.** The answer is parsed into an inert document with
  `DOMParser` and read with `textContent`. The banner is daemon-authored today,
  so that costs nothing today; it is what keeps this region safe on the day an
  outcome carries a name or a path. Same rule as the pane, for the same reason.
- **One line, and only the banner out of the page it received.** The script reads
  `.outcome`, or the alarming outcome's `.outcome-heading` and `.outcome-body`
  joined — both halves, because reducing "Teardown could not be verified" to its
  body is the flattening that shape exists to prevent. An answer carrying neither
  becomes a fixed sentence saying the host answered without a message. It never
  renders what it received: an entire card landed in here once, when a create
  answered with one (#78).
- **The message outlives the reload it is reporting.** A destroy or a create
  changes the fleet's shape, and the live half reloads the page — which wiped the
  toast a moment after it appeared. The sentence is written to `sessionStorage`
  before it is painted, and repainted on the page the reload lands on. Per tab,
  never sent to the daemon, and a tab that refuses storage still gets the toast,
  just not across the reload.
- **It expires, and the next action clears it.** Six seconds, and a second submit
  cancels the earlier timer so one answer never stands under a later one. The
  cost is worth stating plainly: down the scripted path this region is the only
  place the operator is told anything at all, the alarming outcome included, and
  six seconds later it is gone.
- **Two forms get no sentence here, and they are the two that post to a daemon
  about to stop answering.** The update and the restart replace the settings
  panel with the daemon's own waiting markup instead, because neither is an
  action with an outcome — the process is going down, so what is useful is the
  page staying put, and a sentence that expires six seconds later would leave an
  untouched-looking page in front of a host that is not there. The script
  singles the two out by the address they post to and not by a class:
  `.update-form` is the name of one of them rather than a shape the other may
  wear, and the restart form deliberately carries no class at all. See "The
  settings page".
- **Every value in the rule is a token**, `--toast-max` included, and the copy is
  sans: a sentence a person reads was written by one (see `design-system.md`).
- **It ships `hidden` and is revealed as it is written, which is not the shape
  `.fleet-note` uses** — and the difference is recorded here rather than settled.
  The Accessibility floor gives the rule that note follows: a live region has to
  be in the accessibility tree before its text arrives, which is why it renders
  present and empty. This one is `hidden` until there is an answer, and the
  script writes the text before it clears `hidden`. Whether a region revealed
  that way is announced by a real screen reader is not something any test in this
  tree can answer and it has not been checked on one, so it is an open point
  about a component that works rather than a defect anybody has seen.

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
- **The container scrolls, the page does not.** A fixed `max-block-size` and
  `overflow: auto`, and the horizontal axis contains its overscroll: panning past
  column 80 otherwise chains into the browser's own back gesture and throws the
  reader off the page mid-session. That is unconditional, because a trackpad does
  it too. **The vertical axis is deliberately left alone** — the pane is most of a
  phone's display, so containing it would seal the reader into a box instead of
  letting a flick that began inside it scroll the page.
- **Below the breakpoint the pane wraps, and it is a trade rather than a fix.**
  `white-space: pre-wrap` with `overflow-wrap: anywhere` there; the base rule
  keeps `white-space: pre`. What it costs is alignment: Claude Code's own box
  borders and dividers wrap into a line plus a stub, and any output that depends
  on columns is misrepresented on a phone. What it buys is that reading a
  paragraph stops requiring a horizontal pan per line — the dominant phone task,
  which fails outright without it. Shrinking to fit was priced instead and needs a
  ~6.9px font. **Reverting is two declarations**, and whether the trade reads
  against a real session's chrome is the first
  [open question](mobile-open-questions.md).
- **Pinch-zoom is the escape hatch, so no page in this tree may clamp it.** A
  reader who needs the columns back zooms out to them. `maximum-scale` at any
  value and `user-scalable=no` are both refused by `TestNoPageClampsTheZoom`,
  across every template — a clamp is cheap to add and it removes the one
  mitigation the wrap has.
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

No Form partial. Three forms are shipped, and they are the pattern: the create
form (`partials/create-form.html`, outside every card, because a create names no
session), the rename disclosure on the session's own page
(`templates/session.html`), and the sign-in form (`templates/login.html`, on a
daemon whose door is the dashboard password). A text entry is a
`<div class="field">` holding a `<label class="field-label">` and an
`<input class="field-input">`.

The sign-in page adds **no class at all**, which is what makes it worth naming
here rather than giving it a section: it is `.field`, `.field-label`,
`.field-input` and `.button` in the arrangement above, and the `<form>` itself
carries `.field` for the reason the create form's idle-override block does — that
class is this vocabulary's stack, and an outer one binds a control to what sits
with it. It is also the one page in the tree that composes **no header**, because
it is served before there is an operator to name.

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
- **On a touch pointer a text entry is set at `--fs-input`.** Below that a mobile
  browser zooms the page when a field takes focus, so creating a session, renaming
  one or editing a setting zoomed and panned and the operator pinched back out —
  every time. The threshold is the browser's, not a size anyone chose, which is
  why it is a token of its own rather than a raised body scale. It applies to
  `.field-input` **and** to the settings page's `.setting-input`: covering one
  leaves half the forms zooming while the fix reads as done.

### Working-directory picker

The create form's second field. It is a **native control with a theme over it**,
not a widget: `<input list>` plus a `<datalist>` filter as the operator types,
take a keyboard, announce their options and leave any path typeable in full with
nothing running at all. `crswd.js` adds the one thing no stylesheet can reach — a
datalist popup is drawn by the browser and no CSS in any engine styles it.

| Class | What it is |
|---|---|
| `.combo` | The wrapper, marked `data-combo`, which is what the script finds |
| `.combo-list` | The themed listbox. Rendered empty and `hidden`; filled by the script |
| `.combo-status` | The field's one live region, `role="status"` |

Rules:
- **The native control works first.** With the script absent an operator meets
  the field the daemon rendered, and nothing the script does narrows what may be
  typed or submitted. Enhance; never replace.
- **The ARIA is added by the script, never by the template.** With no script
  there is no combobox to expand and no listbox to control, and markup that
  describes a control which is not there is worse for a screen reader than markup
  describing the plain field that is.
- **`.combo` sets `display: grid` and `position: relative`, and both are
  load-bearing.** An `<input>` is inline-block, so a block wrapper shrinks the
  field; and in the flow the listbox would push the hint and the submit button
  down as an operator typed, moving the control they were aiming at. No test can
  see either, which is why they are here.
- **One live region, and its sentence is the template's.** `.combo-status`
  carries FR-045's "showing n of all" copy in a `data-` attribute and the script
  fills it, on a settle timer — a polite region rewritten on every keystroke
  hands a screen reader a backlog of counts that are wrong by the time they are
  spoken. What the interface says to a person belongs to a template.
- **The options are `<li role="option">` with ids, not buttons.** Focus stays in
  the input for as long as the control is open and `aria-activedescendant` points
  at the active option; a focusable option would take focus out of the field
  being typed in. What makes them operable without a pointer is the keyboard, and
  that is what the accessibility floor is about.
- **The active option's ring is keyed on `[aria-selected="true"]`.**
  `:focus-visible` can never reach an option, so without that rule a keyboard
  operator moves an invisible cursor.
- **Pointer selection binds `mousedown`, never `click`.** Leaving the field
  closes the list, and a blur lands *between* a press and the click that would
  have followed it — a `click` handler here selects an option only when it wins
  that race, which reads as flakiness rather than as a bug. The press is refused
  for every position inside the list, not only on an option, so dragging the
  scroll bar cannot blur the field and shut the list under the pointer.
- **One accept, two triggers.** Enter and the pointer call the same helper, and
  the field's value is written in exactly one place in the whole script. A second
  assignment is a second answer to what the operator chose, on the one field
  where any path has to stay typeable in full.
- **A suggestion is never an authorisation.** The field submits an ordinary
  string, so the handler cannot tell a chosen path from a typed one — both meet
  the same allowlist check, the same refusal, and the same audit record. The list
  is presentation; the approved roots are the control. See `security.md`.
- Suggestions are written with `textContent`, never `innerHTML`: they are
  directory names off a filesystem walk, which is the same rule the pane follows.
- **An option is a tap target on a touch pointer** and is padded accordingly. A
  listbox option is as tappable as a button and gets none of a button's box; left
  alone it is one line of text tall.

### Switch

One `<input type="checkbox">`, themed. Today there are two, both on the create
form: remote control, and the idle override that lets a session outlive the idle
clock. This section said there was exactly one for as long as that was true; the
second arrived with milestone 10, and the rules below say which of them each one
governs rather than reading as though the form still had a single switch.

| Class | What it is |
|---|---|
| `.field-switch` | The row. The one field whose label sits beside its input rather than above it |
| `.switch-input` | The native checkbox. `name="remote_control"` `value="on"`, and `name="idle_timeout"` `value="0"` |
| `.switch-label` | Its label, in the design system's label role |

Rules — the first two are the remote-control switch's:
- **It carries a mode, never a name.** The browser neither sees nor sends a
  command name; which configured command each mode runs is read from
  configuration server-side. A control offering command names is the defect this
  replaced.
- **Two states and no third.** A ticked box posts `on` and an unticked one posts
  nothing at all, so a lost or stripped field yields the *less* privileged mode.
  Anything else is the uniform refusal, including a real configured command name.

And these are the idle override's:
- **It posts the spelling the signed API already has**, `idle_timeout=0`, read by
  that route's own parser — one door offering a session that outlives the
  defaults and the other refusing the same word would be two sets of rules for
  one bound. An unticked box posts nothing, which is the daemon's configured
  default, so a form submitted without touching it starts exactly the session
  this door started before the field existed.
- **The label says "never die" and the hint beside it says what still does.**
  There are two clocks: this switch turns off the idle one, and the absolute
  lifetime is counted from creation, is never renewed, and cannot be disabled at
  all. A label offering "never die" alone would be the interface asserting
  something the daemon does not do — the same defect as copy claiming a session
  was compacted when the daemon only delivered the request. The sentence is a
  `.field-hint` named by `aria-describedby`, exactly as the working-directory
  field's roots are, because `.switch-label` is the label role and prose set in
  it is shouting.
- **Neither switch ships `checked`.** The relaxed bound and the more privileged
  mode are both choices an operator makes, never states they arrive in.
- **The native control is the accessible core; only its presentation changes.**
  `accent-color` paints the platform's own checkbox and there is deliberately no
  `appearance: none` — what an operator reads is the tick, a shape, and the
  colour is reinforcement. Take the appearance away and the only thing telling
  the two states apart is a hue.
- The label follows the box, which is the one place this tree departs from the
  Form sketch above: a checkbox is read as "control, then what it controls", and
  `for` binds them whichever order they sit in.
- The pointer is on the label as well as the box. They are one control, and a
  checkbox is a small target for what starts an unsandboxed shell in the more
  privileged mode.
- **The row is the target on a touch pointer**, sized to `--tap` — the box alone
  is `--s4` square. Sizing the row rather than the box is the same statement the
  bullet above makes, in geometry.

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
  narrating every one of those on a busy host is the same noise. An outcome is
  the one that moved: `partials/outcome.html` carries no live region on either
  shape, because a scriptless operator arrives on a new page and the banner is
  the first thing on it — position doing the work of an announcement. The
  scripted path never navigates, so its sentence has to be announced where it
  lands, and that is the Toast above. This bullet used to end by saying an
  outcome needs no live region at all, because it replaced the control the
  operator had just used; the fragment that did the replacing has not existed
  since the actions began answering `303`.

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
