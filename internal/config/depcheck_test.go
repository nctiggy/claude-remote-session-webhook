package config_test

// The startup dependency check (T029 and T030, FR-012 … FR-015, SC-011): what
// the daemon refuses to start without, what it merely complains about, the
// clause the requirement is really about — that it complains about the command
// this operator configured rather than the one this project happens to be named
// after — and what it tells them to type to fix it.
//
// Every case drives the check through an injected probe rather than the real
// PATH, and through an injected identification rather than the real
// /etc/os-release. A test that emptied PATH would be describing the process
// instead of a host, and would be doing it to every other test in this binary at
// the same time; a test that read the real os-release would be asserting against
// whichever machine the suite happens to be running on.

import (
	"bytes"
	"errors"
	"fmt"
	"go/ast"
	"os"
	"slices"
	"strconv"
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
	// identification is what this host's /etc/os-release says, and nil is a host
	// that has no such file. Every case except the ones about the install
	// command leaves it nil, because the platform is not what they are
	// describing and the file the real machine has is not theirs to read.
	identification []byte
}

func newHostTools(installed ...string) *hostTools {
	h := &hostTools{installed: make(map[string]bool, len(installed))}
	for _, name := range installed {
		h.installed[name] = true
	}
	return h
}

func (h *hostTools) osRelease() []byte { return h.identification }

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
		host.lookPath, host.osRelease, &warn)

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
		host.lookPath, host.osRelease, &warn)

	if err != nil {
		t.Fatalf("a missing start command refused the start: %v", err)
	}

	got := warn.String()
	for _, want := range []string{`"default"`, `"rc"`, `"claude"`, "not on", "PATH", "starting anyway"} {
		if !strings.Contains(got, want) {
			t.Errorf("the warning does not mention %q:\n%s", want, got)
		}
	}
}

