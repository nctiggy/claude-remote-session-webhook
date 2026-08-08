// Internal test, matching actions_test.go: the four steps behind this route are
// an unexported field, the outcome vocabulary is unexported, and the two things
// worth asserting hardest — that each step was reached with what the step before
// it produced, and that nothing was reached at all when the request was refused —
// are only visible from inside.
//
// Every literal these cases compare against is written out here rather than read
// from the constant the code writes, which is the discipline every file in this
// package follows: a test that asked the code what it does proves only that the
// code agrees with itself.
package httpapi

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/nctiggy/claude-remote-session-webhook/internal/audit"
	"github.com/nctiggy/claude-remote-session-webhook/internal/updater"
)

// The update's own answers, quoted rather than read from outcome.go.
const (
	wantUpdatedOutcome                        = outcome("updated")
	wantUpdateUnconfirmedOutcome              = outcome("update-unconfirmed")
	wantBadVersionOutcome                     = outcome("bad-version")
	wantUpdateNotFetchedOutcome               = outcome("update-not-fetched")
	wantUpdateUnverifiedOutcome               = outcome("update-unverified")
	wantUpdateRefusedOutcome                  = outcome("update-refused")
	wantUpdatePath                            = "/dashboard/update"
	wantUpdateAction             audit.Action = "dashboard.update"
)

// The release the fake publishes. The version is deliberately not the one any
// case submits, so an assertion about an asset name is an assertion about the
// tag the *release* answered with — a route that pasted the submitted string
// into the asset name would pass every case that asks for a named version and
// fail every one that asks for the latest.
const (
	fixtureVersion  = "v0.42"
	fixtureBinary   = "the bytes of a release tarball"
	fixtureSums     = "the published checksum list"
	fixtureSignedBy = "the signature over that list"
)

// fixtureAsset is the tarball's name for this machine. It is composed here the
// way updater.AssetName composes it and then compared against what the route
// asked for, which is FR-027 from the caller's side: the asset is named exactly,
// and a release that publishes something near it is refused rather than
// installed.
func fixtureAsset() string { return "crswd_" + fixtureVersion + "_linux_" + runtime.GOARCH + ".tar.gz" }

// stageCall and swapCall are what the two later steps were handed. They are
// recorded rather than asserted inside the fake so that a case failing prints
// what really arrived, and so that "the step was reached" and "the step was
// reached with the right thing" are two separate failures.
type stageCall struct {
	version   string
	name      string
	asset     []byte
	sums      []byte
	signature []byte
}

type swapCall struct {
	staged  string
	version string
}

// fakeUpdatePath stands in for all three collaborators behind the route: the
// fetcher, the stager and the swapper.
//
// One type rather than three, because what almost every case here asserts is the
// *sequence* — which steps ran, in what order, with what the previous one
// returned — and three fakes recording into three places would make that
// sequence something a test has to reassemble.
type fakeUpdatePath struct {
	// dir is where a staged candidate is claimed to be. A real file, because the
	// production swapper is handed a path and this fake's caller must not be able
	// to tell the difference by looking.
	dir string

	// published is the release description: what the API answers, and the assets
	// it names. A name absent from the map is answered the way the real fetcher
	// answers one — refused, never approximated.
	published map[string][]byte

	// The refusals a case injects, one per step.
	releaseErr error
	assetErr   error
	stageErr   error
	swapErr    error

	// What was asked of it.
	askedVersions []string
	fetched       []string
	staged        []stageCall
	swapped       []swapCall
	exits         int

	// atExit runs inside ExitForRestart, which is the only moment from which the
	// ordering the contract requires can be observed: the redirect and the audit
	// record must already exist when the process is allowed to end.
	atExit func()
}

func newFakeUpdatePath(t *testing.T) *fakeUpdatePath {
	t.Helper()

	return &fakeUpdatePath{
		dir: t.TempDir(),
		published: map[string][]byte{
			fixtureAsset():         []byte(fixtureBinary),
			updater.ChecksumsAsset: []byte(fixtureSums),
			updater.SignatureAsset: []byte(fixtureSignedBy),
		},
	}
}

func (f *fakeUpdatePath) Release(_ context.Context, version string) (*updater.Release, error) {
	f.askedVersions = append(f.askedVersions, version)
	if f.releaseErr != nil {
		return nil, f.releaseErr
	}
	return &updater.Release{Version: fixtureVersion}, nil
}

