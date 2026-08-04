// An internal test (package access), like keys_test.go and verify_test.go: what
// this file asserts is which *reason* a genuine assertion was refused for, and
// the browser door's whole point is that a caller can never tell those apart.
package access

import (
	"context"
	"crypto/rsa"
	"encoding/json"
	"errors"
	"maps"
	"strings"
	"testing"
)

const (
	// testAUD is the tag the test validator is pinned to. Cloudflare's real ones
	// are 64 hex characters; the check is equality, so the shape is irrelevant
	// and a short one keeps the table readable.
	testAUD = "c0ffee"

	testEmail = "operator@example.com"
)

// identityMembers is the identity shape from research D2: what the edge mints
// when a person signs in with Google. It carries the members this daemon does
// not read — `type`, `identity_nonce`, `country`, `custom` — because passing
// over an unrecognised member is a property worth failing on, and a fixture
// trimmed to what the parser names could not catch it.
//
// The times sit either side of the clock every test validator starts at, so a
// case that does not name a time is valid by default.
func identityMembers(v *Validator) map[string]any {
	return map[string]any{
		"aud":            []any{testAUD},
		"iss":            v.issuer,
		"email":          testEmail,
		"sub":            "e7f9c3d1-0b2a-4c6e-9f8d-1a2b3c4d5e6f",
		"iat":            keysTimestamp - 300,
		"exp":            keysTimestamp + 3600,
		"type":           "app",
		"identity_nonce": "M0nc3",
		"country":        "GB",
		"custom":         map[string]any{"department": "operations"},
	}
}

// serviceTokenMembers is the other documented shape, and the one that matters:
// **every API call the operator's own client makes produces one of these** once
// the daemon is behind the edge. It is genuine, signed by the same key, minted
// for the same application — and it names a machine rather than a person.
func serviceTokenMembers(v *Validator) map[string]any {
	return map[string]any{
		"aud":         []any{testAUD},
		"iss":         v.issuer,
		"common_name": "1d4e0f5a6b7c8d9e.access",
		"sub":         "",
		"iat":         keysTimestamp - 300,
		"exp":         keysTimestamp + 3600,
		"type":        "app",
	}
}

// with returns the members with one added or replaced; without returns them with
// some removed. Both copy, so the rows of a table cannot edit each other's
// fixtures — and "absent" is a case only a map can express, which is why the
// payloads here are built from one rather than from a struct.
func with(members map[string]any, name string, value any) map[string]any {
	next := maps.Clone(members)
	next[name] = value
	return next
}

func without(members map[string]any, names ...string) map[string]any {
	next := maps.Clone(members)
	for _, name := range names {
		delete(next, name)
	}
	return next
}

func mintClaims(t *testing.T, key *rsa.PrivateKey, members map[string]any) string {
	t.Helper()

	payload, err := json.Marshal(members)
	if err != nil {
		t.Fatalf("marshalling the claims fixture: %v", err)
	}
	return mint(t, key, joseRS256, string(payload))
}

// mintPayload signs a payload that is not necessarily JSON at all, for the cases
// where the point is that step 6 meets something it cannot read.
func mintPayload(t *testing.T, key *rsa.PrivateKey, payload string) string {
	t.Helper()
	return mint(t, key, joseRS256, payload)
}

// TestClaimsAcceptAVerifiedIdentity is the positive case: a genuine identity
// assertion, carrying every member the edge really sends, yields the address the
// dashboard will greet the operator by.
func TestClaimsAcceptAVerifiedIdentity(t *testing.T) {
	t.Parallel()

	key := signingKey(t, 0)
	_, v := publishing(t, "k1", key)

	claims, err := v.verifiedClaims(context.Background(), mintClaims(t, key, identityMembers(v)))
	if err != nil {
		t.Fatalf("a genuine identity assertion was refused: %v", err)
	}
	if claims.Email != testEmail {
		t.Fatalf("Email = %q, want %q as the edge wrote it", claims.Email, testEmail)
	}
}

// TestClaimsRefuseAnAssertionThatNamesNoPerson is FR-013c, and the reason step
// 10 is stated as a requirement rather than as an objection.
//
// The first case is the one that decides it. Nothing about that assertion is
// forged: the edge signed it, for this application, minutes ago. It is what the
// operator's own API client presents on every call. A validator written as
// "refuse an email that is present and not allowed" admits it to the dashboard —
// and passes every other test in this package, because none of the rest presents
// an assertion with no email at all.
func TestClaimsRefuseAnAssertionThatNamesNoPerson(t *testing.T) {
	t.Parallel()

	key := signingKey(t, 0)
	_, v := publishing(t, "k1", key)

	cases := []struct {
		name    string
		members map[string]any
	}{
		{"a valid service-token assertion, presented to the dashboard", serviceTokenMembers(v)},
		{"no email member at all", without(identityMembers(v), "email")},
		{"an empty email", with(identityMembers(v), "email", "")},
		// Blank is empty: config refuses an allowlist entry containing
		// whitespace, so this address could never match one.
		{"a blank email", with(identityMembers(v), "email", "   ")},
		{"a null email", with(identityMembers(v), "email", nil)},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			claims, err := v.verifiedClaims(context.Background(), mintClaims(t, key, tc.members))
			if claims != nil || !errors.Is(err, errNoEmail) {
				t.Fatalf("verifiedClaims = %+v, %v; want no claims and errNoEmail", claims, err)
			}
		})
	}
}

