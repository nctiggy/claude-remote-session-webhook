// The journal is the daemon's only durable memory, and every case below is a
// way it could lose or corrupt what it was told. The awkward ones — a
// half-written final line, a file from a newer build — are the ordinary
// consequences of an unclean stop, which is the event this file exists for.
package session

import (
	"context"
	"encoding/json"

	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nctiggy/claude-remote-session-webhook/internal/config"
)

func tempJournal(t *testing.T) *Journal {
	t.Helper()
	return NewJournal(filepath.Join(t.TempDir(), "sessions.jsonl"))
}

func TestJournalRoundTrip(t *testing.T) {
	t.Parallel()

	j := tempJournal(t)
	want := journalRecord{
		At:           time.Unix(1785706480, 0).UTC(),
		ID:           "abc123",
		Event:        journalCreated,
		Owner:        "operator",
		Conversation: "7f3a1b2c-4d5e-4f60-8a71-b2c3d4e5f607",
		WorkDir:      "/code/repo",
		Start:        "rc",
		Lifetime:     "never",
		Created:      time.Unix(1785706480, 0).UTC(),
	}
	if err := j.Append(want); err != nil {
		t.Fatalf("Append() = %v", err)
	}

	got, stats, err := j.Replay()
	if err != nil {
		t.Fatalf("Replay() = %v", err)
	}
	if stats.Records != 1 || stats.Discarded != 0 {
		t.Fatalf("stats = %+v, want 1 record and nothing discarded", stats)
	}
	if len(got) != 1 {
		t.Fatalf("replayed %d records, want 1", len(got))
	}
	want.V = journalVersion
	if got[0] != want {
		t.Errorf("replayed %+v, want %+v", got[0], want)
	}
}

