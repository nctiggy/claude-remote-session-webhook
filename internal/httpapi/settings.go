package httpapi

// settings.go is GET /settings — the account of how this daemon was configured,
// and the page that makes US1 legible: an operator who edited a file and saw
// nothing change reads here which layer actually supplied each value.
//
// This handler reads the Config the server was built from and nothing else.
// Every value on the page was resolved once, at startup, by the one shim in
// internal/config that decides precedence — so the page cannot infer a
// provenance the loader did not record, and cannot disagree with the daemon it
// describes (research R4). There is no re-read, no lookup of the environment,
// and no path taken from the request.
//
// # It reads here and writes on another route, deliberately
//
// The page is not read-only, and this comment claimed it was for a milestone
// after it stopped being true (#117). The forms it renders post to two other
// routes: POST /settings/edit, in settings_edit.go, which writes one key into
// the operator's configuration file, and POST /dashboard/update for the binary.
// This handler mints the token each of them carries. Both are registered
// through s.handleAction, which is layer 1 plus the two checks a write needs —
// the cross-site refusal, where an absent Sec-Fetch-Site refuses as well as a
// wrong one, and a page token bound to the identity layer 1 verified — and both
// run before the handler does, therefore before anything is written.
//
// `GET /settings` is nevertheless still the only verb registered on this path.
// The write being a separate pattern rather than a POST on this one is a
// decision, and it is worth stating on purpose:
//
//   - **The gate is chosen per registration, and the path is what a reader
//     checks it against.** A read is authorised by an identity alone; a write
//     must also establish that the operator asked for it, because the browser's
//     credential is an ambient cookie that a hostile page can cause to be sent.
//     A path on which every registered verb reads is one nobody has to consult
//     the mux to reason about.
//   - **A mutating verb here stays a path nothing claims**, which is what
//     TestNoMutatingVerbRegistered asserts for POST, PUT, PATCH and DELETE
//     alike. A write registered on this path by mistake is then a failing test
//     rather than a review comment somebody did not make.
//   - **The write is one of the dashboard's actions rather than a shape of its
//     own.** On its own path it reaches the mux through the same s.handleAction
//     call the destroy, rename, compact, mode and update writes use, so the
//     cross-site check, the page token and the trail's own name for the request
//     are inherited rather than restated — one door, not a second one that has
//     to be kept in step with the first.
//
// The absence of a route was the whole safeguard once, and stopped being it when
// the edit shipped (#106). What bounds the write now is that gate together with
// config.Editable, which answers no for every secret; the reasoning is at the
// top of settings_edit.go. Deleting a form does not bring the old sentence back.

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/nctiggy/claude-remote-session-webhook/internal/buildinfo"
	"github.com/nctiggy/claude-remote-session-webhook/internal/updater"

	"github.com/nctiggy/claude-remote-session-webhook/internal/access"
	"github.com/nctiggy/claude-remote-session-webhook/internal/config"
)

// patternSettings is the settings page's route, and the method is part of it for
// the reason it is part of every other pattern in this package: net/http matches
// both halves, so a path registered for GET is not silently reachable by POST.
//
// It carries no `{$}` because it names an exact path rather than the root — only
// `/` is a subtree pattern in net/http's router, so `GET /settings` matches that
// one path and nothing beneath it.
const patternSettings = "GET /settings"

