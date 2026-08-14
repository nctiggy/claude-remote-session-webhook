# Research: Session Lifetime Honesty

Phase 0. Seven decisions, each with what was rejected and why.

---

## D1 — Where the per-session lifetime is kept so it survives adoption

**Decision**: a sixth tmux user option, `@crswd-lifetime`, written in
`Manager.start` beside the five already there and read back in `List`.

**Why**: the mechanism exists and is the one adoption already trusts. `#72` put
the name and working directory on the session for exactly this reason, and `#58`
added the start-command name. The daemon's own comment on `Adopt` explains that
`CreatedAt` comes from tmux's `#{session_created}` so a restart cannot extend a
session — the lifetime belongs in the same place, for the same reason.

**Encoding**: the vocabulary the configuration already uses.

| Record value | Option value | Meaning |
|---|---|---|
| `Lifetime == 0` | `""` | unset — the daemon's configured default applies |
| `Lifetime < 0` | `never` | the absolute deadline is switched off |
| `Lifetime > 0` | `72h0m0s` (`time.Duration.String()`) | that lifetime |

`never` is `config.NeverLifetime`, the same word the HTTP door takes
(`parseLifetimeOverrides`) and the same word the configuration file takes. Three
spellings of one idea was the defect `neverLifetimeDuration` was introduced to
avoid; this reuses it rather than adding a fourth.

**Separator safety**: `List` joins fields with `|` and splits from the right, so a
field that could contain `|` or a newline must be encoded. A duration string is
`[0-9hmsµn.]` and `never` is five letters — neither can. So it goes on raw, like
`@crswd-name` and `@crswd-start`, and unlike `@crswd-workdir`, which is base64
precisely because a path can contain anything.

**Rejected — a state file next to the config.** It would be a second source of
truth for a fact the host already holds, and it would go stale exactly when it
matters: a session killed while the daemon is down would leave a record behind.
The tmux server is the thing whose life the session shares.

**Rejected — encoding the lifetime into the tmux session name.** The session name
is `crswd-<id>` and is derived from the ID alone, which is `FR-034`: there is no
path from a caller-supplied string to a tmux target. Putting data in the name
would reopen that. The operator suggested this; user options are the same idea
done where the daemon already does it.

---

## D2 — What happens when a restored lifetime exceeds the current ceiling

**Decision**: the restored value is re-checked against the daemon's *current*
ceiling. If it would be refused on a create today, the session is adopted with the
daemon's default instead, and the substitution is named in the adoption record.

**Why**: the ceiling is the operator saying how long a session on this host may
live, and it is read at startup from the configuration in force *now*. A session
carrying `never` from a time when the ceiling was unbounded must not keep it after
the operator has narrowed the ceiling — that would be a caller (their past self,
via a value on the host) exempting itself from a bound the operator currently
sets. `resolveLifetimes` already refuses this shape on a create; adoption uses the
same rule rather than a second one.

**Not skipping the adoption.** A session whose lifetime cannot be honoured is
still a live unsandboxed shell. `FR-010` and the existing `Adopt` comment are
emphatic: an unadopted session is an unowned one, which is the trade Principle VI
never makes. Adopt it under the current rules and say so.

---

## D3 — How far the idle removal reaches

**Decision**: the concept is deleted, not disabled. Removed entirely:

| Where | What goes |
|---|---|
| `internal/session/session.go` | `IdleTimeout` const, `Session.Idle`, `Session.TmuxActivity`, `IdleDeadline`, `IdleSince`, `IdleDisabled`, `DisplayIdle` |
| `internal/session/reaper.go` | `ExpiryIdle`, `reasonPastIdle`, the idle arm of `expiredAt` |
| `internal/session/manager.go` | `syncActivity`, the idle half of `SetLifetimes` and `resolveLifetimes` |
| `internal/tmuxctl` | `SessionInfo.Activity`, `#{session_activity}` from the `List` format |
| `internal/config` | `EnvIdleTimeout`, `EnvIdleTimeoutMax`, their `Config` fields, their file keys |
| `internal/httpapi` | `fieldIdleTimeout`, the idle half of `parseLifetimeOverrides`, the idle rows on the settings page, idle deadlines in views and API responses |
| `web/` | the "Never die when idle" switch and the hint that explains it |
| docs, `README.md`, `.env.example`, `config.example`, `deploy/crswd.example.service` | every mention |

**`Expiry` is kept** with one member. Collapsing it would churn `reapRecord`, the
audit vocabulary and every reaper test to delete a type that still answers a real
question — *which* bound, of the ones that exist, was passed. Principle IV.

**`#{session_activity}` goes with it.** It exists only to feed `IdleSince`. A
field read from the host, carried on the record and used by nothing would be dead
weight that a future reader would reasonably assume something depends on.

**Rejected — keeping the reading as display-only.** Offered to the operator and
declined. A last-output timestamp on a card invites the reading that something
acts on it, which after this change nothing does.

---

## D4 — What a configuration carrying a retired idle key does

**Decision**: `SchemaVersion` 1 → 2. The retired keys are named in a
`retiredKeys` set, and a file carrying one is refused at startup with a message
pointing at `crswd config migrate`, which drops them and keeps a backup.

