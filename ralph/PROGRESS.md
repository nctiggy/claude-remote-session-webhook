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

---

## Iteration 0 — updates that carry the files, not just the binary

**Did:** Archived milestone 14.

**The operator:** *"How do we make it so that the updates also grab or update the
systemd unit file as well? I feel like the config and systemd files should update
as part of the updates… values saved as part of the updates but the files
updated."*

**Findings:**

- **Half of it exists and nothing calls it.** `crswd config migrate`
  (`internal/config/migrate.go`) already rewrites a config into the current schema
  **line by line**, copying every line it has no reason to touch byte for byte,
  spacing and line endings included — because "a migration that reproduced the
  settings and dropped the commentary would take away more than it fixed". It is a
  manual command. The updater never runs it.
- **The unit needs a different answer, and the operator proved why in the same
  session.** They hand-edited their unit to relax three hardening settings so
  `sudo` works. An update that overwrote units would silently revert that on every
  release. **The existing rule — never overwrite a unit this installer did not
  write — is what protects them.**
- **But the current behaviour is silence.** Their unit has no recorded hash, so it
  will never be touched again and nothing ever says so. It still carries
  `ExecStart=%h/bin/crswd`, the path v0.80 fixed, and no `EnvironmentFile` line at
  all. **They are two fixes behind and have no way to find out.**
- **So the shape is `.pacnew`, not overwrite**: keep refusing to replace an edited
  unit, and stop being quiet about it — write the new one alongside and say so,
  with a way to see the difference.

**The test that matters:** an operator who relaxed `NoNewPrivileges` must still
have it relaxed after an update, and must be told a newer unit exists.

---

## Iteration 1 — 2026-08-12

**Did:** T001. `internal/updater/config.go` — a `ConfigMigrator` that rewrites the
operator's configuration into the current schema during an update: stage beside
their file, read the staged bytes back off disk, run them through `config.Validate`
(the same loader a startup uses), then back up the original to `config.bak` and
rename into place. `updateFromBrowser` calls it after a successful `Swap`.

**Learned — things the next iteration would otherwise rediscover:**

- **`internal/config` writes after all.** `migrate.go`'s header says "cmd/crswd is
  the only code in this repository that writes a config file"; that has been stale
  since the settings page shipped — `internal/config/write.go` has `WriteFile`,
  `Validate`, `BackupPath`, and `internal/httpapi/settings_edit.go` is a second
  writer. **`settings_edit.go` is the template to copy** for anything that writes
  the operator's file: validate → back up → write, all through `config.*`. I reused
  `config.WriteFile` for both the staged file and the backup rather than adding a
  third copy of `writeAndSync` — `cmd/crswd/config_cmd.go` has its own.
- **`config.Validate` needs a real environment.** It runs the whole loader, so a
  test fixture needs `CRSW_SHARED_SECRET` (64 chars is safe) *and* a resolvable
  `allowed_roots` — and it layers env **over** the file, so a fixture that sets
  `CRSW_ALLOWED_ROOTS` in the environment cannot then test a bad root in the file.
- **The one value that parses and does not load** is `allowed_roots` naming a
  directory that is not there: `parseFile` only checks grammar, keys and schema, so
  it sails through the migration and is refused by the loader. That is the whole
  fixture for "a migration that would not validate".
- **Where the migration could NOT go.** Startup is the obvious home and it is
  closed: FR-008 and `specs/004-configure-and-operate/quickstart.md` both say the
  daemon never writes the file it reads, and `cmd/crswd/config_cmd_test.go` asserts
  it. An update is the exception because the operator asked for it by name.
- `selfUpdate` now has a fourth member and `wired()` counts it, so a dropped wiring
  refuses loudly rather than quietly stopping carrying the file.

**Left:** T002–T007. T002 is next (ship the unit as a comparable release asset).

**Findings — noticed, not fixed:**

- **⚠️ The migration runs in the OLD binary, so a new release's schema changes land
  one update late.** `config.SchemaVersion` and `renamedKeys` come from the code
  that is running, and that is v-current, not v-next. A rename shipped in v0.90 is
  applied by the update *after* the one that installs v0.90. Today this costs
  nothing (`renamedKeys` is empty and `SchemaVersion` is 1) and it is what the plan
  asked for. The fixes, if it ever matters: exec the staged candidate's own
  `crswd config migrate` (T007 territory), or migrate on the first start after an
  update — which needs an exception to FR-008 that nobody has written yet.
  **Do not "fix" this silently; it is a spec question.**
- **`cmd/crswd/config_cmd.go` still has its own `writeConfigFile`/`writeAndSync`,
  duplicating `internal/config/write.go`.** Left alone on purpose (AR-008) — that
  is exactly T007's job, and T007 should collapse three write paths, not two.
- **`internal/config/migrate.go`'s header comment is wrong.** It claims cmd/crswd
  is the only writer in the repository. Two other writers exist now. One-line doc
  fix for the fix lane; not touched here.
