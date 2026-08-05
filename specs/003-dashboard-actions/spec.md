# Feature Specification: Dashboard Actions

**Feature Branch**: `003-dashboard-actions`

**Created**: 2026-08-05

**Status**: Draft

**Input**: Milestone 3 — the read-only fleet dashboard from milestone 2 becomes able to act: create, destroy, rename and compact a session from the browser. Must answer the cross-site-request question milestone 2 deliberately parked, and resolve issue #15 (the fleet never refreshes).

## Why this milestone is different

Milestones 1 and 2 added capability to a surface that was already safe. This one changes what the surface *is*.

Every mutating route in the daemon today requires an HMAC signature over the request line and body, keyed with a secret the browser does not have and must never be given. The dashboard's reads are authorised by an ambient Cloudflare Access cookie, and that is safe **only because of** the signature requirement: a hostile page can make a visitor's browser send a request with its Access cookie attached, but it cannot make that browser produce a signature.

The first browser-driven write ends that. An ambient credential plus a mutating route is the definition of cross-site request forgery, and milestone 2 recorded the debt explicitly rather than letting milestone 3 inherit the old reasoning by assumption:

> *This milestone's streams are authorised by an ambient cookie, which is safe **only** because every mutating route requires a signature a browser cannot produce. That reasoning does not survive the first browser-driven write, and milestone 3 must not inherit it by assumption.*

Paying that debt is the first requirement of this milestone, not a hardening task appended to it.

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Destroy a session from the browser (Priority: P1)

An operator sees a session on the fleet that should not be running — wrong directory, finished work, or simply stuck. They destroy it from the card, without a terminal and without a signing helper, and the card tells them the truth about what happened.

**Why this priority**: It is the action most often wanted away from a desk, and it is the highest-consequence write in the system — it ends an unsandboxed shell. Any cross-site defence that is correct for destroy is correct for the rest, so this story is the one that proves the security model. Shipped alone it is already useful: a phone can stop a runaway session.

**Independent Test**: Load the dashboard as the allowlisted identity, destroy a session, and confirm the tmux session is gone from the daemon's own tmux server and the card no longer claims it is running. Separately, replay the same request from a page on another origin and confirm it is refused and audited.

**Acceptance Scenarios**:

1. **Given** a running session owned by the signed-in identity, **When** the operator destroys it from its card, **Then** the session is torn down with verified teardown, the record and credential hash are cleared, and the card reflects that it is gone.
2. **Given** a destroy that tmux cannot confirm, **When** the daemon checks liveness, **Then** the operator is told the teardown could not be verified, the record is retained, and the failure is audited prominently — the browser gets the same honesty the API gets today.
3. **Given** a page on any other origin, **When** it causes a visitor's browser to send a destroy request carrying the Access cookie, **Then** the daemon refuses it and records the refusal, and no session is destroyed.
4. **Given** a session belonging to another owner, **When** a destroy is attempted for its identifier, **Then** the response is byte-identical to the response for an identifier that does not exist.

---

### User Story 2 - Start a session from the browser (Priority: P2)

An operator starts new work from the dashboard: they name it, choose a working directory from the approved roots, and the session appears on the fleet.

**Why this priority**: It closes the loop — with destroy alone the dashboard can only reduce the fleet. It is second rather than first because creating is the action least likely to be needed urgently away from a desk, and because it introduces input validation that destroy does not.

**Independent Test**: Create a session from the dashboard, confirm it appears on the fleet and is live on the daemon's tmux server, then confirm that a working directory outside the approved roots is refused with nothing created.

**Acceptance Scenarios**:

1. **Given** the create control, **When** the operator submits a valid name and an approved working directory, **Then** a session starts and appears on the fleet.
2. **Given** a name containing characters the daemon rejects, or a directory outside the approved roots, **When** submitted, **Then** the request is refused, nothing is created, and the operator is told which field was wrong without being told anything about the filesystem beyond that.
3. **Given** the concurrent-session cap is reached, **When** a create is submitted, **Then** it is refused with a message an operator can act on, and existing sessions are unaffected.
4. **Given** a session is created from the browser, **When** the response is rendered, **Then** the per-session bearer token is **not** displayed, stored in the page, or written to any log — the browser is not a credential holder.

---

### User Story 3 - The fleet stays current (Priority: P3)

