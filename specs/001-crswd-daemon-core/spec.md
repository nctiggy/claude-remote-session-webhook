# Feature Specification: crswd Daemon Core (Milestone 1)

**Feature Branch**: `001-crswd-daemon-core`

**Created**: 2026-08-02

**Status**: Draft — clarifications resolved, ready for `/speckit-plan`

**Input**: User description: "Milestone 1: the crswd daemon core. Config, tmux control, session CRUD, HMAC auth, audit log, and the reaper. No web UI in this milestone. The task list and four resolved decisions live in ralph/IMPLEMENTATION_PLAN.md. The constitution and docs/security.md plus docs/auth-and-sessions.md are binding. Mark anything you cannot determine as NEEDS CLARIFICATION rather than guessing."

## Context

This milestone delivers the daemon (`crswd`) with **no user interface**. The only
client is a program that speaks the signed HTTP API — a script, a `curl` invocation,
or (later) the companion Claude skill. The operator is a single person running the
daemon on their own machine.

A request that passes authentication causes **unsandboxed code execution on the
host**. There is no sandbox behind the auth check. Every requirement below is
ranked by that fact, and the binding documents
([`docs/security.md`](../../docs/security.md),
[`docs/auth-and-sessions.md`](../../docs/auth-and-sessions.md),
[`.specify/memory/constitution.md`](../../.specify/memory/constitution.md)) are
treated as requirements sources, not as advice.

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Start a Claude session on my machine from somewhere else (Priority: P1)

The operator, away from their desk, sends a signed request naming a working
directory and a session name. The daemon verifies the request came from someone
holding the shared secret, confirms the directory is one it is allowed to touch,
starts a session on the host, and returns an identifier plus a one-time credential
for driving that session. An unsigned, missigned, stale, or replayed request gets
nothing.

**Why this priority**: This is the product. Without it there is no reason for the
daemon to exist — and because the request causes code execution, the authentication
around it is not a separate story that can be deferred; it ships in the same slice
or the slice is a vulnerability.

**Independent Test**: Send a correctly signed create request and observe a live
session on the host plus an identifier and credential in the response. Then send
the same request unsigned, with a tampered body, with a timestamp five minutes old,
with a timestamp an hour in the future, and a byte-identical replay of the good one
— all five must be refused with the same response, and must create no session.

**Acceptance Scenarios**:

1. **Given** a running daemon and a correctly signed create request naming an
   allowed working directory, **When** the request is sent, **Then** a session is
   started on the host and the response carries an unguessable session identifier
   and a bearer credential returned exactly once.
2. **Given** a create request with a valid body but no signature header, **When** it
   is sent, **Then** it is refused and no session is created.
3. **Given** a correctly signed request whose body is altered after signing,
   **When** it is sent, **Then** it is refused with the same response as an unsigned
   request, revealing nothing about which check failed.
4. **Given** a request whose timestamp is more than five minutes in the past *or*
   more than five minutes in the future, **When** it is sent, **Then** it is refused.
5. **Given** a request that was already accepted once, **When** the identical bytes
   are sent again inside the signature window, **Then** the second attempt is refused
   and no second session is created.
6. **Given** a create request naming a working directory outside the approved roots
   — by traversal, by absolute path, or via a symbolic link pointing outward —
   **When** it is sent, **Then** it is refused and no session is created.
7. **Given** a create request whose session name contains `:`, `.`, whitespace, or
   any character outside `a-zA-Z0-9-`, **When** it is sent, **Then** it is refused.
8. **Given** a create request whose body contains an unrecognised field or exceeds
   the body size limit, **When** it is sent, **Then** it is refused.
9. **Given** no approved-roots value in the environment, **When** the daemon starts,
   **Then** it starts on its built-in default root and emits a prominent warning
   naming both the unset environment value and the path now in force.
10. **Given** the daemon started on its built-in default root, **When** a create
    request names a directory outside that root, **Then** it is refused exactly as it
    would be under an explicitly configured root.

---

### User Story 2 - Drive a running session and read what it said (Priority: P2)

The operator sends a prompt into an existing session and later reads back what is
on that session's screen. The prompt is delivered to the session exactly as typed —
shell metacharacters, quotes, and newlines are inert data, not instructions. The
output comes back as plain text with terminal control sequences removed.

