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
	"sync/atomic"
	"testing"
	"time"

	"github.com/nctiggy/claude-remote-session-webhook/internal/access"
	"github.com/nctiggy/claude-remote-session-webhook/internal/audit"
	"github.com/nctiggy/claude-remote-session-webhook/internal/auth"
	"github.com/nctiggy/claude-remote-session-webhook/internal/session"
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

	// testServiceTokenName is what the edge writes in place of an email when the
	// caller is a machine holding a service token — the shape every API call the
	// operator's own client makes arrives in.
	testServiceTokenName = "0123456789abcdef.access"

	// testForgedKeyID is a key id nothing publishes, and the one part of a JOSE
	// header a forger picks freely. Named so the trail can be searched for it.
	testForgedKeyID = "a-key-id-nothing-published"
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

	published, _ := testKeys()
	return serving(t, jwkSetJSON(t, testKeyID, &published.PublicKey))
}

// newEmptyKeyServer publishes a key set holding no usable key. The fetch
// succeeds and yields nothing, which contracts/access-jwt.md treats as keys
// unobtainable rather than as an empty set of things to check against —
// a distinction with the whole of FR-009 behind it.
func newEmptyKeyServer(t *testing.T) *keyServer {
	t.Helper()
	return serving(t, []byte(`{"keys":[]}`))
}

// serving starts a key server answering every fetch with one fixed body.
func serving(t *testing.T, body []byte) *keyServer {
	t.Helper()

	published, unpublished := testKeys()
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

// newRefusingKeyServer is a key source that is up, answering, and answering with
// something that is not a key set — and counting what it was asked for.
//
// The count is the point. Every other unobtainable-keys fixture here proves a
// request is refused; this one is what tells "the daemon asked once" apart from
// "the daemon asked once per request", which is the difference between an outage
// the daemon rides out and an outage it amplifies.
func newRefusingKeyServer(t *testing.T) (*keyServer, *atomic.Int64) {
	t.Helper()

	fetches := &atomic.Int64{}
	published, unpublished := testKeys()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fetches.Add(1)
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	t.Cleanup(srv.Close)

	return &keyServer{published: published, unpublished: unpublished, origin: srv.URL}, fetches
}

// newSilentKeyServer accepts the connection and then says nothing: the provider
// that is wedged rather than the one that is gone.
//
// newDeadKeyServer cannot stand in for it. A refused connection comes back in
// microseconds, so a daemon with no bound on the fetch at all passes every case
// built on one; only a socket that stays open and stays quiet asks whether the
// request ever ends.
func newSilentKeyServer(t *testing.T) *keyServer {
	t.Helper()

	published, unpublished := testKeys()
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		<-release
	}))

	// Registered before the release, so it runs after it: cleanups run in
	// reverse, Close waits for the handler above to return, and a handler still
	// blocked on release would wedge the test run rather than fail it.
	t.Cleanup(srv.Close)
	t.Cleanup(func() { close(release) })

	return &keyServer{published: published, unpublished: unpublished, origin: srv.URL}
}

