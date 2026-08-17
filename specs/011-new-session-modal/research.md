# Research: New Session Modal

**Feature**: 011-new-session-modal | **Date**: 2026-08-17

Phase 0. Every unknown the spec left to the plan, resolved. Nothing here is a
preference; each entry says what was chosen, why, and what was rejected.

---

## R1 — How a `<dialog>` opens with no script

**Decision**: The **Invoker Commands API** — `<button command="show-modal"
commandfor="create-dialog">` — with a feature-detected fallback in `crswd.js`
that binds a click to `showModal()`.

**Rationale**: `command`/`commandfor` is the declarative equivalent of calling
`HTMLDialogElement.showModal()`, and it reached Baseline in 2026 when Safari 26.2
shipped it, joining Chrome 135 and Firefox 144. On such a browser the dialog
opens, traps focus, and closes on `Esc` with no JavaScript at all — which is the
property that lets this feature keep the tree's rule that every control works
with the script switched off. `showModal()` itself has been Baseline since March
2022 at ~97% global support, so the fallback covers everything between those two
dates.

The detection is on the platform, not the browser:

```js
if (!('commandForElement' in HTMLButtonElement.prototype)) { /* bind the fallback */ }
```

A script that bound the click unconditionally would open the dialog twice on a
browser that supports both — the declarative invocation and the listener would
each fire.

**Alternatives rejected**:
- **`showModal()` unconditionally, no invoker attributes.** Simpler by three
  lines, and it makes the create form script-dependent for the first time in this
  tree's history. The whole dashboard is built the other way round.
- **`<details>`/`<summary>` disclosure.** Scriptless and Baseline for years, but
  it is not a modal: no focus trap, no `Esc`, no backdrop, and the page behind it
  stays interactive. That is a disclosure widget wearing a modal's clothes, and
  `docs/components.md`'s Modal rules describe the thing it is not.
- **A separate `/dashboard/new` page.** Scriptless by construction and it costs a
  route, a template, a round trip, and a second place a create can be composed —
  which is the "second create path" the spec rules out.

---

## R2 — Where the refusal goes when the dialog is open

**Decision**: a live region inside the dialog, `.modal-outcome`. The toast is
untouched and keeps every other answer on every other path.

**This entry was rewritten during implementation, and the first answer is kept
below rather than deleted**, because the reason it failed is the useful part and
because it is the one decision here that was measured instead of reasoned.

**Rationale**: This is the defect the spec found before implementation started. On
the scripted path the create form is posted by the submit handler in `crswd.js`
and never navigates; the answer is pulled out of the banner the 303'd page
rendered and written into `#action-toast` at the foot of `<body>`. A modal
`<dialog>` is promoted to the **top layer** and **makes everything outside it
inert**, and inert is stronger than hidden: an inert element cannot take focus,
is not hit-tested, and is removed from the accessibility tree. So that sentence
would be neither seen *nor announced* — worse than what shipped before the dialog
existed.

### What was decided first, and what measuring it showed

The original decision was to give `#action-toast` `popover="manual"` and show it
with `showPopover()`, on the reasoning that a popover is promoted to the top
layer too and that top-layer stacking is insertion order — so a toast shown after
the dialog opened would land above it.

The stacking claim is correct. It does not help. Driven in Chrome 149 against the
daemon's own rendered fleet page:

| Probe | Result |
|---|---|
| A control inside the toast, **no dialog open** | focusable |
| A control inside the **dialog**, dialog open | focusable |
| A control inside the **promoted toast**, dialog open | **not focusable** |
| The promoted toast's box | 34×26px, centred in a 1200×813 viewport |

The first two rows are the controls that make the third mean something: focus
works in that harness, and the dialog is not blocking focus generally. **A
popover above a modal dialog is inert too** — the promotion bought the top layer
and lost the accessibility tree, which is the half that mattered. And the popover
user-agent rules (`inset: 0; margin: auto; width: fit-content`) override the
toast's own fixed-to-the-bottom positioning, moving and shrinking it on the way.

So the retreat this entry had already written down became the design. The
objection to it was that a page with two places an outcome can be written has no
outcome region — and the measurement is what answers it: the toast is unreachable
*by construction* while the dialog is open, so exactly one region can speak at any
moment, and both take their words from `outcome.go`. A create that succeeds closes
the dialog before the sentence is shown, so a success still lands in the toast
over the fleet it just changed.

**Alternatives rejected**:
- **The popover promotion.** Measured above.
- **Close the dialog on a refusal and let the toast do its job.** The cheapest
  option, and it satisfies SC-004's "zero retyping" — the form element persists,
  so reopening restores every value. It fails US3 as written, which asks the
  operator not to be moved at all.
- **Render the outcome banner into the dialog by swapping markup.** A third
  rendering of a sentence that already has two.

---

## R3 — Whether Modal becomes a partial

