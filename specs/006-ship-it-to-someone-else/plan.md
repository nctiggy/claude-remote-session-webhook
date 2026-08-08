# Implementation Plan: Ship it to someone else

**Branch**: `006-ship-it-to-someone-else` | **Date**: 2026-08-07 | **Spec**: [spec.md](./spec.md)

## Summary

The last five issues, four of them a chain: **versioning → releases → installer →
self-update.** Each is meaningless without the one before it.

Two things make this milestone different from the five before it.

**It is about other people's machines.** Everything until now changed how the
daemon behaves for its author, here. An installer cannot be proven here — the
project is already installed, the config already exists, `~/.local/bin` is already
on `PATH`. Every precondition the installer exists to create is already true, so a
successful run on this box demonstrates nothing. Research R9 answers that with a
GitHub-hosted job running the published release against a fresh `HOME`.

**The updater's hard part is not cryptography.** Ed25519 is in the standard
library and the verification is twenty lines. The hard part is that a daemon must
replace **the binary it is currently executing** and leave a host that still runs
something. Research R5 answers it, and the step worth reading is the smoke test: a
checksum proves the bytes are the published bytes and says nothing about whether
they run *on this host*. An arm64 build on an amd64 machine passes every
cryptographic check and then fails to exec — and by then it is installed. Running
the staged binary with `--version` before the swap turns that from an outage into
a refusal, and it is only possible because US1 gave the binary a way to say what
it is.

## Technical Context

**Language/Version**: Go 1.23
**Primary Dependencies**: None. `crypto/ed25519`, `crypto/sha256`, `net/http`, `archive/tar` are all stdlib. `go.sum` stays absent.
**Storage**: `~/.local/share/crswd/` for staging and installer bookkeeping. No new persistent daemon state.
**Testing**: `go test ./...`, `-tags tmux`, `-tags quickstart`, all three in CI. Plus a new `verify-install` job on a GitHub-hosted runner.
**Target Platform**: linux/amd64 and linux/arm64. Ubuntu and systemd first.
**Constraints**: FR-034 … FR-038, carried forward.

### Unknowns

None. Nine questions were open; all nine are answered in
[research.md](./research.md).

## Constitution Check

| Principle | Gate | Verdict |
|---|---|---|
| I. Security is a gate | The update route runs code from the internet. | **PASS** — checksum, then signature against an embedded key, then a smoke test, and only then is anything renamed into place. The route joins the browser door through `handleAction`, so it inherits both cross-site halves unchanged. |
| II. Unknowns surfaced | Nine questions, several with no obvious answer. | **PASS** — all nine answered with rationale and alternatives; none guessed. |
| III. Every change verifiable | The installer's requirements are about a machine this one is not. | **PASS** — R9 names the mechanism, and the contracts' "must fail when" rows name the *it worked on the author's machine* failure explicitly. |
| IV. Smallest correct change | Self-update could have been a supervisor. | **PASS** — it is stage, verify, rename, exit. systemd already restarts things; this adds no process manager. |
| V. Standards enforced | Full gate per task at v2.12.2. | **PASS** |
| VI. Blast radius bounded | New behaviour is in new files. | **PASS** — `internal/buildinfo`, `internal/updater`, `install.sh`, `.github/workflows/release.yml`. |
| VII. Design system binding | The update control and the rain. | **PASS** — tokens only, confirming step like destroy's, nothing animating under reduced motion. |

**Post-design re-evaluation**: one thing changed and it is worth recording. The
spec originally inherited #66's position that the update must not be reachable
from the browser. The operator challenged it and the challenge held: an attacker
with the dashboard can already create a session running a permission-skipping
assistant in an approved root, which is code execution. The update route lets them
install *a release this project signed*, which is strictly less. **The signature
bounds the damage, not the door.** The route therefore joins the browser door with
the gate and a confirming step, exactly as destroy does.

## Project Structure

```
specs/006-ship-it-to-someone-else/
├── spec.md · plan.md · research.md · data-model.md · quickstart.md
└── contracts/
    ├── version.md             # US1 — the stamp, the sentinel, the two readers
    ├── release.md             # US2 — asset names, retention, reproducibility
    ├── signing.md             # the key's lifecycle; the operator holds it
    ├── installer.md           # US3 — decomposed; and how it is proven elsewhere
    ├── self-update.md         # US4 — decomposed; stage → verify → smoke → swap
    └── rain-messages.md       # US5
```

```
internal/buildinfo/buildinfo.go     # NEW — one variable, default "dev"
internal/updater/
├── fetch.go                        # NEW — TLS, no cross-host redirects, exact asset
├── verify.go                       # NEW — sha256 then ed25519, in that order
├── stage.go                        # NEW — 0600 until verified; swept at startup
├── swap.go                         # NEW — smoke test, atomic rename, exit
└── release_key.txt                 # NEW — public half, committed, one key per line

internal/httpapi/
├── update.go                       # NEW — POST route via handleAction, confirming step
└── version.go                      # NEW — GET, reports buildinfo.Version

cmd/crswd/
├── main.go                         # --version, and the keygen subcommand
└── keygen.go                       # NEW — prints both halves; writes nothing

install.sh                          # NEW — repo root, no individual's name in it
.github/workflows/release.yml       # NEW — tag-triggered; builds, signs, prunes
deploy/crswd.example.service        # Restart=always, which self-update depends on
```

## The dependency chain is strict

```
US1 version  →  US2 release  →  US3 installer
                    │
                    └─────────→  US4 self-update  (also needs US1's --version
                                                    for the smoke test)
```

US4 depends on US1 twice: once to know what to install, once to prove the staged
binary runs. US5 is independent of all of it.

## Complexity Tracking

| Choice | Why it is not a violation |
|---|---|
| The asset name format is written three times — YAML, shell, Go | They cannot share a constant across three languages. The duplication is unavoidable; the drift is not, so a test reads `install.sh` and the Go constant and asserts they agree. A drift is a 404 at the moment someone is installing. |
| The updater execs the staged binary before installing it | The alternative is discovering it does not run *after* it is the installed binary. This is the difference between a refusal and an outage. |
| No automatic rollback | A daemon that cannot start cannot decide it should be replaced. Doing it properly needs a supervisor this project does not have, so the failure is made loud instead: the previous binary is kept, and rolling back is a documented one-liner. |
| `Restart=always` added to the unit | Self-update exits and relies on systemd to restart into the new binary. Without it, step 7 is just the daemon stopping. |
