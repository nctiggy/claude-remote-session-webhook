// Internal test, matching the rest of the package. The isolation rule is about
// what a handler may *reach*, so the assertions here need the store, the tmux
// fake, and the audit sink that newAuditedServer exposes and an external test
// could not see.
//
// This is the suite docs/auth-and-sessions.md calls non-negotiable and the
// constitution's quality gate names as "cross-session isolation still holds".
// Every test in it sweeps the real router rather than a list written here, so a
// seventh {id} route inherits all of it without anyone remembering to.
//
// One note on the recipe, which is worth reading before changing anything here.
// docs/auth-and-sessions.md writes it as "create A, produce distinctive output,
// create B as a different caller, assert every read endpoint scoped to B returns
// nothing from A". Milestone 1 has exactly one operator identity, so a request
// scoped to a second owner's session cannot be authenticated *as* that owner: it
// is refused at layer 3 before any handler runs, and the interesting half of the
// recipe would be unreachable. So the recipe is split, and both halves are here.
// Sessions A and B belong to the same caller, which is where a handler reaching
// past its resolved record would really hand back the wrong session's content
// (FR-035); the second owner's session is the FR-033 half, where the whole claim
// is that it is indistinguishable from one that never existed.
package httpapi

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/nctiggy/claude-remote-session-webhook/internal/auth"
	"github.com/nctiggy/claude-remote-session-webhook/internal/session"
	"github.com/nctiggy/claude-remote-session-webhook/internal/tmuxctl"
)

// otherOperator is the synthetic second identity the resolved decisions in
// ralph/IMPLEMENTATION_PLAN.md call for. Nothing in this milestone can
// authenticate as it — that is the point. Its session exists to be unreachable,
// so that milestone 2's real second identity does not arrive to find the
// ownership check missing.
const otherOperator auth.CallerID = "a-second-operator"

// isolated is one session together with everything about it that must never
// surface anywhere else: in another session's response, in a host command
// another request caused, or in another request's audit record.
type isolated struct {
	session.Session

	token string
	pane  string
}

// marks is what a leak would look like. Each is a distinct word, so a failure
// names the field that escaped rather than reporting that "alpha" turned up
// somewhere. The tmux name is deliberately not among them: it derives from the
// ID, so the ID already covers it and a second entry would only make a leak
// report twice.
func (i isolated) marks() map[string]string {
	return map[string]string{
		"session id":        i.ID,
		"name":              i.Name,
		"working directory": i.WorkDir,
		"pane content":      i.pane,
		"bearer credential": i.token,
	}
}

// absentFrom fails for every mark of this session that appears in text.
func (i isolated) absentFrom(t *testing.T, what, text string) {
	t.Helper()

	for field, mark := range i.marks() {
		if strings.Contains(text, mark) {
			t.Errorf("%s carries another session's %s (%q): %q", what, field, mark, text)
		}
	}
}

// isolationFixture is three sessions on one server: two the caller owns and one
// it does not, each marked so that a byte of one appearing in an answer about
// another is unmistakable.
type isolationFixture struct {
	*testServer

	a      isolated
	b      isolated
	theirs isolated

	// unknown is an ID of the right shape that was never issued. It is the
	// answer every refusal in this file is compared against: FR-033 is satisfied
	// only if "not yours" and "never existed" are the same bytes.
	unknown string
}

func newIsolationFixture(t *testing.T) isolationFixture {
	t.Helper()

	s := newAuditedServer(t)

	unknown, err := session.NewID()
	if err != nil {
		t.Fatalf("session.NewID = _, %v; want an id", err)
	}

	return isolationFixture{
		testServer: s,
		a:          s.isolate(t, "alpha", auth.CallerOperator),
		b:          s.isolate(t, "bravo", auth.CallerOperator),
		theirs:     s.isolate(t, "charlie", otherOperator),
		unknown:    unknown,
	}
}

// isolate plants one marked session and arranges the distinctive output the
// isolation rule asks for.
//
// The pane is set rather than produced, for the reason plant seeds a session
// rather than creating one: a fixture that drove tmux would record calls the
// request under test did not make, and the argv sweep below would have nothing
// to say.
func (s *testServer) isolate(t *testing.T, mark string, owner auth.CallerID) isolated {
	t.Helper()

	planted, issued := s.fixture.plant(t, session.Session{
		Owner:   owner,
		Name:    mark + "-name",
		WorkDir: filepath.Join(s.fixture.root, mark+"-workdir"),
		State:   session.StateRunning,
	})

	// One line and no newline: a mark has to survive JSON encoding to be
	// searchable in a raw body, and encoding/json escapes a newline.
	pane := mark + "-pane"
	s.fixture.tmux.SetPane(planted.TmuxName(), pane)

	return isolated{Session: planted, token: issued, pane: pane}
}

