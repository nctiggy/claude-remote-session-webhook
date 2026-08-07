# Progress

> Append-only notebook. Newest at the bottom. Never edit or delete past entries —
> this is the loop's only memory across fresh contexts.

Each iteration appends:

```
## Iteration N — YYYY-MM-DD HH:MM
**Did:** one or two lines.
**Learned:** anything that would otherwise be rediscovered the hard way.
**Left:** what remains.
**Findings:** problems noticed but not fixed (ad-hoc bugs, smells, risks).
```

Findings are the point of this file as much as progress is. An observation that
dies in a context window is a bug you will pay for twice. Real ad-hoc fixes also
get a one-liner in `docs/fixes-log.md`.

When the whole plan is done and green, append a line containing exactly
`RALPH_COMPLETE` — `loop.sh` stops on it.

---

---

## Iteration 0 — milestone 6 begins

**Did:** Archived milestones 1–5 and started a fresh notebook.

**Learned:** The sentinel trap that was live at the start of milestones 4 and 5 is now
**handled by code rather than by memory.** `loop.sh` refuses to start when `PROGRESS.md`
already contains `RALPH_COMPLETE`, and says what to do about it. Both previous milestones
caught it by hand, and the second time it recurred *despite* Iteration 0 of the first having
written the warning down — because that note lives in the file that gets archived, so the
reminder was archived along with the trap. A guard in `loop.sh` cannot be archived. Nothing to
remember here any more.

**Left:** T001–T021, all open. **T013 is BLOCKED-ON-HUMAN and the loop must not finish it.**

**Findings:**

1. **T013 stops.** It implements `crswd keygen` and commits an *empty* `release_key.txt`, then
   halts. Do not generate a key. Do not commit one. Do not put an "example" key in a fixture —
   an example key that happens to be valid is a real key in the repository. Mark it `- [!]`,
   write the handover, and move to the next open task. T014 and everything after it wait on
   the operator adding `RELEASE_SIGNING_KEY` and committing the public line.
2. **T020 and T021 are the work to take while T013/T014 are blocked**, not idling.
3. **Nothing about the installer can be proven on this host.** The project is installed here, a
   config exists, `~/.local/bin` is on `PATH`, the unit is in place — every precondition the
   installer creates is already true, so a green run here demonstrates nothing. T012 moves that
   verification to a GitHub-hosted runner with a fresh `HOME`. It is not optional polish.
4. **US4 cannot start before US1.** T017 execs the staged binary and requires `--version` to
   print the expected string; without T002 there is nothing to ask.

---

## Iteration 1 — 2026-08-07 22:36

**Did:** T001. Added `internal/buildinfo` with the single exported `var Version = "dev"` and
`TestDefaultVersionIsDev`. Gate green: build, vet, `go test ./...`, `golangci-lint run` (0 issues).

**Learned:**

- **The linter here is 2.12.2 — the v2 the config expects.** Verified before trusting the green,
  per the plan's warning (#26). No need to re-check unless the toolchain moves.
- **`go test -ldflags "-X …/internal/buildinfo.Version=v0.42" ./internal/buildinfo` is how you
  prove `TestDefaultVersionIsDev` can fail** without editing the file under test. It reports
  `v0.42, want "dev"`. Keep that trick for T002's `TestStampedVersionIsReported`, which needs the
  same ldflags path but asserts the opposite — and note the exact flag string is now proven to
  reach the variable, so a T002 failure there is T002's own bug, not a wrong symbol path.
- The test is `package buildinfo_test` (external) deliberately: the two real readers, `cmd/crswd`
  and `internal/httpapi`, see the package from outside too.
- `.golangci.yml` enables only bodyclose, errcheck, gosec, govet, staticcheck, ineffassign,
  unused, with `check-blank: true` on errcheck — so `_ = f()` is a lint failure, not a shortcut.
  No `gochecknoglobals`, which is why the exported `var` passes.

**Left:** T002–T021. T002 (`--version`, `-tags quickstart`) and T003 (`GET /dashboard/version`)
are both unblocked now and can go in either order — the plan lists T003 as parallel with T002.
T013 remains BLOCKED-ON-HUMAN; T014 and after wait on it.

**Findings:**

