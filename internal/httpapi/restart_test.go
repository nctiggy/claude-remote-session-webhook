// Internal test, matching update_test.go: the installer behind this route is an
// unexported field, the outcome vocabulary is unexported, and the two things
// worth asserting hardest — that the process was allowed to end only after the
// answer and the record existed, and that it was not allowed to end at all when
// the request was refused — are only visible from inside.
//
// Every literal these cases compare against is written out here rather than read
// from the constant the code writes, which is the discipline every file in this
// package follows: a test that asked the code what it does proves only that the
// code agrees with itself.
package httpapi

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/nctiggy/claude-remote-session-webhook/internal/audit"
	"github.com/nctiggy/claude-remote-session-webhook/internal/buildinfo"
)

// The restart's own answers, quoted rather than read from outcome.go and
// restart.go.
const (
	wantRestartUnconfirmedOutcome              = outcome("restart-unconfirmed")
	wantRestartRefusedOutcome                  = outcome("restart-refused")
	wantRestartPath                            = "/dashboard/restart"
	wantRestartAction             audit.Action = "dashboard.restart"
)

// restartDoor is the registered restart route with the exit replaced and
// everything else real: the same gate, the same middleware, the same trail.
//
// It drives Server.ServeHTTP rather than the handler, and that is load-bearing
// rather than tidy: a route registered with handleBrowser instead of
// handleAction would leave the cross-site defence absent from a request that can
// end this daemon, and no test of the handler alone would notice.
//
// The exit is fakeUpdatePath's, which is the same fake the update's cases drive
// and stands in for exactly the collaborator this route reaches. Only the
// installer is wired: the fetcher and the stager are left nil on purpose, so a
// restart that reached for either would nil-panic here rather than pass.
type restartDoor struct {
	*testServer
	keys  *keyServer
	steps *fakeUpdatePath

	// answer is the recorder the request in flight is writing into, so that
	// atExit can look at the response from inside the exit.
	answer *httptest.ResponseRecorder
}

func newRestartDoor(t *testing.T) *restartDoor {
	t.Helper()

	keys := newKeyServer(t)
	steps := newFakeUpdatePath(t)
	d := &restartDoor{testServer: newAuditedServerWith(t, keys.validator(t)), keys: keys, steps: steps}
	d.updates = selfUpdate{installer: steps}
	return d
}

// confirmed is the form the restart button submits: the render's page token, and
// the confirming step.
func (d *restartDoor) confirmed(t *testing.T) url.Values {
	t.Helper()

	form := url.Values{}
	form.Set(fieldPageToken, mustMint(t, d.pageKey, testOperatorEmail, testTime))
	form.Set(fieldConfirm, confirmYes)
	return form
}

func (d *restartDoor) post(t *testing.T, form url.Values) *httptest.ResponseRecorder {
	t.Helper()

	return d.send(t, http.MethodPost, wantRestartPath, secFetchSiteSameOrigin, form)
}

// send is post with the method, the path and the browser's own account of where
// the request came from chosen by the caller — the three things the cross-site
// and wrong-method cases have to vary and a rendered form never does.
func (d *restartDoor) send(t *testing.T, method, path, site string, form url.Values) *httptest.ResponseRecorder {
	t.Helper()

	r := httptest.NewRequest(method, path, strings.NewReader(form.Encode()))
	r.Header.Set(headerContentType, contentTypeForm)
	r.Header.Set(headerAccessAssertion, d.keys.mint(t, d.keys.claims()))
	if site != absent {
		r.Header.Set(headerSecFetchSite, site)
	}

	w := httptest.NewRecorder()
	d.answer = w
	d.ServeHTTP(w, r)
	return w
}

// record asserts the one record a request produced and returns it.
func (d *restartDoor) record(t *testing.T, decision audit.Decision) map[string]any {
	t.Helper()

	rec := d.only(t)
	if got, want := rec["action"], string(wantRestartAction); got != want {
		t.Errorf("action = %v; want %v — a restart is not an update, not a session action and not a page view", got, want)
	}
	if got, want := rec["decision"], string(decision); got != want {
		t.Errorf("decision = %v; want %v", got, want)
	}
	return rec
}

