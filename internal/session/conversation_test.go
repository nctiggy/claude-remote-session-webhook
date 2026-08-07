package session

import (
	"context"
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

	"github.com/nctiggy/claude-remote-session-webhook/internal/auth"
	"github.com/nctiggy/claude-remote-session-webhook/internal/tmuxctl"
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

// resumeFixture is the manager fixture with a conversation store beside it: a
// host Claude Code has already been run on, driven by a Manager whose creates
// can reach what it recorded.
//
// The store is set on the manager directly rather than through the environment.
// os.UserHomeDir reads a process-wide variable, so a fixture that moved HOME
// would make every case here serial — and would be describing the test binary's
// host rather than the one the case is about.
type resumeFixture struct {
	managerFixture
	conversations conversationFixture
}

func newResumeFixture(t *testing.T) resumeFixture {
	t.Helper()

	m := newManagerFixture(t)
	c := conversationFixture{workDirFixture: m.workDirFixture, store: filepath.Join(m.base, "store")}
	if err := os.MkdirAll(c.store, 0o750); err != nil {
		t.Fatalf("create the conversation store: %v", err)
	}
	m.mgr.conversationStore = c.store

	return resumeFixture{managerFixture: m, conversations: c}
}

// startedCommand is the command line the create typed into the new session's
// shell, which is the only place a resumed conversation is observable: the
// record deliberately does not carry one.
func startedCommand(t *testing.T, f resumeFixture) string {
	t.Helper()

	for _, c := range f.tmux.Calls() {
		if c.Op == tmuxctl.OpSendKeys {
			// argv is ["tmux", "send-keys", "-t", target, "--", command, "Enter"].
			if len(c.Argv) < 2 {
				t.Fatalf("send-keys ran with %q", c.Argv)
			}
			return c.Argv[len(c.Argv)-2]
		}
	}
	t.Fatal("the create sent no command into the pane at all")
	return ""
}

// TestResumeStillMintsNewRecord is FR-036 and the create half of FR-033: a
// resumed conversation is an *input* to starting a session, never an alternative
// to starting one.
//
// **Must fail when** a resume produces anything but a new record — the same
// identifier twice, a shared credential, a lifetime inherited from whatever the
// conversation belonged to before. A resume that adopted a session would be a
// second way into a record that ownership, the cap, and both deadlines are all
// keyed to, and none of those three would apply to it.
//
// The fresh case is here rather than in a test of its own because it is the same
// assertion from the other side: what a create adds for a resume is one flag and
// one word, so a create that asks for nothing must type exactly the line the
// operator configured.
func TestResumeStillMintsNewRecord(t *testing.T) {
	t.Parallel()

	const conversation = "8f14e45f-ceea-467a-9b3d-0f2fc9de5b21"

	t.Run("a create that says nothing starts fresh", func(t *testing.T) {
		t.Parallel()

		f := newResumeFixture(t)
		f.conversations.record(t, f.repo(), conversation+".jsonl", conversationTime)

		mustCreate(t, f.managerFixture, f.request())

		if got := startedCommand(t, f); got != claudeStartCommand {
			t.Errorf("a create that asked for no conversation typed %q, want %q — starting fresh is the default (FR-037)", got, claudeStartCommand)
		}
	})

	t.Run("a resumed conversation is a new session", func(t *testing.T) {
		t.Parallel()

		f := newResumeFixture(t)
		f.conversations.record(t, f.repo(), conversation+".jsonl", conversationTime)

		req := f.request()
		req.Resume = conversation
		first, firstToken := mustCreate(t, f.managerFixture, req)

		if want := claudeStartCommand + " --resume " + conversation; startedCommand(t, f) != want {
			t.Errorf("Create() typed %q, want %q", startedCommand(t, f), want)
		}
		if !idShape.MatchString(first.ID) {
			t.Errorf("a resumed session carries the id %q, which is not one this daemon minted", first.ID)
		}
		if !tokenShape.MatchString(firstToken) || !first.TokenMatches(firstToken) {
			t.Error("a resumed session was not handed a credential of its own")
		}
		if first.Lifetime != f.request().Lifetime || first.CreatedAt != f.now {
			t.Errorf("a resumed session carries lifetime %s from %s, want this daemon's own defaults as of %s",
				first.Lifetime, first.CreatedAt, f.now)
		}

		// The same conversation twice. Two records, two credentials — a resume that
		// returned the existing session would be a create that started nothing and
		// handed back something the caller already had.
		second, secondToken := mustCreate(t, f.managerFixture, req)
		if second.ID == first.ID {
			t.Error("resuming the same conversation twice produced one record; a conversation is an input to a session, not a session")
		}
		if secondToken == firstToken || second.TokenMatches(firstToken) {
			t.Error("the second session accepts the first one's credential")
		}
		if got := len(f.store.List(auth.CallerOperator)); got != 2 {
			t.Errorf("the store holds %d records after two creates, want 2", got)
		}
	})
}

// TestAmbiguousResumeRefuses is FR-032, and the one requirement in this task
// whose failure mode is a *success*: every wrong answer here starts a session
// that looks entirely correct and is carrying on from somebody else's work.
//
// **Must fail when** the daemon resolves a name it cannot match to the most
// recent conversation in the directory. Every case below is set in a directory
// holding two, and the newest one's identifier is asserted to have reached no
// command line — so a fallback to "the last conversation in this directory"
// fails here whichever of the cases it is reached through.
//
// The cases are the ways a name can fail to identify exactly one conversation in
// the directory this create actually named. They are one refusal for the reason
// the working-directory refusals are one: a caller who could tell them apart
// could ask this daemon which conversations exist where.
func TestAmbiguousResumeRefuses(t *testing.T) {
	t.Parallel()

	const (
		older = "0a1b2c3d-4e5f-6071-8293-a4b5c6d7e8f9"
		newer = "f9e8d7c6-b5a4-3928-1706-f5e4d3c2b1a0"
	)

	// Written as functions of the fixture, because two of them are about a
	// conversation somewhere else on the host and one is about a store there is
	// none of. Each takes the subtest's own t: a Fatalf on the parent's would tear
	// down the temp directory its siblings are still reading, and every one of them
	// would report a fixture that is not there.
	cases := map[string]func(t *testing.T, f resumeFixture) string{
		"an identifier this directory does not hold": func(*testing.T, resumeFixture) string {
			return "11111111-2222-3333-4444-555555555555"
		},
		"a conversation belonging to another approved directory": func(t *testing.T, f resumeFixture) string {
			const elsewhere = "c0ffee00-dead-beef-cafe-0123456789ab"
			f.conversations.record(t, filepath.Join(f.second, "repo"), elsewhere+".jsonl", conversationTime)
			return elsewhere
		},
		"an identifier differing only in case": func(*testing.T, resumeFixture) string {
			return strings.ToUpper(newer)
		},
		"a name that would close the command line": func(t *testing.T, f resumeFixture) string {
			const hostile = "x; touch pwned"
			f.conversations.record(t, f.repo(), hostile+".jsonl", conversationTime.Add(time.Hour))
			return hostile
		},
		"a store this host does not have": func(_ *testing.T, f resumeFixture) string {
			f.mgr.conversationStore = ""
			return newer
		},
	}

	for name, arrange := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			f := newResumeFixture(t)
			// Two conversations, so "the most recent" is an answer this daemon
			// could give and must not.
			f.conversations.record(t, f.repo(), older+".jsonl", conversationTime)
			f.conversations.record(t, f.repo(), newer+".jsonl", conversationTime.Add(time.Minute))

			req := f.request()
			req.Resume = arrange(t, f)

			s, token, err := f.mgr.Create(context.Background(), req)
			if !errors.Is(err, ErrUnknownConversation) {
				t.Fatalf("Create() = %v, %v; want the refusal %v", s, err, ErrUnknownConversation)
			}
			if s != nil || token != "" {
				t.Error("a refused create handed back a session or a credential")
			}
			if got := len(f.store.List(auth.CallerOperator)); got != 0 {
				t.Errorf("a refused create left %d records; a create refused before anything is built builds nothing", got)
			}
			// The refusal costs no tmux command at all, which is what makes it a
			// refusal rather than a teardown.
			if calls := f.tmux.Calls(); len(calls) != 0 {
				t.Errorf("a refused create ran %d tmux commands: %v", len(calls), calls)
			}
			// FR-032 itself. Nothing reached a pane, so this asserts the newest
			// identifier is absent from the whole trail of commands rather than
			// from one of them — the fallback would appear wherever it were added.
			for _, c := range f.tmux.Calls() {
				for _, arg := range c.Argv {
					if strings.Contains(arg, newer) {
						t.Errorf("the refusal resolved to the most recent conversation instead: %q", c.Argv)
					}
				}
			}
			// Never the name that was refused. It is caller text, and an error is
			// what the audit trail carries (docs/security.md).
			if strings.Contains(err.Error(), req.Resume) {
				t.Errorf("Create() error %q carries the identifier the caller sent", err)
			}
		})
	}
}

