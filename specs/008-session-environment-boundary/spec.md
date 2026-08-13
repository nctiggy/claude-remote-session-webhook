# Feature Specification: The boundary between the daemon's environment and a session's

**Feature Branch**: `008-session-environment-boundary`

**Created**: 2026-08-13

**Status**: Draft

**Input**: User description: "The boundary between the daemon's own environment and a session's — two defects that are the same boundary seen from two sides: what a session inherits from the daemon, and where an operator's systemd deviations live so an update never reverts them."

## Why this exists

The daemon and the sessions it starts are two different trust levels wearing the same
environment. A session is `claude --dangerously-skip-permissions` — arbitrary code, by
design, with no permission prompt in front of it. The daemon is the thing that decides
who may start one. Today the second hands the first everything it knows, in both
directions that matter:

- **Outward**, the daemon's own secrets travel into every session it starts.
- **Inward**, an operator who needs to change how systemd runs the daemon has nowhere
  to put that change except the unit file, which makes the unit permanently
  un-updatable.

Both are the same missing boundary. Neither is a new policy question; each contradicts
something this project already committed to.

## User Scenarios & Testing *(mandatory)*

### User Story 1 - A session cannot read the operator's secrets (Priority: P1)

The operator starts a session from the dashboard to work on a repository. That session
runs Claude with permissions skipped, and anything in its environment is one `env` away
from being pane content, model context, and a transcript that leaves the host. The
operator expects the credentials that protect the daemon itself to be absent from it.

**Why this priority**: It is the only item here that is a live credential exposure, and
it contradicts a non-negotiable this project already wrote down. `docs/security.md`
states "Session output is secret. Captured pane content can contain anything on the
host — keys, tokens, customer data. Never log it, never ship it to telemetry, never
include it in an error message." A secret sitting in the session's own environment
makes that discipline unenforceable from the inside. The shared secret is layer-2
authentication: whoever holds it can sign valid API requests against the daemon.

**Independent Test**: Start a session and inspect its environment. It delivers value on
its own: no part of Story 2 or 3 is needed for the exposure to be closed.

**Acceptance Scenarios**:

1. **Given** a daemon configured with a shared secret and Cloudflare Access values,
   **When** the operator starts a session and inspects its environment,
   **Then** none of the settings the daemon classifies as secret appears in it.
2. **Given** the same daemon, **When** the operator inspects a session's environment,
   **Then** no daemon configuration variable appears in it either.
3. **Given** a session with a scrubbed environment, **When** the operator runs their
   normal work in it, **Then** the session still finds its home directory, its shell,
   its terminal type and the commands on its path.
4. **Given** an operator who needs one extra variable passed through, **When** they name
   it in their configuration file, **Then** the session receives it — **and when** they
   name a secret there instead, **Then** the daemon refuses rather than passing it.

---

### User Story 2 - Needing sudo in a session does not cost the operator every future update (Priority: P2)

An operator discovers that `sudo` does not work inside a session. The only place to fix
that is the systemd unit, so they edit it. From that moment the unit is one this daemon
did not write: it is never replaced, every release that changes the unit offers a
`.new` beside it forever, and the settings page and journal say so permanently. The
operator wanted one capability and paid for it with the unit's updatability.

**Why this priority**: It is the defect the operator hit in practice, and it makes the
whole "an update carries the unit" design unreachable for the hosts most likely to need
it. It is second only because it costs maintenance and clarity rather than a credential.

**Independent Test**: Install onto a host, answer the prompt, confirm `sudo` works in a
session and the unit is still byte-identical to the shipped one. Testable with Story 1
absent.

**Acceptance Scenarios**:

1. **Given** a fresh install, **When** the installer asks whether elevated privileges
   are needed inside sessions and the operator declines, **Then** the host runs the
   shipped unit with its hardening intact and no override file exists.
2. **Given** a fresh install, **When** the operator accepts, **Then** `sudo` works
   inside a session **and** the unit file is still byte-identical to the one the
   release shipped.
3. **Given** a host with that override in place, **When** a release ships a changed
   unit and the daemon updates, **Then** the unit is replaced and the operator's
   override still applies afterwards.
4. **Given** an installer run with no terminal attached, **When** it reaches the
   question, **Then** it proceeds as though the answer were no.
