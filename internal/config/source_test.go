package config_test

// The four words below are what an operator reads in the settings page's source
// column to decide where to make a change that will actually take effect. The
// behavioural half of this test is trivial and the structural half is not: a
// fifth layer added to the shim is a word the page has never heard of, and no
// assertion about the four that exist can notice a fifth arriving. So the
// vocabulary is restated here as literals, and the package is walked for any
// Source constant it does not account for.

import (
	"go/ast"
	"go/token"
	"strings"
	"testing"

	"github.com/nctiggy/claude-remote-session-webhook/internal/config"
)

// sourceTypeName is the type whose constants the settings page renders.
const sourceTypeName = "Source"

// sourceVocabulary is the whole of the source column, written out rather than
// derived from the package under test — a list read out of the answer agrees
// with the answer by construction, and a renamed word has to be catchable here.
var sourceVocabulary = []struct {
	name   string // as the package declares it
	source config.Source
	word   string // as the settings page prints it
}{
	{"SourceDefault", config.SourceDefault, "default"},
	{"SourceFile", config.SourceFile, "file"},
	{"SourceEnv", config.SourceEnv, "environment"},
	{"SourceFlag", config.SourceFlag, "flag"},
}

func TestSourceStringsAreTheSettingsPageVocabulary(t *testing.T) {
	t.Parallel()

	for _, tc := range sourceVocabulary {
		if got := tc.source.String(); got != tc.word {
			t.Errorf("%s.String() = %q, want %q: the settings page prints this word and an operator acts on it", tc.name, got, tc.word)
		}
	}

	// Provenance is a map keyed by environment-variable name, so a key nothing
	// supplied answers with the zero value. That has to be the layer meaning
	// "nothing supplied it", or an unrecorded key would claim to have come from
	// a file the operator never wrote.
	if config.SourceDefault != 0 {
		t.Errorf("SourceDefault = %d, want 0: an unrecorded key reads the zero value and must not report a layer it never came from", config.SourceDefault)
	}

	// A Source with no word of its own must not borrow one of these four. It
	// renders as its number instead, which is unmistakably a bug rather than an
	// answer to "where did this value come from?".
	unknown := config.SourceFlag + 1
	for _, tc := range sourceVocabulary {
		if got := unknown.String(); got == tc.word {
			t.Errorf("the Source one past SourceFlag renders as %q, which is %s's word: an unnamed layer must not read as a named one", got, tc.name)
		}
	}

	// The half that catches a fifth source. Adding one to the shim without
	// giving the page a word for it is invisible to every assertion above.
	fset, files := packageFiles(t)
	declared := sourceConstants(files)
	if len(declared) == 0 {
		t.Fatalf("no constant of type %s was found in this package, so the walk below is checking nothing; if the constants moved, sourceConstants has to move with them", sourceTypeName)
	}

	known := make(map[string]bool, len(sourceVocabulary))
	for _, tc := range sourceVocabulary {
		known[tc.name] = true
	}
	seen := make(map[string]bool, len(declared))
	for _, decl := range declared {
		seen[decl.name] = true
		if !known[decl.name] {
			t.Errorf("%s: %s is a configuration source the settings page has no word for. Add it to sourceVocabulary here and to %s.String(), or the source column silently gains a fifth meaning",
				fset.Position(decl.pos), decl.name, sourceTypeName)
		}
	}
	for _, tc := range sourceVocabulary {
		if !seen[tc.name] {
			t.Errorf("%s is asserted here but this package declares no such constant: the page's vocabulary and the shim's layers have drifted apart", tc.name)
		}
	}
}

// sourceConst is a declared configuration source, positioned so a failure names
// the line to go and look at.
type sourceConst struct {
	name string
	pos  token.Pos
}

// sourceConstants finds every constant in the package that is a configuration
// source.
//
// Two ways of spelling one, because the walk has to survive the next author's
// habits rather than the current author's: a constant typed Source — including
// the untyped continuations of an iota run, which carry the type of the spec
// that opened it — and a constant merely *named* Source-something, which catches
// `SourceExtra = Source(4)` in a block of its own. The name test costs a false
// positive on an unrelated Source-prefixed constant, which this package does not
// have and which would fail loudly and legibly if it ever did.
func sourceConstants(files map[string]*ast.File) []sourceConst {
	var found []sourceConst
	for _, file := range files {
		for _, decl := range file.Decls {
			gen, ok := decl.(*ast.GenDecl)
			if !ok || gen.Tok != token.CONST {
				continue
			}
			// An iota run is one spec with a type followed by specs with
			// neither a type nor a value; those inherit. A spec that supplies
			// its own value ends the inheritance.
			inRun := false
			for _, spec := range gen.Specs {
				value, ok := spec.(*ast.ValueSpec)
				if !ok {
					continue
				}
				switch {
				case value.Type != nil:
					ident, isIdent := value.Type.(*ast.Ident)
					inRun = isIdent && ident.Name == sourceTypeName
				case len(value.Values) > 0:
					inRun = false
				}
				for _, name := range value.Names {
					if inRun || strings.HasPrefix(name.Name, sourceTypeName) {
						found = append(found, sourceConst{name: name.Name, pos: name.Pos()})
					}
				}
			}
		}
	}
	return found
}
