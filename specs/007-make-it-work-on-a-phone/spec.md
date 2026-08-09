# Feature Specification: Make it work on a phone

**Feature Branch**: `007-make-it-work-on-a-phone`

**Created**: 2026-08-09

**Status**: Draft

**Input**: A full mobile sweep, planned from an audit of every page, partial and
rule at a 390px viewport. The operator's report, verbatim: *"Right now mobile is
rough"* and *"Seeing settings is tricky right now as well"*.

---

## Why this milestone exists

This daemon exists so its operator can drive Claude Code sessions from a phone
while away from a desk. That is the whole premise: a session runs on a machine at
home, and the person who owns it is somewhere else, holding a phone.

Every surface was built and judged on a desktop.

The stylesheet has exactly one width breakpoint. It does four things — narrows the
shell's padding, drops the summary to two columns, hides one decorative tag, and
collapses the settings grid — and **not one of them is the reason mobile is
rough**. The breakpoint was written to stop the desktop layout breaking, not to
make a phone work.

The result is a product whose core use case is the one it serves worst.

---

## User Scenarios & Testing *(mandatory)*

### User Story 1 — Read what Claude said, from a phone (Priority: P1)

The operator is away from their desk. A session has been running; they want to
know what it has done and whether it is waiting on them. They open the session
page on their phone and read the pane.

**Why this priority**: This is the product's core screen and its reason to exist.
Everything else on the dashboard is navigation towards this moment.

Today the pane is 80 columns of terminal output in a window about 44 characters
wide. Reading a paragraph requires a horizontal pan **per line** — and because the
pane's scroll container does not contain its overscroll, panning at the edge can
chain into the browser's navigation gesture and throw the operator off the page
entirely, mid-read.

**Independent Test**: Open the session page at a 390px viewport with a session
whose pane holds ordinary prose output. The text can be read top to bottom without
horizontal panning, and panning at a scroll edge does not navigate away.

**Acceptance Scenarios**:

1. **Given** a session whose pane contains prose wider than the viewport, **When**
   the operator reads it at 390px, **Then** every line is reachable by vertical
   scrolling alone.
2. **Given** the same pane on a desktop viewport, **When** it renders, **Then**
   output is not wrapped and column alignment is preserved exactly as today.
3. **Given** a pane that still scrolls horizontally, **When** the operator pans to
   its edge and continues, **Then** the browser does not navigate.
4. **Given** any page, **When** the operator pinch-zooms, **Then** zooming works —
   the viewport declaration never clamps scale.

---

### User Story 2 — Read and change a setting, from a phone (Priority: P1)

The operator wants to check what the daemon is configured with, or change one
value. They open settings on their phone.

**Why this priority**: The operator reported this one unprompted, and the audit
confirms it is the worst surface in the product. It is also the page an operator
reaches for when something is already wrong, which is the worst moment to hand
someone a page they have to fight.

Three faults compound:

- The section menu **stacks above the panel** and consumes roughly the whole first
  screen. Every section is a fresh page load that lands at the top, so choosing a
  section means scrolling past the entire menu again to read the result.
- The horizontal scroll sits on the **wrapper holding both menu and panel**, so a
  wide table pans the whole settings area rather than just the table.
- Each section is a three-column table that cannot fit the available width, and on
  an editable row the input and its Save button push the row half again wider —
  so **editing a setting means typing inside a horizontally panning table**.

**Independent Test**: At a 390px viewport, open settings, switch sections, and
edit one editable value. The panel's content is visible without scrolling past the
menu, the menu never pans off with the table, and the input is fully visible while
being typed into.

**Acceptance Scenarios**:

1. **Given** the settings page at 390px, **When** it loads, **Then** the section
   content is reachable without scrolling past a full screen of menu.
2. **Given** a section whose values are wider than the viewport, **When** the
   operator pans that content, **Then** the section menu does not move with it.
3. **Given** an editable setting at 390px, **When** the operator focuses its
   input, **Then** the input and its Save control are both fully visible.
4. **Given** JavaScript is disabled, **When** the operator switches sections,
   **Then** every section remains reachable — the menu entries are real links and
   stay real links.
5. **Given** any section, **When** it renders, **Then** the current section is
   marked by more than colour alone.

---

### User Story 3 — Hit the control you aimed at (Priority: P2)

The operator taps a control with a thumb.

**Why this priority**: Every interactive control in the product except one fails
the 44px touch minimum — most are around 24px. The sharpest case: **Destroy sits
about 8px from Compact**, both around 24px tall. A mis-tap there ends an
unsandboxed shell the operator wanted to keep.