**Decision**: **No partial.** Modal ships as a class vocabulary — `.modal`,
`.modal-head`, `.modal-title`, `.modal-close`, `.modal-body` — used by
`partials/create-form.html`, exactly as Button and Field already ship as
`.button` and `.field` with no partial on disk.

**Rationale**: Two facts force it, and they point the same way.

First, **the template set is parsed with no function map**, so there is no `dict`
and `docs/components.md`'s illustrative `{{ template "modal" (dict "Title" …) }}`
call site cannot be written. Go templates take one argument; a modal partial
would have to receive the whole create view and call the create form by name from
inside itself, which is not a reusable component — it is the create dialog with a
generic name on it.

Second, `TestEveryCanonicalComponentIsAPartial` reads a hard-coded inventory
(`components` in `partials_test.go`) and requires a file per entry in
`templates/partials/`. Adding `modal` there would demand a partial that has no
honest content.

The precedent is already written in the document being changed: *"the class names
in those sections are the ones the shipped templates already use — `.button`,
`.button-danger`, `.field`, `.field-label` — so the day one of them earns a
partial, the markup lifts into it unchanged."* Modal takes that path.

**This amends FR-015**, which asked for the move into the canonical inventory.
See plan.md § Spec amendments.

**Alternatives rejected**:
- **Add a function map with `dict`.** It would make a real Modal partial
  possible and it changes how every template in the tree is parsed, for one
  component. Constitution IV.
- **A `modal` partial taking the create view.** Named for reuse, capable of none.

---

## R4 — Holding the new vocabulary to the document

**Decision**: Extend `documentedComponentClass` in `stylesheet_test.go` from
`\.(combo|switch|masthead|action-toast)[\w-]*` to include `modal`.

**Rationale**: That regex is the third direction of the class sweep — the one
that catches a class which is rendered *and* styled *and* undocumented. Its own
comment says the toast is in it "because it is the family that already rotted":
`docs/components.md` called the toast unused for four milestones while it shipped
on three pages, and no sweep could see it. A `.modal*` family added without
joining that expression is the same rot, pre-arranged.

Widening it is deliberate and it is the point: the document's side of the change
has to land in the same commit.

**Alternatives rejected**: leaving the regex alone and trusting review. That is
the practice Principle V exists to replace.

---

## R5 — Backdrop dismissal

**Decision**: `closedby="any"` on the dialog. Nothing else, and no script.

**Rationale**: Where supported, the platform gives light dismiss for free and
correctly. Where not, the attribute is ignored and `Esc` and Cancel still close
the dialog — FR-008's "must not be the only way" is satisfied by construction
rather than by a fallback. As of mid-2026 `closedby` is shipped in Chrome, Edge
and Firefox and is on WebKit's Interop 2026 list, so it is Limited availability
rather than Baseline; that is precisely the profile of an enhancement.

**Alternatives rejected**: a click listener on the dialog comparing
`event.target` to the dialog element itself. It is the well-known polyfill, it is
four lines, and it re-implements in script something the platform is in the
middle of shipping — and it gets the padded-dialog case wrong unless the padding
moves to an inner element, which is a layout change made to serve a workaround.

---

## R6 — What the copy cut actually removes

**Decision**: The four multi-sentence hints become one line each or nothing.

| Today | After | Why |
|---|---|---|
| Roots hint: a sentence plus the full list | **Kept in full** | The refusal deliberately will not name the roots. This is the one hint that is load-bearing. |
| `create-never-expire-note` — two sentences | One line | "Removes the absolute lifetime. Nothing then reaps this session." |
| Standing-ceiling line — one sentence | **Kept** | It is already one line and it is the only thing telling an operator their session ends. |
| `create-resume-note` — two sentences | Removed | The `<select>`'s own label and options say it. |
| `create-busy` note — two sentences | One line | It says the page reloads; on the scripted path it does not. |
| `create-heading` "Start a session" | Becomes the dialog title | Same words, one place. |

**Rationale**: FR-013 caps prose at one line per control, and the roots list is
exempt as a list. Every cut above removes an explanation of *the daemon*; nothing
removed explains *the field*.

**Alternatives rejected**: cutting the roots list to the first root. A hint
missing an operator's second root reads as a refusal they cannot explain — the
template already says so, and it is right.

---

## R7 — What must not move

Recorded so the plan can be checked against it:

- The route, the field names (`name`, `work_dir`, `remote_control`, `lifetime`,
  `resume`), the page token, and `confirm`-style discipline.
- `ValidateName`, `ResolveWorkDir`, `ValidateResume`, `resolveLifetimes` — every
  refusal stays server-side and uniform.
- The absence of a `pattern` on the working-directory field, and the absence of a
  command-name chooser (`TestCreateFormHasNoStartCommandSelect`).
- The datalist/combo enhancement, its status region, and its keyboard behaviour.
- The command preview as a readout with no `name` attribute.
- The submit-once guard.