// settingsView is the whole of what the settings page renders against.
//
// It is a page rather than a component, which is why it is here beside the
// handler and not in view.go — the same division fleetView and sessionPageView
// follow.
type settingsView struct {
	// Operator is the identity layer 1 verified, passed straight to the header
	// component as the same pointer OperatorFrom returned (FR-020, FR-036). It is
	// the only thing on this page that comes from the request at all, and it did
	// not come from anything the caller wrote.
	Operator *access.VerifiedOperator

	// Settings is the table's rows, projected from the Config at render time by
	// settingsOf: one per key config.go declares, in that order.
	Settings []settingRow

	// ConfigFile is the file those values were read from, and is empty when none
	// was (FR-018). The page says which above the table, because "why did my edit
	// do nothing?" is answered by the source column only once the operator knows
	// they and this daemon are talking about the same file — the commonest
	// version of that question is an edit made to a file the daemon never read.
	//
	// It comes from the Config, so it is the path this daemon *did* read at
	// startup rather than the one it would look for now: a value recovered from
	// the backup beside it names the backup (config.Config.FilePath).
	ConfigFile string

	// Shown is the section the operator is looking at, which is the one the menu
	// marks as current and the only one whose contents are rendered.
	Shown string

	// Sections is Settings grouped for reading rather than for storage.
	//
	// One flat table of every key is what the daemon knows; it is not how an
	// operator looks for something. The grouping is by what a setting is *about*
	// — where it listens, who may reach it, what it may touch — because that is
	// the question somebody arrives with.
	Sections []settingSection

	// Token is what an edit form carries, minted for this render and this
	// identity. Empty when none was minted, in which case no form is drawn — a
	// field that could not be submitted is worse than a value that cannot be
	// changed, because it looks like it could.
	Token string

	// Becoming is the version an accepted update is installing, and is empty on
	// every ordinary render.
	//
	// It is what turns this page into the one an operator watches while the
	// daemon restarts underneath them: the script polls for a daemon answering
	// with this version and reloads when one does.
	Becoming string

	// Restarting is whether this render is the answer to an accepted restart
	// rather than to an accepted update, and it decides one sentence.
	//
	// It is a second field rather than a comparison of Becoming against the
	// running version, because the two are equal exactly when a restart is what
	// happened *and* when an operator asked for the version they are already on —
	// and a page that inferred it would say a restart was under way while a
	// release was being installed over it.
	Restarting bool

	// Update is what this daemon is and what it could become. Nil when the
	// release feed could not be reached, which is deliberately not an error: this
	// page's first job is reporting local configuration, and that needs no
	// network at all.
	Update *updatePanel
}

// settingSection is a heading and the keys that belong under it.
type settingSection struct {
	Title    string
	Settings []settingRow

	// Door is what this page says about layer 1, and is the zero value on every
	// section but one.
	//
	// It sits on the section rather than on the view because it belongs *under*
	// a heading: the page shows one section at a time, and a fact rendered
	// outside them would either follow the operator into "Limits" or be lost
	// whenever they were reading anything else. Which section gets it is decided
	// in Go, by sectioned, so the template asks only whether there is one — a
	// template comparing a heading against a string would be a second answer to
	// "which section is this", free to disagree with settingSectionOf.
	//
	// It is not a settingRow, and could not be: a row is one key config.go
	// declares, with the layer that supplied its value beside it, and this is
	// neither. No environment variable holds it — it is what the daemon *did*
	// with the ones that do.
	Door doorFacts
}

// doorFacts is everything this page says about layer 1: the sentence naming
// which door is live, and whether it is one the operator can leave.
//
// The two travel together in one value because they are two readings of one
// thing, taken once (doorFactsOf). Composed separately they would be free to
// disagree, and the shape of that disagreement is the page saying "behind
// Cloudflare Access" above a Sign out button, or saying "behind the dashboard
// password" with no way out of it.
type doorFacts struct {
	// Sentence is which layer 1 a browser actually meets, said in one sentence.
	// Empty renders nothing, which is what a section that is not the door's gets.
	Sentence string

	// SignOut is whether this daemon has a sign-out route to offer, which is the
	// same question newServer asked when it decided whether to register one. A
	// control the daemon would answer with a 404 is worse than no control: it
	// looks like a way out.
	SignOut bool
}

// doorFactsOf reads the door this server was **built with**, once, and answers
// both of the questions this page asks about it.
//
// Never the Config — see doorSentence for the whole of why, and passwordDoorOf
// for why the sign-out half is the same predicate the registration uses rather
// than a second one that agrees with it today.
func doorFactsOf(door layer1) doorFacts {
	_, password := passwordDoorOf(door)
	return doorFacts{Sentence: doorSentence(door), SignOut: password}
}

// updatePanel is the Updates section: what is running, what is available, and
// what each of them says about itself.
//
// Available is empty when the feed could not be reached or when this build is
// already the newest — two different facts, distinguished by Reachable so the
// page can say "you are current" rather than "we could not tell", which are not
// the same reassurance.
type updatePanel struct {
	// Checked is whether this render asked the release feed at all.
	//
	// It did not, unless the operator pressed Check. Composing a page that
	// reaches somebody else's API every time it is opened makes the settings
	// page as slow and as fallible as GitHub, on a page whose first job is
	// reporting local configuration — and it asks on behalf of an operator who
	// may only have come to read a root.
	Checked bool

	Installed      string
	InstalledNotes string
	Available      string
	AvailableNotes string
	Reachable      bool
	Token          string

	// Unit is what became of this host's systemd unit, which belongs in this
	// section because it is the other file an update carries (M15/T004).
	//
	// It is updater.UnitFacts rather than a projection of this page's own,
	// because the journal says the same thing at startup (M15/T005) and one file
	// described two ways is two accounts an operator has no way to reconcile.
	// The sentences, the waiting file and the diff command are all
	// internal/updater's — see facts.go, which argues that at length.
	//
	// The zero value renders nothing at all, which is a daemon with no update
	// path wired behind it — every server this package's own tests build. A
	// shipping daemon always has one (liveSelfUpdate), and selfUpdate.wired
	// refuses the update route rather than letting a dropped wiring look like a
	// host with nothing to carry.
	Unit updater.UnitFacts
}

