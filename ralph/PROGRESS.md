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

---

## Iteration 4 — 2026-08-11 — T004, the login page and the route that answers it

**Did:** Built `GET /login` and `POST /login` in `internal/httpapi/login.go`, the
page in `web/templates/login.html`, and registered both **only** when the layer 1
`newServer` was handed is the password door. A sign-in now issues the cookie T003
built and nothing else does. `docs/security.md`, `docs/auth-and-sessions.md` and
`docs/components.md` all describe the two routes.

**Left:** T005 through T010.

### These are the only two routes in front of layer 1, and that is the task

Everything else on this door is registered through `handleBrowser` or
`handleAction` and cannot be reached without a verified operator. These cannot be
reached *with* one — the credential is what they exist to produce. So there is a
third registration function, `handleLogin`, and it is a function rather than a
flag for the reason `handleAction` is one: the promise it makes is the most
consequential of the three, so registering a route in front of the door has to be
a thing somebody types.

What bounds it, all of it tested:

- **Registered from the door that was built, never from `cfg.DashboardPassword`.**
  A daemon whose file names a door the server did not build is a wiring defect,
  and a wiring defect must not be what puts a login form on the network — the same
  distinction `mayBindOffLoopback` draws, for the same reason. Under Access,
  `/login` matches no route and is the browser door's 404 from *behind* layer 1.
- **The password is read from `PostForm` and never `Form`**, so a submission that
  put it in the query string fails rather than working.
- **Every refusal is `refuseBrowser`'s bytes** — the same 401 a missing cookie
  gets. Two sentinels tell the trail which; a test asserts they are not one string.
- **A refused attempt sets no cookie**, which is `issue` being last rather than a
  rule anybody remembers.

### The action gate is absent and cannot apply

Two of its three checks are the layer-1 identity and a page token bound to it, and
neither exists before this route runs. Half a gate would be the second,
differently-shaped authorisation path this milestone exists to not have. **What is
left open is that a hostile page can make the operator's browser submit guesses it
cannot read the answer to** — that is T005's limiter, and it is the reason that
task is not optional polish. See the finding below.

### Two decisions the plan does not make

- **No version and no header on the login page.** Every other page shows the
  version, and every other page is behind layer 1. This one answers a stranger,
  and `version.go` already says why that matters: "a version is exactly the fact a
  scanner would like for free". The header goes with it for a different reason —
  the masthead exists so it is never ambiguous whose credentials are driving
  sessions on this host, and there are none yet.
- **303 to `/`, with no "return to" parameter.** An unauthenticated caller
  supplying the address a successful sign-in redirects to is an open redirect with
  a login form in front of it.

**Learned, for whoever picks up T005:**

- **`handleLogin(pattern, action, handler)` is where the limiter goes**, wrapped
  around the handler at that one line the way `limitCreates` is wrapped in
  `handle`. Note the difference the plan already names: `limiter.allow` takes an
  `auth.CallerID` and sits *behind* layer 2, because a create's budget is spent by
  an identity. There is no identity here, so the key is the source — and
  `limiter.allow`'s signature is `auth.CallerID`, so T005 either widens that type
  or gives the login its own limiter over the same bucket code. **Reusing the
  bucket, not copying it, is what the plan asks for.**
- **`RequestAudit` grew an unexported `allow(caller)`.** The two middlewares set
  those fields inline because they learned the identity on the way past; the login
  route is the one route that *establishes* one, so the handler takes the decision
  and this is the one nil-safe line for it.
- **`newAuditedServerOn(t, cfg, browser)`** is the new fixture: which routes a
  daemon registers is decided at construction, so `settingsOn`'s
  adjust-the-Config-afterwards shape cannot express a claim about them.
  `newLoginDaemon` builds through the real `verifiedLayer1` deliberately — a
  hand-built `*passwordDoor` would prove registration works for a door no
  configuration produces.
