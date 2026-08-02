# AGENTS.md

Primary context file for **every** AI agent working in this repo (Claude Code, Copilot,
Cursor, Codex, or a human). Read this first. Everything else loads on demand.

---

## Why

A self-hosted Go daemon that starts and drives Claude Code sessions on the owner's own machine from anywhere. It serves an authenticated web dashboard and API under a `*.example.com` hostname, fronted by a Cloudflare Tunnel so no inbound port is ever opened, and spawns each session in a tmux window running with `--dangerously-skip-permissions`. Two clients, two front doors: a browser UI behind Cloudflare Access with Google as the identity provider (see all sessions in flight, create, destroy, rename, compact, and relay Claude's own device-code login when a session asks for it), and a companion Claude skill authenticating by HMAC signature. A request that passes auth causes unsandboxed code execution on the host — authentication, request integrity, and bounded blast radius are the product, not features bolted onto it.

---

## What — project map

| Path | What lives here |
|---|---|
| `cmd/crswd/` | Daemon entrypoint; flag parsing and wiring only |
| `internal/` | All real logic: `auth`, `session`, `tmuxctl`, `httpapi`, `config` |
| `internal/**/*_test.go` | Tests, colocated with the package they cover |
| `web/` | Templates, htmx, CSS — embedded via `go:embed`, no npm |
| `skill/` | The companion Claude skill that drives the daemon |
| `deploy/` | systemd unit + Cloudflare Tunnel config |
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
| Install | `go mod download` |
| Build | `go build ./...` |
| Test (all) | `go test ./...` |
| Test (single) | `go test ./internal/auth -run TestVerify` |
| Lint | `golangci-lint run` |
| Format | `gofmt -w . && goimports -w .` |
| Typecheck | `go vet ./...` |

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
- 2026-01-15 — Reaper skipped sessions with a nil pty; guard before Kill(). (#42)
```

**3. GitHub issue → automated PR.** Label an issue `claude-fix`. A runner picks it
up, works under these same rules, and opens a PR. It never pushes to `main`.

---

## Progressive disclosure — load only what the task needs

Do not read all of these. Read the one that matches what you are about to change.

| Touching… | Read |
|---|---|
| Signing, tokens, Google/Access login, session lifecycle | `docs/auth-and-sessions.md` |
| Request input, authz, secrets, routes, exposure, rendering pane output | `docs/security.md` |
| Layout, spacing, colour, any CSS | `docs/design-system.md` |
| A session card, status pill, pane viewer, action button | `docs/components.md` |
| Anything at all | this file |

**The security docs are binding, not advisory.** A request that passes auth is
unsandboxed code execution on the host. Read them before touching any handler.

---

## Conventions

**Naming** — standard Go. Packages lowercase and singular; no `util`/`common`.
```
package session       // files: manager.go, manager_test.go
func (m *Manager) Create(ctx context.Context, req CreateRequest) (*Session, error)
```

**Errors** — never swallow. Wrap with `%w` and context; return, do not log-and-continue.
```go
if err := m.tmux.Kill(ctx, s.Target()); err != nil {
    return fmt.Errorf("kill session %s: %w", s.ID, err)  // never a bare `return err`
}
```
Sentinel errors for conditions callers branch on (`ErrSessionNotFound`), checked with
`errors.Is`. Never put a secret, prompt, or pane content in an error string.

**Tests** — table-driven, `t.Parallel()`, no network, no real tmux (`tmuxctl` is an
interface). Every PR needs a test that fails without the change. Auth and session
code also needs the **negative** cases: bad signature, stale timestamp, replay, wrong owner.
```go
req := signedRequest(t, a, `{"cwd":"/repo"}`)
mustVerify(t, a, req)                            // first use passes
if _, err := a.Verify(clone(t, req)); err == nil {
    t.Fatal("replayed request was accepted")     // this is the whole point
}
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
