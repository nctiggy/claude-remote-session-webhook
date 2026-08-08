package main

// `crswd keygen` (T013), and the three things that have to be true of a command
// whose output is a private key.
//
// Two of them are about the key never becoming a file. That is not a stylistic
// preference: the failure this whole task is written against is a keygen that
// "helpfully" saves what it generated, because a key on disk beside a checkout
// is a key that gets committed by the next `git add -A`. So the first test
// watches for a write *and* asserts structurally that keygen.go cannot make one,
// and the third sweeps the repository for key material that arrived some other
// way.
//
// The third is about the pair being usable. A keygen that prints two blobs in
// formats the verifier cannot read fails at the first real release, weeks later,
// with a signature that refuses and no clue why — and by then the operator has
// already pasted the private half into a repository secret and cannot tell
// whether the problem is the key, the signing step, or the verifier.
//
// None of these generate a key that outlives the test binary. The one that needs
// a real private key to detect writes it into t.TempDir(); planting one inside
// the repository to prove the sweep works is the exact mistake being tested for.

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"unicode/utf8"
)

// keygenSource is the file under test, read rather than called by the structural
// half of TestKeygenWritesNothingToDisk.
const keygenSource = "keygen.go"

// keygenMayImport is everything keygen.go is allowed to reach, and the list is
// this short on purpose. A file that imports neither os nor log cannot create a
// file, cannot append to one, and cannot hand the key to a logger that writes to
// either — so "writes nothing to disk" stops being a property somebody has to
// keep remembering and becomes one the compiler carries.
//
// Adding an entry here is a decision about a command that prints a private key,
// not a tidy-up. Make it deliberately.
var keygenMayImport = []string{
	"crypto/ed25519",
	"crypto/rand",
	"encoding/base64",
	"io",
}

// TestKeygenWritesNothingToDisk is the contract's first requirement, made twice
// over because neither half is sufficient alone.
//
// The behavioural half watches the three directories a convenience feature would
// actually choose — the home it is told about, the temporary directory, and the
// one the command was run from — and would miss a fourth. The structural half
// cannot miss any of them, because it denies the file the means.
func TestKeygenWritesNothingToDisk(t *testing.T) {
	// Not parallel: t.Setenv, and the point is to watch directories while
	// nothing else in this binary is writing into them.
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "config"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, "data"))
	t.Setenv("XDG_RUNTIME_DIR", filepath.Join(home, "run"))

	temporary := t.TempDir()
	t.Setenv("TMPDIR", temporary)

	here, err := os.Getwd()
	if err != nil {
		t.Fatalf("working directory: %v", err)
	}

	watched := []string{home, temporary, here}
	before := map[string]map[string]bool{}
	for _, dir := range watched {
		before[dir] = treeOf(t, dir)
	}

	var out, errOut bytes.Buffer
	if code := runKeygen(&out, &errOut, nil); code != 0 {
		t.Fatalf("keygen exited %d, want 0:\n%s", code, errOut.String())
	}

	for _, dir := range watched {
		for path := range treeOf(t, dir) {
			if !before[dir][path] {
				t.Errorf("keygen created %s. What it prints is a private key, and a key written to a file is a key that gets committed — there is deliberately no flag that asks for this", path)
			}
		}
	}

	// The sweep above is a guess about where. This is the assertion that does
	// not have to guess.
	file, err := parser.ParseFile(token.NewFileSet(), keygenSource, nil, parser.ImportsOnly)
	if err != nil {
		t.Fatalf("parse %s: %v", keygenSource, err)
	}
	if len(file.Imports) == 0 {
		t.Fatalf("%s imports nothing at all, so this check is reading the wrong file", keygenSource)
	}
	for _, imported := range file.Imports {
		path := strings.Trim(imported.Path.Value, `"`)
		if !slices.Contains(keygenMayImport, path) {
			t.Errorf("%s imports %s, which is not one of: %s.\nos and log are the two that matter — with neither of them in scope, saving the key somewhere convenient is not something this file can do",
				keygenSource, path, strings.Join(keygenMayImport, ", "))
		}
	}
}

