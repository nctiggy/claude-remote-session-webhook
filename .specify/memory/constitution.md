# `<FILL IN: PROJECT_NAME>` Constitution

The highest-authority document in this repo. Spec Kit reads it at
`/speckit-specify`, `/speckit-plan`, `/speckit-tasks`, and `/speckit-implement`.
Where anything conflicts with it — a spec, a plan, a PR, a preference — **this wins.**

Operational detail lives in `AGENTS.md`. Principles live here.

---

## Core Principles

### I. Security is a gate, not a review comment (NON-NEGOTIABLE)

[`docs/security.md`](../../docs/security.md) is binding. Every plan and every task
list is checked against it. A feature that cannot satisfy it does not ship.

Specifically, and without exception:
- Authorization is enforced **server-side on every request**. Client guards are UX.
- Ownership is checked, not merely authentication.
- Input is validated at the boundary; output is encoded by context.
- No secret ever enters the repository.

### II. Design system is binding (NON-NEGOTIABLE)

[`docs/design-system.md`](../../docs/design-system.md) and
[`docs/components.md`](../../docs/components.md) define the vocabulary.

- Tokens only — never a hard-coded colour, size, or font in a component.
- Reuse the canonical component. A second button component is a defect.
- Every authenticated view shows user identity top-right. No exceptions.

### III. Unknowns are surfaced, never invented (NON-NEGOTIABLE)

If a requirement is ambiguous, the artifact must say `NEEDS CLARIFICATION` and
stop. A plausible guess that turns out wrong costs more than a question.

This applies to specs, plans, tasks, PRs, and autonomous loop iterations alike.

### IV. Every change is verifiable

"It works" is not a claim, it is a demonstration. A change is done when the
build, test, and lint commands in `AGENTS.md` pass — the same commands CI runs.
No merging on a red or skipped check.

### V. Smallest correct change

Fix the thing asked for. Do not refactor adjacent code, rename things in passing,
or "improve" what was not in scope. Unrequested churn hides real changes in review.

### VI. Standards are enforced, not documented

A rule that lives only in prose is a suggestion. Rules belong in
`.claude/hooks/` and `.github/workflows/`, which are themselves tested
(`.claude/hooks/test-hooks.sh`). Changing a guardrail requires a reviewed PR —
that is the point, not friction to be avoided.

### VII. `<FILL IN: PRINCIPLE_7>`

`<FILL IN: project-specific principle — e.g. Test-First, Library-First,
Observability, Simplicity/YAGNI. Delete if six is enough.>`

---

## Development Workflow

Three lanes, one set of rules. All inherit these principles.

| Lane | When | Path |
|---|---|---|
| **Feature** | More than one moving part | constitution → `/speckit-specify` → `/speckit-plan` → `/speckit-tasks` → `/speckit-implement` |
| **Quick fix** | One-line bug, typo, copy | Fix → verify → one line in `docs/fixes-log.md` |
| **Automated** | Issue labelled `claude-fix` | Runner → bounded run → **PR only, never `main`** |

Autonomous execution (`ralph/loop.sh`) works the plan **one task per iteration**
with a fresh context each time, and commits after each. It is bound by every
principle above.

---

## Quality Gates

A change may merge only when all of these are true:

- [ ] Build, test, and lint green (the `AGENTS.md` commands)
- [ ] New behaviour has a test that fails without the change
- [ ] `docs/security.md` checklist satisfied for anything touching input, authz, or secrets
- [ ] `docs/design-system.md` + `docs/components.md` respected for anything visual
- [ ] Sign-out clears all state, if auth or session was touched
- [ ] No `NEEDS CLARIFICATION` left unanswered
- [ ] Reviewed by a code owner (`.github/CODEOWNERS`)

---

## Governance

This constitution supersedes all other practice. Amending it requires a pull
request that states what changed, why, and what it breaks — reviewed by a code
owner like any other change.

Complexity must be justified in the plan. "It might be useful later" is not a
justification.

Agents: `AGENTS.md` is your runtime guide; this document is the authority behind it.

**Version**: 1.0.0 | **Ratified**: `<FILL IN: YYYY-MM-DD>` | **Last Amended**: `<FILL IN: YYYY-MM-DD>`
