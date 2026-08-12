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

---

## Iteration 1 — 2026-08-12

**Did:** T001. Corrected `AGENTS.md` against the tree — the audit named four false
claims and checking the rest found two more.

**Learned:**

- **`AGENTS.md` has almost no room to grow. CI fails it at ≥150 lines** (`ci.yml`,
  "AGENTS.md stays skimmable"), and it sits at **147**. Any future edit must budget
  lines, not just words. Deleting the phantom `skill/` row is what paid for the two
  lines the corrected CI paragraph needed.
- **The audit's count was low; it was wrong in six places.** Beyond the four named:
  `internal/` was listed as five packages when there are ten (`access`, `audit`,
  `auth`, `buildinfo`, `config`, `httpapi`, `release`, `session`, `tmuxctl`,
  `updater`), and `cmd/crswd/` was "flag parsing and wiring only" when it carries
  three subcommands (`config check`, `config migrate`, `keygen`).
- **The `Why` paragraph also claimed the device-code relay works.** It does not —
  `internal/session/session.go:105`, `status-pill.html:14` and `crswd.js:498` all
  park it on "milestone 4's device-code relay", and no `httpapi` route mentions it.
  The `README.md` already names it and the companion skill as the two unbuilt
  things; `AGENTS.md` now does too, so T004 does not have to re-derive this.
- **The two tagged suites are broader than the table said.** `-tags tmux` also
  covers `internal/session/mode_test.go` (a session name round-tripping through a
  real tmux user option); `-tags dev` spans `internal/access`, `internal/httpapi`
  **and** `cmd/crswd`, not just the first.
- **CI runs Install, Lint, Typecheck, Test, Build, `-tags tmux`, `-tags quickstart`
  — and not Format or `-tags dev`.** `.golangci.yml` has a `formatters:` block with
  exclusions but enables no formatter, so nothing in CI checks `gofmt`. That is why
  the corrected sentence names the commands rather than saying "the untagged ones".
- **No test parses `AGENTS.md`.** Every Go reference to it is a comment citing a
  rule. It is the one context file that is *not* a fixture — unlike `config.example`,
  `.env.example` and the README table. Its only mechanical guard is the line count.
- **`golangci-lint` here is v2.12.2, matching the CI pin** — the #26 check passes,
  so a green local lint is a real one this iteration.

**Left:** T002–T008.

**Findings (not fixed):**

1. **`docs/auth-and-sessions.md` §"Two doors, one hostname" (line 48) is about the
   Access-vs-API split, and calls the API client "the skill" — which does not
   exist.** With milestone 12's password door, the phrase "two doors" now names two
   different pairs in two different files, which is exactly the kind of collision
   that makes a reader trust the wrong one. **Deliberately not touched**: that file
   is named out of scope in the plan and is a binding correctness spec, so changing
   it needs its own task, not a drive-by. Worth a task in the next milestone.
2. **`README.md:656` still says "Go templates + htmx, not an SPA."** Same falsehood
   T001 removed from `AGENTS.md`. It is inside T007's territory (the roadmap and
   duplication trim) — **T007 should fix it rather than leave the last htmx claim
   standing in the front-page file.**
3. **The quickstart suite cannot run on this host**: the deployed daemon holds
   `127.0.0.1:8765` (confirmed via `ss -ltn`), and two startup cases bind that exact
   port. `go vet -tags quickstart ./...` is the fallback and it passes. Any task
   whose gate is "run quickstart" will hit this — CI's self-hosted runners are where
   it actually executes.

---

## Iteration 2 — 2026-08-12

**Did:** T002. `deploy/README.md` now says which deployment it is for, states what
the daemon actually refuses on the `CRSW_ACCESS_*` group, and treats 1Password as an
example of a shape rather than the procedure; `CONTRIBUTING.md`'s CI claim is true.

**Learned:**

- **Iteration 1's finding #3 is wrong — retract it. `go test -tags quickstart ./...`
  runs green on this host with the deployed daemon still holding `127.0.0.1:8765`**
  (34s; `ss -ltn` confirms the port is held). The two startup cases stopped binding
  8765 when `freeAddrOn` landed (`quickstart_test.go:468`) — its comment describes
  exactly the symptom that finding reported, so the fix predates the finding. **Do
  not skip the quickstart gate on that premise.**
