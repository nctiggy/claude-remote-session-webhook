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
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/nctiggy/claude-remote-session-webhook/internal/config"
)

// hostTools is a host, described by what is installed on it: it answers the
// probe and records every name it was asked about, which is the half of the
// behaviour no message can show.
//
// It is two environments and not one (T014, #96). `installed` is what this
// daemon's own process can see, which under the deployed unit is the systemd
// user manager's PATH; `loginPATH` is the directory list a session's login shell
// hands the command it is actually going to run. A host where the two agree is
// the easy case and is not the one that shipped.
type hostTools struct {
	installed map[string]bool
	asked     []string
	// identification is what this host's /etc/os-release says, and nil is a host
	// that has no such file. Every case except the ones about the install
	// command leaves it nil, because the platform is not what they are
	// describing and the file the real machine has is not theirs to read.
	identification []byte

	// loginPATH is a real directory, because the check resolves a name against
	// this list itself: a fake that answered "installed" would agree with
	// whatever the caller believed and skip the resolution entirely. It starts
	// empty, so by default this is a login shell that finds nothing.
	loginPATH string
	// loginErr is a login shell that could not be asked at all — no shell, a
	// profile that hung, a shell that printed nothing. FR-023c's case.
	loginErr error
	// loginAsks counts the profile executions this check causes. Zero is a
	// claim: a daemon whose commands are all present has no business running
	// the operator's profile at startup.
	loginAsks int
}

