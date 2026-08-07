# Research: Configure and operate it

Six questions were open when this milestone was planned. Each is answered here
with what was chosen, why, and what was rejected. Nothing below is a guess; where
existing code already settled a question, the code is cited and adopted rather
than re-litigated.

---

## R1 — The configuration file's exact grammar

**Decision.** Flat `key = value`, one pair per line, no sections and no nesting.

- **Comments** are `#`, and only where `#` is the first non-whitespace character
  on a line. There are no trailing comments.
- **The separator** is the **first** `=` on the line. Whitespace around it is
  insignificant and is trimmed from both key and value.
- **A list** is spelled exactly as its environment variable is spelled today:
  comma-separated, on one line. `allowed_roots = /home/a,/home/b`. There are no
  repeated keys and no continuation lines; a repeated key is a refusal.
- **A key** is its environment variable minus the `CRSW_` prefix, lower-cased.
  `allowed_roots` is `CRSW_ALLOWED_ROOTS`. This is a rule, not a table.

**Rationale.** The trailing-comment exclusion is the one non-obvious part and it
is a security decision, not a style one: the shared secret may legitimately
contain `#`, and a parser that stripped from the first `#` would silently
truncate a secret and produce a daemon that starts, looks healthy, and rejects
every request. The existing `file.go` reaches the same conclusion in its own
comments, which is corroboration rather than coincidence.

Splitting on the **first** `=` rather than the only one is the same argument: a
secret or a start command may contain `=`, and a parser that refused an
ambiguous line would refuse valid configuration.

Deriving keys from variable names by rule means a variable added to `config.go`
is file-readable the same day, and a key matching no variable is an *unknown key*
rather than a silent no-op. FR-005 depends on that distinction existing.

**Alternatives considered.** YAML and TOML were rejected outright: neither has a
standard-library parser, both would create `go.sum`, and neither is safe to
hand-roll. SC-012 and `docs/security.md` §5 make this non-negotiable. JSON was
the closest real candidate and was rejected because it has no comments, and the
commentary in `config.example` is the most useful documentation this repository
has about what each bound is for — a format change that deleted it would be a
net loss of understanding.

---

## R2 — Where precedence is decided

**Decision.** Precedence is **flag → environment → file → default**, and it is
implemented in exactly one place: a `getenv` shim passed to the existing
`config.LoadFrom`.

```go
func withFile(getenv func(string) string, f *File) func(string) string {
	return func(key string) string {
		if v := getenv(key); v != "" {
			return v          // environment wins
		}
		return f.Lookup(key)  // then the file; "" means default
	}
}
```

**Rationale.** `LoadFrom(getenv func(string) string, warn io.Writer, ...)`
already exists and is already the single place every value is validated. Making
the file a *source* behind that seam rather than a *system* beside it means no
bound, no refusal and no default is written twice — so a value cannot mean one
thing in a unit and another in a file. It is also why FR-003 is free: no file
means the shim returns `""` for everything, which is precisely today's
behaviour, which is why SC-002 can be verified against the existing acceptance
suites unchanged.

Flags sit above the environment because a flag is typed by a person at the
moment of running, and the most immediate statement should win.

**Alternatives considered.** A merged `Config` struct assembled from both
sources, then validated — rejected because the merge is a second place where
precedence can be wrong, and it is silent when it is. A file that overrode the
environment — rejected because it breaks the container case in FR-004, where the
image ships a file and the orchestrator overrides one value.

---

## R3 — Which keys are secrets

**Decision.** Two keys are secret for both purposes — the mode refusal (FR-007)
and the present/absent rendering (FR-017):

| Key | Why |
|---|---|
| `shared_secret` | The API credential. Disclosure is total compromise of the six operations. |
| `access_allowed_emails` | The allowlisted addresses. Not a credential, but it names *who* can reach this daemon, which is exactly what an attacker needs to know to target the identity check. |

The classification lives in **one exported function** in `internal/config`, so
the permission check and the page render cannot disagree about what a secret is:

```go
func IsSecret(key string) bool
```

**Rationale.** FR-017 names both explicitly. Putting the predicate in one place
is what stops the settings page from confidently printing something the
permission check considered too sensitive to leave group-readable — a
disagreement that would be invisible until it mattered.

The permission refusal is conditional on a secret being **present in the file**,
not on the file existing: a file holding only `allowed_roots` is not a secret
file, and refusing to start over its mode would be a refusal the operator cannot
act on sensibly.

