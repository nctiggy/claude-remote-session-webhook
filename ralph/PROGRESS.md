# Ralph progress notebook

**Milestone 17 — `crswd-axi` (task lm-90).** Started 9/9/26.

Milestone 16's notebook is in `ralph/archive/2026-09-09/PROGRESS.md`, beside its
plan and prompt. This file starts empty on purpose: the loop reads it fresh every
iteration, and a notebook carrying a finished milestone's conclusions is a
notebook that tells the next iteration it is already done.

The plan is [`ralph/IMPLEMENTATION_PLAN.md`](IMPLEMENTATION_PLAN.md). What "done"
means is [`ralph/VALIDATION_CONTRACT.md`](VALIDATION_CONTRACT.md), written before
the plan was decomposed.

---

## How to write in this file

Append one section per iteration, newest last:

```
## Iteration N — TNNN

**Did.** One or two lines.
**Learned.** Anything the next iteration would waste time rediscovering.
**Left.** What is still open.
**Noticed but did not fix.** Findings go here, never silently dropped.
```

`NEEDS CLARIFICATION` goes under its own heading with the task marked `- [!]` in
the plan. Constitution principle II: unknowns are surfaced, never invented.

---

## Known before iteration 1

Measured in this repository on 9/9/26 while planning. The full detail is in the
plan's *Ground truth* section — this is the short list of what will bite.

- **A bearer token is disclosed exactly once.** `internal/session/token.go`:
  *"there is no re-issue path (FR-015)"*. Credentials reach a caller only in the
  `201` from create and in `listSessions`, which calls `ClaimPending` and stamps
  `token` on sessions adopted at startup. A bare `crswd-api GET /sessions` after
  a restart prints them once and loses them. T006 exists for this.
- **Identical signed requests in the same second are a replay.**
  `Authenticator.Verify` observes the signature value. A retry must land on a
  different unix second, not merely be re-signed.
- **gitleaks scans `deploy/crswd-axi/`.** Not allowlisted. Fixtures must use a
  `test-only-` prefix or the repeated hex alphabet (`0123456789abcdef`), both of
  which `.gitleaks.toml` allows by construction.
- **`AGENTS.md` is 147 lines; CI fails it at 150.** T009 has two lines to spend.
- **Node here is v22.23.2.** `node --test`, `node:util.parseArgs` and global
  `fetch` are all present, so `lib/` and `test/` need no dependency at all.
- **`npm install` reaches the network.** Only T001 needs it. If the host is
  offline, T001 blocks and T002–T007 are still doable — they import nothing.

---

## Carried forward from milestone 16

Open findings the previous milestone recorded and did not resolve. None of them
blocks this milestone; they are here so they are not lost with the archive.

- **#121 is still open and only a human can close it.** Milestone 16's T007
  documentation shipped in `63f2694`, but `gh` is on no allowlist in
  `.claude/settings.json`, so no loop iteration can read or close the issue. The
  closing comment is written out ready to paste in
  `ralph/archive/2026-09-09/PROGRESS.md`, Iteration 7.
- **`TestQuickstartStory5RateLimit` has a temp-home race.**
- **`aria-describedby="card-id-…"` on the reflow button** resolves only because
  the session page also draws the card. A page rendering a pane without a card
  would ship a dangling reference silently.
- **Two `wantErr` rows in `TestParseSessions`** fail on field count rather than
  the reason they name — pre-existing.
- **The width shown may be the intent rather than the truth**, until an operator
  puts a session back to `window-size latest` and attaches.
- **The config migration still runs in the old binary** — a spec question from
  milestone 15, untouched.

---

## Iterations

_None yet._