- **Flaky test:** `TestTheOpeningScreenIsSentWithoutWaitingOutAnInterval`
  (`internal/httpapi/stream_test.go`) failed once on a loaded machine — "the opening
  screen arrived 14ms after the open, which is past the 10ms interval" — and passed
  on every rerun. It asserts a wall-clock deadline of 10ms, which a parallel suite
  on a busy host will miss regardless of the code. CI will hit this eventually.

---

## Iteration 2 — 2026-08-12

**Did:** T002. `internal/updater/unit.go` — `UnitAsset` (the release's own
`crswd.service`), install.sh's two paths, and `Unit.Standing(published)` answering
one of four: `UnitAbsent`, `UnitCurrent`, `UnitOurs`, `UnitTheirs`. Reads only;
nothing is written and nothing is fetched here.

**Learned — things the next iteration would otherwise rediscover:**

- **The delivery half of T002 was already done and needed nothing.** The release
  workflow publishes `dist/crswd.service`, SHA256SUMS covers it, and the signature
  covers SHA256SUMS. `Verify(UnitAsset, bytes, sums, sig)` works today —
  `Verify` takes the asset name, so there is no second code path to write.
  `TestThePublishedUnitIsDeliveredLikeEveryOtherAsset` pins it, including the
  unsummed-asset refusal. **T003 just fetches `updater.UnitAsset` alongside the
  tarball in `updateTo` and hands the bytes to `Standing`.**
- **Ownership is the recorded digest, never the published bytes**, and the
  comment in unit.go says why at length: an operator's unit differs from an
  *older* published unit exactly as it differs from a *newer* one, so comparing
  against the release refuses to correct any host that ever took a unit.
  `internal/release/install_test.go`'s `TestInstallNeverOverwritesEditedUnit`
  makes the same point from install.sh's side; read it before touching this.
- **`UnitTheirs` is the zero value on purpose.** A standing nobody computed reads
  as "leave that file alone". Do not reorder the `iota` block.
- **Paths come from `HOME`, not XDG**, because install.sh composes them from
  `$HOME` — `stage.go` already made the same choice for the staging directory.
  `TestUnitAssetAndPathsAreTheInstallersOwn` reads `readonly UNIT_ASSET|UNIT|UNIT_RECORD`
  out of install.sh; if those shell names ever move, move that pattern with them.
- **Test fixtures are cheap here.** `newUnitFixture(t, unit, record)` builds a
  whole host in a `t.TempDir()`; `nil` means the file is absent. `published(t)`
  in `verify_test.go` already carries the unit as a published asset — it now uses
  `UnitAsset` and the shared `publishedUnit` const rather than a literal.
- **Mutation-checked, not just green:** flipping the no-record branch to
  `UnitOurs` and drifting `unitRecordPath` each failed the new tests. Worth doing
  again for T003 — the branch that matters most there is the one that does nothing.

**Left:** T003–T007. T003 is next: fetch the unit during an update, act on the
standing, never overwrite `UnitTheirs`, and write `crswd.service.new` beside it.

**Findings — noticed, not fixed:**

- **⚠️ T003 has one decision T002 could not make: `UnitCurrent` with no record.**
  A host whose hand-written unit happens to be byte-identical to the published one
  is current, so there is nothing to write — but there is still no record, so the
  *next* release will read it as `UnitTheirs` and hand out a `.new`. Recording its
  digest then would claim ownership of a file this daemon did not write, which is
  the thing install.sh is careful never to do. Both answers are defensible; pick
  one deliberately in T003 and say why in the code.
- **`Unit` has no `wired()` seat yet.** `selfUpdate` in `internal/httpapi/update.go`
  counts its four members so a dropped wiring refuses loudly (see Iteration 1).
  T003 adds a fifth — add it to `wired()` too, or an update that stops carrying the
  unit will look exactly like one that had nothing to carry.
- **`internal/httpapi/render.go` is not gofmt-clean** (its `buildinfo` import sorts
  above the stdlib block). Pre-existing, untouched, and invisible to CI because
  AGENTS.md says Format runs nowhere but locally — `gofmt -l .` names it. One-line
  fix for the fix lane.
- **Still open from Iteration 1:** the migration runs in the *old* binary (schema
  changes land one update late); `cmd/crswd/config_cmd.go` still duplicates
  `internal/config/write.go` (T007); `internal/config/migrate.go`'s header comment
  claims cmd/crswd is the only config writer, which is wrong; and
  `TestTheOpeningScreenIsSentWithoutWaitingOutAnInterval` is wall-clock flaky.

---

## Iteration 3 — 2026-08-12

**Did:** T003. `internal/updater/place.go` — `Unit.Place(asset, sums, signature)`
acts on the standing T002 computes and returns a `UnitOutcome`: `UnitOurs` →
replace and re-record, `UnitAbsent` → install and record, `UnitTheirs` → write
`crswd.service.new` beside theirs and touch nothing else, `UnitCurrent` →
nothing. `updateTo` fetches `updater.UnitAsset` as a fourth asset and the handler
calls `Place` after `Swap`, beside the config migration.

