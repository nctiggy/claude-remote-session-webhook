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

## Iteration 0 — make idle mean what it says

**Did:** Reset the notebook. The previous plan (a config template) was **wrong and
was correctly blocked**: `config.example` already exists at the repository root,
already carries every key, and is already guarded in both directions by
`configExamplePath` in `internal/config/file_test.go`. The plan told an iteration
to create a duplicate and it refused. That block was right.

**Left:** the tasks below.

**The operator's question, and the answer they were owed:** *"I have no idea what
idle means… even if I am using the session I think it is still considered idle."*

They are right, and it is worse than they put it. `LastActivity` is advanced by
exactly three things — `Resolve` (the signed-API credential path), `Compact`, and
`SetMode`. Reading does not: the dashboard, the session page and the **live
stream** all go through `View`, which has no clock reading in it by construction.
There is no route to type into a session from the browser at all; `Prompt` exists
only on the signed API.

**So a browser-driven operator watching a session all day has it reaped at sixty
minutes**, and someone attached to the tmux session directly on the host is
invisible to the clock entirely. "Idle" measures daemon-mediated mutating HTTP
requests and calls that activity.

**What makes this fixable rather than a trade:** tmux tracks the real thing.
`#{session_activity}` is a timestamp of when the session last produced output.
`argvList()` in `internal/tmuxctl/fake.go:94` already runs `list-sessions -F` with
six fields on every reconciliation — this is a seventh field on an exec that
already happens, not a new call.

It also answers the objection that killed the obvious fix: counting browser reads
would let a forgotten tab hold an unsandboxed shell open forever. Real tmux
activity is not a forgotten tab.

---

## Iteration 1 — 2026-08-12 — T001, the seventh field

**Did:** `argvList()` now asks tmux for `#{session_activity}` as a seventh field,
`parseSessions` reads it, and it rides on `SessionInfo.Activity`. The `Fake`
stores it, stamps it from the injected clock in `New`, carries it through `Seed`,
and exposes `SetActivity` for a test that needs a session tmux says is busy while
the daemon has not heard from it. Nothing consumes `Activity` yet — that is T002.

**Learned:**

- **The real tmux here renders `#{session_activity}` as a Unix timestamp.**
  Verified, not assumed: `TestTmuxListReportsProvenanceAndCreation` now asserts
  it lands within seconds of now, so a tmux that did not know the format (it
  would emit the literal text) fails loudly instead of silently falling every
  session back to the old clock. `go test -tags tmux ./internal/tmuxctl` passes.
- **Activity is the last field, so it is the first cut from the right.** The
  start-command name moved to second-from-right. Everything after the session
  name is still digits, a flag, a validated label, base64, and a validated
  command name — nothing that can carry a `|` — so cutting from the right still
  holds. Only the session name may contain the separator.
- **The parse is deliberately asymmetric.** An unreadable *creation* time still
  fails the row; an unreadable *activity* time yields the zero time and the row
  parses. Failing the row would abandon reconciliation and leave every managed
  session on the host unadopted, which is far worse than measuring idle the way
  yesterday's build did. Two table cases pin this and two more pin that a bad
  creation time still errors.
- **Three places assert the six-field argv**, and all three had to move together:
  `internal/tmuxctl/fake_test.go`, `internal/tmuxctl/exec_test.go`, and
  `internal/session/manager_test.go` (the Adopt argv). Grep for
  `session_created` before changing the format string again.
- **Proved by breaking it.** With the seventh field removed the real-tmux test
  fails on `unreadable row`; with the `Activity` assignments dropped, seven
  parse cases and the fake round-trip test fail. Both restored.

**Left:** T002–T007. T002 is the one that makes any of this matter — nothing
reads `SessionInfo.Activity` yet, and per `docs/conventions.md` a test that
cannot fail is not a test: assert the caller.

**Findings:**

- **⚠️ `go test -tags quickstart ./cmd/crswd` ran while this iteration's tree was
  dirty, and the tree came back clean with the work in `git stash`.** Nothing in
  the repo runs `git stash` (grepped `.claude/`, `.githooks/`, `ralph/`, and the
  suite itself), so the stash came from outside it — the reflog shows the branch
  was also moved from `feat/m13b-idle-real` to `main` at the same moment. **The
  lesson stands regardless of cause: commit before running the quickstart
  acceptance suite, never with uncommitted work in the tree.** That suite builds
  a real binary and binds a real port; it is the one command here with a foot
  outside the process.
- **A leftover `stash@{0}` on this branch is a duplicate of this commit** and can
  be dropped. `stash@{1}` is older and from `fix/42-prg` — not mine, left alone.
