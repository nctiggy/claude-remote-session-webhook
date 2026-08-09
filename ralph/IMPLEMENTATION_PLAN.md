# Implementation Plan

**Milestone 9 — Two operator requests.**

> *"All true/false settings should be check boxes."*
> *"Also can we have a way to restart the daemon from within the UI?"*

Six tasks. No spec directory: two features, both small, both verified against the
code before this plan was written.

---

## What is already true (verified, do not re-derive)

- **There are exactly two boolean keys**: `discover_roots` and
  `destroy_on_shutdown` — the only two callers of `loadBool` in
  `internal/config/config.go` (lines 610 and 629). Neither is a secret, so both are
  already `Editable`.
- **Neither feature needs a new class.** The switch (`.switch-input`,
  `.switch-label`) exists, is styled, and is named in `docs/components.md`. The
  restart reuses `.updating` and `.spinner`, which settings.html already renders.
  **That keeps both class sweeps and the components-doc guard out of the risk
  surface entirely** — do not introduce a new name and neither can fire.
- **`ExitForRestart()` and `exitGrace` already exist** in the updater and
  `internal/httpapi/update.go`. The restart route reuses both.
- **`Restart=always` is already in `deploy/crswd.example.service`**, added for
  self-update. Without it a restart is just a stop.

---

## ⚠️ The trap in the checkbox work is HTTP, not CSS

**An unchecked checkbox submits nothing at all.**

`internal/httpapi/settings_edit.go:59` reads
`r.PostForm.Get(fieldSettingValue)`. An unchecked box is therefore
indistinguishable from a cleared field, and "off" would read as "unset".

**Do not reach for the hidden-input trick.** A hidden field and a checkbox sharing
one name submit two values when checked, and `.Get` returns the **first** — which
is the hidden `false`. It looks right, tests green if the test only checks the
unchecked case, and silently makes every boolean unsettable.

**The fix**: the handler knows the key is boolean and reads an absent value as
`false` — and does so **only** for keys it knows are boolean. A truncated or
malformed request must never clear a setting that is not one. That rule is the
security-relevant half of this task and belongs in a test.

---

## ⚠️ The restart is a mutating route on the browser door

It joins through `s.handleAction(...)`, the same call destroy and update use, so
the gate, the audit record, the ownership check and **both halves of the
cross-site defence** are inherited rather than re-implemented. Registering it by
hand is how they get re-implemented differently.

**Why a browser may do this at all**: update already can, and the operator argued
that door open on #66 — an attacker with the dashboard can already start a
session running a permission-skipping assistant in an approved root, which is code
execution. Restart runs **the binary that is already installed**. It is strictly
less than update, which installs code from the internet.

**Sessions survive it.** That is what #63 bought, and it is why this is a
reasonable control to offer rather than a foot-gun.

---

## Conventions

- `- [ ]` open · `- [x]` done · `- [!]` blocked (reason in `PROGRESS.md`)
- Priority order is meaningful — the loop always takes the topmost open item.
- Each task must be independently completable **and verifiable** in one iteration.
- **Every task ends green**: `go build ./... && go vet ./... && go test ./... && golangci-lint run`.
  Add `-tags tmux` when the task touches tmux and `-tags quickstart` when it touches
  `cmd/crswd`. Tests ship inside the task that implements the behaviour — never as a separate
  failing test, which step 6 of `PROMPT.md` would make the iteration revert.
- **Check the linter is v2 before trusting it.** A pre-v2 binary reads this repo's v2 config,
  runs zero linters, and exits 0 — a green that means nothing (#26).
- `go.sum` must never appear. An import needs justification under `docs/security.md` §5 first.
- **AR-005: a test satisfies the cross-site checks, it never disables them.**
- **AR-008: no refactoring outside the task**, however obvious the improvement.
- **A task is not done when the code exists. It is done when something calls it.**
- **A new guard must be proven by breaking it** — show it failing against the unfixed
  state before calling the task done. It is the only way to tell a guard from decoration.
- **Everything works with no JavaScript.** A checkbox that only submits correctly with a
  script, or a restart that only reports progress with one, has failed the task.

---

## Tasks

- [x] **T001** Add a way to ask whether a config key is a boolean, in `internal/config/`. The two keys are `discover_roots` and `destroy_on_shutdown` — derive them from the `loadBool` call sites rather than hand-listing them somewhere that can drift, if that is reachable; otherwise list them next to `IsSecret`'s list and say why. Test that both are reported boolean, that a secret is not, and that an unknown key is not.

- [x] **T002** 🔒 Teach `internal/httpapi/settings_edit.go` that a boolean key's absent value means `false`. **Only for keys T001 reports as boolean.** A missing value for any other key must behave exactly as it does today. Test both halves: an unchecked boolean saves `false`, and a non-boolean with no value is still refused. **The second test is the security-relevant one** — without it, a truncated request clears a setting.

- [ ] **T003** Render boolean settings as a switch in `web/templates/settings.html`, reusing `.switch-input` and `.switch-label`. **Introduce no new class.** The checkbox reflects the current value and submits `true` when checked. Assert against the rendered markup that a boolean row carries a checkbox and a non-boolean row still carries a text input — milestone 4 shipped three green tasks about a control that never changed, and reading the markup is what stops that.

- [ ] **T004** 🔒 Add `POST /dashboard/restart`, registered through `s.handleAction(...)` exactly as destroy and update are. Confirming field `confirm` must equal `yes` — reuse `fieldConfirm`/`confirmYes`. Audit action `dashboard.restart`, exactly one record. It calls the same `ExitForRestart()` the update uses, from a goroutine after `exitGrace`, for the reason written above that constant: exiting before the response flushes is what made an earlier update arrive as a Cloudflare 502. Test the confirming step, both cross-site halves independently, and that exactly one audit record is written.

- [ ] **T005** Put a Restart control on the settings page, in the Updates section, reusing `.updating` and `.spinner`. It needs a confirming step in the markup and must say what it does — sessions survive a restart, and an operator who does not know that will not press it. **No new class.**

- [ ] **T006** Make the restart wait out the daemon in `web/static/crswd.js`, reusing the update's path. The submit handler branches on `form.matches('.update-form')` at line ~1043; the restart form should take the same branch. Widen the match rather than duplicating the branch, and keep the ordering the existing test pins — the swap must precede the toast, or the toast wins and the special case is unreachable. Update `TestTheUpdateDoesNotBecomeAToast` or add its sibling so both forms are held to it.

---

## Out of scope

- **#120** — resizing the tmux PTY to the reader's viewport. Needs design, not an iteration.
- **#121** — a pane wrap toggle. **Blocked on Q1** of `docs/mobile-open-questions.md`.
- **Answering Q1 or Q2.** They belong to the operator and a real device.
- **Any other control on the settings page.** Two requests, six tasks, nothing else.
