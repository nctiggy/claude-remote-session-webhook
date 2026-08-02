# Auth & Sessions

> Loaded when: touching login, logout, tokens, sessions, or anything user-scoped.

Session bugs are the ones that leak *other people's data*. Treat this file as a
correctness spec, not a style guide.

## The session-bleed rule (non-negotiable)

**Logout must fully clear state. Signing in as user B must never show any trace of
user A.**

This is the single most common serious bug in AI-assisted apps: the token is
cleared but a cache, store, or memoised query still holds the previous user's data.

On sign-out, clear **all** of:

- [ ] Auth token / session cookie (server-side invalidation too, not just client)
- [ ] In-memory state stores (Redux/Zustand/Pinia/context)
- [ ] Query caches (React Query / SWR / Apollo — `clear()`, not just invalidate)
- [ ] `localStorage` **and** `sessionStorage` keys owned by the app
- [ ] Any IndexedDB / offline store
- [ ] Any in-flight requests (abort them)
- [ ] Any websocket / SSE connection

```
<FILL IN: canonical signOut() implementation that does all of the above>
```

**Test for it.** A passing test looks like: log in as A → load data → sign out →
log in as B → assert nothing from A is reachable in UI or cache.

## Token storage

`<FILL IN: chosen strategy>`

Guidance, strongest first:
1. **httpOnly, Secure, SameSite cookie** — preferred. Not readable by JS, so XSS
   cannot exfiltrate it.
2. In-memory (lost on refresh; pair with a refresh cookie).
3. `localStorage` — only with eyes open: any XSS is a full account takeover.

Never put a token in a URL, a query string, or a log line.

## Session lifetime

| Setting | Value |
|---|---|
| Access token TTL | `<FILL IN>` |
| Refresh token TTL | `<FILL IN>` |
| Idle timeout | `<FILL IN>` |
| Absolute timeout | `<FILL IN>` |

Rotate refresh tokens on use. Detect reuse of a rotated token and revoke the family.

## Route protection

Client-side guards are UX, **not security**. Every protected route must also be
enforced server-side on every request. See `security.md`.

```
<FILL IN: canonical guard + server-side check>
```

## Checklist before merging auth work

- [ ] Sign-out clears every item in the list above
- [ ] Session invalidated server-side, not just client-side
- [ ] No token in URL, log, or error message
- [ ] Protected endpoints re-check identity server-side
- [ ] A→B account-switch test exists and passes
