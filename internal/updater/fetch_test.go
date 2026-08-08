package updater

// What fetch.go has to be true of, checked by serving it a release.
//
// An internal test, because the thing under test is a policy rather than a
// return value: the daemon's own client — its CheckRedirect, its scheme check,
// its exact-name lookup — has to be the one exercised, and the only thing a
// test is allowed to change is where the bytes come from. A test that built its
// own http.Client would be asserting about a client nothing ships.
//
// The servers are TLS, and reached under the name example.com rather than
// 127.0.0.1, for the same reason: FR-026 is about hosts, so a test about it has
// to have more than one host in it. httptest's certificate is issued for
// example.com as well as 127.0.0.1, so both names verify against the real
// TLSClientConfig and nothing here relaxes verification.
//
// The two failures worth naming:
//
//   - the default client, which follows a redirect anywhere. Every legitimate
//     download redirects once, so "it works" says nothing at all — which is why
//     TestCrossHostRedirectRefused asserts a same-host redirect *is* followed
//     beside the cross-host one that is not, and why the far end is a real
//     server that records being asked.
//   - matching an asset by suffix. The release carries a second asset ending in
//     the same nine characters, so a matcher that takes the tail downloads the
//     wrong version's binary and every later step verifies it happily.

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"
)

const (
	// testHost is a name httptest's certificate is issued for, so a request to
	// it verifies against the same TLSClientConfig the daemon uses.
	testHost = "example.com"

	testRepo    = "an-owner/a-repo"
	testVersion = "v0.42"
)

// apiAsset is the part of GitHub's release description this package reads.
type apiAsset struct {
	Name string `json:"name"`
	URL  string `json:"browser_download_url"`
}

// releaseBody is what the API answers, plus a field nothing here reads — the
// real API sends dozens, and a decoder that refused them would fail against a
// release that is perfectly fine.
func releaseBody(t *testing.T, tag string, assets []apiAsset) []byte {
	t.Helper()

	raw, err := json.Marshal(struct {
		TagName string     `json:"tag_name"`
		Draft   bool       `json:"draft"`
		Assets  []apiAsset `json:"assets"`
	}{TagName: tag, Assets: assets})
	if err != nil {
		t.Fatalf("marshal the release description: %v", err)
	}
	return raw
}

// recorder counts what a server was asked for, so a test can assert about a
// request that should never have been made. "Nothing was downloaded" is the
// claim in half the cases here, and it is not visible in an error value.
type recorder struct {
	mu   sync.Mutex
	seen []string
}

func (r *recorder) record(path string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.seen = append(r.seen, path)
}

func (r *recorder) paths() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.seen...)
}

// serve starts a TLS server and closes it with the test.
func serve(t *testing.T, h http.Handler) *httptest.Server {
	t.Helper()

	srv := httptest.NewTLSServer(h)
	t.Cleanup(srv.Close)
	return srv
}

// answer writes a whole response, and reports a write that did not finish
// rather than dropping it. A half-sent release would reach the assertions as a
// fetch that failed, and the reason would be this file rather than the daemon.
func answer(t *testing.T, w http.ResponseWriter, body []byte) {
	t.Helper()

	// Bytes, declared as bytes. Nothing here is a document, and a response with
	// no declared type is one a browser would sniff — which is not this
	// daemon's failure mode, but it is not a habit worth having in a file that
	// serves attacker-shaped names back.
	w.Header().Set("Content-Type", "application/octet-stream")
	if _, err := w.Write(body); err != nil {
		t.Errorf("serve %d bytes: %v", len(body), err)
	}
}

