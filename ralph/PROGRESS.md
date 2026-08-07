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

---

## Iteration 4 — 2026-08-07 16:42

**Did:** T004, in commit `f66b94b`. The create route reads `remote_control` as a two-state
mode — `on`, or absent — instead of the `start_command` name the form stopped sending in T003.
Which command a mode runs is asked of `Manager.RemoteStartCommand()`, a new export over the
existing `commandForMode`'s remote branch. Five tests, all in `actions_test.go`. **This closes
the milestone's MVP: T001–T004 deliver what milestone 4 claimed.**

**The two open questions from iteration 3, answered:**

1. **`remote_control=on` on a daemon that configures no remote-control command → refused**
   (`outcomeCreateFailed`, `errModeUnavailable` on the record). **This was not invented.** The
   repo already states the rule in three places and they agree: `config.go:124` ("a switch that
   silently started plain sessions instead would be worse than no switch"),
   `Manager.commandForMode`, which refuses the identical mode for the identical reason on the
   toggle route, and `refuseBrowserCreate`'s unknown-name branch ("a caller who asked for
   remote control and silently got a plain session has no way to discover that is what
   happened"). Applying a stated rule to a new caller is not a guess.
2. **The switch still renders unconditionally, and that is now a UX wart rather than a hole.**
   Before this commit a daemon with no remote command showed a switch that silently did
   nothing; it now shows one that refuses honestly. The conditional still needs a view field
   nothing specifies, and `data-model.md`'s `RemoteDefault bool` still has no owner — but
   `RemoteDefault` is "whether the switch renders *on*", default `false`, which is what an
   unchecked box already is, so it is a no-op field and not the same thing as the conditional.
   **Neither is blocking any remaining task.** Carried to T016 or to a milestone-6 decision.

**Learned:**

- **`PostForm.Get` cannot express this field, and that is the whole design.** Absent,
  present-and-empty, and repeated all flatten to `""`. Absence is a *state* (local), so
  reading the map directly — `values, present := form[field]` — is what keeps the safe state
  reachable by exactly the one spelling a form produces. `offersRemoteControlState` requires
  `len(values) == 1 && values[0] == "on"`; everything else is the uniform refusal.
- **The mapping had to go through the manager, and reusing `commandForMode` wholesale would
  have been wrong.** Its *local* branch refuses when the remote-control command *is* the
  default — correct for a transition (nowhere to move to), and catastrophic for a create: on
  such a daemon every unticked create would error, which is precisely the "absence is treated
  as an error" the task forbids. So `RemoteStartCommand()` exports the remote branch only, and
  local stays `StartCommand: ""`, which `config.StartCommands.Command` already reads as the
  default. **Check what a shared helper refuses before sharing it.**
- **The refusal reuses `outcomeBadMode` and `errModeUnavailable` rather than adding codes.**
  Both already say exactly the right thing, and `outcome_test.go:27` sweeps the vocabulary, so
  a new code is a change in two places for no new sentence. The trail tells a create's refusal
  from a toggle's by `action` (`dashboard.create` vs `session.mode`), which is the arrangement
  every other shared reason on this door already uses.
- **The value check sits *after* the rate limiter, deliberately** — the opposite of
  `modeFromBrowser`, which reads its value first. The toggle's argument is that skipping a
  confirming step costs nothing; a budget is not a confirming step. A refusal in front of the
  limiter is one an unbudgeted stream produces for free, audit records included.
- **`offersRemoteControl` was already taken** — it is a `*testServer` method in
  `actions_test.go`. The production allowlist is `offersRemoteControlState`. A method and a
  package function may share a name in Go, but a reader should not have to know that.
- **The test fixture was lying and had to be fixed to assert the card.** `offersRemoteControl()`
  told the *manager* the remote name but not `s.cfg`, and `cardOf` derives the word a card says
  from `s.cfg.RemoteControlCommand` (dashboard.go:259). A production daemon sets both from one
  value at `server.go:346`, so the fixture was a daemon that cannot exist. One line added; the
  whole suite stays green.
- **`cardModeRow` and `markupTags` in `partials_test.go` are reusable** — same package — and
  they are how "the card says so in words" is asserted without a test that would pass on a
  `title` attribute.
- **Verified against three mutations, each reverted:** (a) the pre-T004 passthrough
  `StartCommand: r.PostForm.Get(fieldRemoteControl)` with the allowlist deleted — four of the
  five tests fail, including every row of the security case; (b) absence returning
  `("", false)` — `TestAbsentFieldMeansLocal` and the local half of the audit test fail;
  (c) the unavailable-remote branch falling through to the default —
  `TestRemoteControlOnWithNoRemoteCommandRefuses` fails alone. Note that a naive mutation of
  the struct literal alone will not compile: `startCommand` goes unused and Go refuses it.
- **A "the reason does not quote the value" substring check is a trap.** It was written and
  removed: `errCreateStateNotOffered`'s own sentence contains "on" (inside *remote-control*),
  so the `on`-valued rows failed on their own sentinel. Asserting the reason **equals** the
  sentinel is the stronger claim anyway — a fixed string cannot carry caller text.
- Linter confirmed v2 before trusting the green: `golangci-lint 2.12.2`, 0 issues. The
  quickstart acceptance suite ran uncached (14s) and passed; `-tags tmux`, `-tags dev` and
  `-tags quickstart` all compile.

**Left:** T005–T016. **T005 is next** and it is the plan's own warning made concrete: a
`workdir_suggestions` key with no reader is `CRSW_DESTROY_ON_SHUTDOWN` for the fifth time. The
create-form chain's next link is T006, and `create-form.html` was not touched this iteration,
so the ordering is intact.

**Findings:**

1. **`outcomeBadStartCommand`'s comment in `outcome.go` is now stale.** It reasons that "the
   operator picked from a list this page rendered", which stopped being true when T003 deleted
   the list. The *sentence* it justifies is still correct, and the branch is still reachable if
   the manager's command set and `cfg.RemoteControlCommand` ever disagree — so this is prose to
   correct, not a defect. Not fixed here: it is in a different file from anything this task
   changed, and AR-008 is load-bearing this milestone. **T016 owns the tidy-up.**
2. **`refuseBrowserCreate`'s "the same four branches" and the const block's "two fields" are
   both fixed** — the two stale-prose items iterations 2 and 3 assigned to this task. The
   branch count is now stated as a relationship rather than a number, so it cannot go stale
   again the next time a branch is added.
3. **`ErrUnknownStartCommand` is now unreachable from the browser door.** The name this door
   submits is the daemon's own, resolved from the same configuration the manager holds. The
   branch is kept and its comment says why: it is two objects agreeing rather than one fact,
   and it is the honest answer the day they do not. Worth knowing before anyone reads it as
   dead code and deletes it — the create's *name* validation is what it guards, and the API
   door still submits names.
4. **The daemon is live and this changes its behaviour.** Between `73b9933` (T003) and this
   commit, every browser create ran the default command regardless of the switch. It now
   honours the switch. Nothing needs migrating — the mode is derived from the start command
   already recorded — but an operator watching the deployment will see the switch start working.

---

## Iteration 5 — 2026-08-07 16:50

**Did:** T005, in commit `2b1f3b3`. `CRSW_WORKDIR_SUGGESTIONS` — comma-separated, absolute
paths — is declared, loaded through the `withFile` shim like every other key, and lands on
`Config.WorkdirSuggestions`. `TestWorkdirSuggestionsIsRead` in `config_test.go` plus two rows
in `TestLoadFromRejects`. **Nothing consumes the field yet; T006 is the union that does.**

**Learned:**

- **One new `CRSW_` constant forces edits in six files, and the compiler tells you about none
  of them.** `declaredVars` in `envexample_test.go` parses `config.go`'s own AST, so adding a
  constant instantly reddens four suites in three packages. The full list, for whoever does
  this next:
  1. `internal/config/config.go` — const, `Config` field, loader, call site, struct literal
  2. `internal/config/file.go` — `Vars()`, **in `config.go`'s declaration order**
  3. `.env.example` — assignment with a comment line *immediately above it* (no blank line
     between, or `TestEnvExampleDescribesEveryVariable` fails) and **no value**
  4. `README.md` — a row in the configuration table, first cell `` `CRSW_...` ``
  5. `deploy/crswd.example.service` — an inline `Environment=` line, empty unless the daemon
     has a non-empty default (`TestUnitInlineValuesAreTheDaemonDefaults` pins the ones it has)
  6. `config.example` — a commented `# key = value` line, **in `Vars()` order**, which is
     asserted positionally against `config.Vars()`
  7. `internal/httpapi/settings.go` — a `settingValue` case, or the row renders an empty cell
     and `TestEverySettingRendersAValue` fails
- **The validation question T005 actually has to answer is "which refusals?", and the contract
  answers it.** `contracts/directory-suggestions.md`'s worked example offers `/srv/scratch`
  and says it is refused on submit unless it is under a root — so containment is deliberately
  *not* checked at load. What is refused is only what no configuration could ever accept: a
  relative entry (`ResolveWorkDir` refuses non-absolute before it even reaches containment, so
  it is a suggestion with one possible outcome) and an empty entry. Nothing here touches the
  filesystem, which is the point rather than an omission — a stat behind a key that is live by
  default would be the disclosure `discover_roots` exists to keep opt-in.
- **Mutation-verified twice, both reverted.** Making `loadWorkdirSuggestions` ignore its
  `getenv` — the literal "declared and never read" failure — fails exactly three assertions and
  nothing else: the value check, and both reject rows. Note the settings-page suite stays
  **green** under that mutant, because an empty cell is still a cell; the config test is the
  only thing pinning the read. That is worth knowing before trusting `/settings` as the
  no-loader detector a second time.
- Linter confirmed v2 before trusting the green: `golangci-lint 2.12.2`, 0 issues. `go vet`
  compiles all three tagged suites, and the quickstart acceptance suite ran uncached (28s) and
  passed — `127.0.0.1:8765` was free.

**Left:** T006–T016. **T006 is next**: `internal/config/suggestions.go`, the union of roots ∪
`workdir_suggestions` ∪ discovered children, deduped and sorted, replacing
`s.cfg.DiscoveredWorkDirs()` at `dashboard.go:254`. It is also the third link in the
create-form chain (`T001 → T003 → T006 → T008`); `create-form.html` was not touched this
iteration, so the ordering is intact. Note the loader keeps duplicates *within* the list on
purpose — dedup is T006's, and two rules about it would be two answers.

**Findings:**

1. **`config.example:157` tells the operator a lie about `destroy_on_shutdown`.** It says
   "This build does not read the key. It parses, and /settings renders it, but the loader has
   no case for it yet" — which stopped being true when the loader was added (`config.go`, the
   `loadBool` call that the comment above it dates to #63). `README.md`'s row for
   `CRSW_DESTROY_ON_SHUTDOWN` says the same thing: "**This build parses the key and does not
   act on it**". Both now describe a daemon that has not shipped for some time, and an operator
   reading either will not set a flag that works. Not fixed here — neither file's line is part
   of this task and AR-008 is load-bearing. **T016 owns it**, and it is a two-line docs fix.
2. **`internal/httpapi/settings.go:114-118` carries the same stale claim in a code comment**,
   naming `CRSW_DESTROY_ON_SHUTDOWN` as the "key with no loader behind it" the page exists to
   expose. The paragraph's *argument* is still right and still worth keeping; only its example
   is spent. It also refers to "internal/config's own `varWithNoLoader`", which no longer
   exists anywhere in the tree. T016 again.
3. **`contracts/directory-suggestions.md:62` spells `allowed_roots` with commas**
   (`/home/nctiggy/code,/home/nctiggy/work`) where the real separator is `:`
   (`rootListSeparator`, fixed at colon on purpose). The contract is right about everything
   that matters and wrong in the one place an operator would copy from. Spec files are not
   this milestone's to edit, but it is worth a line in T016's docs pass.

---

## Iteration 6 — 2026-08-07 16:59

**Did:** T006, in commit `c71ef89`. `internal/config/suggestions.go` adds
`Config.SuggestedWorkDirs()` — the union of the approved roots (always), `WorkdirSuggestions`
(explicit), and `DiscoveredWorkDirs()` (only when `DiscoverRoots` is on), sorted and
deduplicated with `slices.Sort` + `slices.Compact`. `dashboard.go:254` now calls it. Five tests
in `internal/config/suggestions_test.go` and `TestDefaultInstallRendersOptions` in
`partials_test.go`. **The picker is no longer empty on a default install.**

**Learned:**

- **The method is `SuggestedWorkDirs`, not `WorkDirSuggestions`.** The field beside it is
  `Config.WorkdirSuggestions` (lowercase `d`), and Go would happily compile a method whose name
  differs from a field's by the case of one letter. `SuggestedWorkDirs` also reads as a sibling
  of `DiscoveredWorkDirs`, which is what it unions.
- **T006 breaks an existing test, and that is the task rather than collateral.**
  `TestTheRenderedFleetOffersWhatDiscoveryFound` (`dashboard_test.go:606`) asserted that with
  discovery off the page renders **no `<datalist>` at all** — true when discovery was the only
  source, false the moment roots became one. Rewritten to the new claim: with discovery off the
  form offers the root and not the root's child. Its doc comment's "wired to anything constant —
  the roots, a literal" example was corrected for the same reason.
- **The five config tests do not catch a union with no caller.** Under the mutant where
  `SuggestedWorkDirs` is correct and `dashboard.go` still calls `DiscoveredWorkDirs`, the whole
  `internal/config` suite passes and only `TestDefaultInstallRendersOptions` and the fleet's own
  render fail. That is this repo's four-times-shipped failure reproduced exactly, and it is the
  argument for the markup assertion rather than a nice-to-have.
- **Mutation-verified five ways, each reverted:** (a) discovery as the only source, the shipped
  defect — all five config tests and both markup tests fail; (b) deduped but unsorted — the
  sorted test and the union test fail; (c) sorted but not deduped — the union test alone fails;
  (d) the `DiscoverRoots` gate dropped inside the union — `TestDiscoveryStillOffByDefault`
  **and both markup tests** fail, so "the fix for emptiness turns discovery on" is caught in the
  rendered page and not only in a unit test; (e) the caller left on the old walk — see above.
- **`newDiscoveryFixture` in `discover_test.go` is reusable** — same `config_test` package — and
  it is the only fixture in the tree where all three sources can be live at once: two roots on a
  real filesystem, a child under each, symlinks that escape. `TestSourcesAreUnionedAndDeduped`
  uses it rather than building a second one.
- **A `<datalist>` now renders on every real page.** `TestNoSuggestionsRendersPlainField` still
  passes and still should: it hands the *component* an empty view, which is a state the running
  daemon no longer reaches because a daemon with no root refuses to start. `view.go`'s comment
  on `Suggestions` was corrected to say so — it claimed "no task in this milestone builds" the
  explicit source, which this commit falsified.
- Linter confirmed v2 before trusting the green: `golangci-lint 2.12.2`, 0 issues. `go vet`
  compiles all three tagged suites, and the quickstart acceptance suite ran uncached (28s) and
  passed. No `go.sum`.

**Left:** T007–T016. **T007 is next** and it is security-relevant: `TestSuggestedPathOutsideRootsRefused`
in `actions_test.go`, asserting a path that appears in the rendered `<datalist>` but is not under
an approved root is refused with the same response and the same audit record as a typed one. It
is a test-only task — the refusal already exists — and it is now genuinely reachable, because
`workdir_suggestions` loads a path outside the roots on purpose and this iteration made it
render. The create-form chain's last link is T008 (`T001 → T003 → T006 → T008`), and
`create-form.html` was **not touched this iteration**: the union changed what the template is
handed, never the template. The ordering is intact.

**Findings:**

1. **The union has no cap, and `DiscoveredWorkDirs` has one it does not announce.** The walk
   stops at `maxDiscoveredWorkDirs = 200` (silent, noted in milestone 4 iterations 23–25); the
   union then adds the roots and the explicit list on top, so a daemon can render more than 200
   options and no bound describes the total. Nothing is wrong today — the extra entries are
   paths the operator typed into their own configuration — but "200" no longer describes what a
   page can carry, and `discover.go`'s comment says the bound is about markup as well as work.
   Not fixed: it is a decision about a number nothing in the spec names, and inventing one is
   Principle II. **Worth a line in T016**, or a `NEEDS CLARIFICATION` if a later task wants the
   markup bounded.
2. **`specs/004-configure-and-operate/quickstart.md:116` is now describable as stale**: "Open
   the create form **with JavaScript disabled** | The field offers suggestions and filters as
   you type". That was aspirational when written (the field offered nothing on the shipped
   default) and is true for the first time as of this commit. No action — recording it because
   it is the one manual step in the milestone-4 quickstart that could not have passed before,
   and someone re-running that checklist should know it now can.
