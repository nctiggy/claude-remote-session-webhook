# Deployment

**Nothing in this directory contains a real value.** Every file here is an example.
Real hostnames, tunnel IDs, account IDs, usernames, paths, and secrets are supplied
at deploy time from the environment or 1Password.

This repository is public. Treat every file in it as published, because it is.

> **Do not deploy milestone 1 behind a public hostname yet.** The daemon does not
> validate a Cloudflare Access JWT until milestone 2, so HMAC is the only check on
> the API, and Cloudflare Access is what keeps unauthenticated traffic off the box
> entirely. See "Not shippable before T037" in
> [`ralph/IMPLEMENTATION_PLAN.md`](../ralph/IMPLEMENTATION_PLAN.md).

## What you fill in yourself

| Value | Where it comes from | Never |
|---|---|---|
| Public hostname | Your own DNS zone | In this repo |
| `CRSW_SHARED_SECRET` | 1Password (`Lobster` vault) | In this repo, in a unit file, in a log |
| `CRSW_ALLOWED_ROOTS` | Directories sessions may run in | In this repo |
| Tunnel ID + credentials | `cloudflared tunnel create` | In this repo |
| Access AUD tag, team domain, allowed email | Cloudflare Access → application | In this repo |

The AUD tag and team domain are not secrets in the cryptographic sense, but they
identify your specific Access application. Keep them out of a public repo anyway —
there is no upside to publishing them.

**The daemon does not read the three Access values yet.** They are configured on
the Cloudflare side only; the variables that carry them into the daemon arrive with
milestone 2. Setting them in the unit file today does nothing and tells you
nothing, which is the failure mode this table used to have.

Everything the daemon *does* read is named in [`.env.example`](../.env.example),
with what each value refuses to start on. `CRSW_SHARED_SECRET` is required; the
rest have defaults, and `crswd.example.service` sets each of them to its own
default so the list is visible in one place.

## Order of operations

1. `cloudflared tunnel create crswd` — note the tunnel ID and credentials path
2. Create the Cloudflare Access application, Google IdP, allowlist your one email
3. Copy the AUD tag from the application's settings
4. Put `CRSW_SHARED_SECRET` in 1Password; generate it with `openssl rand -hex 32`
5. Copy both example files, fill them in **outside the repo**, install them
6. `loginctl enable-linger "$USER"` — without it the unit stops at logout
7. `systemctl --user enable --now crswd` then `systemctl --user enable --now cloudflared`

The secret reaches the daemon through an `EnvironmentFile` outside the repo, never
through the unit. `EnvironmentFile` parses `NAME=value` lines, so write the
assignment rather than the bare secret:

```bash
mkdir -p ~/.config/crswd
( umask 077; printf 'CRSW_SHARED_SECRET=%s\n' \
    "$(op read 'op://Lobster/crswd/shared-secret')" > ~/.config/crswd/env )
```

`Environment=` in a unit is the wrong place for it regardless of this repo:
anyone who can run `systemctl --user show crswd` can read it back.

### The Access service token

Cloudflare Access sits in front of the hostname and refuses the API client as
readily as a stranger, so the client presents a **service token** at the edge —
two headers, alongside the signature it already sends. The daemon never reads
them; the edge does, and layers 2 and 3 are still enforced behind it.

The same 1Password item carries all three values, so one item rebuilds the whole
deployment:

```bash
op read 'op://Lobster/crswd/shared-secret'          # the daemon's HMAC key
op read 'op://Lobster/crswd/access-client-id'       # CF-Access-Client-Id
op read 'op://Lobster/crswd/access-client-secret'   # CF-Access-Client-Secret
```

A client call therefore carries four headers: the two Access ones the edge
consumes, and the timestamp and signature the daemon checks. Dropping the Access
pair gets a 302 to the login page and the daemon is never reached; dropping the
signature gets past the edge and a uniform 401 from the daemon. Both are worth
trying once — the second is the layering doing its job, and a deployment where it
returns 200 is misconfigured.

The client id is not a secret. The client secret is shown **once**, when the token
is created, and is unrecoverable afterwards — regenerate the token if it is lost.
The shared secret is worse: nothing can regenerate it, and losing it invalidates
every live session token. It exists in exactly two places, 1Password and
`~/.config/crswd/env`, and that is deliberate.

## Reading the audit trail

Audit records are structured JSON on stdout, so systemd's journal is the whole
storage design — there is no file mode, no rotation, and no disk to fill.

```bash
journalctl --user -u crswd -f -o cat | jq .          # follow, one record per line
journalctl --user -u crswd -o cat | jq 'select(.action == "auth.reject")'
journalctl --user -u crswd --since "1 hour ago" -o cat | jq -r '.action' | sort | uniq -c
```

`-o cat` is what makes this work: it prints the message alone, without the syslog
prefix systemd would otherwise put in front of the JSON.

No record carries prompt text, pane output, a token, a token hash, or the shared
secret — `internal/audit/leak_test.go` asserts that across every operation. The
journal is safe to read over someone's shoulder; a pane is not.

## Why the unit looks the way it does

Three settings in `crswd.example.service` are load-bearing and easy to "fix" into
breakage:

- **`KillMode=process`.** A tmux server this daemon starts is in the unit's
  cgroup, so the default would kill every session on any restart — the sessions
  `Manager.Adopt` exists to recover. Teardown is the daemon's verified teardown,
  not systemd's approximation.
- **`TimeoutStopSec=45s`.** Longer than the daemon's own 30s shutdown budget, so
  the daemon's deadline is the one that fires. A SIGKILL part-way through teardown
  is the ending that leaves unsandboxed shells running.
- **No `PrivateTmp=`, no `ProtectHome=`.** tmux's socket lives under `/tmp` and
  Claude Code writes throughout `$HOME`. Both options break the tool instead of
  bounding it, and the unit says so inline so the next person does not re-add
  them.

The hardening that *is* there protects the host from the daemon. It does not
sandbox a session: sessions run `claude --dangerously-skip-permissions` as you,
so anything the daemon can reach, a session can reach. `CRSW_ALLOWED_ROOTS` is the
real control.

## Verifying the exposure model

After deploying, confirm the daemon is not reachable except through the tunnel:

```bash
ss -tlnp | grep crswd          # must show 127.0.0.1:PORT, never 0.0.0.0
curl -sS http://<host-lan-ip>:PORT/   # must fail to connect
```

If the second command reaches the daemon, stop and fix the bind address before
going any further. See `docs/security.md`.
