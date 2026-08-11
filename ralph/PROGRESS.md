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
