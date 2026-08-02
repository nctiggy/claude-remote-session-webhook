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

There are **two front doors**, and each needs its own answer to "who is this?":

| Door | Caller | Authenticated by |
|---|---|---|
| Web dashboard | A browser, operated by a human | Cloudflare Access (Google IdP) → signed JWT header, validated by the daemon |
| API | The companion Claude skill, or any script | HMAC-SHA256 request signature |

Neither door is allowed to skip the daemon-side check. Cloudflare Access failing
open, or a tunnel misconfiguration, must not become an unauthenticated session.

## Non-negotiables

### 1. Never trust the request
Every request is authenticated **and** authorized on the server, on every call. There
is no client to trust — the Claude skill is just another HTTP caller, and anyone can
write one.

```go
// Every handler starts this way. No exceptions, no "internal" routes.
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
- `.env` is gitignored; `.env.example` documents the *names* only.
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

| Header | Value |
|---|---|
| `Content-Security-Policy` | `default-src 'none'; script-src 'self'; style-src 'self'; connect-src 'self'; img-src 'self' data:; base-uri 'none'; form-action 'self'; frame-ancestors 'none'` |
| `Strict-Transport-Security` | `max-age=31536000; includeSubDomains` |
| `X-Content-Type-Options` | `nosniff` |
| `Referrer-Policy` | `no-referrer` |

No `unsafe-inline`, no CDN. htmx and the stylesheet are served from `self`, embedded
in the binary via `go:embed`. If a change needs `unsafe-inline` to work, the change
is wrong.

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
  and unbounded spawning is a local denial of service.
- Cap concurrent sessions; refuse past the cap rather than degrading the host.
- **Every request is audited**: timestamp, caller ID, action, session ID, decision.
  Audit records carry no prompt text and no session output.

## Pre-merge security checklist

- [ ] No secret added to the repo (CI + push protection verify this)
- [ ] Every new endpoint enforces authn **and** ownership server-side
- [ ] Session IDs unguessable; unauthorized access returns 404, not 403
- [ ] Input decoded into a typed struct with unknown fields rejected
- [ ] No shell string built; `exec.Command` with argv only
- [ ] Any caller-supplied path allowlisted and symlink-resolved
- [ ] Auth failures are uniform and leak no detail about which check failed
- [ ] No session output, prompt, or token in any log line
- [ ] Listener still bound to loopback
- [ ] No new dependency without justification
