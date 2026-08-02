# Phase 1 Data Model: crswd Daemon Core

**Feature**: [spec.md](./spec.md) · **Plan**: [plan.md](./plan.md) · **Research**: [research.md](./research.md)

Everything here lives in memory for the process lifetime. There is no schema, no
migration, and no file on disk — restart recovery comes from tmux (FR-021), not from
storage. Field types are Go types because the model *is* the code in this milestone.

---

## Session

The central record. One per live Claude Code session.

| Field | Type | Source | Invariant |
|---|---|---|---|
| `ID` | `string` | `crypto/rand`, 16 bytes → 32 lowercase hex | `^[a-f0-9]{32}$`, ≥128 bits, never sequential (FR-016). Immutable |
| `Owner` | `CallerID` | The credential used to create it, server-side | Never from a request field (FR-012, FR-017). Immutable |
| `Name` | `string` | Caller-supplied | `^[a-zA-Z0-9-]{1,64}$`; `:` and `.` rejected explicitly (FR-027). **Display label only — never part of a tmux target** |
| `WorkDir` | `string` | Caller-supplied, then resolved | Canonical absolute path, symlinks resolved, verified under an approved root (FR-028). Immutable |
| `TokenHash` | `[32]byte` | SHA-256 of the bearer token | The plaintext is never stored (FR-013) |
| `CreatedAt` | `time.Time` | Injected clock, or tmux `session_created` when adopted | Origin of the absolute deadline (FR-024) |
| `LastActivity` | `time.Time` | Injected clock | Updated by every request that touches the session; reset at adoption (FR-024) |
| `State` | `State` | See state machine below | |
| `Adopted` | `bool` | Set by reconciliation | Audit-visible; does not change any rule |

### Derived, not stored

Storing these would let them drift from the fields they derive from.

| Derivation | Value | Why derived |
|---|---|---|
| tmux session name | `"crswd-" + ID` | Built from the ID alone, so a caller's `Name` can never influence a target (FR-034) |
| Session target | `"=crswd-" + ID` | For `has-session`, `kill-session` (research D2) |
| Pane target | `"=crswd-" + ID + ":"` | For `send-keys`, `capture-pane`, `paste-buffer`, `set-option` — **different syntax** (D2) |
| Idle deadline | `LastActivity + 60m` | One clock, one rule |
| Absolute deadline | `CreatedAt + 24h` | No renewal (FR-038) |
| Token expiry | `CreatedAt + 24h` | Equal to the absolute deadline by construction, so the two cannot diverge (FR-015, SC-010) |

**`TokenExpiry` is deliberately not a field.** The whole point of the operator's Q2
decision is that a credential cannot outlive or under-live its session. Making it a
separate stored value reintroduces the possibility of them disagreeing.

### State machine

```
                  Create                 first successful prompt/output
        (none) ─────────────► starting ──────────────────────────────► running
                                 │                                        │
                                 │ tmux reports the session gone          │
                                 ▼                                        ▼
                              ┌──────────────────────────────────────────────┐
                              │                   dead                       │
                              └──────────────────────────────────────────────┘
                                 ▲                    ▲                  ▲
                    Destroy ─────┘   reaper (idle/absolute)   shutdown ───┘

        (tmux, no record) ──── Adopt ────► running          [if past 24h → destroyed, never adopted]
```

| State | Meaning | Accepts prompt/output? |
|---|---|---|
| `starting` | tmux session created, Claude command sent, nothing confirmed yet | Yes |
| `running` | Confirmed present at last check | Yes |
| `dead` | Confirmed gone, or teardown verified | No — 404 |

There is no `needs-auth` state in this milestone; it arrives with the device-code relay
in milestone 4. Recording it now would be inventing a requirement.

**Transition rules**

- `Destroy` is only reported successful after `has-session` confirms absence (FR-019).
  A kill that leaves the session present keeps the record in its prior state and returns
  an error — it does **not** silently become `dead`.
- A record whose tmux session has vanished on its own transitions to `dead` on the next
  observation, and its endpoints then behave exactly like an unknown ID (FR-033).
- Reconciliation destroys rather than adopts anything already past
  `session_created + 24h` (FR-025).

---

## CallerID

```go
type CallerID string   // milestone 1: exactly one value, from the shared secret
```

The type exists so milestone 2's Access identity is a new *source*, not a new *shape*.
Handlers already compare `session.Owner != caller.ID`; nothing about them changes when a
second identity source appears. Tests use a synthetic second `CallerID` to exercise the
cross-owner path (FR-032, SC-005) even though production has one.

---

## SessionToken

Never a stored entity — it exists as plaintext only inside the create response.

