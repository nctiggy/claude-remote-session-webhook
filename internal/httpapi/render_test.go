// Internal test, matching server_test.go. The property T002 is about — the set
// is parsed when the server is built, not when a browser asks — is a field on an
// unexported struct, and the refusals it must make are only reachable through
// parseTemplates' fs.FS seam.
package httpapi

import (
	"io/fs"
	"path"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/nctiggy/claude-remote-session-webhook/web"
)

// TestNewServerParsesTheEmbeddedTemplateSet is the whole claim of this task: by
// the time a Server exists, every template in web/ has been read and compiled.
//
// It asserts the set is *exactly* the embedded tree rather than merely non-empty.
// That is what makes the refusals below load-bearing: a parse failure in any file
// under web/templates/ is a parse failure inside this set, so newServer returns
// an error, New returns it to cmd/crswd, and the daemon exits without binding.
func TestNewServerParsesTheEmbeddedTemplateSet(t *testing.T) {
	t.Parallel()

	s := newTestServer(t, loopbackListen)
	if s.templates == nil {
		t.Fatal("the server was built with no template set; a page would fail on the first request instead of at startup")
	}

	got := make(map[string]bool)
	for _, tmpl := range s.templates.Templates() {
		if tmpl.Name() == "" { // the unnamed root of the set
			continue
		}
		got[tmpl.Name()] = true
	}

	want := embeddedTemplateNames(t)
	if len(want) == 0 {
		t.Fatal("web.Templates embedded no templates; the //go:embed pattern has stopped matching")
	}
	for name := range want {
		if !got[name] {
			t.Errorf("template %q is embedded but not in the server's set", name)
		}
	}
	for name := range got {
		if !want[name] {
			t.Errorf("the server's set defines %q, which is not a file in web/templates", name)
		}
	}
}

// TestEmbeddedTemplatesRenderThePlaceholderPage proves the embedded page is a
// template that executes and not merely one that parses. The placeholder is
// US1's to replace; what must survive is that it references no external origin,
// because docs/security.md's CSP is sent unmodified and a CDN reference would
// fail in a browser rather than in review (SC-005 asserts this properly in T017).
func TestEmbeddedTemplatesRenderThePlaceholderPage(t *testing.T) {
	t.Parallel()

	var page strings.Builder
	if err := newTestServer(t, loopbackListen).templates.ExecuteTemplate(&page, "dashboard", nil); err != nil {
		t.Fatalf("execute the dashboard template: %v", err)
	}
	rendered := page.String()
	if !strings.Contains(rendered, "<!doctype html>") {
		t.Errorf("the dashboard template rendered no document:\n%s", rendered)
	}
	for _, origin := range []string{"http://", "https://", "//cdn", "srcset"} {
		if strings.Contains(rendered, origin) {
			t.Errorf("the dashboard template references an external origin (%q); the CSP permits 'self' only", origin)
		}
	}
}

// TestTemplatesEscapeUntrustedText pins the import: html/template, never
// text/template. Everything a Claude session prints reaches this page, along with
// session names and working directories a caller chose, and contextual escaping
// is what closes that surface by construction rather than by sanitising
// (Constitution VII, docs/security.md). Swapping the import would still compile
// and would still pass every other test in this file.
func TestTemplatesEscapeUntrustedText(t *testing.T) {
	t.Parallel()

	set, err := parseTemplates(fstest.MapFS{
		"pane.html": &fstest.MapFile{Data: []byte(`<p>{{.}}</p>`)},
	})
	if err != nil {
		t.Fatalf("parseTemplates() = _, %v; want a set", err)
	}

	var out strings.Builder
	if err := set.ExecuteTemplate(&out, "pane", `<script>alert(1)</script>`); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if strings.Contains(out.String(), "<script>") {
		t.Fatalf("rendered %q; untrusted text reached the page as markup", out.String())
	}
}