**Independent Test**: On a touch device, every control can be hit reliably; on a
desktop with a mouse, no control has grown.

**Acceptance Scenarios**:

1. **Given** a touch device, **When** any control renders, **Then** it presents at
   least a 44px touch target.
2. **Given** a session card on a touch device, **When** Destroy and Compact
   render, **Then** they are separated by more than the default spacing, and they
   remain distinguished by their labels rather than by colour.
3. **Given** a desktop viewport with a mouse, **When** the same controls render,
   **Then** their sizes are unchanged from today.
4. **Given** a large tablet in landscape — wider than the width breakpoint but
   still touch-operated — **When** controls render, **Then** they present touch
   targets. *Target size follows the pointer, not the viewport.*

---

### User Story 4 — Fill in a form without the page jumping (Priority: P2)

The operator renames a session, creates one, or edits a setting.

**Why this priority**: Every text input in the product is below the size at which
mobile browsers zoom the page on focus. The page zooms and pans on every single
form interaction, and the operator has to pinch back out afterwards. It happens
constantly and it makes the whole product feel broken.

**Independent Test**: On a phone, focusing any text input does not change the
page's zoom level.

**Acceptance Scenarios**:

1. **Given** any text input on a touch device, **When** the operator focuses it,
   **Then** the page does not zoom.
2. **Given** the same input on a desktop, **When** it renders, **Then** its size is
   unchanged from today.

---

### User Story 5 — See the whole session name and path (Priority: P3)

The operator looks at a session card to find out which session it is and where it
is running.

**Why this priority**: Names and working directories are truncated with the full
text held in a tooltip — and **a tooltip needs a hover, which a touch device does
not have**. On a phone the full name and the full working directory are simply
unreachable. That is data loss rather than a styling problem, which is why it is
here at all; it is P3 only because the truncated text usually carries enough to
identify the session.

**Independent Test**: At a 390px viewport with a session whose name and working
directory both exceed the card width, both are readable in full.

**Acceptance Scenarios**:

1. **Given** a session card at 390px whose name exceeds one line, **When** it
   renders, **Then** the full name is readable without hovering.
2. **Given** the same card on a desktop, **When** it renders, **Then** truncation
   behaves exactly as today.

---

### User Story 6 — A masthead that fits (Priority: P3)

**Why this priority**: Cosmetic, cheap, and visible on every page. The masthead
keeps desktop padding after the page content has narrowed, so the header is
visibly out of alignment with everything beneath it; and a longer operator
identity wraps the sticky bar onto two rows, spending scarce vertical space on a
header.

**Independent Test**: At 390px the masthead's content aligns with the page
content beneath it, and a long operator identity does not wrap the bar.

**Acceptance Scenarios**:

1. **Given** any page at 390px, **When** the masthead renders, **Then** its
   content aligns with the page content below it.
2. **Given** an operator identity long enough to overflow, **When** the masthead
   renders, **Then** it stays one row and the identity truncates.

---

### User Story 7 — Fix what the sweep found on the way (Priority: P3)

Three defects the audit surfaced that are not mobile-specific but were found by
looking properly at these surfaces for the first time.

**Why this priority**: Each is small, each is real, and one of them is a rule that
has never rendered on any device.

- **A style declaration that resolves to nothing.** The settings page's
  current-section tint is built from a value of the wrong kind. It is invalid, so
  it renders as nothing, and it has *always* rendered as nothing — on desktop as
  well. The marker survives only because the section is also marked in ways that
  do not depend on colour, which is exactly why nobody noticed.
- **Superseded rules that still match.** Styling from the settings page's previous
  flat-table design still applies to today's markup by descendant matching, and
  one rule targets an element the page no longer renders at all. The stylesheet
  sweep cannot see either, because it checks class names.
- **A template comment that contradicts its own file.** The settings template's
  header comment states the page has no form, no token, no action row and no live
  region. The page below it renders all four. In a pipeline where a fresh-context
  executor reads comments as contract, a false comment is a defect that costs an
  iteration.

**Independent Test**: The tint renders; no rule matches an element the page does
not produce; the comment describes the file it heads.

**Acceptance Scenarios**:

1. **Given** the settings page, **When** a section is current, **Then** its marker
   renders as designed — and remains marked by more than colour alone.
2. **Given** the stylesheet, **When** it is swept, **Then** no rule targets markup
   the page does not render.
