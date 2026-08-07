# Tasks: Finish the dashboard

**Input**: Design documents from `specs/005-finish-the-dashboard/`
**Prerequisites**: [plan.md](./plan.md), [research.md](./research.md), [contracts/](./contracts/)

**Tests ship inside the task that implements the behaviour**, never split out. A
task that leaves a failing test behind is reverted by step 6 of `ralph/PROMPT.md`.

---

## 🔒 Three tasks are security-relevant

| Task | Why |
|---|---|
| **T004** `remote_control` validation | The field carries a **mode**, not a name. A real configured command name like `rc` must still be refused, or the browser regains the ability to name what runs. |
| **T007** suggestion validation | A path in the datalist but outside `allowed_roots` must be refused identically to a typed one. A suggestion is never an authorisation. |
| **T014** probe honesty | A check that says "missing" about a command that works trains an operator to ignore it, which is worse than not checking. |

## ⚠️ The ordering obligation

**Four stories touch `web/templates/partials/create-form.html`.** They run in this
order and are not interleaved — each leaves the file green, so a failure names one
story:

```
US5 delete  →  US1 replace  →  US2 feed  →  US4 wrap
```

## ⚠️ The obligation this milestone exists for

**A requirement about what an operator SEES needs a test that reads the RENDERED
MARKUP.** Milestone 4 shipped three green tasks for FR-026 while the create form
kept its dropdown of command names, because every assertion was about a route or a
record. Where a task below carries the failing condition *"the route accepts the
right value but the form still renders the old control"*, that phrasing is the
point and must not be softened.

---

## Phase 1: US5 — Stop asking a question nobody can answer (P5, first because it deletes)

**Goal**: The create form no longer asks for an opaque conversation identifier.

**Independent test**: Open the create form; there is no conversation field, and
creating a session still works.

- [ ] T001 [US5] Remove the resume field from `web/templates/partials/create-form.html` — the `<input name="resume" id="create-resume">`, its `list="conversation-suggestions"` attribute and the `<datalist id="conversation-suggestions">` — and remove the `Conversations` field from the create view in `internal/httpapi/view.go` and its population in `internal/httpapi/dashboard.go`. Tests in `internal/httpapi/partials_test.go`: `TestCreateFormHasNoResumeField` asserts the rendered markup contains no `name="resume"` and no `conversation-suggestions`; `TestViewCarriesNoConversations` asserts the view struct exposes no conversation data. **Must fail when** the field survives in a hidden input, or the view field is left in place "for later".

- [ ] T002 [US5] Delete `internal/session/conversation.go` and `internal/session/conversation_test.go`, and add `TestStrayResumeValueIsNotExecuted` to `internal/httpapi/actions_test.go` asserting a submitted `resume=$(whoami)` reaches no command line. **Record the deleting commit's SHA in issue #95** so the code is recoverable — the root check, the listing that opens no file, the symlink exclusion and the separator-flattening are worth reading back. **Must fail when** the file is kept unused, which would be the fifth caller-less thing this repository has shipped, or when an abandoned field name remains an unguarded path to a command.

---

## Phase 2: US1 — Remote control at create time (P1) 🎯 the milestone-4 miss

**Goal**: An operator chooses remote control as a two-state mode, never by naming
a command.

**Independent test**: The create form offers a switch and shows no command name
anywhere; creating with it on yields a remote session.

- [ ] T003 [US1] Replace the `<select class="field-input" id="create-start-command" name="start_command">` block in `web/templates/partials/create-form.html` with the switch from [contracts/remote-control-toggle.md](./contracts/remote-control-toggle.md): `<input class="switch-input" id="create-remote" type="checkbox" name="remote_control" value="on">` plus a `<label class="switch-label" for="create-remote">Remote control</label>`. Remove `StartCommands` from the create view in `internal/httpapi/view.go` and stop populating it at `internal/httpapi/dashboard.go:297`. Tests in `internal/httpapi/partials_test.go`: `TestCreateFormHasNoStartCommandSelect` asserts the rendered markup contains no `<select` and no `name="start_command"`; `TestCreateFormRendersNoCommandName` asserts that for every configured command name there are **zero** occurrences in the rendered markup; `TestCreateFormRendersRemoteSwitch` asserts exactly one `type="checkbox"` named `remote_control` with `value="on"` and a `<label for>` bound to it; `TestViewCarriesNoStartCommands`. **Must fail when** the route accepts the right value but the form still renders the old control — that is the exact milestone-4 miss and it is why this assertion reads markup rather than behaviour.

