package session

import (
	"errors"
	"go/ast"
	"go/parser"
	gotoken "go/token"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

// conversationFixture is the working-directory fixture with a conversation store
// beside it, so a test can describe a host Claude Code has been run on without
// touching the one it is running on.
//
//	base/code/repo                    the working directory being asked about
//	base/store                        Claude Code's ~/.claude/projects
//	base/store/-…-code-repo/*.jsonl   its conversations
type conversationFixture struct {
	workDirFixture
	store string
}

func newConversationFixture(t *testing.T) conversationFixture {
	t.Helper()

	f := conversationFixture{workDirFixture: newWorkDirFixture(t)}
	f.store = filepath.Join(f.base, "store")
	if err := os.MkdirAll(f.store, 0o750); err != nil {
		t.Fatalf("create the conversation store: %v", err)
	}
	return f
}

// workDir is the ordinary approved working directory these tests ask about.
func (f conversationFixture) workDir() string { return filepath.Join(f.root, "repo") }

// storeFor is where this fixture writes one working directory's conversations,
// and it restates the mapping rather than calling storeDirName.
//
// Calling the production function would make every case below agree with a typo
// in it: a listing that encoded the path wrongly would read the directory this
// helper wrote wrongly in the same way, and the suite would stay green while the
// daemon found nothing on a real host. TestStoreIsClaudeCodesOwnLayout pins the
// mapping itself against a literal.
func (f conversationFixture) storeFor(workDir string) string {
	return filepath.Join(f.store, strings.ReplaceAll(workDir, "/", "-"))
}

// record writes one conversation file for a working directory and returns its
// path.
//
// It is given real contents on purpose. Every case here would pass against an
// implementation that opened and parsed each transcript, so the fixture at least
// makes sure there is something to find — the assertions that a file is *not*
// opened are TestNeverOpensAFile's, and they need a file worth opening.
func (f conversationFixture) record(t *testing.T, workDir, name string, modified time.Time) string {
	t.Helper()

	dir := f.storeFor(workDir)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatalf("create the store directory for a working directory: %v", err)
	}

	path := filepath.Join(dir, name)
	body := `{"role":"user","content":"the contents of a transcript, which never leaves the host"}` + "\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	if !modified.IsZero() {
		if err := os.Chtimes(path, modified, modified); err != nil {
			t.Fatalf("set the modification time of %s: %v", name, err)
		}
	}
	return path
}

// conversationTime is a fixed instant the fixtures are placed around, so the
// expectations below are literals rather than arithmetic on time.Now().
var conversationTime = time.Date(2026, time.August, 7, 9, 0, 0, 0, time.UTC)