// TestParseTemplatesAssociatesTheSet covers the reason there is one set rather
// than one template per file: a page reaches the partials docs/components.md
// defines, by the base name that document already uses at its call sites.
func TestParseTemplatesAssociatesTheSet(t *testing.T) {
	t.Parallel()

	set, err := parseTemplates(fstest.MapFS{
		"templates/dashboard.html":      &fstest.MapFile{Data: []byte(`page:{{template "empty" .}}`)},
		"templates/partials/empty.html": &fstest.MapFile{Data: []byte(`no sessions`)},
	})
	if err != nil {
		t.Fatalf("parseTemplates() = _, %v; want a set", err)
	}

	var out strings.Builder
	if err := set.ExecuteTemplate(&out, "dashboard", nil); err != nil {
		t.Fatalf("execute a page that includes a partial: %v", err)
	}
	if got, want := out.String(), "page:no sessions"; got != want {
		t.Errorf("rendered %q; want %q", got, want)
	}
}

// TestParseTemplatesRefuses is the failure table. Every row is a tree that must
// stop the daemon at construction: reaching a browser is the outcome each one is
// there to prevent, and the only place to refuse is before a listener exists.
func TestParseTemplatesRefuses(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		fsys fs.FS
		// names something the refusal must say, so an operator reading a startup
		// failure learns which file to open.
		mentions string
	}{
		"an unparseable action": {
			fsys: fstest.MapFS{
				"dashboard.html": &fstest.MapFile{Data: []byte(`{{ .Sessions `)},
			},
			mentions: "dashboard.html",
		},
		"a template calling an unknown function": {
			fsys: fstest.MapFS{
				"dashboard.html": &fstest.MapFile{Data: []byte(`{{ dict "a" 1 }}`)},
			},
			mentions: "dashboard.html",
		},
		"two files claiming one name": {
			fsys: fstest.MapFS{
				"card.html":          &fstest.MapFile{Data: []byte(`page`)},
				"partials/card.html": &fstest.MapFile{Data: []byte(`partial`)},
			},
			mentions: "card",
		},
		"a file that is not a template": {
			fsys: fstest.MapFS{
				"dashboard.html": &fstest.MapFile{Data: []byte(`page`)},
				"notes.tmpl":     &fstest.MapFile{Data: []byte(`page`)},
			},
			mentions: "notes.tmpl",
		},
		"an empty tree": {
			fsys:     fstest.MapFS{},
			mentions: "empty",
		},
		"no tree at all": {
			fsys:     nil,
			mentions: "no template source",
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			set, err := parseTemplates(tc.fsys)
			if err == nil {
				t.Fatalf("parseTemplates() = %v, nil; want a refusal", set)
			}
			if set != nil {
				t.Errorf("parseTemplates() returned a set alongside its error; a caller could serve from it")
			}
			if !strings.Contains(err.Error(), tc.mentions) {
				t.Errorf("refusal %q does not mention %q", err, tc.mentions)
			}
		})
	}
}

// TestStaticAssetsAreEmbedded covers the other half of the tree. The stylesheet
// is referenced by the page above, so an asset tree that stopped being embedded
// would otherwise show up as an unstyled dashboard in a browser rather than as a
// failure here.
func TestStaticAssetsAreEmbedded(t *testing.T) {
	t.Parallel()

	css, err := web.Static.ReadFile("static/crswd.css")
	if err != nil {
		t.Fatalf("read the embedded stylesheet: %v", err)
	}
	if len(css) == 0 {
		t.Error("the embedded stylesheet is empty")
	}
}

// embeddedTemplateNames walks web.Templates the way a browser never can, naming
// each file the way parseTemplates does. It is deliberately a second
// implementation of that naming: a test that asked parseTemplates for the names
// it produced would assert only that the code agrees with itself.
func embeddedTemplateNames(t *testing.T) map[string]bool {
	t.Helper()

	names := make(map[string]bool)
	err := fs.WalkDir(web.Templates, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if path.Ext(p) != templateExt {
			t.Errorf("web/%s is not a %s file; parseTemplates refuses the tree it is in", p, templateExt)
			return nil
		}
		names[strings.TrimSuffix(path.Base(p), templateExt)] = true
		return nil
	})
	if err != nil {
		t.Fatalf("walk the embedded template tree: %v", err)
	}
	return names
}
