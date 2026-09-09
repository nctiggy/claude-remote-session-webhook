# Implementation Plan

**Milestone 17 — `crswd-axi`: an agent-ergonomic CLI over crswd's six signed routes.**

Task `lm-90`. Nine tasks. What "done" means is in
[`ralph/VALIDATION_CONTRACT.md`](VALIDATION_CONTRACT.md) — read it before T001,
because it was written before this decomposition and it, not this file, is what a
later black-box pass checks.

---

## The problem, stated once

`deploy/crswd-api` is 73 lines of bash with the signature `METHOD PATH [BODY]
[BEARER]`. There are no subcommands, no `--json`, and no help beyond a header
comment. So every agent that touches crswd re-derives HMAC signing and
hand-builds JSON bodies from memory of `internal/httpapi/sessions.go`. That is
the exact shape `opp-axi` was written to kill for Salesforce.

`crswd-api` is not being replaced or changed. It is the minimum client and it
stays: three lines of `op`, four headers, one signature. `crswd-axi` is the
ergonomic one beside it.

---

## Ground truth, measured in this repository on 9/9/26

Everything below was read out of the code, not remembered. Do not re-derive it.

**The six routes** (`internal/httpapi/server.go:101`, `var routes`) — nothing else
is registered, and a request matching no route is refused like an unknown session:

| Route | Session-scoped | Success |
|---|---|---|
| `POST /sessions` | no | `201` |
| `GET /sessions` | no | `200` |
| `GET /sessions/{id}` | **yes** | `200` |
| `DELETE /sessions/{id}` | **yes** | `200` |
| `POST /sessions/{id}/prompt` | **yes** | `202` |
| `GET /sessions/{id}/output` | **yes** | `200` |

**The signature** (`specs/001-crswd-daemon-core/contracts/http-api.md`):

```
payload = METHOD + "\n" + PATH + "\n" + timestamp + "." + rawBody
X-CRSW-Signature: sha256=<lowercase hex HMAC-SHA256(secret, payload)>
X-CRSW-Timestamp: <unix seconds, decimal>
```

`PATH` is the escaped path with no query string. An empty body signs as the empty
string, so `GET /sessions` at 1785706480 signs `"GET\n/sessions\n1785706480."`.
Skew tolerance is ±300s, both directions.

**Bodies and shapes** (`internal/httpapi/sessions.go`):

- create takes `name`, `work_dir`, `start_command`, `resume`, `lifetime` — and
  **nothing else**: the decoder uses `DisallowUnknownFields`, so an extra member
  is a `400`, not an ignored field.
- `name` is `^[a-zA-Z0-9-]{1,64}$`; `:` and `.` are rejected explicitly.
- prompt takes exactly `{"text": "..."}`. The `202` reply may carry `parked_on`
  or `suspicious_dialog` — both omitted when empty, and both mean the keystrokes
  did **not** reach an ordinary prompt. A client that reads only `delivered` is
  reporting a lie the daemon did not tell.
- a list entry and a detail are the same object: `id`, `name`, `work_dir`,
  `state`, `created_at`, `expires_at`, `last_activity`, optional `start_command`,
  `adopted`, optional `token`.

**Status codes**: `400` malformed/unknown field/failed validation · `401` any
layer-2 failure *including an oversize body* · `404` unknown session, another
owner's session, wrong bearer, **or no such route** · `409` teardown unverified,
the session may still be alive · `429` cap or rate limit · `500` no detail.

### ⚠️ Two traps that will cost an iteration each

**1. A bearer token is disclosed exactly once, and a raw list destroys it.**
`internal/session/token.go` says it plainly: *"there is no re-issue path
(FR-015)"*. A credential reaches a caller in exactly two places — the `201` from
create, and `listSessions` (`internal/httpapi/sessions.go:576`), which calls
`ClaimPending` and stamps `entry.Token` on sessions the daemon adopted at
startup. That is the **only** response that will ever carry them. So a bare
`crswd-api GET /sessions` after a daemon restart prints the re-minted credentials
to a terminal and they are gone. **Handling this is the point of the milestone,
not a detail of it** — see T006 and the fourth assertion in the validation
contract.

