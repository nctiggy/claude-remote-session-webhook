package httpapi

// view.go is the parameter list of every canonical component in
// docs/components.md that takes more than a single value. The header takes the
// access.VerifiedOperator layer 1 already built, and the status pill takes the
// session.DisplayState the record already derives, so neither has a type here: a
// projection whose only job is to copy one field is a second place for that
// field to be wrong.
//
// Parameters are struct fields rather than the dict(...) call sites
// docs/components.md spells, because this template set is parsed with no
// function map, deliberately — a template calling an unknown function is one of
// the trees parseTemplates refuses, and TestParseTemplatesRefuses pins it. The
// consequence worth knowing before editing a partial: a component's parameters
// are exactly the fields below, and adding a field is the only way a component
// gains one.
//
// Nothing here reaches a store. The values are projected by the page handlers
// from the owner-scoped reads (FR-017, FR-037), which is the only way a session
// this viewer does not own can fail to be here.

import "github.com/nctiggy/claude-remote-session-webhook/internal/session"

// sessionView is one session as the card renders it — the projection
// data-model.md names, built per render rather than stored, so it cannot drift
// from the record it came from.
type sessionView struct {
	// ID is the identifier and the card's link target. It carries no
	// credential, which is why a URL may hold one, and it is rendered as well
	// as linked because a session with no name has no other handle: without it
	// every adopted card would read identically.
	ID string

	// Name is caller-supplied and may be empty — which is what every session
	// adopted after a restart looks like, not an edge case (FR-018a). Empty
	// renders as a statement that the value is unknown, never as a placeholder.
	Name string

	// WorkDir is the resolved, allowlist-checked directory, rendered on exactly
	// the same terms as Name and absent under the same circumstances.
	WorkDir string

	// DisplayState is derived per render from the reaper's own idle deadline
	// (FR-019a–c). The stored lifecycle field is deliberately not in this
	// struct: a view that carried it would be a second thing the card could
	// render, and the one it must not.
	DisplayState session.DisplayState

	// Age is already formatted — coarse, human-readable, computed server-side.
	// There is no ticking clock in the browser for it to drift from, and no
	// duration formatting inside a template, so the string is the projection's
	// to build.
	Age string

	// PageToken is the value every action form on this card submits: minted
	// for this render and bound to the identity layer 1 verified for it
	// (FR-002b, FR-007, contracts/actions.md). It is the card's parameter rather
	// than the page's because the per-session forms are the card's own, and one
	// value is handed to every card a page draws — a fleet of ten renders one
	// token, not ten, because a page is rendered for one identity at one instant
	// and a second mint would be a second expiry that nothing is truer for.
	//
	// It reaches the browser in a hidden field and in nothing else: never a URL,
	// never a cookie, never a data- attribute, never a record. A token in a link
	// is a token in a referrer header, a browser history, and a proxy log — which
	// is also why the gate reads it out of PostForm and never out of Form.
	//
	// Empty renders no field at all (partials/page-token.html), which is what
	// makes the zero value safe: a card built without a token offers nothing that
	// looks like one. That is FR-018a's discipline — state the absence, never
	// render something that reads like a value — applied to a credential.
	//
	// It is also what decides whether the card renders its action row at all. The
	// row was milestone 2's absent parameter (FR-024a), kept so that this
	// milestone would fill a container rather than restore one; what fills it is
	// a form, and a form with no token is a control the gate is certain to
	// refuse. So there is one value here rather than two, and no arrangement in
	// which a card offers an action it could not authorise.
	PageToken string
}

// paneView is the pane viewer's parameters (docs/components.md): one session's
// screen as the single-session page renders it, before anything live is
// attached to it.
type paneView struct {
	// ID is the session the screen belongs to, and it names the element rather
	// than the session for the caller's benefit: the live half finds the pane by
	// it (T026). It carries no credential, which is why a URL may hold one and
	// why the card is allowed to link here at all.
	ID string

	// Text is the whole screen as of this request, captured through the manager
	// so that terminal escapes were stripped where output leaves
	// internal/session (FR-029) rather than by a second stripper here. It
	// reaches the page as a text node and never as markup — everything a Claude
	// session prints arrives in this field, which makes it the project's one XSS
	// surface, closed by html/template rather than by sanitising (FR-028,
	// Constitution VII).
	//
	// It is the current screen and not a transcript (FR-031a). What replaces it
	// on each update is the same whole screen, which is why there is one field
	// here and no accumulator.
	Text string

	// Unread says the host could not be asked for the screen.
	//
	// It is a state of its own rather than an empty Text, because "the session
	// has printed nothing" and "nobody could read what it printed" are different
	// facts and a blank pane would assert the first. It is FR-018a's discipline
	// — state the absence, never render something that reads like a value —
	// applied to the screen instead of to a name.
	Unread bool
}

// emptyView is what the fleet page renders instead of a grid when the viewer
// owns no sessions (FR-021).
type emptyView struct {
	// Title and Body are the component's copy, supplied at the call site the
	// way docs/components.md documents. Body is prose, and the design system
	// sets prose in sans because a human wrote it.
	Title string
	Body  string

	// Action is docs/components.md's "start a session", absent here for the
	// reason the card's row is absent (FR-024a). A pointer rather than a slice
	// because the component documents one.
	Action *actionView
}

// actionView is one entry in an action row, and it is deliberately empty.
//
// What an action *is* — variant, label, target, confirmation — is
// docs/components.md's Button, which FR-024a forbids building in this milestone,
// and inventing its shape now would be inventing milestone 3's requirements
// (Principle II). What FR-024a does ask for is a parameter that can be absent
// and a row that appears when it is not, and this is exactly that much and no
// more.
type actionView struct{}
