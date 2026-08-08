// Package updater moves a running daemon onto a published release.
//
// The order contracts/self-update.md fixes is the security property rather than
// a convenient sequence of helpers — fetch, checksum, signature, chmod, smoke
// test, rename, exit — so each step is its own file. A step that shares a file
// with the next one is a step somebody removes with an early return; a step
// that is a file is one they have to delete.
//
// **Nothing in this package installs anything by itself.** This file is step 1
// and it verifies nothing at all, deliberately: a fetcher that could also swap
// would mean verification was built after the thing it exists to protect
// (contracts/self-update.md, "the five tasks").
package updater

// fetch.go is step 1 — ask GitHub what a release published, and download
// exactly one named asset from it. It is the only file in this package that
// touches the network, and the only one that is allowed to.
//
// Three refusals live here. Each is a requirement, not caution:
//
//   - **TLS, always** (FR-026). A release fetched over plain HTTP can be
//     replaced by anyone on the path, and so can the checksum that travels
//     beside it. The scheme is checked on every URL, including the ones the API
//     hands back, because those arrive as data.
//   - **No redirect to a host a release does not come from** (FR-026).
//     net/http's default client follows a redirect anywhere, which turns one
//     bad response into a download from somebody else's server. The client here
//     carries its own CheckRedirect and an explicit list of hosts.
//   - **The asset named exactly** (FR-027). "Ends in `_arm64.tar.gz`" is the
//     looser check research.md R3 considered and rejected: a release carrying a
//     second asset with a matching tail is one where a loose matcher downloads
//     the wrong file and every later step verifies it happily.
//
// What it does *not* do is check the bytes. That is T015, and it is a separate
// file for the reason at the top of this one.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/nctiggy/claude-remote-session-webhook/internal/buildinfo"
)

const (
	// apiBase and repoPath are where a release is. The account name is
	// unavoidable here for the same reason contracts/installer.md gives for
	// install.sh naming it: it is where the bytes are, and there is nowhere
	// else to read it from at run time.
	apiBase  = "https://api.github.com"
	repoPath = "nctiggy/claude-remote-session-webhook"

	// ChecksumsAsset and SignatureAsset are the two assets whose names carry no
	// version, so unlike the tarball they are spelled identically in every
	// language that names them. T015 verifies the first against the second.
	ChecksumsAsset = "SHA256SUMS"
	SignatureAsset = ChecksumsAsset + ".sig"

	// maxAPIBytes bounds the release description, and maxAssetBytes the asset.
	// Both are read into memory, and an unbounded io.ReadAll of a response is an
	// out-of-memory the far end chooses the moment for. The asset limit is well
	// above a release tarball (single-digit megabytes) and well below anything
	// this daemon should hold; exceeding it is a refusal naming the limit.
	maxAPIBytes   = 1 << 20
	maxAssetBytes = 64 << 20

	// maxRedirects is below net/http's default of 10. Every redirect a release
	// legitimately takes is one hop, from the release page to whichever host
	// serves the bytes; a chain is something else happening.
	maxRedirects = 5

	// fetchTimeout is a backstop under the caller's context, not a replacement
	// for it. It covers the body as well as the response, so a server that
	// accepts the connection and then dribbles cannot hold an update open.
	fetchTimeout = 5 * time.Minute
)

// releaseHosts are the hosts a release may be served from, and the whole of the
// redirect policy.
//
// api.github.com answers what a release published; github.com is where the
// asset URLs it hands back point; the githubusercontent hosts are where those
// URLs redirect to for the bytes themselves. **A redirect anywhere else is
// refused rather than followed**, and the failure names the host it refused, so
// a host GitHub starts serving from that is not listed here costs an operator a
// message they can act on rather than a download from a stranger. That is the
// direction to be wrong in: refusing an update is always safe (FR-028), and
// following a redirect somewhere unexpected is the failure this list exists to
// prevent.
var releaseHosts = []string{
	"api.github.com",
	"github.com",
	"objects.githubusercontent.com",
	"release-assets.githubusercontent.com",
}

// versionShape is what may be pasted into an API path.
//
// The tag contracts/release.md fixes is `v0.<count>`; this is deliberately a
// little wider, so a future v1 is not a code change, and deliberately not "any
// string": T019 takes this value from a form field, and a version is
// concatenated into a URL path where `..` addresses a different endpoint. It is
// validated here rather than at the route because this is the boundary that
// builds the URL — the rule docs/security.md §2 states.
var versionShape = regexp.MustCompile(`^v[0-9]+(\.[0-9]+)*$`)

