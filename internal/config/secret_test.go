package config_test

// IsSecret is shared by the 0600 permission refusal and the settings page's
// value column. The behavioural test below is the easy half. The structural one
// is the half that matters: the failure this classifier exists to prevent is not
// a wrong answer but a *second* answer somewhere else, and no behavioural test
// can see one of those — both lists are right on the day they are written, and
// the suite stays green through every day afterwards on which they drift apart.

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/nctiggy/claude-remote-session-webhook/internal/config"
)

const (
	// classifierFile is the one file exempt from the literal search below, and
	// classifierFunc the one declaration exempt from the shape check. Both
	// exemptions are only sound while the classifier is really there, which is
	// asserted rather than assumed.
	classifierFile = "secret.go"
	classifierFunc = "IsSecret"
)

// secretKeyNames is restated as literals rather than taken from the package
// under test. A list read out of the answer agrees with the answer by
// construction, and "only shared_secret is classified" — the failure this task
// names — has to be catchable here.
var secretKeyNames = []string{"shared_secret", "access_allowed_emails", "dashboard_password"}

func TestIsSecretNamesBothSecretKeys(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		key    string
		secret bool
		why    string
	}{
		{"shared_secret", true, "it authenticates every API caller"},
		{"access_allowed_emails", true, "it is not a credential, but it names who may reach a daemon that runs unsandboxed code"},
		{"dashboard_password", true, "it is the browser door itself, and a password rendered on the page it protects, or editable from it, is not a door"},
		{"allowed_roots", false, "it is the containment boundary, and an operator whose working directory was refused has to be able to read it"},
		{"listen", false, "a loopback address discloses nothing"},
		{"start_commands", false, "the settings page names commands the operator configured themselves"},
		{"", false, "no key at all is not a secret one"},
		{"shared_secret_backup", false, "secrecy is the key, not a prefix of it"},
		// A mixed-case spelling is not a key at all: the file grammar fixes a key
		// to [a-z0-9_]+, so this is refused as malformed long before anything asks
		// whether it is secret. If that grammar ever widens, this row is the one to
		// revisit — an exact match would be a fail-open the moment a key can be
		// spelled two ways.
		{"Shared_Secret", false, "the file grammar admits no upper case, so this is refused as malformed before secrecy is asked"},
	} {
		t.Run(tc.key, func(t *testing.T) {
			t.Parallel()

			if got := config.IsSecret(tc.key); got != tc.secret {
				t.Errorf("IsSecret(%q) = %v, want %v: %s", tc.key, got, tc.secret, tc.why)
			}
		})
	}
}

// TestIsSecretIsTheOnlyClassifier holds the property the predicate exists for.
// One answer cannot disagree with itself; two can, and the disagreement surfaces
// as the settings page printing a value the permission check refused to leave
// group-readable.
func TestIsSecretIsTheOnlyClassifier(t *testing.T) {
	t.Parallel()

	fset, files := packageFiles(t)

	classifier := files[classifierFile]
	if classifier == nil {
		t.Fatalf("%s does not exist, so %s has moved and both exemptions below are excusing the wrong file", classifierFile, classifierFunc)
	}
	if !declares(classifier, classifierFunc) {
		t.Fatalf("%s does not declare %s; exempting it would exempt a file that classifies nothing", classifierFile, classifierFunc)
	}
	// The exempt file has to name both keys, or the search finds nothing
	// anywhere and passes by being vacuous — which is the same green a deleted
	// classifier would produce.
	named := make(map[string]bool, len(secretKeyNames))
	for _, lit := range stringLiterals(classifier) {
		if key, ok := secretKeyIn(lit); ok {
			named[key] = true
		}
	}
	for _, key := range secretKeyNames {
		if !named[key] {
			t.Fatalf("%s never names %q, so it is not classifying it and this test is checking nothing", classifierFile, key)
		}
	}

	for name, file := range files {
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || (name == classifierFile && fn.Name.Name == classifierFunc) {
				continue
			}
			if decidesSecrecy(fn) {
				t.Errorf("%s: %s decides whether a key is secret; config.%s is the only thing that may, because two predicates are two answers and the permission refusal and the settings page will one day give different ones",
					fset.Position(fn.Pos()), fn.Name.Name, classifierFunc)
			}
		}

		if name == classifierFile {
			continue
		}
		for _, lit := range stringLiterals(file) {
			key, ok := secretKeyIn(lit)
			if !ok {
				continue
			}
			t.Errorf("%s names the secret key %q: a second list of secret keys is a second answer to what a secret is. Ask config.%s instead",
				fset.Position(lit.Pos()), key, classifierFunc)
		}
	}
}

// packageFiles parses every non-test Go file in this package.
//
// Test files are skipped, because a fixture naming a secret key is not a second
// classifier — file_test.go's worked example is required to carry one. Build
// tags are deliberately not honoured: ParseFile reads a file whatever it is
// tagged with, so a tag is not a way to hide a second list from this walk.
func packageFiles(t *testing.T) (*token.FileSet, map[string]*ast.File) {
	t.Helper()

	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read the package directory: %v", err)
	}

	fset := token.NewFileSet()
	files := make(map[string]*ast.File)
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		parsed, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		files[name] = parsed
	}

	if len(files) < 2 {
		t.Fatalf("found %d non-test files in this package; the walk has nothing but the classifier to check", len(files))
	}
	return fset, files
}

// declares reports whether a file declares a plain function by that name.
func declares(file *ast.File, name string) bool {
	for _, decl := range file.Decls {
		if fn, ok := decl.(*ast.FuncDecl); ok && fn.Recv == nil && fn.Name.Name == name {
			return true
		}
	}
	return false
}

// decidesSecrecy reports whether a declaration has the shape of a question about
// a key — one string in, one bool out — *and* is named for the answer it gives.
//
// Neither half alone is enough to accuse. config.go's isPlaceholder has the
// shape and not the name; loadSecret has the name and not the shape. Neither
// decides whether a key is secret, and neither should be flagged.
func decidesSecrecy(fn *ast.FuncDecl) bool {
	if !strings.Contains(strings.ToLower(fn.Name.Name), "secret") {
		return false
	}
	return isOneUnnamedOrSingle(fn.Type.Params, "string") && isOneUnnamedOrSingle(fn.Type.Results, "bool")
}

// isOneUnnamedOrSingle reports whether a parameter or result list is exactly one
// value of the named built-in type.
func isOneUnnamedOrSingle(list *ast.FieldList, typeName string) bool {
	if list == nil || len(list.List) != 1 || len(list.List[0].Names) > 1 {
		return false
	}
	ident, ok := list.List[0].Type.(*ast.Ident)
	return ok && ident.Name == typeName
}

// stringLiterals is every string literal in a file, positions kept so a failure
// names the line to go and look at.
func stringLiterals(file *ast.File) []*ast.BasicLit {
	var lits []*ast.BasicLit
	ast.Inspect(file, func(n ast.Node) bool {
		if lit, ok := n.(*ast.BasicLit); ok && lit.Kind == token.STRING {
			lits = append(lits, lit)
		}
		return true
	})
	return lits
}

// secretKeyIn matches a literal against the key names exactly, never as a
// substring. config.go's redacting String() spells "shared_secret:<redacted>",
// which names the field it is refusing to print and is the opposite of a second
// classifier; a substring match would report it and teach the next reader to
// stop believing this test.
func secretKeyIn(lit *ast.BasicLit) (string, bool) {
	value, err := strconv.Unquote(lit.Value)
	if err != nil {
		return "", false
	}
	for _, key := range secretKeyNames {
		if value == key {
			return key, true
		}
	}
	return "", false
}
