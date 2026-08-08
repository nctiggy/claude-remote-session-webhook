// Internal test, matching settings_test.go.
//
// **Every claim in the first half of this file is about markup**, which is the
// whole of issue #103. Milestone 6 built the update path, tested the route, the
// record, the checksum, the signature, the smoke test and the atomic swap — and
// shipped with no control anywhere in web/templates, because not one of those
// assertions read a page. Milestone 4 shipped the same defect (FR-026) one
// milestone earlier. So what is asserted below is what an operator can reach.
package httpapi

import (
	"context"
	"fmt"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nctiggy/claude-remote-session-webhook/internal/buildinfo"
	"github.com/nctiggy/claude-remote-session-webhook/internal/config"
	"github.com/nctiggy/claude-remote-session-webhook/internal/updater"
	"github.com/nctiggy/claude-remote-session-webhook/web"
)

// The update control as an operator meets it, written out rather than read from
// the constants the code uses: what must be true is that a browser posts these
// bytes to that path, and a test asking the code what it spells proves only that
// the code agrees with itself.
const (
	wantUpdateFormAction = "/dashboard/update"
	wantUpdateFormMethod = `method="post"`
)

// updateForm is the form on a rendered settings page, or a failure saying there
// is none — which is the failure this file exists to produce.
var updateForm = regexp.MustCompile(`(?s)<form[^>]*action="` + regexp.QuoteMeta(wantUpdateFormAction) + `"[^>]*>(.*?)</form>`)

// hiddenField reads one hidden input's value out of some markup.
var hiddenField = regexp.MustCompile(`<input type="hidden" name="([^"]+)" value="([^"]*)">`)

// formOn isolates the update form, and fails with the whole page when there is
// none: "the settings page renders no update control" is precisely the sentence
// this project needed a year of milestones ago.
func formOn(t *testing.T, page string) string {
	t.Helper()

	form := updateForm.FindStringSubmatch(page)
	if form == nil {
		t.Fatalf("the settings page renders no form posting to %s, so the update route this daemon carries is reachable from nothing an operator can click:\n%s", wantUpdateFormAction, page)
	}
	return form[0]
}

// TestSettingsRendersTheUpdateControl is issue #103's first half, at the layer
// that could have caught it.
//
// The route, the record, the checksum, the signature, the smoke test and the
// swap were all tested and all correct. What was missing was a `<form>`, and
// nothing in the suite looked for one.
//
// **Must fail when** the Updates section is removed from settings.html, which is
// the state this daemon shipped in.
func TestSettingsRendersTheUpdateControl(t *testing.T) {
	t.Parallel()

	form := formOn(t, settingsBody(t, newFleet(t)))

	if !strings.Contains(strings.ToLower(form), wantUpdateFormMethod) {
		t.Errorf("the update control is not a post, so it would reach a route this daemon registers for POST alone:\n%s", form)
	}

	fields := make(map[string]string)
	for _, field := range hiddenField.FindAllStringSubmatch(form, -1) {
		fields[field[1]] = field[2]
	}

	// The confirming step FR-029a requires, carried by the page deliberately.
	// Without it the route refuses and answers a banner, which is a control that
	// is present and cannot work — the same defect as one that is absent, minus
	// the excuse.
	if got := fields["confirm"]; got != "yes" {
		t.Errorf("the update form submits confirm=%q; the route refuses anything but yes, so this control could never install anything:\n%s", got, form)
	}
	// The gate's evidence. A form without it is refused uniformly, with nothing
	// on the page to say why.
	if fields[fieldPageToken] == "" {
		t.Errorf("the update form carries no %s, so the gate refuses every submission of it:\n%s", fieldPageToken, form)
	}
	if !strings.Contains(form, `type="submit"`) {
		t.Errorf("the update form has no submit control, so there is nothing on this page to press:\n%s", form)
	}
}

