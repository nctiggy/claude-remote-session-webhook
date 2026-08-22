# Feature Specification: Session Revival

**Feature Branch**: `012-session-revival`

**Created**: 2026-08-22

**Status**: Draft

**Input**: User description: "Session revival: crswd detects when a session's Claude process has died and brings it back by resuming the same conversation, plus the new-session form's resume options follow the working directory the operator has actually typed."

## User Scenarios & Testing *(mandatory)*

### User Story 1 - A session that died comes back where it left off (Priority: P1)

An operator starts a session, drives it for an hour, and closes the browser. Some
time later the Claude process inside that session stops running — it crashed, it
was killed for memory, or it simply ended. The operator opens the dashboard the
next morning and finds the session working again, carrying everything it knew
before it stopped, rather than a dead shell or a session that quietly did nothing
all night.

**Why this priority**: This is the reported failure. A session that dies silently
is indistinguishable from a session that is thinking, so the operator learns it
died only by coming back to it — which is exactly when the work is most stale.
Nothing else in this feature matters if this does not work.

**Independent Test**: Start a session, end the Claude process inside it without
going through the dashboard, wait one sweep, and confirm the session is running
again and answers a question about what it was told before it died.

**Acceptance Scenarios**:

1. **Given** a running session whose Claude process has stopped, **When** the
   daemon's next sweep runs, **Then** the session is running again and is
   continuing the same conversation, not a fresh one.
2. **Given** a session that has been revived, **When** the operator asks it about
   work from before it died, **Then** it answers from that conversation.
3. **Given** a session that has been revived, **When** the operator reads its
   record, **Then** the session keeps the identity, owner and absolute deadline it
   had before it died — revival is not a new session and does not extend its life.
4. **Given** a session whose Claude process is healthy, **When** a sweep runs,
   **Then** nothing is sent to it.
5. **Given** a session whose whole shell is gone — its tmux session no longer
   exists at all — **When** a sweep runs, **Then** a new shell is created for it
   in its recorded working directory and its recorded conversation is resumed
   there, under the same session identity.

---

### User Story 2 - Revival never resurrects what the operator ended (Priority: P1)

An operator destroys a session from the dashboard. It stays destroyed. Nothing
the daemon does later brings it back, and no revival ever creates a session the
operator did not ask for.

**Why this priority**: The constitution bounds blast radius by construction. A
mechanism that starts unsandboxed shells on its own initiative is only acceptable
if the operator's decision to end one is final. Getting this wrong turns a
convenience into a way for sessions to outlive the person who ended them.

**Independent Test**: Destroy a session through the dashboard, then run sweeps
and confirm no process is started and no record reappears.

**Acceptance Scenarios**:

1. **Given** a session the operator destroyed, **When** any number of sweeps run,
   **Then** nothing is started and the session does not reappear.
2. **Given** a session the reaper destroyed for reaching its deadline, **When** a
   sweep runs, **Then** it is not revived — an expired session is over.
3. **Given** a working directory that is no longer allowlisted, **When** a session
   in it needs reviving, **Then** it is not revived and the refusal is recorded.

---

### User Story 3 - Revival gives up loudly instead of looping (Priority: P2)

A session cannot be revived — its directory is gone, its conversation is
unreadable, or Claude exits immediately every time. The daemon tries a bounded
number of times, stops, and marks the session as failed so the operator can see
it on the dashboard. It does not keep trying forever.

**Why this priority**: An unbounded retry is not a smaller bug than no retry at
all; it is a louder one. This host has already lost four hours to a unit that
restarted 2,826 times against an error no retry could fix, and the damage was
that it failed invisibly. Revival must not reproduce that.

**Independent Test**: Make revival fail deterministically, run many sweeps, and
confirm the attempt count stops at the bound and the session is shown as failed.

**Acceptance Scenarios**:

1. **Given** a session whose revival fails, **When** sweeps continue, **Then**
   attempts stop at the configured bound and no further attempt is made.
2. **Given** a session that has exhausted its attempts, **When** the operator
   views the dashboard, **Then** the session is shown as failed rather than as
   running or as healthy.
3. **Given** a session whose revival fails, **When** the operator reads the audit
   trail, **Then** each attempt and the final give-up are recorded.
4. **Given** consecutive revival attempts, **When** they are made, **Then** each
   waits longer than the last.

---

### User Story 4 - Resume options follow the directory actually typed (Priority: P3)

An operator opens the new-session form and types or picks a working directory.
The list of conversations they may continue is the list for *that* directory, not
for whichever directory the form happened to suggest when the page was rendered.

**Why this priority**: The control already exists and already lists
conversations; it simply lists them for a directory the operator may have since
changed. It is the smallest slice here and the only one visible on the form, but
a wrong list is worse than no list — it offers to continue work from somewhere
else.

