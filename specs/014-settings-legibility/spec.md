# Feature Specification: A Settings Page an Operator Can Read

**Feature Branch**: `014-settings-legibility`

**Created**: 2026-08-23

**Status**: Draft

**Input**: Operator: "I don't know if we need any of this in the update section — not sure what value it brings. Can we just make the sections Updates, General, Network. Can we not have save after every setting and be at the bottom. In the current Other section I have no idea why or what these settings do... same with the what it runs section. Do we really need to have a source column?"

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Three sections, and every setting is in one that fits (Priority: P1)

An operator opens settings and finds three headings — Updates, General, Network —
instead of seven, and no setting sitting under a heading that means "we did not
classify this".

**Why this priority**: "Other" is the complaint and it is also a defect: three
keys land there because the section map names keys that do not exist and misses
keys that do. An operator reading their own configuration should never meet a
group called "everything else".

**Independent Test**: Open settings; count the headings; confirm no setting is
unclassified and that every configured key appears exactly once.

**Acceptance Scenarios**:

1. **Given** the settings page, **When** it renders, **Then** it shows exactly
   three sections: Updates, General, Network.
2. **Given** any key the daemon configures, **When** the page renders, **Then**
   that key appears under one of the three and never under a fallback heading.
3. **Given** a key added to the configuration later, **When** nothing claims it,
   **Then** it still appears on the page rather than vanishing, and the page says
   plainly that it is unclassified.

---

### User Story 2 - One Save, at the bottom (Priority: P1)

An operator changes two settings and presses Save once.

**Why this priority**: it is the second thing asked for and it is what makes the
page feel like a form rather than a list of forms.

**Independent Test**: Change two values, submit once, confirm both are written and
nothing else was.

**Acceptance Scenarios**:

1. **Given** the settings page, **When** it renders, **Then** there is one Save
   control, at the end of the form, and no per-row Save.
2. **Given** two changed values, **When** Save is pressed, **Then** both are
   written in one request.
3. **Given** values the operator did not touch, **When** Save is pressed, **Then**
   those keys are not written at all — not rewritten with the same value.
4. **Given** a secret rendered as a statement that it is set rather than as its
   value, **When** Save is pressed without touching it, **Then** the secret is
   unchanged and its rendered placeholder is never written into it.
5. **Given** a browser with no scripting, **When** Save is pressed, **Then**
   scenarios 2–4 still hold.

---

### User Story 3 - The update section says only what an operator can act on (Priority: P2)

An operator opening Updates sees whether an update is available and what taking
one would do — not three lines restating that the host is in its normal state.

**Why this priority**: it is the first thing asked about, but it is a smaller
change than the two above and touches no configuration.

**Independent Test**: On a host in the ordinary state, confirm the section carries
no unit prose. On a host where a newer unit is waiting, confirm it does.

**Acceptance Scenarios**:

1. **Given** a host whose unit is the one the daemon installed and nothing newer
   is waiting, **When** Updates renders, **Then** it states no unit facts.
2. **Given** a host where a newer unit is waiting, **When** Updates renders,
   **Then** it says so, because that is a thing the operator may take or leave.
3. **Given** a host running under a hardening override, **When** an update is
   available, **Then** the page says the override will not be touched — the one
   moment that fact changes what an operator expects to happen.
4. **Given** any of it, **When** an operator wants the detail anyway, **Then** it
   is reachable on the page rather than deleted.

---

### User Story 4 - Where a value came from, without a column for it (Priority: P3)

An operator reads a settings table of keys and values. Where a value came from
somewhere surprising, the page says so; where it came from the ordinary place, it
does not spend a column saying so.

**Why this priority**: the smallest of the four, and the one whose current cost is
only width.

**Independent Test**: Render a page whose values come from the configuration file
and confirm no source column; render one where a value came from the environment
and confirm the page still says so.

**Acceptance Scenarios**:

1. **Given** every value read from the configuration file, **When** the table
   renders, **Then** there is no Source column.
