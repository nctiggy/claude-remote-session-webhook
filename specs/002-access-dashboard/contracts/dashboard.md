# Contract: The Read-Only Dashboard

**Feature**: [../spec.md](../spec.md) · **Plan**: [../plan.md](../plan.md) ·
**Research**: [../research.md](../research.md) · **Data model**: [../data-model.md](../data-model.md)

The browser-facing surface: which routes exist, which door owns every route on the
daemon, the headers every browser response carries, and the rendering rules — most
of which are security rules wearing design clothes (Principle VII). The live stream
has a contract of its own: [stream.md](./stream.md).

---

## Two doors, one route table

Every route is owned by exactly one door and refused only by the check that applies
to it (FR-012, FR-013): an API request is never refused for lacking a browser
identity, and a browser request is never refused for lacking a request signature.

| Route | Door | Serves |
|---|---|---|
| `POST /sessions`, `GET /sessions`, `GET /sessions/{id}`, `DELETE /sessions/{id}`, `POST /sessions/{id}/prompt`, `GET /sessions/{id}/output` | **API** (layers 2 + 3) | [Milestone 1's six operations](../../001-crswd-daemon-core/contracts/http-api.md), byte-for-byte unchanged (FR-014, FR-015) |
| `GET /` | Browser (layer 1) | The fleet: summary row, then one card per session |
| `GET /sessions/{id}/view` | Browser (layer 1) | One session: card plus live pane |
| `GET /static/crswd.css`, `GET /static/crswd.js` | Browser (layer 1) | The only two assets, embedded via `go:embed` (research [D9](../research.md)) |
| *anything else* | Browser (layer 1) | See below (FR-013d) |

The browser door serves `GET` only. There is no mutating route behind it — that
absence is the read-only guarantee's foundation, below.

At the edge, both doors sit behind Access on **one hostname** with no bypass path:
browsers admitted by the identity provider, the API client by a service token
(FR-013a). That is deployment configuration, verified against the running hostname
(SC-014, explicitly exempt from SC-017); the daemon's own obligation is FR-013b —
its checks run regardless of what the edge decided.

### Unrouted requests move doors — the one deliberate behaviour change

Milestone 1 answered every unmatched path through the API door: uniform 401
unsigned, uniform 404 signed. FR-013d changes that: a request matching **no route in
either table** is answered by the browser door — layer 1 validates it, and a
verified operator gets an HTML not-found page in the dashboard's own dress rather
than the API's raw JSON refusal. An unverified caller gets layer 1's uniform
refusal, so a scanner still learns nothing (the trail still records `route.unknown`,
or `access.reject` if layer 1 refused first).

This is an amendment to what
[milestone 1's contract](../../001-crswd-daemon-core/contracts/http-api.md)
documented for unknown paths, made because the spec makes it explicitly (FR-013d) —
a signed-in browser that mistypes a URL currently receives a refusal from an
interface it never used. The six operations, their signing, and their responses are
untouched (FR-014, FR-015, SC-007); no shipped client ever depended on the
unrouted-path behaviour, which is why this is the one place the spec permits the
answer to change. Wrong-method requests to known paths (`PUT /sessions`) fall under
the same rule: no route matches, so the browser door answers — still never a `405`,
still never an `Allow` header.

---

## Response headers

Every browser-door response — pages, assets, refusals, the not-found page — carries
the headers `docs/security.md` names, exactly:

| Header | Value |
|---|---|
| `Content-Security-Policy` | `default-src 'none'; script-src 'self'; style-src 'self'; connect-src 'self'; img-src 'self' data:; base-uri 'none'; form-action 'self'; frame-ancestors 'none'` |
| `Strict-Transport-Security` | `max-age=31536000; includeSubDomains` |
| `X-Content-Type-Options` | `nosniff` |
| `Referrer-Policy` | `no-referrer` |

The CSP is sent **unmodified** (research [D9](../research.md)). No `unsafe-inline`
ever: the rain canvas and the stream client live in `crswd.js`, styling lives in
`crswd.css`, and a change that needs an inline exception is wrong by
`docs/security.md`'s own words. `default-src 'none'` plus `'self'`-only sources is
also FR-025 enforced by the browser itself — a template that slipped a CDN reference
past review would fail to load, visibly, rather than quietly shipping a third-party
dependency.

Two additions this contract makes, each with its reason:

- **`Cache-Control: no-store`** on `GET /`, `GET /sessions/{id}/view`, and the
  stream. These responses contain session names, working directories, and pane
  content — all secret under `docs/security.md` §3 — and a browser or intermediary
  cache is a copy on disk that outlives the session. The two static assets are
  exempt: they contain no session data, and caching them is what makes the page
  cheap. This header belongs in `docs/security.md`'s table when it is amended for
  this milestone (the spec already schedules that amendment for the CORS rule).
- **No `Access-Control-Allow-*` header on any route, either door, ever** (FR-034c,
  research [D8](../research.md)). This is load-bearing, not hygiene: the browser's
  layer-1 credential is a cookie and therefore **ambient** — it rides on requests a
  hostile third-party page triggers, and the edge converts those into a valid
  assertion. Same-origin policy is the only thing stopping that page *reading* the
  response, and it holds exactly as long as the daemon never opts out of it. The
  per-session token would have forced a preflight and provided this protection a
  second way; declining it for streams (FR-034a) means this absence is the
  protection. It is asserted by a test that sweeps **every** response — an absence
  nobody checks is one refactor away from present.

The API door's six responses gain none of these headers: FR-014 freezes every
response the daemon returns to a milestone 1 client, and headers are part of a
response. (The CORS rule is an absence, so it holds there without changing a byte.)

---

## Rendering rules that are security rules

Everything a Claude session prints reaches this page. So do session names and
working directories — a caller chose them (FR-030). All of it is untrusted, and the
defence is structural (FR-028: closed by construction, not by sanitising):

- **Server-rendered content goes through `html/template` as plain strings.** The
  engine escapes text nodes by default; the contract is that nothing ever opts out.
  `template.HTML`, `template.JS`, and any `safeHTML`-style helper are forbidden on
  pane content, names, and working directories — not for ANSI colour, not for links,
  not ever. There is no valid reason, and `docs/security.md` says so in terms.
- **Terminal escapes are stripped server-side before text reaches a template**
  (FR-029). Milestone 1 already owns this: capture without `-e`, then
  `tmuxctl.Strip` — the same path feeds the initial pane render and the stream, so
  there is one stripper, not two that agree today.
- **The live half assigns `textContent`** — never `innerHTML`, never a swap that
  treats a payload as markup ([stream.md](./stream.md), research
  [D4](../research.md)).
- **Names and working directories render as text on the same terms as output**,
  truncated with a `title` attribute when long (the fleet edge case), because a
  64-character name must break the layout's line, never the layout.
- **An adopted session's missing name and working directory render as an explicit
  statement that the value is unknown** — dim, sans-serif prose, visually unlike a
  real value — and never as an invented placeholder (FR-018a). This is a routine
  state after every restart, not an edge case: milestone 1 records neither field on
  adoption, on purpose, because nothing on the host carries them.

No inline `<script>`, no inline `style=`, no event-handler attributes: the CSP
refuses them at runtime, and the template sweep test refuses them at build time —
two independent enforcements, because the CSP failing open (a proxy stripping
headers) must not be the only thing between a template and execution.

### No htmx in this milestone

`docs/components.md` and `docs/security.md` anticipate htmx for the dashboard's
interactive parts. Every interactive part is a milestone 3 action; a read-only
dashboard is server-rendered documents plus one `EventSource`. So this milestone
ships exactly two assets — `crswd.css` and `crswd.js` — and no client framework,
vendored or otherwise. Embedding htmx now would mean shipping a third-party script
that nothing calls, purely as attack surface. It arrives with the first feature that
swaps a partial.

---

## Read-only, structurally (FR-022)

The guarantee is not "the buttons are hidden". It is that the capability does not
exist, at four independent layers — remove any one and the others still hold:

1. **No mutating route exists behind the browser door.** The door serves `GET`
   only; there is nothing to POST to that a cookie could authorise.
2. **Every mutating route requires a layer-2 HMAC signature a browser cannot
   produce.** No page, asset, or script holds the shared secret, so the dashboard
   could not sign a create or destroy even if it grew a button (FR-024a's "they
   would be non-functional as well as out of scope").
3. **The templates render no action affordance.** The canonical session card and
   empty state are documented in `docs/components.md` *with* action rows; the
   component takes the action row as a parameter, and this milestone passes none —
   absent parameter, not deleted code milestone 3 must restore (FR-024a). The
   rendered HTML contains no `<form>`, no `<button>` that submits, and no
   htmx-style mutation attribute — asserted by a test over the rendered pages.
4. **The dashboard's reads reach no mutating code path.** Its handlers call the
   owner-scoped read paths only (FR-017, FR-037) — `Store.List(owner)` and the
   clock-neutral single read — never `Create`, `Destroy`, `Prompt`, or the
   owner-blind lookups milestone 1 keeps unexported on purpose.

This structure is also why the streams-over-ambient-cookie posture is safe **in this
milestone only**: with zero browser-reachable writes, classic CSRF has nothing to
forge. That reasoning expires with milestone 3's first browser-driven write, and the
spec's Out of Scope records it so it cannot be inherited by assumption.

---

## The pages

### `GET /` — the fleet

Rendered from the owner-scoped list, in one page load with no further interaction
(SC-003):

1. **Header** — brand left; the operator's verified email right, from the validated
   assertion and never from anything the request supplies (FR-020). Ambient rain
   canvas behind at `.16`, per the design system.
2. **Summary row before any detail** — a count per display state, derived from the
   cards below it ([data model](../data-model.md#sessionview)). A dashboard is
   scanned, not read.
3. **Session grid** — one canonical card per owned session: name, state pill (text
   label always — FR-019), working directory, age. Cards link to the session view.

**The empty state** (FR-021): with zero sessions, the full-strength rain field and
one sans-serif explanation that nothing is executing on this host — never a blank
region, and **without** the "Start a session" action `docs/components.md` documents,
which is milestone 3's (FR-024a). The canvas is `aria-hidden` and vanishes entirely
under `prefers-reduced-motion`, leaving the message.

### `GET /sessions/{id}/view` — one session

The same canonical card (the card is the only place a session's summary is composed)
above the pane viewer, whose live behaviour is [stream.md](./stream.md). The initial
screen is server-rendered into the `<pre>` — escaped text, like everything else — so
the page is useful before the stream connects.

The page states plainly that the pane shows the **live screen, not scrollback**
(FR-032a): successive captures are redraws of a full-screen program, attaching on
the host is what shows history, and an interface that silently implies otherwise is
one an operator will trust wrongly.

An unknown session, another owner's session (the synthetic second owner of
FR-037b), and a session that no longer exists all produce **one byte-identical
uniform 404** through this route — the dashboard's own path, not a pointer at
milestone 1's API test (FR-037b, SC-016). The enumeration reasoning is milestone
1's, unchanged: the difference between "not yours" and "does not exist" is what
enumeration is made of.

The ownership comparison runs on **every** session-scoped request even though
production has one owner today — a check removed because it always passes is a check
that will not be there when a second identity arrives (FR-037b).

---

## Design-system obligations that are testable

Principle VII applies for the first time, and these are the obligations a test can
hold (the untestable taste lives in `docs/design-system.md` itself):

| Obligation | Enforced by | Covers |
|---|---|---|
| Every state carries a text label; colour is reinforcement | Rendered-page assertion: each card contains the state as text | FR-019, SC-009 |
| Tokens only — no hard-coded colour, size, or font in any template | Sweep over `web/`: no hex literal, no `px` literal, no `font-family` outside the token block of `crswd.css`, no `style=` attribute in templates | FR-023 |
| Canonical components only — one card, one pill, one pane viewer | All partials live in `web/templates/partials/`; pages compose them; a second card partial is a defect by inspection of the tree | FR-024 |
| Keyboard operable, visible focus | `:focus-visible` outline present in `crswd.css`; every interactive element is `<a>` or `<button>`; verified by keyboard walk in [quickstart](../quickstart.md) | FR-027, SC-010 |
| Reduced motion removes the animated background | `prefers-reduced-motion: reduce` leaves zero rain canvases rendering; scanlines removed likewise | FR-027, SC-011 |
| Zero external origins | Rendered pages contain no `http://`/`https://` reference in any `src`, `href`, `@import`, or `url()`; the CSP independently refuses any that slip | FR-025, SC-005 |
| Identity in the header | Rendered from the validated assertion; a request-supplied value cannot reach it because the template is fed the `VerifiedOperator`, not the request | FR-020, FR-036 |

---

## Audit

One record per request, browser and API alike (FR-016, SC-008): `dashboard.view`
for the two pages, `dashboard.asset` for the two assets, `route.unknown` for
unrouted paths, `access.reject` when layer 1 refuses — the action table and the
reasoning live in [data-model.md](../data-model.md#auditrecord--additions). No
record ever carries pane content, a name, a working directory, an assertion, or a
token (FR-035).

---

## Contract tests

Each maps to a requirement or success criterion and must fail if the behaviour is
removed (SC-017). Layer-1 validation itself is
[access-jwt.md](./access-jwt.md)'s table; these are the dashboard's own.

| Test | Asserts | Covers |
|---|---|---|
| Valid identity assertion, sessions exist | Summary row, one card per owned session, identity in header, one page load | FR-017, FR-018, FR-020, SC-003 |
| Session idle past threshold vs recently active | Cards read **idle** / **running** as text; the derivation uses `IdleDeadline()` | FR-019a–c, SC-009 |
| Zero sessions | Empty state with explanation; no action affordance | FR-021, FR-024a |
| Adopted session (no name, no workdir) | Explicit unknown statement; no invented placeholder | FR-018a |
| Session name / output containing `<script>`, `<img onerror=…>`, unclosed tags, null bytes, invalid UTF-8 | Rendered as visible text; zero script execution | FR-028, FR-030, SC-004 |
| Long name and long workdir | Truncated with `title`; layout intact | edge case |
| Rendered pages and stylesheet | No external origin anywhere; no inline script or style; no hex/px in templates | FR-023, FR-025, SC-005 |
| Header sweep over **every** response, both doors | Security headers present on browser responses; `no-store` on pages; **zero** `Access-Control-Allow-*` anywhere | FR-026, FR-034c |
| Rendered pages | No `<form>`, no mutating control, no route reference to the six API operations | FR-022, FR-024a |
| Browser request with no layer-2 signature | Served (not refused for the API's reason) | FR-012 |
| API request carrying a browser assertion | Served exactly as milestone 1 serves it; assertion ignored | FR-012, FR-013b |
| Unrouted path, verified operator | HTML not-found page from the browser door; `route.unknown` recorded | FR-013d |
| Unrouted path, no/invalid assertion | Layer 1's uniform refusal; nothing about the route table disclosed | FR-013d, FR-010 |
| Milestone 1's acceptance suite, unchanged | All six operations identical before and after | FR-014, FR-015, SC-007 |
| Synthetic second owner's session via `/sessions/{id}/view` | Byte-identical to unknown-ID 404 | FR-037b, SC-016 |
| Reduced-motion media query | Zero rain canvases active | FR-027, SC-011 |
| Full trail capture across all of the above | One record per request; no session content, assertion, or token | FR-016, FR-035, SC-008 |
