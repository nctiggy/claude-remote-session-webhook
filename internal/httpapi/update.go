package httpapi

// update.go is the sixth route on the browser door that changes something, and
// the only one of the six that changes *this daemon* rather than a session it
// manages (US4, contracts/self-update.md).
//
// What lives here is the order of the four steps internal/updater implements and
// nothing else: fetch, then stage — which verifies before anything is executable
// — then swap, which smoke-tests before it renames. No check is re-implemented on
// this side, because a second copy of one is a copy free to be the weaker
// (FR-029b): what admits the *request* is the gate every other action runs
// behind, and what admits the *bytes* is a signature made before this request
// existed.
//
// The step this file owns outright is the last one. Swap returns with the new
// binary in place and this process still running the old one, so the exit is the
// route's to time — and it is timed after the redirect and after the record,
// because a process that ended on its way out of the handler would take both with
// it.

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"runtime"
	"time"

	"github.com/nctiggy/claude-remote-session-webhook/internal/updater"
)

// patternDashboardUpdate is the update route, from contracts/self-update.md's
// table.
//
// It is spelled the other five's way and for their reasons: under /dashboard/ so
// milestone 1's surface is untouched (FR-005) and a grep for the prefix finds
// every browser-initiated change, and the method inside the pattern so a GET here
// matches no pattern of this route's, falls to handleUnrouted's `/`, and is
// answered as a path nothing claims rather than as a 405 with an Allow header
// (FR-033).
//
// It carries no {id}, like the create and unlike the other four: an update names
// no session. What it replaces is the binary every session on this host is
// managed by, which is why there is no ownership question behind it and no
// uniform not-found for it to give.
const patternDashboardUpdate = "POST /dashboard/update"

// fieldVersion is the release an operator named, and the whole of what this route
// reads beside the confirming step (FR-022).
//
// Absent means whatever `latest` resolves to, which is the ordinary update.
// Naming one is what makes a rollback an ordinary update as well, so there is no
// second route for going backwards and no second path for it to go wrong on.
//
// The value is deliberately not checked here. internal/updater checks it at the
// two boundaries that consume it — the one that pastes it into an API path, and
// the one that joins it into a filename — which is the rule docs/security.md §2
// states; a shape check copied onto this door would be a third opinion, free to
// be the loosest of the three and free to drift from both.
const fieldVersion = "version"

// The three collaborators the seven steps live in.
//
// They are interfaces so that this route can be driven without the network,
// without writing into the operator's staging directory and without renaming
// anything over the binary this daemon is running — and three of them rather than
// one because standing in front of exactly one while leaving the others real is
// what makes a test able to say *which* step refused.
type (
	// releaseSource is step 1: what a release published, and the bytes of one
	// asset of it by exact name (FR-027).
	releaseSource interface {
		Release(ctx context.Context, version string) (*updater.Release, error)
		Asset(ctx context.Context, rel *updater.Release, name string) ([]byte, error)
	}

	// releaseStager is steps 2 to 4, in one call: the bytes are written at 0600,
	// checked against the published checksum, checked against the signature over
	// it, and only then made executable. The order is that method's, and this
	// route cannot reorder it.
	releaseStager interface {
		Stage(version, name string, asset, sums, signature []byte) (string, error)
	}

	// releaseInstaller is steps 5 and 6, and then step 7 separately. The split is
	// the whole reason ExitForRestart exists as its own method: a swap that
	// exited on the way out would end the process with the redirect unwritten and
	// the audit record unemitted.
	releaseInstaller interface {
		Swap(ctx context.Context, staged, version string) error
		ExitForRestart()
	}

	// configMigrator is the half of an update that is not the binary: the
	// operator's configuration file, brought to the schema this release
	// understands. It is a fourth collaborator rather than something the swapper
	// does on the way past, because it is the one step here that must not be able
	// to refuse an update — by the time it runs the binary is already installed.
	configMigrator interface {
		Migrate() (bool, error)
	}

	// unitCarrier is the third file an update has an answer about, and the answer
	// is not the configuration's. A unit is what systemd executes and under which
	// hardening, so one this daemon did not write is never replaced — it is left
	// exactly as it is with the release's own beside it. See internal/updater's
	// place.go for why that is the rule and what it costs to break it.
	//
	// It is handed the asset with the checksum list and the signature rather than
	// bytes this route vouched for, because the verification is the carrier's:
	// what these bytes become is a file in ~/.config/systemd/user, and a route
	// able to hand it something unverified would be a route able to choose what
	// this host runs.
	unitCarrier interface {
		Place(asset, sums, signature []byte) (updater.UnitOutcome, error)
	}
)

