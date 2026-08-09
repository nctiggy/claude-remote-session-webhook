# Implementation Plan: Make it work on a phone

**Branch**: `007-make-it-work-on-a-phone` | **Date**: 2026-08-09 | **Spec**: [spec.md](./spec.md)

## Summary

A stylesheet, two comments, and documentation. No new route, no new state, no new
Go behaviour, no new class.

That makes this milestone's risk unusual, and it is worth naming before anything
else: **the danger here is not the design — it is the guards.** Nine tests in
`internal/httpapi/stylesheet_test.go` can fail a correct-looking CSS change, and
most of them fail it for reasons that are invisible from the rule itself. A rule
in the wrong position does nothing. A value spelled directly instead of as a token
fails a sweep. A second media query at an identical width fails a count.

A fresh-context iteration that meets one of those by tripping it has spent its
iteration learning something this plan could have told it. So
[research.md](./research.md)'s R1 is an inventory of all nine — the test name, the
line, the exact condition, and the one correct way to satisfy each — and every
contract restates the ones its task can hit.

**The second thing worth reading is what this milestone cannot prove.** Nothing in
this repository renders CSS and nothing in it has a thumb. Every task here lands
with an assertion, and an assertion proves a declaration exists — not that a page
is usable on a phone. Three questions therefore ship deliberately open, and R8
gives them a mechanism that survives to the last task rather than a good
intention.

## Technical Context

**Language/Version**: Go 1.23 (tests only — no production Go changes)
**Primary Dependencies**: None. `go.sum` stays absent.
**Storage**: None. No new state of any kind.
**Testing**: `go test ./...`, `-tags tmux`, `-tags quickstart`, plus `go build`,
`go vet` and `golangci-lint run` at **v2.12.2**.
**Target Platform**: Any browser. The narrow case is designed at 390px; the touch
case is conditioned on the pointer, not the width.
**Constraints**: FR-024 … FR-031, carried forward.

### Unknowns

**Three, and they are deliberate.** They are not unknowns the plan failed to
resolve — they are questions no artefact in this repository can answer, recorded
with their fallbacks named in advance. See [spec.md](./spec.md) *What a test in
this repository cannot settle* and [research.md](./research.md) R8.

Everything else is resolved: eight questions, eight answers, in research.md.

## Constitution Check

| Principle | Gate | Verdict |
|---|---|---|
| I. Security is a gate | Does any of this touch a door? | **PASS** — nothing here touches routing, authentication, the cross-site defence, audit records, or any secret. The milestone is presentation. The one adjacent property is that the settings menu stays real links with no JavaScript, which is a *strengthening* of the no-JS guarantee, not a change to it. |
| II. Unknowns surfaced | Eight questions; three that cannot be answered here. | **PASS**, and this is the principle the milestone leans on hardest. Eight are answered with rationale and rejected alternatives. Three are *named as unanswerable here*, given fallbacks in advance, and carried in a committed file the last task must re-read. Inventing an answer to any of the three would be the violation. |
| III. Every change verifiable | A stylesheet assertion is weaker than it looks. | **PASS with a stated limit.** Every task lands with an assertion that reads the **parsed stylesheet** via `blockFor`/`cssRules` — the block a declaration lives in, not a substring that could sit in a comment (`stylesheet()` strips comments, so a comment cannot satisfy anything). The limit — that this proves existence, not usability — is written into the spec rather than glossed. |
| IV. Smallest correct change | Several bigger answers were available. | **PASS** — R7 confirms zero template changes are needed for any layout fix. The `<dl>`-per-setting rewrite, the pane toggle control, and the tmux-resize answer are all named and all deferred. |
| V. Standards enforced | Full gate per task at v2.12.2. | **PASS** |
| VI. Blast radius bounded | One stylesheet, edited in many places. | **PASS by construction, and it is worth saying why.** Because no new class is introduced, **G5 and G6 cannot fire at all** — every rule targets markup that already renders, and no new `.combo*`/`.switch*`/`.masthead*` name is created. Two of the nine guards leave every task's risk surface. |
| VII. Design system binding | This milestone *is* the design system. | **PASS** — two new tokens, each landed in `docs/design-system.md`, the stylesheet, and the hand-transcribed `designTokens` map **in the same commit**; a new pointer-coarse policy stated in the doc before any rule spends it; and a documentation defect fixed rather than matched. |

