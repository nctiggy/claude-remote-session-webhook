package audit_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nctiggy/claude-remote-session-webhook/internal/audit"
)

// Deliberately not 32 hex characters. A realistic session ID is exactly the
// shape gitleaks reads as a credential, and the ID's shape is T013's assertion
// to make, not this package's — audit stores whatever it is handed.
const testSessionID = "a-test-session-id"

// fixedTime is not UTC, so a record proving it emitted "…Z" proves the
// conversion happened rather than the fixture already being right.
var fixedTime = time.Date(2026, 8, 2, 21, 36, 58, 123456789, time.FixedZone("UTC+2", 2*60*60))

func fixedClock() func() time.Time { return func() time.Time { return fixedTime } }

// emit runs one record through a Logger and hands back the raw bytes written.
func emit(t *testing.T, rec audit.Record) string {
	t.Helper()
	var buf bytes.Buffer
	if err := audit.NewTo(&buf, fixedClock()).Emit(rec); err != nil {
		t.Fatalf("Emit(%+v) = %v, want no error", rec, err)
	}
	return buf.String()
}

// decode parses the one line a record must produce, failing if it is not
// exactly one line of JSON.
func decode(t *testing.T, out string) map[string]any {
	t.Helper()
	if n := strings.Count(out, "\n"); n != 1 || !strings.HasSuffix(out, "\n") {
		t.Fatalf("record spans %d newlines, want exactly one trailing newline: %q", n, out)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("record is not JSON: %v (%q)", err, out)
	}
	return got
}