**Why this priority**: A session that cannot be prompted or read is a session that
only burns host resources. This is what makes P1 useful, but P1 is independently
valuable (the operator can still attach to the session by hand on the host).

**Independent Test**: Create a session, send a prompt containing `;`, `$(id)`,
backticks, and an embedded newline, then read the output back and assert the literal
characters appear and that no substitution or extra command execution occurred.

**Acceptance Scenarios**:

1. **Given** a session the caller owns, **When** a signed, correctly credentialed
   prompt request is sent, **Then** the prompt text arrives in that session verbatim.
2. **Given** a prompt containing `;`, `$(...)`, backticks, quotes, or newlines,
   **When** it is delivered, **Then** none of it is interpreted by a shell and no
   additional command runs.
3. **Given** a session that has produced coloured or cursor-positioned output,
   **When** its output is read, **Then** the response contains readable text with
   terminal escape sequences stripped.
4. **Given** a prompt or output request carrying a valid signature but the wrong
   session credential, **When** it is sent, **Then** it is refused.

---

### User Story 3 - See what is running and shut it down for certain (Priority: P3)

The operator lists their sessions, inspects one, and destroys it. Destruction is
confirmed, not assumed: the daemon reports success only after verifying the session
is actually gone from the host. Another owner's session is invisible — reads scoped
to it are indistinguishable from reads of an identifier that never existed.

**Why this priority**: Needed for the operator to keep the host under control, but
the reaper (P5) provides a safety net if this slice lands later.

**Independent Test**: Create sessions as two distinct owners. Assert each owner's
list contains only their own; assert owner B using a valid signature on owner A's
identifier gets exactly the same response as for a random identifier. Destroy a
session and assert nothing corresponding to it survives on the host.

**Acceptance Scenarios**:

1. **Given** several sessions owned by the caller, **When** the list is requested,
   **Then** every session the caller owns is returned and no session owned by anyone
   else is.
2. **Given** a session owned by a different owner, **When** the caller requests it by
   identifier, **Then** the response is identical to the response for an identifier
   that does not exist — same status, same body, no enumeration signal.
3. **Given** a session the caller owns, **When** destroy is requested, **Then** the
   daemon terminates it, confirms it is gone from the host, and only then reports
   success.
4. **Given** a session whose termination did not take effect, **When** destroy is
   requested, **Then** the daemon reports failure and records the surviving session
   prominently, rather than reporting success.
5. **Given** a destroyed session, **When** any endpoint scoped to its identifier is
   called, **Then** nothing about it is returned and its stored credential no longer
   authorises anything.

---

### User Story 4 - Restart the daemon without leaving unowned shells behind (Priority: P4)

The daemon is restarted — a deploy, a crash, a reboot of the service. Sessions
started before the restart are still alive on the host. On startup the daemon finds
them, takes them back under management, and issues fresh credentials for them. It
never leaves a live unsandboxed shell running with no owner and no timeout.

**Why this priority**: Directly required by Constitution Principle VI — an orphaned
session is a live unsandboxed shell with no owner. It ranks below P1–P3 only because
it cannot occur until sessions exist to survive a restart.

**Independent Test**: Record a live session, discard the daemon's in-memory state as
a restart would, run startup reconciliation, and assert the surviving session is
listed, owned, subject to timeouts, and destroyable — and that a similarly-named
session the daemon did not create is left alone. Then restart repeatedly against a
session whose host start time is 23 hours old and assert it still dies at 24 hours.

**Acceptance Scenarios**:

1. **Given** a session started by a previous daemon run that is still alive, **When**
   the daemon starts, **Then** it is taken under management as the operator's own,
   appears in listings, and is destroyable through the API.
2. **Given** an adopted session, **When** the operator asks to drive it, **Then** a
   freshly issued credential is required — credentials from before the restart are
   permanently unusable.
3. **Given** a host session whose name matches the daemon's prefix but which the
   daemon did not create, **When** startup reconciliation runs, **Then** it is not
   adopted and not destroyed.
4. **Given** a half-dead session — present but with no usable process behind it —
   **When** startup reconciliation runs, **Then** the daemon resolves it to a
   definite state rather than recording it as healthy.
5. **Given** a surviving session whose host session started 23 hours ago, **When** the
   daemon adopts it, **Then** its absolute lifetime is counted from that original
   start time and it is destroyed an hour later — not 24 hours after adoption.
