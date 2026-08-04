# Quickstart: Validating crswd Daemon Core

**Feature**: [spec.md](./spec.md) · **Plan**: [plan.md](./plan.md) · **Contracts**: [contracts/](./contracts/)

How to prove milestone 1 actually works. One section per user story, each independently
runnable. Nothing here is implementation — it is the acceptance procedure the tasks must
satisfy.

---

## Prerequisites

| Requirement | Check | Status on this host |
|---|---|---|
| Go 1.23+ | `go version` | ✅ go1.24.0 (compiles the 1.23 module) |
| tmux | `tmux -V` | ✅ tmux 3.4 |
| `golangci-lint` v2.12.2 | `golangci-lint --version` | ❌ **not installed** — see below |
| `goimports` | `which goimports` | ❌ **not installed** |

```bash
# Install the two missing tools before claiming lint is green.
go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.12.2
go install golang.org/x/tools/cmd/goimports@latest
export PATH="$PATH:$(go env GOPATH)/bin"
```

Until these exist, `.claude/hooks/format-and-lint.sh` no-ops and `golangci-lint run`
cannot run at all — "lint passed" would mean "lint did not execute". CI enforces both
regardless ([research D12](./research.md)).

---

## The gate — every change must pass all four

These are the `AGENTS.md` commands, and exactly what CI runs.

```bash
go build ./...
go vet ./...
go test ./...
golangci-lint run
```

---

## Setup

```bash
export CRSW_SHARED_SECRET="$(head -c 48 /dev/urandom | base64)"   # ≥32 bytes
export CRSW_ALLOWED_ROOTS="$HOME/code"
export CRSW_LISTEN="127.0.0.1:8765"

go build -o /tmp/crswd ./cmd/crswd && /tmp/crswd
```

A signing helper, since every request needs one (keep it out of the repo — it holds the
secret in its environment):

```bash
crswd_call() {  # crswd_call METHOD PATH [JSON_BODY] [BEARER]
  local method="$1" path="$2" body="${3-}" bearer="${4-}"
  local ts sig
  ts=$(date +%s)
  # METHOD "\n" PATH "\n" timestamp "." body — the request line is signed too, so
  # one signed read is not a valid destroy at the same instant.
  sig=$(printf '%s\n%s\n%s.%s' "$method" "$path" "$ts" "$body" \
        | openssl dgst -sha256 -hmac "$CRSW_SHARED_SECRET" -hex \
        | awk '{print $NF}')
  curl -sS -X "$method" "http://127.0.0.1:8765$path" \
    -H "X-CRSW-Timestamp: $ts" \
    -H "X-CRSW-Signature: sha256=$sig" \
    ${bearer:+-H "Authorization: Bearer $bearer"} \
    ${body:+-H "Content-Type: application/json" -d "$body"}
}
```

---

## Story 1 (P1) — Start a session, and prove nothing else can

**Happy path**

```bash
crswd_call POST /sessions '{"name":"demo","work_dir":"'"$HOME"'/code/claude-remote-session-webhook"}'
```

Expect `201` with `id`, `expires_at` exactly 24h after `created_at`, and `token` — the
only time it is ever shown. Confirm the session really exists:

```bash
tmux has-session -t "=crswd-<id>" && echo "live"
```

**The refusals that matter more.** Each must return the *identical* `401`:

```bash
# unsigned
curl -sS -i -X POST http://127.0.0.1:8765/sessions -d '{"name":"x","work_dir":"'"$HOME"'/code"}'
# tampered body (sign one thing, send another)
ts=$(date +%s); sig=$(printf 'POST\n/sessions\n%s.%s' "$ts" '{"name":"a"}' | openssl dgst -sha256 -hmac "$CRSW_SHARED_SECRET" -hex | awk '{print $NF}')
curl -sS -i -X POST http://127.0.0.1:8765/sessions -H "X-CRSW-Timestamp: $ts" \
  -H "X-CRSW-Signature: sha256=$sig" -d '{"name":"b","work_dir":"/tmp"}'
```

Stale, far-future, and replayed requests — all `401`, and critically **no session
created** by any of them:

```bash
# replay: capture a valid create and send the exact bytes twice; the second must fail
# and `GET /sessions` must show exactly one new session
```

**Boundary refusals** — all `400`, no session created:

```bash
crswd_call POST /sessions '{"name":"bad:name","work_dir":"'"$HOME"'/code"}'          # colon
crswd_call POST /sessions '{"name":"ok","work_dir":"'"$HOME"'/code/../../etc"}'      # traversal
crswd_call POST /sessions '{"name":"ok","work_dir":"/etc"}'                          # outside roots
crswd_call POST /sessions '{"name":"ok","work_dir":"'"$HOME"'/code","extra":1}'      # unknown field
ln -s /etc /tmp/escape 2>/dev/null; \
crswd_call POST /sessions '{"name":"ok","work_dir":"/tmp/escape"}'                    # symlink escape
```

**The loud default** (SC-015):

```bash
unset CRSW_ALLOWED_ROOTS && /tmp/crswd 2>&1 | head -5
```

Expect a prominent warning naming `CRSW_ALLOWED_ROOTS` and `$HOME/code`. It must appear
on **every** start that defaults, not just the first.

**Startup failures** (SC-014) — non-zero exit, nothing bound:

