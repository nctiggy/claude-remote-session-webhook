// Internal test, matching middleware_test.go and server_test.go: the door under
// test is an unexported method, and two of the properties are only visible from
// inside — the pending audit record, and the fail-closed paths that newServer
// makes unreachable from outside.
//
// Every assertion here is minted from a locally generated RSA key pair and
// resolved against a key server this file starts, so nothing reaches the network
// and no fixture expires. That is contracts/access-jwt.md's own instruction, and
// it is what lets the negative cases be genuine rather than approximated: a
// forged signature is really forged, and an expired assertion really expired.
package httpapi

import (
	"context"
	"crypto"
	"crypto/hmac"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"maps"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nctiggy/claude-remote-session-webhook/internal/access"
	"github.com/nctiggy/claude-remote-session-webhook/internal/audit"
	"github.com/nctiggy/claude-remote-session-webhook/internal/auth"
)

// testKeys generates the two key pairs this file signs with, once for the whole
// package: the one the key server publishes, and one it never does.
//
// Once, because a 2048-bit generation costs more than every other thing these
// tests do put together, and the cases below need a fresh *validator* rather
// than a fresh key — the two are independent, since each case builds its own key
// server around the same pair.
var testKeys = sync.OnceValues(func() (*rsa.PrivateKey, *rsa.PrivateKey) {
	published, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		panic("generate the published test key: " + err.Error())
	}
	unpublished, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		panic("generate the unpublished test key: " + err.Error())
	}
	return published, unpublished
})

const (
	// testKeyID is the key id the published key is served under. The value is
	// arbitrary; that an assertion has to name it is not.
	testKeyID = "test-key-1"

	// testAUD is the audience tag this application is pinned to, and testOtherAUD
	// is a genuinely valid assertion minted for a different application in the
	// same account — the case the audience check exists for.
	testAUD      = "test-only-audience-tag"
	testOtherAUD = "another-application-in-the-same-account"

	// testOperatorEmail is on the allowlist and testStrangerEmail is not. Both
	// are addresses the edge would have verified; the difference is only whether
	// this daemon serves them.
	testOperatorEmail = "operator@example.com"
	testStrangerEmail = "someone-else@example.com"
)

// keyServer is a stand-in for the edge's published key set, plus the private key
// that matches it.
type keyServer struct {
	published   *rsa.PrivateKey
	unpublished *rsa.PrivateKey
	origin      string
}

// newKeyServer starts a key server for one case and returns a validator pointed
// at it.
//
// The origin is loopback over http, which config.loadTeamDomain permits for
// exactly this reason: a key set the tests control needs no synthetic CA in the
// trust store. Nothing is skipped for it — the full sequence runs against
// whatever this server answers with.
func newKeyServer(t *testing.T) *keyServer {
	t.Helper()

	published, unpublished := testKeys()
	body := jwkSetJSON(t, testKeyID, &published.PublicKey)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set(headerContentType, contentTypeJSON)
		if _, err := w.Write(body); err != nil {
			t.Errorf("serve the test key set: %v", err)
		}
	}))
	t.Cleanup(srv.Close)

	return &keyServer{published: published, unpublished: unpublished, origin: srv.URL}
}

// newDeadKeyServer is the fail-closed case (FR-009): an origin nothing answers
// on. The server is started and stopped so the address is real and refuses,
// which is what a key source that is down looks like from here.
func newDeadKeyServer(t *testing.T) *keyServer {
	t.Helper()

	published, unpublished := testKeys()
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	origin := srv.URL
	srv.Close()

	return &keyServer{published: published, unpublished: unpublished, origin: origin}
}

// validator builds layer 1 against this key server, exactly as NewWith builds
// the production one.
func (k *keyServer) validator(t *testing.T) *access.Validator {
	t.Helper()

	v, err := access.New(k.origin, testAUD, []string{testOperatorEmail})
	if err != nil {
		t.Fatalf("access.New = _, %v; want a validator", err)
	}
	return v
}

