package auth_test

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nctiggy/claude-remote-session-webhook/internal/auth"
)

// Spelled in words rather than hex, for the reason config_test.go records: a run
// of hex characters next to the word "secret" is what a real HMAC key looks
// like, and the repo's gitleaks rules are right to say so. Both are exactly the
// 32 bytes config.MinSecretBytes requires, so the fixtures are realistic.
const (
	testSecret  = "test-only-shared-secret-for-auth"
	otherSecret = "test-only-second-secret-for-auth"
)

// testTimestamp is the example instant in contracts/http-api.md. Every fixture
// is signed at it and the injected clock is placed relative to it, so a case can
// move one of the two and blame the outcome on the distance between them.
const testTimestamp int64 = 1785706480

// testWindow restates FR-008's 300 seconds rather than reading the package's own
// constant. A test that imports the number it is checking cannot notice the
// number changing — this one fails loudly if the window is ever widened.
const testWindow = 300 * time.Second

// testMaxBody is small enough to build an oversize body in a test and large
// enough that no ordinary fixture brushes it.
const testMaxBody int64 = 4096

// signatureOver recomputes the header value from the contract's own
// description, independently of the code under test. A shared helper inside
// the package would mirror a bug in the payload layout instead of catching it.
func signatureOver(t *testing.T, secret string, timestamp int64, body string) string {
	t.Helper()

	mac := hmac.New(sha256.New, []byte(secret))
	if _, err := io.WriteString(mac, strconv.FormatInt(timestamp, 10)+"."+body); err != nil {
		t.Fatalf("building the signature fixture: %v", err)
	}
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

// fakeClock is stopped at one instant. A window boundary is then exact and no
// test sleeps to reach it. Immutable, so the parallel tests can share one
// without a lock.
type fakeClock struct{ now time.Time }

func (c fakeClock) Now() time.Time { return c.now }

// driftedClock places the daemon's clock a given distance from the instant the
// fixtures are signed at. A **positive** drift puts the daemon ahead, making the
// request stale; a **negative** drift leaves the request in the future.
func driftedClock(drift time.Duration) fakeClock {
	return fakeClock{now: time.Unix(testTimestamp, 0).Add(drift)}
}

func newAuth(t *testing.T) *auth.Authenticator {
	t.Helper()

	return newAuthDrifting(t, 0)
}

func newAuthDrifting(t *testing.T, drift time.Duration) *auth.Authenticator {
	t.Helper()

	a, err := auth.NewWithClock([]byte(testSecret), testMaxBody, driftedClock(drift))
	if err != nil {
		t.Fatalf("auth.NewWithClock() unexpected error: %v", err)
	}
	return a
}

// validRequest verifies cleanly, so each table case can change exactly one
// thing and blame the failure on it. A case that stops failing is then a real
// regression rather than a fixture that rotted.
func validRequest(t *testing.T, body string) *http.Request {
	t.Helper()

	return requestAt(t, testTimestamp, body)
}

// requestAt signs a request as of an arbitrary instant. A case that moves the
// timestamp this way keeps a matching signature, so the window has to be the
// reason the request is refused — not a MAC over the wrong bytes.
func requestAt(t *testing.T, timestamp int64, body string) *http.Request {
	t.Helper()

	r := httptest.NewRequest(http.MethodPost, "/sessions", strings.NewReader(body))
	r.Header.Set(auth.HeaderTimestamp, strconv.FormatInt(timestamp, 10))
	r.Header.Set(auth.HeaderSignature, signatureOver(t, testSecret, timestamp, body))
	return r
}

// verifyReason runs Verify and returns the *server-side* reason behind a
// refusal, which is what the cases in this file are about. What Verify itself
// returns is opaque and identical for every failure by design; caller_test.go is
// where that is asserted.
//
// It also holds the invariant every case here shares for free: an identity comes
// back exactly when the request was accepted, so no test in this file can pass
// against a Verify that names a caller for a request it refused.
func verifyReason(t *testing.T, a *auth.Authenticator, r *http.Request) error {
	t.Helper()

	caller, err := a.Verify(r)
	switch {
	case err != nil && caller != nil:
		t.Error("Verify() returned a Caller alongside a denial")
	case err == nil && caller == nil:
		t.Error("Verify() accepted a request without naming its caller")
	}
	return auth.Reason(err)
}

func hexPart(t *testing.T, signature string) string {
	t.Helper()

	rest, ok := strings.CutPrefix(signature, "sha256=")
	if !ok {
		t.Fatalf("signature fixture %q has no sha256= prefix", signature)
	}
	return rest
}

// TestVerifyAcceptsASignedRequest pins the signed payload — timestamp, a dot,
// then the raw body — against the independent fixture above. The bodies are
// chosen so that a layout bug shows up rather than cancelling out.
func TestVerifyAcceptsASignedRequest(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		body string
	}{
		{name: "no body at all", body: ""},
		{name: "a create request", body: `{"name":"demo","work_dir":"/home/u/code/x"}`},
		{name: "body starting with the separator", body: ".not-a-timestamp"},
		{name: "body of digits and dots", body: "1785706480.1785706480"},
		{name: "embedded newlines", body: "first\nsecond\n"},
		{name: "hostile prompt bytes", body: "a; echo PWNED; $(id) `whoami`"},
		{name: "invalid utf-8", body: "\xff\xfe\x00binary"},
		{name: "exactly the maximum", body: strings.Repeat("a", int(testMaxBody))},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			r := validRequest(t, tt.body)
			if err := verifyReason(t, newAuth(t), r); err != nil {
				t.Fatalf("Verify() rejected a correctly signed request: %v", err)
			}

			// The handler behind Verify still needs the bytes the signature
			// covered, not a drained reader.
			got, err := io.ReadAll(r.Body)
			if err != nil {
				t.Fatalf("reading the re-buffered body: %v", err)
			}
			if string(got) != tt.body {
				t.Errorf("re-buffered body = %q, want the signed bytes back", got)
			}
		})
	}
}

