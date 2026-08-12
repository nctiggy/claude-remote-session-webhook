# Implementation Plan

**Milestone 13 — Make idle mean what it says, and let a session live forever.**

> *"I have no idea what idle means in this platform? Even if I am using the
> session I think it is still considered idle. How is idle determined… is it
> real?"*
> *"I also want to allow for sessions to have an infinite lifetime."*

Seven tasks.

---

## What idle measures today, and why the operator is right

`LastActivity` is advanced by **exactly three** things:

| Advances the idle clock | Reachable from |
|---|---|
| `Resolve` — resolving a session's credential | **signed API only** |
| `Compact` | browser + API |
| `SetMode` | browser + API |

**Nothing else.** Reading does not: the dashboard, the session page and the live
stream all go through `View`, which has no clock reading in it — deliberately,
under FR-034f, *"watching is not driving"*. Rename does not. And **there is no
route to type into a session from the browser at all** — `Prompt` exists only on
the signed API.

So a browser-driven operator watching a session all day has it reaped at sixty
minutes, and someone attached to the tmux session on the host is invisible to the
clock entirely. Idle measures *daemon-mediated mutating HTTP requests* and calls
that activity.

## What makes this fixable rather than a trade

**tmux tracks the real thing.** `#{session_activity}` is a timestamp of when the
session last produced output — the agent printing, a human typing in an attached
terminal, anything.

`argvList()` (`internal/tmuxctl/fake.go:94`) already runs `list-sessions -F` with
six fields on every reconciliation. **This is a seventh field on an exec that
already happens**, not a new call and not a new cost.

It also answers the objection that killed the obvious fix. Counting browser reads
was rejected because a forgotten tab would hold an unsandboxed shell open forever.
**Real tmux activity is not a forgotten tab** — a tab produces no output.

---

## ⚠️ Fail safe, in the direction that keeps sessions alive

tmux's activity field can be absent, unparsable, or from a session the daemon does
not manage. **Every one of those falls back to `LastActivity` and never to
"reap it".** A parse bug that starts destroying an operator's sessions is a far
worse failure than one that keeps a dead session a while longer, and the absolute
deadline still bounds the latter.

The idle deadline is computed from **the later of** the two clocks. Either counts,
because either is genuinely activity.

---

## ⚠️ "Never" needs a spelling that cannot mean "unset"

The lesson is already in this codebase and must not be relearned: a negative
`Idle` disables idle reaping *because* zero already means "unset", and one value
cannot mean both.

An infinite lifetime needs the same care. Whatever the spelling, it must be
**unambiguous, and it must exist for the ceiling too** — a per-session "never"
that cannot fit under `session_lifetime_max` is a setting that always refuses.

**This removes a bound rather than relaxing one**, which is a real difference from
the negative-idle case, where the absolute deadline still fired. That is the
operator's decision to make on their own host — the containment control here is
`allowed_roots` and the doors, not the reaper — but the documentation must say
plainly what is being switched off rather than presenting it as free.

---

## Conventions

- `- [ ]` open · `- [x]` done · `- [!]` blocked (reason in `PROGRESS.md`)
- Priority order is meaningful — the loop always takes the topmost open item.
- **Every task ends green**: `go build ./... && go vet ./... && go test ./... && golangci-lint run`,
  plus **`-tags tmux`** (this milestone changes the tmux argv, so that suite is
  not optional) and `-tags quickstart` where touched.