func (f *fakeUpdatePath) Asset(_ context.Context, _ *updater.Release, name string) ([]byte, error) {
	f.fetched = append(f.fetched, name)
	if f.assetErr != nil {
		return nil, f.assetErr
	}
	body, published := f.published[name]
	if !published {
		return nil, fmt.Errorf("%q: %w", name, updater.ErrAssetNotFound)
	}
	return body, nil
}

func (f *fakeUpdatePath) Stage(version, name string, asset, sums, signature []byte) (string, error) {
	f.staged = append(f.staged, stageCall{version: version, name: name, asset: asset, sums: sums, signature: signature})
	if f.stageErr != nil {
		return "", f.stageErr
	}
	path := filepath.Join(f.dir, "crswd."+version)
	if err := os.WriteFile(path, asset, 0o700); err != nil { //nolint:gosec // G302: this fake stands in for the step that makes a verified candidate executable, and asserting the swapper was handed one means writing one.
		return "", err
	}
	return path, nil
}

func (f *fakeUpdatePath) Swap(_ context.Context, staged, version string) error {
	f.swapped = append(f.swapped, swapCall{staged: staged, version: version})
	return f.swapErr
}

func (f *fakeUpdatePath) ExitForRestart() {
	f.exits++
	if f.atExit != nil {
		f.atExit()
	}
}

// reached is the whole sequence as one sentence, for a failure message. A case
// that expected an update to be refused before it began is more useful when it
// prints how far it actually got.
func (f *fakeUpdatePath) reached() string {
	return fmt.Sprintf("asked for %v, fetched %v, staged %d, swapped %d, exited %d",
		f.askedVersions, f.fetched, len(f.staged), len(f.swapped), f.exits)
}

// untouched reports whether nothing on the update path ran at all — which is what
// every refusal ahead of step 1 has to be able to claim.
func (f *fakeUpdatePath) untouched() bool {
	return len(f.askedVersions) == 0 && len(f.fetched) == 0 && len(f.staged) == 0 && len(f.swapped) == 0 && f.exits == 0
}

// updateDoor is the registered update route with the four steps behind it
// replaced and everything else real: the same gate, the same middleware, the
// same trail.
//
// It drives Server.ServeHTTP rather than the handler, and that is load-bearing
// rather than tidy: a route registered with handleBrowser instead of
// handleAction would leave the cross-site defence absent from the one request
// that can make this host download and execute a binary, and no test of the
// handler alone would notice.
type updateDoor struct {
	*testServer
	keys  *keyServer
	steps *fakeUpdatePath

	// answer is the recorder the request in flight is writing into, so that
	// atExit can look at the response from inside the exit.
	answer *httptest.ResponseRecorder
}

func newUpdateDoor(t *testing.T) *updateDoor {
	t.Helper()

	keys := newKeyServer(t)
	steps := newFakeUpdatePath(t)
	d := &updateDoor{testServer: newAuditedServerWith(t, keys.validator(t)), keys: keys, steps: steps}
	d.updates = selfUpdate{releases: steps, staging: steps, installer: steps}
	return d
}

// confirmed is the form the update button submits: the render's page token, and
// the confirming step FR-029a requires.
func (d *updateDoor) confirmed(t *testing.T) url.Values {
	t.Helper()

	form := url.Values{}
	form.Set(fieldPageToken, mustMint(t, d.pageKey, testOperatorEmail, testTime))
	form.Set(fieldConfirm, confirmYes)
	return form
}

func (d *updateDoor) post(t *testing.T, form url.Values) *httptest.ResponseRecorder {
	t.Helper()

	return d.send(t, http.MethodPost, wantUpdatePath, secFetchSiteSameOrigin, form)
}

// send is post with the method, the path and the browser's own account of where
// the request came from chosen by the caller — the three things the cross-site
// and wrong-method cases have to vary and a rendered form never does.
func (d *updateDoor) send(t *testing.T, method, path, site string, form url.Values) *httptest.ResponseRecorder {
	t.Helper()

	r := httptest.NewRequest(method, path, strings.NewReader(form.Encode()))
	r.Header.Set(headerContentType, contentTypeForm)
	r.Header.Set(headerAccessAssertion, d.keys.mint(t, d.keys.claims()))
	if site != absent {
		r.Header.Set(headerSecFetchSite, site)
	}

	w := httptest.NewRecorder()
	d.answer = w
	d.ServeHTTP(w, r)
	return w
}

