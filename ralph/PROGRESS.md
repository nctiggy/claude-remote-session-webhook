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

## Iteration 0 — two operator requests

**Did:** Archived milestone 8, opened a fresh notebook.

**Left:** six tasks, from two requests: *"All true/false settings should be check
boxes"* and *"can we have a way to restart the daemon from within the UI?"*

**Findings, all verified before the plan was written:**

- **There are exactly two boolean keys**: `discover_roots` and
  `destroy_on_shutdown`, the two callers of `loadBool`. Neither is a secret, so both
  are already `Editable`.
- **Neither feature needs a new component or a new class.** The switch
  (`.switch-input`, `.switch-label`) exists and is documented; the restart reuses
  `.updating` and `.spinner`. That keeps both class sweeps and the components-doc
  guard out of the risk surface entirely.
- **The trap in the checkbox work is not CSS, it is HTTP.** An unchecked checkbox
  submits **nothing at all**. The handler currently reads
  `r.PostForm.Get(fieldSettingValue)`, so an unchecked box is indistinguishable from
  a cleared field. The fix is not the hidden-input trick — with both fields sharing a
  name, `.Get` returns the first, which is the wrong one. It is for the handler to
  know the key is boolean and read an absent value as `false`, and **only** for keys
  it knows are boolean. A truncated request must never clear a setting that is not
  one.
- **The restart needs almost no new machinery.** `ExitForRestart()` and `exitGrace`
  already exist for the update, and the reason for the goroutine and the grace period
  is written above them: exiting before the response flushes is what turned an
  earlier update into a Cloudflare 502.
- **Restart is strictly less dangerous than update**, which already goes through the
  browser door. Update installs code from the internet; restart runs the binary that
  is already installed. The argument the operator won on #66 covers this a fortiori.

---

## Iteration 1 — 2026-08-09 21:39 — T001

**Did:** Added `config.IsBool(key)` in `internal/config/secret.go`, next to
`IsSecret`, plus `bool_test.go` — a behavioural table and a structural test that
parses the package and holds the list to loadBool's call sites in both directions.

**Learned:**

- **Deriving the keys at runtime is not reachable**, so the plan's fallback applies:
  the list is a literal next to `IsSecret`'s. Go cannot ask a function who calls it,
  and populating a registry *from* `loadBool` does not work either — it only runs
  during a load, so the set would be empty before the first one and the settings
  page asks before that.
- **The AST walk is the repo's existing answer to exactly this**, so no new technique
  was invented: `secret_test.go` walks the package for a second secrecy classifier,
  `envexample_test.go`'s `declaredVars` parses config.go's constants, and
  `TestVarsNamesEveryDeclaredVariable` pins a hand-written list to them. T001 reuses
  `packageFiles` from `secret_test.go` directly — it is in `package config_test`, so
  it is already in scope from a new file in that package.
- **`declaredVars` returns values only** (`map[value]bool`), and resolving a call site
  needs the other direction, name→value. I added `varConstants` rather than widening
  `declaredVars`, per AR-008. If a later task needs both, that is the moment to merge
  them, not now.
- **The reverse direction is checked over `config.Vars()`, not over IsBool's own string
  literals.** Vars() is the whole universe of keys a file may set or the page may
  render, so a key wrongly claimed boolean is caught exactly where it could do harm,
  and the check stays behavioural instead of asserting how `IsBool` happens to be
  written. A later rewrite of `IsBool` into a map or a switch will not break it.
- **Both directions were shown failing before the commit**, per the plan's rule. A:
  dropping `destroy_on_shutdown` → *"is loaded with loadBool and config.IsBool(...) is
  false"*. B: adding `listen` → *"no loadBool call reads CRSW_LISTEN, so a request that
  omits its value would write `false` over a setting that is not a boolean"*. B is the
  direction T002 depends on.
- **`golangci-lint` is 2.12.2 here**, so the #26 fake-green does not apply to this
  machine. Worth re-checking each iteration anyway; it is one command.
- **Adding `IsBool` to `secret.go` does not trip `TestIsSecretIsTheOnlyClassifier`.**
  Its `decidesSecrecy` accuses only a declaration whose name contains "secret" *and*
  which is `(string) bool`; `IsBool` has the shape but not the name. The literal walk
  matches the two secret keys exactly, and the new keys are not those. If a later task
  adds a third classifier there, check both halves again — the shape alone is not
  enough to be accused, which is what makes the file safe to grow.

