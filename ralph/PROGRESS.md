# Progress — milestone 17

**`lm-86` — re-auth a logged-out session from the phone (needs-auth relay).**

Milestone 16's notebook is archived at `ralph/archive/2026-09-08/PROGRESS.md`
alongside the plan and prompt that produced it. This file starts empty on purpose:
the loop's exit sentinel is a whole-line match on this file, so a notebook carried
over from a finished milestone stops iteration 1 before it does anything.

Append to the bottom. One section per iteration.

---

## Iteration 0 — planning (not a loop iteration)

Wrote `ralph/VALIDATION_CONTRACT.md`, `ralph/IMPLEMENTATION_PLAN.md`,
`ralph/PROMPT.md` and this file. No source file was changed. What follows is what
was read out of the repository so that no iteration pays for it twice.

### Measured, not assumed

- **A logged-out session renders `running` today.** `Session.DisplayState`
  (`internal/session/session.go:552`) answers `DisplayRunning` for anything that is
  not `StateFailed`, and no entry in `dialogSignatures` matches the login screen.
  So the board reports a dead fleet as healthy, which is why T001 is first and
  worth shipping even alone.
- **The CSP permits the sign-in link.** `internal/httpapi/render.go:59` is
  `default-src 'none'; script-src 'self'; style-src 'self'; connect-src 'self';
  img-src 'self' data:; base-uri 'none'; form-action 'self'; frame-ancestors 'none'`.
  There is **no `navigate-to`** directive, so an `<a href>` to an external origin
  navigates fine. `form-action 'self'` only constrains form posts, and every form
  in this milestone posts back to this daemon.
- **A literal `https:` anywhere in a template fails the build.**
  `forbiddenInTemplates` (`internal/httpapi/partials_test.go:1516`) sweeps the
  whole embedded tree for `(?i)https?:|//cdn|srcset`, and
  `TestNoTemplateCarriesAValueThatBelongsInATokenOrAnOrigin` fails on a match.
  `{{/* … */}}` comments are stripped before the sweep, so prose *about* the URL is
  fine. **The handler supplies the whole URL and the markup holds only an action.**
  The `claude.com` host allowlist literal lives in Go, not in markup.
- **The fleet grid captures no pane per card.** `internal/httpapi/dashboard.go:311`
  passes `""` as `paneText` to `cardOf`; the comment at `:465` states it. The
  session page does capture one (`:624`, `pane.Text`), which is the capture the URL
  scrape should read — there is no need for a second one.
- **The session stream ships pane text, never a re-rendered card**
  (`internal/httpapi/stream.go:425`). So a `needs-auth` pill appears on page load
  and not mid-stream.
- **`.claude/settings.json` allows Go and git commands only.** No `curl`, no
  `tmux`, no `gh`, no `bash`. Milestone 16's T007 ended half-done on exactly this
  and re-running the loop could not clear it. Plan accordingly: every task in this
  milestone is verifiable with `go test`.

### A correction to the task text, decided rather than left open

The task says `internal/session/session_test.go:301` "asserts it is deliberately
*not yet* valid" and that "that test is the one to flip". **It should not be
flipped.** That row asserts `State("needs-auth").Valid() == false`, and `State` is
the *stored lifecycle* field that FR-019a forbids the dashboard reading at all.
Every pane-derived label this daemon has — `blocked`, `unknown` — is a
`DisplayState` composed per render by `effectiveDisplayState`
(`internal/httpapi/dashboard.go:511`), never written to the store.

So `needs-auth` joins `DisplayRunning`, `DisplayFailed`, `DisplayBlocked` and
`DisplayUnknown` as a `DisplayState`. `State.Valid()` keeps returning false for it,
and T002 rewrites that row's comment to say why rather than deleting it. Storing
the label would give the reaper, adoption and the journal each an opinion to hold
about a state that only a render can know.

### Two more decisions taken up front

- **Detection lands in `internal/session`, beside `DetectDialog`.**
  `docs/auth-and-sessions.md:488` sketches an `internal/claudeauth` package holding
  a single `DetectPrompt`. The composition point that turns a pane into a label
  already exists and already calls into `internal/session`; a second package would
  need a second call site and a second thing to keep in step. T010 updates the doc
  to name where detection really lives rather than leaving the doc describing a
  package that is not there.
- **`/status` was rejected as the expired-login signal.** Its `Login` row reads
  `Expired — log in again` on Claude Code v2.1.210+ and is a cleaner string, but
  crswd can only obtain it by typing `/status` into the pane — a side effect on the
  thing being measured, on a schedule, into a session an operator may be reading.
  Scraping the error line is passive, and if the string later moves it is a
  one-line fix in one file.

### Findings noticed and deliberately not fixed here

- **`ralph/archive/2026-09-08/` is untracked.** It holds byte-identical copies of
  milestone 16's three files (verified with `diff`), so nothing was lost when this
  plan overwrote them — but it is not committed yet. Whoever opens this milestone's
  branch should stage that directory alongside these four files.
- **#121 is still open** and milestone 16's T007 says closing it is the last thing
  between that milestone and its sentinel. No iteration can close it: `gh` is not
  on the Bash allowlist. The closing comment is written out ready to paste in
  `ralph/archive/2026-09-08/PROGRESS.md`, Iteration 7.
- **`summarised` (`internal/httpapi/dashboard.go:190`) holds only
  `DisplayRunning`.** `blocked` and `unknown` are already absent from it and this
  has never mattered, because the fleet grid derives no pane-based state. If the
  fleet ever does capture panes, that list and `recount` in `web/static/crswd.js`
  (which reloads the page on a card whose state has no summary row) both become
  live. Out of scope here; recorded so it is not a surprise later.
- **`specs/004-configure-and-operate/spec.md:268` lists this relay under "Out of
  Scope".** That was true of milestone 4 and the file stays as written; `specs/` is
  deliberately outside this milestone's files-touched allowlist.

### The readiness gate rejected the first draft — do not undo the fix

The gate reads **only the first line** of a task and of a contract assertion. The
first draft wrapped both across several lines with `Verify:` at the bottom, so it
scored every one of the ten tasks and all thirteen bullets as having no way to be
checked, and refused to dispatch. Two consequences, both load-bearing:

- **Every task in `ralph/IMPLEMENTATION_PLAN.md` and every assertion in
  `ralph/VALIDATION_CONTRACT.md` is now a single line naming its own command**,
  each under the gate's 400-character task limit. Do not re-wrap them for
  readability — that is exactly what got the plan rejected. Context belongs in the
  prose sections above the task list, not in the task line.
- **The gate resolves every path in the *Files touched* list against the working
  tree**, so the two files T003 has yet to create cannot be listed there; the
  package directory is listed instead, with the restriction spelled out beside it.
  Body prose is not path-checked — `internal/claudeauth` and a `partials/` path
  both go unremarked — so naming the new files inside T003 is safe.

The two bullets under *What this contract does not claim* were counted as
assertions (13 bullets, not 11) and are now prose. Anything written as `- ` in
that file is read as a checkable claim.

### What is left

All ten tasks, T001 first. And after them, the one thing the loop cannot do: a
live re-auth end to end, from a phone, against a genuinely logged-out session on a
real host. That is the operator's gate, it is stated in
`ralph/VALIDATION_CONTRACT.md` under *What this contract does not claim*, and no
iteration may report it as done.
