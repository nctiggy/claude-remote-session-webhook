# Implementation Plan: Session Lifetime Honesty

**Branch**: `009-session-lifetime-honesty` | **Date**: 2026-08-14 | **Spec**: [spec.md](spec.md)

**Input**: Feature specification from `specs/009-session-lifetime-honesty/spec.md`

## Summary

Four changes to one subject — how long a session lives and what starts it.

The idle bound is deleted from the product. The per-session absolute-lifetime
override gains a tmux user option so it survives adoption, joining the five facts
already carried that way. The create form gains a server-rendered preview of the
command line it will run. And a create may resume a prior Claude conversation,
either the most recent one in the working directory or a specific one chosen from
a list built by reading Claude's own on-disk history.

The first two are the 2026-08-14 incident. The last two are the operator's
workflow around it.

## Technical Context

**Language/Version**: Go 1.25 (`go.mod`), standard library only — no `go.sum`, by
constitutional constraint (`internal/config/file.go:10`)

**Primary Dependencies**: none added. tmux is the runtime dependency and is
already the persistence layer this feature extends.

**Storage**: tmux user options on the session itself (`@crswd-*`), plus tmux's own
`#{session_created}`. This feature adds one option and removes one format field.
No new store, no file the daemon writes.

**Testing**: `go test ./...`; `-tags tmux` for the `internal/tmuxctl` round trip
and `internal/session`'s name round trip; `-tags quickstart` for `cmd/crswd`
acceptance; `-tags dev` for the loopback bypass. All four per `AGENTS.md`.

**Target Platform**: Linux, self-hosted, loopback bind behind a Cloudflare Tunnel

**Project Type**: single Go daemon with an embedded server-rendered dashboard

**Performance Goals**: unchanged. The one new I/O path is a directory listing on
create-form render, bounded by the number of conversations in one directory.

**Constraints**: no shell string is ever constructed (`internal/tmuxctl`'s package
doc); the start command is typed into a live shell, so anything interpolated into
it is shell-interpreted; pane output and every operator value render as text,
never markup.

**Scale/Scope**: ~40 Go files touched by the idle removal, 1 new package, 1 new
tmux option, 1 new form control group, 1 schema version bump.

## Constitution Check

*GATE: checked before Phase 0 and again after Phase 1.*

Constitution **2.0.0** (amended on this branch, 2026-08-14). The amendment is the
prerequisite recorded in the spec's Constitutional Impact section: Principle VI no
longer requires an idle timeout, and now states that a session with its absolute
deadline switched off is bounded by nothing time-based.

| Principle | Status | Note |
|---|---|---|
| I — Security is a gate | **PASS, with the feature's one real risk named** | The resume identifier reaches a command line that is typed into a live shell. It is validated to a strict UUID at the boundary (research D6) and is the single most important control in this plan. |
| II — Unknowns surfaced | **PASS** | Both spec markers resolved before planning. This plan adds none. |
| III — Every change verifiable | **PASS** | Every task carries a test that fails without it; the four suites in `AGENTS.md` gate the merge. |
| IV — Smallest correct change | **PASS, with one deliberate exception** | The idle removal is large, but it is exactly the requested scope. `Expiry` is kept as a type with one member rather than collapsed, to avoid churn in the trail's vocabulary. |
| V — Standards enforced | **PASS** | New rules land as tests, not prose: the option round trip, the field-count guard on the List format, the UUID validator's negative cases. |
| VI — Blast radius bounded | **PASS under the amendment; one bound genuinely removed** | Idle reaping is gone. The absolute deadline, the session cap, the allowlisted roots, verified teardown and the loopback bind are untouched. **What now becomes reachable**: a session created with the deadline switched off is reaped by nothing, and — unlike today — stays that way across a restart. That is the feature. It still takes two operator decisions. |
| VII — Design system binding | **PASS** | The preview and the resume control reuse `.field`, `.field-hint`, `.field-switch`, `.switch-input`. One new class for the preview block, justified in research D5; no second button, no second card. |

**Widening declared, per Principle VI's closing rule**: this feature makes
never-expiring sessions actually never expire. Before it, the switch was
undone by the next restart — which is to say the bound the constitution rested on
was being enforced by a bug. After it, the operator's choice holds, and a host
whose ceiling has been unbounded can carry sessions that no clock will ever end.

## Project Structure

### Documentation (this feature)