An operator leaves the dashboard open. Sessions that start, end, or change state elsewhere — from the API, the reaper, or another operator's action — appear and disappear without a manual reload.

**Why this priority**: This is issue #15, and it becomes materially worse once actions exist: a dashboard that can act but shows a stale fleet will offer controls for sessions that are already gone. It is P3 rather than P1 because each individual action can update its own card, so stories 1 and 2 are usable without it.

**Independent Test**: Open the dashboard, create and destroy a session entirely through the API, and confirm the open page reflects both changes without interaction.

**Acceptance Scenarios**:

1. **Given** an open dashboard, **When** a session is created by any means, **Then** it appears on the fleet without a reload.
2. **Given** an open dashboard, **When** the reaper destroys an idle session, **Then** that card leaves the fleet without a reload.
3. **Given** an open dashboard and a daemon restart, **When** the connection is lost, **Then** the page says so plainly rather than continuing to display a fleet it can no longer vouch for.
4. **Given** an operator who prefers no motion, **When** the fleet updates, **Then** the change does not animate.

---

### User Story 4 - Rename a session (Priority: P4)

An operator gives a session a name that means something, or corrects one that does not.

**Why this priority**: Genuine quality-of-life on a fleet of more than two or three sessions, especially for adopted sessions that arrive without a meaningful name. It changes no security surface beyond the shared write path, so it is cheap once story 1 is done.

**Independent Test**: Rename a session, confirm the new name shows on its card and in the API listing, and confirm the underlying tmux session and every identifier-based operation are unaffected.

**Acceptance Scenarios**:

1. **Given** a session, **When** the operator renames it to a valid name, **Then** the new name appears on the fleet and in the API listing.
2. **Given** a rename to a name the daemon rejects, **When** submitted, **Then** it is refused and the existing name is unchanged.
3. **Given** a renamed session, **When** any identifier-based operation runs against it, **Then** it behaves exactly as before the rename.

---

### User Story 5 - Compact a session (Priority: P5)

An operator reduces a long-running session's accumulated context without losing the session.

**Why this priority**: Lowest because it is the least understood of the four and the only one whose meaning is not already fixed by the daemon's existing vocabulary — see Question 2. It is also the only one that is purely an optimisation: nothing is blocked by its absence.

**Independent Test**: Compact a session and confirm it survives, remains drivable, and the intended reduction actually occurred.

**Acceptance Scenarios**:

1. **Given** a running session, **When** the operator compacts it, **Then** the session remains alive and drivable afterwards.
2. **Given** a session that is not in a state where compaction is meaningful, **When** compaction is requested, **Then** the operator is told plainly rather than the request appearing to succeed.

---

### Edge Cases

- **A hostile page targets a mutating route.** A request arriving with a valid Access cookie but no evidence it originated from the dashboard must be refused and audited. This is the milestone's central case, not a footnote.
- **A stale control.** An operator clicks destroy on a card for a session that has already ended. The result must be the same uniform response an unknown identifier gets — a control going stale must not become an oracle for which identifiers exist.
- **A repeated click.** Two destroys for the same session, or a double-submitted create, must not produce two teardowns or two sessions.
- **Teardown cannot be verified.** The browser path must surface the same honesty the API has, because an operator needs to know a live unsandboxed shell may have survived.
- **The cap is reached mid-session.** A create submitted when the concurrent cap is full must be refused without disturbing existing sessions.
- **The identity is no longer allowed.** An operator whose Access session expires, or whose identity is removed from the allowlist, must not retain the ability to act from an already-loaded page.
- **A rename collides.** Two sessions carrying the same display name must not become ambiguous anywhere the name is used to address a session — noting that the daemon addresses sessions by identifier, never by name.
- **The fleet connection drops.** A page whose live updates have stopped must say so rather than silently showing an old fleet.
- **An action arrives for another owner's session.** Every action must apply the ownership check the read paths already apply, and answer identically to a session that does not exist.

## Requirements *(mandatory)*

### Functional Requirements

#### Cross-site request defence