**2. Two byte-identical signed requests in the same second are a replay, and the
second is refused.** `Authenticator.Verify` observes the *signature value*
(`internal/auth/hmac.go:197`), and the bearer token is deliberately not in the
signed payload. So a retry of the same method, path, body and unix second
produces the same signature and gets a uniform `401`. A retry must therefore land
on a **different second**, not merely be re-signed. This is written down in the
contract as a consequence "worth knowing when writing a client"; it is now the
client.

---

## The decision, and the shape it forces

**Node on `axi-sdk-js`, taken 9/8/26.** The SDK gives spec-compliant TOON output,
`--help`, `--version`, structured `AxiError` exit codes and a built-in `update`
self-updater for free, and it sidesteps the Python copy-paste problem in lm-91.
Cost is a second language in the axi family — accepted, since `tasks-axi` and
`quota-axi` are already Node.

**The concern this plan is built around, stated once:** the SDK could not be
verified from the planning session — reads outside the worktree were refused, and
`npm view` needs approval a non-interactive session cannot give. So its exact
package name, version and exported surface are **unknown at planning time**. A
plan that guessed an import would burn every iteration on the guess.

The answer is architectural, and it is the right one anyway: **the SDK is
confined to one file.** Everything carrying real behaviour — signing, credential
resolution, the token store, the six calls, the verb bodies — is plain Node 22
standard library (`node:crypto`, `fetch`, `node:util.parseArgs`, `node:test`) and
is written and proved without the SDK present. `bin/crswd-axi` is the only file
that imports it, and it is wired in T008 from what T001 wrote down.

That also buys the seventh assertion in the validation contract: `node --test
deploy/crswd-axi/test/` runs on a clean checkout with **no `npm install`**, no
network and no daemon. A Ralph iteration that cannot reach the network can still
prove its work.

Node on this host is **v22.23.2** — measured. `node --test`, `parseArgs` and
global `fetch` are all in it.

---

## Layout

```
deploy/crswd-axi/
  package.json        T001   name, bin, "type":"module", the test script
  lib/sign.js         T002   the HMAC payload and header value
  lib/creds.js        T003   secret, Access pair, host — env then `op`
  lib/tokens.js       T004   the on-disk credential store
  lib/client.js       T005   sign + headers + fetch, and the six calls
  lib/acquire.js      T006   token capture, harvest and retry
  lib/verbs.js        T007   list show create prompt output close, and home
  bin/crswd-axi       T008   the SDK shell — the ONLY file importing the SDK
  test/*.test.js      T002+  node:test, standard library only, no SDK
  README.md           T009
```

`test/` stays dependency-free. If a test needs the SDK, the thing it is testing
is in the wrong file.

---

## Things that will bite, written down so nobody rediscovers them

- **gitleaks scans `deploy/crswd-axi/`.** `.gitleaks.toml` allowlists
  `deploy/README.md` and `deploy/*.example.*` — **not** this directory — and the
  default ruleset is on. A 64-hex fixture token in a JS test will be rejected by
  `.githooks/pre-commit` and by CI. Two shapes are safe *by construction* and
  already allowlisted: a value prefixed `test-only-`, and the hex alphabet
  repeated (`0123456789abcdef` × 4 for a 64-char token). Use those.
- **CI does not gain a Node lane in this milestone.** The `Detect stack` step in
  `.github/workflows/ci.yml` probes `package.json` at the **repository root**
  only; ours is nested, so nothing about CI changes. The consequence is honest and
  should be said in T009's README rather than hidden: these tests run locally and
  in the Ralph loop, and not yet in CI.
- **`node_modules/` and `dist/` are gitignored.** So there is no build step: the
  client ships as the source that runs. Do not add a compile.
