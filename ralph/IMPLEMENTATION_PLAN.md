# Implementation Plan

**Milestone 16 — the terminal does the wrapping, not the stylesheet.**

> *"80 columns through a ~44-character window meant a horizontal pan per line,
> and prose is most of what a session prints. Wrapping fixed reading and damaged
> alignment… **Reflowing at the PTY is the correct answer.** A terminal resized to
> 44 columns rewraps — the TUI redraws its chrome to fit, and nothing is
> misrepresented, because the program did the wrapping rather than CSS."*
> — #120

Seven tasks. Closes #120 and settles #121.

---

## The mechanics are measured, not assumed

Run on this host against tmux 3.4, on detached sessions, with no client attached —
which is the only shape this daemon ever creates.

| Claim | Measured |
|---|---|
| `resize-window` works on a **detached** session | Yes. `resize-window -t '=crswd-abc:' -x 44 -y 12` → rc 0, `window_width=44`. `PaneTarget` is the right target helper; no third one is needed. |
| tmux **reflows what is already on screen** | Yes, and this is the load-bearing one. An 80-column line already in the pane re-wrapped to 44 **with no new output and no client attached**. The operator does not have to wait for the TUI to repaint before the pane reads. |
| The reflow is the terminal's, not CSS's | Yes — the break lands at the column edge, which is exactly what #120 asks for. |

## ⚠️ The option #120 preferred does not exist

> *"Detach the browser's view into its own tmux client with its own size
> (`new-session -t` shares the session but not the window size). **The third is
> the one I would look at first.**"*

**Measured: it does not hold here.** `new-session -d -t a -s b -x 100 -y 30`
returns 0 and leaves `#{window_width}` at the *original* value for both sessions.
Grouped sessions share the window, a window has one size, and `-x`/`-y` describe a
**client's** size — so they are inert for a daemon that never attaches a client.

That is not a tmux limitation to work around. It is the reason the design problem
#120 states is real and cannot be dissolved: **one window, one width, however many
readers.** The remaining answer is a policy, and the operator chose one.

## The policy: offered, not taken

**The browser reports its width. The daemon never acts on it by itself.**

When a viewer's screen is narrower than the session, the pane offers one control —
*this session is 80 columns, your screen fits 44 — reflow it* — and reflows only
when someone presses it. The width then becomes a **durable property of the
session**, not of a viewer: written to `@crswd-width` beside the five options
adoption already restores, so it survives a restart, and every watcher sees the
same screen because there is only one.

This is the update story applied to a terminal: *a change is visible before it is
taken*, and nothing reflows under a second reader without someone choosing it.
Resize-on-view was rejected for the reason #120 gives — it is hostile to a second
viewer — and a manually pinned width was rejected because making the operator go
and set 44 on a phone is the problem this milestone exists to remove.

---

## ⚠️ `resize-window` permanently flips the window out of automatic sizing

**Measured:** `window-size` reads `latest` before the call and **`manual` after**.
tmux sets it implicitly, and nothing sets it back.

The consequence is not on the browser path at all — it is on the operator's. A
session this daemon has reflowed no longer sizes itself to a terminal that later
runs `tmux attach` on the host: they get a 44-column window in a 120-column
terminal, with nothing on screen explaining why.

**So the reflow is not one command.** Whatever ships must leave the operator a way
back to automatic sizing, and must say — on the page and in the docs — that a
reflowed session stops following the terminal it is attached from. A daemon whose
whole update story is "never change a file the operator edited without saying so"
must not silently change how their terminal behaves either.

---

## What already exists, and what it costs

- **`Controller` (`internal/tmuxctl/controller.go`)** is the interface every other
  package's tests run against. A new method means `Exec`, `Fake`, and an argv
  assertion — three edits, one behaviour. `argvCapturePane` in `fake.go` is the
  pattern to copy: **the argv is built once and both sides read the same builder**,
  which is what makes the assertion meaningful.
- **`PaneTarget`/`SessionTarget` (`target.go`)** already carry the `=` exact-match
  rule. `resize-window` takes a window target and `PaneTarget` is correct for it —
  verified above, not assumed.
- **`@crswd-lifetime` (spec 009)** is the exact precedent for `@crswd-width`: a
  fact that exists nowhere but this daemon, written onto the tmux session so
  adoption after a restart has something to restore rather than a default to guess.
  **Read spec 009's failure before writing T003** — four sessions were destroyed
  because adoption rebuilt a record from what tmux knew and tmux did not know that
  fact.
- **`POST /dashboard/sessions/{id}/continue` (spec 013)** is the newest action
  route and the template for T004: behind `handleAction`, `confirm=yes`, taking
  only what it needs, and **no directory** — the session's own record supplies
  what the caller does not get to choose.
- **`DefaultPaneBound`'s comment is about to become wrong.** It reads: *"a tmux
  session this daemon starts is never attached, so it keeps tmux's 80x24
  default"*. After T001 that assumption is retired by this milestone's own
  feature. It is load-bearing prose — it justifies the number — so it is updated
  where it is falsified, not left for a reader to trip over.

---

## Conventions

- `- [ ]` open · `- [x]` done · `- [!]` blocked (reason in `PROGRESS.md`)
- Priority order is meaningful — the loop always takes the topmost open item.
- **Every task ends green**: `go build ./... && go vet ./... && go test ./... && golangci-lint run`,
  plus `-tags tmux` / `-tags quickstart` where touched.
