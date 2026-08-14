# Contract: the `@crswd-lifetime` option and its round trip

Covers FR-007 through FR-013.

## Written

`Manager.start` sets `@crswd-lifetime` on the tmux session, after
`@crswd-start` and before the start command is typed. Always — including when the
value is empty.

| `Session.Lifetime` | option value |
|---|---|
| `0` | `""` |
| `< 0` | `never` |
| `> 0` | `d.String()`, e.g. `72h0m0s` |

A failure to set it fails the create, exactly as the five options before it do. A
session whose lifetime the host did not record is a session that would come back
from a restart as something else, which is the defect this contract exists to
close.

## Read

`List` returns it verbatim as `SessionInfo.Lifetime`. `tmuxctl` does not parse it.

## Restored

`Manager.Adopt` turns the string back into a duration:

| option value | restored | then |
|---|---|---|
| `""` | `0` | the daemon's configured default applies — this is every pre-feature session |
| `never` | `-1` | subject to the ceiling check below |
| a valid duration | that duration | subject to the ceiling check below |
| anything else | `0` | treated as absent; adoption proceeds |

**Nothing here fails an adoption.** A malformed value is absence, not an error:
`FR-010`, and the standing rule that an unadopted session is an unowned
unsandboxed shell.

### The ceiling check

The restored value is passed through the same rule `resolveLifetimes` applies to a
create, against the ceiling **currently** configured:

- A restored `never` on a daemon whose ceiling is finite → the daemon's default.
- A restored duration above the current ceiling → the daemon's default.
- Anything the current configuration would grant → kept.

A substitution is recorded on that session's `startup.adopt` record, with a reason
naming that the recorded lifetime was not granted under the current ceiling. The
reason is a constant in the daemon, never built from the value — the trail carries
no byte a caller supplied.

### Ordering

The ceiling check runs **before** the "past its deadline while we were down"
check, so a session is judged against the deadline it will actually be adopted
with rather than one it will not keep.

## Invariants

1. `CreatedAt` remains tmux's `#{session_created}`. A restart never extends a
   session.
2. A session created before this option existed adopts cleanly and takes the
   default.
3. A value on a session without `@crswd-managed` influences nothing — such a
   session is neither adopted nor touched, as today.
4. Two adoption passes over a live store leave the first pass's records alone.

## Tests that must fail without the change

- Create with `Lifetime < 0` → assert `SetOption(@crswd-lifetime, "never")` is in
  the recorded argv.
- Adopt a fake session carrying `never` → the adopted record has
  `LifetimeDisabled() == true`.
- Adopt carrying `never` with a finite ceiling → the record has the default, and
  the adopt record carries the substitution reason.
- Adopt carrying `""` → default, adopted, no substitution reason.
- Adopt carrying `"banana"` → default, adopted, no error.
- Round trip through real tmux under `-tags tmux`.
