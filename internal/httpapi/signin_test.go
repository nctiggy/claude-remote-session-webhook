package httpapi

// The sign-in relay's three routes, and one claim that matters more than the
// rest: the code an operator types is a live credential, and it must not reach
// the trail, an outcome, a redirect, or the page it came from.

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/nctiggy/claude-remote-session-webhook/internal/audit"
	"github.com/nctiggy/claude-remote-session-webhook/internal/loginrelay"
	"github.com/nctiggy/claude-remote-session-webhook/internal/tmuxctl"
)

const (
	pathSignIn       = "/dashboard/signin"
	pathSignInCode   = "/dashboard/signin/code"
	pathSignInCancel = "/dashboard/signin/cancel"

	// missingBinary is a start command naming a program this host does not have.
	//
	// Deliberate: it lets a test drive every tmux-side behaviour through the fake
	// while SignedIn's subprocess fails, which keeps the suite from asking the
	// developer's own machine whether it happens to be signed in to Claude. The
	// panel renders that as "could not ask", which is a branch worth exercising
	// anyway.
	missingBinary = "crswd-no-such-binary-for-tests"
)

type signInDoor struct {
	*testServer
	keys *keyServer
	tmux *tmuxctl.Fake
}

func newSignInDoor(t *testing.T) *signInDoor {
	t.Helper()

	keys := newKeyServer(t)
	fake := tmuxctl.NewFake()
	d := &signInDoor{testServer: newAuditedServerWith(t, keys.validator(t)), keys: keys, tmux: fake}

	relay, err := loginrelay.New(fake, missingBinary, t.TempDir(), nil)
	if err != nil {
		t.Fatalf("build the test relay: %v", err)
	}
	d.signin = relay
	return d
}

// form is what a rendered control submits: this render's page token, plus
// whatever the particular control carries.
func (d *signInDoor) form(t *testing.T, extra url.Values) url.Values {
	t.Helper()

	form := url.Values{}
	form.Set(fieldPageToken, mustMint(t, d.pageKey, testOperatorEmail, testTime))
	for k, vs := range extra {
		for _, v := range vs {
			form.Add(k, v)
		}
	}
	return form
}

func (d *signInDoor) post(t *testing.T, path string, form url.Values) *httptest.ResponseRecorder {
	t.Helper()

	r := httptest.NewRequest(http.MethodPost, path, strings.NewReader(form.Encode()))
	r.Header.Set(headerContentType, contentTypeForm)
	r.Header.Set(headerAccessAssertion, d.keys.mint(t, d.keys.claims()))
	r.Header.Set(headerSecFetchSite, secFetchSiteSameOrigin)

	w := httptest.NewRecorder()
	d.ServeHTTP(w, r)
	return w
}

// outcomeOf reads the code a 303 carried back to the operator.
func outcomeOf(t *testing.T, w *httptest.ResponseRecorder) string {
	t.Helper()

	if w.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want %d: %s", w.Code, http.StatusSeeOther, w.Body)
	}
	to, err := url.Parse(w.Header().Get(headerLocation))
	if err != nil {
		t.Fatalf("parse the redirect: %v", err)
	}
	return to.Query().Get(queryOutcome)
}

// TestSignInStartSummonsAWindow is the success path.
func TestSignInStartSummonsAWindow(t *testing.T) {
	t.Parallel()

	d := newSignInDoor(t)
	w := d.post(t, pathSignIn, d.form(t, url.Values{fieldConfirm: {confirmYes}}))

	if got := outcomeOf(t, w); got != string(outcomeSignInStarted) {
		t.Errorf("outcome = %q, want %q", got, outcomeSignInStarted)
	}

	var created bool
	for _, call := range d.tmux.Calls() {
		if call.Op == tmuxctl.OpNew {
			created = true
			if !strings.Contains(strings.Join(call.Argv, " "), loginrelay.WindowName) {
				t.Errorf("a window was created with the wrong name: %v", call.Argv)
			}
		}
	}
	if !created {
		t.Error("no window was created, so no sign-in was summoned")
	}
}

// TestSignInStartReturnsToItsOwnPanel is the one action on this door that does
// not return to the fleet.
//
// The flow's steps happen on the panel — the link to open, the box to paste
// into — and an operator on a phone bounced to a grid of cards after each step
// would have to navigate back into it three times while holding a code.
func TestSignInStartReturnsToItsOwnPanel(t *testing.T) {
	t.Parallel()

	d := newSignInDoor(t)
	w := d.post(t, pathSignIn, d.form(t, url.Values{fieldConfirm: {confirmYes}}))

	to, err := url.Parse(w.Header().Get(headerLocation))
	if err != nil {
		t.Fatalf("parse the redirect: %v", err)
	}
	if to.Path != pathSettingsPage {
		t.Errorf("redirected to %q, want the settings page", to.Path)
	}
	if got := to.Query().Get(querySection); got != sectionSignIn {
		t.Errorf("redirected to section %q, want %q", got, sectionSignIn)
	}
}

