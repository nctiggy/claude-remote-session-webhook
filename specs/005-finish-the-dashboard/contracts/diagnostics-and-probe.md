# Contract: Diagnostics, the audit trail, and the dependency probe

**Files**: `internal/config/depcheck.go`, `internal/httpapi/server.go`, `cmd/crswd/main.go`, `deploy/crswd.example.service`
**Tests**: `internal/config/depcheck_test.go`, `cmd/crswd/quickstart_test.go`
**Satisfies**: FR-023, FR-023a, FR-023b, FR-023c
**Closes**: #88, #96

---

Both defects here were found by **running the daemon**, not by reading it. Neither
appears in the issues this milestone was scoped from, and neither would have been
found by any amount of code review — which is the argument for the quickstart
suite that now runs in CI.

---

## Part 1 — The audit trail cannot be read as documented (#88)

`deploy/crswd.example.service` documented, before T015:

```
journalctl --user -u crswd -o cat | jq .
```

**Corrected as of T015**, and this section is kept as the record of why. The unit
and both READMEs now document the `grep '^{'` form below, and the quickstart
suite runs every `journalctl` line this repository commits against a real stream.

Measured on the live daemon:

| Command | Result |
|---|---|
| the documented one | **fails** |
| `journalctl --user -u crswd _COMM=crswd -o cat \| jq .` | **fails** |
| `journalctl --user -u crswd -o cat \| grep '^{' \| jq .` | works |

**The `_COMM` row is the finding.** Filtering to the daemon's own process still
fails, so the non-JSON lines are *the daemon's own* — not systemd's, as #88
assumed. Its dependency warnings and its `log` package errors share a stream with
the audit records.

### The fix is two changes, because the command is a symptom

1. **Human diagnostics go to stderr; audit records go to stdout.** They are
   different things for different readers.
2. **Document the filter anyway**, because systemd merges both into the journal:

```
journalctl --user -u crswd -o cat | grep '^{' | jq .
```

`grep '^{'` works because audit records are the only JSON — and that stays true
only because of change 1.

---

## Part 2 — The dependency probe asks the wrong environment (#96)

The live daemon prints on **every start**:

```
crswd: warning: the start command "rc" runs "claude", which is not on PATH.
crswd: starting anyway; sessions using "rc" will fail until it is present.
```

Sessions using `rc` do not fail. They work.

### Cause, measured

The probe uses `exec.LookPath` in the daemon's own process. The command does not
run there — it runs in a login shell inside a tmux pane.

| Environment | has `~/.local/bin` | finds `claude` |
|---|---|---|
| systemd user manager (the daemon inherits this) | no | **no** |
| login shell (what tmux spawns) | yes, via `.profile` | yes |

`claude` is at `/home/nctiggy/.local/bin/claude`. Both answers are right about
their own environment. The check asks the wrong one.

### The fix

Resolve the start command **the way the session will** — through a login shell,
the same resolution the command will actually get. Where that cannot be answered
confidently, say what was checked rather than asserting absence (FR-023c):

```
crswd: note: the start command "rc" runs "claude", which this daemon cannot see on
crswd: its own PATH. Sessions run it through a login shell, which may still find
crswd: it. Checked: the service manager's PATH.
```

### What does not change

**The tmux probe stays fatal and stays as it is.** The daemon execs `tmux`
itself, so its own PATH is the right environment to ask — the bug is specific to
commands that run inside a pane.

### The near miss, recorded

FR-012 made the start-command probe a **warning** rather than fatal, reasoning
that a daemon without one can still serve the dashboard and say what is wrong.
That is the only reason this did not refuse to start a healthy deployment — and it
was decided for an entirely different reason than the one that saved it.

The lesson is not that the reasoning got lucky. It is that **fatality belongs only
to what the daemon itself cannot do without**, and that rule held under a case it
was never designed for.

## Contract tests

| Test | Asserts | **Must fail when** |
|---|---|---|
| `TestAuditRecordsGoToStdout` | Every audit record is written to stdout | A record reaches stderr and the documented filter misses it |
| `TestDiagnosticsGoToStderr` | Every human diagnostic goes to stderr | A warning is written to stdout, breaking the "only JSON is JSON" property the filter depends on |
| `TestDocumentedCommandParses` | The command in `crswd.example.service` yields valid JSON on a daemon that has logged both records and diagnostics | The documentation drifts from what works |
| `TestProbeResolvesThroughLoginShell` | A command on the login shell's path but not the service manager's is **not** reported missing | The probe keeps asking the daemon's own PATH — the shipped defect |
| `TestProbeNamesWhatItChecked` | The message states which environment was inspected | An operator cannot tell a real absence from a probe looking in the wrong place |
| `TestGenuinelyMissingCommandStillWarns` | A command on neither path still warns | The fix silences the check entirely |
| `TestMissingTmuxStillFatal` | Empty PATH → refuses to start, naming the install command | The tmux probe is loosened along with the other one |
| `TestNoSecretInAnyDiagnostic` | No diagnostic contains a secret value | A warning quotes configuration verbatim |
