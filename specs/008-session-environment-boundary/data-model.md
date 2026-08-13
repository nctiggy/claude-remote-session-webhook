# Phase 1 Data Model: The boundary between the daemon's environment and a session's

**Feature**: 008-session-environment-boundary
**Date**: 2026-08-13

No database and no persisted records. The entities here are in-memory values and files
on disk, and the rules that matter are the ones about what may and may not appear in
each.

---

## 1. Session environment

The set of variables handed to a session. **Derived, never inherited wholesale.**

| Field | Meaning |
|---|---|
| Base | A fixed list of names every session needs to be a working shell |
| Pass-through | Additional names the operator asked for, from their configuration |
| Value source | The daemon's own environment, read at compose time |

**Base names**: `HOME`, `PATH`, `SHELL`, `USER`, `LOGNAME`, `TERM`, `LANG`, `LC_*`,
`XDG_RUNTIME_DIR`.

### Validation rules

- **V1** — A name the daemon classifies as secret is never included, from any source.
  The classification is the existing `config.IsSecret` predicate, reused (FR-002).
- **V2** — No `CRSW_`-prefixed name is ever included, including ones not currently
  classified secret (FR-003). The daemon's configuration is not a session's business,
  and this is also what lets the project's own test suite pass inside a session.
- **V3** — A base name whose value is absent from the daemon's environment is omitted
  rather than passed as empty. An empty `HOME` is worse than an unset one.
- **V4** — `PATH` carries the daemon's existing value unchanged. Scrubbing is not the
  occasion to change which commands a session can find.
- **V5** — The composed set is the whole environment. There is no merge with the
  parent's, which is what makes V1 and V2 properties of the result rather than of the
  care taken at each call site.

### State

None. Composed fresh per exec; nothing is cached, so a configuration reload cannot leave
a stale set behind.

---

## 2. Pass-through list

An operator-named list of additional variable names, from the configuration file.

| Field | Meaning |
|---|---|
| Key | Follows the existing rule: the environment variable minus `CRSW_`, lower-cased |
| Value | Comma-separated variable **names**, never values |

### Validation rules

- **V6** — An entry naming a secret is a **startup failure**, not a warning and not a
  silent drop (FR-007). The operator is told which entry and why.
- **V7** — An entry naming a `CRSW_` variable is refused on the same terms, so the
  escape hatch cannot reintroduce what V2 removes.
- **V8** — An entry naming a variable absent from the daemon's environment is not an
  error. The operator is describing intent about an environment that may change.
- **V9** — The list holds names, so it is not itself secret-bearing and does not trip
  the configuration file's 0600 refusal.

---

## 3. tmux server global environment

Not this project's data structure, but state this feature must reconcile.

| Property | Meaning |
|---|---|
| Lifetime | The tmux server's, which outlives the daemon process |
| Origin | The environment of whichever client command started the server |
| Effect | Merged into every **newly created** session's pane process |

### Validation rules

- **V10** — After daemon startup, no name violating V1 or V2 remains in the global
  table.
- **V11** — Reconciliation removes names; it never adds or overwrites operator state.
- **V12** — Reconciliation must not disturb running sessions. Removing a name from the
  global table has no effect on already-started panes, which is both why it is safe and
  why V13 exists.
- **V13** — Sessions running before reconciliation retain their original environment
  and **cannot** be cleaned in place. This is a documented limitation, never reported
  as clean.

---

## 4. Hardening override (systemd drop-in)

The operator's deviations from the shipped unit, in the one place nothing automated
touches.

| Field | Meaning |
|---|---|
| Path | A drop-in file under the unit's `.d` directory in the systemd user config |
| Contents | Overrides for `NoNewPrivileges`, `RestrictSUIDSGID`, `ProtectKernelTunables`, `ProtectSystem` |
| Owner | The operator, permanently |

### Validation rules

- **V14** — Must override `ProtectKernelTunables`, not only `NoNewPrivileges`. The
  former implies the latter and systemd treats it as a floor, so the obvious override
  alone is measurably inert (research R5).
- **V15** — Written by the installer at most once, on an affirmative answer only.
- **V16** — Never created, modified or removed by the updater or by any later installer
  run (FR-012, FR-014).
- **V17** — Never given a recorded digest. Recording it is what would license an update
  to replace it, which is the opposite of its purpose.

### State transitions

```
absent ──(operator answers yes at install)──> present
absent ──(operator answers no, or no terminal)──> absent
present ──(any update, any re-install)──> present, unchanged
present ──(operator deletes it by hand)──> absent
```

---

## 5. Unit standing (extended)

The existing report gains one fact.

| Field | Meaning |
|---|---|
| *(existing)* | Whether the unit is absent, the shipped one, this daemon's, or the operator's |
| **Override present** | **NEW** — whether a drop-in is changing what the unit produces |

### Validation rules

- **V18** — A unit byte-identical to the release's, with an override present, must not
  be reported as simply matching the release. True of the file; misleading about the
  host.
- **V19** — The fact is computed once in the shared vocabulary and rendered by both the
  settings page and the startup journal. Two computations could disagree, and a page and
  a journal disagreeing about the same host is a question with no tie-breaker on it.
