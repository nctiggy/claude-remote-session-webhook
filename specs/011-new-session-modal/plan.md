# Implementation Plan: New Session Modal

**Branch**: `feat/011-new-session-modal` | **Date**: 2026-08-17 | **Spec**: [spec.md](spec.md)

**Input**: Feature specification from `/specs/011-new-session-modal/spec.md`

## Summary

The create form moves off the dashboard and behind one button. It becomes a modal
`<dialog>` opened declaratively — `command="show-modal"` — so it still works with
the script switched off, with a feature-detected `showModal()` fallback for
browsers between March 2022 and Baseline 2026. Its four multi-sentence hints are
cut to one line each or removed. A refusal is written into a live region inside
the dialog, because a modal `<dialog>` makes the toast at the foot of the page
inert. `Modal` becomes a documented class vocabulary rather than a partial, for
the same reason Button and Field are.

No route, no field, no validator, no daemon concept changes. One field is added
to an existing view — the outcome's own code, so the script can tell a success
from a refusal without reading prose.

## Technical Context

**Language/Version**: Go 1.24 (`go.mod`), plus hand-written CSS and one embedded
ES2020 script. No npm, no framework, no htmx.

**Primary Dependencies**: standard library only. The platform features this
feature leans on are the Invoker Commands API, `HTMLDialogElement.showModal()`
and the `closedby` attribute. **The Popover API is deliberately not among them** —
see the amendment below.

**Storage**: N/A — this feature stores nothing and adds no field to a record.

**Testing**: `go test ./...`; the markup sweeps in
`internal/httpapi/partials_test.go` and the stylesheet sweeps in
`internal/httpapi/stylesheet_test.go`; `go test -tags quickstart ./cmd/crswd` for
the acceptance pass.

**Target Platform**: the operator's browser — desktop and a phone — served from a
loopback listener behind Cloudflare Access or a dashboard password.

**Project Type**: server-rendered web dashboard over a Go daemon.

**Performance Goals**: none new. The dialog adds no request and no round trip; a
create costs exactly what it costs today.

**Constraints**: the CSP in `docs/security.md` is sent unmodified — no inline
`<script>`, no inline `<style>`, no external origin. Tokens only, no literal
colour or size in a template or a rule. Every control keyboard-operable with a
visible focus ring.

**Scale/Scope**: one operator, one host. Six files of source, three of tests, two
of documentation.

## Constitution Check

*GATE: passed before Phase 0, re-checked after Phase 1 design.*

| Principle | Applies? | Verdict |
|---|---|---|
| **I. Security is a gate** | Yes, weakly | No route, no handler, no validator, no field is touched. Every refusal stays server-side and uniform; the working-directory field still carries no `pattern`, so nothing client-side becomes the reason a session does or does not start. The one new client behaviour reads the daemon's own banner as **text**, never as markup — the rule the toast already follows. **PASS** |
| **II. Unknowns surfaced** | Yes | Spec carries zero `NEEDS CLARIFICATION`; five decisions are in Assumptions with reasons, and seven more are resolved in `research.md` with alternatives named. FR-015 was amended in the open rather than quietly satisfied. **PASS** |
| **III. Every change is verifiable** | Yes | Every FR below maps to a task with a test that fails without it. The three sweeps that could rot — dead class, dead element, undocumented family — are extended rather than worked around. **PASS** |
| **IV. Smallest correct change** | Yes | This is the principle that decided R3: no function map, no `dict`, no Modal partial, no new route. The create form's *controls* are carried across untouched; only its wrapper and its prose change. **PASS** |
| **V. Standards enforced, not documented** | Yes | `documentedComponentClass` gains `modal`, so the document's side of this change is a test rather than a promise. **PASS** |
| **VI. Blast radius bounded** | Yes, weakly | Nothing widens. `allowed_roots`, the session cap, the absolute lifetime, verified teardown and the loopback bind are all untouched, and the never-expire switch keeps both of the two decisions it requires. **PASS** |
| **VII. Design system is binding** | Yes, centrally | Tokens only. The one thing this feature needs that the design system reserves is elevation — `docs/design-system.md` already names "a modal overlay" as the single exception to "never shadows", so this is the exception being spent for the first time and on the case it was written for. Rain stays off the dialog (FR-017). **PASS** |

