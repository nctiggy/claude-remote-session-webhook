# Security

> Loaded when: handling request input, authorization, secrets, or routes.
> Referenced by `.specify/memory/constitution.md` as a hard gate.

## The threat model, stated plainly

This daemon spawns Claude Code sessions with `--dangerously-skip-permissions` on the
host machine. **A request that passes authentication is arbitrary code execution as
the daemon's user.** There is no sandbox behind the auth check to catch a mistake.

That single fact ranks every decision in this file. A bug in the auth path is not a
vulnerability in a feature — it is total host compromise.

Assume the endpoint is being scanned. It is on the public internet under a
predictable hostname.

There are **two front doors**, on one hostname, behind one Access application with two
edge policies. **No path gets an edge bypass**, and each door still needs its own
answer to "who is this?" behind the edge:

| Door | Caller | Admitted at the edge by | Checked by the daemon |
|---|---|---|---|
| Web dashboard | A browser, operated by a human | The identity policy: Google IdP, one allowlisted address | Layer 1 — the forwarded `Cf-Access-Jwt-Assertion` is genuine, is the **identity** shape, and names an allowlisted email. A route that **changes** something adds the action gate below |
| API | The companion Claude skill, or any script | An Access **service token**, sent as `CF-Access-Client-Id` + `CF-Access-Client-Secret` | HMAC-SHA256 signature, timestamp, replay, per-session token. The assertion is ignored entirely |

The two shapes are not interchangeable. A service token's assertion carries
`common_name`, an empty `sub`, and **no email** — so "no email" must never read as
"allow", or every API call the operator makes is also admitted to the dashboard. A
service-token assertion presented to the browser door is refused exactly as a
stranger's is.

Neither door is allowed to skip the daemon-side check. Cloudflare Access failing
open, or a tunnel misconfiguration, must not become an unauthenticated session.

**Each door refuses only by the check that applies to it.** A browser request is never
refused for carrying no signature, and an API request is never refused for carrying no
identity — adding either check to the other door breaks a working client to enforce
something that door does not use. Both doors resolve to one owner by construction, so
the ownership check below runs on every request through either.
[`docs/auth-and-sessions.md`](./auth-and-sessions.md) has the full layering.

## Non-negotiables

### 1. Never trust the request
Every request is authenticated **and** authorized on the server, on every call. There
is no client to trust — the Claude skill is just another HTTP caller, and anyone can
write one.

```go
// Every API handler starts this way. No exceptions, no "internal" routes.
func (s *Server) handle(w http.ResponseWriter, r *http.Request) {
    caller, err := s.auth.Verify(r) // signature + timestamp + replay check
    if err != nil {
        // Uniform response: never reveal which check failed.
        http.Error(w, "unauthorized", http.StatusUnauthorized)
        s.log.Warn("auth rejected", "remote", r.RemoteAddr, "reason", err)
        return
    }
    sess, err := s.sessions.Get(sessionIDFrom(r))
    if err != nil || sess.Owner != caller.ID { // ownership, not just authn
        http.Error(w, "not found", http.StatusNotFound) // 404, not 403 — no enumeration
        return
    }
    ...
}
```

That is the API door. The browser door asks the same two questions with the other
door's credential — validate the assertion, then check ownership — and answers with
the same uniform refusal. What changes is the credential, never the questions, and
neither door has a route exempt from either.

A browser route that **changes** something asks two more before either of them, and
they are not about who the caller is. The browser's credential is an ambient cookie,
so "the operator's identity was verified" and "the operator asked for this" are
different facts; the second is what the action gate establishes. See
[`docs/auth-and-sessions.md`](./auth-and-sessions.md) for the rule in full — the short
version is layer 1, then `Sec-Fetch-Site` (**absent refuses here**), then a stateless
page token bound to the verified identity, all before the handler runs and therefore
before anything changes.

Check **ownership**, not just authentication. "Is this caller authenticated?" is not
the same question as "does this caller own session `a3f9`?" Session IDs must be
unguessable (`crypto/rand`, ≥128 bits) and must never be sequential.

### 2. Validate every input at the boundary
Decode into a typed struct with `DisallowUnknownFields`. Reject anything unexpected.

```go
func decode[T any](r *http.Request) (T, error) {
    var v T
    dec := json.NewDecoder(io.LimitReader(r.Body, maxBodyBytes))
    dec.DisallowUnknownFields()
    if err := dec.Decode(&v); err != nil {
        return v, fmt.Errorf("malformed body: %w", err)
    }
    return v, validate(v) // struct tags; every field constrained
}
```

