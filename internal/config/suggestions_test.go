package config_test

// The union behind the create form's working-directory picker (T006, FR-006 …
// FR-010): what a daemon offers, what it may only offer when asked, and that two
// renders of an unchanged host offer the same thing in the same order.

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/nctiggy/claude-remote-session-webhook/internal/config"
)

// TestRootsAreOfferedByDefault is the defect this task exists for, asserted
// through LoadFrom rather than a struct literal because "by default" is a claim
// about the daemon an operator who configured nothing else gets.
//
// **Must fail when** discovery remains the only source. That was the shipped
// state: DiscoverRoots is off unless asked for, so the picker had no source at
// all on a default install and rendered a field with nothing behind it.
//
// Two roots, because a union that stopped at the first would leave an operator's
// second root missing from a list that names their first — the same reason the
// roots *hint* renders every one of them.
func TestRootsAreOfferedByDefault(t *testing.T) {
	t.Parallel()

	pairs, root := baseEnv(t)
	second := t.TempDir()
	pairs[config.EnvAllowedRoots] = root + ":" + second

	cfg := mustLoad(t, pairs)
	want := []string{cfg.Roots[0].Path, cfg.Roots[1].Path}
	slices.Sort(want)

	// Against the resolved roots rather than the paths the environment named:
	// what the picker offers is what config resolved at startup, so an operator
	// choosing a suggestion submits the spelling ResolveWorkDir will compare.
	if got := cfg.SuggestedWorkDirs(); !slices.Equal(got, want) {
		t.Errorf("SuggestedWorkDirs() = %v, want %v; a daemon configured with nothing but its allowlist still has directories to offer, and offering none is the picker an operator reported as missing",
			got, want)
	}
}

// TestExplicitListIsOffered is the other half of T005, which loaded the key and
// left nothing consuming it.
//
// **Must fail when** the key is declared and never read — this repository's
// recurring failure, and the one CRSW_DESTROY_ON_SHUTDOWN shipped as. A union
// that ignored WorkdirSuggestions would leave every test around it green while
// the operator's own list appeared nowhere.
//
// Neither path exists and neither is under a root, which is
// contracts/directory-suggestions.md's worked example rather than an oversight:
// the list is presentation, so it reads no filesystem and consults no allowlist.
// A suggestion outside the roots is offered here and refused on submit, and that
// is the contract working.
func TestExplicitListIsOffered(t *testing.T) {
	t.Parallel()

	pairs, _ := baseEnv(t)
	pairs[config.EnvWorkdirSuggestions] = "/srv/scratch,/srv/second"

	cfg := mustLoad(t, pairs)
	got := cfg.SuggestedWorkDirs()
	for _, want := range []string{"/srv/scratch", "/srv/second"} {
		if !slices.Contains(got, want) {
			t.Errorf("SuggestedWorkDirs() = %v, and %q is configured in %s; a key nothing consumes offers the operator what they wrote nowhere",
				got, want, config.EnvWorkdirSuggestions)
		}
	}
	// The roots are still there beside them. An explicit list replacing the
	// default source rather than joining it is the same emptiness one
	// configuration step further along.
	if want := cfg.Roots[0].Path; !slices.Contains(got, want) {
		t.Errorf("SuggestedWorkDirs() = %v and dropped the approved root %q once %s was set; the three sources are a union", got, want, config.EnvWorkdirSuggestions)
	}
}

