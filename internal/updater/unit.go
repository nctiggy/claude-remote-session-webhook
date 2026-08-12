package updater

// unit.go is the other file an update has to have an answer about, and the one
// where the answer is not "bring it forward".
//
// # Why the unit is not treated like the configuration
//
// config.go migrates the operator's configuration in place, because every value
// in it is theirs and the schema around those values is this daemon's. A unit is
// not that. It is what systemd executes, with which environment, and under which
// hardening — and an operator who edited one edited it to make something work
// that did not work before. The operator this milestone is for relaxed
// NoNewPrivileges, RestrictSUIDSGID and ProtectSystem so that `sudo` works
// inside a session. An update that replaced units would undo that on every
// release, and they would rediscover it every time.
//
// **So an edited unit is never overwritten.** That rule already exists in
// install.sh and it is right. What is missing is everything after it: this
// daemon has never been able to say whether the unit on this host is the one the
// release ships, so a host that is two fixes behind looks exactly like one that
// is current. That silence is the defect, and answering it starts here.
//
// # Why the record, and not the published bytes, decides ownership
//
// The only evidence that distinguishes a unit this project wrote from one the
// operator authored is the digest install.sh recorded beside it when it wrote
// one. Comparing against the bytes the release ships cannot do it: an operator's
// unit and an older published unit differ in exactly the same way as an
// operator's unit and a newer published unit, so a comparison against the
// release would refuse to correct any host that had ever taken a unit — which is
// every host, and the reason install.sh reads the record instead.
//
// A record that is not there falls on the "not ours" side, and that is not an
// edge case: every host deployed before the installer existed has a hand-written
// unit and no record of one, the host that publishes these releases included.
// Absence of evidence that this project wrote a file is not permission to
// replace it.
//
// # What this file does not do
//
// It writes nothing and fetches nothing. Deciding what to do about a standing —
// replacing a unit this project wrote, or putting the new one alongside as
// crswd.service.new — is T003, and it is separate for the reason every step in
// this package is separate: a step that shares a file with the next one is a
// step somebody removes with an early return.

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

const (
	// UnitAsset is the release asset carrying the systemd unit — the same asset
	// install.sh downloads, checks against SHA256SUMS and places, and the fourth
	// spelling of a name four languages have to agree on.
	//
	// **Reused rather than delivered a second time.** A release already publishes
	// this file, already sums it in SHA256SUMS, and already signs that list; an
	// update asks for it by exactly this name through the same Fetcher and puts
	// it through the same Verify as the tarball. A second channel for the unit
	// would be a second thing to verify, and the one that gets verified less.
	//
	// The other spellings are .github/workflows/release.yml, install.sh's
	// UNIT_ASSET, and deploy/crswd.example.service's own filename.
	// TestUnitAssetIsWhatTheInstallerFetches holds this one against install.sh.
	UnitAsset = "crswd.service"

	// unitPath is where install.sh writes that asset, relative to HOME.
	//
	// Composed from HOME rather than from XDG_CONFIG_HOME, which systemd itself
	// would honour, because install.sh composes it from HOME: one arrangement
	// both halves agree on is worth more than an environment variable nobody in
	// this project sets, and a daemon looking somewhere the installer never
	// writes would report every host as having no unit at all.
	// TestUnitPathsAreWhereTheInstallerWrites holds the two together.
	unitPath = ".config/systemd/user/crswd.service"

	// unitRecordPath is the digest install.sh recorded of the unit it wrote,
	// relative to HOME. It is the whole of what says a unit is this project's to
	// replace, which is why it is read from the installer's own location rather
	// than from somewhere this package would prefer.
	unitRecordPath = ".local/share/crswd/crswd.service.sha256"
)

// ErrNoUnitHome is a process with no absolute HOME, and so nowhere to look for
// either file.
//
// A refusal rather than a guess, for ErrNoStagingDir's reason: there is no
// second place a unit might be, and an update that answered "absent" here would
// be reporting a fact about this daemon's environment as a fact about the
// operator's host.
var ErrNoUnitHome = errors.New("this daemon has no home directory to find a systemd unit under")

// UnitStanding is what an update finds at ~/.config/systemd/user/crswd.service,
// as one of four answers to "is the operator's unit the one this release ships?"
type UnitStanding int

const (
	// UnitTheirs is a unit this project did not write: no record of one, or a
	// record that does not describe the file that is there. It is never
	// overwritten.
	//
	// **It is the zero value on purpose.** A standing that was never computed —
	// a struct field nobody filled in, a branch that forgot to ask — must read as
	// "leave that file alone", because the direction to be wrong in is the one
	// where an operator keeps the edit they made for a reason this daemon cannot
	// see.
	UnitTheirs UnitStanding = iota

	// UnitAbsent is a host with no unit at that path at all. It is not the same
	// as UnitTheirs and must not be folded into it: nothing is being protected
	// from anything, and what to do about it is a decision, not a refusal.
	UnitAbsent

	// UnitCurrent is a unit byte-for-byte identical to the one the release
	// publishes. There is nothing to carry forward, whoever wrote it.
	UnitCurrent

	// UnitOurs is a unit that differs from the published one and hashes to
	// exactly what install.sh recorded when it wrote it — untouched since, and
	// therefore this project's to replace.
	UnitOurs
)

