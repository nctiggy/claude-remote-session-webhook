# Tasks: New Session Modal

**Branch**: `feat/011-new-session-modal` | **Spec**: [spec.md](spec.md) | **Plan**: [plan.md](plan.md)

**Definition of done for every task**: `go build ./... && go vet ./... && go test ./...`
passes, plus `golangci-lint run`. A task that cannot leave the tree green on its
own says so and names the task it lands with.

**Two pairs must land in the same commit**, because neither half is green alone:

- **T002 + T003** — markup and stylesheet. `TestTheStylesheetAndTheMarkupNameTheSameThings`
  sweeps both directions: a class in a template with no rule fails, and a rule with
  no class fails. `.modal*` arriving in one without the other is red either way.
- **T012 + T013** — the dialog's own live region and the script that writes into
  it. A region nothing writes to is exactly as silent as no region; a script
  writing to a region no template renders falls back to a toast that is inert.

---

## Phase 1 — Setup

- [x] T001 Read the three sweeps that constrain this feature and record what each
      one will demand, in `internal/httpapi/stylesheet_test.go` and
      `internal/httpapi/partials_test.go`: the class sweep
      (`TestTheStylesheetAndTheMarkupNameTheSameThings`), the element sweep
      (`TestTheStylesheetStylesNoElementTheMarkupNeverRenders`) and the document
      sweep (`TestTheComponentsDocumentNamesThePickerTheSwitchTheHeaderAndTheToast`).
      **Already checked and recorded, so this task is a re-read rather than a
      discovery**: `TestNoBackgroundSpendsAShadowToken` forbids only `var(--glow)`
      inside a `background` declaration, so a `box-shadow` on `.modal` is not what
      it is about — the design system's "single exception is a modal overlay" can
      be spent without touching that test.

---

## Phase 2 — Foundational: the Modal exists

Blocking. Every story below draws inside what this phase builds.

- [x] T002 `web/templates/partials/create-form.html`: wrap the existing form in
      the dialog shape from `contracts/new-session-modal.md` — `<section class="create">`
      keeps the `{{ if .PageToken }}` gate and now holds a `New session` trigger and
      a `<dialog class="modal" id="create-dialog">`. `.create-heading` becomes
      `.modal-title` with the same words, referenced by `aria-labelledby`. Add
      `.modal-head`, `.modal-close` (icon-only, so it carries `aria-label`) and
      `.modal-body` on the form. **Every field, every hint and every condition is
      carried across untouched** — this task moves markup and renames one class and
      does nothing else. Lands with T003.
- [x] T003 `web/static/crswd.css`: the `.modal*` family per
      `contracts/modal-vocabulary.md` — surface, border, padding, a width bound,
      `.modal::backdrop`, and `.modal-body` scrolling inside the dialog rather than
      pushing it past the viewport. Tokens only; the elevation exception
      `docs/design-system.md` grants a modal overlay is spent here and nowhere else.
      Delete the `.create-heading` rule, which now styles a class no template
      renders. Lands with T002.
- [x] T004 `docs/components.md`: rewrite the Modal section to describe what
      shipped. Remove the `{{ template "modal" (dict …) }}` call site — there is no
      `dict` in a template set parsed with no function map — and say so, with the
      Button/Field precedent it now follows. Keep all four rules (focus in and back,
      `Esc`, backdrop, never nested). Update the Create form row in the canonical
      inventory to say it is a trigger and a dialog. Move Modal out of "Specified
      here, not built" without moving it into the inventory, and say why.
- [x] T005 `internal/httpapi/stylesheet_test.go`: widen `documentedComponentClass`
      to `\.(combo|switch|masthead|action-toast|modal)[\w-]*`, and extend the
      comment above it to say the modal family joined for the reason the toast did.
      Green only after T004, which is the point of the sweep.

---

## Phase 3 — User Story 1 (P1): the dashboard leads with the fleet

**Goal**: one button on the dashboard, the form behind it, and a create that works
identically — including with the script switched off.

**Independent test**: load the dashboard, confirm no create field is visible and
one trigger is; open it, submit a valid create, and confirm the session starts by
the same route with the same audit record.

- [x] T006 [US1] `web/templates/partials/create-form.html`: make the dialog open
      and close declaratively — `command="show-modal" commandfor="create-dialog"`
      on the trigger, `command="close" commandfor="create-dialog"` on a Cancel
      control and on `.modal-close`, and `closedby="any"` on the dialog. The
      trigger is `type="button"`: it sits inside no form today, and the day the
      markup moves, a `<button>`'s `submit` default is the bug.
- [x] T007 [US1] `web/static/crswd.js`: the fallback that opens the dialog on a
      browser predating the Invoker Commands API, guarded on
      `'commandForElement' in HTMLButtonElement.prototype`. **The guard is the
      contract** — bound unconditionally it fires alongside the declarative
      invocation on a current browser and opens the dialog twice.
