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
