# Feature Specification: Ship it to someone else

**Feature Branch**: `006-ship-it-to-someone-else`
**Created**: 2026-08-07
**Status**: Draft
**Input**: The last five open issues — #68, #57, #69, #66 and #54.

## Why this is a milestone and not five issues

Four of the five are one piece of work with a real dependency chain:

```
versioning  →  releases  →  installer  →  self-update
```

Each is meaningless without the one before it. A version nothing reports cannot
name a release; a release nobody publishes cannot be installed; an installer with
nothing to install is a script; and self-update without a steady stream of
releases is a button that does nothing. This has been deferred twice, correctly
both times, because it also depends on the configuration file — which now exists.

The fifth (#54) is unrelated polish and is deliberately last.

## The one thing that makes this milestone different

Everything before it changed how the daemon behaves **for its author, on his
machine**. This milestone is entirely about **other people's machines**, and that
changes what "done" means.

Milestone 5 learned this the expensive way twice: a test suite rotted for months
because it only ever ran on one host, and a race was dismissed as flakiness
because the machine that could expose it was not the machine running the tests.

So this milestone's success criteria are written so that **"it worked here" cannot
satisfy them.**

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Know what is running (Priority: P1)

An operator asks the daemon what version it is and gets an answer, from the
command line and from the running process.

**Why this priority**: Nothing else in this milestone can be built on a daemon
that cannot name itself. A release is a version; an update is a change of
version; a rollback is a version you name.

**Independent test**: Run the binary with a version flag. Ask the running daemon.
Both answer the same thing, and it matches what built it.

**Acceptance scenarios**:

1. **Given** a released binary, **When** asked its version on the command line,
   **Then** it prints the version it was built as.
2. **Given** a running daemon, **When** asked over the API, **Then** it reports
   the same version.
3. **Given** a build from an unreleased working tree, **When** asked, **Then** it
   says so rather than claiming a release it is not.

---

### User Story 2 - Releases exist (Priority: P2)

Every merge to the main branch produces a downloadable build, for the two
architectures anyone is likely to run this on, with everything needed to deploy
it and a way to tell it arrived intact.

**Why this priority**: This is the artifact everything downstream consumes.

**Independent test**: Merge something. A release appears, carrying binaries for
both architectures, the deployment files, and checksums that verify.

**Acceptance scenarios**:

1. **Given** a merge to the main branch, **When** it completes, **Then** a release
   is published carrying a version derived from that merge.
2. **Given** a release, **When** its contents are listed, **Then** it carries a
   binary per supported architecture, the service definition, the tunnel example,
   the signing client, and a checksum file.
3. **Given** a downloaded binary, **When** checked against the published checksum,
   **Then** they agree.
4. **Given** a binary from a release, **When** run on a host without a compiler or
   development libraries, **Then** it runs.
5. **Given** more releases than the retention limit, **When** the limit is
   exceeded, **Then** the oldest are removed — never the newest two, and never one
   a pointer still resolves to.

---

### User Story 3 - Install in one line (Priority: P3)

Someone who has never seen this project installs it by running one command, and
is told exactly what to do next.

**Why this priority**: It is the whole point of the milestone. Today the answer
is "clone it and build it", which is fine for the author and wrong for anybody
else.

**Independent test**: On a machine that has never had this software, run the one
line. It lands, it is not running, and the next steps are printed.

**Acceptance scenarios**:

1. **Given** a supported platform, **When** the installer runs, **Then** it
   verifies what it downloaded **before** making anything executable.
2. **Given** an unsupported platform, **When** the installer runs, **Then** it
   refuses and names what is published.
3. **Given** a host with no existing configuration, **When** the installer runs,
   **Then** a configuration file is written, readable only by its owner.
4. **Given** a host with an **existing** configuration, **When** the installer
   runs, **Then** that file is left exactly as it was.
5. **Given** a host with a service definition the operator has edited, **When** the
   installer runs, **Then** that file is not overwritten.
6. **Given** a completed install, **When** it finishes, **Then** the daemon is
   **not running**, and the operator is told what to set and how to start it.

---

### User Story 4 - Update without rebuilding (Priority: P4)

An operator moves a running daemon to a newer published build, or back to an
older one, without a compiler and without ever running something the project did
not publish.

**Why this priority**: It is the highest-consequence surface in the product, and
it is worth stating plainly why: **an updater downloads code from the internet and
runs it as a daemon that executes unsandboxed shells on the operator's host.** The
download is the easy part. Proving that what arrived is what the operator's own
repository published is the whole feature.

**Independent test**: Ask a running daemon to update. It ends up on the new
version. Then corrupt a byte of what it would have downloaded and ask again — it
refuses and stays where it is.

**Acceptance scenarios**:

1. **Given** a newer published release, **When** an update is requested, **Then**
   the daemon ends up running that version.
2. **Given** a named older version, **When** an update is requested for it,
   **Then** the daemon ends up running that version — a rollback is an update you
   name.
3. **Given** downloaded bytes that do not match the published checksum, **When**
   verification runs, **Then** the update is refused, nothing is made executable,
   and the daemon keeps running what it had.
4. **Given** a checksum file whose signature does not verify, **When**
   verification runs, **Then** the update is refused.
5. **Given** a release carrying **no** signature, **When** an update is requested,
   **Then** it is refused. Refusing to update is always safe; updating to
   something unverified is not.
6. **Given** a redirect to a host other than the expected one, **When** fetching,
   **Then** it is refused.
7. **Given** a browser session without the cross-site evidence, **When** it
   attempts an update, **Then** it is refused — both halves of the existing gate
   apply, exactly as they do to destroy.
8. **Given** an update request without a deliberate confirming step, **When** it
   arrives, **Then** it is refused, because it replaces the running binary.

---

### User Story 5 - The rain says something (Priority: P5)

Occasionally, the rain on the dashboard header says something.

**Why this priority**: Unrelated to the rest, costs little, and is deliberately
last.

**Independent test**: Watch the header. Occasionally something appears. It never
interferes with anything, and it does not appear under a reduced-motion
preference.

**Acceptance scenarios**:

1. **Given** the dashboard, **When** the rain runs, **Then** it occasionally
   surfaces a message.
2. **Given** a reduced-motion preference, **When** the page renders, **Then**
   nothing animates and nothing is surfaced.
3. **Given** a screen reader, **When** a message appears, **Then** it is not
   announced — this is decoration and must not interrupt.

---

## Requirements *(mandatory)*

### Versioning

- **FR-001**: The daemon MUST report its version on the command line.
- **FR-002**: The daemon MUST report its version over the API.
- **FR-003**: A build that does not correspond to a published release MUST say so
  rather than claim one.
- **FR-004**: Versions MUST be monotonic and MUST NOT imply a compatibility
  contract this project has not committed to.
- **FR-005**: The version MUST appear in the release and in the names of its
  artifacts, so a downloaded file can be identified without being run.

### Releases

- **FR-006**: Every merge to the main branch MUST produce a published release.
- **FR-007**: A release MUST carry a binary for each supported architecture.
- **FR-008**: A release MUST carry the deployment files an operator needs, not
  only the binary.
- **FR-009**: A release MUST carry a checksum for every artifact.
- **FR-010**: A released binary MUST run on a host with no compiler and no
  development libraries present.
- **FR-011**: Release history MUST be bounded, and pruning MUST NOT remove the two
  most recent, nor any release a pointer still resolves to.

### The installer

- **FR-012**: Installation MUST be a single command requiring no prior knowledge of
  the project.
- **FR-013**: The installer MUST verify what it downloaded **before** anything is
  made executable.
- **FR-014**: The installer MUST refuse a platform it has nothing published for,
  and MUST say what is published.
- **FR-015**: The installer MUST NOT overwrite an existing configuration file.
- **FR-016**: The installer MUST NOT overwrite a service definition the operator
  has modified.
- **FR-017**: A configuration file the installer writes MUST be readable only by
  its owner.
- **FR-018**: The installer MUST NOT start or enable the daemon.
- **FR-019**: The installer MUST print what remains to be done for the daemon to
  work.
- **FR-020**: The installer MUST NOT contain any individual's name or any account
  identifier — it belongs to the project, not to a person.

### Self-update

- **FR-021**: An operator MUST be able to move a running daemon to a published
  version without rebuilding.
- **FR-022**: An operator MUST be able to name the version, which is what makes a
  rollback possible.
- **FR-023**: Downloaded bytes MUST be verified against a published checksum
  **before** the file is made executable.
- **FR-024**: The checksum MUST itself be verified against a signature, because a
  checksum fetched from the same place as the binary proves only that they arrived
  together.
- **FR-025**: An unsigned or unverifiable release MUST be refused.
- **FR-026**: Transport MUST be authenticated, and a redirect to an unexpected host
  MUST be refused.
- **FR-027**: An artifact that is not the expected release asset MUST be refused.
- **FR-028**: A refused update MUST leave the daemon running exactly what it was
  running.
- **FR-029**: An operator MUST be able to trigger an update **from the browser**.
  The product exists to be operated when its operator is not at the machine, and
  an update reachable only from a terminal is an update they cannot apply from
  where they actually are.
- **FR-029a**: The update route MUST carry both halves of the cross-site defence
  and a deliberate confirming step, exactly as destroy does.
- **FR-029b**: Verification MUST NOT be relaxed because the caller came through
  the browser. The signature is the control; the door is not.
- **FR-030**: The signing key MUST never appear in a release artifact, a log, an
  audit record, or a page.

### The rain

- **FR-031**: The rain MAY occasionally surface a message.
- **FR-032**: Nothing MUST be surfaced under a reduced-motion preference.
- **FR-033**: A surfaced message MUST NOT be announced to a screen reader.

### Carried forward

- **FR-034**: Zero third-party dependencies. This binds the signing design: the
  verification a daemon performs must be possible with the standard library alone.
- **FR-035**: Every request to every route produces exactly one audit record.
- **FR-036**: No new route weakens the uniform refusal or the uniform not-found.
- **FR-037**: No secret, token, prompt text, pane content, or conversation
  transcript in any log, audit record, or page.
- **FR-038**: Nothing animates under a reduced-motion preference; no state is
  conveyed by colour alone; every control is keyboard-operable with a visible
  focus indicator.

## Success Criteria *(mandatory)*

**These are deliberately written so that "it worked on the author's machine"
cannot satisfy them.** That is the correction milestone 5 earned twice.

- **SC-001**: A released binary runs on a host that has never had a compiler, a
  development library, or this project's source — verified on such a host, not
  reasoned about.
- **SC-002**: Installation on a machine that has never seen the project completes
  in a single command and under two minutes, ending with the daemon **not
  running** and the next steps printed.
- **SC-003**: An install run twice in a row leaves the second run's configuration
  byte-identical to the first run's, including any edits made between them.
- **SC-004**: 100% of updates whose bytes do not match the published checksum are
  refused, with the daemon still running its previous version afterwards.
- **SC-005**: 100% of updates whose checksum signature does not verify are
  refused.
- **SC-006**: An update triggered from a browser without the cross-site evidence,
  or without the confirming step, is refused — each condition verified
  independently.
- **SC-007**: A daemon can be moved to a named older version and back, ending on
  the version named each time.
- **SC-008**: Every artifact in a release verifies against its published checksum.
- **SC-009**: The signing key appears in no release artifact, no log, no audit
  record and no page, verified by searching all of them.
- **SC-010**: Release history never exceeds the retention limit, and the two most
  recent are never removed.
- **SC-011**: Every route added here produces exactly one audit record per
  request.
- **SC-012**: `go.sum` remains absent.
- **SC-013**: No individual's name or account identifier appears in the installer.

## Key Entities

- **Version**: What a build calls itself. Monotonic, deliberately not a
  compatibility promise, and the same string whether asked of a binary on disk or
  a daemon in memory.
- **Release**: A published set of artifacts for one version — binaries, deployment
  files, checksums, and a signature over those checksums.
- **Checksum file**: What proves an artifact arrived intact. Worthless on its own,
  because it travels with what it describes; meaningful only signed.
- **Signature**: What proves the checksum file came from this project rather than
  from whoever served it. The one artifact whose absence must stop an update.
- **Installer**: A script that puts a verified binary and unmodified deployment
  files on a host, and then stops.

## Assumptions

- **A checksum without a signature proves only co-arrival.** Both are fetched from
  the same place, so an attacker who can serve one can serve the other. This is
  why FR-024 exists and why FR-025 refuses rather than degrades.
- **Refusing to update is always safe.** The daemon keeps running what it has,
  which is a known quantity. This asymmetry is why every failure mode here is a
  refusal.
- **The browser door is the right door for this, and issue #66 was wrong to
  exclude it.** Its reasoning was that an updater runs downloaded code as a daemon
  that executes unsandboxed shells — true, and it does not distinguish the doors.
  An attacker holding the dashboard can already create a session in an approved
  root running a permission-skipping assistant, which is code execution on the
  host. An update route grants them the ability to install **a release this
  project signed**, which is strictly less. The boundary was already crossed; the
  signature, not the door, is what bounds the damage.
- **A downgrade is the one attack this opens**, and it is accepted for the same
  reason: an attacker who could perform it already has a shorter path to the same
  outcome.
- **The operator's files are the operator's.** The installer writes a
  configuration only when there is none, and never touches a service definition
  that has been edited — the same rule the configuration file already follows.
- **Two architectures cover the realistic hosts**: a workstation or server, and a
  small ARM board.
- **The version is stamped at build time**, so a build outside the release process
  can be honest about not being a release.

## Out of Scope

Deliberately not in this milestone, so no task wanders into them:

- **Auto-recovery of a crashed session** (#95). Still unspecified on purpose: it
  collides with the existing rule that refuses to resume where "the last
  conversation in this directory" could be another session's, and the operator
  asked to think about it further.
- **Editing settings from the browser.** The read-only view is correct and no
  mutating verb is registered on that page at all.
- **Windows, and any package manager** — apt, brew, nix. A tarball and an install
  script, nothing more.
- **Multi-user support**, the device-code login relay, and the companion skill.
- **Any change to milestone 1's signing procedure, its six operations, or the
  audit record shape.** The update path is new; the API it sits beside is not.
- **Automatic updating.** This milestone makes an update possible and verifiable.
  A daemon that updates itself on a schedule, without anyone asking, is a
  different decision and a larger one.