// TestTheUpdateControlIsAcceptedByTheRouteItPosts is the other half of the same
// lesson, and the one a regexp cannot give: markup that looks right and is
// refused by the daemon is still a dead control.
//
// So this submits exactly what the rendered page carries — the same fields, the
// same values, the same path — and asserts the route got past every check that
// stands in front of it. The fixture wires no update path (see selfUpdate), so
// the furthest a request can travel is the refusal for a daemon with nothing
// behind the route; what matters is that it is *that* refusal and not the gate's,
// not the missing-token one, and not the unconfirmed one.
//
// **Must fail when** the form omits the token, sends the confirming step under
// another name, or posts to a path this daemon does not register.
func TestTheUpdateControlIsAcceptedByTheRouteItPosts(t *testing.T) {
	t.Parallel()

	f := newFleet(t)
	form := formOn(t, settingsBody(t, f))

	submitted := url.Values{}
	for _, field := range hiddenField.FindAllStringSubmatch(form, -1) {
		submitted.Set(field[1], field[2])
	}

	r := httptest.NewRequest(http.MethodPost, wantUpdateFormAction, strings.NewReader(submitted.Encode()))
	r.Header.Set(headerContentType, contentTypeForm)
	r.Header.Set(headerAccessAssertion, f.keys.mint(t, f.keys.claims()))
	r.Header.Set(headerSecFetchSite, secFetchSiteSameOrigin)

	w := httptest.NewRecorder()
	f.ServeHTTP(w, r)

	if w.Code != http.StatusSeeOther {
		t.Fatalf("submitting the page's own update control was answered %d (%s); want %d — anything else is a control the daemon refuses",
			w.Code, w.Body.String(), http.StatusSeeOther)
	}
	to, err := url.Parse(w.Header().Get(headerLocation))
	if err != nil {
		t.Fatalf("parse the redirect the update answered with: %v", err)
	}
	// The daemon behind this fixture has no update path wired, so this is as far
	// as any request can get. The two outcomes that must not appear are the ones
	// that would mean the *form* was wrong.
	switch got := to.Query().Get(queryOutcome); got {
	case string(outcomeUpdateUnconfirmed):
		t.Errorf("the page's own control was refused for not being confirmed, so the confirming step it renders is not the one the route reads")
	case "":
		t.Errorf("the update answered no outcome at all, so the page an operator lands on says nothing about what happened")
	default:
		if got == string(outcomeBadVersion) {
			t.Errorf("the page's own control was refused for its version field, which it renders empty and the route reads as the latest release")
		}
	}
}

// controllessRoutes is every browser route that changes something and is
// deliberately reachable from no rendered page, with the reason.
//
// It has one entry, and the entry is not this issue's: **the mode toggle
// (`POST /dashboard/sessions/{id}/mode`) has no control either.** It was found by
// the sweep below while fixing #103 and is left alone on purpose — what a control
// for it should say, and where it belongs on a card, is a design question this
// issue did not ask and Principle II says not to guess at. It is named here so
// the gap is written down where it cannot be missed rather than discovered a
// third time.
//
// The map is closed in both directions by the test: a route that gains a control
// must leave this list, and a route added with no control must be argued for in a
// reviewed PR rather than shipped silently.
var controllessRoutes = map[string]string{
	"/dashboard/sessions/{id}/mode": "issue #103's second finding: the toggle #58 built renders no control either, and what one should look like is not this issue's to invent",
}

// mutatingPatterns is every POST pattern this package registers, read from the
// constants themselves — which is the right direction here, unlike everywhere
// else in this suite: the claim is about *all* of them, so a list written out by
// hand would be a list the next route is forgotten from, which is the defect.
var mutatingPatterns = []string{
	patternDashboardCreate,
	patternDashboardDestroy,
	patternDashboardRename,
	patternDashboardCompact,
	patternDashboardMode,
	patternDashboardUpdate,
}

