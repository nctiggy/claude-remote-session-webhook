//go:build quickstart

// Milestone 2's acceptance run: specs/002-access-dashboard/quickstart.md,
// executed against a real build on a real port rather than eyeballed (T034).
//
// It is the companion to quickstart_test.go and shares that file's harness — the
// same built binary, the same isolated tmux server, the same signing helper — so
// the two milestones drive one daemon rather than two descriptions of one.
//
// Three departures from the literal shell in quickstart.md. None weakens an
// assertion, and each is recorded here because the document is the contract:
//
//   - The local identity edge is a Go httptest server and a Go-generated RSA key
//     pair rather than openssl, python3 and /tmp/crswd-idp. It publishes the same
//     Cloudflare-shaped /cdn-cgi/access/certs, and every assertion below is built
//     the way RFC 7515 describes a JWS rather than by calling internal/access —
//     a fixture built with the code under test proves only that the code agrees
//     with itself.
//   - The listener is a free port, never 8765. The document writes 8765
//     throughout, which is the port the deployed daemon holds on the one host an
//     operator would run this on; the address is not what any story is about.
//   - Story 2's payload is sent as two prompts rather than one. A tmux pane wraps
//     at its width, and a wrapped line is a screen the daemon rendered correctly
//     and a substring assertion cannot see. Both payloads still arrive, and the
//     claim — every byte renders as text, nothing executes — is unchanged.
//
// What this file deliberately does not claim to have run: the greyscale, the
// keyboard walk and the reduced-motion check (SC-009, SC-010, SC-011). Go cannot
// render a page, and a test asserting a CSS rule exists is not the check the
// document asks for. They are recorded as outstanding in ralph/PROGRESS.md
// rather than quietly reported as passing here.

package main

import (
	"bufio"
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

const (
	// dashKeyID is quickstart.md's "local-test-key": the one key id the local
	// edge publishes. Its value is arbitrary; that an assertion has to name a
	// published one is not.
	dashKeyID = "local-test-key"

	// dashOperator is the address on the allowlist and dashIntruder is one the
	// edge would have verified just as thoroughly and this daemon still refuses.
	dashOperator = "operator@example.com"
	dashIntruder = "intruder@example.com"

	// dashHeaderAssertion is the header Cloudflare Access writes and the browser
	// door reads.
	dashHeaderAssertion = "Cf-Access-Jwt-Assertion"

	// noAssertion sends no header at all, which is a different shape from
	// sending an empty one: it is the first line of the negative sweep.
	noAssertion = "\x00absent\x00"
)

// dashKeys generates the pair once for the whole run. Two 2048-bit keys per test
// would be most of this suite's wall clock, and nothing here depends on a
// per-test key: the unpublished one is unpublished in every case that uses it.
var dashKeys = sync.OnceValues(func() (*rsa.PrivateKey, *rsa.PrivateKey) {
	published, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		panic("generate the acceptance run's published key: " + err.Error())
	}
	unpublished, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		panic("generate the acceptance run's unpublished key: " + err.Error())
	}
	return published, unpublished
})

// edge is quickstart.md's "Setup — a local identity edge": one key pair the
// daemon will trust, one it must not, and a key server on loopback serving the
// Cloudflare-shaped certs path.
//
// http on loopback is what config.loadTeamDomain permits for exactly this
// reason, and it is the whole of why this suite needs no Cloudflare account.
type edge struct {
	t *testing.T

	published   *rsa.PrivateKey
	unpublished *rsa.PrivateKey

	srv    *httptest.Server
	origin string

	// aud is this application's audience tag. It is random per run for the
	// reason the shared secret is: a fixture value that leaked into the daemon's
	// own defaults would make the audience check untestable.
	aud string

	mu   sync.Mutex
	asks []string
}

func newEdge(t *testing.T) *edge {
	t.Helper()

	published, unpublished := dashKeys()
	e := &edge{t: t, published: published, unpublished: unpublished, aud: dashAUD(t)}

	// A mux rather than a handler that answers everything, so that the daemon
	// asking for some other path fails here rather than being served a key set
	// it should never have found.
	mux := http.NewServeMux()
	mux.HandleFunc("/cdn-cgi/access/certs", func(w http.ResponseWriter, r *http.Request) {
		e.mu.Lock()
		e.asks = append(e.asks, r.URL.Path)
		e.mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		if _, err := w.Write(e.keySet()); err != nil {
			t.Errorf("serve the local key set: %v", err)
		}
	})

	e.srv = httptest.NewServer(mux)
	e.origin = e.srv.URL
	t.Cleanup(e.srv.Close)
	return e
}

// close is quickstart.md's "kill the key server": the fail-closed case (FR-009).
func (e *edge) close() { e.srv.Close() }

func (e *edge) fetches() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return len(e.asks)
}

// dashAUD is the document's `head -c 32 /dev/urandom | od -An -tx1`.
func dashAUD(t *testing.T) string {
	t.Helper()

	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		t.Fatalf("read an audience tag: %v", err)
	}
	return hex.EncodeToString(raw)
}

