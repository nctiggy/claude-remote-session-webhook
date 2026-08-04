# Feature Specification: Access Validation & Read-Only Dashboard (Milestone 2)

**Feature Branch**: `002-access-dashboard`

**Created**: 2026-08-04

**Status**: Draft — clarifications resolved, ready for `/speckit-plan`

**Input**: User description: "Milestone 2: the read-only dashboard and Cloudflare Access. Daemon-side validation of the Cloudflare Access JWT (layer 1), plus a read-only web dashboard showing every session in flight with live pane output. No create, destroy, rename, or compact from the UI. Binding: constitution (Principle VII now applies), docs/security.md, docs/auth-and-sessions.md, docs/design-system.md, docs/components.md. Milestone 1's shipped contract must not be broken."

## Context

Milestone 1 shipped and is **deployed**: the daemon runs as a service on the operator's
host, bound to loopback, reachable only through an outbound tunnel. Its public hostname
has deliberately been left without a DNS record, because the daemon does not yet check
who the browser is — layer 1 of the three in
[`docs/auth-and-sessions.md`](../../docs/auth-and-sessions.md) is the one thing missing
before that hostname can exist.

This milestone delivers exactly that check, and the first thing worth looking at through
it. Two halves that must ship together: without the dashboard, validating the edge
identity has nothing to protect; without the validation, the dashboard is an
unauthenticated window onto a host running unsandboxed shells.

**Principle VII of the constitution applies for the first time.** Milestone 1 had no
user interface, so the design system was inert. Every pixel in this milestone is bound
by [`docs/design-system.md`](../../docs/design-system.md) and
[`docs/components.md`](../../docs/components.md), and the rule that pane output renders
as text and never as markup is a security rule wearing a design rule's clothes.

Everything milestone 1 built stays working. Its request signing, its audit record shape,
and its six operations are a shipped contract, and a caller written against them must
not need changing.

## User Scenarios & Testing *(mandatory)*

### User Story 1 - See every session on my host at a glance (Priority: P1)

The operator opens the dashboard in a browser. Cloudflare Access has already made them
sign in with Google, and the daemon independently confirms that assertion before
rendering anything. They see a summary of how many sessions are in each state, then a
card per session: its name, its state, its working directory, and how old it is. Their
own Google identity is visible in the top-right, so it is never ambiguous whose
credentials are driving sessions on this host.

**Why this priority**: This is the milestone's product. It is also what makes the public
hostname safe to create, which is what unblocks everything after it.

**Independent Test**: With a valid Access assertion, load the dashboard and see every
session the daemon knows about, each with a state label. With no assertion, with a
forged one, and with one minted for a different Access application, get nothing.

**Acceptance Scenarios**:

1. **Given** a browser carrying a valid Access assertion for an allowed identity,
   **When** the dashboard is loaded, **Then** it renders a state summary followed by one
   card per session, and the operator's own identity appears in the header.
2. **Given** sessions in different states, **When** the dashboard renders, **Then** each
   card carries a **text** state label, not colour alone.
3. **Given** no sessions at all, **When** the dashboard is loaded, **Then** an empty
   state explains that nothing is executing on this host, rather than an empty page.
4. **Given** a request with no Access assertion, **When** it reaches the daemon,
   **Then** it is refused and no session data is disclosed.
5. **Given** an assertion whose signature does not verify, **When** it is presented,
   **Then** it is refused identically to one that is absent.
6. **Given** a genuine assertion minted for a *different* Access application in the same
   account, **When** it is presented, **Then** it is refused — the audience is checked,
   not merely the signature.
7. **Given** a valid assertion for an identity outside the daemon's own allowlist,
   **When** it is presented, **Then** it is refused, and the refusal is recorded.
8. **Given** the dashboard rendered, **When** its markup is inspected, **Then** it
   references no external origin — no CDN, no webfont URL, no third-party script.

---

### User Story 2 - Watch a session's output as it happens (Priority: P2)

The operator opens one session and watches its terminal output arrive live, the way it
would if they were attached to it. The text is readable, monospaced, and scrolls in its
own container. If they have scrolled up to read something, new output does not yank the
viewport away from them.

