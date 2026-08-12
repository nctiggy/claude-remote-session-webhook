# claude-remote-session-webhook

Start and drive Claude Code sessions on your own machine, from anywhere.

A self-hosted Go daemon (`crswd`) that serves a web dashboard and an HTTP API.
Each session runs in a tmux window with `--dangerously-skip-permissions`, so it
survives a daemon restart — the daemon adopts what it finds — and can be attached
to by hand. Two clients: a browser, and a script signing its requests with HMAC.

The browser is admitted by **one of two doors, chosen in the configuration and
never both at once** — Cloudflare Access when the daemon is on the internet under
a `*.example.com` hostname, or a **dashboard password** when it is on a network
you control and there is no Cloudflare in front of it. Which one you want, and
what each costs you, is [The two doors](#the-two-doors).

## What it does

- See every session in flight, with live pane output
- Create, destroy, rename and compact a session
- Switch a running session between its plain and remote-control modes
- Read the whole configuration on `/settings` — every value and where it came from
- Edit one non-secret setting, update the daemon to a signed release, and restart
  it, from that same page
- Drive the sessions from a script instead of a browser, over a
  [signed HTTP API](#the-api-door)

**Everything works with scripting switched off.** Every action is a plain form
post; the live pane and the fleet updates are the enhancement, not the mechanism.
The pages are built for a phone as well as a desktop.

**Two things are not built yet**, and are named here so nobody goes looking for
them: relaying Claude's own device-code login when a session asks for it, and the
companion Claude skill that would drive the API. Everything else on this page
describes what the daemon does today.

## The security posture, stated plainly

A request that passes authentication is **arbitrary code execution as the daemon's
user**. There is no sandbox behind the auth check: what it starts for you is an
assistant with the permission prompt turned off, on your machine, in a directory
you approved.

That is the whole point of the tool and also its entire risk. It is why the door
below is the product rather than a feature, why
[`docs/security.md`](docs/security.md) and
[`docs/auth-and-sessions.md`](docs/auth-and-sessions.md) are binding documents
rather than advice, and why the constitution has a principle dedicated to keeping
the blast radius bounded. Read both before touching a handler.

What bounds the damage, once somebody is through the door, is configuration:
sessions may only run under [allowlisted roots](#configuration), there is a cap on
how many exist at once, and every one of them has an idle timeout and an absolute
lifetime enforced by a reaper — deadlines a create can switch off only as far as
[the daemon's own configuration](#configuration) already allows.

## Install

**There are two deployments, and choosing between them comes before anything you
type.** Both run the same installer and the same daemon; what differs is which
door the dashboard has, and therefore where the daemon is allowed to listen.

1. **On the internet** — a Cloudflare Tunnel dials out and **Cloudflare Access**
   decides who reaches it. Needs a Cloudflare account with a domain on it, and
   `cloudflared` on this host.
   → [Path 1](#path-1--on-the-internet-cloudflare-tunnel-and-access)
2. **On a network you control** — a **dashboard password**, and the daemon
   listening on a LAN address. Needs nothing you do not already have, and gives
   you no TLS unless you put a reverse proxy in front of it.
   → [Path 2](#path-2--on-your-own-network-the-dashboard-password)

What each one costs you is [The two doors](#the-two-doors) — read that first if
the choice is not obvious. You can install before you decide: a daemon with
neither door runs, serves the API, and admits nobody to the dashboard.

**What the host needs, either way:**

- **Linux with a systemd user session.** Everything here is a `systemctl --user`
  unit, running as you and not as root.
- **`tmux`.** Every session is a tmux window, so the installer refuses a host
  without it and so does the daemon at startup.
- **`claude`, installed and already signed in**, as the user the daemon will run
  as. Run it once in a terminal and finish its login first. **Relaying Claude's
  own device-code login is not built**: a session that comes up at that prompt
  sits there, and the only way to answer it is attaching to the tmux window by
  hand on the host — which is the thing you installed this to avoid.

Then, on that host, whichever path you picked:

```bash
curl -fsSL https://raw.githubusercontent.com/nctiggy/claude-remote-session-webhook/main/install.sh | bash
```

One command. No clone, no compiler, no package manager. It downloads the latest
release, verifies the ed25519 signature over `SHA256SUMS` and then each file's
checksum — **in that order, and before anything it downloaded is executable** —
and then writes four things:

| | |
|---|---|
| `~/.local/bin/crswd` | the binary |
| `~/.config/systemd/user/crswd.service` | the unit |
| `~/.local/share/crswd/crswd.service.sha256` | a record of the unit it wrote |
| `~/.config/crswd/config` | a starter configuration, **only if there is none** |

That configuration is complete enough to start: it carries a generated
`shared_secret` and an `allowed_roots` pointing at a directory the installer
created. What it does not carry is a **door** — neither can be invented for you,
one being a credential and the other a Cloudflare account — so until you choose
one the daemon serves the API and admits nobody to the dashboard. It is not
broken; it is closed. Opening it is what the rest of your path does.

It enables nothing and starts nothing. What would be enabled at boot is a daemon
that spawns shells with the permission prompt turned off, and whether to run one —
on which machine, admitting whom — is not a decision to take from inside a pipe.
So the rest is yours, and it is the same three lines on both paths — only the
first line's answer differs:

```bash
$EDITOR ~/.config/crswd/config          # dashboard_password, or access_enabled + the access_* keys
loginctl enable-linger "$USER"          # or the unit stops when you log out
systemctl --user enable --now crswd
```

The middle line is the one that is easy to skip and expensive to skip: a
`systemctl --user` unit enabled from an SSH session stops when that session ends,
and the symptom arrives minutes later looking nothing like its cause.

**What goes in that file is your path's business** —
[path 1](#path-1--on-the-internet-cloudflare-tunnel-and-access) has more to set
up in front of the daemon than in it,
[path 2](#path-2--on-your-own-network-the-dashboard-password) is two keys here and
one line to take back out of the unit — and both come back to the last two lines
above.

**If it does not come up, it has already said why.** Every bound in
[Configuration](#configuration) is a startup failure with a reason attached,
written to stderr and merged into the journal, naming the setting and what was
wrong with it — so read the refusal with `journalctl --user -u crswd -e` before
changing anything.

**Run it again whenever.** It never replaces a configuration, and it replaces the
unit only when the hash it recorded still matches what is on disk — a unit you
edited, or one you wrote by hand before this installer existed, is left alone and
said so. There is no record to read for a unit it did not write, and no record is
read as "leave it", never as permission.

> **A release is installed only if it is signed by a key this installer
> carries.** The checksum alone proves nothing: it is fetched from the same place
> as the binary, so anyone able to serve one can serve the other. The signature is
> verified against a key committed to this repository, which means the trust
> decision was made when you installed — before any attacker was involved.
>
> An installer carrying no key refuses every release rather than skipping the
> check. That is the intended failure, not an oversight.

### Updating, and rolling back

An update is a version, and **a rollback is an update you name**:

```
POST /dashboard/update    confirm=yes                 # the latest release
POST /dashboard/update    confirm=yes  version=v0.41  # a named one
```

Both are buttons on `/settings`; the form is there so that neither needs a shell.
The daemon stages the release under `~/.local/share/crswd/staging/` at mode
`0600`, checks the checksum and then the signature, and only then makes it
executable and runs it once with `--version` to prove it execs *on this host*: a
binary for the wrong architecture passes every cryptographic check and then fails
to start, and by then it would be the installed one. Only after that does it
rename over `~/.local/bin/crswd` and exit for systemd to restart into it. A
failure at any step leaves the daemon running exactly what it was running.

The binary it replaced is kept at **`~/.local/bin/crswd.previous`**. That is the
way back from the case the route cannot help with — a daemon that no longer
starts, so no request reaches it:

```bash
systemctl --user stop crswd
install -m 0755 ~/.local/bin/crswd.previous ~/.local/bin/crswd
systemctl --user start crswd
~/.local/bin/crswd --version          # which one is actually running
```

`crswd.previous` is one deep, and the next successful update overwrites it. It is
the way back from *this* version, not a history — to go further back, name the
version.

### A release, without the installer

Every release carries a tarball per architecture, `SHA256SUMS`, `SHA256SUMS.sig`,
and the deployment files: `crswd.service`, `cloudflared.example.yml` and
`crswd-api`. Check the sums before unpacking anything.

**A release asset is bytes and nothing else — file modes do not survive the
upload.** `crswd-api` is executable in this repository and arrives without the
bit, so install it rather than copying it:

```bash
install -m 0755 crswd-api ~/.local/bin/crswd-api
```

---

## The two doors

**There are two deployments, and choosing between them is the most consequential
thing on this page.** A request that passes the browser door starts an unsandboxed
shell on this host, so the door is the product. Both run the daemon as a **systemd
user service**; what differs is who is allowed to knock, and from where.

| | 1 · On the internet | 2 · On your own network |
|---|---|---|
| Reached through | A Cloudflare Tunnel dialling out to `*.example.com` | The LAN, directly |
| The browser door | **Cloudflare Access** — Google login and a one-email allowlist, enforced at the edge and re-checked here | **A dashboard password** — a sign-in form this daemon serves, and a cookie it signed |
| Configured by | `CRSW_ACCESS_TEAM_DOMAIN` + `CRSW_ACCESS_AUD` + `CRSW_ACCESS_ALLOWED_EMAILS`, and `CRSW_ACCESS_ENABLED=true` to say so | `CRSW_DASHBOARD_PASSWORD` |
| Where it listens | `CRSW_LISTEN=127.0.0.1:8765` — the tunnel is the only way in | `CRSW_LISTEN` on a LAN address, or `0.0.0.0` |
| TLS | The Cloudflare edge terminates it, before the tunnel | **None by default.** Put a reverse proxy with a certificate in front, or the password crosses the network in clear — **the warning below** |

**Never both.** Configure a password beside Access and the daemon refuses to start
rather than picking a winner: which door is live decides who may execute code here,
and that is the last question a daemon should answer by guessing. Configure neither
and it serves the API, admits nobody to the dashboard, and says so at every start.

Which one a running daemon actually has is on `GET /settings`, under **Who may reach
it**, in a sentence — read from the door the server was built with rather than from
the file, so a daemon wired one way and configured another says what it *is*.

### Path 1 — on the internet: Cloudflare Tunnel and Access

**What you are building:** a tunnel that dials out from this host to Cloudflare, a
public hostname routed to that tunnel, and an **Access application** in front of
that hostname deciding who reaches it. The daemon goes on listening on
`127.0.0.1:8765` throughout — nothing below opens a port on the host or the router.

You need a Cloudflare account with a domain on it. Steps 1 to 8 happen in
Cloudflare and in `~/.cloudflared/`; only 9 onwards touch the daemon.

> **A public hostname with no Access application in front of it leaves HMAC alone
> between the internet and unsandboxed execution.** The daemon validates the
> assertion the edge forwards — pinned audience, pinned algorithm, and its own copy
> of the allowlist — so the edge and the daemon each get a say. But it can only
> check an assertion the edge actually wrote, and there is none to write when there
> is no application. Do not point a public name at this before step 8.

**1 · Install and authenticate `cloudflared`.** Cloudflare publishes packages and a
static binary; install it however this host prefers. Then:

```bash
cloudflared tunnel login
```

It opens a browser, asks which of your zones you are authorising, and writes a
certificate to `~/.cloudflared/cert.pem`. That certificate is what authorises the
next two commands against your account. It is not the tunnel's own credential —
step 2 creates that.

**2 · Create the tunnel.**

```bash
cloudflared tunnel create crswd
```

It prints a **tunnel ID**, a UUID, and writes that tunnel's credential to
`~/.cloudflared/<tunnel-id>.json`. Both are values you fill in at step 4.

**3 · Route the hostname to the tunnel.**

```bash
cloudflared tunnel route dns crswd crswd.example.com
```

This writes the DNS record — a proxied `CNAME` at that name, pointing at the
tunnel. It is the step most easily missed, and missing it is a tunnel that runs
correctly at a name that resolves to nothing. The name must be inside the zone you
authorised in step 1.

**4 · Fill in the tunnel's configuration.** Copy `cloudflared.example.yml` to
`~/.cloudflared/config.yml` — from [`deploy/`](deploy/) in this repository, or from
the release assets if you took the [one-line install](#install) — and edit four
values:

| In `~/.cloudflared/config.yml` | What goes there |
|---|---|
| `tunnel:` | the tunnel ID from step 2 |
| `credentials-file:` | the JSON path printed beside it |
| `hostname:` | the name you routed in step 3 |
| `service:` | `http://127.0.0.1:8765` — leave it, unless you moved `listen` |

**The `service` line and the daemon's `listen` are one address written twice.** A
mismatch is a `502` from the edge with nothing in the daemon's journal to explain
it, because nothing ever reached the daemon. Leave the catch-all
`- service: http_status:404` last: it is what stops the tunnel proxying whatever
else this host may serve later.

**5 · Create the Access application.** In Cloudflare's Zero Trust dashboard, add a
**self-hosted** application whose domain is the hostname from step 3. Two of the
daemon's four settings come out of it:

- the **Application Audience (AUD) tag**, on the application itself →
  `access_aud`
- your **team domain**, normally `<team>.cloudflareaccess.com` →
  `access_team_domain`

Cloudflare rearranges that dashboard from time to time; those two names are what to
search it for.

**6 · Configure the identity provider.** Add **Google** as a login method for the
team, then select it on this application. Access runs the login at the edge, so an
unauthenticated browser never reaches this host at all; what the daemon sees is the
assertion Access writes afterwards.

**7 · Create a service token** — skip this if you will only ever use a browser. It
is what the API client presents at the edge in place of a login: a
`CF-Access-Client-Id` and a `CF-Access-Client-Secret`, sent alongside the signature
the daemon checks. **The client secret is shown once, when the token is created,**
and cannot be recovered afterwards — only regenerated. Keep it with the shared
secret; `deploy/crswd-api` reads both at call time.

**8 · Give that application both policies.** On the one application:

- an **Allow** policy naming your email address. That same address is
  `access_allowed_emails` at step 9.
- a **Service Auth** policy naming the token from step 7.

**Both, or the door is wrong in one of two ways.** Without the Service Auth policy
the edge answers the API client with a redirect to the login page and the daemon is
never reached. Without at least one Allow policy beside it, Access never issues a
usable assertion to anybody.

**9 · Write the four settings the daemon reads.** The installer already left
`~/.config/crswd/config` holding `shared_secret` and `allowed_roots`, at mode
`0600`. What this path adds is the door:

```
access_enabled = true
access_team_domain = <team>.cloudflareaccess.com
access_aud = <the AUD tag from step 5>
access_allowed_emails = you@example.com
```

Keep that file at `0600`: it now holds the allowlist as well, and the daemon
refuses to start from a file naming a person that any other account can read.
`listen` stays at its default `127.0.0.1:8765`, because the tunnel is the only way
in and there is nothing to widen.

**The first line is not what selects the Access door** — the other three do that on
their own. What it buys is a refusal: with `access_enabled = true` and the three
absent, the daemon stops instead of starting a dashboard that admits nobody, which
on this deployment is the mistake worth making fatal.

**10 · Check the file, then start both.**

```bash
crswd config check                  # parses the file, names the keys it sets
systemctl --user restart crswd      # or enable --now, if you have not started it yet
cloudflared tunnel run crswd        # in another terminal, the first time
```

`config check` reads the file the daemon would read and reports its grammar, the
keys it sets — never a value — and the mode it is sitting on. The values themselves
are checked against the environment at startup, so a start that fails is still read
with `journalctl --user -u crswd -e`. Running the tunnel in the foreground is what
proves the path end to end; what keeps it up across a reboot is a service of
cloudflared's own — its `service install` subcommand writes one — or a unit beside
the daemon's, which is the order [`deploy/README.md`](deploy/README.md) takes.

**11 · Browse to `https://crswd.example.com/`.** Google's login, then the
dashboard. **If the dashboard renders without a login, stop**: the Access
application is not in front of that hostname, and every session on the host is a
shell reachable from the internet. The check is
[Verifying the exposure model](#verifying-the-exposure-model), and it is worth
running once even when the login does appear.

**Every file in `deploy/` is an example.** This repository is public, so no real
hostname, tunnel ID, AUD tag, allowed email, or path appears in one — those come
from the environment or a secret manager at deploy time.
[`deploy/README.md`](deploy/README.md) is the operator's page behind this one: the
`EnvironmentFile` shape for the four values, why three settings in the unit are
load-bearing, and the service token's two halves.

#### From a clone instead

The [one-line install](#install) does the first two lines below for you, from a
published release. What follows is the same deployment done from a clone, and the
account of what each file is for.

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

### Path 2 — on your own network: the dashboard password

No Cloudflare, no tunnel, no hostname: the daemon listens on an address the machine
you are sitting at can reach, and serves its own sign-in form. Nothing goes in front
of it, so all four steps below are on this host.

**1 · Add the door to the configuration you already have.** The installer left
`~/.config/crswd/config` holding `shared_secret` and `allowed_roots`, at mode
`0600`. What this path adds is two lines:

```
dashboard_password = <a long passphrase, at least 16 characters>
listen = 0.0.0.0:8765
```

**Both in the same edit.** A non-loopback address on a daemon whose dashboard
admits nobody is refused at startup — the bound at the end of this section — so
`listen` written ahead of the password is a daemon that stops and names both
doors. Keep the file at `0600`: the installer wrote it that way, and
the daemon refuses to start from a file setting a secret that any other account
can read. *Writing the file yourself rather than installing?* Then
`shared_secret` and `allowed_roots` are yours to write too —
[`config.example`](config.example) is the annotated copy, and the secret is the
output of `openssl rand -hex 32`.

**2 · Take the unit's copy of `listen` out of the way**, or the line you just
wrote does nothing. The unit the installer wrote carries
`Environment=CRSW_LISTEN=127.0.0.1:8765`, and **the environment beats the file**:
leave it and the daemon goes on binding loopback, unreachable from the LAN, while
the file says otherwise and `/settings` names the environment as the layer that
won.

```bash
$EDITOR ~/.config/systemd/user/crswd.service   # delete the CRSW_LISTEN line
systemctl --user daemon-reload
```

Putting the LAN address on that line instead of in the file works equally well;
what does not work is setting it in both and expecting the file to win. Either
way the unit becomes one you edited, so a later re-run of the
[installer](#install) leaves it alone and says so.

**3 · Check the file, then start.**

```bash
crswd config check                  # parses the file, names the keys it sets
systemctl --user enable --now crswd
```

`config check` reads the file the daemon would read and reports its grammar, the
keys it sets — never a value — and the mode it is sitting on. It says nothing
about the unit and nothing about the values: those are checked against the
environment at startup, so a start that fails is still read with the daemon's own
refusal — `journalctl --user -u crswd -e`. That the port ended up where you meant
it is `ss -tlnp | grep crswd`, under
[Verifying the exposure model](#verifying-the-exposure-model).

**4 · Browse to the sign-in form.**

```
http://<the host's LAN address>:8765/login
```

**That path is the only way in, and nothing points you at it.** A browser arriving
at `/` with no cookie gets the same uniform 401 a stranger gets — this door tells
nobody which paths it serves, including you. Sign in there and every other page
works as it does behind Access. **To sign out**, go to Settings (the link in the
header of every page) and press **Sign out** under *Who may reach it*, beside the
sentence naming the door. That ends this browser's copy of the cookie; a copy taken
off the machine stays valid until it expires, and what ends every outstanding
sign-in at once is changing `dashboard_password` or rotating `shared_secret`.

`listen` may be a LAN address instead of `0.0.0.0` if the host has more than one
interface and you want exactly one of them. It may not be a name — see the note
under [Configuration](#configuration) — and `0.0.0.0` is refused outright on a
daemon with no door, which is the bound that makes this mode safe to have at all.

> **⚠️ Without TLS in front of the daemon, the password crosses the network in
> clear.** Anyone who can watch that network — a switch port, a compromised device,
> the Wi-Fi you are on — reads it and gets everything you have. This is a real
> weakness of this mode, not a footnote, and it is stated rather than reassured
> away.
>
> **Put a reverse proxy with a certificate in front of the daemon.** Terminate TLS
> there and point it back at `127.0.0.1:8765`; the daemon does not terminate TLS
> itself, and an operator who wants it puts a proxy in front, which is what the
> loopback bind already assumed. The cookie limits the exposure to one crossing per
> sign-in instead of one per request — it does not remove it.
>
> Behind such a proxy the session cookie is still not marked `Secure`, and that is
> deliberate: the flag follows the TLS state of the connection the *daemon* sees,
> and never `X-Forwarded-Proto`, which is caller-authored text there is no
> configured proxy to believe. So do not leave a plaintext route to the same host
> open beside the proxied one — a browser would send the cookie down it.
>
> Run this only on a network you actually control. Everything on that network can
> reach the port, and behind the port is an unsandboxed shell.

**The password door authenticates less than Access does, and knowing what less
means is the point of choosing.** Access verifies a *person* against Google, at the
edge, before this host is reachable at all; a password verifies that whoever is
asking knows one secret. There is one operator behind it by construction, no
allowlist to check, and nothing failing closed in front of it. What is unchanged is
everything behind the door: the same owner, the same ownership checks, the same
action gate on anything that changes the host. What bounds a guess is a sixteen
character minimum, a budget of six attempts a minute per source address, and a
refusal that says nothing about which of the two sides was wrong.

### Verifying the exposure model

**Behind the tunnel** — the daemon must be reachable no other way:

```bash
ss -tlnp | grep crswd                  # 127.0.0.1:PORT, never 0.0.0.0
curl -sS http://<host-lan-ip>:PORT/    # must fail to connect
```

If the second command reaches the daemon, stop and fix the bind address before
going any further.

**On a LAN** the second command is *supposed* to connect — so what to check is that
connecting buys nothing. It must answer `401` with no cookie, and the sign-in form
must be the only thing that does not:

```bash
curl -sS -o /dev/null -w '%{http_code}\n' http://<host-lan-ip>:PORT/         # 401
curl -sS -o /dev/null -w '%{http_code}\n' http://<host-lan-ip>:PORT/login    # 200
```

A `200` on the first line is the failure this whole section exists to catch: it
means something is admitting an unauthenticated browser, and every session on the
host is a shell anyone on that network can drive. Stop and fix it.

---

## Configuration

The daemon is configured by the environment and, optionally, by one file. Both are
read once at startup, before it binds or spawns anything, and nothing here can be
changed while it runs. There are no flags; `-h` reports usage and nothing else.

Anything that would weaken a bound is a **startup failure, not a warning**.
Sessions run with `--dangerously-skip-permissions`, so these settings are what
stands in for the permission prompt that is gone.

| Variable | Required | Default | Bounds |
|---|---|---|---|
| `CRSW_SHARED_SECRET` | **yes** | — | The HMAC secret the API client signs with. Unset, or shorter than 32 bytes, refuses to start |
| `CRSW_ALLOWED_ROOTS` | no | `$HOME/code`, with a loud banner | Colon-separated absolute directories a session may run in. An entry that is empty, relative, missing, unresolvable, or not a directory refuses |
| `CRSW_DISCOVER_ROOTS` | no | `false` | Offer each approved root's immediate subdirectories as working-directory suggestions. Anything but a boolean refuses |
| `CRSW_WORKDIR_SUGGESTIONS` | no | empty | Comma-separated absolute directories offered on the create form beside the roots. An entry that is empty or relative refuses; one outside the roots is offered and refused on create, because the list is a convenience and `CRSW_ALLOWED_ROOTS` is the control |
| `CRSW_LISTEN` | no | `127.0.0.1:8765` | The listener. The host must be an IP literal; a name refuses under every door. A non-loopback host such as `0.0.0.0` is permitted **only when the dashboard has a door** — Access or `CRSW_DASHBOARD_PASSWORD`. With neither, it refuses: a daemon that admits nobody may not be reachable by anybody. A port out of range refuses |
| `CRSW_MAX_SESSIONS` | no | `5` | How many sessions may exist at once. Below 1 refuses |
| `CRSW_DESTROY_ON_SHUTDOWN` | no | `false` | Tear every session down when the daemon stops. Off by default: sessions survive a clean stop and startup adoption reclaims them, so a redeploy no longer costs the fleet. `true` restores the old behaviour, for a host being decommissioned rather than updated |
| `CRSW_SESSION_LIFETIME` | no | `24h` | How long a session may live from creation, never renewed. The default every create inherits, and a create may ask for another up to the ceiling below. Zero or negative refuses, and so does `never` — the word belongs to the ceiling, because a *default* of never would make every session on the host immortal without a create ever asking |
| `CRSW_SESSION_LIFETIME_MAX` | no | `CRSW_SESSION_LIFETIME` | The ceiling a per-session lifetime override may not exceed — a create asking past it is refused, never clamped. Below the default refuses. It defaults to the default, so an override buys nothing until this is raised deliberately. `never` is the one value here that is not a duration: no ceiling at all, and so a daemon on which a create may ask for a session that never expires. A negative refuses rather than being read as a second spelling of it |
| `CRSW_IDLE_TIMEOUT` | no | `60m` | How long a session may sit idle before the reaper takes it, counted from the later of two clocks and moving with whichever is more recent: the last request that drove the session, and what tmux itself last saw it print. Negative refuses; longer than the lifetime refuses |
| `CRSW_IDLE_TIMEOUT_MAX` | no | `CRSW_IDLE_TIMEOUT` | The ceiling a per-session idle override may not exceed. Below the default refuses. It bounds an idle timeout set *longer*; the form's idle switch turns that clock off rather than lengthening it, which this does not bound and the absolute lifetime does. It takes no `never` and needs none — switching idle reaping off asks this ceiling for no room, because the absolute deadline still bounds such a session |
| `CRSW_CREATE_RATE_PER_MIN` | no | `6` | Creates per minute per caller. Below 1 refuses |
| `CRSW_MAX_BODY_BYTES` | no | `65536` | The largest request body read. Below 1 refuses |
| `CRSW_ACCESS_ENABLED` | no | `false` | Declares Cloudflare Access to be the browser door. On with none of the three below, it refuses to start rather than serving a dashboard that admits nobody; on beside `CRSW_DASHBOARD_PASSWORD`, it refuses rather than choosing a door. It is not required for the Access door — the three values still select it on their own |
| `CRSW_ACCESS_TEAM_DOMAIN` | all three, or none | none — the dashboard admits nobody | The Cloudflare Access team domain the assertion's issuer and key set are both derived from |
| `CRSW_ACCESS_AUD` | all three, or none | none | The Access application's AUD tag, compared for equality and never parsed |
| `CRSW_ACCESS_ALLOWED_EMAILS` | all three, or none | none | Comma-separated addresses admitted to the dashboard. An entry that is empty or carries a space refuses. **Treated as a secret** |
| `CRSW_DASHBOARD_PASSWORD` | no | none | The browser door for a daemon with no Cloudflare in front of it. Shorter than 16 characters refuses; set alongside Access, refuses. **Treated as a secret**, so it is never rendered and never editable from the browser. Without TLS in front of the daemon it crosses the network in clear |
| `CRSW_MAX_STREAMS` | no | `10` | Live output streams open at once. Below 1 refuses |
| `CRSW_PANE_BOUND` | no | `200` | The largest screen a pane capture may return, in lines. A capture past it is refused, never shortened |
| `CRSW_START_COMMAND` | no | `claude --dangerously-skip-permissions` | The command line bound to the name `default`. Empty, or carrying a `;` or a control character, refuses |
| `CRSW_START_COMMANDS` | no | empty — only `default` | The named set a create may choose from, `name=command` pairs separated by commas. An entry that is empty, is not `name=command`, repeats a name, names one outside `[a-z0-9-]`, re-defines `default` alongside `CRSW_START_COMMAND`, or carries a command the rule above refuses, refuses |
| `CRSW_REMOTE_CONTROL_COMMAND` | no | `rc`, when a command by that name exists | Which entry of that set the dashboard's remote-control switch means. A name the set does not have refuses |

The three Access variables are **all three or none of them**. None means a daemon
that serves the API and admits nobody to the dashboard, which it says loudly at
every start; some of them is a half-configured door and a startup failure.

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
- **The create form offers three sources of working directory, unioned.** The
  approved roots themselves, always; `CRSW_WORKDIR_SUGGESTIONS`; and each root's
  immediate subdirectories when `CRSW_DISCOVER_ROOTS` is on. Sorted and
  deduplicated, so a default install with neither of the other two set still
  offers something rather than an empty list. **A suggestion is never an
  authorisation**: the field submits an ordinary string, so a path taken from the
  list and a path typed in full meet the same allowlist check, the same refusal,
  and the same audit record. The list is a convenience and `CRSW_ALLOWED_ROOTS`
  is the control, which is why a suggestion outside the roots is legal to
  configure and refused on create.
- **Remote control is a mode, not a command name.** The create form renders one
  checkbox, and a ticked box posts `remote_control=on` while an unticked one
  posts nothing — so a lost or stripped field yields the *less* privileged mode.
  Which configured command that mode runs is `CRSW_REMOTE_CONTROL_COMMAND`, read
  server-side; **no command name is ever sent to or accepted from a browser**,
  and a real configured name submitted in that field is refused like any other
  value. `CRSW_START_COMMANDS` is still how the API door names one, because that
  door takes names.
- **There are two clocks, and either of them can be turned off.** The idle
  timeout runs from the session's last activity — the later of the last request
  that drove it and what tmux itself last saw it print — and moves with whichever
  is more recent; the absolute lifetime runs from creation and is never renewed.
  A create may override either under the `_MAX` ceiling beside it, and **the
  dashboard offers both as switches**: *Never die when idle*, on every daemon,
  and *Never die at the lifetime limit*, only on one whose operator removed the
  lifetime ceiling — anywhere else that create would be refused, and a control
  whose only outcome is a refusal is not drawn. With both ticked, nothing reaps
  the session. Both deadlines are on the session's card, beside the activity the
  idle one counts from, so which clock is coming is something to read rather than
  infer.
- **Watching a session is still not driving it.** Reading — the dashboard, the
  session page, the live stream — advances neither clock. What advances the idle
  one is a request that drives the session, or the session producing output on
  this host, so a long job you are watching is not reaped out from under you
  while a tab left open over a weekend holds nothing alive. A tmux reading that
  is missing or unusable can only fail to be the later of the two, so no session
  is ever reaped *because* that reading failed.
- **"Never" is spellable, and it removes a bound rather than raising one.**
  `CRSW_SESSION_LIFETIME_MAX=never` is no ceiling at all: a create may then ask
  for a session that never expires, which is what puts the second switch on the
  form. The default beside it still refuses the word, so no session becomes
  immortal without a create asking. Read what it costs first — the absolute
  deadline is the one bound that is never renewed, so with it gone what contains
  a session is `CRSW_ALLOWED_ROOTS` and whichever door admits callers, not the
  reaper. One edge comes with it: no session record survives the daemon, so a
  restart adopts such a session back from tmux as an ordinary one carrying the
  built-in 24h lifetime, and destroys it on the spot if it is already older.
  Saying how long is still the smaller answer, and there is no upper bound on the
  parse, so a year is a legal one:
  ```
  CRSW_SESSION_LIFETIME=8760h     # the default every create inherits; the ceiling follows it
  CRSW_IDLE_TIMEOUT=8760h         # or leave this and tick the form's switch per session
  ```
  Raising only `CRSW_SESSION_LIFETIME_MAX` widens what a create may *ask* for and
  changes nothing an operator gets by default — that is the shape for the API
  door, which sends `lifetime` and `idle_timeout` per create, rather than for the
  form, whose lifetime control is those two switches. Nothing is re-read while the
  daemon runs and a session keeps the deadlines it was created with, so a raise
  reaches the next session and not the one already running.
- **`CRSW_LISTEN` will not take a hostname, under either door.** `localhost` is
  refused rather than resolved: `/etc/hosts` or a resolver could move the bind
  without this value changing, and only an IP literal says where the listener will
  actually be. The wildcard is spelled `0.0.0.0` for the same reason — `:8765` is an
  empty host, which is a name. **Whether a non-loopback literal is accepted at all
  depends on which door is configured**, and the refusal names both of them; see
  [The two doors](#the-two-doors).
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

One limit is still a **constant in the code, not a setting**: the signed-request
timestamp window, 300s in both directions. The session lifetime and the idle
timeout are settings now, and what bounds them is the `_MAX` ceiling beside each
rather than their being unwritable — the ceiling is the operator's, the choice
under it is the session's, and the blast radius stays bounded because a session
cannot reach past what the host was configured to allow.

### The configuration file

Everything in the table can be written in a file instead.
[`config.example`](config.example) is the annotated copy to start from — every
setting commented out at its default, so a copy with nothing uncommented is a
daemon that behaves exactly as one with no file at all.
[`.env.example`](.env.example) documents the same settings as the environment
variables a systemd unit sets, and carries names only — never a value.

```
$CRSW_CONFIG_FILE, if it is set — it names the file outright
else $XDG_CONFIG_HOME/crswd/config
else ~/.config/crswd/config
```

A missing file is never an error: no file is the configuration every deployment
before milestone 4 had. A file that *is* there and cannot be read is an error,
because starting on the environment instead would ignore every bound written in
it.

The format is `key = value` with `#` comments, and three rules carry the weight:

- **`#` is a comment marker at the start of a line and nowhere else.** A shared
  secret may legitimately contain a `#`, and stripping from the first one would
  truncate that secret into a daemon that starts, looks healthy, and rejects every
  request.
- **The separator is the first `=`.** `start_commands` always carries one inside
  its value.
- **A key is its variable minus `CRSW_`, lower-cased** — `allowed_roots` is
  `CRSW_ALLOWED_ROOTS`. That is a rule rather than a table, so every variable above
  is a key and the reverse. A misspelled key is refused, never skipped, and so is a
  key set twice.

The one key that is not a setting is `version`, the schema the file was written
against. Absent means 1, which is what every hand-written file is; a number higher
than the daemon understands is refused rather than read optimistically.

**Precedence is flag, then environment, then file, then default**, decided in one
shim behind `config.LoadFrom` so no bound, default or refusal is written twice.
(The daemon defines no flags today — the slot is the order the shim decides in, not
a promise that one is coming.) A container or a unit can therefore override one
value without writing a file, and a stale file on a host cannot silently override
the environment a deployment was configured with.

**A file that sets `shared_secret`, `access_allowed_emails` or `dashboard_password`
must be mode 0600**, and the daemon refuses to start from one any other account can
read. That refusal fires only on a file that actually holds one of the three:
refusing over the mode of a file holding nothing but `allowed_roots` would demand a
change that protects nothing.

```bash
crswd config check [path]      # report on a file without starting anything
crswd config migrate [path]    # rewrite one into the current schema, keeping config.bak
```

`config migrate` is the only thing that rewrites a configuration file wholesale —
the settings page's edit replaces one key in place, and the installer writes a
file only where there is none. If the file stops loading, the daemon starts from
the `config.bak` beside it instead — loudly, naming both files, because it is then
running on the older one.

### Seeing what it is configured to do

Every page carries a **Settings link** in the header, beside the operator's
identity and outside the page's heading. One link is not a navigation bar, and
it is the whole of the dashboard's navigation.

`GET /settings` is the read-only account of how this daemon was configured: one
row per key, the effective value, and **which layer supplied it**. Provenance is
recorded by the precedence shim as it decides rather than inferred by comparison —
a file and an environment holding the same bytes are indistinguishable by
comparison, and that is exactly the case an operator is on the page to ask about.
Above the table is the file those values were read from, or the sentence saying
none was.

A secret's value column reads `present` or `absent` and nothing else — not a
length, not a prefix, not a hash. `GET` is still the only verb registered on
`/settings` itself, but **the page does act**, through four routes of its own:
`POST /settings/edit` for one configuration key, `POST /dashboard/update` for the
binary, `POST /dashboard/restart` for the process running it, and `POST /logout`
on a daemon that has a sign-in to end.

An edit is bounded rather than absent, and the bounds are worth knowing before you
use it:

- **No secret is editable.** `shared_secret` is the credential for the signed API,
  which the browser door does not cover; `dashboard_password` is the door itself,
  and a password settable from the page it protects is not a door. A row that
  cannot be edited renders as text rather than as a control that would be refused.
- **It is validated before it lands.** The candidate file is parsed and loaded
  through exactly the loader a startup uses, so a value that would stop the daemon
  coming up is refused while it is still running.
- **It writes the file, not the running daemon.** Configuration is read once at
  startup; an edit takes effect at the next start, which is what the restart button
  beside it is for. The write is atomic and keeps the previous file, and the audit
  record names the key and never the value.

### What it checks before it binds

At startup the daemon probes what it shells out to, and the two dependencies fail
differently on purpose: **`tmux` missing is fatal**, because without it there is
nothing this daemon can do and starting would only defer the failure to the first
create, while **a start command's binary missing is a warning**, because a daemon
that still serves the dashboard is the thing that can tell you so. It resolves a
start command the way the session will — in a tmux pane's login shell, not on the
service manager's `PATH` — so a `claude` under `~/.local/bin` is found rather than
warned about, and a login shell that cannot be asked produces a note naming what
was checked rather than a claim that the command is missing.

Why the daemon is allowed to run `$SHELL -l` at all, and every bound on the one
thing it asks, is [`docs/security.md`](docs/security.md) §4; what it costs a
deployment — the operator's own profile running before anything binds — is
[`deploy/README.md`](deploy/README.md).

---

## Reading the audit trail

Audit records are structured JSON on stdout, which makes the journal the entire
storage design — no file mode, no rotation, no disk to fill.

```bash
journalctl --user -u crswd -f -o cat | grep '^{' | jq .
journalctl --user -u crswd -o cat | grep '^{' | jq 'select(.action == "auth.reject")'
```

**The `grep` is not optional.** The daemon writes records to stdout and its own
diagnostics to stderr — the startup probe's banners, the config loader, the `log`
package — and **systemd merges both file descriptors into one journal**, so
`journalctl` hands them back interleaved however cleanly the daemon separated
them. Without the filter the first banner is a `jq: parse error` and the trail
reads as unparseable (#88). `_COMM=crswd` is not a substitute and was measured:
the non-JSON lines are the daemon's own, so filtering to its process keeps every
one of them.

`-o cat` is what makes this work: it prints the message alone, without the syslog
prefix systemd would otherwise put in front of the JSON. No record carries prompt
text, pane output, a token, a token hash, or the shared secret — `internal/audit`
asserts that across every operation, so the journal is safe to read in a way a
pane never is.

---

## The API door

**The API is a second door, and neither browser door is a share of it.** Whichever
one the browser uses, every request to `/sessions…` carries an HMAC signature over
its body, a timestamp inside a 300-second window, and — for anything naming a
session — that session's own token, all checked by the daemon itself. On the
internet the edge's Service Auth policy stands in front of that as well — the
service token and the policy naming it are steps 7 and 8 of
[path 1](#path-1--on-the-internet-cloudflare-tunnel-and-access) — while on a LAN
there is no edge, so the signature is the whole of it. `deploy/crswd-api` is the
client, and it is a shell script so that reading it is the documentation.

---

## Why it is built this way

**tmux, not bare subprocesses.** Sessions survive a daemon restart, you can attach
by hand to debug, and `send-keys` / `capture-pane` are what make `/compact` and the
device-code login relay possible at all.

**Cloudflare Tunnel, not an open port.** On the internet the daemon binds
`127.0.0.1` and the tunnel connects outbound; nothing inbound is ever opened on the
host or the router. It *will* bind wider, for the LAN deployment above — but only on
a daemon whose dashboard has a door somebody can open. The invariant is **never
reachable without authentication**, not *never reachable*, and that is a bound
relaxed rather than deleted.

**Cloudflare Access, not an in-daemon OAuth flow.** Google login and the one-email
allowlist are enforced at the edge, so unauthenticated traffic never reaches the
box. The daemon just validates the signed JWT — tens of lines instead of hundreds.

**A password, only where there is no edge to do that.** A daemon on your own
network has no Access application in front of it and so no assertion to validate.
There it serves its own sign-in form and issues a cookie it signed. That proves
knowledge of one secret rather than identifying a person against an identity
provider — strictly less than Access does, which is stated above rather than
smoothed over.

**Go templates and hand-written JavaScript, not an SPA.** Single static binary via
`go:embed`. No npm, no framework, no second toolchain, no dependency at all —
`go.sum` does not exist, and a test fails if one appears. SSE is a natural fit for
tailing pane output.

## Working on it

Changing the daemon is [`CONTRIBUTING.md`](CONTRIBUTING.md) — the from-source
build, how a milestone is planned, and how the autonomous loop is run.
[`AGENTS.md`](AGENTS.md) is the contract every change is held to, human or agent.

## Licence

MIT — see [LICENSE](LICENSE).