// settingRow is one configuration key as the page states it.
//
// There is a Value and there is no raw value beside it, which is the whole
// arrangement: what reaches the template is what a browser is allowed to see, so
// there is no field for a render to reach for by mistake and no second
// projection where a "helpful" masked form could be added without touching
// settingsOf.
type settingRow struct {
	// Key is the file spelling — the environment variable minus CRSW_,
	// lower-cased — because that is the spelling an operator wrote in their own
	// file and the spelling config.IsSecret takes.
	Key string

	// Editable is whether this page may write the key, which is
	// config.Editable's answer and not a second one. A row that is not editable
	// renders its value as text, which is what every row did before editing
	// existed.
	Editable bool

	// Boolean is whether the key holds a true/false setting, which is
	// config.IsBool's answer and not a second one. It decides the control the row
	// offers — a checkbox rather than a text field — and it is deliberately the
	// same predicate settings_edit.go asks when it reads an absent value as
	// `false`. Two lists would disagree the day one was edited, and the shape of
	// that disagreement is a key rendered as a box the handler will not read as a
	// box: the operator unticks it and the setting is cleared instead of turned
	// off.
	Boolean bool

	// On is whether that box is ticked, and it is read off Value rather than off
	// the Config a second time. The page's account of a setting and the state of
	// the control it offers for it are then one answer: a switch that disagreed
	// with the value column would be this page telling an operator two things
	// about the same key.
	On bool

	// Value is the effective value, or — for a secret — one of the two words
	// below and nothing else.
	Value string

	// Source is which layer supplied it, in config.Source's four words. It is
	// read from the record the precedence shim wrote as it decided, never worked
	// out here by comparing the value against the default: a file and an
	// environment holding the same bytes are indistinguishable by comparison, and
	// that is the case an operator is on this page to ask about.
	Source string
}

// The value column's entire vocabulary for a secret (FR-017,
// contracts/settings-page.md). Not a length, not a prefix, not a suffix, not a
// hash: a masked value is still a disclosure, and the four characters of a
// credential a page is willing to print are four an attacker no longer has to
// guess.
const (
	secretPresent = "present"
	secretAbsent  = "absent"
)

// The whole vocabulary for which door is live (M12/T006), and the most
// consequential sentence on this page.
//
// Which layer 1 a browser meets decides who may execute code on this host, and
// until this task it was visible nowhere: an operator could read `access_enabled`
// and `dashboard_password` as configured and still not know which of them the
// daemon acted on, or whether it had built either.
//
// Four sentences here and a fifth in bypass_dev.go, all of them constants, none
// of them naming a value. In particular the password door's says that the door
// is the password and never a word about what the password is — the presence of
// one is already the `present` its own row renders, and this sentence adds
// nothing to it.
const (
	doorSentenceAccess = "This dashboard is behind Cloudflare Access."

	doorSentencePassword = "This dashboard is behind the dashboard password."

	doorSentenceClosed = "This dashboard has no door configured and admits nobody."

	doorSentenceUnrecognised = "This dashboard's door is not one this page can name."
)

