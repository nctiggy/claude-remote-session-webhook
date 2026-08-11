# Implementation Plan

**Milestone 11 — Make it installable by a stranger.**

> *"The readme needs to be clear and crisp and be on theme. Someone else should be
> able to easily install this on their own machine. It should also try to automate
> as much as possible so the user can just run the curl/bash command to install if
> they want."*

Six tasks.

---

## ⚠️ None of this can be proven on the author's machine

This box has the project installed, a config written, `~/.local/bin` on `PATH`
and the unit in place. **Every precondition the installer exists to create is
already true here**, so a successful run proves nothing.

`verify-install` in `.github/workflows/release.yml` runs the published installer on
a **GitHub-hosted runner with a fresh `HOME`**, twice — once to prove it installs,
once to prove it does not overwrite. That job is the only thing in this project
that can fail for the reasons these tasks are about.

**Any task that changes `install.sh` must extend that job in the same task.**
"It worked when I ran it" is not evidence here and never has been.

---

## ⚠️ A generated secret must never be printed

The installer's output goes to a terminal scrollback, a CI log, and often a pipe
from `curl`. A secret that appears there has a second copy in all three.

Write it into the `0600` config and report **that** one was generated — never what
it is. This is the same discipline `crswd keygen` follows, for the same reason.

**And never overwrite an existing configuration.** That rule already exists and
generating a secret must not weaken it: a config that is present is the operator's,
generated secret or not.

---

## What the four secrets are, since T004 has to explain them

| Secret | Door | What it buys |
|---|---|---|
| ed25519 release key | — | The operator holds the private half; the public half is embedded at install. A self-update proves the bytes came from **this** repository. A checksum alone proves nothing: whoever can serve a binary can serve its checksum. |
| `shared_secret` | API | HMAC-SHA256 the companion skill and scripts sign with. Under 32 bytes refuses to start. |
| Access team domain / AUD / allowed emails | Browser | The daemon verifies the forwarded Access JWT **itself**, so Access failing open does not become an unauthenticated session. All three or none. |
| Per-session bearer token | API | Minted per session, scoped to that session. Tells apart callers who all authenticate as one shared secret. |

A service token's assertion carries **no email**, which is why "no email" must
never read as "allow" — or every API call would also admit the caller to the
dashboard.

---

## Conventions

- `- [ ]` open · `- [x]` done · `- [!]` blocked (reason in `PROGRESS.md`)
- Priority order is meaningful — the loop always takes the topmost open item.
- Each task must be independently completable **and verifiable** in one iteration.
- **Every task ends green**: `go build ./... && go vet ./... && go test ./... && golangci-lint run`,
  plus `-tags tmux` / `-tags quickstart` where touched.
