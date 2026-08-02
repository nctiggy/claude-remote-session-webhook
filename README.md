# claude-remote-session-webhook

Start and drive Claude Code sessions on your own machine, from anywhere.

A self-hosted Go daemon (`crswd`) that serves a web dashboard and an HTTP API under
a `*.example.com` hostname. Each session runs in a tmux window with
`--dangerously-skip-permissions`. Two clients: a browser UI behind Cloudflare Access
(Google identity), and a companion Claude skill authenticating by HMAC signature.

> **Status: scaffolded, no implementation yet.** The contract (`AGENTS.md`), the
> constitution, and the security docs are written. Code starts at milestone 1.

---

## What it does

- See every session in flight, with live pane output
- Create, destroy, and rename sessions
- Compact a session (`/compact` sent into the pane)
- Relay Claude's own device-code login when a session asks for it
- Drive all of the above from a Claude skill instead of the browser

## Why it is built this way

**tmux, not bare subprocesses.** Sessions survive a daemon restart, you can attach
by hand to debug, and `send-keys` / `capture-pane` are what make `/compact` and the
device-code login relay possible at all.

**Cloudflare Tunnel, not an open port.** The daemon binds `127.0.0.1` only. The
tunnel connects outbound; nothing inbound is ever opened on the host or the router.

**Cloudflare Access, not an in-daemon OAuth flow.** Google login and the one-email
allowlist are enforced at the edge, so unauthenticated traffic never reaches the
box. The daemon just validates the signed JWT — tens of lines instead of hundreds.

**Go templates + htmx, not an SPA.** Single static binary via `go:embed`. No npm,
no second toolchain, and SSE is a natural fit for tailing pane output.

## The security posture, stated plainly

A request that passes authentication is **arbitrary code execution as the daemon's
user**. There is no sandbox behind the auth check.

That is the whole point of the tool and also its entire risk. It is why
[`docs/security.md`](docs/security.md) and
[`docs/auth-and-sessions.md`](docs/auth-and-sessions.md) are binding documents
rather than advice, and why the constitution has a principle dedicated to keeping
the blast radius bounded. Read both before touching a handler.

---

## Roadmap

Each milestone is planned and run separately — one Ralph loop per milestone, not
one loop for the lot.

| # | Milestone | Contents |
|---|---|---|
| 1 | Daemon core | config, `tmuxctl`, session CRUD, HMAC auth, audit log, reaper. No UI. |
| 2 | Read-only dashboard | Access JWT validation, session list, live pane via SSE |
| 3 | Dashboard actions | create, destroy, rename, compact |
| 4 | Claude login relay | detect device-code prompt, surface URL, relay code back |

## Working in this repo

Read [`AGENTS.md`](AGENTS.md) first — it is the contract for humans and agents
alike, and it names which `docs/` file to load for a given change.

```bash
git config core.hooksPath .githooks   # once per clone — gitleaks pre-commit
cp .env.example .env                  # names only; fill in locally, never commit

go mod download
go build ./...
go test ./...
golangci-lint run
```

### Planning a milestone

```
/prd                    # write the PRD
/prd-critic-loop        # Staff Engineer / SRE / Security passes
```

Then convert the milestone's tasks into `ralph/IMPLEMENTATION_PLAN.md` as a
checklist. Spec Kit (`/speckit-specify` → `/speckit-plan` → `/speckit-tasks`) is
the alternative route and is wired into this repo.

> **Note:** `loop.sh` reads `ralph/IMPLEMENTATION_PLAN.md` (markdown checklist).
> It does **not** read snarktank `prd.json` — that is a different Ralph.

### Running a loop

```bash
git switch -c feat/milestone-1     # the loop refuses to run on main
./ralph/loop.sh 5                  # cap at 5 iterations
```

Run `claude -p "$(cat ralph/PROMPT.md)"` by hand two or three times first and watch
what it does. Wrap it in the loop only once the behaviour is boring.

---

## Deployment

Example systemd user unit and `cloudflared` config live in [`deploy/`](deploy/).

**Every file there is an example.** This repository is public, so no real hostname,
tunnel ID, Access AUD tag, allowed email, or path appears in it — those come from
the environment or 1Password at deploy time. `deploy/README.md` lists exactly what
you supply and in what order, including how to verify the daemon really is bound to
loopback once it is running.

## Licence

MIT — see [LICENSE](LICENSE).