3. The three stale-prose findings from iteration 5 (`config.example:157`, `README.md`'s
   `CRSW_DESTROY_ON_SHUTDOWN` row, `settings.go:114-118`) and iteration 4's
   `outcomeBadStartCommand` comment are all still open and all still **T016's**. Nothing this
   iteration touched them.

---

## Iteration 7 — 2026-08-07 17:06

**Did:** T007, in commit `db1d57c`. `TestSuggestedPathOutsideRootsRefused` in `actions_test.go`:
a daemon whose `workdir_suggestions` names a real directory outside its roots offers that path
in the rendered `<datalist>` and refuses it on submit with the same response and the same audit
record as a typed path — status, body, `Location`, `action`, `decision`, `reason`. **Test-only;
the refusal already existed.** US2 is complete.

**Learned:**

- **The milestone-4 test it sits beside is not the same test, and the difference is which
  daemon it describes.** `TestChosenPathValidatedIdentically` (378e9a8, FR-042) reaches an
  offered-yet-unacceptable path by setting `c.cfg.Roots` to one directory while the fixture's
  manager stands on another — `server.go:332` builds the manager from `cfg.Roots`, so no
  deployed daemon has that divergence. T005/T006 made the arrangement ordinary: an explicit
  suggestion list is unconstrained by the roots *by contract*, so one configuration produces
  it. The new test therefore sets `c.cfg.Roots = fixture.root` deliberately — making the page
  and the allowlist agree is the point, not an oversight.
