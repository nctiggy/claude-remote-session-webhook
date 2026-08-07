# Feature Specification: Finish the dashboard

**Feature Branch**: `005-finish-the-dashboard`
**Created**: 2026-08-07
**Status**: Draft
**Input**: The operator's own feedback after a day of using milestone 4, plus two
requirements milestone 4 claimed and did not deliver.

## Why this is a milestone and not six issues

Nothing here is a new capability. Every item is something that **already exists**
and is unreachable, invisible, incomprehensible, or visibly foreign. That is a
different kind of work from milestone 4's, and it is worth naming, because it is
the kind that never gets prioritised on its own: each item is individually small
enough to postpone forever, and collectively they are the difference between
software that works and software that is pleasant to use.

Two of the six are not feedback at all. They are **requirements milestone 4 wrote
down and did not deliver**, which every test passed over. Those come first.

## The lesson milestone 4 paid for

FR-026 said: *"An operator MUST be able to choose remote control as a mode when
creating a session, rather than selecting a command by name."*

Three tasks were written for it. One derived the mode from the record, one added
the route that changes it, one showed it on the card. All three shipped, all
their tests passed, and **the create form still renders a dropdown of command
names** — because no task asserted on what the create form renders.

The failure was not insufficient testing. It was testing the wrong layer: every
assertion was about a route or a record, and the requirement was about a control.

**This milestone's success criteria are written so that cannot happen again.** A
requirement about what an operator sees is only met when something asserts on the
rendered markup.

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Turn remote control on when I create a session (Priority: P1)

An operator creating a session decides, at that moment, whether it should be
reachable from claude.ai. They express that as a yes-or-no choice, not by
selecting the name of a command they did not write and cannot see.

**Why this priority**: It is a milestone-4 requirement that did not ship, and the
operator has now asked for it twice in their own words: *"I don't want it to be
like a start command. I want it to just be a button."*

**Independent test**: Open the create form. There is a two-state control for
remote control and no list of command names anywhere on the page. Create with it
on; the session is in remote mode. Create with it off; it is not.

**Acceptance scenarios**:

1. **Given** the create form, **When** it renders, **Then** it offers a two-state
   remote-control choice and shows no command name and no command line.
2. **Given** remote control on, **When** the session starts, **Then** its mode is
   remote and its card says so.
3. **Given** remote control off, **When** the session starts, **Then** its mode is
   local.
4. **Given** a submitted value that is neither of the two states, **When** it
   arrives, **Then** it is refused uniformly and nothing starts.

---

### User Story 2 - Pick a working directory from a list that has something in it (Priority: P2)

An operator starting a session chooses a directory from offered options instead of
typing a path from memory, and can still type any path in full.

**Why this priority**: The picker shipped and the operator concluded it does not
exist. They were right in every way that matters — on a default install it has no
sources at all and renders as a plain text box.

**Independent test**: On a daemon with approved roots configured and nothing else,
open the create form. Directories are offered. Type a path that is not among them;
it is still accepted if it is allowed.

**Acceptance scenarios**:

1. **Given** a daemon with approved roots and no other configuration, **When** the
   create form renders, **Then** it offers directories to choose from.
2. **Given** an operator who types a path not in the list, **When** it is
   submitted, **Then** it is accepted if it is under an approved root.
3. **Given** a suggested path that is **not** under an approved root, **When** it
   is submitted, **Then** it is refused exactly as a typed one would be.
4. **Given** an explicitly configured list of directories, **When** the form
   renders, **Then** those appear.

---

### User Story 3 - Reach the settings page (Priority: P3)

An operator who wants to know how the daemon is configured gets there by clicking,
not by knowing a URL.

**Why this priority**: The page shipped, is useful, and is reachable only by
typing an address. It is also the smallest item here.

**Independent test**: From any page, a visible control leads to the settings page.

**Acceptance scenarios**:

1. **Given** any page, **When** it renders, **Then** it carries a visible link to
   the settings page.
2. **Given** that link, **When** it is followed, **Then** the settings page
   renders and produces exactly one audit record.
3. **Given** the header, **When** it renders, **Then** the wordmark still links
   home and the header has not become a navigation bar.

---

### User Story 4 - Controls that belong to this interface (Priority: P4)

An operator using the create form sees controls that look like the rest of the
product rather than like the browser's own.

**Why this priority**: In the operator's words, the current ones *"take you out of
it."* It is the largest item here and the one whose cost was accepted knowingly.

**Independent test**: Open the create form and compare the choosing controls
against the surrounding interface. Then disable scripting and confirm every one of
them still works.

**Acceptance scenarios**:

1. **Given** the create form, **When** it renders, **Then** its choosing controls
   are styled from the design system's tokens.
2. **Given** scripting is disabled, **When** the form is used, **Then** every
   control still accepts input and submits, degrading to a plain text field.
3. **Given** keyboard-only operation, **When** a control is focused, **Then** it
   has a visible focus indicator and can be operated without a pointer.
4. **Given** a filtered list, **When** it narrows, **Then** the result is
   announced, and the interface says when it is showing a subset.
5. **Given** a reduced-motion preference, **When** a control opens or closes,
   **Then** nothing animates.

---

### User Story 5 - Stop being asked a question I cannot answer (Priority: P5)

An operator creating a session is not asked to supply an opaque identifier they
have no way to obtain.

**Why this priority**: It is a removal, and removals are cheap. The operator's
report: *"I have no idea what the conversation section is for now... it is free
text... I have no idea how to understand that."*

**Independent test**: Open the create form. There is no conversation field.
Creating a session still works.

**Acceptance scenarios**:

1. **Given** the create form, **When** it renders, **Then** it contains no
   conversation or resume field.