**The two decisions T002 left open, and why:**

- **`UnitCurrent` records nothing**, even when there is no record. A record is
  what licenses the *next* release to replace a file, so writing one for a unit
  this daemon did not write is install.sh's "this refusal undoing itself one
  command later". Such a host is offered a `.new` at the release after this one,
  which is correct — by then it really has fallen behind.
- **`UnitAbsent` installs the published unit and records it**, which is
  install.sh's own first row: nothing to protect, nothing to take away, and what
  lands is inert until somebody runs `daemon-reload` and enables it.

**Learned — things the next iteration would otherwise rediscover:**

- **`Place` verifies its own bytes**; the caller cannot hand it unverified ones.
  `Unit` grew a `verify` seam exactly like `Stager`'s, defaulted to
  `updater.Verify` and pinned by `TestTheUnitIsVerifiedWithTheCommittedKey`. That
  is why the httpapi seam is `Place(asset, sums, signature)` and not "the route
  verifies then places" — update.go's header forbids a second copy of a check on
  the route side (FR-029b).
- **The unit is fetched with the other three, before the swap**, in install.sh's
  own order (tarball, SHA256SUMS, SHA256SUMS.sig, crswd.service). A release with
  no unit is refused while nothing on the host has changed. **Every tag from
  v0.58 publishes it**, so this costs no rollback — checked, not assumed.
- **`updateTo` now returns an `installed` struct**, not a version string: the
  unit bytes have to survive out to the handler, because the steps that cannot
  refuse an update run *after* the only irreversible line.
- **A `.new` is withdrawn on every path that makes it untrue.** After a
  replacement a leftover one names an *older* unit than the one just installed,
  which is worse than the silence this milestone set out to fix.
- **A replacement keeps the operator's mode.** A chmod does not change a digest,
  so a unit narrowed to 0600 on purpose still reads as ours; widening it back to
  install.sh's 0644 would be the same silent revert in the one dimension the
  ownership check is blind to. New files get 0644 (`unitMode`), the record 0600.
- **Writes go through `config.WriteFile`** rather than a fourth copy of
  write-to-a-temp-and-rename. See the finding below about its temp-file name.
- **Mutation-checked, five ways**, all caught: replacing a `UnitTheirs` unit,
  recording a `UnitCurrent` one, dropping the verification, dropping
  `withdrawOffer` from `settle`, and dropping `Place` from the route.

**Left:** T004–T007. T004 is next: say on the settings page which of the three
happened, with the `.new` filename and the `diff` command, reusing existing
classes.

**Findings — noticed, not fixed:**

- **⚠️ T004 needs an outcome the handler currently drops.** `Place`'s
  `UnitOutcome` is discarded at the call site on purpose — nothing renders it
  yet, and the page the handler is composing is the one that waits for the
  restart. **T004 has to decide where it comes from**: either the settings page
  recomputes a `Standing` at render time (which needs the published unit, i.e. a
  fetch on a page render — probably not), or the update persists what it did.
  Note `Unit.NewPath()` already exists for naming the file, and the file on disk
  is itself evidence: `crswd.service.new` being present *is* "a newer one is
  waiting". That may be all T004 needs, and it needs no new state.
- **`config.WriteFile`'s temporary file is named `.crswd-config-*`**, and
  place.go now writes systemd units through it. A leftover from a crash
  mid-write would be a `.crswd-config-…` file in `~/.config/systemd/user`, which
  names the wrong thing. Reusing the one tested atomic writer is still right —
  a fourth copy is what T007 exists to prevent — but the prefix should stop
  saying "config" when T007 collapses the write paths.
- **`config/write.go`'s header is now two callers out of date.** It says "Two
  callers reach it, both explicit: `crswd config migrate`, and the settings
  page's edit". `internal/updater/config.go` was the third and place.go is the
  fourth. Same fix-lane one-liner as `migrate.go`'s header.
- **The `quickstart` suite could not be run here**: `127.0.0.1:8765` is held by
  the deployed daemon, which AGENTS.md documents as that suite's requirement.
  `go vet -tags quickstart ./...` is clean, and so are `-tags tmux` and
  `-tags dev`.
- **Still open from Iterations 1 and 2:** the migration runs in the *old* binary;
  `cmd/crswd/config_cmd.go` duplicates `internal/config/write.go` (T007);
  `internal/config/migrate.go`'s header comment is wrong;
  `internal/httpapi/render.go` is still not gofmt-clean (`gofmt -l .` names it and
  nothing else); and `TestTheOpeningScreenIsSentWithoutWaitingOutAnInterval` is
  wall-clock flaky.

---

## Iteration 4 — 2026-08-12

