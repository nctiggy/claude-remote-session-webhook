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
- 2026-08-14 — #138 made a unit updatable by moving the operator's hardening into a drop-in, and shipped it for fresh installs only: a host that had already hand-edited its unit had no route to that arrangement, so it was offered a `crswd.service.new` by every release and could never take one. `crswd unit check` and `crswd unit adopt` are that route — the relaxation is read out of the operator's own unit rather than written from a constant, and anything it cannot reproduce is a refusal that writes nothing. Found while building it: systemd has no trailing comments, so the deployed unit's `ProtectSystem=false      # relaxed on this host` is an unparseable value systemd ignores; a reader taking it at face value would have carried nothing into the drop-in and silently mounted /usr read-only inside every session. (#010)
- 2026-08-14 — `crswd unit adopt` shipped refusing on two things it could have done itself — "put that value in your configuration file, then try again", and a missing binary at the path the release's unit names — so the one host it was written for still could not run it. Both are now planned work rather than homework: a setting the file has no opinion about moves into it, and the binary the unit names is the one already running, so it is copied there. A file that *disagrees* is still a refusal, because which value the operator meant is not the daemon's to guess. Separately, the settings page named the waiting unit and the diff and stopped — the operator read that page, saw no way forward, and asked why the binary had not fixed it; the panel now names `crswd unit check`. (#011)
- 2026-09-02 — The `rc` start command every example in this repo recommends drives a session into remote control with the `/rc` slash command, and Claude Code 2.1.259 gave `/rc` a menu — Disconnect / Show QR code / Continue, "Enter to select · Esc to continue" — drawn over the input box and waiting for a keystroke nobody is there to press. The pane parked, the create reported success, the card said `running`, and `POST /prompt` returned `delivered: true` having typed into a menu: every observable the daemon has said the session was healthy. `--remote-control {name}` is the same registration without the menu, and also frees the positional prompt argument `/rc` was occupying and carries the operator's name through, which `/rc` was not — the session had been arriving in claude.ai under an auto-generated one. No default moved; `rc` is only ever a lookup key here, so this is the examples and the `InsertStartFlags` comment that cited the broken line as the deployed shape. That comment matters more now: the new shape ends in a flag and its value, so an insertion one token earlier would land between `--remote-control` and the name and silently rename the session, which TestInjectedFlagsNeverSplitRemoteControlFromItsName now holds. (#150)
