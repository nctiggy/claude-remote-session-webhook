package preflight

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nctiggy/claude-remote-session-webhook/internal/config"
)

// modulePath is stripped before the hardcoded-name scan below, because every
// import in this package carries it and it is not the literal being hunted.
const modulePath = "github.com/nctiggy/claude-remote-session-webhook"

// hostWith builds a Host whose PATH holds exactly the named binaries and whose
// /etc/os-release is the given text. An empty osRelease is a host that does not
// say what it is.
func hostWith(goos, osRelease string, present ...string) Host {
	installed := make(map[string]bool, len(present))
	for _, name := range present {
		installed[name] = true
	}
	return Host{
		GOOS: goos,
		LookPath: func(name string) (string, error) {
			if installed[name] {
				return "/usr/bin/" + name, nil
			}
			return "", fmt.Errorf("exec: %q: executable file not found in $PATH", name)
		},
		OSRelease: func() ([]byte, error) {
			if osRelease == "" {
				return nil, errors.New("open /etc/os-release: no such file or directory")
			}
			return []byte(osRelease), nil
		},
	}
}

// TestMissingTmuxRefusesToStart is the whole point: nothing works without tmux,
// so its absence is an error and not a warning.
func TestMissingTmuxRefusesToStart(t *testing.T) {
	t.Parallel()

	var warn bytes.Buffer
	h := hostWith("linux", "ID=ubuntu\n", "claudeless-stand-in")
	err := CheckOn(h, config.NewStartCommands(map[string]string{
		config.DefaultStartCommandName: "claudeless-stand-in --go",
	}), &warn)
	if err == nil {
		t.Fatal("a host without tmux started")
	}
	for _, want := range []string{"tmux", "sudo apt install tmux", "refusing to start"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not mention %q: %v", want, err)
		}
	}
}

// TestInstallAdviceNamesThePlatformsCommand walks the whole table, including the
// two cases that carry the design: ID_LIKE answering for a distribution nothing
// lists, and a host that says nothing at all still getting usable advice.
func TestInstallAdviceNamesThePlatformsCommand(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name      string
		goos      string
		osRelease string
		want      string
	}{
		{"debian", "linux", "ID=debian\n", `install it with "sudo apt install tmux"`},
		{"ubuntu", "linux", "ID=ubuntu\nID_LIKE=debian\n", `install it with "sudo apt install tmux"`},
		{"fedora", "linux", "ID=fedora\n", `install it with "sudo dnf install tmux"`},
		{"centos", "linux", `ID="centos"` + "\n", `install it with "sudo dnf install tmux"`},
		{"arch", "linux", "ID=arch\n", `install it with "sudo pacman -S tmux"`},
		{"alpine", "linux", "ID=alpine\n", `install it with "sudo apk add tmux"`},
		{"darwin", "darwin", "", `install it with "brew install tmux"`},

		// The two ID_LIKE cases. Neither ID is in the table, and neither needs
		// to be: Mint says it is debian-like and Rocky says it is rhel-like.
		{"mint via ID_LIKE", "linux", "ID=linuxmint\nID_LIKE=ubuntu debian\n", `install it with "sudo apt install tmux"`},
		{"rocky via ID_LIKE", "linux", `ID=rocky` + "\n" + `ID_LIKE="rhel centos fedora"` + "\n", `install it with "sudo dnf install tmux"`},

		// ID_LIKE is a priority list: the first token that is recognised wins,
		// so an unrecognised one ahead of it is skipped rather than ending the
		// search.
		{"unknown token first", "linux", "ID=steamos\nID_LIKE=holo arch\n", `install it with "sudo pacman -S tmux"`},

		{"no os-release", "linux", "", "install it with your package manager"},
		{"unrecognised", "linux", "ID=plan9\n", "install it with your package manager"},
		{"os-release without an ID", "linux", "PRETTY_NAME=\"Something\"\n", "install it with your package manager"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			h := hostWith(tc.goos, tc.osRelease)
			if got := installAdvice(h, tmuxBinary); got != tc.want {
				t.Errorf("installAdvice = %q, want %q", got, tc.want)
			}

			// And the refusal an operator actually reads carries it.
			err := CheckOn(h, config.NewStartCommands(nil), nil)
			if err == nil {
				t.Fatal("a host without tmux started")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("the refusal does not carry the advice %q: %v", tc.want, err)
			}
		})
	}
}

// TestMissingStartCommandWarnsAndStarts is the deliberate asymmetry: the
// dashboard and the reaper still work without the start command, so the daemon
// says so loudly rather than refusing.
func TestMissingStartCommandWarnsAndStarts(t *testing.T) {
	t.Parallel()

	var warn bytes.Buffer
	h := hostWith("linux", "ID=debian\n", tmuxBinary)
	err := CheckOn(h, config.NewStartCommands(map[string]string{
		config.DefaultStartCommandName: "not-installed-anywhere --flag",
	}), &warn)
	if err != nil {
		t.Fatalf("a missing start command refused the start: %v", err)
	}
	for _, want := range []string{config.DefaultStartCommandName, "not-installed-anywhere", config.EnvStartCommand} {
		if !strings.Contains(warn.String(), want) {
			t.Errorf("the warning does not mention %q:\n%s", want, warn.String())
		}
	}
	// The flags are the operator's, not a binary — a warning naming them would
	// send them looking for the wrong thing.
	if strings.Contains(warn.String(), "--flag") {
		t.Errorf("the warning names the whole command line rather than its binary:\n%s", warn.String())
	}
}

