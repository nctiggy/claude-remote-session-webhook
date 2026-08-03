package session

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nctiggy/claude-remote-session-webhook/internal/config"
)

// The fixture is built on the real filesystem because the thing under test is
// a question about the real filesystem: no fake reproduces EvalSymlinks
// walking a link in a parent component. Laid out to put the trap in the task
// list — a sibling whose name carries the root's name as a string prefix —
// side by side with the root it must not be confused for.
//
//	base/code                 the approved root
//	base/code/repo            an ordinary working directory
//	base/code/repo/nested
//	base/code/repo/file.txt   a regular file inside the root
//	base/code/inside-link  -> base/code/repo
//	base/code/escape-link  -> base/outside
//	base/code/dangling     -> base/outside/never-created
//	base/codeEVIL             NOT approved, and HasPrefix says otherwise
//	base/codeEVIL/repo
//	base/outside              NOT approved
//	base/outside/repo
//	base/second               a second approved root
//	base/second/repo
type workDirFixture struct {
	base    string
	root    string
	evil    string
	outside string
	second  string
}

func newWorkDirFixture(t *testing.T) workDirFixture {
	t.Helper()

	// The temp dir is resolved because config.ApprovedRoot.Path promises a path
	// with every symlink already gone, and ResolveWorkDir takes that promise at
	// its word. A test that handed it an unresolved root would be testing a
	// misconfiguration; there is a case below that does exactly that, on purpose.
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("resolve the temp dir: %v", err)
	}

	f := workDirFixture{
		base:    base,
		root:    filepath.Join(base, "code"),
		evil:    filepath.Join(base, "codeEVIL"),
		outside: filepath.Join(base, "outside"),
		second:  filepath.Join(base, "second"),
	}

	for _, dir := range []string{
		filepath.Join(f.root, "repo", "nested"),
		filepath.Join(f.evil, "repo"),
		filepath.Join(f.outside, "repo"),
		filepath.Join(f.second, "repo"),
	} {
		if err := os.MkdirAll(dir, 0o750); err != nil {
			t.Fatalf("create %s: %v", dir, err)
		}
	}

	for _, path := range []string{f.file(), f.outsideFile()} {
		if err := os.WriteFile(path, []byte("not a directory\n"), 0o600); err != nil {
			t.Fatalf("create %s: %v", path, err)
		}
	}

	for _, link := range []struct{ target, name string }{
		{filepath.Join(f.root, "repo"), f.insideLink()},
		{f.outside, f.escapeLink()},
		{filepath.Join(f.outside, "never-created"), f.danglingLink()},
	} {
		if err := os.Symlink(link.target, link.name); err != nil {
			t.Fatalf("link %s: %v", link.name, err)
		}
	}

	return f
}

func (f workDirFixture) file() string         { return filepath.Join(f.root, "repo", "file.txt") }
func (f workDirFixture) outsideFile() string  { return filepath.Join(f.outside, "file.txt") }
func (f workDirFixture) insideLink() string   { return filepath.Join(f.root, "inside-link") }
func (f workDirFixture) escapeLink() string   { return filepath.Join(f.root, "escape-link") }
func (f workDirFixture) danglingLink() string { return filepath.Join(f.root, "dangling") }

func (f workDirFixture) roots() []config.ApprovedRoot {
	return []config.ApprovedRoot{{Path: f.root}, {Path: f.second}}
}

// everyWorkDirReason is what makes the per-case expectation an assertion rather
// than a formality: each rejection must match its own sentinel and none of the
// others, so an implementation that wrapped all four at once would fail.
var everyWorkDirReason = []error{
	ErrWorkDirNotAbsolute,
	ErrWorkDirUnresolvable,
	ErrWorkDirNotDirectory,
	ErrWorkDirOutsideRoots,
}

