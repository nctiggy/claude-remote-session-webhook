# Implementation Plan

**Milestone 8 — Close the gaps milestone 7 found.**

Five issues, filed after the mobile sweep and each verified against the code before
filing. No spec directory: these are bugs with issue numbers, not a feature.

---

## The shape they all share

**Every task here exists because something in this repository stated an invariant
that nothing enforced.**

| What it says | What is true |
|---|---|
| `designTokens` transcribes `docs/design-system.md` | No test opens that file (#116) |
| `settings.go`: no mutating verb reaches the config | `POST /settings/edit` shipped in milestone 5 (#117) |
| `components.md`: the Toast "has no section and no use" | `.action-toast` renders in three templates (#119) |
| The class sweep holds stylesheet and markup to the same names | It cannot see an element selector (#118) |
| `freePort` returns a free port | It returns a port that *was* free (#123) |

Four of the five are **a document or comment that is confidently wrong.** In a
pipeline whose executor reads comments as contract, that is not tidiness — it is a
defect that costs an iteration, or worse, is believed. `settings.go`'s is the sharp
one: it reassures a reader about a security property that stopped being true.

---

## ⚠️ The one to be careful with

**T005 (#123) is the only task that touches a test harness rather than a document.**
A wrong fix makes the suite flaky in a *new* way instead of the old one, and the
failure mode is a red run every few dozen builds that everyone learns to re-run.

Read the issue's three options before choosing. The recommendation there is the
retry, because it is cheap and this is a harness — but say in the commit message
why the window exists, so the next person does not rediscover it.

---

## ⚠️ T003 is the task most likely to grow

Once the sweep can see element selectors it may report rules nobody has looked at
since #103.

**Anything it finds must be checked against the templates before deletion.**
Milestone 7's T015 was expected to delete four rules and kept two — `.settings th,
td` and `.settings p` were load-bearing and sole, and deleting either would have
left an unstyled page **no guard could report**, because the blindness runs in both
directions.

If the new sweep reports more than a couple of rules, **do not fix them all in this
task.** Land the guard with the rules it forces you to fix, and open an issue for
the rest. A task that ends green having deleted six rules on suspicion is worse
than one that ends green having deleted none.

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
  runs zero linters, and exits 0 — a green that means nothing. The session-start hook warns
  when this is the case; believe it (#26).
- `go.sum` must never appear. An import needs justification under `docs/security.md` §5 first.
- **AR-005: a test satisfies the cross-site checks, it never disables them.**
- **AR-008: no refactoring outside the task**, however obvious the improvement.
- **A task is not done when the code exists. It is done when something calls it.**
- **A new guard must be proven by breaking it.** Every task below that adds a test must
  show the test failing against the unfixed state before it is called done — milestone 7's
  card fix did this, and it is the only way to tell a guard from decoration.

---

## Tasks

- [x] **T001** (#116) Make `docs/design-system.md` the thing `designTokens` is checked against. Parse the document's declared tokens and compare with the map at `internal/httpapi/stylesheet_test.go:31` in **both** directions — a token in the map the document does not declare is a fabricated transcription; a token the document declares that the map omits is the drift the map exists to catch. **Prove it by breaking it**: add a token to the stylesheet and the map only, and watch the new test fail.

- [ ] **T002** (#117) Rewrite the header comment of `internal/httpapi/settings.go` (lines 3 and 12–18). It says *"No mutating verb is registered on this path"* and *"Writing the operator's configuration file from a browser is the highest-consequence surface in the product (spec, Out of Scope)"*. The first is true only of `GET /settings`; `internal/httpapi/settings_edit.go:39` registers `POST /settings/edit` and `settings.html` renders forms that post to it. Describe the doors that exist: a GET that renders, a separate POST that writes, what gates it, and **why the write is a different pattern rather than a verb on the same one** — that is a good decision and it should be stated on purpose.

- [ ] **T003** (#118) Teach the stylesheet sweep to see element selectors. Minimum viable version: fail any rule naming an element **no template in the tree ever renders** — `.settings caption` was the case, and the class sweep read it as "something about `.settings`". Read the two warnings above before deleting anything this finds.

- [ ] **T004** (#119) Give the Toast a section in `docs/components.md`. The document currently says it *"has no section and no use"* while `.action-toast` renders in `dashboard.html`, `session.html` and `settings.html`. Describe what shipped: what it is for, when it is used instead of an in-place answer, that it is script-only, and that `#action-toast` must exist in the markup before the script writes to it — the live-region rule the document already states elsewhere, and whose absence made the update button navigate away for three attempts running. Consider widening `documentedComponentClass` so this cannot rot again.

- [ ] **T005** (#123) ⚠️ Fix the `freePort` race in `cmd/crswd/quickstart_test.go`. It binds `:0`, reads the address, **closes the listener**, and returns the address; the daemon binds it milliseconds later and anything can take the port in that gap. Read the issue's three options. Whatever is chosen, record **why the window exists** so it is not rediscovered.

---

## Out of scope

- **#120** — resizing the tmux PTY to the reader's viewport. The correct answer to
  terminal reflow, and a daemon change with a real multiple-readers-at-different-widths
  problem. Needs design, not an iteration.
- **#121** — a pane wrap toggle. **Explicitly blocked on Q1** of
  `docs/mobile-open-questions.md`: if the wrapped pane reads badly against Claude
  Code's real TUI, the fallback is reverting the wrap, which makes the toggle moot.
  Do not build it before the operator has answered.
- **Answering Q1 or Q2.** They belong to the operator and a real device. The loop
  must not tick them.