**Post-design note.** One thing changed while reading the code and it is worth
recording. The stylesheet already carries a comment (line ~1330) explaining that
`var(--bright)` resolved to nothing, that the current-section tint therefore never
rendered, and that "the redundant cue was the working one". **The very next
declaration is `background: var(--glow)`, which is the same bug**: `--glow` is a
shadow list, invalid as a background, so it too resolves to nothing.

The comment describing the class of defect sits directly above another instance of
it. `TestEveryTokenReferencedExists` was added in response to the first and cannot
catch the second, because it checks that a referenced token *exists* — not that it
is of a kind the property accepts. That is a real gap in the guard set, and this
milestone closes the instance while recording the gap.

## Project Structure

```
specs/007-make-it-work-on-a-phone/
├── spec.md · plan.md · research.md · data-model.md · quickstart.md
└── contracts/
    ├── guards.md          # R1 as a contract — the nine guards, restated per task
    ├── pane.md            # US1 — wrap under the breakpoint, contain the overscroll
    ├── settings.md        # US2 — the menu, the overflow, the stacked rows
    ├── touch.md           # US3, US4 — the pointer-coarse block
    └── hygiene.md         # US5, US6, US7 — cards, masthead, and what the sweep found
```

```
web/static/crswd.css                    # the milestone, almost entirely
docs/design-system.md                   # breakpoint paragraph, two tokens, the pointer policy
docs/components.md                      # pane and settings-menu prose
docs/mobile-open-questions.md           # NEW — the three questions, and their fallbacks
internal/httpapi/stylesheet_test.go     # designTokens map + one assertion per task
web/templates/settings.html             # a comment that contradicts its own file
```

## Ordering

Strict, and shallower than it looks — nothing depends on a later task.

```
docs + tokens  ─┬─→  pane
                ├─→  settings  (overflow → menu → rows, in that order)
                ├─→  touch     (targets → input size)
                └─→  hygiene   (cards, masthead, --glow, dead rules, comment)
```

**Docs and tokens go first because everything else spends them.** A rule
referencing `--tap` before `--tap` exists fails G3 immediately, and the token
cannot be added without `docs/design-system.md` declaring it, because the
`designTokens` map is a transcription of that document and not of the stylesheet.

Within settings the order is real: **the overflow move must land before the
stacked rows**, or the intermediate state pans the menu with a table that has just
been made taller.

## Complexity Tracking

| Choice | Why it is not a violation |
|---|---|
| A second media block is added (`pointer: coarse`) | It is not a width breakpoint, and G1's regex matches only width features — quoted in R2. The policy that keeps this honest rather than merely passing: a pointer block changes ergonomics and **never layout**, so layout still varies on exactly one tested axis. |
| The pane's wrap damages alignment-dependent output | Recorded as a trade rather than a fix, in the spec and in the contract. Reading prose is the dominant phone task and today it fails outright; tables and diffs are degraded but were already unreadable through a 44-character window. Pinch-zoom stays available deliberately, and reverting is one line. |
| Table rows are restyled into a grid, weakening table semantics | The column headers are hidden accessibly rather than deleted, and the linear order reads correctly alone. The residual risk is one of the three open questions, with a template-level fallback specced but not built — because building the heavier answer before knowing the lighter one is inadequate is the wrong order. |
| Three questions ship unanswered | They cannot be answered by any artefact here. Answering them by assumption is the failure mode; R8 gives them a committed file, an inline marker in each contract, and a final task that must re-read both. |
