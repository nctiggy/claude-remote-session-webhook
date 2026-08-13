package updater

// What unit.go has to be true of, against files in a directory of this test's
// own — never the unit of the daemon running the suite.
//
// The case this file exists for is the one an operator is living: they relaxed
// three hardening settings in their unit so that `sudo` works inside a session,
// and every update from here on has to leave that edit exactly where it is.
// TestAnOperatorsHardeningEditIsNeverThisDaemonsToReplace is that case, and it
// is written with the real setting names because the failure it guards against
// is somebody deciding that a unit "close enough" to the published one is ours.
//
// The rest of the table is the other three answers, and each is a different
// action in T003: absent, already current, and ours-and-out-of-date. They are
// four values rather than a bool for that reason — "replace it" and "leave it
// alone" are not the whole question, and a comparison that answered only those
// two would give a host with no unit at all the same treatment as a host whose
// operator wrote one.
//
// The digests here are computed with crypto/sha256 rather than through
// unitDigest, so that what the record has to hold is stated independently of the
// code that reads it. A test that built the record with the production helper
// would pass against any hash function both halves happened to share.

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// publishedUnit is the unit a release ships, in the fixtures here and in
// verify_test.go's release. Short, and shaped like the real one: what every
// comparison below turns on is whether the bytes are equal, and a longer file
// would say nothing more.
const publishedUnit = "[Unit]\nDescription=crswd\n\n[Service]\nExecStart=%h/.local/bin/crswd\nRestart=always\n"

// unitFixture is a host: a home directory with whatever unit and whatever record
// a case needs, and nothing else.
type unitFixture struct {
	home   string
	unit   *Unit
	path   string
	record string
}

// newUnitFixture builds that host. A nil unit or record means the file is not
// there, which is a state two of the cases below are entirely about.
func newUnitFixture(t *testing.T, unit, record []byte) *unitFixture {
	t.Helper()

	home := t.TempDir()
	path := filepath.Join(home, unitPath)
	recordPath := filepath.Join(home, unitRecordPath)

	for at, contents := range map[string][]byte{path: unit, recordPath: record} {
		if contents == nil {
			continue
		}
		if err := os.MkdirAll(filepath.Dir(at), 0o700); err != nil {
			t.Fatalf("make the fixture directory for %s: %v", at, err)
		}
		if err := os.WriteFile(at, contents, 0o600); err != nil {
			t.Fatalf("write the fixture %s: %v", at, err)
		}
	}

	return &unitFixture{home: home, unit: newUnit(path, recordPath), path: path, record: recordPath}
}

// digestOf is the record install.sh would have written for these bytes.
func digestOf(contents string) []byte {
	return []byte(fmt.Sprintf("%x\n", sha256.Sum256([]byte(contents))))
}

// TestUnitStandingAnswersWhatAnUpdateHasToDecide is the four answers, one row
// each.
//
// **Must fail when** any of them is folded into another. The pairs that look
// alike and are not: absent and not-ours both mean "this daemon did not write
// what is there", and only one of them is a file being protected; current and
// ours both mean "we know what that file is", and only one of them has anything
// to carry forward.
func TestUnitStandingAnswersWhatAnUpdateHasToDecide(t *testing.T) {
	t.Parallel()

	// A unit this project wrote and then superseded: it is not the published
	// bytes, and its digest is what the record holds.
	const older = "[Unit]\nDescription=crswd\n\n[Service]\nExecStart=%h/bin/crswd\n"

	for _, c := range []struct {
		name   string
		unit   []byte
		record []byte
		want   UnitStanding
		why    string
	}{
		{
			name: "no unit on this host",
			want: UnitAbsent,
			why:  "there is nothing at that path, so nothing is being protected from anything and the decision is open",
		},
		{
			name:   "no unit and a record left over from one",
			record: digestOf(older),
			want:   UnitAbsent,
			why:    "a record describes a file that is not there; the host still has no unit",
		},
		{
			name: "the unit the release publishes",
			unit: []byte(publishedUnit),
			want: UnitCurrent,
			why:  "the bytes are identical, so there is nothing to carry forward whoever wrote them",
		},
		{
			name:   "the unit the release publishes, with a stale record",
			unit:   []byte(publishedUnit),
			record: digestOf(older),
			want:   UnitCurrent,
			why:    "what the record says cannot make identical bytes out of date",
		},
		{
			name:   "a unit this daemon wrote, superseded by the release",
			unit:   []byte(older),
			record: digestOf(older),
			want:   UnitOurs,
			why:    "untouched since this project wrote it, which is the one case where replacing it takes nothing away",
		},
		{
			name: "a unit with no record of one",
			unit: []byte(older),
			want: UnitTheirs,
			why:  "every host deployed before the installer existed is in this state, and absence of evidence that we wrote a file is not permission to replace it",
		},
		{
			name:   "a unit that has been edited since we wrote it",
			unit:   []byte(older + "Environment=CRSW_MAX_SESSIONS=1\n"),
			record: digestOf(older),
			want:   UnitTheirs,
			why:    "the record describes bytes that are no longer there, so somebody changed them on purpose",
		},
		{
			name:   "a record that is not a digest",
			unit:   []byte(older),
			record: []byte("whatever was in there\n"),
			want:   UnitTheirs,
			why:    "there is no information in an unreadable record, so it decides nothing and the file is left alone",
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			fixture := newUnitFixture(t, c.unit, c.record)
			got, err := fixture.unit.Standing([]byte(publishedUnit))
			if err != nil {
				t.Fatalf("Standing() = _, %v; want %v", err, c.want)
			}
			if got != c.want {
				t.Errorf("Standing() = %v, want %v.\n%s", got, c.want, c.why)
			}
		})
	}
}

