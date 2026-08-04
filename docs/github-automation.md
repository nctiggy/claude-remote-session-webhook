# GitHub Automation & Setup

Everything the repo leans on from GitHub, and what you must configure once per
project. **No secret values appear in this repo — only their names.**

---

## What is wired up

**This repo is public**, and that is a deliberate security decision rather than a
default. On a private repo, secret scanning, push protection, CodeQL, dependency
review, and rulesets are all either licence-gated or Pro-gated. Public makes every
one of them free — on a project whose subject matter is secrets and an RCE surface,
that tooling is worth more than design obscurity.

| Feature | File / location | Status |
|---|---|---|
| CI guardrails (hook tests, template integrity) | `.github/workflows/ci.yml` | **On** — free on public repos |
| gitleaks secret scan | `.github/workflows/ci.yml` + `.githooks/pre-commit` | **On** |
| Issue → automated PR | `.github/workflows/claude-issue.yml` | **On** — GitHub-hosted; Claude usage billed by Anthropic |
| Secret scanning + push protection | Settings → Code security | **On** |
| CodeQL code scanning | `.github/workflows/codeql.yml` | **On** — `go` |
| Dependency review on PRs | `.github/workflows/dependency-review.yml` | **On** |
| Dependabot alerts + security updates | `.github/dependabot.yml` | **On** |
| Dependency graph | Automatic | **On** |
| Rulesets / branch protection | Settings → Rules | **On** |
| Private vulnerability reporting | Settings → Code security | **On** |
| Code owners review | `.github/CODEOWNERS` | **On** |
| Issue Forms | `.github/ISSUE_TEMPLATE/*.yml` | **On** |

**The cost of being public** is targeting, not design disclosure: the auth design is
standard and does not depend on secrecy. What must never appear here is anything
that identifies the actual deployment — hostname, tunnel ID, Access AUD, allowed
emails, real paths. See [`deploy/README.md`](../deploy/README.md); every file there
is an example, and the real values come from the environment or 1Password.

---

## 1. Required secrets

Repository → **Settings → Secrets and variables → Actions**.

| Secret | Needed by | Notes |
|---|---|---|
| `CLAUDE_CODE_OAUTH_TOKEN` | `claude-issue.yml` | **Default.** Subscription auth (Pro/Max) — no API billing. Generate with `claude setup-token`. |
| `ANTHROPIC_API_KEY` | `claude-issue.yml` *(alternative)* | Pay-as-you-go API billing. Only if you swap the input in the workflow. |
| `GITHUB_TOKEN` | all workflows | **Injected automatically.** Never create this yourself. |

### Subscription auth (recommended)

```bash
claude setup-token          # interactive; prints a long-lived token
gh secret set CLAUDE_CODE_OAUTH_TOKEN --body '<paste>'
```

The token is tied to your Claude subscription, so runner activity draws on the
subscription rather than metered API credits. It is long-lived but not eternal —
if the automation starts failing auth, regenerate and re-set the secret.

Using Bedrock / Vertex / Foundry instead of the Anthropic API? Swap
`anthropic_api_key` for `use_bedrock` / `use_vertex` / `use_foundry` plus OIDC —
see the action's `docs/cloud-providers.md`.

> Never paste a key into a workflow file, an issue, or a PR comment. Push
> protection will catch the obvious cases; do not rely on it as your only defence.

---

## 2. Runner

`claude-issue.yml` targets `runs-on: ubuntu-latest` — GitHub-hosted. There is no
runner to install or keep online. This repo is private, so Actions minutes come out
of the account's included quota rather than being free.

**Do not move this to a self-hosted runner on the daemon's own host.** A self-hosted
runner executes repository code on the machine it runs on; putting one on the box
that also runs unsandboxed Claude sessions collapses two trust boundaries into one
for no benefit. If a self-hosted runner is ever genuinely needed, it belongs on a
separate machine.

The trigger is `issues: [labeled]`, which requires write access to apply the label —
that is the safety boundary. Do not widen it to `pull_request` without thinking it
through.

---

## 2a. Which model the automation uses

Pinned in `claude-issue.yml` as `claude_args: --max-turns 25 --model claude-opus-5`.

Two decisions are worth stating, because both look like fussiness until they bite:

**Why pin at all.** Left unset, the run takes `claude-code-action`'s default, which
resolves through the OAuth token to whatever the CLI defaults to *at the time it
runs*. That can move between action versions or subscription changes with no commit
to point at. `go 1.23` and `golangci-lint v1.62` are pinned for the same reason. On a
repo where a request that passes authentication is unsandboxed code execution, which
model wrote a PR belongs in the diff.

**Why here and not `.claude/settings.json`.** That file is passed to the action too,
and a `"model"` key in it would work — but it also governs every interactive session
in this repo, and an operator switching models by hand should not be fighting the
automation's choice. The automation and a human at a terminal have different needs;
pinning the workflow gets reproducibility without imposing it on people.

The action declares no `model` input — 32 inputs and that is not one of them — so the
value rides in `claude_args`, which is documented as arguments passed straight to the
CLI. If a future version adds a real input, move it and delete this paragraph.

---

## 3. Labels

Create the trigger label (name must match the workflow's `if:` exactly):

```bash
gh label create claude-fix   --color 1F7A78 --description "Hand to Claude automation"
gh label create bug          --color d73a4a --description "Something is broken"
gh label create enhancement  --color a2eeef --description "New behaviour"
gh label create dependencies --color 0366d6 --description "Dependency updates"
```

---

## 4. Protect `main`

Settings → **Rules → Rulesets → New branch ruleset**, target `main`:

- Restrict deletions
- Block force pushes
- Require a pull request before merging (1 approval, require code-owner review)
- Require status checks: `Template guardrails`, and `Build / test / lint` once filled in
- Require branches to be up to date before merging

This is what makes "never push to main" structural rather than aspirational —
including for the automation, which only ever opens PRs.

---

## 5. Security features

All free on a public repo, and all on:

- [x] Dependabot alerts + security updates
- [x] Dependency graph
- [x] Secret scanning + push protection
- [x] CodeQL (`codeql.yml`, `go`)
- [x] Dependency review on PRs
- [x] Private vulnerability reporting

Plus gitleaks, which is not a GitHub feature. Enable the local hook once per clone:

```bash
git config core.hooksPath .githooks
```

It no-ops with a warning if `gitleaks` is not installed, so a missing binary never
makes the repo uncommittable. CI runs it regardless, which is what actually keeps
an unscanned commit off `main`.

---

## 6. Verify it end to end

```bash
gh workflow list
gh workflow run CI && gh run watch          # guardrails should pass on a clean clone

gh issue create --title "[bug] test automation" \
                --body "Steps: 1. do X  2. see Y. Expected Z." \
                --label claude-fix
gh run watch                                 # runner picks it up
gh pr list                                   # a PR should appear, never a push to main
```

---

## Troubleshooting

| Symptom | Cause |
|---|---|
| Workflow never starts | Label name differs from the `if:` in `claude-issue.yml` |
| "No runner matching labels" | Workflow was pointed at a runner label that does not exist |
| Auth failure in the action | `ANTHROPIC_API_KEY` missing/expired at repo scope |
| Agent opens a PR that ignores the rules | `settings: .claude/settings.json` was removed from the step |
| CI fails on `AGENTS.md is N lines` | It grew past 150 — move detail into `docs/` and link it |
