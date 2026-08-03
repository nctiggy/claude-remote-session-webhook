// Internal test, matching server_test.go and middleware_test.go. decode is
// unexported — it is the daemon's only body path, not a public helper — and two
// of the properties here are only visible from inside: the reason recorded for
// each refusal, and the zero value that comes back in place of a half-decoded
// struct.
package httpapi

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/nctiggy/claude-remote-session-webhook/internal/audit"
)

// decodeProbe stands in for T022's create request: a fixed shape with a couple
// of typed fields, which is all decode is required to know about.
type decodeProbe struct {
	Name    string `json:"name"`
	WorkDir string `json:"work_dir"`
	Count   int    `json:"count"`
}

// pendingAudit is the record the middleware hands a handler — already `allow`,
// because layer 2 passed to get here. A refusal has to flip it.
func pendingAudit() *RequestAudit {
	return &RequestAudit{rec: audit.Record{Action: audit.ActionSessionCreate, Decision: audit.Allow}}
}

type decoded struct {
	answer *httptest.ResponseRecorder
	value  decodeProbe
	ok     bool
	audit  *RequestAudit
}

// decodeBytes drives decode the way a handler behind the middleware does,
// without going through layer 2. That is deliberate for the oversize case in
// particular: auth refuses an oversize body with a 401 long before a handler
// runs, so a test that drove this through a route would be testing the
// authenticator (see TestAnOversizeBodyNeverReachesTheDecoder).
func decodeBytes(t *testing.T, s *Server, body []byte) decoded {
	t.Helper()

	req := httptest.NewRequest(http.MethodPost, "/sessions", bytes.NewReader(body))
	ra := pendingAudit()
	req = req.WithContext(context.WithValue(req.Context(), auditContextKey, ra))

	answer := httptest.NewRecorder()
	v, ok := decode[decodeProbe](s, answer, req)
	return decoded{answer: answer, value: v, ok: ok, audit: ra}
}

// oversizeBody is one byte past the configured maximum — the boundary, not an
// order of magnitude, so the test still fails if the limit is applied off by one.
func oversizeBody() []byte {
	const envelope = `{"name":"` + `"}`
	return []byte(`{"name":"` + strings.Repeat("a", testMaxBody+1-len(envelope)) + `"}`)
}

// refusedBodies is every way FR-026 refuses a body, with the reason each is
// recorded under. The reasons are the point: the answer outward is uniform, so
// the trail is the only place the difference survives.
func refusedBodies() map[string]struct {
	body   []byte
	reason error
} {
	return map[string]struct {
		body   []byte
		reason error
	}{
		"an unknown field":    {[]byte(`{"name":"probe","surprise":true}`), errBodyUnknownField},
		"only unknown fields": {[]byte(`{"surprise":true}`), errBodyUnknownField},
		"a truncated body":    {[]byte(`{"name":"probe"`), errBodyMalformed},
		"a truncated string":  {[]byte(`{"name":"pro`), errBodyMalformed},
		"not JSON at all":     {[]byte(`name = probe`), errBodyMalformed},
		"a field of the wrong type": {
			[]byte(`{"name":["probe"]}`), errBodyWrongShape,
		},
		"a number where the shape wants a string": {
			[]byte(`{"name":7}`), errBodyWrongShape,
		},
		"an array where the shape wants an object": {
			[]byte(`[{"name":"probe"}]`), errBodyWrongShape,
		},
		"an empty body":     {[]byte(``), errBodyMissing},
		"whitespace only":   {[]byte("  \n\t "), errBodyMissing},
		"two JSON values":   {[]byte(`{"name":"first"}{"name":"second"}`), errBodyTrailingData},
		"trailing garbage":  {[]byte(`{"name":"probe"} and then some`), errBodyTrailingData},
		"an oversize body":  {oversizeBody(), errBodyTooLarge},
		"an oversize array": {append([]byte(`[`), bytes.Repeat([]byte("0,"), testMaxBody)...), errBodyTooLarge},
	}
}

