package updater

// place.go acts on the standing unit.go computes, and it is the only file in
// this package that writes into ~/.config/systemd/user.
//
// # The rule it keeps
//
// A unit this daemon did not write is never overwritten. install.sh has said so
// since it shipped and it is right: the operator this milestone is for relaxed
// NoNewPrivileges, RestrictSUIDSGID and ProtectSystem in theirs so that `sudo`
// works inside a session, and an update that replaced units would undo that on
// every release and make them rediscover it every time.
//
// What is new here is the other half of it. Refusing to touch their file was
// never the defect; refusing *silently* was. A host two fixes behind looked
// exactly like a current one, because nothing on it could say which it was. So
// the refusal now leaves something behind: the unit this release ships, written
// beside theirs as crswd.service.new — which is what a package manager has done
// with .pacnew for twenty years, and for this reason. The operator decides, with
// both files in front of them.
//
// # The two decisions unit.go left open
//
// **No unit at all → write one, and record it.** That is install.sh's own first
// row: there is nothing to protect and nothing to take away, and it is the one
// case where placing a unit costs the operator nothing. What is placed is inert
// until somebody runs `systemctl --user daemon-reload` and enables it, and this
// daemon does neither — enabling a unit is a decision about the machine it runs
// on, and this is as much a stranger there as the installer is.
//
// **A unit that already *is* the published bytes → nothing at all, not even a
// record.** A host whose hand-written unit happens to match the release is
// current, so there is nothing to carry; recording its digest would be this
// daemon claiming a file it did not write, and the claim is what would license
// the *next* release to replace it. install.sh refuses the same thing in the
// same words — "recording somebody else's unit would make the next run read it
// as ours and replace it, which is this refusal undoing itself one command
// later". The price is that such a host is offered a .new at the release after
// this one, and that price is right: by then it really has fallen behind, and
// saying so is the whole point of this milestone.
//
// # Why an offer is withdrawn
//
// crswd.service.new is this daemon's file and it is a claim — "there is a newer
// unit than the one you are running". Every branch that makes the claim untrue
// removes it. The branch that matters is the replacement: an offer left beside a
// unit this update just brought forward names an *older* file than the one now
// installed, which is worse than the silence this milestone set out to fix.

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/nctiggy/claude-remote-session-webhook/internal/config"
)

const (
	// newSuffix names the unit a release ships when the one on this host is not
	// this daemon's to replace. Deliberately the convention an operator has met
	// before: a file beside the real one, differing from it by a suffix, is
	// something `diff` takes two arguments of and nothing systemd will ever load
	// — a unit file has to end in .service to be a unit at all.
	newSuffix = ".new"

	// unitMode is the mode install.sh gives a unit it places (`place 0644`), and
	// what this uses for a unit it writes where there was none.
	unitMode fs.FileMode = 0o644

	// recordMode is what install.sh's redirect produces under the ordinary
	// umask. The record holds the digest of a world-readable file and is a secret
	// to nobody; what matters is that a host cannot tell whether the installer or
	// an update wrote it, because both write it and neither owns it.
	recordMode fs.FileMode = 0o600

	// unitDirMode keeps a directory this creates owner-only, which is
	// stagingDirMode's reason: the user manager that reads it runs as this
	// account and nothing else on the host has business in either location.
	unitDirMode fs.FileMode = 0o700
)

// UnitOutcome is what an update did about the unit on this host — the action
// behind UnitStanding's answer.
type UnitOutcome int

const (
	// UnitUnchanged is the zero value, and it means nothing on this host was
	// written: the unit is already the one this release ships, or the question
	// could not be asked at all.
	//
	// The zero value for UnitStanding's reason. An outcome nobody computed reads
	// as "no file changed", so a dropped branch understates what happened rather
	// than reporting a replacement that never occurred.
	UnitUnchanged UnitOutcome = iota

	// UnitReplaced is a unit this project wrote and had not been touched since,
	// brought forward and recorded again.
	UnitReplaced

	// UnitInstalled is a host that had no unit and now has the published one.
	UnitInstalled

	// UnitOffered is the operator's own unit left exactly as it was, with the
	// one this release ships beside it as crswd.service.new.
	UnitOffered
)

// String is what the journal and the settings page say about an outcome, in the
// operator's terms rather than this package's.
func (o UnitOutcome) String() string {
	switch o {
	case UnitReplaced:
		return "replaced with the one this release ships"
	case UnitInstalled:
		return "installed, on a host that had none"
	case UnitOffered:
		return "left alone, with the one this release ships alongside it"
	case UnitUnchanged:
		return "already the one this release ships"
	default:
		// Unreachable through the constants above. Answered as the outcome that
		// claims least, for the reason every should-not-happen branch in this
		// package is answered the safe way round.
		return "already the one this release ships"
	}
}

// NewPath is where the unit a release ships is put when the one on this host is
// not this daemon's to replace, or "" when there is no home to name it under.
//
// It is exported because the operator has to be told the name: a file they
// cannot find is a difference they cannot diff, and a difference nobody can see
// is a decision nobody can take.
func (u *Unit) NewPath() string {
	if u.path == "" {
		return ""
	}
	return u.path + newSuffix
}

