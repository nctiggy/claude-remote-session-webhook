# Research: Session Revival

**Feature**: 012-session-revival | **Date**: 2026-08-22

Everything below was settled against the running host or the existing code, not
from memory. Where a claim was checked, the check is shown.

---

## D1 — The resume handle is a daemon-minted session id, not a display name

**Decision**: At create, the daemon generates a UUID and passes
`--session-id <uuid>`. To revive, it passes `--resume <uuid>`.

**Rationale**: `--name` is documented as *"Set a display name for this session"*.
It is not a resume handle. `--resume` is documented as *"Resume a conversation by
session ID, **or open interactive picker** with optional search term"* — so a
non-identifier value does not fail, it opens a picker. In a detached tmux pane
that is a wedged session that still looks alive, which is the worst available
failure mode for a supervisor that is trying to decide whether something is
healthy.

A daemon-minted UUID also settles the case that prompted this work: two sessions
started from the same working directory. Names collide and are silently
auto-renamed by the CLI; `--continue` resolves to "most recent" and is ambiguous
by construction. An identifier the daemon chose is neither.

**Verified** on this host:

```
$ claude -p --session-id 11111111-2222-4333-8444-555555555555 "Reply with exactly: BANANA-7"
BANANA-7
$ ls ~/.claude/projects/<slug>/
11111111-2222-4333-8444-555555555555.jsonl
$ claude -p --resume 11111111-2222-4333-8444-555555555555 "What word did you just say?"
BANANA-7
```

Resuming appends to the same transcript, so the handle is stable across any
number of revivals.

**Alternatives rejected**:
- `--name` + `--resume <name>` — the picker hazard above, plus silent renames.
- `--continue` — cannot distinguish two sessions in one directory, which is the
  stated requirement. *Spec 013 removed it from the daemon entirely, for the same
  reason enlarged: a value that names a conversation nobody can see is one nobody
  can choose.*

**Free consequence**: the identifier is already the shape
`session.ValidateResume` accepts (8-4-4-4-12 lowercase hex), so the validator
guarding the SendKeys command line needs no change, and the transcript path is
already computed by `session.projectDirFor`.

---

## D2 — The handle cannot live only on the tmux session

**Decision**: The conversation identifier is written **both** to a tmux user
option (`@crswd-conversation`, fast path, consistent with the five options
already there) **and** to a durable journal on disk. The journal is the
authority; the option is the cache.

**Rationale**: This is the correction that the incident forced. The session that
prompted the feature was not lost to Claude exiting, and not to a reboot:

```
Aug 22 08:16:10  Out of memory: Killed process 1395733 (2.1.233)
                 anon-rss:12646016kB
                 task_memcg=…/tmux-spawn-8ad83d7d-….scope
Aug 22 08:17:40  tmux-spawn-8ad83d7d-….scope: Failed with result 'oom-kill'
```

The kernel OOM killer takes a **cgroup**, not a process. Claude, its login shell
and its tmux session died together, on a host that had been up five days and
whose tmux server (pid 40473, started Aug 17 14:37:58) never restarted. Every
`@crswd-*` option on that session died with it.

A design that kept the handle only on the running shell would lose it in exactly
the failure this feature exists to recover from.

**Alternatives rejected**:
- tmux options only — loses the handle in the observed failure.
- A database — the operator ruled it out, and nothing here needs queries.

---

## D3 — Journal location and format

**Decision**: `~/.config/crswd/sessions-<listen-address>.jsonl`, append-only, one JSON object per
line, `O_APPEND` + `fsync` per record, replayed at startup.

**Rationale for the directory**: it is where the operator asked for it, and it is
where `config.DefaultPath` already resolves this daemon's file
(`$XDG_CONFIG_HOME/crswd/` falling back to `~/.config/crswd/`). The journal
follows the same resolution so that a host configured by `XDG_CONFIG_HOME` keeps
its state beside its configuration rather than in two places.

**Rationale for append-only**: the failure being defended against is an unclean
stop. On this very host, the unclean reset of 2026-08-17 lost the
`hasTrustDialogAccepted` flag out of a rewritten `~/.claude.json` and took a
gitignored `.env` with it. A rewrite-in-place state file is that failure. An
append-only log with a per-record fsync degrades to "the last record may be
missing", never to "the file is now truncated".

**Rationale for replay**: it is the same shape `Adopt` already has — reconcile
against what is on the host rather than assume. A later record for the same
session supersedes an earlier one; the last record wins.

**Alternatives rejected**:
- `$XDG_STATE_HOME` — more correct by the letter of the XDG spec, but splits the
  daemon's files across two roots for no operational gain, and the operator named
  `~/.config/crswd`.