// wantRestartingPage is the restart's answer, which is a page rather than a
// redirect.
//
// Every ordinary action on this door answers 303 and this one deliberately does
// not, for the reason the update does not: a redirect tells the browser to go
// and ask for a page from a daemon that is in the act of stopping, so the
// operator watches their own restart turn into a connection error at the one
// moment they most need to be told it is working.
//
// The version is asserted present because it is what anything waiting has to
// poll for, and the sentence is asserted *not* to claim an installation: a
// restart installs nothing, and a page saying otherwise would be this daemon
// reporting something it did not do.
func wantRestartingPage(t *testing.T, w *httptest.ResponseRecorder) {
	t.Helper()

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d (%s); want %d — a restart answers in place, because a redirect points at a daemon about to stop existing",
			w.Code, w.Body.String(), http.StatusOK)
	}
	if got := w.Header().Get(headerLocation); got != "" {
		t.Errorf("%s = %q; want none — this answer is the page, not somewhere to go", headerLocation, got)
	}
	body := w.Body.String()
	if !strings.Contains(body, `data-becoming="`+buildinfo.Version+`"`) {
		t.Errorf("the page does not name the version this daemon is coming back as, so nothing can wait for it:\n%s", body)
	}
	if strings.Contains(body, "Installing") {
		t.Errorf("the page says it is installing something; a restart runs the binary that is already here:\n%s", body)
	}
	if !strings.Contains(body, "Restarting") {
		t.Errorf("the page never says what is happening, so an operator with no script is looking at a spinner and nothing else:\n%s", body)
	}
}

// TestRestartEndsTheProcessAfterAnsweringAndRecording is the whole of T004's
// success claim: the confirming step is read, the process is ended exactly once,
// and the answer and the record both exist before it is.
//
// **Must fail when** the exit happens inside the handler, before the flush, or
// before the emit. That ordering is not decoration — os.Exit inside the handler
// severs the connection before net/http finishes the response, and what reached
// the operator was a Cloudflare 502 for a restart that was working.
func TestRestartEndsTheProcessAfterAnsweringAndRecording(t *testing.T) {
	t.Parallel()

	d := newRestartDoor(t)

	var (
		statusAtExit  int
		flushedAtExit bool
		recordsAtExit int
	)
	d.steps.atExit = func() {
		statusAtExit = d.answer.Code
		flushedAtExit = d.answer.Flushed
		recordsAtExit = len(d.records(t))
	}

	w := d.post(t, d.confirmed(t))

	wantRestartingPage(t, w)

	// Nothing on the update path was reached. A restart that asked a release feed
	// anything would be an update wearing another name — and, on a host with no
	// network, one that could not restart.
	if got := d.steps.askedVersions; len(got) != 0 {
		t.Errorf("the restart asked for releases %v; want none — it installs nothing", got)
	}
	if got := d.steps.fetched; len(got) != 0 {
		t.Errorf("the restart fetched %v; want nothing", got)
	}
	if len(d.steps.staged) != 0 || len(d.steps.swapped) != 0 {
		t.Errorf("the restart staged %d and swapped %d; want neither — the binary at ExecStart is the one that was already there",
			len(d.steps.staged), len(d.steps.swapped))
	}

	d.steps.waitForExit(t)
	if d.steps.count() != 1 {
		t.Fatalf("exited %d times; want exactly 1 — a restart that does not exit is a button that does nothing", d.steps.count())
	}
	if statusAtExit != http.StatusOK {
		t.Errorf("the response was %d when the process was allowed to end; want %d written first — an answer that has not been written cannot arrive from a process that has ended", statusAtExit, http.StatusOK)
	}
	if !flushedAtExit {
		t.Errorf("the answer had not been flushed when the process was allowed to end; the operator's browser would be waiting on a connection that is about to close")
	}
	// One record existed before the process was allowed to end.
	//
	// What this catches is an exit taken inline: a handler that ended the process
	// on its way out would take the record with it, and a restart nobody can
	// audit is a change to this host with no answer to "who asked for it".
	//
	// What it deliberately does not claim is that the *handler* wrote that
	// record. It cannot: the exit waits out the grace period, by which time the
	// handler has returned and the middleware's own deferred emit has written one
	// either way. The handler emits first regardless, and the argument for that
	// is at the emit, where it is an ordering claim rather than a testable one.
	if recordsAtExit != 1 {
		t.Errorf("the trail held %d records when the process was allowed to end; want 1 — a restart with no record is a change to this host nobody can audit", recordsAtExit)
	}

	d.record(t, audit.Allow)
}

