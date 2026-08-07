# Tasks: Configure and operate it

**Input**: Design documents from `specs/004-configure-and-operate/`
**Prerequisites**: [plan.md](./plan.md), [research.md](./research.md), [contracts/](./contracts/)

**Tests are included in every behavioural task**, never split out. A task that
leaves a failing test behind is reverted by step 6 of `ralph/PROMPT.md`.

---

## 🔒 Four tasks are security-critical

Flagged inline as **[SECURITY]**. These are where a weaker model is riskiest, and
they are worth reviewing by hand or running on a stronger model:

| Task | Why it is the dangerous one |
|---|---|
| **T001** `IsSecret` | Shared by the permission refusal *and* the page render. A disagreement between those two is invisible until it matters — the page confidently prints something the permission check thought too sensitive to leave group-readable. |
| **T007** the precedence shim | Ordering *is* the security property. Reversed, a stale file silently overrides the environment a container was configured with. |
| **T011** secret rendering | The one page that holds every secret at render time. A "helpful" masked prefix is still a disclosure. |
| **T017** the mode toggle's validation | The only new route that takes a value naming something to run. If a command line can arrive from a browser, FR-030 is gone in both directions. |

## Carry-forward is first-class

Four abandoned lane branches hold ~3,800 lines. Each **builds standing alone**;
each broke only against a `main` that has since moved. Where a task says carry
forward, the work is a **rebase-and-reconcile, not a rewrite** — read the branch
first, and preserve its comments, which are the best documentation of why the
format is what it is.

| Branch | Carries |
|---|---|
| `claude/issue-issue-65-20260807-0112` | `internal/config/file.go`, `file_test.go` |
| `claude/issue-issue-42-20260805-1832` | the four 303 handlers, `internal/httpapi/outcome.go`, the banner partial |
| `claude/issue-issue-60-20260806-0406` | the card split, the rename disclosure |
| `claude/issue-issue-59-20260807-0055` | **the discovery walk only** — its hand-rolled combobox is replaced by markup per research R6 |

---

## Phase 1: Foundational (blocks every story)

- [ ] T001 🔒 **[SECURITY]** Add `IsSecret(key string) bool` to new file `internal/config/secret.go`, returning true for exactly `shared_secret` and `access_allowed_emails` and false for every other key. Test in `internal/config/secret_test.go`: `TestIsSecretNamesBothSecretKeys` asserts both return true and `allowed_roots` returns false; `TestIsSecretIsTheOnlyClassifier` walks `internal/config/` with `go/ast` and asserts no other function decides secrecy. **Must fail when** a second list of secret keys is introduced anywhere, or when only `shared_secret` is classified.

- [ ] T002 [P] Add the `Source` type to new file `internal/config/source.go` with constants `SourceDefault`, `SourceFile`, `SourceEnv`, `SourceFlag` (in that iota order) and a `String()` returning `"default"`, `"file"`, `"environment"`, `"flag"`. Test in `internal/config/source_test.go`: `TestSourceStringsAreTheSettingsPageVocabulary` asserts the four exact strings. **Must fail when** a fifth source is added without the settings page learning about it.

---

## Phase 2: US1 — Configure the daemon in a file (P1) 🎯 MVP

**Goal**: An operator changes any setting by editing one file and restarting,
without editing a service definition.

**Independently testable**: write a config file, restart, observe the value take
effect; delete it, restart, observe today's behaviour exactly.

**Decomposed into six tasks.** Carrying #65 as one entry is precisely the failure
this milestone exists to escape.

- [ ] T003 [US1] Carry forward `internal/config/file.go` and `file_test.go` from branch `claude/issue-issue-65-20260807-0112` and rebase onto current `main`, keeping **only** the grammar: whole-line `#` comments, `strings.Cut` on the **first** `=`, both sides trimmed, list values comma-separated on one line, keys derived as the environment variable minus `CRSW_` lower-cased. Tests per [contracts/config-file.md](./contracts/config-file.md): `TestParseAcceptsWorkedExample`, `TestValueMayContainHash`, `TestValueMayContainEquals`, `TestWhitespaceAroundSeparatorIgnored`, `TestWhitespaceInsideValuePreserved`. **Must fail when** the parser splits on the last `=`, or strips trailing `#` — a secret containing `#` would be silently truncated into a daemon that starts, looks healthy, and rejects every request.

