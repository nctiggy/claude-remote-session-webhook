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

---

## Iteration 6 — 2026-08-07 23:19

**Did:** T006. A `Checksum every asset` step between `Stage` and `Publish` in `release.yml`
writes `dist/SHA256SUMS` over every regular file in `dist/`, and `gh release create` uploads it
as the sixth asset. `TestEveryAssetHasAChecksum` in `internal/release/assets_test.go`, plus
`SHA256SUMS` added to `TestReleaseCarriesEveryAsset`'s `want` via a new `generated` list. Gate
green: build, vet, `go test ./...`, `golangci-lint run` (2.12.2, 0 issues), `gofmt -l` empty.

**Learned:**

- **The step is *replayed*, not pattern-matched, and `stepScript` is the reusable part.** It
  pulls a named step's `run:` block out of the YAML and dedents it, so the test builds a fake
  `dist/`, runs the real shell under `bash -e` (GitHub's own flags for a step naming no shell),
  and reads the file that comes out. A regex over `sha256sum …` would have agreed with anything
  that looked about right. **T007's retention and T012's install job can both use `stepScript`.**
- **`sha256sum -c SHA256SUMS` is run at the end, in the fake `dist/`** — the exact command
  `quickstart.md` promises an operator. It is what caught the self-referential-file mutation
  with `SHA256SUMS: FAILED`, and it pins the two-space `<64 hex>  <name>` format for free, which
  **T015's Go verifier must parse**.
- **The list is derived from `dist/`, never typed.** `find . -maxdepth 1 -type f -printf '%P\n'`
  after `cd dist`. Three consequences worth not rediscovering: `-maxdepth 1 -type f` is what
  excludes `dist/amd64/` and `dist/arm64/`, the tarballs' *input*; `%P` after `cd` is what makes
  the names bare, which `sha256sum -c` needs because it runs where `dist/` does not exist; and
  the sums go into a variable **before** the redirect creates the file, or `SHA256SUMS` appears
  in its own list holding the checksum of the empty file it was a moment earlier.
- **Step order is asserted separately, because the replay cannot see it.** A checksum step above
  `Stage the deployment files` sums exactly the two tarballs and every other assertion still
  passes — the contract's named failure, reached without anything going red.
- **All six mutations were run and each fails with the right message**: `sha256sum crswd_*.tar.gz`
  (names the three missing deployment files); `-maxdepth 2` (names `amd64/crswd` as a path);
  redirect instead of deferred write (fires twice — an unpublished name, and `sha256sum -c`
  reporting `SHA256SUMS: FAILED`); no `cd dist`, so every name carries `dist/`; the step moved
  above `Stage`; and `dist/SHA256SUMS` computed but left out of the upload.
- **`SHA256SUMS.sig` is skipped by prefix, not by name** (`strings.HasPrefix(name, "SHA256SUMS")`),
  so **T014 needs no change to `TestEveryAssetHasAChecksum`** — a signature over the sums file
  cannot be inside it. T014 does need `"SHA256SUMS.sig"` appended to the new `generated` list, or
  `TestReleaseCarriesEveryAsset` goes red on the unexpected upload.
- **gosec fires on ordinary test scaffolding.** `os.MkdirAll(…, 0o755)` is G301 and
  `os.WriteFile(…, 0o644)` is G306 even inside `t.TempDir()`; `0o750`/`0o600` satisfy both with
  no `//nolint`. Only the `os.ReadFile` of a computed path (G304) needed one.
- **actionlint still cannot be run here** (not installed; the sandbox refused to fetch it, and
  approval for `shellcheck` on a heredoc was refused too). The step's shell follows the pattern
  the rest of the file already uses. **`ci.yml`'s guardrails job runs actionlint 1.7.7 over every
  workflow (`ci.yml:88`), which shellchecks `run:` bodies — that is the first real check of it.**
- **No tagged suite was needed.** Nothing here is behind a build tag, and neither
  `deploy/crswd.example.service` nor `cmd/crswd` was touched. **T008 still must run
  `-tags quickstart ./cmd/crswd`** — it edits the unit file that `quickstart_test.go:1687` reads.

**Left:** T007–T021. T007 (retention) is next and unblocked; it edits the same workflow, so run
`go test ./internal/release` after any edit to `release.yml`, and `stepScript` is already there
to replay whatever step it adds. T013 remains BLOCKED-ON-HUMAN; T014 and after wait on it.

**Findings:**

1. **`data-model.md` says `SHA256SUMS` "covers every asset above", and the list above it includes
   `SHA256SUMS.sig`.** Taken literally that is impossible — the signature is made *from* the sums
   file, so it cannot be inside it. Built to the only consistent reading: the two tarballs and the
   three deployment files, five of the seven names. Not fixed, because it is one line in a
   superseded artifact and outside this task (AR-008), but **T015's verifier must expect exactly
   this**, and `contracts/release.md`'s wording ("`SHA256SUMS` covers every asset") has the same
   ambiguity. Worth a fix-lane line if anyone touches either.
2. **Iteration 5's finding about `crswd-api` arriving non-executable still stands** — and T006
   does not change it: a checksum records bytes, not a file mode, so `sha256sum -c` will pass on
   a `crswd-api` the operator still has to `chmod`. Still T021's README.
3. **Iteration 4's finding about `plan.md`'s "tag-triggered" line still stands.**
4. **Iteration 3's finding about `internal/audit/audit_test.go`'s action table still stands.**
5. **Iteration 2's finding about `AGENTS.md`'s quickstart row still stands.**
6. **No ad-hoc defects observed** in the code touched.

---

## Iteration 7 — 2026-08-07 23:30

**Did:** T007. A `Prune old releases` step after `Publish` in `release.yml`: keep `KEEP: "20"`,
ranked by the number in the tag, never the two most recent whatever `KEEP` says, never what the
`latest` pointer resolves to, never a tag this workflow did not publish.
`TestRetentionKeepsTwentyAndNeverTheNewestTwo` in `internal/release/assets_test.go` — six
scenarios, table-driven. Gate green: build, vet, `go test ./...`, `golangci-lint run` (2.12.2,
0 issues), `gofmt -l` empty.

**Learned:**

- **`gh` is stubbed as a bash *function* prepended to the replayed script, not as a file on
  `PATH`.** The step calls `gh` as a plain command, so a function of that name shadows it — no
  temp bin directory, and no executable to create, which matters because `os.WriteFile` at
  `0o700` is gosec G306 and `os.Chmod` to `0700` is G302. **T012's install job can use the same
  trick** if it needs to stand in for a command.
- **The stub answers and decides nothing, deliberately.** It emulates two reads —
  `gh release view --json tagName --jq '.tagName'` (one tag, the pointer target; an error and
  nothing on stdout when there is no release) and `gh release list --json tagName --jq
  '.[].tagName'` (one tag per line) — and records `gh release delete <tag>` to a file. Filtering
  inside the stub, e.g. by putting the latest-exclusion in the `--jq` filter, would move the rule
  under test into the stub, and a step that had stopped applying it would still pass. **Anything
  the step must decide has to be in the step's own shell for the replay to be worth running.**
- **`KEEP` is step-level `env:` so the floor is testable at all.** With the limit at 20, "never
  the two most recent" is unreachable — the test lowers `KEEP` to 0 and asserts the newest two
  survive. `stepScript` returns only the `run:` body, so **the replay has to supply any
  step-level env itself** (`replay.Env = append(os.Environ(), "KEEP="+…)`); the test separately
  reads `KEEP: "20"` out of the YAML so the declared limit is still pinned.
- **Ranking is by `sort -t. -k2,2nr`, not by date and not as text.** Fixtures are handed to the
  stub *ascending*, the order `gh release list` is least likely to answer in, so a step that
  keeps the first twenty it is given fails. Text sorting puts `v0.9` above `v0.30`; the 30-tag
  scenario is sized to expose exactly that.
- **All seven mutations were run and each fails with the right message**: the pointer guard
  removed (`the step deleted v0.4`); `sort -r` instead of numeric; no sort at all; the `-le 2`
  floor removed; `KEEP: "5"`; the `grep -E '^v0\.[0-9]+$'` filter removed (deletes `nightly` and
  `v1.0.0`); and the step moved above `Publish`.
- **Order is asserted separately, because a replay cannot see it.** Pruning before publishing
  prunes to twenty and then makes it twenty-one, and a publish that fails afterwards has already
  cost a release. Every behavioural assertion still passes in that arrangement.
- **`gh release delete` is called without `--cleanup-tag`** — the tag is the only remaining
  record of which commit a pruned version named.
- **Scripted mutation testing was not possible here**: `python3 <file>` needed approval and was
  refused, so each mutation was applied with the Edit tool and reverted the same way. `git diff
  --stat` showing additions only is the check that the tree came back.
- **`shellcheck` is installed at `~/.local/bin/shellcheck` but running it was refused too**, and
  actionlint is still absent. `KEEP` is referenced the same way the build step already references
  `VERSION` — step-level `env:`, uppercase, every expansion quoted — and that step passes CI's
  actionlint job today. **`ci.yml`'s guardrails job (`ci.yml:88`) is still the first real check.**
- **No tagged suite was needed.** `internal/release` is test-only in the default build, and
  neither `cmd/crswd` nor `deploy/crswd.example.service` was touched. **T008 does touch the unit
  file and must run `-tags quickstart ./cmd/crswd`** (`quickstart_test.go:1687` reads it).

**Left:** T008–T021. T008 (`Restart=always` in `deploy/crswd.example.service`) is next and
unblocked; it is the last of US2 and the milestone is shippable after it. T013 remains
BLOCKED-ON-HUMAN; T014 and after wait on it.

**Findings:**

1. **Retention is bounded by what `gh release list --limit 1000` returns.** With one release per
   merge and a prune every run the list stays near 20, so the limit is unreachable in practice;
   a backlog beyond 1000 would silently prune only the newest 1000's tail. Not worth code today,
   but it is the kind of cap that is invisible when it bites — **worth a line if anyone changes
   the release cadence.**
2. **Iteration 6's finding about `data-model.md`/`contracts/release.md` and `SHA256SUMS.sig`
   still stands** — T015's verifier must expect the sums file to cover five names, not seven.
3. **Iteration 5's finding about `crswd-api` arriving non-executable still stands** (T021's README).
4. **Iteration 4's finding about `plan.md`'s "tag-triggered" line still stands.**
5. **Iteration 3's finding about `internal/audit/audit_test.go`'s action table still stands.**
6. **Iteration 2's finding about `AGENTS.md`'s quickstart row still stands.**
7. **No ad-hoc defects observed** in the code touched.