6. **Given** a surviving session that already passed its 24-hour ceiling while the
   daemon was down, **When** reconciliation runs, **Then** it is destroyed rather than
   adopted.
7. **Given** repeated daemon restarts within one session's lifetime, **When** each
   reconciliation runs, **Then** the session's absolute deadline is unchanged by any
   of them.

---

### User Story 5 - Abandoned sessions die on their own (Priority: P5)

A session the operator forgot about stops consuming the host. Idleness and total
age both end a session without anyone sending a request, and the daemon refuses to
start more sessions than the host is configured to carry.

**Why this priority**: Constitution Principle VI requires it, and it is the last
line of defence when the operator's client disappears mid-flight. It sits below the
CRUD slices because it only matters once sessions run unattended.

**Independent Test**: With a controllable clock (no real waiting), advance past the
idle timeout and assert the session is destroyed with teardown verified; advance
past the absolute lifetime on a session that is still being used and assert it is
destroyed anyway. Separately, create sessions up to the cap and assert the next
create is refused rather than accepted.

**Acceptance Scenarios**:

1. **Given** a session with no activity for the idle timeout, **When** that timeout
   elapses, **Then** the daemon destroys it unprompted with teardown verified.
2. **Given** a session that has been actively used for its full absolute lifetime,
   **When** that lifetime elapses, **Then** the daemon destroys it anyway — there is
   no renewal.
3. **Given** the concurrent session cap is reached, **When** another create request
   arrives, **Then** it is refused with a distinct, non-leaking error and the
   existing sessions are unaffected.
4. **Given** a caller creating sessions faster than the configured rate, **When** the
   rate is exceeded, **Then** further create requests are refused until the rate
   recovers.
5. **Given** running sessions, **When** the daemon receives a termination signal,
   **Then** it tears those sessions down with verification before exiting.

---

### User Story 6 - Answer "what happened on my host" afterwards (Priority: P6)

Every request that reaches the daemon leaves a machine-readable record — when, from
which caller, what action, which session, and whether it was allowed or refused. The
records are safe to read and safe to ship: they never contain prompt text, session
output, credentials, or the shared secret.

**Why this priority**: Required by both binding documents, and it is what turns an
incident into an investigation. It is last because the system is functional without
it — but it is not shippable without it.

**Independent Test**: Exercise every endpoint with both accepted and refused
requests, capture the emitted records, assert each request produced exactly one
record with the required fields, and assert a scan of the whole capture finds no
prompt text, no pane content, no credential, and no secret material.

**Acceptance Scenarios**:

1. **Given** any request, accepted or refused, **When** it is handled, **Then**
   exactly one structured record is emitted carrying timestamp, caller, action,
   session identifier (where applicable), and the allow/deny decision.
2. **Given** a request whose body contains a prompt, **When** the record is emitted,
   **Then** the prompt text does not appear in it.
3. **Given** a response containing session output, **When** the record is emitted,
   **Then** no session output appears in it.
4. **Given** a rejected authentication attempt, **When** the record is emitted,
   **Then** it records the refusal without echoing the presented credential or
   revealing to the caller which check failed.

---

### Edge Cases

- **Session name is hostile**: contains `:` or `.` (which address a different host
  session target), a path separator, a leading `-`, control characters, or non-ASCII
  — all refused before anything is started.
- **Working directory escapes**: `../../etc`, an absolute path outside the roots, a
  path that is inside a root but is a symbolic link pointing outside, a path that
  does not exist, and a path that exists but is not a directory.
- **Body abuse**: unknown JSON fields, a body larger than the limit, a truncated
  body, a body that is valid JSON but the wrong shape, and an empty body on an
  endpoint that requires one.
- **Clock abuse**: timestamp far in the future, far in the past, non-numeric,
  missing, and a timestamp that is valid while the signature covers a different one.
- **Replay**: the same signed request sent twice, sent concurrently twice, and sent
  again after the replay cache TTL but still inside the signature window.
- **Identifier abuse**: a well-formed identifier that does not exist, another owner's
  identifier, and a destroyed session's identifier — all producing the same response.