// doorSentence is which layer 1 a browser actually meets, said in one sentence.
//
// It reads the door this server was **built with** and nothing else — never the
// Config. A page that named the door from configuration would describe the
// daemon its operator meant to start rather than the one they are reading, which
// is the single thing this page exists not to do; it is the same
// intent-versus-evidence distinction mayBindOffLoopback draws and the sign-in
// route's registration draws, and here the evidence is the whole answer.
//
// Two of the doors are one type, so that type is asked what it is
// (assertionDoor.door). Inferring it from the Config was tried and does not
// work: config.WithAccessBypassActive lifts the requirement to set the three
// Access values and not the ability to, so a developer running the bypass
// against their ordinary file has all three — and a page reading them would
// report Cloudflare Access on the one build whose layer 1 admits everybody
// without checking anything. That is the one lie this sentence must not tell.
//
// The closed door's sentence can be read by nobody, and it is written anyway.
// closedDoor admits no browser, so a daemon holding one serves this page to
// no-one at all; what an operator meets there is the uniform 401, which is the
// answer to the question, said by the door. It stays because this projection has
// to be total — a door with no sentence of its own would fall through to
// another's, and on a switch the one it falls to is whichever is written last.
//
// The unrecognised sentence is the same discipline one level down: a door built
// without saying which it is has nothing true to say about itself, so this says
// that rather than picking the likeliest.
func doorSentence(door layer1) string {
	switch d := door.(type) {
	case *passwordDoor:
		return doorSentencePassword
	case closedDoor:
		return doorSentenceClosed
	case assertionDoor:
		if d.door == "" {
			return doorSentenceUnrecognised
		}
		return d.door
	default:
		return doorSentenceUnrecognised
	}
}

// settingsOf projects a Config into the rows the settings page renders.
//
// config.IsSecret is the gate, and it is deliberately the same predicate the
// 0600 refusal in internal/config/file.go asks (T001). A second list of secret
// keys here would be invisible while every test passed: this page would
// confidently print the value that check thought too sensitive to leave
// group-readable, and the disagreement would surface as a credential in a
// browser rather than as a failure.
//
// It walks config.Vars() rather than a list of its own, so the page cannot
// invent a setting or forget one, and the order is the order config.go declares
// them.
//
// One row per key, including a key with no loader behind it. There is no such
// key today — CRSW_DESTROY_ON_SHUTDOWN was the example when this page was
// written and has been loaded and acted on since — but the rule is what matters
// rather than the example: a key the shim never looked up is absent from
// cfg.Sources, so it reports its zero value from `default`, which is what that
// daemon actually does. Dropping such a row would hide the gap from the one page
// an operator would find it on.
func settingsOf(cfg *config.Config) []settingRow {
	rows := make([]settingRow, 0, len(config.Vars()))
	for _, name := range config.Vars() {
		key := config.KeyForVar(name)
		boolean := config.IsBool(key)
		value := settingText(cfg, name, key)
		rows = append(rows, settingRow{
			Key:      key,
			Editable: config.Editable(key),
			Boolean:  boolean,
			// Guarded by the predicate rather than by the spelling alone: a
			// non-boolean key whose value happens to read `true` is a text field,
			// and nothing about it is ticked.
			On:    boolean && value == boolOn,
			Value: value,
			// The shim's own record, read by the name it keyed it under. A key
			// nothing ever looked up is absent from the map and reads
			// SourceDefault, which is the zero value precisely so that "nothing
			// supplied it" needs no special case here.
			Source: cfg.Sources[name].String(),
		})
	}
	return rows
}

// settingText is the value cell: two words for a secret, the effective value for
// everything else.
//
// The secret gate comes first and is the only branch that can suppress a value,
// which is what keeps config.IsSecret the single classifier T001 made it. A
// second rule about what is too sensitive to print — a start command redacted
// here because it looks like one, say — would be a second answer to "which keys
// are secret", and the two would disagree the day one of them was edited.
func settingText(cfg *config.Config, name, key string) string {
	if config.IsSecret(key) {
		configured, known := secretConfigured(cfg, name)
		return presence(configured && known)
	}
	value, _ := settingValue(cfg, name)
	return value
}

// The separators the value column spells a list with.
//
// A root list is prose here rather than the ":" the variable takes, because
// these paths are the *resolved* ones — absolute, cleaned, every symlink already
// followed — so the cell is not a string an operator could paste back into their
// file whatever it is separated by. What SC-004 needs it to be is legible: an
// operator whose working directory was refused is reading down this cell asking
// whether theirs is under one of them.
//
// The start-command set keeps its own grammar exactly, for the opposite reason:
// that cell *is* what the operator wrote, and a space introduced for looks would
// make the page disagree with the file it is describing.
const (
	rootsSeparator         = ", "
	startCommandsSeparator = ","

	// The suggestion list keeps its own grammar for the start-command set's
	// reason and not the root list's: these paths are the operator's own,
	// unresolved and unread, so the cell is the line they wrote and stays one
	// they could paste back.
	workdirSuggestionsSeparator = ","

	// sessionEnvironmentSeparator matches the one config parses the list with.
	// A page that joined on something else would show the operator a value they
	// could not paste back into their configuration file.
	sessionEnvironmentSeparator = ","
)

