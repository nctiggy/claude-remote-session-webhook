// Internal test, matching name_test.go and workdir_test.go. The store's
// invariants are what this file is about, and several of them are only visible
// from inside the package — that a rejected lookup returns the zero record, and
// that no field carries an expiry the derived one could drift from.
package session

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nctiggy/claude-remote-session-webhook/internal/auth"
	"github.com/nctiggy/claude-remote-session-webhook/internal/config"
)

// The instants from contracts/http-api.md's create response, transcribed rather
// than computed, so the 24-hour rule is checked against the document a client
// reads and not against the constant the code already uses.
var (
	contractCreatedAt = time.Date(2026, 8, 2, 21, 36, 58, 0, time.UTC)
	contractExpiresAt = time.Date(2026, 8, 3, 21, 36, 58, 0, time.UTC)
)

// A synthetic second identity. Production has exactly one caller (data-model.md
// CallerID), so the cross-owner path only exists to be tested — and it is the
// path FR-032 and FR-033 are about.
const otherOwner auth.CallerID = "someone-else"

// testID builds an ID-shaped value without putting a 32-character hex string in
// the source. gitleaks reads one as a credential and blocks the commit
// (iteration 8's finding), and this file needs several distinct ones.
func testID(ch string) string {
	return strings.Repeat(ch, IDLen)
}

// newTestSession is the minimum valid record: anything a test wants to vary, it
// varies on the returned copy.
func newTestSession(id string, owner auth.CallerID) Session {
	return Session{
		ID:           id,
		Owner:        owner,
		Name:         "refactor-auth",
		WorkDir:      "/home/u/code/repo",
		CreatedAt:    contractCreatedAt,
		LastActivity: contractCreatedAt,
		State:        StateStarting,
	}
}

func mustAdd(t *testing.T, st *Store, s Session) {
	t.Helper()

	if err := st.Add(s); err != nil {
		t.Fatalf("Add(%q) unexpected error: %v", s.ID, err)
	}
}

// storeWith returns a store holding one session owned by CallerOperator.
func storeWith(t *testing.T, s Session) *Store {
	t.Helper()

	st := NewStore()
	mustAdd(t, st, s)
	return st
}

func TestLifetimesMatchTheContract(t *testing.T) {
	t.Parallel()

	// docs/auth-and-sessions.md's lifetimes table, transcribed. This is the
	// number Principle VI bounds the blast radius with, and since milestone 15
	// it is the only one — the idle timeout that stood beside it was withdrawn
	// with constitution 2.0.0.
	if got, want := AbsoluteLifetime, 24*time.Hour; got != want {
		t.Errorf("AbsoluteLifetime = %v, want %v", got, want)
	}
}

// FR-034: there is no path from a caller-supplied string to a tmux target. The
// hostile values here would never survive ValidateName, which is the point —
// even if one did, it reaches no target.
func TestSessionDerivesEveryTargetFromTheIDAlone(t *testing.T) {
	t.Parallel()

	id := testID("a")
	base := newTestSession(id, auth.CallerOperator)

	tests := []struct {
		name    string
		mutate  func(*Session)
		hostile string
	}{
		{name: "an unchanged record", mutate: func(*Session) {}},
		{
			name:    "a name carrying tmux window syntax",
			mutate:  func(s *Session) { s.Name = "decoy:1" },
			hostile: "decoy",
		},
		{
			name:    "a name carrying tmux pane syntax",
			mutate:  func(s *Session) { s.Name = "decoy.0" },
			hostile: "decoy",
		},
		{
			name:    "a working directory",
			mutate:  func(s *Session) { s.WorkDir = "/home/u/code/decoydir" },
			hostile: "decoydir",
		},
		{
			name:    "an owner",
			mutate:  func(s *Session) { s.Owner = "decoyowner" },
			hostile: "decoyowner",
		},
		{
			name:   "an adopted record",
			mutate: func(s *Session) { s.Adopted = true },
		},
		{
			name:   "a dead record",
			mutate: func(s *Session) { s.State = StateDead },
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			s := base
			tc.mutate(&s)

			if got, want := s.TmuxName(), "crswd-"+id; got != want {
				t.Errorf("TmuxName() = %q, want %q", got, want)
			}
			if tc.hostile == "" {
				return
			}
			for label, target := range map[string]string{
				"TmuxName":      s.TmuxName(),
				"SessionTarget": s.SessionTarget(),
				"PaneTarget":    s.PaneTarget(),
			} {
				if strings.Contains(target, tc.hostile) {
					t.Errorf("%s() = %q, contains caller-supplied %q", label, target, tc.hostile)
				}
			}
		})
	}
}

