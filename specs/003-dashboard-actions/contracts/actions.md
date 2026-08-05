# Contract: Dashboard action routes

**Feature**: [../spec.md](../spec.md) · **Plan**: [../plan.md](../plan.md) · **Research**: [../research.md](../research.md)

Four mutating routes on the browser door, and the token that authorises them. Written to be
executed literally: every header name, path, body, status code and audit action below is the exact
string to use.

---

## Route table

All four are `POST`. All four live under `/dashboard/` so the milestone 1 API surface is untouched
(FR-005) and a `grep` for the prefix finds every browser-initiated change.

| Method | Path | Action | Audit action |
|---|---|---|---|
| `POST` | `/dashboard/sessions` | Create | `dashboard.create` |
| `POST` | `/dashboard/sessions/{id}/destroy` | Destroy | `dashboard.destroy` |
| `POST` | `/dashboard/sessions/{id}/rename` | Rename | `dashboard.rename` |
| `POST` | `/dashboard/sessions/{id}/compact` | Compact | `dashboard.compact` |

`{id}` is 32 lowercase hex characters. Anything else is not a route match and takes the existing
unknown-route path.

**No route accepts any other method.** A `GET` to any of these is an unknown route, answered
exactly as any other unknown route is — never a `405`, never an `Allow` header.

---

## The gate every one of them passes first

In this order, in the browser-door middleware, **before any handler runs and before any state
changes** (FR-003):

1. **Layer 1 — Cloudflare Access.** The existing `verifyBrowser`. Failure → the existing browser
   refusal. This is what makes FR-008 structural: an ended Access session never reaches step 3.