- **Host-side failures**: the session multiplexer is not installed or not on the
  path; a session dies on its own between requests; termination reports success but
  the session survives; output capture fails; the working directory disappears after
  the session started.
- **Startup failures**: shared secret unset or too short; the port already in use —
  each an explicit non-zero exit with no listener bound, never a degraded start.
- **Startup on the default root**: the approved-roots value is unset, so the daemon
  starts on its built-in default and must announce that fact prominently; the value
  is set but names a path that does not exist, is not a directory, or is a symbolic
  link out of the daemon user's control; the value is set but empty.
- **Concurrency**: two creates racing at the cap boundary; a destroy racing the
  reaper on the same session; a prompt arriving while the reaper is tearing the
  session down.
- **Restart**: a surviving session, a survivor the daemon did not create, a survivor
  that is present but unusable, and a survivor that already exceeded its 24-hour
  ceiling while the daemon was down.
- **Credential expiry**: a credential presented for a session that has already been
  reaped, and a credential presented at the exact moment its session's absolute
  lifetime elapses.

## Requirements *(mandatory)*

### Functional Requirements

#### Configuration and startup

- **FR-001**: The daemon MUST take all configuration from its environment. No
  configuration value, and in particular no secret, may be read from a file
  committed to the repository.
- **FR-002**: The daemon MUST refuse to start — non-zero exit, no listener bound, no
  session started — when the shared secret is unset or shorter than 32 bytes. A
  weakened authentication configuration is never a warning.
- **FR-003**: The set of approved working-directory roots MUST come from an optional
  environment value. When it is unset, the daemon MUST fall back to a single narrow
  built-in default — the `code` directory under the daemon user's home — and MUST NOT
  fall back to the home directory itself or to any broader path.
- **FR-004**: When the daemon falls back to the built-in default root, it MUST say so
  loudly at startup: a prominent warning naming the environment value that was unset
  and the exact path now in force. An unconfigured allowlist is never silent. The
  warning MUST be emitted on every start that uses the default, not only the first.
- **FR-005**: The daemon MUST bind its listener to the loopback interface only.
  Reachability comes from the tunnel, never from the listener.
- **FR-006**: The daemon MUST expose exactly these operations in this milestone —
  create a session, list sessions, read one session, destroy a session, send a prompt
  to a session, read a session's output — and no others. There is no unauthenticated
  health or status route.

#### Request authentication (layer 2)

- **FR-007**: Every registered route MUST require a valid request signature computed
  over the request method, path, timestamp, and raw body together. No route is
  exempt. The method and path are covered so that a signature names the request it
  authorizes: over the timestamp and body alone, every empty-body request at one
  instant signs identically, so one signed read is a valid destroy in the same
  second and a client reading twice in one second is refused as a replay.
- **FR-008**: The daemon MUST reject any request whose timestamp differs from its own
  clock by more than 300 seconds **in either direction**.
- **FR-009**: The daemon MUST compare signatures in constant time.
- **FR-010**: The daemon MUST refuse a request whose signature has already been
  accepted, retaining seen signatures for twice the signature window.
- **FR-011**: All authentication failures MUST produce one uniform response that
  reveals nothing about which check failed. The specific reason MUST be recorded
  server-side only.
- **FR-012**: Caller identity MUST be derived server-side from the credential
  presented, never from a field the caller supplies in the body, path, or headers.

#### Session credentials (layer 3)

- **FR-013**: Creating a session MUST return a bearer credential generated from a
  cryptographically secure random source, delivered exactly once, and retained by the
  daemon only as a one-way hash. The plaintext credential MUST never be stored,
  logged, or returned again.
- **FR-014**: Every session-scoped operation MUST require both a valid request
  signature and the matching session credential. Holding the shared secret alone MUST
  NOT grant access to a session the caller did not create.
- **FR-015**: A session credential's lifetime MUST equal the session's absolute
  lifetime of 24 hours, so that a session is driveable, readable, and destroyable by
  its owner for exactly as long as it exists. A credential MUST stop being accepted
  once that lifetime expires, and MUST NOT outlive the session it belongs to. There
  is no credential renewal and no re-issue path: a session is reachable for its whole
  life on the credential handed out at creation, and then it is gone.

#### Session lifecycle

- **FR-016**: Session identifiers MUST come from a cryptographically secure random
  source, carry at least 128 bits of entropy, and MUST NOT be sequential or otherwise
  guessable.