// TestAnOperatorsHardeningEditIsNeverThisDaemonsToReplace is the case this
// milestone was asked for, in the operator's own words: they relaxed three
// settings so `sudo` works inside a session, and an update must not take that
// back.
//
// **Must fail when** ownership is decided by anything other than the recorded
// digest — a comparison against "looks like ours", against the published bytes
// with the edits ignored, or against the file merely existing. Each of those
// reads this unit as replaceable, and each costs the operator the same
// afternoon, every release.
func TestAnOperatorsHardeningEditIsNeverThisDaemonsToReplace(t *testing.T) {
	t.Parallel()

	// The unit as this project shipped it, hardened.
	const ours = "[Service]\nExecStart=%h/.local/bin/crswd\nNoNewPrivileges=yes\nRestrictSUIDSGID=yes\nProtectSystem=strict\n"
	// The same file after the operator made sudo work in a session.
	const relaxed = "[Service]\nExecStart=%h/.local/bin/crswd\nNoNewPrivileges=no\nRestrictSUIDSGID=no\nProtectSystem=no\n"

	fixture := newUnitFixture(t, []byte(relaxed), digestOf(ours))

	standing, err := fixture.unit.Standing([]byte(publishedUnit))
	if err != nil {
		t.Fatalf("Standing() = _, %v; want the operator's edited unit read as theirs", err)
	}
	if standing != UnitTheirs {
		t.Fatalf("Standing() = %v, want %v.\nThis is the unit an operator edited so that sudo works inside a session; a release that replaced it would undo that silently, and they would rediscover it every time", standing, UnitTheirs)
	}

	// Nothing on this path writes. The reading half of the answer has to be
	// exactly that — T003 is where a file is placed, and a comparison that
	// touched the operator's unit would already have taken the decision.
	after, err := os.ReadFile(fixture.path) //nolint:gosec // G304: a path inside this test's own temporary directory.
	if err != nil {
		t.Fatalf("read the unit back: %v", err)
	}
	if string(after) != relaxed {
		t.Errorf("the comparison changed the operator's unit:\ngot:\n%s\nwant:\n%s", after, relaxed)
	}
	if _, err := os.Stat(fixture.path + ".new"); err == nil {
		t.Error("the comparison wrote a .new unit. Putting one beside theirs is T003's, and a step that both decides and writes is one nothing can stand in front of")
	}
}