**No entries in Complexity Tracking.** Nothing here required a justified
violation.

## Spec amendments made during planning

- **FR-015** was rewritten. It asked Modal to move into the canonical inventory;
  R3 established the inventory cannot hold it without a function map the tree does
  not have. It now asks for the documentation and the sweep, which is the part
  that was actually load-bearing. The amendment and its reason are recorded in
  `spec.md` beside the requirement.
- **User Story 3 was raised from P2 to P1.** It was written as a refinement and it
  is not one: US1 shipped alone puts a refusal somewhere an operator cannot read
  it, which is a regression against today. The MVP is US1 and US3 together.

## Plan amendments made during implementation

- **R2's mechanism was replaced, and the replacement was measured rather than
  reasoned.** The plan said the toast would be promoted into the top layer with
  `popover="manual"`. Driven against the daemon's own rendered page in Chrome 149,
  a control inside the promoted toast could not take focus while the dialog was
  open, where the identical control was focusable with the dialog closed and
  focusable inside the dialog while it was open: **a popover above a modal dialog
  is inert**, which loses the accessibility tree — the half that mattered — and
  the popover's user-agent rules moved the region to the middle of the viewport
  and shrank it to its text on the way. The retreat `research.md` had already
  written down became the design. `contracts/outcome-in-top-layer.md` is
  superseded by `contracts/outcome-where-the-operator-is.md`, which keeps the
  failed attempt and its measurements rather than deleting them.
- **`outcomeView` gained a `Code` field**, rendered as `data-outcome` on both
  banner shapes, so the script tells a success from a refusal using `outcome.go`'s
  own closed vocabulary instead of matching prose. It is set only after the map
  lookup succeeds, so no caller string reaches the document and FR-022 is
  unchanged.
- **A defect was found in this plan's own markup sketch before it shipped.**
  `.modal { display: flex }` — the obvious way to write the column below — beats
  the user-agent's `dialog:not([open]) { display: none }`, because the cascade
  sorts by origin before specificity. It would have left the dialog standing open
  on the fleet on every load: the exact page this milestone exists to stop
  drawing. The rule is on `.modal[open]`, and `TestAClosedDialogStaysClosed` is
  the pin, because every other sweep in the tree passes on the broken version.

## Project Structure

### Documentation (this feature)

```text
specs/011-new-session-modal/
├── plan.md              # This file
├── spec.md
├── research.md          # Phase 0 — R1…R7
├── data-model.md        # Phase 1 — deliberately empty, and says why
├── quickstart.md        # Phase 1 — how to prove it
├── contracts/
│   ├── new-session-modal.md      # the trigger, the dialog, open and close
│   ├── outcome-where-the-operator-is.md  # where a refusal renders while the dialog is up
│   └── modal-vocabulary.md       # the class family and the document's obligation
├── checklists/
│   └── requirements.md
└── tasks.md             # /speckit-tasks output — not created here
```

### Source Code (repository root)

```text
web/
├── templates/
│   ├── dashboard.html                 # call site unchanged; commentary corrected
│   └── partials/
│       └── create-form.html           # trigger + <dialog>; prose cut
└── static/
    ├── crswd.css                      # .modal* family, ::backdrop; .create folded
    └── crswd.js                       # invoker fallback; toast as popover; close on success

internal/httpapi/
├── partials_test.go                   # new pins for the trigger, the dialog, the cut
└── stylesheet_test.go                 # documentedComponentClass gains `modal`

docs/
├── components.md                      # Modal section rewritten; Create form entry updated
└── design-system.md                   # the modal-overlay elevation exception, spent
```