**Left:** T002–T006. T002 is next and is the security-relevant half: teach
`settings_edit.go` that an absent value means `false` **only** for keys `IsBool`
reports, and test that a non-boolean with no value is still refused.

**Findings:** none new. One thing noted in passing and deliberately not acted on:
`secret.go` is now a file named for one of the two predicates it holds. Renaming it
is out of scope under AR-008, and it would move the file `secret_test.go` exempts by
name (`classifierFile = "secret.go"`), which is a change worth its own task rather
than a drive-by.

---

## Iteration 2 — 2026-08-09 21:47 — T002

**Did:** `submittedValue(form, key)` in `internal/httpapi/settings_edit.go` reads an
absent value field as `false` for `config.IsBool` keys and as the empty value for
every other key, plus two tests: an unchecked box turning `discover_roots` off, and
the narrowness that keeps `false` out of everything else.

**Learned:**

- **The plan's wording for the second test does not hold, and the fix is sharper than
  the wording.** "A non-boolean with no value is still refused" is not true of any key
  that could carry the test: an absent value writes `key = ` (empty), and the loader
  reads empty as *unset*, so `max_sessions` falls back to its default and
  `allowed_roots` falls back to the built-in `$HOME` root — both **accepted**. Nothing
  is refused. What the test asserts instead is what the plan's own body asks for: an
  absent value for a non-boolean behaves **exactly as it does today**, and never
  becomes `false`.
- **The bytes-only assertion would have been decoration.** Against the over-broad
  reading, the candidate is `max_sessions = false`, `config.Validate` refuses it, and
  the file is left *unchanged* — so "the file does not contain `max_sessions = false`"
  passes against the very defect it names. The assertion that actually fires is the
  other one: the file must carry `max_sessions = ` (with the newline, since
  `"max_sessions = "` is a prefix of the line being refused). Shown failing by
  dropping `&& config.IsBool(key)` and restoring it.
- **`allowed_roots` cannot host that test at all**, which is worth knowing before
  someone reaches for the most security-shaped key. Empty and `false` are both
  refused-or-accepted identically from the outside — `false` fails the absolute-path
  check, empty falls back to the default root — so the over-broad reading is invisible
  through it. `max_sessions` is the key where the two readings diverge observably.
- **For today's two keys this changes nothing the daemon does.** `loadBool` already
  reads empty as false, so unticking a box "worked" before this task by coincidence.
  What it changes is the operator's file (`discover_roots = false`, not a half-finished
  line) and the coincidence itself: a boolean defaulting to *true* would have been
  turned on by an unticked box. That is why T002 is worth its iteration even though the
  suite would have gone green without the production change.
- **`editForm` re-renders the settings section each time to lift a page token**, so a
  test can post twice against one fleet (on, then off) without minting anything itself.
- **`golangci-lint` is 2.12.2 here** — checked again per #26. `go vet` under all three
  build tags compiles clean; none of them was touched.

**Left:** T003–T006. T003 is next: render the two boolean rows as switches in
`web/templates/settings.html`, reusing `.switch-input`/`.switch-label`, introducing no
new class, and asserting against the *rendered markup* that a boolean row carries a
checkbox and a non-boolean row still carries a text input. The checked box must submit
`value=true`; unchecked submits nothing, which is what this iteration made safe.

**Findings:**

- **A truncated edit POST silently resets a setting to its default, and always has.**
  A request naming `allowed_roots` with no value field writes `allowed_roots = `, the
  loader reads that as unset, and the containment allowlist becomes the built-in
  `$HOME` root — accepted, written, and audited as an ordinary edit. Same shape for
  `max_sessions` (back to the default cap). This is pre-existing behaviour, out of
  T002's scope under AR-008, and T002 deliberately pins it rather than changing it. It
  is worth its own task: the honest fix is for the handler to refuse a request whose
  value field is absent for a key that is not boolean, rather than treating absence as
  "clear it". Constitution VI is the reason — both keys are containment bounds.
- `submittedValue` is package-level rather than a method for testability of the pure
  reading, but nothing tests it directly; both guards go through the route. That is the
  right way round per `docs/conventions.md` ("assert the caller"), noted only so a later
  iteration does not add a unit test for it and think it has covered the route.

---

## Iteration 3 — 2026-08-09 21:56 — T003

