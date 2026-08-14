# Feature Specification: Session Lifetime Honesty

**Feature Branch**: `009-session-lifetime-honesty`

**Created**: 2026-08-14

**Status**: Draft — blocked on a constitutional amendment (see Constitutional Impact)

**Input**: User description: "Remove the idle bound from the product entirely; make the per-session 'never expires' switch survive a daemon restart; show the operator the command line a create will run; and let a create continue a dead session's conversation."

## Why this exists

At 00:52:03 UTC on 2026-08-14 a daemon restart re-adopted four running sessions.
At 01:52:03 UTC — sixty minutes later, to the second — the reaper destroyed all
four with the reason *"the session was idle past its idle timeout"*. The operator
had asked for sessions that do not expire, and got sessions that died in an hour.

Two separate faults produced that outcome, and this feature addresses both:

1. **Adoption silently drops the per-session lifetime override.** A session
   created with its absolute deadline switched off comes back from a restart as
   an ordinary 24-hour session. The switch works until the moment it matters.
2. **The idle bound does not describe anything the operator wants bounded.** The
   working pattern is: create a session, leave, come back later. A session that
   is quiet because it is waiting for a human is the normal case. Destroying it
   for that is the feature working as designed and being wrong anyway.

The daemon's idle measurement is not itself broken — it takes the later of the
record's own clock and the host's `#{session_activity}`, so it sees real terminal
output and not merely dashboard traffic. The bound is being withdrawn because it
is the wrong bound, not because it is mis-measured. That distinction matters for
the amendment below: nothing here is being removed to avoid fixing it.

## Constitutional Impact *(must be resolved before implementation)*

Constitution Principle VI (**NON-NEGOTIABLE**) currently reads, in part:

> Every session has an **idle timeout and an absolute lifetime**, enforced by a
> reaper, not by the next request.

User Story 1 removes the idle timeout, and therefore contradicts a
non-negotiable principle. Under Governance, the constitution wins over any spec
or preference; amending it requires a pull request stating what changed, why, and
what it breaks.

**This spec cannot be implemented until Principle VI is amended.** The amendment
must state plainly what the removal costs: after it, the absolute deadline is the
*only* time-based bound on a session, and the fleet's containment rests on
`allowed_roots`, the session cap, verified teardown, the loopback bind, and the
doors. A session created with the absolute deadline switched off is then bounded
by nothing time-based at all — which is precisely the state the operator is
asking for, and precisely the state Principle VI was written to prevent.

The amendment is the operator's to make. It is recorded here as a dependency
rather than assumed.

**Resolved 2026-08-14**: the operator elected to amend Principle VI and to have
the amendment state the cost rather than merely strike two words — the rewritten
principle says what bounds a session once the absolute deadline is switched off,
and what is left holding the fleet when nothing time-based is. Implementation
remains blocked until that amendment is merged.

## User Scenarios & Testing *(mandatory)*

### User Story 1 - A quiet session is not a doomed session (Priority: P1)

The operator creates a session, closes the browser, and does something else for
several hours. Nothing in the daemon counts that silence against the session.
When they come back, it is still there.

**Why this priority**: It is the incident. Every other story in this spec is
worth less if sessions keep dying for being quiet.

**Independent Test**: Create a session, advance time well past sixty minutes with
no interaction of any kind, and observe that the session is still running and
that no destruction was recorded against it.

**Acceptance Scenarios**:

1. **Given** a running session with no activity for many hours, **When** the
   reaper sweeps, **Then** the session is left running and no idleness-related
   audit record is written.
2. **Given** a running session past its absolute deadline, **When** the reaper
   sweeps, **Then** it is destroyed and the recorded reason refers to the
   absolute deadline alone.
3. **Given** an operator viewing the fleet, **When** any session is displayed,
   **Then** no idle countdown, idle status, or idle deadline appears anywhere.
4. **Given** a configuration file or environment carrying a retired idle setting,
   **When** the daemon starts, **Then** it behaves per the retired-setting rule in
   FR-006 rather than silently accepting a key that now governs nothing.

---

### User Story 2 - "Never expires" survives a restart (Priority: P1)

The operator creates a session with its absolute deadline switched off. The
daemon is restarted — a redeploy, a crash, a settings change. The session is
adopted back with its deadline still switched off, and still does not expire.

**Why this priority**: This is the actual defect. It is P1 alongside Story 1
because either one alone still loses the operator's sessions: without Story 1 a
never-expiring session is reaped for idleness anyway, and without Story 2 the
switch remains a promise the daemon breaks at the next restart.

**Independent Test**: Create a session with the deadline switched off, restart
the daemon, and confirm the adopted record still reports that it never expires —
and is still alive after the daemon's default lifetime has elapsed.

**Acceptance Scenarios**:

1. **Given** a session created with its absolute deadline switched off, **When**
   the daemon restarts and adopts it, **Then** the adopted session still has no
   absolute deadline.
2. **Given** a session created with an explicit non-default lifetime, **When** the
   daemon restarts and adopts it, **Then** the adopted session keeps that
   lifetime, measured from its original creation time and not from adoption.
3. **Given** a session started by a build that predates this feature and so
   carries no recorded lifetime, **When** the daemon adopts it, **Then** adoption
   succeeds and the session takes the daemon's configured default — it is never
   skipped, and no value is invented for it.
4. **Given** a session whose recorded lifetime is missing, malformed, or exceeds
   the daemon's current ceiling, **When** the daemon adopts it, **Then** adoption
   succeeds under the daemon's current ceiling rather than honouring an
   unverifiable value, and the substitution is recorded in the audit trail.
5. **Given** a session that outlived its restored deadline while the daemon was
   down, **When** the daemon starts, **Then** it is destroyed rather than adopted,
   as it is today.

---

### User Story 3 - The form shows the command line it will run (Priority: P2)

The operator fills in the create form and can see, updating as they change the
options, the exact command line the session will be started with. They can copy
it.

**Why this priority**: It makes the other options legible — a "continue" checkbox
and a "never expires" checkbox are both easier to trust when the operator can see
what they do. It changes no behaviour, so it ranks below the two correctness
stories.

**Independent Test**: Open the create form, change each option in turn, and
confirm the displayed command line matches what the session is actually started
with.

**Acceptance Scenarios**:

1. **Given** the create form, **When** the operator selects a start command,
   **Then** the exact command line that will run is displayed.
2. **Given** the create form, **When** the operator toggles any option that
   affects the command line, **Then** the displayed command updates to match.
3. **Given** a displayed command line, **When** the session is created, **Then**
   what ran is what was displayed.
4. **Given** the displayed command line, **When** the operator attempts to edit
   it, **Then** they cannot: it is a readout, and the only commands the daemon
   will run remain the operator-configured set.
5. **Given** a session name containing characters that need quoting, **When** the
   preview renders it, **Then** it is shown as text and never interpreted, and
   the quoting shown matches the quoting used.

---

### User Story 4 - Continuing a dead session's conversation (Priority: P3)

A session dies — reaped, crashed, or destroyed — and the conversation inside it
was worth keeping. The operator creates a new session in the same working
directory and asks it to continue where the old one left off, either picking up
the most recent conversation there or choosing a specific earlier one.

**Why this priority**: It recovers value after a loss rather than preventing one.
With Stories 1 and 2 done, the losses it compensates for should become rare.

**Independent Test**: Create a session in a directory with a prior conversation,
choose to continue, and confirm the started session resumes that conversation
rather than beginning an empty one.

**Acceptance Scenarios**:

1. **Given** the create form with a working directory selected, **When** the
   operator asks to continue, **Then** the session starts by resuming the most
   recent conversation in that directory.
2. **Given** a directory holding several prior conversations, **When** the
   operator opens the picker, **Then** they can choose one and the session
   resumes that one specifically.
3. **Given** a directory with no prior conversation, **When** the operator opens
   the form, **Then** continuing is offered in a state that makes plain there is
   nothing to continue, and a create with it selected either starts a fresh
   session or is refused with a reason — never a session that silently ignored
   the request.
4. **Given** a working directory outside the allowlisted roots, **When** anything
   in this story would read or list conversations for it, **Then** it is refused,
   on the same rule that refuses a create there.

---

### User Story 5 - A card that says what a session is (Priority: P3)

Looking at the fleet, the operator can tell at a glance how long each session has
been alive, whether it was started with remote control, and whether it can ever
die.

**Why this priority**: Presentation of facts the daemon already holds. Valuable
for trusting the fleet at a glance, but nothing depends on it.

**Independent Test**: Create sessions covering each combination — remote-control
and plain, expiring and never-expiring — and confirm each card states all three
facts correctly, before and after a daemon restart.

**Acceptance Scenarios**:

1. **Given** any session, **When** its card is shown, **Then** it states how long
   the session has been alive.
2. **Given** a session started with remote control, **When** its card is shown,
   **Then** it is distinguishable from one that was not.
3. **Given** a session whose absolute deadline is switched off, **When** its card
   is shown, **Then** it says so, rather than showing a deadline.
4. **Given** any of the above after a daemon restart, **When** the adopted card is
   shown, **Then** all three facts are unchanged.

---

### Edge Cases

- A session is adopted whose recorded lifetime says "never" but the daemon's
  configured ceiling is no longer unbounded — the operator narrowed the ceiling
  while the daemon was down. The session must not quietly keep a bound the
  current configuration would refuse to grant (FR-011).