---

## Iteration 8 — 2026-08-07 23:35

**Did:** T008, the last of US2. `deploy/crswd.example.service` now says `Restart=always` where it
said `Restart=on-failure`, under a comment naming self-update's step 7 as the reason.
`TestUnitRestartsAlways` in `internal/release/assets_test.go` reads the directive back out of
`[Service]` and requires that comment. Gate green: build, vet, `go test ./...`,
**`go test -tags quickstart ./cmd/crswd` (31.5s, real)**, `golangci-lint run` (2.12.2, 0 issues),
`gofmt -l` empty.

**Learned:**

- **`on-failure` is the wrong value *silently*, which is why the test says so by name.** Step 7
  of `contracts/self-update.md` is `exit 0` — deliberate, after the rename. `on-failure` treats
  a clean exit as success and does nothing, so the update completes, every check passes, and the
  host is left with the new binary and no daemon running it. There is no error anywhere.
- **`RestartSec=5s` was already there and is untouched**, so a restart loop is still bounded.
  `Restart=always` does **not** make the unit unstoppable — an explicit `systemctl --user stop`
  still stops it; the directive governs the daemon ending on its own. That objection is the one
  a reviewer raises, so it is answered in the file's comment rather than left to be rediscovered.
- **The test reads the unit through `deployed["crswd.service"]`, not a second path constant.**
  That map already pins `deploy/crswd.example.service` for `TestReleaseCarriesEveryAsset`, so a
  rename in `deploy/` now fails in one place instead of two. **T009–T012 should do the same** —
  `install.sh` needs the same file and the same name-it-once rule applies.
- **Comment attribution is by contiguous block, and a blank line ends it.** The parser keeps the
  comment lines immediately above a directive and clears them on any blank line or section
  header, so prose elsewhere in the file cannot stand in for a missing reason. That is a real
  mutation, not a hypothetical — inserting one blank line reds the test.
- **`RestartSec=` is excluded by the `=`, not by a special case.** `strings.HasPrefix(trimmed,
  "Restart=")` is the whole discrimination; `HasPrefix(…, "Restart")` would have matched both.
- **Section tracking matters.** `Restart=` is only a restart policy inside `[Service]`; moved to
  `[Unit]` systemd ignores it with a warning nobody reads. The test scopes its search, so that
  move reports as "sets no `Restart=` in `[Service]`" rather than passing.
- **All six mutations were run and each fails with the right message**: `on-failure`; the
  directive deleted; the comment deleted; a second `Restart=on-failure` added lower in
  `[Service]` (systemd takes the last); the directive moved into `[Unit]`; and one blank line
  inserted between the comment and the directive.
- **A `//nolint:gosec // G304` on the `os.ReadFile` turned out to be unnecessary and was
  removed** — gosec does not fire on a path joined from package-level constants here, unlike the
  `os.MkdirAll`/`os.WriteFile` modes iteration 6 hit. **Check before adding one**; an unused
  directive is noise the linter does not flag.
- **The quickstart suite ran for real, not just `go vet -tags quickstart`.** `tmux`, `jq` and a
  free `127.0.0.1:8765` were all available on this host, so `quickstart_test.go:1687`'s read of
  the unit file was genuinely exercised against the edit. Checking that with
  `systemctl --user is-active crswd` was refused by the sandbox; running the suite was not.
  **Try the suite before assuming the environment is unavailable.**

**Left:** T009–T021. **US2 is complete and the milestone is shippable here** — a daemon that can
say what it is (T001–T003) and a release with every asset, checksummed and pruned (T004–T008).
T009 (`install.sh`: detect, download, verify before anything is executable) is next and
unblocked; it starts US3, all four tasks of which are about another machine. `stepScript` and the
`gh`-as-a-bash-function stub in `assets_test.go` are both there for T012's install job. T013
remains BLOCKED-ON-HUMAN; T014 and after wait on it.

**Findings:**

1. **No prose anywhere restates the restart policy**, so this edit stranded no documentation —
   checked, not assumed (`grep -rn "on-failure\|Restart=" --include=*.md .` finds only the plan
   and this notebook). `deploy/README.md` mentions restarts twice but only about `KillMode` and
   session survival, both still true. **Nothing here for T021's README pass.**
2. **Iteration 7's finding about `gh release list --limit 1000` still stands** — invisible until
   the release cadence changes.
3. **Iteration 6's finding about `data-model.md`/`contracts/release.md` and `SHA256SUMS.sig`
   still stands** — T015's verifier must expect the sums file to cover five names, not seven.
4. **Iteration 5's finding about `crswd-api` arriving non-executable still stands** (T021's README).
5. **Iteration 4's finding about `plan.md`'s "tag-triggered" line still stands.**
6. **Iteration 3's finding about `internal/audit/audit_test.go`'s action table still stands.**
7. **Iteration 2's finding about `AGENTS.md`'s quickstart row still stands.**
8. **No ad-hoc defects observed** in the code touched.

---

## Iteration 9 — 2026-08-07 23:55

**Did:** T009, the start of US3. `install.sh` at the repository root: detect linux/amd64 or
linux/arm64 and refuse anything else by name, resolve the version from the `latest` redirect,
download the tarball, `SHA256SUMS` and `SHA256SUMS.sig`, verify the signature and then the
checksum, and only then unpack. Five tests in `internal/release/install_test.go`
(`TestAssetNamesAgreeAcrossLanguages`, `TestInstallerNamesNobody`,
`TestInstallVerifiesBeforeExecutable` with five cases, `TestInstallRefusesUnknownPlatform`,
`TestInstallerCarriesTheCommittedKeys`). Gate green: build, vet, `go test ./...`,
`golangci-lint run` (2.12.2, 0 issues), `gofmt -l` empty, `go.sum` still absent.

**Learned:**

- **The whole script is replayed, not a step of it** — `bash -c stubs+"\n"+install.sh`, with
  `curl`, `uname`, `openssl`, `sha256sum`, `tar`, `chmod` and `install` defined as bash
  functions ahead of it. That is iteration 7's `ghStub` trick applied to a file rather than a
  `run:` block, and two things make it work: prepending before the `#!` line is harmless, and
  **`command -v` reports a shell function**, so `require_tools` is satisfied by the stubs even
  where the real tool is absent. Only `curl` and `uname` answer for themselves; the rest wrap
  the real tool with `command` and log that they ran, because a stub standing in for `openssl`
  or `tar` would be the test agreeing with itself.
- **"Verified before anything is executable" is an ordering claim, so the test reads an event
  log rather than the filesystem.** `tar` is in the same list as `chmod` and `install`, and
  that is the non-obvious part: **tar restores the mode stored in the archive**, so unpacking
  is itself the moment inert bytes become a file the host will run. The invariant asserted is
  that no event in {tar, chmod, install} precedes both {openssl, sha256sum} — which is also
  what **T010 and T011 have to keep true as they add the placement steps**. Checking the
  filesystem instead would have proven nothing: the script's `trap … EXIT` removes its own
  workdir, so "nothing executable is left" is true even of an installer that made one.
- **openssl is on this host and the signature path really runs.** A raw ed25519 public key is
  wrapped for `openssl pkeyutl -verify -pubin -rawin` by prepending the constant 12-byte SPKI
  header, whose base64 is `MCowBQYDK2VwAyEA` — 12 divides by three, so it concatenates with
  the key's base64 with no re-encoding. The test derives that prefix from
  `x509.MarshalPKIXPublicKey` rather than typing it, because **a wrong prefix fails exactly
  like a wrong key**: every release refuses, and nothing says why. **T013's `keygen` must
  print the public half as base64 of the raw 32 bytes** for this to keep working, which is
  also what `ed25519.PublicKey` marshals to.
- **The installer carries its own copy of the key list and it is empty**, matching what T013 is
  told to do with `internal/updater/release_key.txt`. It cannot read that file — it is fetched
  on its own with no checkout — so the lines are written twice.
  `TestInstallerCarriesTheCommittedKeys` **skips today and starts asserting the moment T013
  creates the file**, which is the drift it exists for. **The operator's step is now three
  places, not two: `RELEASE_SIGNING_KEY`, `release_key.txt`, and the `RELEASE_KEYS` heredoc in
  `install.sh`.**
