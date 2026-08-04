// An *internal* test (package access), like internal/auth's replay_test.go and
// for the same reason: the properties FR-008 fixes are statements about the
// cache, not about a response. "Did that lookup cause a fetch?" and "is the
// floor holding?" cannot be asked through an exported surface without growing
// one that exists only so a test can call it.
package access

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// The instant the clock starts at. Milestone 1's fixtures use the example
// timestamp from contracts/http-api.md; this package has no such example, so it
// takes the same one rather than inventing a second epoch.
const keysTimestamp int64 = 1785706480

// signingKeys are generated once for the whole package. A 2048-bit key pair is
// hundreds of milliseconds and every case below needs at least one, so
// generating per test would trade the suite's runtime for nothing.
//
// Two, because the cases here need the key the daemon trusts and the rotation
// that replaces it. A later task wanting a third adds it here.
var signingKeys = sync.OnceValue(func() [2]*rsa.PrivateKey {
	var keys [2]*rsa.PrivateKey
	for i := range keys {
		k, err := rsa.GenerateKey(rand.Reader, 2048)
		if err != nil {
			panic("generating a test signing key: " + err.Error())
		}
		keys[i] = k
	}
	return keys
})

func signingKey(t *testing.T, n int) *rsa.PrivateKey {
	t.Helper()
	return signingKeys()[n]
}

func b64url(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

// jwkFor spells the entry out as the provider publishes it, independently of
// jwk.publicKey. Building the fixture by calling the code under test would
// mirror a decoding bug into the thing meant to catch it.
func jwkFor(t *testing.T, kid string, pub *rsa.PublicKey) string {
	t.Helper()
	return fmt.Sprintf(`{"kid":%q,"kty":"RSA","alg":"RS256","use":"sig","n":%q,"e":%q}`,
		kid, b64url(pub.N.Bytes()), b64url(big.NewInt(int64(pub.E)).Bytes()))
}

func keySetJSON(entries ...string) string {
	return `{"keys":[` + strings.Join(entries, ",") + `]}`
}

// keyServer is the loopback stand-in for the edge's certs endpoint: it counts
// what it is asked for, can be made to fail, and can be held open so a race can
// be arranged rather than hoped for.
type keyServer struct {
	srv *httptest.Server

	mu      sync.Mutex
	body    string
	status  int
	fetches int
	paths   []string
	hold    chan struct{}
	started chan struct{}
}

func newKeyServer(t *testing.T, body string) *keyServer {
	t.Helper()

	ks := &keyServer{body: body, status: http.StatusOK, started: make(chan struct{})}
	var once sync.Once
	ks.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ks.mu.Lock()
		ks.fetches++
		ks.paths = append(ks.paths, r.URL.Path)
		hold, body, status := ks.hold, ks.body, ks.status
		ks.mu.Unlock()

		once.Do(func() { close(ks.started) })
		if hold != nil {
			<-hold
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		if _, err := io.WriteString(w, body); err != nil {
			t.Errorf("serving the key set: %v", err)
		}
	}))
	t.Cleanup(ks.srv.Close)
	return ks
}

func (k *keyServer) URL() string { return k.srv.URL }

func (k *keyServer) serve(body string, status int) {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.body, k.status = body, status
}

// holdRequests makes every request wait, and returns the function that lets
// them go.
//
// The release is idempotent and also runs on the way out of the test, whatever
// the reason. httptest's own Close waits for the handler this is holding, so a
// t.Fatal between the hold and the release would wedge the suite — the failure
// would arrive as a hung run rather than as the assertion that failed. Learned
// by mutating the code under test until this test failed.
func (k *keyServer) holdRequests(t *testing.T) func() {
	t.Helper()

	held := make(chan struct{})
	var once sync.Once
	release := func() { once.Do(func() { close(held) }) }
	t.Cleanup(release)

	k.mu.Lock()
	defer k.mu.Unlock()
	k.hold = held
	return release
}

func (k *keyServer) fetchCount() int {
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.fetches
}

func (k *keyServer) requestedPaths() []string {
	k.mu.Lock()
	defer k.mu.Unlock()
	return append([]string(nil), k.paths...)
}