// TestDecodeAcceptsAWellFormedBody is the case every refusal below is measured
// against: the fields arrive, nothing is written, and the audit record is left
// as the middleware set it.
func TestDecodeAcceptsAWellFormedBody(t *testing.T) {
	t.Parallel()

	s := newTestServer(t, loopbackListen)
	got := decodeBytes(t, s, []byte(`{"name":"refactor-auth","work_dir":"/home/operator/code/myrepo","count":3}`))

	if !got.ok {
		t.Fatalf("decode refused a well-formed body; answer = %d %q", got.answer.Code, got.answer.Body)
	}
	want := decodeProbe{Name: "refactor-auth", WorkDir: "/home/operator/code/myrepo", Count: 3}
	if got.value != want {
		t.Errorf("decode = %+v; want %+v", got.value, want)
	}
	if got.answer.Body.Len() != 0 {
		t.Errorf("decode wrote %q; a successful decode leaves the response to the handler", got.answer.Body)
	}
	if got.audit.rec.Decision != audit.Allow {
		t.Errorf("decision = %q; want %q — nothing was refused", got.audit.rec.Decision, audit.Allow)
	}
}

// TestDecodeRefusesEveryBadBody is FR-026 and the contract's 400 row together:
// unknown field, oversize, truncated, and wrong shape all refused, all 400, and
// each recorded under its own server-authored reason.
func TestDecodeRefusesEveryBadBody(t *testing.T) {
	t.Parallel()

	for name, c := range refusedBodies() {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			s := newTestServer(t, loopbackListen)
			got := decodeBytes(t, s, c.body)

			if got.ok {
				t.Fatalf("decode accepted %s: %+v", name, got.value)
			}
			if got.answer.Code != http.StatusBadRequest {
				t.Errorf("status = %d; want %d", got.answer.Code, http.StatusBadRequest)
			}
			if body := got.answer.Body.String(); body != string(bodyBadRequest) {
				t.Errorf("body = %q; want %q", body, bodyBadRequest)
			}
			if ct := got.answer.Header().Get(headerContentType); ct != contentTypeJSON {
				t.Errorf("Content-Type = %q; want %q — every response is JSON", ct, contentTypeJSON)
			}
			// A struct half-filled by the decoder before it failed would carry
			// caller bytes past the refusal, into a handler that believes the
			// request was rejected.
			if got.value != (decodeProbe{}) {
				t.Errorf("decode returned %+v alongside its refusal; want the zero value", got.value)
			}
			if got.audit.rec.Decision != audit.Deny {
				t.Errorf("decision = %q; want %q", got.audit.rec.Decision, audit.Deny)
			}
			if got.audit.rec.Reason != c.reason.Error() {
				t.Errorf("reason = %q; want %q", got.audit.rec.Reason, c.reason.Error())
			}
			if got.audit.rec.Action != audit.ActionSessionCreate {
				t.Errorf("action = %q; want %q — refusing must not rename the operation",
					got.audit.rec.Action, audit.ActionSessionCreate)
			}
		})
	}
}

// TestDecodeBodyKeepsNothingItParsedBeforeItFailed pins the claim decode rests
// on rather than restating it. json.Decoder fills in every field it read before
// the one that broke, so `{"name":"probe","surprise":true}` leaves a populated
// Name behind — caller bytes surviving a refused request, into a handler that
// believes nothing arrived.
func TestDecodeBodyKeepsNothingItParsedBeforeItFailed(t *testing.T) {
	t.Parallel()

	for name, c := range refusedBodies() {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			req := httptest.NewRequest(http.MethodPost, "/sessions", bytes.NewReader(c.body))
			got, err := decodeBody[decodeProbe](httptest.NewRecorder(), req, testMaxBody)

			if err == nil {
				t.Fatalf("decodeBody accepted %s: %+v", name, got)
			}
			if got != (decodeProbe{}) {
				t.Errorf("decodeBody = %+v alongside its refusal; want the zero value", got)
			}
			if !errors.Is(err, c.reason) {
				t.Errorf("decodeBody error = %v; want %v", err, c.reason)
			}
		})
	}
}