1. **T003's `TestFlagAndRouteAgree` cannot be written as a pure unit test in
   `internal/httpapi`.** The flag lives in `cmd/crswd`, which `internal/httpapi` must not import
   (and the reverse import is the real direction). The honest form is to assert the route reports
   `buildinfo.Version` — i.e. that the route holds no copy of its own — and leave the flag half
   to T002's quickstart test, which already execs a real binary. Whoever takes T003 should write
   it that way rather than inventing a cross-package import to satisfy the test's name.
2. **No ad-hoc defects observed.** The tree was clean going in and `go.sum` is still absent.

---

## Iteration 2 — 2026-08-07 22:42

**Did:** T002. `--version` on `cmd/crswd`, printing `crswd v0.42` stamped and
`crswd dev (not a release)` unstamped, returning before the daemon starts. Acceptance tests in
`cmd/crswd/version_test.go` (`-tags quickstart`). Gate green: build, vet, `go test ./...`,
`golangci-lint run` (2.12.2, 0 issues), **and the whole `-tags quickstart ./cmd/crswd` suite, 30s**.

**Learned:**

- **`main_test.go`'s `TestDiagnosticsGoToStderr` is the gate any new stdout print has to pass**,
  and it is easy to miss until it fails. It walks every non-test `.go` file in the module and
  rejects `os.Stdout` anywhere except `internal/audit/audit.go` and *as an argument to a named
  call* — it was `runConfigCommand` only. `--version` had to be added to that list
  (`theVersionReport = "printVersion"`), and the `exempt < 2` floor raised to 3. Keep the
  call-named form: it is deliberate, so that an ordinary `fmt.Println` added to `main.go` later
  still fails. **T003 will not hit this** — it writes to a `ResponseWriter`, not a stream.
- **Both proofs were run, and the wrong-symbol one is worth repeating.** Changing the ldflags
  path to `…buildinfo.version` (lower-case `v`) builds and runs clean and prints
  `crswd dev (not a release)`; only `TestStampedVersionIsReported` catches it. That is the exact
  silent failure T002 exists for, and it is what T004 will get wrong if it retypes the symbol.
  The proven-good string is now in one place: `theStampedSymbol` in `version_test.go`. **T004
  should stamp with that literal rather than a fresh one typed into YAML.**
- **`unset` is the quickstart suite's sentinel for "remove this variable".** `h.runBinary(bin,
  map[string]string{"CRSW_SHARED_SECRET": unset}, "--version")` gives a process the daemon
  cannot start in, which is what turns exit 0 into evidence that `--version` answered *instead*
  of the daemon. Asserting the whole `CombinedOutput` — not `strings.Contains` — is what makes
  "nothing else printed" checkable.
- `h.buildStamped(version)` is now beside `h.buildDev()` (`quickstart_dashboard_test.go`) as the
  second variant-build helper. T017's smoke test will want the same shape.
- `say(w, format, …)` in `config_cmd.go` is the package's existing write helper and already
  carries the `//nolint:errcheck`. Do not add a second one.

**Left:** T003–T021. T003 (`GET /dashboard/version`) is the next open task and is unblocked.
T013 remains BLOCKED-ON-HUMAN; T014 and after wait on it.

**Findings:**

1. **`AGENTS.md`'s quickstart row is stale and scarier than the truth.** It says the suite needs
   `127.0.0.1:8765` free because "two startup cases bind that exact port". They do not any more —
   `freeAddrOn` (`quickstart_test.go:438`) asks the kernel for a free port under the deliberately
   non-loopback *host spelling*, which is what those two cases are actually about. The full suite
   passed here with the deployed daemon holding 8765 the whole time. Not fixed: it is a doc line
   outside this task (AR-008), and it is a one-liner for the fix lane.
2. **Iteration 1's finding about `TestFlagAndRouteAgree` still stands and now has a second half.**
   The flag half is covered: `version_test.go` execs a real binary and reads the line. T003 should
   assert the route reports `buildinfo.Version` — that it holds no copy of its own — rather than
   inventing a `cmd/crswd` import that `internal/httpapi` must not have.
3. **No ad-hoc defects observed** in the code touched.

---

## Iteration 3 — 2026-08-07 22:51

