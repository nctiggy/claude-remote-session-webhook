// An internal test (package access), like the rest of this package: what it
// asserts is which *reason* an assertion was refused for, and the browser door's
// whole point is that a caller can never tell those apart.
package access

import (
	"context"
	"crypto/rsa"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/nctiggy/claude-remote-session-webhook/internal/auth"
)

// mustAllowlist is the fixture newTestValidator wires, built through
// newAllowlist rather than as a map literal so a test list is normalised exactly
// as a configured one is.
func mustAllowlist(t *testing.T, addresses ...string) allowlist {
	t.Helper()

	list, err := newAllowlist(addresses)
	if err != nil {
		t.Fatalf("newAllowlist(%q): %v", addresses, err)
	}
	return list
}

// admitting returns a validator holding the one published key and an allowlist
// of exactly the addresses given.
func admitting(t *testing.T, key *rsa.PrivateKey, addresses ...string) *Validator {
	t.Helper()

	_, v := publishing(t, "k1", key)
	v.allowed = mustAllowlist(t, addresses...)
	return v
}

// TestAllowlistAdmitsTheConfiguredOperator is the positive case, and the only
// place in this package that produces a VerifiedOperator at all.
//
// The owner is asserted against the constant rather than against a fixture:
// research D7's whole point is that the dashboard's owner is code, so a wrong
// value here would be an empty dashboard for a person the daemon just verified.
func TestAllowlistAdmitsTheConfiguredOperator(t *testing.T) {
	t.Parallel()

	key := signingKey(t, 0)
	v := admitting(t, key, testEmail)

	operator, err := v.Verify(context.Background(), mintClaims(t, key, identityMembers(v)))
	if err != nil {
		t.Fatalf("the configured operator was refused: %v", err)
	}
	if operator.Email != testEmail {
		t.Fatalf("Email = %q, want %q as the edge wrote it", operator.Email, testEmail)
	}
	if operator.Owner != auth.CallerOperator {
		t.Fatalf("Owner = %q, want the constant %q — never configuration, never a claim",
			operator.Owner, auth.CallerOperator)
	}
}

// TestAllowlistFoldsCaseOnBothSides: lowercasing folds spellings of one mailbox,
// and the fold has to happen on both sides. An operator who typed a capital into
// their own CRSW_ACCESS_ALLOWED_EMAILS must not be locked out of their own host
// by it, and the edge's spelling of the address is not the operator's to control.
//
// The admitted address is also asserted to come back **as the edge wrote it**:
// the lowercasing belongs to the comparison, not to the identity the header will
// display.
func TestAllowlistFoldsCaseOnBothSides(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name       string
		configured string
		claimed    string
	}{
		{"both as Google issues them", testEmail, testEmail},
		{"a capital in the configured entry", "Operator@Example.COM", testEmail},
		{"a capital in the verified claim", testEmail, "Operator@Example.com"},
		{"capitals on both sides, differently", "OPERATOR@EXAMPLE.COM", "OpErAtOr@ExAmPlE.cOm"},
		{"a space either side of the configured entry", "  " + testEmail + "  ", testEmail},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			key := signingKey(t, 0)
			v := admitting(t, key, tc.configured)

			operator, err := v.Verify(context.Background(), mintClaims(t, key, with(identityMembers(v), "email", tc.claimed)))
			if err != nil {
				t.Fatalf("configured %q, claimed %q: refused with %v", tc.configured, tc.claimed, err)
			}
			if operator.Email != tc.claimed {
				t.Fatalf("Email = %q, want %q — the address as the edge wrote it", operator.Email, tc.claimed)
			}
		})
	}
}

// TestAllowlistRefusesAnAddressItDoesNotHold is FR-007 doing its job: the
// assertion is genuine in every other respect — the edge signed it, for this
// application, minutes ago, about a real person — and the daemon still refuses,
// because the edge is not the only thing that decides who this host serves.
//
// The near misses are the point. Every one of them is a string an equality test
// refuses and a looser comparison — a prefix, a domain match, a subaddress fold —
// would admit.
func TestAllowlistRefusesAnAddressItDoesNotHold(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		claimed string
	}{
		{"another person at the same domain", "intruder@example.com"},
		{"the same local part at another domain", "operator@example.net"},
		{"a domain that merely ends with the allowed one", "operator@notexample.com"},
		{"a prefix of the allowed address", "operator@example.co"},
		{"the allowed address with a suffix", "operator@example.community"},
		{"a local part the allowed one starts with", "oper@example.com"},
		// Subaddressing is Google's delivery rule, not the operator's
		// configuration. Folding it here would be the daemon inventing an
		// equivalence nobody wrote down, and every plus-address at the domain
		// would become the operator.
		{"a subaddress of the allowed address", "operator+dashboard@example.com"},
		{"a trailing dot", testEmail + "."},
		// Not trimmed, unlike a configured entry: an address the edge wrote with
		// a space in it is not the address on the list.
		{"a leading space", " " + testEmail},
		{"a trailing space", testEmail + " "},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			key := signingKey(t, 0)
			v := admitting(t, key, testEmail)

			operator, err := v.Verify(context.Background(), mintClaims(t, key, with(identityMembers(v), "email", tc.claimed)))
			if operator != nil || !errors.Is(err, errEmailNotAllowed) {
				t.Fatalf("Verify = %+v, %v; want no operator and errEmailNotAllowed", operator, err)
			}
		})
	}
}

