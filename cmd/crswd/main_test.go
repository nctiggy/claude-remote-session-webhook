// A test about where this daemon's two streams go, and the only untagged test
// of main.go itself: config_cmd_test.go covers the subcommands and
// bypass_build_test.go covers which files the shipping artifact is built from.
//
// It is here rather than in a library package because the invariant is the
// *process's* — every line on stdout is an audit record — and cmd/crswd is
// where both destinations are chosen. See main.go's "The two streams".
package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// moduleRoot is where this package sits relative to the module. A test binary
// runs in its own package directory, and cmd/crswd is two levels down.
const moduleRoot = "../.."

// theTrailsOwnFile is the one file allowed to name os.Stdout outright, because
// writing the trail there is the whole of what it does.
const theTrailsOwnFile = "internal/audit/audit.go"

// theSubcommandsReport is the other, and it is allowed only as an argument to
// this call. `crswd config check` runs *instead of* the daemon and emits no
// record, so its report on stdout is the answer the operator ran it for rather
// than a diagnostic in the trail. Naming the call and not the file keeps a
// second, ordinary print in main.go from inheriting the exemption.
const theSubcommandsReport = "runConfigCommand"

// theVersionReport is the third, on exactly the same terms and for the same
// reason: `crswd --version` runs *instead of* the daemon, emits no record, and
// the line it prints is the answer the operator ran it for. Named as a call
// like the one above, so that an ordinary print added to main.go later still
// fails this test.
const theVersionReport = "printVersion"

// theKeygenReport is the fourth and the one with the strongest claim to the
// stream: `crswd keygen` runs instead of the daemon, emits no record, and what
// it puts on stdout is a private key the operator asked for and must be able to
// pipe, copy, or read. Named as a call for the same reason as the two above.
const theKeygenReport = "runKeygen"

// theUnitReport is the fifth, on the terms of the first: `crswd unit …` runs
// instead of the daemon, emits no record, and what it puts on stdout is a report
// the operator asked for and must be able to pipe into a pager or a diff.
//
// It is named as a *call* rather than by file, like every entry above, so that
// the exemption covers the two writers this dispatch hands the streams to and
// nothing else in the file that grew one later.
const theUnitReport = "runUnitCommand"

// TestDiagnosticsGoToStderr is FR-023a as a property of the whole daemon rather
// than of one sink: nothing it is built from writes a human-readable line to
// standard output.
//
// Structural, because the behaviour it forbids is one nobody writes a test for.
// A print to stdout is added while chasing something else, or a warning is
// moved to "where the operator will see it", and every existing case stays
// green while the documented reader of the audit trail —
//
//	journalctl --user -u crswd -o cat | grep '^{' | jq .
//
// — starts either dropping records or failing on one, depending on the first
// character of the new sentence. This is the check the live daemon did not have
// when #88 was filed: its dependency banner and its records shared the journal,
// and nothing anywhere said which stream either belonged on.
//
// Not parallel: it reads the process's standard logger, which internal/audit's
// leak suite redirects in its own binary and which nothing here may see moved.
func TestDiagnosticsGoToStderr(t *testing.T) {
	// The last-resort channel in internal/httpapi and internal/session is the
	// standard logger, so where that writes is where a report with nowhere else
	// to go lands. It is stderr by the log package's own default and this daemon
	// never moves it — which is a claim worth making, since moving it is one
	// call from anywhere in the process.
	if log.Writer() != os.Stderr {
		t.Errorf("the standard logger writes to %T rather than stderr; every last-resort report in this daemon goes through it", log.Writer())
	}

	fset := token.NewFileSet()
	files := parseTheDaemon(t, fset)

	// Filled at the call, which ast.Inspect visits before its arguments.
	permitted := map[token.Pos]bool{}
	var found, exempt int

	for name, file := range files {
		ast.Inspect(file, func(n ast.Node) bool {
			if call, ok := n.(*ast.CallExpr); ok {
				if fn, ok := call.Fun.(*ast.Ident); ok &&
					(fn.Name == theSubcommandsReport || fn.Name == theVersionReport ||
						fn.Name == theKeygenReport || fn.Name == theUnitReport) {
					for _, arg := range call.Args {
						permitted[arg.Pos()] = true
					}
				}
				return true
			}

			sel, ok := n.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			pkg, ok := sel.X.(*ast.Ident)
			if !ok || pkg.Name != "os" || sel.Sel.Name != "Stdout" {
				return true
			}

			found++
			if name == theTrailsOwnFile || permitted[sel.Pos()] {
				exempt++
				return true
			}
			t.Errorf("%s: %s writes to standard output, which carries the audit trail and nothing else. A human-readable line goes to stderr — see cmd/crswd/main.go, \"The two streams\"",
				fset.Position(sel.Pos()), name)
			return true
		})
	}

	// The walk has to have seen the three it knows about, or it is a sweep over
	// nothing reporting no violations — which is what a wrong root path, a
	// renamed audit file, or a skipped directory each look like from here.
	if exempt < 4 {
		t.Fatalf("the sweep found %d os.Stdout in %d files and exempted %d; it expects at least the trail's own, the subcommand's, --version's and keygen's, so it is reading the wrong tree",
			found, len(files), exempt)
	}
}