// TestClaimsPinTheAudience is FR-003. The edge's signing keys are per *account*,
// so without this check an assertion minted for any other application the same
// account protects verifies here perfectly.
func TestClaimsPinTheAudience(t *testing.T) {
	t.Parallel()

	key := signingKey(t, 0)
	_, v := publishing(t, "k1", key)
	base := identityMembers(v)

	cases := []struct {
		name    string
		members map[string]any
		want    error
	}{
		{"the array form the edge issues", with(base, "aud", []any{testAUD}), nil},
		// RFC 7519 allows a bare string, and equality against the pinned tag is
		// the check either way.
		{"a bare string", with(base, "aud", testAUD), nil},
		{"one tag among several", with(base, "aud", []any{"1dea", testAUD, "d0d0"}), nil},
		{"another application in the same account", with(base, "aud", []any{"deadbeef"}), errAudienceMismatch},
		{"an empty array", with(base, "aud", []any{}), errAudienceMismatch},
		{"a prefix of the configured tag", with(base, "aud", []any{testAUD[:len(testAUD)-1]}), errAudienceMismatch},
		{"the configured tag with a suffix", with(base, "aud", []any{testAUD + "0"}), errAudienceMismatch},
		{"absent", without(base, "aud"), errAudienceMismatch},
		{"null", with(base, "aud", nil), errAudienceMismatch},
		{"an empty string", with(base, "aud", ""), errAudienceMismatch},
		// Not an audience in either documented form. Reading it as "no
		// audience" would turn a malformed assertion into an ordinary mismatch,
		// which is a difference the journal should keep.
		{"a number", with(base, "aud", 7), errClaimsMalformed},
		{"an object", with(base, "aud", map[string]any{"tag": testAUD}), errClaimsMalformed},
		{"an array of numbers", with(base, "aud", []any{7}), errClaimsMalformed},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			claims, err := v.verifiedClaims(context.Background(), mintClaims(t, key, tc.members))
			if tc.want == nil {
				if err != nil {
					t.Fatalf("verifiedClaims refused an assertion for this application: %v", err)
				}
				return
			}
			if claims != nil || !errors.Is(err, tc.want) {
				t.Fatalf("verifiedClaims = %+v, %v; want no claims and %v", claims, err, tc.want)
			}
		})
	}
}

// TestClaimsPinTheIssuer is FR-005: the issuer must be the configured team
// domain exactly, and "exactly" is compared byte for byte against the origin the
// key set itself fetches from.
func TestClaimsPinTheIssuer(t *testing.T) {
	t.Parallel()

	key := signingKey(t, 0)
	_, v := publishing(t, "k1", key)
	base := identityMembers(v)

	cases := []struct {
		name    string
		members map[string]any
		want    error
	}{
		{"another team's domain", with(base, "iss", "https://other.cloudflareaccess.com"), errIssuerMismatch},
		{"absent", without(base, "iss"), errIssuerMismatch},
		{"empty", with(base, "iss", ""), errIssuerMismatch},
		// Near misses, all refused: the comparison is equality, not a prefix, a
		// host match, or a case-folded reading.
		{"the same origin with a trailing slash", with(base, "iss", v.issuer+"/"), errIssuerMismatch},
		{"the same origin with a path", with(base, "iss", v.issuer+"/cdn-cgi/access"), errIssuerMismatch},
		{"the same origin in upper case", with(base, "iss", strings.ToUpper(v.issuer)), errIssuerMismatch},
		{"a number", with(base, "iss", 7), errClaimsMalformed},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			claims, err := v.verifiedClaims(context.Background(), mintClaims(t, key, tc.members))
			if claims != nil || !errors.Is(err, tc.want) {
				t.Fatalf("verifiedClaims = %+v, %v; want no claims and %v", claims, err, tc.want)
			}
		})
	}
}

