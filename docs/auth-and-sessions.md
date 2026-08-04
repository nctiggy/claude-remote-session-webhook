# Auth & Sessions

> Loaded when: touching request authentication, tokens, or the session lifecycle.

In this project "session" means **a Claude Code session running in a tmux window**,
and "auth" means **proving a request is allowed to create, drive, or watch one**.

There are two callers and two front doors, and the daemon serves no login form of its
own: the API client signs each request, and a person signs in to Cloudflare Access at
the edge, before the daemon is reachable at all. **The daemon still keeps no browser
session** — it issues no cookie, stores no server-side session record, and has nothing
to fixate, renew, or invalidate. It re-derives who is looking from a signed assertion
on **every** request and keeps nothing between them.

Treat this file as a correctness spec, not a style guide. A bug here is host
compromise — see `docs/security.md` for why.

## Three layers, each independently sufficient to say no

| Layer | Enforced by | Stops |
|---|---|---|
| 1. Network | Cloudflare Tunnel + Access at the edge, **and the daemon validating the assertion the edge forwards** | Anyone the edge did not admit reaching the daemon at all, and anything that reached it anyway claiming to be an identity the edge vouched for |
| 2. Request | HMAC-SHA256 signature + timestamp | A forged or replayed request from something that got past layer 1 |
| 3. Session | Per-session bearer token | A valid caller driving a session that is not theirs |

Never collapse these. Layer 1 is not a substitute for layer 2: if the tunnel is
misconfigured, or the daemon is ever reached over loopback by another local process,
the signature is what is left. The converse holds on the browser door, which carries
no signature at all — there, a validated identity plus an ownership check is the whole
of the authorisation, which is why layer 1 has to be a real check in the daemon and
not a header the edge is trusted to have written.

## Two doors, one hostname

Both doors are behind the same Access application on the same hostname, admitted by
two edge policies. **No path gets an edge bypass**, and each door still faces its own
daemon-side check behind the edge:

| | Browser — the dashboard | API client — the skill |
|---|---|---|
| Admitted at the edge by | The identity policy: Google IdP, one allowlisted address | An Access **service token**, sent as `CF-Access-Client-Id` + `CF-Access-Client-Secret` |
| What the edge forwards | `Cf-Access-Jwt-Assertion`, **identity shape** — carries a verified `email` | `Cf-Access-Jwt-Assertion`, **service-token shape** — carries `common_name`, an empty `sub`, and **no email** |
| What the daemon checks | Layer 1: the assertion is genuine and names an allowlisted person | Layers 2 and 3: signature, timestamp, replay, per-session token. The assertion is **ignored entirely** |
| Refused with | One uniform 401 page | The uniform 401 JSON |

**Each door refuses only by the check that applies to it.** A browser request is never
refused for carrying no signature, and an API request is never refused for carrying no
identity. Adding either check to the other door would break a working client to
enforce something that door does not use.

**Both doors resolve to one owner** — the same `auth.CallerOperator` constant, by
construction and never by configuration — so a session created through the API is a
session the dashboard shows. The ownership comparison still runs on every request.

## Layer 1 — the Access assertion

Google is the identity provider, configured in Cloudflare Access with an allowlist of
exactly one email. Access enforces login at the edge, so an unauthenticated browser
never reaches the host at all.

Access then forwards a signed JWT in `Cf-Access-Jwt-Assertion`. **The daemon must
validate it.** A header is trivially forgeable by anything that can reach the
listener; the signature is what makes it evidence. That header is the *only* source of
a browser identity — never a path, a query, a body, or the `CF_Authorization` cookie,
which is the browser's credential *to the edge* rather than the edge's product *for
the daemon*. Two sources for one identity are two things free to disagree.

Verification is **hand-rolled against the standard library** (`internal/access`), and
that is a requirement, not an accident. A JWT library's value is exactly the algorithm
agility this daemon must refuse; `go.sum` must not appear. The order below is the
contract, because two properties live in the ordering alone — the algorithm is settled
before any cryptography runs, and the claims stay uninterpreted bytes until the
signature says who wrote them.