// settingValue is the effective value of one non-secret setting, and whether
// this page knows how to state it.
//
// It reads the Config's own fields rather than re-reading the environment or the
// file, so the page cannot state a value the daemon is not actually running on
// — the two would drift the moment an operator edited the file under a daemon
// that had already started, which is the exact confusion this page exists to end.
//
// **The start commands are spelled out, command lines and all.** They are not
// secret by config.IsSecret and they are the operator's own configuration, read
// back to the identity layer 1 verified — the same identity that may start a
// session running them. Config.String names them without spelling them, but that
// is a rule about *log lines*, where the alternative is a command line in a
// journal anyone with the unit can read. Applying it here would be a second
// redaction rule outside IsSecret, and it would break the one thing this cell is
// for: an operator who cannot see what `rc` runs cannot tell whether the switch
// they are about to throw does what they think.
//
// The second return exists for the variable that is not here yet, and it is the
// same guard secretConfigured carries: a variable added to config.Vars() and not
// to this switch renders an empty cell, which claims nothing rather than
// claiming `false` or `0` about a setting this page has never heard of.
// TestEverySettingRendersAValue keeps that branch unreachable. It also fails
// closed for the two secrets, which do not reach here at all — narrowing
// IsSecret drops a value from this page rather than printing one.
func settingValue(cfg *config.Config, name string) (value string, known bool) {
	switch name {
	case config.EnvAllowedRoots:
		paths := make([]string, 0, len(cfg.Roots))
		for _, root := range cfg.Roots {
			paths = append(paths, root.Path)
		}
		return strings.Join(paths, rootsSeparator), true
	case config.EnvDiscoverRoots:
		return strconv.FormatBool(cfg.DiscoverRoots), true
	case config.EnvWorkdirSuggestions:
		return strings.Join(cfg.WorkdirSuggestions, workdirSuggestionsSeparator), true
	case config.EnvSessionEnvironment:
		// Names, never values — which is what makes this safe to render at all.
		// The variables it names hold whatever the daemon's environment holds,
		// and this page states configuration rather than reading the process's
		// environment back to the browser.
		return strings.Join(cfg.SessionEnvironment, sessionEnvironmentSeparator), true
	case config.EnvListen:
		return cfg.Listen, true
	case config.EnvMaxSessions:
		return strconv.Itoa(cfg.MaxSessions), true
	case config.EnvDestroyOnShutdown:
		return strconv.FormatBool(cfg.DestroyOnShutdown), true
	case config.EnvSessionLifetime:
		return cfg.SessionLifetime.String(), true
	case config.EnvSessionLifetimeMax:
		// The one setting on this page whose value may not be a duration. It is
		// carried as a negative and shown as the word the operator would write,
		// because this row is the only place a daemon says out loud that the
		// bound which is never renewed has no ceiling on it — and "-1h0m0s"
		// there reads as a misconfiguration rather than as the decision it is.
		if cfg.SessionLifetimeMax < 0 {
			return config.NeverLifetime, true
		}
		return cfg.SessionLifetimeMax.String(), true
	case config.EnvIdleTimeout:
		return cfg.IdleTimeout.String(), true
	case config.EnvIdleTimeoutMax:
		return cfg.IdleTimeoutMax.String(), true
	case config.EnvCreateRatePerMin:
		return strconv.Itoa(cfg.CreateRatePerMin), true
	case config.EnvMaxBodyBytes:
		return strconv.FormatInt(cfg.MaxBodyBytes, 10), true
	case config.EnvAccessEnabled:
		return strconv.FormatBool(cfg.AccessEnabled), true
	case config.EnvAccessTeamDomain:
		return cfg.AccessTeamDomain, true
	case config.EnvAccessAUD:
		return cfg.AccessAUD, true
	case config.EnvMaxStreams:
		return strconv.Itoa(cfg.MaxStreams), true
	case config.EnvPaneBound:
		return strconv.Itoa(cfg.PaneBound), true
	case config.EnvStartCommand:
		// The command bound to the default name, which is what this variable
		// sets and what a create that asks for nothing in particular runs.
		command, _ := cfg.StartCommands.Command(config.DefaultStartCommandName)
		return command, true
	case config.EnvStartCommands:
		return startCommandSet(cfg.StartCommands), true
	case config.EnvRemoteControlCommand:
		return cfg.RemoteControlCommand, true
	default:
		return "", false
	}
}

