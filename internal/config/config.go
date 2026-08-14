// Package config loads the daemon's whole configuration once, before anything
// binds or spawns: from the process environment, and behind it from the
// operator's file (file.go), which answers for every setting the environment
// does not. Everything below withFile reads one getenv and cannot tell the two
// apart, which is the point — the file is a second *source*, never a second set
// of rules.
//
// Every check here is fail-closed on purpose. Sessions run with
// --dangerously-skip-permissions, so the allowlisted roots, the loopback bind,
// and the shared secret are not settings — they are the constraints standing in
// for the permission prompt that is gone. A value that would weaken one of them
// is a startup failure, never a warning (docs/security.md §4). There are two
// exceptions, both written down where they happen: an unset root list, which
// FR-004 requires be loud rather than fatal, and the layer-1 values, which an
// activated development bypass is allowed not to demand (FR-042).
package config

import (
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
)

// Every setting the daemon has, named as constants so an error message and the
// variable it blames cannot drift. Each is also a configuration-file key — the
// same name lower-cased without its CRSW_ prefix (KeyForVar in file.go) — so
// there is one list of settings rather than two that can disagree about which
// exist.
//
// gosec G101 fires on EnvSharedSecret because the identifier says "secret" and
// the value is a string literal. The value is the *name* of an environment
// variable and is meant to be published — .env.example carries it verbatim. The
// secret itself only ever exists as the []byte returned by loadSecret.
const (
	EnvSharedSecret = "CRSW_SHARED_SECRET" //nolint:gosec // G101: an env var name, not a credential
	EnvAllowedRoots = "CRSW_ALLOWED_ROOTS"
	EnvListen       = "CRSW_LISTEN"
	EnvMaxSessions  = "CRSW_MAX_SESSIONS"

	// EnvDiscoverRoots offers the immediate subdirectories of each approved root
	// as working-directory suggestions on the create form (#59, FR-041).
	//
	// Off by default, and that default is the setting's whole point: listing a
	// filesystem is a disclosure, however mild, and an operator opts into it
	// rather than finding their directory names on a page they did not ask to
	// have read the host. Anything strconv.ParseBool refuses — `yes`, `on`, `2`
	// — is a startup failure rather than a silent off, for the reason every
	// loader here refuses rather than defaults: an operator who wrote `yes` said
	// on, and a daemon that read it as off would run without the thing they asked
	// for and say nothing.
	//
	// It widens nothing. EnvAllowedRoots is still the control — a suggestion is
	// checked against the roots as it is offered and every path, picked or typed,
	// meets session.ResolveWorkDir on the create it is submitted with. See
	// Config.DiscoveredWorkDirs for what the walk may and may not name.
	EnvDiscoverRoots = "CRSW_DISCOVER_ROOTS"

	// EnvWorkdirSuggestions is the operator's own list of working directories the
	// create form offers, comma-separated (FR-006). It is the second of the three
	// sources the picker unions and the only one written by hand: the approved
	// roots are always offered, and their children only when EnvDiscoverRoots is
	// on.
	//
	// It reads no filesystem, which is what keeps it clear of the disclosure
	// EnvDiscoverRoots exists to gate. These paths were typed by an operator
	// rather than found on the host, so offering one says nothing about what is
	// there — not even that it exists.
	//
	// A path outside the approved roots is accepted here and refused on the create
	// it is submitted with, with the same response and the same audit record as a
	// typed one (contracts/directory-suggestions.md). That is the contract working
	// rather than a hole: this list is presentation and EnvAllowedRoots is the
	// control. What is refused at startup is only an entry that no configuration
	// could ever accept — see loadWorkdirSuggestions.
	EnvWorkdirSuggestions = "CRSW_WORKDIR_SUGGESTIONS"

	// EnvSessionEnvironment is the operator's list of additional variable NAMES
	// a session receives, on top of the base set in sessionenv.go.
	//
	// It exists because an allowlist has a cost, and this is what pays it: a
	// workflow that quietly depended on some variable the daemon happened to
	// inherit stops working when the boundary closes, with nothing to point at.
	// Rather than widen the base set for everybody's edge case, the operator
	// names theirs.
	//
	// **Names, never values.** The value comes from the daemon's own
	// environment; this list only says which of them may cross. A list of values
	// would be a second place to configure things that already have one, and a
	// place an operator would eventually put a credential.
	//
	// An entry naming a secret, or anything CRSW_, is a startup failure rather
	// than a silent drop — see loadSessionEnvironment. An escape hatch that
	// quietly ignored the dangerous case would be an escape hatch an operator
	// believes is working.
	EnvSessionEnvironment = "CRSW_SESSION_ENVIRONMENT"

	// EnvDestroyOnShutdown restores the pre-#63 behaviour: tear every session
	// down when the daemon stops. Default is off — a restart preserves them and
	// startup adoption reclaims them.
	EnvDestroyOnShutdown = "CRSW_DESTROY_ON_SHUTDOWN"

	// The two lifetime variables (#37). Defaults reproduce the constants the
	// daemon shipped with, so an operator who sets none of them sees no change.
	// MAX is the ceiling a per-session override may not exceed; without it an
	// override would be unbounded, which is the thing that must not be true of a
	// bound.
	//
	// There were four until milestone 15. CRSW_IDLE_TIMEOUT and
	// CRSW_IDLE_TIMEOUT_MAX went with the bound they configured; they are named
	// in retiredKeys so that a file still carrying one is refused rather than
	// read as though it still did something.
	EnvSessionLifetime    = "CRSW_SESSION_LIFETIME"
	EnvSessionLifetimeMax = "CRSW_SESSION_LIFETIME_MAX"
	EnvCreateRatePerMin   = "CRSW_CREATE_RATE_PER_MIN"
	EnvMaxBodyBytes       = "CRSW_MAX_BODY_BYTES"

	// EnvAccessEnabled declares that Cloudflare Access is this daemon's browser
	// door, which turns the three values below from optional into required (M12).
	//
	// It selects nothing on its own: the door is chosen from what is configured,
	// and a daemon carrying the three Access values has had the Access door since
	// long before this variable existed. What this adds is the operator *saying
	// so* — and a stated intention is checkable in the two places an unstated one
	// is not. Set with none of the three, it refuses to start rather than serving
	// the closed door an unset team domain would otherwise produce; set beside
	// EnvDashboardPassword, it names the ambiguity instead of picking a winner.
	//
	// It is deliberately not required for the Access door. Making it so would
	// mean every daemon already running on the three values either lost its
	// dashboard on upgrade or refused to start, and neither is a thing to do to
	// an operator who changed nothing.
	EnvAccessEnabled = "CRSW_ACCESS_ENABLED"

	// Layer 1 — the Cloudflare Access assertion the browser door validates.
	// Required, and fatal when absent, for the same reason the shared secret is:
	// a daemon that cannot verify who the browser is has no browser door at all.
	EnvAccessTeamDomain    = "CRSW_ACCESS_TEAM_DOMAIN"
	EnvAccessAUD           = "CRSW_ACCESS_AUD"
	EnvAccessAllowedEmails = "CRSW_ACCESS_ALLOWED_EMAILS"

	// EnvDashboardPassword is the other browser door: the one for a daemon on an
	// internal network, where there is no Cloudflare edge in front of it and
	// therefore no assertion for layer 1 to validate (M12).
	//
	// It is a secret by IsSecret, which is what keeps it out of the settings
	// page's value column and out of any form that page renders. A password
	// settable from the page it protects is not a door.
	//
	// It is never the door *as well as* Access. Two configured doors is a
	// startup failure, because which one is live decides who may execute code on
	// this host and that is the last question a daemon should answer by picking.
	//
	// gosec G101 fires here for the reason it fires on EnvSharedSecret: the
	// identifier says "password" and the value is a string literal. The value is
	// the *name* of an environment variable and is meant to be published —
	// .env.example carries it verbatim.
	EnvDashboardPassword = "CRSW_DASHBOARD_PASSWORD" //nolint:gosec // G101: an env var name, not a credential

	EnvMaxStreams = "CRSW_MAX_STREAMS"

	// EnvPaneBound is the largest screen a capture may return, in lines (#41,
	// FR-052). It reaches tmuxctl.NewExec, and tmuxctl.Exec.CapturePane is where
	// it is relied upon and stated.
	//
	// A capture past it is refused rather than shortened, because half a screen
	// is a wrong screen and not a smaller one (FR-053) — so this is a bound on
	// what the daemon is willing to *believe* about a pane, not a display
	// preference. Below 1 is a startup failure like every other count here.
	EnvPaneBound = "CRSW_PANE_BOUND"

	// EnvStartCommand is the command line typed into a new session's shell. It
	// names the command bound to DefaultStartCommandName, which is what a create
	// that asks for no command in particular gets.
	EnvStartCommand = "CRSW_START_COMMAND"

	// EnvStartCommands is the named set a create may choose from, spelled
	// `name=command line,name=command line`. A create carries a *name* and never
	// a command line (see StartCommands), so this variable is the whole of what
	// the daemon can be made to type — an operator who sets neither this nor
	// EnvStartCommand has exactly one command, and it is the one the daemon has
	// always used.
	EnvStartCommands = "CRSW_START_COMMANDS"

	// EnvRemoteControlCommand names which entry of that set the dashboard's
	// remote-control switch means (#58). It carries a name for the same reason a
	// create does: a switch wired to a name is a switch whose behaviour an
	// operator can read out of their own configuration and change there, and a
	// switch wired to a command line would be the thing the allowlist exists to
	// prevent (#38).
	//
	// Unset means DefaultRemoteControlCommandName. Set to a name the daemon does
	// not configure, it is a startup failure — an operator who spelled it
	// differently has said which name they mean, and a switch that silently
	// started plain sessions instead would be worse than no switch.
	EnvRemoteControlCommand = "CRSW_REMOTE_CONTROL_COMMAND"

	envHome = "HOME"

	// rootListSeparator is fixed at ":" rather than os.PathListSeparator: the
	// spec says colon, the daemon is a systemd/tmux service, and a separator
	// that changes with the build platform would change what an allowlist means.
	rootListSeparator = ":"

	// emailListSeparator is "," because that is what the spec fixes and because
	// an address cannot carry one outside a quoted local part, which no Google
	// identity has.
	emailListSeparator = ","

	// workdirSuggestionListSeparator is "," rather than the ":" a root list
	// takes, because the spec fixes it and because these two variables are not
	// the same kind of thing: an allowlist entry is a boundary and a suggestion
	// is a convenience. A directory whose name contains a comma cannot be
	// suggested, which costs the operator a path they type instead of pick —
	// the field is free text either way (FR-008).
	workdirSuggestionListSeparator = ","

	// sessionEnvironmentListSeparator is "," rather than the ":" the root list
	// uses: these are variable names, which cannot contain either character, and
	// a comma is what an operator writing a list of names reaches for.
	sessionEnvironmentListSeparator = ","

	// The two separators EnvStartCommands is spelled with. A command line may
	// therefore not contain a comma, which is a limit worth stating rather than
	// working around: every escape scheme that would lift it is a second grammar
	// between an operator and the thing this daemon types into an unsandboxed
	// shell. The name/command split is the *first* "=", so a command line may
	// carry as many as it likes.
	startCommandListSeparator = ","
	startCommandNameSeparator = "="

	// maxStartCommandNameLen bounds a name because a name reaches the audit
	// trail. Anything longer is a command line typed into the wrong half of an
	// entry.
	maxStartCommandNameLen = 32

	// maxSessionNameLen is session.ValidateName's ceiling, restated here for the
	// reason RenderStartCommand restates its alphabet: this package cannot import
	// the one that owns it, and the substitution's whole safety argument rests on
	// the bound holding at the point of substitution.
	maxSessionNameLen = 64
)

