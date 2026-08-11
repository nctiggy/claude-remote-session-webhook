package httpapi

import (
	"encoding/hex"
	"testing"
)

// TestDerivePasswordMatchesAReferencePBKDF2 is the only thing standing between
// this file and plausible-looking code.
//
// derivePassword is hand-written because crypto/pbkdf2 needs Go 1.24 and go.mod
// pins 1.23, and x/crypto would mean a go.sum. A hand-rolled primitive that
// nobody checked against a reference is not a primitive, it is an assumption --
// and one that fails open here, because a wrong derivation still compares equal
// to itself and every test above would pass.
//
// The vector is CPython's hashlib.pbkdf2_hmac with this file's own parameters.
//
// **Must fail when** the iteration count, the block index, the XOR fold or the
// PRF changes in a way that stops this being PBKDF2-HMAC-SHA256.
func TestDerivePasswordMatchesAReferencePBKDF2(t *testing.T) {
	got := derivePassword([]byte("correct horse battery staple"), []byte("0123456789abcdef0123456789abcdef"))
	const want = "c8746f4db7232fec6386909b6a80287dfe5d4b83f990a1167d4126b64b83b68e"
	if hex.EncodeToString(got[:]) != want {
		t.Fatalf("derivePassword is not PBKDF2-HMAC-SHA256:\n got  %s\n want %s", hex.EncodeToString(got[:]), want)
	}
}
