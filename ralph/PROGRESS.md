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

## Iteration 0 — the guard-gap backlog begins

**Did:** Archived milestone 7 and opened a fresh notebook.

**Left:** five tasks, each closing a gap that milestone 7 surfaced by tripping over it.

**Findings:** Every task in this list exists because **something in this repository
stated an invariant that nothing enforced**. That is the shape to look for, and it
recurs:

- `designTokens` says it transcribes `docs/design-system.md`. No test opens that file.
- `settings.go` says no mutating verb reaches the config. `POST /settings/edit` does.
- `components.md` says the Toast "has no section and no use". It ships in three templates.
- The class sweep says it holds the stylesheet and the markup to the same names. It
  cannot see an element selector, which is how four dead rules survived two milestones.
- `freePort` says it returns a free port. It returns a port that *was* free.

Four of the five are a **document or a comment that is confidently wrong**, and in a
pipeline whose executor reads comments as contract, that is not tidiness — it is a
defect that costs an iteration or, worse, is believed.

The fifth (#123) is the one to be most careful with: it is the only change here that
touches a test harness rather than a document, and a wrong fix makes the suite flaky
in a new way instead of the old one.

**T003 is the task most likely to grow.** Once the sweep can see element selectors it
may report rules nobody has looked at since #103. Anything it finds must be checked
against the templates before deletion — T015 of milestone 7 kept two of four rules it
was expected to delete, and deleting either would have left an unstyled page no guard
could report.

---

## Iteration 1 — 2026-08-09

**Did:** T001 (#116). `TestTheTranscriptionIsTheDocumentItTranscribes` in
`internal/httpapi/stylesheet_test.go` now reads `docs/design-system.md` and compares it
with the `designTokens` map in both directions. Only that test file changed; the
document and the stylesheet are byte-identical to before.

**Learned:**

- **The document declares tokens in three shapes, not one.** The map's old comment said
  "every token that document declares as CSS", and that was already wrong when it was
  written. Fenced CSS blocks carry 22 of them; the **state table** carries the four
  `--state-*` tokens in two cells and never as CSS; and `--pane-h: 30rem` is a single
  inline code span in one sentence of the Layout section. A sweep that read only the
  fences would have reported the four state tokens as fabrications and missed `--pane-h`
  entirely — wrong in both directions at once.
- **`--pane-h` was a live instance of the very drift the map exists to catch.** It is
  declared in the document (line 217) and in the stylesheet, and the map omitted it, so
  nothing held one to the other. It is in the map now.
- **Two regex traps, both silent.** A token *reference* looks like a declaration: the
  pointer-coarse table is full of `var(--tap)` and `var(--s3)`. What separates them is
  the character before — `(` is not admitted by the leading class — and the colon. And
  the inline declaration is delimited by backticks on **both** sides, so the value class
  has to exclude a backtick or `--pane-h`'s value swallows the rest of the sentence, and
  the leading class has to *admit* one or the declaration is never found at all. The
  first attempt got the value right and the lead wrong, and the symptom was `--pane-h`
  reported as a fabrication rather than any parse error.
- **The state-table parse anchors on the second cell being nothing but a backticked
  token.** That is what keeps it off the breakpoint and pointer tables, whose second cell
  is a declaration made *about* a selector (`` `min-block-size: var(--tap)` ``) rather
  than a token being declared. It is shape-based and therefore brittle by nature, so
  `designSystemTokens` fatals if any of `documentedStates`' four tokens is missing from
  the table parse — a parser that has gone blind must not read as a map that invented
  the palette.
- **Breaking it is cheap here and worth doing all four ways.** Direction 1 was proved by
  adding a token to the stylesheet *and* the map: `TestTheTokenBlockIsTheDesignSystem`
  stays green on that, which is precisely the hole. Watch out for name collisions when
  doing this — `--glow` is already declared further down `crswd.css`, which muddies the
  proof; `--halo` is free.

**Left:** T002 (#117), T003 (#118), T004 (#119), T005 (#123).

**Findings (noticed, not fixed — no code changed for any of these):**

- **The stylesheet declares ~20 tokens the design system never names**, and nothing
  reports it: `--fs-brand`, `--fs-nano`, `--fs-micro`, `--fs-eyebrow`, `--fs-body`,
  `--lh-body`, `--fs-prose`, `--fs-label`, `--fs-pane`, `--lh-pane`, `--tuck`,
  `--ls-brand`, `--ls-eyebrow`, `--ls-label`, `--focus-width`, `--focus-offset`,
  `--shell-max`, `--field-min`, `--menu-min`, `--menu-max`, `--card-min`, `--toast-max`,
  `--combo-h`, `--rain-wipe`, `--glow`. Most are legitimate — the design system gives
  typography and layout in tables *without* token names, and each declaration carries a
  comment saying so. But that is an argument made in comments, not a rule anything holds,
  and it is the same shape as every task in this milestone: **the third direction of
  this sweep, stylesheet → document, does not exist.** Note the tension before opening
  it — closing it either means naming two dozen tokens in the design system or writing
  down an explicit exemption list, and an exemption list nobody prunes is how a
  fabricated token hides. Not in scope for T001, which was about the map.
- **`--focus-width: 2px` / `--focus-offset: 2px` are the closest thing to a real gap in
  that list.** The design system gives the focus rule verbatim as CSS
  (`outline: 2px solid var(--phosphor); outline-offset: 2px;`), so unlike the typography
  table those two lengths *are* in the document — just spelled as literals inside a rule
  rather than as tokens. `designSystemTokens` correctly does not read them, because they
  are not declarations of a `--name`. A future task could hold the focus rule's two
  lengths to the document the way the breakpoint's `780px` already is.
- **The design system's Layout section is the one place a token is declared in prose.**
  If that sentence is ever rewritten to drop the value, the new test reports `--pane-h`
  as a fabrication in the map rather than a document that stopped saying something. The
  message names the file, so it should not cost more than a minute — but it is the
  failure mode of reading a value out of English.

---

## Iteration 2 — 2026-08-09

**Did:** T002 (#117). Rewrote `internal/httpapi/settings.go`'s header comment: it now
describes the two doors this page has — `GET /settings` here, `POST /settings/edit` in
`settings_edit.go` — the action gate in front of the write, and why the write is a
separate pattern rather than a verb on this path. Comment-only; no executable change.

**Learned:**

- **The false claim was in the same file twice, and the issue names only one of them.**
  Besides the header, `settings`' own doc comment said *"It mints no page token, and
  that is a decision rather than an omission. A page token authorises a write, this page
  offers none"* — while the handler four lines below calls `s.mintPageToken`, and
  `updatePanelFor` mints a second one. Both are fixed. If a comment in this repo asserts
  an absence, grep the file for the thing it says is absent before believing it.
- **`web/templates/settings.html` and `docs/components.md` already tell the true story**,
  and told it well: the template's header has the "it was once read-only, and the absence
  of a route was the safeguard — do not restore it by deleting a form" paragraph, and
  components.md has the matching one. They were the model for the new text, so the three
  now agree. Read those two before writing anything about this page.
- **The narrow invariant is still real and still enforced.** `GET /settings` genuinely is
  the only verb registered on that path, and `TestNoMutatingVerbRegistered`
  (`settings_test.go:217`) holds POST/PUT/PATCH/DELETE to it to the unknown-route answer.
  What was wrong was the *conclusion drawn from it* — that no browser route writes the
  config — not the assertion itself. Do not delete that test while fixing its prose.
- **The write's real justification is in `git log 0cb587d`**, the #106 commit, and in
  `internal/config/write.go`'s `Editable`. Neither is linked from anywhere obvious; that
  commit message is the best account of why five of issue #49's six settings became
  editable and the shared secret did not.

**Left:** T003 (#118), T004 (#119), T005 (#123).

**Findings (noticed, not fixed — no code changed for any of these):**

- **`docs/security.md` carries the same falsehood, and it is binding.** Two places:
  line 216, *"**Nothing in the daemon writes the operator's file.** `crswd config
  migrate` is the only code in this repository that writes a configuration file"* —
  `editSetting` writes it through `config.WriteFile`, and has since #106. And lines
  233–237, *"**No mutating verb is registered on `/settings` at all.** … Writing the
  operator's configuration file from a browser is the highest-consequence surface in
  the product; a route that does not exist cannot be exploited"* — the first sentence is
  still literally true of that path, but the paragraph reads as "the write does not
  exist", and it does, at `/settings/edit`. **This is the more serious half of #117**:
  the constitution makes this document the gate every plan is checked against, so a
  planner reading it will conclude the browser cannot write the config file. Not fixed
  here because AR-005/AR-008 scope T002 to `settings.go`, and because a binding security
  document deserves its own reviewed change rather than a paragraph slipped into a
  comment fix. It wants an issue.
- **`internal/httpapi/server.go:571–577`** — the registration comment above
  `s.handleBrowser(patternSettings, …)` still says *"no mutating verb is registered on
  the path at all. Editing the operator's file from a browser is out of scope this
  milestone, and the absence of a POST is the safeguard rather than a POST that
  refuses"*. Fifty lines below it, `s.handleAction(patternSettingsEdit, …)` registers the
  POST. Same defect, different file.
- **`internal/httpapi/settings_test.go:195–216`** — `TestNoMutatingVerbRegistered`'s doc
  comment says it is *"the safeguard the spec's Out of Scope section rests on: editing
  the operator's configuration file from a browser is not in this milestone"* and
  **"Must fail when an edit route is added"**. An edit route was added, on a sibling
  path, and the test correctly did not fail. The assertion is right; the reason written
  above it is two milestones stale.
- **`gofmt -l ./...` is not clean on `main`.** `internal/httpapi/render.go` has the
  `buildinfo` import sorted ahead of `bytes` in the same group; `gofmt` wants it after
  `fmt`. Nothing catches it: the `format-and-lint` hook only touches files an iteration
  writes, and `golangci-lint run` reports 0 issues, so whichever formatter the config
  enables does not include this. Pre-existing and unrelated to T002 — but `AGENTS.md`
  lists `gofmt -w .` as the format command, so the next iteration that runs it as
  written will produce an unrelated diff in a file it did not touch.
