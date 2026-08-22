package config_test

// The configuration file's grammar, its refusals, its mode, and its absence
// (#65, T003, T004, T005 and T006). The grammar cases drive config.ParseFile
// with bytes, because the grammar is about the contents of a file rather than
// about the file on disk; the cases from TestGroupReadableWithSecretRefuses
// onwards drive config.ReadFile against a real file, because a mode, an absence
// and an mtime are not things bytes have.
//
// Two of the grammar cases are the reason the format is what it is. A `#` inside
// a value belongs to the value, and a value may contain `=`. Both look like
// pedantry until the value is a shared secret or a command line.
//
// The refusal cases share one obligation, and TestErrorNeverContainsValue is
// where it is stated outright: a refusal names the file, the line, and at most
// the key. The line it refused may be the shared secret with a typo in it, and a
// startup error goes to stderr and stays in the journal forever.

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/nctiggy/claude-remote-session-webhook/internal/config"
)

// hexSecret is a 64-character value that is entirely inside the key alphabet:
// what `openssl rand -hex 32` produces, and therefore what a shared secret
// pasted onto a line without its key looks like to this parser.
const hexSecret = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

// workedExample is the file from specs/004-configure-and-operate/contracts/config-file.md,
// verbatim. It is copied rather than referenced so that changing the contract
// without changing the parser is a red build.
const workedExample = `# crswd configuration. Comments explain why a bound is what it is,
# which is the reason this file is not JSON.
version = 1

listen = 127.0.0.1:8787

# The containment boundary. A session may not be created outside these.
allowed_roots = /home/nctiggy/code,/home/nctiggy/work

# name=command pairs. Note the "=" inside the value: the parser splits
# on the FIRST "=" only, which is why this line means what it looks like.
start_commands = default=claude --dangerously-skip-permissions,rc=claude remote-control --permission-mode bypassPermissions

# Sessions live a day unless told otherwise. The word never is spellable on
# the ceiling below it, which is what lets one create ask for a session that
# never expires.
session_lifetime = 24h
session_lifetime_max = never

# This value contains a "#". It is not a comment, because a comment
# marker is only a comment marker at the start of a line.
shared_secret = hunter2#not-a-comment
`

func TestParseAcceptsWorkedExample(t *testing.T) {
	t.Parallel()

	f, err := config.ParseFile("config", []byte(workedExample), io.Discard)
	if err != nil {
		t.Fatalf("ParseFile() rejected the worked example: %v", err)
	}

	want := map[string]string{ //nolint:gosec // G101 false positive: these are the expected *parse results* of the worked example in contracts/config-file.md. The "secret" is a fixture chosen to carry a "#" precisely so this test proves a value is not truncated at one; it authenticates nothing and exists nowhere but here.
		"listen":               "127.0.0.1:8787",
		"allowed_roots":        "/home/nctiggy/code,/home/nctiggy/work",
		"start_commands":       "default=claude --dangerously-skip-permissions,rc=claude remote-control --permission-mode bypassPermissions",
		"session_lifetime":     "24h",
		"session_lifetime_max": "never",
		"shared_secret":        "hunter2#not-a-comment",
	}
	for key, value := range want {
		got, ok := f.Lookup(config.VarForKey(key))
		if !ok {
			t.Errorf("the worked example does not set %s", key)
			continue
		}
		if got != value {
			t.Errorf("%s = %q, want %q", key, got, value)
		}
	}

	// A comment line the parser stopped skipping would not quietly add a key:
	// `# name=command pairs...` cuts to a key of `# name`, which the alphabet
	// refuses, so the parse above would already have failed. What this catches
	// is the other direction — a parser inventing a setting nothing wrote.
	if _, ok := f.Lookup(config.EnvMaxSessions); ok {
		t.Errorf("the worked example does not mention %s and the parser produced one anyway", config.EnvMaxSessions)
	}

	// The example's `version = 1` is accepted — the parse above succeeded — and
	// is not a setting. It maps to no environment variable, so a settings page
	// that rendered it would be showing a row nothing can change the meaning of.
	//
	// The contract's prose beneath the example says it "yields exactly eight
	// keys" and the example sets seven, of which this is one. The example itself
	// is unambiguous about what parses to what; the count is what is wrong.
	if v, ok := f.Lookup(config.VarForKey("version")); ok {
		t.Errorf("version = %q was kept as a setting; it is the schema the file was written against, not something the daemon reads", v)
	}
}

// A `#` after a value is part of the value. Stripping from the first one would
// truncate a shared secret into a daemon that starts, looks healthy, and rejects
// every request — a failure with no symptom pointing at the config file.
func TestValueMayContainHash(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		contents string
		key      string
		want     string
		absent   bool
	}{
		{name: "the contract's own value", contents: "shared_secret = hunter2#not-a-comment\n", key: "shared_secret", want: "hunter2#not-a-comment"},
		{name: "a hash with spaces around it", contents: "shared_secret = a # b\n", key: "shared_secret", want: "a # b"},
		{name: "a value that is only a hash", contents: "shared_secret = #\n", key: "shared_secret", want: "#"},
		{name: "a whole-line comment sets nothing", contents: "# shared_secret = x\n", key: "shared_secret", absent: true},
		{name: "an indented comment sets nothing", contents: "   \t# shared_secret = x\n", key: "shared_secret", absent: true},
		{name: "a comment with no space sets nothing", contents: "#shared_secret = x\n", key: "shared_secret", absent: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f, err := config.ParseFile("config", []byte(tc.contents), io.Discard)
			if err != nil {
				t.Fatalf("ParseFile(%q): %v", tc.contents, err)
			}
			got, ok := f.Lookup(config.VarForKey(tc.key))
			if tc.absent {
				if ok {
					t.Errorf("a comment set %s to %q", tc.key, got)
				}
				return
			}
			if !ok {
				t.Fatalf("%s was not set", tc.key)
			}
			if got != tc.want {
				t.Errorf("%s = %q, want %q", tc.key, got, tc.want)
			}
		})
	}
}

// The separator is the first `=`. start_commands always carries `=` inside its
// value, so a parser using strings.Split would refuse valid configuration — or,
// worse, keep only the first fragment of a command line.
func TestValueMayContainEquals(t *testing.T) {
	t.Parallel()

	const both = "default=claude --dangerously-skip-permissions,rc=claude remote-control --permission-mode bypassPermissions"

	cases := []struct {
		name     string
		contents string
		key      string
		want     string
	}{
		{name: "the contract's own value", contents: "start_commands = " + both + "\n", key: "start_commands", want: both},
		{name: "every separator after the first belongs to the value", contents: "start_commands = b=c=d\n", key: "start_commands", want: "b=c=d"},
		{name: "a value that is only a separator", contents: "start_commands = =\n", key: "start_commands", want: "="},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f, err := config.ParseFile("config", []byte(tc.contents), io.Discard)
			if err != nil {
				t.Fatalf("ParseFile(%q): %v", tc.contents, err)
			}
			got, ok := f.Lookup(config.VarForKey(tc.key))
			if !ok {
				t.Fatalf("%s was not set", tc.key)
			}
			if got != tc.want {
				t.Errorf("%s = %q, want %q — the separator is the first `=` and no other", tc.key, got, tc.want)
			}
		})
	}
}

// The fixture is a real key rather than a one-letter placeholder because T004
// refuses a key the daemon does not read, and `a` is not one. Whitespace is
// still the only thing under test.
func TestWhitespaceAroundSeparatorIgnored(t *testing.T) {
	t.Parallel()

	for _, contents := range []string{
		"listen=b\n",
		"listen = b\n",
		"  listen  =  b  \n",
		"\tlisten\t=\tb\t\n",
		"listen =b\n",
		"listen= b\n",
		// The last line of a file need not end in one.
		"listen = b",
		// CRLF is a line ending, not part of the value.
		"listen = b\r\n",
	} {
		t.Run(strings.ReplaceAll(contents, "\n", `\n`), func(t *testing.T) {
			t.Parallel()

			f, err := config.ParseFile("config", []byte(contents), io.Discard)
			if err != nil {
				t.Fatalf("ParseFile(%q): %v", contents, err)
			}
			got, ok := f.Lookup(config.EnvListen)
			if !ok {
				t.Fatalf("ParseFile(%q) set no key `listen`", contents)
			}
			if got != "b" {
				t.Errorf("listen = %q, want %q", got, "b")
			}
		})
	}
}

