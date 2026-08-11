# claude-remote-session-webhook

Start and drive Claude Code sessions on your own machine, from anywhere.

A self-hosted Go daemon (`crswd`) that serves a web dashboard and an HTTP API under
a `*.example.com` hostname. Each session runs in a tmux window with
`--dangerously-skip-permissions`. Two clients: a browser UI behind Cloudflare Access
(Google identity), and a companion Claude skill authenticating by HMAC signature.

## Install

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

It enables nothing and starts nothing, because the daemon cannot serve a request
before the secret is set and a service that fails on first boot teaches its
operator to ignore a failing service. So the rest is yours:

```bash
$EDITOR ~/.config/crswd/config          # shared_secret, allowed_roots
systemctl --user enable --now crswd
```

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

> **Status: milestones 1 through 5 are complete and deployed; milestone 6 is
> landing.** The daemon core — config, `tmuxctl`, session CRUD, HMAC auth, the
> audit log, and the reaper — ships alongside daemon-side Cloudflare Access
> validation and a dashboard that reads, streams **and acts**: create, destroy,
> rename and compact, each a plain form post that works with scripting switched
> off. Both doors are real: a browser is admitted by Google identity and then
> re-verified by the daemon, and the API client is admitted by an Access service
> token and then checked by signature, timestamp, replay and per-session token.
> Milestone 4 added the configuration file, the read-only `/settings` page that
> says where every value came from, and the startup dependency probes; milestone
> 5 finished the dashboard. Milestone 6 is the one above: `--version` and
> `GET /dashboard/version`, a release on every merge, the installer, and
> **`POST /dashboard/update` with the staging-and-swap behind it** — the release
> is fetched, checked against its published checksum, checked against a signature
> made by a key the operator holds, run once to prove it execs here, and only
> then renamed into place. The device-code login relay and the companion Claude
> skill are still ahead of all of it.

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
| 4 | Configure and operate | config file, read-only `/settings`, dependency probes, a dashboard that works without script |
| 5 | Finish the dashboard | remote control as a mode, working-directory suggestions + themed picker, the settings link, the audit-trail and probe defects |
| 6 | Ship it to someone else | `--version`, a release per merge, the one-line installer, signed self-update |

The device-code login relay and the companion Claude skill follow milestone 6;
they are not in it.

## Working in this repo

Read [`AGENTS.md`](AGENTS.md) first — it is the contract for humans and agents
alike, and it names which `docs/` file to load for a given change.

