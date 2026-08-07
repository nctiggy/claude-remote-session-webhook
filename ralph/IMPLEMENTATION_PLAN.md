# Implementation Plan

> Worked top-to-bottom by `ralph/loop.sh` — one task per iteration.
>
> **This is milestone 4.** Milestones 1, 2 and 3 are complete, reviewed, and deployed;
> their task lists are archived at [`archive/milestone-1-tasks.md`](archive/milestone-1-tasks.md),
> [`archive/milestone-2-tasks.md`](archive/milestone-2-tasks.md) and
> [`archive/milestone-3-tasks.md`](archive/milestone-3-tasks.md) because `PROGRESS.md`
> references their T-numbers.

## Status: generated from the spec

Generated from [`specs/004-configure-and-operate/tasks.md`](../specs/004-configure-and-operate/tasks.md),
which is the single source of truth. `spec.md`, `plan.md`, `research.md`, `data-model.md`
and the seven files in `contracts/` supersede anything this file summarises.

**Before starting a task, read its matching `T0NN` entry in `tasks.md`.** The entries below
are the ordered checklist; the task file carries the exact literals, the test each task must
include, and — for every task that adds behaviour — **the condition under which that test
must fail**. That last part is the load-bearing half. Several tasks look wrong until you read
the reason: whole-line-only comments, a split on the *first* `=` rather than the only one, a
mode that is derived rather than stored, and a permission check that deliberately does not
fire on a file holding no secret.

## 🔒 Four tasks are security-critical

**T001, T007, T011 and T019.** A mistake in any of them is invisible: every other test still
passes. If an iteration is running on a smaller model, stop after each and get it reviewed
rather than proceeding on green alone.

| Task | Why it is the dangerous one |
|---|---|
| **T001** `IsSecret` | Shared by the permission refusal *and* the settings page. A disagreement between them is invisible until it matters. |
| **T007** the precedence shim | Ordering *is* the security property. Reversed, a stale file silently overrides the environment a container was configured with. |
| **T011** secret rendering | The one page holding every secret at render time. A "helpful" masked prefix is still a disclosure. |
| **T019** the mode toggle | The only new route taking a value that names something to run. If a command line can arrive from a browser, FR-030 is gone in both directions. |

## This milestone is mostly finishing, not building

Four abandoned lane branches hold **~3,800 lines** between them. Verified at planning time:
each **builds standing alone**, and each broke only against a `main` that has since moved.
Where a task says carry forward, the work is a **rebase-and-reconcile, not a rewrite** — read
the branch first, and preserve its comments, which are the best documentation of why the
format is what it is.

| Branch | Carries | Task |
|---|---|---|
| `claude/issue-issue-65-20260807-0112` | `internal/config/file.go`, `file_test.go` | T003 |
| `claude/issue-issue-42-20260805-1832` | four 303 handlers, `outcome.go`, banner partial | T014 |
| `claude/issue-issue-60-20260806-0406` | card split, rename disclosure | T026 |
| `claude/issue-issue-59-20260807-0055` | **the discovery walk only** | T023 |

The last row is a deliberate exclusion: that branch's hand-rolled combobox is **replaced by
markup**, not carried. See the resolved decision below.

## What is already running

Milestones 1 through 3 are **live**, not merely built. Changes here land on a deployed daemon:

| | |
|---|---|
| Service | `crswd.service`, systemd user unit, loopback `127.0.0.1:8765` |
| tmux | **Its own server**, `-L crswd-<listen>` — never the operator's default server (#22) |
| Public | `https://crswd.craigcloud.io` via the `crswd` Cloudflare Tunnel |
| Edge | Access app `CRSWD Session Control`, two policies — Google identity, and Service Auth for the API client |
| Daemon | Validates the Access assertion itself; the dashboard reads, streams, and **acts** |
| Audit | `journalctl --user -u crswd -o cat \| jq .` |
| Secrets | `op://Lobster/crswd/{shared-secret,access-client-id,access-client-secret}` |

**Sessions now survive a daemon restart with their metadata**, which is what makes a
configuration change tolerable at all: a restart no longer costs the fleet. T017 extends that
mechanism by one option rather than inventing a second one.

## Resolved decisions

