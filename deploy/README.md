# Deployment

**Nothing in this directory contains a real value.** Every file here is an example.
Real hostnames, tunnel IDs, account IDs, usernames, paths, and secrets are supplied
at deploy time from the environment or 1Password.

This repository is public. Treat every file in it as published, because it is.

> **The daemon validates the Cloudflare Access assertion itself.** Every dashboard
> route verifies the forwarded `Cf-Access-Jwt-Assertion` against the team domain's
> key set, the AUD tag, and the allowlist before it renders anything; the API door
> is unchanged and still checks the HMAC signature. Access at the edge stays in
> front of both — no path gets an edge bypass — and the three `CRSW_ACCESS_*`
> values below are what the daemon checks against. It refuses to start without them.

## What you fill in yourself

| Value | Where it comes from | Never |
|---|---|---|
| Public hostname | Your own DNS zone | In this repo |
| `CRSW_SHARED_SECRET` | 1Password (`Lobster` vault) | In this repo, in a unit file, in a log |
| `CRSW_ALLOWED_ROOTS` | Directories sessions may run in | In this repo |
| Tunnel ID + credentials | `cloudflared tunnel create` | In this repo |
| `CRSW_ACCESS_TEAM_DOMAIN` | Your Access team domain, normally `<team>.cloudflareaccess.com` | In this repo |
| `CRSW_ACCESS_AUD` | Cloudflare Access → application → the AUD tag in its settings | In this repo |
| `CRSW_ACCESS_ALLOWED_EMAILS` | The addresses your Access identity policy admits | In this repo |

The AUD tag and team domain are not secrets in the cryptographic sense, but they
identify your specific Access application, and the allowlist names a person. Keep
all three out of a public repo anyway — there is no upside to publishing them.

**The three are all-or-nothing, and this deployment needs all three.** They are
what the daemon checks a browser's assertion against: the team domain fixes both
the expected issuer and the key set the signature is verified with, the AUD tag
pins the one Access application, and the allowlist is the daemon's own copy of the
list the edge enforces — the edge is the gate, and this is the daemon asserting the
gate is configured as believed. Setting *some* of them is a startup failure: a
half-configured door would refuse every login while looking correctly configured.
Setting none is a supported deployment — the API works and the dashboard admits
nobody — and the daemon says so in a banner at every start, so a dashboard that
turns out to admit nobody is never a surprise found from the browser.

Everything the daemon reads is named in [`.env.example`](../.env.example), with
what each value refuses to start on. `CRSW_SHARED_SECRET` is required outright and
the three `CRSW_ACCESS_*` above are required by this deployment; the rest have
defaults, which `crswd.example.service` sets to their own default value so the
whole list is visible in one place. Among them is `CRSW_MAX_STREAMS` (default 10):
how many live output streams the dashboard may hold open at once, across every tab.
It is a second cap of the same kind as `CRSW_MAX_SESSIONS` — past it an open is
refused with 429 rather than the host quietly degrading.

### Or configure it in a file

Every one of those settings can be written in `~/.config/crswd/config` instead of
the environment, one `key = value` per line, `#` comments — the key is the variable
minus `CRSW_`, lower-cased. [`config.example`](../config.example) is the annotated
copy to start from, and `crswd config check` reports what a file makes without
starting anything.

**The environment still wins.** Precedence is flag, then environment, then file,
then default — so this unit's `Environment=` lines and its `EnvironmentFile` both
override a file on the host, and a stale file cannot silently take over a
deployment. `GET /settings` names which layer supplied each value, which is the
question worth answering when an edit appears to have done nothing.

A file holding `shared_secret` or `access_allowed_emails` **must be mode 0600** or
the daemon refuses to start. The recipe below writes the `EnvironmentFile` under
`umask 077` for the same reason, and either route is fine — what is not fine is a
credential in the unit itself.

## Order of operations

1. `cloudflared tunnel create crswd` — note the tunnel ID and credentials path
2. Create the Cloudflare Access application, Google IdP, allowlist your one email —
   that address is also `CRSW_ACCESS_ALLOWED_EMAILS`, and your team domain is
   `CRSW_ACCESS_TEAM_DOMAIN`
3. Copy the AUD tag from the application's settings — it is `CRSW_ACCESS_AUD`
4. Put `CRSW_SHARED_SECRET` in 1Password; generate it with `openssl rand -hex 32`
5. Copy both example files, fill them in **outside the repo**, install them
6. `loginctl enable-linger "$USER"` — without it the unit stops at logout
7. `systemctl --user enable --now crswd` then `systemctl --user enable --now cloudflared`

**Four values** reach the daemon through an `EnvironmentFile` outside the repo,
never through the unit. `EnvironmentFile` parses `NAME=value` lines, so write the
assignments rather than the bare values:

```bash
mkdir -p ~/.config/crswd
( umask 077
  printf 'CRSW_SHARED_SECRET=%s\n'         "$(op read 'op://Lobster/crswd/shared-secret')"
  printf 'CRSW_ACCESS_TEAM_DOMAIN=%s\n'    '<team>.cloudflareaccess.com'
  printf 'CRSW_ACCESS_AUD=%s\n'            '<the Access application AUD tag>'
  printf 'CRSW_ACCESS_ALLOWED_EMAILS=%s\n' '<you@example.com>'
) > ~/.config/crswd/env
```

Writing only the secret gets a daemon that refuses to start rather than one
running with an unchecked browser door, and the startup error names the variable
it is missing.

`Environment=` in a unit is the wrong place for any of them regardless of this
repo: anyone who can run `systemctl --user show crswd` can read them back. The
secret is the obvious case; the other three name a Cloudflare team, an
application, and a person.

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

