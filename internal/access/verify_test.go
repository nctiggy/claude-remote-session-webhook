// An internal test (package access), like keys_test.go: half of what this file
// asserts is an *ordering* — that no key was fetched before the algorithm was
// settled, that the claims came back unread — and an ordering is not visible
// through an exported surface that only reports the verdict.
package access

import (
	"context"
	"crypto"
	"crypto/hmac"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"strings"
	"testing"
)

// joseRS256 is the JOSE header the edge writes, `typ` included: an unrecognised
// member must be passed over rather than refused, or the real assertion is the
// one this validator rejects.
const joseRS256 = `{"alg":"RS256","kid":"k1","typ":"JWT"}`

// identityClaims is the identity shape from research D2, spelled out because
// something has to sit in the payload. Nothing in this file reads it — the
// claims are steps 6 to 11, and this file ends at step 5 — so its only property
// that matters is that the bytes come back exactly as they went in.
const identityClaims = `{"aud":["c0ffee"],"iss":"https://team.cloudflareaccess.com","email":"operator@example.com","sub":"u1","iat":1785706400,"exp":1785710000}`

// mint assembles and signs an assertion segment by segment rather than through
// any helper the code under test shares, so a case can produce shapes no correct
// signer would emit — a header claiming `none`, a payload that is not JSON.
func mint(t *testing.T, key *rsa.PrivateKey, header, claims string) string {
	t.Helper()

	signed := b64url([]byte(header)) + "." + b64url([]byte(claims))
	digest := sha256.Sum256([]byte(signed))
	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, digest[:])
	if err != nil {
		t.Fatalf("signing the assertion: %v", err)
	}
	return signed + "." + b64url(sig)
}

// mintHS256 is the key-confusion break in fixture form: the RSA *public* key,
// in the PEM form every deployment publishes, used as an HMAC secret. Against a
// verifier that dispatches on the token's own `alg`, this is a valid signature
// produced entirely from public information.
func mintHS256(t *testing.T, pub *rsa.PublicKey, header, claims string) string {
	t.Helper()

	der, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		t.Fatalf("marshalling the public key: %v", err)
	}
	secret := pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der})

	signed := b64url([]byte(header)) + "." + b64url([]byte(claims))
	mac := hmac.New(sha256.New, secret)
	if _, err := mac.Write([]byte(signed)); err != nil {
		t.Fatalf("computing the HMAC: %v", err)
	}
	return signed + "." + b64url(mac.Sum(nil))
}

// newTestValidator wires a validator to a key server, bypassing New so the
// clock stays under the test's control. New's own wiring is asserted separately.
//
// The issuer is read back off the key set rather than restated, exactly as New
// does it: a fixture that spelled the origin itself could pass while production
// derived a different one.
func newTestValidator(t *testing.T, srv *keyServer, clk clock) *Validator {
	t.Helper()

	keys := mustKeySet(t, srv.URL(), clk)
	return &Validator{keys: keys, issuer: keys.origin, aud: testAUD, clock: clk}
}

// publishing returns a key server holding the one key the daemon trusts, and a
// validator that reads from it.
func publishing(t *testing.T, kid string, key *rsa.PrivateKey) (*keyServer, *Validator) {
	t.Helper()

	srv := newKeyServer(t, keySetJSON(jwkFor(t, kid, &key.PublicKey)))
	return srv, newTestValidator(t, srv, newStepClock())
}

// TestVerifyAcceptsAnAssertionTheEdgeSigned is the positive case: a genuine
// assertion verifies, and what comes back is the payload untouched.
func TestVerifyAcceptsAnAssertionTheEdgeSigned(t *testing.T) {
	t.Parallel()

	key := signingKey(t, 0)
	srv, v := publishing(t, "k1", key)

	claims, err := v.signedClaims(context.Background(), mint(t, key, joseRS256, identityClaims))
	if err != nil {
		t.Fatalf("a genuine assertion was refused: %v", err)
	}
	if string(claims) != identityClaims {
		t.Fatalf("signedClaims returned %q, want the payload as signed %q", claims, identityClaims)
	}

	// The second call proves the key set is the validator's, not the request's:
	// FR-008 forbids a fetch per request, and a validator built per request
	// would fetch per request whatever the cache does.
	if _, err := v.signedClaims(context.Background(), mint(t, key, joseRS256, identityClaims)); err != nil {
		t.Fatalf("the second assertion was refused: %v", err)
	}
	if n := srv.fetchCount(); n != 1 {
		t.Fatalf("two verifications produced %d fetches, want 1", n)
	}
}