// sessionScopedRoutes is every route layer 3 applies to, read off the real
// router for the reason the layer-2 and layer-3 sweeps do it: a hand-written
// list is exactly what a seventh {id} route would be forgotten from, and here a
// forgotten route is one where another session's pane is reachable.
func sessionScopedRoutes(t *testing.T) []Route {
	t.Helper()

	var scoped []Route
	for _, route := range newTestServer(t, loopbackListen).Routes() {
		if route.SessionScoped() {
			scoped = append(scoped, route)
		}
	}
	if len(scoped) == 0 {
		t.Fatal("the router registered no session-scoped routes, so every sweep in this file would pass vacuously")
	}
	return scoped
}

// driveScoped sends one signed request at a session-scoped route with a bearer
// credential of the caller's choosing.
//
// The signing instant is a parameter because the signature covers the timestamp
// and the body and nothing else: two requests to the same route at the same
// instant share a signature, and the replay cache would refuse the second as a
// replay where the test was asking about isolation.
func driveScoped(t *testing.T, s *testServer, route Route, id, presented string, at time.Time) *httptest.ResponseRecorder {
	t.Helper()

	body := bodyFor(s.fixture, route)
	path := strings.ReplaceAll(route.Pattern, "{"+pathValueID+"}", id)

	req := httptest.NewRequest(route.Method, path, bytes.NewReader(body))
	signRequest(t, req, body, at)
	if presented != "" {
		// After signing, because layer 3 is a separate credential and not part
		// of the signed payload.
		req.Header.Set(headerAuthorization, bearerScheme+presented)
	}

	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	return rec
}

// assertSameAnswer is FR-033 stated as bytes. Any difference at all — a status,
// a body, a header, a Content-Length — is an oracle that turns a session ID into
// something worth guessing at, and behind an ID is an unsandboxed shell.
func assertSameAnswer(t *testing.T, what string, got, want *httptest.ResponseRecorder) {
	t.Helper()

	if got.Code != want.Code {
		t.Errorf("%s = %d; an id that never existed answers %d", what, got.Code, want.Code)
	}
	if !reflect.DeepEqual(got.Header(), want.Header()) {
		t.Errorf("%s answered with headers %v; an id that never existed answers %v", what, got.Header(), want.Header())
	}
	if !bytes.Equal(got.Body.Bytes(), want.Body.Bytes()) {
		t.Errorf("%s answered %q; an id that never existed answers %q", what, got.Body, want.Body)
	}
}

// TestNoAnswerScopedToOneSessionCarriesAnother is FR-035 on the one path where
// it is actually reachable: both sessions belong to the caller, so every layer
// allows the request and only the handler's choice of record decides what comes
// back.
//
// A cross-owner pair could not prove this. That request is refused at layer 3
// and never reaches a handler, so it would pass against a handler that read the
// {id} out of the path and captured whatever window it named.
func TestNoAnswerScopedToOneSessionCarriesAnother(t *testing.T) {
	t.Parallel()

	for _, route := range sessionScopedRoutes(t) {
		t.Run(route.String(), func(t *testing.T) {
			t.Parallel()

			f := newIsolationFixture(t)
			rec := driveScoped(t, f.testServer, route, f.b.ID, f.b.token, testTime)

			if want := reachedStatus[route]; rec.Code != want {
				t.Fatalf("%s scoped to the caller's own session = %d (%q); want %d", route, rec.Code, rec.Body, want)
			}

			outward := rec.Body.String() + " " + fmt.Sprint(rec.Header())
			f.a.absentFrom(t, route.String()+" scoped to another session", outward)
			f.theirs.absentFrom(t, route.String()+" scoped to another session", outward)
		})
	}
}