// stepClock is the advanceable clock the refetch floor is measured on. Guarded,
// because the stampede test reads it from every goroutine at once.
type stepClock struct {
	mu  sync.Mutex
	now time.Time
}

func newStepClock() *stepClock {
	return &stepClock{now: time.Unix(keysTimestamp, 0)}
}

func (c *stepClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *stepClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

func mustKeySet(t *testing.T, teamDomain string, clk clock) *keySet {
	t.Helper()

	ks, err := newKeySet(teamDomain, clk)
	if err != nil {
		t.Fatalf("newKeySet(%q): %v", teamDomain, err)
	}
	return ks
}

func mustKey(t *testing.T, ks *keySet, kid string) *rsa.PublicKey {
	t.Helper()

	pub, err := ks.key(context.Background(), kid)
	if err != nil {
		t.Fatalf("key(%q): %v", kid, err)
	}
	return pub
}

// TestKeySetServesFromCacheAndFetchesOnce is FR-008's positive half: keys are
// cached and never fetched per request.
func TestKeySetServesFromCacheAndFetchesOnce(t *testing.T) {
	t.Parallel()

	key := signingKey(t, 0)
	srv := newKeyServer(t, keySetJSON(jwkFor(t, "k1", &key.PublicKey)))
	ks := mustKeySet(t, srv.URL(), newStepClock())

	for i := range 3 {
		got := mustKey(t, ks, "k1")
		if got.N.Cmp(key.N) != 0 || got.E != key.E {
			t.Fatalf("lookup %d resolved a key that is not the published one", i)
		}
	}

	if n := srv.fetchCount(); n != 1 {
		t.Fatalf("three lookups produced %d fetches, want 1: FR-008 forbids a fetch per request", n)
	}
	if paths := srv.requestedPaths(); len(paths) != 1 || paths[0] != certsPath {
		t.Fatalf("fetched %v, want exactly [%s] derived from the team domain", paths, certsPath)
	}
}

// TestKeySetRefetchesOnAnUnknownKeyID covers the one refetch trigger: a
// rotation announces itself as a key id the cache has never seen.
func TestKeySetRefetchesOnAnUnknownKeyID(t *testing.T) {
	t.Parallel()

	old, rotated := signingKey(t, 0), signingKey(t, 1)
	srv := newKeyServer(t, keySetJSON(jwkFor(t, "old", &old.PublicKey)))
	clk := newStepClock()
	ks := mustKeySet(t, srv.URL(), clk)

	mustKey(t, ks, "old")

	srv.serve(keySetJSON(jwkFor(t, "rotated", &rotated.PublicKey)), http.StatusOK)
	clk.Advance(refetchFloor)

	got := mustKey(t, ks, "rotated")
	if got.N.Cmp(rotated.N) != 0 {
		t.Fatal("the refetch did not pick up the rotated key")
	}
	if n := srv.fetchCount(); n != 2 {
		t.Fatalf("an unknown key id produced %d fetches in total, want 2", n)
	}
}

// TestKeySetRefetchesOnceForAnUnknownKeyID: the refetch happens exactly once
// and the request is then refused, rather than the miss driving a retry loop.
func TestKeySetRefetchesOnceForAnUnknownKeyID(t *testing.T) {
	t.Parallel()

	key := signingKey(t, 0)
	srv := newKeyServer(t, keySetJSON(jwkFor(t, "k1", &key.PublicKey)))
	clk := newStepClock()
	ks := mustKeySet(t, srv.URL(), clk)

	mustKey(t, ks, "k1")
	clk.Advance(refetchFloor)

	pub, err := ks.key(context.Background(), "never-published")
	if pub != nil || !errors.Is(err, errKeyIDUnknown) {
		t.Fatalf("key(unknown) = %v, %v; want no key and errKeyIDUnknown", pub, err)
	}
	if n := srv.fetchCount(); n != 2 {
		t.Fatalf("an unknown key id produced %d fetches, want 2 — one refetch, not a loop", n)
	}
}

// TestKeySetHonoursTheRefetchFloor: an unknown-key-id storm is an attack, not a
// rotation, and must not become an outbound request flood.
func TestKeySetHonoursTheRefetchFloor(t *testing.T) {
	t.Parallel()

	key := signingKey(t, 0)
	srv := newKeyServer(t, keySetJSON(jwkFor(t, "k1", &key.PublicKey)))
	clk := newStepClock()
	ks := mustKeySet(t, srv.URL(), clk)

	mustKey(t, ks, "k1") // the first fetch, and the mark the floor runs from

	// Offsets from the fetch, not steps, so the storm stays inside the floor
	// however many entries the table grows.
	var elapsed time.Duration
	for _, offset := range []time.Duration{0, time.Second, refetchFloor / 2, refetchFloor - time.Nanosecond} {
		clk.Advance(offset - elapsed)
		elapsed = offset

		if _, err := ks.key(context.Background(), fmt.Sprintf("storm-%s", offset)); !errors.Is(err, errKeyIDUnknown) {
			t.Fatalf("%v into the floor: err = %v, want errKeyIDUnknown", offset, err)
		}
		if n := srv.fetchCount(); n != 1 {
			t.Fatalf("%v into the floor: %d fetches, want 1 — the floor did not hold", offset, n)
		}
	}

	// The cache still answers what it already knows while the floor holds: the
	// floor bounds fetching, not serving.
	mustKey(t, ks, "k1")

	clk.Advance(refetchFloor - elapsed)
	if _, err := ks.key(context.Background(), "still-unknown"); !errors.Is(err, errKeyIDUnknown) {
		t.Fatalf("past the floor: err = %v, want errKeyIDUnknown", err)
	}
	if n := srv.fetchCount(); n != 2 {
		t.Fatalf("past the floor: %d fetches, want 2 — the floor never lifted", n)
	}
}

// TestKeySetFailsClosedWhenUnreachable is FR-009: an identity that cannot be
// verified is not an identity.
func TestKeySetFailsClosedWhenUnreachable(t *testing.T) {
	t.Parallel()

	srv := newKeyServer(t, keySetJSON(jwkFor(t, "k1", &signingKey(t, 0).PublicKey)))
	url := srv.URL()
	srv.srv.Close() // nothing is listening from here on

	ks := mustKeySet(t, url, newStepClock())

	pub, err := ks.key(context.Background(), "k1")
	if pub != nil || !errors.Is(err, errKeysUnobtainable) {
		t.Fatalf("key() with the key server down = %v, %v; want no key and errKeysUnobtainable", pub, err)
	}
}

// TestKeySetRetriesAfterAFailedFetch: a failed fetch refuses the request that
// needed it, and a later request past the floor tries again.
func TestKeySetRetriesAfterAFailedFetch(t *testing.T) {
	t.Parallel()

	key := signingKey(t, 0)
	srv := newKeyServer(t, "")
	srv.serve("", http.StatusServiceUnavailable)
	clk := newStepClock()
	ks := mustKeySet(t, srv.URL(), clk)

	if _, err := ks.key(context.Background(), "k1"); !errors.Is(err, errKeysUnobtainable) {
		t.Fatalf("first request = %v, want errKeysUnobtainable", err)
	}

	srv.serve(keySetJSON(jwkFor(t, "k1", &key.PublicKey)), http.StatusOK)
	clk.Advance(refetchFloor)

	if got := mustKey(t, ks, "k1"); got.N.Cmp(key.N) != 0 {
		t.Fatal("the retry resolved a key that is not the published one")
	}
	if n := srv.fetchCount(); n != 2 {
		t.Fatalf("%d fetches, want 2 — the failure must not have poisoned the cache", n)
	}
}

// TestKeySetKeepsTheCacheWhenAFetchFails: a failed refetch leaves the keys
// already held in place. Replacing them with nothing would refuse every request
// until the next rotation.
func TestKeySetKeepsTheCacheWhenAFetchFails(t *testing.T) {
	t.Parallel()

	key := signingKey(t, 0)
	srv := newKeyServer(t, keySetJSON(jwkFor(t, "k1", &key.PublicKey)))
	clk := newStepClock()
	ks := mustKeySet(t, srv.URL(), clk)

	mustKey(t, ks, "k1")

	// A set that parses but carries nothing usable is a failed fetch, not an
	// empty cache.
	srv.serve(keySetJSON(), http.StatusOK)
	clk.Advance(refetchFloor)

	if _, err := ks.key(context.Background(), "k2"); !errors.Is(err, errKeysUnobtainable) {
		t.Fatalf("an empty key set = %v, want errKeysUnobtainable", err)
	}
	if got := mustKey(t, ks, "k1"); got.N.Cmp(key.N) != 0 {
		t.Fatal("the failed refetch took the cached key with it")
	}
}

// TestKeySetSkipsUnusableEntries defines what "usable n and e" means, one
// defect at a time. An unusable entry is skipped rather than failing the whole
// set — a provider may publish something this daemon does not verify — but a
// set that yields nothing is a failed fetch.
func TestKeySetSkipsUnusableEntries(t *testing.T) {
	t.Parallel()

	good := signingKey(t, 0)
	n := b64url(good.N.Bytes())
	// Half a modulus is 1024 bits: under the minimum, and built by truncation
	// because generating a real undersized key is the one thing gosec's G403
	// exists to stop anybody doing on purpose.
	half := b64url(good.N.Bytes()[:len(good.N.Bytes())/2])

	cases := []struct {
		name  string
		kid   string
		entry string
	}{
		{"not an RSA key", "ec", fmt.Sprintf(`{"kid":"ec","kty":"EC","n":%q,"e":"AQAB"}`, n)},
		{"no key id", "", fmt.Sprintf(`{"kty":"RSA","n":%q,"e":"AQAB"}`, n)},
		{"modulus is not base64url", "bad-n", `{"kid":"bad-n","kty":"RSA","n":"not base64!","e":"AQAB"}`},
		{"modulus is padded base64", "padded-n", fmt.Sprintf(`{"kid":"padded-n","kty":"RSA","n":%q,"e":"AQAB"}`, base64.URLEncoding.EncodeToString(good.N.Bytes()))},
		{"empty modulus", "empty-n", `{"kid":"empty-n","kty":"RSA","n":"","e":"AQAB"}`},
		{"empty exponent", "empty-e", fmt.Sprintf(`{"kid":"empty-e","kty":"RSA","n":%q,"e":""}`, n)},
		{"exponent is not base64url", "bad-e", fmt.Sprintf(`{"kid":"bad-e","kty":"RSA","n":%q,"e":"!!"}`, n)},
		{"exponent wider than four bytes", "wide-e", fmt.Sprintf(`{"kid":"wide-e","kty":"RSA","n":%q,"e":%q}`, n, b64url([]byte{1, 0, 0, 0, 1}))},
		{"even exponent", "even-e", fmt.Sprintf(`{"kid":"even-e","kty":"RSA","n":%q,"e":%q}`, n, b64url([]byte{2}))},
		{"exponent below three", "one-e", fmt.Sprintf(`{"kid":"one-e","kty":"RSA","n":%q,"e":%q}`, n, b64url([]byte{1}))},
		{"modulus below the minimum", "small", fmt.Sprintf(`{"kid":"small","kty":"RSA","n":%q,"e":"AQAB"}`, half)},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			alone := newKeyServer(t, keySetJSON(tc.entry))
			lonely := mustKeySet(t, alone.URL(), newStepClock())
			if _, err := lonely.key(context.Background(), "anything"); !errors.Is(err, errKeysUnobtainable) {
				t.Fatalf("a set holding only this entry = %v, want errKeysUnobtainable", err)
			}

			mixed := newKeyServer(t, keySetJSON(tc.entry, jwkFor(t, "good", &good.PublicKey)))
			ks := mustKeySet(t, mixed.URL(), newStepClock())
			if got := mustKey(t, ks, "good"); got.N.Cmp(good.N) != 0 {
				t.Fatal("the usable entry alongside it did not survive the fetch")
			}
			if tc.kid != "" {
				if pub, cached := ks.cached(tc.kid); cached {
					t.Fatalf("the unusable entry entered the cache as %v", pub)
				}
			}
		})
	}
}