// startCommandSet spells the named set the way the variable and the file spell
// it, so the cell and the operator's own line are the same string.
//
// The names come back sorted from Names(), which is the order the create form
// already offers them in — a set that rendered in map order would reshuffle
// itself on every load of a page whose whole purpose is being compared against a
// file that does not move.
func startCommandSet(commands config.StartCommands) string {
	names := commands.Names()
	entries := make([]string, 0, len(names))
	for _, name := range names {
		command, ok := commands.Command(name)
		if !ok {
			continue
		}
		entries = append(entries, name+"="+command)
	}
	return strings.Join(entries, startCommandsSeparator)
}

// presence is the only function that turns a fact about a secret into text, so
// there is one place that could ever be made to say more than two words.
func presence(configured bool) string {
	if configured {
		return secretPresent
	}
	return secretAbsent
}

// secretConfigured reports whether a secret setting has a value on this daemon,
// and whether this page can tell.
//
// It reads the Config's own field rather than the provenance map, because
// provenance answers a different question — which layer supplied a value — and a
// secret with no default makes the two look identical right up until one of them
// is wrong.
//
// The second return exists for the key that is not here yet. config.IsSecret is
// the classifier, so a third secret added there is kept out of the value column
// by settingText whether or not this switch has heard of it; what it would
// otherwise lose is the ability to say anything true about it. An unknown one
// reads as absent rather than as present because that is the answer an operator
// investigates: reading
// "absent" for a configured secret sends somebody to look, and reading "present"
// for one that is missing does not. TestEverySecretKeyReportsItsPresence keeps
// the branch unreachable.
func secretConfigured(cfg *config.Config, name string) (configured, known bool) {
	switch name {
	case config.EnvSharedSecret:
		return len(cfg.SharedSecret) > 0, true
	case config.EnvAccessAllowedEmails:
		return len(cfg.AccessAllowedEmails) > 0, true
	case config.EnvDashboardPassword:
		// Whether there is a password door, which is a fact about this daemon an
		// operator has to be able to read. The length behind it is not: the cell
		// says one of two words either way.
		return len(cfg.DashboardPassword) > 0, true
	default:
		return false, false
	}
}

// settings serves GET /settings (FR-016 … FR-020, contracts/settings-page.md).
//
// It mints a page token, which this request has no use for and the next one
// does: a token authorises a write, and the forms this page renders are received
// by two routes that will not act without one. It is minted here for the reason
// the fleet and the session view mint theirs — a page that carries forms carries
// the token they submit — and this comment said the opposite for a milestone
// after the forms arrived (#117).
//
// A token that could not be minted is the empty string rather than a failed
// render, because everything above the forms is still true and still worth
// reading; the template draws no form it could not authorise, which is the same
// discipline as a card with no actions rather than actions certain to be refused.
func (s *Server) settings(w http.ResponseWriter, r *http.Request) {
	operator, ok := OperatorFrom(r.Context())
	if !ok {
		// Fail closed on the path that should not happen, exactly as the fleet
		// does: newServer registers this route behind authenticateBrowser, so a
		// false here is a wiring mistake and deserves a reason of its own in the
		// trail rather than a page rendered for nobody.
		AuditFrom(r.Context()).Deny(errDashboardNoOperator.Error())
		s.refuseBrowser(w)
		return
	}

	// No SetSessionID: this page is about the daemon and not about one session.
	// The record the middleware emits carries settings.view and the identity that
	// asked, which is the whole of what an operator counting who read the
	// configuration needs.
	// The Config the server was built from, read here and nowhere else: every
	// value on this page was resolved once, at startup, so the page cannot
	// disagree with the daemon it describes.
	rows := settingsOf(s.cfg)
	// s.browser and never the Config: which door a browser meets is the fact this
	// page was least able to state, and the only honest source for it is the
	// layer 1 this server was actually built with. See doorSentence.
	sections := sectioned(rows, doorFactsOf(s.browser))
	editToken, _ := s.mintPageToken(r, operator)
	s.renderPage(w, r, http.StatusOK, "settings", settingsView{
		Operator:   operator,
		Settings:   rows,
		Sections:   sections,
		Shown:      shownSection(r.URL.Query().Get(querySection), sections),
		Token:      editToken,
		ConfigFile: s.cfg.FilePath,
		Update:     s.updatePanelFor(r, operator),
	})
}

