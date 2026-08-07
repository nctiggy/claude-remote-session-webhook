# Quickstart: Proving milestone 4 by hand

How to verify each user story on a real daemon. Every scenario below maps to a
success criterion, so a scenario that passes is evidence and not just a demo.

## Prerequisites

```bash
cd /home/nctiggy/code/claude-remote-session-webhook
go build ./... && go vet ./... && go test ./...
golangci-lint run                      # must be v2.12.2 — see below
```

> **Check the linter version before trusting a green run.** v1.62.2 reads this
> repository's v2 config, runs **zero** linters, and exits 0. A silent false
> green is worse than a red one. `golangci-lint --version` must print `2.12.2`.

Run the daemon on its own socket so it cannot adopt or reap the sessions you are
already using:

```bash
CRSW_LISTEN=127.0.0.1:8799 ./crswd     # a distinct port means a distinct tmux socket
```

---

## US1 — Configure the daemon in a file (SC-001, SC-002, SC-003)

```bash
mkdir -p ~/.config/crswd
cat > ~/.config/crswd/config <<'EOF'
version = 1
allowed_roots = /home/nctiggy/code
idle_timeout = -1
shared_secret = hunter2#not-a-comment
EOF
chmod 600 ~/.config/crswd/config
```

| Step | Expected |
|---|---|
| Start the daemon | Starts. `allowed_roots` and `idle_timeout` take effect **without touching the service definition** — SC-001. |
| `mv ~/.config/crswd/config{,.away}` and restart | Starts and behaves exactly as before the milestone — SC-002. |
| Add `allowd_roots = /tmp` (typo) and restart | **Refuses**, naming the key and the line. Never accepted-and-ignored — SC-003. |
| `chmod 644` the file and restart | **Refuses**, naming the file and `chmod 600`. |
| Remove `shared_secret`, `chmod 644`, restart | **Starts** — mode only matters when a secret is present. |
| Check the file's mtime after every start | Unchanged. The daemon never writes it (FR-008). |

Verify the secret survived the `#`:

```bash
curl -sf -H "Authorization: Bearer hunter2#not-a-comment" http://127.0.0.1:8799/sessions
```

A 401 here means the parser stripped a trailing comment and truncated the secret
— the exact failure the grammar exists to prevent.

---

## US2 — See what it is configured to do (SC-004, SC-005)

Open `/settings` in the browser.

| Check | Expected |
|---|---|
| Every configured key is listed | Yes |
| `shared_secret` and `allowed_identities` | `present` or `absent` — **never** a value, prefix, or length |
| The file that was read | Named at the top, verbatim |
| Source column | `file` for the keys above |
| Now set `CRSW_LISTEN` in the environment and restart | That row reads `environment` — this is what answers "why is my change not applied?" (SC-004) |

Prove SC-005 mechanically rather than by eye:

```bash
# exercise every route, then search everything returned for the secret
grep -R 'hunter2' /tmp/crswd-sweep-output && echo "LEAK" || echo "clean"
```

---

## US3 — The dashboard behaves without script (SC-006)

Disable JavaScript entirely, then perform each of the four actions.

| Action | Expected |
|---|---|
| Create | Lands on a usable page, outcome stated in words |
| Destroy | Same |
| Compact | Same — **this is the white-page bug** that made the milestone necessary |
| Rename | Same |
| An unauthorised action | The uniform refusal, **not** a redirect (FR-025) |

The last row is the security check hiding in a usability story: redirecting an
unauthorised caller tells them their request was processed.

---

## US4 — Turn remote control on and off (SC-007)

| Step | Expected |
|---|---|
| Create a session in local mode | Card shows the mode **in words** |
| Toggle to remote, without confirming | Refused; mode unchanged |
| Toggle to remote, confirming | Succeeds |
| Scrollback | **Still there** — the pane was not recreated |
| Ask the session something about earlier in the conversation | It answers — `--continue` worked, SC-007 |
| Session id, credential, lifetime | Unchanged |
| Restart the daemon | Mode is still remote — `@crswd-start` survived |

---

## US5 — Pick a working directory (SC-008, SC-009)

| Step | Expected |
|---|---|
| Open the create form **with JavaScript disabled** | The field offers suggestions and filters as you type — the platform's own datalist |
| Type a path not in the list | Accepted if allowlisted (FR-040) |
| Choose `/home/nctiggy/code/customer-opportunities` from the list | Starts — SC-009, the directory that used to cost a search |
| Add a path outside the roots to the suggestion list, then choose it | **Refused.** A suggestion is never an authorisation (FR-042) |
| Unset `discover_roots` | Only explicit entries appear (FR-041) |

---

## US6 — The card split (SC-010)

| Check | Expected |
|---|---|
| Click anywhere in the readable block | Navigates — the whole block, not the name alone |
| Count links per card | Exactly one |
| Select text inside the readable block | Does **not** navigate (FR-051) |
| Rename on the fleet | Absent |
| Rename on the session page | Present, revealed on request |
| With `prefers-reduced-motion` | Nothing animates |
| Tab through every control | Visible focus ring on each |

---

## Dependency check (SC-011)

```bash
env PATH=/nonexistent ./crswd
```

Refuses, naming `tmux` and the install command for **this** platform, read from
`/etc/os-release` rather than guessed. Then configure a start command whose
binary does not exist: the daemon **starts** and warns, because it can still
serve the dashboard.

---

## The invariants, checked every iteration

```bash
test ! -f go.sum && echo "SC-012 ok"      # zero dependencies
go build ./... && go vet ./... && go test ./...
go test -tags tmux ./... && go test -tags quickstart ./...
golangci-lint run                          # v2.12.2
```

One audit record per request, allowed or denied (SC-013) — assert it in tests
rather than by reading a journal; a count is a test and an eyeball is not.