// TestKeySetRefusesAnOversizedResponse: an identity provider must not be able
// to balloon the daemon any more than a caller may.
func TestKeySetRefusesAnOversizedResponse(t *testing.T) {
	t.Parallel()

	key := signingKey(t, 0)
	set := keySetJSON(jwkFor(t, "k1", &key.PublicKey))

	// Padded with leading whitespace, which JSON ignores, so the only thing
	// separating the two cases is the byte count.
	atLimit := strings.Repeat(" ", int(maxKeySetBytes)-len(set)) + set
	overLimit := " " + atLimit

	srv := newKeyServer(t, atLimit)
	ks := mustKeySet(t, srv.URL(), newStepClock())
	if got := mustKey(t, ks, "k1"); got.N.Cmp(key.N) != 0 {
		t.Fatal("a response of exactly the limit was refused")
	}

	over := newKeyServer(t, overLimit)
	oversized := mustKeySet(t, over.URL(), newStepClock())
	if _, err := oversized.key(context.Background(), "k1"); !errors.Is(err, errKeysUnobtainable) {
		t.Fatalf("a response one byte past the limit = %v, want errKeysUnobtainable", err)
	}
}

// TestKeySetRefusesAnUnusableResponse covers every shape of answer that is not
// a key set: the wrong status, and a body that is not the documented JSON.
func TestKeySetRefusesAnUnusableResponse(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		body   string
		status int
	}{
		{"not found", "", http.StatusNotFound},
		{"server error", "", http.StatusInternalServerError},
		{"unavailable", "", http.StatusServiceUnavailable},
		{"a key set behind a redirect status", keySetJSON(), http.StatusFound},
		{"not JSON", "<html>login</html>", http.StatusOK},
		{"JSON, but not a key set", `{"error":"forbidden"}`, http.StatusOK},
		{"no keys at all", keySetJSON(), http.StatusOK},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			srv := newKeyServer(t, tc.body)
			srv.serve(tc.body, tc.status)
			ks := mustKeySet(t, srv.URL(), newStepClock())

			pub, err := ks.key(context.Background(), "k1")
			if pub != nil || !errors.Is(err, errKeysUnobtainable) {
				t.Fatalf("key() = %v, %v; want no key and errKeysUnobtainable", pub, err)
			}
		})
	}
}

