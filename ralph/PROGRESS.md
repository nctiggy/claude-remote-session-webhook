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

## Iteration 0 — 2026-09-01 — the loop had stopped, and nothing was broken

**Did:** Archived milestone 15 to `ralph/archive/progress-milestone-15.md` and its
plan to `ralph/archive/milestone-15-tasks.md`, then swept every finding milestone
15 recorded as noticed-but-not-fixed to find out which are still real.

**Why this iteration exists.** `ralph/PROGRESS.md` had not moved in nine days. The
cause is not a failure: it is that `loop.sh` refuses to start while the notebook
carries the exit sentinel, and by 23 August that notebook carried **four** of them
— milestone 15's, then one each appended by specs 012, 013 and 014, which ran the
Spec Kit lane through the same file. The guard was doing exactly its job. What was
missing underneath it is a milestone to run.

**Learned — what the sweep actually found:**

- **Every fix-lane finding milestone 15 wrote down has since been taken.** Each was
  carried forward by name through Iterations 1–7 as still open, so the notebook
  reads as though a backlog accumulated. It did not. Measured on this tree:
  - `internal/httpapi/render.go` **is gofmt-clean** — `gofmt -l .` names nothing.
  - `TestTheOpeningScreenIsSentWithoutWaitingOutAnInterval` **is no longer
    wall-clock flaky.** It ran against a 10ms cadence; it now takes
    `slowTickUnderTest` deliberately, and its comment records that the old
    assertion "was really 'is this machine busy?'".
  - `internal/config/migrate.go`'s header **no longer claims `cmd/crswd` is the
    only writer** — it now says every write in the package is in `write.go`.
  - `internal/config/write.go`'s header **names all four callers**, not two.
  - `config.WriteFile`'s temporary-file prefix **is documented for the reason it
    was flagged**: what comes through it is also a systemd unit, so it is named
    for the daemon rather than for a configuration.
  **The lesson for a future sweep: a finding restated in six consecutive
  iterations is evidence of a copy-forward habit, not of six months of neglect.
  Re-measure before planning against one.**

- **The tree on `main` is green.** `go build ./...`, `go vet ./...`,
  `go test ./...` (all 11 packages ok) and `gofmt -l .` were run here and pass.

- **The open task counts in `specs/*/tasks.md` are stale checkboxes, not work.**
  003, 004, 005 and 007 report 12–16 open apiece for milestones that shipped;
  006's two are `stage.go` and `swap.go`, both of which exist; 009's and 010's
  single items are each "PR against `main`", and both PRs merged (#139, #140).
  Nothing in `specs/` is a source of open work.

**Left:** three items, and **every one of them needs the operator**, which is why
this notebook stops here rather than picking one. They are set out with their
evidence in `ralph/IMPLEMENTATION_PLAN.md`.

**Findings — noticed, not fixed:**

- **⚠️ The config migration still runs in the *old* binary**, so a rename shipped
  in v0.90 is applied by the update *after* the one that installs v0.90. Iteration
  7 marked this **"a spec question, not a bug — do not 'fix' it silently"**, and
  that still holds. It costs nothing today: `renamedKeys` is empty and
  `SchemaVersion` is 1. T007 made one of the two fixes cheap — the staged
  candidate's own `crswd config migrate` is now the same code the updater runs, so
  exec-ing it changes *when* it runs and nothing else. Which fix is right is a
  decision, not a discovery.

- **`docs/mobile-open-questions.md` Q2 is still UNANSWERED**, and spec 014 may have
  overtaken it. Q2 asks whether a bare provenance word reads as part of the value
  once settings rows stack below 780px. Spec 014 then **removed the Source column
  entirely** and marks a value from somewhere else on its own row. The question was
  written against a layout that no longer ships. **It should be re-read before it
  is answered** — the fallback it specced (an explicit label in the row) may
  already be moot, and that file's own rule is that a question is answered by the
  operator's report, never by a task claiming it.

- **#120 and #121 are unchanged and were deliberately deferred.** Milestone 15 put
  both out of scope in writing. #120 (reflow at the PTY rather than wrapping in
  CSS) is the real answer to a phone-width pane and is a session-plumbing change,
  not a CSS one; #121 (a wrap/alignment toggle) was gated on the wrap having been
  used in anger first.

- **The Spec Kit lane and the Ralph lane share one notebook and one sentinel.**
  Specs 012–014 appended `RALPH_COMPLETE` to the milestone-15 notebook, which is
  what left four exit sentinels in a file whose guard tolerates none. Nothing was
  harmed, but the next loop start would have been refused on a file the Ralph lane
  had finished with weeks earlier. Either lane may write here — but only the lane
  that owns the current plan should write the sentinel.

---

## Iteration 0, continued — milestone 16 chosen, and two decisions taken

The three remaining items were put to the operator rather than picked. Both
answers are recorded here because the next iteration will otherwise re-derive
them, and one of them contradicts an issue this repository wrote itself.

**Decision 1 — milestone 16 is #120**, reflow at the PTY instead of wrapping in
CSS. The other two items stay where they are: the migration/old-binary question is
still a spec question, and Q2 is still the operator's to read on a phone.

**Decision 2 — the width policy is *offered, not taken*.** The browser reports its
width advisorily; the daemon never acts on it by itself, and a reflow is a
deliberate act that changes the session for every reader at once. Resize-on-view
was rejected for the reason #120 itself gives — hostile to a second viewer — and a
manually pinned width was rejected because making the operator set 44 by hand on a
phone is the problem the milestone exists to remove.

**Learned — tmux facts, measured on this host against tmux 3.4, detached sessions,
no client attached. Do not re-derive these; they cost an hour.**

- **`resize-window` works on a detached session.**
  `resize-window -t '=crswd-abc:' -x 44 -y 12` → rc 0, `window_width=44`.
  `PaneTarget` is the correct target helper; `resize-window` takes a window target
  and `=name:` satisfies it. No third target helper is needed.
- **tmux reflows content already on screen.** An 80-column line already in the
  pane came back re-wrapped at 44 **with no new output and no client attached**.
  This is the fact the whole milestone rests on, and it is better than assumed —
  the operator does not wait for the TUI to repaint before the pane reads.