- [ ] T004 [US1] Add the file-level refusals to `internal/config/file.go`, each naming the file and line and **never the value**: unknown key `config file %s:%d has unknown key %q; refusing to start`; renamed key as a *warning* naming both spellings; malformed line; repeated key; future schema version. Tests in `internal/config/file_test.go`: `TestUnknownKeyRefuses`, `TestRenamedKeyWarnsAndAccepts`, `TestMalformedLineRefuses`, `TestRepeatedKeyRefuses`, `TestFutureVersionRefuses`, `TestErrorNeverContainsValue`. **Must fail when** an unknown key is skipped, warned, or accepted; when a rename is treated as an unknown key; or when any message is built with `%q` on the value or the raw line.

- [ ] T005 🔒 **[SECURITY]** [US1] Add the permission refusal to `internal/config/file.go`, using `config.IsSecret` from T001 — refuse when `info.Mode().Perm()&0o077 != 0` **and** the file contains a secret key, with `config file %s is mode %04o, so it is readable by other accounts on this host and may hold the shared secret; run chmod 600 %s`. Tests: `TestGroupReadableWithSecretRefuses` and `TestGroupReadableWithoutSecretStarts` (mode 0644 holding only `allowed_roots` **starts**). **Must fail when** the mode check is dropped, checks only world, or ignores whether a secret is present — a refusal the operator cannot act on sensibly is a bug in the other direction.

- [ ] T006 [US1] Make an absent file a non-error in `internal/config/file.go`: return empty and nil. Test `TestMissingFileIsNotAnError` in `internal/config/file_test.go`, plus `TestParserNeverWrites` asserting the file's bytes and mtime are unchanged after a parse. **Must fail when** absence becomes a refusal — that breaks SC-002 and every existing deployment.

- [ ] T007 🔒 **[SECURITY]** [US1] Wire the file as a fallback `getenv` in `internal/config/config.go`, adding `withFile` per [contracts/config-precedence.md](./contracts/config-precedence.md): environment first, file second, `""` meaning default. Add nothing else — no bound, default or refusal may be written twice. Tests in `internal/config/source_test.go`: `TestEnvBeatsFile`, `TestFileBeatsDefault`, `TestEmptyEnvValueDoesNotBeatFile`, `TestFileValueIsValidatedIdentically`, `TestNoFileMatchesTodayExactly`. **Must fail when** the file is merged over the environment; when the file is parsed but never consulted (the exact bug left on the abandoned branch); or when the file layer adds validation of its own.

- [ ] T008 [US1] Record provenance in the same shim in `internal/config/config.go`, writing `map[string]Source` keyed by environment-variable name as each lookup is answered. Tests: `TestSourceRecordedForEveryKey` (every `CRSW_` constant in `config.go` has a `Source` after `Load`), `TestSourceIsNotInferred` (a value equal in file and env still reports `SourceEnv`), `TestSecretNeverInProvenanceLog`. **Must fail when** provenance is computed by comparing sources after the fact — a value present and equal in both is indistinguishable by comparison, and that is exactly when an operator is asking why their edit did nothing.

- [ ] T009 [US1] Add `crswd config check` and `crswd config migrate` to `cmd/crswd/`. `check` parses and reports without starting; `migrate` is the **only** code in the repository that writes a config file, and keeps a backup at `config.bak`. Add `config.bak` fallback on load failure, announced loudly. Tests in `cmd/crswd/config_cmd_test.go` (`-tags quickstart`): `TestConfigCheckDoesNotStart`, `TestMigrateKeepsBackup`, `TestFallsBackToBackupLoudly`. **Must fail when** any code path outside `migrate` writes the operator's file (FR-008).

---

## Phase 3: US2 — See what it is configured to do (P2)

**Goal**: An operator answers "why was my working directory refused?" and "why is
my change not applied?" from one page, without reading a log.

**Independently testable**: open `/settings`, read the roots, read each value's
source.

- [ ] T010 [US2] Add the read-only settings route in new file `internal/httpapi/settings.go`: path `/settings`, method `GET` **only**, audit action `settings.view`, behind the same identity middleware as every other page. Tests in `internal/httpapi/settings_test.go`: `TestSettingsRequiresIdentity`, `TestSettingsEmitsExactlyOneAuditRecord`, `TestNoMutatingVerbRegistered` (POST, PUT, PATCH, DELETE all 405). **Must fail when** the page is registered outside the identity middleware, when the handler audits per row or not at all, or when any edit route is added — editing is out of scope this milestone, and a route that does not exist cannot be exploited.

- [ ] T011 🔒 **[SECURITY]** [US2] Render secrets as `present` or `absent` in new file `web/templates/settings.html`, gated by `config.IsSecret` from T001 — never a value, length, prefix, suffix, or hash. Tests: `TestSettingsNeverRendersSecretValue`, `TestSecretRendersPresentOrAbsent`, `TestAllowedIdentitiesTreatedAsSecret`. **Must fail when** a "masked" value like `hun…` is introduced, or when only `shared_secret` is treated as secret.