```bash
CRSW_SHARED_SECRET="" /tmp/crswd; echo "exit=$?"       # unset
CRSW_SHARED_SECRET="tooshort" /tmp/crswd; echo "exit=$?"  # <32 bytes
CRSW_LISTEN="0.0.0.0:8765" /tmp/crswd; echo "exit=$?"     # not loopback
```

---

## Story 2 (P2) — Drive a session, hostilely

The test that matters is the one `send-keys` would fail ([D4](./research.md)):

```bash
ID=<id>; TOK=<token>
crswd_call POST "/sessions/$ID/prompt" '{"text":";"}'          "$TOK"   # a lone semicolon
crswd_call POST "/sessions/$ID/prompt" '{"text":"foo;"}'       "$TOK"   # trailing semicolon
crswd_call POST "/sessions/$ID/prompt" '{"text":"a; echo PWNED; $(id) `whoami`"}' "$TOK"
crswd_call GET  "/sessions/$ID/output" '' "$TOK"
```

Assert: every payload appears **byte-for-byte** in the output, `PWNED` appears only as
literal text, and no extra command ran. A lone `;` that arrives as an empty string is the
exact regression this milestone is designed to prevent.

Assert the output is plain text — no `ESC[` byte sequences:

```bash
crswd_call GET "/sessions/$ID/output" '' "$TOK" | grep -c $'\033' || echo "clean"
```

---

## Story 3 (P3) — Isolation and verified teardown

**Cross-owner** (needs the synthetic second owner from the tests):

```bash
crswd_call GET "/sessions/$OTHER_ID" '' "$TOK"     # another owner's id, our token
crswd_call GET "/sessions/$ID"       '' "$WRONG"   # our id, wrong token
crswd_call GET "/sessions/00000000000000000000000000000000" '' "$TOK"   # nonexistent
```

All three must be **byte-identical** `404`s. Diff the full responses including headers —
a difference in `Content-Length` alone is an enumeration oracle (SC-005).

**Verified teardown**:

```bash
crswd_call DELETE "/sessions/$ID" '' "$TOK"
tmux has-session -t "=crswd-$ID" 2>&1     # must report: can't find session
```

The `409` path — kill issued, session survives — cannot be produced by hand; it is
covered by the fake controller in unit tests (see [contracts/tmuxctl.md](./contracts/tmuxctl.md)).

---

## Story 4 (P4) — Restart without orphans

```bash
# 1. create a session, note its id
# 2. kill the daemon (not the tmux session)
kill "$(pgrep -f /tmp/crswd)"
tmux ls | grep crswd-        # the session is still alive — this is the danger state

# 3. plant a lookalike the daemon must NOT touch
tmux new-session -d -s "crswd-notours-by-hand"

# 4. restart
/tmp/crswd &
crswd_call GET /sessions
```

Assert: the real session is listed, owned, and destroyable; `crswd-notours-by-hand` is
absent from the listing **and still alive** in tmux (FR-022); driving the adopted session
needs the newly issued token, and the pre-restart token is dead (FR-021).

The 24-hour ceiling across restarts (SC-009) is a unit test — the fake reports a
`session_created` 23 hours old and the reaper must fire an hour later, not 24 hours after
adoption.

---

## Story 5 (P5) — Bounds hold

```bash
for i in $(seq 1 6); do
  crswd_call POST /sessions '{"name":"cap-'"$i"'","work_dir":"'"$HOME"'/code"}'
done
```

The 6th must be `429` with `CRSW_MAX_SESSIONS=5`, and the first five must be unaffected.
Rate limiting likewise `429`s a burst.

Idle and absolute expiry are unit tests on the injected clock — advance past 60m and 24h
and assert destruction with verified teardown. **No test sleeps** (FR-039).

```bash
kill -TERM "$(pgrep -f /tmp/crswd)"
tmux ls 2>&1 | grep -c crswd-      # must be 0: shutdown reaps with verification
```

---

## Story 6 (P6) — The audit trail leaks nothing

```bash
/tmp/crswd > /tmp/audit.jsonl 2>&1 &
# exercise every endpoint, including failures, then:
jq -r 'select(.action) | [.time,.action,.caller,.session_id,.decision] | @tsv' /tmp/audit.jsonl
```

Assert one record per request, then prove the negative — this is SC-013 and it must be a
test, not an eyeball:

```bash
grep -c "PWNED"                    /tmp/audit.jsonl   # prompt text        → 0
grep -c "$CRSW_SHARED_SECRET"      /tmp/audit.jsonl   # shared secret      → 0
grep -c "$TOK"                     /tmp/audit.jsonl   # bearer token       → 0
grep -c $'\033'                    /tmp/audit.jsonl   # pane escapes       → 0
```

---

## Definition of done for the milestone

- [ ] `go build ./...`, `go vet ./...`, `go test ./...`, `golangci-lint run` all green
- [ ] Every story above validated end to end
- [ ] Every contract test in [contracts/http-api.md](./contracts/http-api.md) present and failing without its change
- [ ] `go.sum` still absent — zero third-party dependencies ([D7](./research.md))
- [ ] No secret, token, prompt, or pane content in any log line, asserted by test
- [ ] `docs/security.md` pre-merge checklist satisfied
- [ ] `docs/auth-and-sessions.md` pre-merge checklist satisfied