// fetcherFor returns the daemon's Fetcher pointed at srv under the name
// example.com. Only the dial is redirected; the TLS configuration, the
// redirect policy and every check in fetch.go are the shipped ones.
//
// hosts is what the fetcher will accept, exactly as releaseHosts is in
// production. Anything not listed is a host a release does not come from.
func fetcherFor(t *testing.T, srv *httptest.Server, hosts ...string) *Fetcher {
	t.Helper()

	trusted, ok := srv.Client().Transport.(*http.Transport)
	if !ok {
		t.Fatalf("httptest gave a %T rather than an *http.Transport; this test needs its TLS roots", srv.Client().Transport)
	}
	transport := trusted.Clone()

	// example.com resolves nowhere. Everything else — including the second
	// server in the cross-host case — is dialled for real, so a redirect that
	// is wrongly followed reaches something that answers.
	addr := srv.Listener.Addr().String()
	var dialer net.Dialer
	transport.DialContext = func(ctx context.Context, network, target string) (net.Conn, error) {
		if strings.HasPrefix(target, testHost+":") {
			target = addr
		}
		return dialer.DialContext(ctx, network, target)
	}

	if len(hosts) == 0 {
		hosts = []string{testHost}
	}
	return newFetcher("https://"+testHost, testRepo, hosts, transport)
}

// tagPath and assetPath are where this test's server keeps things.
func tagPath(version string) string { return "/repos/" + testRepo + "/releases/tags/" + version }

func assetURL(scheme, host, name string) string { return scheme + "://" + host + "/assets/" + name }

// TestAssetMatchedByExactName is FR-027.
//
// The release below carries four assets whose names are close to the one asked
// for: another architecture, another version with the same tail, the signature
// beside it, and the checksum file. A matcher that takes a prefix, a suffix or
// a substring picks one of them, and what it picks is a binary that passes
// every later check in this package — it is a real release asset, correctly
// signed, for something other than what the operator asked to run.
func TestAssetMatchedByExactName(t *testing.T) {
	t.Parallel()

	want := AssetName(testVersion, "amd64")
	names := []string{
		want,
		AssetName(testVersion, "arm64"),
		// Same tail as `want`, a different release. This is what a suffix
		// matcher takes.
		AssetName("v0.99", "amd64"),
		// Same head as `want`. This is what a prefix matcher takes.
		want + ".sig",
		ChecksumsAsset,
		SignatureAsset,
	}

	// The server answers only for what the release published, rather than
	// echoing whatever path it was asked for. A handler that built its answer
	// out of the request would serve every name equally well, including the
	// ones this test needs to come back as a refusal.
	published := make([]apiAsset, 0, len(names))
	bodies := make(map[string][]byte, len(names))
	for _, name := range names {
		published = append(published, apiAsset{Name: name, URL: assetURL("https", testHost, name)})
		bodies["/assets/"+name] = []byte("the bytes of " + name)
	}

	srv := serve(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == tagPath(testVersion) {
			answer(t, w, releaseBody(t, testVersion, published))
			return
		}
		if body, ok := bodies[r.URL.Path]; ok {
			answer(t, w, body)
			return
		}
		http.NotFound(w, r)
	}))

	f := fetcherFor(t, srv)
	rel, err := f.Release(context.Background(), testVersion)
	if err != nil {
		t.Fatalf("Release(%q): %v", testVersion, err)
	}
	if rel.Version != testVersion {
		t.Fatalf("Release(%q) describes %q", testVersion, rel.Version)
	}

	tests := []struct {
		name string
		ask  string
		body string
	}{
		{name: "the exact tarball", ask: want, body: "the bytes of " + want},
		{name: "the checksums", ask: ChecksumsAsset, body: "the bytes of " + ChecksumsAsset},
		{name: "the signature", ask: SignatureAsset, body: "the bytes of " + SignatureAsset},

		// Every one of these matches a published asset under some looser rule.
		{name: "a suffix of a published name", ask: "_linux_amd64.tar.gz"},
		{name: "a prefix of a published name", ask: "crswd_" + testVersion + "_linux_amd64.tar"},
		{name: "a substring of a published name", ask: "linux_amd64"},
		{name: "the empty name", ask: ""},
		{name: "an asset this release does not carry", ask: AssetName("v0.41", "amd64")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := f.Asset(context.Background(), rel, tt.ask)
			if tt.body == "" {
				if !errors.Is(err, ErrAssetNotFound) {
					t.Fatalf("Asset(%q) = %q, %v; want ErrAssetNotFound.\nThat name matches a published asset only under a looser rule than \"named exactly this\", and what it matches is a real, correctly signed release asset for something else", tt.ask, got, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("Asset(%q): %v", tt.ask, err)
			}
			if string(got) != tt.body {
				t.Errorf("Asset(%q) returned %q, want %q.\nThe name asked for and the bytes returned are for different assets", tt.ask, got, tt.body)
			}
		})
	}
}

