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

---

## Iteration 2 — 2026-08-11 — T002, the bind guard relaxed rather than removed

**Did:** A non-loopback listen address is now permitted when layer 1 admits
somebody — an Access door or a `dashboard_password` — and refused when it does
not. `loadListen` takes that answer as an argument and moved below `validateDoors`
to get it. `docs/security.md`'s transport section states the new rule.

**Left:** T003 through T010.

### The bind guard is three checks, and all three had to move

The plan names `loadListen`. It is not the only reading of that rule: `httpapi`'s
package doc says so itself — `assertLoopbackAddress` on the configured string and
`assertLoopbackAddr` on the address the kernel handed back. **Relaxing only the
first would have shipped a daemon that loads a configuration it then refuses to
bind**, which is the failure an operator meets after being told their config is
fine. So all three moved together, and the two in `httpapi` were renamed
`assertBindAddress` / `assertBoundAddress`: a function still called
`assertLoopback` that returns nil for `0.0.0.0` is the comment that lies to the
next reader.

**The two packages ask the question differently on purpose.**

- `config.browserDoorAdmits(teamDomain, password)` — intent. It is what the
  operator configured, and it is also what the "admits nobody" startup banner
  reads, so the banner and the bind cannot disagree.
- `httpapi.mayBindOffLoopback(cfg, door)` — intent **and** evidence. Not a
  `closedDoor`, *and* the config names a door. Either half alone is wrong:
  - door half alone lets the **development bypass** bind off loopback, because
    `*access.Bypass` is not a `closedDoor` — it is the one layer 1 that admits
    everybody without checking anything, and it would have got the widest bind in
    the codebase. `access.NewBypass` still refuses on its own (and is now the
    only unconditional reading of the rule), but that is the backstop, not the
    reason.
  - config half alone lets a daemon whose file names a door the server did not
    build put an unauthenticated listener on the network. That is a wiring
    defect, and a wiring defect must not read as permission.

  Both halves are proven by breaking: invert either and exactly one case of
  `TestNewBindsOffLoopbackOnlyForADaemonSomebodyCanGetInto` goes red for it.

### Between here and T003, a password door still cannot bind off loopback

`verifiedLayer1` returns `closedDoor` for a password-configured daemon until T003
builds the door, so `mayBindOffLoopback`'s door half says no and the daemon
refuses to start on a LAN address. **This is deliberate and fails closed** — it
is the plan's "a `closedDoor` daemon must still refuse", read as the door the
server actually built rather than the one the file asked for. T003 makes the two
halves agree, and nothing else needs changing for it.

**Learned, for whoever picks up T003:**

- **A name is refused under every door, and that is now the *only* absolute part
  of the address rule.** `:8765` arrives at the same branch as `localhost:8765`
  (an empty host is not an IP literal), so the wildcard has to be written
  `0.0.0.0`. The refusal's wording is built from `doorAdmits` so it never demands
  loopback of a daemon that does not need it — an operator told half the rule
  fixes the address twice.
- **Test fixtures carrying a door hid this everywhere.** `config`'s `baseEnv`,
  `fileLines`, `httpapi`'s `testConfig` and the quickstart host env all set the
  three Access values, so eleven existing cases were asserting "0.0.0.0 is
  refused" on daemons that may now have it. `withoutAccess` (config) and
  `noDoorConfig` (httpapi) are the doorless fixtures; **a case about the bind
  that uses a door-carrying fixture proves nothing.**
- Two `file_test.go` fixtures used `listen = 0.0.0.0:8080` as a stand-in for "a
  value that will not load" beside `fileLines`' Access door. They say
  `localhost:8080` now — a name, refused under every door — which keeps those
  cases about the backup recovery rather than about the bind.
- The whole `-tags quickstart` suite passes here in ~35s with the deployed daemon
  still holding 127.0.0.1:8765; `-tags tmux` and `-tags dev` are green too.

**Findings, not fixed:**

- **`.specify/memory/constitution.md` Principle VI still says "The listener binds
  **loopback only**. Reachability comes from the tunnel."** That is now false, and
  it is the highest-authority document in the repo. The constitution's own
  governance section requires a PR stating what changed and why, reviewed by a
  code owner — **no task in this plan is chartered to amend it**, and an
  autonomous loop rewriting the constitution to match its own change is precisely
  what Principle II is about. It is raised here rather than done. The same
  principle's own escape hatch is arguably already satisfied ("a feature that
  widens any of these needs an explicit justification in the plan naming what now
  becomes reachable" — this plan's preamble is that justification), which is why
  this is a wording amendment and not a conflict.
