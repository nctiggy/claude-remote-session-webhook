package httpapi

// settings.go is GET /settings — the read-only account of how this daemon was
// configured, and the page that makes US1 legible: an operator who edited a file
// and saw nothing change reads here which layer actually supplied each value.
//
// It reads the Config the server was built from and nothing else. Every value on
// it was resolved once, at startup, by the one shim in internal/config that
// decides precedence — so the page cannot infer a provenance the loader did not
// record, and cannot disagree with the daemon it describes (research R4). There
// is no re-read, no lookup of the environment, and no path taken from the
// request.
//
// **No mutating verb is registered on this path, and that absence is the
// safeguard rather than a handler that refuses.** Writing the operator's
// configuration file from a browser is the highest-consequence surface in the
// product (spec, Out of Scope); a route that does not exist cannot be exploited,
// mis-gated, or reached by a future refactor that forgets which door it is on.
//
// That sentence is about *this* path and it survives issue #103 unchanged. What
// changed is that the page now carries a form posting to a route of its own —
// POST /dashboard/update, which milestone 6 built and nothing rendered a control
// for. The configuration is still read-only; the daemon's own version is the one
// thing on this page an operator may act on, and the action is on the update
// route where it always was.

import (
	"net/http"
	"slices"
	"strconv"
	"strings"

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

	// Sections is the page's configuration, grouped (issue #103). One flat table
	// of every key was the whole page until this change, and it made the page
	// answer only the question an operator already knew to ask: reading down
	// twenty-one rows to find the four that decide who is admitted is a search,
	// not a report.
	//
	// The rows inside a section are still settingsOf's, in config.go's order, so
	// nothing about *what* the page says has moved — a section is a boundary
	// drawn around rows that were already adjacent in meaning.
	Sections []settingsSection

	// Updates is the one section that is not a projection of the Config: what
	// this daemon is running, what it could be running, and the control that
	// moves it (issue #103). It is last on the page for the reason the create
	// form is last on the fleet — a page is read for what it reports before it is
	// used for what it can do.
	Updates updatesView

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
}

// settingsSection is one group of configuration keys, with the sentence that
// says what the group is for (issue #103).
//
// The Slug is what binds the table to the heading above it. A `<table>` inside a
// `<section>` has no accessible name from the heading beside it, so the page
// points one at the other by id — and the id is composed here rather than in the
// template, because a template that built one out of a title would build a
// different one the day a title gained a word a browser will not take in an id.
type settingsSection struct {
	Title    string
	Slug     string
	Blurb    string
	Settings []settingRow
}

// The sections, in the order the page renders them, and the sentence each one
// carries.
//
// The order is not config.go's and deliberately so: what config.go declares is a
// loading order, and what an operator reads down is a subject. Identity is first
// because it is the answer to "who can reach this daemon at all", and the
// listener and its limits are last because they are the thing an operator is
// least often on this page about.
//
// Sans, and a sentence rather than a label: docs/design-system.md gives prose to
// a human and mono to the machine, and every one of these is a human explaining
// what the rows under it decide.
var settingsSections = []settingsSection{
	{
		Title: "Identity",
		Slug:  "settings-identity",
		Blurb: "Who this daemon admits, on each of its two doors: the browser through Cloudflare Access, and the companion skill by signature.",
	},
	{
		Title: "Working directories",
		Slug:  "settings-work-dirs",
		Blurb: "Where a session may run. A working directory outside these roots is refused before anything starts.",
	},
	{
		Title: "Sessions",
		Slug:  "settings-sessions",
		Blurb: "How many unsandboxed shells this host will carry at once, and how long each one may live before the reaper ends it.",
	},
	{
		Title: "Commands",
		Slug:  "settings-commands",
		Blurb: "What a session actually runs, and which of the configured commands means remote control.",
	},
	{
		Title: "Listener and limits",
		Slug:  "settings-listener",
		Blurb: "The loopback address this daemon binds, and the bounds it answers a request under.",
	},
}

// sectionForKey says which section a configuration key belongs to, keyed by the
// file spelling the page renders.
//
// A map rather than a field on each row, because the grouping is a property of
// the page and not of the configuration: internal/config decides what a setting
// means and this decides where an operator reads about it, and folding the two
// together would put a rendering decision inside the loader.
//
// A key that is not here renders in no section at all rather than in a bucket
// called "other" — and TestEverySettingIsInASection is what turns that into a
// failure of the suite instead of a setting quietly missing from the one page
// that is meant to hold every one of them.
var sectionForKey = map[string]string{
	config.KeyForVar(config.EnvAccessTeamDomain):    "settings-identity",
	config.KeyForVar(config.EnvAccessAUD):           "settings-identity",
	config.KeyForVar(config.EnvAccessAllowedEmails): "settings-identity",
	config.KeyForVar(config.EnvSharedSecret):        "settings-identity",

	config.KeyForVar(config.EnvAllowedRoots):       "settings-work-dirs",
	config.KeyForVar(config.EnvDiscoverRoots):      "settings-work-dirs",
	config.KeyForVar(config.EnvWorkdirSuggestions): "settings-work-dirs",

	config.KeyForVar(config.EnvMaxSessions):        "settings-sessions",
	config.KeyForVar(config.EnvSessionLifetime):    "settings-sessions",
	config.KeyForVar(config.EnvSessionLifetimeMax): "settings-sessions",
	config.KeyForVar(config.EnvIdleTimeout):        "settings-sessions",
	config.KeyForVar(config.EnvIdleTimeoutMax):     "settings-sessions",
	config.KeyForVar(config.EnvDestroyOnShutdown):  "settings-sessions",

	config.KeyForVar(config.EnvStartCommand):         "settings-commands",
	config.KeyForVar(config.EnvStartCommands):        "settings-commands",
	config.KeyForVar(config.EnvRemoteControlCommand): "settings-commands",

	config.KeyForVar(config.EnvListen):           "settings-listener",
	config.KeyForVar(config.EnvMaxBodyBytes):     "settings-listener",
	config.KeyForVar(config.EnvCreateRatePerMin): "settings-listener",
	config.KeyForVar(config.EnvMaxStreams):       "settings-listener",
	config.KeyForVar(config.EnvPaneBound):        "settings-listener",
}

