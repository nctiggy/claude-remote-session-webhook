package updater

// What stage.go has to be true of, checked by staging real releases into a real
// directory under t.TempDir().
//
// The invariant is a mode, and a mode is only interesting *while* something is
// being decided about the file that carries it. So the three tests here are not
// all post-conditions:
//
//   - **TestStagedFileIsNotExecutableBeforeVerification looks at the file at the
//     instant verification runs**, from inside the verify seam. A post-hoc check
//     cannot catch the mutation the contract names — move the chmod above the
//     signature check and every after-the-fact assertion still passes, because
//     the failure path removes the file it just made executable.
//   - **TestStagedBinaryIsTheReleaseBinary** exists because the staged path is
//     the tarball for most of its life. A stage that never unpacks leaves
//     something that verifies, has the right name and the right mode, and is a
//     gzip stream where the daemon's binary is meant to be.
//   - **TestStagingSweptAtStartup asserts the caller as well as the callee.**
//     A sweep nothing calls is this repository's oldest failure and the plan
//     names five instances of it; the structural half reads cmd/crswd/main.go.
//
// Every table carries a row for the release exactly as published, wanting no
// error, for the reason verify_test.go gives: without it a fixture broken for
// some unrelated reason makes every refusal case pass while proving nothing.

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/ed25519"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// mainSource is cmd/crswd's startup sequence, read by the structural half of
// TestStagingSweptAtStartup. It lives here rather than in that package's own
// test because the invariant is this directory's: the file that must be swept
// and the code that sweeps it are both in this package, and only the call is
// over there.
const mainSource = "../../cmd/crswd/main.go"

// binaryBytes is what a release tarball carries. Not an ELF header — nothing
// here execs it, that is swap.go's — but distinguishable from the archive
// around it, which is the whole of what these tests need to tell apart.
var binaryBytes = []byte("#!/bin/sh\necho crswd " + testVersion + "\n")

// tarball builds a release asset the way .github/workflows/release.yml does:
// `tar -C dist/<arch> -czf ... crswd`, one member, named by the whole name.
func tarball(t *testing.T, members map[string][]byte) []byte {
	t.Helper()
	return tarballOf(t, members, tar.TypeReg)
}

// tarballOf is tarball with the member type chosen, so a case can publish
// something that is not a regular file where the binary should be.
func tarballOf(t *testing.T, members map[string][]byte, typeflag byte) []byte {
	t.Helper()

	written := make([]tarMember, 0, len(members))
	for name, body := range members {
		if typeflag != tar.TypeReg {
			body = nil
		}
		written = append(written, tarMember{name: name, typeflag: typeflag, body: body})
	}
	return archiveOf(t, written...)
}

// tarMember is one entry of an archive a map cannot describe — the same name
// twice, most of all.
type tarMember struct {
	name     string
	typeflag byte
	body     []byte
}