// Only the ends are trimmed. The spaces inside a value are the operator's:
// start_commands holds whole command lines, and collapsing them changes what
// runs on this host.
func TestWhitespaceInsideValuePreserved(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		want string
	}{
		{name: "a command line's single spaces", want: "default=claude --dangerously-skip-permissions"},
		{name: "runs of spaces are not collapsed", want: "default=claude  --dangerously-skip-permissions"},
		{name: "a tab inside a value survives", want: "default=claude\t--dangerously-skip-permissions"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f, err := config.ParseFile("config", []byte("  start_commands   =   "+tc.want+"   \n"), io.Discard)
			if err != nil {
				t.Fatalf("ParseFile(): %v", err)
			}
			got, ok := f.Lookup(config.EnvStartCommands)
			if !ok {
				t.Fatal("start_commands was not set")
			}
			if got != tc.want {
				t.Errorf("start_commands = %q, want %q", got, tc.want)
			}
		})
	}
}

// A key is its environment variable minus CRSW_, lower-cased. That is a rule
// rather than a table, so this asserts it against every variable config.go
// actually declares: a variable added there is readable from a file the same
// day, or this fails.
func TestKeyForVarAndVarForKeyAreInverses(t *testing.T) {
	t.Parallel()

	for name := range declaredVars(t) {
		key := config.KeyForVar(name)
		if key == "" || strings.ContainsAny(key, " ABCDEFGHIJKLMNOPQRSTUVWXYZ") {
			t.Errorf("KeyForVar(%s) = %q, which is not a key this parser accepts", name, key)
		}
		if got := config.VarForKey(key); got != name {
			t.Errorf("VarForKey(KeyForVar(%s)) = %s", name, got)
		}
		if _, err := config.ParseFile("config", []byte(key+" = x\n"), io.Discard); err != nil {
			t.Errorf("the parser refuses %q, which is how %s is spelled in a file: %v", key, name, err)
		}
	}
}

// A line the grammar does not describe is refused, and the refusal says where
// and never what. The line may be the shared secret with a typo in it, and a
// startup error is written to stderr and kept in the journal forever.
func TestALineThatIsNotAPairIsRefusedWithoutQuotingIt(t *testing.T) {
	t.Parallel()

	// Each fixture puts hexSecret in a position the parser refuses: alone on a
	// line, on the value side of a line with no key, and in the key position,
	// where a secret pasted without its key would otherwise land.
	cases := []struct {
		name     string
		contents string
		wantLine string
	}{
		{name: "no separator", contents: "listen = x\n" + hexSecret + "\n", wantLine: "config:2"},
		{name: "no key", contents: "= " + hexSecret + "\n", wantLine: "config:1"},
		{name: "a key outside the alphabet", contents: "Shared_Secret = " + hexSecret + "\n", wantLine: "config:1"},
		{name: "a key with a space in it", contents: "shared secret = " + hexSecret + "\n", wantLine: "config:1"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, err := config.ParseFile("config", []byte(tc.contents), io.Discard)
			if err == nil {
				t.Fatalf("ParseFile() accepted %s", tc.name)
			}
			if !strings.Contains(err.Error(), tc.wantLine) {
				t.Errorf("error = %q, want it to name %s — a line number is the whole message", err, tc.wantLine)
			}
			if strings.Contains(err.Error(), hexSecret) {
				t.Errorf("the refusal quoted the line back: %q", err)
			}
		})
	}
}

// A mixed-case key is refused rather than folded, and that is a security
// property rather than tidiness: IsSecret matches its two keys exactly, so a
// `Shared_Secret` accepted as written would hold a secret under a key nothing
// classifies as one, and T005's mode check would not fire on the file.
func TestAKeyIsNeverFoldedToLowerCase(t *testing.T) {
	t.Parallel()

	if _, err := config.ParseFile("config", []byte("Shared_Secret = x\n"), io.Discard); err == nil {
		t.Fatal("ParseFile() accepted a mixed-case key")
	}
	if !config.IsSecret("shared_secret") {
		t.Fatal("IsSecret no longer classifies the key this test is about")
	}
	if config.IsSecret("Shared_Secret") {
		t.Fatal("IsSecret folds case, so refusing a mixed-case key is no longer what protects it")
	}
}

// No file at all is the deployment configured entirely by environment
// variables, which this daemon has always supported. It answers "not set"
// rather than panicking on the way to saying so.
func TestLookupOnNoFileAnswersAbsent(t *testing.T) {
	t.Parallel()

	var f *config.File
	if v, ok := f.Lookup(config.EnvListen); ok {
		t.Errorf("a nil *File answered %s with %q", config.EnvListen, v)
	}
	if p := f.Path(); p != "" {
		t.Errorf("a nil *File named the file %q", p)
	}
}

// ---------------------------------------------------------------------------
// T004 — the file-level refusals.
// ---------------------------------------------------------------------------

// A misspelled key that quietly did nothing is how a containment boundary ends
// up unset on a daemon whose operator believes they set it. Skipping is wrong,
// and so is warning: the operator wrote a line meaning to change something, and
// the only answer that cannot be missed is refusing to start.
func TestUnknownKeyRefuses(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		contents string
		wantKey  string
		wantLine string
	}{
		{name: "a misspelled boundary", contents: "shared_secret = x\nalowed_roots = /tmp\n", wantKey: "alowed_roots", wantLine: "config:2"},
		{name: "a key from another daemon", contents: "port = 8080\n", wantKey: "port", wantLine: "config:1"},
		// The prefix belongs to the variable, not to the key. Spelling it in a
		// file is a plausible mistake and it is still not a key.
		{name: "the variable spelling", contents: "crsw_listen = x\n", wantKey: "crsw_listen", wantLine: "config:1"},
		// Plural, singular, and a key that merely starts like a real one: near
		// misses are the whole population this refusal exists for.
		{name: "a near miss", contents: "allowed_root = /tmp\n", wantKey: "allowed_root", wantLine: "config:1"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f, err := config.ParseFile("config", []byte(tc.contents), io.Discard)
			if err == nil {
				t.Fatalf("ParseFile() accepted %s; a key that sets nothing must not parse", tc.wantKey)
			}
			if f != nil {
				t.Errorf("ParseFile() returned a *File alongside its refusal; a caller could start on it")
			}
			msg := err.Error()
			if !strings.Contains(msg, tc.wantKey) {
				t.Errorf("error = %q, want the misspelling %q named — an operator has to know which line to fix", msg, tc.wantKey)
			}
			if !strings.Contains(msg, tc.wantLine) {
				t.Errorf("error = %q, want it to name %s", msg, tc.wantLine)
			}
		})
	}
}

// Every key config.go declares parses. This is the other half of the unknown-key
// refusal: a variable the list forgets becomes a real setting an operator is
// told does not exist, and the message would be confidently wrong.
func TestVarsNamesEveryDeclaredVariable(t *testing.T) {
	t.Parallel()

	declared := declaredVars(t)
	listed := make(map[string]bool)
	for _, name := range config.Vars() {
		if listed[name] {
			t.Errorf("config.Vars() names %s twice", name)
		}
		listed[name] = true
	}

	for name := range declared {
		if !listed[name] {
			t.Errorf("config.go declares %s and config.Vars() omits it, so `%s` in a config file would be refused as unknown",
				name, config.KeyForVar(name))
		}
	}
	for name := range listed {
		if !declared[name] {
			t.Errorf("config.Vars() names %s and config.go does not declare it", name)
		}
	}
}