// ErrStartCommandName refuses a substitution whose session name is absent or
// outside the alphabet the licence in StartCommandNamePlaceholder depends on.
// Callers branch on it to answer a create the way they answer any other unusable
// name — a refusal, never a command line with an empty argument in it.
var ErrStartCommandName = errors.New("the session name cannot be put into the start command")

// Defaults for everything optional. There is deliberately no default for the
// shared secret: an absent secret is an absent auth layer.
const (
	MinSecretBytes = 32

	// MinDashboardPasswordLen bounds the password door because Principle I says
	// weak auth configuration is a startup failure rather than a warning, and a
	// password is the one credential in this daemon a human chooses. The shared
	// secret's 32 bytes are generated; this one is typed, and demanding 32 of
	// those would be answered with a shorter password in a different place.
	//
	// Sixteen because the attacker this door is written against is on the same
	// LAN: a fast network, and a rate limiter keyed per source is exactly as
	// effective as the number of sources they have. Sixteen characters is past
	// what an offline wordlist reaches, which is the bound worth having, and no
	// operator who is pasting from a password manager notices it.
	MinDashboardPasswordLen = 16

	// DefaultRootName is joined to $HOME. Never $HOME itself, which would make
	// the allowlist decorative — SSH keys, cloud credentials and browser
	// profiles all live directly under it.
	DefaultRootName = "code"

	DefaultListen           = "127.0.0.1:8765"
	DefaultMaxSessions      = 5
	DefaultCreateRatePerMin = 6
	DefaultMaxBodyBytes     = 65536

	// DefaultMaxStreams is twice DefaultMaxSessions, so every session on a fully
	// loaded host is watchable from two tabs before the daemon starts refusing.
	// The spec fixes the property, not the number: capped, and refusing past it.
	DefaultMaxStreams = 10

	// DefaultPaneBound is 200 lines: a tmux session this daemon starts is never
	// attached, so it keeps tmux's 80x24 default, and 200 is far above the
	// tallest terminal an operator could resize one to. Like DefaultMaxStreams
	// the spec fixes the property rather than the number — bounded, and refusing
	// past it — so this is chosen to fire only when the assumption behind it has
	// broken, never because a pane is merely full.
	//
	// What breaks it is a capture that reaches into the scrollback: tmux's own
	// history-limit defaults to 2000 lines, an order of magnitude past this, so
	// a -S added upstream is refused on the first capture instead of quietly
	// enlarging every screen the daemon believes in.
	DefaultPaneBound = 200

	// DefaultStartCommand is what every session started before this was
	// configurable at all, byte for byte. An operator who sets neither
	// EnvStartCommand nor EnvStartCommands gets exactly the daemon they had.
	DefaultStartCommand = "claude --dangerously-skip-permissions"

	// DefaultStartCommandName is the name a create that asks for nothing in
	// particular resolves to. It always exists — loadStartCommands fills it from
	// EnvStartCommand or from DefaultStartCommand — so there is no configuration
	// in which a create with no start_command has no command to run.
	DefaultStartCommandName = "default"

	// DefaultRemoteControlCommandName is the name the remote-control switch means
	// when EnvRemoteControlCommand names none. A daemon that configures no such
	// command offers no switch at all rather than one that cannot work.
	DefaultRemoteControlCommandName = "rc"

	// NeverLifetime is the one value EnvSessionLifetimeMax takes that is not a
	// duration: no ceiling at all, and therefore a daemon on which a create may
	// ask for a session that never expires (milestone 13).
	//
	// A word rather than a number, and this is the whole argument for it. The
	// spelling could not be `0`, because zero is what "the operator said
	// nothing" already looks like by the time a duration has been parsed — but
	// it could have been a negative duration, which is how the *record* spells
	// the same thing. It is not, because the two surfaces fail differently: a
	// negative on a record is written by this daemon's own code, while a
	// duration in a configuration file is typed by a person, and `0` or `-1h` in
	// that file are both easy to write meaning "no time at all". Getting that
	// backwards switches off the one bound that is never renewed, on a host
	// running unsandboxed shells. `never` cannot be misread in that direction,
	// and loadLifetimeCeiling refuses a negative rather than accepting it as a
	// second spelling of this one.
	NeverLifetime = "never"

	// StartCommandNamePlaceholder is the one substitution a configured start
	// command may carry: the session's own name, so that a `claude
	// remote-control --name {name}` shows in claude.ai under the name the
	// operator gave it here rather than one Claude invented (#58).
	//
	// Interpolating a value into a command line typed at a shell is normally how
	// injection happens. It is safe for this one field, for one stated reason:
	// **session.ValidateName restricts a name to `^[a-zA-Z0-9-]{1,64}$`** — no
	// space, quote, semicolon, backtick, dollar or newline — so there is no name
	// a caller can supply that changes the *shape* of the command line, only the
	// argument it already had. RenderStartCommand re-checks that alphabet at the
	// point of substitution rather than trusting its caller to have done so.
	//
	// That guarantee is the whole licence. If this set ever grows a second
	// placeholder, the new value has to be *proven* unable to alter a command
	// line — a working directory, for one, cannot be: it is a filesystem path and
	// may contain anything. Extending the set is a decision to be reconsidered,
	// not a line to be copied.
	StartCommandNamePlaceholder = "{name}"
)

// ApprovedRoot is a directory a session may run in, resolved once at startup so
// that a root which is itself a symlink cannot be swapped between the check and
// the spawn. Containment against these lives in internal/session.
type ApprovedRoot struct {
	// Path is absolute, cleaned, and has every symlink already resolved.
	Path string

	// IsDefault records that EnvAllowedRoots was unset and the built-in root is
	// in force. It drives the loud startup warning and the startup audit record.
	IsDefault bool
}

// StartCommands is the operator's named set of command lines a session may be
// started with, resolved once at startup.
//
// The names are the API. A create carries one of them and never a command line,
// because a create route that took a command line would make this daemon a
// general remote-shell service behind whatever authenticates that route rather
// than a Claude session manager — the same reason a working directory is chosen
// from an allowlist instead of being named freely (FR-028). What a caller can
// influence is *which* of the operator's commands runs, and nothing else.
//
// Every command line here has already been through validateStartCommand, so a
// value that came out of this type is safe to hand to tmux send-keys: it carries
// no ";" for tmux's parser to eat (research D4) and no control byte that would
// submit half a line. That check is at startup rather than at delivery so an
// operator learns about it from the daemon refusing to boot, not from a session
// that started with three quarters of a command.
//
// The zero value holds nothing and answers false to every lookup. Only Load
// builds a usable one, which is what keeps "the set was validated" true of every
// value of this type that exists.
type StartCommands struct {
	byName map[string]string
}

// NewStartCommands builds a set from names already validated by their caller.
//
// It exists because the loader reads the environment, and the packages that
// *use* a set — session, and its tests — have no environment to read. Without
// this the only way to exercise a configured daemon from another package is to
// set process-wide variables, which is a test that cannot run in parallel with
// its neighbours.
//
// It copies the map. A set that shared storage with its caller would be a value
// whose meaning could change after it was handed over, and the whole point of
// this type is that the resolved command line is fixed at load.
func NewStartCommands(byName map[string]string) StartCommands {
	out := StartCommands{byName: make(map[string]string, len(byName))}
	for name, cmd := range byName {
		out.byName[name] = cmd
	}
	return out
}

// Command is the command line for a name, and reports whether the name is one
// the operator configured. An empty name is DefaultStartCommandName, so a create
// that asks for nothing gets the daemon's default rather than a refusal.
func (c StartCommands) Command(name string) (string, bool) {
	if name == "" {
		name = DefaultStartCommandName
	}
	cmd, ok := c.byName[name]
	return cmd, ok
}

