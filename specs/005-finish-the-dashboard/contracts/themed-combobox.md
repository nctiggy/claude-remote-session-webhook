# Contract: The themed working-directory picker

**Files**: `web/templates/partials/create-form.html`, `web/static/crswd.css`, `web/static/crswd.js`
**Tests**: `internal/httpapi/partials_test.go`, `internal/httpapi/stylesheet_test.go`
**Satisfies**: FR-014 … FR-019
**Decomposed**: this contract is four tasks, not one. See the bottom.

---

## The rule that governs everything below

**The native control works first. The theme is an enhancement over a control that
already functions.** Script failure must cost appearance and nothing else — never
the ability to choose a directory, type a path, or submit the form.

That is why the ARIA roles below are added by script and are **not** in the
template.

## What the template renders

Literal. No ARIA attributes, because without script there is nothing to describe:

```html
<div class="combo" data-combo>
<input class="field-input" id="create-work-dir" type="text" name="work_dir"
       {{ if .Suggestions }}list="workdir-suggestions" {{ end }}autocomplete="off"
       spellcheck="false" required>
{{ if .Suggestions }}<datalist id="workdir-suggestions">{{ range .Suggestions }}<option value="{{ . }}"></option>{{ end }}</datalist>{{ end }}
<ul class="combo-list" id="workdir-listbox" hidden></ul>
<p class="combo-status" role="status" aria-live="polite"></p>
</div>
```

`role="status"` on `.combo-status` **is** in the template, because a live region
has to be in the accessibility tree before text arrives for the announcement to
happen at all — the same rule the fleet's notes follow.

## What the script does, in this order

Only when `[data-combo]` is present and script is running:

1. `input.removeAttribute("list")` — suppresses the native popup. Do this
   **first**: leaving it set is what produces two popups at once.
2. Add to the input: `role="combobox"`, `aria-expanded="false"`,
   `aria-controls="workdir-listbox"`, `aria-autocomplete="list"`.
3. Add `role="listbox"` to the `<ul>`; each rendered child is
   `<li role="option">`.
4. On input, filter the options read out of the `<datalist>` — which stays in the
   DOM as the data source — and write the FR-045 subset message into
   `.combo-status`.
5. Maintain `aria-expanded` to match whether the listbox is shown.

**The `<datalist>` is never removed.** It is the enhancement's data source, and
leaving it means the script needs no separate copy of the options that could
disagree with the markup.

## Keyboard

| Key | Behaviour |
|---|---|
| `↓` / `↑` | move the active option; `aria-activedescendant` follows |
| `Enter` | accept the active option, close |
| `Escape` | close, leave the typed text alone |
| `Tab` | close and move on; whatever is typed stands |

Typing is never intercepted. Any path remains typeable in full (FR-008) — the
listbox offers, it does not constrain.

## Styling

Every value comes from the token block in `crswd.css`. `stylesheet_test.go`
already fails a literal colour (`TestNoRuleCarriesAValueThatBelongsInAToken`) and
already requires the focus ring to survive (`TestTheFocusRingSurvives`).

The remote-control switch is CSS over a real `<input type="checkbox">` — see
[remote-control-toggle.md](./remote-control-toggle.md). Its appearance is never
the only cue for its state (FR-019).

Nothing transitions or animates under `prefers-reduced-motion` (FR-018).

**A rule the sweep cannot reach is a rule the sweep reads as dead.** Every class
named here must appear in rendered markup, which is why `.combo-list` and
`.combo-status` are in the template rather than created by script.

## Worked example

Suggestions `["/home/nctiggy/code", "/home/nctiggy/work"]`, operator types `co`:

- **No script**: the browser's own popup narrows to `/home/nctiggy/code`. Unthemed,
  working, and identical to today.
- **Script**: the native popup never appears; `#workdir-listbox` shows one
  `role="option"` reading `/home/nctiggy/code`; `.combo-status` reads
  `Showing 1 of 2 directories.`; `aria-expanded="true"`.

Typing `/tmp/elsewhere` in either case leaves the field holding `/tmp/elsewhere`,
which the handler then refuses if it is not under a root — exactly as it refuses a
suggested path that is not (FR-009).

## Contract tests

| Test | Asserts | **Must fail when** |
|---|---|---|
| `TestComboRendersWithoutAriaRoles` | Template output has no `role="combobox"` and no `aria-expanded` | The roles are moved into the template, making the markup describe a control that is not there without script |
| `TestComboRendersListAndDatalist` | `list="workdir-suggestions"` and a matching `<datalist id=…>` | The ids drift apart — the symptom is a picker that renders perfectly and offers nothing |
| `TestComboRendersPlainFieldWithNoSuggestions` | No `list`, no `<datalist>`, no empty `<ul>` content | An empty `<datalist>` is emitted |
| `TestComboStatusRegionIsInTheTemplate` | `.combo-status` present with `role="status"` | It is created by script, so the first announcement is missed and the stylesheet sweep reads its rule as dead |
| `TestComboClassesAppearInRenderedMarkup` | `.combo`, `.combo-list`, `.combo-status` all reachable | A class exists only in CSS, which `stylesheet_test.go` reads as a dead rule |
| `TestNoLiteralColourInComboRules` | Every combo rule uses tokens | A hex value is introduced |
| `TestComboFocusRingSurvives` | The focus indicator is visible on the input and on an option | `outline: none` without a replacement |
| `TestComboDoesNotAnimateUnderReducedMotion` | No transition/animation applies under the media query | A fade is added to the listbox |
| `TestSuggestedPathStillValidated` | A suggested path outside the roots is refused, same response and same audit record as a typed one | The handler trusts a value because the picker offered it — the only real vulnerability here |

## The four tasks

Carrying this whole is the failure milestone 4 escaped. In order:

1. **Markup** — the `.combo` wrapper, the `<ul>`, the status region. No script, no
   styling. Tests: the first five rows above.
2. **Styling** — tokens for the wrapper, the list, the options, the focus ring,
   the reduced-motion rule. Tests: rows six through eight.
3. **Enhancement** — `removeAttribute("list")`, the ARIA, filtering, the subset
   message. Test: the no-script path still passes untouched.
4. **Keyboard** — arrows, Enter, Escape, Tab, `aria-activedescendant`.

Task 1 must leave the form working exactly as it does today. If it does not, stop:
the enhancement is being built into the foundation, which is the mistake this
ordering exists to prevent.
