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

	// Actions is the action row docs/components.md documents on this component:
	// destroy, compact, rename. This milestone passes none (FR-024a). The
	// dashboard is read-only (FR-022) and a browser holds no shared secret, so
	// the row would be non-functional as well as out of scope — the parameter
	// exists so milestone 3 fills a row that is already there rather than
	// restoring markup this milestone deleted.
	Actions []actionView
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