- **⚠️ `new-session -t` does NOT give a grouped session its own width**, which is
  the option #120 says it would look at first. `new-session -d -t a -s b -x 100
  -y 30` returns 0 and leaves `#{window_width}` unchanged for both. Grouped
  sessions share the window, a window has one size, and `-x`/`-y` describe a
  **client's** size — inert for a daemon that never attaches a client. **The issue
  is wrong on this point and the plan says so.**
- **⚠️ `resize-window` implicitly sets `window-size` to `manual`** — it reads
  `latest` before the call and `manual` after, and nothing sets it back. A
  reflowed session therefore stops sizing itself to a terminal that later attaches
  on the host. That is a change to how the operator's own terminal behaves, and
  this daemon does not make those silently; T005 and T007 carry it.

**Left:** milestone 16, T001–T007, in `ralph/IMPLEMENTATION_PLAN.md`. Nothing is
blocked. The loop can run.

---

## Iteration 1 — 2026-09-01 — T001, the resize primitive

**Did:** Added `Resize(ctx, name, cols, rows)` to `Controller`, `Exec` and
`Fake` as `tmux resize-window -t <PaneTarget> -x <cols> -y <rows>`, built by one
shared `argvResize` that clamps both integers. Commit `058e30e`.

**Learned — measured on this host against tmux 3.4, detached session, no client.
Do not re-derive.**

- **`resize-window` accepts 1..10000 on both axes and refuses everything else**,
  by exit 1 with `width too small` (for 0 and negatives) or `width too large`.
  That is where `minDimension`/`maxDimension` in `fake.go` come from — they are
  tmux's numbers, not a policy. **T002's floor and ceiling are a different
  decision** (what makes a *usable* terminal) and should not be pinned to these.
- **The reflow is confirmed and it is immediate.** The new `-tags tmux` case
  prints an 80-column line, resizes to 44, and the line comes back as 44 + 36
  with no new output. No polling was actually needed; `waitFor` is there so a
  future tmux that defers the rewrap fails loudly rather than flakily.
- **`window-size` flips `latest` → `manual`, re-confirmed here.** Unchanged from
  Iteration 0 and still owed to the operator by T005 and T007.
- **The clamp lives in the shared argv builder, not in `Exec.Resize`.** That is
  what makes the fake's argv assertion a proof of the clamp rather than of a
  second copy of it. T004 clamps again at the handler — deliberate, and the
  comments on both say so.
- **`Fake.Size(name)` is new** and answers `80x24` for a session nothing has
  resized, because that is what the real server answers. T003/T004/T005 should
  assert through it rather than through `Calls()` alone: a handler that returns
  200 and resizes nothing passes an argv-only assertion.
- **Both guards were proven by breaking them**, as the plan requires: a
  pass-through `clampDimension` fails the argv assertion at every edge, and a
  no-op `Exec.Resize` times the reflow case out at 15s.
- **`tmux` is not on the Bash allowlist in `.claude/settings.json`.** The only
  sanctioned way to drive a real tmux from a loop iteration is a `//go:build
  tmux` test run through `go test -tags tmux`. A throwaway probe file in the
  package, run and then deleted, is how these numbers were measured — cheaper
  than guessing and it leaves nothing behind.

**Left:** T002–T007. Nothing is blocked.

**Findings — noticed, not fixed:**

- **`DefaultPaneBound`'s comment is now falsified and T002 owns the fix.** It
  still reads that a session "keeps tmux's 80x24 default"; as of this commit a
  session can be resized. The 200-line number it justifies is unaffected — a
  *narrower* window makes a capture taller, so if anything the bound matters
  more after a reflow than before. Worth a sentence in T002 rather than a silent
  edit.
- **Nothing enforces that a reflowed session is ever put back to automatic
  sizing.** `Resize` is one-way: there is no `window-size latest` call anywhere
  in the package, so the way back the plan promises does not exist in code yet.
  T005 says "with the way back" — if that means a control rather than only a
  documented `tmux set-window-option -t … window-size latest`, it needs a second
  Controller method and T005 is bigger than it reads. **Not a blocker for T002 or
  T003; read it before starting T005.**

---

## Iteration 2 — 2026-09-01 — T002, the width clamp

**Did:** Added `internal/config/width.go`: `DefaultPaneWidth = 80`,
`MinPaneWidth = 20`, `MaxPaneWidth = 500`, `ClampPaneWidth(int) int` and
`ParsePaneWidth(string) int`. Corrected `DefaultPaneBound`'s comment in the same
commit. Commit `3c9cbfc`.

**Learned:**

