# Implementation Plan: Adopting a hand-written unit

**Branch**: `010-adopt-the-unit` | **Date**: 2026-08-14 | **Spec**: [spec.md](spec.md)

## Summary

Two subcommands, `crswd unit check` and `crswd unit adopt`, mirroring the
`config check` / `config migrate` pair an operator already knows. The adopt path
moves an inline hardening relaxation into the drop-in, installs the unit an
update already left waiting, and records its digest — after three checks, any of
which refuses without writing anything.

## Technical Context

**Language**: Go 1.25, standard library only.
**Storage**: files `install.sh` already defines — the unit, its `.d` directory,
and `~/.local/share/crswd/crswd.service.sha256`. No new location.
**Testing**: `go test ./...`, plus `-tags quickstart` for the command as a real
operator runs it.
**Constraints**: writes a file systemd executes under. Nothing fetched; the bytes
installed are the ones an update already verified and placed.

## Constitution Check

Constitution 2.0.0.

| Principle | Status | Note |
|---|---|---|
| I — Security is a gate | **PASS** | Writes hardening, so the refusals are the feature. FR-011 and FR-015 are the ones that matter: the drop-in may only contain settings the current unit already grants. |
| II — Unknowns surfaced | **PASS** | No markers. |
| III — Verifiable | **PASS** | Every refusal has a test, and the round trip is asserted as effective-hardening equality rather than as file contents. |
| IV — Smallest correct change | **PASS** | No change to install.sh's rules, no change to the updater's standing logic. One new file in `internal/updater`, one new subcommand. |
| VI — Blast radius | **PASS, and this is the paragraph to read** | It writes a drop-in that permits `sudo` inside sessions — a path from an authenticated request to root. **It widens nothing**: every line it writes is one the current unit already grants, checked per setting, and a unit that does not grant them gets no drop-in. What becomes reachable is not a privilege; it is the *updatability* of a host that had traded it away. |
| VII — Design system | **N/A** | No page. The journal banner gains one sentence. |

**Widening declared**: none. The adoption is a relocation. The check that makes
that true rather than aspirational is FR-015, and it is implemented as: derive the
drop-in from the current unit's own relaxations, never from a constant, and refuse
on any relaxation the drop-in has no line for.

## Phase 0 — Decisions

### D1 — Where the replacement bytes come from

**The `crswd.service.new` already beside the unit.** It is what an update
verified and placed, it is the file the banner already tells the operator to
diff, and using it means adoption needs no network and no second verification
path. A host with no waiting unit has nothing to adopt, which is FR-003.

*Rejected*: fetching the release. A second delivery channel is a second thing to
verify and the one that gets verified less — the same argument `install.sh`'s
`write_dropin` makes about the drop-in itself.

### D2 — What "the same hardening" means, and how it is checked

systemd's merge is: the unit, then `<unit>.d/*.conf` over it. So the property to
preserve is the **effective value of each hardening setting**, not the text.

For each setting the drop-in can express — `NoNewPrivileges`,
`RestrictSUIDSGID`, `ProtectKernelTunables`, `ProtectSystem` — compare the
current unit's effective value against the waiting unit's:

| Current | Waiting | Meaning | Action |
|---|---|---|---|
| relaxed | hardened | the operator's edit | drop-in line |
| same | same | nothing to carry | none |
| hardened | relaxed | the release relaxed it | none; the release's value stands |

Any *other* directive where the current unit is more permissive than the waiting
one and which the drop-in cannot express is FR-011's refusal.

**Absent means systemd's default, not "unset".** The motivating host relaxes by
*commenting the line out*, so the values being compared are the effective ones
after defaults are applied. Reading absence as "no opinion" would find no
relaxation on the very host this exists for.

### D3 — What to do about environment assignments the waiting unit drops

The current unit assigns `Environment=CRSW_*`; the waiting one comments them out
(that is #137). Dropping an assignment is safe exactly when the daemon would load
the same value without it — which `internal/config` can answer, because
precedence is `environment > file > default` and the file and the defaults are
both readable here.

So per assignment the waiting unit does not make: resolve the key from the
configuration file and the built-in default. Equal to the assigned value → the
assignment is a no-op and may be dropped. Different → FR-012's refusal, naming the
key and telling the operator to write it into their configuration file.

**Empty assignments are always droppable**, and that is not a special case but the
same rule: `withFile` treats an empty environment value as unset, so the daemon
already loads such a key from the file.

### D4 — Why the ExecStart check exists at all

The waiting unit names `%h/.local/bin/crswd`; the motivating host runs
`%h/bin/crswd`. Installing the waiting unit on that host produces a service that
will not start — the failure this feature would be most embarrassing to cause.

So: refuse unless the path the waiting unit names exists and is executable, and
say what to do (re-run the installer, which puts the binary there). It is a
refusal rather than a rewrite because a unit adoption edited is a unit with no
recorded digest again, which is the whole problem returning by another door.

### D5 — Two commands rather than a flag

`crswd unit check` and `crswd unit adopt`, because `crswd config check` and
`crswd config migrate` already exist and mean exactly these two things. An
operator who has run one knows what the other does.

*Rejected*: `--dry-run`. It is the same thing spelled a way this repository does
not spell it.

### D6 — Not a dashboard action

Replacing what systemd executes is a terminal operation. The dashboard already
reports the standing (M15/T004) and the journal already names the waiting file;
both gain the command's name and nothing else. A browser action would be a new
authenticated mutation whose blast radius is the service manager.

## Phase 1 — Design

### New: `internal/updater/adopt.go`

```
type AdoptPlan struct {
    Adoptable bool
    Unit, Waiting, Backup, DropIn, Record string
    Relaxations []Relaxation   // what the drop-in will carry
    Dropped     []string       // environment assignments that change nothing
    Refusals    []Refusal      // why not, when not
}

func (u *Unit) PlanAdoption(cfg ConfigResolver) (AdoptPlan, error)
func (u *Unit) Adopt(plan AdoptPlan) error
```

`PlanAdoption` reads and decides; `Adopt` writes and nothing else. They are split
for the reason every step in this package is split: a function that decides and
writes is one where a refusal added later gets added after the first write.

### Order of writes in `Adopt`

1. The drop-in, if the plan carries relaxations and there is no drop-in already.
2. The backup of the current unit.
3. The unit itself.
4. The record.

Backup before replace, for `MigrateFile`'s reason: the ending where one write
fails is the ending where the operator still has both copies. The record last,
because a record written before the unit is a claim about a file that is not
there yet.

### Files touched

```
internal/updater/adopt.go        NEW — the plan and the writes
internal/updater/unit.go         + the settings the drop-in expresses
cmd/crswd/unit_cmd.go            NEW — `crswd unit check|adopt`
cmd/crswd/main.go                + the subcommand
cmd/crswd/unit.go                + the sentence naming the command
README.md, deploy/README.md      the operator-facing account
docs/fixes-log.md                one line
```

## Complexity Tracking

| Thing | Why not simpler |
|---|---|
| Parsing a systemd unit at all | The alternative is trusting that the operator's edits are the ones we expect, which is what makes FR-011 the difference between a relocation and a grant. The parse is small: `[Section]` headers and `Key=Value`, last assignment wins, which is systemd's own rule for these directives. |
| Resolving configuration to decide about an environment line | The cheaper rule — "drop every assignment" — silently changes what the daemon loads on any host whose unit assigns something its file does not. That is #137 arriving from the other side. |