- **`sha256sum -c SHA256SUMS` is the wrong command here and it fails against a *correct*
  release.** The file covers every asset and the installer downloads one of them, so the other
  four are missing files. What it does instead is `grep -Fqx "$(sha256sum "$tarball")"` — the
  published line matched whole, which cannot be satisfied by a substring and needs no parsing.
  The fixture carries the four undownloaded names deliberately, so this mutation is caught.
- **The version comes from the `latest` redirect, not the API.** `curl -o /dev/null -w
  '%{url_effective}'` on `/releases/latest` answers `…/releases/tag/v0.42`; the API would work
  too and is rate-limited by address, so an office behind one address installs a few times and
  then cannot. `${url##*/}` is also why the version can never contain a `/`, which is what
  makes it safe in the asset name and the `-o` path.
- **All eight mutations were run and each fails with the right message**: unpack moved above
  the checks; an empty key list treated as "nothing to verify against"; a missing
  `SHA256SUMS.sig` downloaded with `|| true` and skipped; `sha256sum -c` over the whole file;
  one character changed in the SPKI prefix (fires twice — the prefix assertion *and* every
  signature); `crswd-${version}-linux-…` renaming; a `/home/<user>` path in `mktemp`; and an
  unknown architecture falling through to amd64. **Note that `fetch … || true` does not mutate
  anything** — `die` runs `exit`, which `||` does not catch — so the missing-signature
  mutation has to bypass `fetch` entirely to be a real one.
- **`errcheck` has `check-blank: true`, so `raw, _ := os.ReadFile(…)` is a lint failure** even
  in a test, even beside a `//nolint:gosec`. `if raw, err := os.ReadFile(log); err == nil` is
  shorter anyway and drops the `os.Stat` in front of it.
- **No tagged suite was needed.** Nothing here is behind a build tag: `internal/release` is
  test-only in the default build, and neither `cmd/crswd` nor `deploy/` was touched.
- **`shellcheck` and `chmod` were both refused by the sandbox**, as in iterations 6 and 7. The
  script's syntax is exercised for real — the tests run the whole file under `bash` eight
  times — but nothing has *linted* it. **`install.sh` is therefore committed mode 0644**:
  `chmod` and `git update-index --chmod=+x` were both refused, and the documented use is
  `curl … | bash`, which does not need the bit. **Whoever can run `chmod` should set it**, or
  `./install.sh` from a clone fails for a reason that has nothing to do with the script.

**Left:** T010–T021. T010 (place the binary, the unit, the recorded hash, and a config only if
absent) is next and unblocked; it extends `install.sh` where `main` currently ends, and
`runInstaller` in `install_test.go` already sets `HOME` to a directory the test owns. T013
remains BLOCKED-ON-HUMAN; T014 and after wait on it.

**Findings:**

1. **The installer refuses every release until the operator commits a key, and that is
   deliberate — but it means T012 cannot go green yet.** T012 runs the installer on a fresh
   runner against **the published release**, and no published release carries `SHA256SUMS.sig`
   until T014, which waits on T013's human step. The refusal is correct in both directions
   (spec FR-025, and "an unsigned release is a release nobody can install"), so **T012 should
   be taken after the operator's step rather than before it**, or it lands as a job that fails
   on every merge to `main`. Not a defect in T009 — the same is true of T015's verifier, which
   embeds the same empty file.
2. **`quickstart.md`'s installer check contradicts `contracts/installer.md`.** Line 56 runs
   `grep -iE 'nctiggy\|/home/[a-z]' install.sh` and expects **no matches**, but the contract
   says plainly that "the repository owner appears in the URL it fetches from, which is
   unavoidable and fine", and there is nowhere else to download a release from. Built to the
   contract, and `TestInstallerNamesNobody` encodes exactly that reading: the account name only
   on a line containing `https://`, no `/home`, `/Users` or `/root` path, no address. The
   quickstart line as written can only pass on an installer that cannot work. Not fixed — it is
   one line in a superseded artifact and outside this task (AR-008) — but **T021 should correct
   it while documenting the one-liner**, because it is the check a human would run by hand.
3. **CI lints no shell outside `.claude/hooks/`, `ralph/loop.sh`, `.claude/statusline.sh` and
   `.github/scripts/`.** `ci.yml`'s `Shell syntax` and `shellcheck` steps both name those
   paths explicitly, so `install.sh` — the one shell script this project asks strangers to pipe
   into bash — is checked by nothing but the tests written for it. Adding it to both lists is a
   two-word change to `ci.yml`, outside this task's named files. **Worth doing in the fix lane,
   or by T012 while it is already editing a workflow.**
4. **Iteration 7's finding about `gh release list --limit 1000` still stands.**
5. **Iteration 6's finding about `data-model.md`/`contracts/release.md` and `SHA256SUMS.sig`
   still stands** — T015's verifier must expect the sums file to cover five names, not seven.
6. **Iteration 5's finding about `crswd-api` arriving non-executable still stands** (T021's
   README). `install.sh` does not touch it: T009 unpacks the binary and nothing else.
7. **Iteration 4's finding about `plan.md`'s "tag-triggered" line still stands.**
8. **Iteration 3's finding about `internal/audit/audit_test.go`'s action table still stands.**
9. **Iteration 2's finding about `AGENTS.md`'s quickstart row still stands.**
10. **No ad-hoc defects observed** in the code touched.

---

## Iteration 10 — 2026-08-08 00:08

**Did:** T010, the placement half of the installer. `install.sh` now downloads the published
`crswd.service` beside the tarball and checksums it too, then places the binary at
`~/.local/bin/crswd` (0755), the unit at `~/.config/systemd/user/crswd.service` (0644), the
hash of that unit at `~/.local/share/crswd/crswd.service.sha256`, and a starter configuration
at `~/.config/crswd/config` (0600) **only when there is none** — and prints what is left
without enabling or starting anything. Four tests in `internal/release/install_test.go`:
`TestInstallPlacesWhatItDownloaded`, `TestConfigModeIs0600` (two cases),
`TestInstallPrintsNextSteps`, and a sixth case in T009's `TestInstallVerifiesBeforeExecutable`
for a unit that does not match its checksum. Gate green: build, vet, `go test ./...`,
`golangci-lint run` (2.12.2, 0 issues), `gofmt -l` empty, `go.sum` still absent.

**Learned:**

- **The release publishes no `config.example`, so "write the config from the example" can only
  mean an example this script carries.** `contracts/release.md` fixes the asset list at seven
  names and `config.example` is not one of them; the tarball holds `crswd` and nothing else;
  there is no `crswd config example` subcommand to print one. Fetching it from `raw.
  githubusercontent.com` would be pulling unverified content onto a host mid-install, which is
  the one thing this installer exists to refuse. So the starter is a heredoc in `install.sh`:
  `version = 1` and every other setting commented out, which is also what `docs/security.md`
  §3 says keeps a copy of the example from being a file that holds a secret. **It is a fourth
  copy of configuration prose and it will drift from `config.example`** — it names only
  `shared_secret` and `allowed_roots` and links to the full file to keep that surface small.
- **`install(1)`, not `cp`, and that is not style.** It unlinks the destination before writing,
  so a re-install over the binary of a daemon that happens to be running produces a new file
  under the old name instead of `ETXTBSY`. It was already in T009's stub list; it is now also
  in `require_tools`.
- **The umask and the `chmod` are both there on purpose.** `umask 077` around the heredoc is
  what closes the window — a file written first and chmod'd second is world-readable for the
  length of the write, and that file is where the shared secret goes. The `chmod 0600` after it
  states the mode rather than leaving it as a consequence of arithmetic three lines up. The
  mutation that removes both is caught; either one alone still passes, which is correct.
- **`fakeRelease` now writes the deployment files, not just their checksums.** That is what
  makes the installer's `crswd.service` fetch fail if the release workflow ever renames the
  asset: the fixture builds its directory from `deployed`, so the name is checked by the
  release actually carrying it rather than by a second string comparison. The two files it
  never fetches still have no copy in the download directory, so the `sha256sum -c SHA256SUMS`
  mutation is still caught.
- **`runInstaller` drops every `XDG_*` variable from the environment it hands bash.** The
  daemon reads `$XDG_CONFIG_HOME` ahead of `~/.config`; `install.sh` does not, deliberately,
  because `contracts/installer.md` fixes the literal paths. On the day somebody teaches the
  installer the daemon's rule, an inherited `XDG_CONFIG_HOME` would send these tests' writes
  into the home of whoever ran the suite. Dropped now so that cannot arrive as a surprise.
- **`runInstaller` takes optional `seed` functions** that run against the temporary `$HOME`
  before the installer does — that is how "a host that already has a configuration" is set up,
  and T011 needs the same hook for "a host that already has a unit".
- **All eight mutations were run and each fails with the right message**: config inheriting the
  umask (0644); config written unconditionally; `next_steps` removed; `systemctl --user enable
  --now crswd` actually run; `record_unit` removed; the binary placed 0644; the unit never
  placed; the unit fetched and never checksummed; and placement moved in front of
  `verify_signature`, which trips T009's ordering assertion 25 times over.