**Did:** T003. `GET /dashboard/version` in `internal/httpapi/version.go`, on `handleBrowser` under
the new `audit.ActionDashboardVersion` (`dashboard.version`), answering
`{"version":"…"}` from `buildinfo.Version` read inside the handler. Tests in
`version_test.go`. Gate green: build, vet, `go test ./...`, `golangci-lint run` (2.12.2, 0 issues),
plus `-tags quickstart ./cmd/crswd` (30s) and `-tags dev ./...`, since the route changes
`newServer`, which both of those build.

**Learned:**

- **The route answers JSON, not a page, and the contract is the reason.**
  `contracts/version.md`'s **Files** line names `internal/httpapi/version.go` and no template;
  `contracts/settings-page.md` named both its handler *and* `web/templates/settings.html`. So the
  omission is a decision, not a gap — that comparison is the cheapest way to settle "page or
  payload?" for any future route here. It also kept `docs/design-system.md` out of this task
  entirely. The body reuses `s.writeJSON` (`sessions.go:750`).
- **`versionResponse` has one field on purpose.** The contract's table says the route reports the
  version "plus the latest available"; that second half needs the fetch **T018** builds, so it is
  US4's and not a gap in T003. Adding it later is a field on the struct, not a change of shape —
  which is why the answer is an object rather than a bare string. **Whoever takes T018/T019 should
  add `latest` here rather than inventing a second route.**
- **Both mutations were run, and the construction-copy one is the subtle one.**
  `var ownCopy = buildinfo.Version` at package scope compiles, keeps the import, and is exactly
  the "route has its own copy" defect — `TestFlagAndRouteAgree` catches it *only* because it
  changes the variable **after** `newFleet` and asks a second time. A test that stamped before
  building the server would pass against it. The second mutation, `s.mux.Handle(patternVersion,
  http.HandlerFunc(s.version))`, is answered 200 with the version to a caller carrying no
  assertion at all, and emits zero audit records.
