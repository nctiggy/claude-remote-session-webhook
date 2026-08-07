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
