# Feature Specification: Configure and Operate It

**Feature Branch**: `004-configure-and-operate`

**Created**: 2026-08-07

**Status**: Draft

**Input**: Consolidate the open issue backlog — #41, #42, #44, #45, #49, #58, #59, #60, #65, #71 — into one milestone. The daemon is now used daily; the friction is in configuring it and in the dashboard's rough edges.

## Why this is a milestone and not ten issues

Milestones 1 to 3 shipped through spec → plan → tasks → loop. The ten issues below were instead fed to the one-at-a-time lane, and **not one run finished**: each took thirteen to sixteen minutes, hit its turn budget, left committed partial work on a branch, and had to be completed by hand.

Two of them are the reason. **#65** is a file format, a parser, a migration path and every variable moved; **#59** is a hand-built accessible combobox with filtering and a discovery flag. Those are milestone-sized on their own, and no turn budget makes a milestone fit in one run. The rest are ordinary but interdependent — the settings page cannot exist before the config file, and the remote-control toggle cannot exist before the start-command allowlist it selects from.

Decomposing them together is the work this milestone exists to do.

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Configure the daemon in a file I can read (Priority: P1)

An operator opens one file, sees every setting with the reasoning beside it, changes what they need, and restarts. No unit file editing, no environment variables scattered across a service definition.

**Why this priority**: It is the foundation the settings page stands on, and it is the friction an operator meets first — before this, changing the allowed roots means editing a systemd unit. It also moves the shared secret out of a unit file, where `systemctl show` prints it to anyone who asks.

**Independent Test**: Write a config file, start the daemon, confirm it reads every setting from the file. Delete the file, confirm the daemon still starts on defaults and environment variables — the deployment that exists today must not break.

**Acceptance Scenarios**:

1. **Given** a config file naming the approved roots, **When** the daemon starts, **Then** it uses those roots and says which file it read.
2. **Given** no config file at all, **When** the daemon starts, **Then** it starts on defaults and environment variables exactly as it does today.
3. **Given** a config file and an environment variable naming the same setting, **When** the daemon starts, **Then** the environment variable wins, so a container or a test can override one value without writing a file.
4. **Given** a config file containing a key the daemon does not recognise, **When** it starts, **Then** it refuses and names the key — a misspelled containment boundary must never silently do nothing.
5. **Given** a config file readable by group or world **and** containing a secret, **When** the daemon starts, **Then** it refuses and says what to run to fix it.

---

### User Story 2 - See what this daemon is actually configured to do (Priority: P2)

An operator's session was refused and they do not know why. They open a settings page and read the approved roots, the limits, the lifetimes, and which identity provider is trusted.

**Why this priority**: It answers the question an operator actually asks — *"why was my working directory refused?"* — and it is the whole of what a settings page needs to be before anything is editable. It carries no mutating surface at all.

**Independent Test**: Open the page as the allowlisted identity and confirm every configured value appears, that secrets show as present or absent and never as values, and that a request without an identity is refused exactly as every other browser route is.

**Acceptance Scenarios**:

1. **Given** a configured daemon, **When** the operator opens the settings page, **Then** every setting is shown with its current value.
2. **Given** a configured shared secret, **When** the page renders, **Then** it says a secret is set and never shows it — and the same for the allowlisted addresses.
3. **Given** a daemon reading a config file, **When** the page renders, **Then** it names the file it read, so "why is my change not applied" has an answer on the page.
4. **Given** a request with no verified identity, **When** it reaches the settings page, **Then** it is refused with the same uniform response every other browser route gives.

---

### User Story 3 - The dashboard behaves when scripting does not (Priority: P3)

An operator acts on a session from a browser with JavaScript disabled, or from a tab whose script failed to load, and still lands somewhere sensible.

**Why this priority**: Actions currently answer with a fragment and rely on a script to place it. When that script is absent — a stale tab, a cached file, an error earlier on the page — the operator lands on a bare sentence with no page around it. That happened repeatedly in real use, and it looked like a broken product every time.

**Independent Test**: Disable scripting, perform each of the four actions, and confirm every one returns to the fleet with the outcome shown.

**Acceptance Scenarios**:

1. **Given** scripting is unavailable, **When** the operator destroys a session, **Then** the browser lands back on the fleet with the outcome stated.
2. **Given** scripting is available, **When** the operator acts, **Then** the outcome appears without navigating — the enhancement still applies.
3. **Given** a teardown that could not be verified, **When** it is reported without scripting, **Then** it is still prominent rather than reduced to one line among many.

---

