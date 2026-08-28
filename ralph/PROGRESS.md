# Progress notebook — Milestone 16, voice conversation inside Claude Code

Append-only. One entry per iteration. This is the only memory the loop has.

Milestone 15's notebook is archived at `ralph/archive/2026-08-28/PROGRESS.md`,
alongside the plan and prompt it belonged to.

---

## Iteration 0 — planning (not a loop iteration)

Wrote `ralph/VALIDATION_CONTRACT.md`, `ralph/IMPLEMENTATION_PLAN.md` and
`ralph/PROMPT.md` for milestone 16 after reading the real repository. Nothing was
implemented.

### What the investigation established

Recorded here so no iteration pays to rediscover it.

- **`connect-src 'self'` is the reason Wispr is not in this milestone.**
  `internal/httpapi/render.go` declares the policy as one constant and
  `internal/httpapi/render_test.go` transcribes it by hand, so the page cannot
  open a socket to any origin but this daemon. `SpeechRecognition` and
  `speechSynthesis` are browser APIs rather than fetches, so they need no change
  to it. This is the whole architectural decision of the milestone.

- **The API's route table is a closed set of six.** `routes` in
  `internal/httpapi/server.go` is the surface fixed by
  `specs/001-crswd-daemon-core/contracts/http-api.md`, and a seventh
  entry is a contract change. Everything this milestone adds goes on the
  **browser door** instead, through `handleBrowser` (reads) or `handleAction`
  (writes), neither of which appends to `s.registered`.

- **`handleAction` already carries the whole gate**: layer 1, the
  `Sec-Fetch-Site` refusal, the page token and the audit record. A new write
  route inherits all four by being registered with it, and re-implementing any of
  them is the defect the two-function split exists to prevent.

- **This repository tests its JavaScript from Go.** `stylesheet_test.go` and
  `partials_test.go` read `web.Static` and `web.Templates` and assert on the
  source — `TestTheStreamClientReplacesTheScreenWithText`,
  `TestTheFleetClientSubscribesAndSaysWhenItStops` and about seventy others.
  There is no npm and no JS runner, and adding one is not this milestone. Client
  tasks are verified the same way the existing client tasks were.

- **`crswd.js` is a series of IIFEs**, each `'use strict'` and closed over its
  own scope. Nothing is exported, which is why a testable reduction has to live
  in Go rather than in the script.

- **The submit interceptor already exists** (`crswd.js`, the delegated `submit`
  listener) and posts any form on the page, follows the redirect, extracts the
  banner and writes the toast. A voice turn should call `requestSubmit()` and let
  it do the work — `session.html` already renders the `#action-toast` region the
  module bails without.

- **`redirectOutcome` always redirects to the fleet** (`pathFleet = "/"`). That is
  fine behind the interceptor, which does not navigate, and it is why the new
  outcome codes need entries in `outcome.go`'s `banners` map like every other.

- **`Manager.Prompt` refuses an empty text with `session.ErrEmptyPrompt`** and
  names only the session in its errors, never the text. The new route's refusals
  should borrow that vocabulary rather than invent one.

- **The stream machinery is reusable in pieces**: `streamCap`, the `panes`
  registry, `openStream`, `stream.hold` and the terminal `end` event. A second
  stream attaching to the same `panes` buffer costs no extra `capture-pane` exec
  per session, which is the whole reason that registry exists.

- **Toolchain verified on this worktree**: `go build ./...` exits 0,
  `go test ./internal/httpapi -run <name>` works, and `golangci-lint` is
  **v2.12.2** — the version check in the plan's conventions is satisfied.

### Known unknowns, recorded rather than guessed

- **Which terminator phrases actually feel right in a shower.** The plan fixes
  `go ahead`, `over` and `over to you` so the loop has something to build; T009
  files this as Q4 in `docs/mobile-open-questions.md`, where only the operator
  can answer it.
- **Whether one dictated turn per fleet render is too expensive.** The submit
  interceptor follows the redirect, so every delivered turn costs a fleet render.
  Acceptable at conversational pace; noted in case it is not.
- **Two open streams per voice session.** The page keeps the pane stream and adds
  the speech stream, so a voice conversation spends two slots of
  `CRSW_MAX_STREAMS` rather than one. Not a defect; worth knowing before an
  operator wonders why their cap feels half the size.

### What is left

All nine tasks. T001 is the topmost open item.