- **Check the linter is v2 before trusting it** (#26).
- `go.sum` must never appear.
- **AR-008: no refactoring outside the task.**
- **A task is not done when the code exists. It is done when something calls it.**
- **A new guard must be proven by breaking it.**
- **`install.sh` names nobody** (FR-020): no personal name, no account identifier,
  no `/home/<someone>` path. The repository owner in a URL is fine and unavoidable.

---

## Tasks

- [x] **T001** 🔒 Generate `shared_secret` in `install.sh` when writing a **new** configuration, and write it in rather than leaving it commented. Use a real CSPRNG (`openssl rand -hex 32`, or `/dev/urandom` if openssl is not a dependency the installer already has — check `require_tools` before adding one). **Never print it.** Say that a secret was generated, not what it is. An existing config is still never touched. Extend `verify-install` to assert: the written config contains a `shared_secret` of at least 32 bytes, the file is `0600`, the secret does **not** appear in the installer's stdout, and the second run leaves it byte-identical.

- [ ] **T002** Set `allowed_roots` in `install.sh` for a new configuration, to the default the config file already names (`$HOME/code`), creating the directory if it is absent. **Never `$HOME` itself** — the config's own comment says why: SSH keys, cloud credentials and browser profiles live directly under it, which would make the allowlist decorative. Write it explicitly rather than relying on a default, so the operator can see what their containment is. Extend `verify-install` to assert the directory exists and the config names it.

- [ ] **T003** Reduce what is left after install to as close to nothing as it can honestly be, and rewrite `next_steps()` to say it. With T001 and T002 the daemon can now start, so the remaining gap is Cloudflare Access — until it is configured the dashboard admits nobody, which is a working daemon serving no one rather than a broken one. **Decide and record whether the installer should now enable the unit.** The existing reasoning (a service failing on first boot teaches an operator to ignore a failing service) was written against an incomplete config; say whether it still holds and why. Do not change the behaviour without writing the argument down.

- [ ] **T004** Rewrite `README.md` for a stranger. Lead with what it is, then install, then the four secrets and what each buys (table above), then configure, then run. **Move the contributor material out** — "Working in this repo", "Planning a milestone", "Running a loop" are ~46 lines of internal workflow sitting between the install steps and the configuration reference; they belong in `CONTRIBUTING.md`. Keep the voice this project already has: plain, direct, willing to say why. Do not invent a marketing register.

- [ ] **T005** Make the Cloudflare Access setup a crisp, ordered sequence in the README.
  **The concrete detail below was verified against the code — use it rather than
  re-deriving it, and do not soften it into generalities.**

  | What | Value / where it comes from |
  |---|---|
  | `access_team_domain` | `<team>.cloudflareaccess.com`. **Origin only** — `loadAccessOrigin` refuses a path, query, fragment or credentials. |
  | The key set | The daemon fetches `<team_domain>/cdn-cgi/access/certs` (`internal/access/keys.go`, `certsPath`). Say so: it explains why the team domain must be exact. |
  | `access_aud` | The application's **Application Audience (AUD) Tag**, on its Overview page. Compared for equality, never parsed. |
  | `access_allowed_emails` | Comma-separated. An entry that is empty or contains a space refuses. |

  **One Access application, two policies** — this is the part that costs an
  operator an afternoon:

  | Policy | Action | Rule | Serves |
  |---|---|---|---|
  | Browser | **Allow** | Emails → the operator's address | The dashboard |
  | API | **Service Auth** | Service Token | The companion skill and scripts |

  The API policy's action must be **Service Auth**, not Allow. A service token's
  assertion carries `common_name`, an empty `sub`, and **no email** — which is why
  "no email" must never read as "allow", and why a service-token assertion
  presented to the browser door is refused exactly as a stranger's is.

  Also give the ingress the tunnel needs (`deploy/cloudflared.example.yml`):
  hostname → `http://127.0.0.1:8765`, with a `http_status:404` catch-all. The
  daemon binds loopback only; the tunnel is the sole way in.

  **Name the failure an operator actually hits**: all three Access values or none.
  Set none and the daemon *starts*, warns, and admits nobody to the dashboard —
  a working daemon serving no one, which looks healthy and is the worst version.

  Original wording, still binding: — it is the one part that cannot be automated, so it must at least be followable without guessing. Team domain, AUD, allowed emails, and the two edge policies (identity for the browser, service token for the API). Say what each is for. Name the failure an operator will actually hit: the two assertion shapes are not interchangeable, and a service token presented to the browser door is refused exactly as a stranger's is.

- [ ] **T006** Fix `#129` — `.env.example` says the idle timeout and absolute session lifetime "are constants in the code, not variables" about 120 lines below listing `CRSW_SESSION_LIFETIME` and `CRSW_SESSION_LIFETIME_MAX` as variables. **Correct the claims that stopped being true and keep the ones that did not**: the signed-request timestamp window really is a constant, and there really is no variable that disables Access validation. Do not delete the section.

---

## Out of scope

- **Automating Cloudflare Access.** It is configuration on somebody else's service.
- **Auto-starting the daemon**, unless T003 argues for it explicitly and writes the
  argument down.
- **Packaging (apt, brew, nix).** A tarball and an install script, as specced.
- **#120, #121.** Unchanged. **Q2** is still the operator's to answer.