// The two target syntaxes are not interchangeable (research D2), and a session
// method that returned the wrong one would fail at runtime rather than compile.
func TestSessionTargetsUseTmuxTargetSyntax(t *testing.T) {
	t.Parallel()

	id := testID("b")
	s := newTestSession(id, auth.CallerOperator)

	if got, want := s.SessionTarget(), "=crswd-"+id; got != want {
		t.Errorf("SessionTarget() = %q, want %q", got, want)
	}
	if got, want := s.PaneTarget(), "=crswd-"+id+":"; got != want {
		t.Errorf("PaneTarget() = %q, want %q", got, want)
	}
	if s.SessionTarget() == s.PaneTarget() {
		t.Error("SessionTarget() and PaneTarget() returned the same string")
	}
}

// FR-015 and the operator's Q2 ruling: the credential's life and the session's
// are one number, so neither can be shortened without the other.
func TestTokenExpiryEqualsTheAbsoluteDeadline(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		createdAt time.Time
		want      time.Time
	}{
		{
			name:      "the instants in the contract's create response",
			createdAt: contractCreatedAt,
			want:      contractExpiresAt,
		},
		{
			name:      "a session created at a DST boundary",
			createdAt: time.Date(2026, 3, 29, 0, 30, 0, 0, time.UTC),
			want:      time.Date(2026, 3, 30, 0, 30, 0, 0, time.UTC),
		},
		{
			name:      "sub-second precision is carried, not truncated",
			createdAt: contractCreatedAt.Add(1 * time.Nanosecond),
			want:      contractExpiresAt.Add(1 * time.Nanosecond),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			s := newTestSession(testID("c"), auth.CallerOperator)
			s.CreatedAt = tc.createdAt

			if got := s.AbsoluteDeadline(); !got.Equal(tc.want) {
				t.Errorf("AbsoluteDeadline() = %v, want %v", got, tc.want)
			}
			if got := s.TokenExpiry(); !got.Equal(tc.want) {
				t.Errorf("TokenExpiry() = %v, want %v", got, tc.want)
			}
			if !s.TokenExpiry().Equal(s.AbsoluteDeadline()) {
				t.Errorf("TokenExpiry() = %v and AbsoluteDeadline() = %v disagree",
					s.TokenExpiry(), s.AbsoluteDeadline())
			}
		})
	}
}

// "Derived, not stored" is a claim about the struct, so it is checked against
// the struct. A future field named ExpiresAt would be a second copy of a number
// that must never disagree with the first.
func TestSessionStoresNoExpiryField(t *testing.T) {
	t.Parallel()

	forbidden := []string{"expir", "deadline", "ttl"}

	rt := reflect.TypeOf(Session{})
	for i := 0; i < rt.NumField(); i++ {
		lower := strings.ToLower(rt.Field(i).Name)
		for _, word := range forbidden {
			if strings.Contains(lower, word) {
				t.Errorf("Session has field %q; expiry is derived from CreatedAt, never stored", rt.Field(i).Name)
			}
		}
	}

	// The other half of the claim: it is available, just not as a field.
	for _, method := range []string{"TokenExpiry", "AbsoluteDeadline"} {
		if _, ok := reflect.TypeOf(Session{}).MethodByName(method); !ok {
			t.Errorf("Session has no %s method", method)
		}
	}
}

// FR-042 forbids the token hash in anything that leaves the daemon. Session is a
// domain type no handler should marshal, so this asserts the guard that holds
// when one does anyway.
func TestSessionKeepsTheTokenHashOutOfJSON(t *testing.T) {
	t.Parallel()

	// A byte value that is distinctive as decimal text: a [32]byte marshals as
	// an array of numbers, so a leak would read as "171,171,...".
	const marker = 171

	s := newTestSession(testID("e"), auth.CallerOperator)
	for i := range s.TokenHash {
		s.TokenHash[i] = marker
	}

	encoded, err := json.Marshal(s)
	if err != nil {
		t.Fatalf("json.Marshal(Session) unexpected error: %v", err)
	}
	out := string(encoded)

	if strings.Contains(out, fmt.Sprint(marker)) {
		t.Errorf("marshalled Session carries the token hash bytes: %s", out)
	}
	if strings.Contains(strings.ToLower(out), "token") {
		t.Errorf("marshalled Session mentions a token: %s", out)
	}

	// The rule rather than this one field: anything token-shaped added later is
	// excluded too, or this fails.
	rt := reflect.TypeOf(Session{})
	for i := 0; i < rt.NumField(); i++ {
		f := rt.Field(i)
		if !strings.Contains(strings.ToLower(f.Name), "token") {
			continue
		}
		if f.Tag.Get("json") != "-" {
			t.Errorf(`Session.%s is token-shaped and lacks json:"-"`, f.Name)
		}
	}
}

