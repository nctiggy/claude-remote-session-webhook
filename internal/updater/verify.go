package updater

// verify.go is steps 2 and 3 of the order contracts/self-update.md fixes:
// sha256 of the downloaded bytes against SHA256SUMS, **then** ed25519 over
// SHA256SUMS itself against a key this binary was built carrying.
//
// # Why both, and why in that order
//
// A checksum travels with the thing it describes. SHA256SUMS and the tarball
// arrive from the same host over the same connection, so on its own the list
// says only that the two arrived together — anyone who can serve a tampered
// tarball can serve a list that matches it. **The signature is what makes the
// checksum mean anything** (FR-024), and it is checked against
// release_key.txt, which was embedded when the operator installed this binary:
// the trust decision was made before any attacker was involved.
//
// So neither check is sufficient and both must pass. The order is the one the
// contract fixes, and it decides which refusal an operator is shown when a
// release is wrong in both ways at once: the cheap, local, concrete one —
// "these are not the published bytes" — rather than the cryptographic one.
// TestChecksumMismatchRefuses pins that.
//
// # Why a missing signature is a refusal
//
// An absent or empty SHA256SUMS.sig is ErrNotSigned, never "there is nothing to
// verify against" (FR-025). Treating absence as a skip is the failure that makes
// signing decorative: an attacker who can serve a release can also decline to
// serve a signature for it, so a verifier that shrugs at absence has no
// signature check at all. The release workflow refuses to publish a release it
// could not sign for the same reason, from the other end.
//
// # What this file does not do
//
// It writes nothing, reads no file at run time, and installs nothing. Staging
// bytes at 0600 is T016's and the swap is T017's, each its own file, because a
// step that shares a file with the next one is a step somebody removes with an
// early return.

import (
	"crypto/ed25519"
	"crypto/sha256"
	_ "embed" // for the //go:embed directive below; the package itself is unused.
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
)

// releaseKeyFile is the committed list of public keys, compiled in.
//
// **Embedded rather than fetched, and that is the property signing exists to
// buy.** A key retrieved at update time from the host that serves the release
// is not a second factor, it is the same factor twice: whoever can serve a
// malicious binary and a matching checksum can serve the key that vouches for
// both. Embedding moves the trust decision back to the moment the operator
// installed this binary. TestReleaseKeyFileIsEmbedded holds it here.
//
//go:embed release_key.txt
var releaseKeyFile string

// keyFileName is where an operator can act on a refusal about the key list. It
// is a path in this repository rather than on the host, because that is where
// the file being complained about actually is — the copy on disk is inside the
// binary.
const keyFileName = "internal/updater/release_key.txt"

var (
	// ErrUnsummedAsset is a release whose SHA256SUMS does not cover the asset
	// that was downloaded. A refusal rather than a pass, because the whole
	// purpose of the list is to say what the published bytes are: silence about
	// an asset is not permission to install it.
	ErrUnsummedAsset = errors.New("SHA256SUMS publishes no checksum for that asset")

	// ErrChecksumMismatch is FR-023: the bytes are not the ones published.
	ErrChecksumMismatch = errors.New("the downloaded bytes are not the ones SHA256SUMS publishes")

	// ErrMalformedChecksums is a SHA256SUMS that is not a checksum list at all.
	// Refused rather than skipped past: a line this parser cannot read is a line
	// that might have been the one covering the asset.
	ErrMalformedChecksums = errors.New("SHA256SUMS is not a list of sha256 checksums")

	// ErrNotSigned is FR-025's first half — a release carrying no signature, or
	// something too short to be one. It is deliberately a different sentinel
	// from ErrSignatureUnverified: one says the release was never signed, the
	// other that it was signed by somebody this daemon does not know.
	ErrNotSigned = errors.New("the release carries no signature over SHA256SUMS")

	// ErrSignatureUnverified is FR-025's second half: signed, by nothing this
	// binary carries.
	ErrSignatureUnverified = errors.New("SHA256SUMS is not signed by any release key this daemon carries")

	// ErrNoReleaseKey is a binary built with every key line absent or commented
	// out. Nothing it downloads could be shown to come from this project, so
	// every update refuses — the same refusal install.sh makes when its
	// RELEASE_KEYS block is empty. Reached only through a build, so
	// TestReleaseKeyFileIsEmbedded is what keeps it out of a release.
	ErrNoReleaseKey = errors.New("this daemon carries no release key, so nothing it downloads can be shown to come from this project")

	// ErrMalformedReleaseKey is a key line that is not 32 base64-encoded bytes.
	// It refuses the whole update rather than skipping the line and trying the
	// rest: a typo in a rotation would otherwise verify against the old key and
	// look like a rotation that worked, right up until the old line is retired.
	ErrMalformedReleaseKey = errors.New("a line in the committed release keys is not an ed25519 public key")
)

