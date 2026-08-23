# Feature Specification: Continue a Conversation After the Session Is Running

**Feature Branch**: `013-continue-after-start`

**Created**: 2026-08-23

**Status**: Draft

**Input**: User description: "Remove the lifetime hint from the new session dialog. Remove the continue — it is useless. Rather, after starting a session, allow me to select a conversation to continue from the resumes available in the path."

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Continue a conversation from a session that is already running (Priority: P1)

An operator starts a session in a directory, looks at it, and decides they would
rather pick up work from earlier. From the running session they choose "Continue
a conversation", see the prior conversations recorded for that session's own
working directory, pick one, and the session carries on from it.

**Why this priority**: This is the whole feature. The decision to continue is one
an operator makes *after* they can see what a directory holds, not while filling
in a form before the session exists.

**Independent Test**: Start a session in a directory with prior conversations,
choose one from the running session, and confirm it answers about work from that
conversation.

**Acceptance Scenarios**:

1. **Given** a running session, **When** the operator opens the continue control,
   **Then** they see the conversations recorded for that session's working
   directory, newest first, and nothing from any other directory.
2. **Given** a chosen conversation, **When** the operator confirms, **Then** the
   session is running that conversation and answers about work from it.
3. **Given** a session that has continued a conversation, **When** the operator
   reads its record, **Then** its identity, owner, working directory, start
   command and absolute deadline are unchanged. Continuing is not a new session.
4. **Given** a session that has continued a conversation, **When** it later dies,
   **Then** revival brings back the conversation it was continuing, not the one
   it was created with.
5. **Given** a working directory with no prior conversations, **When** the
   operator opens the control, **Then** it says there is nothing to continue
   rather than offering an empty list.

---

### User Story 2 - The new-session dialog asks only what it needs (Priority: P2)

An operator creating a session is asked for a name, a directory, a start command
and a lifetime — and nothing about conversations. Every session starts fresh.

**Why this priority**: It is the other half of moving the decision, and it removes
a control that asked an operator to choose before they could see. It is separable
from US1 only in that the dialog could be cleaned up first.

**Independent Test**: Open the new-session dialog and confirm no resume control is
present; create a session and confirm it starts a fresh conversation.

**Acceptance Scenarios**:

1. **Given** the new-session dialog, **When** it renders in any directory,
   **Then** it offers no way to resume or continue anything.
2. **Given** a create, **When** the session starts, **Then** it is a fresh
   conversation, and one this daemon can later revive and continue.
3. **Given** the lifetime control, **When** the operator switches the deadline
   off, **Then** the dialog no longer claims nothing will reap the session.

---

### User Story 3 - "The most recent" is gone from the daemon (Priority: P3)

Nothing in the daemon resolves "whatever conversation this directory last had".
An operator continues a conversation they can see and name, or none.

**Why this priority**: It is cleanup that follows from US1 and US2 rather than a
capability anyone gains. It is listed separately because it removes a value the
daemon accepted, which is a contract change rather than a screen change.

**Independent Test**: Post the old "latest" value at every route that took it and
confirm it is refused; confirm no command line the daemon builds carries the
continue flag.

**Acceptance Scenarios**:

1. **Given** the value that meant "the most recent", **When** it is submitted to
   any route, **Then** it is refused exactly as any other unrecognised value is.
2. **Given** any session this daemon starts, restarts, revives or continues,
   **When** its command line is built, **Then** it never carries the continue
   flag.

---

### Edge Cases

- **The conversation vanishes between listing and choosing.** A transcript can be
  removed while the control is open; continuing one that is no longer there must
  fail visibly rather than restart the session into nothing.
- **The operator continues the conversation the session is already having.** It
  must be a no-op or a clean restart of the same conversation, never a second
  process in one shell.
- **The session's directory left the allowlist.** Continuing must be refused for
  the same reason creating there would be.
- **The session is dead or failed.** A session that is not running has nothing to
  continue into.