The `CRSW_ACCESS_*` variables are the other half of that split, and no value is
shared between them: the edge reads the service token and the daemon never sees
it, while the daemon reads those three and the edge never sees them. The two
assertion shapes are not interchangeable either — a service token's carries
`common_name`, an empty `sub`, and **no email**, so presenting it to the dashboard
is refused exactly as a stranger's is.

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

## Driving the API from a shell

`deploy/crswd-api` is the whole client: four headers and one signature line. It
runs on macOS or Linux, called from bash or zsh, and reads all three credentials
from 1Password at call time so none of them lives in a file or a history entry.

```bash
install -m 0755 deploy/crswd-api ~/bin/crswd-api

crswd-api GET    /sessions
crswd-api POST   /sessions '{"name":"demo","work_dir":"'"$HOME"'/code/some-repo"}'
crswd-api POST   /sessions/<id>/prompt '{"text":"run the tests"}' <token>
crswd-api GET    /sessions/<id>/output '' <token>
crswd-api DELETE /sessions/<id>        '' <token>
```

`CRSWD_HOST` and `CRSWD_OP_ITEM` override the hostname and the vault item.

Three portability hazards it exists to avoid, each of which cost real time:

- **The request path is `reqpath`, never `path`.** In zsh, `path` is tied to
  `PATH`; a function doing `local path=/sessions` sets `PATH=/sessions` for the
  length of the call, and every external command disappears. The symptom —
  `command not found: date` — looks nothing like the cause.
- **`PATH` is appended to, never prepended.** Prepending `/usr/bin` would shadow
  Homebrew's tools on a Mac.
- **A script, not a shell function.** The shebang fixes the interpreter, so the
  caller's shell cannot change how it parses.

Running it **on the daemon's host** is the better default. It reads the HMAC
shared secret, and that is the one credential that cannot be regenerated without
invalidating every live session token — running it elsewhere puts a second copy
of it on a second machine.

## Reading the audit trail

Audit records are structured JSON on stdout, so systemd's journal is the whole
storage design — there is no file mode, no rotation, and no disk to fill.

```bash
journalctl --user -u crswd -f -o cat | grep '^{' | jq .   # follow, one record per line
journalctl --user -u crswd -o cat | grep '^{' | jq 'select(.action == "auth.reject")'
journalctl --user -u crswd --since "1 hour ago" -o cat | grep '^{' | jq -r '.action' | sort | uniq -c
```

`-o cat` is what makes this work: it prints the message alone, without the syslog
prefix systemd would otherwise put in front of the JSON.

**The `grep '^{'` is not optional, and the third command is why.** Audit records
go to stdout and the daemon's own diagnostics go to stderr, but **systemd merges
both file descriptors into one journal** — so `journalctl` returns them
interleaved no matter how cleanly the daemon separated them. Without the filter
the first two fail outright on a `jq: parse error`, and the third would have
silently under-counted every action it was asked to tally. `_COMM=crswd` does not
help: the non-JSON lines are the daemon's own, which is how #88's cause was
identified.

The same command is in `crswd.example.service`, and the quickstart suite runs
**every** `journalctl` line this repository documents — that unit and both
READMEs — against a real captured stream carrying real diagnostics. A command
here that stopped working stops the build rather than an operator's afternoon.

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

### The unit's PATH is not the session's, and the daemon knows it

A start command is typed into a login shell inside a tmux pane, so it is resolved
against the `PATH` **your profile** builds — not against the one systemd hands
the unit. `claude` installed at `~/.local/bin/claude` is on the first and absent
from the second, which is why the startup probe used to warn that a working
command was missing.

The probe now asks the login shell. If a configured start command is not on the
daemon's own `PATH`, it runs `$SHELL -l` **once per start**, gives it one
constant line on stdin (`printf '%s\n' "$PATH"`), and resolves the binary against
what comes back. It never names the command to the shell.

Two consequences for a deployment:

- **Your `~/.profile` runs at daemon startup**, before anything binds, on a host
  where a command is already missing from the unit's `PATH`. It is bounded by a
  5s timeout and a 1s wait delay, so a profile that blocks costs a start five
  seconds and cannot hang one. A profile with a side effect you would not want on
  every `systemctl --user restart crswd` is worth knowing about.
- **A shell that cannot be asked is a note, not a warning.** The daemon says what
  it checked and what it could not, and never that the command is absent —
  because a check that is wrong about a working host teaches an operator to stop
  reading it.

Setting `Environment=PATH=` in the unit to include `~/.local/bin` is a reasonable
thing to do and changes only what the probe finds first. It does not change what
a session resolves, which is the login shell's answer either way.

## Verifying the exposure model

After deploying, confirm the daemon is not reachable except through the tunnel:

```bash
ss -tlnp | grep crswd          # must show 127.0.0.1:PORT, never 0.0.0.0
curl -sS http://<host-lan-ip>:PORT/   # must fail to connect
```

If the second command reaches the daemon, stop and fix the bind address before
going any further. See `docs/security.md`.

### Edge admission (SC-014)

The one check that cannot be made off the deployment. Everything else about
milestone 2 is provable on a laptop against a local key server — this is not,
because the thing under test is the edge itself. Confirm it once the hostname is
live, and again after any change to the Access application:

```bash
curl -sS -o /dev/null -w '%{http_code}\n' https://<host>/            # 302 to the IdP, never 200
curl -sS -o /dev/null -w '%{http_code}\n' https://<host>/sessions    # refused at the edge as well
```

Every path must refuse at the edge without either a browser identity or the two
service-token headers, and the API client must gain **those two headers and
nothing else** (FR-014a): its signing procedure is unchanged, and the daemon
never sees the service token.
