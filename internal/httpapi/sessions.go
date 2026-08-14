package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/nctiggy/claude-remote-session-webhook/internal/config"
	"github.com/nctiggy/claude-remote-session-webhook/internal/session"
)

// bodyInternalError is the 500 body. contracts/http-api.md says it carries no
// detail, so it carries none: the caller that triggered an internal failure is
// the last party who should learn its shape, and "tmux is not installed" is a
// fact about the host. What failed is recorded in the trail instead.
var bodyInternalError = []byte(`{"error":"internal error"}`)

// bodyTeardownUnverified is the 409 body, byte for byte from
// contracts/http-api.md, and the only non-uniform error this API writes.
//
// Being specific here discloses nothing. The caller has already proved it owns
// this session by presenting the credential issued for it, so the one fact this
// body carries is a fact about the caller's own session — which is what makes
// this the exception rather than a hole in FR-011 and FR-033. Being specific is
// also the point: the alternative is telling an operator a session was torn down
// while a live unsandboxed shell may have survived it (Principle VI).
var bodyTeardownUnverified = []byte(`{"error":"teardown could not be verified"}`)

// bodyTooManyRequests is the 429 body. contracts/http-api.md gives the status
// one row — "concurrent-session cap reached, or create rate limit exceeded" —
// and no body, so this is the status's own phrase and nothing more.
//
// One body for both halves of that row is deliberate. A caller cannot act on the
// difference: the answer to either is to wait, and telling them apart would say
// how many sessions the host is running to a caller that owns none of them. The
// rate limiter T034 adds writes these same bytes.
var bodyTooManyRequests = []byte(`{"error":"too many requests"}`)

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

	// StartCommand names one of the operator's configured commands (#38). It is
	// a name, never a command line: a create route that accepted a command line
	// would be a remote shell wearing a session manager's clothes. Absent means
	// the daemon's default, so every client written before this field keeps
	// working unchanged.
	StartCommand string `json:"start_command"`

	// Resume asks the new session to continue a prior Claude conversation in its
	// working directory instead of starting empty (milestone 15): "latest" for
	// the most recent, or a conversation identifier for one in particular. Absent
	// starts fresh.
	//
	// The signed door takes it for the reason it takes the lifetime: two doors
	// offering different capabilities for one daemon is how a caller learns to
	// prefer whichever one was written last. It is validated in the manager,
	// which is where every caller meets the same check.
	Resume string `json:"resume"`

	// Lifetime is an optional per-session override (#37), as a Go duration
	// string: "72h", or "never" meaning no absolute deadline at all. Absent
	// means the daemon's default.
	//
	// There was an IdleTimeout beside it until milestone 15, whose "0" meant no
	// idle reaping for this session. A client still sending that field is not
	// refused for it — decode ignores unknown members — and gets a session with
	// the one bound this daemon still has.
	//
	// "never" is granted only on a daemon whose own ceiling is unbounded
	// (config.NeverLifetime), and refused with every other over-the-ceiling
	// value on one that is not — so it names a door the operator opened rather
	// than one this field can open.
	//
	// Strings rather than numbers because a bare 3600 is a unit the caller and
	// the daemon have to agree about silently, and "1h" is one nobody can read
	// two ways.
	Lifetime string `json:"lifetime"`
}

// parseLifetimeOverride turns the request's duration string into what the
// manager takes (#37).
//
// "never" is translated to a negative, so that "the operator said nothing" and
// "the operator said none" cannot be one value. It is a word rather than "0"
// for config.NeverLifetime's reason: a caller who sent zero meaning "no time at
// all" would be handed a session nothing reaps, and the deadline being switched
// off here is the one that is never renewed. Whether it is granted is not
// decided here — resolveLifetimes weighs it against the operator's ceiling,
// exactly as it weighs "8760h".
//
// It parsed a second field until milestone 15, the idle timeout, whose "0" was
// that bound's own spelling of the disable. Both went with the bound.
func parseLifetimeOverride(lifetime string) (time.Duration, error) {
	v := strings.TrimSpace(lifetime)
	if v == "" {
		return 0, nil
	}
	if strings.EqualFold(v, config.NeverLifetime) {
		return neverLifetimeDuration, nil
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return 0, fmt.Errorf("%w: lifetime %q is not a duration such as 72h, or %q", session.ErrInvalidLifetime, v, config.NeverLifetime)
	}
	return d, nil
}