// validator builds layer 1 against this key server, exactly as NewWith builds
// the production one — the *Validator inside the same assertionDoor
// verifiedLayer1 wraps it in, so a test drives the door the daemon serves and
// not the validator underneath it.
func (k *keyServer) validator(t *testing.T) layer1 {
	t.Helper()

	v, err := access.New(k.origin, testAUD, []string{testOperatorEmail})
	if err != nil {
		t.Fatalf("access.New = _, %v; want a validator", err)
	}
	return assertionDoor{validator: v}
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

// serviceTokenClaims is the other documented assertion shape (research D2), and
// the one that matters: **every API call the operator's own client makes
// produces one of these** once the daemon is behind the edge. It is genuine,
// signed by the same key, minted for the same application — and it names a
// machine rather than a person.
func (k *keyServer) serviceTokenClaims() map[string]any {
	claims := k.claims()
	delete(claims, "email")
	claims["sub"] = ""
	claims["common_name"] = testServiceTokenName
	return claims
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

	encoded, err := json.Marshal(claims)
	if err != nil {
		t.Fatalf("encode the test assertion's claims: %v", err)
	}
	return signPayload(t, key, header, string(encoded))
}

// signPayload signs a payload that is not necessarily JSON at all, which is the
// only way step 6 is reachable from outside: the signature has to be genuine
// before anything reads what it covers.
func signPayload(t *testing.T, key *rsa.PrivateKey, header map[string]any, payload string) string {
	t.Helper()

	signed := segment(t, header) + "." + base64.RawURLEncoding.EncodeToString([]byte(payload))
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
	return newDoorFor(t, browser, audit.ActionDashboardView)
}

// newDoorFor is newDoor with the guarded route's action chosen, which the header
// tests in render_test.go need and no test of layer 1 itself does: the two
// static assets are the one response the cache rule treats differently (T010),
// and the door learns which it is guarding from this action.
func newDoorFor(t *testing.T, browser layer1, action audit.Action) *door {
	t.Helper()

	d := &door{testServer: newAuditedServerWith(t, browser)}
	d.handler = d.authenticateBrowser(action, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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

	// keys is this row's edge: the key server its assertion is minted from *and*
	// the one the validator behind its door reads.
	//
	// One server for both, which is not tidiness. An assertion minted against a
	// second key server names that server's origin as its issuer, so a row meaning
	// to present an expired assertion is refused for its issuer instead — and
	// every refusal looks the same from outside, so the row goes on passing while
	// testing a check it was not written for.
	keys func(t *testing.T) *keyServer

	// door overrides what stands behind the door, for the two paths that have no
	// validator at all. Nil is the ordinary arrangement: a validator over keys.
	door func(t *testing.T, k *keyServer) *door

	assertion func(t *testing.T, k *keyServer) string

	// step names the check that must refuse this row, and is what the trail is
	// read against: rows sharing a step must record the same reason, and rows
	// with different steps must never record the same one. It is a label for the
	// test's own use — nothing in the daemon has a value like it, and no caller
	// ever learns which one applied.
	step string
}

// present drives this row through its own door, and hands back the door, what
// was presented to it, and what came back — the trail, the input and the
// response being the three things the claims below are made of.
func (c browserFailure) present(t *testing.T) (*door, string, *httptest.ResponseRecorder) {
	t.Helper()

	keys := c.keys(t)
	build := c.door
	if build == nil {
		build = func(t *testing.T, k *keyServer) *door { t.Helper(); return newDoor(t, k.validator(t)) }
	}

	d := build(t, keys)
	assertion := c.assertion(t, keys)
	return d, assertion, d.request(assertion)
}

// browserFailures is contracts/access-jwt.md's test table, presented to the
// browser door: every one of the eleven steps, the two keys-unobtainable
// shapes, and the two fail-closed paths newServer makes unreachable from
// outside.
//
// Every row is a genuinely valid assertion spoiled in exactly one way, minted
// from a local key pair rather than approximated. That is what makes the three
// claims below claims about the door — a table of malformed strings would prove
// only that garbage is refused, which was never in doubt. What is in doubt is
// whether a *valid* assertion for another audience, another team, another
// person, or another kind of caller altogether is told apart from the
// operator's, and whether the telling apart leaks.
func browserFailures() []browserFailure {
	genuine := func(t *testing.T, k *keyServer) []string {
		t.Helper()
		return strings.Split(k.mint(t, k.claims()), ".")
	}

	return []browserFailure{{
		name:      "no header at all",
		step:      "no assertion",
		keys:      newKeyServer,
		assertion: func(*testing.T, *keyServer) string { return absent },
	}, {
		name:      "an empty header",
		step:      "no assertion",
		keys:      newKeyServer,
		assertion: func(*testing.T, *keyServer) string { return "" },
	}, {
		name: "one segment",
		step: "not three segments",
		keys: newKeyServer,
		assertion: func(t *testing.T, k *keyServer) string {
			return genuine(t, k)[0]
		},
	}, {
		name: "two segments",
		step: "not three segments",
		keys: newKeyServer,
		assertion: func(t *testing.T, k *keyServer) string {
			parts := genuine(t, k)
			return parts[0] + "." + parts[1]
		},
	}, {
		name: "four segments",
		step: "not three segments",
		keys: newKeyServer,
		assertion: func(t *testing.T, k *keyServer) string {
			parts := genuine(t, k)
			return strings.Join(parts, ".") + "." + parts[2]
		},
	}, {
		// The three forgeries below carry no `.` of their own, which is the whole
		// difference between reaching the decoder and being counted. A segment
		// spelled "not base64url.\n" is four segments, so the row named for the
		// decoder is refused by the segment count — and passes, since one refusal
		// is indistinguishable from another from outside.
		name: "a JOSE header that is not base64url",
		step: "the header is not base64url",
		keys: newKeyServer,
		assertion: func(t *testing.T, k *keyServer) string {
			parts := genuine(t, k)
			return "not base64url!" + "." + parts[1] + "." + parts[2]
		},
	}, {
		name: "a payload that is not base64url",
		step: "the payload is not base64url",
		keys: newKeyServer,
		assertion: func(t *testing.T, k *keyServer) string {
			parts := genuine(t, k)
			return parts[0] + "." + "not base64url!" + "." + parts[2]
		},
	}, {
		name: "a signature that is not base64url",
		step: "the signature is not base64url",
		keys: newKeyServer,
		assertion: func(t *testing.T, k *keyServer) string {
			parts := genuine(t, k)
			return parts[0] + "." + parts[1] + "." + "not base64url!"
		},
	}, {
		name: "a JOSE header that is not JSON",
		step: "the header is not JSON",
		keys: newKeyServer,
		assertion: func(t *testing.T, k *keyServer) string {
			parts := genuine(t, k)
			return base64.RawURLEncoding.EncodeToString([]byte("not json")) + "." + parts[1] + "." + parts[2]
		},
	}, {
		// alg: none carrying a real-looking signature, so the refusal is step 3's
		// reading of alg and not step 2's shape check. The historical break is a
		// verifier that skipped verification because the token asked it to.
		name: "alg: none",
		step: "the algorithm",
		keys: newKeyServer,
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
		step: "the algorithm",
		keys: newKeyServer,
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
		// Named by the contract alongside none and HS256, because the rule is one
		// accepted algorithm rather than a list of refused ones: RS384 is signed
		// by the very key the edge publishes, and is still not what this validator
		// verifies.
		name: "alg: RS384",
		step: "the algorithm",
		keys: newKeyServer,
		assertion: func(t *testing.T, k *keyServer) string {
			header := joseHeaderFor(testKeyID)
			header["alg"] = "RS384"
			return signAssertion(t, k.published, header, k.claims())
		},
	}, {
		name: "a crit parameter",
		step: "a critical extension",
		keys: newKeyServer,
		assertion: func(t *testing.T, k *keyServer) string {
			header := joseHeaderFor(testKeyID)
			header["crit"] = []string{"b64"}
			return signAssertion(t, k.published, header, k.claims())
		},
	}, {
		// Picking "the only key in the set" on the caller's behalf would be the
		// verifier deciding from attacker input, so no kid is its own refusal
		// rather than a default.
		name: "no key id at all",
		step: "no key id",
		keys: newKeyServer,
		assertion: func(t *testing.T, k *keyServer) string {
			header := joseHeaderFor(testKeyID)
			delete(header, "kid")
			return signAssertion(t, k.published, header, k.claims())
		},
	}, {
		name: "a key id the edge does not publish",
		step: "an unknown key id",
		keys: newKeyServer,
		assertion: func(t *testing.T, k *keyServer) string {
			return signAssertion(t, k.published, joseHeaderFor(testForgedKeyID), k.claims())
		},
	}, {
		name: "signed by a key the edge does not publish",
		step: "the signature",
		keys: newKeyServer,
		assertion: func(t *testing.T, k *keyServer) string {
			return signAssertion(t, k.unpublished, joseHeaderFor(testKeyID), k.claims())
		},
	}, {
		name: "a payload tampered with after signing",
		step: "the signature",
		keys: newKeyServer,
		assertion: func(t *testing.T, k *keyServer) string {
			parts := genuine(t, k)
			forged := k.claims()
			forged["email"] = testStrangerEmail
			return parts[0] + "." + segment(t, forged) + "." + parts[2]
		},
	}, {
		// Step 6, which is reachable only with a genuine signature over it: until
		// then the payload is attacker-authored bytes nothing has parsed.
		name: "claims that are not JSON",
		step: "the claims are not JSON",
		keys: newKeyServer,
		assertion: func(t *testing.T, k *keyServer) string {
			return signPayload(t, k.published, joseHeaderFor(testKeyID), "the edge signed this, and it is still not a claim set")
		},
	}, {
		name: "an expired assertion",
		step: "expired",
		keys: newKeyServer,
		assertion: func(t *testing.T, k *keyServer) string {
			claims := k.claims()
			claims["exp"] = time.Now().Add(-time.Hour).Unix()
			return k.mint(t, claims)
		},
	}, {
		name: "an assertion whose validity has not begun",
		step: "not yet valid",
		keys: newKeyServer,
		assertion: func(t *testing.T, k *keyServer) string {
			claims := k.claims()
			claims["nbf"] = time.Now().Add(time.Hour).Unix()
			return k.mint(t, claims)
		},
	}, {
		// The other direction of FR-006, and the one a validator that only ever
		// asked "has this expired" would admit.
		name: "an assertion issued in the future",
		step: "issued in the future",
		keys: newKeyServer,
		assertion: func(t *testing.T, k *keyServer) string {
			claims := k.claims()
			claims["iat"] = time.Now().Add(time.Hour).Unix()
			return k.mint(t, claims)
		},
	}, {
		name: "the wrong audience",
		step: "the audience",
		keys: newKeyServer,
		assertion: func(t *testing.T, k *keyServer) string {
			claims := k.claims()
			claims["aud"] = []string{testOtherAUD}
			return k.mint(t, claims)
		},
	}, {
		name: "the wrong issuer",
		step: "the issuer",
		keys: newKeyServer,
		assertion: func(t *testing.T, k *keyServer) string {
			claims := k.claims()
			claims["iss"] = "https://another-team.cloudflareaccess.com"
			return k.mint(t, claims)
		},
	}, {
		// The negative that tells the two allowlist spellings apart (FR-013c).
		// Every API call the operator's own client makes produces one of these,
		// and a check written as "refuse an email that is present and not
		// allowed" admits all of them to the dashboard. Presented to a dashboard
		// *route* as well, below, which is where the requirement is written.
		name: "a valid service-token assertion",
		step: "no email",
		keys: newKeyServer,
		assertion: func(t *testing.T, k *keyServer) string {
			return k.mint(t, k.serviceTokenClaims())
		},
	}, {
		name: "an address the allowlist does not hold",
		step: "the allowlist",
		keys: newKeyServer,
		assertion: func(t *testing.T, k *keyServer) string {
			claims := k.claims()
			claims["email"] = testStrangerEmail
			return k.mint(t, claims)
		},
	}, {
		// FR-009: an identity that cannot be verified is not an identity. The
		// assertion is perfectly good; the keys to check it against are gone.
		name: "keys that cannot be obtained",
		step: "the keys cannot be reached",
		keys: newDeadKeyServer,
		assertion: func(t *testing.T, k *keyServer) string {
			return k.mint(t, k.claims())
		},
	}, {
		// The same rule with the key source answering perfectly well. A fetch
		// yielding no usable key is a failed fetch, not a set that happens to
		// match nothing — the second reading refuses the same request today and
		// stops meaning fail-closed the moment a caller can influence the set.
		name: "a key set holding no usable key",
		step: "the key set holds nothing",
		keys: newEmptyKeyServer,
		assertion: func(t *testing.T, k *keyServer) string {
			return k.mint(t, k.claims())
		},
	}, {
		// Unreachable behind newServer, which refuses a nil validator, and kept
		// because fail-closed is only a property if it holds where it should not
		// be needed.
		name:      "layer 1 that named nobody and gave no reason",
		step:      "layer 1 named nobody",
		keys:      newKeyServer,
		door:      func(t *testing.T, _ *keyServer) *door { t.Helper(); return newDoor(t, stubLayer1{}) },
		assertion: func(t *testing.T, k *keyServer) string { return k.mint(t, k.claims()) },
	}, {
		name: "no layer 1 behind the door at all",
		step: "no layer 1 at all",
		keys: newKeyServer,
		door: func(t *testing.T, _ *keyServer) *door {
			t.Helper()
			d := newDoor(t, testBrowser())
			d.browser = nil
			return d
		},
		assertion: func(t *testing.T, k *keyServer) string { return k.mint(t, k.claims()) },
	}}
}

// TestTheSweepPresentsAssertionsThatWouldOtherwiseBeAdmitted is the sweep's
// non-vacuity, and it is not a formality.
//
// Every row above spoils the assertion this fixture mints. If that assertion had
// stopped being one the door admits — an expiry that drifted, an allowlist that
// no longer holds the fixture's address — all thirty rows would still be refused
// and every claim made about them would be about nothing. The uniformity claim
// in particular would pass on a door that refused the operator too.
func TestTheSweepPresentsAssertionsThatWouldOtherwiseBeAdmitted(t *testing.T) {
	t.Parallel()

	keys := newKeyServer(t)
	d := newDoor(t, keys.validator(t))

	if w := d.request(keys.mint(t, keys.claims())); w.Code != http.StatusOK {
		t.Fatalf("the assertion every row of the sweep spoils was answered %d (%s); want %d",
			w.Code, w.Body.String(), http.StatusOK)
	}
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
		d, _, w := c.present(t)

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
		d, _, _ := c.present(t)

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
// The sweep answers every row with the same bytes. The trail must not: two rows
// refused at the same step record the same reason, and two rows refused at
// different steps never do. A door that flattened every refusal to one string
// would pass the uniformity test above and leave an operator with no way to tell
// a misconfigured audience from an attack — and one that recorded the step it
// reached rather than the check that failed would pass a spot check of two rows
// while collapsing a dozen others.
func TestBrowserDoorKeepsTheReasonServerSide(t *testing.T) {
	t.Parallel()

	// The row that first recorded each reason, so a collision names both.
	type refusal struct{ row, reason string }
	byStep := map[string]refusal{}

	for _, c := range browserFailures() {
		d, _, w := c.present(t)

		if body := w.Body.String(); body != string(bodyBrowserRefused) {
			t.Fatalf("%s answered %q; want the one refusal body", c.name, body)
		}
		reason, ok := d.only(t)["reason"].(string)
		if !ok || strings.TrimSpace(reason) == "" {
			t.Errorf("%s: the record carries no reason", c.name)
			continue
		}

		if first, seen := byStep[c.step]; seen {
			if reason != first.reason {
				t.Errorf("%s and %s are both refused at %s, but recorded %q and %q; one cause reads as two in the journal",
					c.name, first.row, c.step, reason, first.reason)
			}
			continue
		}
		for step, first := range byStep {
			if first.reason == reason {
				t.Errorf("%s (%s) and %s (%s) both recorded %q; the response is uniform, the record must not be",
					c.name, c.step, first.row, step, reason)
			}
		}
		byStep[c.step] = refusal{row: c.name, reason: reason}
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
		d, assertion, _ := c.present(t)

		trail := d.sink.String()
		for _, secret := range []string{
			assertion,
			testOperatorEmail, testStrangerEmail,
			testKeyID, testForgedKeyID, testAUD, testOtherAUD,
			// The service token's own name. It is the machine's identifier at the
			// edge, which makes it the one caller-authored value that reads like
			// something worth keeping — and the trail already names the door.
			testServiceTokenName,
		} {
			if secret == absent || secret == "" {
				continue
			}
			if strings.Contains(trail, secret) {
				t.Errorf("%s: the trail carries %q, which the caller supplied:\n%s", c.name, secret, trail)
			}
		}
	}
}

// TestTheDashboardRefusesAValidServiceTokenAssertion is FR-013c at the route the
// requirement is written about, and the one row of the sweep that cannot be made
// at the door alone.
//
// The row above presents this assertion to authenticateBrowser in isolation.
// This presents it to GET / — the real router, the real fleet handler, a real
// session behind it — because what is being guarded against is not a door that
// admits nobody. It is the *dashboard* served to a credential that identifies a
// machine: signature valid, audience valid, issuer valid, inside its validity,
// and no email for a check to object to. Every API call the operator's own
// client makes carries one of these once the daemon is behind the edge, so the
// wrong spelling of step 10 puts the fleet on the far side of the operator's own
// automation — and puts it there permanently, since nothing about that assertion
// ever expires in a way an operator would notice.
func TestTheDashboardRefusesAValidServiceTokenAssertion(t *testing.T) {
	t.Parallel()

	f := newFleet(t)
	planted, _ := f.fixture.plant(t, session.Session{Name: "a name no machine may read", WorkDir: f.fixture.repo})

	// Non-vacuity first, and at this route rather than in principle: the page
	// this refusal withholds is a page that exists and really names the session.
	if page := f.view(t).Body.String(); !strings.Contains(page, planted.Name) {
		t.Fatalf("the fleet does not show the session a service token must not see:\n%s", page)
	}

	assertion := f.keys.mint(t, f.keys.serviceTokenClaims())
	w := f.openWith(t, "/", assertion)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("GET / with a valid service-token assertion = %d (%s); want %d",
			w.Code, w.Body.String(), http.StatusUnauthorized)
	}
	body := w.Body.String()
	if body != string(bodyBrowserRefused) {
		t.Errorf("GET / answered a service token with %q; want the browser door's one refusal", body)
	}
	for _, withheld := range []string{planted.ID, planted.Name, f.fixture.repo} {
		if strings.Contains(body, withheld) {
			t.Errorf("the refusal carries %q, from the fleet the service token asked for:\n%s", withheld, body)
		}
	}

	// The same bytes a caller carrying no credential at all receives, at the same
	// route (SC-001). A refusal that differed by so much as a header would tell
	// the operator's own client that its token is *nearly* enough, which is the
	// first half of knowing what to forge next.
	stranger := f.openWith(t, "/", absent)
	if stranger.Code != w.Code || stranger.Body.String() != body {
		t.Errorf("a service token was answered %d %q and no credential at all %d %q; the two must be indistinguishable",
			w.Code, body, stranger.Code, stranger.Body.String())
	}
	if !maps.EqualFunc(stranger.Header(), w.Header(), func(a, b []string) bool {
		return strings.Join(a, "\x00") == strings.Join(b, "\x00")
	}) {
		t.Errorf("a service token was answered with headers %v and no credential at all with %v",
			w.Header(), stranger.Header())
	}

	// One record per request, the two refusals recorded as this door's own
	// rejection, and nothing the machine wrote kept: its assertion is the largest
	// piece of caller-authored text the daemon receives, and its common_name is
	// the one value in there that reads like something worth logging.
	records := f.records(t)
	if len(records) != 3 {
		t.Fatalf("three requests emitted %d audit records (%v); want one each", len(records), records)
	}
	for _, rec := range records[1:] {
		if got, want := rec["action"], string(audit.ActionAccessReject); got != want {
			t.Errorf("action = %v; want %v", got, want)
		}
		if got, want := rec["decision"], string(audit.Deny); got != want {
			t.Errorf("decision = %v; want %v", got, want)
		}
	}
	for _, secret := range []string{assertion, testServiceTokenName, planted.Name} {
		if strings.Contains(f.sink.String(), secret) {
			t.Errorf("the trail carries %q:\n%s", secret, f.sink.String())
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

// --- T019: fail-closed, end to end ------------------------------------------
//
// The sweep above presents thirty spoiled assertions to authenticateBrowser in
// isolation, two of which carry keys that cannot be obtained. What a door in
// isolation cannot say is what the outage does to *the daemon*: which routes it
// closes, whether the pages that are not the fleet close with them, what each
// refused request costs, whether the other door goes down with it, and whether a
// request against a key source that accepts the connection and then says nothing
// ever comes back at all. Those are FR-009's real claims, and every one of them
// is about a route.

// browserRequest is one of the requests the browser door answers. Every row is
// a request this daemon really serves — the fleet's own page, and the paths
// FR-013d moved onto this door — so that "refused when the keys are gone" is a
// claim about something. The API's six operations are not here: they are the
// other door's, and what the outage does to them is asserted separately.
type browserRequest struct {
	name, method, target string

	// action is what the trail calls this request when the keys *are*
	// obtainable, and it is the row's proof that it reaches this door at all. A
	// row that had quietly moved to the API door would still be refused in the
	// sweep below — for having no signature, which has nothing to do with the
	// key set — and nothing about a 401 would say so.
	action audit.Action

	// served is the status an admitted request receives. A 404 page counts as
	// served: the door ran, the handler ran, and the operator was told
	// something. The failure this suite exists for is precisely the one where a
	// refusal is mistaken for an answer.
	served int

	// contentType is what an admitted request is answered as. Most of this door
	// answers a person with a document; the two embedded assets are the
	// exception, and typing them exactly is what a browser sent `nosniff` needs
	// in order to use them at all.
	contentType string
}

// browserSurface is that door's whole surface, written out rather than derived,
// because the thing being asserted is which door each path is on — and deriving
// the list from the router would make the sweep agree with whatever the router
// currently does. The control below is what keeps a hand-written table honest.
func browserSurface(sessionID string) []browserRequest {
	return []browserRequest{{
		name:        "the fleet",
		method:      http.MethodGet,
		target:      "/",
		action:      audit.ActionDashboardView,
		served:      http.StatusOK,
		contentType: contentTypeHTML,
	}, {
		// The two assets. They hold no session data, which is exactly why they
		// belong in this sweep: the tempting exception is a door that admits an
		// asset unverified because there is nothing in it to protect, and an
		// exception is a path a stranger can use to learn that this daemon is a
		// crswd rather than whatever else lives behind that hostname.
		name:        "the stylesheet",
		method:      http.MethodGet,
		target:      "/static/crswd.css",
		action:      audit.ActionDashboardAsset,
		served:      http.StatusOK,
		contentType: contentTypeCSS,
	}, {
		name:        "the rain loop",
		method:      http.MethodGet,
		target:      "/static/crswd.js",
		action:      audit.ActionDashboardAsset,
		served:      http.StatusOK,
		contentType: contentTypeJS,
	}, {
		// The address every session card links to, which since T021b is a page
		// this daemon really serves. That is what makes the row worth having: it
		// renders a session's name, its working directory and its whole screen,
		// so it is the response with the most to withhold when layer 1 cannot
		// verify anyone — and the sweep below asserts it withholds all of it.
		name:        "the page a card links to",
		method:      http.MethodGet,
		target:      "/sessions/" + sessionID + "/view",
		action:      audit.ActionDashboardView,
		served:      http.StatusOK,
		contentType: contentTypeHTML,
	}, {
		name:        "a path nothing claims",
		method:      http.MethodGet,
		target:      "/not-a-route",
		action:      audit.ActionUnknownRoute,
		served:      http.StatusNotFound,
		contentType: contentTypeHTML,
	}, {
		// A contract *path* with a method the contract does not answer: the one
		// browser row that shares a pattern with the API door, and so the one
		// most able to end up on the wrong side of it.
		name:        "a method no route answers",
		method:      http.MethodPut,
		target:      "/sessions",
		action:      audit.ActionUnknownRoute,
		served:      http.StatusNotFound,
		contentType: contentTypeHTML,
	}}
}

// ask drives one row at a fleet carrying whatever credential it was given.
func (b browserRequest) ask(t *testing.T, f *fleet, assertion string) *httptest.ResponseRecorder {
	t.Helper()

	r := httptest.NewRequest(b.method, b.target, nil)
	if assertion != absent {
		r.Header.Set(headerAccessAssertion, assertion)
	}

	w := httptest.NewRecorder()
	f.ServeHTTP(w, r)
	return w
}

// TestTheBrowserSurfaceIsServedWhenTheKeysCanBeObtained is the fail-closed
// claim's non-vacuity, and it is the argument
// TestTheSweepPresentsAssertionsThatWouldOtherwiseBeAdmitted makes about the
// door's own table, made about the routes instead.
//
// It pins each row's audit action as well as its status. That is the half a
// status cannot give: a row that stopped reaching the browser door — a pattern
// that lost to the API door's, a catch-all that stopped catching — would be
// refused in the sweep below just the same, and the sweep would go on passing
// while asserting nothing about layer 1.
func TestTheBrowserSurfaceIsServedWhenTheKeysCanBeObtained(t *testing.T) {
	t.Parallel()

	f := newFleet(t)
	planted, _ := f.fixture.plant(t, session.Session{Name: "a session an outage must not reveal", WorkDir: f.fixture.repo})

	rows := browserSurface(planted.ID)
	for _, row := range rows {
		w := row.ask(t, f, f.keys.mint(t, f.keys.claims()))

		if w.Code != row.served {
			t.Errorf("%s: %s %s = %d; want %d:\n%s", row.name, row.method, row.target, w.Code, row.served, w.Body.String())
		}
		if got := w.Header().Get(headerContentType); got != row.contentType {
			t.Errorf("%s: answered as %q; want %q — this door answers a person with a document, and the two assets it loads with the type each really is", row.name, got, row.contentType)
		}
	}

	records := f.records(t)
	if len(records) != len(rows) {
		t.Fatalf("%d requests emitted %d audit records (%v); want one each", len(rows), len(records), records)
	}
	for i, row := range rows {
		if got, want := records[i]["action"], string(row.action); got != want {
			t.Errorf("%s recorded as %v; want %v — this row does not reach the browser door, so nothing said about it below is about layer 1",
				row.name, got, want)
		}
	}

	// And the fleet really carries what the refusals below have to withhold.
	if page := f.view(t).Body.String(); !strings.Contains(page, planted.Name) {
		t.Errorf("the fleet does not name the session the outage must withhold:\n%s", page)
	}
}

// TestEveryBrowserRequestIsRefusedWhenTheKeysCannotBeObtained is FR-009 at the
// routes rather than at the door: with the edge's signing keys unobtainable,
// every page this daemon serves answers the one uniform refusal, and the handler
// behind it does not run.
//
// The assertion presented is genuine — minted by this fleet's own key server,
// inside its validity, naming the allowlisted operator, and served by the
// control above. The only thing wrong with any of these requests is that the
// daemon cannot obtain the keys to check it against, which is the whole of "an
// identity that cannot be verified is not an identity". The tempting failure is
// the opposite reading, where a validator that cannot check an assertion decides
// it has nothing to object to.
//
// Two shapes of unobtainable, because they fail in different places and only one
// of them looks like an outage: a key source nothing answers on, and one that
// answers perfectly with a set holding no usable key.
func TestEveryBrowserRequestIsRefusedWhenTheKeysCannotBeObtained(t *testing.T) {
	t.Parallel()

	for name, newKeys := range map[string]func(*testing.T) *keyServer{
		"a key source that cannot be reached": newDeadKeyServer,
		"a key set holding no usable key":     newEmptyKeyServer,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			f := newFleetWith(t, newKeys(t))
			planted, _ := f.fixture.plant(t, session.Session{Name: "a session an outage must not reveal", WorkDir: f.fixture.repo})

			rows := browserSurface(planted.ID)
			var first *httptest.ResponseRecorder
			for _, row := range rows {
				w := row.ask(t, f, f.keys.mint(t, f.keys.claims()))

				if w.Code != http.StatusUnauthorized {
					t.Fatalf("%s: %s %s = %d; want %d:\n%s", row.name, row.method, row.target, w.Code, http.StatusUnauthorized, w.Body.String())
				}
				body := w.Body.String()
				if body != string(bodyBrowserRefused) {
					t.Errorf("%s answered %q; want the browser door's one refusal", row.name, body)
				}
				// The fleet's own contents, and the not-found page's copy. The
				// second is the quieter disclosure: an answer that told a stranger
				// which paths this daemon does not serve would be the route table,
				// handed out during the one failure where nobody is verified.
				for _, withheld := range []string{planted.ID, planted.Name, f.fixture.repo, notFoundTitle} {
					if strings.Contains(body, withheld) {
						t.Errorf("%s: the refusal carries %q:\n%s", row.name, withheld, body)
					}
				}

				if first == nil {
					first = w
					continue
				}
				// Uniform across routes, not merely across failures (SC-001). The
				// door's own table proves one path answers every spoiled assertion
				// alike; this proves the pages do not differ from each other, which
				// is what a scanner comparing two addresses would read.
				if !maps.EqualFunc(w.Header(), first.Header(), func(a, b []string) bool { return strings.Join(a, "\x00") == strings.Join(b, "\x00") }) {
					t.Errorf("%s answered with headers %v, and %s with %v; the difference between two addresses is not something a stranger is owed",
						row.name, w.Header(), rows[0].name, first.Header())
				}
			}

			records := f.records(t)
			if len(records) != len(rows) {
				t.Fatalf("%d requests emitted %d audit records (%v); FR-041 requires exactly one each", len(rows), len(records), records)
			}
			for i, row := range rows {
				if got, want := records[i]["action"], string(audit.ActionAccessReject); got != want {
					t.Errorf("%s recorded as %v; want %v", row.name, got, want)
				}
				if got, want := records[i]["decision"], string(audit.Deny); got != want {
					t.Errorf("%s recorded decision %v; want %v", row.name, got, want)
				}
				if got, want := records[i]["caller"], audit.CallerUnknown; got != want {
					t.Errorf("%s recorded caller %v; want %v — no identity was established, so none may be named", row.name, got, want)
				}
			}
			if trail := f.sink.String(); strings.Contains(trail, planted.Name) {
				t.Errorf("the trail carries the session the outage withheld:\n%s", trail)
			}
		})
	}
}

// TestAnUnobtainableKeySetCostsOneFetchAndNoStartup is the "does not crash" half
// of the claim, in the two places a fail-closed path usually goes wrong.
//
// Startup first. SC-013 makes a missing or invalid layer-1 *configuration* a
// startup failure; whether the key source is answering this morning is not
// configuration, and a daemon that fetched at boot would refuse to start
// whenever its identity provider was having a bad one — taking the API door,
// which needs none of this, down beside the dashboard it cannot serve.
//
// Then the cost of each refusal. Anyone who can reach the listener can ask for
// the dashboard as often as they like, so a fetch per refused request would turn
// an outage at the edge into a load generator pointed at it, and would make
// every one of those requests wait out a fresh timeout of its own. The refetch
// floor is what prevents both, and this is the only place it is asserted through
// a route.
func TestAnUnobtainableKeySetCostsOneFetchAndNoStartup(t *testing.T) {
	t.Parallel()

	keys, fetches := newRefusingKeyServer(t)
	f := newFleetWith(t, keys)

	if got := fetches.Load(); got != 0 {
		t.Errorf("the daemon asked the key source %d times before any request arrived; reachability is not configuration, and a daemon that fetches at startup does not start during an outage", got)
	}

	const asks = 5
	for i := range asks {
		w := f.openWith(t, "/", f.keys.mint(t, f.keys.claims()))

		if w.Code != http.StatusUnauthorized {
			t.Fatalf("request %d = %d; want %d:\n%s", i+1, w.Code, http.StatusUnauthorized, w.Body.String())
		}
		if body := w.Body.String(); body != string(bodyBrowserRefused) {
			t.Fatalf("request %d answered %q; want the browser door's one refusal", i+1, body)
		}
	}

	if got := fetches.Load(); got != 1 {
		t.Errorf("%d refused requests asked the key source %d times; want 1 — the refetch floor is what keeps an outage from being amplified by whoever is knocking", asks, got)
	}
}

// TestABrowserRequestIsAnsweredWhenTheKeySourceNeverAnswers is the "does not
// hang" half, and the case no other fixture here can stand in for: a socket that
// accepts and then stays quiet is what an identity provider looks like when it
// is wedged rather than down, and it is the shape that turns a refusal into a
// request that never ends.
//
// Nothing in this package bounds it. The bound is internal/access's own client
// timeout, and this is the only test in the repository that would notice its
// removal — which is also why this test is the slowest one in the package: it
// waits for that timeout to elapse. The deadline below is deliberately far
// larger, because what is being asserted is that *a* bound exists and not what
// it is; a test tightened to the current value would fail on a busy machine and
// would have to be relaxed by whoever was least able to tell a hang from a
// hiccup.
func TestABrowserRequestIsAnsweredWhenTheKeySourceNeverAnswers(t *testing.T) {
	t.Parallel()

	f := newFleetWith(t, newSilentKeyServer(t))

	// Minted here rather than inside the goroutine: mint fails the test on error,
	// and t.Fatalf may only be called from the goroutine running the test.
	assertion := f.keys.mint(t, f.keys.claims())

	answered := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		r.Header.Set(headerAccessAssertion, assertion)

		w := httptest.NewRecorder()
		f.ServeHTTP(w, r)
		answered <- w
	}()

	const wedged = 60 * time.Second
	select {
	case w := <-answered:
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("GET / against a silent key source = %d; want %d:\n%s", w.Code, http.StatusUnauthorized, w.Body.String())
		}
		if body := w.Body.String(); body != string(bodyBrowserRefused) {
			t.Errorf("GET / against a silent key source answered %q; want the browser door's one refusal", body)
		}
	case <-time.After(wedged):
		t.Fatalf("GET / has been waiting on a silent key source for %s; the request never comes back, which is a daemon that hangs rather than one that refuses", wedged)
	}
}

// TestTheSignedAPIIsUnaffectedWhenTheKeysCannotBeObtained is what "the daemon
// does not crash" means to the caller who is not looking at a browser.
//
// An outage at the identity provider closes the dashboard, because the
// dashboard's whole credential is an assertion nothing can check. It must close
// nothing else: the six operations are authorised by a signature this daemon
// verifies with a secret it already holds, and they neither read an assertion
// nor wait on one (FR-012). T020 owns the general form of that claim — each door
// refuses only by the check that applies to it — and this is that claim during
// the one failure that could plausibly cross the two doors.
//
// The browser request first, so the sweep is not passing because the outage was
// never happening.
func TestTheSignedAPIIsUnaffectedWhenTheKeysCannotBeObtained(t *testing.T) {
	t.Parallel()

	f := newFleetWith(t, newDeadKeyServer(t))

	if w := f.openWith(t, "/", f.keys.mint(t, f.keys.claims())); w.Code != http.StatusUnauthorized {
		t.Fatalf("GET / = %d; want %d — the keys are obtainable after all, so nothing below is about an outage", w.Code, http.StatusUnauthorized)
	}

	for i, route := range routes {
		// A distinct instant per route: the signature covers the timestamp and
		// the body, so two identical empty-bodied requests would share one and
		// the replay cache would refuse the second.
		w := httptest.NewRecorder()
		f.ServeHTTP(w, requestFor(t, f.testServer, route, testTime.Add(-time.Duration(i)*time.Second)))

		if want := reachedStatus[route]; w.Code != want {
			t.Errorf("%s = %d; want %d — this door reads no assertion, so an unobtainable key set is not its business:\n%s",
				route, w.Code, want, w.Body.String())
		}
		if got := w.Header().Get(headerContentType); got != contentTypeJSON {
			t.Errorf("%s answered as %q; want %q — milestone 1's responses are frozen byte for byte", route, got, contentTypeJSON)
		}
	}
}
