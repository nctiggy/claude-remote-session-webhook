package updater

// What place.go has to be true of, against files in a directory of this test's
// own — never the unit of the daemon running the suite.
//
// The case this file exists for is the third one: an operator who relaxed three
// hardening settings so that `sudo` works inside a session still has them
// relaxed after an update, and now has a file naming what they are missing.
// unit_test.go proves the *comparison* leaves their unit alone; this proves the
// step that writes does too, which is the one that could actually take it away.
//
// Every case drives Place with a real signed release, because verification is
// part of what Place does rather than something a caller was trusted to have
// done: what these bytes become is a file systemd executes.

import (
	"crypto/ed25519"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// placing is a host and the release being installed onto it: the fixture
// unit_test.go builds, with a signed crswd.service to carry.
type placing struct {
	*unitFixture
	release fixture
	signer  ed25519.PrivateKey
}

// newPlacing builds both halves. The release's UnitAsset is publishedUnit, the
// same bytes every standing in unit_test.go is computed against.
func newPlacing(t *testing.T, unit, record []byte) *placing {
	t.Helper()

	release, signer := published(t)
	p := &placing{unitFixture: newUnitFixture(t, unit, record), release: release, signer: signer}
	// The committed key cannot sign a fixture, so the seam takes the pair this
	// release was signed with. TestTheUnitIsVerifiedWithTheCommittedKey is what
	// holds the production wiring to the real thing.
	p.unit.verify = func(name string, asset, sums, signature []byte) error {
		return verifyAgainst(name, asset, sums, signature, release.keys)
	}
	return p
}

// place runs the step under test with this release's own unit.
func (p *placing) place() (UnitOutcome, error) {
	return p.unit.Place([]byte(publishedUnit), p.release.sums, p.release.signature)
}

// contents is what is at path now, or "" if nothing is.
func (p *placing) contents(t *testing.T, path string) string {
	t.Helper()

	data, err := os.ReadFile(path) //nolint:gosec // G304: a path inside this test's own temporary directory.
	if errors.Is(err, fs.ErrNotExist) {
		return ""
	}
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}

// offered is the crswd.service.new beside the operator's unit.
func (p *placing) offered() string { return p.path + newSuffix }

// TestPlaceReplacesOnlyTheUnitThisDaemonWrote is the four branches, one row
// each: what is at the unit path afterwards, what is beside it, and what the
// record says.
//
// **Must fail when** any branch writes where it should not. The rows are the
// four standings and they differ in exactly what makes them safe — the record is
// the only evidence that a unit is this project's, and a branch that acted on
// anything else would replace a file somebody wrote for a reason this daemon
// cannot see.
func TestPlaceReplacesOnlyTheUnitThisDaemonWrote(t *testing.T) {
	t.Parallel()

	// A unit this project wrote and then superseded, and the same file after an
	// operator changed one line of it.
	const ours = "[Unit]\nDescription=crswd\n\n[Service]\nExecStart=%h/bin/crswd\n"
	const edited = ours + "Environment=CRSW_MAX_SESSIONS=1\n"

	for _, c := range []struct {
		name       string
		unit       []byte
		record     []byte
		want       UnitOutcome
		wantUnit   string // what is at ~/.config/systemd/user/crswd.service afterwards
		wantOffer  string // what is at crswd.service.new afterwards, "" for nothing
		wantRecord string // what the record holds afterwards, "" for nothing
		why        string
	}{
		{
			name:       "a unit this daemon wrote, superseded by the release",
			unit:       []byte(ours),
			record:     digestOf(ours),
			want:       UnitReplaced,
			wantUnit:   publishedUnit,
			wantRecord: string(digestOf(publishedUnit)),
			why:        "untouched since this project wrote it, which is the one case where replacing it takes nothing away — and the record has to move with it, or the next update reads this file as somebody else's",
		},
		{
			name:       "no unit on this host",
			want:       UnitInstalled,
			wantUnit:   publishedUnit,
			wantRecord: string(digestOf(publishedUnit)),
			why:        "there is nothing to protect and nothing to take away; this is install.sh's own first row",
		},
		{
			name:      "a unit that has been edited since we wrote it",
			unit:      []byte(edited),
			record:    digestOf(ours),
			want:      UnitOffered,
			wantUnit:  edited,
			wantOffer: publishedUnit,
			// The record still describes the file the operator edited, which is
			// what keeps the next update reading it as theirs.
			wantRecord: string(digestOf(ours)),
			why:        "somebody changed those bytes on purpose, so the release goes alongside and the decision stays theirs",
		},
		{
			name:      "a unit with no record of one",
			unit:      []byte(ours),
			want:      UnitOffered,
			wantUnit:  ours,
			wantOffer: publishedUnit,
			why:       "every host deployed before the installer existed is in this state, and an update that recorded this file would be claiming one it never wrote",
		},
		{
			name:     "the unit the release publishes",
			unit:     []byte(publishedUnit),
			want:     UnitUnchanged,
			wantUnit: publishedUnit,
			why:      "identical bytes, so there is nothing to carry — and no record either, because writing one would let the next release replace a file this daemon did not write",
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			p := newPlacing(t, c.unit, c.record)

			got, err := p.place()
			if err != nil {
				t.Fatalf("Place() = _, %v; want %v", err, c.want)
			}
			if got != c.want {
				t.Errorf("Place() = %v, want %v.\n%s", got, c.want, c.why)
			}
			if unit := p.contents(t, p.path); unit != c.wantUnit {
				t.Errorf("the unit is now:\n%s\nwant:\n%s\n%s", unit, c.wantUnit, c.why)
			}
			if offer := p.contents(t, p.offered()); offer != c.wantOffer {
				t.Errorf("%s is now:\n%s\nwant:\n%s\n%s", p.offered(), offer, c.wantOffer, c.why)
			}
			if record := p.contents(t, p.record); record != c.wantRecord {
				t.Errorf("the record is now %q, want %q.\n%s", record, c.wantRecord, c.why)
			}
		})
	}
}

