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

## Iteration 0 — make it installable by a stranger

**Did:** Archived milestone 10, opened a fresh notebook.

**Left:** six tasks. The operator: *"the readme needs to be clear and crisp and be on
theme. Someone else should be able to easily install this on their own machine. it
should also try to automate as much as possible so the user can just run the
curl/bash command."*

**Findings:**

- **The installer leaves both required settings commented out.** It writes
  `# shared_secret =` and `# allowed_roots =` and then tells the operator to fill
  them in. The secret is a `openssl rand -hex 32` the installer could simply do,
  and the roots have a default the config file already names.
- **That is why the installer does not start the daemon**, and the reasoning is
  sound as far as it goes: a service that fails on first boot teaches an operator
  to ignore a failing service. But the premise is the incomplete config, and
  completing it changes what that rule is protecting against.
- **A generated secret must never be printed.** The installer's output can be in a
  terminal scrollback, a CI log, or a pipe from curl. Write it into the 0600 file
  and say *that* it was generated, never what it is. Same discipline as
  `crswd keygen`, for the same reason.
- **Never overwrite an existing config**, generated secret or not. That rule already
  exists and this must not weaken it.
- **The README is 538 lines and mixes two audiences.** "Working in this repo",
  "Planning a milestone" and "Running a loop" are ~46 lines of contributor
  workflow sitting between the install instructions and the configuration
  reference. A stranger installing this reads past the Ralph loop to reach the
  config table.
- **None of this can be proven here.** This box has the project installed, a config
  written, `~/.local/bin` on PATH and the unit in place — every precondition the
  installer exists to create is already true. `verify-install` on a GitHub-hosted
  runner with a fresh `HOME` is the only thing that can fail. **Any task changing
  the installer must extend that job.**

---

## Iteration 1 — T001, and an interruption worth recording

**Did:** T001. `install.sh` generates `shared_secret` with `openssl rand -hex 32` when
it writes a **new** configuration, writes it into the `0600` file, and never prints it.
`TestInstallGeneratesASharedSecret` holds three properties: the secret is at least
`config.MinSecretBytes`, it does **not** appear in stdout or stderr, and two installs do
not produce the same one — the last catching a constant masquerading as a secret.

**Interrupted, and the note is for the next reader rather than the loop.** The
operator asked for install and Cloudflare documentation mid-iteration, so this
iteration was killed to add it. The kill raced the iteration's own final write, and a
half-written `install.sh` briefly carried `say "... generated shared_secret $secret"` —
which was read as a leak and was not one. The iteration corrected it before exiting,
and the guard above already covered it.

**The lesson is about reading, not about the code:** a working tree during an
interrupted write is not a state to draw conclusions from. Verify against the file, and
against the test, before calling something a defect.

**Left:** T002 through T006.

---

## Iteration 2 — T001 finished, and the record above corrected

**Did:** Finished T001 and replaced the test's own configuration parser with the
daemon's. `installedSecret` now calls `config.ParseFile` + `File.Lookup` rather than
cutting the line on `=` in the test file, so what is asserted is that **the daemon
reads the file the installer wrote** — a fixture parser can agree with the installer
about a file the daemon refuses, and the operator meets that as a service that will
not come up. `install.sh` and the `verify-install` additions are unchanged from
`6b727f2`.

**The entry above describes this iteration's working tree, and the account in it is
wrong.** Nothing was killed and no write was interrupted. The `say "... generated
shared_secret $secret"` line it saw was **a deliberate break check**, made and reverted
inside a minute, because this plan requires that *a new guard must be proven by
breaking it*. Four were run against `install.sh` in turn, each reverted:

| Break | What must fail |
|---|---|
| `say` the value | `never printed` |
| `secret=deadbeef` | the length check, before anything is written |
| a constant 64 characters long | `a different one on every host` |
| back to `# shared_secret =` | every case, via "sets no shared_secret" |

**What actually happened is worth more than the correction.** Two agents were working
this one checkout at the same time. `6b727f2` is this iteration's uncommitted work,
committed by the other one at the moment it happened to look at the tree — its message
describes tests it did not write, and the `install_test.go` in it is this iteration's
file three edits before it was finished. `ralph/loop.sh` refuses to *start* on a dirty
tree (line 31), which is the whole of its protection: nothing stops a second iteration
starting on a clean tree and then committing a first one's half-finished edits, and
both will pick the same topmost open task because that is what the prompt tells them
to do.

**Findings:**

- **One checkout, one loop.** A second concurrent iteration is not a slow loop, it is
  a corrupt notebook: commit messages describing work their author did not do, and a
  `PROGRESS.md` entry inferring a defect from a transient state. If two are wanted,
  they need separate worktrees.
- **A transient working tree is not evidence.** Any iteration following this plan will
  put a deliberate leak into `install.sh` for a few seconds, because the plan tells it
  to. Reading one and reporting a leak is the false positive that costs the most trust.
- **`README.md:33` is now stale** — `$EDITOR ~/.config/crswd/config  # shared_secret,
  allowed_roots` tells a stranger to set a secret the installer generated. **T004** owns
  the rewrite, so it is left there rather than half-fixed twice.
- **`specs/006-…/contracts/installer.md:86` shows the old transcript** in its worked
  example (`Next: set shared_secret and allowed_roots`). It is a record of what
  milestone 6 specced and this plan is the authority now, so it was left alone — but
  the next reader of that contract should know it describes an installer that no longer
  exists.
- **CI never shellchecks `install.sh`.** `ci.yml` lists `.claude/hooks/*.sh`,
  `ralph/loop.sh`, `.claude/statusline.sh` and `.github/scripts/*.sh` — not the one
  script strangers pipe into `bash`. The `format-and-lint` hook covers it at
  `-S error` for whoever edits it, and nothing covers it on a pull request.
