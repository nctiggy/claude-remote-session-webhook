# ai-project-template

A portable, tool-agnostic scaffold for AI-assisted development.

The premise: **consistency should come from the repository, not from remembering
to ask for it.** Standards that live only in prose get skimmed and drift. So the
rules here are enforced by hooks and CI, and every lane of work — human,
autonomous loop, or GitHub automation — inherits the same guardrails.

---

## The three lanes

All three read the same `AGENTS.md`, obey the same constitution, and run under the
same hooks. That is the whole idea.

**1. Feature — Spec Kit + Ralph**
For anything with more than one moving part.
```
/speckit-constitution → /speckit-specify → /speckit-plan → /speckit-tasks → /speckit-implement
```
Paste the resulting tasks into `ralph/IMPLEMENTATION_PLAN.md` and let the loop
work them one at a time, fresh context per iteration.

**2. Quick fix**
A typo, a one-line bug, bad copy. No spec, no ceremony. Fix it, verify it, append
one line to `docs/fixes-log.md`.

**3. GitHub issue → automated PR**
Label an issue `claude-fix`. A self-hosted runner picks it up, works under the
same rules, and opens a reviewable PR. It never pushes to `main`.

---

## Layout

```
AGENTS.md                  ← the contract. Read first. Under 150 lines, on purpose.
CLAUDE.md                  ← thin pointer to AGENTS.md
docs/                      ← loaded on demand, not all at once
  design-system.md           tokens, spacing, header rules
  components.md              canonical components — use these, don't invent
  auth-and-sessions.md       session handling; the no-bleed rule
  security.md                input, authz, secrets
  fixes-log.md               append-only quick-fix log
  github-automation.md       runner, secrets, rulesets, free-tier setup
.specify/                  ← Spec Kit: constitution, templates, scripts
.claude/
  settings.json              hook wiring
  hooks/                     danger-guard · format-and-lint · session-start
  hooks/test-hooks.sh        the guardrails are themselves tested
  skills/speckit-*           Spec Kit slash commands
ralph/
  PROMPT.md                  loop prompt: ONE task, then exit
  IMPLEMENTATION_PLAN.md     prioritized tasks
  PROGRESS.md                append-only notebook across fresh contexts
  loop.sh                    bounded loop, commits each iteration
.github/
  workflows/                 ci · claude-issue · codeql · dependency-review
  ISSUE_TEMPLATE/            issue forms that feed automation cleanly
  dependabot.yml · CODEOWNERS · PULL_REQUEST_TEMPLATE.md
```

---

## Start a new project

### The easy way — the `start-project` skill

One command, no clone. Claude does the whole bootstrap: creates the repo, fills
every placeholder, prunes docs that do not apply, enables the guardrails and the
free GitHub security features, optionally wires a design system, and hands back a
repo that is ready for specs and Ralph loops.

```bash
claude --plugin-url https://github.com/nctiggy/ai-project-template/releases/latest/download/start-project.zip
```

then in the session:

```
/start-project:start-project
```

It reads what you already told it and asks only about genuine gaps — typically
visibility, whether there is a UI, and whether there is auth. Everything else
(build/test/lint commands, CI setup, CodeQL language, Dependabot ecosystem) is
derived from the stack.

Source lives in [`plugin/`](plugin/); it is removed from generated projects.

### The manual way

```bash
gh repo create my-project --public --template nctiggy/ai-project-template --clone
cd my-project
rm -rf plugin/                          # template tooling, not project content

grep -rn "<FILL IN" --exclude-dir=.git . # fill every marker, AGENTS.md first
chmod +x .claude/hooks/*.sh ralph/loop.sh
./.claude/hooks/test-hooks.sh            # should be 28/28

claude
> /speckit-constitution
```

Then configure the repo side once: [`docs/github-automation.md`](docs/github-automation.md).

---

## Run a Ralph loop

```bash
git switch -c feat/my-feature          # loop refuses to run on main
./ralph/loop.sh 5                      # cap at 5 iterations
```