// selfUpdate is the update path as one field on the Server, so that a route
// reaching all of it reaches one thing rather than three that can be wired
// apart.
type selfUpdate struct {
	releases  releaseSource
	staging   releaseStager
	installer releaseInstaller
	migrator  configMigrator
	unit      unitCarrier
}

// liveSelfUpdate is the update path as the shipping build wires it: this
// project's releases, the documented staging directory, and the binary
// install.sh placed.
//
// It is built in newWithLayer1 and deliberately not in newServer, which is the
// constructor every test in this package builds a server through. A test server
// therefore carries no update path at all, and the route refuses on it — so a
// case that forgot to stand in front of these three cannot download a release
// onto the machine running the suite and rename it over the daemon that host is
// already running. TestTheShippingBuildWiresTheRealUpdatePath is the other half
// of that arrangement: a seam that could be left unwired in production is a
// dashboard button that quietly does nothing.
// configFile is the file this daemon loaded at startup, which is the one an
// update migrates — never the one a second look at the environment would find.
func liveSelfUpdate(configFile string) selfUpdate {
	return selfUpdate{
		releases:  updater.NewFetcher(),
		staging:   updater.NewStager(os.Getenv),
		installer: updater.NewSwapper(os.Getenv),
		migrator:  updater.NewConfigMigrator(configFile, os.Getenv),
		unit:      updater.NewUnit(os.Getenv),
	}
}

// wired reports whether there is an update path behind this route at all.
//
// The migrator and the unit carrier are counted with the other three rather than
// treated as optional, and that is deliberate: a daemon with no configuration
// file still has a migrator — it answers "nothing to do" — and a host with no
// unit still has a carrier, which is the case that installs one. A nil here
// means the wiring was dropped, not that this host has nothing to carry, and an
// update that quietly stopped carrying either file would look exactly like one
// that had nothing to carry.
func (u selfUpdate) wired() bool {
	return u.releases != nil && u.staging != nil && u.installer != nil && u.migrator != nil && u.unit != nil
}