- **FR-001**: The daemon MUST refuse any browser-authenticated mutating request that does not carry positive evidence it was initiated by the dashboard itself. An ambient Cloudflare Access cookie MUST NOT be sufficient authorisation for any state change.
- **FR-002**: That evidence MUST be **two independent checks, both required**:
  - **FR-002a**: The request's `Origin` MUST match the dashboard's own origin. A request carrying **no** `Origin` header MUST be refused rather than exempted — an absent header is not evidence of same-origin, and treating it as such would make the check optional for anything that can omit it.
  - **FR-002b**: The request MUST carry a token minted into the page that issued it, bound to the authenticated Access identity (FR-007) and invalidated with that identity's session (FR-008).
- **FR-002c**: Neither check alone may authorise a state change, and each MUST be independently removable in a test that then fails. Two checks that cannot be shown to work separately are one check with extra steps.
- **FR-003**: A mutating request missing the required evidence MUST be refused **before** any state changes, and the refusal MUST be audited.
- **FR-004**: The refusal MUST NOT disclose which check failed, consistent with the uniform failure responses milestone 1 established.
- **FR-005**: The HMAC-signed API path MUST continue to work unchanged. This milestone adds a second way to authorise a write; it does not alter or weaken the first.
- **FR-006**: The shared signing secret MUST NOT be sent to the browser, embedded in any page, or derivable from anything the browser receives.
- **FR-007**: Any credential minted for the browser MUST be bound to the authenticated Access identity, so that a value issued to one identity is not usable by another.
- **FR-008**: Any credential minted for the browser MUST become invalid when the Access session that produced it is no longer valid.

#### Actions

- **FR-009**: An operator MUST be able to destroy a session from the dashboard.
- **FR-010**: Destroy from the browser MUST use verified teardown, and MUST report unverified teardown to the operator rather than reporting success.
- **FR-011**: An operator MUST be able to create a session from the dashboard, supplying a name and a working directory.
- **FR-012**: Create from the browser MUST apply the same name and working-directory validation the API applies, including the approved-roots restriction and symlink resolution.
- **FR-013**: The per-session bearer token MUST NOT be displayed in the browser, embedded in the page, or persisted client-side when a session is created from the dashboard.
- **FR-014**: An operator MUST be able to rename a session.
- **FR-015**: Rename MUST change only the session record's display name. It MUST NOT change the underlying tmux session name, because that name is derived from the immutable identifier and every operation targets it.
- **FR-016**: An operator MUST be able to compact a session. Compact means **delivering Claude Code's own `/compact` command into the session**, using the same byte-for-byte delivery mechanism prompts already use.
- **FR-016a**: Because the daemon cannot inspect what the assistant is carrying, it MUST NOT claim the compaction succeeded. It may report only what it can observe: that the command was delivered and the session is still alive and drivable.
- **FR-016b**: The delivered text MUST NOT appear in any audit record or log line, exactly as prompt text does not.
- **FR-017**: Every action MUST apply the ownership check on the target session, and MUST answer a not-owned session identically to one that does not exist.
- **FR-018**: A repeated or double-submitted action MUST NOT produce a duplicated effect.

#### Fleet currency

- **FR-019**: An open dashboard MUST reflect sessions appearing, disappearing, and changing state without operator interaction, delivered by a **fleet-level event stream**.
- **FR-019a**: That stream is a new authenticated route and MUST have a written contract before it is built, as every other route in this daemon does.
- **FR-019b**: The stream MUST be authorised exactly as the dashboard's other reads are, and MUST emit only changes to sessions the authenticated identity owns. Being a stream rather than a page MUST NOT become a way to observe another owner's fleet.
- **FR-020**: A dashboard that can no longer receive updates MUST say so, rather than displaying a fleet it cannot vouch for.
- **FR-021**: Fleet updates MUST be scoped to the authenticated identity's own sessions, exactly as the initial page render is.
- **FR-022**: Fleet updates MUST NOT animate when the operator has expressed a preference for reduced motion.

#### Auditing and disclosure

- **FR-023**: Every request to every new route MUST produce exactly one audit record, allowed or denied.
- **FR-024**: Each action MUST be distinguishable in the audit log from every other action, and a browser-initiated action MUST be distinguishable from the API-initiated equivalent.
- **FR-025**: No audit record or log line may contain the shared secret, a bearer token, any browser-issued credential, prompt text, or pane content.
- **FR-026**: A refused cross-site attempt MUST be recorded with enough detail for an operator to recognise it as an attack rather than a mistake, while still satisfying FR-025.

#### Interface