// Names is every configured name, sorted, so a dashboard renders them in the
// same order on every render and a test can assert on the list.
func (c StartCommands) Names() []string {
	names := make([]string, 0, len(c.byName))
	for name := range c.byName {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// Len is how many commands are configured. Nothing enforces a cap on it: the set
// comes from the environment the daemon was started with, which is the operator
// themselves.
func (c StartCommands) Len() int { return len(c.byName) }

// String names the commands and never spells one. The command lines are not
// secret, but they are the closest thing this daemon has to an executable
// payload, and a Config is formatted into logs — so the names travel and the
// bodies stay where they were configured.
func (c StartCommands) String() string { return fmt.Sprintf("%v", c.Names()) }

// Config is the validated configuration. Every field is safe to use as-is;
// nothing here needs a second check on the request path.
type Config struct {
	// SharedSecret is at least MinSecretBytes long. It must never be logged,
	// returned in an error, or echoed back — not even its length (FR-043).
	SharedSecret []byte

	Roots       []ApprovedRoot
	Listen      string
	MaxSessions int

	// DiscoverRoots turns the working-directory suggestions on the create form
	// into a walk one level below each approved root. Off unless the operator
	// asked for it; DiscoveredWorkDirs in discover.go is the walk it gates, and
	// is the only thing that reads this.
	DiscoverRoots bool

	// WorkdirSuggestions is the operator's explicit list of directories the create
	// form offers, in the order they wrote them. It is one of the three sources
	// the picker unions and it authorises nothing on its own: a path taken from it
	// meets Roots on the create it is submitted with, exactly as a typed one does.
	WorkdirSuggestions []string

	// SessionEnvironment is the operator's list of additional variable names a
	// session receives. Empty on almost every deployment, which is the intent:
	// the base set in sessionenv.go is meant to be enough.
	SessionEnvironment []string

	// DestroyOnShutdown tears every session down on a clean stop. Off by
	// default: a graceful restart is overwhelmingly the common case, and
	// destroying a fleet to redeploy a binary is a cost nobody asked for.
	DestroyOnShutdown bool

	// SessionLifetime is what a create that asks for nothing gets;
	// SessionLifetimeMax is the ceiling an override is checked against (#37).
	//
	// SessionLifetimeMax may be negative, which is NeverLifetime loaded: no
	// ceiling, and therefore a daemon on which a create may ask for a session
	// that never expires. Read it through the sign and never through a
	// comparison that assumes a duration in the future.
	SessionLifetime    time.Duration
	SessionLifetimeMax time.Duration
	CreateRatePerMin   int
	MaxBodyBytes       int64

	// AccessEnabled is the operator's declaration that Cloudflare Access is the
	// browser door. Read EnvAccessEnabled for what it does and does not decide:
	// the door is selected from AccessTeamDomain and DashboardPassword, and this
	// is what makes the three Access values required and the two doors exclusive.
	AccessEnabled bool

	// AccessTeamDomain is a normalised origin — scheme and host, no path, host
	// lower-cased. It is one configured value because two things must agree: the
	// issuer is exactly this string, and the key set is fetched from it.
	// Configuring them separately would allow an issuer and a key set that do not
	// belong to each other, which is a validator checking signatures against the
	// wrong authority.
	AccessTeamDomain string

	// AccessAUD is compared for equality against the assertion's audience and
	// never parsed, so only non-emptiness is enforced. Pinning Cloudflare's
	// current 64-hex format would add nothing — a wrong value already fails every
	// request — and would break the daemon the day that format changes.
	AccessAUD string

	// AccessAllowedEmails is the daemon's own copy of the list the edge enforces.
	// The edge is the gate; this is the daemon asserting the gate is configured
	// as believed.
	AccessAllowedEmails []string

	// DashboardPassword is the browser door for a daemon with no Cloudflare edge
	// in front of it. Empty means no password door, and it is never non-empty at
	// the same time as AccessTeamDomain — validateDoors refuses that start.
	//
	// It is held as the bytes the operator configured, like SharedSecret and for
	// the same reason: hashing it here would put a second representation of one
	// credential in the process, and the comparison that matters hashes both
	// sides at the point of comparison anyway. It must never be logged, rendered,
	// or returned in an error — not even its length.
	DashboardPassword []byte

	// MaxStreams bounds concurrent live-output streams, which are the one thing
	// a browser can hold open indefinitely.
	MaxStreams int

	// PaneBound is how many lines of screen a capture may return, handed to
	// tmuxctl.NewExec at wiring time. Read tmuxctl.Exec.CapturePane for what a
	// larger one means and why it is refused rather than shortened.
	PaneBound int

	// StartCommands is the named set a create chooses from, always carrying
	// DefaultStartCommandName.
	StartCommands StartCommands

	// RemoteControlCommand is the *name* in that set which the dashboard's
	// remote-control switch turns on (#58). Empty means this daemon configures no
	// remote-control command, and the dashboard renders no switch — an operator
	// is never offered a control whose only outcome is a refusal.
	RemoteControlCommand string

	// Sources is which layer supplied each value, keyed by environment-variable
	// name — the record that answers "why did my edit do nothing?" (FR-018).
	//
	// It is written by withFile as it decides and never computed afterwards. A
	// value present and equal in the environment and the file is indistinguishable
	// by comparison, and that is precisely the case where an operator is asking
	// the question; so provenance is a byproduct of having one place decide
	// (research R4).
	//
	// Populated once, during LoadFrom, and not written again: nothing calls the
	// shim after the Config is built. A key absent from it was never looked up,
	// and reads SourceDefault — the zero value, which is why that constant is
	// zero. The settings page reads this, and nothing else does.
	Sources map[string]Source

	// FilePath is the configuration file these values were read from, and is
	// empty when none was (FR-018). The settings page names it above the table.
	//
	// It is the file that was actually *read*, which is not always the file the
	// operator most recently wrote: a start recovered from the backup beside it
	// carries the backup's path, because naming the live file there would have
	// the page describe a set of values that is not in effect. That is the same
	// question provenance answers one row at a time — "which layer supplied
	// this?" — asked once about the whole file.
	FilePath string
}

// String redacts the shared secret so that formatting a Config — in a log line,
// a panic, or a hastily added debug print — cannot leak it. GoString does the
// same for %#v.
//
// The allowed addresses are counted rather than named: they are a list of real
// people, and this string is written wherever a Config is formatted.
//
// The dashboard password is redacted rather than counted, unlike the addresses:
// a count is a length, and a length is the one measurement of a human-chosen
// password worth having. Whether there is one at all is said plainly, because a
// log line that read `<redacted>` for a daemon with no password door would
// describe a door that is not there.
func (c Config) String() string {
	password := "unset"
	if len(c.DashboardPassword) > 0 {
		password = "<redacted>"
	}
	return fmt.Sprintf("config{shared_secret:<redacted> roots:%v listen:%q max_sessions:%d create_rate_per_min:%d max_body_bytes:%d access_enabled:%t access_team_domain:%q access_aud:%q allowed_emails:%d dashboard_password:%s max_streams:%d start_commands:%v}",
		c.Roots, c.Listen, c.MaxSessions, c.CreateRatePerMin, c.MaxBodyBytes,
		c.AccessEnabled, c.AccessTeamDomain, c.AccessAUD, len(c.AccessAllowedEmails),
		password, c.MaxStreams, c.StartCommands)
}

// GoString mirrors String, so %#v is not a way around the redaction.
func (c Config) GoString() string { return c.String() }

// Option adjusts what counts as a complete configuration. There is exactly one,
// because there is exactly one thing outside the environment that changes the
// answer.
type Option func(*loadOptions)

type loadOptions struct {
	accessBypassed bool

	// noFile stops the loader consulting the operator's configuration file.
	//
	// It exists for Validate, which is asking whether a *candidate* would load.
	// Layering the file on disk underneath it makes that question unanswerable:
	// the candidate is the same file, so its old contents would supply anything
	// the new ones dropped, and removing a key would validate as safe.
	//
	// It is also what made the edit tests pass on the author's machine and fail
	// in CI. Validate borrowed the secret from his real file and called the
	// candidate loadable; a runner with no such file called it what it was.
	noFile bool
}

// WithoutConfigFile evaluates the environment alone, with no file underneath.
func WithoutConfigFile() Option {
	return func(o *loadOptions) { o.noFile = true }
}

// WithAccessBypassActive stops the loader demanding the three layer-1 values
// (FR-042). It says the operator has *activated* the development bypass — a
// flag that exists only in a `-tags dev` build — not that a build merely carries
// one, because demanding an audience the bypass then ignores would make local
// development need a Cloudflare account.
//
// It lifts the requirement to be present, never the requirement to be valid: a
// layer-1 value that is set and malformed is still a startup failure, since the
// operator meant it and will one day run without the bypass.
func WithAccessBypassActive() Option {
	return func(o *loadOptions) { o.accessBypassed = true }
}

// Load reads the configuration from the real environment and the file at
// DefaultPath, writing any startup warning to stderr. It is the only form
// cmd/crswd needs.
func Load(opts ...Option) (*Config, error) { return LoadFrom(os.Getenv, os.Stderr, opts...) }

// LoadFrom is Load with the environment and the warning sink injected, so tests
// can drive every case in parallel without mutating the process environment.
func LoadFrom(getenv func(string) string, warn io.Writer, opts ...Option) (*Config, error) {
	if getenv == nil {
		return nil, errors.New("no environment source provided; refusing to start")
	}
	var o loadOptions
	for _, opt := range opts {
		opt(&o)
	}
	if warn == nil {
		// Discarding here would make FR-004's warning silent, which is the one
		// thing it may never be.
		warn = os.Stderr
	}

	// The file is a source *behind* this seam and never a system beside it. Every
	// loader below reads through getenv exactly as it did before there was a
	// file, so a value the operator wrote in one is bounded by the same check,
	// defaulted by the same fallback and blamed in the same message as one they
	// put in a unit. A file layer with validation of its own would be a second
	// set of rules for the first to disagree with, and the disagreement would
	// surface as a bound that means one thing in a test and another in
	// production.
	path := DefaultPath(getenv)
	if o.noFile {
		path = ""
	}
	var file *File
	var err error
	if path != "" {
		file, err = ReadFile(path, warn)
	}
	if err == nil {
		cfg, loadErr := loadWith(getenv, file, warn, o)
		if loadErr == nil {
			return cfg, nil
		}
		// A file that was never there cannot be the reason this failed, and the
		// backup of one is not the recovery: the environment and the defaults
		// that produced the refusal are the same with the backup layered in. The
		// deployment with no file at all keeps refusing exactly as it does today.
		if file == nil {
			return nil, loadErr
		}
		err = loadErr
	}

	// FR-010. The operator's file will not load; the last known-good one is
	// beside it, and reading it is the only recovery that does not need shell
	// access on this host.
	cfg, announceErr := loadBackup(getenv, path, warn, o, err)
	switch {
	case announceErr != nil:
		// A fallback nobody was told about is a daemon running on a
		// configuration its operator did not write, so a warning that could not
		// be emitted refuses the start instead of proceeding quietly.
		return nil, announceErr
	case cfg != nil:
		return cfg, nil
	default:
		// No usable backup: the operator's own file is the problem, and its
		// refusal is the one they can act on. The backup's is not — they never
		// wrote it.
		return nil, err
	}
}

// loadWith is the load itself, with the file already resolved: it layers that
// file behind the environment, reads every setting through the one seam, and
// builds the Config.
//
// It is separate from LoadFrom because FR-010 runs it twice — once on the
// operator's file, and, when that will not load, once on the backup beside it.
// Both attempts go through exactly this code, so a value recovered from a
// backup is bounded, defaulted and refused identically to one read live. The
// cost is that a warning the first attempt emitted is emitted again by the
// second; the announcement between them says why the same line appears twice.
func loadWith(getenv func(string) string, file *File, warn io.Writer, o loadOptions) (*Config, error) {
	// Provenance is recorded by the shim as it answers, because the shim is the
	// only code that knows which layer won. The map is handed in rather than
	// returned so that the recording is the same statement as the decision — a
	// second traversal that worked it out afterwards could only compare values,
	// and two equal values do not say which one was used.
	sources := make(map[string]Source, len(Vars()))
	getenv = withFile(getenv, file, sources)

	secret, err := loadSecret(getenv)
	if err != nil {
		return nil, err
	}
	roots, err := loadRoots(getenv, warn)
	if err != nil {
		return nil, err
	}
	discoverRoots, err := loadBool(getenv, EnvDiscoverRoots)
	if err != nil {
		return nil, err
	}
	workdirSuggestions, err := loadWorkdirSuggestions(getenv)
	if err != nil {
		return nil, err
	}
	sessionEnvironment, err := loadSessionEnvironment(getenv)
	if err != nil {
		return nil, err
	}
	// Read here for the first time since #63 shipped the variable. The constant
	// existed, server.go branched on the field, and the settings page rendered
	// it — but nothing ever assigned it, so it was false on every daemon that
	// has ever run and the operator's opt-out did nothing. Nothing failed
	// loudly: a flag that silently means its default looks exactly like a flag
	// working, right up until somebody needs the other behaviour.
	//
	// It is the fourth time this repository has shipped code with no caller, and
	// the first one a page caught: the settings page renders every variable's
	// value and its source, so this one read "false / default" no matter what
	// the operator set. That is the whole argument for the page.
	destroyOnShutdown, err := loadBool(getenv, EnvDestroyOnShutdown)
	if err != nil {
		return nil, err
	}
	maxSessions, err := loadInt(getenv, EnvMaxSessions, DefaultMaxSessions)
	if err != nil {
		return nil, err
	}
	createRate, err := loadInt(getenv, EnvCreateRatePerMin, DefaultCreateRatePerMin)
	if err != nil {
		return nil, err
	}
	maxBody, err := loadInt64(getenv, EnvMaxBodyBytes, DefaultMaxBodyBytes)
	if err != nil {
		return nil, err
	}
	lifetime, err := loadDuration(getenv, EnvSessionLifetime, session_AbsoluteLifetime)
	if err != nil {
		return nil, err
	}
	lifetimeMax, err := loadLifetimeCeiling(getenv, EnvSessionLifetimeMax, lifetime)
	if err != nil {
		return nil, err
	}
	if err := validateLifetimes(lifetime, lifetimeMax); err != nil {
		return nil, err
	}

	maxStreams, err := loadInt(getenv, EnvMaxStreams, DefaultMaxStreams)
	if err != nil {
		return nil, err
	}
	paneBound, err := loadInt(getenv, EnvPaneBound, DefaultPaneBound)
	if err != nil {
		return nil, err
	}
	accessEnabled, err := loadBool(getenv, EnvAccessEnabled)
	if err != nil {
		return nil, err
	}
	teamDomain, err := loadTeamDomain(getenv, o.accessBypassed)
	if err != nil {
		return nil, err
	}
	aud, err := loadAUD(getenv, o.accessBypassed)
	if err != nil {
		return nil, err
	}
	emails, err := loadAllowedEmails(getenv, o.accessBypassed)
	if err != nil {
		return nil, err
	}
	if err := validateAccessGroup(teamDomain, aud, emails, o.accessBypassed); err != nil {
		return nil, err
	}
	dashboardPassword, err := loadDashboardPassword(getenv)
	if err != nil {
		return nil, err
	}
	// After the group check, so that teamDomain standing for "Access is
	// configured" is a fact rather than an assumption: all three or none of them
	// is already true by the time this asks.
	if err := validateDoors(accessEnabled, teamDomain, dashboardPassword, o.accessBypassed); err != nil {
		return nil, err
	}
	// Read after the two checks above rather than beside the other bounds, which
	// is where it used to sit: what a listen address is allowed to be now depends
	// on whether anybody can get through the browser door, and that is not a fact
	// until the group check and the selection have both had their say (M12/T002).
	listen, err := loadListen(getenv, browserDoorAdmits(teamDomain, dashboardPassword))
	if err != nil {
		return nil, err
	}
	if !browserDoorAdmits(teamDomain, dashboardPassword) && !o.accessBypassed {
		if err := warnNoIdentityProvider(warn); err != nil {
			return nil, err
		}
	}
	startCommands, err := loadStartCommands(getenv)
	if err != nil {
		return nil, err
	}
	remoteControl, err := loadRemoteControlCommand(getenv, startCommands)
	if err != nil {
		return nil, err
	}

	return &Config{
		SharedSecret:        secret,
		Roots:               roots,
		DiscoverRoots:       discoverRoots,
		WorkdirSuggestions:  workdirSuggestions,
		SessionEnvironment:  sessionEnvironment,
		DestroyOnShutdown:   destroyOnShutdown,
		Listen:              listen,
		MaxSessions:         maxSessions,
		CreateRatePerMin:    createRate,
		MaxBodyBytes:        maxBody,
		AccessEnabled:       accessEnabled,
		AccessTeamDomain:    teamDomain,
		AccessAUD:           aud,
		AccessAllowedEmails: emails,
		DashboardPassword:   dashboardPassword,
		MaxStreams:          maxStreams,
		PaneBound:           paneBound,
		SessionLifetime:     lifetime,
		SessionLifetimeMax:  lifetimeMax,
		StartCommands:       startCommands,

		RemoteControlCommand: remoteControl,
		Sources:              sources,

		// The file this load actually read, taken from the *File that was
		// layered in rather than from DefaultPath: a path that was looked for
		// and not found would otherwise have the settings page name a file
		// nobody wrote. A nil *File is the deployment with no file at all and
		// answers with the empty string; the recovery in loadBackup comes
		// through here with the backup's own *File, so it names the file whose
		// values are in effect.
		FilePath: file.Path(),
	}, nil
}

// loadBackup answers with the configuration the last known-good file makes, or
// with nil when there is no usable one (FR-010).
//
// The recovery this exists for is the operator who edited the file from
// somewhere that is not a terminal on this host — the daemon is how they reach
// it, and a daemon that refuses to start is a daemon they cannot reach to fix.
// A copy of the file that worked, read loudly, is the only way back that does
// not need shell access.
//
// The failed file is never touched. It is what they will fix, the announcement
// says where it is, and nothing in this package has a write path to it anyway.
//
// The backup is accepted only if it loads *completely*: it goes through the
// same loadWith as the live file, so a stale backup that no longer satisfies a
// bound is not a start either. Anything less would make this the one path where
// a value skipped its check.
//
// The error returned is the announcement's, never the load's: a caller reading
// nil, nil is being told there is nothing here, and the refusal it reports is
// the operator's own file, which is the one they can act on.
func loadBackup(getenv func(string) string, path string, warn io.Writer, o loadOptions, cause error) (*Config, error) {
	if path == "" {
		return nil, nil
	}
	backup := BackupPath(path)
	f, err := ReadFile(backup, warn)
	if err != nil || f == nil {
		return nil, nil
	}
	cfg, err := loadWith(getenv, f, warn, o)
	if err != nil {
		return nil, nil
	}
	if err := warnLoadedFromBackup(warn, path, backup, cause); err != nil {
		return nil, err
	}
	return cfg, nil
}

// warnLoadedFromBackup says, loudly and on every start, that this daemon is not
// running on the file its operator most recently wrote.
//
// It names both files, because the recovery is a two-step one: the daemon is up
// on the older configuration, and the newer one is still there with the defect
// still in it. It carries the refusal that caused it, which is built in this
// repository and names a path and a line and never a value — the same error
// main.go would have printed on the way to exiting.
func warnLoadedFromBackup(warn io.Writer, path, backup string, cause error) error {
	banner := fmt.Sprintf(
		"crswd: the configuration at %s will not load (%v); started from the backup at %s instead. "+
			"This daemon is running on the older file: %s is unchanged, and every setting written into it since the backup was taken is NOT in effect. Fix it and restart.\n",
		path, cause, backup, path)

	if _, err := io.WriteString(warn, banner); err != nil {
		return fmt.Errorf("emit the warning that the configuration was read from %s: %w", backup, err)
	}
	return nil
}

// withFile answers from the environment first and the file second, and records
// which one answered. That is the whole of the precedence chain
// (contracts/config-precedence.md), and its size is the design: precedence has
// one implementation, and no bound, default or refusal is written a second time
// behind it. It is the only code that knows where a value came from, which is
// why it is also the only code that records it.
//
//	flag > environment > file > default
//
// There is no flag layer yet. When there is, it goes in front of this one and
// nothing underneath has to learn about it.
//
// The environment beats the file so a container or a test can change one value
// without writing one (FR-004). Reversed, a stale file on a host would silently
// override the environment a container was deployed with — which is why this is
// four lines that are read rather than a merge that is trusted. The file beats
// only the built-in default, which is what makes a daemon with no file behave
// exactly as it does today (FR-003, SC-002).
//
// Non-emptiness rather than presence, because non-emptiness is the whole of what
// a getenv can see — and because `CRSW_LISTEN=` in a unit is an operator
// clearing a variable rather than setting it to something the loader could use.
// It falls through to the file and then to the default, which is what an unset
// variable has always done.
//
// A nil *File is the deployment with no file at all: every lookup misses and
// every value falls through, and this shim still runs for every key rather than
// being skipped, so there is one code path and not two.
//
// src is written for every name asked of it and never read here, so a name that
// is not a setting — HOME, which defaultRoot reads through this seam — is
// recorded as truthfully as the rest. That costs nothing: the settings page
// walks Vars() and asks this map about each, so it renders the settings and only
// the settings. A filter here would be a second rule about what a setting is,
// which is the thing this package keeps to one place.
//
// What is never recorded is a value. This map is names and layers, so the record
// of where the shared secret came from cannot become a copy of it.
func withFile(getenv func(string) string, f *File, src map[string]Source) func(string) string {
	return func(name string) string {
		if v := getenv(name); v != "" {
			src[name] = SourceEnv
			return v
		}
		if v, ok := f.Lookup(name); ok {
			src[name] = SourceFile
			return v
		}
		src[name] = SourceDefault
		return ""
	}
}

// loadRemoteControlCommand decides what the dashboard's remote-control switch
// means on this daemon (#58).
//
// Unset resolves to DefaultRemoteControlCommandName, and only if the operator
// actually configured a command by that name — a daemon that configures none has
// no remote control to switch on, so it gets the form it had before this
// existed. Set explicitly, the name must exist: an operator who spelled their
// command differently has said which one they mean, and a switch that quietly
// started plain sessions is exactly the failure a create refuses an unknown name
// to avoid.
func loadRemoteControlCommand(getenv func(string) string, cmds StartCommands) (string, error) {
	name := strings.TrimSpace(getenv(EnvRemoteControlCommand))
	if name == "" {
		if _, ok := cmds.Command(DefaultRemoteControlCommandName); !ok {
			return "", nil
		}
		return DefaultRemoteControlCommandName, nil
	}
	if err := validateStartCommandName(EnvRemoteControlCommand, name); err != nil {
		return "", err
	}
	if _, ok := cmds.Command(name); !ok {
		return "", fmt.Errorf("%s names the %q start command, which %s does not configure; refusing to start",
			EnvRemoteControlCommand, name, EnvStartCommands)
	}
	return name, nil
}

// loadWorkdirSuggestions reads the operator's own list of directories the create
// form offers (FR-006).
//
// Nothing here touches the filesystem, and that is the point rather than an
// omission: a suggestion is a string in a <datalist>, and session.ResolveWorkDir
// decides on the create whether the path submitted may be run in. Existence,
// symlinks and containment are settled there, against the value that actually
// arrived. Settling them here would put a read of the host behind a key that is
// live by default, which is the disclosure EnvDiscoverRoots exists to keep
// opt-in.
//
// The two refusals are the entries no configuration could ever make usable.
// ResolveWorkDir demands an absolute path before it considers anything else, so
// a relative entry is a suggestion whose only possible outcome is a refusal —
// unlike one outside the roots, which the operator can make good by widening the
// allowlist. An empty entry is a stray separator, and it would render a blank
// option in the picker. Both refuse to start rather than being skipped, for the
// reason every list in this file refuses: an entry dropped in silence leaves an
// operator hunting for a directory they configured and were never told about.
//
// Surrounding whitespace goes so that `a, b` means what it looks like. Interior
// whitespace stays — a directory name may legitimately contain a space, and this
// is the operator's own path rather than caller text.
func loadWorkdirSuggestions(getenv func(string) string) ([]string, error) {
	raw := strings.TrimSpace(getenv(EnvWorkdirSuggestions))
	if raw == "" {
		return nil, nil
	}

	parts := strings.Split(raw, workdirSuggestionListSeparator)
	// Duplicates are kept. The union in suggestions.go dedupes across all three
	// sources, and a second rule here would be a list that disagrees with itself
	// about how many suggestions the operator configured.
	suggestions := make([]string, 0, len(parts))
	for _, part := range parts {
		path := strings.TrimSpace(part)
		if path == "" {
			return nil, fmt.Errorf("%s contains an empty entry; refusing to start", EnvWorkdirSuggestions)
		}
		if !filepath.IsAbs(path) {
			return nil, fmt.Errorf("%s entry %q is not an absolute path, so no create could ever accept it; refusing to start",
				EnvWorkdirSuggestions, path)
		}
		suggestions = append(suggestions, path)
	}
	return suggestions, nil
}

// loadSessionEnvironment reads the operator's list of additional variable names
// a session may receive, and refuses the entries that would undo the boundary.
//
// The refusal is a startup failure rather than a warning or a silent drop, for
// the reason every refusal in this file is one: a daemon that starts, works, and
// never mentions it again leaves the operator believing they configured
// something. Here the something is a credential reaching an unsandboxed shell,
// which is the case where "starts anyway" is least acceptable.
//
// **No error here ever names a value**, only the entry — which is a variable
// NAME, so naming it discloses nothing. That is the same rule the configuration
// file's refusals follow.
func loadSessionEnvironment(getenv func(string) string) ([]string, error) {
	raw := strings.TrimSpace(getenv(EnvSessionEnvironment))
	if raw == "" {
		return nil, nil
	}

	parts := strings.Split(raw, sessionEnvironmentListSeparator)
	names := make([]string, 0, len(parts))
	for _, part := range parts {
		name := strings.TrimSpace(part)
		if name == "" {
			return nil, fmt.Errorf("%s contains an empty entry; refusing to start", EnvSessionEnvironment)
		}

		// The daemon's own configuration, by the prefix rule. Passing one on
		// would put this daemon's settings in the environment of the code it
		// starts, and is also what makes this project's own test suite fail when
		// run from inside a session.
		if strings.HasPrefix(name, envPrefix) {
			return nil, fmt.Errorf("%s names %s, which is this daemon's own configuration and is never given to a session; refusing to start",
				EnvSessionEnvironment, name)
		}

		// Belt and braces: a secret that one day is not spelled with the prefix
		// above is still refused, and the two answers cannot drift apart.
		if IsSecret(KeyForVar(name)) {
			return nil, fmt.Errorf("%s names the secret %s, which a session must never receive; refusing to start",
				EnvSessionEnvironment, name)
		}

		names = append(names, name)
	}
	return names, nil
}

// RenderStartCommand substitutes a session's own name into a configured start
// command, and is the only place a caller-supplied value ever reaches one.
//
// Read StartCommandNamePlaceholder for why this is safe and where the licence
// ends. The alphabet is re-checked here, deliberately duplicating
// session.ValidateName, because this function is where the value stops being
// data and becomes part of a line typed at an unsandboxed shell — and because
// internal/session imports this package, so the check cannot be borrowed.
//
// A command with no placeholder is returned untouched, so a name is never
// required by a command that does not ask for one. A command that *does* ask
// with nothing to give is an error rather than an empty `--name`: the operator
// asked for their name on the other side, and a blank argument is neither that
// nor an honest refusal.
func RenderStartCommand(command, sessionName string) (string, error) {
	if !strings.Contains(command, StartCommandNamePlaceholder) {
		return command, nil
	}
	if sessionName == "" {
		return "", fmt.Errorf("%w: the start command needs a session name and this session has none", ErrStartCommandName)
	}
	if len(sessionName) > maxSessionNameLen {
		return "", fmt.Errorf("%w: longer than %d characters", ErrStartCommandName, maxSessionNameLen)
	}
	for _, c := range sessionName {
		if (c < 'a' || c > 'z') && (c < 'A' || c > 'Z') && (c < '0' || c > '9') && c != '-' {
			return "", fmt.Errorf("%w: outside [a-zA-Z0-9-]", ErrStartCommandName)
		}
	}
	return strings.ReplaceAll(command, StartCommandNamePlaceholder, sessionName), nil
}

// unknownPlaceholder finds the first `{identifier}` in a command line that is
// not one this daemon substitutes.
//
// A typo is a startup failure rather than a literal: `{worrking_dir}` typed at a
// shell is a brace expansion nobody asked for, and the operator who wrote it
// believes their working directory is being passed. Naming the token in the
// refusal is what turns that into a five-second fix.
//
// Only `{` immediately followed by an identifier and closed by `}` counts, so a
// shell brace expansion like `{1..3}` still passes through — and `${HOME}` is
// exempt outright, because that brace belongs to the shell's own grammar and the
// daemon never interprets it.
func unknownPlaceholder(command string) (string, bool) {
	for i := 0; i < len(command); i++ {
		if command[i] != '{' || (i > 0 && command[i-1] == '$') {
			continue
		}
		end := strings.IndexByte(command[i:], '}')
		if end < 0 {
			break
		}
		token := command[i : i+end+1]
		if !isPlaceholder(token) || token == StartCommandNamePlaceholder {
			continue
		}
		return token, true
	}
	return "", false
}

// isPlaceholder reports whether a `{...}` run is shaped like one of ours: a
// non-empty identifier and nothing else.
func isPlaceholder(token string) bool {
	inner := token[1 : len(token)-1]
	if inner == "" {
		return false
	}
	for i, c := range inner {
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c == '_':
		case i > 0 && c >= '0' && c <= '9':
		default:
			return false
		}
	}
	return true
}

// loadStartCommands builds the named set a create chooses from.
//
// Three shapes, and the first is the one that matters: with neither variable set
// the set is exactly {default: DefaultStartCommand}, so a daemon nobody
// configured types what it has always typed. EnvStartCommand replaces the
// default's command line; EnvStartCommands adds names beside it.
//
// The two are refused together when both would name the default, rather than one
// silently winning. Which of two spellings of one value takes effect is the
// question an operator can least afford to guess about here: the answer decides
// what gets typed into an unsandboxed shell on every create that names no
// command.
//
// Every command line is validated, and a bad one is a startup failure rather
// than a create-time refusal (docs/security.md §4) — see validateStartCommand for
// what "bad" means and why it is settled here.
func loadStartCommands(getenv func(string) string) (StartCommands, error) {
	fallback := strings.TrimSpace(getenv(EnvStartCommand))
	defaultSet := fallback != ""
	if !defaultSet {
		fallback = DefaultStartCommand
	}
	if err := validateStartCommand(EnvStartCommand, DefaultStartCommandName, fallback); err != nil {
		return StartCommands{}, err
	}

	byName := map[string]string{DefaultStartCommandName: fallback}

	raw := strings.TrimSpace(getenv(EnvStartCommands))
	if raw == "" {
		return StartCommands{byName: byName}, nil
	}

	named := map[string]bool{}
	for _, entry := range strings.Split(raw, startCommandListSeparator) {
		if strings.TrimSpace(entry) == "" {
			return StartCommands{}, fmt.Errorf("%s contains an empty entry; refusing to start", EnvStartCommands)
		}
		name, command, found := strings.Cut(entry, startCommandNameSeparator)
		if !found {
			return StartCommands{}, fmt.Errorf("%s has an entry that is not %s; refusing to start",
				EnvStartCommands, "name"+startCommandNameSeparator+"command")
		}

		name = strings.TrimSpace(name)
		if err := validateStartCommandName(EnvStartCommands, name); err != nil {
			return StartCommands{}, err
		}
		if named[name] {
			return StartCommands{}, fmt.Errorf("%s names %q twice; refusing to start", EnvStartCommands, name)
		}
		// Only the *second* definition of the default is a conflict, and only
		// when the first came from the operator: a set that names the default
		// while EnvStartCommand is unset is simply the operator spelling their
		// default in the one variable that can also carry the others.
		if name == DefaultStartCommandName && defaultSet {
			return StartCommands{}, fmt.Errorf("%s and %s both set the %q start command; set it in one of them; refusing to start",
				EnvStartCommand, EnvStartCommands, DefaultStartCommandName)
		}

		command = strings.TrimSpace(command)
		if err := validateStartCommand(EnvStartCommands, name, command); err != nil {
			return StartCommands{}, err
		}

		named[name] = true
		byName[name] = command
	}

	return StartCommands{byName: byName}, nil
}

// validateStartCommandName holds a name to what a name is for: an identifier a
// create carries, an operator types, and the audit trail records. Lowercase
// letters, digits and hyphens only, so a name in a record cannot be mistaken for
// anything else and cannot carry a byte a journal reader has to escape.
//
// The variable is named by the caller because two of them carry a name now: the
// set that defines them and EnvRemoteControlCommand, which chooses one.
func validateStartCommandName(variable, name string) error {
	if name == "" {
		return fmt.Errorf("%s contains an entry with no name; refusing to start", variable)
	}
	if len(name) > maxStartCommandNameLen {
		return fmt.Errorf("%s contains a name longer than %d characters; refusing to start", variable, maxStartCommandNameLen)
	}
	for _, c := range name {
		if (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '-' {
			return fmt.Errorf("%s contains a name outside [a-z0-9-]; refusing to start", variable)
		}
	}
	return nil
}

// validateStartCommand is the whole of what an operator may make this daemon
// type into a session's shell.
//
// The command is typed into the shell rather than being the tmux session's own
// command (FR-018, research D3), which is unchanged by its being configurable:
// if Claude were the session's command a crash would take the session and its
// scrollback with it, and milestone 4's device-code relay needs a prompt to type
// into.
//
// It is still delivered by SendKeys and *not* through Paste, and this check is
// what keeps that safe. tmux's parser eats a trailing unescaped ";" from caller
// text before -l ever applies (research D4), so a command line carrying one would
// be delivered silently truncated — the exact failure that research exists to
// prevent. The daemon's own constant was safe because it contains no ";" at all;
// an operator's is safe because a ";" is refused here, at startup, where an
// operator is reading a startup failure rather than wondering why a session came
// up half-configured. Routing it through Paste instead would have worked too, and
// was not chosen: the start command is configuration, not caller text, and giving
// it the path that exists for hostile bytes would put the daemon's own start-up
// keystrokes into a named tmux buffer any other client on the socket can read.
//
// Control bytes go for a related reason: a newline in a command line submits it
// early, so half of one would run and the rest would be typed at whatever came
// up. There is no escaping to be had here — this string is typed at a shell, so
// what it says is what runs.
func validateStartCommand(variable, name, command string) error {
	if command == "" {
		return fmt.Errorf("%s gives the %q start command an empty command line; refusing to start", variable, name)
	}
	if strings.Contains(command, ";") {
		return fmt.Errorf("%s: the %q start command contains a %q, which tmux's parser would eat before the command was typed; refusing to start",
			variable, name, ";")
	}
	for _, c := range command {
		if unicode.IsControl(c) {
			return fmt.Errorf("%s: the %q start command contains a control character, which would submit the line before it was finished; refusing to start",
				variable, name)
		}
	}
	// An unrecognised placeholder is refused rather than typed (#58). The
	// operator who wrote it is expecting a value to be substituted; passing it
	// through as a literal would type a brace expansion at a shell and tell
	// nobody. The token is named so the fix is obvious.
	if token, found := unknownPlaceholder(command); found {
		return fmt.Errorf("%s: the %q start command contains %s, which is not a placeholder this daemon substitutes (only %s); refusing to start",
			variable, name, token, StartCommandNamePlaceholder)
	}
	return nil
}

// loadSecret returns errors that name the variable and nothing else. The value
// never appears, and neither does its length — "shorter than 32" is the
// requirement restated, not a measurement of what was supplied.
func loadSecret(getenv func(string) string) ([]byte, error) {
	v := getenv(EnvSharedSecret)
	if v == "" {
		return nil, fmt.Errorf("%s is required; refusing to start", EnvSharedSecret)
	}
	if len(v) < MinSecretBytes {
		return nil, fmt.Errorf("%s is shorter than the required %d bytes; refusing to start", EnvSharedSecret, MinSecretBytes)
	}
	return []byte(v), nil
}

func loadRoots(getenv func(string) string, warn io.Writer) ([]ApprovedRoot, error) {
	raw := getenv(EnvAllowedRoots)
	if raw == "" {
		return defaultRoot(getenv, warn)
	}

	parts := strings.Split(raw, rootListSeparator)
	roots := make([]ApprovedRoot, 0, len(parts))
	seen := make(map[string]bool, len(parts))
	for _, part := range parts {
		if part == "" {
			return nil, fmt.Errorf("%s contains an empty entry; refusing to start", EnvAllowedRoots)
		}
		root, err := resolveRoot(part)
		if err != nil {
			return nil, err
		}
		// Two spellings can resolve to one directory; keep the allowlist a set
		// so the containment check and the audit record agree on its size.
		if seen[root.Path] {
			continue
		}
		seen[root.Path] = true
		roots = append(roots, root)
	}
	return roots, nil
}

func defaultRoot(getenv func(string) string, warn io.Writer) ([]ApprovedRoot, error) {
	home := getenv(envHome)
	if home == "" {
		return nil, fmt.Errorf("%s is unset and %s is empty, so the default root cannot be determined; refusing to start", EnvAllowedRoots, envHome)
	}
	if !filepath.IsAbs(home) {
		return nil, fmt.Errorf("%s is unset and %s (%q) is not an absolute path; refusing to start", EnvAllowedRoots, envHome, home)
	}

	path := filepath.Join(home, DefaultRootName)
	if err := warnDefaultRoot(warn, path); err != nil {
		return nil, err
	}

	root, err := resolveRoot(path)
	if err != nil {
		return nil, err
	}
	root.IsDefault = true
	return []ApprovedRoot{root}, nil
}

// warnNoIdentityProvider says the dashboard admits nobody (#70).
//
// A write failure is fatal for the reason warnDefaultRoot's is: a daemon
// serving an API and a closed door, while looking entirely healthy, is exactly
// the state an operator must not be left to discover. Repeated on every start,
// not only the first — a weakened posture nobody is reminded of is one nobody
// remembers.
//
// It names both doors since M12, and the caller no longer emits it for a daemon
// with a password door: a banner that said the dashboard admits nobody beside a
// door that admits the operator would be the kind of warning people learn to
// scroll past.
func warnNoIdentityProvider(warn io.Writer) error {
	banner := fmt.Sprintf(
		"crswd: no identity provider configured (%s, %s, %s) — the API works, the dashboard admits nobody.\n"+
			"crswd: configure all three to enable the dashboard, or set %s for a daemon that is not behind Cloudflare. See docs/auth-and-sessions.md.\n",
		EnvAccessTeamDomain, EnvAccessAUD, EnvAccessAllowedEmails, EnvDashboardPassword)

	if _, err := io.WriteString(warn, banner); err != nil {
		return fmt.Errorf("emit the absent-identity-provider warning: %w", err)
	}
	return nil
}

// warnDefaultRoot emits FR-004's warning. A write failure is fatal: an
// allowlist nobody was told about is exactly what the requirement forbids.
func warnDefaultRoot(warn io.Writer, path string) error {
	const rule = "!!! ==========================================================================="
	banner := strings.Join([]string{
		rule,
		fmt.Sprintf("!!! WARNING: %s is not set; using the built-in default root:", EnvAllowedRoots),
		"!!!     " + path,
		fmt.Sprintf("!!! Set %s to choose which directories sessions may run in.", EnvAllowedRoots),
		rule,
		"",
	}, "\n")

	if _, err := io.WriteString(warn, banner); err != nil {
		return fmt.Errorf("emit the %s default-root warning: %w", EnvAllowedRoots, err)
	}
	return nil
}

// resolveRoot fails closed. A root that does not exist cannot be resolved, and
// an unresolvable root would leave the containment check comparing against a
// path that means nothing.
func resolveRoot(path string) (ApprovedRoot, error) {
	if !filepath.IsAbs(path) {
		return ApprovedRoot{}, fmt.Errorf("%s entry %q is not an absolute path; refusing to start", EnvAllowedRoots, path)
	}

	resolved, err := filepath.EvalSymlinks(filepath.Clean(path))
	if err != nil {
		return ApprovedRoot{}, fmt.Errorf("%s entry %q cannot be resolved; refusing to start: %w", EnvAllowedRoots, path, err)
	}

	info, err := os.Stat(resolved)
	if err != nil {
		return ApprovedRoot{}, fmt.Errorf("%s entry %q cannot be read; refusing to start: %w", EnvAllowedRoots, path, err)
	}
	if !info.IsDir() {
		return ApprovedRoot{}, fmt.Errorf("%s entry %q is not a directory; refusing to start", EnvAllowedRoots, path)
	}

	return ApprovedRoot{Path: resolved}, nil
}

// browserDoorAdmits reports whether layer 1 has anybody to let in: Cloudflare
// Access, or the password door beside it.
//
// One predicate rather than the same pair of comparisons written twice, for the
// reason IsSecret is one predicate — its two callers decide different things
// about the same fact (what the startup banner says, and how far the listener
// may reach), and two spellings that could drift would have a daemon warn that
// it admits nobody while binding an address only a daemon that admits somebody
// is allowed.
//
// It is deliberately not told about the development bypass. That build
// authenticates nobody, so it has nobody to admit in the sense that matters
// here, and internal/access refuses a non-loopback listener under it anyway.
func browserDoorAdmits(teamDomain string, password []byte) bool {
	return teamDomain != "" || len(password) > 0
}

// loadListen bounds where the daemon may bind (FR-005).
//
// Loopback is the default and the only address a daemon with no browser door
// may have. It stops being the *only* permitted address when layer 1 admits
// somebody: an operator on their own network, with no Cloudflare in front of
// this daemon, has to be able to reach it from the machine they are sitting at
// (M12). The bound is relaxed there and nowhere else, because the invariant it
// serves is "never reachable without authentication" rather than "never
// reachable" — a daemon whose dashboard admits nobody stays where the tunnel
// can find it and nothing else can.
//
// doorAdmits is a caller's answer rather than a Config field read here: this
// runs during the load that builds the Config, and a check that read its own
// half-built result would be checking nothing.
func loadListen(getenv func(string) string, doorAdmits bool) (string, error) {
	v := getenv(EnvListen)
	if v == "" {
		v = DefaultListen
	}

	host, port, err := net.SplitHostPort(v)
	if err != nil {
		return "", fmt.Errorf("%s %q is not a host:port address; refusing to start: %w", EnvListen, v, err)
	}

	// Said once, because the two refusals below differ only in whether loopback
	// is part of the demand, and an operator told half of it fixes the address
	// twice.
	permitted := "a loopback IP literal such as 127.0.0.1 or ::1"
	if doorAdmits {
		permitted = "an IP literal such as 127.0.0.1, ::1 or 0.0.0.0"
	}

	ip := net.ParseIP(host)
	if ip == nil {
		// A name is refused rather than resolved, under either door: /etc/hosts
		// or a resolver can point "localhost" anywhere, which would move the bind
		// without changing this value. The wildcard ":8765" arrives here too, as
		// an empty host — 0.0.0.0 is the same address said in a way that can be
		// read off the line.
		return "", fmt.Errorf("%s host %q must be %s, never a name; refusing to start", EnvListen, host, permitted)
	}
	if !ip.IsLoopback() && !doorAdmits {
		// The refusal names both doors because both are missing, and it may name
		// them: this is a startup error to a terminal, not an HTTP response, so
		// there is no stranger here to tell anything to.
		return "", fmt.Errorf(
			"%s host %q is not loopback and this daemon's dashboard admits nobody: configure %s (with %s and %s) or %s before binding off loopback, or keep the listener on loopback and reach it through the tunnel; refusing to start",
			EnvListen, host, EnvAccessTeamDomain, EnvAccessAUD, EnvAccessAllowedEmails, EnvDashboardPassword)
	}

	n, err := strconv.Atoi(port)
	if err != nil || n < 1 || n > 65535 {
		return "", fmt.Errorf("%s port %q must be a number between 1 and 65535; refusing to start", EnvListen, port)
	}

	return v, nil
}

// The three layer-1 loaders below share one discipline the rest of this file
// does not need: their errors name the variable and the defect, never the value.
// A team domain names an organisation and an allowed address names a person,
// and a startup error is written to stderr and kept in the journal forever.
//
// Each takes bypassed rather than reading a package-level switch, so that the
// relaxation is visible at the call site and impossible to reach by accident.

// loadTeamDomain normalises the single value two derivations must agree on: the
// issuer is exactly the string returned here, and the key set is fetched from
// it. It accepts a bare team hostname, which is the normal form, or a full
// origin — with http:// permitted only on loopback.
func loadTeamDomain(getenv func(string) string, bypassed bool) (string, error) {
	v := strings.TrimSpace(getenv(EnvAccessTeamDomain))
	if v == "" {
		// Optional since #70. An absent identity provider means the dashboard
		// admits nobody, not that the daemon refuses to start — requiring it
		// meant nobody without a Cloudflare account could run this at all, and
		// the API has never needed one.
		//
		// The startup warning is where an operator learns what they have: silent
		// is how a daemon ends up serving nothing and looking healthy.
		return "", nil
	}

	raw := v
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}

	// The parse error is answered rather than wrapped: url.Error carries the
	// value it failed on, which is the one thing this message may not.
	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("%s is not a usable origin; refusing to start", EnvAccessTeamDomain)
	}
	if u.User != nil || u.Host == "" || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return "", fmt.Errorf("%s must be an origin such as <team>.cloudflareaccess.com, carrying no credentials, path, query or fragment; refusing to start", EnvAccessTeamDomain)
	}

	switch u.Scheme {
	case "https":
	case "http":
		// The same rule, for the same reason, as EnvListen: a name can be pointed
		// anywhere by /etc/hosts or a resolver, so loopback has to be an IP
		// literal. The carve-out exists so the contract tests and the quickstart
		// can serve a key set they control without a synthetic CA in the trust
		// store. It is not a bypass — validation runs in full against whatever
		// that origin serves.
		if ip := net.ParseIP(u.Hostname()); ip == nil || !ip.IsLoopback() {
			return "", fmt.Errorf("%s may use http:// only on a loopback IP literal such as 127.0.0.1; refusing to start", EnvAccessTeamDomain)
		}
	default:
		return "", fmt.Errorf("%s must be an https:// origin, or http:// on loopback; refusing to start", EnvAccessTeamDomain)
	}

	// Lower-cased because DNS is case-insensitive and the issuer comparison is
	// not: an operator who capitalises the team name would otherwise configure a
	// daemon that refuses every assertion Cloudflare mints.
	return u.Scheme + "://" + strings.ToLower(u.Host), nil
}

