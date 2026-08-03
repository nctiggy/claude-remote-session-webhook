# Phase 0 Research: crswd Daemon Core

**Feature**: [spec.md](./spec.md) · **Date**: 2026-08-02 · **Plan**: [plan.md](./plan.md)

Findings that change the design. Everything in the "verified" sections was run
against tmux 3.4 on an isolated socket (`tmux -L crswd-research`), not recalled from
memory — the transcript of each check is reproduced in the finding. Three of these
contradict the obvious implementation and would have shipped as defects.

---

## D1. One tmux *session* per crswd session, not one window

**Decision**: Each crswd session is its own tmux **session** named `crswd-<id>`,
containing a single window.

**Rationale**: The source documents use "window" loosely, but every operation they
mandate is session-scoped: `docs/auth-and-sessions.md` requires teardown via
`tmux kill-session` verified gone, and `ralph/IMPLEMENTATION_PLAN.md` requires
startup to "list tmux sessions matching the `crswd-` prefix". Windows are not
independently listable across sessions without extra bookkeeping, they have no
per-window creation timestamp usable for FR-024, and they cannot carry the
per-session user options D3 relies on. One tmux session per crswd session makes
list, kill, verify, adopt, and the lifetime clock all fall out of session-level
primitives.

**Alternatives rejected**: One shared tmux session with a window per crswd session —
kill/verify/adopt all become window-index arithmetic, and window indices are
renumbered by tmux, so a stored index can silently address a *different* session's
window. That is a session-bleed bug by construction and is disqualified by the
isolation rule in `docs/auth-and-sessions.md`.

---

## D2. Target addressing: `=name` for session targets, `=name:` for pane targets (VERIFIED)

**Decision**: Every tmux invocation addresses its target with an explicit exact-match
prefix. Commands taking a *target-session* (`has-session`, `kill-session`) use
`=crswd-<id>`. Commands taking a *target-pane* (`send-keys`, `capture-pane`,
`set-option`, `show-options`, `paste-buffer`) use `=crswd-<id>:` — **with the
trailing colon**.

**Why this is not cosmetic**: tmux resolves a bare name by exact match, then prefix,
then `fnmatch`. Historically `kill-session -t foo` could kill `foobar`. Under a
prefix-matching tmux, a caller-influenced name could address another session's
window — precisely the "caller-supplied tmux target string" FR-034 forbids.

**Verified on tmux 3.4**:

```
has-session -t crswd-abc12   (partial)  -> can't find session: crswd-abc12
has-session -t =crswd-abc123 (exact)    -> MATCHED
kill-session -t targe        (partial)  -> can't find session: targe
kill-session -t target  (with targetLONGER also present) -> killed only `target`
```

So 3.4 does **not** prefix-match. The `=` prefix is nevertheless mandatory in this
codebase: it makes the guarantee explicit and version-independent rather than
inherited from the host's tmux build.

**The trap** — the two target *kinds* take different syntax:

```
send-keys -t =sendtest   -> can't find pane: =sendtest      # bare `=name` is INVALID for a pane target
send-keys -t sendtest    -> OK                              # but not exact-matched
send-keys -t =sendtest:  -> OK                              # correct form
set-option -t =crswd-abc123  -> no such session             # same trap
set-option -t =crswd-abc123: -> OK
```

`internal/tmuxctl` must expose two distinct target helpers so a caller cannot pick
the wrong one; a single `Target()` string returning `=name` silently breaks every
pane-target command, and returning `=name:` silently breaks `has-session`.

---

## D3. Provenance: mark our sessions with a tmux user option (VERIFIED)

**Decision**: On create, set `@crswd-managed` (and `@crswd-owner`) on the tmux
session. On startup, adopt only sessions that carry the marker; a session whose name
merely matches the prefix is left alone.

**Rationale**: FR-022 requires distinguishing "ours" from "merely prefix-matching",
and a name prefix alone cannot do that — the operator may legitimately have a hand-made
`crswd-notes` session. A user option is metadata the daemon wrote itself.

**Verified**: options are per-session, readable by name or in a list format string,
and absent (rc=1, `invalid option`) on sessions we did not mark.

```
set-option -t crswd-abc123 @crswd-managed 1     -> set ok
show-options -v -t crswd-abc123 @crswd-managed  -> 1
show-options -v -t crswd-abc123-decoy @...      -> invalid option (rc=1)
list-sessions -F '#{session_name}=[#{@crswd-managed}]'
    -> crswd-abc123=[1] crswd-abc123-decoy=[] notours=[]
```

**Scope limit, stated plainly**: this is a *provenance* signal, not a security
boundary. Anyone who can run tmux as this user can set the option — but they can also
already run arbitrary commands as this user, so nothing is lost. It exists to stop the
daemon from adopting or killing something that was never its business.

---

## D4. Prompt delivery via `load-buffer` + `paste-buffer`, not `send-keys -l` (VERIFIED)

**Decision**: Prompt text is written to tmux's stdin through
`load-buffer -b <tmp> -` and delivered with `paste-buffer -d -b <tmp> -t =<id>:`.
`send-keys -l` is **not** used for caller-supplied text.