// TestCrossHostRedirectRefused is FR-026's second half.
//
// Both halves are here on purpose. A client that refuses every redirect passes
// the cross-host case and cannot download a release at all, because the
// legitimate path is a redirect from the release page to whatever serves the
// bytes — so the same-host case has to pass beside it.
//
// The far end is a real server that records being asked, rather than an address
// nothing answers on: with net/http's default client the refused case does not
// merely fail differently, it succeeds, and the daemon ends up holding bytes
// from somewhere no release comes from.
func TestCrossHostRedirectRefused(t *testing.T) {
	t.Parallel()

	var elsewhereSeen recorder
	elsewhere := serve(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		elsewhereSeen.record(r.URL.Path)
		answer(t, w, []byte("bytes from a host a release does not come from"))
	}))

	// 127.0.0.1:port — a different host from example.com, dialled for real, and
	// httptest's certificate is valid for it, so a client that follows the
	// redirect completes the download rather than failing on TLS.
	const (
		crossHost = "cross-host"
		sameHost  = "same-host"
	)
	published := []apiAsset{
		{Name: crossHost, URL: assetURL("https", testHost, crossHost)},
		{Name: sameHost, URL: assetURL("https", testHost, sameHost)},
	}

	srv := serve(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case tagPath(testVersion):
			answer(t, w, releaseBody(t, testVersion, published))
		case "/assets/" + crossHost:
			http.Redirect(w, r, elsewhere.URL+"/assets/"+crossHost, http.StatusFound)
		case "/assets/" + sameHost:
			http.Redirect(w, r, assetURL("https", testHost, "moved/"+sameHost), http.StatusFound)
		case "/assets/moved/" + sameHost:
			answer(t, w, []byte("the published bytes"))
		default:
			http.NotFound(w, r)
		}
	}))

	f := fetcherFor(t, srv)
	rel, err := f.Release(context.Background(), testVersion)
	if err != nil {
		t.Fatalf("Release(%q): %v", testVersion, err)
	}

	got, err := f.Asset(context.Background(), rel, crossHost)
	if !errors.Is(err, ErrRedirectRefused) {
		t.Errorf("a redirect to %s was not refused: %q, %v.\nnet/http's default client follows a redirect anywhere, which turns one response from the release host into a download from somebody else's server", elsewhere.URL, got, err)
	}
	if asked := elsewhereSeen.paths(); len(asked) != 0 {
		t.Errorf("the daemon fetched %v from %s.\nThose bytes reached this host, and every check after this one would have been applied to them", asked, elsewhere.URL)
	}

	// The other half: the shape every real download takes.
	got, err = f.Asset(context.Background(), rel, sameHost)
	if err != nil {
		t.Fatalf("a redirect within %s was refused: %v.\nA release is served through exactly this hop, so refusing it is a daemon that can never update", testHost, err)
	}
	if string(got) != "the published bytes" {
		t.Errorf("following the redirect returned %q", got)
	}
}

// TestInsecureTransportRefused is FR-026's first half, in the two places a
// plain-HTTP URL can arrive: named by the API, and reached by a redirect.
//
// Neither is hypothetical. The asset address is data from a response, and a
// redirect chosen by whoever answered it; a client that checks the scheme only
// on the URL it built itself checks the one URL nobody could have chosen.
func TestInsecureTransportRefused(t *testing.T) {
	t.Parallel()

	var plainSeen recorder
	plain := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		plainSeen.record(r.URL.Path)
		answer(t, w, []byte("bytes nobody authenticated"))
	}))
	t.Cleanup(plain.Close)

	const (
		named      = "named-over-http"
		redirected = "redirected-to-http"
	)
	published := []apiAsset{
		// The API itself names an unauthenticated address.
		{Name: named, URL: plain.URL + "/assets/" + named},
		{Name: redirected, URL: assetURL("https", testHost, redirected)},
	}

	srv := serve(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case tagPath(testVersion):
			answer(t, w, releaseBody(t, testVersion, published))
		case "/assets/" + redirected:
			// A downgrade to the same host, which a host-only check allows.
			http.Redirect(w, r, assetURL("http", testHost, redirected), http.StatusFound)
		default:
			http.NotFound(w, r)
		}
	}))

	// The plain server's host is listed as somewhere a release may come from,
	// so the only thing left to refuse it is the scheme.
	f := fetcherFor(t, srv, testHost, plain.Listener.Addr().String())
	rel, err := f.Release(context.Background(), testVersion)
	if err != nil {
		t.Fatalf("Release(%q): %v", testVersion, err)
	}

	for _, name := range []string{named, redirected} {
		got, err := f.Asset(context.Background(), rel, name)
		if !errors.Is(err, ErrInsecureTransport) {
			t.Errorf("Asset(%q) = %q, %v; want ErrInsecureTransport.\nA release fetched over plain HTTP can be replaced by anyone on the path, and so can the checksum that travels beside it", name, got, err)
		}
	}
	if asked := plainSeen.paths(); len(asked) != 0 {
		t.Errorf("the daemon fetched %v over plain HTTP", asked)
	}
}

