# Deployment

**This page is one of the two deployments: the daemon on the internet, behind a
Cloudflare Tunnel with Access in front of the hostname.** Everything below assumes
it.

**On a network you control, you want the other one** — no tunnel, no Access
application, no hostname. The browser door there is `CRSW_DASHBOARD_PASSWORD` and a
sign-in form the daemon serves itself, and the whole configuration is four lines;
[`README.md`](../README.md) has it under *On your own network*. Three sections here
still apply to it — **Or configure it in a file**, **Why the unit looks the way it
does** (it is the same unit), and **Reading the audit trail**. The rest is
Cloudflare, and none of it is.

**Nothing in this directory contains a real value.** Every file here is an example.
Real hostnames, tunnel IDs, account IDs, usernames, paths, and secrets are supplied
at deploy time from the environment or a secret manager.

This repository is public. Treat every file in it as published, because it is.

> **The daemon validates the Cloudflare Access assertion itself.** Every dashboard
> route verifies the forwarded `Cf-Access-Jwt-Assertion` against the team domain's
> key set, the AUD tag, and the allowlist before it renders anything; the API door
> is unchanged and still checks the HMAC signature. Access at the edge stays in
> front of both — no path gets an edge bypass — and the three `CRSW_ACCESS_*`
> values below are what the daemon checks against. **This deployment sets all
> three.** What the daemon enforces on them is all-or-nothing, not required — the
> next section is what each one is and what setting none of them gets you.

## What you fill in yourself

| Value | Where it comes from | Never |
|---|---|---|
| Public hostname | Your own DNS zone | In this repo |
| `CRSW_SHARED_SECRET` | `openssl rand -hex 32`, kept in whatever secret manager you already use | In this repo, in a unit file, in a log |
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

**Which means forgetting all three is not a startup failure, and on this deployment
that is the mistake worth making fatal.** `CRSW_ACCESS_ENABLED=true` is the operator
saying out loud that Access is the browser door: with it set and the three absent,
the daemon refuses to start instead of serving the API beside a dashboard nobody can
enter. The unit already carries the line, empty — fill it in on your copy. It is not
required for the Access door (the three select it on their own, as they did before
the variable existed), and it is refused beside `CRSW_DASHBOARD_PASSWORD`, because
two configured doors is the one question a daemon must not answer by guessing.

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
4. Generate `CRSW_SHARED_SECRET` with `openssl rand -hex 32` and put it wherever
   you keep secrets
5. Copy both example files, fill them in **outside the repo**, install them
6. `loginctl enable-linger "$USER"` — without it the unit stops at logout
7. `systemctl --user enable --now crswd` then `systemctl --user enable --now cloudflared`

**Four values** reach the daemon through an `EnvironmentFile` outside the repo,
never through the unit. That is the shape, and it is the whole of it: a credential
comes out of a secret manager at deploy time, lands in a file written under
`umask 077` outside the repository, and never becomes an `Environment=` line.
`EnvironmentFile` parses `NAME=value` lines, so write the assignments rather than
the bare values:

```bash
mkdir -p ~/.config/crswd
( umask 077
  printf 'CRSW_SHARED_SECRET=%s\n'         "$(op read 'op://Lobster/crswd/shared-secret')"
  printf 'CRSW_ACCESS_TEAM_DOMAIN=%s\n'    '<team>.cloudflareaccess.com'
  printf 'CRSW_ACCESS_AUD=%s\n'            '<the Access application AUD tag>'
  printf 'CRSW_ACCESS_ALLOWED_EMAILS=%s\n' '<you@example.com>'
) > ~/.config/crswd/env
```

**`op read` is one spelling of "out of a secret manager", not the procedure.** This
deployment happens to use 1Password; `pass show`, `gopass`, `vault kv get`, a
`systemd-creds` decrypt, or pasting the value in by hand produce the same file, and
nothing in the daemon knows which you used. What is not interchangeable is the other
two rules — a secret committed to the repo or set in the unit is the same secret
either way.

Writing only the secret gets a daemon that **starts**, serves the API, and admits
nobody to the dashboard — the banner at every start says exactly that. What refuses
to start is writing one or two of the three, and that error names them. Making the
first case a refusal as well is what `CRSW_ACCESS_ENABLED=true` is for, above.

