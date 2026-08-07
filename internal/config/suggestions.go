package config

import "slices"

// SuggestedWorkDirs is everything the create form's working-directory picker
// offers: the union of the three sources in
// contracts/directory-suggestions.md, deduplicated and sorted (FR-006 … FR-010).
//
// The approved roots are always in it, and that is the fix rather than a
// convenience. Discovery was the only source until now and it is off by
// default, so the picker on a default install had nothing to offer and rendered
// a plain field — the operator who reported the feature as missing was right in
// every way that matters. The roots are the one source guaranteed non-empty
// whenever a session can be created at all, since a daemon with no root does not
// start, and a root is a legitimate working directory in its own right rather
// than a placeholder for one.
//
// They also disclose nothing. Every path here was configured by the operator
// this page is rendered for, and is already on every card in the fleet as a
// working directory. Their *children* are the opposite — enumerating them reads
// the host filesystem, which is exactly what DiscoverRoots exists to keep
// opt-in, so this unions DiscoveredWorkDirs rather than walking anything itself
// and the gate stays the one place it has always been. A fix for a
// discoverability bug must not quietly become a disclosure.
//
// **A suggestion is never an authorisation.** Nothing here is validated and
// nothing here needs to be: the datalist submits an ordinary string, so a chosen
// path and a typed one are indistinguishable to the create route and both meet
// session.ResolveWorkDir — the same allowlist check, the same uniform refusal,
// the same audit record (FR-009). A path in this list grants nothing, and a path
// absent from it is still acceptable typed (FR-008). That is why a configured
// suggestion outside the roots is offered and then refused on submit: the list
// is presentation, Roots is the control, and the refusal names the real rule.
//
// Sorted rather than kept in source order, because a union of three sources has
// no order of its own to keep — and the alternative a dedup reaches for first,
// ranging a map, would make the page differ between renders for a host that had
// not changed. Deduplicated because the same directory twice is a picker where
// an operator cannot tell which of the two they chose; a root that is also
// written in WorkdirSuggestions is one suggestion.
func (c Config) SuggestedWorkDirs() []string {
	union := make([]string, 0, len(c.Roots)+len(c.WorkdirSuggestions))
	for _, root := range c.Roots {
		union = append(union, root.Path)
	}
	union = append(union, c.WorkdirSuggestions...)
	union = append(union, c.DiscoveredWorkDirs()...)

	slices.Sort(union)
	return slices.Compact(union)
}
