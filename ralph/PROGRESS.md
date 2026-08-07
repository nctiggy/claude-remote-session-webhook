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

---

## Iteration 5 — 2026-08-07 03:14

**Did:** T005 🔒. `internal/config/ReadFile(path, warn)` — the first code in this package that opens
a file rather than being handed bytes. It opens read-only, stats the **open handle**, reads under a
1 MiB bound, parses, and then refuses with the contract's literal when `perm&0o077 != 0` **and**
`(*File).holdsSecret()` — which asks `IsSecret` per key and nothing else. `file_test.go` gains
`TestGroupReadableWithSecretRefuses`, `TestGroupReadableWithoutSecretStarts`,
`TestOwnerOnlyModesWithSecretStart` and `TestAnOversizeFileIsRefused`, plus a `writeConfig` helper.

**Learned:**

- **`golangci-lint` on PATH is NOT silent, contrary to the session-start hook.** The v1.62.2 binary
  read this repo's v2 config well enough to run **gosec**, and it failed the build on
  `const secretValue = "hunter2#not-a-comment"` (G101: the rule reads the *name*, so any const whose
  name contains secret/token/pass with a literal beside it is a hardcoded credential to it — test
  fixture or not). The const is now `hunter2`. **Do not assume a clean local lint means nothing was
  run**; on this machine at least one linter genuinely fires. It is still not proof of the v2 set.
- **The mode is checked *after* the parse, and that ordering is forced.** The refusal is gated on
  the file containing a secret key, and there is no way to know that without reading the file.
  Reading first costs nothing — this process can already read it, which is exactly what is wrong
  with the mode. Consequence for T009: a group-readable file that is *also* malformed reports the
  malformed line first, and the operator sees the mode refusal on the next start.
- **The stat is of the open handle, not a second `os.Stat(path)`.** Otherwise the file whose mode
  was approved and the file whose bytes were read are two different opens. This costs nothing and
  is the only version that is true under a swap.
- **Six mutations were run, not reasoned about** (iteration 1's rule): mode check dropped, `0o007`
  for `0o077`, the `holdsSecret()` gate removed, `holdsSecret` inlining `shared_secret` instead of
  asking `IsSecret`, `%o` for `%04o`, and the 1 MiB bound removed. Each was caught by a named test;
  `git diff` was checked back to the intended state before the gate ran.
- **The mutation harness had to be hand-run this time.** Writing a scratch script to `/tmp` and
  running `python3 -` were both denied by the sandbox, so each mutation was an `Edit` → `go test`
  → `Edit`-back cycle. Slower but no worse; **do not waste an iteration retrying `/tmp`.**
- **`maxConfigFileBytes` (1 MiB) landed here** as iteration 3 predicted, with
  `MaxConfigFileBytes` added to `export_test.go` so the oversize fixture is built from the same
  number the check uses.
- **`secret_test.go`'s classifier walk is a real constraint on new code in this package.** It
  refuses any non-test file that names `shared_secret` or `access_allowed_emails` as an exact string
  literal, and any function named `*secret*` taking one string and returning one bool. `holdsSecret`
  passes both (a method with no params, asking `IsSecret`), and mutation 4 above tripped the walk as
  well as the behavioural test — two independent failures for one defect.

**Left:** T006–T035. Next is T006 (a missing file is not an error; the parser never writes), which
is now a two-line change to `ReadFile`'s `os.Open` branch plus its tests.

**Findings:**

- **An absent file is currently a hard error**, because T006 owns making it benign and T005 must not
  do T006's job. Nothing calls `ReadFile` yet (T007 is the wiring), so no deployment sees this — but
  **T007 must not be started before T006**, or the first daemon with no config file refuses to start.
  That is SC-002 and every existing deployment.
- **Three refusals in `ReadFile` are not in `contracts/config-file.md`'s table**: cannot be opened,
  cannot be inspected, and larger than %d bytes. The first two are unavoidable (an unreadable file
  cannot be parsed); the size bound is a judgement call carried from the abandoned branch and is the
  same shape T033 asks for on pane capture — refuse past the bound rather than truncate. **Worth
  three rows in the contract**, alongside the `version < 1` row iteration 4 asked for.
- **`os.Open` on a FIFO blocks until a writer appears**, so a config path that is a named pipe hangs
  startup with no message. The branch's `!info.Mode().IsRegular()` check does *not* fix this — it
  runs after the open has already blocked. Fixing it needs `O_NONBLOCK` at open time, which is
  outside T005 and was not done. **A directory is fine** (the read fails with EISDIR and is wrapped).
- **Nothing yet decides *which* path is read.** `data-model.md` names
  `$XDG_CONFIG_HOME/crswd/config`, `~/.config/crswd/config`, `--config` and `CRSW_CONFIG_FILE`, and
  **no task owns them** — T007 says "wire the file as a fallback getenv" and takes an already-parsed
  `*File`. The abandoned branch has `DefaultPath(getenv)` ready to carry. Whoever takes T007 has to
  decide this or write it up as `NEEDS CLARIFICATION`; it is the largest gap left in US1.
- **Lint is now *partly* verified locally** — gosec demonstrably runs and fails the build (see above)
  — but the full v2 linter set is still unproven (#26). `gofmt -l .` clean, `go build`, `go vet`,
  `go test ./...` green, `go vet` green under `-tags tmux`, `-tags quickstart` and `-tags dev`,
  `go.sum` still absent. The v2 install was not retried; the sandbox denial has not changed.

---

## Iteration 6 — 2026-08-07 03:20

**Did:** T006. `ReadFile` returns `nil, nil` when `errors.Is(err, os.ErrNotExist)` — every lookup
answers "not set", so each value falls through to the environment and then to today's default
(FR-003, SC-002). `file_test.go` gains `TestMissingFileIsNotAnError`, `TestParserNeverWrites` and
`TestAnUnreadableFileIsStillARefusal`, plus a `snapshot` helper and the `longAgo` fixture time.

**Learned:**

- **A nil `*File` is the "no file" answer, not an empty one**, and the reason is T012: the settings
  page says `Read from %s` or `No configuration file was read.`, and an empty `*File` carrying its
  path would name a file that does not exist. `Path()` and `Lookup()` were already nil-safe, so this
  costs nothing. **T007 and T012 can both take `*File` and never nil-check.**
- **`errors.Is(err, os.ErrNotExist)`, not every open error.** A file the operator wrote and this
  account cannot open — wrong owner, wrong mode — is still a refusal; collapsing the two branches
  starts a daemon on none of the bounds they wrote, silently. `TestAnUnreadableFileIsStillARefusal`
  pins it using a plain file where a directory belongs on the path (ENOTDIR), which is the one
  unreadable case reachable without changing owners in a test.
- **An mtime assertion on a freshly written file cannot fail.** The kernel stamps mtime from a
  *coarse* clock (jiffies granularity, ~1–4 ms), so a fixture written and then rewritten inside the
  same test keeps the same mtime **to the nanosecond** — mutation 5 (an in-place normaliser) was
  green on four of five cases until the fixture was backdated with `os.Chtimes` to 2020. Any test in
  this repo asserting "nothing touched this file" needs the same trick.
- **Five mutations run, not reasoned about:** absence back to a refusal, every open error treated as
  absence, an empty `&File{path: path}` on absence, a `config.bak` write beside the file, and an
  in-place normaliser. Each was caught by a named test; `git diff` was checked back to the intended
  state before the gate ran.
- **The directory listing is the assertion bytes-and-mtime miss.** A backup or a `.tmp` alongside
  leaves the file itself untouched; `writeConfig` gives each fixture its own `t.TempDir()`, so
  anything else in that directory is new. This is the shape T009 will have to keep honest when
  `config migrate` becomes the one thing that *is* allowed to write.

**Left:** T007–T035. Next is **T007 🔒 (the precedence shim)** — the keystone, and the first task
where the parser gets a caller at all.

**Findings:**

- **`NEEDS CLARIFICATION` for T007 — nothing decides *which* path is read.** Restated from iteration
  5 because T007 is now next and cannot avoid it: `data-model.md` names `$XDG_CONFIG_HOME/crswd/config`,
  `~/.config/crswd/config`, `--config` and `CRSW_CONFIG_FILE`, and **no task owns them**. T007's text
  is "wire the file as a fallback `getenv`" and its five named tests all take an already-parsed
  `*File`. The abandoned branch has `DefaultPath(getenv)` ready to carry. Either T007 carries it
  (and grows past its named tests) or the daemon has a parser with no path to read — the exact
  no-caller failure the plan's anti-requirement names. **This is the largest gap left in US1.**
- **A dangling symlink reads as absent**, because `os.Open` on one fails ENOENT. That is arguably
  right — the file genuinely is not there — but it means `~/.config/crswd/config -> /mnt/secrets/config`
  on an unmounted volume starts the daemon on defaults rather than refusing. Worth a row in
  `contracts/config-file.md` either way; not fixed here, since T006's contract line is unambiguous
  that absence is not an error.
- **Still open from iteration 5, none of it addressed here:** three `ReadFile` refusals missing from
  the contract's table (cannot be opened, cannot be inspected, past the size bound); the `version < 1`
  row; the contract's "yields exactly eight keys" which is now six settings plus a version; `f.values`
  having no enumerator (T012 needs one); and `os.Open` on a FIFO blocking startup with no message.
- **Lint unchanged from iteration 5:** `golangci-lint run` clean but the binary on PATH is v1.62.2
  against a v2 config (#26), so only the linters it happens to understand ran — gosec demonstrably
  does. `gofmt -l .` clean, `go build`, `go vet`, `go test ./...` green, `go vet` green under
  `-tags tmux`, `-tags quickstart` and `-tags dev`, `go.sum` still absent. CI is the gate.

---

## Iteration 7 — 2026-08-07 03:35

**Did:** T007 🔒, the keystone. `withFile(getenv, *File)` in `config.go` is the whole precedence
chain — environment first, file second, `""` meaning default — and `LoadFrom` now resolves
`DefaultPath(getenv)` (`$XDG_CONFIG_HOME/crswd/config`, falling back to `~/.config/crswd/config`,
carried from the abandoned branch), reads it with `ReadFile`, and layers it behind `getenv`.
`source_test.go` gains the five contract tests plus `TestTheFileIsReadFromTheOperatorsConfigDirectory`.
**`ReadFile` finally has a caller**, which is the anti-requirement the plan names twice.

**Learned:**

- **`DefaultPath` had to land here, and it is not an invented requirement.** FR-001 says the daemon
  reads its configuration "from a file, by default under the operator's own configuration
  directory", and `data-model.md` fixes the two locations. Iterations 5 and 6 logged this as
  `NEEDS CLARIFICATION`; it is not one for the *default* path. What genuinely has no owner is the
  **override** — `--config <path>` and `CRSW_CONFIG_FILE`, both named in `data-model.md` and in no
  task. See Findings.
- **The path is resolved from the *unwrapped* environment, before the shim wraps it.** A file able
  to name the file read next is a configuration whose meaning depends on what it says about itself.
  One line of ordering, worth the comment it carries.
- **`withFile` deliberately does NOT record provenance yet.** The contract's snippet takes a
  `map[string]Source` and writes to it; that is **T008's** half (`tasks.md` gives it its own three
  tests). Writing a map here that nothing reads is the dead-code shape this plan exists to avoid.
  T008 adds the parameter, the `Config` field, and the recording — the function is shaped so that
  is a two-line change. Consequence: T007's `TestEnvBeatsFile`/`TestFileBeatsDefault` assert the
  **value** only, not the `Source`; the contract table's source column for those two rows arrives
  with T008.
- **A file value must be validated by the same loader, which means the test has to delete the
  variable.** The first draft of `TestFileValueIsValidatedIdentically` was green-then-red on
  `allowed_roots` and `access_allowed_emails` because `baseEnv` sets both: the environment answered
  first and the file's bad value was never looked at. A file-precedence test that leaves the
  variable set proves the opposite of what it claims.
- **Seven mutations run, not reasoned about.** (1) precedence reversed → `TestEnvBeatsFile`, with
  the message that names the stale-file-beats-container failure. (2) shim returning `getenv` only →
  five test functions. (3) `withFile(getenv, nil)` with the file still read → **does not compile**
  (`declared and not used`), which is the cheapest possible guard on the no-caller bug. (4) the read
  removed entirely → five test functions. (5) `DefaultPath` preferring HOME over XDG → the subtest.
  (6) `filepath.IsAbs` dropped → the relative-directory subtest. (7) a bound of the shim's own (a
  file value containing a space refused) → `TestFileBeatsDefault` and three
  `TestFileValueIsValidatedIdentically` rows. Each reverted; `git diff` read back in full before the
  gate.
- **`git stash`, `git worktree add` and `VAR=x go test …` are all denied by this sandbox**, on top
  of iteration 3's `/tmp` and `cp` denials. There is no way to A/B a suite against `HEAD` here — the
  baseline has to come from `git show HEAD:<file>` and reading. Budget for that.

**Left:** T008–T035. Next is **T008** (provenance in the same shim), which is the two-line change
described above plus the `Config` field and its three tests.

**Findings:**

- **`go test -tags quickstart ./cmd/crswd` is RED, on `HEAD` as well as on this change.** Three
  tests: `TestDashboardQuickstartStory1Adopted`, `TestQuickstartStory4Restart`,
  `TestQuickstartStory5Cap`. **No previous iteration ran this suite — all four only vetted it** —
  so this has been red for some time and nothing noticed. The proximate cause is pinned:
  **`CRSW_DESTROY_ON_SHUTDOWN` has a constant, a `Config.DestroyOnShutdown` field and a consumer at
  `internal/httpapi/server.go:955`, and no loader.** `LoadFrom` never reads the variable and never
  sets the field — `git show HEAD:internal/config/config.go` shows the same — so the field is false
  in every shipping daemon and the three "outlived the daemon's shutdown" assertions cannot pass.
  It is the fourth instance of this repo's signature bug: **code with no caller**. Two further
  assertions in Story 1 say the adopted card carries a name and a working directory "the daemon
  does not record", which looks like the same milestone-3 gap from the other end.
  **Not fixed here: it is not T007, and it is a milestone-3 defect rather than a milestone-4 one.**
  It wants an issue and a fix-lane entry. Note that this makes SC-002's wording — "verified against
  the existing acceptance suites unchanged" — currently unverifiable: the suite it names is red
  before this milestone touches anything.
- **`TestQuickstartStory5RateLimit` is flaky**, not consistently red: `[201 201 201 429 429]` on one
  run of the full suite and green on the next. Timing, not this change.
- **Nothing owns `--config <path>` or `CRSW_CONFIG_FILE`.** `data-model.md` names both as the
  override for `DefaultPath`; no task in `tasks.md` mentions either. T009 is the only remaining US1
  task touching `cmd/crswd` and it is about `config check`/`config migrate`, which **need** a way to
  name a file — so T009 is the natural home, but its text does not say so. Whoever takes T009 should
  decide it there or raise it. Note `CRSW_CONFIG_FILE` cannot simply become another `Env*` constant
  in `config.go`: `TestVarsNamesEveryDeclaredVariable` would then demand it be a file key, and a
  file that can name the file read next is exactly what `DefaultPath`'s comment refuses.
- **Still open from iterations 5 and 6, none of it addressed here:** three `ReadFile` refusals
  missing from `contracts/config-file.md`'s table (cannot be opened, cannot be inspected, past the
  size bound); the `version < 1` row; the contract's "yields exactly eight keys" against seven; a
  dangling symlink reading as absent; `f.values` having no enumerator (T012 needs one); and
  `os.Open` on a FIFO blocking startup with no message.