**Why this priority**: Live output is what makes the dashboard worth opening rather than
running a command. It ranks below US1 because a static view of the fleet is already
useful, and it carries the milestone's whole XSS surface, so it should land on top of a
working, already-authenticated page rather than alongside one.

**Independent Test**: Produce distinctive output in a session, open it in the dashboard,
and see that text appear without reloading. Then produce output containing HTML tags,
terminal escape sequences, and a script element, and confirm every byte renders as
visible text and nothing executes.

**Acceptance Scenarios**:

1. **Given** an open session view, **When** the session produces new output, **Then** it
   appears without the operator reloading the page.
2. **Given** a session whose output contains `<script>`, `<img onerror=...>`, or any
   other markup, **When** it renders, **Then** the characters are displayed literally
   and nothing is interpreted as markup.
3. **Given** output containing terminal escape sequences, **When** it renders, **Then**
   the escapes are gone and the readable text remains.
4. **Given** the operator has scrolled up within the output, **When** new output
   arrives, **Then** the viewport stays where they put it.
5. **Given** the operator is at the bottom of the output, **When** new output arrives,
   **Then** the view follows it.
6. **Given** a live view of a session, **When** that session is destroyed or expires,
   **Then** the view says so plainly rather than silently freezing.
7. **Given** a live view left open, **When** it is open for longer than a session's idle
   timeout, **Then** the session is still reaped on schedule and the view reports that it
   ended. Watching is not driving: a stream that postponed the idle deadline would let an
   abandoned browser tab hold an unsandboxed shell open indefinitely, which is the bound
   Principle VI calls non-negotiable.

---

### User Story 3 - A browser that should not be here gets nothing (Priority: P3)

Someone who is not the operator points a browser at the hostname — a scanner, a
colleague, an attacker who learned the name. Cloudflare Access stops them at the edge.
If it does not, because it is misconfigured or failing open, the daemon stops them
itself.

**Why this priority**: This is the assertion that the layering is real. It ranks below
the stories it protects only because it cannot be demonstrated until they exist.

**Independent Test**: Present, directly to the daemon's listener, every shape of bad
assertion — absent, malformed, expired, unsigned, signed with the wrong key, `alg: none`,
right signature but wrong audience, right audience but a disallowed identity — and get
the same refusal for each, with the reason recorded server-side only.

**Acceptance Scenarios**:

1. **Given** an assertion using an algorithm the daemon does not accept — including
   `none` — **When** it is presented, **Then** it is refused.
2. **Given** an expired assertion, **When** it is presented, **Then** it is refused.
3. **Given** any of the invalid assertions above, **When** each is refused, **Then** the
   response is the same for all of them and reveals nothing about which check failed.
4. **Given** a refusal, **When** the audit trail is read, **Then** it records that a
   browser request was denied and why, and contains no assertion, no token, and no
   session content.
5. **Given** the daemon cannot reach the identity provider's signing keys at all,
   **When** a browser request arrives, **Then** it is refused rather than admitted.

---

### User Story 4 - My existing API client keeps working (Priority: P4)

The operator's script — and later the companion skill — continues to drive sessions over
the signed API exactly as it did before, against the same hostname, with no change to
how it signs a request.

**Why this priority**: Milestone 1 is deployed and in use. Breaking it to add a UI would
be a regression dressed as a feature. It sits at P4 because it is preservation rather
than new capability, but a failure here is worse than any missing dashboard.

**Independent Test**: Take the exact request-signing procedure milestone 1 documents,
run it against the deployed hostname, and complete a create → list → destroy cycle
without modification.

**Acceptance Scenarios**:

1. **Given** a correctly signed API request, **When** it is sent to the same hostname the
   dashboard uses, **Then** it is served exactly as milestone 1 served it.
2. **Given** the signing procedure from milestone 1's contract, **When** it is used
   unchanged, **Then** it still produces an accepted signature.
3. **Given** an API request carrying no browser identity at all, **When** it is sent,
   **Then** it is not refused for lacking one.