**Did:** T004. `updater.Unit.Report()` reads this host's unit, the record beside
it and the offer beside that — no release, no network. `unitFactsOf` in
`internal/httpapi/settings.go` turns it into one of five sentences, and the
Updates section renders it as three rows of the **existing** `.version-facts`
list: the sentence, `Waiting at` naming `crswd.service.new`, and `Compare them`
carrying the `diff` command. No new CSS class, and no CSS change at all.

**The decision T004 inherited, and the answer:**

- **Where the outcome comes from: the files, not persisted state.** Iteration 3
  left this open. `Place`'s `UnitOutcome` is still dropped at the call site. The
  `crswd.service.new` an operator diffs *is* the claim "there is a newer unit
  than yours", so it is what decides what the page says — a second account
  written by the update would be free to go stale the moment somebody took the
  offer and deleted it. **No new state, and nothing fetched on a render.**
- **⚠️ The one place I did not do what T004 literally says.** T004 asks for
  "theirs is current". A page cannot honestly say that from disk: absence of an
  offer means *no update has left one*, which on a host that has never been
  updated — the operator this milestone is for — is exactly the false
  reassurance the milestone exists to end. So `unitSentenceTheirs` says "an
  update never replaces it. Nothing newer is *waiting* beside it." The stronger
  claim needs the published bytes, i.e. a fetch on a page render, which
  Iteration 3 already priced as "probably not". **If somebody wants the literal
  wording, the fix is to compare against the release on the existing Check
  (three more asset fetches plus a `Verify`), not to soften the sentence.**

**Learned — things the next iteration would otherwise rediscover:**

- **`unitCarrier` in `internal/httpapi/update.go` grew `Report()`**, rather than
  a second seam. One pair of files answered by two seams is two answers a page
  and an update could disagree about. `fakeUpdatePath` in `update_test.go`
  answers the zero report deliberately — the page assertions run against a
  **real** `updater.Unit` over a `t.TempDir()` home (`unitOnHost` in
  `settings_test.go`), because a fake would let the page and the updater agree
  about a host neither looked at.
- **`updatePanelFor` is the composer, and it returns `nil` with no page token.**
  So a mint failure costs the unit sentence as well as the update button. Left
  as is: that server has no forms at all. The nil check is on
  `s.updates.unit`, because `newServer` (every test in the package) wires no
  update path — `newWithLayer1` is the only constructor that does.
- **`.version-facts` was the whole answer to "reuse a class".** It is a `<dl>`
  documented as "two terms, two answers", so a fact stated as a `dt`/`dd` pair
  needs no name of its own. Adding a class would have needed a stylesheet rule,
  and `stylesheet_test.go` holds every value in it to `docs/design-system.md`.
- **`updater.Report` refuses rather than returning the zero `UnitReport`**, and
  the reason is the sentence it would produce: the zero value reads as "no unit
  on this host", whose sentence *promises an update will install one*. A read
  that failed has its own sentence, and `unitFactsOf` takes the error to pick it.
- **Mutation-checked, four ways**, all caught: composing `Offer` from the unit
  path instead of stat-ing it (failed 6 cases across both packages), dropping
  the two `.new` rows from the template, collapsing `UnitOurs` into
  `UnitTheirs`, and dropping the `panel.Unit` wiring entirely (failed all four
  page arrangements — the T001 lesson, tested).

**Left:** T005–T007. T005 is next: the same three facts at startup, into the
journal. It needs `updater.NewUnit(os.Getenv).Report()` in `cmd/crswd` beside the
absent-identity-provider warning — **the sentences are `internal/httpapi`
constants today**, so T005 either moves them somewhere both callers can reach or
writes the journal's own wording; picking the second means two vocabularies for
one fact, which is the drift T007 exists to collapse. Decide it deliberately.

**Findings — noticed, not fixed:**

- **`docs/components.md` gained the paragraph, `docs/design-system.md` did
  not**, and it needed none: no token, no rule, no new class. Worth knowing
  before T006 goes looking for a design doc to update.
- **The `diff` command is shell-quoted by `shellQuoted` in `settings.go`.** It
  is printed, never run — nothing in this daemon executes a shell — but an
  operator pastes it, and an unquoted path with a space in it is a command that
  silently diffs two other files. `TestTheDiffCommandSurvivesAHomeWithASpaceInIt`
  is the only thing in the tree that would notice: every path a test builds is
  under a temporary directory with no spaces in it.
- **The `quickstart` suite still could not be run here.** `127.0.0.1:8765` is
  held by the deployed daemon (checked with `ss -ltn`), which AGENTS.md
  documents as that suite's requirement. `go vet -tags quickstart ./...` is
  clean; `-tags tmux` and `-tags dev` were run in full and pass.
- **Still open from Iterations 1–3:** the migration runs in the *old* binary;
  `cmd/crswd/config_cmd.go` duplicates `internal/config/write.go` (T007);
  `internal/config/migrate.go`'s and `internal/config/write.go`'s header
  comments both name the wrong set of callers; `config.WriteFile`'s temp file is
  still named `.crswd-config-*` while place.go writes systemd units through it;
  `internal/httpapi/render.go` is still the only file `gofmt -l .` names; and
  `TestTheOpeningScreenIsSentWithoutWaitingOutAnInterval` is wall-clock flaky.