- **FR-027**: Action controls MUST NOT be nested inside the card's existing link. The card carries exactly one anchor today and that must remain true.
- **FR-028**: Every action control MUST be reachable and operable by keyboard alone, with a visible focus indicator.
- **FR-029**: Destroy MUST require a deliberate confirming step, because it ends an unsandboxed shell and cannot be undone.
- **FR-030**: An action's outcome MUST be conveyed as text, never by colour alone.
- **FR-031**: An action in progress MUST be distinguishable from one that has completed, and a failed action MUST state that it failed rather than silently reverting.

#### Constraints carried forward

- **FR-032**: The daemon MUST continue to have zero third-party dependencies.
- **FR-033**: No new route may weaken the uniform response for unknown, not-owned, or wrongly-credentialed requests.
- **FR-034**: Session records MUST remain in memory only; nothing in this milestone introduces on-disk persistence.

### Key Entities

- **Browser action credential**: Whatever value proves a mutating request came from the dashboard rather than a hostile page. Bound to an Access identity, invalidated with that identity's session, never written to a log, and never the shared secret.
- **Action outcome**: What the operator is told after acting — succeeded, refused with a reason they can act on, or *could not be verified*, which is distinct from failure and matters most for destroy.
- **Fleet update**: A change to the set of sessions an identity owns, or to one session's state, delivered to an already-loaded dashboard.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: A cross-origin page cannot cause any state change, for any of the four actions, using only a valid ambient Access credential. Demonstrated by an explicit test per action.
- **SC-001a**: Each of the two checks is independently load-bearing: disabling the `Origin` check alone, or the page-token check alone, causes a test to fail. A defence whose halves are never tested separately is one check wearing two names.
- **SC-002**: 100% of mutating browser requests missing the required evidence are refused before any state change and appear in the audit log.
- **SC-003**: An operator can destroy a session from a phone, with no terminal and no signing helper, in under 15 seconds from opening the dashboard.
- **SC-004**: An operator can start a session from the browser in under 60 seconds, including choosing a directory.
- **SC-005**: A destroy that cannot be verified is reported as unverified to the operator in 100% of cases, and never as success.
- **SC-006**: A session created or destroyed by any means appears or disappears on an open dashboard within 5 seconds, with no interaction.
- **SC-007**: Every action is completable using only a keyboard, verified on every control.
- **SC-008**: Every action produces exactly one audit record; a full exercise of every action, allowed and denied, contains no secret, token, credential, prompt text, or pane content.
- **SC-009**: Actions against another owner's session are byte-identical to actions against an identifier that does not exist, for all four actions.
- **SC-010**: The dependency count remains zero.
- **SC-011**: A dashboard whose update channel has dropped says so within 30 seconds rather than displaying a stale fleet.
- **SC-012**: Renaming a session leaves 100% of identifier-based operations behaving exactly as before.

## Assumptions

- **Rename does not touch tmux.** The daemon addresses tmux sessions as `crswd-<identifier>`, never by display name, so a rename is a record-only change. This is read from the existing implementation rather than assumed, and it is why FR-015 can be stated flatly.
- **One allowlisted identity in production.** The ownership check runs on every action anyway, and the synthetic second owner used in existing tests remains the way cross-owner behaviour is proven. A check removed because it always passes is a check that will be missing when it stops always passing.
- **Cloudflare Access remains layer 1 for the browser.** This milestone does not revisit how a browser authenticates, only what it is allowed to do once authenticated.
- **The Access cookie's own attributes are not ours to set.** It is issued by Cloudflare under its own domain policy, so any defence resting on that cookie's `SameSite` value would depend on something this project does not control and cannot test. That is a substantive constraint on Question 1, not a detail.
- **Existing per-session streams are unchanged.** Whatever answers FR-019 is additive; the pane stream contract from milestone 2 stays as it is.
- **The concurrent-session cap and rate limits already exist** and apply to browser-initiated creates without change.
- **No new persistence.** The fleet, its records, and any browser credential live in memory and do not survive a restart.

## Out of Scope

- Relaying Claude Code's own device-code login — milestone 4.
- The companion Claude skill.
- Multi-user support beyond one allowlisted identity and the ownership check that exists.
- Persisting session records, dashboard state, or output history to disk.
- Any change to milestone 1's signing procedure, its operations, or the audit record shape.
- Sending arbitrary prompt text from the browser. This milestone adds four named actions; a general "type into the session" control is a larger surface with its own questions and is not part of it.
- Editing a session's working directory after creation.
- Bulk actions across multiple sessions at once.

