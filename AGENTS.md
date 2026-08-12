# AGENTS.md

Primary context file for **every** AI agent working in this repo (Claude Code, Copilot,
Cursor, Codex, or a human). Read this first. Everything else loads on demand.

---

## Why

A self-hosted Go daemon that starts and drives Claude Code sessions on the owner's own machine from anywhere. It serves an authenticated web dashboard and HTTP API on loopback, and spawns each session in a tmux window running with `--dangerously-skip-permissions`. Two clients: a browser, and any script signing its requests by HMAC. The browser is admitted by **one of two doors, chosen in the configuration and never both at once** — Cloudflare Access with Google as the identity provider, reached through a Cloudflare Tunnel under a `*.example.com` hostname so no inbound port is ever opened; or a dashboard password, for a network the operator controls with no Cloudflare in front of it. From the browser: see every session in flight, create, destroy, rename, compact, switch modes, and read or edit the configuration. **Not built yet — do not describe either as working:** relaying Claude's own device-code login when a session asks for it, and the companion Claude skill that would drive the API. A request that passes auth causes unsandboxed code execution on the host — authentication, request integrity, and bounded blast radius are the product, not features bolted onto it.

---

## What — project map

| Path | What lives here |
|---|---|
| `cmd/crswd/` | Daemon entrypoint and wiring, plus the `config check`, `config migrate` and `keygen` subcommands |
| `internal/` | All real logic: `access`, `audit`, `auth`, `buildinfo`, `config`, `httpapi`, `release`, `session`, `tmuxctl`, `updater` |
| `internal/**/*_test.go` | Tests, colocated with the package they cover |
| `web/` | Templates, CSS, hand-written JS — embedded via `go:embed`. No npm, no framework, **no htmx** |
| `deploy/` | systemd unit, Cloudflare Tunnel config, the `crswd-api` shell client |
| `docs/` | Standards, loaded on demand (see Progressive disclosure) |
| `ralph/` | Autonomous loop: plan, prompt, progress notebook |
| `.specify/` | Spec Kit: constitution, templates, feature specs |
| `.claude/hooks/` | Enforced guardrails (see Enforcement) |
| `.github/workflows/` | CI, release, CodeQL, dependency review, issue automation |

---

## How — the only commands that matter

Keep this table honest. A stale command here costs more than a missing one.

| Task | Command |
|---|---|
| Install | `go mod download` |
| Build | `go build ./...` |
| Test (default build) | `go test ./...` |
| Test (single) | `go test ./internal/auth -run TestVerify` |
| Test (real tmux) | `go test -tags tmux ./...` |
| Test (acceptance) | `go test -tags quickstart ./cmd/crswd` |
| Test (dev bypass) | `go test -tags dev ./...` |
| Lint | `golangci-lint run` |
| Format | `gofmt -w . && goimports -w .` |
| Typecheck | `go vet ./...` |

**Definition of done** — a change is not done until build, test, and lint all pass.
CI runs Install, Lint, Typecheck, Test and Build from this table, plus the `tmux`
and `quickstart` suites below; Format and the `dev` tag run nowhere but here.

**A build tag hides a file from the default build, so a tagged suite reports nothing
whether or not it still compiles.** `go test ./...` reaches none of the three below.
Run the one that matches what you touched:

| Tag | Covers | Needs |
|---|---|---|
| `tmux` | `internal/tmuxctl` driven against the real binary, and `internal/session`'s session-name round trip through it | `tmux` installed. Each test gets a private `-L` socket, never the operator's server |
| `quickstart` | `cmd/crswd` acceptance — a real build, a real port, real tmux | `tmux`; `jq`, because one case runs the unit file's documented audit-trail command as written; and `127.0.0.1:8765` free: the deployed daemon holds it and two startup cases bind that exact port |
| `dev` | the loopback auth bypass in `internal/access`, `internal/httpapi` and `cmd/crswd`, all absent from the shipping build | nothing |

`go vet -tags <tag> ./...` compiles a tagged suite without running it — the cheap check
when its environment is not available.

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
- 2026-01-15 — Reaper skipped sessions with a nil pty; guard before Kill(). (#42)
```

**3. GitHub issue → automated PR.** Label an issue `claude-fix` and it joins a queue;
a runner takes one at a time, works under these rules, and opens a PR. Never `main`.

---

## Progressive disclosure — load only what the task needs

Do not read all of these. Read the one that matches what you are about to change.

| Touching… | Read |
|---|---|
| Signing, tokens, Access or dashboard-password login, session lifecycle | `docs/auth-and-sessions.md` |
| Request input, authz, secrets, routes, exposure, rendering pane output | `docs/security.md` |
| Writing or changing any Go | `docs/conventions.md` |
| Layout, spacing, colour, any CSS | `docs/design-system.md` |
| A session card, status pill, pane viewer, action button | `docs/components.md` |
| Anything at all | this file |

**The security docs are binding, not advisory.** A request that passes auth is
unsandboxed code execution on the host. Read them before touching any handler.

---

## Conventions

Standard Go, with the detail and the examples in
[`docs/conventions.md`](docs/conventions.md). The rules themselves:

- **Naming** — packages lowercase and singular; no `util`/`common`.
- **Errors** — never swallow. Wrap with `%w` and context; return, do not
  log-and-continue. Sentinels for what callers branch on, checked with `errors.Is`.
  Never a secret, prompt, or pane content in an error string.
- **Tests** — table-driven, `t.Parallel()`, no network, no real tmux. Every PR needs
  a test that fails without the change; auth and session code needs the negative
  cases too — bad signature, stale timestamp, replay, wrong owner.
- **Comments** — explain *why*, never *what*.

---

## Enforcement — not suggestions

Standards live in `.claude/hooks/` and `.github/workflows/`, not in prose. Prose
gets skimmed; hooks do not.

| Hook | Fires on | Does |
|---|---|---|
| `danger-guard.sh` | `PreToolUse` (Bash) | **Blocks** `rm -rf /`, `rm -rf ~`, force-push to main/master, `git reset --hard origin`, `DROP`/`TRUNCATE TABLE`, `DROP DATABASE`, `mkfs`, `dd` to a device. Exit 2 with a reason. |
| `format-and-lint.sh` | `PostToolUse` (Write/Edit) | Formats + auto-fixes the changed file. No-ops if the tool is not installed. |
| `session-start.sh` | `SessionStart` | Injects git status, open TODOs, and this checklist. |
| `.githooks/pre-commit` | `git commit` | Runs gitleaks on staged changes. Enable once: `git config core.hooksPath .githooks`. CI enforces it regardless. |

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