- A single rewritten JSON document — the truncation failure above.

---

## D4 — Detecting both deaths costs one tmux call

**Decision**: Extend the existing `list-sessions` format string with
`#{pane_current_command}`. A session absent from the listing is a vanished
shell; a session present whose pane command is not the start command's binary is
a dead Claude.

**Verified** — `list-sessions` resolves pane formats against the session's active
pane, so no second call is needed:

```
$ tmux -L … list-sessions -F '#{session_name}|#{pane_current_command}|#{@crswd-managed}'
crswd-38dec2be…|claude|1
crswd-5e7f387d…|claude|1
crswd-7132239a…|claude|1
```

**Rationale**: `Adopt` already makes exactly one `List` call and takes everything
it needs from it (research D6 of spec 010). Keeping that true means the
supervisor's sweep is also one call regardless of fleet size.

**Alternatives rejected**:
- `remain-on-exit` + `#{pane_dead}` — the pane runs a login shell, not Claude, so
  the pane never dies when Claude does. It would only work if Claude were the
  pane's own process, which costs the inspectable shell (see D5).
- A `pane-died` tmux hook posting to the daemon — adds an inbound route to a
  daemon whose threat model is that any request that passes auth is unsandboxed
  execution. Polling adds no surface.
- Reading the pane — forbidden by FR-007 and by docs/security.md; pane content is
  the operator's work.

---

## D5 — The daemon does not attempt a clean-exit distinction

**Decision**: Destroy is the only signal that ends a session for good. A Claude
process that exited for any other reason is revived.

**Rationale**: `tmuxctl.Controller.New` starts a login shell and the start command
is typed into it, deliberately — *"so a Claude crash leaves an inspectable shell
instead of a vanished session"*. That choice means no exit status is ever
observable. Recovering one would mean running Claude as the pane's own process,
which trades away the inspectable shell that diagnosing a crash depends on.

Put to the operator on 2026-08-22 and decided: revive anything not destroyed. A
session ended from inside comes back once and is destroyed if that was meant.

**Alternatives rejected**: running Claude as the pane command (above); reading the
pane for a shell prompt (FR-007).

---

## D6 — A supervisor beside the reaper, not inside it

**Decision**: A new `Supervisor` in `internal/session`, borrowing `Reaper`'s
injectable `ticker` and `Clock`, started next to it from the same place in
`cmd/crswd`.

**Rationale**: the two sweep on the same cadence and share a shape, but they move
in opposite directions — the reaper *ends* sessions, the supervisor *starts*
them. A single type that both starts and stops unsandboxed shells is a worse
thing to review than two that each do one, and `Reaper`'s vocabulary (`Reaped`,
`Expiry`, "the thing that ends a session nobody came back for") does not stretch
to cover revival without being rewritten — which Principle IV rules out.

**Alternatives rejected**: folding into `Reaper.Sweep` — cheaper by one file,
more expensive in every future reading of it.

---

## D7 — Backoff and the bound

**Decision**: 3 attempts per death, spaced 5s → 30s → 3m, then `StateFailed`.

**Rationale**: the numbers are chosen so that the common case — Claude died once,
transiently — recovers inside one sweep, while a session that cannot start costs
at most three attempts before it goes quiet and visible. They are constants for
the same reason `SweepInterval` is: an operator who could widen them could widen
the blast radius the constitution bounds by construction.

The bound is not decoration. This host lost four hours on 2026-08-17 to
`claude-remote-seap.service` restarting **2,826 times** at five-second intervals
against `Workspace not trusted` — an error no retry could ever fix. The damage
was not the failure; it was that the failure was invisible. `StateFailed` on the
card is the part that matters most.

A session killed for using 12 GB is likely to be killed again, so recreation is
bounded by this same rule rather than being exempt from it.

---

## D8 — The form's conversation list follows the chosen directory

**Decision**: A read-only `GET` returning the conversations for one directory,
called by the existing form JS when the working directory changes.

**Rationale**: `session.Conversations(workDir)` already exists, already resolves
through `ResolveWorkDir`, already opens no transcript, and already returns an
empty list on every failure. The defect is only that
`httpapi/dashboard.go:407` calls it once at render for `suggestions[0]`, so the
list belongs to whichever directory the form happened to suggest.

The route discloses strictly less than the form already does today, and is bound
by the same allowlist, so it widens nothing.

**Alternatives rejected**: pre-rendering every suggested directory's
conversations into the page and switching client-side — adds no route, but covers
only the suggested directories and not a directory the operator types, which is
the case in the requirement.
