# claude-remote-session-webhook Constitution

The highest-authority document in this repo. Spec Kit reads it at
`/speckit-specify`, `/speckit-plan`, `/speckit-tasks`, and `/speckit-implement`.
Where anything conflicts with it — a spec, a plan, a PR, a preference — **this wins.**

Operational detail lives in `AGENTS.md`. Principles live here.

---

## Core Principles

### I. Security is a gate, not a review comment (NON-NEGOTIABLE)

[`docs/security.md`](../../docs/security.md) is binding. Every plan and every task
list is checked against it. A feature that cannot satisfy it does not ship.

A request that passes authentication causes **unsandboxed code execution on the
host**. There is no second line of defence behind the auth check. Therefore, and
without exception:
- Authorization is enforced **server-side on every request**. The calling skill is
  not a trust boundary — anyone can write another client.
- Ownership is checked, not merely authentication.
- Input is validated at the boundary; no shell string is ever constructed.
- Missing or weak auth configuration is a startup failure, never a warning.
- No secret ever enters the repository.

### II. Unknowns are surfaced, never invented (NON-NEGOTIABLE)

If a requirement is ambiguous, the artifact must say `NEEDS CLARIFICATION` and
stop. A plausible guess that turns out wrong costs more than a question.

This applies to specs, plans, tasks, PRs, and autonomous loop iterations alike.

### III. Every change is verifiable

"It works" is not a claim, it is a demonstration. A change is done when the
build, test, and lint commands in `AGENTS.md` pass — the same commands CI runs.
No merging on a red or skipped check.

### IV. Smallest correct change

Fix the thing asked for. Do not refactor adjacent code, rename things in passing,
or "improve" what was not in scope. Unrequested churn hides real changes in review.

### V. Standards are enforced, not documented

A rule that lives only in prose is a suggestion. Rules belong in
`.claude/hooks/` and `.github/workflows/`, which are themselves tested
(`.claude/hooks/test-hooks.sh`). Changing a guardrail requires a reviewed PR —
that is the point, not friction to be avoided.

### VI. Blast radius is bounded by construction (NON-NEGOTIABLE)

Sessions run with `--dangerously-skip-permissions`. The permission prompt is gone,
so every constraint it used to provide must be re-established structurally:

- Working directories are **allowlisted**, resolved, and verified — never taken
  from the caller as a free-form path.
- Concurrent sessions are **capped**; the daemon refuses rather than degrading
  the host.
- Every session has an **absolute lifetime**, enforced by a reaper, not by the
  next request. It is renewed by nothing, and it is switched off only by an
  operator who has first unbounded the daemon's own ceiling — two decisions, so
  that a caller cannot reach it alone. **A session with it switched off is bounded
  by nothing time-based at all**, and the four constraints around this one are
  then the whole of the containment. That is a state an operator may choose; it is
  not a state anything here prevents.

  *Amended 2.0.0: an idle timeout was required here until 2026-08-14. It was
  withdrawn because it bounded the wrong thing — a session waiting for a human is
  quiet, and destroying it for that lost work the operator expected to keep — not
  because it was mis-measured. What it cost to remove is written above.*
- Teardown is **verified**, not assumed. An orphaned tmux window is a live
  unsandboxed shell with no owner.
- The listener binds **loopback only**. Reachability comes from the tunnel.

A feature that widens any of these needs an explicit justification in the plan
naming what now becomes reachable.

### VII. Design system is binding

[`docs/design-system.md`](../../docs/design-system.md) and
[`docs/components.md`](../../docs/components.md) define the vocabulary for the
dashboard.

- Tokens only — never a hard-coded colour, size, or font in a template.
- Reuse the canonical component. A second session card is a defect.
- **Pane output is rendered as text, never as HTML.** Everything a Claude session
  prints reaches the browser. This is the project's only XSS surface and it is
  closed by construction, not by sanitising.

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
- [ ] `docs/auth-and-sessions.md` checklist satisfied, if auth or the session lifecycle was touched
- [ ] Cross-session isolation still holds; teardown still verified
- [ ] `docs/design-system.md` + `docs/components.md` respected for anything visual
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

**Version**: 2.0.0 | **Ratified**: 2026-08-02 | **Last Amended**: 2026-08-14
