# Security

> Loaded when: handling user input, authorization, secrets, or routes.
> Referenced by `.specify/memory/constitution.md` as a hard gate.

## Non-negotiables

### 1. Never trust the client
Every authorization decision happens **server-side**, on every request. A hidden
button is not access control.

```
<FILL IN: canonical server-side authz check>
```

Check **ownership**, not just authentication. "Is this user logged in?" is not the
same question as "does this user own record 4172?" — the second one is the one that
matters (IDOR is the most common real-world break).

### 2. Sanitize input, encode output
- Validate at the boundary, against a schema. Reject unknown fields.
- Parameterized queries only. String-concatenated SQL is never acceptable.
- Escape on output by context (HTML vs attribute vs URL vs JS).
- Never `innerHTML` / `dangerouslySetInnerHTML` with user data. If unavoidable,
  sanitize with a maintained library and document why.

```
<FILL IN: canonical validation example>
```

### 3. Secret hygiene
- Secrets come from the environment or a secret manager. Never a source file.
- `.env` is gitignored; `.env.example` documents the *names* only.
- Never log a secret, token, or full request body containing one.
- Rotate anything that has ever been committed — assume it is public forever.
- GitHub secret scanning + push protection are enabled on this repo.

### 4. Dependencies
- Dependabot is enabled. Security updates get merged promptly, not eventually.
- New dependency requires justification: what does it do that stdlib cannot?

## Headers & transport

`<FILL IN>` — baseline:

| Header | Value |
|---|---|
| `Content-Security-Policy` | `<FILL IN>` |
| `Strict-Transport-Security` | `max-age=31536000; includeSubDomains` |
| `X-Content-Type-Options` | `nosniff` |
| `Referrer-Policy` | `strict-origin-when-cross-origin` |

HTTPS everywhere. No mixed content.

## Pre-merge security checklist

- [ ] No secret added to the repo (CI + push protection verify this)
- [ ] All new endpoints enforce authn **and** authz server-side
- [ ] Ownership checked, not just authentication
- [ ] User input validated against a schema at the boundary
- [ ] No raw HTML injection path introduced
- [ ] No new dependency without justification
- [ ] Errors returned to users leak no internals (stack traces, SQL, paths)
