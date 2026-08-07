# Data Model: Finish the dashboard

Four entities. None is new — each is an existing thing whose shape or sources
change. This milestone adds no storage and no persisted state.

---

## 1. Create-form view

What the daemon hands the create template. The changes here are mostly
**subtractions**, and that is the point: three of the four are things the browser
should never have been told.

| Field | Before | After |
|---|---|---|
| `StartCommands []string` | configured command **names** | **removed** — a name is a command detail, and FR-002 keeps both out of the browser |
| `Conversations []Conversation` | identifiers for the resume field | **removed** with the field (FR-020) |
| `Suggestions []string` | discovered children only | the **union** of roots, explicit list, and discovered children |
| `Roots []string` | approved roots, for the help text | unchanged |
| *(new)* `RemoteDefault bool` | — | whether the switch renders on; `false` |

**Relationship**: the view is derived per render and persisted nowhere. Nothing
here is state.

The removals matter more than the addition. `StartCommands` reaching the browser
is what let milestone 4 ship a form that selected commands by name while the
requirement said not to.

---

## 2. Directory suggestion

A convenience for the create form. **Never an authorisation** (FR-009).

| Property | Value |
|---|---|
| Sources | approved roots (always) ∪ `workdir_suggestions` (explicit) ∪ discovered children (opt-in) |
| Combination | union, deduplicated, sorted |
| Default when only roots are configured | the roots themselves |
| Discovery depth | one level below each root, unchanged |
| Validation | **none of its own** — a chosen path is checked exactly as a typed one |

Roots are safe as a default source because they disclose nothing the operator has
not already configured. Their children are not, which is why enumerating them
stays behind `discover_roots`.

---

## 3. Session mode, at create time

Unchanged as a model. What changes is only how it is **asked for**.

| Property | Value |
|---|---|
| Values | `ModeLocal`, `ModeRemote` |
| Derived from | `Session.StartCommand`, a configured name — unchanged |
| Persisted as | tmux option `@crswd-start` — unchanged |
| **Asked for as** | a checkbox posting `remote_control=on`, or absent |

The wire format is two states, and the mapping from state to configured command
lives in the daemon. No name and no command line crosses in either direction.

---

## 4. Diagnostic output

Newly *separated*, which is the fix.

| Stream | Carries | Read by |
|---|---|---|
| stdout | audit records, JSON, one per request | `journalctl … \| grep '^{' \| jq .` |
| stderr | human diagnostics — dependency notes, startup errors | an operator reading the journal directly |

They share stdout today, which is why the documented audit-trail command cannot
work: it feeds everything the daemon says to a parser that accepts only records.

**The invariant this establishes**: on stdout, every line is a record. The
documented filter depends on it, and `TestDiagnosticsGoToStderr` is what keeps it
true.

---

## What this milestone deliberately does not model

- **A resumable conversation.** The entity existed in milestone 4, was
  incomprehensible in use, and is removed. #95 will need a different one — *this
  session's* conversation, not *this directory's* list — and designing it here
  would be guessing at a problem the operator asked to think about further.