**Alternatives considered.** Treating every key as secret — rejected because it
makes the settings page useless for its actual purpose (SC-004: answering "why
was my working directory refused?" requires seeing the roots). Treating only
`shared_secret` as secret — rejected because FR-017 says otherwise, and because
the allowlist is the identity control.

---

## R4 — How the settings page learns each value's source

**Decision.** The same shim that decides precedence **records** it, because the
shim is the only code that knows. It writes a `map[string]Source` as it answers
lookups, where `Source` is one of `SourceFlag`, `SourceEnv`, `SourceFile`,
`SourceDefault`.

**Rationale.** Provenance is not an extra feature bolted onto the page; it is the
byproduct of having one place decide. Any other implementation would be a second
traversal that infers what the first one knew, and inference can be wrong exactly
when the operator most needs it to be right — that is, when a change is not
taking effect.

This is what makes SC-004 achievable and FR-018 meaningful: the page names the
file it read, and each value says whether it came from that file, from the
environment, or from neither.

**Alternatives considered.** Re-reading the environment at render time and
comparing — rejected because a value equal in both sources is indistinguishable,
and that is the confusing case. Storing provenance per field on `Config` —
rejected because it changes a struct that thirty tests construct directly.

---

## R5 — Where session mode lives

**Decision.** Mode is **derived** from the start-command name, not stored
alongside it. The set of names meaning "remote control" is configuration, and the
record persists the name it was started with as a fifth tmux user option,
`@crswd-start`.

**Rationale.** The record already carries `StartCommand` as a *name* — never a
command line, which is what keeps FR-030 true in both directions: no command line
reaches the browser, and none arrives from it. Deriving mode from that name means
there is nothing for two fields to disagree about after a rename, a restart, or a
toggle that half-succeeded.

FR-031 asks that the record carry which mode it is in. It does: it carries the
name that determines the mode, and the four options already persisted prove the
mechanism works across a restart. The genuine gap is that `StartCommand` is not
among those four, so a restarted session forgets which command started it — that
is one small task, and it is strictly smaller than maintaining a second source of
truth.

**Alternatives considered.** A boolean `RemoteControl` field on the record —
rejected as a second source of truth for one bit that is already implied. Parsing
the running process's command line to recover the mode — rejected because it
reads a command line into the daemon, which is the surface FR-030 exists to keep
closed.

---

## R6 — The combobox's no-JavaScript degradation path

**Decision.** The control is a native `<input list>` bound to a `<datalist>`.

```html
<input type="text" name="workdir" list="workdir-suggestions" ...>
<datalist id="workdir-suggestions">
  <option value="/home/nctiggy/code/claude-remote-session-webhook">
</datalist>
```

With no scripting at all, this is *already* the whole feature: the browser
filters the list as the operator types, the control is keyboard-operable, the
options are announced to a screen reader, and the field remains a free-text path
field — which is exactly the field that exists today. That satisfies FR-038,
FR-039, FR-040, FR-043 and FR-044 with no script running.

The script adds one thing and only one: the FR-045 announcement that the list is
showing a subset. That is an enhancement over a control that already works, in
the same shape as every other enhancement in this interface.

**Rationale.** The abandoned `#59` branch built a scripted combobox — 225 lines
of `crswd.js` reimplementing filtering, focus management, and ARIA roles that the
platform already provides correctly. It works, and it is the wrong foundation:
with scripting off it degrades to nothing, and it owns accessibility bugs the
browser would otherwise own. The discovery walk on that branch is genuinely
valuable and is carried forward; the control itself is replaced by markup.

FR-042 is unaffected either way, and this is worth stating plainly because it is
the security-relevant half: a path chosen from the list is validated exactly as a
typed one is. `<datalist>` is a convenience that submits an ordinary string. The
allowlist remains the control, and the picker never becomes an authorisation.

**Alternatives considered.** A `<select>` of discovered directories — rejected
because FR-040 requires any path to remain typeable, and a `select` forecloses
that. The scripted combobox from the branch — rejected as above, though its
discovery code survives.

---

## Carried-forward constraints, and how each is kept

These were not open questions. They are recorded so no task has to re-derive
them, and so a reviewer can check them without reading the whole spec.

| Constraint | How this design keeps it |
|---|---|
| FR-054 — zero dependencies | Nothing above needs a library. `go.sum` stays absent. |
| FR-055 — one audit record per request | The settings page is the only new route and it goes through the existing middleware, which is what emits the record. |
| FR-056 — refusal and 404 unweakened | No new route adds a distinguishing response. The settings page answers the uniform refusal to an unauthorised caller, like every other page. |
| FR-057 — no secrets in logs, records or pages | R3's `IsSecret` gates the render; the parser's own error text deliberately omits the value on a malformed line, for the reason given in R1. |
| FR-058 — both cross-site halves testable | The settings page is a GET and adds no mutating route; the mode toggle is mutating and inherits both halves unchanged. |
| FR-059 — motion, colour, keyboard | The datalist is the platform's own control. The card split adds a boundary that is not colour alone. Nothing added here animates. |
