package config

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"slices"
	"strings"
)

const (
	// tmuxDependency is the one tool this daemon cannot do anything at all
	// without: every session it manages is a window on a tmux server it starts
	// itself.
	tmuxDependency = "tmux"

	// osReleasePath is where a Linux host writes down what it is. It is the
	// system's own identification rather than this daemon's opinion of it
	// (FR-013), which is the whole difference between a derived install command
	// and a guessed one.
	osReleasePath = "/etc/os-release"

	// maxOsReleaseBytes bounds the read. The real file is a few hundred bytes on
	// every distribution that ships one; anything past this is not an
	// identification and there is no reason to hold it in memory to find that
	// out.
	maxOsReleaseBytes = 64 << 10

	// genericInstallAdvice is what an unrecognised platform is told. It names no
	// package manager on purpose: naming apt on a host that has never had it
	// sends the operator to a command that fails, which is worse than sending
	// them to their own documentation.
	genericInstallAdvice = "install tmux using your platform's package manager"
)

// CheckDependencies probes the tools this daemon shells out to, at startup and
// before anything binds (FR-012).
//
// Two dependencies, and they fail differently on purpose
// (contracts/dependency-check.md). Without tmux there is nothing this daemon can
// do, so starting would only defer the failure to the operator's first request —
// a create that fails at the moment they are trying to use it, rather than a
// refusal they read the second they restarted the unit. Without a start
// command's binary it can still serve the dashboard, adopt the sessions already
// on the host and say what is wrong, so a refusal there would take away the one
// thing that could tell them.
//
// The second probe reads the *configured* commands rather than a fixed name
// (FR-015). A daemon pointed at something other than Claude is checked for that
// something; a check that hardcoded `claude` would warn about a binary this
// daemon will never type and stay silent about the one it will.
//
// Nothing here installs anything, and nothing here runs a probed binary: this
// file's only contact with the host is exec.LookPath, which reads PATH and
// stats. A daemon that installs software is a daemon that can be made to install
// software (FR-014), and one that *executed* what it found to see whether it
// works would be running an operator's command line at startup with no session
// to run it in.
func (c Config) CheckDependencies(warn io.Writer) error {
	return c.checkDependencies(exec.LookPath, readOsRelease, warn)
}

// checkDependencies is CheckDependencies with the two things it may know about
// the host injected — what is on PATH, and what the system says it is — so the
// tests can describe a Debian box with no tmux on it without being one.
//
// Injection rather than an empty PATH because PATH is one variable shared by
// every test in this binary: a case that cleared it could not run in parallel,
// and the suite it ran alone in would be one os.Setenv away from probing the
// real host instead of the described one.
func (c Config) checkDependencies(lookPath func(string) (string, error), osRelease func() []byte, warn io.Writer) error {
	if _, err := lookPath(tmuxDependency); err != nil {
		// The error is not wrapped: exec.LookPath's carries the whole of PATH,
		// which is the operator's environment rather than anything they asked to
		// have written to their journal, and it says nothing this message does
		// not.
		//
		// The install command is in the refusal rather than in the documentation
		// because this sentence is read by an operator whose unit has just failed
		// to start, and the next thing they need is the line to type.
		return fmt.Errorf("%s is not installed, and this daemon cannot manage a session without it; %s; refusing to start",
			tmuxDependency, installAdvice(osRelease(), runtime.GOOS))
	}

	// Names() rather than a map walk, so two starts on one host emit the same
	// warnings in the same order and an operator comparing two journals is
	// reading a difference that is really there.
	for _, name := range c.StartCommands.Names() {
		command, ok := c.StartCommands.Command(name)
		if !ok {
			continue
		}
		binary := commandBinary(command)
		if binary == "" {
			continue
		}
		if _, err := lookPath(binary); err == nil {
			continue
		}
		if err := warnStartCommandNotOnPath(warn, name, binary, c.startCommandVariable(name), c.FilePath); err != nil {
			return err
		}
	}
	return nil
}

// commandBinary is the first word of a start command, which is the only part of
// it PATH can answer for.
//
// The rest is arguments. Handing the whole line to LookPath would look for a
// file called "claude --dangerously-skip-permissions", find nothing, and warn
// about every start command this daemon has — a check that is wrong about a
// correctly configured host teaches an operator to ignore it.
func commandBinary(command string) string {
	fields := strings.Fields(command)
	if len(fields) == 0 {
		return ""
	}
	return fields[0]
}

// startCommandVariable is which setting an operator corrects to fix this
// command. The default's command line is what EnvStartCommand carries; every
// other name exists only because EnvStartCommands named it.
func (c Config) startCommandVariable(name string) string {
	if name == DefaultStartCommandName {
		return EnvStartCommand
	}
	return EnvStartCommands
}