func keysOf(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// The full record: every field in data-model.md's AuditRecord table, and
// nothing that is not in it.
func TestEmitWritesTheDocumentedFields(t *testing.T) {
	t.Parallel()

	got := decode(t, emit(t, audit.Record{
		Action:    audit.ActionSessionCreate,
		Caller:    "operator",
		SessionID: testSessionID,
		Decision:  audit.Allow,
		Reason:    "created",
		Remote:    "127.0.0.1:54321",
	}))

	want := map[string]any{
		"time":       "2026-08-02T19:36:58Z",
		"action":     "session.create",
		"caller":     "operator",
		"session_id": testSessionID,
		"decision":   "allow",
		"reason":     "created",
		"remote":     "127.0.0.1:54321",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("record = %v, want %v", got, want)
	}
}

// slog's JSON handler emits "level" and "msg" of its own accord. A record
// carrying keys the spec does not name is a record whose shape drifted.
func TestEmitWritesNoKeyBeyondTheDocumentedSeven(t *testing.T) {
	t.Parallel()

	got := decode(t, emit(t, audit.Record{
		Action:    audit.ActionSessionDestroy,
		Caller:    "operator",
		SessionID: testSessionID,
		Decision:  audit.Allow,
		Reason:    "verified gone",
		Remote:    "127.0.0.1:54321",
	}))

	want := []string{"action", "caller", "decision", "reason", "remote", "session_id", "time"}
	if keys := keysOf(got); !reflect.DeepEqual(keys, want) {
		t.Errorf("keys = %v, want %v", keys, want)
	}
	for _, unwanted := range []string{"level", "msg", "source"} {
		if _, ok := got[unwanted]; ok {
			t.Errorf("record carries slog's %q key: %v", unwanted, got)
		}
	}
}

// The three optional fields are absent, not empty strings: data-model.md says
// session_id is "the 32-hex ID, or absent", and an action with no request
// behind it has no peer to name.
func TestEmitOmitsTheFieldsThatDoNotApply(t *testing.T) {
	t.Parallel()

	got := decode(t, emit(t, audit.Record{
		Action:   audit.ActionStartupAdopt,
		Caller:   "operator",
		Decision: audit.Allow,
	}))

	want := []string{"action", "caller", "decision", "time"}
	if keys := keysOf(got); !reflect.DeepEqual(keys, want) {
		t.Errorf("keys = %v, want %v", keys, want)
	}
}

// A rejected request has no established identity, and a record with no caller
// at all would be worse than one that says so.
func TestEmitDefaultsAnUnnamedCallerToUnknown(t *testing.T) {
	t.Parallel()

	got := decode(t, emit(t, audit.Record{
		Action:   audit.ActionAuthReject,
		Decision: audit.Deny,
		Reason:   "signature mismatch",
		Remote:   "127.0.0.1:54321",
	}))

	if got["caller"] != audit.CallerUnknown {
		t.Errorf("caller = %v, want %q", got["caller"], audit.CallerUnknown)
	}
}

func TestEmitStampsUTCRFC3339FromTheInjectedClock(t *testing.T) {
	t.Parallel()

	got := decode(t, emit(t, audit.Record{
		Action:   audit.ActionReaperDestroy,
		Decision: audit.Allow,
	}))

	stamp, ok := got["time"].(string)
	if !ok {
		t.Fatalf("time = %v, want a string", got["time"])
	}
	if want := "2026-08-02T19:36:58Z"; stamp != want {
		t.Errorf("time = %q, want %q", stamp, want)
	}
	if !regexp.MustCompile(`^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}Z$`).MatchString(stamp) {
		t.Errorf("time = %q, want RFC3339 in UTC at second precision", stamp)
	}
	if parsed, err := time.Parse(time.RFC3339, stamp); err != nil {
		t.Errorf("time %q does not parse as RFC3339: %v", stamp, err)
	} else if !parsed.Equal(fixedTime.Truncate(time.Second)) {
		t.Errorf("time = %v, want %v", parsed, fixedTime.Truncate(time.Second))
	}
}

// The trail is read line by line (quickstart.md greps it), so a reason that
// contains a newline, a quote, or an escape must not be able to forge a second
// record or break the parse.
func TestEmitKeepsAHostileReasonOnOneLine(t *testing.T) {
	t.Parallel()

	hostile := "line one\nline two\r\"quoted\"\x1b[31m\t{\"action\":\"forged\"}"
	got := decode(t, emit(t, audit.Record{
		Action:   audit.ActionAuthReject,
		Decision: audit.Deny,
		Reason:   hostile,
	}))

	if got["reason"] != hostile {
		t.Errorf("reason = %q, want %q", got["reason"], hostile)
	}
	if got["action"] != string(audit.ActionAuthReject) {
		t.Errorf("action = %v, want %q — a forged record escaped the reason field", got["action"], audit.ActionAuthReject)
	}
}

func TestEmitWritesOneLinePerRecord(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	log := audit.NewTo(&buf, fixedClock())
	for _, action := range []audit.Action{
		audit.ActionSessionCreate, audit.ActionSessionPrompt, audit.ActionSessionDestroy,
	} {
		if err := log.Emit(audit.Record{Action: action, Decision: audit.Allow}); err != nil {
			t.Fatalf("Emit(%q) = %v, want no error", action, err)
		}
	}

	lines := strings.Split(strings.TrimSuffix(buf.String(), "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("got %d lines, want 3: %q", len(lines), buf.String())
	}
	for i, line := range lines {
		var rec map[string]any
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Errorf("line %d is not JSON: %v (%q)", i, err, line)
		}
	}
}

func TestEmitAcceptsEveryDocumentedAction(t *testing.T) {
	t.Parallel()

	// Restated as literals rather than referencing the constants, so renaming a
	// constant's value is a failure here instead of a silent change to the
	// trail an operator greps. Listing *every* action also makes two constants
	// sharing one spelling a compile error — constant keys in a map literal must
	// be distinct — which is what keeps the two doors' refusals countable apart
	// when the quickstart runs `grep -c 'access.reject'`.
	cases := map[audit.Action]string{
		audit.ActionSessionCreate:  "session.create",
		audit.ActionSessionPrompt:  "session.prompt",
		audit.ActionSessionDestroy: "session.destroy",
		audit.ActionAuthReject:     "auth.reject",
		audit.ActionReaperDestroy:  "reaper.destroy",
		audit.ActionStartupAdopt:   "startup.adopt",
		audit.ActionSessionList:    "session.list",
		audit.ActionSessionDetail:  "session.detail",
		audit.ActionSessionOutput:  "session.output",
		audit.ActionUnknownRoute:   "route.unknown",

		// data-model.md's AuditRecord additions.
		audit.ActionAccessReject:   "access.reject",
		audit.ActionDashboardView:  "dashboard.view",
		audit.ActionDashboardAsset: "dashboard.asset",
		audit.ActionStreamOpen:     "stream.open",

		// research.md R5's additions, the vocabulary of a browser that can write.
		audit.ActionDashboardCreate:  "dashboard.create",
		audit.ActionDashboardDestroy: "dashboard.destroy",
		audit.ActionDashboardRename:  "dashboard.rename",
		audit.ActionDashboardCompact: "dashboard.compact",
		audit.ActionDashboardReject:  "dashboard.reject",
		audit.ActionFleetOpen:        "fleet.open",

		// Milestone 4's one addition: the read-only settings page.
		audit.ActionSettingsView: "settings.view",
	}
	for action, want := range cases {
		t.Run(want, func(t *testing.T) {
			t.Parallel()
			if string(action) != want {
				t.Errorf("action constant = %q, want %q", action, want)
			}
			got := decode(t, emit(t, audit.Record{Action: action, Decision: audit.Allow}))
			if got["action"] != want {
				t.Errorf("action = %v, want %q", got["action"], want)
			}
		})
	}
}

// The browser door's actions ride in the same record as milestone 1's: FR-016
// freezes the shape, so what a second door adds is vocabulary and never a field.
// Asserting the emitted key set per action is what makes that checkable here
// rather than at each future call site.
func TestEmitWritesTheBrowserDoorActionsInTheExistingShape(t *testing.T) {
	t.Parallel()

	for _, action := range []audit.Action{
		audit.ActionAccessReject, audit.ActionDashboardView,
		audit.ActionDashboardAsset, audit.ActionStreamOpen,
	} {
		t.Run(string(action), func(t *testing.T) {
			t.Parallel()

			got := decode(t, emit(t, audit.Record{
				Action:    action,
				Caller:    "operator",
				SessionID: testSessionID,
				Decision:  audit.Deny,
				Reason:    "a reason this repo authored",
				Remote:    "127.0.0.1:54321",
			}))

			want := map[string]any{
				"time":       "2026-08-02T19:36:58Z",
				"action":     string(action),
				"caller":     "operator",
				"session_id": testSessionID,
				"decision":   "deny",
				"reason":     "a reason this repo authored",
				"remote":     "127.0.0.1:54321",
			}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("record = %v, want %v", got, want)
			}

			// A layer-1 refusal happens before any session is resolved and before
			// an identity exists, so the door's records must omit rather than
			// invent — the same absence milestone 1's startup.adopt relies on.
			bare := decode(t, emit(t, audit.Record{Action: action, Decision: audit.Deny}))
			if keys := keysOf(bare); !reflect.DeepEqual(keys, []string{"action", "caller", "decision", "time"}) {
				t.Errorf("keys = %v, want the four a record with no session and no peer carries", keys)
			}
			if bare["caller"] != audit.CallerUnknown {
				t.Errorf("caller = %v, want %q", bare["caller"], audit.CallerUnknown)
			}
		})
	}
}