- **The default `internal/httpapi` fixture is that divergence.** `testConfig` carries
  `Roots: {testRoot}` = `/nonexistent-crswd-test-root` while `newSessionFixture` builds the
  manager on a real `t.TempDir()`. Since T006 that means **every fleet page in this package
  now renders `<option value="/nonexistent-crswd-test-root">`** — harmless, but it is why a
  test that wants a coherent daemon has to say so, and why an assertion of the form "the
  datalist holds exactly what the allowlist admits" would fail across the suite.
- **Mutation-verified twice, both reverted:** (a) a `refuseBrowserCreate` branch that answers
  an offered path with `outcomeCreateFailed` — the new test fails on **both** halves, the
  `Location` and the record's reason, and so does `TestChosenPathValidatedIdentically`;
  (b) `WorkdirSuggestions` dropped from the union in `suggestions.go` — the new test fails at
  the *render* assertion, which is what keeps it a claim about a **suggested** path rather
  than about any path outside the roots. `TestChosenPathValidatedIdentically` survives (b),
  because its source is the walk.
- **Comparing against a typed control needs a second `newCreator`.** One server would spend
  a second create from the same per-caller budget and would interleave the two requests'
  records; the control is built first so `only(t)` reads its one record before anything else
  is written, exactly as the milestone-4 test does it.
- Linter confirmed v2 before trusting the green: `golangci-lint 2.12.2`, 0 issues. `go vet`
  compiles all three tagged suites; `go.sum` still absent. `go test ./...` green in 5.7s.

**Left:** T008–T016. **T008 is next** and it is the last link in the create-form chain
(`T001 → T003 → T006 → T008`): the `.combo` wrapper, the `<ul class="combo-list">`, the
`role="status"` region — **behaviour unchanged, and no ARIA in the template**. This iteration
touched no template at all, so the ordering is intact. T012–T015 remain independent and are
what to pick up if the chain ever blocks.

**Findings:**

1. **Nothing pins the wiring the new test's argument rests on.** The test proves the *handler*
   does not consult the suggestion list. It cannot see a change to `server.go:332` that fed
   `cfg.SuggestedWorkDirs()` into `session.NewManager`'s roots — which would make every
   suggestion an authorisation, the exact thing FR-009 forbids — because **every fixture in
   `internal/httpapi` injects `fixture.mgr`, built on its own root, rather than letting `New`
   build one from the Config.** The quickstart suite does build a real daemon and does refuse
   `/etc` (`quickstart_test.go:973`), but it configures no `workdir_suggestions`, so that
   mutation survives it too. Not fixed: writing the missing test is not T007's task and AR-008
   is load-bearing. **Worth a line in T016**, or a task in milestone 6 — the assertion wanted
   is "a daemon built by `New` refuses a path that is in its own `SuggestedWorkDirs`".
2. The stale-prose findings from iterations 4–6 are all still open and all still **T016's**:
   `outcome.go`'s `outcomeBadStartCommand` comment, `config.example:157`, `README.md`'s
   `CRSW_DESTROY_ON_SHUTDOWN` row, `settings.go:114-118`, and
   `contracts/directory-suggestions.md:62`'s comma-spelled `allowed_roots`. Iteration 6's
   uncapped-union note is also still open and still unowned.

---

## Iteration 8 — 2026-08-07 17:15

**Did:** T008, in commit `0d0530d`. `create-form.html` wraps the working-directory field in
`<div class="combo" data-combo>` holding the existing `<input>` and `<datalist>`, plus an empty
hidden `<ul class="combo-list" id="workdir-listbox">` and an empty
`<p class="combo-status" role="status" aria-live="polite">`. **No ARIA on the input.** Four
tests in `partials_test.go` (`TestComboRendersWithoutAriaRoles`, `TestComboRendersListAndDatalist`,
`TestComboRendersPlainFieldWithNoSuggestions`, `TestComboStatusRegionIsInTheTemplate`) and three
minimum rules in `crswd.css`. **The create-form chain `T001 → T003 → T006 → T008` is now
complete**, and behaviour is unchanged: every existing picker test passes untouched and the
quickstart acceptance suite ran a real daemon green.

**Learned:**

- **The CSS was not optional, exactly as it was not for T003's switch.**
  `TestTheStylesheetAndTheMarkupNameTheSameThings` sweeps *both* directions off the template
  *source*, so the moment `.combo` appears in the markup the tree is red until a rule exists.
  What shipped is the minimum that sweep demands; T009 owns the presentation. Verified by
  renaming `.combo` to `.combo-unused` in the stylesheet — both directions fail at once.
- **`.combo { display: grid }` is load-bearing and no test can see it.** An `<input>` is
  inline-block: inside a plain block wrapper it falls back to its default character width
  instead of stretching, which is what it does today as a grid item of `.field`. Dropping the
  `display` is a silent visual regression — the one part of this task nothing pins. Do not
  "simplify" it in T009 without replacing it with something that also makes the input a
  stretched item.
- **T010 cannot be written without rewriting `TestSubsetAnnounced`** (`stylesheet_test.go:916`).
  Its addition sweep currently *forbids* `crswd.js` from containing `removeAttribute(`,
  `setAttribute(`, `createElement(`, or the string `datalist` — and T010's task text mandates
  all four (`input.removeAttribute("list")` first, the ARIA attributes, `<li role="option">`
  children, and the `<datalist>` read as the data source). The sweep is not wrong, it is
  scoped to a control that was markup-only; T010 has to re-aim it at the property that still
  holds — *the picker works with the file absent* — rather than at the operations. Budget for
  that; it is most of T010.
- **The old subset region stays, and an existing test requires it.** `TestSubsetAnnounced`
  asserts `<div ... id="create-workdir-subset"></div>` verbatim in the template plus
  `data-workdir-note="create-workdir-subset"` on the field, so `#create-workdir-subset` could
  not have been folded into `.combo-status` here even if T008 had wanted to. The consequence is
  that the field now carries **two `role="status"` regions**: the live one the script writes,
  and the empty one T010 will move the sentence into. Only one ever speaks, so nothing is
  announced twice — but **T010 must delete `#create-workdir-subset` when it moves the sentence**,
  or the form ships with a dead live region for good.
- `.combo-status` and `.combo-list` are unconditional where `list=` and the `<datalist>` are
  conditional on `.Suggestions`. That is the contract's literal block and the distinction is
  real: an attribute pointing at nothing is an offer with nothing behind it (FR-018a), while an
  empty box an enhancement writes into makes no claim either way.
