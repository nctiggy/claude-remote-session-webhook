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

## Iteration 0 — 2026-09-01 — the loop had stopped, and nothing was broken

**Did:** Archived milestone 15 to `ralph/archive/progress-milestone-15.md` and its
plan to `ralph/archive/milestone-15-tasks.md`, then swept every finding milestone
15 recorded as noticed-but-not-fixed to find out which are still real.

**Why this iteration exists.** `ralph/PROGRESS.md` had not moved in nine days. The
cause is not a failure: it is that `loop.sh` refuses to start while the notebook
carries the exit sentinel, and by 23 August that notebook carried **four** of them
— milestone 15's, then one each appended by specs 012, 013 and 014, which ran the
Spec Kit lane through the same file. The guard was doing exactly its job. What was
missing underneath it is a milestone to run.

**Learned — what the sweep actually found:**

- **Every fix-lane finding milestone 15 wrote down has since been taken.** Each was
  carried forward by name through Iterations 1–7 as still open, so the notebook
  reads as though a backlog accumulated. It did not. Measured on this tree:
  - `internal/httpapi/render.go` **is gofmt-clean** — `gofmt -l .` names nothing.
  - `TestTheOpeningScreenIsSentWithoutWaitingOutAnInterval` **is no longer
    wall-clock flaky.** It ran against a 10ms cadence; it now takes
    `slowTickUnderTest` deliberately, and its comment records that the old
    assertion "was really 'is this machine busy?'".
  - `internal/config/migrate.go`'s header **no longer claims `cmd/crswd` is the
    only writer** — it now says every write in the package is in `write.go`.
  - `internal/config/write.go`'s header **names all four callers**, not two.
  - `config.WriteFile`'s temporary-file prefix **is documented for the reason it
    was flagged**: what comes through it is also a systemd unit, so it is named
    for the daemon rather than for a configuration.
  **The lesson for a future sweep: a finding restated in six consecutive
  iterations is evidence of a copy-forward habit, not of six months of neglect.
  Re-measure before planning against one.**

- **The tree on `main` is green.** `go build ./...`, `go vet ./...`,
  `go test ./...` (all 11 packages ok) and `gofmt -l .` were run here and pass.

- **The open task counts in `specs/*/tasks.md` are stale checkboxes, not work.**
  003, 004, 005 and 007 report 12–16 open apiece for milestones that shipped;
  006's two are `stage.go` and `swap.go`, both of which exist; 009's and 010's
  single items are each "PR against `main`", and both PRs merged (#139, #140).
  Nothing in `specs/` is a source of open work.

**Left:** three items, and **every one of them needs the operator**, which is why
this notebook stops here rather than picking one. They are set out with their
evidence in `ralph/IMPLEMENTATION_PLAN.md`.

**Findings — noticed, not fixed:**

- **⚠️ The config migration still runs in the *old* binary**, so a rename shipped
  in v0.90 is applied by the update *after* the one that installs v0.90. Iteration
  7 marked this **"a spec question, not a bug — do not 'fix' it silently"**, and
  that still holds. It costs nothing today: `renamedKeys` is empty and
  `SchemaVersion` is 1. T007 made one of the two fixes cheap — the staged
  candidate's own `crswd config migrate` is now the same code the updater runs, so
  exec-ing it changes *when* it runs and nothing else. Which fix is right is a
  decision, not a discovery.

- **`docs/mobile-open-questions.md` Q2 is still UNANSWERED**, and spec 014 may have
  overtaken it. Q2 asks whether a bare provenance word reads as part of the value
  once settings rows stack below 780px. Spec 014 then **removed the Source column
  entirely** and marks a value from somewhere else on its own row. The question was
  written against a layout that no longer ships. **It should be re-read before it
  is answered** — the fallback it specced (an explicit label in the row) may
  already be moot, and that file's own rule is that a question is answered by the
  operator's report, never by a task claiming it.

- **#120 and #121 are unchanged and were deliberately deferred.** Milestone 15 put
  both out of scope in writing. #120 (reflow at the PTY rather than wrapping in
  CSS) is the real answer to a phone-width pane and is a session-plumbing change,
  not a CSS one; #121 (a wrap/alignment toggle) was gated on the wrap having been
  used in anger first.

- **The Spec Kit lane and the Ralph lane share one notebook and one sentinel.**
  Specs 012–014 appended `RALPH_COMPLETE` to the milestone-15 notebook, which is
  what left four exit sentinels in a file whose guard tolerates none. Nothing was
  harmed, but the next loop start would have been refused on a file the Ralph lane
  had finished with weeks earlier. Either lane may write here — but only the lane
  that owns the current plan should write the sentinel.

---

## Iteration 0, continued — milestone 16 chosen, and two decisions taken

The three remaining items were put to the operator rather than picked. Both
answers are recorded here because the next iteration will otherwise re-derive
them, and one of them contradicts an issue this repository wrote itself.

**Decision 1 — milestone 16 is #120**, reflow at the PTY instead of wrapping in
CSS. The other two items stay where they are: the migration/old-binary question is
still a spec question, and Q2 is still the operator's to read on a phone.

**Decision 2 — the width policy is *offered, not taken*.** The browser reports its
width advisorily; the daemon never acts on it by itself, and a reflow is a
deliberate act that changes the session for every reader at once. Resize-on-view
was rejected for the reason #120 itself gives — hostile to a second viewer — and a
manually pinned width was rejected because making the operator set 44 by hand on a
phone is the problem the milestone exists to remove.

**Learned — tmux facts, measured on this host against tmux 3.4, detached sessions,
no client attached. Do not re-derive these; they cost an hour.**

- **`resize-window` works on a detached session.**
  `resize-window -t '=crswd-abc:' -x 44 -y 12` → rc 0, `window_width=44`.
  `PaneTarget` is the correct target helper; `resize-window` takes a window target
  and `=name:` satisfies it. No third target helper is needed.
- **tmux reflows content already on screen.** An 80-column line already in the
  pane came back re-wrapped at 44 **with no new output and no client attached**.
  This is the fact the whole milestone rests on, and it is better than assumed —
  the operator does not wait for the TUI to repaint before the pane reads.
- **⚠️ `new-session -t` does NOT give a grouped session its own width**, which is
  the option #120 says it would look at first. `new-session -d -t a -s b -x 100
  -y 30` returns 0 and leaves `#{window_width}` unchanged for both. Grouped
  sessions share the window, a window has one size, and `-x`/`-y` describe a
  **client's** size — inert for a daemon that never attaches a client. **The issue
  is wrong on this point and the plan says so.**
- **⚠️ `resize-window` implicitly sets `window-size` to `manual`** — it reads
  `latest` before the call and `manual` after, and nothing sets it back. A
  reflowed session therefore stops sizing itself to a terminal that later attaches
  on the host. That is a change to how the operator's own terminal behaves, and
  this daemon does not make those silently; T005 and T007 carry it.

**Left:** milestone 16, T001–T007, in `ralph/IMPLEMENTATION_PLAN.md`. Nothing is
blocked. The loop can run.
