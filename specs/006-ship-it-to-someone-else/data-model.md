# Data Model: Ship it to someone else

Five entities. None is persistent daemon state — this milestone adds no database,
no new session field, and nothing that survives a restart except files the
operator owns.

---

## 1. Version

What a build calls itself.

| Property | Value |
|---|---|
| Held in | `internal/buildinfo.Version`, one variable |
| Default | `"dev"` — the honest answer for a build no release produced |
| Stamped by | `-ldflags -X` at release time |
| Shape | `v0.<count>`, from `git rev-list --count HEAD` |
| Read by | `crswd --version` and `GET /dashboard/version`, both from the same variable |

**The default is the sentinel**, so forgetting to stamp cannot make a working tree
claim to be a release. Deliberately not semver.

---

## 2. Release

A published set of artifacts for one version.

| Asset | |
|---|---|
| `crswd_<version>_linux_amd64.tar.gz` | the binary |
| `crswd_<version>_linux_arm64.tar.gz` | cross-compiled, same runner |
| `crswd.service` | the unit |
| `cloudflared.example.yml` | the tunnel example |
| `crswd-api` | the signing client |
| `SHA256SUMS` | covers every asset above |
| `SHA256SUMS.sig` | ed25519 over `SHA256SUMS` |

**Relationship**: one release per merge to main. Retention keeps 20, never the
newest two, never one a pointer resolves to.

---

## 3. Signature

What proves a checksum file came from this project rather than from whoever
served it.

| Property | Value |
|---|---|
| Algorithm | ed25519 (`crypto/ed25519`, stdlib) |
| Signs | `SHA256SUMS`, not the binaries directly |
| Private half | a repository secret. **The operator holds it; nobody else has a copy.** |
| Public half | committed to `internal/updater/release_key.txt`, one per line, embedded |
| Rotation | additive — add the new line, retire the old only once every retained release is signed by it |

**This is the entity whose absence must stop an update.** A checksum travels with
what it describes; the signature is what makes it mean anything.

---

## 4. Staged binary

A downloaded candidate that is not yet installed.

| Property | Value |
|---|---|
| Location | `~/.local/share/crswd/staging/crswd.<version>` |
| Mode on arrival | `0600` — **not executable** |
| Mode after verification | `0700`, and only then |
| Lifetime | removed on failure; **swept at startup** |
| Becomes installed by | `os.Rename` over `~/.local/bin/crswd` — atomic on one filesystem |

**The invariant: nothing here is executable until both the checksum and the
signature verify.** Swept at startup because the process that vouched for those
bytes did not live to say so.

### State transitions

```
absent → downloaded(0600) → verified(0700) → smoke-tested → installed
              │                   │               │
              └───── refused ─────┴───────────────┘   (removed; daemon unchanged)
```

---

## 5. Installer record

What the installer knows about what it previously wrote.

| Property | Value |
|---|---|
| Location | `~/.local/share/crswd/crswd.service.sha256` |
| Written | after the installer writes the unit |
| Read | on a later run, to decide whether the unit may be replaced |

| Recorded | Installed unit | Action |
|---|---|---|
| matches | untouched since we wrote it | replace |
| differs | **operator edited it** | leave, and say so |
| absent | somebody else put it there | leave, and say so |

The third row is not an edge case — it is every host deployed before this
milestone, including this operator's.

---

## What is deliberately not modelled

- **An update schedule.** This milestone makes an update possible and verifiable.
  A daemon that updates itself unasked is a larger decision.
- **A rollback state machine.** The previous binary is kept and rolling back is a
  documented one-liner. A daemon that cannot start cannot decide to replace
  itself, so automatic rollback needs a supervisor this project does not have.