- **`bash -n`, `shellcheck`, `perl -pi` and writing outside the repo were all refused by the
  sandbox.** The mutation harness had to live in `.ralph-tmp/` (gitignored, removed after) and
  each mutation applied with the Edit tool and reverted from a saved copy. The script's syntax
  is exercised for real — the suite runs the whole file under `bash` a dozen times — but
  **nothing has linted it**, as in iterations 6, 7 and 9.
- **No tagged suite was needed.** Nothing here is behind a build tag; `internal/release` is
  test-only in the default build and neither `cmd/crswd` nor `deploy/` was touched.

**Left:** T011–T021. **T011 (refuse to clobber) is next and unblocked** — it reads the record
this task writes, `~/.local/share/crswd/crswd.service.sha256`, which holds the bare lowercase
hex digest and no filename; `write_config`'s if-absent branch is the shape its unit comparison
wants, and `runInstaller`'s new `seed` parameter is how it plants an edited unit and a record
that does or does not exist. T013 remains BLOCKED-ON-HUMAN; T014 and after wait on it.

**Findings:**

1. **⚠️ The unit this installer places cannot start the binary this installer places.**
   `deploy/crswd.example.service`, published as the `crswd.service` asset, has
   `ExecStart=%h/bin/crswd` — `~/bin/crswd`, not `~/.local/bin/crswd`, which is the path
   `contracts/installer.md` step 4 fixes and this task implements. A fresh install therefore
   ends with a unit pointing at a file nothing wrote, and the operator's first
   `systemctl --user enable --now crswd` fails with 203/EXEC. **This is not something T010
   could fix in scope** — the contract fixes the installer's path, and changing `ExecStart`
   edits a file that is live on this operator's own host — but it makes the printed next step
   a step that does not work. **T012's `verify-install` job will catch it on a fresh runner if
   it does more than check the file landed**, and T021 documents the one-liner. Worth a fix-lane
   PR before either.
2. **⚠️ Same shape, second instance: the unit's `EnvironmentFile=%h/.config/crswd/env` is
   required, and the installer writes `~/.config/crswd/config`.** No `-` prefix, so systemd
   fails the unit outright when that file is absent — which it is on every fresh install. The
   unit also sets `Environment=CRSW_ALLOWED_ROOTS=%h/code` inline, and the environment beats
   the configuration file, so `allowed_roots` set where the installer says to set it is
   **overridden by the unit that installer just wrote**. The next-steps text is built to
   `contracts/installer.md`'s worked example, which is the authority; the two files disagree
   about where configuration lives and **only one of them can be right**. Belongs with
   finding 1, in the same fix.
3. **The if-absent branch of `write_config` is exercised, but "a second whole run leaves the
   file byte-identical" is still T011's `TestInstallNeverOverwritesConfig`.** What is asserted
   here is one run against a seeded home, which is the same predicate reached a shorter way.
4. **Iteration 9's finding that T012 cannot go green before the operator's key still stands**,
   and now matters more: T012 is the only thing that would catch findings 1 and 2.
5. **Iteration 9's finding about `quickstart.md`'s `grep -iE 'nctiggy|/home/[a-z]'` check still
   stands** (T021).
6. **Iteration 9's finding about CI linting no shell outside `.claude/hooks/`, `ralph/loop.sh`,
   `.claude/statusline.sh` and `.github/scripts/` still stands** — `install.sh` has now doubled
   in size and is still linted by nothing.
7. **Iteration 7's finding about `gh release list --limit 1000` still stands.**
8. **Iteration 6's finding about `data-model.md`/`contracts/release.md` and `SHA256SUMS.sig`
   still stands** — T015's verifier must expect the sums file to cover five names, not seven.
9. **Iteration 5's finding about `crswd-api` arriving non-executable still stands** (T021's
   README). `install.sh` still does not touch it, and does not touch
   `cloudflared.example.yml` either: `contracts/installer.md`'s eight steps name neither.
10. **Iteration 4's finding about `plan.md`'s "tag-triggered" line still stands.**
11. **Iteration 3's finding about `internal/audit/audit_test.go`'s action table still stands.**
12. **Iteration 2's finding about `AGENTS.md`'s quickstart row still stands.**

---

## Iteration 11 — 2026-08-08 00:18

**Did:** T011, the clobber refusal, finishing every part of US3 that can be done off a
GitHub-hosted runner. `place_unit` in `install.sh` compares the installed unit against the
hash `record_unit` wrote: matching → replace, differing → `has been modified — leaving it
alone`, no record → `was not written by this installer — leaving it alone`, and **nothing is
recorded on either refusal**. Three tests in `internal/release/install_test.go`:
`TestInstallNeverOverwritesConfig`, `TestInstallNeverOverwritesEditedUnit` (two cases),
`TestInstallLeavesNoRecordAlone`. Gate green: build, vet, `go test ./...`, `golangci-lint run`
(2.12.2, 0 issues), `gofmt -l` empty, `go.sum` still absent.

**Learned:**

- **Every question in this task needs the installer run *twice against one home*, which the
  harness could not do**, so `runInstaller` now delegates to `runInstallerIn(t, home, …)` and
  `twice(t, between, seed…)` sits on top: install, let the caller be an operator (or a later
  release), install again. **The event log had to move out of `$HOME`** — with both runs
  appending to `$HOME/events`, run 2 reads as run 1 having done everything twice, and every
  ordering assertion in `TestInstallVerifiesBeforeExecutable` would have been evaluated against
  a doubled log. It is now `t.TempDir()/events`, one per run.
- **A test that reads the filesystem *after* `twice` returns cannot see anything the second run
  changed**, because both runs share a home and the read happens once, at the end. That is not
  hypothetical: the record-unchanged assertion was written as `placed(first)` vs `placed(second)`
  and it **passed against an installer that called `record_unit` on the leave path** — the exact
  defect it was written for, comparing two reads of one file. Anything that must not change
  **has to be read inside `between`**. Any later test using `twice` inherits this trap.
- **The edited-unit case cannot tell the two possible comparisons apart**, and that is why the
  second subtest exists. Comparing against the freshly downloaded `crswd.service` leaves an
  edited unit alone exactly as comparing against the record does; the two differ only when the
  release publishes a *new* unit onto a host that never touched the old one — where the shipped
  copy reading refuses forever and no host ever receives a corrected unit. Hence
  `release.republish(t)`, which re-sums and re-signs whatever the release directory now holds:
  a test can change an asset and still be a *release* rather than a tampered download.
  `fakeRelease` now ends with it instead of building the sums inline.
- **Six mutations were run and five failed first time; the sixth is the one above.** The list:
  no record read as permission (the `-f` guard folded into the outer `if`); comparison against
  the shipped `$UNIT_ASSET`; the unit placed unconditionally, i.e. T010's own behaviour; the
  leave path calling `record_unit`; `write_config`'s if-absent branch removed; and the `say` on
  the modified branch deleted. `cp install.sh .ralph-tmp/install.sh.good` and restoring from it
  is the harness — `git checkout` cannot be used to revert a mutation while the task's own work
  is uncommitted.
- **A record that cannot be read is treated as no record, not as a mismatch.** `[ -f ] && [ -r ]`
  before the `cat`, because under `set -eu` a failing `cat` in a command substitution ends the
  script with a message about `cat` rather than about the unit — and "we cannot show we wrote
  this" is exactly the third row anyway.
- **The record is compared as a whole string against `${current%% *}`.** Any malformed record —
  empty, truncated, carrying a filename the way `sha256sum -c` lines do — compares unequal and
  therefore leaves the unit alone, which is the safe direction by construction rather than by a
  special case.
- **No tagged suite was needed.** Nothing here is behind a build tag; `internal/release` is
  test-only in the default build, and neither `cmd/crswd` nor `deploy/` was touched.
  `install.sh` has no Go reader outside `internal/release`.

**Left:** T012–T021. **T012 is next in the plan but cannot go green yet** — see finding 1; it
runs against the published release, which carries no `SHA256SUMS.sig` until T014, which waits on
T013's human step. **T020 and T021 are the work to take meanwhile**, per the plan's own
instruction. T013 remains BLOCKED-ON-HUMAN.

**Findings:**

1. **T012 still cannot go green before the operator's key, and it is now the only unproven part
   of US3.** T009–T011 are all implemented and all of them are proven only here, where every
   precondition the installer creates is already true. The three cases most worth a fresh runner
   are the two below, which no test in the working tree can see.