Specific to this daemon:
- **Never build a shell string.** Use `exec.Command` with an argv slice. No
  `sh -c`, no `fmt.Sprintf` into a command line.
- **`tmux send-keys` takes the prompt as a single literal argument**, never
  interpolated into a shell. Treat prompt text as hostile bytes.
- **Working directories are allowlisted**, resolved with `filepath.Clean` +
  `filepath.EvalSymlinks`, then checked to be under an approved root. A caller
  does not get to name an arbitrary path.
- **Session names are `^[a-zA-Z0-9-]{1,64}$`.** They become tmux target strings;
  a name containing `:` or `.` addresses a different window.

### 3. Secret hygiene
- Secrets come from the environment or 1Password. Never a source file.
- The daemon's own file is `~/.config/crswd/config`, and it must be mode 0600 —
  group- or world-readable is a **startup failure**, because a secret in a
  mode-0644 file has already leaked to every account on the host. That refusal is
  the reason the file beats an `Environment=` line, which `systemctl show` prints
  to anyone who asks. `config.example` is committed and documents the *keys* only.
- **A config parse error never quotes the line.** A malformed line may be the
  secret with a typo in it, and a startup error lives in the journal forever.
- **Session output is secret.** Captured pane content can contain anything on the
  host — keys, tokens, customer data. Never log it, never ship it to telemetry,
  never include it in an error message.
- Never log the shared secret, a bearer token, or a full signed body.
- Rotate anything ever committed — assume it is public forever.
- GitHub secret scanning + push protection are enabled on this repo.

### 4. Fail closed
Any error in the auth path denies the request. A missing config value that would
weaken auth is a **startup failure**, not a warning:

```go
if cfg.SharedSecret == "" {
    return fmt.Errorf("CRSW_SHARED_SECRET is required; refusing to start")
}
```

A daemon that starts with auth disabled is worse than one that does not start.

### 5. Dependencies
- Dependabot is enabled. Security updates merge promptly, not eventually.
- A new dependency needs justification: what does it do that stdlib cannot?
  The HTTP server, HMAC, and JSON handling are all stdlib. Keep it that way.

## Transport & exposure

The daemon binds **`127.0.0.1` only**. It is never reachable directly; a Cloudflare
Tunnel connects outbound and brokers `*.example.com` traffic to the loopback
listener. No inbound port is opened on the host or the router.

```
internet → Cloudflare edge (TLS, Access) → tunnel (outbound) → 127.0.0.1:PORT
```

If a change would make the daemon listen on `0.0.0.0`, that change is wrong.

### Response headers

Every **browser-door** response carries all of these — pages, static assets, refusals,
and the not-found page alike. A refusal that carried a different set would be a refusal
that tells a stranger which paths this daemon really serves:

| Header | Value |
|---|---|
| `Content-Security-Policy` | `default-src 'none'; script-src 'self'; style-src 'self'; connect-src 'self'; img-src 'self' data:; base-uri 'none'; form-action 'self'; frame-ancestors 'none'` |
| `Strict-Transport-Security` | `max-age=31536000; includeSubDomains` |
| `X-Content-Type-Options` | `nosniff` |
| `Referrer-Policy` | `no-referrer` |
| `Cache-Control` | `no-store` — with exactly one exemption, below |

No `unsafe-inline`, no CDN. The stylesheet and the dashboard's script are served from
`self`, embedded in the binary via `go:embed`; a client-side library would be too, and
never from a CDN. If a change needs `unsafe-inline` to work, the change is wrong.

`no-store` is the default rather than the exception, because a page, a stream, and an
authorisation decision are all things a cached copy outlives: pages carry session
names, working directories, and pane content, all secret under §3. **The two embedded
static assets are the only exemption** — they hold no session data, and they are served
`no-cache` with an entity tag rather than a lifetime, so a browser revalidates instead
of running the previous binary's script against this one's markup.

The API door's responses gain none of these headers. Every byte a milestone 1 client
sees is frozen, and a header is part of a response.

### The header that must never appear

| Header | Rule |
|---|---|
| `Access-Control-Allow-*` | **Never sent. Any of them, on any route, on either door.** |

