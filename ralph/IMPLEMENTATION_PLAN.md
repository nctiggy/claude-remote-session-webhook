# Implementation Plan

**Milestone 17 — re-auth a logged-out session from the phone (`lm-86`).**

> *"I am getting logged out of claude on the remote VM and crswd has no way for me
> to re-auth without ssh'ing in. That is inconvenient when I am not at a computer.
> Having a mechanism to do that with would be nice in crswd."*
> — Craig, 9/8/26

Ten tasks. `ralph/VALIDATION_CONTRACT.md` is what done means; this file is how.

---

## Nothing here is new design

`docs/auth-and-sessions.md:479-497` specified this relay and `specs/004-configure-and-operate/spec.md:268`
deferred it. It has been deferred every milestone since. What was missing was
captured evidence of the pane, and that now exists.

**Decided 9/8/26, and not re-openable:** stay on the subscription `/login`. API
keys and workload identity federation move billing off the subscription and are
off the table — not "later", not "for workers". Do not plan around a metered
credential. That makes this the only path to unattended-friendly auth.

## The captured pane — real strings, not invented

Taken 9/8/26 against an isolated `CLAUDE_CONFIG_DIR` on a private tmux socket, so
no credential on the box was touched and the operator's tmux server was never used.

```
 Claude Code can be used with your Claude subscription or billed based on API usage through your Console account.
 Select login method:
 ❯ 1. Claude account with subscription · Pro, Max, Team, or Enterprise
   2. Anthropic Console account · API usage billing
```

then, after selecting:

```
 Browser didn't open? Use the url below to sign in (c to copy)
https://claude.com/cai/oauth/authorize?code=true&client_id=…&code_challenge=…&state=…
 Paste code here if prompted >
```

`Select login method:` and `Paste code here if prompted` are the stable anchors.
**The URL line is not an anchor** — it carries a live challenge that differs every
time. `internal/session/dialog.go`'s own header demands captured text rather than
invented phrases, and this is why: a signature that does not match is worse than
none, because it looks like coverage.

---

## The mechanism detail that changes the build

An expired credential does **not** put an existing session on the device-code
screen. Once the stored login expires and cannot be refreshed, *each model request
fails with `Login expired · Please run /login`*. The login screen appears only on
a **fresh** `claude` start, or after someone types `/login`.

So the relay needs **two** detectors and one control:

1. **`login-expired`** — an already-running session erroring on every request.
2. **`claude-login`** — the device-code screen itself, captured above.
3. **A control that sends `/login` into the pane**, so an operator on a phone can
   *produce* the device-code screen from (1) and reach (2).

Without (1) and (3), a session that dies mid-flight sits erroring and the relay
never triggers, because the thing it watches for never appears. That is the
difference between this working for Craig and only working on a cold start.

**`/status` was considered as the signal for (1) and rejected.** Its `Login` row
reads `Expired — log in again` on Claude Code v2.1.210+ and is a cleaner string,
but crswd can only get it by typing `/status` into the pane — a side effect on the
very thing being measured, on a schedule, into a session an operator may be
reading. Scraping the error line is passive. If a capture later shows the error
string has moved, that is a one-line fix in one file; typing into panes to take a
reading is not.

---

## Four measurements taken before this plan was written

Do not spend an iteration rediscovering these.

| Claim | Measured, 9/8/26 |
|---|---|
| A logged-out session renders `running` today | Yes. `Session.DisplayState` answers `DisplayRunning` for anything not `StateFailed`, and no signature in `dialog.go` matches the login screen. The board reports a dead fleet as healthy. |
| The CSP permits the sign-in link | Yes. `internal/httpapi/render.go:59` is `default-src 'none'; … form-action 'self'; frame-ancestors 'none'` with **no `navigate-to`**, so an `<a href>` to an external origin navigates. `form-action 'self'` only constrains form posts, which all stay on this daemon. |
| A literal `https:` in a template fails the build | Yes. `forbiddenInTemplates` (`internal/httpapi/partials_test.go:1516`) sweeps the embedded tree for `(?i)https?:` and `TestNoTemplateCarriesAValueThatBelongsInATokenOrAnOrigin` fails on a match. Template **comments are stripped first**, so prose about the URL is fine. **The handler supplies the whole URL; the markup holds only `{{ … }}`.** The host allowlist literal lives in Go. |
| The fleet grid captures no pane per card | Yes — `internal/httpapi/dashboard.go:311` passes `""` to `cardOf`, and the comment at `:465` says so. The session stream ships pane text only, never a re-rendered card. |

---

## ⚠️ The task's own instruction to flip `session_test.go:301` is wrong, and T002 says why

That row asserts `State("needs-auth").Valid() == false`. `State` is the **stored
lifecycle** field, and FR-019a forbids the dashboard reading it at all. Every
pane-derived label this daemon has — `blocked`, `unknown` — is a `DisplayState`
composed per render by `internal/httpapi`'s `effectiveDisplayState`, never stored.