var (
	// ErrMalformedVersion is a version that is not shaped like a release tag.
	// Named so T019 can answer a bad form field differently from a release that
	// genuinely is not there.
	ErrMalformedVersion = errors.New("that is not the shape of a release version")

	// ErrAssetNotFound is a release that published nothing under exactly the
	// name asked for. It is a refusal rather than a fallback, which is FR-027:
	// the nearest asset is not the asset.
	ErrAssetNotFound = errors.New("the release publishes no asset under exactly that name")

	// ErrRedirectRefused is FR-026's second half.
	ErrRedirectRefused = errors.New("a release download was redirected to a host a release does not come from")

	// ErrInsecureTransport is FR-026's first half: a URL that is not https,
	// wherever it came from.
	ErrInsecureTransport = errors.New("a release may only be fetched over an authenticated transport")
)

// AssetName is the release asset carrying the binary for one version and one
// architecture — the third spelling of a name three languages have to agree on.
//
// The other two are in .github/workflows/release.yml (YAML) and install.sh
// (shell). They cannot share a constant, so research.md R3 accepted the
// duplication and made the drift preventable instead:
// internal/release's TestAssetNamesAgreeAcrossLanguages reads all three and
// requires them to be the same string. A drift has no symptom until somebody
// updates, at which point it is a 404 from a project that looks broken.
func AssetName(version, arch string) string {
	return "crswd_" + version + "_linux_" + arch + ".tar.gz"
}

// Release is one published version, as the API described it.
type Release struct {
	// Version is the tag this release actually carries, which is not
	// necessarily the one that was asked for — an empty request means whatever
	// the `latest` pointer resolves to, and the answer is what the daemon has
	// to name in the asset it then asks for.
	Version string

	// Notes is what the release said about itself — GitHub's `body`, which is
	// the text an author wrote on the release page (issue #103).
	//
	// It is carried because an operator deciding whether to take an update is
	// deciding what they would be taking, and a version number alone does not
	// say. It is a stranger's text as far as this daemon is concerned: it
	// arrives over the network, before any signature has been checked, and
	// nothing in this package acts on it. Whatever renders it renders it as
	// text — html/template does that by construction, which is the rule
	// docs/security.md gives pane output and this is the same kind of byte.
	//
	// A release that published no notes carries none, and the page says so
	// rather than showing an empty panel: absence is a fact about the release.
	Notes string

	// assets maps an exact asset name to where its bytes are. Unexported, so
	// the only URLs this package will fetch are ones a release named: a caller
	// asks for an asset by name and cannot ask for an address.
	assets map[string]string
}

// Fetcher downloads release assets. It holds no state about an update in
// progress; one is safe to keep for the life of the daemon.
type Fetcher struct {
	api    string
	repo   string
	hosts  []string
	client *http.Client
}

// NewFetcher returns a Fetcher pointed at this project's releases.
func NewFetcher() *Fetcher {
	return newFetcher(apiBase, repoPath, releaseHosts, nil)
}

// newFetcher is the constructor with everything about "which releases" and
// "over what" supplied, so a test can serve its own release without either the
// network or a relaxed policy: the transport is the only thing that changes,
// and CheckRedirect below is exactly the one the daemon runs.
func newFetcher(api, repo string, hosts []string, transport http.RoundTripper) *Fetcher {
	f := &Fetcher{
		api:   strings.TrimSuffix(api, "/"),
		repo:  repo,
		hosts: append([]string(nil), hosts...),
	}
	f.client = &http.Client{
		Timeout:       fetchTimeout,
		Transport:     transport, // nil is http.DefaultTransport, which is what the daemon uses.
		CheckRedirect: f.checkRedirect,
	}
	return f
}

