# Quickstart: Validating Dashboard Actions

**Feature**: [spec.md](./spec.md) · **Plan**: [plan.md](./plan.md) · **Contracts**: [contracts/](./contracts/)

How to prove milestone 3 works. One section per user story, each independently runnable. This is
the acceptance procedure the tasks must satisfy, not implementation.

---

## Prerequisites

```bash
export PATH="$(go env GOPATH)/bin:$PATH"   # ahead of any older golangci-lint (see #26)
golangci-lint --version                    # MUST report v2.x — a v1 binary passes silently
```

A v1 binary reads the v2 config, runs zero linters, and exits 0. If the session-start hook warned
about this, lint is unverified until it is fixed.

## The gate — every change must pass all of these

```bash
go build ./...
go vet ./...
go test ./...
golangci-lint run
go test -tags tmux ./...
go test -tags quickstart ./cmd/crswd
```

`go.sum` must not exist at any point.

---

## Story 1 (P1) — Destroy, and prove nothing else can

**The refusals matter more than the happy path.** Each must return the byte-identical uniform
`403` from [contracts/actions.md](./contracts/actions.md):

```bash
# wrong origin
curl -si -X POST "$BASE/dashboard/sessions/$ID/destroy" \
  -H "Cf-Access-Jwt-Assertion: $JWT" -H 'Sec-Fetch-Site: cross-site' \
  -d "confirm=yes&crsw_page_token=$TOK"

# no origin header at all — absence is not evidence
curl -si -X POST "$BASE/dashboard/sessions/$ID/destroy" \
  -H "Cf-Access-Jwt-Assertion: $JWT" -d "confirm=yes&crsw_page_token=$TOK"

# no token
curl -si -X POST "$BASE/dashboard/sessions/$ID/destroy" \
  -H "Cf-Access-Jwt-Assertion: $JWT" -H 'Sec-Fetch-Site: same-origin' -d "confirm=yes"

# expired token, and a token whose expiry was edited forward
# another identity's token
```

Diff the **full** responses including headers. A difference in `Content-Length` alone is a
disclosure (spec glossary, *uniform response*).

Then confirm each half is independently load-bearing — disable one in a build, confirm the other
still refuses, and confirm a test fails. **Never disable a check to make a test pass** (AR-005).

**Happy path**, then verify against the host rather than the page:

```bash
tmux -L "crswd-$(echo "$CRSW_LISTEN" | tr '.:' '--')" ls    # the session is gone
```

**Unverified teardown** is a `409` with the record retained. It cannot be produced by hand; the
fake controller covers it.

---

## Story 2 (P2) — Create from the browser

Submit the create form, then assert the negative that matters:

```bash
curl -s "$BASE/" -H "Cf-Access-Jwt-Assertion: $JWT" | grep -c "$KNOWN_TOKEN"   # → 0
```

The per-session bearer token must appear in **no** served byte (FR-013). Boundary refusals — a name
with `:`, a `work_dir` traversal, an absolute path outside the roots, a symlink escaping them, a
non-directory — are all `400` with **one** message. Diff them: distinguishing "does not exist" from
"not permitted" is a filesystem oracle.

---

## Story 3 (P3) — The fleet stays current

The point of #15 is changes this page did not cause:

```bash
# with a dashboard open, drive everything through the API
~/bin/crswd-api POST /sessions '{"name":"fleet-check","work_dir":"'"$HOME"'/code"}'
```

Assert an `appeared` event carrying that id arrives on the open stream. Then let the reaper take an
idle session and assert `vanished`. Then sever the stream and assert the page **says so** rather
than continuing to present a fleet it cannot vouch for.

Ownership is the security assertion here:

```bash
# with identity A's stream open, create a session owned by the synthetic second owner
# assert: zero bytes about it reach A's stream
```

---

## Story 4 (P4) — Rename changes only the record

```bash
tmux -L "$SOCKET" ls | grep "crswd-$ID"    # unchanged after rename
```

Then run every identifier-based operation and assert unchanged behaviour (SC-012).

---

## Story 5 (P5) — Compact is delivered, not claimed

Assert the response says **delivered**. A response claiming the compaction happened is a defect
(FR-016a) — the daemon cannot see what the assistant is carrying.

```bash
grep -c "compact" /tmp/audit.jsonl    # → 0: the delivered text is never audited
```

---

## Story 6 — The audit trail leaks nothing

```bash
jq -r 'select(.action) | [.time,.action,.caller,.session_id,.decision] | @tsv' /tmp/audit.jsonl
```

Exactly one record per request. Then prove the negatives — this is SC-008 and it must be a test,
not an eyeball:

```bash
grep -c "$CRSW_SHARED_SECRET" /tmp/audit.jsonl   # → 0
grep -c "$TOK"                /tmp/audit.jsonl   # → 0  (bearer token)
grep -c "crsw_page_token"     /tmp/audit.jsonl   # → 0
grep -c "/compact"            /tmp/audit.jsonl   # → 0
grep -c $'\033'               /tmp/audit.jsonl   # → 0  (pane escapes)
```

And confirm `dashboard.destroy` is distinguishable from `session.destroy` (FR-024).

---

## Definition of done for the milestone

- [ ] Every gate command green, including all three build tags
- [ ] Every story above validated end to end
- [ ] Every contract test in [contracts/](./contracts/) present, each failing without its change
- [ ] `go.sum` still absent
- [ ] Both halves of the cross-site defence proven independently load-bearing
- [ ] No secret, token, page token, prompt, compact text, or pane content in any log line, asserted by test
- [ ] `docs/security.md` and `docs/auth-and-sessions.md` pre-merge checklists satisfied