// TestVerifyCanonicalisesTheTimestamp pins the decision that the signed payload
// carries the *parsed* instant, not the header's spelling of it: one moment has
// exactly one signature, which is what the replay cache will key on (T011).
func TestVerifyCanonicalisesTheTimestamp(t *testing.T) {
	t.Parallel()

	r := validRequest(t, "")
	r.Header.Set(auth.HeaderTimestamp, "+"+strconv.FormatInt(testTimestamp, 10))

	if err := verifyReason(t, newAuth(t), r); err != nil {
		t.Fatalf("Verify() rejected a request whose timestamp header carries a redundant sign: %v", err)
	}
}

// TestVerifyAcceptsTimestampsInsideTheWindow walks the accepted range out to
// both edges. `|now - ts| <= 300s` includes the boundary itself, so a request
// exactly 300 seconds old is still good.
func TestVerifyAcceptsTimestampsInsideTheWindow(t *testing.T) {
	t.Parallel()

	const body = `{"name":"demo","work_dir":"/home/u/code/x"}`

	tests := []struct {
		name  string
		drift time.Duration
	}{
		{name: "the clocks agree exactly", drift: 0},
		{name: "one second stale", drift: time.Second},
		{name: "one second in the future", drift: -time.Second},
		{name: "stale by a minute", drift: time.Minute},
		{name: "a minute in the future", drift: -time.Minute},
		{name: "a nanosecond inside the stale edge", drift: testWindow - time.Nanosecond},
		{name: "a nanosecond inside the future edge", drift: -testWindow + time.Nanosecond},
		{name: "stale by exactly the window", drift: testWindow},
		{name: "exactly the window into the future", drift: -testWindow},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if err := verifyReason(t, newAuthDrifting(t, tt.drift), requestAt(t, testTimestamp, body)); err != nil {
				t.Fatalf("Verify() rejected a request %v from the daemon's clock: %v", tt.drift, err)
			}
		})
	}
}