// Release asks what one version published. An empty version asks for whatever
// the `latest` pointer resolves to; naming one is what makes a rollback an
// ordinary update (FR-022).
func (f *Fetcher) Release(ctx context.Context, version string) (*Release, error) {
	endpoint := f.api + "/repos/" + f.repo + "/releases/latest"
	if version != "" {
		if !versionShape.MatchString(version) {
			// The value is not echoed. It arrives from a form field, and a
			// refusal that quotes it is a refusal that carries whatever was
			// sent into a log.
			return nil, ErrMalformedVersion
		}
		endpoint = f.api + "/repos/" + f.repo + "/releases/tags/" + version
	}

	body, err := f.get(ctx, endpoint, maxAPIBytes)
	if err != nil {
		return nil, fmt.Errorf("ask for release %q: %w", version, err)
	}

	// DisallowUnknownFields is the rule at this project's own boundaries
	// (docs/security.md §2) and would be wrong here: the GitHub API answers with
	// dozens of fields this daemon has no opinion about, and adding one is a
	// thing it does without telling anybody. What is constrained instead is what
	// is *read* — three fields, all strings, none reaching a filesystem or a
	// command line. The third is the release's own description, which is read
	// and shown to an operator and acted on by nothing.
	var described struct {
		TagName string `json:"tag_name"`
		Body    string `json:"body"`
		Assets  []struct {
			Name string `json:"name"`
			URL  string `json:"browser_download_url"`
		} `json:"assets"`
	}
	if err := json.Unmarshal(body, &described); err != nil {
		return nil, fmt.Errorf("read the description of release %q: %w", version, err)
	}
	if described.TagName == "" {
		return nil, fmt.Errorf("the description of release %q names no tag", version)
	}

	rel := &Release{
		Version: described.TagName,
		Notes:   described.Body,
		assets:  make(map[string]string, len(described.Assets)),
	}
	for _, a := range described.Assets {
		if _, dup := rel.assets[a.Name]; dup {
			// "The asset named exactly this" has to identify one file. Two of
			// them is an ambiguity to refuse rather than resolve by order.
			return nil, fmt.Errorf("release %s publishes two assets named %q", rel.Version, a.Name)
		}
		rel.assets[a.Name] = a.URL
	}
	return rel, nil
}

// Asset returns the bytes of exactly the named asset, and nothing that resembles
// it (FR-027).
//
// It writes nothing: staging the result at 0600 is T016's, and it is separate
// so that bytes cannot reach the filesystem before something has decided they
// are the published ones.
func (f *Fetcher) Asset(ctx context.Context, rel *Release, name string) ([]byte, error) {
	src, published := rel.assets[name]
	if !published {
		return nil, fmt.Errorf("release %s: %q: %w", rel.Version, name, ErrAssetNotFound)
	}

	body, err := f.get(ctx, src, maxAssetBytes)
	if err != nil {
		return nil, fmt.Errorf("download %s from release %s: %w", name, rel.Version, err)
	}
	return body, nil
}

// get is every request this package makes: check the address, ask, bound the
// answer.
func (f *Fetcher) get(ctx context.Context, endpoint string, limit int64) ([]byte, error) {
	target, err := url.Parse(endpoint)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", endpoint, err)
	}
	// The first URL meets the same rule as every redirect after it. One of them
	// arrives as data from the API, so "we built this one ourselves" is not true
	// of the request that matters most.
	if err := f.allow(target); err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("build a request for %s: %w", target.Redacted(), err)
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	// GitHub answers an unauthenticated request with no User-Agent with a 403,
	// which would read as "this release is not there". Read from the variable
	// per request rather than at construction, the same reason
	// GET /dashboard/version does.
	req.Header.Set("User-Agent", "crswd/"+buildinfo.Version)

	resp, err := f.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch %s: %w", target.Redacted(), err)
	}
	defer resp.Body.Close() //nolint:errcheck // The body is fully read or abandoned; a close failure says nothing about the bytes.

	if resp.StatusCode != http.StatusOK {
		// The status and the address, never the body: it is a stranger's bytes
		// and errors go to the journal.
		return nil, fmt.Errorf("fetch %s: %s", target.Redacted(), resp.Status)
	}

	// One byte past the limit, so "exactly the limit" and "too large" are
	// distinguishable rather than a silent truncation that later fails a
	// checksum for a reason nobody can see.
	body, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", target.Redacted(), err)
	}
	if int64(len(body)) > limit {
		return nil, fmt.Errorf("fetch %s: larger than the %d byte limit", target.Redacted(), limit)
	}
	return body, nil
}

// checkRedirect is what makes this client refuse what the default one follows.
func (f *Fetcher) checkRedirect(req *http.Request, via []*http.Request) error {
	if len(via) >= maxRedirects {
		return fmt.Errorf("fetch %s: more than %d redirects", req.URL.Redacted(), maxRedirects)
	}
	return f.allow(req.URL)
}

// allow is FR-026 in one place, so the initial request and every redirect are
// held to the same rule.
//
// The comparison is against Host rather than Hostname: a redirect to another
// port on a host a release does come from is not a release either, and treating
// the two as the same host would let one through.
func (f *Fetcher) allow(target *url.URL) error {
	if target.Scheme != "https" {
		return fmt.Errorf("%s: %w", target.Redacted(), ErrInsecureTransport)
	}
	for _, host := range f.hosts {
		if target.Host == host {
			return nil
		}
	}
	return fmt.Errorf("%s: a release comes from %s: %w", target.Host, strings.Join(f.hosts, ", "), ErrRedirectRefused)
}
