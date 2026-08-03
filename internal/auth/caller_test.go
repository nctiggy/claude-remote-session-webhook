package auth_test

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/nctiggy/claude-remote-session-webhook/internal/auth"
)

// opaqueMessage restates the word contracts/http-api.md answers an
// unauthenticated request with, rather than reading auth.ErrUnauthorized. A test
// that imports the value it is checking cannot notice that value changing — and
// this one is the difference between a uniform denial and a message that says
// which check failed.
const opaqueMessage = "unauthorized"

// operatorIdentity restates data-model.md's audit table: caller is `operator`,
// or `unknown` on a rejected request. T031 gives adopted sessions the same
// identity, so a rename here silently makes every adopted session unownable.
const operatorIdentity = "operator"

// attackerMarker is what a request claims about itself in the cases below. It
// must never appear in the identity the daemon derives.
const attackerMarker = "attacker"

// TestVerifyNamesTheCaller is the positive half of FR-012: a verified request
// comes back with an identity, and it is the one the audit trail and every
// ownership check are written in terms of.
func TestVerifyNamesTheCaller(t *testing.T) {
	t.Parallel()

	caller, err := newAuth(t).Verify(validRequest(t, `{"name":"demo"}`))
	if err != nil {
		t.Fatalf("Verify() rejected a correctly signed request: %v", err)
	}
	if caller == nil {
		t.Fatal("Verify() accepted a request without naming its caller")
	}
	if caller.ID != auth.CallerOperator {
		t.Errorf("caller.ID = %q, want %q", caller.ID, auth.CallerOperator)
	}
	if string(auth.CallerOperator) != operatorIdentity {
		t.Errorf("auth.CallerOperator = %q, want %q — the audit trail and adopted sessions both spell it that way",
			auth.CallerOperator, operatorIdentity)
	}
}

// TestCallerIdentityIgnoresTheRequest is the point of the task. Every case is a
// correctly signed request that also *claims* an identity, in each of the places
// a claim could be smuggled: a header, the JSON body, the query, the path, basic
// auth, a cookie, the peer address. Identity comes from the fact that the
// signature verified, and from nothing else (FR-012).
//
// A future refactor that reaches into the request to name a caller — an
// X-Forwarded-User header when a proxy is added, an "owner" field when a second
// operator is imagined — fails here rather than in production, where the
// consequence is one caller reading another's session.
func TestCallerIdentityIgnoresTheRequest(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		body  string
		claim func(r *http.Request)
	}{
		{
			name:  "a header naming a caller",
			claim: func(r *http.Request) { r.Header.Set("X-CRSW-Caller", attackerMarker) },
		},
		{
			name:  "a proxy identity header",
			claim: func(r *http.Request) { r.Header.Set("X-Forwarded-User", attackerMarker+"@example.com") },
		},
		{
			name: "a forged Access assertion",
			claim: func(r *http.Request) {
				r.Header.Set("Cf-Access-Jwt-Assertion", "not.a.jwt."+attackerMarker)
			},
		},
		{
			name:  "a bearer token",
			claim: func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+attackerMarker) },
		},
		{
			name:  "basic auth credentials",
			claim: func(r *http.Request) { r.SetBasicAuth(attackerMarker, "not-a-real-password") },
		},
		{
			name:  "a cookie",
			claim: func(r *http.Request) { r.AddCookie(&http.Cookie{Name: "caller", Value: attackerMarker}) },
		},
		{
			// Signed, so the daemon really did agree to these bytes — and they
			// still say nothing about who sent them.
			name: "owner and caller fields in the signed body",
			body: `{"name":"demo","owner":"` + attackerMarker + `","caller":"` + attackerMarker + `","user":"root"}`,
		},
		{
			name:  "a query parameter",
			claim: func(r *http.Request) { r.URL.RawQuery = "caller=" + attackerMarker + "&owner=root" },
		},
		{
			name:  "a path that names an owner",
			claim: func(r *http.Request) { r.URL.Path = "/sessions/" + attackerMarker },
		},
		{
			name:  "a spoofed peer address",
			claim: func(r *http.Request) { r.RemoteAddr = "203.0.113.9:1234" },
		},
		{
			name:  "a different method",
			claim: func(r *http.Request) { r.Method = http.MethodDelete },
		},
		{
			name:  "a host header for somewhere else",
			claim: func(r *http.Request) { r.Host = attackerMarker + ".example.com" },
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			body := tt.body
			if body == "" {
				body = `{"name":"demo"}`
			}

			// One Authenticator per case: several cases sign identical bodies at
			// the same instant, and a shared one would refuse the second as a
			// replay rather than answering the question this test asks.
			r := validRequest(t, body)
			if tt.claim != nil {
				tt.claim(r)
			}

			caller, err := newAuth(t).Verify(r)
			if err != nil {
				t.Fatalf("Verify() rejected a correctly signed request: %v", err)
			}
			if caller.ID != auth.CallerOperator {
				t.Fatalf("caller.ID = %q, want %q: identity followed something the request said",
					caller.ID, auth.CallerOperator)
			}
			if strings.Contains(string(caller.ID), attackerMarker) {
				t.Errorf("caller.ID = %q, which carries the request's own claim", caller.ID)
			}
		})
	}
}

