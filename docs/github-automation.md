# GitHub Automation & Setup

Everything the repo leans on from GitHub, and what you must configure once per
project. **No secret values appear in this repo — only their names.**

---

## What is wired up

| Feature | File / location | Cost on a public repo |
|---|---|---|
| CI guardrails (hook tests, template integrity) | `.github/workflows/ci.yml` | Free — Actions is free on GitHub-hosted runners for public repos |
| Issue → automated PR | `.github/workflows/claude-issue.yml` | Runner is self-hosted; Claude API usage is billed by Anthropic |
| CodeQL code scanning | `.github/workflows/codeql.yml` *(or Default setup)* | Free for public repos |
| Secret scanning | Automatic | Free and on by default for public repos |
| Push protection | Settings → Code security | Free for public repos — **turn it on** |
| Dependabot alerts + updates | `.github/dependabot.yml` | Free |
| Dependency review on PRs | `.github/workflows/dependency-review.yml` | Free for public repos |
| Private vulnerability reporting | Settings → Code security | Free |
| Issue Forms | `.github/ISSUE_TEMPLATE/*.yml` | Free |
| Code owners review | `.github/CODEOWNERS` | Free |
| Rulesets / branch protection | Settings → Rules | Free for public repos |
| Discussions, Projects, Pages | Settings | Free |

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

## 2. Self-hosted runner

`claude-issue.yml` targets `runs-on: [self-hosted]`.

Settings → **Actions → Runners → New self-hosted runner**, then follow the
generated commands on your machine. Afterwards:

```bash
cd ~/actions-runner
sudo ./svc.sh install     # run as a service
sudo ./svc.sh start
sudo ./svc.sh status
```

The runner host must have: `git`, `bash`, `curl`, plus your project's toolchain.
The action installs its own Bun/Claude Code dependencies.

**Security note.** A self-hosted runner executes code from the repository. On a
*public* repo this is a real risk: never enable it for `pull_request` events from
forks. This template only triggers on `issues: [labeled]`, which requires write
access to apply the label — that is the safety boundary. Do not widen the trigger
without thinking it through.

Prefer no runner to maintain? Change one line:

```yaml
runs-on: ubuntu-latest    # free for public repos
```

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

## 5. Turn on the free security features

Settings → **Code security**:

- [ ] Secret scanning — on (automatic for public repos)
- [ ] **Push protection** — on
- [ ] Private vulnerability reporting — on
- [ ] Dependabot alerts — on
- [ ] Dependabot security updates — on
- [ ] Code scanning → CodeQL → **Default setup** (simplest), *or* fill in `codeql.yml`

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
| "No runner matching labels" | Runner offline, or labels do not include `self-hosted` |
| Auth failure in the action | `ANTHROPIC_API_KEY` missing/expired at repo scope |
| Agent opens a PR that ignores the rules | `settings: .claude/settings.json` was removed from the step |
| CI fails on `AGENTS.md is N lines` | It grew past 150 — move detail into `docs/` and link it |
