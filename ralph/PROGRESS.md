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

## Iteration 0 — the config an install actually leaves behind

**Did:** Archived milestone 12.

**Left:** the tasks below. The operator: *"The config that is written during
install is not the complete example. It is very basic and missing a lot of
options."*

**Findings:**

- **The installer writes two settings. The daemon understands twenty-three.**
  An operator installing today gets `shared_secret` and `allowed_roots` and no
  sign that `listen`, `dashboard_password`, `max_sessions`, the lifetimes, the
  start commands or anything else exists.
- **`.env.example` is held to the code in both directions** by
  `TestEnvExampleNamesEveryVariable` — a variable the code reads that it never
  names fails, and a variable it names that nothing reads fails too. **The
  installer's config template is guarded by nothing**, which is exactly why it
  drifted to two of twenty-three while `.env.example` stayed complete.
- **The unit is already shipped as a release asset**, fetched, checksummed and
  verified by the installer alongside the tarball. A config template follows that
  path exactly rather than inventing a second one, and the same signature covers
  it.
- **The shape to copy is `.env.example`'s, not its content.** That file is
  `NAME=value` for an environment; the config file is `key = value`. Same
  discipline, different spelling — and the two must not drift from each other
  either.
