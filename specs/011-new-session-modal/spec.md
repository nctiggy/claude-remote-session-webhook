# Feature Specification: New Session Modal

**Feature Branch**: `011-new-session-modal`

**Created**: 2026-08-17

**Status**: Draft

**Input**: User description: "Move the create-session form off the dashboard into a modal. The dashboard gets a single 'New session' button; clicking it opens a `<dialog>` containing the existing create form. The form's copy is cut down hard — it is currently far too verbose for what is a routine action. Nothing about what a create does changes: same route, same page token, same validation, same allowlist."

## Why this exists

The dashboard's job is to say what is running on this host. The largest thing on
it is a form for starting something that is not.

The create form is four controls, four explanatory notes, a list of allowed
roots, a command preview and a submit — permanently on the page whether or not
the operator came to create anything. On a phone it is roughly a screen and a
half below a fleet that is usually one or two cards.

No part of it was a mistake. Each arrived on its own milestone with its own
reason: the working-directory picker and its roots list (007), the remote-control
switch (005), the never-expire switch and its note (009), the conversation picker
and its note (009), the command preview (009). The defect is not in any of them.
The defect is that all of them are on screen all of the time, for an action an
operator takes occasionally, on the page they open to answer a different
question.

And it is about to get bigger. `docs/harnesses.md` describes a harness picker
that lands on this same form, and the operator has since chosen the set it will
offer — Claude Code, Antigravity CLI, Codex and Copilot CLI. That control is
coming to a form that has no room left. This feature is the room.

So: the dashboard keeps one button, and the form moves behind it.

## What this is not

- **Not a change to what a create does.** Same route, same page token, same
  `ValidateName`, same `ResolveWorkDir`, same allowlist, same audit record, same
  outcome vocabulary. A create refused today is refused identically after this.
- **Not the harness picker.** That is its own feature, and this one only makes
  space for it. No harness knowledge enters this milestone.
- **Not a redesign of the form's controls.** US2 cuts the form's *prose*. The
  fields themselves — name, working directory, remote control, never-expire,
  conversation, preview — are carried across unchanged in number and in meaning.
- **Not a second create path.** There is one form, and moving it does not leave a
  copy behind.

## User Scenarios & Testing *(mandatory)*

### User Story 1 - The dashboard leads with the fleet (Priority: P1)

An operator opens the dashboard to see what is running. They scroll past sessions
and nothing else. When they do want a new session, one button opens a dialog
holding the form they already know; they fill it in and start a session exactly
as before.

**Why this priority**: This is the feature. Everything else here refines it.

**Independent Test**: Load the dashboard. Confirm no create field is visible and
one "New session" button is. Activate it, fill in a name and a working directory,
submit, and confirm a session starts and appears in the fleet — the same session,
by the same route, with the same audit record as before this change.

**Acceptance Scenarios**:

1. **Given** a dashboard with sessions running, **When** the operator loads it,
   **Then** no create field, hint, preview or heading is visible, and a single
   "New session" button is.
2. **Given** the dashboard, **When** the operator activates "New session",
   **Then** a dialog opens containing every control the create form carried
   before, and focus moves into it.
3. **Given** the open dialog, **When** the operator submits a valid name and a
   working directory inside the allowed roots, **Then** a session starts, the
   dialog closes, and the fleet shows the new card.
4. **Given** a dashboard rendered without a page token, **When** it loads,
   **Then** it offers neither the button nor the dialog — the rule the form
   already follows, applied to its trigger.
5. **Given** an operator with no sessions at all, **When** the dashboard loads,
   **Then** the empty state and the "New session" button are both reachable, and
   the button is not nested inside the rain field.

---

### User Story 2 - The form says less (Priority: P2)

An operator opening the dialog reads what they need to act and nothing that
explains the daemon to them. The allowed roots stay, because the refusal
deliberately will not name them. The rest is cut to a line or removed.

**Why this priority**: Independently valuable and independently testable — the
copy could be cut on the inline form tomorrow. It is second because a shorter
form on the dashboard is still a form on the dashboard.

**Independent Test**: Open the dialog and count the explanatory sentences. No
control carries more than one line of prose, and the roots list is still present
in full.

**Acceptance Scenarios**:

1. **Given** the open dialog, **When** the operator reads it, **Then** no control
   carries more than one line of explanatory text.
2. **Given** a daemon with two or more allowed roots, **When** the dialog opens,
   **Then** every configured root is listed — a hint missing a root reads as a
   refusal the operator cannot explain.
3. **Given** a daemon whose lifetime ceiling stands, **When** the dialog opens,
   **Then** it still says that sessions end at a limit and where that limit
   moves — the sentence survives the cut, shortened.