**`needs-auth` is a `DisplayState`.** `State.Valid()` and that test row stay
returning `false`; what changes is its comment, which must stop saying "milestone
4, deliberately not yet" and start saying that this is derived and not stored.
Storing it would put a label in the store that the reaper, adoption and the
journal would each then have to have an opinion about.

## What already exists, and what it costs

- **`DetectDialog` (`internal/session/dialog.go`)** returns `(name, dialog)` and is
  already composed into `effectiveDisplayState`. A new signature is one entry plus
  a fixture; there is no second call site to wire.
- **`.pill-needs-auth` already exists** (`web/static/crswd.css:1095`), coloured
  from `--state-auth`, and `partials/status-pill.html` derives its class from the
  value. `TestEveryDocumentedStateHasARule` and
  `TestTheStatusPillAlwaysCarriesItsLabelAsText` already assert both. **The pill
  needs no edit.** That is the claim those tests were written to make; T010 checks
  it rather than assuming it.
- **`continueFromBrowser` / `reflowFromBrowser` (`internal/httpapi/actions.go`)**
  are the two newest action routes and the template for T005 and T006: behind
  `handleAction`, `confirm=yes`, `Manager.View` for ownership, an audit action, a
  `redirectOutcome`. Copy the shape, including the negative cases.
- **`Manager.Prompt` (`internal/session/manager.go:1019`)** is the delivery path —
  `tmux load-buffer` then `paste-buffer` then `send-keys Enter`. Its error names
  the session and never the text, because prompt text is secret under
  `docs/security.md` §3. The relayed code inherits that rule exactly.
- **`internal/audit/leak_test.go`** drives every operation with unmistakable values
  and reads back both sinks. It is the enforcement of "never log the code", and
  T007 is the task that makes it cover this milestone.
- **`sessionPage` already captures the pane** and hands `pane.Text` to `cardOf`
  (`internal/httpapi/dashboard.go:624`). The URL scrape reads that same capture —
  **no second capture, and no capture on the fleet path.**

---

## Conventions

- `- [ ]` open · `- [x]` done · `- [!]` blocked (reason in `ralph/PROGRESS.md`)
- Priority order is meaningful — the loop always takes the topmost open item.
- **Every task ends green**: `go build ./... && go vet ./... && go test ./... && golangci-lint run`.
- **AR-008: no refactoring outside the task.**
- `go.sum` must never appear.
- **A task is not done when the code exists. It is done when something calls it.**
- **Never a captured phrase invented from memory.** T001's strings are the ones in
  this file. T004's string is quoted from Claude Code's own documentation and its
  comment must say so — it goes in its own registry, not in `dialogSignatures`,
  which requires a pane capture.
- **The relayed code is a live credential.** Never audited, never logged, never
  rendered back into a page, never named in an error (`docs/auth-and-sessions.md:491`).
- **Never auto-submit anything the operator did not type** (`:494`).

---

## Tasks

Each task is one line, and each line names the command that proves it. The
context a task needs is in the sections above, not in the task line.

- [ ] **T001** Add a `claude-login` signature to `internal/session/dialog.go` from the capture above — anchors `Select login method:` and `Paste code here if prompted`, never the URL line, which carries a live challenge — with both captured panes as fixtures in `dialog_test.go`. Verify: `go test ./internal/session/` passes and each fixture pane returns the name `claude-login`.

- [ ] **T002** Add `DisplayNeedsAuth DisplayState = "needs-auth"` to `internal/session/session.go` and map the `claude-login` signature to it in `effectiveDisplayState`, leaving `State.Valid()` and the `session_test.go:301` row false and rewriting that row's comment to say needs-auth is derived per render, never stored. Verify: `go test ./internal/session/ ./internal/httpapi/`.

- [ ] **T003** Add a new `login.go` and `login_test.go` to `internal/session` that scrape the sign-in URL from captured pane text, accepting only scheme `https` and host `claude.com` and reporting whether a code box is showing, never logged and never named in an error. Verify: `go test ./internal/session/` — the fixture yields the URL; a `javascript:` or wrong-host line yields nothing.

- [ ] **T004** Detect an already-running session failing every request with `Login expired · Please run /login`, in its own registry in `login.go` and **not** in `dialogSignatures`, which requires captured text, with a comment stating the string is quoted from Claude Code's documentation, and render it as needs-auth too. Verify: `go test ./internal/session/ ./internal/httpapi/`.

- [ ] **T005** Add `POST /dashboard/sessions/{id}/login` behind `handleAction`, modelled on `continueFromBrowser` — `confirm=yes`, ownership via `Manager.View`, delivery of the constant `/login` via `Manager.Prompt`, a new audit action and outcomes. Verify: `go test ./internal/httpapi/` — wrong owner, missing confirm and an unknown id each refuse without a 500.

- [ ] **T006** Add `POST /dashboard/sessions/{id}/auth-code` delivering the pasted code byte for byte via `Manager.Prompt` — never audited, logged, echoed into a page or named in an error, an empty field refused rather than padded, and nothing submitted the operator did not type. Verify: `go test ./internal/httpapi/`.