`Environment=` in a unit is the wrong place for any of the four regardless of this
repo: anyone who can run `systemctl --user show crswd` can read them back. The
secret is the obvious case; the other three name a Cloudflare team, an
application, and a person.

### The Access service token

Cloudflare Access sits in front of the hostname and refuses the API client as
readily as a stranger, so the client presents a **service token** at the edge —
two headers, alongside the signature it already sends. The daemon never reads
them; the edge does, and layers 2 and 3 are still enforced behind it.

**Keep all three in one place, so one item rebuilds the whole deployment.** Here
that is a single 1Password item — the shape is "one entry, three fields", and any
manager does it:

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
every live session token. It exists in exactly two places — your manager and
`~/.config/crswd/env` — and that is deliberate.

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

`CRSWD_HOST` and `CRSWD_OP_ITEM` override the hostname and the vault item. **This
is the one place 1Password is more than an example**: the script shells out to `op`,
in three lines near the top. Another manager is those three lines changed — what the
rest of it does with the values is the same either way.

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

### What an update does to this file

**An update never replaces a unit this daemon did not write** — and the three
settings above are why. They are decisions somebody makes once, and a release that
overwrote units would undo them every time and make each one a rediscovery. The
rule is install.sh's and it has held since it shipped; what is new is the other
half of it, because refusing quietly meant a host two fixes behind looked exactly
like a current one.

