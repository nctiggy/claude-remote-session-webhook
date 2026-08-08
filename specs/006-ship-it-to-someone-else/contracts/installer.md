# Contract: The installer

**Files**: `install.sh` (repository root), `.github/workflows/release.yml` (the verify job)
**Tests**: `internal/release/install_test.go`, and a `verify-install` CI job
**Satisfies**: FR-012 … FR-020
**Decomposed**: four tasks. See the bottom.

---

## This contract cannot be satisfied on the author's machine

**Read this before writing any test for it.**

Here, the project is already installed, a configuration already exists,
`~/.local/bin` is already on `PATH`, and the service unit is already in place.
**Every precondition the installer exists to create is already true.** A
successful run on this box demonstrates nothing about a new user's machine, and
"I ran it and it worked" is the specific failure this contract is written to
prevent.

So every requirement below is verified in a `verify-install` job on a
**GitHub-hosted `ubuntu-latest` runner** — not the self-hosted ones, which are
this operator's machines and carry his home directory. A fresh cloud VM is
genuinely somebody else's computer.

The job runs **the published release**, not a local build, with a fresh `HOME`,
and runs the installer **twice** — once to prove it installs, once to prove it
does not overwrite.

## What the installer does, in order

```
1. detect os/arch          → refuse anything not published, naming what is
2. download binary + SHA256SUMS + SHA256SUMS.sig
3. verify                  → BEFORE anything is made executable
4. install binary          → ~/.local/bin/crswd
5. install unit            → ~/.config/systemd/user/crswd.service   (see below)
6. record unit hash        → ~/.local/share/crswd/crswd.service.sha256
7. write config IF ABSENT  → ~/.config/crswd/config, mode 0600
8. print next steps        → and STOP. Do not enable. Do not start.
```

## The three things it must never do

**Never overwrite a configuration.** Step 7 runs only when the file is absent. An
installer that replaced a working configuration would destroy the one file the
operator authored, and it would do it during an operation they think of as safe.

**Never overwrite an edited unit.** Step 5 compares the installed unit against the
hash recorded at step 6 of a previous run:

| Recorded hash | Installed unit | Action |
|---|---|---|
| exists, matches | untouched since we wrote it | replace |
| exists, differs | **the operator edited it** | leave it, say so |
| no record | somebody else put it there | leave it, say so |

The third row is not an edge case. It is what happens when an operator wrote the
unit by hand, which is exactly how this daemon has been deployed until now.

**Never start the daemon.** It cannot work before the secret and the roots are
set. A service that fails on first boot teaches an operator to ignore a failing
service, which is a habit worth more than the convenience.

## No individual's name (FR-020)

`install.sh` must contain no personal name and no account identifier. The
repository owner appears in the URL it fetches from, which is unavoidable and
fine; a hardcoded home directory, username, or path under `/home/<someone>` is
not.

## Worked example

Fresh Ubuntu container, nothing installed:

```
$ curl -fsSL <raw url>/install.sh | bash
crswd: detected linux/amd64
crswd: downloading v0.42
crswd: verifying SHA256SUMS.sig ... ok
crswd: verifying crswd_v0.42_linux_amd64.tar.gz ... ok
crswd: installed ~/.local/bin/crswd
crswd: installed ~/.config/systemd/user/crswd.service
crswd: wrote ~/.config/crswd/config (mode 0600)
crswd:
crswd: Next: set shared_secret and allowed_roots in ~/.config/crswd/config,
crswd: then: systemctl --user enable --now crswd
$ systemctl --user is-active crswd
inactive
```

Run again after editing the config and the unit:

```
crswd: ~/.config/crswd/config exists — leaving it alone
crswd: ~/.config/systemd/user/crswd.service has been modified — leaving it alone
```

## Contract tests

| Test | Asserts | **Must fail when** |
|---|---|---|
| `TestInstallOnFreshHost` (CI, ubuntu-latest) | Binary, unit and config land; daemon **not** running | It is only ever run where the project is already installed — *"it worked on the author's machine"*, which here proves nothing at all |
| `TestInstallVerifiesBeforeExecutable` | Nothing is chmod'd `+x` before the signature verifies | Verification happens after installation |
| `TestInstallRefusesUnknownPlatform` | Unsupported arch refuses and names what is published | It downloads an amd64 binary onto arm |
| `TestInstallNeverOverwritesConfig` (CI, run twice) | Second run leaves an edited config **byte-identical** | It writes unconditionally |
| `TestInstallNeverOverwritesEditedUnit` (CI, run twice) | An edited unit survives; the installer says so | It compares against a shipped copy rather than what it recorded |
| `TestInstallLeavesNoRecordAlone` | With no recorded hash, an existing unit is left alone | Absence of a record is read as permission |
| `TestConfigModeIs0600` | The written config is `0600` | It inherits umask, so a group-readable config holds a secret |
| `TestInstallDoesNotStartDaemon` (CI) | `systemctl --user is-active` is `inactive` after install | Convenience wins and a broken service starts on first boot |
| `TestInstallPrintsNextSteps` | Output names the secret, the roots, and the enable command | It succeeds silently and the operator does not know what is left |
| `TestInstallerNamesNobody` | `install.sh` contains no personal name or `/home/<user>` path | The author's home path is hardcoded |
| `TestAssetNamesAgreeAcrossLanguages` | The name built by `install.sh` equals the Go constant | They drift, and the drift is a 404 exactly when someone is installing |

## The four tasks

1. **Detect, download, verify** — platform detection, fetch, checksum and
   signature, before anything is executable.
2. **Place files** — binary, unit, the recorded hash, and the config-only-if-absent
   rule.
3. **Refuse to clobber** — the edited-unit comparison and its three outcomes.
4. **The `verify-install` CI job** — ubuntu-latest, fresh `HOME`, published
   release, run twice.

Task 4 is not optional polish. Without it, tasks 1 through 3 are only ever proven
in the one environment where they cannot fail.