- Two daemons, or a daemon and a stale one, adopt against the same tmux server.
  Reconciliation already leaves a session a live store knows about untouched;
  restoring the lifetime must not change that.
- The host answers a request for a session's recorded lifetime with an error
  rather than a value. An error is not an answer, and adopting on one must not
  record a bound on no evidence.
- The operator retires the idle settings but their configuration file still
  carries them from a previous deploy (FR-006).
- A conversation the picker offered no longer exists when the create runs.
- The working directory suggestion list and the conversation list disagree —
  a directory is offered for create but has no readable conversation history.
- A session is created with both "never expires" and "continue" selected, and the
  continued conversation is one from a session that was destroyed for outliving
  its own deadline.

## Requirements *(mandatory)*

### Functional Requirements

**Retiring the idle bound**

- **FR-001**: The daemon MUST NOT destroy a session for inactivity, under any
  configuration.
- **FR-002**: The daemon MUST NOT present idleness anywhere in the dashboard or
  API — no idle status, idle deadline, idle countdown, or last-activity readout
  offered as a reason a session may die.
- **FR-003**: The absolute deadline MUST remain enforced by the reaper, on the
  daemon's own sweep and not on the next request.
- **FR-004**: The reason recorded when the reaper destroys a session MUST name the
  bound that fired, and after this feature the only such bound is the absolute
  deadline.
- **FR-005**: All idle-related configuration MUST be removed from the daemon's
  settings, its documentation, its example configuration, and its deployment
  unit, so that no operator-facing surface describes a control that no longer
  exists.
- **FR-006**: A configuration carrying a retired idle setting MUST NOT be silently
  accepted as though it still governed something. A file carrying one MUST be
  refused at startup, as the daemon already refuses any key that maps to no
  setting, and the operator MUST be pointed at the existing migration path rather
  than left to edit by hand. The configuration schema MUST record that these keys
  were retired, so that a file written against the older schema is recognised as
  out of date rather than merely wrong.

**Restoring the lifetime across a restart**

- **FR-007**: A session's per-session lifetime override MUST be recorded on the
  host at creation, alongside the facts already recorded there.
- **FR-008**: On adoption the daemon MUST restore that override, so a session's
  absolute deadline after a restart is the one it was created with.
- **FR-009**: The absolute deadline MUST continue to be measured from the
  session's original creation time, never from the moment of adoption, so no
  restart can extend a session past its ceiling.
- **FR-010**: A session carrying no recorded override — including every session
  created before this feature — MUST adopt successfully and take the daemon's
  configured default. Adoption MUST NOT be skipped over a missing value, because
  an unadopted session is an unowned unsandboxed shell.
- **FR-011**: A restored override that the daemon's current configuration would
  not grant on a create MUST NOT be honoured. The session MUST be adopted under
  the current ceiling instead, and the substitution recorded.
- **FR-012**: An override MUST be restorable only from a session the daemon owns.
  A value found on a session the daemon did not create MUST NOT influence any
  deadline.
- **FR-013**: The default for a new session is unchanged: the daemon's configured
  lifetime applies, and switching the absolute deadline off remains an opt-in
  that requires both an operator-configured unbounded ceiling and an explicit
  request on the create.

**Showing the command line**

- **FR-014**: The create form MUST display the exact command line the create will
  run, updating as the operator changes any option that affects it.
- **FR-015**: The displayed command line MUST be what actually runs.
- **FR-016**: The display MUST be a readout only. The operator-configured start
  command set MUST remain the only source of commands the daemon will execute,
  and nothing in this feature may introduce a path from a browser-supplied string
  to an executed command.
- **FR-017**: Any operator-supplied value appearing in the preview — a session
  name, a working directory — MUST be rendered as text and never as markup, on
  the same rule that governs pane output.
- **FR-018**: The operator MUST be able to copy the displayed command line.

**Continuing a conversation**

- **FR-019**: A create MUST be able to resume the most recent prior conversation
  in the session's working directory instead of starting an empty one.
- **FR-020**: A create MUST be able to resume a specific named prior conversation
  when more than one exists for that directory.
- **FR-021**: The daemon MUST identify prior conversations for a working directory
  by reading the conversation history Claude keeps on disk, deriving the location
  from the working directory itself. It covers conversations the daemon did not
  start, which is the case the operator needs after a crash, and it requires the
  daemon to record nothing new.
- **FR-021a**: That read is a **disclosure of work the daemon had no part in** —
  that a directory has been worked in, how many times, and when — and it reaches
  outside the allowlisted roots. It MUST therefore be: read-only; confined to
  listing conversations for a working directory the operator could create a
  session in (FR-022); and never a path from a caller-supplied string to an
  arbitrary filesystem location. A working directory MUST be resolved and checked
  before it is used to derive any path, on the same rule that governs a create.
