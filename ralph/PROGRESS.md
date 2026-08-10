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

---

## Iteration 2 — 2026-08-10

**Did:** T002. The create form now carries a second switch —
`<input type="checkbox" name="idle_timeout" value="0">`, labelled "Never die when
idle" — so the override T001 taught the handler to read is something an operator
can actually say. Beside it, a `.field-hint` naming the clock that still ends the
session.

**Learned — what the next iteration would otherwise rediscover:**

- **Adding a checkbox to the create form breaks `TestCreateFormRendersRemoteSwitch`**
  (`partials_test.go`). It counted *every* checkbox in the render and fatally
  failed at 2, with a message about the mode being one two-state control. The
  count was a proxy for "one control per mode", so it now counts the ones posting
  `remote_control`. It keeps the **literal** name rather than `fieldRemoteControl`
  deliberately — a rename that edited the constant and the template together would
  pass a test written against the constant while browsers posted a field the
  daemon does not read. The new test uses the constant instead, because its job is
  the opposite one: holding the template's second spelling to what `actions.go`
  reads.
- **`.switch-label` is uppercase and letter-spaced** (`crswd.css:817`) — it is the
  design system's *label role*, not body text. Prose set in it shouts. The honest
  sentence therefore lives in a `.field-hint`/`.field-hint-text` named by
  `aria-describedby`, which is exactly the arrangement the working-directory
  field's roots hint already uses. No new class was needed for any of it.
- **`.field-switch` is `grid-auto-flow: column`**, so a hint placed inside the
  switch row becomes a third *column* beside the box. The row and its hint are
  wrapped in an outer `.field` instead; `.field` nests without trouble (grid, gap
  `--s1`, `inline-size: min(100%, --card-min)`), and the coarse-pointer
  `min-block-size: var(--tap)` still lands on the row that carries the class.
- **The hint names no number.** What the absolute lifetime is here is
  configuration — `session_lifetime`, default 24h — so copy saying "24 hours"
  would be false on any install that set it. It points at settings instead, which
  is reachable: `config.Editable` is `!IsSecret && VarForKey != ""`, and
  `session_lifetime` is in `file.go`'s key list.
- **Proven by breaking it, three ways**: `value="30m"` (parses to a *positive*
  idle — the control would claim something it does not do), the
  `aria-describedby` removed, and the field renamed to `idle`. Each fails with the
  sentence written for it. The value arm is the one worth keeping: it feeds the
  markup's value to `parseLifetimeOverrides` itself rather than asserting the
  string `"0"`, so what is pinned is that the submission *disables reaping*.
- **`go test -tags quickstart ./cmd/crswd` passes here** (36s) — tmux, jq and
  `127.0.0.1:8765` were all available. Worth running for a template change: the
  acceptance suite renders the real dashboard.

**Left:** T003 (the deadline on the card), T004 (the stale comment at
`internal/session/session.go:15`), T005 (README).

**Findings — noticed, not fixed:**

- **Both findings from iteration 1 still stand and neither was touched.** The
  signed API's 500 on a refused lifetime (`refuseCreate`,
  `internal/httpapi/sessions.go:432`) is still a fix-lane line of its own, and
  `internal/httpapi/render.go` is still the one file `gofmt -l .` names.
- **The form can turn the idle clock off but cannot set a lifetime.** T001 reads
  both `lifetime` and `idle_timeout`; this task put a control on only the second,
  because that is the one the plan specified and the one the operator's words
  describe. So the absolute lifetime a browser-created session gets is always the
  daemon's default — an operator who wants a longer one raises `session_lifetime`
  in settings, which is what the hint tells them. Whether the form should also
  offer a per-session lifetime entry is a real question and **not one this
  milestone answers**; it would need `.field-label`/`.field-input`, a duration the
  ceiling can refuse, and copy about what happens when it does.
- **`docs/components.md`'s Switch section had gone stale in the making.** It
  opened "Today there is exactly one on the dashboard", which this task made
  false, and its `.switch-input` row named only `remote_control`. Updated in the
  same commit — that document is binding under Principle VII, and the drift it
  exists to catch is exactly this one. Worth noting that **no test would have
  caught it**: the class sweep
  (`TestTheComponentsDocumentNamesThePickerTheSwitchTheHeaderAndTheToast`) matches
  `.switch*` by *name*, so a document naming the right classes while describing
  the wrong number of controls passes it cleanly. Same blind spot #119 was about,
  one level up from the classes.