- **FR-017**: Every session record MUST carry an owner, populated from the credential
  used to create it, from the first version onward.
- **FR-018**: Creating a session MUST start the operator's login shell in a dedicated
  host session window named with the daemon's reserved prefix followed by the session
  identifier, and then deliver the Claude start command into that window as
  keystrokes — so that a Claude crash leaves an inspectable window rather than a dead
  session.
- **FR-019**: Destroying a session MUST terminate it and then **verify** it is gone.
  Success MUST be reported only after that verification. A failed teardown MUST be
  reported as a failure and recorded prominently.
- **FR-020**: Destroying a session MUST also clear its record, its stored credential
  hash, any buffered or cached output, and any working directory the daemon created
  for it.
- **FR-021**: On startup the daemon MUST reconcile with the host: every live session
  bearing its reserved prefix that has no in-memory record MUST be taken under
  management, with a freshly issued credential. Credentials issued before the restart
  are unrecoverable by design.
- **FR-022**: Reconciliation MUST NOT adopt a host session that merely resembles the
  prefix but was not created by the daemon, and MUST resolve a present-but-unusable
  session to a definite state rather than recording it as healthy.
- **FR-023**: An adopted session MUST be subject to the same ownership check and the
  same timeouts as one created through the API. Its owner MUST be the single
  configured operator identity.
- **FR-024**: An adopted session's absolute lifetime MUST be measured from the
  underlying host session's own start time, not from the moment of adoption, so that
  restarting the daemon cannot extend a session past the 24-hour ceiling. Its idle
  clock MUST be reset at adoption, since the daemon has no record of when the session
  was last touched.
- **FR-025**: A session whose absolute lifetime has already elapsed while the daemon
  was down MUST be destroyed by the reconciliation pass rather than adopted into an
  already-expired state.

#### Input validation at the boundary

- **FR-026**: Request bodies MUST be decoded into a fixed shape with unrecognised
  fields rejected and a maximum body size enforced before decoding.
- **FR-027**: Session names MUST match `^[a-zA-Z0-9-]{1,64}$`, with `:` and `.`
  rejected explicitly because they address a different host session target.
- **FR-028**: A caller-supplied working directory MUST be normalised to a canonical
  absolute path with symbolic links resolved, and then confirmed to lie under an
  approved root. Anything else is refused. A caller never names an arbitrary path.
- **FR-029**: The daemon MUST NOT construct a shell command string anywhere. Host
  commands are invoked with an explicit argument list.
- **FR-030**: Prompt text MUST be delivered to a session as a single literal
  argument. Shell metacharacters, quotes, substitutions, and newlines inside a prompt
  MUST NOT be interpreted.
- **FR-031**: Captured session output MUST have terminal escape sequences removed
  before it leaves the daemon.

#### Session isolation

- **FR-032**: Every session-scoped request MUST be authorised against the session's
  recorded owner, not merely authenticated.
- **FR-033**: A request for a session owned by someone else MUST produce the same
  response as a request for a session that does not exist — no status, body, or
  header difference that permits enumeration.
- **FR-034**: Every read path MUST resolve its target from the authenticated,
  ownership-checked session record. A caller-supplied host session target string MUST
  NOT be usable to address a window.
- **FR-035**: Output produced by one session MUST NOT be reachable through any
  request scoped to another session.

#### Resource bounds

- **FR-036**: The daemon MUST enforce a cap on concurrent sessions and refuse
  creation past it rather than degrading the host.
- **FR-037**: The daemon MUST rate-limit session creation per caller.
- **FR-038**: A background reaper MUST destroy any session idle for longer than 60
  minutes and any session older than 24 hours, without requiring a request to trigger
  it, and with the same verified teardown as an explicit destroy. There is no renewal
  of the absolute lifetime.
- **FR-039**: The reaper's notion of time MUST be injectable so that its behaviour is
  testable without real elapsed time.
- **FR-040**: On a termination signal the daemon MUST shut down gracefully, tearing
  down its sessions with verification before exiting.

#### Audit

- **FR-041**: Every request MUST produce exactly one structured, machine-readable
  record on standard output containing at least: timestamp, caller identity, action,
  session identifier where applicable, and the allow/deny decision. Records are
  captured by the host service manager; the daemon MUST NOT write audit files or
  implement its own rotation.