2. **Given** a value that came from somewhere other than that file, **When** the
   table renders, **Then** the page still says where, on that row.

---

### Edge Cases

- **A key nobody classified.** The page must still show it. A settings page that
  silently drops a key is worse than one with an ugly heading.
- **A submit that changes nothing.** Pressing Save without editing must write
  nothing and say so, rather than reporting a change that did not happen.
- **One value in a batch is refused.** The others must not be silently written or
  silently dropped; the operator has to be told which failed.
- **A secret whose new value is the same word the placeholder uses.** Rare, and it
  must not be silently ignored — the page has to be honest that it cannot tell.
- **Two operators saving at once.** The later write wins, as it does today; this
  feature does not add a lock it cannot honour.

## Requirements *(mandatory)*

### Functional Requirements

#### Sections

- **FR-001**: The page MUST group settings under exactly three headings: Updates,
  General, Network.
- **FR-002**: Every configured key MUST appear on the page exactly once.
- **FR-003**: No key MUST be assigned to a heading meaning "unclassified" while a
  fitting one exists; the keys currently falling through MUST be classified.
- **FR-004**: A key that nothing claims MUST still render, under a heading that
  says plainly it is unclassified, so the page cannot lose one silently.

#### Saving

- **FR-005**: The page MUST carry exactly one Save control, at the end of the
  form.
- **FR-006**: One submit MUST be able to change more than one setting.
- **FR-007**: A key whose submitted value is unchanged MUST NOT be written.
- **FR-008**: A secret rendered as a statement rather than a value MUST NOT be
  written when it is submitted unchanged, and the statement MUST never become the
  stored value.
- **FR-009**: FR-006 through FR-008 MUST hold with scripting unavailable.
- **FR-010**: A submit that changes nothing MUST say so.
- **FR-011**: When some values are written and others refused, the page MUST name
  what was refused and MUST NOT report a wholesale success.

#### Updates

- **FR-012**: The update section MUST state unit facts only when they change what
  taking an update would do — a newer unit waiting, or an override that an update
  will not touch while an update is available.
- **FR-013**: The detail MUST remain reachable on the page rather than removed.

#### Source

- **FR-014**: The settings table MUST NOT carry a Source column.
- **FR-015**: A value that did not come from the configuration file MUST still be
  marked as such on its own row.

### Key Entities

- **Setting**: unchanged — a key, a value, an origin, and whether it is editable.
- **Section**: three now instead of seven, plus a fallback that exists to be
  empty.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: The page shows three headings, and no setting is unclassified.
- **SC-002**: An operator changing three settings presses Save once.
- **SC-003**: A submit writes exactly the keys whose values changed, and no
  others — verifiable by the audit trail naming only those keys.
- **SC-004**: A secret is never overwritten by the text the page rendered in its
  place, under any submit.
- **SC-005**: On an ordinary host, the update section carries no unit prose; on a
  host with a newer unit waiting, it does.
- **SC-006**: The settings table is three columns rather than four, and a value
  from the environment is still identifiable.

## Assumptions

- **"Other" is a defect, not a category.** `start_command`,
  `remote_control_command` and `session_environment` fall through because the
  section map names keys that do not exist. They are meaningful settings that were
  misfiled, so they are classified rather than hidden.
- **Nothing is hidden from the operator.** The request wondered whether some
  settings need exposing at all. This feature moves and regroups them; a setting
  the daemon reads is one its operator may see, and hiding configuration is a
  different decision from tidying it.
- **The single Save is decided on the server, not in script.** Ignoring unchanged
  values is a rule the route applies, so it holds with scripting off — which is
  what makes FR-009 possible at all.
- **Per-row saving was deliberate and its reasons are answered rather than
  overruled.** It avoided re-submitting untouched values and avoided putting a
  secret in a POST body. FR-007 and FR-008 keep both properties under one button.
- **The update section keeps its actionable state.** One of the unit's four
  sentences offers the operator something to do; that one stays.