// archiveOf writes the members in the order given.
func archiveOf(t *testing.T, members ...tarMember) []byte {
	t.Helper()

	var raw bytes.Buffer
	zw := gzip.NewWriter(&raw)
	tw := tar.NewWriter(zw)

	for _, member := range members {
		if err := tw.WriteHeader(&tar.Header{
			Name:     member.name,
			Typeflag: member.typeflag,
			Size:     int64(len(member.body)),
			// Deliberately wider than anything this daemon would choose, so a
			// stage that honoured the archive's own mode is visible in the mode
			// of the staged file rather than only in review.
			Mode: 0o777,
		}); err != nil {
			t.Fatalf("write the header for %s: %v", member.name, err)
		}
		if _, err := tw.Write(member.body); err != nil {
			t.Fatalf("write %s: %v", member.name, err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("close the tar writer: %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("close the gzip writer: %v", err)
	}
	return raw.Bytes()
}

// release is a published release and the directory it stages into.
type release struct {
	dir       string
	stager    *Stager
	name      string
	asset     []byte
	sums      []byte
	signature []byte
	keys      string
}

// staging returns a correct release, staged into a directory of this test's
// own, verified against a pair generated in process and never written down.
//
// The stager's verify seam is pointed at the same verifyAgainst the shipping
// build reaches through Verify, with this release's key list rather than the
// committed one — the half that signs the real key is not in this repository
// and must never be. TestStagerVerifiesWithTheCommittedKey is what holds the
// production wiring to the real thing.
func staging(t *testing.T) (*release, ed25519.PrivateKey) {
	t.Helper()

	name := AssetName(testVersion, "amd64")
	asset := tarball(t, map[string][]byte{binaryMember: binaryBytes})

	// Both architectures, because a release publishes both and the list has to
	// be one an asset is looked up *in* rather than one it is the whole of.
	sums := sumsOver(map[string][]byte{
		name:                            asset,
		AssetName(testVersion, "arm64"): tarball(t, map[string][]byte{binaryMember: []byte("another architecture")}),
	})
	public, private := keyPair(t)

	r := &release{
		dir:       filepath.Join(t.TempDir(), "staging"),
		name:      name,
		asset:     asset,
		sums:      sums,
		signature: ed25519.Sign(private, sums),
		keys:      keyList(public),
	}
	r.stager = newStager(r.dir)
	r.stager.verify = func(name string, asset, sums, signature []byte) error {
		return verifyAgainst(name, asset, sums, signature, r.keys)
	}
	return r, private
}

// stage runs the whole of stage.go over this release.
func (r *release) stage() (string, error) {
	return r.stager.Stage(testVersion, r.name, r.asset, r.sums, r.signature)
}

// resign replaces the checksum list and signs the replacement, so a case about
// the archive is not accidentally a case about the signature.
func (r *release) resign(signer ed25519.PrivateKey) {
	assets := map[string][]byte{r.name: r.asset}
	r.sums = sumsOver(assets)
	r.signature = ed25519.Sign(signer, r.sums)
}

// entries is what the staging directory holds, by name.
func entries(t *testing.T, dir string) []os.DirEntry {
	t.Helper()

	found, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		t.Fatalf("read %s: %v", dir, err)
	}
	return found
}

// requireNothingExecutable is the assertion FR-028 turns into a property of the
// directory rather than of one file: whatever a failed update left, none of it
// may be runnable.
func requireNothingExecutable(t *testing.T, dir string) {
	t.Helper()

	for _, entry := range entries(t, dir) {
		info, err := entry.Info()
		if err != nil {
			t.Fatalf("stat %s: %v", entry.Name(), err)
		}
		if mode := info.Mode().Perm(); mode&0o111 != 0 {
			t.Fatalf("a failed update left %s at mode %#o", entry.Name(), mode)
		}
	}
}

// mode is the permission bits of one path.
func mode(t *testing.T, path string) fs.FileMode {
	t.Helper()

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	return info.Mode().Perm()
}

// TestStagedFileIsNotExecutableBeforeVerification is data-model.md §4's
// invariant, watched at the one moment it can be observed.
//
// **Must fail when** anything is chmod'd executable before the signature
// verifies. The observation is made from inside the verify seam because that is
// the only place the window is open: the mutation this catches — hoisting the
// chmod above the check — leaves no trace afterwards, since the refusal path
// removes the file.
func TestStagedFileIsNotExecutableBeforeVerification(t *testing.T) {
	t.Parallel()

	t.Run("a candidate arriving in an empty directory", func(t *testing.T) {
		t.Parallel()

		r, _ := staging(t)
		path, seen := stageWatchingTheMode(t, r)

		if seen != stagedMode {
			t.Fatalf("the candidate was mode %#o while it was being verified, want %#o", seen, stagedMode)
		}
		if got := mode(t, path); got != verifiedMode {
			t.Fatalf("a verified candidate is mode %#o, want %#o", got, verifiedMode)
		}
	})

	t.Run("a candidate written over one an earlier run left executable", func(t *testing.T) {
		t.Parallel()

		// The path is not always fresh. A verified candidate whose swap did not
		// happen — the smoke test refused it, or the process died between the
		// two — is a 0700 file at exactly this name, and staging that version
		// again writes through it. The mode O_CREATE carries says nothing about
		// a file that already exists, so without an explicit chmod the second
		// candidate inherits the first one's execute bit and spends the whole
		// invariant on a path nobody looks at twice.
		r, _ := staging(t)
		first, err := r.stage()
		if err != nil {
			t.Fatalf("stage a release exactly as published: %v", err)
		}
		if got := mode(t, first); got != verifiedMode {
			t.Fatalf("the first candidate is mode %#o, want %#o", got, verifiedMode)
		}

		_, seen := stageWatchingTheMode(t, r)
		if seen != stagedMode {
			t.Fatalf("a candidate written over an executable one was mode %#o while it was being verified, want %#o",
				seen, stagedMode)
		}
	})
}

// stageWatchingTheMode stages a release and reports the mode the candidate had
// at the instant verification ran.
//
// The observation is made from inside the verify seam and not afterwards,
// because afterwards is too late: hoisting the chmod above the signature check
// leaves no trace at all on the failure path, which removes the file it just
// made executable.
func stageWatchingTheMode(t *testing.T, r *release) (path string, whileVerifying fs.FileMode) {
	t.Helper()

	inner := r.stager.verify
	watched := false
	r.stager.verify = func(name string, asset, sums, signature []byte) error {
		whileVerifying, watched = mode(t, filepath.Join(r.dir, stagedPrefix+testVersion)), true
		return inner(name, asset, sums, signature)
	}
	defer func() { r.stager.verify = inner }()

	path, err := r.stage()
	if err != nil {
		t.Fatalf("stage a release exactly as published: %v", err)
	}
	if !watched {
		t.Fatal("verification never ran, so nothing about the order was observed")
	}
	return path, whileVerifying
}

// TestStagedBinaryIsTheReleaseBinary is the other half of the same run: what is
// left at the staged path is the member the archive carried, at a mode this
// package chose.
//
// **Must fail when** the tarball itself is left there — it verifies, it is named
// for the release, it is mode 0700, and it is a gzip stream where swap.go
// expects something it can exec. Or when the archive's own 0777 is honoured,
// which is how a mode somebody else wrote reaches a file this daemon renames
// over its own binary.
func TestStagedBinaryIsTheReleaseBinary(t *testing.T) {
	t.Parallel()

	r, _ := staging(t)

	path, err := r.stage()
	if err != nil {
		t.Fatalf("stage a release exactly as published: %v", err)
	}
	if want := filepath.Join(r.dir, stagedPrefix+testVersion); path != want {
		t.Fatalf("staged at %s, want %s", path, want)
	}

	got, err := os.ReadFile(path) //nolint:gosec // G304: the path is this test's own staging directory under t.TempDir().
	if err != nil {
		t.Fatalf("read the staged release: %v", err)
	}
	if !bytes.Equal(got, binaryBytes) {
		t.Fatalf("the staged file is %d bytes and the release's binary is %d; the archive was not unpacked",
			len(got), len(binaryBytes))
	}
	if got := mode(t, path); got != verifiedMode {
		t.Fatalf("the staged binary is mode %#o, want %#o", got, verifiedMode)
	}
	if got := mode(t, r.dir); got != stagingDirMode {
		t.Fatalf("the staging directory is mode %#o, want %#o", got, stagingDirMode)
	}
}

// TestFailedUpdateLeavesNothingExecutable is FR-028 as a property of the
// directory: after any refusal, this daemon is running what it was running and
// there is nothing here that could change that.
//
// **Must fail when** a partial download is left behind at 0700 — or left behind
// at all, which is the stronger thing asserted, because the startup sweep exists
// to clean up after the process that died rather than after the one that
// refused.
func TestFailedUpdateLeavesNothingExecutable(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		mutate func(t *testing.T, r *release, signer ed25519.PrivateKey)
		want   error
	}{
		{
			name: "a release exactly as published",
		},
		{
			name: "one byte of the tarball changed",
			mutate: func(_ *testing.T, r *release, _ ed25519.PrivateKey) {
				r.asset[len(r.asset)/2] ^= 0xff
			},
			want: ErrChecksumMismatch,
		},
		{
			name: "a tarball with a checksum list rebuilt to match it",
			mutate: func(t *testing.T, r *release, _ ed25519.PrivateKey) {
				r.asset = tarball(t, map[string][]byte{binaryMember: []byte("somebody else's binary")})
				_, stranger := keyPair(t)
				r.resign(stranger)
			},
			want: ErrSignatureUnverified,
		},
		{
			name: "a release carrying no signature",
			mutate: func(_ *testing.T, r *release, _ ed25519.PrivateKey) {
				r.signature = nil
			},
			want: ErrNotSigned,
		},
		{
			name: "a checksum list that does not cover the asset",
			mutate: func(_ *testing.T, r *release, signer ed25519.PrivateKey) {
				r.name = AssetName(testVersion, "riscv64")
				r.signature = ed25519.Sign(signer, r.sums)
			},
			want: ErrUnsummedAsset,
		},
		{
			name: "an asset that is not a gzip stream",
			mutate: func(_ *testing.T, r *release, signer ed25519.PrivateKey) {
				r.asset = []byte("this is not an archive")
				r.resign(signer)
			},
			want: ErrUnreadableRelease,
		},
		{
			name: "an archive carrying no crswd",
			mutate: func(t *testing.T, r *release, signer ed25519.PrivateKey) {
				r.asset = tarball(t, map[string][]byte{"crswd.service": []byte("[Unit]\n")})
				r.resign(signer)
			},
			want: ErrNoReleaseBinary,
		},
		{
			name: "an archive carrying two crswd members",
			mutate: func(t *testing.T, r *release, signer ed25519.PrivateKey) {
				// tar carries no index and permits the same name twice; the
				// convention is that the later one wins. "Which of these is the
				// daemon's next binary" is not a question to answer by
				// convention, so both are refused.
				member := tarMember{name: binaryMember, typeflag: tar.TypeReg, body: binaryBytes}
				r.asset = archiveOf(t, member, member)
				r.resign(signer)
			},
			want: ErrNoReleaseBinary,
		},
		{
			name: "an archive whose crswd is a directory",
			mutate: func(t *testing.T, r *release, signer ed25519.PrivateKey) {
				r.asset = tarballOf(t, map[string][]byte{binaryMember: nil}, tar.TypeDir)
				r.resign(signer)
			},
			want: ErrNoReleaseBinary,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			r, signer := staging(t)
			if c.mutate != nil {
				c.mutate(t, r, signer)
			}

			path, err := r.stage()
			switch {
			case c.want == nil && err != nil:
				t.Fatalf("staging refused a correct release: %v", err)
			case c.want == nil:
				if got := mode(t, path); got != verifiedMode {
					t.Fatalf("a verified candidate is mode %#o, want %#o", got, verifiedMode)
				}
				return
			case err == nil:
				t.Fatalf("staging accepted %s", c.name)
			case !errors.Is(err, c.want):
				t.Fatalf("staging %s refused with %v, want %v", c.name, err, c.want)
			}

			if path != "" {
				t.Fatalf("a refused update named a staged file at %s", path)
			}
			requireNothingExecutable(t, r.dir)
			if left := entries(t, r.dir); len(left) != 0 {
				t.Fatalf("a refused update left %d file(s) in the staging directory", len(left))
			}
		})
	}
}

