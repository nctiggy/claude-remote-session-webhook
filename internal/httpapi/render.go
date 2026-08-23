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
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"html/template"
	"io/fs"
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/nctiggy/claude-remote-session-webhook/internal/buildinfo"
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

// The two kinds of file web/static holds, and the exact type each is declared
// as. They are constants rather than a call to mime.TypeByExtension because that
// function consults /etc/mime.types on a Unix host: the same binary would serve
// `.js` as text/javascript on one machine and application/javascript on another,
// and with `nosniff` sent on every response a browser refuses what it is told
// rather than guessing. What a daemon serves may not depend on a file the
// operator has never heard of.
const (
	contentTypeCSS = "text/css; charset=utf-8"
	contentTypeJS  = "text/javascript; charset=utf-8"
)

const headerETag = "ETag"

// cacheControlRevalidate is the asset half of the cache rule, and it reads
// stricter than it is: `no-cache` means "keep it, but ask before using it", not
// `no-store`'s "do not keep it at all".
//
// contracts/dashboard.md exempts the two assets from no-store because they hold
// no session data and caching them is what makes the page cheap. What it does not
// give them is a freshness lifetime, and this is why: the URLs carry no
// fingerprint, so a `max-age` would let a browser run the previous binary's
// script against this one's markup for as long as the lifetime lasted — and the
// script is the whole of the dashboard's client code. The entity tag below is a
// hash of the embedded bytes, so a revalidation is exact and an unchanged asset
// costs a 304 with no body.
const cacheControlRevalidate = "no-cache"

// staticRoot is the one subtree of web/ this door exposes, and naming it here is
// the whole of that guarantee: the template tree is compiled into pages by
// parseTemplates and never handed to a caller as source.
const staticRoot = "static"

// assetContentTypes is the closed set of asset kinds the browser door serves. A
// file under static/ with any other extension is a startup failure rather than a
// file served as something a browser has to guess at — docs/security.md's CSP
// admits a stylesheet and a script from 'self' and nothing else, so a third kind
// of asset is a policy change before it is a file.
var assetContentTypes = map[string]string{
	".css": contentTypeCSS,
	".js":  contentTypeJS,
}

// asset is one embedded file resolved at construction: the route it answers, the
// bytes, the type they are declared as, and the tag those bytes hash to.
//
// Everything is decided before the listener binds. A request for an asset reads
// no path, opens no file, and makes no decision — which is what makes "only
// web/static/ is reachable" a property of the router rather than a check a
// handler has to get right (see loadAssets).
type asset struct {
	pattern     string
	contentType string
	etag        string
	body        []byte
}

// loadAssets resolves the embedded asset tree into one route per file.
//
// One literal route per file, rather than a `/static/{path}` wildcard reading
// the path back out of the request. The wildcard is the shape the task list
// sketches and it is the weaker one: net/http unescapes a wildcard's value, so
// `%2e%2e%2f` arrives as `../` inside a single segment and the handler is left
// holding a caller-supplied path it must validate — which is the check
// docs/security.md §2 says to avoid needing. With literal patterns there is
// nothing to traverse: a path that is not exactly one of these files matches no
// asset route at all, falls to the catch-all, and is answered as a page nothing
// claims.
//
// The refusals are startup failures for the reason parseTemplates' are. A file
// this door cannot type, or one nested a directory deeper than the route that
// would name it, is a broken build — and the only place to refuse a broken build
// is before anything can ask for it.
func loadAssets(fsys fs.FS) ([]asset, error) {
	if fsys == nil {
		return nil, errors.New("httpapi: no asset source provided; refusing to start")
	}

	var assets []asset
	err := fs.WalkDir(fsys, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if dir := path.Dir(p); dir != staticRoot {
			return fmt.Errorf("%s is not directly under %s/, so the path a browser asks for and the file it is served from would be two different strings", p, staticRoot)
		}
		contentType, ok := assetContentTypes[path.Ext(p)]
		if !ok {
			return fmt.Errorf("%s is not a kind of asset this door serves", p)
		}
		body, err := fs.ReadFile(fsys, p)
		if err != nil {
			return fmt.Errorf("read %s: %w", p, err)
		}
		sum := sha256.Sum256(body)
		assets = append(assets, asset{
			pattern:     http.MethodGet + " /" + p,
			contentType: contentType,
			etag:        `"` + base64.RawURLEncoding.EncodeToString(sum[:]) + `"`,
			body:        body,
		})
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("httpapi: load the embedded assets: %w", err)
	}
	if len(assets) == 0 {
		// Unreachable through web.Static, since //go:embed refuses to match
		// nothing, but reachable through the seam above — and a daemon serving no
		// stylesheet is the unstyled dashboard this task exists to end, which is a
		// thing nobody notices in a test suite.
		return nil, errors.New("httpapi: the asset tree is empty; refusing to start")
	}
	return assets, nil
}

// serveAsset answers one asset route.
//
// The security headers are already written and the no-store default already
// lifted — authenticateBrowser does both, and lifts it only for a request layer 1
// admitted, so a refusal on this route is byte-identical to a refusal on a page.
// What is left here is the response's own description of itself.
//
// http.ServeContent rather than a Write, for the conditional request alone: it
// compares the tag above against If-None-Match and answers 304 with no body. The
// modification time is deliberately zero — an embedded file has none, and a
// Last-Modified invented from process start would make every restart look like an
// edit.
func (s *Server) serveAsset(a asset) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set(headerContentType, a.contentType)
		w.Header().Set(headerETag, a.etag)
		w.Header().Set(headerCacheControl, cacheControlRevalidate)
		http.ServeContent(w, r, "", time.Time{}, bytes.NewReader(a.body))
	}
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
// The status is a parameter because not every page on this door is a 200: the
// not-found page is a page *and* a refusal (FR-013d), and a renderer that always
// wrote 200 would answer a mistyped URL with a success. It is written after the
// template has succeeded, for the reason the buffer exists — a status line
// cannot be taken back either.
//
// The security headers are not set here. authenticateBrowser writes them before
// layer 1 has decided anything, so a page and a refusal leave with the identical
// set and a handler cannot forget headers it never sets.
func (s *Server) renderPage(w http.ResponseWriter, r *http.Request, status int, page string, data any) {
	var body bytes.Buffer
	if err := s.templates.ExecuteTemplate(&body, page, data); err != nil {
		AuditFrom(r.Context()).Deny(errPageNotRendered.Error())
		s.report(fmt.Errorf("render the %s page: %w", page, err))
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	w.Header().Set(headerContentType, contentTypeHTML)
	w.WriteHeader(status)
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

	// The version is a function rather than a field on every view.
	//
	// Every page shows it, and none of them is *about* it: threading it through
	// eight view structs would put the same line in eight places for a value
	// that is a property of the binary rather than of anything a page renders.
	// A function also cannot drift — buildinfo.Version is the same variable
	// --version prints, so the footer and the command line cannot disagree.
	set := template.New("").Funcs(template.FuncMap{
		"version": func() string { return buildinfo.Version },
	})
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