// claims is an assertion the daemon should admit: this issuer, this audience,
// inside its validity, naming an allowed person. Every negative case below is
// this map with one member changed, so each one differs from a working assertion
// by exactly the thing it is named for.
func (k *keyServer) claims() map[string]any {
	now := time.Now()
	return map[string]any{
		"iss":   k.origin,
		"aud":   []string{testAUD},
		"email": testOperatorEmail,
		"iat":   now.Add(-time.Minute).Unix(),
		"nbf":   now.Add(-time.Minute).Unix(),
		"exp":   now.Add(time.Hour).Unix(),
	}
}

// header is the JOSE header of a genuine assertion.
func joseHeaderFor(kid string) map[string]any {
	return map[string]any{"alg": "RS256", "kid": kid, "typ": "JWT"}
}

// mint signs claims with the published key under a genuine JOSE header.
func (k *keyServer) mint(t *testing.T, claims map[string]any) string {
	t.Helper()
	return signAssertion(t, k.published, joseHeaderFor(testKeyID), claims)
}

// signAssertion builds a JWS the way RFC 7515 describes it rather than by
// calling internal/access's own code: a fixture built with the code under test
// proves only that the code agrees with itself.
func signAssertion(t *testing.T, key *rsa.PrivateKey, header, claims map[string]any) string {
	t.Helper()

	signed := segment(t, header) + "." + segment(t, claims)
	digest := sha256.Sum256([]byte(signed))
	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, digest[:])
	if err != nil {
		t.Fatalf("sign the test assertion: %v", err)
	}
	return signed + "." + base64.RawURLEncoding.EncodeToString(sig)
}

// segment is one base64url-encoded JSON member of a JWS, unpadded as RFC 7515
// requires.
func segment(t *testing.T, v map[string]any) string {
	t.Helper()

	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("encode a test assertion segment: %v", err)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

// jwkSetJSON publishes one RSA key in the shape RFC 7517 defines and Cloudflare
// serves.
func jwkSetJSON(t *testing.T, kid string, pub *rsa.PublicKey) []byte {
	t.Helper()

	b, err := json.Marshal(map[string]any{"keys": []map[string]string{{
		"kty": "RSA",
		"kid": kid,
		"n":   base64.RawURLEncoding.EncodeToString(pub.N.Bytes()),
		"e":   base64.RawURLEncoding.EncodeToString(big.NewInt(int64(pub.E)).Bytes()),
	}}})
	if err != nil {
		t.Fatalf("encode the test key set: %v", err)
	}
	return b
}

// door is the browser door with everything behind it readable: the trail it
// writes, and whether the handler it guards ever ran.
type door struct {
	*testServer
	served  int
	handler http.Handler
}

func newDoor(t *testing.T, browser layer1) *door {
	t.Helper()

	d := &door{testServer: newAuditedServerWith(t, browser)}
	d.handler = d.authenticateBrowser(audit.ActionDashboardView, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		d.served++
		if op, ok := OperatorFrom(r.Context()); ok {
			w.Header().Set("X-Test-Operator", op.Email+" "+string(op.Owner))
		}
		w.WriteHeader(http.StatusOK)
	}))
	return d
}

// request drives one request through the door. An assertion of absent sends no
// header at all, which is a different shape from sending an empty one and has to
// be reachable as its own case.
const absent = "\x00absent"

func (d *door) request(assertion string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	if assertion != absent {
		r.Header.Set(headerAccessAssertion, assertion)
	}
	w := httptest.NewRecorder()
	d.handler.ServeHTTP(w, r)
	return w
}

