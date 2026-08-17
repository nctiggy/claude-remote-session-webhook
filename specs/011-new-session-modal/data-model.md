# Data Model: New Session Modal

**There is none, and that is the finding rather than an omission.**

This feature adds no entity, no field on an existing one, no stored state, and no
route. A create carries exactly what it carried before — `name`, `work_dir`,
`remote_control`, `lifetime`, `resume`, and the page token — to exactly the same
handler, which resolves the same start command from the operator's own configured
set and writes the same audit record.

What moved is where the form is drawn.

## What is deliberately still true

Recorded because the value of this file is the absence it documents, and an
absence is only worth writing down if something can be checked against it:

| Property | Where it lives | Unchanged |
|---|---|---|
| A create carries a **name, never a command line** | `CreateRequest.StartCommand`, resolved against `start_commands` | Yes |
| Remote control is a **mode**, never a configured name | the `remote_control` boolean | Yes |
| The working directory is resolved against `allowed_roots` | `ResolveWorkDir` | Yes |
| A never-expiring session needs **two** decisions by the same person | the daemon's ceiling plus the per-session switch | Yes |
| A conversation to resume is validated before it becomes an argument | `ValidateResume` | Yes |
| The session cap and the reaper | `internal/session` | Yes |

## The one piece of state this feature does introduce

It is the browser's, it never reaches the daemon, and it is not persisted:

**Whether the dialog is open.** It is held by the platform, in the `<dialog>`
element's own open state and the top layer. Nothing in this feature reads it back
except the script deciding whether a finished create should close the dialog — and
that decision is made from the outcome the daemon returned, not from the state
itself.

The form's field values persist across a close and reopen because it is the same
form element in the same document. That is the platform's behaviour, it is what an
operator wants after a stray `Esc`, and it is recorded in the spec's Assumptions
so that it is a decision rather than an accident.