// Verify is the whole of steps 2 and 3, over bytes that are not on disk yet.
//
// It takes the asset bytes rather than a path deliberately: nothing has to be
// written, and so nothing can be written and left behind, before something has
// decided the bytes are the published ones. T016 stages what this accepts.
//
// name is the exact asset name to look for in sums — the same string the fetch
// asked for, since "the nearest asset" is not the asset (FR-027).
func Verify(name string, asset, sums, signature []byte) error {
	return verifyAgainst(name, asset, sums, signature, releaseKeyFile)
}

// verifyAgainst is Verify with the key list supplied, so a test can exercise
// this exact parser and this exact comparison against a pair it generated in
// process and never wrote down.
//
// The operator's key cannot be used for that: nothing outside their terminal and
// the RELEASE_SIGNING_KEY secret has the half that signs, and a fixture key in
// this repository would be a real signing key in this repository.
func verifyAgainst(name string, asset, sums, signature []byte, keyList string) error {
	// Step 2 before step 3, per contracts/self-update.md.
	if err := matchesPublishedChecksum(name, asset, sums); err != nil {
		return err
	}
	return signedByACommittedKey(sums, signature, keyList)
}

// matchesPublishedChecksum is step 2.
func matchesPublishedChecksum(name string, asset, sums []byte) error {
	published, err := publishedChecksum(name, sums)
	if err != nil {
		return err
	}

	// No constant-time comparison: both sides are public, and the far end
	// already knows the checksum it published.
	digest := sha256.Sum256(asset)
	if hex.EncodeToString(digest[:]) != published {
		// Neither digest is named. They are not secret, but the only thing an
		// operator can do about this refusal is not install it, and a line of
		// hex in the journal does not help them decide that.
		return fmt.Errorf("%s: %w", name, ErrChecksumMismatch)
	}
	return nil
}

// publishedChecksum finds the one line of SHA256SUMS covering exactly name.
//
// Whole-name matching rather than `sha256sum -c` over the file: the list covers
// every asset in the release and an update downloads one of them, so checking
// the file as a whole would fail against a release that is entirely correct.
// install.sh matches the same line the same way, for the same reason.
func publishedChecksum(name string, sums []byte) (string, error) {
	found := ""
	for n, line := range strings.Split(string(sums), "\n") {
		line = strings.TrimSuffix(line, "\r")
		if line == "" {
			continue
		}
		sum, listed, err := splitSumLine(line)
		if err != nil {
			// The line number, not the line: this file arrived from the network
			// and errors go to the journal.
			return "", fmt.Errorf("%s line %d: %w", ChecksumsAsset, n+1, err)
		}
		if listed != name {
			continue
		}
		if found != "" && found != sum {
			// "The published checksum of this asset" has to be one value. Two
			// of them is an ambiguity to refuse rather than resolve by order,
			// the same way fetch.go refuses two assets under one name.
			return "", fmt.Errorf("%s covers %s twice with different checksums: %w", ChecksumsAsset, name, ErrMalformedChecksums)
		}
		found = sum
	}
	if found == "" {
		return "", fmt.Errorf("%s: %s: %w", ChecksumsAsset, name, ErrUnsummedAsset)
	}
	return found, nil
}