- [ ] T004 🔒 **[SECURITY]** [US1] Accept the switch in `internal/httpapi/actions.go`: `remote_control=on` selects remote mode, an absent field selects local, and **any other value is the uniform refusal**. Which configured command each mode runs is read from configuration and never from the request. Tests in `internal/httpapi/actions_test.go`: `TestAbsentFieldMeansLocal`, `TestRemoteControlOnMeansRemote` (and the card says so in words), `TestArbitraryRemoteControlValueRefused` asserting `remote_control=rc` is refused **even though `rc` is a real configured name**, `TestCreateEmitsExactlyOneAuditRecord`. **Must fail when** the value is passed through as a command name, or absence is treated as an error rather than as local — a lost field must yield the *less* privileged mode.

---

## Phase 3: US2 — Suggestions that exist on a default install (P2)

**Goal**: A daemon with approved roots and nothing else offers directories.

**Independent test**: Configure only `allowed_roots`; the rendered create form
contains at least one `<option>`.

- [ ] T005 [US2] Add the `workdir_suggestions` configuration key (`CRSW_WORKDIR_SUGGESTIONS`, comma-separated) to `internal/config/config.go`, loaded through the same `withFile` shim as every other key so it gains provenance for the settings page. Test in `internal/config/config_test.go`: `TestWorkdirSuggestionsIsRead` asserts a configured value reaches `Config`. **Must fail when** the key is declared and never read — this repository's recurring failure, most recently `CRSW_DESTROY_ON_SHUTDOWN`, which was false on every daemon that ever ran.

- [ ] T006 [US2] Create `internal/config/suggestions.go` returning the **union** of three sources — the approved roots themselves (always), `workdir_suggestions` (explicit), and discovered children (only when `discover_roots` is on) — deduplicated and sorted. Replace `suggestions := s.cfg.DiscoveredWorkDirs()` at `internal/httpapi/dashboard.go:254` with it. Tests in `internal/config/suggestions_test.go`: `TestRootsAreOfferedByDefault`, `TestExplicitListIsOffered`, `TestSourcesAreUnionedAndDeduped`, `TestSuggestionsAreSorted`, `TestDiscoveryStillOffByDefault`; and in `internal/httpapi/partials_test.go`, `TestDefaultInstallRendersOptions` asserting that with only roots configured the **rendered create form** contains at least one `<option value=`. **Must fail when** discovery remains the only source (the shipped defect), when the fix for emptiness turns discovery on, or when map iteration order reaches the markup so the page differs between renders.

- [ ] T007 🔒 **[SECURITY]** [US2] Add `TestSuggestedPathOutsideRootsRefused` to `internal/httpapi/actions_test.go`: a path that appears in the rendered `<datalist>` but is **not** under an approved root is refused with the same response and the same audit record as a typed one. **Must fail when** the handler trusts a value because the picker offered it. The list is presentation; `allowed_roots` is the control, and this is the only real vulnerability the picker could introduce.

---

## Phase 4: US4 — Controls that belong to this interface (P4)

**Goal**: The choosing controls are styled from the design system and still work
with no script.

**Decomposed into four tasks.** Carrying US4 whole is the failure milestone 4
escaped.

- [ ] T008 [US4] Wrap the working-directory field in `web/templates/partials/create-form.html` with the structure from [contracts/themed-combobox.md](./contracts/themed-combobox.md): a `<div class="combo" data-combo>` holding the existing `<input id="create-work-dir" name="work_dir">` and `<datalist id="workdir-suggestions">`, plus `<ul class="combo-list" id="workdir-listbox" hidden></ul>` and `<p class="combo-status" role="status" aria-live="polite"></p>`. **Add no ARIA roles to the input** — without script `aria-expanded` would describe a control that is not there. Tests in `internal/httpapi/partials_test.go`: `TestComboRendersWithoutAriaRoles`, `TestComboRendersListAndDatalist` (the `list` attribute and the `<datalist>` id match), `TestComboRendersPlainFieldWithNoSuggestions`, `TestComboStatusRegionIsInTheTemplate`. **This task must leave the form behaving exactly as it does today** — if it does not, the enhancement is being built into the foundation, which is what this ordering prevents. **Must fail when** the roles are moved into the template, or the ids drift apart, whose symptom is a picker that renders perfectly and offers nothing.

