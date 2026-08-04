// A test about the build rather than about behaviour, and deliberately carrying
// no build tag: FR-041's property is that the shipping *artifact* has no bypass,
// and a check that ran only in the build carrying one would be asserting it of
// the wrong artifact.
//
// internal/access/bypass_build_test.go makes these claims about the package that
// defines the bypass. This makes them about the command that would have to wire
// it in, which is the other place the exclusion can be lost — a flag here
// reaching a validator built there is the backdoor, and neither file sees it
// alone.
package main

import (
	"go/ast"
	"go/build"
	"go/parser"
	"go/token"
	"slices"
	"strings"
	"testing"
)

// devFlagName is the flag the development build defines, spelled here so the
// shipping build can be searched for it. It is the one string that turns a
// build-tag exclusion into a switch if it ever appears in an untagged file.
const devFlagName = "dev-auth-bypass"

// contextWithTags replaces the build tags outright rather than appending to
// them, so this file says the same thing whichever tags its own test binary was
// built with.
func contextWithTags(tags ...string) build.Context {
	ctxt := build.Default
	ctxt.BuildTags = tags
	return ctxt
}

func importCommand(t *testing.T, ctxt build.Context) *build.Package {
	t.Helper()

	pkg, err := ctxt.ImportDir(".", 0)
	if err != nil {
		t.Fatalf("read cmd/crswd with build tags %q: %v", ctxt.BuildTags, err)
	}
	return pkg
}

// TestShippingCommandExcludesTheBypassFile is the pair of files doing its job:
// the development half is in the repository and is never compiled without
// -tags dev, and the shipping half is never compiled with it.
func TestShippingCommandExcludesTheBypassFile(t *testing.T) {
	t.Parallel()

	const devOnlyFile = "bypass_dev.go"

	shipping := importCommand(t, contextWithTags())
	if slices.Contains(shipping.GoFiles, devOnlyFile) {
		t.Fatalf("%s is compiled into the default build; the bypass wiring must be excluded by its build tag", devOnlyFile)
	}
	if !slices.Contains(shipping.IgnoredGoFiles, devOnlyFile) {
		t.Fatalf("%s is not among the files the default build ignores: %q", devOnlyFile, shipping.IgnoredGoFiles)
	}
	if !slices.Contains(shipping.GoFiles, "bypass_prod.go") {
		t.Fatal("bypass_prod.go is not in the shipping build, so the daemon it starts is not the one this file describes")
	}

	// The other half, so that a mistyped tag no context satisfies fails here
	// rather than being discovered by a developer whose --dev-auth-bypass is
	// rejected by a build that was supposed to define it.
	development := importCommand(t, contextWithTags("dev"))
	if !slices.Contains(development.GoFiles, "bypass_dev.go") {
		t.Fatal("bypass_dev.go is not compiled with -tags dev, so the flag exists nowhere and US5 has no artifact")
	}
	if slices.Contains(development.GoFiles, "bypass_prod.go") {
		t.Fatal("bypass_prod.go is compiled alongside the bypass; the two halves must exclude each other")
	}
}

// TestShippingCommandDeclaresNoBypass keeps the exclusion meaningful.
//
// A build tag stops one file compiling; it does not stop a later change adding a
// --dev-auth-bypass flag to main.go behind a neutral-looking variable, which
// would be the "defaulted off" switch FR-041 forbids. So every file the shipping
// build compiles is parsed, and neither its declared names nor its string
// literals may name the bypass.
func TestShippingCommandDeclaresNoBypass(t *testing.T) {
	t.Parallel()

	fset := token.NewFileSet()
	for _, name := range importCommand(t, contextWithTags()).GoFiles {
		file, err := parser.ParseFile(fset, name, nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}

		for _, declared := range declaredNames(file) {
			if strings.Contains(strings.ToLower(declared), "bypass") {
				t.Errorf("%s declares %q; the shipping command must carry no bypass at all", name, declared)
			}
		}

		// The literal as well as the name, because a flag is defined by the
		// string a caller types and not by the variable it is stored in.
		ast.Inspect(file, func(n ast.Node) bool {
			lit, ok := n.(*ast.BasicLit)
			if ok && lit.Kind == token.STRING && strings.Contains(lit.Value, devFlagName) {
				t.Errorf("%s carries the literal %s; the shipping build must not name the development flag", name, lit.Value)
			}
			return true
		})
	}
}

// declaredNames is every top-level name a file introduces: functions and
// methods, types, constants and variables.
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
