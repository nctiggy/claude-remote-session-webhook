# Contract: revival

## Cadence

One sweep every `SweepInterval` (30s), the reaper's cadence, started once by the
daemon and never restarted.

## One tmux call per sweep

The sweep asks tmux exactly one question — the existing `List`, with
`#{pane_current_command}` added to its format — and judges every session from
that one answer. A sweep whose cost grows with fleet size would be a sweep an
operator learns to fear.

## Decision

The ordered table in [data-model.md](../data-model.md#revival-decision) is the
contract. Two properties matter most:

- **Terminal is terminal.** Nothing moves a session out of `dead`. Nothing moves
  it out of `failed` except an operator.
- **The bound is written before the attempt.** `ReviveAttempts` and
  `NextReviveAt` are persisted *before* the daemon acts, so a daemon that dies
  mid-revival comes back backing off rather than retrying instantly. This is the
  2,826-restarts defence, and it only works in that order.

## What a revival does

**In place** (tmux session survived):
1. Resolve the start command by name; render it; insert `--resume <uuid>` with
   the existing `config.InsertStartFlags`.
2. `SendKeys` the line and Enter into the surviving shell.

**Recreate** (tmux session gone):
1. `New` a tmux session in the recorded working directory, under the same session
   id, so the tmux target is the one the record already names.
2. Re-write every `@crswd-*` option — managed, owner, name, workdir, start,
   lifetime, conversation — *before* the command is sent, so a failure part-way
   leaves something `Adopt` can recognise rather than an unowned shell.
3. Then exactly as *in place*.

## What a revival never does

- Extend a lifetime, or issue a new deadline. `CreatedAt` is carried, never
  refreshed (FR-010).
- Mint a new credential. The record never went away, so its ownership did not
  change — unlike adoption, which mints precisely because the record *had* gone.
- Take the fleet over the cap (FR-011).
- Run twice at once for one session (FR-015).
- Touch `LastActivity`. Reviving is not driving; the operator did not drive it.

## Audit

One record per decision that acted, named for what it did:

| action | when |
|---|---|
| `supervisor.revive` | an attempt was made — carries the attempt ordinal |
| `supervisor.recreate` | an attempt was made that had to build a new shell |
| `supervisor.recovered` | an attempt succeeded |
| `supervisor.failed` | attempts exhausted, or a refusal made the session terminal |

Each carries the session id and the reason constant. None carries pane content, a
credential, a working directory the caller spelled, or any free text (FR-022).
A healthy session produces no record — a trail that logged every sweep would bury
the four lines that matter.