3. **Given** the settings template, **When** its header comment is read, **Then**
   it describes the page that follows it.

---

### Edge Cases

- **What happens to output whose meaning is its alignment?** Tables, diffs and
  box-drawn interface chrome are misrepresented when wrapped. This is a known,
  accepted cost of US1 — see *Resolved decisions*. Pinch-zoom remains available,
  which is the operator's escape hatch, and it is why clamping scale is forbidden.
- **What happens when the section menu holds more entries than fit one row?** It
  scrolls. With no JavaScript nothing can scroll the current entry into view, so an
  operator deep in the list may land with the current entry offscreen. Accepted,
  recorded, and given a named fallback.
- **What happens to a screen reader when a data table is restyled into stacked
  rows?** The table's row and column semantics are weakened. The linearised order
  must read correctly on its own, and the removed column headers must not leave a
  value ambiguous.
- **What happens on a touch device wider than the breakpoint?** A tablet in
  landscape is touch-operated at a desktop width. Touch ergonomics must reach it,
  which is why they are conditioned on the pointer rather than the viewport.
- **What happens on a narrow desktop window?** It is a mouse at a phone width.
  Layout should adapt; targets should not inflate.
- **What happens with no JavaScript at all?** Everything in this milestone
  continues to work. Nothing here may introduce a script dependency.

---

## Requirements *(mandatory)*

### Functional Requirements

**The pane (US1)**

- **FR-001**: Below the width breakpoint, pane output MUST wrap rather than
  overflow horizontally, so that ordinary prose is readable by vertical scrolling
  alone.
- **FR-002**: Above the width breakpoint, pane output MUST remain unwrapped, with
  today's column alignment preserved exactly.
- **FR-003**: The pane MUST NOT chain its horizontal overscroll to the page or the
  browser, at any viewport.
- **FR-004**: No page may clamp the maximum zoom scale. Pinch-zoom MUST remain
  available on every page, at every viewport.

**Settings (US2)**

- **FR-005**: Below the width breakpoint, the settings section content MUST be
  reachable without scrolling past a full screen of section menu.
- **FR-006**: Horizontal overflow of a section's content MUST be scoped to that
  content. The section menu MUST NOT move with it.
- **FR-007**: Below the width breakpoint, a setting's value and its Save control
  MUST both be fully visible at the same time while the value is being edited.
- **FR-008**: The section menu MUST remain a list of real links, navigable and
  complete with no JavaScript.
- **FR-009**: The current section MUST be marked by at least one signal that is not
  colour, and that marker MUST render as designed at every viewport.
- **FR-010**: When a section's rows are restyled for narrow viewports, the
  linearised reading order MUST be key, then value, then provenance, and no value
  may be left ambiguous by the loss of its column header.

**Touch (US3, US4)**

- **FR-011**: On a touch-operated pointer, every interactive control MUST present a
  touch target of at least 44px in its block dimension.
- **FR-012**: Touch ergonomics MUST be conditioned on the pointer, not the
  viewport, so that a touch device wider than the breakpoint is served and a
  mouse-operated narrow window is not.
- **FR-013**: Touch ergonomics MUST NOT change layout — only target size, spacing
  and input scale.
- **FR-014**: On a touch-operated pointer, the destructive control on a session
  card MUST be separated from its neighbour by more than the default spacing, and
  the two MUST remain distinguished by label rather than by colour.
- **FR-015**: On a touch-operated pointer, text inputs MUST render at a size that
  does not trigger the browser's focus zoom.
- **FR-016**: Control sizes and input sizes MUST be unchanged on a mouse-operated
  pointer.

**Content reachability (US5, US6)**

- **FR-017**: Below the width breakpoint, a session's name and working directory
  MUST be readable in full without hovering.
- **FR-018**: Above the width breakpoint, truncation behaviour MUST be unchanged.
- **FR-019**: Below the width breakpoint, the masthead's content MUST align with
  the page content beneath it.
- **FR-020**: An operator identity too long for the masthead MUST truncate rather
  than wrap the bar to a second row.

**Correctness found on the way (US7)**

- **FR-021**: No style declaration may be built from a value of a kind it cannot
  accept. The settings current-section marker MUST render as designed.
- **FR-022**: No style rule may target markup the application does not render.
- **FR-023**: A template's header comment MUST describe the template it heads.

**Constraints that bind every requirement above**

- **FR-024**: The stylesheet MUST retain exactly one width breakpoint, at its
  current value. No requirement here justifies a second.