func newHostTools(t *testing.T, installed ...string) *hostTools {
	t.Helper()

	h := &hostTools{
		installed: make(map[string]bool, len(installed)),
		loginPATH: t.TempDir(),
	}
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

// loginShellPATH is what a session's shell answers, and counting the calls is
// the point as much as the answer is.
func (h *hostTools) loginShellPATH() (string, error) {
	h.loginAsks++
	if h.loginErr != nil {
		return "", h.loginErr
	}
	return h.loginPATH, nil
}

// loginShellFinds puts a real executable on the login shell's PATH — the shape
// of #96 on the live host, where claude is a symlink in ~/.local/bin that the
// service manager's PATH has never included.
func (h *hostTools) loginShellFinds(t *testing.T, names ...string) {
	t.Helper()

	for _, name := range names {
		path := filepath.Join(h.loginPATH, name)
		if err := os.WriteFile(path, []byte("#!/bin/sh\n"), 0o600); err != nil {
			t.Fatalf("write the command this host has: %v", err)
		}
		// The execute bit is the property the probe reads, so the fixture has
		// to really carry it.
		//nolint:gosec // G302: a mode with no execute bit is not the file this case is describing.
		if err := os.Chmod(path, 0o700); err != nil {
			t.Fatalf("make %s executable, which is the whole of what the probe looks for: %v", name, err)
		}
	}
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

	host := newHostTools(t, "claude")
	var warn warnBuffer

	err := config.CheckDependenciesWith(
		configWith(map[string]string{"default": "claude --dangerously-skip-permissions"}, ""),
		host.lookPath, host.loginShellPATH, host.osRelease, &warn)

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

	host := newHostTools(t, "tmux")
	var warn warnBuffer

	err := config.CheckDependenciesWith(
		configWith(map[string]string{
			"default": "claude --dangerously-skip-permissions",
			"rc":      "claude remote-control --name {name}",
		}, "/home/operator/.config/crswd/config"),
		host.lookPath, host.loginShellPATH, host.osRelease, &warn)

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

	host := newHostTools(t, "tmux", "claude")
	var warn warnBuffer

	err := config.CheckDependenciesWith(
		configWith(map[string]string{
			"default": "claude --dangerously-skip-permissions",
			"x":       "frobnicate",
		}, ""),
		host.lookPath, host.loginShellPATH, host.osRelease, &warn)

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

			host := newHostTools(t, "tmux", tc.want)
			var warn warnBuffer

			err := config.CheckDependenciesWith(
				configWith(map[string]string{"default": tc.command}, ""),
				host.lookPath, host.loginShellPATH, host.osRelease, &warn)

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
			// Asking the login shell means running the operator's profile. A
			// host where everything is already present has given this daemon no
			// reason to cause that.
			if host.loginAsks != 0 {
				t.Errorf("a host with the command on its own PATH ran the operator's profile %d times", host.loginAsks)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// The environment the command actually runs in (T014, FR-023b, FR-023c, #96)
// ---------------------------------------------------------------------------

// TestProbeResolvesThroughLoginShell is the shipped defect. The live daemon
// warned on every start that "claude" was missing while sessions using it
// worked: the probe asked the systemd user manager's PATH, and the command is
// typed into a login shell inside a tmux pane, which has ~/.local/bin.
//
// Must fail when the probe keeps asking this daemon's own PATH — the host below
// is exactly the deployed one, and a check that reads a single environment
// cannot tell it from a broken host.
func TestProbeResolvesThroughLoginShell(t *testing.T) {
	t.Parallel()

	host := newHostTools(t, "tmux")
	host.loginShellFinds(t, "claude")
	var warn warnBuffer

	err := config.CheckDependenciesWith(
		configWith(map[string]string{
			"default": "claude --dangerously-skip-permissions",
			"rc":      "claude remote-control --name {name}",
		}, ""),
		host.lookPath, host.loginShellPATH, host.osRelease, &warn)

	if err != nil {
		t.Fatalf("refused the start: %v", err)
	}
	if got := warn.String(); got != "" {
		t.Errorf("a command a session will find was reported anyway:\n%s", got)
	}
	// One profile execution per start, not one per command. Two commands here
	// for that reason: the answer is a property of the shell, and asking twice
	// runs an operator's ~/.profile twice on the way to the same PATH.
	if host.loginAsks != 1 {
		t.Errorf("the login shell was asked %d times for two commands; it answers once for the host", host.loginAsks)
	}
}

// TestGenuinelyMissingCommandStillWarns is the other direction, and it is what
// stops the fix above from being a way to stop hearing about anything: a command
// on neither PATH is a command that really is not there.
//
// Must fail when the fix silences the check entirely.
func TestGenuinelyMissingCommandStillWarns(t *testing.T) {
	t.Parallel()

	// The login shell answers, and what it answers is a directory with nothing
	// in it. That is the distinction this test rests on: the question was asked
	// and the answer was no.
	host := newHostTools(t, "tmux")
	var warn warnBuffer

	err := config.CheckDependenciesWith(
		configWith(map[string]string{"default": "frobnicate --flag"}, ""),
		host.lookPath, host.loginShellPATH, host.osRelease, &warn)

	if err != nil {
		t.Fatalf("a missing start command refused the start: %v", err)
	}

	got := warn.String()
	for _, want := range []string{`"frobnicate"`, "not on PATH", "starting anyway"} {
		if !strings.Contains(got, want) {
			t.Errorf("the warning does not say %q:\n%s", want, got)
		}
	}
	if host.loginAsks != 1 {
		t.Errorf("the login shell was asked %d times before this daemon called a command missing; the answer needs both", host.loginAsks)
	}
}

// TestProbeNamesWhatItChecked is FR-023c. "Not on PATH" is a claim about an
// environment, and this daemon has two — so a message that does not say which
// one it looked in cannot be told apart from a probe looking in the wrong place.
// That is what an operator was left with for a whole milestone.
//
// The second case is the one the requirement is really for: the login shell
// could not be asked at all, and the honest answer is what was checked rather
// than an assertion that the command is absent.
func TestProbeNamesWhatItChecked(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		loginErr error
		want     []string
		// absent is the vocabulary of a daemon claiming the command is not
		// there. The note must not use it: nothing was found missing, a
		// question went unanswered.
		absent []string
	}{
		"both environments were asked": {
			want: []string{"checked:", "own PATH", "login shell"},
		},
		"the login shell could not be asked": {
			loginErr: errors.New("no login shell on this host"),
			want: []string{
				"checked:", "own PATH", "and nothing else",
				"may still find it", "no login shell on this host",
			},
			absent: []string{"will fail", "starting anyway"},
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			host := newHostTools(t, "tmux")
			host.loginErr = tc.loginErr
			var warn warnBuffer

			if err := config.CheckDependenciesWith(
				configWith(map[string]string{"rc": "frobnicate"}, ""),
				host.lookPath, host.loginShellPATH, host.osRelease, &warn); err != nil {
				t.Fatalf("refused the start: %v", err)
			}

			got := warn.String()
			for _, want := range tc.want {
				if !strings.Contains(got, want) {
					t.Errorf("the message does not say %q:\n%s", want, got)
				}
			}
			for _, absent := range tc.absent {
				if strings.Contains(got, absent) {
					t.Errorf("a message about a question nobody answered says %q, which is a claim about the command:\n%s", absent, got)
				}
			}
		})
	}
}

// TestMissingTmuxStillFatal is the probe that does not change. The daemon execs
// tmux itself, so its own PATH is the right environment to ask about it, and
// without tmux there is no session to have a PATH problem in.
//
// Must fail when the tmux probe is loosened along with the other one — which is
// what the second case describes: a host whose login shell has tmux and whose
// daemon cannot see it is a daemon that cannot start a session.
func TestMissingTmuxStillFatal(t *testing.T) {
	t.Parallel()

	t.Run("no tmux anywhere refuses, and says what to type", func(t *testing.T) {
		t.Parallel()

		host := newHostTools(t)
		host.identification = []byte("ID=debian\n")
		var warn warnBuffer

		err := config.CheckDependenciesWith(
			configWith(map[string]string{"default": "claude"}, ""),
			host.lookPath, host.loginShellPATH, host.osRelease, &warn)

		if err == nil {
			t.Fatalf("a host with no tmux started; warnings were:\n%s", warn.String())
		}
		for _, want := range []string{"tmux", "refusing to start", "sudo apt install tmux"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("the refusal does not mention %q: %v", want, err)
			}
		}
	})

	t.Run("a login shell that has tmux does not rescue it", func(t *testing.T) {
		t.Parallel()

		host := newHostTools(t)
		host.loginShellFinds(t, "tmux")
		var warn warnBuffer

		err := config.CheckDependenciesWith(
			configWith(map[string]string{"default": "claude"}, ""),
			host.lookPath, host.loginShellPATH, host.osRelease, &warn)

		if err == nil {
			t.Fatal("tmux on a session's PATH started a daemon that cannot exec it; this daemon runs tmux itself, not in a pane")
		}
		if host.loginAsks != 0 {
			t.Errorf("the tmux probe asked the login shell %d times; the question is about this process", host.loginAsks)
		}
	})
}

