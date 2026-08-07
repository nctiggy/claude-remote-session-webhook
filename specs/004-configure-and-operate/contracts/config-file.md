# Contract: The configuration file

**File**: `internal/config/file.go` (carry forward from `claude/issue-issue-65-20260807-0112`)
**Tests**: `internal/config/file_test.go`
**Satisfies**: FR-001 … FR-011

---

## Grammar

Normative. A parser that accepts more than this is wrong in the same way one that
accepts less is.

```
file    := line*
line    := blank | comment | pair
blank   := WS* EOL
comment := WS* "#" ANY* EOL
pair    := WS* key WS* "=" WS* value WS* EOL
key     := [a-z0-9_]+
value   := ANY*            ; may contain "=" and "#"
```

Five rules follow from that, and each is load-bearing:

1. **Comments are whole lines only.** `#` is a comment marker only where it is
   the first non-whitespace character. There are no trailing comments, because
   `shared_secret` may legitimately contain `#` and stripping from the first one
   would silently truncate a secret.
2. **The separator is the first `=`.** Split with `strings.Cut`, not
   `strings.Split`. A value may contain `=` — `start_commands` always does.
3. **Whitespace around the separator is insignificant.** Key and value are both
   trimmed of leading and trailing whitespace. Whitespace *inside* a value is
   preserved exactly.
4. **A list is comma-separated on one line**, spelled exactly as the environment
   variable is spelled today. No repeated keys, no continuation lines.
5. **A key is its environment variable minus `CRSW_`, lower-cased.** This is a
   rule, not a table. `allowed_roots` ⇔ `CRSW_ALLOWED_ROOTS`.

## Worked example

```
# crswd configuration. Comments explain why a bound is what it is,
# which is the reason this file is not JSON.
version = 1

listen = 127.0.0.1:8787

# The containment boundary. A session may not be created outside these.
allowed_roots = /home/nctiggy/code,/home/nctiggy/work

# name=command pairs. Note the "=" inside the value: the parser splits
# on the FIRST "=" only, which is why this line means what it looks like.
start_commands = default=claude --dangerously-skip-permissions,rc=claude remote-control --permission-mode bypassPermissions

# Sessions live a day unless told otherwise; -1 disables idle reaping.
session_lifetime = 24h
idle_timeout = -1

# This value contains a "#". It is not a comment, because a comment
# marker is only a comment marker at the start of a line.
shared_secret = hunter2#not-a-comment
```

Parsing that file yields exactly seven keys, of which **six are settings**:
`version` is the schema the file was written against, not something the daemon
reads, so it maps to no environment variable and the settings page must not
render a row for it.

`shared_secret` is `hunter2#not-a-comment`, and `start_commands` retains both `=`
signs in its value.

## Refusals

Each refusal names the file and the line. **No refusal ever includes the
value**, because the malformed line may be a secret with a typo in it and the
error is written to stderr and kept in a journal.

| Condition | Message shape | FR |
|---|---|---|
| Unknown key | `config file %s:%d has unknown key %q; refusing to start` | FR-005 |
| Renamed key | `config file %s:%d: %q was renamed to %q; accepting it for now` — a **warning**, not a refusal | FR-006 |
| Malformed line | `config file %s:%d is not a comment, blank, or key=value; refusing to start` | FR-005 |
| Repeated key | `config file %s:%d repeats key %q; refusing to start` | FR-005 |
| Too permissive | `config file %s is mode %04o, so it is readable by other accounts on this host and may hold the shared secret; run chmod 600 %s` | FR-007 |
| Future schema | `config file %s:%d has version %d; this daemon understands %d; refusing to start` | FR-009 |
| Bad version | `config file %s:%d has a version that is not a whole number; refusing to start` | FR-009 |

The permission refusal fires only when the file **contains a secret key**
(`shared_secret` or `access_allowed_emails` — see `config.IsSecret`). A file holding
only `allowed_roots` is not a secret file, and refusing to start over its mode
would be a refusal the operator cannot act on sensibly.

## The daemon never writes

`file.go` contains no write path at all. Not a formatter, not a normaliser, not
an "upgrade in place". `crswd config migrate` is the *only* code that writes a
config file, it is explicit, and it keeps a backup at `config.bak` (FR-009).

## Contract tests

| Test | Asserts | **Must fail when** |
|---|---|---|
| `TestParseAcceptsWorkedExample` | The eight keys above parse to exactly those values | The parser splits on the last `=`, or strips trailing `#` |
| `TestValueMayContainHash` | `shared_secret` parses as `hunter2#not-a-comment` | Trailing-comment stripping is added |
| `TestValueMayContainEquals` | `start_commands` retains both `=` signs | `strings.Split` replaces `strings.Cut` |
| `TestWhitespaceAroundSeparatorIgnored` | `a=b`, `a = b`, `  a  =  b  ` all yield `a`→`b` | Key or value is not trimmed |
| `TestWhitespaceInsideValuePreserved` | `start_commands` keeps its internal spaces | The value is over-trimmed or collapsed |
| `TestUnknownKeyRefuses` | Error names the key and the line | An unknown key is skipped, warned, or accepted |
| `TestRenamedKeyWarnsAndAccepts` | Both spellings named; parse **succeeds** | A rename is treated as an unknown key |
| `TestMalformedLineRefuses` | Error names the line number | A line with no `=` is skipped silently |
| `TestRepeatedKeyRefuses` | Error names the key | Last-wins or first-wins is implemented instead |
| `TestErrorNeverContainsValue` | No refusal message contains the value text | A message is built with `%q` on the value or the raw line |
| `TestGroupReadableWithSecretRefuses` | Mode `0640` + `shared_secret` refuses, naming `chmod 600` | The mode check is dropped or only checks world |
| `TestGroupReadableWithoutSecretStarts` | Mode `0644` + only `allowed_roots` **starts** | The mode check ignores whether a secret is present |
| `TestFutureVersionRefuses` | `version = 99` refuses, naming both numbers | The version key is ignored |
| `TestMissingFileIsNotAnError` | Absent file returns empty, nil error | Absence becomes a refusal — this breaks SC-002 |
| `TestParserNeverWrites` | The file's mtime and bytes are unchanged after a parse | Any write path is introduced |
