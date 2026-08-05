# Contract: Fleet event stream

**Feature**: [../spec.md](../spec.md) · **Plan**: [../plan.md](../plan.md) · **Research**: [../research.md](../research.md)

The new authenticated route FR-019a requires be contracted before it is built. It answers issue
#15: an open dashboard learns about changes it did not cause.

---

## Route

| Method | Path | Audit action |
|---|---|---|
| `GET` | `/dashboard/fleet/stream` | `fleet.open` |

One record per **open**, not per event. An event-per-record trail would be an audit log that grows
with the fleet's activity rather than with requests, and FR-023 counts requests.

## Authorisation

Identical to the pane stream, and for the same reasons:

1. Layer 1 — Cloudflare Access, the existing `verifyBrowser`.
2. Same-origin — the existing `crossSite(r)`: `Sec-Fetch-Site` must be exactly `same-origin`.

**No page token.** This route mutates nothing, and the token exists to authorise state changes. A
read requiring one would be inconsistent with the pane stream, which this route otherwise mirrors
exactly.

Failure → the existing browser refusal, byte-identical to the pane stream's.

## Response

```http
HTTP/1.1 200 OK
Content-Type: text/event-stream; charset=utf-8
Cache-Control: no-store
X-Content-Type-Options: nosniff
```

Write deadlines are extended per write via `http.ResponseController.SetWriteDeadline`, as the pane
stream does. The server's `WriteTimeout` is **not** disabled.

## Events

Framing constants are milestone 2's, unchanged: `data: `, `event: `, `\n\n`.

```
event: appeared
data: {"id":"9f2c1ab47e0d4f6a8b3c5d7e1f0a2b4c"}

event: changed
data: {"id":"9f2c1ab47e0d4f6a8b3c5d7e1f0a2b4c"}

event: vanished
data: {"id":"9f2c1ab47e0d4f6a8b3c5d7e1f0a2b4c"}

```

| Event | Emitted when |
|---|---|
| `appeared` | An owned session enters the fleet — API create, dashboard create, or startup adoption |
| `vanished` | An owned session leaves — dashboard or API destroy, the reaper, or a confirmed-gone discovery |
| `changed` | An owned session's displayed state or name changes |

The payload is **only** an identifier. No rendered HTML, no fleet snapshot, no session fields
([R6](../research.md#r6--the-fleet-stream-carries-identifiers-not-fleets)). The page re-fetches
the card it needs.

### Heartbeat

An SSE comment on the same one-per-second cadence as the pane stream, so a browser that vanished
without closing is detected identically on both routes.

```
: heartbeat

```

### Ownership

Filtered **server-side, before the event is written** (FR-019b). A session belonging to another
identity must never produce a byte on this stream. This is the whole reason the route is
contracted: a stream is not exempt from the ownership check merely because it is not a page.

---

## Contract tests

| Test | Asserts | Must fail when | Covers |
|---|---|---|---|
| Foreign `Sec-Fetch-Site` | The uniform browser refusal; no stream opens | The `crossSite` call is removed | FR-019b |
| No Access assertion | The uniform browser refusal | Layer 1 is skipped for streams | FR-019b |
| Second identity's session created while identity A's stream is open | Nothing is written to A's stream | The ownership filter is removed | FR-019b, FR-021 |
| API-created session, stream open | One `appeared` carrying that id | Only dashboard-originated changes emit | FR-019, SC-006 |
| Reaper destroys an idle session | One `vanished` carrying that id | The reaper path does not emit | FR-019, SC-006 |
| Rename an owned session | One `changed` carrying that id | Rename does not emit | FR-019 |
| Idle deadline crossed | One `changed` | `DisplayState` transitions do not emit | FR-019 |
| Quiet stream held open past 1s | A heartbeat comment, not an event | The heartbeat is dropped | R6 |
| Event payload inspected | Exactly `{"id":"<32 hex>"}` — no name, path, state, or markup | Any session field is added to the payload | R6, FR-025 |
| Stream severed, page inspected | The page says updates have stopped | The page keeps presenting the fleet as current | FR-020, SC-011 |
| One open, many events | Exactly one `fleet.open` audit record | A record is written per event | FR-023 |
| Full stream capture searched for secret, tokens, prompt text, pane content | None present | Any appears | FR-025 |
| `POST` to the stream path | The unknown-route response; never `405` | A method-not-allowed path is added | FR-033 |