// TestSignInStartRequiresConfirm is this door's ordering rule at this route.
//
// A sign-in started by accident is a window holding a live challenge nobody
// meant to create, so the confirming step is read before anything happens.
func TestSignInStartRequiresConfirm(t *testing.T) {
	t.Parallel()

	for name, extra := range map[string]url.Values{
		"nothing at all":  {},
		"a checkbox's on": {fieldConfirm: {"on"}},
		"a bare true":     {fieldConfirm: {"true"}},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			d := newSignInDoor(t)
			w := d.post(t, pathSignIn, d.form(t, extra))

			if got := outcomeOf(t, w); got != string(outcomeSignInUnconfirmed) {
				t.Errorf("outcome = %q, want %q", got, outcomeSignInUnconfirmed)
			}
			for _, call := range d.tmux.Calls() {
				if call.Op == tmuxctl.OpNew {
					t.Errorf("an unconfirmed sign-in created a window: %v", call.Argv)
				}
			}
		})
	}
}

// TestSignInStartRefusesASecond covers the operator who pressed twice.
//
// Refused rather than restarted: a second summon abandons the challenge the
// first is waiting for, and they would have no way to know the code they were
// about to paste had just stopped working.
func TestSignInStartRefusesASecond(t *testing.T) {
	t.Parallel()

	d := newSignInDoor(t)
	confirmed := url.Values{fieldConfirm: {confirmYes}}

	if got := outcomeOf(t, d.post(t, pathSignIn, d.form(t, confirmed))); got != string(outcomeSignInStarted) {
		t.Fatalf("the first sign-in did not start: %q", got)
	}
	if got := outcomeOf(t, d.post(t, pathSignIn, d.form(t, confirmed))); got != string(outcomeSignInRunning) {
		t.Errorf("the second sign-in = %q, want %q", got, outcomeSignInRunning)
	}
}

// TestSignInCodeNeverLeavesTheDaemon is the claim this file exists for.
//
// **Must fail when** the code reaches the trail, a redirect, the response, or a
// tmux command line. It is the one value on this door that is a live credential
// in transit: docs/auth-and-sessions.md says the trail may record that a login
// relay happened and never what was relayed, and a tmux argv is in
// /proc/<pid>/cmdline for anything on the host to read.
func TestSignInCodeNeverLeavesTheDaemon(t *testing.T) {
	t.Parallel()

	const code = "SECRET-DEVICE-CODE-0123456789"

	d := newSignInDoor(t)
	d.post(t, pathSignIn, d.form(t, url.Values{fieldConfirm: {confirmYes}}))

	w := d.post(t, pathSignInCode, d.form(t, url.Values{fieldCode: {code}}))
	if got := outcomeOf(t, w); got != string(outcomeSignInCodeSent) {
		t.Fatalf("outcome = %q, want %q", got, outcomeSignInCodeSent)
	}

	// Not in the redirect an operator's browser is about to follow, and not in
	// anything written back to them.
	if loc := w.Header().Get(headerLocation); strings.Contains(loc, code) {
		t.Errorf("the code is in the redirect: %s", loc)
	}
	if body := w.Body.String(); strings.Contains(body, code) {
		t.Errorf("the code is in the response body")
	}

	// Not anywhere in the trail. The whole sink rather than named fields,
	// because a record shape that gained a field would otherwise be a leak this
	// test stopped looking for.
	if trail := d.sink.String(); strings.Contains(trail, code) {
		t.Errorf("the code is in the audit trail")
	}

	// Not in any command line, and really delivered on stdin.
	var onStdin bool
	for _, call := range d.tmux.Calls() {
		for _, arg := range call.Argv {
			if strings.Contains(arg, code) {
				t.Errorf("the code is in a tmux command line: %s %v", call.Op, call.Argv)
			}
		}
		if strings.Contains(string(call.Stdin), code) {
			onStdin = true
		}
	}
	if !onStdin {
		t.Error("the code never reached tmux on stdin, so it was not delivered by Paste")
	}
}

// TestSignInCodeRecordsThatItHappened is the other half: silence is not the goal.
//
// A relay that left no trace would mean this host's shared credential could be
// replaced with nothing to say who asked. The record says that a code was
// carried, which is exactly what docs/auth-and-sessions.md allows.
func TestSignInCodeRecordsThatItHappened(t *testing.T) {
	t.Parallel()

	d := newSignInDoor(t)
	d.post(t, pathSignIn, d.form(t, url.Values{fieldConfirm: {confirmYes}}))
	d.post(t, pathSignInCode, d.form(t, url.Values{fieldCode: {"a-code"}}))

	var found bool
	for _, rec := range d.records(t) {
		if rec["action"] == string(audit.ActionDashboardSignInCode) {
			found = true
		}
	}
	if !found {
		t.Errorf("no %s record; a credential was replaced with nothing to say who asked", audit.ActionDashboardSignInCode)
	}
}

