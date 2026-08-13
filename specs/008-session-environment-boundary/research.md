# Phase 0 Research: The boundary between the daemon's environment and a session's

**Feature**: 008-session-environment-boundary
**Date**: 2026-08-13

Everything below was verified on a real host rather than reasoned about. Where a
finding contradicts what the feature description assumed, the finding wins and the
contradiction is stated.

---

## R1. Where the daemon hands its environment to a session

**Decision**: Set the child environment at `internal/tmuxctl/exec.go`'s `run`, which is
the single place this package reaches a process.

**Rationale**: Every tmux invocation in the package — `New`, `SendKeys`, `Paste`,
`CapturePane`, `Kill`, `Has`, `List`, `SetOption` — funnels through one
`exec.CommandContext` at `exec.go:389`. `cmd.Env` is never assigned, and a nil `Env`
means "inherit the parent's". So the daemon's whole environment reaches tmux through
one line, and one line closes it.

**Alternatives considered**: Setting the environment per-builder was rejected — eight
call sites is eight chances to add a ninth that forgets. The package already treats
`run` as the chokepoint for the socket argument and for the G204 argument; the
environment belongs in the same place for the same reason.

---

## R2. Fixing `cmd.Env` alone is **not** sufficient — the tmux server outlives the daemon

**This is the finding that changes the design.**

**Verified**: On the live host, `tmux -L crswd-127-0-0-1-8765 show-environment -g`
returns **20 `CRSW_` variables, including `CRSW_SHARED_SECRET`**.

A tmux server takes its global environment from whichever client command started it,
and then keeps it for the server's whole life. The daemon deliberately keeps its
sessions across restarts — startup adoption reclaims them, and `Restart=always` plus
the self-update path mean the daemon process is replaced regularly while the tmux
server is not. So a daemon that only scrubs `cmd.Env` would start clean **only on a
host where the tmux server happened to die**, and every existing deployment would stay
exposed for as long as its server lives, which is indefinitely.

**Verified remediation**, on a throwaway server rather than the operator's:

| Step | Observed |
|---|---|
| Start a server with `CRSW_PROBE_SECRET` in the environment | pane process has `CRSW_PROBE_SECRET=leaky-value` |
| `show-environment -t <session> VAR` | `unknown variable` — the *session* table does not hold it |
| `set-environment -g -u CRSW_PROBE_SECRET` | removed from the global table |
| Create a **new** session on the same server | pane process no longer has it |

Two things this proves, and one it does not:

- The pane process inherits the server's **global** environment, so `show-environment -t`
  is the wrong place to look and would have reported a clean host that was not.
- Removing a variable from the global table is enough to clean **subsequently created**
  sessions, and does not require killing the server or disturbing running sessions.
- It does **not** clean sessions that are already running. A process's environment
  cannot be changed from outside; those panes keep what they were started with until
  they are recreated.

**Decision**: Two mechanisms, not one.

1. `cmd.Env` at the chokepoint (R1), which makes a freshly started server correct.
2. A reconciliation of the running server's global environment against the allowlist,
   performed by the daemon at startup, which makes an **adopted** server correct.

**Alternatives considered**:
- *Kill the tmux server on upgrade* — rejected outright. It destroys the operator's
  running sessions to fix a leak in the ones not yet created, and adoption exists
  precisely so a redeploy does not cost the fleet.
- *`new-session -e VAR=…` per session* — tmux 3.2+, and this host is 3.4, so it is
  available. Rejected as the primary mechanism because it sets rather than removes, and
  would require enumerating every variable to be blanked at every create; the global
  reconciliation is done once per daemon start and leaves the server itself correct.
- *Do nothing about adopted servers and document the recreate* — rejected: it makes the
  fix depend on an operator reading a release note, and the whole point of this feature
  is that a host does not stay quietly wrong.

---

## R3. Allowlist versus denylist

**Decision**: Allowlist, with an operator-named pass-through list that refuses secrets.

**Rationale**: A denylist of `CRSW_*` leaves everything else in the daemon's
environment flowing into an unsandboxed shell, and needs an edit each time a new
secret-bearing variable appears — where the failure mode is an exposure nobody
notices. `docs/security.md`'s fourth non-negotiable already requires the auth path to
fail closed; this is the same argument one process boundary over.

The cost is real and is what the pass-through list answers: an allowlist silently
breaks a workflow that depended on an inherited variable, and the operator has no way
to see why. The escape hatch is closed against re-opening the hole by refusing any key
the daemon classifies as secret (`config.IsSecret` — `shared_secret`,
`access_allowed_emails`, `dashboard_password`), and by refusing loudly at startup
rather than dropping the entry silently.

