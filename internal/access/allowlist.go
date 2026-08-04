package access

import (
	"context"
	"errors"
	"strings"

	"github.com/nctiggy/claude-remote-session-webhook/internal/auth"
)

// errEmailNotAllowed is step 11, and the only refusal in this package that
// happens after everything about the assertion has checked out: the edge signed
// it, for this application, inside its validity, about a person.
//
// It names no address, and that is FR-007's own rule rather than tidiness. The
// reason reaches the audit trail, and milestone 1's discipline is that a trail
// reason is a fixed string authored in this repo — never bytes a caller sent.
// An operator asking *which* address was refused correlates with the edge's own
// Access log, which recorded the identity at the layer that verified it first.
var errEmailNotAllowed = errors.New("the assertion names an address the daemon's own allowlist does not hold")

// allowlist is the daemon's own copy of the addresses it will serve, held so
// that an edge misconfiguration cannot silently widen access. The edge is the
// gate; this is the daemon asserting the gate is configured the way the operator
// believes it is (FR-007).
//
// It decides who may *become* the operator, and nothing more. What that identity
// then is stays the constant auth.CallerOperator: the allowlist is
// configuration, the mapping is code, and keeping them apart is what makes a
// misconfigured allowlist fail loudly — a recorded refusal — rather than quietly,
// as an empty dashboard with every test still green (research D7).
type allowlist map[string]struct{}

// newAllowlist normalises the configured addresses once, at startup.
//
// A list naming nobody is a startup failure and not an allowlist that refuses
// everything at runtime (FR-011): the second is a daemon that looks healthy and
// serves no one, discovered by an operator locked out of their own host.
func newAllowlist(addresses []string) (allowlist, error) {
	list := make(allowlist, len(addresses))
	for _, address := range addresses {
		// Entries are trimmed because an operator types them into an environment
		// variable, where a space after a comma is a typing artefact rather than
		// part of an address. config.loadAllowedEmails already does this and
		// refuses interior whitespace besides; repeating it here costs a
		// strings.TrimSpace and means New's own contract holds whatever
		// eventually calls it.
		normalised := normaliseAddress(address)
		if normalised == "" {
			return nil, errors.New("access: the allowed-address list holds an entry naming no address; refusing to start")
		}
		list[normalised] = struct{}{}
	}
	if len(list) == 0 {
		return nil, errors.New("access: no allowed addresses configured for the dashboard; refusing to start")
	}
	return list, nil
}

// permits is the membership test, on the address lowercased as the entries were.
//
// Lowercasing folds spellings of one mailbox and never across mailboxes: Google
// issues lowercase addresses, and an operator who typed a capital into their own
// configuration should not be locked out of their own host by it.
//
// The claimed address is deliberately *not* trimmed, unlike a configured entry.
// A configured entry is something a person typed; a claim is the edge's word
// about a verified identity, and an address the edge wrote with a space in it is
// not the address on the list.
func (l allowlist) permits(email string) bool {
	_, ok := l[strings.ToLower(email)]
	return ok
}

func normaliseAddress(address string) string {
	return strings.ToLower(strings.TrimSpace(address))
}

// VerifiedOperator is what layer 1 concludes, and the only thing it hands on.
// The handler behind the browser door receives this, never the assertion.
//
// It is derived per request and never stored. A cached one would be the daemon's
// first cross-request browser state, and with it the expiry, invalidation and
// fixation questions this design exists not to have — the edge already keeps the
// browser session, and the daemon only ever checks the evidence of it (FR-036).
type VerifiedOperator struct {
	// Email is the address as the edge wrote it, non-empty by construction:
	// step 10 refuses an assertion that names no person, so one of these cannot
	// be produced without one. The lowercasing is a property of the comparison,
	// not of the address — the dashboard greets the operator by the spelling the
	// edge verified.
	Email string

	// Owner is the constant auth.CallerOperator, never configuration and never a
	// claim. It is what session ownership is checked against, so the browser door
	// and the API door cannot disagree about who the operator is (FR-037a).
	Owner auth.CallerID
}

// Verify is the browser door's whole question — was this assertion written by
// the edge, about someone this daemon serves — and runs steps 1 to 11 of
// contracts/access-jwt.md in that order.
//
// Every failure returns a nil operator and a reason for the trail alone. The
// caller answers all of them with the one uniform 401 (FR-010): the difference
// between a bad signature and a disallowed address is reconnaissance, and telling
// a stranger which check refused them is telling them which forgery to try next.
func (v *Validator) Verify(ctx context.Context, assertion string) (*VerifiedOperator, error) {
	claims, err := v.verifiedClaims(ctx, assertion)
	if err != nil {
		return nil, err
	}

	// Step 11. Reached only with a non-empty email in hand, because step 10
	// required one: "no email" can never arrive here to be read as "nothing to
	// object to" (FR-013c).
	if !v.allowed.permits(claims.Email) {
		return nil, errEmailNotAllowed
	}

	return &VerifiedOperator{Email: claims.Email, Owner: auth.CallerOperator}, nil
}