// TestUnitStandingRefusesWithoutAHomeToLookIn holds the difference between "this
// host has no unit" and "this daemon cannot tell".
//
// **Must fail when** an unresolvable home is answered with UnitAbsent. That is a
// fact about the daemon's own environment reported as a fact about the
// operator's host, and the action T003 takes on it — placing a unit where one
// was thought to be missing — is the one an empty answer must never trigger.
func TestUnitStandingRefusesWithoutAHomeToLookIn(t *testing.T) {
	t.Parallel()

	for _, home := range []string{"", "  ", "relative/home"} {
		u := NewUnit(func(string) string { return home })
		if u.Path() != "" || u.RecordPath() != "" {
			t.Errorf("NewUnit with HOME=%q named %q and %q; neither is a path this daemon may act on", home, u.Path(), u.RecordPath())
		}

		standing, err := u.Standing([]byte(publishedUnit))
		if !errors.Is(err, ErrNoUnitHome) {
			t.Errorf("Standing() with HOME=%q = _, %v; want ErrNoUnitHome", home, err)
		}
		if standing != UnitTheirs {
			t.Errorf("Standing() with HOME=%q = %v; a standing this daemon could not compute must read as a file to leave alone", home, standing)
		}
	}
}

// TestNewUnitLooksWhereTheInstallerWrote pins the composition itself: the two
// paths are under this process's home, and they are the installer's.
func TestNewUnitLooksWhereTheInstallerWrote(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	u := NewUnit(func(name string) string {
		if name != envHome {
			t.Errorf("the unit was resolved from %s; install.sh composes both paths from %s alone", name, envHome)
		}
		return home
	})

	if want := filepath.Join(home, unitPath); u.Path() != want {
		t.Errorf("NewUnit reads %q, want %q", u.Path(), want)
	}
	if want := filepath.Join(home, unitRecordPath); u.RecordPath() != want {
		t.Errorf("NewUnit records at %q, want %q", u.RecordPath(), want)
	}
}

// TestUnitAssetAndPathsAreTheInstallersOwn holds this package and install.sh to
// one asset name and two paths.
//
// **Must fail when** either file moves. They cannot share a constant — one is Go
// and one is shell — and every drift here is silent: an asset name that has
// drifted asks a release for a file it does not publish, a unit path that has
// drifted reports every host as having no unit, and a record path that has
// drifted reads every unit as one this project never wrote and hands out a .new
// beside a file that is already current.
func TestUnitAssetAndPathsAreTheInstallersOwn(t *testing.T) {
	t.Parallel()

	installer, err := os.ReadFile(installerSource)
	if err != nil {
		t.Fatalf("read %s: %v", installerSource, err)
	}

	for _, c := range []struct {
		declared string
		here     string
		cost     string
	}{
		{declared: "UNIT_ASSET", here: UnitAsset, cost: "an update asks a release for an asset it does not publish, and every host is told its unit could not be checked"},
		{declared: "UNIT", here: unitPath, cost: "an update reads a file systemd never loads, so every host looks like one with no unit at all"},
		{declared: "UNIT_RECORD", here: unitRecordPath, cost: "an update finds no record anywhere, so no unit is ever this project's to replace and every host is handed a .new it does not need"},
	} {
		t.Run(c.declared, func(t *testing.T) {
			t.Parallel()

			pattern := regexp.MustCompile(`(?m)^readonly ` + c.declared + `="([^"]+)"`)
			declared := pattern.FindStringSubmatch(string(installer))
			if declared == nil {
				t.Fatalf("%s declares no %s, so this test can no longer see what the installer uses.\nIf it moved rather than went away, move this pattern with it — these two files agree by nothing but this test", installerSource, c.declared)
			}
			if declared[1] != c.here {
				t.Errorf("%s uses %q and this package uses %q.\n%s", installerSource, declared[1], c.here, c.cost)
			}
		})
	}
}

