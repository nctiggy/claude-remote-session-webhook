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

	// DisplayState is derived per render from the record (FR-019a–c). The stored
	// lifecycle field is deliberately not in this struct: a view that carried it
	// would be a second thing the card could render, and the one it must not.
	DisplayState session.DisplayState

	// StartCommand is the name of the command this session was running when the
	// page rendered (#38, #39). A name, never the command line: the card tells
	// an operator which of two identically-shaped sessions is the remote-control
	// one, and the daemon's own arguments are not a thing a page needs.
	//
	// Empty for a session started with the default and for an adopted one. The
	// card renders nothing at all rather than inventing a label — an adopted
	// session was not started by this daemon, and "default" would be a guess
	// dressed as a fact.
	StartCommand string

	// Mode is where this session is driven from, in the two words
	// session.Mode spells: local, or remote (FR-031, T021).
	//
	// It is not the same fact as StartCommand above, which is why both are here.
	// The name says what is running; the mode says whether claude.ai can reach
	// it. Reading the name and knowing which one means remote is configuration —
	// the settings page, not a card — so a card that showed only the name would
	// be asking an operator to hold this daemon's configuration in their head to
	// answer the one question the toggle exists for.
	//
	// Derived by the record's own method rather than carried on it, so this
	// field is the projection's copy of an answer computed at render (research
	// R5). There is no case in which it is empty: Mode returns one of its two
	// constants for every record, including an adopted one, which is why the
	// card renders it unconditionally where it states the absence of a name.
	Mode session.Mode

	// Age is already formatted — coarse, human-readable, computed server-side.
	// There is no ticking clock in the browser for it to drift from, and no
	// duration formatting inside a template, so the string is the projection's
	// to build.
	Age string

	// AbsoluteDeadline is how much longer this session has under the one bound
	// Principle VI gives it, formatted here for the reason Age is (T003).
	//
	// There were three fields here until milestone 15: an idle deadline, the
	// activity it was measured from, and this. The card needed all three because
	// a session could die of either bound and the operator had no way to check
	// which clock was watching. With the idle bound withdrawn there is one
	// question and one answer.
	//
	// It states that there is no lifetime limit for a session whose operator
	// switched the deadline off, rather than the instant AbsoluteDeadline returns
	// for one: that instant is a century out and means "never" rather than a
	// date, and a card rendering it would be telling an operator a fact about
	// their session that nothing in the daemon believes.
	AbsoluteDeadline string

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

	// Hidden is a page saying it composed this state without showing it.
	//
	// The fleet page renders both of its shapes — the summary with its grid, and
	// this — and hides whichever does not apply, so that a session appearing or
	// vanishing is the live half revealing markup the daemon already authored
	// rather than composing an empty state of its own (issue #51). A second
	// composition would be a second empty state, free to disagree with this one.
	//
	// The zero value shows the state, which is what every other call site wants:
	// the not-found page renders this and nothing hides it.
	Hidden bool
}

