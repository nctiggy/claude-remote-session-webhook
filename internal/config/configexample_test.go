package config_test

// config.example is the only list an operator gets of what this daemon reads,
// and since #65 it is also the file they copy to ~/.config/crswd/config rather
// than a description of one. Both of its properties rot silently: a setting
// added to config.go is a setting nobody is told about, and a value pasted in
// here is a value this public repository publishes forever. Neither failure
// shows up in a test run of the package it belongs to, so it is checked here
// instead of being left to review (Constitution Principle V).
//
// The strongest assertion in this file is the last one: the example must load.
// A file the daemon refuses to parse would be a file the release ships (#57) and
// every operator's first startup failure.

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/nctiggy/claude-remote-session-webhook/internal/config"
)

const (
	configExamplePath = "../../config.example"

	// configSourcePath is parsed rather than having its constants restated here,
	// so a variable added to it fails this test whether or not it was exported —
	// a third hand-maintained copy of the list is exactly the drift being caught.
	configSourcePath = "config.go"

	envPrefix = "CRSW_"

	// schemaVersionKey is the one key in the example that is not a setting, so
	// it is exempt from the name check on both sides.
	schemaVersionKey = "version"
)

// documentedKey is one assignment line in config.example, with the two things
// worth asserting about it besides its name.
type documentedKey struct {
	name string
	// value is everything right of the first "=" and must always be empty,
	// except for the schema version, which is not a secret and is the one thing
	// an example of this format has to demonstrate.
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

// documentedKeys reads the assignment lines of config.example. Comments are the
// file's descriptions and are deliberately not scanned for names: a key
// mentioned in prose is not one an operator can set by copying this file.
func documentedKeys(t *testing.T) []documentedKey {
	t.Helper()

	raw, err := os.ReadFile(configExamplePath)
	if err != nil {
		t.Fatalf("read %s: %v", configExamplePath, err)
	}

	var keys []documentedKey
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
				t.Fatalf("%s:%d is neither blank, a comment, nor a `key = value` assignment", configExamplePath, i+1)
			}
			keys = append(keys, documentedKey{
				name:      strings.TrimSpace(name),
				value:     value,
				described: precededByComment,
			})
			precededByComment = false
		}
	}
	return keys
}

func TestConfigExampleNamesEveryKey(t *testing.T) {
	t.Parallel()

	documented := make(map[string]bool)
	for _, k := range documentedKeys(t) {
		if documented[k.name] {
			t.Errorf("%s assigns %s twice", configExamplePath, k.name)
		}
		documented[k.name] = true
	}

	for name := range declaredVars(t) {
		if key := config.KeyForVar(name); !documented[key] {
			t.Errorf("%s reads %s and %s never names `%s`, so no operator learns it exists",
				configSourcePath, name, configExamplePath, key)
		}
	}
	for key := range documented {
		if key == schemaVersionKey {
			continue
		}
		if !declaredVars(t)[config.VarForKey(key)] {
			t.Errorf("%s tells an operator to set `%s` and nothing in %s reads it", configExamplePath, key, configSourcePath)
		}
	}
}

// TestConfigExampleCarriesNoValues is the committed-secret guard. gitleaks
// allowlists this path precisely because it is meant to hold no values, so the
// allowlist is only safe while that stays true.
func TestConfigExampleCarriesNoValues(t *testing.T) {
	t.Parallel()

	for _, k := range documentedKeys(t) {
		if k.name == schemaVersionKey {
			continue
		}
		if strings.TrimSpace(k.value) != "" {
			t.Errorf("%s assigns `%s` a value; this file is committed and public, so it carries names and descriptions only", configExamplePath, k.name)
		}
	}
}

// TestConfigExampleDescribesEveryKey holds the half of the rule that a name list
// alone does not: a bare name tells an operator nothing about what happens when
// the value is wrong, and every one of these refuses to start.
func TestConfigExampleDescribesEveryKey(t *testing.T) {
	t.Parallel()

	for _, k := range documentedKeys(t) {
		if !k.described {
			t.Errorf("%s assigns `%s` with no comment line immediately above it to describe it", configExamplePath, k.name)
		}
	}
}

// The assertion that makes the rest worth having: the file an operator is told
// to copy is a file this daemon can read. Copied verbatim it sets nothing, so
// the daemon it produces is the daemon defaults and variables produce — which is
// exactly what a file full of empty assignments has to mean.
func TestConfigExampleLoads(t *testing.T) {
	t.Parallel()

	raw, err := os.ReadFile(configExamplePath)
	if err != nil {
		t.Fatalf("read %s: %v", configExamplePath, err)
	}
	path := filepath.Join(t.TempDir(), "config")
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	root := t.TempDir()
	cfg, err := config.LoadFrom(env(map[string]string{
		config.EnvSharedSecret: goodSecret,
		config.EnvAllowedRoots: root,
	}), io.Discard, config.WithFile(path))
	if err != nil {
		t.Fatalf("the file operators are told to copy does not load: %v", err)
	}

	// Empty is unset, so every default still stands. An example that quietly
	// changed a bound by being copied would be worse than none.
	if cfg.Listen != config.DefaultListen || cfg.MaxSessions != config.DefaultMaxSessions {
		t.Errorf("copying the example changed a default: listen=%q max_sessions=%d", cfg.Listen, cfg.MaxSessions)
	}
}