// sectionWhoMayReachIt is the section the door sentence belongs under, named
// once because three places now have to agree on it: the classifier below, the
// order the menu shows, and sectioned, which decides where the sentence lands. A
// literal in all three is three chances for the sentence to attach to a heading
// that no longer exists, and a section that quietly stopped carrying it looks
// exactly like a daemon nobody asked about its door.
const sectionWhoMayReachIt = "Who may reach it"

// settingSectionOf says which section a key belongs under.
//
// A map from key to heading rather than a heading per key, so a key added to
// config.go lands somewhere without this file being edited: anything unclaimed
// falls to "Other", which is visible on the page and is the prompt to classify
// it. The alternative — a key silently vanishing because no section claimed it —
// is the failure this arrangement is shaped to avoid.
func settingSectionOf(key string) string {
	switch key {
	case "listen":
		return "Where it listens"
	case "access_enabled", "access_team_domain", "access_aud", "access_allowed_emails",
		"dashboard_password", "shared_secret":
		return sectionWhoMayReachIt
	case "allowed_roots", "workdir_suggestions", "discover_roots":
		return "What it may touch"
	case "start_commands", "remote_start_commands":
		return "What it runs"
	case "session_lifetime", "session_lifetime_max", "idle_timeout", "idle_timeout_max",
		"max_sessions", "max_streams", "create_rate_per_min", "max_body_bytes", "pane_bound",
		"destroy_on_shutdown":
		return "Limits"
	default:
		return "Other"
	}
}

// sectionOrder is the order the page shows them in, which is the order an
// operator is likely to be looking: the reachable surface first, the containment
// boundary next, and the numbers last.
var sectionOrder = []string{
	"Where it listens",
	sectionWhoMayReachIt,
	"What it may touch",
	"What it runs",
	"Limits",
	"Other",
}

// sectioned groups rows without dropping any, and hands the door sentence to
// the one section it is an answer to.
//
// The count is asserted by TestEverySettingAppearsInASection, because a grouping
// that loses a key is a settings page that is quietly incomplete — worse than no
// grouping, since the operator has no way to tell which one it is.
//
// The door goes under "Who may reach it" rather than above every section,
// because that heading *is* the question it answers and the keys it is the
// outcome of are the rows beneath it. Repeating it on all six would be the same
// sentence six times; putting it outside them would be a fact the operator
// carries into "Limits". It arrives as a parameter rather than being worked out
// here for the reason every value on this page arrives resolved: which door was
// built is not something a projection of the Config can see.
//
// The sign-out control rides on that same value and lands on that same section
// (M12/T007), because it is the same fact with a verb on it: the one heading that
// answers "who may reach it" is the one that should offer "and stop being one of
// them". Nothing else on the page has to know it exists.
func sectioned(rows []settingRow, door doorFacts) []settingSection {
	byTitle := make(map[string][]settingRow, len(sectionOrder))
	for _, row := range rows {
		title := settingSectionOf(row.Key)
		byTitle[title] = append(byTitle[title], row)
	}

	out := make([]settingSection, 0, len(sectionOrder))
	for _, title := range sectionOrder {
		held := byTitle[title]
		if len(held) == 0 {
			continue
		}
		section := settingSection{Title: title, Settings: held}
		if title == sectionWhoMayReachIt {
			section.Door = door
		}
		out = append(out, section)
	}
	return out
}

// releaseFeedTimeout is how long composing this page will wait on the network.
//
// Short on purpose. This page's first job is reporting local configuration,
// which needs no network at all, so a slow or unreachable feed must cost the
// Updates section and nothing else. An operator asking "why was my working
// directory refused?" should never wait on GitHub to find out.
const releaseFeedTimeout = 3 * time.Second

// releaseFeedTTL is how long an answer is reused.
//
// Without it, every render of this page is a request to someone else's API, and
// a page an operator leaves open would poll it forever. Releases are cut on
// merge, so minutes-old is current enough for a question about whether to
// update.
const releaseFeedTTL = 5 * time.Minute

// errNoReleaseFeed is a server built without one. It is not a failure: the
// Updates section still reports the installed version, which is local knowledge.
var errNoReleaseFeed = errors.New("this server was built with no release feed")

// updatePanelFor composes the Updates section.
//
// It returns nil rather than an error when the feed cannot be reached. That is
// the whole arrangement: the section is missing and the rest of the page is
// exactly as it was, because a settings page that failed to render because
// GitHub was slow would be reporting on this daemon by asking somebody else.
// queryCheck is how the Check button asks for the one thing that costs a
// network request.
const queryCheck = "check"

