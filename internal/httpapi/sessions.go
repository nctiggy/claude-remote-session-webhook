package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/nctiggy/claude-remote-session-webhook/internal/session"
)

// bodyInternalError is the 500 body. contracts/http-api.md says it carries no
// detail, so it carries none: the caller that triggered an internal failure is
// the last party who should learn its shape, and "tmux is not installed" is a
// fact about the host. What failed is recorded in the trail instead.
var bodyInternalError = []byte(`{"error":"internal error"}`)

// timestampFormat is how every instant this API returns is spelled — RFC 3339,
// UTC, to the second, exactly as contracts/http-api.md writes it and exactly as
// internal/audit writes its own. A response and the trail read side by side
// should line up without a timezone conversion.
const timestampFormat = time.RFC3339

// createRequest is the fixed shape POST /sessions accepts, and the closed set of
// things a caller may influence about a new session (FR-026).
//
// There is deliberately no owner field, and adding one is the change FR-012
// forbids: identity is derived server-side from the credential, and a body that
// could name its own owner would make the ownership check a formality. Because
// decode rejects unknown fields, a caller that sends "owner" is refused rather
// than ignored — which is the difference between a probe that is noticed and one
// that is not.
type createRequest struct {
	Name    string `json:"name"`
	WorkDir string `json:"work_dir"`
}

// createResponse is the contract's 201 body, in the contract's field order.
//
// Times are formatted strings rather than time.Time because encoding/json would
// otherwise render whatever precision the clock happened to carry; the contract
// documents seconds. The instants themselves come from the record, so
// expires_at is the deadline the reaper will actually enforce and not a second
// computation of it.
type createResponse struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	WorkDir   string `json:"work_dir"`
	State     string `json:"state"`
	CreatedAt string `json:"created_at"`
	ExpiresAt string `json:"expires_at"`

	// Token is the only copy of the bearer token that will ever exist outside
	// the caller (FR-013). It appears in this response and nowhere else — not in
	// a list entry, not in a detail response, not in the trail.
	Token string `json:"token"`
}

// promptRequest is the fixed shape POST /sessions/{id}/prompt accepts: one
// field, which is the text to deliver (contracts/http-api.md).
//
// There is deliberately no session field. Which session a prompt is for is
// settled by the path and the credential presented for it, both of which layer 3
// has already checked; a body that could name a session would be a second answer
// to that question, free to disagree with the first.
type promptRequest struct {
	// Text is data, not a command. It is length-bounded by CRSW_MAX_BODY_BYTES
	// and required to be non-empty, and that is the whole of its validation
	// (FR-030): it is delivered byte-for-byte, so anything this handler
	// "sanitised" would be a prompt the caller did not send.
	Text string `json:"text"`
}

// promptResponse is the contract's 202 body. It carries the daemon's own ID for
// the session and no echo of the text — which is not merely unnecessary but
// forbidden: prompt text is secret under docs/security.md §3, and a response
// that repeats it is one more place it can be logged by whatever reads it.
type promptResponse struct {
	ID        string `json:"id"`
	Delivered bool   `json:"delivered"`
}

// outputResponse is the contract's 200 body for GET /sessions/{id}/output, in
// the contract's field order.
//
// Text is pane content: everything the session printed, which is untrusted and
// secret at once. It is a plain string in a JSON document and nothing else — no
// HTML, no markup, no ANSI passed through for a client to interpret — because
// this is the value milestone 2 will put in a browser, and Principle VII closes
// that surface by construction rather than by sanitising later.
type outputResponse struct {
	ID         string `json:"id"`
	CapturedAt string `json:"captured_at"`
	Text       string `json:"text"`
}

// The reasons this handler records, authored here.
//
// None is ever written into a response, and none is built from a byte the caller
// sent (FR-042, FR-043). The refusals that come from internal/session are
// recorded under that package's own sentinels instead — see createReason — which
// are fixed strings for the same reason these are.
var (
	// errCreateNoCaller is unreachable behind the middleware, which is why it
	// fails closed rather than defaulting to an identity: a create that got here
	// without one would be a session whose owner is the zero CallerID, and every
	// later ownership check would be against nothing.
	errCreateNoCaller = errors.New("the create handler was reached with no authenticated caller")

	// errCreateRefused is the fail-closed reason for a refusal none of the
	// sentinels explain. A refusal nobody classified is still a refusal.
	errCreateRefused = errors.New("the session could not be created")

	// errCreateOrphaned is the loud one. It means tmux may have been left with a
	// live unsandboxed shell (Principle VI), which is the single fact in this
	// file an operator most needs the trail to carry.
	errCreateOrphaned = errors.New("a tmux session may have survived a failed create and could not be confirmed gone")

	// errResponseUnencodable cannot happen for a struct of strings, and is here
	// so that it cannot happen *silently* if a later field makes it possible.
	errResponseUnencodable = errors.New("the response could not be encoded")

	// errPromptNoSession is unreachable behind the layer-3 resolver, for the
	// reason errCreateNoCaller is unreachable behind layer 2. It fails closed
	// rather than falling back to the {id} in the path: a handler that read the
	// path itself would be addressing a window on a caller's say-so, which is
	// the one thing FR-034 exists to prevent.
	errPromptNoSession = errors.New("the prompt handler was reached with no resolved session")

	// errPromptUndelivered is what a caller's prompt failing to reach the
	// session is recorded as. It names no tmux message and no text: the caller
	// learns the prompt did not land, and the operator learns which session from
	// the record the resolver already stamped.
	errPromptUndelivered = errors.New("the prompt could not be delivered to the session")

	// errOutputNoSession is unreachable behind the layer-3 resolver, and fails
	// closed for the reason errPromptNoSession does: a handler that fell back to
	// the {id} in the path would be capturing a window on a caller's say-so.
	errOutputNoSession = errors.New("the output handler was reached with no resolved session")

	// errOutputUncaptured is what a pane the daemon could not read is recorded
	// as. It names no tmux message, and — the part that matters here — no
	// fragment of what was read: pane content is secret under docs/security.md
	// §3 and may not reach the trail (FR-042).
	errOutputUncaptured = errors.New("the session's output could not be captured")
)

