package updater

// stage.go is where a candidate lands, and it owns the one invariant the whole
// update path rests on: **nothing in the staging directory is executable until
// both the checksum and the signature have verified** (data-model.md §4).
//
// # Why the bytes are written before they are checked
//
// The order contracts/self-update.md fixes is write-at-0600, checksum,
// signature, chmod — the file exists on disk while it is being verified, and it
// exists there with no execute bit for anybody. That is deliberate: a mode is a
// property of the file, so the invariant is enforced by the filesystem for the
// whole window rather than by this function remembering not to run anything.
//
// Nothing *parses* the candidate before it verifies, though. The bytes go to
// disk and no further: the archive is not opened until verifyAgainst has said
// the bytes are the published ones, so a tampered tarball never reaches
// archive/tar at all.
//
// # Why the same path is written twice
//
// A release asset is a tarball and the thing that gets renamed over
// ~/.local/bin/crswd is the binary inside it, so between the signature check and
// the chmod the staged file is replaced by the member it carried. It is the same
// path throughout because there is exactly **one** staged candidate: a second
// name in this directory is a second thing to remove on failure, and the one
// that gets forgotten is the one left behind.
//
// # Why it is swept at startup
//
// A staged file present at boot was vouched for by a process that did not live
// to say so. It may have been verified; nothing running now witnessed that, and
// "probably fine" is not a property this directory is allowed to have — its
// contents become the daemon's own binary. Sweep is called from cmd/crswd's
// startup sequence before anything binds, and TestStagingSweptAtStartup asserts
// both halves: that it empties the directory, and that startup calls it.

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

const (
	// envHome is the only variable this file reads. XDG_DATA_HOME is
	// deliberately not consulted: install.sh writes its own bookkeeping to
	// $HOME/.local/share/crswd, the staging directory is its sibling, and one
	// arrangement that both halves agree on is worth more here than an
	// environment variable nobody in this project sets.
	envHome = "HOME"

	// stagingPath is data-model.md §4's location, relative to HOME.
	stagingPath = ".local/share/crswd/staging"

	// stagedPrefix names the candidate for the version it claims to be, so a
	// directory listing during a failed update says which release was refused.
	stagedPrefix = "crswd."

	// binaryMember is the one member of a release tarball that is unpacked, by
	// whole name. install.sh extracts the same name the same way: an archive
	// carrying extra paths then cannot write anywhere this did not intend, and
	// one carrying `../` cannot be read as naming a file at all.
	binaryMember = "crswd"

	// stagedMode is the mode a candidate has for its whole unverified life.
	// Not executable by anybody, this account included.
	stagedMode fs.FileMode = 0o600

	// verifiedMode is the first moment the candidate is executable at all, and
	// it is owner-only: the smoke test in swap.go execs it, and nothing else on
	// this host has any business doing so.
	verifiedMode fs.FileMode = 0o700

	// stagingDirMode keeps the directory itself owner-only. Another account able
	// to write here could replace a candidate between the signature check and
	// the rename, which is every check in this package spent for nothing.
	stagingDirMode fs.FileMode = 0o700
)

var (
	// ErrNoStagingDir is a process with no absolute HOME. It is a refusal
	// rather than a fallback to some other directory: an update has to write
	// the candidate somewhere only this account can reach, and there is no
	// second guess about where that is. A container configured entirely by
	// environment variables cannot self-update, which is correct — its binary
	// comes from its image.
	ErrNoStagingDir = errors.New("this daemon has no home directory to stage a release in")

	// ErrUnreadableRelease is an asset that verified and then turned out not to
	// be a gzipped tar. Reachable only for a release this project published and
	// signed, so it means the release is broken rather than that somebody is
	// attacking; it is still a refusal, because the alternative is renaming
	// something that is not a binary over the one that is running.
	ErrUnreadableRelease = errors.New("the release asset is not a gzipped tar archive")

	// ErrNoReleaseBinary is an archive that does not carry exactly one regular
	// file named crswd. Two of them is an ambiguity to refuse rather than
	// resolve by order, the same way fetch.go refuses two assets under one name.
	ErrNoReleaseBinary = errors.New("the release archive does not carry exactly one crswd binary")

	// ErrStagedFileIsExecutable is the invariant checking itself, and it should
	// be unreachable. See the guard in Stage for why it is worth the stat.
	ErrStagedFileIsExecutable = errors.New("a staged release was executable before it was verified")
)