// TestKeySetConcurrentFirstFetchesDoNotStampede is the edge case the spec
// names: two requests racing the first fetch produce one fetch, with the losers
// waiting on the winner's result rather than opening their own connection.
//
// The race is arranged rather than hoped for — the server holds the first
// request until every goroutine has entered key() — so a pass cannot be the
// accident of them running one after another.
func TestKeySetConcurrentFirstFetchesDoNotStampede(t *testing.T) {
	t.Parallel()

	key := signingKey(t, 0)
	srv := newKeyServer(t, keySetJSON(jwkFor(t, "k1", &key.PublicKey)))
	release := srv.holdRequests(t)
	ks := mustKeySet(t, srv.URL(), newStepClock())

	const callers = 8
	var entered, done sync.WaitGroup
	entered.Add(callers)
	done.Add(callers)
	results := make([]*rsa.PublicKey, callers)
	errs := make([]error, callers)

	for i := range callers {
		go func() {
			defer done.Done()
			entered.Done()
			results[i], errs[i] = ks.key(context.Background(), "k1")
		}()
	}

	entered.Wait()
	release()
	done.Wait()

	for i := range callers {
		if errs[i] != nil {
			t.Fatalf("caller %d: %v", i, errs[i])
		}
		if results[i].N.Cmp(key.N) != 0 {
			t.Fatalf("caller %d resolved a key that is not the published one", i)
		}
	}
	if n := srv.fetchCount(); n != 1 {
		t.Fatalf("%d callers racing the first fetch produced %d fetches, want 1", callers, n)
	}
}

