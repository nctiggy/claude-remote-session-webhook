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

---

## Iteration 2 — 2026-08-07 02:42

**Did:** T002. `internal/config/source.go` holds `type Source uint8`, the four constants in the
`data-model.md` iota order, and `String()` returning `default` / `file` / `environment` / `flag`.
`source_test.go` carries `TestSourceStringsAreTheSettingsPageVocabulary`: the four words as
literals, `SourceDefault == 0`, an unnamed layer must not borrow a named one's word, and a walk
of the package's non-test files for any `Source` constant the list does not account for.

**Learned:**

- **The five mutations were run, not reasoned about**, per iteration 1's rule. Caught: a fifth
  constant appended to the iota run; a fifth declared as `const SourceOverride = Source(4)` in a
  block of its own; `"environment"` abbreviated to `"env"`; the `default:` arm returning a real
  word; and the iota reordered so `SourceDefault` is 3. Each was reverted and `source.go` read
  back byte-for-byte before the gate.
- **`packageFiles`, `declares` and `stringLiterals` from `secret_test.go` are reusable** — same
  `package config_test`, so a new test file just calls them. `sourceConstants` is the only new
  helper. Whatever T007/T008 add to `source_test.go`, the AST scaffolding already exists.
- **The `default:` arm of `String()` is load-bearing and is not dead code.** It returns
  `Source(4)` rather than a word so a layer with no vocabulary cannot blend into the settings
  page's source column as one of the four. The mutation that made it return `"flag"` is the one
  a reader would "tidy up" into existence.
- **A const walk has to handle two spellings.** An iota continuation carries no `Type` and no
  `Values` and inherits from the spec that opened the run; a constant in its own block carries a
  value and no type. `sourceConstants` tracks the run *and* falls back to a `Source`-prefixed
  name, which is what catches the second spelling.
- **`config.Source(len(sourceVocabulary))` trips gosec G115** (int → uint8). `config.SourceFlag + 1`
  says the same thing with no conversion — untyped constant arithmetic on a typed value. Worth
  knowing before T008 writes `map[string]Source` code with indices in it.

- **Iteration 1's `NEEDS CLARIFICATION` was answered by the operator *during* this iteration.**
  Commit `b0f16c2` landed at 02:38, between this iteration's first read of `secret.go` and its
  gate, and renamed two keys across the contracts, `data-model.md`, `tasks.md` and `secret.go`:
  `allowed_identities` → **`access_allowed_emails`** and `default_lifetime` → **`session_lifetime`**,
  both by applying the stated rule (the variable minus `CRSW_`, lower-cased) instead of describing
  the setting. **T003 must derive keys by that rule and must not enter either old spelling in
  `renamedKeys`** — a rename of a spelling that never shipped invents version skew. Anything above
  this line in the notebook that says `allowed_identities` is describing a spelling that no longer
  exists. Nothing in T002 depended on it; the gate was re-run on the merged tree.

**Left:** T003–T035. Next is T003 (carry the parser forward from
`claude/issue-issue-65-20260807-0112`), which is the first task that touches an abandoned branch.

**Findings:**