// TestThePublishedUnitIsDeliveredLikeEveryOtherAsset is the other half of T002:
// the unit an update compares against arrives through the delivery that already
// exists, and is held to it.
//
// **Must fail when** the unit is fetched or trusted some other way. A second
// channel for it would be a second thing to verify and, being the one nobody
// looks at, the one that gets verified less — and what it delivers is written
// into ~/.config/systemd/user, where systemd executes whatever it says.
func TestThePublishedUnitIsDeliveredLikeEveryOtherAsset(t *testing.T) {
	t.Parallel()

	release, private := published(t)

	if err := verifyAgainst(UnitAsset, []byte(publishedUnit), release.sums, release.signature, release.keys); err != nil {
		t.Fatalf("the published unit did not verify against the release that published it: %v.\nIt is summed in SHA256SUMS beside the tarball and covered by the same signature; if it is not, an update has nothing it can trust to compare a host's unit against", err)
	}

	// The same list and the same signature, over a unit somebody changed on the
	// way. Nothing about the record path or the comparison saves a host from
	// this — only the delivery does.
	tampered := []byte(publishedUnit + "ExecStart=/tmp/theirs\n")
	if err := verifyAgainst(UnitAsset, tampered, release.sums, release.signature, release.keys); !errors.Is(err, ErrChecksumMismatch) {
		t.Errorf("a tampered unit verified: %v; want ErrChecksumMismatch.\nThose bytes are placed in ~/.config/systemd/user, where they decide what this host executes and with which privileges", err)
	}

	// And a release that publishes no checksum for the unit at all is a refusal
	// rather than a unit taken on trust — the same answer FR-025 requires of a
	// missing signature, for the same reason.
	unsummed := regexp.MustCompile(`(?m)^.*`+regexp.QuoteMeta(UnitAsset)+`\n`).ReplaceAllString(string(release.sums), "")
	if unsummed == string(release.sums) {
		t.Fatalf("the fixture release does not sum %s, so this case is asserting nothing", UnitAsset)
	}
	release.resum(private, []byte(unsummed))
	if err := verifyAgainst(UnitAsset, []byte(publishedUnit), release.sums, release.signature, release.keys); !errors.Is(err, ErrUnsummedAsset) {
		t.Errorf("a unit no checksum covers was accepted: %v; want ErrUnsummedAsset.\nSilence about an asset is not permission to install it", err)
	}
	if !strings.Contains(string(release.sums), AssetName(testVersion, "amd64")) {
		t.Fatal("the fixture lost the tarball's own line, so the case above proved something else")
	}
}

// TestTheUnitReportIsReadOffTheFilesThemselves is T004's evidence: what the
// settings page and the journal say about this host's unit, worked out from the
// files on it and from nothing else.
//
// **Must fail when** the report needs a release to be reachable. Standing takes
// the published bytes because an update has them; a page being rendered does
// not, and a render that fetched them would make the settings page as slow and
// as fallible as somebody else's API — on a page whose first job is reporting
// local configuration.
//
// The arrangements below are what an operator can be told, and the one that
// matters is the offer: a crswd.service.new beside their unit is this daemon
// saying "there is a newer one than yours", and it is the file they diff.
func TestTheUnitReportIsReadOffTheFilesThemselves(t *testing.T) {
	t.Parallel()

	// A unit this project wrote and then superseded, exactly as
	// TestUnitStandingAnswersWhatAnUpdateHasToDecide uses it.
	const older = "[Unit]\nDescription=crswd\n\n[Service]\nExecStart=%h/bin/crswd\n"
	// The unit the operator this milestone is for wrote for themselves.
	const theirs = "[Service]\nExecStart=%h/bin/crswd\nNoNewPrivileges=no\n"

	for _, c := range []struct {
		name   string
		unit   []byte
		record []byte
		offer  []byte
		want   UnitReport
		why    string
	}{
		{
			name: "no unit on this host",
			want: UnitReport{},
			why:  "nothing is there, so nothing is present, nothing is ours and nothing is waiting",
		},
		{
			name:   "a unit this daemon wrote and has not been touched since",
			unit:   []byte(older),
			record: digestOf(older),
			want:   UnitReport{Present: true, Ours: true},
			why:    "the digest install.sh recorded describes the file that is there, which is the whole of what says an update may replace it",
		},
		{
			name: "the operator's own unit, with no record of one",
			unit: []byte(theirs),
			want: UnitReport{Present: true},
			why:  "no record is the state every host deployed before the installer existed is in, and it is never permission to replace a file",
		},
		{
			name:   "the operator's own unit, with a record describing an older one",
			unit:   []byte(theirs),
			record: digestOf(older),
			want:   UnitReport{Present: true},
			why:    "a record that does not describe the file that is there means somebody edited it, which is exactly the operator this milestone is for",
		},
		{
			name:   "a newer unit waiting beside the operator's own",
			unit:   []byte(theirs),
			record: digestOf(older),
			offer:  []byte(publishedUnit),
			want:   UnitReport{Present: true},
			why:    "this is the case the whole milestone is about: their file untouched, and the release's own beside it to be diffed",
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			fixture := newUnitFixture(t, c.unit, c.record)
			want := c.want
			want.Path = fixture.path
			if c.offer != nil {
				want.Offer = fixture.unit.NewPath()
				if err := os.WriteFile(want.Offer, c.offer, unitMode); err != nil {
					t.Fatalf("write the fixture offer %s: %v", want.Offer, err)
				}
			}

			got, err := fixture.unit.Report()
			if err != nil {
				t.Fatalf("Report() = _, %v; want %+v", err, want)
			}
			if got != want {
				t.Errorf("Report() = %+v, want %+v.\n%s", got, want, c.why)
			}
		})
	}
}