- **An iteration can find itself on a different branch than it started on.**
  This one did, and nearly committed T001 onto `main`, which the hard rules
  forbid. `git branch --show-current` before `git commit` is cheap; the loop's
  clean-tree check at `ralph/loop.sh:32` does not cover this.
- **`Fake.Seed` silently drops `Label`, `WorkDir` and `StartCommand`** from the
  `SessionInfo` it is handed — it only carries `Created` and `Managed` (and now
  `Activity`). A test that seeds a labelled session and asserts the label back
  would be testing nothing. Not fixed: outside T001, and no caller relies on it
  today.

---

## Iteration 0b — the finding that nearly cost the milestone

**Did:** Added T000 and recorded why the rest of this plan is worth running.

**The operator, mid-milestone:** *"In the remote control sessions, the only ones
that matter TBH, there is no new activity in the tmux session."*

They were right, and the consequence is larger than the idle clock. Their `rc`
command carried `--spawn`, which makes the tmux session a **launcher**: the
conversation lives on the relay and the pane goes quiet after startup. So for the
sessions this operator actually runs, the pane is empty, the pill cannot tell
running from idle from needs-auth, `compact` types into a pane nobody reads, and
`#{session_activity}` never moves.

**T001 and T002 would have measured a clock that never ticks, for the only
sessions that matter.** It was one iteration from shipping.

The fix is a start command, not daemon code:

    rc=claude --dangerously-skip-permissions "/rc {name}"

An ordinary interactive session that drives itself into remote control, so the
tmux session *is* the session. Verified twice: the operator ran the form and it
worked, and the config parser passes the quotes through intact — `send-keys` is
called without `-l`, so the shell parses them normally.

**The lesson is about where the daemon's assumptions come from.** Everything the
dashboard does — the pane, the pill's inferred states, compact, milestone 7's
whole mobile sweep — assumes the session renders into its own pane. Nothing
checked that assumption, and a start command an operator was free to configure
had quietly broken it for their primary use.

---

## Iteration 2 — 2026-08-12 — T000, the default that renders in its own pane

**Did:** `config.example` and `.env.example` now show
`rc=claude --dangerously-skip-permissions "/rc {name}"` as the remote-control
command, with the `--spawn` launcher kept in both as a documented alternative
and one sentence on why it is no longer the example. A new guard in
`internal/config/file_test.go` loads the uncommentable `start_commands` line
through `config.LoadFrom` and fails if the command the switch resolves to spawns
its session elsewhere.

**Learned:**

- **`config.go` ships no built-in remote-control command line.** T000 said to
  change `DefaultRemoteControlCommand` "or whatever `config.go` names the
  built-in" — there is none. `DefaultRemoteControlCommandName = "rc"` is a
  *name*, and `loadRemoteControlCommand` resolves it only if the operator
  configured a command by that name; otherwise the dashboard renders no switch
  at all. So the two example files **are** the whole of the shipped default, and
  adding a built-in `rc` would have handed a remote-control switch to every
  daemon that configures nothing — a widening nobody asked for. Not done.
- **The guard loads rather than greps.** The claim about that line is that a
  daemon starts on it, so it goes through the daemon's own loader; the
  `--spawn` check is the last assertion, not the only one. Proven by breaking:
  with the launcher form restored it fails naming the file, the line and the
  consequence.
- **The duplicate-key trap is real and it was one indentation away.** Any
  comment line whose text before the first `=` is a known key counts as that
  key's line, indented or not — so the alternative form is written as prose
  (`claude remote-control --spawn=same-dir …`) and never as
  `# start_commands = …`. `exampleLines` in `file_test.go:1298` trims the `#`
  *and* the whitespace before matching.
- **The config-file parser trims only the ends of a value**, so the quotes reach
  `send-keys` intact. That is what makes the quoted form safe *in
  `config.example`*, and it is a property of that parser only.

**Left:** T002–T007. T002 is the one that makes T001 matter — nothing reads
`SessionInfo.Activity` yet.

**Findings:**

- **⚠️ The quoted form may not survive an env file, and I could not verify it.**
  `deploy/crswd.example.service` reads `EnvironmentFile=-%h/.config/crswd/env`
  and sets `Environment="CRSW_START_COMMAND=…"`; systemd parses quotes in both.
  If it strips them, the operator's env-file spelling delivers
  `claude --dangerously-skip-permissions /rc {name}` — two positional arguments
  rather than one prompt — and that is exactly the class of silent breakage this
  task exists to fix. The sandbox refused the `systemd-run` check, so
  `.env.example` says only that the quotes are part of the command line and to
  confirm whatever reads that file leaves them on, and points at
  `config.example`, which takes the value verbatim. **Someone with a shell on a
  systemd host should settle this**; if the quotes are eaten, `deploy/` needs
  the escaped spelling and the unit's `Environment=` line does too.