---

## Iteration 5 — 2026-08-12

**Did:** T005. `cmd/crswd/unit.go` — `sayWhatBecameOfTheUnit` writes this host's
unit standing to stderr, called from `run` immediately after the staging sweep.
The vocabulary moved out of `internal/httpapi/settings.go` into
`internal/updater/facts.go` (`UnitFacts`, the five `UnitSentence*` constants,
`DescribeUnit`, `unitCompare`, `shellQuoted`), so the page and the journal are
one implementation. The page now renders `updater.UnitFacts` and calls
`updater.DescribeUnit`; the template is untouched, because the three field names
are identical.

**The decision T004 left open, and the answer:**

- **Move the vocabulary, do not write a second one.** Iteration 4 handed this
  over explicitly. Two sets of words for one file is exactly the drift T007
  exists to collapse, and its shape here would be a page and a journal
  disagreeing about whether an update replaces this operator's unit — a question
  with no tie-breaker anywhere on the host. `internal/updater` is the home
  because it owns `UnitReport`, both callers already import it, and a startup
  banner about a systemd unit must not come out of the HTTP package.
- **The journal carries one thing the page does not: the read error.** The page
  says `UnitSentenceUnknown` and stops, because the error names a path on this
  disk. In the journal that detail is the whole value — "could not read the
  unit" with no reason is a line that sends somebody looking.

**Learned — things the next iteration would otherwise rediscover:**

- **`sayWhatBecameOfTheUnit(w, report, err)` takes three arguments, and the
  middle one cannot be spread.** Go refuses a multi-value call mixed with other
  arguments, so `run` does `unitReport, unitErr := ...Report()` on its own line.
  `updater.DescribeUnit(s.updates.unit.Report())` still spreads in httpapi
  because the report is its *only* argument.
- **`run` does not fail on a unit it cannot read**, and that is deliberate: the
  read error becomes a sentence. What *is* fatal is a write that fails, on
  `warnNoIdentityProvider`'s terms — a stream that will not take a line has
  nowhere to report anything below that point, and the alternative is a
  swallowed error.
- **`cmd/crswd/main_test.go` already had the AST-wiring pattern** —
  `TestStartupDiagnosticsGoToStderr` parses main.go, finds the call by name and
  checks `render(call.Args[0]) == "os.Stderr"`. `TestStartupSaysWhatBecameOfTheUnit`
  is that shape, and it is the assertion this milestone is about: not that the
  code exists, but that a start runs it.
- **The banner is safe for the documented `grep '^{' | jq` filter** — every line
  is prefixed `crswd: `. `TestDocumentedCommandParses` (quickstart) asserts the
  trail contains a `crswd: ` diagnostic *and* that the filter still parses, so it
  gets stronger rather than weaker from this.
- **The page test no longer builds its expected diff command with the
  implementation's own helper.** It spells `diff '<unit>' '<offer>'` out, so a
  page rendering some other command fails instead of agreeing with itself. The
  journal test does the same.
- **Mutation-checked, six ways**, all caught: dropping the call from `run`
  (wiring test, 0 calls), pointing it at `os.Stdout` (that test *and*
  `TestDiagnosticsGoToStderr`), dropping the read-error line, dropping the
  waiting/compare lines, swallowing the write error, and collapsing
  `UnitTheirs` into `UnitAbsent` — that last one failed in all three packages,
  which is the point of there being one vocabulary.

**Left:** T006 (README + deploy/README) and T007 (one config-write
implementation). T006 is next.

**Findings — noticed, not fixed:**

- **T006 has a doc question to answer, not just prose to write.** T005 gave the
  journal the read error and the page nothing; if `deploy/README.md` is going to
  tell an operator how to find out where their unit stands, it should say which
  of the two to read and why they differ.
- **`internal/httpapi/settings.go` lost ~115 lines and gained an import it
  already had.** Nothing else in that file used `shellQuoted`, so no caller was
  left behind — checked by grep before the move, and by the build after.
- **The `quickstart` suite still could not be run here.** `127.0.0.1:8765` is
  held by the deployed daemon (`ss -ltn`), which AGENTS.md documents as that
  suite's requirement. `go vet -tags quickstart ./...` is clean; `-tags tmux` and
  `-tags dev` were run in full and pass.
- **Still open from Iterations 1–4:** the migration runs in the *old* binary;
  `cmd/crswd/config_cmd.go` duplicates `internal/config/write.go` (T007);
  `internal/config/migrate.go`'s and `internal/config/write.go`'s header
  comments both name the wrong set of callers; `config.WriteFile`'s temp file is
  still named `.crswd-config-*` while place.go writes systemd units through it;
  `internal/httpapi/render.go` is still the only file `gofmt -l .` names; and
  `TestTheOpeningScreenIsSentWithoutWaitingOutAnInterval` is wall-clock flaky.