// loadAUD enforces non-emptiness and nothing else. The value is compared for
// equality against the assertion's audience and never parsed, so pinning
// Cloudflare's current 64-hex tag format would add no safety a wrong value does
// not already have, and would break the daemon the day that format changes.
func loadAUD(getenv func(string) string, bypassed bool) (string, error) {
	v := strings.TrimSpace(getenv(EnvAccessAUD))
	if v == "" {
		// Absent is allowed here and checked as a group instead (#70): the three
		// Access variables are all-or-nothing, because a team domain without an
		// audience is a half-configured door rather than an absent one.
		return "", nil
	}
	return v, nil
}

func loadAllowedEmails(getenv func(string) string, bypassed bool) ([]string, error) {
	raw := getenv(EnvAccessAllowedEmails)
	if strings.TrimSpace(raw) == "" {
		// Absent is checked as a group instead (#70). See validateAccessGroup.
		return nil, nil
	}

	parts := strings.Split(raw, emailListSeparator)
	emails := make([]string, 0, len(parts))
	for i, part := range parts {
		address := strings.TrimSpace(part)
		if address == "" {
			return nil, fmt.Errorf("%s contains an empty entry; refusing to start", EnvAccessAllowedEmails)
		}
		// Interior whitespace is the separator typed wrong. Left to run it is not
		// a startup failure but a silent one: the address never matches, and the
		// operator is refused by their own allowlist with nothing saying why.
		if strings.ContainsFunc(address, unicode.IsSpace) {
			return nil, fmt.Errorf("%s entry %d contains whitespace; separate addresses with %q; refusing to start", EnvAccessAllowedEmails, i+1, emailListSeparator)
		}
		emails = append(emails, address)
	}
	return emails, nil
}