// A rename is not a typo, and treating it as one would make every rename a
// breaking change for every operator with a file. The former spelling loads, and
// startup says loudly that both spellings name one setting and which to move to.
//
// renamedKeys is empty and stays empty until a rename actually ships, so this
// drives the mechanism with a fixture table through the package's test-only
// seam. A mechanism first exercised by the release that depends on it is a
// mechanism proven in production.
func TestRenamedKeyWarnsAndAccepts(t *testing.T) {
	t.Parallel()

	renames := map[string]string{"bind_address": "listen"}

	var warn bytes.Buffer
	f, err := config.ParseFileWithRenames("config", []byte("bind_address = 127.0.0.1:9999\n"), renames, &warn)
	if err != nil {
		t.Fatalf("ParseFile() refused a renamed key instead of accepting it: %v", err)
	}

	// It sets the setting, under the current spelling — the point of a rename is
	// that the old file keeps working, not that it parses and does nothing.
	got, ok := f.Lookup(config.EnvListen)
	if !ok {
		t.Fatal("the former spelling parsed and set nothing")
	}
	if got != "127.0.0.1:9999" {
		t.Errorf("listen = %q, want the value written under the former spelling", got)
	}

	banner := warn.String()
	for _, want := range []string{"bind_address", "listen", "config:1"} {
		if !strings.Contains(banner, want) {
			t.Errorf("warning = %q, want it to name %q — an operator who is not told both spellings cannot act on it", banner, want)
		}
	}

	// The warning is a warning. A refusal here would be the breaking change the
	// rename table exists to avoid.
	if strings.Contains(banner, "refusing to start") {
		t.Errorf("warning = %q, but a renamed key is accepted", banner)
	}

	// And the table itself ships empty: a rename entered before it happens tells
	// operators to migrate off a key no released daemon ever read.
	if n := config.RenamedKeys(); n != 0 {
		t.Errorf("renamedKeys carries %d entries; nothing has been renamed yet, and a rename of a spelling that never shipped invents version skew", n)
	}
}

// A line the grammar does not describe is refused rather than skipped. Skipping
// is worse than it sounds: the line the operator got wrong is precisely the one
// they were trying to change.
func TestMalformedLineRefuses(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		contents string
		wantLine string
	}{
		{name: "a bare word", contents: "listen = x\nnonsense\n", wantLine: "config:2"},
		{name: "a key with no separator", contents: "allowed_roots /tmp\n", wantLine: "config:1"},
		{name: "no key before the separator", contents: "= 127.0.0.1:8765\n", wantLine: "config:1"},
		{name: "a section header from another format", contents: "[server]\n", wantLine: "config:1"},
		{name: "yaml nesting", contents: "listen = x\n  timeout: 5\n", wantLine: "config:2"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if _, err := config.ParseFile("config", []byte(tc.contents), io.Discard); err == nil {
				t.Fatalf("ParseFile() accepted %s", tc.name)
			} else if !strings.Contains(err.Error(), tc.wantLine) {
				t.Errorf("error = %q, want it to name %s", err, tc.wantLine)
			}
		})
	}
}

// Which of two lines wins is not a decision to leave to a parser. Last-wins and
// first-wins are both defensible and an operator cannot see which one they got,
// so the file is refused and they say what they mean.
func TestRepeatedKeyRefuses(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		contents string
		wantKey  string
		// Both lines are named, so the operator can go and delete one of them.
		wantRepeat string
		wantFirst  string
	}{
		{name: "adjacent", contents: "max_sessions = 1\nmax_sessions = 2\n", wantKey: "max_sessions", wantRepeat: "config:2", wantFirst: "line 1"},
		{name: "separated by comments and blanks", contents: "listen = a\n\n# a note\nlisten = b\n", wantKey: "listen", wantRepeat: "config:4", wantFirst: "line 1"},
		{name: "differing only in whitespace", contents: "listen=a\n  listen  =  b  \n", wantKey: "listen", wantRepeat: "config:2", wantFirst: "line 1"},
		{name: "the version key twice", contents: "version = 1\nversion = 1\n", wantKey: "version", wantRepeat: "config:2", wantFirst: "line 1"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, err := config.ParseFile("config", []byte(tc.contents), io.Discard)
			if err == nil {
				t.Fatalf("ParseFile() accepted %s and silently picked a winner", tc.name)
			}
			msg := err.Error()
			if !strings.Contains(msg, tc.wantKey) {
				t.Errorf("error = %q, want the repeated key %q named", msg, tc.wantKey)
			}
			for _, want := range []string{tc.wantRepeat, tc.wantFirst} {
				if !strings.Contains(msg, want) {
					t.Errorf("error = %q, want it to contain %q", msg, want)
				}
			}
		})
	}
}

// Two spellings of one setting are a repetition even when the file writes them
// differently. Checked the other way round they look like two keys, and one
// silently overwrites the other — the exact outcome this refusal prevents.
func TestARenamedKeyRepeatsItsCurrentSpelling(t *testing.T) {
	t.Parallel()

	renames := map[string]string{"bind_address": "listen"}

	for _, contents := range []string{
		"bind_address = a\nlisten = b\n",
		"listen = a\nbind_address = b\n",
	} {
		t.Run(strings.ReplaceAll(contents, "\n", `\n`), func(t *testing.T) {
			t.Parallel()

			_, err := config.ParseFileWithRenames("config", []byte(contents), renames, io.Discard)
			if err == nil {
				t.Fatalf("ParseFile() accepted %q; both lines set listen", contents)
			}
			if !strings.Contains(err.Error(), "listen") {
				t.Errorf("error = %q, want the setting both lines name", err)
			}
		})
	}
}

// A file outlives the binary that reads it. A version from a newer daemon is
// refused rather than read optimistically: the reason to bump the schema is that
// a key changed meaning, and guessing at the new meaning is how a containment
// boundary ends up set to something the operator did not write.
func TestFutureVersionRefuses(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		contents string
		want     []string
		// ok marks a version this daemon still reads, which is every schema up
		// to and including its own.
		ok bool
	}{
		{name: "a version from a newer daemon", contents: "version = 99\n", want: []string{"99", "config:1"}},
		{name: "one past this one", contents: fmt.Sprintf("version = %d\n", config.SchemaVersion+1), want: []string{strconv.Itoa(config.SchemaVersion + 1), "config:1"}},
		// The schema before this one still loads. Bumping the version is how a
		// retirement is recorded, not a reason to refuse every file written
		// before it — an operator whose file predates schema 2 and sets no
		// retired key has nothing to change.
		{name: "the schema before this one", contents: "version = 1\nmax_sessions = 2\n", ok: true},
		{name: "not a number", contents: "version = one\n", want: []string{"whole number", "config:1"}},
		{name: "a decimal", contents: "version = 1.0\n", want: []string{"whole number"}},
		{name: "before the first schema", contents: "version = 0\n", want: []string{"the first schema is 1"}},
		{name: "negative", contents: "version = -3\n", want: []string{"the first schema is 1"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, err := config.ParseFile("config", []byte(tc.contents), io.Discard)
			if tc.ok {
				if err != nil {
					t.Fatalf("ParseFile() refused %s: %v", tc.name, err)
				}
				return
			}
			if err == nil {
				t.Fatalf("ParseFile() accepted %s, so the version key is being ignored", tc.name)
			}
			for _, want := range tc.want {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error = %q, want it to contain %q", err, want)
				}
			}
		})
	}

	// The version this daemon writes for is accepted, and is not a setting.
	f, err := config.ParseFile("config", []byte("version = 1\nlisten = x\n"), io.Discard)
	if err != nil {
		t.Fatalf("ParseFile() refused version %d: %v", config.SchemaVersion, err)
	}
	if v, ok := f.Lookup(config.VarForKey("version")); ok {
		t.Errorf("version = %q was kept as a setting", v)
	}
}