// TestKeygenOutputIsParseableByVerifier is the other half of the contract: the
// printed public half verifies a signature made by the printed private half.
//
// The blobs are found by decoding rather than by matching the prose around them,
// so the test says nothing about the wording and everything about there being
// exactly one key of each size in the output. A second copy of the private half
// — printed twice, or echoed in a summary line — fails here rather than being
// discovered in a terminal's scrollback.
func TestKeygenOutputIsParseableByVerifier(t *testing.T) {
	t.Parallel()

	var out, errOut bytes.Buffer
	if code := runKeygen(&out, &errOut, nil); code != 0 {
		t.Fatalf("keygen exited %d, want 0:\n%s", code, errOut.String())
	}
	if errOut.Len() != 0 {
		t.Errorf("keygen wrote %q to the diagnostic stream; a run that succeeded reports on one stream only", errOut.String())
	}

	printed := out.String()

	// Anywhere in the output, not line by line: a summary that repeats the
	// private half inside a sentence is a second copy of it, and a scan that
	// only looked at whole lines would call that output clean. Standard
	// encoding only, so a blob printed in the URL alphabet is *not* counted —
	// which is the point, because neither release_key.txt nor install.sh can
	// read one.
	var public ed25519.PublicKey
	var private ed25519.PrivateKey
	var publics, privates int
	var blobs []string
	for _, blob := range anyBase64Blob.FindAllString(printed, -1) {
		raw, err := base64.StdEncoding.DecodeString(blob)
		if err != nil {
			continue
		}
		switch len(raw) {
		case ed25519.PublicKeySize:
			public, publics = raw, publics+1
			blobs = append(blobs, blob)
		case ed25519.PrivateKeySize:
			private, privates = raw, privates+1
			blobs = append(blobs, blob)
		}
	}
	if publics != 1 || privates != 1 {
		t.Fatalf("keygen printed %d public and %d private standard-base64 blobs; want exactly one of each — that is the shape install.sh's key list and internal/updater/release_key.txt are read as, and a second copy of the private half is a second copy however friendly the sentence around it. What it printed:\n%s",
			publics, privates, redacted(printed))
	}

	// Each on a line of its own, because what the operator does with these is
	// select the line and paste it. A key sharing a line with prose is one they
	// have to edit by hand, and an edited key is a key with a typo in it.
	for _, blob := range blobs {
		if !slices.ContainsFunc(strings.Split(printed, "\n"), func(line string) bool {
			return strings.TrimSpace(line) == blob
		}) {
			t.Errorf("a key was printed inside a longer line rather than on one of its own:\n%s", redacted(printed))
		}
	}

	// The private half must be well formed and not merely the right length:
	// Go's PrivateKey.Public() copies the stored tail rather than deriving it,
	// so 64 arbitrary bytes would pass a comparison against themselves.
	if !bytes.Equal(ed25519.NewKeyFromSeed(private[:ed25519.SeedSize]), private) {
		t.Fatalf("the private half is not a well-formed ed25519 key: the public half stored inside it is not the one its seed derives")
	}
	if !bytes.Equal(private[ed25519.SeedSize:], public) {
		t.Errorf("the two halves keygen printed are not a pair, so the operator would commit a key that cannot verify anything CI signs")
	}

	// The verifier's own operation on the shape it will meet: T015 reads
	// SHA256SUMS.sig as a raw ed25519 signature over the bytes of SHA256SUMS,
	// and install.sh hands openssl exactly the same two files.
	sums := []byte("0000000000000000000000000000000000000000000000000000000000000000  crswd_v0.42_linux_amd64.tar.gz\n")
	signature := ed25519.Sign(private, sums)
	if !ed25519.Verify(public, sums, signature) {
		t.Errorf("the printed public half does not verify a signature made by the printed private half; every release signed with this pair would be refused")
	}
	if ed25519.Verify(public, []byte(string(sums)+"tampered\n"), signature) {
		t.Errorf("the printed public half verifies a signature over bytes it was not made from")
	}

	// The output is the operator's handover as well as two keys. All three
	// destinations have to be named: a private half in the secret with no
	// public line committed signs releases nothing will accept, and a public
	// line in one of the two files but not the other is a release the daemon
	// installs and the installer refuses.
	for _, destination := range []string{"RELEASE_SIGNING_KEY", committedKeyFile, "install.sh"} {
		if !strings.Contains(printed, destination) {
			t.Errorf("keygen's output never names %s, which is one of the three places the operator has to put something.\nWhat it printed:\n%s", destination, redacted(printed))
		}
	}
}