// wantUpdateRecord asserts the one record a request produced and returns it.
func (d *updateDoor) record(t *testing.T, decision audit.Decision) map[string]any {
	t.Helper()

	rec := d.only(t)
	if got, want := rec["action"], string(wantUpdateAction); got != want {
		t.Errorf("action = %v; want %v — an update is not a session action and not a page view", got, want)
	}
	if got, want := rec["decision"], string(decision); got != want {
		t.Errorf("decision = %v; want %v", got, want)
	}
	return rec
}

// reasons is every reason this request had reported to it or recorded, which is
// where the two rules about what may leave this route are checked: a sentinel on
// the trail, and everything else on stderr.
func (d *updateDoor) reported() string {
	var b strings.Builder
	for _, err := range d.failed {
		b.WriteString(err.Error())
		b.WriteString("\n")
	}
	return b.String()
}

// TestUpdateInstallsTheReleaseAndExitsForRestart is the whole of T019's claim:
// the route reaches fetch, stage and swap, each with what the step before it
// produced, and only then answers and ends the process.
//
// **Must fail when** any step is skipped, or when a step is handed something it
// did not receive from the one before it — an asset name built from the
// submitted version rather than the tag the release answered with, a checksum
// list fetched by pattern, a staged path the swapper never sees. The three
// counts are asserted as well as the arguments, because "reached once" is the
// claim: a chain that fetched twice would be downloading a release to check it
// and downloading it again to install it.
//
// The ordering inside the exit is the second half. contracts/self-update.md
// numbers exit as step 7, after the 303 and after the record — a swap that
// exited on its way out would take both with it, and the operator would be left
// with a request that never answered and an update with nothing in the journal
// to say it happened.
func TestUpdateInstallsTheReleaseAndExitsForRestart(t *testing.T) {
	t.Parallel()

	d := newUpdateDoor(t)

	var (
		statusAtExit  int
		flushedAtExit bool
		recordsAtExit int
	)
	d.steps.atExit = func() {
		statusAtExit = d.answer.Code
		flushedAtExit = d.answer.Flushed
		recordsAtExit = len(d.records(t))
	}

	w := d.post(t, d.confirmed(t))

	wantOutcome(t, w, wantUpdatedOutcome)

	// Step 1: the latest release, and exactly the three assets it takes to
	// install one.
	if got, want := d.steps.askedVersions, []string{""}; len(got) != 1 || got[0] != want[0] {
		t.Errorf("asked for releases %v; want exactly one, for the latest — %s", got, d.steps.reached())
	}
	wantFetched := []string{fixtureAsset(), "SHA256SUMS", "SHA256SUMS.sig"}
	if len(d.steps.fetched) != len(wantFetched) {
		t.Fatalf("fetched %v; want exactly %v", d.steps.fetched, wantFetched)
	}
	for i, want := range wantFetched {
		if got := d.steps.fetched[i]; got != want {
			t.Errorf("asset %d = %q; want %q — the asset is named exactly (FR-027)", i, got, want)
		}
	}

	// Step 2 to 4: the bytes that were fetched, under the name they were fetched
	// by, for the version the release said it is.
	if len(d.steps.staged) != 1 {
		t.Fatalf("staged %d times; want exactly 1 — %s", len(d.steps.staged), d.steps.reached())
	}
	stage := d.steps.staged[0]
	switch {
	case stage.version != fixtureVersion:
		t.Errorf("staged version = %q; want %q — the tag the release answered with", stage.version, fixtureVersion)
	case stage.name != fixtureAsset():
		t.Errorf("staged name = %q; want %q — the checksum list is read by exact asset name", stage.name, fixtureAsset())
	case string(stage.asset) != fixtureBinary:
		t.Errorf("staged asset = %q; want the bytes that were fetched", stage.asset)
	case string(stage.sums) != fixtureSums:
		t.Errorf("staged sums = %q; want the published checksum list", stage.sums)
	case string(stage.signature) != fixtureSignedBy:
		t.Errorf("staged signature = %q; want the signature over that list", stage.signature)
	}

	// Steps 5 and 6: the candidate staging returned, and nothing this route
	// composed itself.
	if len(d.steps.swapped) != 1 {
		t.Fatalf("swapped %d times; want exactly 1 — %s", len(d.steps.swapped), d.steps.reached())
	}
	if got, want := d.steps.swapped[0].staged, filepath.Join(d.steps.dir, "crswd."+fixtureVersion); got != want {
		t.Errorf("swapped %q; want %q — the path staging returned", got, want)
	}
	if got, want := d.steps.swapped[0].version, fixtureVersion; got != want {
		t.Errorf("swapped version = %q; want %q — what the smoke test requires the candidate to print", got, want)
	}

	// Step 7, and what had to be true before it.
	if d.steps.exits != 1 {
		t.Fatalf("exited %d times; want exactly 1 — an update that does not exit leaves systemd running the binary it replaced", d.steps.exits)
	}
	if statusAtExit != http.StatusSeeOther {
		t.Errorf("the response was %d when the process was allowed to end; want %d written first", statusAtExit, http.StatusSeeOther)
	}
	if !flushedAtExit {
		t.Errorf("the redirect had not been flushed when the process was allowed to end; the operator's browser would be waiting on a connection that is about to close")
	}
	if recordsAtExit != 1 {
		t.Errorf("the trail held %d records when the process was allowed to end; want 1 — an update with no record is the one change to this host nobody can audit", recordsAtExit)
	}

	d.record(t, audit.Allow)
}

