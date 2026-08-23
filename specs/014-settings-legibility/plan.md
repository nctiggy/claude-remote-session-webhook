# Implementation Plan: A Settings Page an Operator Can Read

**Branch**: `feat/014-settings-legibility` | **Date**: 2026-08-23 | **Spec**: [spec.md](spec.md)

## Summary

Seven headings become three. Seven per-row Save buttons become one. The Source
column goes and its information stays. The update section stops narrating the
status quo and speaks when it has something the operator can act on.

Three of the four are subtraction. The one addition is a route that writes several
settings in one request, and it reuses the per-key validation rather than growing
a second opinion about what a setting may be.

## Technical Context

**Language**: Go 1.24, standard library, server-rendered templates.
**Storage**: the existing configuration file. No new persistence.
**Testing**: `go test ./...`; the settings page has heavy partial coverage already.
**Constraints**: the page must work with scripting off — that is what forces the
unchanged-value rule onto the server rather than into a script.

### Why there is no research.md or data-model.md

No new entity, no new persistent shape, and no question that had to be settled
against the host. `settingSection` and `settingRow` already exist; this changes
which section a key names and how many forms wrap the rows.

## Constitution Check

| Principle | Assessment | Pass |
|---|---|---|
| **I — Security is a gate** | One new route, behind the same action gate as the per-key edit. Every key and value goes through the *same* validation the per-key route uses — called, not reimplemented. The one new hazard is a rendered secret placeholder becoming a stored secret, and FR-008 closes it by comparing against what was rendered. | ✅ |
| **II — Unknowns surfaced** | The request wondered aloud whether some settings need exposing at all. That is recorded as a deliberate no — hiding configuration is a different decision from grouping it — rather than acted on by guess. | ✅ |
| **III — Verifiable** | Every FR is observable in a render or in the trail. | ✅ |
| **IV — Smallest correct change** | Net subtraction: four headings fewer, one button instead of N, one column fewer. | ✅ |
| **V — Standards enforced** | No guardrail changed. The stylesheet/markup guard and the every-setting-appears-once guard both still apply and both get stricter. | ✅ |
| **VI — Blast radius** | Untouched. Settings are the operator's own configuration; nothing here changes what a session may reach. | ✅ |
| **VII — Design system** | Fewer components. The Save is the canonical button; the table loses a column; the update detail moves into the existing disclosure pattern. | ✅ |

## Design

### Sections

`settingSectionOf` returns one of three, and the map is corrected while it is
rewritten — `start_command`, `remote_control_command` and `session_environment`
fall to "Other" today because the switch names `remote_start_commands`, which is
not a key this daemon has.

| Section | Keys |
|---|---|
| **Network** | `listen`, `access_enabled`, `access_team_domain`, `access_aud`, `access_allowed_emails`, `dashboard_password`, `shared_secret` |
| **General** | `allowed_roots`, `workdir_suggestions`, `discover_roots`, `start_command`, `start_commands`, `remote_control_command`, `session_environment`, `session_lifetime`, `session_lifetime_max`, `max_sessions`, `max_streams`, `create_rate_per_min`, `max_body_bytes`, `pane_bound`, `destroy_on_shutdown` |
| **Updates** | not configuration — the update panel |

The fallback heading stays and is expected to be empty. `TestEverySettingAppearsInASection`
already fails if a key goes missing; a new test fails if the fallback is *used*,
so the next key added to `config.go` is a build failure here rather than a
mystery heading on the page.

### One Save

```
POST /settings            (all changed values, one request)
```

The rule that makes it safe lives on the server: **for each submitted key, if the
value equals what the page rendered for that key, skip it.** Unchanged values are
not rewritten, and a secret rendered as `present` and submitted back as `present`
is a no-op by the same rule that handles every other unchanged field.

This is what lets FR-009 hold. A script that diffed the form client-side would
give the same result with scripting on and the wrong one with it off — and the
wrong one is a secret overwritten by the word `present`.

The page carries the rendered value in a hidden field per row, so "what was
rendered" is a fact the request carries rather than one the server re-derives from
a file that may have changed underneath it.

Refusals are per key. The response names which keys were refused and which were
written; a partial batch never reports wholesale success.

### Updates

The unit prose renders when it is actionable:

| State | Rendered |
|---|---|
| a newer unit waiting | yes — the operator may take or leave it |
| overridden **and** an update available | yes — an update will not touch the override |
| anything else | no |

The detail moves into a `<details>` so nothing is deleted, only quietened.

### Source

The column goes. A row whose value did not come from the configuration file keeps
a marker in the value cell — the surprising case is the one worth the ink, and
the ordinary case was spending a quarter of the table on the word "file".

## Project Structure

```text
internal/httpapi/
├── settings.go        MOD  three sections; the corrected map; the rendered-value field
├── settings_edit.go   MOD  the batch write, reusing the per-key validation
├── server.go          MOD  register the batch route
└── outcome.go         MOD  outcomes for written / nothing-changed / partly-refused

web/
├── templates/settings.html   MOD  one form, one Save, three sections, no Source column
└── static/crswd.css          MOD  whatever the single Save and the row marker need
```

## Complexity Tracking

No violations. One thing is worth naming for review: the hidden per-row "as
rendered" value. It is caller-supplied data used to decide whether to write, which
sounds like trusting the caller — but the worst a forged one can do is cause a
write the operator is authorised to make anyway, or suppress one they asked for.
It cannot write a value the validators would refuse, and it cannot reach a key
they may not edit.