- **FR-042**: Audit records MUST NOT contain prompt text, session output, session
  credentials, or the shared secret. This MUST be asserted by test, not by review.
- **FR-043**: No log line at any level may contain the shared secret, a session
  credential, a full signed body, or captured session output.

### Key Entities

- **Session**: One Claude Code session running in a host terminal window. Carries an
  unguessable identifier, an owner, a name, a validated working directory, a creation
  time, a last-activity time, a lifecycle state, and the hash of its bearer
  credential. Held in memory only — it is never written to disk in this milestone.
- **Caller**: The authenticated origin of a request, established server-side from the
  presented credential. In this milestone there is exactly one real caller identity;
  the field and the ownership check exist so a second identity source can be added
  without changing a handler.
- **Session credential**: A one-time bearer secret bound to exactly one session,
  returned at creation, retained only as a hash, and valid for exactly as long as the
  session it belongs to.
- **Approved root**: A directory under which sessions are permitted to run. Callers
  select from within these; they never define them. Set by the operator through the
  environment, or — announced loudly — the built-in default.
- **Seen-signature entry**: A record that a given signature has already been accepted,
  retained long enough to make the signature window non-replayable.
- **Audit record**: The structured account of one request — who, what, which session,
  allowed or refused — containing no secret and no session content.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: 100% of the daemon's operations refuse an unsigned request, a request
  with a tampered body, and a request signed with the wrong secret — verified per
  operation, with zero exempt routes.
- **SC-002**: A captured valid request replayed inside the signature window is
  refused 100% of the time and results in zero additional sessions.
- **SC-003**: Requests timestamped more than five minutes from the daemon's clock are
  refused in both the past and future directions, with zero accepted.
- **SC-004**: The operator can go from a signed request to a live session on the host
  in a single round trip, receiving the identifier and one-time credential in that
  same response.
- **SC-005**: A session belonging to another owner is indistinguishable from one that
  never existed: identical status and body across 100% of session-scoped operations.
- **SC-006**: Zero unowned host shells remain after any teardown path — explicit
  destroy, reaper expiry, or shutdown — with teardown verified rather than assumed in
  every case.
- **SC-007**: After a daemon restart, 100% of surviving daemon-created host sessions
  are under management again, with an owner and active timeouts; zero remain
  unmanaged, and zero host sessions the daemon did not create are touched.
- **SC-008**: An untouched session is destroyed within its idle timeout and no
  session survives past its absolute lifetime, in both cases with no client request
  involved.
- **SC-009**: No session survives more than 24 hours from the moment its host
  session started, across any number of daemon restarts in that period. Restarting
  the daemon extends zero sessions.
- **SC-010**: A session is driveable, readable, and destroyable by its owner for
  100% of its life — there is no interval in which a live session exists that its
  owner cannot reach with the credential issued at creation.
- **SC-011**: Session creation past the concurrent cap is refused 100% of the time,
  and the host never runs more concurrent sessions than the configured cap.
- **SC-012**: A prompt containing shell metacharacters, command substitution, and
  embedded newlines reaches the session byte-for-byte, with zero unintended commands
  executed.
- **SC-013**: A full capture of audit output from an end-to-end exercise of every
  operation contains zero occurrences of prompt text, session output, session
  credentials, or the shared secret, and exactly one record per request.
- **SC-014**: Every startup misconfiguration case — secret missing, secret too short
  — exits non-zero with no listener bound; zero cases start in a degraded mode.
- **SC-015**: Every start that falls back to the built-in default root emits a
  prominent warning naming the unset environment value and the path now in force.
  Zero starts adopt the default silently, and the built-in default is never the
  daemon user's home directory or any parent of it.
- **SC-016**: The listener is bound to loopback in 100% of runs, asserted by test.
- **SC-017**: Output produced in one session appears in zero responses scoped to any
  other session.
- **SC-018**: Build, test, and lint all pass, and every behaviour above is covered by
  a test that fails when the behaviour is removed — including the negative cases (bad
  signature, stale timestamp, replay, wrong owner).

## Assumptions

Reasonable defaults chosen where the source material did not specify, plus the
decisions the operator resolved. Everything here can be overturned cheaply.

### Resolved during specification

