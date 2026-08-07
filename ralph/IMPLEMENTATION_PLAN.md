# Implementation Plan

> Worked top-to-bottom by `ralph/loop.sh` — one task per iteration.
>
> **This is milestone 5.** Milestones 1 through 4 are complete, reviewed, and deployed;
> their task lists are archived under [`archive/`](archive/) because `PROGRESS.md`
> references their T-numbers, and the notebook itself is at
> [`archive/progress-milestones-1-4.md`](archive/progress-milestones-1-4.md).

## Status: generated from the spec

Generated from [`specs/005-finish-the-dashboard/tasks.md`](../specs/005-finish-the-dashboard/tasks.md),
which is the single source of truth. `spec.md`, `plan.md`, `research.md`, `data-model.md`
and the six files in `contracts/` supersede anything this file summarises.

**Before starting a task, read its matching `T0NN` entry in `tasks.md`.** The entries below
are the ordered checklist; the task file carries the exact literals, the test each task must
include, and — for every task that adds behaviour — **the condition under which that test
must fail**.

## ⚠️ The obligation this milestone exists for

**A requirement about what an operator SEES needs a test that reads the RENDERED MARKUP.**

Milestone 4 wrote FR-026: *"choose remote control as a mode, rather than selecting a command
by name."* Three tasks shipped for it — derive the mode, add the route, show it on the card —
all green. **The create form still renders a dropdown of command names**, because every
assertion was about a route or a record and none read the form.

That is not a testing-volume problem. It is testing the wrong layer. Where a task below
carries the failing condition *"the route accepts the right value but the form still renders
the old control"*, that phrasing is the point and must not be softened.

## ⚠️ The create-form ordering is strict

**Four stories touch `web/templates/partials/create-form.html`.** They run in this order and
are never interleaved — each leaves the file green, so a failure names one story:

```
T001 delete  →  T003 replace  →  T006 feed  →  T008 wrap
```

A task that tidies that file while it is there makes the next story's diff unreadable. AR-008
is load-bearing this milestone.

## 🔒 Three tasks are security-relevant

| Task | Why |
|---|---|
| **T004** `remote_control` validation | The field carries a **mode**, not a name. A real configured name like `rc` must still be refused, or the browser regains the ability to name what runs. |
| **T007** suggestion validation | A path in the datalist but outside `allowed_roots` must be refused identically to a typed one. A suggestion is never an authorisation. |
| **T014** probe honesty | A check that says "missing" about a command that works trains an operator to ignore it, which is worse than not checking at all. |

## What is already running

Milestones 1 through 4 are **live**. Changes here land on a deployed daemon:

