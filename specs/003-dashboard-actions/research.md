# Research: Dashboard Actions

**Feature**: [spec.md](./spec.md) · **Plan**: [plan.md](./plan.md)

Decisions taken before implementation, each with what was rejected. The spec settled *what* to
build (D1–D3 in its Resolved Decisions); this settles *how*, and two of the answers are materially
different from what the spec anticipated — both because the existing code already solved the
problem better than the spec assumed it would have to.

---

## R1 — The same-origin half of D1 is already built

**Decision**: Reuse `crossSite(r)` from `internal/httpapi/stream.go`, which reads
`Sec-Fetch-Site` and admits only `same-origin`. Do **not** add an `Origin` comparison.

**Why this differs from the spec.** FR-002a says the request's `Origin` must match. That was
written without knowing the daemon already had a same-origin check — milestone 2 built one for the
pane stream (its FR-034d, research D8), and it is stronger than an `Origin` comparison for this
purpose:

- `Sec-Fetch-Site` is set by the browser and **cannot be set by script**, the same as `Origin`.
- It distinguishes `none` — a URL typed, bookmarked, or opened by no page at all — which an
  `Origin` check cannot cleanly see. The existing comment gives the reasoning: *"the dashboard
  opens this address from its own page, so a stream nobody's page asked for is not a stream this
  daemon owes anyone."*
- It already fails closed on any unrecognised value, with an exact rather than case-folded
  comparison, and it already has tests.

Adding a second, weaker check beside it would be a larger change that verifies less. Constitution
IV (smallest correct change) points the same way.

**FR-002a is therefore satisfied by `Sec-Fetch-Site`, not `Origin`.** This is a change of
mechanism, not of intent: the requirement asks for positive evidence of same-origin initiation, and
this is that evidence. The spec text should be read accordingly; `tasks.md` must not generate a
task that adds an `Origin` header check.

**Rejected**: `Origin` as specified (weaker on the `none` case, and duplicates working code);
requiring both (a third thing to keep in agreement, and FR-002c's requirement that each half be
independently disableable gets muddier with three).

---

## R2 — The page token must be stateless, and this is not a preference

**Decision**: The token is a self-authenticating value the daemon can verify without storing
anything: `<expiry-unix>.<hex HMAC-SHA256(pageKey, identity + "\n" + expiry)>`. `pageKey` is 32
random bytes generated at startup and never persisted or served.

**Why it must be stateless.** Milestone 2 made an explicit, load-bearing choice not to hold any
per-browser state, and wrote down why:

> *there is no per-browser session and no "already checked" state, so a VerifiedOperator is derived
> per request and never stored (FR-036). Caching one would be the daemon's first cross-request
> browser state, and with it the expiry, invalidation and fixation questions this design exists not
> to have.*

A stored CSRF token would be exactly the state that comment refuses. A minted-and-remembered token
brings back expiry sweeps, an unbounded map an unauthenticated-until-layer-1 caller can grow, and
fixation questions — the whole class milestone 2 designed away. A stateless token has none of them:
nothing to sweep, nothing to grow, nothing to fixate.

**How each requirement is met without storage:**

| Requirement | How |
|---|---|
| FR-007, bound to identity | The identity is inside the MAC. A token minted for one identity fails verification when presented as another |
| FR-008, dies with the Access session | **By construction, not by bookkeeping.** The token check runs *after* the layer-1 Access check in the same middleware. If the Access session has ended, layer 1 refuses and the token is never reached |
| Expiry | The expiry is in the payload and covered by the MAC, so it cannot be extended by the holder |
| FR-006, secret never served | `pageKey` is unrelated to `CRSW_SHARED_SECRET` — a fresh random key, so no served value has any relationship to the signing secret |

**Restart invalidates every token**, because `pageKey` is regenerated. That is acceptable and
already anticipated: session records do not survive a restart either, and FR-031 requires a failed
action to say so rather than silently revert. An open page across a restart gets one clear failure
and a reload fixes it.

