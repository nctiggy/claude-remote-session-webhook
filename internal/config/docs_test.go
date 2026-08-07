package config_test

// The two claims the repository's own README makes about configuration, checked
// here rather than left to review (Constitution Principle V).
//
// The first is SC-012: this daemon has no third-party dependency, and go.sum
// does not exist. That property is why the configuration file is `key = value`
// hand-parsed rather than YAML or TOML, so a go.sum appearing would not merely
// add a dependency — it would remove the reason the format is what it is. It was
// asserted once already, in cmd/crswd's quickstart suite, which is behind a build
// tag CI does not run: an assertion nothing runs is an assertion that has stopped
// being made.
//
// The second is the variable table. README.md is where an operator who has not
// opened .env.example or config.example learns what this daemon reads, and a
// table maintained by hand beside a list maintained by the compiler drifts in the
// silent direction — the operator who sets a variable the README invented, or who
// never learns about the bound that was added last week.
//
// declaredVars lives in envexample_test.go and parses config.go's own constants,
// so a variable renamed there fails this file too.

import (
	"bytes"
	"os"
	"regexp"
	"strings"
	"testing"
)

const (
	rootREADMEPath = "../../README.md"
	goModPath      = "../../go.mod"
	goSumPath      = "../../go.sum"
)

// TestNoDependencies is SC-012, in the default build because that is the one CI
// runs. A dependency is added by an import and a `go mod tidy`, both of which are
// ordinary-looking edits; what makes this repository's zero-dependency claim true
// is that the two files those edits produce are checked for.
func TestNoDependencies(t *testing.T) {
	t.Parallel()

	if _, err := os.Stat(goSumPath); err == nil {
		t.Errorf("%s exists, so something was imported; docs/security.md §5 and the configuration file's format both rest on its absence", goSumPath)
	} else if !os.IsNotExist(err) {
		t.Errorf("stat %s: %v", goSumPath, err)
	}

	// The other half: a require block can name a dependency before anything has
	// been fetched, so go.sum's absence alone would be true of a repository that
	// is one `go mod download` from having one.
	mod, err := os.ReadFile(goModPath)
	if err != nil {
		t.Fatalf("read %s: %v", goModPath, err)
	}
	if bytes.Contains(mod, []byte("require")) {
		t.Errorf("%s carries a require block:\n%s", goModPath, mod)
	}
}

// readmeVarRow is the CRSW_ variable a README table row is *about* — the first
// cell of the row, which is the name column of the configuration table.
//
// It is the first cell and not every name on the line because a row describes
// one variable in terms of others: the default of CRSW_IDLE_TIMEOUT_MAX is
// CRSW_IDLE_TIMEOUT, and reading those as documented rows would let a variable
// with no row of its own pass on a mention in someone else's.
//
// It is a table row and not the whole file for the same reason in the other
// direction. The prose around the table names CRSW_CONFIG_FILE, which is read by
// file.go and declared nowhere in config.go — it points at the file rather than
// being a setting in one, so it has no row, and a scan of the prose would report
// it as invented.
var readmeVarRow = regexp.MustCompile("^\\|\\s*`(" + envPrefix + "[A-Z0-9_]+)`\\s*\\|")

// readmeVars returns the variables README.md's configuration table has a row for.
func readmeVars(t *testing.T) map[string]bool {
	t.Helper()

	raw, err := os.ReadFile(rootREADMEPath)
	if err != nil {
		t.Fatalf("read %s: %v", rootREADMEPath, err)
	}

	vars := make(map[string]bool)
	for _, line := range strings.Split(string(raw), "\n") {
		match := readmeVarRow.FindStringSubmatch(strings.TrimSpace(line))
		if match == nil {
			continue
		}
		if vars[match[1]] {
			t.Errorf("%s has two rows for %s; an operator reading one of them does not know the other exists", rootREADMEPath, match[1])
		}
		vars[match[1]] = true
	}

	if len(vars) == 0 {
		t.Fatalf("%s has no %s row in its configuration table; this test is not checking anything", rootREADMEPath, envPrefix)
	}
	return vars
}

// TestREADMEDocumentsEveryVariable holds the table to config.go in both
// directions. A setting the README omits is a bound the operator never learns
// they have; a setting it invents is one they will set and never see take effect.
func TestREADMEDocumentsEveryVariable(t *testing.T) {
	t.Parallel()

	declared := declaredVars(t)
	documented := readmeVars(t)

	for name := range declared {
		if !documented[name] {
			t.Errorf("%s reads %s and %s has no row for it, so an operator reading the README alone never learns it exists", configSourcePath, name, rootREADMEPath)
		}
	}
	for name := range documented {
		if !declared[name] {
			t.Errorf("%s documents %s and nothing in %s reads it, so an operator who sets it runs on the default and is never told", rootREADMEPath, name, configSourcePath)
		}
	}
}