5. **Given** a host whose effective hardening differs from the shipped unit's,
   **When** the operator reads the daemon's own account of the unit, **Then** it says
   so rather than reporting the host as simply current.

---

### User Story 3 - An operator already holding a hand-edited unit can get back onto the supported path (Priority: P3)

An operator who edited their unit before any of this existed has a working host and no
route back. They need a written procedure that moves their deviations into the
supported place, puts the shipped unit back, and hands the file over to the daemon —
including what that handover does and does not mean.

**Why this priority**: It is documentation over a mechanism Stories 1 and 2 create, and
it serves the hosts that already exist rather than the ones installed next. Valuable,
but it cannot be written before the mechanism it describes.

**Independent Test**: Follow the written procedure on a host with a hand-edited unit and
end with a vanilla unit, preserved behaviour, and a daemon that reports the unit as its
own.

**Acceptance Scenarios**:

1. **Given** a host with a hand-edited unit, **When** the operator follows the
   documented migration, **Then** the capabilities they edited it for still work and
   the daemon reports the unit as one it wrote.
2. **Given** an operator reading that documentation, **When** they relax only the
   most obvious privilege setting, **Then** the documentation has already told them why
   that alone will not work.

---

### Edge Cases

- **An operator relaxes only the obvious setting.** Turning off the no-new-privileges
  guard alone leaves it on, because another hardening option implies it. Verified on a
  real host: relaxing that option alone still yields a locked-down process, and the
  implying option must be relaxed too. Anyone who gets this wrong sees `sudo` fail with
  nothing in the file that looks like the cause.
- **A session that genuinely needs an inherited variable.** Agent forwarding, a
  proxy setting, or a language toolchain's home may be in the daemon's environment
  today and silently relied upon. Removing it breaks a workflow with no obvious cause.
- **Sessions already running when the change lands.** They were started by the previous
  daemon and still hold the old environment; nothing about them changes until they are
  recreated.
- **An operator names a secret in the pass-through list**, deliberately or by copying a
  line, and would otherwise re-open the exposure the feature exists to close.
- **The installer runs with no terminal** — piped from a download, or from automation —
  and cannot ask a question.
- **An operator accepts the prompt and later wants it gone**, or accepts it twice by
  re-running the installer.
- **A host with the override present reads as fully current**, because the unit really
  is the shipped one, while the process it produces is not the one the unit describes.

## Requirements *(mandatory)*

### Functional Requirements

**The session boundary**

- **FR-001**: The daemon MUST construct the environment it gives a session explicitly,
  rather than passing on the environment it was started with.
- **FR-002**: No value the daemon classifies as secret may appear in a session's
  environment. That existing classification MUST be reused rather than restated, so the
  configuration-file refusal, the settings page, the edit form and this boundary cannot
  disagree about what a secret is.
- **FR-003**: No daemon configuration variable may appear in a session's environment.
  The daemon's configuration is not a session's business, and leaving it there also
  makes the daemon's own test suite fail when run from inside a session.
- **FR-004**: A session MUST still receive what it needs to be a working shell: at
  minimum its home directory, path, shell, user, terminal type and locale.
- **FR-005**: The set of variables a session receives MUST have exactly one definition,
  with a test that fails when a new secret-bearing setting is added and not accounted
  for.
- **FR-006**: The operator MUST be able to name additional variables to pass through,
  in their configuration file.
- **FR-007**: The daemon MUST refuse to pass through any variable that FR-002 excludes,
  and say so at startup rather than silently dropping or silently honouring it.

**The unit boundary**

- **FR-008**: The unit a release ships MUST keep its hardening. No install may reduce
  it without the operator saying so.
- **FR-009**: The installer MUST ask, at install time, whether elevated privileges are
  needed inside sessions, and write the override only on an affirmative answer.
- **FR-010**: With no terminal attached, the installer MUST proceed as though the
  answer were no.
- **FR-011**: The override MUST relax the implying hardening option as well as the
  obvious one, because relaxing the obvious one alone has no effect.
- **FR-012**: Neither the installer nor the updater may create, modify or remove the
  override directory or its contents on any subsequent run.
- **FR-013**: The daemon's account of this host's unit MUST state when an override is
  in effect, so that a host running relaxed hardening is never reported as simply
  matching the release.