func TestResolveWorkDirAcceptsDirectoriesUnderAnApprovedRoot(t *testing.T) {
	t.Parallel()

	f := newWorkDirFixture(t)

	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "the root itself", in: f.root, want: f.root},
		{name: "a child of the root", in: filepath.Join(f.root, "repo"), want: filepath.Join(f.root, "repo")},
		{
			name: "a grandchild of the root",
			in:   filepath.Join(f.root, "repo", "nested"),
			want: filepath.Join(f.root, "repo", "nested"),
		},
		{
			// Clean is textual, so a traversal that comes back down inside the
			// root is an ordinary path and is accepted. The rule is where the
			// path lands, never what it was spelled with.
			name: "a traversal that returns inside the root",
			in:   f.root + "/repo/../repo",
			want: filepath.Join(f.root, "repo"),
		},
		{
			name: "a traversal above the root and back down",
			in:   f.root + "/../code/repo",
			want: filepath.Join(f.root, "repo"),
		},
		{name: "an uncleaned dot component", in: f.root + "/./repo", want: filepath.Join(f.root, "repo")},
		{name: "a trailing separator", in: f.root + "/repo/", want: filepath.Join(f.root, "repo")},
		{name: "a doubled separator", in: f.root + "//repo", want: filepath.Join(f.root, "repo")},
		{
			// A link is not refused for being a link. It is resolved, and the
			// resolution is what gets checked — which is the same rule that
			// refuses escape-link below.
			name: "a symlink pointing inside the same root",
			in:   f.insideLink(),
			want: filepath.Join(f.root, "repo"),
		},
		{
			name: "a path under the second approved root",
			in:   filepath.Join(f.second, "repo"),
			want: filepath.Join(f.second, "repo"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := ResolveWorkDir(tt.in, f.roots())
			if err != nil {
				t.Fatalf("ResolveWorkDir(%q) = %v, want nil", tt.in, err)
			}
			// The returned path, not the caller's spelling, is what reaches
			// tmux -c. Asserting the value is what stops a future change from
			// validating one path and starting a session in another.
			if got != tt.want {
				t.Errorf("ResolveWorkDir(%q) = %q, want %q", tt.in, got, tt.want)
			}
			if !filepath.IsAbs(got) || got != filepath.Clean(got) {
				t.Errorf("ResolveWorkDir(%q) = %q, want an absolute cleaned path", tt.in, got)
			}
		})
	}
}

