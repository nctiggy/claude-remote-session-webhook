# Implementation Plan: Continue a Conversation After the Session Is Running

**Branch**: `feat/013-continue-after-start` | **Date**: 2026-08-23 | **Spec**: [spec.md](spec.md)

## Summary

The decision to continue a conversation moves from the create form to the running
session. The new-session dialog loses its resume control and its lifetime hint
text; the daemon loses `--continue` and the "most recent" value entirely; and a
running session gains a **Continue** action that lists the conversations recorded
for its own working directory and restarts it into the one chosen.

This feature removes more than it adds. Nearly everything it needs already exists:
conversation listing (spec 009), identifier validation, revive-in-place and the
`@crswd-conversation` option (spec 012), and the session-action gate (milestone 3).

## Technical Context

**Language/Version**: Go 1.24. **Dependencies**: standard library; `tmux`.
**Storage**: the existing session journal — one new event kind, no new file.
**Testing**: `go test ./...`, plus `tmux` and `quickstart` tags.
**Target**: Linux, systemd user unit, loopback listener behind a tunnel.
**Constraints**: no new door; no shell string constructed; the only caller-supplied
value is a conversation identifier the existing validator already covers.
**Scale**: one operator's fleet.

### Why there is no research.md or data-model.md

Both were written for spec 012 because it introduced a durable record, a new
identity, and three unknowns that had to be settled against the host. This feature
introduces no new entity and no new persistent shape: `Session.ConversationID`
already exists and merely becomes a value that can change during a session's life
rather than only at its birth. The one design question in the request — whether
the create form keeps a per-conversation list — was put to the operator and
answered before the spec was written. Producing empty artifacts to match a
previous feature's shape would be ceremony, not planning.

## Constitution Check

*Checked before Phase 0 and again after design. Both passes recorded.*

| Principle | Assessment | Pass |
|---|---|---|
| **I — Security is a gate** | No new door. One session-scoped action behind the existing `handleAction` gate — ownership, page token, cross-site checks — like compact and mode. The working directory comes from the **record**, not the caller, which makes this strictly less caller-controlled than spec 012's lookup. The only caller value is a conversation identifier, validated by the existing `ValidateResume` before it can reach a command line. | ✅ |
| **II — Unknowns surfaced** | The single ambiguity was put to the operator and answered before writing. No `NEEDS CLARIFICATION` remains. | ✅ |
| **III — Verifiable** | Every FR is an observable behaviour; `quickstart.md` gives a runnable check per story. | ✅ |
| **IV — Smallest correct change** | Reuses `Conversations`, `ValidateResume`, `revive`, `SetConversation`, the journal and the action gate. Net deletion of a flag, a constant, a form control and a script branch. | ✅ |
| **V — Standards enforced** | No guardrail changed. | ✅ |
| **VI — Blast radius** | Continuing restarts a process, never a session: `CreatedAt` and the deadline are untouched, no credential is minted, the cap is unmoved, the allowlist is re-resolved, and the single-restart rule covers continue and revival together. | ✅ |
| **VII — Design system** | One control built from the existing action and select vocabulary on a surface that already carries actions. No new component. | ✅ |

### Principle VI in detail

Continuing is the second thing that restarts a session's process. It is bounded by
the same rules revival is, and shares the mechanism so they cannot diverge:

- **Allowlist** — re-resolved from the record before anything is sent (FR-011).
- **Deadline** — `CreatedAt` is carried; continuing extends nothing (FR-007).
- **Cap** — no session is created or destroyed, so the count cannot move (FR-010).
- **Credential** — the record never went away, so nothing is re-issued (FR-010).
- **One restart at a time** — continue takes the supervisor's own in-flight claim,
  so a continue racing a revival cannot put two processes in one shell (FR-012).

## Design

### The action

```
POST /dashboard/sessions/{id}/continue
  conversation=<uuid>   proof=<page token>   confirm=yes
```

Registered with `handleAction`, exactly as mode and compact are. The handler
mirrors `modeFromBrowser` step for step — operator from context, `routableID`
shape check, `PostForm` only, a `confirm=yes` step because this ends the process
the operator is watching, `View` for ownership, `SetSessionID` on the trail from
the record's id — and then calls one manager method.

### The manager method

`Manager.Continue(ctx, s, conversationID)`:

1. Validate the identifier (`ValidateResume`), refuse `ResumeLatest` — it no
   longer exists.
2. Refuse a session that is not running (`StateDead`, `StateFailed`).
3. Re-resolve `WorkDir` through `ResolveWorkDir`.
4. Refuse a conversation with no transcript (`HasTranscript`).
5. Take the single-restart claim.
6. Write the new conversation to the store, the `@crswd-conversation` tmux option
   and the journal — **before** the restart, so a daemon that dies mid-continue
   comes back knowing which conversation the session is on.
7. Interrupt the pane and re-send the start command with `--resume <uuid>`, which
   is `revive`'s own path.

Step 6 before step 7 is the same ordering discipline the supervisor uses, for the
same reason.

### What is deleted

| Thing | Where |
|---|---|
| `ResumeLatest`, `ResumeLatestFlag` | `internal/session` |
| `CreateRequest.Resume` and its plumbing | `internal/session`, `internal/httpapi` |
| The `resume` select and its script | `web/templates/partials/create-form.html`, `web/static/crswd.js` |
| `ResumeLatest`/`ResumeLatestFlag` view fields | `internal/httpapi/view.go` |
| The "nothing then reaps this session" hint | `create-form.html` |

**`SetMode` changes as a consequence, and improves.** It appends `--continue`
today so a mode switch keeps its conversation. With that flag gone it uses
`--resume <ConversationID>` instead — exact rather than "whatever was most
recent". A session carrying no conversation identifier (created before spec 012,
or under a start command this daemon cannot give one to) gets no flag and starts
fresh on a mode switch. That is a real behaviour change for those sessions, and it
is the honest one: there is no identifier to resume, and `--continue` was the
mechanism being removed precisely because it resolved to something nobody could
see.

## Project Structure

```text
specs/013-continue-after-start/
├── plan.md            # this file
├── spec.md
├── quickstart.md
├── contracts/session-continue.md
├── checklists/requirements.md
└── tasks.md

internal/session/
├── manager.go         MOD  Continue(); SetMode uses --resume; Resume plumbing removed
├── conversation.go    MOD  ResumeLatest removed; ValidateResume refuses it
└── supervisor.go      MOD  the in-flight claim becomes shared with Continue

internal/httpapi/
├── actions.go         MOD  patternDashboardContinue + continueFromBrowser
├── server.go          MOD  register the action
├── dashboard.go       MOD  offer the conversations for a session's own directory
├── view.go            MOD  ResumeLatest/ResumeLatestFlag out; session view gains the list
└── conversations.go   MOD  the lookup keeps serving one directory, now the record's

web/
├── templates/partials/create-form.html    MOD  resume control and hint text removed
├── templates/partials/session-card.html   MOD  the Continue control
└── static/crswd.js                        MOD  create-form resume script removed
```

**Structure Decision**: existing layout, no new files outside the spec directory.

## Complexity Tracking

No constitution violations. One shared piece of state is worth naming for review:
the supervisor's in-flight claim becomes reachable from the manager so that
continue and revival cannot both restart one shell. The alternative — a second
lock with the same job — is how two mechanisms end up disagreeing about which of
them is allowed to type into a pane.