// TestNoRequestScopedToOneSessionAddressesAnothersWindow is the host-side half
// of the same rule, and the sharper one: a response can only carry what a
// handler read, but a command names the window it was about to act on. On DELETE
// that is the difference between tearing down the session the credential named
// and tearing down somebody else's.
func TestNoRequestScopedToOneSessionAddressesAnothersWindow(t *testing.T) {
	t.Parallel()

	for _, route := range sessionScopedRoutes(t) {
		t.Run(route.String(), func(t *testing.T) {
			t.Parallel()

			f := newIsolationFixture(t)
			rec := driveScoped(t, f.testServer, route, f.b.ID, f.b.token, testTime)

			if want := reachedStatus[route]; rec.Code != want {
				t.Fatalf("%s scoped to the caller's own session = %d (%q); want %d", route, rec.Code, rec.Body, want)
			}

			for _, call := range f.fixture.tmux.Calls() {
				// Stdin is included because prompt text travels there, and a
				// paste that reached the wrong buffer is the same defect as a
				// capture that read the wrong pane.
				issued := strings.Join(call.Argv, " ") + " " + string(call.Stdin)

				if !strings.Contains(issued, f.b.TmuxName()) {
					t.Errorf("%s ran %s %v against a target the resolved session does not name; want %q",
						route, call.Op, call.Argv, f.b.TmuxName())
				}
				f.a.absentFrom(t, route.String()+" issued "+string(call.Op), issued)
				f.theirs.absentFrom(t, route.String()+" issued "+string(call.Op), issued)
			}
		})
	}
}

// TestOneSessionsCredentialIsUselessOnAnother is the second half of the recipe
// in docs/auth-and-sessions.md — B's token on A's ID — swept across every
// session-scoped route rather than asserted on one.
//
// Both sessions belong to the caller here, which makes this the narrowest form
// of the claim: the request passes authentication, names a session the caller
// really does own, and is still refused, because owning a session is not what
// authorises a request against it (FR-014).
func TestOneSessionsCredentialIsUselessOnAnother(t *testing.T) {
	t.Parallel()

	for _, route := range sessionScopedRoutes(t) {
		t.Run(route.String(), func(t *testing.T) {
			t.Parallel()

			f := newIsolationFixture(t)
			crossed := driveScoped(t, f.testServer, route, f.a.ID, f.b.token, testTime)
			never := driveScoped(t, f.testServer, route, f.unknown, f.b.token, testTime.Add(-time.Second))

			if crossed.Code != http.StatusNotFound {
				t.Errorf("%s with another session's credential = %d (%q); want %d",
					route, crossed.Code, crossed.Body, http.StatusNotFound)
			}
			assertSameAnswer(t, route.String()+" with another session's credential", crossed, never)

			// The refusal must also be inert. A 404 that had already killed the
			// window, or pasted the prompt, would satisfy every assertion above
			// and still be the failure this file exists to prevent.
			if calls := f.fixture.tmux.Calls(); len(calls) != 0 {
				t.Errorf("%s refused the request but reached the host: %v", route, calls)
			}
			if _, err := f.fixture.store.Get(f.a.ID, auth.CallerOperator); err != nil {
				t.Errorf("%s refused the request and the named session is gone: %v", route, err)
			}
		})
	}
}

// TestAnotherOwnersSessionIsIndistinguishableFromOneThatNeverExisted is SC-005
// across every session-scoped route: a caller holding the shared secret, and
// even holding the other owner's credential, learns nothing about a session it
// does not own — not that it exists, not what it is called, not that its
// credential is the right one.
//
// The credential is presented deliberately. Ownership is checked before the
// token compare, so this is the case that proves the order: a resolver that
// matched the token first would answer differently for the right credential than
// for a wrong one, and that difference is a session ID oracle.
func TestAnotherOwnersSessionIsIndistinguishableFromOneThatNeverExisted(t *testing.T) {
	t.Parallel()

	for _, route := range sessionScopedRoutes(t) {
		t.Run(route.String(), func(t *testing.T) {
			t.Parallel()

			f := newIsolationFixture(t)
			theirs := driveScoped(t, f.testServer, route, f.theirs.ID, f.theirs.token, testTime)
			never := driveScoped(t, f.testServer, route, f.unknown, f.theirs.token, testTime.Add(-time.Second))

			if theirs.Code != http.StatusNotFound {
				t.Errorf("%s against another owner's session = %d (%q); want %d",
					route, theirs.Code, theirs.Body, http.StatusNotFound)
			}
			assertSameAnswer(t, route.String()+" against another owner's session", theirs, never)
			f.theirs.absentFrom(t, route.String()+" against another owner's session",
				theirs.Body.String()+" "+fmt.Sprint(theirs.Header()))

			if calls := f.fixture.tmux.Calls(); len(calls) != 0 {
				t.Errorf("%s refused the request but reached the host: %v", route, calls)
			}
			if _, err := f.fixture.store.Get(f.theirs.ID, otherOperator); err != nil {
				t.Errorf("%s refused the request and the other owner's session is gone: %v", route, err)
			}
		})
	}
}