func TestStateValid(t *testing.T) {
	t.Parallel()

	tests := []struct {
		in   State
		want bool
	}{
		{in: StateStarting, want: true},
		{in: StateRunning, want: true},
		{in: StateDead, want: true},
		{in: "", want: false},
		{in: "needs-auth", want: false}, // milestone 4, deliberately not yet
		{in: "Starting", want: false},
		{in: "starting ", want: false},
	}

	for _, tc := range tests {
		t.Run(fmt.Sprintf("%q", tc.in), func(t *testing.T) {
			t.Parallel()

			if got := tc.in.Valid(); got != tc.want {
				t.Errorf("State(%q).Valid() = %v, want %v", tc.in, got, tc.want)
			}
		})
	}

	// The wire values in contracts/http-api.md, transcribed.
	for state, want := range map[State]string{
		StateStarting: "starting",
		StateRunning:  "running",
		StateDead:     "dead",
	} {
		if string(state) != want {
			t.Errorf("state constant = %q, want %q", string(state), want)
		}
	}
}

// FR-019a: the stored lifecycle field takes no part in the derivation. StateDead
// is included precisely because it has no production caller — a switch on State
// is the implementation this forbids, and it would pass every other test here.
func TestDisplayStateIgnoresTheStoredLifecycleField(t *testing.T) {
	t.Parallel()

	base := newTestSession(testID("3"), auth.CallerOperator)

	tests := []struct {
		name string
		now  time.Time
		want DisplayState
	}{
		{name: "inside the lifetime", now: base.AbsoluteDeadline().Add(-time.Minute), want: DisplayRunning},
		// Past the ceiling still reads running, and that is not a gap. A record
		// this far along is one the reaper has already dropped, so no card is
		// being rendered for it — the derivation answers for live sessions, and
		// after milestone 15 every live session is running.
		{name: "past the lifetime", now: base.AbsoluteDeadline(), want: DisplayRunning},
	}

	for _, state := range []State{StateStarting, StateRunning, StateDead} {
		for _, tc := range tests {
			t.Run(fmt.Sprintf("%s/%s", state, tc.name), func(t *testing.T) {
				t.Parallel()

				s := base
				s.State = state

				if got := s.DisplayState(tc.now); got != tc.want {
					t.Errorf("DisplayState() on a %q record = %q, want %q", state, got, tc.want)
				}
			})
		}
	}
}

// Mode is derived from the one name the record already carries (research R5),
// and the cases below are the ones where a plausible implementation gets a
// different answer from the operator's own configuration.
func TestModeDerivedFromStartCommand(t *testing.T) {
	t.Parallel()

	const remote = "rc"

	tests := []struct {
		name          string
		startCommand  string
		remoteCommand string
		want          Mode
	}{
		{
			name:          "the configured remote-control name",
			startCommand:  remote,
			remoteCommand: remote,
			want:          ModeRemote,
		},
		{
			name:          "any other configured name",
			startCommand:  config.DefaultStartCommandName,
			remoteCommand: remote,
			want:          ModeLocal,
		},
		{
			// A create that named no command runs the default, so the mode has to
			// be read against the name it resolved to and not against the empty
			// string the caller sent.
			name:          "no name at all is the default name",
			startCommand:  "",
			remoteCommand: remote,
			want:          ModeLocal,
		},
		{
			// The same rule the other way up: an operator whose remote-control
			// command is the default gets a remote session from a create that
			// asked for nothing, because that is the command it started.
			name:          "no name at all when the default is the remote one",
			startCommand:  "",
			remoteCommand: config.DefaultStartCommandName,
			want:          ModeRemote,
		},
		{
			// A daemon with no remote-control command has no remote sessions,
			// whatever a record carries — including a record adopted from a host
			// where an older configuration did have one.
			name:          "nothing configured as remote",
			startCommand:  remote,
			remoteCommand: "",
			want:          ModeLocal,
		},
		{
			// Names are compared byte for byte, as StartCommands.Command compares
			// them. "Close enough" is how a session silently reads as the wrong
			// mode.
			name:          "a name differing only in case",
			startCommand:  "RC",
			remoteCommand: remote,
			want:          ModeLocal,
		},
		{
			// FR-030 in the direction that matters here: what a record carries is
			// a name, and a command line is not one. Nothing in this method reads
			// a command line, and a value that is one matches no configured name.
			name:          "a command line where a name belongs",
			startCommand:  "claude remote-control --permission-mode bypassPermissions",
			remoteCommand: remote,
			want:          ModeLocal,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			s := newTestSession(testID("5"), auth.CallerOperator)
			s.StartCommand = tt.startCommand

			if got := s.Mode(tt.remoteCommand); got != tt.want {
				t.Errorf("Mode(%q) on start command %q = %q, want %q",
					tt.remoteCommand, tt.startCommand, got, tt.want)
			}
		})
	}
}