### User Story 4 - Turn remote control on and off (Priority: P4)

An operator decides, per session and at any time, whether it is reachable from claude.ai — on creation, and on a session already running, without losing the conversation.

**Why this priority**: The naming half already shipped; a session started with remote control carries its own name. What is missing is choosing it as a *mode* rather than picking a command from a list, and changing that decision later.

**Independent Test**: Create a session with remote control off, turn it on from its card, confirm the process changed and the conversation survived, then turn it off again.

**Acceptance Scenarios**:

1. **Given** the create form, **When** the operator enables remote control, **Then** the session starts reachable from claude.ai under the name they gave it.
2. **Given** a running plain session, **When** the operator turns remote control on, **Then** the tmux session and its scrollback survive and the conversation continues.
3. **Given** a running remote-control session, **When** the operator turns it off, **Then** the same holds in reverse.
4. **Given** the toggle, **When** it is used, **Then** it takes no command line from the browser — only a choice between commands the operator configured.

---

### User Story 5 - Pick a working directory instead of typing one (Priority: P5)

An operator starts a session by choosing from directories they actually use, typing a few characters to narrow the list, and can still enter any path by hand.

**Why this priority**: A mistyped path is the most common way a create fails, and the refusal deliberately does not say which directory does not exist. In real use an operator lost time to a transposed pair of letters in a directory name.

**Independent Test**: Configure a list, confirm typing filters it, confirm selecting fills the field, and confirm a path that is in no list is still accepted.

**Acceptance Scenarios**:

1. **Given** configured directories, **When** the operator types, **Then** the list narrows to matches.
2. **Given** a path in no list, **When** it is typed in full, **Then** it is accepted — a convenience must never remove a capability.
3. **Given** discovery enabled, **When** the form renders, **Then** directories under the approved roots appear without being listed by hand.
4. **Given** scripting is unavailable, **When** the form renders, **Then** it is the plain text field it is today.
5. **Given** a list longer than the page, **When** the operator filters, **Then** the control says when it is showing a subset.

---

### User Story 6 - A card that separates what it is from what it does (Priority: P6)

An operator scans the fleet and sees, on each card, a block of information and a row of controls with a line between them — and can click anywhere in the information to open the session.

**Why this priority**: The click target is currently the name alone, and the controls sit among the facts with nothing dividing them. Quality of life, and it makes room for the remote-control toggle.

**Independent Test**: Confirm the card has exactly one link, that it covers the whole readable block, that no control sits inside it, and that rename is absent from the fleet and present on the session's own page.

**Acceptance Scenarios**:

1. **Given** a card, **When** the operator clicks anywhere in the readable block, **Then** the session opens.
2. **Given** a card, **When** it renders, **Then** it contains exactly one link and no control inside it.
3. **Given** the fleet, **When** it renders, **Then** no rename control appears on any card.
4. **Given** a session's own page, **When** the operator asks to rename, **Then** a field appears, and it is not present until asked for.

---

### Edge Cases

- **A config file that will not parse at all.** The daemon must not start on a half-read configuration, and must not leave the operator without a way back.
- **A config file written by an older daemon.** Renamed keys must keep working, loudly, rather than being read as unknown and refused.
- **A settings page on a daemon with no config file.** It must say so rather than showing an empty page or inventing a path.
- **A remote-control toggle on a session in a directory shared with another session.** Resuming "the last conversation in this directory" may resume the wrong one.
- **A directory list rendered before a directory was deleted.** A path chosen from a stale list must be validated exactly as a typed one is.
- **An action performed with no scripting on a page that also has no live region.** Every page that can act must work on both paths.
- **A pane larger than any expectation.** The bound must be explicit rather than inherited from what tmux happens to return.
- **The operator's own identity removed from the allowlist while they are signed in.** They must not retain the ability to act from a loaded page.

## Requirements *(mandatory)*

### Functional Requirements

#### Configuration file