// TestOversizedResponseRefused is the bound on what a stranger's server can
// make this daemon hold.
//
// **The size of the body is not the assertion; when reading stops is.** A
// length check after an unbounded io.ReadAll refuses exactly the same responses
// and refuses them a gigabyte too late — the allocation has already happened,
// on a host running the operator's sessions, and it is the far end that chose
// how big. So the server here writes one byte past the limit and then never
// finishes: a fetcher that stops at the limit returns immediately, and one that
// reads to the end of the body is still waiting.
//
// Refusing at the limit rather than truncating there is the other half. A short
// read would arrive at T015 as a checksum that does not match, which is the
// same symptom as a corrupted download and a different cause.
func TestOversizedResponseRefused(t *testing.T) {
	t.Parallel()

	const limit = 64

	// Closed by the cleanup below, which runs before the server's own — so the
	// handler is released whatever the test does.
	unblock := make(chan struct{})
	srv := serve(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := w.Write([]byte(strings.Repeat("a", limit+1))); err != nil {
			return
		}
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
		if r.URL.Path == "/endless" {
			<-unblock
		}
	}))
	t.Cleanup(func() { close(unblock) })

	f := fetcherFor(t, srv)

	refused := make(chan error, 1)
	go func() {
		_, err := f.get(context.Background(), "https://"+testHost+"/endless", limit)
		refused <- err
	}()

	select {
	case err := <-refused:
		if err == nil {
			t.Errorf("a body of %d bytes was accepted under a %d byte limit", limit+1, limit)
		}
	case <-time.After(10 * time.Second):
		t.Errorf("the fetch is still reading a body that does not end.\nThe limit is being applied to what was read rather than to the reading, so the refusal costs whatever the far end chose to send before it arrives")
	}

	// The boundary itself, against a body that does end: exactly the limit is a
	// release, not an attack.
	if _, err := f.get(context.Background(), "https://"+testHost+"/finite", limit+1); err != nil {
		t.Errorf("a body of exactly the limit was refused: %v", err)
	}
}

