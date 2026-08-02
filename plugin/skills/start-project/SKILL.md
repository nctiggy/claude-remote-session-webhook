---
name: start-project
description: Bootstrap a brand-new project repository from the ai-project-template — creates the GitHub repo, fills in every placeholder, enables the guardrails and free GitHub security features, optionally builds the design system, and hands back a repo that is immediately ready for Spec Kit PRDs and Ralph loops. Use this skill whenever the user wants to start, create, scaffold, bootstrap, spin up, or kick off a new project, repo, app, service, CLI, or codebase — even if they never mention the template by name, and even if they only describe the idea ("I want to build a thing that…"). Prefer this over hand-rolling a repo from scratch.
argument-hint: "[project name] [one-line description]"
allowed-tools: Bash(gh:*), Bash(git:*), Bash(mkdir:*), Bash(chmod:*), Bash(sed:*), Bash(grep:*), Bash(ls:*), Bash(cat:*), Bash(bash:*), Read, Write, Edit, Glob
---

# Start a new project

Turn an idea into a working repository with the guardrails already on.

The finished state is specific: a GitHub repo, cloned locally, **zero `<FILL IN>`
markers left**, hooks executable and passing, CI green, and enough context written
down that the very next thing the user can do is write a spec and run a Ralph loop.

## Environment (already gathered — do not re-ask)

- GitHub user: !`gh api user --jq .login 2>/dev/null || echo "NOT AUTHENTICATED"`
- gh auth: !`gh auth status 2>&1 | head -3`
- Working dir: !`pwd`
- Existing dirs here: !`ls -d */ 2>/dev/null | head -10`
- Self-hosted runners: !`gh api repos/nctiggy/ai-project-template/actions/runners --jq '.runners[]?|"\(.name) \(.status)"' 2>/dev/null || echo "none visible"`

If gh is not authenticated, stop and tell the user to run `gh auth login`. Nothing
downstream works without it.

## Phase 1 — Work out what they're building

**Read the conversation first.** Most of this is usually already said. Extract what
you can and *show the user what you inferred* rather than interrogating them. Ask
only about genuine gaps, and batch the questions into one exchange.

What you need:

| Need | Usually inferable from | Ask only if |
|---|---|---|
| Repo name | what they called it | truly unstated |
| One-line purpose (the "Why") | how they described the idea | vague |
| Visibility | — | always ask; never assume public |
| Stack + package manager | "a Next.js app", "a Go CLI" | unstated |
| Has a UI? | the kind of thing it is | ambiguous |
| Has auth/users? | the kind of thing it is | ambiguous |
| Runner: GitHub-hosted or self-hosted | environment block above | both available |

Derive the build/test/lint commands from the stack — do **not** ask a user who said
"Next.js" what their test command is. Read `references/<stack>.md` for the canonical
set. If the stack has no reference file, ask for the five commands directly.

The UI and auth answers decide which docs survive Phase 4. That is why they matter.

## Phase 2 — Create the repo

```bash
gh repo create <name> --<public|private> \
  --template nctiggy/ai-project-template --clone
cd <name>
```

Then remove the template's own tooling, which is not project content:

```bash
rm -rf plugin/            # this skill's source; irrelevant inside a new project
```

## Phase 3 — Fill in every placeholder

Run the script rather than hand-editing twelve files — it is deterministic and it
cannot forget one:

```bash
bash scripts/fill-placeholders.sh   # invoked from the skill directory
```

It rewrites `AGENTS.md`, `.specify/memory/constitution.md`, `.github/CODEOWNERS`,
`.github/dependabot.yml`, `.github/workflows/ci.yml` and the `docs/` set from the
answers in Phase 1.

Then **verify nothing was missed** — this is the acceptance test for this phase:

```bash
grep -rn "<FILL IN" --exclude-dir=.git . || echo "clean"
```

Anything still matching, fix by hand before continuing. A shipped `<FILL IN>` is
worse than no doc at all: it teaches every future agent that the docs are decorative.

## Phase 4 — Prune docs that do not apply

If the project has **no UI**, delete `docs/design-system.md` and
`docs/components.md`, and remove their rows from the `AGENTS.md` progressive-
disclosure table and the constitution's Principle II.

If it has **no auth**, delete `docs/auth-and-sessions.md` and its references.

Keep `docs/security.md` always. Every project handles input.

## Phase 5 — Design system (UI projects only)

Ask: *"Do you want to build a real design system now, or start with plain tokens?"*

**Preferred — Claude Design.** If the user says yes, use the `DesignSync` tool with
the `/design-sync` skill to create or attach a design-system project, then write the
resulting tokens and component inventory into `docs/design-system.md` and
`docs/components.md`. This is the highest-value option: those two docs stop being
aspirational and start describing something real.

**Fallback.** If design scopes are unavailable, suggest the `frontend-design` or
`ui-theme-designer` plugins from the official marketplace, or fill the token tables
by hand from whatever brand the user has.

Either way the docs must end up with real values. See `references/design.md`.

## Phase 6 — Turn the guardrails on

```bash
chmod +x .claude/hooks/*.sh ralph/loop.sh
./.claude/hooks/test-hooks.sh          # expect 28/28
bash scripts/enable-github.sh <owner> <repo>
```

`enable-github.sh` enables the dependency graph, Dependabot alerts and security
updates, creates the `claude-fix`/`dependencies` labels, and applies a `main`
ruleset requiring a PR and the `Template guardrails` check.

Secrets are the user's to paste — never generate or guess one:

```bash
claude setup-token                     # interactive; user runs this
gh secret set CLAUDE_CODE_OAUTH_TOKEN  # paste when prompted
```

If the user picked GitHub-hosted runners, change `runs-on: [self-hosted]` to
`runs-on: ubuntu-latest` in `.github/workflows/claude-issue.yml`.

## Phase 7 — Establish the principles

Run `/speckit-constitution` and work through it with the user. The template's
constitution already binds `docs/security.md` and `docs/design-system.md`; this step
adds the project-specific principles (Principle VII onward).

## Phase 8 — Commit, verify, hand off

```bash
git add -A && git commit -m "chore: initialize <name> from ai-project-template"
git push
bash scripts/verify.sh <owner> <repo>   # waits for CI, reports pass/fail
```

Do not declare success until CI is actually green. If it is red, read the failure
and fix it — handing back a red repo defeats the point of the template.

Then tell the user exactly what is ready and what is next:

```
<name> is ready.

  Repo      https://github.com/<owner>/<name>
  CI        green (Template guardrails + Build/test/lint)
  Hooks     28/28
  Secrets   CLAUDE_CODE_OAUTH_TOKEN set | NOT set — automation will not run

Next, in the repo:
  1. /speckit-specify     describe the first feature
  2. /speckit-plan        then /speckit-tasks
  3. paste tasks into ralph/IMPLEMENTATION_PLAN.md
  4. git switch -c feat/<name> && ./ralph/loop.sh 5
```

## Things that go wrong

- **`gh repo create --template` fails** — the template repo must have its template
  flag set, and the user needs `repo` scope.
- **A placeholder survives** — Phase 3's grep is the gate. Never skip it.
- **CI red on `AGENTS.md is N lines`** — the fill-in made it exceed 150 lines. Move
  detail into `docs/` and link it; do not raise the limit.
- **Runner never picks the job up** — the label must match `claude-fix` exactly, and
  the runner must be online.

## Reference files

Read only what applies:

- `references/node-ts.md`, `references/python.md`, `references/go.md`,
  `references/rust.md` — canonical commands and CI setup per stack
- `references/design.md` — Claude Design / design-system wiring
