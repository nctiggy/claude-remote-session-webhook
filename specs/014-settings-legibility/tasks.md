---
description: "Task list for 014-settings-legibility"
---

# Tasks: A Settings Page an Operator Can Read

**Tests**: REQUIRED per `AGENTS.md`.

## Phase 1: Setup
- [X] T001 Confirm the tree is green before touching it.

## Phase 2: User Story 1 — three sections (P1)
- [X] T002 Collapse the seven headings to `Network`, `General` and an unclassified fallback in `internal/httpapi/settings.go`.
- [X] T003 Correct the section map while rewriting it: `start_command`, `remote_control_command` and `session_environment` were falling through because it named `remote_start_commands`, which is not a key this daemon has.
- [X] T004 Point the door sentence at the Network section.
- [X] T005 Update `sectionOrder` and every test naming an old heading.
- [X] T006 Add `TestNoSettingIsUnclassified`, driven off the rendered rows, so an unclassified key is a red test rather than a mystery heading.

## Phase 3: User Story 2 — one Save (P1)
- [X] T007 Add `value.<key>` and `was.<key>` per row in `web/templates/settings.html`; remove the per-row form and button.
- [X] T008 Wrap the table in one form with one Save at the end.
- [X] T009 Rewrite the edit route as a batch write in `internal/httpapi/settings_edit.go`: skip any key whose submitted value equals what was rendered, build one candidate file, validate once, write once.
- [X] T010 Add `outcomeSettingUnchanged` so a submit that changed nothing says so.
- [X] T011 Record the changed keys — sorted, names only — on the audit record.
- [X] T012 Remove the now-dead `fieldSettingKey`, `fieldSettingValue` and `submittedValue`, folding their reasoning onto `submittedValueFrom`.
- [X] T013 Update the edit test helpers to the batch form shape.
- [X] T014 Add `TestSaveWritesOnlyWhatChanged`, `TestSaveWithNothingChangedWritesNothing` and `TestSaveNeverStoresARenderedSecret`.
- [X] T015 Add `TestOneSaveForTheWholeForm`.

## Phase 4: User Story 3 — the update section (P2)
- [X] T016 Render the unit facts inline only when `.Unit.Waiting` says a newer unit is really there.
- [X] T017 Move the same facts into a disclosure otherwise, so nothing is deleted.
- [X] T018 Share the rename/continue disclosure's rules rather than adding a third set.

## Phase 5: User Story 4 — the source column (P3)
- [X] T019 Drop the Source column from the table head and every row.
- [X] T020 Mark a row whose value did not come from the configuration file.
- [X] T021 Rewrite `TestShowsSourcePerKey` as `TestShowsAnUnexpectedOriginOnly`.

## Phase 6: Polish
- [X] T022 Style `.settings-form`, `.settings-save` and `.settings-origin`; remove `.setting-form` and `.setting-save`.
- [X] T023 Run the full gate.
- [X] T024 Update `docs/components.md` for the settings page's new shape.
- [X] T025 Append to `ralph/PROGRESS.md`.
