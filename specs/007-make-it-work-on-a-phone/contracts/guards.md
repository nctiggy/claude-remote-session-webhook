# Contract: The guards

**File**: `internal/httpapi/stylesheet_test.go` (2125 lines, as of `06ace3c`)
**Read this before any other contract in this milestone.**

---

## Why this contract exists

Every other contract here adds CSS. This one is the list of ways a correct-looking
CSS change fails, and it exists because **most of these failures are invisible
from the rule itself**. A rule in the wrong position parses fine and does nothing.
A value spelled directly fails a sweep in a different file. A second media query
at an identical width fails a count.

An iteration that discovers a guard by tripping it has spent its iteration
learning something it could have been told. So it is being told.

---

## The reading helpers — every guard inherits these

| Helper | Line | Behaviour that changes what you must write |
|---|---|---|
| `stylesheet(t)` | 83 | **Strips every `/* … */` comment.** No guard can be satisfied by a comment. |
| `tokenBlockAndRules(t)` | 97 | Splits at the **first** `{` and **first** `}`. The token block is positionally first; a token declared elsewhere is not a token. |
| `blockFor(t, source, marker)` | 1729 | First block whose prelude contains `marker`, **brace-counted** — works on a media block and returns everything inside it. |
| `cssRules(source)` | 1796 | Strips media **preludes**, splits on `}`. A rule inside a query is read as an ordinary rule. |
| `mediaOpen` | 77 | `@media[^{]*\{` — strips the prelude, so the value sweeps see inside every query. |

**The consequence that matters most**: there is **no exemption for being inside a
media query**. Rules in the 780px block and the new pointer block are swept by the
value guards exactly like top-level rules.

---

## The nine guards

### G1 — `TestTheDashboardHasExactlyOneBreakpoint` (line 235)

```go
widths := regexp.MustCompile(`\((?:max|min)-width\s*:\s*([^)]+)\)`).FindAllStringSubmatch(stylesheet(t), -1)
if len(widths) != 1 { t.Fatalf(...) }
if got := strings.TrimSpace(widths[0][1]); got != "780px" { t.Errorf(...) }
```

**Trips when**: the count is not exactly 1, or the value is not `780px`.

**The one correct way**: every width-conditional rule goes **inside the block that
already exists at `crswd.css:1041`**. A second `@media (max-width: 780px)` fails
this even at an identical width.

**Must fail when** a task adds a second width query anywhere, at any value.

**Never** use range syntax (`(width <= 780px)`). It would slip past this regex
while doing exactly what the guard forbids. That is routing around a hook.

### G2 — `TestNoRuleCarriesAValueThatBelongsInAToken` (line 173)

Sweeps everything after the token block, media preludes stripped first:

```go
{"a hard-coded length", regexp.MustCompile(`\d+(\.\d+)?(px|rem|em|pt|ch|ex|vh|vw)\b`)}
```

**Trips when**: any literal colour, colour function, colour keyword, length, or
external origin appears in a rule.

**`%` is not in the unit list.** `inset(50%)` and `inline-size: 100%` are legal
and already used. Verified, not assumed.

**Must fail when** a task writes `44px` or `16px` inline instead of spending a
token.

### G3 — `TestEveryTokenReferencedExists` (line 2076)

**Trips when**: a rule spends `var(--x)` and no line defines `--x`.

**The one correct way**: add the token to the block at the top **in the same
commit** as the rule that spends it.

**Must fail when** a rule references a token a later task was going to add.

**This guard has a known gap, and this milestone is fixing an instance of it.** It
checks a referenced token *exists* — not that it is of a kind the property
accepts. `background: var(--glow)` passes it and renders nothing, because `--glow`
is a shadow list. See `hygiene.md`.

### G4 — `TestTheTokenBlockIsTheDesignSystem` (line 133)

`designTokens` at line 31 is a **hand transcription of `docs/design-system.md`**,
deliberately not read from the stylesheet. The comment at line 21 says why: a test
comparing the file against its own spelling would still pass on a palette that had
quietly drifted.

**A new token is a three-file change in ONE commit**:

1. `docs/design-system.md` — declare it and why it exists
2. `web/static/crswd.css` — add to the token block (the first rule)
3. `internal/httpapi/stylesheet_test.go` — add to `designTokens`

**Must fail when** a token is added to the stylesheet and the map but not the
document — which passes every test and breaks the map's stated premise.

### G5 — `TestTheStylesheetAndTheMarkupNameTheSameThings` (line 443)

