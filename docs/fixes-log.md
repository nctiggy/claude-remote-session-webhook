# Fixes Log

> The **quick-fix lane**. Append-only. Newest at the bottom.

For changes too small to deserve a spec: a typo, a one-line bug, a wrong copy
string, a missing null check. Fix it, prove it, log it here in one line.

**Format** — one line, no essays:

```
- YYYY-MM-DD — What was wrong; what you changed. (#issue-or-PR)
```

Anything that needs more than one line of explanation is not a quick fix. Run it
through Spec Kit instead (`/speckit-specify`).

Why this exists: without a log, ad-hoc fixes are invisible. A recurring entry here
is a signal that something needs a real design change rather than a fifth patch.

---

<!-- Append below. Do not edit or reorder existing entries. -->

- 2026-08-02 — claude-remote-session-webhook initialized from ai-project-template.
- 2026-08-04 — TestShutdownIsNotDelayedByOpenStreams failed ~1 run in 20: hold's select picks at random when a tick and the shutdown are both ready, so one last heartbeat may follow; it now forbids data after shutdown rather than bytes. (T029)
- 2026-08-04 — A session whose tmux session died out of band kept a live card on the fleet; a failed capture now asks confirmGone and drops the record only when the host affirms the session is gone. (#21)
- 2026-08-04 — Reduced motion removed the rain but left the two hover fades running; the media query now resets transitions universally. (#23)
- 2026-08-04 — Every daemon drove tmux's shared default server, so a second one adopted the first's sessions and reaped them on shutdown; each daemon now gets its own server, named from its listen address, and refuses the default. (#22)
- 2026-08-04 — A pre-v2 golangci-lint does not reject the v2 `.golangci.yml`: it runs zero linters and exits 0, so local lint reads green having checked nothing; quickstart now says so and gives the version check. The SessionStart banner for it is still owed — this runner cannot write under `.claude/`. (#26)
- 2026-08-04 — The claude-fix lane could only print a compare link, so its `Closes #N` survived merge solely by the goodwill of whoever clicked it; `.github/scripts/open-pr.sh` now opens the PR itself, and needs a workflow step wired by hand because an App token without `workflows` scope cannot land one. (#30)
- 2026-08-04 — The only link on a session card was its 32-character identifier, leaving the name — the part an operator reads and aims at — inert; the card heading is now the single link, and the identifier stays as text and describes it so nameless cards are still told apart. (#16)
- 2026-08-06 — A session appearing or vanishing reloaded the whole fleet page, losing a half-typed create form, the scroll position and the focus; the page now composes both of its shapes and the live half reveals the one that applies, announcing the change and keeping the reload as the fallback. (#51)
- 2026-08-06 — A session page opened from a card had no route back to the fleet, leaving the browser's back button; the header wordmark is now a link to `/` on every page, and the card's exactly-one-anchor rule was already asserted per card so it is untouched. (#48)
- 2026-08-13 — The shipped systemd unit assigned eight CRSW_ variables inline and precedence is `environment > file > default`, so each one silently beat the same key in `~/.config/crswd/config`; every value equalled the daemon's own default, which is what hid it. `allowed_roots` is the case that mattered — install.sh writes it and calls it the only bound on what a session can reach, and the unit overrode it. The values are now documented behind a `#`, parsed and held to the constants they claim, with TestUnitNeverShadowsTheConfigFile rejecting any inline assignment. (#137)
- 2026-08-14 — Adoption rebuilt a session's record from what tmux knew and tmux did not know a lifetime, so a session created never to expire came back from every restart carrying the daemon's default and was destroyed on the next sweep it was old enough for; four were lost this way at 01:52:03 UTC, sixty minutes after a redeploy. The lifetime is now written to `@crswd-lifetime` beside the five options adoption already restores, and re-checked against the ceiling in force at startup rather than the one in force when it was written. Shipped as part of #009 rather than as a one-line fix, because the same milestone withdrew the idle bound that actually did the reaping. (#009)
