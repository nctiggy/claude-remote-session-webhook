// Package config loads the daemon's whole configuration from the process
// environment, once, before anything binds or spawns.
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
	"strconv"
	"strings"
	"unicode"
)

// The environment is the only configuration surface (FR-001). Named as
// constants so an error message and the variable it blames cannot drift.
//
// gosec G101 fires on EnvSharedSecret because the identifier says "secret" and
// the value is a string literal. The value is the *name* of an environment
// variable and is meant to be published — .env.example carries it verbatim. The
// secret itself only ever exists as the []byte returned by loadSecret.
const (
	EnvSharedSecret     = "CRSW_SHARED_SECRET" //nolint:gosec // G101: an env var name, not a credential
	EnvAllowedRoots     = "CRSW_ALLOWED_ROOTS"
	EnvListen           = "CRSW_LISTEN"
	EnvMaxSessions      = "CRSW_MAX_SESSIONS"
	EnvCreateRatePerMin = "CRSW_CREATE_RATE_PER_MIN"
	EnvMaxBodyBytes     = "CRSW_MAX_BODY_BYTES"

	// Layer 1 — the Cloudflare Access assertion the browser door validates.
	// Required, and fatal when absent, for the same reason the shared secret is:
	// a daemon that cannot verify who the browser is has no browser door at all.
	EnvAccessTeamDomain    = "CRSW_ACCESS_TEAM_DOMAIN"
	EnvAccessAUD           = "CRSW_ACCESS_AUD"
	EnvAccessAllowedEmails = "CRSW_ACCESS_ALLOWED_EMAILS"

	EnvMaxStreams = "CRSW_MAX_STREAMS"

	envHome = "HOME"

	// rootListSeparator is fixed at ":" rather than os.PathListSeparator: the
	// spec says colon, the daemon is a systemd/tmux service, and a separator
	// that changes with the build platform would change what an allowlist means.
	rootListSeparator = ":"

	// emailListSeparator is "," because that is what the spec fixes and because
	// an address cannot carry one outside a quoted local part, which no Google
	// identity has.
	emailListSeparator = ","
)

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

// Config is the validated configuration. Every field is safe to use as-is;
// nothing here needs a second check on the request path.
type Config struct {
	// SharedSecret is at least MinSecretBytes long. It must never be logged,
	// returned in an error, or echoed back — not even its length (FR-043).
	SharedSecret []byte

	Roots            []ApprovedRoot
	Listen           string
	MaxSessions      int
	CreateRatePerMin int
	MaxBodyBytes     int64

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
}

// String redacts the shared secret so that formatting a Config — in a log line,
// a panic, or a hastily added debug print — cannot leak it. GoString does the
// same for %#v.
//
// The allowed addresses are counted rather than named: they are a list of real
// people, and this string is written wherever a Config is formatted.
func (c Config) String() string {
	return fmt.Sprintf("config{shared_secret:<redacted> roots:%v listen:%q max_sessions:%d create_rate_per_min:%d max_body_bytes:%d access_team_domain:%q access_aud:%q allowed_emails:%d max_streams:%d}",
		c.Roots, c.Listen, c.MaxSessions, c.CreateRatePerMin, c.MaxBodyBytes,
		c.AccessTeamDomain, c.AccessAUD, len(c.AccessAllowedEmails), c.MaxStreams)
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

// Load reads the configuration from the real environment, writing any startup
// warning to stderr. It is the only form cmd/crswd needs.
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
	}, nil
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
		if bypassed {
			return "", nil
		}
		return "", fmt.Errorf("%s is required; refusing to start", EnvAccessTeamDomain)
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
		if bypassed {
			return "", nil
		}
		return "", fmt.Errorf("%s is required; refusing to start", EnvAccessAUD)
	}
	return v, nil
}

func loadAllowedEmails(getenv func(string) string, bypassed bool) ([]string, error) {
	raw := getenv(EnvAccessAllowedEmails)
	if strings.TrimSpace(raw) == "" {
		if bypassed {
			return nil, nil
		}
		return nil, fmt.Errorf("%s is required and must name at least one address; refusing to start", EnvAccessAllowedEmails)
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