// The refusals this route authors, one per step so that the journal says which
// of them turned an update away. Each is a sentinel written here, so no record
// can carry a byte the caller chose (FR-042) — and in particular none of them
// names the version that was asked for, which is the one field this route reads.
var (
	// errUpdateUnconfirmed is an update without `confirm=yes` (FR-029a). It is
	// its own sentinel rather than the destroy's or the toggle's, for the reason
	// every reason on this door is: what tells one action's records from
	// another's is this.
	errUpdateUnconfirmed = errors.New("a browser update arrived without the confirming step")

	// errUpdateVersionNotOffered is a `version` field that is not shaped like a
	// release tag (updater.ErrMalformedVersion). The value reaches a URL path and
	// a filename, and it is refused before it reaches either; the refusal names
	// neither the value nor its length, because what this check exists to turn
	// away is exactly the text nobody should be handed back or have written into
	// their journal.
	errUpdateVersionNotOffered = errors.New("a browser update named something that is not the shape of a release version")

	// errUpdateNotFetched is step 1 refusing: no such release, no asset published
	// under exactly the name asked for, a redirect to a host a release does not
	// come from, or a download that did not finish. Nothing has been written
	// anywhere on this path.
	errUpdateNotFetched = errors.New("the release a browser asked for could not be downloaded")

	// errUpdateNotVerified is steps 2 to 4 refusing, and it is the one an operator
	// should read twice: the bytes arrived and are not the ones this project
	// published, or are not signed by any key this binary carries. Nothing was
	// made executable, and the candidate has been removed.
	errUpdateNotVerified = errors.New("a release was refused before anything was made executable")

	// errUpdateNotInstalled is steps 5 and 6 refusing: a release that verified and
	// then would not run here, called itself another version, or could not be
	// renamed into place. This is where an arm64 build on an amd64 host stops
	// (FR-028) — cryptographically perfect, and not for this machine.
	errUpdateNotInstalled = errors.New("a verified release was not installed, and this daemon is running what it was running")

	// errUpdateUnwired is the route reached on a server that has no update path
	// behind it. It should not happen in a daemon built by New, and it is a
	// refusal rather than a panic for the reason every other should-not-happen
	// branch on this door is one: fail-closed is only a property if it holds on
	// the paths that do not happen.
	errUpdateUnwired = errors.New("the update route was reached on a daemon with no update path behind it")
)