---

### User Story 3 - A refused create keeps the operator where they are (Priority: P1)

An operator who typed a name the daemon will not take sees why, next to what they
typed, without the dialog closing and without retyping.

**Why this priority**: It is the one place moving the form could quietly make
things worse. On the scripted path a create is posted by the live half and never
navigates, so the toast at the foot of the page carries the answer — and a
`<dialog>` opened as a modal sits in the top layer, which would leave that
sentence announced but invisible behind the backdrop.

*Raised from P2 to P1 during planning.* It was written as a refinement, and it is
not one: User Story 1 shipped alone puts a refusal behind a backdrop, which is a
regression against what the dashboard does today. The MVP is these two together.

**Independent Test**: With scripting on, submit a name the daemon refuses.
Confirm the dialog is still open, the typed values are still in the fields, and
the reason is legible inside the dialog rather than behind it.

**Acceptance Scenarios**:

1. **Given** the open dialog with scripting on, **When** the operator submits a
   name `ValidateName` refuses, **Then** the dialog stays open, the fields keep
   what was typed, and the refusal reads inside the dialog.
2. **Given** the open dialog with scripting on, **When** the operator submits a
   working directory outside the allowed roots, **Then** the same holds, with the
   uniform refusal unchanged — it still does not say whether the path exists.
3. **Given** the open dialog, **When** a create succeeds, **Then** the dialog
   closes and the success reads where every other action's success already reads.
4. **Given** scripting is off, **When** a create is refused, **Then** the browser
   lands on the fleet with the outcome banner — the behaviour that ships today,
   unchanged.

---

### User Story 4 - The dialog dismisses the way a dialog does (Priority: P3)

`Esc` closes it. A click on the backdrop closes it. A Cancel control closes it.
Focus returns to the button that opened it.

**Why this priority**: Two of the three come from the platform for free once the
dialog is modal, so this story is mostly the third. It is last because a dialog
that only closes by `Esc` and Cancel is already usable.

**Independent Test**: Open the dialog, press `Esc`, and confirm focus is back on
"New session". Repeat with a backdrop click on a browser that supports light
dismiss.

**Acceptance Scenarios**:

1. **Given** the open dialog, **When** the operator presses `Esc`, **Then** it
   closes and focus returns to the trigger.
2. **Given** the open dialog, **When** the operator activates Cancel, **Then** it
   closes, focus returns to the trigger, and nothing was created.
3. **Given** a browser that supports light dismiss, **When** the operator clicks
   the backdrop, **Then** the dialog closes.
4. **Given** a browser that does not, **When** the operator clicks the backdrop,
   **Then** nothing happens and `Esc` and Cancel still close it.

---

### Edge Cases

- **A browser with neither declarative invocation nor scripting.** The dialog
  never opens and the create form is unreachable. See Assumptions — this is a
  browser predating March 2022 with JavaScript disabled, and it is accepted
  rather than solved.
- **The fleet changes shape while the dialog is open.** The live half adds or
  removes a card underneath. The dialog is in the top layer and unaffected; a
  half-typed name survives, exactly as it survives a card swap today.
- **The daemon restarts while the dialog is open.** The stalled-fleet note
  appears underneath the backdrop, where it cannot be read. The submission that
  follows fails on its own and reports in the dialog (US3).
- **The operator opens the dialog, types, closes it, reopens it.** Whether the
  fields still hold what was typed is a decision, not an accident — see
  Assumptions.
- **The submit-once guard fires inside the dialog.** The button disables and the
  in-progress note reveals; both must be visible inside the dialog rather than at
  the foot of the page.
- **A daemon with no conversation history and a standing lifetime ceiling.** The
  dialog renders the two controls that apply and neither of the two that do not,
  which is the existing rule about absent values — a dialog is not permission to
  render an offer with nothing behind it.

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: The dashboard MUST render exactly one control that leads to a
  create, and MUST NOT render any create field, hint, preview or heading outside
  the dialog.
- **FR-002**: The dialog MUST carry every control the create form carries today —
  name, working directory with its suggestions and roots, remote control,
  never-expire where the ceiling is removed, conversation where there is history,
  and the command preview — with each control's existing conditions on rendering
  unchanged.
- **FR-003**: A create MUST reach the same route with the same fields, the same
  page token, and the same server-side validation. No client-side check may
  become the reason a session does or does not start.
- **FR-004**: The trigger and the dialog MUST both be absent when the render
  minted no page token, so a page that cannot authorise a create offers none.
- **FR-005**: The dialog MUST open without JavaScript on a browser supporting
  declarative invocation, and MUST open via the page's existing script on a
  browser that does not.