// TestUpdateNamesTheVersionForARollback is FR-022 from this door: the field
// reaches the fetch, which is the whole of what makes a rollback an ordinary
// update.
//
// **Must fail when** the field is ignored, which would silently install the
// latest release for an operator asking to go back to the one before it — the
// exact opposite of what they asked for, answered as a success.
func TestUpdateNamesTheVersionForARollback(t *testing.T) {
	t.Parallel()

	d := newUpdateDoor(t)
	form := d.confirmed(t)
	form.Set(fieldVersion, "v0.41")

	w := d.post(t, form)

	wantOutcome(t, w, wantUpdatedOutcome)
	if got, want := d.steps.askedVersions, "v0.41"; len(got) != 1 || got[0] != want {
		t.Fatalf("asked for releases %v; want exactly one, %q", got, want)
	}
	// The asset is named for the tag the release answered with and not for the
	// string that was submitted. They are the same on a real rollback; the fake
	// answers a different one on purpose, because a route that pasted the form
	// field into the asset name would be indistinguishable here otherwise.
	if got, want := d.steps.staged[0].version, fixtureVersion; got != want {
		t.Errorf("staged %q; want %q — the version the release said it is, never the one that was asked for", got, want)
	}
}

// TestUpdateRequiresConfirm is FR-029a's confirming step on the one route that
// replaces the running binary (contracts/self-update.md).
//
// **Must fail when** the step is dropped, or read loosely. `on`, `true`, `1` and
// an empty value are all things a stray checkbox or a hand-built request
// produces, and none of them is the deliberate act this asks for — the
// comparison is exact, which is what the table below pins.
//
// Nothing on the update path may have run: the assertion is the fake's own
// counters and not the response, because a route that downloaded a release and
// then noticed the missing confirmation would answer identically.
func TestUpdateRequiresConfirm(t *testing.T) {
	t.Parallel()

	unconfirmed := map[string]string{
		"the field was absent":           absent,
		"the field was empty":            "",
		"a checkbox posted on":           "on",
		"something posted true":          "true",
		"something posted 1":             "1",
		"the case was not the same":      "YES",
		"the value carried whitespace":   "yes ",
		"something confirmed a rollback": "v0.41",
	}
	for name, value := range unconfirmed {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			d := newUpdateDoor(t)
			form := d.confirmed(t)
			if value == absent {
				form.Del(fieldConfirm)
			} else {
				form.Set(fieldConfirm, value)
			}

			w := d.post(t, form)

			wantOutcome(t, w, wantUpdateUnconfirmedOutcome)
			if !d.steps.untouched() {
				t.Errorf("an unconfirmed update reached the update path: %s", d.steps.reached())
			}
			rec := d.record(t, audit.Deny)
			if got, want := rec["reason"], errUpdateUnconfirmed.Error(); got != want {
				t.Errorf("reason = %v; want %q", got, want)
			}
		})
	}
}