// loadDashboardPassword reads the password door's credential, absent when unset.
//
// Its errors name the variable and the requirement and nothing else — never the
// value, and never its actual length, for loadSecret's reason: "shorter than
// sixteen" is the rule restated, while "eleven characters" is a measurement of a
// live credential written to stderr and kept in the journal forever.
//
// Unset is not a failure. It is the daemon that has no password door, which is
// every daemon that exists today.
func loadDashboardPassword(getenv func(string) string) ([]byte, error) {
	v := getenv(EnvDashboardPassword)
	if v == "" {
		return nil, nil
	}
	if len(v) < MinDashboardPasswordLen {
		return nil, fmt.Errorf("%s is shorter than the required %d characters; refusing to start", EnvDashboardPassword, MinDashboardPasswordLen)
	}
	return []byte(v), nil
}

// loadBool reads an on/off setting, off when unset.
//
// A value that is neither is a startup failure rather than a false, which is the
// same choice loadInt makes about a number that will not parse: an operator who
// wrote `yes` meant on, and a daemon that read that as off would run with the
// thing they asked for silently absent. The value is quoted back because it is
// the operator's own word for true and nothing about it is secret — the keys
// where that is not so are the two IsSecret names, and neither is a boolean.
func loadBool(getenv func(string) string, name string) (bool, error) {
	v := strings.TrimSpace(getenv(name))
	if v == "" {
		return false, nil
	}
	on, err := strconv.ParseBool(v)
	if err != nil {
		return false, fmt.Errorf("%s %q is not true or false; refusing to start", name, v)
	}
	return on, nil
}

