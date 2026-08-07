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
`RALPH_COMPLETE` — `loop.sh` stops on it.

---

---

## Iteration 0 — milestone 5 begins

**Did:** Archived milestones 1–4 to `archive/progress-milestones-1-4.md` (3,256 lines) and
started a fresh notebook, exactly as at the start of milestone 4.

**Learned:** The archived file ended with a line reading exactly `RALPH_COMPLETE` — loop.sh's
own exit sentinel, matched with `grep -qxF`. **This is the second milestone in a row where
that trap was live.** Starting against it exits after one iteration reporting the plan
complete with every task still open. Iteration 0 of milestone 4 recorded this and it happened
again anyway, because the note lives in the file that gets archived. If a third milestone
starts this way, fix `loop.sh` rather than the notebook: have it refuse to start when the
sentinel is already present, which is a two-line guard and permanent.

**Left:** T001–T016 in `IMPLEMENTATION_PLAN.md`, all open.

**Findings:** Two tasks close defects in code milestone 4 shipped, both found by *running* the
daemon during planning rather than reading it. T014 fixes a dependency probe that has warned
on every start of the live deployment that `claude` is missing while sessions using it work —
it asks the service manager's PATH and the command runs in a login shell. T013/T015 fix an
audit-trail command that cannot work because the daemon's diagnostics share a stream with its
records; `_COMM=crswd` still fails, which is how the cause was identified.

The create-form chain T001 → T003 → T006 → T008 is strict. Four stories touch that one
template, and the order exists so a failure names one story.

---

## Iteration 1 — 2026-08-07 16:10

**Did:** T001. Removed the resume field and its datalist from `create-form.html`, the
`Conversations` field from `createFormView`, and the projection behind it. Added
`TestCreateFormHasNoResumeField` (markup) and `TestViewCarriesNoConversations` (the struct's
own fields, by reflection). `TestTheRenderedFleetOffersPriorConversations` in
`dashboard_test.go` became `TestTheRenderedFleetReadsNoConversationStore` — the same claim
read the other way round, asserted on a host that really has a conversation recorded, which
is the only state a surviving walk would show itself in.

**Learned, and worth knowing before T003 touches the same file:**

- **Removing a view field drags three things with it, not one.** `unused` is enabled in
  `.golangci.yml`, so deleting `Conversations` made `conversationOffer`, `conversationOffers`
  and `maxOfferedConversations` lint errors. T003 removes `StartCommands` and will meet the
  same thing: `s.cfg.StartCommands.Names()` is the call to drop, and anything that becomes
  caller-less with it must go in the same commit or the tree is red.
- **`rootPaths`'s doc comment had `maxOfferedConversations` wedged into the end of it** —
  the const was inserted mid-comment, so `rootPaths` itself had none. Deleting the const
  reattached it. Nothing was rewritten; the comment is the one that was already there.
- **Both new tests were confirmed against their own mutation** rather than assumed: a
  `<input type="hidden" name="resume">` added to the form, and a `Conversations []string`
  left on the view. Both failed, both reverted. This is cheap and it is the exact defect the
  milestone exists for — the task's "must fail when" is a claim, and a claim can be checked.
- **The dashboard quickstart suite runs locally even with the daemon up.** It binds a free
  port, never 8765; only two startup cases in `quickstart_test.go` need that exact port. So
  `go test -tags quickstart ./cmd/crswd -run TestDashboardQuickstart` is available to anything
  touching a template, and it takes 14s. It passed.

**Left:** T002–T016. T002 is next and it is the other half of this one: `conversation.go` is
now caller-less, which is precisely the state the plan's fourth-time-shipped warning is about.
The route still reads `resume` out of the form (`fieldResume`, `actions.go:283`) and still
passes it to `Manager.Create` — deliberately untouched here, because T002 owns both the file
deletion and `TestStrayResumeValueIsNotExecuted`. **T002 must record the deleting commit's SHA
in issue #95 before the file is gone.**

**Findings:** none new. One thing noticed and deliberately not fixed under AR-008: the
template now has two adjacent comment blocks (`*/}}{{/*`) where the conversation comment sits
against the start-command one. T003 deletes the second of them, so tidying it now would only
make that story's diff harder to read.

---

## Iteration 2 — 2026-08-07 16:16

**Did:** T002, in commit `ef18756aa39aee9c7219e1f32c4a26f5dfdacb0e`. Deleted
`internal/session/conversation.go` and `conversation_test.go`, everything that fed them, and
everything that existed to refuse their input. Added `TestStrayResumeValueIsNotExecuted` to
`internal/httpapi/actions_test.go`. 1,239 lines deleted, 66 added.

