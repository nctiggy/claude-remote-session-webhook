# Quickstart: Proving milestone 6

**Read the first section before running anything here.**

## The rule that governs this milestone

**Nothing about the installer can be proven on the machine that wrote it.**

Here, the project is already installed, a config already exists, `~/.local/bin` is
already on `PATH`, and the unit is already in place. Every precondition the
installer exists to create is already true. "I ran it and it worked" proves
nothing, and it is the specific failure this milestone is written against.

So the installer scenarios below run in the `verify-install` CI job on a
GitHub-hosted runner with a fresh `HOME`. The ones you can run here are marked.

---

## US1 — Version (SC-012) · runnable here

```bash
go build -o /tmp/crswd-dev ./cmd/crswd && /tmp/crswd-dev --version
#   crswd dev (not a release)

go build -ldflags "-X .../internal/buildinfo.Version=v0.42" -o /tmp/crswd-42 ./cmd/crswd
/tmp/crswd-42 --version
#   crswd v0.42
```

The first is the important one: an unstamped build must **not** claim a release.

---

## US2 — Releases · CI

| Check | Expected |
|---|---|
| Merge to main | A release appears, version from `git rev-list --count HEAD` |
| Assets | Both tarballs, the unit, the tunnel example, the client, sums, signature |
| `ldd crswd` | not a dynamic executable |
| `sha256sum -c SHA256SUMS` | all ok |
| 21st release | Oldest pruned; newest two untouched |

---

## US3 — Installer · **CI only, on a fresh host**

| Step | Expected |
|---|---|
| Run the one-liner on a clean machine | Binary, unit, config land |
| `systemctl --user is-active crswd` | **inactive** — it must not start |
| Output | Names the secret, the roots, and the enable command |
| `stat -c %a ~/.config/crswd/config` | `600` |
| Edit config and unit, run again | Both left **byte-identical**; installer says so |
| Run on an unsupported arch | Refuses, names what is published |
| `grep -nE '/home/\|/Users/\|/root/' install.sh` | no matches |
| `grep -n nctiggy install.sh` | only lines carrying `https://` |

The last two rows are FR-020, and they are two rows rather than one on purpose.
`contracts/installer.md` says plainly that the account name in the URL it fetches
from "is unavoidable and fine" — the release is published under it and there is
nowhere else to get the bytes. A check expecting the name to be absent outright
could only pass on an installer that cannot download anything.
`TestInstallerNamesNobody` encodes the same two rows.

---

## US4 — Self-update · runnable here against a test release

| Step | Expected |
|---|---|
| `POST /dashboard/update` with `confirm=yes` | Daemon ends up on the new version |
| Same, no `confirm` | Refused, version unchanged |
| Corrupt one byte of the tarball | **Refused**, nothing renamed, still on the old version |
| Break the signature | Refused |
| Remove `SHA256SUMS.sig` | Refused — absence is not "nothing to check" |
| Stage an arm64 binary on amd64 | **Refused by the smoke test**, before the rename |
| Redirect to another host | Refused |
| `version=v0.41` | Rolls back |
| After any failure | `ls -l ~/.local/share/crswd/staging/` shows nothing executable |
| Restart mid-stage | Staging swept at boot |

The arm64 row is the one worth doing by hand. It passes every cryptographic check
and must still be refused.

---

## US5 — The rain · runnable here

| Check | Expected |
|---|---|
| Watch the header | Occasionally a message |
| `curl -s <dashboard> \| grep -i <message>` | No match — it is drawn, not inserted |
| With `prefers-reduced-motion` | No rain, no messages |

---

## The invariants, every iteration

```bash
test ! -f go.sum && echo "SC-012 ok"
go build ./... && go vet ./... && go test ./...
go test -tags tmux ./... && go test -tags quickstart ./...
golangci-lint run          # must print 2.12.2
```

And the one that matters most here:

```bash
grep -rIl 'PRIVATE KEY\|RELEASE_SIGNING_KEY' --exclude-dir=.git . | grep -v '\.md$'
# must be empty — SC-009
```