// "Derived, not stored" is a claim about the struct, so it is checked against
// the struct, exactly as the expiry claim above is. A Mode field or a
// RemoteControl bool would be a second answer to a question the start-command
// name already answers — free to disagree with it after a restart or a toggle
// that half succeeded, which is the failure research R5 rejected the field for.
func TestSessionStoresNoModeField(t *testing.T) {
	t.Parallel()

	forbidden := []string{"mode", "remote"}

	rt := reflect.TypeOf(Session{})
	for i := 0; i < rt.NumField(); i++ {
		lower := strings.ToLower(rt.Field(i).Name)
		for _, word := range forbidden {
			if strings.Contains(lower, word) {
				t.Errorf("Session has field %q; mode is derived from StartCommand, never stored", rt.Field(i).Name)
			}
		}
	}

	if _, ok := rt.MethodByName("Mode"); !ok {
		t.Error("Session has no Mode method; the mode has to be available, just not as a field")
	}
}

func TestStoreAddRejectsAMalformedRecord(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		mutate func(*Session)
	}{
		{name: "no id", mutate: func(s *Session) { s.ID = "" }},
		{name: "no owner", mutate: func(s *Session) { s.Owner = "" }},
		{name: "no state", mutate: func(s *Session) { s.State = "" }},
		{name: "a state outside the three", mutate: func(s *Session) { s.State = "zombie" }},
		{name: "no creation time", mutate: func(s *Session) { s.CreatedAt = time.Time{} }},
		{name: "no last-activity time", mutate: func(s *Session) { s.LastActivity = time.Time{} }},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			s := newTestSession(testID("f"), auth.CallerOperator)
			tc.mutate(&s)

			st := NewStore()
			err := st.Add(s)
			if err == nil {
				t.Fatal("Add() accepted a malformed record")
			}
			if !errors.Is(err, ErrInvalidSession) {
				t.Errorf("Add() error = %v, want one wrapping ErrInvalidSession", err)
			}
			if got := st.Len(); got != 0 {
				t.Errorf("Len() = %d after a refused Add, want 0", got)
			}
		})
	}
}

func TestStoreAddRefusesADuplicateID(t *testing.T) {
	t.Parallel()

	id := testID("a")
	first := newTestSession(id, auth.CallerOperator)
	for i := range first.TokenHash {
		first.TokenHash[i] = 1
	}
	st := storeWith(t, first)

	// A second record for the same ID would silently replace a token hash whose
	// plaintext its owner is still holding.
	second := newTestSession(id, auth.CallerOperator)
	second.Name = "usurper"

	err := st.Add(second)
	if !errors.Is(err, ErrSessionExists) {
		t.Fatalf("Add() error = %v, want one wrapping ErrSessionExists", err)
	}

	got, err := st.Get(id, auth.CallerOperator)
	if err != nil {
		t.Fatalf("Get() unexpected error: %v", err)
	}
	if got.Name != first.Name {
		t.Errorf("Get().Name = %q, want the original %q", got.Name, first.Name)
	}
	if got.TokenHash != first.TokenHash {
		t.Error("the refused Add replaced the stored token hash")
	}
	if n := st.Len(); n != 1 {
		t.Errorf("Len() = %d, want 1", n)
	}
}

// The two halves of FR-036 that only the store can state: AddCapped refuses at
// the limit while validating exactly as Add does, and a limit under 1 refuses
// everything rather than being read as "no limit".
//
// The manager and the config both refuse a cap under 1 before a store could ever
// be handed one, which is why this is asserted here — the fail-closed reading is
// the property, and nothing else in the daemon can reach it to prove it.
func TestStoreAddCappedRefusesAtTheLimit(t *testing.T) {
	t.Parallel()

	st := NewStore()
	if err := st.AddCapped(newTestSession(testID("a"), auth.CallerOperator), 2); err != nil {
		t.Fatalf("AddCapped() under the limit unexpected error: %v", err)
	}
	if err := st.AddCapped(newTestSession(testID("b"), auth.CallerOperator), 2); err != nil {
		t.Fatalf("AddCapped() at the last free place unexpected error: %v", err)
	}

	err := st.AddCapped(newTestSession(testID("c"), auth.CallerOperator), 2)
	if !errors.Is(err, ErrTooManySessions) {
		t.Fatalf("AddCapped() past the limit = %v, want one wrapping ErrTooManySessions", err)
	}
	if n := st.Len(); n != 2 {
		t.Errorf("Len() = %d after a refused AddCapped, want 2", n)
	}

	// A malformed record is refused as malformed whichever door it comes
	// through: the cap is a second check, not a replacement for the first.
	if err := st.AddCapped(Session{}, 99); !errors.Is(err, ErrInvalidSession) {
		t.Errorf("AddCapped() of a malformed record = %v, want one wrapping ErrInvalidSession", err)
	}

	for _, limit := range []int{0, -1} {
		if err := st.AddCapped(newTestSession(testID("d"), auth.CallerOperator), limit); !errors.Is(err, ErrTooManySessions) {
			t.Errorf("AddCapped() with a limit of %d = %v, want one wrapping ErrTooManySessions", limit, err)
		}
	}
}

