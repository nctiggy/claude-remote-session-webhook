---

description: "Task list for 008-session-environment-boundary"
---

# Tasks: The boundary between the daemon's environment and a session's

**Input**: Design documents from `/specs/008-session-environment-boundary/`

**Prerequisites**: [plan.md](./plan.md), [spec.md](./spec.md), [research.md](./research.md), [data-model.md](./data-model.md), [contracts/](./contracts/)

**Tests**: **Required, not optional.** Constitution Principle III and AGENTS.md both
demand a test that fails without the change; auth and session code additionally needs
its negative cases. Every implementation task below is preceded by the test that fails
without it.

## Format: `[ID] [P?] [Story] Description`

- **[P]**: Can run in parallel (different files, no dependencies)
- **[Story]**: Which user story this task belongs to (US1, US2, US3)

## Path Conventions

Single Go project at the repository root: `internal/`, `cmd/crswd/`, `deploy/`,
`web/`, plus `install.sh`. Tests are colocated with the package they cover.

---

## Phase 1: Setup

**Purpose**: Confirm the environment the later phases assert against, before writing anything that depends on it.

- [X] T001 Confirm the `tmux` suite runs on this host with `go test -tags tmux ./...`, and record the tmux version; US1's reconciliation test is behind that tag and `set-environment` behaviour is what it asserts
- [X] T002 [P] Confirm a systemd user manager is present and `systemd-run --user` works, since US2's drop-in assertions depend on it

---

## Phase 2: Foundational (Blocking Prerequisites)

**Purpose**: The names and paths more than one story spells. Getting these wrong is the drift this repo keeps finding.

- [X] T003 Add the drop-in directory and file name as exported constants in `internal/updater/unit.go`, beside the existing `unitPath` and `unitRecordPath`, composed from HOME the same way and for the same stated reason
- [ ] T004 Add a test in `internal/release/install_test.go` holding those constants against the path `install.sh` writes, in the shape of the existing `TestUnitPathsAreWhereTheInstallerWrites` — two languages, one path, drift not allowed **(deferred to Phase 4: asserts against the drop-in path install.sh gains in T026)**

**Checkpoint**: US1 and US2 can now proceed independently.

---

## Phase 3: User Story 1 — A session cannot read the operator's secrets (Priority: P1) 🎯 MVP

**Goal**: A session receives a composed environment, never the daemon's own.

**Independent test**: From inside a freshly created session, `env | grep -c '^CRSW_'` returns `0`, and the session is still a working shell. Delivers the whole security value with US2 and US3 absent.

### Tests (write first — each must fail)

- [X] T005 [P] [US1] Add `internal/config/sessionenv_test.go` asserting the composed set excludes every key `IsSecret` names and every `CRSW_`-prefixed name, and includes the base names when present in the source environment (data-model V1, V2, V3)
- [X] T006 [P] [US1] Add a case asserting a base name absent from the source environment is **omitted, not passed empty** (V3), and that `PATH` is carried through unchanged (V4)
- [X] T007 [P] [US1] Add a case in `internal/config/sessionenv_test.go` proving the composed set is the *whole* environment — no merge with the parent — so V1 and V2 are properties of the result rather than of caller discipline (V5)
- [X] T008 [P] [US1] Add tests in `internal/config/config_test.go` for the pass-through list: a normal name passes; a name absent from the environment is not an error (V8); a name `IsSecret` classifies **fails startup** naming the entry (V6); a `CRSW_` name fails the same way (V7)
- [X] T009 [US1] Add a `//go:build tmux` test in `internal/tmuxctl` that starts a server on a private `-L` socket with a marker variable set, runs the reconciliation, creates a **new** session, and asserts the pane process does not carry the marker — the R2 finding, asserted rather than trusted
- [X] T010 [US1] Add a case to the same file asserting reconciliation leaves a **running** session's pane untouched (V12) and removes only names, never adding or overwriting (V11)

### Implementation

