package httpapi

// render.go is the dashboard's rendering half: the template set, parsed once
// when the server is built, and the headers every browser-door response carries.
// The page handlers (T014) join them here.
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
	"bytes"
	"errors"
	"fmt"
	"html/template"
	"io/fs"
	"net/http"
	"path"
	"strings"
)

// The browser door's response headers, named here once each because the name a
// header is written under and the name a test looks for are the same string.
const (
	headerCSP                = "Content-Security-Policy"
	headerHSTS               = "Strict-Transport-Security"
	headerContentTypeOptions = "X-Content-Type-Options"
	headerReferrerPolicy     = "Referrer-Policy"
	headerCacheControl       = "Cache-Control"
)

// The values, from docs/security.md's table and contracts/dashboard.md, spelled
// exactly as those documents give them.
//
// The policy is sent unmodified (research D9). `default-src 'none'` with
// `'self'`-only sources is FR-025 enforced by the browser itself: a template that
// slipped a CDN reference past review fails to load, visibly, rather than
// quietly shipping a third-party dependency. There is no `unsafe-inline` and no
// route may add one — docs/security.md rules that a change needing one is the
// wrong change, which is why this is a constant and not a value a handler
// assembles.
const (
	contentSecurityPolicy   = "default-src 'none'; script-src 'self'; style-src 'self'; connect-src 'self'; img-src 'self' data:; base-uri 'none'; form-action 'self'; frame-ancestors 'none'"
	strictTransportSecurity = "max-age=31536000; includeSubDomains"
	contentTypeNosniff      = "nosniff"
	referrerPolicyNone      = "no-referrer"
	cacheControlNoStore     = "no-store"
)

// setBrowserSecurityHeaders writes what every browser-door response carries —
// pages, assets, refusals, and the not-found page alike (FR-026).
//
// It is called from one place, authenticateBrowser, before layer 1 has decided
// anything. That is deliberate twice over: a handler cannot forget headers it
// never sets, and a refusal leaves with the identical set to a served page, so
// the headers cannot become the thing that tells a stranger which paths this
// daemon really serves.
//
// `no-store` is the default rather than the exception. contracts/dashboard.md
// enumerates the one exemption — the two embedded assets, which carry no session
// data — so everything else on this door is a page, a stream, or an
// authorisation decision, and a cached copy of any of those is a copy that
// outlives what it described. See authenticateBrowser for where the exemption is
// taken, and why only an admitted request may take it.
//
// What this function does not write is as load-bearing as what it does: no
// `Access-Control-Allow-*` header, here or on the API door (FR-034c, research
// D8). The browser's layer-1 credential is an ambient cookie — it rides on
// requests a hostile third-party page triggers, and the edge turns those into a
// valid assertion — so same-origin policy is the only thing stopping that page
// *reading* these responses, and it holds exactly as long as the daemon never
// opts out of it. There is deliberately no CORS helper in this package to reach
// for; the absence is the protection, and it is a swept assertion rather than a
// habit because an absence nobody checks is one refactor away from present.
func setBrowserSecurityHeaders(h http.Header) {
	h.Set(headerCSP, contentSecurityPolicy)
	h.Set(headerHSTS, strictTransportSecurity)
	h.Set(headerContentTypeOptions, contentTypeNosniff)
	h.Set(headerReferrerPolicy, referrerPolicyNone)
	h.Set(headerCacheControl, cacheControlNoStore)
}

// errPageNotRendered is what the trail records when a page could not be built.
// Like every other reason in this repo it is a constant authored here: the
// template engine's own error can name a field, a value, or a session, and none
// of that may reach the journal (FR-035, FR-042).
var errPageNotRendered = errors.New("the page could not be rendered")

// renderPage executes one page of the set and writes it, and it is the only way
// a handler on this door answers with a document.
//
// The page is built into a buffer first. A response cannot be taken back once
// the first byte is written — the status line and the headers are gone — so a
// template that failed halfway would leave a browser holding a truncated
// document under a 200, which is the one failure that looks like success. The
// pages this daemon renders are a fleet and one session, so the buffer is small
// by construction.
//
// The failure answers with no body at all. What the template said is
// server-side detail, and a 500 carrying it would be the one place this door
// talks about its own internals — to whoever asked. It goes to the trail and to
// the report channel instead, which is where an operator is already reading.
//
// The security headers are not set here. authenticateBrowser writes them before
// layer 1 has decided anything, so a page and a refusal leave with the identical
// set and a handler cannot forget headers it never sets.
func (s *Server) renderPage(w http.ResponseWriter, r *http.Request, page string, data any) {
	var body bytes.Buffer
	if err := s.templates.ExecuteTemplate(&body, page, data); err != nil {
		AuditFrom(r.Context()).Deny(errPageNotRendered.Error())
		s.report(fmt.Errorf("render the %s page: %w", page, err))
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	w.Header().Set(headerContentType, contentTypeHTML)
	if _, err := w.Write(body.Bytes()); err != nil {
		s.report(fmt.Errorf("write the %s page: %w", page, err))
	}
}

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