// updateFromBrowser is POST /dashboard/update (US4, contracts/self-update.md).
//
// Everything that authorises it has already run: handleAction wrapped this
// handler in the gate, so layer 1 has verified an identity, the browser has said
// the request came from this page, and the form has carried a token minted for
// that identity. What is left is the confirming step and the seven steps of the
// update itself.
//
// The confirming step is read first, ahead of anything that costs this host
// anything at all. An update replaces the binary that manages every session on
// this machine and then ends the process; a request that was never going to be
// carried out must not download a release on the way to being refused, which is
// where the destroy's own ordering rule comes from (FR-029, FR-029a).
func (s *Server) updateFromBrowser(w http.ResponseWriter, r *http.Request) {
	if _, ok := OperatorFrom(r.Context()); !ok {
		// Fail closed on the path that should not happen, the way every other
		// handler on this door does: the gate in front puts the operator in the
		// context, so a false here is a route wired without one.
		AuditFrom(r.Context()).Deny(errDashboardNoOperator.Error())
		s.refuseBrowser(w)
		return
	}

	// Read from PostForm and never Form, for the reason the gate reads the token
	// that way: a confirmation this daemon would accept from a query string is a
	// binary replacement that a link can carry. The form itself was parsed by the
	// gate, under the configured body limit.
	if r.PostForm.Get(fieldConfirm) != confirmYes {
		AuditFrom(r.Context()).Deny(errUpdateUnconfirmed.Error())
		s.redirectOutcome(w, r, outcomeUpdateUnconfirmed)
		return
	}

	if !s.updates.wired() {
		AuditFrom(r.Context()).Deny(errUpdateUnwired.Error())
		s.redirectOutcome(w, r, outcomeUpdateRefused)
		return
	}

	done, err := s.updateTo(r.Context(), r.PostForm.Get(fieldVersion))
	if err != nil {
		s.refuseBrowserUpdate(w, r, err)
		return
	}
	version := done.version

	// The other half of what an update carries, and it runs here rather than
	// inside updateTo for two reasons that point the same way.
	//
	// It runs *after* the binary is in place, because an update that refused must
	// leave the host exactly as it found it — nothing on this path is renamed on
	// the way to a refusal, and the operator's configuration is the last file
	// that should be an exception.
	//
	// And its failure is reported rather than refused. Every step above can turn
	// an update away because none of them has changed anything yet; this one runs
	// after the only irreversible line in the path, and a refusal here would tell
	// the operator that nothing was installed while the new binary sat at
	// ExecStart waiting for the restart. A configuration that could not be
	// migrated is one this daemon was running perfectly well on a moment ago.
	//
	// The cause goes to stderr and never to the trail, like every other reported
	// failure on this door. It carries a path this daemon chose and whatever the
	// loader says about the operator's own file — the same sentence a refusing
	// start already writes to the same journal, and one docs/security.md §3
	// already forbids from naming the value it refused over.
	if _, err := s.updates.migrator.Migrate(); err != nil {
		s.report(fmt.Errorf("migrate the configuration file during the update to %s: %w", version, err))
	}

	// The third file, on the same terms and after the same line: the binary is in
	// place, so this cannot refuse, and a host whose unit could not be carried is
	// a host still running the unit it was running a moment ago.
	//
	// The bytes are the release's own crswd.service, handed over unverified on
	// purpose — the carrier verifies them against the checksum list and the
	// signature this route fetched, because a check re-implemented on this side is
	// a second copy free to be the weaker one (FR-029b).
	//
	// The outcome is deliberately not read here. What was done about the unit is
	// something the operator has to be told — on the settings page (T004) and in
	// the journal at startup (T005) — and neither of those is a sentence this
	// handler writes: the answer it is composing is the page that waits for the
	// restart. Nothing is lost by dropping it *here*; the file on disk is the
	// record, and it is what both of those read.
	if _, err := s.updates.unit.Place(done.unit, done.sums, done.signature); err != nil {
		s.report(fmt.Errorf("carry the systemd unit during the update to %s: %w", version, err))
	}

	// Step 7, in the order the two things that must survive it require. The
	// redirect is written and pushed onto the connection, the record reaches the
	// trail, and only then may the process end — an exit before either would
	// leave the operator watching a request that never answered and an update
	// that happened with nothing in the journal to say so (FR-041).
	//
	// The record is emitted here rather than left to the middleware for the reason
	// the stream emits its own: this handler does not return in production. The
	// emit is guarded against a second one, so the deferred emit on the way out —
	// which is what runs in a test, where the exit is a fake — writes nothing more.
	// Answered in place rather than with a redirect, and that is the whole of
	// this change.
	//
	// A 303 told the browser to go and ask for a page from a daemon that was
	// about to stop existing. The redirect was delivered, the browser followed
	// it, and the operator watched their own update turn into a connection
	// error — the one moment they most need to be told it is working.
	//
	// So the answer is a page that stays where it is and waits. It carries the
	// version it is becoming, and the script polls until a daemon answers with
	// that version and then reloads. With no script it is still a page rather
	// than an error, and it says to reload in a moment.
	s.renderUpdating(w, r, version)
	s.emit(AuditFrom(r.Context()))
	if err := http.NewResponseController(w).Flush(); err != nil { //nolint:bodyclose // false positive: a ResponseController is not a response and has no body to close.
		// Reported and not acted on. The rename has already happened, so the
		// binary at ExecStart is the new one either way; a daemon that stayed up
		// because it could not flush a redirect would be one running the release
		// it just replaced, which is the single state this whole path exists to
		// avoid.
		s.report(fmt.Errorf("flush the answer to an update of %s before exiting: %w", version, err))
	}
	// Exit after the handler returns, not inside it.
	//
	// Flush pushes the body into the socket; os.Exit then kills the process
	// before net/http has finished the response and before the connection closes
	// cleanly. What arrives at the other end is a severed connection, so a proxy
	// answers 502 and a fetch lands in its error path — the operator watched
	// their own update turn into a Cloudflare error page even though the update
	// had already succeeded.
	//
	// A goroutine and a short grace period let the response complete first.
	// Nothing is waited on: the rename has already happened, so the binary at
	// ExecStart is the new one whether or not this answer arrives, and the exit
	// must not become conditional on a browser still being there.
	go func() {
		time.Sleep(exitGrace)
		s.updates.installer.ExitForRestart()
	}()
}

