package access

import (
	"context"
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// algorithmRS256 is the only algorithm this validator accepts, written once as a
// constant so there is nowhere for a second one to be added.
//
// It is compared against, never dispatched on. See signedClaims's step 3: the
// JOSE header's `alg` is read for exactly one purpose, which is to refuse
// anything that is not this value. That inversion is the whole reason layer 1 is
// hand-written (research D1) — `alg: none` and RS256/HS256 key confusion are
// both bugs a verifier can only have if it lets the token choose the verifier,
// and there is nothing here to choose with.
const algorithmRS256 = "RS256"

// The reasons an assertion is not one the edge wrote. Like the key-set errors
// they are recorded server-side and never reach a caller: every layer-1 failure
// produces the single uniform 401 the contract fixes, because the difference
// between "bad signature" and "wrong audience" tells an attacker which forgery
// to try next.
//
// None of them carries a byte of the assertion. The whole of it is
// caller-authored, and an error string here is written to the journal.
var (
	// errAssertionMissing is step 1. Nothing to validate is not a pass.
	errAssertionMissing = errors.New("the request carries no Access assertion")

	// errAssertionMalformed is step 2: refuse malformed input before any of it
	// is interpreted.
	errAssertionMalformed = errors.New("the assertion is not a well-formed JWS")

	// errJOSEHeaderMalformed is the one piece of interpretation step 3 cannot
	// avoid — `alg` and `kid` must be read to verify at all. Kept distinct from
	// the shape errors so an operator reading the journal can tell "this was not
	// three base64url segments" from "it was, and the header inside was not
	// JSON".
	errJOSEHeaderMalformed = fmt.Errorf("%w, and its JOSE header is not JSON", errAssertionMalformed)

	// errAlgorithmRefused is step 3's only purpose.
	errAlgorithmRefused = errors.New("the assertion is not signed with the one accepted algorithm")

	// errCriticalExtension is the rest of step 3. A `crit` parameter announces an
	// extension the signer requires its verifier to implement, and RFC 7515 says
	// a verifier that does not implement it must reject the token rather than
	// proceed as though the extension were not there.
	errCriticalExtension = errors.New("the assertion demands a critical extension this validator does not implement")

	// errSignatureInvalid is step 5, and the only thing that makes any of the
	// claims worth reading.
	errSignatureInvalid = errors.New("the assertion's signature does not verify")
)

// Validator is layer 1: the thing the browser door asks whether an assertion was
// written by the edge, and for whom.
//
// One per process. It holds the cached key set, so a validator built per request
// would fetch per request — exactly what FR-008 forbids.
type Validator struct {
	keys *keySet
}

// New builds the validator, and is the one place this package chooses the host
// clock — everything below it measures time on the clock it was given, which is
// what lets the refetch floor be tested without sleeping for a minute.
//
// It takes the team domain for the same reason newKeySet does: the issuer the
// assertion must name and the origin the keys come from are two readings of one
// configured value. The audience, the allowlist, and the time checks join here
// in T005 and T006.
func New(teamDomain string) (*Validator, error) {
	keys, err := newKeySet(teamDomain, systemClock{})
	if err != nil {
		return nil, err
	}
	return &Validator{keys: keys}, nil
}

// joseHeader is the two fields verification cannot proceed without, plus the one
// that forbids it proceeding at all.
//
// Unknown members are ignored rather than refused, unlike every request body
// this daemon decodes (docs/security.md §2): a real Access header carries `typ`,
// and RFC 7515 makes unrecognised-but-uncritical parameters something a verifier
// may pass over. `crit` is precisely the parameter that says otherwise, which is
// why it is named here.
type joseHeader struct {
	Alg string `json:"alg"`
	Kid string `json:"kid"`

	// Presence is the whole test, so this is a RawMessage rather than a
	// []string: `"crit": null` and `"crit": 7` are as much an announcement of an
	// extension as `"crit": ["b64"]` is, and decoding into a typed field would
	// let the first two through as an absent parameter.
	Crit json.RawMessage `json:"crit"`
}

// signedClaims runs steps 1 to 5 of the validation sequence
// (contracts/access-jwt.md) and returns the claim bytes the verified signature
// covers. It does not read them: what the claims say is steps 6 to 11, and what
// this returns is only the fact that the edge wrote them.
//
// The order is the contract, not an implementation detail. Two of its properties
// live in the ordering alone: the algorithm is settled before any cryptography
// runs, and the claims stay uninterpreted bytes until the signature says who
// wrote them. A parser is attack surface, and until step 5 passes the payload is
// attacker-authored.
func (v *Validator) signedClaims(ctx context.Context, assertion string) ([]byte, error) {
	// Step 1. The total header block is already bounded by milestone 1's 16 KiB
	// MaxHeaderBytes, so there is no size gate of its own here.
	if assertion == "" {
		return nil, errAssertionMissing
	}

	// Step 2. Shape first, so nothing below is handed something that was never a
	// JWS. Unpadded RawURLEncoding as RFC 7515 requires — a padded segment is a
	// token no compliant signer emits.
	parts := strings.Split(assertion, ".")
	if len(parts) != 3 || parts[0] == "" || parts[1] == "" || parts[2] == "" {
		return nil, fmt.Errorf("%w: it is not three non-empty segments", errAssertionMalformed)
	}
	joseBytes, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return nil, fmt.Errorf("%w: its JOSE header is not base64url", errAssertionMalformed)
	}
	claims, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, fmt.Errorf("%w: its payload is not base64url", errAssertionMalformed)
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return nil, fmt.Errorf("%w: its signature is not base64url", errAssertionMalformed)
	}

	// Step 3.
	var header joseHeader
	if err := json.Unmarshal(joseBytes, &header); err != nil {
		return nil, errJOSEHeaderMalformed
	}
	if header.Alg != algorithmRS256 {
		return nil, errAlgorithmRefused
	}
	if header.Crit != nil {
		return nil, errCriticalExtension
	}

	// Step 4. The key errors are already the sentinels the trail distinguishes,
	// and adding context to them here could only add the kid, which is
	// caller-authored and may never reach the trail.
	pub, err := v.keys.key(ctx, header.Kid)
	if err != nil {
		return nil, err
	}

	// Step 5. Over the first two segments exactly as they arrived — sliced out of
	// the assertion rather than rejoined from the parts, so no re-encoding can
	// come between what was signed and what is verified. A payload re-serialised
	// before verification is a payload the signature no longer covers.
	signed := assertion[:len(parts[0])+1+len(parts[1])]
	digest := sha256.Sum256([]byte(signed))
	if err := rsa.VerifyPKCS1v15(pub, crypto.SHA256, digest[:], signature); err != nil {
		// rsa's own error is dropped rather than wrapped: it is a single
		// constant carrying nothing this daemon does not already know.
		return nil, errSignatureInvalid
	}

	return claims, nil
}