Answered by the operator or settled in [`research.md`](../specs/004-configure-and-operate/research.md).
**Do not re-litigate these in an iteration** — if one looks wrong, write it in `PROGRESS.md`
under `NEEDS CLARIFICATION` and stop.

| Question | Decision | Consequence |
|---|---|---|
| YAML, TOML, JSON, or something hand-parsed? | **`key = value` with `#` comments**, hand-parsed | YAML and TOML have no standard-library parser and neither is safe to hand-roll; both would create `go.sum`, which `docs/security.md` §5 forbids. JSON was the real candidate and was rejected because it deletes the commentary in `config.example` — the most useful documentation this repo has about what each bound is for |
| Are trailing comments allowed? | **No. `#` is a comment marker only at the start of a line** | This is a security decision wearing style clothes: `shared_secret` may legitimately contain `#`, and stripping from the first one would silently truncate a secret into a daemon that starts, looks healthy, and rejects every request |
| Which `=` separates key from value? | **The first one** — `strings.Cut`, never `strings.Split` | `start_commands` always contains `=` inside its value. A parser that refused an ambiguous line would refuse valid configuration |
| How is a list spelled? | **Comma-separated on one line**, exactly as the environment variable spells it today | The file is a second *source*, not a second set of rules. The value string is handed to the same parser the variable goes through |
| Where is precedence decided? | **One `getenv` shim**, four lines, behind the existing `config.LoadFrom` seam | Flag → environment → file → default. No bound, default or refusal is written twice, so a value cannot mean one thing in a unit and another in a file. It is also why a daemon with no file behaves exactly as today, which is what lets SC-002 be verified against the **existing** acceptance suites unchanged |
| How does the settings page know where each value came from? | **The shim records it as it decides** | Provenance is a byproduct of having one place decide, never an inference. A value present and equal in both sources is indistinguishable by comparison — and that is exactly when an operator is asking why their edit did nothing |
| Which keys are secret? | **`shared_secret` and `access_allowed_emails`**, behind one exported `IsSecret` | The allowlist is not a credential but it names *who* can reach this daemon. One predicate means the permission check and the page render cannot disagree about what a secret is |
| Does the 0600 refusal fire on any config file? | **Only one containing a secret key** | A file holding only `allowed_roots` is not a secret file, and refusing to start over its mode would be a refusal the operator cannot act on sensibly |
| Is session mode stored or derived? | **Derived** from the start-command name | Two fields that must agree are two fields that can disagree. FR-031 is satisfied by carrying the name that determines the mode; the one real gap — the name not surviving a restart — is closed by a fifth tmux option, which is smaller than a second source of truth |
| How does the directory picker work without JavaScript? | **`<input list>` + `<datalist>`** — the platform's own control | It satisfies five of the six picker requirements with **no script running**: filtering, keyboard operation, screen-reader announcement, free-text entry, and today's field unchanged. The abandoned branch's 225 lines of hand-rolled combobox degrade to nothing and own accessibility bugs the browser would otherwise own |
| Does the settings page get an edit route? | **No route at all**, not a route that refuses | Editing is out of scope this milestone. A route that does not exist cannot be exploited, and writing the operator's file from a browser is the highest-consequence surface in the product |
| Is a missing dependency fatal? | **tmux yes, the start command no** | Without tmux this daemon can do nothing, so starting only defers the failure to the first request. Without a start command it can still serve the dashboard and say what is wrong |

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
- **AR-005: a test satisfies the cross-site checks, it never disables them.** Setting
  `Sec-Fetch-Site: same-origin` and minting a valid token is correct. A build tag or flag that
  turns a check off is the exact defect the gate exists to prevent.
- **AR-008: no refactoring outside the task**, however obvious the improvement.
- **A task is not done when the code exists. It is done when something calls it.** This repo
  has shipped that failure three times — a reaper with no caller, `Store.Touch` with no caller,
  and a PR-opener script no workflow invoked. **T007 is the one to watch**: a config parser
  that is never consulted is the exact bug left on the abandoned branch.

---

## Tasks

### Foundation (blocks every story)

- [x] T001 🔒 `IsSecret` in `internal/config/secret.go` — the single classifier
- [x] T002 `Source` type and its four strings in `internal/config/source.go`

### US1 — Configure the daemon in a file (P1, MVP)