**Did:** `settings.html` renders a boolean row as `.switch-input` and every other
editable row as the `.setting-input` it always had, branching on two new
`settingRow` fields (`Boolean`, `On`) that `settingsOf` fills from `config.IsBool`.
Four tests read the rendered markup; a fifth drives the box's own value through the
edit route.

**Learned:**

- **The value attribute is the whole of this task's real risk, and it is invisible
  to a markup-only test.** A checkbox with no `value` submits `on`; `loadBool` calls
  `strconv.ParseBool`, which refuses `on`; `config.Validate` then refuses the whole
  candidate file. So the operator ticks the box, presses Save, and the file is
  **unchanged** — a control that looks right and does nothing, reported as a refusal
  about a value they never chose. Proven by mutation: `value="on"` leaves
  `discover_roots` absent from the file entirely. `boolOn` now sits beside `boolOff`
  in `settings_edit.go` as this page's two spellings for a boolean, and
  `TestTheSwitchSubmitsTheSpellingThisPageWrites` holds the template's literal to it
  — the arrangement `confirm=yes` already has.
- **The sweep is over every editable key, not over the two booleans.** Both
  directions then hold and neither can go stale: a third boolean forgotten in the
  template fails as a text field, and a key wrongly reported boolean fails as a box.
  The second direction is the one that matters — a box is the control whose *absence*
  the route reads as `false`, so a wrongly-boxed key is a setting an untick clears.
- **`On` is read off `Value`, not off the Config a second time.** The value column
  and the tick are then one answer. Reading the Config again would let this page state
  a setting in a cell and contradict it in the control beside it.
- **All five guards were shown failing first**, per the plan's rule: `{{ if false }}`
  (the unfixed state — all four fail), `{{ if true }}` (16 non-boolean rows become
  boxes), no `value`, `value="on"`, and no `{{ if .On }} checked`.
- **`settingsRowFor` already existed** and is the right isolation for any assertion
  about one row; `settingControl` builds on it and refuses a row offering more than
  one input, so "a row edits one setting with one control" is asserted on the way past.
- **`golangci-lint` is 2.12.2 here** — checked again per #26; 0 issues. `go vet` under
  all three build tags compiles; none was touched, and no file in `cmd/crswd` mentions
  the settings page.

**Left:** T004–T006, the restart half of the milestone. T004 is next and is the
security-relevant one: `POST /dashboard/restart` registered through
`s.handleAction(...)` exactly as destroy and update are, `confirm=yes` via
`fieldConfirm`/`confirmYes`, audit action `dashboard.restart` written exactly once,
and `ExitForRestart()` from a goroutine after `exitGrace`.

**Findings:**

- **The plan asked for `.switch-label` and this row deliberately has none.** Two
  binding rules collide on this one control: `docs/components.md`'s Switch section
  puts the pointer on the label as well as the box, and its "The settings page"
  section says a row's input is labelled by `aria-label` rather than a visible
  `<label>`, "because the row header beside it already says the key and a second copy
  is the same word twice to anybody reading it aloud" — the same sentence is on
  `.setting-form` in `crswd.css`. The rule specific to *this row* won, and the plan's
  binding constraint ("introduce no new class") is satisfied either way. `.switch-label`
  is still rendered by the create form, so no sweep sees a dead rule.
- **What that costs is a tap target, and it is a real gap rather than a neutral
  trade.** `.switch-input` is `--s4` square. On the create form the row is
  `.field-switch`, which the coarse-pointer block sizes to `--tap`; nothing does that
  for a switch inside `.setting-form`, so on a phone this is a `--s4` box where every
  button on the page is a thumb. It is CSS and outside a template-only task under
  AR-008, so it is left here rather than done quietly. **The honest fix is a
  `.setting-form .switch-input` (or `.setting-save`-style) rule in the
  `@media (pointer: coarse)` block**, and it is worth its own task — note that
  `TestTheCoarseBlockChangesNoLayout` forbids `display`/`position` there, so it has to
  be a size.
- **A boolean row no longer prints the words `true`/`false` anywhere.**
  `TestSettingsStatesTheValueOfEveryNonSecretKey` names only non-boolean keys so it
  stayed green, but the page's claim — "one row per key, with the value beside it" —
  is now carried for two keys by a tick rather than by text. That is the operator's own
  request and reads correctly, noted only so a later iteration does not read the
  absence of the word as a regression.