- [X] T011 [US1] Create `internal/config/sessionenv.go` as the single definition of a session's environment: the base list from [contracts/session-environment.md](./contracts/session-environment.md), composed from a `getenv`, reusing `IsSecret` from `internal/config/secret.go` rather than restating it (FR-002, FR-005)
- [X] T012 [US1] Add the pass-through configuration variable to `internal/config/config.go` following the key rule stated at `internal/config/file.go:17` — the variable minus `CRSW_`, lower-cased — so no table needs editing (FR-006, research R7)
- [X] T013 [US1] Make an entry naming a secret or a `CRSW_` variable a **startup failure** in `internal/config/config.go`, naming the offending entry and the reason, never the value (FR-007; `docs/security.md` forbids a refusal that names what it refused over)
- [X] T014 [US1] Set `cmd.Env` from the composed set in `internal/tmuxctl/exec.go`'s `run`, at the single `exec.CommandContext` chokepoint all eight builders funnel through (FR-001)
- [X] T015 [US1] Create `internal/tmuxctl/env.go` reconciling a running server's **global** environment against the allowlist via `set-environment -g -u`, kept out of `exec.go` so a future early return cannot silently skip it (research R2)
- [X] T016 [US1] Call the reconciliation once at daemon startup from `internal/session/manager.go`, before any session is created, so an **adopted** server is corrected rather than left dirty for the server's lifetime
- [X] T017 [US1] Thread the composed environment from configuration load into `tmuxctl` construction, so the pass-through list reaches the chokepoint without `tmuxctl` reading the environment itself

**Checkpoint**: SC-001 and SC-005 both provable. `go test ./...` should now pass **from inside a session**, which it does not today.

---

## Phase 4: User Story 2 — Needing sudo does not cost every future update (Priority: P2)

**Goal**: The shipped unit stays hardened and replaceable; the operator's deviation lives where nothing automated touches it.

**Independent test**: Install answering yes; `sudo` works in a session **and** the unit is byte-identical to the release's. Testable with US1 absent.

### Tests (write first — each must fail)

- [ ] T018 [P] [US2] Add a test in `internal/release/install_test.go` asserting the shipped `deploy/crswd.example.service` still carries its four hardening settings — FR-008's guard, so no future edit quietly relaxes the default
- [ ] T019 [P] [US2] Add a test asserting the example drop-in overrides **`ProtectKernelTunables`** and not only `NoNewPrivileges`; the measured trap from research R5, where the obvious override alone is inert (FR-011, V14)
- [ ] T020 [P] [US2] Add a test reading `install.sh` as text asserting the answer is read from `/dev/tty` and **not** from stdin, since the documented install path is `curl | bash` where stdin is the script (FR-009, research R4)
- [ ] T021 [P] [US2] Add a test asserting `install.sh` treats an unopenable `/dev/tty` as no, and that no code path writes the drop-in without an affirmative answer (FR-010)
- [ ] T022 [P] [US2] Add a test asserting neither `install.sh` nor `internal/updater` contains a write, rename or remove targeting the drop-in directory outside the single first-run creation (FR-012, FR-014, V16)
- [ ] T023 [P] [US2] Add tests in `internal/updater/unit_test.go` for the new report fact: override present with a matching unit must **not** report as simply matching the release (FR-013, V18)

### Implementation

- [ ] T024 [P] [US2] Create `deploy/crswd.service.d/10-relax.conf.example` with the four overrides from [contracts/hardening-dropin.md](./contracts/hardening-dropin.md), carrying in comments why `ProtectKernelTunables` is load-bearing and the measurement behind it
- [ ] T025 [US2] Add the prompt to `install.sh` reading from `/dev/tty`, with the wording fixed in [contracts/installer-prompt.md](./contracts/installer-prompt.md) — it must name **root**, not sudo, because Principle VI's standard for a widening is naming what becomes reachable
- [ ] T026 [US2] Write the drop-in on an affirmative answer only, reporting the path written; leave hardening intact otherwise (FR-009, V15)
- [ ] T027 [US2] Make a later `install.sh` run idempotent: do not re-ask, rewrite, duplicate or remove an existing drop-in; report that one is present and where (FR-014)
- [ ] T028 [US2] Extend `UnitReport` in `internal/updater/unit.go` with whether an override is in effect, read off the files like every other fact that surface reports (FR-013, research R6)
- [ ] T029 [P] [US2] Render the fact on the settings page via `internal/httpapi/settings.go`'s `unitFactsOf`, as a row of the existing `.version-facts` list — no new component, per `docs/components.md`
- [ ] T030 [P] [US2] Say the same thing in the journal from `cmd/crswd/unit.go`'s `sayWhatBecameOfTheUnit`, from the shared vocabulary rather than a second computation (V19)
- [ ] T031 [US2] Update `web/templates/settings.html` for the added row, per `docs/design-system.md`

**Checkpoint**: SC-002, SC-003 and SC-004 provable.

---

## Phase 5: User Story 3 — A hand-edited unit can get back onto the supported path (Priority: P3)

**Goal**: The operators who already exist have a written way home.