// TestEveryRefusedBodyAnswersTheIdenticalResponse is the 401's rule applied to
// the 400. "Unknown field" and "malformed" are harmless to distinguish, but the
// same body and the same code also answer T022's refusals — a path outside an
// approved root among them — and a caller that can tell those apart has a
// filesystem oracle.
func TestEveryRefusedBodyAnswersTheIdenticalResponse(t *testing.T) {
	t.Parallel()

	s := newTestServer(t, loopbackListen)
	answers := map[string]*httptest.ResponseRecorder{}
	for name, c := range refusedBodies() {
		answers[name] = decodeBytes(t, s, c.body).answer
	}

	names := slices.Sorted(maps.Keys(answers))
	for _, name := range names[1:] {
		a, b := answers[names[0]], answers[name]
		if !reflect.DeepEqual(a.Header(), b.Header()) {
			t.Errorf("%q answered with headers %v but %q answered with %v; the refusal must be uniform",
				names[0], a.Header(), name, b.Header())
		}
		if !bytes.Equal(a.Body.Bytes(), b.Body.Bytes()) {
			t.Errorf("%q answered %q but %q answered %q; the refusal must be uniform",
				names[0], a.Body, name, b.Body)
		}
		if a.Code != b.Code {
			t.Errorf("%q answered %d but %q answered %d", names[0], a.Code, name, b.Code)
		}
	}
}

// TestARefusalCarriesNoCallerBytesOutward is FR-042 and FR-043 at this
// boundary. encoding/json quotes the input back in every one of its errors, so
// the failure this pins is one wrapped error away at all times.
func TestARefusalCarriesNoCallerBytesOutward(t *testing.T) {
	t.Parallel()

	const marker = "kumquat"
	bodies := map[string][]byte{
		"an unknown field":     []byte(`{"` + marker + `":true}`),
		"a truncated body":     []byte(`{"name":"` + marker),
		"not JSON at all":      []byte(marker),
		"a wrong-shaped field": []byte(`{"name":{"` + marker + `":1}}`),
		"trailing garbage":     []byte(`{"name":"probe"} ` + marker),
	}

	s := newTestServer(t, loopbackListen)
	for name, body := range bodies {
		got := decodeBytes(t, s, body)

		outward := got.answer.Body.String() + " " + fmt.Sprint(got.answer.Header())
		if strings.Contains(outward, marker) {
			t.Errorf("the %s refusal echoed the body back: %q", name, outward)
		}
		if strings.Contains(got.audit.rec.Reason, marker) {
			t.Errorf("the %s refusal put the body in the audit reason: %q", name, got.audit.rec.Reason)
		}
	}
}

// TestARefusedBodyStillEmitsExactlyOneAuditRecord runs the whole path — signed
// request, middleware, handler, decode — because FR-041 is a claim about a
// request and not about a function.
func TestARefusedBodyStillEmitsExactlyOneAuditRecord(t *testing.T) {
	t.Parallel()

	s := newAuditedServer(t)
	body := []byte(`{"name":"probe","surprise":true}`)
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := decode[decodeProbe](s.Server, w, r); ok {
			t.Error("decode accepted a body carrying an unknown field")
		}
	})

	req := httptest.NewRequest(http.MethodPost, "/sessions", bytes.NewReader(body))
	signRequest(t, req, body, testTime)
	answer := httptest.NewRecorder()
	s.authenticate(audit.ActionSessionCreate, handler).ServeHTTP(answer, req)

	if answer.Code != http.StatusBadRequest {
		t.Fatalf("status = %d; want %d", answer.Code, http.StatusBadRequest)
	}

	rec := s.only(t)
	if rec["decision"] != string(audit.Deny) {
		t.Errorf("decision = %v; want %q — the request was refused, whatever layer 2 decided",
			rec["decision"], audit.Deny)
	}
	if rec["reason"] != errBodyUnknownField.Error() {
		t.Errorf("reason = %v; want %q", rec["reason"], errBodyUnknownField.Error())
	}
	if rec["action"] != string(audit.ActionSessionCreate) {
		t.Errorf("action = %v; want %q", rec["action"], audit.ActionSessionCreate)
	}
}

