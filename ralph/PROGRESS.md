# Progress

> Append-only notebook. Newest at the bottom. Never edit or delete past entries —
> this is the loop's only memory across fresh contexts.

Each iteration appends:

```
## Iteration N — YYYY-MM-DD HH:MM
**Did:** one or two lines.
**Learned:** anything that would otherwise be rediscovered the hard way.
**Left:** what remains.
**Findings:** problems noticed but not fixed (ad-hoc bugs, smells, risks).
```

Findings are the point of this file as much as progress is. An observation that
dies in a context window is a bug you will pay for twice. Real ad-hoc fixes also
get a one-liner in `docs/fixes-log.md`.

When the whole plan is done and green, append a line containing exactly

---

## Iteration 0 — a second front door

**Did:** Archived milestone 11's notebook. Note that milestone 11 is **not
finished**: T003 through T006 (next_steps, the README rewrite, the Cloudflare
walkthrough, `.env.example`) are still open and carried into this plan's tail.

**Left:** the tasks below.

**The request:** run the dashboard on an internal network without Cloudflare, with
the choice made in the config file — a bool for whether Access is the door, and a
password when it is not.

**Findings, all verified before the plan was written:**

- **Two walls, and the bind is the lesser one.** `loadListen` refuses any
  non-loopback address outright. But even lifted, `cfg.AccessTeamDomain == ""`
  yields `closedDoor{}`, which admits nobody — so the daemon would be reachable
  and refuse everyone. **Cloudflare Access is currently the dashboard's only
  authentication.**
- **Layer 1 is constructed at exactly one place** (`server.go:339`) and returns a
  validator interface. A password door is a third implementation returned from
  that same function — never a special case in the middleware. `closedDoor`'s own
  comment says why: *"a nil validator and a special case in the middleware would
  be the second path, and the second path is the one nobody reads."*
- **`--dev-auth-bypass` is behind a build tag** and absent from released binaries.
  It is not a route to this and must not be reached for.
- **What this daemon runs matters here more than anywhere else.** Every session is
  an unsandboxed shell with `--dangerously-skip-permissions`. A weak door is
  code execution as the operator, for anyone who can reach the port.