- **Check the linter is v2 before trusting it** (#26).
- `go.sum` must never appear.
- **AR-008: no refactoring outside the task.**
- **A task is not done when the code exists. It is done when something calls it.**
- **A new guard must be proven by breaking it.**
- **The tagged suites are the ones that matter here.** `-tags tmux` is the only
  thing that can tell you a resize really reflowed; a fake that returns what you
  told it to proves the argv and nothing else. Run it.

---

## Tasks

- [x] **T001** 🔒 Add the resize to `internal/tmuxctl` — `Controller`, `Exec` and
      `Fake` — as `tmux resize-window -t <PaneTarget> -x <cols> -y <rows>`, with
      the argv built by one shared builder like `argvCapturePane`. **The two
      integers are formatted in this package with `strconv` and are the only
      caller-influenced values that have ever reached an argv here** — the package
      header says a request that reaches it has already passed authentication and
      that this boundary is the last one that still holds, so bound them here too
      rather than trusting the handler to have done it. Tests: the argv assertion
      in `fake_test.go`, and a `-tags tmux` case in `exec_tmux_test.go` that proves
      **an 80-column line already in the pane comes back re-wrapped at 44** — the
      measured behaviour above, pinned, because the whole milestone rests on it.

- [ ] **T002** 🔒 Give the width a clamp in `internal/config`, in the shape
      `PaneBound` already has (`EnvPaneBound`/`DefaultPaneBound`, `loadInt`).
      #120's requirement is exact: **advisory only — a bad value clamps, never
      refuses, and never escapes into an argv.** So: a floor and a ceiling, a
      value outside them is silently brought inside, and a value that is not a
      number is the default rather than an error. Fix `DefaultPaneBound`'s comment
      in the same task — it currently justifies 200 lines on the claim that a
      session "keeps tmux's 80x24 default", which this milestone retires. Test the
      clamp at both edges and past both edges.

- [ ] **T003** Make the width durable: `OptionWidth = "@crswd-width"` written onto
      the tmux session when a reflow is taken, and restored by adoption beside the
      five options it already restores. **Read spec 009 first** — a fact this
      daemon knows and tmux does not is exactly what was lost across a restart
      there, and the cost was four destroyed sessions. A session carrying no
      option is 80 columns, which is what every session that predates this
      milestone is. Test: a session reflowed to 44, adopted after a restart, is
      still 44; a session with no option adopts as 80.

- [ ] **T004** 🔒 `POST /dashboard/sessions/{id}/reflow`, behind the existing
      action gate, taking a column count and `confirm=yes` and nothing else — the
      rows come from the session, not from the caller. Model it on spec 013's
      `continue` route, including the audit action. **The clamp is applied here
      and again in T001**; that is deliberate duplication at a trust boundary, not
      drift. Test the negative cases the way `docs/security.md` requires of an
      action route — wrong owner, missing confirm, a width that is a word, a width
      that is negative, a width of nine million — and none of them may 500.

- [ ] **T005** Offer it in the pane, reusing what `docs/components.md` already
      defines — **the pane viewer and the existing action controls, no new
      component**. The browser reports its own width; the control appears only
      when that width is narrower than the session's, names both numbers, and
      **says that a reflowed session stops sizing itself to a terminal attached on
      the host** (the ⚠️ above), with the way back. Progressive enhancement only:
      **the baseline must still be a pane that works with no JavaScript**, and
      #121's rule stands — the control must not become the thing that makes the
      pane function.

- [ ] **T006** Remove the CSS wrap. Two declarations — `white-space: pre-wrap` and
      `overflow-wrap: anywhere` on `.pane` inside the `@media (max-width: 780px)`
      block of `web/static/crswd.css` — and the comment block above them that
      names the trade, which stops being true the moment the terminal does the
      wrapping. #120 is explicit: *"the CSS wrap becomes unnecessary and should be
      removed rather than left as a second mechanism doing the same job worse."*
      **This task is last among the code tasks on purpose**: removed before the
      reflow works, it is a straight regression on a phone. The base rule's
      `white-space: pre` is what the pane returns to.

- [ ] **T007** Document it and settle #121. `README.md` and
      `docs/components.md`: what a reflow does, that it is per session and not per
      viewer, that it survives a restart, and that it takes the window out of
      automatic sizing until it is put back. Then **#121 — the wrap/alignment
      toggle — is moot and should be closed saying why**: it existed to escape a
      CSS wrap that no longer happens, its own prerequisite (Q1) is answered, and
      T006 removes the thing it was a toggle for. Closing it is the point of the
      task; leaving it open is a second mechanism waiting to be built.

---

## Out of scope

- **Resizing on view, or any reflow no one pressed.** #120 names it hostile to a
  second viewer and the operator chose against it. A width changes when somebody
  changes it.
- **Grouped sessions / a client per viewer.** Measured inert for detached
  sessions. Do not spend an iteration rediscovering that.
- **Height.** #120 is about columns. Rows come from the session and
  `DefaultPaneBound` already bounds what a capture may return.
- **A settings-page control for the width.** It is a property of one session, set
  where that session is read.
- **`docs/mobile-open-questions.md` Q2.** Still UNANSWERED and still the
  operator's — and spec 014 changed the layout it was asked about, so it needs
  re-reading before it is answered, not answering here.
- **The config migration running in the old binary.** Recorded in
  `ralph/PROGRESS.md`; a spec question, not this milestone's.