// The rule every refusal in this file is written to keep. A startup error goes
// to stderr and stays in the journal forever, and the line that was refused may
// be the shared secret with a typo in it — so a message names the file, the
// line, and at most a key short enough to be a key.
func TestErrorNeverContainsValue(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		contents string
	}{
		{name: "an unknown key holding a secret", contents: "shard_secret = " + hexSecret + "\n"},
		{name: "a repeated key holding a secret", contents: "shared_secret = " + hexSecret + "\nshared_secret = x\n"},
		{name: "a malformed line that is a secret", contents: hexSecret + "\n"},
		{name: "a secret pasted where a key belongs", contents: hexSecret + " = x\n"},
		{name: "a secret pasted where a version belongs", contents: "version = " + hexSecret + "\n"},
		{name: "a secret on the value side of a keyless line", contents: "= " + hexSecret + "\n"},
		{name: "a key outside the alphabet holding a secret", contents: "Shared_Secret = " + hexSecret + "\n"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, err := config.ParseFile("config", []byte(tc.contents), io.Discard)
			if err == nil {
				t.Fatalf("ParseFile() accepted %s", tc.name)
			}
			if strings.Contains(err.Error(), hexSecret) {
				t.Errorf("the refusal carried the value it was refusing to print: %q", err)
			}
		})
	}
}

// The bound that makes quoting a key safe at all. A key is quoted back so an
// operator can see their misspelling; 64 characters inside [a-z0-9_] is what
// `openssl rand -hex 32` produces, so past the bound the message says the line
// number and nothing else.
func TestAnOverlongKeyIsRefusedWithoutQuotingIt(t *testing.T) {
	t.Parallel()

	// One character over is refused; one under is quoted, which is what makes
	// the bound the thing under test rather than the length of the fixture.
	long := strings.Repeat("k", 33)
	short := strings.Repeat("k", 32)

	_, err := config.ParseFile("config", []byte(long+" = x\n"), io.Discard)
	if err == nil {
		t.Fatal("ParseFile() accepted a key longer than any this daemon has")
	}
	if strings.Contains(err.Error(), long) {
		t.Errorf("the refusal quoted a secret-length key back: %q", err)
	}

	_, err = config.ParseFile("config", []byte(short+" = x\n"), io.Discard)
	if err == nil {
		t.Fatal("ParseFile() accepted an unknown key at the bound")
	}
	if !strings.Contains(err.Error(), short) {
		t.Errorf("error = %q, want a key at the bound named — the refusal is useless if no key is ever quoted", err)
	}
}

// hunter2 is what a shared secret looks like on a line of a real file, `#` and
// all — the contract's own example value. The refusals below must never print
// it, and the file holding it must never be one another account can read.
//
// It is named for the value rather than for what the value is because gosec
// G101 reads the *name*: a const called anything with "secret" in it and a
// literal on the right is a hardcoded credential to the linter, whether or not
// the file is a test fixture.
const hunter2 = "hunter2#not-a-comment"

// writeConfig writes a fixture config file and sets its mode explicitly.
//
// The mode argument to os.WriteFile passes through the umask, so a fixture that
// relied on it would be testing the umask of whoever ran the suite. os.Chmod
// does not, and these cases are about exact modes.
func writeConfig(t *testing.T, contents string, mode os.FileMode) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "config")
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("write the fixture config file: %v", err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatalf("set the fixture config file to mode %04o: %v", mode, err)
	}
	return path
}

// A secret in a file another account can read has already leaked, so the daemon
// refuses rather than starting and never mentioning it (FR-007). Group and world
// are one test: a group-readable file is readable by however many accounts that
// group holds, which is not a number this daemon can know.
func TestGroupReadableWithSecretRefuses(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		contents string
		mode     os.FileMode
	}{
		{name: "group readable", contents: "shared_secret = " + hunter2 + "\n", mode: 0o640},
		{name: "world readable", contents: "shared_secret = " + hunter2 + "\n", mode: 0o604},
		{name: "group and world readable", contents: "shared_secret = " + hunter2 + "\n", mode: 0o644},
		// Not a leak but the other direction: a file the group can write is a
		// file whose start_commands another account chooses.
		{name: "group writable", contents: "shared_secret = " + hunter2 + "\n", mode: 0o620},
		// The allowlist authenticates nobody and still names who may reach a
		// daemon that runs unsandboxed code. It is secret because IsSecret says
		// so, which is the only place that says so.
		{name: "the allowlist alone is enough", contents: "access_allowed_emails = nctiggy@gmail.com\n", mode: 0o644},
		// The password door's own credential, and the reason it is worth a row of
		// its own: it arrived a milestone after this check, and it is secret here
		// for the same reason it is unrenderable and uneditable — because
		// IsSecret names it, in one place, for all three callers.
		{name: "the dashboard password alone is enough", contents: "dashboard_password = " + hunter2 + "\n", mode: 0o644},
		{name: "a secret among ordinary settings", contents: "listen = 127.0.0.1:8787\nallowed_roots = /home/nctiggy/code\nshared_secret = " + hunter2 + "\n", mode: 0o644},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			path := writeConfig(t, tc.contents, tc.mode)

			f, err := config.ReadFile(path, io.Discard)
			if err == nil {
				t.Fatalf("ReadFile() started on a mode %04o file holding a secret", tc.mode)
			}
			if f != nil {
				t.Errorf("ReadFile() refused and returned values anyway; a caller reading them past the error would run on the file that was refused")
			}

			// The remedy has to be in the message. An operator who is told their
			// file is wrong and not what to run about it is an operator who will
			// reach for the environment variable instead.
			if !strings.Contains(err.Error(), path) {
				t.Errorf("error = %q, want the file named — an operator with two config files cannot act on this otherwise", err)
			}
			if want := fmt.Sprintf("chmod 600 %s", path); !strings.Contains(err.Error(), want) {
				t.Errorf("error = %q, want it to name %q", err, want)
			}
			if want := fmt.Sprintf("mode %04o", tc.mode.Perm()); !strings.Contains(err.Error(), want) {
				t.Errorf("error = %q, want it to name %q", err, want)
			}
			if strings.Contains(err.Error(), hunter2) {
				t.Errorf("the refusal printed the secret it was refusing to leave readable: %q", err)
			}
		})
	}
}

// The refusal in the other direction, which is a bug of the same kind. A file
// holding only allowed_roots is not a secret file, and a daemon that refused to
// start over its mode would be demanding a change that protects nothing.
func TestGroupReadableWithoutSecretStarts(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		contents string
		mode     os.FileMode
		key      string
		want     string
	}{
		{
			name:     "the containment boundary is readable on purpose",
			contents: "allowed_roots = /home/nctiggy/code\n",
			mode:     0o644,
			key:      "allowed_roots",
			want:     "/home/nctiggy/code",
		},
		{
			name:     "world readable and world writable",
			contents: "listen = 127.0.0.1:8787\n",
			mode:     0o666,
			key:      "listen",
			want:     "127.0.0.1:8787",
		},
		{
			name:     "a key whose name merely resembles one",
			contents: "start_commands = default=claude --dangerously-skip-permissions\n",
			mode:     0o640,
			key:      "start_commands",
			want:     "default=claude --dangerously-skip-permissions",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			path := writeConfig(t, tc.contents, tc.mode)

			f, err := config.ReadFile(path, io.Discard)
			if err != nil {
				t.Fatalf("ReadFile() refused a mode %04o file holding no secret: %v", tc.mode, err)
			}

			got, ok := f.Lookup(config.VarForKey(tc.key))
			if !ok {
				t.Fatalf("ReadFile() started and did not read %s; the mode check is not the only thing this function does", tc.key)
			}
			if got != tc.want {
				t.Errorf("%s = %q, want %q", tc.key, got, tc.want)
			}
			if f.Path() != path {
				t.Errorf("Path() = %q, want %q — the settings page names this file to answer \"why did my edit do nothing?\"", f.Path(), path)
			}
		})
	}
}