// createSession is POST /sessions: validate, start, and hand back the only copy
// of the token (contracts/http-api.md).
//
// The order is the contract's. Identity comes first and from the context alone,
// then the body through the one decoder, and only then does anything execute —
// so a request that was never going to be allowed costs no tmux command. Every
// field-level refusal is answered by the manager rather than pre-checked here:
// ValidateName and ResolveWorkDir are the rules, and a handler that repeated
// them would be a second copy of the allowlist free to disagree with the first.
func (s *Server) createSession(w http.ResponseWriter, r *http.Request) {
	caller, ok := CallerFrom(r.Context())
	if !ok {
		s.failInternal(w, r, errCreateNoCaller)
		return
	}

	req, ok := decode[createRequest](s, w, r)
	if !ok {
		return
	}

	created, token, err := s.sessions.Create(r.Context(), session.CreateRequest{
		Owner:   caller.ID,
		Name:    req.Name,
		WorkDir: req.WorkDir,
	})
	if err != nil {
		s.refuseCreate(w, r, err)
		return
	}

	// The daemon's own ID, off the record it just made — never the bytes the
	// caller sent, which is the rule SetSessionID exists to keep.
	AuditFrom(r.Context()).SetSessionID(created.ID)

	s.writeJSON(w, r, http.StatusCreated, createResponse{
		ID:      created.ID,
		Name:    created.Name,
		WorkDir: created.WorkDir,
		State:   string(created.State),

		CreatedAt: created.CreatedAt.UTC().Format(timestampFormat),
		// TokenExpiry, not CreatedAt.Add(24h): the contract says expires_at is
		// the session's absolute deadline and the token's at once, and asking
		// the record for it is what keeps the three from ever being three.
		ExpiresAt: created.TokenExpiry().UTC().Format(timestampFormat),

		Token: token,
	})
}

// refuseCreate maps a Create failure onto the answer the contract gives it.
//
// A failed field validation is a 400 with the uniform body, indistinguishable
// from an unknown field or malformed JSON — deliberately, because the fields
// being validated are a session name and a *filesystem path*. A caller that
// could tell "outside an approved root" from "does not exist" would have a
// filesystem oracle behind one signature, which is exactly what the allowlist
// exists to deny. The distinction is kept, server-side, in the trail.
//
// Everything else is a 500. That includes the orphan case, where the record is
// kept and the caller holds no token: the session that may still be running is
// drivable by nobody and collectable by the daemon, which is the intended end
// state and not a success to report as one.
func (s *Server) refuseCreate(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, session.ErrInvalidName), errors.Is(err, session.ErrInvalidWorkDir):
		s.rejectBadRequest(w, r, createReason(err))
	case errors.Is(err, session.ErrOrphanedSession):
		s.failInternal(w, r, errCreateOrphaned)
	default:
		s.failInternal(w, r, errCreateRefused)
	}
}

// createReason picks the most specific account of a refusal that is still a
// fixed string.
//
// The wrapped error is never used directly. It reads harmlessly today —
// internal/session drops the caller's path on purpose — but the trail's guarantee
// cannot rest on another package continuing to choose that, and one %w away is a
// record carrying an attacker-chosen path, newlines included. The sentinels are
// values, so this survives any rewording of the text around them.
func createReason(err error) error {
	// Most specific first: each of these wraps ErrInvalidName or
	// ErrInvalidWorkDir, so the general two must come last or they would answer
	// for everything.
	for _, reason := range []error{
		session.ErrNameIsTmuxTarget,
		session.ErrWorkDirNotAbsolute,
		session.ErrWorkDirUnresolvable,
		session.ErrWorkDirNotDirectory,
		session.ErrWorkDirOutsideRoots,
		session.ErrInvalidName,
		session.ErrInvalidWorkDir,
	} {
		if errors.Is(err, reason) {
			return reason
		}
	}
	return errCreateRefused
}