// denialCase is one way a request can be refused. Together they cover every
// sentinel the package exports; TestDenialsCoverEveryFailureMode asserts that
// rather than trusting the table to have kept up.
type denialCase struct {
	name string

	// drift places the daemon's clock relative to the instant fixtures are
	// signed at: positive is stale, negative is a request from the future.
	drift time.Duration

	// build returns the request to verify, having done any preparation it needs
	// against the same Authenticator — the replay case needs a first use.
	build func(t *testing.T, a *auth.Authenticator) *http.Request

	wantReason error
}

func denialCases() []denialCase {
	const body = `{"name":"demo"}`

	// mutate turns "change one thing about a request that would otherwise
	// verify" into a build func, so each case below names only its own change.
	mutate := func(f func(t *testing.T, r *http.Request)) func(*testing.T, *auth.Authenticator) *http.Request {
		return func(t *testing.T, _ *auth.Authenticator) *http.Request {
			t.Helper()

			r := validRequest(t, body)
			f(t, r)
			return r
		}
	}
	unchanged := func(t *testing.T, _ *auth.Authenticator) *http.Request {
		t.Helper()

		return validRequest(t, body)
	}

	return []denialCase{
		{
			name:       "no timestamp header",
			build:      mutate(func(_ *testing.T, r *http.Request) { r.Header.Del(auth.HeaderTimestamp) }),
			wantReason: auth.ErrMissingTimestamp,
		},
		{
			name:       "timestamp that is not a number",
			build:      mutate(func(_ *testing.T, r *http.Request) { r.Header.Set(auth.HeaderTimestamp, attackerMarker) }),
			wantReason: auth.ErrMalformedTimestamp,
		},
		{
			name:       "timestamp too old",
			drift:      2 * testWindow,
			build:      unchanged,
			wantReason: auth.ErrTimestampOutsideWindow,
		},
		{
			name:       "timestamp from the future",
			drift:      -2 * testWindow,
			build:      unchanged,
			wantReason: auth.ErrTimestampOutsideWindow,
		},
		{
			name:       "no signature header",
			build:      mutate(func(_ *testing.T, r *http.Request) { r.Header.Del(auth.HeaderSignature) }),
			wantReason: auth.ErrMissingSignature,
		},
		{
			name: "signature from another secret",
			build: mutate(func(t *testing.T, r *http.Request) {
				r.Header.Set(auth.HeaderSignature, signatureOver(t, otherSecret, testTimestamp, body))
			}),
			wantReason: auth.ErrSignatureMismatch,
		},
		{
			name: "body swapped after signing",
			build: mutate(func(_ *testing.T, r *http.Request) {
				r.Body = io.NopCloser(strings.NewReader(`{"name":"evil"}`))
			}),
			wantReason: auth.ErrSignatureMismatch,
		},
		{
			name: "body over the maximum",
			build: mutate(func(_ *testing.T, r *http.Request) {
				r.Body = io.NopCloser(strings.NewReader(strings.Repeat("a", int(testMaxBody)+1)))
			}),
			wantReason: auth.ErrBodyTooLarge,
		},
		{
			name:       "body that cannot be read",
			build:      mutate(func(_ *testing.T, r *http.Request) { r.Body = io.NopCloser(failingReader{}) }),
			wantReason: auth.ErrUnreadableBody,
		},
		{
			name: "a request sent twice",
			build: func(t *testing.T, a *auth.Authenticator) *http.Request {
				t.Helper()

				if _, err := a.Verify(validRequest(t, body)); err != nil {
					t.Fatalf("the first use of the replayed fixture was refused: %v", err)
				}
				return validRequest(t, body)
			},
			wantReason: auth.ErrReplayedRequest,
		},
	}
}

