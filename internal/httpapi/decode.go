package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// bodyBadRequest is the uniform 400, spelled once for the same reason
// bodyUnauthorized is: a body built per call site is a body that eventually
// differs, and a caller that can tell "unknown field" from "path outside an
// approved root" has been handed an oracle it was never meant to have. Which
// check refused is recorded in the audit trail and nowhere else.
//
// contracts/http-api.md gives 400 to a malformed body, an unknown field, a
// failed field validation, and an oversize body alike, so this is also the body
// T022's name and work_dir refusals answer with.
var bodyBadRequest = []byte(`{"error":"bad request"}`)

// The reasons a body is refused, one value each, authored here.
//
// They exist to be recorded server-side. None of them carries a byte the caller
// sent, which is what makes them safe for the audit trail (FR-042, FR-043) — and
// it is why the encoding/json error they were chosen from is dropped rather than
// wrapped: `json: unknown field "x"` and `invalid character 'x'` both quote the
// request back.
var (
	errBodyMissing      = errors.New("request body is empty")
	errBodyTooLarge     = errors.New("request body exceeds the configured maximum")
	errBodyMalformed    = errors.New("request body is not well-formed JSON")
	errBodyUnknownField = errors.New("request body carries a field the request shape does not define")
	errBodyWrongShape   = errors.New("request body field has the wrong type")
	errBodyTrailingData = errors.New("request body carries more than one JSON value")

	// errBodyUnreadable is the failure that was not about the JSON: a body that
	// stopped arriving mid-request is the reachable one. It is also the default
	// for a decoder error this package does not recognise, which is what makes
	// the classifier fail closed — an unrecognised failure is a refusal.
	errBodyUnreadable = errors.New("request body could not be read")
)

// unknownFieldPrefix is how encoding/json spells the refusal
// DisallowUnknownFields produces. There is no error type for it — a string
// compare is the only way to tell it from any other decode failure.
//
// It is load-bearing for the trail's detail and for nothing else: if a future
// Go release rewords the message, the request is still refused, still with a
// 400, and still with a reason — errBodyUnreadable, the fail-closed default,
// instead of this one. Probed, not assumed: rewording this constant leaves every
// status assertion green and fails only the reason ones.
const unknownFieldPrefix = "json: unknown field "

// decode reads the request body into T and answers the caller itself when it
// cannot. It is the only path a body takes into this daemon (plan.md).
//
// A handler that ignores the second return would go on to act on a zero value,
// so the answer is written here rather than left to the caller: refusing, saying
// so, and recording why are one step, and there is no shape of handler that can
// do two of the three.
//
// It takes the Server as a parameter because Go has no generic methods, and the
// alternative — a free function plus a separate call to write the 400 — is the
// forgettable second step this exists to remove.
func decode[T any](s *Server, w http.ResponseWriter, r *http.Request) (T, bool) {
	v, err := decodeBody[T](w, r, s.cfg.MaxBodyBytes)
	if err != nil {
		s.rejectBadRequest(w, r, err)
		// v is already the zero value — decodeBody guarantees it, and zeroing
		// again here would be a second guarantee that hides a break in the
		// first: a probe against decodeBody's would pass with this line in
		// place, which is precisely how an untested claim survives.
		return v, false
	}
	return v, true
}

// decodeBody is FR-026: a fixed shape, unknown fields rejected, and a size limit
// enforced before the decode.
//
// The limit is deliberately redundant. auth.readBody has already refused
// anything past CRSW_MAX_BODY_BYTES — it must, since a signature cannot be
// computed over bytes the daemon declined to read — and what it leaves behind is
// an in-memory reader that can no longer exceed the same limit. This is the
// second line of defence for the body that arrives some other way: a handler
// under direct test, a future route reached before layer 2, or a request whose
// body was replaced between the two.
//
// The zero value comes back on every failure. json.Decoder populates what it
// parsed before it failed, and a half-filled struct escaping a refused request
// is how a validated field ends up unvalidated.
func decodeBody[T any](w http.ResponseWriter, r *http.Request, maxBody int64) (T, error) {
	var zero T
	if r.Body == nil {
		return zero, errBodyMissing
	}

	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBody))
	dec.DisallowUnknownFields()

	var v T
	if err := dec.Decode(&v); err != nil {
		return zero, refusal(err)
	}

	// Exactly one value, so that `{"name":"a"} {"name":"b"}` is refused rather
	// than silently read as the first. Both objects are inside the signature,
	// and a request that means two things is not one the daemon gets to choose
	// between.
	if err := dec.Decode(new(struct{})); !errors.Is(err, io.EOF) {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			return zero, errBodyTooLarge
		}
		return zero, errBodyTrailingData
	}
	return v, nil
}

// refusal maps a decoder failure onto one of this package's own reasons.
//
// The error it was handed is read and then dropped, never wrapped: every
// encoding/json failure quotes the offending input, and the trail may not carry
// caller-supplied bytes. An unrecognised failure is still a refusal — the
// default is a reason, not an acceptance.
func refusal(err error) error {
	var (
		tooLarge  *http.MaxBytesError
		wrongType *json.UnmarshalTypeError
		syntax    *json.SyntaxError
	)
	switch {
	case errors.As(err, &tooLarge):
		return errBodyTooLarge
	case errors.Is(err, io.EOF):
		return errBodyMissing
	case errors.Is(err, io.ErrUnexpectedEOF), errors.As(err, &syntax):
		return errBodyMalformed
	case errors.As(err, &wrongType):
		return errBodyWrongShape
	case strings.HasPrefix(err.Error(), unknownFieldPrefix):
		return errBodyUnknownField
	default:
		return errBodyUnreadable
	}
}

// rejectBadRequest is the one place a 400 is written. The reason is recorded and
// the caller is told only that the request was bad.
//
// Amending the audit record here is what keeps the trail honest: the middleware
// opened it at the authentication decision, which for a request that reached a
// handler is `allow` — accurate about layer 2 and wrong about what happened next
// unless the handler says so.
//
// The reason must be an error authored in this repo. Never one built from a
// path, a name, a field, or a body (FR-042, FR-043).
func (s *Server) rejectBadRequest(w http.ResponseWriter, r *http.Request, reason error) {
	AuditFrom(r.Context()).Deny(reason.Error())

	w.Header().Set(headerContentType, contentTypeJSON)
	w.WriteHeader(http.StatusBadRequest)
	if _, err := w.Write(bodyBadRequest); err != nil {
		s.report(fmt.Errorf("write the bad request response: %w", err))
	}
}
