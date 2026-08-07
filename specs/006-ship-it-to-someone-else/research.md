# Research: Ship it to someone else

Eight questions were open. The hardest is not the cryptography — ed25519 is in the
standard library and the verification is twenty lines. It is **how a daemon
replaces the binary it is currently executing** without leaving the host with
nothing that runs.

---

## R1 — How the version reaches the binary

**Decision.** A new package `internal/buildinfo` holding one variable, stamped at
link time:

```go
package buildinfo

// Version is what this build calls itself. "dev" is the honest answer for a
// build that no release produced, and it is the default precisely so that
// forgetting the ldflags cannot make a working tree claim to be a release.
var Version = "dev"
```

```
go build -ldflags "-X github.com/nctiggy/claude-remote-session-webhook/internal/buildinfo.Version=v0.42"
```

**Rationale.** A package rather than `main` because `internal/httpapi` has to
report the same string, and a `main` variable is unreachable from there. One
variable, one default, and the default is the sentinel — a build that skipped the
stamping says `dev`, which is true.

`--version` prints `crswd dev` or `crswd v0.42`. The route reports the same
string, from the same variable, so the two cannot disagree.

**Alternatives considered.** `debug.ReadBuildInfo()` with VCS stamping — rejected
because it reports a commit, not a release, and the two differ for exactly the
builds this milestone cares about. A generated file — rejected; a build artifact
in the tree is a merge conflict waiting to happen.

---

## R2 — Where the build number comes from

**Decision.** `git rev-list --count HEAD` on the merge commit. Version is
`v0.<count>`.

**Rationale.** It is monotonic on a linear main branch, and — the part that
matters — it is **derived from the repository itself**. `github.run_number` was
the obvious candidate and resets if a workflow is renamed or deleted and
recreated, which would make an older release outrank a newer one and quietly
break both the updater's "is this newer" question and the retention rule.

A number that lives in the repository survives CI being rebuilt around it.

**Alternatives considered.** `github.run_number` — rejected above. Semver —
rejected by the spec: it promises a compatibility contract this project has not
made. A date-based version — rejected because two merges in one day collide.

---

## R3 — The exact asset names

**Decision.**

```
crswd_v0.42_linux_amd64.tar.gz
crswd_v0.42_linux_arm64.tar.gz
SHA256SUMS
SHA256SUMS.sig
crswd.service
cloudflared.example.yml
crswd-api
```

Format: `crswd_<version>_<os>_<arch>.tar.gz`.

**Rationale, and this is the interesting part.** Three separate things construct
this name — the release workflow (YAML), the installer (shell), and the updater
(Go). They cannot share a constant across those three languages, so **the format
is duplicated three times by necessity**, and a drift between any two is a
download that 404s at exactly the moment an operator is trying to install or
update.

So a test reads `install.sh` and the Go constant and asserts they agree, and the
release workflow's output is asserted against the same shape. The duplication is
unavoidable; the drift is not.

**Alternatives considered.** Generating the shell from Go — rejected as a build
step to save one string. Asking the GitHub API for the asset list and matching by
suffix — rejected because "the asset whose name ends in `_arm64.tar.gz`" is a
looser check than "the asset named exactly this", and FR-027 wants the strict one.

---

## R4 — The signing key's lifecycle

**Decision.** Ed25519. **The operator generates the key and holds the private
half. It is never stored by anyone else, and never passes through a transcript.**

| Step | Who | What |
|---|---|---|
| Generate | operator | `crswd keygen` prints both halves to their terminal |
| Private half | operator | pasted into the repository secret `RELEASE_SIGNING_KEY` |
| Public half | operator | committed to `internal/updater/release_key.txt`, embedded with `go:embed` |
| Sign | CI | signs `SHA256SUMS`, producing `SHA256SUMS.sig` |
| Verify | daemon | `ed25519.Verify` against the embedded public key |

**Rationale.** `crswd keygen` rather than an `openssl` recipe because the daemon
must parse the result, and a subcommand that emits exactly what the daemon
expects removes a whole class of "wrong PEM variant" failure. It writes nothing
to disk — the operator copies from their own terminal.

The public key is **committed, not fetched**. A key retrieved at update time from
the same host as the release is not a second factor; it is the same factor twice.
Embedding it means the trust decision was made when the binary was installed.

**Rotation** is additive: `release_key.txt` holds one key per line, verification
succeeds against any of them, and the old line is deleted only after every
retained release is signed by the new key. A rotation that deletes first is a
rotation that strands every existing release.

**Alternatives considered.** Sigstore/cosign — better known, genuinely better at
transparency, and both require tooling the daemon cannot carry under FR-034.
Signing with a GitHub deploy key — rejected; it lives in the same account whose
compromise the signature is supposed to survive. GPG — rejected; no stdlib
verifier.

---

## R5 — How a daemon replaces the binary it is running

**Decision.** Stage, verify, **smoke-test the staged binary**, atomically rename,
then exit and let systemd restart into it.