- **FR-001**: The daemon MUST read its configuration from a file, by default under the operator's own configuration directory.
- **FR-002**: The file format MUST support comments, because the reasoning for each setting is as valuable as the value and lives with it today.
- **FR-003**: A missing file MUST NOT be an error. Defaults and environment variables MUST still start a daemon, so every existing deployment keeps working.
- **FR-004**: An environment variable MUST override the file, so a container or a test can change one value without writing one.
- **FR-005**: An unrecognised key MUST refuse at startup, naming it. A misspelled containment boundary that silently does nothing is the failure this prevents.
- **FR-006**: A key the daemon has **renamed** MUST be accepted and warned about, naming both spellings — that is what distinguishes a typo from a version skew.
- **FR-007**: The daemon MUST refuse to start if a config file containing a secret is readable by group or world, naming the file and what to run.
- **FR-008**: The daemon MUST NOT rewrite the operator's file on its own. A file under source control that the daemon reformats is a change nobody asked for.
- **FR-009**: The daemon MUST provide a way to check a file without starting, and a separate explicit way to migrate one, keeping a backup.
- **FR-010**: If the configuration will not load, the daemon MUST fall back to the last known-good file and say so loudly — the only recovery that does not need shell access.
- **FR-011**: Every setting configurable by environment variable today MUST be configurable in the file.

#### Dependencies

- **FR-012**: The daemon MUST verify at startup that the tools it shells out to are present, and MUST refuse to start when the one it cannot work without is missing.
- **FR-013**: A missing dependency MUST be reported with the install command for the platform the daemon is running on, derived from the system's own identification rather than guessed.
- **FR-014**: The daemon MUST NOT install anything. It names the command; the operator runs it.
- **FR-015**: The check MUST read the **configured** start command rather than a fixed name, so a daemon configured to run something else is checked for that.

#### Settings view

- **FR-016**: An operator MUST be able to see every configured setting on a page.
- **FR-017**: A secret MUST be shown as present or absent, never as a value. This includes the shared secret and the allowlisted addresses.
- **FR-018**: The page MUST name the configuration file it read, or say that none was read.
- **FR-019**: The page MUST be behind the same identity check as every other browser page, and MUST produce exactly one audit record per view.
- **FR-020**: The page MUST NOT offer any way to change a setting in this milestone.

#### Actions without scripting

- **FR-021**: Every action MUST answer in a way that leaves the browser on a usable page when no script runs.
- **FR-022**: The outcome MUST be conveyed from a fixed set of values chosen by the daemon, never from caller-supplied text.
- **FR-023**: An unverified teardown MUST remain prominent rather than becoming one message among many.
- **FR-024**: The existing in-page behaviour MUST continue to work when scripting is available — this adds a floor rather than replacing the enhancement.
- **FR-025**: A refusal MUST NOT be answered with a redirect. Sending an unauthorised caller somewhere tells them their request was processed.

#### Remote control

- **FR-026**: An operator MUST be able to choose remote control as a mode when creating a session, rather than selecting a command by name.
- **FR-027**: An operator MUST be able to change that mode on a session that is already running.
- **FR-028**: Changing the mode MUST preserve the tmux session, its window and its scrollback, and MUST continue the existing conversation.
- **FR-029**: Changing the mode MUST require a deliberate confirming step, because it ends and restarts a running process.
- **FR-030**: The toggle MUST select between commands the operator configured. No command line may reach it from a browser, in either direction.
- **FR-031**: The session record MUST carry which mode it is in, and a card MUST show it.
- **FR-032**: Where "the last conversation in this directory" could resolve to another session's, the daemon MUST refuse rather than resume the wrong one.

#### Resuming a conversation

- **FR-033**: An operator MUST be able to see prior conversations for a working directory and choose one when starting a session.
- **FR-034**: The daemon MUST NOT read conversation contents. Only an identifier and a modification time may be shown or recorded.
- **FR-035**: Reading the conversation store is a new filesystem surface and MUST be narrow: a directory listing, no file contents, and no lookup at all for a directory that is not under an approved root.
- **FR-036**: A resumed conversation MUST still be a new session record with its own identifier, credential and lifetime.
- **FR-037**: Starting fresh MUST be the default, so a resume is always something the operator chose.

#### Working directory picker

- **FR-038**: An operator MUST be able to choose a working directory from a list.
- **FR-039**: Typing MUST filter that list.
- **FR-040**: A path not in the list MUST still be accepted in full.
- **FR-041**: The list MUST be configurable explicitly, and MUST also be discoverable from the approved roots behind a flag that is off by default.
- **FR-042**: A path chosen from the list MUST be validated exactly as a typed one is. The list is a convenience; the allowlist is the control.
- **FR-043**: The control MUST be usable with no scripting, degrading to the field that exists today.
- **FR-044**: The control MUST be operable by keyboard alone and MUST announce the filtered result to a screen reader.
- **FR-045**: When the control is showing a subset of matches, it MUST say so.

#### Card layout

