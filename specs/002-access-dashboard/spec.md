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
session the viewer owns, each with a state label. With no assertion, with a
forged one, and with one minted for a different Access application, get nothing.

**Acceptance Scenarios**:

1. **Given** a browser carrying a valid Access assertion for an allowed identity,
   **When** the dashboard is loaded, **Then** it renders a state summary followed by one
   card per session, and the operator's own identity appears in the header.
2. **Given** one session used a moment ago and another untouched for longer than the idle
   threshold, **When** the dashboard renders, **Then** the first reads *running* and the
   second reads *idle*, each as a **text** label rather than colour alone.
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
4. **Given** the operator has scrolled within the output, **When** the screen updates,
   **Then** the viewport is not moved for them.
5. **Given** a session whose program repaints its whole screen — a progress indicator, a
   redraw, a cleared display — **When** the update arrives, **Then** the view shows the
   new screen without accumulating duplicated or torn-apart lines from the redraw.
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

**Independent Test**: Take the exact request-signing procedure milestone 1 documents and
complete a create → list → destroy cycle against the daemon's own listener with no
modification whatsoever. Then repeat it through the public hostname with only the edge
service-token headers added, and get the same results.

**Acceptance Scenarios**:

1. **Given** a correctly signed API request carrying the edge service token, **When** it
   is sent to the same hostname the dashboard uses, **Then** it is served exactly as
   milestone 1 served it.
1a. **Given** the same request without the service token, **When** it is sent to that
   hostname, **Then** it is refused by the edge and never reaches the daemon — no path on
   the hostname is edge-exempt.
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
  whose name or working directory is long enough to break a layout; a session one second
  either side of the idle threshold; a session adopted after a restart, carrying no name
  and no working directory.
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
- **FR-002**: Validation MUST verify the assertion's signature against **the edge's own
  published signing keys for this account**. These are the edge's keys, not the identity
  provider's — the identity provider signs nothing the daemon ever sees, and the keys are
  per-account rather than per-application. What pins the assertion to *this* application
  is the audience in FR-003.
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
- **FR-013c**: A request admitted by the service token carries an assertion with **no
  identity claim** — it names a credential, not a person. The daemon MUST refuse such an
  assertion for the dashboard, and MUST NOT treat the absence of an identity as an
  allowlist match. This is the one malformed-looking assertion that will arrive in normal
  operation, every time the API client calls.
- **FR-013d**: A request to a path the daemon serves no route for MUST be answered by the
  door that owns the dashboard, not by the API's refusal. A signed-in browser that
  mistypes a URL currently receives the API's raw refusal body, which is neither useful
  nor consistent with the interface it came from.
- **FR-014**: Milestone 1's request-signing procedure and its daemon-side contract MUST
  remain unchanged: the signed payload, the headers the daemon reads, and every response
  it returns are fixed. A client written against the shipped contract MUST work without
  modification **against the daemon's own listener**.
- **FR-014a**: Reaching the daemon through the public hostname additionally requires the
  edge admission of FR-013a, so the deployed client gains the service-token headers. This
  is a change to what the client sends *the edge*, not to what it sends the daemon, and
  the distinction is the whole reason FR-014 is scoped to the listener. An implementation
  MUST NOT resolve the difference by weakening either side — no edge bypass, no change to
  the signing payload.
- **FR-015**: Milestone 1's six operations MUST keep their existing behaviour, status
  codes, and response bodies.
- **FR-016**: The audit trail MUST keep emitting exactly one record per request, in the
  existing shape, for browser requests as well as API ones.
- **FR-016a**: A long-lived stream MUST be recorded when it **opens**, carrying the
  authorisation decision, rather than only when it closes. Milestone 1 emits its record
  after the handler returns, which for a stream lasting hours means a daemon that dies
  mid-stream leaves no trace that session output was being read. Whether a second record
  marks the close is this milestone's choice, but the open MUST be recorded and the total
  MUST be stated rather than left to whatever the existing mechanism happens to do.

#### The dashboard

- **FR-017**: The dashboard MUST show every session **the viewer owns**, with a state
  summary before any detail. By FR-037a that is every session in practice, but the
  dashboard MUST reach them through the same owner-scoped read the API uses — never an
  owner-blind one. Milestone 1 keeps its only owner-blind lookup unexported on purpose;
  exporting it to satisfy this requirement would break the isolation rule
  `docs/auth-and-sessions.md` calls non-negotiable.