4. **Given** a browser request carrying no request signature, **When** it is sent to a
   dashboard route, **Then** it is not refused for lacking one.
5. **Given** any request, **When** it is served, **Then** it produced exactly one audit
   record, as milestone 1 requires.

---

### User Story 5 - Develop against it locally without leaving a back door (Priority: P5)

The operator works on the dashboard on their own machine, where no Cloudflare Access
exists to sign anything. They can bypass layer 1 locally, and that bypass cannot exist
in anything they ship.

**Why this priority**: Without it the dashboard cannot be developed at all. It is last
because it delivers nothing to a running system, and it is genuinely dangerous — a
production binary that can disable authentication by flag is a backdoor, which
`docs/auth-and-sessions.md` says in those words.

**Independent Test**: Build the shipping artifact and confirm the bypass is not merely
disabled but absent. Then build the development artifact, confirm the bypass works, that
it refuses to operate on a non-loopback listener, and that it announces itself on every
request.

**Acceptance Scenarios**:

1. **Given** the shipping artifact, **When** the bypass is requested by any means,
   **Then** it does not exist and the artifact refuses to start with it.
2. **Given** the development artifact with the bypass active, **When** the listener is
   anything other than loopback, **Then** it refuses to start.
3. **Given** the bypass active, **When** any request is served, **Then** a prominent
   warning is emitted — every request, not only at startup.
4. **Given** the bypass active, **When** a request is served, **Then** layers 2 and 3 are
   still enforced. The bypass skips layer 1 only.

---

### Edge Cases

- **Assertion shape**: absent, empty, malformed, not three segments, valid JSON but no
  signature, signature over a modified payload, `alg` switched to `none` or to a
  symmetric algorithm, `kid` naming a key that does not exist, correct signature but
  expired, correct signature but issued in the future, correct signature but wrong
  audience, correct audience but an identity outside the allowlist.
- **Signing keys**: the provider is unreachable on first request; the key set is fetched
  but empty; a `kid` arrives that is not in the cached set; the cache is stale and the
  key has rotated; two requests race the first fetch.
- **Rendering**: output containing `<script>`, an unclosed tag, a null byte, invalid
  UTF-8, an extremely long single line, thousands of lines at once, and terminal escapes
  that move the cursor rather than colour text.
- **Streaming**: the browser disappears without closing the connection; the session is
  destroyed while being watched; the session expires while being watched; the same
  session is watched from two tabs; many sessions are watched at once; the daemon is
  asked to shut down while streams are open.
- **Fleet**: zero sessions; one session; more sessions than fit on a screen; a session
  whose name or working directory is long enough to break a layout; a session in every
  state at once.
- **Coexistence**: an API request that also happens to carry a browser assertion; a
  browser request to an API path; a dashboard request to a path that does not exist.
- **Motion and accessibility**: reduced-motion is requested; the page is driven entirely
  by keyboard; a screen reader is in use while output streams.

## Requirements *(mandatory)*

### Functional Requirements

#### Layer 1 — edge identity, verified by the daemon

- **FR-001**: The daemon MUST validate the identity assertion Cloudflare Access forwards,
  on every browser request. A header is trivially forgeable by anything that can reach
  the listener; the signature is what makes it evidence.
- **FR-002**: Validation MUST verify the assertion's signature against the identity
  provider's published signing keys for this specific application.
- **FR-003**: The daemon MUST pin the expected audience. Without it, an assertion minted
  for any other application in the same account would validate here.
- **FR-004**: The daemon MUST pin the accepted signing algorithm and MUST reject any
  assertion naming a different one, `none` included. The algorithm is never taken from
  the assertion.
- **FR-005**: The daemon MUST verify the expected issuer.
- **FR-006**: The daemon MUST reject an expired assertion, and one whose validity begins
  in the future.
- **FR-007**: The daemon MUST re-check the identity against its own allowlist, held in
  its own configuration. The edge is the gate; this is the daemon's assertion that the
  gate is configured the way the operator believes.
