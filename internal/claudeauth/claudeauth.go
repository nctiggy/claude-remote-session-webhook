// Package claudeauth recognises Claude Code's own sign-in prompt in a pane.
//
// # Why this is one package with one entry point
//
// A session is `claude`, and when its stored login is gone that binary stops
// being a shell and starts being a form. The daemon has no readiness signal from
// it — there is no API, no exit code, no file to stat — so the only way to know
// is to read the screen. That is screen-scraping, it is the most fragile thing
// in this project, and docs/auth-and-sessions.md requires it live behind a
// single DetectPrompt covered by golden files captured from real output: when
// Claude Code changes its wording this must be a one-file fix, not a hunt.
//
// # Why phrases are matched against a flattened pane
//
// A pane is a fixed-width grid, not a paragraph. Every string Claude Code draws
// wraps at the pane's width, and the width is the operator's browser column
// count rather than anything this daemon chooses. Captured at 120 columns, the
// subscription option already arrives split:
//
//	❯ 1. Claude account with subscription · Pro, Max,
//	Team, or Enterprise
//
// so a phrase containing "Pro, Max, Team" matches at one width and silently
// stops matching at another — which is the worst failure available here, because
// it looks exactly like a healthy session. Matching against a whitespace-
// flattened copy makes a phrase mean what it reads as, at any width.
//
// # What this package deliberately does not do
//
// It does not relay anything. Taking the operator's code and sending it into a
// pane is a separate, larger change with its own security surface — a browser
// door route that carries a live credential — and shipping detection first is
// what stops a logged-out session rendering as `running` in the meantime.
package claudeauth

import "strings"

// Kind is which of Claude Code's two sign-in screens a pane is showing.
//
// They are distinguished because they need different things from the operator
// and, later, different things from a relay: the first is a menu that must be
// answered before any URL exists, the second is the device-code screen that
// carries one.
type Kind string

const (
	// KindSelectMethod is the menu offering a subscription account, a Console
	// account and a third-party platform. It appears on a fresh start with no
	// stored login, and after the operator types /login.
	KindSelectMethod Kind = "select-method"

	// KindDeviceCode is the screen carrying the sign-in URL and an input
	// waiting for the code pasted back from a browser.
	KindDeviceCode Kind = "device-code"
)

// Prompt is what a pane showing a sign-in screen is asking for.
type Prompt struct {
	// Kind is which screen it is.
	Kind Kind

	// URL is the sign-in link, present only on KindDeviceCode.
	//
	// It carries a one-shot PKCE challenge and the state that pairs with it, so
	// it is a credential in everything but name: it must never be logged, put
	// in an audit record, stored, or included in an error. String below exists
	// so that the ordinary ways a struct leaks — %v, %s, a wrapped error —
	// cannot be the way this one does.
	URL string
}

// String describes a Prompt without disclosing the URL.
//
// A field that must never be logged, on a struct that will one day be in an
// error's message or a debug line, needs the redaction to be a property of the
// type rather than a rule people remember. fmt reaches for String on both %v
// and %s, so the default rendering is the safe one.
func (p Prompt) String() string {
	if p.URL == "" {
		return "claudeauth.Prompt{" + string(p.Kind) + "}"
	}
	return "claudeauth.Prompt{" + string(p.Kind) + ", url redacted}"
}

// selectMethodPhrases are the anchors for the login menu.
//
// Every phrase here is short and free of the punctuation Claude Code renders as
// multi-byte glyphs, because both are what survive a version bump. The option
// lines themselves ("Claude account with subscription · Pro, Max, Team, or
// Enterprise") are deliberately not used: they are the part of this screen most
// likely to be reworded, and one already changed between the capture in the
// design notes and the shipped 2.1.263, which added a third option.
var selectMethodPhrases = []string{
	"Select login method:",

	// A second anchor, for the reason deviceCodePhrases has two: one phrase is
	// one coincidence away from a card that says a working session is unusable.
	// This one is on the screen at every width — it is the first option, and the
	// menu has no state in which it is absent.
	"Claude account with subscription",
}

// deviceCodePhrases are the anchors for the device-code screen.
//
// Both must be present. Either alone is a phrase Claude Code could plausibly
// print elsewhere; together they are this screen. The URL line is not an anchor
// — it carries a live challenge and its host is the thing most likely to move.
var deviceCodePhrases = []string{
	"Browser didn't open?",
	"Paste code here if prompted",
}

// urlPrefix is where the sign-in link starts.
//
// Matched as a prefix rather than parsed, because the query string is the part
// that changes every time and none of it is this daemon's business.
const urlPrefix = "https://claude.com/"