// TestTheProbeReallyAsksALoginShell is the one claim no injected fixture can
// make, and it is this repository's recurring failure written as a test: every
// case above hands the check a directory list, so a probe that started the wrong
// shell, passed the wrong flag, or read the wrong stream would leave all of them
// green while the live daemon went on warning about a command that works.
//
// It runs a real shell against a real profile. That is the only way to assert
// that a *login* shell is what gets asked: an ordinary one never reads
// ~/.profile, which is where the PATH in #96 comes from.
func TestTheProbeReallyAsksALoginShell(t *testing.T) {
	// Named here rather than exported from the package under test: $SHELL is set
	// below, so this is the shell this case asks for and not the one the daemon
	// falls back to. It is the shell every host that can run tmux has.
	const shell = "/bin/sh"

	// Not parallel: t.Setenv, and the two variables below are the process's.
	if _, err := os.Stat(shell); err != nil {
		t.Skipf("this host has no %s to ask, which is not a claim about the daemon: %v", shell, err)
	}

	home := t.TempDir()
	// The directory a profile adds — the shape of ~/.local/bin, which is the
	// whole of #96.
	bin := filepath.Join(home, "profile-only-bin")
	if err := os.MkdirAll(bin, 0o700); err != nil {
		t.Fatalf("make the directory this host's profile adds: %v", err)
	}
	profile := "PATH=\"$PATH:" + bin + "\"\nexport PATH\n"
	if err := os.WriteFile(filepath.Join(home, ".profile"), []byte(profile), 0o600); err != nil {
		t.Fatalf("write the profile: %v", err)
	}

	t.Setenv("HOME", home)
	t.Setenv("SHELL", shell)

	got, err := config.LoginShellPATH()
	if err != nil {
		t.Fatalf("the login shell could not be asked on a host that has one: %v", err)
	}
	if !slices.Contains(filepath.SplitList(got), bin) {
		t.Errorf("the PATH this daemon read is %q, and the directory ~/.profile adds is not in it — so the shell it asked was not a login shell", got)
	}
}