// TestAnOperatorsHardeningEditSurvivesAnUpdateAndIsToldAbout is the case the
// milestone was asked for, from the side that writes.
//
// **Must fail when** the update replaces their unit — they lose `sudo` inside
// every session and have to rediscover why, once per release — **or when it
// leaves them nothing to look at**, which is the silence this milestone exists
// to end. Both halves are the point: the file is theirs, and the difference is
// visible.
func TestAnOperatorsHardeningEditSurvivesAnUpdateAndIsToldAbout(t *testing.T) {
	t.Parallel()

	// The unit as this project shipped it, hardened, and the same file after the
	// operator made sudo work in a session.
	const ours = "[Service]\nExecStart=%h/.local/bin/crswd\nNoNewPrivileges=yes\nRestrictSUIDSGID=yes\nProtectSystem=strict\n"
	const relaxed = "[Service]\nExecStart=%h/.local/bin/crswd\nNoNewPrivileges=no\nRestrictSUIDSGID=no\nProtectSystem=no\n"

	p := newPlacing(t, []byte(relaxed), digestOf(ours))

	outcome, err := p.place()
	if err != nil {
		t.Fatalf("Place() = _, %v; want the operator's unit left alone", err)
	}
	if outcome != UnitOffered {
		t.Fatalf("Place() = %v, want %v", outcome, UnitOffered)
	}

	if after := p.contents(t, p.path); after != relaxed {
		t.Errorf("the update changed the unit an operator edited so that sudo works inside a session:\ngot:\n%s\nwant:\n%s", after, relaxed)
	}
	if offer := p.contents(t, p.offered()); offer != publishedUnit {
		t.Errorf("%s holds:\n%s\nwant the unit this release ships:\n%s\nAn operator who cannot see the difference cannot decide, and being unable to find out is the defect this milestone is about", p.offered(), offer, publishedUnit)
	}
	// Nothing recorded. A record of the offer, or of their file, is what would
	// let the *next* release read their unit as this project's and replace it —
	// this refusal undoing itself one release later.
	if record := p.contents(t, p.record); record != string(digestOf(ours)) {
		t.Errorf("the record is now %q; want the digest of the unit this project last wrote, unchanged. Recording anything here hands the next update permission to overwrite a file it did not write", record)
	}
	if got, want := p.unit.NewPath(), p.offered(); got != want {
		t.Errorf("NewPath() = %q, want %q — it is the name the operator is told to diff", got, want)
	}
}