func TestResolveWorkDirRejectsHostilePaths(t *testing.T) {
	t.Parallel()

	f := newWorkDirFixture(t)

	tests := []struct {
		name string
		in   string
		want error // the one reason this case must report; nil for none in particular
	}{
		{name: "empty", in: ""},

		// Relative paths. There is no base to resolve them against that a
		// caller could reason about, so they are refused rather than joined to
		// whatever directory the unit was started in.
		{name: "a bare relative name", in: "repo", want: ErrWorkDirNotAbsolute},
		{name: "a relative traversal", in: "../../etc", want: ErrWorkDirNotAbsolute},
		{name: "a bare dot", in: ".", want: ErrWorkDirNotAbsolute},
		{name: "a bare double dot", in: "..", want: ErrWorkDirNotAbsolute},
		{name: "a tilde", in: "~/code", want: ErrWorkDirNotAbsolute},

		// Absolute, resolvable, and outside the allowlist.
		{name: "the parent of the approved root", in: f.base, want: ErrWorkDirOutsideRoots},
		{name: "an absolute traversal out of the root", in: f.root + "/repo/../..", want: ErrWorkDirOutsideRoots},
		{name: "an unapproved sibling", in: f.outside, want: ErrWorkDirOutsideRoots},
		{name: "a child of an unapproved sibling", in: filepath.Join(f.outside, "repo"), want: ErrWorkDirOutsideRoots},
		{name: "the root of the filesystem", in: string(filepath.Separator), want: ErrWorkDirOutsideRoots},

		// The trap the task names: HasPrefix says yes, the filesystem says no.
		{name: "a sibling whose name extends the root's", in: f.evil, want: ErrWorkDirOutsideRoots},
		{
			name: "a child of a sibling whose name extends the root's",
			in:   filepath.Join(f.evil, "repo"),
			want: ErrWorkDirOutsideRoots,
		},

		// Links. Each of these is inside an approved root by spelling, which is
		// exactly why the check runs on the resolution instead.
		{name: "a symlink inside a root pointing outside", in: f.escapeLink(), want: ErrWorkDirOutsideRoots},
		{
			name: "a path through a symlinked parent pointing outside",
			in:   filepath.Join(f.escapeLink(), "repo"),
			want: ErrWorkDirOutsideRoots,
		},
		{name: "a dangling symlink", in: f.danglingLink(), want: ErrWorkDirUnresolvable},

		// Fail closed on anything that cannot be resolved. FR-028 refuses a
		// missing directory; it never creates one.
		{name: "a path that does not exist", in: filepath.Join(f.root, "nope"), want: ErrWorkDirUnresolvable},
		{
			name: "a path whose parent does not exist",
			in:   filepath.Join(f.root, "nope", "deeper"),
			want: ErrWorkDirUnresolvable,
		},
		{
			// The syscall layer refuses this one; the point of the case is that
			// it refuses rather than truncating at the NUL and accepting the
			// prefix, which is how this bug reads in other languages.
			name: "a NUL byte in the path",
			in:   f.root + "\x00/repo",
			want: ErrWorkDirUnresolvable,
		},

		{name: "a regular file under an approved root", in: f.file(), want: ErrWorkDirNotDirectory},
		{
			// Both rules apply, and the escape is the one that must reach the
			// audit trail. This is the case that pins the order of the two
			// checks; without it the ordering comment in workdir.go is prose.
			name: "a regular file outside the approved roots",
			in:   f.outsideFile(),
			want: ErrWorkDirOutsideRoots,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := ResolveWorkDir(tt.in, f.roots())
			if err == nil {
				t.Fatalf("ResolveWorkDir(%q) = %q, nil, want an error", tt.in, got)
			}
			if got != "" {
				t.Errorf("ResolveWorkDir(%q) returned %q alongside an error; a refused path must yield nothing usable", tt.in, got)
			}
			// One sentinel for the handler to branch on, whatever the reason.
			if !errors.Is(err, ErrInvalidWorkDir) {
				t.Errorf("ResolveWorkDir(%q) error = %v, want one wrapping ErrInvalidWorkDir", tt.in, err)
			}
			for _, reason := range everyWorkDirReason {
				if got := errors.Is(err, reason); got != (reason == tt.want) {
					t.Errorf("ResolveWorkDir(%q) errors.Is(%v) = %t, want %t (error: %v)",
						tt.in, reason, got, reason == tt.want, err)
				}
			}
		})
	}
}

func TestUnderRootChecksAtASeparatorBoundary(t *testing.T) {
	t.Parallel()

	// Written with the literal paths data-model.md uses, so the rule it states
	// is asserted in its own words and not only through a temp directory whose
	// name changes every run.
	tests := []struct {
		path string
		root string
		want bool
	}{
		{path: "/home/u/code", root: "/home/u/code", want: true},
		{path: "/home/u/code/repo", root: "/home/u/code", want: true},
		{path: "/home/u/code/repo/nested/deeper", root: "/home/u/code", want: true},

		// The prefix trap, in the four spellings that would each slip past a
		// bare HasPrefix.
		{path: "/home/u/codeEVIL", root: "/home/u/code"},
		{path: "/home/u/codeEVIL/repo", root: "/home/u/code"},
		{path: "/home/u/code-evil", root: "/home/u/code"},
		{path: "/home/u/code2", root: "/home/u/code"},

		// Neither is the other's prefix, and a parent is not a child.
		{path: "/home/u/cod", root: "/home/u/code"},
		{path: "/home/u", root: "/home/u/code"},
		{path: "/", root: "/home/u/code"},
		{path: "/var/tmp", root: "/home/u/code"},

		// The degenerate root. "/" is the one cleaned path that already ends in
		// a separator, so appending another would match nothing at all.
		{path: "/", root: "/", want: true},
		{path: "/etc", root: "/", want: true},
		{path: "/home/u/code", root: "/", want: true},
	}

	for _, tt := range tests {
		if got := underRoot(tt.path, tt.root); got != tt.want {
			t.Errorf("underRoot(%q, %q) = %t, want %t", tt.path, tt.root, got, tt.want)
		}
	}
}