// TestRestartRequiresConfirm is FR-029a at this route: the confirming step is
// read before anything happens, and only the one spelling counts.
//
// **Must fail when** the field is ignored, or read loosely enough that a
// checkbox's own `on` or a bare `true` gets through. The counter is the
// assertion that matters — a check that ran after the exit was scheduled would
// answer with a refusal from a process already on its way down.
func TestRestartRequiresConfirm(t *testing.T) {
	t.Parallel()

	unconfirmed := map[string]string{
		"the field was absent":         absent,
		"the field was empty":          "",
		"a checkbox posted on":         "on",
		"something posted true":        "true",
		"something posted 1":           "1",
		"the case was not the same":    "YES",
		"the value carried whitespace": "yes ",
	}
	for name, value := range unconfirmed {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			d := newRestartDoor(t)
			form := d.confirmed(t)
			if value == absent {
				form.Del(fieldConfirm)
			} else {
				form.Set(fieldConfirm, value)
			}

			w := d.post(t, form)

			wantOutcome(t, w, wantRestartUnconfirmedOutcome)
			if got := d.steps.count(); got != 0 {
				t.Errorf("an unconfirmed restart ended the process %d times; want 0", got)
			}
			rec := d.record(t, audit.Deny)
			if got, want := rec["reason"], errRestartUnconfirmed.Error(); got != want {
				t.Errorf("reason = %v; want %q", got, want)
			}
		})
	}
}

// TestRestartCrossSiteBothHalves is AR-005 and the security half of this task:
// each half of the cross-site defence refuses this route on its own, and every
// case here *satisfies* the half it is not testing rather than switching it off.
//
// **Must fail when** the route is registered by hand instead of through
// handleAction, or when either half is missing — a route that only checked the
// token would serve the first two cases, and one that only checked the initiator
// would serve the last two.
//
// The counter matters more than the status. A gate that ran after the handler
// would answer 403 from a process already exiting, which is the one failure a
// status code cannot see (FR-003).
func TestRestartCrossSiteBothHalves(t *testing.T) {
	t.Parallel()

	cases := map[string]func(t *testing.T, d *restartDoor) (url.Values, string){
		"the browser said the request came from another site": func(t *testing.T, d *restartDoor) (url.Values, string) {
			t.Helper()
			// The token is present and valid: only the initiator differs.
			return d.confirmed(t), "cross-site"
		},
		"the browser sent no initiator at all": func(t *testing.T, d *restartDoor) (url.Values, string) {
			t.Helper()
			// Absent refuses on a route that changes something. This is the half
			// that would be lost by registering the route as an ordinary page.
			return d.confirmed(t), absent
		},
		"the form carried no page token": func(t *testing.T, d *restartDoor) (url.Values, string) {
			t.Helper()
			// Same-origin, as a rendered form is: only the token differs.
			form := d.confirmed(t)
			form.Del(fieldPageToken)
			return form, secFetchSiteSameOrigin
		},
		"the form carried another identity's page token": func(t *testing.T, d *restartDoor) (url.Values, string) {
			t.Helper()
			form := d.confirmed(t)
			form.Set(fieldPageToken, mustMint(t, d.pageKey, "somebody-else@example.com", testTime))
			return form, secFetchSiteSameOrigin
		},
	}
	for name, build := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			d := newRestartDoor(t)
			form, site := build(t, d)

			w := d.send(t, http.MethodPost, wantRestartPath, site, form)

			if w.Code != wantActionStatus {
				t.Errorf("status = %d (%s); want %d — the gate's uniform refusal", w.Code, w.Body.String(), wantActionStatus)
			}
			if got := w.Body.String(); got != wantActionBody {
				t.Errorf("body = %q; want %q", got, wantActionBody)
			}
			if got := d.steps.count(); got != 0 {
				t.Errorf("a request the gate refused ended the process %d times; want 0", got)
			}
			if got, want := d.only(t)["action"], string(audit.ActionDashboardReject); got != want {
				t.Errorf("action = %v; want %v — an identity that got in and then failed the cross-site check is not an identity that never got in", got, want)
			}
		})
	}
}