// TestVersionNamesOneEndpoint covers FR-021 and FR-022 — the update that takes
// whatever is newest, and the named one that makes a rollback possible — and
// the boundary between them.
//
// The version arrives from a form field at T019 and is concatenated into a URL
// path here, so the malformed cases are the point of the test: a value that
// escapes the path addresses a different endpoint, and one that reaches the
// network at all has already been trusted.
func TestVersionNamesOneEndpoint(t *testing.T) {
	t.Parallel()

	var asked recorder
	srv := serve(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked.record(r.URL.Path)
		switch r.URL.Path {
		case "/repos/" + testRepo + "/releases/latest":
			answer(t, w, releaseBody(t, "v0.43", nil))
		case tagPath(testVersion):
			answer(t, w, releaseBody(t, testVersion, nil))
		default:
			http.NotFound(w, r)
		}
	}))
	f := fetcherFor(t, srv)

	rel, err := f.Release(context.Background(), "")
	if err != nil {
		t.Fatalf(`Release(""): %v`, err)
	}
	if rel.Version != "v0.43" {
		t.Errorf(`Release("") resolved to %q, want the tag the latest pointer holds`, rel.Version)
	}

	rel, err = f.Release(context.Background(), testVersion)
	if err != nil {
		t.Fatalf("Release(%q): %v", testVersion, err)
	}
	if rel.Version != testVersion {
		t.Errorf("Release(%q) resolved to %q; a rollback lands on whatever this says", testVersion, rel.Version)
	}

	before := len(asked.paths())

	for _, bad := range []string{
		"latest",
		"../latest",
		testVersion + "/../../../../repos/somebody/else/releases/latest",
		testVersion + "?draft=true",
		"v0.42; rm -rf ~",
		"nightly",
		"V0.42",
		"0.42",
	} {
		if _, err := f.Release(context.Background(), bad); !errors.Is(err, ErrMalformedVersion) {
			t.Errorf("Release(%q) = %v; want ErrMalformedVersion.\nIt is pasted into an API path and arrives from a form field", bad, err)
		}
	}

	if after := asked.paths(); len(after) != before {
		t.Errorf("a malformed version reached the network: %v.\nIt has to be refused before a request is built, not by whatever answers", after[before:])
	}
}

// TestTheDaemonAndTheInstallerFetchTheSameProject holds the one fact this file
// and install.sh both have to spell, and neither can read from the other.
//
// They are two halves of one promise: the installer places a binary from a
// project, and that binary then replaces itself from a project. If those are
// not the same project, an operator who installed this one is updated by
// another — and the signature would be checked against the key committed here,
// which is the check they would expect to catch exactly that.
func TestTheDaemonAndTheInstallerFetchTheSameProject(t *testing.T) {
	t.Parallel()

	const installer = "../../install.sh"

	raw, err := os.ReadFile(installer)
	if err != nil {
		t.Fatalf("read %s: %v", installer, err)
	}

	declared := regexp.MustCompile(`(?m)^readonly REPO_URL="([^"]+)"`).FindStringSubmatch(string(raw))
	if declared == nil {
		t.Fatalf("%s declares no REPO_URL.\nIf it moved rather than went away, move this pattern with it — these two files agree by nothing but this test", installer)
	}

	if want := "https://github.com/" + repoPath; declared[1] != want {
		t.Errorf("%s downloads from %s; this daemon updates itself from %s.\nWhichever is wrong, the result is a host that installs one project and updates into another", installer, declared[1], want)
	}
}

// TestFetcherIsTheDaemonsOwnClient pins the two things about the shipped
// Fetcher that the tests above cannot see, because they replace exactly these:
// where a release is looked for, and which hosts it may come from.
//
// A test-only default would leave every assertion above passing against a
// daemon pointed at nothing.
func TestFetcherIsTheDaemonsOwnClient(t *testing.T) {
	t.Parallel()

	f := NewFetcher()

	if f.api != apiBase || f.repo != repoPath {
		t.Errorf("NewFetcher asks %s about %s; a release is at %s about %s", f.api, f.repo, apiBase, repoPath)
	}
	if !strings.HasPrefix(f.api, "https://") {
		t.Errorf("NewFetcher asks %s, which is not authenticated", f.api)
	}
	if f.client.CheckRedirect == nil {
		t.Error("NewFetcher's client has no CheckRedirect, so it follows a redirect anywhere — which is the whole of FR-026's second half")
	}
	if f.client.Timeout == 0 {
		t.Error("NewFetcher's client has no timeout, so a server that accepts the connection and then says nothing holds an update open forever")
	}

	// The transport is the standard one, and stays nil rather than being set to
	// a copy of it: a Fetcher carrying its own TLSClientConfig is where
	// certificate verification could be turned off without a test noticing,
	// because every test above supplies a transport of its own.
	if f.client.Transport != nil {
		t.Errorf("NewFetcher's client carries a %T rather than http.DefaultTransport", f.client.Transport)
	}

	for _, host := range f.hosts {
		if host == "" || strings.Contains(host, "/") {
			t.Errorf("%q is not a host; it is compared against a URL's Host, which carries no path", host)
		}
	}
	if len(f.hosts) == 0 {
		t.Error("NewFetcher accepts no hosts at all, so it can download nothing")
	}
}