// TestStartCommandCheckedIsTheConfiguredOne is the failure #38 makes possible: a
// daemon configured to run something else must be checked for that, and a daemon
// running the default must not be excused because some other agent is installed.
func TestStartCommandCheckedIsTheConfiguredOne(t *testing.T) {
	t.Parallel()

	// The default's own binary, taken from the configuration rather than
	// spelled here, so this test says nothing about which binary that is.
	defaultBinary := commandBinary(config.DefaultStartCommand)

	cases := []struct {
		name      string
		commands  map[string]string
		installed []string
		warns     string
	}{
		{
			name:      "the configured command is absent and the default's binary is present",
			commands:  map[string]string{config.DefaultStartCommandName: "some-other-agent --go"},
			installed: []string{tmuxBinary, defaultBinary},
			warns:     "some-other-agent",
		},
		{
			name:      "the configured command is present",
			commands:  map[string]string{config.DefaultStartCommandName: "some-other-agent --go"},
			installed: []string{tmuxBinary, "some-other-agent"},
			warns:     "",
		},
		{
			name:      "the default is in force and its binary is absent",
			commands:  map[string]string{config.DefaultStartCommandName: config.DefaultStartCommand},
			installed: []string{tmuxBinary, "some-other-agent"},
			warns:     defaultBinary,
		},
		{
			name: "a named command beside the default",
			commands: map[string]string{
				config.DefaultStartCommandName: "some-other-agent",
				"rc":                           "another-agent remote-control --name {name}",
			},
			installed: []string{tmuxBinary, "some-other-agent"},
			warns:     "another-agent",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var warn bytes.Buffer
			h := hostWith("linux", "ID=debian\n", tc.installed...)
			if err := CheckOn(h, config.NewStartCommands(tc.commands), &warn); err != nil {
				t.Fatalf("the check refused a host that has tmux: %v", err)
			}
			switch {
			case tc.warns == "" && warn.Len() != 0:
				t.Errorf("an installed start command warned:\n%s", warn.String())
			case tc.warns != "" && !strings.Contains(warn.String(), tc.warns):
				t.Errorf("the warning does not name %q:\n%s", tc.warns, warn.String())
			}
		})
	}
}

// TestNoHardcodedDefaultBinary fails if somebody reintroduces the literal name
// of the default start command's binary into this package.
//
// The behavioural tests above already show the configured command is what gets
// checked, but they pass just as well against a package that happens to check
// the right thing *and* carries the old name somewhere ready to be used. Since
// #38 the daemon may be configured to run anything, so a name written down here
// is a check that is wrong on those hosts and right by accident on the rest.
func TestNoHardcodedDefaultBinary(t *testing.T) {
	t.Parallel()

	forbidden := commandBinary(config.DefaultStartCommand)
	if forbidden == "" {
		t.Fatal("the default start command has no binary; this guard would check nothing")
	}

	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read the package directory: %v", err)
	}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		data, err := os.ReadFile(filepath.Clean(name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		// The module path carries the name in every import line in this package
		// and is not what is being looked for.
		source := strings.ReplaceAll(string(data), modulePath, "")
		if strings.Contains(strings.ToLower(source), strings.ToLower(forbidden)) {
			t.Errorf("%s names %q: check the configured start command, not a fixed one (#38)", name, forbidden)
		}
	}
}

// TestCommandBinary pins what a command line's binary is, including the shapes
// this package deliberately does not try to be clever about.
func TestCommandBinary(t *testing.T) {
	t.Parallel()

	cases := map[string]string{
		"agent":                        "agent",
		"  agent --flag  ":             "agent",
		"/usr/local/bin/agent --flag":  "/usr/local/bin/agent",
		"agent remote --name {name}":   "agent",
		"":                             "",
		"   ":                          "",
		"env FOO=bar agent --flag":     "env",
		"agent\t--tab-separated-flags": "agent",
	}

	for command, want := range cases {
		if got := commandBinary(command); got != want {
			t.Errorf("commandBinary(%q) = %q, want %q", command, got, want)
		}
	}
}

// TestNoLookPathRefuses keeps the zero value of Host from being a check that
// silently passes everything.
func TestNoLookPathRefuses(t *testing.T) {
	t.Parallel()

	if err := CheckOn(Host{}, config.NewStartCommands(nil), nil); err == nil {
		t.Fatal("a Host that cannot look a binary up passed the check")
	}
}