- [x] T008 [P] [US1] `internal/httpapi/partials_test.go`: pin FR-001 and FR-004 —
      the dashboard renders exactly one create trigger; no create field, hint,
      roots list, preview or heading appears outside the dialog; and a render with
      no page token offers neither the trigger nor the dialog. **Must fail when**
      the form is moved into the dialog but a copy is left behind, which is the
      shape of the mistake this is guarding.
- [x] T009 [P] [US1] `internal/httpapi/partials_test.go`: pin FR-002 — every
      control the form carried is inside the dialog, each with the condition it
      already had. Asserted from a view configured to reach the conditional ones
      (suggestions present, ceiling removed, conversations present), because a
      component rendered from a bare view draws none of them and a test reading
      only that passes while three controls are missing — the near miss
      `TestCreateFormHasNoStartCommandSelect` was written about.
- [x] T010 [P] [US1] `internal/httpapi/partials_test.go`: pin FR-005 and FR-007 —
      the trigger carries `command`/`commandfor`, a close control exists, and the
      dialog carries `closedby`. Read from the markup, so it is a claim about what
      a browser is handed rather than about what the script does.
- [x] T011 [US1] `internal/httpapi/stylesheet_test.go`: pin that the script's
      fallback is feature-detected. **Must fail when** the guard is dropped, which
      is a defect nothing else here can see: the double-open looks like nothing on
      the first click and like a flicker on the second.

---

## Phase 4 — User Story 3 (P1): a refused create keeps the operator where they are

**Goal**: the reason a create was refused is legible above the backdrop, beside the
values that caused it.

**Independent test**: with scripting on, submit a refused name; the dialog is still
open, the fields still hold what was typed, and the sentence is readable.

- [x] T012 [US3] `web/templates/partials/create-form.html`: a `.modal-outcome`
      live region inside the dialog, above the scrolling body so a form scrolled
      to its submit still shows it. Rendered present and empty rather than hidden —
      a live region has to be in the accessibility tree before its text arrives.
      Lands with T013.

      *Rewritten during implementation.* This task said to add `popover="manual"`
      to `#action-toast` on all three pages. Measured in Chrome 149: a popover
      above a modal dialog **is inert**, so the promotion loses the accessibility
      tree, and the popover user-agent rules move the region to the middle of the
      viewport and shrink it to its text. See `contracts/outcome-where-the-operator-is.md`.
- [x] T013 [US3] `web/static/crswd.js`: `show()` takes the form and writes into
      that form's open dialog when there is one, and into the toast otherwise. The
      branch is the dialog's own `open` state, so every card action — which sits
      in no dialog — takes the path it always has. No `sessionStorage` on the
      dialog branch: nothing navigates there, so a stored sentence would be said
      again on the next page the operator loaded. Lands with T012.
- [x] T014 [US3] `web/static/crswd.js`, `internal/httpapi/outcome.go`,
      `internal/httpapi/view.go`, `web/templates/partials/outcome.html`: close the
      dialog on a create that succeeded and leave it open on one that did not,
      decided from the outcome the banner carried per
      `contracts/outcome-where-the-operator-is.md`. Success closes; every refusal,
      the session cap, and a failed start all leave the operator looking at the
      form — **with what they typed still in it**, which the unconditional
      `form.reset()` used to throw away on every answer.

      The banner needed somewhere to say *which* outcome it is, so `outcomeView`
      gained `Code` and both banner shapes render `data-outcome`. It is set after
      the map lookup succeeds, never before, so the value is one of outcome.go's
      constants and no caller string reaches the document.
- [x] T015 [P] [US3] `internal/httpapi/stylesheet_test.go`:
      `TestAnAnswerGoesWheretheOperatorIsLooking` — the script looks for the
      submitted form's dialog, asks whether it is open, and writes into
      `.modal-outcome`; and the promotion that was measured wrong is **gone**
      rather than left beside its replacement, because a leftover `showPopover()`
      relocates the toast for no benefit. `TestTheToastReadsTheBannerTheDaemonRenders`
      still holds the textContent-never-innerHTML half.
- [x] T016 [P] [US3] `internal/httpapi/partials_test.go`:
      `TestARefusedCreateHasSomewhereLegibleToBeSaid` — the dialog carries a live
      region with `role="status"`, `aria-live="polite"` and no `hidden`; the page
      outside it carries exactly one toast; and the toast is **not** a popover.

      *Inverted during implementation.* This task said to pin that a
      `.modal-outcome` is absent. That was written against a design where the
      toast could speak through a backdrop, and the measurement showed it cannot
      speak at all. The assertion is now that the region exists — and the reason,
      with the three focus probes that settled it, is in the test's own comment.

---

## Phase 5 — User Story 2 (P2): the form says less

**Goal**: no control carries more than one line of prose.

**Independent test**: open the dialog and count. One line per control; the roots
list still complete.

- [x] T017 [US2] `web/templates/partials/create-form.html`: apply the cut in
      `contracts/modal-vocabulary.md` — the never-expire note to one line, the
      in-flight note to one line, the resume note removed, the standing-ceiling
      line kept as it is, the roots hint kept in full. **Every cut removes an
      explanation of the daemon; nothing removed explains the field.**