// DetectPrompt reports whether a pane is showing Claude Code's sign-in screen.
//
// It is the only entry point on purpose. Everything that knows what Claude
// Code's login looks like is in this file, so a wording change is one edit and
// one golden file.
func DetectPrompt(pane string) (*Prompt, bool) {
	if pane == "" {
		return nil, false
	}

	flat := flatten(pane)

	if containsAll(flat, deviceCodePhrases) {
		// Ordered before the menu check because the two screens share no
		// anchor today and the order is therefore not load-bearing — stated so
		// that a future shared phrase is a decision rather than an accident.
		return &Prompt{Kind: KindDeviceCode, URL: signInURL(pane)}, true
	}
	if containsAll(flat, selectMethodPhrases) {
		return &Prompt{Kind: KindSelectMethod}, true
	}
	return nil, false
}

// quotes are the characters that turn an anchor into a mention of an anchor.
//
// # Why a detector has to care
//
// Every pane this runs against is a Claude Code session, and those sessions read
// this repository — including this file, which declares all four anchor phrases
// as string literals. Before this guard, `cat internal/claudeauth/claudeauth.go`
// detected as KindDeviceCode: measured, not supposed. So did a diff of it, a
// pager holding it, and a session answering a question about it.
//
// That failure is the inverted twin of the one this package fixes, and it is the
// worse direction. A logged-out session reading `running` is a stale card. A
// working session reading `needs-auth` sends an operator to re-authenticate a
// host whose credential is fine — and it is silent, because the mislabelled
// session goes on working and never contradicts the card.
//
// # Why quoting, and not something stronger
//
// The obvious guard is to anchor on whole lines, and it does not survive the
// wrap this pane arrives with: the phrases are rendered at whatever width the
// operator's terminal happens to be, which is the reason flatten() exists at all.
// Quoting survives flattening, because a quote is part of the text and travels
// with it wherever the line breaks.
//
// It is not a proof. An unquoted sentence that also carries a second anchor
// still matches, and that is the honest limit of screen-scraping something this
// package does not own. What it removes is the whole realistic population —
// source, diffs, JSON, and prose that quotes a phrase to talk about it.
const quotes = "\"'`"

// containsUnquoted reports whether phrase appears at least once without a quote
// character immediately on either side of it.
//
// At least once, rather than never quoted: a real sign-in screen may be on a
// pane that ALSO holds a quoted mention scrolled above it, and the screen is what
// matters. Only every occurrence being quoted means the phrase is being talked
// about rather than shown.
func containsUnquoted(flat, phrase string) bool {
	for at := 0; ; {
		i := strings.Index(flat[at:], phrase)
		if i < 0 {
			return false
		}
		start := at + i
		end := start + len(phrase)

		beforeQuoted := start > 0 && strings.ContainsRune(quotes, rune(flat[start-1]))
		afterQuoted := end < len(flat) && strings.ContainsRune(quotes, rune(flat[end]))
		if !beforeQuoted && !afterQuoted {
			return true
		}
		at = start + 1
	}
}

// flatten collapses every run of whitespace to a single space.
//
// This is what makes a phrase mean what it reads as regardless of where the
// pane happened to wrap it. strings.Fields splits on all Unicode whitespace,
// newlines included, and drops the leading and trailing padding tmux returns.
func flatten(pane string) string {
	return strings.Join(strings.Fields(pane), " ")
}

// containsAll reports whether every phrase is present and shown rather than
// quoted. See containsUnquoted for why the second half is not optional.
func containsAll(flat string, phrases []string) bool {
	for _, phrase := range phrases {
		if !containsUnquoted(flat, phrase) {
			return false
		}
	}
	return true
}

// signInURL rebuilds the sign-in link from the lines a pane split it across.
//
// The URL is longer than any terminal is wide, so it always arrives wrapped —
// captured at 120 columns it spans four lines. tmux hard-wraps at the pane edge
// and adds no marker, so the only thing separating a continuation from the next
// sentence is that Claude Code indents its prose by one space and starts the URL
// at column zero. Continuation lines therefore run until the first line that is
// blank or indented.
//
// Returns "" when no URL is on screen, which is the device-code screen caught
// in the moment before the link is drawn.
func signInURL(pane string) string {
	lines := strings.Split(pane, "\n")
	for i, line := range lines {
		if !strings.HasPrefix(line, urlPrefix) {
			continue
		}
		var url strings.Builder
		url.WriteString(strings.TrimRight(line, " "))
		for _, next := range lines[i+1:] {
			trimmed := strings.TrimRight(next, " ")
			if trimmed == "" || strings.HasPrefix(trimmed, " ") {
				break
			}
			url.WriteString(trimmed)
		}
		return url.String()
	}
	return ""
}
