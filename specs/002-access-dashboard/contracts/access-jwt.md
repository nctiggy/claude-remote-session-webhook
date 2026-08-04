# Contract: Access Assertion Validation (Layer 1)

**Feature**: [../spec.md](../spec.md) · **Plan**: [../plan.md](../plan.md) ·
**Research**: [../research.md](../research.md) · **Data model**: [../data-model.md](../data-model.md)

What the daemon checks before it believes a browser is the operator, in what order,
and what is refused at each step. This contract governs the browser door only. The
API door never consults it: an API request arrives carrying a valid service-token
assertion — every one of them does, once deployed behind the edge (FR-013a) — and
the daemon ignores that assertion entirely, authenticating the request at layers 2
and 3 exactly as [milestone 1's contract](../../001-crswd-daemon-core/contracts/http-api.md)
documents (FR-013b, research [D2](../research.md)).

Implemented in `internal/access` with the standard library only — the decision and
its justification are research [D1](../research.md) and the plan's Complexity
Tracking. `go.sum` must not appear; `TestQuickstartNoDependencies` fails if it does.

---

## What the daemon reads

| | Value |
|---|---|
| Header | `Cf-Access-Jwt-Assertion` — **the only source** |
| Key set | `https://<team>.cloudflareaccess.com/cdn-cgi/access/certs`, derived from `CRSW_ACCESS_TEAM_DOMAIN` |
| Expected issuer | `https://<team>.cloudflareaccess.com`, derived from the same value |
| Expected audience | `CRSW_ACCESS_AUD`, compared for equality, never parsed |
| Accepted algorithm | `RS256`. Exactly one, as a constant |

The browser also carries a `CF_Authorization` cookie. The daemon never reads it. The
cookie is the browser's credential *to the edge*; the header is the edge's product
*for the daemon*, injected after the edge's own check. Reading both would be two
sources for one identity, free to disagree — and the cookie is precisely the ambient
credential whose ambient-ness makes FR-034c load-bearing
([dashboard.md](./dashboard.md)).

Missing or unusable configuration for any value above is a startup failure with no
listener bound (FR-011, SC-013) — the same rule milestone 1 applies to its shared
secret. The one exception: with the dev bypass **active**, the three
`CRSW_ACCESS_*` values are not demanded (FR-042), because requiring an audience the
bypass then ignores would make local development need a Cloudflare account.

---

## The validation sequence

Every browser request runs the full sequence; there is no per-browser session and no
"already checked" state (data model: VerifiedOperator is derived per request, never
stored). The order is part of the contract, because two of its properties live in
the ordering: the algorithm is decided **before** any cryptography, and the claims
are parsed **after** the signature proves who wrote them.

| # | Check | Refused when | Why here, in this order |
|---|---|---|---|
| 1 | Header present and non-empty | Absent or empty | Nothing to validate. The total header block is already bounded by milestone 1's 16 KiB `MaxHeaderBytes`, so no separate size gate is needed |
| 2 | Exactly three non-empty `.`-separated segments, each valid base64url (`RawURLEncoding`) | Any other shape | Refuse malformed input before any of it is interpreted |
| 3 | JOSE header decodes as JSON; **`alg` is byte-for-byte `RS256`**; no `crit` parameter is present | Any other `alg` — `none`, `HS256`, `RS384`, anything — or any `crit` | See below. `crit` is refused because it announces an extension this validator does not implement, and RFC 7515 says an unimplemented critical extension invalidates the token |
| 4 | `kid` present and resolves to a cached key | No `kid`; unknown `kid` after one refetch (subject to the floor); keys unobtainable | The key-set rules are their own section below. **Fail closed** (FR-009): unobtainable keys refuse the request, never admit it |
| 5 | RS256 signature verifies — SHA-256 over the first two segments **as received**, `rsa.VerifyPKCS1v15` against the resolved key | Verification fails | Over the received bytes, never a re-serialisation: a payload that is re-encoded before verification is a payload the signature no longer covers |
| 6 | Claims decode as JSON | Malformed | Only now. Until step 5 passed, the claims were attacker-authored bytes, and a parser is attack surface — nothing reads them before the signature says who wrote them. (The JOSE header is the unavoidable exception: `alg` and `kid` must be read to verify at all, which is why step 3 is a rejection filter and never an instruction) |
| 7 | `iss` equals the expected issuer exactly | Mismatch | FR-005 |
| 8 | `aud` contains the configured AUD tag | Missing or no match | Cloudflare issues `aud` as an array; a bare string is accepted too, since equality against the pinned value is the check either way. Without this, an assertion minted for **any other application in the same account** validates here — the keys are per-account, and the audience is the only thing that pins *this* application (FR-002, FR-003, SC-002) |
| 9 | Time: `now < exp`; if `nbf` present, `now ≥ nbf`; `iat` not in the future | Expired, or validity begins in the future | Both directions (FR-006). Leeway: **60 seconds**, fixed. Clock drift between the edge and the host is real; anything wider extends every token's life, and milestone 1's rule applies — do not widen a window to fix a clock, fix the clock |
| 10 | `email` present and **non-empty** | Absent or empty | This is the service-token shape, and the whole of FR-013c — see below |
| 11 | `email`, lowercased, is in the allowlist | Not on it | The daemon's own re-check of the gate (FR-007). Refused **and recorded** — the reason is a repo-authored constant; the address itself is not written to the trail ([data-model.md](../data-model.md#auditrecord--additions)) |

Success produces a `VerifiedOperator{Email, Owner: auth.CallerOperator}` and nothing
else. The handler behind the middleware receives the conclusion, never the assertion.

### The algorithm is read only to reject

Step 3 reads `alg` for exactly one purpose: to refuse anything that is not the
constant `RS256`. It is **never used to select a verifier** — there is exactly one
verifier, and it runs regardless of what the header said, because by the time it
runs the header has already been required to say `RS256`.

This is the entire class of historical JWT breaks: `alg: none` (verification skipped
because the token asked), and `alg: HS256` key confusion (the RSA public key used as
an HMAC secret, because the token chose the algorithm and the verifier obeyed). Both
require a verifier that dispatches on attacker input. A validator with one algorithm
and no dispatch cannot express either bug, which is why hand-rolling ~120 lines is
*stricter* than configuring a general library not to do the other things (research
[D1](../research.md)).

### Why "no email" must not read as "allow"

Two assertion shapes arrive in production
([data model](../data-model.md#the-two-assertion-shapes-research-d2-verified)): the
identity shape carries a verified `email`; the service-token shape — which **every
API call produces**, every day — carries none. Step 10 states the rule positively:
the dashboard requires a non-empty email on the allowlist.

The wrong spelling — "reject if `email` is present and not allowed" — passes every
test that only presents identity assertions, and admits every service-token request
to the dashboard: signature valid, audience valid, issuer valid, no email to object
to. That is a credential meant to *identify a machine to the edge* becoming a
session on the dashboard. The contract test below presents a genuinely valid
service-token assertion to a dashboard route precisely to tell the two spellings
apart (FR-013c).

---

## Key-set fetch and cache

Rules and rationale live in
[data-model.md § SigningKeySet](../data-model.md#signingkeyset); the contract's
summary, in order of application at step 4:

1. Keys are cached in memory and **never fetched per request** (FR-008).
2. An unknown `kid` triggers **one** refetch — the only trigger. Rotation announces
   itself as a new `kid`; there is no timed refresh.
3. Refetches are **single-flight** (two requests racing the first fetch produce one
   fetch) and **floored** at one per 60 seconds (a stream of random `kid`s is an
   attack, not rotation; inside the floor an unknown `kid` is refused without a
   fetch).
4. The fetch itself is bounded: a request timeout, and the response read through a
   size limit — an identity provider must not be able to stall or balloon the daemon
   any more than a caller may.
5. Only `kty: RSA` entries with usable `n` and `e` enter the cache; anything else in
   the set is skipped. A fetch yielding **zero usable keys** is a failed fetch.
6. **Any failure refuses the browser request** (FR-009, US3 scenario 5). The cache
   keeps its previous contents; a later request may trigger the next attempt.

For local development and the [quickstart](../quickstart.md),
`CRSW_ACCESS_TEAM_DOMAIN` accepts a full origin, with `http://` permitted only on a
loopback host — the rationale and limits are in
[data-model.md § Config](../data-model.md#config--additions). Validation runs in
full against whatever that origin serves; nothing is skipped.

---

## The uniform response

Every failure in the sequence — all eleven steps, and the keys-unobtainable case —
produces **one identical response**: status `401`, one package-level body, the same
bytes for every cause, carrying the browser-door response headers
([dashboard.md](./dashboard.md)) and nothing that names a check (FR-010, SC-001).

Milestone 1's reasoning applies unchanged: a body assembled per call site is a body
that eventually differs by a space, and the difference between "bad signature" and
"wrong audience" is reconnaissance — it tells an attacker which forgery to try next.
The response is a single constant for the same reason `bodyUnauthorized` is one in
`internal/httpapi`. It is deliberately *not* the API door's JSON body: each door has
one uniform refusal of its own, and FR-010's uniformity is within the door, where an
attacker probing it lives.

The specific reason is recorded server-side only, as `access.reject`, one record per
request, under a repo-authored constant — never the assertion, never a claim value
(FR-010, SC-008).

---

## The dev bypass (FR-038 – FR-042)

| Rule | Contract |
|---|---|
| Scope | Skips **layer 1 only**. Layers 2 and 3 run untouched — a signed API request still needs its signature and token with the bypass on (FR-038) |
| Existence | `//go:build dev` for the bypass, `//go:build !dev` for its absence. In the shipping artifact the bypass **does not exist** — requesting it refuses startup, because the flag itself is unknown (FR-041, SC-012). CI's release build compiles without the tag, and a build-failing check asserts the shipping artifact cannot enable it |
| Listener | Refuses to operate unless the listener is loopback (FR-039). Milestone 1 already refuses a non-loopback bind at three independent points, so this is a fourth assertion — kept because the bypass must not silently widen if the bind rule is ever relaxed, and a redundant refusal is cheap |
| Announcement | A prominent warning on **every request**, not only at startup (FR-040). A startup line scrolls away; the operator who left the bypass on yesterday needs today's requests to say so |
| Configuration | The `CRSW_ACCESS_*` values are not demanded while the bypass is active (FR-042) |
| Identity | The header shows an explicit bypass marker, never a fabricated email. FR-020's header exists so it is never ambiguous whose credentials are driving sessions; under the bypass the truthful answer is "nobody's — layer 1 is off", and the interface says that |

---

## Contract tests

Each maps to a requirement or success criterion and must fail if the behaviour is
removed (SC-017). All assertions are minted from a **locally generated RSA key
pair** — no network, no fixture that expires (plan, Testing) — with the key set
served from a test-controlled source.

| Test | Asserts | Covers |
|---|---|---|
| Valid identity assertion | Admitted; `VerifiedOperator` carries the email; owner is `CallerOperator` | FR-001, FR-036, FR-037a |
| No header / empty header | Uniform refusal | SC-001 |
| Two segments; bad base64; header not JSON; claims not JSON | Uniform refusal | SC-001, edge cases |
| `alg: none`, `alg: HS256` (signed with the RSA public key as HMAC secret), `alg: RS384` | Uniform refusal — the HS256 case is the key-confusion break in test form | FR-004, SC-001 |
| Unknown `crit` parameter | Uniform refusal | step 3 |
| Signature over a modified payload | Uniform refusal | FR-001, SC-001 |
| Signed with a key not in the set | Uniform refusal after one refetch | FR-008, SC-001 |
| Expired; `nbf` in the future; `iat` in the future | Uniform refusal, both directions | FR-006, SC-001 |
| Wrong audience (valid assertion, different AUD) | Uniform refusal | FR-003, SC-002 |
| Wrong issuer | Uniform refusal | FR-005 |
| **Valid service-token assertion presented to a dashboard route** | Refused. The negative that tells the two allowlist spellings apart | FR-013c |
| Valid identity, email not on allowlist | Refused; `access.reject` recorded; the email absent from the trail | FR-007, FR-010, SC-008 |
| Key source down on first request | Refused, not admitted; a later request fetches | FR-009, US3 scenario 5 |
| Key set fetched but empty | Treated as unobtainable; refused | FR-009, edge case |
| Two requests race the first fetch | Exactly one fetch | FR-008, edge case |
| Unknown-`kid` storm | At most one fetch per floor interval | FR-008 |
| Response bytes across every failure above | Byte-identical, headers included | FR-010, SC-001 |
| Full trail capture across every case above | One record per request; no assertion, token, or claim value | FR-016, SC-008 |
| Release build (`!dev`) | Bypass flag does not exist; requesting it refuses startup | FR-041, SC-012 |
| Dev build, bypass active | Layer 1 skipped; layers 2–3 still enforced; warning on every request; `CRSW_ACCESS_*` not required | FR-038, FR-040, FR-042 |
| Dev build, bypass active, non-loopback listener | Refuses to start | FR-039 |
| Missing/empty `CRSW_ACCESS_TEAM_DOMAIN`, `_AUD`, `_ALLOWED_EMAILS` (bypass off) | Startup failure, nothing bound | FR-011, SC-013 |