**Rationale — this is the finding that matters most.** `send-keys -l -- "$text"`
looks correct and is what the seeded task list implies, but tmux's *own* command
parser consumes a trailing unescaped `;` from the final argument, before the `-l`
literal flag ever applies. `--` does not prevent it.

**Verified byte-for-byte** (payload sent → bytes that landed in the pane):

| payload | landed | |
|---|---|---|
| `;` | `` (empty) | **silently swallowed** |
| `foo;` | `foo` | **trailing `;` stripped** |
| `foo;;` | `foo;` | one stripped |
| `foo;bar` | `foo;bar` | embedded is fine |
| `\;` | `;` | escaping works |

A long hostile prompt survives intact — `hi; echo PWNED; $(id) \`whoami\` "quoted"
'single' ${HOME} 100% \ backslash` came back byte-identical — which is exactly why
this would have passed a casual test and failed on the prompt that happens to end in
a semicolon. SC-012 says *byte-for-byte*; `send-keys -l` does not deliver that.

The fix is not to pre-escape hostile input (`docs/security.md` §2 is explicit that
building command text out of caller data is the thing to avoid). Instead the payload
travels via stdin and never becomes part of a tmux command line:

```
printf '%s' '; echo PWNED; $(id) `whoami`' | tmux load-buffer -b crswd -
tmux paste-buffer -d -b crswd -t '=sb:'
  -> landed: ; echo PWNED; $(id) `whoami`      # leading semicolon intact
  -> show-buffer -b crswd: "no buffer crswd"    # -d deleted it
```

`-d` deletes the buffer after pasting, so prompt text does not linger in a tmux
buffer readable by any other tmux client — which matters because prompts are secret
under `docs/security.md` §3. Nothing is written to disk: the payload goes to the
child process's stdin, not a temp file.

`send-keys` is still used for the *fixed, daemon-authored* strings — the `Enter`
key after a paste, and the `claude --dangerously-skip-permissions` startup line —
because those are constants, not caller input.

---

## D5. Pane capture emits no ANSI unless you ask for it (VERIFIED)

**Decision**: Capture with `capture-pane -p -t =<id>:` and **never** pass `-e`. Still
run a defensive control-character stripper over the result.

**Rationale**: tmux stores the *rendered* screen, so the default output is already
plain text — the escape sequences were consumed when the pane was drawn. Verified with
`od`: the default capture contains the literal characters the user typed
(`\ 0 3 3 [ 3 1 m` — seven ordinary bytes), whereas `-e` reintroduces real ESC bytes
(`033 [ 1 m 033 [ 3 2 m`) reconstructed from cell attributes.

So FR-031's "ANSI stripping" is achieved primarily by *not opting in*. The stripper
stays anyway, for two reasons: it is a second line of defence if someone later adds
`-e` for colour, and the rendered screen can still contain C0 control bytes and
oddities that have no business reaching a JSON response. Golden-file tests for the
stripper still earn their keep.

---

## D6. Adoption clock from `session_created` (VERIFIED)

**Decision**: An adopted session's absolute deadline is
`session_created + 24h`, read from tmux, satisfying FR-024 and SC-009.

**Verified**: `list-sessions -F '#{session_name}|#{session_created}'` yields Unix
epoch seconds (`crswd-abc123|1785706480`), which is precisely the origin the spec
demands. Everything adoption needs — name, creation time, and the `@crswd-managed`
marker — comes from a single `list-sessions -F` call with a composite format string,
so reconciliation is one exec, not one per session.

---

## D7. Dependency budget: standard library only

**Decision**: Zero third-party modules. `go.sum` stays absent for milestone 1.