**Independent Test**: Open the form, change the working directory to a second
allowlisted directory with its own conversation history, and confirm the offered
conversations are that directory's.

**Acceptance Scenarios**:

1. **Given** the new-session form, **When** the operator changes the working
   directory, **Then** the conversations offered are those of the new directory.
2. **Given** a directory with no conversation history, **When** it is chosen,
   **Then** the form offers to start fresh and offers no conversations.
3. **Given** a directory that is not allowlisted, **When** it is entered, **Then**
   no conversations are disclosed for it.

---

### Edge Cases

- **The daemon restarts while a session is dead.** Revival state must survive the
  daemon, or a restart becomes a way to reset the attempt bound and resume
  looping.
- **The operator is watching the pane when revival happens.** The revived command
  is typed into the same shell the operator can see, so revival must be legible
  in the pane rather than appearing as unexplained input.
- **Two sweeps overlap.** A slow revival must not have a second one started on top
  of it, which would leave two Claude processes in one session.
- **The conversation has no transcript.** A session whose conversation was never
  written, or whose transcript was removed, cannot be resumed by identifier; it
  must not be revived into a resume of nothing.
- **The session is at the fleet cap.** A revived session was already counted; it
  must not be refused for a cap it is itself part of, nor may revival admit a
  session over the cap.
- **The whole shell is gone, not just Claude.** The kernel OOM killer takes a
  cgroup, not a process: Claude, its shell and its tmux session vanish together
  and every value stored on that tmux session vanishes with them. This is the
  observed failure, and it is why the resume handle cannot live only on the shell.
- **A recreated session dies the same way again.** A session killed for using 12
  GB is likely to do it again, so recreation must be bounded by the same
  give-up rule as any other revival rather than becoming an OOM loop.

## Requirements *(mandatory)*

### Functional Requirements

#### Conversation identity

- **FR-001**: Every session the daemon creates MUST be given a conversation
  identifier chosen by the daemon at creation, so that the conversation can later
  be resumed exactly rather than guessed at.
- **FR-002**: The identifier MUST be unique per session, so that two sessions
  started from the same working directory are never confused for one another.
- **FR-003**: The identifier MUST survive both a daemon restart and the loss of
  the session's shell, without a database. A handle stored only on the running
  shell is lost in exactly the failure this feature exists to recover from.
- **FR-004**: The identifier MUST be validated against the existing conversation
  identifier alphabet before it reaches any command line.
- **FR-005**: A session created before this feature existed MUST continue to work,
  and MUST NOT be revived by identifier it never had.

#### Detecting death

- **FR-006**: The daemon MUST detect, without operator action, both kinds of
  death: a session whose Claude process has stopped while its shell survives, and
  a session whose shell no longer exists at all.
- **FR-007**: Detection MUST NOT read the content of a session's pane or its
  conversation transcript.
- **FR-008**: Detection MUST run on a recurring sweep whose resolution is finer
  than the delay an operator would notice, and MUST NOT depend on a request
  arriving.

#### Reviving

- **FR-009**: On detecting a dead session that is eligible for revival, the daemon
  MUST restart it so that it continues its recorded conversation.
- **FR-010**: A revived session MUST keep its identity, its owner, its working
  directory, its start command and its absolute deadline. Revival MUST NOT extend
  a session's lifetime or issue it a new deadline.
- **FR-011**: A revived session MUST NOT count as a new session against the
  concurrent-session cap, and revival MUST NOT take the fleet over that cap.
- **FR-012**: Revival MUST NOT run for a session the operator destroyed, for a
  session the reaper destroyed, or for a session past its deadline.
- **FR-013**: Revival MUST NOT run for a session whose working directory no longer
  resolves inside the allowlist.
- **FR-014**: Revival MUST NOT run for a session whose recorded conversation has
  no transcript on the host.
- **FR-015**: At most one revival MUST be in flight for a session at a time.

#### Recreating a vanished shell

- **FR-015a**: A session whose shell no longer exists, but whose record says it
  should be running, MUST have a new shell created for it in its recorded working
  directory, under its existing session identity.
- **FR-015b**: A recreated session MUST be re-established as daemon-managed and
  re-owned by its original owner, so that a shell created this way is never an
  unowned unsandboxed shell.
- **FR-015c**: Recreation MUST be subject to every refusal that governs revival —
  destroyed, expired, over cap, un-allowlisted, no transcript — with no exception
  for the shell having vanished.

#### Giving up

- **FR-016**: Revival attempts for one session MUST be bounded by a fixed maximum.
- **FR-017**: Consecutive attempts MUST be separated by an increasing delay.
- **FR-018**: A session that exhausts its attempts MUST be marked failed, MUST stop
  being attempted, and MUST remain visible to the operator in that state.