// TestRestartEmitsExactlyOneAuditRecord is FR-041 on the second route that does
// not return in production.
//
// **Must fail when** the handler's early emit is left to race the middleware's
// deferred one. The success case is the sharp end: the record is written by the
// handler, before the exit, and the middleware's own emit still runs afterwards
// in this test — where the exit is a fake and the handler does return. Two
// records there is the guard in emit having been removed.
func TestRestartEmitsExactlyOneAuditRecord(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		build    func(t *testing.T, d *restartDoor) url.Values
		wired    bool
		decision audit.Decision
	}{
		"a restart that ended the process": {
			build:    func(t *testing.T, d *restartDoor) url.Values { t.Helper(); return d.confirmed(t) },
			wired:    true,
			decision: audit.Allow,
		},
		"a restart that was not confirmed": {
			build: func(t *testing.T, d *restartDoor) url.Values {
				t.Helper()
				form := d.confirmed(t)
				form.Del(fieldConfirm)
				return form
			},
			wired:    true,
			decision: audit.Deny,
		},
		"a restart on a daemon with no way to end itself": {
			build:    func(t *testing.T, d *restartDoor) url.Values { t.Helper(); return d.confirmed(t) },
			wired:    false,
			decision: audit.Deny,
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			d := newRestartDoor(t)
			if !tc.wired {
				d.updates = selfUpdate{}
			}
			form := tc.build(t, d)

			d.post(t, form)

			if tc.decision == audit.Allow {
				d.steps.waitForExit(t)
			}
			d.record(t, tc.decision)
		})
	}
}

// TestARestartWithNoWayToEndTheProcessRefusesRatherThanPanics is fail-closed on
// the path that should not happen.
//
// A server built by newServer carries no installer at all, which is deliberate:
// it is what keeps a case in this package that reaches this route from ending
// the process running the suite. So the branch is reachable, and what it must do
// is refuse with a reason rather than dereference a nil interface — a panic here
// would be answered as a 500 by net/http, with no record and no page.
//
// **Must fail when** the wired check is dropped: the route then panics on a
// server with no update path, which is exactly the state every other test in
// this package builds.
func TestARestartWithNoWayToEndTheProcessRefusesRatherThanPanics(t *testing.T) {
	t.Parallel()

	d := newRestartDoor(t)
	d.updates = selfUpdate{}

	w := d.post(t, d.confirmed(t))

	wantOutcome(t, w, wantRestartRefusedOutcome)
	rec := d.record(t, audit.Deny)
	if got, want := rec["reason"], errRestartUnwired.Error(); got != want {
		t.Errorf("reason = %v; want %q", got, want)
	}
}

// TestARestartIsNoRouteOnAnyOtherMethod is FR-033 at this route: a method this
// route does not serve is answered as a path nothing claims, not as a 405 naming
// what does work.
//
// **Must fail when** the method is dropped from the pattern. The route would
// then match a GET, and a link — which carries no form and therefore no token —
// would reach the gate rather than the not-found page.
func TestARestartIsNoRouteOnAnyOtherMethod(t *testing.T) {
	t.Parallel()

	for _, method := range []string{http.MethodGet, http.MethodPut, http.MethodDelete} {
		t.Run(method, func(t *testing.T) {
			t.Parallel()

			d := newRestartDoor(t)

			w := d.send(t, method, wantRestartPath, secFetchSiteSameOrigin, d.confirmed(t))

			if w.Code != http.StatusNotFound {
				t.Errorf("status = %d; want %d — a method this route does not serve is a path nothing claims", w.Code, http.StatusNotFound)
			}
			if got := w.Header().Get("Allow"); got != "" {
				t.Errorf("Allow = %q; want none — naming the methods that work maps the route table for a scanner", got)
			}
			if got := d.steps.count(); got != 0 {
				t.Errorf("a %s ended the process %d times; want 0", method, got)
			}
		})
	}
}