// TestKeySetWaiterTakesItsOwnCancellation: a caller waiting on someone else's
// fetch answers to its own context. The fetch itself is not cancelled with it —
// it is shared, and the callers still waiting are entitled to its result.
func TestKeySetWaiterTakesItsOwnCancellation(t *testing.T) {
	t.Parallel()

	key := signingKey(t, 0)
	srv := newKeyServer(t, keySetJSON(jwkFor(t, "k1", &key.PublicKey)))
	release := srv.holdRequests(t)
	ks := mustKeySet(t, srv.URL(), newStepClock())

	winner := make(chan error, 1)
	go func() {
		_, err := ks.key(context.Background(), "k1")
		winner <- err
	}()
	<-srv.started // the fetch is registered and in flight

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := ks.key(ctx, "k1"); !errors.Is(err, errKeysUnobtainable) {
		t.Fatalf("a cancelled waiter = %v, want errKeysUnobtainable", err)
	}

	release()
	if err := <-winner; err != nil {
		t.Fatalf("the waiter's cancellation reached the shared fetch: %v", err)
	}
	if n := srv.fetchCount(); n != 1 {
		t.Fatalf("%d fetches, want 1", n)
	}
}

// TestKeySetRefusesAnAssertionNamingNoKey: no key id means no key, and never
// "the only one in the cache" — that would be the verifier choosing from
// attacker input.
func TestKeySetRefusesAnAssertionNamingNoKey(t *testing.T) {
	t.Parallel()

	srv := newKeyServer(t, keySetJSON(jwkFor(t, "k1", &signingKey(t, 0).PublicKey)))
	ks := mustKeySet(t, srv.URL(), newStepClock())

	pub, err := ks.key(context.Background(), "")
	if pub != nil || !errors.Is(err, errKeyIDMissing) {
		t.Fatalf("key(\"\") = %v, %v; want no key and errKeyIDMissing", pub, err)
	}
	if n := srv.fetchCount(); n != 0 {
		t.Fatalf("an empty key id produced %d fetches, want 0", n)
	}
}

