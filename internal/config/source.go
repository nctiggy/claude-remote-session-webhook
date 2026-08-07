package config

import "strconv"

// Source names which layer supplied a configuration value.
//
// It exists because a value alone cannot answer the question an operator
// actually asks — "why did my edit do nothing?" A value present and equal in two
// layers is indistinguishable by comparison, and that is precisely the case
// where the question is being asked. So provenance is recorded by the precedence
// shim as it decides, never inferred afterwards from what the values look like
// (research R4). The settings page reads it, and nothing else does.
type Source uint8

// The four layers, lowest precedence first. The chain is
// flag > environment > file > default (contracts/config-precedence.md).
//
// SourceDefault is deliberately the zero value: provenance is held in a map
// keyed by environment-variable name, and a key nothing ever supplied answers a
// lookup with "nothing supplied it" rather than with a layer it never came from.
const (
	SourceDefault Source = iota // nothing supplied it; the built-in default stands
	SourceFile
	SourceEnv
	SourceFlag
)

// String is the settings page's source column, and these four words are its
// whole vocabulary — an operator reads them to decide where to make a change
// that will actually take effect.
//
// "environment" is spelled out where the constant is abbreviated, because the
// column is prose for a person and CRSW_LISTEN is not "env" to anyone who did
// not write this package.
//
// A Source outside the four renders as its number rather than as a word. That
// case is unreachable today and is meant to stay ugly: a fifth layer that
// reaches this method without being given a word here would otherwise blend into
// the column as one of the four, and a value's origin is the one thing this page
// exists to state.
func (s Source) String() string {
	switch s {
	case SourceDefault:
		return "default"
	case SourceFile:
		return "file"
	case SourceEnv:
		return "environment"
	case SourceFlag:
		return "flag"
	default:
		return "Source(" + strconv.Itoa(int(s)) + ")"
	}
}
