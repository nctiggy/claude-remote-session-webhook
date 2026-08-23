# Contract: the session journal

**File**: `$XDG_CONFIG_HOME/crswd/sessions-<listen-address>.jsonl`, else `~/.config/crswd/sessions-<listen-address>.jsonl`
**Resolution**: the same base directory `config.DefaultPath` resolves for the
configuration file, so configuration and state never live in two roots.

## Shape

One JSON object per line. UTF-8. No trailing content. Written `O_APPEND`,
`0600`, `fsync` after each record.

```json
{"v":1,"at":"2026-08-22T16:32:10Z","id":"38dec2be02c48f3fb2d3d63a01c263c3","event":"created","owner":"operator","conversation":"7f3a…","workdir":"/home/x/code/y","start":"rc","lifetime":"0s","created":"2026-08-22T16:32:10Z","attempts":0}
```

Fields are specified in [data-model.md](../data-model.md).

## Rules

1. **Append only.** No record is ever rewritten or removed in place. The file is
   compacted, if ever, by writing a new file and renaming it over this one — and
   nothing in this feature compacts.
2. **Last record wins.** Replay reads the file in order; a later record for an id
   supersedes an earlier one.
3. **A truncated final line is discarded, not fatal.** An unclean stop can leave a
   partial write; the daemon drops it and continues. This is the whole reason the
   format is a log.
4. **An unknown `v` is skipped.** A daemon reading a newer file starts the
   sessions it understands rather than refusing to start.
5. **A missing file is not an error.** It means no session has been created yet.
6. **An unreadable file is fatal at startup.** A daemon that silently continued
   would revive nothing and report nothing, which is the invisible failure this
   feature exists to remove.
7. **Never a secret.** No token, no token hash, no pane content, no conversation
   content, no caller free text. The file is `0600` and holds none of those
   anyway.

## Replay

At startup, before the listener binds, in this order:

1. Replay the journal into candidate records.
2. Run the existing `Adopt`, which reconciles with tmux and wins on any conflict —
   the host is the authority on what is *running*; the journal is the authority on
   what *should be*.
3. Re-check every replayed candidate against the allowlist, the cap and the
   deadline. A directory that left the allowlist, a session past its ceiling, or a
   fleet already at the cap drops the candidate and records why.

Replay never starts anything itself. It hands candidates to the supervisor, whose
ordinary sweep does the work under the same rules as any other revival.
