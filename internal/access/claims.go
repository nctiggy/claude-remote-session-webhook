package access

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"time"
)

// clockLeeway is how far apart the edge's clock and this host's may be before a
// perfectly good assertion starts being refused, and it is fixed rather than
// configurable.
//
// Drift between two machines is real, so zero leeway would refuse assertions
// that are merely a second early. Anything wider extends the life of every token
// the edge mints, in both directions: a minute of leeway is a minute of extra
// validity after expiry. Milestone 1's rule applies — do not widen a window to
// fix a clock, fix the clock.
const clockLeeway = 60 * time.Second

// The reasons a verified assertion is still not this application's operator.
// Every one of them is reached only after the signature proved the edge wrote
// these claims, so unlike the errors above them these describe a genuine
// assertion that does not apply here.
//
// They are recorded server-side and never reach a caller — the contract's single
// uniform 401 covers all of them — and none carries a claim value. The audience,
// the issuer and the address are the edge's words about a person; an error
// string here is written to the journal.
var (
	// errClaimsMalformed is step 6, and the first time anything reads the
	// payload at all. Until step 5 passed it was attacker-authored bytes.
	errClaimsMalformed = errors.New("the assertion's claims are not the documented JSON")

	// errIssuerMismatch is step 7: the edge's key verified it, but the assertion
	// names another authority. In practice this is a misconfiguration rather
	// than an attack, and it is refused all the same.
	errIssuerMismatch = errors.New("the assertion names an issuer that is not the configured team domain")

	// errAudienceMismatch is step 8, and the check that makes the account's
	// shared signing keys specific to this application. Without it every
	// assertion Cloudflare mints for any other app in the account validates.
	errAudienceMismatch = errors.New("the assertion was minted for another application")

	// errNoExpiry is step 9's precondition. An assertion carrying no expiry
	// cannot be shown to be unexpired, and both documented shapes carry one, so
	// its absence is a refusal rather than an unbounded pass.
	errNoExpiry = errors.New("the assertion carries no expiry")

	errExpired = errors.New("the assertion has expired")

	errNotYetValid = errors.New("the assertion's validity has not begun")

	errIssuedInTheFuture = errors.New("the assertion was issued in the future")

	// errNoEmail is step 10, and the whole of FR-013c. This is the service-token
	// shape — the one the operator's own API client produces on every call — and
	// it identifies a machine to the edge, not a person to the dashboard.
	errNoEmail = errors.New("the assertion carries no email address, so it identifies no person")
)

// claimSet is the members of an Access assertion this daemon reads, and nothing
// else. A real assertion also carries `type`, `identity_nonce`, `country` and
// `custom`; those are passed over for the reason jwkSet and joseHeader pass over
// what they do not name, and unlike a request body (docs/security.md §2) an
// unknown member here is the provider's business, not a caller's smuggled input.
//
// `sub` and `common_name` are deliberately absent, though research D2 documents
// both and they are what actually distinguishes the two assertion shapes.
// Reading them would invite the rule to be written as a discrimination between
// shapes — "if it has a common_name, refuse it" — and a shape test only refuses
// the shapes it was taught. The rule is a requirement on the email, so the email
// is the only thing the check reads.
type claimSet struct {
	Iss   string   `json:"iss"`
	Aud   audience `json:"aud"`
	Email string   `json:"email"`

	// Seconds since the epoch, as pointers because absent and zero are different
	// answers: 1970 is an expiry that has passed, while a missing `exp` is an
	// assertion that never expires, and only one of those may be treated as an
	// expiry at all.
	//
	// A fractional NumericDate fails to decode into these and is refused as a
	// malformed payload. RFC 7519 permits one; the edge has never emitted one,
	// and refusing is the safe direction of that difference.
	Exp *int64 `json:"exp"`
	Nbf *int64 `json:"nbf"`
	Iat *int64 `json:"iat"`
}

// audience is `aud` in both the forms RFC 7519 allows. Cloudflare issues an
// array; a bare string is accepted too, because the check either way is equality
// against the one pinned tag — never a parse of what the audience means.
type audience []string