// TestVerifyRejectsTimestampsOutsideTheWindow is the other half of FR-008, and
// the future rows are the point of it: an implementation that only asks "is
// this too old?" passes every other test in this file.
func TestVerifyRejectsTimestampsOutsideTheWindow(t *testing.T) {
	t.Parallel()

	const body = `{"name":"demo","work_dir":"/home/u/code/x"}`

	tests := []struct {
		name  string
		drift time.Duration
	}{
		{name: "a nanosecond past the stale edge", drift: testWindow + time.Nanosecond},
		{name: "a nanosecond past the future edge", drift: -testWindow - time.Nanosecond},
		{name: "a second past the stale edge", drift: testWindow + time.Second},
		{name: "a second past the future edge", drift: -testWindow - time.Second},
		{name: "stale by twice the window", drift: 2 * testWindow},
		{name: "twice the window into the future", drift: -2 * testWindow},
		{name: "stale by an hour", drift: time.Hour},
		{name: "an hour into the future", drift: -time.Hour},
		{name: "stale by a year", drift: 365 * 24 * time.Hour},
		{name: "a year into the future", drift: -365 * 24 * time.Hour},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := verifyReason(t, newAuthDrifting(t, tt.drift), requestAt(t, testTimestamp, body))
			if !errors.Is(err, auth.ErrTimestampOutsideWindow) {
				t.Fatalf("Verify() error = %v, want %v for a request %v from the daemon's clock",
					err, auth.ErrTimestampOutsideWindow, tt.drift)
			}
		})
	}
}

// TestVerifyRejectsAFarFutureTimestamp states the consequence the table above
// only implies. A timestamp bounded on one side never goes stale, so one
// captured request stamped a year ahead would be a key that still works next
// summer — and the replay cache cannot save it, because that forgets a
// signature after twice the window.
func TestVerifyRejectsAFarFutureTimestamp(t *testing.T) {
	t.Parallel()

	const aYear = 365 * 24 * 60 * 60

	r := requestAt(t, testTimestamp+aYear, `{"name":"demo"}`)
	if err := verifyReason(t, newAuth(t), r); !errors.Is(err, auth.ErrTimestampOutsideWindow) {
		t.Fatalf("Verify() error = %v, want %v: a request signed a year ahead must not be a credential for a year",
			err, auth.ErrTimestampOutsideWindow)
	}
}

// TestVerifyRejectsExtremeTimestamps takes the window's arithmetic to the edges
// of int64, where time.Unix wraps past its own year-1 origin. The standard
// library clamps such a difference to the maximum duration rather than wrapping
// with it, so an absurd timestamp can only ever land outside the window — this
// asserts that rather than assuming it, because the failure mode if it were
// wrong is an accepted request.
func TestVerifyRejectsExtremeTimestamps(t *testing.T) {
	t.Parallel()

	// The distance between the Unix epoch and time.Time's internal year-1
	// origin: the offset whose addition overflows for a large enough timestamp.
	const internalOrigin int64 = 62135596800

	tests := []struct {
		name      string
		timestamp int64
	}{
		{name: "the unix epoch", timestamp: 0},
		{name: "one second before the epoch", timestamp: -1},
		{name: "the largest int64", timestamp: math.MaxInt64},
		{name: "the smallest int64", timestamp: math.MinInt64},
		{name: "the largest value that does not overflow internally", timestamp: math.MaxInt64 - internalOrigin},
		{name: "the smallest value that does overflow internally", timestamp: math.MaxInt64 - internalOrigin + 1},
		{name: "the negative mirror of the overflow point", timestamp: math.MinInt64 + internalOrigin},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := verifyReason(t, newAuth(t), requestAt(t, tt.timestamp, `{"name":"demo"}`))
			if !errors.Is(err, auth.ErrTimestampOutsideWindow) {
				t.Fatalf("Verify() error = %v, want %v for timestamp %d", err, auth.ErrTimestampOutsideWindow, tt.timestamp)
			}
		})
	}
}