// exitGrace is how long the response has to finish before this process ends.
//
// Long enough for a flushed body to leave a loopback socket and reach the proxy
// in front of it, short enough that an operator does not notice. It is not a
// correctness guarantee — nothing here can prove the far end received anything —
// and it does not need to be: the update is already installed, so the worst a
// missed answer costs is a page that has to be reloaded rather than one that
// reloads itself.
const exitGrace = 250 * time.Millisecond

// installed is what an update carried: the release now at ExecStart, and the
// three assets the step after the swap needs to decide what to do with this
// host's systemd unit.
//
// The unit's bytes travel out of updateTo rather than being acted on inside it
// because of where the only irreversible line is. Everything updateTo does can
// still refuse; placing a unit runs after the rename, where a refusal would tell
// the operator nothing was installed while the new binary sat waiting for the
// restart. Same reason the configuration migration is a statement in the handler
// and not a step here.
type installed struct {
	version string

	// unit, sums and signature are the release's own crswd.service and the two
	// files that vouch for it. They are passed on together and unverified for the
	// reason unitCarrier documents: whatever checks them has to be whatever
	// writes them.
	unit      []byte
	sums      []byte
	signature []byte
}

// updateTo is steps 1 to 6, in the order contracts/self-update.md numbers them,
// and it returns what was installed.
//
// asked is the operator's `version` field: empty means whatever `latest`
// resolves to. What every step after the first is given is the version the
// release *says* it is (rel.Version) rather than the string that was submitted —
// an empty ask has no other name to work from, and a named one that resolved to
// something else is a release this daemon should stage under the name it really
// carries.
//
// Each step's failure is wrapped in the sentinel for that step, so the handler
// can say which one refused without any of them having to be told about the
// others. The cause travels with it for the operator's stderr and never for the
// trail — the record gets the sentinel alone (FR-042).
func (s *Server) updateTo(ctx context.Context, asked string) (installed, error) {
	rel, err := s.updates.releases.Release(ctx, asked)
	if err != nil {
		if errors.Is(err, updater.ErrMalformedVersion) {
			// Deliberately not wrapped with the cause: this is the one refusal on
			// this path reached by something the caller wrote, and the value stops
			// here. Every other error below quotes at most a version internal/updater
			// has already matched against its own shape.
			return installed{}, errUpdateVersionNotOffered
		}
		return installed{}, fmt.Errorf("%w: %w", errUpdateNotFetched, err)
	}

	// This host's own architecture, asked of the toolchain rather than of the
	// request. FR-027 wants the asset named exactly, and the only machine whose
	// name is right here is the one about to run the binary — an operator cannot
	// usefully choose, and a field that let them choose would be a way to install
	// a build that passes every check and then will not exec.
	name := updater.AssetName(rel.Version, runtime.GOARCH)

	asset, err := s.updates.releases.Asset(ctx, rel, name)
	if err != nil {
		return installed{}, fmt.Errorf("%w: %w", errUpdateNotFetched, err)
	}
	// The checksum list and the signature over it are two more assets of the same
	// release, fetched by exactly their names. A release that publishes no
	// signature ends here, as a refusal rather than as a skipped check: absence is
	// not "nothing to verify against" (FR-025).
	sums, err := s.updates.releases.Asset(ctx, rel, updater.ChecksumsAsset)
	if err != nil {
		return installed{}, fmt.Errorf("%w: %w", errUpdateNotFetched, err)
	}
	signature, err := s.updates.releases.Asset(ctx, rel, updater.SignatureAsset)
	if err != nil {
		return installed{}, fmt.Errorf("%w: %w", errUpdateNotFetched, err)
	}
	// And the unit, which every release since v0.58 publishes and install.sh
	// fetches unconditionally in exactly this position. Fetched here rather than
	// after the swap so that a release missing it is refused while nothing on this
	// host has changed — an update that installed a binary and then found it had
	// nothing to compare the unit against would have to report a half-done job.
	unit, err := s.updates.releases.Asset(ctx, rel, updater.UnitAsset)
	if err != nil {
		return installed{}, fmt.Errorf("%w: %w", errUpdateNotFetched, err)
	}

	// Steps 2 to 4. The bytes are handed over rather than a path, so nothing this
	// route fetched has reached the filesystem before the one place that writes it
	// at 0600 does — and the name is the one that was fetched, because the
	// checksum list is read by exact asset name and "the nearest entry" is not the
	// entry.
	staged, err := s.updates.staging.Stage(rel.Version, name, asset, sums, signature)
	if err != nil {
		return installed{}, fmt.Errorf("%w: %w", errUpdateNotVerified, err)
	}

	// Steps 5 and 6. The candidate is executed once before it is renamed, which is
	// the step no cryptographic check can stand in for, and the rename is the only
	// irreversible line in this path.
	if err := s.updates.installer.Swap(ctx, staged, rel.Version); err != nil {
		return installed{}, fmt.Errorf("%w: %w", errUpdateNotInstalled, err)
	}
	return installed{version: rel.Version, unit: unit, sums: sums, signature: signature}, nil
}