**Base set**: `HOME`, `PATH`, `SHELL`, `USER`, `LOGNAME`, `TERM`, `LANG`, `LC_*`,
`XDG_RUNTIME_DIR`. `PATH` is carried across with the value the daemon already has
rather than recomposed — scrubbing the environment is not the occasion to change which
commands a session can find, and `internal/config/depcheck.go` already distinguishes
"this daemon's own PATH" from "the PATH a login shell gives a session", so changing it
here would move a decision that file has already reasoned about.

`XDG_RUNTIME_DIR` is in the base set because tmux and the systemd user manager both
use it; omitting it is the most likely way to produce a session that starts and then
behaves strangely.

**Alternatives considered**: A denylist of exactly the three secret keys was rejected
for the reasons above. A denylist of `CRSW_*` was rejected additionally because it
would not have removed anything else the daemon's unit or the operator's login
environment happens to carry.

---

## R4. The installer cannot use `read` from standard input

**Verified**: `install.sh` is `#!/usr/bin/env bash`, and its own documented invocation
at line 4 is:

```
curl -fsSL https://raw.githubusercontent.com/.../install.sh | bash
```

**Under that invocation standard input is the script itself.** A `read` would consume
the script's own remaining bytes, and `[ -t 0 ]` is false even when a human is sitting
at the terminal — so the obvious interactivity test gives the wrong answer for the
project's primary install path, in the direction that silently skips the question.

**Decision**: Read the answer from `/dev/tty`, and treat "cannot open `/dev/tty`" as
the non-interactive case, which answers no.

**Rationale**: `/dev/tty` is the controlling terminal regardless of what stdin was
redirected to, so it is correct for both `curl | bash` and `bash install.sh`. Its
absence is exactly the automation case FR-010 is about.

**Alternatives considered**: `[ -t 0 ]` — wrong answer for the documented install path.
An `--enable-sudo` flag instead of a prompt — rejected because FR-009 requires every
install to be *asked*, and a flag is only found by someone who already knew.

**Note for the plan**: `install.sh` today contains no prompting machinery at all — no
`read`, no `/dev/tty`, no interactivity test. This is new surface rather than an
extension of an existing pattern, so it carries its own test rather than inheriting one.

---

## R5. The drop-in mechanism, and the option that actually matters

**Verified on this host** with `systemd-run --user`:

| Properties | Effective `NoNewPrivs` |
|---|---|
| `ProtectKernelTunables=true` | `1` |
| `ProtectKernelTunables=true` + `NoNewPrivileges=false` | **`1`** — the obvious relaxation alone does nothing |
| `ProtectKernelTunables=true` then `ProtectKernelTunables=false` | `0` |
| All four relaxed (`NoNewPrivileges`, `RestrictSUIDSGID`, `ProtectKernelTunables`, `ProtectSystem`) | `0`, and `/usr/bin/sudo` is mode `-rwsr-xr-x` and usable |

**Decision**: The drop-in must override `ProtectKernelTunables=false` as well as
`NoNewPrivileges=false`, `RestrictSUIDSGID=false` and `ProtectSystem=false`.

**Rationale**: `ProtectKernelTunables=true` *implies* `NoNewPrivileges`, and systemd
treats that as a floor — an explicit `NoNewPrivileges=no` in the merged unit does not
lower it back. Only overriding the implying option removes the implication. An operator
who relaxes the obvious setting alone gets a session where `sudo` still fails with
nothing in either file that looks like the cause, which is why FR-015 requires this be
documented rather than left to be rediscovered.

A drop-in override was also confirmed to work at all: a `.d/10-relax.conf` beside a
throwaway unit changed all four properties as read back by `systemctl --user show`.

This closes the item `ralph/archive/progress-milestone-14.md:447` recorded as "probably
the better answer … this iteration could not verify it on this host".

---

## R6. Where FR-013's new fact goes

**Decision**: Both existing accounts, from one shared vocabulary.

**Rationale**: Milestone 15 deliberately put the unit vocabulary in `internal/updater`
so the settings page and the startup journal could not drift into two accounts an
operator cannot reconcile. FR-013 adds a fact to the same surface —
`cmd/crswd/unit.go`'s `sayWhatBecameOfTheUnit` for the journal, and
`internal/httpapi/settings.go` for the page — and must extend that shared vocabulary
rather than compute the answer twice.

**The fact itself**: a host with a drop-in is not described by its unit. The unit may be
byte-identical to the release's and the process still runs with different hardening, so
"the one this release ships" is true of the file and misleading about the host. The
report needs to say the unit matches *and* that an override is changing what it
produces.

---

## R7. Configuration key for the pass-through list

**Decision**: One new key, following the existing rule.

**Rationale**: `internal/config/file.go:17` states the rule as "a key is its environment
variable minus the `CRSW_` prefix, lower-cased … That is a rule and not a table", so the
key and its variable come as a pair automatically, and it is readable from the
operator's file the same day it is added to `config.go`. No table needs editing.

Because it is a list of *names* and not values, it is not itself a secret and does not
trip the 0600 refusal — but the names it may carry are constrained by FR-007.