// TestVerifyChecksTheWindowBeforeTheBody pins the contract's verification order.
// It is not cosmetic: everything past this point buffers and MACs the caller's
// body, and an unauthenticated caller should not be able to buy that work with a
// timestamp from last year.
func TestVerifyChecksTheWindowBeforeTheBody(t *testing.T) {
	t.Parallel()

	body := &countingReader{remaining: int(testMaxBody) * 10}
	r := requestAt(t, testTimestamp, "")
	r.Body = io.NopCloser(body)

	if err := verifyReason(t, newAuthDrifting(t, time.Hour), r); !errors.Is(err, auth.ErrTimestampOutsideWindow) {
		t.Fatalf("Verify() error = %v, want %v", err, auth.ErrTimestampOutsideWindow)
	}
	if body.read != 0 {
		t.Errorf("read %d bytes of a body whose request was already refused on its timestamp", body.read)
	}
}

// TestVerifyRejects is the negative table. Each case mutates a request that
// would otherwise verify.
func TestVerifyRejects(t *testing.T) {
	t.Parallel()

	const body = `{"name":"demo"}`

	tests := []struct {
		name   string
		mutate func(t *testing.T, r *http.Request)
		want   error
	}{
		{
			name:   "timestamp header absent",
			mutate: func(_ *testing.T, r *http.Request) { r.Header.Del(auth.HeaderTimestamp) },
			want:   auth.ErrMissingTimestamp,
		},
		{
			name:   "timestamp header empty",
			mutate: func(_ *testing.T, r *http.Request) { r.Header.Set(auth.HeaderTimestamp, "") },
			want:   auth.ErrMissingTimestamp,
		},
		{
			name:   "timestamp not a number",
			mutate: func(_ *testing.T, r *http.Request) { r.Header.Set(auth.HeaderTimestamp, "not-a-number") },
			want:   auth.ErrMalformedTimestamp,
		},
		{
			name:   "timestamp with surrounding space",
			mutate: func(_ *testing.T, r *http.Request) { r.Header.Set(auth.HeaderTimestamp, " 1785706480 ") },
			want:   auth.ErrMalformedTimestamp,
		},
		{
			name:   "timestamp in hexadecimal",
			mutate: func(_ *testing.T, r *http.Request) { r.Header.Set(auth.HeaderTimestamp, "0x6a6f0bd0") },
			want:   auth.ErrMalformedTimestamp,
		},
		{
			name:   "timestamp overflowing int64",
			mutate: func(_ *testing.T, r *http.Request) { r.Header.Set(auth.HeaderTimestamp, "99999999999999999999") },
			want:   auth.ErrMalformedTimestamp,
		},
		{
			// The window is checked before the signature, so this is the error
			// even though the mutation also invalidates the MAC.
			name:   "timestamp at the unix epoch",
			mutate: func(_ *testing.T, r *http.Request) { r.Header.Set(auth.HeaderTimestamp, "0") },
			want:   auth.ErrTimestampOutsideWindow,
		},
		{
			name:   "signature header absent",
			mutate: func(_ *testing.T, r *http.Request) { r.Header.Del(auth.HeaderSignature) },
			want:   auth.ErrMissingSignature,
		},
		{
			name:   "signature header empty",
			mutate: func(_ *testing.T, r *http.Request) { r.Header.Set(auth.HeaderSignature, "") },
			want:   auth.ErrMissingSignature,
		},
		{
			name: "signature computed with another secret",
			mutate: func(t *testing.T, r *http.Request) {
				r.Header.Set(auth.HeaderSignature, signatureOver(t, otherSecret, testTimestamp, body))
			},
			want: auth.ErrSignatureMismatch,
		},
		{
			name: "signature with its last character flipped",
			mutate: func(t *testing.T, r *http.Request) {
				sig := hexPart(t, r.Header.Get(auth.HeaderSignature))
				last := "0"
				if strings.HasSuffix(sig, "0") {
					last = "1"
				}
				r.Header.Set(auth.HeaderSignature, "sha256="+sig[:len(sig)-1]+last)
			},
			want: auth.ErrSignatureMismatch,
		},
		{
			name: "signature missing its algorithm prefix",
			mutate: func(t *testing.T, r *http.Request) {
				r.Header.Set(auth.HeaderSignature, hexPart(t, r.Header.Get(auth.HeaderSignature)))
			},
			want: auth.ErrSignatureMismatch,
		},
		{
			name: "signature in uppercase hex",
			mutate: func(t *testing.T, r *http.Request) {
				r.Header.Set(auth.HeaderSignature, "sha256="+strings.ToUpper(hexPart(t, r.Header.Get(auth.HeaderSignature))))
			},
			want: auth.ErrSignatureMismatch,
		},
		{
			name: "signature truncated",
			mutate: func(t *testing.T, r *http.Request) {
				sig := r.Header.Get(auth.HeaderSignature)
				r.Header.Set(auth.HeaderSignature, sig[:len(sig)-1])
			},
			want: auth.ErrSignatureMismatch,
		},
		{
			name: "signature padded with whitespace",
			mutate: func(_ *testing.T, r *http.Request) {
				r.Header.Set(auth.HeaderSignature, r.Header.Get(auth.HeaderSignature)+" ")
			},
			want: auth.ErrSignatureMismatch,
		},
		{
			name: "body tampered with after signing",
			mutate: func(_ *testing.T, r *http.Request) {
				r.Body = io.NopCloser(strings.NewReader(`{"name":"evil"}`))
			},
			want: auth.ErrSignatureMismatch,
		},
		{
			name: "body extended after signing",
			mutate: func(_ *testing.T, r *http.Request) {
				r.Body = io.NopCloser(strings.NewReader(body + " "))
			},
			want: auth.ErrSignatureMismatch,
		},
		{
			name: "body emptied after signing",
			mutate: func(_ *testing.T, r *http.Request) {
				r.Body = io.NopCloser(strings.NewReader(""))
			},
			want: auth.ErrSignatureMismatch,
		},
		{
			name: "timestamp changed after signing",
			mutate: func(_ *testing.T, r *http.Request) {
				r.Header.Set(auth.HeaderTimestamp, strconv.FormatInt(testTimestamp+1, 10))
			},
			want: auth.ErrSignatureMismatch,
		},
		{
			name: "signature from a request for a different body",
			mutate: func(t *testing.T, r *http.Request) {
				r.Header.Set(auth.HeaderSignature, signatureOver(t, testSecret, testTimestamp, `{"name":"other"}`))
			},
			want: auth.ErrSignatureMismatch,
		},
		{
			name: "body one byte over the maximum",
			mutate: func(_ *testing.T, r *http.Request) {
				r.Body = io.NopCloser(strings.NewReader(strings.Repeat("a", int(testMaxBody)+1)))
			},
			want: auth.ErrBodyTooLarge,
		},
		{
			name: "body that cannot be read",
			mutate: func(_ *testing.T, r *http.Request) {
				r.Body = io.NopCloser(failingReader{})
			},
			want: auth.ErrUnreadableBody,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			r := validRequest(t, body)
			tt.mutate(t, r)

			err := verifyReason(t, newAuth(t), r)
			if !errors.Is(err, tt.want) {
				t.Fatalf("Verify() error = %v, want %v", err, tt.want)
			}
		})
	}
}