2. **⚠️ Iteration 10's findings 1 and 2 still stand and T011 has made the first one reachable
   twice over.** `deploy/crswd.example.service` — published as the `crswd.service` asset — has
   `ExecStart=%h/bin/crswd` and a required `EnvironmentFile=%h/.config/crswd/env`, while the
   installer places the binary at `~/.local/bin/crswd` and the configuration at
   `~/.config/crswd/config`. A fresh install still ends with a unit that cannot start (203/EXEC,
   or a missing `EnvironmentFile`). **What is new: now that a unit this installer wrote is
   replaced when the release publishes a different one, the fix genuinely reaches installed
   hosts** — one PR to `deploy/crswd.example.service` and every host that has not edited its
   unit takes it on the next run. Before this task, no host would ever have received it. Still
   out of scope here (AR-008, and the file is live on the operator's own host), still worth a
   fix-lane PR before T012 or T021.
3. **Nothing tests the case where the unit is absent but a record is present** — an operator who
   deleted the unit and re-ran the line. The code places and re-records, which is right, but the
   contract's table has three rows and says nothing about that fourth state, so no test asserts
   it. Worth one case if anyone extends `TestInstallNeverOverwritesEditedUnit`.
4. **Iteration 9's finding about `quickstart.md`'s `grep -iE 'nctiggy|/home/[a-z]'` check still
   stands** (T021).
5. **Iteration 9's finding about CI linting no shell outside `.claude/hooks/`, `ralph/loop.sh`,
   `.claude/statusline.sh` and `.github/scripts/` still stands.** `install.sh` grew again here
   and is still linted by nothing — `shellcheck` was refused by the sandbox for the third
   iteration running. Its syntax is exercised for real (the suite runs the whole file under bash
   more than twenty times) but no linter has read it. **T012 edits `ci.yml`'s neighbour and is
   the cheapest place to add it to both lists.**
6. **Iteration 7's finding about `gh release list --limit 1000` still stands.**
7. **Iteration 6's finding about `data-model.md`/`contracts/release.md` and `SHA256SUMS.sig`
   still stands** — T015's verifier must expect the sums file to cover five names, not seven.
8. **Iteration 5's finding about `crswd-api` arriving non-executable still stands** (T021's
   README).
9. **Iteration 4's finding about `plan.md`'s "tag-triggered" line still stands.**
10. **Iteration 3's finding about `internal/audit/audit_test.go`'s action table still stands.**
11. **Iteration 2's finding about `AGENTS.md`'s quickstart row still stands.**
12. **No ad-hoc defects observed** in the code touched.

---

## Iteration 12 — 2026-08-08 00:31

**Did:** Marked **T012 blocked** (`- [!]` in both the plan and `tasks.md`) and took **T020**
instead, which is the plan's own instruction for while T013/T014 wait on a human. The rain now
occasionally says something: five strings and two constants beside the rain in
`web/static/crswd.js`, drawn by `saying(field)` from inside `paint` and nowhere else. Four
tests in `internal/httpapi/partials_test.go` — `TestRainCanvasIsAriaHidden`,
`TestNoMessageInRenderedMarkup`, `TestNothingRunsUnderReducedMotion`,
`TestMessagesAreNotServerSupplied`. Gate green: build, vet, `go test ./...`, `golangci-lint run`
(2.12.2, 0 issues), `gofmt -l` empty, **`-tags quickstart ./cmd/crswd` (31s, real)**, `go.sum`
still absent.

**Learned:**

- **T012 is blocked and it is verifiable in one line, so stop re-deriving it.**
  `install.sh:151` — `[ -n "$found" ] || die "this installer carries no release key…"` — and
  `release_keys()` is an empty heredoc. **The installer refuses every release today,
  unconditionally.** T012 installs from *the published release* on a fresh runner, so the job
  would fail on every merge to `main` until the operator's key exists and T014 signs. Iterations
  9, 10 and 11 each rediscovered this from scratch; it is now written into the plan and
  `tasks.md` beside the task so a fresh context reads it before opening `install.sh`.
- **T020's own instruction to "add `aria-hidden="true"` to the canvas in `header.html`" was
  already satisfied and not by that file.** The canvas lives in `partials/rain.html`
  (`header.html` only does `{{ template "rain" }}`) and has carried the attribute since
  milestone 1, asserted by `TestTheRainCarriesNoInformationAndStaysOffReadingContent`. So
  `TestRainCanvasIsAriaHidden` was written to claim something the existing test does not: every
  `<canvas>` on every *page*, not the partial in isolation. It catches a page that renders a
  second canvas of its own. **A task naming a file is not always naming the file the thing is
  in — check before editing the one it names.**
- **Go cannot execute this file, so FR-032 is a structural claim, and the region boundary is
  the whole trick.** `TestNothingRunsUnderReducedMotion` slices `crswd.js` from `const GLYPHS =`
  to `const watch = (pane) =>` — the rain's half — asserts both markers exist, then requires
  every occurrence of `MESSAGES`, `SAYING_FRAMES`, `SAYING_ODDS`, `saying` and `saidFor` to fall
  inside it. That is what catches a message path added anywhere else, **including one added as a
  listener rather than a timer**, which a `setTimeout`/`setInterval` blocklist would have missed
  entirely. The timer blocklist is still there for the in-region case.
- **Mutation 4 had to be run twice and the first attempt is the lesson.** A `setInterval` message
  path inserted just above `const watch` is *inside* the slice, so it was caught by the timer
  check rather than by the confinement check — the right answer for the wrong reason. Re-run with
  the mutation in the file's last IIFE, the confinement check fired on both `MESSAGES` reads.
  **When mutating to test a region boundary, check which side of it the mutation landed on.**
- **All seven mutations were run and each fails with the right message**: `aria-hidden` removed
  from the canvas (fires on all four pages and on both canvases of the two that have two); a
  `<p>Wake Up</p>` in `rain.html` (fires in both message tests, and the **case-insensitive**
  match is what caught the title-cased copy); `saying(field)` moved from `paint` into `tick`;
  a message path outside the rain; the `still.matches` guard removed from `start()`;
  `MESSAGES` emptied (both sweeps *fatal* rather than passing vacuously — that guard is the
  point); and a `rainSaying` constant added to `internal/httpapi/dashboard.go`.
- **A Go mutation has to go after the imports.** `const rainSaying = …` placed above the `import`
  block is `syntax error: imports must appear before other declarations` — a build failure, which
  is not a mutation and proves nothing about the test.
- **`os.ReadFile` over a name from `os.ReadDir(".")` is gosec G304** and needs a `//nolint`, unlike
  iteration 8's read of a path joined from constants. Iteration 6 hit the `MkdirAll`/`WriteFile`
  mode rules; this is the third distinct gosec rule this package's tests have tripped.
- **The messages must stay distinctive strings.** Both sweeps are substring matches over whole
  rendered pages and over every Go file in the package, so a message like "running" or "sessions"
  would fail against correct code. The five chosen are unmistakably decoration, which is also
  what the contract asks for on its own grounds.
- **`-tags quickstart` was run even though no route changed**, because `web/` is embedded into
  the real binary the suite builds and serves. It passed; `tmux`, `jq` and `127.0.0.1:8765` were
  all available, as in iteration 8.

**Left:** T012 (`- [!]`, blocked), T013 (BLOCKED-ON-HUMAN), T014–T019, and **T021**, which is
the only unblocked task remaining and is next. T021 has real material waiting for it in these
findings: the `crswd-api` file mode (iteration 5), `quickstart.md`'s installer grep (iteration 9),
and rolling back with `crswd.previous`. **After T021 the loop has nothing left it may do** —
everything else waits on the operator running `crswd keygen`, adding `RELEASE_SIGNING_KEY`, and
committing the public line to `internal/updater/release_key.txt` *and* to the `RELEASE_KEYS`
heredoc in `install.sh`.

**Findings:**

1. **⚠️ The operator's key is now the critical path for six of the seven remaining tasks.**
   T012, T013, T014, T015, T016 and T017 all wait on it directly or behind it; only T021 does
   not. This is no longer "T013 stops for a human" — it is most of the milestone. Worth saying
   plainly at the next handover rather than as a footnote.
2. **Iteration 11's finding about CI linting no shell outside `.claude/hooks/`, `ralph/loop.sh`,
   `.claude/statusline.sh` and `.github/scripts/` still stands, and T012 was its planned home.**
   With T012 blocked, `install.sh` has no scheduled linting at all. It is a two-word change to
   `ci.yml`'s two path lists — **now squarely a fix-lane one-liner rather than something to wait
   for.** `shellcheck` is at `~/.local/bin/shellcheck` but running it was refused by the sandbox
   again, the fourth iteration running.
3. **Iteration 11's finding that nothing tests "unit absent, record present" still stands.**
4. **⚠️ Iteration 10's findings 1 and 2 still stand** — `deploy/crswd.example.service` has
   `ExecStart=%h/bin/crswd` and a required `EnvironmentFile=%h/.config/crswd/env`, while the
   installer places `~/.local/bin/crswd` and `~/.config/crswd/config`. A fresh install still ends
   with a unit that cannot start. **T012 was the thing that would have caught it and T012 is now
   blocked**, which makes the fix-lane PR more urgent, not less.
5. **Iteration 9's finding about `quickstart.md`'s `grep -iE 'nctiggy|/home/[a-z]'` check still
   stands** (T021).
6. **Iteration 7's finding about `gh release list --limit 1000` still stands.**
7. **Iteration 6's finding about `data-model.md`/`contracts/release.md` and `SHA256SUMS.sig`
   still stands** — T015's verifier must expect the sums file to cover five names, not seven.
8. **Iteration 5's finding about `crswd-api` arriving non-executable still stands** (T021's
   README).
9. **Iteration 4's finding about `plan.md`'s "tag-triggered" line still stands.**
10. **Iteration 3's finding about `internal/audit/audit_test.go`'s action table still stands.**
11. **Iteration 2's finding about `AGENTS.md`'s quickstart row still stands.**
12. **No ad-hoc defects observed** in the code touched. `contracts/rain-messages.md` also names
    `internal/httpapi/stylesheet_test.go` in its **Tests** line; nothing was added there, because
    all four tests it lists belong in `partials_test.go` and the two existing rain assertions in
    `stylesheet_test.go` still pass unchanged.

---

## Iteration 13 — 2026-08-08 00:40