// TestSourcesAreUnionedAndDeduped is the claim that makes the three sources one
// list: a directory reachable two ways is one suggestion.
//
// **Must fail when** the sources are concatenated without dedup. A picker
// offering the same path twice is one an operator cannot choose from — they
// cannot tell which of the two they picked, and nothing on the page says the
// entries are the same directory.
//
// The fixture is the discovery walk's own, so all three sources are live at
// once: two approved roots, a walk that finds a child under each, and an
// explicit list that names one of those children again and repeats an entry of
// its own.
func TestSourcesAreUnionedAndDeduped(t *testing.T) {
	t.Parallel()

	f := newDiscoveryFixture(t)
	cfg := f.on()
	cfg.WorkdirSuggestions = []string{f.repo(), "/srv/scratch", "/srv/scratch"}

	want := []string{f.root, f.second, f.repo(), f.work(), "/srv/scratch"}
	slices.Sort(want)

	if got := cfg.SuggestedWorkDirs(); !slices.Equal(got, want) {
		t.Errorf("SuggestedWorkDirs() = %v, want %v; %q is a root and a discovered child and an explicit entry, and it is one directory",
			got, want, f.repo())
	}
}

// TestSuggestionsAreSorted is what stops the dedup from being written with a map
// and left there.
//
// **Must fail when** map iteration order reaches the markup. Two renders of a
// host that has not changed would offer the same directories in a different
// order — a page that differs from itself, in the one control an operator is
// scanning for a path they already know the shape of.
//
// The sources are deliberately written out of order and interleaved with each
// other, so a union that merely concatenated them in source order would fail
// this while a sorted one passes. Nothing here exists on disk: with discovery
// off the union reads no filesystem, which is what makes that safe to assert.
func TestSuggestionsAreSorted(t *testing.T) {
	t.Parallel()

	cfg := config.Config{
		Roots:              []config.ApprovedRoot{{Path: "/srv/zulu"}, {Path: "/srv/alpha"}},
		WorkdirSuggestions: []string{"/srv/mike", "/srv/bravo"},
	}

	want := []string{"/srv/alpha", "/srv/bravo", "/srv/mike", "/srv/zulu"}
	first := cfg.SuggestedWorkDirs()
	if !slices.Equal(first, want) {
		t.Fatalf("SuggestedWorkDirs() = %v, want %v", first, want)
	}
	if second := cfg.SuggestedWorkDirs(); !slices.Equal(second, first) {
		t.Errorf("two calls on one configuration answered %v then %v; the same host renders the same page", first, second)
	}
}

// TestDiscoveryStillOffByDefault is the line the fix for emptiness must not
// cross. The picker being empty on a default install is a usability bug; turning
// the walk on to fix it would trade an operator's privacy decision for it,
// silently, and reading their filesystem is the one thing on this path they have
// to ask for.
//
// **Must fail when** the union turns discovery on, or reaches past the
// DiscoverRoots gate to walk the roots itself.
//
// The second half is what stops the first from being vacuous: the same fixture
// with the variable set does offer the child, so a union that had simply
// dropped the discovered source could not pass this by finding nothing.
func TestDiscoveryStillOffByDefault(t *testing.T) {
	t.Parallel()

	pairs, root := baseEnv(t)
	if err := os.Mkdir(filepath.Join(root, "repo"), 0o750); err != nil {
		t.Fatalf("create a discoverable directory: %v", err)
	}

	cfg := mustLoad(t, pairs)
	offered := cfg.SuggestedWorkDirs()
	child := filepath.Join(cfg.Roots[0].Path, "repo")
	if slices.Contains(offered, child) {
		t.Errorf("SuggestedWorkDirs() offered %q with %s unset; listing what is inside a root is a disclosure an operator opts into",
			child, config.EnvDiscoverRoots)
	}
	if want := cfg.Roots[0].Path; !slices.Contains(offered, want) {
		t.Fatalf("SuggestedWorkDirs() = %v and does not name the approved root %q, so the default install has an empty picker again", offered, want)
	}

	pairs[config.EnvDiscoverRoots] = "true"
	asked := mustLoad(t, pairs)
	if got := asked.SuggestedWorkDirs(); !slices.Contains(got, child) {
		t.Errorf("with %s = true the picker offered %v, and %q is a directory one level under the approved root; the union drops the source rather than gating it",
			config.EnvDiscoverRoots, got, child)
	}
}
