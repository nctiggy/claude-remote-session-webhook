// Package config loads the daemon's whole configuration from the process
// environment, once, before anything binds or spawns.
//
// Every check here is fail-closed on purpose. Sessions run with
// --dangerously-skip-permissions, so the allowlisted roots, the loopback bind,
// and the shared secret are not settings — they are the constraints standing in
// for the permission prompt that is gone. A value that would weaken one of them
// is a startup failure, never a warning (docs/security.md §4). The single
// exception is an unset root list, which FR-004 requires be loud rather than
// fatal.
package config

import (
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
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
	envHome             = "HOME"

	// rootListSeparator is fixed at ":" rather than os.PathListSeparator: the
	// spec says colon, the daemon is a systemd/tmux service, and a separator
	// that changes with the build platform would change what an allowlist means.
	rootListSeparator = ":"
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
}

// String redacts the shared secret so that formatting a Config — in a log line,
// a panic, or a hastily added debug print — cannot leak it. GoString does the
// same for %#v.
func (c Config) String() string {
	return fmt.Sprintf("config{shared_secret:<redacted> roots:%v listen:%q max_sessions:%d create_rate_per_min:%d max_body_bytes:%d}",
		c.Roots, c.Listen, c.MaxSessions, c.CreateRatePerMin, c.MaxBodyBytes)
}

// GoString mirrors String, so %#v is not a way around the redaction.
func (c Config) GoString() string { return c.String() }

// Load reads the configuration from the real environment, writing any startup
// warning to stderr. It is the only form cmd/crswd needs.
func Load() (*Config, error) { return LoadFrom(os.Getenv, os.Stderr) }

// LoadFrom is Load with the environment and the warning sink injected, so tests
// can drive every case in parallel without mutating the process environment.
func LoadFrom(getenv func(string) string, warn io.Writer) (*Config, error) {
	if getenv == nil {
		return nil, errors.New("no environment source provided; refusing to start")
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

	return &Config{
		SharedSecret:     secret,
		Roots:            roots,
		Listen:           listen,
		MaxSessions:      maxSessions,
		CreateRatePerMin: createRate,
		MaxBodyBytes:     maxBody,
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