```go
// 1–2. Shape first, so nothing below is handed something that was never a JWS.
//      Unpadded RawURLEncoding: a padded segment is a token no signer emits.
parts := strings.Split(assertion, ".")
if len(parts) != 3 || parts[0] == "" || parts[1] == "" || parts[2] == "" {
    return nil, errAssertionMalformed
}

// 3. `alg` is read to REFUSE, never to select a verifier — there is one verifier
//    and nothing to choose with. `crit` announces an extension this validator does
//    not implement, which RFC 7515 says invalidates the token.
if header.Alg != algorithmRS256 || header.Crit != nil {
    return nil, errAlgorithmRefused
}

// 4. Cached key set, refetched only on an unknown kid and no faster than the floor.
//    Unobtainable keys REFUSE: an identity that cannot be verified is not an identity.
pub, err := v.keys.key(ctx, header.Kid)
if err != nil {
    return nil, err
}

// 5. Over the first two segments exactly as they arrived — sliced out of the
//    assertion, never rejoined. A payload re-serialised before verification is a
//    payload the signature no longer covers.
signed := assertion[:len(parts[0])+1+len(parts[1])]
digest := sha256.Sum256([]byte(signed))
if err := rsa.VerifyPKCS1v15(pub, crypto.SHA256, digest[:], signature); err != nil {
    return nil, errSignatureInvalid
}

// Only now is the payload worth parsing: 6 claims decode, 7 `iss`, 8 `aud`,
// 9 `exp`/`nbf`/`iat` in both directions, 10 a NON-EMPTY email, 11 the allowlist.
```

Non-negotiable:
- **Pin the audience.** The signing keys are per-account, so without `aud` an
  assertion minted for any other Access app in the same account validates here.
- **Pin the algorithm, and read it only to reject.** Accepting `alg` from the token
  is the classic JWT break — `alg: none` and RS256/HS256 key confusion are both bugs
  a verifier can only have if the token picks the verifier.
- **No JWT library.** See above; this is a decision, not a preference.
- **Parse nothing before the signature verifies.** A parser is attack surface, and
  until step 5 passes the payload is attacker-authored. The JOSE header is the one
  unavoidable exception, which is why step 3 is a filter and never an instruction.
- **Require a non-empty, allowlisted email — state it positively.** "Refuse an email
  that is disallowed" passes every test that presents an identity assertion and
  **admits every service-token assertion**, because there is no email to object to.
  The operator's own API client produces one of those on every call. *"No email" must
  never read as "allow".*
- **Re-check the email allowlist in the daemon.** Access is the gate; this is the
  assertion that the gate was configured the way you think. The refused address never
  reaches the trail.
- **Fail closed.** Unobtainable keys, an unknown `kid`, a malformed anything — all
  refuse. A layer-1 outage must not become an open door.
- **One uniform refusal.** Every failure above answers byte-identically; the reason
  is recorded server-side only. The difference between "bad signature" and "wrong
  audience" tells an attacker which forgery to try next.
- **Derive the identity per request; never store it.** A cached one would be the
  daemon's first cross-request browser state, and with it the expiry, invalidation
  and fixation questions this design exists not to have.

**Local development** uses the `//go:build dev` bypass in `internal/access`, which
skips layer 1 only. It must: refuse to start unless the listener is loopback, log a
loud warning on **every** request, and be absent from the shipping build. It is
selected by a build tag and never by a flag — a production binary that can bypass auth
by being invoked differently is a backdoor.

## Layer 2 — request signing

Every request carries `X-CRSW-Timestamp` (Unix seconds) and `X-CRSW-Signature`
(`sha256=` + hex HMAC over `METHOD + "\n" + PATH + "\n" + timestamp + "." + rawBody`).

```go
func (a *Authenticator) Verify(r *http.Request) (*Caller, error) {
    ts, err := strconv.ParseInt(r.Header.Get("X-CRSW-Timestamp"), 10, 64)
    if err != nil {
        return nil, errors.New("bad timestamp")
    }
    // Reject outside the window in BOTH directions — a far-future timestamp
    // would otherwise stay replayable indefinitely.
    if d := a.now().Sub(time.Unix(ts, 0)).Abs(); d > clockSkew {
        return nil, errors.New("timestamp outside window")
    }

    body, err := io.ReadAll(io.LimitReader(r.Body, maxBodyBytes))
    if err != nil {
        return nil, errors.New("unreadable body")
    }
    r.Body = io.NopCloser(bytes.NewReader(body)) // handler still needs it

    // METHOD "\n" PATH "\n" timestamp "." body. The request line is in the
    // payload because a signature has to name what it authorizes.
    mac := hmac.New(sha256.New, a.secret)
    fmt.Fprintf(mac, "%s\n%s\n%d.", r.Method, r.URL.EscapedPath(), ts)
    mac.Write(body)
    want := "sha256=" + hex.EncodeToString(mac.Sum(nil))

    // Constant-time. A byte-by-byte compare leaks the signature under timing.
    if !hmac.Equal([]byte(want), []byte(r.Header.Get("X-CRSW-Signature"))) {
        return nil, errors.New("signature mismatch")
    }
    if !a.replay.Observe(want, ts) { // seen-nonce cache, TTL == 2 * clockSkew
        return nil, errors.New("replay")
    }
    return a.callerFor(r), nil
}
```