// splitSumLine reads one sha256sum(1) line: 64 hex digits, a two-character
// separator, then the name.
//
// coreutils spells that separator two ways and this accepts both — "  " for a
// text-mode digest, " *" for a binary one. The release workflow produces the
// first. Accepting the second costs nothing and means a release summed with
// `sha256sum -b` is not a refusal nobody can explain; accepting *anything*
// after the digest would let a line be read as covering a name it does not.
func splitSumLine(line string) (sum, name string, err error) {
	const digest = sha256.Size * 2

	// The digest, the separator, and at least one character of name.
	if len(line) < digest+3 {
		return "", "", ErrMalformedChecksums
	}
	sum, rest := line[:digest], line[digest:]
	if _, err := hex.DecodeString(sum); err != nil {
		return "", "", ErrMalformedChecksums
	}
	if rest[0] != ' ' || (rest[1] != ' ' && rest[1] != '*') {
		return "", "", ErrMalformedChecksums
	}
	// Lowered rather than compared case-insensitively at the call site, so the
	// comparison there is against hex.EncodeToString's own spelling.
	return strings.ToLower(sum), rest[2:], nil
}

// signedByACommittedKey is step 3, and it is not optional.
func signedByACommittedKey(sums, signature []byte, keyList string) error {
	// Absence first, so a release with no signature is told it has none rather
	// than being told about this host's key list. install.sh asks in the same
	// order.
	if len(signature) == 0 {
		return fmt.Errorf("%s: %w", SignatureAsset, ErrNotSigned)
	}
	if len(signature) != ed25519.SignatureSize {
		return fmt.Errorf("%s holds %d bytes rather than the %d of an ed25519 signature: %w",
			SignatureAsset, len(signature), ed25519.SignatureSize, ErrNotSigned)
	}

	keys, err := releaseKeys(keyList)
	if err != nil {
		return err
	}

	// **Every key, not the first.** Rotation is additive: for as long as it
	// takes for every retained release to be signed by the new key, releases
	// signed by either are live, and a loop that stopped at the first line
	// would strand whichever half of them the operator might need to roll back
	// to.
	for _, key := range keys {
		if ed25519.Verify(key, sums, signature) {
			return nil
		}
	}
	return fmt.Errorf("%s: %w", ChecksumsAsset, ErrSignatureUnverified)
}

// releaseKeys parses the committed list: one base64 public key per line, with
// blank lines and # comments ignored — a commented-out line is how an operator
// retires a key, so it must not count as carried. install.sh's RELEASE_KEYS
// block is read by the same rules and TestInstallerCarriesTheCommittedKeys
// holds the two copies together.
func releaseKeys(keyList string) ([]ed25519.PublicKey, error) {
	var keys []ed25519.PublicKey
	for n, line := range strings.Split(keyList, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		// The line number, never the line. A public key is not a secret, but
		// contracts/self-update.md is flat that no key material appears in a
		// log, a record or a page, and a refusal quoting one is all three.
		raw, err := base64.StdEncoding.DecodeString(line)
		if err != nil {
			return nil, fmt.Errorf("%s line %d is not base64: %w", keyFileName, n+1, ErrMalformedReleaseKey)
		}
		if len(raw) != ed25519.PublicKeySize {
			return nil, fmt.Errorf("%s line %d decodes to %d bytes rather than the %d of an ed25519 public key: %w",
				keyFileName, n+1, len(raw), ed25519.PublicKeySize, ErrMalformedReleaseKey)
		}
		keys = append(keys, ed25519.PublicKey(raw))
	}
	if len(keys) == 0 {
		return nil, fmt.Errorf("%s: %w", keyFileName, ErrNoReleaseKey)
	}
	return keys, nil
}
