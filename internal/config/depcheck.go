package config

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"
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

	// shellVariable and fallbackLoginShell are which shell gets asked what a
	// session's PATH will be. tmux picks its default-shell from $SHELL and falls
	// back to the passwd entry; this daemon cannot read the second without
	// leaving pure Go, so it asks the first and says so rather than guessing at
	// the rest. /bin/sh exists on every host that can run tmux at all, and a
	// login sh reads the same ~/.profile the failure in #96 came from.
	shellVariable      = "SHELL"
	fallbackLoginShell = "/bin/sh"

	// loginShellScript is the whole of what the shell is asked, and it is a
	// constant on purpose — see loginShellPATH. `\n` here is printf's, not Go's:
	// the two characters are the format string, and the newline after the
	// backtick is what ends the command.
	loginShellScript = `printf '%s\n' "$PATH"` + "\n"

	// loginShellTimeout bounds a profile this daemon does not own. Startup is
	// blocked on this probe — it runs before anything binds — so a .profile that
	// waits on a network mount would otherwise hold the unit in "activating"
	// with nothing said about why.
	loginShellTimeout = 5 * time.Second

	// loginShellWaitDelay bounds the *second* way that hangs: a profile that
	// backgrounds something holding the output pipe open keeps the read blocked
	// long after the shell itself is gone.
	loginShellWaitDelay = time.Second

	// checkedBothEnvironments and checkedThisDaemonOnly are FR-023c. "Not on
	// PATH" is a claim about an environment, and this daemon has two to choose
	// from — its own, and the one a session's command really runs in — so a
	// diagnostic that does not say which one it looked in cannot be told apart
	// from a probe looking in the wrong place. That is what #96 was.
	checkedBothEnvironments = "checked: this daemon's own PATH, and the PATH a login shell gives a session"
	checkedThisDaemonOnly   = "checked: this daemon's own PATH, and nothing else"
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
// Nothing here installs anything, and nothing here runs a probed binary. A
// daemon that installs software is a daemon that can be made to install software
// (FR-014), and one that *executed* what it found to see whether it works would
// be running an operator's command line at startup with no session to run it in.
// The one program this file starts is the operator's own login shell, asked what
// PATH it has and nothing else — loginShellPATH is where that is argued.
func (c Config) CheckDependencies(warn io.Writer) error {
	return c.checkDependencies(exec.LookPath, loginShellPATH, readOsRelease, warn)
}

// checkDependencies is CheckDependencies with the three things it may know about
// the host injected — what is on this process's PATH, what a session's login
// shell answers, and what the system says it is — so the tests can describe a
// Debian box with no tmux on it without being one.
//
// Injection rather than an empty PATH because PATH is one variable shared by
// every test in this binary: a case that cleared it could not run in parallel,
// and the suite it ran alone in would be one os.Setenv away from probing the
// real host instead of the described one.
func (c Config) checkDependencies(lookPath func(string) (string, error), loginPATH func() (string, error), osRelease func() []byte, warn io.Writer) error {
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

	// Asked at most once, and only after this daemon's own PATH has failed to
	// answer: the ask runs the operator's profile, and a host where every
	// command is present has no reason to be given that side effect.
	session := sessionPATH{ask: loginPATH}

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

		// This daemon's PATH does not have it, which by itself says nothing:
		// the command is typed into a login shell inside a tmux pane and that
		// is the environment it will be resolved in (#96). Ask that one before
		// telling an operator anything is wrong.
		list, err := session.get()
		switch {
		case err != nil:
			// FR-023c: the question could not be answered, so the answer is
			// what was checked — never an assertion of absence, which is the
			// sentence that trained an operator to ignore this check.
			if err := noteStartCommandUnresolved(warn, name, binary, c.startCommandVariable(name), c.FilePath, err); err != nil {
				return err
			}
		case lookInPATH(list, binary):
			// The session will find it. There is nothing wrong with this host.
		default:
			if err := warnStartCommandNotOnPath(warn, name, binary, c.startCommandVariable(name), c.FilePath); err != nil {
				return err
			}
		}
	}
	return nil
}

// sessionPATH is the PATH a session's command will be resolved against, asked
// for once per start and remembered — including the failure, so a host whose
// shell cannot be asked says so once per command rather than starting one shell
// per command to find that out again.
type sessionPATH struct {
	ask   func() (string, error)
	asked bool
	list  string
	err   error
}

func (s *sessionPATH) get() (string, error) {
	if !s.asked {
		s.asked = true
		s.list, s.err = s.ask()
	}
	return s.list, s.err
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

// loginShellPATH is the PATH a start command will really be resolved against.
//
// tmux starts a pane as a login shell and the command is typed into it
// (tmuxctl.Controller.New), so by the time the name is looked up the operator's
// profile has run. This daemon's own PATH comes from whatever started it — for
// the deployed unit, the systemd user manager, which reads no profile at all and
// therefore has no ~/.local/bin. Both environments answer correctly about
// themselves; #96 is the check asking the one that does not run the command.
//
// **The shell is asked what PATH it has, never about a command.** That is the
// difference between this and `sh -lc "command -v $binary"`, which docs/security.md
// §2 forbids outright: the script above is a constant, no configured value ever
// becomes part of a command line, and the resolution happens in Go against the
// list that comes back. Running the profile at all is the cost, and it is the one
// research R7 took deliberately — a check that resolves a command differently
// from the thing that runs it is not a check.
//
// Nothing is executed to see whether it works: printf is a shell builtin and the
// probed binary is not named to the shell, let alone run.
func loginShellPATH() (string, error) {
	shell := os.Getenv(shellVariable)
	if shell == "" {
		shell = fallbackLoginShell
	}

	ctx, cancel := context.WithTimeout(context.Background(), loginShellTimeout)
	defer cancel()

	//nolint:gosec // G702: the program is the operator's own $SHELL and the argv is a constant. Nothing configured, and nothing from a request, reaches it — TestNeverExecutesInstall holds that structurally.
	cmd := exec.CommandContext(ctx, shell, "-l")
	cmd.Stdin = strings.NewReader(loginShellScript)
	// Set rather than left nil, and that is a disclosure rule: with nil stderr
	// Output() would fold whatever the profile printed into the error below, and
	// this daemon writes that error into a journal that outlives the process
	// (FR-043). A .profile is free to print anything, a credential among it.
	cmd.Stderr = io.Discard
	cmd.WaitDelay = loginShellWaitDelay

	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("ask %s what PATH a session gets: %w", shell, err)
	}

	// The last line, because a profile may print a banner and this command runs
	// after it. A shell that answered with nothing has not answered.
	list := lastLine(string(out))
	if list == "" {
		return "", errors.New("the login shell reported no PATH at all")
	}
	return list, nil
}