// FR-024: a change made from the browser must be distinguishable in the trail
// from the same change made through the API, and FR-026 needs a cross-site
// refusal to stand out from a layer-1 one. Both are properties of the spelling
// alone, which is why they are asserted here rather than at the routes: a
// browser action that reused session.destroy would leave every other test in
// this package green — the record still emits, still parses, still carries the
// documented seven keys — and the loss would surface only during an incident,
// as a grep that quietly conflated two doors.
func TestDashboardActionsAreDistinctFromAPI(t *testing.T) {
	t.Parallel()

	// Keyed by the literal so that two constants sharing one spelling is a
	// duplicate-key compile error, the same construction the documented-action
	// test relies on.
	added := map[string]audit.Action{
		"dashboard.create":  audit.ActionDashboardCreate,
		"dashboard.destroy": audit.ActionDashboardDestroy,
		"dashboard.rename":  audit.ActionDashboardRename,
		"dashboard.compact": audit.ActionDashboardCompact,
		"dashboard.reject":  audit.ActionDashboardReject,
		"fleet.open":        audit.ActionFleetOpen,
	}

	// Every action the trail already spoke before this milestone — milestone 1's
	// ten and the browser door's four. session.create and session.destroy are the
	// two a dashboard action is most tempting to reuse, since the daemon does the
	// identical work behind both.
	existing := map[audit.Action]string{
		audit.ActionSessionCreate:  "ActionSessionCreate",
		audit.ActionSessionPrompt:  "ActionSessionPrompt",
		audit.ActionSessionDestroy: "ActionSessionDestroy",
		audit.ActionAuthReject:     "ActionAuthReject",
		audit.ActionReaperDestroy:  "ActionReaperDestroy",
		audit.ActionStartupAdopt:   "ActionStartupAdopt",
		audit.ActionSessionList:    "ActionSessionList",
		audit.ActionSessionDetail:  "ActionSessionDetail",
		audit.ActionSessionOutput:  "ActionSessionOutput",
		audit.ActionUnknownRoute:   "ActionUnknownRoute",
		audit.ActionAccessReject:   "ActionAccessReject",
		audit.ActionDashboardView:  "ActionDashboardView",
		audit.ActionDashboardAsset: "ActionDashboardAsset",
		audit.ActionStreamOpen:     "ActionStreamOpen",
	}

	for want, action := range added {
		t.Run(want, func(t *testing.T) {
			t.Parallel()

			if string(action) != want {
				t.Fatalf("action constant = %q, want %q", action, want)
			}
			if name, reused := existing[action]; reused {
				t.Errorf("%q is also %s; a browser-initiated change must be distinguishable from the API's (FR-024)", want, name)
			}
			// noun.verb, the naming this package has used since milestone 1: one
			// dot, both halves lowercase and non-empty.
			if parts := strings.Split(want, "."); len(parts) != 2 || parts[0] == "" || parts[1] == "" || want != strings.ToLower(want) {
				t.Errorf("action %q is not noun.verb", want)
			}
		})
	}
}