**Trips when**: a rendered class has no rule, **or** a styled class is rendered
nowhere. `styledClasses` (414) strips media preludes, so a class styled only
inside a query still counts.

**Cannot fire in this milestone** — no task introduces a new class. If a task
finds itself wanting one, that is a signal it has left the plan.

### G6 — `TestTheComponentsDocumentNamesThePickerTheSwitchAndTheHeader` (line 499)

**Trips when**: a `.combo*`, `.switch*` or `.masthead*` rule exists that
`docs/components.md` never names, or the reverse.

**Verified current state**: the doc and stylesheet agree exactly on eight names —
`.combo`, `.combo-list`, `.combo-status`, `.masthead`, `.masthead-bar`,
`.masthead-link`, `.switch-input`, `.switch-label`.

Adding a **rule** for an already-named class is free. Introducing a **new** name in
those three families requires a `docs/components.md` edit in the same commit.

**Note**: `.field-switch` is NOT in this family — the regex anchors on the dot, so
`.field-switch` does not match `\.(combo|switch|masthead)`.

### G7 — `TestHiddenAlwaysWins` (line 1766)

**Trips when**: the `[hidden] { display: none !important }` rule is missing, or a
`\n\.[a-z-]+\s*\{[^}]*display:` match appears after it.

**The one correct way**: put every new rule and every new block **before** the
`[hidden]` rule at the end of `crswd.css`.

The ordering regex requires column zero, so an indented rule inside a media block
placed after `[hidden]` would slip past it. **Do not rely on that.** It would be
correct CSS and a dishonest pass.

### G8 — `TestReducedMotionStopsEveryTransition` (line 274)

**Trips when**: the reduced-motion block's `*` rule stops setting
`transition: none`, or anything inside that block sets a non-`none` transition.

**The one correct way for this milestone**: add no transition and no animation
anywhere. The universal reset already covers what exists.

### G9 — `TestTheFocusRingSurvives` (line 217)

**Trips when**: `:focus-visible` stops setting `outline` / `var(--phosphor)` /
`outline-offset`, or `outline: none` or `outline: 0` appears **anywhere** in the
file.

**The one correct way**: never write `outline: none`. Nothing here should touch
the ring.

---

## What every assertion in this milestone must look like

**The obligation, carried from milestone 4's failure**: the proof must read the
**parsed stylesheet** — the block a declaration lives in — not a substring of the
file.

```go
// CORRECT — reads the rule
pane := blockFor(t, stylesheet(t), ".pane")
if !strings.Contains(pane, "overscroll-behavior-x") { t.Error(...) }

// CORRECT — reads inside the one media block
narrow := blockFor(t, stylesheet(t), "@media (max-width: 780px)")
if !strings.Contains(blockFor(t, narrow, ".pane"), "pre-wrap") { t.Error(...) }

// WRONG — a substring of the whole file proves nothing about which rule carries it
if !strings.Contains(stylesheet(t), "pre-wrap") { t.Error(...) }
```

The second form is what milestone 4 shipped three times while the control it was
about went unchanged. `stylesheet()` strips comments, so a comment cannot satisfy
even the wrong form — but the wrong form still passes when the declaration lands
in the wrong rule, which is the failure that matters here.

---

## Worked example — adding one declaration correctly

Task: give `.pane` contained horizontal overscroll.

```
1. Read crswd.css:891, the .pane rule.
2. Add `overscroll-behavior-x: contain;` inside it.
   - G2: no literal — `contain` is a keyword, not a length or colour.  ok
   - G3: no token referenced.                                          ok
   - G7: .pane is far above [hidden].                                  ok
3. Add to stylesheet_test.go:
     func TestThePaneDoesNotChainItsOverscroll(t *testing.T) {
       t.Parallel()
       pane := blockFor(t, stylesheet(t), ".pane")
       if !strings.Contains(pane, "overscroll-behavior-x: contain") {
         t.Errorf("the pane chains its horizontal overscroll to the page: %q", pane)
       }
     }
4. Full gate: go build, go vet, go test ./..., -tags tmux, -tags quickstart,
   golangci-lint run  (v2.12.2 — verify with `golangci-lint version`)
```

---

## The linter version, because it has cost this project a milestone before

`golangci-lint` **must be v2.12.2**. v1.62.2 reads the v2 config, runs a subset,
and reports success — fourteen iterations once passed that way with fifteen real
issues outstanding.

```
golangci-lint version    # must print 2.12.2
```

**Must fail when** the gate is run with a v1 binary, which reports green on a
config it does not understand.