func loadInt(getenv func(string) string, name string, def int) (int, error) {
	v := getenv(name)
	if v == "" {
		return def, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return 0, fmt.Errorf("%s %q is not a whole number; refusing to start", name, v)
	}
	if n < 1 {
		return 0, fmt.Errorf("%s must be at least 1, got %d; refusing to start", name, n)
	}
	return n, nil
}

// loadDuration reads a Go duration, defaulting when unset (#37).
//
// A negative value is the caller's business rather than this function's — the
// lifetime ceiling allows one as NeverLifetime — so the check belongs to each
// caller, and this only refuses what cannot be parsed.
// The daemon's built-in bounds, duplicated here rather than imported: internal
// /session imports this package, so importing it back would be a cycle. They are
// the values internal/session declares, and config_test pins them equal so the
// duplication cannot drift silently.
const session_AbsoluteLifetime = 24 * time.Hour

// validateLifetimes refuses at startup what cannot be corrected at runtime (#37).
//
// A ceiling below its own default would mean every create is refused by a bound
// the operator set without meaning to — a setting that does nothing is worse
// than one that refuses, because nothing tells you it did nothing.
// validateAccessGroup enforces all-or-nothing on the identity provider (#70).
//
// None of the three means the dashboard admits nobody, which is a deployment
// this daemon supports. Some of them means an operator configured a door and got
// one detail wrong — and a daemon that started anyway would refuse every login
// while looking correctly configured, which is the worst of the three outcomes.
func validateAccessGroup(teamDomain, aud string, emails []string, bypassed bool) error {
	if bypassed {
		return nil
	}
	set := 0
	for _, present := range []bool{teamDomain != "", aud != "", len(emails) > 0} {
		if present {
			set++
		}
	}
	if set == 0 || set == 3 {
		return nil
	}
	return fmt.Errorf(
		"%s, %s and %s are all-or-nothing: configure every one to enable the dashboard, or none to run the API alone; refusing to start with %d of 3",
		EnvAccessTeamDomain, EnvAccessAUD, EnvAccessAllowedEmails, set)
}