// TestTheTrailNamesOnlyTheSessionTheRequestResolved is the isolation rule
// applied to the audit trail, which is a place output can bleed exactly as a
// response can — and a durable one. An operator reading journalctl must be able
// to tell which session a request acted on, which is only true if a record names
// one session and never a second.
func TestTheTrailNamesOnlyTheSessionTheRequestResolved(t *testing.T) {
	t.Parallel()

	for _, route := range sessionScopedRoutes(t) {
		t.Run(route.String(), func(t *testing.T) {
			t.Parallel()

			f := newIsolationFixture(t)
			rec := driveScoped(t, f.testServer, route, f.b.ID, f.b.token, testTime)

			if want := reachedStatus[route]; rec.Code != want {
				t.Fatalf("%s scoped to the caller's own session = %d (%q); want %d", route, rec.Code, rec.Body, want)
			}

			if got := f.only(t)["session_id"]; got != f.b.ID {
				t.Errorf("%s recorded session_id = %v; want the resolved session %q", route, got, f.b.ID)
			}
			written := f.sink.String()
			f.a.absentFrom(t, route.String()+"'s audit trail", written)
			f.theirs.absentFrom(t, route.String()+"'s audit trail", written)
		})
	}
}

// TestDestroyingOneSessionLeavesTheOthersDrivable is the isolation rule across
// the one operation that cannot be taken back. FR-020 clears the record, the
// credential hash, and the buffered output of the session that was destroyed —
// and of no other, which is a claim only a fleet of more than one can make.
func TestDestroyingOneSessionLeavesTheOthersDrivable(t *testing.T) {
	t.Parallel()

	f := newIsolationFixture(t)

	torn, _ := deleteSession(t, f.testServer, f.b.ID, f.b.token, testTime)
	if torn.Code != http.StatusOK {
		t.Fatalf("destroying one session = %d (%q); want %d", torn.Code, torn.Body, http.StatusOK)
	}

	// The survivor is still readable, with its own output rather than the
	// destroyed session's or nothing at all.
	read := driveScoped(t, f.testServer, Route{http.MethodGet, "/sessions/{id}/output"},
		f.a.ID, f.a.token, testTime.Add(-time.Second))
	if read.Code != http.StatusOK {
		t.Fatalf("reading the surviving session = %d (%q); want %d", read.Code, read.Body, http.StatusOK)
	}
	var capture map[string]any
	if err := json.Unmarshal(read.Body.Bytes(), &capture); err != nil {
		t.Fatalf("the response %q is not JSON: %v", read.Body, err)
	}
	if capture["text"] != f.a.pane {
		t.Errorf("the surviving session's output = %v; want its own pane %q", capture["text"], f.a.pane)
	}

	// The other owner's session is untouched by a teardown it had nothing to do
	// with — the record, and the window behind it.
	if _, err := f.fixture.store.Get(f.theirs.ID, otherOperator); err != nil {
		t.Errorf("destroying one session removed another owner's record: %v", err)
	}
	for _, call := range f.fixture.tmux.Calls() {
		if call.Op == tmuxctl.OpKill && !strings.Contains(strings.Join(call.Argv, " "), f.b.TmuxName()) {
			t.Errorf("the teardown killed a window the credential did not name: %v", call.Argv)
		}
	}

	// And the credential of a destroyed session is now worth exactly what a
	// credential for a session that never existed is worth.
	stale := driveScoped(t, f.testServer, Route{http.MethodGet, "/sessions/{id}"},
		f.b.ID, f.b.token, testTime.Add(-2*time.Second))
	never := driveScoped(t, f.testServer, Route{http.MethodGet, "/sessions/{id}"},
		f.unknown, f.b.token, testTime.Add(-3*time.Second))
	assertSameAnswer(t, "a destroyed session's credential", stale, never)
}
