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

	// EnvDestroyOnShutdown restores the pre-#63 behaviour: tear every session
	// down when the daemon stops. Default is off — a restart preserves them and
	// startup adoption reclaims them.
	EnvDestroyOnShutdown = "CRSW_DESTROY_ON_SHUTDOWN"

	// The four lifetime variables (#37). Defaults reproduce the constants the
	// daemon shipped with, so an operator who sets none of them sees no change.
	// The MAX pair are ceilings a per-session override may not exceed; without
	// them an override would be unbounded, which is the thing that must not be
	// true of a bound.
	EnvSessionLifetime    = "CRSW_SESSION_LIFETIME"
	EnvSessionLifetimeMax = "CRSW_SESSION_LIFETIME_MAX"
	EnvIdleTimeout        = "CRSW_IDLE_TIMEOUT"
	EnvIdleTimeoutMax     = "CRSW_IDLE_TIMEOUT_MAX"
	EnvCreateRatePerMin   = "CRSW_CREATE_RATE_PER_MIN"
	EnvMaxBodyBytes       = "CRSW_MAX_BODY_BYTES"

	// Layer 1 — the Cloudflare Access assertion the browser door validates.
	// Required, and fatal when absent, for the same reason the shared secret is:
	// a daemon that cannot verify who the browser is has no browser door at all.
	EnvAccessTeamDomain    = "CRSW_ACCESS_TEAM_DOMAIN"
	EnvAccessAUD           = "CRSW_ACCESS_AUD"
	EnvAccessAllowedEmails = "CRSW_ACCESS_ALLOWED_EMAILS"

	EnvMaxStreams = "CRSW_MAX_STREAMS"

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

	// DestroyOnShutdown tears every session down on a clean stop. Off by
	// default: a graceful restart is overwhelmingly the common case, and
	// destroying a fleet to redeploy a binary is a cost nobody asked for.
	DestroyOnShutdown bool

	// SessionLifetime and IdleTimeout are what a create that asks for nothing
	// gets; the Max pair are the ceilings an override is checked against (#37).
	SessionLifetime    time.Duration
	SessionLifetimeMax time.Duration
	IdleTimeout        time.Duration
	IdleTimeoutMax     time.Duration
	CreateRatePerMin   int
	MaxBodyBytes       int64

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

	// MaxStreams bounds concurrent live-output streams, which are the one thing
	// a browser can hold open indefinitely.
	MaxStreams int

	// StartCommands is the named set a create chooses from, always carrying
	// DefaultStartCommandName.
	StartCommands StartCommands

	// RemoteControlCommand is the *name* in that set which the dashboard's
	// remote-control switch turns on (#58). Empty means this daemon configures no
	// remote-control command, and the dashboard renders no switch — an operator
	// is never offered a control whose only outcome is a refusal.
	RemoteControlCommand string
}

// String redacts the shared secret so that formatting a Config — in a log line,
// a panic, or a hastily added debug print — cannot leak it. GoString does the
// same for %#v.
//
// The allowed addresses are counted rather than named: they are a list of real
// people, and this string is written wherever a Config is formatted.
func (c Config) String() string {
	return fmt.Sprintf("config{shared_secret:<redacted> roots:%v listen:%q max_sessions:%d create_rate_per_min:%d max_body_bytes:%d access_team_domain:%q access_aud:%q allowed_emails:%d max_streams:%d start_commands:%v}",
		c.Roots, c.Listen, c.MaxSessions, c.CreateRatePerMin, c.MaxBodyBytes,
		c.AccessTeamDomain, c.AccessAUD, len(c.AccessAllowedEmails), c.MaxStreams,
		c.StartCommands)
}

// GoString mirrors String, so %#v is not a way around the redaction.
func (c Config) GoString() string { return c.String() }

// Option adjusts what counts as a complete configuration. There is exactly one,
// because there is exactly one thing outside the environment that changes the
// answer.
type Option func(*loadOptions)

