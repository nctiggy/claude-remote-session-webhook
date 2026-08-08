# Implementation Plan

> Worked top-to-bottom by `ralph/loop.sh` — one task per iteration.
>
> **This is milestone 6, the last of the backlog.** Milestones 1 through 5 are complete,
> reviewed, and deployed; their task lists are archived under [`archive/`](archive/) and the
> notebook at [`archive/progress-milestones-1-5.md`](archive/progress-milestones-1-5.md).

## Status: generated from the spec

Generated from [`specs/006-ship-it-to-someone-else/tasks.md`](../specs/006-ship-it-to-someone-else/tasks.md),
which is the single source of truth. `spec.md`, `plan.md`, `research.md`, `data-model.md` and
the six files in `contracts/` supersede anything this file summarises.

**Before starting a task, read its matching `T0NN` entry in `tasks.md`.**

## ✅ THE KEY HAS ARRIVED — nothing in this milestone waits on a human any more

**The operator generated the pair and completed all three steps of the handover** (commit
`c606df3`, before Iteration 15): the private half is the `RELEASE_SIGNING_KEY` repository
secret, and the public half is committed to **both** `internal/updater/release_key.txt` and
`install.sh`'s `RELEASE_KEYS` block. `TestInstallerCarriesTheCommittedKeys` holds them together.

**Still true, forever: do not generate a key. Do not commit one. Do not put an "example" key in
a fixture** — an example key that happens to be valid is a real key in the repository. Tests that
need a signature generate an ephemeral pair in the process and never write it down; that is what
`install_test.go` and `TestReleaseIsSigned` do. Rotation is the operator's too, and additive:
commit the new public line to both files **first**, then replace the secret. The signing step
refuses the other order rather than publishing a release that verifies nowhere.

**This heading has cost two iterations more than the tasks under it did.** Iteration 15 found
T018 was never blocked by it. Worse, the key landed in `c606df3` **before Iteration 15 ran**, and
that iteration's notebook still recorded the file as holding no key line — a fact it stated
rather than checked. **A summary does not block a task; `tasks.md`, the contract, and the tree
do.** Before believing a heading, look at the thing it describes:
`git log -1 -- internal/updater/release_key.txt` settles this one in a second.

## ⚠️ Nothing about the installer can be proven on this host

The project is installed here, a config exists, `~/.local/bin` is on `PATH`, and the unit is in
place. **Every precondition the installer exists to create is already true**, so a green run
here demonstrates nothing.

T012 moves that verification to a GitHub-hosted runner with a fresh `HOME`, running the
published release, twice. It is **not optional polish** — without it, T009–T011 are proven only
in the one environment where they cannot fail.

Where a task carries *"it was only ever run where the project is already installed"*, that
phrasing is the point.

## 🔒 Four tasks are security-critical, in this order of risk

| Task | Why |
|---|---|
| **T015** verify | Checksum **then** signature, and a missing signature is a refusal, not a skip. Get this wrong and signing is decorative. |
| **T016** stage | Nothing executable before both checks pass; staging swept at startup. |
| **T017** swap | Smoke test before the rename; any failure leaves the daemon on what it was running. |
| **T013** keygen | Writes nothing to disk. The operator holds the private half. |

## What is already running

Milestones 1 through 5 are **live**:

