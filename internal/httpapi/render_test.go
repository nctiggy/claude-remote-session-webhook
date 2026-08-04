// Internal test, matching server_test.go. The property T002 is about — the set
// is parsed when the server is built, not when a browser asks — is a field on an
// unexported struct, and the refusals it must make are only reachable through
// parseTemplates' fs.FS seam. T010's headers are reached the same way: the
// browser door has no registered route until T014, so its responses are driven
// through the middleware, and the API door's are driven through the real router.
package httpapi

import (
	"io/fs"
	"maps"
	"net/http"
	"net/http/httptest"
	"path"
	"slices"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/nctiggy/claude-remote-session-webhook/internal/access"
	"github.com/nctiggy/claude-remote-session-webhook/internal/audit"
	"github.com/nctiggy/claude-remote-session-webhook/internal/auth"
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

// TestEmbeddedTemplatesRenderTheFleetPage proves the embedded page is a template
// that executes and not merely one that parses. It rendered a placeholder against
// no data until T014 gave it the fleet to render; what has survived that is the
// claim it was always making — the page references no external origin, because
// docs/security.md's CSP is sent unmodified and a CDN reference would fail in a
// browser rather than in review (SC-005 asserts this properly in T017).
func TestEmbeddedTemplatesRenderTheFleetPage(t *testing.T) {
	t.Parallel()

	var page strings.Builder
	if err := newTestServer(t, loopbackListen).templates.ExecuteTemplate(&page, "dashboard", fleetView{
		Operator: &access.VerifiedOperator{Email: testOperatorEmail, Owner: auth.CallerOperator},
		Empty:    emptyView{Title: emptyFleetTitle, Body: emptyFleetBody},
	}); err != nil {
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

// securityHeaders is docs/security.md's table, written out here rather than read
// from render.go's own constants: a test that compared the code against its own
// spelling would still pass on a policy that had quietly gained unsafe-inline or
// lost frame-ancestors, which is the edit this table exists to catch.
var securityHeaders = map[string]string{
	"Content-Security-Policy":   "default-src 'none'; script-src 'self'; style-src 'self'; connect-src 'self'; img-src 'self' data:; base-uri 'none'; form-action 'self'; frame-ancestors 'none'",
	"Strict-Transport-Security": "max-age=31536000; includeSubDomains",
	"X-Content-Type-Options":    "nosniff",
	"Referrer-Policy":           "no-referrer",
}

// browserResponses drives one response of every shape the browser door produces:
// a page and an asset layer 1 admitted, and a refusal on each kind of route.
// contracts/dashboard.md names all four — "pages, assets, refusals, the
// not-found page" — so a helper that swept only the served ones would leave the
// responses a stranger actually receives unasserted.
//
// Each case builds its own door, because there is no registered browser route to
// drive until T014 and because the action a door guards is what the cache rule
// reads.
func browserResponses(t *testing.T) map[string]*httptest.ResponseRecorder {
	t.Helper()

	admitted := func(action audit.Action) *httptest.ResponseRecorder {
		keys := newKeyServer(t)
		w := newDoorFor(t, keys.validator(t), action).request(keys.mint(t, keys.claims()))
		if w.Code != http.StatusOK {
			t.Fatalf("%s: a valid identity assertion was answered %d; want %d", action, w.Code, http.StatusOK)
		}
		return w
	}
	refused := func(action audit.Action) *httptest.ResponseRecorder {
		w := newDoorFor(t, newKeyServer(t).validator(t), action).request(absent)
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("%s: a request carrying no assertion was answered %d; want %d", action, w.Code, http.StatusUnauthorized)
		}
		return w
	}

	return map[string]*httptest.ResponseRecorder{
		"a page served":           admitted(audit.ActionDashboardView),
		"an asset served":         admitted(audit.ActionDashboardAsset),
		"a page refused":          refused(audit.ActionDashboardView),
		"an asset refused":        refused(audit.ActionDashboardAsset),
		"an unknown path refused": refused(audit.ActionUnknownRoute),
	}
}

// TestEveryBrowserResponseCarriesTheSecurityHeaders is FR-026: the four headers
// docs/security.md names, on everything this door answers with.
//
// The refusals matter as much as the pages. A refusal is still a document a
// browser renders, and it is the one response an unverified caller can reach —
// so a policy that arrived only with a successful page would be missing from
// every response an attacker sees.
func TestEveryBrowserResponseCarriesTheSecurityHeaders(t *testing.T) {
	t.Parallel()

	for name, w := range browserResponses(t) {
		for header, want := range securityHeaders {
			if got := w.Header().Get(header); got != want {
				t.Errorf("%s: %s = %q; want %q", name, header, got, want)
			}
		}
	}
}

// TestOnlyAServedAssetMayBeStored is the `Cache-Control` half.
//
// contracts/dashboard.md enumerates one exemption — the two embedded assets,
// which hold no session data — so everything else is `no-store`: a page carries
// session names and working directories, a stream carries pane content, and a
// refusal is an authorisation decision. Each is secret under docs/security.md §3
// or expires, and a cached copy is one that outlives what it described.
func TestOnlyAServedAssetMayBeStored(t *testing.T) {
	t.Parallel()

	const exempt = "an asset served"
	for name, w := range browserResponses(t) {
		got := w.Header().Get("Cache-Control")
		if name == exempt {
			if got != "" {
				t.Errorf("%s: Cache-Control = %q; the two embedded assets are the contract's one exemption, and caching them is what makes the page cheap", name, got)
			}
			continue
		}
		if got != "no-store" {
			t.Errorf("%s: Cache-Control = %q; want %q — this response is a page, a stream, or a decision", name, got, "no-store")
		}
	}
}

// TestABrowserRefusalLooksTheSameOnEveryRoute keeps the cache exemption from
// becoming a disclosure.
//
// The exemption is taken on the admit path alone. Taken before layer 1 instead,
// a refusal on an asset route would answer without the header a refusal on a
// page route carries — and that difference tells a stranger which paths this
// daemon really serves, which is exactly what the one uniform refusal (FR-010)
// exists to withhold.
func TestABrowserRefusalLooksTheSameOnEveryRoute(t *testing.T) {
	t.Parallel()

	refusals := map[string]http.Header{}
	for _, action := range []audit.Action{
		audit.ActionDashboardView,
		audit.ActionDashboardAsset,
		audit.ActionUnknownRoute,
		audit.ActionStreamOpen,
	} {
		refusals[string(action)] = newDoorFor(t, newKeyServer(t).validator(t), action).request(absent).Header().Clone()
	}

	names := slices.Sorted(maps.Keys(refusals))
	for _, name := range names[1:] {
		first, got := refusals[names[0]], refusals[name]
		if !maps.EqualFunc(got, first, func(a, b []string) bool { return strings.Join(a, "\x00") == strings.Join(b, "\x00") }) {
			t.Errorf("a refusal on a %s route answered with headers %v, but one on a %s route answered with %v; the route a refusal came from is not something the caller is owed",
				name, got, names[0], first)
		}
	}
}

// apiResponses drives every registered route twice — refused, and served — so a
// sweep can assert over what milestone 1's clients actually receive.
func apiResponses(t *testing.T) map[string]*httptest.ResponseRecorder {
	t.Helper()

	s := newAuditedServer(t)
	routes := s.Routes()
	if len(routes) == 0 {
		t.Fatal("the router registered no routes, so a sweep over them would pass vacuously")
	}

	out := map[string]*httptest.ResponseRecorder{}
	for i, route := range routes {
		unsigned := httptest.NewRecorder()
		s.ServeHTTP(unsigned, httptest.NewRequest(route.Method, pathFor(route), nil))
		out["unauthenticated "+route.String()] = unsigned

		// A distinct instant per route, because the signature covers the timestamp
		// and the body and not the method or path: identical empty-bodied requests
		// would share one signature and the replay cache would refuse the second.
		signed := httptest.NewRecorder()
		s.ServeHTTP(signed, requestFor(t, s, route, testTime.Add(-time.Duration(i)*time.Second)))
		if want := reachedStatus[route]; signed.Code != want {
			t.Fatalf("%s = %d; want %d — a sweep over a response no handler produced proves nothing", route, signed.Code, want)
		}
		out["signed "+route.String()] = signed
	}
	return out
}

// unroutedResponses drives the two shapes of request the contract has no route
// for. Since T016 they are the browser door's (FR-013d), and each is driven both
// bare and signed because a layer-2 signature is not an identity: this fixture's
// layer 1 admits nobody, so all four are that door's uniform refusal. They are
// swept for the absence that holds on both doors rather than for either door's
// headers, which browserResponses covers with a door it can admit through.
func unroutedResponses(t *testing.T) map[string]*httptest.ResponseRecorder {
	t.Helper()

	s := newAuditedServer(t)
	out := map[string]*httptest.ResponseRecorder{}
	for i, c := range []struct{ name, method, path string }{
		{"an unknown path", http.MethodGet, "/not-a-route"},
		{"a method no route answers", http.MethodPut, "/sessions"},
	} {
		unsigned := httptest.NewRecorder()
		s.ServeHTTP(unsigned, httptest.NewRequest(c.method, c.path, nil))
		out["unauthenticated "+c.name] = unsigned

		req := httptest.NewRequest(c.method, c.path, nil)
		signRequest(t, req, nil, testTime.Add(-time.Duration(i)*time.Second))
		signed := httptest.NewRecorder()
		s.ServeHTTP(signed, req)
		out["signed "+c.name] = signed
	}
	return out
}

// TestNoResponseOnEitherDoorCarriesACORSHeader is FR-034c, and it is a sweep
// rather than a reading of the code because the requirement is an absence: there
// is nothing to point at, so the only way to hold it is to look at everything.
//
// The browser's layer-1 credential is an ambient cookie — it rides on requests a
// hostile third-party page triggers, and the edge converts those into a valid
// assertion. Same-origin policy is what stops that page reading the answer, and
// a single Access-Control-Allow-Origin anywhere is the daemon opting out of it.
func TestNoResponseOnEitherDoorCarriesACORSHeader(t *testing.T) {
	t.Parallel()

	responses := browserResponses(t)
	maps.Copy(responses, apiResponses(t))
	maps.Copy(responses, unroutedResponses(t))

	for name, w := range responses {
		for header, values := range w.Header() {
			if strings.HasPrefix(strings.ToLower(header), "access-control-") {
				t.Errorf("%s answered with %s: %v; the daemon may never opt out of same-origin policy, on either door", name, header, values)
			}
		}
	}
}

// TestTheAPIDoorGainsNoBrowserHeaders is FR-014 from the header side: a header
// is part of a response, and milestone 1's six responses are frozen.
//
// The browser door's headers are for a document a person is looking at. Adding
// them to the API's JSON would change every answer a shipped client already
// receives — and the way that happens is a well-meaning "apply the security
// headers globally", which this sweep is here to fail.
func TestTheAPIDoorGainsNoBrowserHeaders(t *testing.T) {
	t.Parallel()

	for name, w := range apiResponses(t) {
		for header := range securityHeaders {
			if got := w.Header().Get(header); got != "" {
				t.Errorf("%s carries %s: %q; milestone 1's responses are frozen byte for byte", name, header, got)
			}
		}
		if got := w.Header().Get("Cache-Control"); got != "" {
			t.Errorf("%s carries Cache-Control: %q; milestone 1's responses are frozen byte for byte", name, got)
		}
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