- **`ralph/loop.sh` refuses to start on a dirty tree** and sweeps anything
  uncommitted into a commit afterwards. `npm install` writes only into the
  ignored `node_modules/`, which is fine — but a stray build artefact anywhere
  else stops the next iteration.
- **The release is a fixed seven-asset contract** (`internal/release/assets_test.go`,
  `deployed`) checked by a Go test against `.github/workflows/release.yml`. Shipping `crswd-axi` as
  a release asset is **out of scope** — see below — and touching either file will
  fail `go test ./...`.
- **`AGENTS.md` is 147 lines and CI fails it at 150.** T009 has a two-line budget.

---

## The tasks in detail

### T001 — Pin the SDK against reality
Create `deploy/crswd-axi/package.json`: `"name": "crswd-axi"`, `"type": "module"`,
`"private": true`, `"bin"` pointing at `bin/crswd-axi`, and
`"scripts": {"test": "node --test test/"}`. Then install the SDK into that
directory and **write what you find into `ralph/PROGRESS.md` under a second-level
heading named SDK surface**: the resolved package name, the resolved version, the exact
import specifier, and every export T008 will need — how a command is registered,
how zero-arg home is declared, how output is emitted, the error type, and the
`update` verb. Later tasks read that section; they do not guess.

If the package cannot be installed — it does not exist under that name, or the
host is offline — **do not invent an API**. Constitution principle II applies:
record NEEDS CLARIFICATION in `ralph/PROGRESS.md` naming exactly what failed,
mark this task blocked with the blocked marker from Conventions below, and stop.
That costs one iteration. A guess costs nine.

### T002 — The signature
`lib/sign.js`: build the payload and the header value from method, path, unix
seconds, raw body string and secret. No network, no I/O, `node:crypto` only.
The tests must not be tautological — asserting the digest against a digest this
same file computed proves nothing. Assert instead: the exact payload string for
the contract's documented example (`GET\n/sessions\n1785706480.`); that changing
only the method changes the header value; that the value is `sha256=` followed by
exactly 64 lowercase hex characters.

### T003 — Credentials, without putting one in an error
`lib/creds.js` resolves four things: the shared secret, the Cloudflare Access
client id and secret (both optional — the dashboard-password door has none), and
the host. Order: environment first (`CRSWD_SHARED_SECRET`,
`CRSWD_ACCESS_CLIENT_ID`, `CRSWD_ACCESS_CLIENT_SECRET`), otherwise `op read` from
`CRSWD_OP_ITEM`, default `op://Lobster/crswd`, exactly as `deploy/crswd-api` does.
Host from `CRSWD_HOST`, default `http://127.0.0.1:8765` — loopback, for the reason
`crswd-api` gives in its own comment. The env path exists so the tests and the
loop can run with no 1Password. **No resolved value ever reaches an error string,
a log line or `--json` output**; `docs/security.md` is binding here.

### T004 — The token store
`lib/tokens.js`: a JSON map of session id → bearer token at
`${XDG_STATE_HOME:-$HOME/.local/state}/crswd-axi/tokens.json`, directory `0700`,
file `0600`, written by temp-file-plus-rename so an interrupted write cannot
leave a truncated store. `CRSWD_AXI_STATE_DIR` overrides the location, which is
how the tests get a temp directory. A corrupt or unreadable store must degrade to
empty rather than crash the CLI — losing cached credentials costs a re-list;
crashing costs the session.

### T005 — The six calls
`lib/client.js`: one `request()` that signs, sets the four headers, attaches
`Authorization: Bearer` **only** on the four `{id}` routes, and `fetch`es; plus
six named functions over it. Map the status codes to distinct typed errors —
`400` bad request, `401` unauthorized, `404` not found, `409` teardown
unverified, `429` rate limited, `500` internal — because T008 turns those into
exit codes and the validation contract requires two of them to differ. Honour
trap 2: any retry of an identical request waits for the unix second to tick.
Test against a `node:http` stub bound to `127.0.0.1:0`.