// validateDoors enforces that this daemon has at most one browser door (M12).
//
// The whole selection, and it is deliberately small:
//
//	access_enabled  dashboard_password  layer 1
//	true            unset               Access — and the three become required
//	false / unset   set                 the password door
//	true            set                 refuse to start
//	false / unset   unset               the closed door, or Access if the three
//	                                    values are set, which is today's daemon
//
// The last row is the one the table in the plan does not name and the one every
// existing deployment is on: three Access values and no such variable, because
// the variable did not exist when they were written. Reading that as "no door"
// would take the dashboard away from a daemon whose operator changed nothing,
// and reading it as a refusal would stop that daemon starting at all. So the
// three values still select Access on their own, and access_enabled is the
// operator saying it out loud — see EnvAccessEnabled.
//
// It runs after validateAccessGroup, which is what makes teamDomain a sound
// stand-in for "Access is configured": by here the three are all set or all
// unset.
//
// The refusal is a startup error to a terminal rather than an HTTP response, so
// it may name which two settings collided. It names the *variables* and never a
// value, because one of the two is a live credential.
func validateDoors(accessEnabled bool, teamDomain string, password []byte, bypassed bool) error {
	accessConfigured := accessEnabled || teamDomain != ""

	if len(password) > 0 && accessConfigured {
		// Whichever of the two the operator used to configure Access, so the
		// message names the line they can go and look at.
		named := EnvAccessTeamDomain
		if accessEnabled {
			named = EnvAccessEnabled
		}
		return fmt.Errorf(
			"%s and %s each configure the browser door and this daemon has one: unset whichever you did not mean; refusing to start rather than choosing for you",
			named, EnvDashboardPassword)
	}

	// An operator who said Access is the door and configured no Access is asking
	// for a dashboard that admits nobody. Unstated, that is the supported
	// deployment the warning covers; stated, it is a mistake, and a daemon that
	// started anyway would look configured and refuse every login.
	if accessEnabled && teamDomain == "" && !bypassed {
		return fmt.Errorf(
			"%s is on and %s, %s and %s are not set: configure all three, or turn it off to run the API alone or a %s door; refusing to start",
			EnvAccessEnabled, EnvAccessTeamDomain, EnvAccessAUD, EnvAccessAllowedEmails, EnvDashboardPassword)
	}
	return nil
}

