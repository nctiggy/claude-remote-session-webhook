# Contract: The working-directory picker

**Files**: `web/templates/partials/create-form.html`, `internal/config/discover.go`
**Tests**: `internal/httpapi/partials_test.go`, `internal/config/discover_test.go`
**Satisfies**: FR-038 … FR-045, SC-008, SC-009

---

## The control is markup, not script

```html
<input type="text" name="workdir" list="workdir-suggestions"
       placeholder="/home/nctiggy/code/…" required>
<datalist id="workdir-suggestions">
  <option value="/home/nctiggy/code/claude-remote-session-webhook">
  <option value="/home/nctiggy/code/customer-opportunities">
</datalist>
```

With **no scripting at all**, this is already the whole feature: the browser
filters as the operator types (FR-039), the control is keyboard-operable
(FR-044), options are announced to a screen reader (FR-044), any path can still
be typed in full (FR-040), and the field remains exactly the free-text field that
exists today (FR-043).

The script adds one thing and only one: the FR-045 announcement that the list is
showing a subset. An enhancement over a control that already works — the same
shape as every other enhancement in this interface.

> **Why not the abandoned branch's combobox.** `claude/issue-issue-59-...` built
> 225 lines of `crswd.js` reimplementing filtering, focus management and ARIA
> roles the platform already provides correctly. With scripting off it degrades
> to nothing, and it owns accessibility bugs the browser would otherwise own.
> Its **discovery walk is genuinely valuable and is carried forward**; the
> control itself is replaced by markup.

## Where suggestions come from

| Source | Key | Default |
|---|---|---|
| Explicit list | `workdir_suggestions` | empty |
| Discovered | `discover_roots` | **off** (FR-041) |

Discovery lists subdirectories **one level** below each approved root. It is off
by default because listing a filesystem is a disclosure, however mild, and an
operator should opt into it.

## A suggestion is never an authorisation

**This is the security half of the contract.** A path chosen from the list is
validated *exactly* as a typed one is — same allowlist check, same refusal, same
audit record (FR-042). The `<datalist>` submits an ordinary string and the
handler cannot tell the difference, which is the property that makes it safe.

A path appearing in the list grants nothing. A path absent from it is still
acceptable if typed.

## Contract tests

| Test | Asserts | **Must fail when** |
|---|---|---|
| `TestPickerWorksWithoutScript` | The rendered form contains `<input list>` and a `<datalist>`; no script is required to submit it | The control becomes script-dependent (FR-043) |
| `TestAnyPathStillTypeable` | A path absent from the datalist submits and is accepted if allowlisted | A `<select>` replaces the input (FR-040) |
| `TestChosenPathValidatedIdentically` | A suggested path outside the allowlist is **refused**, same as a typed one | The handler trusts a value because it was suggested — the one real vulnerability here |
| `TestDiscoveryOffByDefault` | With no `discover_roots`, the datalist holds only explicit entries | Discovery defaults on (FR-041) |
| `TestDiscoveryListsOneLevel` | Subdirectories of roots appear; grandchildren do not | The walk recurses |
| `TestDiscoveryNeverLeavesRoots` | A symlink out of a root is not listed | The walk follows symlinks out of containment |
| `TestSubsetAnnounced` | When filtered, the control says so | FR-045 is dropped |
| `TestNoSuggestionsRendersPlainField` | Empty list → the field alone, no empty datalist | An empty `<datalist>` is emitted |