| | |
|---|---|
| Service | `crswd.service`, systemd user unit, loopback `127.0.0.1:8765` |
| tmux | **Its own server**, `-L crswd-<listen>` — never the operator's default server (#22) |
| Public | `https://crswd.craigcloud.io` via the `crswd` Cloudflare Tunnel |
| Daemon | Config file, settings page, mode toggle, post-redirect-get, directory picker |
| Audit | `journalctl --user -u crswd -o cat \| jq .` — **which does not work; that is T015** |
| CI | `go test`, `-tags tmux` and `-tags quickstart` all run on the self-hosted runners (#87) |

**Two tasks fix defects in code milestone 4 shipped**, both found by *running* the daemon
during planning rather than reading it. T014 fixes a probe that has warned on every start that
`claude` is missing while sessions using it work. T013/T015 fix an audit trail that cannot be
read because diagnostics share a stream with records.

## Resolved decisions

Settled in [`research.md`](../specs/005-finish-the-dashboard/research.md). **Do not
re-litigate these in an iteration** — if one looks wrong, write it in `PROGRESS.md` under
`NEEDS CLARIFICATION` and stop.

| Question | Decision | Consequence |
|---|---|---|
| What is the remote-control control? | A single **checkbox**, `name="remote_control"`, `value="on"`, styled as a switch | The platform's own two-state control: keyboard-operable and announced correctly with no script. An unticked box posts nothing, so a lost field yields the *less* privileged mode |
| What does a default install offer as directories? | **The approved roots themselves**, always; union with the explicit list and, when enabled, discovered children | The roots are the one source guaranteed non-empty whenever a session can be created, and they disclose nothing the operator did not already configure. Their *children* read the filesystem, which is what `discover_roots` exists to keep opt-in |
| Where does the settings link go? | In `.masthead-bar` **after** the operator and **outside** the `<h1 class="brand">` | #46 made the wordmark the link home and it lives in the page's one first-level heading. A second anchor there would make the heading a menu. One link is not a nav bar |
| Native control or scripted one? | **Enhance the native one** — script layers a themed listbox over `<input list>` + `<datalist>` | Milestone 4's R6 chose native and was right on what it weighed; it missed that a datalist popup cannot be styled by any CSS. Enhancing rather than replacing means script failure costs the theme and never the ability to pick or type a directory |
| Where do the ARIA roles live? | **Added by script, never in the template** | Without script `aria-expanded` would describe a control that is not there. Markup that lies to a screen reader is worse than markup describing the plain field that exists |
| Does `conversation.go` survive the field it fed? | **Deleted**, with its SHA recorded in #95 | Keeping it would be the fifth caller-less thing this repo has shipped. It also likely does not fit #95, which needs *this session's* conversation while `listConversations` answers about *this directory's* — the ambiguity FR-032 already refuses to resolve by guessing |
| Why does the documented audit command fail? | The daemon's **own** diagnostics share stdout with its records | Measured: `_COMM=crswd` still fails, which is how the cause was identified — #88 assumed the noise was systemd's. Fix is diagnostics to stderr, and the filter works only because of that |
| Why does the dependency probe warn falsely? | It asks the **service manager's** PATH; the command runs in a **login shell** inside tmux | `claude` is at `~/.local/bin/claude`, which the login shell has and the service manager does not. Both answers are right about their own environment; the check asks the wrong one |

## Conventions

- `- [ ]` open · `- [x]` done · `- [!]` blocked (reason in `PROGRESS.md`)
- Priority order is meaningful — the loop always takes the topmost open item.
- Each task must be independently completable **and verifiable** in one iteration.
- **Every task ends green**: `go build ./... && go vet ./... && go test ./... && golangci-lint run`.
  Add `-tags tmux` when the task touches tmux and `-tags quickstart` when it touches
  `cmd/crswd`. Tests ship inside the task that implements the behaviour — never as a separate
  failing test, which step 6 of `PROMPT.md` would make the iteration revert.
- **Check the linter is v2 before trusting it.** A pre-v2 binary reads this repo's v2 config,
  runs zero linters, and exits 0 — a green that means nothing. The session-start hook warns
  when this is the case; believe it (#26).
- `go.sum` must never appear. An import needs justification under `docs/security.md` §5 first.
- **AR-005: a test satisfies the cross-site checks, it never disables them.**
- **AR-008: no refactoring outside the task**, however obvious the improvement.
- **A task is not done when the code exists. It is done when something calls it.** This repo
  has shipped that failure four times — a reaper with no caller, `Store.Touch` with no caller,
  a PR-opener no workflow invoked, and `CRSW_DESTROY_ON_SHUTDOWN`, which was false on every
  daemon that ever ran. **T005 is where that bites**: a config key with no reader is exactly
  that fourth one again.

---

## Tasks

### US5 — Stop asking a question nobody can answer (first, because it deletes)

- [x] T001 Remove the resume field and the `Conversations` view data
- [x] T002 Delete `internal/session/conversation.go`; record the SHA in #95
  - ⚠️ **The SHA is `ef18756` and it is NOT yet on #95.** `gh` is not an approved
    command in the loop's session, so the comment could not be posted. The text to
    post is in `PROGRESS.md` iteration 2 — a human or an iteration with `gh` should
    paste it. The code is recoverable from git either way; #95 is where someone
    will look for it.

### US1 — Remote control at create time (the milestone-4 miss)

- [x] T003 Replace the start-command `<select>` with the `remote_control` switch
- [x] T004 🔒 Accept `on` or absent; refuse everything else, including a real command name
  - Both of T003's open questions are answered in `PROGRESS.md` iteration 4. **`on` with no
    remote command configured is refused**, which is the rule `config.go:124`,
    `Manager.commandForMode` and `refuseBrowserCreate` already state — not a new decision.
    **The switch still renders unconditionally**: that conditional needs a view field nothing
    specifies, and it is now a UX wart rather than a hole, because the route refuses honestly.
    `data-model.md`'s `RemoteDefault bool` is still unowned and is a no-op (`false` is what an
    unchecked box already is). Neither blocks a remaining task; **T016 or milestone 6**.

### US2 — Suggestions that exist on a default install

- [x] T005 Add the `workdir_suggestions` configuration key
  - The key is read and reaches `Config.WorkdirSuggestions`; **nothing consumes it until
    T006's union**. Refusals are only what no configuration could accept — a relative
    entry and an empty one. A path outside the roots loads and is offered, exactly as
    `contracts/directory-suggestions.md` requires; the create refuses it. Duplicates
    *within* the list survive on purpose: dedup belongs to T006 and is stated once.
- [x] T006 Union the three sources in `internal/config/suggestions.go`
  - `Config.SuggestedWorkDirs()`, read by `dashboard.go:254`. **A `<datalist>` now renders
    on every real page**, so `TestTheRenderedFleetOffersWhatDiscoveryFound`'s "discovery off
    means no list at all" half was rewritten to "offers the root, not the root's child" —
    that was the task, not collateral. The walk's silent `maxDiscoveredWorkDirs = 200` no
    longer bounds what a page can carry, since the roots and the explicit list are added on
    top of it; see `PROGRESS.md` iteration 6, **T016** or a `NEEDS CLARIFICATION`.
- [x] T007 🔒 A suggested path outside the roots is refused identically to a typed one
  - `TestSuggestedPathOutsideRootsRefused` in `actions_test.go`, on **one coherent
    configuration** rather than milestone 4's page/allowlist divergence: `workdir_suggestions`
    is unconstrained by the roots by contract, so a real daemon offers the path. Test-only —
    the refusal already existed. **What it pins is the handler, not the wiring**: every
    `internal/httpapi` fixture injects `fixture.mgr`, so a `server.go:332` that fed
    `SuggestedWorkDirs()` into the manager's roots would go unnoticed here *and* in quickstart.
    See `PROGRESS.md` iteration 7 — **T016** or milestone 6.

### US4 — Controls that belong to this interface

- [x] T008 Markup: the `.combo` wrapper, the listbox, the status region — **behaviour unchanged**
  - Three minimum CSS rules shipped with it, because `TestTheStylesheetAndTheMarkupNameTheSameThings`
    sweeps both directions and a rendered class with no rule is red — the same reason T003
    carried `.switch-input`. **`.combo { display: grid }` is load-bearing** and nothing pins it:
    an `<input>` is inline-block, so a block wrapper would shrink the field. **T010 must rewrite
    `TestSubsetAnnounced`'s addition sweep** — it forbids the exact operations T010 mandates —
    **and delete `#create-workdir-subset`** when it moves the sentence into `.combo-status`, or
    the field keeps two live regions with one dead. See `PROGRESS.md` iteration 8.
- [x] T009 Styling: tokens, focus rings, and the reduced-motion rule
  - The active option's ring is keyed on **`[aria-selected="true"]`** — `:focus-visible`
    can never reach an option, so **T011 must set that attribute** or the ring is worn by
    nothing. `position: relative` on `.combo` is now load-bearing exactly as `display: grid`
    is, and no test can see either. **The reduced-motion block resets `transition` and not
    `animation`**: verified green with an `animation` on the listbox, so the picker's own
    test forbids the property outright and every other component in the file is still
    uncovered — **T016** or milestone 6. See `PROGRESS.md` iteration 9.
- [x] T010 Enhancement: suppress the native popup, add the ARIA, filter, announce the subset
  - `field.list` is read **before** `removeAttribute("list")` cuts it, and the order is
    asserted positionally: reversed, the picker offers nothing and every other assertion
    stays green. `#create-workdir-subset` is **deleted** and FR-045's sentence now sits on
    `.combo-status`, so the field has one live region. `aria-controls` is read off
    `listbox.id`; the script carries no id the template owns. **T011 must set
    `aria-selected="true"`** (T009's ring is keyed on it), **give each `<li>` an id** for
    `aria-activedescendant`, and add the close path — T010 closes the list only when nothing
    matches. **Nothing selects an option with the pointer and no task owns that**; see
    `PROGRESS.md` iteration 10, finding 1.
- [ ] T011 Keyboard: arrows, Enter, Escape, Tab, `aria-activedescendant`

### US3 — Reaching the settings page (independent)

- [ ] T012 Add the settings link to the header, outside the brand heading

### Two defects in shipped code (independent)

- [ ] T013 Diagnostics to stderr, audit records to stdout
- [ ] T014 🔒 Resolve the start command the way the session will, or say what was checked
- [ ] T015 Correct the documented audit-trail command

### Ship it

- [ ] T016 Docs, and assert `go.sum` is still absent

---

## Shippable at T004

**T001–T004 are the demonstrable MVP.** They deliver what milestone 4 claimed and did not: an
operator chooses remote control as a mode, and no command name reaches the browser. Everything
after is real but additive.

T012–T015 are independent of every story and can run whenever an iteration is blocked on the
create-form chain.

---

## Out of scope

Deliberately NOT in milestone 5, so no iteration wanders into them:

- **Releases, versioning, the installer, and self-update** (#57, #68, #69, #66) — milestone 6.
  Scope recorded on #69: the installer should also write the systemd unit, Ubuntu and systemd
  first, and it must not overwrite a unit the operator has edited
- **Auto-recovery of a crashed session** (#95's second half). Removing the incomprehensible
  field is in scope; designing what replaces it is not. It collides with FR-032, which already
  refuses to resume where "the last conversation in this directory" could be another
  session's — and the operator asked to think about it further
- **Editing settings from the browser.** The read-only view is correct, and **no mutating verb
  is registered on `/settings` at all** — a route that does not exist cannot be exploited
- **The rain's Easter eggs** (#54) and the **browser accessibility verification** (#17) —
  polish, and a task only a human with a browser can do
- The Claude device-code login relay, and the `needs-auth` state
- The companion Claude skill
- **Sending arbitrary prompt text from the browser**
- Bulk actions across multiple sessions at once
- Persisting session records, dashboard state, or output history to disk
- Multi-user support beyond one allowlisted identity and the ownership check that exists
- Any change to milestone 1's signing procedure, its six operations, or the audit record shape