// The mode the operator is told to set has to be the mode that starts, or the
// refusal sends them somewhere that refuses them again.
func TestOwnerOnlyModesWithSecretStart(t *testing.T) {
	t.Parallel()

	for _, mode := range []os.FileMode{0o600, 0o400} {
		t.Run(fmt.Sprintf("%04o", mode), func(t *testing.T) {
			t.Parallel()

			path := writeConfig(t, "shared_secret = "+hunter2+"\n", mode)

			f, err := config.ReadFile(path, io.Discard)
			if err != nil {
				t.Fatalf("ReadFile() refused a mode %04o file, which is what the refusal tells operators to run: %v", mode, err)
			}
			if got, _ := f.Lookup(config.EnvSharedSecret); got != hunter2 {
				t.Errorf("shared_secret = %q, want %q", got, hunter2)
			}
		})
	}
}

// The read is bounded before anything looks at what was read. The path comes
// from the operator's own environment, and an io.ReadAll pointed at /dev/zero by
// a typo in a unit file is a daemon that never starts and never says why.
func TestAnOversizeFileIsRefused(t *testing.T) {
	t.Parallel()

	// A single comment line, so nothing but the bound can refuse it: were the
	// check removed, this file parses to zero settings and no error.
	oversize := strings.Repeat("#", config.MaxConfigFileBytes+1)

	path := writeConfig(t, oversize, 0o600)
	if _, err := config.ReadFile(path, io.Discard); err == nil {
		t.Fatal("ReadFile() read a file past the bound")
	}

	atBound := strings.Repeat("#", config.MaxConfigFileBytes)
	path = writeConfig(t, atBound, 0o600)
	if _, err := config.ReadFile(path, io.Discard); err != nil {
		t.Errorf("ReadFile() refused a file exactly at the bound: %v", err)
	}
}

// ---------------------------------------------------------------------------
// T006 — absence, and the absence of a write path.
// ---------------------------------------------------------------------------

// No file is not a misconfiguration: it is the configuration every deployment of
// this daemon has today, because until this milestone there was no file to have
// (FR-003). A refusal here would take all of them down on upgrade, which is what
// SC-002 is for.
func TestMissingFileIsNotAnError(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	cases := []struct {
		name string
		path string
	}{
		{name: "no file in a directory that exists", path: filepath.Join(dir, "config")},
		// The likelier shape by far: nobody has run this daemon with a file, so
		// ~/.config/crswd does not exist either.
		{name: "no directory to hold one", path: filepath.Join(dir, "crswd", "config")},
		{name: "nothing anywhere along the path", path: filepath.Join(dir, "a", "b", "c", "config")},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f, err := config.ReadFile(tc.path, io.Discard)
			if err != nil {
				t.Fatalf("ReadFile() refused to start over a file nobody wrote: %v", err)
			}

			// Empty, and empty in the way the precedence shim asks: every
			// variable answers "not set", so every value falls through to the
			// environment and then to the default it has today.
			for _, name := range config.Vars() {
				if v, ok := f.Lookup(name); ok {
					t.Errorf("a file that does not exist set %s to %q", name, v)
				}
			}

			// And it names no file. The settings page says "no configuration
			// file was read" from this, and a path here would have it name a
			// file the operator can go and look for in vain.
			if p := f.Path(); p != "" {
				t.Errorf("Path() = %q for a file that does not exist", p)
			}
		})
	}
}

// A file that exists and cannot be read is still a refusal. The two are one
// branch apart and collapsing them is the plausible mistake: an operator whose
// file is owned by root gets a daemon running on none of the bounds they wrote,
// and nothing anywhere says so.
func TestAnUnreadableFileIsStillARefusal(t *testing.T) {
	t.Parallel()

	// A plain file where a directory belongs is the one case reachable without
	// being able to change owners: opening under it fails ENOTDIR, which is not
	// absence however much it looks like it from the path.
	notADir := writeConfig(t, "listen = 127.0.0.1:8787\n", 0o600)
	path := filepath.Join(notADir, "config")

	if _, err := config.ReadFile(path, io.Discard); err == nil {
		t.Fatal("ReadFile() treated an unreadable path as an absent file, so a daemon would start on none of the settings the operator wrote")
	}
}

// file.go has no write path at all — not a formatter, not a normaliser, not an
// "upgrade in place". The operator's file is the operator's, and under source
// control a reformat is a diff nobody asked for. Reading one is never an
// occasion to write one (FR-008); write.go is where that happens, and only
// because somebody asked.
func TestParserNeverWrites(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		contents string
		mode     os.FileMode
	}{
		{name: "a file that parses", contents: workedExample, mode: 0o600},
		// The refusal paths matter more than the happy one here: a daemon that
		// refuses is a daemon an operator is about to edit, and a file rewritten
		// under them mid-edit is the worst version of this bug.
		{name: "a file the grammar refuses", contents: "listen = x\nnonsense\n", mode: 0o600},
		{name: "a file refused for its mode", contents: "shared_secret = " + hunter2 + "\n", mode: 0o644},
		{name: "a file refused for its version", contents: "version = 99\n", mode: 0o600},
		// Nothing normalises the spelling of a file that is merely untidy — no
		// trimmed whitespace, no rewritten line ending, no added final newline.
		{name: "a file that is untidy but valid", contents: "  listen  =  127.0.0.1:8787  \r\n\n\n# a note\nmax_sessions = 3", mode: 0o600},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			path := writeConfig(t, tc.contents, tc.mode)

			// Backdated so the mtime comparison can fail at all. The kernel
			// stamps an mtime from a coarse clock that does not tick within a
			// test this short, so a fixture written moments ago and rewritten
			// during the read keeps the same mtime to the nanosecond — the
			// assertion would be green against a daemon that had just
			// overwritten the operator's file.
			if err := os.Chtimes(path, longAgo, longAgo); err != nil {
				t.Fatalf("backdate the fixture config file: %v", err)
			}

			before, beforeInfo := snapshot(t, path)

			// The error is deliberately not asserted on. This test is about the
			// bytes on disk, and it must hold whichever way the parse went.
			_, _ = config.ReadFile(path, io.Discard) //nolint:errcheck // deliberate, per the comment above: this test asserts on the bytes on disk and must hold whichever way the parse went.

			after, afterInfo := snapshot(t, path)
			if !bytes.Equal(before, after) {
				t.Errorf("the file's contents changed:\nbefore %q\nafter  %q", before, after)
			}
			if !afterInfo.ModTime().Equal(beforeInfo.ModTime()) {
				t.Errorf("the file's mtime moved from %s to %s, so something opened it for writing",
					beforeInfo.ModTime(), afterInfo.ModTime())
			}
			if afterInfo.Mode() != beforeInfo.Mode() {
				t.Errorf("the file's mode changed from %04o to %04o; the mode refusal says what to run, it does not run it",
					beforeInfo.Mode().Perm(), afterInfo.Mode().Perm())
			}

			// The other half of "never writes", and the one comparing bytes
			// misses: a backup, a `.tmp` alongside, a migrated copy. Each
			// fixture has its own directory, so anything here is new.
			entries, err := os.ReadDir(filepath.Dir(path))
			if err != nil {
				t.Fatalf("list the fixture directory: %v", err)
			}
			if len(entries) != 1 || entries[0].Name() != filepath.Base(path) {
				names := make([]string, 0, len(entries))
				for _, e := range entries {
					names = append(names, e.Name())
				}
				t.Errorf("reading the config file left %v beside it; config migrate is the only code that writes one", names)
			}
		})
	}
}