- [ ] T012 [US2] Render one row per key in `web/templates/settings.html` with columns key, value, source, in the order `config.go` declares them, and name the file that was read above the table — `Read from %s` or `No configuration file was read.` Tests: `TestShowsSourcePerKey`, `TestNamesConfigFileRead`, `TestSaysWhenNoFileRead`. **Must fail when** sources are inferred at render time instead of read from T008's map.

- [ ] T013 [US2] Add `TestFullRouteSweepLeaksNoSecret` to `internal/httpapi/settings_test.go`: exercise **every** registered route and search all response bodies and all audit records for the configured secret value. **Must fail when** any page or error path prints one. This is SC-005 and it is the test that catches a leak nobody thought to look for.

---

## Phase 4: US3 — The dashboard behaves without script (P3)

**Goal**: All four actions leave the browser on a usable page with scripting
disabled. This is the white-page bug that made the milestone necessary.

- [ ] T014 [US3] Carry forward the post-redirect-get work from branch `claude/issue-issue-42-20260805-1832` and rebase onto current `main`: the four routes answering `303`, `internal/httpapi/outcome.go`'s fixed outcome vocabulary, the banner partial, and the create form's configured roots. **This is a rebase-and-reconcile, not a rewrite** — the handler work is done and correct. Reconcile against the toast and delegated-submit work that landed on `main` afterwards.

- [ ] T015 [US3] Finish the ~19 tests in `internal/httpapi/` that still assert the old fragment responses, updating each to assert `303` plus the redirect target. Add `TestRefusalIsNotARedirect` asserting an unauthorised action gets the uniform refusal. **Must fail when** a refusal is answered with a redirect — sending an unauthorised caller somewhere tells them their request was processed (FR-025).

- [ ] T016 [US3] Add `TestAllFourActionsUsableWithoutScript` to `internal/httpapi/actions_test.go`, asserting each of create, destroy, compact and rename answers `303` to a page that renders the outcome in words from `outcome.go`'s fixed vocabulary. **Must fail when** an outcome is built from caller-supplied text (FR-022).

---

## Phase 5: US4 — Turn remote control on and off (P4)

**Goal**: Remote control toggles on a running session and the conversation
survives.

- [ ] T017 [US4] Persist the start-command **name** as a fifth tmux user option `@crswd-start` in `internal/session/manager.go`, joining `@crswd-managed`, `@crswd-owner`, `@crswd-name` and `@crswd-workdir`. A restored session with no `@crswd-start` is `ModeLocal`, not an error. Tests in `internal/session/mode_test.go` (`-tags tmux`): `TestStartCommandSurvivesRestart`, `TestRestoredSessionWithoutOptionIsLocal`. **Must fail when** the fifth option is not written, or absence is treated as a failure — sessions started before this milestone must still adopt cleanly.

- [ ] T018 [US4] Add `func (s Session) Mode() Mode` to `internal/session/session.go`, **derived** from `StartCommand` against the configured `remote_start_commands` list. Refuse at startup when a name in `remote_start_commands` is absent from `start_commands`. Tests: `TestModeDerivedFromStartCommand`, `TestModeNotInStartCommandsRefusedAtStartup`. **Must fail when** a second stored field is introduced — two fields that must agree are two fields that can disagree — or when the mismatch is deferred to runtime.

- [ ] T019 🔒 **[SECURITY]** [US4] Add the toggle route to `internal/httpapi/actions.go`: `POST /dashboard/sessions/{id}/mode`, form field `mode` accepting **only** the literals `local` and `remote`, form field `confirm` which must equal `yes`, audit action `session.mode`, answering `303` on success. Tests in `internal/httpapi/actions_test.go`: `TestArbitraryModeValueRefused` (`mode=claude --dangerously-skip-permissions` → uniform refusal), `TestToggleRequiresConfirm`, `TestToggleEmitsExactlyOneAuditRecord`, `TestToggleCrossSiteBothHalves`. **Must fail when** the value is passed through as a command, when the confirming step is dropped, or when a test **disables** either cross-site half instead of satisfying it (AR-005).

- [ ] T020 [US4] Implement the transition in `internal/session/manager.go`: end and restart the process inside the pane **without** ending the session, passing `--continue`, preserving the tmux session, its window and its scrollback, and leaving the identifier, credential and lifetime unchanged. Tests (`-tags tmux`): `TestTogglePreservesSessionAndScrollback`, `TestTogglePassesContinue`, `TestToggleKeepsIdentifierAndLifetime`. **Must fail when** the implementation destroys and recreates the session, or omits `--continue` — the conversation is lost and SC-007 fails.