// TestVerifyDeniesIdentically is FR-011 as a property rather than a promise.
// Ten different reasons, one indistinguishable answer: the same message, the
// same dynamic type, the same rendering under every verb a handler might reach
// for, and no sentinel reachable through errors.Is.
//
// Distinguishability here is not a cosmetic leak. "Signature mismatch" versus
// "timestamp outside window" tells an attacker their captured request is still
// well-formed and only their clock is wrong; "replayed" tells them the bytes
// they hold were genuine. Each one turns guessing into a search.
func TestVerifyDeniesIdentically(t *testing.T) {
	t.Parallel()

	// One denial to compare the rest against by type, so "the same dynamic type"
	// is checked against something real rather than against a type name this
	// test restates. A handler that could tell the cases apart with a type
	// switch would defeat the uniformity as thoroughly as a message that
	// differed.
	wantType := fmt.Sprintf("%T", missingSignatureDenial(t))

	for _, tt := range denialCases() {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			a := newAuthDrifting(t, tt.drift)
			caller, err := a.Verify(tt.build(t, a))

			if err == nil {
				t.Fatal("Verify() accepted a request the case was built to reject")
			}
			if caller != nil {
				t.Errorf("Verify() named caller %q for a request it refused", caller.ID)
			}

			if got := err.Error(); got != opaqueMessage {
				t.Errorf("err.Error() = %q, want %q", got, opaqueMessage)
			}
			if got := fmt.Sprintf("%T", err); got != wantType {
				t.Errorf("denial type = %s, want %s: the failed check is readable off the type", got, wantType)
			}
			if !errors.Is(err, auth.ErrUnauthorized) {
				t.Errorf("errors.Is(err, auth.ErrUnauthorized) = false; a handler cannot recognise the denial")
			}

			// The verbs a handler reaches for when it writes an error into a
			// response or a log line. Each must render the same for every case,
			// which is why they are compared against a constant rather than
			// against each other.
			for verb, want := range map[string]string{
				"%v":  opaqueMessage,
				"%s":  opaqueMessage,
				"%q":  `"` + opaqueMessage + `"`,
				"%+v": opaqueMessage,
			} {
				if got := fmt.Sprintf(verb, err); got != want {
					t.Errorf("fmt.Sprintf(%q, err) = %q, want %q", verb, got, want)
				}
			}

			// Every sentinel, not just the one this case triggered: a denial
			// that answers errors.Is for its own reason is a denial a handler
			// can take apart.
			for _, sentinel := range everySentinel() {
				if errors.Is(err, sentinel) {
					t.Errorf("errors.Is(err, %v) = true; the failed check is readable off the error", sentinel)
				}
			}

			// The reason is still there for the trail — reachable only by
			// asking for it.
			if got := auth.Reason(err); !errors.Is(got, tt.wantReason) {
				t.Errorf("auth.Reason(err) = %v, want %v", got, tt.wantReason)
			}
		})
	}
}