// The path override (T009). Until now the daemon read one file, in one place,
// and a deployment that keeps its configuration anywhere else — a container
// mount, a second daemon on one host, a fixture in a test — had no way to say
// so. CRSW_CONFIG_FILE names the file outright.
//
// It is a variable and not a key, and that is the assertion the second subtest
// is about: resolved through the precedence shim it would be a key that
// configures which file it is read from, and the first question anybody asked
// about it would be which of the two files won.
func TestConfigFileEnvOverridesDefaultPath(t *testing.T) {
	t.Parallel()

	t.Run("a file at neither default location is read when it is named", func(t *testing.T) {
		t.Parallel()

		// Both default locations hold a *loadable* file naming a different
		// listener, so a daemon that read either of them answers with a value
		// this test can name rather than failing for some unrelated reason.
		root := t.TempDir()
		home := homeWith(t, fileLines(root, "listen = 127.0.0.1:1111"))
		xdg := t.TempDir()
		plantConfig(t, xdg, fileLines(root, "listen = 127.0.0.1:2222"))

		named := filepath.Join(t.TempDir(), "somewhere-else")
		if err := os.WriteFile(named, []byte(fileLines(root, "listen = 127.0.0.1:3333")), 0o600); err != nil {
			t.Fatalf("write the named configuration file: %v", err)
		}

		cfg, err := config.LoadFrom(env(map[string]string{
			"HOME":            home,
			xdgConfigHomeVar:  xdg,
			configFileEnvName: named,
		}), io.Discard)
		if err != nil {
			t.Fatalf("LoadFrom() with %s naming a file: %v", configFileEnvName, err)
		}
		if cfg.Listen != "127.0.0.1:3333" {
			t.Errorf("Listen = %q, want the file %s names; 1111 is HOME and 2222 is XDG_CONFIG_HOME, and either means the override was not honoured",
				cfg.Listen, configFileEnvName)
		}
	})

	t.Run("a file cannot name the file read after it", func(t *testing.T) {
		t.Parallel()

		// The override is resolved before anything is parsed, so it is not a
		// setting, so `config_file` is not a key. This is what fails the day
		// somebody adds it to Vars() to be helpful: the daemon would read one
		// file to find out which file to read, and a file naming itself would
		// have to be refused by a rule that does not exist.
		root := t.TempDir()
		other := filepath.Join(t.TempDir(), "other")
		home := homeWith(t, fileLines(root, "config_file = "+other))

		_, err := config.LoadFrom(env(map[string]string{"HOME": home}), io.Discard)
		if err == nil {
			t.Fatal("LoadFrom() accepted a config file naming the file to read next")
		}
		if !strings.Contains(err.Error(), "config_file") || !strings.Contains(err.Error(), "unknown key") {
			t.Errorf("LoadFrom() = %v, want config_file refused as an unknown key", err)
		}
	})

	t.Run("the named file is read as written, relative included", func(t *testing.T) {
		t.Parallel()

		// Taken exactly as the operator wrote it. Ignoring a relative path and
		// quietly reading the XDG file instead is the silently-wrong-file
		// failure the rest of this package refuses everywhere, and `crswd config
		// check ./config` has to mean the same file the daemon would read.
		for _, named := range []string{"/etc/crswd/config", "./config", "config"} {
			if got := config.DefaultPath(env(map[string]string{
				configFileEnvName: named,
				xdgConfigHomeVar:  "/xdg",
				"HOME":            "/home/operator",
			})); got != named {
				t.Errorf("DefaultPath() = %q, want %q exactly", got, named)
			}
		}
	})
}

// configFileEnvName is restated rather than exported from the package, for the
// reason xdgConfigHomeVar is: a test that asks the code under test which
// variable it reads agrees with it by construction, including on the day it
// agrees about the wrong one.
const configFileEnvName = "CRSW_CONFIG_FILE"

// FR-010, the recovery that needs no shell access. The operator edits the file
// through whatever they have to hand, gets it wrong, and the daemon they would
// use to fix it is the daemon that will not start. The last known-good copy
// beside it is the way back, and it is only a way back if it is announced —
// otherwise the daemon is running on a configuration nobody wrote, and the next
// surprise is unexplainable.
func TestBackupIsConsultedWhenTheFileWillNotLoad(t *testing.T) {
	t.Parallel()

	// broken is a *file* defect: a line that is not a comment, not blank, and
	// not a pair. The backup beside it is a whole working configuration.
	setup := func(t *testing.T, live string) (string, string) {
		t.Helper()

		root := t.TempDir()
		dir := t.TempDir()
		path := filepath.Join(dir, "config")
		if err := os.WriteFile(path, []byte(live), 0o600); err != nil {
			t.Fatalf("write the live configuration file: %v", err)
		}
		if err := os.WriteFile(config.BackupPath(path), []byte(fileLines(root, "listen = 127.0.0.1:8888")), 0o600); err != nil {
			t.Fatalf("write the backup configuration file: %v", err)
		}
		return path, config.BackupPath(path)
	}

	t.Run("a file that will not parse", func(t *testing.T) {
		t.Parallel()

		path, backup := setup(t, "listen 127.0.0.1:9999\n")

		var warn bytes.Buffer
		cfg, err := config.LoadFrom(env(map[string]string{configFileEnvName: path}), &warn)
		if err != nil {
			t.Fatalf("LoadFrom() refused rather than falling back to %s: %v", backup, err)
		}
		if cfg.Listen != "127.0.0.1:8888" {
			t.Errorf("Listen = %q, want the backup's value", cfg.Listen)
		}

		// Loud, and specific about both files: the daemon is up on the older one
		// and the newer one is still there with the defect still in it.
		said := warn.String()
		for _, want := range []string{path, backup, "NOT in effect"} {
			if !strings.Contains(said, want) {
				t.Errorf("the startup warning does not mention %q:\n%s", want, said)
			}
		}
	})

	t.Run("a file that parses and will not load", func(t *testing.T) {
		t.Parallel()

		// The other half of "will not load": the grammar is fine and a *value*
		// is refused. It is the same recovery — the operator still cannot start
		// the daemon they would use to fix it.
		//
		// A host name is the value, because fileLines writes an Access door and a
		// daemon with one may bind off loopback now (M12/T002). A name is refused
		// under every door.
		root := t.TempDir()
		path, _ := setup(t, fileLines(root, "listen = localhost:8080"))

		cfg, err := config.LoadFrom(env(map[string]string{configFileEnvName: path}), io.Discard)
		if err != nil {
			t.Fatalf("LoadFrom() refused rather than falling back: %v", err)
		}
		if cfg.Listen != "127.0.0.1:8888" {
			t.Errorf("Listen = %q, want the backup's value", cfg.Listen)
		}
	})

	t.Run("no backup is the operator's own refusal", func(t *testing.T) {
		t.Parallel()

		path, backup := setup(t, "listen 127.0.0.1:9999\n")
		if err := os.Remove(backup); err != nil {
			t.Fatalf("remove the backup: %v", err)
		}

		_, err := config.LoadFrom(env(map[string]string{configFileEnvName: path}), io.Discard)
		if err == nil {
			t.Fatal("LoadFrom() started with no backup to start from")
		}
		if !strings.Contains(err.Error(), path) || !strings.Contains(err.Error(), ":1") {
			t.Errorf("LoadFrom() = %v, want the live file and its line named — that is the defect the operator can act on", err)
		}
	})

	t.Run("a backup that will not load either is not a start", func(t *testing.T) {
		t.Parallel()

		// A backup is accepted only if it loads *completely*, through the same
		// loadWith the live file goes through — so the two cases here are the
		// two halves of "will not load", and neither is a start. A backup
		// exempted from a bound would make this the one path on which a value
		// skipped its check.
		root := t.TempDir()
		for name, contents := range map[string]string{
			"the backup does not parse":    "also not a pair\n",
			"the backup breaks a bound":    fileLines(root, "listen = localhost:8080"),
			"the backup is short a secret": "allowed_roots = " + root + "\n",
		} {
			path, backup := setup(t, "listen 127.0.0.1:9999\n")
			if err := os.WriteFile(backup, []byte(contents), 0o600); err != nil {
				t.Fatalf("%s: write the backup: %v", name, err)
			}

			var warn bytes.Buffer
			_, err := config.LoadFrom(env(map[string]string{configFileEnvName: path}), &warn)
			if err == nil {
				t.Errorf("%s: LoadFrom() started on a backup that does not load", name)
				continue
			}
			// The live file's refusal, not the backup's: the operator wrote one
			// of those two files, and it is not the one this daemon copied.
			if !strings.Contains(err.Error(), path+":1") {
				t.Errorf("%s: LoadFrom() = %v, want the live file's refusal", name, err)
			}
			// And nothing announced a fallback that did not happen. A daemon
			// that says it started from the backup and then refuses to start
			// sends its operator to the wrong file.
			if strings.Contains(warn.String(), backup) {
				t.Errorf("%s: the daemon announced a fallback it did not make:\n%s", name, warn.String())
			}
		}
	})

	t.Run("a backup is not consulted when no file was read", func(t *testing.T) {
		t.Parallel()

		// The deployment with no configuration file at all, and a stale
		// config.bak left beside where one used to be. Deleting a configuration
		// is a thing an operator does on purpose, and a daemon that answered by
		// reading the copy it kept would be running on the bounds they deleted.
		//
		// The environment here is short of exactly one thing the backup
		// supplies, so a fallback that fired would start rather than refuse —
		// which is the only version of this case that can fail.
		path, _ := setup(t, "")
		if err := os.Remove(path); err != nil {
			t.Fatalf("remove the live file: %v", err)
		}

		pairs, _ := baseEnv(t)
		pairs[configFileEnvName] = path
		delete(pairs, config.EnvSharedSecret)

		_, err := config.LoadFrom(env(pairs), io.Discard)
		if err == nil {
			t.Fatal("LoadFrom() started on the backup of a file that is not there, so a deleted configuration is still in force")
		}
		if !strings.Contains(err.Error(), config.EnvSharedSecret) {
			t.Errorf("LoadFrom() = %v, want the environment's own refusal", err)
		}
	})
}