// TestWarningNamesThePathItProbed is issue #96, and it is the difference between
// a warning and a wrong warning.
//
// The probe runs in the daemon's process. The command does not: it is typed into
// a shell in a tmux pane, which loads the operator's profile and on the live
// deployment carried a ~/.local/bin the systemd user manager never had. So the
// daemon answered "not on PATH" about a claude that was on the session's PATH
// the whole time, and then predicted a failure that never happened on every
// single start.
//
// The daemon cannot resolve the pane's PATH from here without executing the
// operator's profile at startup, which this package may not do
// (TestNeverExecutesInstall). What it can do is stop claiming an outcome it has
// no way to know and say which PATH it actually read — so that the operator of a
// healthy host reads a difference between two environments rather than a
// prediction that their working sessions are about to break.
func TestWarningNamesThePathItProbed(t *testing.T) {
	t.Parallel()

	host := newHostTools("tmux")
	var warn warnBuffer

	if err := config.CheckDependenciesWith(
		configWith(map[string]string{"rc": "claude remote-control"}, ""),
		host.lookPath, host.osRelease, &warn); err != nil {
		t.Fatalf("refused the start: %v", err)
	}

	got := warn.String()

	// Which PATH was read, and whose PATH decides. Either half alone still reads
	// as a verdict on the session: the first without the second is a scope the
	// operator has no reason to think matters, and the second without the first
	// does not say what was measured.
	for _, want := range []string{"this daemon's PATH", "tmux pane"} {
		if !strings.Contains(got, want) {
			t.Errorf("the warning does not say %q, so it does not say what it checked:\n%s", want, got)
		}
	}

	// And the claim it may not make. The daemon does not know this, was wrong
	// about it on a working deployment, and an operator who learns to scroll past
	// a false alarm has been taught to scroll past a true one.
	for _, forbidden := range []string{"will fail", "until it is present"} {
		if strings.Contains(got, forbidden) {
			t.Errorf("the warning predicts %q about a command it never resolved in the environment that runs it:\n%s", forbidden, got)
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
		host.lookPath, host.osRelease, &warn)

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
				host.lookPath, host.osRelease, &warn)

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
				configWith(tc.commands, tc.filePath), host.lookPath, host.osRelease, &warn); err != nil {
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

// ---------------------------------------------------------------------------
// The install command (T030, FR-013, FR-014)
// ---------------------------------------------------------------------------

// TestInstallCommandFromOsRelease is FR-013: the command comes from what the
// host says it is, never from what this daemon assumes it is.
//
// The table is the point. Every Linux row compiles to the same GOOS, so a
// derivation that read GOOS alone would answer all four of them identically and
// tell an Alpine operator to run apt — which is the failure this requirement
// names, and which no single-row test can see.
func TestInstallCommandFromOsRelease(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		osRelease string
		goos      string
		want      string
	}{
		"debian names itself in ID": {
			osRelease: "PRETTY_NAME=\"Debian GNU/Linux 12 (bookworm)\"\nNAME=\"Debian GNU/Linux\"\nID=debian\n",
			goos:      "linux",
			want:      "sudo apt install tmux",
		},
		"ubuntu inherits it through ID_LIKE": {
			osRelease: "NAME=\"Ubuntu\"\nID=ubuntu\nID_LIKE=debian\nVERSION_ID=\"24.04\"\n",
			goos:      "linux",
			want:      "sudo apt install tmux",
		},
		"rhel declares what it is like": {
			osRelease: "NAME=\"Red Hat Enterprise Linux\"\nID=\"rhel\"\nID_LIKE=\"fedora\"\n",
			goos:      "linux",
			want:      "sudo dnf install tmux",
		},
		"a rhel derivative names several": {
			osRelease: "ID=\"centos\"\nID_LIKE=\"rhel fedora\"\n",
			goos:      "linux",
			want:      "sudo dnf install tmux",
		},
		"arch": {
			osRelease: "NAME=\"Arch Linux\"\nID=arch\nBUILD_ID=rolling\n",
			goos:      "linux",
			want:      "sudo pacman -S tmux",
		},
		"alpine": {
			osRelease: "NAME=\"Alpine Linux\"\nID=alpine\nVERSION_ID=3.20.0\n",
			goos:      "linux",
			want:      "sudo apk add tmux",
		},
		// The one row GOOS is allowed to decide, and only because it is the row
		// with no file to read: macOS ships no /etc/os-release, so this is the
		// absence of an identification rather than an assumption made over one.
		"macos has no os-release at all": {
			osRelease: "",
			goos:      "darwin",
			want:      "brew install tmux",
		},
		// Comments and blank lines are a distribution's own commentary, not a
		// reason to stop recognising the host underneath them.
		"commentary around the field": {
			osRelease: "# Written by the distribution\n\n   ID=debian   \n# and nothing after it\n",
			goos:      "linux",
			want:      "sudo apt install tmux",
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got := config.InstallAdviceFor([]byte(tc.osRelease), tc.goos)
			if !strings.Contains(got, tc.want) {
				t.Errorf("advice for this host is %q, which does not name %q", got, tc.want)
			}
			if got == config.GenericInstallAdvice {
				t.Errorf("a host that identified itself got the sentence for one that did not: %q", got)
			}
		})
	}

	// The derivation is only worth having where an operator reads it, and the
	// refusal is the only place that does. This repo has shipped code with no
	// caller three times.
	t.Run("the refusal is where an operator reads it", func(t *testing.T) {
		t.Parallel()

		host := newHostTools("claude")
		host.identification = []byte("ID=debian\n")
		var warn warnBuffer

		err := config.CheckDependenciesWith(
			configWith(map[string]string{"default": "claude"}, ""),
			host.lookPath, host.osRelease, &warn)

		if err == nil {
			t.Fatal("a host with no tmux started")
		}
		if !strings.Contains(err.Error(), "sudo apt install tmux") {
			t.Errorf("the refusal does not carry the install command for this host: %v", err)
		}
	})
}

// TestUnknownPlatformSaysSo is the row that makes the other seven safe. A
// platform this daemon does not recognise is told so in a sentence, because the
// alternative — naming a package manager on the strength of nothing — sends the
// operator to a command that does not exist on the host they are standing in
// front of, from a daemon that has just refused to start.
func TestUnknownPlatformSaysSo(t *testing.T) {
	t.Parallel()

	cases := map[string]string{
		"an ID nothing here knows":       "NAME=\"Plan 9\"\nID=plan9\n",
		"no file at all":                 "",
		"a file that identifies nothing": "# a comment and nothing else\n\n",
		// Not a refusal: this daemon does not own /etc/os-release and cannot
		// have the operator correct it, so a line it cannot read costs the
		// command and nothing more.
		"a line it cannot parse": "ID\nID_LIKE\n\x00\n",
		// The field has to match the whole key. A daemon matching a prefix would
		// read VERSION_ID as ID and answer for a version number.
		"a longer key that starts the same way": "VERSION_ID=\"debian\"\nBUILD_ID=arch\n",
	}

	for name, osRelease := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got := config.InstallAdviceFor([]byte(osRelease), "linux")
			if got != config.GenericInstallAdvice {
				t.Errorf("an unrecognised host was told %q, want %q", got, config.GenericInstallAdvice)
			}
			// Restated rather than derived: the failure is a *guess*, and a
			// check that asked the package what it would have guessed would
			// agree with the guess.
			for _, manager := range []string{"apt", "dnf", "pacman", "apk", "brew", "sudo"} {
				if strings.Contains(got, manager) {
					t.Errorf("the sentence for an unidentified host names %q: %q", manager, got)
				}
			}
		})
	}

	// And the refusal says it rather than falling back to a command.
	t.Run("the refusal carries the sentence", func(t *testing.T) {
		t.Parallel()

		host := newHostTools("claude")
		host.identification = []byte("ID=plan9\n")
		var warn warnBuffer

		err := config.CheckDependenciesWith(
			configWith(map[string]string{"default": "claude"}, ""),
			host.lookPath, host.osRelease, &warn)

		if err == nil {
			t.Fatal("a host with no tmux started")
		}
		if !strings.Contains(err.Error(), config.GenericInstallAdvice) {
			t.Errorf("the refusal on an unidentified host does not say %q: %v", config.GenericInstallAdvice, err)
		}
	})
}