// keySet is the JWK set the edge publishes, in the shape RFC 7517 defines and
// Cloudflare serves.
func (e *edge) keySet() []byte {
	e.t.Helper()

	b, err := json.Marshal(map[string]any{"keys": []map[string]string{{
		"kty": "RSA",
		"kid": dashKeyID,
		"alg": "RS256",
		"use": "sig",
		"n":   base64.RawURLEncoding.EncodeToString(e.published.PublicKey.N.Bytes()),
		"e":   base64.RawURLEncoding.EncodeToString(big.NewInt(int64(e.published.PublicKey.E)).Bytes()),
	}}})
	if err != nil {
		e.t.Fatalf("encode the local key set: %v", err)
	}
	return b
}

// claims is quickstart.md's claims() helper: the identity shape, every parameter
// overridable so the negative sweep can lie in one dimension at a time.
func (e *edge) claims(email string, ttl time.Duration, aud string) map[string]any {
	now := time.Now()
	return map[string]any{
		"aud":   []string{aud},
		"iss":   e.origin,
		"exp":   now.Add(ttl).Unix(),
		"iat":   now.Add(-time.Minute).Unix(),
		"nbf":   now.Add(-time.Minute).Unix(),
		"sub":   "user",
		"email": email,
	}
}

// operatorClaims is the assertion a valid operator carries.
func (e *edge) operatorClaims() map[string]any {
	return e.claims(dashOperator, 10*time.Minute, e.aud)
}

// serviceTokenClaims is the other documented shape, and the sweep's most
// important line: a genuine signature, audience and issuer, naming a credential
// rather than a person (FR-013c).
func (e *edge) serviceTokenClaims() map[string]any {
	c := e.operatorClaims()
	delete(c, "email")
	c["sub"] = ""
	c["common_name"] = "0123456789abcdef.access"
	return c
}

func dashHeader(kid, alg string) map[string]any {
	return map[string]any{"alg": alg, "kid": kid, "typ": "JWT"}
}

// mint signs claims with the published key under a genuine JOSE header.
func (e *edge) mint(claims map[string]any) string {
	e.t.Helper()
	return e.mintWith(e.published, dashHeader(dashKeyID, "RS256"), claims)
}

func (e *edge) mintWith(key *rsa.PrivateKey, header, claims map[string]any) string {
	e.t.Helper()

	signed := dashSegment(e.t, header) + "." + dashSegment(e.t, claims)
	digest := sha256.Sum256([]byte(signed))
	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, digest[:])
	if err != nil {
		e.t.Fatalf("sign an assertion: %v", err)
	}
	return signed + "." + base64.RawURLEncoding.EncodeToString(sig)
}

// unsigned is the `alg: none` line: a header, a payload, and a trailing dot
// where a signature would be.
func (e *edge) unsigned(claims map[string]any) string {
	e.t.Helper()
	return dashSegment(e.t, map[string]any{"alg": "none", "typ": "JWT"}) + "." + dashSegment(e.t, claims) + "."
}