- **FR-025**: Media query range syntax MUST NOT be used anywhere. The guard on
  breakpoint count is a hook, and phrasing a query to slip past it is routing
  around a hook rather than satisfying it.
- **FR-026**: Every value introduced MUST come from the design system's tokens,
  including inside conditional blocks.
- **FR-027**: Every rule introduced MUST be reachable from markup the application
  actually renders.
- **FR-028**: Nothing introduced may animate under a reduced-motion preference.
- **FR-029**: Every control MUST remain keyboard-operable with a visible focus
  ring.
- **FR-030**: Nothing in this milestone may introduce a dependency on JavaScript.
- **FR-031**: Every requirement above that concerns what an operator SEES MUST be
  accompanied by a check that reads the rendered artefact — not by a claim that
  the change was made.

---

## Resolved decisions

Recorded here rather than left open, because each was argued during the audit and
re-litigating it in a task would cost an iteration.

### The pane wraps, and that is a trade rather than a fix

**Decision**: Below the breakpoint, pane output wraps, breaking within an
unbroken run where necessary. Overscroll is contained at all viewports. Zoom is
never clamped.

**What it costs, stated plainly**: Claude Code draws its own interface — box
borders, dividers, the input box — at full terminal width. Every one of those
wraps into a line plus a stub. Output whose meaning *is* its alignment (tables,
diffs) is misrepresented on a phone.

**What it buys**: Reading what Claude said stops requiring a horizontal pan per
line. That is the dominant task on a phone, and today it fails.

**Why not shrink to fit**: Fitting 80 columns into the available width needs a
font of roughly 6.9px. Even 60 columns needs about 9.2px. Both are below
legibility, and pinch-zoom then defeats the fit anyway.

**Why this is a safe decision to get wrong**: it is the cheapest reversible change
in the milestone — reverting is deleting one declaration.

**What is deliberately NOT done**: resizing the terminal to the reader's width.
That is the *correct* answer to terminal reflow, and it is out of scope: it is a
daemon change, and it reflows the session for every reader at once — a desktop
viewer, a companion tool, and the operator attached on the host may all be
watching at different widths. Named as future work; not started here.

### One width breakpoint stays one

**Decision**: The single width breakpoint is retained at its current value and its
guard is untouched. Every layout change in this milestone triggers at that same
threshold, and the layouts that do not need it are already intrinsic — they adapt
to available space with no query at all, which is the better tool and is already
in place.

**The consequence for tasks**: the guard counts occurrences, so *every*
width-conditional rule must live inside the block that already exists. A second
block fails the guard even at an identical width.

### Touch ergonomics are conditioned on the pointer

**Decision**: Touch target sizing and input scale are conditioned on the pointer
being coarse, not on viewport width.

**The argument**: a tablet in landscape is a touch device at a desktop width, and
the width breakpoint never reaches it. A narrow desktop window is a mouse, and
should not get inflated controls. **Target size is a property of the pointer, not
the viewport.**

**The policy this establishes, which the design system must state**: a
pointer-conditioned block changes ergonomics — size, spacing, input scale — and
**never layout**. That keeps the guard's intent honest: layout still has exactly
one axis of variation, and it is still tested.

### The settings menu stays a menu

**Decision**: Below the breakpoint the section menu becomes a single scrolling row
of the same real links, with the same current-section marking.

**Rejected**: a dropdown in a navigating form — works without JavaScript, but
invents a component this vocabulary does not have and demotes seven scannable
links into a closed popup. A disclosure that expands — works without JavaScript,
but adds a tap to every section switch. An accordion — changes the
one-section-per-request model the page is built on.

**The honest cost**: with seven entries the row scrolls, and with no JavaScript
nothing can scroll the current entry into view. An operator deep in the list may
land with the current entry offscreen.

### Settings rows stack

**Decision**: Below the breakpoint, each setting's key, value and provenance stack
vertically instead of sitting in three columns, giving the editable input and its
Save button the full width.

**The cost**: restyling table rows this way weakens the table semantics assistive
technology relies on. Mitigated by hiding the column headers accessibly rather
than removing them, and by a linear order that reads correctly on its own.

**The residual risk**: a bare provenance word beneath a value could momentarily
read as part of the value. Flagged for a device check rather than pre-emptively
solved — see below.

---

## What a test in this repository cannot settle

This project's design system is enforced by tests, and every task in this
milestone lands with one. That is the right mechanism and it has caught real
regressions.