- **`deploy/README.md` is a fixture, twice over, and neither guard is obvious.**
  `internal/config/deployexample_test.go` splits it on ``` fences, takes every block
  containing `/.config/crswd/env`, harvests the `CRSW_*=` names from *all* of them
  into one environment, and asserts that environment starts a daemon. **A second
  env-file recipe is therefore not additive — it merges.** Adding a
  `CRSW_DASHBOARD_PASSWORD=` block beside the Access one would fail two ways:
  `validateDoors` refuses password-beside-Access, and `baseEnv` (`config_test.go:39`)
  has no sample value for that variable, which is a `t.Fatalf`. That is why T002's
  LAN note is a pointer to `README.md` and not a recipe. Separately,
  `quickstart_test.go:2114` sweeps this file's `journalctl` lines against a real
  stream.
- **The false claim had a second copy** below the recipe ("writing only the secret
  gets a daemon that refuses to start"). It does not: `loadBool` reads the unit's
  empty `Environment=CRSW_ACCESS_ENABLED=` as false, `validateAccessGroup` passes on
  zero of three, and the daemon starts with `warnNoIdentityProvider`'s banner. **When
  a plan names a false statement by line number, grep the file for the claim** — the
  audit found the loudest copy, not the only one.
- **`CRSW_ACCESS_ENABLED=true` is the fix for the gap that falsehood was papering
  over**, and the file had never mentioned it: it turns "none of the three" from a
  supported deployment into a refusal, which is what an Access deployment wants.
- **The `dev` tag and `gofmt` are the only things in `AGENTS.md`'s command table
  that run nowhere but locally** — `.golangci.yml` has a `formatters:` block that
  enables no formatter. That is now stated in `CONTRIBUTING.md` too.

**Left:** T003–T008.

**Findings (not fixed):**

1. **Findings 1 and 2 from iteration 1 still stand** — `docs/auth-and-sessions.md`'s
   colliding "two doors" and the API client called "the skill" (needs its own task,
   that file is out of scope here), and `README.md:656`'s surviving htmx claim
   (T007's territory).
2. **`deploy/README.md`'s "Verifying the exposure model" is Access-only and does not
   say so.** `ss -tlnp | grep crswd` "must show 127.0.0.1, never 0.0.0.0" is exactly
   backwards for the LAN deployment, where `listen = 0.0.0.0:8765` is the documented
   configuration. T002's header now tells a LAN reader which three sections apply,
   which bounds the damage, but the section itself still reads as universal. **Not
   fixed**: rewriting it is a security-doc change in a file T002 was scoped to
   correct, not extend — worth a task.
3. **Checked and *not* a finding, so nobody spends an iteration on it:**
   `.env.example` (lines 86–97) and `crswd.example.service` (lines 67–71) both
   already document the password door properly — what it is, that it is never a door
   as well as Access, that it belongs in the `EnvironmentFile`, and the clear-wire
   warning. The stale Access-only framing was `deploy/README.md`'s alone.

---

## Iteration 3 — 2026-08-12

**Did:** T003. `config.example` now leads with the fact on every key — name and
purpose, then format/bounds/what a wrong value does, then the why where it is
load-bearing, then the default, then the one commented line. 465 → 306 lines.

**Learned:**

- **The plan's ~215-line target was measured against a 401-line file, and the file
  is 465 today.** Milestone 13 added ~64 lines the plan itself protects: what
  `never` costs on the lifetime ceiling, and the two idle clocks. 465 → 306 is a
  34% cut where 401 → 215 was 46%. **Getting to 215 from here means deleting a
  named load-bearing passage, which the same plan forbids** ("The voice stays…
  the fix is *order*, not deleting the why"). Reordering and de-paragraphing is
  worth roughly a third; the rest of that target was never available. **The next
  file with a line target should be measured before it is trusted.**
- **Folding `Default: x.` into the end of the block's last sentence, instead of
  giving it its own stanza, is the single biggest structural saving** — two lines
  per key, 46 lines across 23 keys, and it reads better because the default lands
  next to the bounds it belongs to rather than as a footnote.
- **The duplicate-key landmine has a twin that bit nothing but nearly did.** The
  test cuts each comment line on its first `=` and matches the left side against
  the known keys, so `#   key = value` in the format illustration is safe (`key`
  is not a setting) — but a wrapped line is not. Writing `# … Default: claude`
  followed by `# --dangerously-skip-permissions, byte for byte …` is fine, yet the
  same wrap one word earlier would have started a line with a real key. **Keep the
  key name off the start of any wrapped line.**
- **Every documented value must round-trip, secrets included.** `IsSecret` only
  suppresses the value from the failure message — `file_test.go:1408` still
  compares. Keeping the illustration strings byte-for-byte (`paste the output of
  openssl rand -hex 32 here`, `rc=claude --dangerously-skip-permissions "/rc
  {name}"`) is what makes a rewrite of this file safe.
- **The gate is real and it all ran here:** `go build`, `go vet`, `go test ./...`,
  `golangci-lint run` (v2.12.2, so #26's check passes), and
  `go test -tags quickstart ./cmd/crswd` (36s, green — iteration 2's retraction
  holds).

**Left:** T004–T008.

**Findings (not fixed):**

1. **Iteration 1's findings 1 and 2 and iteration 2's finding 2 all still stand** —
   `docs/auth-and-sessions.md`'s colliding "two doors" and its "the skill",
   `README.md:656`'s htmx claim (T007's), and `deploy/README.md`'s Access-only
   "Verifying the exposure model".