// Adoption's door is uncapped on purpose: a session already running on the host
// is taken back however many there are, because the alternative to an over-cap
// record is a live unsandboxed shell with no owner and no deadline.
func TestStoreAddIsUncapped(t *testing.T) {
	t.Parallel()

	st := NewStore()
	for _, ch := range []string{"a", "b", "c"} {
		mustAdd(t, st, newTestSession(testID(ch), auth.CallerOperator))
	}
	if n := st.Len(); n != 3 {
		t.Fatalf("Len() = %d, want 3", n)
	}

	// And what those records do is count: the next create is refused.
	if err := st.AddCapped(newTestSession(testID("d"), auth.CallerOperator), 3); !errors.Is(err, ErrTooManySessions) {
		t.Errorf("AddCapped() against records the store already holds = %v, want one wrapping ErrTooManySessions", err)
	}
}

// FR-033: unknown and not-owned must be one answer. The store returns it, so no
// handler has to remember to.
func TestStoreGetIsOwnerScopedAndUniform(t *testing.T) {
	t.Parallel()

	id := testID("a")
	st := storeWith(t, newTestSession(id, auth.CallerOperator))

	tests := []struct {
		name  string
		id    string
		owner auth.CallerID
	}{
		{name: "an unknown id", id: testID("b"), owner: auth.CallerOperator},
		{name: "someone else's id", id: id, owner: otherOwner},
		{name: "an unknown id for another owner", id: testID("b"), owner: otherOwner},
		{name: "no owner at all", id: id, owner: ""},
		{name: "an empty id", id: "", owner: auth.CallerOperator},
	}

	var messages []string
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := st.Get(tc.id, tc.owner)
			if !errors.Is(err, ErrSessionNotFound) {
				t.Fatalf("Get() error = %v, want one wrapping ErrSessionNotFound", err)
			}
			if got != (Session{}) {
				t.Errorf("Get() returned %+v alongside an error, want the zero record", got)
			}
			messages = append(messages, err.Error())
		})
	}

	// Not merely the same sentinel — the same text, so nothing downstream can
	// tell the cases apart even by rendering the error.
	for i, msg := range messages {
		if msg != messages[0] {
			t.Errorf("failure %d reads %q, want the same %q as the first", i, msg, messages[0])
		}
	}

	// The control: the owner's own lookup still works.
	if _, err := st.Get(id, auth.CallerOperator); err != nil {
		t.Fatalf("Get() for the owner unexpected error: %v", err)
	}
}

// The store hands out copies, so a handler cannot mutate a live record outside
// the lock — which is what makes the reaper's concurrent reads safe.
func TestStoreGetReturnsACopy(t *testing.T) {
	t.Parallel()

	id := testID("a")
	st := storeWith(t, newTestSession(id, auth.CallerOperator))

	got, err := st.Get(id, auth.CallerOperator)
	if err != nil {
		t.Fatalf("Get() unexpected error: %v", err)
	}
	got.Owner = otherOwner
	got.State = StateDead
	got.CreatedAt = contractCreatedAt.Add(-72 * time.Hour)
	got.TokenHash[0] = 9

	again, err := st.Get(id, auth.CallerOperator)
	if err != nil {
		t.Fatalf("Get() after mutating the copy unexpected error: %v", err)
	}
	if again.Owner != auth.CallerOperator {
		t.Errorf("Owner = %q, want %q — the copy reached the store", again.Owner, auth.CallerOperator)
	}
	if again.State != StateStarting {
		t.Errorf("State = %q, want %q", again.State, StateStarting)
	}
	if !again.CreatedAt.Equal(contractCreatedAt) {
		t.Errorf("CreatedAt = %v, want %v", again.CreatedAt, contractCreatedAt)
	}
	if again.TokenHash != (Session{}).TokenHash {
		t.Error("TokenHash changed through the returned copy")
	}
}

func TestStoreListIsOwnerScoped(t *testing.T) {
	t.Parallel()

	mine, theirs := testID("a"), testID("b")
	st := NewStore()
	mustAdd(t, st, newTestSession(mine, auth.CallerOperator))
	mustAdd(t, st, newTestSession(theirs, otherOwner))

	got := st.List(auth.CallerOperator)
	if len(got) != 1 {
		t.Fatalf("List() returned %d sessions, want 1", len(got))
	}
	if got[0].ID != mine {
		t.Errorf("List()[0].ID = %q, want %q", got[0].ID, mine)
	}

	if n := len(st.List(otherOwner)); n != 1 {
		t.Errorf("List(otherOwner) returned %d sessions, want 1", n)
	}
	// An unauthenticated caller has no identity, and no identity owns nothing.
	if n := len(st.List("")); n != 0 {
		t.Errorf("List(\"\") returned %d sessions, want 0", n)
	}
	if n := st.Len(); n != 2 {
		t.Errorf("Len() = %d, want 2 — Len counts every record, not one owner's", n)
	}
}