// TestVerifyRebuffersTheBodyAfterAFailure keeps the request in a usable state on
// the denial path too: the middleware still has to audit and answer, and a
// half-drained reader is a trap for whatever reads next.
func TestVerifyRebuffersTheBodyAfterAFailure(t *testing.T) {
	t.Parallel()

	const body = `{"name":"demo"}`

	r := validRequest(t, body)
	r.Header.Set(auth.HeaderSignature, signatureOver(t, otherSecret, testTimestamp, body))

	if err := verifyReason(t, newAuth(t), r); !errors.Is(err, auth.ErrSignatureMismatch) {
		t.Fatalf("Verify() error = %v, want %v", err, auth.ErrSignatureMismatch)
	}

	got, err := io.ReadAll(r.Body)
	if err != nil {
		t.Fatalf("reading the re-buffered body: %v", err)
	}
	if string(got) != body {
		t.Errorf("re-buffered body = %q, want %q", got, body)
	}
}

// TestVerifyTreatsAnAbsentBodyAsEmpty covers the contract's `"1785706480."`
// payload: a request with no body signs over the empty string.
func TestVerifyTreatsAnAbsentBodyAsEmpty(t *testing.T) {
	t.Parallel()

	r := validRequest(t, "")
	r.Body = nil

	if err := verifyReason(t, newAuth(t), r); err != nil {
		t.Fatalf("Verify() rejected a signed request with no body: %v", err)
	}
}