**⚠️ OUTSTANDING — the SHA is not on #95.** `gh` is not an approved command in this loop's
session and the session is non-interactive, so the comment could not be posted. The task
requires it, so it is written out here to be pasted verbatim onto issue #95:

> Deleted `internal/session/conversation.go` and its test in
> ef18756aa39aee9c7219e1f32c4a26f5dfdacb0e (branch `feat/m5-loop-1`, milestone 5 T002),
> recorded here so the code is recoverable: `git show ef18756^:internal/session/conversation.go`.
>
> Worth reading back before any auto-recovery work: the root check that runs *before* any
> store lookup (so the listing cannot be an oracle for what exists elsewhere on the host), the
> listing that opens no file, the symlink exclusion, and `storeDirName`'s separator-flattening,
> which removes the means of traversal rather than checking for it.
>
> Note the ambiguity this file could not resolve, and which is why it went rather than waiting:
> `listConversations` answers about *this directory's* conversations, while #95 needs *this
> session's* — and a directory two sessions share has a most-recent conversation belonging to
> whichever wrote last. FR-032 refuses to guess between them.

**Learned:**

- **The deletion is not two files, it is a whole path — and the path is longer than the plan's
  one line suggests.** `conversation.go` was called from `Manager.Create`, so removing it
  forced out `CreateRequest.Resume`, `Manager.conversationStore` (and with it the
  `os.UserHomeDir` lookup and the `os` import), `resumeFlag`, and `start`'s third parameter;
  `ErrUnknownConversation` forced out its `case` in `refuseBrowserCreate`, its entry in
  `createReason`'s sentinel list in `sessions.go`, and `outcomeBadConversation` with its
  banner. Eight files. **T003 will be the same shape** — the note from iteration 1 stands and
  is now measured: budget for the cascade, not for the edit.
- **Deleting a field deletes its guard, which is the whole reason the new test is about argv.**
  `resume` was safe because `resumableID` refused anything that was not letters, digits, `-`
  and `_` before it was appended to a command line. That alphabet is gone with the file. So
  the assertion that matters is not "the field is refused" — nothing refuses it now, and the
  create answers `created` — it is "no byte of it reaches what the host runs".
  `TestStrayResumeValueIsNotExecuted` posts `resume=$(whoami)` and sweeps every `Call.Argv`
  and `Call.Stdin` the fake tmux recorded. **It is the tripwire for any later task that puts
  request text on a command line**, whichever field name it uses.
- **Verified against its own mutation, as iteration 1 did.** Restoring the three lines — the
  `Resume` field, `req.Resume` into `start`, and the append — failed it precisely:
  `"claude --dangerously-skip-permissions --resume $(whoami)"` on the send-keys argv. Reverted
  by copying the two files back from a scratch dir inside the repo, because `git checkout`
  was not available with the rest of the change uncommitted and the sandbox refuses `/tmp`.
- **`c.fixture.tmux.Calls()` is the right lens for "did this reach the host".** `Call.Argv`
  carries the command line (`argvSendKeys`), `Call.Stdin` carries paste payloads, and nothing
  else reaches a pane. `c.started()` counts `OpNew` only, so it says a session began and
  nothing about what it was told to run.
- **The quickstart suite is worth the 13s here.** It drives a real create against a real
  daemon, which is the path this change rewired. It passed, as did `-tags tmux` and `-tags dev`
  under `go vet`.

**Left:** T003–T016. T003 is next and it is the create-form chain's second link: replace the
start-command `<select>` with the `remote_control` switch, and remove `StartCommands` from the
view and from `dashboard.go:297`. Expect `s.cfg.StartCommands.Names()` and whatever becomes
caller-less with it to have to go in the same commit — that is the cascade above, again.

**Findings:**

1. **`refuseBrowserCreate`'s doc comment claims "the same four branches" and there are five.**
   It was already wrong before this iteration (there were six), and this change moved the count
   without fixing the prose. **T004 rewrites what that function accepts**, so it owns the
   correction; doing it here would put an unrelated hunk in a deletion commit. Same for the
   const block above `createFromBrowser`, which says "The two fields a create carries" over
   three constants — T003 and T004 both edit that block.
2. Nothing else. `web/templates/partials/create-form.html` was not touched this iteration, so
   the create-form chain's ordering is intact for T003.

---

## Iteration 3 — 2026-08-07 16:27

**Did:** T003, in commit `73b9933`. Replaced the start-command `<select>` with the contract's
`remote_control` checkbox, removed `StartCommands` from `createFormView` and from the fleet
projection, and added the four tests. Also added `.field-switch`, `.switch-input` and
`.switch-label` to `crswd.css` — see below, that was not optional.

