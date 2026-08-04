// Package access implements layer 1 of the daemon's authentication: the
// Cloudflare Access assertion the browser door validates before it believes a
// browser is the operator (specs/002-access-dashboard/contracts/access-jwt.md).
//
// Layer 2 is internal/auth's HMAC signature and layer 3 is the per-session
// bearer token; this package knows about neither. The split is deliberate:
// auth proves a *request* is genuine, access proves a *person* is allowed, and
// the two have different failure modes. internal/auth continues to know
// nothing about browsers.
//
// Everything here is built from the standard library. A JWT library exists to
// handle the algorithms this daemon must refuse, and refusing them is the whole
// job (research D1); go.sum must not appear.
//
// This file is the signing key set: the edge's published RSA public keys, held
// in memory and refetched only when an assertion names a key id the cache has
// never seen. The verifier that consumes it is verify.go.
package access

import (
	"context"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"sync"
	"time"
)

// certsPath is where Cloudflare publishes the account's signing keys, appended
// to the configured team domain.
//
// Derived rather than configured, for the reason data-model.md gives: the
// issuer the assertion must name and the URL the keys come from are two
// readings of one value, and a second environment variable could hold them in
// disagreement — a validator checking signatures against the wrong authority.
const certsPath = "/cdn-cgi/access/certs"

const (
	// refetchFloor is the shortest interval between two fetch attempts.
	//
	// An unknown key id is the only refetch trigger, and anyone who can reach
	// the listener can mint assertions naming random key ids forever. Without a
	// floor each one is a command to call out to Cloudflare, so the daemon
	// becomes a traffic amplifier aimed at its own identity provider. Real
	// rotation happens on the order of weeks; a minute costs nothing legitimate.
	refetchFloor = 60 * time.Second

	// fetchTimeout bounds the whole exchange — dial, TLS, response, body. An
	// identity provider must not be able to stall the daemon any more than a
	// caller may.
	fetchTimeout = 5 * time.Second

	// maxKeySetBytes bounds what a fetch will read. A key set holding a handful
	// of 2048-bit keys is a kilobyte or two; this is generous by orders of
	// magnitude and still refuses a response that tries to balloon the daemon.
	maxKeySetBytes int64 = 64 << 10

	// minModulusBits is the smallest RSA key this cache will hold.
	//
	// The edge issues 2048-bit keys and crypto/rsa itself refuses to verify
	// under 1024, so the only entries this excludes are ones that could never be
	// a real Access key. Refusing them as they enter the cache keeps a weak key
	// from ever becoming a candidate the verifier has to decline.
	minModulusBits = 2048
)

// The reasons a key cannot be resolved. They are recorded server-side and never
// reach a caller: every layer-1 failure produces the one uniform 401 the
// contract fixes, and the trail's reason is a repo-authored constant chosen by
// the middleware (T009).
//
// None of them carries the key id, the response body, or the configured origin.
// The key id is caller-authored bytes, which the audit trail may never hold
// (milestone 1's FR-042), and the other two are the daemon's own business.
var (
	errKeyIDMissing = errors.New("the assertion names no signing key")

	errKeyIDUnknown = errors.New("the assertion was signed by a key the edge does not publish")

	// errRefetchFloored is the same conclusion reached without a fetch, kept
	// distinct so an operator reading the journal can tell "we asked and the key
	// is not there" from "we declined to ask again this soon".
	errRefetchFloored = fmt.Errorf("%w, and the refetch floor had not elapsed", errKeyIDUnknown)

	// errKeysUnobtainable is FR-009 in one value. A fetch that fails, times out,
	// or yields nothing usable refuses the request that needed it — an identity
	// that cannot be verified is not an identity, and there is no stale-hope
	// path that admits one.
	errKeysUnobtainable = errors.New("the edge's signing keys could not be obtained")
)

// clock is this package's view of time, injected for the reason auth.Clock and
// httpapi's limiter are: the refetch floor is measured in it, and a floor read
// from the wall clock could only be tested by sleeping for a minute.
//
// It is the seam every later time-based check takes: the assertion's own exp,
// nbf and iat are measured on it too, so a suite can settle the clock once and
// have both the floor and the token's validity window agree about now.
type clock interface{ Now() time.Time }

// systemClock is the host clock, chosen in exactly one place (New) so that
// everything below it takes the clock it was given.
type systemClock struct{}

func (systemClock) Now() time.Time { return time.Now() }