// TestTheUnitReportNamesNoOfferThatIsNotThere is the half of it an operator pays
// for directly.
//
// **Must fail when** Offer is composed from the unit path rather than read off
// the disk. Both spellings type-check and both look right in a review; one of
// them sends an operator to diff a file that is not there, on the one page that
// exists to tell them the truth about this host.
func TestTheUnitReportNamesNoOfferThatIsNotThere(t *testing.T) {
	t.Parallel()

	fixture := newUnitFixture(t, []byte(publishedUnit), digestOf(publishedUnit))

	report, err := fixture.unit.Report()
	if err != nil {
		t.Fatalf("Report() = _, %v", err)
	}
	if report.Offer != "" {
		t.Errorf("Report() names an offer at %q on a host that has none.\nNothing is there to diff, and a page saying otherwise is worse than the silence this milestone set out to fix", report.Offer)
	}
}

// TestTheUnitReportRefusesWithoutAHomeToLookIn is
// TestUnitStandingRefusesWithoutAHomeToLookIn's claim for the read T004 makes.
//
// **Must fail when** an unresolvable home answers with the zero report. That
// value reads as "this host has no unit", which is a fact about the daemon's own
// environment stated as a fact about the operator's machine — and it is the one
// a page would turn into "an update installs one".
func TestTheUnitReportRefusesWithoutAHomeToLookIn(t *testing.T) {
	t.Parallel()

	for _, home := range []string{"", "  ", "relative/home"} {
		u := NewUnit(func(string) string { return home })

		report, err := u.Report()
		if !errors.Is(err, ErrNoUnitHome) {
			t.Errorf("Report() with HOME=%q = _, %v; want ErrNoUnitHome", home, err)
		}
		if report != (UnitReport{}) {
			t.Errorf("Report() with HOME=%q = %+v; a report this daemon could not compute must carry no findings at all", home, report)
		}
	}
}

// operatorPages are the two documents that tell an operator what an update does
// to the files this package writes: the page somebody reads before they have the
// repository, and the one they follow with the unit in front of them.
var operatorPages = []string{"../../README.md", "../../deploy/README.md"}

// TestTheOperatorPagesNameTheFilesThisPackageWrites is T006 held to T002 and
// T003, and it is the same agreement TestUnitAssetAndPathsAreTheInstallersOwn
// makes with install.sh: four languages spell these names and nothing but a test
// keeps them the same.
//
// **Must fail when** a name here moves and the documentation does not follow.
// Every drift is silent in the direction that costs an operator most. A page
// naming the wrong offer sends them to diff a file that is not there, which is
// the "difference nobody can see" this milestone exists to end; a page naming the
// wrong record tells them to hand over a unit by writing a file this daemon never
// reads, so the next update goes on offering a .new they thought they had
// answered.
func TestTheOperatorPagesNameTheFilesThisPackageWrites(t *testing.T) {
	t.Parallel()

	for _, page := range operatorPages {
		t.Run(page, func(t *testing.T) {
			t.Parallel()

			raw, err := os.ReadFile(page) //nolint:gosec // G304: the path is one of the two documents named above, committed to this repository.
			if err != nil {
				t.Fatalf("read %s: %v", page, err)
			}
			doc := string(raw)

			for _, c := range []struct {
				name string
				cost string
			}{
				{name: unitPath, cost: "an operator told to look at the wrong file cannot see what an update decided about theirs"},
				{name: unitRecordPath, cost: "the record is the whole of what says a unit is this daemon's to replace, and a page naming another path describes a host that does not exist"},
				{name: UnitAsset + newSuffix, cost: "the offer is the file they diff, and a name they cannot find is a decision they cannot take"},
			} {
				if !strings.Contains(doc, c.name) {
					t.Errorf("%s never names %q.\n%s", page, c.name, c.cost)
				}
			}
		})
	}
}
