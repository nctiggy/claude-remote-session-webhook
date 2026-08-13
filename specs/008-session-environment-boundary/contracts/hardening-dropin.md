# Contract: The hardening override

**Feature**: 008-session-environment-boundary

## Location

A drop-in beside the unit, in the systemd user configuration directory:

```
~/.config/systemd/user/crswd.service.d/10-relax.conf
```

Composed from `HOME` and a constant, the same way the updater already composes the unit
and record paths — one arrangement both halves agree on beats an environment variable
nothing in this project sets.

## Contents

```ini
[Service]
NoNewPrivileges=false
RestrictSUIDSGID=false
ProtectKernelTunables=false
ProtectSystem=false
```

### `ProtectKernelTunables` is load-bearing and is not optional

**Measured on a real host:**

| Merged properties | Effective `NoNewPrivs` |
|---|---|
| `ProtectKernelTunables=true` | `1` |
| `ProtectKernelTunables=true` + `NoNewPrivileges=false` | **`1`** |
| `ProtectKernelTunables` overridden to `false` | `0` |

`ProtectKernelTunables=true` **implies** `NoNewPrivileges`, and systemd treats that as a
floor: an explicit `NoNewPrivileges=no` in the merged unit does not lower it back. Only
overriding the implying option removes the implication.

An override missing this line produces a service where `sudo` still fails, with nothing
in either file that looks like the cause. That is why FR-015 requires it documented and
why the file itself carries the explanation.

## Ownership

**The operator's, permanently.**

| Actor | May create | May modify | May delete |
|---|---|---|---|
| `install.sh`, first run, answer yes | ✅ once | ❌ | ❌ |
| `install.sh`, any later run | ❌ | ❌ | ❌ |
| The updater | ❌ | ❌ | ❌ |
| The operator | ✅ | ✅ | ✅ |

**It is never given a recorded digest.** The digest record is what marks a file as this
project's to replace; recording this one would license an update to overwrite exactly the
thing it exists to preserve.

## Why this and not an edited unit

An edited unit is permanently one this daemon did not write: never updated, offered a
`.new` at every release that changes it, and reported as such forever. A drop-in leaves
the unit byte-identical to the release's, so the unit stays replaceable **and** the
operator's deviation survives every update. The two facts stop being in tension.

## Consequence for reporting

A unit matching the release, with an override present, is **not** a host matching the
release. The unit describes a file; the override changes what that file produces. The
daemon's account must say both, or it reports a relaxed host as a current one — the
silence milestone 15 existed to end.

## Observable test

- Drop-in absent → `systemctl --user show crswd -p NoNewPrivileges` is `yes`.
- Drop-in present → it is `no`, and `sudo` works inside a session.
- Drop-in present, unit replaced by an update → still `no` afterwards.
- Drop-in present with **only** `NoNewPrivileges=false` → still `yes`, which is the trap
  the documentation must name.