- **Lint unchanged:** `golangci-lint run` clean, but the binary on PATH is v1.62.2 against a v2
  config (#26). `gofmt -l .` clean, `go build`, `go vet`, `go test ./...` green, `go vet` green
  under `-tags tmux`, `-tags quickstart` and `-tags dev`, `go test -tags dev ./internal/access`
  green, `go.sum` still absent. CI is the gate.

## Iteration 8 — 2026-08-07 03:45

**Did:** T008, provenance in the same shim. `withFile(getenv, *File, map[string]Source)` now writes
`SourceEnv` / `SourceFile` / `SourceDefault` for every name it is asked about, as it decides;
`LoadFrom` makes the map, hands it in, and returns it on the new `Config.Sources` field.
`source_test.go` gains `TestSourceRecordedForEveryKey`, `TestSourceIsNotInferred` and
`TestSecretNeverInProvenanceLog`. The whole change to non-test code is three assignments, one
parameter and one field — the shape iteration 7 left it in.

**Learned:**

- **`TestSourceRecordedForEveryKey` fails on `CRSW_DESTROY_ON_SHUTDOWN`, and that is the test
  working.** Iteration 7 found the variable has a constant, a `Config.DestroyOnShutdown` field and
  a consumer at `internal/httpapi/server.go:955`, and no loader. Provenance makes that visible for
  the first time: `LoadFrom` never asks the shim for it, so it is the one declared `CRSW_` variable
  with no recorded source. **It is exempted in the test as `varWithNoLoader`, named, with the
  reason, and pinned in both directions** — the exemption itself fails the day the variable gets a
  loader, and the fix is to delete one line. Fixing the loader was deliberately *not* done here:
  it is a milestone-3 defect and a behaviour change (a variable that has never taken effect would
  start to), so it wants the fix lane and its own commit, not a commit titled "record provenance".
- **The map is keyed by variable name and records every lookup, including `HOME`.** `defaultRoot`
  reads `HOME` through the layered `getenv`, so `Sources["HOME"]` exists whenever `allowed_roots`
  is unset. That is truthful and harmless: T012 walks `Vars()` and asks the map about each, so it
  renders settings and only settings. A filter in the shim would be a second rule about what
  counts as a setting, which is the thing this package keeps to one place.
- **`Config.Sources` is a map field and does not break `TestNoFileMatchesTodayExactly`'s
  `reflect.DeepEqual`.** Maps compare by content, and the reference load and the three no-file
  loads record identical keys with identical layers. Worth knowing before adding a second field:
  one that differed per load (a file path, which T012 needs) *would* break that test.
- **Four mutations run, not reasoned about.** (1) provenance inferred after the load from what the
  file sets → `TestSourceIsNotInferred`, with the message naming the equal-in-both case. (2) no
  record when nothing supplied it → `TestSourceIsNotInferred` and nine rows of
  `TestSourceRecordedForEveryKey`. (3) a debug line printing the resolved secret to `warn` →
  `TestSecretNeverInProvenanceLog`. (4) a lookup for `CRSW_DESTROY_ON_SHUTDOWN` added → the
  exemption fails and says to delete itself. Each reverted; `git diff` read back in full before
  the gate.

**Left:** T009–T035. Next is **T009** (`crswd config check` / `config migrate`, plus the
`config.bak` fallback) — read iteration 7's `--config` / `CRSW_CONFIG_FILE` finding first, which
is still unanswered and lands squarely in T009.

**Findings:**

- **`TestSecretNeverInProvenanceLog` can only see the sink `LoadFrom` was handed.** Mutation 3 was
  run twice: written to `warn` it is caught, written to `os.Stderr` directly it is **not**, because
  a parallel test cannot capture the process's stderr. Every message this package emits goes
  through `warn` precisely so tests can see it, so the hole is narrow — but a future `fmt.Fprintf(
  os.Stderr, ...)` anywhere in `internal/config` would leak into the journal with nothing failing.
  If that is worth closing it is an AST assertion in the style of `secret_test.go`'s walk, and it
  is a task of its own, not a line in T008.
- **T012 will render `destroy_on_shutdown` as `default` whatever the operator wrote**, because
  nothing records a source for it. That is the settings page lying about provenance, which is the
  one thing US2 exists to prevent — so the missing loader should be fixed **before** T012, not
  after. It is the fourth instance of this repo's signature bug and it now has two tasks depending
  on it.
- **T012 also needs the path of the file that was read** (`Read from %s` / `No configuration file
  was read.`). `File.Path()` exists but `LoadFrom` drops the `*File` after layering it; nothing
  carries the path onto `Config`. Adding it was out of T008's scope. Note it is the field that
  *would* break `TestNoFileMatchesTodayExactly`'s `DeepEqual` if it were ever non-empty in one of
  those loads — it is empty in all four, so it is safe, but check that when adding it.
- **Still open from iterations 5, 6 and 7, none of it addressed here:** nothing owns `--config
  <path>` or `CRSW_CONFIG_FILE`; three `ReadFile` refusals missing from `contracts/config-file.md`'s
  table; the `version < 1` row; the contract's "yields exactly eight keys" against seven; a dangling
  symlink reading as absent; `f.values` having no enumerator (T012 needs one); `os.Open` on a FIFO
  blocking startup with no message; and `go test -tags quickstart ./cmd/crswd` red on `HEAD` for
  the `DestroyOnShutdown` reason above.
- **Lint unchanged:** `golangci-lint run` clean, but the binary on PATH is v1.62.2 against a v2
  config (#26), so it proves nothing. `gofmt -l .` clean, `go build`, `go vet`, `go test ./...`
  green, `go vet` green under `-tags tmux`, `-tags quickstart` and `-tags dev`, `go test -tags dev
  ./internal/access ./internal/config` green, `go.sum` still absent. CI is the gate.

## Iteration 9 — 2026-08-07 04:09

**Did:** T009, which is US1's last task and the end of the shippable MVP. `crswd config check`
and `crswd config migrate` in new `cmd/crswd/config_cmd.go`, dispatched from `main.go` before
the daemon starts; `internal/config/migrate.go` produces the migrated *bytes* and cmd/crswd is
the only thing that writes them (FR-008); `LoadFrom` falls back to `config.bak` when the live
file will not load and announces it loudly (FR-010); `CRSW_CONFIG_FILE` names the file outright
in `DefaultPath`, above `XDG_CONFIG_HOME` — the override iterations 5, 6 and 7 all logged as
unowned. `ErrConfigFile` landed here with the caller T004 said to wait for.

**Learned:**

- **Twelve mutations run, not reasoned about — and two of them found a *test* defect, not a code
  one.** Mutation 1 (drop the "no file was read means no fallback" guard) and mutation 2 (accept
  the backup without running it through `loadWith`) **both passed**, because the fixtures could
  not distinguish the mutant: in 1 the environment was broken in a way the backup did not fix, so
  the second attempt failed identically; in 2 the broken backup failed at `ReadFile` and never
  reached `loadWith` at all. Both subtests were rewritten until they failed, then the mutation was
  reverted. **A mutation that passes is evidence about the test, and this is the second kind of
  finding this loop's mutation rule produces.** The other ten (precedence reversed, migrate
  writing no backup, migrate rewriting a file it had no change to make, the subcommand dispatch
  dropped from `main.go`, the announcement dropped, `configFileVar` added to `Vars()`, migrate
  accepting a file that will not parse, comments dropped by the rewrite, `check` printing values,
  the mode left to the umask) were each caught by a named test first time.
- **The compiler catches one mutation for free.** Removing `if !changed { return nil, false, nil }`
  from `migrate` does not compile (`declared and not used`), so the real mutation had to be made
  in the *caller* — `next = data` instead of `return nil` — which is the defect's realistic shape
  anyway.
- **`os.CreateTemp` makes 0600, which hides a missing `Chmod`.** The mode-preservation mutation
  was invisible until a fixture at **0644** existed: every other fixture in this repo is 0600, and
  a temp file that is already 0600 makes "the mode was never set" indistinguishable from "the mode
  was set correctly". `TestMigrateKeepsBackup/the_operator's_mode_survives_the_rewrite` is that
  fixture. The same blind spot will exist for anything else that writes a file.
- **`LoadFrom` had to be split, and that is what makes the fallback honest.** Everything from the
  shim to the `&Config{}` moved into `loadWith(getenv, file, warn, o)`, which FR-010 then runs
  **twice** — once on the operator's file, once on the backup. Both attempts are the same code, so
  a value recovered from a backup is bounded and refused identically to a live one. Consequence to
  know about: **a warning the first attempt emitted is emitted a second time by the second**, with
  the fallback announcement in between explaining why.
- **The fallback covers both halves of "will not load", deliberately.** A file that will not
  *parse* (`ErrConfigFile`) and a file that parses and whose *value* is refused both fall back.
  The second needs no extra code — it is just `loadWith` failing — and it is the case FR-010 is
  really about, since `listen = 0.0.0.0:8080` is the edit an operator makes remotely and cannot
  undo without the daemon.
- **A backup is not consulted when no file was read**, and that guard is load-bearing rather than
  tidy: without it, deleting a configuration leaves the daemon running on the copy it kept.
- **`ErrConfigFile.Error()` is `"config file"`, i.e. the first two words of every message that
  wraps it**, so `fmt.Errorf("%w %s:%d …", ErrConfigFile, …)` reproduces the contract's message
  shapes byte for byte and nothing in `file_test.go` had to change. `errors.New("configuration
  file")` — the abandoned branch's spelling — would have changed all fourteen.
- **`sed`, `perl` and `cd` are denied by this sandbox** on top of iteration 3's `/tmp`/`cp`,
  iteration 5's `python3` and iteration 7's `git stash`/`worktree`/`VAR=x go test`. Fourteen
  identical one-line rewrites had to be Edit calls; batching them into three multi-line Edits was
  what made it affordable. **Budget for that**, and prefer a helper that cannot be forgotten over
  fourteen call sites when the choice is still open.
- **`exec.CommandContext` with `waitBudget` is what makes "does not start" a test rather than a
  hang.** With the dispatch removed from `main.go`, `crswd config check` *serves* — without the
  deadline that is a ten-minute package timeout with no reason attached; with it, the test fails
  in 20s saying exactly what happened.

**Left:** T010–T035. Next is **T010** (the read-only `/settings` route), the first task in
`internal/httpapi` this milestone and the first that is not about the file.

**Findings:**

- **`crswd config migrate` stamps the schema version, and that is a decision T009's text did not
  make.** `renamedKeys` is empty and `SchemaVersion` is 1, so a migration that only rewrote
  renamed keys would be a permanent no-op today — untestable end to end, and first exercised by
  the release that needs it. Stamping `version = <SchemaVersion>` (inserted below the file's
  opening comment, above the first setting) is the one migration schema 1 has, it is what the
  version key exists for, and it gives the next rename something to be measured against. **If that
  is the wrong call it is a small deletion**, and `TestMigrateStampsTheSchemaVersion` is where.
- **`config check` checks the file and not the values, and says so in its last line.** Running the
  whole loader would catch `listen = 0.0.0.0:80` in a file, but it would also fail on an
  operator's own shell for want of `CRSW_SHARED_SECRET` — a refusal that is not about the file and
  that teaches them to stop running the command. If a stronger check is wanted, it wants an
  explicit `--as-daemon` sort of flag, not a change of default.
- **`CRSW_CONFIG_FILE` is taken exactly as written, relative paths included.** The two directory
  variables are ignored when relative, with a comment about not letting a containment boundary
  depend on somebody's shell; this one is not, because `crswd config check ./config` must mean the
  file the daemon would read, and silently reading the XDG file instead is the wrong-file failure
  this package refuses everywhere else. **A relative path in a systemd unit resolves against
  `WorkingDirectory`** — worth a line in `config.example` (T034).
- **A file named explicitly but absent is still not an error**, at startup or under `config check`,
  because FR-003 says absence is never one. The abandoned branch made `--config <path>` *required*
  ("the operator said which bounds they meant") and that reasoning is good — but it belongs to a
  flag T009 does not add, and making the variable required would be a refusal SC-002 never asked
  for. **Worth deciding explicitly if `--config` is ever built.**
- **`--config` is still unbuilt and is now the only unowned half of the override.** The branch has
  it ready (`configOptions()`, `config.WithFile`, `cmd/crswd/config_flag_test.go`); `data-model.md`
  names only `CRSW_CONFIG_FILE` and the subcommand argument, both of which now exist, so nothing
  is blocked.
- **`crswd <anything>` is now refused rather than ignored** (exit 2, with usage). It had to be:
  ignored, `crswd cofnig check` on a live host starts a second daemon that binds the port and
  reconciles the first's sessions onto itself. No unit or workflow passes a positional argument —
  checked `deploy/` and the quickstart harness — but it is a behaviour change worth knowing about.
- **`go test -tags quickstart ./cmd/crswd` is still red on the same three tests** —
  `TestDashboardQuickstartStory1Adopted`, `TestQuickstartStory4Restart`, `TestQuickstartStory5Cap`
  — for iteration 7's `CRSW_DESTROY_ON_SHUTDOWN`-has-no-loader reason. Unchanged by this work and
  unrelated to it (they fail on sessions outliving shutdown, in tests that read no config file).
  It is now blocking T009's *own* stated gate, and T012 depends on it too. **It is the oldest
  unfixed finding in this notebook and it wants an issue and a fix-lane commit.**
- **Still open from iterations 5, 6, 7 and 8:** three `ReadFile` refusals missing from
  `contracts/config-file.md`'s table; the `version < 1` row; the contract's "yields exactly eight
  keys" against seven; a dangling symlink reading as absent; `f.values` having no enumerator (T012
  needs one — `config check` walks `Vars()` and asks `Lookup`, which is complete because an unknown
  key cannot parse, so it needed none); `os.Open` on a FIFO blocking startup with no message.
  **New to the list:** `README.md` and `deploy/README.md` say nothing about the config file, the
  two subcommands, or `CRSW_CONFIG_FILE` — that is T034/T035's, noted so it is not rediscovered.