// formActions is every path a template posts to, with the route parameters a
// template interpolates left as a wildcard.
func formActions(t *testing.T) []string {
	t.Helper()

	action := regexp.MustCompile(`<form[^>]*\baction="([^"]*)"`)

	var actions []string
	err := fs.WalkDir(web.Templates, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || path.Ext(p) != templateExt {
			return err
		}
		source, err := fs.ReadFile(web.Templates, p)
		if err != nil {
			return err
		}
		for _, match := range action.FindAllStringSubmatch(string(source), -1) {
			actions = append(actions, match[1])
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk the embedded template tree: %v", err)
	}
	if len(actions) == 0 {
		t.Fatal("no template posts to anything at all, so this sweep asserts nothing")
	}
	return actions
}

// interpolated is a form's action as a pattern, with `{{ … }}` — the session
// identifier a card substitutes — standing for a route's `{id}`.
var interpolated = regexp.MustCompile(`{{[^}]*}}`)

// TestEveryBrowserActionHasAControl is the lesson issue #103 says was written
// down and not applied: a route that works, tests that pass, and no way for an
// operator to reach it.
//
// It is a sweep rather than a test per route, because the failure is always the
// *next* route — the one somebody adds with a handler, a contract, an audit
// action and a suite, and no markup. Milestone 4 shipped it as FR-026 and
// milestone 6 shipped it again; a per-route assertion would have caught neither,
// since nobody writes the assertion they forgot to need.
//
// It reads the template tree as source rather than a rendered page, which bounds
// what it can claim: a form that exists in a template and is never rendered
// satisfies it. That is deliberate — a sweep over every route cannot know which
// view each control belongs on — and it is why the control this issue adds also
// has TestSettingsRendersTheUpdateControl above, which reads what a browser
// actually receives. The sweep catches the route nobody wrote markup for; the
// per-route test catches the markup nobody renders.
//
// **Must fail when** a POST route is registered on the browser door and nothing
// in web/templates posts to it.
func TestEveryBrowserActionHasAControl(t *testing.T) {
	t.Parallel()

	actions := formActions(t)

	for _, pattern := range mutatingPatterns {
		route := strings.TrimPrefix(pattern, http.MethodPost+" ")
		// The route's own parameter, spelled as the router spells it, so the
		// exception list below reads as the routes table does.
		named := regexp.MustCompile(`{[^}]*}`).ReplaceAllString(route, "{id}")

		rendered := false
		for _, action := range actions {
			if interpolated.ReplaceAllString(action, "{id}") == named {
				rendered = true
				break
			}
		}

		why, excused := controllessRoutes[named]
		switch {
		case rendered && excused:
			t.Errorf("%s is listed as having no control and a template posts to it; the list is stale, and a stale exception is how the next one hides", named)
		case !rendered && !excused:
			t.Errorf("%s changes something on this host and no template posts to it, so it is a route an operator cannot reach — the defect issue #103 is about, one route later", named)
		case !rendered && excused:
			t.Logf("%s has no control: %s", named, why)
		}
	}
}

// fakeReleaseFeed is a release feed that answers from a map, which is the whole
// of what the settings page asks of one.
//
// It implements releaseSource rather than a narrower interface so that the page
// is driven through exactly the seam the daemon wires — Asset is never called on
// this path, and a fake that could not answer it would be a fake the update route
// could not also be pointed at.
type fakeReleaseFeed struct {
	mu sync.Mutex

	// latest is the tag `latest` resolves to, and notes is what each tag said
	// about itself.
	latest string
	notes  map[string]string

	// err is what every ask is refused with — an unreachable host, in one field.
	err error

	// asked is every version this feed was asked for, in order, so a case can say
	// that a second page load cost no second request.
	asked []string

	// deadline records whether the context each ask arrived under carried one,
	// and how far away it was. A lookup with no deadline is the settings page
	// waiting on the fetcher's own five-minute timeout.
	deadline []time.Duration
}

func (f *fakeReleaseFeed) Release(ctx context.Context, version string) (*updater.Release, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.asked = append(f.asked, version)
	if at, ok := ctx.Deadline(); ok {
		f.deadline = append(f.deadline, time.Until(at))
	} else {
		f.deadline = append(f.deadline, 0)
	}
	if f.err != nil {
		return nil, f.err
	}

	tag := version
	if tag == "" {
		tag = f.latest
	}
	notes, published := f.notes[tag]
	if !published {
		return nil, fmt.Errorf("release %q: %w", tag, updater.ErrAssetNotFound)
	}
	return &updater.Release{Version: tag, Notes: notes}, nil
}

func (f *fakeReleaseFeed) Asset(context.Context, *updater.Release, string) ([]byte, error) {
	return nil, updater.ErrAssetNotFound
}

func (f *fakeReleaseFeed) versionsAsked() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.asked...)
}

// TestSettingsShowsWhatEachReleaseSaidAboutItself is the section's reason for
// existing (issue #103): an operator about to replace the binary that manages
// every session on this host can read what they would be taking before they take
// it, and a version number does not say.
func TestSettingsShowsWhatEachReleaseSaidAboutItself(t *testing.T) {
	t.Parallel()

	const (
		installedNotes = "This release fixed the reaper."
		availableNotes = "This release moves the update control onto settings."
	)

	f := newFleet(t)
	f.updates.releases = &fakeReleaseFeed{
		latest: "v0.99",
		// The running binary's own tag, which is what the page asks the feed for.
		// In a suite this is "dev" and the feed refuses it; a case that needs the
		// installed notes has to publish a release under exactly that name.
		notes: map[string]string{"v0.99": availableNotes, buildinfo.Version: installedNotes},
	}

	page := settingsBody(t, f)

	if !strings.Contains(page, "v0.99") {
		t.Errorf("the settings page never names the release that is available, so an operator is asked to update to nothing in particular:\n%s", page)
	}
	if !strings.Contains(page, availableNotes) {
		t.Errorf("the settings page does not carry the notes of the release it offers, so an operator cannot read what they would be taking:\n%s", page)
	}
	if !strings.Contains(page, installedNotes) {
		t.Errorf("the settings page does not carry the notes of the release it is running:\n%s", page)
	}
}

