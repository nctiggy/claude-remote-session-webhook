# Quickstart: proving the New Session Modal

**Feature**: 011-new-session-modal

How to convince yourself this shipped correctly. Automated checks first, because
they are the ones that stay true; the manual pass covers what no test in this tree
can see — the platform's own focus behaviour and the top layer.

## Prerequisites

```bash
go mod download
tmux -V          # the tagged suites need it
```

## The automated pass

```bash
go build ./...
go vet ./...
go test ./...
golangci-lint run
```

`go test ./...` is where this feature actually lives. The pins that matter:

| Test | Proves |
|---|---|
| the trigger pin (new) | The dashboard renders one create control and no create field outside the dialog — FR-001 |
| the dialog-contents pin (new) | Every control the form carried is inside the dialog, with its own condition intact — FR-002 |
| `TestCreateFormCarriesToken` | The token still rides in the form — FR-003 |
| `TestAComponentHandedNothingToActWithOffersNoAction` + the trigger pin | No token, no trigger and no dialog — FR-004 |
| the declarative-open pin (new) | The trigger carries `command`/`commandfor` and the script's fallback is guarded — FR-005 |
| the close pin (new) | A Cancel control exists and `closedby` is set — FR-007, FR-008 |
| the prose-cap pin (new) | No control carries more than one line — FR-013 |
| `TestTheCreateFormNamesTheConfiguredRoots` | Every root is still listed — FR-014 |
| `TestTheComponentsDocumentNames…` (extended) | `.modal*` is documented — FR-015 |
| `TestNoTemplateCarriesAValueThatBelongsInATokenOrAnOrigin` | No literal colour or size in the markup — FR-016 |
| `TestTheStylesheetStylesNoElementTheMarkupNeverRenders` | The `dialog` rules have a `<dialog>` to style |
| `TestTheStylesheetAndTheMarkupNameTheSameThings` | `.create-heading` did not survive as a dead rule |
| `TestTheRainCarriesNoInformationAndStaysOffReadingContent` | No rain behind the dialog — FR-017 |
| `TestCreateFormHasNoStartCommandSelect` | The chooser is still gone. This one is here because it is the assertion milestone 4 did not have |

Then the acceptance suite, which needs `127.0.0.1:8765` free — stop the deployed
daemon first or it binds the port:

```bash
go test -tags quickstart ./cmd/crswd
```

If the port is held, the honest substitute is `go vet -tags quickstart ./...`,
which compiles the suite without running it. Say which one you ran.

## The browser pass

The things no Go test in this tree can see: the platform's own focus behaviour,
the top layer, and inertness. Two of the three were driven in a real engine rather
than eyeballed, and the results are recorded here because the record of having
looked is the only evidence there is.

**Method.** The daemon's own rendered fleet page was dumped to a file, its
stylesheet inlined, and loaded in Chrome 149 headless (`--dump-dom`), with probes
appended that write their findings into the DOM. Chrome 149 is well past the
versions this feature depends on — Invoker Commands (135), `closedby` (134),
`showModal()` (2022).

### 1. It opens with no script at all — **verified**

The daemon's script reference was removed from the harness, so nothing but the
markup was loaded. Clicking the trigger:

| Probe | Before the click | After |
|---|---|---|
| `dialog[open]` | `false` | `true` |
| computed `display` | `none` | `flex` |
| `dialog:modal` | `false` | `true` |

So the dialog opens **modally** — top layer, focus trap, inert page behind, `Esc`
— from `command="show-modal"` alone. That is FR-005, and it is the property the
whole shape was chosen for.

The `display: none` in the first column is the other half, and it is the defect
this pass caught: an author `display` on `.modal` beats the user-agent's
`dialog:not([open]) { display: none }`, so the first draft of the stylesheet would
have left the dialog standing open on the fleet. The rule is on `.modal[open]`,
and `TestAClosedDialogStaysClosed` is the pin.

### 2. A refusal is legible where the operator is — **verified**

With the shipped `crswd.js` inlined and the dialog open:

| Probe | Result |
|---|---|
| `.modal-outcome`, empty | `display: none` — no empty box |
| `.modal-outcome`, with a sentence | `display: block`, inside the dialog's box |
| A control **inside `.modal-outcome`** can take focus | **`true`** |
| A control **inside `#action-toast`** can take focus | **`false`** |

The last two rows are the whole of
`contracts/outcome-where-the-operator-is.md`. A modal dialog makes everything
outside it inert — the toast included — so a refusal written there would be
neither seen nor announced. The promotion tried first (`popover="manual"`) is
inert too; that measurement, with its controls, is in `research.md` R2.

To repeat it by hand: open the dialog, submit an empty name. The dialog stays
open, the fields keep what was typed, and the reason reads inside the dialog. Then
submit a valid create: the dialog closes, the toast says it over the fleet, and
the new card arrives on the stream without a reload.

### 3. It is usable on the phone — **not verified, and it needs a person**

This is the one that was not done, and it is stated rather than implied. A
headless viewport is not a touch pointer: the `pointer: coarse` block is what
sizes the trigger and the close control, and no automated pass here exercised it.

Open the dashboard on the phone you actually use and check:

- The dashboard is sessions and one button. Scroll it: nothing about creating a
  session appears.
- The trigger and the close control are thumb-sized.
- The dialog fits the viewport, and a long form scrolls **inside** `.modal-body`
  rather than pushing the close control off the screen.
- Focusing a field does not zoom the page.

## What "done" means here

Build, vet, test and lint green, the quickstart suite run or its substitute named,
and the browser pass above. **Section 3 is outstanding**, and that is the honest
state rather than a formality — `AGENTS.md`'s definition of done is not decorative
here, and Principle III is that "it works" is a demonstration rather than a claim.