// TestUpdateCrossSiteBothHalves is FR-029a's other half, and AR-005 with it: each
// half of the cross-site defence refuses this route on its own, and every case
// here *satisfies* the half it is not testing rather than switching it off.
//
// **Must fail when** the route is registered by hand instead of through
// handleAction, or when either half is missing — a route that only checked the
// token would serve the first case, and one that only checked the initiator
// would serve the second.
//
// The counters matter more than the status. A gate that ran after the handler
// would answer 403 with the release already installed and the process already
// exiting, which is the one failure a status code cannot see (FR-003).
func TestUpdateCrossSiteBothHalves(t *testing.T) {
	t.Parallel()

	cases := map[string]func(t *testing.T, d *updateDoor) (url.Values, string){
		"the browser said the request came from another site": func(t *testing.T, d *updateDoor) (url.Values, string) {
			t.Helper()
			// The token is present and valid: only the initiator differs.
			return d.confirmed(t), "cross-site"
		},
		"the browser sent no initiator at all": func(t *testing.T, d *updateDoor) (url.Values, string) {
			t.Helper()
			return d.confirmed(t), absent
		},
		"the form carried no page token": func(t *testing.T, d *updateDoor) (url.Values, string) {
			t.Helper()
			// Same-origin, as a rendered form is: only the token differs.
			form := d.confirmed(t)
			form.Del(fieldPageToken)
			return form, secFetchSiteSameOrigin
		},
		"the form carried another identity's page token": func(t *testing.T, d *updateDoor) (url.Values, string) {
			t.Helper()
			form := d.confirmed(t)
			form.Set(fieldPageToken, mustMint(t, d.pageKey, "somebody-else@example.com", testTime))
			return form, secFetchSiteSameOrigin
		},
	}
	for name, build := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			d := newUpdateDoor(t)
			form, site := build(t, d)

			w := d.send(t, http.MethodPost, wantUpdatePath, site, form)

			if w.Code != wantActionStatus {
				t.Errorf("status = %d (%s); want %d — the gate's uniform refusal", w.Code, w.Body.String(), wantActionStatus)
			}
			if got := w.Body.String(); got != wantActionBody {
				t.Errorf("body = %q; want %q", got, wantActionBody)
			}
			if !d.steps.untouched() {
				t.Errorf("a request the gate refused reached the update path: %s", d.steps.reached())
			}
			if got, want := d.only(t)["action"], string(audit.ActionDashboardReject); got != want {
				t.Errorf("action = %v; want %v — an identity that got in and then failed the cross-site check is not an identity that never got in", got, want)
			}
		})
	}
}

// TestUpdateEmitsExactlyOneAuditRecord is FR-041 on the one route that does not
// return in production.
//
// **Must fail when** each stage emits its own record, or when the handler's early
// emit is left to race the middleware's deferred one. The success case is the
// sharp end: the record is written by the handler, before the exit, and the
// middleware's own emit still runs afterwards in this test — where the exit is a
// fake and the handler does return. Two records there is the guard in emit
// having been removed.
func TestUpdateEmitsExactlyOneAuditRecord(t *testing.T) {
	t.Parallel()

	cases := map[string]func(t *testing.T, d *updateDoor) (url.Values, audit.Decision){
		"an update that installed a release": func(t *testing.T, d *updateDoor) (url.Values, audit.Decision) {
			t.Helper()
			return d.confirmed(t), audit.Allow
		},
		"an update that was not confirmed": func(t *testing.T, d *updateDoor) (url.Values, audit.Decision) {
			t.Helper()
			form := d.confirmed(t)
			form.Del(fieldConfirm)
			return form, audit.Deny
		},
		"an update whose release would not verify": func(t *testing.T, d *updateDoor) (url.Values, audit.Decision) {
			t.Helper()
			d.steps.stageErr = updater.ErrSignatureUnverified
			return d.confirmed(t), audit.Deny
		},
	}
	for name, build := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			d := newUpdateDoor(t)
			form, decision := build(t, d)

			d.post(t, form)

			d.record(t, decision)
		})
	}
}

