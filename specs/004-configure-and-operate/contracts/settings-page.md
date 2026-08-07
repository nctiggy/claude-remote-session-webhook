# Contract: The settings page (read-only)

**Files**: `internal/httpapi/settings.go` (new), `web/templates/settings.html` (new)
**Tests**: `internal/httpapi/settings_test.go`
**Satisfies**: FR-016 … FR-020, SC-004, SC-005, SC-013

---

## Route

| Property | Literal |
|---|---|
| Path | `/settings` |
| Method | `GET` **only** |
| Audit action | `settings.view` |
| Unauthorised response | The uniform refusal, unchanged |
| Mutating verbs | **None registered.** A route that does not exist cannot be exploited. |

Editing is out of scope for this milestone (spec, Out of Scope). The absence of a
`POST` here is the safeguard, not a `POST` that refuses.

## What it renders

One row per configuration key, in the order `config.go` declares them:

| Column | Content |
|---|---|
| Key | The file spelling, e.g. `allowed_roots` |
| Value | The effective value — **or `present` / `absent` when `config.IsSecret(key)`** |
| Source | `flag`, `environment`, `file`, or `default` |

Above the table, one line naming the configuration file that was read, or saying
that none was (FR-018):

- `Read from /home/nctiggy/.config/crswd/config`
- `No configuration file was read.`

## Secrets

`config.IsSecret` is the **single** predicate, shared with the permission check
in `file.go`. The page never renders a secret's value, its length, a prefix, a
suffix, or a hash — `present` or `absent`, and nothing else.

Two keys are secret: `shared_secret` and `allowed_identities`.

## Worked example

Configuration as in [config-precedence.md](./config-precedence.md):

```
Read from /home/nctiggy/.config/crswd/config

  listen              0.0.0.0:9000                              environment
  allowed_roots       /home/nctiggy/code, /home/nctiggy/work    file
  allowed_identities  present                                   file
  shared_secret       present                                   file
  idle_timeout        -1                                        file
  pane_bound          200                                       default
```

Row 2 is what makes SC-004 answerable: an operator whose working directory was
refused reads the roots and sees immediately whether theirs is under one. Row 1
is what makes "why is my change not applied?" answerable.

## Contract tests

| Test | Asserts | **Must fail when** |
|---|---|---|
| `TestSettingsRequiresIdentity` | An unallowlisted caller gets the uniform refusal | The page is registered outside the identity middleware |
| `TestSettingsEmitsExactlyOneAuditRecord` | One record, action `settings.view` | The handler audits per row, or not at all |
| `TestSettingsNeverRendersSecretValue` | The rendered body does not contain the secret, any prefix of it, or its length | A "masked" value like `hun…` is introduced |
| `TestSecretRendersPresentOrAbsent` | Configured → `present`; unset → `absent` | The template prints the raw value for either secret key |
| `TestAllowedIdentitiesTreatedAsSecret` | It renders `present`, not the addresses | Only `shared_secret` is classified |
| `TestNamesConfigFileRead` | The path appears verbatim | The page omits it, breaking FR-018 |
| `TestSaysWhenNoFileRead` | `No configuration file was read.` | The line is blank instead |
| `TestShowsSourcePerKey` | Each row's source matches the shim's record | Sources are inferred at render time |
| `TestNoMutatingVerbRegistered` | `POST`, `PUT`, `PATCH`, `DELETE` to `/settings` all 405 | An edit route is added — out of scope this milestone |
| `TestFullRouteSweepLeaksNoSecret` | Exercising **every** route and searching all responses finds no secret value | Any page or error path prints one — this is SC-005 |
