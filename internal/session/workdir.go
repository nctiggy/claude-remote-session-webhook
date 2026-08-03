package session

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/nctiggy/claude-remote-session-webhook/internal/config"
)

// pathSeparator is the boundary the containment check is written against,
// spelled once so the two places that need it cannot drift apart.
const pathSeparator = string(filepath.Separator)

var (
	// ErrInvalidWorkDir wraps every rejection, so a handler answering 400 has
	// exactly one thing to branch on (contracts/http-api.md).
	ErrInvalidWorkDir = errors.New("invalid working directory")

	// ErrWorkDirNotAbsolute marks a path with no meaning of its own. The
	// daemon's own working directory is whatever the systemd unit was given, so
	// resolving a caller's relative path against it would make the allowlist
	// depend on where the service happened to start.
	ErrWorkDirNotAbsolute = errors.New("a working directory must be an absolute path")

	// ErrWorkDirUnresolvable is the fail-closed answer: the path does not
	// exist, a component of it does not, a symlink in it dangles, or the
	// daemon cannot traverse it. FR-028 refuses rather than creates, so an
	// absent directory is a rejection and never a mkdir.
	ErrWorkDirUnresolvable = errors.New("the path does not exist or cannot be resolved")

	// ErrWorkDirNotDirectory separates "a file under an approved root" from the
	// containment answer, because the two want different fixes.
	ErrWorkDirNotDirectory = errors.New("the path is not a directory")

	// ErrWorkDirOutsideRoots is the one that matters. It is a sentinel of its
	// own so the separator-boundary rule below is observable from a test: a
	// string-prefix check would admit /home/u/codeEVIL against /home/u/code and
	// still return an error for everything the other cases cover.
	ErrWorkDirOutsideRoots = errors.New("the path is not under an approved root")
)

// ResolveWorkDir turns a caller-supplied working directory into the canonical
// path a session may actually run in, or refuses it (FR-028).
//
// The order is the security property, not a style choice. A path is cleaned,
// then fully resolved through every symlink, and only the *result* is tested
// for containment — checking containment against the caller's spelling would
// accept a link that sits inside an approved root and points anywhere on the
// host. Resolution fails closed: a path that cannot be resolved is refused, so
// a session never starts in a directory the daemon could not see.
//
// The roots are consumed exactly as config resolved them at startup and are
// deliberately not re-resolved here (data-model.md). A root that is itself a
// symlink must not be re-read on the request path, where a swap between the
// check and the spawn is a race a caller could win. The cost of that rule is
// that a caller-supplied root spelling would fail *closed*, which is the
// direction it should fail in.
//
// This is a create-time check and cannot be anything more. Nothing stops the
// directory from being moved or replaced after it is returned; the answer to
// that is the bounded lifetime and verified teardown of Principle VI, not a
// second stat here that would race the same way.
func ResolveWorkDir(path string, roots []config.ApprovedRoot) (string, error) {
	if path == "" {
		return "", fmt.Errorf("%w: a working directory is required", ErrInvalidWorkDir)
	}
	if !filepath.IsAbs(path) {
		return "", fmt.Errorf("%w: %w", ErrInvalidWorkDir, ErrWorkDirNotAbsolute)
	}

	// The underlying error is dropped on purpose, here and below: os.PathError
	// carries the caller's path, and this error is on its way to an audit
	// record and a log line. A path is caller-controlled text — echoing it back
	// would put arbitrary bytes, newlines included, into the record. config
	// does interpolate its paths because those come from the operator's
	// environment at startup; these come off the wire.
	resolved, err := filepath.EvalSymlinks(filepath.Clean(path))
	if err != nil {
		return "", fmt.Errorf("%w: %w", ErrInvalidWorkDir, ErrWorkDirUnresolvable)
	}

	// Containment before the directory check, so that the reason reaching the
	// audit trail for a path outside the allowlist is the escape and not an
	// incidental fact about what was found there.
	if !underAnyRoot(resolved, roots) {
		return "", fmt.Errorf("%w: %w", ErrInvalidWorkDir, ErrWorkDirOutsideRoots)
	}

	info, err := os.Stat(resolved)
	if err != nil {
		return "", fmt.Errorf("%w: %w", ErrInvalidWorkDir, ErrWorkDirUnresolvable)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("%w: %w", ErrInvalidWorkDir, ErrWorkDirNotDirectory)
	}

	return resolved, nil
}

// underAnyRoot reports whether an already-resolved path is inside the
// allowlist. An empty list contains nothing, which is the answer that fails
// closed.
func underAnyRoot(path string, roots []config.ApprovedRoot) bool {
	for _, root := range roots {
		if underRoot(path, root.Path) {
			return true
		}
	}
	return false
}

// underRoot is the separator-boundary check, and it is why ApprovedRoot exists
// as a type rather than as a strings.HasPrefix call (data-model.md).
//
// Both arguments must already be cleaned and symlink-resolved; this is string
// work over two canonical paths, not a filesystem question.
func underRoot(path, root string) bool {
	if path == root {
		return true
	}
	// The trailing separator is the whole point: "/home/u/codeEVIL" carries
	// "/home/u/code" as a *string* prefix and is not under it. Trimming first
	// keeps the degenerate root "/" meaningful — it is the one cleaned path
	// that already ends in a separator, and "//" would be a prefix of nothing.
	return strings.HasPrefix(path, strings.TrimSuffix(root, pathSeparator)+pathSeparator)
}
