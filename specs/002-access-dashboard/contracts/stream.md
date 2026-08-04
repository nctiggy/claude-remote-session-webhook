# Contract: The Live Output Stream (SSE)

**Feature**: [../spec.md](../spec.md) · **Plan**: [../plan.md](../plan.md) ·
**Research**: [../research.md](../research.md) · **Data model**: [../data-model.md](../data-model.md)

One route: `GET /sessions/{id}/stream`, on the browser door. It delivers a session's
**current screen** — replaced on each update, never an appended transcript
(FR-031a) — to one browser, for as long as the session lives and the viewer is
allowed to watch it. The entity and its lifecycle invariants are
[data-model.md § LiveStream](../data-model.md#livestream); this contract is the wire
format, the authorisation, and the timing rules.

The response is `Content-Type: text/event-stream; charset=utf-8`, with the
browser-door security headers and `Cache-Control: no-store`
([dashboard.md](./dashboard.md)) — pane content is secret under
`docs/security.md` §3 and must not land in any cache.

---

## Authorisation at open — ordered, like every other check in this project

| # | Check | On failure | Why this order |
|---|---|---|---|
| 1 | Layer 1: a validated **identity** assertion ([access-jwt.md](./access-jwt.md)) — a service-token assertion is refused here like everywhere on this door | Uniform layer-1 refusal | Nothing session-shaped is asked before the caller is known |
| 2 | Cross-site refusal: if `Sec-Fetch-Site` is present and not `same-origin`, refuse (research [D8](../research.md)) | Refused | Belt-and-braces before any lookup. The header is *present-and-wrong means refuse*, never *absent means refuse* — non-browser clients (and the [quickstart](../quickstart.md)'s `curl`) do not send it, and the protection that must hold without it is the CORS absence below |
| 3 | Ownership: the `{id}` resolves to a session **owned by the verified operator**, through a read that does not touch the idle clock (below) | The uniform 404, byte-identical for unknown, unowned, and dead (SC-016) | Enumeration resistance is milestone 1's rule, unchanged |
| 4 | Capacity: open streams `< CRSW_MAX_STREAMS` | `429` | Last, so an unauthenticated or unauthorised caller can never observe the cap's state. Counted and admitted in one critical section — the same count-then-insert race `Store.AddCapped` closes (FR-034e) |

**What is deliberately not required:**

- **The per-session bearer token** (FR-034a). That token exists because every API
  caller authenticates as the same shared secret and needs a second factor naming
  the session. A browser identity is verified per person, and the ownership check in
  step 3 is what distinguishes sessions for it — requiring the token would add no
  check while forcing a credential into the one place it must never go:
- **Any credential in the URL** (FR-034). The URL carries the session ID and
  nothing else. URLs are logged by every intermediary — the edge, the tunnel, a
  browser's history — and a session ID alone opens nothing: presenting it still
  requires the validated identity and passes the ownership check. The credential is
  the ambient cookie, carried in headers by the browser itself.

Declining the token is also what makes FR-034c load-bearing: a header credential
would force a preflight on cross-origin use; a cookie does not. The daemon therefore
emits **no CORS header on any route**, asserted by a sweeping test, so same-origin
policy — the only thing stopping a hostile page from reading this stream with the
operator's own riding cookie — is never opted out of
([dashboard.md](./dashboard.md), research [D8](../research.md)).

### The audit record is written at open (FR-016a)

Milestone 1 emits its one record after the handler returns. For a connection lasting
hours, that is a daemon that can die mid-stream leaving no trace that session output
was being read — which is why FR-016a exists. The stream's record — action
`stream.open`, the session ID from the daemon's own record, the authorisation
decision — is emitted **when the decision is made**, for refusals as well as
admissions.

**The total is one record per stream request, and there is no close record.** SC-008
requires exactly one record per request; FR-016a leaves the close-record choice to
this milestone, and this is the choice, stated. The close adds no authorisation fact
the open did not carry, and a second record would make the browser door the one
place "exactly one per request" is false. The record carries no pane content, ever
(FR-035).

---

## Framing (research [D4](../research.md))

One SSE event per changed capture. The `data:` field is a **JSON-encoded string
holding the entire screen**:

```
data: "$ go test ./...\r\nok  \tinternal/access\t0.31s\r\n…the whole screen…"

```

Why JSON, when SSE already frames: SSE's wire format is line-oriented — a raw
newline inside `data:` starts a new field, multi-line payloads are split and
rejoined, and a payload containing a lone `\r` is silently corrupted in the rejoin.
A screen is inherently multi-line. Encoding it as one JSON string makes the
payload's framing independent of its content: one `json.Marshal` on the server, one
`JSON.parse` on the client, and no byte the session prints can influence the
framing.

The client does exactly one thing with the parsed value:

```js
pane.textContent = JSON.parse(event.data);
```

**Assignment, not append.** The screen replaces the screen (FR-031a): what is being
watched is a full-screen program that repaints in place, so successive captures are
redraws — diffing them into appended lines would manufacture spurious output from
every cursor move and progress spinner, and the spec names this the likeliest place
for the milestone to lose days. Assigning `textContent` is also the whole XSS
answer: the parsed value is a string, and a string assigned to `textContent` has no
path to becoming markup — closed by construction, not by sanitising (FR-028,
SC-004). Never `innerHTML`, never an htmx swap for this payload
(`docs/components.md`'s corrected pane rule; note its illustrative JS still shows an
*append* and is amended alongside this milestone — see the spec's "Documents this
milestone must amend").

Text is stripped of terminal escapes server-side before it is marshalled (FR-029),
by the same `tmuxctl.Strip` path milestone 1's `/output` route uses — one stripper,
not a second that agrees today.

**Scroll position is never moved by an update** (FR-032). A replaced screen has no
"bottom" to follow; the container simply repaints. If the operator has scrolled
within the pane, the viewport stays where they put it.

### The terminal event

When the watched session ends — destroyed, reaped, expired — or ceases to be the
viewer's, the stream sends one final event before closing:

```
event: end
data: "ended"

```

The client shows a plain text state ("session ended") and calls `close()` on the
`EventSource` (FR-033) — without the explicit close, `EventSource` auto-reconnects
forever into a uniform 404, which is a polite client turned into a scanner. A
connection that drops for any other reason (network, daemon restart) is left to
`EventSource`'s default reconnect, which re-runs the full authorisation at open —
re-connection is a new request, never a resumed privilege.

---

## Cadence and suppression (research [D5](../research.md))

- The daemon captures each **watched session** once per second — `capture-pane` is a
  poll by nature; tmux offers no change notification. One second sits under the lag
  a human notices and bounds the work to one exec per watched session per second.
- Captures are **per session, not per stream**: two tabs watching one session share
  one capture loop and one exec (the cap counts connections; the cost model counts
  sessions). A newly opened stream receives the latest screen immediately rather
  than waiting out the first interval.
- **An unchanged screen is not re-sent.** A session idling at a prompt would
  otherwise push an identical screen to every open tab every second, forever —
  burning the host for zero information.
- **Every tick writes exactly one thing per stream**: the event if the screen
  changed, otherwise an SSE comment line (`:`) as a heartbeat. The heartbeat is not
  a re-send — a comment is not an event and carries no data — and it exists for two
  failures the suppression rule would otherwise create. First, a browser that
  vanishes without closing the connection (an edge case the spec names) would sit on
  a cap slot indefinitely if a quiet screen meant zero writes: the heartbeat is what
  turns a dead peer into a write error the daemon notices. Second, an idle proxy
  timeout at the edge would sever a quiet stream that is working perfectly. The
  one-write-per-tick invariant is also trivially testable.

---

## The write deadline (research [D3](../research.md), verified)

Milestone 1's server carries a 30-second `WriteTimeout`, and its `server.go` already
notes that SSE cannot live under it. The answer is per-response, standard library:

```go
rc := http.NewResponseController(w)
rc.SetWriteDeadline(time.Time{}) // this response is deliberately unbounded
```

on the stream handler only. The alternative — `WriteTimeout: 0` on the server —
removes the timeout from all six milestone 1 routes to serve one new one; the
rejected-alternatives table in the plan records it so it is not rediscovered. The
six routes keep their deadline; the stream lifts its own.

An unbounded deadline does not mean an unbounded connection: any failed write —
event or heartbeat — ends the stream and releases its slot, and a cleanly closed
browser cancels the request context, which ends the stream the same way. Flush
follows every write; an SSE event sitting in a buffer is an update the operator is
not seeing.

---

## Lifecycle: re-evaluation, teardown, shutdown

**Authorisation is re-evaluated, not established** (FR-034b). Every tick, before
capturing: does the session still exist, and is it still the viewer's? A destroyed,
reaped, or expired session — or one that stops belonging to the viewer — stops
receiving output within **one polling interval** (SC-015): terminal event, then
close. The check runs against the daemon's own records; there is no cached "was
authorised at open" that a teardown then races.

**Watching is not driving** (FR-034f). The stream's reads — the open's resolution
and every tick's re-evaluation and capture — go through a read path that does
**not** advance the session's idle clock. Milestone 1 advances that clock inside
`Manager.Resolve`, whose comment claims it is the only place a request becomes a
session; this milestone makes that claim false and amends the comment (research,
Open items), because the failure mode of not doing so is a later iteration
"fixing" the stream path by adding the touch back — at which point a forgotten
browser tab holds an unsandboxed shell open forever, the exact bound Principle VI
calls non-negotiable. US2 scenario 7 is the observable contract: a session watched
continuously past its idle timeout is reaped on schedule, and the watcher is told.

**Teardown closes streams.** Destroying a session ends any stream tailing it —
`docs/auth-and-sessions.md`'s teardown checklist has carried that box since before
the feature existed — and drops the shared last-screen buffer. Discovery via the
next tick satisfies SC-015's bound; teardown does not block on watchers.

**Shutdown is not delayed by streams** (FR-034f). Milestone 1's shutdown drains
in-flight requests for up to 10 seconds, then tears every session down. A stream is
an in-flight request that never finishes, so streams are closed **before** the
drain begins; the drain budget then belongs to the six short routes it was sized
for. Streams do not get a farewell event at shutdown as a guarantee — the daemon is
racing its own service manager — and the client's reconnect handling covers the
distinction: reconnecting to a live daemon re-authorises, reconnecting to a dead
one fails visibly.

---

## Concurrency cap (FR-034e)

`CRSW_MAX_STREAMS` (default 10 — rationale in
[data-model.md § Config](../data-model.md#config--additions)) bounds open streams
globally. Admission is one critical section with the count; release happens on
every close path, including panic-unwinding ones. Past the cap the open is refused
with `429` — the same status milestone 1 gives the session cap and the create rate,
because it is the same statement: nothing about the request is wrong, there is no
room. Each stream is a long-lived connection doing periodic exec work against the
host; unbounded streams are the local denial of service the session cap exists to
prevent (Principle VI).

---

## Contract tests

Each maps to a requirement or success criterion and must fail if the behaviour is
removed (SC-017). Stream tests drive the handler with a fake controller and an
injected clock — no real tmux, no sleeps.

| Test | Asserts | Covers |
|---|---|---|
| Open with validated identity + owned session | Stream opens; first event is the current screen, immediately | FR-031, FR-034 |
| Open with a valid **service-token** assertion | Refused at layer 1 | FR-013c |
| Open with no/invalid assertion | Uniform layer-1 refusal; no session existence disclosed | FR-034, FR-010 |
| Open for an unknown session vs a synthetic second owner's session | Byte-identical uniform 404 | FR-037b, SC-016 |
| `Sec-Fetch-Site: cross-site` (and `none`) on open | Refused; `same-origin` and absent admitted | FR-034d |
| Header sweep on the stream response | Security headers, `no-store`, zero `Access-Control-Allow-*` | FR-034c |
| URL of an open stream | Contains the session ID and no credential | FR-034 |
| Screen changes between ticks | One event per change; `data:` is one JSON string of the whole screen | FR-031a, D4 |
| Screen unchanged between ticks | No event; one comment heartbeat per tick | D5 |
| Payload containing `\n`, lone `\r`, `<script>`, ANSI escapes | Framing intact; escapes stripped server-side; client-side value assigned to `textContent` renders as text, zero execution | FR-028, FR-029, SC-004 |
| Session destroyed / reaped while watched | `end` event within one interval, then close | FR-033, FR-034b, SC-015 |
| Session watched continuously past the idle timeout (injected clock) | `LastActivity` unmoved by the stream; reaper fires on schedule; watcher told | FR-034f, US2-7 |
| Two tabs, one session | Both receive updates; one capture per tick, not two | D5 |
| Opens past `CRSW_MAX_STREAMS` | `429`; earlier streams unaffected; a close frees a slot | FR-034e |
| Two opens racing the last slot | Exactly one admitted | FR-034e |
| Write failure / client disconnect | Stream ends; slot released; no goroutine leaked | edge case |
| Shutdown with streams open | Streams closed before the drain; shutdown completes within its budget | FR-034f |
| `stream.open` audit record | Emitted at open with the decision, for refusals too; **exactly one** record per stream request; no pane content in any record | FR-016a, FR-035, SC-008 |
