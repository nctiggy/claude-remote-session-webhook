# Contract: the create form's command-line preview

Covers FR-014 through FR-018.

## What is shown

The exact command line the create will run, for the form's current state:

```
claude --dangerously-skip-permissions --continue "/rc my-session"
```

It is the output of the same rendering the daemon uses — the configured template
for the selected mode, with the resume flags inserted and `{name}` substituted.

## How it is built

**Server-rendered first.** The view carries the resolved command line for each
reachable mode, and the template renders the one matching the form's initial
state. With no script running, the operator sees the command for the default
state, which is the state an unmodified form submits.

**Updated by script.** `crswd.js` re-renders the block when the mode switch, the
resume control, or the name field changes. It **selects between command lines the
server supplied** and substitutes the name; it does not assemble a command from
parts. String assembly in the browser would be a second implementation of
`RenderStartCommand`, free to disagree with the one that runs.

## What it may not be

- **Not editable.** No `contenteditable`, no input, no textarea. It is a `<pre>`.
- **Not a source of commands.** Nothing the browser sends selects a command line
  other than by mode and resume state. The configured `start_commands` set remains
  the only source of executable commands, and a create still resolves its command
  from the record's start-command *name*, server-side, exactly as before.
- **Not a place values escape escaping.** The session name is interpolated into
  the preview and is rendered by `html/template` as text, like every other
  operator value and for the same reason pane output is.

## Degradation

| Condition | Behaviour |
|---|---|
| No script | The default-state command is shown, correct for an unmodified submission |
| No configured commands | The block renders the built-in default command |
| A mode with no configured command | The block is absent rather than empty — `FR-018a`'s discipline about absent values |

## Disclosure

The command lines are the operator's own configuration, already rendered in full
to the same authenticated caller on the settings page (`startCommandSet`), and
`start_commands` is not a secret. The preview shows this caller nothing new.

Milestone 4's rule stands unchanged: the switch carries a **mode**, never a name,
and the form still posts `remote_control=on` or nothing at all.

## Tests that must fail without the change

- The rendered form contains the resolved default command line.
- The rendered command line equals what `start` types, for each mode.
- A name containing `<` renders escaped in the preview.
- The preview element carries no `contenteditable` and no form field name.
- A view with no command for a mode renders no preview block for it.