func TestJournalLastRecordWins(t *testing.T) {
	t.Parallel()

	j := tempJournal(t)
	for _, ev := range []string{journalCreated, journalRevived, journalFailed} {
		if err := j.Append(journalRecord{ID: "abc123", Event: ev, Attempts: len(ev)}); err != nil {
			t.Fatalf("Append(%s) = %v", ev, err)
		}
	}

	got, _, err := j.Replay()
	if err != nil {
		t.Fatalf("Replay() = %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("replayed %d records for one session, want 1", len(got))
	}
	if got[0].Event != journalFailed {
		t.Errorf("replayed event %q, want the last one written (%q)", got[0].Event, journalFailed)
	}
}

// TestJournalDiscardsATruncatedFinalLine is the whole reason this file is an
// append-only log rather than a rewritten document. An unclean stop leaves the
// last write half-finished; losing that one record is survivable, and losing the
// file is not.
func TestJournalDiscardsATruncatedFinalLine(t *testing.T) {
	t.Parallel()

	j := tempJournal(t)
	if err := j.Append(journalRecord{ID: "keepme", Event: journalCreated}); err != nil {
		t.Fatalf("Append() = %v", err)
	}
	f, err := os.OpenFile(j.Path(), os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatalf("open the journal: %v", err)
	}
	if _, err := f.WriteString(`{"v":1,"id":"halfwrit`); err != nil {
		t.Fatalf("write a partial record: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close the journal: %v", err)
	}

	got, stats, err := j.Replay()
	if err != nil {
		t.Fatalf("Replay() after a torn write = %v; a torn final line must not be fatal", err)
	}
	if stats.Discarded != 1 {
		t.Errorf("stats.Discarded = %d, want 1", stats.Discarded)
	}
	if len(got) != 1 || got[0].ID != "keepme" {
		t.Errorf("replayed %+v, want only the intact record", got)
	}
}

func TestJournalSkipsANewerVersion(t *testing.T) {
	t.Parallel()

	j := tempJournal(t)
	if err := j.Append(journalRecord{ID: "known", Event: journalCreated}); err != nil {
		t.Fatalf("Append() = %v", err)
	}
	f, err := os.OpenFile(j.Path(), os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatalf("open the journal: %v", err)
	}
	if _, err := f.WriteString(`{"v":99,"id":"fromthefuture","event":"created"}` + "\n"); err != nil {
		t.Fatalf("write a future record: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close the journal: %v", err)
	}

	got, stats, err := j.Replay()
	if err != nil {
		t.Fatalf("Replay() = %v; a newer record must not stop a daemon starting", err)
	}
	if stats.SkippedVersion != 1 {
		t.Errorf("stats.SkippedVersion = %d, want 1", stats.SkippedVersion)
	}
	if len(got) != 1 || got[0].ID != "known" {
		t.Errorf("replayed %+v, want only the record this version understands", got)
	}
}

func TestJournalMissingFileIsNotAnError(t *testing.T) {
	t.Parallel()

	got, stats, err := tempJournal(t).Replay()
	if err != nil {
		t.Fatalf("Replay() on a host with no journal = %v; that is a host where nothing has been created yet", err)
	}
	if len(got) != 0 || stats.Records != 0 {
		t.Errorf("replayed %+v / %+v, want nothing", got, stats)
	}
}

// TestJournalUnreadableFileIsFatal is the other half of the case above. A
// missing journal means "nothing created yet"; one that cannot be read means the
// daemon does not know what it is responsible for, and continuing quietly is the
// invisible failure spec 012 exists to remove.
func TestJournalUnreadableFileIsFatal(t *testing.T) {
	t.Parallel()

	if os.Geteuid() == 0 {
		t.Skip("root reads unreadable files; the permission this asserts does not apply")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "sessions.jsonl")
	if err := os.WriteFile(path, []byte("{}\n"), 0o000); err != nil {
		t.Fatalf("write an unreadable journal: %v", err)
	}
	if _, _, err := NewJournal(path).Replay(); err == nil {
		t.Fatal("Replay() on an unreadable journal returned no error")
	}
}

func TestJournalWithNoPathKeepsNothing(t *testing.T) {
	t.Parallel()

	j := NewJournal("")
	if err := j.Append(journalRecord{ID: "abc", Event: journalCreated}); err != nil {
		t.Fatalf("Append() to a journal with no path = %v; that is a container with no home, not a failure", err)
	}
	got, _, err := j.Replay()
	if err != nil || len(got) != 0 {
		t.Errorf("Replay() = %+v, %v; want nothing and no error", got, err)
	}
}

// TestJournalIsPrivate and the two below are the security properties. The file
// sits in the operator's configuration directory and holds the identity of every
// session on the host.
func TestJournalIsPrivate(t *testing.T) {
	t.Parallel()

	j := tempJournal(t)
	if err := j.Append(journalRecord{ID: "abc", Event: journalCreated}); err != nil {
		t.Fatalf("Append() = %v", err)
	}
	info, err := os.Stat(j.Path())
	if err != nil {
		t.Fatalf("stat the journal: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("journal mode is %o, want 600", perm)
	}
}

// TestJournalCarriesNoSecret is FR-022 applied to a file. A record is written
// with every field this daemon has, and the encoding must contain none of the
// things that may never leave it.
func TestJournalCarriesNoSecret(t *testing.T) {
	t.Parallel()

	rec := journalRecord{
		V: journalVersion, ID: "abc", Event: journalCreated, Owner: "operator",
		Conversation: "7f3a1b2c-4d5e-4f60-8a71-b2c3d4e5f607", WorkDir: "/code/repo",
		Start: "rc", Lifetime: "never", Attempts: 2,
	}
	encoded, err := json.Marshal(rec)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	for _, forbidden := range []string{"token", "Token", "hash", "Hash", "secret", "pane", "credential"} {
		if strings.Contains(string(encoded), forbidden) {
			t.Errorf("a journal record carries %q: %s", forbidden, encoded)
		}
	}
}

// TestJournalRefusesANewline guards the format itself: a record carrying one
// would end its line early and make the next read discard the remainder.
func TestJournalRefusesANewline(t *testing.T) {
	t.Parallel()

	j := tempJournal(t)
	// Marshal escapes newlines, so this passes today and the guard is for a
	// future field that does not go through Marshal. Asserting it now is what
	// makes that guard a promise rather than a comment.
	if err := j.Append(journalRecord{ID: "abc", Event: journalCreated, WorkDir: "/code/re\npo"}); err != nil {
		t.Fatalf("Append() = %v", err)
	}
	got, stats, err := j.Replay()
	if err != nil {
		t.Fatalf("Replay() = %v", err)
	}
	if stats.Discarded != 0 || len(got) != 1 {
		t.Fatalf("a newline in a field broke the file: %+v / %+v", got, stats)
	}
	if got[0].WorkDir != "/code/re\npo" {
		t.Errorf("WorkDir came back %q, want it intact", got[0].WorkDir)
	}
}

// The replay tests below are the other half of US2. A journal outlives the
// configuration that produced it, so an allowlist that shrank, a ceiling that
// narrowed, or a fleet already at its cap must all shrink what comes back — or
// the journal becomes a way to reinstate a session the operator's current
// configuration would refuse to create.

// restarted rebuilds the manager against the same journal and host, which is
// what a daemon restart looks like from the store's point of view: everything
// the process remembered is gone, and the file and the host are all that is left.
func restarted(t *testing.T, f *managerFixture, j *Journal, roots []config.ApprovedRoot, now time.Time) {
	t.Helper()

	f.store = NewStore()
	mgr, err := NewManagerWithClock(f.tmux, f.store, roots, capNotUnderTest, stoppedClock{now: now})
	if err != nil {
		t.Fatalf("NewManagerWithClock() = %v", err)
	}
	mgr.SetJournal(j)
	f.mgr = mgr
}

func TestReplaySkipsASessionTheOperatorEnded(t *testing.T) {
	f := newManagerFixture(t)
	j := tempJournal(t)
	f.mgr.SetJournal(j)

	s := revivableSession(t, f)
	if err := f.mgr.Destroy(context.Background(), s); err != nil {
		t.Fatalf("Destroy() = %v", err)
	}
	// The daemon has restarted and remembers nothing.
	restarted(t, &f, j, f.roots(), f.now)

	got, _, err := f.mgr.ReplayJournal(context.Background())
	if err != nil {
		t.Fatalf("ReplayJournal() = %v", err)
	}
	for _, r := range got {
		if r.Session.ID == s.ID && r.Reason == "" {
			t.Error("a destroyed session was put back by a replay; destroy must survive a restart")
		}
	}
	if _, ok := f.store.lookup(s.ID); ok {
		t.Error("a destroyed session is in the store after a replay")
	}
}

func TestReplayRefusesADeAllowlistedDirectory(t *testing.T) {
	f := newManagerFixture(t)
	j := tempJournal(t)
	f.mgr.SetJournal(j)
	s := revivableSession(t, f)
	f.tmux.Vanish(s.TmuxName())

	// The allowlist shrank while the daemon was down: it now names a different
	// directory, so the session's own is outside it.
	restarted(t, &f, j, []config.ApprovedRoot{{Path: t.TempDir()}}, f.now)

	got, _, err := f.mgr.ReplayJournal(context.Background())
	if err != nil {
		t.Fatalf("ReplayJournal() = %v", err)
	}
	if len(got) != 1 || got[0].Reason == "" {
		t.Fatalf("replayed %+v, want the session refused with a reason", got)
	}
	if _, ok := f.store.lookup(s.ID); ok {
		t.Error("a session outside the current allowlist was put back into the store")
	}
}

func TestReplayRefusesAnExpiredSession(t *testing.T) {
	f := newManagerFixture(t)
	j := tempJournal(t)
	f.mgr.SetJournal(j)
	s := revivableSession(t, f)
	f.tmux.Vanish(s.TmuxName())

	// The daemon came back long after this session's ceiling.
	restarted(t, &f, j, f.roots(), s.AbsoluteDeadline().Add(time.Hour))

	got, _, err := f.mgr.ReplayJournal(context.Background())
	if err != nil {
		t.Fatalf("ReplayJournal() = %v", err)
	}
	if len(got) != 1 || got[0].Reason == "" {
		t.Fatalf("replayed %+v, want the session refused with a reason", got)
	}
	if _, ok := f.store.lookup(s.ID); ok {
		t.Error("a session past its absolute deadline was put back; revival must never outlive a ceiling")
	}
}

// TestReplayLeavesALiveSessionToAdopt is the ordering contract: the host is the
// authority on what is running, the journal on what should be.
func TestReplayLeavesALiveSessionToAdopt(t *testing.T) {
	f := newManagerFixture(t)
	j := tempJournal(t)
	f.mgr.SetJournal(j)
	revivableSession(t, f)
	// Still on the host — the daemon restarted, the session did not.

	restarted(t, &f, j, f.roots(), f.now)

	got, _, err := f.mgr.ReplayJournal(context.Background())
	if err != nil {
		t.Fatalf("ReplayJournal() = %v", err)
	}
	if len(got) != 0 {
		t.Errorf("replay claimed %+v; a session tmux still has belongs to Adopt", got)
	}
}

// TestReplayCarriesTheOriginalCreation is FR-010 across a restart: a session
// that comes back comes back as old as it was.
func TestReplayCarriesTheOriginalCreation(t *testing.T) {
	f := newManagerFixture(t)
	j := tempJournal(t)
	f.mgr.SetJournal(j)
	s := revivableSession(t, f)
	f.tmux.Vanish(s.TmuxName())

	restarted(t, &f, j, f.roots(), f.now.Add(time.Minute))

	if _, _, err := f.mgr.ReplayJournal(context.Background()); err != nil {
		t.Fatalf("ReplayJournal() = %v", err)
	}
	got, ok := f.store.lookup(s.ID)
	if !ok {
		t.Fatal("the session was not put back")
	}
	if !got.CreatedAt.Equal(s.CreatedAt) {
		t.Errorf("CreatedAt = %v, want the original %v — a replay must not restart a deadline", got.CreatedAt, s.CreatedAt)
	}
	if got.ConversationID != s.ConversationID {
		t.Errorf("ConversationID = %q, want %q", got.ConversationID, s.ConversationID)
	}
	if !got.CredentialPending {
		t.Error("a replayed session must have no usable credential until one is claimed")
	}
}