// TestAnIdentifierThatCouldReachAShellIsNeitherOfferedNorResumed is the alphabet
// at both ends, and it is one test because the two ends must agree: a listing
// that offered a name the create refuses is a page an operator cannot act on,
// and a create that accepted a name the listing would not offer is a command
// line nobody wrote.
//
// **Must fail when** the identifier stops being checked in either place. What is
// at stake is not markup: a resumed identifier is appended to the command line
// this daemon types into an unsandboxed shell, and every entry here is a file
// name anyone able to write under the daemon's home can create.
func TestAnIdentifierThatCouldReachAShellIsNeitherOfferedNorResumed(t *testing.T) {
	t.Parallel()

	// Every one of these is a name a single directory entry can carry, which is
	// the point: they are files anyone able to write under the daemon's home can
	// create, not strings only a caller could invent.
	hostile := []string{
		"x; touch pwned",
		"$(id)",
		"`id`",
		"a b",
		"--dangerously-skip-permissions",
		"'",
		strings.Repeat("a", maxConversationID+1),
	}
	// Refused as a request and impossible as an entry, so they are asked for
	// without being written: a directory holds no entry named with a separator.
	requested := append([]string{"../../etc/passwd", "x; touch /tmp/pwned"}, hostile...)

	f := newResumeFixture(t)
	for i, name := range hostile {
		f.conversations.record(t, f.repo(), name+".jsonl", conversationTime.Add(time.Duration(i)*time.Minute))
	}

	offered, err := listConversations(f.repo(), f.roots(), f.conversations.store)
	if err != nil {
		t.Fatalf("listConversations() = %v, want the ordinary listing", err)
	}
	if len(offered) != 0 {
		t.Errorf("listConversations() offers %v; none of these is a name this daemon may put on a command line", ids(offered))
	}

	for _, name := range requested {
		req := f.request()
		req.Resume = name
		if _, _, err := f.mgr.Create(context.Background(), req); !errors.Is(err, ErrUnknownConversation) {
			t.Errorf("Create(resume %q) = %v, want %v", name, err, ErrUnknownConversation)
		}
	}
	if calls := f.tmux.Calls(); len(calls) != 0 {
		t.Errorf("a refused create ran %d tmux commands: %v", len(calls), calls)
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
