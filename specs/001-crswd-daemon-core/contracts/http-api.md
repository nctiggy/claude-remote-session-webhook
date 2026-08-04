# Contract: crswd HTTP API (Milestone 1)

**Feature**: [../spec.md](../spec.md) · **Plan**: [../plan.md](../plan.md)

The daemon's only external surface. Six operations, no others (FR-006) — in particular
**no unauthenticated health, status, metrics, or version route**. Adding one later is a
constitution-level change, not a convenience.

Base URL is loopback-only: `http://127.0.0.1:8765` by default, reached from outside the
host solely through the Cloudflare Tunnel.

---

## Authentication — required on every route, no exemptions

Two layers must both pass. Layer 1 (Cloudflare Access) is milestone 2 and is not
implemented here.

### Layer 2 — request signature (every request)

| Header | Value |
|---|---|
| `X-CRSW-Timestamp` | Unix seconds, as a decimal string |
| `X-CRSW-Signature` | `sha256=` + lowercase hex HMAC-SHA256 |

The signed payload is `METHOD + "\n" + PATH + "\n" + timestamp + "." + rawBody`, keyed
with `CRSW_SHARED_SECRET`. `PATH` is the escaped path from the request line, without the
query string. For a request with no body, the raw body is the empty string — so a
`GET /sessions` payload is `"GET\n/sessions\n1785706480."`.

```
mac = HMAC_SHA256(secret, METHOD + "\n" + PATH + "\n" + "1785706480." + rawBody)
X-CRSW-Signature: sha256=<hex(mac)>
```

**Why the request line is in the payload.** Over `timestamp + "." + rawBody` alone,
every empty-body request at one instant signs identically. One signed `GET /sessions`
was therefore a valid `DELETE /sessions/{id}` in the same second, with only the replay
cache in the way — and only if the original request actually arrived. It also made the
daemon refuse itself: a client reading twice inside one second sent the same signature
twice and got a 401 on the second.

The bearer token is deliberately **not** in the payload. It is layer 3, a separate
credential with a separate lifetime; binding it into the layer-2 signature would
collapse two independent checks into one. A consequence worth knowing when writing a
client: two requests differing *only* by bearer token, at the same instant, are one
signature and the second is refused as a replay.

Verification order, all of which must pass:

1. Timestamp parses, and `|now - ts| <= 300s` — **both directions** (FR-008)
2. Body read through a size limit; the signature covers the bytes as received
3. `hmac.Equal` against the recomputed value — never `==` (FR-009)
4. Signature not already observed; observing and recording are atomic (FR-010)

### Layer 3 — per-session bearer token (session-scoped routes)

| Header | Value |
|---|---|
| `Authorization` | `Bearer <token>` |

Required on every route carrying an `{id}`. Compared against the stored SHA-256 with a
constant-time compare. A valid signature alone is **not** sufficient (FR-014).

### Failure responses are uniform

Any layer-2 failure — missing header, malformed timestamp, skew, bad signature, replay —
returns the identical response. The specific reason is recorded server-side only
(FR-011, SC-001).

```http
HTTP/1.1 401 Unauthorized
Content-Type: application/json

{"error":"unauthorized"}
```

Any ownership failure, unknown ID, or wrong bearer token returns the identical
**404**, so a caller cannot distinguish "not yours" from "does not exist" (FR-033,
SC-005). Byte-identical body, same headers.

```http
HTTP/1.1 404 Not Found
Content-Type: application/json

{"error":"not found"}
```

---

## Common rules

- **Request bodies** decode into a fixed struct with `DisallowUnknownFields`, read
  through `http.MaxBytesReader` at `CRSW_MAX_BODY_BYTES` (default 64 KiB). An unknown
  field or an oversize body is `400` (FR-026).
- **Every response** is `application/json`.
- **Every request** produces exactly one audit record, allowed or denied (FR-041).
- Success bodies never contain the shared secret; only `POST /sessions` ever contains a
  token, and only once.

### Status codes

| Code | When |
|---|---|
| `200` | Read or delete succeeded |
| `201` | Session created |
| `202` | Prompt accepted for delivery |
| `400` | Malformed body, unknown field, failed field validation, oversize body |
| `401` | Any layer-2 authentication failure (uniform) |
| `404` | Unknown session, another owner's session, wrong bearer token (uniform) |
| `409` | Teardown could not be verified — the session may still be alive |
| `429` | Concurrent-session cap reached, or create rate limit exceeded |
| `500` | Internal failure; body carries no detail |

---

## `POST /sessions` — create

Creates a tmux session, sends the Claude start command into it, and returns the only
copy of the bearer token.

**Request**

```json
{
  "name": "refactor-auth",
  "work_dir": "/home/operator/code/myrepo"
}
```

| Field | Rules |
|---|---|
| `name` | Required. `^[a-zA-Z0-9-]{1,64}$`. `:` and `.` rejected explicitly (FR-027) |
| `work_dir` | Required. Cleaned, symlink-resolved, must be an existing directory under an approved root (FR-028). Not created if absent |

**Response `201`**