// TestDenialNeverRendersItsReason closes the other half: no verb, including the
// %#v that config.Config needed a GoString for, may print what the denial is
// hiding.
func TestDenialNeverRendersItsReason(t *testing.T) {
	t.Parallel()

	for _, tt := range denialCases() {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := denyOne(t, tt)

			reason := auth.Reason(err)
			if reason == nil {
				t.Fatal("auth.Reason() lost the reason behind a denial")
			}

			for _, verb := range []string{"%v", "%s", "%q", "%+v", "%#v"} {
				rendered := fmt.Sprintf(verb, err)
				if strings.Contains(rendered, reason.Error()) {
					t.Errorf("fmt.Sprintf(%q, err) = %q, which says which check failed", verb, rendered)
				}
				for _, forbidden := range []string{testSecret, attackerMarker} {
					if strings.Contains(rendered, forbidden) {
						t.Errorf("fmt.Sprintf(%q, err) leaks %q", verb, forbidden)
					}
				}
			}
		})
	}
}

// TestDenialsCoverEveryFailureMode keeps the table above honest. The sentinel
// list is restated here rather than derived, so a ninth failure mode added to
// hmac.go without a case in this file is a compile error at worst and a visible
// omission at best — not a mode that quietly goes unchecked for uniformity.
func TestDenialsCoverEveryFailureMode(t *testing.T) {
	t.Parallel()

	covered := make(map[error]bool)
	for _, tt := range denialCases() {
		covered[tt.wantReason] = true
	}

	for _, sentinel := range everySentinel() {
		if !covered[sentinel] {
			t.Errorf("no case denies with %v, so nothing proves that failure is uniform", sentinel)
		}
	}
}

// TestReasonPassesOtherErrorsThrough keeps the middleware's audit path simple:
// it can ask for the reason behind any error without first working out where the
// error came from.
func TestReasonPassesOtherErrorsThrough(t *testing.T) {
	t.Parallel()

	if got := auth.Reason(nil); got != nil {
		t.Errorf("auth.Reason(nil) = %v, want nil", got)
	}

	other := errors.New("some other failure entirely")
	if got := auth.Reason(other); !errors.Is(got, other) {
		t.Errorf("auth.Reason(other) = %v, want the error itself back", got)
	}

	// Wrapped by a caller that added its own context — the denial is still
	// found, and the reason with it.
	err := missingSignatureDenial(t)
	if got := auth.Reason(fmt.Errorf("verifying request: %w", err)); !errors.Is(got, auth.ErrMissingSignature) {
		t.Errorf("auth.Reason() through a wrapped denial = %v, want %v", got, auth.ErrMissingSignature)
	}
}

// denyOne runs a single case and returns the denial, failing the test if the
// request was accepted.
func denyOne(t *testing.T, tt denialCase) error {
	t.Helper()

	a := newAuthDrifting(t, tt.drift)

	_, err := a.Verify(tt.build(t, a))
	if err == nil {
		t.Fatalf("Verify() accepted the %q request, which the case was built to reject", tt.name)
	}
	return err
}

// missingSignatureDenial is the cheapest denial there is: no preparation, no
// clock to move, and a mode the tables above also cover.
func missingSignatureDenial(t *testing.T) error {
	t.Helper()

	r := validRequest(t, "")
	r.Header.Del(auth.HeaderSignature)

	_, err := newAuth(t).Verify(r)
	if err == nil {
		t.Fatal("Verify() accepted a request carrying no signature at all")
	}
	return err
}

// everySentinel is the audit vocabulary this package can produce. Reason returns
// one of these and the denial hides all of them.
func everySentinel() []error {
	return []error{
		auth.ErrMissingTimestamp,
		auth.ErrMalformedTimestamp,
		auth.ErrTimestampOutsideWindow,
		auth.ErrMissingSignature,
		auth.ErrSignatureMismatch,
		auth.ErrReplayedRequest,
		auth.ErrUnreadableBody,
		auth.ErrBodyTooLarge,
	}
}
