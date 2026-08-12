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

## Iteration 0 — the config an install actually leaves behind

**Did:** Archived milestone 12.

**Left:** the tasks below. The operator: *"The config that is written during
install is not the complete example. It is very basic and missing a lot of
options."*

**Findings:**

- **The installer writes two settings. The daemon understands twenty-three.**
  An operator installing today gets `shared_secret` and `allowed_roots` and no
  sign that `listen`, `dashboard_password`, `max_sessions`, the lifetimes, the
  start commands or anything else exists.
- **`.env.example` is held to the code in both directions** by
  `TestEnvExampleNamesEveryVariable` — a variable the code reads that it never
  names fails, and a variable it names that nothing reads fails too. **The
  installer's config template is guarded by nothing**, which is exactly why it
  drifted to two of twenty-three while `.env.example` stayed complete.
- **The unit is already shipped as a release asset**, fetched, checksummed and
  verified by the installer alongside the tarball. A config template follows that
  path exactly rather than inventing a second one, and the same signature covers
  it.
- **The shape to copy is `.env.example`'s, not its content.** That file is
  `NAME=value` for an environment; the config file is `key = value`. Same
  discipline, different spelling — and the two must not drift from each other
  either.

---

## Iteration 1 — 2026-08-12

**Did:** Nothing to the product. Took T001, found that the file it asks for
already exists under another name, and blocked the milestone rather than
committing a second copy of four hundred lines of configuration prose.

**Learned:**

- **`config.example` at the repository root is already the file T001
  describes.** Every clause of T001 is true of it today: the complete
  configuration in `key = value`, all twenty-three keys, each commented out,
  each with the explanation an operator needs, in `Vars()` order. It was written
  by milestone 4's T034 for exactly this purpose.
- **It is already guarded, and by more than T002 asks for.**
  `TestConfigExampleParsesAndCoversEveryKey` (`internal/config/file_test.go`)
  parses it with the daemon's own parser, fails when a key in `config.go` is
  missing from it, fails on a duplicated key, fails on a live assignment, holds
  it to declaration order, and — the part no hand-written guard would have
  thought of — uncomments each line individually and asserts it sets the value
  it appears to set.
- **The one clause of T001 that is *not* true of it: "values are never
  present."** `config.example` deliberately carries an illustrative value on
  every commented line (`# listen = 127.0.0.1:8765`, `# allowed_roots =
  /home/you/code:/home/you/work`), and its header states that as a decision.
  That is the entire distance between the two files, and T002's no-value
  assertion is what makes the distance load-bearing rather than cosmetic.
- **The plan's author did not know `config.example` was there.** T001 says to
  take the explanations "from `.env.example` and the README" — not from the file
  that already holds them in this exact format. Iteration 0's findings never
  name it either.
- **This was seen once before and written down.** `ralph/archive/progress-milestone-6.md:677`:
  *"The release publishes no `config.example` … the installer's heredoc is a
  second copy of configuration prose and it will drift from `config.example`."*
  That is this milestone's diagnosis, recorded two milestones early, and it
  names `config.example` as the thing being drifted *from*.
- **`.gitleaks.toml` already allowlists `^deploy/.*\.example\..*$`**, so a file
  at `deploy/crswd.example.config` is exempt from secret scanning the moment it
  is created, with no guard and no review step. That is a real argument for
  T002's no-value assertion and for the `deploy/` path — and equally a reason
  not to put a file there before the guard exists.
- The release ships `crswd.service`, `cloudflared.example.yml` and `crswd-api`
  (`.github/workflows/release.yml:99-104`, listed again at `:238-244`), and the
  installer fetches and `verify_checksum`s the unit only. The template follows
  that path whichever file it turns out to be; **T003 through T006 are unaffected
  by the answer except for the path they name.**

**NEEDS CLARIFICATION — the milestone cannot start until this is answered:**

> **Is the shipped, installed template `config.example`, or a new
> `deploy/crswd.example.config` beside it?**

Both readings are defensible and they produce different milestones:

- **(a) Ship `config.example`.** One annotated configuration in this repository,
  which is what "one source and not two that can disagree" (T005) and
  "prefer editing over creating" (`AGENTS.md`) both point at. T001 becomes
  approximately a no-op, T002 becomes "extend the guard that already exists",
  and T003 copies `config.example` into `dist/` beside the unit. The cost:
  either the installed file carries `/home/you/code` and `you@example.com` as
  placeholder values on a host where they are wrong, or `config.example`'s
  illustrative values are deleted — which is a change to a deliberate, tested
  design and is arguably "changing a default", named out of scope.
- **(b) Create `deploy/crswd.example.config` as written.** The installed file is
  *this host's* configuration rather than a repository document, so a
  placeholder value in it is a line an operator can uncomment into a daemon that
  refuses to boot — `allowed_roots = /home/you/code` is the sharp case. Keys and
  prose only is the right shape for a file a machine places. The cost: a third
  annotated list of the same twenty-three settings, after `.env.example` and
  `config.example`, whose prose can drift even though its *keys* cannot.

**Recommendation, if the operator wants one: (b), with one amendment** — T001
should say it is a *third* file and say why, and it should take its explanations
from `config.example` rather than from `.env.example`, since that is where they
already exist in this format. The repo already runs two parallel guarded
examples on purpose; the reason for a third is that a placeholder value is
harmless in a file you read and harmful in a file the installer wrote for your
host. If the operator disagrees, (a) is a smaller milestone and the plan should
be rewritten around it before T002 is attempted.

**Left:** T001 blocked. T002–T006 wait on the same answer — each names
`deploy/crswd.example.config` explicitly, and under (a) three of them change.

**Findings:**

- **`.env.example:22` already points an operator at `config.example` for the
  per-setting prose**, and `install.sh:373` points the installed file at it by
  URL. So the file is load-bearing documentation that two other files delegate
  to. Whichever way the question above is answered, those two references need
  checking afterwards — under (b) there would be three files and only one of
  them is linked.
- **`config.example` is not in `.gitleaks.toml`'s allowlist.** `.env.example` is
  (`\.env\.example$`) and everything under `deploy/` matching `*.example.*` is,
  but the root `config.example` matches neither pattern. It has never tripped
  the scanner because it genuinely holds no secret, so this is latent rather
  than broken — but it means the file the whole repo delegates its configuration
  prose to is the one example file being scanned on its merits. Not fixed here:
  it is not this task, and under reading (a) the right fix is a pattern for it,
  while under (b) it is arguably correct as it stands.
