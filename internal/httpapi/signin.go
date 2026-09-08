package httpapi

// signin.go is the browser's half of the sign-in relay: three actions that
// summon Claude Code's own login in a window of its own, carry a code the
// operator types into it, and end it.
//
// # Why these carry no {id}
//
// A sign-in names no session. There is one Claude credential store on this host
// and every session shares it, so what these repair belongs to the daemon rather
// than to anything an operator owns — which is why there is no ownership check
// behind them and no uniform not-found for them to give. They are the restart's
// shape, not the rename's.
//
// # What may never reach a record, a URL, or a page
//
// docs/auth-and-sessions.md is binding here and it is stricter than anywhere
// else on this door:
//
//   - The code is a live credential. It is read from PostForm, handed straight to
//     internal/loginrelay, and never logged, never put in an audit record, never
//     echoed into an outcome, and never rendered back into the form. Every
//     refusal below is a sentinel authored in this file, so no record and no
//     redirect can carry a byte the operator typed (FR-042).
//   - The sign-in URL is a one-shot PKCE challenge. It reaches exactly one place
//     — the panel on the settings page, as a link — and it is never in a query
//     string, never in the trail, and never on the fleet grid. claudeauth.Prompt
//     redacts it under %v so the ordinary ways a value leaks cannot be the way
//     this one does.
//   - Nothing is ever submitted that the operator did not type. These routes
//     summon a screen and carry what they are handed; they answer nothing on the
//     operator's behalf.

import (
	"errors"
	"net/http"

	"github.com/nctiggy/claude-remote-session-webhook/internal/loginrelay"
)

// The three patterns. Each is a POST with no {id}, for the reason above.
const (
	patternDashboardSignIn       = "POST /dashboard/signin"
	patternDashboardSignInCode   = "POST /dashboard/signin/code"
	patternDashboardSignInCancel = "POST /dashboard/signin/cancel"
)

// fieldCode is the name of the one field the code arrives in.
//
// Spelled once, so the name the form writes and the name the handler reads
// cannot drift into a route that silently receives nothing and reports an empty
// code for one the operator really typed.
const fieldCode = "code"

// The refusals these routes author. Sentinels written here, so no record can
// carry a byte the caller chose — and on this door that rule is not a
// formality, because one of the fields is a credential.
var (
	errSignInUnconfirmed = errors.New("a browser sign-in arrived without the confirming step")
	errSignInUnwired     = errors.New("the sign-in route was reached on a daemon with no relay behind it")
	errSignInRefused     = errors.New("the sign-in could not be started on this host")
	errSignInAlready     = errors.New("a sign-in was already in progress")

	// errSignInNoCode and errSignInBadCode are kept apart because only one of
	// them is the operator's to fix by typing again. Neither names any part of
	// what was submitted.
	errSignInNoCode  = errors.New("a code was submitted with nothing in it")
	errSignInBadCode = errors.New("a code was submitted carrying characters a code does not")

	errSignInNotRunning = errors.New("a code was submitted with no sign-in waiting for one")
	errSignInCancel     = errors.New("the sign-in window could not be ended")
)

// signInFromBrowser is POST /dashboard/signin.
//
// Everything that authorises it has already run: handleAction wrapped this in
// the gate, so an identity is verified, the browser has said the request came
// from this page, and the form carried a token minted for that identity.
func (s *Server) signInFromBrowser(w http.ResponseWriter, r *http.Request) {
	if _, ok := OperatorFrom(r.Context()); !ok {
		AuditFrom(r.Context()).Deny(errDashboardNoOperator.Error())
		s.refuseBrowser(w)
		return
	}

	// The confirming step first, ahead of anything else, which is this door's
	// ordering rule throughout. A sign-in started by accident is a window holding
	// a live challenge that nobody meant to create.
	if r.PostForm.Get(fieldConfirm) != confirmYes {
		AuditFrom(r.Context()).Deny(errSignInUnconfirmed.Error())
		s.redirectSignIn(w, r, outcomeSignInUnconfirmed)
		return
	}

	if s.signin == nil {
		AuditFrom(r.Context()).Deny(errSignInUnwired.Error())
		s.redirectSignIn(w, r, outcomeSignInRefused)
		return
	}

	switch err := s.signin.Start(r.Context()); {
	case errors.Is(err, loginrelay.ErrAlreadyRunning):
		// Not an error and not a success: the operator asked for something that
		// is already true, and the reason it is refused rather than restarted is
		// that restarting abandons a challenge they may be part-way through
		// answering on their phone.
		AuditFrom(r.Context()).Deny(errSignInAlready.Error())
		s.redirectSignIn(w, r, outcomeSignInRunning)
	case err != nil:
		// The host's own account of the failure goes to the report channel where
		// an operator is already reading, never into the record or the redirect.
		s.report(err)
		AuditFrom(r.Context()).Deny(errSignInRefused.Error())
		s.redirectSignIn(w, r, outcomeSignInRefused)
	default:
		s.redirectSignIn(w, r, outcomeSignInStarted)
	}
}