func validateLifetimes(lifetime, lifetimeMax time.Duration) error {
	switch {
	case lifetime <= 0:
		// The *default* has no "never", and that asymmetry is deliberate: this
		// is what every session gets without asking, and a daemon whose every
		// session is immortal by default is not what a per-session override is
		// for. Where "never" is spelled is the ceiling, which opens the door,
		// and the create, which walks through it.
		return fmt.Errorf("%s must be positive, got %s; every session gets this one without asking, so %s is where a lifetime with no end is allowed and a create is what asks for it", EnvSessionLifetime, lifetime, EnvSessionLifetimeMax)
	case lifetimeMax >= 0 && lifetimeMax < lifetime:
		// Skipped for a ceiling that is not there (NeverLifetime, carried as a
		// negative): no default sits above "no bound", and the arithmetic says
		// the opposite.
		return fmt.Errorf("%s (%s) is below %s (%s); every create would be refused by a ceiling under its own default", EnvSessionLifetimeMax, lifetimeMax, EnvSessionLifetime, lifetime)
	}
	return nil
}

func loadDuration(getenv func(string) string, name string, def time.Duration) (time.Duration, error) {
	v := strings.TrimSpace(getenv(name))
	if v == "" {
		return def, nil
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return 0, fmt.Errorf("%s %q is not a duration such as 90m or 4h; refusing to start", name, v)
	}
	return d, nil
}

// loadLifetimeCeiling reads the one duration in this file that has a spelling
// for no bound at all (NeverLifetime, milestone 13).
//
// It carries that value as a negative, which is how the session record spells
// the same absence — so the manager reads one rule and not a translation. The
// word is this loader's alone, and a negative typed into a configuration file is
// refused here rather than accepted: two spellings of "never" is how one of them
// becomes the one nobody documented, and a lone `-` is a keystroke away from a
// duration somebody meant to be positive.
//
// noCeiling rather than the shortest negative duration for the reason the word
// exists: a value read by eye in a log line should say what it is.
func loadLifetimeCeiling(getenv func(string) string, name string, def time.Duration) (time.Duration, error) {
	v := strings.TrimSpace(getenv(name))
	if strings.EqualFold(v, NeverLifetime) {
		return noCeiling, nil
	}
	d, err := loadDuration(getenv, name, def)
	if err != nil {
		return 0, err
	}
	if d < 0 {
		return 0, fmt.Errorf("%s %q is negative; a ceiling with no upper bound is spelled %q, which is the one value here that cannot be read as a duration somebody meant to be positive; refusing to start", name, v, NeverLifetime)
	}
	return d, nil
}

// noCeiling is NeverLifetime loaded: a negative, because that is what a session
// record already means by "this bound is off", and one hour of it so that a
// configuration dumped for support reads as -1h0m0s rather than as -1ns.
const noCeiling = -time.Hour

func loadInt64(getenv func(string) string, name string, def int64) (int64, error) {
	v := getenv(name)
	if v == "" {
		return def, nil
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%s %q is not a whole number; refusing to start", name, v)
	}
	if n < 1 {
		return 0, fmt.Errorf("%s must be at least 1, got %d; refusing to start", name, n)
	}
	return n, nil
}