```
1. download        → ~/.local/share/crswd/staging/crswd.v0.42   mode 0600
2. checksum        → against SHA256SUMS
3. signature       → SHA256SUMS.sig against the embedded key
4. chmod 0700      → the first moment it is executable at all
5. smoke test      → exec it with --version; it must print exactly v0.42
6. os.Rename       → over ~/.local/bin/crswd, atomic on one filesystem
7. exit 0          → systemd restarts into the new binary
```

**Rationale.** Step 5 is the one that is not obvious and is worth the most. A
checksum proves the bytes are the published bytes; it says nothing about whether
they *run on this host*. An arm64 build on an amd64 machine passes every
cryptographic check and then fails to exec — and by then it is the installed
binary. The smoke test is what makes that a refusal rather than an outage, and it
is only possible because US1 gave the binary a way to say what it is.

Exiting rather than `syscall.Exec` because systemd owns the process: exec would
keep the PID and confuse nothing, but it also inherits a listening socket and a
tmux server's worth of state that the new binary would have to be careful with. A
clean exit and a fresh start is the same path the operator already takes by hand,
and sessions survive it — that is what #63 bought.

**This requires `Restart=always` in the unit**, which today it may not have. That
is a change to `deploy/crswd.example.service`, and it is load-bearing: without it,
step 7 is just the daemon stopping.

**What if the new binary will not start?** The smoke test makes that nearly
impossible, and "nearly" is not "never". The previous binary is kept at
`~/.local/bin/crswd.previous`, and the failure is loud rather than silent: systemd
restart-loops and the journal says so. Rolling back is a documented one-liner.
Automatic rollback was considered and rejected — a daemon that cannot start cannot
decide it should be replaced, so the thing performing the rollback would have to
be something other than the daemon, which is a supervisor this project does not
have.

**Alternatives considered.** In-place overwrite — rejected; writing to a running
executable's inode is how you get `ETXTBSY` or a half-written binary.
`syscall.Exec` — above. Downloading to the final path and verifying there —
rejected; it makes the failure mode "an unverified binary is already installed".

---

## R6 — What "staged" means

**Decision.** `~/.local/share/crswd/staging/`, mode `0700`, containing a file
named for its version and created `0600`.

**The invariant: nothing in that directory is executable until both the checksum
and the signature have verified.** Downloaded bytes sit at `0600` — readable by
the operator, runnable by nobody, including by accident.

A failed update removes the staged file. A crash mid-update leaves it, which is
why the directory is swept at startup: a staged file is never trusted across a
restart, because the thing that vouched for it did not survive to say so.

---

## R7 — How the installer knows a unit was edited

**Decision.** Record what it wrote, and compare against that.

At install, after writing `~/.config/systemd/user/crswd.service`, the installer
also writes `~/.local/share/crswd/crswd.service.sha256` containing that file's
hash. On a later run:

| Installed unit's hash | Means | Action |
|---|---|---|
| matches the recorded hash | untouched since we wrote it | safe to replace |
| differs | the operator edited it | **leave it, and say so** |
| no record exists | this install did not write it | **leave it, and say so** |

**Rationale.** The alternative — comparing against every unit any version ever
shipped — grows without bound and gets it wrong the first time an old version's
file is legitimately still in place. Recording what *this* installer wrote is a
fact it actually knows.

The third row matters: no record means an operator put that file there, or an
older installer did. Both are "not ours to overwrite".

---

## R8 — The rain's messages, and keeping them silent

**Decision.** Drawn on the existing canvas, never inserted into the DOM. The
canvas element carries `aria-hidden="true"`.

**Rationale.** Canvas content is not in the accessibility tree, so a message drawn
there is invisible to a screen reader by construction rather than by an attribute
that could be removed. `aria-hidden` on the container is belt and braces for the
element itself.

FR-032 is satisfied by the rain's existing reduced-motion behaviour: no rain, no
messages. Nothing new is needed, and nothing new may be added that runs when the
rain does not.

The messages themselves live in `crswd.js` beside the rain, not in a template —
they are decoration with no server involvement, and routing them through a
template would make them look like content.

---

## R9 — How anything here gets proven on another machine

**This is the milestone's defining constraint and it deserves its own answer.**

The installer cannot be proven on this box. Here, the project is already
installed, a config already exists, `~/.local/bin` is already on `PATH`, and the
service unit is already in place — every precondition the installer exists to
create is already true, so a successful run here demonstrates nothing.

**Decision.** A `verify-install` job on **GitHub-hosted `ubuntu-latest`**, running
after the release job, against the release it just published.

| Property | Why this shape |
|---|---|
| GitHub-hosted, not self-hosted | The self-hosted runners are this operator's machines and carry his home directory. A fresh cloud VM is genuinely somebody else's computer. |
| Runs the published release | Not a local build — the artifact an actual user would download. |
| Fresh `HOME` | So "config already exists" and "unit already exists" are false, as they are for a new user. |
| Runs the installer twice | Once to prove it installs, once to prove it does not overwrite (SC-003). |

**Alternatives considered.** A container on the self-hosted runner — workable, and
rejected because it needs Docker present on a machine this project does not
otherwise constrain. A `fresh-HOME` harness in the Go tests — kept as well, since
it is fast and catches most of it, but it cannot catch a missing `PATH` entry or a
libc assumption, which are precisely the other-machine failures.