- [x] T018 [US2] `web/templates/partials/create-form.html`: correct the in-flight
      note's copy. It says the page reloads with the fleet, and on the scripted
      path it does not — the dialog closes and the card arrives on the stream. That
      sentence has been wrong since the submit handler started intercepting this
      form, and shortening it is the moment to stop repeating it.
- [x] T019 [P] [US2] `internal/httpapi/partials_test.go`: pin FR-013 — no control
      in the dialog carries more than one line of explanatory text, with the roots
      list exempt as a list. `TestTheCreateFormNamesTheConfiguredRoots` already
      covers FR-014 and must still pass untouched.

---

## Phase 6 — User Story 4 (P3): the dialog dismisses the way a dialog does

Most of this arrived free with T006. What is left is proving it.

- [x] T020 [P] [US4] `internal/httpapi/partials_test.go`: pin that the dialog is
      opened as a modal rather than shown inline — `command="show-modal"` and not
      `command="show"`. The difference is the focus trap, the inert page behind,
      and `Esc`, which is three of this story's four requirements resting on one
      word.
- [~] T021 [US4] `specs/011-new-session-modal/quickstart.md`: walk the browser
      pass and record what the platform actually did. **Two of three done, one
      outstanding, and which is which is written into the quickstart rather than
      implied.**

      Done, driven in Chrome 149 headless against the daemon's own rendered page:
      the dialog opens modally from `command="show-modal"` with no script loaded
      at all; a closed dialog computes `display: none` (which is how the
      `.modal[open]` defect was caught); the in-dialog region is reachable while
      the dialog is modal and the toast is not.

      **Outstanding: the phone.** A headless viewport is not a touch pointer, so
      the `pointer: coarse` sizing of the trigger and the close control, and a
      long form scrolling inside `.modal-body` rather than pushing the close
      control off screen, were not exercised. That needs a person with a phone.

---

## Phase 7 — Polish and cross-cutting

- [x] T022 [P] `web/templates/partials/create-form.html` and
      `web/templates/dashboard.html`: correct the commentary. Both files explain at
      length why the create form sits where it does and what it renders; after this
      feature several of those paragraphs describe a page that no longer exists.
      **Comments explain why, never what** — and a comment that is merely stale is
      worse than none, because it is read as current.
- [x] T023 [P] `docs/design-system.md`: note that the modal-overlay exception to
      "elevation never comes from shadows" is now spent, and by what. The sentence
      granting it has been theoretical since it was written.
- [x] T024 Run the full definition of done, including the tagged suites this
      feature can reach: `go test -tags dev ./...` and `go test -tags quickstart ./cmd/crswd`.
      The quickstart suite binds `127.0.0.1:8765` and the deployed daemon holds it —
      stop it first, or run `go vet -tags quickstart ./...` and **say which one you
      ran**. `-tags tmux` covers `internal/tmuxctl` and `internal/session` and is
      untouched by this feature.
- [x] T025 `docs/fixes-log.md`: nothing. This is the feature lane, not the fix
      lane, and the log is for changes that skipped a spec. Recorded as a task so
      that it is a decision rather than an omission.

---

## Dependencies

```
T001  (setup)
  └── T002 + T003 ──┬── T004 ── T005
                    │
                    ├── Phase 3 (US1): T006 ── T007 ── T008, T009, T010, T011
                    │
                    ├── Phase 4 (US3): T012 + T013 ── T014 ── T015, T016
                    │
                    ├── Phase 5 (US2): T017 ── T018 ── T019
                    │
                    └── Phase 6 (US4): T020 ── T021
                                          │
                                    Phase 7: T022, T023 ── T024 ── T025
```

**US1 and US3 are the MVP and are not independent of each other in practice**:
US1 alone puts a refusal behind a backdrop, which is worse than what ships today.
That is why US3 was raised to P1 during planning.

US2 and US4 are genuinely independent — either could be dropped and the feature
still delivers, and either could have shipped against the inline form.

## Parallel opportunities

| Together | Why they do not collide |
|---|---|
| T008, T009, T010 | Three assertions in one file, no shared fixture, no shared state |
| T015, T016 | Different files — one sweeps the stylesheet, one sweeps the markup |
| T022, T023 | Different documents |

Everything else is sequential, and most of it because the sweeps make it so: a
class must be rendered before it may be styled, and styled before the document
may be held to it.

## Implementation strategy

**MVP is Phase 2 + Phase 3 + Phase 4** — the modal exists, the dashboard leads
with the fleet, and a refusal is legible. That is a shippable dashboard that is
better than today's in the way the operator asked for and worse in no way.

**Then Phase 5**, which is the half of the request that was about verbosity rather
than placement, and which stands on its own.

**Then Phase 6 and 7**, which are proof and tidying.

Stop after any phase and the tree is green, the dashboard works, and the create
form does what it has always done.
