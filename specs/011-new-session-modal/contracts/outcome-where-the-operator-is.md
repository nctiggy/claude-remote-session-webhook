# Contract: where an outcome renders while the dialog is open

Covers FR-009, FR-010, FR-011.

**Supersedes `outcome-in-top-layer.md`**, which specified a mechanism that was
measured and does not work. What that file got right is kept below; what it got
wrong is recorded rather than deleted, because the reason is the useful part.

## The problem this exists to solve

A modal `<dialog>` is promoted to the **top layer**, and it **makes everything
outside it inert**. Inert is not "behind the backdrop": an inert element cannot
take focus, is not hit-tested, and **is removed from the accessibility tree**.

`#action-toast` — the one live region this dashboard writes an outcome into —
sits at the foot of `<body>`. So while the create dialog is open it can say
nothing to anybody. A refused create would be invisible *and* unannounced, which
is worse than what shipped before the dialog existed and is the one way this
milestone could have made things worse.

It only bites on the **scripted path**. A browser with no script navigates, gets
the 303, and reads the outcome as a banner on a fresh fleet page with no dialog
anywhere.

## What was tried first, and why it failed

The first design promoted the toast into the top layer with `popover="manual"`,
reasoning that a popover is promoted too and that top-layer stacking is insertion
order — so a toast shown after the dialog opened would land above it.

The stacking claim is true. It does not help, and the measurement is why this
contract was rewritten before the code shipped rather than after. In Chrome 149:

| Probe | Result |
|---|---|
| A control inside the toast, **no dialog open** | focusable |
| A control inside the **dialog**, dialog open | focusable |
| A control inside the **promoted toast**, dialog open | **not focusable** |
| The promoted toast's box | 34×26px, centred in a 1200×813 viewport |

Two defects, and the first is silent. **A popover above a modal dialog is inert
too** — so the promotion bought the top layer and lost the accessibility tree,
which is the half that mattered. And the popover's user-agent rules
(`inset: 0; margin: auto; width: fit-content`) override the toast's own
fixed-to-the-bottom positioning, moving and shrinking it for nothing.

## The rule

**The answer follows the operator.**

```html
<dialog class="modal" id="create-dialog" …>
  <div class="modal-head">…</div>
  <p class="modal-outcome" role="status" aria-live="polite"></p>
  <div class="modal-body">…the form…</div>
</dialog>
```

- **Dialog open** → the answer is written into `.modal-outcome`, inside the
  dialog, above the scrolling body so a form scrolled to its submit still shows
  it.
- **Dialog closed, or a form in no dialog at all** → the toast, exactly as
  before. Every card action takes this branch and nothing about it changes.

The branch is read from the dialog's own `open` state, not from which form was
submitted.

**This is not the second outcome region the earlier draft refused, and the
distinction is worth stating rather than assuming.** The objection was that a
page with two places an outcome can be written has no outcome region. Here the
toast is unreachable *by construction* while the dialog is open — the platform
has made it inert — so exactly one region can speak at any moment. The copy is
`outcome.go`'s either way; what varies is which of two mutually exclusive places
it is legible in.

## What happens on each answer

| Answer | Dialog | Where the sentence goes | Fields |
|---|---|---|---|
| Created | **Closes first** | The toast, over the fleet it just changed | Reset |
| `bad-name` | Stays open | `.modal-outcome` | Keep what was typed |
| `bad-work-dir` | Stays open | `.modal-outcome`, refusal unchanged | Keep what was typed |
| `bad-lifetime`, `bad-resume`, `bad-start-command` | Stays open | `.modal-outcome` | Keep what was typed |
| `limited` — the session cap | Stays open | `.modal-outcome` | Keep what was typed |
| `create-failed` | Stays open | `.modal-outcome` | Keep what was typed |
| The request never reached the host | Stays open | `.modal-outcome` | Keep what was typed |
| Anything unrecognised | Stays open | `.modal-outcome`, generic sentence | Keep what was typed |

The distinction the script makes is **success against everything else**, read
from `data-outcome` on the banner the daemon rendered — its own closed
vocabulary, never the prose. An answer carrying no code takes the refusal path,
which leaves the operator looking at what they typed: the safe direction to be
wrong in.

**Closing happens before the sentence is shown**, and that ordering is the whole
of why a success reads in the toast: by the time the answer is written the dialog
is closed, so the branch above sends it to the toast without needing a second
rule.

## What must not change

- **The answer is read as text.** `textContent`, never `innerHTML`, in the region
  as in the toast. The banner is daemon-authored today; this is what keeps it safe
  on the day one of these sentences carries a name or a path.
- **The uniform refusal stays uniform.** "Outside the roots", "not a directory"
  and "not there at all" still answer identically, so the dialog cannot be asked
  whether a path exists.
- **The scriptless path is untouched.** A 303 to the fleet with `?outcome=…` and a
  banner, exactly as today.
- **The toast keeps its six seconds, its `sessionStorage` carry across a reload,
  and its clearing of an earlier answer by a later one.** None of that applies to
  the dialog's region and none of it is copied there: nothing navigates on that
  branch, so a stored sentence would be said again on the next page the operator
  happened to load. The region is cleared on the next submission instead.

## Degradation

| Condition | Behaviour |
|---|---|
| No script | The banner on the fleet page, unchanged |
| Script, dialog closed | The toast, unchanged |
| A dialog with no `.modal-outcome` | The toast — wrong, and the reason it is pinned by a test in both trees |
| Storage refused | The toast still paints; it does not survive a reload |
