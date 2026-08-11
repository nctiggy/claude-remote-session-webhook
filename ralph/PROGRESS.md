# Progress

> Append-only notebook. Newest at the bottom. Never edit or delete past entries —
> this is the loop's only memory across fresh contexts.

Each iteration appends:

```
## Iteration N — YYYY-MM-DD HH:MM
**Did:** one or two lines.
**Learned:** anything that would otherwise be rediscovered the hard way.
**Left:** what remains.
**Findings:** problems noticed but not fixed (ad-hoc bugs, smells, risks).
```

Findings are the point of this file as much as progress is. An observation that
dies in a context window is a bug you will pay for twice. Real ad-hoc fixes also
get a one-liner in `docs/fixes-log.md`.

When the whole plan is done and green, append a line containing exactly

---

## Iteration 0 — a second front door

**Did:** Archived milestone 11's notebook. Note that milestone 11 is **not
finished**: T003 through T006 (next_steps, the README rewrite, the Cloudflare
walkthrough, `.env.example`) are still open and carried into this plan's tail.

**Left:** the tasks below.

**The request:** run the dashboard on an internal network without Cloudflare, with
the choice made in the config file — a bool for whether Access is the door, and a
password when it is not.

**Findings, all verified before the plan was written:**

- **Two walls, and the bind is the lesser one.** `loadListen` refuses any
  non-loopback address outright. But even lifted, `cfg.AccessTeamDomain == ""`
  yields `closedDoor{}`, which admits nobody — so the daemon would be reachable
  and refuse everyone. **Cloudflare Access is currently the dashboard's only
  authentication.**
- **Layer 1 is constructed at exactly one place** (`server.go:339`) and returns a
  validator interface. A password door is a third implementation returned from
  that same function — never a special case in the middleware. `closedDoor`'s own
  comment says why: *"a nil validator and a special case in the middleware would
  be the second path, and the second path is the one nobody reads."*
- **`--dev-auth-bypass` is behind a build tag** and absent from released binaries.
  It is not a route to this and must not be reached for.
- **What this daemon runs matters here more than anywhere else.** Every session is
  an unsandboxed shell with `--dangerously-skip-permissions`. A weak door is
  code execution as the operator, for anyone who can reach the port.

---

## Iteration 1 — 2026-08-11 — T001, the two settings and the selection

**Did:** Added `access_enabled` (bool) and `dashboard_password` (secret) to
`internal/config`, implemented the selection table in `validateDoors`, and gave
both keys their row on the settings page. `dashboard_password` joins `IsSecret`,
so it is `present`/`absent` on the page and refused by `Editable`; `access_enabled`
joins `IsBool`, so it renders as a checkbox and satisfies the structural test.
`docs/security.md` §3 now names three secrets rather than two.

**Left:** T002 through T010. Nothing in T001 is blocked or partial.

### The decision the plan's table does not make, and why it went this way

The table is keyed on `access_enabled` × `dashboard_password` and never says what
a daemon with **the three Access values and no `access_enabled`** is. That is
every deployment that exists — the variable did not exist when they were written.

- Reading it as `closedDoor` takes the dashboard away from an operator who
  changed nothing.
- Reading it as a refusal stops their daemon starting.
- **`false` and unset are one value here** and cannot be told apart: the settings
  page renders booleans as checkboxes, an unchecked box submits nothing, and the
  edit handler therefore reads absent as `false`. That is written down in
  `IsBool`'s own comment. So "explicit false disables Access" would disable it for
  every daemon that never set it.

So: **the three Access values still select the Access door on their own**, and
`access_enabled` is the operator saying so out loud. Stated, it is checkable where
unstated intent is not — on with none of the three refuses, and on beside a
password refuses. Every row of the plan's table holds unchanged; this only
answers the row it omits, and answers it with today's behaviour, which is what
that table's own last row appeals to. **Revisit if the intent was that Access must
be declared** — it is a one-line change in `validateDoors` plus a migration note.

### The bound the plan does not state

`MinDashboardPasswordLen = 16`. The plan's password rules are exhaustive and do
not include a length, but Principle I says weak auth configuration is a startup
failure rather than a warning, and length is the only mechanical reading of "weak"
available. **The number is the judgement call, not the bound** — 16 because the
attacker is on the same LAN and a per-source limiter is worth as much as the
number of sources they have. Change the constant if you want another.

**Learned, for whoever picks up T002:**

- **Adding one `Env` constant costs five files.** `config.go`, `Vars()` in
  `file.go`, and then four tests fail until `.env.example` (comment line required
  directly above), the `README.md` variable table, `config.example` (in `Vars()`
  order — the order is asserted), and `deploy/crswd.example.service` all name it.
  A secret must additionally join `deploymentSpecific()` in `deployexample_test.go`
  and be named in a unit comment rather than set inline.
- **Two more fail in `internal/httpapi`**: `settingValue` needs a case for any
  non-secret key, `secretConfigured` needs one for any secret key. Both are the
  page's "I have never heard of this setting" guard firing correctly.
- **`TestIsSecretIsTheOnlyClassifier` forbids the literal `"dashboard_password"`
  in any non-test file of `internal/config` except `secret.go`.** The `Env`
  constant's value is a different literal, so it is fine; a `case
  "dashboard_password":` anywhere would not be.
- **`validateDoors` runs after `validateAccessGroup` on purpose.** That is what
  makes `teamDomain != ""` a sound stand-in for "Access is configured" rather than
  an assumption — by then the three are all set or all unset.
- The `httpapi` fixtures build a `*config.Config` as a struct literal, so a test
  can hold a Config with **both** doors that no `LoadFrom` would ever produce.
  Used deliberately in the render sweeps: the page must hide the password whether
  or not the loader agreed to start.
- **Both new guards were proven by breaking them.** Disabling `validateDoors`
  fails five cases; dropping the key from `IsSecret` fails four across two
  packages; making the page print the password fails four more, including the
  full-route sweep.

**Findings, not fixed:**

- **`.env.example` still says the three Access values are required** — "Unset or
  unusable is a startup failure" — which stopped being true at #70. It is a claim
  of exactly the kind **T010** is chartered to correct; left there rather than
  fixed in passing (AR-008).
- **The bind guard is still absolute.** A password door now loads, and
  `loadListen` still refuses to bind off loopback, so the door built in T003/T004
  is reachable from nowhere until **T002** relaxes it. Expected — that is T002 —
  but worth knowing that T001 alone changes nothing an operator can observe except
  two rows on the settings page.
- **`access_enabled` is browser-editable** (it is not secret, and it holds no
  credential). Turning it on from the page writes `true`, and `config.Validate`
  refuses the write if the three Access values are not there, so it cannot lock an
  operator out. Turning it *off* does not disable Access, for the reason above.
  Stated here because "a checkbox next to the door" invites the assumption that
  unticking it removes the door.