func dashSegment(t *testing.T, v map[string]any) string {
	t.Helper()

	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("encode an assertion segment: %v", err)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

// startDashboard is quickstart.md's daemon, pointed at the local key server.
func (h *host) startDashboard(e *edge, over map[string]string) *daemon {
	h.t.Helper()

	if over == nil {
		over = map[string]string{}
	}
	over["CRSW_ACCESS_TEAM_DOMAIN"] = e.origin
	over["CRSW_ACCESS_AUD"] = e.aud
	over["CRSW_ACCESS_ALLOWED_EMAILS"] = dashOperator
	return h.startBinary(h.bin, over)
}

// browse is quickstart.md's dash(): a GET carrying a browser assertion and no
// signature at all.
func (d *daemon) browse(path, assertion string) response {
	d.t.Helper()

	req, err := http.NewRequest(http.MethodGet, "http://"+d.addr+path, nil)
	if err != nil {
		d.t.Fatalf("build a browser request for %s: %v", path, err)
	}
	if assertion != noAssertion {
		req.Header.Set(dashHeaderAssertion, assertion)
	}
	return d.do(req)
}

// ---------------------------------------------------------------------------
// Story 1 (P1) — the fleet at a glance
// ---------------------------------------------------------------------------

// externalOrigin is the document's `grep -Ec '(src|href)="https?://'`.
var externalOrigin = regexp.MustCompile(`(?:src|href)="https?://`)

func TestDashboardQuickstartStory1Fleet(t *testing.T) {
	h := newHost(t)
	e := newEdge(t)
	d := h.startDashboard(e, nil)

	jwt := e.mint(e.operatorClaims())

	// Empty first (FR-021): the explanatory state, not a blank region, with no
	// "start a session" action (FR-024a) and the operator's address in the
	// header (FR-020).
	//
	// Two of these assertions were narrowed by milestone 3's T010, and the
	// narrowing is worth reading before it is trusted. They used to be page-wide,
	// and they used to hold that this dashboard could not act at all: the copy
	// read "This dashboard only watches — sessions are started through the API",
	// and no string anywhere on the page was allowed to offer to start one.
	//
	// That is the precise limitation milestone 3 exists to remove — its spec opens
	// by calling this "the read-only fleet dashboard from milestone 2 becomes able
	// to act" — so the page now carries a create form, outside the empty state,
	// and the two assertions could not survive as written by any means other than
	// naming the control something it is not.
	//
	// What they assert instead is every claim they were really making, scoped to
	// the section they were about: the empty state explains itself rather than
	// rendering a blank region, it renders no action row of its own, and it does
	// not itself offer a control. FR-024a is untouched — the component's Action
	// parameter is still absent, now because docs/design-system.md keeps a form
	// off the full-strength rain behind this section rather than because no route
	// exists to take it. **T023 owes this edit a ratification**: its own wording
	// says a story needing changes to accommodate this milestone is a regression
	// to fix in the code, and this is the one case where the code doing the
	// accommodating is the milestone's own plan.
	empty := d.browse("/", jwt)
	if empty.Status != http.StatusOK {
		t.Fatalf("GET / = %d, want 200: %s", empty.Status, empty.Body)
	}
	page := string(empty.Body)
	for _, want := range []string{"No sessions running", "Nothing is executing on this host right now", dashOperator} {
		if !strings.Contains(page, want) {
			t.Errorf("the empty fleet does not say %q:\n%s", want, page)
		}
	}
	if strings.Contains(page, "empty-action") {
		t.Error("the empty state renders an action row; this milestone passes none (FR-024a)")
	}

	// The section itself, not the page. Sliced at its own closing tag, which the
	// create form's section comes after — so a control that migrated into the
	// empty state is caught here exactly as it was before.
	emptySection := page
	if _, after, ok := strings.Cut(page, `<section class="empty">`); ok {
		emptySection, _, _ = strings.Cut(after, "</section>")
	} else {
		t.Errorf("the page renders no empty state at all:\n%s", page)
	}
	for _, offer := range []string{"start a session", "<form", "<button"} {
		if strings.Contains(strings.ToLower(emptySection), offer) {
			t.Errorf("the empty state offers %q; the rain runs at full strength behind it, and the design system keeps a control off the rain:\n%s", offer, emptySection)
		}
	}

	// SC-005 and FR-034c, the document's three greps — against the headers as
	// well as the body, because that is what `curl -D-` puts in front of grep.
	whole := empty.fingerprint()
	if n := len(externalOrigin.FindAllString(page, -1)); n != 0 {
		t.Errorf("the page references %d external origins, want 0", n)
	}
	if strings.Contains(strings.ToLower(whole), "access-control-") {
		t.Errorf("a CORS header is present on the fleet page:\n%s", whole)
	}
	if got := empty.Header.Values("Content-Security-Policy"); len(got) != 1 {
		t.Errorf("%d Content-Security-Policy headers, want exactly 1: %q", len(got), got)
	}

	// Two sessions through milestone 1's own API, then reload.
	first := d.createSession("alpha")
	second := d.createSession("beta")
	d.waitForPane(first.ID, first.Token, shimReady)

	fleet := d.browse("/", jwt)
	if fleet.Status != http.StatusOK {
		t.Fatalf("GET / with two sessions = %d, want 200: %s", fleet.Status, fleet.Body)
	}
	page = string(fleet.Body)

	// The summary row comes before any detail (FR-017): a dashboard is scanned.
	summary, grid := strings.Index(page, `class="summary"`), strings.Index(page, `class="grid"`)
	switch {
	case summary < 0:
		t.Errorf("no summary row rendered above the fleet:\n%s", page)
	case grid < 0:
		t.Errorf("no card grid rendered:\n%s", page)
	case summary > grid:
		t.Error("the summary row renders after the cards it summarises")
	}

	// The opener without its closing bracket, which is how the other seven counts
	// of a card in this repository are spelled. This one pinned the whole tag, so
	// it was an assertion about the card's attribute list rather than about how
	// many cards a fleet of two renders — and T014 gave the card the identifier
	// the fleet stream names it by (data-session). Nothing about the claim here
	// changed: one card per owned session, on a page composed exactly as it was.
	if cards := strings.Count(page, `<article class="card"`); cards != 2 {
		t.Errorf("%d cards rendered, want one per owned session (2):\n%s", cards, page)
	}
	for _, want := range []string{first.ID, second.ID, "alpha", "beta"} {
		if !strings.Contains(page, want) {
			t.Errorf("the fleet does not name %q", want)
		}
	}

	// FR-019: every state is a text label, so greyscale loses nothing. The pill
	// carries the state as its text node and not only in its class.
	if !strings.Contains(page, `<span class="pill pill-running">running</span>`) {
		t.Errorf("the running state is not rendered as a text label:\n%s", page)
	}

	// Every card links to the page the card links to, and it exists (T021b).
	view := d.browse("/sessions/"+first.ID+"/view", jwt)
	if view.Status != http.StatusOK {
		t.Errorf("the card's own link = %d, want 200: %s", view.Status, view.Body)
	}

	// The trail carries one record per browser request and no assertion.
	views := 0
	for _, rec := range d.records() {
		if rec.Action == "dashboard.view" {
			views++
		}
	}
	if views != 3 {
		t.Errorf("%d dashboard.view records, want one per browser request (3)", views)
	}
	for _, forbidden := range []string{jwt, dashOperator} {
		if strings.Contains(d.readTrail(), forbidden) {
			t.Errorf("the trail carries %q", forbidden)
		}
	}
}

// TestDashboardQuickstartStory1Adopted is FR-018a: a session the daemon adopted
// after a restart has no name and no working directory, and the card says so
// rather than inventing one.
func TestDashboardQuickstartStory1Adopted(t *testing.T) {
	h := newHost(t)
	e := newEdge(t)
	addr := freePort(t)
	d := h.startDashboard(e, map[string]string{"CRSW_LISTEN": addr})

	c := d.createSession("outlives-the-daemon")
	d.waitForPane(c.ID, c.Token, shimReady)

	// SIGKILL, not SIGTERM: a termination signal reaps the fleet on the way out,
	// so the state this case is about is only reachable through a crash.
	_ = d.stop(syscall.SIGKILL)
	if !h.hasSession("crswd-" + c.ID) {
		t.Fatal("the session did not outlive the crash, so there is nothing to adopt")
	}

	restarted := h.startDashboard(e, map[string]string{"CRSW_LISTEN": addr})
	page := string(restarted.browse("/", e.mint(e.operatorClaims())).Body)

	for _, want := range []string{"no name recorded", "no working directory recorded"} {
		if !strings.Contains(page, want) {
			t.Errorf("the adopted card does not state %q:\n%s", want, page)
		}
	}
	if strings.Contains(page, "outlives-the-daemon") {
		t.Error("the adopted card carries the name from before the restart, which the daemon does not record")
	}
	if strings.Contains(page, h.workDir) {
		t.Errorf("the adopted card carries a working directory the daemon never recorded (%s)", h.workDir)
	}
}

// ---------------------------------------------------------------------------
// Story 2 (P2) — watch a session, hostilely
// ---------------------------------------------------------------------------

// dashPayloads are quickstart.md's two hostile payloads, sent one per prompt so
// that neither wraps in an 80-column pane.
var dashPayloads = []string{
	`echo "<script>alert(1)</script>"`,
	`echo "<img src=x onerror=alert(2)>"`,
}

func TestDashboardQuickstartStory2View(t *testing.T) {
	h := newHost(t)
	e := newEdge(t)
	d := h.startDashboard(e, nil)

	c := d.createSession("hostile")
	d.waitForPane(c.ID, c.Token, shimReady)
	for _, payload := range dashPayloads {
		body, err := json.Marshal(map[string]string{"text": payload})
		if err != nil {
			t.Fatalf("encode the prompt: %v", err)
		}
		if resp := d.call(http.MethodPost, "/sessions/"+c.ID+"/prompt", string(body), c.Token); resp.Status != http.StatusAccepted {
			t.Fatalf("prompt %q = %d, want 202: %s", payload, resp.Status, resp.Body)
		}
		d.waitForPane(c.ID, c.Token, shimEcho+payload)
	}

	page := string(d.browse("/sessions/"+c.ID+"/view", e.mint(e.operatorClaims())).Body)

	// SC-004: every byte renders as visible text and nothing executes. The
	// escaped spelling is asserted as well as the raw one — an absent raw
	// payload alone would also be what a page that dropped it looks like.
	pane := paneOf(t, page)
	for _, want := range []string{"&lt;script&gt;alert(1)&lt;/script&gt;", "&lt;img src=x onerror=alert(2)&gt;"} {
		if !strings.Contains(pane, want) {
			t.Errorf("the pane does not carry %q as text:\n%s", want, pane)
		}
	}
	// The raw spellings are the ones a browser would act on, and they are the
	// tags rather than their contents: `onerror=alert(2)` is inert text once the
	// angle brackets around its element are escaped, and asserting its absence
	// would be asserting that the payload never arrived.
	for _, forbidden := range []string{"<script>alert(1)", "<img src=x"} {
		if strings.Contains(page, forbidden) {
			t.Errorf("the page carries %q unescaped, so it would execute:\n%s", forbidden, page)
		}
	}

	// FR-032a: the page says what it is showing.
	if !strings.Contains(page, "This is the live screen, not scrollback") {
		t.Errorf("the session page does not say it shows the live screen:\n%s", page)
	}
	// FR-029: no terminal control bytes reach the browser.
	if strings.ContainsRune(page, 0x1b) {
		t.Error("the session page carries an ESC byte")
	}
}

// paneOf is the <pre> the pane viewer renders, with the pane's own line breaks
// removed: tmux wraps at the pane width, and a wrapped payload is a screen the
// daemon rendered correctly that a substring assertion cannot see.
func paneOf(t *testing.T, page string) string {
	t.Helper()

	open := strings.Index(page, `<pre class="pane"`)
	if open < 0 {
		t.Fatalf("no pane rendered on the session page:\n%s", page)
	}
	start := strings.Index(page[open:], ">")
	if start < 0 {
		t.Fatalf("the pane element is not closed:\n%s", page[open:])
	}
	start += open + 1

	end := strings.Index(page[start:], "</pre>")
	if end < 0 {
		t.Fatalf("the pane element has no end:\n%s", page[start:])
	}
	return strings.ReplaceAll(page[start:start+end], "\n", "")
}

// watcher is one open stream, read off the wire by a goroutine so the test can
// assert about what has arrived without ever blocking on what has not.
type watcher struct {
	t      *testing.T
	resp   *http.Response
	cancel context.CancelFunc
	done   chan struct{}

	mu    sync.Mutex
	lines []string
}

// watch opens the stream quickstart.md opens with `curl -sN`. It sends no
// Sec-Fetch-Site, which is the absent case the open sequence admits (FR-034d) —
// curl sends none either.
func (d *daemon) watch(id, assertion string) *watcher {
	d.t.Helper()

	ctx, cancel := context.WithCancel(context.Background())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+d.addr+"/sessions/"+id+"/stream", nil)
	if err != nil {
		cancel()
		d.t.Fatalf("build the stream request: %v", err)
	}
	if assertion != noAssertion {
		req.Header.Set(dashHeaderAssertion, assertion)
	}

	// No client timeout: a stream is meant to stay open, and a Timeout here
	// would cut exactly the thing under test.
	resp, err := (&http.Client{}).Do(req)
	if err != nil {
		cancel()
		d.t.Fatalf("open the stream: %v", err)
	}

	w := &watcher{t: d.t, resp: resp, cancel: cancel, done: make(chan struct{})}
	go w.read()
	d.t.Cleanup(w.close)
	return w
}