- **FR-021b**: The daemon MUST NOT fail a create because conversation history is
  unreadable, absent, or malformed. A host that answers nothing yields an empty
  list and a form that still works, never a startup failure or a refused create.
- **FR-022**: Listing or resuming a conversation MUST be refused for any working
  directory a create would itself be refused for.
- **FR-023**: A conversation identifier MUST be validated at the boundary and MUST
  NOT reach a command line as an unvalidated caller-supplied string.
- **FR-024**: A request to continue that cannot be satisfied MUST NOT silently
  produce a fresh session. The operator MUST be told.
- **FR-025**: Conversation content MUST NOT be exposed by this feature. What may
  be shown is enough to choose between conversations — no more.

**Reading the fleet**

- **FR-026**: Every session MUST state how long it has been alive, whether it was
  started with remote control, and whether it can ever expire.
- **FR-027**: Those three facts MUST survive a daemon restart, and MUST read the
  same before and after one.

### Key Entities

- **Session**: A managed tmux session. Gains a durable per-session lifetime
  override — a value that outlives the daemon that created it. Loses its idle
  bound and its idle-derived state entirely.
- **Recorded session facts**: The set of facts the daemon writes onto the host at
  creation and reads back at adoption — today: managed-ness, owner, name, working
  directory, and start command, alongside the host's own creation timestamp. This
  feature adds the lifetime override to that set.
- **Start command**: An operator-configured, named command line. Remains the only
  source of executable commands; this feature adds options that modify a chosen
  one and a readout showing the result, and no way to introduce a new one.
- **Prior conversation**: A Claude conversation associated with a working
  directory, from which a new session may be continued. How the daemon comes to
  know of these is FR-021.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: A session left completely untouched for 24 hours is still running,
  where today it is destroyed after 60 minutes.
- **SC-002**: A session created to never expire is still running after a daemon
  restart followed by more than the daemon's default lifetime — the exact
  sequence that destroyed four sessions on 2026-08-14.
- **SC-003**: Across a daemon restart, every session's stated lifetime,
  remote-control provenance, age, and working directory read identically before
  and after.
- **SC-004**: No session is destroyed for any reason other than an operator asking
  for it or its absolute deadline passing.
- **SC-005**: The word "idle" appears nowhere in the operator-facing product —
  dashboard, API responses, configuration, or documentation.
- **SC-006**: For every combination of create options, the command line shown
  before creating matches the one the session was started with.
- **SC-007**: An operator whose session died can recover its conversation in a new
  session without leaving the dashboard.
- **SC-008**: The set of commands the daemon can be made to execute is exactly the
  operator-configured set, before and after this feature.

## Assumptions

- The operator has decided to withdraw the idle bound as a product decision. The
  measurement is not in question (see Why this exists), and no attempt is made
  here to preserve it in a weaker form.
- The default absolute lifetime is unchanged at the daemon's configured value.
  This feature makes the "never expires" switch honest; it does not make it the
  default.
- The existing rule that switching the absolute deadline off takes two decisions —
  an operator-configured unbounded ceiling *and* an explicit request on the
  create — is retained. Nothing here lets a caller reach that state alone.
- The mechanism already used to carry session facts across a restart is the
  natural place for the lifetime override, and no new persistence store is
  introduced by this feature.
- Resuming a conversation is a capability of the underlying Claude CLI, expressed
  through the configured start command. This feature composes flags onto a
  configured command; it does not reimplement conversation storage.
- The operator accepted the disclosure in FR-021a knowingly: the daemon runs as
  them, on their own machine, and the only caller that can reach the listing is
  already authenticated as the operator. The trade taken is that a caller who has
  passed the door learns which directories have been worked in and when — which is
  strictly less than what that same caller can already do by creating a session in
  one of them.
- The location of Claude's conversation history is a property of the Claude CLI,
  not of this daemon. It is derived, not configured, and FR-021b keeps a change to
  that layout from breaking a create.
- The dashboard remains the only client that needs these controls. The signed API
  is unchanged except where a response would otherwise still describe idleness.
- **Dependency**: Constitution Principle VI must be amended before implementation
  (see Constitutional Impact). Without it, this spec conflicts with a
  non-negotiable principle and must not proceed.
- **Dependency**: `docs/security.md` governs FR-019 through FR-025 — a new read of
  on-disk conversation history is a disclosure, and a conversation identifier is
  caller input reaching a command line.
- **Dependency**: `docs/auth-and-sessions.md` governs the session lifecycle
  changes in FR-001 through FR-013.
- **Dependency**: `docs/design-system.md` and `docs/components.md` govern the
  command-line preview and the session card, which must reuse the existing
  component vocabulary rather than introduce a second card or a second button.