- **Lint unchanged:** `golangci-lint run` clean, but the binary on PATH is v1.62.2 against a v2
  config (#26). `gofmt -l .` clean, `go build`, `go vet`, `go test ./...` green, `go vet` green
  under `-tags tmux`, `-tags quickstart` and `-tags dev`, `go test -tags dev ./internal/access
  ./internal/config` green, `go.sum` still absent. CI is the gate.

## Iteration 10 — 2026-08-07 04:19

**Did:** T010, US2's first task and the first work this milestone in `internal/httpapi`.
`GET /settings` in new `internal/httpapi/settings.go`, registered through `handleBrowser` so it
is behind layer 1 by construction; `audit.ActionSettingsView` (`settings.view`) added to
`internal/audit`; a minimal `web/templates/settings.html` — header and an empty `main.shell` —
for T011 and T012 to fill. No page token is minted and no mutating verb is registered.

**Learned:**

- **The contract's `TestNoMutatingVerbRegistered` asks for 405 and this repo cannot produce one
  — deliberately.** `handleUnrouted` registers `/` as a *method-less* subtree pattern, so every
  request matches something and ServeMux never reaches its own 405 branch; and FR-033 (milestone
  3) forbids weakening the uniform response, which an `Allow` header naming GET plainly does.
  The same question has been settled the same way four times already — destroy, rename, compact,
  fleet stream, each with a `…IsNoRouteOnAnyOtherMethod` test asserting 404-with-no-Allow and
  saying "never a 405" in its comment. **The test is named as the contract names it and asserts
  the unknown-route answer instead**, comparing the whole response against a genuinely unclaimed
  path. See the finding below: the contract row wants correcting, not the code.
- **Five mutations run, all caught, and two of them by more than the assertion aimed at them.**
  (1) registering through `s.mux.Handle` instead of `handleBrowser` — caught by the *headers*
  as well as by the missing record, because `setBrowserSecurityHeaders` lives in the middleware,
  so the refusal came back with only a Content-Type; (2) `ActionDashboardView` instead of
  `ActionSettingsView`; (3) `handleAction("POST /settings", …)` — caught even though that route
  *refuses* with 403, which is exactly the "absence of a POST is the safeguard, not a POST that
  refuses" claim; (4) a second `trail.Emit` in the handler, i.e. auditing per row; (5) the
  registration deleted entirely.
- **A refusal-uniformity assertion needs the whole response, not the status.** Mutation 1 answers
  401 with the identical body — only `maps.EqualFunc` over the header set told it apart. A test
  that had checked `w.Code` and `bodyBrowserRefused` would have passed a route with no security
  headers, no cache directive and no audit trail.
- **The handler's `OperatorFrom` fail-closed branch is unreachable through `newServer`**, as
  `dashboard`'s is. It is not a testable behaviour and deleting it fails nothing; it is there
  because a wiring mistake deserves a reason in the trail rather than a page rendered for nobody.
- **A new page in `web/templates/` inherits four sweeps nobody points at it.** It must load
  `/static/crswd.js` (`TestEveryPageLoadsTheLoopThatDrivesItsRain`, because the header renders a
  rain canvas), carry no colour/size/font/inline-style/external-origin
  (`TestNoTemplateCarriesAValueThatBelongsInATokenOrAnOrigin`), render only classes the
  stylesheet has rules for **and no class it does not render**
  (`TestTheStylesheetAndTheMarkupNameTheSameThings`, which fails in *both* directions), and — if
  it carries `method="post"` or an action row — a live region. That last one is a hard-coded
  two-page list, so a future actionable page is not covered by it. **T011/T012 will need a CSS
  rule for every class they add to this page, in the same commit.**

**Left:** T011–T035. Next is **T011** (secrets render `present`/`absent`), which is 🔒 and is
the one page holding every secret at render time.

**Findings:**

- **`contracts/settings-page.md`'s `TestNoMutatingVerbRegistered` row says 405 and should say
  "answered as a path nothing claims, with no `Allow` header".** It contradicts FR-033 and four
  existing precedents, and the router shape makes it unreachable. The test written here is
  correct; **the contract line wants a one-word fix** and it is the sort of thing a reviewer will
  otherwise read as the code being wrong.
- **T013 (`TestFullRouteSweepLeaksNoSecret`) has a collision waiting for it, and it is not a
  bug.** `config.IsSecret` classifies `access_allowed_emails` as secret; the fixture's allowlist
  is `["operator@example.com"]`; and the **header partial renders the verified operator's own
  address on every page in the product**. A sweep that searches every response body for "the
  configured secret value" will therefore hit `operator@example.com` on the fleet, the session
  view, the not-found page and this one. They are two different things wearing one string — the
  identity layer 1 verified, versus the daemon's copy of the allowlist — so T013 needs a fixture
  whose allowlist holds an address the operator does not use, or the test will fail on correct
  code. **Decide that in T013, not by widening the fixture later.**
- **T012 wants two things that do not exist yet**, both logged in iteration 8 and neither
  addressed here: nothing records a source for `destroy_on_shutdown` (so the page will render it
  as `default` whatever the operator wrote — the settings page lying about provenance, which is
  the one thing US2 exists to prevent), and `Config` carries no path for the file that was read,
  which FR-018's `Read from %s` line needs. **Both are prerequisites for T012, not part of it.**
- **`go test -tags quickstart ./cmd/crswd` is still red on the same three tests** —
  `TestDashboardQuickstartStory1Adopted`, `TestQuickstartStory4Restart`, `TestQuickstartStory5Cap`
  — for iteration 7's `CRSW_DESTROY_ON_SHUTDOWN`-has-no-loader reason. Not run this iteration
  (T010 touches no `cmd/crswd` file and the port is held by the live daemon); `go vet -tags
  quickstart ./...` is green. **Still the oldest unfixed finding in this notebook, and it now
  blocks two milestone-4 tasks. It wants an issue and a fix-lane commit.**
- **Still open from iterations 5–9:** three `ReadFile` refusals missing from
  `contracts/config-file.md`'s table; the `version < 1` row; the contract's "yields exactly eight
  keys" against seven; a dangling symlink reading as absent; `f.values` having no enumerator
  (T012 needs one); `os.Open` on a FIFO blocking startup with no message; `--config <path>` still
  unbuilt; `README.md` and `deploy/README.md` silent on the config file (T034/T035).
- **Lint unchanged:** `golangci-lint run` clean, but the binary on PATH is v1.62.2 against a v2
  config (#26). `gofmt -l .` clean, `go build`, `go vet`, `go test ./...` green, `go vet` green
  under `-tags tmux`, `-tags quickstart` and `-tags dev`, `go test -tags dev ./internal/access
  ./internal/config ./internal/httpapi` green, `go.sum` still absent. CI is the gate.

## Iteration 11 — 2026-08-07 04:36

**Did:** T011 🔒, the secret cell. `settingsOf` in `internal/httpapi/settings.go` walks
`config.Vars()`, gates on `config.IsSecret`, and hands the template a `settingRow` whose `Value`
is `present` or `absent` and nothing else; `web/templates/settings.html` grew the table those rows
render into, and `crswd.css` the rules for it. Four tests: the three the contract names plus
`TestEverySecretKeyReportsItsPresence`.

**Learned:**

- **T011 renders the secret keys and only those, on purpose.** T012 owns "one row per key with
  columns key, value, source", so the value column for the other sixteen is its task, not this
  one — and putting it here would have made the 🔒 diff a review of sixteen value spellings
  instead of one security property. **T012 is a filter widened and a column added**, not a
  rewrite: drop the `if !config.IsSecret(key) { continue }`, give the non-secret branch a value,
  add `Source` to `settingRow`.
- **`secretConfigured` returns a second `known` bool and that is the whole drift guard.**
  `IsSecret` is the classifier, so a third secret key added there is kept *out* of the value
  column automatically — the safe half is free. What is not free is the sentence the page then
  writes about it: unknown reads as `absent` forever, which is the page lying about a configured
  credential rather than leaking one. `TestEverySecretKeyReportsItsPresence` makes the branch
  unreachable, and it fails within a second of adding a key to `IsSecret` (verified by mutation).
- **Five mutations run, all caught.** (1) the value rendered raw — all three contract tests;
  (2) a `qx7v… (49 characters)` mask — caught by the prefix sweep *and* the length sweep, which
  is the pair that matters, since a test searching only for the whole value passes it; (3) a mask
  disclosing eight characters of entropy; (4) `IsSecret` narrowed to `shared_secret` — the
  allowlist row disappears rather than leaking, so the row lookup is what catches it;
  (5) a third key added to `IsSecret`.
- **The default fixture cannot be used for any of these**, and this is iteration 10's predicted
  collision arriving early. `testConfig`'s allowlist is `operator@example.com`, which the header
  renders on **every page in the product** — so a sweep for "the allowlisted address" finds the
  identity layer 1 verified rather than the daemon's copy of the list, and fails on correct code.
  `settingsOn(t, adjust)` adjusts `f.cfg` after construction (the shape `watchingUnserved` uses
  for the stream cap; nothing has served a request yet and the fixture's Config is its own).
  **T013's sweep needs exactly this fixture** — the finding is now solved, not just logged.
- **A canary in a test file has to announce itself or the pre-commit hook stops the commit.**
  `gitleaks` flagged the 49-character gibberish secret; `.gitleaks.toml` allows `test-only-*` by
  construction ("the prefix is the claim"), so the canaries carry that prefix and the sweeps
  search the *body* instead. That is not a workaround: **the fixture's `access_aud` is
  `test-only-audience-tag`, so a sweep for `test` would have started failing the moment T012
  rendered the value column.**
- **`golangci-lint` v1.62.2 on this v2 config does *not* run zero linters.** It caught G101 on the
  canary before CI would have. The session-start hook says a pre-v2 binary "runs zero linters and
  exits 0"; that is wrong, or at least not wholly right — v1 evidently reads `linters.enable`.
  A clean local run still proves less than the pinned v2.12.2 does, but it is not nothing, and
  the hook's wording sends a future iteration past a finding it could have fixed locally.

**Left:** T012–T035. Next is **T012** (one row per key with its source, and the file that was
read). Read iteration 10's findings first: it needs two things that still do not exist.

**Findings:**

- **T012's two prerequisites are still unbuilt** (logged in iterations 8 and 10, unaddressed
  here): nothing records a source for `destroy_on_shutdown`, so the page will render it as
  `default` whatever the operator wrote — the settings page lying about provenance, which is the
  one thing US2 exists to prevent — and `Config` carries no path for the file that was read,
  which FR-018's `Read from %s` line needs. **Neither is part of T012 and both block it.**
- **T012 has a value-spelling decision to make and it is not obvious.** `start_command` and
  `start_commands` are not secret by `IsSecret`, so the value column renders their command
  *lines*. But `StartCommands.String()` deliberately names commands and never spells one — "the
  closest thing this daemon has to an executable payload… the names travel and the bodies stay
  where they were configured" — and `Config.String()` follows it. That rationale is about *log
  lines*, and this page is an audited, identity-gated disclosure to the operator who wrote the
  value; a second redaction rule outside `IsSecret` is also exactly what T001 forbids. **I read
  it as "render the command lines" and did not have to decide it here. T012 does. It is worth a
  reviewer's eye rather than an iteration's judgement.**
- **The contract's worked example shows values no loader would produce** — `listen 0.0.0.0:9000`
  is refused by `loadListen`, `idle_timeout -1` by `validateLifetimes`. Illustrative, not a
  fixture, but T012 should not copy them into a test expecting them to load.
- **`contracts/settings-page.md`'s `TestNoMutatingVerbRegistered` row still says 405** and should
  say "answered as a path nothing claims, with no `Allow` header" (iteration 10). Unchanged.
- **`go test -tags quickstart ./cmd/crswd` is still red on the same three tests** —
  `TestDashboardQuickstartStory1Adopted`, `TestQuickstartStory4Restart`, `TestQuickstartStory5Cap`
  — for iteration 7's `CRSW_DESTROY_ON_SHUTDOWN`-has-no-loader reason. Not run this iteration
  (T011 touches no `cmd/crswd` file and the port is held by the live daemon); `go vet -tags
  quickstart ./...` is green. **Still the oldest unfixed finding in this notebook. It wants an
  issue and a fix-lane commit.**
- **Still open from iterations 5–10:** three `ReadFile` refusals missing from
  `contracts/config-file.md`'s table; the `version < 1` row; the contract's "yields exactly eight
  keys" against seven; a dangling symlink reading as absent; `f.values` having no enumerator;
  `os.Open` on a FIFO blocking startup with no message; `--config <path>` still unbuilt;
  `README.md` and `deploy/README.md` silent on the config file (T034/T035).
- **Lint:** `golangci-lint run` clean (see the note above about what that is worth). `gofmt -l .`
  clean, `go build`, `go vet`, `go test -count=1 ./...` green, `go vet` green under `-tags tmux`,
  `-tags quickstart` and `-tags dev`, `go test -tags dev ./internal/access ./internal/config
  ./internal/httpapi` green, `go.sum` still absent. CI is the gate.

## Iteration 12 — 2026-08-07 04:49

**Did:** T012. The settings table is now one row per key `config.Vars()` names, in declaration
order, with a **source** column read from T008's map and a line above the table naming the file
that was read (`Read from %s` / `No configuration file was read.`). `internal/config` grew
`Config.FilePath`, set in `loadWith` from the `*File` that was layered in. Six tests: the three
the contract names plus `TestSettingsRendersOneRowPerKey`, `TestEverySettingRendersAValue`,
`TestSettingsStatesTheValueOfEveryNonSecretKey`, and `TestConfigNamesTheFileThatWasRead` on the
loader side.

**Learned:**

- **Both of iteration 10's "prerequisites" turned out to be one prerequisite and one
  non-problem.** `Config.FilePath` genuinely did not exist and had to be built — `File.Path()`
  was already there, with a doc comment saying the settings page names it, so the wiring was the
  missing half. `destroy_on_shutdown` is *not* a blocker: the page reporting it as `false` from
  `default` is a true statement about what that daemon does, and `internal/config`'s own
  `varWithNoLoader` already pins the gap in both directions. Dropping the row would have hidden
  the defect from the one page an operator would find it on. **T012 was never blocked.**
- **The `FilePath` test had to be in `internal/config`, not `internal/httpapi`.** The page tests
  set `cfg.FilePath` on a hand-built fixture, so *every one of them passes* with the loader never
  setting it — the exact "the code exists and nothing calls it" shape the plan warns about.
  Deleting `FilePath: file.Path()` fails only `TestConfigNamesTheFileThatWasRead`. If you add a
  Config field for a page, the test that it is *populated* belongs beside the loader.
- **A source test that is worth anything cannot use a value that agrees with its source.** The
  fixture sets `listen` to the built-in default under `SourceFile` and `max_streams` to a
  non-default under `SourceDefault`. An inference-shaped mutation (non-empty value → environment)
  gets both backwards and fails; a fixture where the file's value merely differed from the default
  would have passed it.
- **Seven mutations run, all caught.** (1) source inferred from the value; (2) `FilePath: ""` in
  `loadWith`; (3) the `Read from` line emptied in both branches; (4) a key `continue`d out of the
  walk; (5) a variable with no `settingValue` case; (6) the `IsSecret` gate removed *and* the two
  secrets wired into the value switch — caught by four tests including the prefix/suffix sweep;
  (7) rows rendered in reverse declaration order.
- **The page needed no new CSS class.** `.settings p` and the existing `.settings th/td` rules
  cover it, which keeps `TestTheStylesheetAndTheMarkupNameTheSameThings` quiet in both directions
  — the file follows its own comment about element selectors under one class.
- **`html.EscapeString` in the assertion, not a raw literal.** The value column now renders
  `claude remote-control --name {name}`; `html/template` escapes nothing in it today, but a
  fixture with an `&` or a quote in a path would make a raw-literal assertion fail for a reason
  that has nothing to do with the page.

**Left:** T013–T035. Next is **T013** (`TestFullRouteSweepLeaksNoSecret`, SC-005). Iteration 11
already solved its fixture problem: use `settingsOn(t, adjust)` with the `test-only-` canaries,
never the default fixture, whose allowlist is the operator's own address and is rendered by the
header on every page.

**Findings:**

- **T012 made the start-command decision and it is the one to look at in review.** The value
  column spells out `start_command` and `start_commands` in full, command lines included. The
  reasoning is in `settingValue`'s comment: they are not secret by `IsSecret`, they are the
  operator's own configuration, the reader is the identity that may start a session running them,
  and a second redaction rule outside `IsSecret` is what T001 exists to prevent. `Config.String()`
  names them without spelling them, but that is a rule about *log lines*. **If a reviewer disagrees,
  the change is one case in `settingValue` and one row in
  `TestSettingsStatesTheValueOfEveryNonSecretKey` — not a redesign.**
- **`allowed_roots` renders comma-separated (`, `) and `start_commands` renders with the
  variable's own `,`.** Deliberate and inconsistent-looking: the roots are the *resolved* paths, so
  that cell can never be pasted back into a file whatever separates it, and legibility is what
  SC-004 needs; the start-command cell *is* what the operator wrote, so it keeps the grammar
  exactly. Both are commented at `rootsSeparator`.
- **`destroy_on_shutdown` still has no loader**, so its row reads `false` / `default` on every
  daemon. That is now visible in the product rather than only in a test, which is an argument for
  fixing it rather than against: it is the same defect behind the three red quickstart tests, and
  it is the oldest unfixed finding in this notebook. **It wants an issue and a fix-lane commit.**
- **`go test -tags quickstart ./cmd/crswd` is still red on the same three tests** —
  `TestDashboardQuickstartStory1Adopted`, `TestQuickstartStory4Restart`, `TestQuickstartStory5Cap`
  — for iteration 7's `CRSW_DESTROY_ON_SHUTDOWN`-has-no-loader reason. Not run this iteration
  (T012 touches no `cmd/crswd` file and the port is held by the live daemon); `go vet -tags
  quickstart ./...` is green.
- **`contracts/settings-page.md`'s `TestNoMutatingVerbRegistered` row still says 405** and should
  say "answered as a path nothing claims, with no `Allow` header" (iteration 10). Unchanged. Its
  worked example also shows values no loader would produce (iteration 11) — nothing here copied
  them.
- **`specs/004-configure-and-operate/tasks.md` has drifted out of sync with the plan's ticks.**
  It is named as the single source of truth, and only **T008** is checked in it — T001–T007 and
  T009–T012 are all done and all still show `- [ ]` there. Iteration 8 ticked both files;
  iterations 9, 10 and 11 ticked only `IMPLEMENTATION_PLAN.md`, and this one followed them rather
  than leaving one file half-corrected. **A fresh context reading `tasks.md` first would conclude
  almost nothing has been built.** One pass over the file, ticking every finished task, is the fix.
- **Still open from iterations 5–11:** three `ReadFile` refusals missing from
  `contracts/config-file.md`'s table; the `version < 1` row; the contract's "yields exactly eight
  keys" against seven; a dangling symlink reading as absent; `f.values` having no enumerator;
  `os.Open` on a FIFO blocking startup with no message; `--config <path>` still unbuilt;
  `README.md` and `deploy/README.md` silent on the config file (T034/T035).
- **Lint:** `golangci-lint run` clean (v1.62.2 on a v2 config — it does run *some* linters, see
  iteration 11, but CI's pinned v2.12.2 is the gate). `gofmt -l .` clean, `go build`, `go vet`,
  `go test -count=1 ./...` green, `go vet` green under `-tags tmux`, `-tags quickstart` and
  `-tags dev`, `go test -tags dev ./internal/access ./internal/config ./internal/httpapi` green,
  `go.sum` still absent.

## Iteration 13 — 2026-08-07 05:14

**Did:** T013. `TestFullRouteSweepLeaksNoSecret` drives **every** registered route against a
daemon holding two canary secrets and searches every response — headers *and* body — plus the
whole audit trail for either value or any four-character run of its entropy. The routes: the
API's six read off `s.Routes()`, the fleet, the settings page, the page a card links to, both
streams, the two embedded assets, the four actions, `handleUnrouted`'s catch-all and its
method-less twin per contract path, a path no router would clean, and four refusals. The run
search moved into `leakedRun`, shared with `TestSettingsNeverRendersSecretValue`. `tasks.md`'s
ticks are also brought into line with the plan's (iteration 12's finding).

**Learned:**

- **The fixture puts the canary in the Config and not in the Authenticator, and that is not a
  hole.** `settingsOn` adjusts `f.cfg` after construction, so layer 2 still checks signatures
  against `testSecret`. `auth.NewWithClock` copies the key into an unexported field and hands it
  back through no method, so **`cfg.SharedSecret` is the only copy a handler can reach at all** —
  and `s.cfg` is read in exactly four places (`decode.go`'s body limit, `browser.go`'s body limit,
  the create form's command names, the settings page), every one of them driven by the sweep. The
  daemon's real key is swept for separately, whole rather than in runs: it shares its `test-only-`
  announcement with the fixture's `access_aud`, which T012 renders.
- **Route coverage is checked rather than claimed.** net/http will not enumerate a `ServeMux`, so
  the sweep registers the daemon's own pattern constants on a mux of its own and asks
  `Handler(r)` which route each request reached. That catches the mistake that actually happens —
  a target that quietly falls to the catch-all instead of the route it names — which I proved by
  typo'ing the compact path. A **new** browser route still has to be added to
  `registeredPatterns` by hand; that gap is stated in the comment rather than papered over.
- **Put the vacuity guard after the search.** The first mutation (raw values on the settings page)
  failed on the "both secrets are configured" precondition, which `t.Fatalf`'d before the search
  ran and reported a real leak as a broken fixture. The guard now runs last and reports.
- **The session cap is 5 and a route sweep spends it.** Every `plant` counts against
  `Manager.Create`, so the API's DELETE gets a session of its own and the browser's destroy runs
  last of the four actions; peak is four. A sweep that planted per session-scoped route would hit
  the cap and silently turn both creates into refusals.
- **The streams are driven through a recorder**, which answers 500 once the open sequence has
  admitted the request, so what is swept is the open and its record and not a delivered stream.
  Also not a hole: neither stream handler reads the Config at all, and the 500 path returns
  *before* `panes.attach`, so a recorder-driven open leaks no goroutine either.
- **Five mutations run, all caught.** (1) raw values for both secret keys on the settings page;
  (2) a `present (test-only-qx7v…)` mask — caught by the four-character prefix run; (3) the
  allowlist in a response **header** on `GET /sessions`, i.e. the *other door*, reported by route
  — the thing a page test cannot do; (4) the shared secret in the action gate's audit reason,
  caught in the trail; (5) a route dropped from the sweep, caught by the coverage check, which
  named it.

**Left:** T014–T035. Next is **T014** (carry post-redirect-get forward from
`claude/issue-issue-42-...1832`; a rebase-and-reconcile, not a rewrite).

**Findings:**

- **`internal/httpapi` carries a data race in its own fixture, and CI can hit it.**
  `newAuditedServerWith` sets `s.report = func(err error) { ts.failed = append(ts.failed, err) }`
  (`middleware_test.go:215`); two live streams on a bound fleet call it concurrently from
  net/http's own goroutines, and the append is unsynchronised. It reproduces on demand with
  `go test -race -count=2 -parallel 32 ./internal/httpapi` (twice out of two) and appeared once at
  default parallelism; ~14 further runs at default parallelism were clean, with this change and at
  `HEAD` alike, so it predates this task. `failed` wants the lock `syncSink` already has. **Not
  fixed here (AR-008) — it wants a fix-lane commit.**
- **`specs/004-configure-and-operate/tasks.md` was ticked to match the plan this iteration.** It
  had drifted to one checked task out of thirteen finished ones, which a fresh context reading it
  first would have read as "almost nothing is built" (iteration 12's finding). Bookkeeping, not a
  second task — but it is the file the plan names as the single source of truth, and future
  iterations should keep both in step.
- **A four-character run is a probabilistic search over base64url.** The sweep reads page tokens
  and bearer credentials, so a canary's four-character prefix could in principle appear in one by
  chance — order 1e-5 per run, and zero for the hex-shaped values, since none of the runs is hex.
  Worth knowing before anyone debugs a one-off red build.
- **Still open from iterations 5–12:** the three red `-tags quickstart` tests
  (`CRSW_DESTROY_ON_SHUTDOWN` has no loader — the oldest unfixed finding here, and still wanting
  an issue and a fix-lane commit); `contracts/settings-page.md`'s `TestNoMutatingVerbRegistered`
  row still saying 405, and its worked example showing values no loader would produce; three
  `ReadFile` refusals missing from `contracts/config-file.md`'s table; the `version < 1` row; the
  contract's "yields exactly eight keys" against seven; a dangling symlink reading as absent;
  `f.values` having no enumerator; `os.Open` on a FIFO blocking startup with no message;
  `--config <path>` still unbuilt; `README.md` and `deploy/README.md` silent on the config file
  (T034/T035).
- **Lint:** `golangci-lint run` clean (v1.62.2 on a v2 config; CI's pinned v2.12.2 is the gate).
  `gofmt -l .` clean, `go build`, `go vet`, `go test -count=1 ./...` green, `go vet` green under
  `-tags tmux`, `-tags quickstart` and `-tags dev`, `go test -tags dev ./internal/access
  ./internal/config ./internal/httpapi` green, `go.sum` still absent.

## Iteration 14 — 2026-08-07 05:45

**Did:** T014. The four dashboard actions answer `303` to `/?outcome=<code>`; `outcome.go` holds
the closed vocabulary and the copy; `partials/outcome.html` renders the banner, with the
unverified teardown as a titled block; the create form names the configured roots. Carried from
`claude/issue-issue-42-...1832` and reconciled: `outcomeBadStartCommand` added for the
`ErrUnknownStartCommand` arm that landed after the branch, and the action toast rewired to read
`.outcome` / `.outcome-alarm` off the page the redirect lands on.

**Learned:**

- **T014 could not be committed without doing the bulk of T015.** Removing the eight fragment
  bodies breaks 21 top-level tests at once, and `PROMPT.md` step 6 plus the plan's "every task
  ends green" forbid committing that. So every test asserting a fragment status/body was moved to
  `303` + `Location` here. **What is left for T015 is `TestRefusalIsNotARedirect` and an audit
  pass** — not nineteen rewrites. Whoever takes T015 should read this before planning a day's work.
- **The fetch-following-the-redirect is what saves the toast.** `fetch` defaults to
  `redirect: 'follow'`, so the script's POST comes back with the whole fleet page; `sentence()`
  now pulls the banner out of it. `redirect: 'manual'` would have been a dead end — a same-origin
  manual redirect is an opaque response with no readable `Location`. The sessionStorage carry-over
  is untouched and still needed: the fleet's live half still reloads on a shape change.
- **Opening the fleet inside a test costs an audit record.** Four tests now follow the redirect to
  assert the card or the sentence, and `only(t)` fails the moment a second `dashboard.view` lands.
  The audit block has to run *before* the page fetch. This will bite T015 and T016 the same way.
- **`http.Redirect` writes no body for a POST** (it writes one for GET only), so `wantOutcome`'s
  empty-body assertion is free rather than something the handler has to arrange.
- **The FR-016a claim moved from the response to the page.** "Delivered, never compacted" is now
  asserted on the banner the fleet renders, reached through `compactor.landed`. The code alone
  would go on passing through an edit to the copy, which is exactly what contracts/actions.md
  pinned bytes against — so both halves are asserted, in the two places they now live.
- **`stylesheet_test.go`'s `actionFragments` is down to one entry** (the uniform not-found), and
  its `found == 0` vacuity guard had to go: the only body composed in Go now carries no class, so
  a guard demanding one asserted something this door no longer does. The fold-in stays for the
  next route that writes markup without a template.

**Left:** T015–T035. Next is **T015** (`TestRefusalIsNotARedirect` plus the audit pass — see the
first bullet above).

**Findings:**

- **`contracts/actions.md` (milestone 3's) is now stale in nine places.** It fixes the four
  routes' statuses (200/202/400/409/429/500) and quotes two bodies byte for byte; every one of
  those is a `303` now. Not touched here — it is milestone 3's contract and AR-008 keeps this task
  inside its named files — but a fresh context reading it would implement the wrong thing. **It
  wants a docs commit.** Milestone 4 has no actions contract of its own, so there is nothing in
  `specs/004-*/contracts/` that supersedes it.
- **`TestBrowserCreateStartsTheSessionAndAnswersWithItsCard` and
  `TestRenameRelabelsTheRecordAndAnswersWithItsCard` are now misnamed** — neither answers with a
  card; both assert the card on the fleet the redirect lands on. Left alone deliberately (renaming
  a test is churn outside the task), but T015 or T016 should rename them while it is in the file.
- **The `dashboard.view` record the toast's fetch now causes is real, not just a test artefact.**
  Every scripted action produces two records where it produced one: the action, then the fleet the
  script fetched. FR-041 is about one record *per request* and each request still leaves exactly
  one, so this is not a violation — but an operator counting `dashboard.view` in the journal will
  see one per action from now on, and nothing in the docs says so.
- **`internal/httpapi` still carries the data race in its own fixture** (iteration 13):
  `newAuditedServerWith` appends to `ts.failed` unsynchronised from net/http's goroutines
  (`middleware_test.go:215`). Reproduces with `go test -race -count=2 -parallel 32
  ./internal/httpapi`. Still unfixed; still wants a fix-lane commit.
- **Still open from iterations 5–13:** the three red `-tags quickstart` tests
  (`CRSW_DESTROY_ON_SHUTDOWN` has no loader — the oldest unfixed finding here; the quickstart
  suite drives none of the four action routes, so T014 neither helped nor hurt it);
  `contracts/settings-page.md`'s `TestNoMutatingVerbRegistered` row still saying 405, and its
  worked example showing values no loader would produce; three `ReadFile` refusals missing from
  `contracts/config-file.md`'s table; the `version < 1` row; the contract's "yields exactly eight
  keys" against seven; a dangling symlink reading as absent; `f.values` having no enumerator;
  `os.Open` on a FIFO blocking startup with no message; `--config <path>` still unbuilt;
  `README.md` and `deploy/README.md` silent on the config file (T034/T035).
- **Lint:** `golangci-lint run` clean (v1.62.2 on a v2 config; CI's pinned v2.12.2 is the gate).
  `gofmt -l .` clean, `go build`, `go vet`, `go test -count=1 ./...` green, `go vet` green under
  `-tags tmux`, `-tags quickstart` and `-tags dev`, `go test -tags dev ./internal/access
  ./internal/config ./internal/httpapi` green, `go.sum` still absent.

## Iteration 15 — 2026-08-07 05:55

**Did:** T015. `TestRefusalIsNotARedirect` in `internal/httpapi/actions_test.go` drives all
eight refusal shapes — layer 1's two, the gate's four, the lookup's two — at all four
**registered** action routes, asserting no 3xx and no `Location` before it asserts the uniform
answer each one had before T014. A fifth subtest per route makes the opposite claim. The
"~19 tests still asserting fragments" half of the task was already done by iteration 14
(it could not commit T014 without them); an audit pass over `internal/httpapi`,
`internal/audit/leak_test.go` and `settings_test.go` found **zero** stragglers.

**Learned:**

- **The audit half of T015 was already finished and the plan line is misleading.** Iteration
  14's first bullet says so; this iteration confirmed it by grep rather than by trust —
  `dashboard/sessions` across every test file in the repo, plus every non-303 status assertion
  in `actions_test.go`. Everything already asserts `303`, including `internal/audit/leak_test.go`
  (four `r.act(t, http.StatusSeeOther, …)` calls) and `settings_test.go`'s four route rows.
  **A plan line saying "~19 tests" can describe work a previous iteration folded into its own
  commit.** Read the last iteration's Learned section before sizing the task.
- **The `Location`-only mutation is the one that justifies the second assertion.** Adding
  `w.Header().Set(headerLocation, pathFleet)` to `refuseAction` while leaving the 403 alone is
  caught by 16 subtests and by **nothing else in the suite** — `TestRefusalIsByteIdentical`
  compares header maps between refusals, so a header added to *all* of them stays uniform and
  passes. A status-only assertion would have missed it, and a `Location` sitting on a 403 is one
  well-meaning edit from being a redirect.
- **Five mutations run, not reasoned about** (iteration 1's rule): `refuseAction` answering 303
  (16 rows), `notFoundAction` answering 303 (6), `refuseBrowser` answering 303 (8), a `Location`
  on an otherwise-untouched 403 (16), and `redirectOutcome` writing 200 with no `Location` — the
  pre-T014 shape — which failed **only** the four non-vacuity rows and left every refusal row
  green, which is what a non-vacuity block is for. All four touched files were checked by
  `sha256sum` against their pre-mutation digests before the gate.
- **The unconfirmed destroy is deliberately not in the table, and the comment says why.** It
  *does* redirect and must: the operator was verified and the gate admitted them, so FR-029 tells
  them nothing was torn down via a banner. FR-025 is about a caller this daemon would not act for
  at all. Getting this wrong in either direction is a red suite for a reason it did not mean —
  which is also why each route's fixture supplies the fields a request that *would* have worked
  carries (the destroy's `confirm`, the create's two), so every row refuses for the one thing it
  is named for.
- **The session cap is 5, so the fixture is per-subtest.** `fixture.plant` uses `store.Add`
  rather than `Manager.Create`, but the store enforces the cap itself; one shared `refuser` across
  nine cases would have hit it. 34 subtests × a fresh `newAuditedServerWith` costs 0.7s because
  `testKeys` is a `sync.OnceValues` — the RSA pair is generated once per package, not once per
  key server.
- **`PROGRESS.md`'s iteration 13 and 14 entries end with byte-identical lint bullets**, so an
  `Edit` anchored on one matches both and the append silently lands in the wrong place. Anchor on
  the *preceding* line, whose wrapping differs. The same trap will exist for iteration 15 and 16.

**Left:** T016–T035. Next is **T016** (`TestAllFourActionsUsableWithoutScript`). It needs the
outcome *sentence* rendered on the page the redirect lands on, and iteration 14's warning applies
to it directly: opening the fleet inside a test costs a second `dashboard.view` record, so any
audit block has to run before the page fetch or `only(t)` fails.

**Findings:**

- **`contracts/actions.md` (milestone 3's) is still stale in nine places** — iteration 14's
  finding, unchanged and now with a second test resting on the current behaviour. It fixes the
  four routes' statuses (200/202/400/409/429/500) and quotes two bodies byte for byte; every one
  of those is a `303` today. **It wants a docs commit**, and milestone 4 has no actions contract
  of its own to supersede it. Second iteration logging it.
- **`TestBrowserCreateStartsTheSessionAndAnswersWithItsCard` and
  `TestRenameRelabelsTheRecordAndAnswersWithItsCard` are still misnamed** (iteration 14). Neither
  answers with a card. T015 did not rename them: AR-008 keeps a task inside its named work, and
  T015's named work is a new test rather than a sweep of old names. **T016 is in the same file
  and is the last natural chance** before the names outlive everyone who remembers why.
- **`internal/httpapi` still carries the data race in its own fixture** (iterations 13, 14):
  `newAuditedServerWith` sets `s.report = func(err error) { ts.failed = append(ts.failed, err) }`
  unsynchronised (`middleware_test.go:215`). `go test -race -count=2 -run TestRefusalIsNotARedirect`
  is clean — this test opens no stream, so nothing calls `report` concurrently — but the race is
  untouched. Third iteration logging it; still wants a fix-lane commit and the lock `syncSink`
  already has.
- **Still open from iterations 5–14:** the three red `-tags quickstart` tests
  (`CRSW_DESTROY_ON_SHUTDOWN` has no loader — the oldest unfixed finding here; not run this
  iteration, the port is held by the live daemon and T015 touches no `cmd/crswd` file);
  `contracts/settings-page.md`'s `TestNoMutatingVerbRegistered` row still saying 405, and its
  worked example showing values no loader would produce; three `ReadFile` refusals missing from
  `contracts/config-file.md`'s table; the `version < 1` row; the contract's "yields exactly eight
  keys" against seven; a dangling symlink reading as absent; `f.values` having no enumerator;
  `os.Open` on a FIFO blocking startup with no message; `--config <path>` still unbuilt;
  `README.md` and `deploy/README.md` silent on the config file (T034/T035).
- **Lint:** `golangci-lint run` reports `0 issues` (v1.62.2 on a v2 config; CI's pinned v2.12.2
  is the gate). `gofmt -l .` clean, `go build`, `go vet`, `go test -count=1 ./...` green, `go vet`
  green under `-tags tmux`, `-tags quickstart` and `-tags dev`, `go test -tags dev
  ./internal/access ./internal/config ./internal/httpapi` green, `go.sum` still absent.

## Iteration 16 — 2026-08-07 06:09

**Did:** T016, and US3 with it. `TestAllFourActionsUsableWithoutScript` drives create, destroy,
rename and compact through the registered mux, **follows the `Location` each one answers with**,
and asserts the page that comes back is a whole fleet the operator can act from again — doctype,
`<html lang="en">`, `<main class="shell">`, and the create form with a freshly minted token —
stating the outcome in `outcome.go`'s own words. Three subtests per route (two on the compact)
carry FR-022.

**Learned:**

- **Every action test in this package stopped at the 303, so nothing followed one.** T014 wrote
  the redirect and T015 asserted what deliberately is not one; both end at the first response.
  Milestone 3's defect was that the *second* request landed on a fragment, so US3's own claim was
  the one thing 4,400 lines of `actions_test.go` could not see. `compactor.landed` came closest
  and rebuilds the URL by hand rather than reading the header. **The new `follows` takes the
  address off the response**, which is the difference between proving the fleet renders a code
  the test chose and proving it renders the one the daemon wrote.
- **Three mutations, run rather than reasoned about, and each fails a different subtest.**
  `redirectOutcome(w, r, outcome(r.PostForm.Get(fieldName)))` on the create's success → a code no
  vocabulary spells → the page renders no banner (case 1 red). A `code = outcome(form["outcome"])`
  line at the top of `redirectOutcome` → all four routes redirect to `teardown-unverified` and the
  page renders the alarm block (case 2 red on all four). And the create's bad-name arm redirecting
  to `…&name=<caller text>` with `dashboard` appending it to `banner.Message` → case 3 names the
  reflected fragment. **The third is the one a well-meaning hand actually writes** — "that name is
  not usable" reads better with the name in it.
- **`git checkout -- <file>` needs approval in this loop and did not get it.** Reverting a
  mutation has to be a reverse `Edit`. That works, but note the `format-and-lint` hook runs
  `goimports` on every write: mutation 3 added `net/url` to `actions.go` and the hook **removed it
  again** on the reverting edit, so the second reverse edit failed with "string not found" — which
  is the hook being right, not a problem. `git status --porcelain` afterwards is the check that
  matters, and it showed only `actions_test.go`.
- **The local `golangci-lint` is now v2.12.2**, the version CI pins. Iterations 5–15 all recorded
  v1.62.2 reading a v2 config, which runs zero linters and exits 0 (#26). Whatever the loop is
  running on has been upgraded; `0 issues` this iteration is a real green, and the session-start
  hook's warning is silent.
- **`refuser` and `mutatingRoutes()` are the right seam for anything that sweeps the four action
  routes**, and reusing them beat a sixth fixture: `attempt` gained one field (`smuggled`, merged
  over the route's fields and before the page token) and `mutatingRoute` gained two (`states`, and
  the `chosen` field a caller can fill in). The name `refuser` reads oddly in a success test —
  `withoutScript` embeds it rather than renaming it, because renaming a type T015 owns is the
  refactor AR-008 forbids.

**Left:** T017–T035. Next is **T017** (persist the start-command name as `@crswd-start`), which
is the first US4 task and the first in a while that touches tmux — it needs `-tags tmux` as well
as the default gate.

**Findings:**

- **A comment inserted above an existing type silently becomes that type's doc comment.** Adding
  `callerText` between `mutatingRoute`'s doc block and its `type` line left the route's whole
  explanation attached to the new struct, and `gofmt`, `go vet` and `golangci-lint` were all
  green on it. Caught by reading the diff. In a file where the comments *are* the documentation,
  the diff read is not optional.
- **`contracts/actions.md` (milestone 3's) is still stale in nine places** — iterations 14 and 15,
  unchanged, and now with a third test resting on the current behaviour. It fixes the four routes'
  statuses (200/202/400/409/429/500) and quotes two bodies byte for byte; every one is a `303`
  today. **It wants a docs commit.** Third iteration logging it.
- **`TestBrowserCreateStartsTheSessionAndAnswersWithItsCard` and
  `TestRenameRelabelsTheRecordAndAnswersWithItsCard` are still misnamed** (iterations 14, 15).
  Neither answers with a card. T016 did not rename them either — AR-008 keeps a task inside its
  named work, and T016's is a new test — so iteration 15's "last natural chance" has passed. It
  is now a fix-lane commit or nothing.
- **`internal/httpapi` still carries the data race in its own fixture** (iterations 13–15):
  `newAuditedServerWith` sets `s.report = func(err error) { ts.failed = append(ts.failed, err) }`
  unsynchronised (`middleware_test.go:215`). Untouched; fourth iteration logging it; still wants
  the lock `syncSink` already has.
- **Still open from iterations 5–15:** the three red `-tags quickstart` tests
  (`CRSW_DESTROY_ON_SHUTDOWN` has no loader — the oldest unfixed finding here; not run this
  iteration, the port is held by the live daemon and T016 touches no `cmd/crswd` file);
  `contracts/settings-page.md`'s `TestNoMutatingVerbRegistered` row still saying 405, and its
  worked example showing values no loader would produce; three `ReadFile` refusals missing from
  `contracts/config-file.md`'s table; the `version < 1` row; the contract's "yields exactly eight
  keys" against seven; a dangling symlink reading as absent; `f.values` having no enumerator;
  `os.Open` on a FIFO blocking startup with no message; `--config <path>` still unbuilt;
  `README.md` and `deploy/README.md` silent on the config file (T034/T035).
- **Lint:** `golangci-lint run` reports `0 issues`, and this time on **v2.12.2** — CI's own pinned
  version, so the green means what it says. `gofmt -l .` clean, `go build`, `go vet`, `go test
  -count=1 ./...` and `go test -count=2 ./internal/httpapi` green, `go vet` green under
  `-tags tmux`, `-tags quickstart` and `-tags dev`, `go test -tags dev ./internal/access
  ./internal/config ./internal/httpapi` green, `go.sum` still absent.

## Iteration 17 — 2026-08-07 06:18

**Did:** T017, the first US4 task. `@crswd-start` is written by `Manager.start` as the fifth
tmux user option, carried in the `list-sessions` format string, and read back into the record by
`Adopt`. Two `-tags tmux` tests in the new `internal/session/mode_test.go` drive the round trip
through a **real** tmux on a private `-L` socket: `TestStartCommandSurvivesRestart` (create under
manager A, adopt under manager B with an empty store) and `TestRestoredSessionWithoutOptionIsLocal`
(a session built by hand with provenance and no `@crswd-start` adopts cleanly, empty name).

**Learned:**

- **The option is a fifth field in a format string, so it is a fifth field in six test fixtures.**
  `parseSessions` cuts from the right and the row went from five fields to six, which meant one
  more `|` on every valid row in `exec_test.go`, plus the argv literal in three files
  (`manager_test.go`, `fake_test.go`, `exec_test.go`) and two call-count assertions
  (`TestCreateSendsTheTmuxCommandsInOrder`, and `TestCreateStartsTheSessionItPromised` in
  `internal/httpapi`, which counts `SetOption` ops). **The httpapi one is the one you will not
  predict** — nothing in `internal/session` points at it, and `go build`/`go vet` are both silent.
  Run the whole default suite before assuming a tmuxctl change is local.
- **`internal/session` had no `-tags tmux` file before this one**, so there was no harness to
  reuse. It needed its own `newModeFixture` — `tmuxctl.NewExec` on a `crswd-test-<name>` socket
  with a `kill-server` cleanup, modelled on `newTestExec` in `internal/tmuxctl/exec_tmux_test.go`
  (whose `socketFor` is unexported and one package away). Two things that are **not** optional
  there: the fixture calls `SetStartCommands` with `true` under both names, because the daemon's
  own default is `claude --dangerously-skip-permissions` and a real `SendKeys` into a real shell
  would start an unsandboxed assistant on whatever host ran the suite; and the manager takes the
  **real** clock via `NewManager`, not `stoppedClock`, because tmux stamps `#{session_created}`
  from the host clock and `Adopt` compares the two — a stopped clock makes every real session look
  either newborn or long expired.
- **A build tag excludes, it does not replace.** `mode_test.go` compiles *alongside*
  `manager_test.go` and `workdir_test.go` under `-tags tmux`, so `newWorkDirFixture`,
  `capNotUnderTest` and `stoppedClock` are all in scope. Only `repo()` had to be restated: it
  hangs off `managerFixture`, which carries the tmux fake this file exists to avoid.
- **Both mutations were run, not reasoned about.** Wrapping the new `SetOption` in `if false`
  → `TestStartCommandSurvivesRestart` reds with `restored StartCommand = "", want "rc"`. Adding
  an `info.StartCommand == ""` → `failures` arm to `Adopt` → `TestRestoredSessionWithoutOptionIsLocal`
  reds with the refusal in the message. Reverted by reverse `Edit` (iteration 16's note:
  `git checkout --` needs approval in this loop), and `git diff --stat` afterwards is the check.
- **The deployed daemon is safe across this.** The new format string ships with the new parser, and
  tmux renders an unset user option as an empty field — so the live fleet's five-option sessions
  produce six-field rows with the last one empty, which is exactly the second test's case.

**Left:** T018–T035. Next is **T018** (`Session.Mode()`, derived, plus the startup refusal for a
`remote_start_commands` name absent from `start_commands`). It is where `ModeLocal`/`ModeRemote`
first exist — see the first finding below.

**Findings:**

- **T017's contract row names `ModeLocal`, which T018 is the task that creates.**
  `contracts/session-mode.md` says `TestRestoredSessionWithoutOptionIsLocal` asserts "No
  `@crswd-start` → `ModeLocal`, no error", but `Session.Mode()` does not exist until T018. The
  test as shipped asserts the observable half T017 owns — an empty `StartCommand` and a
  successful adoption — which is precisely the value `Mode()` will read. **T018 should strengthen
  it to `restored.Mode() != ModeLocal` in the same commit that adds `Mode()`**; it is a one-line
  change and the test is already positioned for it. Not done here because AR-008 keeps a task
  inside its named work.
- **Two `TestParseSessions` fixtures pass for the wrong reason** (pre-existing, untouched):
  `"creation time is not a number"` is `"crswd-abc123|whenever|1\n||"` and `"creation time missing
  entirely"` is `"crswd-abc123||1\n||"`. The `\n` in the middle looks like a typo for a single row
  — as written they are two rows, the first of which fails on *separator count* and never reaches
  `ParseInt`, so neither case exercises the parse it is named for. They were left exactly as they
  were (they still error, before and after), so this is a **fix-lane commit**: drop the `\n` and
  pad each to six fields. First iteration logging it.
- **`specs/001-crswd-daemon-core/contracts/tmuxctl.md` is now stale by three fields**, not one.
  Line 163 still documents `list-sessions -F '#{session_name}|#{session_created}|#{@crswd-managed}'`
  — it was already two behind after #72 added `@crswd-name` and `@crswd-workdir`, and this
  iteration makes it three. Lines 81-82 likewise list two `set-option` calls where `start` now
  makes five. **Wants a docs commit** alongside the `contracts/actions.md` one below.
- **`contracts/actions.md` (milestone 3's) is still stale in nine places** — iterations 14, 15, 16,
  unchanged. It fixes the four routes' statuses (200/202/400/409/429/500) and quotes two bodies
  byte for byte; every one is a `303` today. **It wants a docs commit.** Fourth iteration logging it.
- **`TestBrowserCreateStartsTheSessionAndAnswersWithItsCard` and
  `TestRenameRelabelsTheRecordAndAnswersWithItsCard` are still misnamed** (iterations 14-16).
  Neither answers with a card. Now fix-lane or nothing.
- **`internal/httpapi` still carries the data race in its own fixture** (iterations 13-16):
  `newAuditedServerWith` sets `s.report = func(err error) { ts.failed = append(ts.failed, err) }`
  unsynchronised (`middleware_test.go:215`). Untouched; fifth iteration logging it; still wants the
  lock `syncSink` already has.
- **Still open from iterations 5-16:** the three red `-tags quickstart` tests
  (`CRSW_DESTROY_ON_SHUTDOWN` has no loader — the oldest unfixed finding here; not run this
  iteration, the port is held by the live daemon, though `go vet -tags quickstart ./...` is green);
  `contracts/settings-page.md`'s `TestNoMutatingVerbRegistered` row still saying 405, and its worked
  example showing values no loader would produce; three `ReadFile` refusals missing from
  `contracts/config-file.md`'s table; the `version < 1` row; the contract's "yields exactly eight
  keys" against seven; a dangling symlink reading as absent; `f.values` having no enumerator;
  `os.Open` on a FIFO blocking startup with no message; `--config <path>` still unbuilt;
  `README.md` and `deploy/README.md` silent on the config file (T034/T035).
- **Lint:** `golangci-lint run` reports `0 issues` on **v2.12.2**, CI's pinned version. `gofmt -l .`
  clean, `go build`, `go vet`, `go test -count=1 ./...` green; `go test -count=1 -tags tmux ./...`
  green **and the two new tests ran rather than skipped** (`-v` confirms); `go vet` green under
  `-tags tmux`, `-tags quickstart` and `-tags dev`; `go test -tags dev ./internal/access
  ./internal/config ./internal/httpapi` green; `go.sum` still absent.

## Iteration 18 — 2026-08-07 06:27

**Did:** T018. `session.Mode` (`ModeLocal`/`ModeRemote`) and `func (s Session) Mode(remoteCommand
string) Mode` in `internal/session/session.go`, derived from `StartCommand` and stored nowhere.
Seven-case table `TestModeDerivedFromStartCommand` and reflect-walk `TestSessionStoresNoModeField`
in the **untagged** `session_test.go`; `TestModeNotInStartCommandsRefusedAtStartup` in
`internal/config/config_test.go`; and the two `-tags tmux` tests strengthened to assert the mode
itself, which is what iteration 17 left positioned for this task.

**Learned:**

- **The startup refusal T018 asks for already shipped with #58.** `loadRemoteControlCommand`
  refuses a `CRSW_REMOTE_CONTROL_COMMAND` naming a command `CRSW_START_COMMANDS` does not
  configure, and the refusals table in `config_test.go` already had it as one row. The new test is
  that behaviour under the name `contracts/session-mode.md` gives it, so the contract row is
  traceable to a test — not new behaviour. It was still mutation-checked (return `"", nil` instead
  of the error → red).
- **`Mode()` takes a parameter, and the contract's signature says it takes none.** A `Session` is a
  record; which name means remote is startup configuration, and the only zero-argument spellings
  are a package-level variable set at startup (global mutable state, unparallelisable tests) or a
  field on the record (the second source of truth research R5 rejects). `DisplayState(now)` is the
  in-repo precedent: a derived value takes the thing the record cannot know. **The deviation is
  deliberate** — see the first finding.
- **Empty `StartCommand` must be normalised to `config.DefaultStartCommandName` before comparing.**
  `StartCommands.Command` reads an empty name that way, so a create that asked for nothing runs the
  default command; if the operator pointed `CRSW_REMOTE_CONTROL_COMMAND` at `default`, those
  sessions genuinely *are* remote. Dropping the normalisation passes six of the seven table cases —
  only `start="" remote="default"` reds. That case is the whole reason the normalisation is there.
- **Pure derivation tests do not go in `mode_test.go`.** That file is `//go:build tmux`, so CI never
  reaches it (`AGENTS.md`: a tagged suite reports nothing to `go test ./...`). The contract names it
  as the file for these tests; putting them there would have hidden the only test of the new method
  from every CI run. They went in the untagged `session_test.go`, and only the two assertions that
  genuinely need a real tmux round trip were added to `mode_test.go`.
- **`internal/session/session.go` now imports `internal/config`.** No cycle — `manager.go` in the
  same package already did, and `config` deliberately restates `maxSessionNameLen` rather than
  importing back. goimports added the line unprompted after the edit.

**Left:** T019–T035. Next is **T019** (🔒 `POST /dashboard/sessions/{id}/mode`), which is the first
caller of `Mode()`: today nothing outside tests calls it, which is the repo's recurring failure
mode, and the plan's own ordering is what defers it by one task.

**Findings:**

- **NEEDS CLARIFICATION (not blocking): `remote_start_commands` does not exist, and this iteration
  did not create it.** `tasks.md` T018, `contracts/session-mode.md` and `data-model.md` all derive
  the mode against "`remote_start_commands`, a **list** of names". The shipping daemon has the
  singular `CRSW_REMOTE_CONTROL_COMMAND` → `Config.RemoteControlCommand` (#58), with exactly the
  startup refusal the task asks for, a `remote_control_command` row already in the settings page
  and `deploy/crswd.example.service`. Adding a plural key would put **two** places on this daemon
  saying which names mean remote — the duplication this whole milestone argues against — and no
  FR in `spec.md` asks for a list. A list is also incoherent with T019's own contract: `mode` is
  the literal `local` or `remote`, so a set of remote names gives `mode=remote` nothing to pick
  from. So `Mode()` takes the one configured name, and is a one-line change to a set if the
  operator wants one. **If the plural key is actually wanted, T018 and the three spec files want
  amending together, and this is the iteration to say so.**
- **The contract's signature is `func (s Session) Mode() Mode` and the shipped one is
  `Mode(remoteCommand string)`.** Reasons in the second bullet above. `contracts/session-mode.md`
  line 12 and `data-model.md` line 95 both want the parameter added when someone reconciles the
  `remote_start_commands` question — one docs commit, both files.
- **Two `TestParseSessions` fixtures still pass for the wrong reason** (iteration 17, untouched):
  `"creation time is not a number"` and `"creation time missing entirely"` in
  `internal/tmuxctl/exec_test.go` carry a stray `\n`, so each is two rows and the first fails on
  separator count before `ParseInt` is reached. **Fix-lane commit:** drop the `\n`, pad to six
  fields.
- **`specs/001-crswd-daemon-core/contracts/tmuxctl.md` is stale by three fields** (iteration 17):
  line 163's `list-sessions` format string and lines 81-82's two `set-option` calls against five.
  **Wants a docs commit** alongside the `contracts/actions.md` one.
- **`contracts/actions.md` (milestone 3's) is still stale in nine places** — iterations 14-17,
  unchanged. Every route it documents as 200/202/400/409/429/500 is a `303` today. Fifth iteration
  logging it.
- **`TestBrowserCreateStartsTheSessionAndAnswersWithItsCard` and
  `TestRenameRelabelsTheRecordAndAnswersWithItsCard` are still misnamed** (iterations 14-17).
  Neither answers with a card. Fix-lane or nothing.
- **`internal/httpapi` still carries the data race in its own fixture** (iterations 13-17):
  `newAuditedServerWith` sets `s.report` unsynchronised (`middleware_test.go:215`). Sixth iteration
  logging it; still wants the lock `syncSink` already has.
- **Still open from iterations 5-17:** the three red `-tags quickstart` tests
  (`CRSW_DESTROY_ON_SHUTDOWN` has no loader — the oldest unfixed finding here; not run this
  iteration, the port is held by the live daemon, though `go vet -tags quickstart ./...` is green);
  `contracts/settings-page.md`'s `TestNoMutatingVerbRegistered` row still saying 405, and its worked
  example showing values no loader would produce; three `ReadFile` refusals missing from
  `contracts/config-file.md`'s table; the `version < 1` row; the contract's "yields exactly eight
  keys" against seven; a dangling symlink reading as absent; `f.values` having no enumerator;
  `os.Open` on a FIFO blocking startup with no message; `--config <path>` still unbuilt;
  `README.md` and `deploy/README.md` silent on the config file (T034/T035).
- **Lint:** `golangci-lint run` reports `0 issues` on **v2.12.2**, CI's pinned version. `gofmt -l .`
  clean, `go build`, `go vet`, `go test -count=1 ./...` green; `go test -count=1 -tags tmux ./...`
  green and the two strengthened tests ran rather than skipped (`-v` confirms); `go vet` green under
  `-tags tmux`, `-tags quickstart` and `-tags dev`; `go test -tags dev ./internal/access
  ./internal/config ./internal/httpapi` green; `go.sum` still absent. All three mutations were run,
  not reasoned about, and reverted by reverse `Edit` (`git diff --stat` afterwards shows additions
  only).

## Iteration 19 — 2026-08-07 06:45

**Did:** T019 🔒. `POST /dashboard/sessions/{id}/mode` in `internal/httpapi/actions.go`, registered
through `handleAction` under the new `audit.ActionSessionMode` (`session.mode`), with `offersMode`
matching the `mode` field against `session.ModeLocal`/`ModeRemote`, `confirm=yes` (FR-029), the
ownership lookup, and three new outcome codes — `bad-mode`, `mode-unconfirmed`, `mode-failed`.
Six tests in `actions_test.go` (the four the task names, plus the cross-owner uniformity the
security checklist requires and one pinning the deferred answer), the two new codes in
`outcome_test.go`'s `spelledOutcomes`, and the route added to `settings_test.go`'s
`registeredPatterns` **with a row driving it**, without which SC-005 would sweep nine routes and
say nothing about the tenth.

**Learned:**

- **The transition is T020's, so this route deliberately answers a refusal on its success path.**
  The plan orders the door before the engine, and `internal/session/manager.go` is T020's named
  file, so there is nothing behind this handler that can restart a pane. It answers
  `outcome=mode-failed` with `errModeUnavailable` on the record rather than a 303 saying the mode
  changed — a success nobody performed would put a card describing a local session under the word
  remote, which is the one claim this route must never get wrong. **T020's iteration replaces
  exactly two lines** (the `Deny` + `redirectOutcome` at the foot of `modeFromBrowser`), adds
  `outcomeModeChanged`, and must rewrite `TestToggleSaysSoWhenItCannotAct` — which is why that test
  exists and says so in its own comment. Nothing links to the route yet (no template posts to it),
  so the live daemon gains a gated route that refuses and changes nothing.
- **The value check is an allowlist and must never become a conversion.** `session.Mode(value)`
  compiles, is shorter, and hands `claude --dangerously-skip-permissions` straight through as a
  `Mode` carrying that spelling. `offersMode` compares against the session package's two literals
  instead, so what a form posts and what a card derives cannot come to mean different things.
- **The value is checked *before* the confirming step**, which is the reverse of the destroy's
  order and deliberate: both run before the store is read, and the journal should carry the fact
  that something posted a command line at this daemon whether or not the same request also forgot
  to confirm.
- **Which configured command each mode names was left to T020.** The mapping (remote →
  `Config.RemoteControlCommand`, local → `DefaultStartCommandName`) belongs where the transition
  uses it; a copy on this door would be a second place free to disagree about what "remote" runs.
  The consequence to know: **`mode=remote` on a daemon configuring no remote-control command is
  admitted by this door today** and stopped by the unavailable arm behind it. T020 owes that
  refusal — and the one for a daemon whose remote command *is* `default`, where no local command
  exists to switch to.
- **`registeredPatterns` is the one thing a new browser route cannot drift from silently.** Its
  own comment says a tenth entry has to be added by hand; adding the pattern without a request
  driving it fails loudly (`... is registered on this daemon and nothing above drove it`), which is
  the good failure. `mutatingRoutes()` was left at four on purpose: every row needs a `succeeds`
  outcome and there is no success to name until T020.
- **`script-src` contains `rc`.** The "the answer never carries the submitted value" assertion
  searches the `Location` and the body only, not the headers — the CSP would false-positive on the
  `rc` start-command-name case. The check is gated on values of four characters or more for the
  same reason.
- **Both `session.mode` and the two `dashboard.*`-shaped alternatives were considered; the
  contract's literal won.** See the first finding.

**Left:** T020–T035. Next is **T020** (the transition itself, `-tags tmux`), which is the first
thing that makes this route do anything.

**Findings:**

- **`session.mode` puts a browser-door action in the API's `session.*` namespace.** `tasks.md`
  T019 and `contracts/session-mode.md` both fix the literal, so that is what shipped, but
  `docs/security.md` says a browser action is audited under its own name and lists four
  `dashboard.*` ones — an operator grepping `session\.` now counts one browser action among the API
  operations. `settings.view` is the existing precedent for a browser route named for its subject,
  so this is consistent with the newer half of the trail rather than with the older half. **If the
  operator wants `dashboard.mode` instead, it is a one-line change in three places**
  (`internal/audit/audit.go`, `server.go`, `wantModeAction` in the test) plus the two spec files.
- **The contract says the toggle redirects to the *session page*; this route redirects to the
  fleet.** `contracts/session-mode.md`'s success row says "303 to the session page (per the PRG
  contract)", and the PRG contract as built (`redirectOutcome`, T014) goes to `/` — which is the
  only page that renders a banner at all (`dashboard.html` executes `{{ template "outcome" }}`,
  `session.html` does not). Redirecting to the session page today would silently drop what the
  operator is told. Closing this properly means teaching `sessionPage` an `Outcome` field, which is
  outside T019's named files; **T020 or T021 should decide**, and one of them owns the toggle's
  markup, which does not exist yet either — no task in the plan adds the control that posts to this
  route.
- **Two `TestParseSessions` fixtures still pass for the wrong reason** (iterations 17-18,
  untouched): `"creation time is not a number"` and `"creation time missing entirely"` in
  `internal/tmuxctl/exec_test.go` carry a stray `\n`, so each is two rows and the first fails on
  separator count before `ParseInt` is reached. **Fix-lane commit:** drop the `\n`, pad to six
  fields.
- **`specs/001-crswd-daemon-core/contracts/tmuxctl.md` is stale by three fields** (iterations
  17-18): line 163's `list-sessions` format string and lines 81-82's two `set-option` calls against
  five. **Wants a docs commit** alongside the `contracts/actions.md` one.
- **`contracts/actions.md` (milestone 3's) is still stale in nine places** — iterations 14-18,
  unchanged. Every route it documents as 200/202/400/409/429/500 is a `303` today. It now also
  describes four action routes where the daemon registers five. Sixth iteration logging it.
- **`TestBrowserCreateStartsTheSessionAndAnswersWithItsCard` and
  `TestRenameRelabelsTheRecordAndAnswersWithItsCard` are still misnamed** (iterations 14-18).
  Neither answers with a card. Fix-lane or nothing.
- **`internal/httpapi` still carries the data race in its own fixture** (iterations 13-18):
  `newAuditedServerWith` sets `s.report` unsynchronised (`middleware_test.go:215`). Seventh
  iteration logging it; still wants the lock `syncSink` already has.
- **Still open from iterations 5-18:** the three red `-tags quickstart` tests
  (`CRSW_DESTROY_ON_SHUTDOWN` has no loader — the oldest unfixed finding here; not run this
  iteration, the port is held by the live daemon, and this task touches no `cmd/crswd` file, though
  `go vet -tags quickstart ./...` is green); `contracts/settings-page.md`'s
  `TestNoMutatingVerbRegistered` row still saying 405, and its worked example showing values no
  loader would produce; three `ReadFile` refusals missing from `contracts/config-file.md`'s table;
  the `version < 1` row; the contract's "yields exactly eight keys" against seven; a dangling
  symlink reading as absent; `f.values` having no enumerator; `os.Open` on a FIFO blocking startup
  with no message; `--config <path>` still unbuilt; `README.md` and `deploy/README.md` silent on the
  config file (T034/T035).
- **Lint:** `golangci-lint run` reports `0 issues` on **v2.12.2**, CI's pinned version. `gofmt -l .`
  clean, `go build`, `go vet`, `go test -count=1 ./...` green; `go test -count=1 -tags tmux ./...`
  green; `go vet` green under `-tags tmux`, `-tags quickstart` and `-tags dev`; `go test -tags dev
  ./internal/access ./internal/config ./internal/httpapi` green; `go.sum` still absent. All three
  mutations were run, not reasoned about — the value check widened to accept anything non-empty
  (`TestArbitraryModeValueRefused` red on eight cases), the confirming step dropped
  (`TestToggleRequiresConfirm` red on all six), and the route re-registered with `handleBrowser`
  (`TestToggleCrossSiteBothHalves` red on all four, each answering 303 where 403 was wanted) — and
  each reverted by reverse `Edit`, with `git diff --stat` afterwards showing additions only.

## Iteration 20 — 2026-08-07 07:10

**Did:** T020. `Manager.SetMode` in `internal/session/manager.go`: two sends and an option write —
`C-c C-c` to the pane, then the other configured command with `--continue` and `Enter`, then
`@crswd-start` — plus `commandForMode` (remote → `Config.RemoteControlCommand`, local →
`DefaultStartCommandName`), `SetRemoteControlCommand`, three sentinels (`ErrModeUnavailable`,
`ErrModeUnchanged`, `ErrUnknownMode`) and `Store.SetStartCommand`. Wired end to end: `server.go`
passes `cfg.RemoteControlCommand` to the manager, and `modeFromBrowser`'s two-line dead end is now
the real call plus `refuseBrowserMode`, answering the new `outcomeModeChanged`. Tests: the three the
task names in `mode_test.go` (`-tags tmux`), five fake-based ones in `manager_test.go` that CI
actually runs, the toggle's success and a two-case rewrite of `TestToggleSaysSoWhenItCannotAct` in
`actions_test.go`, and the route's fifth row in `mutatingRoutes()`.

**Learned:**

- **`respawn-pane -k` wipes the scrollback. Measured, not reasoned about.** It was the obvious
  mechanism — deterministic where a signal is a request — and a throwaway `-tags tmux` probe showed
  the pane comes back empty: the marker echoed before it was gone, and `send-keys C-c` left the
  whole history intact. That is SC-007's one requirement, so the transition signals through the
  terminal and resets nothing. **Do not "fix" the interrupt into a respawn.**
- **The interrupt is sent twice in one `SendKeys` call, and cannot be verified.** A TUI that catches
  SIGINT reads the first as cancel and only the second as exit; at a bare shell prompt both are
  no-ops on an empty line. tmux offers no way to ask whether the process took it, so `SetMode`
  claims only that the keys reached the pane — Compact's own limit (FR-016a). See the finding below:
  a process that ignores SIGINT would receive the command line as *input*.
- **`#{session_id}` is a worthless witness on these fixtures, and the mutation is what showed it.**
  Each `modeFixture` holds one session on a private `-L` server: killing it stops the server, and
  the session made next is numbered `$0` again — so a destroy-and-recreate passed the check. The
  test compares `#{pane_pid}` now, which does not restart and also catches `respawn-pane`, since
  that hands the pane a new shell. The first version also read both ends *after* the transition,
  comparing a value with itself; `toggleRun` exists to carry the reading taken before it.
- **A scrollback test needs something that has actually scrolled.** The local command is
  `seq -f crswd-local-%g 1 200` into a 24-row window, and the fixture asserts `crswd-local-1` is
  *off* the visible screen before toggling — otherwise the test would only be proving a screen was
  not cleared. Match whole lines when doing this: `strings.Contains(page, "crswd-local-1")` is true
  of `crswd-local-179`, which is how the first run passed for the wrong reason.
- **The remote command in the tmux fixture must tolerate a trailing `--continue`.** The transition
  appends the flag to whatever it restarts, so a command that parses its arguments (`seq`) fails and
  prints nothing, and the test waits for output a correct implementation never produces. `echo` is
  why the remote side is an echo.
- **Banner sentences may not contain an apostrophe.** The template escapes it, so `statesOutcome`
  compares `&#39;` against `'` and the row fails on punctuation. The other four sentences have none,
  which is not a coincidence anyone had written down until now.
- **`ErrModeUnchanged` is a refusal on purpose.** Carrying out a toggle to the mode a session is
  already in would interrupt the process the operator is watching to leave it where it was; a stale
  card and a double submission both arrive that way. It is compared as *modes* rather than names, so
  a session started under some third configured command is correctly already local.
- **`mutatingRoutes()` is five now**, which is what put the toggle's success under the no-script and
  caller-text sweeps. `newRefuser` had to configure the daemon for modes — the only one of the five
  whose success depends on configuration rather than on the request — and
  `TestAllFourActionsUsableWithoutScript` became `TestEveryActionIsUsableWithoutScript`.

**Left:** T021–T035. Next is **T021** (show the mode on the card, textually), which is the first
thing that renders what this iteration writes. It needs the remote-control name inside
`internal/httpapi` to call `Session.Mode(...)`; `s.cfg.RemoteControlCommand` is already there.

**Findings:**

- **NEEDS CLARIFICATION (not blocking): a start command that ignores SIGINT would receive the new
  command line as a prompt.** The daemon cannot observe whether the interrupt took, so on a process
  that catches SIGINT and stays up, `SendKeys(command + " --continue", Enter)` lands in *that
  process's input* rather than in the shell — which is prompt text arriving from a browser, the
  surface `spec.md` puts out of scope, and a mode change that reports success while nothing moved.
  Closing it needs one of: a Claude-specific exit sequence (FR-015 forbids hardcoding `claude`), or
  a new read-only verb in `internal/tmuxctl` — `#{pane_current_command}` or `#{pane_pid}` — to
  confirm the pane is back at its shell before typing. **That is a real task, not a line**, and it
  is outside T020's named file. The operator should decide whether it belongs in this milestone.
- **No task in the plan adds the control that posts to `/dashboard/sessions/{id}/mode`.** T021 shows
  the mode textually; nothing renders a form. The route is reachable, gated and now functional, and
  the dashboard offers no way to reach it — carried forward from iteration 19, still true.
- **The contract says the toggle redirects to the *session page*; it still redirects to the fleet**
  (iteration 19). `redirectOutcome` goes to `/`, which is the only page that renders a banner —
  `dashboard.html` executes the outcome template and `session.html` does not — so redirecting to the
  session page today would silently drop what the operator is told. **Decided for now: the fleet**,
  because the alternative is a success nobody is told about. Closing it properly means teaching
  `sessionPage` an `Outcome` field, which belongs with whichever task adds the control.
- **`session.mode` puts a browser-door action in the API's `session.*` namespace** (iteration 19,
  unchanged). One-line change in three places plus two spec files if the operator wants
  `dashboard.mode`.
- **`contracts/session-mode.md` and `data-model.md` still spell `Mode()` with no parameter and still
  describe `remote_start_commands`, a plural key this daemon does not have** (iteration 18). Both
  want one docs commit, and this iteration adds a third line to it: the contract's transition
  section should say the process is signalled rather than the pane respawned, and say why.
- **Two `TestParseSessions` fixtures still pass for the wrong reason** (iterations 17-19): the stray
  `\n` in `"creation time is not a number"` and `"creation time missing entirely"` in
  `internal/tmuxctl/exec_test.go`. **Fix-lane commit:** drop the `\n`, pad to six fields.
- **`specs/001-crswd-daemon-core/contracts/tmuxctl.md` is stale by three fields** (iterations
  17-19): line 163's `list-sessions` format string and lines 81-82's two `set-option` calls against
  five.
- **`contracts/actions.md` (milestone 3's) is still stale in nine places** — iterations 14-19. Every
  route it documents as 200/202/400/409/429/500 is a `303` today, and it describes four action
  routes where the daemon registers five. Seventh iteration logging it.
- **`TestBrowserCreateStartsTheSessionAndAnswersWithItsCard` and
  `TestRenameRelabelsTheRecordAndAnswersWithItsCard` are still misnamed** (iterations 14-19).
- **`internal/httpapi` still carries the data race in its own fixture** (iterations 13-19):
  `newAuditedServerWith` sets `s.report` unsynchronised (`middleware_test.go:215`). Eighth iteration
  logging it.
- **Still open from iterations 5-19:** the three red `-tags quickstart` tests
  (`CRSW_DESTROY_ON_SHUTDOWN` has no loader — the oldest unfixed finding here; not run this
  iteration, the port is held by the live daemon, though `go vet -tags quickstart ./...` is green);
  `contracts/settings-page.md`'s `TestNoMutatingVerbRegistered` row still saying 405, and its worked
  example showing values no loader would produce; three `ReadFile` refusals missing from
  `contracts/config-file.md`'s table; the `version < 1` row; the contract's "yields exactly eight
  keys" against seven; a dangling symlink reading as absent; `f.values` having no enumerator;
  `os.Open` on a FIFO blocking startup with no message; `--config <path>` still unbuilt;
  `README.md` and `deploy/README.md` silent on the config file (T034/T035).
- **Lint:** `golangci-lint run` reports `0 issues` on **v2.12.2**, CI's pinned version. `gofmt -l .`
  clean, `go build`, `go vet`, `go test -count=1 ./...` green; `go test -count=1 -tags tmux ./...`
  green and the three new tmux tests ran rather than skipped; `go vet` green under `-tags tmux`,
  `-tags quickstart` and `-tags dev`; `go test -tags dev ./internal/access ./internal/config
  ./internal/httpapi` green; `go.sum` still absent. Four mutations were run, not reasoned about —
  `--continue` dropped (three tests red across both suites and httpapi), the transition replaced by
  `Kill` + `New` (the pane-pid and scrollback assertions red, and the argv one), the
  already-in-that-mode guard short-circuited (red in both packages), and the `@crswd-start` write
  removed (two red) — each reverted by reverse `Edit`, with `git diff --stat` afterwards showing
  additions only.