// TestStagingSweptAtStartup is the sweep, and the fact that something calls it.
//
// **Must fail when** a staged file is trusted across a restart — the process
// that vouched for those bytes did not live to say so — or when the sweep exists
// and startup does not run it, which is the failure this repository has shipped
// five times and which no behavioural test of Sweep alone can see.
func TestStagingSweptAtStartup(t *testing.T) {
	t.Parallel()

	t.Run("a staged file present at boot is removed", func(t *testing.T) {
		t.Parallel()

		r, _ := staging(t)
		path, err := r.stage()
		if err != nil {
			t.Fatalf("stage a release exactly as published: %v", err)
		}

		// A directory as well as the verified binary: whatever a previous
		// process left, none of it may survive, and RemoveAll is what makes the
		// difference between the two invisible.
		if err := os.MkdirAll(filepath.Join(r.dir, "crswd.v0.41.d", "deeper"), 0o700); err != nil {
			t.Fatalf("leave a directory behind: %v", err)
		}

		if err := r.stager.Sweep(); err != nil {
			t.Fatalf("sweep the staging directory: %v", err)
		}
		if left := entries(t, r.dir); len(left) != 0 {
			t.Fatalf("the sweep left %d entr(ies) behind", len(left))
		}
		if _, err := os.Stat(path); !errors.Is(err, fs.ErrNotExist) {
			t.Fatalf("the swept candidate is still there: %v", err)
		}
		if _, err := os.Stat(r.dir); err != nil {
			t.Fatalf("the sweep removed the staging directory itself: %v", err)
		}
	})

	t.Run("a directory that was never created is not an error", func(t *testing.T) {
		t.Parallel()

		// Every daemon until its first update, including every one deployed
		// before this milestone. A sweep that refused here would be a startup
		// failure on hosts that have never staged anything.
		if err := newStager(filepath.Join(t.TempDir(), "never")).Sweep(); err != nil {
			t.Fatalf("sweep a staging directory that does not exist: %v", err)
		}
	})

	t.Run("a process with no home has nothing to sweep", func(t *testing.T) {
		t.Parallel()

		// A container configured entirely by environment variables. There is
		// nowhere to stage, so there is nothing to sweep — and startup must not
		// fail on a daemon that could never have staged anything.
		s := NewStager(func(string) string { return "" })
		if s.Dir() != "" {
			t.Fatalf("a process with no HOME named a staging directory at %s", s.Dir())
		}
		if err := s.Sweep(); err != nil {
			t.Fatalf("sweep with no home directory: %v", err)
		}
		if _, err := s.Stage(testVersion, "asset", nil, nil, nil); !errors.Is(err, ErrNoStagingDir) {
			t.Fatalf("staging with no home directory refused with %v, want %v", err, ErrNoStagingDir)
		}
	})

	t.Run("startup sweeps before it listens", func(t *testing.T) {
		t.Parallel()

		// Read as an AST rather than grepped. Iteration 17's lesson was that a
		// well-commented step names the call it makes three times — in the
		// comment, in the code, and in the message it prints when it fails — so
		// a string search is satisfied by prose about the call it is looking
		// for. The parser is given no comments to be fooled by.
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, mainSource, nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatalf("parse %s: %v", mainSource, err)
		}

		imported := false
		for _, spec := range file.Imports {
			if strings.HasSuffix(strings.Trim(spec.Path.Value, `"`), "/internal/updater") {
				imported = true
			}
		}
		if !imported {
			t.Fatalf("%s does not import this package, so nothing there can sweep the staging directory", mainSource)
		}

		startup := funcNamed(t, file, "run")
		sweep, listen := callPos(startup, "Sweep"), callPos(startup, "Listen")
		if sweep == token.NoPos {
			t.Fatalf("%s's run() calls no Sweep: a staged file would survive a restart", mainSource)
		}
		if listen == token.NoPos {
			t.Fatalf("%s's run() calls no Listen, so this test is asserting an order that no longer exists", mainSource)
		}
		if sweep > listen {
			t.Fatalf("%s's run() sweeps at line %d, after it listens at line %d",
				mainSource, fset.Position(sweep).Line, fset.Position(listen).Line)
		}
	})
}

