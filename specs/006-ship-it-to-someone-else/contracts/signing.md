# Contract: The signing key

**Files**: `cmd/crswd/keygen.go`, `internal/updater/release_key.txt`, `.github/workflows/release.yml`
**Tests**: `cmd/crswd/keygen_test.go`, `internal/updater/verify_test.go`
**Satisfies**: FR-024, FR-025, FR-030

---

## The operator holds the private key. Nobody else ever sees it.

This is the one part of the milestone that requires a human action, and it
requires it precisely because the whole value of a signature is that the person
who can produce one is the person who is supposed to.

| Step | Who | What |
|---|---|---|
| Generate | **operator** | `crswd keygen` prints both halves to their terminal |
| Store private | **operator** | pastes into repository secret `RELEASE_SIGNING_KEY` |
| Commit public | **operator** | adds the printed line to `internal/updater/release_key.txt` |
| Sign | CI | signs `SHA256SUMS`, emits `SHA256SUMS.sig` |
| Verify | daemon | `ed25519.Verify` against the embedded public key |

**`crswd keygen` writes nothing to disk and logs nothing.** It prints to stdout
and exits. That is deliberate: a key written to a file is a key that gets
committed by accident, and a key that passes through anything other than the
operator's own terminal is a key with a second copy somewhere.

## Why the public key is committed, not fetched

A key retrieved at update time from the same host that serves the release is not
a second factor — it is the same factor twice. Anyone able to serve a malicious
binary and a matching checksum can serve the key that vouches for both.

Embedding it means the trust decision was made **when the operator installed this
binary**, before any attacker was involved. That is the property signing exists
to buy, and fetching the key spends it.

## Rotation is additive

`release_key.txt` holds **one base64 key per line**. Verification succeeds against
any line.

```
# Rotating:
1. crswd keygen                          → new pair
2. add the new public line to release_key.txt, keep the old
3. replace RELEASE_SIGNING_KEY           → new releases sign with the new key
4. wait until every retained release is signed by the new key
5. only then delete the old line
```

**A rotation that deletes first strands every existing release**, including the
one an operator might need to roll back to. Step 5 is the whole procedure.

## What must never happen

- The private key in a release artifact, a log, an audit record, a page, or a
  test fixture.
- The private key in the repository, in any form, including base64 or a
  "example" key that is actually valid.
- A release published without `SHA256SUMS.sig`.
- Verification that treats a missing signature as "nothing to check".

## Worked example

```
$ crswd keygen
crswd: generated an ed25519 release key pair.

Private half — paste into the repository secret RELEASE_SIGNING_KEY.
Do not commit it, and do not paste it anywhere else:

  <base64>

Public half — add as a new line to internal/updater/release_key.txt:

  <base64>
```

## Contract tests

| Test | Asserts | **Must fail when** |
|---|---|---|
| `TestKeygenWritesNothingToDisk` | No file is created anywhere | It "helpfully" saves the key, which is how it ends up committed |
| `TestKeygenOutputIsParseableByVerifier` | The printed public half verifies a signature made by the printed private half | The two halves are printed in formats the verifier cannot read, discovered only at the first real release |
| `TestVerifyAcceptsAnyCommittedKey` | A signature by any line in `release_key.txt` verifies | Only the first line is tried, so a rotation strands old releases |
| `TestVerifyRejectsUnknownKey` | A signature by a key not in the file is refused | Any well-formed signature passes |
| `TestMissingSignatureRefuses` | No `.sig` → refused | Absence reads as "nothing to verify" |
| `TestNoPrivateKeyInRepository` | No committed file contains an ed25519 private key | An example key that happens to be real is added to a fixture |
| `TestNoKeyInAnyLogOrRecord` | Exercising verification writes no key material anywhere | A diagnostic prints what it verified against |
| `TestReleaseKeyFileIsEmbedded` | The verifier reads the committed file, not a runtime fetch | Someone "improves" it by fetching the key, spending the property signing exists to buy |