func TestListsIdAndTimeOnly(t *testing.T) {
	t.Parallel()

	// FR-034 stated as the type it is. A third field is how this stops being a
	// listing: there is no way to fill one in without reading the transcript, so
	// the field is the disclosure and not the code that would populate it.
	typ := reflect.TypeOf(Conversation{})
	want := []struct {
		name string
		kind reflect.Type
	}{
		{name: "ID", kind: reflect.TypeOf("")},
		{name: "Modified", kind: reflect.TypeOf(time.Time{})},
	}
	if typ.NumField() != len(want) {
		t.Fatalf("Conversation has %d fields, want exactly %d: an identifier and a time, never contents (FR-034)",
			typ.NumField(), len(want))
	}
	for i, field := range want {
		if got := typ.Field(i); got.Name != field.name || got.Type != field.kind {
			t.Errorf("Conversation field %d is %s %s, want %s %s", i, got.Name, got.Type, field.name, field.kind)
		}
	}

	f := newConversationFixture(t)
	dir := f.workDir()

	older := conversationTime.Add(-2 * time.Hour)
	newer := conversationTime.Add(-1 * time.Minute)
	// The names run the other way to the times on purpose. ReadDir returns a
	// directory sorted by name, so a fixture whose newest file also sorts first
	// would be satisfied by an implementation that never sorted at all.
	f.record(t, dir, "0a1b2c3d-old.jsonl", older)
	f.record(t, dir, "f9e8d7c6-new.jsonl", newer)
	// Two entries sharing a modification time, which the identifier tiebreak
	// orders. Without it these two would come back in whatever order the
	// filesystem offered and this test would be flaky rather than wrong.
	f.record(t, dir, "same-b.jsonl", older)
	f.record(t, dir, "same-a.jsonl", older)

	// Noise the listing must not offer: a file that is not a transcript, a
	// directory that is named like one, an entry whose whole name is the suffix,
	// and a symlink pointing at a real transcript outside the store.
	f.record(t, dir, "notes.txt", conversationTime)
	f.record(t, dir, ".jsonl", conversationTime)
	if err := os.Mkdir(filepath.Join(f.storeFor(dir), "a-directory.jsonl"), 0o750); err != nil {
		t.Fatalf("create the directory that is named like a transcript: %v", err)
	}
	target := f.record(t, filepath.Join(f.second, "repo"), "elsewhere.jsonl", conversationTime)
	if err := os.Symlink(target, filepath.Join(f.storeFor(dir), "linked.jsonl")); err != nil {
		t.Fatalf("link a transcript into the store: %v", err)
	}

	got, err := listConversations(dir, f.roots(), f.store)
	if err != nil {
		t.Fatalf("listConversations() = %v, want the conversations of an approved directory", err)
	}

	// Newest first, then by identifier. The times are the ones written, to the
	// second: a listing reporting time.Now(), the directory's own time, or the
	// entry's size-as-a-time would satisfy "two fields" and none of this.
	expected := []Conversation{
		{ID: "f9e8d7c6-new", Modified: newer},
		{ID: "0a1b2c3d-old", Modified: older},
		{ID: "same-a", Modified: older},
		{ID: "same-b", Modified: older},
	}
	if len(got) != len(expected) {
		t.Fatalf("listConversations() returned %d conversations (%v), want %d", len(got), ids(got), len(expected))
	}
	for i, wanted := range expected {
		if got[i].ID != wanted.ID {
			t.Errorf("conversation %d is %q, want %q; the order is newest first, ties by identifier", i, got[i].ID, wanted.ID)
		}
		if !got[i].Modified.Equal(wanted.Modified) {
			t.Errorf("conversation %q reports %v, want the file's own %v", got[i].ID, got[i].Modified, wanted.Modified)
		}
	}
}

// ids is what a failure prints instead of a slice of structs carrying times.
func ids(conversations []Conversation) []string {
	names := make([]string, 0, len(conversations))
	for _, c := range conversations {
		names = append(names, c.ID)
	}
	return names
}

// TestNeverOpensAFile is FR-035, and it is half structural because the behaviour
// it forbids is one nobody writes a case for. "Show the first prompt" is added as
// a kindness to the operator, every existing case here stays green, and a
// daemon that hands a transcript to a browser ships.
//
// The runtime half alone would not catch it either: a transcript this daemon can
// read is the normal state of the store, so the only observable difference is a
// file it cannot.
func TestNeverOpensAFile(t *testing.T) {
	t.Parallel()

	t.Run("this file reaches the filesystem only to list a directory", func(t *testing.T) {
		t.Parallel()

		// Only os.ReadDir names a directory rather than a file, and only
		// UserHomeDir answers where the store is. Everything else in os that this
		// file could reach for takes a path to something to open.
		reads := map[string]bool{"ReadDir": true, "UserHomeDir": true}

		fset := gotoken.NewFileSet()
		file, err := parser.ParseFile(fset, "conversation.go", nil, 0)
		if err != nil {
			t.Fatalf("parse conversation.go: %v", err)
		}

		// A reader does not have to come through os: bufio, io and encoding/json
		// each read a file handed to them, and an aliased os would put every call
		// beyond the selector walk below, which would then pass by seeing nothing.
		openers := []string{"bufio", "io", "io/ioutil", "encoding/json", "os/exec"}
		for _, imp := range file.Imports {
			path, err := strconv.Unquote(imp.Path.Value)
			if err != nil {
				t.Fatalf("%s: unquote an import path: %v", fset.Position(imp.Pos()), err)
			}
			if slices.Contains(openers, path) {
				t.Errorf("%s: conversation.go imports %q; this file lists a directory and opens nothing in it — a listing that cannot leak a transcript is the whole of FR-035",
					fset.Position(imp.Pos()), path)
			}
			if path == "os" && imp.Name != nil {
				t.Errorf("%s: conversation.go imports os as %q; the walk below looks for os.X and would not see a call made through another name",
					fset.Position(imp.Pos()), imp.Name.Name)
			}
		}

		var seen []string
		ast.Inspect(file, func(n ast.Node) bool {
			sel, ok := n.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			pkg, ok := sel.X.(*ast.Ident)
			if !ok || pkg.Name != "os" {
				return true
			}
			seen = append(seen, sel.Sel.Name)
			if !reads[sel.Sel.Name] {
				t.Errorf("%s: conversation.go reaches os.%s; this file may list a directory and read the home it is under, and nothing else. Every conversation is a transcript of somebody's work on this host",
					fset.Position(sel.Pos()), sel.Sel.Name)
			}
			return true
		})

		if !slices.Contains(seen, "ReadDir") {
			t.Fatalf("nothing in conversation.go calls os.ReadDir (found %v), so the listing has gone and this walk is checking an empty file", seen)
		}
	})

	t.Run("a transcript this daemon cannot read is still offered", func(t *testing.T) {
		t.Parallel()

		if os.Geteuid() == 0 {
			t.Skip("running as root, which can read a 0000 file: the mode would prove nothing")
		}

		f := newConversationFixture(t)
		dir := f.workDir()
		path := f.record(t, dir, "unreadable.jsonl", conversationTime)
		if err := os.Chmod(path, 0o000); err != nil {
			t.Fatalf("make the transcript unreadable: %v", err)
		}

		// The fixture's own proof that the mode bites. Without it a filesystem
		// mounted without permissions would turn this case into one that passes
		// for the wrong reason.
		if _, err := os.ReadFile(path); err == nil { //nolint:gosec // the point of this line is that it must fail.
			t.Fatal("the fixture's 0000 transcript is readable, so this case cannot tell a listing from a read")
		}

		got, err := listConversations(dir, f.roots(), f.store)
		if err != nil {
			t.Fatalf("listConversations() = %v, want a listing that never needed to read the file", err)
		}
		if len(got) != 1 || got[0].ID != "unreadable" {
			t.Fatalf("listConversations() = %v, want the one conversation it cannot open", ids(got))
		}
	})
}