- [ ] T021 [US4] Show the mode on the card in `web/templates/partials/session-card.html`, **textually**. Test `TestCardShowsMode` in `internal/httpapi/partials_test.go`. **Must fail when** mode is shown as a coloured dot — state is never conveyed by colour alone (FR-059).

---

## Phase 6: US5 — Pick a working directory (P5)

**Goal**: A working directory can be chosen without typing, and any path can
still be typed.

**Decomposed into three tasks**, per the hard obligation.

- [ ] T022 [US5] Replace the working-directory field in `web/templates/partials/create-form.html` with `<input type="text" name="workdir" list="workdir-suggestions">` plus a `<datalist id="workdir-suggestions">`. **Do not carry the hand-rolled combobox from branch `claude/issue-issue-59-20260807-0055`** — 225 lines of `crswd.js` reimplementing filtering, focus management and ARIA roles the platform already provides correctly, which degrade to nothing with scripting off. Tests in `internal/httpapi/partials_test.go`: `TestPickerWorksWithoutScript`, `TestAnyPathStillTypeable`, `TestNoSuggestionsRendersPlainField`. **Must fail when** the control becomes script-dependent, or a `<select>` replaces the input — FR-040 requires any path to remain typeable.

- [ ] T023 [US5] Carry forward the discovery walk from branch `claude/issue-issue-59-20260807-0055` into new file `internal/config/discover.go`: subdirectories **one level** below each approved root, behind `discover_roots`, **off by default**. Tests in `internal/config/discover_test.go`: `TestDiscoveryOffByDefault`, `TestDiscoveryListsOneLevel`, `TestDiscoveryNeverLeavesRoots`. **Must fail when** discovery defaults on, the walk recurses, or a symlink out of a root is listed.

- [ ] T024 🔒 **[SECURITY]** [US5] Add `TestChosenPathValidatedIdentically` to `internal/httpapi/actions_test.go`: a path present in the datalist but **outside** the allowlist is refused, with the same refusal and the same audit record as a typed one. **Must fail when** the handler trusts a value because it was suggested. A suggestion is never an authorisation — this is the one real vulnerability the picker could introduce.

- [ ] T025 [US5] Announce a filtered subset in `web/static/crswd.js` per FR-045, as an enhancement over a control that already works. Test `TestSubsetAnnounced` in `internal/httpapi/stylesheet_test.go`. **Must fail when** the announcement is the thing that makes the control function rather than an addition to it.

---

## Phase 7: US6 — The card's two halves (P6)

- [ ] T026 [US6] Carry forward the card split from branch `claude/issue-issue-60-20260806-0406` into `web/templates/partials/session-card.html`, reconciling with the toast and anchor work that landed on `main` afterwards: one anchor covering the whole readable block, no control inside it, a boundary that is structural and not colour alone. Tests: `TestCardHasExactlyOneAnchor`, `TestAnchorCoversReadableBlock`, `TestNoControlInsideAnchor`, `TestBoundaryIsNotColourAlone`. **Must fail when** a second link is added — the recurring regression here — or the anchor wraps the name alone.

- [ ] T027 [US6] Move rename off the fleet and onto the session's own page as a disclosure in `web/templates/session.html`, revealed on request rather than resident. Tests: `TestRenameAbsentFromFleet`, `TestRenameOnSessionPageIsDisclosure`. **Must fail when** rename returns to the card (FR-049) or becomes a resident field (FR-050).

- [ ] T028 [P] [US6] Stop a text selection inside the anchor from navigating, in `web/static/crswd.js`. Test `TestSelectionDoesNotNavigate`. **Must fail when** this becomes a functional dependency rather than a papercut fix — the card must still work with no script.

---

## Phase 8: Dependency check (#71)

- [ ] T029 Add startup probes in new file `internal/config/depcheck.go`: `tmux` via `exec.LookPath` is **fatal**; the first word of each `start_commands` entry is a **warning**. Tests in `internal/config/depcheck_test.go`: `TestMissingTmuxRefusesToStart`, `TestMissingStartCommandWarnsOnly`, `TestChecksConfiguredCommandNotClaude`, `TestProbesFirstWordOnly`. **Must fail when** the check hardcodes `claude` (FR-015), warns about tmux instead of refusing, or promotes the start-command warning to fatal.

