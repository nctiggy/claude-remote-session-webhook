package config_test

// The startup dependency check (T029, FR-012 … FR-015, SC-011): what the daemon
// refuses to start without, what it merely complains about, and — the clause the
// requirement is really about — that it complains about the command this
// operator configured rather than the one this project happens to be named
// after.
//
// Every case drives the check through an injected probe rather than the real
// PATH. A test that emptied PATH would be describing the process instead of a
// host, and would be doing it to every other test in this binary at the same
// time.

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/nctiggy/claude-remote-session-webhook/internal/config"
)

// hostTools is a host, described by what is installed on it: it answers the
// probe and records every name it was asked about, which is the half of the
// behaviour no message can show.
type hostTools struct {
	installed map[string]bool
	asked     []string
}

func newHostTools(installed ...string) *hostTools {
	h := &hostTools{installed: make(map[string]bool, len(installed))}
	for _, name := range installed {
		h.installed[name] = true
	}
	return h
}

func (h *hostTools) lookPath(name string) (string, error) {
	h.asked = append(h.asked, name)
	if !h.installed[name] {
		return "", fmt.Errorf("exec: %q: executable file not found in $PATH", name)
	}
	return "/usr/bin/" + name, nil
}

// warnBuffer is the warning sink. It is a type rather than a bytes.Buffer so
// that "nothing was written" reads as a sentence in the failure message.
type warnBuffer struct{ written strings.Builder }

func (w *warnBuffer) Write(p []byte) (int, error) { return w.written.Write(p) }
func (w *warnBuffer) String() string              { return w.written.String() }

// configWith is a loaded Config carrying nothing but the two fields this check
// reads. Everything else has already been validated by the time the check runs,
// and a fixture that set it would be describing a load rather than a probe.
func configWith(commands map[string]string, filePath string) config.Config {
	return config.Config{
		StartCommands: config.NewStartCommands(commands),
		FilePath:      filePath,
	}
}

// TestMissingTmuxRefusesToStart is the fatal half. Without tmux this daemon can
// do nothing, so it says so at startup rather than at the operator's first
// create.
func TestMissingTmuxRefusesToStart(t *testing.T) {
	t.Parallel()

	host := newHostTools("claude")
	var warn warnBuffer

	err := config.CheckDependenciesWith(
		configWith(map[string]string{"default": "claude --dangerously-skip-permissions"}, ""),
		host.lookPath, &warn)

	if err == nil {
		t.Fatalf("a host with no tmux started; warnings were:\n%s", warn.String())
	}
	for _, want := range []string{"tmux", "refusing to start"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not mention %q: %v", want, err)
		}
	}
	// The refusal is the whole answer: a daemon that also warned about the
	// start commands of a host it is not going to run on would be describing a
	// configuration nobody is going to reach.
	if warn.String() != "" {
		t.Errorf("the refusal also wrote a warning:\n%s", warn.String())
	}
}

// TestMissingStartCommandWarnsOnly is the other half, and it is a
// *non*-refusal: the daemon that cannot start a session can still serve the
// dashboard, adopt what is already on the host, and say what is wrong — which is
// the only way the operator finds out.
func TestMissingStartCommandWarnsOnly(t *testing.T) {
	t.Parallel()

	host := newHostTools("tmux")
	var warn warnBuffer

	err := config.CheckDependenciesWith(
		configWith(map[string]string{
			"default": "claude --dangerously-skip-permissions",
			"rc":      "claude remote-control --name {name}",
		}, "/home/operator/.config/crswd/config"),
		host.lookPath, &warn)

	if err != nil {
		t.Fatalf("a missing start command refused the start: %v", err)
	}

	got := warn.String()
	for _, want := range []string{`"default"`, `"rc"`, `"claude"`, "not on PATH", "starting anyway"} {
		if !strings.Contains(got, want) {
			t.Errorf("the warning does not mention %q:\n%s", want, got)
		}
	}
}

// TestChecksConfiguredCommandNotClaude is FR-015. The host here has claude on
// it, so a check that probed the name this project is called would find
// everything present and say nothing at all — which is the failure: the command
// that will actually be typed is the one that is missing.
func TestChecksConfiguredCommandNotClaude(t *testing.T) {
	t.Parallel()

	host := newHostTools("tmux", "claude")
	var warn warnBuffer

	err := config.CheckDependenciesWith(
		configWith(map[string]string{
			"default": "claude --dangerously-skip-permissions",
			"x":       "frobnicate",
		}, ""),
		host.lookPath, &warn)

	if err != nil {
		t.Fatalf("a missing start command refused the start: %v", err)
	}

	got := warn.String()
	if !strings.Contains(got, `"frobnicate"`) || !strings.Contains(got, `"x"`) {
		t.Errorf("the configured command was not the one reported:\n%s", got)
	}
	if !slices.Contains(host.asked, "frobnicate") {
		t.Errorf("frobnicate was never probed; the check asked about %q", host.asked)
	}
}

// TestProbesFirstWordOnly holds the probe to the part of a command line PATH can
// answer for. The whole line looked up as a filename finds nothing, which would
// make this check warn about every correctly configured daemon there is.
func TestProbesFirstWordOnly(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		command string
		want    string
	}{
		"arguments follow the binary": {"claude --dangerously-skip-permissions", "claude"},
		"a placeholder is an argument": {
			"claude remote-control --name {name}", "claude",
		},
		"leading space is not part of the name": {"  claude  --resume", "claude"},
		"a path is taken as written":            {"/opt/claude/bin/claude --resume", "/opt/claude/bin/claude"},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			host := newHostTools("tmux", tc.want)
			var warn warnBuffer

			err := config.CheckDependenciesWith(
				configWith(map[string]string{"default": tc.command}, ""),
				host.lookPath, &warn)

			if err != nil {
				t.Fatalf("refused the start: %v", err)
			}
			if warn.String() != "" {
				t.Errorf("an installed command warned:\n%s", warn.String())
			}
			// Positional, because "it asked about the right thing" and "it asked
			// about nothing else" are two different claims and the second is the
			// one a whole-line lookup breaks.
			if len(host.asked) != 2 || host.asked[0] != "tmux" || host.asked[1] != tc.want {
				t.Errorf("probed %q, want [tmux %s]", host.asked, tc.want)
			}
		})
	}
}

// TestMessageNamesConfigFile is the "and where" half of the warning. An operator
// told that a command is missing and not told which file names it is an operator
// grepping their host for a string.
//
// The contract lists this test and no task owns it; it is here because this is
// the task that writes the message.
func TestMessageNamesConfigFile(t *testing.T) {
	t.Parallel()

	const path = "/home/operator/.config/crswd/config"

	cases := map[string]struct {
		filePath string
		commands map[string]string
		want     []string
	}{
		"a file was read": {
			filePath: path,
			commands: map[string]string{"default": "claude", "rc": "frobnicate"},
			// The file's own spelling of the key, because that is what the
			// operator will be looking at when they follow this sentence.
			want: []string{path, "start_commands"},
		},
		"the daemon is configured by environment": {
			filePath: "",
			commands: map[string]string{"default": "frobnicate"},
			want:     []string{config.EnvStartCommand, "environment"},
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			host := newHostTools("tmux", "claude")
			var warn warnBuffer

			if err := config.CheckDependenciesWith(
				configWith(tc.commands, tc.filePath), host.lookPath, &warn); err != nil {
				t.Fatalf("refused the start: %v", err)
			}

			got := warn.String()
			for _, want := range tc.want {
				if !strings.Contains(got, want) {
					t.Errorf("the warning does not say %q:\n%s", want, got)
				}
			}
		})
	}
}
