// Package preflight checks, before the daemon binds or asks tmux anything,
// that the binaries it cannot work without are actually on the host.
//
// Without it the first thing an operator learns about a missing dependency is a
// 500 and a log line naming a binary they did not know was needed (#71) — from a
// daemon that looked healthy to systemd, to a status page, and to them, right up
// until that request. The failure is moved to startup, where it is the one thing
// an operator is already reading.
//
// Two dependencies, and they are not equally fatal. tmux is every session, so
// its absence is a refusal to start. The start command is what a session types
// once it exists, so its absence is a loud warning: the dashboard still renders,
// the reaper still sweeps the sessions a previous daemon left, and the operator
// still gets told.
package preflight

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strings"

	"github.com/nctiggy/claude-remote-session-webhook/internal/config"
)

// tmuxBinary is the one hard dependency. It is a constant rather than a
// parameter because it is not configuration: internal/tmuxctl builds every argv
// it runs around this name, so a daemon told to check for something else would
// be checking for a binary it never execs.
const tmuxBinary = "tmux"

// osReleasePath is where a Linux distribution identifies itself. ID_LIKE is the
// half that matters: it is what makes Mint answer "debian" and Rocky answer
// "rhel" without either being listed here.
const osReleasePath = "/etc/os-release"

// Host is everything a check asks about the machine it is running on, injected
// so the whole matrix — every distribution, and a host with no /etc/os-release
// at all — is reachable from a parallel test without touching the process.
type Host struct {
	// LookPath resolves a binary the way the daemon will when it comes to run
	// it, which is to say exec.LookPath and its rules: a name is searched along
	// PATH, and anything with a separator in it is checked where it points.
	LookPath func(name string) (string, error)

	// GOOS is runtime.GOOS. macOS is identified by this and never by
	// /etc/os-release, which it does not have.
	GOOS string

	// OSRelease reads osReleasePath. An error means "this host does not say",
	// which is the generic advice and never a startup failure — a daemon that
	// refused to start because it could not name a package manager would be
	// refusing over the wording of its own error message.
	OSRelease func() ([]byte, error)
}

// RealHost is the host the daemon is actually running on.
func RealHost() Host {
	return Host{
		LookPath:  exec.LookPath,
		GOOS:      runtime.GOOS,
		OSRelease: func() ([]byte, error) { return os.ReadFile(osReleasePath) },
	}
}

// Check verifies the real host, writing any warning to warn. It is the only
// form cmd/crswd needs.
func Check(cmds config.StartCommands, warn io.Writer) error {
	return CheckOn(RealHost(), cmds, warn)
}

// CheckOn is Check with the host injected.
//
// It takes the resolved command set rather than a binary name so that a daemon
// configured to run something other than the default is checked for what it was
// configured with (#38). Looking for a fixed name here would be wrong on such a
// daemon, and right for the wrong reason on every other.
func CheckOn(h Host, cmds config.StartCommands, warn io.Writer) error {
	if h.LookPath == nil {
		return errors.New("no way to look a binary up on this host; refusing to start")
	}
	if warn == nil {
		// Discarding would make the start-command warning silent, and a warning
		// nobody sees is the state this whole package exists to end.
		warn = os.Stderr
	}

	if _, err := h.LookPath(tmuxBinary); err != nil {
		return fmt.Errorf("%s is not on PATH and every session is a tmux session; %s; refusing to start",
			tmuxBinary, installAdvice(h, tmuxBinary))
	}

	for _, name := range cmds.Names() {
		command, ok := cmds.Command(name)
		if !ok {
			continue
		}
		binary := commandBinary(command)
		if binary == "" {
			continue
		}
		if _, err := h.LookPath(binary); err == nil {
			continue
		}
		if err := warnMissingStartCommand(warn, name, binary); err != nil {
			return err
		}
	}
	return nil
}