// TestVerifySignatureCoversTheBytesAsReceived: the digest is taken over the two
// segments exactly as they arrived. A verifier that decoded the JOSE header and
// re-encoded it before hashing would refuse this — the spacing is preserved by
// the signer and lost by any round trip through a JSON encoder.
func TestVerifySignatureCoversTheBytesAsReceived(t *testing.T) {
	t.Parallel()

	key := signingKey(t, 0)
	_, v := publishing(t, "k1", key)

	spaced := `{ "typ" : "JWT" ,  "alg" : "RS256" ,  "kid" : "k1" }`
	claims := "{\n  \"email\" : \"operator@example.com\"\n}"

	got, err := v.signedClaims(context.Background(), mint(t, key, spaced, claims))
	if err != nil {
		t.Fatalf("an assertion whose header is not canonical JSON was refused: %v", err)
	}
	if string(got) != claims {
		t.Fatalf("signedClaims returned %q, want the payload byte for byte %q", got, claims)
	}
}

// TestVerifyDoesNotReadTheClaims is step 6's half of the ordering, asserted from
// below: the payload is handed back unparsed. Until the signature verified it
// was attacker-authored, and a parser is attack surface — so nothing here reads
// it, and a payload that is not JSON at all still verifies.
func TestVerifyDoesNotReadTheClaims(t *testing.T) {
	t.Parallel()

	key := signingKey(t, 0)
	_, v := publishing(t, "k1", key)

	nonsense := "this is not JSON, and step 5 does not care"

	got, err := v.signedClaims(context.Background(), mint(t, key, joseRS256, nonsense))
	if err != nil {
		t.Fatalf("verification read the claims: %v", err)
	}
	if string(got) != nonsense {
		t.Fatalf("signedClaims returned %q, want %q", got, nonsense)
	}
}

// TestVerifyRefusesAMalformedAssertion is step 2, plus the JSON half of step 3.
// Nothing here reaches a key: malformed input is refused before any of it is
// interpreted, and an unparseable assertion must not cost an outbound fetch.
func TestVerifyRefusesAMalformedAssertion(t *testing.T) {
	t.Parallel()

	key := signingKey(t, 0)
	srv, v := publishing(t, "k1", key)

	good := mint(t, key, joseRS256, identityClaims)
	parts := strings.Split(good, ".")

	padded := base64.URLEncoding.EncodeToString([]byte(joseRS256))
	if !strings.HasSuffix(padded, "=") {
		t.Fatalf("the padded fixture no longer pads: %q", padded)
	}

	cases := []struct {
		name      string
		assertion string
		want      error
	}{
		{"empty", "", errAssertionMissing},
		{"no segments at all", "not a jws", errAssertionMalformed},
		{"one segment", parts[0], errAssertionMalformed},
		{"two segments", parts[0] + "." + parts[1], errAssertionMalformed},
		{"four segments", good + "." + parts[2], errAssertionMalformed},
		{"dots only", "..", errAssertionMalformed},
		{"empty header segment", "." + parts[1] + "." + parts[2], errAssertionMalformed},
		{"empty payload segment", parts[0] + ".." + parts[2], errAssertionMalformed},
		// The unsigned JWS an `alg: none` forgery is actually written as. It is
		// refused here, by shape, before the algorithm is ever read — the alg
		// gate below covers the same forgery carrying a signature.
		{"empty signature segment", parts[0] + "." + parts[1] + ".", errAssertionMalformed},
		{"header is not base64url", "not base64!." + parts[1] + "." + parts[2], errAssertionMalformed},
		{"payload is not base64url", parts[0] + ".not base64!." + parts[2], errAssertionMalformed},
		{"signature is not base64url", parts[0] + "." + parts[1] + ".not base64!", errAssertionMalformed},
		{"header is padded base64", padded + "." + parts[1] + "." + parts[2], errAssertionMalformed},
		{"header is not JSON", b64url([]byte("RS256")) + "." + parts[1] + "." + parts[2], errJOSEHeaderMalformed},
		{"header is a JSON array", b64url([]byte(`["RS256"]`)) + "." + parts[1] + "." + parts[2], errJOSEHeaderMalformed},
		{"alg is not a string", b64url([]byte(`{"alg":256,"kid":"k1"}`)) + "." + parts[1] + "." + parts[2], errJOSEHeaderMalformed},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			claims, err := v.signedClaims(context.Background(), tc.assertion)
			if claims != nil || !errors.Is(err, tc.want) {
				t.Fatalf("signedClaims = %q, %v; want no claims and %v", claims, err, tc.want)
			}
		})
	}

	if n := srv.fetchCount(); n != 0 {
		t.Fatalf("%d assertions that were never a JWS produced %d fetches, want 0", len(cases), n)
	}
}

