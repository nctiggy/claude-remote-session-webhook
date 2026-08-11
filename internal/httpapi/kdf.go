package httpapi

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
)

// passwordKDFIterations is how many rounds a single sign-in costs.
//
// The number is a trade an operator never sees and should not have to: high
// enough that guessing is expensive, low enough that the one person who knows
// the password does not notice. 200k rounds of HMAC-SHA256 is a few tens of
// milliseconds on the hardware this daemon runs on, and it multiplies the cost
// of every guess by the same amount.
//
// It is deliberately not configurable. A knob here is a knob an operator can
// turn the wrong way, and the only wrong way is down.
const passwordKDFIterations = 200_000

// derivePassword is PBKDF2-HMAC-SHA256 (RFC 8018 §5.2) over one block, which is
// all a 32-byte key needs.
//
// Written out rather than imported: crypto/pbkdf2 arrived in Go 1.24 and go.mod
// pins the language at 1.23 on purpose, so reaching for it would raise the floor
// for every consumer of this module to buy twenty lines. golang.org/x/crypto is
// not an option at all — go.sum must stay absent (docs/security.md §5).
//
// Why it exists: sha256.Sum256 was here first, purely to make both sides of a
// constant-time compare the same length. That is sound against the threat it was
// written for and CodeQL was right that it is not sound against the other one —
// a fast digest makes an offline guess cheap, and cheap guesses are what a
// password door has to survive. The rate limiter bounds guesses per minute; this
// bounds what each one costs, and the two are not substitutes.
//
// The salt is the daemon's shared secret: already required, already at least 32
// bytes, already unique per deployment, and already secret — so two daemons with
// the same password derive different keys, and a derived key from one is useless
// against the other.
func derivePassword(password, salt []byte) [sha256.Size]byte {
	mac := hmac.New(sha256.New, password)

	// U1 = PRF(password, salt || INT(1))
	var block [4]byte
	binary.BigEndian.PutUint32(block[:], 1)
	mac.Write(salt)
	mac.Write(block[:])
	u := mac.Sum(nil)

	var out [sha256.Size]byte
	copy(out[:], u)

	// U2..Uc, folded in as they are produced. Each round depends on the last, so
	// the work cannot be parallelised away by whoever is guessing.
	for i := 1; i < passwordKDFIterations; i++ {
		mac.Reset()
		mac.Write(u)
		u = mac.Sum(u[:0])
		for j := range out {
			out[j] ^= u[j]
		}
	}
	return out
}
