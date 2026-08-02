# Security Policy

## Reporting a vulnerability

Do **not** open a public issue for a security problem.

Use GitHub private vulnerability reporting:
**Security → Advisories → Report a vulnerability** (free on public repos).

<FILL IN: contact email, if you want a second channel.>

Expect an acknowledgement within `<FILL IN: e.g. 3 business days>`.

## What is enabled on this repository

| Control | Status |
|---|---|
| Secret scanning | On — automatic and free for public repos |
| Push protection | Enable in Settings → Code security |
| Dependabot alerts + updates | On (`.github/dependabot.yml`) |
| Dependency review on PRs | On (`.github/workflows/dependency-review.yml`) |
| CodeQL code scanning | Enable Default setup, or fill in `.github/workflows/codeql.yml` |
| Branch protection | Configure a ruleset on `main` (see `docs/github-automation.md`) |

## Secrets

Never commit a secret. If one is committed, assume it is public permanently:
rotate it immediately, then remove it from history.

Application-level security rules live in [`docs/security.md`](docs/security.md).
