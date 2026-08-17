# Contract: the Modal class vocabulary and what holds it to the document

Covers FR-013, FR-015, FR-016.

## Modal is a vocabulary, not a partial

`docs/components.md` has carried a Modal section since before there was anything
to put in one, with an illustrative call site:

```gotemplate
{{ template "modal" (dict "Title" "Destroy session?" …) }}
```

**That cannot be written in this tree.** The template set is parsed with no
function map, so there is no `dict`. A Go template takes one argument; a Modal
partial would have to be handed the create view and call the create form by name
from inside itself, which is not a component — it is this dialog with a general
name on it.

So Modal ships the way Button and Field already ship: a class family the
templates use, documented, swept, and ready to lift into a partial unchanged the
day one is warranted. The document already states that path for exactly these
names.

## The family

| Class | On | For |
|---|---|---|
| `.modal` | `<dialog>` | The surface, its border, its padding, its width bound |
| `.modal::backdrop` | — | The dimming behind it |
| `.modal-head` | `<div>` | Title and close, on one row |
| `.modal-title` | `<h2>` | The dialog's accessible name, referenced by `aria-labelledby` |
| `.modal-close` | `<button>` | The icon-only dismissal, which therefore carries an accessible name |
| `.modal-body` | The `<form>` | The scrolling region, so a long form does not push the dialog past the viewport |

`.create`, `.create-form` and `.create-note` survive where they still describe
something. `.create-heading` does not: its words become `.modal-title`, and a
class no template renders is a rule the sweep reads as dead.

## Rules, carried across from the Modal section unchanged

- Focus moves into the modal on open and returns to the trigger on close.
- `Esc` closes. The backdrop closes, where the browser has it. Both, always.
- Never nest a modal inside a modal.
- Uses `<dialog>`, so the focus trap comes from the platform rather than from JS.

## Tokens only

No literal colour, size or font — in the rules or in the template. This includes
the backdrop, whose dimming is expressed from `--ground`.

**Elevation is the one exception, and it is already granted.**
`docs/design-system.md` says elevation comes from `--surface-lift` and borders,
never shadows, and then names the single exception: *a modal overlay*. This
feature is that exception being spent, for the first time, on the case it was
written for. It is not a licence for a second one.

## What holds this to the document

`documentedComponentClass` in `stylesheet_test.go` becomes:

```go
var documentedComponentClass = regexp.MustCompile(`\.(combo|switch|masthead|action-toast|modal)[\w-]*`)
```

That expression is the third direction of the class sweep — the one that catches a
class which is rendered *and* styled *and* undocumented. Its own comment records
why the toast is in it: `docs/components.md` called the toast unused for four
milestones while it shipped on three pages, because being rendered and styled
satisfies the other two sweeps and only a document-facing check can see the drift.

A `.modal*` family added without joining that expression is the same rot, arranged
in advance. Widening it is deliberate, and the point is that the document's side
of this change has to land in the same commit or the build is red.

**Must fail when** a `.modal*` rule exists that `docs/components.md` never names,
or the document names one the stylesheet does not have.

## The prose cap

FR-013: no control in the dialog carries more than one line of explanatory text.
The allowed-roots hint is exempt, because it is a list rather than prose and the
refusal deliberately will not name what is in it.

| Today | After |
|---|---|
| Roots: a sentence and the full list | Unchanged, every root |
| Never-expire note, two sentences | One line |
| Standing-ceiling line, one sentence | Unchanged |
| Resume note, two sentences | Removed — the label and the options say it |
| In-flight note, two sentences | One line |
| `create-heading` "Start a session" | Becomes `.modal-title`, same words |

Every cut removes an explanation of **the daemon**. Nothing removed explains
**the field**.