- **FR-017a**: The fleet MUST keep describing the daemon's own records **after** the page
  has been rendered. A session created, destroyed, or reaped since the load MUST appear or
  disappear without the operator acting, within a bounded interval this milestone states.
  FR-031 requires exactly this of pane output and nothing required it of the list itself,
  which is why the first build of this milestone rendered the fleet once and never again
  (#15) — and why every test passed while it did, each one rendering a fresh page. It is
  the dashboard's own guarantee rather than a convenience: this page exists to say what is
  executing on this host, and a fleet still showing a session the reaper destroyed twenty
  minutes ago states the opposite of that, with the same authority as a card whose label
  FR-019c makes agree with the reaper's own deadline.
- **FR-017b**: A refresh MUST be one more of the reads FR-017 already describes — the
  owner-scoped list, projected through the canonical components — and MUST NOT introduce a
  second description of a session. A fleet reassembled in the browser out of data would be
  the second card FR-024 calls a defect, written in the one language the design system
  cannot reach.
- **FR-017c**: A refresh MUST NOT advance any session's idle clock and MUST NOT reach a
  mutating path. Watching is not driving (FR-034f, FR-022): a dashboard nobody is driving
  must not postpone an idle deadline, and one that refreshes on a timer would otherwise
  postpone it for as long as the tab lives.
- **FR-017d**: A refresh MUST NOT be performed while the page is not being looked at, and
  MUST happen when it is looked at again. An interval that runs in a hidden tab is one
  request per interval per forgotten tab, each costing the daemon a render and the trail a
  record (FR-016) for a page nobody is reading; refreshing on becoming visible instead
  means the fleet is current at the only moment the guarantee is about — the moment
  somebody reads it.
- **FR-018**: Each session MUST show its name, state, working directory, and age.
- **FR-018a**: A session adopted after a daemon restart has **no name and no working
  directory** — milestone 1 records neither, on purpose, because nothing on the host
  carries them. The dashboard MUST render that absence as an explicit, readable statement
  that the value is unknown, and MUST NOT invent a placeholder that reads like a real
  value. This is a routine state after any restart, not an edge case.
- **FR-019**: Every state MUST carry a **text label**. Colour is reinforcement and MUST
  NOT be the only signal.
- **FR-019a**: Display state MUST be **derived at render time** from the session record,
  not read from a stored lifecycle field. The daemon writes only `starting` and
  `running`, has no production path that writes any other value, and deletes records
  rather than marking them dead — so a dashboard that rendered the stored field directly
  would show one label forever.
- **FR-019b**: The derivation is: **idle** when the session has had no activity for
  longer than the idle threshold the reaper enforces, and **running** otherwise.
  `starting` is displayed as running — the distinction is momentary and invisible to an
  operator. `dead` is never displayed, because a dead session has no record to render.
- **FR-019c**: The idle threshold used for display MUST be the same value the reaper
  enforces, taken from one place. A dashboard saying "running" about a session the reaper
  is about to destroy is worse than no label at all.
- **FR-020**: The header MUST show the operator's verified identity, taken from the
  validated assertion and never from anything the request supplies.
- **FR-021**: With no sessions, the dashboard MUST render an explanatory empty state
  rather than a blank region.
- **FR-022**: The dashboard MUST be **read-only**. It MUST NOT offer create, destroy,
  rename, or compact, and MUST NOT reach any route that performs them.
- **FR-023**: All styling MUST come from the tokens in the design system. No hard-coded
  colour, size, or font may appear in a template.
- **FR-024**: The dashboard MUST reuse the canonical components defined in
  `docs/components.md`, which this milestone creates for the first time — they exist as
  prose, not yet as code. A second card, pill, or button is a defect.
- **FR-024a**: The canonical session card and empty state are documented **with action
  affordances** — destroy, compact, rename, "start a session". Those MUST NOT be built in
  this milestone (FR-022). A browser cannot sign an API request, so they would be
  non-functional as well as out of scope. The components MUST be created such that the
  action row is a parameter that is simply absent here, not deleted code milestone 3 has
  to restore.
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
- **FR-031a**: The view MUST present the session's **current screen**, replaced on each
  update — not a growing transcript. What is being watched is a full-screen terminal
  program that repaints in place; successive captures are redraws, not new lines.
  Reconstructing an append-only transcript by diffing redraws would produce spurious
  lines from every cursor move, progress spinner and repaint, and is the single most
  likely place for this milestone to lose days.
- **FR-032**: The view MUST NOT move the operator's scroll position when the screen
  updates. A screen that repaints has no "bottom" to follow, so the requirement is that
  updating never yanks the viewport — not that it tracks new output.
- **FR-032a**: The dashboard MUST make plain that it shows the live screen and not
  scrollback. Attaching to the session on the host is what shows history, and an
  interface that silently implies otherwise is one an operator will trust wrongly.
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
- **FR-034c**: The daemon MUST NOT emit any cross-origin resource-sharing response
  header on any route. The browser's layer-1 credential is a cookie and therefore
  ambient: it rides on requests a hostile third-party page triggers, and the edge will
  convert those into a valid assertion. Same-origin policy is what stops that page
  *reading* the result, and it holds only while the daemon never opts out of it. This is
  the one protection the per-session token would also have provided — a header credential
  forces a preflight, a cookie does not — so declining the token (FR-034a) makes this
  requirement load-bearing rather than tidy.
- **FR-034d**: A stream MUST be refused when the request indicates a cross-site
  initiator, where the browser supplies that signal.
- **FR-034e**: The daemon MUST cap concurrent output streams and refuse past the cap
  rather than degrading the host. Each stream is a long-lived connection doing periodic
  work against the host; unbounded streams are the same local denial of service the
  session cap exists to prevent (Principle VI).
- **FR-034f**: An open stream MUST NOT delay session teardown or daemon shutdown, and
  MUST NOT advance a session's idle clock. Watching is not driving: a stream that
  postponed the idle deadline would let a forgotten browser tab hold an unsandboxed shell
  open indefinitely. Milestone 1 advances that clock in the single place a request
  resolves to a session, and this milestone adds a second path that must not.
- **FR-035**: Session output MUST NOT appear in any audit record or log line, exactly as
  in milestone 1.

#### Identity and ownership

- **FR-036**: The dashboard's caller identity MUST be derived from the validated
  assertion, server-side, and never from a request field.
- **FR-037**: Every session-scoped read the dashboard performs MUST be authorised against
  the session's recorded owner.
- **FR-037a**: The allowlisted browser identity and the API's shared-secret identity MUST
  resolve to the **same owner**, so a session created through the API is one the
  dashboard owns and can read. The owner MUST be derived server-side by construction and
  MUST NOT be a value either request supplies. It MUST NOT be an operator-settable knob
  either: milestone 1 makes the API's owner a constant precisely so there is no second
  place for identity to disagree, and a setting whose only correct value is that constant
  is a way to produce an empty dashboard with every test passing.
- **FR-037b**: The ownership comparison MUST still be performed on every session-scoped
  request. It is not skipped on the grounds that there is currently one subject — a check
  that is removed because it always passes is a check that will not be there when a
  second identity arrives. A cross-owner refusal MUST be exercised with a synthetic second
  owner **through the dashboard's own path**; pointing at milestone 1's existing API test
  does not satisfy this, because it is the dashboard's route that is new.

#### Local development

- **FR-038**: A layer-1 bypass MUST exist for local development, MUST skip layer 1 only,
  and MUST leave layers 2 and 3 enforced.
- **FR-039**: The bypass MUST refuse to operate unless the listener is loopback.
- **FR-040**: The bypass MUST announce itself on every request, not only at startup.
- **FR-041**: The bypass MUST be **absent** from the shipping artifact — excluded at
  build time, not merely defaulted off. A production artifact that can disable
  authentication by flag is a backdoor.
- **FR-042**: With the bypass active, the daemon MUST NOT require the layer-1
  configuration FR-011 makes fatal when absent. Demanding an audience and an issuer that
  the bypass then ignores would make local development need a Cloudflare account.

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
- **SC-006a**: A session created, destroyed, or reaped after the fleet was rendered appears
  on it or disappears from it with **no operator action**, within the refresh interval
  FR-017a requires the milestone to state.
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
  path on the hostname — zero paths are edge-exempt. **This is deployment behaviour and
  is verified against the running hostname, not by a test in this repository**, since the
  edge is configured outside it. It is therefore explicitly exempt from SC-017 and
  belongs on the deployment checklist.
- **SC-015**: A live stream stops delivering within one polling interval of the session
  ending, expiring, or ceasing to be the viewer's, in 100% of cases.
- **SC-016**: A session created through the API is visible and readable in the dashboard
  without any change to how it was created, and a session belonging to a synthetic second
  owner is refused to the dashboard identically to a session that does not exist.
- **SC-017**: Build, test, and lint all pass, and every behaviour above **except SC-014**
  is covered by a test that fails when the behaviour is removed — including the negative
  cases. SC-014 is edge configuration and has no in-repository test that could fail.

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

Two further decisions were taken on the same day, after a cross-model review of this
specification found that both were unimplementable as originally written:

- **Display state is derived, not stored.** The daemon writes only `starting` and
  `running` — `SetState` has no production caller and dead records are deleted rather
  than marked — so the four display states the design system defines cannot all occur.
  The dashboard derives **idle** from last activity against the reaper's own threshold and
  shows everything else as **running**; `dead` is never rendered because such a session
  has no record. `docs/design-system.md` is amended to describe those states rather than
  four that cannot all happen. (FR-019a–c.)
- **The pane shows the current screen, replaced on each update.** The original spec
  modelled output as an append-only line stream, which a full-screen terminal program is
  not. Diffing repaints into appended lines is fragile in a way that would have surfaced
  late and expensively. (FR-031a, FR-032, FR-032a.)

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
- **Output is delivered as whole screens**, replacing what was shown. This follows from
  the decision recorded below and from what a Claude Code session actually is: a
  full-screen program, not a log.
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

## Documents this milestone must amend

These are binding, and this milestone makes each of them wrong. Amending them is in
scope, not follow-up work — a binding document that contradicts the running system is
worse than none, because it is followed.

- **`docs/components.md`** — its canonical pane viewer used htmx's `sse-swap` with
  `hx-swap="beforeend"`, which inserts the payload as markup and is exactly what
  `docs/security.md` forbids. **Already corrected** as part of this specification, because
  leaving it would have had FR-024 instructing an implementer to open the project's only
  XSS surface. The correction is the reason FR-024 and FR-028 are now jointly satisfiable.
- **`docs/auth-and-sessions.md`** — opens "There are no browser sessions and no human
  login form." This milestone creates both. Its two-door table also has no service token,
  and the stream-authorisation rule (FR-034 and its parts) exists only in this spec until
  that document carries it.
- **`docs/security.md`** — its two-door table predates the service token, and its header
  table says nothing about cross-origin headers, which FR-034c now makes load-bearing.

## Dependencies added

Verifying a signed assertion and fetching a rotating key set is not something the standard
library does. Milestone 1 shipped with **zero** third-party dependencies and
`docs/security.md` §5 says to keep it that way, requiring justification for each addition:
what does it do that the standard library cannot?

This milestone must answer that in its plan rather than have an implementation quietly
vendor a library. Whichever way it goes — a dependency with a written justification, or a
narrow hand-rolled verification of one algorithm against a cached key set — it is a
deliberate decision recorded in the plan, and `go.sum` appearing without one is a defect.

## Out of Scope

- Create, destroy, rename, and compact from the dashboard — milestone 3.
- Relaying Claude Code's own device-code login — milestone 4.
- The companion Claude skill.
- Multi-user support beyond one allowlisted identity and the ownership check that already
  exists.
- Persisting session records, dashboard state, or output history to disk.
- Any change to milestone 1's signing procedure, operations, or audit record shape.
- Any write action from the browser. Milestone 3 adds them, and it will need a
  cross-site-request answer of its own: this milestone's streams are authorised by an
  ambient cookie, which is safe **only** because every mutating route requires a signature
  a browser cannot produce. That reasoning does not survive the first browser-driven
  write, and milestone 3 must not inherit it by assumption.
- Mobile-specific layouts beyond the single responsive breakpoint the design system
  already defines.