// TestNewKeySetRefusesAnUnusableConfiguration is the second check on a value
// config.loadTeamDomain has already normalised — the assertion that the two
// cannot drift apart.
func TestNewKeySetRefusesAnUnusableConfiguration(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name       string
		teamDomain string
	}{
		{"empty", ""},
		{"no scheme", "team.cloudflareaccess.com"},
		{"no host", "https://"},
		{"carries a path", "https://team.cloudflareaccess.com/cdn-cgi"},
		{"carries a query", "https://team.cloudflareaccess.com?a=1"},
		{"carries a fragment", "https://team.cloudflareaccess.com#f"},
		{"carries credentials", "https://user:pass@team.cloudflareaccess.com"},
		{"not an http scheme", "ftp://team.cloudflareaccess.com"},
		{"not a URL at all", "://"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			ks, err := newKeySet(tc.teamDomain, newStepClock())
			if err == nil {
				t.Fatalf("newKeySet(%q) built %v, want a refusal", tc.teamDomain, ks)
			}
			if strings.Contains(err.Error(), tc.teamDomain) && tc.teamDomain != "" {
				t.Fatalf("the refusal quotes the configured value: %v", err)
			}
		})
	}
}

func TestNewKeySetRefusesANilClock(t *testing.T) {
	t.Parallel()

	if _, err := newKeySet("https://team.cloudflareaccess.com", nil); err == nil {
		t.Fatal("newKeySet accepted a nil clock; the refetch floor would panic on the first miss")
	}
}

// TestKeySetURLIsDerivedFromTheTeamDomain: one configured value, two
// derivations. A separately configured key-set URL could disagree with the
// issuer it must belong to.
func TestKeySetURLIsDerivedFromTheTeamDomain(t *testing.T) {
	t.Parallel()

	cases := []struct{ domain, want string }{
		{"https://team.cloudflareaccess.com", "https://team.cloudflareaccess.com" + certsPath},
		{"https://team.cloudflareaccess.com/", "https://team.cloudflareaccess.com" + certsPath},
		{"http://127.0.0.1:8099", "http://127.0.0.1:8099" + certsPath},
	}

	for _, tc := range cases {
		ks := mustKeySet(t, tc.domain, newStepClock())
		if ks.url != tc.want {
			t.Errorf("newKeySet(%q).url = %q, want %q", tc.domain, ks.url, tc.want)
		}
	}
}