// TestKeygenIsReachableFromTheCommandLine is the caller. The three tests around
// it drive runKeygen directly, and every one of them would stay green against a
// keygen.go nothing dispatches to — which is the failure this repository has
// shipped four times and the plan names by name.
//
// Structural rather than a real exec, because the property is a line in main.go
// and the untagged suite builds no binary: `crswd keygen` reaches runKeygen, and
// it is handed os.Stdout. The second half matters as much as the first. Passing
// os.Stderr instead would put the key on the stream the daemon's diagnostics use
// — the one systemd merges into the journal — and every test here that reads a
// buffer would still pass.
func TestKeygenIsReachableFromTheCommandLine(t *testing.T) {
	t.Parallel()

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "main.go", nil, 0)
	if err != nil {
		t.Fatalf("parse main.go: %v", err)
	}

	var calls int
	ast.Inspect(file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		fn, ok := call.Fun.(*ast.Ident)
		if !ok || fn.Name != theKeygenReport {
			return true
		}

		calls++
		if len(call.Args) == 0 || render(call.Args[0]) != "os.Stdout" {
			t.Errorf("%s: %s reports to somewhere other than standard output; the pair it prints is the answer the operator ran the command for, and stderr is where the daemon's diagnostics go",
				fset.Position(call.Pos()), theKeygenReport)
		}
		return true
	})

	if calls != 1 {
		t.Fatalf("main.go calls %s %d times; want exactly one, on the `%s` argument. Without it the command exists and nothing reaches it",
			theKeygenReport, calls, keygenCommand)
	}
	// What the dispatch compares against is not checked here, and does not need
	// to be: main.go branches on keygenCommand, the same constant this test
	// names, so there is no second spelling of the word to drift from.
}

// TestNoPrivateKeyInRepository is FR-030 as a property of the tree rather than
// of any one command: nothing committed here contains ed25519 private key
// material, whoever put it there and whatever they called the file.
//
// The failure it is written for is a fixture — a test that needs "a key" and
// gets one by running keygen and pasting the output, which is a real signing key
// in the repository whatever the comment beside it says.
func TestNoPrivateKeyInRepository(t *testing.T) {
	t.Parallel()

	found, scanned := privateKeysUnder(t, moduleRoot)
	if len(found) > 0 {
		t.Errorf("ed25519 private key material is in this repository:\n  %s\nThe private half of a release key belongs in the repository secret RELEASE_SIGNING_KEY and nowhere else (FR-030). Rotate it: it is compromised the moment it is committed",
			strings.Join(found, "\n  "))
	}
	// A clean report from a walk that read nothing is the shape a wrong root,
	// a skipped directory or a too-eager binary filter all take.
	if scanned < 100 {
		t.Fatalf("the sweep read %d files under %s; this repository has far more than that, so it is not reading the tree it claims to be", scanned, moduleRoot)
	}

	// A sweep that has never found anything is a sweep whose silence means
	// nothing, so it is pointed at a tree that certainly holds two. The key is
	// generated here and never leaves t.TempDir() — planting one inside the
	// repository to prove the point is the mistake this test exists to catch.
	planted := t.TempDir()
	_, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate a key to plant: %v", err)
	}
	plant := func(name, content string) {
		if err := os.WriteFile(filepath.Join(planted, name), []byte(content), 0o600); err != nil {
			t.Fatalf("plant %s: %v", name, err)
		}
	}
	plant("fixture.go", "const exampleKey = \""+base64.StdEncoding.EncodeToString(private)+"\"\n")
	plant("signing.pem", "-----BEGIN "+pemPrivateKey+"\nMC4CAQAwBQYDK2VwBCIEIA\n-----END "+pemPrivateKey+"\n")

	if plantedFound, _ := privateKeysUnder(t, planted); len(plantedFound) != 2 {
		t.Fatalf("the sweep found %d of the 2 keys planted under %s, so its silence about the repository proves nothing: %q", len(plantedFound), planted, plantedFound)
	}
}

// anyBase64Blob is anything long enough to be key material, in any of base64's
// alphabets. Deliberately looser than base64Run below on both counts: 40
// characters catches a 32-byte public half as well as a 64-byte private one,
// and -_ catches the URL alphabet — because the failure being reported may well
// be "keygen printed it in the wrong encoding", and a redaction that only knew
// the right one would let exactly that case through.
var anyBase64Blob = regexp.MustCompile(`[A-Za-z0-9+/_-]{40,}={0,2}`)