// signInCodeFromBrowser is POST /dashboard/signin/code.
//
// The one route in this daemon that carries a credential the operator typed.
// What it does with it is hand it to internal/loginrelay and forget it: the
// value is never assigned to anything that outlives this call, never wrapped in
// an error, and never chosen as an outcome.
func (s *Server) signInCodeFromBrowser(w http.ResponseWriter, r *http.Request) {
	if _, ok := OperatorFrom(r.Context()); !ok {
		AuditFrom(r.Context()).Deny(errDashboardNoOperator.Error())
		s.refuseBrowser(w)
		return
	}

	if s.signin == nil {
		AuditFrom(r.Context()).Deny(errSignInUnwired.Error())
		s.redirectSignIn(w, r, outcomeSignInRefused)
		return
	}

	// No confirming step. Typing a code into a box and pressing the one button
	// beside it IS the confirmation — a second one would be a dialog between an
	// operator and the thing they just typed, and docs/auth-and-sessions.md's
	// rule is that nothing is auto-submitted, not that everything is asked twice.
	switch err := s.signin.Deliver(r.Context(), r.PostForm.Get(fieldCode)); {
	case errors.Is(err, loginrelay.ErrEmptyCode):
		AuditFrom(r.Context()).Deny(errSignInNoCode.Error())
		s.redirectSignIn(w, r, outcomeSignInNoCode)
	case errors.Is(err, loginrelay.ErrUnusableCode):
		AuditFrom(r.Context()).Deny(errSignInBadCode.Error())
		s.redirectSignIn(w, r, outcomeSignInBadCode)
	case errors.Is(err, loginrelay.ErrNotRunning):
		AuditFrom(r.Context()).Deny(errSignInNotRunning.Error())
		s.redirectSignIn(w, r, outcomeSignInNotRunning)
	case err != nil:
		// Reported, not recorded. loginrelay's delivery error is already written
		// to carry none of the code, and this keeps the trail's account of it to
		// a sentinel either way.
		s.report(err)
		AuditFrom(r.Context()).Deny(errSignInRefused.Error())
		s.redirectSignIn(w, r, outcomeSignInRefused)
	default:
		s.redirectSignIn(w, r, outcomeSignInCodeSent)
	}
}

// signInCancelFromBrowser is POST /dashboard/signin/cancel.
//
// It is how an operator abandons an attempt, and it is also the tidy-up after a
// successful one — a window left sitting on the host is a Node process and a
// spent challenge with nothing watching them.
func (s *Server) signInCancelFromBrowser(w http.ResponseWriter, r *http.Request) {
	if _, ok := OperatorFrom(r.Context()); !ok {
		AuditFrom(r.Context()).Deny(errDashboardNoOperator.Error())
		s.refuseBrowser(w)
		return
	}

	if s.signin == nil {
		AuditFrom(r.Context()).Deny(errSignInUnwired.Error())
		s.redirectSignIn(w, r, outcomeSignInRefused)
		return
	}

	// No confirming step here either, and for the opposite reason to the code's:
	// this is the undo. An operator who pressed it by accident has lost a
	// challenge they can replace by pressing start again, and putting a
	// confirmation in front of the way out of a flow is how people get stuck in
	// one.
	if err := s.signin.Stop(r.Context()); err != nil {
		s.report(err)
		AuditFrom(r.Context()).Deny(errSignInCancel.Error())
		s.redirectSignIn(w, r, outcomeSignInRefused)
		return
	}
	s.redirectSignIn(w, r, outcomeSignInCancelled)
}

// redirectSignIn is redirectOutcome pointed at the panel rather than the fleet.
//
// Every other action returns to the fleet, because the fleet is the page that
// already reflects what the action did. A sign-in is the exception: the thing
// that reflects it is the panel itself — the link to open, the box to paste
// into, whether this host is signed in yet — and a flow that bounced the
// operator to a grid of cards after every step would be one they had to
// navigate back into three times, on a phone, while holding a code.
func (s *Server) redirectSignIn(w http.ResponseWriter, r *http.Request, code outcome) {
	s.redirectSection(w, r, sectionSignIn, code)
}
