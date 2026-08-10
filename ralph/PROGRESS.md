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

## Iteration 0 — let a session outlive the defaults

**Did:** Archived milestone 9, opened a fresh notebook.

**Left:** five tasks. The operator: *"I like leaving these sessions running forever
if I want to. 1 hour is way too tight"* and *"I just want to be able to allow
sessions to never die if I choose."*

**Findings, all verified:**

- **There are two clocks, and this matters more than anything else here.**
  `AbsoluteLifetime` (24h from creation, never renewed) and `IdleTimeout` (60m from
  last activity, moves with it). Turning off idle does **not** make a session
  immortal — the absolute deadline still fires. Any task that claims to deliver
  "never dies" while only touching idle has not read this.
- **The per-session override already exists and is already bounded.**
  `CreateRequest.Lifetime` and `.Idle`, resolved by `resolveLifetimes`, with a
  negative `Idle` meaning *idle reaping off for this session* — explicitly safe
  because the absolute deadline still applies, so the bound is relaxed rather than
  removed.
- **The browser cannot reach any of it.** `internal/httpapi/sessions.go:381` (the
  signed API) passes `Lifetime` and `Idle`. `internal/httpapi/actions.go:455` (the
  browser create) passes Owner, Name, WorkDir and StartCommand — and nothing else.
  **This is the fifth "code with no caller" in this repository**, after the reaper,
  `Store.Touch`, the PR-opener and `CRSW_DESTROY_ON_SHUTDOWN`. `CRSW_IDLE_TIMEOUT_MAX`
  is documented as "the ceiling for a per-session idle override" — a ceiling on
  something the dashboard cannot do.
- **`loadDuration` has no upper bound**, so a very large lifetime already parses.
  There is no "never" sentinel and the README says so deliberately.
- **Watching deliberately does not advance the idle clock.** `View`'s comment:
  *"Watching is not driving (FR-034f). The property holds by construction — there is
  no clock reading in this method to hand to Touch."* The operator noticed this
  ("it has no idea if I am connected"). **Do not change it in this milestone.** A
  forgotten tab holding an unsandboxed shell open forever is a worse failure than an
  explicit per-session choice, and the choice is what these tasks deliver.
- **A stale comment to fix:** `internal/session/session.go:15` still says the two
  lifetimes "are constants rather than configuration on purpose: an operator who
  could widen them could widen the blast radius." They became configurable. That is
  the same class of defect milestone 8 spent five tasks closing.