// releaseKeyLines is every key this binary would verify a release against, read
// from the file the daemon embeds rather than from the embedded copy: what the
// next test is about is whether any of it can leave this process, and asking the
// package under test for the answer would be asking the suspect.
func releaseKeyLines(t *testing.T) []string {
	t.Helper()

	raw, err := os.ReadFile(filepath.Join("..", "updater", "release_key.txt"))
	if err != nil {
		t.Fatalf("read the committed release keys: %v", err)
	}

	var keys []string
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		keys = append(keys, line)
	}
	if len(keys) == 0 {
		t.Skip("the committed key list is empty, so there is no key material for this daemon to leak")
	}
	return keys
}

// TestNoKeyMaterialInAnyOutput is FR-030 at this door: the public half is the
// only key this daemon holds, and even that never reaches a response, a page, an
// audit record or a diagnostic.
//
// **Must fail when** a diagnostic prints what it verified against. That is the
// tempting edit — a refusal saying "not signed by any of <keys>" is exactly what
// somebody debugging a failed update reaches for — and it would put the key list
// into the operator's journal, which is the one place a rotation is supposed to
// be invisible from.
//
// The verification refusal is driven through the **real** stager, over a staging
// directory of the test's own, so the failure it is looking at is the real
// comparison against the real embedded key list rather than a fake's canned
// error.
func TestNoKeyMaterialInAnyOutput(t *testing.T) {
	t.Parallel()

	keys := releaseKeyLines(t)

	cases := map[string]func(t *testing.T, d *updateDoor) url.Values{
		"an update that installed a release": func(t *testing.T, d *updateDoor) url.Values {
			t.Helper()
			return d.confirmed(t)
		},
		"an update refused by the real verifier": func(t *testing.T, d *updateDoor) url.Values {
			t.Helper()
			// The bytes are not a release and are not signed by anything; what is
			// real here is the code that decides so, and the key list it decides
			// against.
			d.updates.staging = updater.NewStager(func(string) string { return t.TempDir() })
			return d.confirmed(t)
		},
		"an update naming something that is not a version": func(t *testing.T, d *updateDoor) url.Values {
			t.Helper()
			d.steps.releaseErr = updater.ErrMalformedVersion
			return d.confirmed(t)
		},
	}
	for name, build := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			d := newUpdateDoor(t)
			w := d.post(t, build(t, d))

			for _, key := range keys {
				if strings.Contains(w.Body.String(), key) {
					t.Errorf("the response carried a release key:\n%s", w.Body.String())
				}
				for header, values := range w.Header() {
					for _, value := range values {
						if strings.Contains(value, key) {
							t.Errorf("header %s carried a release key: %q", header, value)
						}
					}
				}
				if trail := d.sink.String(); strings.Contains(trail, key) {
					t.Errorf("the audit trail carried a release key:\n%s", trail)
				}
				if reported := d.reported(); strings.Contains(reported, key) {
					t.Errorf("a diagnostic carried a release key:\n%s", reported)
				}
			}
		})
	}
}

// TestUpdateRefusesAReleaseThatDoesNotVerify is FR-029b: the door is not the
// control, the signature is. A caller who passed layer 1, the initiator check and
// the page token still cannot install bytes that do not verify.
//
// **Must fail when** the route stages without verifying, or installs what
// staging refused. The real stager is what makes the first half checkable from
// here — it is the object that calls updater.Verify — and the swap counter is
// what makes the second half checkable at all.
//
// The staging directory is asserted empty afterwards, which is the property a
// refusal has to leave behind: a candidate left there is a file in the directory
// this daemon renames its own binary out of.
func TestUpdateRefusesAReleaseThatDoesNotVerify(t *testing.T) {
	t.Parallel()

	d := newUpdateDoor(t)
	home := t.TempDir()
	d.updates.staging = updater.NewStager(func(string) string { return home })

	w := d.post(t, d.confirmed(t))

	wantOutcome(t, w, wantUpdateUnverifiedOutcome)
	if len(d.steps.swapped) != 0 || d.steps.exits != 0 {
		t.Errorf("a release that did not verify was installed anyway: %s", d.steps.reached())
	}
	rec := d.record(t, audit.Deny)
	if got, want := rec["reason"], errUpdateNotVerified.Error(); got != want {
		t.Errorf("reason = %v; want %q", got, want)
	}

	staging := filepath.Join(home, ".local", "share", "crswd", "staging")
	entries, err := os.ReadDir(staging)
	if err != nil && !os.IsNotExist(err) {
		t.Fatalf("read the staging directory %s: %v", staging, err)
	}
	if len(entries) != 0 {
		t.Errorf("the staging directory holds %d entries after a refusal; want none", len(entries))
	}
}