// keySet is the cached key set: the edge's RSA public keys by key id, plus the
// two facts the refetch rules are decided from.
//
// These are the *edge's* keys. Google signs nothing this daemon ever sees.
type keySet struct {
	// origin is the normalised scheme-and-host the key URL was built from, kept
	// because the issuer an assertion must name is that same origin. Reading it
	// back from here is what makes data-model.md's one-configured-value rule
	// true by construction: the address the keys came from and the authority the
	// assertion claims cannot be normalised into disagreement, because only one
	// normalisation ever runs.
	origin string
	url    string
	client *http.Client
	clock  clock

	// floor is a field rather than the constant read directly so that a test can
	// exercise the single-flight path without the floor deciding the outcome
	// for it. Nothing in production sets it.
	floor time.Duration

	mu   sync.Mutex
	keys map[string]*rsa.PublicKey
	// lastAttempt marks attempts, not successes: a failed fetch has already cost
	// the outbound request the floor exists to bound.
	lastAttempt time.Time
	inflight    *fetchCall
}

// fetchCall is one in-flight fetch, and what the callers who arrived while it
// was running wait on. Its error is written before done is closed, so a waiter
// that has received from the channel reads a settled value.
type fetchCall struct {
	done chan struct{}
	err  error
}

// newKeySet fails closed on a configuration the fetch could not use.
//
// config.loadTeamDomain has already normalised the origin and refused an
// http:// one off loopback; this is the assertion that the two cannot drift
// apart, not a second opinion about the scheme.
func newKeySet(teamDomain string, clk clock) (*keySet, error) {
	if teamDomain == "" {
		return nil, errors.New("access: no team domain configured for the signing keys; refusing to start")
	}
	if clk == nil {
		return nil, errors.New("access: no clock provided for the signing key set; refusing to start")
	}

	// Answered rather than wrapped, as config's own loader does it: url.Error
	// quotes the value it failed on, and the team domain names an organisation.
	u, err := url.Parse(teamDomain)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return nil, errors.New("access: the configured team domain is not an origin; refusing to start")
	}
	if u.Scheme != "https" && u.Scheme != "http" {
		return nil, errors.New("access: the configured team domain names no usable scheme; refusing to start")
	}

	origin := u.Scheme + "://" + u.Host
	return &keySet{
		origin: origin,
		url:    origin + certsPath,
		client: &http.Client{Timeout: fetchTimeout},
		clock:  clk,
		floor:  refetchFloor,
		keys:   make(map[string]*rsa.PublicKey),
	}, nil
}

// key resolves the key id a JOSE header named, fetching only when the cache
// cannot answer (FR-008).
//
// A cache miss is the one refetch trigger, because a rotated key announces
// itself as a key id nothing has seen before. There is no timed refresh: a
// second trigger would be a second thing to reason about, and the cost of not
// having one is one refused request at the instant of a rotation.
func (k *keySet) key(ctx context.Context, kid string) (*rsa.PublicKey, error) {
	// An assertion with no kid cannot select a key, and picking "the only one"
	// on its behalf would be the verifier deciding from attacker input.
	if kid == "" {
		return nil, errKeyIDMissing
	}

	if pub, ok := k.cached(kid); ok {
		return pub, nil
	}
	if err := k.refetch(ctx); err != nil {
		return nil, err
	}
	if pub, ok := k.cached(kid); ok {
		return pub, nil
	}
	return nil, errKeyIDUnknown
}

func (k *keySet) cached(kid string) (*rsa.PublicKey, bool) {
	k.mu.Lock()
	defer k.mu.Unlock()

	pub, ok := k.keys[kid]
	return pub, ok
}

// refetch runs at most one fetch at a time and at most one per floor interval.
//
// Single-flight matters on a cold start, where every request in the first
// moment misses an empty cache: without it the daemon answers its own first
// burst by opening a connection per request to the identity provider. The loser
// waits on the winner's result rather than starting a second fetch, and takes
// its own context's cancellation as the answer if the browser gives up first.
func (k *keySet) refetch(ctx context.Context) error {
	k.mu.Lock()
	if call := k.inflight; call != nil {
		k.mu.Unlock()
		select {
		case <-call.done:
			return call.err
		case <-ctx.Done():
			return fmt.Errorf("%w: the request ended while the keys were being fetched", errKeysUnobtainable)
		}
	}

	now := k.clock.Now()
	if !k.lastAttempt.IsZero() && now.Sub(k.lastAttempt) < k.floor {
		k.mu.Unlock()
		return errRefetchFloored
	}

	call := &fetchCall{done: make(chan struct{})}
	k.inflight = call
	k.lastAttempt = now
	k.mu.Unlock()

	// Outside the lock: a fetch takes as long as the network does, and holding
	// the mutex across it would make every cached lookup wait on it too.
	keys, err := k.fetch(ctx)

	k.mu.Lock()
	if err == nil {
		k.keys = keys
	}
	// A failed fetch leaves the cache exactly as it was. The keys already held
	// are still the edge's keys; what failed is learning about a new one.
	call.err = err
	k.inflight = nil
	k.mu.Unlock()
	close(call.done)

	return err
}

