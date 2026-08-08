package httpapi

// releasenotes.go is the Updates section of GET /settings (issue #103): the
// version installed now, the version available, what each of them said about
// itself, and nothing else.
//
// **The page's first job needs no network at all, and this file is written so
// that stays true.** Everything else on /settings is the Config resolved at
// startup — it cannot be slow and it cannot fail — while the notes come from
// GitHub, which is a host this daemon may not be able to reach at all. So every
// failure here is a section that says it could not read the release feed, on a
// page that has already rendered every configured value above it. There is no
// path from an unreachable feed to a 500, an empty page, or a settings page an
// offline operator cannot open.
//
// Two bounds make "cannot be slow" more than an intention:
//
//   - **A deadline of its own**, under the request's context. The fetcher's own
//     timeout is five minutes, which is right for downloading a release and
//     absurd for composing a page.
//   - **A cache**, because the unauthenticated GitHub API allows sixty requests
//     an hour from one address and a lookup spends two of them. Without it,
//     thirty page loads would exhaust an operator's budget and the thirty-first
//     would report the feed as unreachable — which would be true, and caused by
//     this page.
//
// Nothing here is written to the audit trail. Reading a public release feed is
// not an action on this host, the record the middleware already emits says who
// read the settings page, and a second record per page load would be counting
// this daemon's own outbound curiosity as the operator's doing.

import (
	"context"
	"sync"
	"time"

	"github.com/nctiggy/claude-remote-session-webhook/internal/buildinfo"
)

const (
	// releaseLookupTimeout is how long composing this section may spend on the
	// network in total, across both requests.
	//
	// It is short because of what is waiting on it: an operator who opened
	// /settings to read a value they configured. Two seconds is longer than the
	// API takes when it is reachable and shorter than a person waits before
	// deciding a page is broken — and when it expires, what they get is the
	// whole page with one section saying the feed could not be read.
	releaseLookupTimeout = 2 * time.Second

	// releaseFeedTTL is how long an answer is reused. Releases are published
	// every few days at most, so a quarter of an hour is invisible to an
	// operator and turns a page that could be reloaded thirty times into eight
	// requests an hour.
	releaseFeedTTL = 15 * time.Minute

	// releaseFeedRetry is the same for an answer that failed, and it is shorter
	// on purpose: a daemon that was offline for a minute should not report the
	// feed as unreachable for a quarter of an hour afterwards. It is not shorter
	// still because a rate-limited API answers a failure as fast as it answers
	// anything, and retrying on every load is how a budget stays exhausted.
	releaseFeedRetry = time.Minute
)

// releaseView is one release as the section states it.
type releaseView struct {
	// Version is the tag, and it is empty exactly when this daemon could not
	// find out — an unreachable feed for the available release, never for the
	// installed one, which is a fact about this process.
	Version string

	// Notes is what that release said about itself, and empty means either that
	// it said nothing or that this daemon could not ask. The section
	// distinguishes those two by Known, because "this release published no
	// notes" and "the release feed could not be read" send an operator to
	// different places.
	Notes string

	// Known reports that the release feed answered for this release.
	Known bool
}

// updatesView is the whole of what the Updates section renders against.
type updatesView struct {
	// Installed is the release this process is running. Its version is always
	// known — it is buildinfo.Version, stamped at link time — and its notes are
	// known only when the feed could be read and the running binary is a
	// published release. A development build is neither, and the section says so
	// rather than showing an empty panel.
	Installed releaseView

	// Available is what the `latest` pointer resolves to.
	Available releaseView

	// Current reports that the two above are the same release, which is the
	// answer an operator opening this section most often wants and the one a
	// pair of version strings makes them work out for themselves.
	Current bool

	// Reachable reports whether the release feed answered at all. False is not
	// an error state of the page: it is a fact about this host's network stated
	// on a page that has rendered everything else.
	Reachable bool

	// PageToken is the render's own token, and the whole of what makes the
	// update control render (docs/components.md: a component handed nothing to
	// act with offers no action).
	PageToken string
}