func (w *watcher) read() {
	defer close(w.done)

	sc := bufio.NewScanner(w.resp.Body)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for sc.Scan() {
		w.mu.Lock()
		w.lines = append(w.lines, sc.Text())
		w.mu.Unlock()
	}
}

func (w *watcher) snapshot() []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]string(nil), w.lines...)
}

// waitFor polls what has arrived until it satisfies the caller, and fails
// naming what it was waiting for rather than hanging the run.
func (w *watcher) waitFor(what string, ok func([]string) bool) []string {
	w.t.Helper()

	deadline := time.Now().Add(waitBudget)
	for time.Now().Before(deadline) {
		if lines := w.snapshot(); ok(lines) {
			return lines
		}
		time.Sleep(50 * time.Millisecond)
	}
	w.t.Fatalf("timed out waiting for %s; the stream carried:\n%s", what, strings.Join(w.snapshot(), "\n"))
	return nil
}

func (w *watcher) close() {
	w.cancel()
	_ = w.resp.Body.Close()
	<-w.done
}

// dataLines is every `data:` field the stream has written, decoded from the JSON
// string each one carries.
func dataLines(t *testing.T, lines []string) []string {
	t.Helper()

	var out []string
	for _, line := range lines {
		raw, ok := strings.CutPrefix(line, "data: ")
		if !ok {
			continue
		}
		var screen string
		if err := json.Unmarshal([]byte(raw), &screen); err != nil {
			t.Errorf("a data field is not one JSON string: %q: %v", raw, err)
			continue
		}
		out = append(out, screen)
	}
	return out
}