// TestVerifyStopsReadingAtTheLimit proves the maximum bounds the read itself,
// not merely the verdict. Buffering a body to sign it is the one place the
// daemon holds caller-controlled bytes in memory before deciding anything.
func TestVerifyStopsReadingAtTheLimit(t *testing.T) {
	t.Parallel()

	body := &countingReader{remaining: int(testMaxBody) * 10}
	r := validRequest(t, "")
	r.Body = io.NopCloser(body)

	if err := verifyReason(t, newAuth(t), r); !errors.Is(err, auth.ErrBodyTooLarge) {
		t.Fatalf("Verify() error = %v, want %v", err, auth.ErrBodyTooLarge)
	}
	if int64(body.read) > testMaxBody+1 {
		t.Errorf("read %d bytes of an oversize body, want no more than %d", body.read, testMaxBody+1)
	}
}

// TestVerifyErrorsRevealNothing guards the audit trail: these errors become the
// reason field, which may never carry caller-supplied bytes (FR-042).
func TestVerifyErrorsRevealNothing(t *testing.T) {
	t.Parallel()

	const marker = "distinctive-body-marker"

	tests := []struct {
		name   string
		mutate func(t *testing.T, r *http.Request)
	}{
		{
			name:   "malformed timestamp",
			mutate: func(_ *testing.T, r *http.Request) { r.Header.Set(auth.HeaderTimestamp, marker) },
		},
		{
			name: "signature mismatch",
			mutate: func(t *testing.T, r *http.Request) {
				r.Header.Set(auth.HeaderSignature, signatureOver(t, otherSecret, testTimestamp, marker))
			},
		},
		{
			name: "oversize body",
			mutate: func(_ *testing.T, r *http.Request) {
				r.Body = io.NopCloser(strings.NewReader(strings.Repeat(marker, int(testMaxBody))))
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			r := validRequest(t, marker)
			signature := r.Header.Get(auth.HeaderSignature)
			tt.mutate(t, r)

			// The reason, not the denial: the denial is one fixed word, and a
			// leak would hide in the value that actually reaches the trail.
			err := verifyReason(t, newAuth(t), r)
			if err == nil {
				t.Fatal("Verify() accepted a request the case was built to reject")
			}

			for _, forbidden := range []string{marker, testSecret, otherSecret, signature, hexPart(t, signature)} {
				if strings.Contains(err.Error(), forbidden) {
					t.Errorf("Verify() error leaks %q into the audit trail", forbidden)
				}
			}
		})
	}
}

func TestVerifyIsSafeForConcurrentUse(t *testing.T) {
	t.Parallel()

	a := newAuth(t)

	// Built up front: t.Fatalf inside validRequest would call Goexit on the
	// wrong goroutine, and a helper that only sometimes reports a failure is
	// worse than no helper.
	const requests = 16
	good := make([]*http.Request, 0, requests)
	bad := make([]*http.Request, 0, requests)
	for i := range requests {
		body := strconv.Itoa(i)
		good = append(good, validRequest(t, body))

		forged := validRequest(t, body)
		forged.Header.Set(auth.HeaderSignature, signatureOver(t, otherSecret, testTimestamp, body))
		bad = append(bad, forged)
	}

	var wg sync.WaitGroup
	for i := range requests {
		wg.Add(1)
		go func() {
			defer wg.Done()

			if err := verifyReason(t, a, good[i]); err != nil {
				t.Errorf("Verify() rejected a correctly signed request: %v", err)
			}
			if err := verifyReason(t, a, bad[i]); !errors.Is(err, auth.ErrSignatureMismatch) {
				t.Errorf("Verify() error = %v, want %v", err, auth.ErrSignatureMismatch)
			}
		}()
	}
	wg.Wait()
}