- **A continue races the supervisor's revival.** Both restart the same shell, and
  only one may do so at a time.
- **Continuing is not driving.** It restarts what the session runs, so it moves
  what the record says the session is having — but it must not extend any bound.

## Requirements *(mandatory)*

### Functional Requirements

#### The new control

- **FR-001**: A running session MUST offer a way to continue one of the prior
  conversations recorded for its own working directory.
- **FR-002**: The conversations offered MUST be those of the session's recorded
  working directory, and never a directory the caller names.
- **FR-003**: The list MUST be ordered newest first and MUST disclose no more than
  an identifier and a time. No transcript is opened.
- **FR-004**: A session whose directory has no prior conversations MUST be told
  there is nothing to continue, rather than shown an empty control.
- **FR-005**: Only the session's owner may continue it, and the same request
  integrity every other session-changing action requires MUST apply.

#### What continuing does

- **FR-006**: Continuing MUST restart what the session is running so that it picks
  up the chosen conversation.
- **FR-007**: The session's identity, owner, working directory, start command,
  creation time and absolute deadline MUST be unchanged by continuing.
- **FR-008**: The session's recorded conversation MUST become the chosen one, so
  that a later revival brings back the conversation it was continuing.
- **FR-009**: The change MUST survive a daemon restart and the loss of the
  session's shell.
- **FR-010**: Continuing MUST NOT issue a new credential, and MUST NOT change how
  many sessions count against the concurrent cap.
- **FR-011**: Continuing MUST be refused for a session that is not running, for a
  conversation with no transcript on the host, and for a working directory that no
  longer resolves inside the allowlist.
- **FR-012**: At most one restart of a session MUST be in flight at a time,
  whether it comes from continuing or from revival.
- **FR-013**: Each continue MUST produce one audit record naming the session, and
  carrying no pane content, no conversation content and no caller-supplied text.

#### What is removed

- **FR-014**: The new-session dialog MUST offer no resume or continue control, and
  every session it creates MUST start a fresh conversation.
- **FR-015**: The new-session dialog MUST NOT state that removing the absolute
  lifetime leaves nothing to reap the session.
- **FR-016**: The value meaning "the most recent conversation in this directory"
  MUST be refused wherever it was previously accepted.
- **FR-017**: No command line this daemon builds may carry the continue flag, on
  any path — create, restart, revive or continue.

### Key Entities

- **Session**: unchanged in shape. Its recorded conversation becomes a value that
  can move during the session's life rather than only at its creation.
- **Conversation**: a prior conversation for a working directory, identified and
  ordered by recency, with no content disclosed.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: An operator can continue a prior conversation from a running session
  without creating a new one, and the session answers about that conversation's
  work.
- **SC-002**: A session that has continued a conversation keeps the same
  identifier and the same expiry as before it continued, in 100% of cases.
- **SC-003**: A session that continued a conversation and then dies comes back on
  the conversation it continued.
- **SC-004**: The new-session dialog presents no choice about conversations, and
  creating a session takes fewer decisions than before.
- **SC-005**: Submitting the retired "most recent" value anywhere is refused.
- **SC-006**: No session this daemon starts by any route runs with the continue
  flag.

## Assumptions

- **Continuing is a session action, like compacting.** It changes what a running
  session is doing, is refused for a session that is not running, and is
  authorised exactly as the existing session-changing actions are. It is not a
  second kind of create.
- **The directory is the session's, never the caller's.** The existing conversation
  listing takes a directory from the caller because the create form has no session
  yet. This one has one, so the directory is read from the record and the caller
  supplies only which conversation.
- **Continuing does not extend a life.** It restarts the process, not the session,
  and every bound the session already had still applies with the same origin.
- **Removing "the most recent" loses nothing an operator can see.** It resolved to
  a conversation only the CLI knew, so it could not be shown, named or predicted —
  which is what made it a choice nobody could make.
- **The create form's directory-scoped conversation lookup stays.** It is now used
  only by the continue control, which asks about one directory the same way.
