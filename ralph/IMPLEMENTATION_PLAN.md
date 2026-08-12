# Implementation Plan

**Milestone 14 — Say what is true, then say it clearly.**

> *"The explanations in the config.example are very long and not super human
> readable… the readme should really include all the instructions needed to
> complete an install."*

Eight tasks, ordered by a Fable 5 audit that read every markdown file,
`config.example`, `.env.example`, and the six test files that pin docs to code.

---

## Wrong beats wordy

The audit disagreed with the premise, usefully. `config.example` **is** hard to
read — but the cause is **ordering, not length**: nearly every block leads with the
justification and buries the operative fact. And the worst problems in the doc set
are not verbosity at all. **Fix the false statements first.**

| File | What it says | What is true |
|---|---|---|
| `deploy/README.md:14` | The daemon "refuses to start without" the three `CRSW_ACCESS_*` values | Lines 38–41 of the same file say setting none is supported. It is. |
| `AGENTS.md:22` | `web/` holds "Templates, htmx, CSS"; a `skill/` directory exists | There is no htmx (`docs/components.md` says so emphatically) and no `skill/` |
| `AGENTS.md:10` | The browser door is Cloudflare Access | Milestone 12 added the password door |
| `AGENTS.md:50`, `CONTRIBUTING.md:22-27` | CI runs the untagged commands "and nothing else" | The tmux and quickstart suites run too |

**`AGENTS.md` is the first file every agent loads.** In a Ralph-loop project a
stale one is compounding error — every iteration of every milestone begins by
reading it — which is why it is T001 rather than a tidy-up at the end.

---

## ⚠️ The docs are test fixtures. Do not move them.

`config.example`, `.env.example`, the README's configuration table,
`docs/design-system.md`'s tokens and `docs/components.md`'s class names are read
**at relative paths** and held to the code **in both directions**.

- `internal/config/file_test.go` — `config.example`, one `# key = value` line per
  key, in `config.Vars()` order, each parsing to exactly the value shown
- `internal/config/envexample_test.go` — `.env.example`, names every variable,
  carries no values
- `internal/config/docs_test.go` — the README's table, one row per `CRSW_` variable
- `internal/httpapi/stylesheet_test.go` — component classes and design tokens
- `internal/release/readme_test.go` — the install one-liner verbatim and first
- the quickstart suite — every documented `journalctl` command

**A rewrite that renames a key, drops one, or reorders `Vars()` fails the suite.**
That is the guard working, not an obstacle.

**A landmine for T003:** any comment line beginning `# <known_key> = …` counts as
that key's line. Prose like `# idle_timeout = 0 disables nothing` fails as a
duplicate. Use the env spelling in examples (`CRSW_IDLE_TIMEOUT=…`) as
`.env.example` already does, or do not lead with the key.

---

## ⚠️ What must survive a rewrite

The audit named these load-bearing. They are long because the reasoning is the
point, and compressing them is how the next person "simplifies" a security
property into a bug.

- `config.example` — `$HOME` is never the default root (SSH keys, cloud
  credentials, browser profiles live directly under it); no trailing comments
  because a secret may contain `#`; first-`=` separator because `start_commands`
  carries `=` in its value; the listen/door invariant; the password crossing a LAN
  in clear; what `;`, control characters and `{name}` mean in a start command.
- `docs/security.md` and `docs/auth-and-sessions.md` — **essentially whole**. Why
  method and path are in the signed payload (a live milestone-1 bug), *"no email
  must never read as allow"*, the bounds on the two pre-layer-1 login routes, why
  the page token is stateless and `pageKey` unrelated to the shared secret, the
  `$SHELL -l` probe trade.
- `README.md` — the security posture section, "Never both", the TLS warning
  including the `Secure`/`X-Forwarded-Proto` paragraph, signature-before-executable
  ordering, the rollback recipe, both halves of "Verifying the exposure model".
- `deploy/README.md` — the unit rationale (`KillMode=process`, `TimeoutStopSec`,
  no `PrivateTmp`), the `journalctl | grep '^{'` explanation, the `crswd-api` zsh
  `path` hazard.

**The voice stays.** "Says why, not just what" is right for this project. The fix
is *order* — fact, bounds, default, then why — not deleting the why.

---

## Conventions