// TestSignInCodeRefusals covers what a box can be submitted holding.
func TestSignInCodeRefusals(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		code string
		want outcome
	}{
		"nothing":             {"", outcomeSignInNoCode},
		"only whitespace":     {"   ", outcomeSignInNoCode},
		"an embedded newline": {"code\nand-more", outcomeSignInBadCode},
		"a control byte":      {"code\x1b[A", outcomeSignInBadCode},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			d := newSignInDoor(t)
			d.post(t, pathSignIn, d.form(t, url.Values{fieldConfirm: {confirmYes}}))

			before := len(d.tmux.Calls())
			w := d.post(t, pathSignInCode, d.form(t, url.Values{fieldCode: {tc.code}}))

			if got := outcomeOf(t, w); got != string(tc.want) {
				t.Errorf("outcome = %q, want %q", got, tc.want)
			}
			for _, call := range d.tmux.Calls()[before:] {
				if call.Op == tmuxctl.OpPaste {
					t.Errorf("a refused code was delivered anyway")
				}
			}
		})
	}
}

// TestSignInCodeWithNoSignInRunning covers the operator who pressed submit on a
// page whose sign-in has since been cancelled or finished.
func TestSignInCodeWithNoSignInRunning(t *testing.T) {
	t.Parallel()

	d := newSignInDoor(t)
	w := d.post(t, pathSignInCode, d.form(t, url.Values{fieldCode: {"a-code"}}))

	if got := outcomeOf(t, w); got != string(outcomeSignInNotRunning) {
		t.Errorf("outcome = %q, want %q", got, outcomeSignInNotRunning)
	}
}

// TestSignInCancelEndsTheWindow covers both callers: abandoning an attempt, and
// tidying up after one that worked.
func TestSignInCancelEndsTheWindow(t *testing.T) {
	t.Parallel()

	d := newSignInDoor(t)
	d.post(t, pathSignIn, d.form(t, url.Values{fieldConfirm: {confirmYes}}))

	w := d.post(t, pathSignInCancel, d.form(t, nil))
	if got := outcomeOf(t, w); got != string(outcomeSignInCancelled) {
		t.Errorf("outcome = %q, want %q", got, outcomeSignInCancelled)
	}

	var killed bool
	for _, call := range d.tmux.Calls() {
		if call.Op == tmuxctl.OpKill {
			killed = true
		}
	}
	if !killed {
		t.Error("cancelling left the window running, so a Node process and a live challenge stay on the host")
	}
}

// TestSignInRoutesNeverTouchASession is the safety property, asserted at the
// door as well as inside the relay.
//
// **Must fail when** any of these three reaches a name that is a session. The
// whole design rests on the relay being unable to damage the work it was called
// to rescue, and a route is where a mistake would arrive.
func TestSignInRoutesNeverTouchASession(t *testing.T) {
	t.Parallel()

	const sessionName = "crswd-abcdef0123456789abcdef0123456789ab"

	d := newSignInDoor(t)
	d.tmux.Seed(tmuxctl.SessionInfo{Name: sessionName, Managed: true})

	d.post(t, pathSignIn, d.form(t, url.Values{fieldConfirm: {confirmYes}}))
	d.post(t, pathSignInCode, d.form(t, url.Values{fieldCode: {"a-code"}}))
	d.post(t, pathSignInCancel, d.form(t, nil))

	for _, call := range d.tmux.Calls() {
		for _, arg := range call.Argv {
			if strings.Contains(arg, sessionName) {
				t.Errorf("%s reached a real session: %v", call.Op, call.Argv)
			}
		}
	}
}

// TestSignInRoutesRefuseWithoutTheGate is AR-005 at these three: a mutating
// route must refuse a request the browser did not say came from this page, and
// one carrying no page token.
func TestSignInRoutesRefuseWithoutTheGate(t *testing.T) {
	t.Parallel()

	for _, path := range []string{pathSignIn, pathSignInCode, pathSignInCancel} {
		t.Run(path, func(t *testing.T) {
			t.Parallel()

			d := newSignInDoor(t)

			// No page token: the form was not one this daemon rendered.
			noToken := url.Values{fieldConfirm: {confirmYes}, fieldCode: {"a-code"}}
			r := httptest.NewRequest(http.MethodPost, path, strings.NewReader(noToken.Encode()))
			r.Header.Set(headerContentType, contentTypeForm)
			r.Header.Set(headerAccessAssertion, d.keys.mint(t, d.keys.claims()))
			r.Header.Set(headerSecFetchSite, secFetchSiteSameOrigin)
			w := httptest.NewRecorder()
			d.ServeHTTP(w, r)

			if w.Code == http.StatusSeeOther {
				t.Errorf("a request with no page token was carried out (%d)", w.Code)
			}
			for _, call := range d.tmux.Calls() {
				if call.Op == tmuxctl.OpNew || call.Op == tmuxctl.OpPaste || call.Op == tmuxctl.OpKill {
					t.Errorf("an ungated request reached the host: %s %v", call.Op, call.Argv)
				}
			}
		})
	}
}
