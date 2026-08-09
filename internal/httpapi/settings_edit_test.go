package httpapi

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/nctiggy/claude-remote-session-webhook/internal/config"
)

// editable is a fleet whose configuration is a real file on disk.
//
// The edit tests skipped without one, which is a test that asserts nothing while
// reporting success — the same shape as code with no caller, arriving in the
// suite instead of the daemon. A file under t.TempDir() is what makes them mean
// something.
func editable(t *testing.T) *fleet {
	t.Helper()

	path := filepath.Join(t.TempDir(), "config")
	// Complete, because Validate no longer borrows anything from a file on disk
	// (config.WithoutConfigFile). An incomplete fixture would refuse every edit
	// and the tests would pass for the wrong reason — which is exactly how this
	// suite passed on the author's machine and failed in CI.
	contents := strings.Join([]string{
		"# a configuration this test may rewrite",
		"shared_secret = " + strings.Repeat("k", 32),
		"allowed_roots = " + t.TempDir(),
		"access_team_domain = example.cloudflareaccess.com",
		"access_aud = " + strings.Repeat("a", 64),
		"access_allowed_emails = operator@example.com",
		"max_sessions = 4",
		"",
	}, "\n")
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	return settingsOn(t, func(cfg *config.Config) { cfg.FilePath = path })
}

// tokenIn lifts the page token out of a rendered page, which is what a browser
// does with it.
//
// Reading it from the markup rather than minting one directly is deliberate: it
// asserts, on the way past, that the page actually carried a token for its own
// form. A test that minted its own would pass against a page whose fields could
// never be submitted.
var tokenIn = regexp.MustCompile(`name="` + fieldPageToken + `" value="([^"]+)"`)

func editForm(t *testing.T, f *fleet, key, value string) url.Values {
	t.Helper()

	page := settingsSectionBody(t, f, settingSectionOf(key))
	found := tokenIn.FindStringSubmatch(page)
	if found == nil {
		t.Fatalf("the %s section rendered no page token, so its form could never be submitted:\n%s", key, page)
	}
	return url.Values{
		fieldPageToken:    {found[1]},
		fieldSettingKey:   {key},
		fieldSettingValue: {value},
	}
}

// editFormWithoutValue is what an unchecked checkbox submits: the token, the
// key, and no value field at all.
//
// It is built by deleting the field rather than by setting it empty, because
// those are the two states this task is about and url.Values.Encode is what
// tells them apart on the wire. A form carrying `value=` would exercise the
// branch that was already there.
func editFormWithoutValue(t *testing.T, f *fleet, key string) url.Values {
	t.Helper()

	form := editForm(t, f, key, "")
	form.Del(fieldSettingValue)
	return form
}

// editPost submits the way the page's form does: a genuine assertion, a
// same-origin initiator, and the token the page carried.
//
// It satisfies both halves of the cross-site gate rather than disabling either
// (AR-005). A test that turned one off would be asserting the handler's
// behaviour on a request this daemon will never see.
func editPost(t *testing.T, f *fleet, form url.Values) *httptest.ResponseRecorder {
	t.Helper()

	r := httptest.NewRequest(http.MethodPost, "/settings/edit", strings.NewReader(form.Encode()))
	r.Header.Set(headerContentType, contentTypeForm)
	r.Header.Set(headerAccessAssertion, f.keys.mint(t, f.keys.claims()))
	r.Header.Set(headerSecFetchSite, "same-origin")

	w := httptest.NewRecorder()
	f.ServeHTTP(w, r)
	return w
}

// TestEditWritesTheSetting is the write, end to end.
//
// **Must fail when** the route accepts an edit and the file is unchanged — the
// same shape as a route with no control: something reports success and nothing
// happened.
func TestEditWritesTheSetting(t *testing.T) {
	f := editable(t)

	editPost(t, f, editForm(t, f, "max_sessions", "3"))

	after, err := os.ReadFile(f.cfg.FilePath)
	if err != nil {
		t.Fatalf("read %s: %v", f.cfg.FilePath, err)
	}
	if !strings.Contains(string(after), "max_sessions = 3") {
		t.Errorf("the file does not carry the edit:\n%s", after)
	}
}

// TestEditRefusesASecret is the one exclusion, and the reason is narrower than
// "it is a boundary".
//
// **Must fail when** a secret becomes editable. A form that edited one would put
// it in the page, in the browser's history and in a POST body — undoing, in the
// same file, what this page spent a milestone learning by rendering `present`
// and never a value.
func TestEditRefusesASecret(t *testing.T) {
	f := editable(t)
	before, _ := os.ReadFile(f.cfg.FilePath) //nolint:errcheck // absence is compared as absence.

	form := editForm(t, f, "max_sessions", "3")
	form.Set(fieldSettingKey, "shared_secret")
	form.Set(fieldSettingValue, "a-value-an-attacker-chose")

	if w := editPost(t, f, form); w.Code == http.StatusSeeOther {
		t.Error("the settings page wrote a secret")
	}

	after, _ := os.ReadFile(f.cfg.FilePath) //nolint:errcheck // same.
	if string(before) != string(after) {
		t.Error("a refused edit changed the file")
	}
}

