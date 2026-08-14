# Running harnesses other than Claude Code

**Status: research. Nothing here is implemented, and nothing here should be built
without its own spec.**

The question: can this daemon drive coding agents other than Claude Code — Codex,
Aider, Gemini CLI, Cursor's CLI, a plain shell — and what would that take?

## The short answer: most of it already exists

`crswd` has never actually known what Claude Code is. It starts a tmux session,
types a command line into it, and reads the pane back. The command line is
configuration.

Two settings already carry this:

| Setting | What it holds |
|---|---|
| `start_command` | The one command a create types when nothing else is configured |
| `start_commands` | A **named set**: `name=command line,name=command line,…` |

And the property that makes it safe is already load-bearing: **a create carries a
name, never a command line.** `CreateRequest.StartCommand` is a key into the
operator's configured set, and a name the daemon does not configure is a refusal
rather than a shell. `config.go` says why:

> a switch wired to a name is a switch whose behaviour an operator can read out of
> their own configuration and change there, and a switch wired to a command line
> would be the thing the allowlist exists to prevent.

So an operator can already write:

```ini
start_commands = claude=claude --dangerously-skip-permissions,codex=codex --full-auto,aider=aider --yes,shell=
```

…and the daemon will start any of them. Today.

## Where the gap actually is

**The daemon supports N harnesses. The dashboard exposes two.**

- The **signed API** passes `req.StartCommand` straight through, so a script or
  the companion skill can already name any configured harness.
- The **browser create form** offers a single boolean, `remote_control`, which
  `actions.go` resolves to either the default command or `RemoteStartCommand()`.
  Two outcomes from a set that may hold ten.

That asymmetry is the whole feature. It is a UI gap, not a capability gap, which
is why this note exists instead of a milestone.

## What building it would look like

**The small version — probably the right one.** Replace the boolean switch on the
create form with a control that lists the configured names, defaulting to the
current behaviour when only one is configured. The card already shows a session's
mode; it would show the harness name instead. No new daemon concept, no new
security surface, and the refusal for an unknown name already exists.

The honest cost: `Mode` is currently a two-valued type (`ModeLocal`,
`ModeRemote`) that several things read, including the mode-switch route and the
card's display. Generalising it to "which named command" is a change to a type
that is deliberately small, and small types are load-bearing here.

**The larger version — and the reason to think before starting.** Different
harnesses are not interchangeable in the ways that matter to this daemon:

- **The pane reader assumes a TUI.** `capture-pane` returns whatever the program
  drew. Milestone 7 wrapped it for phones on the strength of what Claude Code
  draws; a harness with different chrome may read worse or better and nobody
  would know until they looked.
- **"Needs attention" is a Claude Code shape.** The status pill's states —
  running, needs-auth, dead — are inferred from what the daemon can see.
  Another harness prompting for input may not look like any of them.
- **The permission model differs per tool**, and this daemon's containment story
  is `allowed_roots` plus the operator's own judgement about
  `--dangerously-skip-permissions`. Each harness has its own flag with its own
  meaning, and an operator configuring `codex` should not have to guess that the
  equivalent flag is spelled differently and means something slightly different.
- **Compact is Claude Code's.** The compact route types a Claude Code command. On
  another harness it is either meaningless or wrong.

None of these block the small version. All of them mean "supports other harnesses"
is a claim to make carefully rather than a checkbox.

## What to decide before building anything

1. **Is the goal "run another tool" or "run another tool as well as this one runs
   Claude Code"?** The first is nearly free. The second means per-harness
   knowledge — what its states look like, whether compact means anything — and
   that is a plugin boundary this project has never needed.
2. **Does `Mode` generalise, or does it gain a sibling?** A session already knows
   its start command; the mode may be derivable rather than stored.
3. **What does the pill say for a harness the daemon has no states for?** "Running"
   and "dead" are observable from tmux alone. The other two are not.

## What is not in question

Whatever this becomes, a create keeps carrying **a name and never a command
line**. That is the property that makes an operator's configured set an allowlist
rather than a suggestion, and it is not negotiable for convenience.