- **FR-008**: Signing keys MUST be cached and refetched on encountering an unknown key
  identifier — never fetched per request.
- **FR-009**: If the signing keys cannot be obtained, browser requests MUST be refused.
  An identity that cannot be verified is not an identity.
- **FR-010**: Every layer-1 failure MUST produce one uniform response revealing nothing
  about which check failed, with the specific reason recorded server-side only.
- **FR-011**: A missing or unusable layer-1 configuration MUST be a startup failure, not
  a warning — the same rule milestone 1 applies to its shared secret.

#### Two front doors on one hostname

- **FR-012**: The signed API and the browser dashboard MUST both work, and each MUST be
  refused only by the check that applies to it: an API request is not refused for
  carrying no browser identity, and a browser request is not refused for carrying no
  request signature.
- **FR-013**: Every route MUST be covered by exactly one of the two doors. No route may
  be reachable through neither.
- **FR-013a**: Both doors MUST live on **one hostname**, with the edge admitting each by
  its own policy: the browser by the identity provider, and the API client by an Access
  **service token** it presents as headers. Neither door may be given an edge bypass —
  an unprotected path on this hostname would restore exactly the posture that kept the
  DNS record from being created.
- **FR-013b**: The daemon MUST still apply its own check behind whichever edge policy
  admitted a request: a browser request is validated at layer 1, an API request at layers
  2 and 3. The edge deciding who may knock never substitutes for the daemon deciding who
  is allowed in.
- **FR-014**: Milestone 1's request-signing procedure MUST remain unchanged. A client
  written against the shipped contract MUST work without modification.
- **FR-015**: Milestone 1's six operations MUST keep their existing behaviour, status
  codes, and response bodies.
- **FR-016**: The audit trail MUST keep emitting exactly one record per request, in the
  existing shape, for browser requests as well as API ones.

#### The dashboard

- **FR-017**: The dashboard MUST show every session the daemon knows about, with a state
  summary before any detail.
- **FR-018**: Each session MUST show its name, state, working directory, and age.
- **FR-019**: Every state MUST carry a **text label**. Colour is reinforcement and MUST
  NOT be the only signal.
- **FR-020**: The header MUST show the operator's verified identity, taken from the
  validated assertion and never from anything the request supplies.
- **FR-021**: With no sessions, the dashboard MUST render an explanatory empty state
  rather than a blank region.
- **FR-022**: The dashboard MUST be **read-only**. It MUST NOT offer create, destroy,
  rename, or compact, and MUST NOT reach any route that performs them.
- **FR-023**: All styling MUST come from the tokens in the design system. No hard-coded
  colour, size, or font may appear in a template.
- **FR-024**: The dashboard MUST reuse the canonical components already defined. A second
  card, pill, or button is a defect.
- **FR-025**: Every asset MUST be served by the daemon itself. No external origin may be
  referenced — no CDN, no remote font, no third-party script.
- **FR-026**: The daemon MUST send the response headers named in the security document,
  including a content policy that forbids inline script and any external origin.
- **FR-027**: The interface MUST be keyboard operable with a visible focus indicator, and
  MUST honour a reduced-motion preference by removing the animated background.

#### Session output — the one XSS surface

- **FR-028**: Session output MUST reach the browser as text and MUST NOT be interpreted
  as markup under any circumstance. This is closed by construction, not by sanitising.
- **FR-029**: Terminal escape sequences MUST be removed before output leaves the daemon.
- **FR-030**: Session names and working directories MUST be rendered as text on the same
  terms as output — a caller chose them.
- **FR-031**: Output MUST update live, without the operator reloading.
- **FR-032**: The view MUST follow new output only when the operator is already at the
  bottom of it.
- **FR-033**: When a watched session ends, the view MUST say so rather than silently
  stopping.
- **FR-034**: A live view MUST be authorised by the **validated layer-1 identity plus an
  ownership check on the session**, evaluated when the stream opens. It MUST NOT be
  assumed from the fact that a page is open, and no credential may appear in the stream's
  URL — URLs are logged by every intermediary.