// redacted is what a failing assertion about keygen's output may print.
//
// A test failure is a log: it reaches a terminal, a CI transcript, and an issue
// somebody pastes it into. Dumping what keygen printed puts a private key in all
// three, which is FR-030's exact prohibition arriving through the one door
// nobody guards. Found by mutation — the URL-encoding mutation of the public
// half made this test fail, and the failure printed the private half.
func redacted(printed string) string {
	return anyBase64Blob.ReplaceAllString(printed, "<a base64 blob, not shown>")
}

// pemPrivateKey is the tail of a PEM private-key banner, assembled from two
// pieces so that this file — which the sweep below reads like any other — does
// not contain the string it is searching for. A detector that reports itself is
// a detector somebody switches off.
const pemPrivateKey = "PRIVATE KEY" + "-----"

// base64Run is a run of base64 long enough to hold a 64-byte value: 86
// characters unpadded, 88 with the padding standard encoding adds.
var base64Run = regexp.MustCompile(`[A-Za-z0-9+/]{86,}={0,2}`)

// privateKeysUnder reports every file under root holding ed25519 private key
// material — naming the file and how it was recognised — and how many files it
// actually read, so a caller can tell "nothing found" from "nothing looked at".
//
// .git is skipped and nothing else is: a key hard-coded into a workflow instead
// of read from a secret is exactly the mistake worth catching, and .github is a
// dot directory. Files that are not text are skipped the way `grep -I` skips
// them, because a build artifact left in the tree is neither committed nor
// readable.
func privateKeysUnder(t *testing.T, root string) (found []string, scanned int) {
	t.Helper()

	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			if d.Name() == ".git" {
				return fs.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}

		raw, readErr := os.ReadFile(path) //nolint:gosec // G304: the path comes from a walk of this repository, in a test that reads every file in it.
		if readErr != nil {
			return readErr
		}
		if bytes.IndexByte(raw, 0) >= 0 || !utf8.Valid(raw) {
			return nil
		}
		scanned++

		text := string(raw)
		if strings.Contains(text, pemPrivateKey) {
			found = append(found, path+" — a PEM private-key block")
			return nil
		}
		for _, run := range base64Run.FindAllString(text, -1) {
			if holdsEd25519PrivateKey(run) {
				found = append(found, path+" — a base64 ed25519 private key")
				return nil
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("search %s for key material: %v", root, err)
	}
	return found, scanned
}

// holdsEd25519PrivateKey reports whether any window of a base64 run decodes to
// one. Sliding rather than decoding the run whole, because a key pasted into the
// middle of a longer token still starts at some offset, and a check that only
// looked at the whole run would miss it.
func holdsEd25519PrivateKey(run string) bool {
	// Padded and unpadded: what keygen prints is the first, but the padding is
	// the easiest character to lose on the way into a file.
	for _, width := range []int{88, 86} {
		for i := 0; i+width <= len(run); i++ {
			if isEd25519PrivateKey(run[i : i+width]) {
				return true
			}
		}
	}
	return false
}

// isEd25519PrivateKey decides by checking the key against itself: the last 32
// bytes of an ed25519 private key are the public half its first 32 derive. An
// unrelated 64-byte blob satisfies that with probability 2^-256, so this reports
// what it found rather than what it suspects — which is what makes it safe to
// fail a build on.
//
// It cannot see a bare 32-byte seed, which is indistinguishable from any other
// 32 random bytes. That is the accepted limit: `crswd keygen` prints the 64-byte
// form, so what this repository could plausibly commit is what this catches.
func isEd25519PrivateKey(blob string) bool {
	raw, err := base64.StdEncoding.DecodeString(blob)
	if err != nil {
		raw, err = base64.RawStdEncoding.DecodeString(blob)
	}
	if err != nil || len(raw) != ed25519.PrivateKeySize {
		return false
	}
	return bytes.Equal(ed25519.NewKeyFromSeed(raw[:ed25519.SeedSize]), raw)
}

// treeOf is every path under root, so that two calls can be compared for
// something that appeared between them.
func treeOf(t *testing.T, root string) map[string]bool {
	t.Helper()

	paths := map[string]bool{}
	err := filepath.WalkDir(root, func(path string, _ fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		paths[path] = true
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
	return paths
}