// TestBrowserDoorAdmitsAVerifiedOperator is the positive case, and it also pins
// FR-020 and FR-037a: the identity the handler receives comes from the assertion
// the edge signed, and the owner it carries is the same constant the API door's
// caller carries, so a session created through one door is a session the other
// can see.
//
// The request carries no layer-2 signature and is served anyway, which is FR-012
// from this side: each door refuses only by the check that applies to it.
func TestBrowserDoorAdmitsAVerifiedOperator(t *testing.T) {
	t.Parallel()

	keys := newKeyServer(t)
	d := newDoor(t, keys.validator(t))

	w := d.request(keys.mint(t, keys.claims()))

	if w.Code != http.StatusOK {
		t.Fatalf("a valid identity assertion was answered %d (%s); want %d", w.Code, w.Body.String(), http.StatusOK)
	}
	if d.served != 1 {
		t.Fatalf("the handler behind the door ran %d times; want exactly 1", d.served)
	}
	if got, want := w.Header().Get("X-Test-Operator"), testOperatorEmail+" "+string(auth.CallerOperator); got != want {
		t.Errorf("the handler saw operator %q; want %q", got, want)
	}

	rec := d.only(t)
	if got, want := rec["action"], string(audit.ActionDashboardView); got != want {
		t.Errorf("action = %v; want %v", got, want)
	}
	if got, want := rec["decision"], string(audit.Allow); got != want {
		t.Errorf("decision = %v; want %v", got, want)
	}
	// The server-derived owner, never the verified address: the trail names who
	// the ownership check will compare against, and an email is a claim value.
	if got, want := rec["caller"], string(auth.CallerOperator); got != want {
		t.Errorf("caller = %v; want %v", got, want)
	}
	if strings.Contains(d.sink.String(), testOperatorEmail) {
		t.Errorf("the trail carries the verified address:\n%s", d.sink.String())
	}
}

// browserFailure is one shape of request the door must refuse. Each builds its
// own key server and its own validator, so no case can be admitted or refused
// because of what a previous one left in the key cache.
type browserFailure struct {
	name string
	// door is what the case is presented to, and assertion is what it is
	// presented with.
	door      func(t *testing.T) *door
	assertion func(t *testing.T, k *keyServer) string
}

