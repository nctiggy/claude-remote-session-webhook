package config_test

// The configuration file's grammar (#65, T003). Every case here drives
// config.ParseFile with bytes, because the grammar is the one part of this
// feature that is about a line of text rather than about a configuration: which
// keys exist, what the file's mode must be, and what happens when it is absent
// are decisions T004 to T006 make, and they are tested where they are made.
//
// Two of these cases are the reason the format is what it is. A `#` inside a
// value belongs to the value, and a value may contain `=`. Both look like
// pedantry until the value is a shared secret or a command line.

import (
	"strings"
	"testing"

	"github.com/nctiggy/claude-remote-session-webhook/internal/config"
)

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

	f, err := config.ParseFile("config", []byte(workedExample))
	if err != nil {
		t.Fatalf("ParseFile() rejected the worked example: %v", err)
	}

	want := map[string]string{
		"version":          "1",
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

			f, err := config.ParseFile("config", []byte(tc.contents))
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

			f, err := config.ParseFile("config", []byte(tc.contents))
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

func TestWhitespaceAroundSeparatorIgnored(t *testing.T) {
	t.Parallel()

	for _, contents := range []string{
		"a=b\n",
		"a = b\n",
		"  a  =  b  \n",
		"\ta\t=\tb\t\n",
		"a =b\n",
		"a= b\n",
		// The last line of a file need not end in one.
		"a = b",
		// CRLF is a line ending, not part of the value.
		"a = b\r\n",
	} {
		t.Run(strings.ReplaceAll(contents, "\n", `\n`), func(t *testing.T) {
			t.Parallel()

			f, err := config.ParseFile("config", []byte(contents))
			if err != nil {
				t.Fatalf("ParseFile(%q): %v", contents, err)
			}
			got, ok := f.Lookup("CRSW_A")
			if !ok {
				t.Fatalf("ParseFile(%q) set no key `a`", contents)
			}
			if got != "b" {
				t.Errorf("a = %q, want %q", got, "b")
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

			f, err := config.ParseFile("config", []byte("  start_commands   =   "+tc.want+"   \n"))
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
		if _, err := config.ParseFile("config", []byte(key+" = x\n")); err != nil {
			t.Errorf("the parser refuses %q, which is how %s is spelled in a file: %v", key, name, err)
		}
	}
}

// A line the grammar does not describe is refused, and the refusal says where
// and never what. The line may be the shared secret with a typo in it, and a
// startup error is written to stderr and kept in the journal forever.
func TestALineThatIsNotAPairIsRefusedWithoutQuotingIt(t *testing.T) {
	t.Parallel()

	// Each fixture is a secret-shaped string in the position the parser refuses:
	// alone on a line, on the value side of a line with no key, and in the key
	// position, where a hex secret pasted without its key would otherwise land.
	const hexSecret = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

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

			_, err := config.ParseFile("config", []byte(tc.contents))
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

	if _, err := config.ParseFile("config", []byte("Shared_Secret = x\n")); err == nil {
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
