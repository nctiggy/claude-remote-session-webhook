# Contract: Remote control at create time

**Files**: `web/templates/partials/create-form.html`, `internal/httpapi/dashboard.go`, `internal/httpapi/view.go`, `internal/httpapi/actions.go`
**Tests**: `internal/httpapi/partials_test.go`, `internal/httpapi/actions_test.go`
**Satisfies**: FR-001 … FR-005
**Replaces**: the `<select name="start_command">` at `create-form.html:225`

---

## Why this contract exists

Milestone 4's FR-026 said: *"An operator MUST be able to choose remote control as
a mode when creating a session, rather than selecting a command by name."*

Three tasks shipped for it — `Mode()` derivation, the toggle route, the card
display — all green, and **the create form was never touched**. Every assertion
was about a route or a record. This contract's tests read markup.

## The control

Literal:

```html
<div class="field field-switch">
<input class="switch-input" id="create-remote" type="checkbox" name="remote_control" value="on">
<label class="switch-label" for="create-remote">Remote control</label>
</div>
```

A real `<input type="checkbox">`, styled as a switch. The native control is the
accessible core; only its presentation changes. No script is required for it to
work.

## What it posts

| Submitted | Mode | Why |
|---|---|---|
| `remote_control=on` | remote | the box was ticked |
| field absent | **local** | an unticked checkbox posts nothing |
| `remote_control=` anything else | **uniform refusal** | not one of the two states (FR-003) |

"Absent means local" is deliberate and is the safe direction: a lost or stripped
field yields the *less* privileged mode, never the more.

## What must never reach the browser

**No command name and no command line, in either direction** (FR-002, FR-004).

`dashboard.go:297` currently sends `StartCommands: s.cfg.StartCommands.Names()`
into the view. That field goes away. Which configured command each mode runs stays
the daemon's decision, read from configuration at startup and never from a
request — exactly as the existing mode-toggle route already does it.

## Worked example

Configuration: `start_commands = default=…,rc=…` and `remote_start_commands = rc`.

| Operator does | Form posts | Session runs | Card says |
|---|---|---|---|
| leaves the switch off | (no field) | `default` | `local` |
| turns the switch on | `remote_control=on` | `rc` | `remote` |
| hand-builds `remote_control=rc` | — | nothing | uniform refusal |

The third row is the security case: `rc` is a real configured name, and it is
**still refused**, because the field carries a mode and not a name.

## Contract tests

| Test | Asserts | **Must fail when** |
|---|---|---|
| `TestCreateFormHasNoStartCommandSelect` | Rendered markup contains no `<select` and no `name="start_command"` | **The route accepts the right value but the form still renders the old control.** This is the exact milestone-4 miss and it is why this row is first. |
| `TestCreateFormRendersNoCommandName` | For every configured name, zero occurrences in the rendered markup | A name leaks into a label, a value, a title, or a data attribute |
| `TestCreateFormRendersRemoteSwitch` | Exactly one `type="checkbox"` with `name="remote_control"` and `value="on"`, with a `<label for>` bound to it | The control is a button, a link, or an unlabelled input |
| `TestAbsentFieldMeansLocal` | No `remote_control` in the post → local mode | Absence is treated as an error, or as remote |
| `TestRemoteControlOnMeansRemote` | `remote_control=on` → remote mode, and the card says so in words | The value is accepted but not applied |
| `TestArbitraryRemoteControlValueRefused` | `remote_control=rc` → uniform refusal, nothing starts | The value is passed through as a command name |
| `TestViewCarriesNoStartCommands` | The view struct handed to the template exposes no command names | The field is left in place "for later" |
| `TestSwitchIsKeyboardOperable` | The checkbox is focusable, has a visible focus ring, and toggles with Space | `appearance: none` without a focus style |
| `TestModeNotConveyedByColourAlone` | The switch's state is readable from text or shape, not hue | The switch is styled with colour as its only cue (FR-019) |
| `TestCreateEmitsExactlyOneAuditRecord` | One record per create, whichever mode | The mode choice adds a second record |
