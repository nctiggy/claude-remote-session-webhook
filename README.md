# claude-remote-session-webhook

Start and drive Claude Code sessions on your own machine, from anywhere.

A self-hosted Go daemon (`crswd`) that serves a web dashboard and an HTTP API under
a `*.example.com` hostname. Each session runs in a tmux window with
`--dangerously-skip-permissions`. Two clients: a browser UI behind Cloudflare Access
(Google identity), and a companion Claude skill authenticating by HMAC signature.

> **Status: milestones 1 and 2 complete.** The daemon core — config, `tmuxctl`,
> session CRUD, HMAC auth, the audit log, and the reaper — ships alongside
> daemon-side Cloudflare Access validation and a read-only dashboard with live
> session output. Both doors are real: a browser is admitted by Google identity and
> then re-verified by the daemon, and the API client is admitted by an Access
> service token and then checked by signature, timestamp, replay and per-session
> token. Create, destroy, rename and compact from the browser are milestone 3; the
> device-code login relay is milestone 4.

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

## Configuration

The daemon is configured **only** by the environment, read once at startup before
it binds or spawns anything. There are no flags and no config file; `-h` reports
usage and nothing else. [`.env.example`](.env.example) carries the same list with
longer descriptions, and names only — never a value.

Anything that would weaken a bound is a **startup failure, not a warning**.
Sessions run with `--dangerously-skip-permissions`, so these variables are what
stands in for the permission prompt that is gone.

| Variable | Required | Default | Refuses to start when |
|---|---|---|---|
| `CRSW_SHARED_SECRET` | **yes** | — | unset, or shorter than 32 bytes |
| `CRSW_ALLOWED_ROOTS` | no | `$HOME/code`, with a loud banner | an entry is empty, relative, missing, unresolvable, or not a directory |
| `CRSW_LISTEN` | no | `127.0.0.1:8765` | the host is not a loopback IP literal, or the port is out of range |
| `CRSW_MAX_SESSIONS` | no | `5` | not a whole number, or below 1 |
| `CRSW_CREATE_RATE_PER_MIN` | no | `6` | not a whole number, or below 1 |
| `CRSW_MAX_BODY_BYTES` | no | `65536` | not a whole number, or below 1 |

Generate the secret with `openssl rand -hex 32`. It is never logged, never put in
an error string, and never echoed back — not even its length. Formatting a
`Config` redacts it under every verb, `%#v` included.

Notes worth having before you set these:

- **`CRSW_ALLOWED_ROOTS` is colon-separated**, like `PATH`, and every entry is
  resolved through its symlinks at startup so a root cannot be swapped between the
  check and the spawn. It is the real blast-radius control — keep it narrow. Unset
  is legal but announced on stderr at every start, and the default is `$HOME/code`
  rather than `$HOME`, which would put SSH keys and cloud credentials inside the
  allowlist.
- **`CRSW_LISTEN` will not take a hostname.** `localhost` is refused rather than
  resolved: `/etc/hosts` or a resolver could move the bind off loopback without
  this value changing. Reachability is the tunnel's job.
- **The create burst is derived, not configured.** The limiter is a per-caller
  token bucket filling at `CRSW_CREATE_RATE_PER_MIN` and holding `max(1, rate/2)`
  tokens — 3 at the default. There is no burst variable on purpose, since a second
  knob could be set in disagreement with the first. A create spends a token
  whatever it goes on to answer.
- **An oversize body is answered `401`, not `400`.** The signature covers bytes the
  daemon declined to read, so the request fails authentication before anything
  parses it — which is also why no part of it can reach the audit trail.
- **`HOME` matters only when `CRSW_ALLOWED_ROOTS` is unset**, and must then be an
  absolute path or startup fails.

Three limits are **constants in the code, not variables**: the idle timeout (60m),
the absolute session lifetime (24h, which is the session token's lifetime by
construction so the two cannot diverge), and the signed-request timestamp window
(300s in both directions). They bound the host; an environment file that could
widen them could unbound it.

This is milestone 1. Milestone 2 adds the Cloudflare Access variables — the AUD
tag, the team domain, and the allowed-email list — which the daemon does not read
yet.

---

## Deployment

The daemon runs as a **systemd user service**, with a Cloudflare Tunnel dialling out
beside it. Example files for both live in [`deploy/`](deploy/), and
[`deploy/README.md`](deploy/README.md) is the operator's page: what you supply, in
what order, and why three settings in the unit are load-bearing.

**Every file in `deploy/` is an example.** This repository is public, so no real
hostname, tunnel ID, Access AUD tag, allowed email, or path appears in it — those
come from the environment or 1Password at deploy time.

> **A public hostname needs an Access application in front of it, with two
> policies.** The daemon validates the assertion the edge forwards — pinned
> audience, pinned algorithm, and its own copy of the allowlist — so the edge and
> the daemon each get a say. But the daemon can only check an assertion the edge
> actually wrote: put a hostname in front of this without an Access application and
> layer 1 has nothing to verify, leaving HMAC alone between the internet and
> unsandboxed execution.
>
> The two policies are an identity policy for the browser and a **Service Auth**
> policy for the API client, on the same application. Access needs at least one
> Allow policy alongside Service Auth, or it never issues a usable assertion. See
> `deploy/README.md`.

```bash
go build -o ~/bin/crswd ./cmd/crswd
cp deploy/crswd.example.service ~/.config/systemd/user/crswd.service   # then edit
cp deploy/cloudflared.example.yml ~/.cloudflared/config.yml           # then edit

# The secret comes from 1Password, into a file outside the repo, mode 0600.
# EnvironmentFile parses NAME=value lines — write the assignment, not the secret.
mkdir -p ~/.config/crswd
( umask 077; printf 'CRSW_SHARED_SECRET=%s\n' \
    "$(op read 'op://Lobster/crswd/shared-secret')" > ~/.config/crswd/env )

loginctl enable-linger "$USER"          # or the unit stops when you log out
systemctl --user daemon-reload
systemctl --user enable --now crswd
```

`Environment=CRSW_SHARED_SECRET=` in the unit would be wrong even in a private
repo: anyone who can run `systemctl --user show crswd` can read a unit back.

### Reading the audit trail

Audit records are structured JSON on stdout, which makes the journal the entire
storage design — no file mode, no rotation, no disk to fill.

```bash
journalctl --user -u crswd -f -o cat | jq .
journalctl --user -u crswd -o cat | jq 'select(.action == "auth.reject")'
```

`-o cat` is what makes this work: it prints the message alone, without the syslog
prefix systemd would otherwise put in front of the JSON. No record carries prompt
text, pane output, a token, a token hash, or the shared secret — `internal/audit`
asserts that across every operation, so the journal is safe to read in a way a
pane never is.

### Verifying the exposure model

```bash
ss -tlnp | grep crswd                  # 127.0.0.1:PORT, never 0.0.0.0
curl -sS http://<host-lan-ip>:PORT/    # must fail to connect
```

If the second command reaches the daemon, stop and fix the bind address before
going any further.

## Licence

MIT — see [LICENSE](LICENSE).