// browserFailures is the refusal table. The eleven steps of
// contracts/access-jwt.md are represented here, plus the keys-unobtainable case
// and the two fail-closed paths newServer makes unreachable from outside.
//
// It is not the contract's full sweep — that is T018's, in this file — but every
// row is a genuinely valid assertion spoiled in exactly one way, which is what
// makes "all of these answer identically" a claim about the door rather than
// about malformed input.
func browserFailures() []browserFailure {
	live := func(t *testing.T) *door { t.Helper(); return newDoor(t, newKeyServer(t).validator(t)) }

	return []browserFailure{{
		name:      "no header at all",
		door:      live,
		assertion: func(*testing.T, *keyServer) string { return absent },
	}, {
		name:      "an empty header",
		door:      live,
		assertion: func(*testing.T, *keyServer) string { return "" },
	}, {
		name: "two segments",
		door: live,
		assertion: func(t *testing.T, k *keyServer) string {
			parts := strings.Split(k.mint(t, k.claims()), ".")
			return parts[0] + "." + parts[1]
		},
	}, {
		name: "a payload that is not base64url",
		door: live,
		assertion: func(t *testing.T, k *keyServer) string {
			parts := strings.Split(k.mint(t, k.claims()), ".")
			return parts[0] + ".not base64url.\n" + parts[2]
		},
	}, {
		name: "a JOSE header that is not JSON",
		door: live,
		assertion: func(t *testing.T, k *keyServer) string {
			parts := strings.Split(k.mint(t, k.claims()), ".")
			return base64.RawURLEncoding.EncodeToString([]byte("not json")) + "." + parts[1] + "." + parts[2]
		},
	}, {
		// alg: none carrying a real-looking signature, so the refusal is step 3's
		// reading of alg and not step 2's shape check. The historical break is a
		// verifier that skipped verification because the token asked it to.
		name: "alg: none",
		door: live,
		assertion: func(t *testing.T, k *keyServer) string {
			header := joseHeaderFor(testKeyID)
			header["alg"] = "none"
			return signAssertion(t, k.published, header, k.claims())
		},
	}, {
		// The key-confusion break in test form: the RSA public key offered as an
		// HMAC secret. It is refused before any cryptography runs, because there
		// is one verifier and nothing to select with.
		name: "alg: HS256, signed with the public key as an HMAC secret",
		door: live,
		assertion: func(t *testing.T, k *keyServer) string {
			header := joseHeaderFor(testKeyID)
			header["alg"] = "HS256"
			secret, err := x509.MarshalPKIXPublicKey(&k.published.PublicKey)
			if err != nil {
				t.Fatalf("encode the public key as an HMAC secret: %v", err)
			}
			signed := segment(t, header) + "." + segment(t, k.claims())
			mac := hmac.New(sha256.New, secret)
			if _, err := mac.Write([]byte(signed)); err != nil {
				t.Fatalf("compute the confused signature: %v", err)
			}
			return signed + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
		},
	}, {
		name: "a crit parameter",
		door: live,
		assertion: func(t *testing.T, k *keyServer) string {
			header := joseHeaderFor(testKeyID)
			header["crit"] = []string{"b64"}
			return signAssertion(t, k.published, header, k.claims())
		},
	}, {
		name: "a key id the edge does not publish",
		door: live,
		assertion: func(t *testing.T, k *keyServer) string {
			return signAssertion(t, k.published, joseHeaderFor("a-key-id-nothing-published"), k.claims())
		},
	}, {
		name: "signed by a key the edge does not publish",
		door: live,
		assertion: func(t *testing.T, k *keyServer) string {
			return signAssertion(t, k.unpublished, joseHeaderFor(testKeyID), k.claims())
		},
	}, {
		name: "a payload tampered with after signing",
		door: live,
		assertion: func(t *testing.T, k *keyServer) string {
			parts := strings.Split(k.mint(t, k.claims()), ".")
			forged := k.claims()
			forged["email"] = testStrangerEmail
			return parts[0] + "." + segment(t, forged) + "." + parts[2]
		},
	}, {
		name: "an expired assertion",
		door: live,
		assertion: func(t *testing.T, k *keyServer) string {
			claims := k.claims()
			claims["exp"] = time.Now().Add(-time.Hour).Unix()
			return k.mint(t, claims)
		},
	}, {
		name: "an assertion whose validity has not begun",
		door: live,
		assertion: func(t *testing.T, k *keyServer) string {
			claims := k.claims()
			claims["nbf"] = time.Now().Add(time.Hour).Unix()
			return k.mint(t, claims)
		},
	}, {
		name: "the wrong audience",
		door: live,
		assertion: func(t *testing.T, k *keyServer) string {
			claims := k.claims()
			claims["aud"] = []string{testOtherAUD}
			return k.mint(t, claims)
		},
	}, {
		name: "the wrong issuer",
		door: live,
		assertion: func(t *testing.T, k *keyServer) string {
			claims := k.claims()
			claims["iss"] = "https://another-team.cloudflareaccess.com"
			return k.mint(t, claims)
		},
	}, {
		// The negative that tells the two allowlist spellings apart (FR-013c).
		// Every API call the operator's own client makes produces one of these,
		// and a check written as "refuse an email that is present and not
		// allowed" admits all of them to the dashboard.
		name: "a valid service-token assertion",
		door: live,
		assertion: func(t *testing.T, k *keyServer) string {
			claims := k.claims()
			delete(claims, "email")
			claims["sub"] = ""
			claims["common_name"] = "0123456789abcdef.access"
			return k.mint(t, claims)
		},
	}, {
		name: "an address the allowlist does not hold",
		door: live,
		assertion: func(t *testing.T, k *keyServer) string {
			claims := k.claims()
			claims["email"] = testStrangerEmail
			return k.mint(t, claims)
		},
	}, {
		// FR-009: an identity that cannot be verified is not an identity. The
		// assertion is perfectly good; the keys to check it against are gone.
		name: "keys that cannot be obtained",
		door: func(t *testing.T) *door { t.Helper(); return newDoor(t, newDeadKeyServer(t).validator(t)) },
		assertion: func(t *testing.T, k *keyServer) string {
			return k.mint(t, k.claims())
		},
	}, {
		// Unreachable behind newServer, which refuses a nil validator, and kept
		// because fail-closed is only a property if it holds where it should not
		// be needed.
		name:      "layer 1 that named nobody and gave no reason",
		door:      func(t *testing.T) *door { t.Helper(); return newDoor(t, stubLayer1{}) },
		assertion: func(t *testing.T, k *keyServer) string { return k.mint(t, k.claims()) },
	}, {
		name: "no layer 1 behind the door at all",
		door: func(t *testing.T) *door {
			t.Helper()
			d := newDoor(t, testBrowser())
			d.browser = nil
			return d
		},
		assertion: func(t *testing.T, k *keyServer) string { return k.mint(t, k.claims()) },
	}}
}

