package auth_test

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"

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

// testTimestamp is the example instant in contracts/http-api.md. T010 enforces
// the 300-second window against an injected clock; pinning every fixture to one
// instant here means that clock has a single value to return and these tests
// keep passing.
const testTimestamp int64 = 1785706480

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

func newAuth(t *testing.T) *auth.Authenticator {
	t.Helper()

	a, err := auth.New([]byte(testSecret), testMaxBody)
	if err != nil {
		t.Fatalf("auth.New() unexpected error: %v", err)
	}
	return a
}

// validRequest verifies cleanly, so each table case can change exactly one
// thing and blame the failure on it. A case that stops failing is then a real
// regression rather than a fixture that rotted.
func validRequest(t *testing.T, body string) *http.Request {
	t.Helper()

	r := httptest.NewRequest(http.MethodPost, "/sessions", strings.NewReader(body))
	r.Header.Set(auth.HeaderTimestamp, strconv.FormatInt(testTimestamp, 10))
	r.Header.Set(auth.HeaderSignature, signatureOver(t, testSecret, testTimestamp, body))
	return r
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
			if err := newAuth(t).Verify(r); err != nil {
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

	if err := newAuth(t).Verify(r); err != nil {
		t.Fatalf("Verify() rejected a request whose timestamp header carries a redundant sign: %v", err)
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

			err := newAuth(t).Verify(r)
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

	if err := newAuth(t).Verify(r); !errors.Is(err, auth.ErrSignatureMismatch) {
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

	if err := newAuth(t).Verify(r); err != nil {
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

	if err := newAuth(t).Verify(r); !errors.Is(err, auth.ErrBodyTooLarge) {
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

			err := newAuth(t).Verify(r)
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

			if err := a.Verify(good[i]); err != nil {
				t.Errorf("Verify() rejected a correctly signed request: %v", err)
			}
			if err := a.Verify(bad[i]); !errors.Is(err, auth.ErrSignatureMismatch) {
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

// TestNewCopiesTheSecret closes the aliasing gap: the caller's slice is the one
// config loaded, and a key that can change after construction is a key that can
// change mid-request.
func TestNewCopiesTheSecret(t *testing.T) {
	t.Parallel()

	secret := []byte(testSecret)
	a, err := auth.New(secret, testMaxBody)
	if err != nil {
		t.Fatalf("auth.New() unexpected error: %v", err)
	}

	for i := range secret {
		secret[i] = 'x'
	}

	if err := a.Verify(validRequest(t, "")); err != nil {
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
