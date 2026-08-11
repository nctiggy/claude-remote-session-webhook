# Implementation Plan

**Milestone 12 — A second front door.**

> *"I want this to work on my internal network without needing Cloudflare… I want
> to be able to configure one or the other. If just local UI then built in or set
> password in the config file. Otherwise Cloudflare. And that should also have a
> bool for Cloudflare being enabled."*

Ten tasks. **This is the highest-consequence surface since the updater.** Read the
whole preamble before T001.

---

## ⚠️ What is actually being changed

Two walls stand between a LAN and this daemon, and the bind is the lesser one.

1. **`loadListen` refuses any non-loopback address.** Visible, and not the blocker.
2. **`closedDoor{}` admits nobody** when Access is unconfigured. **Cloudflare
   Access is currently the dashboard's only authentication.** Lift wall 1 alone and
   the daemon is reachable over the LAN and refuses everyone.

**Every session this daemon starts is an unsandboxed shell running with
`--dangerously-skip-permissions`.** A weak door here is code execution as the
operator for anyone who can reach the port. That is the standard every task below
is held to.

---

## ⚠️ Exactly one layer 1. Never two.

Layer 1 is built at **one** place — `internal/httpapi/server.go:339` — and returns
a validator interface. The password door is a **third implementation returned from
that same function**: `closedDoor`, `access.Validator`, `password`.

`closedDoor`'s own comment says why this matters:

> A nil validator and a special case in the middleware would be the second path,
> and the second path is the one nobody reads.

**No task may add a branch in the browser middleware.** If a task finds itself
wanting one, it has left the plan.

**The selection is explicit and mutually exclusive:**

| `access_enabled` | `dashboard_password` | Layer 1 | Notes |
|---|---|---|---|
| `true` | unset | `access.Validator` | The three Access values become required |
| `false` / unset | set | password door | The new one |
| `true` | set | **refuse to start** | Ambiguity is the defect; never pick a winner silently |
| `false` / unset | unset | `closedDoor` | Today's behaviour — admits nobody |

---

## ⚠️ The bind guard is relaxed conditionally, never removed

A non-loopback listen is permitted **only when layer 1 admits somebody**. A
`closedDoor` daemon must still refuse to bind off loopback.

That keeps the invariant — *never reachable without authentication* — intact
rather than deleted. A task that simply drops the `IsLoopback` check has removed a
bound instead of relaxing it, which is the same distinction milestone 10 drew
between a negative idle and a negative lifetime.

---

## ⚠️ Password rules, all of them non-negotiable

- **It is a secret.** `config.IsSecret("dashboard_password")` must return true, so
  it is never editable from the browser and renders as `present`/`absent` only. A
  password settable from the page it protects is not a door.
- **Never in a log, an audit record, a page, an error, or a URL.**
- **Constant-time comparison**, over hashes of both sides so length does not leak.
- **Rate-limited**, with the limiter keyed per source rather than per session. A
  LAN is a fast network to brute force from.
- **The session cookie is `HttpOnly`, `SameSite=Lax`, `Path=/`, and `Secure`
  whenever the request arrived over TLS.** Lax rather than Strict because the
  operator arrives by typing a URL, and Strict withholds the cookie on that first
  top-level navigation.
- **The cookie is signed**, keyed by HMAC over the existing `shared_secret` with a
  distinct label — no new key, and rotating the secret invalidates sessions, which
  is correct.
- **The login route is unreachable when Access is the configured door.** A login
  form that exists beside a working Access door is the second path again.

**State the plaintext-over-HTTP risk in the documentation rather than hiding it.**
On a LAN without TLS the password crosses the network in clear. That is a real
weakness of this mode and an operator choosing it deserves to be told, not
reassured.

---

## Conventions

- `- [ ]` open · `- [x]` done · `- [!]` blocked (reason in `PROGRESS.md`)
- Priority order is meaningful — the loop always takes the topmost open item.
- **Every task ends green**: `go build ./... && go vet ./... && go test ./... && golangci-lint run`,
  plus `-tags tmux` / `-tags quickstart` where touched.