// TestVerifyRefusesEveryAlgorithmButRS256 is FR-004, and the reason this
// validator is hand-written.
//
// Every case here is a genuine attempt, signed the way its header claims where
// that is possible at all. None of them resolves a key: the algorithm is settled
// before any cryptography runs, which is what makes `alg: none` and RS256/HS256
// confusion unexpressible rather than merely handled.
func TestVerifyRefusesEveryAlgorithmButRS256(t *testing.T) {
	t.Parallel()

	key := signingKey(t, 0)
	srv, v := publishing(t, "k1", key)

	cases := []struct {
		name      string
		assertion string
	}{
		{"none, carrying a real signature", mint(t, key, `{"alg":"none","kid":"k1"}`, identityClaims)},
		{"none, in upper case", mint(t, key, `{"alg":"None","kid":"k1"}`, identityClaims)},
		// The break in one line: signed with nothing but the published public
		// key, and accepted by any verifier that lets the token pick.
		{"HS256, signed with the public key as the secret", mintHS256(t, &key.PublicKey, `{"alg":"HS256","kid":"k1"}`, identityClaims)},
		{"RS384", mint(t, key, `{"alg":"RS384","kid":"k1"}`, identityClaims)},
		{"RS512", mint(t, key, `{"alg":"RS512","kid":"k1"}`, identityClaims)},
		{"ES256", mint(t, key, `{"alg":"ES256","kid":"k1"}`, identityClaims)},
		{"PS256", mint(t, key, `{"alg":"PS256","kid":"k1"}`, identityClaims)},
		// Byte for byte: the comparison is against a constant, not a
		// case-folded or trimmed reading of one.
		{"rs256 in lower case", mint(t, key, `{"alg":"rs256","kid":"k1"}`, identityClaims)},
		{"RS256 with a leading space", mint(t, key, `{"alg":" RS256","kid":"k1"}`, identityClaims)},
		{"no alg at all", mint(t, key, `{"kid":"k1"}`, identityClaims)},
		{"an empty alg", mint(t, key, `{"alg":"","kid":"k1"}`, identityClaims)},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			claims, err := v.signedClaims(context.Background(), tc.assertion)
			if claims != nil || !errors.Is(err, errAlgorithmRefused) {
				t.Fatalf("signedClaims = %q, %v; want no claims and errAlgorithmRefused", claims, err)
			}
		})
	}

	if n := srv.fetchCount(); n != 0 {
		t.Fatalf("refusing %d algorithms cost %d key fetches, want 0 — alg is settled before any key is resolved", len(cases), n)
	}
}

// TestVerifyRefusesACriticalExtension: `crit` announces an extension the signer
// requires its verifier to implement. This one implements none, so RFC 7515
// makes refusing the only correct answer — proceeding as though the parameter
// were absent is the failure mode.
func TestVerifyRefusesACriticalExtension(t *testing.T) {
	t.Parallel()

	key := signingKey(t, 0)
	srv, v := publishing(t, "k1", key)

	cases := []struct{ name, crit string }{
		{"a named extension", `["b64"]`},
		{"several", `["b64","exp"]`},
		// Present but empty, and present but not a list: both announce an
		// extension, and a typed field would have read them as absent.
		{"empty", `[]`},
		{"null", `null`},
		{"not a list", `7`},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			header := `{"alg":"RS256","kid":"k1","crit":` + tc.crit + `}`

			claims, err := v.signedClaims(context.Background(), mint(t, key, header, identityClaims))
			if claims != nil || !errors.Is(err, errCriticalExtension) {
				t.Fatalf("signedClaims = %q, %v; want no claims and errCriticalExtension", claims, err)
			}
		})
	}

	if n := srv.fetchCount(); n != 0 {
		t.Fatalf("refusing a crit parameter cost %d key fetches, want 0", n)
	}
}

// TestVerifyRefusesAForgedSignature covers the two ways a signature fails to be
// the edge's: a payload edited after signing, and a key that is not the one the
// edge publishes.
func TestVerifyRefusesAForgedSignature(t *testing.T) {
	t.Parallel()

	published, other := signingKey(t, 0), signingKey(t, 1)
	_, v := publishing(t, "k1", published)

	genuine := strings.Split(mint(t, published, joseRS256, identityClaims), ".")
	tampered := genuine[0] + "." + b64url([]byte(`{"email":"attacker@example.com"}`)) + "." + genuine[2]

	cases := []struct{ name, assertion string }{
		{"the payload was edited after signing", tampered},
		// The header the edge would have written, naming the key id the edge
		// publishes — signed by a key pair that is not the edge's.
		{"signed by a key the edge does not publish", mint(t, other, joseRS256, identityClaims)},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			claims, err := v.signedClaims(context.Background(), tc.assertion)
			if claims != nil || !errors.Is(err, errSignatureInvalid) {
				t.Fatalf("signedClaims = %q, %v; want no claims and errSignatureInvalid", claims, err)
			}
		})
	}
}