func TestRefusesOutsideApprovedRoot(t *testing.T) {
	t.Parallel()

	f := newConversationFixture(t)

	tests := []struct {
		name string
		in   string
		want error
	}{
		{name: "a directory outside every root", in: filepath.Join(f.outside, "repo"), want: ErrWorkDirOutsideRoots},
		{
			// The prefix trap: /base/codeEVIL carries /base/code as a string
			// prefix and is under no approved root.
			name: "a sibling whose name starts with the root's",
			in:   filepath.Join(f.evil, "repo"),
			want: ErrWorkDirOutsideRoots,
		},
		{
			name: "a link inside a root pointing out of every root",
			in:   f.escapeLink(),
			want: ErrWorkDirOutsideRoots,
		},
		{name: "a relative path", in: "repo", want: ErrWorkDirNotAbsolute},
		{name: "no path at all", in: "", want: ErrInvalidWorkDir},
		{name: "a path that does not exist", in: filepath.Join(f.root, "gone"), want: ErrWorkDirUnresolvable},
		{name: "a file", in: f.file(), want: ErrWorkDirNotDirectory},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			// A store for the refused directory, so that an implementation which
			// listed first and checked afterwards is caught returning it. Without
			// this every case here is satisfied by a lookup that simply found
			// nothing.
			if filepath.IsAbs(tt.in) {
				f.record(t, tt.in, "0f1e2d3c.jsonl", conversationTime)
			}

			got, err := listConversations(tt.in, f.roots(), f.store)
			if got != nil {
				t.Errorf("listConversations(%q) returned %v; a directory that is not under an approved root gets no lookup at all (FR-035)", tt.in, ids(got))
			}
			if !errors.Is(err, ErrInvalidWorkDir) {
				t.Errorf("listConversations(%q) = %v, want it wrapped in %v", tt.in, err, ErrInvalidWorkDir)
			}
			if !errors.Is(err, tt.want) {
				t.Errorf("listConversations(%q) = %v, want %v", tt.in, err, tt.want)
			}
			// Fatal rather than Errorf: a nil error here would take the next line
			// down with it, and a panicking subtest tears the fixture out from
			// under every other case running beside it.
			if err == nil {
				t.Fatalf("listConversations(%q) refused nothing", tt.in)
			}
			// The refusal names the reason and nothing else. It is on its way to
			// a log line, and the path came off the wire.
			if tt.in != "" && strings.Contains(err.Error(), tt.in) {
				t.Errorf("listConversations(%q) = %q, which echoes the caller's path back into the audit trail", tt.in, err)
			}
		})
	}
}