- **FR-014**: Re-running the installer on a host that already has an override MUST NOT
  duplicate it or silently change the operator's answer.

**Documentation**

- **FR-015**: The deployment documentation MUST state what taking the override does and
  does not hand over, and MUST name the implying-option trap from FR-011 explicitly.
- **FR-016**: A migration procedure MUST exist for a host whose unit was hand-edited
  before this feature, ending with a shipped unit the daemon reports as its own.

### Key Entities

- **Session environment**: the set of variables handed to a newly started session.
  Derived, never inherited wholesale; composed of a fixed base plus the operator's
  named pass-throughs, minus anything classified secret.
- **Hardening override**: an operator-owned systemd drop-in holding this host's
  deviations from the shipped unit. Written at most once by the installer, never
  touched again by any automated path, and the reason the unit itself can stay
  replaceable.
- **Unit standing**: what the daemon can say about this host's unit. Gains a new fact —
  whether an override is changing what the unit actually produces.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: Zero of the operator's secrets are reachable from inside a session. An
  operator can confirm this in one command from a session, and the answer is empty.
- **SC-002**: An operator who needs elevated privileges inside sessions keeps them
  across every subsequent update without editing a unit file even once.
- **SC-003**: No install results in a host that can reach root from a session unless
  the operator answered yes to a question that named that consequence.
- **SC-004**: A host whose effective hardening differs from the shipped unit's can be
  identified as such from the daemon's own reporting, without inspecting files by hand.
- **SC-005**: The project's own test suite passes when run from inside a session it
  started. It does not today.
- **SC-006**: An operator holding a hand-edited unit can reach a fully supported state
  by following written steps, with no capability lost along the way.

## Assumptions

- **The session environment is built as an allowlist, not a denylist.** Excluding
  known-bad names would leave everything else in the daemon's environment flowing
  through, and would need editing every time a new secret appears — the failure mode
  being an exposure nobody notices. An allowlist fails closed, which is what
  `docs/security.md`'s fourth non-negotiable already requires of the auth path. FR-006
  exists because a strict allowlist would otherwise break a workflow relying on an
  inherited variable, and FR-007 keeps that escape hatch from becoming the hole.
- **The path a session gets is the one it gets today.** Scrubbing the environment is
  not an occasion to change which commands a session can find; the existing value is
  carried across rather than recomposed, so `claude` remains reachable exactly as now.
- **Sessions running at upgrade are left alone.** They are existing windows the daemon
  adopts, and killing an operator's work to close an exposure in it would cost more
  than the recreate they can do themselves. The documentation says so.
- **The shipped unit stays hardened by default, and this was a decision rather than an
  omission.** Shipping the relaxation would widen the blast radius on every install from
  "arbitrary code as the operator" to "a path to root", behind auth alone, reachable
  through a tunnel. Constitution Principle VI is NON-NEGOTIABLE and requires an explicit
  justification naming what becomes reachable; no such justification holds here, and the
  operator chose the prompt instead.
- **The drop-in mechanism is verified, not assumed.** Milestone 14 recorded it as
  "probably the better answer" and explicitly unverified. It has since been tested on a
  real host: the override works, and it works only when the implying hardening option is
  relaxed alongside the obvious one. FR-011 exists because of that test.
- **The updater's existing accommodation for a non-standard binary location stays.**
  Migration moves a host onto the shipped unit's path, but removing the accommodation
  would strand any host mid-migration.
- **The override is never given a recorded digest.** Recording it is what would license
  an update to replace it, which is the opposite of why it exists.

## Out of Scope

- **The unit's inline configuration assignments shadowing the operator's configuration
  file.** Fixed separately in PR #137, which moved eight assignments behind comments and
  added a guard against reintroducing them. This feature must not redo or revisit it.
- **Changing the precedence chain itself.** `environment > file > default` stays as it
  is; FR-003 removes the daemon's variables from a session's environment and does not
  touch how the daemon reads its own.
- **Sandboxing the session.** Bounding what a session can reach beyond the environment
  boundary — filesystem, network, capabilities — is the allowlisted-roots control and
  its own separate question.
- **The migration running in the old binary**, recorded as an open spec question at the
  close of milestone 15.
