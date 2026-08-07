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

## Iteration 0 — milestone 4 begins

**Did:** Archived milestones 1–3 to `archive/progress-milestones-1-3.md` — 14,784 lines,
which was becoming a context cost paid by every fresh iteration for memory that is now
history rather than working state. Nothing was deleted; the archive is complete.

**Learned:** The previous file ended with a line reading exactly `RALPH_COMPLETE`, which is
the loop's own exit sentinel. Starting milestone 4 against it would have declared the plan
complete after one iteration with all 35 tasks still open — the same class of bug as the
substring-grep failure the loop already carries a `-x -F` fix for. **If you archive this file
again, check the last line.**

**Left:** T001–T035 in `IMPLEMENTATION_PLAN.md`, all open.

**Findings:** Four abandoned lane branches hold ~3,800 lines of working code that each build
standing alone and broke only against a moved `main`. Tasks T003, T014, T023 and T026 carry
them forward. Read the branch before writing anything — those files' comments are the best
documentation in the repo of why the config format is what it is.

---

## Iteration 1 — 2026-08-07 02:35

**Did:** T001. `internal/config/secret.go` holds `IsSecret(key string) bool`, true for exactly
`shared_secret` and `allowed_identities`. `secret_test.go` carries the behavioural table and
`TestIsSecretIsTheOnlyClassifier`, which parses every non-test file in the package and fails on
(a) any other file naming a secret key as an exact string literal, and (b) any other function
shaped like a key classifier — one `string` in, one `bool` out, `secret` in its name.

**Learned:**

- **The mutations were run, not reasoned about.** Cutting `allowed_identities` out of `IsSecret`
  fails both tests; adding `var secretFileKeys = map[string]bool{...}` plus an `isSecretish`
  helper to `config.go` fails the structural one with file:line positions. Both were reverted and
  `git status` checked clean before the gate. Do this for T005, T007, T011 and T019 too — the
  plan says a mistake in those is invisible, and "invisible" means the suite passing is not
  evidence.
- **The literal match is exact, never a substring, and that is load-bearing.** `config.go:344`
  formats `"config{shared_secret:<redacted> ..."` — the redaction naming the field it refuses to
  print. A substring match reports it, someone adds an exclusion, and the next reader stops
  believing the test.
- **Test files are exempt from the walk, deliberately.** T003's `file_test.go` worked example
  *must* contain `shared_secret = hunter2#not-a-comment`. A fixture is not a second classifier.
  Build tags are also not honoured — `parser.ParseFile` reads a file whatever it is tagged with,
  so a tag is not a hiding place.
- **`t.Fatalf` guards against a vacuous pass.** The walk exempts `secret.go`, so it asserts first
  that `secret.go` exists, declares `IsSecret`, and names both keys. Without those three, deleting
  the classifier makes the search find nothing anywhere and pass.
- **`isPlaceholder` in `config.go` has the classifier shape** (`func(string) bool`) and is not
  flagged, because the check also requires `secret` in the name. `loadSecret` has the name and not
  the shape. If you add a `func isSensitive(key string) bool`, the check will *not* catch it —
  the name filter is the price of not flagging `isPlaceholder`.
- **Lint is UNVERIFIED locally and cannot be fixed from inside the loop.** The binary on PATH is
  v1.62.2, which reads the v2 config, runs zero linters and exits 0 (#26). `go install
  golangci-lint/v2@v2.12.2` was attempted and **denied by the sandbox** — this session is
  non-interactive and the install needs approval. `go build`, `go vet`, `go test ./...` and
  `gofmt -l` are all green; CI installs the pinned v2.12.2 and is the real gate. Do not report a
  green `golangci-lint run` from this machine as evidence.

**Left:** T002–T035. Next is T002 (`Source` type), which is `[P]` and touches only new files.

**Findings:**

- **The walk stops at `internal/config/`, as T001 specifies — but T011 renders secrets in
  `internal/httpapi`.** Nothing stops a second list of secret keys appearing in `settings.go`.
  Widening the walk to the repo is safe (checked: no non-test file outside this package matches
  either literal exactly), and **T011 is where to do it**, not here — AR-008.
- **`IsSecret` is an exact match, so a mixed-case key would classify as not-secret.** Safe only
  because the file grammar fixes a key to `[a-z0-9_]+`, so `Shared_Secret` is refused as malformed
  before anything asks. There is a table row pinning that with the reasoning. **T003 owns this
  coupling**: if the parser ever lower-cases keys instead of refusing them, or the grammar widens,
  exact matching becomes a fail-open.
- **`allowed_identities` maps to no environment variable today.** The rule is "the variable minus
  `CRSW_`, lower-cased", and the closest thing that exists is `CRSW_ACCESS_ALLOWED_EMAILS`, which
  by that rule spells `access_allowed_emails`. `data-model.md` and both contracts name
  `allowed_identities` consistently, so it is the intended file key — but T003/T004 will have to
  decide whether it is a **rename** of `access_allowed_emails` (belonging in `renamedKeys`, FR-006)
  or a new key with no variable behind it. Not a blocker for T001, which was told the two literals
  outright. **Flagging it for T003, not guessing it here.**