- [ ] T009 [US4] Style `.combo`, `.combo-list`, `.combo-list li`, `.combo-status`, `.switch-input` and `.switch-label` in `web/static/crswd.css`, every value drawn from the existing token block. Include a visible focus ring on the input, on an option and on the switch, and a `prefers-reduced-motion` rule under which nothing transitions. Tests in `internal/httpapi/stylesheet_test.go`: `TestNoLiteralColourInComboRules`, `TestComboFocusRingSurvives`, `TestComboDoesNotAnimateUnderReducedMotion`, `TestModeNotConveyedByColourAlone`, and `TestComboClassesAppearInRenderedMarkup`. **Must fail when** a hex value is introduced, `outline: none` appears without a replacement, the switch's state is readable only from hue, or a class exists only in CSS — which the existing sweep reads as a dead rule.

- [ ] T010 [US4] Add the enhancement to `web/static/crswd.js`, guarded on `[data-combo]` being present. In order: `input.removeAttribute("list")` **first** (leaving it set is what produces two popups at once), then add `role="combobox"`, `aria-expanded="false"`, `aria-controls="workdir-listbox"` and `aria-autocomplete="list"`, then `role="listbox"` on the `<ul>` with `role="option"` children read out of the `<datalist>`, which stays in the DOM as the data source. Write the FR-045 subset message into `.combo-status`. Test `TestSubsetAnnounced`, and re-run the T008 tests unchanged to prove the no-script path still passes. **Must fail when** the `<datalist>` is removed rather than reused, giving the script a second copy of the options that can disagree with the markup.

- [ ] T011 [US4] Add keyboard handling to `web/static/crswd.js`: `↓`/`↑` move the active option with `aria-activedescendant` following, `Enter` accepts and closes, `Escape` closes and leaves the typed text alone, `Tab` closes and lets whatever is typed stand. Typing is never intercepted. Test `TestComboKeyboardOperable`. **Must fail when** the listbox constrains what can be typed — any path must remain typeable in full (FR-008), and the listbox offers rather than restricts.

---

## Phase 5: US3 — Reaching the settings page (P3, independent)

- [ ] T012 [US3] Add `<a class="masthead-link" href="/settings">Settings</a>` to `web/templates/partials/header.html`, inside `.masthead-bar`, **after** the `<p class="operator">` and **outside** the `<h1 class="brand">`. Tests in `internal/httpapi/partials_test.go`: `TestHeaderLinksToSettings`, `TestSettingsLinkIsOutsideTheBrandHeading`, `TestWordmarkIsStillTheFirstAnchor` (the first `<a>` is `.brand-link` with `href="/"`), `TestHeaderHasExactlyTwoAnchors`, `TestEveryPageCarriesTheHeader`, `TestSettingsStillHasNoMutatingVerb` (POST, PUT, PATCH, DELETE all 405). **Must fail when** the anchor lands inside the `<h1>`, turning the page's one first-level heading into a menu, or when reachability is mistaken for permission to add editing.

---

## Phase 6: Two defects in shipped code (independent of every story)

- [ ] T013 [US-none] Send human diagnostics to **stderr** and audit records to **stdout** in `internal/httpapi/server.go`, `internal/config/depcheck.go` and `cmd/crswd/main.go`, establishing the invariant that **every line on stdout is a record**. Tests: `TestAuditRecordsGoToStdout`, `TestDiagnosticsGoToStderr`, `TestNoSecretInAnyDiagnostic`. **Must fail when** a warning is written to stdout, which breaks the "only JSON is JSON" property the documented filter depends on. Closes half of #88.

- [ ] T014 🔒 **[SECURITY]** [US-none] Fix the start-command probe in `internal/config/depcheck.go` to resolve the command **the way the session will** — through a login shell, the same resolution it will actually get inside a tmux pane — and where that cannot be answered confidently, state what was checked rather than asserting absence. The live daemon has warned on every start that `claude` is missing while sessions using it work, because the probe asks the service manager's PATH and the command runs in a login shell that has `~/.local/bin`. Tests in `internal/config/depcheck_test.go`: `TestProbeResolvesThroughLoginShell` (a command on the login shell's path but not the service manager's is **not** reported missing), `TestProbeNamesWhatItChecked`, `TestGenuinelyMissingCommandStillWarns`, `TestMissingTmuxStillFatal`. **Must fail when** the probe keeps asking the daemon's own PATH, or when the fix silences the check entirely. **The tmux probe is untouched and stays fatal** — the daemon execs tmux itself, so its own PATH is the right environment to ask. Closes #96.