- [ ] T030 Derive the install command from `/etc/os-release` and `runtime.GOOS` in `internal/config/depcheck.go`, per the table in [contracts/dependency-check.md](./contracts/dependency-check.md), falling back to `install tmux using your platform's package manager`. Tests: `TestInstallCommandFromOsRelease`, `TestUnknownPlatformSaysSo`, `TestNeverExecutesInstall`. **Must fail when** a package manager is guessed from `GOOS` alone, or any code path executes an install — naming `apt` on a host that never had it is worse than naming nothing.

---

## Phase 9: Prior conversations (#44)

- [ ] T031 List prior conversations in new file `internal/session/conversation.go`: a **directory listing** returning identifier and modification time only, never opening a file, and only for a working directory under an approved root. Tests in `internal/session/conversation_test.go`: `TestListsIdAndTimeOnly`, `TestNeverOpensAFile`, `TestRefusesOutsideApprovedRoot`, `TestAbsentStoreOffersNothing`. **Must fail when** any conversation content is read — a listing cannot leak a transcript, and that narrowness is the whole security property (FR-034, FR-035).

- [ ] T032 Offer conversations in `web/templates/partials/create-form.html`, with **starting fresh as the default**. A resumed conversation still produces a new session record with its own identifier, credential and lifetime. Tests: `TestFreshIsDefault`, `TestResumeStillMintsNewRecord`, `TestAmbiguousResumeRefuses`. **Must fail when** the daemon picks the most recent conversation where the choice is ambiguous — it must refuse rather than resume the wrong one (FR-032).

---

## Phase 10: Pane bound (#41)

- [ ] T033 [P] Bound the captured pane size explicitly with a `pane_bound` config key in `internal/tmuxctl/exec.go`, stated where it is relied upon, refusing a capture past the bound rather than truncating. Tests in `internal/tmuxctl/exec_test.go`: `TestCaptureRefusesPastBound`. **Must fail when** an oversized capture is truncated — half a screen is a wrong screen, not a smaller one (FR-053).

---

## Phase 11: Polish

- [ ] T034 [P] Write `config.example` at the repository root, carrying the commentary that justified choosing a commented format over JSON. Test `TestConfigExampleParsesAndCoversEveryKey` in `internal/config/file_test.go`. **Must fail when** a key exists in `config.go` and not in the example.

- [ ] T035 [P] Update `docs/` and `README.md` for the config file, the settings page and the dependency check. Assert `go.sum` is still absent (SC-012).

---

## Dependencies

```
T001 (IsSecret) ──┬──> T005 (permission refusal)
                  └──> T011 (secret rendering)
T002 (Source)   ─────> T008 (provenance) ──> T012 (source column)
T003 (grammar)  ─────> T004, T005, T006 ──> T007 (shim) ──> T008
T017 (@crswd-start) ─> T018 (Mode()) ──> T019 (toggle) ──> T020 (--continue)
T022 (datalist) ─────> T023 (discovery) ──> T025 (announcement)
T014 (carry PRG) ────> T015 (finish tests) ──> T016
```

**Story order**: US1 → US2 (needs T008's provenance) → US3 → US4 → US5 → US6.
Phases 8, 9 and 10 are independent of the stories and can run whenever an
iteration is blocked elsewhere.

## Parallel opportunities

- T002 alongside T003 — different files, no shared state.
- Phases 8, 9, 10 alongside any story phase.
- T028, T033, T034, T035 are marked `[P]` and touch one file each.

## MVP scope

**US1 alone is a shippable milestone.** T001–T009 deliver the config file end to
end: an operator changes any setting by editing one file and restarting, and a
daemon with no file behaves exactly as it does today. Everything after it is
additive, and US2 is the first thing that makes US1 legible.

## Anti-requirements

Carried forward, and the two most likely to be violated by a well-meaning
implementer:

- **AR-005: a test satisfies the cross-site checks, it never disables them.**
  Setting `Sec-Fetch-Site: same-origin` and minting a valid page token is
  correct. A build tag or flag that turns a check off is the exact defect the
  gate exists to prevent.
- **AR-008: no refactoring outside the task's named files**, however obvious the
  improvement.
- **A task is not done when the code exists. It is done when something calls
  it.** This repository has shipped that failure three times — a reaper with no
  caller, `Store.Touch` with no caller, and a PR-opener no workflow invoked.
  T007 is the one to watch here: a parser that is never consulted is the exact
  bug left on the abandoned branch.

## The gate, every task

```bash
go build ./... && go vet ./... && go test ./... && golangci-lint run
```

Plus `-tags tmux` where the task touches tmux (T017, T020) and `-tags quickstart`
where it touches `cmd/crswd` (T009).

**Check the linter is v2.12.2 before trusting a green run.** A pre-v2 binary
reads this repository's v2 config, runs **zero** linters, and exits 0 — a green
that means nothing.
