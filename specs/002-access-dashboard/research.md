# Phase 0 Research: Access Validation & Read-Only Dashboard

**Feature**: [spec.md](./spec.md) · **Date**: 2026-08-04 · **Plan**: [plan.md](./plan.md)

Decisions that change the design. Cloudflare's behaviour was read from its documentation
rather than recalled, and milestone 1's behaviour from its code rather than its spec —
the cross-model review of this feature found three places where the spec described a
system that did not exist, so nothing here is taken on trust.

---

## D1. Verify the assertion with the standard library. No dependency.

**Decision**: Hand-roll RS256 verification against a cached key set, in a new
`internal/access` package. `go.sum` continues not to exist.

**What Cloudflare actually issues** (verified, sources below):

| | Value |
|---|---|
| Header | `Cf-Access-Jwt-Assertion` (browsers also carry a `CF_Authorization` cookie) |
| Key set | `https://<team>.cloudflareaccess.com/cdn-cgi/access/certs` |
| Algorithm | **RS256**, and only RS256 |
| `iss` | `https://<team>.cloudflareaccess.com` |
| `aud` | The per-application AUD tag, fixed for the life of the application |

**Rationale**: `docs/security.md` §5 puts the burden of proof on the import — "what does
it do that stdlib cannot?" — and milestone 1's zero-dependency property is asserted by a
test that fails if `go.sum` appears. For **one** algorithm against **one** issuer, the
answer is: nothing.

```
encoding/base64   segment decode (RawURLEncoding)
encoding/json     header and claims
crypto/rsa        VerifyPKCS1v15
crypto/sha256     the digest
math/big          modulus and exponent from the JWK's n and e
net/http          fetch the key set
```

A general JWT library exists to handle the algorithms this daemon must **refuse**. Its
main historical CVEs are `alg` confusion and `alg: none` — vulnerabilities that come from
being general. Accepting exactly one algorithm, named as a constant and compared before
anything else is parsed, is both smaller and stricter than configuring a library not to
do the other things.

**Alternatives rejected**: `github.com/golang-jwt/jwt` plus a JWKS fetcher — two
dependencies, a `go.sum`, a Dependabot surface, and a supply-chain root for a daemon
whose threat model is "a request that passes authentication is arbitrary code execution".
The trade is real and goes the other way here.

**Non-negotiable in implementation**: the algorithm is read from the header only to
**reject** anything that is not `RS256` — it is never used to select a verifier. That is
the entire class of JWT break, and the reason this is ~120 lines rather than a call.