- **Mutation-verified five ways, each reverted:** (a) `role="listbox"`/`aria-expanded` moved
  into the template — `TestComboRendersWithoutAriaRoles` fails on both views; (b) the listbox id
  drifted to `workdir-list` — `TestComboRendersListAndDatalist` fails naming both spellings;
  (c) `.combo-status` left to the script — `TestComboStatusRegionIsInTheTemplate`,
  `TestComboRendersPlainFieldWithNoSuggestions` **and** the stylesheet sweep fail;
  (d) the `<datalist>` emitted unconditionally so an empty one renders —
  `TestComboRendersPlainFieldWithNoSuggestions` fails; (e) the `.combo` rule renamed — see above.
- Linter confirmed v2 before trusting the green: `golangci-lint 2.12.2`, 0 issues. `go vet`
  compiles all three tagged suites; `go test ./...` green; `-tags quickstart` ran uncached (28s)
  and passed with `127.0.0.1:8765` free. No `go.sum`.

**Left:** T009–T016. **T009 is next**: style `.combo`, `.combo-list`, `.combo-list li`,
`.combo-status`, `.switch-input` and `.switch-label` from the token block, with a visible focus
ring on the input, an option and the switch, and a `prefers-reduced-motion` rule. Note two of
its six selectors (`.switch-input`, `.switch-label`) already carry T003's minimum rules and
three carry T008's — T009 is replacing placeholders, not writing on a blank file. It also adds
`TestComboClassesAppearInRenderedMarkup`, which T008 has already made true.

**Findings:**

1. **`.combo` and `.combo-list` are a new component and `docs/components.md` does not document
   them.** That file is the canonical vocabulary and its whole premise is that a class nobody
   documented is how a second card starts; the picker now has a wrapper, a listbox and a status
   region with no entry there. Not fixed: `docs/` is T016's named scope and AR-008 is
   load-bearing this milestone. **T016 owns it**, and it should be an entry rather than a line —
   the ARIA-by-script rule in particular belongs somewhere a future component can find it.
2. **`.switch-label` still duplicates `.field-label` exactly** (flagged in iteration 3, still
   open). T009 is the task that either gives the switch its own presentation or collapses the
   two, and it is the last one that will have a reason to look.
3. The stale-prose findings from iterations 4–7 are all still open and all still **T016's**:
   `outcome.go`'s `outcomeBadStartCommand` comment, `config.example:157`, `README.md`'s
   `CRSW_DESTROY_ON_SHUTDOWN` row, `settings.go:114-118`, and
   `contracts/directory-suggestions.md:62`'s comma-spelled `allowed_roots`. Iteration 6's
   uncapped-union note and iteration 7's missing wiring test are also still open and unowned.

---

## Iteration 9 — 2026-08-07 17:27

**Did:** T009, in commit `01be599`. The picker and the switch are styled from the token
block: `.combo` gains `position: relative`, `.combo-list` is a bounded scrolling popup on
`--surface-lift` behind the field's own `--edge-bright`, `.combo-list li` is the field's own
mono/body/`--text` with a hover on `--ground`, `.combo-list li[aria-selected="true"]` carries
the design system's ring, `.combo-status` is set as `.field-hint` is, and `.switch-label`
stopped being a copy of `.field-label`. One new token, `--combo-h: 14rem`. Five tests in
`stylesheet_test.go`: `TestNoLiteralColourInComboRules`, `TestComboFocusRingSurvives`,
`TestComboDoesNotAnimateUnderReducedMotion`, `TestModeNotConveyedByColourAlone`,
`TestComboClassesAppearInRenderedMarkup`.

**Learned:**

- **The reduced-motion block resets `transition` and nothing else, and that gap is real.**
  `TestReducedMotionStopsEveryTransition`'s universal rule says `transition: none`; an
  `animation:` is a different property and obeys none of it. Verified: adding
  `animation: fade .3s ease` to `.combo-list li` leaves that test **green**. So
  `TestComboDoesNotAnimateUnderReducedMotion` forbids `animation` in a picker rule outright
  rather than trusting the block. **The same hole is open for every other component in the
  file** — nothing stops an animation anywhere else. Not fixed here (AR-008); worth a
  universal `animation: none` in the block, which is a one-line change to a rule this task
  does not own. **T016 or milestone 6.**
- **An option can never be reached by `:focus-visible`,** so the ring on it is a rule rather
  than inheritance: focus stays on the input and `aria-activedescendant` is what moves (T011).
  T009 chose **`[aria-selected="true"]`** as the selector for "the option the keyboard is on"
  — **T011 must set that attribute**, or the ring exists and nothing ever wears it. The
  outline offset is negative on purpose: an outward ring on the first or last option is
  clipped by the scroll box.
- **`position: relative` on `.combo` is load-bearing and nothing pins it**, exactly as
  iteration 8 said of `display: grid`. Drop it and the absolutely positioned listbox resolves
  against the initial containing block — it lands somewhere near the top of the document
  rather than under the field. Both are silent visual regressions no Go test can see.
- **The listbox hangs off the wrapper, not the field**, because the template's order is
  input → `<ul>` → `<p class="combo-status">` and T008's markup is fixed. So when the subset
  sentence is written, it sits *between* the field and the options and the popup drops by one
  line. That reads correctly — field, then what is showing, then the list — but it is a
  consequence of the markup rather than a free choice, and T010 should not "fix" it by
  moving the status region.
- **`.switch-label` is no longer `.field-label`** (the duplication flagged in iterations 3 and
  8 is closed). It is `--fs-label`/`--ls-label` — the design system's *label* role, which is
  what a name beside a control is — and `--text` rather than `--dim`, because with the box
  drawn by the platform those two words are the whole of what says what the tick means.
- **Mutation-verified six ways, each reverted:** (a) `background: #101710` on `.combo-list` —
  `TestNoLiteralColourInComboRules` fails twice, on the hex and on the missing token, and the
  whole-file sweep fails too; (b) the option's `outline` dropped for a background-only cue —
  only the new test fails, which is the half `TestTheFocusRingSurvives` cannot see;
  (c) `outline: none` on `.combo-list li`; (d) `animation:` on the same rule — see above;
  (e) `appearance: none` + a fill on `.switch-input`, and (f) a `.switch-input:checked` rule
  whose only declaration is a colour — both fail `TestModeNotConveyedByColourAlone`;
  (g) `.combo-status` renamed to `.combo-note` — the new test and the both-directions sweep
  fail together.
- Linter confirmed v2 before trusting the green: `golangci-lint 2.12.2`, 0 issues.
  `go test ./...` green; `go vet` compiles all three tagged suites. **`-tags quickstart` was
  not run: `127.0.0.1:8765` is held by the deployed daemon right now** (it was free in
  iteration 8). This task touches no `cmd/crswd` code, and `go vet -tags quickstart ./...`
  passes. No `go.sum`.

**Left:** T010–T016. **T010 is next**, and iteration 8's warning stands: it must rewrite
`TestSubsetAnnounced`'s addition sweep (`stylesheet_test.go:916`), which forbids the exact
four operations T010 mandates, and it must **delete `#create-workdir-subset`** when it moves
the sentence into `.combo-status`, or the field keeps two live regions with one dead. Add to
that: T010 sets `aria-expanded` and `role="option"`, and **T011 sets `aria-selected="true"`**
on the active option, which is the selector the ring T009 shipped is keyed on.

**Findings:**

1. **Nothing in the file stops an animation under `prefers-reduced-motion`** — see the first
   learning. The picker is now covered by a test of its own; every other component is not.
   **T016 or milestone 6**, as a universal `animation: none` beside the existing
   `transition: none`.
2. `docs/components.md` still documents no `.combo`, `.combo-list`, `.combo-status` or
   `.switch-*` entry (iteration 8's finding 1, still open, still **T016's**). T009 makes it
   more pressing, not less: there is now a listbox, an option, an active-option ring keyed on
   an ARIA attribute, and a switch whose label is deliberately *not* a field label — four
   decisions a future component will otherwise re-invent.
3. The stale-prose findings from iterations 4–7 are all still open and all still **T016's**:
   `outcome.go`'s `outcomeBadStartCommand` comment, `config.example:157`, `README.md`'s
   `CRSW_DESTROY_ON_SHUTDOWN` row, `settings.go:114-118`, and
   `contracts/directory-suggestions.md:62`'s comma-spelled `allowed_roots`. Iteration 6's
   uncapped-union note and iteration 7's missing wiring test are also still open and unowned.

---

## Iteration 10 — 2026-08-07 17:41

