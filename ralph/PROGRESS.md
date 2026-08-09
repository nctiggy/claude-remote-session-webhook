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
