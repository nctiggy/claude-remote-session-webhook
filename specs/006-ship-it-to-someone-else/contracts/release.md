# Contract: What a release is

**Files**: `.github/workflows/release.yml` (new)
**Tests**: the workflow's own assertions, plus `internal/release/assets_test.go`
**Satisfies**: FR-006 … FR-011

---

## Assets, named exactly

```
crswd_<version>_linux_amd64.tar.gz
crswd_<version>_linux_arm64.tar.gz
SHA256SUMS
SHA256SUMS.sig
crswd.service
cloudflared.example.yml
crswd-api
```

**The name format is written three times** — in YAML, in `install.sh`, and in Go —
because they cannot share a constant across three languages. The duplication is
unavoidable. The drift is not, and a drift is a 404 at the exact moment somebody
is installing or updating, so a test reads `install.sh` and the Go constant and
asserts they agree.

## The binary must run where there is no compiler

`CGO_ENABLED=0`. The current build links libc dynamically, so it is not the
"download and run" artifact it appears to be. arm64 is **cross-compiled** —
`GOARCH=arm64` on the same runner — not built on a second machine.

## The deployment files matter as much as the binary

`deploy/` already carries working, commented examples. An operator who downloads
only a binary still has to write a unit file by hand, which is the state this
milestone exists to end.

## Retention

Keep the last **20**. Never delete the two most recent, and never one a pointer
still resolves to — a `latest` that 404s is worse than an unbounded list.

## Contract tests

| Test | Asserts | **Must fail when** |
|---|---|---|
| `TestReleaseCarriesEveryAsset` | All seven are present | The deployment files are dropped as "not the real artifact" |
| `TestEveryAssetHasAChecksum` | `SHA256SUMS` covers every asset | Only the binaries are summed |
| `TestBinaryIsStaticallyLinked` | `ldd` reports not-a-dynamic-executable | `CGO_ENABLED=0` is dropped and it works on the builder anyway |
| `TestAssetNamesAgreeAcrossLanguages` | Shell and Go build the same name | They drift — a 404 while installing |
| `TestRetentionKeepsTwentyAndNeverTheNewestTwo` | Pruning honours both rules | It prunes by age alone and deletes what `latest` points at |
| `TestReleasePublishedOnMerge` | A merge to main produces a release | It is tag-only, so releases stop happening and self-update has nothing to find |
