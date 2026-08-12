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

---

## Iteration 1 — 2026-08-12

**Did:** T001. `internal/updater/config.go` — a `ConfigMigrator` that rewrites the
operator's configuration into the current schema during an update: stage beside
their file, read the staged bytes back off disk, run them through `config.Validate`
(the same loader a startup uses), then back up the original to `config.bak` and
rename into place. `updateFromBrowser` calls it after a successful `Swap`.

**Learned — things the next iteration would otherwise rediscover:**

- **`internal/config` writes after all.** `migrate.go`'s header says "cmd/crswd is
  the only code in this repository that writes a config file"; that has been stale
  since the settings page shipped — `internal/config/write.go` has `WriteFile`,
  `Validate`, `BackupPath`, and `internal/httpapi/settings_edit.go` is a second
  writer. **`settings_edit.go` is the template to copy** for anything that writes
  the operator's file: validate → back up → write, all through `config.*`. I reused
  `config.WriteFile` for both the staged file and the backup rather than adding a
  third copy of `writeAndSync` — `cmd/crswd/config_cmd.go` has its own.
- **`config.Validate` needs a real environment.** It runs the whole loader, so a
  test fixture needs `CRSW_SHARED_SECRET` (64 chars is safe) *and* a resolvable
  `allowed_roots` — and it layers env **over** the file, so a fixture that sets
  `CRSW_ALLOWED_ROOTS` in the environment cannot then test a bad root in the file.
- **The one value that parses and does not load** is `allowed_roots` naming a
  directory that is not there: `parseFile` only checks grammar, keys and schema, so
  it sails through the migration and is refused by the loader. That is the whole
  fixture for "a migration that would not validate".
- **Where the migration could NOT go.** Startup is the obvious home and it is
  closed: FR-008 and `specs/004-configure-and-operate/quickstart.md` both say the
  daemon never writes the file it reads, and `cmd/crswd/config_cmd_test.go` asserts
  it. An update is the exception because the operator asked for it by name.
- `selfUpdate` now has a fourth member and `wired()` counts it, so a dropped wiring
  refuses loudly rather than quietly stopping carrying the file.

**Left:** T002–T007. T002 is next (ship the unit as a comparable release asset).

**Findings — noticed, not fixed:**

- **⚠️ The migration runs in the OLD binary, so a new release's schema changes land
  one update late.** `config.SchemaVersion` and `renamedKeys` come from the code
  that is running, and that is v-current, not v-next. A rename shipped in v0.90 is
  applied by the update *after* the one that installs v0.90. Today this costs
  nothing (`renamedKeys` is empty and `SchemaVersion` is 1) and it is what the plan
  asked for. The fixes, if it ever matters: exec the staged candidate's own
  `crswd config migrate` (T007 territory), or migrate on the first start after an
  update — which needs an exception to FR-008 that nobody has written yet.
  **Do not "fix" this silently; it is a spec question.**
- **`cmd/crswd/config_cmd.go` still has its own `writeConfigFile`/`writeAndSync`,
  duplicating `internal/config/write.go`.** Left alone on purpose (AR-008) — that
  is exactly T007's job, and T007 should collapse three write paths, not two.
- **`internal/config/migrate.go`'s header comment is wrong.** It claims cmd/crswd
  is the only writer in the repository. Two other writers exist now. One-line doc
  fix for the fix lane; not touched here.
- **Flaky test:** `TestTheOpeningScreenIsSentWithoutWaitingOutAnInterval`
  (`internal/httpapi/stream_test.go`) failed once on a loaded machine — "the opening
  screen arrived 14ms after the open, which is past the 10ms interval" — and passed
  on every rerun. It asserts a wall-clock deadline of 10ms, which a parallel suite
  on a busy host will miss regardless of the code. CI will hit this eventually.
