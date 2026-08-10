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

---

## Iteration 1 — 2026-08-10

**Did:** T001. `createFromBrowser` now reads two optional form fields and passes
`Lifetime` and `Idle` into `session.CreateRequest`, so the dashboard can finally
reach the per-session overrides the record, the ceilings, the reaper and the README
have all had since #37.

**Learned — the shape T002 has to submit against:**

- **The fields are `lifetime` and `idle_timeout`**, the same names POST /sessions
  spells in JSON, read from `PostForm` and parsed by that route's own
  `parseLifetimeOverrides` (`internal/httpapi/sessions.go:85`). No second parser
  was written. They carry **Go duration strings** (`72h`, `90m`), and absent means
  the daemon's default — a form submitting neither starts exactly the session this
  door started before.
- **`idle_timeout=0` is the "no idle limit" spelling.** `parseLifetimeOverrides`
  turns a submitted zero into `-1` because zero on the record already means "unset",
  and a negative `Idle` is the disable. So T002's switch can be
  `<input type="checkbox" name="idle_timeout" value="0">` and needs no new parsing
  on either side. A hand-built `idle_timeout=-30m` reaches the same record state;
  both are covered by a test.
- **A new outcome code: `bad-lifetime`** (`outcome.go`), with its sentence in
  `banners` and its spelling added to `spelledOutcomes` in `outcome_test.go` —
  `TestEveryOutcomeThisPackageSpellsHasASentence` counts the map, so a new code
  must be added in both places or the suite fails.
- **"The uniform refusal" in T001 was read as this door's field-level refusal**, not
  as the action gate's 403. A value past a ceiling comes from an operator who was
  admitted and passed the gate; it is the same class of thing as a bad name or a
  forbidden work dir, and those answer with an outcome redirect. A 403 there would
  make the gate's own refusal ambiguous.
- **The ceilings in the httpapi fixture are the constants** — `newSessionFixture`
  never calls `SetLifetimes`, so `maxLifetime` is 24h and `maxIdle` is 60m. That is
  what makes `lifetime=720h` and `idle_timeout=90m` refusable in a unit test with no
  configuration.
- **The fixture's manager clock does not move**, so "idle reaping is off" cannot be
  proven by running a sweep. It is asserted through `IdleDeadline()` — the method
  `expiredAt` compares against — never falling before `AbsoluteDeadline()`, plus
  `DisplayState` still reading `running` an hour past the default idle threshold.
- **Both halves were proven by breaking them.** With the two fields unread the
  carry test fails on the record and the refusal test gets `outcome=created` on
  every row.

**Left:** T002 (the control on the create form), T003 (the deadline on the card),
T004 (the stale comment), T005 (README).

**Findings — noticed, not fixed:**

- **The signed API answers a refused lifetime with a 500.** `refuseCreate`
  (`internal/httpapi/sessions.go:432`) has no `ErrInvalidLifetime` branch, so both
  an unparseable duration and one past a ceiling fall to `default:` →
  `failInternal` → 500 with the internal-error body. `docs/security.md` says a
  field-level refusal is a 400 with the uniform body, and `createReason` already
  carries the sentinel for the trail — the fix is one `case` beside the existing
  `ErrInvalidName, ErrInvalidWorkDir` one. Left alone deliberately: T001 is the
  browser door, and AR-008 forbids the reach. **Worth a fix-lane line of its own.**
- **`internal/httpapi/render.go` is not `gofmt` clean on this branch** — its import
  block has `internal/buildinfo` above the stdlib imports, and `gofmt -l .` names
  the file. It is untouched by this task and pre-existing. Nothing catches it:
  `golangci-lint run` reports 0 issues and no CI workflow runs `gofmt` or
  `goimports`, so the `AGENTS.md` format command is the only thing that would, and
  only for a file someone happens to edit.
