package main

// `crswd keygen` (FR-024, FR-030) — the one command in this repository that
// produces a secret, and the only one that must be run by a person rather than
// by anything else.
//
// # Why it writes nothing
//
// It prints both halves of an ed25519 pair to the report stream and exits. It
// opens no file, creates no directory, and logs nothing, which is why this file
// imports neither os nor log — a constraint keygen_test.go asserts structurally,
// because "helpfully" saving the key somewhere convenient is exactly how it ends
// up committed. There is no --output flag for the same reason.
//
// The operator's terminal is therefore the only copy that ever exists. That is
// the point rather than an inconvenience: a key that passed through a file, a
// pipeline, or an autonomous agent's transcript is a key with a second copy
// somewhere, and the whole value of a signature is that the person who can
// produce one is the person who is supposed to be able to.
//
// # The two halves, and where each goes
//
// The private half goes into the repository secret RELEASE_SIGNING_KEY, where
// CI signs SHA256SUMS with it. The public half is *committed*, to two files —
// internal/updater/release_key.txt, which the daemon embeds, and the
// RELEASE_KEYS block in install.sh, which cannot read that file because it is
// fetched on its own with no checkout beside it. Committing it rather than
// fetching it is what makes the trust decision one the operator made when they
// installed this binary, before any attacker was involved; a key retrieved at
// update time from the host that serves the release is the same factor twice.
//
// Both are base64 of the raw key bytes, with no PEM wrapper and no header: 32
// bytes public, 64 bytes private. The public spelling is fixed by install.sh,
// which wraps that line in the constant 12-byte SubjectPublicKeyInfo header
// openssl(1) needs. The private spelling is Go's own ed25519.PrivateKey — seed
// first, public half after it — so whatever signs in CI can take the seed from
// the first 32 bytes if the tool it uses wants a PKCS#8 key instead.

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"io"
)

// keygenCommand is the word on the command line, spelled once: main.go
// dispatches on it and keygen_test.go drives it.
const keygenCommand = "keygen"

// committedKeyFile and installerKeyBlock are where the operator puts the public
// half. Both are named in the output rather than left to a document, because the
// second one is the copy that gets forgotten — the two lists disagreeing is a
// release the daemon installs and the installer refuses, or the reverse.
const (
	committedKeyFile  = "internal/updater/release_key.txt"
	installerKeyBlock = "the RELEASE_KEYS block in install.sh"
)

const keygenUsage = "usage: crswd keygen    print a new ed25519 release key pair, and exit\n"

// runKeygen answers `crswd keygen` and returns the process's exit code.
//
// An extra argument is refused rather than ignored, on the same grounds as
// runConfigCommand's refusal: the alternative is that a mistyped instruction
// falls through to a command that prints a private key. It also forecloses the
// most likely shape of the mistake this whole file is written against —
// `crswd keygen > release.key`, which is an argument this program never sees,
// but `crswd keygen release.key` is one it does.
func runKeygen(out, errOut io.Writer, rest []string) int {
	if len(rest) > 0 {
		say(errOut, "crswd: keygen takes no arguments, and %q was given\n%s", rest[0], keygenUsage)
		return 2
	}

	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		// The error and not the pair: whatever GenerateKey produced before
		// failing is still key material, and this stream is the one an operator
		// pastes into an issue.
		say(errOut, "crswd: could not generate a key pair: %v\n", err)
		return 1
	}

	printKeyPair(out, public, private)
	return 0
}

// printKeyPair is the whole of what keygen does, and it is the operator's
// handover as much as it is two base64 lines.
//
// It names all three destinations, because an operator who fills only the secret
// has a CI job that signs releases nothing will accept, and one who fills only
// release_key.txt has a daemon and an installer that disagree.
func printKeyPair(out io.Writer, public ed25519.PublicKey, private ed25519.PrivateKey) {
	sayln(out, "crswd: generated an ed25519 release key pair.")
	sayln(out, "")
	sayln(out, "Private half — paste into the repository secret RELEASE_SIGNING_KEY.")
	sayln(out, "Do not commit it, and do not paste it anywhere else:")
	sayln(out, "")
	say(out, "  %s\n", base64.StdEncoding.EncodeToString(private))
	sayln(out, "")
	say(out, "Public half — add as a new line to %s,\nand the same line to %s:\n", committedKeyFile, installerKeyBlock)
	sayln(out, "")
	say(out, "  %s\n", base64.StdEncoding.EncodeToString(public))
	sayln(out, "")
	sayln(out, "Nothing was written to disk. What is above is the only copy of the private")
	sayln(out, "half that exists, so store it before you close this terminal.")
}