- **A reading T002 forced, written down because the next iteration must not
  re-take it: there is no `CRSW_PANE_WIDTH`.** The task says "in the shape
  `PaneBound` already has (`EnvPaneBound`/`DefaultPaneBound`, `loadInt`)", and
  two of those three are environment-variable machinery — so the env var was
  considered and rejected, for three measured reasons. (1) An `Env*` constant in
  this package is not just a constant: `config.Vars()`, `internal/httpapi`'s
  `settingsValue` switch, `.env.example` and `README.md` all enumerate them, and
  `TestREADMEDocumentsEveryVariable` and `deployexample_test.go` enforce it — so
  it would ship a **settings-page row for the width**, which the plan puts out of
  scope in writing ("It is a property of one session, set where that session is
  read"). (2) `loadInt` refuses a non-number at startup, so it cannot be the
  thing T002 describes: "a value that is not a number is the default rather than
  an error" is a rule about the value being *clamped*, not about a variable being
  loaded. (3) Nothing in T003–T007 needs the bounds to be operator-settable.
  **If the operator does want one, it is a small addition on top of this — the
  constants are already exported and already the only definition of the bounds.**
- **The clamp reads text, not an int, and that is what T003 and T004 both need.**
  A width reaches this daemon as a string every single time: a form field in
  T004, and the `@crswd-width` option read back in T003. `ParsePaneWidth("")` is
  80 by construction, so **T003's "a session with no option adopts as 80" falls
  out of this function rather than needing a second rule** — call it, do not
  write `if raw == ""`.
- **`strconv.Atoi` saturates on `ErrRange` and that is load-bearing here.** A
  40-digit width comes back as `math.MaxInt` *with* an error; the parse clamps it
  rather than treating it as gibberish, so there is no cliff between 9999999999
  (clamps to 500) and one digit more (would otherwise have been 80). T004's
  "a width of nine million" case and any longer one now answer the same way.
- **20 and 500 are a usability policy and are deliberately not tmux's 1..10000.**
  Iteration 1 flagged that they should be a separate decision and they are. The
  floor's argument is the one the milestone rests on: a reflow changes the
  session for *every* reader, so the floor bounds the damage one viewer can do to
  the others, and 20 is well under the narrowest real report measured here (44,
  on a phone). The ceiling only exists so no width this daemon accepts can be one
  tmux refuses — the T005 control is offered only to a viewer *narrower* than the
  session, so no honest reflow goes near it.
- **Iteration 1's finding about `DefaultPaneBound` was right that the comment was
  falsified and wrong about why.** It said a narrower window makes a capture
  *taller*. It does not: `capture-pane` with no `-S` returns the visible screen,
  which is `pane_height` lines, and a reflow sets columns while the rows come
  from the session. A narrower window fills the same 24 rows more completely. The
  comment now says that, and 200 is untouched.
- **Both guards were proven by breaking them**, as the plan requires: a
  pass-through `ClampPaneWidth` fails 5 clamp rows and 6 parse rows, and dropping
  the `ErrRange` exception fails the 40-digit row alone. Restored and green.

**Left:** T003–T007. Nothing is blocked.

**Findings — noticed, not fixed:**

- **T002's clamp has no production caller yet, and by the plan's own convention
  ("a task is not done when the code exists; it is done when something calls
  it") that is a gap T004 closes.** It is the same shape T001 shipped in — the
  `Resize` primitive still has no production caller either. **Two tasks now owe a
  caller; if T004 slips, both are dead code with green tests**, which is exactly
  the failure `docs/conventions.md` records three of. Worth checking at T004 that
  it calls `config.ParsePaneWidth` rather than growing its own `strconv.Atoi`.
- **`internal/tmuxctl/exec.go`'s `CapturePane` comment carries the same falsified
  claim `DefaultPaneBound` did** — "a detached session keeps tmux's default
  dimensions". Left alone deliberately: it is a different package, AR-008 says no
  refactoring outside the task, and T002 named only `DefaultPaneBound`. The bound
  it justifies is unaffected for the reason above (rows, not columns). **T007 owns
  the documentation sweep and should take this sentence with it.**
- **`ParsePaneWidth` trims surrounding whitespace and `loadInt` does not.** That
  is a real inconsistency between two parsers in one package, and it is
  intentional here — an advisory value from a browser should not be refused over
  a space — but a reader comparing them will notice. Not worth a change to
  `loadInt`, which refuses on purpose.

---

## Iteration 3 — 2026-09-01 — T003, the width the host holds

**Did:** Added `tmuxctl.OptionWidth` (`@crswd-width`) and `SessionInfo.Width` to
the `list-sessions` row, `Session.Width` + `Columns()` + `encodeWidth`/
`decodeWidth` + `Store.SetWidth`, restored the width in `Adopt`, and wrote
`Manager.Reflow` — which is **T001's and T002's first production caller**.
Commit `2eddeb2`.

**Learned:**

- **T003 was split at "the route", not at "the write".** The task says the option
  is "written onto the tmux session when a reflow is taken", so the reflow itself
  is here and **T004 is only the HTTP route plus its audit and negative cases**.
  T004 should call `Manager.Reflow(ctx, s, cols)` the way `continueFromBrowser`
  calls `Continue`, and its own clamp is the third one on that path (handler →
  `Reflow` → `argvResize`), which is what its "deliberate duplication" means.
- **`markSession` deliberately does *not* write `@crswd-width`.** It is called on
  a **recreate**, which builds a *new* 80-column window; writing the record's old
  width there would put "44" on a window that is 80 — a lie in the direction spec
  009 warns about. So the option is written by `Reflow` alone, where the resize
  that makes it true happens in the same call. **A recreated session therefore
  comes back at 80 with no option, which is honest**, and the operator reflows
  again. See the findings for the journal half of this.
- **Zero and 80 are kept apart on purpose, and T005 needs that.** `Width == 0`
  means nobody has reflowed the session; `Columns()` collapses it to 80 for
  anything that wants the number. The distinction is the *only* record that this
  daemon took the window out of tmux's automatic sizing, which is the ⚠️ sentence
  T005 and T007 owe the operator. `decodeWidth` therefore checks for an empty
  option rather than calling `ParsePaneWidth` bare, which is a **deliberate
  departure from iteration 2's advice** — the plan's "no option adopts as 80"
  still holds, through `Columns()`.
- **The width field went between `@crswd-lifetime` and spec 012's pair**, so the
  row stays "everything the daemon wrote, then the one thing tmux computes", and
  `parseSessions`' comment about the last two fields stays true. `listFieldCount`
  is now **10**. Every row literal in `exec_test.go` needed a field inserted
  *third from the right* — cutting is from the right, so counting from the left
  gets the malformed-on-purpose rows wrong.
- **`tmuxctl.DefaultRows` is now exported** (was `tmuxDefaultRows`) because
  `resize-window` names both axes and #120 is about columns: `Reflow` passes back
  the 24 rows the session already has. The column half stays unexported —
  `config.DefaultPaneWidth` is the same number where the policy lives.
- **The real-tmux round trip is pinned:** `TestTmuxListReportsTheWidthOption`
  resizes *and* sets the option, so it cannot pass on a tmux where the resize
  silently did nothing. `go test -tags tmux ./...` is green here.
- **All six new guards were proven by breaking them**, as the plan requires:
  dropping the restore in `Adopt`, dropping the `SetOption`, dropping the clamp,
  writing the store before the resize, making the fake return `""` for a width it
  stored, and reading the width out of the lifetime's field — each fails at least
  one test. Restored and green.

**Left:** T004–T007. Nothing is blocked.

**Findings — noticed, not fixed:**

- **The journal does not carry the width, and that is deliberate but incomplete.**
  `journalRecord` holds the lifetime; adding the width without also resizing on
  the recreate path would make the record claim a width the new window does not
  have. **Either both or neither** — and "neither" is what shipped. If T005 or a
  later spec wants a reflow to survive an OOM-killed tmux server, the change is
  `reviveRecord` + a `Resize` in `Manager.revive`'s `!shellSurvives` branch, and
  it needs the rows question below answered first.
- **A reflow normalises the height to 24 and nothing says so.** Before the first
  resize `window-size` is `latest`, so an operator who had attached a 50-row
  terminal has a 50-row window; `Reflow` sets `-y 24` and that height is gone.
  `#{window_height}` on the list row would give the true value if this ever
  matters. **T007's documentation sweep should mention it, or T005 should read
  the height back.**
- **`#{window_width}` exists and is not what `@crswd-width` holds.** The option is
  what this daemon *asked for*; the format variable is what the window *is*. They
  agree today because nothing else resizes these windows, and they would diverge
  the moment the operator puts a session back to `window-size latest` and attaches
  — which is exactly the "way back" T005 promises. **If T005 renders a number to
  the operator, it should consider rendering the truth rather than the intent.**
- **`TestQuickstartStory5RateLimit` failed once on a `t.TempDir` cleanup race**
  (`unlinkat .../001/home: directory not empty`) — not an assertion; the burst was
  `[201 201 201 429 429]`, which is what it wants. It passed on two re-runs and
  the full suite passed after. **Flaky teardown, not a regression**; something is
  still writing under the temp home while cleanup runs.
- **Two `wantErr` rows in `TestParseSessions` fail on the field count rather than
  the reason they name** ("creation time is not a number" has seven fields, not
  ten). That was already true before this change and is now one field further out.
  Left alone under AR-008; worth a line when someone next touches that table.

---

## Iteration 4 — 2026-09-01 — T004, the route that asks for it

**Did:** Added `POST /dashboard/sessions/{id}/reflow` behind `handleAction` —
`patternDashboardReflow`, `fieldColumns`, `reflowFromBrowser`,
`refuseBrowserReflow`, `audit.ActionDashboardReflow`, and three outcomes
(`reflowed`, `reflow-unconfirmed`, `reflow-failed`). It is **T001's, T002's and
T003's first reachable caller**: before this commit the whole milestone could be
driven only from a test. Commit `9ea64dc`.

**Learned:**

- **The handler's clamp is not redundant with `Manager.Reflow`'s, and the
  difference is exactly one case.** Breaking the handler's
  `config.ParsePaneWidth` down to a bare `strconv.Atoi` fails **5** of the 13
  clamp rows, not 13: the manager clamps too, so `-40`, `0`, `19`, `501`,
  `9000000` and 40 digits all still land correctly. What only the handler's
  *parse* decides is that a **non-number is the default (80) rather than the
  floor (20)** — `wide`, `$(tput cols)`, `44px`, an absent field and an empty
  one. **If a later edit wants to argue the third clamp is redundant, that is
  the case to point at**; the two are not the same function wearing two names.
- **The verbatim-argv sweep has to be restricted to non-numeric input.** A
  substring check for the submitted value matches the daemon's own correct
  output whenever the clamp is the identity (`20` in `-x 20`). The table now
  carries a `nameable` flag and only sweeps the three values that are text. A
  first draft of this failed on `20`, `19`, `0` and `500` — all four *correct*.
- **`dashboard.reflow`, not `session.reflow`.** `session.mode`'s precedent would
  allow the second, but the plan directs modelling on spec 013's `continue` and
  the reason holds: what an operator counts here is what the *browser* changed,
  and the trail is the only place a second reader's screen going narrow is
  attributable to somebody's phone. It is proven non-vacuous — registering the
  route under `ActionDashboardCompact` fails `TestReflowFromBrowser`.
- **`Fake.Size(name)` is the assertion that matters and Iteration 1 was right to
  flag it.** A handler that wrote the store and skipped tmux passes every
  store-only check. `reflower.onHost` reads the fake; `reflower.recorded` reads
  the store; both are asserted on every clamp row.
- **`untouchedWidth` checks `Width == 0`, deliberately not `Width == 80`.** Zero
  means nobody has taken this session out of tmux's automatic sizing; 80 means
  somebody reflowed it to the number it already was. Same window, different
  facts — and T005 needs the distinction to decide whether to draw the ⚠️.
- **Four guards were proven by breaking them**, as the plan requires: the clamp
  (5 rows), the confirming step (3 rows), the `handleAction` registration (3
  rows, via `handleBrowser`), and the audit action. All restored and green.
- **The success banner carries the ⚠️** — that a reflowed session stops sizing
  itself to a terminal attached on the host. **T005 still owns saying it
  *before* the operator presses**, with the way back; this is the after.

**Left:** T005, T006, T007. Nothing is blocked.

**Findings — noticed, not fixed:**

- **`registeredPatterns` in `settings_test.go` is stale by five routes and now
  six.** It sweeps every registered path for secret disclosure and for
  `Access-Control-Allow-*`, and its own comment admits "a twelfth would have to
  be added here by hand". Since it was written, `patternDashboardContinue`,
  `patternDashboardUpdate`, `patternDashboardRestart`, `patternSettingsEdit` and
  `patternConversations` were all registered without being added — and
  `patternDashboardReflow` follows them, because adding one to the list also
  requires driving it in that sweep, which is outside T004 (AR-008). **This is
  the one guard in the repo that is quietly losing coverage as routes are
  added**, and it is worth a task of its own rather than a line here: the fix is
  to derive the list from the mux rather than to keep typing it.
- **`docs/auth-and-sessions.md` and `docs/components.md` do not know this route
  exists.** T007 owns the documentation and should add it beside the
  "Continuing a conversation" section, which is the shape the reflow's paragraph
  wants.
- **There is still no way back to `window-size latest` in code**, restated from
  Iteration 1 because T005 is next and it is T005's blocker to size. Nothing in
  `internal/tmuxctl` sets it, so if T005's "way back" is a control rather than a
  documented `tmux set-window-option`, it needs a second `Controller` method and
  T005 is bigger than it reads.
- **A reflow to 80 and a session nobody reflowed are indistinguishable to the
  operator but not to the daemon.** The route accepts an absent `columns` field
  as 80, which means "put it back to the default width" — and that writes
  `Width = 80`, which is *not* the same as returning the window to automatic
  sizing. **T005 should not offer the absent-field case as an undo**; it looks
  like one and is not.

---

## Iteration 5 — 2026-09-01 — T005, the offer in the pane

**Did:** Added the reflow control to `partials/pane.html` — a form to the T004
route, shipped `hidden`, revealed by a new module in `crswd.js` that measures the
pane's own font against its own box. `paneView` gained `Columns`, `MinColumns`,
`Target` and `PageToken`; `sessionPage` fills all four off the record. Commit
`d0ea1ad`.

**Learned:**

- **The "way back" is a rendered command, not a second `Controller` method, and
  that was the sizing question Iterations 1 and 4 kept restating.** T005's text
  is *"says that a reflowed session stops sizing itself to a terminal attached on
  the host (the ⚠️ above), with the way back"* — the way back is part of what the
  control **says**. The plan's ⚠️ section agrees: it requires the operator be
  *left* a way back and that the page and docs *say* it. tmux already provides
  one; nothing in this daemon took it away. So the sentence ends with
  `tmux set-window-option -t '=crswd-<id>:' window-size latest`, rendered as text
  and never run — the settings page's `diff` command is the exact precedent
  ("this daemon prints it and never runs it"). **`internal/tmuxctl` still has no
  `window-size latest` call and now deliberately needs none.** A control-shaped
  undo would have been a second route, a second audit action and a second
  outcome, all to wrap one command the operator can already run — and it would
  have been the *only* action on this door with no confirming decision behind it.
- **The offer carries no class, and that is the whole of "no new component".**
  The settings page's restart form is the precedent — `<form>` with no class at
  all, `.button`, and a `<p>` beside it — and taking it means **zero CSS in this
  task**, so `TestTheStylesheetAndTheMarkupNameTheSameThings` and the
  `.combo|.switch|.masthead|.action-toast|.modal` document sweep are both
  untouched. **If a later task gives this control a class it must also give
  `docs/components.md` a name for it in the same commit**, which is the trap #119
  was.
- **`script(t)` strips comments before any assertion sees the file**
  (`jsComment.ReplaceAllString`). A JS test anchored on a comment reports a
  missing module for a file that was only re-worded. `REFLOW_CELLS` exists as the
  module's first statement partly to be that anchor, and the test says so.
- **`TestTheStreamClientReplacesTheScreenWithText` polices every write in the
  whole file**, not just the stream's: the sink regex rejects any
  `innerHTML`/`outerHTML`/`innerText`/`nodeValue`/`srcdoc` and any `+=`, and a
  separate assertion demands `pane.textContent =` appear **exactly once**. So a
  new module may write `textContent` but must not name its element `pane` while
  doing it. `note.textContent =` is why this one passes.
- **Measurement is a canvas, deliberately not a probe element.** `measureText`
  with the pane's computed `fontSize`/`fontFamily` adds nothing to the document
  at all — a measuring node *inside* the pane would be markup this dashboard put
  into the one element it renders nothing but text. The `font` shorthand is not
  read: engines do not all report it for a rule written with custom properties,
  and an empty one measures the canvas default confidently and wrongly. **There
  is no `@font-face` in this tree** (`TestNoRuleNamesAFontFace`), so metrics are
  stable at `defer` time and nothing waits on a font load.
- **Every comparison in the reveal is written to fail closed.** `Number()` of a
  missing attribute is `NaN` and every `<`/`>` against one is false, so the guard
  is `!(fits >= floor) || !(fits < session)` rather than the readable inverse:
  markup that drifted must leave the form hidden, never reveal it by arithmetic
  accident.
- **Below the floor the offer is withheld rather than clamped.** Naming a width
  and then having the route clamp it would be the page claiming something the
  daemon will not do; `MinColumns` rides in the markup so config keeps its one
  definition. It is unreachable on a real phone (20 columns is ~145px) — what it
  actually guards is a measurement that came back nonsense.
- **It sits above the `<pre>`, not below it.** That is the rename's and the
  continue's argument applied inside the component: a control under a
  fixed-height scroll container is one an operator scrolls past a terminal to
  reach, and on the narrow viewport where this is the only offer that ever
  appears, that terminal is most of the display.
- **Five guards were proven by breaking them**, as the plan requires: dropping
  `hidden`, rendering the form without the token gate, revealing before
  comparing, rendering `config.DefaultPaneWidth` instead of `live.Columns()`, and
  deleting the way-back clause. Each fails at least one new test. Restored and
  green.

**Left:** T006 and T007. Nothing is blocked.

**Findings — noticed, not fixed:**

- **The offer is computed once, at load, and nothing re-runs it.** Rotating a
  phone from landscape to portrait leaves no offer where one now applies, and
  from portrait to landscape leaves a stale one on screen naming a width the
  reader no longer has. A `resize` listener is three lines; what stopped it being
  written is that hiding the control again can pull it out from under a finger
  mid-tap, and that is a decision rather than an omission. **The stale direction
  is the one that matters** — the number in the sentence is the number in the
  hidden field, so a rotated reader could press an offer for a width they no
  longer have. Worth a task, not a silent addition.
- **`docs/components.md`'s Pane viewer section does not know this control
  exists**, and neither does its Action controls table, which still lists four
  routes. **T007 owns it** and should add the reflow to both — the table's "All
  four also answer 400…" sentence needs the count changing with it. Left here
  deliberately rather than done in passing: T007 is the documentation task and
  AR-008 forbids the drive-by.
- **`registeredPatterns` in `settings_test.go` is now stale by seven routes**,
  restated from Iteration 4 because nothing has taken it. Unchanged by this task;
  still worth a task of its own, and still the one guard in the repo quietly
  losing coverage as routes are added.
- **`TestQuickstartStory5RateLimit` fails under full-suite load and passes in
  isolation**, with `unlinkat …/001/home: directory not empty` from `t.TempDir`'s
  cleanup — the assertion itself passes, the burst being `[201 201 201 429 429]`,
  which is what it wants. **This is the third iteration to see it** (Iteration 3
  recorded the identical message). I tried to establish it as pre-existing by
  running the suite at `HEAD` in a detached worktree and **could not complete
  that check**: the worktree fails `-buildvcs` stamping, and the environment
  refused both the `GOFLAGS` re-run and a scratch script, so **"pre-existing" here
  rests on Iteration 3's record and on the changed files, not on a baseline run I
  actually made.** Nothing this task touched runs in `cmd/crswd`'s temp-home path
  — the change is `internal/httpapi` views, one template and one script — but
  that is an argument, not a measurement. Something is still writing under the
  test's temp home while cleanup runs, and it deserves a task.
- **`aria-describedby="card-id-…"` on the reflow button resolves only because
  the session page draws the card.** True today on the one page that renders this
  component; a second page rendering a pane without a card would ship a dangling
  reference, silently. The card's own action row has the same coupling, so this
  is the shape of the tree rather than a defect this control introduced.

---

## Iteration 6 — 2026-09-01 — T006, the second mechanism removed

**Did:** Deleted `white-space: pre-wrap` + `overflow-wrap: anywhere` and the
comment block that named the trade from the 780px block of `web/static/crswd.css`,
and inverted the guard that pinned them. Commit `c66aa38`.

**Learned:**

- **The task was three files, not one, and the third was mandatory.**
  `docs/design-system.md` says in its own words that the breakpoint enumeration
  gains a row in the same commit a rule is added — and it already went stale once,
  saying "two effects" while the block did four. Removal is the same obligation
  read backwards, so the `.pane` row went with the rule and the sentence now says
  both directions out loud. **This is not the T007 documentation sweep**: T007
  owns `README.md` and `docs/components.md`, and both are still stale on purpose.
- **`TestThePaneWrapsOnlyOnNarrowViewports` had to be replaced rather than
  deleted, and the replacement is wider than the thing it replaced.** The old test
  asserted the two declarations existed in the one block allowed to carry them.
  `TestNoPaneRuleWrapsTheTerminalsOutput` sweeps **every** `.pane` rule through
  `cssRules(stylesheet(t))` — which flattens media blocks, `mediaOpen` strips the
  preludes — for `overflow-wrap|word-break|word-wrap` or a wrapping `white-space`
  keyword. A deleted guard would have left the wrap free to return from the
  `(pointer: coarse)` block or a block nobody has written yet.
- **The sweep and `TestThePaneKeepsItsDesktopAlignment` are not redundant, and I
  measured which catches what.** Restoring both declarations fails the sweep;
  `overflow-wrap` alone fails it too; **deleting `white-space: pre` from the base
  rule passes the sweep and fails the alignment test**, because an override that
  declares nothing is legitimate and an absent base declaration inherits `normal`,
  which wraps. Both comments now say exactly that, and both claims were run.
- **`stylesheet(t)` strips CSS comments before every assertion in that file.** So
  the value sweep (`TestNoRuleCarriesAValueThatBelongsInAToken`) cannot see a `px`
  or a hex inside a comment, and prose in `crswd.css` is free to name real
  numbers. Iteration 5's note that `script(t)` does the same for JS is the same
  fact in the other file; **do not anchor an assertion on a comment in either.**
- **Four other tests carried the wrap as their stated reason and were corrected
  in place**, because a live guard whose premise is a deleted declaration is the
  stale-enumeration failure this repo keeps recording:
  `TestThePaneDoesNotChainItsOverscroll` ("even where the pane wraps"),
  `TestThePaneDoesNotTrapVerticalScrolling` ("the breakpoint block, where the wrap
  lives"), `TestThePaneKeepsItsDesktopAlignment` (framed as override-vs-base, a
  distinction that no longer exists) and `TestNoPageClampsTheZoom` (whose guard
  still stands — pinch is now the escape hatch for a reader who has **not**
  reflowed, which is every reader until somebody presses the offer). No assertion
  changed in any of the four.
- **`docs/mobile-open-questions.md` Q1 was ANSWERED and said "the wrap stays".**
  Its fallback was *exactly* these two declarations, so T006 took a fallback the
  operator's answer had declined. It is recorded as superseded rather than
  re-answered: that file's mechanism is that **only the operator's report answers
  a question**, and this is not a verdict on the reading — it is the mechanism
  moving. The answer stands above the note, untouched.
- **Neither tagged suite covers this.** `cmd/crswd` and `internal/tmuxctl`
  reference neither `crswd.css` nor `pre-wrap` (grepped, not assumed). Both were
  compiled with `go vet -tags quickstart` and `-tags tmux`, and
  `go test -tags tmux ./internal/tmuxctl ./internal/session` was run and is green.
  `golangci-lint` is **v2.12.2** (#26's check) and reports 0 issues; `gofmt -l .`
  names nothing; `go test ./...` is green in all 11 packages.

**Left:** T007 alone — `README.md` and `docs/components.md`, then closing #121.

**Findings — noticed, not fixed:**

- **`docs/components.md`'s pane bullet is now false and shipped that way, by the
  plan's ordering.** It still reads "Below the breakpoint the pane wraps, and it
  is a trade rather than a fix", names both declarations, and says "**Reverting is
  two declarations**" — which has now happened. Its Action controls table is also
  still missing the reflow route and still says "All four". **T007 owns both and
  is the next task**; this is a one-iteration window, not a backlog, but if T007
  is deferred for any reason this file is the first thing to fix.
- **`specs/007-make-it-work-on-a-phone/contracts/pane.md` names the guard by its
  old spelling** (`TestThePaneWrapsOnlyOnNarrowViewports`, "carries `white-space:
  pre-wrap` **and** `overflow-wrap: anywhere`"), and `tasks.md` in the same spec
  repeats it. Left alone: `specs/` is the record of what a milestone decided at
  the time, and Iteration 0 established that nothing in `specs/` is a source of
  open work. **Nothing enforces those test names**, so this rots silently rather
  than failing — worth knowing before someone greps for a test that no longer
  exists.
- **The pane is now unwrapped for every reader who has not reflowed, and the only
  thing standing between them and a horizontal pan per line is a JavaScript
  module.** With the script blocked or failed, T005's offer stays `hidden` — which
  is correct, and is #121's rule — but the baseline it degrades to is now the
  *pre-milestone-7* phone experience rather than the wrapped one. That is the
  milestone's stated intent and the operator chose it; it is written down here so
  the next report of "the pane is unreadable on my phone" is diagnosed as *the
  offer did not appear* rather than as a regression in the CSS.
- **`TestQuickstartStory5RateLimit`'s temp-home cleanup race is unchanged and
  unexamined here.** This task touched no Go outside `stylesheet_test.go`, and
  `go test ./...` passed on the first run this iteration. Three iterations have now
  recorded it; it still deserves a task rather than another line.

---

## Iteration 7 — 2026-09-01 — T007, the documentation half. #121 is BLOCKED.

**Did:** Wrote the reflow into `README.md` (a new *Reading a session on a narrow
screen* section under "What it does", plus a bullet) and into
`docs/components.md` (a `### Reflow offer` subsection under Pane viewer, the
falsified wrap bullet replaced, and the reflow added to the Action controls
table), and added `TestTheDocumentsNameTheReflowAndTheWayBack` to hold both to
it. Commit `63f2694`.

**⚠️ T007 is marked `- [!]`, not `- [x]`, and the milestone is NOT complete.**
The task has two halves and only one of them can be done from inside this loop.

### The blocker, stated exactly

**#121 cannot be closed by any iteration of this loop, now or later.**

- `.claude/settings.json` allows `Bash(gh pr:*)`, `Bash(gh repo:*)` and
  `Bash(gh auth status)`. It does **not** allow `gh issue` or `gh api`. Measured:
  `gh issue view 121` was refused as requiring approval.
- A loop iteration is non-interactive, so the approval prompt cannot be answered.
- So this iteration could not even *read* #121, let alone close it. Everything
  said about that issue below is from the plan and from
  `docs/mobile-open-questions.md`, not from the issue itself. **Do not record it
  as verified against the issue text.**

Two ways out, and both are the operator's:

1. Close it by hand. The comment is written out below.
2. Add `Bash(gh issue:*)` to `.claude/settings.json` and re-run. **This widens
   what an autonomous loop may do to a public artifact of this project**, which
   is a decision rather than a convenience, and is why no iteration should add it
   on its own.

**Closing comment for #121, ready to paste:**

> Closing as moot. This issue asked for a wrap/alignment toggle, and the thing it
> was a toggle for no longer exists.
>
> Milestone 16 (#120) moved the wrapping from the stylesheet to the terminal: a
> pane narrower than its session offers a reflow, the daemon resizes the tmux
> window, and the program re-wraps its own screen at the column edge. There is no
> longer a CSS wrap to switch off — `white-space: pre-wrap` and
> `overflow-wrap: anywhere` were deleted from `web/static/crswd.css`, and
> `TestNoPaneRuleWrapsTheTerminalsOutput` now refuses their return from any
> `.pane` rule at any width.
>
> Its prerequisite is also settled: Q1 in `docs/mobile-open-questions.md` was
> ANSWERED on 2026-08-09 and the answer was that the wrapped reading was worth
> its cost *while a stylesheet was the only thing that could do the wrapping*.
> That is no longer the case.
>
> Building the toggle now would be the second mechanism #120 asked to be rid of,
> with a control on top of it. The reflow is documented in `README.md` and in
> `docs/components.md`.

### Learned

- **`docs/mobile-open-questions.md` is a third file with a claim about #121, and
  whoever closes the issue owes it a line.** Q1 ends *"#121 is unblocked by this
  answer and is still not evidenced"*, which stops being true the moment the
  issue is closed as moot. Deliberately not edited here: that file's own
  mechanism is that only the operator's report changes it, and the issue is not
  in fact closed yet, so the sentence is still accurate. **Edit it in the same
  commit as the close, not before.**
- **A prose anchor can pass by accident, and one of mine did.** The first
  spelling of the second-reader assertion was `"every reader"`. Breaking the
  reflow paragraph in `docs/components.md` left the test green, because line 138
  of that file already says *"would tell every reader something untrue"* about a
  settings sentence. The anchor is now
  `"however many people are reading it"` — the *reason* rather than the phrase —
  and the test comment records why. **Every guard in this repo that greps a
  document for prose has this failure mode; break it against a reworded document
  and not only against a deleted one.**
- **`gosec` G304 fires on `os.ReadFile(loopVariable)` even in a test that only
  ever opens two constants.** The two existing document tests
  (`TestTheComponentsDocumentNames…`, `TestTheREADMENamesTheSignInPath`) read a
  single constant each and never met it. The fix is to read through the constants
  and put the paths in a slice of structs — **not** `//nolint:gosec`, which this
  tree does use but which would be a suppression on a file-open for the next hand
  to copy somewhere it matters.
- **The Action controls table's count is gone rather than corrected.** It said
  "The four things the dashboard can change" and "All four also answer 400…"
  while the tree registered continue, update, restart, settings-edit and logout
  as well — so "five" would have been the same defect one milestone later. The
  table is now the enumeration and the sentence carries no number, with one line
  saying why. The status codes in that table (`200`, `202`) are a different
  staleness and were left alone; see findings.
- **`docs/auth-and-sessions.md` was deliberately not touched**, though it is
  stale in the same way. T007 names two files, AR-008 forbids the drive-by, and
  its "Four routes let a browser change this host" sentence was already wrong
  before this milestone started. Finding below.
- **Green here:** `go build ./...`, `go vet ./...`, `go test ./...` (11 packages
  ok), `gofmt -l .` names nothing, `golangci-lint` is **v2.12.2** (#26's check)
  with 0 issues, `go vet` compiles all three tagged suites, and
  `go test -tags tmux ./internal/tmuxctl ./internal/session` is green.

**Left:** the close of #121, and nothing else. Every code task in the milestone
is done and the documentation is written.

**Findings — noticed, not fixed:**

- **`docs/auth-and-sessions.md` line 322 says "Four routes let a browser change
  this host: `POST /dashboard/sessions` and
  `POST /dashboard/sessions/{id}/{destroy,rename,compact}`", and there are now
  six.** It also says "ownership, on each of the three that name a session",
  which is five. It was already stale when spec 013 added `continue`; this
  milestone made it staler by one. The action-gate *rules* underneath it are all
  still correct — it is the enumeration that rotted, exactly as
  `docs/components.md`'s did. **It deserves the same treatment: delete the count,
  keep the list.** Not done here because T007 names two files and this is a third.
- **`docs/components.md`'s Action controls table still reports `200`/`202`
  statuses for four routes that answer `303`.** The Toast section three
  paragraphs later says so plainly — "every one of those routes answers `303`
  (`redirectOutcome`)" — so the document contradicts itself. The reflow's row was
  written to the truth rather than to the table's convention, which makes the
  disagreement visible instead of joining it. Fixing the other four rows is a
  one-line-each edit and was outside this task.
- **`registeredPatterns` in `settings_test.go` is stale by seven routes**,
  restated from Iterations 4 and 5 because nothing has taken it and this
  iteration added no route to it either. Third recording. The fix is to derive
  the list from the mux; it is still the one guard in this repo quietly losing
  coverage as routes are added, and it still wants a task.
- **The offer is still computed once, at load, and nothing re-runs it on a
  rotation** — Iteration 5's finding, unchanged. A phone turned from portrait to
  landscape keeps a stale offer naming a width the reader no longer has, and the
  number in the sentence is the number in the hidden field. The documentation
  written this iteration does not mention it, deliberately: it describes what the
  control does rather than a bug in when it appears.
- **`TestQuickstartStory5RateLimit`'s temp-home cleanup race went unexercised
  again.** This task touched one Go test file in `internal/httpapi` and two
  markdown documents; `go test ./...` passed on every run. **Four iterations have
  now recorded it without anyone looking at it**, which is the copy-forward habit
  Iteration 0 warned about. It should be a task or it should be dropped.

---

## Iteration 8 — 2026-09-01 — the blocker re-measured, and Iteration 7's account of it corrected

**Did:** No code. T007's remaining half is still unreachable, so this iteration
did the only two things that were available: re-measured the blocker rather than
copying it forward, and **corrected a false premise in the way it was written
down**. `ralph/IMPLEMENTATION_PLAN.md` T007 amended.

### The correction, because it changes what the operator is deciding

Iteration 7 wrote — and T007 in the plan repeated — that
`.claude/settings.json` "allows `Bash(gh pr:*)`, `Bash(gh repo:*)` and
`Bash(gh auth status)`". **That is wrong.** Read this iteration: the repo's
`.claude/settings.json` allowlist is fifteen Go entries and eight git entries
and **no `gh` entry at all**. `grep -rn "gh pr\|gh repo\|gh auth\|gh issue"`
over the whole of `.claude/` matches nothing — not settings, not hooks.

The *conclusion* was right and is unchanged: `gh issue` is not permitted, so no
iteration can read or close #121. But the premise mattered, because Iteration 7
offered the operator route 2 — "add `Bash(gh issue:*)` to
`.claude/settings.json`" — described as widening an allowlist that already had
`gh` on it. It does not. **That edit would put the first `gh` entry of any kind
into this repo's allowlist**, which is a larger step than Iteration 7's wording
implies, and the operator should be choosing it knowing that.

Where Iteration 7's `gh pr` / `gh repo` belief came from is not established. It
is not in this repo. It may be from a settings file outside the working
directory; **I could not check** — reads and greps outside
`/home/nctiggy/code/claude-remote-session-webhook` are blocked in this session,
so that is unknown rather than ruled out.

### Re-measured, not copied forward

- `gh issue view 121 --json number,title,state` → **refused, "This command
  requires approval"**. The session is non-interactive, so the prompt cannot be
  answered. The blocker holds exactly as stated.
- Note for whoever tries this next: writing it as
  `gh issue view 121 ... 2>&1 | head -20` was refused *earlier and differently*,
  as "multiple operations". A pipe or a redirect changes the refusal message
  without changing the outcome — **do not read the first refusal as evidence
  about the allowlist**; strip the command to one bare invocation before
  concluding anything from what it says.

### Verified rather than trusted

T007's shipped half was checked against the artifact, not against Iteration 7's
claim of it:

- `TestTheDocumentsNameTheReflowAndTheWayBack` exists and **passes** (run
  singly, `-v`).
- Tree green on all four: `go build ./...`, `go vet ./...`, `go test ./...`
  (11 packages ok, `web` has no test files), `golangci-lint run` **0 issues**
  at **v2.12.2** — #26's version check made, not assumed.
- `docs/mobile-open-questions.md:64` still reads "#121 is unblocked by this
  answer and is still not evidenced" — still accurate, because the issue is
  still open. Iteration 7's instruction stands: **that line is edited in the
  same commit as the close, not before.**

### The loop cannot finish, and that is now structural

Every code task T001–T006 is `[x]`. T007 is `[!]`. `RALPH_COMPLETE` requires
every task checked, and the one unchecked half is an action on a public GitHub
artifact that this loop is not permitted to take. **Further iterations will
produce entries like this one and nothing else.** Two ways out, both the
operator's, unchanged from Iteration 7 — close #121 by hand with the comment
written out above, or grant `Bash(gh issue:*)` knowing it is the first `gh`
grant in the repo. A third exists and is worse: redefine T007 so the close is
not part of it. Recorded, not taken — the plan says closing #121 *is* the point
of the task, and an autonomous loop editing its own definition of done to reach
`RALPH_COMPLETE` is the failure mode the whole notebook exists to prevent.

**Left:** the close of #121. Nothing else, for the eighth time and the last time
a loop iteration can usefully say it.

**Findings — noticed, not fixed:**

- **The four carried-forward findings are deliberately not restated here.**
  `registeredPatterns` stale by seven routes, `docs/components.md`'s `200`/`202`
  rows, `docs/auth-and-sessions.md`'s "Four routes", the offer not recomputing on
  rotation, and `TestQuickstartStory5RateLimit`'s cleanup race are all in
  Iteration 7 and above, unchanged, and none was touched this iteration. Copying
  them down a ninth time would make the notebook longer without making any of
  them more likely to be fixed. **They need a milestone 17, not another line.**
- **This iteration is itself evidence for the rule it just followed.** It
  produced one factual correction and no code, from a full context load. If the
  loop is re-run against this plan unchanged, the next iteration has strictly
  less to find than this one did.