```json
{
  "id": "9f2c1ab47e0d4f6a8b3c5d7e1f0a2b4c",
  "name": "refactor-auth",
  "work_dir": "/home/operator/code/myrepo",
  "state": "starting",
  "created_at": "2026-08-02T21:36:58Z",
  "expires_at": "2026-08-03T21:36:58Z",
  "token": "3b7f...64 hex chars..."
}
```

`token` appears in this response and nowhere else, ever (FR-013). `expires_at` is both
the session's absolute deadline and the token's — they are the same instant by
construction (FR-015).

**Failures**: `400` invalid name / path outside an approved root / unknown field ·
`429` cap or rate limit · `500` tmux failed to start the session.

---

## `GET /sessions` — list

Returns only sessions owned by the caller (FR-032). Never includes tokens or hashes.

**Response `200`**

```json
{
  "sessions": [
    {
      "id": "9f2c1ab47e0d4f6a8b3c5d7e1f0a2b4c",
      "name": "refactor-auth",
      "work_dir": "/home/operator/code/myrepo",
      "state": "running",
      "created_at": "2026-08-02T21:36:58Z",
      "expires_at": "2026-08-03T21:36:58Z",
      "last_activity": "2026-08-02T22:10:03Z",
      "adopted": false
    }
  ]
}
```

No bearer token is required here — the route is caller-scoped, not session-scoped — but
the layer-2 signature is (FR-007).

---

## `GET /sessions/{id}` — detail

Requires the session's bearer token. Same object shape as one list entry.

**Failures**: `404` for unknown, not-owned, or wrong-token — all identical.

---

## `DELETE /sessions/{id}` — destroy

Kills the tmux session, **verifies it is gone**, then clears the record, the token hash,
and any buffered output (FR-019, FR-020).

**Response `200`**

```json
{"id":"9f2c1ab47e0d4f6a8b3c5d7e1f0a2b4c","destroyed":true}
```

**Response `409`** — the kill was issued but the session is still present. The record is
**not** removed, the failure is audited prominently, and the caller is told the truth
rather than a comfortable lie:

```json
{"error":"teardown could not be verified"}
```

This is the one place where a non-uniform error body is correct: the caller owns this
session (already proven by the token), so nothing is disclosed, and an operator needs to
know a live unsandboxed shell may have survived.

---

## `POST /sessions/{id}/prompt` — send a prompt

Delivers text into the session verbatim. Implemented with `load-buffer` from stdin plus
`paste-buffer -d`, never `send-keys -l` — see [research D4](../research.md); a prompt
that is exactly `;` or ends in `;` is silently mangled by `send-keys`.

**Request**

```json
{"text": "run the tests; then summarise failures"}
```

| Field | Rules |
|---|---|
| `text` | Required, non-empty, within the body limit. **Not otherwise validated or escaped** — it is data, delivered byte-for-byte (FR-030, SC-012) |

**Response `202`**

```json
{"id":"9f2c1ab47e0d4f6a8b3c5d7e1f0a2b4c","delivered":true}
```

The prompt text never appears in any audit record or log line (FR-042).

---

## `GET /sessions/{id}/output` — read the pane

**Response `200`**

```json
{
  "id": "9f2c1ab47e0d4f6a8b3c5d7e1f0a2b4c",
  "captured_at": "2026-08-02T22:11:44Z",
  "text": "$ go test ./...\nok  \tinternal/auth\t0.012s\n"
}
```

`text` is plain text: captured without `-e`, then run through a defensive control-character
stripper (FR-031, [research D5](../research.md)). It is returned as a JSON string and is
**never** interpolated into HTML — milestone 2 must render it as a text node.

Pane content is secret (`docs/security.md` §3): it is never logged, never audited, never
placed in an error message.

---

## Contract tests this implies

Each maps to a success criterion and must fail if the behaviour is removed.

| Test | Asserts | Covers |
|---|---|---|
| Unauthenticated sweep over all 6 routes | Every route returns the uniform `401`; zero exempt | SC-001 |
| Tampered body | `401`, identical to unsigned | SC-001 |
| Skew both directions (`-301s`, `+301s`) | `401` both | SC-003 |
| Replay of a valid request | Second attempt `401`; exactly one session exists | SC-002 |
| Concurrent replay | Exactly one succeeds | edge case |
| Cross-owner on every `{id}` route | `404`, byte-identical to unknown-ID | SC-005 |
| Valid signature + wrong bearer token | `404`, byte-identical | FR-014 |
| Unknown JSON field / oversize body | `400`, no session created | FR-026 |
| `work_dir` traversal, absolute escape, symlink escape | `400`, no session created | FR-028 |
| `name` containing `:` or `.` | `400` | FR-027 |
| Prompt `";"`, `"foo;"`, `"$(id)"`, embedded newline | Delivered byte-for-byte; nothing executed | SC-012 |
| Destroy when the fake reports survival | `409`, record retained, audited | FR-019 |
| Token presented after `expires_at` | `404` | FR-015 |
| Full audit capture, all routes | No prompt, pane, token, or secret; one record per request | SC-013 |
