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