**Did:** T010, in commit `ab560e4`. `crswd.js` now enhances the working-directory field:
guarded on `[data-combo]`, it reads `field.list` **first**, cuts the `list` attribute, adds
`role="combobox"`/`aria-expanded`/`aria-autocomplete`/`aria-controls` to the input and
`role="listbox"` to the `<ul>`, and on every keystroke draws the matching options as
`<li role="option">` with `textContent`. FR-045's sentence moved out of
`#create-workdir-subset` — **deleted, as iterations 8 and 9 required** — onto
`.combo-status` as `data-workdir-subset`, so the field carries one live region rather than
two. `TestSubsetAnnounced` rewritten and `TestTheThemedPickerEnhancesTheNativeOne` added in
`stylesheet_test.go`. The five T008/T009 tests pass untouched.

**Learned:**

- **`field.list` is null the instant `removeAttribute("list")` runs**, which is why the
  contract puts the read first and why the order is asserted positionally rather than by
  mention. Get it backwards and every test above it still passes: the picker announces
  "showing 0 of 0", the themed box is empty for good, and nothing in Go can see it. That
  positional assertion is the one this task most needed.
- **The old addition sweep could not be kept and could not simply be dropped.** It forbade
  `datalist`, `createElement(`, `setAttribute(` and `.value =` — the exact four operations
  T010 mandates — because it was written about a control that was markup-only. What it was
  protecting is that the picker is still the daemon's with this file absent, so that is what
  the new sweep asserts: no second handle on the options (the `datalist` literal and
  `new Option(` stay forbidden, the list is reached only through `field.list`), no id spelled
  here that the template owns, and the markup half re-checked. **`.value =` is deliberately no
  longer forbidden** — T011's Enter has to assign it — and FR-008/FR-040 are held instead by
  `TestAnyPathStillTypeable`, which reads the markup and is where that claim belongs.
- **A regex cannot name the variable holding the datalist, so the removal is counted.**
  `offered.remove()` after copying the options into an array is the exact must-fail this task
  was given, it reads as tidying, and `\.list\.remove\(` misses it. `strings.Count(source,
  ".remove()") != 1` catches it — one removal in the file, the fleet's departed card.
- **`aria-controls` is set from `listbox.id`, not from the literal.** The script now carries no
  id the template owns, and the sweep forbids `workdir-listbox`, `create-work-dir` and
  `workdir-suggestions` outright — the principle the old test stated about the subset note,
  applied to the two joints T010 adds.
- **The debounce is on the sentence only.** The list is drawn on the keystroke; only
  `.combo-status` waits `SETTLE_MS`. A list lagging typing by 400ms feels broken to the
  operator it is fastest for, while a polite region written per keystroke hands a reader a
  backlog of counts already wrong when spoken.
- **The enhancement bails when there is no `<datalist>`**, so a daemon with nothing to suggest
  keeps the plain field and gains no roles. That is FR-018a rather than defensiveness: a
  combobox over no options announces a control with nothing behind it.
- **Mutation-verified six ways, each reverted:** (a) the read and the cut swapped — the
  positional assertion fails naming both offsets; (b) the options copied into an array and
  `offered.remove()` — the removal count fails; (c) `aria-controls` spelled
  `'workdir-listbox'` — the `.id` assertion **and** the id sweep fail together; (d)
  `role="option"` dropped from the built `<li>` — the ARIA table fails; (e)
  `#create-workdir-subset` restored in the template — both new region assertions fail;
  (f) the datalist reached by `combo.querySelector('datalist')`, which *works* at runtime —
  the `datalist` sweep and the `field.list` assertion fail.
- Linter confirmed v2 before trusting the green: `golangci-lint 2.12.2`, 0 issues.
  `go test ./...` green; `gofmt -l` clean; `go vet` compiles all three tagged suites. No
  `go.sum`. **`-tags quickstart` was not run: `127.0.0.1:8765` is held by the deployed daemon**
  (as in iteration 9). This task touches no `cmd/crswd` code and the quickstart suite names
  nothing in the picker.

**Left:** T011–T016. **T011 is next.** Three things it inherits, in the order they bite:

1. **It must set `aria-selected="true"`** on the active option — T009's ring is keyed on that
   selector and nothing wears it yet.
2. **The options carry no ids.** `aria-activedescendant` names an element by id, so T011 has
   to give each `<li>` one as it is built (in `draw()`), which is its own requirement rather
   than something T010 left half-done.
3. **The listbox has no close path.** T010 opens it whenever something matches and closes it
   only when nothing does; Escape, Tab and Enter are all T011's, and until they exist the list
   stays open once typed into.

**Findings:**

1. **Nothing selects an option with the pointer, and no task owns that.** T010's scope is
   suppress/ARIA/filter/announce and T011's is the keyboard; neither mentions a click. So the
   themed listbox is display-only today, and `.combo-list li` already carries
   `cursor: pointer` (T009) — an affordance that does nothing. The native popup this replaces
   *was* clickable, so this is the one place the enhancement currently costs behaviour rather
   than adding to it. Not fixed: it is outside both task texts and AR-008 is load-bearing.
   **T011 should take it** (its Enter already has to assign the value, so the accept path
   exists) — otherwise milestone 6. Note the ordering trap when it does: a `blur` handler that
   closes the list fires before a `mousedown` on an option lands, which is the classic version
   of this bug.
2. Iteration 9's reduced-motion hole is still open and still **T016's or milestone 6's**:
   nothing in `crswd.css` stops an `animation` under `prefers-reduced-motion`, only a
   `transition`. T010 adds no animation, and `TestComboDoesNotAnimateUnderReducedMotion`
   forbids the property in picker rules outright, so the picker is covered and every other
   component is not.
