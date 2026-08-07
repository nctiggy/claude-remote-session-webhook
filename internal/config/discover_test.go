package config_test

// The discovery walk behind CRSW_DISCOVER_ROOTS (T023, FR-041): what the create
// form's picker offers when an operator asks it to read the host, and — the half
// that matters — what it may not offer even then.

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/nctiggy/claude-remote-session-webhook/internal/config"
)

// The fixture is built on the real filesystem because the thing under test is a
// question about the real filesystem: no fake reproduces EvalSymlinks walking a
// link out of the directory it sits in. Laid out to put every case the walk has
// to answer beside the ordinary directory it must not be confused with.
//
//	base/code                  an approved root
//	base/code/repo             a subdirectory — what a picker is for
//	base/code/repo/nested      a grandchild, one level too deep
//	base/code/notes.txt        a regular file inside a root
//	base/code/inside-link  ->  base/code/repo
//	base/code/escape-link  ->  base/outside
//	base/code/dangling     ->  base/outside/never-created
//	base/second                a second approved root
//	base/second/work
//	base/outside               NOT approved
//	base/outside/repo
type discoveryFixture struct {
	base    string
	root    string
	second  string
	outside string
}

func newDiscoveryFixture(t *testing.T) discoveryFixture {
	t.Helper()

	// Resolved, because config resolves every root at startup and the walk
	// compares two already-canonical paths. On a host where the temp directory
	// is itself a symlink, an unresolved root would make every case here fail
	// for a reason that has nothing to do with the code under test.
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("resolve the temp dir: %v", err)
	}

	f := discoveryFixture{
		base:    base,
		root:    filepath.Join(base, "code"),
		second:  filepath.Join(base, "second"),
		outside: filepath.Join(base, "outside"),
	}

	for _, dir := range []string{f.nested(), f.work(), filepath.Join(f.outside, "repo")} {
		if err := os.MkdirAll(dir, 0o750); err != nil {
			t.Fatalf("create %s: %v", dir, err)
		}
	}
	if err := os.WriteFile(f.file(), []byte("not a directory\n"), 0o600); err != nil {
		t.Fatalf("create %s: %v", f.file(), err)
	}
	for _, link := range []struct{ target, name string }{
		{f.repo(), f.insideLink()},
		{f.outside, f.escapeLink()},
		{filepath.Join(f.outside, "never-created"), f.danglingLink()},
	} {
		if err := os.Symlink(link.target, link.name); err != nil {
			t.Fatalf("link %s: %v", link.name, err)
		}
	}
	return f
}

func (f discoveryFixture) repo() string         { return filepath.Join(f.root, "repo") }
func (f discoveryFixture) nested() string       { return filepath.Join(f.repo(), "nested") }
func (f discoveryFixture) work() string         { return filepath.Join(f.second, "work") }
func (f discoveryFixture) file() string         { return filepath.Join(f.root, "notes.txt") }
func (f discoveryFixture) insideLink() string   { return filepath.Join(f.root, "inside-link") }
func (f discoveryFixture) escapeLink() string   { return filepath.Join(f.root, "escape-link") }
func (f discoveryFixture) danglingLink() string { return filepath.Join(f.root, "dangling") }

// on is the fixture as a daemon whose operator asked for discovery, which is the
// only configuration in which any of this runs at all.
func (f discoveryFixture) on() config.Config {
	return config.Config{
		Roots:         []config.ApprovedRoot{{Path: f.root}, {Path: f.second}},
		DiscoverRoots: true,
	}
}

