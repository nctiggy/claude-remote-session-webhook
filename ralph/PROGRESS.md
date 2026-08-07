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
