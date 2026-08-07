# Quickstart: Proving milestone 5 by hand

Every scenario maps to a success criterion. Several are deliberately checks on
**rendered markup** rather than on behaviour — that is the correction milestone 4
earned, where three green tasks left the control they were about unchanged.

## Prerequisites

```bash
cd /home/nctiggy/code/claude-remote-session-webhook
go build ./... && go vet ./... && go test ./...
go test -tags tmux ./... && go test -tags quickstart ./...
golangci-lint run          # must print 2.12.2 — v1 reads the v2 config and lints nothing
```

Run on a separate port so the daemon gets its own tmux socket and cannot touch
your live sessions:

```bash
CRSW_LISTEN=127.0.0.1:8799 ./crswd
```

---

## US1 — Remote control at create time (SC-001)

**The markup check comes first, because this is what milestone 4 missed:**

```bash
curl -s <dashboard> | grep -c 'name="start_command"'     # must be 0
curl -s <dashboard> | grep -c 'name="remote_control"'    # must be 1
for n in default rc; do curl -s <dashboard> | grep -c "$n"; done   # must all be 0
```

| Step | Expected |
|---|---|
| Open the create form | A switch labelled Remote control. No dropdown of command names anywhere. |
| Create with it **off** | Session is local; the card says so in words |
| Create with it **on** | Session is remote; the card says so |
| `curl -d 'remote_control=rc' …` | **Uniform refusal.** `rc` is a real configured name and is still refused — the field carries a mode, not a name. |
| Tab to the switch | Visible focus ring; Space toggles it |

---

## US2 — Directory suggestions (SC-002)

```bash
# with ONLY allowed_roots configured — no workdir_suggestions, no discover_roots
curl -s <dashboard> | grep -c '<option value='    # must be >= 1
```

| Step | Expected |
|---|---|
| Default install, roots configured | The roots are offered |
| Add `workdir_suggestions = /srv/scratch` | It joins the list |
| A path in two sources | Appears once |
| Reload twice, diff the markup | Identical — sorted, not map order |
| `discover_roots` unset | No child directories offered |
| Choose an offered path **outside** the roots | **Refused**, same response and same audit record as typing it |

That last row is the security half. A suggestion is never an authorisation.

---

## US3 — Reaching settings (SC-003)

| Step | Expected |
|---|---|
| Any page | A visible Settings link in the header |
| The header's first anchor | Still the `crswd` wordmark, `href="/"` |
| Count anchors in the header | Exactly two |
| `curl -X POST <base>/settings` | `405` — reachable is not editable |

---

## US4 — The themed picker (SC-005, SC-007)

**With scripting fully disabled** — this is the row that matters most:

| Step | Expected |
|---|---|
| Open the create form | The directory field works and offers the browser's own suggestions |
| Type a path not offered | Accepted if allowlisted |
| Submit | Session starts |

Then **with scripting on**:

| Step | Expected |
|---|---|
| Focus the field and type | A themed list, styled like the rest of the page — not the browser's |
| Only one popup appears | The native one is suppressed |
| `↓` `↑` `Enter` `Escape` | Move, accept, dismiss; typed text survives Escape |
| Narrow the list | It says how many of how many are showing |
| `prefers-reduced-motion` | Nothing animates |
| Grep the stylesheet for a literal colour in combo rules | None |

---

## US5 — The conversation field is gone (SC-004)

```bash
curl -s <dashboard> | grep -c 'name="resume"'   # must be 0
```

| Step | Expected |
|---|---|
| Open the create form | No conversation field |
| Create a session | Starts fresh, as before |
| `curl -d 'resume=$(whoami)' …` | Ignored or refused; **never** reaches a command line |
| `grep -r listConversations .` | No hits — the code went with the field |

---

## Diagnostics and the probe (SC-008, SC-008a)

The documented command, verbatim from `deploy/crswd.example.service`:

```bash
journalctl --user -u crswd -o cat | grep '^{' | jq . > /dev/null && echo ok
```

| Step | Expected |
|---|---|
| Run it on a daemon that has logged records **and** warnings | Clean |
| `journalctl --user -u crswd -o cat \| grep -v '^{'` | Diagnostics only — and none on stdout |
| Start with `claude` on the login shell's path but not the service manager's | **No warning that it is missing.** This is the live case #96 was filed for. |
| Start with `claude` on neither | Still warns, and names what it checked |
| `env PATH=/nonexistent ./crswd` | Still refuses, naming tmux and the install command |

---

## The invariants, every iteration

```bash
test ! -f go.sum && echo "SC-010 ok"
go build ./... && go vet ./... && go test ./...
go test -tags tmux ./... && go test -tags quickstart ./...
golangci-lint run
```

One audit record per request (SC-009) — asserted in tests, never eyeballed in a
journal.