### T006 — Token acquisition is the CLI's job
`lib/acquire.js`, and it is the milestone's reason for existing (trap 1).
`create` stores the token from its `201`. `list` harvests **every** `token`
present on **every** entry it receives and stores them before rendering, because
that response is the only one that will ever carry them. A session-scoped verb
called with no stored token runs a list first and retries **once** — and the
retry obeys trap 2. `close` deletes the stored entry when the `DELETE` returns
`200`, and leaves it alone on a `409`, because a `409` means the session may
still be alive and its credential is still the only way to reach it.

### T007 — The verbs, and home
`lib/verbs.js`: `list`, `show`, `create`, `prompt`, `output`, `close`, plus the
zero-arg home. Flags, never positional JSON: `create --name --dir
[--start-command] [--resume] [--lifetime]`, `prompt <id> --text`. Home follows the
AXI shape `tasks-axi` prints — the bin path, a one-line description, counts by
state, and a short list of suggestions written as runnable commands — and it
**exits 0 with an unreachable daemon**, reporting that in the summary. `--json` on
every verb writes one JSON document to stdout and nothing else to stdout.
A `202` carrying `parked_on` or `suspicious_dialog` must be surfaced, not
flattened into "delivered".

### T008 — The SDK shell
`bin/crswd-axi`, executable, and the only file that imports the SDK. Register the
six verbs and home using the API recorded in the SDK surface section of
`ralph/PROGRESS.md`, and
map T005's typed errors onto the SDK's error type so the exit codes are
structured. `--help`, `--version` and `update` come from the SDK; do not
hand-write them.