// TestARefusedUpdateNeverInstallsAndNeverExits is FR-028 step by step: whichever
// step refuses, the daemon keeps running exactly what it was running, and the
// operator is told which step it was.
//
// **Must fail when** a failure is logged and the chain carries on — the failure
// that makes every check on this path decorative — or when every refusal
// collapses to one answer, which would tell an operator to retry a release that
// did not verify.
func TestARefusedUpdateNeverInstallsAndNeverExits(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		inject  func(f *fakeUpdatePath)
		outcome outcome
		reason  error
		swaps   int
	}{
		"the release is not there": {
			inject:  func(f *fakeUpdatePath) { f.releaseErr = errors.New("404 from the release API") },
			outcome: wantUpdateNotFetchedOutcome,
			reason:  errUpdateNotFetched,
		},
		"the version is not a version": {
			inject:  func(f *fakeUpdatePath) { f.releaseErr = updater.ErrMalformedVersion },
			outcome: wantBadVersionOutcome,
			reason:  errUpdateVersionNotOffered,
		},
		"the release publishes no asset under that name": {
			inject:  func(f *fakeUpdatePath) { delete(f.published, fixtureAsset()) },
			outcome: wantUpdateNotFetchedOutcome,
			reason:  errUpdateNotFetched,
		},
		"the release is not signed": {
			inject:  func(f *fakeUpdatePath) { delete(f.published, updater.SignatureAsset) },
			outcome: wantUpdateNotFetchedOutcome,
			reason:  errUpdateNotFetched,
		},
		"the checksum does not match": {
			inject:  func(f *fakeUpdatePath) { f.stageErr = updater.ErrChecksumMismatch },
			outcome: wantUpdateUnverifiedOutcome,
			reason:  errUpdateNotVerified,
		},
		"the signature is not one this daemon knows": {
			inject:  func(f *fakeUpdatePath) { f.stageErr = updater.ErrSignatureUnverified },
			outcome: wantUpdateUnverifiedOutcome,
			reason:  errUpdateNotVerified,
		},
		"the staged release will not run on this host": {
			inject:  func(f *fakeUpdatePath) { f.swapErr = updater.ErrCandidateWillNotRun },
			outcome: wantUpdateRefusedOutcome,
			reason:  errUpdateNotInstalled,
			swaps:   1,
		},
		"the staged release calls itself another version": {
			inject:  func(f *fakeUpdatePath) { f.swapErr = updater.ErrCandidateIsAnotherVersion },
			outcome: wantUpdateRefusedOutcome,
			reason:  errUpdateNotInstalled,
			swaps:   1,
		},
		"there is nothing installed to replace": {
			inject:  func(f *fakeUpdatePath) { f.swapErr = updater.ErrNoInstalledBinary },
			outcome: wantUpdateRefusedOutcome,
			reason:  errUpdateNotInstalled,
			swaps:   1,
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			d := newUpdateDoor(t)
			tc.inject(d.steps)

			w := d.post(t, d.confirmed(t))

			wantOutcome(t, w, tc.outcome)
			if got := len(d.steps.swapped); got != tc.swaps {
				t.Errorf("swapped %d times; want %d — %s", got, tc.swaps, d.steps.reached())
			}
			if d.steps.exits != 0 {
				t.Errorf("a refused update ended the process anyway: %s", d.steps.reached())
			}
			rec := d.record(t, audit.Deny)
			if got, want := rec["reason"], tc.reason.Error(); got != want {
				t.Errorf("reason = %v; want %q — the step that refused is the whole of what the journal has to say", got, want)
			}
		})
	}
}