// TestNewRejects keeps auth.New fail-closed independently of config. The two
// are wired together in T019/T020, and a construction that silently accepts no
// key is exactly the "daemon that starts with auth disabled" docs/security.md
// §4 calls worse than one that does not start.
func TestNewRejects(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		secret  []byte
		maxBody int64
	}{
		{name: "nil secret", secret: nil, maxBody: testMaxBody},
		{name: "empty secret", secret: []byte{}, maxBody: testMaxBody},
		{name: "zero max body", secret: []byte(testSecret), maxBody: 0},
		{name: "negative max body", secret: []byte(testSecret), maxBody: -1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			a, err := auth.New(tt.secret, tt.maxBody)
			if err == nil {
				t.Fatal("New() accepted a configuration that cannot authenticate anything")
			}
			if a != nil {
				t.Error("New() returned an Authenticator alongside an error")
			}
			if strings.Contains(err.Error(), testSecret) {
				t.Error("New() error leaks the shared secret")
			}
		})
	}
}

// TestNewWithClockRejectsANilClock keeps the same fail-closed rule as the
// secret. An Authenticator that cannot tell the time cannot enforce the window
// at all, and finding that out on the first request is a daemon that started
// with half its authentication missing.
func TestNewWithClockRejectsANilClock(t *testing.T) {
	t.Parallel()

	a, err := auth.NewWithClock([]byte(testSecret), testMaxBody, nil)
	if err == nil {
		t.Fatal("NewWithClock() accepted a nil clock")
	}
	if a != nil {
		t.Error("NewWithClock() returned an Authenticator alongside an error")
	}
}

// TestNewUsesTheHostClock proves the delegation, the way config_test.go's
// t.Setenv cases prove Load() reads the environment. Without it, New could hand
// NewWithClock a clock stopped at the zero time and every other test here would
// still pass, because they all supply their own.
func TestNewUsesTheHostClock(t *testing.T) {
	t.Parallel()

	a, err := auth.New([]byte(testSecret), testMaxBody)
	if err != nil {
		t.Fatalf("auth.New() unexpected error: %v", err)
	}

	now := time.Now().Unix()
	if err := verifyReason(t, a, requestAt(t, now, "")); err != nil {
		t.Fatalf("Verify() rejected a request signed at the host's own clock: %v", err)
	}

	// Measured from that same clock rather than from testTimestamp, so this half
	// does not quietly depend on how old the fixture instant has become.
	stale := now - int64(2*testWindow.Seconds())
	if err := verifyReason(t, a, requestAt(t, stale, "")); !errors.Is(err, auth.ErrTimestampOutsideWindow) {
		t.Fatalf("Verify() error = %v, want %v for a request %v old", err, auth.ErrTimestampOutsideWindow, 2*testWindow)
	}
}

// TestNewCopiesTheSecret closes the aliasing gap: the caller's slice is the one
// config loaded, and a key that can change after construction is a key that can
// change mid-request.
func TestNewCopiesTheSecret(t *testing.T) {
	t.Parallel()

	secret := []byte(testSecret)
	a, err := auth.NewWithClock(secret, testMaxBody, driftedClock(0))
	if err != nil {
		t.Fatalf("auth.NewWithClock() unexpected error: %v", err)
	}

	for i := range secret {
		secret[i] = 'x'
	}

	if err := verifyReason(t, a, validRequest(t, "")); err != nil {
		t.Fatalf("Verify() failed after the caller's secret slice was overwritten: %v", err)
	}
}

// failingReader stands in for a connection that dies mid-body.
type failingReader struct{}

func (failingReader) Read([]byte) (int, error) { return 0, errors.New("connection reset") }

// countingReader yields as many bytes as asked for, up to a limit, and records
// how many were actually taken.
type countingReader struct {
	remaining int
	read      int
}

func (c *countingReader) Read(p []byte) (int, error) {
	if c.remaining == 0 {
		return 0, io.EOF
	}
	n := min(len(p), c.remaining)
	for i := range p[:n] {
		p[i] = 'a'
	}
	c.remaining -= n
	c.read += n
	return n, nil
}