### T009 — Document it, and say what is not covered
`deploy/crswd-axi/README.md`: install, the two credential paths, **why the token
store exists** (trap 1, in the operator's terms), the exit codes, the retry rule
(trap 2), and the honest note that these tests do not yet run in CI. A short
section in `deploy/README.md` beside the `crswd-api` one saying which client to
reach for and why both exist. One project-map row and one command row in
`AGENTS.md`, inside the two-line budget.

---

## Conventions

- `- [ ]` open · `- [x]` done · `- [!]` blocked, with the reason in `ralph/PROGRESS.md`.
- Priority order is meaningful — the loop always takes the topmost open item.
- **Every task ends green.** Go must stay exactly as green as it was:
  `go build ./... && go vet ./... && go test ./...`. The client's own suite is
  `node --test deploy/crswd-axi/test/`.
- **A task is not done when the code exists. It is done when something calls it.**
- **No refactoring outside the task**, and no Go changes in this milestone at all.
- Fixtures use `test-only-…` or the repeated hex alphabet. See the gitleaks note.

---

## Tasks

- [ ] **T001** Pin the SDK per the T001 section: add `deploy/crswd-axi/package.json`, install the SDK there, and record its name, version, import specifier and exports in `ralph/PROGRESS.md` under an SDK surface heading; if it will not install, record the blocker, mark this blocked and stop rather than invent an API. Verify: `npm ls --prefix deploy/crswd-axi` exits 0 and lists the SDK.

- [ ] **T002** Write `deploy/crswd-axi/lib/sign.js` per the T002 section: the method, path and timestamp-dot-body payload plus the sha256= header value, `node:crypto` only. Verify: `node --test deploy/crswd-axi/test/sign.test.js` exits 0, pinning the contract's documented example payload, that changing only the method changes the value, and that it is sha256= plus 64 lowercase hex.

- [ ] **T003** Write `deploy/crswd-axi/lib/creds.js` per the T003 section: environment first, then `op` for the shared secret and optional Access pair, host from CRSWD_HOST defaulting to loopback. Verify: `node --test deploy/crswd-axi/test/creds.test.js` exits 0, proving the environment path resolves with no `op` on PATH and that no failure message repeats a secret or `op`'s stderr.

- [ ] **T004** Write `deploy/crswd-axi/lib/tokens.js` per the T004 section: an atomic session-id-to-token JSON store under the XDG state directory, directory 0700, file 0600, CRSWD_AXI_STATE_DIR overriding it. Verify: `node --test deploy/crswd-axi/test/tokens.test.js` exits 0 — put and get round-trip, mode is 0600, delete removes the key, a corrupt store reads empty.

- [ ] **T005** Write `deploy/crswd-axi/lib/client.js` per the T005 section: one signed request helper plus the six calls, bearer only on the four scoped routes, each status mapped to a typed error. Verify: `node --test deploy/crswd-axi/test/client.test.js` exits 0 against a `node:http` stub — the four headers, no bearer on the unscoped routes, every mapping, and a retry on a later second.

- [ ] **T006** Write `deploy/crswd-axi/lib/acquire.js` per the T006 section: create stores its token, list harvests every token on every entry before rendering, a scoped verb holding none lists once then retries once, close deletes on 200 and keeps on 409. Verify: `node --test deploy/crswd-axi/test/acquire.test.js` exits 0 against a stub disclosing a token only on the first list.

- [ ] **T007** Write `deploy/crswd-axi/lib/verbs.js` per the T007 section: list, show, create, prompt, output and close driven by flags, the zero-arg home in the AXI shape, and --json on every verb. Verify: `node --test deploy/crswd-axi/test/verbs.test.js` exits 0 — home exits 0 against an unreachable host, --json parses as one document, and a 202 carrying parked_on is surfaced.

- [ ] **T008** Write `deploy/crswd-axi/bin/crswd-axi` per the T008 section, executable and the only file importing the SDK: register the six verbs and home from the surface T001 recorded, mapping T005's typed errors onto the SDK error type. Verify: `deploy/crswd-axi/bin/crswd-axi --help` exits 0 naming all six verbs, and a bad flag and an unknown id exit with two different non-zero codes.

- [ ] **T009** Document it per the T009 section: `deploy/crswd-axi/README.md`, a section beside the crswd-api one in `deploy/README.md`, and one project-map row plus one command row in `AGENTS.md`, covering the token store, exit codes, the retry rule and the CI gap. Verify: `wc -l AGENTS.md` reports fewer than 150, and `node --test deploy/crswd-axi/test/` and `go test ./...` exit 0.

---

## Files touched

The blast radius of this milestone. A diff outside this list is rejected.

- `deploy/crswd-axi/**` — the new client, its tests and its README (new directory)
- `deploy/README.md` — one section beside the `crswd-api` one
- `AGENTS.md` — one project-map row and one command row, inside the 150-line limit
- `ralph/IMPLEMENTATION_PLAN.md` — ticking tasks
- `ralph/PROGRESS.md` — the notebook

Nothing under `internal/`, `cmd/`, `web/` or `specs/` changes. No Go changes at
all: the daemon and its six routes are already what this milestone wraps.

---

## Out of scope

- **Changing `deploy/crswd-api`.** It is the minimum client and it stays. Two
  clients is the point: one that is three lines of `op` and four headers, one that
  is ergonomic.
- **Shipping `crswd-axi` as a release asset.**
  `specs/006-ship-it-to-someone-else/contracts/release.md` fixes seven
  names and `internal/release/assets_test.go` holds `.github/workflows/release.yml`
  to them. Adding
  an eighth is a contract change with its own spec, and it would have to answer
  how a Node client is published to a host that has only the Go binary.
- **A Node lane in CI.** Stated as a known gap in T009 rather than hidden. It is a
  workflow change, and workflow changes are not in the allowlist above.
- **Any change to the six routes, their bodies, or their status codes.** If a verb
  seems to want a seventh route, that is a spec question, not an iteration.
- **lm-91 and lm-86.** lm-90 pairs with the needs-auth relay (lm-86) and unblocks
  lm-91; neither is built here.
- **`--watch`, streaming, or tailing `output`.** The route is a capture, not a
  stream. `internal/httpapi/stream.go` serves the browser, not this API.