- **FR-006**: Opening the dialog MUST move focus into it; closing it MUST return
  focus to the trigger.
- **FR-007**: `Esc` MUST close the dialog. A Cancel control MUST close it. Neither
  MUST create anything.
- **FR-008**: Backdrop dismissal MUST be offered where the browser supports it and
  MUST NOT be the only way to close the dialog.
- **FR-009**: On the scripted path, a refused create MUST leave the dialog open
  with the submitted values intact, and MUST show the refusal inside the dialog.
- **FR-010**: On the scripted path, a successful create MUST close the dialog and
  report success where the other actions already report it.
- **FR-011**: With scripting off, a create MUST behave exactly as it does today —
  a form post, a 303, and the outcome banner on the fleet.
- **FR-012**: The in-progress note and the disabled submit MUST be visible inside
  the dialog while a create is in flight.
- **FR-013**: No control in the dialog may carry more than one line of
  explanatory text, with the allowed-roots list exempt because it is a list rather
  than prose.
- **FR-014**: Every allowed root MUST still be listed.
- **FR-015**: `docs/components.md`'s Modal section MUST describe what shipped
  rather than what was imagined: its illustrative `dict` call site MUST go, and
  its rules — focus in and back, `Esc`, backdrop, never nested — MUST survive.
  The `.modal*` class family MUST join the sweep that holds a documented family
  and the stylesheet together, so a rule the document never names fails a test
  rather than a review.

  *Amended 2026-08-17, during planning.* This requirement first asked Modal to
  move into the canonical inventory. Research R3 established that it cannot: the
  template set is parsed with no function map, so the `dict` the inventory's
  entry would be called with does not exist, and the inventory's own test demands
  a partial per entry that would have no honest content. Modal takes Button's and
  Field's path instead — a class vocabulary the shipped templates use, documented
  and swept, ready to lift into a partial unchanged the day one is warranted.
- **FR-016**: No colour, size or font may be expressed outside the design tokens,
  including the backdrop and the elevation the design system permits a modal.
- **FR-017**: The dialog MUST NOT place the rain canvas behind it. Rain has two
  permitted homes and reading content is not one of them.

### Key Entities

None. This feature adds no daemon concept, no stored field, and no route.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: An operator scrolling the dashboard top to bottom passes session
  information and one button, and nothing else about creating a session.
- **SC-002**: Starting a session takes the same committed inputs as today plus
  exactly one — opening the dialog. No field is added, removed or reordered.
- **SC-003**: Every path that could start a session before this change still
  starts one, including with JavaScript disabled.
- **SC-004**: A refused create on the scripted path costs zero retyping and zero
  navigation: the operator reads the reason and corrects the field in place.
- **SC-005**: No control in the dialog carries more than one line of prose.
- **SC-006**: A keyboard-only operator can open the dialog, complete every field,
  submit, and be returned to a known focus position, without reaching for a
  pointer.
- **SC-007**: The build, test and lint commands in `AGENTS.md` pass, including the
  stylesheet sweep that reads a class no template renders as dead.

## Assumptions

- **The operator's browsers are current.** Declarative invocation of a `<dialog>`
  reached Baseline in 2026; the modal `<dialog>` element itself has been Baseline
  since March 2022 and sits near 97% global support. The page's existing script
  covers the gap between those two dates. A browser older than the second *and*
  running with scripting off loses access to the create form, and that is accepted
  rather than solved — the alternative is keeping a second, inline copy of the
  form, which is a second create path.
- **Backdrop dismissal is an enhancement, not a requirement of the platform.**
  Light dismiss is not yet Baseline. It is offered where present and its absence
  costs nothing, because `Esc` and Cancel are always there.
- **The template set is parsed with no function map.** There is no `dict`, so the
  illustrative `{{ template "modal" (dict …) }}` call site in `docs/components.md`
  is not buildable as written. The Modal that ships takes the view it is given, and
  FR-015 is what keeps the document describing the real thing.
- **The dialog keeps its fields between closing and reopening.** It is the same
  form element in the same document, so this is what happens by default; it is
  recorded here because it is a behaviour an operator will notice, and losing a
  half-typed working directory to a stray `Esc` would be worse than keeping it.
- **The button sits in the page flow where the form is today** — after the fleet,
  outside both the grid and the empty state — rather than in the header. The header
  is product identity, operator identity and settings; a create is neither. The
  operator has said the main page is the right home for it.
- **The trigger's label is "New session"**, matching the operator's own words for
  it, and the dialog keeps the form's existing "Start a session" as its title.
- **`docs/harnesses.md` is not touched by this feature.** The harness picker is
  the next spec and this one only clears space for it.
