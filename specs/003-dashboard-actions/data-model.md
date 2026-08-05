# Data Model: Dashboard Actions

**Feature**: [spec.md](./spec.md) · **Plan**: [plan.md](./plan.md) · **Research**: [research.md](./research.md)

Three things are new. Two of them are values rather than stored records, and that is the point —
see [R2](./research.md#r2--the-page-token-must-be-stateless-and-this-is-not-a-preference).

---

## PageToken

A value proving a mutating request came from a page this daemon rendered for this identity. **Not
stored anywhere.** It is verified by recomputation, exactly as the layer-2 request signature is.

### Wire format

```
<expiry>.<mac>
```

| Part | Type | Meaning |
|---|---|---|
| `expiry` | decimal Unix seconds | When the token stops being accepted |
| `mac` | 64 lowercase hex chars | `HMAC-SHA256(pageKey, identity + "\n" + expiry)` |

Shape (the MAC is written as a placeholder rather than a realistic value — a document full of
high-entropy strings trains secret scanners and readers alike to ignore them):

```
1785749600.<64 lowercase hex characters>
```

### pageKey

| Property | Value |
|---|---|
| Size | 32 bytes |
| Source | `crypto/rand` at startup |
| Lifetime | Process. Regenerated on restart, invalidating every outstanding token |
| Persisted | **Never** |
| Served | **Never**, by any route, in any form (FR-006) |
| Relationship to `CRSW_SHARED_SECRET` | **None.** Independent random key ([R2](./research.md)) |

### Validation

In order. Any failure produces the uniform refusal and a `dashboard.reject` record.

1. Field present and non-empty.
2. Splits on the **last** `.` into exactly two parts.
3. `expiry` parses as a base-10 integer.
4. `expiry` is **strictly greater than** now.
5. `mac` is 64 hex characters.
6. `hmac.Equal` against the recomputation using the **request's own verified identity**, never an
   identity taken from the request body or the token itself.

Step 6 is what binds the token to the identity (FR-007). Step 4 is checked before the MAC only for
clarity of failure reporting; both must pass and the response is identical either way.

### Why there is no expiry sweep, no map, and no rotation

Because there is nothing stored. A stateless token cannot leak, grow, or need collecting. Milestone
2 removed per-browser state deliberately and recorded the reason; this design does not put it back.

### FR-008 is satisfied structurally

The token check runs **after** the layer-1 Access verification, in the same middleware. An identity
whose Access session has ended is refused at layer 1 and the token is never examined. There is no
bookkeeping that could drift out of step with the Access session, because there is no bookkeeping.

---

## FleetEvent

One change to the authenticated identity's fleet, written to the fleet stream. Carries an
identifier and a verb, never rendered HTML and never a fleet snapshot ([R6](./research.md)).

### Wire format

SSE, reusing milestone 2's framing constants exactly (`data: `, `event: `, `\n\n`).

```
event: <verb>
data: {"id":"<32 hex>"}

```

| Field | Values |
|---|---|
| `verb` | `appeared`, `vanished`, `changed` |
| `id` | The 32-hex session identifier |

| Verb | Emitted when |
|---|---|
| `appeared` | A session owned by this identity enters the fleet, by any means — API, dashboard, or startup adoption |
| `vanished` | A session leaves the fleet, by any means — destroy, the reaper, or a confirmed-gone discovery |
| `changed` | An owned session's displayed state or name changes |

A heartbeat comment is written on the same one-per-second cadence as the pane stream, so a dead
connection is detected identically on both routes.

### Ownership

Filtered **server-side before the event is written** (FR-019b). An event for a session another
identity owns must never reach the wire — being a stream rather than a page must not become a way
to observe another owner's fleet.

---

## Session — changed fields

No new entity. Two existing fields gain a writer.

| Field | Change | Rules |
|---|---|---|
| `Name` | Now mutable via rename | Same validation as create: `^[a-zA-Z0-9-]{1,64}$`, `:` and `.` rejected. Names need not be unique — the daemon addresses sessions by identifier only |
| `LastActivity` | Touched by compact | Compact is activity: it delivers bytes into the session, so it defers the idle deadline exactly as a prompt does |

**`TmuxName` is unchanged and unchangeable.** It derives from the identifier
(`crswd-<id>`), so rename is a record-only change (FR-015). Every tmux target, every audit
`SessionID`, and every route parameter continues to use the identifier. This is why SC-012 —
renaming leaves 100% of identifier-based operations behaving identically — is verifiable rather
than aspirational.

### State transitions

None added. Rename and compact do not change a session's state, and destroy uses the existing
transition. `DisplayState` continues to derive from the idle deadline, so a compact that defers
that deadline can move a card from `idle` back to `running` — which is correct, and is a `changed`
event on the fleet stream.