// TestAllowlistRefusalNamesNoAddress is the half of FR-007 that is about the
// audit trail rather than the decision: the refusal is recorded, the address is
// not. This error is what T008's record carries as its reason, so the property
// has to hold on the error itself — by the time a middleware is writing it, the
// only thing that could keep the address out is the error never having held it.
func TestAllowlistRefusalNamesNoAddress(t *testing.T) {
	t.Parallel()

	const refused = "intruder@corp.example.net"

	key := signingKey(t, 0)
	v := admitting(t, key, testEmail)

	_, err := v.Verify(context.Background(), mintClaims(t, key, with(identityMembers(v), "email", refused)))
	if !errors.Is(err, errEmailNotAllowed) {
		t.Fatalf("Verify = %v, want errEmailNotAllowed", err)
	}

	// The whole address, and each half of it: a reason built with fmt.Errorf and
	// only the domain, or only the local part, is still the caller's bytes in the
	// journal.
	local, domain, _ := strings.Cut(refused, "@")
	for _, secret := range []string{refused, local, domain} {
		if strings.Contains(err.Error(), secret) {
			t.Fatalf("the refusal reason %q names %q; a trail reason is a fixed string authored in this repo", err, secret)
		}
	}
}

// TestVerifyRunsTheAllowlistLast asserts the ordering the sequence is: step 11
// is reached only by an assertion that passed steps 1 to 10, so the allowlist
// never decides anything about an assertion the edge did not write.
//
// The service-token row is the one that matters, and it is FR-013c asserted on
// the exported path rather than on verifiedClaims. It is what **every API call
// the operator's client makes** carries, and a step 11 written as "refuse an
// email that is present and disallowed" would admit it here with the whole of
// claims_test.go still green.
func TestVerifyRunsTheAllowlistLast(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		// signWithAnotherKey mints with a key the published set does not hold.
		signWithAnotherKey bool
		members            func(v *Validator) map[string]any
		want               error
	}{
		{
			name:               "a disallowed address on an assertion nothing signed",
			signWithAnotherKey: true,
			members:            func(v *Validator) map[string]any { return with(identityMembers(v), "email", "intruder@example.com") },
			want:               errSignatureInvalid,
		},
		{
			name: "a disallowed address on an expired assertion",
			members: func(v *Validator) map[string]any {
				return with(with(identityMembers(v), "email", "intruder@example.com"), "exp", keysTimestamp-3600)
			},
			want: errExpired,
		},
		{
			name: "a disallowed address minted for another application",
			members: func(v *Validator) map[string]any {
				return with(with(identityMembers(v), "email", "intruder@example.com"), "aud", []any{"deadbeef"})
			},
			want: errAudienceMismatch,
		},
		{
			name:    "a valid service-token assertion, presented to the dashboard",
			members: serviceTokenMembers,
			want:    errNoEmail,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			published := signingKey(t, 0)
			v := admitting(t, published, testEmail)

			signer := published
			if tc.signWithAnotherKey {
				signer = signingKey(t, 1)
			}

			operator, err := v.Verify(context.Background(), mintClaims(t, signer, tc.members(v)))
			if operator != nil || !errors.Is(err, tc.want) {
				t.Fatalf("Verify = %+v, %v; want no operator and %v", operator, err, tc.want)
			}
			if errors.Is(err, errEmailNotAllowed) {
				t.Fatal("the allowlist decided an assertion that had not passed the checks before it")
			}
		})
	}
}

// TestNewRefusesAnAllowlistNamingNobody is FR-011 for the third layer-1 value.
// An allowlist holding no address is a dashboard nobody can reach, and the
// difference between failing at startup and failing at every request is whether
// the operator finds out from systemd or from their own locked door.
func TestNewRefusesAnAllowlistNamingNobody(t *testing.T) {
	t.Parallel()

	const domain = "https://team.cloudflareaccess.com"

	cases := []struct {
		name   string
		emails []string
	}{
		{"no list at all", nil},
		{"an empty list", []string{}},
		{"one empty entry", []string{""}},
		{"one blank entry", []string{"   "}},
		{"a good entry and an empty one", []string{testEmail, ""}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if v, err := New(domain, testAUD, tc.emails); err == nil {
				t.Fatalf("New with %q built %v, want a refusal", tc.emails, v)
			}
		})
	}
}

// TestNewWiresTheAllowlist: the constructor the daemon will call builds a
// validator that admits the configured address and refuses another one. Without
// it the allowlist could be implemented, tested through a hand-built validator,
// and never actually reached by New — which is how milestone 1 shipped a reaper
// nothing ran.
//
// This is the one test in the package that runs on the host clock, because New
// is the one place that chooses it, so the assertion's validity window is
// expressed against time.Now rather than keysTimestamp.
func TestNewWiresTheAllowlist(t *testing.T) {
	t.Parallel()

	key := signingKey(t, 0)
	srv := newKeyServer(t, keySetJSON(jwkFor(t, "k1", &key.PublicKey)))

	v, err := New(srv.URL(), testAUD, []string{testEmail})
	if err != nil {
		t.Fatalf("New(%q): %v", srv.URL(), err)
	}

	now := time.Now().Unix()
	members := with(with(identityMembers(v), "iat", now-60), "exp", now+3600)

	operator, err := v.Verify(context.Background(), mintClaims(t, key, members))
	if err != nil {
		t.Fatalf("a validator from New refused the configured operator: %v", err)
	}
	if operator.Email != testEmail {
		t.Fatalf("Email = %q, want %q", operator.Email, testEmail)
	}

	stranger, err := v.Verify(context.Background(), mintClaims(t, key, with(members, "email", "intruder@example.com")))
	if stranger != nil || !errors.Is(err, errEmailNotAllowed) {
		t.Fatalf("Verify = %+v, %v; want no operator and errEmailNotAllowed", stranger, err)
	}
}
