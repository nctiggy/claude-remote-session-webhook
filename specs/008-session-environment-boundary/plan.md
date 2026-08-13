# Implementation Plan: The boundary between the daemon's environment and a session's

**Branch**: `008-session-environment-boundary` | **Date**: 2026-08-13 | **Spec**: [spec.md](./spec.md)

**Input**: Feature specification from `/specs/008-session-environment-boundary/spec.md`

## Summary

Two halves of one boundary. **Outward**: the daemon hands every session its own
environment, so the shared secret and the Access values sit in the environment of a
process running with permissions skipped. The fix is an allowlist applied at
`internal/tmuxctl`'s single exec chokepoint — **plus** a reconciliation of the running
tmux server's global environment, because research established that the chokepoint
alone leaves every existing deployment exposed for as long as its tmux server lives.

**Inward**: an operator who needs `sudo` in a session has nowhere to put that change but
the unit, which permanently costs them the unit's updatability. The fix is a systemd
drop-in the installer offers at install time, which nothing automated ever touches — and
which must relax `ProtectKernelTunables` as well as `NoNewPrivileges`, because the
former implies the latter and relaxing the obvious one alone provably does nothing.

## Technical Context

**Language/Version**: Go 1.x (module `github.com/nctiggy/claude-remote-session-webhook`), plus POSIX-ish `bash` for `install.sh`

**Primary Dependencies**: standard library only — the constitution keeps this repo free of `go.sum`. External binaries: `tmux` (3.4 on the reference host; `set-environment` is available in every supported version), `systemd` user manager

**Storage**: files only — the operator's configuration at `~/.config/crswd/config`, the systemd unit and its drop-in directory under `~/.config/systemd/user/`

**Testing**: `go test ./...`; build-tagged suites `tmux`, `quickstart`, `dev`. The `tmux` tag is the one this feature most needs — it drives the real binary on a private `-L` socket

**Target Platform**: Linux with a systemd user manager and lingering enabled

**Project Type**: single Go project — daemon plus embedded web assets, no frontend build

**Performance Goals**: not a factor. The environment is composed once per exec; the global-environment reconciliation runs once per daemon start

**Constraints**: the reconciliation must not disturb running sessions; a session must remain a working shell; the installer must behave correctly when its own stdin is the script (`curl | bash`)

**Scale/Scope**: one host, one daemon, sessions bounded by `max_sessions` (default 5)

## Constitution Check

*GATE: Must pass before Phase 0 research. Re-check after Phase 1 design.*

| Principle | Verdict | Evidence |
|---|---|---|
| **I. Security is a gate (NON-NEGOTIABLE)** | **PASS — and this feature is the gate closing** | It removes a live credential exposure rather than adding surface. `docs/security.md` is loaded and binding for this work |
| **II. Unknowns surfaced, never invented (NON-NEGOTIABLE)** | **PASS** | Every open question was resolved by testing on a real host, not by assertion. R2 and R5 both contradicted the initial assumption and the finding was kept. Milestone 14's explicitly-unverified drop-in claim is now verified |
| **III. Every change is verifiable** | **PASS** | Each FR maps to a test in [quickstart.md](./quickstart.md). The two that need a real tmux go behind the `tmux` tag |
| **IV. Smallest correct change** | **PASS, with one deliberate exception** | The environment is fixed at one chokepoint, not eight call sites. The exception is R2's second mechanism — see Complexity Tracking |
| **V. Standards are enforced, not documented** | **PASS** | FR-005 requires one definition with a failing test; FR-007's refusal is a startup failure, not a warning; the drop-in's `ProtectKernelTunables` requirement is asserted, not just written in prose |
| **VI. Blast radius is bounded by construction (NON-NEGOTIABLE)** | **PASS — narrows, does not widen** | Part A strictly removes what a session can reach. Part B keeps the shipped unit hardened; the only widening is opt-in, per-host, and named at the moment of consent. A feature that widened any bullet would need justification here, and none is claimed |
| **VII. Design system is binding** | **N/A → one line** | FR-013 adds a fact to the existing `.version-facts` list on the settings page. `docs/design-system.md` and `docs/components.md` apply and no new component is introduced |