// String is what the journal and the settings page say about a standing, in the
// operator's terms rather than this package's.
func (s UnitStanding) String() string {
	switch s {
	case UnitAbsent:
		return "absent"
	case UnitCurrent:
		return "the one this release ships"
	case UnitOurs:
		return "the one this daemon installed, and out of date"
	case UnitTheirs:
		return "not one this daemon wrote"
	default:
		// Unreachable through the constants above, and an answer rather than a
		// panic for the reason every should-not-happen branch on this project is
		// one: it reads as "do not touch that file", which is the safe direction.
		return "not one this daemon wrote"
	}
}

// Unit is the operator's systemd unit and the record of the one this project
// wrote. It holds no state about an update in progress; one is safe to keep for
// the life of the daemon.
type Unit struct {
	// path is ~/.config/systemd/user/crswd.service, or "" when this process has
	// no absolute HOME. Empty means Standing refuses rather than reporting a
	// host's unit as absent.
	path string

	// record is ~/.local/share/crswd/crswd.service.sha256 — install.sh's own
	// bookkeeping, read here and (from T003) written here, so that the installer
	// and the updater keep answering the ownership question the same way.
	record string
}

// NewUnit returns the Unit the daemon runs, for the paths install.sh writes.
func NewUnit(getenv func(string) string) *Unit {
	return newUnit(underHome(getenv, unitPath), underHome(getenv, unitRecordPath))
}

// newUnit is NewUnit with both paths supplied, for tests that must not read or
// write the unit of the daemon running the suite.
func newUnit(path, record string) *Unit { return &Unit{path: path, record: record} }

// underHome joins one of the installer's relative paths onto this process's
// home directory, or returns "" when there is no absolute one.
//
// A relative HOME is ignored rather than joined to whatever directory the daemon
// happened to be started in, for StagingDir's reason: which unit an update reads
// may not depend on somebody's working directory at the moment they ran
// systemctl.
func underHome(getenv func(string) string, rel string) string {
	home := strings.TrimSpace(getenv(envHome))
	if !filepath.IsAbs(home) {
		return ""
	}
	return filepath.Join(home, rel)
}

// Path is the unit this daemon compares against, or "" if it cannot name one.
func (u *Unit) Path() string { return u.path }

// RecordPath is where the digest of the unit this project wrote is kept, or ""
// if it cannot name one.
func (u *Unit) RecordPath() string { return u.record }

// Standing compares the unit on this host against the one a release publishes.
//
// published is the verified bytes of the UnitAsset of the release being
// installed — verified, because this is the value every branch below is decided
// against, and comparing an operator's unit with bytes nothing vouched for would
// be reading a stranger's answer to "are you up to date".
//
// An error means the question could not be asked: no home directory, or a file
// that is there and could not be read. It never means "leave it alone" — that is
// UnitTheirs, which is a fact about the host rather than a failure.
func (u *Unit) Standing(published []byte) (UnitStanding, error) {
	if u.path == "" || u.record == "" {
		return UnitTheirs, ErrNoUnitHome
	}

	current, err := os.ReadFile(u.path) //nolint:gosec // G304: the path is HOME joined with a constant this package declares, not anything a request named.
	if errors.Is(err, fs.ErrNotExist) {
		return UnitAbsent, nil
	}
	if err != nil {
		return UnitTheirs, fmt.Errorf("read the systemd unit %s: %w", u.path, err)
	}

	// Asked before the record, and that ordering is the difference between "you
	// are current" and "somebody else's file". A host whose hand-written unit
	// happens to be exactly what the release ships has nothing to be told and
	// nothing to be given alongside.
	if bytes.Equal(current, published) {
		return UnitCurrent, nil
	}

	recorded, err := u.recorded()
	if err != nil {
		return UnitTheirs, err
	}
	if recorded == "" || recorded != unitDigest(current) {
		return UnitTheirs, nil
	}
	return UnitOurs, nil
}

// recorded is the digest install.sh wrote for the unit it placed, or "" when
// there is no record to read.
//
// A record that is not there is not an error: it is the third row of
// install.sh's table — somebody else put that unit there — and it is the state
// every host deployed before the installer existed is in.
//
// A record that is there and is not a digest is answered the same way, and
// deliberately not as a failure. There is no information in an unreadable
// record, so the standing it produces is the one absence produces: not ours,
// leave it alone. The operator is not left in the dark either — a unit that is
// not this daemon's is exactly the case T004 and T005 report on.
func (u *Unit) recorded() (string, error) {
	raw, err := os.ReadFile(u.record) //nolint:gosec // G304: the path is HOME joined with a constant this package declares, not anything a request named.
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("read the record of the unit this daemon wrote %s: %w", u.record, err)
	}

	// install.sh writes the digest alone and a newline, with no filename, so
	// that the record is not tied to the directory the hash was taken in.
	sum := strings.ToLower(strings.TrimSpace(string(raw)))
	if len(sum) != hex.EncodedLen(sha256.Size) {
		return "", nil
	}
	if _, err := hex.DecodeString(sum); err != nil {
		return "", nil
	}
	return sum, nil
}

// unitDigest is the hash install.sh takes of a unit, spelled the way it spells
// it: sha256, lowercase hex. The record is compared as text, so the two have to
// agree on the encoding as well as on the algorithm.
func unitDigest(contents []byte) string {
	sum := sha256.Sum256(contents)
	return hex.EncodeToString(sum[:])
}