| Property | Value |
|---|---|
| Generation | `crypto/rand`, 32 bytes → 64 lowercase hex (never `math/rand`) |
| Transport | Returned once, in the create response body (FR-013) |
| At rest | `sha256.Sum256(token)` on the `Session` record |
| Comparison | `hmac.Equal` over the hashes — constant time, no `==` |
| Lifetime | Equal to the session's absolute lifetime (FR-015) |
| On destroy | Hash zeroed with the record (FR-020) |

---

## ApprovedRoot

| Field | Type | Notes |
|---|---|---|
| `Path` | `string` | Canonical absolute, symlinks already resolved at startup |
| `IsDefault` | `bool` | True when `CRSW_ALLOWED_ROOTS` was unset and `~/code` is in force |

Resolved once at startup, never per request — a root that is itself a symlink must not be
re-resolved on the hot path where a swap could race the check. `IsDefault` drives the
loud startup warning (FR-004) and appears in the startup audit record.

**Containment check** (the whole point of the type):

1. `filepath.Clean` the caller's path
2. `filepath.EvalSymlinks` — fails closed if the path does not exist
3. Confirm the result equals a root, or is under one **at a path-separator boundary**

Step 3's boundary check is why this is a type and not a `strings.HasPrefix` call:
`/home/u/codeEVIL` has `/home/u/code` as a string prefix but is not under it.

---

## SeenSignature (replay cache)

| Field | Type | Notes |
|---|---|---|
| key | `string` | The full `sha256=...` signature |
| value | `time.Time` | Observation time, on the injected clock |

TTL is `2 × 300s = 600s`, matching the replay-cache row in `docs/auth-and-sessions.md`.
Expired entries are swept opportunistically on write; there is no separate goroutine.

`Observe(sig) bool` checks and records **inside one critical section**. Splitting them
into `Seen()` then `Record()` is a check-then-act race, and the spec's edge case "the
same signed request sent twice, concurrently" is exactly that race — two replays would
both win.

---

## AuditRecord

Emitted as one JSON object per request on stdout via `log/slog` (FR-041).

| Field | Type | Example |
|---|---|---|
| `time` | RFC3339 | `2026-08-02T21:36:58Z` |
| `action` | `string` | `session.create`, `session.prompt`, `session.destroy`, `auth.reject`, `reaper.destroy`, `startup.adopt` |
| `caller` | `string` | `operator` — or `unknown` on a rejected request |
| `session_id` | `string` | The 32-hex ID, or absent |
| `decision` | `string` | `allow` \| `deny` |
| `reason` | `string` | Server-side only; the *client* always sees a uniform message (FR-011) |
| `remote` | `string` | `127.0.0.1:54321` |

**Forbidden in every record, asserted by test (FR-042, SC-013)**: prompt text, pane
content, the bearer token or its hash, the shared secret, any full request body.

The safest implementation is a fixed struct with explicit fields — no `map[string]any`
passthrough, no `slog.Any("req", r)`. A record that cannot carry arbitrary data cannot
leak arbitrary data, which turns FR-042 from a review item into a compile-time shape.

---

## Config

Loaded once at startup from the environment (FR-001). Every failure is fatal *before*
the listener binds.

| Env | Type | Required | Default | Failure |
|---|---|---|---|---|
| `CRSW_SHARED_SECRET` | `[]byte` | **yes** | — | Exit non-zero if unset or `len < 32` (FR-002) |
| `CRSW_ALLOWED_ROOTS` | `[]string` (`:`-separated) | no | `$HOME/code` | Loud warning when defaulted (FR-004); fatal if a listed root does not exist |
| `CRSW_LISTEN` | `string` | no | `127.0.0.1:8765` | Fatal if the host is not loopback (FR-005) |
| `CRSW_MAX_SESSIONS` | `int` | no | `5` | Fatal if `< 1` |
| `CRSW_CREATE_RATE_PER_MIN` | `int` | no | `6` | Fatal if `< 1` |
| `CRSW_MAX_BODY_BYTES` | `int64` | no | `65536` | Fatal if `< 1` |

`CRSW_SHARED_SECRET` is never logged, never included in an error string, and never
echoed back — not even its length (FR-043).

---

## Relationships

```
Config ──(ApprovedRoot[])──► Manager ──owns──► Session[]  ──1:1──► tmux session "crswd-<ID>"
                                │                  │
                                │                  └──has──► TokenHash  (plaintext returned once)
                                │
                                ├──uses──► Controller (interface; fake in tests)
                                └──uses──► Clock      (interface; fake in tests)

Authenticator ──owns──► SeenSignature[]      Every path ──emits──► AuditRecord
```

**Cardinality that matters**: exactly one tmux session per `Session`, addressed only by
`ID`. There is no path from a caller-supplied string to a tmux target, which is what
makes FR-034 and the isolation rule structural rather than maintained by care.
