# Auth & Sessions

> Loaded when: touching request authentication, tokens, or the session lifecycle.

In this project "session" means **a Claude Code session running in a tmux window**,
and "auth" means **proving a webhook request is allowed to create or drive one**.
There are no browser sessions and no human login form.

Treat this file as a correctness spec, not a style guide. A bug here is host
compromise — see `docs/security.md` for why.

## Three layers, each independently sufficient to say no

| Layer | Enforced by | Stops |
|---|---|---|
| 1. Network | Cloudflare Tunnel + Access | Anyone who is not on the allowed identity list reaching the daemon at all |
| 2. Request | HMAC-SHA256 signature + timestamp | A forged or replayed request from something that got past layer 1 |
| 3. Session | Per-session bearer token | A valid caller driving a session that is not theirs |

Never collapse these. Layer 1 is not a substitute for layer 2: if the tunnel is
misconfigured, or the daemon is ever reached over loopback by another local process,
the signature is what is left.

## Layer 1 — Cloudflare Access (the browser's door)

Google is the identity provider, configured in Cloudflare Access with an allowlist
of exactly one email. Access enforces login at the edge, so an unauthenticated
browser never reaches the host at all.

Access then forwards a signed JWT in `Cf-Access-Jwt-Assertion`. **The daemon must
validate it.** A header is trivially forgeable by anything that can reach the
listener; the signature is what makes it evidence.

```go
// Verify against Cloudflare's public keys for this Access application.
// Cache the JWKS; refetch on unknown kid, never per request.
tok, err := jwt.Parse(r.Header.Get("Cf-Access-Jwt-Assertion"),
    a.jwks.Keyfunc,
    jwt.WithAudience(a.cfg.AccessAUD),          // AUD tag pins THIS application
    jwt.WithIssuer(a.cfg.AccessTeamDomain),     // https://<team>.cloudflareaccess.com
    jwt.WithValidMethods([]string{"RS256"}),    // never accept "none" or HS*
)
if err != nil {
    return nil, fmt.Errorf("access jwt: %w", err)
}
if !a.cfg.AllowedEmails[claimEmail(tok)] {      // allowlist again, on our side
    return nil, errors.New("email not allowed")
}
```

Non-negotiable:
- **Pin the audience.** Without `aud`, a JWT minted for any other Access app in the
  same team validates here.
- **Pin the algorithm.** Accepting `alg` from the token is the classic JWT break.
- **Re-check the email allowlist in the daemon.** Access is the gate; this is the
  assertion that the gate was configured the way you think.

**Local development** uses `--dev-auth-bypass`, which skips layer 1 only. It must:
refuse to start unless the listener is loopback, log a loud warning on every
request, and be excluded from release builds by a `//go:build dev` tag. A production
binary that can bypass auth via a flag is a backdoor.

## Layer 2 — request signing

Every request carries `X-CRSW-Timestamp` (Unix seconds) and `X-CRSW-Signature`
(`sha256=` + hex HMAC over `timestamp + "." + rawBody`).

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

    mac := hmac.New(sha256.New, a.secret)
    fmt.Fprintf(mac, "%d.", ts)
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

## The session-isolation rule (non-negotiable)

**Output from session A must never be reachable through a request scoped to session
B.** This is the analogue of the session-bleed bug in web apps, and it is worse here:
pane content can contain anything on the host.

Every read path takes the session ID from the *authenticated, ownership-checked*
session record — never from a caller-supplied tmux target string.

**Test for it.** A passing test looks like: create session A → produce distinctive
output → create session B as a different caller → assert every read endpoint scoped
to B returns nothing from A, and that B's token on A's ID returns 404.

## Session teardown must be complete

Destroying a session clears **all** of:

- [ ] The tmux session (`tmux kill-session`), verified gone — not assumed
- [ ] The child Claude process (reaped; no orphan holding a pty)
- [ ] The session record and its token hash
- [ ] Any buffered or cached pane output
- [ ] Any temp working directory the daemon created for it
- [ ] Any open SSE/websocket stream tailing that session

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
| Request signature window | 5 minutes |
| Replay cache TTL | 10 minutes |
| Session bearer token TTL | 12 hours |
| Session idle timeout | 60 minutes, then auto-destroy |
| Session absolute lifetime | 24 hours, no renewal |

Idle and absolute timeouts are enforced by a reaper goroutine, not by the next
request — an abandoned session must die on its own.

## Checklist before merging auth or session work

- [ ] All three layers still enforced; none bypassed for a "local" or "health" route
- [ ] Constant-time comparison on every secret
- [ ] Body included in the signed payload
- [ ] Replay cache consulted and populated
- [ ] Timestamp checked in both directions
- [ ] Tokens generated with `crypto/rand`, stored hashed, returned once
- [ ] Ownership re-checked server-side on every session-scoped call
- [ ] Teardown clears every item above, kill verified
- [ ] Cross-session isolation test exists and passes
- [ ] No token, secret, or pane content in any log line