**Did:** **T021**, the last task the loop may take. `README.md` now leads with the one-line
install — the `curl … | bash`, what the installer writes, what it refuses to overwrite, and the
plain statement that it refuses every release until the operator commits a key — followed by
**Updating, and rolling back** (`POST /dashboard/update` with a named `version`, and
`~/.local/bin/crswd.previous` for the daemon that no longer starts) and a note that a release
asset carries no file mode. Three tests in new `internal/release/readme_test.go`:
`TestReadmeLeadsWithTheOneLiner`, `TestReadmeDocumentsRollingBack`, `TestReleaseHasNoDependencies`.
Also corrected `quickstart.md`'s FR-020 check (iteration 9, finding 2). Gate green: build, vet,
`go test ./...`, `golangci-lint run` (2.12.2, 0 issues), `gofmt -l` empty, `go.sum` still absent.

**Learned:**

- **`go.sum`'s absence was asserted only under `-tags quickstart`.** `TestQuickstartNoDependencies`
  in `cmd/crswd/quickstart_test.go` is behind the tag, so `go test ./...` never reached it and
  neither did the untagged half of CI — an import added between tagged runs would have been caught
  by a suite nobody ran on the change that added it. `TestReleaseHasNoDependencies` is the untagged
  copy. **The duplication is deliberate**; do not "deduplicate" it by deleting one.
- **Prose is testable the same way the asset name is.** The install command is read out of
  `install.sh`'s header comment with `findIn` and required to appear in `README.md`; the rollback
  path is built from the script's own `readonly BINARY=` line as `~/$BINARY.previous`. Both are
  the `TestAssetNamesAgreeAcrossLanguages` trick applied to a document, and both catch the failure
  that has no symptom until a stranger runs the line.
- **"Leads with" has to be asserted as a position, not a mention.** The test finds the byte offset
  of the install command and requires `go build`, `git clone` and `go mod download` to all appear
  after it. A `strings.Contains` would have passed against a README that mentions the one-liner as
  a footnote under clone-and-build, which is what it looked like before.
- **The installer and the updater verify in opposite orders, and both are correct.** `install.sh`
  is signature-then-checksum (`verify_signature` at :327, then `verify_checksum`); `contracts/
  self-update.md` and T015 fix the updater at checksum-then-signature. Both put both checks in
  front of anything executable, which is the property. **Whoever writes T015 should not "fix" it
  to match the shell** — and the README describes each component's own order, so the two
  paragraphs disagreeing is not a defect in either.
- **All six mutations were run and each fails with the right message**: the base URL in
  `install.sh`'s header changed to `master/` (the drift check, which is the whole reason the
  command is not typed twice); a clone-and-build block inserted above the install line (fires on
  both `go build` and `git clone`, naming both byte offsets); `readonly BINARY` moved to
  `.local/libexec/crswd`; the `crswd.previous` paragraph replaced with a vaguer sentence; a
  one-line `go.sum` created; and a `require` block appended to `go.mod`.
- **No tagged suite was needed.** Nothing here is behind a build tag: `internal/release` is
  test-only in the default build, and `cmd/crswd`, `web/` and `deploy/` were all untouched.
- **The mutation harness lived in `.ralph-tmp/`** (gitignored, removed after), as in iterations 10
  and 11 — `git checkout` cannot revert a mutation while the task's own work is uncommitted.

**Left:** **Nothing the loop may take.** T012 (`- [!]`), T013 (BLOCKED-ON-HUMAN) and T014–T019
all wait, directly or transitively, on the operator's signing key. **This is the handover.**

### ⛔ HANDOVER — what the operator has to do

Six of the seven remaining tasks are behind one manual step, in this order:

1. Run `crswd keygen` — **which T013 has not implemented yet**, so this is two steps: someone
   implements T013 (it writes nothing to disk and commits an *empty*
   `internal/updater/release_key.txt`), then the operator runs it.
2. Add the private half as the repository secret `RELEASE_SIGNING_KEY`.
3. Commit the public half **to both copies**: `internal/updater/release_key.txt` *and* the
   `RELEASE_KEYS` heredoc in `install.sh:72`. `TestInstallerCarriesTheCommittedKeys` fails if only
   one of them is filled — it currently `t.Skip`s, because the file does not exist yet.

Then T014 (sign in CI) → T012 (`verify-install`) and T015–T019 unblock in plan order.

**Findings:**

1. **⚠️ The operator's key is the critical path for the whole remainder of the milestone**
   (iteration 12, finding 1) — now with T021 done it is the critical path for *everything* left.
   T013 itself is the loop's own work up to the halt; what the loop must not do is generate the
   key.
2. **⚠️ Iteration 10's findings 1 and 2 still stand and are still the most urgent fix-lane PR.**
   `deploy/crswd.example.service` — published as the `crswd.service` asset — has
   `ExecStart=%h/bin/crswd` and a required `EnvironmentFile=%h/.config/crswd/env`, while
   `install.sh` places the binary at `~/.local/bin/crswd` and the configuration at
   `~/.config/crswd/config`. **A fresh install still ends with a unit that cannot start**
   (203/EXEC, or a missing `EnvironmentFile`), and the README now tells strangers to run that
   install as the first thing on the page. T012 would have caught it and T012 is blocked. The
   unit also sets `Environment=CRSW_ALLOWED_ROOTS=%h/code` inline, which beats the config file
   the installer's own next-steps text points at.
3. **Iteration 12's finding about CI linting no shell outside `.claude/hooks/`, `ralph/loop.sh`,
   `.claude/statusline.sh` and `.github/scripts/` still stands.** `install.sh` is linted by
   nothing; `shellcheck` was refused by the sandbox for the fifth iteration running. Two words in
   `ci.yml`'s two path lists, and T012 is no longer coming to do it.
4. **Iteration 11's finding that nothing tests "unit absent, record present" still stands.**
5. **Iteration 7's finding about `gh release list --limit 1000` still stands.**
6. **Iteration 6's finding about `data-model.md`/`contracts/release.md` and `SHA256SUMS.sig`
   still stands** — T015's verifier must expect the sums file to cover five names, not seven.
7. **Iteration 4's finding about `plan.md`'s "tag-triggered" line still stands.**
8. **Iteration 3's finding about `internal/audit/audit_test.go`'s action table still stands.**
9. **Iteration 2's finding about `AGENTS.md`'s quickstart row still stands.**
10. **Iteration 9's finding about `quickstart.md`'s installer grep is FIXED** here, and iteration
    5's about `crswd-api` arriving non-executable is **documented** in the README's "A release,
    without the installer" section. Both were held for T021 and both are now discharged.
11. **The README describes `POST /dashboard/update` before it exists**, marked as milestone 6's
    remaining work in the status block above it rather than in the section itself. Whoever
    finishes T019 should delete that qualifier; the route's shape as documented comes straight
    from `contracts/self-update.md` and should need no other edit.
12. **No ad-hoc defects observed** in the code touched.

**No `RALPH_COMPLETE`.** T012–T019 are open, and the tree being green does not make the plan done.

---

## Iteration 14 — 2026-08-08 00:59

**Did:** **T013, up to its halt.** `crswd keygen` in new `cmd/crswd/keygen.go` generates an
ed25519 pair and prints both halves to stdout — private for `RELEASE_SIGNING_KEY`, public for
`internal/updater/release_key.txt` *and* the `RELEASE_KEYS` block in `install.sh` — dispatched
from `main.go` ahead of the config subcommands. `internal/updater/release_key.txt` is committed
holding comments and **no keys**. Four tests in `cmd/crswd/keygen_test.go`:
`TestKeygenWritesNothingToDisk`, `TestKeygenOutputIsParseableByVerifier`,
`TestKeygenIsReachableFromTheCommandLine`, `TestNoPrivateKeyInRepository`. Gate green: build,
vet, `go test ./...`, `golangci-lint run` (2.12.2, 0 issues), `gofmt -l` empty,
**`-tags quickstart ./cmd/crswd` (31.5s, real)** and **`-tags dev ./...`**, because both build
`cmd/crswd`. `go.sum` still absent. **T013 is marked `- [!]` in both files and this is the
handover; no key was generated, committed, or fixtured.**

**Learned:**

- **"Writes nothing to disk" is asserted twice and neither half is sufficient.** The behavioural
  sweep watches `$HOME`, `$TMPDIR` and the working directory, which is a *guess about where*; a
  mutation writing to a hard-coded `/tmp` path passed it. The structural half cannot miss:
  `keygen.go`'s imports are held to a four-entry allowlist (`crypto/ed25519`, `crypto/rand`,
  `encoding/base64`, `io`), so **the file cannot reach the filesystem or the logger at all**.
  Both mutations fire on the first, only the second fires on the hard-coded path. Keep both.
- **⚠️ A test failure is a log, and mine leaked a key before it was fixed.** The URL-encoding
  mutation made `TestKeygenOutputIsParseableByVerifier` fail, and the failure message dumped
  keygen's whole output — including the private half — into the terminal. That is FR-030's exact
  prohibition arriving through the one door nobody guards, and in CI it would land in a public
  log. Fixed here with `redacted()`, which masks any base64 run of 40+ characters **in the URL
  alphabet as well as the standard one** — because the failure being reported may itself be
  "printed in the wrong encoding", and a redactor that knew only the right one would let exactly
  that case through. **T015–T019 all handle signatures and keys: any assertion of theirs that
  prints a buffer needs the same treatment.** (The key that leaked was ephemeral, generated by
  the mutated binary, never committed and never used to sign anything.)