// TestAnOversizeBodyNeverReachesTheDecoder pins the ordering the notebook ruled
// on: the signature covers the bytes as received, so a body the daemon refused
// to finish reading is one whose signature cannot be computed. Layer 2 answers
// 401 and the handler never runs — which is why decode's own limit is a second
// line of defence and never the thing that produces the contract's 400.
func TestAnOversizeBodyNeverReachesTheDecoder(t *testing.T) {
	t.Parallel()

	s := newAuditedServer(t)
	body := oversizeBody()
	reached := false
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached = true
		// Never reached; here so the handler is the one a real route would run.
		decode[decodeProbe](s.Server, w, r)
	})

	req := httptest.NewRequest(http.MethodPost, "/sessions", bytes.NewReader(body))
	signRequest(t, req, body, testTime)
	answer := httptest.NewRecorder()
	s.authenticate(audit.ActionSessionCreate, handler).ServeHTTP(answer, req)

	if reached {
		t.Error("the handler ran on an oversize body; layer 2 must refuse it first")
	}
	if answer.Code != http.StatusUnauthorized {
		t.Errorf("status = %d; want %d — an unverifiable signature is not a bad request",
			answer.Code, http.StatusUnauthorized)
	}
	if got := answer.Body.String(); got != string(bodyUnauthorized) {
		t.Errorf("body = %q; want %q", got, bodyUnauthorized)
	}
}

// TestDecodeRefusesARequestWithNoBodyAtAll covers the shape net/http never
// produces but a handler under direct test does. Fail closed: no body is not an
// empty object.
func TestDecodeRefusesARequestWithNoBodyAtAll(t *testing.T) {
	t.Parallel()

	s := newTestServer(t, loopbackListen)
	req := httptest.NewRequest(http.MethodPost, "/sessions", nil)
	req.Body = nil
	ra := pendingAudit()
	req = req.WithContext(context.WithValue(req.Context(), auditContextKey, ra))

	answer := httptest.NewRecorder()
	if _, ok := decode[decodeProbe](s, answer, req); ok {
		t.Fatal("decode accepted a request with no body")
	}
	if answer.Code != http.StatusBadRequest {
		t.Errorf("status = %d; want %d", answer.Code, http.StatusBadRequest)
	}
	if ra.rec.Reason != errBodyMissing.Error() {
		t.Errorf("reason = %q; want %q", ra.rec.Reason, errBodyMissing.Error())
	}
}

// unreadableBody is a client that hung up in the middle of its own request.
type unreadableBody struct{ err error }

func (b unreadableBody) Read([]byte) (int, error) { return 0, b.err }
func (b unreadableBody) Close() error             { return nil }