- `- [ ]` open · `- [x]` done · `- [!]` blocked (reason in `PROGRESS.md`)
- Priority order is meaningful — the loop always takes the topmost open item.
- **Every task ends green**: `go build ./... && go vet ./... && go test ./... && golangci-lint run`,
  plus `-tags quickstart` for anything touching a documented command.
- **Check the linter is v2 before trusting it** (#26).
- **AR-008: no refactoring outside the task.**
- **Verify a claim before writing it down.** This milestone exists because four
  files asserted things nobody checked.

---

## Tasks

- [x] **T001** Fix `AGENTS.md`. Remove `htmx` and the `skill/` directory from the project map; correct the browser-door description to name both doors; correct the CI claim to include the tmux and quickstart suites. **Check every other claim in the file against the tree while you are there** — it is the file every agent reads first, and it has been wrong in four places at once.

- [x] **T002** Fix `deploy/README.md:14` and `CONTRIBUTING.md:22-27`. The Access values are optional; setting none is a supported deployment that admits nobody to the dashboard. Say which deployment the file is for at the top, and note that a LAN operator wants the password door instead. Reposition the 1Password paths as **one example of a shape** — a secret in a manager, `EnvironmentFile` under `umask 077`, never in the unit — rather than the procedure.

- [x] **T003** Reshape `config.example` to lead with the fact. Per key: **name and what it does; format, bounds and what a wrong value does; then the why where the why is load-bearing; then the default; then the one commented line.** Cut the 81-line preamble to roughly 30 — keep the file locations, the `CRSW_CONFIG_FILE` relative-path trap, `crswd config check`, the `config.bak` fallback, one precedence line, and the three format rules; replace the "why not JSON/YAML/TOML" argument with a pointer to `docs/security.md` §5. Target ~215 lines from 401. **Mind the duplicate-key-line landmine above, and keep every passage named as load-bearing.**

- [x] **T004** Restructure `README.md`'s install into **two numbered paths, chosen up front**: "on the internet" (Access) and "on a network you control" (password). Add the prerequisites nobody is told: Linux with a systemd user session, `tmux`, and **`claude` installed and authenticated on the host** — the device-code relay is not built, so a session that hits a login prompt is stuck. Add one troubleshooting line pointing at `journalctl --user -u crswd -e`, since the daemon's refusals are its best operator feature and nothing points at them.

- [x] **T005** Write the Access path end to end, as a stranger must follow it: install and authenticate `cloudflared`; `cloudflared tunnel create`; **route DNS to the tunnel** (mentioned nowhere today); what to edit in `cloudflared.example.yml` — the hostname and the service URL, since the README says "then edit" and never says what; create the Access application (self-hosted, on that hostname); configure the identity provider; **both policies — an identity Allow and a Service Auth for the API**, which are required today and shown nowhere; where the AUD tag lives; the service token; the four config lines; `crswd config check`; restart; and finally **"browse to https://your-hostname/"**, which the README never says.

- [x] **T006** Reconcile the LAN path with the installer. It currently shows a config from scratch, but a one-line-install reader already has `shared_secret` and `allowed_roots` written — the real edit is **adding** `dashboard_password` and `listen`. Say that. Note the installer already wrote the file `0600`, and fold `crswd config check` in as the pre-restart verification.

- [x] **T007** Trim `README.md`'s duplication. The startup probe and login-shell `PATH` material is stated three times (here, `deploy/README.md`, `docs/security.md`) — security.md owns the why, deploy owns the operational consequence, the front page gets two sentences and a link. Move the two API-door bullets out of the operator's install reading. Cut the twelve-milestone roadmap: "what is not built yet" already exists in two sentences near the top.

- [x] **T008** Compress `docs/components.md`'s self-history. Four passages narrate the document's own revisions ("That paragraph replaced one asserting the opposite…"). The lesson each carries is already encoded in `stylesheet_test.go`; keep one sentence plus the issue number per site and lose the memoir. **Do not touch any class name** — the both-directions sweep reads them.

---

## Out of scope

- **A documentation site.** The audit's answer is no, and the reason is that these
  docs are test fixtures: moving them breaks the guards, copying them creates the
  one unguarded copy that rots. If navigation is the itch, a short "which file
  answers what" index costs nothing — that is T004's business, not a toolchain's.
- **`docs/security.md` and `docs/auth-and-sessions.md`.** Binding correctness
  specs, correctly served by their length.
- **#120, #121.** Unchanged. **Q2** is still the operator's to answer.