---

## Iteration 3 — 2026-08-10

**Did:** T003. The card carries two new `.card-meta` rows — `idle deadline` and
`lifetime deadline` — so an operator can see when a session dies and can tell
that the T002 switch took effect. **Two rows, not one**: a card showing only the
idle deadline would read as "this session never dies" for exactly the session
whose operator relaxed the bound.

**Learned — what the next iteration would otherwise rediscover:**

- **`IdleDeadline()` returns `LastActivity + AbsoluteLifetime*400` when reaping is
  off — 400 days.** Rendered through `formatAge` that reads "in 400 days", a date
  nothing in the daemon believes. `formatIdleDeadline` (dashboard.go) states
  `noIdleLimit` instead. This is the single most important thing about the idle
  clock at the render layer.
- **`session.Session.IdleDisabled()` is new** and is now the one expression of
  "a negative `Idle` means off". `IdleDeadline()` calls it, so the two cannot
  drift. Anything outside `internal/session` that needs to know should call it
  rather than compare against zero.
- **`formatDeadline` puts the boundary where `expiredAt` does** (`d <= 0` →
  `"due now"`), so the card and the reaper agree about a session that is already
  past its bound. `formatAge` would otherwise say "in less than a minute" about a
  session the next sweep is entitled to take.
- **The card's meta rows need no class and no CSS.** `.card-meta` is a two-column
  grid of `dt`/`dd`, `max-content` label column above the breakpoint and one
  column below it. Adding rows changed no stylesheet rule, so
  `TestTheComponentsDocumentNamesThePickerTheSwitchTheHeaderAndTheToast` was never
  in play.
- **`ownedCard()` in `partials_test.go` is "everything the daemon can know"** and
  now carries both formatted deadlines. A new `sessionView` field wants a value
  there or every card test renders an empty `dd`.
- **Proven by breaking it, twice**: with the `IdleDisabled` branch removed the
  disabled row reads `"in 400 days"`; with the lifetime row deleted from the
  template the test says an operator cannot tell when the session dies.
- **`go test -tags quickstart ./cmd/crswd` passes here again** (36s) — worth the
  run for a template change, as iteration 2 found.

**Left:** T004 (the stale comment at `internal/session/session.go:15` — note it is
now at **line 16** and the file grew by `IdleDisabled`, so grep for the sentence
rather than trusting the line number), T005 (README).

**Findings — noticed, not fixed:**

- **Both findings from iteration 1 still stand, untouched for a third
  iteration.** The signed API answers a refused lifetime with a **500**
  (`refuseCreate`, `internal/httpapi/sessions.go`) where `docs/security.md` wants
  a 400 with the uniform body — one `case` beside the existing
  `ErrInvalidName, ErrInvalidWorkDir` one. And `internal/httpapi/render.go` is
  still the only file `gofmt -l .` names, on a branch where `golangci-lint run`
  reports 0 issues and no CI job runs `gofmt`. Both are fix-lane lines.
- **The card shows the lifetime deadline but nothing can set it from the
  browser.** T001 reads `lifetime`; T002 put a control only on the idle clock. So
  the new `lifetime deadline` row is the daemon's default for every
  browser-created session, and an operator who reads it and wants it longer has
  to go to settings. That is the same gap iteration 2 logged, now *visible* on
  every card — which arguably strengthens the case for the per-session lifetime
  field, and is still not this milestone's to answer.
- **`specs/004-configure-and-operate/contracts/card-layout.md` describes the
  readable half as "name, state, mode, workdir, age"** and is now one milestone
  stale. Left alone deliberately: a shipped spec is a record of what that
  milestone decided, and `docs/components.md` is the binding document — that one
  was updated in the same commit, including the `cardOf` field list, which had
  already been missing `StartCommand` and `Mode` before this task touched it.

---

## Iteration 4 — 2026-08-10

**Did:** T004. The comment above `AbsoluteLifetime`/`IdleTimeout`
(`internal/session/session.go`) no longer claims they are constants *on purpose*
so no operator can widen them. It now says what #37 actually built: they are the
built-in defaults, the operator's configuration sets both the defaults and the
ceilings, a create may override either under those ceilings, and the **ceiling**
is what keeps Principle VI true.