- **`.env.example` and `README.md` still describe the absolute rule.**
  `.env.example` line 151 says "A non-loopback host is a startup failure" and the
  README's variable table says the same; both are **T008**'s charter ("the bind
  change"), so they were left rather than fixed in passing (AR-008). Same for
  `install.sh`'s "the daemon binds loopback and something has to carry it", which
  is **T009**'s.
- **No acceptance case starts a daemon on `0.0.0.0`.** The quickstart case that
  binds a public address is the refusal (now with the three Access values unset,
  so it is about the closed door). Proving the positive there would mean putting
  a real listener on the network of whatever machine runs the suite; it is proven
  at the unit level instead — `newServer` accepts the address and `Listen` keeps
  a listener claiming `192.168.1.10`.
- **`docs/security.md`'s "two front doors" table still says both doors are behind
  Cloudflare Access.** True today; **T003/T004** make it false. Whoever writes the
  password door owns that table.

---

## Iteration 3 — 2026-08-11 — T003, the password door

**Did:** Built the third layer 1 in `internal/httpapi/password.go` — a signed
session cookie, verified by recomputing it — and returned it from the same
`verifiedLayer1` that returns `closedDoor` and the Access validator. `docs/security.md`'s
"two front doors" section and `docs/auth-and-sessions.md`'s opening now describe
three implementations instead of one.

**Left:** T004 through T010.

### `layer1` had to take the request, and that is the whole shape of this task

The plan forbids a branch in the browser middleware, and the two doors read
**different credentials** — Access a header, the password a cookie. The old
interface was `Verify(ctx, assertion string)`, so the middleware was reading the
header itself and handing it over, which is a middleware that already knows which
door it holds. It now takes `*http.Request` and each door reads its own
credential. `assertionDoor` wraps `internal/access`'s two implementations at one
place, so nothing in that package changed and both still reach the middleware the
same way.

**This is the fixture change T004 onward will trip over:** `keyServer.validator(t)`
returns `layer1` (an `assertionDoor`) rather than `*access.Validator`, and
`stubLayer1.Verify` takes a request. Roughly twenty call sites kept working
because the fixture absorbed it — a new test that builds `access.New(...)` by
hand must wrap it in `assertionDoor{validator: v}` or it will not compile.

### Two things the plan does not state, decided and written down

- **`dashboardSessionLifetime = 12h`**, equal to `pageTokenLifetime` and chosen
  the same way. The plan fixes every other property of this cookie and not the
  number. Change the constant if you want another; nothing depends on it.
- **The password's SHA-256 is inside the signed payload**, not just the label the
  plan asks for. Without it, an operator who changes the password *because they
  think it is known* keeps admitting whoever holds a cookie minted under the old
  one, for up to a lifetime, with no recourse short of rotating the shared
  secret. It costs nothing and it makes forging a cookie need the password **and**
  the secret rather than the secret alone.

**Learned, for whoever picks up T004:**

- **`admits` and `issue` are the two methods T004 calls**, and they are on the
  concrete `*passwordDoor`, not on `layer1`. Registering the login route means a
  type assertion at registration — which is where the plan wants the decision
  ("registered only when the password door is the configured layer 1"), and is
  not a branch in the middleware.
- **The identity is `passwordOperator`**, a constant that is deliberately not
  address-shaped, for the reason `access.bypassEmail` is not. It is what the
  masthead renders and what the page token is bound to, so it must stay non-empty
  — a token bound to `""` is one every empty identity verifies.
- **`isPageTokenMAC` is now `isHexMAC`**, shared with the cookie. One call site
  and no test named it, so the rename was three lines; two copies of that
  predicate would be two rules about the uppercase twin, free to disagree.
- **gosec needs three `//nolint`s here and each has a real reason** — G124 on the
  real `SetCookie` (Secure is conditional *on purpose*: a Secure cookie on a
  plaintext LAN is one the browser never sends back), G124 on the test's
  request-side cookie (a request carries no attributes), and G101 on the fixture
  password. The linter is v2.12.2, checked (#26).
- **Every guard was proven by breaking it**, one at a time, restoring between:
  the constant-time compare, the digest in the MAC input, the expiry check, the
  canonical-spelling check, the MAC shape check, `hmac.Equal`, each of the three
  cookie attributes, and the selection in `verifiedLayer1`. Each turns exactly
  the named case red and nothing else. The cookie-attribute assertions were
  rewritten from a `switch` to independent `if`s when the first breakage proved
  the chain hid the other two.
- All four suites green here: default, `-tags dev`, `-tags tmux`, and
  `-tags quickstart` (~36s, with the deployed daemon still holding 127.0.0.1:8765).

**Findings, not fixed:**

- **`.specify/memory/constitution.md` Principle VI is still wrong** and now more
  so: it says the listener binds loopback only, and the door that makes a wider
  bind legitimate exists as of this commit. Iteration 2 raised it; no task in this
  plan is chartered to amend the constitution, and an autonomous loop rewriting
  the highest-authority document to match its own change is what Principle II is
  about. **Still owed a human PR.**
- **`verifiedLayer1` prefers Access when a Config names both doors.** No
  `config.Load` produces one — `validateDoors` refuses it — but a hand-built
  Config can, and `TestVerifiedLayer1PicksExactlyOneDoor` pins the answer rather
  than leaving it to argument. If that ever needs to be a refusal instead, it is
  one case in that switch.
- **Nothing sets the cookie yet.** `issue` has a test caller and no production
  one until T004 registers the login route, so a password-configured daemon
  currently starts, binds where its operator can reach it, and refuses every
  browser — which is *strictly* the shape the plan asked for at this task
  boundary, but it is not a usable daemon until T004 lands. Do not ship this
  commit alone.
- **`docs/components.md` has no login-form entry.** T004 must reuse `.field`,
  `.field-input`, `.field-label` and `.button` and add no class; whether the page
  itself earns a components entry is that task's call, and there is no precedent
  for a full-page single-purpose form in there yet.