// TestPlaceWithdrawsAnOfferItHasMadeUntrue holds the other direction of the same
// silence.
//
// **Must fail when** a crswd.service.new survives an update that brought the
// unit beside it up to date. That file is a claim — "there is a newer unit than
// the one you are running" — and after a replacement it names an *older* file
// than the one now installed, which is worse than saying nothing at all.
func TestPlaceWithdrawsAnOfferItHasMadeUntrue(t *testing.T) {
	t.Parallel()

	const ours = "[Unit]\nDescription=crswd\n\n[Service]\nExecStart=%h/bin/crswd\n"
	const stale = "[Service]\nExecStart=%h/.local/bin/crswd\n# from the release before this one\n"

	for _, c := range []struct {
		name   string
		unit   []byte
		record []byte
		want   UnitOutcome
	}{
		{name: "after the unit is replaced", unit: []byte(ours), record: digestOf(ours), want: UnitReplaced},
		{name: "after a unit is installed where there was none", want: UnitInstalled},
		{name: "when the unit is already current", unit: []byte(publishedUnit), want: UnitUnchanged},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			p := newPlacing(t, c.unit, c.record)
			// The directory too: one of these rows is a host with no unit, so
			// nothing has created it yet.
			if err := os.MkdirAll(filepath.Dir(p.offered()), 0o700); err != nil {
				t.Fatalf("make the directory the stale offer sits in: %v", err)
			}
			if err := os.WriteFile(p.offered(), []byte(stale), 0o644); err != nil { //nolint:gosec // G306: install.sh's own mode for this file, inside this test's temporary directory.
				t.Fatalf("write the stale offer: %v", err)
			}

			outcome, err := p.place()
			if err != nil {
				t.Fatalf("Place() = _, %v; want %v", err, c.want)
			}
			if outcome != c.want {
				t.Fatalf("Place() = %v, want %v", outcome, c.want)
			}
			if _, err := os.Stat(p.offered()); !errors.Is(err, fs.ErrNotExist) {
				t.Errorf("%s survived: %v.\nIt says this host is behind, beside a unit that is not", p.offered(), p.contents(t, p.offered()))
			}
		})
	}
}

// TestPlaceRefusesAUnitTheReleaseDidNotPublish is the check that cannot be
// skipped by calling this some other way.
//
// **Must fail when** the verification is moved out to the caller or dropped:
// these bytes are written into ~/.config/systemd/user, where systemd runs what
// they say, with which environment, under which hardening. A tampered unit
// accepted here is a host handed to whoever tampered with it — and on the
// UnitOurs branch it would be written straight over the working one.
func TestPlaceRefusesAUnitTheReleaseDidNotPublish(t *testing.T) {
	t.Parallel()

	const ours = "[Unit]\nDescription=crswd\n\n[Service]\nExecStart=%h/bin/crswd\n"
	tampered := []byte(publishedUnit + "ExecStart=/tmp/theirs\n")

	for _, c := range []struct {
		name   string
		unit   []byte
		record []byte
	}{
		{name: "over a unit this daemon wrote", unit: []byte(ours), record: digestOf(ours)},
		{name: "onto a host with no unit at all"},
		{name: "beside a unit the operator wrote", unit: []byte(ours)},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			p := newPlacing(t, c.unit, c.record)

			outcome, err := p.unit.Place(tampered, p.release.sums, p.release.signature)
			if !errors.Is(err, ErrChecksumMismatch) {
				t.Fatalf("Place() = _, %v; want %v", err, ErrChecksumMismatch)
			}
			if outcome != UnitUnchanged {
				t.Errorf("Place() = %v; a refused unit changed nothing, and the outcome has to say so", outcome)
			}
			if got := p.contents(t, p.path); got != string(c.unit) {
				t.Errorf("the unit on this host is now:\n%s\nwant it untouched:\n%s", got, c.unit)
			}
			if got := p.contents(t, p.offered()); got != "" {
				t.Errorf("a unit nothing vouched for was written to %s:\n%s", p.offered(), got)
			}
		})
	}
}