- [ ] T015 [US-none] Correct the documented audit-trail command in `deploy/crswd.example.service` to `journalctl --user -u crswd -o cat | grep '^{' | jq .`, with a comment recording why `_COMM=crswd` is not enough: the non-JSON lines are the daemon's own, not systemd's. Test in `cmd/crswd/quickstart_test.go` (`-tags quickstart`): `TestDocumentedCommandParses` runs the command as written against a daemon that has logged both records and diagnostics. **Must fail when** the documentation drifts from what works. Closes the other half of #88.

---

## Phase 7: Polish

- [ ] T016 [P] Update `docs/` and `README.md` for the switch, the suggestion sources, the settings link, and the corrected audit-trail command. Assert `go.sum` is still absent (SC-010).

---

## Dependencies

```
T001 (remove field) ──> T003 (replace select) ──> T006 (feed options) ──> T008 (wrap)
                                                                            │
                                                            T009 ──> T010 ──> T011
T005 (config key) ─────> T006
T003 ─────────────────> T004 (validate)
T006 ─────────────────> T007 (validate suggested path)
```

**The create-form chain T001 → T003 → T006 → T008 is strict.** Everything else is
free: T012 (header), T013–T015 (the two defects) and T016 touch different files
entirely and can run whenever an iteration is blocked.

## Parallel opportunities

- T012, T013, T014, T015 alongside any create-form task — different files.
- T005 alongside T001 or T003.
- T016 is marked `[P]`.

## MVP scope

**US1 alone is the milestone's point.** T001–T004 deliver what milestone 4 claimed
and did not: an operator chooses remote control as a mode, and no command name
reaches the browser. Everything after it is real but additive.

## Anti-requirements

- **AR-005: a test satisfies the cross-site checks, it never disables them.**
- **AR-008: no refactoring outside the task's named files.** With four stories
  queued on one template, this one is load-bearing — a task that tidies the file
  while it is there makes the next story's diff unreadable.
- **A task is not done when the code exists. It is done when something calls it.**
  T005 is where that bites: a config key with no reader is exactly
  `CRSW_DESTROY_ON_SHUTDOWN`, which was false on every daemon that ever ran.

## The gate, every task

```bash
go build ./... && go vet ./... && go test ./... && golangci-lint run
```

Plus `-tags tmux` and `-tags quickstart` where touched (T015 needs quickstart).
**All three suites now run in CI (#87)**, so a task can no longer pass by not
being run.

**Check the linter prints 2.12.2.** A pre-v2 binary reads this repository's v2
config, runs zero linters, and exits 0 — a green that means nothing.

---

## Phase 8: The pointer (added mid-milestone, from T011's findings)

T008–T011 built the picker's markup, styling, enhancement and keyboard. **Nothing
selects an option with a pointer.** `.combo-list li` carries `cursor: pointer` from
T009, so the affordance is drawn and does nothing — the first thing an operator
using a mouse will try.

T011 found it and correctly did not fix it: its task text named four keys, a click
handler is outside that, and AR-008 is load-bearing with four stories queued on one
file. Flagging beat scope-creeping.

- [ ] T017 [US4] Add pointer selection **and** blur-close together in `web/static/crswd.js`. Bind **`mousedown`** on an option, not `click` — a blur-close fires first and would eat the click, which is why these two cannot be written separately. Pointer selection runs the same `activate` and accept path `Enter` already uses, so this adds a trigger and not a second behaviour. Blur-close hides the listbox and sets `aria-expanded="false"` when focus leaves the combo, without disturbing the typed text. Tests in `internal/httpapi/stylesheet_test.go`: `TestComboOptionIsPointerSelectable` and `TestComboClosesOnBlur`. **Must fail when** `click` is used instead of `mousedown`, which makes selection work only when the blur handler happens to lose the race — a bug that reproduces intermittently and reads as flakiness. Also **must fail when** the affordance is removed instead of implemented: deleting `cursor: pointer` would make the picker consistent and worse.