// TestEditRefusesAValueThatWouldNotLoad is what makes editing safe while
// running.
//
// **Must fail when** the candidate is written before it is validated, which
// leaves a daemon that runs fine until its next restart — the worst time to find
// out, because by then nobody is watching.
func TestEditRefusesAValueThatWouldNotLoad(t *testing.T) {
	f := editable(t)
	before, _ := os.ReadFile(f.cfg.FilePath) //nolint:errcheck // compared as-is.

	editPost(t, f, editForm(t, f, "max_sessions", "not a number"))

	after, _ := os.ReadFile(f.cfg.FilePath) //nolint:errcheck // same.
	if string(before) != string(after) {
		t.Errorf("a value the loader rejects was written anyway:\n%s", after)
	}
}

// TestEditTurnsABooleanOffWithNoValue is the checkbox's one HTTP fact, end to
// end: an unchecked box submits nothing at all.
//
// It turns the setting on and then off through the same route two clicks use,
// because the off is only interesting against an on — a fixture that was already
// false would pass whatever the handler did with the absence, which is the shape
// of a test that cannot fail.
//
// **Must fail when** the absence is read as an empty value: the file then ends
// at `discover_roots =`, a line loadBool happens to read as false today and no
// operator would write. The next boolean added to the loader is one whose
// default may not be false, and by then the coincidence is load-bearing.
func TestEditTurnsABooleanOffWithNoValue(t *testing.T) {
	f := editable(t)

	editPost(t, f, editForm(t, f, "discover_roots", "true"))
	editPost(t, f, editFormWithoutValue(t, f, "discover_roots"))

	after, err := os.ReadFile(f.cfg.FilePath)
	if err != nil {
		t.Fatalf("read %s: %v", f.cfg.FilePath, err)
	}
	if !strings.Contains(string(after), "discover_roots = false") {
		t.Errorf("an unchecked box did not turn the setting off:\n%s", after)
	}
}

// TestEditInventsFalseForNothingButABoolean is the half of this that is about
// security rather than about a checkbox.
//
// `false` is a value the handler invents, and a request that arrives without its
// value field — truncated, stripped by a proxy, or hand-built — is
// indistinguishable from an unchecked box. So the invention stops at the keys
// whose only other state *is* false. max_sessions is the cap the daemon refuses
// past rather than degrading the host (constitution VI); nothing that merely
// omitted a field may write a word into it.
//
// **Must fail when** the absence is read as `false` for every key. The candidate
// is then `max_sessions = false`, which Validate refuses — so the over-broad
// reading is caught by the outcome as well as by the bytes, and the assertion
// below on the empty value is what makes the difference visible at all.
func TestEditInventsFalseForNothingButABoolean(t *testing.T) {
	f := editable(t)

	editPost(t, f, editFormWithoutValue(t, f, "max_sessions"))

	after, err := os.ReadFile(f.cfg.FilePath)
	if err != nil {
		t.Fatalf("read %s: %v", f.cfg.FilePath, err)
	}
	if strings.Contains(string(after), "max_sessions = false") {
		t.Errorf("a request with no value wrote a word into a setting that is not a boolean:\n%s", after)
	}
	// The other direction, and the reason the assertion above is not enough on
	// its own: an absent value must still reach the loader as the empty one it
	// has always reached it as. Spelled with the newline because
	// "max_sessions = " is a prefix of the line this test exists to refuse.
	if !strings.Contains(string(after), "max_sessions = \n") {
		t.Errorf("a missing value stopped behaving as an empty one:\n%s", after)
	}
}

// TestEditKeepsWhatItReplaced is the backup, which is the only thing between a
// mistyped bound and a file the operator has to reconstruct.
func TestEditKeepsWhatItReplaced(t *testing.T) {
	f := editable(t)
	before, _ := os.ReadFile(f.cfg.FilePath) //nolint:errcheck // compared below.

	editPost(t, f, editForm(t, f, "max_sessions", "2"))

	kept, err := os.ReadFile(config.BackupPath(f.cfg.FilePath))
	if err != nil {
		t.Fatalf("no backup was kept: %v", err)
	}
	if string(kept) != string(before) {
		t.Error("the backup is not what the file held before the edit")
	}
}

// TestTheTickedSwitchIsAValueTheLoaderAccepts closes the loop the two tests
// above open: an unchecked box turns a setting off, and this is the tick.
//
// What it submits is lifted out of the rendered markup rather than typed here,
// so the value under test is the one a browser would actually send. That is the
// whole assertion. A checkbox with no `value` submits `on`, ParseBool refuses
// `on`, Validate refuses the candidate file — and the operator ticks a box,
// presses Save, and watches nothing happen, with the daemon reporting a refusal
// about a value they never chose.
//
// **Must fail when** the template's `value="true"` is dropped or misspelled.
func TestTheTickedSwitchIsAValueTheLoaderAccepts(t *testing.T) {
	f := editable(t)

	control := settingControl(t, settingsEverySection(t, f), "discover_roots")
	found := settingControlValue.FindStringSubmatch(control)
	if found == nil {
		t.Fatalf("the switch carries no value at all: %q", control)
	}

	editPost(t, f, editForm(t, f, "discover_roots", found[1]))

	after, err := os.ReadFile(f.cfg.FilePath)
	if err != nil {
		t.Fatalf("read %s: %v", f.cfg.FilePath, err)
	}
	if !strings.Contains(string(after), "discover_roots = "+boolOn) {
		t.Errorf("what the ticked box submits (%q) did not turn the setting on; the loader would not take it:\n%s", found[1], after)
	}
}