// configExamplePath is the operator's copy of everything this daemon can be
// told, and the only place that says *why* each bound is what it is — the
// commentary JSON would have deleted, which is the reason this format is not
// JSON (research D1).
const configExamplePath = "../../config.example"

// exampleLine is one `# key = value` line of config.example: the form an
// operator uncomments, and the only form this test recognises. A key named in
// prose is not a setting anyone can turn on by uncommenting it.
type exampleLine struct {
	key   string
	value string
	line  int
}

// exampleLines reads those lines out of config.example, filtering to keys the
// daemon actually has so that a sentence of prose is not mistaken for a
// setting. A key mentioned mid-sentence is left alone — everything before the
// first `=` has to be the key and nothing else, which is exactly the test an
// operator's eye applies when deciding what to uncomment.
func exampleLines(t *testing.T, raw []byte, known map[string]bool) []exampleLine {
	t.Helper()

	var out []exampleLine
	for i, text := range strings.Split(string(raw), "\n") {
		trimmed := strings.TrimSpace(text)
		if !strings.HasPrefix(trimmed, "#") {
			continue
		}
		key, value, found := strings.Cut(strings.TrimPrefix(trimmed, "#"), "=")
		if !found {
			continue
		}
		key = strings.TrimSpace(key)
		if !known[key] {
			continue
		}
		out = append(out, exampleLine{key: key, value: strings.TrimSpace(value), line: i + 1})
	}
	return out
}

// TestConfigExampleParsesAndCoversEveryKey pins config.example to the daemon
// that reads it (T034). Three things rot silently about an example file, and
// none of them shows up in a run of the package it documents:
//
//   - a setting added to config.go that the example never names, so no operator
//     learns it exists;
//   - a line that no longer parses the way it reads, so uncommenting it sets
//     something other than what it looks like;
//   - a live assignment, which is both a daemon that behaves differently the
//     moment the file is copied and the way a secret reaches this repository.
//
// The keys come from config.go's own declarations rather than from a list kept
// here, so a variable added there and forgotten in the example is a red build —
// which is the whole of this task's obligation.
func TestConfigExampleParsesAndCoversEveryKey(t *testing.T) {
	t.Parallel()

	raw, err := os.ReadFile(configExamplePath)
	if err != nil {
		t.Fatalf("read %s: %v", configExamplePath, err)
	}

	// The daemon's own parser, not a second reading of the same bytes. An
	// example an operator copies has to be a file this daemon starts on.
	f, err := config.ParseFile(configExamplePath, raw, io.Discard)
	if err != nil {
		t.Fatalf("%s does not parse: %v", configExamplePath, err)
	}

	// And copying it changes nothing (FR-003, SC-002): every setting in it is
	// commented out, so the only live line is the schema version, which is not
	// a setting. The value is deliberately not quoted back — the case this
	// catches is a real one having been left on a line.
	for _, name := range config.Vars() {
		if _, set := f.Lookup(name); set {
			t.Errorf("%s sets %s; every setting in it is commented out, so that copying it leaves the daemon exactly as it is without a file — and so that no value is published here",
				configExamplePath, config.KeyForVar(name))
		}
	}

	known := make(map[string]bool)
	for name := range declaredVars(t) {
		known[config.KeyForVar(name)] = true
	}

	shown := exampleLines(t, raw, known)
	seen := make(map[string]int, len(shown))
	for _, l := range shown {
		if first, dup := seen[l.key]; dup {
			t.Errorf("%s:%d names %s a second time, first on line %d; one line per setting, or the page it is compared against no longer lines up with it",
				configExamplePath, l.line, l.key, first)
			continue
		}
		seen[l.key] = l.line
	}
	for key := range known {
		if _, ok := seen[key]; !ok {
			t.Errorf("%s reads %s and %s never shows it, so no operator learns the setting exists",
				configSourcePath, config.VarForKey(key), configExamplePath)
		}
	}

	// The order is the settings page's order, which is config.go's. The page
	// exists to be read beside this file, and a table and an example that
	// disagree about where a setting is make that comparison manual.
	if len(shown) == len(config.Vars()) {
		for i, name := range config.Vars() {
			if key := config.KeyForVar(name); shown[i].key != key {
				t.Errorf("%s:%d shows %s where config.go declares %s; the example is read beside /settings, which renders in declaration order",
					configExamplePath, shown[i].line, shown[i].key, key)
			}
		}
	}

	// Each line means what it looks like once uncommented. start_commands is
	// the one that makes this worth asserting: its value contains an `=`, and
	// the claim that the parser splits on the first one is made *in* this file.
	for _, l := range shown {
		one, err := config.ParseFile(configExamplePath, []byte(l.key+" = "+l.value+"\n"), io.Discard)
		if err != nil {
			t.Errorf("%s:%d does not parse once uncommented: %v", configExamplePath, l.line, err)
			continue
		}
		got, ok := one.Lookup(config.VarForKey(l.key))
		if !ok {
			t.Errorf("%s:%d sets nothing once uncommented", configExamplePath, l.line)
			continue
		}
		if got != l.value {
			// Neither half is quoted for the two IsSecret keys. Their example
			// line is the one an operator replaces with a real value, and a
			// test that printed whatever was on it would be the one thing in
			// this repository that publishes it.
			if config.IsSecret(l.key) {
				t.Errorf("%s:%d does not set the value it shows for %s", configExamplePath, l.line, l.key)
				continue
			}
			t.Errorf("%s:%d reads as %s = %q and uncommenting it sets %q instead",
				configExamplePath, l.line, l.key, l.value, got)
		}
	}
}