---

## Iteration 6 — 2026-08-12

**Did:** T006. `README.md` gains *The other two files an update carries* under
*Updating, and rolling back* — the migration (staged, loaded, `config.bak`,
discarded if it would not load) and the unit's four branches as a table, with the
`diff`/`cp`/`daemon-reload`/`restart` that takes a `.new`. `deploy/README.md`
gains *What an update does to this file* under *Why the unit looks the way it
does*. `TestTheOperatorPagesNameTheFilesThisPackageWrites` in
`internal/updater/unit_test.go` holds both pages to `unitPath`, `unitRecordPath`
and `UnitAsset + newSuffix`.

**The question T005 left open, and the answer:**

- **Which of the two accounts to read: either, and the journal when the read
  failed.** They are one read of the same two files through
  `updater.DescribeUnit`, so neither is fresher. The single difference is stated
  in `deploy/README.md`: a failed read names a path on this disk and why, which
  is a diagnostic for whoever administers the host rather than something a
  browser is owed — so the page says only that it happened.

**The one thing documented beyond the task's literal ask**, deliberately: **how
to hand a unit over.** T006 asks how to *take* a `.new`, and the honest answer
raises the next question immediately — a hand copy writes no record, so the unit
stays the operator's and the next differing release offers again. `deploy/README.md`
therefore also documents writing the digest into
`~/.local/share/crswd/crswd.service.sha256` by hand, with the price stated in the
same breath (every future update then replaces that file with no `.new` and no
diff). Verified `sha256sum < file | cut -d' ' -f1` produces exactly what
install.sh's `${sum%% *}` records, and the recipe `mkdir -p`s the directory
because a host whose unit the installer never placed has never had one made.

**Learned — things the next iteration would otherwise rediscover:**

- **⚠️ A line beginning `journalctl` in either README is executed by the
  quickstart suite.** `trailCommands` in `cmd/crswd/quickstart_test.go` collects
  every line whose trimmed text (leading `#` stripped) starts with `journalctl`
  from `README.md` and `deploy/README.md`; `filterOf` then *fatals* unless it
  contains `--user`, `-u crswd`, `-o cat` **and** a pipe, and the command must
  both survive a stream carrying `crswd: ` diagnostics and **reject** a truncated
  record. `… | grep 'crswd: '` would fail that second half. A leading backtick
  saves an inline mention, which is why every journal command added here is prose
  rather than a fenced line.
- **That sweep is runnable on this host even though the rest of `quickstart` is
  not.** `go test -tags quickstart ./cmd/crswd -run TestEveryDocumentedTrailCommandSurvivesTheStream`
  passes — it takes its own port; only the two startup cases need `127.0.0.1:8765`,
  which the deployed daemon still holds. Worth running for any README change.
- **Two other tests read these pages**: `internal/release/readme_test.go` (the
  install one-liner must come before any `go build`/`git clone`, and the rollback
  path, `POST /dashboard/update` and `version=` must all appear) and
  `internal/config/deployexample_test.go` (only fenced blocks containing
  `/.config/crswd/env` are scanned for `CRSW_` assignments — a new block is
  invisible to it unless it names that file).
