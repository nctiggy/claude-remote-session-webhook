# Implementation Plan

**Milestone 15 — Updates that carry the files, not just the binary.**

> *"How do we make it so that the updates also grab or update the systemd unit
> file as well? I feel like the config and systemd files should update as part of
> the updates… values saved as part of the updates but the files updated."*

Seven tasks.

---

## Two files, two different answers

| File | What an update should do | Why |
|---|---|---|
| **config** | Migrate it in place, keeping every value and every comment | The daemon knows every key, and `crswd config migrate` already does exactly this. Nothing calls it. |
| **unit** | **Never overwrite an edited one.** Write the new one alongside and say so. | An edited unit carries decisions. Overwriting reverts them silently, every release. |

**The operator proved the second one in the same session they asked for this.**
They hand-edited their unit to relax `NoNewPrivileges`, `RestrictSUIDSGID` and
`ProtectSystem` so `sudo` works in a session. An update that replaced units would
undo that on every release, and they would have to rediscover it each time.

**The rule that protects them stays.** What changes is the silence around it.

---

## What already exists

`crswd config migrate` — `internal/config/migrate.go` — rewrites a config into the
current schema **line by line**, copying every line it has no reason to touch byte
for byte, spacing and line endings included. Its own comment says why:

> Comments are the reason this format is not JSON: they carry why each bound is
> what it is, and a migration that reproduced the settings and dropped the
> commentary would take away more than it fixed.

It is a manual command. **The updater never runs it.** T001 is wiring, not
invention.

---

## ⚠️ The current failure is silence, and this operator is living it

Their unit has **no recorded hash**, so the installer will never touch it — and
nothing has ever told them. It still carries `ExecStart=%h/bin/crswd`, the path
v0.80 fixed, and no `EnvironmentFile` line at all.

**They are two fixes behind with no way to find out.** That is the defect this
milestone is really about. "Never overwrite" was right; "never mention" was not.

---

## ⚠️ A migration that breaks a config is worse than one that never ran

The daemon refuses to start on a config it cannot load. An update that migrates a
config into something unloadable turns a working daemon into a boot loop, and it
does so **at the moment the operator is least able to look** — mid-update, from a
phone.

So: migrate to a temporary file, **load it and validate it**, and only then move it
into place, keeping the previous one. A migration that does not validate must not
be written at all.

---

## Conventions

- `- [ ]` open · `- [x]` done · `- [!]` blocked (reason in `PROGRESS.md`)
- Priority order is meaningful — the loop always takes the topmost open item.
- **Every task ends green**: `go build ./... && go vet ./... && go test ./... && golangci-lint run`,
  plus `-tags tmux` / `-tags quickstart` where touched.
- **Check the linter is v2 before trusting it** (#26).
- `go.sum` must never appear.
- **AR-008: no refactoring outside the task.**
- **A task is not done when the code exists. It is done when something calls it.**
  This milestone exists because `config migrate` was written and never called.
- **A new guard must be proven by breaking it.**

---

## Tasks

- [x] **T001** 🔒 Run the config migration as part of an update, in `internal/updater/`. Migrate to a temporary file, **load and validate the result**, and only then move it into place — keeping the previous file as a backup. A migration that does not validate is discarded and the update proceeds with the config untouched; **it must never leave the daemon unable to start**. Test: a config missing keys a newer schema adds comes back with them and every existing value intact; a migration that would produce an unloadable file leaves the original in place.

- [x] **T002** 🔒 Ship the unit as a release asset the daemon can compare against. The installer already fetches `crswd.service` and records its hash at `~/.local/share/crswd/crswd.service.sha256`. The updater needs the same file to answer "is the operator's unit the one this release ships?" — reuse the existing asset and the existing checksum path rather than inventing a second delivery.

- [x] **T003** 🔒 On update, decide what to do with the unit, and **never overwrite one this daemon did not write**: recorded hash matches → replace it and re-record; hash differs, or no record exists → **write the new one alongside as `crswd.service.new` and leave theirs alone**. Test all three branches. **The one that matters most**: an operator who relaxed `NoNewPrivileges` still has it relaxed afterwards, and has a `.new` file naming what they are missing.

- [x] **T004** Tell the operator, on the settings page, reusing existing classes — **no new class**. Say which of the three happened: the unit was updated, a newer one is waiting as `.new`, or theirs is current. When one is waiting, name the file and the command to compare it (`diff`) — an operator who cannot see the difference cannot decide, and this daemon's whole update story is that a change is visible before it is taken.

- [x] **T005** Surface the same thing at startup, into the journal. The daemon already warns about an absent identity provider for the same reason: a deployment that is quietly behind looks identical to one that is current, and the journal is where an operator looks when something is wrong.

- [x] **T006** Document it in `README.md` and `deploy/README.md`: what an update does to each of the two files, why the unit is never overwritten when it has been edited, and how to take a `.new` unit when you want it. Say plainly that a hand-written unit — one this installer never wrote — is never replaced and will always produce a `.new`.

- [ ] **T007** Make `crswd config migrate` and the update path share one implementation, if T001 did not already. Two code paths that rewrite an operator's configuration differently is the drift this repository keeps finding; one of them is a command an operator runs and the other runs unattended during an update, which is the worse place to discover a difference.

---

## Out of scope

- **Overwriting an edited unit, under any condition.** The operator's `sudo`
  relaxation is the standing counter-example.
- **Migrating a config the daemon cannot load.** It is refused at startup today
  and that stays; this milestone must not paper over it.
- **A settings-page editor for the unit.** It is a systemd file, not daemon
  configuration, and a route that wrote one would be a route that edits how the
  daemon is launched.
- **#120, #121.** Unchanged. **Q2** is still the operator's to answer.