func (s *Server) updatePanelFor(r *http.Request, operator *access.VerifiedOperator) *updatePanel {
	token, minted := s.mintPageToken(r, operator)
	if !minted {
		// No token means no form may act, so the section would be a description
		// of a button that could not be pressed.
		return nil
	}

	panel := &updatePanel{Installed: buildinfo.Version, Token: token}

	// What became of the other file an update carries, read off this disk and
	// composed on every render — before the Check below, because it costs no
	// network at all (M15/T004).
	//
	// It reads the files rather than anything an update recorded, which is
	// updater.UnitReport's whole design: the crswd.service.new an operator
	// diffs *is* the claim that there is a newer unit than theirs, so it is what
	// decides what this page says. A second account, persisted by the update,
	// would be free to go stale the moment somebody took the offer.
	//
	// Nil is a daemon with no update path behind it, which is every server this
	// package's tests build and no daemon New returns. The zero UnitFacts renders
	// nothing rather than a sentence about a host nothing looked at.
	if s.updates.unit != nil {
		panel.Unit = updater.DescribeUnit(s.updates.unit.Report())
	}

	// Asked for, never assumed. Without this the page is only as fast and as
	// available as somebody else's API, on behalf of an operator who may have
	// come to read a root.
	if r.URL.Query().Get(queryCheck) == "" {
		return panel
	}
	panel.Checked = true

	ctx, cancel := context.WithTimeout(r.Context(), releaseFeedTimeout)
	defer cancel()

	latest, notes, err := s.latestRelease(ctx)
	if err != nil {
		// Reachable stays false, which is a different sentence on the page from
		// "you are current" — the page must not offer reassurance it did not
		// earn.
		return panel
	}
	panel.Reachable = true
	if latest != buildinfo.Version {
		panel.Available = latest
		panel.AvailableNotes = notes
	}
	return panel
}

// releaseCache is the one answer this server keeps about somebody else's API.
type releaseCache struct {
	mu      sync.Mutex
	version string
	notes   string
	fetched time.Time
}

// latestRelease answers what is published, from cache when it is fresh.
//
// The lock is held across the fetch rather than only around the fields. Two
// operators opening this page at once would otherwise make two identical
// requests to an API that rate-limits by address, and the second learns nothing
// the first did not.
func (s *Server) latestRelease(ctx context.Context) (version, notes string, err error) {
	s.releases.mu.Lock()
	defer s.releases.mu.Unlock()

	if time.Since(s.releases.fetched) < releaseFeedTTL && s.releases.version != "" {
		return s.releases.version, s.releases.notes, nil
	}

	if s.releaseFeed == nil {
		// No feed wired: the Updates section reports what is installed and says
		// it could not look further. Tests get this by construction rather than
		// by reaching the internet — a page composer that made a live request to
		// somebody else's API in a unit test would be slow, flaky, and wrong
		// offline, and it would be asserting GitHub's behaviour rather than this
		// daemon's.
		return "", "", errNoReleaseFeed
	}

	rel, err := s.releaseFeed(ctx)
	if err != nil {
		return "", "", err
	}

	s.releases.version = rel.Version
	s.releases.notes = rel.Notes
	s.releases.fetched = time.Now()
	return rel.Version, rel.Notes, nil
}

// querySection is how the menu names which section to show.
const querySection = "section"

// sectionUpdates is the one section that is not a group of configuration keys,
// so it is named rather than derived.
const sectionUpdates = "Updates"

// shownSection resolves the menu's choice to a section that exists.
//
// Anything unrecognised falls back to the first, rather than refusing: a link
// somebody kept from an older version, or a section that stopped existing when a
// key moved, should land an operator on a page rather than on an error. Nothing
// here reaches a filesystem or a command — it selects among headings this
// process composed a moment ago, and a value matching none of them selects the
// default.
func shownSection(asked string, sections []settingSection) string {
	if asked == sectionUpdates {
		return sectionUpdates
	}
	for _, section := range sections {
		if section.Title == asked {
			return asked
		}
	}
	// The first configuration section, not Updates.
	//
	// Somebody opening this page is usually answering a question about how the
	// daemon is configured — why a working directory was refused, what it
	// listens on. Updates is the section with news, which is exactly why it
	// should not be the one that greets everybody: a page that opens on an offer
	// is a page that interrupts.
	if len(sections) > 0 {
		return sections[0].Title
	}
	return sectionUpdates
}