func TestResolveWorkDirRefusesEverythingWithoutRoots(t *testing.T) {
	t.Parallel()

	f := newWorkDirFixture(t)

	// An empty allowlist permits nothing. The alternative reading — an empty
	// list meaning "unrestricted" — is the failure mode Principle VI exists to
	// rule out, and config cannot be the only thing standing between the two.
	for _, roots := range [][]config.ApprovedRoot{nil, {}} {
		in := filepath.Join(f.root, "repo")
		got, err := ResolveWorkDir(in, roots)
		if err == nil {
			t.Fatalf("ResolveWorkDir(%q, %v) = %q, nil, want an error", in, roots, got)
		}
		if !errors.Is(err, ErrWorkDirOutsideRoots) {
			t.Errorf("ResolveWorkDir(%q, %v) error = %v, want one wrapping ErrWorkDirOutsideRoots", in, roots, err)
		}
	}
}

func TestResolveWorkDirFailsClosedOnAnUnresolvedRoot(t *testing.T) {
	t.Parallel()

	f := newWorkDirFixture(t)

	// config resolves the roots at startup and ResolveWorkDir deliberately does
	// not repeat the work, so a root that still carries a symlink is a broken
	// precondition. This pins the direction it breaks in: a legitimate path
	// under that root is refused, never a path outside one admitted.
	rootViaLink := filepath.Join(f.base, "root-link")
	if err := os.Symlink(f.root, rootViaLink); err != nil {
		t.Fatalf("link %s: %v", rootViaLink, err)
	}

	in := filepath.Join(rootViaLink, "repo")
	got, err := ResolveWorkDir(in, []config.ApprovedRoot{{Path: rootViaLink}})
	if err == nil {
		t.Fatalf("ResolveWorkDir(%q) = %q, nil, want a refusal — an unresolved root must fail closed", in, got)
	}
	if !errors.Is(err, ErrWorkDirOutsideRoots) {
		t.Errorf("ResolveWorkDir(%q) error = %v, want one wrapping ErrWorkDirOutsideRoots", in, err)
	}
}

func TestResolveWorkDirKeepsThePathOutOfTheError(t *testing.T) {
	t.Parallel()

	f := newWorkDirFixture(t)

	// A refused path is caller-supplied text on its way to a log line and an
	// audit record. os.PathError carries the path it failed on, so wrapping the
	// filesystem error verbatim — the obvious thing to do — is what this
	// forbids: it would let a caller write arbitrary bytes, newlines included,
	// into the audit trail by naming a directory.
	const canary = "canary-do-not-echo"

	paths := []string{
		canary,
		"../" + canary,
		filepath.Join(f.root, canary),
		filepath.Join(f.root, canary, "deeper"),
		filepath.Join(f.outside, canary),
		filepath.Join(f.evil, canary),
		f.root + "\x00" + canary,
		strings.Repeat("/"+canary, 40),
	}

	for _, in := range paths {
		_, err := ResolveWorkDir(in, f.roots())
		if err == nil {
			t.Fatalf("ResolveWorkDir(%q) = nil, want an error", in)
		}
		if strings.Contains(err.Error(), canary) {
			t.Errorf("ResolveWorkDir(%q) error %q echoes the path it refused", in, err)
		}
	}
}