- **Check the linter is v2 before trusting it** (#26).
- `go.sum` must never appear.
- **AR-008: no refactoring outside the task.**
- **A task is not done when the code exists. It is done when something calls it.**
- **A new guard must be proven by breaking it.**
- **`config.example` is guarded in both directions** and any comment line
  beginning `# <known_key> = …` counts as that key's line — illustrative prose
  must not start that way or the suite fails it as a duplicate.

---

## ⚠️ Remote-control sessions produced no pane output, and that changed everything

The operator's shipped `rc` command was:

```
rc=claude remote-control --permission-mode bypassPermissions --spawn=same-dir --name {name}
```

`--spawn` makes the tmux session a **launcher**. The conversation lives on the
relay, so the pane goes quiet after startup — and for the sessions this operator
actually uses, that meant the pane was empty, the pill could not tell running from
idle from needs-auth, `compact` typed into a pane nobody read, and **tmux activity
never ticked**.

So T001 and T002 as originally written would have measured a clock that never
moves, for the only sessions that matter. That is worth stating because it nearly
shipped.

**The fix is a start command, and it is verified working**:

```
rc=claude --dangerously-skip-permissions "/rc {name}"
```

An ordinary interactive session that drives itself into remote control, so the
tmux session *is* the session. The operator confirmed the form runs; the config
parser passes the quotes through intact (`send-keys` is called without `-l`, so
the shell parses them normally).

**T000 below ships that as the default.** Everything after it is worth building
only because of it.

---

## Tasks

- [x] **T000** Change the shipped remote-control default so a remote session renders in its own pane. `DefaultRemoteControlCommand` (or whatever `config.go` names the built-in) becomes the interactive form above rather than the `--spawn` launcher. Update `config.example` and `.env.example`'s `start_commands` examples to match, and say **why** in one sentence: a spawned session leaves the pane empty, and an empty pane is a dashboard that cannot show, judge, or compact anything. Keep the launcher form documented as a supported alternative for an operator who wants it — this changes a default, it does not remove a capability.


- [x] **T001** Add `#{session_activity}` as a seventh field to `argvList()` in `internal/tmuxctl/`, parse it in `parseSessions`, and carry it on the reconciled session. The existing tests assert the six-field argv and will need updating — that is expected and correct, not a signal to route around them. `Fake` must serve it too, with a knob to set it, or nothing downstream is testable. **Unparsable or absent is not an error**: it yields a zero time, and T002 decides what that means.

- [x] **T002** 🔒 Compute the idle deadline from **the later of** `LastActivity` and the tmux activity time. A zero or unusable tmux time falls back to `LastActivity` alone — **never to reaping**. Test: a session with old `LastActivity` and recent tmux activity is **not** reaped; one with both old is; one with an unparsable tmux time behaves exactly as today. **Must fail when** a parse failure makes a live session reapable.

- [x] **T003** Show the operator what the clock is actually watching. The card already carries `idle deadline`; add the last activity it is measured from, so "why is this about to die" is answerable from the page. Reuse `.card-meta`'s `dt`/`dd`; **no new class**.

- [x] **T004** 🔒 Give the absolute lifetime a spelling for **never**, matching the discipline the idle disable already follows: it may not collide with "unset". Apply it to both the per-session override **and** `session_lifetime_max`, since a per-session never under a finite ceiling is a setting that always refuses. `resolveLifetimes` currently refuses a negative lifetime — that refusal is what changes, deliberately and with the reason recorded.

- [x] **T005** Offer it on the create form beside the existing never-die-when-idle switch, reusing `.field-switch`, `.switch-input`, `.switch-label`. **No new class.** The label must say what it switches off: with both switches on, nothing reaps this session, and the operator has said so twice.

- [x] **T006** Update `config.example` for both changes — what idle now measures, and how to spell an unlimited lifetime — **leading with the fact and putting the reasoning after it**. Mind the duplicate-key-line trap above. Also correct the four lifetime keys' prose if it now describes something that is no longer true.

- [x] **T007** Update `README.md`'s configuration table and `docs/auth-and-sessions.md` where either describes the idle clock. `auth-and-sessions.md` is a binding correctness spec — **change only what stopped being true** and leave its reasoning intact. **`.env.example` belongs to this task too** (found in T006, which named `config.example` alone): it carries the same four keys with the same prose, and its `CRSW_IDLE_TIMEOUT` block still tells the operator that "a long job you are watching is still reaped" — the exact sentence T002 made false, in the file most likely to be copied.

---

## Out of scope

- **Making browser reads advance the clock.** T002 removes the need: real tmux
  activity is the thing worth counting, and a forgotten tab produces none.
- **A prompt route on the browser door.** It does not exist today, and adding one
  is a different milestone with its own security argument.
- **The docs overhaul.** A separate audit has already ranked it and it is next:
  stale facts in `AGENTS.md` and `deploy/README.md`, a README that cannot get a
  stranger through a Cloudflare install, and `config.example`'s rationale-first
  ordering.
- **#120, #121.** Unchanged. **Q2** is still the operator's to answer.
