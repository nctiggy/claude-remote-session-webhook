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

## Iteration 0 — say what is true, then say it clearly

**Did:** Archived milestone 13.

**Left:** the tasks below, ranked by a Fable 5 audit that read every markdown file,
`config.example`, `.env.example`, and the six test files that pin docs to code.

**The audit disagreed with the premise, usefully.** The operator asked for shorter,
clearer docs. `config.example` is genuinely hard to read — but the cause is
**ordering, not length**: nearly every block leads with the justification and
buries the operative fact. And the worst problems in the doc set are not verbosity
at all. **Wrong beats wordy.**

**Three files state things that are no longer true:**

- `deploy/README.md:14` says the daemon "refuses to start without" the three
  `CRSW_ACCESS_*` values, and line 38-41 of the same file says setting none is a
  supported deployment. Both cannot be right; the second one is.
- `AGENTS.md:22` lists `htmx` (there is none — `docs/components.md` says so
  emphatically) and a `skill/` directory that does not exist. Line 10 describes an
  Access-only browser door, which milestone 12 superseded. Line 50 says CI runs
  the untagged commands "and nothing else", false since the tmux and quickstart
  suites were added.
- `CONTRIBUTING.md:22-27` carries the same stale CI claim.

**`AGENTS.md` is the first file every agent loads.** In a Ralph-loop project a
stale one is compounding error, not a cosmetic issue — every iteration of every
milestone starts by reading it.

**The README cannot get a stranger through a Cloudflare install.** No DNS routing,
no Access application steps, no Google IdP setup, no AUD location, no service
token, and it never says "now browse to your hostname". It also never says to
install and authenticate `claude` first — and since the device-code relay is not
built, a session that hits a login prompt is simply stuck.

**On mkdocs the audit says no, firmly, and the best reason is one I had not
considered: these docs are test fixtures.** `config.example`, `.env.example`, the
README's table, the design tokens and the component class names are read at
relative paths and held to the code in both directions. Move them and the guards
break; copy them and you have created the one unguarded copy — which is the drift
this repository's whole discipline exists to kill.

**A landmine for T003:** any comment line in `config.example` beginning
`# <known_key> = …` counts as that key's line. Illustrative prose like
`# idle_timeout = 0 disables nothing` fails the suite as a duplicate key.