func TestDecisionConstantsAreTheDocumentedTwo(t *testing.T) {
	t.Parallel()

	if string(audit.Allow) != "allow" {
		t.Errorf("Allow = %q, want %q", audit.Allow, "allow")
	}
	if string(audit.Deny) != "deny" {
		t.Errorf("Deny = %q, want %q", audit.Deny, "deny")
	}

	for _, decision := range []audit.Decision{audit.Allow, audit.Deny} {
		got := decode(t, emit(t, audit.Record{Action: audit.ActionSessionCreate, Decision: decision}))
		if got["decision"] != string(decision) {
			t.Errorf("decision = %v, want %q", got["decision"], decision)
		}
	}
}

// A record with no action, or with an outcome that is neither allow nor deny,
// answers none of the questions the trail exists to answer. Refusing it turns
// the miswiring into a failure at the call site rather than a line an operator
// has to interpret later.
func TestEmitRefusesAMalformedRecord(t *testing.T) {
	t.Parallel()

	cases := map[string]audit.Record{
		"no action":       {Decision: audit.Allow},
		"no decision":     {Action: audit.ActionSessionCreate},
		"unknown outcome": {Action: audit.ActionSessionCreate, Decision: audit.Decision("maybe")},
		"decision cased":  {Action: audit.ActionSessionCreate, Decision: audit.Decision("Allow")},
	}
	for name, rec := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			var buf bytes.Buffer
			err := audit.NewTo(&buf, fixedClock()).Emit(rec)
			if err == nil {
				t.Fatalf("Emit(%+v) = nil, want an error", rec)
			}
			if buf.Len() != 0 {
				t.Errorf("a refused record still wrote %q", buf.String())
			}
		})
	}
}

type failingWriter struct{ err error }

func (f failingWriter) Write([]byte) (int, error) { return 0, f.err }

// FR-041 makes the record mandatory, so a caller that could not write one has
// to be able to find out. Swallowing this would make the audit trail silently
// incomplete, which is the one failure mode it cannot have.
func TestEmitReportsAWriteFailure(t *testing.T) {
	t.Parallel()

	sentinel := errors.New("stdout is gone")
	err := audit.NewTo(failingWriter{err: sentinel}, fixedClock()).Emit(audit.Record{
		Action:   audit.ActionSessionCreate,
		Decision: audit.Allow,
		Reason:   "a reason nobody else should see",
	})
	if err == nil {
		t.Fatal("Emit = nil, want an error when the sink fails")
	}
	if !errors.Is(err, sentinel) {
		t.Errorf("Emit = %v, want it to wrap %v", err, sentinel)
	}
	// The error travels back into a handler that may log it in turn, so it
	// names the action and nothing else from the record.
	if strings.Contains(err.Error(), "a reason nobody else should see") {
		t.Errorf("the write error repeats the record's reason: %v", err)
	}
}

// Reflection, not review, is what keeps FR-042 true as the struct grows: a map,
// a slice, an any, or an embedded slog.Attr added later fails here. The field
// list is restated rather than derived, so a new field is a failure even if its
// type is a string.
func TestRecordCannotCarryFreeFormContent(t *testing.T) {
	t.Parallel()

	rt := reflect.TypeOf(audit.Record{})
	want := []string{"Action", "Caller", "SessionID", "Decision", "Reason", "Remote"}
	if rt.NumField() != len(want) {
		t.Fatalf("Record has %d fields, want %d (%v) — a new field needs a check against FR-042", rt.NumField(), len(want), want)
	}
	for i, name := range want {
		field := rt.Field(i)
		if field.Name != name {
			t.Errorf("field %d = %q, want %q", i, field.Name, name)
		}
		if kind := field.Type.Kind(); kind != reflect.String {
			t.Errorf("field %s is a %s; every audit field must be a string so it cannot hold a prompt, a pane capture, or a body", field.Name, kind)
		}
	}
}