// createFormView is the create form's whole parameter list (T010): the token
// its submission has to present, and nothing else.
//
// One field, because a create names no session. The other three forms this
// milestone adds are the card's and take the card's own projection with them;
// this one has nothing to describe — what it makes is what it is for — so the
// only thing it needs is the evidence the gate demands.
//
// It is a type of its own rather than the bare string for the reason every other
// component's parameters are a struct: this template set is parsed with no
// function map, so a component's parameters are exactly the fields of what it is
// executed against, and a component that grows one grows it here.
type createFormView struct {
	// PageToken is the render's own token, the same value every card on the
	// same page carries (see sessionView.PageToken for why one render mints
	// one). Empty renders no form at all rather than a form with an empty
	// field: a control the gate is certain to refuse is worse than no control,
	// because an operator cannot tell the two apart until they use it.
	PageToken string

	// Roots is every directory this daemon will start a session under —
	// CRSW_ALLOWED_ROOTS as config.Load resolved it, absolute and with the
	// symlinks already followed.
	//
	// It renders as a hint under the working-directory field (T014), which
	// reverses milestone 3's deliberate omission. That omission was right about
	// the wrong disclosure: the uniform working-directory refusal stays one
	// message for every cause precisely so a caller cannot ask whether a path
	// exists, and nothing here changes that. Which roots are *permitted* is a
	// different fact — it is this authenticated operator's own configuration, it
	// is already on every card in the fleet as a working directory, and without
	// it the field is one an operator has to guess at. Naming the permitted set
	// is not confirming what is inside it.
	//
	// Every configured root renders, not the first: an operator whose second
	// root is missing from the hint would read it as a refusal they have no way
	// to explain.
	Roots []string

	// Suggestions is what the working-directory picker offers (T022): the paths
	// that render as `<option>` elements inside the field's `<datalist>`.
	//
	// It is not the Roots hint in another shape. That sentence says which
	// directories a session may run *under*; this list is the working
	// directories themselves, one level in, and a form can carry both because
	// they answer different questions — "what is permitted" and "what is here".
	//
	// **Nothing in this list is validated, and nothing in it needs to be.** The
	// datalist submits an ordinary string, so a chosen path and a typed one are
	// indistinguishable to the handler and both meet ResolveWorkDir — the same
	// allowlist check, the same uniform refusal, the same audit record (FR-042).
	// A path here grants nothing, and a path absent from it is still acceptable
	// typed (FR-040). Treating a suggestion as an authorisation is the one real
	// vulnerability this control could introduce, and it is closed by the list
	// reaching no decision at all.
	//
	// Empty renders no datalist and no `list` attribute — the field as it
	// shipped before the picker existed (FR-043). That is a state a running
	// daemon no longer reaches: it is filled by config.Config.SuggestedWorkDirs
	// (T006), which unions the approved roots in on every render, and a daemon
	// with no root refuses to start. The component keeps the branch all the same,
	// because FR-018a's rule is that a component states an absence rather than
	// rendering something shaped like a value.
	Suggestions []string

	// LifetimeCeilingRemoved is the one daemon fact this form branches on: the
	// operator has said, once in their configuration, that a session on this host
	// may live forever (CRSW_SESSION_LIFETIME_MAX = never). It renders the second
	// switch, the one that turns the absolute deadline off for a session (T005).
	//
	// The switch is gated on it because session.Manager.resolveLifetimes grants
	// that request on exactly this condition and refuses it on every other daemon.
	// A form offering it under a finite ceiling would be a control certain to be
	// turned away — the same defect as a card rendering actions with no page token
	// behind them, and worse than the card's, because this one would teach an
	// operator that a box they must tick every time is simply broken. An operator
	// who wants it removes their ceiling and the form starts offering it.
	//
	// It is read off the manager that will decide (LifetimeCeilingRemoved) rather
	// than off the sign of s.cfg.SessionLifetimeMax here, so what the page offers
	// and what the create grants cannot come to disagree.
	//
	// False is the shipped daemon, and it renders exactly the form that shipped
	// before this field existed — which is also what makes the zero value right
	// for every test and call site that says nothing about it.
	LifetimeCeilingRemoved bool

	// There is no StartCommands field either, and its absence is the requirement
	// rather than an omission (US1, FR-002). It carried the operator's configured
	// command *names* so the form could render a chooser of them, which is
	// choosing a command by name — the thing FR-026 said not to do, shipped
	// anyway because every assertion made for it was about a route or a record.
	// What replaces it is a two-state switch, and a mode needs no vocabulary from
	// the daemon's configuration to be asked for: which command each mode runs is
	// read from configuration at the point a session starts, and crosses to the
	// browser in neither direction.
	//
	// There is no Conversations field, and there is no projection behind one
	// (US5). The form asked an operator for an opaque conversation identifier and
	// offered the conversations of every *suggested directory* to fill it with,
	// which is not the thing an operator wants back: what they want is the
	// conversation *this session* was having, and FR-032 already refuses to
	// resolve that ambiguity by guessing.
	//
	// The offer was also the only thing in this struct that read the filesystem
	// on the render path for a fact no other component needed. What replaces the
	// question is not designed yet and is deliberately not invented here
	// (Principle II) — removing it is this milestone's, answering it is not.
}

// outcomeView is the banner the fleet renders for what an action just did (T014).
//
// Its copy is never caller text. The page reads a code out of the query string,
// bannerFor accepts only codes this package spells, and what is rendered is the
// sentence that code maps to — so the field below holds a string chosen by a
// handler and never one chosen by whoever wrote the link (FR-022). An
// unrecognised code produces no view at all, which is why the fleet holds a
// pointer.
type outcomeView struct {
	// Message is the sentence the operator reads. It is the whole of an ordinary
	// outcome and the body of the one alarming one.
	Message string

	// Heading is carried by the alarming outcome alone, and empty everywhere
	// else. It exists so that the one outcome an operator must not scan past has
	// a shape of its own rather than a shade of its own — colour is
	// reinforcement and never the signal (docs/design-system.md).
	Heading string

	// Alarm marks the outcome that must not read as one line alongside
	// "renamed": a teardown this daemon could not verify means a live
	// unsandboxed shell may have survived it (FR-023, AR-004).
	Alarm bool
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