- **FR-034a**: The per-session bearer token MUST NOT be required for a browser stream.
  That token exists because the API's identity is a single shared secret, so every API
  caller looks alike and needs a second factor naming the session. A browser identity is
  verified per person, and the ownership check is what distinguishes sessions for it —
  so requiring the token would not add a check, it would only make the stream
  unimplementable without putting a credential somewhere it must not go.
- **FR-034b**: The stream's authorisation MUST be re-evaluated, not merely established:
  if the session ends, expires, or ceases to be the viewer's, the stream MUST stop
  delivering output.
- **FR-035**: Session output MUST NOT appear in any audit record or log line, exactly as
  in milestone 1.

#### Identity and ownership

- **FR-036**: The dashboard's caller identity MUST be derived from the validated
  assertion, server-side, and never from a request field.
- **FR-037**: Every session-scoped read the dashboard performs MUST be authorised against
  the session's recorded owner.
- **FR-037a**: The allowlisted browser identity and the API's shared-secret identity MUST
  resolve to the **same owner**, so a session created through the API is one the
  dashboard owns and can read. The mapping MUST be configuration, not a value either
  request supplies.
- **FR-037b**: The ownership comparison MUST still be performed on every session-scoped
  request. It is not skipped on the grounds that there is currently one subject — a check
  that is removed because it always passes is a check that will not be there when a
  second identity arrives. Tests MUST continue to exercise a cross-owner refusal with a
  synthetic second owner, as milestone 1 does.

#### Local development

- **FR-038**: A layer-1 bypass MUST exist for local development, MUST skip layer 1 only,
  and MUST leave layers 2 and 3 enforced.
- **FR-039**: The bypass MUST refuse to operate unless the listener is loopback.
- **FR-040**: The bypass MUST announce itself on every request, not only at startup.
- **FR-041**: The bypass MUST be **absent** from the shipping artifact — excluded at
  build time, not merely defaulted off. A production artifact that can disable
  authentication by flag is a backdoor.

### Key Entities

- **Verified operator**: A browser identity the daemon has itself confirmed — signature,
  audience, issuer, expiry, and allowlist all checked. Distinct from "someone Access let
  through", which is a claim rather than a conclusion.
- **Signing key set**: The identity provider's public keys, cached, refetched when an
  unknown key identifier appears, and never fetched per request.
- **Identity allowlist**: The addresses the daemon itself will accept, held in its own
  configuration so that an edge misconfiguration cannot silently widen access.
- **Session view**: One session as the dashboard presents it — the fields in FR-018 plus
  its live output. Built from the same record the API serves, never a second copy that
  could disagree.
- **Live output stream**: A long-lived delivery of one session's output to one browser,
  bound to a session the viewer is authorised to read, ending when the session ends.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: 100% of invalid assertions are refused — absent, malformed, expired,
  future-dated, wrong signature, wrong algorithm including `none`, unknown key, wrong
  audience, disallowed identity — with an identical response for every case.
- **SC-002**: An assertion minted for a different application in the same account is
  refused 100% of the time.
- **SC-003**: The operator goes from opening the hostname to seeing the state of every
  session on the host in one page load, with no further interaction.
- **SC-004**: Session output containing markup, script elements, and terminal escapes
  renders as visible text in 100% of cases, with zero script execution — verified by a
  test that fails if the output is ever treated as markup.
- **SC-005**: The rendered dashboard references zero external origins.
- **SC-006**: New session output becomes visible without a page reload.
- **SC-007**: 100% of milestone 1's API operations behave identically before and after
  this milestone, verified by running milestone 1's own acceptance suite unchanged.
- **SC-008**: Every request produces exactly one audit record, browser and API alike, and
  a full capture contains zero occurrences of an identity assertion, a session token, or
  session output.
- **SC-009**: Every session state is distinguishable without colour — verified by reading
  the interface in greyscale.
- **SC-010**: The interface is fully operable by keyboard, with a visible focus indicator
  at every step.
- **SC-011**: With a reduced-motion preference set, zero animated background elements
  render.
- **SC-012**: The shipping artifact contains no authentication bypass — verified by a
  check that fails the build if one is present.