type loadOptions struct{ accessBypassed bool }

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
	var file *File
	if path := DefaultPath(getenv); path != "" {
		f, err := ReadFile(path, warn)
		if err != nil {
			return nil, err
		}
		file = f
	}
	getenv = withFile(getenv, file)

	secret, err := loadSecret(getenv)
	if err != nil {
		return nil, err
	}
	roots, err := loadRoots(getenv, warn)
	if err != nil {
		return nil, err
	}
	listen, err := loadListen(getenv)
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
	lifetimeMax, err := loadDuration(getenv, EnvSessionLifetimeMax, lifetime)
	if err != nil {
		return nil, err
	}
	idle, err := loadDuration(getenv, EnvIdleTimeout, session_IdleTimeout)
	if err != nil {
		return nil, err
	}
	idleMax, err := loadDuration(getenv, EnvIdleTimeoutMax, idle)
	if err != nil {
		return nil, err
	}
	if err := validateLifetimes(lifetime, lifetimeMax, idle, idleMax); err != nil {
		return nil, err
	}

	maxStreams, err := loadInt(getenv, EnvMaxStreams, DefaultMaxStreams)
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
	if teamDomain == "" && !o.accessBypassed {
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
		Listen:              listen,
		MaxSessions:         maxSessions,
		CreateRatePerMin:    createRate,
		MaxBodyBytes:        maxBody,
		AccessTeamDomain:    teamDomain,
		AccessAUD:           aud,
		AccessAllowedEmails: emails,
		MaxStreams:          maxStreams,
		SessionLifetime:     lifetime,
		SessionLifetimeMax:  lifetimeMax,
		IdleTimeout:         idle,
		IdleTimeoutMax:      idleMax,
		StartCommands:       startCommands,

		RemoteControlCommand: remoteControl,
	}, nil
}

// withFile answers from the environment first and the file second. That is the
// whole of the precedence chain (contracts/config-precedence.md), and its size
// is the design: precedence has one implementation, and no bound, default or
// refusal is written a second time behind it.
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
func withFile(getenv func(string) string, f *File) func(string) string {
	return func(name string) string {
		if v := getenv(name); v != "" {
			return v
		}
		if v, ok := f.Lookup(name); ok {
			return v
		}
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
func warnNoIdentityProvider(warn io.Writer) error {
	banner := fmt.Sprintf(
		"crswd: no identity provider configured (%s, %s, %s) — the API works, the dashboard admits nobody.\n"+
			"crswd: configure all three to enable the dashboard. See docs/auth-and-sessions.md.\n",
		EnvAccessTeamDomain, EnvAccessAUD, EnvAccessAllowedEmails)

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

// loadListen enforces the loopback bind (FR-005). Reachability comes from the
// tunnel; a listener on any other interface is the one change docs/security.md
// says is simply wrong.
func loadListen(getenv func(string) string) (string, error) {
	v := getenv(EnvListen)
	if v == "" {
		v = DefaultListen
	}

	host, port, err := net.SplitHostPort(v)
	if err != nil {
		return "", fmt.Errorf("%s %q is not a host:port address; refusing to start: %w", EnvListen, v, err)
	}

	ip := net.ParseIP(host)
	if ip == nil {
		// A name is refused rather than resolved: /etc/hosts or a resolver can
		// point "localhost" anywhere, which would move the bind off loopback
		// without changing this value.
		return "", fmt.Errorf("%s host %q must be a loopback IP literal such as 127.0.0.1 or ::1; refusing to start", EnvListen, host)
	}
	if !ip.IsLoopback() {
		return "", fmt.Errorf("%s host %q is not loopback; the daemon is reachable only through the tunnel; refusing to start", EnvListen, host)
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
// A negative value is refused rather than clamped everywhere except the idle
// timeout, where the caller allows it as the disable — so the check belongs to
// each caller and not here, and this only refuses what cannot be parsed.
// The daemon's built-in bounds, duplicated here rather than imported: internal
// /session imports this package, so importing it back would be a cycle. They are
// the values internal/session declares, and config_test pins them equal so the
// duplication cannot drift silently.
const (
	session_AbsoluteLifetime = 24 * time.Hour
	session_IdleTimeout      = 60 * time.Minute
)

// validateLifetimes refuses at startup what cannot be corrected at runtime (#37).
//
// A ceiling below its own default would mean every create is refused by a bound
// the operator set without meaning to, and an idle timeout longer than the
// session it sits in can never fire — a setting that does nothing is worse than
// one that refuses, because nothing tells you it did nothing.
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

func validateLifetimes(lifetime, lifetimeMax, idle, idleMax time.Duration) error {
	switch {
	case lifetime <= 0:
		return fmt.Errorf("%s must be positive, got %s; there is no such thing as a session that never expires", EnvSessionLifetime, lifetime)
	case lifetimeMax < lifetime:
		return fmt.Errorf("%s (%s) is below %s (%s); every create would be refused by a ceiling under its own default", EnvSessionLifetimeMax, lifetimeMax, EnvSessionLifetime, lifetime)
	case idle < 0:
		return fmt.Errorf("%s may not be negative, got %s; use 0 to disable idle reaping", EnvIdleTimeout, idle)
	case idleMax < idle:
		return fmt.Errorf("%s (%s) is below %s (%s)", EnvIdleTimeoutMax, idleMax, EnvIdleTimeout, idle)
	case idle > lifetime:
		return fmt.Errorf("%s (%s) exceeds %s (%s), so it could never fire", EnvIdleTimeout, idle, EnvSessionLifetime, lifetime)
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
