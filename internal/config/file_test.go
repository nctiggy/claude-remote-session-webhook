package config_test

// The configuration file's grammar and its refusals (#65, T003 and T004). Every
// case here drives config.ParseFile with bytes, because both are about the
// contents of a file rather than about the file on disk: what mode it must be
// and what happens when it is absent are decisions T005 and T006 make, and they
// are tested where they are made.
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
	"io"
	"strings"
	"testing"

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

# Sessions live a day unless told otherwise; -1 disables idle reaping.
session_lifetime = 24h
idle_timeout = -1

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

	want := map[string]string{
		"listen":           "127.0.0.1:8787",
		"allowed_roots":    "/home/nctiggy/code,/home/nctiggy/work",
		"start_commands":   "default=claude --dangerously-skip-permissions,rc=claude remote-control --permission-mode bypassPermissions",
		"session_lifetime": "24h",
		"idle_timeout":     "-1",
		"shared_secret":    "hunter2#not-a-comment",
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
	}{
		{name: "a version from a newer daemon", contents: "version = 99\n", want: []string{"99", "1", "config:1"}},
		{name: "one past this one", contents: "version = 2\n", want: []string{"2", "config:1"}},
		{name: "not a number", contents: "version = one\n", want: []string{"whole number", "config:1"}},
		{name: "a decimal", contents: "version = 1.0\n", want: []string{"whole number"}},
		{name: "before the first schema", contents: "version = 0\n", want: []string{"the first schema is 1"}},
		{name: "negative", contents: "version = -3\n", want: []string{"the first schema is 1"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, err := config.ParseFile("config", []byte(tc.contents), io.Discard)
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