// Map iteration is randomised, so an unsorted List would return a different
// order between two identical requests.
func TestStoreListIsOrderedOldestFirst(t *testing.T) {
	t.Parallel()

	st := NewStore()
	// Added newest first, and two sharing an instant so the ID tiebreak is
	// exercised rather than incidental.
	for _, spec := range []struct {
		ch     string
		offset time.Duration
	}{
		{ch: "c", offset: 2 * time.Hour},
		{ch: "b", offset: time.Hour},
		{ch: "a", offset: time.Hour},
		{ch: "0", offset: 0},
	} {
		s := newTestSession(testID(spec.ch), auth.CallerOperator)
		s.CreatedAt = contractCreatedAt.Add(spec.offset)
		s.LastActivity = s.CreatedAt
		mustAdd(t, st, s)
	}

	want := []string{testID("0"), testID("a"), testID("b"), testID("c")}
	for attempt := 0; attempt < 8; attempt++ {
		got := st.List(auth.CallerOperator)
		if len(got) != len(want) {
			t.Fatalf("List() returned %d sessions, want %d", len(got), len(want))
		}
		for i := range want {
			if got[i].ID != want[i] {
				t.Fatalf("attempt %d: List()[%d].ID = %q, want %q", attempt, i, got[i].ID, want[i])
			}
		}
	}
}

func TestStoreSetState(t *testing.T) {
	t.Parallel()

	id := testID("a")

	t.Run("moves a live record", func(t *testing.T) {
		t.Parallel()

		st := storeWith(t, newTestSession(id, auth.CallerOperator))
		if err := st.SetState(id, StateRunning); err != nil {
			t.Fatalf("SetState(running) unexpected error: %v", err)
		}
		got, err := st.Get(id, auth.CallerOperator)
		if err != nil {
			t.Fatalf("Get() unexpected error: %v", err)
		}
		if got.State != StateRunning {
			t.Errorf("State = %q, want %q", got.State, StateRunning)
		}
	})

	t.Run("refuses to revive a dead record", func(t *testing.T) {
		t.Parallel()

		st := storeWith(t, newTestSession(id, auth.CallerOperator))
		if err := st.SetState(id, StateDead); err != nil {
			t.Fatalf("SetState(dead) unexpected error: %v", err)
		}

		for _, next := range []State{StateStarting, StateRunning} {
			if err := st.SetState(id, next); !errors.Is(err, ErrSessionDead) {
				t.Errorf("SetState(%q) error = %v, want one wrapping ErrSessionDead", next, err)
			}
		}
		// Re-confirming death is not a revival, so it is not an error.
		if err := st.SetState(id, StateDead); err != nil {
			t.Errorf("SetState(dead) on a dead record error = %v, want nil", err)
		}

		got, err := st.Get(id, auth.CallerOperator)
		if err != nil {
			t.Fatalf("Get() unexpected error: %v", err)
		}
		if got.State != StateDead {
			t.Errorf("State = %q, want %q", got.State, StateDead)
		}
	})

	t.Run("refuses a state outside the three", func(t *testing.T) {
		t.Parallel()

		st := storeWith(t, newTestSession(id, auth.CallerOperator))
		if err := st.SetState(id, "zombie"); !errors.Is(err, ErrInvalidState) {
			t.Fatalf("SetState(zombie) error = %v, want one wrapping ErrInvalidState", err)
		}
		got, err := st.Get(id, auth.CallerOperator)
		if err != nil {
			t.Fatalf("Get() unexpected error: %v", err)
		}
		if got.State != StateStarting {
			t.Errorf("State = %q, want the untouched %q", got.State, StateStarting)
		}
	})

	t.Run("refuses an unknown id", func(t *testing.T) {
		t.Parallel()

		st := NewStore()
		if err := st.SetState(testID("b"), StateRunning); !errors.Is(err, ErrSessionNotFound) {
			t.Fatalf("SetState() error = %v, want one wrapping ErrSessionNotFound", err)
		}
	})
}