// releaseFeed is the cached answer, and the only mutable state on the settings
// path.
//
// One per Server rather than one per request, which is the point: the cost this
// exists to bound is spent per daemon and not per page load.
type releaseFeed struct {
	mu     sync.Mutex
	at     time.Time
	view   updatesView
	answer bool
}

// updatesFor composes the Updates section for one render.
//
// The token is threaded in rather than minted here for the reason the fleet
// threads its own: it belongs to the render, and a second mint would be a second
// expiry nothing on this page is truer for.
func (s *Server) updatesFor(ctx context.Context, token string) updatesView {
	view := s.releases.lookup(ctx, s.updates.releases, buildinfo.Version, s.clock.Now())
	view.PageToken = token
	return view
}

// lookup answers from the cache when the cache is fresh, and asks the feed when
// it is not.
//
// The network call is deliberately outside the lock. Holding it across a request
// would make one slow lookup the wait time of every operator's page, which is the
// thing this file is written to prevent — and the cost of not holding it is that
// two simultaneous first loads may each ask, which is two requests once rather
// than one request behind a queue.
func (f *releaseFeed) lookup(ctx context.Context, src releaseSource, installed string, now time.Time) updatesView {
	if view, fresh := f.fresh(now); fresh {
		return view
	}

	view := readReleaseFeed(ctx, src, installed)

	f.mu.Lock()
	defer f.mu.Unlock()
	f.view, f.at, f.answer = view, now, true
	return view
}

// fresh is the cached answer, if it is still one.
//
// A failure is cached too, and for less long. What must never happen is a page
// that asks a feed which is not answering on every single load — that is how a
// settings page becomes slow for as long as a network is down.
func (f *releaseFeed) fresh(now time.Time) (updatesView, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if !f.answer {
		return updatesView{}, false
	}
	ttl := releaseFeedTTL
	if !f.view.Reachable {
		ttl = releaseFeedRetry
	}
	if now.Sub(f.at) >= ttl {
		return updatesView{}, false
	}
	return f.view, true
}

// readReleaseFeed asks what this project has published, under a deadline, and
// answers with whatever it learned.
//
// It returns a view rather than an error, and that is the shape of the
// requirement: every failure below is a section that says the feed could not be
// read, so there is no error for a caller to handle differently and no way for
// one to reach the operator as a broken page. What the failure *was* stays out of
// the page entirely — a hostname, a proxy's refusal or a rate-limit message is
// this daemon's own diagnostic and belongs where an operator reads those.
//
// A nil source is the daemon with no update path wired behind it, which is every
// server a test builds (see selfUpdate). It reads as a feed that could not be
// reached, which is exactly what it is: this process has nothing to ask.
func readReleaseFeed(ctx context.Context, src releaseSource, installed string) updatesView {
	view := updatesView{Installed: releaseView{Version: installed}}
	if src == nil {
		return view
	}

	ctx, cancel := context.WithTimeout(ctx, releaseLookupTimeout)
	defer cancel()

	// `latest` first, because it is the question the section exists to answer.
	// If this one cannot be asked there is nothing to say about an update at
	// all, and asking the second would spend a deadline on a fact about a
	// release the operator already has.
	latest, err := src.Release(ctx, "")
	if err != nil {
		return view
	}
	view.Reachable = true
	view.Available = releaseView{Version: latest.Version, Notes: latest.Notes, Known: true}

	if latest.Version == installed {
		// One release, described once. Asking again for the tag `latest` just
		// resolved to would spend a second request to be told the same thing.
		view.Current = true
		view.Installed = view.Available
		return view
	}

	// The running binary's own release. It is asked for by the version this
	// process was stamped with, so a development build asks for "dev" — which
	// internal/updater refuses as a malformed version without touching the
	// network, and which lands here as a release with no notes rather than as a
	// failure. That is the honest reading: a build nobody published has no
	// published notes.
	running, err := src.Release(ctx, installed)
	if err != nil {
		return view
	}
	view.Installed = releaseView{Version: installed, Notes: running.Notes, Known: true}
	return view
}
