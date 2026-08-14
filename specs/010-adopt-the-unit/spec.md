# Feature Specification: Adopting a hand-written unit

**Feature Branch**: `010-adopt-the-unit`

**Created**: 2026-08-14

**Status**: Draft

**Input**: "Why can't the installer add the drop-in and overwrite crswd.service? Or why can't the binary handle that?"

## Why this exists

Milestone 15's predecessor (#138) built the mechanism that makes a systemd unit
updatable: put the operator's hardening relaxation in a drop-in, leave the unit
byte-identical to the release's, and the two facts stop being in tension.

**It shipped only for new installs.** The one host that motivated it — the host
that publishes these releases — has a hand-edited unit with the relaxation
inline, no recorded digest, and no drop-in directory at all. So:

- `install.sh` will not replace that unit, correctly: there is no record that this
  project wrote it, and *"absence of evidence that this project wrote a file is
  not permission to replace it"* (`internal/updater/unit.go:33`).
- `install.sh` will not write the drop-in either, because `ask_about_sudo` returns
  early when there is no `/dev/tty` and otherwise asks a yes/no question intended
  for a fresh install. It has **no branch for "this unit already carries the
  relaxation inline"**.

The result is a host permanently in the state #138 exists to end: every release
that changes the unit leaves a `crswd.service.new` beside it, the daemon says so
on every start, and nothing on the host can ever resolve it. The operator's only
route is to hand-edit again, which is what cost the updatability in the first
place.

**Nothing prevents fixing this.** On the motivating host the drop-in grants
exactly what the inline edits grant — `NoNewPrivileges`, `RestrictSUIDSGID`,
`ProtectKernelTunables` and `ProtectSystem` all false, `ProtectControlGroups`
untouched. Moving the relaxation from one file to another is a **relocation, not a
grant**: the same privileges, expressed where an update can survive them.

## What this is not

It is **not** a relaxation of the ownership rule. A unit this project did not
write is still never replaced *silently*, by an update or by an install. What is
added is a third option beside "replace it" and "leave it alone": **offer to adopt
it**, on the operator's explicit command, having checked that adopting changes
nothing about how this host runs.

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Finding out whether adoption is possible (Priority: P1)

The operator runs one command and is told, in terms they can act on, whether
their hand-written unit can be brought under management, and exactly what would
change if it were.

**Why this priority**: it is the whole feature seen from outside, and it is
worth having even if the operator never runs the second command. A host that
cannot be adopted should say why, so the operator can fix the reason.

**Independent Test**: run it against a hand-edited unit and read the report; run
it against a host with no offer waiting and read that.

**Acceptance Scenarios**:

1. **Given** a hand-edited unit and a newer unit waiting beside it, **When** the
   operator asks, **Then** they are told whether it can be adopted, and for each
   difference between the two units what would become of it.
2. **Given** a difference the adoption cannot reproduce, **When** the operator
   asks, **Then** that difference is named, along with what to do about it.
3. **Given** no newer unit waiting, **When** the operator asks, **Then** they are
   told there is nothing to adopt rather than that adoption failed.
4. **Given** a unit this project already wrote, **When** the operator asks,
   **Then** they are told it is already managed and nothing is proposed.

### User Story 2 - Taking the offer (Priority: P1)

The operator runs the adopt command. Their relaxation moves into a drop-in, the
unit becomes the release's, and every future release lands on this host by itself.

**Why this priority**: it is the request. Story 1 without it is a better error
message for a problem that remains unsolvable.

**Independent Test**: adopt on a host whose unit is relaxed inline, then confirm
the unit is byte-identical to the release's, the drop-in grants what the old unit
granted, and the daemon still starts.

**Acceptance Scenarios**:

1. **Given** an adoptable host, **When** the operator adopts, **Then** the unit
   becomes byte-identical to the waiting one and a digest is recorded for it.
2. **Given** a unit that relaxed hardening inline, **When** the operator adopts,
   **Then** a drop-in granting the same settings is written, and the effective
   hardening after the adoption is what it was before.
3. **Given** an adoption, **When** it completes, **Then** the unit it replaced is
   kept, and the operator is told where it is and how to go back.
4. **Given** an adoption, **When** it completes, **Then** the operator is told the
   two commands that put it into effect, because a file on disk is not a running
   service.
5. **Given** a host the checks refuse, **When** the operator adopts, **Then**
   nothing on the host is written at all.

### User Story 3 - Being told the command exists (Priority: P2)

An operator who has never read the documentation learns that adoption is possible
from the place they already read: the startup banner that has been telling them
about the waiting unit.

**Why this priority**: the feature is worth little if the people it is for do not
know it is there. It is P2 because it is one sentence on an existing message.

**Independent Test**: start a daemon on a host with an adoptable unit and read the
journal.

**Acceptance Scenarios**:

1. **Given** a unit that could be adopted, **When** the daemon starts, **Then**
   the banner naming the waiting file also names the command that takes it.
2. **Given** a unit that could not be adopted, **When** the daemon starts, **Then**
   the banner does not offer a command that would refuse.