Three ambiguities were surfaced rather than guessed at, and answered by the operator
on 2026-08-02:

- **Approved roots come from an optional `CRSW_ALLOWED_ROOTS`-style environment
  value, defaulting to the `code` directory under the daemon user's home.** The
  daemon starts either way — but a start on the default is announced loudly, so an
  unset value is visible in the service log rather than silently in force. The
  default is deliberately narrow: never the home directory itself, which would make
  the allowlist decorative by exposing SSH keys, cloud credentials, and browser
  profiles. (FR-003, FR-004.) The exact default path assumes the operator's repos
  live under `~/code`; if that is wrong the value is one environment line to change.
- **Session credential lifetime is raised from 12 hours to 24 hours, matching the
  absolute session lifetime.** As written, the 12-hour credential left up to 12 hours
  in which a live unsandboxed session could not be driven, read, or destroyed by its
  owner — reachable only by the reaper. One credential now covers exactly one
  session's life, with no renewal and no re-issue path. **This amends the Lifetimes
  table in `docs/auth-and-sessions.md`, changed in the same commit as this spec so
  the binding document and the spec never disagree.** (FR-015.)
- **An adopted session is owned by the single configured operator identity, and its
  absolute lifetime is measured from the underlying host session's own start time.**
  Only the idle clock resets at adoption, because the daemon genuinely has no record
  of when the session was last touched. Restarting the daemon therefore cannot extend
  a session past 24 hours — a restarting clock would have let a restart loop hold a
  session open indefinitely, defeating the ceiling Principle VI calls non-negotiable.
  (FR-023, FR-024, FR-025.)

### Assumed defaults

- **Single operator.** Exactly one human operates this daemon. The `Owner` field and
  the ownership check nevertheless exist from day one, and the cross-owner tests use a
  synthetic second owner. (Resolved decision, `ralph/IMPLEMENTATION_PLAN.md`.)
- **Window contents.** The host window runs the login shell and the daemon sends the
  Claude start command as keystrokes, so a Claude crash leaves inspectable scrollback
  and milestone 4 has a prompt to relay into. (Resolved decision.)
- **Restart behaviour.** Surviving daemon-created host sessions are adopted, never
  ignored. (Resolved decision.)
- **Audit destination.** Structured JSON on standard output, captured by the host
  service manager. No file mode, no rotation, no disk-fill failure mode. (Resolved
  decision.)
- **Concurrent session cap: assumed operator-configurable with a conservative default
  in the single digits.** No value is stated in any binding document. The exact
  default is a tuning decision, not a correctness one — but it should be confirmed
  during `/speckit-clarify` rather than discovered in production.
- **Creation rate limit: assumed operator-configurable, on the order of a few creates
  per minute per caller.** Same reasoning as the cap.
- **Listener port is configurable via the environment**, with the tunnel configured to
  match. No default is implied by the binding documents.
- **The daemon does not create working directories.** A caller names an existing
  directory under an approved root; a non-existent path is refused rather than made.
- **No persistence.** Session records live in memory only. Restart recovery comes from
  adopting live host sessions, not from a database. (Explicitly out of scope in the
  milestone plan.)
- **Idle is measured from the last request that touched the session**, since the
  daemon has no reliable signal for activity inside the session itself.
- **Body size limit and the exact refusal codes for cap and rate-limit breaches are
  implementation choices** to be fixed in the plan, provided they leak nothing about
  other sessions.

### Dependencies

- A terminal multiplexer is installed and usable by the daemon's user on the host.
  The daemon exercises it through an interface, so unit tests use a fake and never a
  real one.
- The shared secret is supplied through the environment by the service manager,
  sourced from a password manager rather than any file in this repository.
- Reachability from outside the host is provided by an outbound tunnel configured
  separately. This milestone assumes it but does not deliver it.

## Out of Scope

Deliberately excluded from this milestone, so that no implementation task wanders
into them:

- The web dashboard — templates, styling, live output streaming. (Milestone 2.)
- Edge identity validation of the browser's signed login assertion. (Milestone 2.)
- Rename and compact operations. (Milestone 3.)
- Relaying Claude Code's own device-code login. (Milestone 4.)
- The companion Claude skill — it comes after the API is stable.
- Persisting session records to disk.
- Multi-user support beyond the owner field and its enforcement.