- **`deploy/README.md`'s recipe and the example unit still teach the env-file
  path for command lines**, and neither was touched here — T000 named
  `config.example` and `.env.example` only. Out of scope, worth a task.
- Iteration 1's finding stands: **commit before running
  `go test -tags quickstart`**. Not run this iteration — nothing under
  `cmd/crswd` changed. `go vet -tags quickstart ./...` and `-tags dev` compile;
  `go test -tags tmux ./...` passes.

---

## Iteration 3 — 2026-08-12 — T002, the clock that watches the session

**Did:** `Session.TmuxActivity` holds what the host last saw the session do, and
`IdleDeadline` is measured from `idleFrom()` — the later of it and
`LastActivity`. `Reaper.Sweep` calls the new `Manager.syncActivity` before it
judges anything: one `list-sessions` per sweep, `adoptableID` to decide which
rows are ours, and `Store.setTmuxActivity` to record them. The operator's
session — watched all afternoon in the dashboard, driven by nobody through the
API — is no longer reaped at sixty minutes.

**Learned:**

- **Where the value had to live was decided by FR-019c, not by taste.** The
  reaper could have read the host's times and thrown them away inside `Sweep`,
  storing nothing. It must not: the card renders `s.IdleDeadline()` directly
  (`dashboard.go:388` → `formatIdleDeadline`), so a deadline the sweep knew
  about and the record did not would make the dashboard and the reaper disagree
  about the same session — exactly the drift FR-019c forbids. Hence a field on
  the record, refreshed by the sweep, read by both.
- **The fail-safe rule is a comparison, deliberately not a branch.**
  `idleFrom()` is `if TmuxActivity.After(LastActivity)`. Absent, unparsable,
  stale, and from-a-disagreeing-clock are not four cases — none of them can be
  *later*, so all four fall through to `LastActivity` and none can shorten a
  session's life. There is no "is this usable?" test to get the wrong way round.
- **⚠️ The fake stamped tmux activity with `time.Now()` while the fixture's
  daemon clock stood at `contractCreatedAt`, ten days earlier**, so the first
  run had every managed session looking busy days into its own future and
  **thirteen reaper tests failed at once**. Fixed by pinning the fake's clock to
  the fixture's in `newManagerFixture` — one clock for the host and the daemon,
  because they are one clock in production. A test that wants "the host says
  busy" now says so with `SetActivity`. `manager_test.go:2269` pins the same
  thing locally and is now redundant; left alone (AR-008).
- **Only one existing assertion had to move**, and it is the right one:
  `TestSweepTearsDownTheWayAnExplicitDestroyDoes` pins the exact argv a sweep
  runs, so the new `list-sessions` at the head of it is now in `want`. Grep
  `OpList` before changing the sweep's command sequence again.
- **`setTmuxActivity` is unexported and returns nothing**, breaking the pattern
  every other `Store` mutator follows, for reasons written at the call site: the
  sweep acts on the daemon's own behalf and has no owner to check (as `lookup`
  and `snapshot` do not), and "the host listed a session this store has no
  record of" is the ordinary case rather than a failure.
- **Proved by breaking it, three ways.** `idleFrom` returning `LastActivity`
  alone fails the new sweep test, the store test and one table case; returning
  `TmuxActivity` alone — the parse-failure-makes-a-live-session-reapable bug
  T002 names — fails the fallback test plus a dozen existing ones; disabling the
  `syncActivity` call fails three. All restored.