**Structure Decision**: No new file. The trigger and the dialog live in
`partials/create-form.html` because that partial *is* the create control and the
dashboard already composes it by that name — a new `create-dialog.html` beside it
would be a second thing to render and a second place for a create to be composed,
which the spec's "not a second create path" rules out. The dashboard's call site
(`{{ template "create-form" .Create }}`) does not change at all; only what that
template draws does.

## Phase 1 design notes

### The markup shape

```gotemplate
{{ if .PageToken }}<section class="create">
<button class="button button-primary" type="button" command="show-modal" commandfor="create-dialog">New session</button>
<dialog class="modal" id="create-dialog" closedby="any" aria-labelledby="create-title">
  <div class="modal-head">
    <h2 class="modal-title" id="create-title">Start a session</h2>
    <button class="modal-close" type="button" command="close" commandfor="create-dialog" aria-label="Close">…</button>
  </div>
  <p class="modal-outcome" role="status" aria-live="polite"></p>
  <div class="modal-body">
    <form class="create-form" method="post" action="/dashboard/sessions" …>
      … every existing field, unchanged …
    </form>
    <p class="create-note" id="create-busy" hidden>…</p>
  </div>
</dialog>
</section>{{ end }}
```

The scrolling region is a wrapper rather than the form itself, which the sketch
first had the other way round. `TestEveryActionIsUsableWithoutScript` reads the
literal `<form class="create-form"` to decide that the page a scriptless action
lands on is a fleet an operator can act from again, and a second class on that
attribute would have turned a layout decision into a change to what that test can
see.

Three properties are load-bearing and each is pinned by a task:

1. **`{{ if .PageToken }}` still wraps everything.** A render that minted no token
   offers neither the trigger nor the dialog — FR-004, and the rule the form
   already followed.
2. **The trigger is `type="button"`.** It sits inside no form, but the attribute
   is written because a `<button>` defaults to `submit` and the day this markup
   moves the default is the bug.
3. **The close control carries an accessible name.** It is the accessibility
   floor's icon-only rule, and it is why `aria-label` appears on a control whose
   visible content is a glyph.

### The script's three additions

All three are enhancements over markup that already works:

- **The invoker fallback**, guarded on `'commandForElement' in
  HTMLButtonElement.prototype`. Unguarded it would double-fire on a current
  browser.
- **`showPopover()` on the toast**, so the outcome clears the backdrop. Guarded on
  the method existing; a browser without it gets the toast where it has always
  been.
- **Closing the dialog on a create that succeeded**, and leaving it open on one
  that did not. The submit handler already has the answer in hand; the
  distinction is the outcome code the banner carried.

### Risks the tasks must check rather than assume

- **`TestNoBackgroundSpendsAShadowToken`** may read a `box-shadow` on `.modal` as
  the thing it forbids. The design system permits exactly one — a modal overlay —
  so either the test already excludes this case or it gains the exception the
  document already grants. Read it before writing the rule.
- **`TestTheStylesheetStylesNoElementTheMarkupNeverRenders`** requires a template
  to open a `<dialog>` before a `dialog` rule may exist. Markup task lands before
  the CSS task.
- **The `:has()`-free path.** No rule may depend on a selector the sweeps do not
  expect; keep the dialog's own state in the element, not in an ancestor.
- **Top-layer stacking is insertion order.** The toast must be shown *after* the
  dialog opened to sit above it. That is true on the create path by construction,
  and it should be asserted rather than believed.

## Post-design Constitution re-check

Re-read after the Phase 1 shape above. **No principle moves.** The design adds no
route, no field, no secret, no shell string, no widening of any bound in Principle
VI, and it spends exactly one design-system exception on the case that exception
was written for. Principle IV is the one worth restating: this plan touches six
files and creates none.