// TestDiscoveryOffByDefault is the FR-041 default, and it is asserted through
// LoadFrom rather than against a struct literal because "off by default" is a
// claim about what a daemon nobody configured does, not about a zero value.
//
// The second half is what stops the first from being vacuous: the same fixture
// with the variable set finds the directory, so a walk that was broken outright
// could not pass this by discovering nothing.
func TestDiscoveryOffByDefault(t *testing.T) {
	t.Parallel()

	pairs, root := baseEnv(t)
	if err := os.Mkdir(filepath.Join(root, "repo"), 0o750); err != nil {
		t.Fatalf("create a discoverable directory: %v", err)
	}

	cfg := mustLoad(t, pairs)
	if cfg.DiscoverRoots {
		t.Errorf("%s is unset and the daemon discovers anyway; listing a filesystem is a disclosure an operator opts into", config.EnvDiscoverRoots)
	}
	if found := cfg.DiscoveredWorkDirs(); len(found) != 0 {
		t.Errorf("a daemon configured with no %s offered %v; the picker reads the host only when asked to", config.EnvDiscoverRoots, found)
	}

	pairs[config.EnvDiscoverRoots] = "true"
	asked := mustLoad(t, pairs)
	if !asked.DiscoverRoots {
		t.Fatalf("%s = true and the daemon did not read it, so the setting has no loader and the operator's answer changes nothing", config.EnvDiscoverRoots)
	}
	// Against the resolved root rather than the temp directory the environment
	// named: what the walk answers with is what config resolved at startup.
	want := filepath.Join(asked.Roots[0].Path, "repo")
	if found := asked.DiscoveredWorkDirs(); !slices.Contains(found, want) {
		t.Errorf("with %s = true the walk offered %v, and %q is a directory one level under the approved root", config.EnvDiscoverRoots, found, want)
	}
}

// TestDiscoveryListsOneLevel is the bound on the walk itself. A picker wants the
// repository, not the tree inside it, and a walk that descended would be an
// unbounded read of the host on every render.
func TestDiscoveryListsOneLevel(t *testing.T) {
	t.Parallel()

	f := newDiscoveryFixture(t)
	found := f.on().DiscoveredWorkDirs()

	// Every root, in the operator's own order, each one's entries in ReadDir's
	// order. A walk that stopped at the first root would leave an operator's
	// second root looking like a refusal they have no way to explain.
	want := []string{f.repo(), f.work()}
	if !slices.Equal(found, want) {
		t.Errorf("DiscoveredWorkDirs() = %v, want %v", found, want)
	}
	if slices.Contains(found, f.nested()) {
		t.Errorf("DiscoveredWorkDirs() offered %q, which is two levels below the root; the walk does not recurse:\n%v", f.nested(), found)
	}
	if slices.Contains(found, f.file()) {
		t.Errorf("DiscoveredWorkDirs() offered the regular file %q, which is not a directory a session could run in:\n%v", f.file(), found)
	}
}

// TestDiscoveryNeverLeavesRoots is the security claim of the walk, and the
// fixture is what makes it worth asserting: the root holds a link pointing out
// of it, a dangling one, and a link pointing back inside.
//
// The escaping link is the case this exists for. It sits inside an approved root,
// so a list built from directory *entries* would render it as though it were
// inside — a suggestion the create route is certain to refuse, naming a directory
// above the roots to make it. Deciding on the resolution instead is what makes
// that impossible rather than unlikely.
func TestDiscoveryNeverLeavesRoots(t *testing.T) {
	t.Parallel()

	f := newDiscoveryFixture(t)
	found := f.on().DiscoveredWorkDirs()

	for _, refused := range []string{
		f.escapeLink(),                   // inside a root by spelling, outside it once resolved
		f.outside,                        // what it resolves to
		filepath.Join(f.outside, "repo"), // and anything under that
		f.danglingLink(),                 // resolves to nothing at all
	} {
		if slices.Contains(found, refused) {
			t.Errorf("DiscoveredWorkDirs() offered %q, which is not under an approved root:\n%v", refused, found)
		}
	}

	// The other half of deciding on the resolution: a link that points back
	// inside its own root is kept, and kept once. The same directory twice is a
	// picker where an operator cannot tell which of the two they chose.
	if got := slices.Contains(found, f.repo()); !got {
		t.Errorf("DiscoveredWorkDirs() dropped %q; a link is resolved, not refused for being a link:\n%v", f.repo(), found)
	}
	if got := len(found); got != 2 {
		t.Errorf("DiscoveredWorkDirs() = %v (%d entries); the link and the directory it points at are one suggestion", found, got)
	}
}
