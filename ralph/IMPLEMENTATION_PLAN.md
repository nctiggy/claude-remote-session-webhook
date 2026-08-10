# Implementation Plan

**Milestone 10 — Let a session outlive the defaults.**

> *"I like leaving these sessions running forever if I want to. 1 hour is way too
> tight if we leave those as defaults."*
> *"I just want to be able to allow sessions to never die if I choose."*

Five tasks.

---

## ⚠️ There are TWO clocks. Read this before touching anything.

| Clock | From | Default | Per-session override |
|---|---|---|---|
| **Idle** | `LastActivity`, moves with it | `60m` | `Idle` — a **negative** value turns it off |
| **Absolute** | `CreatedAt`, **never renewed** | `24h` | `Lifetime` — no "never", but no upper cap either |

**Turning off idle does not make a session immortal.** The absolute deadline still
fires. A task that claims to have delivered "never dies" while only touching idle
has not read this table.

**Why the asymmetry is deliberate and must survive**: a negative `Idle` is safe
*because* the absolute deadline still applies — the bound is relaxed, not removed.
A negative `Lifetime` would remove it, and `resolveLifetimes` refuses it.

**How "effectively never" is reached**: the operator raises the ceilings
(`session_lifetime_max`, `idle_timeout_max`) in settings, and a session opts in up
to that ceiling. `loadDuration` has no upper bound, so a very large duration
already parses. **The operator's ceiling stays the bound** — that is what keeps
Principle VI true while giving the per-session freedom that was asked for.

---

## ⚠️ This is the fifth "code with no caller"

`internal/httpapi/sessions.go:381` — the signed API — passes `Lifetime` and `Idle`.
`internal/httpapi/actions.go:455` — **the browser create** — passes Owner, Name,
WorkDir, StartCommand, and nothing else.

So the per-session override exists, is tested, is bounded, is documented — and the
dashboard cannot reach it. `CRSW_IDLE_TIMEOUT_MAX`'s README line calls itself "the
ceiling for a per-session idle override", which is a ceiling on something the
surface the operator actually uses cannot do.

After the reaper, `Store.Touch`, the PR-opener and `CRSW_DESTROY_ON_SHUTDOWN`, this
is the fifth. **T001 is done when the browser can set it, not when the field is
assigned.**

---

## ⚠️ Do NOT make watching advance the idle clock

The operator noticed the behaviour — *"it seems it has no idea if I am connected or
not"* — and they are right about what happens: `View` never touches, the live
stream calls `View` in a loop, so an open stream keeps nothing alive.

**That is deliberate.** `manager.go`, above `View`:

> The idle clock is *not advanced*, which is the whole reason this is a second
> method rather than a flag on Resolve. **Watching is not driving (FR-034f).** The
> property holds by construction — there is no clock reading in this method to hand
> to Touch.

**Leave it alone.** A forgotten browser tab holding an unsandboxed shell open
forever is a worse failure than an explicit per-session choice, and the explicit
choice is what this milestone delivers. If it is revisited later it needs its own
spec and its own argument, not a line added inside a task about something else.

---

## Conventions

- `- [ ]` open · `- [x]` done · `- [!]` blocked (reason in `PROGRESS.md`)
- Priority order is meaningful — the loop always takes the topmost open item.
- Each task must be independently completable **and verifiable** in one iteration.
- **Every task ends green**: `go build ./... && go vet ./... && go test ./... && golangci-lint run`.
  Add `-tags tmux` when the task touches tmux and `-tags quickstart` when it touches
  `cmd/crswd`.
- **Check the linter is v2 before trusting it** (#26).
- `go.sum` must never appear.
- **AR-005: a test satisfies the cross-site checks, it never disables them.**
- **AR-008: no refactoring outside the task.**
- **A task is not done when the code exists. It is done when something calls it.**
  This milestone exists because that rule was broken a fifth time.
- **A new guard must be proven by breaking it.**
- **Everything works with no JavaScript.**

---

## Tasks

- [x] **T001** 🔒 Let the browser create set `Lifetime` and `Idle`, in `internal/httpapi/actions.go` (the `CreateRequest` at ~line 455). The values are **submitted by the operator and therefore untrusted**: they must pass through `resolveLifetimes`' ceilings exactly as the signed API's do, and a request asking beyond the ceiling is refused with the uniform refusal rather than clamped silently. Reuse the signed path's parsing if it is reachable; do not write a second parser with different rules. Test that a create beyond the ceiling is refused, that a negative idle is accepted and disables idle reaping, and that a negative **lifetime** is still refused.

- [ ] **T002** Put the control on the create form in `web/templates/partials/create-form.html`. The operator's word for it is "never die", so the control should say what it does in those terms and be honest that the absolute lifetime still applies unless the operator has raised it. Reuse `.field`, `.field-switch`, `.switch-input`, `.switch-label` — **introduce no new class**. Assert against the rendered markup that the control exists and submits what T001 reads, because a control that renders and submits nothing is exactly the failure this milestone is about.

- [ ] **T003** Show a session's deadline on its card, in `web/templates/partials/session-card.html`. An operator who cannot see when a session dies cannot tell that their choice took effect — and this milestone's whole subject is a clock nobody could see. Use the existing `.card-meta` list and its `dt`/`dd` shape; **no new class**. Say "no idle limit" rather than a far-future timestamp when idle reaping is off for that session.

- [ ] **T004** Fix the stale comment at `internal/session/session.go:15`. It says the two lifetimes "are constants rather than configuration on purpose: an operator who could widen them could widen the blast radius the constitution bounds by construction." They are configurable — `CRSW_SESSION_LIFETIME` and `CRSW_IDLE_TIMEOUT` — and the constants are now the fallback when nothing is configured. Describe what is actually true: the constants are defaults, the operator's configuration sets the ceiling, and the per-session override operates under that ceiling.

- [ ] **T005** Update `README.md`'s configuration table and any prose that describes the lifetimes. The four keys' descriptions should say that the ceilings bound a per-session choice **the dashboard can now make**, and how an operator who wants effectively-never sets them. Keep the honest note that there is no "never" sentinel and say what to do instead.

---

## Out of scope

- **Making a live stream advance the idle clock.** See the warning above.
- **A "never" sentinel for the absolute lifetime.** Removing that bound entirely is
  a different decision from relaxing it, and it needs its own argument.
- **#120, #121.** Unchanged.
- **Q2.** Still the operator's to answer.