func TestStoreTouch(t *testing.T) {
	t.Parallel()

	id := testID("a")

	t.Run("records when the session was last driven", func(t *testing.T) {
		t.Parallel()

		st := storeWith(t, newTestSession(id, auth.CallerOperator))
		later := contractCreatedAt.Add(30 * time.Minute)

		if err := st.Touch(id, later); err != nil {
			t.Fatalf("Touch() unexpected error: %v", err)
		}
		got, err := st.Get(id, auth.CallerOperator)
		if err != nil {
			t.Fatalf("Get() unexpected error: %v", err)
		}
		if !got.LastActivity.Equal(later) {
			t.Errorf("LastActivity = %v, want %v", got.LastActivity, later)
		}
		// Activity does not buy time against the ceiling (FR-038), and since
		// milestone 15 there is no other deadline for it to buy time against —
		// this call moves a fact the interface shows and nothing acts on.
		if !got.AbsoluteDeadline().Equal(contractExpiresAt) {
			t.Errorf("AbsoluteDeadline() = %v, want %v", got.AbsoluteDeadline(), contractExpiresAt)
		}
	})

	t.Run("never moves it backwards", func(t *testing.T) {
		t.Parallel()

		st := storeWith(t, newTestSession(id, auth.CallerOperator))
		later := contractCreatedAt.Add(30 * time.Minute)
		if err := st.Touch(id, later); err != nil {
			t.Fatalf("Touch() unexpected error: %v", err)
		}

		// A lagging read, and the zero time an uninitialised clock would give.
		for _, stale := range []time.Time{contractCreatedAt, time.Time{}} {
			if err := st.Touch(id, stale); err != nil {
				t.Fatalf("Touch(%v) unexpected error: %v", stale, err)
			}
			got, err := st.Get(id, auth.CallerOperator)
			if err != nil {
				t.Fatalf("Get() unexpected error: %v", err)
			}
			if !got.LastActivity.Equal(later) {
				t.Errorf("Touch(%v) moved LastActivity back to %v, want %v", stale, got.LastActivity, later)
			}
		}
	})

	t.Run("refuses a dead record", func(t *testing.T) {
		t.Parallel()

		st := storeWith(t, newTestSession(id, auth.CallerOperator))
		if err := st.SetState(id, StateDead); err != nil {
			t.Fatalf("SetState(dead) unexpected error: %v", err)
		}
		err := st.Touch(id, contractCreatedAt.Add(time.Hour))
		if !errors.Is(err, ErrSessionDead) {
			t.Fatalf("Touch() error = %v, want one wrapping ErrSessionDead", err)
		}
	})

	t.Run("refuses an unknown id", func(t *testing.T) {
		t.Parallel()

		st := NewStore()
		err := st.Touch(testID("b"), contractCreatedAt)
		if !errors.Is(err, ErrSessionNotFound) {
			t.Fatalf("Touch() error = %v, want one wrapping ErrSessionNotFound", err)
		}
	})
}

// #120: the width a reflow left the window at is a field of the record, written
// only by the store and only by this method.
func TestStoreSetWidth(t *testing.T) {
	t.Parallel()

	id := testID("a")

	t.Run("records the width and touches nothing else", func(t *testing.T) {
		t.Parallel()

		before := newTestSession(id, auth.CallerOperator)
		st := storeWith(t, before)

		if err := st.SetWidth(id, 44); err != nil {
			t.Fatalf("SetWidth() unexpected error: %v", err)
		}
		got, err := st.Get(id, auth.CallerOperator)
		if err != nil {
			t.Fatalf("Get() unexpected error: %v", err)
		}
		if got.Width != 44 {
			t.Errorf("Width = %d, want 44", got.Width)
		}
		if got.Columns() != 44 {
			t.Errorf("Columns() = %d, want 44", got.Columns())
		}
		// A reflow is not driving: nothing is delivered into the pane, so the
		// clock a card reports the session was last driven by must not move.
		if !got.LastActivity.Equal(before.LastActivity) {
			t.Errorf("SetWidth moved LastActivity to %v; a reflow drives nothing", got.LastActivity)
		}
		if !got.AbsoluteDeadline().Equal(contractExpiresAt) {
			t.Errorf("AbsoluteDeadline() = %v, want %v", got.AbsoluteDeadline(), contractExpiresAt)
		}
	})

	t.Run("refuses a dead record", func(t *testing.T) {
		t.Parallel()

		st := storeWith(t, newTestSession(id, auth.CallerOperator))
		if err := st.SetState(id, StateDead); err != nil {
			t.Fatalf("SetState(dead) unexpected error: %v", err)
		}
		if err := st.SetWidth(id, 44); !errors.Is(err, ErrSessionDead) {
			t.Fatalf("SetWidth() error = %v, want one wrapping ErrSessionDead", err)
		}
	})

	t.Run("refuses an unknown id", func(t *testing.T) {
		t.Parallel()

		st := NewStore()
		if err := st.SetWidth(testID("b"), 44); !errors.Is(err, ErrSessionNotFound) {
			t.Fatalf("SetWidth() error = %v, want one wrapping ErrSessionNotFound", err)
		}
	})
}