3. `docs/components.md` still documents no `.combo`, `.combo-list`, `.combo-status` or
   `.switch-*` entry (iterations 8 and 9, still open, still **T016's**). T010 adds the part
   most worth writing down: **which ARIA the script adds and why none of it is in the
   template**, plus the read-then-cut order. A future themed control over a native one will
   otherwise re-derive both.
4. The stale-prose findings from iterations 4–7 are all still open and all still **T016's**:
   `outcome.go`'s `outcomeBadStartCommand` comment, `config.example:157`, `README.md`'s
   `CRSW_DESTROY_ON_SHUTDOWN` row, `settings.go:114-118`, and
   `contracts/directory-suggestions.md:62`'s comma-spelled `allowed_roots`. Iteration 6's
   uncapped-union note and iteration 7's missing wiring test are also still open and unowned.

---

## Iteration 11 — 2026-08-07 17:51

**Did:** T011, in commit `ce36a79`. The picker's listbox is now operable: `↓`/`↑` move an
active option (wrapping, reopening a list Escape closed), `Enter` accepts it into the field
and closes, `Escape` and `Tab` close and leave what was typed alone. `draw()` gives every
`<li>` an id built from `listbox.id` and clears the active one on every rebuild; `activate()`
sets `aria-selected="true"` — the selector T009's ring was keyed on and nothing wore — plus
`aria-activedescendant` on the field, and scrolls the option into the bounded list.
`TestComboKeyboardOperable` in `stylesheet_test.go`.

**Learned:**

- **The floor FR-008 needs is the field's own value, counted.** "Typing is never intercepted"
  cannot be asserted directly in Go, but every way of breaking it writes `field.value` — an
  inline completion on input, an Escape that reverts, a blur that normalises. So the test
  counts `.value =` across the whole file: **exactly one**, it reads an option's own
  `textContent`, and it sits after the `'Enter'` literal. That single count is what caught the
  must-fail mutation, and it is worth keeping whole-file rather than block-scoped.
- **Most other claims had to be scoped to the picker's block**, because the words they turn on
  are ordinary: `hidden = true` is what the toast does when it expires, and `preventDefault`
  is called by the toast and by the card's selection fix. The block is
  `source[index("SETTLE_MS"):index("data-combo")]` — the picker's one constant to the query
  that applies it. Both markers are asserted before the slice is taken.
- **A whole-block "aria-activedescendant is cleared" assertion is satisfied by the close path
  and misses the redraw.** Verified: deleting the clear in `draw()` left the test green,
  because `activate(-1)` clears it too. The ids are **positional**
  (`${listbox.id}-option-${index}`), so a stale attribute does not dangle — it names whichever
  path now sits in that position, announced as active while the ring is on nothing. The
  assertion is now positional, between `replaceChildren` and the first `addEventListener`.
- **Enter with nothing active is deliberately not touched.** It is the submit this form has
  always had, so a path typed in full is sent by the same key whether or not the script ran.
  Only the accept is claimed, and only when there is something to accept.
- **`Tab` is never `preventDefault`ed** and the test slices from `'Tab'` to the end of the
  block to say so — which holds because Tab is last in the branch order, as it is last in the
  contract's own table. A swallowed Tab is focus trapped in a text field.
- **`close()` also clears `.combo-status` and the pending settle timer.** The close path is
  new with this task, and without that a count written 400ms later describes a list that is no
  longer on screen. It is the close being honest rather than new prose — the sentence itself
  is still the template's.
- **Mutation-verified six ways, each reverted:** (a) `preventDefault()` added to the Tab
  branch — the Tab slice fails; (b) an inline completion writing the single match into the
  field on input — the `.value =` count fails, which is this task's named must-fail;
  (c) `aria-selected` swapped for a class — the ARIA assertion fails and the ring is worn by
  nothing; (d) the option id replaced with `dataset.at` — the id assertion fails; (e) the
  accept assembled from `matching()[active]` rather than the option's text — the `textContent`
  assertion fails; (f) the clear dropped from `draw()` — **green until the assertion was made
  positional**, see above.
- Linter confirmed v2 before trusting the green: `golangci-lint 2.12.2`, 0 issues.
  `go test ./...` green; `gofmt -l` clean; `go vet` compiles all three tagged suites. No
  `go.sum`. **`-tags quickstart` was not run: `127.0.0.1:8765` is held by the deployed daemon**
  (as in iterations 9 and 10). This task touches no `cmd/crswd` code and no Go outside one
  test file.

**Left:** T012–T016. **T012 is next** and is independent of everything above: the settings
link in `web/templates/partials/header.html`, inside `.masthead-bar`, after `<p class="operator">`
and outside `<h1 class="brand">`, with six tests in `partials_test.go` — including
`TestSettingsStillHasNoMutatingVerb`, which is the security half: reachability is not
permission to add editing.

**Findings:**

1. **Nothing selects an option with the pointer, and it is now the picker's one remaining
   hole** (iteration 10's finding 1, still open). T011's scope is the keyboard and the task
   text names four keys; a click handler is outside it and AR-008 is load-bearing, so it was
   not added. The accept path now exists (`activate` + the assignment in `Enter`), so the fix
   is small: a `mousedown` on an option — **mousedown, not click**, since a blur-close would
   otherwise fire first — that activates it and runs the same accept. `.combo-list li` still
   carries `cursor: pointer` from T009, so the affordance is drawn and does nothing.
   **Milestone 6, or T016 if it is willing to touch behaviour.**
2. **The list has no blur close.** Tab closes it, but a pointer click elsewhere on the page
   leaves it open over the form. Same owner as finding 1 and the same ordering trap — the two
   should be written together or the blur will eat the click that selects.
3. Iteration 9's reduced-motion hole is still open and still **T016's or milestone 6's**:
   nothing in `crswd.css` stops an `animation` under `prefers-reduced-motion`, only a
   `transition`.
4. `docs/components.md` still documents no `.combo`, `.combo-list`, `.combo-status` or
   `.switch-*` entry (iterations 8–10, still open, still **T016's**). T011 adds the last piece
   worth writing down: **the active option is `aria-selected="true"` and the field keeps
   focus**, so the ring is a rule rather than inheritance, and the ids the listbox builds are
   positional and cleared on every redraw.
5. The stale-prose findings from iterations 4–7 are all still open and all still **T016's**:
   `outcome.go`'s `outcomeBadStartCommand` comment, `config.example:157`, `README.md`'s
   `CRSW_DESTROY_ON_SHUTDOWN` row, `settings.go:114-118`, and
   `contracts/directory-suggestions.md:62`'s comma-spelled `allowed_roots`. Iteration 6's
   uncapped-union note and iteration 7's missing wiring test are also still open and unowned.

---

## Iteration 12 — 2026-08-07 18:04

**Did:** T012, in commit `16ee01d`. `web/templates/partials/header.html` now renders
`<a class="masthead-link" href="/settings">Settings</a>` inside `.masthead-bar`, after the
operator and outside the `<h1 class="brand">`; `crswd.css` gained the `.masthead-link` rule
and its hover. Seven tests in `partials_test.go`: the six the task named plus
`TestSettingsLinkHasVisibleFocusRing`, which is in `contracts/settings-link.md` and not in
`tasks.md` — the contract supersedes, and the plan says so.

**Learned:**

- **`.masthead-bar` is `justify-content: space-between`, written for two children.** A third
  puts the *operator* in the centre, which quietly breaks what `docs/components.md` says the
  header is ("identity right"). `margin-inline-start: auto` on `.operator` puts the free space
  in front of the pair instead. That is a second rule edited outside the task's named files
  and it is deliberate: without it this task changes the documented layout, which no test in
  this repo can see. AR-008 forbids tidying, not the change the task requires.
- **A rendered class with no rule is red**, as in T008/T009 — `TestTheStylesheetAndTheMarkupNameTheSameThings`
  sweeps both directions, so the CSS was never optional. Verified: deleting the link from the
  template fails that sweep too, because the rule is then styling nothing.
- **`TestSettingsStillHasNoMutatingVerb` is deliberately not a copy of `TestNoMutatingVerbRegistered`**
  (`settings_test.go:213`), which already holds the same claim against `settingsPath`. The new
  one reads the `href` **out of the rendered header** and asks the four verbs at whatever it
  points to, so it is the pairing this task creates — the page is one click away now — rather
  than the route table asserted twice. It compares against a path nothing claims for the
  existing test's reason: a 405 is a route table handed to whoever asks.
- **`renderedPages` is checked against the template tree rather than trusted.** The map is
  keyed by template name and every `templates/*.html` must appear in it, so a page added later
  cannot ship with an unasserted header. There is no shared layout here — each page composes
  the partial itself, which is exactly how one falls behind (the same shape as `#77`, which
  `TestEveryActionablePageCarriesTheLiveRegion` was written for).
- **The not-found page carries the header too** and is now in that sweep. It was already
  rendering the partial; nothing was needed beyond listing it.
- **Mutation-verified six ways, each reverted:** (a) the anchor moved inside the `<h1>` — only
  `TestSettingsLinkIsOutsideTheBrandHeading` fails, both halves of it, which is the contract's
  named must-fail; (b) the anchor placed before the wordmark — `TestWordmarkIsStillTheFirstAnchor`
  fails; (c) the anchor deleted — four tests plus the stylesheet sweep fail; (d) `settings.html`
  composing its own masthead without the link — `TestEveryPageCarriesTheHeader` fails naming
  the page; (e) `POST /settings` registered on the same handler — `TestSettingsStillHasNoMutatingVerb`
  fails with the 200 it answered; (f) `outline: none` on `.masthead-link` — the focus-ring test
  fails. A seventh: a new page template added to `web/templates/` and not to `renderedPages`
  fails the staleness guard by name.
- Linter confirmed v2 before trusting the green: `golangci-lint 2.12.2`, 0 issues.
  `go test ./...` green (`-count=1` on `internal/httpapi`); `gofmt -l` clean; `go vet` compiles
  all three tagged suites. No `go.sum`. **`-tags quickstart` was not run: `127.0.0.1:8765` is
  held by the deployed daemon** (as in iterations 9–11). This task touches no `cmd/crswd` code,
  but note that quickstart drives *real pages* — the next iteration that finds the port free
  should run it, because T012 changed markup every page renders.

**Left:** T013–T016. **T013 is next** and is independent: diagnostics to stderr, audit records
to stdout across `internal/httpapi/server.go`, `internal/config/depcheck.go` and
`cmd/crswd/main.go`, establishing "every line on stdout is a record". T015 depends on it in
substance — the documented `grep '^{'` filter only works once T013 lands — and T015's test is
`-tags quickstart`, so **T015 needs `127.0.0.1:8765` free**; if it is still held, T014 and T016
are the ones that can run.

**Findings:**

1. **The settings link is the second anchor on every page and nothing counts anchors
   per page.** `TestHeaderHasExactlyTwoAnchors` is about the header component, and
   `TestCardHasExactlyOneAnchor` is about a card; the fleet page as a whole now carries
   two-plus-one-per-card, which is correct and unasserted. Not a defect — noting it because
   the "one link per card" rule and the header's two are easy to conflate, and a future test
   counting anchors page-wide would make them contradict each other. The existing comment on
   `mastheadElement` already says this; it is now true of a second link as well.
2. **Nothing selects a picker option with the pointer** (iterations 10 and 11, still open,
   still **milestone 6's or T016's if it will touch behaviour**), and **the list has no blur
   close**. The two must be written together — `mousedown`, not `click`, or the blur eats the
   selection.
3. Iteration 9's reduced-motion hole is still open and still **T016's or milestone 6's**:
   nothing in `crswd.css` stops an `animation` under `prefers-reduced-motion`, only a
   `transition`. T012 adds two transitions and no animation, both under the universal reset.
4. `docs/components.md` still documents no `.combo`, `.combo-list`, `.combo-status` or
   `.switch-*` entry (iterations 8–11, still open, still **T016's**). T012 adds one more line
   for it: the Header row says "product identity left, operator identity right" and there is
   now a `.masthead-link` after the identity — **and the `.operator` auto margin is what holds
   that sentence true**, which is worth writing down beside it.
5. The stale-prose findings from iterations 4–7 are all still open and all still **T016's**:
   `outcome.go`'s `outcomeBadStartCommand` comment, `config.example:157`, `README.md`'s
   `CRSW_DESTROY_ON_SHUTDOWN` row, `settings.go:114-118`, and
   `contracts/directory-suggestions.md:62`'s comma-spelled `allowed_roots`. Iteration 6's
   uncapped-union note and iteration 7's missing wiring test are also still open and unowned.
6. **#95 still has not received T002's SHA** (`ef18756`), because `gh` is not an approved
   command in the loop's session. Unchanged since iteration 2 and still needs a human.

---

## Iteration 13 — 2026-08-07 18:22

**Did:** T013, in commit `0c80428`. The invariant "every line on stdout is an audit record"
is now stated in `cmd/crswd/main.go` where both streams are chosen, and held by three tests:
`TestAuditRecordsGoToStdout` (`internal/httpapi/server_test.go`), `TestDiagnosticsGoToStderr`
and `TestStartupDiagnosticsGoToStderr` (new `cmd/crswd/main_test.go`), and
`TestNoSecretInAnyDiagnostic` (`internal/config/depcheck_test.go`). No stream was re-routed,
because none needed to be — see below.

**Learned:**

- **The shipped daemon was already writing to the right two streams.** Read every sink before
  changing anything: `audit.New()` takes `os.Stdout` (audit.go:216); `main.go` hands
  `os.Stderr` to `CheckDependencies`; `config.Load` hands `os.Stderr` to `LoadFrom`;
  `reportToStderr` (httpapi) and `reportToLog` (session) both go through the standard logger,
  whose default is `os.Stderr` and which nothing in this repo moves. **Nothing in the module
  writes a diagnostic to stdout.** The plan's resolved decision — "the daemon's own
  diagnostics share stdout with its records" — is imprecise about the mechanism, but its
  *conclusion* is untouched and `contracts/diagnostics-and-probe.md` already states the real
  one: **systemd merges both fds into one journal**, which is why the contract says "document
  the filter anyway". So this was not a `NEEDS CLARIFICATION`: the contract and the fix both
  survive the correction, only the one-line summary in the plan's table does not.
- **Therefore the defect T013 actually closes is the absence of the rule, not a misroute.**
  Every sink was right by accident and nothing anywhere said which stream it belonged on. One
  `fmt.Println` added later costs an audit record from the documented reader, silently, and
  the daemon that shipped it looks identical to the one that did not. That is the shape this
  milestone exists for, arriving from the other direction: not "the test read the wrong
  layer", but "there was no test at all and the code happened to be right".
- **`New` vs `newServer` is the whole point of the httpapi test.** `newTestServer`'s own
  comment (server_test.go:35) says every fixture goes through `newServer` *specifically so
  that records do not land on the test binary's stdout* — so the production constructor's
  choice of sink had no caller-side test anywhere. `TestAuditRecordsGoToStdout` is the only
  test in that package that goes through `New` and drives a request. **Order is load-bearing
  and asserted by construction**: `audit.New()` reads `os.Stdout` at the moment it is called,
  so the pipe swap must happen *before* `New`, or the test reads an empty pipe while the
  records go to the terminal.
- **Swapping `os.Stderr` does not redirect `log.Printf`.** Package `log`'s default logger
  captured the `*os.File` at its own init, so it holds the original no matter what
  `os.Stderr` is later set to. `log.SetOutput` is the only seam — which is what
  `internal/audit/leak_test.go:593` already uses, and why `reportToStderr` must keep going
  through `log` rather than writing to `os.Stderr` directly: that leak suite depends on it.
- **Restore the swapped stdout in `t.Cleanup`, not at the end of the body.** A `t.Fatalf`
  while the pipe is installed would otherwise print into the pipe. Also: `strings.Split("")`
  is one line, not zero, so "nothing reached stdout" needs its own check ahead of the count
  or the failure message says "wrote 1 lines" and shows nothing.
- **The module-wide sweep is an AST walk, following `TestNeverExecutesInstall`** (depcheck_test.go:473)
  and `bypass_build_test.go` — both already assert structure rather than behaviour for
  exactly this reason: the thing forbidden is one nobody writes a test for. Two exemptions,
  and they are different in kind: `internal/audit/audit.go` by **file** (writing the trail is
  all it does), and `runConfigCommand`'s arguments by **call** — naming the call rather than
  `main.go` stops an ordinary print added to `main.go` later from inheriting the exemption.
  The walk fails loudly if it does not find both, so a wrong root or a renamed file cannot
  read as "no violations".
- **Mutation-verified five ways, each reverted:** (a) `reportToStderr` also printing to
  stdout — `TestAuditRecordsGoToStdout` fails, naming the interleaved line, which is this
  task's named must-fail; (b) `New` building `audit.NewTo(os.Stderr, …)` — same test fails
  on the empty stream; (c) `reportToLog` in `internal/session` printing to stdout —
  `TestDiagnosticsGoToStderr` fails with `reaper.go:305`; (d) `CheckDependencies(os.Stdout)`
  in main.go — both `cmd/crswd` tests fail; (e) `warnStartCommandNotOnPath` handed `command`
  instead of `binary` — `TestNoSecretInAnyDiagnostic` fails with the credential in the
  banner. **(e) caught a flaw in my own test first**: the "the warning happened at all"
  guard was `"frobnicate"` *with quotes*, so the verbatim mutation tripped that `t.Fatalf`
  instead of the leak assertion. The guard is now unquoted on purpose, so the sweep is what
  reports the leak.
- Linter confirmed v2 before trusting the green: `golangci-lint 2.12.2`, 0 issues.
  `go build`, `go vet`, `go test ./...` green; `gofmt -l` clean; `go vet` compiles all three
  tagged suites. No `go.sum`.

**Left:** T014, T015, T016. **T014 is next** and is the security one: resolve the start
command through a login shell in `internal/config/depcheck.go`, four tests in
`depcheck_test.go`, and the tmux probe stays fatal and untouched.

**Findings:**

1. **`-tags quickstart` is no longer blocked by the port, and iterations 9–12's stated reason
   is now stale.** Verified this iteration: `127.0.0.1:8765` *is* held by the deployed daemon
   (`ss -ltn`), **but the suite stopped binding it** — `freeAddrOn` (quickstart_test.go:437)
   was written for exactly that, and no test in `cmd/crswd` names 8765 outside a comment. The
   remaining reason not to run it here is that it starts real tmux servers and real sessions
   on the host running the live deployment, and this iteration's `cmd/crswd` change is
   comments plus one untagged test that only parses source. **T015 needs quickstart and should
   run it**; `go vet -tags quickstart ./...` is AGENTS.md's named fallback and was run.
2. **`journalctl -p` may be a second way to separate the two streams, and T015 should not
   adopt it.** systemd assigns priority `info` to a unit's stdout and `err` to its stderr,
   so `journalctl --user -u crswd -p 6..6 -o cat` would in principle filter to records
   alone. It is not the contract's answer, it depends on a systemd behaviour the daemon does
   not control, and it silently returns nothing if `StandardOutput=` is ever changed. Noting
   it so the next iteration does not rediscover it and think it is an improvement — the
   contract's grep filter is the one T015 must document.
3. **`Config.String()` redacts the shared secret and nothing else** (config.go:461). It
   prints `start_commands:%v`, and a start command line is configuration whose arguments an
   operator may fill with a credential. Nothing formats a whole Config into a diagnostic
   today — `TestNoSecretInAnyDiagnostic` now pins that for this package's two messages — but
   the redaction is narrower than its doc comment's "cannot leak it" suggests. **T016 or
   milestone 6**, and it is a docs-or-redaction decision rather than a bug.
4. **Nothing selects a picker option with the pointer** (iterations 10–12, still open), and
   **the list has no blur close**. **T017 now owns both** and the plan lists it after T015 —
   the two must be written together, `mousedown` not `click`, or the blur eats the selection.
5. Iteration 9's reduced-motion hole is still open and still **T016's or milestone 6's**:
   nothing in `crswd.css` stops an `animation` under `prefers-reduced-motion`, only a
   `transition`.
6. `docs/components.md` still documents no `.combo`, `.combo-list`, `.combo-status` or
   `.switch-*` entry (iterations 8–12, still open, still **T016's**).
7. The stale-prose findings from iterations 4–7 are all still open and all still **T016's**:
   `outcome.go`'s `outcomeBadStartCommand` comment, `config.example:157`, `README.md`'s
   `CRSW_DESTROY_ON_SHUTDOWN` row, `settings.go:114-118`, and
   `contracts/directory-suggestions.md:62`'s comma-spelled `allowed_roots`. **T016 has one
   more now**: the plan's resolved-decisions table says the daemon's diagnostics share stdout
   with its records, and they do not — see this iteration's second bullet. Iteration 6's
   uncapped-union note and iteration 7's missing wiring test are also still open and unowned.
8. **#95 still has not received T002's SHA** (`ef18756`), because `gh` is not an approved
   command in the loop's session. Unchanged since iteration 2 and still needs a human.

---

## Iteration 14 — 2026-08-07 18:39

**Did:** T014, in commit `ca1feb9`. The start-command probe now resolves a binary the way a
session will — through a login shell — and where it cannot, it says what it checked instead
of claiming the command is absent. Six new tests in `internal/config/depcheck_test.go`
(`TestProbeResolvesThroughLoginShell`, `TestGenuinelyMissingCommandStillWarns`,
`TestProbeNamesWhatItChecked`, `TestMissingTmuxStillFatal`, `TestTheProbeReallyAsksALoginShell`,
`TestTheLoginShellIsAskedNothingAboutTheCommand`), plus a third case on
`TestNoSecretInAnyDiagnostic` and a rewritten `TestNeverExecutesInstall`. Closes #96.

**Learned:**

- **The probe asks the shell for its PATH; it never names the command to the shell.**
  `sh -lc "command -v $binary"` would resolve identically and is a **shell string built from
  configuration**, which `docs/security.md` §2 forbids by name ("No `sh -c`"). The plan's
  complexity table authorises *executing the operator's profile*, not building a command line
  — those are two different permissions and only the first was granted. So `loginShellPATH`
  runs `$SHELL -l` with a **constant** script (`printf '%s\n' "$PATH"`) on **stdin**, and the
  name is resolved in Go by `lookInPATH` against the list that comes back. Two tests keep that
  from drifting back: `TestNeverExecutesInstall` now requires every `exec.CommandContext`
  argument past the program to be a source literal, and
  `TestTheLoginShellIsAskedNothingAboutTheCommand` requires `loginShellPATH` to take **no
  parameters** — a probe that cannot be told what to look for cannot be told to look for it in
  a command line.
- **`TestNeverExecutesInstall` had to change and its guarantee is intact.** It asserted that
  `exec.LookPath` was the *only* member of `os/exec` this package reaches, which forbids the
  fix outright. It now allows `CommandContext` as well, and pays for it with two claims the old
  version did not make: the argv is literal (above), and this package starts **exactly one**
  subprocess. FR-014 — never install anything, never run a probed binary — is untouched: the
  one program started is the operator's own shell and `printf` is a builtin.
- **`cmd.Stderr = io.Discard` is a disclosure rule, not tidiness.** With `Stderr` left nil,
  `cmd.Output()` folds the child's stderr into the returned `*exec.ExitError`, and this daemon
  prints that error into a journal that outlives the process (FR-043). A `.profile` is free to
  print anything. The note quotes the *exec* error and never the shell's own output.
- **Three outcomes, not two, and the third is the requirement.** Found on the daemon's PATH →
  silent, as before. Not there, login shell finds it → **silent** (the #96 case: nothing is
  wrong). Not there, login shell could not be asked → a **note** naming what was checked.
  Neither there → the old warning, now carrying `checked: this daemon's own PATH, and the PATH
  a login shell gives a session`. The exact sentences differ slightly from the illustrative
  ones in `contracts/diagnostics-and-probe.md` §Part 2; the contract's four named tests all
  exist and pass. **T016 should document the note**, because an operator meeting it in a
  journal has never been told this daemon has two environments to be wrong about.
- **The shell is asked once per start, not once per command** (`sessionPATH` memoises the
  failure too), and **only after this daemon's own PATH has already missed** — a host where
  everything is present never runs the operator's profile. `TestProbesFirstWordOnly` asserts
  the zero, `TestProbeResolvesThroughLoginShell` asserts the one.
- **Which shell is a guess this daemon cannot make better.** tmux takes `default-shell` from
  `$SHELL` and falls back to the passwd entry, which pure Go cannot read (`os/user` exposes no
  Shell field). The probe reads `$SHELL`, falls back to `/bin/sh`, and the messages say what
  was checked — which is exactly why FR-023c exists. A `.profile` full of bashisms read by
  `dash` is the same story: the shell exits, the probe returns an error, and the operator gets
  the note rather than a false absence.
- **Mutation-verified five ways, each reverted:** (a) ignoring the login shell's answer —
  `TestProbeResolvesThroughLoginShell` fails with both warnings quoted, which is #96 verbatim;
  (b) dropping `-l` — `TestTheProbeReallyAsksALoginShell` fails, and its failure prints the
  non-login PATH, which is the only way to see that an ordinary shell never reads `~/.profile`;
  (c) the unanswerable branch calling the warning instead of the note —
  `TestProbeNamesWhatItChecked` fails on four claims including "says `will fail`, which is a
  claim about the command"; (d) the tmux probe consulting the login shell —
  `TestMissingTmuxStillFatal`'s second case fails; (e) a non-literal argument to the shell —
  `TestNeverExecutesInstall` fails naming the position.
- **The fixture describes two environments now.** `newHostTools` takes `*testing.T` and starts
  with a real empty `t.TempDir()` as the login shell's PATH, so "the shell was asked and found
  nothing" is the default and every existing case kept its meaning. `loginShellFinds` writes a
  **real executable** rather than answering a lookup, because the resolution against a
  directory list is production code and a fake that answered "installed" would skip it.

**Left:** T015 (the documented audit-trail command, needs `-tags quickstart`), T017 (pointer
selection and blur close), T016 (docs).

**Ad-hoc findings, not fixed:**

1. **The live daemon's new behaviour was not measured, only the mechanism.** `env -i … /bin/sh -l`
   is not an approved command in the loop's session, so whether the deployed unit now goes
   silent about `rc` is unverified on the host. `TestTheProbeReallyAsksALoginShell` proves a
   login shell is asked and that a `~/.profile` addition reaches the answer; the remaining
   uncertainty is only which shell systemd's environment names. **A human restarting
   `crswd.service` closes this in one line of journal.**
2. **The daemon now runs the operator's profile at startup, before anything binds.** Bounded
   by a 5s timeout and a 1s `WaitDelay`, and only on a host where a command is already missing
   from this daemon's PATH — but it is a new startup dependency on a file the daemon does not
   own. The plan's complexity table took this trade deliberately (`plan.md:138`); recording it
   here because nothing in `deploy/` or `README.md` mentions it. **T016 or milestone 6.**
3. Findings 2–8 of iteration 13 are all still open and unchanged: `journalctl -p` is not
   T015's answer; `Config.String()` prints `start_commands` unredacted (**T016**); the picker
   has no pointer selection and no blur close (**T017**); the reduced-motion rule resets
   `transition` and not `animation` (**T016** or milestone 6); `docs/components.md` documents
   no `.combo*` or `.switch-*` entry (**T016**); the stale-prose list from iterations 4–7
   (**T016**); and **#95 still has not received T002's SHA `ef18756`**, because `gh` is not an
   approved command in the loop's session — unchanged since iteration 2, still needs a human.