- [x] T003 Carry forward the parser from `claude/issue-issue-65-...0112`; grammar only
- [x] T004 The file-level refusals, none of which ever names the value
- [x] T005 🔒 The mode refusal, gated on the file containing a secret
- [x] T006 A missing file is not an error; the parser never writes
- [x] T007 🔒 Wire the file as a fallback `getenv` — **the keystone**
- [x] T008 Record provenance in the same shim
- [x] T009 `crswd config check` and `crswd config migrate`, plus `config.bak` fallback

### US2 — See what it is configured to do (P2)

- [x] T010 The read-only `/settings` route, `GET` only, `settings.view`
- [x] T011 🔒 Secrets render `present` / `absent`, never a value
- [x] T012 One row per key with its source; name the file that was read
- [x] T013 Sweep every route and assert no secret appears anywhere (SC-005)

### US3 — The dashboard behaves without script (P3)

- [x] T014 Carry forward post-redirect-get from `claude/issue-issue-42-...1832`
- [x] T015 Finish the ~19 tests still asserting fragment responses
- [ ] T016 All four actions usable with scripting disabled

### US4 — Turn remote control on and off (P4)

- [ ] T017 Persist the start-command name as `@crswd-start`, the fifth tmux option
- [ ] T018 `Session.Mode()`, derived; refuse a mode naming an unconfigured command
- [ ] T019 🔒 `POST /dashboard/sessions/{id}/mode`, fields `mode` and `confirm`
- [ ] T020 Restart the process in place, with `--continue`, preserving scrollback
- [ ] T021 Show the mode on the card, textually

### US5 — Pick a working directory (P5)

- [ ] T022 Replace the field with `<input list>` + `<datalist>`
- [ ] T023 Carry the discovery walk forward; one level, off by default
- [ ] T024 🔒 A suggested path outside the allowlist is refused identically
- [ ] T025 Announce a filtered subset

### US6 — The card's two halves (P6)

- [ ] T026 Carry forward the card split from `claude/issue-issue-60-...0406`
- [ ] T027 Rename moves to the session page as a disclosure
- [ ] T028 A text selection inside the anchor does not navigate

### Independent of the stories

- [ ] T029 Startup dependency probes — tmux fatal, start command warning
- [ ] T030 Install command from `/etc/os-release`, never guessed
- [ ] T031 List prior conversations — identifier and time only, never contents
- [ ] T032 Offer them at create time, fresh by default; refuse when ambiguous
- [ ] T033 Bound the captured pane explicitly; refuse past it rather than truncate

### Ship it

- [ ] T034 `config.example`, carrying the commentary that justified the format
- [ ] T035 Docs, and assert `go.sum` is still absent

---

## Shippable at T009

**T001–T009 are the demonstrable MVP and are deployable on their own**: an operator changes
any setting by editing one file and restarting, and a daemon with no file behaves exactly as
it does today. Everything after is additive, and US2 is the first thing that makes US1
legible.

T029–T033 are independent of every story and can run whenever an iteration is blocked
elsewhere.

---

## Out of scope

Deliberately NOT in milestone 4, so no iteration wanders into them:

- **Releases, versioning, an installer, and self-update** (#57, #68, #69, #66). They are
  distribution rather than operation, and they depend on this milestone's config file
  existing first — they are the next milestone, not this one
- **Editing settings from the browser.** The read-only view ships here; writing the operator's
  file from a page carries a list of safeguards long enough to be its own piece of work, and
  it is the highest-consequence surface in the product. **No mutating verb is registered on
  `/settings` at all** — a route that does not exist cannot be exploited
- **The rain's Easter eggs** (#54), and **the browser accessibility verification** (#17) —
  polish, and a task only a human with a browser can do
- The Claude device-code login relay, and the `needs-auth` state
- The companion Claude skill
- **Sending arbitrary prompt text from the browser.** The mode toggle selects between
  *configured names*; a general "type into the session" control is a much larger surface
- Editing a session's working directory after creation
- Bulk actions across multiple sessions at once
- Persisting session records, dashboard state, or output history to disk
- Multi-user support beyond one allowlisted identity and the ownership check that already
  exists. The check still runs on every action, and is still tested against a synthetic second
  owner — a check removed because it always passes is a check that will be missing when it
  stops always passing
- Any change to milestone 1's signing procedure, its six operations, or the audit record
  shape. The API path must keep working **unchanged**
