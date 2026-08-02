# Security Policy

## Reporting a vulnerability

Do **not** open a public issue for a security problem.

Use GitHub private vulnerability reporting:
**Security → Advisories → Report a vulnerability**.

Second channel, if you prefer: **nctiggy@gmail.com**.

Expect an acknowledgement within `3 business days`. This is a single-maintainer
side project, not a funded product — response times are best-effort.

Bear in mind what this software is: a daemon that spawns Claude Code sessions with
`--dangerously-skip-permissions`. Anything that lets an unauthenticated caller reach
a session, or lets one caller read another's session, is critical by default.

## What is enabled on this repository

| Control | Status |
|---|---|
| Secret scanning + push protection | **On** |
| CodeQL code scanning | **On** (`.github/workflows/codeql.yml`, `go`) |
| Dependency review on PRs | **On** (`.github/workflows/dependency-review.yml`) |
| Dependabot alerts + security updates | **On** (`.github/dependabot.yml`) |
| Dependency graph | **On** |
| Branch protection | **On** — ruleset on `main` |
| gitleaks | **On** — in CI, and in `.githooks/pre-commit` |

This repo is public specifically so all of the above are available. gitleaks runs
in addition to GitHub's own scanning because an autonomous agent commits here, and
GitHub does not know this project's own secret formats — see `.gitleaks.toml`.

## Secrets

Never commit a secret. If one is committed, assume it is public permanently:
rotate it immediately, then remove it from history.

Application-level security rules live in [`docs/security.md`](docs/security.md).