### Edge Cases

- The waiting unit and the current one are byte-identical — nothing to do, and
  the record should still be written so the host becomes managed.
- A drop-in already exists. It is the operator's and is never overwritten; the
  adoption must check it grants what is needed rather than assume it.
- The binary the waiting unit names is not on this host, which is the motivating
  host's own case: it runs from `~/bin/crswd` and the release's unit names
  `~/.local/bin/crswd`.
- The current unit assigns environment variables the waiting one does not. Each
  either changes nothing when dropped, or is a setting the operator would silently
  lose.
- A unit that relaxes something the drop-in has no line for.
- The unit is not writable, or its directory is not.
- Adoption is run twice.
- The daemon is running while the adoption happens.

## Requirements *(mandatory)*

### Functional Requirements

**Reporting**

- **FR-001**: The daemon MUST provide a command that reports whether this host's
  unit can be adopted, without changing anything.
- **FR-002**: The report MUST name every difference between the current unit and
  the waiting one, and say what adoption would do about each.
- **FR-003**: A host with nothing to adopt — no waiting unit, or a unit already
  managed — MUST be told that plainly, and MUST NOT be reported as a failure.

**Adopting**

- **FR-004**: The daemon MUST provide a command that performs the adoption.
- **FR-005**: Adoption MUST leave the unit byte-identical to the waiting one and
  record its digest, so that later releases replace it as they would any unit this
  project wrote.
- **FR-006**: Adoption MUST reproduce the current unit's hardening relaxations in
  a drop-in, so the host runs afterwards under exactly the hardening it ran under
  before.
- **FR-007**: Adoption MUST keep the unit it replaced, and MUST tell the operator
  where it is.
- **FR-008**: Adoption MUST tell the operator what to run to put it into effect. A
  file on disk is not a running service, and a daemon that reported success while
  the old unit was still loaded would be reporting a fact about a file as a fact
  about a host.
- **FR-009**: An existing drop-in MUST NOT be overwritten. It is the operator's,
  and adoption must verify it grants what is needed rather than replace it.

**Refusing**

- **FR-010**: Adoption MUST refuse unless the binary the waiting unit names exists
  and is executable on this host.
- **FR-011**: Adoption MUST refuse if the current unit relaxes any hardening
  setting the drop-in does not express.
- **FR-012**: Adoption MUST refuse if dropping an environment assignment the
  current unit makes would change the configuration this daemon loads.
- **FR-013**: A refusal MUST name what it refused on and what the operator can do
  about it.
- **FR-014**: A refusal MUST write nothing. A partly-adopted host is worse than an
  unadopted one, because the operator has been told which of the two it is.
- **FR-015**: Adoption MUST NOT widen what the host permits. Every setting it
  writes into the drop-in MUST be one the current unit already grants.

**Provenance**

- **FR-016**: Nothing here may make a unit this project did not write replaceable
  *without the operator's command*. The existing rule — no record means leave it
  alone — is unchanged for installs and updates.
- **FR-017**: The record adoption writes MUST be the one `install.sh` and the
  updater already read, so that all three agree about what is managed.

**Telling the operator**

- **FR-018**: The startup banner that names a waiting unit MUST also name the
  adopt command, and only where adoption would be granted.

### Key Entities

- **Unit standing**: unchanged — absent, current, ours, theirs. Adoption is what
  moves a host from *theirs* to *ours*, and it is the only thing that may.
- **Waiting unit**: the `crswd.service.new` an update left beside the operator's
  own. It is the verified bytes a release published, already on the host, and it
  is what adoption installs.
- **Drop-in**: the operator's hardening override. Written by adoption when there
  is none; read and checked when there is.
- **Unit record**: the digest that says a unit is this project's to replace.

## Success Criteria *(mandatory)*

- **SC-001**: A host whose unit was hand-edited for sudo can be brought under
  management with one command, and every later release updates its unit without
  the operator touching a file.
- **SC-002**: The hardening in effect on that host is identical before and after —
  same four settings relaxed, nothing else changed.
- **SC-003**: The configuration the daemon loads is identical before and after.
- **SC-004**: A host that cannot be adopted is told which difference stopped it,
  and nothing is written.
- **SC-005**: No install and no update gains the ability to replace a unit it did
  not write.
- **SC-006**: An operator who reads only the journal learns that the command
  exists.

## Assumptions

- The waiting unit is trustworthy: it was verified when the update placed it, and
  adoption installs bytes already on the host rather than fetching anything.
- The motivating host's inline relaxations are exactly the drop-in's four
  settings. Verified by inspection; FR-011 is what makes the general case safe.
- Adoption is a terminal command rather than a dashboard action, following
  `config check` / `config migrate`: replacing what systemd executes is not
  something to reach through a browser, and the existing pair is the precedent an
  operator already knows.
- **Dependency**: `docs/security.md` governs FR-006, FR-011 and FR-015 — this
  writes a file that decides the host's hardening.
- **Dependency**: constitution Principle VI governs FR-015 and FR-016. This
  feature widens nothing; it relocates a widening the operator already took.