// commandBinary is the first word of a command line, which is what a shell will
// try to run.
//
// It is deliberately naive: a command line is typed at a shell, so `env FOO=bar
// agent` reports `env`, which exists, and the check passes. That is the honest
// answer — this package can say a binary is absent, and it does not pretend to
// know what a shell will do with a line the operator wrote.
func commandBinary(command string) string {
	fields := strings.Fields(command)
	if len(fields) == 0 {
		return ""
	}
	return fields[0]
}

// warnMissingStartCommand says which configured command cannot run, naming both
// the operator's name for it and the binary, because those are the two halves of
// the fix: one is where they wrote it, the other is what is missing.
//
// A write failure is fatal, for the reason config's own warnings are: the
// warning is the entire remedy for a non-fatal defect, and a daemon that
// swallowed it would serve a fleet whose every create fails while looking
// perfectly configured.
func warnMissingStartCommand(warn io.Writer, name, binary string) error {
	banner := fmt.Sprintf(
		"crswd: the %q start command runs %q, which is not on PATH — every session started with it will fail.\n"+
			"crswd: install it, or set %s. Starting anyway.\n",
		name, binary, config.EnvStartCommand)

	if _, err := io.WriteString(warn, banner); err != nil {
		return fmt.Errorf("emit the missing start command warning: %w", err)
	}
	return nil
}

// installAdvice is the second half of the refusal: what to type.
//
// It is advice and never an action. A daemon that installed a package would
// need privileges it has no other use for, and the operator may deliberately
// want a tmux this command would not give them.
func installAdvice(h Host, pkg string) string {
	if cmd := installCommand(h, pkg); cmd != "" {
		return fmt.Sprintf("install it with %q", cmd)
	}
	return "install it with your package manager"
}

// installCommand names the package manager, or returns empty when the host does
// not say which one it has.
func installCommand(h Host, pkg string) string {
	if h.GOOS == "darwin" {
		// Before /etc/os-release, which a Mac does not have — and which, if
		// something ever put one there, would still not describe how software is
		// installed on it.
		return "brew install " + pkg
	}

	id, idLike := osReleaseIDs(h)
	if format := managerFor(id); format != "" {
		return fmt.Sprintf(format, pkg)
	}
	// ID_LIKE is a priority list, so the first token that is recognised is the
	// one the distribution says it most resembles.
	for _, like := range strings.Fields(idLike) {
		if format := managerFor(like); format != "" {
			return fmt.Sprintf(format, pkg)
		}
	}
	return ""
}

// managerFor maps a distribution ID to its install command. The unlisted
// distributions are reached through ID_LIKE rather than by growing this table:
// Mint says debian, Rocky says rhel, and neither needs an entry.
func managerFor(id string) string {
	switch strings.ToLower(id) {
	case "debian", "ubuntu":
		return "sudo apt install %s"
	case "rhel", "fedora", "centos":
		return "sudo dnf install %s"
	case "arch":
		return "sudo pacman -S %s"
	case "alpine":
		return "sudo apk add %s"
	default:
		return ""
	}
}

// osReleaseIDs reads ID and ID_LIKE. An unreadable or absent file is two empty
// strings — the generic advice — because the only thing riding on this is the
// wording of a message that is already correct without it.
func osReleaseIDs(h Host) (id, idLike string) {
	if h.OSRelease == nil {
		return "", ""
	}
	data, err := h.OSRelease()
	if err != nil {
		return "", ""
	}
	for _, line := range strings.Split(string(data), "\n") {
		key, value, found := strings.Cut(strings.TrimSpace(line), "=")
		if !found {
			continue
		}
		// os-release values are shell-quoted: ID=ubuntu and ID_LIKE="rhel fedora"
		// are both normal, and the quotes are the shell's rather than part of the
		// value.
		value = strings.Trim(strings.TrimSpace(value), `"'`)
		switch strings.TrimSpace(key) {
		case "ID":
			id = value
		case "ID_LIKE":
			idLike = value
		}
	}
	return id, idLike
}