// TestARefusedVersionIsNeverEchoed is FR-042 on the one field this route reads.
//
// **Must fail when** the refusal quotes what arrived. The version reaches an API
// path and a filename, so the values this check exists to turn away are exactly
// the ones that must not come back out through a page or be written into the
// operator's journal — and a helpful "unknown version: <value>" is the edit that
// does both at once.
func TestARefusedVersionIsNeverEchoed(t *testing.T) {
	t.Parallel()

	const submitted = "../../.bashrc; curl evil.example"

	d := newUpdateDoor(t)
	d.steps.releaseErr = updater.ErrMalformedVersion
	form := d.confirmed(t)
	form.Set(fieldVersion, submitted)

	w := d.post(t, form)

	wantOutcome(t, w, wantBadVersionOutcome)
	for what, where := range map[string]string{
		"the response":    w.Body.String(),
		"the audit trail": d.sink.String(),
		"a diagnostic":    d.reported(),
	} {
		if strings.Contains(where, submitted) || strings.Contains(where, ".bashrc") {
			t.Errorf("%s carried the version that was submitted:\n%s", what, where)
		}
	}
	if got, want := d.record(t, audit.Deny)["reason"], errUpdateVersionNotOffered.Error(); got != want {
		t.Errorf("reason = %v; want %q", got, want)
	}
}

// TestAnUpdateIsNoRouteOnAnyOtherMethod is FR-033 on this route, and it is the
// same claim actions_test.go makes about the other five: a GET here is a path
// nothing claims, never a 405 with an Allow header naming what does work.
//
// **Must fail when** the method is dropped from the pattern, which would make
// every one of these reach the handler — and a GET that could reach it is an
// update a link can carry.
func TestAnUpdateIsNoRouteOnAnyOtherMethod(t *testing.T) {
	t.Parallel()

	for _, method := range []string{http.MethodGet, http.MethodPut, http.MethodDelete, http.MethodPatch, http.MethodHead} {
		t.Run(method, func(t *testing.T) {
			t.Parallel()

			d := newUpdateDoor(t)

			w := d.send(t, method, wantUpdatePath, secFetchSiteSameOrigin, d.confirmed(t))

			if w.Code != http.StatusNotFound {
				t.Errorf("status = %d; want %d — a method this route does not serve is a path nothing claims", w.Code, http.StatusNotFound)
			}
			if got := w.Header().Get("Allow"); got != "" {
				t.Errorf("Allow = %q; want none — an Allow header is a route table for whoever is probing", got)
			}
			if !d.steps.untouched() {
				t.Errorf("%s reached the update path: %s", method, d.steps.reached())
			}
		})
	}
}

// TestTheShippingBuildWiresTheRealUpdatePath is the seam checked from both sides,
// and it is the failure this repository has shipped five times: code with no
// production caller.
//
// **Must fail when** the wiring is dropped from newWithLayer1 — the daemon would
// serve an update route that refuses every request, which looks from the outside
// exactly like a daemon that is already up to date.
//
// The second half is the arrangement that makes the rest of this file safe to
// run: a server built the way every test in this package builds one carries no
// update path at all, so a case that forgot to stand in front of these three
// cannot download a release onto this machine and rename it over the daemon
// installed here.
func TestTheShippingBuildWiresTheRealUpdatePath(t *testing.T) {
	t.Parallel()

	t.Run("a daemon has the real four steps behind the route", func(t *testing.T) {
		t.Parallel()

		fixture := newSessionFixture(t)
		srv, err := NewWith(testConfig(loopbackListen), fixture.tmux, audit.NewTo(io.Discard, func() time.Time { return testTime }))
		if err != nil {
			t.Fatalf("NewWith = _, %v; want a server", err)
		}

		if _, ok := srv.updates.releases.(*updater.Fetcher); !ok {
			t.Errorf("releases = %T; want *updater.Fetcher — the release source is where the transport policy lives", srv.updates.releases)
		}
		if _, ok := srv.updates.staging.(*updater.Stager); !ok {
			t.Errorf("staging = %T; want *updater.Stager — the stager is the object that verifies before anything is executable", srv.updates.staging)
		}
		if _, ok := srv.updates.installer.(*updater.Swapper); !ok {
			t.Errorf("installer = %T; want *updater.Swapper — the swapper is the object that smoke-tests before it renames", srv.updates.installer)
		}
	})

	t.Run("a server built for a test has none, and the route refuses", func(t *testing.T) {
		t.Parallel()

		d := newUpdateDoor(t)
		d.updates = selfUpdate{}

		w := d.post(t, d.confirmed(t))

		wantOutcome(t, w, wantUpdateRefusedOutcome)
		if got, want := d.record(t, audit.Deny)["reason"], errUpdateUnwired.Error(); got != want {
			t.Errorf("reason = %v; want %q", got, want)
		}
	})
}