// TestStagerVerifiesWithTheCommittedKey pins the shipping build's answer to the
// one seam in this file.
//
// **Must fail when** the default is left pointing at something weaker than the
// verification every test above drives through verifyAgainst — a seam a test can
// replace is a seam production can be left holding the replacement of.
func TestStagerVerifiesWithTheCommittedKey(t *testing.T) {
	t.Parallel()

	for _, s := range []*Stager{NewStager(func(string) string { return "/home/somebody" }), newStager(t.TempDir())} {
		if s.verify == nil {
			t.Fatal("a Stager was built with no verification at all")
		}
		if reflect.ValueOf(s.verify).Pointer() != reflect.ValueOf(Verify).Pointer() {
			t.Fatal("a Stager was built verifying against something other than the keys this binary carries")
		}
	}
}

// TestStagingDirIsTheDocumentedPath holds the location data-model.md §4 fixes,
// and the refusal to build one out of anything but an absolute HOME.
//
// **Must fail when** a relative HOME is joined to whatever directory the daemon
// was started in: which file becomes this daemon's binary may not depend on
// somebody's working directory at the moment they ran systemctl.
func TestStagingDirIsTheDocumentedPath(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		home string
		want string
	}{
		{name: "an ordinary home", home: "/home/operator", want: "/home/operator/.local/share/crswd/staging"},
		{name: "a trailing newline", home: "/home/operator\n", want: "/home/operator/.local/share/crswd/staging"},
		{name: "no home at all", home: "", want: ""},
		{name: "a relative home", home: "somewhere", want: ""},
		{name: "a home that is only whitespace", home: "   ", want: ""},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			if got := StagingDir(func(name string) string {
				if name != envHome {
					t.Fatalf("the staging directory was resolved from %s", name)
				}
				return c.home
			}); got != c.want {
				t.Fatalf("staging directory %q, want %q", got, c.want)
			}
		})
	}
}