2. **Same-origin.** The existing `crossSite(r)` from `stream.go`: `Sec-Fetch-Site` must be exactly
   `same-origin`. `same-site`, `none`, absent, or any other spelling → refuse
   ([R1](../research.md#r1--the-same-origin-half-of-d1-is-already-built)).
3. **Page token.** Form field `crsw_page_token`, validated per
   [data-model.md](../data-model.md#pagetoken).

Steps 2 and 3 are **independently load-bearing** (FR-002c). Disabling either alone must cause a
test to fail. AR-005: tests satisfy these checks, they never disable them.

### The uniform refusal — one body, referenced everywhere below

Any failure of step 2 or step 3 returns **exactly** this. Byte-identical across every cause: wrong
origin, missing token, malformed token, expired token, wrong identity's token.

```http
HTTP/1.1 403 Forbidden
Content-Type: text/html; charset=utf-8
X-Content-Type-Options: nosniff

<!doctype html><title>refused</title><p>This action was refused.</p>
```

The specific cause goes only into the audit record's `Reason` (FR-004, FR-026). A caller cannot
tell which check failed, or that there is more than one.

**Why `403` and not the browser door's `401`.** A `401` says "authenticate"; this caller already
did, successfully. Reusing it would tell an attacker their Access credential was the problem when
it was not, and would invite a browser to re-prompt for a login that would not help. The two are
different failures and the audit trail must be able to tell them apart — `access.reject` versus
`dashboard.reject` ([R5](../research.md#r5--audit-vocabulary)).

### The uniform not-found

Unknown identifier, another owner's session, or an identifier that no longer exists — all
byte-identical (FR-017, SC-009):

```http
HTTP/1.1 404 Not Found
Content-Type: text/html; charset=utf-8
X-Content-Type-Options: nosniff

<!doctype html><title>not found</title><p>No such session.</p>
```

---

## `POST /dashboard/sessions` — create

**Request** (`Content-Type: application/x-www-form-urlencoded`)

| Field | Rules |
|---|---|
| `name` | Required. `^[a-zA-Z0-9-]{1,64}$` |
| `work_dir` | Required. Cleaned, symlink-resolved, must be an existing directory under an approved root |
| `crsw_page_token` | Required. See the gate above |

**Success `200`** — an HTML fragment of the new card, appended to the fleet.

**The token is not in it** (FR-013). The bearer token minted by the create is discarded by the
handler without ever being written to a response, a template, or a log. A session created from the
browser is drivable from the browser; driving it from the API needs a token the operator does not
have, and that is the accepted consequence of not handing credentials to a page.

**Failures**

| Cause | Status | Body |
|---|---|---|
| Invalid `name` | `400` | Fragment naming the field, nothing about the filesystem |
| `work_dir` outside roots / traversal / symlink escape / not a directory | `400` | Fragment saying the directory is not permitted — **never** whether it exists |
| Concurrent cap or rate limit | `429` | Fragment saying the limit was reached |
| tmux failed to start | `500` | Fragment saying the session could not be started |

The `work_dir` failures are one message on purpose: distinguishing "does not exist" from "not
permitted" is a filesystem oracle.

**Worked example**

Placeholders below are written as `<...>` rather than as realistic-looking values, deliberately: a
document full of high-entropy strings trains both secret scanners and readers to ignore them.

```http
POST /dashboard/sessions HTTP/1.1
Host: crswd.craigcloud.io
Content-Type: application/x-www-form-urlencoded
Sec-Fetch-Site: same-origin
Cf-Access-Jwt-Assertion: <the Access JWT the tunnel injects>

name=refactor-auth&work_dir=%2Fhome%2Foperator%2Fcode%2Fmyrepo&crsw_page_token=<expiry>.<64 hex>
```

```http
HTTP/1.1 200 OK
Content-Type: text/html; charset=utf-8
X-Content-Type-Options: nosniff

<article class="card" id="card-9f2c1ab47e0d4f6a8b3c5d7e1f0a2b4c">...</article>
```

---

## `POST /dashboard/sessions/{id}/destroy` — destroy

**Request**

| Field | Rules |
|---|---|
| `crsw_page_token` | Required |
| `confirm` | Required, must be exactly `yes`. FR-029: a deliberate confirming step |

A destroy without `confirm=yes` is `400`, and **nothing is torn down**.

**Success `200`** — a fragment replacing the card with a removal marker. The record and credential
hash are cleared, as the API path already does.

**Unverified teardown `409`** — the kill was issued and the session is still present. The record is
**retained** and the operator is told the truth (FR-010, AR-004):

```http
HTTP/1.1 409 Conflict
Content-Type: text/html; charset=utf-8
X-Content-Type-Options: nosniff

<p class="card-outcome">Teardown could not be verified. This session may still be running on the host.</p>
```

This is the one non-uniform failure body on these routes, for the same reason milestone 1 allows it
on the API: the caller has already proved they own this session, so nothing is disclosed — and an
operator needs to know a live unsandboxed shell may have survived.

There is **no force path**. AR-004.

---

## `POST /dashboard/sessions/{id}/rename` — rename

**Request**

| Field | Rules |
|---|---|
| `name` | Required. `^[a-zA-Z0-9-]{1,64}$` |
| `crsw_page_token` | Required |

**Success `200`** — a fragment of the re-rendered card carrying the new name.

Changes **only** the record's display name. The tmux session name is derived from the identifier
and is not touched (FR-015). Names are not required to be unique; the daemon never addresses a
session by name.

**Failure `400`** — invalid name. The existing name is unchanged.

---

## `POST /dashboard/sessions/{id}/compact` — compact

**Request**

| Field | Rules |
|---|---|
| `crsw_page_token` | Required |

Delivers the literal 8 bytes `/compact` followed by a newline into the session, using the same
`load-buffer` + `paste-buffer -d` path prompts use — never `send-keys`.

**Success `202`** — accepted for delivery. The fragment must say **delivered**, not compacted:

```http
HTTP/1.1 202 Accepted
Content-Type: text/html; charset=utf-8
X-Content-Type-Options: nosniff

<p class="card-outcome">Compact delivered. The session decides what to do with it.</p>
```

FR-016a: the daemon cannot see what the assistant is carrying, so it must not claim the compaction
happened. It reports only what it observed — that the bytes were delivered.

The delivered text is never audited or logged (FR-016b, AR-007), exactly as prompt text is not.

Compact touches `LastActivity`: it is activity, and defers the idle deadline as a prompt does.

---

## Contract tests

Each row mirrors a line of the spec's Verification Map. The **must fail when** column is the
load-bearing half — a test that cannot fail is not verification.

| Test | Asserts | Must fail when | Covers |
|---|---|---|---|
| Foreign `Sec-Fetch-Site`, all 4 routes | The uniform `403`; no state change | The `crossSite` call is removed from the action path | FR-002a, SC-001 |
| Absent `crsw_page_token`, all 4 routes | The uniform `403`; no state change | The token check is removed | FR-002b, SC-001 |
| Each check disabled separately | The other still refuses | Either half is load-bearing only in combination | FR-002c, SC-001a |
| Token minted for identity A, sent as B | The uniform `403` | The identity is dropped from the MAC input | FR-007 |
| Token with `expiry` in the past | The uniform `403` | The expiry comparison is removed or inverted | R3 |
| Token with `expiry` edited forward, original MAC | The uniform `403` | `expiry` is excluded from the MAC input | data-model |
| Byte-compare all five refusal causes | Identical status, headers, body, `Content-Length` | Any cause produces a distinguishable response | FR-004 |
| Every route with another owner's id, byte-compared against an unknown id | Identical | Any action distinguishes them | FR-017, SC-009 |
| `GET` on each of the four paths | The unknown-route response; never `405`, never `Allow` | A method-not-allowed path is added | FR-033 |
| Destroy without `confirm=yes` | `400`; the session is still alive | The confirm check is removed | FR-029 |
| Destroy against a fake reporting survival | `409`; record retained; audited | The verification is skipped or its result ignored | FR-010, AR-004 |
| Browser create response and rendered page searched for the bearer token | Absent | The token is passed to the template | FR-013 |
| `work_dir` traversal, absolute escape, symlink escape, non-directory | `400`; nothing created; one message for all four | The messages diverge | FR-012 |
| Rename, then every identifier-based operation | Unchanged behaviour; tmux name unchanged | Rename touches the tmux name | FR-015, SC-012 |
| Compact response text | Says delivered, not compacted | It claims the compaction succeeded | FR-016a |
| Audit capture over every route, allowed and denied | Exactly one record each; `dashboard.*` distinct from `session.*` | A request produces zero or two records, or reuses an API action name | FR-023, FR-024 |
| Same capture searched for secret, tokens, page token, prompt text, `/compact`, pane content | None present | Any is recorded | FR-025, AR-007 |
| Anchors in the card template | Exactly one | A control is added inside the anchor | FR-027 |
| Every served byte searched for `CRSW_SHARED_SECRET` and `pageKey` | Absent | Either reaches the browser | FR-006 |