func TestDashboardQuickstartStory2Stream(t *testing.T) {
	h := newHost(t)
	e := newEdge(t)
	d := h.startDashboard(e, nil)
	jwt := e.mint(e.operatorClaims())

	c := d.createSession("watched")
	d.waitForPane(c.ID, c.Token, shimReady)

	w := d.watch(c.ID, jwt)
	if w.resp.StatusCode != http.StatusOK {
		t.Fatalf("open the stream = %d, want 200", w.resp.StatusCode)
	}

	// The current screen arrives immediately, as one data line holding one JSON
	// string (contracts/stream.md).
	lines := w.waitFor("the opening screen", func(lines []string) bool {
		return len(dataLines(t, lines)) >= 1
	})
	opening := dataLines(t, lines)[0]
	if !strings.Contains(strings.ReplaceAll(opening, "\n", ""), shimReady) {
		t.Errorf("the opening screen is not this session's:\n%s", opening)
	}

	// New output arrives without reconnecting (SC-006).
	if resp := d.call(http.MethodPost, "/sessions/"+c.ID+"/prompt", `{"text":"marker-one"}`, c.Token); resp.Status != http.StatusAccepted {
		t.Fatalf("prompt = %d, want 202: %s", resp.Status, resp.Body)
	}
	w.waitFor("the prompted output on the open stream", func(lines []string) bool {
		for _, screen := range dataLines(t, lines) {
			if strings.Contains(screen, shimEcho+"marker-one") {
				return true
			}
		}
		return false
	})

	// A quiet screen produces comment heartbeats and never a repeated screen
	// (FR-034b): three ticks of silence, then count.
	before := len(dataLines(t, w.snapshot()))
	time.Sleep(3 * streamTick)
	quiet := w.snapshot()
	if got := len(dataLines(t, quiet)); got != before {
		t.Errorf("a quiet screen pushed %d more events; the screen did not change", got-before)
	}
	heartbeats := 0
	for _, line := range quiet {
		if line == ":" {
			heartbeats++
		}
	}
	if heartbeats == 0 {
		t.Errorf("a quiet stream wrote no heartbeat at all:\n%s", strings.Join(quiet, "\n"))
	}

	// FR-029: no terminal control bytes anywhere on the wire.
	for _, line := range quiet {
		if strings.ContainsRune(line, 0x1b) {
			t.Errorf("the stream carries an ESC byte: %q", line)
		}
	}

	// FR-034f, watching is not driving — read through GET /sessions, which does
	// not itself advance the idle clock. The per-session read does, so the two
	// readings quickstart.md takes would move whether or not a stream is open.
	activity := lastActivity(t, d, c.ID)
	time.Sleep(2 * streamTick)
	if now := lastActivity(t, d, c.ID); now != activity {
		t.Errorf("last_activity moved from %s to %s while a stream was open", activity, now)
	}

	// FR-033, SC-015: destroying the session ends the stream and says so.
	if resp := d.call(http.MethodDelete, "/sessions/"+c.ID, "", c.Token); resp.Status != http.StatusOK {
		t.Fatalf("destroy = %d, want 200: %s", resp.Status, resp.Body)
	}
	ended := w.waitFor("the terminal event", func(lines []string) bool {
		for _, line := range lines {
			if line == "event: end" {
				return true
			}
		}
		return false
	})
	select {
	case <-w.done:
	case <-time.After(waitBudget):
		t.Errorf("the stream did not close after the terminal event:\n%s", strings.Join(ended, "\n"))
	}

	// One stream.open record, written at the open rather than at the close
	// (T027): it is in the trail while the stream is still running above.
	opens := 0
	for _, rec := range d.records() {
		if rec.Action == "stream.open" {
			opens++
			if rec.SessionID != c.ID {
				t.Errorf("stream.open names session %q, want %q", rec.SessionID, c.ID)
			}
		}
	}
	if opens != 1 {
		t.Errorf("%d stream.open records, want 1", opens)
	}

	// SC-008: no session output in any record or log line.
	trail := d.readTrail()
	for _, forbidden := range []string{shimEcho, "marker-one", shimReady, jwt} {
		if strings.Contains(trail, forbidden) {
			t.Errorf("the trail carries %q", forbidden)
		}
	}
}

