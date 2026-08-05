package httpapi

// actions.go is the browser door's mutating half — the four routes
// contracts/actions.md fixes, and the answers they share. The gate that admits a
// request to any of them is in browser.go, next to the layer-1 door it composes
// with; what lives here is what happens *after* a request has been admitted.
//
// The first of those answers is the one below, because it is the answer three of
// the four give more often than they give any other: an action against a session
// that is not this operator's to act on.

import (
	"fmt"
	"net/http"
	"strconv"
)

// bodyActionNotFound is the uniform not-found, byte for byte from
// contracts/actions.md.
//
// Three causes end here — an identifier no session ever had, one another
// operator owns, and one whose session is already gone — and none of them is
// distinguishable from outside (FR-017, SC-009). The difference between them is
// what enumeration is made of: an answer that separated "never existed" from
// "not yours" would let anyone who can reach this door count the sessions on
// this host and learn the identifiers of the ones they may not touch. Which of
// the three it really was is on the record the caller's handler emits, where the
// operator can read it.
//
// It is deliberately not bodyActionRefused. A refusal says the request was not
// accepted; this says the thing it named is not here, and an operator whose
// session was reaped between rendering a card and clicking it is owed the second
// rather than the first. The two are told apart by status as well — 403 against
// 404 — and both are uniform *within* themselves, which is where a caller
// probing one of them lives.
//
// Like every other body this door writes it references no stylesheet, no script
// and no external origin, so it renders the same under the CSP as without one.
var bodyActionNotFound = []byte(`<!doctype html><title>not found</title><p>No such session.</p>`)

// notFoundAction answers an action route whose {id} resolved to nothing this
// operator may act on (FR-017).
//
// It takes no reason argument, for the reason refuseBrowser and refuseAction
// take none: there is nothing a caller could pass that would be allowed to
// change a byte of what is written, so the parameter would only be an
// invitation — and it is the parameter, not the intent, that a later hand
// reaches for when one of the four routes wants to be helpful about which of the
// three causes applied. The record is where the cause goes, written by the
// handler that did the lookup and knows which sentinel came back.
//
// It writes the response and nothing else. The audit reason is the caller's,
// deliberately: resolveReason already turns a resolver error into the trail's
// existing vocabulary, and a not-found that emitted a record of its own would be
// a second record for one request (FR-041) — the fleet page and the session page
// audit the same failure this way today.
//
// The length is written rather than left to net/http, so that byte-identical is
// a property of this function rather than a property of how the response
// happened to be buffered. Everything else the response carries — nosniff among
// it — was written by setBrowserSecurityHeaders before layer 1 ran, so this
// leaves with the identical header set to a served page (FR-026).
func (s *Server) notFoundAction(w http.ResponseWriter) {
	w.Header().Set(headerContentType, contentTypeHTML)
	w.Header().Set(headerContentLength, strconv.Itoa(len(bodyActionNotFound)))
	w.WriteHeader(http.StatusNotFound)
	if _, err := w.Write(bodyActionNotFound); err != nil {
		s.report(fmt.Errorf("write the browser door's action not-found: %w", err))
	}
}
