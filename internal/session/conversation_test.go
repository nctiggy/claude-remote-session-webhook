package session

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The alphabet is the whole defence, so its negative cases are the test that
// matters (contracts/conversation-resume.md).
//
// A resume value ends up as an argument in a line delivered by SendKeys — typed
// into a live shell. Every other caller-supplied value in this daemon either
// never reaches that line or goes through Paste, which writes to a tmux buffer
// over stdin precisely so a payload never becomes part of a command line. This
// one has to be on the line, because it is a flag argument.
//
// **Must fail when** anything outside 8-4-4-4 lowercase hex is accepted.
func TestValidateResume(t *testing.T) {
	t.Parallel()

	const good = "88e5294c-d947-4527-b8c9-5eb8384bae6a"

	accepted := map[string]string{
		"empty is a create that resumes nothing": "",
		"the word the form posts":                ResumeLatest,
		"a conversation identifier":              good,
		"all zeroes is still the right shape":    "00000000-0000-0000-0000-000000000000",
		"all f is the top of the alphabet":       "ffffffff-ffff-ffff-ffff-ffffffffffff",
	}
	for name, v := range accepted {
		t.Run("accepts/"+name, func(t *testing.T) {
			t.Parallel()

			got, err := ValidateResume(v)
			if err != nil {
				t.Fatalf("ValidateResume(%q) = _, %v; want it accepted", v, err)
			}
			if got != v {
				t.Errorf("ValidateResume(%q) = %q; the value must not be rewritten on the way through", v, got)
			}
		})
	}

	refused := map[string]string{
		// Shell syntax, which is what the alphabet exists for. Each of these in a
		// line typed at a shell reaches a second program.
		"a command substitution": "$(whoami)",
		"a backtick":             "`id`",
		"a separator":            good + "; id",
		"a pipeline":             good + " | id",
		"an and":                 good + " && id",
		"a redirect":             good + " > /tmp/x",
		"a newline":              good + "\nid",
		"a carriage return":      good + "\rid",
		"a leading space":        " " + good,
		"a trailing space":       good + " ",
		"a quote":                good + `"`,
		"a single quote":         good + "'",
		"a backslash":            good + `\`,
		"a dollar":               "$" + good,
		"a glob":                 good + "*",
		// Right alphabet, wrong shape. Each of these is a value this daemon would
		// have to guess about, and guessing is what it must not do.
		"a prefix":                    "88e5294c",
		"a group too short":           "88e5294-d947-4527-b8c9-5eb8384bae6a",
		"a group too long":            "88e5294cc-d947-4527-b8c9-5eb8384bae6a",
		"a group missing":             "88e5294c-d947-4527-5eb8384bae6a",
		"a group too many":            good + "-0000",
		"no hyphens at all":           strings.ReplaceAll(good, "-", ""),
		"uppercase":                   strings.ToUpper(good),
		"one uppercase digit":         "88E5294c-d947-4527-b8c9-5eb8384bae6a",
		"outside hex":                 "88g5294c-d947-4527-b8c9-5eb8384bae6a",
		"a path":                      "../../etc/passwd",
		"a path that looks like one":  "conversations/" + good,
		"the word in another case":    "LATEST",
		"a word that is not the word": "newest",
	}
	for name, v := range refused {
		t.Run("refuses/"+name, func(t *testing.T) {
			t.Parallel()

			got, err := ValidateResume(v)
			if err == nil {
				t.Fatalf("ValidateResume(%q) = %q, nil; want it refused — this value would reach a line typed at a shell", v, got)
			}
			if got != "" {
				t.Errorf("ValidateResume(%q) returned %q alongside its refusal; a refused value must reach no caller", v, got)
			}
			// The trail may carry no byte a caller supplied (FR-042), and this
			// sentinel is what a handler records instead of the value.
			if strings.Contains(err.Error(), v) && v != ResumeLatest {
				t.Errorf("ValidateResume(%q) put the refused value in its error: %v", v, err)
			}
		})
	}
}

// conversationHome plants a Claude conversation history for a working directory
// and points the process at it, returning the directory a create would name.
//
// It sets HOME for the test, which is what os.UserHomeDir reads. t.Setenv makes
// the test non-parallel by construction, which is correct here: the whole point
// is a process-wide value.
func conversationHome(t *testing.T, workDir string, files map[string]time.Time) {
	t.Helper()

	home := t.TempDir()
	t.Setenv("HOME", home)

	project := filepath.Join(home, ".claude", "projects", projectDirFor(workDir))
	if err := os.MkdirAll(project, 0o700); err != nil {
		t.Fatalf("plant a project directory: %v", err)
	}
	for name, at := range files {
		path := filepath.Join(project, name)
		if err := os.WriteFile(path, []byte("{}\n"), 0o600); err != nil {
			t.Fatalf("plant %s: %v", name, err)
		}
		if !at.IsZero() {
			if err := os.Chtimes(path, at, at); err != nil {
				t.Fatalf("date %s: %v", name, err)
			}
		}
	}
}

// The listing an operator picks from: newest first, identifiers only, and
// nothing that is not a conversation.
//
// **Must fail when** the ordering stops being by recency, or an entry that is
// not a conversation transcript is offered as one.
func TestConversationsListsTranscriptsNewestFirst(t *testing.T) {
	f := newManagerFixture(t)
	dir := f.repo()

	old := time.Date(2026, 8, 10, 9, 0, 0, 0, time.UTC)
	mid := time.Date(2026, 8, 12, 9, 0, 0, 0, time.UTC)
	recent := time.Date(2026, 8, 14, 9, 0, 0, 0, time.UTC)

	conversationHome(t, dir, map[string]time.Time{
		"11111111-1111-1111-1111-111111111111.jsonl": old,
		"22222222-2222-2222-2222-222222222222.jsonl": recent,
		"33333333-3333-3333-3333-333333333333.jsonl": mid,
		// Not conversations, and each is a real thing that sits in that
		// directory: a sidecar Claude writes, and a name that is not an
		// identifier at all.
		"44444444-4444-4444-4444-444444444444.ccr-tip.json": mid,
		"notes.jsonl": mid,
		".jsonl":      mid,
	})

	got := f.mgr.Conversations(dir)

	want := []string{
		"22222222-2222-2222-2222-222222222222",
		"33333333-3333-3333-3333-333333333333",
		"11111111-1111-1111-1111-111111111111",
	}
	if len(got) != len(want) {
		t.Fatalf("Conversations() returned %d entries, want %d: %v", len(got), len(want), got)
	}
	for i, id := range want {
		if got[i].ID != id {
			t.Errorf("Conversations()[%d] = %s, want %s — newest first", i, got[i].ID, id)
		}
	}
	if !got[0].Modified.Equal(recent) {
		t.Errorf("Conversations()[0].Modified = %v, want the file's own mtime %v", got[0].Modified, recent)
	}
}

// No transcript is ever opened (FR-025), asserted the only way that can be:
// against a file the process cannot read. A listing that parsed transcripts
// would fail on this directory; one that reads entries does not notice.
//
// **Must fail when** the listing opens a transcript for a title, a summary, or a
// size.
func TestConversationsOpensNoTranscript(t *testing.T) {
	f := newManagerFixture(t)
	dir := f.repo()

	const id = "88e5294c-d947-4527-b8c9-5eb8384bae6a"
	conversationHome(t, dir, map[string]time.Time{id + ".jsonl": time.Now()})

	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("UserHomeDir: %v", err)
	}
	unreadable := filepath.Join(home, ".claude", "projects", projectDirFor(dir), id+".jsonl")
	if err := os.Chmod(unreadable, 0o000); err != nil {
		t.Fatalf("make the transcript unreadable: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(unreadable, 0o600) }) //nolint:errcheck,gosec // best-effort restore so t.TempDir can clean up; a failure here fails nothing under test.

	got := f.mgr.Conversations(dir)
	if len(got) != 1 || got[0].ID != id {
		t.Fatalf("Conversations() = %v; want the one conversation, listed from its directory entry alone", got)
	}
}

// Every failure is an empty list and never an error (FR-021b). A create form
// that would not render because somebody else's release moved a directory is
// this daemon broken by a change it has no part in.
func TestConversationsAnswersNothingRatherThanFailing(t *testing.T) {
	for name, arrange := range map[string]func(t *testing.T, f managerFixture) string{
		"no history for this directory": func(t *testing.T, f managerFixture) string {
			conversationHome(t, f.repo()+"-somewhere-else", nil)
			return f.repo()
		},
		"no .claude directory at all": func(t *testing.T, _ managerFixture) string {
			t.Setenv("HOME", t.TempDir())
			return ""
		},
		"a home that does not exist": func(t *testing.T, _ managerFixture) string {
			t.Setenv("HOME", filepath.Join(t.TempDir(), "gone"))
			return ""
		},
	} {
		t.Run(name, func(t *testing.T) {
			f := newManagerFixture(t)
			dir := arrange(t, f)
			if dir == "" {
				dir = f.repo()
			}

			if got := f.mgr.Conversations(dir); len(got) != 0 {
				t.Errorf("Conversations() = %v; want nothing at all", got)
			}
		})
	}
}

// The allowlist bounds the disclosure (FR-021a, FR-022). A directory the operator
// could not create a session in is one whose conversations cannot be listed, and
// the check happens *before* the path is derived — so the daemon does not learn
// whether a forbidden directory has a history either.
//
// **Must fail when** the working directory reaches the filesystem without passing
// ResolveWorkDir.
func TestConversationsRefusesADirectoryOutsideTheRoots(t *testing.T) {
	f := newManagerFixture(t)

	outside := t.TempDir()
	// A real history for it, so a listing that skipped the allowlist would have
	// something to find and this test would notice.
	conversationHome(t, outside, map[string]time.Time{
		"88e5294c-d947-4527-b8c9-5eb8384bae6a.jsonl": time.Now(),
	})

	for _, dir := range []string{
		outside,
		f.repo() + "/../../etc",
		"relative/path",
		"",
	} {
		if got := f.mgr.Conversations(dir); len(got) != 0 {
			t.Errorf("Conversations(%q) = %v; a directory outside the roots discloses nothing", dir, got)
		}
	}
}

// The encoding, in the one direction the daemon uses it.
//
// It is lossy and that is recorded rather than fixed: a directory named `a-b`
// and the path `a/b` encode identically. It costs nothing because the daemon
// never reads a directory name as a path, and a collision would at worst offer a
// neighbouring directory's conversations to an operator who can already create a
// session in both.
func TestProjectDirForMatchesClaudesLayout(t *testing.T) {
	t.Parallel()

	for in, want := range map[string]string{
		"/home/nctiggy/code/customer-opportunities": "-home-nctiggy-code-customer-opportunities",
		"/home/nctiggy":            "-home-nctiggy",
		"/home/a/b.c":              "-home-a-b-c",
		"/":                        "-",
		"/home/dotted.dir/project": "-home-dotted-dir-project",
	} {
		if got := projectDirFor(in); got != want {
			t.Errorf("projectDirFor(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestHasTranscript is the check that stops the supervisor resuming an
// identifier with nothing behind it (FR-014).
func TestHasTranscript(t *testing.T) {
	const present = "7f3a1b2c-4d5e-4f60-8a71-b2c3d4e5f607"
	const absent = "00000000-0000-4000-8000-000000000000"

	f := newManagerFixture(t)
	conversationHome(t, f.repo(), map[string]time.Time{present + ".jsonl": {}})

	tests := []struct {
		name           string
		conversationID string
		workDir        string
		want           bool
	}{
		{"a transcript that is there", present, f.repo(), true},
		{"a transcript that is not", absent, f.repo(), false},
		{"a session that never had one", "", f.repo(), false},
		{"an identifier that is not one", "../../etc/passwd", f.repo(), false},
		{"a directory outside the allowlist", present, "/not/allowlisted", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := f.mgr.HasTranscript(tt.conversationID, tt.workDir); got != tt.want {
				t.Errorf("HasTranscript(%q, %q) = %v, want %v", tt.conversationID, tt.workDir, got, tt.want)
			}
		})
	}
}

// TestStartBinary fixes what goes into @crswd-binary, which tmux compares the
// pane against. An alphabet outside this set would put a value on a row whose
// fields are separated by "|", so anything unexpected yields "" — read
// everywhere as "no expectation recorded", and therefore as alive.
func TestStartBinary(t *testing.T) {
	t.Parallel()

	tests := []struct{ command, want string }{
		{"claude --dangerously-skip-permissions", "claude"},
		{"/home/op/.local/bin/claude --foo", "claude"},
		{"  claude  --foo", "claude"},
		{"claude", "claude"},
		{"my-tool_v2.1 --x", "my-tool_v2.1"},
		{"", ""},
		{"   ", ""},
		{"bad|name --x", ""},
		// The first whitespace-separated token is the binary, which is the same
		// split the shell makes of the same line — so this is "has", not a
		// failure. config.InsertStartFlags documents that contract; this follows
		// it rather than inventing a second one.
		{"has space/", "has"},
		{"/ --x", ""},
		{"./ --x", ""},
	}
	for _, tt := range tests {
		if got := startBinary(tt.command); got != tt.want {
			t.Errorf("startBinary(%q) = %q, want %q", tt.command, got, tt.want)
		}
	}
}

// TestProjectSegmentCannotEscape is the check projectSegment exists to make
// legible: whatever a caller spells, what reaches the filesystem is one path
// element. ResolveWorkDir has already refused anything outside the allowlist by
// the time a directory gets here, so these are inputs the guard should never
// see — which is exactly why it is worth proving it holds for them.
func TestProjectSegmentCannotEscape(t *testing.T) {
	t.Parallel()

	for _, workDir := range []string{
		"/code/repo",
		"/../../etc",
		"../../../etc/passwd",
		"/",
		"",
		".",
		"..",
		"/code/../../..",
		"/code/repo/",
	} {
		t.Run(workDir, func(t *testing.T) {
			t.Parallel()

			segment, ok := projectSegment(workDir)
			if !ok {
				return
			}
			if segment != filepath.Base(segment) {
				t.Errorf("projectSegment(%q) = %q, which is not a single path element", workDir, segment)
			}
			if strings.ContainsRune(segment, filepath.Separator) {
				t.Errorf("projectSegment(%q) = %q, which carries a separator", workDir, segment)
			}
			if segment == ".." || strings.HasPrefix(segment, "..") && !strings.HasPrefix(segment, "---") {
				// projectDirFor maps '.' to '-', so a traversal cannot survive it
				// as dots. Anything that still begins with one has not been through it.
				t.Errorf("projectSegment(%q) = %q, which reads as a traversal", workDir, segment)
			}
		})
	}
}
