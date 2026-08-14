# Tasks: Adopting a hand-written unit

**Branch**: `010-adopt-the-unit` | **Spec**: [spec.md](spec.md) | **Plan**: [plan.md](plan.md)

**Definition of done for every task**: `go build ./... && go vet ./... && go test ./...`
passes, plus `golangci-lint run`.

## Phase 1 — Reading a unit

- [x] **T001** `internal/updater/unitfile.go`: parse a systemd unit into
      `[Section] → key → last value`. Small, and only what this needs: comments,
      blank lines, a commented directive is *absent*, last assignment wins.
- [x] **T002** The hardening vocabulary: the four settings the drop-in expresses,
      their systemd defaults, and which value counts as relaxed. Held to
      `deploy/crswd.service.d/10-relax.conf.example` by a test, so the drop-in and
      the checker cannot come to disagree about what it grants.
- [x] **T003** Tests: a commented-out directive reads as its default; the last of
      two assignments wins; a value in another section is not read as `[Service]`.

## Phase 2 — Planning an adoption

- [x] **T004** `internal/updater/adopt.go`: `PlanAdoption` — the standing, the
      waiting unit, the diff, and the three refusals.
- [x] **T005** FR-010: refuse unless the waiting unit's `ExecStart` binary exists
      and is executable.
- [x] **T006** FR-011: refuse on any relaxation the drop-in cannot express.
- [x] **T007** FR-012: refuse on an environment assignment whose loss would change
      the loaded configuration; accept the ones that would not.
- [x] **T008** FR-009: an existing drop-in is read and checked, never overwritten.
- [x] **T009** Tests, and the refusals are the point: each one alone, each one
      naming what it refused on, and a plan that refuses proposing no writes.

## Phase 3 — Performing it

- [x] **T010** `Adopt`: drop-in, backup, unit, record — in that order, and nothing
      at all when the plan refuses (FR-014).
- [x] **T011** Tests: the unit ends byte-identical to the waiting one; the record
      is what `install.sh` would have written; the backup holds the old unit; a
      refused plan leaves the host untouched.
- [x] **T012** The property test: effective hardening is identical before and
      after, computed from the merged unit + drop-in rather than from file text.

## Phase 4 — The commands

- [x] **T013** `cmd/crswd/unit_cmd.go`: `crswd unit check` and `crswd unit adopt`,
      in `config_cmd.go`'s shape.
- [x] **T014** `cmd/crswd/main.go`: the subcommand and its line in the usage.
- [x] **T015** FR-008: adoption prints the two commands that put it into effect,
      and where the backup is.
- [x] **T016** Tests: the report on an adoptable host, on a managed one, on one
      with nothing waiting, and on each refusal.

## Phase 5 — Telling the operator

- [x] **T017** FR-018: the startup banner names the command, and only where
      adoption would be granted.
- [x] **T018** Tests: named when adoptable, absent when not.
- [x] **T019 [P]** `README.md` and `deploy/README.md`: what it does, what it
      refuses, and that it grants nothing.

## Phase 6 — Close out

- [x] **T020** Full suite, `-tags tmux`, `-tags dev`, `golangci-lint run`,
      `go vet -tags quickstart` — all clean.

      Also run end to end against a copy of the deployed host's real unit,
      configuration and waiting offer: it refused on all three genuine blockers,
      each refusal was actionable, and following them made the host adoptable.
      The resulting hardening was checked against **systemd's own** reading of the
      running service (`systemctl --user show`), which reports
      NoNewPrivileges=no, RestrictSUIDSGID=no, ProtectKernelTunables=no,
      ProtectSystem=no, ProtectControlGroups=yes — exactly what the generated
      drop-in reproduces.
- [x] **T021** `docs/fixes-log.md`: one line.
- [ ] **T022** PR against `main`.