// TestReleaseNotesAreTextAndNeverMarkup is the constitution's seventh principle
// at the one place this page renders somebody else's bytes.
//
// Release notes arrive from the GitHub API, over the network, before any
// signature has been checked, written in Markdown by whoever authored the
// release. They are exactly the kind of text docs/security.md rules on: rendered
// as text, never as HTML, closed by construction rather than by sanitising.
func TestReleaseNotesAreTextAndNeverMarkup(t *testing.T) {
	t.Parallel()

	const hostile = `<img src=x onerror="fetch('/dashboard/update',{method:'POST'})">`

	f := newFleet(t)
	f.updates.releases = &fakeReleaseFeed{latest: "v0.99", notes: map[string]string{"v0.99": hostile}}

	page := settingsBody(t, f)

	if strings.Contains(page, hostile) {
		t.Fatalf("the release notes reached the page as markup; a release description could then act on this operator's own daemon:\n%s", page)
	}
	if !strings.Contains(page, "&lt;img src=x") {
		t.Errorf("the release notes are neither escaped nor present, so this test asserts nothing about what a browser would do with them:\n%s", page)
	}
}

// TestSettingsSurvivesAnUnreachableReleaseFeed is the design question issue #103
// raises, answered.
//
// The page's first job is reporting local configuration, and that needs no
// network at all. A host with no route to GitHub — the ordinary state of a
// daemon behind a tunnel on a machine that has just booted, and the permanent
// state of one on an isolated network — must still get every configured value,
// the control, and a sentence saying plainly what could not be read.
//
// **Must fail when** the release lookup's failure is allowed to reach the page as
// a 500, an empty section, or a missing control.
func TestSettingsSurvivesAnUnreachableReleaseFeed(t *testing.T) {
	t.Parallel()

	f := newFleet(t)
	f.updates.releases = &fakeReleaseFeed{err: fmt.Errorf("dial api.github.com: no route to host")}

	page := settingsBody(t, f)

	for _, name := range config.Vars() {
		key := config.KeyForVar(name)
		if !strings.Contains(page, ">"+key+"<") {
			t.Errorf("a feed this daemon could not reach cost the settings page its row for %s, which is local configuration and needs no network at all", key)
		}
	}
	if !strings.Contains(page, "could not reach the release feed") {
		t.Errorf("the settings page does not say it could not read the release feed, so an operator reads an empty section and cannot tell it from a project that has published nothing:\n%s", page)
	}
	// The control still works: the route resolves `latest` for itself, and what
	// failed here was this page's attempt to describe it.
	formOn(t, page)
}

// TestTheReleaseLookupIsBounded is the other half of "must not make /settings
// slow".
//
// The fetcher's own timeout is five minutes, which is right for downloading a
// release and absurd for composing a page: a GitHub that accepts the connection
// and then says nothing would hold an operator's settings page open for as long
// as it liked. So the lookup carries a deadline of its own, and this asserts it
// arrives at the feed rather than asserting it exists in a constant.
func TestTheReleaseLookupIsBounded(t *testing.T) {
	t.Parallel()

	feed := &fakeReleaseFeed{latest: "v0.99", notes: map[string]string{"v0.99": "notes"}}

	readReleaseFeed(context.Background(), feed, "v0.42")

	feed.mu.Lock()
	defer feed.mu.Unlock()
	if len(feed.deadline) == 0 {
		t.Fatal("the release feed was never asked anything, so nothing here is asserted")
	}
	for i, left := range feed.deadline {
		if left <= 0 {
			t.Errorf("ask %d reached the feed with no deadline, so composing the settings page inherits the fetcher's five-minute one", i)
			continue
		}
		if left > releaseLookupTimeout {
			t.Errorf("ask %d reached the feed with %s left, which is longer than the %s a page may spend on the network", i, left, releaseLookupTimeout)
		}
	}
}