This is the from-source path, for changing the daemon. To *run* it, use the
[one-line install](#install) — a released binary needs no compiler, which is the
point of publishing one.

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
| `CRSW_LISTEN` | no | `127.0.0.1:8765` | The listener. A host that is not a loopback IP literal, or a port out of range, refuses |
| `CRSW_MAX_SESSIONS` | no | `5` | How many sessions may exist at once. Below 1 refuses |
| `CRSW_DESTROY_ON_SHUTDOWN` | no | `false` | Tear every session down when the daemon stops. Off by default: sessions survive a clean stop and startup adoption reclaims them, so a redeploy no longer costs the fleet. `true` restores the old behaviour, for a host being decommissioned rather than updated |
| `CRSW_SESSION_LIFETIME` | no | `24h` | How long a session may live from creation, never renewed. The default every create inherits, and a create may ask for another up to the ceiling below. Zero or negative refuses; there is no "never", so an operator who wants one sets this long instead |
| `CRSW_SESSION_LIFETIME_MAX` | no | `CRSW_SESSION_LIFETIME` | The ceiling a per-session lifetime override may not exceed — a create asking past it is refused, never clamped. Below the default refuses. It defaults to the default, so an override buys nothing until this is raised deliberately |
| `CRSW_IDLE_TIMEOUT` | no | `60m` | How long a session may sit without a request before the reaper takes it, counted from its last activity and moving with it. Negative refuses; longer than the lifetime refuses |
| `CRSW_IDLE_TIMEOUT_MAX` | no | `CRSW_IDLE_TIMEOUT` | The ceiling a per-session idle override may not exceed. Below the default refuses. It bounds an idle timeout set *longer*; the dashboard's switch turns that clock off rather than lengthening it, which this does not bound and the absolute lifetime does |
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
- **There are two clocks, and only one of them can be turned off.** The idle
  timeout runs from a session's last activity and moves with it; the absolute
  lifetime runs from creation and is never renewed. A create may override either
  under the `_MAX` ceiling beside it, and **the dashboard makes the idle half of
  that choice**: the create form carries a *Never die when idle* switch, and a
  session started with it ticked is never reaped for sitting quiet. It still dies
  at its absolute deadline — which is exactly what makes turning the idle clock
  off safe, and why there is no switch for the other one. Both deadlines are on
  the session's card, so which clock is coming is something to read rather than
  infer.
- **"Effectively never" is a ceiling raised, not a bound removed.** No value
  spells *never*, and a negative lifetime is refused, so the way to get a session
  that outlives a working day is to say how long. There is no upper bound on the
  parse, so a year is a legal answer:
  ```
  CRSW_SESSION_LIFETIME=8760h     # the default every create inherits; the ceiling follows it
  CRSW_IDLE_TIMEOUT=8760h         # or leave this and tick the form's switch per session
  ```
  Raising only `CRSW_SESSION_LIFETIME_MAX` widens what a create may *ask* for and
  changes nothing an operator gets by default — that is the shape for the API
  door, which sends `lifetime` and `idle_timeout` per create, rather than for the
  form, whose only lifetime control is that switch. Nothing is re-read while the
  daemon runs and a session keeps the deadlines it was created with, so a raise
  reaches the next session and not the one already running.
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

**A file that sets `shared_secret` or `access_allowed_emails` must be mode 0600**,
and the daemon refuses to start from one any other account can read. That refusal
fires only on a file that actually holds one of the two: refusing over the mode of
a file holding nothing but `allowed_roots` would demand a change that protects
nothing.

```bash
crswd config check [path]      # report on a file without starting anything
crswd config migrate [path]    # rewrite one into the current schema, keeping config.bak
```

`config migrate` is the only code in this repository that writes a configuration
file. If the file stops loading, the daemon starts from the `config.bak` beside it
instead — loudly, naming both files, because it is then running on the older one.

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
length, not a prefix, not a hash. **No mutating verb is registered on `/settings`
at all**: editing the operator's file from a browser is the highest-consequence
surface in the product, and a route that does not exist cannot be exploited.

### What it checks before it binds

At startup the daemon probes what it shells out to, and the two dependencies fail
differently on purpose:

- **`tmux` missing is fatal.** Without it there is nothing this daemon can do, so
  starting would only defer the failure to the operator's first create. It is
  looked for on the daemon's own `PATH` and nowhere else, because the daemon is
  what runs it.
- **A start command's binary missing is a warning.** It can still serve the
  dashboard, adopt the sessions already on the host, and say what is wrong.

The second probe reads the *configured* commands, not a fixed `claude`, and the
install line it suggests comes from `/etc/os-release` rather than being guessed
from `GOOS`. Nothing installs anything and nothing runs what it found.

**It resolves a start command the way the session will, and that has three
outcomes rather than two.** A start command is typed into a login shell inside a
tmux pane, so the operator's profile has already run by the time the name is
looked up — `claude` under `~/.local/bin` is on that `PATH` and not on the
service manager's, which is the ordinary case for a tool installed under a home
directory. So:

| The binary is… | The daemon says |
|---|---|
| on the daemon's own `PATH` | nothing |
| absent there but on the login shell's | nothing — the session will find it |
| absent from both | a warning, naming the command, the setting it came from, and **both** places that were checked |
| unresolvable, because the shell could not be asked | a **note** saying what was checked and what could not be — never that the command is missing |

The last row is the point. A check that says "missing" about a command that works
trains an operator to ignore it, which is worse than not checking at all.

> **This makes the operator's login shell a startup dependency.** The daemon runs
> `$SHELL -l` and asks it, on stdin, for one thing: the value of `$PATH`. It never
> names the command to the shell — a `sh -lc "command -v $binary"` resolves
> identically and is the shell string `docs/security.md` §2 forbids — and it asks
> **at most once per start**, only after a command has already failed to resolve
> on the daemon's own `PATH`. The ask is bounded by a 5s timeout and a 1s wait
> delay, and a shell that cannot be run is the note above rather than a refusal.
> A profile that blocks therefore costs a start five seconds; it cannot hang one.

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

### Reading the audit trail

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

### Verifying the exposure model

```bash
ss -tlnp | grep crswd                  # 127.0.0.1:PORT, never 0.0.0.0
curl -sS http://<host-lan-ip>:PORT/    # must fail to connect
```

If the second command reaches the daemon, stop and fix the bind address before
going any further.

## Licence

MIT — see [LICENSE](LICENSE).