Non-negotiable properties:
- **`hmac.Equal`, never `==`** on any secret comparison.
- **Sign the body, not just the path.** A signature over the URL alone lets an
  attacker swap the prompt.
- **Sign the method and the path too, not just the body.** The converse failure
  is quieter and was live in milestone 1 until the acceptance run found it. Over
  `timestamp + "." + body` alone, every empty-body request at one instant signs
  identically: one signed `GET /sessions` is a valid `DELETE /sessions/{id}` in
  the same second. Only the replay cache stood between them, and only if the
  original arrived — anyone able to block it and substitute their own request
  line inherited the signature. It also made the daemon refuse itself, since a
  client reading twice in one second sent the same signature twice and got a 401
  the second time. Use `EscapedPath`, so the payload covers the bytes on the
  request line rather than a decoded spelling of them.
- **The replay cache is required.** A captured request is otherwise valid for the
  whole skew window, and one replay is one extra unsandboxed session.
- Clock skew window: **300 seconds**. Do not widen it to fix a clock problem; fix
  the clock.

## Layer 3 — per-session tokens

`POST /sessions` returns a session ID and a bearer token. The token is generated with
`crypto/rand`, returned **once**, and stored only as a hash:

```go
tok := make([]byte, 32)
if _, err := rand.Read(tok); err != nil { return err }  // never math/rand
sess.TokenHash = sha256.Sum256(tok)                      // store the hash only
```

Driving a session requires both a valid layer-2 signature *and* the matching token.
A caller who somehow obtains the shared secret still cannot read a session they did
not create.

**Layer 3 applies to the API door only.** See the next section for why a browser
stream is authorised without it — and why declining it obliges the daemon to do
something else instead.

## Watching a session — the stream-authorisation rule (non-negotiable)

A live output stream (`GET /sessions/{id}/stream`) is authorised by **the validated
layer-1 identity plus the ownership check**, in this order, and nothing about the
response happens until all four have been answered:

| # | Check | Why here |
|---|---|---|
| 1 | Layer 1 — a validated **identity** assertion; a service-token assertion is refused as everywhere on this door | Nothing session-shaped is asked before the caller is known |
| 2 | Cross-site refusal on `Sec-Fetch-Site` | Decided from one header, so a request the browser itself calls cross-site never causes a session to be looked up |
| 3 | Ownership, through the read that does **not** advance the idle clock | Unknown, unowned, and already-dead are one uniform 404 — which it really was goes on the trail |
| 4 | Capacity, **last** | So a caller who was going to be refused anyway can never observe how much of this host is being watched |

`Sec-Fetch-Site` is **present-and-wrong refuses; absent does not**. Browsers send it
and non-browser clients do not, so requiring it would refuse the callers it was never
about while adding nothing against the one it is.

**The per-session token is deliberately not required, and no credential appears in the
URL.** That token exists because every API caller authenticates as the same shared
secret and needs a second factor naming the session; a browser identity is verified
per person, and the ownership check already makes that distinction. Requiring it would
add no check while forcing a credential into a URL — and URLs are logged by every
intermediary. The session ID alone opens nothing: presenting it still requires the
validated identity and still passes ownership.

**Declining it is what makes the next rule load-bearing.** The browser's credential is
an ambient cookie: it rides on requests a hostile third-party page triggers, and the
edge converts those into a valid assertion. A header credential would force a
preflight; a cookie does not. So:

- **No `Access-Control-Allow-*` header on any route, on either door.** Same-origin
  policy is the only thing stopping a hostile page from *reading* this stream with the
  operator's own riding cookie, and it holds only while the daemon never opts out of
  it. Assert the absence with a test that sweeps every registered route — an absence
  nothing checks is an absence one convenience header ends.

**Authorisation is re-evaluated, never established once.** Every tick asks the
daemon's own store again, and uses the record that ask returned rather than the one
the open closed over. A session that ended, expired, or stopped being the viewer's
stops delivering within one interval and **says so** rather than going quiet.
Teardown does not have to find the watchers; the watchers keep asking.

**Watching is not driving.** A stream must never advance the idle clock — milestone 1
advances it in the single place a request resolves to a session, and the stream takes
a second path that does not — and must never delay session teardown or daemon
shutdown. A forgotten tab that postponed an idle deadline would hold an unsandboxed
shell open for as long as it lives.

Two more, for the same reason the session cap exists (constitution VI):

- **Cap concurrent streams** (`CRSW_MAX_STREAMS`) and refuse past it, counted and
  admitted in one critical section. Each stream is a long-lived connection doing
  periodic work against the host.
- **Record the authorisation decision when the stream opens**, not when the handler
  returns. A connection lasting hours otherwise leaves no trace that session output
  was being read until it ends — and none at all if the daemon dies first. One record
  per request, no close record, and never a byte of pane content in it.