// lastLine is the shell's answer out of everything its profile said on the way
// to it.
func lastLine(out string) string {
	lines := strings.Split(out, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if line := strings.TrimSpace(lines[i]); line != "" {
			return line
		}
	}
	return ""
}

// lookInPATH is exec.LookPath against a PATH this process does not have. The
// standard library reads the variable out of this daemon's own environment,
// which is the environment that is wrong about the answer.
func lookInPATH(list, binary string) bool {
	// A name with a separator in it is a path, and no shell searches PATH for
	// one. TestProbesFirstWordOnly's `/opt/claude/bin/claude` is that case.
	if strings.ContainsRune(binary, os.PathSeparator) {
		return isExecutable(binary)
	}
	for _, dir := range filepath.SplitList(list) {
		// An empty element means the working directory to a shell. This
		// daemon's working directory is not a session's, so it answers for
		// nothing here.
		if dir == "" {
			continue
		}
		if isExecutable(filepath.Join(dir, binary)) {
			return true
		}
	}
	return false
}

// isExecutable is the question exec.LookPath asks of a candidate: a regular file
// with an execute bit somewhere.
//
// Stat and not Lstat, because ~/.local/bin is mostly symlinks — the very
// directory #96 is about — and the session will follow them too.
func isExecutable(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	mode := info.Mode()
	return mode.IsRegular() && mode.Perm()&0o111 != 0
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
// wrote it, what was checked, and what it costs them — the four things the
// sentence "claude: not found" in a pane an hour later does not say.
//
// It is reached only once *both* environments have been asked. Before #96 this
// was the only sentence this check had, and it said "not on PATH" on the
// strength of the one environment the command does not run in.
//
// It names the *binary* and never the command line it was cut from. That is a
// disclosure rule and not a tidiness one: a command line is configuration an
// operator wrote, its arguments are theirs to fill with whatever the program
// takes — an API key among them — and this sentence is written into a journal
// that outlives the process (FR-043). The first word is the only part PATH
// could answer for anyway, so nothing is lost by quoting nothing else.
//
// A write failure is fatal, as it is for every other warning in this package: a
// weakened posture nobody could be told about is the state an operator must not
// be left to discover. That is a refusal about the sink and not about the
// dependency — a daemon whose stderr will not take a line has already lost the
// only way it had to report anything.
func warnStartCommandNotOnPath(warn io.Writer, name, binary, variable, path string) error {
	banner := fmt.Sprintf(
		"crswd: warning: the start command %q runs %q, which is not on PATH.\n"+
			"crswd: %s.\n"+
			"crswd: install it, or correct %s.\n"+
			"crswd: starting anyway; sessions using %q will fail until it is present.\n",
		name, binary, checkedBothEnvironments, whereConfigured(variable, path), name)

	if _, err := io.WriteString(warn, banner); err != nil {
		return fmt.Errorf("emit the warning that the %q start command is not installed: %w", name, err)
	}
	return nil
}

// noteStartCommandUnresolved is FR-023c: the sentence for a question this daemon
// could not answer. It is a *note* and not a warning because nothing here has
// found anything wrong — the command may well be there, and the shell that would
// have said so could not be asked.
//
// The distinction is the whole of #96. A daemon that says "missing" about a
// command that works teaches its operator that this check is noise, and the next
// time it is right about a real absence they will scroll past it. Saying what was
// checked costs a line and keeps the check worth reading.
//
// The reason is the exec error and never the shell's own output, which
// loginShellPATH discards for the disclosure reason argued there. It is included
// because a note an operator cannot act on is a note that becomes noise for a
// second time.
//
// Same disclosure rule as the warning above, and same fatality on a write
// failure, for the same reasons.
func noteStartCommandUnresolved(warn io.Writer, name, binary, variable, path string, reason error) error {
	banner := fmt.Sprintf(
		"crswd: note: the start command %q runs %q, which this daemon cannot see on its own PATH.\n"+
			"crswd: sessions run it through a login shell, which may still find it — that shell could not be asked: %v.\n"+
			"crswd: %s. Correct %s if %q really is missing.\n",
		name, binary, reason, checkedThisDaemonOnly, whereConfigured(variable, path), binary)

	if _, err := io.WriteString(warn, banner); err != nil {
		return fmt.Errorf("emit the note that the %q start command could not be resolved: %w", name, err)
	}
	return nil
}

// whereConfigured names the configuration file when there was one and the
// environment variable when there was not, because those are the two places the
// value can have come from and an operator who edited the other one is the
// person these messages exist for.
func whereConfigured(variable, path string) string {
	if path != "" {
		return fmt.Sprintf("%s in %s", KeyForVar(variable), path)
	}
	return fmt.Sprintf("%s in this daemon's environment", variable)
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