// Stager owns the staging directory. It holds no state about an update in
// progress; one is safe to keep for the life of the daemon.
type Stager struct {
	// dir is the staging directory, or "" when this process has no HOME to put
	// one under. Empty means Sweep has nothing to do and Stage refuses.
	dir string

	// verify is step 2 and step 3, named as a field for the same reason
	// newFetcher takes a transport: a test needs to drive this exact sequence
	// against a release it signed with a pair it generated in process, and
	// nothing outside the operator's terminal has the half that signs the real
	// one.
	//
	// **The shipping build's value is updater.Verify**, against the key list
	// compiled into this binary, and TestStagerVerifiesWithTheCommittedKey
	// pins that — a seam that could be left pointing at something weaker is a
	// seam worth a test of its own.
	verify func(name string, asset, sums, signature []byte) error
}

// NewStager returns the Stager the daemon runs: the documented directory, and
// verification against the keys this binary was built carrying.
func NewStager(getenv func(string) string) *Stager {
	return &Stager{dir: StagingDir(getenv), verify: Verify}
}

// newStager is NewStager with the directory supplied, for tests that must not
// write to the operator's own.
func newStager(dir string) *Stager {
	return &Stager{dir: dir, verify: Verify}
}

// StagingDir is ~/.local/share/crswd/staging.
//
// It returns "" when the process has no absolute HOME, which means "there is
// nowhere to stage" rather than being an error — the same answer, and for the
// same reason, that config.DefaultPath gives when it cannot name a file. A
// relative HOME is ignored rather than joined to whatever directory the daemon
// happened to be started in: where a candidate binary is written may not depend
// on somebody's working directory at the moment they ran systemctl.
func StagingDir(getenv func(string) string) string {
	home := strings.TrimSpace(getenv(envHome))
	if !filepath.IsAbs(home) {
		return ""
	}
	return filepath.Join(home, stagingPath)
}

// Dir is where this Stager stages, or "" if it cannot.
func (s *Stager) Dir() string { return s.dir }

// Stage writes asset into the staging directory, verifies it there, unpacks the
// binary it carries, and only then makes that executable. It returns the path
// of a staged binary that swap.go may smoke-test.
//
// version names the release and reaches a filename; name is the exact asset
// name to look up in sums. Any failure removes the candidate and leaves this
// daemon running exactly what it was running (FR-028).
func (s *Stager) Stage(version, name string, asset, sums, signature []byte) (staged string, err error) {
	if s.dir == "" {
		return "", ErrNoStagingDir
	}
	// The version is concatenated into a path, and it arrives as data — from a
	// form field in T019, or from the API's own tag_name, neither of which this
	// package chose. `../../.bashrc` is a filename this would otherwise write.
	// Checked against the same shape fetch.go pastes into a URL path, at the
	// boundary that builds the path, which is the rule docs/security.md §2
	// states.
	if !versionShape.MatchString(version) {
		return "", ErrMalformedVersion
	}

	if err := os.MkdirAll(s.dir, stagingDirMode); err != nil {
		return "", fmt.Errorf("create the staging directory: %w", err)
	}
	path := filepath.Join(s.dir, stagedPrefix+version)

	// Every return below this line removes the candidate, including the ones
	// that are nobody's fault. A refused update that leaves bytes behind is the
	// state the startup sweep exists to clean up after, and arriving there on
	// purpose would make it routine.
	defer func() {
		if err == nil {
			return
		}
		staged = ""
		if rmErr := os.Remove(path); rmErr != nil && !errors.Is(rmErr, fs.ErrNotExist) {
			// Joined rather than swallowed: what is left behind is a file in a
			// directory whose contents become this daemon's binary, and the
			// operator has to be told it is there.
			err = errors.Join(err, fmt.Errorf("remove the refused candidate %s: %w", path, rmErr))
		}
	}()

	if err = writeStaged(path, asset); err != nil {
		return "", err
	}

	// The invariant, asserted rather than assumed, one line before the check it
	// is the whole point of. It is not defence against the host — this process
	// wrote that file a statement ago. It is defence against the diff that moves
	// the chmod up: with this here, that reordering stops a *correct* release
	// from staging instead of silently spending the property signing exists to
	// buy. TestStagedFileIsNotExecutableBeforeVerification watches the same
	// moment from outside.
	if err = refuseIfExecutable(path); err != nil {
		return "", err
	}

	if err = s.verify(name, asset, sums, signature); err != nil {
		return "", err
	}

	// Only now is the archive opened at all. Everything above treated it as
	// bytes; this is the first code to believe anything about its structure.
	var binary []byte
	if binary, err = releaseBinary(asset); err != nil {
		return "", err
	}
	if err = writeStaged(path, binary); err != nil {
		return "", err
	}

	// Step 4. The first moment anything here is executable.
	//nolint:gosec // G302: 0700 is the point — swap.go has to exec this — and it is owner-only, which is as tight as an executable file gets.
	if err = os.Chmod(path, verifiedMode); err != nil {
		return "", fmt.Errorf("make the staged release executable: %w", err)
	}
	return path, nil
}