// streamTick is the daemon's capture interval (internal/httpapi/stream.go). The
// waits above are expressed in it rather than in seconds so that a change to the
// cadence moves them together.
const streamTick = time.Second

func lastActivity(t *testing.T, d *daemon, id string) string {
	t.Helper()

	resp := d.call(http.MethodGet, "/sessions", "", "")
	if resp.Status != http.StatusOK {
		t.Fatalf("GET /sessions = %d, want 200: %s", resp.Status, resp.Body)
	}
	var listed struct {
		Sessions []struct {
			ID           string `json:"id"`
			LastActivity string `json:"last_activity"`
		} `json:"sessions"`
	}
	if err := json.Unmarshal(resp.Body, &listed); err != nil {
		t.Fatalf("decode the list: %v", err)
	}
	for _, s := range listed.Sessions {
		if s.ID == id {
			return s.LastActivity
		}
	}
	t.Fatalf("session %s is not in the list: %s", id, resp.Body)
	return ""
}

// TestDashboardQuickstartStory2Cap is FR-034e: the cap refuses, and recovers.
func TestDashboardQuickstartStory2Cap(t *testing.T) {
	h := newHost(t)
	e := newEdge(t)
	d := h.startDashboard(e, map[string]string{"CRSW_MAX_STREAMS": "2"})
	jwt := e.mint(e.operatorClaims())

	c := d.createSession("watched-twice")
	d.waitForPane(c.ID, c.Token, shimReady)

	first := d.watch(c.ID, jwt)
	second := d.watch(c.ID, jwt)
	for i, w := range []*watcher{first, second} {
		if w.resp.StatusCode != http.StatusOK {
			t.Fatalf("stream %d = %d, want 200", i+1, w.resp.StatusCode)
		}
		// Both really are serving, which is the half a status code does not say.
		w.waitFor(fmt.Sprintf("stream %d's opening screen", i+1), func(lines []string) bool {
			return len(dataLines(t, lines)) >= 1
		})
	}

	third := d.watch(c.ID, jwt)
	if third.resp.StatusCode != http.StatusTooManyRequests {
		t.Errorf("the third stream past a cap of 2 = %d, want 429", third.resp.StatusCode)
	}
	third.close()

	// Close one, and the slot comes back.
	first.close()
	recovered := d.watch(c.ID, jwt)
	if recovered.resp.StatusCode != http.StatusOK {
		t.Errorf("a stream opened after one closed = %d, want 200", recovered.resp.StatusCode)
	}
	recovered.close()
	second.close()
}

// ---------------------------------------------------------------------------
// Story 3 (P3) — the negative sweep
// ---------------------------------------------------------------------------

func TestDashboardQuickstartStory3Sweep(t *testing.T) {
	h := newHost(t)
	e := newEdge(t)
	d := h.startDashboard(e, nil)

	// Ordered rather than a map, so the log below reads in the document's order.
	sweep := []struct {
		name      string
		assertion string
	}{
		{"absent", noAssertion},
		{"malformed", "not-a-jwt"},
		{"expired", e.mint(e.claims(dashOperator, -10*time.Minute, e.aud))},
		{"wrong key, known kid", e.mintWith(e.unpublished, dashHeader(dashKeyID, "RS256"), e.operatorClaims())},
		{"unknown kid", e.mintWith(e.published, dashHeader("ghost", "RS256"), e.operatorClaims())},
		{"alg: none", e.unsigned(e.operatorClaims())},
		{"wrong audience", e.mint(e.claims(dashOperator, 10*time.Minute, "deadbeef"))},
		{"disallowed email", e.mint(e.claims(dashIntruder, 10*time.Minute, e.aud))},
		{"service-token shape", e.mint(e.serviceTokenClaims())},
	}

	var first string
	for i, tc := range sweep {
		resp := d.browse("/", tc.assertion)
		if resp.Status != http.StatusForbidden && resp.Status != http.StatusUnauthorized {
			t.Errorf("%s = %d, want a refusal: %s", tc.name, resp.Status, resp.Body)
		}

		// FR-010, SC-001: byte-identical, headers included. A difference in
		// Content-Length alone is an oracle.
		got := resp.fingerprint()
		if i == 0 {
			first = got
			t.Logf("the uniform refusal is:\n%s", got)
			continue
		}
		if got != first {
			t.Errorf("%s is distinguishable from the first refusal:\n%s\nwant:\n%s", tc.name, got, first)
		}
	}

	// The valid assertion is served, so the sweep is refusing on what it names
	// and not on something the daemon does to every request.
	if resp := d.browse("/", e.mint(e.operatorClaims())); resp.Status != http.StatusOK {
		t.Fatalf("the valid assertion = %d, want 200: %s", resp.Status, resp.Body)
	}

	// FR-010, SC-008: the trail says why; the response never does.
	rejects := 0
	for _, rec := range d.records() {
		if rec.Action == "access.reject" {
			rejects++
			if rec.Reason == "" {
				t.Error("an access.reject record carries no reason")
			}
		}
	}
	if rejects != len(sweep) {
		t.Errorf("%d access.reject records, want one per refusal (%d)", rejects, len(sweep))
	}

	trail := d.readTrail()
	for _, forbidden := range []string{dashIntruder, "0123456789abcdef.access"} {
		if strings.Contains(trail, forbidden) {
			t.Errorf("the trail carries %q, which the refused caller supplied", forbidden)
		}
	}
	for _, tc := range sweep {
		if tc.assertion == noAssertion || tc.assertion == "not-a-jwt" {
			continue
		}
		if strings.Contains(trail, tc.assertion) {
			t.Errorf("the trail carries the %s assertion", tc.name)
		}
	}

	// One fetch, not one per request: an outage the daemon rides out rather than
	// amplifies. The key set is fetched when an assertion first names a key.
	if got := e.fetches(); got > 2 {
		t.Errorf("the daemon fetched the key set %d times for %d assertions", got, len(sweep)+1)
	}
}