// TestTheReleaseFeedIsAskedOnceInAWhile is the rate limit, which is not a
// nicety: the unauthenticated GitHub API allows sixty requests an hour from one
// address and a lookup spends two. Without the cache, thirty reloads of the
// settings page would exhaust an operator's budget and the thirty-first would
// report the feed as unreachable — truthfully, and because of this page.
//
// The failing answer is cached too, and for less long. A daemon that was offline
// for a minute must not report the feed as unreachable for a quarter of an hour
// afterwards, and one whose network is down must not ask on every load.
func TestTheReleaseFeedIsAskedOnceInAWhile(t *testing.T) {
	t.Parallel()

	t.Run("an answer is reused", func(t *testing.T) {
		t.Parallel()

		feed := &fakeReleaseFeed{latest: "v0.42", notes: map[string]string{"v0.42": "notes"}}
		var cache releaseFeed

		cache.lookup(context.Background(), feed, "v0.42", testTime)
		asked := len(feed.versionsAsked())
		if asked == 0 {
			t.Fatal("the first lookup asked nothing, so this case asserts nothing")
		}

		cache.lookup(context.Background(), feed, "v0.42", testTime.Add(releaseFeedTTL-time.Second))
		if got := len(feed.versionsAsked()); got != asked {
			t.Errorf("a second page load inside the cache window cost %d asks; a reloaded page must not spend an operator's API budget", got-asked)
		}

		cache.lookup(context.Background(), feed, "v0.42", testTime.Add(releaseFeedTTL))
		if got := len(feed.versionsAsked()); got == asked {
			t.Errorf("the answer was still being reused after %s, so a release published since would never appear", releaseFeedTTL)
		}
	})

	t.Run("a failure is retried sooner than an answer", func(t *testing.T) {
		t.Parallel()

		feed := &fakeReleaseFeed{err: fmt.Errorf("dial api.github.com: no route to host")}
		var cache releaseFeed

		cache.lookup(context.Background(), feed, "v0.42", testTime)
		asked := len(feed.versionsAsked())

		cache.lookup(context.Background(), feed, "v0.42", testTime.Add(releaseFeedRetry/2))
		if got := len(feed.versionsAsked()); got != asked {
			t.Errorf("a page load while the feed was down cost %d more asks; a network that is down stays down for longer than one page load", got-asked)
		}

		cache.lookup(context.Background(), feed, "v0.42", testTime.Add(releaseFeedRetry))
		if got := len(feed.versionsAsked()); got == asked {
			t.Errorf("a feed that failed was not retried after %s, so a daemon that was briefly offline reports the feed as unreachable long after it came back", releaseFeedRetry)
		}
	})
}

// TestAnInstalledReleaseIsDescribedOnce is the second half of the budget above,
// and it is about the ordinary case rather than the cached one: a daemon already
// running the latest release must not spend two requests to be told the same
// thing twice.
func TestAnInstalledReleaseIsDescribedOnce(t *testing.T) {
	t.Parallel()

	feed := &fakeReleaseFeed{latest: "v0.42", notes: map[string]string{"v0.42": "notes"}}

	view := readReleaseFeed(context.Background(), feed, "v0.42")

	if !view.Current {
		t.Errorf("a daemon running the release `latest` resolves to is not told it is current, which is the one thing this section is opened to find out")
	}
	if got := feed.versionsAsked(); len(got) != 1 {
		t.Errorf("the feed was asked %v; the running release is the one `latest` already answered with", got)
	}
	if view.Installed.Notes != "notes" {
		t.Errorf("the installed release carries %q for notes; it is the release `latest` named and its description is already in hand", view.Installed.Notes)
	}
}

// TestADevelopmentBuildHasNoPublishedNotes is the honest reading of a build
// nobody released.
//
// buildinfo.Version is "dev" in anything the release workflow did not build, and
// asking the feed for it is a request that cannot succeed — internal/updater
// refuses the shape before it touches the network. What the page must not do is
// present that as a feed it could not reach: the feed answered perfectly, and
// this build is simply not one of the things it describes.
func TestADevelopmentBuildHasNoPublishedNotes(t *testing.T) {
	t.Parallel()

	feed := &fakeReleaseFeed{latest: "v0.99", notes: map[string]string{"v0.99": "notes"}}

	view := readReleaseFeed(context.Background(), feed, "dev")

	if !view.Reachable {
		t.Error("a development build reads as a release feed that could not be reached, which sends an operator to look at their network for a fact about their binary")
	}
	if view.Installed.Known {
		t.Error("a development build claims published notes, which no release carries for it")
	}
	if view.Available.Version != "v0.99" {
		t.Errorf("the available release reads %q; a development build is still told what it could move to", view.Available.Version)
	}
}