## The session-isolation rule (non-negotiable)

**Output from session A must never be reachable through a request scoped to session
B.** This is the analogue of the session-bleed bug in web apps, and it is worse here:
pane content can contain anything on the host.

Every read path takes the session ID from the *authenticated, ownership-checked*
session record — never from a caller-supplied tmux target string.

**Test for it.** A passing test looks like: create session A → produce distinctive
output → create session B as a different caller → assert every read endpoint scoped
to B returns nothing from A, and that B's token on A's ID returns 404.

**Every read path, not just the API's.** The dashboard, the single-session page and
the stream are read paths too, and a synthetic second owner has to be refused through
the browser door's own routes — a test that only drives the API proves nothing about a
door the API never uses.

## Session teardown must be complete

Destroying a session clears **all** of:

- [ ] The tmux session (`tmux kill-session`), verified gone — not assumed
- [ ] The child Claude process (reaped; no orphan holding a pty)
- [ ] The session record and its token hash
- [ ] Any buffered or cached pane output
- [ ] Any temp working directory the daemon created for it
- [ ] Any open SSE stream tailing that session — which ends *itself* within one
      interval, because each tick re-asks whether the session is still the viewer's
      (above). Teardown must not wait on a watcher, and must not have to find one

A killed session that leaves a tmux window behind is a live unsandboxed shell with
no owner. Verify the kill; log the failure loudly if it did not work.

## Relaying Claude's own login (milestone 4)

A fresh session may sit at Claude Code's device-code prompt instead of a shell. The
daemon detects that, surfaces the URL in the dashboard, takes the code the operator
pastes back, and sends it into the pane.

This is the most fragile thing in the project and the most sensitive:

- **Detection is screen-scraping** and will break when Claude Code changes its
  output. Keep it in one package (`internal/claudeauth`) behind a single
  `DetectPrompt(pane string) (*Prompt, bool)`, covered by golden files captured from
  real output. When it breaks it must be a one-file fix, not a hunt.
- **The code is a live credential.** Never log it, never put it in an audit record,
  never render it back into the page, never include it in an error. The audit entry
  says *that* a login relay happened, never *what* was relayed.
- **Never auto-submit anything the operator did not type.** The daemon relays; it
  does not decide.
- Session state becomes `needs-auth` while waiting, so it is visible in the list
  rather than silently stuck.

## Lifetimes

| Setting | Value |
|---|---|
| Access assertion clock leeway | 60 seconds, fixed — drift between the edge and this host is real, and anything wider extends every minted token's life in both directions |
| Access key-set refetch floor | 60 seconds between fetch attempts, refetched only on an unknown `kid` |
| Live stream poll interval | 1 second — also the window within which a stream must notice it is no longer authorised |
| Request signature window | 5 minutes |
| Replay cache TTL | 10 minutes |
| Session bearer token TTL | 24 hours — deliberately equal to the absolute lifetime |
| Session idle timeout | 60 minutes, then auto-destroy |
| Session absolute lifetime | 24 hours, no renewal |

Idle and absolute timeouts are enforced by a reaper goroutine, not by the next
request — an abandoned session must die on its own.

**The token TTL must never be shorter than the absolute lifetime.** A shorter token
leaves a window where a live unsandboxed session exists that its owner cannot drive,
read, *or destroy* — only the reaper can end it. There is no renewal and no re-issue:
one token covers exactly one session's life, and then both are gone. If you shorten
one of these two numbers, shorten both.

## Checklist before merging auth or session work

- [ ] All three layers still enforced; none bypassed for a "local" or "health" route
- [ ] Each door still refuses only by the check that applies to it — no browser route
      asks for a signature, no API route consults an assertion
- [ ] The assertion is validated in the daemon: audience pinned, `alg` read only to
      reject, claims parsed only after the signature verifies
- [ ] A non-empty allowlisted email is *required*, so a service-token assertion is
      refused by the dashboard
- [ ] Unobtainable keys refuse rather than admit, and every layer-1 failure answers
      byte-identically
- [ ] No `Access-Control-Allow-*` on any route, swept across every registered one
- [ ] A stream re-evaluates authorisation every tick, never advances the idle clock,
      and never delays teardown or shutdown
- [ ] Constant-time comparison on every secret
- [ ] Body included in the signed payload
- [ ] Replay cache consulted and populated
- [ ] Timestamp checked in both directions
- [ ] Tokens generated with `crypto/rand`, stored hashed, returned once
- [ ] Ownership re-checked server-side on every session-scoped call
- [ ] Teardown clears every item above, kill verified
- [ ] Cross-session isolation test exists and passes
- [ ] No token, secret, or pane content in any log line