// Sweep empties the staging directory (FR-028's other half).
//
// A directory that is not there is not an error: it is a daemon that has never
// staged anything, which is every daemon until the first update.
func (s *Stager) Sweep() error {
	if s.dir == "" {
		return nil
	}
	entries, err := os.ReadDir(s.dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read the staging directory %s: %w", s.dir, err)
	}

	// Every entry is attempted before any failure is reported. Stopping at the
	// first would leave the rest of a directory nobody has vouched for in place,
	// and the point of the sweep is that none of it survives.
	var failed []error
	for _, entry := range entries {
		if rmErr := os.RemoveAll(filepath.Join(s.dir, entry.Name())); rmErr != nil {
			failed = append(failed, rmErr)
		}
	}
	if len(failed) > 0 {
		return fmt.Errorf("sweep the staging directory %s: %w", s.dir, errors.Join(failed...))
	}
	return nil
}

// writeStaged replaces path with data at 0600.
//
// The Chmod is not redundant with the mode passed to OpenFile. That mode applies
// only when the call *creates* the file, and this writes an existing one every
// time a candidate is unpacked over itself — so without it, a file that arrived
// at some other mode would keep it while holding bytes this daemon is about to
// treat as its own binary.
func writeStaged(path string, data []byte) error {
	//nolint:gosec // G304: path is <staging dir>/crswd.<version>, and version was matched against versionShape before this was built.
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, stagedMode)
	if err != nil {
		return fmt.Errorf("open the staged release %s: %w", path, err)
	}

	err = writeStagedTo(f, data)
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return fmt.Errorf("write the staged release %s: %w", path, err)
	}
	return nil
}

func writeStagedTo(f *os.File, data []byte) error {
	if err := f.Chmod(stagedMode); err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		return err
	}
	// Synced here rather than left to the page cache, because the next thing to
	// happen to these bytes is an exec and then a rename over the running
	// daemon's own binary. A host that loses power between the two must not come
	// back with a directory entry pointing at a partial one.
	return f.Sync()
}

// refuseIfExecutable is the invariant, read back off the filesystem.
func refuseIfExecutable(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("stat the staged release %s: %w", path, err)
	}
	if mode := info.Mode().Perm(); mode&0o111 != 0 {
		return fmt.Errorf("%s is mode %#o: %w", path, mode, ErrStagedFileIsExecutable)
	}
	return nil
}

// releaseBinary reads the one member named crswd out of a release tarball.
//
// The member's own mode is read and discarded on purpose. An archive states the
// permissions it wants and this one is about to become the daemon's binary:
// honouring that field is how a setuid bit gets set by whoever built the
// archive, and the mode here is decided by this file and nothing else.
func releaseBinary(archive []byte) ([]byte, error) {
	zr, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrUnreadableRelease, err)
	}
	defer zr.Close() //nolint:errcheck // The archive is fully read or abandoned; a close failure says nothing about the member already extracted.

	var binary []byte
	found := false
	tr := tar.NewReader(zr)
	for {
		header, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("%w: %w", ErrUnreadableRelease, err)
		}
		// Whole-name matching. The name is never joined to a path, so a member
		// called `../crswd` is simply not the member being looked for.
		if header.Name != binaryMember {
			continue
		}
		if header.Typeflag != tar.TypeReg {
			return nil, fmt.Errorf("%s is not a regular file: %w", binaryMember, ErrNoReleaseBinary)
		}
		if found {
			return nil, fmt.Errorf("%s appears twice: %w", binaryMember, ErrNoReleaseBinary)
		}

		// Bounded like every other read in this package: one byte past the limit,
		// so "exactly the limit" and "too large" stay distinguishable rather than
		// becoming a silent truncation that fails to exec for a reason nobody can
		// see. These bytes have verified by now, so this is a broken release
		// rather than a decompression bomb — the refusal is the same either way.
		body, err := io.ReadAll(io.LimitReader(tr, maxAssetBytes+1))
		if err != nil {
			return nil, fmt.Errorf("%w: %w", ErrUnreadableRelease, err)
		}
		if int64(len(body)) > maxAssetBytes {
			return nil, fmt.Errorf("%s is larger than the %d byte limit: %w", binaryMember, maxAssetBytes, ErrUnreadableRelease)
		}
		binary, found = body, true
	}
	if !found {
		return nil, fmt.Errorf("%s: %w", binaryMember, ErrNoReleaseBinary)
	}
	return binary, nil
}