// sectionsOf groups the rows settingsOf projected into the sections the page
// renders.
//
// It reads settingsOf rather than config.Vars() directly, so there is still one
// projection of a Config into rows and this adds only a grouping over it. A
// second walk would be a second answer to "what does this page say about this
// setting", free to disagree with the first about a value or a source.
//
// A section that ends up empty is dropped. That is not a case any shipped
// configuration reaches — every key above names a section that exists — but a
// heading and a sentence over no rows is an affordance-shaped nothing, which is
// the discipline FR-018a states for a value and applies just as well to a table.
func sectionsOf(cfg *config.Config) []settingsSection {
	grouped := make(map[string][]settingRow, len(settingsSections))
	for _, row := range settingsOf(cfg) {
		slug, placed := sectionForKey[row.Key]
		if !placed {
			continue
		}
		grouped[slug] = append(grouped[slug], row)
	}

	sections := make([]settingsSection, 0, len(settingsSections))
	for _, section := range settingsSections {
		rows := grouped[section.Slug]
		if len(rows) == 0 {
			continue
		}
		section.Settings = rows
		sections = append(sections, section)
	}
	return sections
}

// sectionSlugs is every slug the page can render a section under, which is what
// makes "this key names a section that exists" a checkable claim.
func sectionSlugs() []string {
	slugs := make([]string, 0, len(settingsSections))
	for _, section := range settingsSections {
		slugs = append(slugs, section.Slug)
	}
	return slugs
}

// hasSection reports whether slug names one of the sections above.
func hasSection(slug string) bool { return slices.Contains(sectionSlugs(), slug) }

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
		rows = append(rows, settingRow{
			Key:   key,
			Value: settingText(cfg, name, key),
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
	case config.EnvListen:
		return cfg.Listen, true
	case config.EnvMaxSessions:
		return strconv.Itoa(cfg.MaxSessions), true
	case config.EnvDestroyOnShutdown:
		return strconv.FormatBool(cfg.DestroyOnShutdown), true
	case config.EnvSessionLifetime:
		return cfg.SessionLifetime.String(), true
	case config.EnvSessionLifetimeMax:
		return cfg.SessionLifetimeMax.String(), true
	case config.EnvIdleTimeout:
		return cfg.IdleTimeout.String(), true
	case config.EnvIdleTimeoutMax:
		return cfg.IdleTimeoutMax.String(), true
	case config.EnvCreateRatePerMin:
		return strconv.Itoa(cfg.CreateRatePerMin), true
	case config.EnvMaxBodyBytes:
		return strconv.FormatInt(cfg.MaxBodyBytes, 10), true
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
	default:
		return false, false
	}
}

// settings serves GET /settings (FR-016 … FR-020, contracts/settings-page.md).
//
// It mints a page token, which it did not before issue #103. The rule that kept
// one off this page has not changed — a token is minted where something can spend
// it — and what changed is that something can: the Updates section carries the
// control for POST /dashboard/update, and the gate in front of that route refuses
// a submission that arrives without one. A page rendering a form it cannot
// authorise would be the dead control docs/components.md forbids, which is the
// same defect as the missing control this section exists to fix.
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

	token, minted := s.pageTokenFor(w, r, operator)
	if !minted {
		return
	}

	// No SetSessionID: this page is about the daemon and not about one session.
	// The record the middleware emits carries settings.view and the identity that
	// asked, which is the whole of what an operator counting who read the
	// configuration needs.
	// The Config the server was built from, read here and nowhere else: every
	// value on this page was resolved once, at startup, so the page cannot
	// disagree with the daemon it describes.
	//
	// The Updates section is the one part of this page that is not the Config,
	// and it is composed last and separately: everything above it is local and
	// cannot fail, which is what lets the release feed be unreachable without
	// costing an operator the configuration they came here to read.
	s.renderPage(w, r, http.StatusOK, "settings", settingsView{
		Operator:   operator,
		Sections:   sectionsOf(s.cfg),
		Updates:    s.updatesFor(r.Context(), token),
		ConfigFile: s.cfg.FilePath,
	})
}