**Learned:**

- **The cascade T001 and T002 warned about did not happen here, and the reason is worth
  knowing:** `StartCommands.Names()` has four other callers (`settings.go:253`,
  `manager.go:274`, `depcheck.go:90`, and `String()` itself), so dropping the dashboard's call
  freed nothing. The cascade is a property of the *called* thing, not of the removal — check
  callers before budgeting for it.
- **A class rendered by a template must have a CSS rule in the same commit.**
  `TestTheStylesheetAndTheMarkupNameTheSameThings` sweeps both directions, so the three switch
  classes the contract spells would have gone red on their own. **T009 still owns the switch's
  presentation** — `appearance: none`, the focus ring, the reduced-motion rule — and what is
  there now is the minimum that sweep demands: a row layout, `accent-color: var(--phosphor)`
  on the native box, and the eyebrow the label already is. No transition, so the universal
  reduced-motion reset covers it untouched, and no `outline: none`, so the global
  `:focus-visible` ring applies as-is.
- **`TestCreateFormHasNoStartCommandSelect` had to become a page test to mean anything.** The
  chooser was wrapped in `{{ if gt (len .StartCommands) 1 }}`, so a component rendered from
  `createForm()` never drew it — a component-only assertion passes with the `<select>` still
  in the template. **That is the milestone-4 miss one layer down**, and it was caught by
  running the mutation rather than by reading. Verified: with the field, the projection and
  the template block all restored, the component subtest passes and the page subtest fails on
  all three markers. The same mutation fails `TestCreateFormRendersNoCommandName` (`default`
  and `rc`, twice each) and `TestViewCarriesNoStartCommands`.
- **`TestCreateFormRendersNoCommandName` is a page test by necessity, not by preference.** The
  view no longer carries the names, so a component cannot leak what it was never handed; the
  thing that can still go wrong is the projection putting them back. It sweeps the `create`
  section only — a *card* names the command its session runs (#38), which is a different
  disclosure with a different argument behind it.
- **`<option` is not a usable marker for the chooser and must not be added to that sweep.**
  The working-directory datalist renders options legitimately, and **T006 makes it render them
  on every install** — the roots become a source. The `<select>` around them is the marker.
- `goimports` adds `internal/config` to `partials_test.go` on save, so the page tests need no
  import bookkeeping.

**Left:** T004–T016. T004 is next and it is this task's other half: read `remote_control` in
`actions.go`, `on` → remote, absent → local, everything else the uniform refusal. **Until it
lands the switch posts a field nothing reads** — the route still reads `start_command`, which
the form no longer sends, so every browser create runs the default. That is the plan's chosen
ordering, not a defect, but it is live behaviour on a deployed daemon between these two
commits. T004 also owns the two stale prose fixes iteration 2 logged (`refuseBrowserCreate`'s
branch count, and the "two fields" const block, which T003 left alone).

**NEEDS CLARIFICATION — carried into T004, not blocking T003:**

**What does the switch mean on a daemon that configures no remote-control command?**
`config.go:212` states the intent plainly — *"A daemon that configures no such command offers
no switch at all rather than one that cannot work"* — and `loadRemoteControlCommand` leaves
`RemoteControlCommand` empty in exactly that case. But T003's contract spells the markup as a
literal with **no conditional**, the view carries nothing that could express one, and **no
task in this milestone adds a field for it**. So the switch now renders on every daemon,
including one where remote control cannot work. I did not invent the conditional (Principle
II): it needs a view field nothing specifies. **T004 decides what `remote_control=on` does
when no remote command is configured** — refuse, or silently start a local session, which
`config.go:124` already calls "worse than no switch". Related: `data-model.md` lists a new
`RemoteDefault bool` on the create-form view (*"whether the switch renders on"*) and **no task
creates it either**. Neither of these is the same field, and neither has an owner.

**Findings:**

1. **The `docs/components.md` Form section does not cover a checkbox**, and this form now has
   one. Its sketch is label-then-input, which is right for a text entry and wrong for a box;
   the template says why it departs. **T016 owns `docs/` for this milestone** and should add
   the switch to the Form rules — otherwise the next control to need one has no canonical
   spelling and invents a second.
2. **`.switch-label` duplicates `.field-label` exactly.** Deliberate, and flagged rather than
   fixed: T009 gives the switch its own presentation, and collapsing the two now would either
   pre-empt that or force T009 to unpick a shared rule. If T009 ends up not diverging them,
   the duplicate should go.
3. Nothing else. The create-form chain's ordering is intact: T006 is next in that file and
   nothing outside the chooser's own block was touched.