// TestConfigExampleShipsARemoteControlCommandThatRendersInItsOwnPane pins the
// one line an operator uncomments to start remote-controlled sessions.
//
// The example used to be a `remote-control --spawn=…` launcher, and a launcher
// makes the tmux session this daemon started a starter for a session that lives
// on the relay: the pane goes quiet after startup. Everything the dashboard does
// reads that pane — the viewer, the status pill's inferred states, compact — so
// the shipped example is not a matter of taste. An operator who copies a
// spawning one gets a dashboard that can show, judge, and compact nothing, and
// nothing in the daemon notices.
//
// It is loaded through the daemon's own loader rather than pattern-matched,
// because the claim being made about this line is that a daemon starts on it.
func TestConfigExampleShipsARemoteControlCommandThatRendersInItsOwnPane(t *testing.T) {
	t.Parallel()

	raw, err := os.ReadFile(configExamplePath)
	if err != nil {
		t.Fatalf("read %s: %v", configExamplePath, err)
	}

	key := config.KeyForVar(config.EnvStartCommands)
	shown := exampleLines(t, raw, map[string]bool{key: true})
	if len(shown) != 1 {
		t.Fatalf("%s shows %d %s lines, want exactly 1", configExamplePath, len(shown), key)
	}

	pairs, _ := baseEnv(t)
	pairs[config.EnvStartCommands] = shown[0].value
	cfg := mustLoad(t, pairs)

	name := cfg.RemoteControlCommand
	if name == "" {
		t.Fatalf("%s:%d configures no remote-control command, so an operator who copies it is offered no switch",
			configExamplePath, shown[0].line)
	}
	command, ok := cfg.StartCommands.Command(name)
	if !ok {
		t.Fatalf("%s:%d names %q as the remote-control command and defines no such entry", configExamplePath, shown[0].line, name)
	}
	if strings.Contains(command, "--spawn") {
		t.Errorf("%s:%d ships %q as the remote-control command, and --spawn puts the conversation on the relay: the tmux session becomes a launcher whose pane goes quiet after startup.\nThe pane viewer, the status pill and compact all read that pane, so what an operator copies here decides whether the dashboard can see the sessions it started",
			configExamplePath, shown[0].line, name)
	}
}

// TestConfigExampleSpellsNeverWhereTheDaemonTakesIt pins the asymmetry the
// example now teaches: `never` removes the lifetime ceiling, and the same word
// on the default beside it is refused.
//
// It is a claim about behaviour made in prose, which is the kind that rots
// without anything going red — the file is not compiled and nothing else reads
// it. The asymmetry is also the security-bearing half of milestone 13: the
// ceiling is where an operator says a session on this host may outlive the one
// deadline that is never renewed, and a default that quietly learned the word
// would make every session on that host immortal without anyone asking for it.
//
// Both halves go through the daemon's own loader rather than being read a second
// way here, for the reason
// TestConfigExampleShipsARemoteControlCommandThatRendersInItsOwnPane does: what
// the file claims is that a daemon starts on this and behaves so.
//
// **Must fail when** the default accepts the word, when the ceiling stops
// accepting it, or when the example's own two lines stop being ones a daemon
// starts on.
func TestConfigExampleSpellsNeverWhereTheDaemonTakesIt(t *testing.T) {
	t.Parallel()

	raw, err := os.ReadFile(configExamplePath)
	if err != nil {
		t.Fatalf("read %s: %v", configExamplePath, err)
	}

	lifetimeKey := config.KeyForVar(config.EnvSessionLifetime)
	ceilingKey := config.KeyForVar(config.EnvSessionLifetimeMax)
	shown := make(map[string]exampleLine)
	for _, l := range exampleLines(t, raw, map[string]bool{lifetimeKey: true, ceilingKey: true}) {
		shown[l.key] = l
	}
	for _, key := range []string{lifetimeKey, ceilingKey} {
		if _, ok := shown[key]; !ok {
			t.Fatalf("%s shows no %s line", configExamplePath, key)
		}
	}

	// The values on the page are ones an operator uncomments, so they have to be
	// values this daemon starts on — the example may not illustrate a bound with
	// something the loader refuses.
	t.Run("the two lines as shown start a daemon", func(t *testing.T) {
		t.Parallel()

		pairs, _ := baseEnv(t)
		pairs[config.EnvSessionLifetime] = shown[lifetimeKey].value
		pairs[config.EnvSessionLifetimeMax] = shown[ceilingKey].value
		if got := mustLoad(t, pairs).SessionLifetime; got <= 0 {
			t.Errorf("%s:%d shows %q and it loads as %v; the default every session gets must be a positive duration",
				configExamplePath, shown[lifetimeKey].line, shown[lifetimeKey].value, got)
		}
	})

	t.Run("the ceiling takes the word", func(t *testing.T) {
		t.Parallel()

		pairs, _ := baseEnv(t)
		pairs[config.EnvSessionLifetimeMax] = config.NeverLifetime
		cfg := mustLoad(t, pairs)
		if cfg.SessionLifetimeMax >= 0 {
			t.Errorf("%s:%d documents %q as the spelling for no ceiling at all and the loader answers %v; an operator who writes what this file tells them gets a bound they meant to remove",
				configExamplePath, shown[ceilingKey].line, config.NeverLifetime, cfg.SessionLifetimeMax)
		}
	})

	t.Run("the default refuses it", func(t *testing.T) {
		t.Parallel()

		pairs, _ := baseEnv(t)
		pairs[config.EnvSessionLifetime] = config.NeverLifetime
		cfg, err := config.LoadFrom(env(pairs), io.Discard)
		if err == nil {
			t.Fatalf("%s:%d says the word is refused here and the daemon started on it with a %v lifetime; every session on this host would then be immortal without a create ever asking",
				configExamplePath, shown[lifetimeKey].line, cfg.SessionLifetime)
		}
		// The trailing space is load-bearing: the ceiling's variable has this
		// one as a prefix, and a refusal that named only the ceiling would send
		// an operator to the line they wrote correctly.
		if !strings.Contains(err.Error(), config.EnvSessionLifetime+" ") {
			t.Errorf("the refusal does not name %s, so an operator cannot tell which of the two lines it came from: %v", config.EnvSessionLifetime, err)
		}
	})
}

// longAgo is far enough back that any write at all moves the mtime by years
// rather than by whatever the clock's granularity happens to be.
var longAgo = time.Date(2020, time.January, 2, 3, 4, 5, 0, time.UTC)

// snapshot is what the file is, for a comparison of what it still is.
func snapshot(t *testing.T, path string) ([]byte, os.FileInfo) {
	t.Helper()

	data, err := os.ReadFile(path) //nolint:gosec // G304: the path is this test's own fixture.
	if err != nil {
		t.Fatalf("read the fixture config file: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat the fixture config file: %v", err)
	}
	return data, info
}

// TestJournalPath fixes where the session journal lives, which matters because
// a daemon that replayed the wrong one would recreate somebody else's sessions.
func TestJournalPath(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		env  map[string]string
		want string
	}{
		{
			name: "beside a named configuration file",
			env:  map[string]string{"CRSW_CONFIG_FILE": "/etc/crswd/other.conf"},
			want: "/etc/crswd/sessions.jsonl",
		},
		{
			name: "under XDG_CONFIG_HOME when no file is named",
			env:  map[string]string{"XDG_CONFIG_HOME": "/xdg"},
			want: "/xdg/crswd/sessions.jsonl",
		},
		{
			name: "under HOME when neither is named",
			env:  map[string]string{"HOME": "/home/op"},
			want: "/home/op/.config/crswd/sessions.jsonl",
		},
		{
			name: "a named file wins over both",
			env:  map[string]string{"CRSW_CONFIG_FILE": "/tmp/t/config", "XDG_CONFIG_HOME": "/xdg", "HOME": "/home/op"},
			want: "/tmp/t/sessions.jsonl",
		},
		{
			name: "XDG wins over HOME",
			env:  map[string]string{"XDG_CONFIG_HOME": "/xdg", "HOME": "/home/op"},
			want: "/xdg/crswd/sessions.jsonl",
		},
		{
			name: "a relative XDG directory is ignored, not joined to the cwd",
			env:  map[string]string{"XDG_CONFIG_HOME": "relative", "HOME": "/home/op"},
			want: "/home/op/.config/crswd/sessions.jsonl",
		},
		{
			name: "nowhere to put one is not an error",
			env:  map[string]string{},
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := config.JournalPath(func(k string) string { return tt.env[k] })
			if got != tt.want {
				t.Errorf("JournalPath() = %q, want %q", got, tt.want)
			}
		})
	}
}