func TestAbsentStoreOffersNothing(t *testing.T) {
	t.Parallel()

	t.Run("a directory Claude Code has never run in", func(t *testing.T) {
		t.Parallel()

		f := newConversationFixture(t)
		// Another directory's conversations are present, which is what makes this
		// an assertion: a listing that read the store root rather than this
		// working directory's own place in it would offer them here.
		f.record(t, filepath.Join(f.second, "repo"), "8c7b6a59.jsonl", conversationTime)

		assertOffersNothing(t, f, f.workDir(), f.store)
	})

	t.Run("a host with no store at all", func(t *testing.T) {
		t.Parallel()

		f := newConversationFixture(t)
		assertOffersNothing(t, f, f.workDir(), filepath.Join(f.base, "never-created"))
	})

	t.Run("a daemon with no home", func(t *testing.T) {
		t.Parallel()

		f := newConversationFixture(t)
		f.record(t, f.workDir(), "5a4b3c2d.jsonl", conversationTime)

		assertOffersNothing(t, f, f.workDir(), "")
	})

	t.Run("a store directory that cannot be listed", func(t *testing.T) {
		t.Parallel()

		if os.Geteuid() == 0 {
			t.Skip("running as root, which can list a 0000 directory: the mode would prove nothing")
		}

		f := newConversationFixture(t)
		dir := f.workDir()
		f.record(t, dir, "1b2c3d4e.jsonl", conversationTime)

		unreadable := f.storeFor(dir)
		if err := os.Chmod(unreadable, 0o000); err != nil {
			t.Fatalf("make the store directory unlistable: %v", err)
		}
		// Restored so the temp directory can be removed; a test that left it
		// would fail the *next* test's cleanup rather than its own.
		t.Cleanup(func() {
			//nolint:gosec // G302 reads 0750 as a file mode; this is the directory mode MkdirAll created it with.
			if err := os.Chmod(unreadable, 0o750); err != nil {
				t.Errorf("restore the store directory's mode: %v", err)
			}
		})

		assertOffersNothing(t, f, dir, f.store)
	})
}

// assertOffersNothing is the shape every absent-store case shares: nothing
// offered, and no error — a directory with no conversations is the ordinary
// state of a host, not something an operator is asked to fix.
func assertOffersNothing(t *testing.T, f conversationFixture, workDir, store string) {
	t.Helper()

	got, err := listConversations(workDir, f.roots(), store)
	if err != nil {
		t.Fatalf("listConversations() = %v, want no error: an absent store offers nothing", err)
	}
	if len(got) != 0 {
		t.Fatalf("listConversations() = %v, want nothing offered", ids(got))
	}
}

// TestStoreIsClaudeCodesOwnLayout pins the two facts about the host that no
// fixture can check for itself, against literals.
//
// Both are invisible to every case above, which builds a store wherever it likes
// and encodes the path the same way the code under test does. A daemon with
// either of these wrong is one that quietly offers nothing on every real host,
// which is indistinguishable from a host that has no conversations.
func TestStoreIsClaudeCodesOwnLayout(t *testing.T) {
	t.Parallel()

	if got, want := conversationStore("/home/u"), filepath.Join("/home/u", ".claude", "projects"); got != want {
		t.Errorf("conversationStore() = %q, want %q — Claude Code's own directory, which this daemon reads and never creates", got, want)
	}
	if got := conversationStore(""); got != "" {
		t.Errorf("conversationStore(no home) = %q, want no store", got)
	}
	if got, want := storeDirName("/home/u/code/repo"), "-home-u-code-repo"; got != want {
		t.Errorf("storeDirName() = %q, want %q — the mapping Claude Code writes, not one of this daemon's", got, want)
	}

	// The exported entry point goes through the same refusal, which is the half a
	// caller in web/ or httpapi/ will actually reach. A ListConversations wired
	// past ResolveWorkDir would leave every case above green.
	f := newConversationFixture(t)
	got, err := ListConversations(filepath.Join(f.outside, "repo"), f.roots())
	if got != nil || !errors.Is(err, ErrWorkDirOutsideRoots) {
		t.Errorf("ListConversations(a directory outside every root) = %v, %v, want nothing and %v", ids(got), err, ErrWorkDirOutsideRoots)
	}
}