- **A new browser route costs three edits outside its own file**, and two of them fail loudly if
  forgotten: `registeredPatterns` and the driven-request table in `settings_test.go`
  (`TestFullRouteSweepLeaksNoSecret` errors with "is registered on this daemon and nothing above
  drove it"), plus the action-name table in `internal/audit/audit_test.go`. Budget for them.
- The audit action is `dashboard.version` — named for its door, because there is no second way to
  ask a *running* daemon this and the command-line reader emits no record at all.

**Left:** T004–T021. **US1 is complete**; T004 (the release workflow) is next and unblocked.
T004 must stamp with the literal `theStampedSymbol` from `cmd/crswd/version_test.go` rather than a
fresh ldflags path typed into YAML — Iteration 2 proved a mistyped symbol fails silently.
T013 remains BLOCKED-ON-HUMAN; T014 and after wait on it.

**Findings:**

1. **`internal/audit/audit_test.go`'s action table is not the exhaustive list its comment claims.**
   The comment says listing *every* action makes two constants sharing a spelling a compile error,
   but `audit.ActionSessionMode` (`session.mode`, milestone 5) was never added — so a new action
   colliding with it would compile. `dashboard.version` is in the table; `session.mode` still is
   not. Not fixed: it is one line in a file outside this task (AR-008), and it is a fix-lane
   one-liner.
2. **Iteration 2's finding about `AGENTS.md`'s quickstart row still stands** — the `127.0.0.1:8765`
   warning is stale. The suite passed again here with the deployed daemon holding that port.
3. **No ad-hoc defects observed** in the code touched.

---

## Iteration 4 — 2026-08-07 23:05

**Did:** T004. `.github/workflows/release.yml` — push to `main` builds `linux/amd64` and
`linux/arm64` with `CGO_ENABLED=0`, stamps `buildinfo.Version` with `v0.$(git rev-list --count
HEAD)`, and publishes `crswd_<version>_linux_<arch>.tar.gz` with `gh release create`. Four tests
in `internal/release/assets_test.go`. Gate green: build, vet, `go test ./...`, `golangci-lint run`
(2.12.2, 0 issues), `gofmt -l` empty.

**Learned:**

- **`internal/release` has no non-test file and does not need one.** `go build ./...`,
  `go vet ./...` and `golangci-lint run` are all happy with a directory holding only
  `package release_test`. Confirmed rather than assumed, because it decides whether T009's
  `install_test.go` needs a production file invented for it to sit beside. **It does not** — and
  a Go constant for the asset name should wait for T018, the first thing with a real reason to
  build that string in Go, rather than being created here for a test to call. (`internal/release`
  is a *test* package: nothing ships from it.)
- **The linter does read that package.** Verified by removing the `//nolint:errcheck` on
  `f.Close()` and watching errcheck fire. A test-only package silently skipped by the gate would
  have been worth knowing about.
- **`debug/elf` rather than `ldd`, and the reason generalises to T017's smoke test.** `ldd` cannot
  read the cross-compiled arm64 artifact on an amd64 host — the artifact most likely to be wrong —
  while `PT_INTERP` plus `ImportedLibraries()` is exactly what `ldd` reports on and is stdlib.
  **Both were mutated and both fire**: with `CGO_ENABLED=1` the amd64 build gains a dynamic loader
  and `libc.so.6`, and the arm64 build fails outright in the assembler (`gcc_arm64.S: no such
  instruction`), which is the same fact from the other end — the cross-compile only works because
  cgo is off.
- **All five workflow mutations were run and each fails with the right message**: tag-only trigger,
  `fetch-depth: 1`, `buildinfo.version` (lower-case v), `for arch in amd64` alone, and
  `crswd-${VERSION}-linux-` renaming. The wrong-symbol one is Iteration 2's silent failure, now
  caught in the file where it would actually be typed: `TestWorkflowStampsTheSymbolTheBinaryReads`
  reads `theStampedSymbol` out of `cmd/crswd/version_test.go` **as text**, because it is an
  unexported constant in another package's test binary and copying it would be the drift itself.
  **T014 adds `-ldflags` nothing, but T005–T007 all edit this file: run `go test ./internal/release`
  after any edit to it.**
- **`go build -o dist/$arch/crswd` creates the missing parent directories**, so the workflow's
  `mkdir -p dist` is enough. Checked, because a `no such file or directory` here would only ever
  appear in CI.
- **`go test ./...` now cross-compiles the daemon twice** (≈6s amd64, ≈9s arm64, in parallel).
  That is new cost on every untagged run, including CI's.
- **actionlint could not be run here** — it is not installed and the sandbox refused to fetch it.
  The workflow's shell was written to the pattern `claude-issue.yml` already proves passes
  actionlint's shellcheck pass (step-level `env:`, every expansion quoted). **CI's guardrails job
  is the first real check of it.**
- Runner choice is deliberate and documented in the file: GitHub-hosted, *unlike* `ci.yml`. The
  self-hosted runners are the operator's own machines, which also run unsandboxed sessions
  (`docs/github-automation.md` §2), and these bytes are what other people execute. #77's reason for
  leaving GitHub-hosted runners was blocked merges; a slow release blocks nothing.

**Left:** T005–T021. T005 (attach the deployment assets) is next and unblocked; it edits
`release.yml`'s publish step and wants `TestReleaseCarriesEveryAsset` beside the four tests already
in `internal/release/assets_test.go`. T013 remains BLOCKED-ON-HUMAN; T014 and after wait on it.

**Findings:**

1. **`plan.md` says `release.yml` is "tag-triggered"; `tasks.md` and `contracts/release.md` say
   merge-to-main, and the contract has a test whose whole point is that tag-only is wrong.**
   Built to `tasks.md`, which `IMPLEMENTATION_PLAN.md` names the single source of truth. Not
   fixed: it is one line in a superseded artifact, outside this task (AR-008). Worth a fix-lane
   line if anyone touches `plan.md`.
2. **`tasks.md` still had T003 unticked** while `IMPLEMENTATION_PLAN.md` had it ticked — Iteration
   3's `docs(ralph)` commit touched only the plan. Ticked here along with T004, because a
   completed task left open in the file the plan calls authoritative is what makes a fresh context
   redo it.
3. **Iteration 3's finding about `internal/audit/audit_test.go`'s action table still stands** —
   `audit.ActionSessionMode` is still absent from the list its comment calls exhaustive.
4. **Iteration 2's finding about `AGENTS.md`'s quickstart row still stands.**
5. **No ad-hoc defects observed** in the code touched.

---

## Iteration 5 — 2026-08-07 23:10

**Did:** T005. A `Stage the deployment files` step in `release.yml` copies
`deploy/crswd.example.service` → `dist/crswd.service`, plus `cloudflared.example.yml` and
`crswd-api`, and `gh release create` uploads all three beside the two tarballs.
`TestReleaseCarriesEveryAsset` in `internal/release/assets_test.go`. Gate green: build, vet,
`go test ./...`, `golangci-lint run` (2.12.2, 0 issues), `gofmt -l` empty.

**Learned:**

- **The asset set is asserted in *both* directions, and that is a deliberate handover to T006
  and T014.** `TestReleaseCarriesEveryAsset` fails on an upload it does not recognise as loudly
  as on one that went missing, and the message names those two tasks: **whoever adds
  `SHA256SUMS` (T006) or `SHA256SUMS.sig` (T014) to the workflow must add the name to the
  `want` set in that test, or the run goes red.** That is the point — "every asset" is a claim
  about the whole list and stops being true the moment the list grows behind the test's back.
  The seven-name list in `contracts/release.md` is the authority; five of the seven exist now.
- **`uploadedAssets` reads only the `gh release create` command, not the file**, matching a
  backslash-continued run of lines and stopping at the first that does not continue. **This was
  proven against a fake `verify-install` job appended below `Publish`** — the shape T012 adds —
  and the test still passes. Reading to end-of-file would have made T005 and T012 collide.
  Assets are recognised as *quoted arguments containing a slash*, so `--generate-notes` and
  `"$VERSION"` are skipped, and an asset uploaded straight from `deploy/` (no rename) is caught
  by name rather than silently missed.
- **The unit is renamed on the way out and the test pins the mapping**, `crswd.service` ←
  `deploy/crswd.example.service`. It is published under the name it is installed as; an asset
  called `crswd.example.service` invites carrying the word "example" into
  `~/.config/systemd/user`. **T009/T010's installer should ask the release for `crswd.service`.**
- **All four mutations were run and each fails with the right message**: the three deployment
  files dropped from the upload list; the unit uploaded from `deploy/` without the rename (fires
  in *both* directions at once — missing `crswd.service`, unexpected `crswd.example.service`);
  `touch dist/crswd.service` in place of the copy, i.e. the right name over an empty file; and
  a copy from `deploy/crswd.service`, the path that would exist if someone renamed the example
  — caught both as the wrong source and as a file absent from the working tree, which is a
  failure that would otherwise appear only in CI *after* a successful build.
- **No tagged suite was needed.** Nothing here is behind a build tag: `internal/release` is a
  test-only package in the default build, and although `cmd/crswd`'s quickstart suite does read
  `deploy/crswd.example.service` (`quickstart_test.go:1687`), this task did not modify that file.
  **T008 does modify it** — `Restart=always` — so T008 must run `-tags quickstart ./cmd/crswd`.

**Left:** T006–T021. T006 (`SHA256SUMS` over every asset) is next and unblocked; it edits the
same publish step and **must add `SHA256SUMS` to `want` in `TestReleaseCarriesEveryAsset`**.
Run `go test ./internal/release` after any edit to `release.yml`. T013 remains
BLOCKED-ON-HUMAN; T014 and after wait on it.

**Findings:**

1. **A release asset carries no file mode, so `crswd-api` arrives non-executable.** It is `0755`
   in the repository and `deploy/README.md` installs it with `install -m 0755`; a GitHub release
   asset is just bytes, so an operator who downloads it directly gets a file they must `chmod`.
   Not fixed and not a defect in T005 — the fix belongs wherever the download is documented
   (**T021's README**), and shipping it inside a tarball instead would change the asset list
   `contracts/release.md` fixes. Worth one line in the rollback/install prose.
2. **Iteration 4's finding about `plan.md`'s "tag-triggered" line still stands.**
3. **Iteration 3's finding about `internal/audit/audit_test.go`'s action table still stands.**
4. **Iteration 2's finding about `AGENTS.md`'s quickstart row still stands.**
5. **No ad-hoc defects observed** in the code touched.