- **FR-019**: The attempt count and the failed state MUST survive a daemon
  restart, so that restarting the daemon does not reset the bound.
- **FR-020**: A successful revival MUST reset the attempt count.

#### The trail

- **FR-021**: Each detection, each revival attempt, each success and each give-up
  MUST produce an audit record naming the session.
- **FR-022**: No audit record may carry pane content, conversation content, a
  credential, or a caller-supplied string.

#### The form

- **FR-023**: The conversations offered on the new-session form MUST be those of
  the working directory currently chosen on the form.
- **FR-024**: Conversations MUST NOT be disclosed for a directory that does not
  resolve inside the allowlist.
- **FR-025**: Listing conversations MUST NOT open a transcript, and MUST continue
  to disclose no more than an identifier and a time.
- **FR-026**: A failure to list conversations MUST leave the form usable, offering
  a fresh start.

#### Clean exit

- **FR-027**: Destroying a session from the dashboard is the only signal that
  ends it for good. The daemon MUST NOT attempt to distinguish an operator who
  typed `/exit` inside a session from a process that crashed, and MUST revive
  either — a session ended from inside comes back once, and the operator destroys
  it if they meant it.

  *Decided 2026-08-22.* The start command is typed into a login shell, so no exit
  status is observable and the distinction is not available without running Claude
  as the pane's own process — which would cost the inspectable shell that
  surviving a crash depends on. Claiming a distinction the daemon cannot make
  would be worse than not making it.

### Key Entities

- **Session**: an owned, deadline-bound, allowlisted working directory with a
  shell running under it. Gains a conversation identifier, a revival attempt
  count, and a lifecycle state that can be *failed*.
- **Conversation**: a prior Claude conversation for a working directory,
  identified and ordered by recency, with no content disclosed.
- **Revival attempt**: one bounded, recorded try at bringing a dead session back,
  carrying its ordinal and the delay before it.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: A session whose Claude process dies is running again within one
  sweep interval, with no operator action.
- **SC-002**: A revived session answers correctly about work from before it died
  in 100% of cases where the conversation transcript still exists.
- **SC-003**: A session the operator destroyed is never restarted, across any
  number of sweeps.
- **SC-004**: A session that cannot be revived is attempted no more than the fixed
  maximum number of times, and the operator can see it is failed without reading a
  log.
- **SC-005**: Revival never increases the number of live sessions above the
  configured cap, and never extends any session's absolute deadline.
- **SC-006**: An operator changing the working directory on the new-session form
  sees the conversation list for that directory before they submit.
- **SC-007**: Restarting the daemon does not reset a failed session to healthy and
  does not resume attempts against it.

## Assumptions

- **Revival is on by default and has no configuration knob.** The reported failure
  is a session that died silently; a feature that must be switched on would not
  have prevented it. A knob can be added when someone needs one — "it might be
  useful later" is not a justification the constitution accepts.
- **A revived session keeps its existing credential.** Its record never went away,
  so nothing about its ownership changed. This differs deliberately from startup
  adoption, which mints a fresh credential precisely because the record *had* gone.
- **A session can die in three ways, and two of them are in scope.** Claude can
  exit while its shell survives; the shell and its tmux session can be destroyed
  while the tmux server lives; or the host can go down and take the tmux server
  with it. The first two are handled identically here — the record says the
  session should be running, so it is made to run again. The third is handled by
  the same mechanism as a consequence rather than as a goal: nothing about
  recreation cares *why* the shell is missing.

  *This scope was set by evidence, not by preference.* The session that prompted
  this feature was killed on 2026-08-22 at 08:16:10Z by the kernel OOM killer,
  which took the whole `tmux-spawn-…` cgroup scope — Claude, the shell and the
  tmux session together — on a host that had been up for five days and whose tmux
  server never restarted. A design that kept the resume handle on the running
  shell would have lost it in precisely that failure.

- **Sessions come back at the deadline they had, or not at all.** Recreating a
  fleet after a host restart could otherwise start many unsandboxed shells with
  no human present. The absolute deadline, the allowlist and the concurrent cap
  are what bound that, and none of them is relaxed for a recreated session.
- **The sweep shares the reaper's cadence.** The daemon already sweeps for expired
  sessions on a fixed interval; a second cadence would be a second thing to reason
  about for no gain.
- **Conversation identifiers are the daemon's, not the operator's.** The operator
  never types one and never needs to see one; display names remain what a human
  reads.
- **The existing resume machinery is reused.** Resuming a conversation by
  identifier, validating that identifier, and listing a directory's conversations
  all already exist and are not rebuilt here.