// TestVerifyRefusesAnAssertionNamingNoUsableKey is step 4 reached through the
// verifier: the key set's rules are its own, and what matters here is that a
// failure to resolve a key is a refusal rather than a shortcut past step 5.
func TestVerifyRefusesAnAssertionNamingNoUsableKey(t *testing.T) {
	t.Parallel()

	key := signingKey(t, 0)
	srv := newKeyServer(t, keySetJSON(jwkFor(t, "k1", &key.PublicKey)))
	clk := newStepClock()
	v := newTestValidator(t, srv, clk)

	// Warm the cache, so the unknown-kid case below is a miss rather than a cold
	// start, then step past the refetch floor so that case gets its one fetch.
	// Inside the floor it would be refused without one, which is the key set's
	// own property and keys_test.go's to assert.
	if _, err := v.signedClaims(context.Background(), mint(t, key, joseRS256, identityClaims)); err != nil {
		t.Fatalf("the genuine assertion was refused: %v", err)
	}
	clk.Advance(refetchFloor)

	cases := []struct {
		name    string
		header  string
		want    error
		fetches int
	}{
		{"no kid", `{"alg":"RS256","typ":"JWT"}`, errKeyIDMissing, 1},
		{"an empty kid", `{"alg":"RS256","kid":"","typ":"JWT"}`, errKeyIDMissing, 1},
		// One refetch, then a refusal — the rotation the miss might have been
		// is checked for exactly once, and not finding it is not an admission.
		{"a kid the edge does not publish", `{"alg":"RS256","kid":"k9","typ":"JWT"}`, errKeyIDUnknown, 2},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			claims, err := v.signedClaims(context.Background(), mint(t, key, tc.header, identityClaims))
			if claims != nil || !errors.Is(err, tc.want) {
				t.Fatalf("signedClaims = %q, %v; want no claims and %v", claims, err, tc.want)
			}
			if n := srv.fetchCount(); n != tc.fetches {
				t.Fatalf("%d fetches in total, want %d", n, tc.fetches)
			}
		})
	}
}

// TestVerifyFailsClosedWhenTheKeysAreUnobtainable is FR-009 through the
// verifier: a signature that cannot be checked is not a signature that passed.
func TestVerifyFailsClosedWhenTheKeysAreUnobtainable(t *testing.T) {
	t.Parallel()

	key := signingKey(t, 0)
	srv := newKeyServer(t, keySetJSON(jwkFor(t, "k1", &key.PublicKey)))
	url := srv.URL()
	srv.srv.Close() // nothing is listening from here on

	v := &Validator{keys: mustKeySet(t, url, newStepClock())}

	claims, err := v.signedClaims(context.Background(), mint(t, key, joseRS256, identityClaims))
	if claims != nil || !errors.Is(err, errKeysUnobtainable) {
		t.Fatalf("signedClaims with the key server down = %q, %v; want no claims and errKeysUnobtainable", claims, err)
	}
}

// TestNewWiresTheKeySet: the constructor the daemon will call builds a validator
// that verifies a real assertion against a real key set. Without this, the
// package is implemented and tested and never actually assembled — which is how
// milestone 1 shipped a reaper nothing ran.
func TestNewWiresTheKeySet(t *testing.T) {
	t.Parallel()

	key := signingKey(t, 0)
	srv := newKeyServer(t, keySetJSON(jwkFor(t, "k1", &key.PublicKey)))

	v, err := New(srv.URL(), testAUD)
	if err != nil {
		t.Fatalf("New(%q): %v", srv.URL(), err)
	}

	claims, err := v.signedClaims(context.Background(), mint(t, key, joseRS256, identityClaims))
	if err != nil {
		t.Fatalf("a validator from New refused a genuine assertion: %v", err)
	}
	if string(claims) != identityClaims {
		t.Fatalf("signedClaims returned %q, want %q", claims, identityClaims)
	}
}

// TestNewRefusesAnUnusableTeamDomain: a validator that cannot reach a key set is
// a startup failure, not a validator that refuses everything at runtime (FR-011).
func TestNewRefusesAnUnusableTeamDomain(t *testing.T) {
	t.Parallel()

	for _, domain := range []string{"", "team.cloudflareaccess.com", "https://", "ftp://team.cloudflareaccess.com"} {
		v, err := New(domain, testAUD)
		if err == nil {
			t.Fatalf("New(%q, %q) built %v, want a refusal", domain, testAUD, v)
		}
	}
}

// TestNewRefusesAnEmptyAudience is the same rule for the value with no
// derivation to fall back on: an empty audience compares equal to an assertion
// carrying an empty tag, so it is a pin that pins nothing (FR-011).
func TestNewRefusesAnEmptyAudience(t *testing.T) {
	t.Parallel()

	if v, err := New("https://team.cloudflareaccess.com", ""); err == nil {
		t.Fatalf("New with no audience built %v, want a refusal", v)
	}
}