Per `docs/security.md` §5 ("a new dependency needs justification: what does it do that
stdlib cannot?"), each candidate was checked rather than assumed:

| Need | Stdlib answer | Verdict |
|---|---|---|
| HMAC-SHA256, constant-time compare | `crypto/hmac`, `crypto/sha256`, `hmac.Equal` | stdlib |
| CSPRNG for IDs and tokens | `crypto/rand` | stdlib |
| HTTP server | `net/http` | stdlib |
| Method + path-parameter routing | `net/http.ServeMux` patterns (`POST /sessions/{id}/prompt`), Go 1.22+ | stdlib — no router needed |
| JSON with unknown-field rejection | `encoding/json` + `DisallowUnknownFields` | stdlib |
| Structured JSON logging | `log/slog` + `slog.NewJSONHandler(os.Stdout, …)` | stdlib |
| Process execution with argv | `os/exec` | stdlib |
| Path canonicalisation | `path/filepath` (`Clean`, `EvalSymlinks`) | stdlib |
| Graceful shutdown, signals | `net/http.Server.Shutdown`, `os/signal.NotifyContext` | stdlib |
| **Rate limiting** | no stdlib limiter | **hand-rolled, see below** |

**Rate limiting is the only real question.** `golang.org/x/time/rate` is the obvious
import. Rejected: it is a dependency whose entire job here is ~40 lines of token
bucket for *one* endpoint with a single caller identity, and the security doc puts the
burden of proof on the import. A `internal/httpapi` token bucket behind the same
injected clock as the reaper is testable without sleeping, which `x/time/rate` is not
(it reads the wall clock internally). Revisit if milestone 2 needs per-route limits
with burst tuning.

---

## D8. Go 1.23 is the ceiling, and it rules two things out

**Decision**: `go.mod` declares `go 1.23.0`.

`.github/workflows/ci.yml` pins `go-version: '1.23'` and activates the whole
build/test/lint job the moment `go.mod` appears (it gates on
`ls package.json pyproject.toml go.mod Cargo.toml`). The local toolchain here is
1.24.0, which compiles a 1.23 module fine — but anything 1.24-only fails in CI while
passing locally. Two attractive APIs are therefore **off limits**:

- `crypto/rand.Text()` (1.24) — use `rand.Read` into a `[]byte` and encode. See D9.
- `testing/synctest` (1.24, experimental) — cannot be used to test the reaper. This
  is already handled: FR-039 mandates an injected clock, which is the right design
  regardless.

`net/http.ServeMux` method-and-wildcard patterns are 1.22, so routing is safe.

---

## D9. Identifier encoding must be tmux-safe and name-regex-safe

**Decision**: Session IDs are 16 bytes from `crypto/rand` rendered as 32 lowercase
hex characters. Bearer tokens are 32 bytes rendered as 64 hex characters.

**Rationale**: the ID becomes part of a tmux session name (`crswd-<id>`), and tmux
session names must not contain `:` or `.` — the same characters FR-027 rejects in
caller-supplied names, for the same reason. Base64 (`+`, `/`, `=`) and base64url (`-`,
`_`, `=`) both introduce characters that are either tmux-hostile or padding. Hex is
boring, fixed-length, and `^[a-f0-9]{32}$`. 16 bytes = 128 bits, meeting FR-016's
floor exactly.

**Consequence worth stating**: the tmux target is derived from the **ID only**. The
caller-supplied session *name* is a display label stored in the record and never
appears in a tmux target. That is what makes FR-034 structurally true rather than
carefully maintained.

---

## D10. Replay cache and clock injection

**Decision**: A single `internal/auth` type holds `map[string]time.Time` keyed on the
full signature string, guarded by a mutex, with expired entries swept opportunistically
on write. TTL is `2 × 300s = 600s`, matching the doc's replay-cache row.

`Observe(sig, ts) bool` both checks and records in one critical section, so two
concurrent replays of the same request cannot both win — the spec's edge case "the same
signed request sent twice, concurrently" is a check-then-act race if these are split.

Both `internal/auth` and the reaper take the same `Clock` interface (`Now() time.Time`),
with a `fakeClock` in tests. No test sleeps.

---

## D11. Values the spec left as assumptions

The spec deferred two numbers to the plan. Proposed, both operator-configurable:

| Setting | Env | Default | Reasoning |
|---|---|---|---|
| Concurrent session cap | `CRSW_MAX_SESSIONS` | `5` | Each session is a Claude Code process; five is comfortable on a workstation and well under what would degrade the host. "Single digits" per the spec. |
| Create rate limit | `CRSW_CREATE_RATE_PER_MIN` | `6` burst `3` | A human operator creating sessions from a phone does not exceed this; a runaway client hits it immediately. |
| Max request body | `CRSW_MAX_BODY_BYTES` | `65536` | Prompts are text. 64 KiB is generous for a prompt and far below anything that pressures memory. |
| Listen port | `CRSW_LISTEN` | `127.0.0.1:8765` | Loopback is hard-coded in the sense that a non-loopback host is a startup failure (FR-005); the port is free to change. |

**Refusal codes** (chosen so nothing leaks about other sessions): cap reached and rate
limit both return `429 Too Many Requests` with a fixed body. Validation failures return
`400`. Auth failures return a uniform `401`. Ownership failures and unknown IDs both
return `404` with an identical body.

---

## D12. golangci-lint v1.62 config schema

**Decision**: `.golangci.yml` is written against the **v1** schema.

CI pins `golangci/golangci-lint-action@v6` with `version: v1.62`. golangci-lint v2
changed the config format incompatibly, so a v2-style file fails CI. Enable at least
`errcheck`, `gosec`, `govet`, `staticcheck`, `bodyclose` per the seeded task.

**Local gap, flagged**: `golangci-lint` is **not installed** on this machine
(`command not found`), and `.claude/hooks/format-and-lint.sh` no-ops when a tool is
absent. So lint currently passes locally by not running. `goimports` is likewise
absent. The first task should install both, or every "lint is green" claim before
that point is unverified. CI enforces regardless — the risk is discovering it late.

---

## Open items carried into the plan

None blocking. Two flagged for the operator's eye, neither gating implementation:

1. **`~/code` as the default root** (spec Assumptions) is confirmed to exist on this
   host — this repo lives under it — so the default is real rather than theoretical.
2. **`golangci-lint` and `goimports` are missing locally** (D12). Task T002 installs
   them; until then "lint passes" means "lint did not run".
