package config_test

// IsBool is the predicate the settings page uses to decide that a key gets a
// checkbox — and, because an unchecked checkbox submits nothing, that an absent
// value for that key means `false` rather than "unset". The behavioural test
// below is the easy half. The structural one is the half that matters, and it
// matters in both directions: a boolean the list forgets renders as a text box,
// and a *non*-boolean the list invents is a setting a truncated POST can clear.
//
// Neither failure is visible behaviourally. Both lists are right on the day they
// are written, and the suite stays green through every day afterwards on which
// the loader grows a boolean and this one does not.

import (
	"go/ast"
	"go/token"
	"strconv"
	"strings"
	"testing"

	"github.com/nctiggy/claude-remote-session-webhook/internal/config"
)

// boolLoaderFunc is the loader whose call sites define the answer. It is
// unexported, so the walk below finds it by name in the source rather than by
// calling it.
const boolLoaderFunc = "loadBool"

func TestIsBoolNamesBothBooleanKeys(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		key     string
		boolean bool
		why     string
	}{
		{"discover_roots", true, "it is loaded with loadBool and is on or off"},
		{"destroy_on_shutdown", true, "it is loaded with loadBool and is on or off"},
		{"access_enabled", true, "it is loaded with loadBool and is on or off"},
		{"dashboard_password", false, "a password is not a boolean, and an absent one read as false would be a door quietly removed"},
		{"shared_secret", false, "a secret is not a boolean, and an absent value for it must never be read as a decision"},
		{"access_allowed_emails", false, "a list of addresses is not a boolean; reading an absent one as false would rewrite who may sign in"},
		{"allowed_roots", false, "the containment boundary is a list of paths"},
		{"max_sessions", false, "a cap is a number"},
		{"listen", false, "an address is not a boolean"},
		{"", false, "no key at all is not a boolean one"},
		{"nonexistent_setting", false, "an unknown key is not boolean; the page must not invent a control for it"},
		// The truncated-request rule is why this row is not merely pedantic: a
		// prefix match here would make every key beginning `discover_roots`
		// clearable by a POST that omits its value.
		{"discover_roots_also", false, "booleanness is the key, not a prefix of it"},
		{"Discover_Roots", false, "the file grammar admits no upper case, so this is refused as malformed before its kind is asked"},
	} {
		t.Run(tc.key, func(t *testing.T) {
			t.Parallel()

			if got := config.IsBool(tc.key); got != tc.boolean {
				t.Errorf("IsBool(%q) = %v, want %v: %s", tc.key, got, tc.boolean, tc.why)
			}
		})
	}
}

// TestIsBoolNamesEveryBooleanLoaded holds the hand-written list to the loader it
// is a restatement of. config.go's booleans are whatever loadBool is called
// with; this parses the package, resolves the constant at each call site, and
// checks the two sets agree.
//
// The reverse direction is checked over config.Vars() rather than over IsBool's
// own literals. Vars() is the whole universe of keys a file may set and the page
// may render, so a key claimed boolean and never loaded as one is caught exactly
// where it could do harm, and the check stays behavioural rather than becoming
// an assertion about how IsBool happens to be written.
func TestIsBoolNamesEveryBooleanLoaded(t *testing.T) {
	t.Parallel()

	fset, files := packageFiles(t)
	loaded := booleanKeys(t, fset, files, varConstants(t, files))

	if len(loaded) == 0 {
		t.Fatalf("found no %s call in this package; the walk resolved nothing and this test is checking nothing", boolLoaderFunc)
	}

	for key := range loaded {
		if !config.IsBool(key) {
			t.Errorf("%s is loaded with %s and config.IsBool(%q) is false, so the settings page renders it as a text box and an operator types the word",
				config.VarForKey(key), boolLoaderFunc, key)
		}
	}
	for _, name := range config.Vars() {
		key := config.KeyForVar(name)
		if config.IsBool(key) && !loaded[key] {
			t.Errorf("config.IsBool(%q) is true and no %s call reads %s, so a request that omits its value would write `false` over a setting that is not a boolean",
				key, boolLoaderFunc, name)
		}
	}
}

// varConstants maps the name of every CRSW_ constant this package declares to
// its value, which is what turns `loadBool(getenv, EnvDiscoverRoots)` at a call
// site into the key `discover_roots`.
//
// envexample_test.go's declaredVars answers the other question — the set of
// values, with the names discarded — and is left alone rather than widened,
// because a shared helper returning both would make each caller filter for the
// half it wanted.
func varConstants(t *testing.T, files map[string]*ast.File) map[string]string {
	t.Helper()

	consts := make(map[string]string)
	for _, file := range files {
		for _, decl := range file.Decls {
			gen, ok := decl.(*ast.GenDecl)
			if !ok || gen.Tok != token.CONST {
				continue
			}
			for _, spec := range gen.Specs {
				value, ok := spec.(*ast.ValueSpec)
				if !ok || len(value.Names) != len(value.Values) {
					continue
				}
				for i, expr := range value.Values {
					lit, ok := expr.(*ast.BasicLit)
					if !ok || lit.Kind != token.STRING {
						continue
					}
					unquoted, err := strconv.Unquote(lit.Value)
					if err != nil || !strings.HasPrefix(unquoted, envPrefix) {
						continue
					}
					consts[value.Names[i].Name] = unquoted
				}
			}
		}
	}

	if len(consts) == 0 {
		t.Fatalf("found no %s constants in this package; no call site can be resolved", envPrefix)
	}
	return consts
}

// booleanKeys is the file key of every setting read through loadBool.
//
// A call this cannot resolve is an error rather than a silent skip: the whole
// value of the walk is that it sees every call, and one it quietly passed over
// would be a boolean missing from the list with the test still green.
func booleanKeys(t *testing.T, fset *token.FileSet, files map[string]*ast.File, consts map[string]string) map[string]bool {
	t.Helper()

	keys := make(map[string]bool)
	for _, file := range files {
		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			fn, ok := call.Fun.(*ast.Ident)
			if !ok || fn.Name != boolLoaderFunc {
				return true
			}

			where := fset.Position(call.Pos())
			if len(call.Args) != 2 {
				t.Errorf("%s: %s takes %d arguments here; this walk reads the second as the variable name and no longer knows which setting is loaded",
					where, boolLoaderFunc, len(call.Args))
				return true
			}
			ident, ok := call.Args[1].(*ast.Ident)
			if !ok {
				t.Errorf("%s: %s is passed an expression rather than a constant, so which setting it loads cannot be read from the source. Pass the Env constant",
					where, boolLoaderFunc)
				return true
			}
			value, ok := consts[ident.Name]
			if !ok {
				t.Errorf("%s: %s is passed %s, which is not a %s constant declared in this package",
					where, boolLoaderFunc, ident.Name, envPrefix)
				return true
			}
			keys[config.KeyForVar(value)] = true
			return true
		})
	}
	return keys
}
