# Quickstart: Validating the session/daemon environment boundary

**Feature**: 008-session-environment-boundary
**Date**: 2026-08-13

How to prove this feature works end to end. Each scenario maps to requirements in
[spec.md](./spec.md) and to a contract in [contracts/](./contracts/).

## Prerequisites

- `tmux` installed (3.x; the reference host is 3.4)
- A systemd user manager with lingering enabled
- `go` toolchain for the suites
- A daemon configured with a shared secret — the exposure cannot be demonstrated without
  one to expose

**The port matters.** Two `quickstart` cases bind `127.0.0.1:8765`, which a deployed
daemon holds. Stop it or accept that those two cases cannot run; the rest take their own
port.

---

## Scenario 1 — A session cannot read the operator's secrets (FR-001..FR-005, SC-001)

**Contract**: [session-environment.md](./contracts/session-environment.md)

```bash
# From inside a session the daemon started:
env | grep -c '^CRSW_'
```

**Expected**: `0`.

**Before this feature** the reference host returned **20**, including
`CRSW_SHARED_SECRET`.

Then confirm the session is still a usable shell:

```bash
echo "$HOME" "$PATH" "$TERM"   # all populated
command -v claude              # still found
```

---

## Scenario 2 — An adopted tmux server is cleaned too (R2, the one that is easy to miss)

This is the scenario a chokepoint-only fix passes and should not.

```bash
# With a daemon running and at least one session. The socket is the listen
# address with '.' and ':' replaced by '-'; take the address from the
# operator's config, NOT from the environment — this feature scrubs
# CRSW_LISTEN out of the very session you would be typing this in.
tmux -L crswd-127-0-0-1-8765 show-environment -g | grep -c '^CRSW_'
```

**Expected**: `0` after the daemon has started once with this feature.

**Why it matters**: the tmux server outlives the daemon. Restart the daemon *without*
killing the server, create a **new** session, and re-run Scenario 1 — it must still
return `0`. On the reference host the server's global table held 20 `CRSW_` variables.

**Known limitation, and it must not be papered over**: sessions that were already
running keep their original environment. A process's environment cannot be changed from
outside. Recreate them.

---

## Scenario 3 — The pass-through escape hatch, and its refusal (FR-006, FR-007)

```bash
# Allowed: a name that is neither secret nor CRSW_
printf 'session_environment = SSH_AUTH_SOCK\n' >> ~/.config/crswd/config
# restart, then from a new session:
echo "$SSH_AUTH_SOCK"     # present
```

Then the refusal:

```bash
printf 'session_environment = CRSW_SHARED_SECRET\n' >> ~/.config/crswd/config
```

**Expected**: the daemon **refuses to start**, naming the offending entry and the
reason. Not a warning, not a silent drop.

---

## Scenario 4 — A fresh install is asked, and defaults safe (FR-008..FR-010, SC-003)

**Contract**: [installer-prompt.md](./contracts/installer-prompt.md)

```bash
# Interactive, decline:
bash install.sh            # answer n (or press enter)
systemctl --user show crswd -p NoNewPrivileges
```

**Expected**: `NoNewPrivileges=yes`, and no `crswd.service.d/` directory.

```bash
# Non-interactive — the automation case:
setsid bash install.sh < /dev/null > /tmp/out 2>&1
```

**Expected**: exits successfully, **no** drop-in written, and the question is not printed
as though it had been answered. `setsid` is what actually removes the controlling
terminal — redirecting stdin alone does not, which is the whole point of R4.

---

## Scenario 5 — Accepting keeps sudo across updates (FR-011, FR-012, SC-002)

```bash
bash install.sh            # answer y
systemctl --user show crswd -p NoNewPrivileges -p ProtectKernelTunables
```

**Expected**: both `no`.

Verify the trap is really handled — an override with only `NoNewPrivileges=false` must
**fail** to grant it:

```bash
systemd-run --user --wait --pipe -q \
  -p ProtectKernelTunables=true -p NoNewPrivileges=false \
  /bin/sh -c 'grep NoNewPrivs /proc/self/status'
```

**Expected**: `NoNewPrivs: 1` — proving `ProtectKernelTunables` is the load-bearing
override.

Then the durability claim:

```bash
# after an update that replaces the unit:
systemctl --user show crswd -p NoNewPrivileges    # still no
sha256sum ~/.config/systemd/user/crswd.service    # matches the release's
```

---

## Scenario 6 — The daemon says when an override is in effect (FR-013, SC-004)

```bash
journalctl --user -u crswd -n 50 | grep -i 'unit'
```

**Expected**: the startup sentence states both that the unit matches the release **and**
that an override is changing what it produces. The settings page says the same thing —
one vocabulary, two renderings, no tie-breaker needed.

---

## Scenario 7 — Re-running the installer is idempotent (FR-014)

```bash
bash install.sh            # answer y
sha256sum ~/.config/systemd/user/crswd.service.d/10-relax.conf
bash install.sh            # not asked again
sha256sum ~/.config/systemd/user/crswd.service.d/10-relax.conf
```

**Expected**: identical digests, one file, no second question.

---

## Scenario 8 — Migrating a hand-edited unit (FR-016, SC-006)

Follow the procedure in `deploy/README.md` on a host whose unit was hand-edited.

**Expected**: capabilities preserved, unit byte-identical to the release's, and the
daemon reports the unit as its own with an override present.

---

## The automated gate

```bash
go build ./... && go vet ./... && go test ./...
go test -tags tmux ./...          # the server-environment reconciliation lives here
go test -tags quickstart ./cmd/crswd
golangci-lint run
```

**SC-005 is the one to watch**: `go test ./...` must pass **when run from inside a
session the daemon started**. It does not today —
`TestEditRefusesAValueThatWouldNotLoad` fails there because ambient `CRSW_MAX_SESSIONS`
beats the config file the test writes. That test passing inside a session is the
end-to-end proof that FR-003 holds.