This is load-bearing, not hygiene. The browser's credential is a cookie and therefore
**ambient**: it rides on requests a hostile third-party page triggers, and the edge
converts those into a valid assertion the daemon accepts. Same-origin policy is the
only thing stopping that page from *reading* the response, and it holds exactly as long
as the daemon never opts out of it. The per-session bearer token would have provided
this a second way — a header credential forces a preflight, a cookie does not — but a
browser stream cannot carry one, so the absence *is* the protection.

**Assert the absence.** Sweep every registered route on both doors and require zero
such headers. An absence nothing checks is one convenience header away from gone, and
there is deliberately no CORS helper in the codebase to reach for.

The request side of the same rule: a live output stream is refused when the browser
itself reports a cross-site initiator — `Sec-Fetch-Site` present and anything other
than `same-origin`. **Present-and-wrong refuses; absent does not**: browsers send the
header and non-browser clients do not, so requiring it would refuse the callers it was
never about while adding nothing against the one it is. The refusal is decided before
any session is looked up, and the header's value is caller-authored text that never
reaches a log line or an audit record.

**On a route that changes something, absent refuses too.** Same check, same code, one
addition — and the difference is the argument above running out. The only legitimate
caller of an action route is a form this daemon rendered, submitted by a browser, which
always sends the header; a script that wants to change something uses the API door and
its signature. An absent header is not evidence of same-origin initiation, and treating
it as such makes the check optional for anything that can omit it. It is still only
half the defence: the other half is the page token, because a header a future proxy
could strip must not be the whole of it.

## Rendering session output (the one XSS surface)

Everything a Claude session prints — file contents, command output, error text, a
web page it fetched — reaches the dashboard. **All of it is untrusted.**

```go
// html/template escapes by default. Keep pane output as a plain string and let
// the template engine do its job.
{{ .PaneText }}                    // correct: escaped text node
{{ .PaneText | safeHTML }}         // NEVER. There is no valid reason for this.
```

- Never `template.HTML`, `template.JS`, or any `safeHTML`-style helper on pane
  content. Not for ANSI colour, not for links, not for formatting.
- On the client, `textContent` only. Never `innerHTML`, never `hx-swap` a raw pane
  payload into the DOM as markup — stream it into a `<pre>` as text.
- Strip ANSI escape sequences server-side before rendering. If colour is wanted
  later, map codes to CSS classes from an allowlist; never pass the raw bytes through.
- The same applies to session names and working directories. A caller picks those,
  and the regex in §2 is what keeps them boring.

## Rate limiting & audit

- Per-caller rate limit on session creation. Spawning Claude sessions is expensive
  and unbounded spawning is a local denial of service. **Both doors create**, so the
  browser's create calls the same limiter and the same validation rather than a second
  copy of either.
- Cap concurrent sessions; refuse past the cap rather than degrading the host.
- **Every request is audited**: timestamp, caller ID, action, session ID, decision.
  Audit records carry no prompt text and no session output.
- A browser action is audited under its own name — `dashboard.create`,
  `dashboard.destroy`, `dashboard.rename`, `dashboard.compact` — and a request the
  action gate refused under `dashboard.reject`, deliberately not `access.reject`: an
  identity that got in and *then* failed the cross-site check is a different and more
  alarming event than one that never got in, and an operator counting one must not be
  counting the other with it.
- **What was delivered is never recorded.** The compact's own text is a constant the
  daemon sends into a session; the record says the action happened, never its content.
  Every reason on the trail is a sentinel this codebase authored, so a record can
  never carry a byte the caller chose.

## Pre-merge security checklist

- [ ] No secret added to the repo (CI + push protection verify this)
- [ ] Every new endpoint enforces authn **and** ownership server-side
- [ ] Session IDs unguessable; a session another caller owns, or one that never
      existed, returns 404 and not 403 — the 403 is the action gate's answer to a
      request it would not accept at all, and the two never stand in for each other
- [ ] Any new mutating browser route goes through the action gate, not just layer 1,
      and both halves of that gate are tested with the other satisfied
- [ ] Input decoded into a typed struct with unknown fields rejected
- [ ] No shell string built; `exec.Command` with argv only
- [ ] Any caller-supplied path allowlisted and symlink-resolved
- [ ] Auth failures are uniform and leak no detail about which check failed
- [ ] No session output, prompt, or token in any log line
- [ ] Listener still bound to loopback
- [ ] No new dependency without justification
- [ ] No `Access-Control-Allow-*` on any route, either door — swept, not assumed
- [ ] Browser-door responses all carry the header set above, refusals included
