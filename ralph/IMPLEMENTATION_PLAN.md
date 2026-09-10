# Implementation Plan

**Milestone 17 — lift #154's pane-level 401-wait proof onto `main`.**

> PR #153 shipped the fix and merged (`154e2ec`, 9/8/26). PR #154 was closed the
> same day as a duplicate — but it carried two acceptance cases #153 did not, and
> those cases are the difference between *the daemon composes the right map* and
> *the session actually got it*.

Four tasks. No behaviour changes. The daemon already does the right thing; what
is missing is the proof, taken from the only place it can honestly be taken from —
inside a real tmux pane, from the process the daemon started.

`ralph/VALIDATION_CONTRACT.md` is what "done" means. Read it before task 1.

---

## Why the pane and not the map

Every session on this host is a separate `claude` process against one credential
store holding one **rotating** refresh token. When the 8h access token expires they
all refresh at once; the losers replay a spent token, are told 401, and demand a
login on a host whose credential is fine. Claude Code ships the back-off and reads
it from `CLAUDE_CODE_OAUTH_401_WAIT_MS`, defaulting it to 60s only for a remote
session's child — which a local tmux pane is not, so the wait was 0.

**The first attempt at this fix shipped inside one of three start commands** — a
wrapper script — and was believed of the other two, including the `default` that a
session made from the dashboard uses. A test that reads the composed environment
map would have passed for that fix while dashboard sessions still raced. That is
the whole argument for these two cases, and the reason they are worth lifting off a
closed PR.

## Measured on this host, 2026-09-10 — do not re-derive

| Claim | Measured |
|---|---|
| `go build` in this worktree | **Fails**: `error obtaining VCS status: exit status 128`. Not the sandbox — it fails with the sandbox off too. Prefix every Go command with `GOFLAGS=-buildvcs=false`. |
| `go test ./...` bare | **Fails** on `internal/release` for that same reason, on unmodified `main`. Pre-existing, not this milestone's. |
| `GOFLAGS=-buildvcs=false go test ./...` | exit 0 |
| `GOFLAGS=-buildvcs=false go test -tags quickstart ./cmd/crswd -count=1` | exit 0, 36.7s, 15 cases + the dashboard file. `tmux`, `jq` and port 8765 are all available here. |
| `golangci-lint run` | `0 issues.` |
| The two test names exist on `main` | No. `grep -rn "401Wait\|shimEnv" cmd/crswd/` is empty. |

## The one place the lift is NOT verbatim

The branch changed `internal/config/sessionenv.go` as well, so that a
`sessionDefault`'s name counts as part of the base set and an operator overrides it
by setting it in the daemon's environment **alone**. That half did not merge. On
`main` the override route is the documented one:

> "To change or disable it, name it in `CRSW_SESSION_ENVIRONMENT` and set it in the
> daemon's own environment" — `README.md`, and `sessionenv.go`'s own comment.

So `TestQuickstart401WaitYieldsToOperator`, lifted **verbatim**, fails on `main`:
the daemon's `CLAUDE_CODE_OAUTH_401_WAIT_MS=0` is filtered out on the way in and
the default is appended over it. Task 2 adds `CRSW_SESSION_ENVIRONMENT` to that
daemon's environment and asserts `main`'s contract.

**Do not "fix" this by editing `internal/config/`.** That is reopening a design
question #153 settled, it is outside the files-touched list below, and a diff that
touches it is rejected.

## The lift, line by line

Source of truth for the original:
`git diff main...feat/lm-95-oauth-401-wait-default -- cmd/crswd/quickstart_test.go`.
**Do not delete that branch** — it is the only copy, and #154 is closed.

Everything lands in `cmd/crswd/quickstart_test.go`. Three anchors:

1. After `const shimEcho = "shim-read:"` (~line 83):

```go
// shimEnv prefixes the one environment variable the stand-in reports, so a test
// can read a session's actual environment rather than the daemon's idea of it.
// The value follows the prefix, or the word "unset".
const shimEnv = "shim-oauth-401-wait:"
```

2. Inside `writeShim`, between the `shimReady` line and the `while IFS=` line:

```go
"printf '" + shimEnv + "%s\\n' \"${CLAUDE_CODE_OAUTH_401_WAIT_MS-unset}\"\n" +
```

3. At the end of the file, after `TestSessionCarriesWhatRevivalNeeds`: the two
   tests and `shimEnvValue(t, pane)`, which takes the **last** matching line — a
   pane is a scrollback, and a revived session reports again.

Keep both test names exactly as written. They are short on purpose: `t.TempDir()`
puts the test's own name into `TMUX_TMPDIR`, and the socket path under it has to
stay inside `sun_path`'s 108 bytes. The descriptive spelling overflowed it.

`unset` (a `const` in this file) is the value that *removes* a variable from the
daemon's environment rather than setting it — that is how task 1 proves the daemon
stated `60000` rather than inheriting it from whatever ran `go test`.

---

## Tasks

- [ ] Shim and default, in `cmd/crswd/quickstart_test.go`: add `shimEnv`, print it from `writeShim`, add `shimEnvValue`, add `TestQuickstart401WaitReachesAPane` — daemon started with the variable `unset`, pane must report `60000`. Code in "The lift, line by line". Verify: `GOFLAGS=-buildvcs=false go test -tags quickstart ./cmd/crswd -count=1` exits 0.

- [ ] Operator override, same file: add `TestQuickstart401WaitYieldsToOperator` — daemon started with both `CLAUDE_CODE_OAUTH_401_WAIT_MS=0` and `CRSW_SESSION_ENVIRONMENT=CLAUDE_CODE_OAUTH_401_WAIT_MS`, pane must report `0`. Never edit `internal/config/`; see "NOT verbatim". Verify: `GOFLAGS=-buildvcs=false go test -tags quickstart ./cmd/crswd -count=1` exits 0.

- [ ] Prove the two cases are not vacuous, then leave the tree green. Edit only the `60000` in `internal/config/sessionenv.go` to `59000`, re-run the acceptance suite, and record that it **fails**. Restore it with `git checkout -- internal/config/sessionenv.go` and confirm `git status --porcelain` is clean of it before committing. Log both outcomes in `ralph/PROGRESS.md`.

- [ ] Confirm the whole tree, not just the new cases: `GOFLAGS=-buildvcs=false go test ./...`, `GOFLAGS=-buildvcs=false go test -tags quickstart ./cmd/crswd -count=1`, `go vet ./...` and `golangci-lint run` all exit 0 (`0 issues.`). The shared stand-in prints one more line now, so the full suite is the claim. Record each command's result in `ralph/PROGRESS.md`.

---

## Files touched

This milestone's blast radius. A diff outside this list is rejected.

- `cmd/crswd/quickstart_test.go` — the lift itself; all three anchors.
- `cmd/crswd/quickstart_dashboard_test.go` — **only if** the stand-in's extra
  output breaks a case there. It shares `writeShim`. Measured green today with the
  line absent; if one of its pane assertions turns out to be positional, fixing it
  is in scope. It is expected to stay untouched.
- `ralph/IMPLEMENTATION_PLAN.md` — ticking tasks.
- `ralph/PROGRESS.md` — the notebook.

Not in scope, and named so the boundary is not a judgement call:
`internal/config/sessionenv.go` (task 3 mutates it and **reverts** it — it must not
appear in any commit), `README.md` and `config.example` (both already document this
default correctly on `main`), and `docs/fixes-log.md` (the fix was logged on 9/8/26;
this is proof of it, not a new fix).