**Independent test**: Follow the procedure on a host with a hand-edited unit; end with preserved capabilities, a vanilla unit, and a daemon reporting the unit as its own.

### Tests

- [ ] T032 [P] [US3] Extend the documentation test in `internal/config/deployexample_test.go` (or `internal/release/readme_test.go`) so the drop-in path, the unit path and the record path named in prose are held to the constants from T003 — an operator must never be sent to diff a file that is not there
- [ ] T033 [P] [US3] Assert `deploy/README.md` names the `ProtectKernelTunables` trap, since an operator who relaxes only the obvious setting finds `sudo` still broken with nothing that looks like the cause (FR-015)

### Implementation

- [ ] T034 [US3] Document in `deploy/README.md` what taking the drop-in does and does **not** hand over, under the section that already explains why the unit looks the way it does
- [ ] T035 [US3] Document the migration procedure in `deploy/README.md`: move deviations into the drop-in, restore the shipped unit, and record the digest — noting that migration also moves the binary to the shipped `ExecStart` path (FR-016)
- [ ] T036 [US3] Document in `deploy/README.md` that sessions **running** at upgrade keep their old environment and must be recreated, since a process's environment cannot be changed from outside (data-model V13) — the limit stated rather than papered over
- [ ] T037 [P] [US3] Add the branches to `README.md` beside the update table it already carries, so an operator can read what an update does before deciding to run one
- [ ] T038 [P] [US3] Record the session-environment boundary in `docs/security.md` beside the existing "Session output is secret" rule, which this feature makes enforceable from the inside

**⚠️ Trap inherited from milestone 14**: any line beginning `journalctl` in either README is executed by the `quickstart` suite against a captured stream and must reject a truncated record. Adding one to a fenced block fails a suite that may not be runnable on this host.

---

## Phase 6: Polish & Cross-Cutting

- [ ] T039 Run the full gate: `go build ./...`, `go vet ./...`, `go test ./...`, `-tags tmux`, `-tags dev`, `golangci-lint run`, `gofmt -l .`
- [ ] T040 Run `go test ./internal/httpapi -run TestEditRefusesAValueThatWouldNotLoad` **from inside a session** and confirm it passes — SC-005, and the end-to-end proof that FR-003 holds
- [ ] T041 [P] Walk [quickstart.md](./quickstart.md) scenarios 1–8 against a real host and record which could not be run and why, rather than reporting green on unrun cases
- [ ] T042 Confirm `deploy/crswd.example.service` is **unchanged** by this feature — it stays hardened, and PR #137's guard against inline `CRSW_` assignments still passes

---

## Dependencies

```
Phase 1 (T001–T002)
   └─> Phase 2 (T003–T004)
          ├─> Phase 3 / US1 (T005–T017)   ← independent of US2
          └─> Phase 4 / US2 (T018–T031)   ← independent of US1
                 └─> Phase 5 / US3 (T032–T038)   ← documents US2's mechanism
                        └─> Phase 6 (T039–T042)
```

- **US1 and US2 are fully independent** and can be built in either order or at once.
- **US3 depends on US2** only: it documents the drop-in US2 creates.
- Within US1, T014–T017 depend on T011; T015–T016 are the R2 pair and must land together, since T014 alone leaves existing deployments exposed.

## Parallel Execution Examples

**Phase 3 tests** — four files, no shared state:

```
T005, T006, T007, T008   # config-side, all parallel
T009, T010               # tmux-tagged, same file, sequential
```

**Phase 4** — the reporting surface splits cleanly:

```
T029 (settings page)  ∥  T030 (journal)   # different files, shared vocabulary from T028
```

**Phase 5** — documentation is almost entirely parallel:

```
T032, T033, T037, T038   # different files
```

## Implementation Strategy

**MVP is User Story 1 alone.** It closes a live credential exposure and is the only
phase that must ship soon. US2 and US3 are maintenance and operator-experience work that
can follow without leaving anything exposed.

Recommended order: **Phase 1 → 2 → 3 (ship) → 4 → 5 → 6.**

Two things not to defer inside US1:

1. **T015 and T016 ship with T014 or the fix is cosmetic.** The chokepoint alone corrects
   only servers started fresh; the reference host's server holds 20 `CRSW_` variables
   including the shared secret, and would keep them for the server's life.
2. **T036's limitation is documented in the same change**, not later. Sessions running at
   upgrade stay exposed until recreated, and a host that is quietly still leaking while
   being reported as fixed is the failure this whole feature exists to end.