**Gate result: PASS.** No unjustified violations. The single entry in Complexity
Tracking is a justified necessity rather than a preference.

## Project Structure

### Documentation (this feature)

```text
specs/008-session-environment-boundary/
├── plan.md              # This file
├── spec.md              # Feature specification
├── research.md          # Phase 0 output — empirical findings
├── data-model.md        # Phase 1 output
├── quickstart.md        # Phase 1 output — validation scenarios
├── contracts/
│   ├── session-environment.md   # what a session receives, and what it never does
│   ├── installer-prompt.md      # the question, its answers, and the no-terminal case
│   └── hardening-dropin.md      # the drop-in's path, contents and ownership
├── checklists/
│   └── requirements.md  # spec quality checklist (complete)
└── tasks.md             # Phase 2 output — NOT created by /speckit-plan
```

### Source Code (repository root)

```text
internal/
├── config/
│   ├── config.go         # + the pass-through list variable and its bounds (FR-006)
│   ├── secret.go         # IsSecret reused, never restated (FR-002)
│   └── sessionenv.go     # NEW — the one definition of a session's environment (FR-005)
├── tmuxctl/
│   ├── exec.go           # run() sets cmd.Env at the single chokepoint (FR-001)
│   └── env.go            # NEW — reconcile the server's global environment (R2)
├── session/
│   └── manager.go        # startup reconciliation call site
├── updater/
│   └── unit.go           # UnitReport gains the drop-in fact (FR-013)
└── httpapi/
    └── settings.go       # renders that fact (FR-013)

cmd/crswd/
└── unit.go               # journal sentence gains the same fact (FR-013)

deploy/
├── crswd.example.service            # unchanged — stays hardened (FR-008)
├── crswd.service.d/10-relax.conf.example  # NEW — the drop-in the installer writes
└── README.md                        # FR-015, FR-016

install.sh                # the prompt, /dev/tty, and the drop-in write (FR-009..FR-012, FR-014)
web/templates/settings.html          # the rendered fact (FR-013)
```

**Structure Decision**: The existing single-project layout is kept. Two new files, both
because the alternative is worse: `internal/config/sessionenv.go` exists so FR-005's
"exactly one definition" is a file somebody has to delete rather than a convention
somebody can forget, and `internal/tmuxctl/env.go` exists so the global-environment
reconciliation is not buried inside `exec.go`'s chokepoint, where a future early return
would silently skip it.

## Complexity Tracking

| Violation | Why Needed | Simpler Alternative Rejected Because |
|-----------|------------|-------------------------------------|
| **Two mechanisms for one boundary** (`cmd.Env` at exec, *plus* reconciling the tmux server's global environment) — a departure from Principle IV's smallest correct change | The tmux server outlives the daemon by design: adoption reclaims sessions across restarts, and `Restart=always` plus self-update replace the process regularly while the server persists. Verified on the live host — its server's global environment holds 20 `CRSW_` variables including the shared secret | Setting `cmd.Env` alone was measured and is **not sufficient**: it corrects only servers started fresh, so every existing deployment would stay exposed indefinitely — the exact "quietly wrong host" this project keeps eliminating. Killing the server would work and was rejected for destroying the operator's running sessions to fix ones not yet created |

## Phase 1 Design Notes

**What cannot be fixed, and is therefore documented rather than claimed.** A process's
environment cannot be changed from outside it. Sessions already running when the fix
lands keep the secrets they were started with until they are recreated. The daemon must
not report those hosts as clean, and `deploy/README.md` must tell the operator to
recreate sessions after upgrading. Saying otherwise would be exactly the false
reassurance milestone 15 existed to end.

**The installer's question names its consequence.** FR-009 is not satisfied by asking
"enable sudo?". The operator is consenting to a path from an authenticated request to
root on the host, and Principle VI's standard for a widening is naming what becomes
reachable. The wording is fixed in [contracts/installer-prompt.md](./contracts/installer-prompt.md).

**Post-design constitution re-check: PASS.** The design adds no new component, no new
dependency, no second definition of a secret, and no new reachability. The one
complexity entry is recorded above with its measurement.