---

## Glossary

Terms this specification uses in a fixed sense. They are recorded because each one has an
ordinary-English reading that is subtly wrong here, and an implementer who applies the ordinary
reading will produce something that passes review and fails the requirement.

| Term | Means exactly | Does **not** mean |
|---|---|---|
| **Uniform response** | Byte-identical body, status, and headers — including `Content-Length`. Two responses that differ only in length are distinguishable, and a difference an attacker can measure is a disclosure | "Similar", "the same kind of error", or "also a 404" |
| **Verified teardown** | The host was asked, after the kill, whether the session still exists, and answered that it does not. Only an affirmative absence counts | The kill command returned without error |
| **Could not be verified** | A third outcome, distinct from success and from failure. The session may still be alive | A failure, or something to retry silently |
| **Ownership check** | Comparing the target session's owner against the authenticated identity on *every* request, including when production has one identity | A filter applied when listing |
| **Mutating request** | Any request that changes daemon or host state: create, destroy, rename, compact | Only requests using a particular HTTP method |
| **Positive evidence** | Something the request affirmatively carries that a cross-origin page cannot produce. Absence is never evidence | The absence of something suspicious |
| **Bound to identity** | Usable only by the identity it was minted for; presenting it as another identity fails | Issued while that identity was signed in |
| **Delivered** (of `/compact`) | The bytes reached the session. Nothing about what the session then did | Compaction happened |

## Anti-Requirements

Things that must **not** be built. Each is listed because it is a plausible, well-intentioned thing
to add while implementing something nearby — the failure mode is helpfulness, not carelessness.

- **AR-001**: Do not add a route, parameter, or response field that reports whether a session
  identifier exists. Every "does this exist" answer must go through the uniform response.
- **AR-002**: Do not make any error message more specific in order to be more helpful. FR-004 is a
  requirement, not a placeholder for better copy.
- **AR-003**: Do not store the per-session bearer token anywhere the browser can reach, including
  page state, local storage, and any response body other than the API's create response.
- **AR-004**: Do not add a "force" or "skip verification" path to destroy. The unverified outcome
  exists to be reported, not routed around.
- **AR-005**: Do not weaken, bypass, or make conditional either half of the cross-site defence,
  including for same-origin requests, development builds, or tests. Tests must satisfy the checks,
  not disable them.
- **AR-006**: Do not add retry logic that re-sends a mutating request. A retried destroy is a second
  destroy.
- **AR-007**: Do not log, audit, or include in an error the value of any browser credential, the
  shared secret, prompt text, pane content, or the text delivered by compact.
- **AR-008**: Do not refactor, rename, or "tidy" code outside the task at hand, however obvious the
  improvement. Constitution IV governs this.
- **AR-009**: Do not introduce a third-party dependency for CSRF handling, session management,
  templating, or streaming. `go.sum` must remain absent.
- **AR-010**: Do not make the fleet stream a general-purpose event channel. It carries changes to
  the authenticated identity's own sessions and nothing else.

## Verification Map

Every requirement is verifiable, and this states how. It exists so that task generation is
mechanical rather than interpretive: a task can be written directly from a row, and a reviewer can
check the row rather than re-deriving the intent.