**Rejected**: a stored token map (reintroduces exactly the state milestone 2 removed); deriving
`pageKey` from `CRSW_SHARED_SECRET` (creates a relationship between a served artefact and the
signing secret, for no benefit — a random key is strictly safer and no harder).

---

## R3 — Token lifetime: 12 hours

**Decision**: Tokens expire 12 hours after minting.

Long enough that a dashboard left open through a working day still acts, short enough to bound the
value of one captured token. The failure at expiry is visible and recoverable (reload), not silent.
Nothing about the design changes if this number changes; it is one constant.

**Rejected**: matching the 24-hour session ceiling (a token outliving the page that carried it buys
nothing); minutes (turns an open dashboard into a source of spurious failures).

---

## R4 — Delivery: form posts, not fetch

**Decision**: Actions are HTML form submissions to dedicated routes. Responses are HTML fragments
that replace the card, matching how milestone 2 renders.

Keeps the no-JavaScript-framework posture, makes FR-028 (keyboard operability) close to free since
a submit button is already keyboard-operable, and means an action still works if scripting fails.
The token rides as a hidden form field; `Sec-Fetch-Site` is set by the browser either way.

**Rejected**: `fetch` with a JSON body (needs script for the basic path, and FR-028 becomes work
rather than a property of the markup).

---

## R5 — Audit vocabulary

**Decision**: New actions follow the existing `noun.verb` naming in `internal/audit/audit.go`, and
FR-024 (a browser action must be distinguishable from its API equivalent) is met by a distinct
prefix rather than by a flag on an existing action:

| Action | When |
|---|---|
| `dashboard.create` | Session created from the browser |
| `dashboard.destroy` | Session destroyed from the browser |
| `dashboard.rename` | Session renamed |
| `dashboard.compact` | Compact delivered |
| `dashboard.reject` | A mutating browser request refused by the cross-site defence |
| `fleet.open` | A fleet stream opened |

`dashboard.reject` is separate from the existing `access.reject` on purpose: an identity that
passed layer 1 and then failed the cross-site check is a different and more alarming event than one
that never got in, and FR-026 requires an operator to be able to recognise it as an attack rather
than a mistake. Collapsing the two would bury it.

`Reason` on a rejection records which check failed — server-side only, since FR-004 requires the
response itself to be uniform.

**Rejected**: reusing `session.create`/`session.destroy` with a caller field (FR-024 asks for
distinguishable actions, and a grep for `dashboard.` should return every browser-initiated change).

---

## R6 — The fleet stream carries identifiers, not fleets

**Decision**: The stream emits one event per change, naming what changed. It does **not** push
rendered HTML or a whole fleet snapshot.

A snapshot per change is O(fleet) bytes for a one-card change and makes the diffing the browser's
problem. Naming the change lets the page act on exactly the card affected, and keeps the event
small enough to reason about in a contract.

Ownership filtering happens server-side before an event is written (FR-019b). The stream reuses the
existing SSE framing constants (`data: `, `event: `, `\n\n`) and the one-write-per-second heartbeat
cadence milestone 2 established, so a dead connection is detected the same way on both routes.

**Rejected**: pushing rendered fragments (couples the stream to template structure and makes the
contract untestable without rendering); pushing full snapshots (bandwidth proportional to fleet size
for every single change).

---

## R7 — Idempotence without new state

**Decision**: FR-018 (a repeated action must not duplicate its effect) is satisfied by the actions'
own semantics rather than by request-deduplication machinery.

- **Destroy** — the second destroy finds no record and returns the uniform not-found response. Only
  one teardown ever happens.
- **Rename** — setting the same name twice is the same end state.
- **Compact** — delivering `/compact` twice is two deliveries, which is what the operator asked
  for. Not a duplicated *effect* in the sense FR-018 means.
- **Create** — the only genuine exposure. Two rapid submissions produce two sessions. This is
  bounded by the existing concurrent-session cap and rate limit rather than by new state, and the
  contract requires the submit control to disable itself on submission (FR-031's in-progress state
  does double duty here).

**Rejected**: an idempotency-key table — new cross-request state, which R2 argues against for the
same reasons, to solve a problem the cap already bounds.