// TestBrowserDoorRefusesEveryFailureIdentically is FR-010 and SC-001: status,
// body, and headers are the same bytes whichever check refused.
//
// The comparison is against the first case's whole response rather than against
// a literal written here, because the claim is uniformity — a hand-written
// expectation would still hold if every case drifted together, which is the one
// way this could fail and look fine.
func TestBrowserDoorRefusesEveryFailureIdentically(t *testing.T) {
	t.Parallel()

	type answer struct {
		name   string
		code   int
		body   string
		header http.Header
	}

	var answers []answer
	for _, c := range browserFailures() {
		keys := newKeyServer(t)
		d := c.door(t)
		w := d.request(c.assertion(t, keys))

		if d.served != 0 {
			t.Errorf("%s: the handler behind the door ran; a refused request must not reach it", c.name)
		}
		answers = append(answers, answer{name: c.name, code: w.Code, body: w.Body.String(), header: w.Header().Clone()})
	}

	first := answers[0]
	if first.code != http.StatusUnauthorized {
		t.Fatalf("%s was answered %d; want %d", first.name, first.code, http.StatusUnauthorized)
	}
	if first.body != string(bodyBrowserRefused) {
		t.Fatalf("%s answered with %q; want the package's one refusal body", first.name, first.body)
	}
	for _, got := range answers[1:] {
		if got.code != first.code {
			t.Errorf("%s was answered %d, but %s was answered %d; every refusal is the same response", got.name, got.code, first.name, first.code)
		}
		if got.body != first.body {
			t.Errorf("%s answered %q, but %s answered %q; every refusal is the same bytes", got.name, got.body, first.name, first.body)
		}
		if !maps.EqualFunc(got.header, first.header, func(a, b []string) bool { return strings.Join(a, "\x00") == strings.Join(b, "\x00") }) {
			t.Errorf("%s answered with headers %v, but %s answered with %v; a header that names the check is the same disclosure as a body that does",
				got.name, got.header, first.name, first.header)
		}
	}
}

// TestBrowserDoorRecordsOneRejectionPerRefusal is FR-016 at this door: exactly
// one record, under the browser door's own action, naming no caller because none
// was established.
func TestBrowserDoorRecordsOneRejectionPerRefusal(t *testing.T) {
	t.Parallel()

	for _, c := range browserFailures() {
		keys := newKeyServer(t)
		d := c.door(t)
		d.request(c.assertion(t, keys))

		rec := d.only(t)
		if got, want := rec["action"], string(audit.ActionAccessReject); got != want {
			t.Errorf("%s: action = %v; want %v", c.name, got, want)
		}
		if got, want := rec["decision"], string(audit.Deny); got != want {
			t.Errorf("%s: decision = %v; want %v", c.name, got, want)
		}
		if got, want := rec["caller"], audit.CallerUnknown; got != want {
			t.Errorf("%s: caller = %v; want %v", c.name, got, want)
		}
		reason, ok := rec["reason"].(string)
		if !ok || strings.TrimSpace(reason) == "" {
			t.Errorf("%s: the record carries no reason; the trail is the only place the truth of a uniform refusal is kept", c.name)
		}
		if _, ok := rec["remote"]; !ok {
			t.Errorf("%s: the record names no peer", c.name)
		}
	}
}