// ---------------------------------------------------------------------------
// The message (T029, FR-012)
// ---------------------------------------------------------------------------

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

			host := newHostTools(t, "tmux", "claude")
			var warn warnBuffer

			if err := config.CheckDependenciesWith(
				configWith(tc.commands, tc.filePath), host.lookPath, host.loginShellPATH, host.osRelease, &warn); err != nil {
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

// TestNoSecretInAnyDiagnostic is FR-043 at the messages this daemon writes
// before it has a trail to write to, and it is the reason each quotes the binary
// rather than the command line it was cut from.
//
// A start command is a whole command line an operator wrote, and its arguments
// take whatever the program takes — an API key or a token among them. This
// sentence goes to stderr, systemd puts it in the journal, and the journal
// outlives the process; a diagnostic that quoted the configuration it is
// complaining about would publish a credential to fix a PATH problem. The
// shared secret is here too because Config carries it and formatting a Config
// is one `%v` away from any message in this package.
//
// Must fail when a warning quotes configuration verbatim — print the command
// instead of its first word, or the Config instead of the two fields, and the
// marks below turn up in the journal.
func TestNoSecretInAnyDiagnostic(t *testing.T) {
	t.Parallel()

	// Spelled in words, like testSecret in internal/httpapi: a run of hex digits
	// this long is what a real credential looks like, and gitleaks — correctly —
	// refuses to let one into the repository.
	const (
		sharedSecret = "marked-shared-secret-never-printed"
		inAnArgument = "marked-credential-inside-an-argument"
	)

	// The binary is missing and the credential is one of its arguments, which is
	// the shape this is really about: the check has to say the first word and
	// nothing after it.
	marked := config.Config{
		SharedSecret: []byte(sharedSecret),
		StartCommands: config.NewStartCommands(map[string]string{
			"default": "frobnicate --api-key " + inAnArgument,
		}),
		FilePath: "/home/operator/.config/crswd/config",
	}

	// Every diagnostic this package can write, because the rule is about the
	// journal and not about any one sentence. T014 added the third: a message
	// written on the path where the login shell could not be asked is still a
	// message about a command line an operator wrote.
	cases := map[string]struct {
		host func(*testing.T) *hostTools
		// fatal is the tmux refusal, whose sentence is an error rather than a
		// line on the warning stream. Both are diagnostics and the rule is the
		// same for each.
		fatal bool
	}{
		"the warning about a missing start command": {
			host: func(t *testing.T) *hostTools { t.Helper(); return newHostTools(t, "tmux") },
		},
		"the note about a login shell that could not be asked": {
			host: func(t *testing.T) *hostTools {
				t.Helper()
				host := newHostTools(t, "tmux")
				host.loginErr = errors.New("no login shell on this host")
				return host
			},
		},
		"the refusal to start without tmux": {
			host:  func(t *testing.T) *hostTools { t.Helper(); return newHostTools(t) },
			fatal: true,
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			host := tc.host(t)
			var warn warnBuffer
			err := config.CheckDependenciesWith(marked, host.lookPath, host.loginShellPATH, host.osRelease, &warn)

			said := warn.String()
			if tc.fatal {
				if err == nil {
					t.Fatal("a host with no tmux started, so there is no refusal here to read")
				}
				said = err.Error()
			} else {
				if err != nil {
					t.Fatalf("refused the start: %v", err)
				}
				// Without this the sweep below is over an empty string, which
				// passes for the wrong reason. Unquoted on purpose: a message
				// that names the whole command line still satisfies "it
				// reported something", and it is the sweep below that must be
				// the one to fail on it.
				if !strings.Contains(said, "frobnicate") {
					t.Fatalf("the missing command was not reported at all:\n%s", said)
				}
			}

			for what, mark := range map[string]string{
				"the shared secret":                     sharedSecret,
				"an argument of the configured command": inAnArgument,
			} {
				if strings.Contains(said, mark) {
					// Printed because an operator would need to see it; by this
					// point the value is already in the journal.
					t.Errorf("%s appears in a diagnostic, which systemd keeps after the process is gone:\n%s", what, said)
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

		host := newHostTools(t, "claude")
		host.identification = []byte("ID=debian\n")
		var warn warnBuffer

		err := config.CheckDependenciesWith(
			configWith(map[string]string{"default": "claude"}, ""),
			host.lookPath, host.loginShellPATH, host.osRelease, &warn)

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

		host := newHostTools(t, "claude")
		host.identification = []byte("ID=plan9\n")
		var warn warnBuffer

		err := config.CheckDependenciesWith(
			configWith(map[string]string{"default": "claude"}, ""),
			host.lookPath, host.loginShellPATH, host.osRelease, &warn)

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
// os/exec is reachable from this package for exactly two reasons, and neither of
// them runs anything this daemon was told about. LookPath asks PATH whether a
// name resolves. CommandContext starts one program — the operator's own login
// shell, to ask what PATH it has (T014) — and the check below is that its argv
// is written in this file rather than assembled from anything: a configured
// command line that reached a subprocess would be the shell string
// docs/security.md §2 forbids, arriving by the back door.
func TestNeverExecutesInstall(t *testing.T) {
	t.Parallel()

	// The login shell's argv, which is the reason CommandContext is allowed:
	// everything past the program name is written here, and the program name is
	// $SHELL. Nothing configured, and nothing from a request, can appear.
	const startsALoginShell = "CommandContext"

	probes := map[string]bool{"LookPath": true, startsALoginShell: true}

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
				t.Errorf("%s: %s imports os/exec as %q; the walk below looks for exec.LookPath and exec.%s and would not see a call made through another name",
					fset.Position(imp.Pos()), name, imp.Name.Name, startsALoginShell)
			}
		}
	}

	var reached, shells int
	for name, file := range files {
		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			pkg, ok := sel.X.(*ast.Ident)
			if !ok || pkg.Name != "exec" {
				return true
			}

			reached++
			if !probes[sel.Sel.Name] {
				t.Errorf("%s: %s reaches os/exec for %s; this package may ask PATH whether a name resolves and start a login shell to ask the same of it, and nothing else. It names the install command and the operator runs it — a daemon that installs software is a daemon that can be made to install software",
					fset.Position(sel.Pos()), name, sel.Sel.Name)
				return true
			}
			if sel.Sel.Name != startsALoginShell {
				return true
			}

			// Everything past the context and the program is written here, in
			// source. An argument built from a start command would put an
			// operator's command line — API key and all — on a subprocess's
			// argv, which is the shell string this repository does not build.
			shells++
			for i, arg := range call.Args[min(2, len(call.Args)):] {
				if _, ok := arg.(*ast.BasicLit); !ok {
					t.Errorf("%s: %s passes argument %d of the login shell as %T rather than a literal; the only thing this daemon may ask that shell is what PATH it has",
						fset.Position(arg.Pos()), name, i+2, arg)
				}
			}
			return true
		})
	}

	if reached == 0 {
		t.Fatalf("nothing in this package reaches os/exec, so the dependency probe has gone and this test is checking an empty walk")
	}
	if shells != 1 {
		t.Errorf("this package starts %d subprocesses; there is one, and it is the login shell whose PATH a session's command is resolved against", shells)
	}
}

// TestTheLoginShellIsAskedNothingAboutTheCommand is the other half of the rule
// above, and it is a signature rather than a sweep: loginShellPATH takes no
// arguments, so there is nothing about a start command for it to pass on.
//
// The distinction matters because `sh -lc "command -v $binary"` would resolve
// exactly as correctly and would be a shell string built from configuration —
// forbidden outright by docs/security.md §2. Asking the shell for its PATH and
// resolving the name here in Go is what keeps that impossible rather than merely
// avoided, and a parameter added to this function is the first step back.
//
// Must fail when the probe starts taking the thing it is looking for.
func TestTheLoginShellIsAskedNothingAboutTheCommand(t *testing.T) {
	t.Parallel()

	const probe = "loginShellPATH"

	_, files := packageFiles(t)

	var found bool
	for _, file := range files {
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Name.Name != probe || fn.Recv != nil {
				continue
			}
			found = true
			if n := fn.Type.Params.NumFields(); n != 0 {
				t.Errorf("%s takes %d parameters; a probe that can be told what to look for can be told to look for it in a command line", probe, n)
			}
		}
	}

	if !found {
		t.Fatalf("this package declares no %s, so the login-shell probe has gone and #96 is back", probe)
	}
}
