# Contract: Removing the conversation field

**Files**: `web/templates/partials/create-form.html`, `internal/httpapi/view.go`, `internal/httpapi/dashboard.go`, `internal/session/conversation.go` (deleted)
**Tests**: `internal/httpapi/partials_test.go`, `internal/httpapi/actions_test.go`
**Satisfies**: FR-020, FR-021, FR-022

---

## What is removed

The create form's resume field:

```html
<input class="field-input" id="create-resume" type="text" name="resume"
       {{ if .Conversations }}list="conversation-suggestions"{{ end }}>
<datalist id="conversation-suggestions">…</datalist>
```

The operator's report: *"I have no idea what the conversation section is for
now.... I think it is to resume a conversation but it is free text... I have no
idea how to understand that."*

That is a fair reading of a free-text box whose valid values are opaque
identifiers, offered through a list that is empty unless the store happens to be
populated for that exact directory. It asks the operator to know something they
have no way to know.

## What is deleted with it

`internal/session/conversation.go` and its test.

Removing the field leaves the listing with **no caller**. This repository has
shipped code with no caller four times — a reaper, `Store.Touch`, a PR-opener, and
`CRSW_DESTROY_ON_SHUTDOWN`, which was false on every daemon that ever ran. Keeping
this "because #95 will want it" is how the fifth happens.

It also probably will not fit. #95's problem is *"resume **this session's** own
conversation after a crash."* `listConversations` answers *"what conversations
exist for **this directory**."* Those differ in exactly the way that matters: the
second cannot tell one session's conversation from another's, which is the
ambiguity FR-032 refuses to resolve by guessing.

**Deleting is not losing.** The careful parts — the root check before the lookup,
the listing that opens no file, the symlink exclusion, and the separator-flattening
that makes traversal structurally impossible — are in git. The task must record
the deleting commit's SHA in issue #95 so it can be read back.

## What must remain true

- Starting fresh is what a create does, which is already the default (FR-021)
- A request still carrying `resume=` is **ignored or refused, never executed**
  (FR-022) — an abandoned field name must not become an unguarded path to a
  command line
- Creating a session is otherwise unchanged

## Contract tests

| Test | Asserts | **Must fail when** |
|---|---|---|
| `TestCreateFormHasNoResumeField` | Rendered markup has no `name="resume"` and no `conversation-suggestions` | The field survives in a hidden input |
| `TestCreateStillStartsFresh` | A create with no resume value starts a session normally | The removal breaks the ordinary path |
| `TestStrayResumeValueIsNotExecuted` | `resume=$(whoami)` reaches no command line | An abandoned field name remains a path to a command |
| `TestViewCarriesNoConversations` | The view struct exposes no conversation data | The field is left "for later" |
| `TestConversationListingIsGone` | No package references `listConversations` | The file is kept unused, becoming the fifth caller-less thing |
| `TestCreateEmitsExactlyOneAuditRecord` | Unchanged | The removal disturbs the record |
