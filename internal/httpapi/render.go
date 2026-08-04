package httpapi

// render.go is the dashboard's rendering half: the template set, parsed once
// when the server is built. The security headers (T010) and the page handlers
// (T014) join it here.
//
// Parsing at construction is the whole point of this file. A template that does
// not compile then makes the daemon refuse to start — the same answer
// docs/security.md §4 gives a missing secret — rather than producing a page that
// is fine until the first browser asks for the one route nobody exercised. The
// templates are compiled into the binary (package web), so the failure it
// catches is a bad edit, and catching it at startup is what keeps that edit from
// reaching a browser at all.
//
// html/template, never text/template. Everything a Claude session prints reaches
// this page, as do session names and working directories a caller chose, and the
// engine's contextual escaping is the construction that closes the project's one
// XSS surface (Constitution VII, docs/security.md). A future edit that swapped
// the import would still compile; TestTemplatesEscapeUntrustedText is what
// notices.

import (
	"errors"
	"fmt"
	"html/template"
	"io/fs"
	"path"
	"strings"
)

// templateExt is the only extension the set accepts. A file under templates/
// that does not carry it is refused rather than skipped: silently ignoring
// dashboard.tmpl is a page that renders as nothing, discovered in a browser.
const templateExt = ".html"

// parseTemplates parses every template in fsys into one associated set, so a
// page can reach the partials docs/components.md defines.
//
// A template is named by its base name with the extension dropped —
// partials/status-pill.html is "status-pill" — which is the spelling
// docs/components.md already uses at its call sites ({{ template "empty" … }}).
// The cost of that convention is that two files in different directories can
// claim one name, and html/template's own ParseFS resolves it by letting the
// last one win in silence. That is refused here instead: a partial shadowed by a
// page is a component nobody can see is unused.
//
// The root of the set is unnamed, because a base name never is, so it cannot
// collide with a file.
//
// The fs.FS is a parameter rather than web.Templates reached for directly: it is
// the seam that lets a test prove a broken template is refused, which is the
// behaviour this function exists for and which a compiled-in tree can never
// exhibit.
func parseTemplates(fsys fs.FS) (*template.Template, error) {
	if fsys == nil {
		return nil, errors.New("httpapi: no template source provided; refusing to start")
	}

	set := template.New("")
	source := make(map[string]string)
	err := fs.WalkDir(fsys, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if path.Ext(p) != templateExt {
			return fmt.Errorf("%s is not a %s template", p, templateExt)
		}
		name := strings.TrimSuffix(path.Base(p), templateExt)
		if prior, dup := source[name]; dup {
			return fmt.Errorf("%s and %s both define the template %q", prior, p, name)
		}
		source[name] = p

		text, err := fs.ReadFile(fsys, p)
		if err != nil {
			return fmt.Errorf("read %s: %w", p, err)
		}
		if _, err := set.New(name).Parse(string(text)); err != nil {
			return fmt.Errorf("parse %s: %w", p, err)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("httpapi: parse the template set: %w", err)
	}
	if len(source) == 0 {
		// Unreachable through web.Templates, since //go:embed refuses to match
		// nothing, but reachable through the seam above — and a server holding an
		// empty set would answer every page with "template not defined" one
		// request at a time.
		return nil, errors.New("httpapi: the template set is empty; refusing to start")
	}
	return set, nil
}