// neverLifetimeDuration is what this route's "switch that bound off" spelling
// becomes: any negative does it, and one constant so the two doors cannot come
// to disagree about which negative.
const neverLifetimeDuration = -1

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

// sessionEntry is one session as this API describes it, in the field order
// contracts/http-api.md prints — the list's element type, and the shape the
// contract says a detail response repeats.
//
// It exists so that what leaves the daemon is a decision made once, in a type
// whose fields are all there is. session.Session carries a TokenHash and the
// contract's entry does not; the safety of that is not "remember to leave it
// out" but that there is nowhere here to put it. The same goes for anything a
// later field on the record might hold — a record grows, this does not, and a
// response that should carry more is a contract change with a diff.
//
// Every instant is a formatted string for the reason createResponse's are, and
// expires_at comes off the record's own TokenExpiry so a listed session's
// deadline is the one the reaper will enforce.
type sessionEntry struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	WorkDir      string `json:"work_dir"`
	State        string `json:"state"`
	CreatedAt    string `json:"created_at"`
	ExpiresAt    string `json:"expires_at"`
	LastActivity string `json:"last_activity"`

	// StartCommand is the name of the command this session was started with
	// (#38). A name, never the command line — the line is configuration and a
	// response carrying it would put the daemon's own arguments on the wire.
	//
	// omitempty, so a session started before this existed, and one started with
	// the daemon's default, are the same bytes they always were. An adopted
	// session has none either: the daemon did not start it and does not know
	// what did, and guessing "default" would be inventing a fact.
	StartCommand string `json:"start_command,omitempty"`

	// Adopted says the session was reconciled from the host at startup rather
	// than created through this API (FR-023). It changes no rule — an adopted
	// session is owned, deadlined, and reaped exactly as any other — and is
	// here because an operator looking at a fleet after a restart needs to be
	// able to tell which of these the daemon did not start.
	Adopted bool `json:"adopted"`

	// Token is the bearer credential for a session adopted at startup, present
	// in exactly one response and omitted from every other. Adoption happens
	// with nobody asking, so there is no reply to hand a credential to; the
	// first list by the owner is the first time there is one, and that caller
	// has already proved layer 2 and owns the session — the same standing a
	// create has when it receives a token.
	//
	// omitempty is load-bearing. Every ordinary entry must be byte-identical to
	// what it was before this field existed, so a detail and a list entry stay
	// one object (contracts/http-api.md) and no client learns to expect a token
	// that will never come again.
	Token string `json:"token,omitempty"`
}

// listResponse is the contract's 200 body for GET /sessions: one object with one
// array, so that a future addition — a count, a cursor — is a field rather than
// a change of the document's type.
type listResponse struct {
	Sessions []sessionEntry `json:"sessions"`
}

// entryFor renders one record as the contract's entry. It is the only place a
// session becomes something a client sees, so the list and the detail cannot
// describe the same session two ways.
func entryFor(s session.Session) sessionEntry {
	return sessionEntry{
		ID:      s.ID,
		Name:    s.Name,
		WorkDir: s.WorkDir,
		State:   string(s.State),

		CreatedAt: s.CreatedAt.UTC().Format(timestampFormat),
		ExpiresAt: s.TokenExpiry().UTC().Format(timestampFormat),

		LastActivity: s.LastActivity.UTC().Format(timestampFormat),
		StartCommand: s.StartCommand,
		Adopted:      s.Adopted,
	}
}