// TestDashboardQuickstartStory3FailsClosed is FR-009: with the keys unobtainable
// and the cache empty, the *valid* assertion is refused exactly as the rest are,
// and the daemon neither crashes nor hangs.
func TestDashboardQuickstartStory3FailsClosed(t *testing.T) {
	h := newHost(t)
	e := newEdge(t)
	addr := freePort(t)

	d := h.startDashboard(e, map[string]string{"CRSW_LISTEN": addr})
	jwt := e.mint(e.operatorClaims())
	served := d.browse("/", jwt)
	if served.Status != http.StatusOK {
		t.Fatalf("GET / with the key server up = %d, want 200: %s", served.Status, served.Body)
	}
	refused := d.browse("/", "not-a-jwt").fingerprint()

	// Kill the key server, restart the daemon so the cache is empty, and present
	// the assertion that just worked.
	e.close()
	if err := d.stop(syscall.SIGTERM); err != nil {
		t.Fatalf("stop the daemon: %v", err)
	}
	restarted := h.startDashboard(e, map[string]string{"CRSW_LISTEN": addr})

	done := make(chan response, 1)
	go func() { done <- restarted.browse("/", jwt) }()
	select {
	case resp := <-done:
		if resp.Status == http.StatusOK {
			t.Fatalf("an assertion was admitted with the key set unobtainable: %s", resp.Body)
		}
		if got := resp.fingerprint(); got != refused {
			t.Errorf("the fail-closed refusal is distinguishable from the rest:\n%s\nwant:\n%s", got, refused)
		}
	case <-time.After(waitBudget):
		t.Fatal("the daemon hung on a request it could not verify")
	}

	// Still answering: refusing is not falling over.
	if resp := restarted.browse("/", jwt); resp.Status == http.StatusOK {
		t.Error("the second request was admitted with the key set still gone")
	}
}

// ---------------------------------------------------------------------------
// Story 4 (P4) — the existing client keeps working
// ---------------------------------------------------------------------------

func TestDashboardQuickstartStory4Coexistence(t *testing.T) {
	h := newHost(t)
	e := newEdge(t)
	d := h.startDashboard(e, nil)
	jwt := e.mint(e.operatorClaims())

	// Milestone 1's create → list → destroy, unchanged, against a daemon that
	// now validates browser identities (FR-014).
	c := d.createSession("still-works")
	if resp := d.call(http.MethodGet, "/sessions", "", ""); resp.Status != http.StatusOK {
		t.Fatalf("GET /sessions = %d, want 200: %s", resp.Status, resp.Body)
	}

	// FR-012, one door each: an API request carrying a browser assertion as well
	// is served exactly as before.
	both := d.request(http.MethodGet, "/sessions/"+c.ID, "", c.Token, time.Now().Unix())
	both.Header.Set(dashHeaderAssertion, jwt)
	if resp := d.do(both); resp.Status != http.StatusOK {
		t.Errorf("a signed request also carrying an assertion = %d, want 200: %s", resp.Status, resp.Body)
	}

	// And the other half: a browser request carries no signature and is not
	// refused for lacking one.
	if resp := d.browse("/sessions/"+c.ID+"/view", jwt); resp.Status != http.StatusOK {
		t.Errorf("a browser request with no signature = %d, want 200: %s", resp.Status, resp.Body)
	}

	// FR-013d: a path the daemon serves no route for is answered by the door
	// that owns the dashboard — the HTML not-found page, not the API's JSON.
	missing := d.browse("/no-such-page", jwt)
	if missing.Status != http.StatusNotFound {
		t.Errorf("an unrouted path = %d, want 404: %s", missing.Status, missing.Body)
	}
	if ct := missing.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("an unrouted path answered %q, want text/html", ct)
	}
	if !strings.Contains(string(missing.Body), dashOperator) {
		t.Error("the not-found page does not carry the verified identity the rest of the interface does")
	}

	// The routed API paths stay the API's, which is the same rule read the other
	// way round: each door refuses only by the check that applies to it. A
	// browser on GET /sessions is a caller with no signature on a signed route.
	api := d.browse("/sessions", jwt)
	if api.Status != http.StatusUnauthorized {
		t.Errorf("a browser on the API's own route = %d, want 401: %s", api.Status, api.Body)
	}
	if ct := api.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("the API door answered %q, want application/json", ct)
	}

	if resp := d.call(http.MethodDelete, "/sessions/"+c.ID, "", c.Token); resp.Status != http.StatusOK {
		t.Errorf("destroy = %d, want 200: %s", resp.Status, resp.Body)
	}

	// FR-016, SC-008: every request above is in the trail exactly once, served
	// or refused.
	if len(d.records()) == 0 {
		t.Error("the trail is empty")
	}
}