// TestClaimsRefuseAnAssertionOutsideItsValidity is FR-006, in both directions,
// with the fixed 60-second leeway exercised at its edges.
//
// Every time here is expressed against keysTimestamp, which is what the test
// validator's clock reads: the leeway is a boundary, and a boundary asserted
// only from a distance is a boundary that can move by a minute unnoticed.
func TestClaimsRefuseAnAssertionOutsideItsValidity(t *testing.T) {
	t.Parallel()

	key := signingKey(t, 0)
	_, v := publishing(t, "k1", key)
	base := identityMembers(v)

	cases := []struct {
		name    string
		members map[string]any
		want    error
	}{
		{"expired an hour ago", with(base, "exp", keysTimestamp-3600), errExpired},
		{"expired exactly one leeway ago", with(base, "exp", keysTimestamp-60), errExpired},
		{"expired one second inside the leeway", with(base, "exp", keysTimestamp-59), nil},
		{"expiring this second", with(base, "exp", keysTimestamp), nil},
		// An assertion that never expires is not an assertion this daemon can
		// show to be current, and both documented shapes carry an expiry.
		{"no expiry at all", without(base, "exp"), errNoExpiry},
		{"a null expiry", with(base, "exp", nil), errNoExpiry},

		{"not valid until tomorrow", with(base, "nbf", keysTimestamp+86400), errNotYetValid},
		{"not valid until one second past the leeway", with(base, "nbf", keysTimestamp+61), errNotYetValid},
		{"not valid until exactly the leeway", with(base, "nbf", keysTimestamp+60), nil},
		{"valid since yesterday", with(base, "nbf", keysTimestamp-86400), nil},

		{"issued tomorrow", with(base, "iat", keysTimestamp+86400), errIssuedInTheFuture},
		{"issued one second past the leeway", with(base, "iat", keysTimestamp+61), errIssuedInTheFuture},
		{"issued exactly at the leeway", with(base, "iat", keysTimestamp+60), nil},

		// Absent is not a violation of "not in the future", unlike a missing
		// expiry, which defeats a check that has to pass.
		{"neither nbf nor iat", without(base, "nbf", "iat"), nil},

		// RFC 7519 permits a fractional NumericDate; the edge has never emitted
		// one, and refusing is the safe direction of that difference.
		{"a fractional expiry", with(base, "exp", 1785710000.5), errClaimsMalformed},
		{"an expiry as a string", with(base, "exp", "1785710000"), errClaimsMalformed},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			claims, err := v.verifiedClaims(context.Background(), mintClaims(t, key, tc.members))
			if tc.want == nil {
				if err != nil {
					t.Fatalf("verifiedClaims refused an assertion inside its validity: %v", err)
				}
				return
			}
			if claims != nil || !errors.Is(err, tc.want) {
				t.Fatalf("verifiedClaims = %+v, %v; want no claims and %v", claims, err, tc.want)
			}
		})
	}
}

// TestClaimsRefuseAMalformedPayload is step 6: the first moment anything reads
// the payload, and the point at which a genuinely signed assertion can still be
// something this validator will not interpret.
func TestClaimsRefuseAMalformedPayload(t *testing.T) {
	t.Parallel()

	key := signingKey(t, 0)
	_, v := publishing(t, "k1", key)

	cases := []struct {
		name    string
		payload string
		want    error
	}{
		{"not JSON at all", "this is not JSON, and step 6 does care", errClaimsMalformed},
		{"whitespace", "   ", errClaimsMalformed},
		{"a JSON array", `[{"email":"operator@example.com"}]`, errClaimsMalformed},
		{"a bare string", `"operator@example.com"`, errClaimsMalformed},
		{"a number", `7`, errClaimsMalformed},
		{"truncated", `{"email":"operator@example.com"`, errClaimsMalformed},
		{"trailing content after the object", `{"email":"operator@example.com"} and more`, errClaimsMalformed},
		// A payload of `null` decodes into a claim set that claims nothing,
		// which is not malformed — it is an assertion naming no issuer, and the
		// first check it fails says so. Refused either way; the trail keeps the
		// distinction.
		{"null", `null`, errIssuerMismatch},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			claims, err := v.verifiedClaims(context.Background(), mintPayload(t, key, tc.payload))
			if claims != nil || !errors.Is(err, tc.want) {
				t.Fatalf("verifiedClaims = %+v, %v; want no claims and %v", claims, err, tc.want)
			}
		})
	}
}

// TestClaimsAreReadOnlyAfterTheSignature is the ordering half of the sequence,
// asserted from above: a payload that would fail three of the claim checks is
// answered by the signature, because until step 5 passes nothing has read a
// claim at all.
//
// Without this, steps 6 to 10 could quietly move ahead of step 5 and every other
// test in the file would still pass — a refusal is a refusal, whichever check
// produced it, and the one thing that distinguishes them is what got parsed.
func TestClaimsAreReadOnlyAfterTheSignature(t *testing.T) {
	t.Parallel()

	published, other := signingKey(t, 0), signingKey(t, 1)
	_, v := publishing(t, "k1", published)

	members := identityMembers(v)
	members["iss"] = "https://attacker.example.com"
	members["aud"] = []any{"deadbeef"}
	members["exp"] = keysTimestamp - 3600
	delete(members, "email")

	claims, err := v.verifiedClaims(context.Background(), mintClaims(t, other, members))
	if claims != nil || !errors.Is(err, errSignatureInvalid) {
		t.Fatalf("verifiedClaims = %+v, %v; want no claims and errSignatureInvalid — the claims must not be read before the signature verifies", claims, err)
	}
}