// warnStartCommandNotOnPath says which command is missing, where the operator
// wrote it, and what it costs them — the three things the sentence "claude: not
// found" in a pane an hour later does not say.
//
// It names the configuration file when there was one and the environment
// variable when there was not, because those are the two places the value can
// have come from and an operator who edited the other one is the person this
// warning exists for.
//
// A write failure is fatal, as it is for every other warning in this package: a
// weakened posture nobody could be told about is the state an operator must not
// be left to discover. That is a refusal about the sink and not about the
// dependency — a daemon whose stderr will not take a line has already lost the
// only way it had to report anything.
func warnStartCommandNotOnPath(warn io.Writer, name, binary, variable, path string) error {
	where := fmt.Sprintf("%s in this daemon's environment", variable)
	if path != "" {
		where = fmt.Sprintf("%s in %s", KeyForVar(variable), path)
	}

	banner := fmt.Sprintf(
		"crswd: warning: the start command %q runs %q, which is not on PATH.\n"+
			"crswd: install it, or correct %s.\n"+
			"crswd: starting anyway; sessions using %q will fail until it is present.\n",
		name, binary, where, name)

	if _, err := io.WriteString(warn, banner); err != nil {
		return fmt.Errorf("emit the warning that the %q start command is not installed: %w", name, err)
	}
	return nil
}

// readOsRelease is what this host says it is, or nil when it will not say.
//
// Every failure reads the same as an unidentified platform, deliberately, and it
// is the one place in this package where an error is dropped rather than
// returned. This is consulted from inside a refusal that is already fatal about
// tmux; a second error about a file the operator never wrote would bury the
// sentence they actually need. The whole cost of nil is the generic sentence,
// which is the honest answer for a host that did not answer.
func readOsRelease() []byte {
	handle, err := os.Open(osReleasePath)
	if err != nil {
		return nil
	}
	defer func() { _ = handle.Close() }() //nolint:errcheck // read-only handle: a failed close cannot lose data.

	data, err := io.ReadAll(io.LimitReader(handle, maxOsReleaseBytes))
	if err != nil {
		return nil
	}
	return data
}

// installAdvice is the clause the tmux refusal carries: the command for this
// platform when the platform identified itself, and a sentence naming no
// package manager when it did not.
func installAdvice(osRelease []byte, goos string) string {
	if command := installTmuxCommand(osRelease, goos); command != "" {
		return "install it with: " + command
	}
	return genericInstallAdvice
}

// installTmuxCommand derives the install command from the host's own
// identification, or returns "" for a platform it does not recognise
// (contracts/dependency-check.md).
//
// Nothing here is inferred from GOOS on Linux, which is the requirement rather
// than a style: every Linux distribution compiles to the same GOOS, so a command
// chosen from it alone would name apt on Alpine — a confidently wrong line the
// operator will type. The empty answer exists so that an unrecognised host gets
// told nothing rather than told something false.
//
// The two families are matched differently because their files identify
// themselves differently: Debian names itself in ID and Ubuntu inherits it
// through ID_LIKE, while a RHEL-family host is recognised by what it declares
// itself like. Both come out of the file; neither comes out of this daemon.
//
// The consequence is the contract's, and is deliberate rather than missed:
// Fedora itself sets ID=fedora and ships no ID_LIKE at all, so it falls to the
// generic sentence. That is the safe direction of this table — it costs one line
// of convenience on one distribution, where the other direction costs a wrong
// command on every host a widened rule caught by accident.
func installTmuxCommand(osRelease []byte, goos string) string {
	id := osReleaseField(osRelease, "ID")
	like := strings.Fields(osReleaseField(osRelease, "ID_LIKE"))

	switch {
	case id == "debian" || slices.Contains(like, "debian"):
		return "sudo apt install tmux"
	case slices.Contains(like, "rhel") || slices.Contains(like, "fedora"):
		return "sudo dnf install tmux"
	case id == "arch":
		return "sudo pacman -S tmux"
	case id == "alpine":
		return "sudo apk add tmux"
	case goos == "darwin":
		// Last, and only after the file has said nothing: macOS ships no
		// /etc/os-release, so reaching here is the absence of an identification
		// rather than a guess made in spite of one.
		return "brew install tmux"
	}
	return ""
}

// osReleaseField reads one field out of an os-release file.
//
// The format is shell assignments, and this reads the subset of it that the two
// fields above are ever written in: whole lines, one `KEY=value`, the value
// optionally quoted. It is not a shell parser and must not become one — nothing
// here expands, substitutes, or executes anything, and the file it is reading is
// the one place on the host where a value arrives from outside this daemon's
// configuration.
//
// An unparseable line is skipped rather than refused. This file is not the
// operator's to correct, and a daemon that would not tell them how to install
// tmux because a distribution wrote a line it did not expect has traded the
// answer for a complaint.
func osReleaseField(osRelease []byte, key string) string {
	for _, line := range strings.Split(string(osRelease), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		name, value, ok := strings.Cut(line, "=")
		if !ok || strings.TrimSpace(name) != key {
			continue
		}
		return unquoteOsReleaseValue(strings.TrimSpace(value))
	}
	return ""
}

// unquoteOsReleaseValue strips the quotes a distribution may have written around
// a value. ID_LIKE is a list and is quoted on most hosts that set it; ID usually
// is not, and Debian and RHEL disagree about that on the same field.
func unquoteOsReleaseValue(value string) string {
	if len(value) < 2 {
		return value
	}
	quote := value[0]
	if (quote == '"' || quote == '\'') && value[len(value)-1] == quote {
		return value[1 : len(value)-1]
	}
	return value
}
