// Internal test, matching the rest of the package. Every case drives the real
// router and the real browser door, because what is being asserted is a route's
// behaviour — including that it has no way of saying "no".
package httpapi

import (
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nctiggy/claude-remote-session-webhook/internal/config"
)

// conversationsPath is derived from the pattern the server registers rather than
// spelled again, for the reason versionPath is: a renamed route would otherwise
// leave every test here passing against a path nothing claims.
var conversationsPath = strings.TrimPrefix(patternConversations, http.MethodGet+" ")

// plantConversations gives the host a real conversation store for a directory.
// It is serial, because it describes a home directory and the store's location
// comes from the environment Claude Code itself reads.
func plantConversations(t *testing.T, workDir string, ids ...string) {
	t.Helper()

	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := filepath.Join(home, ".claude", "projects", strings.ReplaceAll(workDir, "/", "-"))
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatalf("create the conversation store: %v", err)
	}
	for _, id := range ids {
		if err := os.WriteFile(filepath.Join(dir, id+".jsonl"), []byte("{}\n"), 0o600); err != nil {
			t.Fatalf("record a conversation: %v", err)
		}
	}
}

func askConversations(t *testing.T, f *fleet, dir string) conversationsResponse {
	t.Helper()

	w := f.open(t, conversationsPath+"?"+url.Values{queryDir: {dir}}.Encode())
	if w.Code != http.StatusOK {
		t.Fatalf("GET %s = %d (%s); want %d", conversationsPath, w.Code, w.Body.String(), http.StatusOK)
	}
	var got conversationsResponse
	dec := json.NewDecoder(strings.NewReader(w.Body.String()))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&got); err != nil {
		t.Fatalf("the route answered %q, which is not a conversations response: %v", w.Body.String(), err)
	}
	return got
}

// TestConversationsAnswersForTheDirectoryAsked is the defect spec 012 fixes: the
// list used to belong to whichever directory the form suggested at render, so an
// operator who chose a different one was offered somebody else's work.
func TestConversationsAnswersForTheDirectoryAsked(t *testing.T) {
	const first = "8f14e45f-ceea-467a-9b3d-0f2fc9de5b21"

	f := newFleet(t)
	f.cfg.Roots = []config.ApprovedRoot{{Path: f.fixture.root}}
	plantConversations(t, f.fixture.repo, first)

	got := askConversations(t, f, f.fixture.repo)
	if len(got.Conversations) != 1 || got.Conversations[0].ID != first {
		t.Fatalf("conversations = %+v, want the one recorded for that directory", got.Conversations)
	}
	if got.Conversations[0].Modified == "" {
		t.Error("a conversation was offered with no indication of when it was last written")
	}
}

// TestConversationsRefusesNothing is the route's whole contract. A directory
// outside the allowlist, one that does not exist, and one with no history are
// one answer, so the route cannot be used to ask which directories exist — and
// a form whose list failed to load still works.
func TestConversationsRefusesNothing(t *testing.T) {
	f := newFleet(t)
	f.cfg.Roots = []config.ApprovedRoot{{Path: f.fixture.root}}
	plantConversations(t, f.fixture.repo, "8f14e45f-ceea-467a-9b3d-0f2fc9de5b21")

	for _, dir := range []string{
		"/not/allowlisted",
		"/etc",
		filepath.Join(f.fixture.root, "does-not-exist"),
		"../../etc/passwd",
		"",
		"relative/path",
	} {
		t.Run(dir, func(t *testing.T) {
			got := askConversations(t, f, dir)
			if len(got.Conversations) != 0 {
				t.Errorf("GET %s?dir=%s offered %+v; every refusal is an empty list", conversationsPath, dir, got.Conversations)
			}
		})
	}
}

// TestConversationsOpensNoTranscript is FR-025. What a conversation said is the
// operator's work; this route may disclose that one happened and when, and
// nothing else.
func TestConversationsOpensNoTranscript(t *testing.T) {
	const id = "8f14e45f-ceea-467a-9b3d-0f2fc9de5b21"
	const secret = "THE-CONTENTS-OF-THE-TRANSCRIPT"

	f := newFleet(t)
	f.cfg.Roots = []config.ApprovedRoot{{Path: f.fixture.root}}

	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := filepath.Join(home, ".claude", "projects", strings.ReplaceAll(f.fixture.repo, "/", "-"))
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatalf("create the conversation store: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, id+".jsonl"), []byte(`{"text":"`+secret+`"}`+"\n"), 0o600); err != nil {
		t.Fatalf("record a conversation: %v", err)
	}

	w := f.open(t, conversationsPath+"?"+url.Values{queryDir: {f.fixture.repo}}.Encode())
	if strings.Contains(w.Body.String(), secret) {
		t.Errorf("the route disclosed transcript content:\n%s", w.Body.String())
	}
}

// TestConversationsNeedsTheBrowserDoor: it is a read behind the same door as
// every other dashboard route, and an unauthenticated caller learns nothing.
func TestConversationsNeedsTheBrowserDoor(t *testing.T) {
	f := newFleet(t)

	// `absent` sends no assertion at all, which is the door's own distinction.
	w := f.openWith(t, conversationsPath+"?dir=/tmp", absent)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("GET %s with no identity = %d; want %d — a read outside the door is still a read", conversationsPath, w.Code, http.StatusUnauthorized)
	}
}