| | |
|---|---|
| Service | `crswd.service`, systemd user unit, loopback `127.0.0.1:8765` |
| Public | `https://crswd.craigcloud.io` via the `crswd` Cloudflare Tunnel |
| Daemon | Config file, settings page, mode toggle, PRG, themed picker, dependency probe |
| Audit | `journalctl --user -u crswd -o cat \| grep '^{' \| jq .` |
| CI | `go test`, `-tags tmux`, `-tags quickstart`, all on self-hosted runners (#87) |

**There is no release, no version, and no installer.** That is this milestone.

## Resolved decisions

Settled in [`research.md`](../specs/006-ship-it-to-someone-else/research.md). **Do not
re-litigate these** — if one looks wrong, write it in `PROGRESS.md` under
`NEEDS CLARIFICATION` and stop.

| Question | Decision | Consequence |
|---|---|---|
| How does the version reach the binary? | One variable, `internal/buildinfo.Version`, default `"dev"`, stamped with `-ldflags -X` | A package rather than `main`, because httpapi must read the same string. **The default is the sentinel**: forgetting to stamp cannot make a working tree claim to be a release |
| Where does the build number come from? | `git rev-list --count HEAD` | Monotonic and **derived from the repository itself**. `github.run_number` resets when a workflow is recreated, which would make an older release outrank a newer one and break both "is this newer" and retention |
| What are the asset names? | `crswd_<version>_linux_<arch>.tar.gz`, plus `SHA256SUMS` and `SHA256SUMS.sig` | Built in YAML, shell and Go, which cannot share a constant. The duplication is unavoidable; the drift is not, so a test asserts they agree. A drift is a 404 while somebody is installing |
| Who holds the signing key? | **The operator.** `crswd keygen` prints both halves and writes nothing | A key written to a file is a key that gets committed by accident. The public half is **committed, not fetched** — a key retrieved from the host that serves the release is the same factor twice |
| How does rotation work? | Additive: one key per line, any may verify, retire the old only once every retained release is signed by the new | A rotation that deletes first strands every release an operator might roll back to |
| How does a daemon replace its own binary? | Stage → checksum → signature → chmod → **smoke test** → atomic rename → exit for systemd | The smoke test is the non-obvious step: a checksum proves the bytes are the published bytes and says nothing about whether they *run here*. An arm64 build on amd64 passes every cryptographic check and then fails to exec — after it is installed |
| What does "staged" mean? | `~/.local/share/crswd/staging/`, mode `0600` until verified, swept at startup | Nothing is executable before both checks pass. Swept because the process that vouched for those bytes did not live to say so |
| How does the installer know a unit was edited? | It records the hash of what it wrote; **no record means leave it alone** | The third case is every host deployed before this milestone, including the operator's own |
| Why no automatic rollback? | A daemon that cannot start cannot decide to replace itself | It would need a supervisor this project does not have. The previous binary is kept and the failure is made loud instead |

## Conventions

- `- [ ]` open · `- [x]` done · `- [!]` blocked (reason in `PROGRESS.md`)
- Priority order is meaningful — the loop always takes the topmost open item.
- Each task must be independently completable **and verifiable** in one iteration.
- **Every task ends green**: `go build ./... && go vet ./... && go test ./... && golangci-lint run`.
  Add `-tags tmux` when the task touches tmux and `-tags quickstart` when it touches
  `cmd/crswd`. Tests ship inside the task that implements the behaviour — never as a separate
  failing test, which step 6 of `PROMPT.md` would make the iteration revert.
- **Check the linter is v2 before trusting it.** A pre-v2 binary reads this repo's v2 config,
  runs zero linters, and exits 0 — a green that means nothing. The session-start hook warns
  when this is the case; believe it (#26).
- `go.sum` must never appear. An import needs justification under `docs/security.md` §5 first.
- **AR-005: a test satisfies the cross-site checks, it never disables them.**
- **AR-008: no refactoring outside the task**, however obvious the improvement.
- **A task is not done when the code exists. It is done when something calls it.** This repo
  has shipped that failure four times — a reaper with no caller, `Store.Touch` with no caller,
  a PR-opener no workflow invoked, and `CRSW_DESTROY_ON_SHUTDOWN`, which was false on every
  daemon that ever ran. **T005 is where that bites**: a config key with no reader is exactly
  that fourth one again.

---

## Tasks

### US1 — Know what is running (everything depends on this)

- [x] T001 `internal/buildinfo.Version`, default `"dev"` — the default is the sentinel
- [x] T002 `--version` on the command line, honest about unreleased builds
- [x] T003 `GET /dashboard/version`, reading the same variable

### US2 — Releases exist

- [x] T004 The release workflow: `v0.<count>`, both architectures, `CGO_ENABLED=0`
- [x] T005 Attach the deployment files, not just the binary
- [x] T006 `SHA256SUMS` covering every asset
- [x] T007 Retention: keep 20, never the newest two, never what `latest` resolves to
- [x] T008 `Restart=always` in the unit — self-update depends on it

### US3 — Install in one line (all of it about another machine)

- [x] T009 Detect, download, and verify **before** anything is executable
- [x] T010 Place the binary, the unit, the recorded hash, and a config only if absent
- [x] T011 Refuse to clobber: an edited unit, and one we have no record of
- [ ] T012 The `verify-install` job on a GitHub-hosted runner — **not optional**, and
      **unblocked at Iteration 16**: T014 signs, so the release this job installs from carries a
      `SHA256SUMS.sig` made in the same run that publishes it. **This is the topmost open task.**

### The signing key — ✅ THE HUMAN STEP IS DONE

- [x] T013 🔒 `crswd keygen` and a `release_key.txt` the operator has since filled in — the
      private half is the `RELEASE_SIGNING_KEY` secret, the public half is committed to that
      file **and** to `install.sh`. See Iteration 14 for the build, `c606df3` for the key.
- [x] T014 Sign `SHA256SUMS` in CI, and refuse to publish a release that cannot be signed or
      that would be signed by a key nothing carries. See Iteration 16.

### US4 — Update without rebuilding (cannot start before US1)

- [ ] T015 🔒 Verify: checksum **then** signature; a missing signature refuses
- [ ] T016 🔒 Stage: `0600` until verified, swept at startup
- [ ] T017 🔒 Swap: **smoke-test the staged binary**, then rename, then exit
- [x] T018 Fetch: TLS, no cross-host redirect, exact asset name — **the one task in US4
      that never needed the key**; see Iteration 15
- [ ] T019 🔒 `POST /dashboard/update` via `handleAction`, with a confirming step

### US5 — The rain says something (independent)

- [x] T020 Messages drawn on the canvas, never inserted into the DOM

### Ship it

- [x] T021 README leads with the one-liner; document rolling back

---

## Shippable at T008

**US1 and US2 together are the core.** A daemon that can say what it is, and a release someone
could download by hand. The installer and self-update make it pleasant; releases make it
possible at all.

T020 and T021 were what there was to take while T013/T014 waited on the operator. T018 followed,
because fetching is the one step of US4 that carries no verification and so never needed a key.
T014 came next, once the key turned out to have been committed three iterations earlier.

**Nothing is blocked now.** The graph's one human edge — T013 → T014 → {T012, T015…} — is
satisfied, and every remaining task is ordinary work: **T012**, then **T015 → T016 → T017 → T019**
in that order. T012 is the topmost, and it is the only one of them that can fail for reasons
this host cannot show you.

---

## Out of scope

Deliberately NOT in milestone 6:

- **Automatic updating.** This makes an update possible and verifiable. A daemon that updates
  itself on a schedule, unasked, is a larger decision
- **Auto-recovery of a crashed session** (#95). It collides with the rule that refuses to
  resume where "the last conversation in this directory" could be another session's, and the
  operator asked to think about it further
- **Editing settings from the browser.** No mutating verb is registered on `/settings` at all
- **Windows, and any package manager** — apt, brew, nix. A tarball and an install script
- **Multi-user support**, the device-code login relay, and the companion skill
- **Any change to milestone 1's signing procedure, its six operations, or the audit record
  shape.** The update path is new; the API beside it is not