- Linter confirmed v2 (`2.12.2`, #26). `go test ./...`, `-tags tmux`, `-tags
  dev`, and `-tags quickstart ./cmd/crswd` all pass; quickstart was run **after**
  the commit, per iteration 1.

**Left:** T003–T007.

**Findings:**

- **The card's idle deadline is up to one sweep interval stale.** tmux activity
  reaches a record only when a sweep stores it, so a session that started
  printing five seconds ago still shows the old deadline for up to 30s. That is
  the reaper's own resolution and consistent with it — but T003 is about telling
  the operator what the clock is watching, and "watching, as of the last sweep"
  is the honest wording.
- **`gofmt -l .` flags `internal/httpapi/render.go`** — an import of
  `internal/buildinfo` sorted above the stdlib block. It is committed that way,
  was not touched here, and `golangci-lint run` reports 0 issues, so nothing
  gates on it. One `gofmt -w` fixes it; out of scope for T002 (AR-008).
- **`Fake.Seed` still silently drops `Label`, `WorkDir` and `StartCommand`**
  (iteration 1's finding, unchanged). It does carry `Activity`, which is what
  T002 needed.
- **The API's `last_activity` still means "when a request last drove this"** and
  was deliberately not changed: `entryFor` renders `s.LastActivity`, not
  `idleFrom()`. Nothing on the wire claims an idle deadline, so nothing there
  became inconsistent — but if T003 or a later task exposes the effective clock
  through the API, it needs a name that does not collide with this one.

---

## Iteration 4 — 2026-08-12 — T003, the row that answers "why is this dying"

**Did:** The card carries a `last activity` row directly above `idle deadline`,
rendering the instant the reaper measures from. `idleFrom` is now exported as
`Session.IdleSince`; `cardOf` formats `now.Sub(live.IdleSince())` through a new
`formatSince`, in `formatAge`'s vocabulary. Another `.card-meta` `dt`/`dd`, no
new class, no CSS.

**Learned:**

- **The row is above the deadline, not below it, and that is the argument.**
  The two read as one sentence — last active this long ago, therefore due then.
  A deadline followed by its evidence reads as a footnote; evidence followed by
  a consequence reads as a reason.
- **Iteration 3's naming warning was live, and this is where it landed.** The
  view field is `IdleSince` and never `LastActivity`, because `httpapi` already
  has a `LastActivity` meaning the narrower "a request drove this" —
  `sessions.go:149`, the signed API's `last_activity`. Two fields with one name
  and two meanings in one package is the collision that finding predicted. The
  *label* is still "last activity", because that is the operator's word for it;
  the Go names are what stay apart. `view.go` says so at the field.
- **Coarseness is the honest rendering, not a shortcut.** tmux's reading reaches
  a record only when a sweep stores it (iteration 3's finding), so the value is
  up to one sweep interval — 30s — stale. A minute's granularity absorbs that;
  a timestamp would show a precision the daemon does not have. That reasoning is
  at `formatSince`, so the next hand reaching for `2026-08-12T11:58:03Z` meets it.
- **Exported the accessor rather than taking the later of two fields in the
  dashboard.** `IdleDisabled` exists for exactly that reason and says so — a
  second reading of the rule is free to disagree with the first the day the
  spelling changes. Renaming `idleFrom` touched three lines in `session.go` and
  nothing else in the tree.
- **Proved by breaking it, twice.** Rendering `live.LastActivity` instead of
  `IdleSince()` fails the busy-on-the-host case with the exact defect it
  describes — "1 hour ago" printed beside "in 57 minutes", the page contradicting
  itself; deleting the two template lines fails all five cases. Both restored.
- **`docs/components.md` was updated in the same commit**, because #119's lesson
  is that the drift this document cannot see is the code moving under it. The
  Session card's inventory sentence, its `cardOf` parameter list and one rule
  bullet.
- Linter confirmed v2 (`2.12.2`, #26). `go test ./...`, `-tags tmux`, `-tags
  dev`, `-tags quickstart ./cmd/crswd` all pass; quickstart run **after** the
  commit, per iteration 1.

**Left:** T004–T007. T004 is the next one and it is 🔒: a spelling for a
*never* absolute lifetime that cannot collide with "unset", on the per-session
override **and** on `session_lifetime_max`.

**Findings:**

- **The single-session page and the fleet both gained the row, because there is
  one card** — which is the component rule working, and worth stating because
  the row was designed against the fleet. On the session page it sits above a
  pane that may be showing the very output that moved the clock.
- **Nothing on the signed API exposes the effective idle clock**, and this
  iteration did not add it. `entryFor` still renders `s.LastActivity` under
  `last_activity`. A skill asking "when does this die" gets a deadline it cannot
  derive from the fields beside it — the browser can now answer that question and
  the API still cannot. Not a defect anybody has hit; the milestone names no task
  for it.
- **`gofmt -l .` still flags `internal/httpapi/render.go`** (iteration 3's
  finding, unchanged and untouched). `golangci-lint run` reports 0 issues, so
  nothing gates on it.
- **`session.validate` refusing a zero `LastActivity` is what makes the row
  unconditional**, and no test pins the two together. A record with neither clock
  set would render "56 years ago" rather than stating an absence — unreachable
  through `Store.Add` today, and a guard for it would be code no caller can
  execute, so it was deliberately not written. If that invariant is ever relaxed,
  this row is one of the things that breaks quietly.

---

## Iteration 5 — 2026-08-12 — T004, a lifetime that never ends

**Did:** A negative `Lifetime` now means the absolute deadline is off
(`Session.LifetimeDisabled`, `AbsoluteDeadline`), and `resolveLifetimes` grants
it on exactly one condition: the daemon's own ceiling must already be unbounded.
`CRSW_SESSION_LIFETIME_MAX = never` is how an operator says that, carried as a
negative. The card says "no lifetime limit" and the settings page says `never`.

**The spelling, and why it is a word:** `0` was impossible — zero is already
"unset" by the time a duration is parsed — and a negative duration was possible
but rejected. Both `0` and `-1h` are things a person writes in a config file
meaning *no time at all*, and reading either as "forever" switches off the last
bound on a host running unsandboxed shells. `never` cannot be misread in that
direction. `loadLifetimeCeiling` therefore **refuses** a raw negative and names
the word, so there is one operator-facing spelling and one internal one (the
negative the idle disable already uses), never two of either.

**Learned:**

- **⚠️ The two "off" bounds had to share one span, and this was one iteration
  from being a silent bug.** `IdleDeadline` answered `AbsoluteLifetime * 400`
  for a disabled idle clock — unreachable only because the absolute deadline
  underneath it always fired. Once that one could be switched off too, a session
  with **both** switches off was reaped for idleness after 400 days, by a number
  neither switch mentions and exactly against the label T005 has to write. Both
  now use `neverSpan` (a century). Proven: reverting `IdleDeadline` to the old
  multiplier fails `both bounds off is reaped by neither` and nothing else.
- **The ceiling is what keeps this the operator's decision**, and it is the
  whole security argument. `resolveLifetimes` reads `maxLife < 0` as "no ceiling"
  *before* every comparison, because "is X over a ceiling that is not there" has
  no answer. A daemon that configured nothing refuses `never`, and so does one
  with a 8760h ceiling — a ceiling raised is not a ceiling removed.
- **The idle-can-never-fire check had to become the finite case's alone.**
  `idle > effectiveLife` with a negative lifetime refuses *every* idle timeout,
  on precisely the sessions this milestone exists to keep alive.
- **`TokenExpiry` follows `AbsoluteDeadline`, so a never-expiring session has a
  never-expiring bearer token.** That is FR-015's "equal by construction"
  working rather than breaking: docs/auth-and-sessions.md's rule is that the
  token TTL may never be *shorter* than the lifetime. `expires_at` on the wire
  now reads year 2126 for such a session. Deliberate, and stated at the method.
- **The settings page is a *write* surface, which this task nearly missed.**
  `POST /settings/edit` runs `config.Validate` → the real `LoadFrom`, so `never`
  is accepted there and `-1h` refused, by the same code as at startup — but a
  loader that learned a word the page had not would have been a value an
  operator could read and never save. Pinned by
  `TestEditIsWhereAnOperatorRemovesTheLifetimeCeiling`. **Grep for
  `settings_edit` before adding any new config value spelling.**
- **`docs/components.md` was updated in the same commit** (#119's lesson). It
  said the absolute bound "cannot be turned off at all" and that a disabled
  deadline reads "in 400 days"; both were false the moment this shipped.
- **Proven by breaking it, eight ways** — refusing a negative lifetime
  unconditionally, granting it regardless of the ceiling, dropping
  `AbsoluteDeadline`'s branch, reverting the idle span, accepting a negative in
  the config loader, dropping the `never` word there, restoring
  `lifetimeMax < lifetime`, and dropping the parser case, the card branch and
  the settings branch. Each failed the case named for it; all restored.
- Linter confirmed v2 (`2.12.2`, #26). `go test ./...`, `-tags tmux`, `-tags
  dev` and `-tags quickstart ./cmd/crswd` (36s) all pass; quickstart run
  **after** the commit, per iteration 1.

**Left:** T005–T007. T005 is the create-form switch, and its label is now
literally true: with both switches on, nothing reaps the session.

**Findings:**

- **⚠️ A never-expiring session does not survive a daemon restart as one.**
  Adoption builds its record from what tmux knows, and tmux does not know about
  `Lifetime` — so the record comes back with a zero lifetime, the default
  applies, and `Adopt` tears it down on the spot if it is already older than
  that default (`manager.go`, FR-025). The operator's immortal session is
  therefore mortal across a redeploy, and killed *because* it was long-lived.
  Nothing persists session records today, so this is not fixable inside T004 —
  but it is the sharpest edge on this feature and nobody has been told about it.
  Worth a task, and worth a sentence in T006/T007's prose.
- **T005 has a decision to make about the form's `.field-hint`.** It currently
  reads "It still ends at the absolute lifetime this daemon is configured with…
  Raise session_lifetime in settings if that is too soon" — true for every
  session this form can start today, and false the moment T005 adds the second
  switch. The template comment above it was corrected in this commit; the
  visible sentence was deliberately not, because changing it before the control
  exists would make the page describe a switch it does not render.
- **The signed API's `lifetime` accepts `never` and nothing announces it.**
  There is no capability document or `GET /` shape listing what a create may
  ask for, so a skill discovers the word from the README (T007) or not at all.
  Same gap iteration 4 logged for the effective idle clock, one field over.
- **`gofmt -l .` still flags `internal/httpapi/render.go`** (iteration 3's
  finding, unchanged and untouched). `golangci-lint run` reports 0 issues.
- **`config.example`'s `session_lifetime` prose is now wrong** — it says "There
  is deliberately no way to say 'never'". That is T006's line to fix and is
  named here so it is not read as still true in the meantime.

---

## Iteration 6 — 2026-08-12 — T005, the switch, offered only where it works

**Did:** The create form carries a second override switch — `lifetime=never`,
labelled "Never die at the lifetime limit", `.field-switch`/`.switch-input`/
`.switch-label` and a `.field-hint`, no new class. It is rendered **only** on a
daemon whose operator removed their own lifetime ceiling, decided by a new
`session.Manager.LifetimeCeilingRemoved()` that `resolveLifetimes` now reads
too. The idle switch's hint points at the switch below it on those daemons and
at the settings page on every other.

**Learned:**

- **The gate is the interesting decision, and the plan already named it.** T004
  said a per-session never under a finite ceiling "is a setting that always
  refuses"; drawn unconditionally, this box would be refused on *every*
  submission an operator ever ticked it for. `docs/components.md` has one answer
  for that shape — a card with no page token offers no actions, a settings row
  `config.Editable` refuses is not rendered as a form — so the switch follows
  the daemon. An operator who wants it removes the ceiling and the form starts
  offering it.
- **One reading of the rule, and the break proved it is load-bearing both
  ways.** `resolveLifetimes` now calls the exported predicate instead of testing
  `maxLife < 0` locally, so miswriting it fails *the ceiling suite* as well as
  the form's — `TestALifetimeThatNeverExpiresNeedsTheOperatorsCeiling` and four
  cases of `TestBrowserCreateRefusesALifetimeThisDaemonWillNotGrant` went red on
  a one-character change. The dashboard asks the manager and never
  `s.cfg.SessionLifetimeMax < 0`: substituting the config read passes `go test`
  everywhere except the new page test, because the fixture sets lifetimes on the
  *manager*, exactly as `server.go` does at startup.
- **⚠️ `TestCreateFormRendersNoCommandName` counts configured command names as
  substrings of the whole create section**, and `default` is one of them
  (`config.DefaultStartCommandName`). The hint's first draft said "adopted back
  with this daemon's default lifetime" and tripped it the moment the switch
  rendered on that fixture. Reworded to "the lifetime an ordinary session here
  is given". **Prose in this form may not contain a configured command name**,
  and `rc` is two letters inside a great many English words.
- **Proved by breaking it, four ways.** Dropping the `{{ if }}` fails both
  withheld-case tests; posting `value="0"` instead of `never` fails with the
  parser's own reading — the exact "never must not be spellable as unset" trap
  the plan warns about, caught because the assertion goes through
  `parseLifetimeOverrides` rather than matching the string; reading the config
  instead of the manager fails the projection test; and `m.maxLifetime <= 0`
  fails six pre-existing cases. All restored.
- **`docs/components.md` was updated in the same commit** (#119). Its Switch
  section said there were two switches and that the absolute lifetime "cannot be
  disabled at all" — false since iteration 5. It now also carries the
  conditional-rendering rule, which is the one thing about this control that the
  markup cannot show.
- Linter confirmed v2 (`2.12.2`, #26). `go test ./...`, `-tags tmux`, `-tags
  dev`, `-tags quickstart ./cmd/crswd` (36s) all pass; quickstart run **after**
  the commit, per iteration 1.

**Left:** T006 and T007, both documentation. T006 must fix `config.example`'s
"There is deliberately no way to say 'never'" (iteration 5's finding) and now
also owes a line about the browser form, since `session_lifetime_max = never` is
what makes a switch appear on the dashboard.

**Findings:**

- **The restart edge is now told to the operator, in the hint**, rather than
  waiting for T006/T007 as iteration 5 proposed: saying "no clock reaps this
  session" and omitting that adoption gives it back the default lifetime — and
  tears it down on the spot if it is already older (FR-025) — would have been
  the same overstatement the whole hint exists to prevent. T006 and T007 should
  still say it; this is the one place an operator meets it at the moment of
  choosing.
- **⚠️ The signed API can ask for `lifetime=never` and gets no such gate.** It
  is refused by `resolveLifetimes` on a daemon with a ceiling, which is correct
  — but a skill has no way to ask *whether* this daemon would grant it, so the
  browser can now see the daemon's answer in advance and the API still cannot.
  Third iteration running that the same gap has been logged one field over
  (iteration 3's effective idle clock, iteration 4's `last_activity`). Worth one
  capability document rather than three separate fixes.
- **Nothing renders the ceiling itself on the create form**, deliberately: the
  hint says the daemon has no ceiling but never what the finite one *is*, for
  the reason the existing idle hint names no number — it would be false on every
  install that configured something else. The settings page is where a value is
  read, and the two hints both point there.
- **`gofmt -l .` still flags `internal/httpapi/render.go`** (iteration 3's
  finding, unchanged and untouched). `golangci-lint run` reports 0 issues.
- **`Fake.Seed` still silently drops `Label`, `WorkDir` and `StartCommand`**
  (iteration 1's finding, unchanged).

---

## Iteration 7 — 2026-08-12 — T006, the file that had become wrong about two bounds

**Did:** `config.example`'s four lifetime blocks now describe the daemon that
exists. `idle_timeout` says idle is measured from the later of two clocks — the
last request that drove the session, and what tmux itself last saw it print —
and that watching still advances neither. `session_lifetime_max` documents
`never`; `session_lifetime` says why the same word is refused one block above
it; `idle_timeout_max` says why it needs no such word. The header's "a value
that would weaken a bound is a startup failure" was true until milestone 13 and
is now qualified rather than deleted. One new guard,
`TestConfigExampleSpellsNeverWhereTheDaemonTakesIt`.

**Learned:**

- **The guard is the asymmetry, not the word.** `strings.Contains(raw, "never")`
  would pass on this file forever — "never" appears a dozen times as ordinary
  English, which is what makes the obvious docs-guard here worthless. What is
  checkable is the *behaviour the prose claims*: the ceiling accepts the word
  and the default refuses it, both through `config.LoadFrom`. Same shape as
  iteration 2's T000 guard, for the same reason — the claim is that a daemon
  starts on this.
- **⚠️ `CRSW_SESSION_LIFETIME` is a prefix of `CRSW_SESSION_LIFETIME_MAX`**, so
  `strings.Contains(err, EnvSessionLifetime)` is satisfied by a refusal that
  names only the ceiling. The first draft of the guard passed a break for that
  reason. It now matches `EnvSessionLifetime + " "`, and both refusal messages
  happen to be followed by a space. **Any assertion about which of these two
  variables an error names needs that trailing space.**
- **`session_lifetime = never` is refused three deep**, which is why breaking
  the guard took three edits rather than one: `loadDuration` cannot parse the
  word, `validateLifetimes` refuses `lifetime <= 0`, and `idle > lifetime`
  refuses it again. Good news about the daemon, and worth knowing before
  concluding a break "did not work" — with all three relaxed the guard goes red
  naming the wrong variable, and with `NeverLifetime` dropped from
  `loadLifetimeCeiling` the ceiling case goes red. Both restored.
- **⚠️ Adoption gives a session the *package constant* 24h, not this daemon's
  configured `session_lifetime`.** `Adopt` builds a record with `Lifetime`
  unset, and `AbsoluteDeadline` reads `orDefault(s.Lifetime, AbsoluteLifetime)`
  — the constant at `session.go:34`, never `m.defaultLifetime`. The restart-edge
  sentence was drafted as "this host's ordinary lifetime" and corrected to "the
  built-in 24h" before commit. Verify against `AbsoluteDeadline` before writing
  any sentence about what an adopted session gets.
- **The duplicate-key trap was checked mechanically, not by eye**: no line added
  in this change contains an `=` at all, so none of them can be read as a second
  `# <key> = …` line. `grep -n '=' config.example` lists exactly one line per
  key plus the three prose lines that were always there.
- Linter confirmed v2 (`2.12.2`, #26). `go test ./...`, `-tags tmux`, `-tags
  dev` and `-tags quickstart ./cmd/crswd` (36s) all pass; quickstart run
  **after** the commit, per iteration 1.

**Left:** T007 alone, and this iteration widened it — see the first finding.

**Findings:**

- **⚠️ `.env.example` carries the same four keys and is still wrong about both
  changes**, in blunter words than `config.example` ever used: its
  `CRSW_IDLE_TIMEOUT` block says "a long job you are watching is still reaped"
  (false since T002 — a job producing output moves the tmux clock) and its
  `CRSW_SESSION_LIFETIME` block says "there is deliberately no value meaning
  never" (false since T004). T006 named `config.example` alone and T007 names
  `README.md` and `docs/auth-and-sessions.md`, so this file fell between them.
  **T007's line in the plan has been amended to include it** rather than left as
  a finding to be rediscovered.
- **Adopted sessions ignore the operator's configured default lifetime**
  (above). On a daemon with `session_lifetime = 4h` an adopted session gets 24h
  — *longer* than configured, which is the wrong direction for a bound. Not
  fixed: outside T006, and it predates this milestone. Worth a task.
- **`gofmt -l .` still flags `internal/httpapi/render.go`** (iteration 3's
  finding, unchanged and untouched). `golangci-lint run` reports 0 issues.
- **`Fake.Seed` still silently drops `Label`, `WorkDir` and `StartCommand`**
  (iteration 1's finding, unchanged).

---

## Iteration 8 — 2026-08-12 — T007, the last three files that described the old daemon

**Did:** `.env.example`'s four lifetime blocks, `README.md`'s configuration table
and its two clock notes, and `docs/auth-and-sessions.md` now describe the daemon
milestone 13 built. `.env.example` lost both of the sentences iteration 7 named —
"a long job you are watching is still reaped" and "there is deliberately no value
meaning never" — plus a third nobody had flagged: its not-configurable footer
still ended "and that no value spells never". README's "There are two clocks, and
only one of them can be turned off" became "either of them", and its "Effectively
never is a ceiling raised, not a bound removed" became the opposite claim with the
cost and the restart edge attached. The binding spec got the smallest change of
the three: three rows of the Lifetimes table, one paragraph beside the stream
rule, one checklist line.

**Learned:**

- **No new guard, and that was the decision rather than an omission.** Everything
  the two operator files now claim about `never` is already pinned through
  `config.LoadFrom` by T006's `TestConfigExampleSpellsNeverWhereTheDaemonTakesIt`
  — that test drives the **environment** path (`pairs[EnvSessionLifetimeMax]`),
  which is exactly what `.env.example` documents, so a second guard would assert
  the same daemon behaviour a second way. The idle half is T002's session tests.
  What is left unguarded is prose, and iteration 7 already established that a
  `strings.Contains(raw, "never")` guard over prose is worth nothing.
- **⚠️ The idle disable has two spellings on the wire and only one is
  documented.** `parseLifetimeOverrides` translates `idle_timeout: "0"` to a
  negative, but a caller who sends `-1h` gets a duration that parses, stays
  negative, and disables idle reaping just as well — `resolveLifetimes` refuses
  only `idle > maxIdleAllowed`. `.env.example` said "pass a negative value" and
  now says `"0"`, which is the spelling the daemon's own comment calls the
  disable. Check `sessions.go:99` before writing either into a doc again.
- **The lifetime switch is gated in the template and the idle switch is not**
  (`create-form.html:338` vs `:285`), which is what the README note had to say
  and the one thing a reader cannot infer from the config table. Verified in the
  markup rather than from iteration 6's summary.
- **`internal/config/docs_test.go` checks the README table's *rows*, not its
  cells** — `readmeVarRow` matches the first cell only, deliberately, so every
  factual claim in the fourth column is unchecked by construction. Editing a
  description there is editing unguarded prose; editing a row name is not.
- Linter confirmed v2 (`2.12.2`, #26). `go test ./...`, `-tags tmux`, `-tags dev`
  and `-tags quickstart ./cmd/crswd` (35s) all pass; quickstart run **after** the
  commit, per iteration 1.

**Left:** nothing in this plan. T000–T007 are done and the tree is green.

**Findings:**

- **README's roadmap still says "Milestones 1 through 12 are complete" and has no
  row for 13.** Deliberately not touched: T007 names the configuration table and
  the idle clock, the milestone is not merged, and every previous milestone's row
  was written by its own last task. Whoever merges this branch owes that row —
  "Make idle mean what it says, and let a session live forever".
- **⚠️ Three iterations have now logged the same gap one field over: nothing
  announces what a create may ask for.** A skill cannot discover that `lifetime`
  takes `never`, that this daemon would grant it, or what the effective idle
  clock is, because there is no capability document and `GET /` has no such
  shape. The browser learns all three from the form; the signed API learns them
  from the README or not at all. One task, not three.
- **Adopted sessions still take the package constant 24h rather than the
  configured `session_lifetime`** (iteration 7's finding, unchanged). Both
  `config.example` and now `.env.example` and `README.md` state the 24h as a
  fact, so the docs are honest about a behaviour that is still worth fixing.
- **`.env.example` has no blank line between `CRSW_DESTROY_ON_SHUTDOWN=` and the
  comment block below it**, unlike every other pair in the file. Pre-existing,
  cosmetic, and left alone under AR-008 — noted so the next reader does not read
  the lifetime comment as belonging to the assignment above it.
- **`gofmt -l .` still flags `internal/httpapi/render.go`** (iteration 3's
  finding, unchanged and untouched). `golangci-lint run` reports 0 issues.
- **`Fake.Seed` still silently drops `Label`, `WorkDir` and `StartCommand`**
  (iteration 1's finding, unchanged).

RALPH_COMPLETE