// TestReadsTheSystemsOwnIdentification covers the one line no fixture can reach:
// the path. Every case above hands the derivation its bytes, so a reader opening
// /etc/os-relase — a typo no compiler and no injected test can see — would leave
// all of them green while every Linux host on earth came out unidentified.
func TestReadsTheSystemsOwnIdentification(t *testing.T) {
	t.Parallel()

	// Restated rather than exported from the package under test. A path read out
	// of the answer agrees with the answer by construction, which is precisely
	// the mistake this case exists to catch.
	const path = "/etc/os-release"

	want, err := os.ReadFile(path) //nolint:gosec // G304: a constant path, and the file every distribution publishes.
	switch {
	case errors.Is(err, os.ErrNotExist):
		// A host without one is not a failure — it is the unidentified platform
		// the generic sentence is for — but it has to read as nothing rather
		// than as something.
		if got := config.ReadOsRelease(); got != nil {
			t.Fatalf("this host has no %s, yet the reader returned %d bytes", path, len(got))
		}
		return
	case err != nil:
		t.Skipf("this host has an %s the suite cannot read, which is not a claim about the daemon: %v", path, err)
	}

	if len(want) > config.MaxOsReleaseBytes {
		want = want[:config.MaxOsReleaseBytes]
	}
	got := config.ReadOsRelease()
	if len(got) == 0 {
		t.Fatalf("this host publishes %d bytes of identification and the reader returned none", len(want))
	}
	if !bytes.Equal(got, want) {
		t.Errorf("the reader returned %d bytes, and %s holds %d", len(got), path, len(want))
	}
}

// TestNeverExecutesInstall is FR-014, and it is structural because the
// behaviour it forbids is one nobody writes a test for: an "auto-install"
// convenience is added as a helpfulness, and every existing case here stays
// green while a daemon that can be made to install software ships.
//
// os/exec is reachable from this package for exactly one reason — asking PATH
// whether a name resolves — and LookPath is the only member of it that runs
// nothing.
func TestNeverExecutesInstall(t *testing.T) {
	t.Parallel()

	const probe = "LookPath"

	fset, files := packageFiles(t)

	// An import under another name would put every call beyond the selector walk
	// below, which would then pass by seeing nothing.
	for name, file := range files {
		for _, imp := range file.Imports {
			path, err := strconv.Unquote(imp.Path.Value)
			if err != nil || path != "os/exec" {
				continue
			}
			if imp.Name != nil {
				t.Errorf("%s: %s imports os/exec as %q; the walk below looks for exec.%s and would not see a call made through another name",
					fset.Position(imp.Pos()), name, imp.Name.Name, probe)
			}
		}
	}

	var probes int
	for name, file := range files {
		ast.Inspect(file, func(n ast.Node) bool {
			sel, ok := n.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			pkg, ok := sel.X.(*ast.Ident)
			if !ok || pkg.Name != "exec" {
				return true
			}
			probes++
			if sel.Sel.Name != probe {
				t.Errorf("%s: %s reaches os/exec for %s; this package may ask PATH whether a name resolves and nothing else. It names the install command and the operator runs it — a daemon that installs software is a daemon that can be made to install software",
					fset.Position(sel.Pos()), name, sel.Sel.Name)
			}
			return true
		})
	}

	if probes == 0 {
		t.Fatalf("nothing in this package reaches os/exec, so the dependency probe has gone and this test is checking an empty walk")
	}
}
