# Phase 1 Data Model: Access Validation & Read-Only Dashboard

**Feature**: [spec.md](./spec.md) · **Plan**: [plan.md](./plan.md) · **Research**: [research.md](./research.md)

Everything here lives in memory for the process lifetime, exactly as in
[milestone 1's data model](../001-crswd-daemon-core/data-model.md). Nothing this
milestone adds is persisted, and nothing milestone 1 stores changes shape: the
`Session` record, the audit record, and the config loader gain entries but lose and
alter nothing, because a caller written against the shipped contract must not need
changing (FR-014, FR-015). Field types are Go types because the model is still the
code.

The dominant pattern in this milestone is **derivation**. Milestone 1's model derived
a handful of values to keep them from drifting; here almost every new value the
operator sees — display state, age, the summary counts, the operator's own identity —
is computed at the moment it is rendered, from a record that already exists. The one
genuinely new stored thing is a long-lived connection: the live stream.

---

## VerifiedOperator

The outcome of layer-1 validation: a browser identity the daemon has itself
confirmed. It is a conclusion, not a claim — "someone Access let through" is what the
header asserts; this type exists only after the daemon has re-proven it
([contracts/access-jwt.md](./contracts/access-jwt.md)).

| Field | Type | Source | Invariant |
|---|---|---|---|
| `Email` | `string` | The `email` claim of a **validated identity assertion** | Non-empty by construction — an assertion with no email never produces one of these (FR-013c). Lowercased before the allowlist compare |
| `Owner` | `auth.CallerID` | The constant `auth.CallerOperator` | Never configuration, never a claim. Research [D7](./research.md): the mapping is code, so the dashboard's owner and the API's owner cannot disagree (FR-037a) |

**Derived per request, never stored.** There is no server-side browser session, no
login state, and no identity cache: every browser request re-validates its assertion
and re-derives this value. Storing it would create the daemon's first cross-request
browser state and with it the questions — expiry, invalidation, fixation — that
milestone 1's design never has to answer. The edge already maintains the browser
session; the daemon only ever checks the evidence of it.

### The two assertion shapes (research [D2](./research.md), verified)

Both shapes arrive in production, every day. The service-token shape is what **every
API call** carries after the edge admits it (FR-013a), so it is the one
malformed-looking assertion that is routine rather than hostile.

| Claim | Identity assertion | Service-token assertion |
|---|---|---|
| `aud`, `iss`, `exp`, `iat` | present | present |
| `email` | the verified address | **absent** |
| `sub` | the user | empty string |
| `common_name` | absent | the token's Client ID |

The rule that keeps them apart is stated positively: a `VerifiedOperator` requires a
**non-empty `email` that is on the allowlist**. It is never stated as "reject if the
email is present and disallowed" — that reading admits every service-token assertion
to the dashboard, and both readings pass a test that only tries a valid identity
token (FR-013c). The negative test in
[contracts/access-jwt.md](./contracts/access-jwt.md) exists to tell them apart.

The API door ignores the assertion entirely: a service-token assertion on an API
request is the edge's business, and the daemon authenticates that request at layers
2 and 3 exactly as milestone 1 does (FR-013b).

---

## SigningKeySet

The edge's published RSA public keys for this account, cached in memory. These are
the **edge's** keys, not the identity provider's — Google signs nothing the daemon
ever sees (FR-002).

| Field | Type | Notes |
|---|---|---|
| `keys` | `map[string]*rsa.PublicKey` | Keyed by `kid`, built from each JWK's `n` and `e` via `math/big` (research [D1](./research.md)) |
| `fetchedAt` | `time.Time` | On the injected clock; drives the refetch floor below |

**Fetch and cache rules** — each exists because of a named failure:

- **Never fetched per request** (FR-008). A fetch per request makes the identity
  provider's availability a per-request dependency and hands a traffic amplifier to
  anyone who can send requests.
- **Refetched only on an unknown `kid`.** Key rotation is the one event that
  invalidates the cache, and a rotated key announces itself as a `kid` the cache has
  never seen. There is no timed background refresh, because FR-008 names the rule and
  a second trigger would be a second thing to reason about.
- **Refetches are single-flight.** Two requests racing the first fetch — an edge case
  the spec names — must produce one fetch, with the loser waiting on the winner's
  result rather than fetching again.
- **Refetches have a floor**: at most one fetch per 60 seconds. An attacker who can
  reach the listener can mint assertions with random `kid` values forever; without a
  floor, each one is a command to call out to Cloudflare. Real rotation happens on
  the order of weeks, so a one-minute floor costs nothing legitimate. An unknown
  `kid` inside the floor is refused without a fetch.
- **Fail closed** (FR-009). A fetch that fails, times out, or returns an empty or
  unparseable key set leaves the cache as it was and refuses the request that needed
  it. An identity that cannot be verified is not an identity; the daemon does not
  fall back, retry in-band, or admit on stale hope. The first request after a cold
  start with an unreachable provider is refused — the next request tries again.

The key-set URL is derived (see [Derived, not stored](#derived-not-stored)), never
configured separately: a second config value could disagree with the issuer it must
match.

---

## IdentityAllowlist

The addresses the daemon itself will accept, held in its own configuration so that an
edge misconfiguration cannot silently widen access (FR-007). The edge is the gate;
this is the daemon's assertion that the gate is configured the way the operator
believes.

| Property | Value |
|---|---|
| Source | `CRSW_ACCESS_ALLOWED_EMAILS`, comma-separated |
| Normalisation | Entries lowercased at load; the assertion's claim lowercased before compare |
| Cardinality | Expected to hold exactly one address (the spec's single-operator assumption), enforced to hold at least one |
| Empty | Startup failure (FR-011), except under the dev bypass (FR-042) |

Lowercasing folds spellings of the same mailbox, never across mailboxes: the only
identity provider in play issues lowercase addresses, and an operator who typed a
capital in their own config should not be locked out of their own host by it.

The allowlist decides **who may become** the operator. What that identity then *is*
stays the constant `auth.CallerOperator` — the allowlist is configuration and the
mapping is code, which is the split research [D7](./research.md) fixes so that a
misconfigured allowlist fails loudly (refusal, recorded) rather than quietly
(an empty dashboard with every test green).

---

## Session — unchanged, plus a derivation

The `Session` record is [milestone 1's](../001-crswd-daemon-core/data-model.md#session),
byte for byte. No field is added, because everything this milestone needs to show is
already derivable — and FR-019a exists precisely because the one tempting addition, a
stored display state, would be wrong: the daemon writes only `starting` and `running`
and deletes records rather than marking them dead, so a stored state would show one
label forever.

### DisplayState (derived at render time — FR-019a)

| Stored fact | Displayed as | Why |
|---|---|---|
| `!now.Before(s.IdleDeadline())` | **idle** | The session has had no activity past the threshold the reaper enforces |
| otherwise | **running** | Alive and inside its bounds |
| `State == starting` | **running** | The distinction lasts one tmux exec and means nothing to an operator watching a fleet |
| `State == dead` | *(never rendered)* | A dead session has no record — destroy and the reaper both delete (FR-019b) |

The idle comparison is **`Session.IdleDeadline()`, the same method the reaper's sweep
uses** (`expiredAt` in `internal/session/reaper.go`), not a second constant that
agrees with `IdleTimeout` today (research [D6](./research.md), verified). FR-019c is
satisfied by construction: a session the reaper is about to destroy cannot read as
running, because the dashboard and the reaper are asking the same question of the
same clock. The boundary lands on the same side too — at the deadline, the session is
already idle, exactly as at the deadline it is already reapable.

`needs-auth` keeps its token and its rendering path in the status component but is
not produced this milestone — it arrives with milestone 4's device-code relay, and a
state that renders wrongly the first time it occurs is a defect that ships silently
(spec, Assumed defaults).

---

## SessionView

One session as the dashboard presents it: a projection of the `Session` record built
at render time, never a second copy that could disagree (spec, Key Entities).

| Field | Source | Rendering invariant |
|---|---|---|
| `ID` | `Session.ID` | Used for the detail link and the stream URL. Carries no secret — see [contracts/stream.md](./contracts/stream.md) on why the URL may hold an ID and never a credential |
| `Name` | `Session.Name` | **Text, never markup** (FR-030) — a caller chose it. May be absent (below) |
| `DisplayState` | Derived above | Always a text label; colour is reinforcement only (FR-019) |
| `WorkDir` | `Session.WorkDir` | Text on the same terms as Name. May be absent (below) |
| `Age` | `now − Session.CreatedAt` | Derived, coarse, human-readable. Computed server-side at render; there is no ticking client clock to drift |

**Absence is a routine state, not an edge case** (FR-018a). A session adopted after a
daemon restart has **no Name and no WorkDir** — milestone 1 records neither, on
purpose, because nothing on the host carries them
(`internal/session/manager.go`, `Adopt`; research [D10](./research.md) verified it).
The view renders that absence as an explicit, readable statement that the value is
unknown — visually distinct from a real value (dim, sans-serif prose per the design
system's "a human wrote it" rule) — and never invents a placeholder that reads like a
real name or path. A dashboard showing `~/code` for a session whose directory it does
not know is telling the operator something false about an unsandboxed shell.

Every `SessionView` is reached through the **owner-scoped** reads the API uses —
`Store.List(owner)` for the fleet, an owner-scoped single read for the detail page —
never an owner-blind one (FR-017, FR-037). Milestone 1 keeps its only owner-blind
lookups unexported on purpose, and they stay that way: exporting one to feed the
dashboard would make cross-session isolation a thing every future handler has to
remember, which is the one rule this project cannot afford to enforce by memory.

The summary row's counts are derived by counting `DisplayState` over the views just
built — never tracked as counters that could drift from the cards below them.

---

## LiveStream

A long-lived delivery of one session's screen to one browser. This is the milestone's
one genuinely new stored entity, and its lifecycle is a contract of its own —
[contracts/stream.md](./contracts/stream.md) carries the wire format and the ordered
authorisation; what belongs here is what the entity *is* and the invariants that
outlive any one request.

| Field | Type | Notes |
|---|---|---|
| `SessionID` | `string` | From the daemon's own record after the ownership check — never the path value the caller sent |
| `Owner` | `auth.CallerID` | The verified operator who opened it, re-checked on every tick (FR-034b) |
| `OpenedAt` | `time.Time` | For the operator reading the trail; the open is the audited event (FR-016a) |

### Lifecycle

```
            authorised (layer 1 + ownership + cap, in order)
  (request) ────────────────────────────────────────────────► open
                │                                              │ every tick: re-evaluate,
                │ any check fails                              │ capture, suppress, write
                ▼                                              ▼
             refused ── uniform response,                   closed ── reason recorded nowhere
             one audit record (deny)                        but the connection: the one
                                                            audit record was written at open
```

A stream closes for exactly one of: the client closed the connection; the session
ended (destroyed, reaped, or expired) — announced with a terminal event first
(FR-033); the session ceased to be the viewer's (FR-034b); a write failed; the daemon
is shutting down. Every close releases the stream's cap slot and drops its buffered
screen.

### Invariants — each one names the failure it prevents

- **A stream never advances the idle clock** (FR-034f). Milestone 1 advances that
  clock in `Manager.Resolve`, the single place a request resolves to a session — and
  its comment says so. The stream's reads go through a path that does not touch the
  clock, because a stream that did would let a forgotten browser tab hold an
  unsandboxed shell open indefinitely, which is the bound Principle VI calls
  non-negotiable. The `Resolve` comment is amended as part of this work (research,
  Open items) so a later iteration does not "fix" the inconsistency backwards.
- **A stream never delays teardown or shutdown** (FR-034f). Session teardown closes
  any stream tailing it — `docs/auth-and-sessions.md`'s teardown checklist already
  lists that box, written before the feature existed — and shutdown closes all
  streams before the request drain, so a stream cannot spend the drain budget that
  milestone 1's six short routes rely on.
- **Streams are capped** at `CRSW_MAX_STREAMS`, counted and admitted in one critical
  section (FR-034e). The count-then-insert race is the same one
  `Store.AddCapped` closes for sessions and for the same reason: two opens racing the
  boundary must not both find room. Past the cap the daemon refuses; each stream is
  one `capture-pane` exec per second against the host, and unbounded streams are the
  local denial of service the session cap exists to prevent.
- **One audit record per stream request, emitted at open** (FR-016a). Milestone 1
  emits its record after the handler returns, which for a connection lasting hours
  means a daemon that dies mid-stream leaves no trace that session output was being
  read. The stream's record is written when the authorisation decision is made, and
  there is **no second record at close** — SC-008 requires exactly one record per
  request, and the open is the one that carries the decision. FR-016a leaves the
  close-record choice to this milestone; this is the choice, stated.
- **The last-sent screen is the only pane content a stream holds**, kept solely so an
  unchanged screen is not re-sent (research [D5](./research.md)), shared per watched
  session rather than per stream — the capture cost model is one exec per watched
  *session* per second, however many tabs watch it. It is dropped at close and at
  session teardown, which is the "buffered or cached pane output" box on the teardown
  checklist becoming load-bearing for the first time.

---

## Config — additions

Loaded once at startup, every failure fatal before the listener binds, exactly as
milestone 1's loader works (FR-011 is the same rule milestone 1 applies to its shared
secret). "Required" below means *fatal when absent* — except when the dev bypass is
**active**, under which the three layer-1 values are not demanded at all (FR-042:
demanding an audience the bypass then ignores would make local development need a
Cloudflare account). The bypass being merely compiled in does not lift the
requirement; only the operator activating it does.

| Env | Type | Required | Default | Failure |
|---|---|---|---|---|
| `CRSW_ACCESS_TEAM_DOMAIN` | `string` | **yes** | — | Fatal if unset, or not a usable origin (below) |
| `CRSW_ACCESS_AUD` | `string` | **yes** | — | Fatal if empty |
| `CRSW_ACCESS_ALLOWED_EMAILS` | `[]string` (`,`-separated) | **yes** | — | Fatal if empty or containing an empty entry |
| `CRSW_MAX_STREAMS` | `int` | no | `10` | Fatal if `< 1` |

**`CRSW_ACCESS_TEAM_DOMAIN`** is normally the team's hostname
(`<team>.cloudflareaccess.com`), from which the daemon derives both values that must
agree: the issuer `https://<host>` and the key-set URL
`https://<host>/cdn-cgi/access/certs`. One configured value, two derivations —
configuring them separately would allow an issuer and a key set that do not belong to
each other, which is a validator checking signatures against the wrong authority.

A full origin with an explicit scheme is also accepted, and **`http://` is refused
unless the host is loopback** — the same rule FR-039 applies to the dev bypass, for
the same reason. The carve-out exists so the [quickstart](./quickstart.md) and the
contract tests can stand up a key server they control on `127.0.0.1` and exercise the
whole validator, including its negative cases, with no Cloudflare account and no
synthetic CA in the trust store. It is not a bypass: validation runs in full against
whatever keys that origin serves, and a production value pointing at Cloudflare is
`https` by construction.

**`CRSW_MAX_STREAMS` defaults to 10** — twice the default session cap, so every
session on a fully loaded host is watchable from two tabs before the daemon starts
refusing. The spec fixes no number (FR-034e fixes the *property*: capped, refuse past
it); this default follows milestone 1's pattern of small bounds that an operator can
raise deliberately.

`CRSW_ACCESS_AUD` is compared for equality against the assertion's audience and never
parsed, so only non-emptiness is enforced. Pinning Cloudflare's current 64-hex format
would add nothing — a wrong value already fails every request — and would break the
daemon the day Cloudflare changes its tag format.

---

## AuditRecord — additions

The record's shape is frozen (FR-016): the same fixed seven-field struct, no new
fields, because a record that cannot carry arbitrary data cannot leak arbitrary data.
What is added is actions:

| Action | Emitted when | Notes |
|---|---|---|
| `dashboard.view` | A dashboard page is served — the fleet (`GET /`) or one session's view | `session_id` set on the latter, from the daemon's own record |
| `dashboard.asset` | An embedded static asset is served | See below |
| `stream.open` | A stream request is decided — allow **or** deny | Emitted at open, not close (FR-016a). The stream's only record |
| `access.reject` | Any layer-1 failure | The browser door's `auth.reject`. Reason is server-side only (FR-010) |
| `route.unknown` | A request matches no route | Kept from milestone 1; the *door* that answers changes (FR-013d), the trail's name for it does not |

`dashboard.asset` exists because FR-016 and SC-008 say **every** request produces
exactly one record, asset fetches are requests, and `internal/audit`'s own rule
forbids recording traffic under an approximate neighbour. The plan's sketch named
three new actions; this is the fourth, added for that requirement rather than
invented past it.

**The disallowed email is not recorded.** An allowlist refusal is recorded
(FR-007) under a repo-authored constant reason, but the address itself is a value
from the request, and milestone 1's discipline — reasons are fixed strings authored
in this repo, never bytes a caller supplied — is what makes FR-042/SC-008 a property
of the type rather than a review item. An operator investigating *who* was refused
correlates with the edge's own Access logs, which record the identity at the layer
that verified it first.

**Forbidden in every record, unchanged and re-asserted by test** (FR-035, SC-008): an
identity assertion, any token, pane content, prompt text, the shared secret.

---

## Derived, not stored

Milestone 1 kept this table short; here it is most of the model. Storing any of
these would let it drift from the fields it derives from — and for the first four,
"drift" means the operator reading something false about a live unsandboxed shell.

| Derivation | Value | Why derived |
|---|---|---|
| Display state | idle iff `!now.Before(s.IdleDeadline())`, else running | The stored field would read `running` forever (FR-019a); the reaper's own method is the single definition (FR-019c) |
| Age | `now − CreatedAt` | A stored age is stale the moment it is written |
| Summary counts | Count of each display state over the views rendered below them | A tracked counter can disagree with the cards it summarises |
| Verified operator | Re-validated from the assertion on every request | Stored identity is a browser session, with the expiry and fixation questions this design refuses to acquire (FR-020, FR-036) |
| Owner of a browser request | The constant `auth.CallerOperator` | Code, not configuration — a knob's only correct value is the constant, and a wrong value is an empty dashboard with green tests (FR-037a, research D7) |
| Issuer | `https://` + team domain | Must agree with the key-set URL; one source value cannot disagree with itself |
| Key-set URL | issuer + `/cdn-cgi/access/certs` | Same |
| Open-stream count | Size of the stream registry | A separate counter is the count-then-act race the cap exists to close (FR-034e) |

---

## Relationships

```
Config ──► AccessValidator ──uses──► SigningKeySet (cached; refetch on unknown kid)
                │                    IdentityAllowlist
                │
                └─produces per request─► VerifiedOperator ──owns-as──► auth.CallerOperator
                                                                            │
Store.List(owner) / owner-scoped read ──► Session[] ──projected──► SessionView[]
        (never owner-blind; FR-017)          │                       (+ DisplayState, Age)
                                             │
                                             └──◄ watched by ── LiveStream[] (≤ CRSW_MAX_STREAMS)
                                                                  │  never touches LastActivity
Every request ──emits──► exactly one AuditRecord                  │  (FR-034f)
(streams: at open — FR-016a)                                      └── shares one capture loop
                                                                      per watched session (D5)
```

**Cardinality that matters**: many streams may watch one session (each holds a cap
slot; the captures are shared), and a stream is bound to exactly one session and one
verified owner for its whole life — re-checked, not remembered (FR-034b).