// Place decides what this update does about the systemd unit on this host, does
// it, and reports which of the four it was.
//
// asset is the release's own crswd.service, with the checksum list and the
// signature over it. They are verified here rather than by the caller, and that
// is not ceremony: what these bytes become is a file in ~/.config/systemd/user,
// where systemd executes whatever it says under whatever hardening it asks for.
// A caller able to hand this unverified bytes would be a caller able to choose
// what this host runs — so there is no way to call it that skips the check.
//
// An error means the unit was not carried, and says why. It never refuses an
// update: by the time this runs the binary is already installed, and a host
// whose unit could not be brought forward is a host still running the unit it
// was running a moment ago.
func (u *Unit) Place(asset, sums, signature []byte) (UnitOutcome, error) {
	if u.path == "" || u.record == "" {
		return UnitUnchanged, ErrNoUnitHome
	}
	if err := u.verify(UnitAsset, asset, sums, signature); err != nil {
		return UnitUnchanged, fmt.Errorf("verify the %s this release publishes: %w", UnitAsset, err)
	}

	standing, err := u.Standing(asset)
	if err != nil {
		return UnitUnchanged, err
	}

	switch standing {
	case UnitCurrent:
		// Nothing written, and deliberately not a record either — see the header.
		return UnitUnchanged, u.withdrawOffer()

	case UnitOurs:
		// The operator's mode, not this package's. A chmod does not change a
		// digest, so a unit still recorded as this project's may carry a
		// permission decision somebody made afterwards, and widening it back to
		// 0644 would be the same silent revert everything here exists to prevent.
		info, err := os.Stat(u.path) //nolint:gosec // G304: the path is HOME joined with a constant this package declares, not anything a request named.
		if err != nil {
			return UnitUnchanged, fmt.Errorf("inspect the unit this update replaces %s: %w", u.path, err)
		}
		if err := writeUnit(u.path, asset, info.Mode().Perm()); err != nil {
			return UnitUnchanged, fmt.Errorf("replace the unit this daemon wrote: %w", err)
		}
		return UnitReplaced, u.settle(asset)

	case UnitAbsent:
		if err := writeUnit(u.path, asset, unitMode); err != nil {
			return UnitUnchanged, fmt.Errorf("install the unit this release publishes: %w", err)
		}
		return UnitInstalled, u.settle(asset)

	case UnitTheirs:
		return u.offer(asset)

	default:
		// Unreachable through the constants above, and answered as UnitTheirs for
		// UnitStanding.String()'s reason: a standing this code does not recognise
		// must mean "that file is not yours to touch".
		return u.offer(asset)
	}
}

// offer puts the unit this release ships beside the operator's own and leaves
// theirs exactly where it is.
//
// Nothing is recorded on this path. A record is what says a unit is this
// project's to replace, and the file that was written is not the one at the unit
// path — recording here would hand the next update permission to overwrite the
// operator's file, which is this refusal undoing itself one release later.
func (u *Unit) offer(asset []byte) (UnitOutcome, error) {
	if err := writeUnit(u.NewPath(), asset, unitMode); err != nil {
		return UnitUnchanged, fmt.Errorf("put the unit this release ships beside the operator's own: %w", err)
	}
	return UnitOffered, nil
}

// settle is the bookkeeping behind a unit this daemon just wrote: record what it
// wrote, and withdraw an offer that is now untrue.
//
// Both are attempted whichever fails. They are independent facts about the host
// and stopping at the first would leave the other in whatever state an earlier
// release left it — which for the offer means a crswd.service.new claiming this
// host is behind, sitting beside the unit that just brought it up to date.
func (u *Unit) settle(written []byte) error {
	return errors.Join(u.keepRecord(written), u.withdrawOffer())
}

// keepRecord writes the digest of the unit this daemon just wrote, into the file
// install.sh keeps it in and in the format install.sh writes.
//
// After the unit and never before. A record of bytes that then failed to land
// would make the next update read whatever is at that path — including an
// operator's own file — as this project's, and replace it.
func (u *Unit) keepRecord(written []byte) error {
	if err := os.MkdirAll(filepath.Dir(u.record), unitDirMode); err != nil {
		return fmt.Errorf("create the directory the unit record lives in: %w", err)
	}
	// The digest alone and a newline, with no filename: recorded() reads it back
	// that way and install.sh writes it that way, so that the record is not tied
	// to the directory the hash was taken in.
	if err := config.WriteFile(u.record, []byte(unitDigest(written)+"\n"), recordMode); err != nil {
		return fmt.Errorf("record the unit this update wrote: %w", err)
	}
	return nil
}

// withdrawOffer removes crswd.service.new, on every path where it would no
// longer be true.
//
// A file that is not there is not a failure: it is every host that has never
// been offered a unit, which is most of them.
func (u *Unit) withdrawOffer() error {
	if err := os.Remove(u.NewPath()); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("remove the superseded %s: %w", u.NewPath(), err)
	}
	return nil
}

// writeUnit replaces path with data at mode, creating the directory install.sh
// would have created.
//
// Through config.WriteFile rather than a fourth implementation of write-into-a-
// temporary-file-and-rename. This repository keeps finding two copies of one
// write that have drifted — it is the whole of T007 — and the daemon's atomic
// writer is the one that is already tested. What atomicity buys here is a
// systemd that never reads half a unit, and an operator who never diffs against
// half of one.
func writeUnit(path string, data []byte, mode fs.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), unitDirMode); err != nil {
		return fmt.Errorf("create the directory the unit lives in: %w", err)
	}
	if err := config.WriteFile(path, data, mode); err != nil {
		return fmt.Errorf("write the unit %s: %w", path, err)
	}
	return nil
}
