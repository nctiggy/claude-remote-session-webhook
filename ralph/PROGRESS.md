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

## Iteration 0 — updates that carry the files, not just the binary

**Did:** Archived milestone 14.

**The operator:** *"How do we make it so that the updates also grab or update the
systemd unit file as well? I feel like the config and systemd files should update
as part of the updates… values saved as part of the updates but the files
updated."*

**Findings:**

- **Half of it exists and nothing calls it.** `crswd config migrate`
  (`internal/config/migrate.go`) already rewrites a config into the current schema
  **line by line**, copying every line it has no reason to touch byte for byte,
  spacing and line endings included — because "a migration that reproduced the
  settings and dropped the commentary would take away more than it fixed". It is a
  manual command. The updater never runs it.
- **The unit needs a different answer, and the operator proved why in the same
  session.** They hand-edited their unit to relax three hardening settings so
  `sudo` works. An update that overwrote units would silently revert that on every
  release. **The existing rule — never overwrite a unit this installer did not
  write — is what protects them.**
- **But the current behaviour is silence.** Their unit has no recorded hash, so it
  will never be touched again and nothing ever says so. It still carries
  `ExecStart=%h/bin/crswd`, the path v0.80 fixed, and no `EnvironmentFile` line at
  all. **They are two fixes behind and have no way to find out.**
- **So the shape is `.pacnew`, not overwrite**: keep refusing to replace an edited
  unit, and stop being quiet about it — write the new one alongside and say so,
  with a way to see the difference.

**The test that matters:** an operator who relaxed `NoNewPrivileges` must still
have it relaxed after an update, and must be told a newer unit exists.