- **The session-start warning about the linter is overstated, and that matters.** It says a pre-v2
  binary "runs zero linters and exits 0". The v1.62.2 binary on PATH **flagged a real gosec G115**
  in the first draft of `source_test.go`, so it is reading the v2 `enable:` list (gosec is not in
  v1's default set) and running at least part of it. It is still not the pinned v2.12.2 and is
  still not the gate — but "a green here proves nothing" is not the same claim as "it checks
  nothing", and treating a v1 *finding* as noise would have shipped that conversion to CI. Not
  fixing the hook here (AR-008); flagging the wording.
- **`go install golangci-lint/v2@v2.12.2` was not retried.** Iteration 1 recorded it as denied by
  the sandbox in a non-interactive session; nothing has changed, so lint stays UNVERIFIED locally
  and CI remains the real gate.
- **T008 will key provenance by environment-variable name (`CRSW_LISTEN`) while T011/T012 render
  the file spelling (`listen`).** `IsSecret` takes the file spelling; the provenance map takes the
  variable name. Something has to convert between them, and `data-model.md` states the rule — the
  variable minus `CRSW_`, lower-cased — but no code owns it yet. T003 derives keys by that same
  rule for the parser, so **T003 is the natural home for one exported conversion**, and T012
  reading the map should not re-derive it a second time.

---

## Iteration 3 — 2026-08-07 02:53

**Did:** T003. `internal/config/file.go` carries the grammar forward from
`origin/claude/issue-issue-65-20260807-0112` and nothing else: `ParseFile(path, data)` →
`*File`, whole-line `#` comments, `strings.Cut` on the first `=`, both ends trimmed and the
inside of a value left alone, keys held to `[a-z0-9_]`. `KeyForVar`/`VarForKey` are the one
exported statement of the derivation rule that iteration 2 asked T003 to own, and `File.Lookup`
takes an **environment variable name** while the map is keyed by the **file spelling**, so the
conversion happens in exactly one place. `file_test.go` carries the five contract tests plus a
round-trip of the rule over every variable `config.go` declares, the no-quoting-the-line
refusal, and the mixed-case-key case.

**Learned:**

- **Seven mutations were run, not reasoned about**, per iteration 1's rule: split on the last
  `=`, strip a trailing `#`, fold a mixed-case key to lower case, quote the malformed line back
  in the refusal, treat a `#` anywhere as a comment, collapse whitespace inside a value, and skip
  a malformed line silently. Each failed; each was reverted and both files checked by `sha256sum`
  against their pre-mutation digests before the gate.
- **`git checkout`/`git checkout-index`/`cp` to `/tmp` are all denied by this sandbox**, so the
  revert-after-mutation loop cannot use them. What works: `git add` the file first, then revert
  by hand with the Edit tool and confirm with `sha256sum`. Budget for that when mutating T005,
  T007, T011 and T019.
- **Deliberately left on the branch, for the tasks that own them.** Read them there rather than
  reinventing: `git show origin/claude/issue-issue-65-20260807-0112:internal/config/file.go`.
  `ErrConfigFile`, `Vars()`, `renamedKeys`, `maxKeyLen`, `versionKey`/`SchemaVersion` and
  `checkSchemaVersion` → **T004**. `readConfigFile`'s open/stat/mode/size handling and
  `maxConfigFileBytes` → **T005/T006**. `DefaultPath` and `layeredEnv` → **T007**. The branch's
  `file_test.go` (~15 end-to-end tests driving `config.LoadFrom`) is the test material for
  T004–T007 and should be carried, not rewritten.
- **T004 must carry `maxKeyLen` (32) and its comment when it starts quoting keys.** T003 quotes
  nothing, so it does not need the bound. T004's message shape is `has unknown key %q`, and
  `openssl rand -hex 32` produces 64 characters that are *all* inside `[a-z0-9_]` — a secret
  pasted onto a line without its key parses as a perfectly valid key and would be quoted into
  stderr and the journal. The bound is the only thing standing between that and a leak.
- **`version` currently parses as an ordinary key.** `Lookup(VarForKey("version"))` answers
  `"1"`. Nothing asks for `CRSW_VERSION` so it is inert today, but T004 owns the version key and
  must consume it out of `values` — left in, T012 renders a `version` row for a setting that is
  not one.
- **A repeated key is last-wins in the parser right now**, and it is commented as such. T004's
  `TestRepeatedKeyRefuses` is what fixes it; the grammar's job is what a line *means*, not which
  of two lines wins.
- The T003 tests reuse `declaredVars(t)` from `envexample_test.go`, which parses `config.go` for
  `CRSW_` constants. So a variable added to `config.go` whose name does not round-trip through
  the key rule fails T003's suite, not just T004's.

**Left:** T004–T035. Next is T004 (the file-level refusals), which is the other half of what the
abandoned branch already wrote.

**Findings:**

- **`contracts/config-file.md` says the worked example "yields exactly eight keys" and the
  example beneath it sets seven** — `version`, `listen`, `allowed_roots`, `start_commands`,
  `session_lifetime`, `idle_timeout`, `shared_secret`. I did **not** invent an eighth: the
  example itself is unambiguous about what parses to what, so `TestParseAcceptsWorkedExample`
  asserts those seven pairs exactly and the prose count is what is wrong. Worth correcting in the
  contract, by whoever owns it, before T034 writes `config.example` against the same text.
- **The branch's `layeredEnv` is not the contract's `withFile`, and T007 must not carry it
  verbatim.** `layeredEnv` returns `getenv` *unchanged* when the file is empty. Under T008 the
  same shim also records provenance, so that short-circuit would leave the `map[string]Source`
  empty for every no-file deployment — and `TestSourceRecordedForEveryKey` asserts every key has
  a `Source` after `Load`. The shim has to run on every lookup even when there is no file.
- **`.env.example` still exists and `envexample_test.go` (172 lines) pins it.** The abandoned
  branch *renamed* it to `config.example` and updated `.gitignore`, `.gitleaks.toml`, `README.md`,
  `deploy/README.md` and `cmd/crswd/quickstart_test.go` in the same commit. T034 says only
  "write `config.example` at the repository root" and says nothing about the old file — so T034
  has to decide whether it is a rename (and carry those five edits, including the gitleaks rule
  that teaches the scanner the file spelling of the two secret-bearing settings) or a second
  example beside the first. Two example files disagreeing is the failure mode.
- **`envPrefix = "CRSW_"` is now declared twice** — `file.go` in `package config` and
  `envexample_test.go` in `package config_test`. Legal today because they are different packages.
  It would collide the moment a test file in this package moves to the internal form.
- **Lint remains UNVERIFIED locally** (v1.62.2 on PATH, #26). `golangci-lint run` was silent,
  `gofmt -l .` clean, `go vet` green untagged and under `-tags tmux`, `-tags quickstart` and
  `-tags dev`, and `go.sum` is still absent. `go install golangci-lint/v2@v2.12.2` was not
  retried; the sandbox denial from iteration 1 has not changed. CI is the gate.

---

## Iteration 4 — 2026-08-07 03:04

**Did:** T004. `internal/config/file.go` carries the five file-level refusals forward from
`origin/claude/issue-issue-65-20260807-0112`: unknown key, repeated key, malformed line, future
schema version, and the renamed-key *warning*. With them come `Vars()` (the 18 variables the
unknown-key check is made of), `maxKeyLen`, `versionKey`/`SchemaVersion`/`checkSchemaVersion`,
and an empty `renamedKeys`. `file_test.go` carries the six contract tests plus
`TestVarsNamesEveryDeclaredVariable`, `TestARenamedKeyRepeatsItsCurrentSpelling` and
`TestAnOverlongKeyIsRefusedWithoutQuotingIt`.

**Learned:**

- **Eight mutations were run, not reasoned about**, per iteration 1's rule: unknown key skipped,
  rename not resolved, repeated key last-wins, version key ignored, the value `%q`-ed into the
  unknown-key message, the `maxKeyLen` bound widened ×100, `EnvMaxStreams` dropped from `Vars()`,
  and the rename resolved *after* the seen-check instead of before. Each failed; all three files
  were then checked by `sha256sum` against their pre-mutation digests and `git diff --stat` came
  back empty before the gate.
- **`maxKeyLen` is not tidiness, and the mutation proves it.** Widening the bound made
  `TestErrorNeverContainsValue/a_secret_pasted_where_a_key_belongs` print
  `has unknown key "0123456789abcdef…"` — a 64-character hex secret quoted into stderr and the
  journal. That is the whole reason the bound exists and iteration 3 was right to hand it to T004.
- **`ParseFile` now takes a third argument, `warn io.Writer`** — the rename warning needs a sink,
  and this is the house shape (`LoadFrom(getenv, warn, opts...)`). `nil` becomes `os.Stderr`, not
  `io.Discard`, for the reason `LoadFrom` does it: a file that still works is the one thing that
  will never prompt an operator to update it. **T007 wiring `withFile` must thread `LoadFrom`'s
  own `warn` through**, or the rename banner ends up on a different stream from every other
  startup warning.
- **`renamedKeys` is empty and the mechanism is still proven**, via `internal/config/export_test.go`
  — a new test-only file exposing `parseFile(path, data, renames, warn)` and `RenamedKeys()`. The
  branch made the rename table a parameter for exactly this reason but never wrote the test, so it
  had a rename mechanism nothing had ever run. `export_test.go` compiles only under `go test`,
  declares no constants (so no clash with `envPrefix`), and is exempt from T001's classifier walk.
- **The rename resolves *before* the repeated-key check.** This is a deliberate deviation from the
  branch, which checked `seen` first: with the branch's order, `bind_address = a` and `listen = b`
  in one file are two keys, and one silently overwrites the other. `TestARenamedKeyRepeatsItsCurrentSpelling`
  pins it in both orderings.
- **`version` is consumed, not stored**, as iteration 3 asked. `TestParseAcceptsWorkedExample` had
  to change: it previously asserted `Lookup(VarForKey("version")) == "1"` and now asserts the
  opposite. `TestWhitespaceAroundSeparatorIgnored` also had to move off its `a = b` fixture — `a`
  is not a key this daemon has, so every case in it would now be refused as unknown. **Expect the
  same for any future test that invents a key.**
- **Contract wording beat branch wording again**, as in T003. The branch's messages wrap an
  `ErrConfigFile` sentinel and read "configuration file %s:%d sets %s, which this daemon does not
  read…"; `tasks.md` pins the unknown-key literal as `config file %s:%d has unknown key %q;
  refusing to start`. The branch's reasoning moved into comments, which is where it reads better
  anyway.

**Left:** T005–T035. Next is T005 🔒 (the mode refusal gated on the file containing a secret),
which is the first of the four security-critical tasks since T001 and the first task that opens a
file rather than being handed bytes.

**Findings:**

- **`ErrConfigFile` was deliberately NOT added, and T009 is where it belongs.** T004 names no
  sentinel and nothing branches on a file error yet; `AGENTS.md` says sentinels are for what
  callers branch on, and the plan's own anti-requirement is that code with no caller is the
  failure this repo has shipped three times. The first real caller is **T009**, which needs to tell
  "your file is wrong" from "your configuration is wrong" to decide whether to fall back to
  `config.bak`. When it is added, `errors.New("config file")` + `fmt.Errorf("%w %s:%d …")`
  reproduces the contract's message shapes exactly; `errors.New("configuration file")` — the
  branch's spelling — does not.
- **The `version < 1` refusal is not in the contract's table.** `contracts/config-file.md` lists
  only "Future schema" and "Bad version", and `version = 0` is a whole number, so the contract as
  written accepts it. The branch refused it and T004 carries that. If this is wrong, it is a
  two-line deletion — but a daemon accepting `version = -3` is reading a schema that does not
  exist. **Worth adding a row to the contract table** rather than leaving the code ahead of it.
- **The contract's "yields exactly eight keys" is still wrong and is now wrong differently.** The
  example sets seven keys, one of which is `version`, which T004 consumes — so the example now
  yields **six settings** plus an accepted schema version. Iteration 3 flagged the count; T034
  writes `config.example` against this same text and will inherit the error if nobody fixes it.
- **`f.values` still has no enumerator, and T012 needs one.** `TestParseAcceptsWorkedExample` can
  assert every expected key is present but cannot assert that *no other* key was invented — it
  proxies with a single `EnvMaxSessions` probe. T012 renders one row per key and will need a real
  accessor; adding it there also closes this test's gap.
- **Lint still UNVERIFIED locally** (v1.62.2 on PATH, #26): `golangci-lint run` silent, `gofmt -l .`
  clean, `go build`/`go vet`/`go test ./...` green, `go vet` green under `-tags tmux`,
  `-tags quickstart` and `-tags dev`, and `go.sum` still absent. The v2 install was not retried —
  the iteration-1 sandbox denial has not changed. CI is the gate.
