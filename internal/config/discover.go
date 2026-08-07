package config

import (
	"io/fs"
	"os"
	"path/filepath"
)

// maxDiscoveredWorkDirs bounds what one walk may return.
//
// It bounds markup and work alike, and neither half is taste. Every candidate
// costs a symlink resolution and a stat, and this runs on the render path — a
// root with a thousand directories under it must not turn one dashboard into a
// thousand filesystem calls, nor a thousand elements a browser has to filter.
//
// The bound is affordable because the list is a convenience and never the only
// way through: the field stays free text, so a directory past the cap is typed
// exactly as every directory was before the picker existed (FR-040).
const maxDiscoveredWorkDirs = 200

// DiscoveredWorkDirs is the working directories the create form suggests: the
// subdirectories one level below each approved root (#59, FR-041).
//
// **Off unless the operator turned it on.** The gate is inside rather than at
// the call site on purpose — a caller that forgot it would be a page reading the
// host filesystem in a deployment that never asked for one, and there is no way
// to reach the walk without going past this line.
//
// It is a convenience and grants nothing. A path here is a string in a
// <datalist>, submitted like any other, and the create route resolves and checks
// it against the allowlist exactly as it does one that was typed — same refusal,
// same audit record (FR-042). That is what makes the list safe: it reaches no
// decision at all. A path absent from it is still acceptable typed, and a path
// present in it is still refused when the allowlist says so.
//
// The walk is one level and does not recurse. What an operator wants at the
// working-directory field is the repository, not the tree inside it, and a walk
// that descended would be an unbounded read of the host on every render.
//
// Everything it offers has been resolved through its symlinks and re-checked
// against the root it was found in, so a link sitting inside a root and pointing
// out of one is dropped rather than rendered as though it were inside. That is
// the only claim this list makes about the filesystem, and it is made about the
// *resolved* path — checking the spelling instead is how a picker ends up naming
// a directory above the roots.
//
// Order is the roots in the operator's own order, each root's entries in the
// order ReadDir returns them, which is sorted by name. Two renders of an
// unchanged host therefore produce the same page.
//
// It reads the filesystem on every render rather than caching at startup,
// because a list of what is on the host has one honest reading and it is the one
// taken now. A directory cloned this morning is offered this afternoon without a
// restart, and a stale cache would be a page describing a host that has moved.
func (c Config) DiscoveredWorkDirs() []string {
	if !c.DiscoverRoots {
		return nil
	}

	var found []string
	seen := make(map[string]bool)
	for _, root := range c.Roots {
		// A root that cannot be listed is dropped in silence, and it has to be:
		// this is a form, not a place an operator learns about the state of their
		// own filesystem, and a page that explained why a directory is missing
		// would be a page reporting on paths the allowlist says nothing about.
		// It costs them the suggestions, never the field.
		entries, err := os.ReadDir(root.Path)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if len(found) == maxDiscoveredWorkDirs {
				return found
			}
			// Neither a directory nor a symlink can resolve to a directory, so
			// the resolution is skipped rather than spent. A symlink is kept as a
			// candidate precisely because it may point out of the root, which is
			// what childOf is here to notice.
			if !entry.IsDir() && entry.Type()&fs.ModeSymlink == 0 {
				continue
			}
			path, ok := childOf(root.Path, entry.Name())
			if !ok || seen[path] {
				continue
			}
			seen[path] = true
			found = append(found, path)
		}
	}
	return found
}

// childOf resolves one directory entry and reports whether what it resolves to
// is still a directory one level below the root it was found in.
//
// The equality is the containment check, and for a one-level walk it is the
// whole of it: "under this root" and "a direct child of this root" are the same
// question here, asked of the resolved path, so this package does not grow a
// second copy of the separator-boundary rule that internal/session owns for the
// create-time check — a boundary spelled twice is a boundary that can disagree
// with itself, and session imports this package, so it cannot be spelled once
// here and called from there.
//
// It is stricter than that rule rather than looser, which is the direction it
// must err in: a link inside one root pointing into *another* approved root
// resolves to a child of neither and is not offered, even though a create would
// accept it. The cost of that is a suggestion an operator types instead.
//
// The root must already be absolute, cleaned and symlink-resolved, which is what
// resolveRoot guarantees of every ApprovedRoot at startup. An unresolved root
// matches nothing and fails closed.
func childOf(root, name string) (string, bool) {
	resolved, err := filepath.EvalSymlinks(filepath.Join(root, name))
	if err != nil {
		return "", false
	}
	if filepath.Dir(resolved) != root {
		return "", false
	}
	info, err := os.Stat(resolved)
	if err != nil || !info.IsDir() {
		return "", false
	}
	return resolved, true
}