// destroyResponse is the contract's 200 body for DELETE /sessions/{id}: the
// daemon's own ID for the session, and the claim that it is gone.
//
// Destroyed is always true, and there is deliberately no path that writes a
// false. The contract prints the field, so it is here; but a teardown that could
// not be confirmed is a 409, and a 200 carrying "destroyed":false would be a
// second, quieter way of reporting the one thing Principle VI does not let this
// daemon report quietly.
type destroyResponse struct {
	ID        string `json:"id"`
	Destroyed bool   `json:"destroyed"`
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

	// errCreateCapReached is the concurrent-session cap doing its job (FR-036),
	// and it is the one refusal in this file that says nothing was wrong with the
	// request. It belongs in the trail precisely because it is not an error: an
	// operator seeing it repeatedly is looking at either a fleet the reaper is
	// not collecting or a cap set too low for the way the host is used.
	errCreateCapReached = errors.New("the concurrent-session cap was reached, so the session was refused")

	// errResponseUnencodable cannot happen for a struct of strings, and is here
	// so that it cannot happen *silently* if a later field makes it possible.
	errResponseUnencodable = errors.New("the response could not be encoded")

	// errListNoCaller is unreachable behind the middleware, and fails closed for
	// the reason errCreateNoCaller does — more sharply, if anything: the owner is
	// this route's entire authorisation, so a list reached without one would be
	// asking for every session owned by the zero CallerID. Store.List answers
	// that with nothing today, and that is a property of the store rather than a
	// guarantee this handler is entitled to rest on.
	errListNoCaller = errors.New("the list handler was reached with no authenticated caller")

	// errDetailNoSession is unreachable behind the layer-3 resolver, and fails
	// closed rather than falling back to the {id} in the path: a handler that
	// read the path itself would be describing a session on a caller's say-so,
	// which on this route means handing one caller another's fleet one ID at a
	// time.
	errDetailNoSession = errors.New("the detail handler was reached with no resolved session")

	// errDestroyNoSession is unreachable behind the layer-3 resolver, and fails
	// closed for the reason errDetailNoSession does — most sharply of the four,
	// since a handler that fell back to the {id} in the path would be killing a
	// window on a caller's say-so, and a kill is the one action on this API that
	// cannot be taken back.
	errDestroyNoSession = errors.New("the destroy handler was reached with no resolved session")

	// errDestroyOrphaned is the loud one on this route, and it means what
	// errCreateOrphaned means: tmux may have been left with a live unsandboxed
	// shell (Principle VI). It is the reason the 409 records, and the trail is the
	// only durable copy of it — the caller's 409 is gone the moment its client
	// exits, and the operator who has to go and look is reading journalctl.
	errDestroyOrphaned = errors.New("a tmux session may have survived its teardown and could not be confirmed gone")

	// errDestroyRefused is the fail-closed reason for a teardown that failed for
	// a reason no sentinel explains. It answers 500 rather than 409 because a 409
	// is a specific claim — this session may still be alive — and a failure
	// nobody classified is not evidence for it. Nothing is lost by the caution:
	// Manager.Destroy drops the record only on a confirmed teardown, so the
	// record survives either answer and the reaper can still collect it.
	errDestroyRefused = errors.New("the session could not be destroyed")

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

	lifetime, err := parseLifetimeOverride(req.Lifetime)
	if err != nil {
		s.refuseCreate(w, r, err)
		return
	}

	created, token, err := s.sessions.Create(r.Context(), session.CreateRequest{
		Owner:        caller.ID,
		Name:         req.Name,
		WorkDir:      req.WorkDir,
		StartCommand: req.StartCommand,
		Resume:       req.Resume,
		Lifetime:     lifetime,
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
// A full fleet is a 429 and not a 400, because nothing the caller sent is wrong:
// the request was well-formed and the host has no room for it (FR-036). That
// distinction is the whole value of the status to a client — a 400 says "fix the
// request", and the only fix for this one is to wait or to destroy a session.
//
// Everything else is a 500. That includes the orphan case, where the record is
// kept and the caller holds no token: the session that may still be running is
// drivable by nobody and collectable by the daemon, which is the intended end
// state and not a success to report as one.
func (s *Server) refuseCreate(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, session.ErrInvalidName), errors.Is(err, session.ErrInvalidWorkDir):
		s.rejectBadRequest(w, r, createReason(err))
	case errors.Is(err, session.ErrTooManySessions):
		s.failTooManyRequests(w, r, errCreateCapReached)
	case errors.Is(err, session.ErrOrphanedSession):
		s.failInternal(w, r, errCreateOrphaned)
	default:
		s.failInternal(w, r, errCreateRefused)
	}
}

// failTooManyRequests writes the contract's 429 and records why, which is the
// half that outlives the request.
//
// It takes the reason rather than authoring one, because two different
// conditions share this status: the concurrent-session cap here, and the create
// rate limit T034 adds. The caller is told neither — the response is the same
// bytes either way — and the operator reads which it was in the trail, exactly
// as they do for the four refusals behind the uniform 404.
func (s *Server) failTooManyRequests(w http.ResponseWriter, r *http.Request, reason error) {
	AuditFrom(r.Context()).Deny(reason.Error())

	w.Header().Set(headerContentType, contentTypeJSON)
	w.WriteHeader(http.StatusTooManyRequests)
	if _, err := w.Write(bodyTooManyRequests); err != nil {
		s.report(fmt.Errorf("write the too-many-requests response: %w", err))
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
		session.ErrUnknownStartCommand,
		session.ErrInvalidLifetime,
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

// listSessions is GET /sessions: the caller's own fleet and nobody else's
// (contracts/http-api.md, FR-032).
//
// This is the one route that is caller-scoped rather than session-scoped: it
// names no session, so it presents no bearer token and layer 3 never runs. That
// makes the owner the whole of its authorisation, which is why the identity
// comes from the context alone and is passed *into* the lookup rather than being
// compared against what came back. There is no filtering step here that could be
// written wrongly, and no place to put one — a caller's sessions are what
// Manager.List returns for their identity.
//
// The response is built field by field through entryFor rather than by
// marshalling the records. A Session carries the token hash, and the difference
// between a list that never shows it and one that happens not to today is
// exactly the difference between a type with no such field and a struct tag
// somebody could remove (FR-013).
//
// Nothing is read from the host. A list is a read of the daemon's own records,
// so it costs no tmux command and cannot be made slow, or made to fail, by the
// state of any session in it.
func (s *Server) listSessions(w http.ResponseWriter, r *http.Request) {
	caller, ok := CallerFrom(r.Context())
	if !ok {
		s.failInternal(w, r, errListNoCaller)
		return
	}

	// Claimed before the list is read, so an entry minted here is described by
	// the record that already carries it. A session adopted at startup has no
	// credential until this moment — see Manager.ClaimPending — and this is the
	// one response that will ever carry it (FR-013, FR-021).
	//
	// A failure here is not fatal to the list. The sessions that were claimed
	// really were, and answering 500 would strand the rest of the fleet behind
	// one that could not be minted for; the trail records the failure.
	claimed, err := s.sessions.ClaimPending(caller.ID)
	if err != nil {
		s.report(fmt.Errorf("claim credentials for adopted sessions: %w", err))
	}

	owned := s.sessions.List(caller.ID)

	// Made rather than declared, so a caller with no sessions is answered with an
	// empty array and not a null: "you have none" is a fact, and a client that
	// has to treat the two spellings alike is one this API has made guess.
	entries := make([]sessionEntry, 0, len(owned))
	for _, one := range owned {
		entry := entryFor(one)
		entry.Token = claimed[one.ID]
		entries = append(entries, entry)
	}

	// No SetSessionID. The record says which caller listed and when, because a
	// list is about no single session — and stamping one of the returned IDs on
	// it would make the trail claim an operation on a session that was only read
	// about.
	s.writeJSON(w, r, http.StatusOK, listResponse{Sessions: entries})
}

// sessionDetail is GET /sessions/{id}: one session, described exactly as the
// list describes it (contracts/http-api.md).
//
// The session comes from the context and nowhere else. Layer 3 has already
// matched the {id} against a record the caller owns and the credential issued
// for it, which is the whole of this route's authorisation — so there is no
// ownership check here to write wrongly, and no second lookup that could answer
// for a session the resolver did not approve.
//
// The body is entryFor's, not a shape of its own. The contract says a detail is
// the same object as one list entry, and the way to keep that true is for there
// to be one renderer: a second struct here would be a second decision about what
// a session discloses, free to drift into carrying a token hash on the day
// somebody adds a field to it (FR-013).
//
// Nothing is read from the host, for the reason a list reads nothing: a detail
// is a read of the daemon's own record, so it costs no tmux command and cannot
// be made to fail by the state of the window behind it.
func (s *Server) sessionDetail(w http.ResponseWriter, r *http.Request) {
	resolved, ok := SessionFrom(r.Context())
	if !ok {
		s.failInternal(w, r, errDetailNoSession)
		return
	}

	// No SetSessionID. The resolver already stamped the record's own ID on the
	// trail, and stamping it again here would be this handler asserting something
	// it did not establish.
	s.writeJSON(w, r, http.StatusOK, entryFor(resolved))
}

// destroySession is DELETE /sessions/{id}: tear the session down, and say it is
// gone only once the host has confirmed that (contracts/http-api.md, FR-019).
//
// The session comes from the context and nowhere else, exactly as it does for a
// detail and a prompt. It matters most here: this is the only route whose action
// cannot be taken back, so a handler that read the {id} out of the path would be
// killing a window on a caller's say-so.
//
// The two answers are not two spellings of one. A 200 means the host was asked
// afterwards and said the session is gone; a 409 means it was not gone, or could
// not be asked, and the record is kept — a record is the only thing carrying an
// owner and two deadlines for a session that may still be running. Which of the
// two a caller gets is Manager.Destroy's finding, and this handler's whole job
// is to pass it on unsoftened.
//
// A second DELETE for the same ID is a 404, byte-identical to one for an ID that
// never existed. That is a decision and not an inheritance: destroy is not
// idempotent here, because making it so would mean the resolver telling
// "destroyed" apart from "never yours" — and that difference is what FR-033
// closes. The caller that got the 200 already knows; the caller that did not is
// asking about a session it cannot name.
func (s *Server) destroySession(w http.ResponseWriter, r *http.Request) {
	resolved, ok := SessionFrom(r.Context())
	if !ok {
		s.failInternal(w, r, errDestroyNoSession)
		return
	}

	if err := s.sessions.Destroy(r.Context(), resolved); err != nil {
		s.refuseDestroy(w, r, err)
		return
	}

	// No SetSessionID. The resolver already stamped the record's own ID on the
	// trail, which is what makes the 200 and the 409 name the same session
	// without this handler asserting anything it did not establish.
	s.writeJSON(w, r, http.StatusOK, destroyResponse{ID: resolved.ID, Destroyed: true})
}

// refuseDestroy maps a Destroy failure onto the answer the contract gives it.
//
// There is one case with a status of its own, and it is the one an operator has
// to be able to find: the session may have survived. Everything else is a 500
// with no detail, for the reason a failed capture is — how tmux failed is a fact
// about the host, and a caller that could tell those apart would have an oracle
// about a machine it cannot otherwise see.
func (s *Server) refuseDestroy(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, session.ErrOrphanedSession) {
		s.failTeardownUnverified(w, r)
		return
	}
	s.failInternal(w, r, errDestroyRefused)
}

// failTeardownUnverified writes the contract's 409 and records the fact behind
// it, which is the half that outlives the request.
//
// It is a function of its own next to failInternal for the reason that one is
// one: a status, a header, and a body assembled at a call site are three things
// that can be ordered wrongly, and this response is the one place this API
// deliberately says more than "something went wrong".
func (s *Server) failTeardownUnverified(w http.ResponseWriter, r *http.Request) {
	AuditFrom(r.Context()).Deny(errDestroyOrphaned.Error())

	w.Header().Set(headerContentType, contentTypeJSON)
	w.WriteHeader(http.StatusConflict)
	if _, err := w.Write(bodyTeardownUnverified); err != nil {
		s.report(fmt.Errorf("write the unverified-teardown response: %w", err))
	}
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