// The other half of the same guarantee: no entry point may accept arbitrary
// content alongside the record. Emit(rec, extra ...any) or a With(key, value)
// helper would reopen exactly what the fixed struct closes.
func TestLoggerOffersNoFreeFormEntryPoint(t *testing.T) {
	t.Parallel()

	forbidden := map[reflect.Kind]bool{
		reflect.Interface: true, reflect.Map: true, reflect.Slice: true,
		reflect.Array: true, reflect.Chan: true, reflect.UnsafePointer: true,
	}
	for _, rt := range []reflect.Type{reflect.TypeOf(audit.Logger{}), reflect.TypeOf(&audit.Logger{})} {
		for i := 0; i < rt.NumMethod(); i++ {
			m := rt.Method(i)
			if m.Type.IsVariadic() {
				t.Errorf("%s.%s is variadic; a variadic audit call is a passthrough", rt, m.Name)
			}
			for j := 1; j < m.Type.NumIn(); j++ {
				if kind := m.Type.In(j).Kind(); forbidden[kind] {
					t.Errorf("%s.%s takes a %s parameter, which can carry arbitrary content", rt, m.Name, kind)
				}
			}
		}
	}
}

// One Logger is shared by every request path, so this is the property that
// makes that safe. Under -race a handler that did not serialise its writes
// fails here rather than interleaving two records in production.
func TestEmitIsSafeForConcurrentUse(t *testing.T) {
	t.Parallel()

	const writers = 16
	var buf bytes.Buffer
	log := audit.NewTo(&buf, fixedClock())

	var wg sync.WaitGroup
	errs := make(chan error, writers)
	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- log.Emit(audit.Record{
				Action:    audit.ActionSessionPrompt,
				Caller:    "operator",
				SessionID: testSessionID,
				Decision:  audit.Allow,
			})
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Errorf("Emit = %v, want no error", err)
		}
	}

	lines := strings.Split(strings.TrimSuffix(buf.String(), "\n"), "\n")
	if len(lines) != writers {
		t.Fatalf("got %d lines, want %d", len(lines), writers)
	}
	for i, line := range lines {
		var rec map[string]any
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Errorf("line %d is not JSON — two records interleaved: %v (%q)", i, err, line)
		}
	}
}

// FR-041 says stdout specifically, and New is the only constructor cmd/crswd
// uses. Without this the destination is an assertion in a doc comment.
// Not parallel: it swaps the process's stdout.
func TestNewWritesToStdout(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe() = %v", err)
	}

	real := os.Stdout
	os.Stdout = w
	log := audit.New()
	emitErr := log.Emit(audit.Record{Action: audit.ActionStartupAdopt, Decision: audit.Allow})
	os.Stdout = real

	if err := w.Close(); err != nil {
		t.Fatalf("close pipe: %v", err)
	}
	if emitErr != nil {
		t.Fatalf("Emit = %v, want no error", emitErr)
	}

	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("read pipe: %v", err)
	}
	if err := r.Close(); err != nil {
		t.Fatalf("close pipe reader: %v", err)
	}

	var rec map[string]any
	if err := json.Unmarshal(out, &rec); err != nil {
		t.Fatalf("stdout did not receive a JSON record: %v (%q)", err, out)
	}
	if rec["action"] != string(audit.ActionStartupAdopt) {
		t.Errorf("action = %v, want %q", rec["action"], audit.ActionStartupAdopt)
	}
}

// NewTo defaulting rather than panicking mirrors config.LoadFrom: a nil sink is
// a wiring mistake, and losing the trail to it would be worse than writing to
// the real destination.
func TestNewToDefaultsANilSinkAndClock(t *testing.T) {
	t.Parallel()

	if log := audit.NewTo(nil, nil); log == nil {
		t.Fatal("NewTo(nil, nil) = nil, want a usable Logger")
	}

	var buf bytes.Buffer
	if err := audit.NewTo(&buf, nil).Emit(audit.Record{Action: audit.ActionSessionCreate, Decision: audit.Allow}); err != nil {
		t.Fatalf("Emit = %v, want no error", err)
	}
	if _, ok := decode(t, buf.String())["time"]; !ok {
		t.Error("a nil clock produced no timestamp")
	}
}