// TestBrowserDoorKeepsTheReasonServerSide is the other half of FR-010, and the
// half a uniform response alone does not prove: the door must know *why* it
// refused even though it never says so.
//
// Two causes that answer identically must still be told apart in the trail. A
// door that flattened every refusal to one string would pass the uniformity test
// above and leave an operator with no way to tell a misconfigured audience from
// an attack.
func TestBrowserDoorKeepsTheReasonServerSide(t *testing.T) {
	t.Parallel()

	keys := newKeyServer(t)
	claims := keys.claims()
	claims["aud"] = []string{testOtherAUD}

	reasons := map[string]string{}
	for name, assertion := range map[string]string{
		"absent":         absent,
		"wrong audience": keys.mint(t, claims),
	} {
		d := newDoor(t, newKeyServer(t).validator(t))
		w := d.request(assertion)

		if body := w.Body.String(); body != string(bodyBrowserRefused) {
			t.Fatalf("%s answered %q; want the one refusal body", name, body)
		}
		reason, ok := d.only(t)["reason"].(string)
		if !ok {
			t.Fatalf("%s: the record carries no reason", name)
		}
		reasons[name] = reason
	}

	if reasons["absent"] == reasons["wrong audience"] {
		t.Errorf("both refusals were recorded as %q; the response is uniform, the record must not be", reasons["absent"])
	}
}

// TestBrowserDoorTrailCarriesNothingTheCallerWrote is FR-035 and FR-042 at the
// door that handles an identity assertion.
//
// The assertion is the largest piece of caller-authored text this daemon
// receives, and the address inside it is the edge's word about a person. Neither
// may reach the journal — nor may the key id, which is the one part of the
// header a forger picks freely.
func TestBrowserDoorTrailCarriesNothingTheCallerWrote(t *testing.T) {
	t.Parallel()

	for _, c := range browserFailures() {
		keys := newKeyServer(t)
		d := c.door(t)
		assertion := c.assertion(t, keys)
		d.request(assertion)

		trail := d.sink.String()
		for _, secret := range []string{assertion, testOperatorEmail, testStrangerEmail, testKeyID, testAUD} {
			if secret == absent || secret == "" {
				continue
			}
			if strings.Contains(trail, secret) {
				t.Errorf("%s: the trail carries %q, which the caller supplied:\n%s", c.name, secret, trail)
			}
		}
	}
}

// TestOperatorFromReportsNoOperatorWithoutTheDoor keeps the accessor honest:
// a handler reached some other way must read false, not a zero operator, since a
// zero one carries the empty owner that no session is owned by and would read as
// an anonymous viewer rather than as a refusal.
func TestOperatorFromReportsNoOperatorWithoutTheDoor(t *testing.T) {
	t.Parallel()

	if op, ok := OperatorFrom(context.Background()); ok {
		t.Errorf("a bare context reported operator %v; want none", op)
	}
	//nolint:staticcheck // SA1029 is the point: a key another package could plant is what the unexported key type prevents, and this asserts the accessor does not read one.
	ctx := context.WithValue(context.Background(), "operator", &access.VerifiedOperator{Email: testStrangerEmail, Owner: auth.CallerOperator})
	if op, ok := OperatorFrom(ctx); ok {
		t.Errorf("a value planted under a string key reported operator %v; want none", op)
	}
}
