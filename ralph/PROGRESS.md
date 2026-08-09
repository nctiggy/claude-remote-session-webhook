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

## Iteration 0 — two operator requests

**Did:** Archived milestone 8, opened a fresh notebook.

**Left:** six tasks, from two requests: *"All true/false settings should be check
boxes"* and *"can we have a way to restart the daemon from within the UI?"*

**Findings, all verified before the plan was written:**

- **There are exactly two boolean keys**: `discover_roots` and
  `destroy_on_shutdown`, the two callers of `loadBool`. Neither is a secret, so both
  are already `Editable`.
- **Neither feature needs a new component or a new class.** The switch
  (`.switch-input`, `.switch-label`) exists and is documented; the restart reuses
  `.updating` and `.spinner`. That keeps both class sweeps and the components-doc
  guard out of the risk surface entirely.
- **The trap in the checkbox work is not CSS, it is HTTP.** An unchecked checkbox
  submits **nothing at all**. The handler currently reads
  `r.PostForm.Get(fieldSettingValue)`, so an unchecked box is indistinguishable from
  a cleared field. The fix is not the hidden-input trick — with both fields sharing a
  name, `.Get` returns the first, which is the wrong one. It is for the handler to
  know the key is boolean and read an absent value as `false`, and **only** for keys
  it knows are boolean. A truncated request must never clear a setting that is not
  one.
- **The restart needs almost no new machinery.** `ExitForRestart()` and `exitGrace`
  already exist for the update, and the reason for the goroutine and the grace period
  is written above them: exiting before the response flushes is what turned an
  earlier update into a Cloudflare 502.
- **Restart is strictly less dangerous than update**, which already goes through the
  browser door. Update installs code from the internet; restart runs the binary that
  is already installed. The argument the operator won on #66 covers this a fortiori.