- **Adding a page costs three tests, not one.** `renderedPages` in
  `partials_test.go` fails for a `templates/*.html` nothing renders;
  `TestEveryPageCarriesTheHeader` now consults `pagesWithNobodyToName`, a map to a
  *reason* so the exception has to be argued where it is taken; and
  `TestEveryPageLoadsTheLoopThatDrivesItsRain` was rewritten to ask each rendered
  page whether it drew a canvas rather than assuming every page carries a header.
  That last one is strictly stronger than it was — it now also catches a page that
  renders a canvas of its own without the loop.
- **`TestTheStylesheetAndTheMarkupNameTheSameThings` is what "no new class"
  really means here**: a class a template renders with no rule behind it fails
  that sweep, so the page's vocabulary is pinned twice — once by the stylesheet
  and once by `loginPageVocabulary` in `login_test.go`.
- **Every guard was proven by breaking it**, one at a time, restoring between:
  registering the routes unconditionally, reading `Form` instead of `PostForm`,
  issuing the cookie before the check, dropping the security headers, folding the
  two sentinels into one string, putting a version and a settings link on the
  page, dropping the allow from the successful attempt, and never issuing at all.
  Each turns exactly the named cases red and nothing else.
- All four suites green here: default, `-tags dev`, `-tags tmux`, and
  `-tags quickstart` (~35s, with the deployed daemon still holding 127.0.0.1:8765).
  Linter is v2.12.2, checked (#26), 0 issues.

**Findings, not fixed:**

- **Nothing points an operator at `/login`.** A browser arriving at `/` with no
  cookie gets the uniform 401, which by design says nothing — so the only way to
  find the sign-in page is to know the path. Redirecting the refusal would make it
  non-uniform *and* put a branch keyed on which door is live into the browser
  middleware, which the plan forbids outright. **This is T008's** ("document the
  LAN deployment"): the README has to say `http://host:port/login` in as many
  words, or the daemon is unusable by anyone who did not read this file.
- **A cross-site POST to `/login` is not refused**, and that is a deliberate
  omission rather than an oversight. Login CSRF buys an attacker nothing here —
  there is one account, so there is no session to fixate, and they cannot read the
  answer — but it does let a hostile page **spend the operator's own rate-limit
  budget from the operator's own source address**, which is a lockout. **T005 owns
  this**: either the limiter tolerates it, or that task adds `crossSiteAction`'s
  header check to this route and states why half a gate is acceptable where a
  whole one cannot apply. Raised here rather than done, because adding an
  unasked-for check to the one route in front of layer 1 is not a call to make in
  passing.
- **`.specify/memory/constitution.md` Principle VI is still wrong** — "the listener
  binds loopback only". Iterations 2 and 3 raised it; it is still owed a human PR,
  and no task in this plan is chartered to amend the constitution.
- **`docs/security.md`'s two-front-doors table still says both doors are behind
  Cloudflare Access** in its own opening paragraph, and the M12 paragraph beneath
  it now contradicts that. Iteration 2 raised it against T003/T004; T003 rewrote
  the *layer 1* table and this task rewrote the password-door section, and neither
  touched the sentence above them. It is one paragraph and it is now the last
  stale claim in that file.
- **`TestEveryPageShowsTheVersion` still walks a hand-written list of four pages**
  and so never noticed a fifth arriving without one. The login page's absence from
  it is correct and deliberate, but the list is a guard that cannot see a new page
  — the same shape as the `registeredPatterns` gap the sweep's own comment admits
  to. Deriving it from `renderedPages` minus `pagesWithNobodyToName` is a small
  change and was left out of this task as AR-008 churn.

---

## Iteration 5 — 2026-08-11 — T005, the budget in front of the door

**Did:** `POST /login` is now counted against the address it came from, at six a
minute with a burst of three, on **the create route's own token bucket** — made
generic over its key rather than copied. A submission the browser itself calls
cross-site is refused before it can spend anything. `docs/security.md` states
both, and the four sentinels a refusal can carry.

**Left:** T006 through T010.

### The limiter is generic, and that is what "reuse the bucket" had to mean

`limiter.allow` took an `auth.CallerID`, because a create's budget is spent by an
identity behind layer 2. The sign-in route has no identity — producing one is the
whole of what it does — so the key is the source. Three readings were available
and two are wrong:

- **Cast the address into an `auth.CallerID`.** Free, and a lie of exactly the
  kind iteration 2 renamed `assertLoopback` over. A `CallerID` is something layer
  2 authenticated.
- **A second bucket implementation for the login.** Two rules about what a budget
  is, free to disagree — and the plan says reuse the bucket, not the pattern.
- **`limiter[K comparable]`.** One bucket, two key types, and the compiler
  refusing to let either budget be spent by the other's key.

The cost is a type parameter on six signatures and `newLimiter[auth.CallerID]` at
the one existing call site. `newLimiter` also took a `what` string: two budgets
now share its two startup refusals, and "the create rate limiter" named as the
thing that could not be built would send an operator to the wrong variable.

### The cross-site check, which iteration 4 left to this task to decide

Iteration 4's finding gave T005 two sanctioned answers — tolerate a cross-site
POST, or refuse it and say why half a gate is acceptable. **It is refused**, and
the argument that decides it is that *this task creates the vector*: before there
was a budget there was nothing for a hostile page to exhaust. A guard that adds a
new denial of service to the one route that can end one is not finished while a
five-line reuse of `crossSite` removes the cheapest way to trigger it.

It is `crossSite` and not `crossSiteAction` — present-and-wrong refuses, absent
does not. Every argument `crossSiteAction` makes for refusing an absent header is
about a route that *changes* something; this one changes nothing, and the caller
it would newly refuse (a script signing in with curl) has an address and
therefore a budget of its own. It authorises nobody, which is why it is not the
second authorisation path the milestone forbids.

### Three decisions the plan does not make

- **`loginRatePerMin = 6`, burst 3**, a constant and not a variable. Every other
  bound here is the operator's because they know their host; this one is an
  attacker's budget, and a variable is a variable somebody sets to 6000 on the
  one route in front of layer 1. **This limit is not what makes the password hard
  to guess** — `MinDashboardPasswordLen` is. It buys slow, loud, and cheap to
  refuse.
- **`GET /login` is not limited.** Serving the form costs what refusing an
  unauthenticated request costs anywhere else on this door, none of which is
  limited either — and a budget a page load could spend is one an
  `<img src="/login">` could empty, taking the sign-in form away from the
  operator who needs it. The budget belongs to guesses.
- **The refusal is the uniform 401, not the create route's 429.** A caller that
  proved who it is may be told to slow down; one that has proved nothing is told
  nothing, so a brute-forcer cannot tell a refused guess from a guess nobody read
  and cannot pace by the answer. The cost — an operator locked out by their own
  retries reads why on the trail rather than on the page — is real and taken
  deliberately.

**Learned, for whoever picks up T006:**

- **The checks live in `admitLogin`, in an order that is the design.** Cross-site
  (one header lookup), then the budget, then `ParseForm`, then the comparison.
  Each is cheaper than the next and bounds it, and the budget is spent *before*
  the two sides are compared — a limiter below the comparison stops nobody who is
  about to succeed. Iteration 4 expected the limiter to be middleware around
  `handleLogin`; that turned out to be the one shape that cannot work, because it
  puts both verbs on one bucket.
- **A test whose two verbs come from different addresses proves nothing about one
  bucket.** `httptest.NewRequest` fills `RemoteAddr` with `192.0.2.1:1234`, so
  the page-is-not-limited test passed under a deliberately broken build until
  both halves were made to name one source. It was the breakage exercise that
  found it, not the review — `d.form(t, from)` exists beside `d.get` for that.
- **`d.logins = testLoginLimiter(t, clk)`** is how a test drives the budget:
  the server builds its own on the host clock, and a burst refused by a limiter
  that is also refilling proves the refusal but not the recovery.
- **Every guard was proven by breaking it**, one at a time, restoring between:
  the limiter removed, the limiter moved below the password comparison, the port
  kept in the key, one bucket for the whole daemon, the cross-site check removed,
  it widened to `crossSiteAction`, and the limiter moved into `serveLogin` so
  both verbs shared a bucket. Each turns exactly the named cases red.
- All four suites green: default, `-tags dev`, `-tags tmux`, and `-tags
  quickstart` (~36s, with the deployed daemon still holding 127.0.0.1:8765).
  Linter is v2.12.2, checked (#26), 0 issues.

**Findings, not fixed:**

- **The limiter's map is now growable by a stranger, and `forgetFull` is the only
  thing that bounds it.** The create limiter's keys are identities layer 2
  authenticated — one of them — so nobody thought about this. The sign-in
  limiter's keys are addresses an attacker chooses. What holds is that a bucket
  survives only while it is partly spent and every decision sweeps the full ones,
  so the map holds about as many entries as there were distinct sources in the
  last refill window; the sweep is O(n) in that number, paid by the request that
  grew it. It is written into `forgetFull`'s comment. **No cap was added**: a cap
  turns a distributed flood into a global lockout, which is worse than the
  slowdown it prevents, and reaching a painful n needs a source count an attacker
  on this LAN does not need to bother with. Revisit if this daemon ever faces a
  network it does not trust.
- **`docs/auth-and-sessions.md`'s "Lifetimes and limits" table has no row for the
  dashboard session cookie (T003's 12 hours) or the sign-in rate (this task's six
  a minute).** Both are stated where they are decided and in `docs/security.md`;
  that table is where an operator would look for them together. Adding only this
  task's row would leave it half right, so neither was added — it is one edit for
  a documentation task, and **T008** is the nearest.
- **`.specify/memory/constitution.md` Principle VI is still wrong** — "the
  listener binds loopback only". Iterations 2, 3 and 4 raised it. Still owed a
  human PR; no task in this plan is chartered to amend the constitution.
- **`docs/security.md`'s two-front-doors opening paragraph** was raised by
  iterations 2 and 4 as stale. Reading it again with the M12 sections beside it,
  it is *qualified* rather than false — "in the deployment this was written for"
  — and the table's edge column is true of that deployment. It is under-stated,
  not wrong, which is why a third iteration has left it. Worth one sentence from
  whoever owns the file next; it is not the defect the earlier findings implied.
- **`TestEveryPageShowsTheVersion` still walks a hand-written list of four
  pages** (iteration 4's finding, unchanged — nothing this task touched brings it
  closer or further away).
- **`internal/audit`'s `TestTheLeakSuiteReallyDrivesTheDaemon` is flaky, and the
  mechanism is a clock rather than a race.** It failed once here —
  `leak_test.go:1602`, "the card a create rendered back carries no page token" —
  and passed on every one of ~20 further runs, including `-count=12`. The cause
  is that `run.pageProof` is scraped from a fleet render at one instant and
  compared against the fleet render a create redirected to a few milliseconds
  later, while `pageKey.mint` stamps `now.Add(pageTokenLifetime).Unix()` —
  **second granularity**. The two renders are the same token only when they fall
  in the same second, so the assertion fails whenever the create straddles a
  boundary. Nothing about it is this task's: the page token, the create path and
  the Access door are all untouched here. The fix is in the test, not the daemon
  — re-scrape the proof from the card being compared, or drive the leak run on a
  pinned clock the way `httpapi`'s fixtures do. Left as a **quick-fix-lane** item
  rather than done in passing, because a fresh context should not be changing a
  suite whose whole purpose is to be the thing nobody edits to make a build pass.
