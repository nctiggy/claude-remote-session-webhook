# Implementation Plan

**Milestone 13 — The config an install actually leaves behind.**

> *"The config that is written during install is not the complete example. It is
> very basic and missing a lot of options."*

Six tasks.

---

## The gap, measured

**The installer writes two settings. The daemon understands twenty-three.**

An operator who installs today gets `shared_secret` and `allowed_roots`, and no
indication that `listen`, `dashboard_password`, `access_enabled`, `max_sessions`,
the four lifetime keys, the start commands, or anything else exists. They find out
by reading the README, or they never find out.

## Why it drifted, and what stops it drifting again

`.env.example` is held to the code **in both directions** by
`TestEnvExampleNamesEveryVariable`: a variable the code reads that the file never
names fails, and a variable the file names that nothing reads fails too. It is
complete because it is checked.

**The installer's config template is a heredoc guarded by nothing.** That is the
whole explanation for two-of-twenty-three, and a hand-written replacement with no
guard would be back here in a milestone or two.

So the template becomes **a real file with the same guard**, shipped and verified
along the path the unit already takes: a release asset, checksummed, covered by
the same signature, fetched and verified by the installer before anything is
placed.

---

## ⚠️ Rules that carry over and must not be weakened

- **An existing configuration is never overwritten.** Not for a better template,
  not for a newer one. A config that is present is the operator's.
- **The generated `shared_secret` is never printed**, and now must not appear in
  the shipped template either — the template carries keys and explanations, never
  values. `gitleaks` allowlists `.env.example` *because* it holds no values; the
  same must be true here before that allowlist is extended.
- **`allowed_roots` still defaults to `$HOME/code`, never `$HOME`.**
- **Nothing here can be proven on this machine.** `verify-install` on a fresh
  `HOME` is the only thing that can fail for these reasons. **Any task touching
  `install.sh` or the release extends that job in the same task.**

---

## Conventions

- `- [ ]` open · `- [x]` done · `- [!]` blocked (reason in `PROGRESS.md`)
- Priority order is meaningful — the loop always takes the topmost open item.
- **Every task ends green**: `go build ./... && go vet ./... && go test ./... && golangci-lint run`,
  plus `-tags tmux` / `-tags quickstart` where touched.
- **Check the linter is v2 before trusting it** (#26).
- `go.sum` must never appear.
- **AR-008: no refactoring outside the task.**
- **A task is not done when the code exists. It is done when something calls it.**
- **A new guard must be proven by breaking it.**

---

## Tasks

- [ ] **T001** Create `deploy/crswd.example.config` — the complete configuration in the file format the daemon reads (`key = value`), carrying **every** key the daemon understands, each commented out, each with the explanation an operator needs to decide. Values are never present: the file is keys and prose only. Take the explanations from `.env.example` and the README rather than writing new ones, and keep this project's voice — say what a setting is for and what happens if it is wrong, not just what it is.

- [ ] **T002** 🔒 Guard it, mirroring `TestEnvExampleNamesEveryVariable` in `internal/config/`. Three assertions, all in both directions where that applies: **every key the daemon reads is named** in the file and nothing is named that the daemon does not read; **no key carries a value**, which is the committed-secret guard and the precondition for any `gitleaks` allowlist; and **every key is described** rather than merely listed. **Prove each by breaking it**: add a key to the loader and watch it fail, put a value in and watch it fail.

- [ ] **T003** Ship `deploy/crswd.example.config` as a release asset in `.github/workflows/release.yml`, exactly as `deploy/crswd.example.service` is shipped as `crswd.service` — copied into `dist/`, listed in the upload, and therefore covered by `SHA256SUMS` and the signature over it. **No second mechanism.**

- [ ] **T004** 🔒 Have `install.sh` fetch and verify it like the unit — `fetch`, then `verify_checksum`, before anything is placed. A release whose config asset does not verify is refused exactly as a bad tarball is.

- [ ] **T005** 🔒 Write the installed configuration **from that template** rather than from a heredoc: place the verified file, then fill in the generated `shared_secret` and the `allowed_roots` default. Mode `0600`. **An existing config is still never touched.** The secret is still never printed. Delete the heredoc so there is one source and not two that can disagree.

- [ ] **T006** Extend `verify-install` in the same workflow to assert, on a fresh `HOME`: the installed config names **every** key the daemon reads, `shared_secret` is set and at least 32 bytes, `allowed_roots` names a directory that exists, the file is `0600`, the secret does not appear in the installer's output, and a second run leaves the file byte-identical. **"It worked when I ran it" is not evidence here.**

---

## Out of scope

- **Changing any default.** This milestone makes the options visible, not different.
- **A settings-page editor for keys that are not already editable.** `IsSecret` and
  `Editable` decide that and neither changes here.
- **#120, #121.** Unchanged. **Q2** is still the operator's to answer.