- **SC-013**: Startup fails, with no listener bound, for every missing or invalid
  layer-1 configuration value.
- **SC-014**: A request carrying neither an accepted browser identity nor a valid
  service-token admission is refused at the edge before reaching the daemon, for every
  path on the hostname — zero paths are edge-exempt.
- **SC-015**: A live stream stops delivering within one polling interval of the session
  ending, expiring, or ceasing to be the viewer's, in 100% of cases.
- **SC-016**: A session created through the API is visible and readable in the dashboard
  without any change to how it was created, and a session belonging to a synthetic second
  owner is refused to the dashboard identically to a session that does not exist.
- **SC-017**: Build, test, and lint all pass, and every behaviour above is covered by a
  test that fails when the behaviour is removed — including the negative cases.

## Assumptions

### Resolved during specification

Three ambiguities were surfaced rather than guessed at, and answered by the operator on
2026-08-04:

- **One hostname, two edge policies.** Browsers are admitted by the identity provider;
  the API client is admitted by an Access **service token** it sends as headers. Neither
  gets an edge bypass, so no path on this hostname is reachable without passing the edge
  first, and each still faces its own daemon-side check behind it. The cost is two extra
  headers on the API client and a service token to manage. (FR-013a, FR-013b.)
- **A live stream is authorised by the validated browser identity plus the ownership
  check**, and deliberately not by the per-session bearer token. That token exists to
  tell apart callers who all authenticate as the same shared secret; a verified per-person
  identity plus ownership already makes that distinction, so requiring the token would add
  no check while forcing a credential into a URL. Authorisation is re-evaluated rather
  than established once. (FR-034, FR-034a, FR-034b.)
- **The browser identity and the API identity resolve to one owner**, by configuration.
  Without this the dashboard would own nothing and render an empty fleet while every
  individual requirement passed its test. The ownership comparison is still performed on
  every request, and the cross-owner refusal is still tested with a synthetic second
  owner — a check removed because it always passes is one that will not be there when a
  second identity arrives. (FR-037a, FR-037b.)

### Assumed defaults

- **The dashboard is read-only in the strict sense**: it performs no state-changing
  operation at all, so it cannot start, stop, rename, or compact a session. Those arrive
  in milestone 3.
- **One operator, one allowed identity.** The allowlist exists and is enforced, but is
  expected to hold a single address, matching the single-operator decision milestone 1
  recorded.
- **The `needs-auth` state defined in the design system will not occur in this
  milestone.** It is produced by the device-code relay in milestone 4. The status
  component still handles it, because the design system defines it and a state that
  renders wrongly the first time it occurs is a defect that ships silently.
- **Live output is delivered by a streaming connection from daemon to browser**, as the
  project README anticipates. The mechanism is a planning decision; what this spec
  requires is that output appears without a reload and is authorised per FR-034.
- **Output is delivered as whole lines.** Partial-line updates are not required.
- **No persistence is added.** The dashboard reads the same in-memory records the API
  serves; nothing is written to disk.
- **The tunnel and the Access application are configured outside this repository**, by
  the operator. This milestone delivers the daemon-side validation that makes them
  meaningful, not the configuration itself.
- **The public DNS record is created only once this milestone is deployed**, since its
  absence is currently the only thing keeping an unvalidated hostname off the internet.

### Dependencies

- Milestone 1, deployed and running. This milestone extends it and does not replace any
  part of it.
- A Cloudflare Access application in front of the hostname, with Google as the identity
  provider and the operator's address allowed.
- Reachability from the daemon to the identity provider's published signing keys.

## Out of Scope

- Create, destroy, rename, and compact from the dashboard — milestone 3.
- Relaying Claude Code's own device-code login — milestone 4.
- The companion Claude skill.
- Multi-user support beyond one allowlisted identity and the ownership check that already
  exists.
- Persisting session records, dashboard state, or output history to disk.
- Any change to milestone 1's signing procedure, operations, or audit record shape.
- Mobile-specific layouts beyond the single responsive breakpoint the design system
  already defines.