So an update does one of four things with
`~/.config/systemd/user/crswd.service`, decided by the digest recorded at
`~/.local/share/crswd/crswd.service.sha256`: replaces the unit and re-records it
while that digest still describes the file; installs and records one where there is
none; does nothing at all when the file is already the release's own, byte for
byte; and otherwise — **you edited it, or you wrote it** — leaves it untouched and
puts the release's unit beside it as `crswd.service.new`. The table is in
[`README.md`](../README.md#updating-and-rolling-back).

**A hand-written unit has no record, so it is never replaced and is offered a
`.new` by every release that ships a different one.** Every host deployed before
the installer existed is in that state. It is the intended ending rather than a
state to be cleared.

**Taking one is a copy, a reload and a restart**, after the `diff` the daemon
prints for you:

```bash
diff ~/.config/systemd/user/crswd.service ~/.config/systemd/user/crswd.service.new
cp ~/.config/systemd/user/crswd.service.new ~/.config/systemd/user/crswd.service
systemctl --user daemon-reload
systemctl --user restart crswd
```

**What that does not do is hand the file over.** Copying writes no record, so the
unit is still yours: the next release that changes it offers a `.new` again, which
is the only honest answer about a file this daemon has no evidence it wrote. If you
would rather updates carried the unit for you, say so by recording it — and read
the sentence after the command before you run it:

```bash
mkdir -p ~/.local/share/crswd
sha256sum < ~/.config/systemd/user/crswd.service | cut -d' ' -f1 \
  > ~/.local/share/crswd/crswd.service.sha256
```

The digest alone and a newline, with no filename — that is the format install.sh
writes and the only one this daemon reads back. From then on it reads that unit as
its own and **replaces it on every update, with no `.new` and no diff to read
first**, so record only a unit you have not edited and do not intend to. Deleting
the record puts the file back in your hands and back on the last case above.

**Where your unit stands is said in two places, and they are one read of the same
two files**: on `/settings` under *Updates*, and in the journal at every start.
Neither is fresher than the other, so read whichever you have — the journal on a
host nobody browses, the page otherwise. **The one difference is a read that
failed.** Naming a path on this disk and the reason it could not be read is a
diagnostic for whoever administers the host rather than something a browser is
owed, so the page says only that it happened and the journal says why. The daemon's
own lines are the ones prefixed `crswd: `, beside its other startup banners in
`journalctl --user -u crswd -e`.

### Needing `sudo` in a session, without giving up the unit

The section above is the trap this one exists to stop you walking into. Hardening
lives in systemd's file, not in the daemon's configuration — there is no
`allowed_roots`-style setting that could ever express `NoNewPrivileges` — so an
operator who needed `sudo` inside a session had exactly one place to put that
change, and editing the unit costs it every future update, permanently.

**Put it in a drop-in instead.** systemd merges `<unit>.d/*.conf` over the unit,
and nothing in `install.sh` or the updater ever touches that directory. Your unit
then stays byte-identical to the release's — replaceable, reported as current —
and your deviation survives every update.

`install.sh` asks about this at install time and writes the file if you say yes.
To add it later, or on a host installed before the question existed:

```bash
mkdir -p ~/.config/systemd/user/crswd.service.d
cp deploy/crswd.service.d/10-relax.conf.example \
   ~/.config/systemd/user/crswd.service.d/10-relax.conf
systemctl --user daemon-reload
systemctl --user restart crswd
```

**`ProtectKernelTunables=false` is the line that does the work, and dropping it
gives you a file that changes nothing.** Measured on a real host:

| Merged settings | Effective `NoNewPrivs` |
|---|---|
| `ProtectKernelTunables=true` | `1` |
| `ProtectKernelTunables=true` + `NoNewPrivileges=false` | **`1`** |
| `ProtectKernelTunables` overridden to `false` | `0` |

`ProtectKernelTunables=true` *implies* `NoNewPrivileges`, and systemd treats that
as a floor rather than a value: an explicit `no` in the merged unit does not lower
it back. Relax the obvious setting alone and `sudo` still fails, with nothing in
either file that looks like the cause.

**What you are granting**: a path from an authenticated request to **root on this
host**, not just to your account. `allowed_roots` does not bound it — that bounds
which directory a session starts in, and a root shell is not bounded by its
working directory. Delete the file, `daemon-reload` and restart to take it back.

Check what is actually in effect rather than what the files suggest:

```bash
systemctl --user show crswd -p NoNewPrivileges -p ProtectKernelTunables
```

### Moving a hand-edited unit back onto the supported path

If you edited your unit before drop-ins existed, this is the way home. It ends
with a unit the daemon reports as its own and your deviations intact.

```bash
# 1. What did you actually change? This is the whole of the decision.
diff deploy/crswd.example.service ~/.config/systemd/user/crswd.service

# 2. Hardening differences go in the drop-in (previous section).
#    Anything else -- an ExecStart path, an Environment= line -- belongs in
#    ~/.config/crswd/config instead, which the unit no longer overrides.

# 3. If your ExecStart names a different binary path, move the binary first.
#    The shipped unit runs %h/.local/bin/crswd.
mkdir -p ~/.local/bin && cp ~/bin/crswd ~/.local/bin/crswd

# 4. Take the release's unit.
cp ~/.config/systemd/user/crswd.service ~/crswd.service.backup
cp deploy/crswd.example.service ~/.config/systemd/user/crswd.service

# 5. Hand it over, so updates carry it from now on.
mkdir -p ~/.local/share/crswd
sha256sum < ~/.config/systemd/user/crswd.service | cut -d' ' -f1 \
  > ~/.local/share/crswd/crswd.service.sha256

systemctl --user daemon-reload
systemctl --user restart crswd
```

Step 5 is the one that changes what future updates do, and the sentence about
recording a digest two sections up applies in full: from then on the unit is
replaced on every update with no `.new` and no diff first. That is safe here
precisely because step 2 moved everything of yours out of it.

### After upgrading, recreate your sessions

A session no longer inherits the daemon's environment — the shared secret, the
Access values and every `CRSW_` setting stay on the daemon's side of the
boundary. The daemon also clears them out of the tmux server at startup, so
sessions created from then on are clean.

**Sessions that were already running are not, and cannot be.** A process's
environment cannot be changed from outside it, so a pane started by an older
build keeps what it was given until it is recreated. Nothing on the host will
tell you those panes are still holding it, because nothing can.

```bash
# From inside a session, check what it actually has:
env | grep -c '^CRSW_'      # 0 on a session created after the upgrade
```

If that returns anything other than `0`, destroy the session and create a new
one. And if it ever held `CRSW_SHARED_SECRET`, treat that secret as disclosed:
whatever ran in the pane could read it, and `crswd keygen` is how you replace it.

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
