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

import (
	"net/http"
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
// One row per key, including a key with no loader behind it. CRSW_DESTROY_ON_SHUTDOWN
// is one today — it has a constant, a field and a consumer, and nothing reads it
// (internal/config's own varWithNoLoader pins the gap) — so this page reports it
// as `false` from `default`, which is what that daemon actually does. Dropping
// the row would hide the defect from the one page an operator would find it on.
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
// It mints no page token, and that is a decision rather than an omission. A page
// token authorises a write, this page offers none, and the one on a rendered page
// is a value worth not minting where nothing can spend it — the fleet and the
// session view each carry one because each carries forms.
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
	s.renderPage(w, r, http.StatusOK, "settings", settingsView{
		Operator:   operator,
		Settings:   settingsOf(s.cfg),
		ConfigFile: s.cfg.FilePath,
	})
}