```text
specs/009-session-lifetime-honesty/
├── plan.md              # This file
├── research.md          # Phase 0: D1–D7
├── data-model.md        # Phase 1: what changes on Session, SessionInfo, Config
├── quickstart.md        # Phase 1: acceptance walkthrough
├── contracts/
│   ├── lifetime-persistence.md   # the @crswd-lifetime option and its round trip
│   ├── command-preview.md        # what the form shows and what it may not
│   └── conversation-resume.md    # discovery, validation, and the flag insertion
├── checklists/
│   └── requirements.md
├── spec.md
└── tasks.md             # Phase 2 (/speckit-tasks)
```

### Source code

```text
cmd/crswd/
├── main.go                      # SetLifetimes call loses two arguments
└── config_cmd.go                # migrate drops retired keys

internal/config/
├── config.go                    # EnvIdleTimeout, EnvIdleTimeoutMax and their fields deleted
├── file.go                      # SchemaVersion 1 → 2; retiredKeys added
├── sessionenv.go                # unchanged
└── secret.go                    # unchanged

internal/session/
├── session.go                   # Idle, TmuxActivity, IdleDeadline, IdleSince, IdleDisabled, DisplayIdle deleted
├── manager.go                   # start writes @crswd-lifetime; Adopt restores it; syncActivity deleted
├── reaper.go                    # ExpiryIdle and the idle branch deleted
└── conversation.go              # NEW — prior-conversation discovery

internal/tmuxctl/
├── controller.go                # OptionLifetime added; SessionInfo.Activity deleted
├── exec.go                      # List format: +@crswd-lifetime, −#{session_activity}
└── fake.go                      # matching format and option support

internal/httpapi/
├── actions.go                   # fieldIdleTimeout deleted; resume fields read
├── sessions.go                  # parseLifetimeOverrides loses its idle half
├── dashboard.go                 # create view gains Preview and Conversations
├── settings.go                  # idle rows deleted
└── view.go                      # idle deadline deleted from the session view

web/
├── templates/partials/create-form.html   # idle switch out; preview and resume in
└── static/crswd.js                       # preview updates as the form changes
└── static/crswd.css                      # one new block

docs/                            # auth-and-sessions.md, security.md, fixes-log.md
README.md, .env.example, config.example, deploy/crswd.example.service
```

## Phase 0 — Research

See [research.md](research.md). Seven decisions, D1–D7. The two that carry risk:

- **D6** — the resume identifier is validated to a strict UUID before it can reach
  a command line, because that line is typed into a live shell.
- **D2** — a restored lifetime is re-checked against the *current* ceiling, so a
  narrowed configuration is honoured over a value written when it was wider.

## Phase 1 — Design

See [data-model.md](data-model.md) and the three contracts.

### Order of work, and why

The four stories are independent as specified, but they touch one file each in
common (`create-form.html`, `manager.go`). The order below keeps each step green:

1. **Idle removal** (US1). Largest diff, no new behaviour, and it deletes fields
   the other steps would otherwise have to carry. Doing it first means the
   lifetime work is written against the final shape of `Session`.
2. **Lifetime persistence** (US2). Touches the same `List` format string the idle
   removal edits — sequencing them avoids editing that format twice.
3. **Command preview** (US3). Needs the form's final field set, which step 1
   changes and step 4 adds to. Rendered before the resume control exists, then
   extended by it.
4. **Conversation resume** (US4). The only genuinely new capability, and the only
   one with a new security control. Last, so it lands on a green tree.
5. **Card facts** (US5). Presentation over what steps 1–2 settled.

### Constitution re-check after design

No change to the table above. Design added no new execution surface: the preview
is a readout, and the resume flag composes onto a command line already resolved
from the operator's configured set. The one new filesystem read is bounded by
`ResolveWorkDir`, which is the same check a create passes.

## Complexity Tracking

| Thing | Why it is not simpler |
|---|---|
| A new package-level file for conversation discovery rather than a helper in `session` | It is the only code in the daemon that reads a directory the operator did not configure. Keeping it in one file with one exported function makes the disclosure auditable in one place, which is what `docs/security.md` asks of a new read. |
| Keeping `Expiry` with a single member | Collapsing it churns the audit vocabulary, `reapRecord`, and every reaper test for no behavioural gain. Principle IV. |
| Server-rendered preview *and* a script that updates it | The form must work with scripting off (research R4, milestone 3). A preview that only existed in JavaScript would be absent exactly when the operator most needs to know what will run. |