- [ ] **T007** Drive both new routes from `driveEveryOperation` in `internal/audit/leak_test.go` with an unmistakable code marker and assert where it does and does not reach. Verify: `go test ./internal/audit/` — the marker is in the pasted bytes and in no trail record and no log line.

- [ ] **T008** Render the relay on `web/templates/session.html` beside the rename and continue `<details>` — the scraped URL as a tappable link, a code field, and a `/login` form for a login-expired session — reusing `.button` and `.field-*`, adding no component and no literal `https:` to markup, shown only while parked. Verify: `go test ./internal/httpapi/`.

- [ ] **T009** Add a case to `internal/session/supervisor_test.go` proving the supervisor does not fight the relay: a session parked on the login screen is not stopped, so it must be neither revived nor marked failed while a code is being relayed. Verify: `go test ./internal/session/` — the fake records no restart for a parked session.

- [ ] **T010** Update `docs/auth-and-sessions.md` (drop "not built yet", name where detection landed), `docs/design-system.md`, `docs/components.md` and `README.md`, stating that the grid captures no pane so needs-auth shows on the session page first, and confirm the existing pill needed no edit. Verify: `go test ./...` and `golangci-lint run`.

---

## Files touched

The blast radius. A diff outside this list is rejected.

**Go — detection and state**
- `internal/session/dialog.go`
- `internal/session/dialog_test.go`
- `internal/session/session.go`
- `internal/session/session_test.go`
- `internal/session/supervisor_test.go`
- `internal/session/` — the five files above, **plus the one new source file and
  its test that T003 adds inside this package**. No other existing file in the
  package may change; the directory is listed only because a file that does not
  exist yet cannot be listed as a path.

**Go — routes, rendering and the trail**
- `internal/httpapi/actions.go`
- `internal/httpapi/actions_test.go`
- `internal/httpapi/dashboard.go`
- `internal/httpapi/dashboard_test.go`
- `internal/httpapi/middleware.go`
- `internal/httpapi/middleware_test.go`
- `internal/httpapi/outcome.go`
- `internal/httpapi/outcome_test.go`
- `internal/httpapi/partials_test.go`
- `internal/httpapi/server.go`
- `internal/httpapi/server_test.go`
- `internal/httpapi/stylesheet_test.go`
- `internal/httpapi/view.go`
- `internal/audit/audit.go`
- `internal/audit/audit_test.go`
- `internal/audit/leak_test.go`

**Web**
- `web/templates/session.html`
- `web/static/crswd.css`
- `web/static/crswd.js` — only if a sweep demands it. The relay is a plain form
  and needs no script; a diff here that is not forced by a failing test is
  outside the milestone.

**Docs and the notebook**
- `README.md`
- `docs/auth-and-sessions.md`
- `docs/components.md`
- `docs/design-system.md`
- `docs/security.md`
- `ralph/IMPLEMENTATION_PLAN.md`
- `ralph/PROGRESS.md`

`specs/` is **not** in the allowlist. `specs/004-configure-and-operate/spec.md:268`
lists this relay under "Out of Scope" and it stays as written — that was true of
milestone 4 and editing a shipped spec to match a later milestone is revisionism.
`docs/auth-and-sessions.md` is where the change of status is recorded.

---

## What the loop cannot do

Recorded here because an iteration that discovers it mid-task has wasted itself.

**`.claude/settings.json` allows Go and git commands only.** There is no `curl`,
no `tmux`, no `gh`, no `bash`. So no iteration can start a real logged-out Claude,
drive a browser, or open an issue. Milestone 16's T007 ended half-done on exactly
this, and the loop could not clear it by re-running.

The consequence for this milestone: **the last line of `ralph/VALIDATION_CONTRACT.md`
— a live re-auth, end to end, against a genuinely logged-out session — is the
operator's step and not a task in this plan.** Every task above is verifiable with
`go test`, and the plan is complete when they all pass; the milestone is *proven*
when Craig runs the relay from a phone against a real logged-out session. Do not
write a task claiming to have done that, and do not mark the plan complete with a
sentence implying it happened.

## Out of scope

- **API keys, workload identity federation, and any metered credential.** Decided
  against 9/8/26 and not to be re-opened or planned around.
- **Capturing a pane per card on the fleet grid.** It is one tmux exec per card on
  every render of the page an operator leaves open, which is a cost decision of
  its own. T010 documents the consequence instead: a parked session may read
  `running` on the grid until its own page is opened.
- **Pushing needs-auth down the fleet stream.** The stream ships pane text, not
  re-rendered cards; changing that is the same decision as the row above.
- **A second detection package.** `docs/auth-and-sessions.md:488` sketches an
  `internal/claudeauth` holding `DetectPrompt`; detection is landing beside
  `DetectDialog` in `internal/session` because that is where the one composition
  point already is, and T010 updates the doc to say where it really lives.
- **Reading the code back to the operator, or completing it for them.** The daemon
  relays. It does not decide.