// UnmarshalJSON reads the array form and the single-string form, and refuses
// everything else by leaving the decode to fail. A number or an object is not an
// audience, and silently reading it as none would turn a malformed assertion
// into an ordinary mismatch.
func (a *audience) UnmarshalJSON(data []byte) error {
	var one string
	if err := json.Unmarshal(data, &one); err == nil {
		*a = audience{one}
		return nil
	}

	var many []string
	if err := json.Unmarshal(data, &many); err != nil {
		// Answered rather than wrapped: encoding/json's message names the Go
		// type it could not build, which tells an operator nothing this sentinel
		// does not, and the outer decode turns it into errClaimsMalformed.
		return errClaimsMalformed
	}
	*a = many
	return nil
}

// verifiedClaims is the whole documented sequence — steps 1 to 10 of
// contracts/access-jwt.md — and the only way anything gets a claim out of an
// assertion. Steps 1 to 5 are signedClaims's; this adds the six checks that ask
// whether the identity the edge vouched for is one this daemon serves.
//
// The composition is the ordering property: claims are read only after the
// signature says who wrote them, and there is no second path that reads them
// earlier. Step 11 — the allowlist — is Verify's, which composes this the same
// way and is the only exported way in.
func (v *Validator) verifiedClaims(ctx context.Context, assertion string) (*claimSet, error) {
	payload, err := v.signedClaims(ctx, assertion)
	if err != nil {
		return nil, err
	}

	// Step 6.
	var claims claimSet
	if err := json.Unmarshal(payload, &claims); err != nil {
		return nil, errClaimsMalformed
	}
	if err := v.applies(&claims); err != nil {
		return nil, err
	}
	return &claims, nil
}

// applies is steps 7 to 10: the assertion is genuine, and these ask whether it
// was written about this application, is valid now, and identifies a person.
func (v *Validator) applies(claims *claimSet) error {
	// Step 7. Exact equality with the origin the keys themselves came from.
	if claims.Iss != v.issuer {
		return errIssuerMismatch
	}

	// Step 8. The signing keys are per-account, so this is the only check that
	// distinguishes an assertion minted for this application from one minted for
	// any other application the same account protects.
	if !slices.Contains(claims.Aud, v.aud) {
		return errAudienceMismatch
	}

	// Step 9.
	if err := v.valid(claims); err != nil {
		return err
	}

	// Step 10, stated positively: a non-empty email is *required*. The inverted
	// spelling — refuse an email that is present and disallowed — passes every
	// test that only presents an identity assertion, and admits every
	// service-token assertion the operator's own client produces, because there
	// is no email for it to object to (FR-013c).
	//
	// Blank-only is empty: the address is compared against an allowlist whose
	// loader refuses an entry containing whitespace, so it could never match one
	// and is a missing email written a different way.
	if strings.TrimSpace(claims.Email) == "" {
		return errNoEmail
	}

	return nil
}

// valid is step 9, in both directions (FR-006). An assertion that has expired is
// refused, and so is one whose validity has not begun — a token minted for later
// is not a token that is good now, and accepting it would let a clock that jumps
// backwards admit it twice.
func (v *Validator) valid(claims *claimSet) error {
	now := v.clock.Now()

	if claims.Exp == nil {
		return errNoExpiry
	}
	if !now.Before(time.Unix(*claims.Exp, 0).Add(clockLeeway)) {
		return errExpired
	}

	// nbf and iat are checked when present, unlike exp: the edge omits both from
	// some assertions, and neither absence defeats a check the way a missing
	// expiry does. "Not in the future" is satisfied by a value that is not there.
	if claims.Nbf != nil && now.Before(time.Unix(*claims.Nbf, 0).Add(-clockLeeway)) {
		return errNotYetValid
	}
	if claims.Iat != nil && time.Unix(*claims.Iat, 0).After(now.Add(clockLeeway)) {
		return errIssuedInTheFuture
	}

	return nil
}