- **FR-046**: A card MUST contain exactly one link, covering the whole readable block.
- **FR-047**: No control may sit inside that link.
- **FR-048**: A visible boundary MUST separate the readable block from the controls, and MUST NOT be the only cue that separates them.
- **FR-049**: Rename MUST NOT appear on the fleet.
- **FR-050**: Rename MUST appear on a session's own page, revealed on request rather than always present.
- **FR-051**: Selecting text inside the link MUST NOT navigate.

#### Pane bound

- **FR-052**: The size of a captured screen MUST be bounded explicitly, and the bound MUST be stated where it is relied upon.
- **FR-053**: A capture past the bound MUST be refused rather than truncated. Half a screen is a wrong screen, not a smaller one.

#### Carried forward

- **FR-054**: Zero third-party dependencies.
- **FR-055**: Every request to every new route produces exactly one audit record.
- **FR-056**: No new route weakens the uniform refusal or the uniform not-found.
- **FR-057**: No secret, token, prompt text, pane content, or conversation transcript in any log, audit record, or page.
- **FR-058**: Both halves of the cross-site defence apply to every mutating route and remain independently testable.
- **FR-059**: Nothing animates under a reduced-motion preference; no state is conveyed by colour alone; every control is keyboard-operable with a visible focus indicator.

### Key Entities

- **Configuration file**: The operator's own statement of how this daemon behaves. Readable, commented, under source control if they choose, and never rewritten without being asked.
- **Configuration source**: Which of file, environment, or default supplied each value — what the settings page shows and what makes "why is my change not applied" answerable.
- **Session mode**: Whether a session is reachable from claude.ai. A property of the record, changeable, shown on the card.
- **Prior conversation**: An identifier and a time, offered for resume. Never its contents.
- **Directory suggestion**: A convenience for the create form. Never an authorisation.

## Success Criteria *(mandatory)*

- **SC-001**: An operator can change any setting by editing one file and restarting, without editing a service definition.
- **SC-002**: A daemon with no configuration file starts and behaves exactly as it does today, verified against the existing acceptance suites unchanged.
- **SC-003**: A configuration file containing a mistake is refused at startup 100% of the time, naming the mistake — never accepted and silently ignored.
- **SC-004**: An operator can answer "why was my working directory refused?" from the settings page without reading a log.
- **SC-005**: No secret value appears on any page, in any response, or in any record, verified by searching a full exercise of every route.
- **SC-006**: All four actions leave the browser on a usable page with scripting disabled, verified per action.
- **SC-007**: Remote control can be turned on and off on a running session, and the conversation survives, verified by the session continuing to answer afterwards.
- **SC-008**: A working directory can be chosen without typing a path, and any path can still be typed.
- **SC-009**: A mistyped directory costs an operator one refusal rather than a search, because the list offers the right one.
- **SC-010**: Every card has exactly one link, and its clickable area covers the readable block rather than the name alone.
- **SC-011**: The dependency check turns a missing tool from a failed request into a refused start with an install command.
- **SC-012**: `go.sum` remains absent.
- **SC-013**: Every new page and route produces exactly one audit record per request, allowed or denied.

## Assumptions

- **The config file is a new source, not a new set of rules.** Validation, bounds and refusals are exactly those that exist today; only where a value comes from changes.
- **Environment variables remain supported indefinitely.** They are how a container is configured and how a test overrides one value; the file becomes the source of truth, not the only source.
- **The conversation store's location and naming are Claude Code's, not this daemon's.** Its layout is read as a fact about the host, and a directory with no store simply offers nothing.
- **One allowlisted identity in production.** The ownership check runs on every route regardless, and the synthetic second owner remains how cross-owner behaviour is proven.
- **Sessions survive a restart**, so a configuration change no longer costs the fleet — that is what makes a settings-driven restart tolerable at all.
- **The lane's turn budget is not the constraint this milestone works around.** Decomposition is. A task sized for one iteration is the unit that has always worked here.

## Out of Scope

Deliberately not in this milestone, so no task wanders into them:

- **Releases, versioning, an installer, and self-update** (#57, #68, #69, #66). They are distribution rather than operation, and they depend on this milestone's config file existing first.
- **Editing settings from the browser.** The read-only view ships here; writing the file from a page carries a list of safeguards long enough to be its own piece of work, and it is the highest-consequence surface in the product.
- **The rain's Easter eggs** (#54) and **the browser accessibility verification** (#17) — polish, and a task only a human with a browser can do.
- **The device-code login relay**, and the `needs-auth` state.
- **The companion Claude skill.**
- **Multi-user support** beyond one allowlisted identity and the ownership check that exists.
- **Any change to milestone 1's signing procedure, its six operations, or the audit record shape.**