// TestStagedNameCannotEscapeTheStagingDirectory is docs/security.md §2 at the
// boundary that builds the path.
//
// **Must fail when** the version is pasted into a filename unchecked. It reaches
// this package as data twice over — from the form field T019 reads, and from the
// tag_name the API hands back — and `../` in it is a write to a path this daemon
// then execs and renames over its own binary.
func TestStagedNameCannotEscapeTheStagingDirectory(t *testing.T) {
	t.Parallel()

	escapes := []string{
		"../../.bashrc",
		"..",
		"v0.42/../../.local/bin/crswd",
		"v0.42\x00",
		"/etc/crontab",
		"",
		".",
	}

	for _, version := range escapes {
		t.Run(fmt.Sprintf("%q", version), func(t *testing.T) {
			t.Parallel()

			r, _ := staging(t)
			if _, err := r.stager.Stage(version, r.name, r.asset, r.sums, r.signature); !errors.Is(err, ErrMalformedVersion) {
				t.Fatalf("staging version %q refused with %v, want %v", version, err, ErrMalformedVersion)
			}
			// Nothing was created on the way to refusing, the staging directory
			// included: a refusal that first makes a directory under a name the
			// caller chose is the same defect one level up.
			if left := entries(t, r.dir); len(left) != 0 {
				t.Fatalf("refusing version %q left %d entr(ies) behind", version, len(left))
			}
		})
	}
}

// funcNamed returns the named top-level function of a parsed file.
func funcNamed(t *testing.T, file *ast.File, name string) *ast.FuncDecl {
	t.Helper()

	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if ok && fn.Recv == nil && fn.Name.Name == name {
			return fn
		}
	}
	t.Fatalf("%s declares no func %s", mainSource, name)
	return nil
}

// callPos is where fn calls a method of the given name, or token.NoPos.
func callPos(fn *ast.FuncDecl, method string) token.Pos {
	found := token.NoPos
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		if sel, ok := call.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == method && found == token.NoPos {
			found = call.Pos()
		}
		return true
	})
	return found
}