// TestColumnsIsTmuxsDefaultUntilSomethingReflows states the zero-means-inherited
// rule for the field milestone 16 adds, in the one place it is expressed.
//
// **Must fail when** Columns starts reading the raw field, which would describe
// every session predating this milestone — and every session since that nobody
// has reflowed — as zero columns wide.
func TestColumnsIsTmuxsDefaultUntilSomethingReflows(t *testing.T) {
	t.Parallel()

	if got := (Session{}).Columns(); got != config.DefaultPaneWidth {
		t.Errorf("a session nobody has reflowed reports %d columns, want %d", got, config.DefaultPaneWidth)
	}
	if got := (Session{Width: 44}).Columns(); got != 44 {
		t.Errorf("a session reflowed to 44 reports %d columns", got)
	}
}

// FR-020: destroying a session clears its record and its stored credential hash.
func TestStoreDeleteClearsTheRecord(t *testing.T) {
	t.Parallel()

	id := testID("a")
	s := newTestSession(id, auth.CallerOperator)
	for i := range s.TokenHash {
		s.TokenHash[i] = 7
	}
	st := storeWith(t, s)

	if err := st.Delete(id); err != nil {
		t.Fatalf("Delete() unexpected error: %v", err)
	}
	if _, err := st.Get(id, auth.CallerOperator); !errors.Is(err, ErrSessionNotFound) {
		t.Errorf("Get() after Delete error = %v, want one wrapping ErrSessionNotFound", err)
	}
	if n := st.Len(); n != 0 {
		t.Errorf("Len() = %d after Delete, want 0", n)
	}
	if n := len(st.List(auth.CallerOperator)); n != 0 {
		t.Errorf("List() returned %d sessions after Delete, want 0", n)
	}
	// A second delete is a failure, not a silent success: the caller asked to
	// tear down something that was not there.
	if err := st.Delete(id); !errors.Is(err, ErrSessionNotFound) {
		t.Errorf("second Delete() error = %v, want one wrapping ErrSessionNotFound", err)
	}

	// The ID is free again, which is what makes reconciliation re-adoptable.
	mustAdd(t, st, newTestSession(id, auth.CallerOperator))
	got, err := st.Get(id, auth.CallerOperator)
	if err != nil {
		t.Fatalf("Get() after re-adding unexpected error: %v", err)
	}
	if got.TokenHash != (Session{}).TokenHash {
		t.Error("the re-added record inherited the deleted record's token hash")
	}
}

// The reaper reads while handlers write. Run under -race, this is the assertion
// that holding Sessions by value behind one mutex is enough.
func TestStoreIsSafeForConcurrentUse(t *testing.T) {
	t.Parallel()

	// Four workers per id, so Add genuinely collides rather than each goroutine
	// owning a record no other touches.
	const alphabet = "0123456789abcdef"
	const workers = 4 * len(alphabet)

	st := NewStore()

	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()

			id := testID(alphabet[i%len(alphabet) : i%len(alphabet)+1])
			s := newTestSession(id, auth.CallerOperator)
			s.CreatedAt = contractCreatedAt.Add(time.Duration(i) * time.Minute)
			s.LastActivity = s.CreatedAt

			// Exactly one Add per id wins and the other three see
			// ErrSessionExists — the same shape as two concurrent creates
			// colliding on one record.
			if err := st.Add(s); err != nil && !errors.Is(err, ErrSessionExists) {
				t.Errorf("Add() error = %v, want nil or ErrSessionExists", err)
			}
			if err := st.Touch(id, s.CreatedAt.Add(time.Hour)); err != nil &&
				!errors.Is(err, ErrSessionNotFound) && !errors.Is(err, ErrSessionDead) {
				t.Errorf("Touch() error = %v", err)
			}
			if err := st.SetState(id, StateRunning); err != nil &&
				!errors.Is(err, ErrSessionNotFound) && !errors.Is(err, ErrSessionDead) {
				t.Errorf("SetState() error = %v", err)
			}
			if _, err := st.Get(id, auth.CallerOperator); err != nil &&
				!errors.Is(err, ErrSessionNotFound) {
				t.Errorf("Get() error = %v", err)
			}
			_ = st.List(auth.CallerOperator)
			_ = st.Len()
		}(i)
	}
	wg.Wait()

	if n := st.Len(); n != len(alphabet) {
		t.Errorf("Len() = %d, want %d — one record per distinct id", n, len(alphabet))
	}
}

// TestDisplayStateShowsAFailedSession is the operator-facing half of the
// give-up bound (FR-018). A session nobody could save must be visible as such,
// because the failure spec 012 exists to remove is one that happened silently.
func TestDisplayStateShowsAFailedSession(t *testing.T) {
	t.Parallel()

	tests := []struct {
		state State
		want  DisplayState
	}{
		{StateStarting, DisplayRunning},
		{StateRunning, DisplayRunning},
		{StateFailed, DisplayFailed},
	}
	for _, tt := range tests {
		s := Session{State: tt.state}
		if got := s.DisplayState(time.Time{}); got != tt.want {
			t.Errorf("DisplayState() for %q = %q, want %q", tt.state, got, tt.want)
		}
	}
}
