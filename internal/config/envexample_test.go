package config_test

// .env.example is the only list an operator gets of what this daemon reads, and
// it is the one file in the repo whose whole job is to name variables while
// carrying none of their values. Both properties rot silently: a variable added
// to config.go is a variable nobody is told about, and a value pasted in here is
// a value this public repository publishes forever. Neither failure shows up in
// a test run of the package it belongs to, so it is checked here instead of
// being left to review (Constitution Principle V).
//
// This file was written before this test existed and had drifted badly — it
// named seven variables the daemon has never read and omitted three it does.

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strconv"
	"strings"
	"testing"
)

const (
	envExamplePath = "../../.env.example"

	// configSourcePath is parsed rather than having its constants restated here,
	// so a variable added to it fails this test whether or not it was exported —
	// a third hand-maintained copy of the list is exactly the drift being caught.
	configSourcePath = "config.go"

	envPrefix = "CRSW_"
)

// documentedVar is one assignment line in .env.example, with the two things
// worth asserting about it besides its name.
type documentedVar struct {
	name string
	// value is everything right of the first "=" and must always be empty.
	value string
	// described records that the line immediately above was a comment, which is
	// the only form a description can take in this file.
	described bool
}

// declaredVars returns every CRSW_ environment variable name config.go declares.
func declaredVars(t *testing.T) map[string]bool {
	t.Helper()

	file, err := parser.ParseFile(token.NewFileSet(), configSourcePath, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", configSourcePath, err)
	}

	names := make(map[string]bool)
	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.CONST {
			continue
		}
		for _, spec := range gen.Specs {
			value, ok := spec.(*ast.ValueSpec)
			if !ok {
				continue
			}
			for _, expr := range value.Values {
				lit, ok := expr.(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					continue
				}
				unquoted, err := strconv.Unquote(lit.Value)
				if err != nil {
					continue
				}
				if strings.HasPrefix(unquoted, envPrefix) {
					names[unquoted] = true
				}
			}
		}
	}

	if len(names) == 0 {
		t.Fatalf("found no %s constants in %s; this test is not checking anything", envPrefix, configSourcePath)
	}
	return names
}

// documentedVars reads the assignment lines of .env.example. Comments are the
// file's descriptions and are deliberately not scanned for names: a variable
// mentioned in prose is not one an operator can set by copying this file.
func documentedVars(t *testing.T) []documentedVar {
	t.Helper()

	raw, err := os.ReadFile(envExamplePath)
	if err != nil {
		t.Fatalf("read %s: %v", envExamplePath, err)
	}

	var vars []documentedVar
	precededByComment := false
	for i, line := range strings.Split(string(raw), "\n") {
		trimmed := strings.TrimSpace(line)
		switch {
		case trimmed == "":
			precededByComment = false
		case strings.HasPrefix(trimmed, "#"):
			precededByComment = true
		default:
			name, value, found := strings.Cut(trimmed, "=")
			if !found {
				t.Fatalf("%s:%d is neither blank, a comment, nor a NAME=value assignment: %q", envExamplePath, i+1, trimmed)
			}
			vars = append(vars, documentedVar{
				name:      strings.TrimSpace(name),
				value:     value,
				described: precededByComment,
			})
			precededByComment = false
		}
	}
	return vars
}

func TestEnvExampleNamesEveryVariable(t *testing.T) {
	t.Parallel()

	declared := declaredVars(t)
	documented := make(map[string]bool)
	for _, v := range documentedVars(t) {
		if documented[v.name] {
			t.Errorf("%s assigns %s twice", envExamplePath, v.name)
		}
		documented[v.name] = true
	}

	for name := range declared {
		if !documented[name] {
			t.Errorf("%s reads %s and %s never names it, so no operator learns it exists", configSourcePath, name, envExamplePath)
		}
	}
	for name := range documented {
		if !declared[name] {
			t.Errorf("%s tells an operator to set %s and nothing in %s reads it", envExamplePath, name, configSourcePath)
		}
	}
}

// TestEnvExampleCarriesNoValues is the committed-secret guard. gitleaks
// allowlists this path precisely because it is meant to hold no values, so the
// allowlist is only safe while that stays true.
func TestEnvExampleCarriesNoValues(t *testing.T) {
	t.Parallel()

	for _, v := range documentedVars(t) {
		if strings.TrimSpace(v.value) != "" {
			t.Errorf("%s assigns %s a value; this file is committed and public, so it carries names and descriptions only", envExamplePath, v.name)
		}
	}
}

// TestEnvExampleDescribesEveryVariable holds the half of the rule that a name
// list alone does not: a bare name tells an operator nothing about what happens
// when the value is wrong, and every one of these refuses to start.
func TestEnvExampleDescribesEveryVariable(t *testing.T) {
	t.Parallel()

	for _, v := range documentedVars(t) {
		if !v.described {
			t.Errorf("%s assigns %s with no comment line immediately above it to describe it", envExamplePath, v.name)
		}
	}
}
