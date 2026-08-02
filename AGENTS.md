# AGENTS.md

Primary context file for **every** AI agent working in this repo (Claude Code, Copilot,
Cursor, Codex, or a human). Read this first. Everything else loads on demand.

> `<FILL IN>` markers are the only things a new project must replace.

---

## Why

`<FILL IN: one paragraph. What does this project do, and for whom? If an agent has
to guess the purpose, it will guess wrong and build the wrong thing.>`

---

## What — project map

| Path | What lives here |
|---|---|
| `<FILL IN: src/>` | `<FILL IN: application code>` |
| `<FILL IN: tests/>` | `<FILL IN: test suites>` |
| `docs/` | Standards, loaded on demand (see Progressive disclosure) |
| `ralph/` | Autonomous loop: plan, prompt, progress notebook |
| `.specify/` | Spec Kit: constitution, templates, feature specs |
| `.claude/hooks/` | Enforced guardrails (see Enforcement) |
| `.github/workflows/` | CI + issue automation |

---

## How — the only commands that matter

Keep this table honest. A stale command here costs more than a missing one.

| Task | Command |
|---|---|
| Install | `<FILL IN: npm ci>` |
| Build | `<FILL IN: npm run build>` |
| Test (all) | `<FILL IN: npm test>` |
| Test (single) | `<FILL IN: npm test -- path/to/file.test.ts>` |
| Lint | `<FILL IN: npm run lint>` |
| Format | `<FILL IN: npm run format>` |
| Typecheck | `<FILL IN: npm run typecheck>` |

**Definition of done** — a change is not done until build, test, and lint all pass.
CI runs exactly these commands; do not hand-wave them locally.

---

## Three lanes of work

All three inherit the same hooks, the same constitution, the same docs.

**1. Feature — Spec Kit + Ralph.** Anything with more than one moving part.
```
/speckit-constitution → /speckit-specify → /speckit-plan → /speckit-tasks → /speckit-implement
```
Templates must flag unknowns as `NEEDS CLARIFICATION` rather than guessing. If the
spec is ambiguous, stop and ask — do not invent requirements.

**2. Quick fix.** A typo, a one-line bug, a bad copy string. No spec, no ceremony.
Fix it, prove it, then append one line to `docs/fixes-log.md`:
```
- 2026-01-15 — Logout left stale token in localStorage; clear on signOut(). (#42)
```

**3. GitHub issue → automated PR.** Label an issue `claude-fix`. A runner picks it
up, works under these same rules, and opens a PR. It never pushes to `main`.

---

## Progressive disclosure — load only what the task needs

Do not read all of these. Read the one that matches what you are about to change.

| Touching… | Read |
|---|---|
| UI, layout, spacing, colour | `docs/design-system.md` |
| A button, form, modal, nav | `docs/components.md` |
| Login, session, token, logout | `docs/auth-and-sessions.md` |
| User input, authz, secrets, routes | `docs/security.md` |
| Anything at all | this file |

---

## Conventions

One example each. Match the surrounding code over the example if they conflict.

**Naming** — `<FILL IN>`
```
<FILL IN: e.g. components PascalCase, hooks useCamelCase, files kebab-case>
```

**Errors** — never swallow. Fail loud, with context.
```
<FILL IN: e.g. throw new AppError('checkout.payment_failed', { orderId }, cause)>
```

**Tests** — `<FILL IN: what a test must cover before a PR is opened>`
```
<FILL IN: one canonical test example>
```

**Comments** — explain *why*, never *what*. If the code needs a "what" comment,
rewrite the code.

---

## Enforcement — not suggestions

Standards live in `.claude/hooks/` and `.github/workflows/`, not in prose. Prose
gets skimmed; hooks do not.

| Hook | Fires on | Does |
|---|---|---|
| `danger-guard.sh` | `PreToolUse` (Bash) | **Blocks** `rm -rf /`, `rm -rf ~`, force-push to main/master, `git reset --hard origin`, `DROP`/`TRUNCATE TABLE`, `mkfs`, `dd` to a device. Exit 2 with a reason. |
| `format-and-lint.sh` | `PostToolUse` (Write/Edit) | Formats + auto-fixes the changed file. No-ops if the tool is not installed. |
| `session-start.sh` | `SessionStart` | Injects git status, open TODOs, and this checklist. |

Do not disable, bypass, or route around a hook. If a guard is wrong, fix the guard
in a PR — that is a code change with a review, which is the point.

---

## Hard rules

- **Never commit secrets.** No keys, tokens, or `.env` files. Secret scanning is on.
- **Never push to `main`.** Branch, PR, review. The automation follows this too.
- **Never invent a requirement.** Unknown → `NEEDS CLARIFICATION` → ask.
- **Never leave the tree broken.** Build + test + lint green before you commit.
- **Prefer editing over creating.** A new file needs a reason.
- Reuse what `docs/components.md` already defines. Do not invent a second button.
