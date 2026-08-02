# Implementation Plan

> Worked top-to-bottom by `ralph/loop.sh` — one task per iteration.
>
> **These are milestone 1 only.** Scope is deliberately capped: a working daemon
> with no UI. Milestones 2–4 (dashboard, actions, Claude login relay) get their own
> plan and their own loop. One loop for the whole product drifts.

## Status: provisional

Written from the architecture decisions in `AGENTS.md` and the two security docs.
Feed this to `/speckit-specify`; the generated `spec.md` supersedes it.

## Resolved decisions

Answered by the operator. **Do not re-litigate these in an iteration** — if one
looks wrong, write it in `PROGRESS.md` under `NEEDS CLARIFICATION` and stop.

| Question | Decision | Consequence |
|---|---|---|
| What does session ownership mean with one shared secret? | Single operator, but the `Owner` field and the ownership check exist from day one | Milestone 2's Access identity drops in without touching a handler. The cross-owner test uses a synthetic second owner |
| What does the tmux window run? | The shell, then the daemon sends `claude --dangerously-skip-permissions` as keys | Window survives a Claude crash, so scrollback is inspectable — and milestone 4's device-code relay has a prompt to type into |
| What happens to tmux sessions that outlive the daemon? | **Adopt** them on startup | An orphaned window is a live unsandboxed shell with no owner (Principle VI). Ignoring them is not an option |
| Where does the audit log go? | Structured JSON on stdout, captured by journald | No file mode, rotation, or disk-fill to get wrong. `journalctl --user -u crswd` |

## Conventions

- `- [ ]` open · `- [x]` done · `- [!]` blocked (reason in `PROGRESS.md`)
- Priority order is meaningful — the loop always takes the topmost open item.
- Each task must be independently completable **and verifiable** in one iteration.

---

## Tasks

### Foundation

- [ ] Initialize the Go module (`go 1.23`), add `cmd/crswd/main.go` that parses flags and exits cleanly; `go build ./...` and `go vet ./...` pass
- [ ] Add `.golangci.yml` enabling at minimum `errcheck`, `gosec`, `govet`, `staticcheck`, `bodyclose`; `golangci-lint run` passes on the empty tree
- [ ] `internal/config`: load from environment, with `CRSW_SHARED_SECRET` required — **startup fails** if unset or shorter than 32 bytes. Table test covers every missing-field case

### tmux control

- [ ] `internal/tmuxctl`: define the `Controller` interface (`New`, `Kill`, `SendKeys`, `CapturePane`, `List`) plus a `fake` implementation for tests. No real tmux in unit tests
- [ ] Implement the real `Controller` over `exec.Command` with argv slices only — no shell string anywhere. Integration test guarded by a `tmux` build tag
- [ ] Add ANSI escape stripping for captured pane text, with golden-file tests

### Sessions

- [ ] `internal/session`: `Session` model (including `Owner`, populated from the credential used) and in-memory store. IDs from `crypto/rand`, ≥128 bits, never sequential. Test asserts non-sequential, non-colliding IDs
- [ ] Session name validation `^[a-zA-Z0-9-]{1,64}$`, rejecting `:` and `.` explicitly — they address a different tmux target. Table test with hostile inputs
- [ ] Working-directory allowlist: `filepath.Clean` + `EvalSymlinks`, then verify under an approved root. Tests cover `..`, absolute escapes, and a symlink pointing outside
- [ ] `Manager.Create` opens a tmux window running the login shell, names it `crswd-<id>`, then sends `claude --dangerously-skip-permissions` as keys; records the session. `Destroy` kills it and **verifies the kill**, returning an error if the window survives
- [ ] `Manager.Adopt`: on startup, list tmux sessions matching the `crswd-` prefix and rebuild records for any the daemon has no entry for. Fresh tokens are issued; the old ones are unrecoverable by design. Test covers a surviving session, a half-dead one, and a name that matches the prefix but was not ours

### Authentication

- [ ] `internal/auth`: HMAC-SHA256 verification over `timestamp + "." + rawBody`, using `hmac.Equal`. Tests: valid, bad signature, tampered body, missing header
- [ ] Timestamp window of 300s enforced in **both** directions; test proves a far-future timestamp is rejected
- [ ] Replay cache keyed on signature with TTL `2 × skew`; test proves a second use of a valid request is refused
- [ ] Per-session bearer tokens: `crypto/rand`, returned once, stored as SHA-256 hash. Test asserts the plaintext token is never persisted

### HTTP API

- [ ] `internal/httpapi`: router plus a middleware that authenticates every route. Test asserts an unauthenticated request to each registered route gets 401
- [ ] `POST /sessions` — create; body decoded with `DisallowUnknownFields` and a body-size limit. Tests cover unknown fields and oversize bodies
- [ ] `GET /sessions` and `GET /sessions/{id}` — list and detail, ownership enforced, **404 not 403** for another owner's session. Cross-owner test required
- [ ] `DELETE /sessions/{id}` — destroy with verified teardown
- [ ] `POST /sessions/{id}/prompt` — `send-keys` the prompt as a single literal argument. Test asserts a prompt containing `;`, `$(...)`, and newlines is not interpreted
- [ ] `GET /sessions/{id}/output` — captured pane text, ANSI stripped

### Guardrails

- [ ] `internal/audit`: structured JSON records on stdout via `log/slog` — timestamp, caller, action, session ID, decision. Test asserts prompt text, pane content, and tokens never appear in a record
- [ ] Concurrent-session cap and a rate limit on create; refuse past the cap rather than degrading the host
- [ ] Reaper goroutine enforcing idle (60m) and absolute (24h) lifetimes, driven by an injected clock so tests do not sleep
- [ ] Bind `127.0.0.1` only, with graceful shutdown that reaps sessions on `SIGTERM`. Test asserts the listener address is loopback

### Ship it

- [ ] `deploy/`: systemd user unit and `cloudflared` config, with the secret sourced from 1Password rather than a repo file. README deployment section filled in

---

## Out of scope

Deliberately NOT in milestone 1, so no iteration wanders into them:

- The web dashboard, templates, htmx, CSS, and SSE streaming — milestone 2
- Cloudflare Access JWT validation — milestone 2
- Rename and compact — milestone 3
- The Claude device-code login relay — milestone 4
- The companion Claude skill in `skill/` — after the API is stable
- Persisting session records to disk. The store stays in-memory — restart recovery
  comes from adopting live tmux sessions, not from a database
- Multi-user support. There is exactly one operator