// TestStartupDiagnosticsGoToStderr is the same requirement at the one call that
// produced #88's noise: the dependency probe's banner is what the live daemon
// printed on every start, beside the records, in the journal an operator reads
// with jq.
//
// A wiring assertion rather than a behavioural one. CheckDependencies takes its
// sink as a parameter — internal/config's own tests drive every message through
// a buffer — so the only thing left to get wrong is which stream this command
// hands it, and that is a line in main.go with no test behind it.
func TestStartupDiagnosticsGoToStderr(t *testing.T) {
	t.Parallel()

	const probe = "CheckDependencies"

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "main.go", nil, 0)
	if err != nil {
		t.Fatalf("parse main.go: %v", err)
	}

	var calls int
	ast.Inspect(file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != probe {
			return true
		}

		calls++
		if len(call.Args) != 1 {
			t.Errorf("%s: %s is called with %d arguments; the sink is the only one", fset.Position(call.Pos()), probe, len(call.Args))
			return true
		}
		if got := render(call.Args[0]); got != "os.Stderr" {
			t.Errorf("%s: the startup probe writes its banners to %s; they are diagnostics and stdout is the trail's", fset.Position(call.Pos()), got)
		}
		return true
	})

	if calls != 1 {
		t.Fatalf("main.go calls %s %d times; want exactly one, at the point startup checks the host", probe, calls)
	}
}

// render spells an expression the way it was written, for a failure message
// that names what is there rather than its node type.
func render(e ast.Expr) string {
	sel, ok := e.(*ast.SelectorExpr)
	if !ok {
		return "an expression this test cannot name"
	}
	pkg, ok := sel.X.(*ast.Ident)
	if !ok {
		return "an expression this test cannot name"
	}
	return pkg.Name + "." + sel.Sel.Name
}

// parseTheDaemon is every non-test Go file in the module, keyed by its path from
// the module root.
//
// The whole module rather than this package: the streams belong to the process,
// and a warning printed from internal/session reaches the same journal as one
// printed here. Build tags are ignored on purpose — the development build is an
// artifact this repository ships the source of, and a print added under one is
// a print an operator can be handed.
func parseTheDaemon(t *testing.T, fset *token.FileSet) map[string]*ast.File {
	t.Helper()

	files := map[string]*ast.File{}
	err := filepath.WalkDir(moduleRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			// Nothing under a dot directory is compiled into this daemon, and
			// .git alone is most of the tree by file count.
			if strings.HasPrefix(d.Name(), ".") && path != moduleRoot {
				return fs.SkipDir
			}
			return nil
		}
		name := d.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			return nil
		}
		parsed, parseErr := parser.ParseFile(fset, path, nil, 0)
		if parseErr != nil {
			return parseErr
		}
		relative, relErr := filepath.Rel(moduleRoot, path)
		if relErr != nil {
			return relErr
		}
		files[filepath.ToSlash(relative)] = parsed
		return nil
	})
	if err != nil {
		t.Fatalf("read the daemon's source: %v", err)
	}
	if _, ok := files[theTrailsOwnFile]; !ok {
		t.Fatalf("the walk did not find %s among %d files; it is not reading this module", theTrailsOwnFile, len(files))
	}
	return files
}