**Sources**: [Validate JWTs](https://developers.cloudflare.com/cloudflare-one/identity/authorization-cookie/validating-json/) ·
[Application token](https://developers.cloudflare.com/cloudflare-one/access-controls/applications/http-apps/authorization-cookie/application-token/)

---

## D2. The two assertion shapes are different, and the difference is load-bearing (VERIFIED)

**Decision**: The dashboard accepts only an identity assertion. A service-token assertion
is refused for the dashboard, by requiring a non-empty `email` that is on the allowlist —
never by "no email means allow".

Cloudflare's documented payloads:

| Claim | Identity provider | Service token |
|---|---|---|
| `aud`, `iss`, `exp`, `iat`, `type` | ✅ | ✅ |
| `email` | ✅ the verified address | **absent** |
| `sub` | the user | **empty string** |
| `common_name` | absent | ✅ the service token's Client ID |
| `identity_nonce`, `country`, `nbf` | ✅ | absent |

This is exactly FR-013c, and it is not a hypothetical: **every API call the operator's
client makes produces one of these.** A validator written as "verify signature, then look
up `email` in the allowlist" refuses them correctly. One written as "verify signature,
then reject if `email` is present and not allowed" admits every service-token request to
the dashboard. Both readings pass a test that only tries a valid identity token.

**Consequence for the API path**: the API client's requests arrive at the daemon carrying
a valid service-token assertion. The daemon ignores it entirely and authenticates them at
layers 2 and 3, exactly as milestone 1 does. The assertion is the edge's business.

---

## D3. `http.ResponseController` removes the WriteTimeout problem (VERIFIED)

**Decision**: Keep the server's 30-second `WriteTimeout` and clear it **per response**, on
the stream route only, with `http.NewResponseController(w).SetWriteDeadline(time.Time{})`.

`internal/httpapi/server.go:40` already carries the note that "Milestone 2's SSE
streaming cannot live under WriteTimeout and will need its own answer." This is the
answer, and it is standard library: `ResponseController` has existed since Go 1.20, and
the module targets 1.23.

**Rationale**: the obvious alternative — setting `WriteTimeout: 0` on the server — removes
the timeout from *every* route, including the six that milestone 1 shipped with it. A
per-response deadline keeps the protection where it belongs and lifts it only where a
response is deliberately unbounded.

**Alternatives rejected**: hijacking the connection (loses `http.Server`'s shutdown
tracking, and FR-034f requires streams not to delay shutdown); a second `http.Server` on
a second port (two listeners to keep on loopback, two configs to get wrong).

---

## D4. Screen mirroring over SSE, with the screen carried as JSON

**Decision**: One SSE event per capture, whose `data:` is a **JSON string** holding the
entire screen. The client parses it and assigns `textContent`.

**Rationale**: FR-031a requires the current screen rather than an append-only transcript,
and a screen is inherently multi-line. SSE's wire format is line-oriented — a raw newline
inside `data:` starts a new field — so a screen must either be split across several
`data:` lines (which the client rejoins, and which silently corrupts a payload containing
a lone `\r`) or encoded. JSON encoding is one `json.Marshal` on the server and one
`JSON.parse` on the client, and it makes the payload's framing independent of its content.

It also makes the security rule mechanical: the value that comes out of `JSON.parse` is a
string, and the only thing done with it is `textContent`. There is no path where it
becomes markup, which is FR-028's "closed by construction, not by sanitising".

**Explicitly not used**: htmx's `sse-swap` / `hx-swap`. That inserts payloads as markup,
which `docs/security.md` forbids in terms. `docs/components.md` documented exactly that
until this milestone corrected it — see the spec's "Documents this milestone must amend".
htmx remains the right tool for the rest of the dashboard.

---

## D5. Capture cadence, and why the stream is a poller

**Decision**: The daemon captures each watched session's pane on a fixed interval and
emits an event only when the screen **differs from the last one sent**. Interval: 1s.

**Rationale**: tmux offers no "tell me when this pane changes" notification that
`internal/tmuxctl` could subscribe to; `capture-pane` is a poll by nature. One second is
below the threshold at which a human reading a terminal notices lag, and it bounds the
work: one `exec` per watched session per second, capped by FR-034e.

Suppressing unchanged screens matters more than it looks. A Claude session sitting idle
at a prompt would otherwise push an identical screen every second to every open tab,
forever — burning the host and making the audit story worse for no information.

**Alternatives rejected**: streaming on a tmux `pipe-pane` hook (delivers raw bytes
including escapes, needs a second capture path, and `pipe-pane` writes to a file or
command — a new artifact on disk holding session output, which `docs/security.md` §3
calls secret); pushing from `Manager.Prompt` (only sees what the daemon sends, not what
Claude prints, which is most of it).

---

## D6. Display state derives from milestone 1's own threshold (VERIFIED)

**Decision**: `idle` when `now` is at or past `Session.IdleDeadline()`, `running`
otherwise. No new constant, no new field, no state machine.

Verified present in milestone 1:

```go
// internal/session/session.go
IdleTimeout = 60 * time.Minute
func (s Session) IdleDeadline() time.Time { return s.LastActivity.Add(IdleTimeout) }
```

`internal/session/reaper.go:290` decides expiry with `!now.Before(s.IdleDeadline())`. The
dashboard uses **the same method**, so FR-019c's "exactly one definition" is satisfied by
construction rather than by two constants that agree today. A session the reaper is about
to destroy cannot read as running, because both are asking the same question.

Confirmed unreachable and therefore not rendered: `Store.SetState` has no production
caller, `StateDead` is read in three guards and never written, and both `Destroy` and the
reaper delete records outright. `starting` is written once at create and never changes,
so it is displayed as running — the distinction lasts as long as one `tmux` exec.

---

## D7. Ownership: one owner, by construction

**Decision**: A validated dashboard identity resolves to `auth.CallerOperator` — the same
constant the API's shared secret resolves to. The mapping is code, not configuration.

**Rationale**: `internal/auth/caller.go:22` already says why — "a constant and not a
configured value **on purpose**. Anything an operator could set here would be a second
place for the identity to disagree." A setting whose only correct value is that constant
is a way to produce an empty dashboard with every test green, which is the failure the
spec's third clarification existed to prevent. FR-037a was reworded to forbid the knob.

The allowlist stays configuration — it decides **who may become** the operator. What that
identity then *is* stays fixed.

**The check does not become a no-op**: `Manager.Resolve` and `Store.List` still take an
owner and still compare it. FR-037b requires the cross-owner refusal to be exercised
through the dashboard's own route with a synthetic second owner, not by pointing at
milestone 1's API test.

---

## D8. Cross-site: no CORS headers, and refuse a cross-site stream open

**Decision**: The daemon emits no `Access-Control-Allow-*` header on any route, asserted
by a test that sweeps every response. Stream opens are additionally refused when
`Sec-Fetch-Site` is present and is not `same-origin`.

**Rationale**: the browser's layer-1 credential is a cookie, so it is **ambient** — it
rides on requests a hostile page triggers, and the edge turns those into a valid
assertion. Same-origin policy is what stops that page reading the result, and it holds
only while the daemon never opts out of it. Declining the per-session token for streams
(FR-034a) is the right call, but it means this is the protection doing the work, so it is
a requirement with a test rather than an absence nobody checks.

`Sec-Fetch-Site` is belt-and-braces: it is sent by current browsers and absent from
non-browser clients, so it is treated as "refuse if it says cross-site", never as
"require it".

**Worth stating plainly**: classic CSRF is structurally impossible in this milestone. The
dashboard has no mutating route, and every mutating route requires an HMAC signature a
browser cannot produce. That reasoning expires the moment milestone 3 adds a browser-driven
write, which is why the spec records it in Out of Scope rather than leaving it to be
inherited.

---

## D9. Assets are embedded; the CSP has no exceptions

**Decision**: `web/` holds templates, one stylesheet and one script, embedded with
`go:embed` and served from the binary. The policy in `docs/security.md` is sent
unmodified: no inline script, no inline style, no external origin.

This forces two things the design system already requires: no webfont URL (the fallback
stack is the stack), and the digital-rain canvas driven by an embedded script file rather
than an inline `<script>`. Both are cheaper to do now than to retrofit around a CSP
violation discovered in a browser console.

---

## D10. What this milestone must not disturb (VERIFIED against the code)

Checked, because the spec makes claims about each and a plan built on a wrong belief is
the expensive kind:

| Claim | Verified |
|---|---|
| Signing payload is `METHOD\nPATH\nts.body` | ✅ `internal/auth/hmac.go` |
| Six operations, closed route table, catch-all behind layer 2 | ✅ `internal/httpapi/server.go` |
| Audit record is a fixed seven-field struct | ✅ `internal/audit/audit.go` |
| Uniform 401 and 404 are single package-level byte slices | ✅ `internal/httpapi/middleware.go` |
| `Owner` is a server-derived `CallerID` | ✅ `internal/auth/caller.go` |
| Reaper runs on a 30s tick enforcing 60m/24h | ✅ `internal/session/reaper.go`, wired at `cmd/crswd/main.go` |
| `web/` does not exist and nothing uses `go:embed` yet | ✅ |
| Adopted sessions carry no `Name` and no `WorkDir` | ✅ `internal/session/manager.go` — FR-018a exists because of this |

---

## Open items carried into the plan

None blocking. One flagged:

- **The dashboard adds a second reason to read a session record**, and milestone 1
  advances the idle clock inside `Manager.Resolve` with a comment claiming that is the
  only place a request becomes a session. FR-034f makes that claim false. The plan must
  route dashboard reads so they do **not** touch the clock, and amend that comment —
  leaving it would send a later iteration to "fix" the inconsistency in the wrong
  direction.