// TestPlaceKeepsThePermissionsSomebodyChose covers what a digest cannot see.
//
// **Must fail when** a replacement widens the mode back to the installer's
// 0644. A chmod leaves the contents — and therefore the record — identical, so a
// unit narrowed on purpose still reads as this daemon's to replace; putting the
// default back would be the same silent revert as overwriting an edit, in the
// one dimension the ownership check is blind to.
func TestPlaceKeepsThePermissionsSomebodyChose(t *testing.T) {
	t.Parallel()

	const ours = "[Unit]\nDescription=crswd\n\n[Service]\nExecStart=%h/bin/crswd\n"
	p := newPlacing(t, []byte(ours), digestOf(ours))
	if err := os.Chmod(p.path, 0o600); err != nil {
		t.Fatalf("narrow the fixture unit: %v", err)
	}

	if _, err := p.place(); err != nil {
		t.Fatalf("Place() = _, %v; want a replacement", err)
	}

	info, err := os.Stat(p.path)
	if err != nil {
		t.Fatalf("inspect the replaced unit: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Errorf("the replaced unit is mode %04o; want 0600, the mode it had", got)
	}
}

// TestPlaceGivesAHostWithNoUnitTheInstallersOwnMode holds the other half: a file
// this daemon creates is the file install.sh would have created.
//
// **Must fail when** it is written at the umask's mode, or at 0600. systemd's
// user manager reads it as this account either way, but an operator comparing
// two hosts — one installed, one updated into having a unit — must not find two
// different files.
func TestPlaceGivesAHostWithNoUnitTheInstallersOwnMode(t *testing.T) {
	t.Parallel()

	p := newPlacing(t, nil, nil)

	if _, err := p.place(); err != nil {
		t.Fatalf("Place() = _, %v; want the published unit installed", err)
	}

	for _, at := range []string{p.path, p.record} {
		info, err := os.Stat(at)
		if err != nil {
			t.Fatalf("inspect %s: %v", at, err)
		}
		want := unitMode
		if at == p.record {
			want = recordMode
		}
		if got := info.Mode().Perm(); got != want {
			t.Errorf("%s is mode %04o; want %04o", at, got, want)
		}
	}
}

// TestPlaceRefusesWithoutAHomeToWriteIn is Standing's refusal, kept on the path
// that writes.
//
// **Must fail when** an unresolvable home is answered with anything but a
// refusal: filepath.Join("", …) is a relative path, so the branch this guards
// would create .config/systemd/user under whatever directory the daemon happened
// to be started in — a unit nothing loads, written somewhere nobody will look.
func TestPlaceRefusesWithoutAHomeToWriteIn(t *testing.T) {
	t.Parallel()

	release, _ := published(t)
	for _, home := range []string{"", "  ", "relative/home"} {
		u := NewUnit(func(string) string { return home })

		outcome, err := u.Place([]byte(publishedUnit), release.sums, release.signature)
		if !errors.Is(err, ErrNoUnitHome) {
			t.Errorf("Place() with HOME=%q = _, %v; want ErrNoUnitHome", home, err)
		}
		if outcome != UnitUnchanged {
			t.Errorf("Place() with HOME=%q = %v; nothing was written, and the outcome has to say so", home, outcome)
		}
		if got := u.NewPath(); got != "" {
			t.Errorf("NewPath() with HOME=%q = %q; there is no file to name", home, got)
		}
	}
}

// TestTheUnitIsVerifiedWithTheCommittedKey pins the shipping build's answer to
// the one seam in place.go.
//
// **Must fail when** the default is left pointing at something weaker than the
// verification every case above drives through verifyAgainst. A seam a test can
// replace is a seam production can be left holding the replacement of, and this
// one stands in front of a file systemd executes.
func TestTheUnitIsVerifiedWithTheCommittedKey(t *testing.T) {
	t.Parallel()

	for _, u := range []*Unit{NewUnit(func(string) string { return "/home/somebody" }), newUnit("/somewhere/crswd.service", "/somewhere/crswd.service.sha256")} {
		if u.verify == nil {
			t.Fatal("a Unit was built with no verification at all")
		}
		if reflect.ValueOf(u.verify).Pointer() != reflect.ValueOf(Verify).Pointer() {
			t.Fatal("a Unit was built verifying against something other than the keys this binary carries")
		}
	}
}

// TestUnitOutcomeSaysWhatHappened holds the sentences T004's page and T005's
// journal line are built from.
//
// **Must fail when** two outcomes read the same. "Your unit was replaced" and
// "your unit was left alone with a newer one beside it" are opposite instructions
// to an operator, and the whole milestone is that they can tell which they got.
func TestUnitOutcomeSaysWhatHappened(t *testing.T) {
	t.Parallel()

	said := map[string]UnitOutcome{}
	for _, o := range []UnitOutcome{UnitUnchanged, UnitReplaced, UnitInstalled, UnitOffered} {
		s := o.String()
		if s == "" {
			t.Errorf("outcome %d says nothing", o)
		}
		if first, repeated := said[s]; repeated {
			t.Errorf("outcomes %d and %d both say %q", first, o, s)
		}
		said[s] = o
	}
	if got := fmt.Sprint(UnitOutcome(99)); got != UnitUnchanged.String() {
		t.Errorf("an outcome this code does not recognise says %q; want the one that claims least, %q", got, UnitUnchanged.String())
	}
}

// TestPlaceWritesNothingOutsideTheTwoFilesItOwns is the blast radius, read off
// the filesystem rather than argued.
//
// **Must fail when** anything else appears under HOME. The staged temporary this
// writes through lands in the unit's own directory, and one left behind is a
// file in ~/.config/systemd/user that nobody can account for.
func TestPlaceWritesNothingOutsideTheTwoFilesItOwns(t *testing.T) {
	t.Parallel()

	const ours = "[Unit]\nDescription=crswd\n\n[Service]\nExecStart=%h/bin/crswd\n"
	p := newPlacing(t, []byte(ours), digestOf(ours))

	if _, err := p.place(); err != nil {
		t.Fatalf("Place() = _, %v; want a replacement", err)
	}

	want := map[string]bool{p.path: true, p.record: true}
	if err := filepath.WalkDir(p.home, func(at string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || want[at] {
			return nil
		}
		t.Errorf("an update left %s behind; the only files this step writes are the unit and its record", at)
		return nil
	}); err != nil {
		t.Fatalf("walk the fixture home: %v", err)
	}
}