**Learned — what the next iteration would otherwise rediscover:**

- **T005 (README) can be written from this one file.** The four keys are
  `CRSW_SESSION_LIFETIME` / `CRSW_IDLE_TIMEOUT` (defaults) and
  `CRSW_SESSION_LIFETIME_MAX` / `CRSW_IDLE_TIMEOUT_MAX` (ceilings), loaded at
  `config.go:649,657` and handed to the manager by one call —
  `internal/httpapi/server.go:387` `sessions.SetLifetimes(...)`.
- **Where each bound is actually enforced**, for prose that has to be true:
  `validateLifetimes` (`config.go:1493`) refuses at *startup* — lifetime must be
  positive, a ceiling may not sit below its own default, `CRSW_IDLE_TIMEOUT` may
  not be negative (0 is the disable there) and may not exceed the lifetime.
  `resolveLifetimes` (`manager.go:232`) refuses at *create* — over a ceiling is
  refused, never clamped; a negative lifetime is refused; a negative idle is the
  per-session disable; an idle that could never fire inside the lifetime is
  refused. Note the asymmetry in spelling: **`CRSW_IDLE_TIMEOUT=0` disables,
  a negative `Idle` on a record disables** — zero on the record means "unset".
- **"Effectively never", concretely:** raise `CRSW_SESSION_LIFETIME_MAX` (and
  `CRSW_SESSION_LIFETIME` if the default should move with it — `loadDuration` has
  no upper bound, so `8760h` parses), then a create asks for a lifetime up to that
  ceiling. There is still no "never" sentinel and `AbsoluteDeadline`'s comment
  says why: *"a session that can outlive the daemon's own memory of why it exists
  is what Constitution VI is written against"*. The dashboard can now make the
  idle half of that choice (T002's switch); the lifetime half is still settings.
- **A comment fix has no test that can fail without it.** Nothing in this repo
  reads a Go comment, so the gate for this task was the green tree plus the
  reading. Deliberately did not invent a prose-pinning test: it would pin the
  wording rather than the fact, and Principle IV is against the machinery.

**Left:** T005 (README's configuration table and the lifetime prose) — the last
task in the milestone.

**Findings — noticed, not fixed:**

- **`internal/config/config.go:1454` makes a claim about a test that does not
  exist.** It duplicates the two constants to avoid an import cycle — correct and
  well explained — and then says *"config_test pins them equal so the duplication
  cannot drift silently."* Nothing does: no `_test.go` in the repo mentions
  `session_AbsoluteLifetime`/`session_IdleTimeout`, and no config test imports
  `internal/session`. So the duplication is unpinned **and** a reader is told it
  is safe. Two lanes' worth: the pin is a small test
  (`session_AbsoluteLifetime == session.AbsoluteLifetime`, in a `config_test`
  package to keep the cycle out of the build), the sentence is a fix-lane line.
  Out of scope here — T004 names one comment in one file and AR-008 forbids the
  reach — but this is the *same defect class* T004 exists to close, one package
  over, and it is now the more dangerous of the two: a false claim of coverage.
- **Both findings from iteration 1 still stand, untouched for a fourth
  iteration.** The signed API answers a refused lifetime with a **500**
  (`refuseCreate`, `internal/httpapi/sessions.go`) where `docs/security.md` wants
  a 400 with the uniform body. `internal/httpapi/render.go` is still the only file
  `gofmt -l .` names, on a branch with `golangci-lint run` (v2.12.2) at 0 issues.

---

## Iteration 5 — 2026-08-10

**Did:** T005, the last task. `README.md`'s four lifetime rows now describe a
per-session override the dashboard can actually reach, and two new notes say the
two clocks apart and give the effectively-never recipe the operator asked for.
The closing paragraph now names *why* the `_MAX` ceiling is the bound: the
ceiling is the operator's, the choice under it is the session's.

**Learned — what a later iteration would otherwise rediscover:**

- **The README's configuration table is pinned by name only.**
  `internal/config/docs_test.go`'s `readmeVarRow` matches the **first cell** of a
  row against `config.go`'s declared constants, in both directions. Descriptions
  are free prose. What must not change: one row per variable, on one line, first
  cell exactly ``| `CRSW_X` |``.
- **`cmd/crswd/quickstart_test.go` also reads `README.md`**, but only lines that
  begin `journalctl` after a leading `#` is stripped (`trailCommands`). A new
  fenced block is safe as long as no line in it starts with that word.
  `internal/release/readme_test.go` pins the install one-liner, the "no clone and
  build above it" rule and the rollback path — all outside the configuration
  section.
- **The idle *disable* is not bounded by `CRSW_IDLE_TIMEOUT_MAX`.**
  `resolveLifetimes` checks `idle > maxIdleAllowed` and `idle > effectiveLife`;
  a negative passes both. That is deliberate and is the whole asymmetry — the
  ceiling bounds an idle timeout set *longer*, and what bounds a session with the
  clock off is the absolute deadline. Prose that says the ceiling bounds the
  switch is wrong.
- **Deviation from T005's wording, deliberately.** The task says the four keys
  should say the ceilings bound a choice "the dashboard can now make". Only the
  idle half is true: T002 put a control on the idle clock alone, so
  `CRSW_SESSION_LIFETIME_MAX` still bounds something only the signed API sends.
  The README says exactly that rather than the plan's sentence — a README that
  promised a lifetime field the form does not render would be this milestone's
  own defect, one document over.
- **No test was added, for iteration 4's reason.** Nothing reads README prose,
  and the two facts worth pinning are already pinned elsewhere (the variable
  names here, the form's `idle_timeout` field in `partials_test.go`). A test over
  the wording would pin the sentence rather than the fact.

**Left:** nothing. T001–T005 are all done and the tree is green —
`go build ./... && go vet ./... && go test ./... && golangci-lint run` (v2.12.2,
0 issues), plus `go test -tags quickstart ./cmd/crswd` (36s).

**Findings — noticed, not fixed:**

- **`CRSW_IDLE_TIMEOUT=0` does not disable idle reaping, and the daemon's own
  error message says it does.** `validateLifetimes` (`internal/config/config.go`)
  refuses a negative with *"use 0 to disable idle reaping"*. But a configured 0
  reaches `SetLifetimes` as `defaultIdle = 0`; `resolveLifetimes` reads a zero
  `req.Idle` as "take the default", gets 0, and the record carries 0 — which
  `orDefault` resolves to the built-in `IdleTimeout`, **60m**. So an operator
  following that advice silently gets the very default they were turning off. The
  disable is a *negative* `Idle`, which only a create can spell. Two lanes: the
  message is a fix-lane line; making the configured 0 mean the disable
  (translate to `-1` the way `parseLifetimeOverrides` already does) is a change
  with a test. **This is why the README's new prose never mentions 0** — it would
  have documented the bug.
- **`.env.example` still tells operators the lifetimes are constants.** Its
  closing *"Not configurable, and named here so nobody goes looking for a knob"*
  section says the idle timeout (60m) and the absolute lifetime (24h) "are
  constants in the code, not variables… a deployment that could widen them from
  an environment file could unbound it". Lines 144–169 of **the same file**
  document all four variables. That is T004's defect exactly, in the file an
  operator copies, and it is self-contradicting. One-line fix lane.
- **The create form's hint points at a page that cannot change it.** T002's
  `.field-hint` says *"Raise session_lifetime in settings"*; `/settings` is
  read-only by construction and registers no mutating verb, so the operator who
  follows it finds a table. The real move is the config file or the unit's
  environment, then a restart. Copy fix.
- **`docs/auth-and-sessions.md`'s Lifetimes table is a milestone stale.** It
  states "Session idle timeout | 60 minutes" and "Session absolute lifetime |
  24 hours, no renewal" as facts, with no mention of the configuration or the
  per-session override. It is a binding document under `AGENTS.md`'s progressive
  disclosure — likelier to be read before a handler change than the README is.
- **Iteration 1's two findings and iteration 4's one are all still open.** The
  signed API's **500** on a refused lifetime (`refuseCreate`) where
  `docs/security.md` wants a 400; `internal/httpapi/render.go` still the only
  file `gofmt -l .` names; and `internal/config/config.go`'s claim that
  "config_test pins them equal" about a test that does not exist. Five fix-lane
  lines are now waiting, four of them about the same thing this milestone was
  about: a document that describes code it has outlived.

RALPH_COMPLETE