**It is also not sufficient here, and saying so is part of the spec.** An
assertion that a declaration exists in a stylesheet proves the declaration exists.
It does not prove a page is usable on a phone. Nothing in this repository renders
CSS, and nothing in it has a thumb.

Three questions are therefore left deliberately open. Each ships, each is looked
at on a real device, and each has its fallback named **in advance** so that
answering it later is a decision rather than a redesign:

| Open question | How it gets answered | Fallback if the answer is bad |
|---|---|---|
| Does the wrapped pane read acceptably against Claude Code's real interface chrome? | Operator opens a live session on a phone after this ships | Revert one declaration; the pane returns to today's behaviour |
| Once rows are stacked, does a bare provenance word read as part of the value? | Operator reads a settings section on a phone | Label the provenance explicitly in the row |
| Does the scrolling section menu disorient when the current entry starts offscreen? | Operator switches sections on a phone | Replace the scrolling row with an expanding disclosure |

Recording these as unknowns is the point. **The failure this milestone must not
repeat is a green task list beside an unchanged experience** — which is what
happens when "the assertion passes" is allowed to stand in for "the operator can
use it".

---

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: At a 390px viewport, an operator can read a session's prose output
  end to end using vertical scrolling only — zero horizontal panning.
- **SC-002**: At a desktop viewport, pane output is byte-for-byte identical in
  presentation to today: no wrapping, alignment preserved.
- **SC-003**: At a 390px viewport, the settings page shows section content within
  the first screen, without scrolling past the menu.
- **SC-004**: At a 390px viewport, an operator can change one editable setting
  without any horizontal panning at any point in the interaction.
- **SC-005**: Every interactive control presents at least a 44px touch target on a
  touch-operated pointer — measured across every page, with zero exceptions.
- **SC-006**: Every control's size on a mouse-operated pointer is unchanged from
  today — zero desktop regressions.
- **SC-007**: Focusing any text input on a phone leaves the page's zoom level
  unchanged, in 100% of inputs across every form in the product.
- **SC-008**: A session's full name and full working directory are readable at
  390px without hovering.
- **SC-009**: Every page continues to work with JavaScript disabled — every route
  reachable, every form submittable, every section of settings visible.
- **SC-010**: The stylesheet contains exactly one width breakpoint, and no media
  query anywhere uses range syntax.
- **SC-011**: No style rule targets markup the application does not render, and no
  declaration is built from a value of a kind it cannot accept.
- **SC-012**: Every requirement about what an operator sees is verified by a check
  that reads the rendered artefact. Zero requirements are satisfied by assertion
  alone.
- **SC-013**: The three open questions above are each recorded with a named
  fallback before the milestone is called complete — none is silently resolved by
  assuming the answer is fine.

---

## Out of scope

Named so that no task wanders into them.

- **Resizing the terminal to the reader's viewport.** The correct answer to
  terminal reflow, and a daemon change with a genuine multiple-concurrent-readers
  problem. Future work.
- **A wrap or zoom toggle for the pane.** Permissible later as a progressive
  enhancement. It is a new component, new tokens and new tests for what is
  currently a guess about a preference, and it should not be built before the
  wrap has been used in anger.
- **A second width breakpoint**, and any change to the guard that enforces one.
- **Native applications, service workers, offline support, install prompts.** This
  is a website that must work on a phone.
- **Any change to routing, session semantics, or the security posture.** This
  milestone is a stylesheet, two comments, and documentation.
- **Auto-recovery of a crashed session.** Still deliberately unspecified.

---

## Assumptions

- **390px is the design target** for the narrow case — a common modern phone
  width. Nothing here is tuned to a specific device, and the layouts that matter
  adapt intrinsically rather than to a measured width.
- **44px is the touch minimum**, following the widely-adopted platform guidance.
  No smaller figure is defensible for a control that destroys a shell.
- **16px is the input size that avoids focus zoom**, following mobile browser
  behaviour. This is why the input token exists at all.
- **The operator is the only user.** There is no analytics, no device telemetry,
  and no way to learn what phone is in use except by asking. The three open
  questions above are answered by the operator looking, because there is no other
  mechanism and inventing one would be a larger project than this milestone.
- **Captured terminal output has its trailing whitespace trimmed**, so wrapping
  will not wrap blank padding — only lines with real content past the viewport
  wrap. This was verified in the capture path during the audit and is the
  precondition that makes FR-001 safe.
- **The design system's tests are the enforcement mechanism**, as the constitution
  requires. Every change here lands with one, and the limits of that mechanism are
  recorded above rather than assumed away.