- **`os.ReadFile` over a loop variable needs a `//nolint:gosec // G304` even in a
  test.** The linter is v2.12.2 (#26 checked) and flagged it.
- **Mutation-checked:** flipping `newSuffix` to `.pacnew` failed the new test on
  both pages, which is the drift it exists to catch — a page naming an offer that
  is not there is the "difference nobody can see" this milestone is about.

**Left:** T007 — one config-write implementation shared by `crswd config migrate`
and the update path. That is the last task in the plan.

**Findings — noticed, not fixed:**

- **T007 now has four write paths to consider, not three.** `cmd/crswd/config_cmd.go`'s
  `writeConfigFile`/`writeAndSync`, `internal/config/write.go`'s `WriteFile`,
  `internal/updater/config.go` (staged + backup) and `internal/updater/place.go`
  (units and the record) all go through or around the same idea. Note the
  documentation now *claims* the update runs `crswd config migrate` "for you";
  after T007 that should be literally true rather than nearly so.
- **`README.md` documents `crswd.service.new` as never loadable by systemd**
  (a unit must end in `.service`). That is true of systemd, not enforced here —
  if `newSuffix` ever changes, it has to keep that property, and the new test
  checks only that the pages agree with the constant.
- **The `quickstart` suite as a whole still could not be run**: `ss -ltn` shows
  `127.0.0.1:8765` held by the deployed daemon. `go vet -tags quickstart ./...`
  is clean; `-tags tmux` and `-tags dev` were run in full and pass, as did
  `golangci-lint run` (0 issues).
- **Still open from Iterations 1–5:** the migration runs in the *old* binary;
  `cmd/crswd/config_cmd.go` duplicates `internal/config/write.go` (T007);
  `internal/config/migrate.go`'s and `internal/config/write.go`'s header comments
  both name the wrong set of callers; `config.WriteFile`'s temp file is still
  named `.crswd-config-*` while place.go writes systemd units through it;
  `internal/httpapi/render.go` is still the only file `gofmt -l .` names; and
  `TestTheOpeningScreenIsSentWithoutWaitingOutAnInterval` is wall-clock flaky.

---

## Iteration 7 — 2026-08-13

**Did:** T007, the last task. `config.MigrateFile` in `internal/config/write.go`
is now the whole rewrite — stat for the mode, read, `config.Migrate`, stage at
`config.migrating`, read back, offer to the caller's `accept`, write
`config.bak`, rename — and both callers are it. `internal/updater`'s
`ConfigMigrator.Migrate` is four lines and a `wouldStillStart` predicate;
`cmd/crswd`'s `configMigrate` is the `ReadFile` check, the call, and the report.
`writeConfigFile`/`writeAndSync`/`readFileAndMode` are gone from `cmd/crswd`, so
`config.WriteFile` is the only atomic write of an operator's file left in the
tree.

**The one thing the two callers still choose for themselves, and why:**

- **The check on what landed is a parameter, not a shared constant.** An update
  asks `config.Validate` — the loader, in the daemon's own environment, moments
  before the restart that would otherwise discover the answer. The command asks
  `config.ParseFile` instead, because `configCheck`'s own comment already
  settles it: values are checked against the environment the *daemon* runs in,
  and a migration refused because the operator's terminal carries no
  `CRSW_SHARED_SECRET` would be refusing a file the daemon starts on perfectly
  well, and would teach them to stop migrating. **Sharing the loader here would
  have been the drift T007 exists to end, pointed the other way.**
- **`crswd config migrate` therefore gained the staging and the read-back**, and
  that is a real behaviour change, not a refactor: it used to write the backup
  and then write over the operator's file with bytes nothing had looked at
  again.

**Learned — things the next iteration would otherwise rediscover:**

- **`accept`'s error is returned verbatim**, which is what keeps
  `updater.ErrConfigWouldNotLoad` where its meaning is. `errors.Is` still reaches
  it through `MigrateFile`, and `internal/httpapi/update.go`'s two cases needed
  no change at all. A sentinel moved into `internal/config` would have had to
  mean "would not load" for a caller that cannot ask that question.
- **`config.StagedPath(path)` exists for the same reason `BackupPath` does**:
  `internal/updater/config_test.go` asserts nothing was left at that name, and a
  test spelling `.migrating` itself would pass vacuously the day the constant
  moved.
- **`configMigrate` reports on `(migrated, err)` and not on `err` alone.**
  `MigrateFile`'s deferred cleanup can join an error onto a *successful*
  migration; "your file is unchanged" would then be a lie that sends the
  operator to the wrong file. It is the only branch there where `migrated` is
  true and `err` is not nil.
- **The quickstart leftover check was asserting nothing.** It looked for
  `.tmp-`, which was `cmd/crswd`'s own writer's prefix; `config.WriteFile` never
  wrote such a name. It now names `.migrating` and `.crswd-tmp-` — and the
  temporary file is `.crswd-tmp-` rather than `.crswd-config-` because
  `place.go` writes systemd units through the same call (Iteration 3's finding,
  which was handed to this task).
- **Mutation-checked, four ways**, all caught: renaming into place before
  `accept` sees the bytes (failed in *both* packages), dropping the staged
  file's removal (both again), and each half of the drift guard — a caller that
  stops calling `MigrateFile`, and a caller that grows an `os.Rename` of its own.
- **The guard is an AST scan of two files by path** (`../../cmd/crswd/config_cmd.go`,
  `../updater/config.go`) from `internal/config`'s own test, where
  `docs_test.go` already reads `../../README.md`. It parses rather than compiles,
  so it still runs when the file it is judging does not build.

**Left:** nothing in the plan. T001–T007 are all ticked.

**Findings — noticed, not fixed:**

- **⚠️ Still open, and it is a spec question, not a bug:** the migration runs in
  the **old** binary, so a rename shipped in v0.90 is applied by the update
  *after* the one that installs v0.90. Today that costs nothing (`renamedKeys`
  is empty, `SchemaVersion` is 1). T007 makes one of the two fixes cheaper —
  `crswd config migrate` in the staged candidate is now the same code the
  updater runs, so exec-ing it would change *when* it runs and nothing else —
  but which fix is right needs somebody to decide. **Do not "fix" it silently.**
- **`internal/httpapi/settings_edit.go` was left alone** (AR-008). It writes one
  key rather than migrating a schema, and it validates *before* it writes rather
  than after, which it can because it has the daemon's environment and no
  rewrite to stage. It already goes through `config.WriteFile`, so it is not a
  fourth write path — it is a different operation through the one writer.
- **`README.md:801` says `config migrate` is "the only thing that rewrites a
  configuration file wholesale".** Line 198 already says an update is that
  command run for you, so the two read consistently — but if anyone edits either
  sentence, they are now describing one implementation and should say so.
- **`internal/httpapi/render.go` is still the only file `gofmt -l .` names**, and
  `TestTheOpeningScreenIsSentWithoutWaitingOutAnInterval` is still wall-clock
  flaky (10ms deadline). Both are fix-lane one-liners nobody has taken.
- **The `quickstart` suite as a whole still could not be run**: `127.0.0.1:8765`
  is held by the deployed daemon. `go vet -tags quickstart ./...` is clean and
  the four cases that take their own port were run in full and pass
  (`TestMigrateKeepsBackup`, `TestConfigCheckDoesNotStart`,
  `TestFallsBackToBackupLoudly`, `TestEveryDocumentedTrailCommandSurvivesTheStream`).
  `-tags tmux` and `-tags dev` were run in full and pass, as did
  `golangci-lint run` (v2.12.2, 0 issues).

---

Every task in the plan is checked and the tree is green: build, vet, `go test ./...`,
`-tags tmux`, `-tags dev` and `golangci-lint run` all pass, as do the quickstart
cases that do not need the port the deployed daemon holds.

RALPH_COMPLETE

---

## Spec 012 — session revival (2026-08-22)

**Why it exists.** A session died on 22 August and nothing noticed until somebody
asked. The kernel OOM killer took the whole `tmux-spawn-…` cgroup — Claude, its
login shell and its tmux session together — at 08:16:10Z, on a host that had been
up five days and whose tmux server never restarted. crswd knew the session was
dead and only ever said so in answer to a request.

**What was built.** A supervisor sweeping beside the reaper on the same cadence,
one `tmux list-sessions` per sweep. Two deaths, one answer: Claude exiting leaves
the login shell, so the command is typed again with `--resume`; the shell being
destroyed leaves nothing, so a new one is built under the same session identity,
marked owned before anything is typed into it. A durable append-only journal
carries the conversation identifier and the attempt count across the loss of the
shell. Three attempts per death at 5s / 30s / 3m, then `failed` on the card. And
the create form's conversation list now follows the directory actually chosen.

**Two questions were put to the operator rather than guessed** (Principle II).
Whether to attempt a clean-exit distinction — no, the start command is typed into
a login shell so no exit status exists, and destroy is the only final signal. And
the scope boundary, which was then decided against journal evidence rather than
preference: the observed failure destroys the tmux session, so the resume handle
cannot live only on it.

### Three defects the tests found, not a reviewer

- **A pipe in a pane command shifts every field on a `list-sessions` row**, not
  the field beside it. The first cut put `#{pane_current_command}` on the row and
  compared in Go; writing the corruption case proved the parser reads from the
  right, so one extra separator moves everything. tmux does the comparison now —
  `#{?#{@crswd-binary},#{==:#{pane_current_command},#{@crswd-binary}},?}` — and
  answers in one character. Verified against real tmux 3.4.
- **`--session-id` was being injected into every configured start command.**
  `internal/config` accepts any command line; there is no rule that one must run
  Claude. The `-tags tmux` suite runs a `seq`-based command and produced
  `seq: unrecognized option '--session-id'`. Minting is now gated on the start
  binary, and a start command this daemon cannot give an identifier to gets none
  and is supervised without being revived by one.
- **Two daemons sharing a home replayed each other's journals.** Found by the
  acceptance suite on its first run: daemon B replayed daemon A's records, found
  those sessions absent from its *own* tmux server — correctly, they were on the
  other one — and stood ready to recreate all of them. The journal is now named
  after the listen address, exactly as the tmux server already was.

### One pre-existing fragility corrected

`TestTheLeakSuiteReallyDrivesTheDaemon` asserted two independently-minted page
tokens were byte-equal. A page token is `expiry + mac` where expiry has
one-second granularity, so two renders either side of a second boundary
legitimately differ; it passed only while a create fitted inside one second, and
the journal's per-record fsync was enough to end that. It now asserts the created
page carries a page token rather than the same bytes.

### Verification

Everything in `AGENTS.md` was run and passes: `go build ./...`, `go vet ./...`,
`go test ./...`, `go test -tags tmux ./...`, `go test -tags dev ./...`,
`go test -tags quickstart ./cmd/crswd` (the **whole** suite this time, 35s — the
port was free), `gofmt -l .` clean, and `golangci-lint run` with 0 issues.

**What was not verified here.** The 30-second revival sweep end to end. The
acceptance case asserts everything the sweep depends on — a real create writing a
real conversation identifier and binary onto a real tmux session, and a journal
recording it without a credential — but a test that waited for a timer would
spend half a minute asserting a timer. Revival itself is `quickstart.md`
scenarios 1 and 2, by hand, against a running daemon.

RALPH_COMPLETE
