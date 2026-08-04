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