- **A per-line base64 scan is not enough either, and that was also found by mutation.** Printing
  the private half a second time *inside a sentence* passed a scan that only decoded whole
  trimmed lines. It now extracts blobs with a regexp anywhere in the output and classifies by
  decoded length, plus a separate assertion that each key sits on a line of its own — which is
  what makes it selectable and pasteable without hand-editing.
- **`ed25519.PrivateKey.Public()` copies `priv[32:]`; it does not derive it.** So comparing a
  private key's stored tail against itself passes for any 64 bytes. Both the output test and
  `TestNoPrivateKeyInRepository`'s detector use `ed25519.NewKeyFromSeed(raw[:32])` and compare
  the whole 64 bytes instead. That derivation is also what makes the detector safe to fail a
  build on: an unrelated blob satisfies it with probability 2^-256.
- **`TestNoPrivateKeyInRepository` never puts a key in the repository, and neither should
  anything else.** It sweeps the tree (skipping `.git` only — `.github` is where a key
  hard-coded instead of read from a secret would live), then proves the sweep is not silent by
  pointing it at a `t.TempDir()` holding two planted keys. It also returns a file count, because
  "found nothing" and "read nothing" look identical from the caller. It cannot see a bare
  32-byte seed — indistinguishable from any other 32 random bytes — which is the accepted limit,
  and it is why keygen prints the 64-byte form.
- **The file the sweep searches for its own needle is the file it is in — and so is this one.**
  The PEM banner's tail is assembled in `keygen_test.go` as two string literals joined by `+`,
  so the file does not match itself; a detector that reports itself is one somebody switches
  off. **This notebook hit the same trap on the first run of the finished suite**, because the
  paragraph explaining the trick quoted the needle. It is now described rather than spelled.
  Anything written about this sweep — a doc, a comment, an issue in the tree — has to do the
  same. That the sweep found it is the sweep working.
- **`main_test.go`'s `exempt < N` floor does not catch a lost exemption, and did not before this
  task either.** `internal/audit/audit.go` names `os.Stdout` **twice**, so with the trail,
  `runConfigCommand` and `printVersion` the count is already 4 — the floor was raised 3 → 4 here
  and the "keygen wired to `io.Discard`" mutation still passed it. That floor's job is "is the
  sweep reading the right tree", and it still does that. **The wiring is proven by
  `TestKeygenIsReachableFromTheCommandLine` instead**, which is the plan's "a task is done when
  something calls it" rule: the other three tests call `runKeygen` directly and every one of them
  stays green against a `keygen.go` nothing dispatches to. It asserts the call exists exactly
  once *and* that its first argument is `os.Stdout` — `os.Stderr` would put the key on the
  stream systemd merges into the journal, and every buffer-reading test would still pass.
- **`t.Chdir` is unavailable**: `go.mod` says `go 1.23.0` (the toolchain is 1.24.0), and stdlib
  APIs are gated on the directive. The write-watching test sets `HOME`/`TMPDIR`/`XDG_*` and
  watches `os.Getwd()` in place instead.