**Why**: the daemon already refuses any key that maps to no setting
(`internal/config/file.go:19` — "a key that maps to no variable is an unknown key
rather than a silent no-op"), so *some* refusal is the existing behaviour and the
question is only whether the operator is told what to do about it. `renamedKeys`
already exists for the neighbouring case and is documented as existing before it
was needed; retirement is the same problem with no forwarding address.

`config migrate` is the repo's own answer to a schema change, keeps a backup, and
is covered by `TestMigrateKeepsBackup`.

**Confirmed against the deployed host**: `~/.config/crswd/config` carries no idle
key today, so the running deployment migrates cleanly. The unit file's
`Environment=CRSW_IDLE_TIMEOUT=` lines become assignments to a variable nothing
reads — harmless, and removed from `deploy/crswd.example.service` in the same
change.

---

## D5 — How the command-line preview is built

**Decision**: server-rendered from the view, updated by `crswd.js` as the form
changes. One new CSS block, `.command-preview`.

**Why server-first**: the form works with no script running (research R4 of
milestone 3, and the reason it is a plain form post rather than htmx). A preview
that lived only in JavaScript would be missing exactly when the operator has
scripting off, and the thing it describes — what is about to run unsandboxed on
their host — is the last thing to degrade to nothing.

So the view carries the resolved command line for each reachable combination, and
the script picks between them. It does not assemble one from parts: string
assembly in the browser is a second implementation of `RenderStartCommand`, free
to disagree with the one that runs.

**On disclosing the command lines.** Milestone 4 deliberately removed command
*names* from this form — an operator choosing "rc" from a `<select>` was choosing
a command by name, which `FR-026` said not to do. That was about the control, not
about secrecy: the same authenticated operator already reads the whole configured
set on the settings page (`internal/httpapi/settings.go:540`,
`startCommandSet`), and `start_commands` is not a secret (`config.IsSecret`). The
preview therefore discloses nothing to this caller that the interface does not
already show them. The switch stays a mode, not a name.

**Rendering**: as text, in a `<pre>`, escaped by `html/template` like every other
operator value. The session name appears in it, so `FR-017` is not theoretical —
this is the same rule that renders pane output as text.

---

## D6 — Making a conversation identifier safe to put on a command line

**This is the security decision of the feature.**

The start command is delivered by `SendKeys` — it is *typed into a live shell*.
Every other caller-supplied value in this daemon either never reaches that line
(`{name}` is `ValidateName`'d first) or is delivered by `Paste`, which writes to a
tmux buffer over stdin precisely so a payload never becomes part of a command
line. A resume identifier has to be on the line, because it is a flag argument.

**Decision**: the identifier is validated to a strict UUID — 8-4-4-4-12 lowercase
hexadecimal — at the HTTP boundary and again before it is rendered into the
command, and refused otherwise. Nothing else is accepted: not a path, not a
prefix, not a shortened form.

That alphabet contains no shell metacharacter, no whitespace, no quote, and no
newline, so a value that passes cannot change the shape of the line it lands on.
The check is a fixed-length pattern rather than an escape or a quote, because
escaping is a thing to get wrong and a 36-character hex-and-hyphen alphabet is not.

**Rejected — quoting the identifier.** Correct quoting of a value on a line typed
into an unknown shell is exactly the class of problem this package's doc comment
says it does not attempt. Refusing everything that is not a UUID is smaller and
checkable.

**Rejected — passing the identifier through `Paste`.** `Paste` delivers a payload
as text to be submitted; it cannot contribute an argument to a command line the
daemon is composing. Wrong tool.

**Where the flags go on the line**: immediately after the first whitespace-
separated token, which is the binary. `claude --dangerously-skip-permissions "/rc
crswd"` becomes `claude --continue --dangerously-skip-permissions "/rc crswd"`.
Appending at the end was rejected: the configured commands end in a quoted prompt
argument, and whether a flag after a positional is honoured is the argument
parser's business, not something this daemon should assume. Insertion after the
binary is a rule that can be stated and tested.

The insertion lives in `internal/config` beside `RenderStartCommand`, so
command-line construction stays in one package.

---

## D7 — Finding the prior conversations for a working directory

**Decision**: list `$HOME/.claude/projects/<encoded-workdir>/*.jsonl`. Each file's
base name is a conversation identifier; its modification time is its recency.

**Verified on the host**: the directory exists and holds one `.jsonl` per
conversation, named by UUID, alongside per-conversation subdirectories that are
ignored.

**The encoding** is the working directory with `/` and `.` replaced by `-`
(`/home/nctiggy/code/customer-opportunities` →
`-home-nctiggy-code-customer-opportunities`, confirmed on the host).

It is **lossy and one-directional**: a directory literally named `a-b` and the
path `a/b` encode identically — the host has
`-home-nctiggy-code-customer-opportunities-heartflow`, which could be either. That
costs nothing here because the daemon only ever goes workdir → directory name, and
never interprets a directory name as a path. A collision would at worst offer a
neighbouring directory's conversations to an operator who can already create a
session in both.

**What is read**: the directory entry only — name, mode, modification time.
**No `.jsonl` file is ever opened.** `FR-025` limits what may be shown to enough
to choose between conversations, and the largest file on this host is 115 MB;
opening one to find a title would be both a content disclosure and a performance
trap.

**What is shown**: a shortened identifier and how long ago it was last written.

**Failure is not an error** (`FR-021b`): no `$HOME`, no such directory, an
unreadable directory, or a name that is not a UUID all yield "no prior
conversations". The create form still renders and a create still works. A daemon
that refused to show its create form because Claude changed where it keeps its
history would be a daemon broken by someone else's release.

**Bounding the disclosure** (`FR-021a`): the working directory is run through
`ResolveWorkDir` — the same allowlist check a create passes — *before* it is
encoded into a path. So the set of directories whose conversations can be listed
is exactly the set the operator may start a session in.
