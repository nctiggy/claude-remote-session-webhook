package config

import (
	"fmt"
	"io"
	"os/exec"
	"strings"
)

// tmuxDependency is the one tool this daemon cannot do anything at all without:
// every session it manages is a window on a tmux server it starts itself.
const tmuxDependency = "tmux"

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
	return c.checkDependencies(exec.LookPath, warn)
}

// checkDependencies is CheckDependencies with the PATH probe injected, so the
// tests can describe a host that has no tmux without emptying the process's own
// PATH — which is global, and which every parallel test in this package would
// then be racing.
func (c Config) checkDependencies(lookPath func(string) (string, error), warn io.Writer) error {
	if _, err := lookPath(tmuxDependency); err != nil {
		// The error is not wrapped: exec.LookPath's carries the whole of PATH,
		// which is the operator's environment rather than anything they asked to
		// have written to their journal, and it says nothing this message does
		// not.
		return fmt.Errorf("%s is not installed, and this daemon cannot manage a session without it; refusing to start", tmuxDependency)
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