// promptSession is POST /sessions/{id}/prompt: deliver the caller's text into
// the session it named and say it arrived (contracts/http-api.md).
//
// The session comes from the context and nowhere else. By the time this runs,
// layer 3 has matched the {id} against a record the caller owns and the
// credential issued for it, and has stamped that record's ID on the audit
// record — so this handler neither reads the path nor sets a session ID, and
// there is no version of it that could act on a session it was not given.
//
// Delivery itself belongs to internal/session, which is the only thing here that
// can cause execution on the host. What is left is the HTTP half: read the one
// field, hand it over unaltered, and answer.
func (s *Server) promptSession(w http.ResponseWriter, r *http.Request) {
	resolved, ok := SessionFrom(r.Context())
	if !ok {
		s.failInternal(w, r, errPromptNoSession)
		return
	}

	req, ok := decode[promptRequest](s, w, r)
	if !ok {
		return
	}

	if err := s.sessions.Prompt(r.Context(), resolved, req.Text); err != nil {
		s.refusePrompt(w, r, err)
		return
	}

	// 202, not 200: what is confirmed is that the keystrokes reached the pane,
	// which is not the same claim as Claude having read them, and the contract
	// spells that difference as "accepted for delivery".
	s.writeJSON(w, r, http.StatusAccepted, promptResponse{ID: resolved.ID, Delivered: true})
}

// refusePrompt maps a Prompt failure onto the answer the contract gives it.
//
// An empty text is the one thing the caller can fix, and it is a 400 with the
// same uniform body every other refused body gets. Everything else is a 500 with
// no detail — including a dead session, which the resolver already refuses as a
// 404 and which reaching here would mean a record went terminal between two
// layers of the same request. That is a fact about the host's state, and telling
// a caller which of tmux's failures it hit would be an oracle about a machine it
// cannot otherwise see.
func (s *Server) refusePrompt(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, session.ErrEmptyPrompt) {
		s.rejectBadRequest(w, r, session.ErrEmptyPrompt)
		return
	}
	s.failInternal(w, r, errPromptUndelivered)
}

// sessionOutput is GET /sessions/{id}/output: hand back what the session's pane
// currently holds, as text (contracts/http-api.md).
//
// The session comes from the context and nowhere else, exactly as it does for a
// prompt, and the capture itself belongs to internal/session — the stripper runs
// where output leaves that package, so what arrives here is already the bytes
// that may go out.
//
// There is one failure answer and it is a 500 with no detail. A pane that could
// not be read is a fact about the host: whether tmux is missing, whether the
// server died, or whether the window vanished under a record that still resolves
// are distinctions an operator reads in the trail, and telling them apart for a
// caller would be an oracle about a machine it cannot otherwise see.
//
// Nothing about the capture reaches the audit record. The middleware's record
// says which session was read and by whom, and that is the whole of what FR-042
// permits: pane content is secret, and a trail carrying it would be a second,
// permanent copy of everything the session ever printed.
func (s *Server) sessionOutput(w http.ResponseWriter, r *http.Request) {
	resolved, ok := SessionFrom(r.Context())
	if !ok {
		s.failInternal(w, r, errOutputNoSession)
		return
	}

	capture, err := s.sessions.Output(r.Context(), resolved)
	if err != nil {
		s.failInternal(w, r, errOutputUncaptured)
		return
	}

	s.writeJSON(w, r, http.StatusOK, outputResponse{
		ID:         resolved.ID,
		CapturedAt: capture.At.UTC().Format(timestampFormat),
		Text:       capture.Text,
	})
}

// writeJSON is the one place a success body is written, so that the header, the
// status, and the bytes cannot be ordered wrongly in six handlers.
//
// The payload is marshalled before anything is written. An encoder streaming
// straight to the ResponseWriter would emit a 201 and then discover it cannot
// finish, leaving a truncated body under a status that promises a whole one.
func (s *Server) writeJSON(w http.ResponseWriter, r *http.Request, status int, v any) {
	payload, err := json.Marshal(v)
	if err != nil {
		s.failInternal(w, r, errResponseUnencodable)
		return
	}

	w.Header().Set(headerContentType, contentTypeJSON)
	w.WriteHeader(status)
	if _, err := w.Write(payload); err != nil {
		s.report(fmt.Errorf("write the %d response: %w", status, err))
	}
}

// failInternal is the one place a 500 is written, and it records why for the
// same reason rejectBadRequest does: the middleware's record says `allow`
// because layer 2 allowed it, which is accurate about authentication and wrong
// about what happened next unless the handler says so.
//
// The reason must be an error authored in this repo. Never one built from a
// path, a name, a body, or the failure of a command that was handed one.
func (s *Server) failInternal(w http.ResponseWriter, r *http.Request, reason error) {
	AuditFrom(r.Context()).Deny(reason.Error())

	w.Header().Set(headerContentType, contentTypeJSON)
	w.WriteHeader(http.StatusInternalServerError)
	if _, err := w.Write(bodyInternalError); err != nil {
		s.report(fmt.Errorf("write the internal error response: %w", err))
	}
}