| Requirements | Verified by | Must fail when |
|---|---|---|
| FR-001, FR-002a, FR-003 | A request per action carrying a valid identity credential but a foreign `Origin` | The `Origin` check is removed |
| FR-002b | A request per action carrying a valid identity credential and correct `Origin` but no page token | The token check is removed |
| FR-002c, SC-001a | Two tests, each disabling exactly one half of the defence | Either half is load-bearing only in combination |
| FR-004 | Byte comparison of refusal responses across every distinct failure cause | Any cause produces a distinguishable response |
| FR-005 | The existing milestone 1 API suite, unchanged and still passing | Any signed-API behaviour changes |
| FR-006 | A search of every byte served to the browser — pages, assets, stream frames — for the secret | The secret reaches the browser by any path |
| FR-007 | A credential minted for one identity, presented as a second | It is accepted for the second identity |
| FR-008 | A credential presented after its identity's session ends | It is still accepted |
| FR-009, FR-010 | Destroy via the browser path against a host that reports survival | Success is reported instead of unverified |
| FR-011, FR-012 | Browser create with: a rejected name, a directory outside the approved roots, a symlink escaping them, a non-directory | Any is accepted, or a session is created |
| FR-013 | Inspection of the create response and rendered page for the token | The token appears anywhere client-side |
| FR-014, FR-015, SC-012 | Rename, then every identifier-based operation against the session | Any operation changes behaviour, or the tmux name changes |
| FR-016, FR-016a | Compact, then confirm the session is alive and drivable | Success of the compaction itself is claimed |
| FR-017, SC-009 | Every action against the synthetic second owner's session, byte-compared against an unknown identifier | Any action distinguishes the two |
| FR-018 | The same action submitted twice concurrently | Two effects occur |
| FR-019, FR-019b | A second identity's session created while the first identity's stream is open | It appears on the wrong stream |
| FR-020, SC-011 | The stream severed, then the page inspected | The page continues to present the fleet as current |
| FR-022 | Fleet update rendered under a reduced-motion preference | Anything animates |
| FR-023, FR-024, SC-008 | A full exercise of every action, allowed and denied, then the audit log parsed | Any request produces zero or two records, or a browser action is indistinguishable from its API equivalent |
| FR-025, FR-026, AR-007 | The same audit capture searched for every forbidden value | Any appears |
| FR-027 | Count of anchors in the card template | It exceeds one |
| FR-028, SC-007 | Keyboard-only traversal of every action control | Any control is unreachable or has no visible focus |
| FR-029 | Destroy attempted without the confirming step | It proceeds |
| FR-030, FR-031 | Outcome rendering inspected for text content and for the in-progress, complete, and failed states | An outcome is colour-only, or a failure renders as a revert |
| FR-032, SC-010, AR-009 | Presence of `go.sum` | It exists |
| FR-033 | The milestone 1 and 2 uniform-response suites, re-run | Any weakens |
| FR-034 | Restart with sessions present | Anything survives that should not |

## Resolved Decisions

Three decisions were the operator's rather than the specification's. All three are answered; the
reasoning is kept because a decision without its rejected alternatives is a decision that gets
relitigated.

| # | Decision | Chosen | Why, and what was rejected |
|---|---|---|---|
| D1 | Cross-site defence (FR-002) | **Origin check *and* a per-page token bound to identity** | Defence in depth, for the reason the constitution gives it: a request that passes authentication here is unsandboxed code execution. *Origin alone* was rejected because a single header is then the entire defence, and a future proxy that strips or rewrites it removes that defence silently. *Token alone* was rejected as strictly weaker than having both for a few extra lines. A **`SameSite` cookie policy was never available**: the Access cookie is issued by Cloudflare under its own domain policy, so a defence resting on it would depend on something this project does not control and cannot test. Any scheme where the **page computes an HMAC is excluded outright** — it would mean shipping the layer-2 secret to the browser, which FR-006 forbids and which would hand out the API |
| D2 | What "compact" means (FR-016) | **Deliver Claude Code's own `/compact` into the session** | It is the meaning an operator asking to "compact" actually intends, and it reuses the byte-for-byte delivery prompts already use. The cost is accepted deliberately: the daemon cannot verify it worked, so FR-016a forbids claiming it did. *Clearing scrollback* was rejected as fully verifiable but the wrong thing — it reduces what the pane holds, not what the assistant is carrying. *Both together* was rejected as two effects behind one button where only one can be verified |
| D3 | Fleet currency (FR-019, issue #15) | **A fleet-level event stream** | Changes arrive promptly and only when something happens, reusing the streaming approach milestone 2 proved. It is a new authenticated route, so FR-019a requires a written contract before it is built — this repo has never added one without. *Periodic refresh* was rejected as steady traffic whether or not anything changed. *Refreshing only after this page acts* was rejected because it leaves #15 genuinely unfixed: changes from the API or the reaper still go unseen, which is exactly the reported bug |

### What D1 costs, stated plainly

The per-page token is the only genuinely new stateful thing this milestone introduces. It has to be
minted, bound to an identity, expired with that identity's session, and kept out of every log — and
it creates a failure mode that does not exist today, where a page outlives its token and an action
fails for a reason the operator did not cause. FR-031 exists partly for that case: such a failure
must say so rather than silently reverting.

That cost was accepted rather than overlooked.