// fetch reads the published key set once, bounded in time and in size.
//
// The caller's cancellation is deliberately dropped: this fetch is shared with
// every caller waiting on it, so a browser that gave up must not be able to
// cancel a fetch the others are still waiting for — and a caller who could
// cancel at will could keep the cache empty on purpose. The bound is the
// client's own timeout, not the caller's patience.
func (k *keySet) fetch(ctx context.Context) (map[string]*rsa.PublicKey, error) {
	req, err := http.NewRequestWithContext(context.WithoutCancel(ctx), http.MethodGet, k.url, nil)
	if err != nil {
		return nil, fmt.Errorf("%w: the key set address is not usable", errKeysUnobtainable)
	}

	resp, err := k.client.Do(req)
	if err != nil {
		// The transport's own message is dropped rather than wrapped: it quotes
		// the URL it failed on, and this error is written to the journal.
		return nil, fmt.Errorf("%w: the key server could not be reached", errKeysUnobtainable)
	}

	// Read and closed here rather than in a defer. The body is read in exactly
	// one place, and a deferred close would either drop its error — which
	// AGENTS.md forbids outright — or need a named return to carry it.
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxKeySetBytes+1))
	if cerr := resp.Body.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return nil, fmt.Errorf("%w: the key set could not be read", errKeysUnobtainable)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%w: the key server answered %d", errKeysUnobtainable, resp.StatusCode)
	}
	// One byte past the limit was read on purpose, so a response at exactly the
	// limit is accepted and a longer one is refused rather than truncated into
	// something that might still parse.
	if int64(len(body)) > maxKeySetBytes {
		return nil, fmt.Errorf("%w: the key set exceeds %d bytes", errKeysUnobtainable, maxKeySetBytes)
	}

	// Unknown fields are ignored here, unlike every request body this daemon
	// decodes (docs/security.md §2). A JWK set is required by RFC 7517 to carry
	// members a reader does not recognise — Cloudflare's own set carries several
	// — so DisallowUnknownFields would refuse the real key set the day the
	// provider adds a field.
	var set jwkSet
	if err := json.Unmarshal(body, &set); err != nil {
		return nil, fmt.Errorf("%w: the key set is not the documented JSON", errKeysUnobtainable)
	}

	keys := make(map[string]*rsa.PublicKey, len(set.Keys))
	for _, entry := range set.Keys {
		if pub, ok := entry.publicKey(); ok {
			keys[entry.Kid] = pub
		}
	}
	if len(keys) == 0 {
		// A set that parsed but holds nothing usable is a failed fetch, not an
		// empty cache: treating it as success would replace working keys with
		// none and refuse every request until the next rotation.
		return nil, fmt.Errorf("%w: the key set carries no usable RSA key", errKeysUnobtainable)
	}
	return keys, nil
}

// jwkSet and jwk are the two documented fields of each entry plus the two that
// identify it. Anything else the provider publishes is ignored.
type jwkSet struct {
	Keys []jwk `json:"keys"`
}

type jwk struct {
	Kid string `json:"kid"`
	Kty string `json:"kty"`
	N   string `json:"n"`
	E   string `json:"e"`
}

// publicKey reports whether this entry is a usable RSA key, and builds it.
//
// An entry that is not is skipped rather than failing the whole fetch: a key
// set may legitimately carry an entry for something this daemon does not
// verify, and refusing the set for it would take the working keys down with it.
// The set as a whole still has to yield at least one key.
func (j jwk) publicKey() (*rsa.PublicKey, bool) {
	// A key with no id can never be selected — step 4 resolves by kid — so it
	// would sit in the cache as a key nothing can name.
	if j.Kid == "" || j.Kty != "RSA" {
		return nil, false
	}

	// RawURLEncoding, unpadded, as RFC 7518 requires. Strictness costs nothing
	// here: the provider that publishes a padded value publishes it to every
	// validator.
	n, err := base64.RawURLEncoding.DecodeString(j.N)
	if err != nil || len(n) == 0 {
		return nil, false
	}
	e, err := base64.RawURLEncoding.DecodeString(j.E)
	// Four bytes is 2^32; an RSA exponent wider than that is not one this
	// validator will ever meet, and the limit is what keeps the assembly below
	// inside an int.
	if err != nil || len(e) == 0 || len(e) > 4 {
		return nil, false
	}

	modulus := new(big.Int).SetBytes(n)
	if modulus.BitLen() < minModulusBits {
		return nil, false
	}

	// Assembled byte by byte rather than converted from a big.Int, so no
	// conversion can overflow and no bound has to be restated.
	exponent := 0
	for _, b := range e {
		exponent = exponent<<8 | int(b)
	}
	// An even exponent, or one below three, cannot be an RSA public exponent.
	if exponent < 3 || exponent%2 == 0 {
		return nil, false
	}

	return &rsa.PublicKey{N: modulus, E: exponent}, true
}
