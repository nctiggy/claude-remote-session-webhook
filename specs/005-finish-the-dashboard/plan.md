# Implementation Plan: Finish the dashboard

**Branch**: `005-finish-the-dashboard` | **Date**: 2026-08-07 | **Spec**: [spec.md](./spec.md)
**Input**: Feature specification from `specs/005-finish-the-dashboard/spec.md`

## Summary

Seven items, none of them a new capability. Every one is something that already
exists and is unreachable, invisible, incomprehensible, visibly foreign, or
actively wrong.

Two are requirements milestone 4 wrote down and did not deliver. Two more were
found during this plan's research by **running the daemon rather than reading
it** — a false dependency warning on every start, and an audit-trail command that
cannot work because diagnostics and records share a stream.

The milestone's own lesson is in its success criteria rather than its code:
**a requirement about what an operator sees is only met when something asserts on
the rendered markup.** FR-026 had three tasks in milestone 4, all green, while the
control it was about went unchanged — because every assertion was about a route or
a record.

## Technical Context

**Language/Version**: Go 1.23
**Primary Dependencies**: None. Standard library only; `go.sum` must remain absent.
**Storage**: Unchanged — tmux user options, and the operator's config file.
**Testing**: `go test ./...`, plus `-tags tmux` and `-tags quickstart`. All three now run in CI (#87).
**Target Platform**: Linux and macOS, single host, one allowlisted identity.
**Project Type**: Single Go module with an embedded web interface.
**Constraints**: FR-024 … FR-029, carried forward unchanged.
**Scale/Scope**: One operator, one host.

**The dependency budget bites hardest on US4.** The obvious way to build a themed
combobox is to reach for a library. There is no library. It is hand-written over
the native control, or it is native — and R4 chooses hand-written *over* native so
the failure mode is losing the theme, never losing the control.

### Unknowns

None. Seven questions were open; all seven are answered in
[research.md](./research.md), two of them by measurement on the running daemon.

## Constitution Check

| Principle | Gate | Verdict |
|---|---|---|
| I. Security is a gate | US2 adds a suggestion source; US1 changes what a form posts. | **PASS** — a suggestion is never an authorisation (FR-009), and the posted value is a two-literal allowlist, not a conversion. |
| II. Unknowns surfaced | Seven open questions. | **PASS** — all answered with rationale; the two found by running the daemon are recorded as defects rather than absorbed silently. |
| III. Every change verifiable | The milestone-4 miss was unverifiable by construction. | **PASS** — contracts carry a "must fail when" that names the route-passes-but-markup-unchanged case explicitly. |
| IV. Smallest correct change | US4 could have replaced the control outright. | **PASS** — it enhances the native one, so no-script behaviour is what exists today rather than something new to get right. |
| V. Standards enforced | Full gate per task at the pinned v2.12.2. | **PASS** — and the tagged suites now run in CI, so a task cannot pass by not being run. |
| VI. Blast radius bounded | Three of seven items touch `create-form.html`. | **PASS with a note** — see Collision below. |
| VII. Design system binding | The whole of US4. | **PASS** — tokens only, `stylesheet_test.go` already fails on a literal value or an unreachable rule. |

**Post-design re-evaluation**: unchanged. The one choice that could have violated
a principle was letting the themed listbox become the only way to pick a
directory; R4 keeps the native path working, so scripting failure costs
appearance and nothing else.

## The collision, and how tasks avoid it

`web/templates/partials/create-form.html` is touched by **US1** (replace the
start-command select), **US2** (feed the datalist), **US4** (wrap in the themed
combobox) and **US5** (delete the resume field). Four stories, one file — the
blast-radius principle's least favourite shape.

Ordering resolves it, and the order is not arbitrary:

1. **US5 first — deletion.** Removing the resume field shrinks the file before
   anything else edits it, and it is the only change with no dependency on any
   other.
2. **US1 next — replacement.** The start-command `<select>` becomes a checkbox.
   Independent of the picker.
3. **US2 next — data.** The picker's `<option>` list gains sources. Markup shape
   unchanged, so it cannot conflict with US4.
4. **US4 last — wrapping.** The themed control wraps a field whose contents and
   neighbours have already settled.

Each step leaves the file green, so a failure names one story.

## Project Structure

### Documentation (this feature)

```
specs/005-finish-the-dashboard/
├── spec.md
├── plan.md              # this file
├── research.md          # seven decisions, two of them measured
├── data-model.md
├── quickstart.md
└── contracts/
    ├── remote-control-toggle.md   # US1 — the checkbox, and what it posts
    ├── directory-suggestions.md   # US2 — the three sources and their union
    ├── settings-link.md           # US3 — where it goes, and what must not move
    ├── themed-combobox.md         # US4 — the literal structure, decomposed
    ├── create-form-removals.md    # US5 — the field, and the code behind it
    └── diagnostics-and-probe.md   # #88 and #96 — the two found by running it
```

### Source Code (repository root)

```
web/templates/partials/
├── create-form.html      # US5 → US1 → US2 → US4, in that order
└── header.html           # US3 — one anchor added, none moved

web/static/
├── crswd.css             # US4 — .combo, .combo-list, .combo-status, the switch
└── crswd.js              # US4 — the enhancement, and only the enhancement

internal/config/
├── config.go             # US2 — WorkdirSuggestions; the roots as a default source
├── depcheck.go           # #96 — resolve through a login shell, or say what was checked
└── suggestions.go        # NEW — the union of the three sources

internal/httpapi/
├── dashboard.go          # US1, US2 — stop sending command names; send the union
├── view.go               # US1, US2, US5 — the fields the form actually needs
└── audit.go / server.go  # #88 — diagnostics to stderr, records to stdout

internal/session/
└── conversation.go       # US5 — DELETED, recoverable per research R5

deploy/crswd.example.service   # #88 — the documented command that works
```

**Structure Decision**: Unchanged from milestone 4. Single project, web interface
embedded via `go:embed`.

## Complexity Tracking

| Choice | Why it is not a violation |
|---|---|
| US4 hand-writes a listbox rather than using the platform's | The platform's cannot be styled at all, which research R6 in milestone 4 did not weigh. The cost is bounded by keeping the native control underneath: script failure loses the theme, never the ability to pick or type a directory. |
| `conversation.go` is deleted rather than kept for #95 | Keeping it would be the fifth instance of code with no caller in this repository. It also likely does not fit #95, which needs *this session's* conversation and not *this directory's* list. |
| The dependency probe may execute the operator's profile | That is what the session does. A check that resolves a command differently from the thing that runs it is not a check. |