// ---------------------------------------------------------------------------
// Story 5 (P5) — the bypass exists only where it may
// ---------------------------------------------------------------------------

// TestDashboardQuickstartStory5ShippingArtifact is FR-041 and SC-012: the flag
// does not exist, so the artifact refuses to start with it.
func TestDashboardQuickstartStory5ShippingArtifact(t *testing.T) {
	h := newHost(t)

	out, code := h.runBinary(h.bin, map[string]string{"CRSW_LISTEN": freePort(t)}, "--dev-auth-bypass")
	if code == 0 {
		t.Fatalf("the shipping artifact started with --dev-auth-bypass:\n%s", out)
	}
	if !strings.Contains(out, "flag provided but not defined") {
		t.Errorf("the refusal does not say the flag does not exist:\n%s", out)
	}
	t.Logf("shipping artifact: exit=%d %s", code, firstLine(out))
}

// TestDashboardQuickstartStory5DevelopmentArtifact is FR-038 through FR-042: the
// development build serves without an assertion, warns on every request, keeps
// layer 2, and refuses a listener that is not loopback.
func TestDashboardQuickstartStory5DevelopmentArtifact(t *testing.T) {
	h := newHost(t)
	dev := h.buildDev()

	// FR-042: the three layer-1 values are not demanded. Unset here, so a build
	// that still demanded them would not start at all.
	noLayer1 := map[string]string{
		"CRSW_ACCESS_TEAM_DOMAIN":    unset,
		"CRSW_ACCESS_AUD":            unset,
		"CRSW_ACCESS_ALLOWED_EMAILS": unset,
	}

	d := h.startBinary(dev, noLayer1, "--dev-auth-bypass")

	// Served with no assertion at all, and FR-040's warning is per request.
	for i := 1; i <= 2; i++ {
		resp := d.browse("/", noAssertion)
		if resp.Status != http.StatusOK {
			t.Fatalf("request %d with the bypass active = %d, want 200: %s", i, resp.Status, resp.Body)
		}
		if warnings := strings.Count(d.readTrail(), "served with NO verified browser identity"); warnings != i {
			t.Errorf("%d per-request warnings after %d requests", warnings, i)
		}
	}
	if !strings.Contains(d.readTrail(), "built with -tags dev") {
		t.Error("the bypass did not announce itself at startup")
	}

	// FR-038: layers 2 and 3 are untouched. An unsigned mutation is still 401.
	if resp := d.call(http.MethodPost, "/sessions", "", ""); resp.Status == http.StatusCreated {
		t.Error("the bypass admitted an API write; it must skip layer 1 only")
	}
	unsigned, err := http.NewRequest(http.MethodPost, "http://"+d.addr+"/sessions", strings.NewReader(`{}`))
	if err != nil {
		t.Fatalf("build an unsigned create: %v", err)
	}
	if resp := d.do(unsigned); resp.Status != http.StatusUnauthorized {
		t.Errorf("an unsigned create with the bypass active = %d, want 401: %s", resp.Status, resp.Body)
	}

	// FR-039: not off loopback, in either build.
	public := freeAddrOn(t, "0.0.0.0")
	out, code := h.runBinary(dev, map[string]string{"CRSW_LISTEN": public}, "--dev-auth-bypass")
	if code == 0 {
		t.Errorf("the development artifact bound %s with the bypass active:\n%s", public, out)
	}
	if !strings.Contains(out, "loopback") {
		t.Errorf("the refusal does not name the listener:\n%s", out)
	}

	// And without the flag it is the daemon it always was: the same demand for
	// the same three values.
	out, code = h.runBinary(dev, noLayer1)
	if code == 0 {
		t.Errorf("the development build started without layer-1 configuration and without the flag:\n%s", out)
	}
	if !strings.Contains(out, "CRSW_ACCESS_TEAM_DOMAIN") {
		t.Errorf("the refusal does not name the missing configuration:\n%s", out)
	}
}

// buildDev builds the development artifact, which is the same command with the
// bypass compiled in.
func (h *host) buildDev() string {
	h.t.Helper()

	bin := filepath.Join(h.dir, "crswd-dev")
	cmd := exec.Command("go", "build", "-tags", "dev", "-o", bin, "./cmd/crswd")
	cmd.Dir = "../.."
	if out, err := cmd.CombinedOutput(); err != nil {
		h.t.Fatalf("go build -tags dev ./cmd/crswd: %v\n%s", err, out)
	}
	return bin
}