- **The private half's encoding was a real choice and is worth not re-deriving.** `contracts/
  signing.md` says only `<base64>` for it. It is base64 of Go's 64-byte `ed25519.PrivateKey`
  (seed first, public half after), symmetric with the public half's raw 32 bytes, which
  `install.sh` already wraps in the constant 12-byte SPKI header. **T014 needs the seed if it
  signs with `openssl`: it is the first 32 bytes.** Note the SPKI concatenation trick does *not*
  transfer to PKCS#8 — the public header is 12 bytes and 12 divides by three, the private one is
  16 and does not, so `"MC4CAQAwBQYDK2VwBCIEIA" + base64(seed)` is **not** generally correct.
  T014 must assemble those 48 bytes properly, or sign in Go.
- **`internal/updater/` is a directory with no Go file, and the toolchain is happy.** Build,
  vet and the linter all ignore it. T015 adds `verify.go` with the `go:embed`.
- **`TestInstallerCarriesTheCommittedKeys` is live now** — it skipped only because the file was
  absent. Proven by mutation: a line in `release_key.txt` that `install.sh` does not carry goes
  red naming both files. That is the operator's guard rail for step 3 of the handover.
- **Ten mutations were run and each fails with the right message**, two of them only after the
  test was strengthened (the in-prose second copy, and the hard-coded `/tmp` write): the key
  saved into `$HOME`; the key saved outside the watched directories; the public half in URL
  base64; the two halves taken from different pairs; the private half echoed in a summary line;
  a key printed inside a longer line; `install.sh` dropped from the handover text; a real key
  present in the tree being swept; the `main.go` dispatch deleted; and the dispatch wired to
  `os.Stderr`. The mutation harness lived in `.ralph-tmp/` (gitignored, removed after), as in
  iterations 10, 11 and 13.

**Left:** **Nothing the loop may take. This is the same handover as Iteration 13, with T013's
half of it now done.** T012 (`- [!]`), T013 (`- [!]`, halted at the key) and T014–T019 all wait,
directly or transitively, on the operator's signing key.

### ⛔ HANDOVER — what the operator has to do

Everything remaining is behind one manual step, now reduced to three actions:

1. Run **`crswd keygen`** on your own machine. It prints both halves and writes nothing, so the
   terminal is the only copy — store the private half before closing it.
2. Add the private half as the repository secret **`RELEASE_SIGNING_KEY`**.
3. Commit the public half **to both copies**: a new line in `internal/updater/release_key.txt`
   and the same line in the `RELEASE_KEYS` heredoc at `install.sh:72`.
   `TestInstallerCarriesTheCommittedKeys` fails if only one of them is filled — it is no longer
   skipping, and the mutation above confirms it fires.

Then T014 (sign in CI) → T012 (`verify-install`) and T015–T019 unblock in plan order.

**Findings:**

1. **⚠️ `quickstart.md`'s SC-009 check cannot pass and never could.** Line 110 runs
   `grep -rIl 'PRIVATE KEY\|RELEASE_SIGNING_KEY' --exclude-dir=.git . | grep -v '\.md$'` and says
   it "must be empty", but `install.sh` has named the secret since T009 and `cmd/crswd/keygen.go`
   must name it — `contracts/signing.md`'s worked example prints "paste into the repository
   secret RELEASE_SIGNING_KEY". SC-009 is about the *key*; that grep matches the *name of the
   secret*, which is not key material and appears in every place the procedure is documented.
   **`TestNoPrivateKeyInRepository` is the enforceable version of SC-009** and it is untagged, so
   CI runs it. Not fixed: it is one line in a superseded artifact and outside T013, which is
   under an explicit instruction to stop. Same shape as iteration 9's finding about the installer
   grep, which T021 corrected. **Worth a fix-lane line, and worth it before the operator runs the
   quickstart** — otherwise the first thing they see after `crswd keygen` is a check that appears
   to say the loop committed a key.
2. **⚠️ Iteration 10's findings 1 and 2 still stand and are still the most urgent fix-lane PR.**
   `deploy/crswd.example.service` — published as the `crswd.service` asset — has
   `ExecStart=%h/bin/crswd` and a required `EnvironmentFile=%h/.config/crswd/env`, while
   `install.sh` places the binary at `~/.local/bin/crswd` and the configuration at
   `~/.config/crswd/config`. **A fresh install still ends with a unit that cannot start**
   (203/EXEC, or a missing `EnvironmentFile`), and the README tells strangers to run that install
   as the first thing on the page. T012 would have caught it and T012 is blocked.
3. **An empty zero-byte file `/tmp/crswd-keygen-probe` was left behind by mutation 2** and the
   sandbox refused to remove it (writes and deletes outside the repository are blocked). It holds
   nothing — the mutation deliberately wrote `nil` rather than key material, precisely so a
   leftover could not matter. `rm /tmp/crswd-keygen-probe` when convenient.
4. **Iteration 12's finding about CI linting no shell outside `.claude/hooks/`, `ralph/loop.sh`,
   `.claude/statusline.sh` and `.github/scripts/` still stands.** `install.sh` is linted by
   nothing; `shellcheck` was refused by the sandbox again. Two words in `ci.yml`'s two path lists.
5. **Iteration 11's finding that nothing tests "unit absent, record present" still stands.**
6. **Iteration 7's finding about `gh release list --limit 1000` still stands.**
7. **Iteration 6's finding about `data-model.md`/`contracts/release.md` and `SHA256SUMS.sig`
   still stands** — T015's verifier must expect the sums file to cover five names, not seven.
8. **Iteration 4's finding about `plan.md`'s "tag-triggered" line still stands.**
9. **Iteration 3's finding about `internal/audit/audit_test.go`'s action table still stands** —
   and note it is a *second* table with the same weakness as `main_test.go`'s floor, above.
10. **Iteration 13's finding that the README describes `POST /dashboard/update` before it exists
    still stands** — T019 should delete the qualifier above that section.
11. **No ad-hoc defects observed** in the code touched. The two defects found here were in this
    iteration's own tests, both by mutation, and both are fixed above rather than logged.

**No `RALPH_COMPLETE`.** T012–T019 remain open and every one of them is behind the operator's
key. A fresh iteration has nothing it may take: read the handover above, confirm
`internal/updater/release_key.txt` still holds no key line, and stop.

> **Iteration 15 corrects the last sentence.** T018 was not behind the key and never had been.
> The key file was confirmed unchanged — it still holds no key line — and the handover above
> still stands for T012 and T014.

---

## Iteration 15 — 2026-08-08 01:20

**Did:** **T018**, `internal/updater/fetch.go` — the first Go file in that package. `NewFetcher`
asks the GitHub API what a release published (`/releases/latest`, or `/releases/tags/<version>`
for the named rollback) and returns exactly one asset by exact name, over a client carrying its
own `CheckRedirect`. Refusals, each with a sentinel: `ErrRedirectRefused` (a host outside
`releaseHosts`), `ErrInsecureTransport` (any URL that is not https, **including the ones the API
hands back**), `ErrAssetNotFound` (a name that is not the whole name), `ErrMalformedVersion` (a
version that is not `^v[0-9]+(\.[0-9]+)*$`, refused **before** a request is built). It verifies
nothing and writes nothing — T015 and T016. Seven tests in `internal/updater/fetch_test.go`,
and `internal/release`'s `tarballName` now calls `updater.AssetName` instead of holding a fourth
spelling of it. Gate green: build, vet, `go test ./...`, `-race` on the new package,
`golangci-lint run` (2.12.2, 0 issues), `gofmt -l` empty, and `go vet` under all three tags.
`go.sum` still absent.

**Learned:**

- **⚠️ The loop was not finished, and the plan's own summary was what said it was.** The plan
  header read "there is no unblocked task left"; `tasks.md` draws **no** dependency into T018,
  lists it under *Parallel opportunities* as "a different file that adds no verification", and
  `contracts/self-update.md` closes with "**Task 1 must leave a daemon that can download and
  verify nothing installed** — if it can swap, verification is being built after the thing it
  protects". Three iterations read the summary and stopped. **The plan says `tasks.md` supersedes
  anything it summarises; that includes its own stop signs.** Header corrected in both files.
- **`httptest`'s certificate is issued for `example.com` as well as `127.0.0.1`**, which is what
  makes a cross-host test possible without relaxing TLS. The transport is
  `srv.Client().Transport.(*http.Transport).Clone()` with a `DialContext` that sends
  `example.com:*` to the test server's address and dials everything else for real. So the good
  host is a *name*, the far host is `127.0.0.1:<port>` — genuinely different hosts, both
  cert-valid, and the shipped `CheckRedirect` is the one under test rather than a copy.
- **Test the refusal by giving the wrong answer a real server.** `TestCrossHostRedirectRefused`'s
  far end records what it was asked for, so the mutation "drop `CheckRedirect`" does not fail
  differently — it *succeeds*, and the test prints the bytes that arrived from a host no release
  comes from. It also asserts a **same-host** redirect is followed, because every real download
  is one: a client that refuses every redirect passes the headline case and can never update.
- **⚠️ A size limit checked after the read is not a limit, and my first test could not see the
  difference.** `io.ReadAll(resp.Body)` followed by `if len(body) > limit` refuses exactly the
  same responses as `io.LimitReader` does — a gigabyte later, after the allocation the far end
  chose. The mutation passed. The test now serves one byte past the limit and then **never
  finishes**: bounded reading returns at once, unbounded reading is still waiting, and the
  assertion is on which. Same shape as Iteration 14's leak: the mutation is what found it.
- **`Host`, not `Hostname()`.** A redirect to another port on a host a release does come from is
  not a release either, and comparing hostnames would let it through. It is also what lets the
  test distinguish two `127.0.0.1` servers if a later one needs to.
- **`DisallowUnknownFields` is wrong at this boundary** and this is the exception to
  `docs/security.md` §2, noted in the file: GitHub answers with dozens of fields and adds more
  without telling anybody, so a strict decoder fails against a release that is perfectly fine.
  What is constrained instead is what is *read* — two strings, neither reaching a filesystem or
  a command line.
- **The version is validated here rather than at the route**, because this is where it becomes a
  URL path. `v0.42?draft=true` was the sharp mutation case: with the check removed it did not
  error at all, it fetched `/releases/tags/v0.42` with a query nobody asked for. T019 takes this
  value from a form field, so `ErrMalformedVersion` is named for it to answer 400 with.
- **GitHub 403s an API request with no `User-Agent`**, which would read as "this release is not
  there". Set per request from `buildinfo.Version`, for the reason `version.go` gives for reading
  it per request rather than at construction.
- **Nine mutations were run and each fails with the right message**: `CheckRedirect` dropped
  (three tests, one of them printing the downloaded bytes); asset matched by suffix; the same by
  prefix; the scheme check dropped; the scheme check applied to redirects only; version
  validation dropped; the unbounded read (which **passed** until the test was rewritten);
  `NewFetcher` given no hosts and a transport of its own; the asset name respelled with dashes;
  and `repoPath` pointed at a repository one letter different. The harness lived in `.ralph-tmp/`
  (gitignored, removed after), as in iterations 10, 11, 13 and 14.
- **`errcheck` runs with `check-blank: true` here**, so `_, _ = w.Write(...)` in a test handler
  is an error like any other — the house idiom is `if _, err := w.Write(b); err != nil`. And
  **gosec's G705 fires on a test server that echoes its own request path** into the response;
  the fix was to make the server answer only from a map of what the release published, which is
  a better fake anyway — a handler that builds its answer from the request serves every name
  equally well, including the ones that have to come back as a refusal.

**Left:** T012 (`- [!]`), T013 (`- [!]`, halted at the key), T014, T015, T016, T017, T019.

**Findings:**

1. **⚠️ Whoever takes the next task must decide whether T015 is really blocked, and should not
   take "the plan says so" for an answer.** *For blocked*: `tasks.md`'s graph draws
   T013 → T014 → T015, and says "T014 and everything after it wait on a human". *Against*: a
   verifier is testable with ephemeral pairs generated in the test (which is how `install_test.go`
   already tests signature refusal), an empty `release_key.txt` is the **documented** "refuse
   every release" state rather than an unfinished one, and T015's own entry names no dependency
   on a signature existing anywhere. The same is then true of T016, T017 and T019 in order. **I
   did not take it** — one task per iteration, and this one is a judgement the plan asks to be
   made deliberately. What is certainly still blocked: **T014** (signs with a secret nobody
   holds) and **T012** (installs from a release that carries no signature until T014 runs).
2. **⚠️ `releaseHosts` names two githubusercontent hosts that could not be checked from here.**
   Outbound network calls are refused in this sandbox, so `objects.githubusercontent.com` and
   `release-assets.githubusercontent.com` are written from knowledge of where GitHub redirects
   release asset downloads, not from an observed redirect. **The failure mode is a refusal that
   names the host it refused**, which is the safe direction and is why it was written this way
   rather than as a wildcard — but it means the first real `POST /dashboard/update` may refuse
   with "a release comes from …" and need one line added. **T012's `verify-install` job is where
   this would be caught**, and T012 is blocked; an operator can also confirm it in one command:
   `curl -sI <a release asset URL> | grep -i ^location`.
3. **⚠️ Iteration 14's finding 2 still stands and is still the most urgent fix-lane PR.**
   `deploy/crswd.example.service` — published as the `crswd.service` asset — has
   `ExecStart=%h/bin/crswd` and a required `EnvironmentFile=%h/.config/crswd/env`, while
   `install.sh` places `~/.local/bin/crswd` and `~/.config/crswd/config`. **A fresh install still
   ends with a unit that cannot start**, and the README's first instruction is that install line.
4. **Iteration 14's finding 1 still stands**: `quickstart.md`'s SC-009 check greps for the
   *name* of the `RELEASE_SIGNING_KEY` secret and calls a match a failure, which `install.sh` and
   `keygen.go` both legitimately produce. Worth fixing before the operator runs the quickstart.
5. **Iteration 12's finding about CI linting no shell outside `.claude/hooks/`, `ralph/loop.sh`,
   `.claude/statusline.sh` and `.github/scripts/` still stands.** `install.sh` is linted by
   nothing; `shellcheck` was refused by the sandbox again.
6. **Iteration 14's finding 3 still stands**: an empty zero-byte `/tmp/crswd-keygen-probe` is
   still there; the sandbox refuses to remove it. It holds nothing.
7. **Iteration 11's finding that nothing tests "unit absent, record present" still stands.**
8. **Iteration 7's finding about `gh release list --limit 1000` still stands.**
9. **Iteration 6's finding about `data-model.md`/`contracts/release.md` and `SHA256SUMS.sig`
   still stands** — T015's verifier must expect the sums file to cover five names, not seven.
   `ChecksumsAsset`/`SignatureAsset` are now constants in `internal/updater` for it to use.
10. **Iteration 4's finding about `plan.md`'s "tag-triggered" line still stands.**
11. **Iteration 3's finding about `internal/audit/audit_test.go`'s action table still stands.**
12. **Iteration 13's finding that the README describes `POST /dashboard/update` before it exists
    still stands** — T019 should delete the qualifier above that section.
13. **No ad-hoc defects observed** in the code touched. The one defect found here was in this
    iteration's own size-limit test, found by mutation, and fixed above rather than logged.

**No `RALPH_COMPLETE`.** T012 and T014 wait on the operator's key. T015–T017 and T019 are open,
and finding 1 above is the decision the next iteration has to make first.