2. **`.env.example` is 376 lines documenting the same 23 settings this file
   documents, in the same voice.** Neither points at the other, and nothing holds
   them to each other beyond `envexample_test.go` checking that the names are all
   present and carry no values — so the *prose* in the two files can drift apart
   silently, and a setting whose bounds change needs both edited. **Not fixed:**
   `.env.example` is outside T003's scope and the fix is a judgement call (make one
   the reference and have the other point at it, or accept the duplication because
   an operator reads exactly one of them). Worth a task in the next milestone.
3. **`# --- Required. There is no default, and no way to start without it. ---`
   heads a section of exactly one key** (`shared_secret`). That is fine and it is
   deliberate — the header is where "required" is stated, since the key's own block
   no longer carries a `Default:` line — but a later task adding a second required
   setting should put it here rather than inventing a second heading.

---

## Iteration 4 — 2026-08-12

**Did:** T004. `README.md`'s install now opens with the two deployments as numbered
paths, states the three prerequisites nobody was told, and points at the daemon's
own refusals. The two door sections are renamed **Path 1** and **Path 2** so the
choice and the instructions share a vocabulary.

**Learned:**

- **A troubleshooting `journalctl` line cannot be written as a command.**
  `quickstart_test.go:2050` (`trailCommands`) takes **every line in `README.md` or
  `deploy/README.md` that begins with `journalctl`** once a leading `#` is stripped,
  and `filterOf` then **`t.Fatal`s** unless that line pipes (`|`) *and* its producer
  names `journalctl`, `--user`, `-u crswd` **and** `-o cat`. That sweep exists for
  audit-trail commands; a diagnostics command is the opposite — it wants the stderr
  banners the documented filter removes (#88). So `journalctl --user -u crswd -e`
  is written **inline in prose, mid-line**, never in a fenced block and never at the
  start of a source line. **T005–T008: the same trap fires on a wrapped line.**
- **Five tests read `README.md`, and T005/T006 will walk straight into three of
  them.** `internal/httpapi/login_test.go:1004` requires the literal string
  `http://<the host's LAN address>:8765/login` — **T006 rewrites exactly that
  paragraph and must keep that address byte for byte**, path included, since it is
  read from the mux's own constant. `internal/release/readme_test.go` needs the
  installer's one-liner verbatim with no `go build` / `git clone` /
  `go mod download` **above** it (the from-a-clone block in Path 1 is the only one
  left, and it must stay below), the rollback trio (`~/.local/bin/crswd.previous`,
  `POST /dashboard/update`, `version=`), and the door vocabulary
  (`dashboard_password`, `access_enabled`, `admits nobody`).
  `internal/config/docs_test.go` reads the configuration table one row per line.
- **Anchors: `](#` is still the only check that exists, and an em dash makes a
  double hyphen.** `### Path 1 — on the internet: Cloudflare Tunnel and Access`
  slugs to `#path-1--on-the-internet-cloudflare-tunnel-and-access` — GitHub strips
  the em dash and the colon without collapsing the spaces either side. All six
  in-page links were re-checked after the rename.
- **The installer already warns about `claude` and points here.** `advise_tools`
  (`install.sh:106`) warns when `cloudflared` or `claude` is off `PATH`, "see the
  README" — so the prerequisite bullet is what that warning was pointing at all
  along. The installer says nothing about `claude` being *authenticated*, which is
  the half that actually strands a session, and the page now carries it.
- **The gate ran in full and green:** build, vet, `go test ./...`,
  `golangci-lint run` (v2.12.2, so #26's check passes, 0 issues), and
  `go test -tags quickstart ./cmd/crswd` (36s) — the last one is not optional here,
  since it is the suite that reads this file.

**Left:** T005–T008.

**Findings (not fixed):**

1. **The install opening and the front-matter paragraph (`README.md:10-14`) now
   both frame the two doors** — deliberately, since one is "what this project is"
   and the other is "the first step of installing it", and they link to different
   places (the comparison table vs. the two paths). **T007 owns the duplication
   trim and should decide whether both survive**; if only one does, the install
   copy is the one an operator is actually reading at that moment.
2. **`install.sh`'s printed next steps never mention `loginctl enable-linger`,**
   while the README calls it "easy to skip and expensive to skip" and the symptom
   (the unit dying when the SSH session ends) arrives minutes later looking like
   something else. `next_steps` (`install.sh:461`) lists two things: pick a door,
   then `systemctl --user enable --now crswd`. **Not fixed:** that output is pinned
   by `TestInstallPrintsNextSteps`, so it is a script change plus a test change, not
   a doc task. Worth a task.
3. **Iteration 1's findings 1 and 2 and iteration 2's finding 2 all still stand** —
   `docs/auth-and-sessions.md`'s colliding "two doors" and its "the skill",
   `deploy/README.md`'s Access-only "Verifying the exposure model", and the htmx
   claim under "Why it is built this way" — **now `README.md:700`, not the `:656`
   the last three entries name**, because this task added lines above it. T007
   still owns it. The README's own "Verifying the exposure model" is **not** an
   instance of the `deploy/` problem: it has both halves, tunnel and LAN.