// TestABodyThatStopsArrivingIsRefused reaches the classifier's default branch,
// which is the one that has to fail closed: a read that failed is not an empty
// object, and a decoder error this package does not recognise must still end the
// request. The marker in the read error also pins that the underlying failure is
// dropped rather than recorded — a network error can carry an address.
func TestABodyThatStopsArrivingIsRefused(t *testing.T) {
	t.Parallel()

	const marker = "192.0.2.7:9999"
	s := newTestServer(t, loopbackListen)
	req := httptest.NewRequest(http.MethodPost, "/sessions", nil)
	req.Body = unreadableBody{err: errors.New("read tcp " + marker + ": connection reset by peer")}
	ra := pendingAudit()
	req = req.WithContext(context.WithValue(req.Context(), auditContextKey, ra))

	answer := httptest.NewRecorder()
	if _, ok := decode[decodeProbe](s, answer, req); ok {
		t.Fatal("decode accepted a body it could not read")
	}
	if answer.Code != http.StatusBadRequest {
		t.Errorf("status = %d; want %d", answer.Code, http.StatusBadRequest)
	}
	if body := answer.Body.String(); body != string(bodyBadRequest) {
		t.Errorf("body = %q; want %q", body, bodyBadRequest)
	}
	if ra.rec.Reason != errBodyUnreadable.Error() {
		t.Errorf("reason = %q; want %q", ra.rec.Reason, errBodyUnreadable.Error())
	}
	if strings.Contains(ra.rec.Reason, marker) {
		t.Errorf("the read error travelled into the audit reason: %q", ra.rec.Reason)
	}
}

// TestDecodeIsSafeWithoutAnAuditRecord keeps a handler under direct unit test
// from needing the middleware just to refuse a body.
func TestDecodeIsSafeWithoutAnAuditRecord(t *testing.T) {
	t.Parallel()

	s := newTestServer(t, loopbackListen)
	req := httptest.NewRequest(http.MethodPost, "/sessions", bytes.NewReader([]byte(`{`)))
	answer := httptest.NewRecorder()

	if _, ok := decode[decodeProbe](s, answer, req); ok {
		t.Fatal("decode accepted a truncated body")
	}
	if answer.Code != http.StatusBadRequest {
		t.Errorf("status = %d; want %d", answer.Code, http.StatusBadRequest)
	}
}

// failingWriter answers every write with an error, which is the one way a
// refusal can be lost without anybody noticing.
type failingWriter struct {
	header http.Header
	code   int
	err    error
}

func (w *failingWriter) Header() http.Header {
	if w.header == nil {
		w.header = http.Header{}
	}
	return w.header
}

func (w *failingWriter) WriteHeader(code int)      { w.code = code }
func (w *failingWriter) Write([]byte) (int, error) { return 0, w.err }

// TestARefusalThatCouldNotBeWrittenIsReported: AGENTS.md bans a swallowed error,
// and a 400 that never reached the socket is exactly the request an operator
// would go looking for.
func TestARefusalThatCouldNotBeWrittenIsReported(t *testing.T) {
	t.Parallel()

	want := errors.New("the connection went away")
	s := newTestServer(t, loopbackListen)
	var reported []error
	s.report = func(err error) { reported = append(reported, err) }

	req := httptest.NewRequest(http.MethodPost, "/sessions", bytes.NewReader([]byte(`{"surprise":true}`)))
	if _, ok := decode[decodeProbe](s, &failingWriter{err: want}, req); ok {
		t.Fatal("decode accepted a body carrying an unknown field")
	}

	if len(reported) != 1 {
		t.Fatalf("the failed write was reported %d times; want exactly 1", len(reported))
	}
	if !errors.Is(reported[0], want) {
		t.Errorf("reported %v; want the write failure wrapped", reported[0])
	}
}

// TestTheUnknownFieldMessageStillMatchesTheStandardLibrary is a canary, not a
// requirement. DisallowUnknownFields has no error type, so the reason recorded
// for it rests on a string compare against encoding/json's wording; if a Go
// release changes it, the request is still refused with a 400 and the only loss
// is the trail's detail. This test says so out loud rather than letting the
// reason quietly degrade to "malformed".
func TestTheUnknownFieldMessageStillMatchesTheStandardLibrary(t *testing.T) {
	t.Parallel()

	s := newTestServer(t, loopbackListen)
	got := decodeBytes(t, s, []byte(`{"surprise":true}`))

	if got.audit.rec.Reason != errBodyUnknownField.Error() {
		t.Errorf("reason = %q; want %q — encoding/json may have reworded %q",
			got.audit.rec.Reason, errBodyUnknownField.Error(), unknownFieldPrefix)
	}
}
