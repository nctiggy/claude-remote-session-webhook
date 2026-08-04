# Quickstart: Validating Access Validation & the Read-Only Dashboard

**Feature**: [spec.md](./spec.md) · **Plan**: [plan.md](./plan.md) · **Contracts**: [contracts/](./contracts/)

How to prove milestone 2 actually works. One section per user story, each
independently runnable. Nothing here is implementation — it is the acceptance
procedure the tasks must satisfy.

**No Cloudflare account is needed for any section.** Layer 1 is exercised with
assertions minted from a locally generated RSA key pair against a key server run on
loopback — the same way the unit tests work
([plan.md](./plan.md), Testing) — using the loopback-origin rule in
[data-model.md § Config](./data-model.md#config--additions). The one thing that
cannot be validated here is the edge itself: SC-014 is deployment behaviour,
verified against the running hostname, and belongs on the deployment checklist —
it is explicitly exempt from SC-017.

---

## Prerequisites

Milestone 1's [prerequisites](../001-crswd-daemon-core/quickstart.md#prerequisites)
(Go 1.23+, tmux, `golangci-lint`, `goimports`), plus:

| Requirement | Check | Why |
|---|---|---|
| `openssl` | `openssl version` | Generates the key pair and signs minted assertions |
| `python3` | `python3 --version` | base64url plumbing and the loopback key server — stdlib only |
| A browser | — | SC-009/010/011 are visual and keyboard checks; `curl` cannot see a focus ring |

## The gate — every change must pass all four

```bash
go build ./... && go vet ./... && go test ./... && golangci-lint run
```

And the standing property: **`go.sum` must not exist.** `TestQuickstartNoDependencies`
fails if it does.

---

## Setup — a local identity edge

One key pair the daemon will trust, one it must not, and a key server on loopback
serving the Cloudflare-shaped `certs` path.

```bash
IDP=/tmp/crswd-idp
mkdir -p "$IDP/cdn-cgi/access"
openssl genrsa -out "$IDP/key.pem"   2048
openssl genrsa -out "$IDP/wrong.pem" 2048      # a perfectly good key the daemon has never seen

b64url() { openssl base64 -A | tr '+/' '-_' | tr -d '='; }

jwk_n() {  # a JWK's n: base64url over the raw modulus bytes
  openssl rsa -in "$1" -noout -modulus | cut -d= -f2 | python3 -c \
    'import sys,base64; print(base64.urlsafe_b64encode(bytes.fromhex(sys.stdin.read().strip())).rstrip(b"=").decode())'
}

cat > "$IDP/cdn-cgi/access/certs" <<EOF
{"keys":[{"kid":"local-test-key","kty":"RSA","alg":"RS256","use":"sig","e":"AQAB","n":"$(jwk_n "$IDP/key.pem")"}]}
EOF

( cd "$IDP" && python3 -m http.server 8099 >/dev/null 2>&1 ) &
```

Minting helpers — the identity shape by default, every parameter overridable so the
negative sweep can lie in one dimension at a time:

```bash
AUD=$(head -c 32 /dev/urandom | od -An -tx1 | tr -d ' \n')   # any value; the daemon pins it

claims() {  # claims [EMAIL] [EXP_OFFSET_SECONDS] [AUD]
  printf '{"aud":["%s"],"iss":"http://127.0.0.1:8099","exp":%d,"iat":%d,"sub":"user","email":"%s"}' \
    "${3:-$AUD}" $(( $(date +%s) + ${2:-600} )) "$(date +%s)" "${1:-operator@example.com}"
}

mint() {  # mint CLAIMS_JSON [KEY] [ALG] [KID] — a signed assertion
  local c="$1" key="${2:-$IDP/key.pem}" alg="${3:-RS256}" kid="${4:-local-test-key}"
  local h p
  h=$(printf '{"alg":"%s","kid":"%s","typ":"JWT"}' "$alg" "$kid" | b64url)
  p=$(printf '%s' "$c" | b64url)
  printf '%s.%s.%s' "$h" "$p" \
    "$(printf '%s.%s' "$h" "$p" | openssl dgst -sha256 -sign "$key" -binary | b64url)"
}
```

The daemon, pointed at the local key server (milestone 1's env from its
[quickstart](../001-crswd-daemon-core/quickstart.md#setup) still applies):

```bash
export CRSW_ACCESS_TEAM_DOMAIN="http://127.0.0.1:8099"   # http is legal on loopback only
export CRSW_ACCESS_AUD="$AUD"
export CRSW_ACCESS_ALLOWED_EMAILS="operator@example.com"

go build -o /tmp/crswd ./cmd/crswd && /tmp/crswd > /tmp/audit.jsonl 2>&1 &
```

The trail goes to `/tmp/audit.jsonl` because story 3 ends by grepping it. A
browser-door helper, and the credential a valid operator carries:

```bash
dash() { curl -sS -D- -H "Cf-Access-Jwt-Assertion: ${2-$JWT}" "http://127.0.0.1:8765$1"; }
JWT=$(mint "$(claims)")
```

---

## Story 1 (P1) — The fleet at a glance

**Empty first** (FR-021): with no sessions,

```bash
dash /
```

must render the explanatory empty state — not a blank region — with **no** "start a
session" action (FR-024a), and the operator's email in the header (FR-020).

Create two sessions with milestone 1's `crswd_call` helper, prompt one so it is
fresh, and reload: a summary row **before** the cards, one card per session, each
state as a **text** label (FR-019). Idle-vs-running at the threshold is an
injected-clock unit test, not a 60-minute wait — what is checked here is that the
labels render as text at all.

**The page references nothing external and shares nothing cross-origin** (SC-005,
FR-034c):

```bash
dash / | grep -Ec '(src|href)="https?://'        # 0 — every asset is self
dash / | grep -ci 'access-control-'              # 0 — no CORS header, any route
dash / | grep -c  'Content-Security-Policy'      # 1 — the exact policy from docs/security.md
```

**An adopted session renders its absence honestly** (FR-018a): kill the daemon
(not the tmux session), restart, reload. The adopted card must state that its name
and working directory are unknown — an explicit statement, never a plausible-looking
placeholder.

**In the browser** (SC-009, SC-010, SC-011): read the page in greyscale — every
state still distinguishable by its label; walk it entirely by keyboard — a visible
focus ring at every stop; set reduced motion — zero rain canvases render.

---

## Story 2 (P2) — Watch a session, hostilely

Create a session, then make it print the payloads that matter:

```bash
crswd_call POST "/sessions/$ID/prompt" \
  '{"text":"echo \"<script>alert(1)</script> <img src=x onerror=alert(2)>\""}' "$TOK"
```

Open `/sessions/$ID/view` in the browser: every byte renders as visible text,
nothing executes, no alert fires (SC-004). The page states it shows the live
screen, not scrollback (FR-032a).

The stream, from the terminal:

```bash
curl -sN -H "Cf-Access-Jwt-Assertion: $JWT" "http://127.0.0.1:8765/sessions/$ID/stream"
```

Assert: the current screen arrives immediately as **one `data:` line holding a JSON
string** ([contracts/stream.md](./contracts/stream.md)); new output arrives without
reconnecting (SC-006); a quiet screen produces comment heartbeats (`:`), never
repeated screens; no `ESC[` bytes anywhere (FR-029).

**Watching is not driving** (FR-034f): while the stream above is open,

```bash
crswd_call GET "/sessions/$ID" '' "$TOK" | grep -o '"last_activity":"[^"]*"'
```

read twice a minute apart — `last_activity` must not move because of the stream.
(The full reap-while-watched proof is the injected-clock unit test; US2 scenario 7.)

**Ends are announced** (FR-033, SC-015): destroy the session from the API while the
stream is open — within about a second the stream emits `event: end` and closes.

**The cap refuses, then recovers** (FR-034e): restart with `CRSW_MAX_STREAMS=2`,
open two streams, and a third open must be refused with `429`; close one and it
succeeds.

---

## Story 3 (P3) — The negative sweep

Every shape of bad assertion, each lying in exactly one dimension. Save full
responses — headers included — and diff them: **all must be byte-identical**
(FR-010, SC-001), because a difference in `Content-Length` alone is an oracle.

```bash
curl -sS -D- http://127.0.0.1:8765/                                   # absent
dash / "not-a-jwt"                                                    # malformed
dash / "$(mint "$(claims operator@example.com -600)")"                # expired
dash / "$(mint "$(claims)" "$IDP/wrong.pem")"                         # wrong key, known kid
dash / "$(mint "$(claims)" "$IDP/key.pem" RS256 ghost)"               # unknown kid
dash / "$(printf '%s.%s.' \
  "$(printf '{"alg":"none","typ":"JWT"}' | b64url)" \
  "$(printf '%s' "$(claims)" | b64url)")"                             # alg: none, no signature
dash / "$(mint "$(claims operator@example.com 600 deadbeef)")"        # wrong audience (SC-002)
dash / "$(mint "$(claims intruder@example.com)")"                     # disallowed email
dash / "$(mint '{"aud":["'"$AUD"'"],"iss":"http://127.0.0.1:8099","exp":'$(( $(date +%s) + 600 ))',"iat":'$(date +%s)',"sub":"","common_name":"client-id.access"}')"
                                                                      # service-token shape (FR-013c)
```

That last one is the sweep's most important line: a **valid** signature, audience,
and issuer, refused only because it names a credential and not a person. A
validator that admits it has implemented "no email means allow".

**Fail closed when the keys are gone** (FR-009): kill the key server, restart the
daemon (a fresh, empty cache), and present the *valid* `$JWT` — refused, identically
to the rest.

**The trail says why; the response never does** (FR-010, SC-008):

```bash
grep -c 'access.reject'        /tmp/audit.jsonl    # one per refusal above
grep -c "$JWT"                 /tmp/audit.jsonl    # 0 — no assertion in the trail
grep -c 'intruder@example.com' /tmp/audit.jsonl    # 0 — the address stays out too
```

---

## Story 4 (P4) — The existing client keeps working

Milestone 1's signing procedure, **unchanged, against the same listener** (FR-014):
run its [quickstart's](../001-crswd-daemon-core/quickstart.md) `crswd_call`
create → list → destroy cycle verbatim. Then the coexistence pair (FR-012):

```bash
# An API request that also carries a browser assertion: served exactly as before.
# (Reuse crswd_call with an extra -H, or add the header to one signed request.)
# A browser request carries no signature and is not refused for lacking one:
dash /sessions/$ID/view
```

A signed-in browser on an API path it has no business with, and on a path that does
not exist, gets the dashboard's HTML not-found page — not the API's JSON refusal
(FR-013d).

SC-007's full answer is milestone 1's acceptance suite run unchanged:
`go test -tags quickstart ./cmd/crswd`. Every request above — served or refused —
must appear exactly once in the trail (FR-016, SC-008).

Deployment-only, on the checklist rather than here (SC-014): with the hostname
live, every path refuses at the edge without either a browser identity or the
service-token headers; the API client gains those two headers and nothing else
(FR-014a).

---

## Story 5 (P5) — The bypass exists only where it may

**The shipping artifact** (FR-041, SC-012):

```bash
go build -o /tmp/crswd ./cmd/crswd
/tmp/crswd --dev-auth-bypass; echo "exit=$?"     # refuses: the flag does not exist
```

**The development artifact** (FR-038–040, FR-042):

```bash
go build -tags dev -o /tmp/crswd-dev ./cmd/crswd
env -u CRSW_ACCESS_TEAM_DOMAIN -u CRSW_ACCESS_AUD -u CRSW_ACCESS_ALLOWED_EMAILS \
  /tmp/crswd-dev --dev-auth-bypass &             # starts: layer-1 config not demanded (FR-042)
curl -sS http://127.0.0.1:8765/ >/dev/null       # served, no assertion —
                                                 # and a loud warning logged for THIS request (FR-040)
curl -sS -i -X POST http://127.0.0.1:8765/sessions -d '{}' | head -1   # still 401: layer 2 intact (FR-038)
CRSW_LISTEN="0.0.0.0:8765" /tmp/crswd-dev --dev-auth-bypass; echo "exit=$?"  # refuses (FR-039)
```

The warning must appear on **every** request, not once at startup — tail the log
while clicking around.

---

## Definition of done for the milestone

- [ ] `go build ./...`, `go vet ./...`, `go test ./...`, `golangci-lint run` all green
- [ ] Every story above validated end to end
- [ ] Every contract test in [contracts/access-jwt.md](./contracts/access-jwt.md),
      [contracts/dashboard.md](./contracts/dashboard.md), and
      [contracts/stream.md](./contracts/stream.md) present and failing without its change
- [ ] Milestone 1's acceptance suite passes unchanged (SC-007)
- [ ] `go.sum` still absent — zero third-party dependencies, hand-rolled RS256 only
      ([research D1](./research.md))
- [ ] The no-CORS sweep and the no-external-origin check are tests, not eyeballs
      (FR-034c, SC-005)
- [ ] No assertion, token, secret, prompt, pane content, or disallowed address in any
      log line or audit record, asserted by test (FR-035, SC-008)
- [ ] Greyscale, keyboard-only, and reduced-motion checks done in a real browser
      (SC-009, SC-010, SC-011)
- [ ] The shipping build refuses the bypass; the check that proves it fails the build
      if the bypass leaks in (SC-012)
- [ ] `docs/auth-and-sessions.md`, `docs/security.md`, and `docs/components.md`
      amendments landed (the spec's "Documents this milestone must amend" —
      including the pane snippet's append-vs-replace correction)
- [ ] SC-014 recorded on the deployment checklist: edge admission verified against
      the running hostname once the DNS record exists
- [ ] `docs/security.md` and `docs/auth-and-sessions.md` pre-merge checklists satisfied