// refuseBrowserUpdate maps a refused update onto the answer this route gives, and
// it is refuseBrowserDestroy's and refuseBrowserMode's shape for their reason:
// one function, so the branches are read together.
//
// Four arms rather than one shared failure, and the three named ones are the
// steps where an operator's next move differs: a version that is not a version is
// theirs to correct, a release that would not download is one to try again, and a
// release that did not verify is one to leave alone. Everything past verification
// shares the last arm, because by then there is one thing to say — nothing was
// installed — and the difference between a wrong-architecture build, a
// mislabelled release and a rename that failed is a question for the journal,
// where each carries a reason of its own.
//
// The cause is reported to stderr and never to the trail. It carries at most a
// version internal/updater has already matched against its own shape, an asset
// name this daemon composed, and a path it chose — an operator diagnosing a
// failed update needs all three, and none of them is a byte the caller wrote.
func (s *Server) refuseBrowserUpdate(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, errUpdateVersionNotOffered):
		AuditFrom(r.Context()).Deny(errUpdateVersionNotOffered.Error())
		s.redirectOutcome(w, r, outcomeBadVersion)
	case errors.Is(err, errUpdateNotFetched):
		AuditFrom(r.Context()).Deny(errUpdateNotFetched.Error())
		s.report(err)
		s.redirectOutcome(w, r, outcomeUpdateNotFetched)
	case errors.Is(err, errUpdateNotVerified):
		AuditFrom(r.Context()).Deny(errUpdateNotVerified.Error())
		s.report(err)
		s.redirectOutcome(w, r, outcomeUpdateUnverified)
	default:
		AuditFrom(r.Context()).Deny(errUpdateNotInstalled.Error())
		s.report(err)
		s.redirectOutcome(w, r, outcomeUpdateRefused)
	}
}

// renderUpdating answers an accepted update with the page it was asked from.
//
// It renders the settings page in one extra state rather than a page of its own,
// because everything an operator wants while waiting — what is running, what it
// is becoming — is already there, and a second page would have to say it again.
func (s *Server) renderUpdating(w http.ResponseWriter, r *http.Request, version string) {
	operator, ok := OperatorFrom(r.Context())
	if !ok {
		// Unreachable through the gate, which is why this is a refusal rather
		// than a nil dereference waiting to be found.
		AuditFrom(r.Context()).Deny(errDashboardNoOperator.Error())
		s.refuseAction(w)
		return
	}

	rows := settingsOf(s.cfg)
	// The same account of the door GET /settings composes — see renderRestarting.
	sections := sectioned(rows, doorFactsOf(s.browser))
	s.renderPage(w, r, http.StatusOK, "settings", settingsView{
		Operator:   operator,
		Settings:   rows,
		Sections:   sections,
		Shown:      sectionUpdates,
		ConfigFile: s.cfg.FilePath,
		Update:     s.updatePanelFor(r, operator),
		Becoming:   version,
	})
}