2. **Given** a create request, **When** it is submitted, **Then** the session
   starts fresh, as it already does by default.
3. **Given** a request that still carries a resume value, **When** it arrives,
   **Then** it is ignored or refused, and never passed to a command.

---

## Requirements *(mandatory)*

### Remote control at create time

- **FR-001**: The create form MUST offer remote control as a two-state choice.
- **FR-002**: The create form MUST NOT display a command name or a command line,
  in any control, label, or attribute.
- **FR-003**: The submitted value MUST be one of exactly two states; anything else
  is the uniform refusal.
- **FR-004**: Which configured command each state runs MUST remain the daemon's
  decision, read from configuration and never from the request.
- **FR-005**: A session created with remote control on MUST be in remote mode, and
  its card MUST say so in words.

### Working-directory suggestions

- **FR-006**: A daemon with approved roots and no further configuration MUST offer
  at least one directory to choose from.
- **FR-007**: An operator MUST be able to configure an explicit list of offered
  directories.
- **FR-008**: Any path MUST remain typeable in full, whether or not it is offered.
- **FR-009**: A path chosen from the list MUST be validated exactly as a typed one
  is. The list is a convenience; the allowlist is the control.
- **FR-010**: Offering a directory MUST NOT disclose anything the operator has not
  already told the daemon.

### Reaching the settings page

- **FR-011**: Every page MUST carry a visible link to the settings page.
- **FR-012**: The link MUST NOT displace or compete with the wordmark that links
  home.
- **FR-013**: The settings page MUST remain read-only, with no mutating verb
  registered on it.

### Appearance of the choosing controls

- **FR-014**: Every choosing control on the create form MUST take its appearance
  from the design system's tokens.
- **FR-015**: Every such control MUST function with no scripting, degrading to a
  plain text field that accepts any value.
- **FR-016**: Every such control MUST be operable by keyboard alone, with a
  visible focus indicator.
- **FR-017**: A filtered result MUST be announced to a screen reader, and the
  interface MUST say when it is showing a subset of matches.
- **FR-018**: Nothing MUST animate under a reduced-motion preference.
- **FR-019**: No control's appearance may become the only cue for a state.

### Removing the conversation field

- **FR-020**: The create form MUST NOT ask for a conversation identifier.
- **FR-021**: Starting fresh MUST remain what a create does.
- **FR-022**: No caller-supplied conversation identifier may reach a command line.

### Documentation

- **FR-023**: The documented way to read the audit trail MUST work as written on a
  running daemon.

### Carried forward

- **FR-024**: Zero third-party dependencies.
- **FR-025**: Every request to every route produces exactly one audit record.
- **FR-026**: No change weakens the uniform refusal or the uniform not-found.
- **FR-027**: No secret, token, prompt text, pane content, or conversation
  transcript in any log, audit record, or page.
- **FR-028**: Both halves of the cross-site defence apply to every mutating route
  and remain independently testable.
- **FR-029**: Cards carry exactly one anchor.

## Success Criteria *(mandatory)*

Several of these are deliberately phrased as assertions about **rendered output**.
That is the correction milestone 4 earned: a requirement about what an operator
sees is not met until something reads the markup and says so.

- **SC-001**: The create form's rendered markup contains a two-state remote-control
  control and **zero** occurrences of any configured command name — asserted
  against the markup, not against a route.
- **SC-002**: A daemon configured with approved roots and nothing else renders at
  least one offered directory in its create form, asserted against the markup.
- **SC-003**: Every page's rendered markup contains a link to the settings page.
- **SC-004**: The create form's rendered markup contains no conversation or resume
  field.
- **SC-005**: With scripting disabled, an operator can create a session, choose a
  directory from the offered set, type an unoffered path, and set remote control —
  verified per control.
- **SC-006**: A path that is offered but not allowed is refused with the same
  response and the same audit record as a typed one.
- **SC-007**: Every choosing control on the create form draws its colour, spacing
  and typography from tokens, with no literal colour value in any template.
- **SC-008**: The documented audit-trail command runs clean against a daemon that
  has been up long enough to have logged both its own records and systemd's.
- **SC-009**: Every route still produces exactly one audit record per request.
- **SC-010**: `go.sum` remains absent.

## Assumptions

- **The mode model is already correct and is not revisited.** Mode is derived from
  the start-command name, and configuration says which names mean remote. This
  milestone changes what the operator is shown, not what the daemon stores.
- **The approved roots are safe to offer.** The operator supplied those paths; a
  form that lists them back discloses nothing new, which is what makes them the
  right default source.
- **Removing the conversation field loses nothing an operator was using**, because
  its valid values were never discoverable.
- **A themed control costs more code than a native one.** That is the trade being
  made knowingly, not a problem to be solved away.

## Out of Scope

Deliberately not in this milestone, so no task wanders into them:

- **Releases, versioning, the installer, and self-update** (#57, #68, #69, #66) —
  milestone 6. Note recorded on #69: the installer should also write the systemd
  unit, Ubuntu and systemd first, and it must not overwrite a unit the operator
  has edited.
- **Auto-recovery of a crashed session** (#95's second half). The operator asked to
  think about it further, and it has a genuinely hard part: resuming "the last
  conversation in this directory" may resume another session's, and the daemon
  already refuses rather than resuming the wrong one. Removing the confusing field
  is in scope; designing what replaces it is not.
- **Editing settings from the browser.** The read-only view is correct, and no
  mutating verb is registered on that page at all.
- **The rain's Easter eggs** (#54) and the **browser accessibility verification**
  (#17) — polish, and a task only a human with a browser can do.
- **Any change to milestone 1's signing procedure, its six operations, or the
  audit record shape.**
