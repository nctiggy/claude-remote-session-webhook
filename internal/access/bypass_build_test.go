// A test about the build rather than about behaviour, and deliberately the one
// file of the three with no build tag on it: FR-041's property is that the
// shipping artifact does not contain the bypass, and a check that only ran in
// the build carrying the bypass would be asserting it of the wrong artifact.
//
// It reads the package's own source directory through go/build in an explicitly
// constructed context, so it says the same thing whichever tags the test binary
// was built with. SC-012 asks for a check that fails if the bypass leaks into
// the shipping build; this is that check.
package access

import (
	"go/ast"
	"go/build"
	"go/parser"
	"go/token"
	"slices"
	"strings"
	"testing"
)

// contextWithTags is the default build context with its tags replaced outright
// rather than appended to. Appending would inherit whatever the test binary was
// built with, which is exactly the thing this file must not depend on.
func contextWithTags(tags ...string) build.Context {
	ctxt := build.Default
	ctxt.BuildTags = tags
	return ctxt
}

func importPackage(t *testing.T, ctxt build.Context) *build.Package {
	t.Helper()

	pkg, err := ctxt.ImportDir(".", 0)
	if err != nil {
		t.Fatalf("read this package with build tags %q: %v", ctxt.BuildTags, err)
	}
	return pkg
}

// TestShippingBuildExcludesTheBypassFile is FR-041's literal claim: excluded at
// build time, not defaulted off. The file exists in the repository — it is in
// IgnoredGoFiles, which is how this test tells "excluded" from "deleted" — and
// the compiler never sees it without -tags dev.
func TestShippingBuildExcludesTheBypassFile(t *testing.T) {
	t.Parallel()

	// Named around gosec's G101 rather than for its own sake: the pattern for a
	// hardcoded credential matches "pass", which "bypass" contains, and a
	// filename in a test is not worth a //nolint the way bypassEmail is.
	const devOnlyFile = "bypass_dev.go"

	shipping := importPackage(t, contextWithTags())
	if slices.Contains(shipping.GoFiles, devOnlyFile) {
		t.Fatalf("%s is compiled into the default build; the bypass must be excluded by its build tag", devOnlyFile)
	}
	if !slices.Contains(shipping.IgnoredGoFiles, devOnlyFile) {
		t.Fatalf("%s is not among the files the default build ignores: %q", devOnlyFile, shipping.IgnoredGoFiles)
	}

	// The other half, so that a build tag mistyped into something no context
	// ever satisfies fails here rather than being discovered by a developer
	// whose bypass silently does nothing.
	development := importPackage(t, contextWithTags("dev"))
	if !slices.Contains(development.GoFiles, devOnlyFile) {
		t.Fatalf("%s is not compiled with -tags dev, so the development bypass exists nowhere", devOnlyFile)
	}
	if slices.Contains(development.GoFiles, "bypass_prod.go") {
		t.Fatal("bypass_prod.go is compiled alongside the bypass; the two halves must exclude each other")
	}
}

// TestShippingBuildDeclaresNoBypassSymbol is the check that keeps the exclusion
// meaningful.
//
// A build tag on one file stops that file compiling; it does not stop a later
// change adding a bypassed() method to verify.go, or a BypassActive field to the
// validator, and either would be the "defaulted off" switch FR-041 forbids
// dressed as ordinary code. So every file the shipping build does compile is
// parsed, and nothing it declares may be bypass-shaped.
func TestShippingBuildDeclaresNoBypassSymbol(t *testing.T) {
	t.Parallel()

	fset := token.NewFileSet()
	for _, name := range importPackage(t, contextWithTags()).GoFiles {
		file, err := parser.ParseFile(fset, name, nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		for _, declared := range declaredNames(file) {
			if strings.Contains(strings.ToLower(declared), "bypass") {
				t.Fatalf("%s declares %q; the shipping build must carry no bypass at all", name, declared)
			}
		}
	}
}

// TestShippingHalfOfThePairDeclaresNothing guards the emptiness bypass_prod.go
// is made of.
//
// It is the file a later change would reach for to make the two builds "match",
// and anything declared there is compiled into the artifact that must not have
// it. The comment at its top says this; the test is what enforces it.
func TestShippingHalfOfThePairDeclaresNothing(t *testing.T) {
	t.Parallel()

	file, err := parser.ParseFile(token.NewFileSet(), "bypass_prod.go", nil, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("parse bypass_prod.go: %v", err)
	}
	if declared := declaredNames(file); len(declared) != 0 {
		t.Fatalf("bypass_prod.go declares %q; the shipping half of the pair declares nothing", declared)
	}
}

// declaredNames is every top-level name a file introduces: functions and
// methods, types, constants and variables. A method counts under its own name,
// which is what catches a bypassed() hung off an existing type.
func declaredNames(file *ast.File) []string {
	var names []string
	for _, decl := range file.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			names = append(names, d.Name.Name)
		case *ast.GenDecl:
			for _, spec := range d.Specs {
				switch s := spec.(type) {
				case *ast.TypeSpec:
					names = append(names, s.Name.Name)
				case *ast.ValueSpec:
					for _, name := range s.Names {
						names = append(names, name.Name)
					}
				}
			}
		}
	}
	return names
}