- **Check the linter is v2 before trusting it** (#26).
- `go.sum` must never appear — `crypto/hmac`, `crypto/sha256`, `crypto/subtle` and
  `net/http` are all stdlib.
- **AR-005: a test satisfies the cross-site checks, it never disables them.**
- **AR-008: no refactoring outside the task.**
- **A task is not done when the code exists. It is done when something calls it.**
- **A new guard must be proven by breaking it.**
- **Everything works with no JavaScript**, the login form included.

---

## Tasks

- [x] **T001** 🔒 Add `access_enabled` (bool) and `dashboard_password` (secret) in `internal/config/`. `dashboard_password` joins `IsSecret`; `access_enabled` joins the boolean keys so it renders as a checkbox and is caught by `TestIsBoolNamesEveryBooleanLoaded`. Implement the selection table above in `Validate`, including **refusing to start when both are configured**. Test every row of that table, and test that the password is never returned by anything that renders configuration.

- [x] **T002** 🔒 Relax the bind guard in `loadListen`: a non-loopback address is permitted **only when layer 1 admits somebody**. A `closedDoor` daemon still refuses. The refusal is a startup error to a terminal rather than an HTTP response, so it may name which of the two is missing. **Must fail when** the `IsLoopback` check is simply deleted.

- [x] **T003** 🔒 Implement the password door as a validator in `internal/httpapi/`, returned from the **same** function at `server.go:339` that returns `closedDoor` and `access.Validator`. Constant-time comparison over SHA-256 of both sides. Session cookie signed with `hmac.New(sha256.New, sharedSecret)` over a distinct label; `HttpOnly`, `SameSite=Lax`, `Path=/`, `Secure` when the request was TLS. **No branch in the browser middleware.**

- [x] **T004** 🔒 The login page and its POST route. A real form, working with no JavaScript, reusing `.field`, `.field-input`, `.field-label`, `.button` — **no new class**. The route is registered only when the password door is the configured layer 1, and answers the uniform 404 otherwise. It sets the cookie on success and the uniform refusal on failure, with no distinction between causes.

- [x] **T005** 🔒 Rate-limit the login route, per source, reusing the create route's limiter pattern rather than inventing a second one. Exactly one audit record per attempt, allowed or denied, carrying **no password material**. Test that a burst is refused and that the record says which.

- [x] **T006** Show which door is live on the settings page, reusing existing classes. An operator must be able to tell whether they are behind Access, a password, or a closed door — the single most consequential fact about the daemon, currently invisible. `dashboard_password` renders `present`/`absent` and never its value.

- [x] **T007** Log out. A POST through `handleAction` like every other mutating route, clearing the cookie. Without it a shared or borrowed browser keeps a session the operator cannot end.

- [ ] **T008** Document the LAN deployment in `README.md`: the config for each door, the bind change, and **the plaintext-over-HTTP weakness stated plainly** with TLS recommended. Say what each door is for — Access when it is on the internet, a password when it is not.

- [ ] **T009** Carried from milestone 11: rewrite `next_steps()` in `install.sh` now that the config is complete, and **decide and record** whether the installer should enable the unit. The old reasoning was written against an incomplete config; say whether it still holds.

- [ ] **T010** Carried from milestone 11: rewrite `README.md` for a stranger — lead with what it is, then install, then the doors, then configure. Move "Working in this repo", "Planning a milestone" and "Running a loop" into `CONTRIBUTING.md`. Then fix `#129`: `.env.example` claims the session lifetimes are "constants in the code, not variables" about 120 lines below listing them as variables — correct the claims that stopped being true and keep the ones that did not.

---

## Out of scope

- **mTLS and OIDC.** Two doors is the request; a third is a different milestone.
- **Terminating TLS in the daemon.** An operator who wants it puts a proxy in
  front, which is what the loopback bind already assumes.
- **#120, #121.** Unchanged. **Q2** is still the operator's to answer.