Run `claude -p "$(cat ralph/PROMPT.md)"` manually two or three times first. Watch
what it does. Only wrap it in the loop once the behaviour is boring — an
autonomous loop amplifies whatever it already does, including mistakes.

The loop stops on the iteration cap, on any failure, or when `RALPH_COMPLETE`
appears in `ralph/PROGRESS.md`.

---

## Trigger the issue automation

```bash
gh issue create --title "[bug] Sign-out leaves cached data" \
  --body "1. Sign in as A  2. Sign out  3. Sign in as B → A's data still shows" \
  --label claude-fix
gh run watch
gh pr list
```

---

## What is enforced

| Hook | Fires | Does |
|---|---|---|
| `danger-guard.sh` | `PreToolUse` (Bash) | **Blocks** `rm -rf /`, `rm -rf ~`, force-push to main/master, `git reset --hard origin`, `DROP`/`TRUNCATE TABLE`, `mkfs`, `dd` to a device |
| `format-and-lint.sh` | `PostToolUse` (Write/Edit) | Formats + auto-fixes the changed file. No-ops when a tool isn't installed |
| `session-start.sh` | `SessionStart` | Injects git status, open TODOs, and the read-this-first checklist |

Plus, in CI: required files present, `AGENTS.md` under 150 lines, hooks executable
and shellcheck-clean, all 28 hook behaviour tests green, no secret-like files tracked.

---

## Spec Kit provenance

`.specify/` and `.claude/skills/speckit-*` are **generated by `specify-cli`**, not
hand-written. They are committed so a new project works immediately without
re-running the tool.

Pinned to a **stable release**, not `main` — an unpinned dev build makes the
starting point of every future project non-reproducible.

```bash
pipx install "git+https://github.com/github/spec-kit.git@v0.14.4"   # or: uv tool install
specify init . --integration claude --script sh --force
```

Recorded in `.specify/init-options.json` (`speckit_version`). To upgrade: bump the
tag, re-run the two commands, then **restore `.specify/memory/constitution.md`** —
`--force` overwrites it with the blank template.

The skills are Claude-flavoured because of `--integration claude`. That is
deliberate and does not compromise portability: `AGENTS.md` stays the
tool-agnostic contract that Copilot, Cursor, and Codex read. Re-run `specify init`
with a different `--integration` to add another agent alongside it.

## Status line (opt-in)

`.claude/statusline.sh` ships with the template but is **not enabled by default** —
a status line is a personal preference, and forcing one through the shared
`.claude/settings.json` would override whatever every collaborator already has.

Turn it on for yourself, in your own user settings:

```bash
cp .claude/statusline.sh ~/.claude/statusline.sh && chmod +x ~/.claude/statusline.sh
```

then add to `~/.claude/settings.json`:

```json
{
  "statusLine": { "type": "command", "command": "~/.claude/statusline.sh", "padding": 1 }
}
```

Shows model (+ effort), directory, branch/worktree, a colour-coded context bar
(green → yellow at 60% → red at 85%), 5-hour rate-limit burn, and open-PR review
state. Cost only appears above $0.50, so it stays quiet on a subscription.

Requires `jq`. Degrades to `?` rather than erroring if fields are absent.

## Design notes

**Why `AGENTS.md` and not `CLAUDE.md`?** `AGENTS.md` is tool-agnostic — Copilot,
Cursor, Codex and Claude Code all read it. `CLAUDE.md` is a three-line pointer so
there is exactly one source of truth.

**Why under 150 lines?** A context file long enough to need skimming gets skimmed.
Detail belongs in `docs/`, loaded only when relevant.

**Why fresh context every Ralph iteration?** Long sessions drift, forget the plan,
and start inventing. State belongs in git and `PROGRESS.md`, not a conversation.

**Why test the hooks?** An unenforced guard is a comment. `test-hooks.sh` runs in
CI so the guardrails cannot rot quietly.

MIT licensed.
